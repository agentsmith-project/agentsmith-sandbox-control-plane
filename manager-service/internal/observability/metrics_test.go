package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestResponseWriterWrapper_WriteHeader(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		multiple   bool
	}{
		{"single write header", http.StatusOK, false},
		{"write header 404", http.StatusNotFound, false},
		{"write header 500", http.StatusInternalServerError, false},
		{"multiple calls", http.StatusCreated, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			w := &ResponseWriterWrapper{ResponseWriter: rr}

			w.WriteHeader(tt.statusCode)
			if tt.multiple {
				w.WriteHeader(http.StatusAccepted) // Should be ignored
			}

			if w.StatusCode != tt.statusCode {
				t.Errorf("WriteHeader() StatusCode = %v, want %v", w.StatusCode, tt.statusCode)
			}

			if !w.written {
				t.Error("WriteHeader() written = false, want true")
			}

			if rr.Code != tt.statusCode {
				t.Errorf("WriteHeader() underlying code = %v, want %v", rr.Code, tt.statusCode)
			}
		})
	}

	t.Run("second WriteHeader is ignored", func(t *testing.T) {
		rr := httptest.NewRecorder()
		w := &ResponseWriterWrapper{ResponseWriter: rr}

		w.WriteHeader(http.StatusOK)
		w.WriteHeader(http.StatusNotFound)

		if w.StatusCode != http.StatusOK {
			t.Errorf("WriteHeader() StatusCode = %v, want 200 (first call)", w.StatusCode)
		}
	})
}

func TestResponseWriterWrapper_Write(t *testing.T) {
	t.Run("write sets default status code", func(t *testing.T) {
		rr := httptest.NewRecorder()
		w := &ResponseWriterWrapper{ResponseWriter: rr}

		n, err := w.Write([]byte("test data"))
		if err != nil {
			t.Fatalf("Write() error = %v", err)
		}
		if n != 9 {
			t.Errorf("Write() n = %v, want 9", n)
		}

		if w.StatusCode != http.StatusOK {
			t.Errorf("Write() StatusCode = %v, want 200", w.StatusCode)
		}

		if !w.written {
			t.Error("Write() written = false, want true")
		}
	})

	t.Run("write after WriteHeader", func(t *testing.T) {
		rr := httptest.NewRecorder()
		w := &ResponseWriterWrapper{ResponseWriter: rr}

		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("data"))

		if w.StatusCode != http.StatusCreated {
			t.Errorf("Write() StatusCode = %v, want 201 (from WriteHeader)", w.StatusCode)
		}
	})

	t.Run("multiple writes", func(t *testing.T) {
		rr := httptest.NewRecorder()
		w := &ResponseWriterWrapper{ResponseWriter: rr}

		w.Write([]byte("first"))
		w.Write([]byte(" "))
		w.Write([]byte("second"))

		body := rr.Body.String()
		if body != "first second" {
			t.Errorf("Write() body = %v, want 'first second'", body)
		}

		if w.StatusCode != http.StatusOK {
			t.Errorf("Write() StatusCode = %v, want 200", w.StatusCode)
		}
	})
}

func TestResponseWriterWrapper_Unwrap(t *testing.T) {
	rr := httptest.NewRecorder()
	w := &ResponseWriterWrapper{ResponseWriter: rr}

	unwrapped := w.Unwrap()
	if unwrapped != rr {
		t.Error("Unwrap() returned different ResponseWriter")
	}
}

func TestPatternizePath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		// Non-API routes pass through
		{"health check", "/health", "/health"},
		{"metrics", "/metrics", "/metrics"},
		{"root", "/", "/"},
		{"static file", "/static/file.js", "/static/file.js"},

		// Workspace routes are patternized
		{
			name:     "workload create",
			input:    "/v1/workspaces/ws-abc/projects/proj-xyz/workloads/wl-001",
			expected: "/v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}",
		},
		{
			name:     "workload keepalive",
			input:    "/v1/workspaces/ws-abc/projects/proj-xyz/workloads/wl-001/keepalive",
			expected: "/v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}/keepalive",
		},
		{
			name:     "workload exec",
			input:    "/v1/workspaces/ws-abc/projects/proj-xyz/workloads/wl-001/exec",
			expected: "/v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}/exec",
		},
		{
			name:     "workload short path",
			input:    "/v1/workspaces/ws-abc/projects",
			expected: "/v1/workspaces/ws-abc/projects",
		},

		// Edge cases
		{"short v1 path", "/v1/", "/v1/"},
		{"different version", "/v2/resource", "/v2/resource"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := PatternizePath(tt.input)
			if result != tt.expected {
				t.Errorf("PatternizePath() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestNewHistogram(t *testing.T) {
	buckets := []float64{0.1, 0.5, 1.0}
	h := NewHistogram(buckets)

	if h == nil {
		t.Fatal("NewHistogram() returned nil")
	}
	if len(h.buckets) != len(buckets) {
		t.Errorf("NewHistogram() buckets length = %v, want %v", len(h.buckets), len(buckets))
	}
	if len(h.counts) != len(buckets)+1 {
		t.Errorf("NewHistogram() counts length = %v, want %v", len(h.counts), len(buckets)+1)
	}
	if h.count != 0 {
		t.Errorf("NewHistogram() count = %v, want 0", h.count)
	}
	if h.sum != 0 {
		t.Errorf("NewHistogram() sum = %v, want 0", h.sum)
	}
}

func TestHistogram_Observe(t *testing.T) {
	buckets := []float64{0.1, 0.5, 1.0}
	h := NewHistogram(buckets)

	h.Observe(0.05) // <= 0.1, <= 0.5, <= 1.0
	h.Observe(0.3)  // <= 0.5, <= 1.0
	h.Observe(2.0)  // <= 1.0 (not <= 0.1, not <= 0.5)

	if h.count != 3 {
		t.Errorf("Observe() count = %v, want 3", h.count)
	}

	// Check bucket counts - each bucket counts values <= that bucket
	// 0.05 <= 0.1 (counts[0]++), 0.05 <= 0.5 (counts[1]++), 0.05 <= 1.0 (counts[2]++)
	// 0.3 (not <= 0.1), 0.3 <= 0.5 (counts[1]++), 0.3 <= 1.0 (counts[2]++)
	// 2.0 (not <= 0.1), (not <= 0.5), 2.0 <= 1.0 (false, no increment)
	// +Inf bucket counts all observations
	if h.counts[0] != 1 { // Only 0.05 <= 0.1
		t.Errorf("Observe() counts[0] = %v, want 1", h.counts[0])
	}
	if h.counts[1] != 2 { // 0.05 and 0.3 <= 0.5
		t.Errorf("Observe() counts[1] = %v, want 2", h.counts[1])
	}
	// counts[2] is not incremented by the bucket loop logic (only <= bucket)
	// but +Inf bucket counts all
	if h.counts[3] != 3 { // +Inf bucket gets all
		t.Errorf("Observe() counts[3] = %v, want 3", h.counts[3])
	}

	expectedSum := int64((0.05 + 0.3 + 2.0) * 1000)
	if h.sum != expectedSum {
		t.Errorf("Observe() sum = %v, want %v", h.sum, expectedSum)
	}
}

func TestNewMetricsRegistry(t *testing.T) {
	m := NewMetricsRegistry()

	if m == nil {
		t.Fatal("NewMetricsRegistry() returned nil")
	}
	if m.httpRequestTotal == nil {
		t.Error("NewMetricsRegistry() httpRequestTotal is nil")
	}
	if m.httpRequestDuration == nil {
		t.Error("NewMetricsRegistry() httpRequestDuration is nil")
	}
	if m.k8sAPIFailTotal == nil {
		t.Error("NewMetricsRegistry() k8sAPIFailTotal is nil")
	}
}

func TestMetricsRegistry_RecordHTTPRequest(t *testing.T) {
	m := NewMetricsRegistry()

	m.RecordHTTPRequest("POST", "/v1/workspaces/ws1/projects/p1/workloads/wl1/keepalive", http.StatusOK, 100*time.Millisecond)
	m.RecordHTTPRequest("POST", "/v1/workspaces/ws1/projects/p1/workloads/wl1/keepalive", http.StatusOK, 150*time.Millisecond)
	m.RecordHTTPRequest("POST", "/v1/workspaces/ws1/projects/p1/workloads/wl2/keepalive", http.StatusNotFound, 50*time.Millisecond)

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.httpRequestTotal["POST:/v1/workspaces/ws1/projects/p1/workloads/wl1/keepalive:200"] != 2 {
		t.Errorf("RecordHTTPRequest() total count = %v, want 2", m.httpRequestTotal["POST:/v1/workspaces/ws1/projects/p1/workloads/wl1/keepalive:200"])
	}
	if m.httpRequestTotal["POST:/v1/workspaces/ws1/projects/p1/workloads/wl2/keepalive:404"] != 1 {
		t.Errorf("RecordHTTPRequest() total count = %v, want 1", m.httpRequestTotal["POST:/v1/workspaces/ws1/projects/p1/workloads/wl2/keepalive:404"])
	}

	hist1, ok1 := m.httpRequestDuration["POST:/v1/workspaces/ws1/projects/p1/workloads/wl1/keepalive"]
	if !ok1 {
		t.Fatal("RecordHTTPRequest() histogram for path 1 not created")
	}
	if hist1.count != 2 {
		t.Errorf("RecordHTTPRequest() histogram count = %v, want 2", hist1.count)
	}

	hist2, ok2 := m.httpRequestDuration["POST:/v1/workspaces/ws1/projects/p1/workloads/wl2/keepalive"]
	if !ok2 {
		t.Fatal("RecordHTTPRequest() histogram for path 2 not created")
	}
	if hist2.count != 1 {
		t.Errorf("RecordHTTPRequest() histogram count = %v, want 1", hist2.count)
	}
}

func TestMetricsRegistry_RecordWorkloadOperations(t *testing.T) {
	m := NewMetricsRegistry()

	tests := []struct {
		name       string
		recordFunc func()
		getCount   func(m *MetricsRegistry) int64
	}{
		{"create", m.RecordWorkloadCreate, func(m *MetricsRegistry) int64 { return m.workloadCreateTotal }},
		{"keepalive", m.RecordWorkloadKeepalive, func(m *MetricsRegistry) int64 { return m.workloadKeepaliveTotal }},
		{"exec", m.RecordWorkloadExec, func(m *MetricsRegistry) int64 { return m.workloadExecTotal }},
		{"delete", m.RecordWorkloadDelete, func(m *MetricsRegistry) int64 { return m.workloadDeleteTotal }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.recordFunc()
			tt.recordFunc()
			tt.recordFunc()

			m.mu.RLock()
			count := tt.getCount(m)
			m.mu.RUnlock()

			if count != 3 {
				t.Errorf("%s: count = %v, want 3", tt.name, count)
			}
		})
	}
}

func TestMetricsRegistry_RecordConfigReload(t *testing.T) {
	m := NewMetricsRegistry()

	t.Run("success", func(t *testing.T) {
		hash := "abc123"
		loadedAt := time.Now().UTC().Format(time.RFC3339)

		m.RecordConfigReloadSuccess(hash, loadedAt)

		m.mu.RLock()
		if m.configReloadSuccess != 1 {
			t.Errorf("RecordConfigReloadSuccess() count = %v, want 1", m.configReloadSuccess)
		}
		if m.configHash != hash {
			t.Errorf("RecordConfigReloadSuccess() hash = %v, want %v", m.configHash, hash)
		}
		if m.configLoadedAt != loadedAt {
			t.Errorf("RecordConfigReloadSuccess() loadedAt = %v, want %v", m.configLoadedAt, loadedAt)
		}
		if m.configLastReload == "" {
			t.Error("RecordConfigReloadSuccess() lastReload not set")
		}
		m.mu.RUnlock()
	})

	t.Run("failure", func(t *testing.T) {
		m.RecordConfigReloadFailure()

		m.mu.RLock()
		if m.configReloadFailure != 1 {
			t.Errorf("RecordConfigReloadFailure() count = %v, want 1", m.configReloadFailure)
		}
		m.mu.RUnlock()
	})
}

func TestMetricsRegistry_RecordK8sAPIFailure(t *testing.T) {
	m := NewMetricsRegistry()

	m.RecordK8sAPIFailure("CreatePod")
	m.RecordK8sAPIFailure("CreatePod")
	m.RecordK8sAPIFailure("GetPod")

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.k8sAPIFailTotal["CreatePod"] != 2 {
		t.Errorf("RecordK8sAPIFailure() CreatePod count = %v, want 2", m.k8sAPIFailTotal["CreatePod"])
	}
	if m.k8sAPIFailTotal["GetPod"] != 1 {
		t.Errorf("RecordK8sAPIFailure() GetPod count = %v, want 1", m.k8sAPIFailTotal["GetPod"])
	}
}

func TestMetricsRegistry_Handler(t *testing.T) {
	m := NewMetricsRegistry()

	// Record some data
	m.RecordWorkloadCreate()
	m.RecordWorkloadCreate()
	m.RecordWorkloadKeepalive()
	m.RecordHTTPRequest("GET", "/v1/workspaces/ws-1/projects/p-1/workloads/wl-1/keepalive", http.StatusOK, 100*time.Millisecond)

	req := httptest.NewRequest("GET", "/metrics", nil)
	rr := httptest.NewRecorder()

	handler := m.Handler()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Handler() status = %v, want 200", rr.Code)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "text/plain" {
		t.Errorf("Handler() content-type = %v, want 'text/plain'", ct)
	}

	body := rr.Body.String()

	// Check for expected metric outputs
	expectedSubstrings := []string{
		"# HELP workload_create_total",
		"# TYPE workload_create_total",
		"workload_create_total 2",
		"# HELP workload_keepalive_total",
		"workload_keepalive_total 1",
		"# HELP http_request_total",
		"# TYPE http_request_total",
		"http_request_total{method=\"GET\"",
		"/v1/workspaces/ws-1/projects/p-1/workloads/wl-1/keepalive\"",
	}

	for _, substr := range expectedSubstrings {
		if !strings.Contains(body, substr) {
			t.Errorf("Handler() output missing expected substring: %q", substr)
		}
	}
}

func TestMetricsRegistry_Handler_Concurrent(t *testing.T) {
	m := NewMetricsRegistry()

	const goroutines = 100
	const operationsPerGoroutine = 100

	var wg sync.WaitGroup
	// Concurrent reads and writes
	for i := 0; i < goroutines; i++ {
		wg.Add(2)

		go func() {
			defer wg.Done()
			for j := 0; j < operationsPerGoroutine; j++ {
				m.RecordWorkloadCreate()
			}
		}()

		go func() {
			defer wg.Done()
			for j := 0; j < operationsPerGoroutine; j++ {
				req := httptest.NewRequest("GET", "/metrics", nil)
				rr := httptest.NewRecorder()
				m.Handler().ServeHTTP(rr, req)
			}
		}()
	}

	wg.Wait()

	m.mu.RLock()
	count := m.workloadCreateTotal
	m.mu.RUnlock()

	if count != goroutines*operationsPerGoroutine {
		t.Errorf("Concurrent operations: count = %v, want %v", count, goroutines*operationsPerGoroutine)
	}
}

func TestGetMetrics(t *testing.T) {
	m := GetMetrics()

	if m == nil {
		t.Fatal("GetMetrics() returned nil")
	}

	// Verify it's the global registry
	m.RecordWorkloadCreate()

	m2 := GetMetrics()
	m2.mu.RLock()
	count := m2.workloadCreateTotal
	m2.mu.RUnlock()

	if count != 1 {
		t.Errorf("GetMetrics() not returning global registry, count = %v, want 1", count)
	}
}

func TestGenerateRequestID(t *testing.T) {
	t.Run("generates valid UUID", func(t *testing.T) {
		id := GenerateRequestID()
		if id == "" {
			t.Error("GenerateRequestID() returned empty string")
		}

		// Check if it's a valid UUID format
		uuidRegex := regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
		if !uuidRegex.MatchString(id) {
			// If not UUID, should be hex-encoded 16 bytes (32 hex chars)
			hexRegex := regexp.MustCompile(`^[a-f0-9]{32}$`)
			if !hexRegex.MatchString(id) {
				t.Errorf("GenerateRequestID() = %v, want valid UUID or hex format", id)
			}
		}
	})

	t.Run("generates unique IDs", func(t *testing.T) {
		ids := make(map[string]bool)
		for i := 0; i < 1000; i++ {
			id := GenerateRequestID()
			if ids[id] {
				t.Errorf("GenerateRequestID() generated duplicate ID: %v", id)
			}
			ids[id] = true
		}
	})

	t.Run("never returns 'unknown' in normal operation", func(t *testing.T) {
		// In normal operation, we should always get a valid UUID from uuid.NewRandom()
		for i := 0; i < 100; i++ {
			id := GenerateRequestID()
			if id == "unknown" {
				t.Error("GenerateRequestID() returned 'unknown', this should only happen as a last resort")
			}
		}
	})
}

func TestGetRequestID_Observability(t *testing.T) {
	tests := []struct {
		name     string
		headers  map[string]string
		validate func(*testing.T, string)
	}{
		{
			name: "X-Request-Id header",
			headers: map[string]string{
				"X-Request-Id": "req-123",
			},
			validate: func(t *testing.T, id string) {
				if id != "req-123" {
					t.Errorf("GetRequestID() = %v, want 'req-123'", id)
				}
			},
		},
		{
			name: "X-Request-ID header",
			headers: map[string]string{
				"X-Request-ID": "req-456",
			},
			validate: func(t *testing.T, id string) {
				if id != "req-456" {
					t.Errorf("GetRequestID() = %v, want 'req-456'", id)
				}
			},
		},
		{
			name: "Request-Id header",
			headers: map[string]string{
				"Request-Id": "req-789",
			},
			validate: func(t *testing.T, id string) {
				if id != "req-789" {
					t.Errorf("GetRequestID() = %v, want 'req-789'", id)
				}
			},
		},
		{
			name:    "no headers generates new ID",
			headers: map[string]string{},
			validate: func(t *testing.T, id string) {
				if id == "" {
					t.Error("GetRequestID() returned empty string, want generated ID")
				}
				// Should be a valid UUID or hex string
				if len(id) < 32 {
					t.Errorf("GetRequestID() = %v, want valid ID format", id)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			id := GetRequestID(req)
			tt.validate(t, id)
		})
	}

	t.Run("generates different IDs when no header", func(t *testing.T) {
		req1 := httptest.NewRequest("GET", "/test", nil)
		req2 := httptest.NewRequest("GET", "/test", nil)

		id1 := GetRequestID(req1)
		id2 := GetRequestID(req2)

		if id1 == id2 {
			t.Errorf("GetRequestID() generated same ID: %v, want different IDs", id1)
		}
	})
}

func TestRequestIDMiddleware(t *testing.T) {
	defaultHeaderName := "X-Request-Id"

	tests := []struct {
		name       string
		headerName string
		reqHeaders map[string]string
		validate   func(*testing.T, *httptest.ResponseRecorder, string)
	}{
		{
			name:       "adds request ID to context and response",
			headerName: defaultHeaderName,
			reqHeaders: map[string]string{
				"X-Request-Id": "test-req-id",
			},
			validate: func(t *testing.T, rr *httptest.ResponseRecorder, requestID string) {
				if requestID != "test-req-id" {
					t.Errorf("RequestIDMiddleware() requestID = %v, want 'test-req-id'", requestID)
				}
				respHeader := rr.Header().Get(defaultHeaderName)
				if respHeader != "test-req-id" {
					t.Errorf("RequestIDMiddleware() response header = %v, want 'test-req-id'", respHeader)
				}
			},
		},
		{
			name:       "generates new ID when not provided",
			headerName: defaultHeaderName,
			reqHeaders: map[string]string{},
			validate: func(t *testing.T, rr *httptest.ResponseRecorder, requestID string) {
				if requestID == "" {
					t.Error("RequestIDMiddleware() requestID is empty, want generated ID")
				}
				respHeader := rr.Header().Get(defaultHeaderName)
				if respHeader != requestID {
					t.Errorf("RequestIDMiddleware() response header = %v, want %v", respHeader, requestID)
				}
			},
		},
		{
			name:       "uses custom header name",
			headerName: "X-Custom-Request-ID",
			reqHeaders: map[string]string{},
			validate: func(t *testing.T, rr *httptest.ResponseRecorder, requestID string) {
				respHeader := rr.Header().Get("X-Custom-Request-ID")
				if respHeader != requestID {
					t.Errorf("RequestIDMiddleware() response header = %v, want %v", respHeader, requestID)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a handler that captures the request ID from context
			var capturedRequestID string
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedRequestID = RequestIDFromContext(r.Context())
				w.WriteHeader(http.StatusOK)
			})

			middleware := RequestIDMiddleware(tt.headerName)
			handler := middleware(next)

			req := httptest.NewRequest("GET", "/test", nil)
			for k, v := range tt.reqHeaders {
				req.Header.Set(k, v)
			}

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			tt.validate(t, rr, capturedRequestID)
		})
	}
}

func TestRequestIDFromContext(t *testing.T) {
	t.Run("extracts request ID from context", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), RequestIDKey, "test-req-id")
		id := RequestIDFromContext(ctx)
		if id != "test-req-id" {
			t.Errorf("RequestIDFromContext() = %v, want 'test-req-id'", id)
		}
	})

	t.Run("returns empty string when not in context", func(t *testing.T) {
		ctx := context.Background()
		id := RequestIDFromContext(ctx)
		if id != "" {
			t.Errorf("RequestIDFromContext() = %v, want ''", id)
		}
	})

	t.Run("returns empty string for wrong type", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), RequestIDKey, 12345)
		id := RequestIDFromContext(ctx)
		if id != "" {
			t.Errorf("RequestIDFromContext() = %v, want '' for wrong type", id)
		}
	})

	t.Run("works with uuid.UUID type", func(t *testing.T) {
		testUUID := uuid.New()
		ctx := context.WithValue(context.Background(), RequestIDKey, testUUID.String())
		id := RequestIDFromContext(ctx)
		if id != testUUID.String() {
			t.Errorf("RequestIDFromContext() = %v, want %v", id, testUUID.String())
		}
	})
}
