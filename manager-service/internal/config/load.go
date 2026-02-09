package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Load loads configuration from a YAML file
func Load(path string) (*Config, *ConfigMeta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, &ConfigError{
			Code:      "CONFIG_READ_FAILED",
			Message:   fmt.Sprintf("Failed to read config file: %v", err),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}
	}

	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, nil, &ConfigError{
			Code:      "CONFIG_PARSE_FAILED",
			Message:   fmt.Sprintf("Failed to parse config YAML: %v", err),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}
	}

	// Calculate hash of the config content
	hash := sha256.Sum256(data)
	hashStr := hex.EncodeToString(hash[:])

	meta := &ConfigMeta{
		SchemaVersion: cfg.Version,
		SourcePath:    path,
		CurrentHash:   hashStr,
		LoadedAt:      time.Now().UTC(),
		ReloadCount:   0,
		LastError:     nil,
	}

	return cfg, meta, nil
}

// MustLoad loads configuration or panics
func MustLoad(path string) (*Config, *ConfigMeta) {
	cfg, meta, err := Load(path)
	if err != nil {
		panic(err)
	}
	return cfg, meta
}

// LoadWithDefaults loads configuration and applies defaults for missing values
func LoadWithDefaults(path string) (*Config, *ConfigMeta, error) {
	cfg, meta, err := Load(path)
	if err != nil {
		return nil, nil, err
	}

	// Ensure defaults are applied for any zero values
	applyDefaults(cfg)

	return cfg, meta, nil
}

// applyDefaults ensures all zero values have defaults applied
func applyDefaults(cfg *Config) {
	defaultCfg := DefaultConfig()

	// Server defaults
	if cfg.Server.HTTPPort == 0 {
		cfg.Server.HTTPPort = defaultCfg.Server.HTTPPort
	}
	if cfg.Server.RequestIDHeader == "" {
		cfg.Server.RequestIDHeader = defaultCfg.Server.RequestIDHeader
	}
	if cfg.Server.Timeouts.ReadHeader == 0 {
		cfg.Server.Timeouts.ReadHeader = defaultCfg.Server.Timeouts.ReadHeader
	}
	if cfg.Server.Timeouts.Read == 0 {
		cfg.Server.Timeouts.Read = defaultCfg.Server.Timeouts.Read
	}
	if cfg.Server.Timeouts.Write == 0 {
		cfg.Server.Timeouts.Write = defaultCfg.Server.Timeouts.Write
	}
	if cfg.Server.Timeouts.Idle == 0 {
		cfg.Server.Timeouts.Idle = defaultCfg.Server.Timeouts.Idle
	}
	if cfg.Server.MaxHeaderBytes == 0 {
		cfg.Server.MaxHeaderBytes = defaultCfg.Server.MaxHeaderBytes
	}
	if cfg.Server.Metrics.Path == "" {
		cfg.Server.Metrics.Path = defaultCfg.Server.Metrics.Path
	}
	if cfg.Server.Debug.ConfigPath == "" {
		cfg.Server.Debug.ConfigPath = defaultCfg.Server.Debug.ConfigPath
	}

	// Auth defaults
	if cfg.Auth.HeaderName == "" {
		cfg.Auth.HeaderName = defaultCfg.Auth.HeaderName
	}
	if cfg.Auth.AuthorizationScheme == "" {
		cfg.Auth.AuthorizationScheme = defaultCfg.Auth.AuthorizationScheme
	}
	if cfg.Auth.FailStatusCode == 0 {
		cfg.Auth.FailStatusCode = defaultCfg.Auth.FailStatusCode
	}

	// Kubernetes defaults
	if cfg.Kubernetes.QPS == 0 {
		cfg.Kubernetes.QPS = defaultCfg.Kubernetes.QPS
	}
	if cfg.Kubernetes.Burst == 0 {
		cfg.Kubernetes.Burst = defaultCfg.Kubernetes.Burst
	}
	if cfg.Kubernetes.RequestTimeout == 0 {
		cfg.Kubernetes.RequestTimeout = defaultCfg.Kubernetes.RequestTimeout
	}

	// Sandbox defaults
	if cfg.Sandbox.Defaults.Namespace == "" {
		cfg.Sandbox.Defaults.Namespace = defaultCfg.Sandbox.Defaults.Namespace
	}
	if cfg.Sandbox.Defaults.RunnerImage == "" {
		cfg.Sandbox.Defaults.RunnerImage = defaultCfg.Sandbox.Defaults.RunnerImage
	}
	if cfg.Sandbox.Defaults.ImagePullPolicy == "" {
		cfg.Sandbox.Defaults.ImagePullPolicy = defaultCfg.Sandbox.Defaults.ImagePullPolicy
	}
	if cfg.Sandbox.Defaults.TTLSeconds == 0 {
		cfg.Sandbox.Defaults.TTLSeconds = defaultCfg.Sandbox.Defaults.TTLSeconds
	}
	if cfg.Sandbox.Defaults.PodReadyWait == 0 {
		cfg.Sandbox.Defaults.PodReadyWait = defaultCfg.Sandbox.Defaults.PodReadyWait
	}
	if cfg.Sandbox.Defaults.PodPollInterval == 0 {
		cfg.Sandbox.Defaults.PodPollInterval = defaultCfg.Sandbox.Defaults.PodPollInterval
	}
	if cfg.Sandbox.Defaults.ContainerName == "" {
		cfg.Sandbox.Defaults.ContainerName = defaultCfg.Sandbox.Defaults.ContainerName
	}
	if cfg.Sandbox.Defaults.Workdir == "" {
		cfg.Sandbox.Defaults.Workdir = defaultCfg.Sandbox.Defaults.Workdir
	}

	// Exec defaults
	if cfg.Exec.DefaultTimeout == 0 {
		cfg.Exec.DefaultTimeout = defaultCfg.Exec.DefaultTimeout
	}
	if cfg.Exec.MaxTimeout == 0 {
		cfg.Exec.MaxTimeout = defaultCfg.Exec.MaxTimeout
	}
	if cfg.Exec.StdoutMaxBytes == 0 {
		cfg.Exec.StdoutMaxBytes = defaultCfg.Exec.StdoutMaxBytes
	}
	if cfg.Exec.StderrMaxBytes == 0 {
		cfg.Exec.StderrMaxBytes = defaultCfg.Exec.StderrMaxBytes
	}
	if cfg.Exec.PreserveTailBytes == 0 {
		cfg.Exec.PreserveTailBytes = defaultCfg.Exec.PreserveTailBytes
	}
	if cfg.Exec.ExitCodeMarker.Key == "" {
		cfg.Exec.ExitCodeMarker.Key = defaultCfg.Exec.ExitCodeMarker.Key
	}
	if cfg.Exec.ExitCodeMarker.Stream == "" {
		cfg.Exec.ExitCodeMarker.Stream = defaultCfg.Exec.ExitCodeMarker.Stream
	}
	if cfg.Exec.Shell.Bin == "" {
		cfg.Exec.Shell.Bin = defaultCfg.Exec.Shell.Bin
	}
	if len(cfg.Exec.Shell.Args) == 0 {
		cfg.Exec.Shell.Args = defaultCfg.Exec.Shell.Args
	}
	if cfg.Exec.Env.AllowRegex == "" {
		cfg.Exec.Env.AllowRegex = defaultCfg.Exec.Env.AllowRegex
	}
	if len(cfg.Exec.Workdir.AllowedPrefixes) == 0 {
		cfg.Exec.Workdir.AllowedPrefixes = defaultCfg.Exec.Workdir.AllowedPrefixes
	}

	// Files defaults
	if cfg.Files.RootPrefix == "" {
		cfg.Files.RootPrefix = defaultCfg.Files.RootPrefix
	}
	if cfg.Files.Upload.DefaultDest == "" {
		cfg.Files.Upload.DefaultDest = defaultCfg.Files.Upload.DefaultDest
	}
	if cfg.Files.Upload.MaxBytes == 0 {
		cfg.Files.Upload.MaxBytes = defaultCfg.Files.Upload.MaxBytes
	}
	if cfg.Files.Upload.Format == "" {
		cfg.Files.Upload.Format = defaultCfg.Files.Upload.Format
	}
	if cfg.Files.Download.DefaultSrc == "" {
		cfg.Files.Download.DefaultSrc = defaultCfg.Files.Download.DefaultSrc
	}
	if cfg.Files.Download.Format == "" {
		cfg.Files.Download.Format = defaultCfg.Files.Download.Format
	}
	if cfg.Files.Tar.Bin == "" {
		cfg.Files.Tar.Bin = defaultCfg.Files.Tar.Bin
	}
}

// ComputeHash computes the SHA256 hash of configuration data
func ComputeHash(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// Clone creates a deep copy of the configuration
// Note: This method does not validate the cloned config. If the original config
// was validated, the clone will preserve the same structure. Call Validate()
// on the cloned config if you need to ensure it's still valid after cloning.
func (c *Config) Clone() (*Config, error) {
	data, err := yaml.Marshal(c)
	if err != nil {
		return nil, err
	}

	clone := &Config{}
	if err := yaml.Unmarshal(data, clone); err != nil {
		return nil, err
	}

	return clone, nil
}
