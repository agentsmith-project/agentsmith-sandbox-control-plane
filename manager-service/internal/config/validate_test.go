package config

import (
	"testing"
)

func TestDefaultConfigValidates(t *testing.T) {
	cfg := DefaultConfig()
	result := cfg.Validate()
	if !result.Valid {
		for _, e := range result.Errors {
			t.Errorf("validation error: [%s] %s: %s", e.Code, e.FieldPath, e.Message)
		}
		t.Fatalf("default config should be valid, got %d errors", len(result.Errors))
	}
}

func TestValidate_InvalidVersion(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Version = 99
	result := cfg.Validate()
	if result.Valid {
		t.Error("expected invalid for version 99")
	}
	assertHasError(t, result, "version")
}

func TestValidate_InvalidHTTPPort(t *testing.T) {
	tests := []struct {
		name string
		port int
	}{
		{"zero", 0},
		{"negative", -1},
		{"too high", 70000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Server.HTTPPort = tt.port
			result := cfg.Validate()
			if result.Valid {
				t.Errorf("expected invalid for port %d", tt.port)
			}
			assertHasError(t, result, "server.httpPort")
		})
	}
}

func TestValidate_EmptyRequestIDHeader(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Server.RequestIDHeader = ""
	result := cfg.Validate()
	if result.Valid {
		t.Error("expected invalid for empty requestIdHeader")
	}
	assertHasError(t, result, "server.requestIdHeader")
}

func TestValidate_InvalidK8sQPS(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Kubernetes.QPS = 2000
	result := cfg.Validate()
	if result.Valid {
		t.Error("expected invalid for QPS > 1000")
	}
	assertHasError(t, result, "kubernetes.qps")
}

func TestValidate_InvalidSandboxNamespace(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Defaults.Namespace = ""
	result := cfg.Validate()
	if result.Valid {
		t.Error("expected invalid for empty namespace")
	}
	assertHasError(t, result, "sandbox.defaults.namespace")
}

func TestValidate_InvalidTTLSeconds(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Defaults.TTLSeconds = 0
	result := cfg.Validate()
	if result.Valid {
		t.Error("expected invalid for TTL 0")
	}
	assertHasError(t, result, "sandbox.defaults.ttlSeconds")
}

func TestValidate_InvalidExecTimeout(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Exec.MaxTimeout = cfg.Exec.DefaultTimeout / 2
	result := cfg.Validate()
	if result.Valid {
		t.Error("expected invalid when maxTimeout < defaultTimeout")
	}
	assertHasError(t, result, "exec.maxTimeout")
}

func TestValidate_InvalidFilesRootPrefix(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Files.RootPrefix = "relative/path"
	result := cfg.Validate()
	if result.Valid {
		t.Error("expected invalid for relative rootPrefix")
	}
	assertHasError(t, result, "files.rootPrefix")
}

func TestValidateEnvKey(t *testing.T) {
	cfg := DefaultConfig()

	tests := []struct {
		key   string
		valid bool
	}{
		{"FOO", true},
		{"FOO_BAR", true},
		{"_FOO", true},
		{"A123", true},
		{"foo", false},
		{"1FOO", false},
		{"FOO-BAR", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			if got := cfg.ValidateEnvKey(tt.key); got != tt.valid {
				t.Errorf("ValidateEnvKey(%q) = %v, want %v", tt.key, got, tt.valid)
			}
		})
	}
}

func TestValidateWorkdir(t *testing.T) {
	cfg := DefaultConfig()

	tests := []struct {
		workdir string
		valid   bool
	}{
		{"/workspace", true},
		{"/workspace/subdir", true},
		{"/tmp", false},
		{"relative", false},
		{"/etc/passwd", false},
	}
	for _, tt := range tests {
		t.Run(tt.workdir, func(t *testing.T) {
			if got := cfg.ValidateWorkdir(tt.workdir); got != tt.valid {
				t.Errorf("ValidateWorkdir(%q) = %v, want %v", tt.workdir, got, tt.valid)
			}
		})
	}
}

func TestValidateFilePath(t *testing.T) {
	cfg := DefaultConfig()

	tests := []struct {
		path  string
		valid bool
	}{
		{"/workspace", true},
		{"/workspace/file.txt", true},
		{"/etc/passwd", false},
		{"relative", false},
		{"/workspace/../etc/passwd", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := cfg.ValidateFilePath(tt.path); got != tt.valid {
				t.Errorf("ValidateFilePath(%q) = %v, want %v", tt.path, got, tt.valid)
			}
		})
	}
}

// assertHasError checks that the validation result contains an error for the given field
func assertHasError(t *testing.T, result *ValidationResult, fieldPath string) {
	t.Helper()
	for _, e := range result.Errors {
		if e.FieldPath == fieldPath {
			return
		}
	}
	fields := make([]string, 0, len(result.Errors))
	for _, e := range result.Errors {
		fields = append(fields, e.FieldPath)
	}
	t.Errorf("expected error for field %q, got errors for: %v", fieldPath, fields)
}
