package workload

import (
	"time"

	"github.com/agentsmith-project/agentsmith-sandbox-control-plane/internal/workspacebinding"
)

// CreateRequest is the request body for PUT /v1/workspaces/{wsId}/projects/{projId}/workloads/{wlId}.
// workspace_id and project_id come from URL path, not from body.
type CreateRequest struct {
	Image              string            `json:"image"`
	Command            []string          `json:"command,omitempty"`
	Env                map[string]string `json:"env,omitempty"`
	CPURequest         string            `json:"cpu_request,omitempty"`
	CPULimit           string            `json:"cpu_limit,omitempty"`
	MemoryRequest      string            `json:"memory_request,omitempty"`
	MemoryLimit        string            `json:"memory_limit,omitempty"`
	IdleTimeoutSec     int               `json:"idle_timeout_sec,omitempty"`
	MaxLifetimeSec     int               `json:"max_lifetime_sec,omitempty"`
	WorkspaceBindingID string            `json:"workspace_binding_id,omitempty"`

	resolvedMount *workspacebinding.ResolvedMount
}

// ExecRequest is the request body for POST .../exec.
type ExecRequest struct {
	Cmd            []string `json:"cmd"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
}

// ExecResponse is the response for POST .../exec.
type ExecResponse struct {
	ExitCode   int    `json:"exit_code"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	DurationMs int64  `json:"duration_ms"`
}

// PodStatus is the response for GET .../workloads/{id}.
type PodStatus struct {
	PodName        string `json:"pod_name,omitempty"`
	Phase          string `json:"phase"`
	IP             string `json:"ip,omitempty"`
	StartedAt      string `json:"started_at,omitempty"`
	LastActivityAt string `json:"last_activity_at,omitempty"`
	ExpiresAt      string `json:"expires_at,omitempty"`
	Message        string `json:"message,omitempty"`
}

// DeleteResponse is the response for DELETE .../workloads/{id}.
type DeleteResponse struct {
	Message string `json:"message"`
}

// KeepaliveResponse is the response for POST .../keepalive.
// Client must send keepalive periodically; expired workloads must be released through the workload delete API.
type KeepaliveResponse struct {
	ExpiresAt string `json:"expires_at"`
}

const (
	DefaultIdleTimeout = 30 * time.Minute
	DefaultMaxLifetime = 24 * time.Hour
	WorkloadLabel      = "managed-workload"
	maxExecTimeout     = 300 * time.Second
	workloadRunAsUID   = 1000
)
