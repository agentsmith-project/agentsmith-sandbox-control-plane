package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
)

// ValidationResult contains the result of configuration validation
type ValidationResult struct {
	Valid  bool
	Errors []*ConfigError
}

// Validate validates the entire configuration
func (c *Config) Validate() *ValidationResult {
	result := &ValidationResult{Valid: true}
	errors := []*ConfigError{}

	// Validate version
	if err := validateVersion(c.Version); err != nil {
		errors = append(errors, err)
	}

	// Validate server config
	if errs := validateServerConfig(&c.Server); len(errs) > 0 {
		errors = append(errors, errs...)
	}

	// Validate auth config
	if errs := validateAuthConfig(&c.Auth); len(errs) > 0 {
		errors = append(errors, errs...)
	}

	// Validate kubernetes config
	if errs := validateK8sConfig(&c.Kubernetes); len(errs) > 0 {
		errors = append(errors, errs...)
	}

	// Validate sandbox config
	if errs := validateSandboxConfig(&c.Sandbox); len(errs) > 0 {
		errors = append(errors, errs...)
	}

	// Validate exec config
	if errs := validateExecConfig(&c.Exec); len(errs) > 0 {
		errors = append(errors, errs...)
	}

	// Validate files config
	if errs := validateFilesConfig(&c.Files); len(errs) > 0 {
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

	// HTTP Port
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

	// Request ID Header
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

	// Timeouts
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

	// MaxHeaderBytes
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

	// Metrics path
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

	// Debug config path
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

	if cfg.AuthorizationScheme == "" {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "auth.authorizationScheme",
			RuleID:    "REQUIRED",
			Rule:      "authorizationScheme is required",
			Message:   "authorizationScheme cannot be empty",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	// Validate failStatusCode
	validStatusCodes := map[int]bool{400: true, 401: true, 403: true}
	if !validStatusCodes[cfg.FailStatusCode] {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "auth.failStatusCode",
			RuleID:    "ENUM",
			Rule:      "failStatusCode must be 400, 401, or 403",
			Message:   fmt.Sprintf("Invalid failStatusCode: %d", cfg.FailStatusCode),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	return errors
}

func validateK8sConfig(cfg *K8sConfig) []*ConfigError {
	errors := []*ConfigError{}

	// QPS
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

	// Burst
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

	// RequestTimeout
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

	// Retry config
	if cfg.Retry.Enabled {
		if cfg.Retry.MaxAttempts < 1 {
			errors = append(errors, &ConfigError{
				Code:      "CONFIG_VALIDATION_FAILED",
				FieldPath: "kubernetes.retry.maxAttempts",
				RuleID:    "RANGE",
				Rule:      "maxAttempts must be at least 1",
				Message:   fmt.Sprintf("Invalid maxAttempts: %d", cfg.Retry.MaxAttempts),
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		}
		if cfg.Retry.BaseBackoff < 0 {
			errors = append(errors, &ConfigError{
				Code:      "CONFIG_VALIDATION_FAILED",
				FieldPath: "kubernetes.retry.baseBackoff",
				RuleID:    "RANGE",
				Rule:      "baseBackoff must be non-negative",
				Message:   fmt.Sprintf("Invalid baseBackoff: %v", cfg.Retry.BaseBackoff),
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		}
		if cfg.Retry.MaxBackoff < cfg.Retry.BaseBackoff {
			errors = append(errors, &ConfigError{
				Code:      "CONFIG_VALIDATION_FAILED",
				FieldPath: "kubernetes.retry.maxBackoff",
				RuleID:    "RELATION",
				Rule:      "maxBackoff must be >= baseBackoff",
				Message:   fmt.Sprintf("maxBackoff (%v) < baseBackoff (%v)", cfg.Retry.MaxBackoff, cfg.Retry.BaseBackoff),
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		}
	}

	return errors
}

func validateSandboxConfig(cfg *SandboxConfig) []*ConfigError {
	errors := []*ConfigError{}
	d := &cfg.Defaults

	// Namespace
	if d.Namespace == "" {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "sandbox.defaults.namespace",
			RuleID:    "REQUIRED",
			Rule:      "namespace is required",
			Message:   "namespace cannot be empty",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	// RunnerImage
	if d.RunnerImage == "" {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "sandbox.defaults.runnerImage",
			RuleID:    "REQUIRED",
			Rule:      "runnerImage is required",
			Message:   "runnerImage cannot be empty",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	// ImagePullPolicy
	validPolicies := map[string]bool{"Always": true, "Never": true, "IfNotPresent": true}
	if !validPolicies[d.ImagePullPolicy] {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "sandbox.defaults.imagePullPolicy",
			RuleID:    "ENUM",
			Rule:      "imagePullPolicy must be Always, Never, or IfNotPresent",
			Message:   fmt.Sprintf("Invalid imagePullPolicy: %s", d.ImagePullPolicy),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	// TTLSeconds
	if d.TTLSeconds < 1 || d.TTLSeconds > 86400 {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "sandbox.defaults.ttlSeconds",
			RuleID:    "RANGE",
			Rule:      "ttlSeconds must be between 1 and 86400 (24 hours)",
			Message:   fmt.Sprintf("Invalid ttlSeconds: %d", d.TTLSeconds),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	// PodReadyWait
	if d.PodReadyWait < 0 || d.PodReadyWait > 5*time.Minute {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "sandbox.defaults.podReadyWait",
			RuleID:    "RANGE",
			Rule:      "podReadyWait must be between 0 and 5 minutes",
			Message:   fmt.Sprintf("Invalid podReadyWait: %v", d.PodReadyWait),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	// PodPollInterval
	if d.PodPollInterval < 10*time.Millisecond || d.PodPollInterval > 10*time.Second {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "sandbox.defaults.podPollInterval",
			RuleID:    "RANGE",
			Rule:      "podPollInterval must be between 10ms and 10s",
			Message:   fmt.Sprintf("Invalid podPollInterval: %v", d.PodPollInterval),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	// Workdir
	if !filepath.IsAbs(d.Workdir) {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "sandbox.defaults.workdir",
			RuleID:    "FORMAT",
			Rule:      "workdir must be an absolute path",
			Message:   fmt.Sprintf("Invalid workdir: %s", d.Workdir),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	// ContainerName
	if d.ContainerName == "" {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "sandbox.defaults.containerName",
			RuleID:    "REQUIRED",
			Rule:      "containerName is required",
			Message:   "containerName cannot be empty",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	// Validate resources
	if errs := validateResourceRequirements(&d.Resources, "sandbox.defaults.resources"); len(errs) > 0 {
		errors = append(errors, errs...)
	}

	return errors
}

func validateResourceRequirements(r *ResourceRequirements, fieldPath string) []*ConfigError {
	errors := []*ConfigError{}

	// Validate CPU request <= limit
	if r.Requests.CPU != "" && r.Limits.CPU != "" {
		reqQty, err := resource.ParseQuantity(r.Requests.CPU)
		if err != nil {
			errors = append(errors, &ConfigError{
				Code:      "CONFIG_VALIDATION_FAILED",
				FieldPath: fieldPath + ".requests.cpu",
				RuleID:    "QUANTITY_PARSE",
				Rule:      "cpu must be a valid Kubernetes quantity",
				Message:   fmt.Sprintf("Invalid cpu request: %s", r.Requests.CPU),
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		}
		limQty, err := resource.ParseQuantity(r.Limits.CPU)
		if err != nil {
			errors = append(errors, &ConfigError{
				Code:      "CONFIG_VALIDATION_FAILED",
				FieldPath: fieldPath + ".limits.cpu",
				RuleID:    "QUANTITY_PARSE",
				Rule:      "cpu must be a valid Kubernetes quantity",
				Message:   fmt.Sprintf("Invalid cpu limit: %s", r.Limits.CPU),
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		}
		if err == nil && reqQty.Cmp(limQty) > 0 {
			errors = append(errors, &ConfigError{
				Code:      "CONFIG_VALIDATION_FAILED",
				FieldPath: fieldPath + ".requests.cpu",
				RuleID:    "RELATION",
				Rule:      "cpu request must be <= limit",
				Message:   fmt.Sprintf("cpu request (%s) > limit (%s)", r.Requests.CPU, r.Limits.CPU),
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		}
	}

	// Validate memory request <= limit
	if r.Requests.Memory != "" && r.Limits.Memory != "" {
		reqQty, err := resource.ParseQuantity(r.Requests.Memory)
		if err != nil {
			errors = append(errors, &ConfigError{
				Code:      "CONFIG_VALIDATION_FAILED",
				FieldPath: fieldPath + ".requests.memory",
				RuleID:    "QUANTITY_PARSE",
				Rule:      "memory must be a valid Kubernetes quantity",
				Message:   fmt.Sprintf("Invalid memory request: %s", r.Requests.Memory),
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		}
		limQty, err := resource.ParseQuantity(r.Limits.Memory)
		if err != nil {
			errors = append(errors, &ConfigError{
				Code:      "CONFIG_VALIDATION_FAILED",
				FieldPath: fieldPath + ".limits.memory",
				RuleID:    "QUANTITY_PARSE",
				Rule:      "memory must be a valid Kubernetes quantity",
				Message:   fmt.Sprintf("Invalid memory limit: %s", r.Limits.Memory),
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		}
		if err == nil && reqQty.Cmp(limQty) > 0 {
			errors = append(errors, &ConfigError{
				Code:      "CONFIG_VALIDATION_FAILED",
				FieldPath: fieldPath + ".requests.memory",
				RuleID:    "RELATION",
				Rule:      "memory request must be <= limit",
				Message:   fmt.Sprintf("memory request (%s) > limit (%s)", r.Requests.Memory, r.Limits.Memory),
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		}
	}

	return errors
}

func validateExecConfig(cfg *ExecConfig) []*ConfigError {
	errors := []*ConfigError{}

	// DefaultTimeout
	if cfg.DefaultTimeout < 0 || cfg.DefaultTimeout > 1*time.Hour {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "exec.defaultTimeout",
			RuleID:    "RANGE",
			Rule:      "defaultTimeout must be between 0 and 1 hour",
			Message:   fmt.Sprintf("Invalid defaultTimeout: %v", cfg.DefaultTimeout),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	// MaxTimeout
	if cfg.MaxTimeout < 0 || cfg.MaxTimeout > 1*time.Hour {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "exec.maxTimeout",
			RuleID:    "RANGE",
			Rule:      "maxTimeout must be between 0 and 1 hour",
			Message:   fmt.Sprintf("Invalid maxTimeout: %v", cfg.MaxTimeout),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	// MaxTimeout >= DefaultTimeout
	if cfg.MaxTimeout < cfg.DefaultTimeout {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "exec.maxTimeout",
			RuleID:    "RELATION",
			Rule:      "maxTimeout must be >= defaultTimeout",
			Message:   fmt.Sprintf("maxTimeout (%v) < defaultTimeout (%v)", cfg.MaxTimeout, cfg.DefaultTimeout),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	// StdoutMaxBytes
	if cfg.StdoutMaxBytes < 0 || cfg.StdoutMaxBytes > 64<<20 {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "exec.stdoutMaxBytes",
			RuleID:    "RANGE",
			Rule:      "stdoutMaxBytes must be between 0 and 64MiB",
			Message:   fmt.Sprintf("Invalid stdoutMaxBytes: %d", cfg.StdoutMaxBytes),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	// StderrMaxBytes
	if cfg.StderrMaxBytes < 0 || cfg.StderrMaxBytes > 64<<20 {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "exec.stderrMaxBytes",
			RuleID:    "RANGE",
			Rule:      "stderrMaxBytes must be between 0 and 64MiB",
			Message:   fmt.Sprintf("Invalid stderrMaxBytes: %d", cfg.StderrMaxBytes),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	// PreserveTailBytes relationship
	minMaxBytes := cfg.StdoutMaxBytes
	if cfg.StderrMaxBytes < minMaxBytes {
		minMaxBytes = cfg.StderrMaxBytes
	}
	if cfg.PreserveTailBytes > minMaxBytes {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "exec.preserveTailBytes",
			RuleID:    "RELATION",
			Rule:      "preserveTailBytes must be <= min(stdoutMaxBytes, stderrMaxBytes)",
			Message:   fmt.Sprintf("preserveTailBytes (%d) > min(stdoutMaxBytes, stderrMaxBytes) (%d)", cfg.PreserveTailBytes, minMaxBytes),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	// ExitCodeMarker
	if cfg.ExitCodeMarker.Key == "" {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "exec.exitCodeMarker.key",
			RuleID:    "REQUIRED",
			Rule:      "exitCodeMarker.key is required",
			Message:   "exitCodeMarker.key cannot be empty",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}
	validStreams := map[string]bool{"stdout": true, "stderr": true}
	if !validStreams[cfg.ExitCodeMarker.Stream] {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "exec.exitCodeMarker.stream",
			RuleID:    "ENUM",
			Rule:      "exitCodeMarker.stream must be stdout or stderr",
			Message:   fmt.Sprintf("Invalid exitCodeMarker.stream: %s", cfg.ExitCodeMarker.Stream),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	// Shell bin
	if cfg.Shell.Bin == "" {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "exec.shell.bin",
			RuleID:    "REQUIRED",
			Rule:      "shell.bin is required",
			Message:   "shell.bin cannot be empty",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	// Env allowRegex
	if _, err := regexp.Compile(cfg.Env.AllowRegex); err != nil {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "exec.env.allowRegex",
			RuleID:    "FORMAT",
			Rule:      "allowRegex must be a valid regular expression",
			Message:   fmt.Sprintf("Invalid allowRegex: %v", err),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	// Workdir allowedPrefixes
	for i, prefix := range cfg.Workdir.AllowedPrefixes {
		if !filepath.IsAbs(prefix) {
			errors = append(errors, &ConfigError{
				Code:      "CONFIG_VALIDATION_FAILED",
				FieldPath: fmt.Sprintf("exec.workdir.allowedPrefixes[%d]", i),
				RuleID:    "FORMAT",
				Rule:      "allowedPrefixes must be absolute paths",
				Message:   fmt.Sprintf("Invalid allowedPrefix: %s", prefix),
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		}
	}

	return errors
}

func validateFilesConfig(cfg *FilesConfig) []*ConfigError {
	errors := []*ConfigError{}

	// RootPrefix
	if !filepath.IsAbs(cfg.RootPrefix) {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "files.rootPrefix",
			RuleID:    "FORMAT",
			Rule:      "rootPrefix must be an absolute path",
			Message:   fmt.Sprintf("Invalid rootPrefix: %s", cfg.RootPrefix),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	// Upload defaultDest
	if !filepath.IsAbs(cfg.Upload.DefaultDest) {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "files.upload.defaultDest",
			RuleID:    "FORMAT",
			Rule:      "defaultDest must be an absolute path",
			Message:   fmt.Sprintf("Invalid defaultDest: %s", cfg.Upload.DefaultDest),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	// Check that defaultDest is under rootPrefix
	if filepath.IsAbs(cfg.Upload.DefaultDest) && filepath.IsAbs(cfg.RootPrefix) {
		rel, err := filepath.Rel(cfg.RootPrefix, cfg.Upload.DefaultDest)
		if err != nil || strings.HasPrefix(rel, "..") {
			errors = append(errors, &ConfigError{
				Code:      "CONFIG_VALIDATION_FAILED",
				FieldPath: "files.upload.defaultDest",
				RuleID:    "PREFIX",
				Rule:      "defaultDest must be under rootPrefix",
				Message:   fmt.Sprintf("defaultDest (%s) is not under rootPrefix (%s)", cfg.Upload.DefaultDest, cfg.RootPrefix),
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		}
	}

	// Upload maxBytes
	if cfg.Upload.MaxBytes < 0 || cfg.Upload.MaxBytes > 1<<30 {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "files.upload.maxBytes",
			RuleID:    "RANGE",
			Rule:      "maxBytes must be between 0 and 1GiB",
			Message:   fmt.Sprintf("Invalid maxBytes: %d", cfg.Upload.MaxBytes),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	// Upload format
	validFormats := map[string]bool{"tar.gz": true, "tar": true}
	if !validFormats[cfg.Upload.Format] {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "files.upload.format",
			RuleID:    "ENUM",
			Rule:      "format must be tar.gz or tar",
			Message:   fmt.Sprintf("Invalid upload format: %s", cfg.Upload.Format),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	// Download defaultSrc
	if !filepath.IsAbs(cfg.Download.DefaultSrc) {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "files.download.defaultSrc",
			RuleID:    "FORMAT",
			Rule:      "defaultSrc must be an absolute path",
			Message:   fmt.Sprintf("Invalid defaultSrc: %s", cfg.Download.DefaultSrc),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	// Check that defaultSrc is under rootPrefix
	if filepath.IsAbs(cfg.Download.DefaultSrc) && filepath.IsAbs(cfg.RootPrefix) {
		rel, err := filepath.Rel(cfg.RootPrefix, cfg.Download.DefaultSrc)
		if err != nil || strings.HasPrefix(rel, "..") {
			errors = append(errors, &ConfigError{
				Code:      "CONFIG_VALIDATION_FAILED",
				FieldPath: "files.download.defaultSrc",
				RuleID:    "PREFIX",
				Rule:      "defaultSrc must be under rootPrefix",
				Message:   fmt.Sprintf("defaultSrc (%s) is not under rootPrefix (%s)", cfg.Download.DefaultSrc, cfg.RootPrefix),
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		}
	}

	// Download format
	if !validFormats[cfg.Download.Format] {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "files.download.format",
			RuleID:    "ENUM",
			Rule:      "format must be tar.gz or tar",
			Message:   fmt.Sprintf("Invalid download format: %s", cfg.Download.Format),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	// Tar bin
	if cfg.Tar.Bin == "" {
		errors = append(errors, &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			FieldPath: "files.tar.bin",
			RuleID:    "REQUIRED",
			Rule:      "tar.bin is required",
			Message:   "tar.bin cannot be empty",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	return errors
}

// ValidateEnvKey validates an environment variable key against the allowRegex
func (c *Config) ValidateEnvKey(key string) bool {
	matched, err := regexp.MatchString(c.Exec.Env.AllowRegex, key)
	if err != nil {
		return false
	}
	return matched
}

// ValidateWorkdir validates that a workdir is under allowed prefixes
func (c *Config) ValidateWorkdir(workdir string) bool {
	if !filepath.IsAbs(workdir) {
		return false
	}

	for _, prefix := range c.Exec.Workdir.AllowedPrefixes {
		rel, err := filepath.Rel(prefix, workdir)
		if err == nil && !strings.HasPrefix(rel, "..") {
			return true
		}
	}
	return false
}

// ValidateFilePath validates that a path is under rootPrefix
func (c *Config) ValidateFilePath(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}

	rel, err := filepath.Rel(c.Files.RootPrefix, path)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..")
}

// CheckFileExists checks if a file exists (used for boot config validation)
func CheckFileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}
