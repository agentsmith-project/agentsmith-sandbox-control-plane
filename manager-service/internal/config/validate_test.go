package config

import (
	"testing"
	"time"
)

func TestValidateVersion(t *testing.T) {
	tests := []struct {
		name    string
		version int
		wantErr bool
	}{
		{"valid version 1", 1, false},
		{"invalid version 0", 0, true},
		{"invalid version 2", 2, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateVersion(tt.version)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateVersion() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateServerConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ServerConfig
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: ServerConfig{
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
					Path: "/metrics",
				},
				Debug: DebugConfig{
					ConfigPath: "/debug/config",
				},
			},
			wantErr: false,
		},
		{
			name: "invalid port",
			cfg: ServerConfig{
				HTTPPort:        0,
				RequestIDHeader: "X-Request-Id",
			},
			wantErr: true,
		},
		{
			name: "empty request id header",
			cfg: ServerConfig{
				HTTPPort:        8080,
				RequestIDHeader: "",
			},
			wantErr: true,
		},
		{
			name: "invalid metrics path",
			cfg: ServerConfig{
				HTTPPort:        8080,
				RequestIDHeader: "X-Request-Id",
				Metrics: MetricsConfig{
					Path: "metrics",
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateServerConfig(&tt.cfg)
			if (len(errs) > 0) != tt.wantErr {
				t.Errorf("validateServerConfig() errors = %v, wantErr %v", errs, tt.wantErr)
			}
		})
	}
}

func TestValidateAuthConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     AuthConfig
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: AuthConfig{
				HeaderName:          "X-Service-Key",
				AuthorizationScheme: "ServiceKey",
				FailStatusCode:      401,
			},
			wantErr: false,
		},
		{
			name: "empty header name",
			cfg: AuthConfig{
				HeaderName:          "",
				AuthorizationScheme: "ServiceKey",
				FailStatusCode:      401,
			},
			wantErr: true,
		},
		{
			name: "invalid fail status code",
			cfg: AuthConfig{
				HeaderName:          "X-Service-Key",
				AuthorizationScheme: "ServiceKey",
				FailStatusCode:      500,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateAuthConfig(&tt.cfg)
			if (len(errs) > 0) != tt.wantErr {
				t.Errorf("validateAuthConfig() errors = %v, wantErr %v", errs, tt.wantErr)
			}
		})
	}
}

func TestValidateK8sConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     K8sConfig
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: K8sConfig{
				QPS:            50,
				Burst:          100,
				RequestTimeout: 15 * time.Second,
			},
			wantErr: false,
		},
		{
			name: "negative qps",
			cfg: K8sConfig{
				QPS: -1,
			},
			wantErr: true,
		},
		{
			name: "retry enabled with max backoff < base backoff",
			cfg: K8sConfig{
				Retry: K8sRetryConfig{
					Enabled:     true,
					MaxAttempts: 3,
					BaseBackoff: 2 * time.Second,
					MaxBackoff:  1 * time.Second,
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateK8sConfig(&tt.cfg)
			if (len(errs) > 0) != tt.wantErr {
				t.Errorf("validateK8sConfig() errors = %v, wantErr %v", errs, tt.wantErr)
			}
		})
	}
}

func TestValidateSandboxConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     SandboxConfig
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: SandboxConfig{
				Defaults: SandboxDefaults{
					Namespace:       "sandbox",
					RunnerImage:     "runner:1.0.0",
					ImagePullPolicy: "IfNotPresent",
					TTLSeconds:      900,
					PodReadyWait:    30 * time.Second,
					PodPollInterval: 500 * time.Millisecond,
					Workdir:         "/workspace",
					ContainerName:   "runner",
					Resources: ResourceRequirements{
						Requests: ResourceList{
							CPU:    "100m",
							Memory: "256Mi",
						},
						Limits: ResourceList{
							CPU:    "1",
							Memory: "1Gi",
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "empty namespace",
			cfg: SandboxConfig{
				Defaults: SandboxDefaults{
					Namespace: "",
				},
			},
			wantErr: true,
		},
		{
			name: "invalid image pull policy",
			cfg: SandboxConfig{
				Defaults: SandboxDefaults{
					Namespace:       "sandbox",
					RunnerImage:     "runner:1.0.0",
					ImagePullPolicy: "Invalid",
				},
			},
			wantErr: true,
		},
		{
			name: "relative workdir",
			cfg: SandboxConfig{
				Defaults: SandboxDefaults{
					Namespace:   "sandbox",
					RunnerImage: "runner:1.0.0",
					Workdir:     "workspace",
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateSandboxConfig(&tt.cfg)
			if (len(errs) > 0) != tt.wantErr {
				t.Errorf("validateSandboxConfig() errors = %v, wantErr %v", errs, tt.wantErr)
			}
		})
	}
}

func TestValidateExecConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ExecConfig
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: ExecConfig{
				DefaultTimeout:    30 * time.Second,
				MaxTimeout:        300 * time.Second,
				StdoutMaxBytes:    1 << 20,
				StderrMaxBytes:    1 << 20,
				PreserveTailBytes: 4096,
				ExitCodeMarker: ExitCodeMarker{
					Key:    "__EXIT__",
					Stream: "stderr",
				},
				Shell: ShellConfig{
					Bin: "sh",
				},
				Env: EnvConfig{
					AllowRegex: "^[A-Z_][A-Z0-9_]*$",
				},
				Workdir: WorkdirConfig{
					AllowedPrefixes: []string{"/workspace"},
				},
			},
			wantErr: false,
		},
		{
			name: "max timeout < default timeout",
			cfg: ExecConfig{
				DefaultTimeout: 300 * time.Second,
				MaxTimeout:     30 * time.Second,
			},
			wantErr: true,
		},
		{
			name: "invalid allow regex",
			cfg: ExecConfig{
				DefaultTimeout: 30 * time.Second,
				MaxTimeout:     300 * time.Second,
				Env: EnvConfig{
					AllowRegex: "[invalid(", // Invalid regex
				},
			},
			wantErr: true,
		},
		{
			name: "relative allowed prefix",
			cfg: ExecConfig{
				DefaultTimeout: 30 * time.Second,
				MaxTimeout:     300 * time.Second,
				Workdir: WorkdirConfig{
					AllowedPrefixes: []string{"workspace"},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateExecConfig(&tt.cfg)
			if (len(errs) > 0) != tt.wantErr {
				t.Errorf("validateExecConfig() errors = %v, wantErr %v", errs, tt.wantErr)
			}
		})
	}
}

func TestValidateFilesConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     FilesConfig
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: FilesConfig{
				RootPrefix: "/workspace",
				Upload: FileUploadConfig{
					DefaultDest: "/workspace",
					MaxBytes:    50 << 20,
					Format:      "tar.gz",
				},
				Download: FileDownloadConfig{
					DefaultSrc: "/workspace",
					Format:     "tar.gz",
				},
				Tar: TarConfig{
					Bin: "tar",
				},
			},
			wantErr: false,
		},
		{
			name: "relative root prefix",
			cfg: FilesConfig{
				RootPrefix: "workspace",
			},
			wantErr: true,
		},
		{
			name: "default dest not under root prefix",
			cfg: FilesConfig{
				RootPrefix: "/workspace",
				Upload: FileUploadConfig{
					DefaultDest: "/tmp",
				},
			},
			wantErr: true,
		},
		{
			name: "invalid upload format",
			cfg: FilesConfig{
				RootPrefix: "/workspace",
				Upload: FileUploadConfig{
					DefaultDest: "/workspace",
					Format:      "zip",
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateFilesConfig(&tt.cfg)
			if (len(errs) > 0) != tt.wantErr {
				t.Errorf("validateFilesConfig() errors = %v, wantErr %v", errs, tt.wantErr)
			}
		})
	}
}

func TestValidateEnvKey(t *testing.T) {
	cfg := DefaultConfig()

	tests := []struct {
		name string
		key  string
		want bool
	}{
		{"valid simple", "PATH", true},
		{"valid with numbers", "TEST123", true},
		{"valid with underscore", "TEST_VALUE", true},
		{"invalid lowercase", "test", false},
		{"invalid with dash", "TEST-VALUE", false},
		{"invalid starting with number", "123TEST", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cfg.ValidateEnvKey(tt.key)
			if got != tt.want {
				t.Errorf("ValidateEnvKey() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateWorkdir(t *testing.T) {
	cfg := DefaultConfig()

	tests := []struct {
		name    string
		workdir string
		want    bool
	}{
		{"valid workspace", "/workspace", true},
		{"valid subdirectory", "/workspace/subdir", true},
		{"invalid outside prefix", "/tmp", false},
		{"invalid relative", "workspace", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cfg.ValidateWorkdir(tt.workdir)
			if got != tt.want {
				t.Errorf("ValidateWorkdir() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateFilePath(t *testing.T) {
	cfg := DefaultConfig()

	tests := []struct {
		name string
		path string
		want bool
	}{
		{"valid in workspace", "/workspace/file.txt", true},
		{"valid subdirectory", "/workspace/subdir/file.txt", true},
		{"invalid outside root", "/tmp/file.txt", false},
		{"invalid relative", "file.txt", false},
		{"invalid parent escape", "/workspace/../tmp/file.txt", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cfg.ValidateFilePath(tt.path)
			if got != tt.want {
				t.Errorf("ValidateFilePath() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	result := cfg.Validate()
	if !result.Valid {
		t.Errorf("DefaultConfig() validation failed: %d errors", len(result.Errors))
	}
}

// TestQPSUpperBound tests QPS upper bound validation
func TestQPSUpperBound(t *testing.T) {
	tests := []struct {
		name    string
		qps     int
		wantErr bool
	}{
		{"valid qps 1000", 1000, false},
		{"valid qps 500", 500, false},
		{"invalid qps 1001", 1001, true},
		{"invalid qps 9999", 9999, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Kubernetes.QPS = tt.qps
			errs := validateK8sConfig(&cfg.Kubernetes)
			if (len(errs) > 0) != tt.wantErr {
				t.Errorf("validateK8sConfig() QPS=%d errors=%v, wantErr %v", tt.qps, errs, tt.wantErr)
			}
		})
	}
}

// TestBurstUpperBound tests Burst upper bound validation
func TestBurstUpperBound(t *testing.T) {
	tests := []struct {
		name    string
		burst   int
		wantErr bool
	}{
		{"valid burst 2000", 2000, false},
		{"valid burst 1000", 1000, false},
		{"invalid burst 2001", 2001, true},
		{"invalid burst 9999", 9999, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Kubernetes.Burst = tt.burst
			errs := validateK8sConfig(&cfg.Kubernetes)
			if (len(errs) > 0) != tt.wantErr {
				t.Errorf("validateK8sConfig() Burst=%d errors=%v, wantErr %v", tt.burst, errs, tt.wantErr)
			}
		})
	}
}

// TestConfigConcurrentValidation tests concurrent config validation
func TestConfigConcurrentValidation(t *testing.T) {
	cfg := DefaultConfig()

	// Run multiple validations concurrently
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			result := cfg.Validate()
			if !result.Valid {
				t.Errorf("Concurrent validation failed: %d errors", len(result.Errors))
			}
			done <- true
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}
}

// TestConfigEdgeCases tests edge case configurations
func TestConfigEdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*Config)
		wantErr bool
	}{
		{
			name: "zero qps",
			modify: func(c *Config) {
				c.Kubernetes.QPS = 0
			},
			wantErr: false, // 0 is valid (non-negative)
		},
		{
			name: "zero burst",
			modify: func(c *Config) {
				c.Kubernetes.Burst = 0
			},
			wantErr: false, // 0 is valid (non-negative)
		},
		{
			name: "minimum timeout",
			modify: func(c *Config) {
				c.Kubernetes.RequestTimeout = 0
			},
			wantErr: false, // 0 is valid (non-negative)
		},
		{
			name: "maximum timeout",
			modify: func(c *Config) {
				c.Kubernetes.RequestTimeout = 5 * time.Minute
			},
			wantErr: false,
		},
		{
			name: "negative timeout",
			modify: func(c *Config) {
				c.Kubernetes.RequestTimeout = -1 * time.Second
			},
			wantErr: true,
		},
		{
			name: "empty workdir",
			modify: func(c *Config) {
				c.Sandbox.Defaults.Workdir = ""
			},
			wantErr: true,
		},
		{
			name: "workdir with trailing slash",
			modify: func(c *Config) {
				c.Sandbox.Defaults.Workdir = "/workspace/"
			},
			wantErr: false, // trailing slash is valid
		},
		{
			name: "max ttl seconds",
			modify: func(c *Config) {
				c.Sandbox.Defaults.TTLSeconds = 86400
			},
			wantErr: false,
		},
		{
			name: "exceeds max ttl seconds",
			modify: func(c *Config) {
				c.Sandbox.Defaults.TTLSeconds = 86401
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tt.modify(cfg)
			result := cfg.Validate()
			if result.Valid == tt.wantErr {
				t.Errorf("Validate() Valid=%v, wantErr %v", result.Valid, tt.wantErr)
			}
		})
	}
}
