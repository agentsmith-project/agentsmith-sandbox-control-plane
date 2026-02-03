package session

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

func NewManager() *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
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
		return fmt.Errorf("session not: %s", agentThreadID)
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

type CreateRequest struct {
	AgentThreadID  string
	Image          string
	Command        []string
	Env            map[string]string
	PodNamespace   string
	Config         SecurityConfig
}
