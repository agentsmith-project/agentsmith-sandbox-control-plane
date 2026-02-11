package sandbox

import (
	"testing"
	"time"
)

func TestSandbox_IsExpired_MaxLifetime(t *testing.T) {
	sandbox := &Sandbox{
		SandboxID: "test-123",
		CreatedAt: time.Now().Add(-25 * time.Hour),
		Config: SecurityConfig{
			MaxLifetime: 24 * time.Hour,
		},
		ClientConnected: true,
	}
	if !sandbox.IsExpired() {
		t.Error("expected sandbox to be expired due to max lifetime")
	}
}

func TestSandbox_IsExpired_IdleTimeout(t *testing.T) {
	sandbox := &Sandbox{
		SandboxID: "test-123",
		CreatedAt: time.Now(),
		LastActivityAt: time.Now().Add(-31 * time.Minute),
		Config: SecurityConfig{
			MaxLifetime: 24 * time.Hour,
			IdleTimeout: 30 * time.Minute,
		},
		ClientConnected: false,
	}
	if !sandbox.IsExpired() {
		t.Error("expected sandbox to be expired due to idle timeout")
	}
}

func TestSandbox_GetExpiresAt(t *testing.T) {
	sandbox := &Sandbox{
		SandboxID: "test-123",
		CreatedAt: time.Now(),
		LastActivityAt: time.Now().Add(-10 * time.Minute),
		Config: SecurityConfig{
			MaxLifetime: 24 * time.Hour,
			IdleTimeout: 30 * time.Minute,
		},
		ClientConnected: false,
	}
	expiresAt := sandbox.GetExpiresAt()
	expectedIdleExpiry := sandbox.LastActivityAt.Add(30 * time.Minute)
	diff := expiresAt.Sub(expectedIdleExpiry)
	if diff > time.Second || diff < -time.Second {
		t.Errorf("expected expiry to be idle expiry, got %v", expiresAt)
	}
}

func TestSandbox_Validate(t *testing.T) {
	tests := []struct {
		name    string
		sandbox *Sandbox
		wantErr bool
	}{
		{
			name: "valid sandbox",
			sandbox: &Sandbox{
				SandboxID: "test-123",
				CreatedAt: time.Now(),
				Config: SecurityConfig{
					MaxLifetime: 24 * time.Hour,
				},
			},
			wantErr: false,
		},
		{
			name: "missing SandboxID",
			sandbox: &Sandbox{
				CreatedAt: time.Now(),
				Config: SecurityConfig{
					MaxLifetime: 24 * time.Hour,
				},
			},
			wantErr: true,
		},
		{
			name: "missing CreatedAt",
			sandbox: &Sandbox{
				SandboxID: "test-123",
				Config: SecurityConfig{
					MaxLifetime: 24 * time.Hour,
				},
			},
			wantErr: true,
		},
		{
			name: "invalid MaxLifetime",
			sandbox: &Sandbox{
				SandboxID: "test-123",
				CreatedAt: time.Now(),
				Config: SecurityConfig{
					MaxLifetime: -1 * time.Hour,
				},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.sandbox.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
