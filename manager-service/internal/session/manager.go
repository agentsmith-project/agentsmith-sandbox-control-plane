package session

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sandbox/manager/internal/observability"
)

type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	wg       sync.WaitGroup    // WaitGroup for cleanup goroutine
	logger   observability.Logger
}

func NewManager() *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
		logger:   observability.GetLogger(),
	}
}

func (m *Manager) Create(ctx context.Context, req CreateRequest) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()

	sess := &Session{
		AgentThreadID:   req.AgentThreadID,
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
		OwnerID:         req.OwnerID,
	}

	m.sessions[req.AgentThreadID] = sess
	return sess, nil
}

func (m *Manager) Get(agentThreadID string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[agentThreadID]
	return s, ok
}

// GetOrCreate atomically gets an existing session or creates a new one.
// Returns the session, a boolean indicating whether the session was created,
// and an error if creation fails.
// This method prevents race conditions where multiple goroutines might
// simultaneously check for a session's existence and both create duplicates.
//
// IMPORTANT: When creating a new session, GetOrCreate creates a minimal placeholder
// session with only the following fields initialized:
//   - AgentThreadID: Set to the provided ID
//   - State: Set to StateCreating
//   - CreatedAt, LastActivityAt: Set to current time
//   - ExpiresAt: Set to DefaultMaxLifetime (24 hours)
//   - Config.MaxLifetime: Set to DefaultMaxLifetime
//   - ClientConnected: Set to false
//
// The following fields are NOT initialized (placeholder/zombie session):
//   - Image: Empty string
//   - Command: nil
//   - Env: nil
//   - PodNamespace: Empty string
//   - PodName: Empty string
//   - Config.IdleTimeout: Zero value (no idle timeout)
//   - Config.AllowNetworkAccess, ReadonlyFilesystem, etc.: All false/zero
//
// If you need a fully-initialized session, use Create() with a CreateRequest instead.
// GetOrCreate is designed for scenarios where you need to prevent duplicate session
// creation while the full initialization happens asynchronously (e.g., during WebSocket
// connection establishment).
func (m *Manager) GetOrCreate(agentThreadID string) (*Session, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if session already exists (while holding the lock)
	if sess, exists := m.sessions[agentThreadID]; exists {
		return sess, false, nil
	}

	// Create new session (still holding the lock - atomic operation)
	now := time.Now()
	sess := &Session{
		AgentThreadID:   agentThreadID,
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

	m.sessions[agentThreadID] = sess
	return sess, true, nil
}

func (m *Manager) UpdateState(agentThreadID string, state State) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[agentThreadID]
	if !ok {
		return fmt.Errorf("session not found: %s", agentThreadID)
	}

	s.State = state
	return nil
}

func (m *Manager) SetPodInfo(agentThreadID, podName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[agentThreadID]
	if !ok {
		return fmt.Errorf("session not found: %s", agentThreadID)
	}

	s.PodName = podName
	return nil
}

func (m *Manager) MarkClientConnected(agentThreadID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[agentThreadID]
	if !ok {
		return fmt.Errorf("session not found: %s", agentThreadID)
	}

	now := time.Now()
	s.ClientConnected = true
	s.LastActivityAt = now
	s.ExpiresAt = s.GetExpiresAt()
	return nil
}

func (m *Manager) MarkClientDisconnected(agentThreadID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[agentThreadID]
	if !ok {
		return fmt.Errorf("session not found: %s", agentThreadID)
	}

	s.ClientConnected = false
	s.ExpiresAt = s.GetExpiresAt()
	return nil
}

func (m *Manager) UpdateActivity(agentThreadID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[agentThreadID]
	if !ok {
		return fmt.Errorf("session not found: %s", agentThreadID)
	}

	s.LastActivityAt = time.Now()
	s.ExpiresAt = s.GetExpiresAt()
	return nil
}

func (m *Manager) Delete(agentThreadID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, agentThreadID)
}

// StartCleanup starts a background goroutine that periodically cleans up expired sessions.
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

// cleanupExpired removes all expired sessions from the manager.
func (m *Manager) cleanupExpired() {
	m.mu.Lock()
	defer m.mu.Unlock()

	var deleted []string

	for id, sess := range m.sessions {
		// Check if session is expired
		if sess.IsExpired() {
			delete(m.sessions, id)
			deleted = append(deleted, id)
		}
	}

	if len(deleted) > 0 {
		m.logger.Info("Cleaned up %d expired sessions: %v", len(deleted), deleted)
	}
}

// Shutdown waits for the cleanup goroutine to finish.
// The context passed to StartCleanup should be cancelled before calling this method.
func (m *Manager) Shutdown() {
	m.wg.Wait()
}

// GetSessionCount returns the current number of sessions (for testing/metrics)
func (m *Manager) GetSessionCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}

// ListByOwner returns all sessions owned by a specific user
func (m *Manager) ListByOwner(ownerID string) []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var sessions []*Session
	for _, sess := range m.sessions {
		if sess.OwnerID == ownerID {
			sessions = append(sessions, sess)
		}
	}
	return sessions
}

// GetByPodName finds a session by its pod name
func (m *Manager) GetByPodName(podName string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, sess := range m.sessions {
		if sess.PodName == podName {
			return sess, true
		}
	}
	return nil, false
}

type CreateRequest struct {
	AgentThreadID  string
	Image          string
	Command        []string
	Env            map[string]string
	PodNamespace   string
	Config         SecurityConfig
	OwnerID        string // Owner ID from auth context
}
