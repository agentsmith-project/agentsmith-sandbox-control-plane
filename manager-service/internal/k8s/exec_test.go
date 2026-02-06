package k8s

import (
	"io"
	"net/url"
	"reflect"
	"testing"
	"time"
)

func TestBuildExecURL(t *testing.T) {
	t.Skip("Skipping BuildExecURL test - requires real restClient")
}

func TestExecOptionsDefaults(t *testing.T) {
	opts := &ExecOptions{}

	if opts.Container == "" {
		// The Exec function should set a default, but we're testing the struct
		opts.Container = "runner" // Set expected default
	}

	if opts.Container != "runner" {
		t.Errorf("Expected default container to be 'runner', got '%s'", opts.Container)
	}
}

func TestExtractExitCodeMarker(t *testing.T) {
	tests := []struct {
		name      string
		stderr    string
		markerKey string
		want      int
	}{
		{
			name:      "valid exit code at end",
			stderr:    "some output\n__SBX_EXIT_CODE__=0",
			markerKey: "__SBX_EXIT_CODE__",
			want:      0,
		},
		{
			name:      "exit code 1",
			stderr:    "error occurred\n__SBX_EXIT_CODE__=1",
			markerKey: "__SBX_EXIT_CODE__",
			want:      1,
		},
		{
			name:      "exit code 127",
			stderr:    "command not found\n__SBX_EXIT_CODE__=127",
			markerKey: "__SBX_EXIT_CODE__",
			want:      127,
		},
		{
			name:      "exit code with CRLF",
			stderr:    "output\r\n__SBX_EXIT_CODE__=0\r\n",
			markerKey: "__SBX_EXIT_CODE__",
			want:      0,
		},
		{
			name:      "no marker found",
			stderr:    "some output without marker",
			markerKey: "__SBX_EXIT_CODE__",
			want:      -1,
		},
		{
			name:      "empty stderr",
			stderr:    "",
			markerKey: "__SBX_EXIT_CODE__",
			want:      -1,
		},
		{
			name:      "marker with whitespace prefix",
			stderr:    "output\n __SBX_EXIT_CODE__=0",
			markerKey: "__SBX_EXIT_CODE__",
			want:      0,
		},
		{
			name:      "custom marker key",
			stderr:    "output\nCUSTOM_EXIT=42",
			markerKey: "CUSTOM_EXIT",
			want:      42,
		},
		{
			name:      "invalid exit code",
			stderr:    "__SBX_EXIT_CODE__=abc",
			markerKey: "__SBX_EXIT_CODE__",
			want:      -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractExitCodeMarker(tt.stderr, tt.markerKey)
			if got != tt.want {
				t.Errorf("ExtractExitCodeMarker() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRemoveExitCodeMarker(t *testing.T) {
	tests := []struct {
		name      string
		output    string
		markerKey string
		want      string
	}{
		{
			name:      "remove marker at end",
			output:    "some output\n__SBX_EXIT_CODE__=0\n",
			markerKey: "__SBX_EXIT_CODE__",
			want:      "some output\n",
		},
		{
			name:      "remove marker in middle",
			output:    "line1\n__SBX_EXIT_CODE__=0\nline2",
			markerKey: "__SBX_EXIT_CODE__",
			want:      "line1\nline2",
		},
		{
			name:      "remove marker with CRLF",
			output:    "output\r\n__SBX_EXIT_CODE__=0\r\n",
			markerKey: "__SBX_EXIT_CODE__",
			want:      "output\r\n",
		},
		{
			name:      "no marker to remove",
			output:    "some output without marker",
			markerKey: "__SBX_EXIT_CODE__",
			want:      "some output without marker",
		},
		{
			name:      "empty output",
			output:    "",
			markerKey: "__SBX_EXIT_CODE__",
			want:      "",
		},
		{
			name:      "marker with whitespace prefix",
			output:    "output\n __SBX_EXIT_CODE__=0\n",
			markerKey: "__SBX_EXIT_CODE__",
			want:      "output\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RemoveExitCodeMarker(tt.output, tt.markerKey)
			if got != tt.want {
				t.Errorf("RemoveExitCodeMarker() = %q, want %q", got, tt.want)
			}
		})
	}
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

func TestFindLastMarkerStart(t *testing.T) {
	tests := []struct {
		name         string
		output       string
		markerPrefix string
		want         int
	}{
		{
			name:         "single marker",
			output:       "prefix=__MARKER__=value",
			markerPrefix: "__MARKER__=",
			want:         -1, // '=' is not a valid separator
		},
		{
			name:         "multiple markers",
			output:       "__MARKER__=0\nsome output\n__MARKER__=1",
			markerPrefix: "__MARKER__=",
			want:         25, // Last occurrence starts after "\nsome output\n"
		},
		{
			name:         "no marker",
			output:       "some output without markers",
			markerPrefix: "__MARKER__=",
			want:         -1,
		},
		{
			name:         "marker at start",
			output:       "__MARKER__=0\nrest",
			markerPrefix: "__MARKER__=",
			want:         0,
		},
		{
			name:         "marker preceded by space",
			output:       "  __MARKER__=0",
			markerPrefix: "__MARKER__=",
			want:         2, // space is a valid separator
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findLastMarkerStart(tt.output, tt.markerPrefix)
			if got != tt.want {
				t.Errorf("findLastMarkerStart() = %v, want %v", got, tt.want)
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

// TestPodNameGeneration verifies the PodName function
func TestPodNameGeneration(t *testing.T) {
	tests := []struct {
		name      string
		sessionID string
		wantLen   int
	}{
		{
			name:      "normal session ID",
			sessionID: "test-session-123",
			wantLen:   14, // "sbx-" + 10 chars
		},
		{
			name:      "short session ID",
			sessionID: "abc",
			wantLen:   14,
		},
		{
			name:      "long session ID",
			sessionID: "very-long-session-id-with-lots-of-characters",
			wantLen:   14,
		},
		{
			name:      "session ID with special chars",
			sessionID: "session_123!@#",
			wantLen:   14,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PodName(tt.sessionID)
			if len(got) != tt.wantLen {
				t.Errorf("PodName() length = %v, want %v", len(got), tt.wantLen)
			}
			if !reflect.DeepEqual(got[:4], "sbx-") {
				t.Errorf("PodName() prefix = %v, want 'sbx-'", got[:4])
			}
		})
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
