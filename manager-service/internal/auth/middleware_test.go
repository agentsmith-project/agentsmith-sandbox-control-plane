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

	var resp struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "service_key_missing", resp.Error.Code,
		"missing key must return a stable missing-key code, not the generic 'unauthorized'")
	assert.NotContains(t, rec.Body.String(), "valid-key-123")
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

	var resp struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "service_key_invalid", resp.Error.Code,
		"wrong key must return a stable invalid-key code, distinguishable from missing key")
	assert.NotContains(t, rec.Body.String(), "invalid-key")
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

	// No header -> service_key_missing
	noKey := httptest.NewRequest("GET", "/", nil)
	noKeyRec := httptest.NewRecorder()
	handler.ServeHTTP(noKeyRec, noKey)
	var missingResp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(noKeyRec.Body.Bytes(), &missingResp))

	// Wrong header -> service_key_invalid
	badKey := httptest.NewRequest("GET", "/", nil)
	badKey.Header.Set("X-Service-Key", "not-real-key")
	badKeyRec := httptest.NewRecorder()
	handler.ServeHTTP(badKeyRec, badKey)
	var invalidResp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(badKeyRec.Body.Bytes(), &invalidResp))

	assert.NotEqual(t, missingResp.Error.Code, invalidResp.Error.Code,
		"missing-key and invalid-key errors must be distinct codes")
	assert.Equal(t, "service_key_missing", missingResp.Error.Code)
	assert.Equal(t, "service_key_invalid", invalidResp.Error.Code)
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
