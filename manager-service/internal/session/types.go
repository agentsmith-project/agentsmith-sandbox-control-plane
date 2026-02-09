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

// Initialized checks if the session has been properly initialized with required fields.
// A session is considered initialized if it has an AgentThreadID and a non-empty Config.
func (s *Session) Initialized() bool {
	return s.AgentThreadID != "" && s.CreatedAt.IsZero() == false
}

// Validate checks if the session is in a valid state.
// Returns an error if the session has invalid configuration or state.
func (s *Session) Validate() error {
	if s.AgentThreadID == "" {
		return fmt.Errorf("session: AgentThreadID is required")
	}
	if s.CreatedAt.IsZero() {
		return fmt.Errorf("session: CreatedAt is required")
	}
	if s.Config.MaxLifetime <= 0 {
		return fmt.Errorf("session: MaxLifetime must be positive")
	}
	return nil
}
