package afscp

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientGetOrchestratorMountPlanUsesOrchestratorHeaders(t *testing.T) {
	var got struct {
		Method         string
		Path           string
		Authorization  string
		CallerService  string
		NamespaceID    string
		CorrelationID  string
		ActorType      string
		IdempotencyKey string
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Method = r.Method
		got.Path = r.URL.Path
		got.Authorization = r.Header.Get(HeaderAuthorization)
		got.CallerService = r.Header.Get(HeaderCallerService)
		got.NamespaceID = r.Header.Get(HeaderNamespaceID)
		got.CorrelationID = r.Header.Get(HeaderCorrelationID)
		got.ActorType = r.Header.Get(HeaderActorType)
		got.IdempotencyKey = r.Header.Get(HeaderIdempotencyKey)
		_ = json.NewEncoder(w).Encode(OrchestratorMountPlan{
			MountBindingID:      "wmb_123",
			VolumeID:            "vol_123",
			PayloadVolumeSubdir: "afscp/ns_123/repos/repo_123/payload",
			MountPath:           "/home/task-abc",
			ReadOnly:            true,
			SecretRef:           SecretRef{Namespace: "afscp-mounts", Name: "juicefs-vol-123"},
			SecurityPolicy:      SecurityPolicy{RunAsNonRoot: true, JVSControlOutsidePayload: true},
		})
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(ClientConfig{BaseURL: server.URL, Token: "orchestrator-token", CallerService: "agentsmith-sandbox-control-plane"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	plan, err := client.GetOrchestratorMountPlan(t.Context(), "ns_123", "wmb_123", "corr-123")
	if err != nil {
		t.Fatalf("GetOrchestratorMountPlan: %v", err)
	}

	if plan.PayloadVolumeSubdir != "afscp/ns_123/repos/repo_123/payload" || plan.SecretRef.Name == "" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	if got.Method != http.MethodGet || got.Path != "/internal/v1/workload-mount-bindings/wmb_123/orchestrator-plan" {
		t.Fatalf("request = %s %s", got.Method, got.Path)
	}
	if got.Authorization != "Bearer orchestrator-token" || got.CallerService != "agentsmith-sandbox-control-plane" || got.NamespaceID != "ns_123" || got.CorrelationID != "corr-123" {
		t.Fatalf("headers = %#v", got)
	}
	if got.ActorType != "" || got.IdempotencyKey != "" {
		t.Fatalf("GET plan should not send mutating headers: %#v", got)
	}
}

func TestClientReleaseAndStatusUseMutationHeaders(t *testing.T) {
	var requests []struct {
		Method         string
		Path           string
		IdempotencyKey string
		ActorType      string
		ActorID        string
		Body           map[string]string
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		requests = append(requests, struct {
			Method         string
			Path           string
			IdempotencyKey string
			ActorType      string
			ActorID        string
			Body           map[string]string
		}{
			Method:         r.Method,
			Path:           r.URL.Path,
			IdempotencyKey: r.Header.Get(HeaderIdempotencyKey),
			ActorType:      r.Header.Get(HeaderActorType),
			ActorID:        r.Header.Get(HeaderActorID),
			Body:           body,
		})
		_ = json.NewEncoder(w).Encode(OperationEnvelope{OperationID: "op_123", OperationState: "succeeded"})
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(ClientConfig{BaseURL: server.URL, Token: "token", CallerService: "agentsmith-sandbox-control-plane", ActorID: "agentsmith-sandbox-control-plane"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = client.ReleaseWorkloadMountBinding(t.Context(), "ns_123", "wmb_123", "corr-123", "idem-release")
	if err != nil {
		t.Fatalf("ReleaseWorkloadMountBinding: %v", err)
	}
	_, err = client.UpdateWorkloadMountStatus(t.Context(), "ns_123", "wmb_123", "released", "pod deleted", time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC), "corr-123", "idem-status")
	if err != nil {
		t.Fatalf("UpdateWorkloadMountStatus: %v", err)
	}

	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(requests))
	}
	if requests[0].Method != http.MethodPost || requests[0].Path != "/internal/v1/workload-mount-bindings/wmb_123:release" || requests[0].IdempotencyKey != "idem-release" {
		t.Fatalf("release request = %#v", requests[0])
	}
	if requests[1].Method != http.MethodPatch || requests[1].Path != "/internal/v1/workload-mount-bindings/wmb_123/status" || requests[1].IdempotencyKey != "idem-status" {
		t.Fatalf("status request = %#v", requests[1])
	}
	if requests[0].ActorType != "system" || requests[0].ActorID != "agentsmith-sandbox-control-plane" || requests[1].ActorType != "system" || requests[1].ActorID != "agentsmith-sandbox-control-plane" {
		t.Fatalf("mutation actor headers = %#v", requests)
	}
	if requests[1].Body["status"] != "released" || requests[1].Body["reason"] != "pod deleted" || requests[1].Body["observed_at"] != "2026-05-10T12:00:00Z" {
		t.Fatalf("status body = %#v", requests[1].Body)
	}
}

func TestClientReleaseAndStatusWaitForTerminalSuccess(t *testing.T) {
	counts := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		counts[key]++
		state := "queued"
		if counts[key] > 1 {
			state = "succeeded"
		}
		_ = json.NewEncoder(w).Encode(OperationEnvelope{OperationID: "op_wait", OperationState: state})
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(ClientConfig{BaseURL: server.URL, Token: "token", CallerService: "agentsmith-sandbox-control-plane", OperationPollInterval: time.Millisecond})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	if _, err := client.ReleaseWorkloadMountBinding(t.Context(), "ns_123", "wmb_123", "corr-123", "idem-release"); err != nil {
		t.Fatalf("ReleaseWorkloadMountBinding: %v", err)
	}
	if _, err := client.UpdateWorkloadMountStatus(t.Context(), "ns_123", "wmb_123", "released", "pod deleted", time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC), "corr-123", "idem-status"); err != nil {
		t.Fatalf("UpdateWorkloadMountStatus: %v", err)
	}

	releaseKey := http.MethodPost + " /internal/v1/workload-mount-bindings/wmb_123:release"
	statusKey := http.MethodPatch + " /internal/v1/workload-mount-bindings/wmb_123/status"
	if counts[releaseKey] != 2 {
		t.Fatalf("release calls = %d, want 2 to confirm queued operation", counts[releaseKey])
	}
	if counts[statusKey] != 2 {
		t.Fatalf("status calls = %d, want 2 to confirm queued operation", counts[statusKey])
	}
}

func TestClientConfirmedMutationPendingTimeoutReturnsTypedError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(OperationEnvelope{OperationID: "op_pending", OperationState: "running"})
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(ClientConfig{
		BaseURL:               server.URL,
		Token:                 "token",
		CallerService:         "agentsmith-sandbox-control-plane",
		OperationWaitTimeout:  5 * time.Millisecond,
		OperationPollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, err = client.ReleaseWorkloadMountBinding(t.Context(), "ns_123", "wmb_123", "corr-123", "idem-release")
	if err == nil {
		t.Fatal("expected pending timeout error")
	}
	var pending *PendingOperationError
	if !errors.As(err, &pending) {
		t.Fatalf("expected PendingOperationError, got %T %v", err, err)
	}
	if pending.OperationID != "op_pending" || pending.OperationState != "running" {
		t.Fatalf("pending error = %#v", pending)
	}
	if !pending.Retryable {
		t.Fatalf("pending timeout should be retryable: %#v", pending)
	}
}

func TestClientNon2XXPendingEnvelopeReturnsTypedError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(struct {
			Error struct {
				Code      string            `json:"code"`
				Message   string            `json:"message"`
				RequestID string            `json:"request_id"`
				Details   map[string]string `json:"details"`
			} `json:"error"`
		}{
			Error: struct {
				Code      string            `json:"code"`
				Message   string            `json:"message"`
				RequestID string            `json:"request_id"`
				Details   map[string]string `json:"details"`
			}{
				Code:      "EXPORT_NOT_READY",
				Message:   "export is not ready",
				RequestID: "afscp-req-123",
				Details: map[string]string{
					"operation_id":    "op_export",
					"operation_state": "running",
				},
			},
		})
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(ClientConfig{
		BaseURL:       server.URL,
		Token:         "token",
		CallerService: "agentsmith-sandbox-control-plane",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, err = client.ReleaseWorkloadMountBinding(t.Context(), "ns_123", "wmb_123", "corr-123", "idem-release")
	if err == nil {
		t.Fatal("expected pending AFSCP envelope error")
	}
	var pending *PendingOperationError
	if !errors.As(err, &pending) {
		t.Fatalf("expected PendingOperationError, got %T %v", err, err)
	}
	if pending.StatusCode != http.StatusConflict || pending.Code != "EXPORT_NOT_READY" || pending.RequestID != "afscp-req-123" {
		t.Fatalf("pending error context = %#v", pending)
	}
	if pending.OperationID != "op_export" || pending.OperationState != "running" || !pending.Retryable {
		t.Fatalf("pending error operation = %#v", pending)
	}
}

func TestClientNon2XXPendingishEnvelopeWithTerminalStateReturnsDependencyError(t *testing.T) {
	tests := []struct {
		name   string
		code   string
		state  string
		reason string
	}{
		{
			name:   "failed operation wins over not-ready code",
			code:   "EXPORT_NOT_READY",
			state:  "failed",
			reason: "release pending",
		},
		{
			name:   "cancelled operation wins over pending reason",
			code:   "OPERATION_PENDING",
			state:  "cancelled",
			reason: "EXPORT_NOT_READY",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := json.Marshal(map[string]any{
				"error": map[string]any{
					"code":       tt.code,
					"message":    "export is not ready",
					"reason":     tt.reason,
					"request_id": "afscp-req-terminal",
					"details": map[string]any{
						"operation_id":    "op_terminal",
						"operation_state": tt.state,
					},
				},
			})
			if err != nil {
				t.Fatalf("marshal payload: %v", err)
			}

			err = afscpErrorFromResponse(http.StatusConflict, payload)
			if err == nil {
				t.Fatal("expected terminal dependency error")
			}
			var pending *PendingOperationError
			if errors.As(err, &pending) {
				t.Fatalf("terminal state must not return PendingOperationError: %#v", pending)
			}
			var dependency *DependencyError
			if !errors.As(err, &dependency) {
				t.Fatalf("expected DependencyError, got %T %v", err, err)
			}
			if dependency.Code != tt.code || dependency.OperationID != "op_terminal" || dependency.OperationState != tt.state || dependency.RequestID != "afscp-req-terminal" {
				t.Fatalf("dependency error = %#v", dependency)
			}
		})
	}
}
