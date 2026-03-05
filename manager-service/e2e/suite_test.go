//go:build e2e

// Package e2e contains end-to-end tests for the Sandbox Manager.
//
// Prerequisites:
//
//	kubectl-accessible Kubernetes cluster (kind or real)
//	Manager binary built at ../bin/manager  (run `make build` first)
//	Cleaner binary built at ../bin/cleaner
//
// Environment variables (all optional, see defaults below):
//
//	E2E_MANAGER_URL        URL of a pre-running manager (skip auto-start if set)
//	E2E_MANAGER_BIN        Path to manager binary             (default: ../bin/manager)
//	E2E_CLEANER_BIN        Path to cleaner binary             (default: ../bin/cleaner)
//	E2E_SERVICE_KEY        Service key for auth               (default: e2e-test-key)
//	E2E_NAMESPACE          Workload K8s namespace             (default: sandbox-workloads)
//	E2E_PVC_NAME           JuiceFS / workspace PVC name       (default: juicefs-workloads-pvc)
//	E2E_WORKSPACE_PATH     Manager workspace base path        (default: /tmp/e2e-workspace)
//	E2E_IMAGE              Container image for workloads      (default: ubuntu:22.04)
//	E2E_JUICEFS            "true" → enable file-persistence tests that require JuiceFS
//	E2E_MANAGER_PORT       TCP port when auto-starting manager (default: 18080)
//	KUBECONFIG             Standard kubeconfig path
//
// Run:
//
//	make test-e2e-go
//	# or directly:
//	go test -v -tags e2e -timeout 15m ./e2e/...
package e2e_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// ---------------------------------------------------------------------------
// Global suite state – set in TestMain, read-only in tests
// ---------------------------------------------------------------------------

var (
	suite     *SuiteConfig
	k8sCli    *kubernetes.Clientset
	idCounter uint64 // atomic counter for unique test IDs
)

// SuiteConfig holds the E2E test configuration resolved from env vars.
type SuiteConfig struct {
	ManagerURL     string
	ServiceKey     string
	Namespace      string
	PVCName        string
	WorkspacePath  string
	Image          string
	CleanerBin     string
	ManagerBin     string
	JuiceFSEnabled bool // true → file-persistence tests are enabled

	managerCmd *exec.Cmd // non-nil when we started the manager
	managerLog *os.File
}

// ---------------------------------------------------------------------------
// TestMain – suite-level setup and teardown
// ---------------------------------------------------------------------------

func TestMain(m *testing.M) {
	c := &SuiteConfig{
		ManagerURL:     envOr("E2E_MANAGER_URL", ""),
		ServiceKey:     envOr("E2E_SERVICE_KEY", "e2e-test-key"),
		Namespace:      envOr("E2E_NAMESPACE", "sandbox-workloads"),
		PVCName:        envOr("E2E_PVC_NAME", "juicefs-workloads-pvc"),
		WorkspacePath:  envOr("E2E_WORKSPACE_PATH", "/tmp/e2e-workspace"),
		Image:          envOr("E2E_IMAGE", "ubuntu:22.04"),
		CleanerBin:     absPath(envOr("E2E_CLEANER_BIN", "../bin/cleaner")),
		ManagerBin:     absPath(envOr("E2E_MANAGER_BIN", "../bin/manager")),
		JuiceFSEnabled: os.Getenv("E2E_JUICEFS") == "true",
	}
	suite = c

	// ── Kubernetes client ──────────────────────────────────────────────────
	cli, err := buildK8sClient()
	if err != nil {
		log.Fatalf("E2E: cannot connect to Kubernetes: %v\n  hint: set KUBECONFIG or start a cluster", err)
	}
	k8sCli = cli

	// ── Namespace ──────────────────────────────────────────────────────────
	ensureNamespace(cli, c.Namespace)

	// ── Workspace directory ────────────────────────────────────────────────
	if err := os.MkdirAll(c.WorkspacePath, 0755); err != nil {
		log.Fatalf("E2E: failed to create workspace path %s: %v", c.WorkspacePath, err)
	}

	// ── Manager ────────────────────────────────────────────────────────────
	if c.ManagerURL == "" {
		if _, err := os.Stat(c.ManagerBin); err != nil {
			log.Fatalf("E2E: manager binary not found at %s\n  hint: run `make build` first", c.ManagerBin)
		}
		url, err := startManager(c)
		if err != nil {
			c.stopManager()
			log.Fatalf("E2E: failed to start manager: %v", err)
		}
		c.ManagerURL = url
	} else {
		log.Printf("E2E: using existing manager at %s", c.ManagerURL)
	}

	// ── Run tests ──────────────────────────────────────────────────────────
	code := m.Run()

	// ── Teardown ───────────────────────────────────────────────────────────
	c.stopManager()
	purgeTestPods(cli, c.Namespace)

	os.Exit(code)
}

// ---------------------------------------------------------------------------
// Manager lifecycle
// ---------------------------------------------------------------------------

func startManager(c *SuiteConfig) (string, error) {
	portStr := envOr("E2E_MANAGER_PORT", "18080")
	portNum, err := strconv.Atoi(portStr)
	if err != nil || portNum < 1 || portNum > 65535 {
		return "", fmt.Errorf("invalid E2E_MANAGER_PORT %q: must be 1-65535", portStr)
	}
	url := "http://localhost:" + portStr

	// Write config with httpPort as integer so YAML unmarshal never fails (root cause: string "18080" -> config parse error).
	cfgPath := filepath.Join(os.TempDir(), "e2e-manager-config.yaml")
	cfgContent := fmt.Sprintf(`version: 1
server:
  httpPort: %d
  requestIdHeader: X-Request-Id
  timeouts:
    readHeader: 5s
    read: 120s
    write: 120s
    idle: 120s
  maxHeaderBytes: 1048576
  metrics:
    enabled: true
    path: /metrics
auth:
  headerName: X-Service-Key
kubernetes:
  qps: 50
  burst: 100
  requestTimeout: 15s
`, portNum)
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0644); err != nil {
		return "", fmt.Errorf("write config: %w", err)
	}

	logFile, err := os.CreateTemp("", "e2e-manager-*.log")
	if err != nil {
		return "", fmt.Errorf("create log file: %w", err)
	}
	c.managerLog = logFile

	// Pass KUBECONFIG explicitly so manager uses same cluster as test (avoids silent wrong-cluster or in-cluster config).
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		if home := os.Getenv("HOME"); home != "" {
			kubeconfig = filepath.Join(home, ".kube", "config")
		}
	}

	cmd := exec.Command(c.ManagerBin)
	cmd.Env = append(os.Environ(),
		"CONFIG_PATH="+cfgPath,
		"SERVICE_KEYS="+c.ServiceKey,
		"JUICEFS_BASE_PATH="+c.WorkspacePath,
		"JUICEFS_PVC_NAME="+c.PVCName,
		"K8S_NAMESPACE="+c.Namespace,
		"LOG_LEVEL=info",
	)
	if kubeconfig != "" {
		cmd.Env = append(cmd.Env, "KUBECONFIG="+kubeconfig)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("exec manager: %w", err)
	}
	c.managerCmd = cmd
	log.Printf("E2E: started manager pid=%d log=%s", cmd.Process.Pid, logFile.Name())

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				log.Printf("E2E: manager ready at %s", url)
				return url, nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	// Include last lines of manager log so failures (e.g. CheckReady, config) are diagnosable.
	logSnippet := tailFile(logFile.Name(), 30)
	return "", fmt.Errorf("manager did not become healthy within 30s (log: %s)\n--- last 30 lines ---\n%s",
		logFile.Name(), logSnippet)
}

// tailFile returns the last n lines of the file (for error messages).
func tailFile(path string, n int) string {
	f, err := os.Open(path)
	if err != nil {
		return err.Error()
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	return strings.Join(lines, "\n")
}

func (c *SuiteConfig) stopManager() {
	if c.managerCmd != nil {
		_ = c.managerCmd.Process.Kill()
		_ = c.managerCmd.Wait()
		c.managerCmd = nil
	}
	if c.managerLog != nil {
		_ = c.managerLog.Close()
	}
}

// ---------------------------------------------------------------------------
// Kubernetes helpers
// ---------------------------------------------------------------------------

func buildK8sClient() (*kubernetes.Clientset, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	cfg := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{})
	restCfg, err := cfg.ClientConfig()
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(restCfg)
}

func ensureNamespace(cli *kubernetes.Clientset, ns string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := cli.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{}); err == nil {
		return
	}
	_, err := cli.CoreV1().Namespaces().Create(ctx, &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: ns},
	}, metav1.CreateOptions{})
	if err != nil {
		log.Printf("E2E: warning – could not create namespace %s: %v", ns, err)
	}
}

// purgeTestPods deletes all pods bearing the "e2e=true" label in ns.
func purgeTestPods(cli *kubernetes.Clientset, ns string) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_ = cli.CoreV1().Pods(ns).DeleteCollection(ctx,
		metav1.DeleteOptions{},
		metav1.ListOptions{LabelSelector: "e2e=true"},
	)
}

// waitPodPhase polls until the named pod reaches the given phase.
func waitPodPhase(t *testing.T, ns, podName string, phase v1.PodPhase, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pod, err := k8sCli.CoreV1().Pods(ns).Get(context.Background(), podName, metav1.GetOptions{})
		if err == nil && pod.Status.Phase == phase {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("pod %s/%s did not reach phase %s within %s", ns, podName, phase, timeout)
}

// waitPodExists polls until the named pod appears in K8s (any phase).
// Use this instead of waitPodPhase(..., Pending) when the image is pre-pulled
// and pods may transition through Pending too quickly to observe.
func waitPodExists(t *testing.T, ns, podName string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, err := k8sCli.CoreV1().Pods(ns).Get(context.Background(), podName, metav1.GetOptions{})
		if err == nil {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("pod %s/%s did not appear within %s", ns, podName, timeout)
}

// waitPodGone polls until the named pod no longer exists.
func waitPodGone(t *testing.T, ns, podName string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, err := k8sCli.CoreV1().Pods(ns).Get(context.Background(), podName, metav1.GetOptions{})
		if err != nil {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("pod %s/%s still exists after %s", ns, podName, timeout)
}

// podAnnotations returns the annotations map of a pod, or nil if not found.
func podAnnotations(t *testing.T, ns, podName string) map[string]string {
	t.Helper()
	pod, err := k8sCli.CoreV1().Pods(ns).Get(context.Background(), podName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pod %s/%s: %v", ns, podName, err)
	}
	return pod.Annotations
}

// sweepExpiredWorkloads mimics the cleaner CronJob: finds workload pods whose
// expires_at annotation is in the past and deletes them. Returns count deleted.
func sweepExpiredWorkloads(t *testing.T, ns string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pods, err := k8sCli.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
		LabelSelector: "app=managed-workload",
	})
	if err != nil {
		t.Logf("sweepExpiredWorkloads: list error: %v", err)
		return 0
	}

	now := time.Now()
	deleted := 0
	for _, pod := range pods.Items {
		expiresStr, ok := pod.Annotations["expires_at"]
		if !ok {
			continue
		}
		exp, err := time.Parse(time.RFC3339, expiresStr)
		if err != nil || !now.After(exp) {
			continue
		}
		t.Logf("GC: deleting expired pod %s (expired %s ago)", pod.Name, now.Sub(exp).Round(time.Second))
		grace := int64(0)
		_ = k8sCli.CoreV1().Pods(ns).Delete(ctx, pod.Name, metav1.DeleteOptions{GracePeriodSeconds: &grace})
		deleted++
	}
	return deleted
}

// patchPodExpiry sets a pod's expires_at annotation to a specific time via the K8s API.
func patchPodExpiry(t *testing.T, ns, podName string, expiresAt time.Time) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	patch := []byte(fmt.Sprintf(
		`{"metadata":{"annotations":{"expires_at":%q}}}`,
		expiresAt.UTC().Format(time.RFC3339),
	))
	_, err := k8sCli.CoreV1().Pods(ns).Patch(ctx, podName,
		"application/merge-patch+json", patch, metav1.PatchOptions{})
	if err != nil {
		t.Fatalf("patch pod expiry %s/%s: %v", ns, podName, err)
	}
}

// ---------------------------------------------------------------------------
// Utility helpers
// ---------------------------------------------------------------------------

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func absPath(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return a
}

// uniqueID returns a K8s-safe unique workload ID for use in a single test.
func uniqueID(prefix string) string {
	n := atomic.AddUint64(&idCounter, 1)
	return fmt.Sprintf("%s-%d", prefix, n)
}

// jsonBody encodes v as JSON and returns an io.Reader.
func jsonBody(v interface{}) io.Reader {
	b, _ := json.Marshal(v)
	return bytes.NewReader(b)
}
