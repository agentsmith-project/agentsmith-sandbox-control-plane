package observability

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// HandleHealthz
// ---------------------------------------------------------------------------

func TestHandleHealthz_ReturnsOK(t *testing.T) {
	hc := NewHealthChecker()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	hc.HandleHealthz(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp HealthResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)

	assert.Equal(t, "ok", resp.Status)
	assert.NotEmpty(t, resp.Time, "time field must not be empty")
}

// ---------------------------------------------------------------------------
// HandleReadyz
// ---------------------------------------------------------------------------

func TestHandleReadyz_NoChecks(t *testing.T) {
	hc := NewHealthChecker()

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	hc.HandleReadyz(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp ReadinessResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)

	assert.True(t, resp.Ready)
}

func TestHandleReadyz_AllPass(t *testing.T) {
	hc := NewHealthChecker()
	hc.AddReadyCheck(func() error { return nil })
	hc.AddReadyCheck(func() error { return nil })

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	hc.HandleReadyz(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp ReadinessResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)

	assert.True(t, resp.Ready)
}

func TestHandleReadyz_OneFails(t *testing.T) {
	hc := NewHealthChecker()
	hc.AddReadyCheck(func() error { return nil })
	hc.AddReadyCheck(func() error { return fmt.Errorf("db unreachable") })

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	hc.HandleReadyz(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	var resp ReadinessResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)

	assert.False(t, resp.Ready)
	assert.Contains(t, resp.Message, "check_B",
		"message should contain the name of the failing check")
}

func TestHandleReadyz_AllFail(t *testing.T) {
	hc := NewHealthChecker()
	hc.AddReadyCheck(func() error { return fmt.Errorf("config not loaded") })
	hc.AddReadyCheck(func() error { return fmt.Errorf("k8s unreachable") })

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	hc.HandleReadyz(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	var resp ReadinessResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)

	assert.False(t, resp.Ready)
}

// ---------------------------------------------------------------------------
// AddReadyCheck
// ---------------------------------------------------------------------------

func TestAddReadyCheck(t *testing.T) {
	hc := NewHealthChecker()
	assert.Empty(t, hc.readyChecks, "new checker should have no checks")

	called := false
	hc.AddReadyCheck(func() error {
		called = true
		return nil
	})

	assert.Len(t, hc.readyChecks, 1, "should have one check after AddReadyCheck")

	// Trigger the check through HandleReadyz and verify it was executed.
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	hc.HandleReadyz(rec, req)

	assert.True(t, called, "check function should have been called by HandleReadyz")
	assert.Equal(t, http.StatusOK, rec.Code)
}

// ---------------------------------------------------------------------------
// getCheckName (unexported helper)
// ---------------------------------------------------------------------------

func TestGetCheckName(t *testing.T) {
	dummyCheck := func() error { return nil }

	tests := []struct {
		index    int
		expected string
	}{
		{0, "check_A"},
		{1, "check_B"},
		{2, "check_C"},
		{25, "check_Z"},
	}

	for _, tc := range tests {
		t.Run(tc.expected, func(t *testing.T) {
			assert.Equal(t, tc.expected, getCheckName(dummyCheck, tc.index))
		})
	}
}

// ---------------------------------------------------------------------------
// joinStrings (unexported helper)
// ---------------------------------------------------------------------------

func TestJoinStrings(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		sep      string
		expected string
	}{
		{"empty slice", []string{}, ", ", ""},
		{"one element", []string{"alpha"}, ", ", "alpha"},
		{"multiple elements", []string{"alpha", "beta", "gamma"}, ", ", "alpha, beta, gamma"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, joinStrings(tc.input, tc.sep))
		})
	}
}
