package config

import (
	"os"
	"path/filepath"
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
	validBase := ServerConfig{
		HTTPPort:        8080,
		RequestIDHeader: "X-Request-Id",
		Timeouts:        ServerTimeouts{ReadHeader: 5 * time.Second, Read: 30 * time.Second, Write: 60 * time.Second, Idle: 120 * time.Second},
		MaxHeaderBytes:  1 << 20,
		Metrics:         MetricsConfig{Path: "/metrics"},
		Debug:           DebugConfig{ConfigPath: "/debug/config"},
	}
	tests := []struct {
		name    string
		cfg     ServerConfig
		wantErr bool
	}{
		{
			name:    "valid config",
			cfg:     validBase,
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
			name: "port over 65535",
			cfg: ServerConfig{
				HTTPPort:        65536,
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
				Metrics:         MetricsConfig{Path: "metrics"},
			},
			wantErr: true,
		},
		{
			name: "negative readHeader",
			cfg: func() ServerConfig {
				c := validBase
				c.Timeouts.ReadHeader = -1
				return c
			}(),
			wantErr: true,
		},
		{
			name: "negative read timeout",
			cfg: func() ServerConfig {
				c := validBase
				c.Timeouts.Read = -time.Second
				return c
			}(),
			wantErr: true,
		},
		{
			name: "negative maxHeaderBytes",
			cfg: func() ServerConfig {
				c := validBase
				c.MaxHeaderBytes = -1
				return c
			}(),
			wantErr: true,
		},
		{
			name: "debug configPath not starting with /",
			cfg: func() ServerConfig {
				c := validBase
				c.Debug.ConfigPath = "relative"
				return c
			}(),
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
				HeaderName: "X-Service-Key",
			},
			wantErr: false,
		},
		{
			name: "empty header name",
			cfg: AuthConfig{
				HeaderName: "",
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
			name: "negative burst",
			cfg: K8sConfig{
				QPS:   50,
				Burst: -1,
			},
			wantErr: true,
		},
		{
			name: "negative requestTimeout",
			cfg: K8sConfig{
				QPS:            50,
				Burst:          100,
				RequestTimeout: -time.Second,
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

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	result := cfg.Validate()
	if !result.Valid {
		t.Errorf("DefaultConfig() validation failed: %d errors", len(result.Errors))
	}
}

func TestValidate_ValidConfig(t *testing.T) {
	cfg := &Config{
		Version: 1,
		Server: ServerConfig{
			HTTPPort:        8080,
			RequestIDHeader: "X-Request-Id",
			Timeouts:        ServerTimeouts{ReadHeader: 5 * time.Second, Read: 30 * time.Second, Write: 60 * time.Second, Idle: 120 * time.Second},
			MaxHeaderBytes:  1 << 20,
			Metrics:         MetricsConfig{Path: "/metrics"},
			Debug:           DebugConfig{ConfigPath: "/debug/config"},
		},
		Auth:       AuthConfig{HeaderName: "X-Service-Key"},
		Kubernetes: K8sConfig{QPS: 50, Burst: 100, RequestTimeout: 15 * time.Second},
	}
	result := cfg.Validate()
	if !result.Valid {
		t.Errorf("Validate() valid config: Valid = false, errors = %v", result.Errors)
	}
	if len(result.Errors) != 0 {
		t.Errorf("Validate() valid config: got %d errors", len(result.Errors))
	}
}

func TestValidate_InvalidCombined(t *testing.T) {
	cfg := &Config{
		Version:    0,
		Server:     ServerConfig{RequestIDHeader: ""},
		Auth:       AuthConfig{HeaderName: ""},
		Kubernetes: K8sConfig{QPS: -1},
	}
	result := cfg.Validate()
	if result.Valid {
		t.Error("Validate() invalid config: Valid = true, want false")
	}
	if len(result.Errors) < 2 {
		t.Errorf("Validate() invalid config: got %d errors, want at least 2", len(result.Errors))
	}
}

func TestCheckFileExists_NotExists(t *testing.T) {
	if CheckFileExists(filepath.Join(t.TempDir(), "nonexistent")) {
		t.Error("CheckFileExists(nonexistent) = true, want false")
	}
}

func TestCheckFileExists_Exists(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(f, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if !CheckFileExists(f) {
		t.Error("CheckFileExists(existing file) = false, want true")
	}
}

func TestCheckFileExists_DirReturnsFalse(t *testing.T) {
	dir := t.TempDir()
	if CheckFileExists(dir) {
		t.Error("CheckFileExists(dir) = true, want false (must be file)")
	}
}
