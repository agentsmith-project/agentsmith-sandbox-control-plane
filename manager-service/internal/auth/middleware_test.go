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

func TestServiceKeyMiddleware_MissingKey_Returns401WithCode(t *testing.T) {
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
	assert.Equal(t, "SERVICE_KEY_MISSING", resp["error"],
		"missing key must return SERVICE_KEY_MISSING, not the generic 'unauthorized'")
}

func TestServiceKeyMiddleware_InvalidKey_Returns401WithCode(t *testing.T) {
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
	assert.Equal(t, "SERVICE_KEY_INVALID", resp["error"],
		"wrong key must return SERVICE_KEY_INVALID, distinguishable from missing key")
}

// TestServiceKeyMiddleware_MissingVsInvalidDistinct verifies the two failure modes are
// distinguishable by their error code so callers can report meaningful errors.
func TestServiceKeyMiddleware_MissingVsInvalidDistinct(t *testing.T) {
	validator, err := NewServiceKeyValidator([]string{"real-key"})
	require.NoError(t, err)
	middleware := ServiceKeyMiddleware(validator, "X-Service-Key")
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// No header → SERVICE_KEY_MISSING
	noKey := httptest.NewRequest("GET", "/", nil)
	noKeyRec := httptest.NewRecorder()
	handler.ServeHTTP(noKeyRec, noKey)
	var missingResp map[string]string
	require.NoError(t, json.Unmarshal(noKeyRec.Body.Bytes(), &missingResp))

	// Wrong header → SERVICE_KEY_INVALID
	badKey := httptest.NewRequest("GET", "/", nil)
	badKey.Header.Set("X-Service-Key", "not-real-key")
	badKeyRec := httptest.NewRecorder()
	handler.ServeHTTP(badKeyRec, badKey)
	var invalidResp map[string]string
	require.NoError(t, json.Unmarshal(badKeyRec.Body.Bytes(), &invalidResp))

	assert.NotEqual(t, missingResp["error"], invalidResp["error"],
		"missing-key and invalid-key errors must be distinct codes")
	assert.Equal(t, "SERVICE_KEY_MISSING", missingResp["error"])
	assert.Equal(t, "SERVICE_KEY_INVALID", invalidResp["error"])
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
