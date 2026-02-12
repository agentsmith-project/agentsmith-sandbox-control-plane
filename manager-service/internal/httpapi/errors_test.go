package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestErrorCode_HTTPStatus(t *testing.T) {
	tests := []struct {
		code     ErrorCode
		expected int
	}{
		{ErrBadRequest, http.StatusBadRequest},
		{ErrPodNotFound, http.StatusNotFound},
		{ErrPodNotReady, http.StatusServiceUnavailable},
		{ErrExecTimeout, http.StatusGatewayTimeout},
		{ErrExecFailed, http.StatusInternalServerError},
		{ErrUploadTooLarge, 413},
		{ErrorCode("UNKNOWN"), http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(string(tt.code), func(t *testing.T) {
			if got := tt.code.HTTPStatus(); got != tt.expected {
				t.Errorf("%s.HTTPStatus() = %d, want %d", tt.code, got, tt.expected)
			}
		})
	}
}

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/test", nil)
	r.Header.Set("X-Request-Id", "test-req-123")

	WriteError(w, r, ErrBadRequest, "invalid input")

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var resp ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Error.Code != "BAD_REQUEST" {
		t.Errorf("error code = %q, want %q", resp.Error.Code, "BAD_REQUEST")
	}
	if resp.Error.Message != "invalid input" {
		t.Errorf("message = %q, want %q", resp.Error.Message, "invalid input")
	}
	if resp.Error.RequestID != "test-req-123" {
		t.Errorf("requestId = %q, want %q", resp.Error.RequestID, "test-req-123")
	}
}

func TestGetRequestID(t *testing.T) {
	tests := []struct {
		name     string
		header   string
		value    string
		expected string
	}{
		{"X-Request-Id", "X-Request-Id", "abc", "abc"},
		{"X-Request-ID", "X-Request-ID", "def", "def"},
		{"Request-Id", "Request-Id", "ghi", "ghi"},
		{"none", "", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			if tt.header != "" {
				r.Header.Set(tt.header, tt.value)
			}
			if got := GetRequestID(r); got != tt.expected {
				t.Errorf("GetRequestID() = %q, want %q", got, tt.expected)
			}
		})
	}
}
