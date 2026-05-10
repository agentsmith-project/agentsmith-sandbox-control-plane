package afscp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const (
	HeaderAuthorization  = "Authorization"
	HeaderIdempotencyKey = "Idempotency-Key"
	HeaderCorrelationID  = "X-Correlation-Id"
	HeaderNamespaceID    = "X-AFSCP-Namespace-Id"
	HeaderActorType      = "X-AFSCP-Actor-Type"
	HeaderActorID        = "X-AFSCP-Actor-Id"
	HeaderCallerService  = "X-AFSCP-Caller-Service"
)

type Client struct {
	baseURL       *url.URL
	httpClient    *http.Client
	token         string
	callerService string
	actorType     string
	actorID       string
}

type ClientConfig struct {
	BaseURL       string
	Token         string
	CallerService string
	ActorType     string
	ActorID       string
	HTTPClient    *http.Client
}

type SecretRef struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

type SecurityPolicy struct {
	RunAsNonRoot             bool `json:"run_as_non_root"`
	AllowPrivileged          bool `json:"allow_privileged"`
	JVSControlOutsidePayload bool `json:"jvs_control_outside_payload"`
}

type OrchestratorMountPlan struct {
	MountBindingID      string         `json:"mount_binding_id"`
	VolumeID            string         `json:"volume_id"`
	PayloadVolumeSubdir string         `json:"payload_volume_subdir"`
	MountPath           string         `json:"mount_path"`
	ReadOnly            bool           `json:"read_only"`
	SecretRef           SecretRef      `json:"secret_ref"`
	SecurityPolicy      SecurityPolicy `json:"security_policy"`
}

type OperationEnvelope struct {
	OperationID    string         `json:"operation_id"`
	OperationState string         `json:"operation_state"`
	Resource       map[string]any `json:"resource"`
	Result         map[string]any `json:"result"`
	Error          map[string]any `json:"error"`
}

func NewClient(config ClientConfig) (*Client, error) {
	rawBaseURL := strings.TrimSpace(config.BaseURL)
	if rawBaseURL == "" {
		return nil, fmt.Errorf("afscp base url is required")
	}
	baseURL, err := url.Parse(rawBaseURL)
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" {
		return nil, fmt.Errorf("afscp base url is invalid")
	}
	token := strings.TrimSpace(config.Token)
	if token == "" {
		return nil, fmt.Errorf("afscp orchestrator token is required")
	}
	callerService := firstNonEmpty(config.CallerService, "sandbox-manager")
	actorType := firstNonEmpty(config.ActorType, "system")
	actorID := firstNonEmpty(config.ActorID, callerService)
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{
		baseURL:       baseURL,
		httpClient:    httpClient,
		token:         token,
		callerService: callerService,
		actorType:     actorType,
		actorID:       actorID,
	}, nil
}

func (c *Client) GetOrchestratorMountPlan(ctx context.Context, namespaceID, mountBindingID, correlationID string) (OrchestratorMountPlan, error) {
	var plan OrchestratorMountPlan
	err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/internal/v1/workload-mount-bindings/%s/orchestrator-plan", url.PathEscape(mountBindingID)), namespaceID, correlationID, "", nil, &plan)
	if err != nil {
		return OrchestratorMountPlan{}, err
	}
	return plan, nil
}

func (c *Client) HeartbeatWorkloadMountBinding(ctx context.Context, namespaceID, mountBindingID, correlationID, idempotencyKey string) (OperationEnvelope, error) {
	return c.emptyMutation(ctx, fmt.Sprintf("/internal/v1/workload-mount-bindings/%s:heartbeat", url.PathEscape(mountBindingID)), namespaceID, correlationID, idempotencyKey)
}

func (c *Client) ReleaseWorkloadMountBinding(ctx context.Context, namespaceID, mountBindingID, correlationID, idempotencyKey string) (OperationEnvelope, error) {
	return c.emptyMutation(ctx, fmt.Sprintf("/internal/v1/workload-mount-bindings/%s:release", url.PathEscape(mountBindingID)), namespaceID, correlationID, idempotencyKey)
}

func (c *Client) UpdateWorkloadMountStatus(ctx context.Context, namespaceID, mountBindingID, status, reason string, observedAt time.Time, correlationID, idempotencyKey string) (OperationEnvelope, error) {
	body := map[string]string{
		"status":      strings.TrimSpace(status),
		"observed_at": observedAt.UTC().Format(time.RFC3339),
	}
	if strings.TrimSpace(reason) != "" {
		body["reason"] = strings.TrimSpace(reason)
	}
	var envelope OperationEnvelope
	err := c.doJSON(ctx, http.MethodPatch, fmt.Sprintf("/internal/v1/workload-mount-bindings/%s/status", url.PathEscape(mountBindingID)), namespaceID, correlationID, idempotencyKey, body, &envelope)
	if err != nil {
		return OperationEnvelope{}, err
	}
	return envelope, nil
}

func (c *Client) emptyMutation(ctx context.Context, path string, namespaceID, correlationID, idempotencyKey string) (OperationEnvelope, error) {
	var envelope OperationEnvelope
	err := c.doJSON(ctx, http.MethodPost, path, namespaceID, correlationID, idempotencyKey, nil, &envelope)
	if err != nil {
		return OperationEnvelope{}, err
	}
	return envelope, nil
}

func (c *Client) doJSON(ctx context.Context, method, requestPath, namespaceID, correlationID, idempotencyKey string, body any, out any) error {
	if c == nil {
		return fmt.Errorf("afscp client is not configured")
	}
	if strings.TrimSpace(namespaceID) == "" {
		return fmt.Errorf("afscp namespace id is required")
	}
	if strings.TrimSpace(correlationID) == "" {
		return fmt.Errorf("afscp correlation id is required")
	}
	if method != http.MethodGet && strings.TrimSpace(idempotencyKey) == "" {
		return fmt.Errorf("afscp idempotency key is required")
	}

	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}

	u := *c.baseURL
	u.Path = joinURLPath(c.baseURL.Path, requestPath)
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return err
	}
	req.Header.Set(HeaderAuthorization, "Bearer "+c.token)
	req.Header.Set(HeaderCorrelationID, correlationID)
	req.Header.Set(HeaderCallerService, c.callerService)
	req.Header.Set(HeaderNamespaceID, namespaceID)
	if method != http.MethodGet {
		req.Header.Set(HeaderIdempotencyKey, idempotencyKey)
		req.Header.Set(HeaderActorType, c.actorType)
		req.Header.Set(HeaderActorID, c.actorID)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("afscp request failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode afscp response: %w", err)
	}
	return nil
}

func joinURLPath(basePath, requestPath string) string {
	if strings.TrimSpace(basePath) == "" || basePath == "/" {
		return requestPath
	}
	return path.Join(basePath, requestPath)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
