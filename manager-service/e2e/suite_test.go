//go:build e2e

// Package e2e contains end-to-end tests for ASBCP.
//
// Prerequisites:
//
//	kubectl-accessible Kubernetes cluster (kind or real)
//	ASBCP binary built at ../bin/asbcp  (run `make build` first)
//
// Environment variables (all optional, see defaults below):
//
//	E2E_ASBCP_URL          URL of a pre-running ASBCP (skip auto-start if set)
//	E2E_ASBCP_BIN          Path to ASBCP binary               (default: ../bin/asbcp)
//	E2E_SERVICE_KEY        Service key for auth               (default: e2e-test-key)
//	E2E_NAMESPACE          Workload K8s namespace             (default: sandbox-workloads)
//	E2E_AFSCP_INTERNAL_BASE_URL AFSCP test double or real internal API base URL
//	E2E_AFSCP_ORCHESTRATOR_TOKEN ASBCP AFSCP token            (default: e2e-afscp-token)
//	E2E_AFSCP_NAMESPACE_ID AFSCP namespace id                 (default: ns_e2e)
//	E2E_AFSCP_SECRET_NAMESPACE K8s namespace for plan secret refs (default: afscp-mounts)
//	E2E_STORAGE_CAPACITY   Binding storage capacity           (default: 1Pi)
//	E2E_STORAGE_CLASS      Binding storage class              (default: "")
//	E2E_IMAGE              Container image for workloads      (default: ubuntu:22.04)
//	E2E_JUICEFS            "true" → enable file-persistence tests that require JuiceFS
//	E2E_ASBCP_PORT         TCP port when auto-starting ASBCP   (default: 18080)
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
	"net/http/httptest"
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
	ASBCPURL         string
	ServiceKey       string
	Namespace        string
	AFSCPBaseURL     string
	AFSCPToken       string
	AFSCPNamespaceID string
	AFSCPSecretNS    string
	StorageCapacity  string
	StorageClassName string
	Image            string
	ASBCPBin         string
	JuiceFSEnabled   bool // true → file-persistence tests are enabled

	asbcpCmd *exec.Cmd // non-nil when we started ASBCP
	asbcpLog *os.File
	afscpSrv *httptest.Server
}

// ---------------------------------------------------------------------------
// TestMain – suite-level setup and teardown
// ---------------------------------------------------------------------------

func TestMain(m *testing.M) {
	c := &SuiteConfig{
		ASBCPURL:         envOr("E2E_ASBCP_URL", ""),
		ServiceKey:       envOr("E2E_SERVICE_KEY", "e2e-test-key"),
		Namespace:        envOr("E2E_NAMESPACE", "sandbox-workloads"),
		AFSCPBaseURL:     envOr("E2E_AFSCP_INTERNAL_BASE_URL", ""),
		AFSCPToken:       envOr("E2E_AFSCP_ORCHESTRATOR_TOKEN", "e2e-afscp-token"),
		AFSCPNamespaceID: envOr("E2E_AFSCP_NAMESPACE_ID", "ns_e2e"),
		AFSCPSecretNS:    envOr("E2E_AFSCP_SECRET_NAMESPACE", "afscp-mounts"),
		StorageCapacity:  envOr("E2E_STORAGE_CAPACITY", "1Pi"),
		StorageClassName: envOr("E2E_STORAGE_CLASS", ""),
		Image:            envOr("E2E_IMAGE", "ubuntu:22.04"),
		ASBCPBin:         absPath(envOr("E2E_ASBCP_BIN", "../bin/asbcp")),
		JuiceFSEnabled:   os.Getenv("E2E_JUICEFS") == "true",
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

	// ── ASBCP ──────────────────────────────────────────────────────────────
	if c.ASBCPURL == "" {
		if c.AFSCPBaseURL == "" {
			startFakeAFSCP(c)
		}
		if _, err := os.Stat(c.ASBCPBin); err != nil {
			log.Fatalf("E2E: ASBCP binary not found at %s\n  hint: run `make build` first", c.ASBCPBin)
		}
		url, err := startASBCP(c)
		if err != nil {
			c.stopASBCP()
			log.Fatalf("E2E: failed to start ASBCP: %v", err)
		}
		c.ASBCPURL = url
	} else {
		log.Printf("E2E: using existing ASBCP at %s", c.ASBCPURL)
	}

	// ── Run tests ──────────────────────────────────────────────────────────
	code := m.Run()

	// ── Teardown ───────────────────────────────────────────────────────────
	purgeTestWorkloads(cli, c.Namespace)
	c.stopASBCP()
	c.stopAFSCP()

	os.Exit(code)
}

// ---------------------------------------------------------------------------
// ASBCP lifecycle
// ---------------------------------------------------------------------------

func startASBCP(c *SuiteConfig) (string, error) {
	portStr := envOr("E2E_ASBCP_PORT", "18080")
	portNum, err := strconv.Atoi(portStr)
	if err != nil || portNum < 1 || portNum > 65535 {
		return "", fmt.Errorf("invalid E2E_ASBCP_PORT %q: must be 1-65535", portStr)
	}
	url := "http://localhost:" + portStr

	// Write config with httpPort as integer so YAML unmarshal never fails (root cause: string "18080" -> config parse error).
	cfgPath := filepath.Join(os.TempDir(), "e2e-asbcp-config.yaml")
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

	logFile, err := os.CreateTemp("", "e2e-asbcp-*.log")
	if err != nil {
		return "", fmt.Errorf("create log file: %w", err)
	}
	c.asbcpLog = logFile

	// Pass KUBECONFIG explicitly so ASBCP uses same cluster as test (avoids silent wrong-cluster or in-cluster config).
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		if home := os.Getenv("HOME"); home != "" {
			kubeconfig = filepath.Join(home, ".kube", "config")
		}
	}

	cmd := exec.Command(c.ASBCPBin)
	cmd.Env = append(os.Environ(),
		"ASBCP_CONFIG_PATH="+cfgPath,
		"ASBCP_SERVICE_KEYS="+c.ServiceKey,
		"ASBCP_WORKLOAD_NAMESPACE="+c.Namespace,
		"ASBCP_AFSCP_INTERNAL_BASE_URL="+c.AFSCPBaseURL,
		"ASBCP_AFSCP_ORCHESTRATOR_TOKEN="+c.AFSCPToken,
		"ASBCP_AFSCP_CALLER_SERVICE=agentsmith-sandbox-control-plane",
		"ASBCP_AFSCP_ACTOR_ID=agentsmith-sandbox-control-plane",
		"ASBCP_JUICEFS_CSI_DRIVER="+envOr("E2E_CSI_DRIVER", "csi.juicefs.com"),
		"ASBCP_JUICEFS_STORAGE_CAPACITY="+c.StorageCapacity,
		"ASBCP_JUICEFS_STORAGE_CLASS_NAME="+c.StorageClassName,
		"LOG_LEVEL=info",
	)
	if kubeconfig != "" {
		cmd.Env = append(cmd.Env, "KUBECONFIG="+kubeconfig)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("exec ASBCP: %w", err)
	}
	c.asbcpCmd = cmd
	log.Printf("E2E: started ASBCP pid=%d log=%s", cmd.Process.Pid, logFile.Name())

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				log.Printf("E2E: ASBCP ready at %s", url)
				return url, nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	// Include last lines of ASBCP log so failures (e.g. CheckReady, config) are diagnosable.
	logSnippet := tailFile(logFile.Name(), 30)
	return "", fmt.Errorf("ASBCP did not become healthy within 30s (log: %s)\n--- last 30 lines ---\n%s",
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

func (c *SuiteConfig) stopASBCP() {
	if c.asbcpCmd != nil {
		_ = c.asbcpCmd.Process.Kill()
		_ = c.asbcpCmd.Wait()
		c.asbcpCmd = nil
	}
	if c.asbcpLog != nil {
		_ = c.asbcpLog.Close()
	}
}

func startFakeAFSCP(c *SuiteConfig) {
	c.afscpSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Header.Get("Authorization"), "Bearer "+c.AFSCPToken; got != want {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		namespaceID := strings.TrimSpace(r.Header.Get("X-AFSCP-Namespace-Id"))
		if namespaceID == "" {
			http.Error(w, "missing namespace", http.StatusBadRequest)
			return
		}

		const prefix = "/internal/v1/workload-mount-bindings/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			http.NotFound(w, r)
			return
		}
		suffix := strings.TrimPrefix(r.URL.Path, prefix)
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodGet && strings.HasSuffix(suffix, "/orchestrator-plan") {
			mountBindingID := strings.TrimSuffix(suffix, "/orchestrator-plan")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"mount_binding_id":      mountBindingID,
				"volume_id":             "vol_" + mountBindingID,
				"payload_volume_subdir": "afscp/" + namespaceID + "/bindings/" + mountBindingID + "/payload",
				"mount_path":            taskHomePath(strings.TrimPrefix(mountBindingID, "wmb_")),
				"read_only":             false,
				"secret_ref": map[string]string{
					"namespace": c.AFSCPSecretNS,
					"name":      "juicefs-" + dnsLabel(mountBindingID),
				},
				"security_policy": map[string]bool{
					"run_as_non_root":             true,
					"allow_privileged":            false,
					"jvs_control_outside_payload": true,
				},
			})
			return
		}

		if r.Method == http.MethodPost || r.Method == http.MethodPatch {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"operation_id":    "op_e2e",
				"operation_state": "succeeded",
			})
			return
		}

		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}))
	c.AFSCPBaseURL = c.afscpSrv.URL
	log.Printf("E2E: started AFSCP test double at %s", c.AFSCPBaseURL)
}

func (c *SuiteConfig) stopAFSCP() {
	if c.afscpSrv != nil {
		c.afscpSrv.Close()
		c.afscpSrv = nil
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

func purgeTestWorkloads(cli *kubernetes.Clientset, ns string) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pods, err := cli.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: "app=managed-workload"})
	if err != nil {
		log.Printf("E2E: warning – could not list workloads for cleanup: %v", err)
		return
	}
	client := &http.Client{Timeout: 20 * time.Second}
	for _, pod := range pods.Items {
		wsID := strings.TrimSpace(pod.Labels["workspace_id"])
		projID := strings.TrimSpace(pod.Labels["project_id"])
		wlID := strings.TrimSpace(pod.Labels["workload_id"])
		if wsID == "" || projID == "" || wlID == "" {
			continue
		}
		url := fmt.Sprintf("%s/v1/workspaces/%s/projects/%s/workloads/%s", suite.ASBCPURL, wsID, projID, wlID)
		req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
		if err != nil {
			log.Printf("E2E: warning – build workload cleanup request for %s: %v", pod.Name, err)
			continue
		}
		req.Header.Set("X-Service-Key", suite.ServiceKey)
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("E2E: warning – cleanup workload %s via ASBCP failed: %v", pod.Name, err)
			continue
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
			log.Printf("E2E: warning – cleanup workload %s returned status %d", pod.Name, resp.StatusCode)
		}
	}
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

func releaseExpiredWorkloadsViaASBCP(t *testing.T, ns string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pods, err := k8sCli.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
		LabelSelector: "app=managed-workload",
	})
	if err != nil {
		t.Logf("releaseExpiredWorkloadsViaASBCP: list error: %v", err)
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
		wsID := strings.TrimSpace(pod.Labels["workspace_id"])
		projID := strings.TrimSpace(pod.Labels["project_id"])
		wlID := strings.TrimSpace(pod.Labels["workload_id"])
		if wsID == "" || projID == "" || wlID == "" {
			continue
		}
		t.Logf("releasing expired workload %s (expired %s ago)", pod.Name, now.Sub(exp).Round(time.Second))
		resp := newClient().DeleteWorkload(t, wsID, projID, wlID)
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
			deleted++
		}
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

func splitCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return values
}

func dnsLabel(value string) string {
	value = strings.ToLower(value)
	value = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= '0' && r <= '9':
			return r
		default:
			return '-'
		}
	}, value)
	value = strings.Trim(value, "-")
	if len(value) > 63 {
		value = strings.Trim(value[:63], "-")
	}
	if value == "" {
		return "mount"
	}
	return value
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
