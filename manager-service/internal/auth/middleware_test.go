package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceKeyMiddleware_NoKeysConfigured_AllowsRequest(t *testing.T) {
	validator := &ServiceKeyValidator{}
	middleware := ServiceKeyMiddleware(validator, "X-Service-Key", false, "", 401)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "success", rec.Body.String())
}

func TestServiceKeyMiddleware_ValidKey_AllowsRequest(t *testing.T) {
	validator, err := NewServiceKeyValidator([]string{"valid-key-123"})
	require.NoError(t, err)

	middleware := ServiceKeyMiddleware(validator, "X-Service-Key", false, "", 401)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Service-Key", "valid-key-123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "success", rec.Body.String())
}

func TestServiceKeyMiddleware_MissingKey_Returns401(t *testing.T) {
	validator, err := NewServiceKeyValidator([]string{"valid-key-123"})
	require.NoError(t, err)

	middleware := ServiceKeyMiddleware(validator, "X-Service-Key", false, "", 401)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	var resp ErrorResponse
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, string(ErrServiceKeyMissing), resp.Error.Code)
	assert.Equal(t, "Service key is required", resp.Error.Message)
}

func TestServiceKeyMiddleware_InvalidKey_Returns401(t *testing.T) {
	validator, err := NewServiceKeyValidator([]string{"valid-key-123"})
	require.NoError(t, err)

	middleware := ServiceKeyMiddleware(validator, "X-Service-Key", false, "", 401)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Service-Key", "invalid-key")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	var resp ErrorResponse
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, string(ErrServiceKeyInvalid), resp.Error.Code)
	assert.Equal(t, "Service key is invalid", resp.Error.Message)
}

func TestServiceKeyMiddleware_AuthorizationHeader_AcceptsValidKey(t *testing.T) {
	validator, err := NewServiceKeyValidator([]string{"valid-key-123"})
	require.NoError(t, err)

	middleware := ServiceKeyMiddleware(validator, "X-Service-Key", true, "ServiceKey", 401)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "ServiceKey valid-key-123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestServiceKeyMiddleware_CustomHeaderTakesPrecedence(t *testing.T) {
	validator, err := NewServiceKeyValidator([]string{"custom-key", "auth-key"})
	require.NoError(t, err)

	middleware := ServiceKeyMiddleware(validator, "X-Service-Key", true, "ServiceKey", 401)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Both headers present, custom header should take precedence
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Service-Key", "custom-key")
	req.Header.Set("Authorization", "ServiceKey auth-key")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestServiceKeyMiddleware_RequestID_IncludedInResponse(t *testing.T) {
	validator, err := NewServiceKeyValidator([]string{"valid-key-123"})
	require.NoError(t, err)

	middleware := ServiceKeyMiddleware(validator, "X-Service-Key", false, "", 401)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-Id", "test-request-id-123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	var resp ErrorResponse
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "test-request-id-123", resp.Error.RequestID)
}

func TestServiceKeyMiddleware_CustomStatusCode(t *testing.T) {
	validator, err := NewServiceKeyValidator([]string{"valid-key-123"})
	require.NoError(t, err)

	middleware := ServiceKeyMiddleware(validator, "X-Service-Key", false, "", 403)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestGetRequestID_XRequestID(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-Id", "test-id")

	requestID := getRequestID(req)
	assert.Equal(t, "test-id", requestID)
}

func TestGetRequestID_XRequestIDUpperCase(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-ID", "test-id")

	requestID := getRequestID(req)
	assert.Equal(t, "test-id", requestID)
}

func TestGetRequestID_RequestId(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Request-Id", "test-id")

	requestID := getRequestID(req)
	assert.Equal(t, "test-id", requestID)
}

func TestGetRequestID_NoRequestID(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)

	requestID := getRequestID(req)
	assert.Equal(t, "", requestID)
}

func TestOptionalAuthMiddleware_NoKeys_AllowsRequest(t *testing.T) {
	validator := &ServiceKeyValidator{}
	middleware := OptionalAuthMiddleware(validator, "X-Service-Key", false, "", 401)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestOptionalAuthMiddleware_WithKeys_EnforcesAuth(t *testing.T) {
	validator, err := NewServiceKeyValidator([]string{"valid-key-123"})
	require.NoError(t, err)

	middleware := OptionalAuthMiddleware(validator, "X-Service-Key", false, "", 401)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestCombineMiddleware_AppliesInReverseOrder(t *testing.T) {
	callOrder := []string{}

	middleware1 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callOrder = append(callOrder, "middleware1-before")
			next.ServeHTTP(w, r)
			callOrder = append(callOrder, "middleware1-after")
		})
	}

	middleware2 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callOrder = append(callOrder, "middleware2-before")
			next.ServeHTTP(w, r)
			callOrder = append(callOrder, "middleware2-after")
		})
	}

	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callOrder = append(callOrder, "handler")
		w.WriteHeader(http.StatusOK)
	})

	combined := CombineMiddleware(middleware1, middleware2)
	handler := combined(final)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []string{
		"middleware1-before",
		"middleware2-before",
		"handler",
		"middleware2-after",
		"middleware1-after",
	}, callOrder)
}

func TestCombineMiddleware_Empty(t *testing.T) {
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	combined := CombineMiddleware()
	handler := combined(final)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestServiceKeyMiddleware_SetsUserContext(t *testing.T) {
	validator, err := NewServiceKeyValidator([]string{"valid-key-123"})
	require.NoError(t, err)

	middleware := ServiceKeyMiddleware(validator, "X-Service-Key", false, "", 401)

	var capturedUserCtx *UserContext
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userCtx, ok := GetUserContext(r)
		require.True(t, ok, "User context should be set")
		capturedUserCtx = userCtx
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Service-Key", "valid-key-123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.NotNil(t, capturedUserCtx, "User context should not be nil")
	assert.Equal(t, "service-key-auth", capturedUserCtx.UserID, "UserID should be 'service-key-auth'")
	assert.NotEmpty(t, capturedUserCtx.SessionID, "SessionID should be generated")
	assert.NotEmpty(t, capturedUserCtx.AuditID, "AuditID should be set")
	assert.False(t, capturedUserCtx.IsExpired(), "User context should not be expired")
}
