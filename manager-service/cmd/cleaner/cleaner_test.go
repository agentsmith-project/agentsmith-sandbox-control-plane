package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// ---- constants sanity check -------------------------------------------------

func TestCleanerConstants(t *testing.T) {
	if sandboxAppLabel != "sandbox" {
		t.Errorf("sandboxAppLabel = %q, want sandbox", sandboxAppLabel)
	}
	if workloadAppLabel != "managed-workload" {
		t.Errorf("workloadAppLabel = %q, want managed-workload", workloadAppLabel)
	}
	if expiresAtAnnotation != "expires_at" {
		t.Errorf("expiresAtAnnotation = %q, want expires_at", expiresAtAnnotation)
	}
}

// ---- fake K8s API for cleaner tests -----------------------------------------

type cleanerFakeAPI struct {
	mu               sync.Mutex
	pods             []v1.Pod // pods returned by LIST
	deleted          []string // names of pods deleted
	forceListError   bool     // if true, LIST returns 500
	forceDeleteError string   // if non-empty, DELETE for this pod name returns 500
}

// ServeHTTP handles LIST pods and DELETE pod requests.
func (f *cleanerFakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	path := r.URL.Path

	// Match /api/v1/namespaces/{ns}/pods or /api/v1/namespaces/{ns}/pods/{name}
	podsIdx := strings.Index(path, "/pods")
	if podsIdx < 0 {
		// Unknown – return empty 404
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(&metav1.Status{Status: "Failure", Code: 404})
		return
	}

	rest := path[podsIdx+len("/pods"):]
	podName := ""
	if len(rest) > 1 {
		podName = strings.TrimPrefix(rest, "/")
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	switch r.Method {
	case http.MethodGet:
		if podName == "" {
			// LIST
			if f.forceListError {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(&metav1.Status{Status: "Failure", Code: 500, Message: "injected list error"})
				return
			}
			list := &v1.PodList{
				TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PodList"},
				Items:    f.pods,
			}
			json.NewEncoder(w).Encode(list)
		} else {
			// GET individual pod (not used by cleaner, return 404)
			w.WriteHeader(http.StatusNotFound)
		}
	case http.MethodDelete:
		if podName == f.forceDeleteError {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(&metav1.Status{Status: "Failure", Code: 500, Message: "injected delete error"})
			return
		}
		f.deleted = append(f.deleted, podName)
		json.NewEncoder(w).Encode(&metav1.Status{Status: "Success"})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// newCleanerClientset creates a kubernetes.Clientset pointing at the fake server.
func newCleanerClientset(t *testing.T, api *cleanerFakeAPI) *kubernetes.Clientset {
	t.Helper()
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)

	cfg := &rest.Config{Host: srv.URL}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("kubernetes.NewForConfig: %v", err)
	}
	return cs
}

// ---- helpers ----------------------------------------------------------------

func podWithExpiry(name, ns string, expiresAt time.Time) v1.Pod {
	return v1.Pod{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Annotations: map[string]string{
				expiresAtAnnotation: expiresAt.UTC().Format(time.RFC3339),
			},
		},
	}
}

func podWithLabel(name, ns, appLabel string, expiresAt time.Time) v1.Pod {
	p := podWithExpiry(name, ns, expiresAt)
	p.Labels = map[string]string{"app": appLabel}
	return p
}

func podNoAnnotation(name, ns string) v1.Pod {
	return v1.Pod{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
	}
}

// ---- runCleaner tests -------------------------------------------------------

func TestRunCleaner_DeletesExpiredPod(t *testing.T) {
	expired := podWithExpiry("old-pod", "test-ns", time.Now().Add(-1*time.Hour))
	api := &cleanerFakeAPI{pods: []v1.Pod{expired}}
	cs := newCleanerClientset(t, api)

	if err := runCleaner(context.Background(), cs, "test-ns", false); err != nil {
		t.Fatalf("runCleaner error: %v", err)
	}

	api.mu.Lock()
	deleted := api.deleted
	api.mu.Unlock()
	if len(deleted) != 1 || deleted[0] != "old-pod" {
		t.Errorf("runCleaner deleted %v, want [old-pod]", deleted)
	}
}

func TestRunCleaner_SkipsActivePod(t *testing.T) {
	active := podWithExpiry("live-pod", "test-ns", time.Now().Add(1*time.Hour))
	api := &cleanerFakeAPI{pods: []v1.Pod{active}}
	cs := newCleanerClientset(t, api)

	if err := runCleaner(context.Background(), cs, "test-ns", false); err != nil {
		t.Fatalf("runCleaner error: %v", err)
	}

	api.mu.Lock()
	deleted := api.deleted
	api.mu.Unlock()
	if len(deleted) != 0 {
		t.Errorf("runCleaner deleted active pod(s): %v", deleted)
	}
}

func TestRunCleaner_DryRun_DoesNotDelete(t *testing.T) {
	expired := podWithExpiry("exp-pod", "test-ns", time.Now().Add(-1*time.Hour))
	api := &cleanerFakeAPI{pods: []v1.Pod{expired}}
	cs := newCleanerClientset(t, api)

	if err := runCleaner(context.Background(), cs, "test-ns", true); err != nil {
		t.Fatalf("runCleaner(dryRun=true) error: %v", err)
	}

	api.mu.Lock()
	deleted := api.deleted
	api.mu.Unlock()
	if len(deleted) != 0 {
		t.Errorf("runCleaner dry-run deleted pods: %v (should not delete)", deleted)
	}
}

func TestRunCleaner_NoAnnotation_SkipsPod(t *testing.T) {
	bare := podNoAnnotation("bare-pod", "test-ns")
	api := &cleanerFakeAPI{pods: []v1.Pod{bare}}
	cs := newCleanerClientset(t, api)

	if err := runCleaner(context.Background(), cs, "test-ns", false); err != nil {
		t.Fatalf("runCleaner error: %v", err)
	}

	api.mu.Lock()
	deleted := api.deleted
	api.mu.Unlock()
	if len(deleted) != 0 {
		t.Errorf("runCleaner deleted pod with no annotation: %v", deleted)
	}
}

func TestRunCleaner_InvalidAnnotation_SkipsPod(t *testing.T) {
	bad := podNoAnnotation("bad-pod", "test-ns")
	bad.Annotations = map[string]string{expiresAtAnnotation: "not-a-timestamp"}
	api := &cleanerFakeAPI{pods: []v1.Pod{bad}}
	cs := newCleanerClientset(t, api)

	if err := runCleaner(context.Background(), cs, "test-ns", false); err != nil {
		t.Fatalf("runCleaner error: %v", err)
	}

	api.mu.Lock()
	deleted := api.deleted
	api.mu.Unlock()
	if len(deleted) != 0 {
		t.Errorf("runCleaner deleted pod with invalid annotation: %v", deleted)
	}
}

func TestRunCleaner_MultipleExpired_DeletesAll(t *testing.T) {
	pods := []v1.Pod{
		podWithExpiry("exp-1", "test-ns", time.Now().Add(-2*time.Hour)),
		podWithExpiry("exp-2", "test-ns", time.Now().Add(-1*time.Hour)),
		podWithExpiry("active", "test-ns", time.Now().Add(1*time.Hour)),
	}
	api := &cleanerFakeAPI{pods: pods}
	cs := newCleanerClientset(t, api)

	if err := runCleaner(context.Background(), cs, "test-ns", false); err != nil {
		t.Fatalf("runCleaner error: %v", err)
	}

	api.mu.Lock()
	deleted := api.deleted
	api.mu.Unlock()
	if len(deleted) != 2 {
		t.Errorf("runCleaner deleted %v, want 2 expired pods", deleted)
	}
	for _, d := range deleted {
		if d == "active" {
			t.Error("runCleaner deleted the active pod — should not have")
		}
	}
}

func TestRunCleaner_EmptyNamespace_NoError(t *testing.T) {
	api := &cleanerFakeAPI{pods: []v1.Pod{}}
	cs := newCleanerClientset(t, api)

	if err := runCleaner(context.Background(), cs, "test-ns", false); err != nil {
		t.Errorf("runCleaner on empty namespace error: %v", err)
	}
}

func TestRunCleaner_ListFails_ReturnsError(t *testing.T) {
	api := &cleanerFakeAPI{pods: []v1.Pod{}, forceListError: true}
	cs := newCleanerClientset(t, api)

	err := runCleaner(context.Background(), cs, "test-ns", false)
	if err == nil {
		t.Error("runCleaner expected error when list fails, got nil")
	}
	if !strings.Contains(err.Error(), "list") {
		t.Errorf("runCleaner error should mention list: %v", err)
	}
}

func TestRunCleaner_DeleteFails_Continues(t *testing.T) {
	expired := podWithExpiry("fail-delete-pod", "test-ns", time.Now().Add(-1*time.Hour))
	api := &cleanerFakeAPI{pods: []v1.Pod{expired}, forceDeleteError: "fail-delete-pod"}
	cs := newCleanerClientset(t, api)

	if err := runCleaner(context.Background(), cs, "test-ns", false); err != nil {
		t.Fatalf("runCleaner should not return error when delete fails (continues): %v", err)
	}
	api.mu.Lock()
	deleted := api.deleted
	api.mu.Unlock()
	if len(deleted) != 0 {
		t.Errorf("delete failed so pod should not be in deleted list: %v", deleted)
	}
}

// ---- runWorkloadCleaner tests -----------------------------------------------

func TestRunWorkloadCleaner_DeletesExpiredWorkload(t *testing.T) {
	expired := podWithExpiry("wl-old", "test-ns", time.Now().Add(-30*time.Minute))
	api := &cleanerFakeAPI{pods: []v1.Pod{expired}}
	cs := newCleanerClientset(t, api)

	if err := runWorkloadCleaner(context.Background(), cs, "test-ns", false); err != nil {
		t.Fatalf("runWorkloadCleaner error: %v", err)
	}

	api.mu.Lock()
	deleted := api.deleted
	api.mu.Unlock()
	if len(deleted) != 1 || deleted[0] != "wl-old" {
		t.Errorf("runWorkloadCleaner deleted %v, want [wl-old]", deleted)
	}
}

func TestRunWorkloadCleaner_SkipsActiveWorkload(t *testing.T) {
	active := podWithExpiry("wl-live", "test-ns", time.Now().Add(1*time.Hour))
	api := &cleanerFakeAPI{pods: []v1.Pod{active}}
	cs := newCleanerClientset(t, api)

	if err := runWorkloadCleaner(context.Background(), cs, "test-ns", false); err != nil {
		t.Fatalf("runWorkloadCleaner error: %v", err)
	}

	api.mu.Lock()
	deleted := api.deleted
	api.mu.Unlock()
	if len(deleted) != 0 {
		t.Errorf("runWorkloadCleaner deleted active workload: %v", deleted)
	}
}

func TestRunWorkloadCleaner_DryRun_DoesNotDelete(t *testing.T) {
	expired := podWithExpiry("wl-exp", "test-ns", time.Now().Add(-1*time.Hour))
	api := &cleanerFakeAPI{pods: []v1.Pod{expired}}
	cs := newCleanerClientset(t, api)

	if err := runWorkloadCleaner(context.Background(), cs, "test-ns", true); err != nil {
		t.Fatalf("runWorkloadCleaner(dryRun=true) error: %v", err)
	}

	api.mu.Lock()
	deleted := api.deleted
	api.mu.Unlock()
	if len(deleted) != 0 {
		t.Errorf("runWorkloadCleaner dry-run deleted %v (should not delete)", deleted)
	}
}

func TestRunWorkloadCleaner_NoAnnotation_Skips(t *testing.T) {
	bare := podNoAnnotation("bare-wl", "test-ns")
	api := &cleanerFakeAPI{pods: []v1.Pod{bare}}
	cs := newCleanerClientset(t, api)

	if err := runWorkloadCleaner(context.Background(), cs, "test-ns", false); err != nil {
		t.Fatalf("runWorkloadCleaner error: %v", err)
	}

	api.mu.Lock()
	deleted := api.deleted
	api.mu.Unlock()
	if len(deleted) != 0 {
		t.Errorf("runWorkloadCleaner deleted pod with no annotation: %v", deleted)
	}
}

func TestRunWorkloadCleaner_InvalidAnnotation_Skips(t *testing.T) {
	bad := podNoAnnotation("bad-wl", "test-ns")
	bad.Annotations = map[string]string{expiresAtAnnotation: "garbage"}
	api := &cleanerFakeAPI{pods: []v1.Pod{bad}}
	cs := newCleanerClientset(t, api)

	if err := runWorkloadCleaner(context.Background(), cs, "test-ns", false); err != nil {
		t.Fatalf("runWorkloadCleaner error: %v", err)
	}

	api.mu.Lock()
	deleted := api.deleted
	api.mu.Unlock()
	if len(deleted) != 0 {
		t.Errorf("runWorkloadCleaner deleted pod with invalid annotation: %v", deleted)
	}
}

func TestRunWorkloadCleaner_MixedExpiry_DeletesOnlyExpired(t *testing.T) {
	pods := []v1.Pod{
		podWithExpiry("wl-expired-1", "test-ns", time.Now().Add(-5*time.Minute)),
		podWithExpiry("wl-active", "test-ns", time.Now().Add(10*time.Minute)),
		podWithExpiry("wl-expired-2", "test-ns", time.Now().Add(-2*time.Hour)),
	}
	api := &cleanerFakeAPI{pods: pods}
	cs := newCleanerClientset(t, api)

	if err := runWorkloadCleaner(context.Background(), cs, "test-ns", false); err != nil {
		t.Fatalf("runWorkloadCleaner error: %v", err)
	}

	api.mu.Lock()
	deleted := api.deleted
	api.mu.Unlock()
	if len(deleted) != 2 {
		t.Errorf("runWorkloadCleaner deleted %d pods, want 2", len(deleted))
	}
	for _, d := range deleted {
		if d == "wl-active" {
			t.Error("runWorkloadCleaner deleted the active workload")
		}
	}
}

func TestRunWorkloadCleaner_EmptyNamespace_NoError(t *testing.T) {
	api := &cleanerFakeAPI{pods: []v1.Pod{}}
	cs := newCleanerClientset(t, api)

	if err := runWorkloadCleaner(context.Background(), cs, "test-ns", false); err != nil {
		t.Errorf("runWorkloadCleaner on empty namespace error: %v", err)
	}
}

func TestRunWorkloadCleaner_ListFails_ReturnsError(t *testing.T) {
	api := &cleanerFakeAPI{pods: []v1.Pod{}, forceListError: true}
	cs := newCleanerClientset(t, api)

	err := runWorkloadCleaner(context.Background(), cs, "test-ns", false)
	if err == nil {
		t.Error("runWorkloadCleaner expected error when list fails, got nil")
	}
	if !strings.Contains(err.Error(), "list") {
		t.Errorf("runWorkloadCleaner error should mention list: %v", err)
	}
}

func TestRunWorkloadCleaner_DeleteFails_Continues(t *testing.T) {
	expired := podWithLabel("wl-fail-del", "test-ns", workloadAppLabel, time.Now().Add(-1*time.Hour))
	api := &cleanerFakeAPI{pods: []v1.Pod{expired}, forceDeleteError: "wl-fail-del"}
	cs := newCleanerClientset(t, api)

	if err := runWorkloadCleaner(context.Background(), cs, "test-ns", false); err != nil {
		t.Fatalf("runWorkloadCleaner should not return error when delete fails: %v", err)
	}
	api.mu.Lock()
	deleted := api.deleted
	api.mu.Unlock()
	if len(deleted) != 0 {
		t.Errorf("delete failed so pod should not be in deleted list: %v", deleted)
	}
}

// ---- DryRun counts (both functions count dry-run deletes in expiredCount) ---

func TestRunCleaner_DryRun_CountsExpiredWithoutDeleting(t *testing.T) {
	pods := []v1.Pod{
		podWithExpiry("e1", "test-ns", time.Now().Add(-1*time.Hour)),
		podWithExpiry("e2", "test-ns", time.Now().Add(-2*time.Hour)),
	}
	api := &cleanerFakeAPI{pods: pods}
	cs := newCleanerClientset(t, api)

	// Should complete without error even in dry-run
	if err := runCleaner(context.Background(), cs, "test-ns", true); err != nil {
		t.Fatalf("runCleaner(dryRun=true) error: %v", err)
	}

	api.mu.Lock()
	deleted := api.deleted
	api.mu.Unlock()
	if len(deleted) != 0 {
		t.Errorf("dry-run must not call DELETE; got: %v", deleted)
	}
}
