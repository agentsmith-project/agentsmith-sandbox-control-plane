package httpapi

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
)

// ErrorCode represents an API error code
type ErrorCode string

// Error code constants
const (
	// Request validation errors
	ErrBadRequest     ErrorCode = "BAD_REQUEST"
	ErrInvalidEnvKey  ErrorCode = "INVALID_ENV_KEY"
	ErrInvalidWorkdir ErrorCode = "INVALID_WORKDIR"
	ErrInvalidPath    ErrorCode = "INVALID_PATH"
	ErrUploadTooLarge ErrorCode = "UPLOAD_TOO_LARGE"

	// Kubernetes/sandbox errors
	ErrPodCreateFailed ErrorCode = "POD_CREATE_FAILED"
	ErrPodGetFailed    ErrorCode = "POD_GET_FAILED"
	ErrPodNotFound     ErrorCode = "POD_NOT_FOUND"
	ErrPodNotReady     ErrorCode = "POD_NOT_READY"
	ErrPodReadyTimeout ErrorCode = "POD_READY_TIMEOUT"
	ErrPodPatchFailed  ErrorCode = "POD_PATCH_FAILED"
	ErrPodDeleteFailed ErrorCode = "POD_DELETE_FAILED"

	// Exec errors
	ErrExecTimeout ErrorCode = "EXEC_TIMEOUT"
	ErrExecFailed  ErrorCode = "EXEC_FAILED"

	// Files errors
	ErrUploadValidationFailed ErrorCode = "UPLOAD_VALIDATION_FAILED"
	ErrUploadExecFailed       ErrorCode = "UPLOAD_EXEC_FAILED"
	ErrDownloadExecFailed     ErrorCode = "DOWNLOAD_EXEC_FAILED"
)

// HTTPStatusMapping maps error codes to HTTP status codes
var HTTPStatusMapping = map[ErrorCode]int{
	ErrBadRequest:     http.StatusBadRequest,
	ErrInvalidEnvKey:  http.StatusUnprocessableEntity,
	ErrInvalidWorkdir: http.StatusUnprocessableEntity,
	ErrInvalidPath:    http.StatusUnprocessableEntity,
	ErrUploadTooLarge: 413,

	ErrPodNotFound:     http.StatusNotFound,
	ErrPodNotReady:     http.StatusServiceUnavailable,
	ErrPodReadyTimeout: http.StatusGatewayTimeout,
	ErrPodCreateFailed: http.StatusInternalServerError,
	ErrPodGetFailed:    http.StatusInternalServerError,
	ErrPodPatchFailed:  http.StatusInternalServerError,
	ErrPodDeleteFailed: http.StatusInternalServerError,

	ErrExecTimeout: http.StatusGatewayTimeout,
	ErrExecFailed:  http.StatusInternalServerError,

	ErrUploadValidationFailed: http.StatusUnprocessableEntity,
	ErrUploadExecFailed:       http.StatusInternalServerError,
	ErrDownloadExecFailed:     http.StatusInternalServerError,
}

// HTTPStatus returns the HTTP status code for an error code
func (e ErrorCode) HTTPStatus() int {
	if status, ok := HTTPStatusMapping[e]; ok {
		return status
	}
	return http.StatusInternalServerError
}

// ErrorResponse represents a standard API error response
type ErrorResponse struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"requestId,omitempty"`
	} `json:"error"`
}

// WriteError writes an API error to the response
func WriteError(w http.ResponseWriter, r *http.Request, code ErrorCode, message string) {
	status := code.HTTPStatus()
	requestID := GetRequestID(r)

	var resp ErrorResponse
	resp.Error.Code = string(code)
	resp.Error.Message = message
	resp.Error.RequestID = requestID

	log.Printf("API error: code=%s status=%d requestId=%s message=%s",
		code, status, requestID, message)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("[ERROR] Failed to encode JSON error response: %v", err)
	}
}

// WriteErrorWithCause writes an API error with a cause
func WriteErrorWithCause(w http.ResponseWriter, r *http.Request, code ErrorCode, message string, cause error) {
	log.Printf("API error cause: %v", cause)
	WriteError(w, r, code, fmt.Sprintf("%s: %v", message, cause))
}

// GetRequestID extracts the request ID from the request
func GetRequestID(r *http.Request) string {
	if id := r.Header.Get("X-Request-Id"); id != "" {
		return id
	}
	if id := r.Header.Get("X-Request-ID"); id != "" {
		return id
	}
	if id := r.Header.Get("Request-Id"); id != "" {
		return id
	}
	return ""
}
