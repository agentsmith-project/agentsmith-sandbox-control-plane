//go:build e2e

package e2e_test

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHealthz verifies the /healthz endpoint is reachable without auth.
func TestHealthz(t *testing.T) {
	resp := newClient().Healthz(t)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.BodyString(), `"status":"ok"`)
}

// TestReadyz verifies /readyz returns a valid response (200 ready or 503 degraded).
func TestReadyz(t *testing.T) {
	resp := newClient().Readyz(t)

	assert.True(t, resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusServiceUnavailable,
		"readyz: unexpected status %d", resp.StatusCode)

	if resp.StatusCode == http.StatusOK {
		assert.Contains(t, resp.BodyString(), `"ready":true`, "readyz 200 must report ready:true")
	}
}

// TestMetricsEndpoint verifies /metrics returns Prometheus-format output without auth.
func TestMetricsEndpoint(t *testing.T) {
	resp := newClient().Metrics(t)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	body := resp.BodyString()
	assert.Contains(t, body, "# HELP", "metrics must contain HELP lines")
	assert.Contains(t, body, "# TYPE", "metrics must contain TYPE lines")
	assert.Contains(t, body, "http_request_total", "metrics must include http_request_total counter")
}

// TestMetricsIncrement verifies that making requests causes the http_request_total counter to increase.
func TestMetricsIncrement(t *testing.T) {
	c := newClient()

	// Baseline: sum ALL http_request_total values to avoid non-deterministic
	// map-iteration ordering causing the "first" metric line to differ between calls.
	before := sumMetricValues(t, c.Metrics(t).BodyString(), `http_request_total{`)

	// Issue a few requests to drive the counter up.
	c.Healthz(t)
	c.Healthz(t)
	c.Readyz(t)

	after := sumMetricValues(t, c.Metrics(t).BodyString(), `http_request_total{`)

	// We made 3 counted requests + 1 for the "after" metrics call = at least 4 more.
	assert.Greater(t, after, before, "http_request_total total should increase after requests")
}

// TestRequestIDHeader verifies the manager echoes back a X-Request-Id in the response.
func TestRequestIDHeader(t *testing.T) {
	c := newClient()
	req, err := http.NewRequest(http.MethodGet, c.baseURL+"/healthz", nil)
	require.NoError(t, err)
	req.Header.Set("X-Request-Id", "test-request-id-e2e")

	resp, err := c.http.Do(req)
	require.NoError(t, err)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	// The manager must echo the request ID in the response header.
	assert.Equal(t, "test-request-id-e2e", resp.Header.Get("X-Request-Id"),
		"manager must propagate X-Request-Id response header")
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// parseMetricValue returns the numeric value of the first Prometheus metric line
// matching the given name prefix. Returns 0 if not found.
func parseMetricValue(t *testing.T, body, prefix string) float64 {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, prefix) && !strings.HasPrefix(line, "# ") {
			var val float64
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				_, err := fmt.Sscanf(parts[len(parts)-1], "%g", &val)
				if err == nil {
					return val
				}
			}
		}
	}
	return 0
}

// sumMetricValues sums the numeric values of ALL Prometheus metric lines matching
// the given name prefix. This is more robust than parseMetricValue when the metric
// has multiple label combinations (map iteration order is non-deterministic).
func sumMetricValues(t *testing.T, body, prefix string) float64 {
	t.Helper()
	var total float64
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, prefix) && !strings.HasPrefix(line, "# ") {
			var val float64
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				if _, err := fmt.Sscanf(parts[len(parts)-1], "%g", &val); err == nil {
					total += val
				}
			}
		}
	}
	return total
}
