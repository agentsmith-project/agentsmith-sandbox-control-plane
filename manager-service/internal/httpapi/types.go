package httpapi

// CreateSandboxRequest represents a request to create a sandbox
type CreateSandboxRequest struct {
	TTLSeconds            int               `json:"ttlSeconds,omitempty"`
	Image                 string            `json:"image,omitempty"`
	CPULimit              string            `json:"cpuLimit,omitempty"`
	MemoryLimit           string            `json:"memoryLimit,omitempty"`
	EphemeralStorageLimit string            `json:"ephemeralStorageLimit,omitempty"`
	ContainerName         string            `json:"containerName,omitempty"`
	Workdir               string            `json:"workdir,omitempty"`
	Env                   map[string]string `json:"env,omitempty"`
}

// CreateSandboxResponse represents a response from creating a sandbox
type CreateSandboxResponse struct {
	PodName   string `json:"podName"`
	ExpiresAt string `json:"expiresAt"`
}

// ExecRequest represents a request to execute a command
type ExecRequest struct {
	Cmd            []string          `json:"cmd"`
	Workdir        string            `json:"workdir,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	TimeoutSeconds int               `json:"timeoutSeconds,omitempty"`
}

// ExecResponse represents a response from executing a command (non-streaming fallback)
type ExecResponse struct {
	ExitCode   int    `json:"exitCode"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	DurationMs int64  `json:"durationMs"`
}

// HealthResponse represents a health check response
type HealthResponse struct {
	Status string `json:"status"`
	Time   string `json:"time"`
}

// ReadinessResponse represents a readiness check response
type ReadinessResponse struct {
	Ready        bool   `json:"ready"`
	ConfigLoaded bool   `json:"configLoaded"`
	K8sConnected bool   `json:"k8sConnected"`
	ConfigHash   string `json:"configHash,omitempty"`
	Message      string `json:"message,omitempty"`
}
