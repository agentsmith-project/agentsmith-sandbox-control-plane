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
	// StateCreating is the initial state when a session is being created
	StateCreating State = "creating"
	// StateRestoring is when a session is being restored from a snapshot
	StateRestoring State = "restoring"
	// StateReady is when the session is ready for use
	StateReady State = "ready"
	// StateOffline is when the session is disconnected but may be reconnected
	StateOffline State = "offline"
	// StateTerminating is when the session is being terminated
	StateTerminating State = "terminating"
	// StateTerminated is when the session has been terminated
	StateTerminated State = "terminated"
	// StateFailed is when the session has failed
	StateFailed State = "failed"
	// StateSnapshotting is when a snapshot is being created
	StateSnapshotting State = "snapshotting"
	// StateSnapshotFailed is when snapshot creation has failed
	StateSnapshotFailed State = "snapshot_failed"
)

type Session struct {
	AgentThreadID     string
	PodName           string
	PodNamespace      string
	State             State
	stateMachine      *StateMachine // State machine for managing state transitions
	Image             string
	Command           []string
	Env               map[string]string
	Config            SecurityConfig
	CreatedAt         time.Time
	LastActivityAt    time.Time
	ExpiresAt         time.Time
	ClientConnected   bool
	OwnerID           string // Track which user owns this session
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
