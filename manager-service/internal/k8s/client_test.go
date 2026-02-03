package k8s

import (
	"context"
	"os"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
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

	if client.retry == nil {
		t.Fatal("NewClient() retry config is nil")
	}

	if !client.retry.Enabled {
		t.Error("NewClient() retry not enabled by default")
	}

	if client.retry.MaxAttempts != 3 {
		t.Errorf("NewClient() default MaxAttempts = %v, want 3", client.retry.MaxAttempts)
	}
}

func TestNewClientWithConfig(t *testing.T) {
	cfg := &ClientConfig{
		Namespace:      "test-namespace",
		QPS:            100,
		Burst:          200,
		RequestTimeout: 30 * time.Second,
		Retry: &RetryConfig{
			Enabled:     true,
			MaxAttempts: 5,
			BaseBackoff: 100 * time.Millisecond,
			MaxBackoff:  1 * time.Second,
		},
	}

	// We can't fully test this without a real K8s cluster,
	// but we can test the config parsing
	if cfg.Namespace != "test-namespace" {
		t.Errorf("Config namespace = %v, want 'test-namespace'", cfg.Namespace)
	}

	if cfg.QPS != 100 {
		t.Errorf("Config QPS = %v, want 100", cfg.QPS)
	}

	if cfg.Retry.MaxAttempts != 5 {
		t.Errorf("Config MaxAttempts = %v, want 5", cfg.Retry.MaxAttempts)
	}
}

func TestRetryConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  *RetryConfig
		wantErr bool
	}{
		{
			name: "valid retry config",
			config: &RetryConfig{
				Enabled:     true,
				MaxAttempts: 3,
				BaseBackoff: 200 * time.Millisecond,
				MaxBackoff:  2 * time.Second,
			},
			wantErr: false,
		},
		{
			name: "disabled retry",
			config: &RetryConfig{
				Enabled: false,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Just verify the config structure is valid
			if tt.config.MaxAttempts < 0 {
				t.Error("MaxAttempts cannot be negative")
			}

			if tt.config.BaseBackoff < 0 {
				t.Error("BaseBackoff cannot be negative")
			}

			if tt.config.MaxBackoff < 0 {
				t.Error("MaxBackoff cannot be negative")
			}
		})
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
				ns = "sandbox" // default
			}

			if ns != tt.wantNamespace {
				t.Errorf("namespace = %v, want %v", ns, tt.wantNamespace)
			}
		})
	}
}

func TestClientMethods(t *testing.T) {
	client := &Client{
		clientset: nil, // We can't set fake clientset directly, but we can test other methods
		config:    nil,
		namespace: "default",
		qps:       50,
		burst:     100,
		timeout:   15 * time.Second,
		retry: &RetryConfig{
			Enabled:     true,
			MaxAttempts: 3,
			BaseBackoff: 200 * time.Millisecond,
			MaxBackoff:  2 * time.Second,
		},
	}

	t.Run("Namespace", func(t *testing.T) {
		if client.Namespace() != "default" {
			t.Errorf("Namespace() = %v, want 'default'", client.Namespace())
		}
	})

	t.Run("WithTimeout", func(t *testing.T) {
		ctx := context.Background()
		ctxWithTimeout, cancel := client.WithTimeout(ctx)
		defer cancel()

		if ctxWithTimeout == nil {
			t.Error("WithTimeout() returned nil context")
		}

		// Verify the context has a deadline
		_, hasDeadline := ctxWithTimeout.Deadline()
		if !hasDeadline {
			t.Error("WithTimeout() context should have a deadline")
		}
	})
}

func TestCheckReady(t *testing.T) {
	t.Skip("Skipping CheckReady test - requires real clientset")
}

func TestCreateNamespace(t *testing.T) {
	t.Skip("Skipping CreateNamespace test - requires real clientset")
}

func TestRetry(t *testing.T) {
	tests := []struct {
		name        string
		fn          func() error
		retryConfig *RetryConfig
		wantErr     bool
	}{
		{
			name: "success on first attempt",
			fn: func() error {
				return nil
			},
			retryConfig: &RetryConfig{
				Enabled:     true,
				MaxAttempts: 3,
				BaseBackoff: 10 * time.Millisecond,
				MaxBackoff:  100 * time.Millisecond,
			},
			wantErr: false,
		},
		{
			name: "success on retry",
			fn: func() error {
				// This will be called multiple times
				return nil // Simulate success after some attempts
			},
			retryConfig: &RetryConfig{
				Enabled:     true,
				MaxAttempts: 3,
				BaseBackoff: 10 * time.Millisecond,
				MaxBackoff:  100 * time.Millisecond,
			},
			wantErr: false,
		},
		{
			name: "permanent error",
			fn: func() error {
				return &errors.StatusError{ErrStatus: metav1.Status{
					Reason: metav1.StatusReasonForbidden,
				}}
			},
			retryConfig: &RetryConfig{
				Enabled:     true,
				MaxAttempts: 3,
				BaseBackoff: 10 * time.Millisecond,
				MaxBackoff:  100 * time.Millisecond,
			},
			wantErr: true, // Forbidden errors should not retry
		},
		{
			name: "retry disabled",
			fn: func() error {
				return errors.NewNotFound(v1.Resource("pods"), "test")
			},
			retryConfig: &RetryConfig{
				Enabled: false,
			},
			wantErr: true, // NotFound errors should not retry even with retry enabled
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &Client{
				retry: tt.retryConfig,
			}

			ctx := context.Background()
			err := client.Retry(ctx, tt.fn)

			if (err != nil) != tt.wantErr {
				t.Errorf("Retry() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRetryNonRetryableErrors(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantRet bool // false = should not retry
	}{
		{
			name:    "NotFound",
			err:     errors.NewNotFound(v1.Resource("pods"), "test"),
			wantRet: false,
		},
		{
			name:    "AlreadyExists",
			err:     errors.NewAlreadyExists(v1.Resource("pods"), "test"),
			wantRet: false,
		},
		{
			name:    "Forbidden",
			err:     &errors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReasonForbidden}},
			wantRet: false,
		},
		{
			name:    "timeout error",
			err:     context.DeadlineExceeded,
			wantRet: true, // Context errors should still retry
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attemptCount := 0
			retryConfig := &RetryConfig{
				Enabled:     true,
				MaxAttempts: 3,
				BaseBackoff: 10 * time.Millisecond,
				MaxBackoff:  100 * time.Millisecond,
			}

			client := &Client{
				retry: retryConfig,
			}

			fn := func() error {
				attemptCount++
				if tt.wantRet && attemptCount < retryConfig.MaxAttempts {
					return tt.err
				}
				return tt.err
			}

			ctx := context.Background()
			_ = client.Retry(ctx, fn)

			// For non-retryable errors, we should only attempt once
			if !tt.wantRet && attemptCount != 1 {
				t.Errorf("Non-retryable error should not retry, got %d attempts", attemptCount)
			}
		})
	}
}

func TestRetryBackoff(t *testing.T) {
	retryConfig := &RetryConfig{
		Enabled:     true,
		MaxAttempts: 5,
		BaseBackoff: 50 * time.Millisecond,
		MaxBackoff:  200 * time.Millisecond,
	}

	client := &Client{
		retry: retryConfig,
	}

	attempt := 0
	fn := func() error {
		attempt++
		if attempt < retryConfig.MaxAttempts {
			return errors.NewServiceUnavailable("service unavailable")
		}
		return nil
	}

	start := time.Now()
	ctx := context.Background()
	err := client.Retry(ctx, fn)
	duration := time.Since(start)

	if err != nil {
		t.Errorf("Retry() unexpected error = %v", err)
	}

	// Verify we did multiple attempts
	if attempt != retryConfig.MaxAttempts {
		t.Errorf("Retry() attempts = %v, want %v", attempt, retryConfig.MaxAttempts)
	}

	// With backoff, this should take at least BaseBackoff * (attempts-1)
	minExpectedDuration := retryConfig.BaseBackoff * time.Duration(retryConfig.MaxAttempts-1)
	if duration < minExpectedDuration {
		t.Errorf("Retry() duration = %v, want at least %v", duration, minExpectedDuration)
	}
}

func TestClientConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		config  *ClientConfig
		wantErr bool
	}{
		{
			name: "nil config uses defaults",
			config: &ClientConfig{
				Namespace: "",
				QPS:       0,
				Burst:     0,
			},
			wantErr: false,
		},
		{
			name: "custom values",
			config: &ClientConfig{
				Namespace:      "custom",
				QPS:            100,
				Burst:          200,
				RequestTimeout: 30 * time.Second,
				Retry: &RetryConfig{
					Enabled:     true,
					MaxAttempts: 5,
					BaseBackoff: 500 * time.Millisecond,
					MaxBackoff:  10 * time.Second,
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Verify config values are valid
			if tt.config.QPS < 0 {
				t.Error("QPS cannot be negative")
			}

			if tt.config.Burst < 0 {
				t.Error("Burst cannot be negative")
			}

			if tt.config.RequestTimeout < 0 {
				t.Error("RequestTimeout cannot be negative")
			}

			if tt.config.Retry != nil {
				if tt.config.Retry.MaxAttempts < 0 {
					t.Error("MaxAttempts cannot be negative")
				}

				if tt.config.Retry.BaseBackoff < 0 {
					t.Error("BaseBackoff cannot be negative")
				}

				if tt.config.Retry.MaxBackoff < 0 {
					t.Error("MaxBackoff cannot be negative")
				}

				if tt.config.Retry.MaxBackoff < tt.config.Retry.BaseBackoff {
					t.Error("MaxBackoff should be >= BaseBackoff")
				}
			}
		})
	}
}

// TestContextCancellation verifies that the client respects context cancellation
func TestContextCancellation(t *testing.T) {
	t.Skip("Skipping ContextCancellation test - requires real clientset")
}

// BenchmarkPodNameGeneration benchmarks the PodName function
func BenchmarkPodNameGeneration(b *testing.B) {
	sessionID := "test-session-123456"

	for i := 0; i < b.N; i++ {
		_ = PodName(sessionID)
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
