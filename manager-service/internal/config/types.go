package config

import (
	"fmt"
	"time"
)

// Config is the main configuration structure
type Config struct {
	Version    int           `yaml:"version"`
	Server     ServerConfig  `yaml:"server"`
	Auth       AuthConfig    `yaml:"auth"`
	Kubernetes K8sConfig     `yaml:"kubernetes"`
	Sandbox    SandboxConfig `yaml:"sandbox"`
	Exec       ExecConfig    `yaml:"exec"`
	Files      FilesConfig   `yaml:"files"`
	Storage    StorageConfig `yaml:"storage"`
	Buffer     BufferConfig  `yaml:"buffer"`
}

// ServerConfig contains HTTP server configuration
type ServerConfig struct {
	HTTPPort        int            `yaml:"httpPort"`
	RequestIDHeader string         `yaml:"requestIdHeader"`
	Timeouts        ServerTimeouts `yaml:"timeouts"`
	MaxHeaderBytes  int            `yaml:"maxHeaderBytes"`
	Metrics         MetricsConfig  `yaml:"metrics"`
	Debug           DebugConfig    `yaml:"debug"`
}

// ServerTimeouts contains server timeout settings
type ServerTimeouts struct {
	ReadHeader time.Duration `yaml:"readHeader"`
	Read       time.Duration `yaml:"read"`
	Write      time.Duration `yaml:"write"`
	Idle       time.Duration `yaml:"idle"`
}

// MetricsConfig contains metrics endpoint configuration
type MetricsConfig struct {
	Enabled           bool   `yaml:"enabled"`
	Path              string `yaml:"path"`
	RequireServiceKey bool   `yaml:"requireServiceKey"`
}

// DebugConfig contains debug endpoint configuration
type DebugConfig struct {
	ConfigPath  string `yaml:"configPath"`
	EnablePprof bool   `yaml:"enablePprof"`
}

// AuthConfig contains authentication configuration
type AuthConfig struct {
	Enabled             bool   `yaml:"enabled"`
	HeaderName          string `yaml:"headerName"`
	AcceptAuthorization bool   `yaml:"acceptAuthorization"`
	AuthorizationScheme string `yaml:"authorizationScheme"`
	FailStatusCode      int    `yaml:"failStatusCode"`
}

// K8sConfig contains Kubernetes client configuration
type K8sConfig struct {
	QPS            int            `yaml:"qps"`
	Burst          int            `yaml:"burst"`
	RequestTimeout time.Duration  `yaml:"requestTimeout"`
	Retry          K8sRetryConfig `yaml:"retry"`
}

// K8sRetryConfig contains retry settings for K8s API calls
type K8sRetryConfig struct {
	Enabled     bool          `yaml:"enabled"`
	MaxAttempts int           `yaml:"maxAttempts"`
	BaseBackoff time.Duration `yaml:"baseBackoff"`
	MaxBackoff  time.Duration `yaml:"maxBackoff"`
}

// SandboxConfig contains sandbox defaults
type SandboxConfig struct {
	Defaults SandboxDefaults `yaml:"defaults"`
}

// SandboxDefaults contains default values for sandbox creation
type SandboxDefaults struct {
	Namespace               string               `yaml:"namespace"`
	RunnerImage             string               `yaml:"runnerImage"`
	ImagePullPolicy         string               `yaml:"imagePullPolicy"`
	ImagePullSecrets        []string             `yaml:"imagePullSecrets"`
	TTLSeconds              int                  `yaml:"ttlSeconds"`
	PodReadyWait            time.Duration        `yaml:"podReadyWait"`
	PodPollInterval         time.Duration        `yaml:"podPollInterval"`
	TerminationGraceSeconds int64                `yaml:"terminationGraceSeconds"`
	ActiveDeadlineSeconds   int64                `yaml:"activeDeadlineSeconds"`
	ContainerName           string               `yaml:"containerName"`
	Workdir                 string               `yaml:"workdir"`
	Volumes                 map[string]Volume    `yaml:"volumes"`
	Resources               ResourceRequirements `yaml:"resources"`
	Labels                  map[string]string    `yaml:"labels"`
	Annotations             map[string]string    `yaml:"annotations"`
}

// Volume represents a volume definition
type Volume struct {
	Name      string `yaml:"name"`
	MountPath string `yaml:"mountPath"`
	SizeLimit string `yaml:"sizeLimit"`
}

// ResourceRequirements contains CPU, memory, and ephemeral storage limits
type ResourceRequirements struct {
	Requests ResourceList `yaml:"requests"`
	Limits   ResourceList `yaml:"limits"`
}

// ResourceList contains resource quantities
type ResourceList struct {
	CPU              string `yaml:"cpu"`
	Memory           string `yaml:"memory"`
	EphemeralStorage string `yaml:"ephemeralStorage,omitempty"`
}

// ExecConfig contains execution configuration
type ExecConfig struct {
	DefaultTimeout    time.Duration  `yaml:"defaultTimeout"`
	MaxTimeout        time.Duration  `yaml:"maxTimeout"`
	StdoutMaxBytes    int64          `yaml:"stdoutMaxBytes"`
	StderrMaxBytes    int64          `yaml:"stderrMaxBytes"`
	PreserveTailBytes int64          `yaml:"preserveTailBytes"`
	ExitCodeMarker    ExitCodeMarker `yaml:"exitCodeMarker"`
	Shell             ShellConfig    `yaml:"shell"`
	Env               EnvConfig      `yaml:"env"`
	Workdir           WorkdirConfig  `yaml:"workdir"`
}

// ExitCodeMarker defines how exit codes are marked
type ExitCodeMarker struct {
	Key    string `yaml:"key"`
	Stream string `yaml:"stream"`
}

// ShellConfig contains shell execution settings
type ShellConfig struct {
	Bin  string   `yaml:"bin"`
	Args []string `yaml:"args"`
}

// EnvConfig contains environment variable validation
type EnvConfig struct {
	AllowRegex string `yaml:"allowRegex"`
}

// WorkdirConfig contains workdir validation settings
type WorkdirConfig struct {
	AllowedPrefixes []string `yaml:"allowedPrefixes"`
}

// FilesConfig contains file operation configuration
type FilesConfig struct {
	RootPrefix string             `yaml:"rootPrefix"`
	Upload     FileUploadConfig   `yaml:"upload"`
	Download   FileDownloadConfig `yaml:"download"`
	Tar        TarConfig          `yaml:"tar"`
}

// FileUploadConfig contains upload settings
type FileUploadConfig struct {
	DefaultDest string `yaml:"defaultDest"`
	MaxBytes    int64  `yaml:"maxBytes"`
	Format      string `yaml:"format"`
}

// FileDownloadConfig contains download settings
type FileDownloadConfig struct {
	DefaultSrc string `yaml:"defaultSrc"`
	Format     string `yaml:"format"`
}

// TarConfig contains tar command settings
type TarConfig struct {
	Bin            string `yaml:"bin"`
	RejectSymlinks bool   `yaml:"rejectSymlinks"`
}

// StorageConfig contains object storage configuration
type StorageConfig struct {
	Endpoint  string `yaml:"endpoint"`
	AccessKey string `yaml:"accessKey"`
	SecretKey string `yaml:"secretKey"`
	Bucket    string `yaml:"bucket"`
	UseSSL    bool   `yaml:"useSSL"`
}

// BufferConfig contains buffer capacity settings
type BufferConfig struct {
	Capacity int `yaml:"capacity"`
}

// ConfigMeta contains metadata about loaded configuration
type ConfigMeta struct {
	SchemaVersion int          `yaml:"schemaVersion"`
	SourcePath    string       `yaml:"sourcePath"`
	CurrentHash   string       `yaml:"currentHash"`
	LoadedAt      time.Time    `yaml:"loadedAt"`
	ReloadCount   int          `yaml:"reloadCount"`
	LastError     *ConfigError `yaml:"lastError,omitempty"`
}

// ConfigError represents a configuration loading error
type ConfigError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	FieldPath string `json:"fieldPath,omitempty"`
	RuleID    string `json:"ruleId,omitempty"`
	Rule      string `json:"rule,omitempty"`
	Timestamp string `json:"timestamp"`
}

// Error implements the error interface
func (e *ConfigError) Error() string {
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// DefaultConfig returns the default configuration
func DefaultConfig() *Config {
	return &Config{
		Version: 1,
		Server: ServerConfig{
			HTTPPort:        8080,
			RequestIDHeader: "X-Request-Id",
			Timeouts: ServerTimeouts{
				ReadHeader: 5 * time.Second,
				Read:       30 * time.Second,
				Write:      60 * time.Second,
				Idle:       120 * time.Second,
			},
			MaxHeaderBytes: 1 << 20, // 1MB
			Metrics: MetricsConfig{
				Enabled:           true,
				Path:              "/metrics",
				RequireServiceKey: false,
			},
			Debug: DebugConfig{
				ConfigPath:  "/debug/config",
				EnablePprof: false,
			},
		},
		Auth: AuthConfig{
			Enabled:             true,
			HeaderName:          "X-Service-Key",
			AcceptAuthorization: true,
			AuthorizationScheme: "ServiceKey",
			FailStatusCode:      401,
		},
		Kubernetes: K8sConfig{
			QPS:            50,
			Burst:          100,
			RequestTimeout: 15 * time.Second,
			Retry: K8sRetryConfig{
				Enabled:     true,
				MaxAttempts: 3,
				BaseBackoff: 200 * time.Millisecond,
				MaxBackoff:  2 * time.Second,
			},
		},
		Sandbox: SandboxConfig{
			Defaults: SandboxDefaults{
				Namespace:               "sandbox",
				RunnerImage:             "sandbox-runner:1.0.0",
				ImagePullPolicy:         "IfNotPresent",
				ImagePullSecrets:        nil,
				TTLSeconds:              900,
				PodReadyWait:            5 * time.Minute,
				PodPollInterval:         500 * time.Millisecond,
				TerminationGraceSeconds: 1,
				ActiveDeadlineSeconds:   0,
				ContainerName:           "runner",
				Workdir:                 "/workspace",
				Volumes: map[string]Volume{
					"workspace": {
						Name:      "workspace",
						MountPath: "/workspace",
						SizeLimit: "0",
					},
					"tmp": {
						Name:      "tmp",
						MountPath: "/tmp",
						SizeLimit: "256Mi",
					},
				},
				Resources: ResourceRequirements{
					Requests: ResourceList{
						CPU:    "100m",
						Memory: "256Mi",
					},
					Limits: ResourceList{
						CPU:              "1",
						Memory:           "1Gi",
						EphemeralStorage: "2Gi",
					},
				},
				Labels: map[string]string{
					"app": "llm-sandbox",
				},
				Annotations: make(map[string]string),
			},
		},
		Exec: ExecConfig{
			DefaultTimeout:    30 * time.Second,
			MaxTimeout:        300 * time.Second,
			StdoutMaxBytes:    1 << 20, // 1MB
			StderrMaxBytes:    1 << 20, // 1MB
			PreserveTailBytes: 4096,
			ExitCodeMarker: ExitCodeMarker{
				Key:    "__SBX_EXIT_CODE__",
				Stream: "stderr",
			},
			Shell: ShellConfig{
				Bin:  "sh",
				Args: []string{"-lc"},
			},
			Env: EnvConfig{
				AllowRegex: "^[A-Z_][A-Z0-9_]*$",
			},
			Workdir: WorkdirConfig{
				AllowedPrefixes: []string{"/workspace"},
			},
		},
		Files: FilesConfig{
			RootPrefix: "/workspace",
			Upload: FileUploadConfig{
				DefaultDest: "/workspace",
				MaxBytes:    50 << 20, // 50MB
				Format:      "tar.gz",
			},
			Download: FileDownloadConfig{
				DefaultSrc: "/workspace",
				Format:     "tar.gz",
			},
			Tar: TarConfig{
				Bin:            "tar",
				RejectSymlinks: true,
			},
		},
		Storage: StorageConfig{
			Endpoint:  "s3.amazonaws.com",
			AccessKey: "",
			SecretKey: "",
			Bucket:    "sandbox-storage",
			UseSSL:    true,
		},
		Buffer: BufferConfig{
			Capacity: 10000,
		},
	}
}
