package auth

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
)

// ErrorCode represents an authentication error code
type ErrorCode string

const (
	ErrServiceKeyMissing ErrorCode = "SERVICE_KEY_MISSING"
	ErrServiceKeyInvalid ErrorCode = "SERVICE_KEY_INVALID"
	ErrTokenMissing      ErrorCode = "TOKEN_MISSING"
	ErrTokenInvalid      ErrorCode = "TOKEN_INVALID"
	ErrTokenExpired      ErrorCode = "TOKEN_EXPIRED"

	bearerPrefix = "Bearer "
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
func ServiceKeyMiddleware(validator *ServiceKeyValidator, headerName string, acceptAuthorization bool, authScheme string, failStatusCode int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// If no keys are configured, allow all requests (for development/testing)
			if !validator.HasKeys() {
				log.Printf("Auth: no service keys configured, allowing request")
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

	json.NewEncoder(w).Encode(resp)
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
func OptionalAuthMiddleware(validator *ServiceKeyValidator, headerName string, acceptAuthorization bool, authScheme string, failStatusCode int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// If no keys configured, skip auth
			if !validator.HasKeys() {
				next.ServeHTTP(w, r)
				return
			}

			// Use standard auth middleware
			middleware := ServiceKeyMiddleware(validator, headerName, acceptAuthorization, authScheme, failStatusCode)
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

// TokenAuthMiddleware validates JWT tokens from Authorization header
func TokenAuthMiddleware(authenticator *TokenAuthenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				requestID := getRequestID(r)
				log.Printf("Auth: missing Authorization header (requestId: %s, path: %s)", requestID, r.URL.Path)
				writeAuthError(w, ErrTokenMissing, "Authorization header required", requestID, http.StatusUnauthorized)
				return
			}

			// Expected format: "Bearer <token>"
			if !strings.HasPrefix(authHeader, bearerPrefix) || len(authHeader) == len(bearerPrefix) {
				requestID := getRequestID(r)
				log.Printf("Auth: invalid authorization header format (requestId: %s, path: %s)", requestID, r.URL.Path)
				writeAuthError(w, ErrTokenInvalid, "Invalid authorization header format", requestID, http.StatusUnauthorized)
				return
			}

			token := authHeader[len(bearerPrefix):]
			userCtx, err := authenticator.ValidateToken(token)
			if err != nil {
				requestID := getRequestID(r)
				log.Printf("Auth: invalid or expired token (requestId: %s, path: %s): %v", requestID, r.URL.Path, err)
				writeAuthError(w, ErrTokenInvalid, "Invalid or expired token", requestID, http.StatusUnauthorized)
				return
			}

			if userCtx.IsExpired() {
				requestID := getRequestID(r)
				log.Printf("Auth: token expired (requestId: %s, path: %s)", requestID, r.URL.Path)
				writeAuthError(w, ErrTokenExpired, "Token expired", requestID, http.StatusUnauthorized)
				return
			}

			// Add user context to request context
			ctx := context.WithValue(r.Context(), UserContextKey, userCtx)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// Context key for user context
type contextKey string

const UserContextKey contextKey = "user_context"

// GetUserContext retrieves user context from request context
func GetUserContext(r *http.Request) (*UserContext, bool) {
	userCtx, ok := r.Context().Value(UserContextKey).(*UserContext)
	return userCtx, ok
}
