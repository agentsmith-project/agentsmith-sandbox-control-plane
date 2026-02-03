package httpapi

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
)

// ErrorCode represents an API error code
type ErrorCode string

// Error code constants
const (
	// Auth errors
	ErrServiceKeyMissing ErrorCode = "SERVICE_KEY_MISSING"
	ErrServiceKeyInvalid ErrorCode = "SERVICE_KEY_INVALID"

	// Config errors
	ErrConfigNotLoaded ErrorCode = "CONFIG_NOT_LOADED"
	ErrNotReady        ErrorCode = "NOT_READY"

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
	ErrK8sExecFailed   ErrorCode = "K8S_EXEC_FAILED"

	// Exec errors
	ErrExecTimeout             ErrorCode = "EXEC_TIMEOUT"
	ErrExecExitCodeUnavailable ErrorCode = "EXEC_EXITCODE_UNAVAILABLE"

	// Files errors
	ErrUploadValidationFailed ErrorCode = "UPLOAD_VALIDATION_FAILED"
	ErrUploadExecFailed       ErrorCode = "UPLOAD_EXEC_FAILED"
	ErrDownloadExecFailed     ErrorCode = "DOWNLOAD_EXEC_FAILED"
)

// HTTPStatusMapping maps error codes to HTTP status codes
var HTTPStatusMapping = map[ErrorCode]int{
	// Auth - 401
	ErrServiceKeyMissing: http.StatusUnauthorized,
	ErrServiceKeyInvalid: http.StatusUnauthorized,

	// Config/Ready - 503
	ErrConfigNotLoaded: http.StatusServiceUnavailable,
	ErrNotReady:        http.StatusServiceUnavailable,

	// Validation - 400/413/422
	ErrBadRequest:     http.StatusBadRequest,
	ErrInvalidEnvKey:  http.StatusUnprocessableEntity,
	ErrInvalidWorkdir: http.StatusUnprocessableEntity,
	ErrInvalidPath:    http.StatusUnprocessableEntity,
	ErrUploadTooLarge: 413, // http.StatusEntityTooLarge

	// Pod errors - 404/500/503/504
	ErrPodNotFound:     http.StatusNotFound,
	ErrPodNotReady:     http.StatusServiceUnavailable,
	ErrPodReadyTimeout: http.StatusGatewayTimeout,
	ErrPodCreateFailed: http.StatusInternalServerError,
	ErrPodGetFailed:    http.StatusInternalServerError,
	ErrPodPatchFailed:  http.StatusInternalServerError,
	ErrPodDeleteFailed: http.StatusInternalServerError,
	ErrK8sExecFailed:   http.StatusInternalServerError,

	// Exec errors - 500/504
	ErrExecTimeout:             http.StatusGatewayTimeout,
	ErrExecExitCodeUnavailable: http.StatusInternalServerError,

	// Files errors - 500
	ErrUploadValidationFailed: http.StatusUnprocessableEntity,
	ErrUploadExecFailed:       http.StatusInternalServerError,
	ErrDownloadExecFailed:     http.StatusInternalServerError,
}

// DefaultMessage returns the default message for an error code
func (e ErrorCode) DefaultMessage() string {
	switch e {
	case ErrServiceKeyMissing:
		return "Service key is required"
	case ErrServiceKeyInvalid:
		return "Service key is invalid"
	case ErrConfigNotLoaded:
		return "Configuration not loaded"
	case ErrNotReady:
		return "Service is not ready"
	case ErrBadRequest:
		return "Invalid request format"
	case ErrInvalidEnvKey:
		return "Invalid environment variable key"
	case ErrInvalidWorkdir:
		return "Invalid working directory"
	case ErrInvalidPath:
		return "Invalid file path"
	case ErrUploadTooLarge:
		return "Upload exceeds maximum size"
	case ErrPodCreateFailed:
		return "Failed to create sandbox pod"
	case ErrPodGetFailed:
		return "Failed to get sandbox pod"
	case ErrPodNotFound:
		return "Sandbox pod not found"
	case ErrPodNotReady:
		return "Sandbox pod is not ready"
	case ErrPodReadyTimeout:
		return "Sandbox pod did not become ready in time"
	case ErrPodPatchFailed:
		return "Failed to update sandbox pod"
	case ErrPodDeleteFailed:
		return "Failed to delete sandbox pod"
	case ErrK8sExecFailed:
		return "Failed to execute command in sandbox"
	case ErrExecTimeout:
		return "Command execution timed out"
	case ErrExecExitCodeUnavailable:
		return "Command exit code is unavailable"
	case ErrUploadExecFailed:
		return "Failed to upload files"
	case ErrDownloadExecFailed:
		return "Failed to download files"
	case ErrUploadValidationFailed:
		return "Upload archive validation failed"
	default:
		return "An error occurred"
	}
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
		Code      string                 `json:"code"`
		Message   string                 `json:"message"`
		RequestID string                 `json:"requestId,omitempty"`
		Details   map[string]interface{} `json:"details,omitempty"`
	} `json:"error"`
}

// APIError represents an error that can be returned to API clients
type APIError struct {
	Code      ErrorCode
	Message   string
	RequestID string
	Details   map[string]interface{}
	Cause     error
}

// Error implements the error interface
func (e *APIError) Error() string {
	msg := fmt.Sprintf("[%s] %s", e.Code, e.Message)
	if e.Cause != nil {
		msg += fmt.Sprintf(": %v", e.Cause)
	}
	return msg
}

// NewAPIError creates a new API error
func NewAPIError(code ErrorCode, message string) *APIError {
	if message == "" {
		message = code.DefaultMessage()
	}
	return &APIError{
		Code:    code,
		Message: message,
	}
}

// WithCause adds a cause to the error
func (e *APIError) WithCause(cause error) *APIError {
	e.Cause = cause
	return e
}

// WithRequestID adds a request ID to the error
func (e *APIError) WithRequestID(requestID string) *APIError {
	e.RequestID = requestID
	return e
}

// WithDetail adds a detail to the error
func (e *APIError) WithDetail(key string, value interface{}) *APIError {
	if e.Details == nil {
		e.Details = make(map[string]interface{})
	}
	e.Details[key] = value
	return e
}

// Write writes the error response to the HTTP response writer
func (e *APIError) Write(w http.ResponseWriter, r *http.Request) {
	status := e.Code.HTTPStatus()

	// Get request ID if not set
	if e.RequestID == "" {
		e.RequestID = GetRequestID(r)
	}

	// Build response
	var resp ErrorResponse
	resp.Error.Code = string(e.Code)
	resp.Error.Message = e.Message
	resp.Error.RequestID = e.RequestID
	resp.Error.Details = e.Details

	// Log the error
	log.Printf("API error: code=%s status=%d requestId=%s message=%s",
		e.Code, status, e.RequestID, e.Message)
	if e.Cause != nil {
		log.Printf("  cause: %v", e.Cause)
	}

	// Write response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp)
}

// WriteError writes an API error to the response
func WriteError(w http.ResponseWriter, r *http.Request, code ErrorCode, message string) {
	NewAPIError(code, message).Write(w, r)
}

// WriteErrorWithCause writes an API error with a cause
func WriteErrorWithCause(w http.ResponseWriter, r *http.Request, code ErrorCode, message string, cause error) {
	NewAPIError(code, message).WithCause(cause).Write(w, r)
}

// RequestIDExtractor extracts request ID from a request
type RequestIDExtractor func(*http.Request) string

// DefaultRequestIDExtractor extracts the request ID from various headers
func DefaultRequestIDExtractor(r *http.Request) string {
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

// GetRequestID gets the request ID from a request
var GetRequestID = DefaultRequestIDExtractor

// RequestIDCounter is a counter for generating request IDs
type RequestIDCounter struct {
	mu    sync.Mutex
	count uint64
}

// Next generates the next request ID
func (c *RequestIDCounter) Next() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.count++
	return c.count
}
