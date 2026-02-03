package session

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManager_Create(t *testing.T) {
	ctx := context.Background()
	m := NewManager()

	req := CreateRequest{
		AgentThreadID: "at_test123",
		Image:         "test:latest",
		PodNamespace:  "sandbox",
		Config: SecurityConfig{
			MaxLifetime: 24 * time.Hour,
			IdleTimeout: 30 * time.Minute,
		},
	}

	sess, err := m.Create(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, "at_test123", sess.AgentThreadID)
	assert.Equal(t, StateCreating, sess.State)
	assert.False(t, sess.ClientConnected)
}

func TestManager_Get(t *testing.T) {
	ctx := context.Background()
	m := NewManager()

	sess, _ := m.Create(ctx, CreateRequest{
		AgentThreadID: "at_test",
		Image:         "test:latest",
		PodNamespace:  "sandbox",
		Config:        SecurityConfig{MaxLifetime: time.Hour},
	})

	got, ok := m.Get(sess.AgentThreadID)
	assert.True(t, ok)
	assert.Equal(t, sess.AgentThreadID, got.AgentThreadID)

	_, ok = m.Get("nonexistent")
	assert.False(t, ok)
}

func TestManager_UpdateState(t *testing.T) {
	ctx := context.Background()
	m := NewManager()

	sess, _ := m.Create(ctx, CreateRequest{
		AgentThreadID: "at_test",
		Image:         "test:latest",
		PodNamespace:  "sandbox",
		Config:        SecurityConfig{MaxLifetime: time.Hour},
	})

	err := m.UpdateState(sess.AgentThreadID, StateReady)
	require.NoError(t, err)

	got, _ := m.Get(sess.AgentThreadID)
	assert.Equal(t, StateReady, got.State)
}

func TestManager_ClientConnection(t *testing.T) {
	ctx := context.Background()
	m := NewManager()

	sess, _ := m.Create(ctx, CreateRequest{
		AgentThreadID: "at_test",
		Image:         "test:latest",
		PodNamespace:  "sandbox",
		Config:        SecurityConfig{MaxLifetime: time.Hour},
	})

	err := m.MarkClientConnected(sess.AgentThreadID)
	require.NoError(t, err)

	got, _ := m.Get(sess.AgentThreadID)
	assert.True(t, got.ClientConnected)

	err = m.MarkClientDisconnected(sess.AgentThreadID)
	require.NoError(t, err)

	got, _ = m.Get(sess.AgentThreadID)
	assert.False(t, got.ClientConnected)
}

func TestManager_UpdateActivity(t *testing.T) {
	ctx := context.Background()
	m := NewManager()

	sess, _ := m.Create(ctx, CreateRequest{
		AgentThreadID: "at_test",
		Image:         "test:latest",
		PodNamespace:  "sandbox",
		Config: SecurityConfig{
			MaxLifetime: time.Hour,
			IdleTimeout: 30 * time.Minute,
		},
	})

	oldActivity := sess.LastActivityAt
	time.Sleep(10 * time.Millisecond)

	err := m.UpdateActivity(sess.AgentThreadID)
	require.NoError(t, err)

	got, _ := m.Get(sess.AgentThreadID)
	assert.True(t, got.LastActivityAt.After(oldActivity))
}

func TestManager_Delete(t *testing.T) {
	ctx := context.Background()
	m := NewManager()

	sess, _ := m.Create(ctx, CreateRequest{
		AgentThreadID: "at_test",
		Image:         "test:latest",
		PodNamespace:  "sandbox",
		Config:        SecurityConfig{MaxLifetime: time.Hour},
	})

	m.Delete(sess.AgentThreadID)

	_, ok := m.Get(sess.AgentThreadID)
	assert.False(t, ok)
}

func TestSession_IsExpired(t *testing.T) {
	sess := &Session{
		CreatedAt: time.Now().Add(-2 * time.Hour),
		Config: SecurityConfig{
			MaxLifetime: 1 * time.Hour,
		},
	}
	assert.True(t, sess.IsExpired())

	sess.CreatedAt = time.Now()
	sess.Config.MaxLifetime = 1 * time.Hour
	assert.False(t, sess.IsExpired())
}

func TestSession_IsExpired_Idle(t *testing.T) {
	now := time.Now()

	sess := &Session{
		CreatedAt:      now,
		LastActivityAt: now,
		ClientConnected: true,
		Config: SecurityConfig{
			MaxLifetime: 24 * time.Hour,
			IdleTimeout: 30 * time.Minute,
		},
	}
	assert.False(t, sess.IsExpired())

	sess.ClientConnected = false
	sess.LastActivityAt = now.Add(-2 * time.Hour)
	assert.True(t, sess.IsExpired())
}
