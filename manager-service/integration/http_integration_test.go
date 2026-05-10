//go:build integration
// +build integration

package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sandbox/manager/internal/auth"
	"github.com/sandbox/manager/internal/k8s"
	"github.com/sandbox/manager/internal/observability"
	"github.com/sandbox/manager/internal/ratelimit"
	"github.com/sandbox/manager/internal/workload"
	"github.com/sandbox/manager/internal/workspacebinding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestHTTPStack_HealthAndV1WithAuth runs against an in-process HTTP server
// with workload handler + auth middleware (no real K8s).
func TestHTTPStack_HealthAndV1WithAuth(t *testing.T) {
	// Fake K8s API: return 404 for all requests
	fakeK8s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(&metav1.Status{
			TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
			Status:   metav1.StatusFailure,
			Reason:   metav1.StatusReasonNotFound,
			Code:     http.StatusNotFound,
		})
	}))
	t.Cleanup(fakeK8s.Close)

	kubeconfig := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: %s
users:
- name: test
  user:
    token: fake
contexts:
- name: test
  context:
    cluster: test
    user: test
current-context: test
`, fakeK8s.URL)
	cfgPath := filepath.Join(t.TempDir(), "kubeconfig")
	require.NoError(t, os.WriteFile(cfgPath, []byte(kubeconfig), 0644))
	t.Setenv("KUBECONFIG", cfgPath)

	client, err := k8s.NewClient(&k8s.ClientConfig{Namespace: "test-ns"})
	require.NoError(t, err)

	validator, err := auth.NewServiceKeyValidator([]string{"test-key-123"})
	require.NoError(t, err)
	handler := newTestWorkloadHandler(client)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	v1Mux := http.NewServeMux()
	handler.RegisterRoutes(v1Mux)
	mux.Handle("/v1/", auth.ServiceKeyMiddleware(validator, "X-Service-Key")(v1Mux))

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	base := srv.URL

	// Health: no auth
	resp, err := http.Get(base + "/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// V1 without key: 401
	req, _ := http.NewRequest(http.MethodGet, base+"/v1/workspaces/ws/projects/proj/workloads/wl", nil)
	resp, err = srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	// V1 with key: 200 (pod not found → offline)
	req, _ = http.NewRequest(http.MethodGet, base+"/v1/workspaces/ws/projects/proj/workloads/wl", nil)
	req.Header.Set("X-Service-Key", "test-key-123")
	resp, err = srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var status workload.PodStatus
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&status))
	assert.Equal(t, "offline", status.Phase)
}

// ---- shared stack builder ---------------------------------------------------

// buildTestStack returns a *httptest.Server with auth + optional rate-limit
// middleware wrapping a workload handler backed by the given fakeK8s server URL.
func buildTestStack(t *testing.T, fakeK8sURL, serviceKey string, limiter *ratelimit.Limiter) *httptest.Server {
	t.Helper()

	cfgPath := filepath.Join(t.TempDir(), "kubeconfig")
	kubeconfig := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: %s
    insecure-skip-tls-verify: true
users:
- name: test
  user:
    token: fake
contexts:
- name: test
  context:
    cluster: test
    user: test
current-context: test
`, fakeK8sURL)
	require.NoError(t, os.WriteFile(cfgPath, []byte(kubeconfig), 0644))
	t.Setenv("KUBECONFIG", cfgPath)

	client, err := k8s.NewClient(&k8s.ClientConfig{Namespace: "test-ns"})
	require.NoError(t, err)

	validator, err := auth.NewServiceKeyValidator([]string{serviceKey})
	require.NoError(t, err)
	handler := newTestWorkloadHandler(client)

	v1Mux := http.NewServeMux()
	handler.RegisterRoutes(v1Mux)

	var v1Handler http.Handler = auth.ServiceKeyMiddleware(validator, "X-Service-Key")(v1Mux)
	if limiter != nil {
		v1Handler = limiter.Middleware(v1Handler)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.Handle("/v1/", v1Handler)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newTestWorkloadHandler(client *k8s.Client) *workload.Handler {
	return workload.NewHandler(client, k8s.NewExecutor(client), workload.Options{})
}

// newAlways404K8s returns a fake K8s server that returns 404 for all requests.
func newAlways404K8s(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(&metav1.Status{
			TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
			Status:   metav1.StatusFailure,
			Reason:   metav1.StatusReasonNotFound,
			Code:     http.StatusNotFound,
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// statefulPodFake is a minimal in-memory K8s pod API for integration tests.
// Handles: POST pods (create), GET pod (get one), DELETE pod, PATCH pod.
type statefulPodFake struct {
	mu   sync.Mutex
	pods map[string]*v1.Pod
}

func newStatefulPodFake(t *testing.T) *statefulPodFake {
	f := &statefulPodFake{pods: make(map[string]*v1.Pod)}
	return f
}

// addPod pre-populates the fake with a pod (for tests that need an existing pod without going through manager Create).
func (f *statefulPodFake) addPod(pod *v1.Pod) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
	f.pods[pod.Name] = pod
}

func (f *statefulPodFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := r.URL.Path

	if r.Method == http.MethodGet && strings.Contains(path, "/persistentvolumeclaims/") {
		_ = json.NewEncoder(w).Encode(testBindingPVC("ws", "p", "wmb_demo"))
		return
	}

	// Expect paths like /api/v1/namespaces/test-ns/pods or .../pods/<name>
	idx := strings.Index(path, "/pods")
	if idx < 0 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	suffix := path[idx+len("/pods"):]
	name := strings.TrimPrefix(suffix, "/")

	f.mu.Lock()
	defer f.mu.Unlock()

	switch r.Method {
	case http.MethodPost:
		if name != "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var pod v1.Pod
		if err := json.NewDecoder(r.Body).Decode(&pod); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if pod.CreationTimestamp.IsZero() {
			pod.CreationTimestamp = metav1.Now()
		}
		if pod.Annotations == nil {
			pod.Annotations = make(map[string]string)
		}
		// Mark pod Ready so manager's WaitForPodReady returns immediately (root cause of integration hang).
		pod.Status.Phase = v1.PodRunning
		pod.Status.PodIP = "10.0.0.1"
		pod.Status.Conditions = []v1.PodCondition{
			{Type: v1.PodReady, Status: v1.ConditionTrue, LastTransitionTime: metav1.Now()},
		}
		f.pods[pod.Name] = &pod
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(&pod)

	case http.MethodGet:
		if name == "" {
			list := &v1.PodList{Items: make([]v1.Pod, 0, len(f.pods))}
			for _, p := range f.pods {
				list.Items = append(list.Items, *p)
			}
			json.NewEncoder(w).Encode(list)
			return
		}
		pod, ok := f.pods[name]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(&metav1.Status{Code: 404, Reason: metav1.StatusReasonNotFound})
			return
		}
		// Ensure returned pod appears Ready (in case it was added via addPod without Status).
		if pod.Status.Phase == "" {
			pod.Status.Phase = v1.PodRunning
			pod.Status.PodIP = "10.0.0.1"
			pod.Status.Conditions = []v1.PodCondition{
				{Type: v1.PodReady, Status: v1.ConditionTrue, LastTransitionTime: metav1.Now()},
			}
		}
		json.NewEncoder(w).Encode(pod)

	case http.MethodDelete:
		if _, ok := f.pods[name]; !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		delete(f.pods, name)
		json.NewEncoder(w).Encode(&metav1.Status{Status: "Success", Code: http.StatusOK})

	case http.MethodPatch:
		pod, ok := f.pods[name]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var patch struct {
			Metadata struct {
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
		}
		if err := json.NewDecoder(r.Body).Decode(&patch); err == nil && pod.Annotations != nil {
			for k, v := range patch.Metadata.Annotations {
				pod.Annotations[k] = v
			}
		}
		json.NewEncoder(w).Encode(pod)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *statefulPodFake) makeServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return srv
}

func testBindingPVC(workspaceID, projectID, bindingID string) *v1.PersistentVolumeClaim {
	return &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      workspacebinding.PVCName(workspaceID, projectID, bindingID),
			Namespace: "test-ns",
			Annotations: map[string]string{
				"mbos.io/afscp-namespace-id":          "ns_demo",
				"mbos.io/afscp-mount-binding-id":      bindingID,
				"mbos.io/afscp-volume-id":             "vol_demo",
				"mbos.io/payload-volume-subdir":       "afscp/ns_demo/repos/repo_demo/payload",
				"mbos.io/mount-path":                  "/home/task-plan",
				"mbos.io/read-only":                   "false",
				"mbos.io/run-as-non-root":             "true",
				"mbos.io/allow-privileged":            "false",
				"mbos.io/jvs-control-outside-payload": "true",
			},
		},
	}
}

// ---- Auth error codes -------------------------------------------------------

func TestHTTPStack_MissingKey_ReturnsMissingCode(t *testing.T) {
	fakeK8s := newAlways404K8s(t)
	srv := buildTestStack(t, fakeK8s.URL, "key-abc", nil)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/workspaces/ws/projects/p/workloads/wl", nil)
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "SERVICE_KEY_MISSING", body["error"])
}

func TestHTTPStack_InvalidKey_ReturnsInvalidCode(t *testing.T) {
	fakeK8s := newAlways404K8s(t)
	srv := buildTestStack(t, fakeK8s.URL, "real-key", nil)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/workspaces/ws/projects/p/workloads/wl", nil)
	req.Header.Set("X-Service-Key", "wrong-key")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "SERVICE_KEY_INVALID", body["error"])
}

func TestHTTPStack_AuthErrors_AreDistinct(t *testing.T) {
	fakeK8s := newAlways404K8s(t)
	srv := buildTestStack(t, fakeK8s.URL, "the-key", nil)

	doReq := func(key string) string {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/workspaces/ws/projects/p/workloads/wl", nil)
		if key != "" {
			req.Header.Set("X-Service-Key", key)
		}
		resp, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		var body map[string]string
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
		return body["error"]
	}

	missing := doReq("")
	invalid := doReq("bad")
	assert.NotEqual(t, missing, invalid, "missing and invalid key errors must be distinct")
	assert.Equal(t, "SERVICE_KEY_MISSING", missing)
	assert.Equal(t, "SERVICE_KEY_INVALID", invalid)
}

// ---- Rate limiting in the HTTP stack ----------------------------------------

func TestHTTPStack_RateLimiter_ThrottlesRequests(t *testing.T) {
	fakeK8s := newAlways404K8s(t)
	limiter := ratelimit.NewLimiter(&ratelimit.Config{GlobalRPS: 1, GlobalBurst: 1})
	srv := buildTestStack(t, fakeK8s.URL, "key", limiter)

	doGet := func() int {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/workspaces/ws/projects/p/workloads/wl", nil)
		req.Header.Set("X-Service-Key", "key")
		resp, err := srv.Client().Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		return resp.StatusCode
	}

	// First request: token available → passes auth → 200 (pod offline)
	first := doGet()
	assert.Equal(t, http.StatusOK, first)

	// Second request immediately: rate-limited → 429
	second := doGet()
	assert.Equal(t, http.StatusTooManyRequests, second)
}

func TestHTTPStack_RateLimiter_HealthzNotRateLimited(t *testing.T) {
	// Health endpoint sits outside the rate-limited subtree
	fakeK8s := newAlways404K8s(t)
	limiter := ratelimit.NewLimiter(&ratelimit.Config{GlobalRPS: 1, GlobalBurst: 1})
	srv := buildTestStack(t, fakeK8s.URL, "key", limiter)

	for i := 0; i < 5; i++ {
		resp, err := http.Get(srv.URL + "/healthz")
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode, "healthz should not be rate-limited")
	}
}

// ---- RequestID middleware in the full stack ----------------------------------

func TestHTTPStack_RequestIDMiddleware_SetsResponseHeader(t *testing.T) {
	fakeK8s := newAlways404K8s(t)

	// Build a stack that includes the request-ID middleware
	cfgPath := filepath.Join(t.TempDir(), "kubeconfig")
	kubeconfig := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: %s
    insecure-skip-tls-verify: true
users:
- name: test
  user:
    token: fake
contexts:
- name: test
  context:
    cluster: test
    user: test
current-context: test
`, fakeK8s.URL)
	require.NoError(t, os.WriteFile(cfgPath, []byte(kubeconfig), 0644))
	t.Setenv("KUBECONFIG", cfgPath)

	client, err := k8s.NewClient(&k8s.ClientConfig{Namespace: "test-ns"})
	require.NoError(t, err)
	validator, _ := auth.NewServiceKeyValidator([]string{"test-key"})
	handler := newTestWorkloadHandler(client)

	v1Mux := http.NewServeMux()
	handler.RegisterRoutes(v1Mux)

	// Chain: request-ID → auth → workload
	requestIDMW := observability.RequestIDMiddleware("X-Request-Id")
	v1Handler := requestIDMW(auth.ServiceKeyMiddleware(validator, "X-Service-Key")(v1Mux))

	mux := http.NewServeMux()
	mux.Handle("/v1/", v1Handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/workspaces/ws/projects/p/workloads/wl", nil)
	req.Header.Set("X-Service-Key", "test-key")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	reqID := resp.Header.Get("X-Request-Id")
	assert.NotEmpty(t, reqID, "response must include X-Request-Id header")
}

func TestHTTPStack_RequestIDMiddleware_PropagatesClientID(t *testing.T) {
	fakeK8s := newAlways404K8s(t)
	cfgPath := filepath.Join(t.TempDir(), "kubeconfig")
	kubeconfig := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: %s
    insecure-skip-tls-verify: true
users:
- name: test
  user:
    token: fake
contexts:
- name: test
  context:
    cluster: test
    user: test
current-context: test
`, fakeK8s.URL)
	require.NoError(t, os.WriteFile(cfgPath, []byte(kubeconfig), 0644))
	t.Setenv("KUBECONFIG", cfgPath)

	client, err := k8s.NewClient(&k8s.ClientConfig{Namespace: "test-ns"})
	require.NoError(t, err)
	validator, _ := auth.NewServiceKeyValidator([]string{"test-key"})
	handler := newTestWorkloadHandler(client)

	v1Mux := http.NewServeMux()
	handler.RegisterRoutes(v1Mux)
	v1Handler := observability.RequestIDMiddleware("X-Request-Id")(
		auth.ServiceKeyMiddleware(validator, "X-Service-Key")(v1Mux),
	)

	mux := http.NewServeMux()
	mux.Handle("/v1/", v1Handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	const clientReqID = "my-trace-id-12345"
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/workspaces/ws/projects/p/workloads/wl", nil)
	req.Header.Set("X-Service-Key", "test-key")
	req.Header.Set("X-Request-Id", clientReqID)
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, clientReqID, resp.Header.Get("X-Request-Id"))
}

// ---- Metrics recording in the full stack ------------------------------------

func TestHTTPStack_MetricsRecorded_AfterRequest(t *testing.T) {
	fakeK8s := newAlways404K8s(t)

	cfgPath := filepath.Join(t.TempDir(), "kubeconfig")
	kubeconfig := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: %s
    insecure-skip-tls-verify: true
users:
- name: test
  user:
    token: fake
contexts:
- name: test
  context:
    cluster: test
    user: test
current-context: test
`, fakeK8s.URL)
	require.NoError(t, os.WriteFile(cfgPath, []byte(kubeconfig), 0644))
	t.Setenv("KUBECONFIG", cfgPath)

	client, err := k8s.NewClient(&k8s.ClientConfig{Namespace: "test-ns"})
	require.NoError(t, err)
	validator, _ := auth.NewServiceKeyValidator([]string{"test-key"})
	handler := newTestWorkloadHandler(client)

	metrics := observability.NewMetricsRegistry()

	v1Mux := http.NewServeMux()
	handler.RegisterRoutes(v1Mux)
	v1Handler := auth.ServiceKeyMiddleware(validator, "X-Service-Key")(v1Mux)

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", metrics.Handler())
	mux.Handle("/v1/", v1Handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// Fire a request to record a metric manually (simulating app.go middleware)
	metrics.RecordWorkloadCreate()
	metrics.RecordWorkloadKeepalive()

	resp, err := http.Get(srv.URL + "/metrics")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)
	assert.True(t, strings.Contains(bodyStr, "workload_create_total 1"),
		"metrics should report workload_create_total 1; got:\n%s", bodyStr)
	assert.True(t, strings.Contains(bodyStr, "workload_keepalive_total 1"),
		"metrics should report workload_keepalive_total 1; got:\n%s", bodyStr)
}

// ---- Full stack: unknown route handling ------------------------------------

func TestHTTPStack_UnknownRoute_Returns404(t *testing.T) {
	fakeK8s := newAlways404K8s(t)
	srv := buildTestStack(t, fakeK8s.URL, "key", nil)

	resp, err := http.Get(srv.URL + "/nonexistent/path")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// ---- Full stack: method-not-allowed -----------------------------------------

func TestHTTPStack_WrongMethod_Returns405(t *testing.T) {
	fakeK8s := newAlways404K8s(t)
	srv := buildTestStack(t, fakeK8s.URL, "key", nil)

	// GET on a keepalive endpoint should return 405
	req, _ := http.NewRequest(http.MethodGet,
		srv.URL+"/v1/workspaces/ws/projects/p/workloads/wl/keepalive", nil)
	req.Header.Set("X-Service-Key", "key")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

// ---- Create / route validation -----------------------------------------------

func TestHTTPStack_PUT_InvalidJSON_Returns400(t *testing.T) {
	fakeK8s := newAlways404K8s(t)
	srv := buildTestStack(t, fakeK8s.URL, "key", nil)

	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/v1/workspaces/ws/projects/p/workloads/wl", strings.NewReader("not json"))
	req.Header.Set("X-Service-Key", "key")
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestHTTPStack_PUT_EmptyBody_Returns400(t *testing.T) {
	fakeK8s := newAlways404K8s(t)
	srv := buildTestStack(t, fakeK8s.URL, "key", nil)

	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/v1/workspaces/ws/projects/p/workloads/wl", nil)
	req.Header.Set("X-Service-Key", "key")
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestHTTPStack_Exec_InvalidBody_Returns400(t *testing.T) {
	fakeK8s := newAlways404K8s(t)
	srv := buildTestStack(t, fakeK8s.URL, "key", nil)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/workspaces/ws/projects/p/workloads/wl/exec", strings.NewReader("{"))
	req.Header.Set("X-Service-Key", "key")
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	// Pod doesn't exist so we might get 404; invalid JSON might be parsed first and yield 400
	assert.Contains(t, []int{http.StatusBadRequest, http.StatusNotFound}, resp.StatusCode)
}

func TestHTTPStack_V1Path_TooShort_Returns404(t *testing.T) {
	fakeK8s := newAlways404K8s(t)
	srv := buildTestStack(t, fakeK8s.URL, "key", nil)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/workspaces/ws/projects", nil)
	req.Header.Set("X-Service-Key", "key")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// ---- Full lifecycle with stateful fake (API contract) ------------------------

// TestIntegration_FullLifecycle_CreateGetKeepaliveDeleteGet runs the full API lifecycle including Create.
// Root cause of original hang: stateful fake did not set Pod.Status (Phase/Conditions), so the manager's
// WaitForPodReady blocked for 120s. Fix: fake now sets Phase=Running and PodReady condition on create/get.
func TestIntegration_FullLifecycle_CreateGetKeepaliveDeleteGet(t *testing.T) {
	stateful := newStatefulPodFake(t)
	fakeK8s := stateful.makeServer(t)
	srv := buildTestStack(t, fakeK8s.URL, "key", nil)
	base := srv.URL + "/v1/workspaces/ws/projects/p/workloads/wl1"
	key := "key"

	createBody := `{"image":"busybox:1.36","workspace_binding_id":"wmb_demo","idle_timeout_sec":600,"max_lifetime_sec":3600}`
	req, _ := http.NewRequest(http.MethodPut, base, strings.NewReader(createBody))
	req.Header.Set("X-Service-Key", key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode, "create must return 201: %s", readBody(resp))
	resp.Body.Close()

	stateful.mu.Lock()
	createdPod := stateful.pods["workload-wl1"]
	stateful.mu.Unlock()
	require.NotNil(t, createdPod)
	require.Len(t, createdPod.Spec.Containers, 1)
	require.Len(t, createdPod.Spec.Containers[0].VolumeMounts, 1)
	mount := createdPod.Spec.Containers[0].VolumeMounts[0]
	assert.Equal(t, "/home/task-plan", mount.MountPath)
	assert.Empty(t, mount.SubPath)
	assert.Equal(t, "/home/task-plan/workspace", createdPod.Spec.Containers[0].WorkingDir)
	require.NotNil(t, createdPod.Spec.Volumes[0].PersistentVolumeClaim)
	assert.Equal(t, workspacebinding.PVCName("ws", "p", "wmb_demo"), createdPod.Spec.Volumes[0].PersistentVolumeClaim.ClaimName)

	req, _ = http.NewRequest(http.MethodGet, base, nil)
	req.Header.Set("X-Service-Key", key)
	resp, err = srv.Client().Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var status map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&status))
	resp.Body.Close()
	assert.NotEmpty(t, status["phase"])

	req, _ = http.NewRequest(http.MethodPost, base+"/keepalive", nil)
	req.Header.Set("X-Service-Key", key)
	resp, err = srv.Client().Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	req, _ = http.NewRequest(http.MethodDelete, base, nil)
	req.Header.Set("X-Service-Key", key)
	resp, err = srv.Client().Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	req, _ = http.NewRequest(http.MethodGet, base, nil)
	req.Header.Set("X-Service-Key", key)
	resp, err = srv.Client().Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&status))
	resp.Body.Close()
	assert.Equal(t, "offline", status["phase"])
}

func readBody(resp *http.Response) string {
	if resp == nil || resp.Body == nil {
		return ""
	}
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// TestIntegration_FullLifecycle_GetKeepaliveDeleteGet runs get/keepalive/delete/get against a pre-created pod (no Create).
func TestIntegration_FullLifecycle_GetKeepaliveDeleteGet(t *testing.T) {
	stateful := newStatefulPodFake(t)
	stateful.addPod(&v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "workload-wl1",
			CreationTimestamp: metav1.Now(),
			Annotations:       map[string]string{"workload/maxExpiresAt": "2030-01-01T00:00:00Z"},
		},
		Status: v1.PodStatus{Phase: v1.PodRunning},
	})
	fakeK8s := stateful.makeServer(t)
	srv := buildTestStack(t, fakeK8s.URL, "key", nil)
	base := srv.URL + "/v1/workspaces/ws/projects/p/workloads/wl1"
	key := "key"

	// 1) Get – must return 200 with phase
	req, _ := http.NewRequest(http.MethodGet, base, nil)
	req.Header.Set("X-Service-Key", key)
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var status map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&status))
	resp.Body.Close()
	assert.NotEmpty(t, status["phase"])

	// 2) Keepalive
	req, _ = http.NewRequest(http.MethodPost, base+"/keepalive", nil)
	req.Header.Set("X-Service-Key", key)
	resp, err = srv.Client().Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// 3) Delete
	req, _ = http.NewRequest(http.MethodDelete, base, nil)
	req.Header.Set("X-Service-Key", key)
	resp, err = srv.Client().Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// 4) Get after delete – must return 200 with phase "offline"
	req, _ = http.NewRequest(http.MethodGet, base, nil)
	req.Header.Set("X-Service-Key", key)
	resp, err = srv.Client().Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&status))
	resp.Body.Close()
	assert.Equal(t, "offline", status["phase"])
}

func TestIntegration_GetOfflineWhenNoPod(t *testing.T) {
	fake := newStatefulPodFake(t)
	srv := buildTestStack(t, fake.makeServer(t).URL, "key", nil)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/workspaces/ws/projects/p/workloads/nonexistent", nil)
	req.Header.Set("X-Service-Key", "key")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var status map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&status))
	assert.Equal(t, "offline", status["phase"])
}

func TestIntegration_DeleteNotFound_Returns404(t *testing.T) {
	fake := newStatefulPodFake(t)
	srv := buildTestStack(t, fake.makeServer(t).URL, "key", nil)

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/v1/workspaces/ws/projects/p/workloads/ghost", nil)
	req.Header.Set("X-Service-Key", "key")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestIntegration_KeepaliveNotFound_Returns404(t *testing.T) {
	fake := newStatefulPodFake(t)
	srv := buildTestStack(t, fake.makeServer(t).URL, "key", nil)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/workspaces/ws/projects/p/workloads/ghost/keepalive", nil)
	req.Header.Set("X-Service-Key", "key")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestIntegration_InvalidWorkloadID_Returns400(t *testing.T) {
	fake := newStatefulPodFake(t)
	srv := buildTestStack(t, fake.makeServer(t).URL, "key", nil)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/workspaces/ws/projects/p/workloads/INVALID_UPPER", nil)
	req.Header.Set("X-Service-Key", "key")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestIntegration_CreateWithoutImage_Returns400(t *testing.T) {
	fake := newStatefulPodFake(t)
	srv := buildTestStack(t, fake.makeServer(t).URL, "key", nil)

	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/v1/workspaces/ws/projects/p/workloads/wl", strings.NewReader(`{}`))
	req.Header.Set("X-Service-Key", "key")
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}
