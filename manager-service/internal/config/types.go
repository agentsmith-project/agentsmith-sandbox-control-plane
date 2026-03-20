package config

import (
	"fmt"
	"time"
)

type Config struct {
	Version    int             `yaml:"version"`
	Server     ServerConfig    `yaml:"server"`
	Auth       AuthConfig      `yaml:"auth"`
	Kubernetes K8sConfig       `yaml:"kubernetes"`
	Sandbox    SandboxConfig   `yaml:"sandbox"`
	RateLimit  RateLimitConfig `yaml:"rateLimit"`
}

type SandboxConfig struct {
	Defaults SandboxDefaultsConfig `yaml:"defaults"`
}

type SandboxDefaultsConfig struct {
	Namespace string `yaml:"namespace"`
}

type ServerConfig struct {
	HTTPPort        int            `yaml:"httpPort"`
	RequestIDHeader string         `yaml:"requestIdHeader"`
	Timeouts        ServerTimeouts `yaml:"timeouts"`
	MaxHeaderBytes  int            `yaml:"maxHeaderBytes"`
	Metrics         MetricsConfig  `yaml:"metrics"`
	Debug           DebugConfig    `yaml:"debug"`
}

type ServerTimeouts struct {
	ReadHeader time.Duration `yaml:"readHeader"`
	Read       time.Duration `yaml:"read"`
	Write      time.Duration `yaml:"write"`
	Idle       time.Duration `yaml:"idle"`
}

type MetricsConfig struct {
	Enabled bool   `yaml:"enabled"`
	Path    string `yaml:"path"`
}

type DebugConfig struct {
	ConfigPath  string `yaml:"configPath"`
	EnablePprof bool   `yaml:"enablePprof"`
}

type AuthConfig struct {
	HeaderName string `yaml:"headerName"`
}

type K8sConfig struct {
	QPS            int           `yaml:"qps"`
	Burst          int           `yaml:"burst"`
	RequestTimeout time.Duration `yaml:"requestTimeout"`
}

type RateLimitConfig struct {
	RequestsPerMinute int `yaml:"requestsPerMinute"`
}

type ConfigMeta struct {
	SchemaVersion int          `yaml:"schemaVersion"`
	SourcePath    string       `yaml:"sourcePath"`
	CurrentHash   string       `yaml:"currentHash"`
	LoadedAt      time.Time    `yaml:"loadedAt"`
	ReloadCount   int          `yaml:"reloadCount"`
	LastError     *ConfigError `yaml:"lastError,omitempty"`
}

type ConfigError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	FieldPath string `json:"fieldPath,omitempty"`
	RuleID    string `json:"ruleId,omitempty"`
	Rule      string `json:"rule,omitempty"`
	Timestamp string `json:"timestamp"`
}

func (e *ConfigError) Error() string {
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

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
			MaxHeaderBytes: 1 << 20,
			Metrics: MetricsConfig{
				Enabled: true,
				Path:    "/metrics",
			},
			Debug: DebugConfig{
				ConfigPath:  "/debug/config",
				EnablePprof: false,
			},
		},
		Auth: AuthConfig{
			HeaderName: "X-Service-Key",
		},
		Kubernetes: K8sConfig{
			QPS:            50,
			Burst:          100,
			RequestTimeout: 15 * time.Second,
		},
		Sandbox: SandboxConfig{
			Defaults: SandboxDefaultsConfig{
				Namespace: "sandbox-workloads",
			},
		},
		RateLimit: RateLimitConfig{
			RequestsPerMinute: 60,
		},
	}
}

func (c *Config) DeepCopy() *Config {
	copy := *c
	return &copy
}
