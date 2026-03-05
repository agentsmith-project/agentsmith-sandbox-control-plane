package observability

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---- HealthChecker ----------------------------------------------------------

func TestHealthChecker_HandleHealthz_Returns200(t *testing.T) {
	hc := NewHealthChecker()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	hc.HandleHealthz(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("HandleHealthz status = %d, want 200", rec.Code)
	}
}

func TestHealthChecker_HandleHealthz_ContentType(t *testing.T) {
	hc := NewHealthChecker()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	hc.HandleHealthz(rec, req)
	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("HandleHealthz Content-Type = %q, want application/json", ct)
	}
}

func TestHealthChecker_HandleHealthz_JSONShape(t *testing.T) {
	hc := NewHealthChecker()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	hc.HandleHealthz(rec, req)

	var resp HealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("HandleHealthz body decode error: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("HandleHealthz body.status = %q, want 'ok'", resp.Status)
	}
	if resp.Time == "" {
		t.Error("HandleHealthz body.time is empty, want RFC3339 timestamp")
	}
	if _, err := time.Parse(time.RFC3339, resp.Time); err != nil {
		t.Errorf("HandleHealthz body.time %q is not valid RFC3339: %v", resp.Time, err)
	}
}

func TestHealthChecker_HandleReadyz_NoChecks_Returns200(t *testing.T) {
	hc := NewHealthChecker()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	hc.HandleReadyz(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("HandleReadyz (no checks) status = %d, want 200", rec.Code)
	}
}

func TestHealthChecker_HandleReadyz_NoChecks_ReadyTrue(t *testing.T) {
	hc := NewHealthChecker()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	hc.HandleReadyz(rec, req)

	var resp ReadinessResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !resp.Ready {
		t.Error("HandleReadyz (no checks) ready = false, want true")
	}
}

func TestHealthChecker_HandleReadyz_AllChecksPassing_Returns200(t *testing.T) {
	hc := NewHealthChecker()
	hc.AddReadyCheck(func() error { return nil })
	hc.AddReadyCheck(func() error { return nil })

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	hc.HandleReadyz(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("HandleReadyz (all passing) status = %d, want 200", rec.Code)
	}

	var resp ReadinessResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !resp.Ready {
		t.Error("HandleReadyz (all passing) ready = false, want true")
	}
}

func TestHealthChecker_HandleReadyz_OneCheckFailing_Returns503(t *testing.T) {
	hc := NewHealthChecker()
	hc.AddReadyCheck(func() error { return nil })
	hc.AddReadyCheck(func() error { return errors.New("db not ready") })

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	hc.HandleReadyz(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("HandleReadyz (one failing) status = %d, want 503", rec.Code)
	}

	var resp ReadinessResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Ready {
		t.Error("HandleReadyz (one failing) ready = true, want false")
	}
	if !strings.Contains(resp.Message, "not ready") {
		t.Errorf("HandleReadyz (one failing) message = %q, want 'not ready'", resp.Message)
	}
}

func TestHealthChecker_HandleReadyz_AllChecksFailing_Returns503(t *testing.T) {
	hc := NewHealthChecker()
	hc.AddReadyCheck(func() error { return errors.New("err1") })
	hc.AddReadyCheck(func() error { return errors.New("err2") })

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	hc.HandleReadyz(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("HandleReadyz (all failing) status = %d, want 503", rec.Code)
	}
}

func TestHealthChecker_HandleReadyz_ContentType(t *testing.T) {
	hc := NewHealthChecker()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	hc.HandleReadyz(rec, req)
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("HandleReadyz Content-Type = %q, want application/json", ct)
	}
}

func TestHealthChecker_HandleReadyz_ReadyResponse_JSONFields(t *testing.T) {
	hc := NewHealthChecker()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	hc.HandleReadyz(rec, req)

	// Check raw JSON for field names
	body := rec.Body.String()
	for _, field := range []string{`"ready"`, `"configLoaded"`, `"k8sConnected"`, `"message"`} {
		if !strings.Contains(body, field) {
			t.Errorf("HandleReadyz ready response missing JSON field %s; body = %s", field, body)
		}
	}
}

func TestHealthChecker_HandleReadyz_UnreadyResponse_IncludesFailedCheckNames(t *testing.T) {
	hc := NewHealthChecker()
	hc.AddReadyCheck(func() error { return errors.New("db down") })

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	hc.HandleReadyz(rec, req)

	var resp ReadinessResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	// The message should include the check identifier (e.g., "check_A")
	if !strings.Contains(resp.Message, "check_A") {
		t.Errorf("HandleReadyz message = %q, want check identifier like 'check_A'", resp.Message)
	}
}

// ---- HealthResponse / ReadinessResponse JSON --------------------------------

func TestHealthResponse_JSONFieldNames(t *testing.T) {
	resp := HealthResponse{Status: "ok", Time: "2099-01-01T00:00:00Z"}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	body := string(b)
	if !strings.Contains(body, `"status"`) {
		t.Errorf("HealthResponse JSON missing 'status' field: %s", body)
	}
	if !strings.Contains(body, `"time"`) {
		t.Errorf("HealthResponse JSON missing 'time' field: %s", body)
	}
}

func TestReadinessResponse_JSONFieldNames(t *testing.T) {
	resp := ReadinessResponse{
		Ready:        true,
		ConfigLoaded: true,
		K8sConnected: true,
		ConfigHash:   "abc123",
		Message:      "ok",
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	body := string(b)
	for _, f := range []string{`"ready"`, `"configLoaded"`, `"k8sConnected"`, `"configHash"`, `"message"`} {
		if !strings.Contains(body, f) {
			t.Errorf("ReadinessResponse JSON missing field %s: %s", f, body)
		}
	}
}

func TestReadinessResponse_ConfigHashOmittedWhenEmpty(t *testing.T) {
	resp := ReadinessResponse{Ready: true, ConfigLoaded: true, K8sConnected: true}
	b, _ := json.Marshal(resp)
	if strings.Contains(string(b), "configHash") {
		t.Errorf("ReadinessResponse should omit configHash when empty; got %s", string(b))
	}
}

func TestReadinessResponse_MessageOmittedWhenEmpty(t *testing.T) {
	resp := ReadinessResponse{Ready: true, ConfigLoaded: true, K8sConnected: true}
	b, _ := json.Marshal(resp)
	if strings.Contains(string(b), `"message"`) {
		t.Errorf("ReadinessResponse should omit message when empty; got %s", string(b))
	}
}

// ---- joinStrings (package-private helper) -----------------------------------

func TestJoinStrings_Empty(t *testing.T) {
	if got := joinStrings(nil, ", "); got != "" {
		t.Errorf("joinStrings(nil) = %q, want empty", got)
	}
}

func TestJoinStrings_SingleElement(t *testing.T) {
	if got := joinStrings([]string{"a"}, ", "); got != "a" {
		t.Errorf("joinStrings([a]) = %q, want 'a'", got)
	}
}

func TestJoinStrings_MultipleElements(t *testing.T) {
	got := joinStrings([]string{"a", "b", "c"}, ", ")
	want := "a, b, c"
	if got != want {
		t.Errorf("joinStrings = %q, want %q", got, want)
	}
}

func TestJoinStrings_CustomSeparator(t *testing.T) {
	got := joinStrings([]string{"x", "y"}, " | ")
	if got != "x | y" {
		t.Errorf("joinStrings(custom sep) = %q, want 'x | y'", got)
	}
}

// ---- getCheckName (package-private helper) ----------------------------------

func TestGetCheckName_IndexZero(t *testing.T) {
	got := getCheckName(nil, 0)
	if got != "check_A" {
		t.Errorf("getCheckName(0) = %q, want 'check_A'", got)
	}
}

func TestGetCheckName_IndexOne(t *testing.T) {
	got := getCheckName(nil, 1)
	if got != "check_B" {
		t.Errorf("getCheckName(1) = %q, want 'check_B'", got)
	}
}
