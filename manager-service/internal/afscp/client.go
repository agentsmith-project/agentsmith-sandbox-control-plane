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
	baseURL               *url.URL
	httpClient            *http.Client
	token                 string
	callerService         string
	actorType             string
	actorID               string
	operationWaitTimeout  time.Duration
	operationPollInterval time.Duration
}

type ClientConfig struct {
	BaseURL               string
	Token                 string
	CallerService         string
	ActorType             string
	ActorID               string
	HTTPClient            *http.Client
	OperationWaitTimeout  time.Duration
	OperationPollInterval time.Duration
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

type PendingOperationError struct {
	OperationID    string
	OperationState string
	RequestID      string
	Code           string
	StatusCode     int
	Retryable      bool
}

func (e *PendingOperationError) Error() string {
	operationID := strings.TrimSpace(e.OperationID)
	if operationID == "" {
		operationID = "unknown"
	}
	state := strings.TrimSpace(e.OperationState)
	if state == "" {
		state = "unknown"
	}
	return fmt.Sprintf("afscp operation %s is still pending: last_state=%s", operationID, state)
}

type DependencyError struct {
	StatusCode     int
	Code           string
	Message        string
	RequestID      string
	OperationID    string
	OperationState string
}

func (e *DependencyError) Error() string {
	if e == nil {
		return "afscp request failed"
	}
	parts := []string{fmt.Sprintf("afscp request failed: status=%d", e.StatusCode)}
	if code := strings.TrimSpace(e.Code); code != "" {
		parts = append(parts, "code="+code)
	}
	if operationID := strings.TrimSpace(e.OperationID); operationID != "" {
		parts = append(parts, "operation_id="+operationID)
	}
	if state := strings.TrimSpace(e.OperationState); state != "" {
		parts = append(parts, "operation_state="+state)
	}
	if requestID := strings.TrimSpace(e.RequestID); requestID != "" {
		parts = append(parts, "request_id="+requestID)
	}
	return strings.Join(parts, " ")
}

const (
	defaultOperationWaitTimeout  = 30 * time.Second
	defaultOperationPollInterval = 250 * time.Millisecond
)

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
	callerService := firstNonEmpty(config.CallerService, "agentsmith-sandbox-control-plane")
	actorType := firstNonEmpty(config.ActorType, "system")
	actorID := firstNonEmpty(config.ActorID, callerService)
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	waitTimeout := config.OperationWaitTimeout
	if waitTimeout <= 0 {
		waitTimeout = defaultOperationWaitTimeout
	}
	pollInterval := config.OperationPollInterval
	if pollInterval <= 0 {
		pollInterval = defaultOperationPollInterval
	}
	return &Client{
		baseURL:               baseURL,
		httpClient:            httpClient,
		token:                 token,
		callerService:         callerService,
		actorType:             actorType,
		actorID:               actorID,
		operationWaitTimeout:  waitTimeout,
		operationPollInterval: pollInterval,
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
	return c.confirmedEmptyMutation(ctx, fmt.Sprintf("/internal/v1/workload-mount-bindings/%s:release", url.PathEscape(mountBindingID)), namespaceID, correlationID, idempotencyKey)
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
	return c.confirmedMutation(ctx, http.MethodPatch, fmt.Sprintf("/internal/v1/workload-mount-bindings/%s/status", url.PathEscape(mountBindingID)), namespaceID, correlationID, idempotencyKey, body, &envelope)
}

func (c *Client) emptyMutation(ctx context.Context, path string, namespaceID, correlationID, idempotencyKey string) (OperationEnvelope, error) {
	var envelope OperationEnvelope
	err := c.doJSON(ctx, http.MethodPost, path, namespaceID, correlationID, idempotencyKey, nil, &envelope)
	if err != nil {
		return OperationEnvelope{}, err
	}
	return envelope, nil
}

func (c *Client) confirmedEmptyMutation(ctx context.Context, path string, namespaceID, correlationID, idempotencyKey string) (OperationEnvelope, error) {
	return c.confirmedMutation(ctx, http.MethodPost, path, namespaceID, correlationID, idempotencyKey, nil, nil)
}

func (c *Client) confirmedMutation(ctx context.Context, method, path string, namespaceID, correlationID, idempotencyKey string, body any, out *OperationEnvelope) (OperationEnvelope, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	waitCtx, cancel := context.WithTimeout(ctx, c.operationWaitTimeout)
	defer cancel()

	var last OperationEnvelope
	for {
		var envelope OperationEnvelope
		if out != nil {
			envelope = *out
		}
		if err := c.doJSON(waitCtx, method, path, namespaceID, correlationID, idempotencyKey, body, &envelope); err != nil {
			if waitCtx.Err() != nil && operationPending(last.OperationState) {
				return OperationEnvelope{}, pendingOperationTimeoutError(last)
			}
			return OperationEnvelope{}, err
		}
		last = envelope
		if operationSucceeded(envelope.OperationState) {
			if out != nil {
				*out = envelope
			}
			return envelope, nil
		}
		if operationFailed(envelope.OperationState) {
			return OperationEnvelope{}, fmt.Errorf("afscp operation %s ended in %s", envelope.OperationID, envelope.OperationState)
		}
		if !operationPending(envelope.OperationState) {
			return OperationEnvelope{}, fmt.Errorf("afscp operation %s returned unknown state %q", envelope.OperationID, envelope.OperationState)
		}

		timer := time.NewTimer(c.operationPollInterval)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			return OperationEnvelope{}, pendingOperationTimeoutError(last)
		case <-timer.C:
		}
	}
}

func pendingOperationTimeoutError(envelope OperationEnvelope) *PendingOperationError {
	return &PendingOperationError{
		OperationID:    envelope.OperationID,
		OperationState: envelope.OperationState,
		Retryable:      true,
	}
}

func operationSucceeded(state string) bool {
	return strings.ToLower(strings.TrimSpace(state)) == "succeeded"
}

func operationPending(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "queued", "pending", "running", "releasing", "cancel_requested":
		return true
	default:
		return false
	}
}

func operationFailed(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "failed", "cancelled", "operator_intervention_required":
		return true
	default:
		return false
	}
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
		return afscpErrorFromResponse(resp.StatusCode, data)
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

type afscpErrorEnvelope struct {
	Code           string                     `json:"code"`
	Message        string                     `json:"message"`
	RequestID      string                     `json:"request_id"`
	OperationID    string                     `json:"operation_id"`
	OperationState string                     `json:"operation_state"`
	State          string                     `json:"state"`
	Status         string                     `json:"status"`
	Operation      afscpOperationFields       `json:"operation"`
	Error          afscpErrorFields           `json:"error"`
	Details        map[string]json.RawMessage `json:"details"`
}

type afscpErrorFields struct {
	Code           string                     `json:"code"`
	Message        string                     `json:"message"`
	RequestID      string                     `json:"request_id"`
	OperationID    string                     `json:"operation_id"`
	OperationState string                     `json:"operation_state"`
	State          string                     `json:"state"`
	Status         string                     `json:"status"`
	Reason         string                     `json:"reason"`
	Operation      afscpOperationFields       `json:"operation"`
	Details        map[string]json.RawMessage `json:"details"`
}

type afscpOperationFields struct {
	ID             string `json:"id"`
	OperationID    string `json:"operation_id"`
	State          string `json:"state"`
	OperationState string `json:"operation_state"`
	Status         string `json:"status"`
}

func afscpErrorFromResponse(statusCode int, data []byte) error {
	var envelope afscpErrorEnvelope
	if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &envelope); err != nil {
			return fmt.Errorf("afscp request failed: status=%d", statusCode)
		}
	}

	code := firstNonEmpty(envelope.Error.Code, envelope.Code)
	message := firstNonEmpty(envelope.Error.Message, envelope.Message)
	requestID := firstNonEmpty(envelope.Error.RequestID, envelope.RequestID, detailString(envelope.Error.Details, "request_id"), detailString(envelope.Details, "request_id"))
	operationID := firstNonEmpty(
		envelope.Error.OperationID,
		envelope.Error.Operation.ID,
		envelope.Error.Operation.OperationID,
		envelope.OperationID,
		envelope.Operation.ID,
		envelope.Operation.OperationID,
		detailString(envelope.Error.Details, "operation_id", "dependency_operation_id"),
		nestedDetailString(envelope.Error.Details, "operation", "id", "operation_id"),
		detailString(envelope.Details, "operation_id", "dependency_operation_id"),
		nestedDetailString(envelope.Details, "operation", "id", "operation_id"),
	)
	operationState := firstNonEmpty(
		envelope.Error.OperationState,
		envelope.Error.State,
		envelope.Error.Status,
		envelope.Error.Operation.OperationState,
		envelope.Error.Operation.State,
		envelope.Error.Operation.Status,
		envelope.OperationState,
		envelope.State,
		envelope.Status,
		envelope.Operation.OperationState,
		envelope.Operation.State,
		envelope.Operation.Status,
		detailString(envelope.Error.Details, "operation_state", "dependency_state", "state", "status", "phase"),
		nestedDetailString(envelope.Error.Details, "operation", "operation_state", "state", "status"),
		detailString(envelope.Details, "operation_state", "dependency_state", "state", "status", "phase"),
		nestedDetailString(envelope.Details, "operation", "operation_state", "state", "status"),
	)
	reason := firstNonEmpty(
		envelope.Error.Reason,
		detailString(envelope.Error.Details, "reason", "stable_code"),
		detailString(envelope.Details, "reason", "stable_code"),
		code,
	)

	if operationFailed(operationState) {
		return &DependencyError{
			StatusCode:     statusCode,
			Code:           code,
			Message:        message,
			RequestID:      requestID,
			OperationID:    operationID,
			OperationState: operationState,
		}
	}

	if afscpDependencyPending(code, operationState, reason) {
		if strings.TrimSpace(operationState) == "" {
			operationState = inferredPendingState(code, reason)
		}
		return &PendingOperationError{
			OperationID:    operationID,
			OperationState: operationState,
			RequestID:      requestID,
			Code:           code,
			StatusCode:     statusCode,
			Retryable:      true,
		}
	}

	return &DependencyError{
		StatusCode:     statusCode,
		Code:           code,
		Message:        message,
		RequestID:      requestID,
		OperationID:    operationID,
		OperationState: operationState,
	}
}

func afscpDependencyPending(values ...string) bool {
	for _, value := range values {
		normalized := normalizeDependencyState(value)
		if normalized == "" {
			continue
		}
		if operationPending(normalized) {
			return true
		}
		switch normalized {
		case "EXPORT_NOT_READY", "NOT_READY", "OPERATION_NOT_READY", "OPERATION_PENDING", "RELEASE_PENDING", "RELEASE_INCOMPLETE", "IN_PROGRESS":
			return true
		}
		if strings.Contains(normalized, "PENDING") || strings.Contains(normalized, "RELEASING") || strings.Contains(normalized, "NOT_READY") || strings.Contains(normalized, "IN_PROGRESS") {
			return true
		}
	}
	return false
}

func inferredPendingState(values ...string) string {
	for _, value := range values {
		normalized := normalizeDependencyState(value)
		if strings.Contains(normalized, "RELEASING") {
			return "releasing"
		}
		if strings.Contains(normalized, "RUNNING") || strings.Contains(normalized, "IN_PROGRESS") {
			return "running"
		}
		if strings.Contains(normalized, "QUEUED") {
			return "queued"
		}
	}
	return "pending"
}

func normalizeDependencyState(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func detailString(details map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		raw, ok := details[key]
		if !ok {
			continue
		}
		if value := rawJSONScalarString(raw); value != "" {
			return value
		}
	}
	return ""
}

func nestedDetailString(details map[string]json.RawMessage, parent string, keys ...string) string {
	raw, ok := details[parent]
	if !ok {
		return ""
	}
	var nested map[string]json.RawMessage
	if err := json.Unmarshal(raw, &nested); err != nil {
		return ""
	}
	return detailString(nested, keys...)
}

func rawJSONScalarString(raw json.RawMessage) string {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		return strings.TrimSpace(number.String())
	}
	return ""
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
