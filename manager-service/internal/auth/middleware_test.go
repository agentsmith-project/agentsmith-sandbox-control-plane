package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceKeyMiddleware_ValidKey_AllowsRequest(t *testing.T) {
	validator, err := NewServiceKeyValidator([]string{"valid-key-123"})
	require.NoError(t, err)

	middleware := ServiceKeyMiddleware(validator, "X-Service-Key")
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

	middleware := ServiceKeyMiddleware(validator, "X-Service-Key")
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	var resp map[string]string
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "unauthorized", resp["error"])
}

func TestServiceKeyMiddleware_InvalidKey_Returns401(t *testing.T) {
	validator, err := NewServiceKeyValidator([]string{"valid-key-123"})
	require.NoError(t, err)

	middleware := ServiceKeyMiddleware(validator, "X-Service-Key")
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Service-Key", "invalid-key")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	var resp map[string]string
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "unauthorized", resp["error"])
}

func TestServiceKeyMiddleware_EmptyHeaderName_DefaultsToXServiceKey(t *testing.T) {
	validator, err := NewServiceKeyValidator([]string{"valid-key-123"})
	require.NoError(t, err)

	middleware := ServiceKeyMiddleware(validator, "")
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Service-Key", "valid-key-123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestServiceKeyMiddleware_CustomHeaderName(t *testing.T) {
	validator, err := NewServiceKeyValidator([]string{"valid-key-123"})
	require.NoError(t, err)

	middleware := ServiceKeyMiddleware(validator, "X-Custom-Key")
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Custom-Key", "valid-key-123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}
