package auth_test

import (
	"testing"
	"time"

	"github.com/sandbox/manager/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenAuthenticator_GenerateToken(t *testing.T) {
	authenticator := auth.NewTokenAuthenticator("test-issuer", []byte("test-secret"), 1*time.Hour)

	token, err := authenticator.GenerateToken("user123")
	require.NoError(t, err)
	assert.NotEmpty(t, token)
}

func TestTokenAuthenticator_ValidateToken_Valid(t *testing.T) {
	authenticator := auth.NewTokenAuthenticator("test-issuer", []byte("test-secret"), 1*time.Hour)

	token, _ := authenticator.GenerateToken("user123")
	userCtx, err := authenticator.ValidateToken(token)

	require.NoError(t, err)
	assert.Equal(t, "user123", userCtx.UserID)
	assert.NotEmpty(t, userCtx.SessionID)
}

func TestTokenAuthenticator_ValidateToken_Invalid(t *testing.T) {
	authenticator := auth.NewTokenAuthenticator("test-issuer", []byte("test-secret"), 1*time.Hour)

	_, err := authenticator.ValidateToken("invalid-token")
	assert.Error(t, err)
}

func TestTokenAuthenticator_ValidateToken_Expired(t *testing.T) {
	authenticator := auth.NewTokenAuthenticator("test-issuer", []byte("test-secret"), 1*time.Millisecond)

	token, _ := authenticator.GenerateToken("user123")
	time.Sleep(10 * time.Millisecond)

	_, err := authenticator.ValidateToken(token)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}

func TestTokenAuthenticator_GenerateToken_DifferentUsers(t *testing.T) {
	authenticator := auth.NewTokenAuthenticator("test-issuer", []byte("test-secret"), 1*time.Hour)

	token1, err1 := authenticator.GenerateToken("user1")
	token2, err2 := authenticator.GenerateToken("user2")

	require.NoError(t, err1)
	require.NoError(t, err2)
	assert.NotEqual(t, token1, token2)
}

func TestTokenAuthenticator_ValidateToken_WrongSecret(t *testing.T) {
	authenticator1 := auth.NewTokenAuthenticator("test-issuer", []byte("secret1"), 1*time.Hour)
	authenticator2 := auth.NewTokenAuthenticator("test-issuer", []byte("secret2"), 1*time.Hour)

	token, _ := authenticator1.GenerateToken("user123")
	_, err := authenticator2.ValidateToken(token)
	assert.Error(t, err)
}

func TestTokenAuthenticator_ValidateToken_MalformedToken(t *testing.T) {
	authenticator := auth.NewTokenAuthenticator("test-issuer", []byte("test-secret"), 1*time.Hour)

	tests := []struct {
		name  string
		token string
	}{
		{"empty string", ""},
		{"just bearer", "Bearer"},
		{"no dots", "not-a-token"},
		{"one dot", "only.one"},
		{"invalid base64", "abc.def.ghi"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := authenticator.ValidateToken(tt.token)
			assert.Error(t, err)
		})
	}
}

func TestTokenAuthenticator_GenerateToken_SessionIDUnique(t *testing.T) {
	authenticator := auth.NewTokenAuthenticator("test-issuer", []byte("test-secret"), 1*time.Hour)

	token1, _ := authenticator.GenerateToken("user123")
	token2, _ := authenticator.GenerateToken("user123")

	// Same user should get different session IDs
	userCtx1, _ := authenticator.ValidateToken(token1)
	userCtx2, _ := authenticator.ValidateToken(token2)

	assert.NotEqual(t, userCtx1.SessionID, userCtx2.SessionID)
}
