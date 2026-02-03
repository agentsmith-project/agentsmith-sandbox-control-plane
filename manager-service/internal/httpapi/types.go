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

// ExecResponse represents a response from executing a command
type ExecResponse struct {
	ExitCode   int    `json:"exitCode"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	DurationMs int64  `json:"durationMs"`
}

// TouchResponse represents a response from touching a sandbox
type TouchResponse struct {
	ExpiresAt string `json:"expiresAt"`
}

// DeleteResponse represents a response from deleting a sandbox
type DeleteResponse struct {
	Message string `json:"message"`
}

// UploadQueryParams represents query parameters for file upload
type UploadQueryParams struct {
	Dest string `schema:"dest"`
}

// DownloadQueryParams represents query parameters for file download
type DownloadQueryParams struct {
	Src string `schema:"src"`
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

// DebugConfigResponse represents the debug config endpoint response
type DebugConfigResponse struct {
	Meta   DebugConfigMeta   `json:"meta"`
	Config DebugConfigConfig `json:"config"`
	Boot   DebugConfigBoot   `json:"boot,omitempty"`
}

// DebugConfigMeta represents metadata about the current configuration
type DebugConfigMeta struct {
	SchemaVersion int          `json:"schemaVersion"`
	SourcePath    string       `json:"sourcePath"`
	CurrentHash   string       `json:"currentHash"`
	LoadedAt      string       `json:"loadedAt"`
	ReloadCount   int          `json:"reloadCount"`
	LastError     *ConfigError `json:"lastError,omitempty"`
}

// ConfigError represents a configuration error
type ConfigError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	FieldPath string `json:"fieldPath,omitempty"`
	RuleID    string `json:"ruleId,omitempty"`
	Rule      string `json:"rule,omitempty"`
	Timestamp string `json:"timestamp"`
}

// DebugConfigConfig represents the sanitized configuration
type DebugConfigConfig struct {
	Version    int                `json:"version"`
	Server     DebugServerConfig  `json:"server"`
	Auth       DebugAuthConfig    `json:"auth"`
	Kubernetes DebugK8sConfig     `json:"kubernetes"`
	Sandbox    DebugSandboxConfig `json:"sandbox"`
	Exec       DebugExecConfig    `json:"exec"`
	Files      DebugFilesConfig   `json:"files"`
}

// DebugServerConfig represents the debug server configuration
type DebugServerConfig struct {
	HTTPPort        int                `json:"httpPort"`
	RequestIDHeader string             `json:"requestIdHeader"`
	Timeouts        map[string]string  `json:"timeouts"`
	MaxHeaderBytes  int                `json:"maxHeaderBytes"`
	Metrics         DebugMetricsConfig `json:"metrics"`
	Debug           DebugDebugConfig   `json:"debug"`
}

// DebugMetricsConfig represents the debug metrics configuration
type DebugMetricsConfig struct {
	Enabled           bool   `json:"enabled"`
	Path              string `json:"path"`
	RequireServiceKey bool   `json:"requireServiceKey"`
}

// DebugDebugConfig represents the debug endpoint configuration
type DebugDebugConfig struct {
	ConfigPath  string `json:"configPath"`
	EnablePprof bool   `json:"enablePprof"`
}

// DebugAuthConfig represents the debug auth configuration
type DebugAuthConfig struct {
	Enabled             bool   `json:"enabled"`
	HeaderName          string `json:"headerName"`
	AcceptAuthorization bool   `json:"acceptAuthorization"`
	AuthorizationScheme string `json:"authorizationScheme"`
	FailStatusCode      int    `json:"failStatusCode"`
}

// DebugK8sConfig represents the debug Kubernetes configuration
type DebugK8sConfig struct {
	QPS            int                 `json:"qps"`
	Burst          int                 `json:"burst"`
	RequestTimeout string              `json:"requestTimeout"`
	Retry          DebugK8sRetryConfig `json:"retry"`
}

// DebugK8sRetryConfig represents the debug Kubernetes retry configuration
type DebugK8sRetryConfig struct {
	Enabled     bool   `json:"enabled"`
	MaxAttempts int    `json:"maxAttempts"`
	BaseBackoff string `json:"baseBackoff"`
	MaxBackoff  string `json:"maxBackoff"`
}

// DebugSandboxConfig represents the debug sandbox configuration
type DebugSandboxConfig struct {
	Defaults DebugSandboxDefaults `json:"defaults"`
}

// DebugSandboxDefaults represents the debug sandbox defaults
type DebugSandboxDefaults struct {
	Namespace               string                 `json:"namespace"`
	RunnerImage             string                 `json:"runnerImage"`
	ImagePullPolicy         string                 `json:"imagePullPolicy"`
	TTLSeconds              int                    `json:"ttlSeconds"`
	PodReadyWait            string                 `json:"podReadyWait"`
	PodPollInterval         string                 `json:"podPollInterval"`
	TerminationGraceSeconds int64                  `json:"terminationGraceSeconds"`
	ActiveDeadlineSeconds   int64                  `json:"activeDeadlineSeconds"`
	ContainerName           string                 `json:"containerName"`
	Workdir                 string                 `json:"workdir"`
	Volumes                 map[string]DebugVolume `json:"volumes"`
	Resources               DebugResources         `json:"resources"`
	Labels                  map[string]string      `json:"labels"`
}

// DebugVolume represents a debug volume
type DebugVolume struct {
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
	SizeLimit string `json:"sizeLimit"`
}

// DebugResources represents debug resource requirements
type DebugResources struct {
	Requests DebugResourceList `json:"requests"`
	Limits   DebugResourceList `json:"limits"`
}

// DebugResourceList represents debug resource quantities
type DebugResourceList struct {
	CPU              string `json:"cpu"`
	Memory           string `json:"memory"`
	EphemeralStorage string `json:"ephemeralStorage,omitempty"`
}

// DebugExecConfig represents the debug exec configuration
type DebugExecConfig struct {
	DefaultTimeout    string              `json:"defaultTimeout"`
	MaxTimeout        string              `json:"maxTimeout"`
	StdoutMaxBytes    int64               `json:"stdoutMaxBytes"`
	StderrMaxBytes    int64               `json:"stderrMaxBytes"`
	PreserveTailBytes int64               `json:"preserveTailBytes"`
	ExitCodeMarker    DebugExitCodeMarker `json:"exitCodeMarker"`
	Shell             DebugShellConfig    `json:"shell"`
	Env               DebugEnvConfig      `json:"env"`
	Workdir           DebugWorkdirConfig  `json:"workdir"`
}

// DebugExitCodeMarker represents the debug exit code marker
type DebugExitCodeMarker struct {
	Key    string `json:"key"`
	Stream string `json:"stream"`
}

// DebugShellConfig represents the debug shell configuration
type DebugShellConfig struct {
	Bin  string   `json:"bin"`
	Args []string `json:"args"`
}

// DebugEnvConfig represents the debug env configuration
type DebugEnvConfig struct {
	AllowRegex string `json:"allowRegex"`
}

// DebugWorkdirConfig represents the debug workdir configuration
type DebugWorkdirConfig struct {
	AllowedPrefixes []string `json:"allowedPrefixes"`
}

// DebugFilesConfig represents the debug files configuration
type DebugFilesConfig struct {
	RootPrefix string                  `json:"rootPrefix"`
	Upload     DebugFileUploadConfig   `json:"upload"`
	Download   DebugFileDownloadConfig `json:"download"`
	Tar        DebugTarConfig          `json:"tar"`
}

// DebugFileUploadConfig represents the debug upload configuration
type DebugFileUploadConfig struct {
	DefaultDest string `json:"defaultDest"`
	MaxBytes    int64  `json:"maxBytes"`
	Format      string `json:"format"`
}

// DebugFileDownloadConfig represents the debug download configuration
type DebugFileDownloadConfig struct {
	DefaultSrc string `json:"defaultSrc"`
	Format     string `json:"format"`
}

// DebugTarConfig represents the debug tar configuration
type DebugTarConfig struct {
	Bin            string `json:"bin"`
	RejectSymlinks bool   `json:"rejectSymlinks"`
}

// DebugConfigBoot represents the boot configuration
type DebugConfigBoot struct {
	ConfigPath       string `json:"configPath"`
	DebounceDuration string `json:"debounceDuration,omitempty"`
	MinInterval      string `json:"minInterval,omitempty"`
	MaxBackoff       string `json:"maxBackoff,omitempty"`
	StrictMode       bool   `json:"strictMode,omitempty"`
}

// SandboxListItem represents an item in a sandbox list (for future list endpoint)
type SandboxListItem struct {
	SessionID string `json:"sessionId"`
	PodName   string `json:"podName"`
	CreatedAt string `json:"createdAt"`
	ExpiresAt string `json:"expiresAt"`
	Phase     string `json:"phase"`
	Ready     bool   `json:"ready"`
}

// SandboxList represents a list of sandboxes
type SandboxList struct {
	Items []SandboxListItem `json:"items"`
	Total int               `json:"total"`
}
