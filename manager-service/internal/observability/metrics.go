package observability

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ResponseWriterWrapper wraps http.ResponseWriter to capture status code
type ResponseWriterWrapper struct {
	http.ResponseWriter
	StatusCode int
	written    bool
}

// WriteHeader captures the status code
func (w *ResponseWriterWrapper) WriteHeader(statusCode int) {
	if !w.written {
		w.StatusCode = statusCode
		w.written = true
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

// Write captures writes and ensures we have a status code
func (w *ResponseWriterWrapper) Write(data []byte) (int, error) {
	if !w.written {
		w.StatusCode = http.StatusOK
		w.written = true
	}
	return w.ResponseWriter.Write(data)
}

// Unwrap returns the underlying ResponseWriter
func (w *ResponseWriterWrapper) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// PatternizePath converts specific paths to patterns to avoid high cardinality
// e.g., /v1/sandboxes/abc123touch -> /v1/sandboxes/{sessionId}/touch
func PatternizePath(path string) string {
	// Fast path for non-API routes
	if !strings.HasPrefix(path, "/v1/") {
		return path
	}

	// Parse the path
	parts := strings.Split(path, "/")
	if len(parts) < 4 {
		return path
	}

	// Patternize /v1/sandboxes/{sessionId}/* routes
	if parts[1] == "v1" && parts[2] == "sandboxes" && len(parts) >= 4 {
		// Replace the session ID (parts[3]) with {sessionId}
		result := "/v1/sandboxes/{sessionId}"
		if len(parts) > 4 {
			result += "/" + strings.Join(parts[4:], "/")
		}
		return result
	}

	return path
}

// MetricsRegistry tracks application metrics
type MetricsRegistry struct {
	mu sync.RWMutex

	// HTTP metrics
	httpRequestTotal    map[string]int64      // method:path:status -> count
	httpRequestDuration map[string]*Histogram // method:path -> histogram

	// Business metrics
	sandboxCreateTotal   int64
	sandboxTouchTotal    int64
	sandboxExecTotal     int64
	sandboxUploadTotal   int64
	sandboxDownloadTotal int64
	sandboxDeleteTotal   int64

	// Config metrics
	configReloadSuccess int64
	configReloadFailure int64
	configHash          string
	configLoadedAt      string
	configLastReload    string

	// K8s metrics
	k8sAPIFailTotal map[string]int64 // operation -> count
}

// Histogram tracks value distributions
type Histogram struct {
	buckets []float64
	counts  []int64
	sum     int64
	count   int64
}

// NewHistogram creates a new histogram with default buckets
func NewHistogram(buckets []float64) *Histogram {
	return &Histogram{
		buckets: buckets,
		counts:  make([]int64, len(buckets)+1),
	}
}

// Observe records a value
func (h *Histogram) Observe(value float64) {
	h.count++
	h.sum += int64(value * 1000) // Store as milliseconds

	for i, bucket := range h.buckets {
		if value <= bucket {
			h.counts[i]++
		}
	}
	// +Inf bucket
	h.counts[len(h.counts)-1]++
}

// Default metric buckets
var defaultBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// NewMetricsRegistry creates a new metrics registry
func NewMetricsRegistry() *MetricsRegistry {
	return &MetricsRegistry{
		httpRequestTotal:    make(map[string]int64),
		httpRequestDuration: make(map[string]*Histogram),
		k8sAPIFailTotal:     make(map[string]int64),
	}
}

// RecordHTTPRequest records an HTTP request
func (m *MetricsRegistry) RecordHTTPRequest(method, path string, statusCode int, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := method + ":" + path + ":" + strconv.Itoa(statusCode)
	m.httpRequestTotal[key]++

	// Track duration histogram
	durationKey := method + ":" + path
	if _, ok := m.httpRequestDuration[durationKey]; !ok {
		m.httpRequestDuration[durationKey] = NewHistogram(defaultBuckets)
	}
	m.httpRequestDuration[durationKey].Observe(duration.Seconds())
}

// RecordSandboxCreate records a sandbox creation
func (m *MetricsRegistry) RecordSandboxCreate() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sandboxCreateTotal++
}

// RecordSandboxTouch records a sandbox touch
func (m *MetricsRegistry) RecordSandboxTouch() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sandboxTouchTotal++
}

// RecordSandboxExec records a sandbox exec
func (m *MetricsRegistry) RecordSandboxExec() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sandboxExecTotal++
}

// RecordSandboxUpload records a file upload
func (m *MetricsRegistry) RecordSandboxUpload() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sandboxUploadTotal++
}

// RecordSandboxDownload records a file download
func (m *MetricsRegistry) RecordSandboxDownload() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sandboxDownloadTotal++
}

// RecordSandboxDelete records a sandbox deletion
func (m *MetricsRegistry) RecordSandboxDelete() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sandboxDeleteTotal++
}

// RecordConfigReloadSuccess records a successful config reload
func (m *MetricsRegistry) RecordConfigReloadSuccess(hash string, loadedAt string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.configReloadSuccess++
	m.configHash = hash
	m.configLoadedAt = loadedAt
	m.configLastReload = time.Now().UTC().Format(time.RFC3339)
}

// RecordConfigReloadFailure records a failed config reload
func (m *MetricsRegistry) RecordConfigReloadFailure() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.configReloadFailure++
}

// RecordK8sAPIFailure records a K8s API failure
func (m *MetricsRegistry) RecordK8sAPIFailure(operation string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.k8sAPIFailTotal[operation]++
}

// Handler returns an HTTP handler for Prometheus metrics
func (m *MetricsRegistry) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")

		m.mu.RLock()
		defer m.mu.RUnlock()

		// HTTP request totals
		io.WriteString(w, "# HELP http_request_total Total number of HTTP requests\n")
		io.WriteString(w, "# TYPE http_request_total counter\n")
		for key, count := range m.httpRequestTotal {
			parts := strings.Split(key, ":")
			if len(parts) == 3 {
				io.WriteString(w, "http_request_total{method=\"")
				io.WriteString(w, parts[0])
				io.WriteString(w, "\",path=\"")
				io.WriteString(w, parts[1])
				io.WriteString(w, "\",status=\"")
				io.WriteString(w, parts[2])
				io.WriteString(w, "\"} ")
				io.WriteString(w, strconv.FormatInt(count, 10))
				io.WriteString(w, "\n")
			}
		}

		// HTTP request duration
		io.WriteString(w, "\n# HELP http_request_duration_seconds HTTP request duration in seconds\n")
		io.WriteString(w, "# TYPE http_request_duration_seconds histogram\n")
		for key, hist := range m.httpRequestDuration {
			parts := strings.Split(key, ":")
			if len(parts) == 2 {
				// Bucket counts
				accum := int64(0)
				for i, bucket := range hist.buckets {
					accum += hist.counts[i]
					io.WriteString(w, "http_request_duration_seconds_bucket{method=\"")
					io.WriteString(w, parts[0])
					io.WriteString(w, "\",path=\"")
					io.WriteString(w, parts[1])
					io.WriteString(w, "\",le=\"")
					io.WriteString(w, strconv.FormatFloat(bucket, 'f', -1, 64))
					io.WriteString(w, "\"} ")
					io.WriteString(w, strconv.FormatInt(accum, 10))
					io.WriteString(w, "\n")
				}
				// +Inf bucket
				accum += hist.counts[len(hist.counts)-1]
				io.WriteString(w, "http_request_duration_seconds_bucket{method=\"")
				io.WriteString(w, parts[0])
				io.WriteString(w, "\",path=\"")
				io.WriteString(w, parts[1])
				io.WriteString(w, "\",le=\"+Inf\"} ")
				io.WriteString(w, strconv.FormatInt(accum, 10))
				io.WriteString(w, "\n")

				// Sum and count
				io.WriteString(w, "http_request_duration_seconds_sum{method=\"")
				io.WriteString(w, parts[0])
				io.WriteString(w, "\",path=\"")
				io.WriteString(w, parts[1])
				io.WriteString(w, "\"} ")
				io.WriteString(w, strconv.FormatInt(hist.sum/1000, 10)) // Convert back to seconds
				io.WriteString(w, "\n")

				io.WriteString(w, "http_request_duration_seconds_count{method=\"")
				io.WriteString(w, parts[0])
				io.WriteString(w, "\",path=\"")
				io.WriteString(w, parts[1])
				io.WriteString(w, "\"} ")
				io.WriteString(w, strconv.FormatInt(hist.count, 10))
				io.WriteString(w, "\n")
			}
		}

		// Business metrics
		io.WriteString(w, "\n# HELP sandbox_create_total Total number of sandboxes created\n")
		io.WriteString(w, "# TYPE sandbox_create_total counter\n")
		io.WriteString(w, "sandbox_create_total ")
		io.WriteString(w, strconv.FormatInt(m.sandboxCreateTotal, 10))
		io.WriteString(w, "\n")

		io.WriteString(w, "\n# HELP sandbox_touch_total Total number of sandbox touches\n")
		io.WriteString(w, "# TYPE sandbox_touch_total counter\n")
		io.WriteString(w, "sandbox_touch_total ")
		io.WriteString(w, strconv.FormatInt(m.sandboxTouchTotal, 10))
		io.WriteString(w, "\n")

		io.WriteString(w, "\n# HELP sandbox_exec_total Total number of sandbox execs\n")
		io.WriteString(w, "# TYPE sandbox_exec_total counter\n")
		io.WriteString(w, "sandbox_exec_total ")
		io.WriteString(w, strconv.FormatInt(m.sandboxExecTotal, 10))
		io.WriteString(w, "\n")

		io.WriteString(w, "\n# HELP sandbox_upload_total Total number of file uploads\n")
		io.WriteString(w, "# TYPE sandbox_upload_total counter\n")
		io.WriteString(w, "sandbox_upload_total ")
		io.WriteString(w, strconv.FormatInt(m.sandboxUploadTotal, 10))
		io.WriteString(w, "\n")

		io.WriteString(w, "\n# HELP sandbox_download_total Total number of file downloads\n")
		io.WriteString(w, "# TYPE sandbox_download_total counter\n")
		io.WriteString(w, "sandbox_download_total ")
		io.WriteString(w, strconv.FormatInt(m.sandboxDownloadTotal, 10))
		io.WriteString(w, "\n")

		io.WriteString(w, "\n# HELP sandbox_delete_total Total number of sandbox deletions\n")
		io.WriteString(w, "# TYPE sandbox_delete_total counter\n")
		io.WriteString(w, "sandbox_delete_total ")
		io.WriteString(w, strconv.FormatInt(m.sandboxDeleteTotal, 10))
		io.WriteString(w, "\n")

		// Config metrics
		io.WriteString(w, "\n# HELP config_reload_success_total Total number of successful config reloads\n")
		io.WriteString(w, "# TYPE config_reload_success_total counter\n")
		io.WriteString(w, "config_reload_success_total ")
		io.WriteString(w, strconv.FormatInt(m.configReloadSuccess, 10))
		io.WriteString(w, "\n")

		io.WriteString(w, "\n# HELP config_reload_failure_total Total number of failed config reloads\n")
		io.WriteString(w, "# TYPE config_reload_failure_total counter\n")
		io.WriteString(w, "config_reload_failure_total ")
		io.WriteString(w, strconv.FormatInt(m.configReloadFailure, 10))
		io.WriteString(w, "\n")

		io.WriteString(w, "\n# HELP config_hash_info Info about current config hash\n")
		io.WriteString(w, "# TYPE config_hash_info gauge\n")
		io.WriteString(w, "config_hash_info{hash=\"")
		io.WriteString(w, m.configHash)
		io.WriteString(w, "\"} 1\n")

		io.WriteString(w, "\n# HELP config_loaded_at_timestamp Timestamp when config was loaded\n")
		io.WriteString(w, "# TYPE config_loaded_at_timestamp gauge\n")
		if m.configLoadedAt != "" {
			if t, err := time.Parse(time.RFC3339, m.configLoadedAt); err == nil {
				io.WriteString(w, "config_loaded_at_timestamp ")
				io.WriteString(w, strconv.FormatInt(t.Unix(), 10))
				io.WriteString(w, "\n")
			}
		}

		// K8s metrics
		io.WriteString(w, "\n# HELP k8s_api_fail_total Total number of K8s API failures\n")
		io.WriteString(w, "# TYPE k8s_api_fail_total counter\n")
		for op, count := range m.k8sAPIFailTotal {
			io.WriteString(w, "k8s_api_fail_total{operation=\"")
			io.WriteString(w, op)
			io.WriteString(w, "\"} ")
			io.WriteString(w, strconv.FormatInt(count, 10))
			io.WriteString(w, "\n")
		}
	}
}

// Global metrics registry
var globalMetrics = NewMetricsRegistry()

// GetMetrics returns the global metrics registry
func GetMetrics() *MetricsRegistry {
	return globalMetrics
}
