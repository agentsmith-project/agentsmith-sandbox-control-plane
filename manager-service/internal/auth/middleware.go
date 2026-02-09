package auth

import (
	"encoding/json"
	"log"
	"net/http"
)

// ErrorCode represents an authentication error code
type ErrorCode string

const (
	ErrServiceKeyMissing  ErrorCode = "SERVICE_KEY_MISSING"
	ErrServiceKeyInvalid  ErrorCode = "SERVICE_KEY_INVALID"
	ErrServiceNotConfigured ErrorCode = "SERVICE_NOT_CONFIGURED"
)

// ErrorResponse represents a standard error response
type ErrorResponse struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"requestId,omitempty"`
	} `json:"error"`
}

// ServiceKeyMiddleware creates an HTTP middleware for service key authentication
func ServiceKeyMiddleware(validator *ServiceKeyValidator, headerName string, acceptAuthorization bool, authScheme string, failStatusCode int, allowUnauthenticated bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Fail-closed: If no keys are configured, check allowUnauthenticated flag
			if !validator.HasKeys() {
				if !allowUnauthenticated {
					// Not configured and dev mode is OFF - return 500
					requestID := getRequestID(r)
					log.Printf("Auth: service not configured (requestId: %s, path: %s)", requestID, r.URL.Path)
					writeAuthError(w, ErrServiceNotConfigured, "Service not configured: no service keys configured", requestID, http.StatusInternalServerError)
					return
				}
				// Dev mode is ON - log and allow request
				log.Printf("Auth: dev mode enabled - allowing unauthenticated request (path: %s)", r.URL.Path)
				next.ServeHTTP(w, r)
				return
			}

			// Try the custom header first
			key := r.Header.Get(headerName)

			// If not found and Authorization header is accepted, try that
			if key == "" && acceptAuthorization {
				authHeader := r.Header.Get("Authorization")
				if authHeader != "" {
					extractedKey, err := ExtractServiceKey(authHeader, authScheme)
					if err == nil {
						key = extractedKey
					}
				}
			}

			// Validate the key
			if !validator.Validate(key) {
				// Extract request ID for logging/response
				requestID := getRequestID(r)

				if key == "" {
					log.Printf("Auth: missing service key (requestId: %s, path: %s)", requestID, r.URL.Path)
					writeAuthError(w, ErrServiceKeyMissing, "Service key is required", requestID, failStatusCode)
				} else {
					log.Printf("Auth: invalid service key (requestId: %s, path: %s)", requestID, r.URL.Path)
					writeAuthError(w, ErrServiceKeyInvalid, "Service key is invalid", requestID, failStatusCode)
				}
				return
			}

			// Key is valid, proceed to next handler
			next.ServeHTTP(w, r)
		})
	}
}

// writeAuthError writes an authentication error response
func writeAuthError(w http.ResponseWriter, code ErrorCode, message string, requestID string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	var resp ErrorResponse
	resp.Error.Code = string(code)
	resp.Error.Message = message
	resp.Error.RequestID = requestID

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("Failed to encode auth error response: %v", err)
	}
}

// getRequestID extracts the request ID from the request
func getRequestID(r *http.Request) string {
	// Try common headers
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

// OptionalAuthMiddleware creates an optional authentication middleware
// It only enforces auth if keys are configured
func OptionalAuthMiddleware(validator *ServiceKeyValidator, headerName string, acceptAuthorization bool, authScheme string, failStatusCode int, allowUnauthenticated bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// If no keys configured, skip auth
			if !validator.HasKeys() {
				next.ServeHTTP(w, r)
				return
			}

			// Use standard auth middleware
			middleware := ServiceKeyMiddleware(validator, headerName, acceptAuthorization, authScheme, failStatusCode, allowUnauthenticated)
			middleware(next).ServeHTTP(w, r)
		})
	}
}

// CombineMiddleware combines multiple middleware into one
func CombineMiddleware(middlewares ...func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		for i := len(middlewares) - 1; i >= 0; i-- {
			next = middlewares[i](next)
		}
		return next
	}
}
