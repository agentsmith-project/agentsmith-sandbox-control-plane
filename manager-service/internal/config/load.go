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
func LoadWithDefaults(path string) (*Config, error) {
	cfg, _, err := Load(path)
	if err != nil {
		return nil, err
	}

	applyDefaults(cfg)

	return cfg, nil
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

}

// Clone creates a deep copy of the configuration
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
