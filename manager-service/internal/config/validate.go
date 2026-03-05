package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

type ValidationResult struct {
	Valid  bool
	Errors []*ConfigError
}

func (c *Config) Validate() *ValidationResult {
	result := &ValidationResult{Valid: true}
	errors := []*ConfigError{}

	if err := validateVersion(c.Version); err != nil {
		errors = append(errors, err)
	}
	if errs := validateServerConfig(&c.Server); len(errs) > 0 {
		errors = append(errors, errs...)
	}
	if errs := validateAuthConfig(&c.Auth); len(errs) > 0 {
		errors = append(errors, errs...)
	}
	if errs := validateK8sConfig(&c.Kubernetes); len(errs) > 0 {
		errors = append(errors, errs...)
	}

	if len(errors) > 0 {
		result.Valid = false
		result.Errors = errors
	}
	return result
}

func validateVersion(version int) *ConfigError {
	if version != 1 {
		return &ConfigError{
			Code:      "CONFIG_SCHEMA_UNSUPPORTED",
			FieldPath: "version",
			RuleID:    "ENUM",
			Rule:      "version must be 1",
			Message:   fmt.Sprintf("Unsupported config version: %d (only version 1 is supported)", version),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}
	}
	return nil
}

func validateServerConfig(cfg *ServerConfig) []*ConfigError {
	errors := []*ConfigError{}

	if cfg.HTTPPort < 1 || cfg.HTTPPort > 65535 {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "server.httpPort",
			RuleID:    "RANGE",
			Rule:      "httpPort must be between 1 and 65535",
			Message:   fmt.Sprintf("Invalid httpPort: %d", cfg.HTTPPort),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	if cfg.RequestIDHeader == "" {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "server.requestIdHeader",
			RuleID:    "REQUIRED",
			Rule:      "requestIdHeader is required",
			Message:   "requestIdHeader cannot be empty",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	if cfg.Timeouts.ReadHeader < 0 {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "server.timeouts.readHeader",
			RuleID:    "RANGE",
			Rule:      "readHeader must be non-negative",
			Message:   fmt.Sprintf("Invalid readHeader: %v", cfg.Timeouts.ReadHeader),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}
	if cfg.Timeouts.Read < 0 {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "server.timeouts.read",
			RuleID:    "RANGE",
			Rule:      "read must be non-negative",
			Message:   fmt.Sprintf("Invalid read: %v", cfg.Timeouts.Read),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}
	if cfg.Timeouts.Write < 0 {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "server.timeouts.write",
			RuleID:    "RANGE",
			Rule:      "write must be non-negative",
			Message:   fmt.Sprintf("Invalid write: %v", cfg.Timeouts.Write),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}
	if cfg.Timeouts.Idle < 0 {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "server.timeouts.idle",
			RuleID:    "RANGE",
			Rule:      "idle must be non-negative",
			Message:   fmt.Sprintf("Invalid idle: %v", cfg.Timeouts.Idle),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	if cfg.MaxHeaderBytes < 0 {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "server.maxHeaderBytes",
			RuleID:    "RANGE",
			Rule:      "maxHeaderBytes must be non-negative",
			Message:   fmt.Sprintf("Invalid maxHeaderBytes: %d", cfg.MaxHeaderBytes),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	if !strings.HasPrefix(cfg.Metrics.Path, "/") {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "server.metrics.path",
			RuleID:    "FORMAT",
			Rule:      "metrics.path must start with /",
			Message:   fmt.Sprintf("Invalid metrics.path: %s", cfg.Metrics.Path),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	if !strings.HasPrefix(cfg.Debug.ConfigPath, "/") {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "server.debug.configPath",
			RuleID:    "FORMAT",
			Rule:      "debug.configPath must start with /",
			Message:   fmt.Sprintf("Invalid debug.configPath: %s", cfg.Debug.ConfigPath),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	return errors
}

func validateAuthConfig(cfg *AuthConfig) []*ConfigError {
	errors := []*ConfigError{}
	if cfg.HeaderName == "" {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "auth.headerName",
			RuleID:    "REQUIRED",
			Rule:      "headerName is required",
			Message:   "headerName cannot be empty",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}
	return errors
}

func validateK8sConfig(cfg *K8sConfig) []*ConfigError {
	errors := []*ConfigError{}

	if cfg.QPS < 0 {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "kubernetes.qps",
			RuleID:    "RANGE",
			Rule:      "qps must be non-negative",
			Message:   fmt.Sprintf("Invalid qps: %d", cfg.QPS),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	if cfg.Burst < 0 {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "kubernetes.burst",
			RuleID:    "RANGE",
			Rule:      "burst must be non-negative",
			Message:   fmt.Sprintf("Invalid burst: %d", cfg.Burst),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	if cfg.RequestTimeout < 0 {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "kubernetes.requestTimeout",
			RuleID:    "RANGE",
			Rule:      "requestTimeout must be non-negative",
			Message:   fmt.Sprintf("Invalid requestTimeout: %v", cfg.RequestTimeout),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	return errors
}

// CheckFileExists checks if a file exists (used for boot config validation)
func CheckFileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}
