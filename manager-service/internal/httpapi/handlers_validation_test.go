package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/sandbox/manager/internal/auth"
	"github.com/sandbox/manager/internal/config"
	"github.com/sandbox/manager/internal/k8s"
	"github.com/sandbox/manager/internal/observability"
)

// TestExtractSessionId tests the extractSessionId helper function
func TestExtractSessionId(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{
			name:     "valid path with session ID",
			path:     "/v1/sandboxes/test-session-123",
			expected: "test-session-123",
		},
		{
			name:     "valid path with nested endpoint",
			path:     "/v1/sandboxes/test-session-456/exec",
			expected: "test-session-456",
		},
		{
			name:     "valid path with deeply nested endpoint",
			path:     "/v1/sandboxes/test-session-789/files/upload",
			expected: "test-session-789",
		},
		{
			name:     "invalid path - missing parts",
			path:     "/v1/sandboxes",
			expected: "",
		},
		{
			name:     "invalid path - wrong version",
			path:     "/v2/sandboxes/test-session",
			expected: "",
		},
		{
			name:     "invalid path - wrong resource",
			path:     "/v1/widgets/test-session",
			expected: "",
		},
		{
			name:     "empty path",
			path:     "",
			expected: "",
		},
		{
			name:     "root path",
			path:     "/",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractSessionId(tt.path)
			if result != tt.expected {
				t.Errorf("extractSessionId(%q) = %q, want %q", tt.path, result, tt.expected)
			}
		})
	}
}

// TestErrorCode_DefaultMessage tests that all error codes have default messages
func TestErrorCode_DefaultMessage(t *testing.T) {
	// Test a few key error codes to ensure they have meaningful messages
	tests := []struct {
		code          ErrorCode
		expectNonEmpty bool
	}{
		{ErrBadRequest, true},
		{ErrInvalidEnvKey, true},
		{ErrPodCreateFailed, true},
		{ErrPodNotFound, true},
		{ErrExecTimeout, true},
		{ErrUploadTooLarge, true},
		{ErrDownloadExecFailed, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.code), func(t *testing.T) {
			msg := tt.code.DefaultMessage()
			if tt.expectNonEmpty && msg == "" {
				t.Errorf("ErrorCode %s should have a non-empty default message", tt.code)
			}
			if tt.expectNonEmpty && strings.Contains(msg, "error occurred") {
				// This is the fallback message, should not happen for known errors
				t.Errorf("ErrorCode %s has fallback default message: %s", tt.code, msg)
			}
		})
	}
}

// TestErrorCode_HTTPStatus tests that all error codes have proper HTTP status mappings
func TestErrorCode_HTTPStatus(t *testing.T) {
	// Test that error codes map to appropriate status codes
	tests := []struct {
		code           ErrorCode
		expectedStatus int
	}{
		{ErrBadRequest, 400},
		{ErrServiceKeyMissing, 401},
		{ErrPodNotFound, 404},
		{ErrUploadTooLarge, 413},
		{ErrInvalidWorkdir, 422},
		{ErrPodCreateFailed, 500},
		{ErrExecTimeout, 504},
		{ErrNotReady, 503},
	}

	for _, tt := range tests {
		t.Run(string(tt.code), func(t *testing.T) {
			status := tt.code.HTTPStatus()
			if status != tt.expectedStatus {
				t.Errorf("ErrorCode %s maps to status %d, want %d", tt.code, status, tt.expectedStatus)
			}
			// Verify status is in valid range
			if status < 100 || status > 599 {
				t.Errorf("ErrorCode %s has invalid HTTP status %d", tt.code, status)
			}
		})
	}
}

// TestNewAPIError tests the APIError constructor
func TestNewAPIError(t *testing.T) {
	tests := []struct {
		name    string
		code    ErrorCode
		message string
	}{
		{
			name:    "with custom message",
			code:    ErrBadRequest,
			message: "custom error message",
		},
		{
			name:    "with empty message uses default",
			code:    ErrPodNotFound,
			message: "",
		},
		{
			name:    "with known error code",
			code:    ErrExecTimeout,
			message: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewAPIError(tt.code, tt.message)
			if err == nil {
				t.Fatal("NewAPIError() returned nil")
			}
			if err.Code != tt.code {
				t.Errorf("NewAPIError().Code = %v, want %v", err.Code, tt.code)
			}
			expectedMsg := tt.message
			if expectedMsg == "" {
				expectedMsg = tt.code.DefaultMessage()
			}
			if err.Message != expectedMsg {
				t.Errorf("NewAPIError().Message = %v, want %v", err.Message, expectedMsg)
			}
		})
	}
}

// TestAPIError_WithCause tests the WithCause method
func TestAPIError_WithCause(t *testing.T) {
	baseErr := NewAPIError(ErrPodCreateFailed, "failed to create pod")
	cause := &TestError{Message: "underlying error"}

	errWithCause := baseErr.WithCause(cause)

	if errWithCause.Cause != cause {
		t.Errorf("WithCause() did not set cause, got %v", errWithCause.Cause)
	}

	// Test Error() output includes cause
	errorMsg := errWithCause.Error()
	if !strings.Contains(errorMsg, "underlying error") {
		t.Errorf("Error() output should contain cause message, got: %s", errorMsg)
	}
}

// TestAPIError_WithRequestID tests the WithRequestID method
func TestAPIError_WithRequestID(t *testing.T) {
	baseErr := NewAPIError(ErrPodCreateFailed, "failed to create pod")
	requestID := "req-12345"

	errWithID := baseErr.WithRequestID(requestID)

	if errWithID.RequestID != requestID {
		t.Errorf("WithRequestID() did not set request ID, got %v", errWithID.RequestID)
	}
}

// TestAPIError_WithDetail tests the WithDetail method
func TestAPIError_WithDetail(t *testing.T) {
	baseErr := NewAPIError(ErrPodCreateFailed, "failed to create pod")

	err := baseErr.WithDetail("podName", "test-pod-123").
		WithDetail("namespace", "default")

	if err.Details == nil {
		t.Fatal("WithDetail() did not initialize Details map")
	}

	if err.Details["podName"] != "test-pod-123" {
		t.Errorf("WithDetail() podName = %v, want test-pod-123", err.Details["podName"])
	}

	if err.Details["namespace"] != "default" {
		t.Errorf("WithDetail() namespace = %v, want default", err.Details["namespace"])
	}
}

// TestError is a simple error type for testing
type TestError struct {
	Message string
}

func (e *TestError) Error() string {
	return e.Message
}

// TestIsContextCanceled tests the isContextCanceled helper function
func TestIsContextCanceled(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "context.Canceled",
			err:      context.Canceled,
			expected: true,
		},
		{
			name:     "context.DeadlineExceeded",
			err:      context.DeadlineExceeded,
			expected: true,
		},
		{
			name:     "wrapped context.Canceled - contains message so returns true",
			err:      fmt.Errorf("wrapped: %w", context.Canceled),
			expected: true, // The message contains "context canceled" which is detected
		},
		{
			name:     "error with context canceled message",
			err:      fmt.Errorf("operation failed: context canceled"),
			expected: true,
		},
		{
			name:     "error with operation was canceled message",
			err:      fmt.Errorf("operation was canceled"),
			expected: true,
		},
		{
			name:     "error with deadline exceeded message",
			err:      fmt.Errorf("deadline exceeded"),
			expected: true,
		},
		{
			name:     "generic error",
			err:      fmt.Errorf("some other error"),
			expected: false,
		},
		{
			name:     "error with canceled in middle - contains checks work",
			err:      fmt.Errorf("context canceled during operation"),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isContextCanceled(tt.err)
			if result != tt.expected {
				t.Errorf("isContextCanceled(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

// TestContains tests the contains helper function
func TestContains(t *testing.T) {
	tests := []struct {
		name     string
		s        string
		substr   string
		expected bool
	}{
		{
			name:     "exact match",
			s:        "hello world",
			substr:   "hello world",
			expected: true,
		},
		{
			name:     "prefix match",
			s:        "hello world",
			substr:   "hello",
			expected: true,
		},
		{
			name:     "suffix match",
			s:        "hello world",
			substr:   "world",
			expected: true,
		},
		{
			name:     "middle match",
			s:        "hello world",
			substr:   "lo wo",
			expected: true,
		},
		{
			name:     "no match",
			s:        "hello world",
			substr:   "goodbye",
			expected: false,
		},
		{
			name:     "substring longer than string",
			s:        "hi",
			substr:   "hello world",
			expected: false,
		},
		{
			name:     "empty substring",
			s:        "hello world",
			substr:   "",
			expected: true,
		},
		{
			name:     "empty string with non-empty substring",
			s:        "",
			substr:   "test",
			expected: false,
		},
		{
			name:     "both empty",
			s:        "",
			substr:   "",
			expected: true,
		},
		{
			name:     "case sensitive",
			s:        "Hello World",
			substr:   "hello",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := contains(tt.s, tt.substr)
			if result != tt.expected {
				t.Errorf("contains(%q, %q) = %v, want %v", tt.s, tt.substr, result, tt.expected)
			}
		})
	}
}

// TestContainsMiddle tests the containsMiddle helper function
func TestContainsMiddle(t *testing.T) {
	tests := []struct {
		name     string
		s        string
		substr   string
		expected bool
	}{
		{
			name:     "match in middle",
			s:        "hello world",
			substr:   "lo wo",
			expected: true,
		},
		{
			name:     "match at start",
			s:        "hello world",
			substr:   "hello",
			expected: true,
		},
		{
			name:     "match at end",
			s:        "hello world",
			substr:   "world",
			expected: true,
		},
		{
			name:     "no match",
			s:        "hello world",
			substr:   "goodbye",
			expected: false,
		},
		{
			name:     "single character match",
			s:        "abc",
			substr:   "b",
			expected: true,
		},
		{
			name:     "empty substring",
			s:        "hello",
			substr:   "",
			expected: true,
		},
		{
			name:     "substring longer than string",
			s:        "hi",
			substr:   "hello",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := containsMiddle(tt.s, tt.substr)
			if result != tt.expected {
				t.Errorf("containsMiddle(%q, %q) = %v, want %v", tt.s, tt.substr, result, tt.expected)
			}
		})
	}
}

// TestLogRequest tests the LogRequest helper function
func TestLogRequest(t *testing.T) {
	// This test just ensures the function doesn't panic
	// In a real scenario, you'd capture log output
	req := &http.Request{
		Method: "GET",
		URL:    &url.URL{Path: "/v1/sandboxes/test"},
	}
	LogRequest(req, "test-request-id")
}

// TestNewHandlers tests the NewHandlers constructor
func TestNewHandlers(t *testing.T) {
	mockMgr := &MockManager{}

	h := NewHandlers(mockMgr)

	if h == nil {
		t.Fatal("NewHandlers() returned nil")
	}

	if h.mgr != mockMgr {
		t.Error("NewHandlers() did not set manager")
	}
}

// MockManager is a mock implementation of the Manager interface for testing
type MockManager struct {
	cfg         *config.Config
	k8sClient   *k8s.Client
	k8sExecutor *k8s.Executor
	metrics     *observability.MetricsRegistry
}

func (m *MockManager) GetConfig() *config.Config {
	if m.cfg == nil {
		return &config.Config{}
	}
	return m.cfg
}

func (m *MockManager) GetK8sClient() *k8s.Client {
	return m.k8sClient
}

func (m *MockManager) GetK8sExecutor() *k8s.Executor {
	return m.k8sExecutor
}

func (m *MockManager) GetMetrics() *observability.MetricsRegistry {
	if m.metrics == nil {
		return observability.NewMetricsRegistry()
	}
	return m.metrics
}

func (m *MockManager) GetAuthorizer() *auth.Authorizer {
	return nil
}
