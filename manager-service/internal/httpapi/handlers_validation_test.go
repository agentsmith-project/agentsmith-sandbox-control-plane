package httpapi

import (
	"strings"
	"testing"
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
