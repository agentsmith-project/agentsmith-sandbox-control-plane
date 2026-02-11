package sandbox

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sandbox/manager/internal/observability"
)

type Manager struct {
	mu       sync.RWMutex
	sandboxes map[string]*Sandbox
	wg       sync.WaitGroup // WaitGroup for cleanup goroutine
	logger   observability.Logger
}

func NewManager() *Manager {
	return &Manager{
		sandboxes: make(map[string]*Sandbox),
		logger:    observability.GetLogger(),
	}
}

func (m *Manager) Create(ctx context.Context, req CreateRequest) (*Sandbox, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if sandbox already exists to prevent duplicates
	// This prevents race conditions when Create() and GetOrCreate() are called concurrently
	if existing, exists := m.sandboxes[req.SandboxID]; exists {
		return existing, nil
	}

	now := time.Now()

	sbox := &Sandbox{
		SandboxID:       req.SandboxID,
		Image:           req.Image,
		Command:         req.Command,
		Env:             req.Env,
		Config:          req.Config,
		State:           StateCreating,
		PodNamespace:    req.PodNamespace,
		CreatedAt:       now,
		LastActivityAt:  now,
		ExpiresAt:       now.Add(req.Config.MaxLifetime),
		ClientConnected: false,
	}

	m.sandboxes[req.SandboxID] = sbox
	return sbox, nil
}

func (m *Manager) Get(sandboxID string) (*Sandbox, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sandboxes[sandboxID]
	return s, ok
}

// GetOrCreate atomically gets an existing sandbox or creates a new one.
// Returns the sandbox, a boolean indicating whether the sandbox was created,
// and an error if creation fails.
// This method prevents race conditions where multiple goroutines might
// simultaneously check for a sandbox's existence and both create duplicates.
//
// IMPORTANT: When creating a new sandbox, GetOrCreate creates a minimal placeholder
// sandbox with only the following fields initialized:
//   - SandboxID: Set to the provided ID
//   - State: Set to StateCreating
//   - CreatedAt, LastActivityAt: Set to current time
//   - ExpiresAt: Set to DefaultMaxLifetime (24 hours)
//   - Config.MaxLifetime: Set to DefaultMaxLifetime
//   - ClientConnected: Set to false
//
// The following fields are NOT initialized (placeholder/zombie sandbox):
//   - Image: Empty string
//   - Command: nil
//   - Env: nil
//   - PodNamespace: Empty string
//   - PodName: Empty string
//   - Config.IdleTimeout: Zero value (no idle timeout)
//   - Config.AllowNetworkAccess, ReadonlyFilesystem, etc.: All false/zero
//
// If you need a fully-initialized sandbox, use Create() with a CreateRequest instead.
// GetOrCreate is designed for scenarios where you need to prevent duplicate sandbox
// creation while the full initialization happens asynchronously (e.g., during WebSocket
// connection establishment).
func (m *Manager) GetOrCreate(sandboxID string) (*Sandbox, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if sandbox already exists (while holding the lock)
	if sbox, exists := m.sandboxes[sandboxID]; exists {
		return sbox, false, nil
	}

	// Create new sandbox (still holding the lock - atomic operation)
	now := time.Now()
	sbox := &Sandbox{
		SandboxID:       sandboxID,
		State:           StateCreating,
		CreatedAt:       now,
		LastActivityAt:  now,
		ClientConnected: false,
		// Note: Config must be set separately or pass in a CreateRequest
		Config: SecurityConfig{
			MaxLifetime: DefaultMaxLifetime,
		},
		ExpiresAt: now.Add(DefaultMaxLifetime),
	}

	m.sandboxes[sandboxID] = sbox
	return sbox, true, nil
}

func (m *Manager) UpdateState(sandboxID string, state State) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sandboxes[sandboxID]
	if !ok {
		return fmt.Errorf("sandbox not found: %s", sandboxID)
	}

	s.State = state
	return nil
}

func (m *Manager) SetPodInfo(sandboxID, podName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sandboxes[sandboxID]
	if !ok {
		return fmt.Errorf("sandbox not found: %s", sandboxID)
	}

	s.PodName = podName
	return nil
}

// SetPodIP sets the pod IP for a sandbox (used for shell-bridge connection)
func (m *Manager) SetPodIP(sandboxID, podIP string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sandboxes[sandboxID]
	if !ok {
		return fmt.Errorf("sandbox not found: %s", sandboxID)
	}

	s.PodIP = podIP
	return nil
}

func (m *Manager) MarkClientConnected(sandboxID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sandboxes[sandboxID]
	if !ok {
		return fmt.Errorf("sandbox not found: %s", sandboxID)
	}

	now := time.Now()
	s.ClientConnected = true
	s.LastActivityAt = now
	s.ExpiresAt = s.GetExpiresAt()
	return nil
}

func (m *Manager) MarkClientDisconnected(sandboxID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sandboxes[sandboxID]
	if !ok {
		return fmt.Errorf("sandbox not found: %s", sandboxID)
	}

	s.ClientConnected = false
	s.ExpiresAt = s.GetExpiresAt()
	return nil
}

func (m *Manager) UpdateActivity(sandboxID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sandboxes[sandboxID]
	if !ok {
		return fmt.Errorf("sandbox not found: %s", sandboxID)
	}

	s.LastActivityAt = time.Now()
	s.ExpiresAt = s.GetExpiresAt()
	return nil
}

func (m *Manager) Delete(sandboxID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sandboxes, sandboxID)
}

// StartCleanup starts a background goroutine that periodically cleans up expired sandboxes.
// The cleanup runs on the given interval until the context is cancelled.
func (m *Manager) StartCleanup(ctx context.Context, interval time.Duration) {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				m.logger.Info("Cleanup goroutine stopped")
				return
			case <-ticker.C:
				m.cleanupExpired()
			}
		}
	}()
}

// cleanupExpired removes all expired sandboxes from the manager.
func (m *Manager) cleanupExpired() {
	m.mu.Lock()
	defer m.mu.Unlock()

	var deleted []string

	for id, sbox := range m.sandboxes {
		// Check if sandbox is expired
		if sbox.IsExpired() {
			delete(m.sandboxes, id)
			deleted = append(deleted, id)
		}
	}

	if len(deleted) > 0 {
		m.logger.Info("Cleaned up %d expired sandboxes: %v", len(deleted), deleted)
	}
}

// Shutdown waits for the cleanup goroutine to finish.
// The context passed to StartCleanup should be cancelled before calling this method.
func (m *Manager) Shutdown() {
	m.wg.Wait()
}

// GetSandboxCount returns the current number of sandboxes (for testing/metrics)
func (m *Manager) GetSandboxCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sandboxes)
}

type CreateRequest struct {
	SandboxID    string
	Image        string
	Command      []string
	Env          map[string]string
	PodNamespace string
	Config       SecurityConfig
}
