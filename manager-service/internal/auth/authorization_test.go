package auth_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sandbox/manager/internal/auth"
	"github.com/sandbox/manager/internal/session"
)

// mockSessionManager is a mock implementation of session.Manager for testing
type mockSessionManager struct {
	sessions map[string]*session.Session
}

func (m *mockSessionManager) Create(ctx context.Context, req session.CreateRequest) (*session.Session, error) {
	sess := &session.Session{
		AgentThreadID: req.AgentThreadID,
		Image:         req.Image,
		Command:       req.Command,
		Env:           req.Env,
		Config:        req.Config,
		State:         session.StateCreating,
		PodNamespace:  req.PodNamespace,
		OwnerID:       req.OwnerID,
	}
	m.sessions[req.AgentThreadID] = sess
	return sess, nil
}

func (m *mockSessionManager) Get(agentThreadID string) (*session.Session, bool) {
	sess, ok := m.sessions[agentThreadID]
	return sess, ok
}

func (m *mockSessionManager) GetByPodName(podName string) (*session.Session, bool) {
	for _, sess := range m.sessions {
		if sess.PodName == podName {
			return sess, true
		}
	}
	return nil, false
}

func (m *mockSessionManager) GetOrCreate(agentThreadID string) (*session.Session, bool, error) {
	if sess, exists := m.sessions[agentThreadID]; exists {
		return sess, false, nil
	}
	sess := &session.Session{
		AgentThreadID: agentThreadID,
		State:         session.StateCreating,
	}
	m.sessions[agentThreadID] = sess
	return sess, true, nil
}

func (m *mockSessionManager) UpdateState(agentThreadID string, state session.State) error {
	if sess, ok := m.sessions[agentThreadID]; ok {
		sess.State = state
		return nil
	}
	return nil
}

func (m *mockSessionManager) SetPodInfo(agentThreadID, podName string) error {
	if sess, ok := m.sessions[agentThreadID]; ok {
		sess.PodName = podName
		return nil
	}
	return nil
}

func (m *mockSessionManager) MarkClientConnected(agentThreadID string) error {
	if sess, ok := m.sessions[agentThreadID]; ok {
		sess.ClientConnected = true
		return nil
	}
	return nil
}

func (m *mockSessionManager) MarkClientDisconnected(agentThreadID string) error {
	if sess, ok := m.sessions[agentThreadID]; ok {
		sess.ClientConnected = false
		return nil
	}
	return nil
}

func (m *mockSessionManager) UpdateActivity(agentThreadID string) error {
	return nil
}

func (m *mockSessionManager) Delete(agentThreadID string) {
	delete(m.sessions, agentThreadID)
}

func (m *mockSessionManager) ListByOwner(ownerID string) []*session.Session {
	var sessions []*session.Session
	for _, sess := range m.sessions {
		if sess.OwnerID == ownerID {
			sessions = append(sessions, sess)
		}
	}
	return sessions
}

func (m *mockSessionManager) GetSessionCount() int {
	return len(m.sessions)
}

func (m *mockSessionManager) StartCleanup(ctx context.Context, interval interface{}) {
}

func (m *mockSessionManager) Shutdown() {
}

func TestAuthorizer_VerifySessionAccess_Owner(t *testing.T) {
	sessionMgr := &mockSessionManager{
		sessions: map[string]*session.Session{
			"session-123": {
				AgentThreadID: "session-123",
				OwnerID:       "user-123",
			},
		},
	}

	authorizer := auth.NewAuthorizer(sessionMgr, nil)
	userCtx := &auth.UserContext{UserID: "user-123"}

	err := authorizer.VerifySessionAccess(context.Background(), userCtx, "session-123")
	assert.NoError(t, err)
}

func TestAuthorizer_VerifySessionAccess_NotOwner(t *testing.T) {
	sessionMgr := &mockSessionManager{
		sessions: map[string]*session.Session{
			"session-123": {
				AgentThreadID: "session-123",
				OwnerID:       "user-123",
			},
		},
	}

	authorizer := auth.NewAuthorizer(sessionMgr, nil)
	userCtx := &auth.UserContext{UserID: "user-456"}

	err := authorizer.VerifySessionAccess(context.Background(), userCtx, "session-123")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not authorized")
}

func TestAuthorizer_VerifySessionAccess_NotFound(t *testing.T) {
	sessionMgr := &mockSessionManager{
		sessions: map[string]*session.Session{},
	}

	authorizer := auth.NewAuthorizer(sessionMgr, nil)
	userCtx := &auth.UserContext{UserID: "user-123"}

	err := authorizer.VerifySessionAccess(context.Background(), userCtx, "session-123")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestAuthorizer_CheckSessionQuota(t *testing.T) {
	t.Run("within_quota", func(t *testing.T) {
		sessionMgr := &mockSessionManager{
			sessions: map[string]*session.Session{
				"session-1": {AgentThreadID: "session-1", OwnerID: "user-123"},
				"session-2": {AgentThreadID: "session-2", OwnerID: "user-123"},
			},
		}

		authorizer := auth.NewAuthorizer(sessionMgr, nil)
		userCtx := &auth.UserContext{UserID: "user-123"}

		err := authorizer.CheckSessionQuota(context.Background(), userCtx, 5)
		assert.NoError(t, err)
	})

	t.Run("at_quota_limit", func(t *testing.T) {
		sessionMgr := &mockSessionManager{
			sessions: map[string]*session.Session{
				"session-1": {AgentThreadID: "session-1", OwnerID: "user-123"},
				"session-2": {AgentThreadID: "session-2", OwnerID: "user-123"},
				"session-3": {AgentThreadID: "session-3", OwnerID: "user-123"},
			},
		}

		authorizer := auth.NewAuthorizer(sessionMgr, nil)
		userCtx := &auth.UserContext{UserID: "user-123"}

		err := authorizer.CheckSessionQuota(context.Background(), userCtx, 3)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "maximum session limit")
	})

	t.Run("exceeds_quota", func(t *testing.T) {
		sessionMgr := &mockSessionManager{
			sessions: map[string]*session.Session{
				"session-1": {AgentThreadID: "session-1", OwnerID: "user-123"},
				"session-2": {AgentThreadID: "session-2", OwnerID: "user-123"},
				"session-3": {AgentThreadID: "session-3", OwnerID: "user-123"},
			},
		}

		authorizer := auth.NewAuthorizer(sessionMgr, nil)
		userCtx := &auth.UserContext{UserID: "user-123"}

		err := authorizer.CheckSessionQuota(context.Background(), userCtx, 2)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "maximum session limit")
	})
}

func TestAuthorizer_ListByOwner(t *testing.T) {
	sessionMgr := &mockSessionManager{
		sessions: map[string]*session.Session{
			"session-1": {AgentThreadID: "session-1", OwnerID: "user-123"},
			"session-2": {AgentThreadID: "session-2", OwnerID: "user-123"},
			"session-3": {AgentThreadID: "session-3", OwnerID: "user-456"},
		},
	}

	t.Run("lists_user_sessions", func(t *testing.T) {
		sessions := sessionMgr.ListByOwner("user-123")
		require.Len(t, sessions, 2)

		ids := make([]string, 0, 2)
		for _, sess := range sessions {
			ids = append(ids, sess.AgentThreadID)
		}
		assert.Contains(t, ids, "session-1")
		assert.Contains(t, ids, "session-2")
	})

	t.Run("empty_for_nonexistent_user", func(t *testing.T) {
		sessions := sessionMgr.ListByOwner("user-999")
		assert.Empty(t, sessions)
	})
}
