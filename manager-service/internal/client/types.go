package client

import "time"

// SecurityConfig contains security-related configuration for a session.
type SecurityConfig struct {
	AllowNetworkAccess  bool   `json:"allow_network_access"`
	ReadonlyFilesystem  bool   `json:"readonly_filesystem"`
	CPULimit            string `json:"cpu_limit,omitempty"`
	MemoryLimit         string `json:"memory_limit,omitempty"`
	IdleTimeout         string `json:"idle_timeout,omitempty"`
	MaxLifetime         string `json:"max_lifetime,omitempty"`
	DropAllCapabilities bool   `json:"drop_all_capabilities"`
	AllowPrivileged     bool   `json:"allow_privileged"`
}

// CreateSessionRequest represents a request to create a new session.
type CreateSessionRequest struct {
	AgentThreadID string            `json:"agent_thread_id"`
	Image         string            `json:"image"`
	Command       []string          `json:"command,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Config        SecurityConfig    `json:"config"`
}

// CreateSessionResponse represents the response from creating a session.
// The server sends a status message with state="ready" when session is ready.
type CreateSessionResponse struct {
	Type string `json:"type"`
	Data StatusPayload `json:"data"`
}

// StatusPayload represents the status data sent by the server.
type StatusPayload struct {
	State    string  `json:"state"`
	Message  string  `json:"message,omitempty"`
	Progress float64 `json:"progress,omitempty"`
}

// SessionStatus represents the current status of a session.
type SessionStatus struct {
	SessionID   string    `json:"sessionId"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"createdAt"`
	ExpiresAt   time.Time `json:"expiresAt,omitempty"`
	RunnerPodIP string    `json:"runnerPodIP,omitempty"`
}

// ExecRequest represents a command execution request.
type ExecRequest struct {
	Cmd string `json:"cmd"`
}

// ExecResponse represents the response from command execution.
type ExecResponse struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
}

// FileUploadRequest represents a file upload request.
type FileUploadRequest struct {
	SessionID string `json:"sessionId"`
	Path      string `json:"path"`
	Content   []byte `json:"content"`
}

// FileDownloadRequest represents a file download request.
type FileDownloadRequest struct {
	SessionID string `json:"sessionId"`
	Path      string `json:"path"`
}
