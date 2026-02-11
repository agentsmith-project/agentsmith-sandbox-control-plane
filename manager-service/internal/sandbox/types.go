package sandbox

import (
	"fmt"
	"sync"
	"time"
)

const (
	// DefaultMaxLifetime is the default maximum lifetime for a sandbox.
	// This is used when a sandbox is created via GetOrCreate without a CreateRequest.
	DefaultMaxLifetime = 24 * time.Hour
)

type State string

const (
	StateCreating  State = "creating"
	StateRestoring State = "restoring"
	StateReady     State = "ready"
	StateOffline   State = "offline"
)

// Sandbox represents a sandbox execution environment (Pod + workspace state)
type Sandbox struct {
	SandboxID        string
	PodName          string
	PodNamespace     string
	PodIP            string // IP of the pod for shell-bridge connection
	State            State
	Image            string
	Command          []string
	Env              map[string]string
	Config           SecurityConfig
	CreatedAt        time.Time
	LastActivityAt   time.Time
	ExpiresAt        time.Time
	ClientConnected  bool

	// BridgeConnection holds the shell bridge connection for this sandbox
	BridgeConnection interface{}
	// ConnectionMu provides thread-safe access to BridgeConnection
	ConnectionMu sync.RWMutex
}

type SecurityConfig struct {
	AllowNetworkAccess  bool
	ReadonlyFilesystem  bool
	CPULimit            string
	MemoryLimit         string
	IdleTimeout         time.Duration
	MaxLifetime         time.Duration
	DropAllCapabilities bool
	AllowPrivileged     bool
}

// IsExpired checks if the sandbox has expired based on max lifetime or idle timeout
func (s *Sandbox) IsExpired() bool {
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

// GetExpiresAt returns the expiration time for this sandbox
func (s *Sandbox) GetExpiresAt() time.Time {
	maxExpiry := s.CreatedAt.Add(s.Config.MaxLifetime)

	if !s.ClientConnected && s.Config.IdleTimeout > 0 {
		idleExpiry := s.LastActivityAt.Add(s.Config.IdleTimeout)
		if idleExpiry.Before(maxExpiry) {
			return idleExpiry
		}
	}

	return maxExpiry
}

// Initialized checks if the sandbox has been properly initialized with required fields
func (s *Sandbox) Initialized() bool {
	return s.SandboxID != "" && !s.CreatedAt.IsZero()
}

// Validate checks if the sandbox is in a valid state
func (s *Sandbox) Validate() error {
	if s.SandboxID == "" {
		return fmt.Errorf("sandbox: SandboxID is required")
	}
	if s.CreatedAt.IsZero() {
		return fmt.Errorf("sandbox: CreatedAt is required")
	}
	if s.Config.MaxLifetime <= 0 {
		return fmt.Errorf("sandbox: MaxLifetime must be positive")
	}
	return nil
}
