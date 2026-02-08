package auth

import (
	"context"
	"fmt"

	"github.com/sandbox/manager/internal/k8s"
	"github.com/sandbox/manager/internal/session"
)

// SessionManager defines the interface for session management needed by the authorizer
type SessionManager interface {
	Get(agentThreadID string) (*session.Session, bool)
	GetByPodName(podName string) (*session.Session, bool)
	ListByOwner(ownerID string) []*session.Session
}

// Authorizer handles authorization checks for session access
type Authorizer struct {
	sessionManager SessionManager
	k8sClient      *k8s.Client
}

// NewAuthorizer creates a new Authorizer instance
func NewAuthorizer(sessionMgr SessionManager, k8sClient *k8s.Client) *Authorizer {
	return &Authorizer{
		sessionManager: sessionMgr,
		k8sClient:      k8sClient,
	}
}

// VerifySessionAccess checks if the user is allowed to access this session
func (a *Authorizer) VerifySessionAccess(ctx context.Context, userCtx *UserContext, agentThreadID string) error {
	sess, ok := a.sessionManager.Get(agentThreadID)
	if !ok {
		return fmt.Errorf("session not found: %s", agentThreadID)
	}

	if sess.OwnerID != userCtx.UserID {
		return fmt.Errorf("user %s not authorized to access session %s (owned by %s)",
			userCtx.UserID, agentThreadID, sess.OwnerID)
	}

	return nil
}

// CheckSessionQuota verifies user hasn't exceeded max sessions
func (a *Authorizer) CheckSessionQuota(ctx context.Context, userCtx *UserContext, maxSessions int) error {
	sessions := a.sessionManager.ListByOwner(userCtx.UserID)
	if len(sessions) >= maxSessions {
		return fmt.Errorf("user %s has reached maximum session limit (%d)",
			userCtx.UserID, maxSessions)
	}
	return nil
}

// VerifyPodAccess checks if user can access the specified pod
func (a *Authorizer) VerifyPodAccess(ctx context.Context, userCtx *UserContext, podName string) error {
	sess, ok := a.sessionManager.GetByPodName(podName)
	if !ok {
		return fmt.Errorf("pod not found: %s", podName)
	}

	if sess.OwnerID != userCtx.UserID {
		return fmt.Errorf("user %s not authorized to access pod %s (owned by %s)",
			userCtx.UserID, podName, sess.OwnerID)
	}

	return nil
}
