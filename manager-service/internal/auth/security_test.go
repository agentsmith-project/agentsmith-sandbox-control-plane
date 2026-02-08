package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sandbox/manager/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestJWTTokenSecurity tests JWT token security
func TestJWTTokenSecurity(t *testing.T) {
	t.Run("TokenExpiration", func(t *testing.T) {
		authenticator := auth.NewTokenAuthenticator("test-secret")

		// Create token with very short expiration
		token, err := authenticator.GenerateToken("user123", "session456", 1*time.Millisecond)
		require.NoError(t, err)

		// Token should be valid immediately
		userCtx, err := authenticator.ValidateToken(token)
		require.NoError(t, err)
		assert.Equal(t, "user123", userCtx.UserID)

		// Wait for token to expire
		time.Sleep(10 * time.Millisecond)

		// Token should be expired
		_, err = authenticator.ValidateToken(token)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "token expired")
	})

	t.Run("InvalidToken", func(t *testing.T) {
		authenticator := auth.NewTokenAuthenticator("test-secret")

		tests := []struct {
			name  string
			token string
		}{
			{"Empty token", ""},
			{"Malformed token", "not.a.valid.jwt"},
			{"Random string", "randomstring123"},
			{"Token with wrong secret", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VyX2lkIjoidXNlcjEyMyIsInNlc3Npb25faWQiOiJzZXNzaW9uNDU2In0.wrong"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, err := authenticator.ValidateToken(tt.token)
				assert.Error(t, err)
			})
		}
	})

	t.Run("TokenTampering", func(t *testing.T) {
		authenticator := auth.NewTokenAuthenticator("test-secret")

		token, err := authenticator.GenerateToken("user123", "session456", 1*time.Hour)
		require.NoError(t, err)

		// Tamper with token by changing a character
		tamperedToken := strings.Replace(token, "a", "b", 1)

		_, err = authenticator.ValidateToken(tamperedToken)
		assert.Error(t, err)
	})
}

// TestAuthorizationSecurity tests authorization security
func TestAuthorizationSecurity(t *testing.T) {
	t.Run("SessionOwnership", func(t *testing.T) {
		authorizer := auth.NewAuthorizer()

		// Create a session owned by user1
		session := &auth.Session{
			AgentThreadID: "session123",
			OwnerID:       "user1",
		}

		// user1 should be able to access
		userCtx := &auth.UserContext{
			UserID:    "user1",
			SessionID: "session123",
		}
		err := authorizer.VerifySessionAccess(context.Background(), userCtx, session)
		assert.NoError(t, err)

		// user2 should not be able to access
		userCtx2 := &auth.UserContext{
			UserID:    "user2",
			SessionID: "session123",
		}
		err = authorizer.VerifySessionAccess(context.Background(), userCtx2, session)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "forbidden")
	})

	t.Run("SessionQuota", func(t *testing.T) {
		authorizer := auth.NewAuthorizer()

		// Create 5 sessions for user1 (quota is typically 10)
		var sessions []*auth.Session
		for i := 0; i < 5; i++ {
			sessions = append(sessions, &auth.Session{
				AgentThreadID: "session" + string(rune('0'+i)),
				OwnerID:       "user1",
			})
		}

		// Should allow creating more sessions (under quota)
		err := authorizer.CheckSessionQuota(context.Background(), "user1", len(sessions), 10)
		assert.NoError(t, err)

		// Should exceed quota
		err = authorizer.CheckSessionQuota(context.Background(), "user1", len(sessions), 4)
		assert.Error(t, err)
	})
}

// TestMiddlewareSecurity tests authentication middleware security
func TestMiddlewareSecurity(t *testing.T) {
	t.Run("MissingAuthHeader", func(t *testing.T) {
		authenticator := auth.NewTokenAuthenticator("test-secret")
		middleware := auth.NewTokenAuthMiddleware(authenticator)

		req := httptest.NewRequest("GET", "/api/v1/sessions", nil)
		w := httptest.NewRecorder()

		middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("InvalidToken", func(t *testing.T) {
		authenticator := auth.NewTokenAuthenticator("test-secret")
		middleware := auth.NewTokenAuthMiddleware(authenticator)

		req := httptest.NewRequest("GET", "/api/v1/sessions", nil)
		req.Header.Set("Authorization", "Bearer invalid-token")
		w := httptest.NewRecorder()

		middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("ValidToken", func(t *testing.T) {
		authenticator := auth.NewTokenAuthenticator("test-secret")
		middleware := auth.NewTokenAuthMiddleware(authenticator)

		token, err := authenticator.GenerateToken("user123", "session456", 1*time.Hour)
		require.NoError(t, err)

		req := httptest.NewRequest("GET", "/api/v1/sessions", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()

		middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check that user context is set
			userCtx, ok := auth.GetUserContext(r)
			assert.True(t, ok)
			assert.Equal(t, "user123", userCtx.UserID)
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})
}
