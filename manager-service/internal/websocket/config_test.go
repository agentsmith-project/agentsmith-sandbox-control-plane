package websocket

import (
	"net/http"
	"testing"
	"time"
)

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *Config
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: &Config{
				ReadBufferSize:          1024,
				WriteBufferSize:         1024,
				AllowedOrigins:          []string{"http://localhost:3000"},
				AllowNonBrowserRequests: true,
				HandshakeTimeout:        10 * time.Second,
			},
			wantErr: false,
		},
		{
			name: "zero read buffer size",
			cfg: &Config{
				ReadBufferSize:          0,
				WriteBufferSize:         1024,
				AllowedOrigins:          []string{"http://localhost:3000"},
				AllowNonBrowserRequests: true,
			},
			wantErr: true,
		},
		{
			name: "zero write buffer size",
			cfg: &Config{
				ReadBufferSize:          1024,
				WriteBufferSize:         0,
				AllowedOrigins:          []string{"http://localhost:3000"},
				AllowNonBrowserRequests: true,
			},
			wantErr: true,
		},
		{
			name: "no allowed origins",
			cfg: &Config{
				ReadBufferSize:          1024,
				WriteBufferSize:         1024,
				AllowedOrigins:          []string{},
				AllowNonBrowserRequests: true,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Config.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConfig_Upgrader(t *testing.T) {
	cfg := &Config{
		ReadBufferSize:          1024,
		WriteBufferSize:         2048,
		AllowedOrigins:          []string{"http://localhost:3000", "https://example.com"},
		AllowNonBrowserRequests: true,
		HandshakeTimeout:        5 * time.Second,
	}

	upgrader := cfg.Upgrader()

	if upgrader.ReadBufferSize != 1024 {
		t.Errorf("Expected ReadBufferSize 1024, got %d", upgrader.ReadBufferSize)
	}
	if upgrader.WriteBufferSize != 2048 {
		t.Errorf("Expected WriteBufferSize 2048, got %d", upgrader.WriteBufferSize)
	}
	if upgrader.HandshakeTimeout != 5*time.Second {
		t.Errorf("Expected HandshakeTimeout 5s, got %v", upgrader.HandshakeTimeout)
	}
}

func TestConfig_UpgraderCheckOrigin(t *testing.T) {
	tests := []struct {
		name                    string
		cfg                     *Config
		origin                  string
		allowNonBrowserRequests bool
		want                    bool
	}{
		{
			name: "allowed origin",
			cfg: &Config{
				AllowedOrigins:          []string{"http://localhost:3000"},
				AllowNonBrowserRequests: false,
			},
			origin:                  "http://localhost:3000",
			allowNonBrowserRequests: false,
			want:                    true,
		},
		{
			name: "disallowed origin",
			cfg: &Config{
				AllowedOrigins:          []string{"http://localhost:3000"},
				AllowNonBrowserRequests: false,
			},
			origin:                  "https://evil.com",
			allowNonBrowserRequests: false,
			want:                    false,
		},
		{
			name: "no origin header, non-browser allowed",
			cfg: &Config{
				AllowedOrigins:          []string{"http://localhost:3000"},
				AllowNonBrowserRequests: true,
			},
			origin:                  "",
			allowNonBrowserRequests: true,
			want:                    true,
		},
		{
			name: "no origin header, non-browser not allowed",
			cfg: &Config{
				AllowedOrigins:          []string{"http://localhost:3000"},
				AllowNonBrowserRequests: false,
			},
			origin:                  "",
			allowNonBrowserRequests: false,
			want:                    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upgrader := tt.cfg.Upgrader()

			req, _ := http.NewRequest("GET", "/ws", nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}

			got := upgrader.CheckOrigin(req)
			if got != tt.want {
				t.Errorf("CheckOrigin() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.ReadBufferSize != 1024 {
		t.Errorf("Expected ReadBufferSize 1024, got %d", cfg.ReadBufferSize)
	}
	if cfg.WriteBufferSize != 1024 {
		t.Errorf("Expected WriteBufferSize 1024, got %d", cfg.WriteBufferSize)
	}
	if len(cfg.AllowedOrigins) != 1 || cfg.AllowedOrigins[0] != "http://localhost:3000" {
		t.Errorf("Expected AllowedOrigins [http://localhost:3000], got %v", cfg.AllowedOrigins)
	}
	if !cfg.AllowNonBrowserRequests {
		t.Error("Expected AllowNonBrowserRequests true")
	}
	if cfg.HandshakeTimeout != 10*time.Second {
		t.Errorf("Expected HandshakeTimeout 10s, got %v", cfg.HandshakeTimeout)
	}

	// Default config should validate
	if err := cfg.Validate(); err != nil {
		t.Errorf("DefaultConfig validation failed: %v", err)
	}
}
