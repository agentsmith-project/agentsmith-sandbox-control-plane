package app

import (
	"testing"
	"time"

	"github.com/sandbox/manager/internal/config"
	"github.com/stretchr/testify/assert"
)

// TestParseDuration_ValidDuration_ReturnsDuration tests parseDuration with valid duration strings
func TestParseDuration_ValidDuration_ReturnsDuration(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected time.Duration
	}{
		{"seconds", "30s", 30 * time.Second},
		{"milliseconds", "500ms", 500 * time.Millisecond},
		{"minutes", "5m", 5 * time.Minute},
		{"hours", "2h", 2 * time.Hour},
		{"complex", "1h30m", 90 * time.Minute},
		{"zero", "0s", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseDuration(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestParseDuration_InvalidDuration_ReturnsZero tests parseDuration with invalid duration strings
func TestParseDuration_InvalidDuration_ReturnsZero(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"invalid", "invalid"},
		{"empty", ""},
		{"partial", "5"},
		{"wrong unit", "5years"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseDuration(tt.input)
			assert.Equal(t, time.Duration(0), result)
		})
	}
}

// TestConvertConfigError_Nil_ReturnsNil tests convertConfigError with nil input
func TestConvertConfigError_Nil_ReturnsNil(t *testing.T) {
	result := convertConfigError(nil)
	assert.Nil(t, result)
}

// TestConvertConfigError_ValidError_ReturnsConfigError tests convertConfigError with valid error
func TestConvertConfigError_ValidError_ReturnsConfigError(t *testing.T) {
	input := &config.ConfigError{
		Code:      "TEST_ERROR",
		Message:   "Test error message",
		FieldPath: "server.httpPort",
		RuleID:    "rule-123",
		Rule:      "Test rule",
		Timestamp: "2024-01-01T00:00:00Z",
	}

	result := convertConfigError(input)

	assert.NotNil(t, result)
	assert.Equal(t, "TEST_ERROR", result.Code)
	assert.Equal(t, "Test error message", result.Message)
	assert.Equal(t, "server.httpPort", result.FieldPath)
	assert.Equal(t, "rule-123", result.RuleID)
	assert.Equal(t, "Test rule", result.Rule)
	assert.Equal(t, "2024-01-01T00:00:00Z", result.Timestamp)
}

// TestConvertConfigError_PartialFields_ReturnsPartialConfigError tests convertConfigError with partial fields
func TestConvertConfigError_PartialFields_ReturnsPartialConfigError(t *testing.T) {
	input := &config.ConfigError{
		Code:    "PARTIAL_ERROR",
		Message: "Only some fields set",
	}

	result := convertConfigError(input)

	assert.NotNil(t, result)
	assert.Equal(t, "PARTIAL_ERROR", result.Code)
	assert.Equal(t, "Only some fields set", result.Message)
	assert.Empty(t, result.FieldPath)
	assert.Empty(t, result.RuleID)
}

// TestGetEnvOrDefault tests getEnvOrDefault function behavior
func TestGetEnvOrDefault(t *testing.T) {
	// Test with default value when env var is not set
	result := getEnvOrDefault("NONEXISTENT_ENV_VAR_12345", "default_value")
	assert.Equal(t, "default_value", result)

	// Note: We cannot easily test the case where env var IS set
	// without modifying the test environment, which could affect other tests
	// The function behavior is: return env value if set and non-empty, otherwise return default
}

// TestParseSandboxRoute tests parseSandboxRoute function
func TestParseSandboxRoute(t *testing.T) {
	tests := []struct {
		name            string
		path            string
		expectedRoute   string
		expectedSession string
	}{
		{
			name:            "direct session endpoint",
			path:            "/v1/sandboxes/session-123",
			expectedRoute:   "sandbox",
			expectedSession: "session-123",
		},
		{
			name:            "touch endpoint",
			path:            "/v1/sandboxes/session-123/touch",
			expectedRoute:   "touch",
			expectedSession: "session-123",
		},
		{
			name:            "exec endpoint",
			path:            "/v1/sandboxes/session-123/exec",
			expectedRoute:   "exec",
			expectedSession: "session-123",
		},
		{
			name:            "files upload endpoint",
			path:            "/v1/sandboxes/session-123/files/upload",
			expectedRoute:   "files/upload",
			expectedSession: "session-123",
		},
		{
			name:            "files download endpoint",
			path:            "/v1/sandboxes/session-123/files/download",
			expectedRoute:   "files/download",
			expectedSession: "session-123",
		},
		{
			name:            "invalid path - no version",
			path:            "/sandboxes/session-123",
			expectedRoute:   "",
			expectedSession: "",
		},
		{
			name:            "invalid path - wrong resource",
			path:            "/v1/pods/session-123",
			expectedRoute:   "",
			expectedSession: "",
		},
		{
			name:            "empty path",
			path:            "",
			expectedRoute:   "",
			expectedSession: "",
		},
		{
			name:            "path too short",
			path:            "/v1/sandboxes",
			expectedRoute:   "",
			expectedSession: "",
		},
		{
			name:            "invalid action",
			path:            "/v1/sandboxes/session-123/invalid",
			expectedRoute:   "",
			expectedSession: "",
		},
		{
			name:            "files with invalid sub-action",
			path:            "/v1/sandboxes/session-123/files/invalid",
			expectedRoute:   "",
			expectedSession: "",
		},
		{
			name:            "touch with extra path",
			path:            "/v1/sandboxes/session-123/touch/extra",
			expectedRoute:   "",
			expectedSession: "",
		},
		{
			name:            "session ID with special characters",
			path:            "/v1/sandboxes/session-123-abc/touch",
			expectedRoute:   "touch",
			expectedSession: "session-123-abc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route, session := parseSandboxRoute(tt.path)
			assert.Equal(t, tt.expectedRoute, route)
			assert.Equal(t, tt.expectedSession, session)
		})
	}
}

// TestSanitizeConfig_AllFields_SerializesCorrectly tests that all fields are properly serialized
func TestSanitizeConfig_AllFields_SerializesCorrectly(t *testing.T) {
	cfg := &config.Config{
		Version: 1,
		Server: config.ServerConfig{
			HTTPPort:        8080,
			RequestIDHeader: "X-Request-ID",
			Timeouts: config.ServerTimeouts{
				ReadHeader: 5 * time.Second,
				Read:       30 * time.Second,
				Write:      60 * time.Second,
				Idle:       120 * time.Second,
			},
			MaxHeaderBytes: 1048576,
			Metrics: config.MetricsConfig{
				Enabled:           true,
				Path:              "/metrics",
				RequireServiceKey: false,
			},
			Debug: config.DebugConfig{
				ConfigPath:  "/debug/config",
				EnablePprof: false,
			},
		},
		Auth: config.AuthConfig{
			Enabled:             true,
			HeaderName:          "X-Service-Key",
			AcceptAuthorization: true,
			AuthorizationScheme: "ServiceKey",
			FailStatusCode:      401,
		},
		Kubernetes: config.K8sConfig{
			QPS:            50,
			Burst:          100,
			RequestTimeout: 15 * time.Second,
			Retry: config.K8sRetryConfig{
				Enabled:     true,
				MaxAttempts: 3,
				BaseBackoff: 200 * time.Millisecond,
				MaxBackoff:  2 * time.Second,
			},
		},
		Exec: config.ExecConfig{
			DefaultTimeout:    30 * time.Second,
			MaxTimeout:        300 * time.Second,
			StdoutMaxBytes:    1048576,
			StderrMaxBytes:    1048576,
			PreserveTailBytes: 4096,
			ExitCodeMarker: config.ExitCodeMarker{
				Key:    "__EXIT__",
				Stream: "stderr",
			},
			Shell: config.ShellConfig{
				Bin:  "sh",
				Args: []string{"-c"},
			},
		},
	}

	result := sanitizeConfig(cfg)

	// Verify all major fields are present
	assert.NotNil(t, result)
	assert.Equal(t, 1, result.Version)
	assert.Equal(t, 8080, result.Server.HTTPPort)
	assert.True(t, result.Auth.Enabled)
	assert.Equal(t, 50, result.Kubernetes.QPS)
}
