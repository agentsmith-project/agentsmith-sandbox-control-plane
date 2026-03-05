package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ---- fake K8s pod API -------------------------------------------------------

// fakePodStore is a minimal in-memory K8s pod API used in unit tests.
// It supports GET / DELETE / PATCH for individual pods and GET (list) for
// a pod collection. An optional getHook can be registered per pod name to
// return different payloads on successive calls (used for WaitForPodReady tests).
type fakePodStore struct {
	srv      *httptest.Server
	mu       sync.Mutex
	pods     map[string]*v1.Pod
	deleted  []string // names of pods deleted via DELETE
	getHook  map[string]func(callNum int) *v1.Pod
	getCalls map[string]int
}

func newFakePodStore(t *testing.T) *fakePodStore {
	t.Helper()
	s := &fakePodStore{
		pods:     make(map[string]*v1.Pod),
		getHook:  make(map[string]func(int) *v1.Pod),
		getCalls: make(map[string]int),
	}
	s.srv = httptest.NewServer(s)
	t.Cleanup(s.srv.Close)
	return s
}

func (s *fakePodStore) add(pod *v1.Pod) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pods[pod.Name] = pod
}

// ServeHTTP handles GET/DELETE/PATCH for /api/v1/namespaces/{ns}/pods[/{name}].
// All other paths return 404.
func (s *fakePodStore) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Locate the "/pods" segment in the URL path
	path := r.URL.Path
	podsIdx := strings.Index(path, "/pods")
	if podsIdx < 0 {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	rest := path[podsIdx+len("/pods"):]
	podName := ""
	if len(rest) > 1 {
		podName = strings.TrimPrefix(rest, "/")
		// Strip sub-resources (e.g. /exec)
		if idx := strings.Index(podName, "/"); idx >= 0 {
			podName = podName[:idx]
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	switch r.Method {
	case http.MethodGet:
		if podName == "" {
			s.handleList(w)
		} else {
			s.handleGet(w, podName)
		}
	case http.MethodDelete:
		s.handleDelete(w, podName)
	case http.MethodPatch:
		s.handlePatch(w, r, podName)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *fakePodStore) handleList(w http.ResponseWriter) {
	list := &v1.PodList{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PodList"},
		Items:    make([]v1.Pod, 0, len(s.pods)),
	}
	for _, p := range s.pods {
		list.Items = append(list.Items, *p)
	}
	json.NewEncoder(w).Encode(list)
}

func (s *fakePodStore) handleGet(w http.ResponseWriter, name string) {
	// Apply hook first (for WaitForPodReady transition tests)
	if hook, ok := s.getHook[name]; ok {
		s.getCalls[name]++
		if p := hook(s.getCalls[name]); p != nil {
			s.pods[name] = p
		}
	}
	pod, ok := s.pods[name]
	if !ok {
		writeFakeNotFound(w, name)
		return
	}
	json.NewEncoder(w).Encode(pod)
}

func (s *fakePodStore) handleDelete(w http.ResponseWriter, name string) {
	if _, ok := s.pods[name]; !ok {
		writeFakeNotFound(w, name)
		return
	}
	delete(s.pods, name)
	s.deleted = append(s.deleted, name)
	json.NewEncoder(w).Encode(&metav1.Status{Status: "Success"})
}

func (s *fakePodStore) handlePatch(w http.ResponseWriter, r *http.Request, name string) {
	pod, ok := s.pods[name]
	if !ok {
		writeFakeNotFound(w, name)
		return
	}
	// Apply merge-patch: extract annotations
	var patch struct {
		Metadata struct {
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
	}
	if err := json.NewDecoder(r.Body).Decode(&patch); err == nil {
		if pod.Annotations == nil {
			pod.Annotations = make(map[string]string)
		}
		for k, v := range patch.Metadata.Annotations {
			pod.Annotations[k] = v
		}
	}
	json.NewEncoder(w).Encode(pod)
}

func writeFakeNotFound(w http.ResponseWriter, name string) {
	w.WriteHeader(http.StatusNotFound)
	json.NewEncoder(w).Encode(&metav1.Status{
		TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
		Status:   metav1.StatusFailure,
		Reason:   metav1.StatusReasonNotFound,
		Code:     http.StatusNotFound,
		Message:  fmt.Sprintf("pods %q not found", name),
	})
}

// newClientForStore creates a k8s.Client pointing at the fake server.
func newClientForStore(t *testing.T, store *fakePodStore) *Client {
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
    token: test
contexts:
- name: test
  context:
    cluster: test
    user: test
current-context: test
`, store.srv.URL)
	if err := os.WriteFile(cfgPath, []byte(kubeconfig), 0644); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}
	prev := os.Getenv("KUBECONFIG")
	t.Cleanup(func() { _ = os.Setenv("KUBECONFIG", prev) })
	_ = os.Setenv("KUBECONFIG", cfgPath)

	client, err := NewClient(&ClientConfig{Namespace: "test-ns"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}

// ---- helper pods ------------------------------------------------------------

func makePod(name, phase string, ready bool) *v1.Pod {
	cond := v1.ConditionFalse
	if ready {
		cond = v1.ConditionTrue
	}
	return &v1.Pod{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "test-ns",
		},
		Status: v1.PodStatus{
			Phase: v1.PodPhase(phase),
			Conditions: []v1.PodCondition{
				{Type: v1.PodReady, Status: cond},
			},
		},
	}
}

// ---- GetPod -----------------------------------------------------------------

func TestGetPod_Found(t *testing.T) {
	store := newFakePodStore(t)
	store.add(makePod("my-pod", "Running", true))
	client := newClientForStore(t, store)

	pod, err := client.GetPod(context.Background(), "my-pod")
	if err != nil {
		t.Fatalf("GetPod() unexpected error: %v", err)
	}
	if pod.Name != "my-pod" {
		t.Errorf("GetPod() name = %q, want %q", pod.Name, "my-pod")
	}
}

func TestGetPod_NotFound(t *testing.T) {
	store := newFakePodStore(t)
	client := newClientForStore(t, store)

	_, err := client.GetPod(context.Background(), "ghost-pod")
	if err == nil {
		t.Fatal("GetPod() expected error for missing pod, got nil")
	}
	if !strings.Contains(err.Error(), "failed to get pod") {
		t.Errorf("GetPod() error = %q, want substring 'failed to get pod'", err.Error())
	}
}

func TestGetPod_ReturnsAnnotations(t *testing.T) {
	store := newFakePodStore(t)
	p := makePod("anno-pod", "Running", true)
	p.Annotations = map[string]string{"expires_at": "2099-01-01T00:00:00Z"}
	store.add(p)
	client := newClientForStore(t, store)

	pod, err := client.GetPod(context.Background(), "anno-pod")
	if err != nil {
		t.Fatalf("GetPod() error: %v", err)
	}
	if pod.Annotations["expires_at"] != "2099-01-01T00:00:00Z" {
		t.Errorf("GetPod() annotation = %q, want expiry annotation", pod.Annotations["expires_at"])
	}
}

// ---- PodExists --------------------------------------------------------------

func TestPodExists_True(t *testing.T) {
	store := newFakePodStore(t)
	store.add(makePod("existing-pod", "Running", true))
	client := newClientForStore(t, store)

	exists, err := client.PodExists(context.Background(), "existing-pod")
	if err != nil {
		t.Fatalf("PodExists() error: %v", err)
	}
	if !exists {
		t.Error("PodExists() = false, want true for existing pod")
	}
}

func TestPodExists_False(t *testing.T) {
	store := newFakePodStore(t)
	client := newClientForStore(t, store)

	exists, err := client.PodExists(context.Background(), "no-such-pod")
	if err != nil {
		t.Fatalf("PodExists() unexpected error: %v", err)
	}
	if exists {
		t.Error("PodExists() = true, want false for missing pod")
	}
}

func TestPodExists_FalseAfterDelete(t *testing.T) {
	store := newFakePodStore(t)
	store.add(makePod("temp-pod", "Running", true))
	client := newClientForStore(t, store)

	// Pod exists initially
	exists, err := client.PodExists(context.Background(), "temp-pod")
	if err != nil || !exists {
		t.Fatalf("Expected exists=true initially; err=%v exists=%v", err, exists)
	}

	// Delete via the store directly
	store.mu.Lock()
	delete(store.pods, "temp-pod")
	store.mu.Unlock()

	// Now should not exist
	exists, err = client.PodExists(context.Background(), "temp-pod")
	if err != nil {
		t.Fatalf("PodExists() after deletion unexpected error: %v", err)
	}
	if exists {
		t.Error("PodExists() = true after deletion, want false")
	}
}

// ---- DeletePod --------------------------------------------------------------

func TestDeletePod_Success(t *testing.T) {
	store := newFakePodStore(t)
	store.add(makePod("del-pod", "Running", true))
	client := newClientForStore(t, store)

	err := client.DeletePod(context.Background(), "del-pod", 0)
	if err != nil {
		t.Fatalf("DeletePod() unexpected error: %v", err)
	}

	// Pod should no longer be in the store
	store.mu.Lock()
	_, still := store.pods["del-pod"]
	store.mu.Unlock()
	if still {
		t.Error("DeletePod() pod still in store after deletion")
	}
}

func TestDeletePod_NotFound_IsNotAnError(t *testing.T) {
	// Deleting a non-existent pod must be idempotent – should NOT return an error.
	store := newFakePodStore(t)
	client := newClientForStore(t, store)

	err := client.DeletePod(context.Background(), "ghost-pod", 0)
	if err != nil {
		t.Errorf("DeletePod() on missing pod returned error %v, want nil", err)
	}
}

func TestDeletePod_ZeroGracePeriod(t *testing.T) {
	store := newFakePodStore(t)
	store.add(makePod("grace-pod", "Running", true))
	client := newClientForStore(t, store)

	if err := client.DeletePod(context.Background(), "grace-pod", 0); err != nil {
		t.Errorf("DeletePod(gracePeriod=0) error: %v", err)
	}
}

func TestDeletePod_NegativeGracePeriod_SkipsGraceOption(t *testing.T) {
	// gracePeriodSeconds < 0 means "don't set the option" – the call should still succeed.
	store := newFakePodStore(t)
	store.add(makePod("neg-grace-pod", "Running", true))
	client := newClientForStore(t, store)

	if err := client.DeletePod(context.Background(), "neg-grace-pod", -1); err != nil {
		t.Errorf("DeletePod(gracePeriod=-1) error: %v", err)
	}
}

// ---- PatchActivity ----------------------------------------------------------

func TestPatchActivity_UpdatesAnnotations(t *testing.T) {
	store := newFakePodStore(t)
	store.add(makePod("patch-pod", "Running", true))
	client := newClientForStore(t, store)

	expiresAt := time.Now().UTC().Add(5 * time.Minute)
	if err := client.PatchActivity(context.Background(), "patch-pod", expiresAt); err != nil {
		t.Fatalf("PatchActivity() error: %v", err)
	}

	store.mu.Lock()
	pod := store.pods["patch-pod"]
	store.mu.Unlock()

	if pod.Annotations == nil {
		t.Fatal("PatchActivity() did not set annotations")
	}
	if _, ok := pod.Annotations["last_activity_at"]; !ok {
		t.Error("PatchActivity() missing last_activity_at annotation")
	}
	if got := pod.Annotations["expires_at"]; got == "" {
		t.Error("PatchActivity() missing expires_at annotation")
	}
}

func TestPatchActivity_ExpiresAtFormat(t *testing.T) {
	store := newFakePodStore(t)
	store.add(makePod("fmt-pod", "Running", true))
	client := newClientForStore(t, store)

	future := time.Date(2099, 6, 1, 12, 0, 0, 0, time.UTC)
	if err := client.PatchActivity(context.Background(), "fmt-pod", future); err != nil {
		t.Fatalf("PatchActivity() error: %v", err)
	}

	store.mu.Lock()
	got := store.pods["fmt-pod"].Annotations["expires_at"]
	store.mu.Unlock()

	parsed, err := time.Parse(time.RFC3339, got)
	if err != nil {
		t.Errorf("PatchActivity() expires_at %q is not RFC3339: %v", got, err)
	}
	if !parsed.Equal(future) {
		t.Errorf("PatchActivity() expires_at = %v, want %v", parsed, future)
	}
}

func TestPatchActivity_NotFound_ReturnsError(t *testing.T) {
	store := newFakePodStore(t)
	client := newClientForStore(t, store)

	err := client.PatchActivity(context.Background(), "missing-pod", time.Now().Add(time.Minute))
	if err == nil {
		t.Fatal("PatchActivity() on missing pod should return error, got nil")
	}
}

// ---- WaitForPodReady --------------------------------------------------------

func TestWaitForPodReady_AlreadyReady(t *testing.T) {
	store := newFakePodStore(t)
	store.add(makePod("ready-pod", "Running", true))
	client := newClientForStore(t, store)

	ready, err := client.WaitForPodReady(context.Background(), "ready-pod", 2*time.Second, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForPodReady() error: %v", err)
	}
	if !ready {
		t.Error("WaitForPodReady() = false, want true for already-ready pod")
	}
}

func TestWaitForPodReady_TransitionsToReady(t *testing.T) {
	store := newFakePodStore(t)
	pending := makePod("trans-pod", "Pending", false)
	store.add(pending)
	client := newClientForStore(t, store)

	// After 2 GET calls, upgrade the pod to Running+Ready
	store.mu.Lock()
	store.getHook["trans-pod"] = func(callNum int) *v1.Pod {
		if callNum >= 2 {
			return makePod("trans-pod", "Running", true)
		}
		return nil
	}
	store.mu.Unlock()

	ready, err := client.WaitForPodReady(context.Background(), "trans-pod", 3*time.Second, 30*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForPodReady() error: %v", err)
	}
	if !ready {
		t.Error("WaitForPodReady() = false, want true after pod became ready")
	}
}

func TestWaitForPodReady_Timeout(t *testing.T) {
	store := newFakePodStore(t)
	store.add(makePod("stuck-pod", "Pending", false))
	client := newClientForStore(t, store)

	_, err := client.WaitForPodReady(context.Background(), "stuck-pod", 150*time.Millisecond, 20*time.Millisecond)
	if err == nil {
		t.Fatal("WaitForPodReady() expected timeout error, got nil")
	}
}

func TestWaitForPodReady_PodFailed_ReturnsError(t *testing.T) {
	store := newFakePodStore(t)
	store.add(makePod("failed-pod", "Failed", false))
	client := newClientForStore(t, store)

	_, err := client.WaitForPodReady(context.Background(), "failed-pod", 2*time.Second, 20*time.Millisecond)
	if err == nil {
		t.Fatal("WaitForPodReady() expected error for Failed pod, got nil")
	}
	if !strings.Contains(err.Error(), "failed") {
		t.Errorf("WaitForPodReady() error = %q, want 'failed' in message", err.Error())
	}
}

func TestWaitForPodReady_PodNotFound_ReturnsError(t *testing.T) {
	store := newFakePodStore(t)
	client := newClientForStore(t, store)

	_, err := client.WaitForPodReady(context.Background(), "no-pod", 2*time.Second, 20*time.Millisecond)
	if err == nil {
		t.Fatal("WaitForPodReady() expected error for missing pod, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("WaitForPodReady() error = %q, want 'not found' in message", err.Error())
	}
}

func TestWaitForPodReady_ContextCancelled(t *testing.T) {
	store := newFakePodStore(t)
	store.add(makePod("ctx-pod", "Pending", false))
	client := newClientForStore(t, store)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := client.WaitForPodReady(ctx, "ctx-pod", 10*time.Second, 20*time.Millisecond)
	if err == nil {
		t.Fatal("WaitForPodReady() expected context-cancel error, got nil")
	}
}

// ---- CheckReady & CreateNamespace (remain skipped; require real apiserver) --

func TestGetPod_SkipRequiresRealServer(t *testing.T) {
	t.Skip("covered by TestGetPod_Found / TestGetPod_NotFound using fake server")
}

func TestPodExists_SkipRequiresRealServer(t *testing.T) {
	t.Skip("covered by TestPodExists_True / TestPodExists_False using fake server")
}

func TestWaitForPodReady(t *testing.T) {
	t.Skip("covered by TestWaitForPodReady_* sub-tests using fake server")
}

func TestDeletePod(t *testing.T) {
	t.Skip("covered by TestDeletePod_* sub-tests using fake server")
}

func TestPatchActivity(t *testing.T) {
	t.Skip("covered by TestPatchActivity_* sub-tests using fake server")
}

func TestPodExists(t *testing.T) {
	t.Skip("covered by TestPodExists_True / TestPodExists_False using fake server")
}

func TestGetPod(t *testing.T) {
	t.Skip("covered by TestGetPod_Found / TestGetPod_NotFound using fake server")
}

