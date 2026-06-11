package httperror

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/agentsmith-project/agentsmith-sandbox-control-plane/internal/observability"
)

type Envelope struct {
	Error Error `json:"error"`
}

type Error struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	RequestID string            `json:"request_id"`
	Details   map[string]string `json:"details,omitempty"`
}

func Write(w http.ResponseWriter, r *http.Request, status int, code string, message string) {
	WriteWithDetails(w, r, status, code, message, nil)
}

func WriteWithDetails(w http.ResponseWriter, r *http.Request, status int, code string, message string, details map[string]string) {
	if strings.TrimSpace(code) == "" {
		code = CodeForStatus(status)
	}
	if strings.TrimSpace(message) == "" {
		message = http.StatusText(status)
	}
	message = observability.RedactLogValue(message)
	requestID := GetRequestID(r)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Envelope{Error: Error{
		Code:      code,
		Message:   message,
		RequestID: requestID,
		Details:   sanitizeDetails(details),
	}})
}

func sanitizeDetails(details map[string]string) map[string]string {
	if len(details) == 0 {
		return nil
	}
	out := make(map[string]string, len(details))
	for key, value := range details {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		out[key] = observability.RedactLogValue(value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// GetRequestID returns the request ID selected for the request by observability middleware.
func GetRequestID(r *http.Request) string {
	if r == nil {
		return ""
	}
	if requestID, ok := observability.RequestIDFromContext(r.Context()); ok {
		return requestID
	}
	return observability.GetRequestID(r)
}

func CodeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusMethodNotAllowed:
		return "method_not_allowed"
	case http.StatusConflict:
		return "conflict"
	case http.StatusUnprocessableEntity:
		return "invalid_lifecycle_state"
	case http.StatusTooManyRequests:
		return "rate_limited"
	case http.StatusBadGateway:
		return "dependency_failure"
	case http.StatusServiceUnavailable:
		return "not_ready"
	default:
		return "internal_error"
	}
}
