package auth

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/sandbox/manager/internal/audit"
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
func ServiceKeyMiddleware(validator *ServiceKeyValidator, headerName string, acceptAuthorization bool, authScheme string, failStatusCode int, loggers ...*audit.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Create audit event for authentication attempt if logger is provided
			var authEvent *audit.Event
			var auditLogger *audit.Logger
			if len(loggers) > 0 {
				auditLogger = loggers[0]
				getUserContext := func(r *http.Request) (string, string) {
					if userCtx, ok := GetUserContext(r); ok {
						return userCtx.UserID, userCtx.SessionID
					}
					return "", ""
				}
				authEvent = audit.NewEventFromRequest(r, audit.EventAuthAttempt, getUserContext)
				authEvent.Details = map[string]interface{}{
					"headerName":       headerName,
					"acceptAuthorization": acceptAuthorization,
					"authScheme":      authScheme,
				}
			}

			// If no keys are configured, allow all requests (for development/testing)
			if !validator.HasKeys() {
				log.Printf("Auth: no service keys configured, allowing request")
				if authEvent != nil {
					authEvent.Success = true
					authEvent.Details["reason"] = "no_keys_configured"
					auditLogger.LogEvent(authEvent)
				}
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

				authEvent.Success = false
				if key == "" {
					if authEvent != nil {
						authEvent.Details["error"] = "service_key_missing"
					}
					log.Printf("Auth: missing service key (requestId: %s, path: %s)", requestID, r.URL.Path)
					writeAuthError(w, ErrServiceKeyMissing, "Service key is required", requestID, failStatusCode)
				} else {
					if authEvent != nil {
						authEvent.Details["error"] = "service_key_invalid"
					}
					log.Printf("Auth: invalid service key (requestId: %s, path: %s)", requestID, r.URL.Path)
					writeAuthError(w, ErrServiceKeyInvalid, "Service key is invalid", requestID, failStatusCode)
				}
				if authEvent != nil {
					auditLogger.LogEvent(authEvent)
				}
				return
			}

			// Key is valid, create user context for service key authentication
			sessionID, err := generateSessionID()
			if err != nil {
				if authEvent != nil {
					authEvent.Success = false
					authEvent.Details["error"] = "session_generation_failed"
				}
				log.Printf("Auth: failed to generate session ID: %v", err)
				writeAuthError(w, ErrServiceKeyInvalid, "Failed to create session", getRequestID(r), http.StatusInternalServerError)
				if authEvent != nil {
					auditLogger.LogEvent(authEvent)
				}
				return
			}

			// Create user context for service key auth
			// Service key auth uses a generic user ID since there's no specific user
			userCtx := &UserContext{
				UserID:      "service-key-auth",
				SessionID:   sessionID,
				Permissions: []Permission{}, // Empty permissions - authorization is handled separately
				AuditID:     sessionID,      // Use session ID as audit ID for tracking
				ExpiresAt:   time.Now().Add(24 * time.Hour), // Service key sessions expire after 24 hours
				CreatedAt:   time.Now(),
			}

			// Update auth event with session information
			if authEvent != nil {
				authEvent.Success = true
				authEvent.UserID = userCtx.UserID
				authEvent.SessionID = userCtx.SessionID
				authEvent.Details["sessionCreated"] = true
				auditLogger.LogEvent(authEvent)

				// Log session creation event
				auditLogger.LogSessionCreate(userCtx.UserID, userCtx.SessionID, getRequestID(r), getClientIP(r), true, map[string]interface{}{
					"authMethod": "service_key",
				})
			}

			// Add user context to request context
			ctx := context.WithValue(r.Context(), UserContextKey, userCtx)
			next.ServeHTTP(w, r.WithContext(ctx))
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

func getClientIP(r *http.Request) string {
	// Get client IP address, respecting X-Forwarded-For if present
	ip := r.Header.Get("X-Forwarded-For")
	if ip == "" {
		ip = r.Header.Get("X-Real-IP")
	}
	if ip == "" {
		ip = r.RemoteAddr
	}
	return ip
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
func TokenAuthMiddleware(authenticator *TokenAuthenticator, loggers ...*audit.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Create audit event for authentication attempt if logger is provided
			var authEvent *audit.Event
			var auditLogger *audit.Logger
			if len(loggers) > 0 {
				auditLogger = loggers[0]
				getUserContext := func(r *http.Request) (string, string) {
					if userCtx, ok := GetUserContext(r); ok {
						return userCtx.UserID, userCtx.SessionID
					}
					return "", ""
				}
				authEvent = audit.NewEventFromRequest(r, audit.EventAuthAttempt, getUserContext)
				authEvent.Details = map[string]interface{}{
					"authMethod": "jwt_token",
				}
			}

			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				requestID := getRequestID(r)
				if authEvent != nil {
					authEvent.Success = false
					authEvent.Details["error"] = "token_missing"
				}
				log.Printf("Auth: missing Authorization header (requestId: %s, path: %s)", requestID, r.URL.Path)
				writeAuthError(w, ErrTokenMissing, "Authorization header required", requestID, http.StatusUnauthorized)
				if authEvent != nil {
					auditLogger.LogEvent(authEvent)
				}
				return
			}

			// Expected format: "Bearer <token>"
			if !strings.HasPrefix(authHeader, bearerPrefix) || len(authHeader) == len(bearerPrefix) {
				requestID := getRequestID(r)
				if authEvent != nil {
					authEvent.Success = false
					authEvent.Details["error"] = "invalid_header_format"
				}
				log.Printf("Auth: invalid authorization header format (requestId: %s, path: %s)", requestID, r.URL.Path)
				writeAuthError(w, ErrTokenInvalid, "Invalid authorization header format", requestID, http.StatusUnauthorized)
				if authEvent != nil {
					auditLogger.LogEvent(authEvent)
				}
				return
			}

			token := authHeader[len(bearerPrefix):]
			userCtx, err := authenticator.ValidateToken(token)
			if err != nil {
				requestID := getRequestID(r)
				if authEvent != nil {
					authEvent.Success = false
					authEvent.Details["error"] = "invalid_or_expired_token"
					authEvent.Details["errorDetail"] = err.Error()
				}
				log.Printf("Auth: invalid or expired token (requestId: %s, path: %s): %v", requestID, r.URL.Path, err)
				writeAuthError(w, ErrTokenInvalid, "Invalid or expired token", requestID, http.StatusUnauthorized)
				if authEvent != nil {
					auditLogger.LogEvent(authEvent)
				}
				return
			}

			if userCtx.IsExpired() {
				requestID := getRequestID(r)
				if authEvent != nil {
					authEvent.Success = false
					authEvent.Details["error"] = "token_expired"
				}
				log.Printf("Auth: token expired (requestId: %s, path: %s)", requestID, r.URL.Path)
				writeAuthError(w, ErrTokenExpired, "Token expired", requestID, http.StatusUnauthorized)
				if authEvent != nil {
					auditLogger.LogEvent(authEvent)
				}
				return
			}

			// Update auth event with success information
			if authEvent != nil {
				authEvent.Success = true
				authEvent.UserID = userCtx.UserID
				authEvent.SessionID = userCtx.SessionID
				auditLogger.LogEvent(authEvent)

				// Log session creation event
				auditLogger.LogSessionCreate(userCtx.UserID, userCtx.SessionID, getRequestID(r), getClientIP(r), true, map[string]interface{}{
					"authMethod": "jwt_token",
				})
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
