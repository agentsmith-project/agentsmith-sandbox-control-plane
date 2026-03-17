//go:build e2e

package e2e_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	testWS   = "ws-e2e"
	testProj = "proj-e2e"
)

// TestAuth_NoKey verifies that requests without a service key return 401 SERVICE_KEY_MISSING.
func TestAuth_NoKey(t *testing.T) {
	wlID := uniqueID("auth-no-key")
	resp := newUnauthClient().CreateWorkload(t, testWS, testProj, wlID,
		CreateRequest{Image: suite.Image})

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Contains(t, resp.BodyString(), "SERVICE_KEY_MISSING")
}

// TestAuth_InvalidKey verifies that a wrong service key returns 401 SERVICE_KEY_INVALID.
func TestAuth_InvalidKey(t *testing.T) {
	wlID := uniqueID("auth-bad-key")
	resp := newWrongKeyClient().CreateWorkload(t, testWS, testProj, wlID,
		CreateRequest{Image: suite.Image})

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Contains(t, resp.BodyString(), "SERVICE_KEY_INVALID")
}

// TestAuth_ValidKey verifies that a correct service key passes through authentication.
// The workload creation may succeed or fail for other reasons (missing image, no cluster
// capacity) but must not return 401 or 403.
func TestAuth_ValidKey(t *testing.T) {
	wlID := uniqueID("auth-valid-key")
	resp := newClient().CreateWorkload(t, testWS, testProj, wlID,
		CreateRequest{Image: suite.Image})

	assert.NotEqual(t, http.StatusUnauthorized, resp.StatusCode, "valid key must not produce 401")
	assert.NotEqual(t, http.StatusForbidden, resp.StatusCode, "valid key must not produce 403")
}

// TestAuth_HealthzNoAuthRequired verifies that the health endpoint is open (no key needed).
func TestAuth_HealthzNoAuthRequired(t *testing.T) {
	resp := newUnauthClient().Healthz(t)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestAuth_MetricsNoAuthRequired verifies that the metrics endpoint is open.
func TestAuth_MetricsNoAuthRequired(t *testing.T) {
	resp := newUnauthClient().Metrics(t)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestAuth_ReadyzNoAuthRequired verifies that the readiness endpoint is open (no key needed).
func TestAuth_ReadyzNoAuthRequired(t *testing.T) {
	resp := newUnauthClient().Readyz(t)
	assert.True(t, resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusServiceUnavailable,
		"readyz must not require auth; got %d", resp.StatusCode)
}
