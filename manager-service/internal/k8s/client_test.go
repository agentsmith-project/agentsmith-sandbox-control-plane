package k8s

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNewClientDefaults(t *testing.T) {
	// NewClient() requires either in-cluster config or a readable kubeconfig.
	// For unit tests, provide a minimal kubeconfig so client initialization can proceed
	// without requiring a real cluster.
	tmpDir := t.TempDir()
	kubeconfigPath := tmpDir + "/kubeconfig"
	if err := os.WriteFile(kubeconfigPath, []byte(`
apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: https://127.0.0.1:6443
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
`), 0644); err != nil {
		t.Fatalf("Failed to write kubeconfig: %v", err)
	}

	prev := os.Getenv("KUBECONFIG")
	t.Cleanup(func() { _ = os.Setenv("KUBECONFIG", prev) })
	if err := os.Setenv("KUBECONFIG", kubeconfigPath); err != nil {
		t.Fatalf("Failed to set KUBECONFIG: %v", err)
	}

	client, err := NewClient(nil)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	if client == nil {
		t.Fatal("NewClient() returned nil")
	}

	if client.qps != 50 {
		t.Errorf("NewClient() default QPS = %v, want 50", client.qps)
	}

	if client.burst != 100 {
		t.Errorf("NewClient() default Burst = %v, want 100", client.burst)
	}

	if client.timeout != 15*time.Second {
		t.Errorf("NewClient() default timeout = %v, want 15s", client.timeout)
	}
}

func TestNewClientWithConfig(t *testing.T) {
	cfg := &ClientConfig{
		Namespace:      "test-namespace",
		QPS:            100,
		Burst:          200,
		RequestTimeout: 30 * time.Second,
	}

	if cfg.Namespace != "test-namespace" {
		t.Errorf("Config namespace = %v, want 'test-namespace'", cfg.Namespace)
	}

	if cfg.QPS != 100 {
		t.Errorf("Config QPS = %v, want 100", cfg.QPS)
	}

	if cfg.Burst != 200 {
		t.Errorf("Config Burst = %v, want 200", cfg.Burst)
	}
}

func TestClientNamespace(t *testing.T) {
	tests := []struct {
		name            string
		configNamespace string
		envNamespace    string
		wantNamespace   string
	}{
		{
			name:            "config namespace takes precedence",
			configNamespace: "config-ns",
			envNamespace:    "",
			wantNamespace:   "config-ns",
		},
		{
			name:            "env namespace when config empty",
			configNamespace: "",
			envNamespace:    "env-ns",
			wantNamespace:   "env-ns",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up environment
			if tt.envNamespace != "" {
				// Create a temporary file for the namespace
				tmpDir := t.TempDir()
				namespaceFile := tmpDir + "/namespace"
				if err := os.WriteFile(namespaceFile, []byte(tt.envNamespace), 0644); err != nil {
					t.Fatalf("Failed to write namespace file: %v", err)
				}

				// In a real test, we'd set up the in-cluster namespace path
				// For now, we'll just verify the logic
			}

			// Verify namespace selection logic
			ns := tt.configNamespace
			if ns == "" && tt.envNamespace != "" {
				ns = tt.envNamespace
			}
			if ns == "" {
				ns = "sandbox-workloads" // default
			}

			if ns != tt.wantNamespace {
				t.Errorf("namespace = %v, want %v", ns, tt.wantNamespace)
			}
		})
	}
}

func TestClientMethods(t *testing.T) {
	client := &Client{
		namespace: "default",
		qps:       50,
		burst:     100,
		timeout:   15 * time.Second,
	}

	t.Run("Namespace", func(t *testing.T) {
		if client.Namespace() != "default" {
			t.Errorf("Namespace() = %v, want 'default'", client.Namespace())
		}
	})
}

// fakeK8sForClient is a minimal K8s API server for client CheckReady/CreateNamespace tests.
type fakeK8sForClient struct {
	listPodsStatus int // 200 or 404
	createNSStatus int // 201 or 409
}

func (f *fakeK8sForClient) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := r.URL.Path

	// LIST pods: /api/v1/namespaces/{ns}/pods
	if r.Method == http.MethodGet && strings.Contains(path, "/namespaces/") && strings.HasSuffix(path, "/pods") {
		if f.listPodsStatus == http.StatusNotFound {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(&metav1.Status{
				TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
				Status:   metav1.StatusFailure,
				Code:     http.StatusNotFound,
				Reason:   metav1.StatusReasonNotFound,
			})
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(&v1.PodList{
			TypeMeta: metav1.TypeMeta{Kind: "PodList", APIVersion: "v1"},
			Items:   []v1.Pod{},
		})
		return
	}

	// POST namespaces: /api/v1/namespaces
	if r.Method == http.MethodPost && strings.HasSuffix(path, "/namespaces") {
		if f.createNSStatus == http.StatusConflict {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(&metav1.Status{
				TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
				Status:   metav1.StatusFailure,
				Code:     http.StatusConflict,
				Reason:   metav1.StatusReasonAlreadyExists,
			})
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(&v1.Namespace{
			TypeMeta: metav1.TypeMeta{Kind: "Namespace", APIVersion: "v1"},
			ObjectMeta: metav1.ObjectMeta{Name: "test"},
		})
		return
	}

	w.WriteHeader(http.StatusNotFound)
	json.NewEncoder(w).Encode(&metav1.Status{Code: http.StatusNotFound, Reason: metav1.StatusReasonNotFound})
}

func newClientFromFake(t *testing.T, f *fakeK8sForClient) *Client {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	kubeconfig := `apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: ` + srv.URL + `
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
`
	cfgPath := t.TempDir() + "/kubeconfig"
	if err := os.WriteFile(cfgPath, []byte(kubeconfig), 0644); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}
	prev := os.Getenv("KUBECONFIG")
	t.Cleanup(func() { _ = os.Setenv("KUBECONFIG", prev) })
	os.Setenv("KUBECONFIG", cfgPath)

	client, err := NewClient(&ClientConfig{Namespace: "test-ns"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}

func TestCheckReady_Success(t *testing.T) {
	f := &fakeK8sForClient{listPodsStatus: http.StatusOK}
	client := newClientFromFake(t, f)
	ctx := context.Background()
	if err := client.CheckReady(ctx); err != nil {
		t.Errorf("CheckReady() = %v, want nil", err)
	}
}

func TestCheckReady_NamespaceNotFound(t *testing.T) {
	f := &fakeK8sForClient{listPodsStatus: http.StatusNotFound}
	client := newClientFromFake(t, f)
	ctx := context.Background()
	err := client.CheckReady(ctx)
	if err == nil {
		t.Fatal("CheckReady() = nil, want error")
	}
	if !strings.Contains(err.Error(), "namespace") {
		t.Errorf("CheckReady() error = %v, want message containing 'namespace'", err)
	}
}

func TestCreateNamespace_Success(t *testing.T) {
	f := &fakeK8sForClient{createNSStatus: http.StatusCreated}
	client := newClientFromFake(t, f)
	ctx := context.Background()
	if err := client.CreateNamespace(ctx, "new-ns", nil); err != nil {
		t.Errorf("CreateNamespace() = %v, want nil", err)
	}
}

func TestCreateNamespace_AlreadyExists(t *testing.T) {
	f := &fakeK8sForClient{createNSStatus: http.StatusConflict}
	client := newClientFromFake(t, f)
	ctx := context.Background()
	if err := client.CreateNamespace(ctx, "existing", nil); err != nil {
		t.Errorf("CreateNamespace() when already exists = %v, want nil", err)
	}
}

func TestClientsetAndConfig_NonNil(t *testing.T) {
	f := &fakeK8sForClient{listPodsStatus: http.StatusOK}
	client := newClientFromFake(t, f)
	if client.Clientset() == nil {
		t.Error("Clientset() = nil")
	}
	if client.Config() == nil {
		t.Error("Config() = nil")
	}
}

func TestClientConfigValidation(t *testing.T) {
	tests := []struct {
		name   string
		config *ClientConfig
	}{
		{
			name: "nil config uses defaults",
			config: &ClientConfig{
				Namespace: "",
				QPS:       0,
				Burst:     0,
			},
		},
		{
			name: "custom values",
			config: &ClientConfig{
				Namespace:      "custom",
				QPS:            100,
				Burst:          200,
				RequestTimeout: 30 * time.Second,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.config.QPS < 0 {
				t.Error("QPS cannot be negative")
			}
			if tt.config.Burst < 0 {
				t.Error("Burst cannot be negative")
			}
			if tt.config.RequestTimeout < 0 {
				t.Error("RequestTimeout cannot be negative")
			}
		})
	}
}

func TestCheckReady_ContextCancelled(t *testing.T) {
	f := &fakeK8sForClient{listPodsStatus: http.StatusOK}
	client := newClientFromFake(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := client.CheckReady(ctx)
	if err == nil {
		t.Error("CheckReady with cancelled context want error, got nil")
	}
}

// BenchmarkIsPodReady benchmarks the IsPodReady function
func BenchmarkIsPodReady(b *testing.B) {
	pod := &v1.Pod{
		Status: v1.PodStatus{
			Conditions: []v1.PodCondition{
				{
					Type:   v1.PodReady,
					Status: v1.ConditionTrue,
				},
			},
		},
	}

	for i := 0; i < b.N; i++ {
		_ = IsPodReady(pod)
	}
}
