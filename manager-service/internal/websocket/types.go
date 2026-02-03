package websocket

import "encoding/json"

// Message types
const (
	TypeCreate = "create"
	TypeStdin  = "stdin"
	TypeStatus = "status"
	TypeStdout = "stdout"
	TypeStderr = "stderr"
	TypeExit   = "exit"
	TypeError  = "error"
)

// Message represents a WebSocket message
type Message struct {
	Type  string          `json:"type"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// CreatePayload is the payload for create message
type CreatePayload struct {
	AgentThreadID string            `json:"agent_thread_id"`
	Image         string            `json:"image"`
	Command       []string          `json:"command,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Config        SecurityConfig    `json:"config"`
}

// StdinPayload is the payload for stdin message
type StdinPayload struct {
	Data string `json:"data"` // base64 encoded
}

// StatusPayload is the payload for status message
type StatusPayload struct {
	State    string  `json:"state"` // creating, restoring, ready, error
	Message  string  `json:"message,omitempty"`
	Progress float64 `json:"progress,omitempty"` // 0.0-1.0
}

// OutputPayload is the payload for stdout/stderr message
type OutputPayload struct {
	Data string `json:"data"` // base64 encoded
}

// ExitPayload is the payload for exit message
type ExitPayload struct {
	Code int32 `json:"code"`
}

// ErrorPayload is the payload for error message
type ErrorPayload struct {
	Message string `json:"message"`
}

// SecurityConfig is the security configuration for sandbox
type SecurityConfig struct {
	AllowNetworkAccess  bool   `json:"allow_network_access"`
	ReadonlyFilesystem  bool   `json:"readonly_filesystem"`
	CPULimit            string `json:"cpu_limit,omitempty"`
	MemoryLimit         string `json:"memory_limit,omitempty"`
	IdleTimeout         string `json:"idle_timeout,omitempty"` // duration string
	MaxLifetime         string `json:"max_lifetime,omitempty"` // duration string
	DropAllCapabilities bool   `json:"drop_all_capabilities"`
	AllowPrivileged     bool   `json:"allow_privileged"`
}
