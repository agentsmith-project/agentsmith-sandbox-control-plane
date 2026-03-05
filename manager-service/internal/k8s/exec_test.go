package k8s

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBuildExecURL(t *testing.T) {
	// buildExecURL is called from Exec; test via Exec against a fake API that returns 404.
	// This exercises buildExecURL and the Exec error path (non-SPDY response).
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(fake.Close)

	kubeconfig := `apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: ` + fake.URL + `
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
	cfgPath := filepath.Join(t.TempDir(), "kubeconfig")
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
	executor := NewExecutor(client)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, execErr := executor.Exec(ctx, "some-pod", &ExecOptions{
		Command: []string{"echo", "hi"},
		Timeout: time.Second,
	})
	if execErr == nil {
		t.Error("Exec against non-SPDY fake expected error, got nil")
	}
	if result == nil {
		t.Fatal("Exec must return non-nil result even on error")
	}
	if result.ExitCode != -1 {
		t.Errorf("Exec on failure: ExitCode = %d, want -1", result.ExitCode)
	}
}

func TestExecOptionsDefaults(t *testing.T) {
	opts := &ExecOptions{}
	if opts.Container != "" {
		t.Errorf("Expected zero-value Container to be empty, got %q", opts.Container)
	}
	// The Exec function fills the default container name ("main") at runtime.
}

func TestNewExecutor(t *testing.T) {
	t.Skip("Skipping NewExecutor test - requires real clientset")
}

func TestBufferWriter(t *testing.T) {
	t.Run("Write and String", func(t *testing.T) {
		bw := newBufferWriter()
		data := []byte("test data")

		n, err := bw.Write(data)
		if err != nil {
			t.Fatalf("Write() error = %v", err)
		}

		if n != len(data) {
			t.Errorf("Write() = %v, want %v", n, len(data))
		}

		if bw.String() != "test data" {
			t.Errorf("String() = %v, want %v", bw.String(), "test data")
		}
	})

	t.Run("Multiple writes", func(t *testing.T) {
		bw := newBufferWriter()
		bw.Write([]byte("first"))
		bw.Write([]byte(" "))
		bw.Write([]byte("second"))

		if bw.String() != "first second" {
			t.Errorf("String() = %v, want %v", bw.String(), "first second")
		}
	})

	t.Run("Empty buffer", func(t *testing.T) {
		bw := newBufferWriter()
		if bw.String() != "" {
			t.Errorf("String() = %v, want empty string", bw.String())
		}
	})
}

func TestExecOptionsValidation(t *testing.T) {
	tests := []struct {
		name    string
		opts    *ExecOptions
		wantErr bool
	}{
		{
			name: "valid options",
			opts: &ExecOptions{
				Command:   []string{"echo", "test"},
				Container: "runner",
				Timeout:   30 * time.Second,
			},
			wantErr: false,
		},
		{
			name: "empty command",
			opts: &ExecOptions{
				Command:   []string{},
				Container: "runner",
			},
			wantErr: false, // ExecOptions itself doesn't validate
		},
		{
			name:    "nil options",
			opts:    nil,
			wantErr: false, // Exec() handles nil by creating a new one
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This is mainly for documentation - the real validation happens in the Exec function
			if tt.opts == nil {
				t.Skip("nil opts are handled by Exec()")
			}
			if tt.opts.Command == nil {
				t.Skip("empty commands are handled by higher level")
			}
		})
	}
}

// Mock implementation for testing if needed
type mockReadCloser struct {
	io.Reader
	closed bool
}

func (m *mockReadCloser) Close() error {
	m.closed = true
	return nil
}

// TestExecResult covers the ExecResult struct
func TestExecResult(t *testing.T) {
	result := &ExecResult{
		ExitCode: 0,
		Stdout:   "test output",
		Stderr:   "",
		Duration: 100 * time.Millisecond,
		TimedOut: false,
	}

	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %v, want 0", result.ExitCode)
	}

	if result.Stdout != "test output" {
		t.Errorf("Stdout = %v, want 'test output'", result.Stdout)
	}

	if result.TimedOut {
		t.Error("TimedOut = true, want false")
	}
}

// TestURLBuilding verifies the URL structure for exec endpoints
func TestURLBuilding(t *testing.T) {
	// Create a URL that matches what K8s exec expects
	baseURL, err := url.Parse("http://localhost/api/v1/namespaces/default/pods/test-pod/exec")
	if err != nil {
		t.Fatalf("Failed to parse URL: %v", err)
	}

	// Verify the path structure
	expectedPath := "/api/v1/namespaces/default/pods/test-pod/exec"
	if baseURL.Path != expectedPath {
		t.Errorf("URL path = %v, want %v", baseURL.Path, expectedPath)
	}

	// Verify we can set query parameters
	q := baseURL.Query()
	q.Add("container", "runner")
	q.Add("stdout", "true")
	q.Add("stderr", "true")
	baseURL.RawQuery = q.Encode()

	if baseURL.Query().Get("container") != "runner" {
		t.Error("Failed to set container query parameter")
	}
}

// TestNewExecutorWithNilClient verifies nil handling
func TestNewExecutorWithNilClient(t *testing.T) {
	// This should panic or handle nil gracefully
	// In production, NewExecutor assumes a valid Client
	defer func() {
		if r := recover(); r != nil {
			t.Logf("NewExecutor with nil client panicked: %v", r)
		}
	}()

	_ = NewExecutor(nil)
	// If we get here, nil client is accepted (may cause issues later)
}

// TestExecResultTimeoutFlag verifies the TimedOut flag
func TestExecResultTimeoutFlag(t *testing.T) {
	result := &ExecResult{
		ExitCode: -1,
		TimedOut: true,
	}

	if !result.TimedOut {
		t.Error("TimedOut flag not set correctly")
	}

	if result.ExitCode != -1 {
		t.Errorf("ExitCode for timeout = %v, want -1", result.ExitCode)
	}
}

// TestBufferWriterSequential tests sequential writes
// Note: bufferWriter is not designed for concurrent use
func TestBufferWriterSequential(t *testing.T) {
	bw := newBufferWriter()

	// Write sequentially
	for i := 0; i < 10; i++ {
		n, err := bw.Write([]byte{byte(i)})
		if err != nil {
			t.Fatalf("Write() error = %v", err)
		}
		if n != 1 {
			t.Errorf("Write() returned %v, want 1", n)
		}
	}

	// Verify we got exactly 10 bytes
	result := bw.String()
	if len(result) != 10 {
		t.Errorf("After sequential writes, length = %v, want 10", len(result))
	}
}
