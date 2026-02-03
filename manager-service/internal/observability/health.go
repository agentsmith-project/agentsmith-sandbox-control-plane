package observability

import (
	"encoding/json"
	"net/http"
	"time"
)

// HealthChecker checks the health of the service
type HealthChecker struct {
	readyChecks []CheckFunc
}

// CheckFunc is a function that performs a health check
type CheckFunc func() error

// NewHealthChecker creates a new health checker
func NewHealthChecker() *HealthChecker {
	return &HealthChecker{
		readyChecks: make([]CheckFunc, 0),
	}
}

// AddReadyCheck adds a readiness check
func (h *HealthChecker) AddReadyCheck(check CheckFunc) {
	h.readyChecks = append(h.readyChecks, check)
}

// HandleHealthz handles the healthz endpoint
// The healthz endpoint always returns 200 if the process is alive
func (h *HealthChecker) HandleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	resp := HealthResponse{
		Status: "ok",
		Time:   time.Now().UTC().Format(time.RFC3339),
	}
	json.NewEncoder(w).Encode(resp)
}

// HandleReadyz handles the readyz endpoint
// The readyz endpoint returns 200 only if all readiness checks pass
func (h *HealthChecker) HandleReadyz(w http.ResponseWriter, r *http.Request) {
	// Run all readiness checks
	allReady := true
	failedChecks := []string{}

	for i, check := range h.readyChecks {
		if err := check(); err != nil {
			allReady = false
			failedChecks = append(failedChecks, getCheckName(check, i))
		}
	}

	if allReady {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		resp := ReadinessResponse{
			Ready:        true,
			ConfigLoaded: true,
			K8sConnected: true,
			Message:      "Service is ready",
		}
		json.NewEncoder(w).Encode(resp)
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)

		resp := ReadinessResponse{
			Ready:        false,
			ConfigLoaded: false,
			K8sConnected: false,
			Message:      "Service is not ready: " + joinStrings(failedChecks, ", "),
		}
		json.NewEncoder(w).Encode(resp)
	}
}

// HealthResponse represents a health check response
type HealthResponse struct {
	Status string `json:"status"`
	Time   string `json:"time"`
}

// ReadinessResponse represents a readiness check response
type ReadinessResponse struct {
	Ready        bool   `json:"ready"`
	ConfigLoaded bool   `json:"configLoaded"`
	K8sConnected bool   `json:"k8sConnected"`
	ConfigHash   string `json:"configHash,omitempty"`
	Message      string `json:"message,omitempty"`
}

// getCheckName gets a name for a check function
func getCheckName(check CheckFunc, index int) string {
	// This is a simple implementation - in production you might want
	// to use a more sophisticated approach
	return "check_" + string(rune('A'+index))
}

// joinStrings joins a slice of strings
func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for _, s := range strs[1:] {
		result += sep + s
	}
	return result
}
