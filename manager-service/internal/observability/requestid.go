package observability

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/google/uuid"
)

// RequestIDHeader is the default header name for request ID
const RequestIDHeader = "X-Request-Id"

// GenerateRequestID generates a new request ID
func GenerateRequestID() string {
	// Try UUID first
	if id, err := uuid.NewRandom(); err == nil {
		return id.String()
	}

	// Fallback to random bytes
	b := make([]byte, 16)
	if _, err := rand.Read(b); err == nil {
		return hex.EncodeToString(b)
	}

	// Last resort
	return "unknown"
}

// GetRequestID extracts or generates a request ID
func GetRequestID(r *http.Request) string {
	// Try to get from header
	if id := r.Header.Get(RequestIDHeader); id != "" {
		return id
	}
	if id := r.Header.Get("X-Request-ID"); id != "" {
		return id
	}
	if id := r.Header.Get("Request-Id"); id != "" {
		return id
	}

	// Generate new ID
	return GenerateRequestID()
}

// RequestIDMiddleware is a middleware that sets the request ID response header.
func RequestIDMiddleware(headerName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := GetRequestID(r)
			w.Header().Set(headerName, requestID)
			next.ServeHTTP(w, r)
		})
	}
}
