package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	t.Run("valid config file", func(t *testing.T) {
		// Create a temporary config file
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "config.yaml")
		configContent := `
version: 1
server:
  httpPort: 8080
  requestIdHeader: "X-Request-Id"
  timeouts:
    readHeader: 5s
    read: 30s
    write: 60s
    idle: 120s
  maxHeaderBytes: 1048576
  metrics:
    path: "/metrics"
  debug:
    configPath: "/debug/config"
auth:
  headerName: "X-Service-Key"
kubernetes:
  qps: 50
  burst: 100
  requestTimeout: 15s
sandbox:
  defaults:
    namespace: "agentsmith-sandbox"
rateLimit:
  requestsPerMinute: 60
`
		err := os.WriteFile(configPath, []byte(configContent), 0644)
		if err != nil {
			t.Fatalf("Failed to write config file: %v", err)
		}

		cfg, meta, err := Load(configPath)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}

		if cfg == nil {
			t.Fatal("Load() returned nil config")
		}
		if meta == nil {
			t.Fatal("Load() returned nil meta")
		}
		if cfg.Version != 1 {
			t.Errorf("Load() version = %v, want 1", cfg.Version)
		}
		if cfg.Sandbox.Defaults.Namespace != "agentsmith-sandbox" {
			t.Errorf("Load() sandbox namespace = %q, want agentsmith-sandbox", cfg.Sandbox.Defaults.Namespace)
		}
		if meta.SourcePath != configPath {
			t.Errorf("Load() sourcePath = %v, want %v", meta.SourcePath, configPath)
		}
		if meta.CurrentHash == "" {
			t.Error("Load() hash is empty")
		}
		if meta.SchemaVersion != 1 {
			t.Errorf("Load() schemaVersion = %v, want 1", meta.SchemaVersion)
		}
	})

	t.Run("file does not exist", func(t *testing.T) {
		_, _, err := Load("/nonexistent/path/config.yaml")
		if err == nil {
			t.Error("Load() expected error for nonexistent file, got nil")
		}
		var cfgErr *ConfigError
		if !strings.Contains(err.Error(), "CONFIG_READ_FAILED") {
			t.Errorf("Load() error = %v, want CONFIG_READ_FAILED", err)
		}
		_ = cfgErr
	})

	t.Run("invalid YAML", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "config.yaml")
		err := os.WriteFile(configPath, []byte("invalid: yaml: content: ["), 0644)
		if err != nil {
			t.Fatalf("Failed to write config file: %v", err)
		}

		_, _, err = Load(configPath)
		if err == nil {
			t.Error("Load() expected error for invalid YAML, got nil")
		}
		if !strings.Contains(err.Error(), "CONFIG_PARSE_FAILED") {
			t.Errorf("Load() error = %v, want CONFIG_PARSE_FAILED", err)
		}
	})

	t.Run("empty file uses defaults", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "config.yaml")
		err := os.WriteFile(configPath, []byte("version: 1\n"), 0644)
		if err != nil {
			t.Fatalf("Failed to write config file: %v", err)
		}

		cfg, meta, err := Load(configPath)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}

		if cfg.Version != 1 {
			t.Errorf("Load() version = %v, want 1", cfg.Version)
		}
		if meta.CurrentHash == "" {
			t.Error("Load() hash is empty")
		}
	})
}

func TestMustLoad(t *testing.T) {
	t.Run("valid file", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "config.yaml")
		configContent := `version: 1
server:
  httpPort: 8080
  requestIdHeader: "X-Request-Id"
`
		err := os.WriteFile(configPath, []byte(configContent), 0644)
		if err != nil {
			t.Fatalf("Failed to write config file: %v", err)
		}

		// MustLoad should not panic
		cfg, meta := MustLoad(configPath)
		if cfg == nil {
			t.Error("MustLoad() returned nil config")
		}
		if meta == nil {
			t.Error("MustLoad() returned nil meta")
		}
	})

	t.Run("invalid file panics", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("MustLoad() expected to panic for invalid file")
			}
		}()
		_, _ = MustLoad("/nonexistent/path/config.yaml")
	})
}

func TestLoadWithDefaults(t *testing.T) {
	t.Run("valid config with defaults", func(t *testing.T) {
		tmpDir := t.TempDir()
		configPath := filepath.Join(tmpDir, "config.yaml")
		configContent := `version: 1
server:
  httpPort: 9090
`
		err := os.WriteFile(configPath, []byte(configContent), 0644)
		if err != nil {
			t.Fatalf("Failed to write config file: %v", err)
		}

		cfg, err := LoadWithDefaults(configPath)
		if err != nil {
			t.Fatalf("LoadWithDefaults() error = %v", err)
		}

		if cfg.Server.HTTPPort != 9090 {
			t.Errorf("LoadWithDefaults() httpPort = %v, want 9090", cfg.Server.HTTPPort)
		}

		if cfg.Server.RequestIDHeader == "" {
			t.Error("LoadWithDefaults() requestIDHeader was not applied from defaults")
		}
		if cfg.Sandbox.Defaults.Namespace == "" {
			t.Error("LoadWithDefaults() sandbox namespace was not applied from defaults")
		}
	})

	t.Run("nonexistent file returns error", func(t *testing.T) {
		_, err := LoadWithDefaults("/nonexistent/path/config.yaml")
		if err == nil {
			t.Error("LoadWithDefaults() expected error, got nil")
		}
	})
}

func TestApplyDefaults(t *testing.T) {
	tests := []struct {
		name     string
		input    *Config
		validate func(*Config) bool
	}{
		{
			name:  "empty config gets all defaults",
			input: &Config{},
			validate: func(cfg *Config) bool {
				return cfg.Server.HTTPPort != 0 &&
					cfg.Server.RequestIDHeader != "" &&
					cfg.Kubernetes.QPS != 0
			},
		},
		{
			name: "partial config preserves set values",
			input: &Config{
				Server: ServerConfig{
					HTTPPort: 9999,
				},
			},
			validate: func(cfg *Config) bool {
				return cfg.Server.HTTPPort == 9999 &&
					cfg.Server.RequestIDHeader != "" // default applied
			},
		},
		{
			name: "zero values get defaults",
			input: &Config{
				Server: ServerConfig{
					HTTPPort:        8080,
					RequestIDHeader: "X-Custom-Id",
					Timeouts: ServerTimeouts{
						ReadHeader: 10 * 1000000000,
					},
				},
			},
			validate: func(cfg *Config) bool {
				return cfg.Server.HTTPPort == 8080 &&
					cfg.Server.RequestIDHeader == "X-Custom-Id" &&
					cfg.Server.Timeouts.ReadHeader == 10*1000000000 &&
					cfg.Server.Timeouts.Read != 0 // default applied
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			applyDefaults(tt.input)
			if !tt.validate(tt.input) {
				t.Error("applyDefaults() validation failed")
			}
		})
	}
}

func TestConfig_Clone(t *testing.T) {
	tests := []struct {
		name string
		cfg  *Config
	}{
		{
			name: "default config",
			cfg:  DefaultConfig(),
		},
		{
			name: "custom config",
			cfg: &Config{
				Version: 1,
				Server: ServerConfig{
					HTTPPort:        9090,
					RequestIDHeader: "X-Custom-Id",
				},
			Auth: AuthConfig{
				HeaderName: "X-Auth-Key",
			},
		},
	},
	{
		name: "config with all fields",
		cfg: &Config{
			Version: 1,
			Auth: AuthConfig{
				HeaderName: "X-Auth-Key",
			},
				RateLimit: RateLimitConfig{
					RequestsPerMinute: 120,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clone, err := tt.cfg.Clone()
			if err != nil {
				t.Fatalf("Clone() error = %v", err)
			}

			// Check that clone is not the same instance
			if clone == tt.cfg {
				t.Error("Clone() returned same instance")
			}

			// Check that values are equal
			if clone.Version != tt.cfg.Version {
				t.Errorf("Clone() version = %v, want %v", clone.Version, tt.cfg.Version)
			}
			if clone.Server.HTTPPort != tt.cfg.Server.HTTPPort {
				t.Errorf("Clone() httpPort = %v, want %v", clone.Server.HTTPPort, tt.cfg.Server.HTTPPort)
			}

			// Modify clone and ensure original is unchanged
			clone.Server.HTTPPort = 9999
			if tt.cfg.Server.HTTPPort == 9999 {
				t.Error("Clone() modifying clone affected original")
			}
		})
	}
}

func TestConfig_DeepCopy(t *testing.T) {
	cfg := &Config{
		Version: 1,
		Server:  ServerConfig{HTTPPort: 9090, RequestIDHeader: "X-Req-Id"},
	}
	cp := cfg.DeepCopy()
	if cp == cfg {
		t.Error("DeepCopy() returned same pointer")
	}
	if cp.Version != cfg.Version {
		t.Errorf("DeepCopy().Version = %d, want %d", cp.Version, cfg.Version)
	}
	if cp.Server.HTTPPort != cfg.Server.HTTPPort {
		t.Errorf("DeepCopy().Server.HTTPPort = %d, want %d", cp.Server.HTTPPort, cfg.Server.HTTPPort)
	}
}

func TestConfigError_Error(t *testing.T) {
	err := &ConfigError{
		Code:      "TEST_CODE",
		Message:   "Test message",
		Timestamp: "2024-01-01T00:00:00Z",
	}

	errStr := err.Error()
	if !strings.Contains(errStr, "TEST_CODE") {
		t.Errorf("ConfigError.Error() = %v, want to contain TEST_CODE", errStr)
	}
	if !strings.Contains(errStr, "Test message") {
		t.Errorf("ConfigError.Error() = %v, want to contain 'Test message'", errStr)
	}
}
