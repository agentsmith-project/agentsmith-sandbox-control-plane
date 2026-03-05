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
// e.g., /v1/workspaces/ws-1/projects/p-1/workloads/wl-1/exec -> /v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}/exec
func PatternizePath(path string) string {
	if !strings.HasPrefix(path, "/v1/") {
		return path
	}

	parts := strings.Split(path, "/")
	if len(parts) < 4 {
		return path
	}

	// Patternize /v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}[/action]
	if parts[1] == "v1" && parts[2] == "workspaces" && len(parts) >= 8 {
		// parts: ["", "v1", "workspaces", wsId, "projects", projId, "workloads", wlId, ...]
		result := "/v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}"
		if len(parts) > 8 {
			result += "/" + strings.Join(parts[8:], "/")
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
	workloadCreateTotal int64
	workloadKeepaliveTotal int64
	workloadExecTotal   int64
	workloadDeleteTotal int64

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

// RecordWorkloadCreate records a workload creation
func (m *MetricsRegistry) RecordWorkloadCreate() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.workloadCreateTotal++
}

// RecordWorkloadKeepalive records a workload keepalive (client heartbeat)
func (m *MetricsRegistry) RecordWorkloadKeepalive() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.workloadKeepaliveTotal++
}

// RecordWorkloadExec records a workload exec
func (m *MetricsRegistry) RecordWorkloadExec() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.workloadExecTotal++
}

// RecordWorkloadDelete records a workload deletion
func (m *MetricsRegistry) RecordWorkloadDelete() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.workloadDeleteTotal++
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
		io.WriteString(w, "\n# HELP workload_create_total Total number of workloads created\n")
		io.WriteString(w, "# TYPE workload_create_total counter\n")
		io.WriteString(w, "workload_create_total ")
		io.WriteString(w, strconv.FormatInt(m.workloadCreateTotal, 10))
		io.WriteString(w, "\n")

		io.WriteString(w, "\n# HELP workload_keepalive_total Total number of workload keepalives\n")
		io.WriteString(w, "# TYPE workload_keepalive_total counter\n")
		io.WriteString(w, "workload_keepalive_total ")
		io.WriteString(w, strconv.FormatInt(m.workloadKeepaliveTotal, 10))
		io.WriteString(w, "\n")

		io.WriteString(w, "\n# HELP workload_exec_total Total number of workload execs\n")
		io.WriteString(w, "# TYPE workload_exec_total counter\n")
		io.WriteString(w, "workload_exec_total ")
		io.WriteString(w, strconv.FormatInt(m.workloadExecTotal, 10))
		io.WriteString(w, "\n")

		io.WriteString(w, "\n# HELP workload_delete_total Total number of workload deletions\n")
		io.WriteString(w, "# TYPE workload_delete_total counter\n")
		io.WriteString(w, "workload_delete_total ")
		io.WriteString(w, strconv.FormatInt(m.workloadDeleteTotal, 10))
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
