package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestAPIError_Write(t *testing.T) {
	tests := []struct {
		name       string
		err        *APIError
		validateFn func(*testing.T, *httptest.ResponseRecorder)
	}{
		{
			name: "basic error response",
			err: NewAPIError(ErrPodNotFound, "Pod not found"),
			validateFn: func(t *testing.T, rr *httptest.ResponseRecorder) {
				if rr.Code != http.StatusNotFound {
					t.Errorf("Write() status = %v, want 404", rr.Code)
				}
				ct := rr.Header().Get("Content-Type")
				if ct != "application/json" {
					t.Errorf("Write() content-type = %v, want application/json", ct)
				}

				var resp ErrorResponse
				if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
					t.Fatalf("Failed to decode response: %v", err)
				}
				if resp.Error.Code != string(ErrPodNotFound) {
					t.Errorf("Write() code = %v, want %v", resp.Error.Code, ErrPodNotFound)
				}
				if resp.Error.Message != "Pod not found" {
					t.Errorf("Write() message = %v, want 'Pod not found'", resp.Error.Message)
				}
			},
		},
		{
			name: "error with request ID",
			err: NewAPIError(ErrBadRequest, "Bad request").WithRequestID("test-req-123"),
			validateFn: func(t *testing.T, rr *httptest.ResponseRecorder) {
				var resp ErrorResponse
				if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
					t.Fatalf("Failed to decode response: %v", err)
				}
				if resp.Error.RequestID != "test-req-123" {
					t.Errorf("Write() requestId = %v, want 'test-req-123'", resp.Error.RequestID)
				}
			},
		},
		{
			name: "error with details",
			err: NewAPIError(ErrInvalidEnvKey, "Invalid env key").WithDetail("key", "bad-key"),
			validateFn: func(t *testing.T, rr *httptest.ResponseRecorder) {
				var resp ErrorResponse
				if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
					t.Fatalf("Failed to decode response: %v", err)
				}
				if resp.Error.Details == nil {
					t.Error("Write() details is nil")
				} else if resp.Error.Details["key"] != "bad-key" {
					t.Errorf("Write() details[key] = %v, want 'bad-key'", resp.Error.Details["key"])
				}
			},
		},
		{
			name: "error with cause",
			err: NewAPIError(ErrPodCreateFailed, "Failed to create pod").WithCause(http.ErrHandlerTimeout),
			validateFn: func(t *testing.T, rr *httptest.ResponseRecorder) {
				// The error should be logged, but not in the response
				var resp ErrorResponse
				if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
					t.Fatalf("Failed to decode response: %v", err)
				}
				if resp.Error.Message != "Failed to create pod" {
					t.Errorf("Write() message = %v, want 'Failed to create pod'", resp.Error.Message)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			rr := httptest.NewRecorder()

			tt.err.Write(rr, req)

			tt.validateFn(t, rr)
		})
	}
}

func TestAPIError_Write_ExtractsRequestID(t *testing.T) {
	tests := []struct {
		name      string
		headerKey string
		headerVal string
	}{
		{"X-Request-Id", "X-Request-Id", "req-123"},
		{"X-Request-ID", "X-Request-ID", "req-456"},
		{"Request-Id", "Request-Id", "req-789"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			req.Header.Set(tt.headerKey, tt.headerVal)
			rr := httptest.NewRecorder()

			err := NewAPIError(ErrBadRequest, "Bad request")
			err.Write(rr, req)

			var resp ErrorResponse
			if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
				t.Fatalf("Failed to decode response: %v", err)
			}
			if resp.Error.RequestID != tt.headerVal {
				t.Errorf("Write() requestId = %v, want %v", resp.Error.RequestID, tt.headerVal)
			}
		})
	}
}

func TestWriteError(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	WriteError(rr, req, ErrPodNotFound, "Pod not found")

	if rr.Code != http.StatusNotFound {
		t.Errorf("WriteError() status = %v, want 404", rr.Code)
	}

	var resp ErrorResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if resp.Error.Code != string(ErrPodNotFound) {
		t.Errorf("WriteError() code = %v, want %v", resp.Error.Code, ErrPodNotFound)
	}
}

func TestWriteErrorWithCause(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	cause := http.ErrHandlerTimeout
	WriteErrorWithCause(rr, req, ErrPodCreateFailed, "Failed to create", cause)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("WriteErrorWithCause() status = %v, want 500", rr.Code)
	}

	var resp ErrorResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if resp.Error.Message != "Failed to create" {
		t.Errorf("WriteErrorWithCause() message = %v, want 'Failed to create'", resp.Error.Message)
	}
}

func TestDefaultRequestIDExtractor(t *testing.T) {
	tests := []struct {
		name     string
		headers  map[string]string
		expected string
	}{
		{
			name: "X-Request-Id header",
			headers: map[string]string{
				"X-Request-Id": "req-123",
			},
			expected: "req-123",
		},
		{
			name: "X-Request-ID header",
			headers: map[string]string{
				"X-Request-ID": "req-456",
			},
			expected: "req-456",
		},
		{
			name: "Request-Id header",
			headers: map[string]string{
				"Request-Id": "req-789",
			},
			expected: "req-789",
		},
		{
			name:     "no headers",
			headers:  map[string]string{},
			expected: "",
		},
		{
			name: "X-Request-Id takes precedence",
			headers: map[string]string{
				"X-Request-ID": "req-2",  // This is the canonical form, will overwrite
				"Request-Id":   "req-3",  // Lowercase form
			},
			expected: "req-2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			got := DefaultRequestIDExtractor(req)
			if got != tt.expected {
				t.Errorf("DefaultRequestIDExtractor() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestRequestIDCounter_Next(t *testing.T) {
	c := &RequestIDCounter{}

	// First call should return 1
	if got := c.Next(); got != 1 {
		t.Errorf("Next() = %v, want 1", got)
	}

	// Second call should return 2
	if got := c.Next(); got != 2 {
		t.Errorf("Next() = %v, want 2", got)
	}

	// Third call should return 3
	if got := c.Next(); got != 3 {
		t.Errorf("Next() = %v, want 3", got)
	}
}

func TestRequestIDCounter_Concurrent(t *testing.T) {
	c := &RequestIDCounter{}
	const goroutines = 100
	const callsPerGoroutine = 100

	var wg sync.WaitGroup
	results := make(chan uint64, goroutines*callsPerGoroutine)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < callsPerGoroutine; j++ {
				results <- c.Next()
			}
		}()
	}

	wg.Wait()
	close(results)

	// Verify all values are unique
	seen := make(map[uint64]bool)
	for val := range results {
		if seen[val] {
			t.Errorf("Next() returned duplicate value: %v", val)
		}
		seen[val] = true
	}

	// Verify we got the expected count
	if len(seen) != goroutines*callsPerGoroutine {
		t.Errorf("Next() returned %v unique values, want %v", len(seen), goroutines*callsPerGoroutine)
	}

	// Verify max value
	var max uint64
	for val := range seen {
		if val > max {
			max = val
		}
	}
	if max != uint64(goroutines*callsPerGoroutine) {
		t.Errorf("Next() max value = %v, want %v", max, goroutines*callsPerGoroutine)
	}
}

func TestAPIError_WithCause_ThreadSafe(t *testing.T) {
	err := NewAPIError(ErrBadRequest, "Bad request")

	const goroutines = 10
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			// Each goroutine creates a copy with its own cause
			copy := err.WithCause(http.ErrUseLastResponse)
			if copy == err {
				t.Errorf("WithCause() returned same instance for goroutine %v", idx)
			}
			_ = copy.Error() // Call Error() to ensure it works
		}(i)
	}

	wg.Wait()

	// Original error should be unchanged
	if err.Cause != nil {
		t.Error("WithCause() original error has cause set")
	}
}

func TestAPIError_WithRequestID_ThreadSafe(t *testing.T) {
	err := NewAPIError(ErrBadRequest, "Bad request")

	const goroutines = 10
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id := strings.Repeat("x", idx)
			copy := err.WithRequestID(id)
			if copy == err {
				t.Errorf("WithRequestID() returned same instance for goroutine %v", idx)
			}
			if copy.RequestID != id {
				t.Errorf("WithRequestID() id = %v, want %v", copy.RequestID, id)
			}
		}(i)
	}

	wg.Wait()

	// Original error should be unchanged
	if err.RequestID != "" {
		t.Error("WithRequestID() original error has requestID set")
	}
}

func TestAPIError_WithDetail_ThreadSafe(t *testing.T) {
	err := NewAPIError(ErrBadRequest, "Bad request")

	const goroutines = 10
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			key := "key"
			val := idx
			copy := err.WithDetail(key, val)
			if copy == err {
				t.Errorf("WithDetail() returned same instance for goroutine %v", idx)
			}
			// Original details should be preserved in copy
			if copy.Details == nil {
				t.Error("WithDetail() copy has nil details")
			}
		}(i)
	}

	wg.Wait()

	// Original error should be unchanged
	if err.Details != nil {
		t.Error("WithDetail() original error has details set")
	}
}
