package httperror

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentsmith-project/agentsmith-sandbox-control-plane/internal/observability"
)

func TestWriteUsesRequestIDFromMiddlewareContextForCustomHeader(t *testing.T) {
	const headerName = "X-ASBCP-Request-ID"

	tests := []struct {
		name      string
		requestID string
	}{
		{name: "client provided", requestID: "custom-request-id"},
		{name: "middleware generated", requestID: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := observability.RequestIDMiddleware(headerName)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Write(w, r, http.StatusBadRequest, "invalid_request", "invalid input")
			}))

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			if tt.requestID != "" {
				req.Header.Set(headerName, tt.requestID)
			}
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			responseRequestID := rr.Header().Get(headerName)
			if responseRequestID == "" {
				t.Fatal("response request id header is empty")
			}
			if tt.requestID != "" && responseRequestID != tt.requestID {
				t.Fatalf("response request id header = %q, want %q", responseRequestID, tt.requestID)
			}

			var body Envelope
			if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
				t.Fatalf("decode error envelope: %v", err)
			}

			if got := body.Error.RequestID; got != responseRequestID {
				t.Fatalf("error envelope request_id = %q, want response header %q", got, responseRequestID)
			}
		})
	}
}

func TestHTTPErrorWriteRedactsCredentialLikeMessages(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()
	message := `dependency failed: Authorization: Bearer raw-token-123 token=abc123 password="p@ss" postgres://user:pass@db.internal/asbcp`

	Write(rr, req, http.StatusBadGateway, "dependency_failure", message)

	var body Envelope
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	for _, leaked := range []string{"raw-token-123", "abc123", "p@ss", "user:pass"} {
		if strings.Contains(body.Error.Message, leaked) {
			t.Fatalf("public error message leaked credential-like value %q in %q", leaked, body.Error.Message)
		}
	}
	if !strings.Contains(body.Error.Message, "[REDACTED]") {
		t.Fatalf("public error message should mark redacted dependency material, got %q", body.Error.Message)
	}
}

func TestWriteOmitsEmptyDetails(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()

	Write(rr, req, http.StatusServiceUnavailable, "not_ready", "not ready")

	var raw map[string]map[string]json.RawMessage
	if err := json.NewDecoder(rr.Body).Decode(&raw); err != nil {
		t.Fatalf("decode raw envelope: %v", err)
	}
	if _, ok := raw["error"]["details"]; ok {
		t.Fatalf("legacy Write must omit empty details, got %s", rr.Body.String())
	}
}

func TestWriteWithDetailsIncludesSanitizedDetails(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()

	WriteWithDetails(rr, req, http.StatusServiceUnavailable, "not_ready", "not ready", map[string]string{
		"operation":   "workspace_binding.get",
		"resource":    "persistent_volume_claim",
		"status":      "not_ready",
		"retry_after": "1",
		"blank":       " ",
		"token":       "token=raw-secret",
	})

	var body Envelope
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if got := body.Error.Details["operation"]; got != "workspace_binding.get" {
		t.Fatalf("operation detail = %q", got)
	}
	if got := body.Error.Details["retry_after"]; got != "1" {
		t.Fatalf("retry_after detail = %q", got)
	}
	if _, ok := body.Error.Details["blank"]; ok {
		t.Fatalf("blank detail should be omitted: %#v", body.Error.Details)
	}
	if strings.Contains(body.Error.Details["token"], "raw-secret") {
		t.Fatalf("detail leaked credential-like value: %#v", body.Error.Details)
	}
}
