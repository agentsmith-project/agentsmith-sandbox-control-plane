package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/google/uuid"
)

// ContextKey is the type for context keys
type ContextKey string

// RequestIDKey is the context key for request ID
const RequestIDKey ContextKey = "requestId"

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

// RequestIDMiddleware is a middleware that adds request ID to context and response header
func RequestIDMiddleware(headerName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Get or generate request ID
			requestID := GetRequestID(r)

			// Add to context
			ctx := context.WithValue(r.Context(), RequestIDKey, requestID)

			// Add to response header
			w.Header().Set(headerName, requestID)

			// Call next handler
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequestIDFromContext extracts the request ID from context
func RequestIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(RequestIDKey).(string); ok {
		return id
	}
	return ""
}
