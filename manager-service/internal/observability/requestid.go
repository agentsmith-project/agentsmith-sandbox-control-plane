package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// RequestIDHeader is the default header name for request ID
const RequestIDHeader = "X-Request-Id"

type requestIDContextKey struct{}

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

// WithRequestID stores the request ID selected for this request in context.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDContextKey{}, requestID)
}

// RequestIDFromContext returns the request ID selected by RequestIDMiddleware.
func RequestIDFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	requestID, ok := ctx.Value(requestIDContextKey{}).(string)
	requestID = strings.TrimSpace(requestID)
	return requestID, ok && requestID != ""
}

// GetRequestID extracts or generates a request ID
func GetRequestID(r *http.Request) string {
	if r == nil {
		return GenerateRequestID()
	}
	if id, ok := RequestIDFromContext(r.Context()); ok {
		return id
	}

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
			requestID := r.Header.Get(headerName)
			if requestID == "" {
				requestID = GetRequestID(r)
			}
			r.Header.Set(headerName, requestID)
			w.Header().Set(headerName, requestID)
			r = r.WithContext(WithRequestID(r.Context(), requestID))
			next.ServeHTTP(w, r)
		})
	}
}

// RequestCorrelationID returns the service correlation ID for outbound dependency calls.
// It follows the request ID chosen by middleware first so custom server.requestIdHeader
// values stay aligned with error envelopes, then falls back to accepted legacy headers.
func RequestCorrelationID(r *http.Request, generatedPrefix string) string {
	if r != nil {
		if id, ok := RequestIDFromContext(r.Context()); ok {
			return id
		}
		for _, header := range []string{"X-Correlation-Id", RequestIDHeader, "X-Request-ID", "Request-Id"} {
			if value := strings.TrimSpace(r.Header.Get(header)); value != "" {
				return value
			}
		}
	}
	prefix := cleanCorrelationPrefix(generatedPrefix)
	if prefix == "" {
		prefix = "asbcp"
	}
	return prefix + "-" + time.Now().UTC().Format("20060102T150405.000000000Z")
}

func cleanCorrelationPrefix(prefix string) string {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_':
			return r
		default:
			return '-'
		}
	}, prefix)
}
