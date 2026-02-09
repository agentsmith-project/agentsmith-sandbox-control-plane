package session

import (
	"time"
)

const (
	// DefaultMaxLifetime is the default maximum lifetime for a session.
	// This is used when a session is created via GetOrCreate without a CreateRequest.
	DefaultMaxLifetime = 24 * time.Hour
)

type State string

const (
	StateCreating    State = "creating"
	StateRestoring   State = "restoring"
	StateReady       State = "ready"
	StateOffline     State = "offline"
)

type Session struct {
	AgentThreadID     string
	PodName           string
	PodNamespace      string
	PodIP             string // IP of the pod for shell-bridge connection
	State             State
	Image             string
	Command           []string
	Env               map[string]string
	Config            SecurityConfig
	CreatedAt         time.Time
	LastActivityAt    time.Time
	ExpiresAt         time.Time
	ClientConnected   bool
}

type SecurityConfig struct {
	AllowNetworkAccess    bool
	ReadonlyFilesystem    bool
	CPULimit              string
	MemoryLimit           string
	IdleTimeout           time.Duration
	MaxLifetime           time.Duration
	DropAllCapabilities   bool
	AllowPrivileged       bool
}

func (s *Session) IsExpired() bool {
	// Check max lifetime
	if time.Since(s.CreatedAt) > s.Config.MaxLifetime {
		return true
	}

	// Check idle timeout (only when disconnected)
	if !s.ClientConnected && s.Config.IdleTimeout > 0 {
		idleTime := time.Since(s.LastActivityAt)
		return idleTime > s.Config.IdleTimeout
	}

	return false
}

func (s *Session) GetExpiresAt() time.Time {
	maxExpiry := s.CreatedAt.Add(s.Config.MaxLifetime)

	if !s.ClientConnected && s.Config.IdleTimeout > 0 {
		idleExpiry := s.LastActivityAt.Add(s.Config.IdleTimeout)
		if idleExpiry.Before(maxExpiry) {
			return idleExpiry
		}
	}

	return maxExpiry
}
