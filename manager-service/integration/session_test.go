//go:build !short

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sandbox/manager/internal/session"
)

// TestSessionLifecycle_Create tests creating a session with various configurations
func TestSessionLifecycle_Create(t *testing.T) {
	ctx := context.Background()
	m := session.NewManager()

	t.Run("basic session creation", func(t *testing.T) {
		req := session.CreateRequest{
			AgentThreadID: "at_basic_create",
			Image:         "ubuntu:22.04",
			Command:       []string{"/bin/bash"},
			Env: map[string]string{
				"PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
			},
			PodNamespace: "sandbox",
			Config: session.SecurityConfig{
				AllowNetworkAccess:  true,
				ReadonlyFilesystem:  false,
				CPULimit:           "500m",
				MemoryLimit:        "512Mi",
				IdleTimeout:        30 * time.Minute,
				MaxLifetime:        4 * time.Hour,
				DropAllCapabilities: true,
				AllowPrivileged:     false,
			},
		}

		sess, err := m.Create(ctx, req)
		require.NoError(t, err)

		// Verify session fields
		assert.Equal(t, "at_basic_create", sess.AgentThreadID)
		assert.Equal(t, "ubuntu:22.04", sess.Image)
		assert.Equal(t, []string{"/bin/bash"}, sess.Command)
		assert.Equal(t, "sandbox", sess.PodNamespace)
		assert.Equal(t, session.StateCreating, sess.State)
		assert.False(t, sess.ClientConnected)
		assert.Equal(t, "500m", sess.Config.CPULimit)
		assert.Equal(t, "512Mi", sess.Config.MemoryLimit)
		assert.Equal(t, 30*time.Minute, sess.Config.IdleTimeout)
		assert.Equal(t, 4*time.Hour, sess.Config.MaxLifetime)

		// Verify timestamps
		assert.WithinDuration(t, time.Now(), sess.CreatedAt, time.Second)
		assert.WithinDuration(t, time.Now(), sess.LastActivityAt, time.Second)
		assert.WithinDuration(t, time.Now().Add(4*time.Hour), sess.ExpiresAt, time.Second)
	})

	t.Run("session with minimal config", func(t *testing.T) {
		req := session.CreateRequest{
			AgentThreadID: "at_minimal",
			Image:         "alpine:latest",
			PodNamespace:  "default",
			Config:        session.SecurityConfig{MaxLifetime: time.Hour},
		}

		sess, err := m.Create(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, "at_minimal", sess.AgentThreadID)
		assert.Empty(t, sess.Command)
		assert.Empty(t, sess.Env)
	})
}

// TestSessionLifecycle_UpdateState tests state transitions through the session lifecycle
func TestSessionLifecycle_UpdateState(t *testing.T) {
	ctx := context.Background()
	m := session.NewManager()

	sess, err := m.Create(ctx, session.CreateRequest{
		AgentThreadID: "at_state_transitions",
		Image:         "ubuntu:22.04",
		PodNamespace:  "sandbox",
		Config:        session.SecurityConfig{MaxLifetime: time.Hour},
	})
	require.NoError(t, err)

	// Initial state
	assert.Equal(t, session.StateCreating, sess.State)

	// Transition to Ready
	err = m.UpdateState(sess.AgentThreadID, session.StateReady)
	require.NoError(t, err)
	updated, _ := m.Get(sess.AgentThreadID)
	assert.Equal(t, session.StateReady, updated.State)

	// Transition to Offline
	err = m.UpdateState(sess.AgentThreadID, session.StateOffline)
	require.NoError(t, err)
	updated, _ = m.Get(sess.AgentThreadID)
	assert.Equal(t, session.StateOffline, updated.State)

	// Error on non-existent session
	err = m.UpdateState("nonexistent", session.StateReady)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")
}

// TestSessionLifecycle_ClientConnection tests client connection tracking
func TestSessionLifecycle_ClientConnection(t *testing.T) {
	ctx := context.Background()
	m := session.NewManager()

	sess, err := m.Create(ctx, session.CreateRequest{
		AgentThreadID: "at_client_conn",
		Image:         "ubuntu:22.04",
		PodNamespace:  "sandbox",
		Config: session.SecurityConfig{
			MaxLifetime: 4 * time.Hour,
			IdleTimeout: 30 * time.Minute,
		},
	})
	require.NoError(t, err)

	// Initially not connected
	assert.False(t, sess.ClientConnected)

	// Mark as connected
	err = m.MarkClientConnected(sess.AgentThreadID)
	require.NoError(t, err)
	updated, _ := m.Get(sess.AgentThreadID)
	assert.True(t, updated.ClientConnected)

	// Activity is updated when connecting
	assert.WithinDuration(t, time.Now(), updated.LastActivityAt, time.Second)

	// Mark as disconnected
	err = m.MarkClientDisconnected(sess.AgentThreadID)
	require.NoError(t, err)
	updated, _ = m.Get(sess.AgentThreadID)
	assert.False(t, updated.ClientConnected)

	// Error on non-existent session
	err = m.MarkClientConnected("nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")

	err = m.MarkClientDisconnected("nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "session not")
}

// TestSessionLifecycle_ActivityTracking tests activity updates and their effect on expiration
func TestSessionLifecycle_ActivityTracking(t *testing.T) {
	ctx := context.Background()
	m := session.NewManager()

	req := session.CreateRequest{
		AgentThreadID: "at_activity",
		Image:         "ubuntu:22.04",
		PodNamespace:  "sandbox",
		Config: session.SecurityConfig{
			MaxLifetime: 4 * time.Hour,
			IdleTimeout: 30 * time.Minute,
		},
	}

	sess, err := m.Create(ctx, req)
	require.NoError(t, err)

	initialActivity := sess.LastActivityAt

	// Wait a bit to ensure timestamp changes
	time.Sleep(10 * time.Millisecond)

	// Update activity
	err = m.UpdateActivity(sess.AgentThreadID)
	require.NoError(t, err)

	updated, _ := m.Get(sess.AgentThreadID)
	assert.True(t, updated.LastActivityAt.After(initialActivity), "LastActivityAt should be updated")

	// For connected sessions, expiry is based on MaxLifetime, not idle timeout
	err = m.MarkClientConnected(sess.AgentThreadID)
	require.NoError(t, err)
	updated, _ = m.Get(sess.AgentThreadID)
	assert.Equal(t, sess.CreatedAt.Add(4*time.Hour).Truncate(time.Second), 
		updated.ExpiresAt.Truncate(time.Second))

	// For disconnected sessions, idle timeout affects expiry
	err = m.MarkClientDisconnected(sess.AgentThreadID)
	require.NoError(t, err)
	updated, _ = m.Get(sess.AgentThreadID)
	idleExpiry := updated.LastActivityAt.Add(30 * time.Minute)
	maxExpiry := sess.CreatedAt.Add(4 * time.Hour)
	// Idle expiry should be earlier than max lifetime
	assert.True(t, idleExpiry.Before(maxExpiry) || idleExpiry.Equal(maxExpiry))
	assert.Equal(t, idleExpiry.Truncate(time.Second), updated.ExpiresAt.Truncate(time.Second))

	// Error on non-existent session
	err = m.UpdateActivity("nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")
}

// TestSessionLifecycle_Expiration_MaxLifetime tests max lifetime expiration
func TestSessionLifecycle_Expiration_MaxLifetime(t *testing.T) {
	ctx := context.Background()
	m := session.NewManager()

	t.Run("session expires after max lifetime", func(t *testing.T) {
		// Create session with short max lifetime
		req := session.CreateRequest{
			AgentThreadID: "at_max_lifetime",
			Image:         "ubuntu:22.04",
			PodNamespace:  "sandbox",
			Config: session.SecurityConfig{
				MaxLifetime: 100 * time.Millisecond,
				IdleTimeout: 30 * time.Minute,
			},
		}

		sess, err := m.Create(ctx, req)
		require.NoError(t, err)

		// Not expired initially
		assert.False(t, sess.IsExpired())

		// Wait for expiration
		time.Sleep(150 * time.Millisecond)

		updated, _ := m.Get(sess.AgentThreadID)
		assert.True(t, updated.IsExpired(), "Session should be expired after max lifetime")
	})

	t.Run("connected session still expires by max lifetime", func(t *testing.T) {
		req := session.CreateRequest{
			AgentThreadID: "at_max_lifetime_connected",
			Image:         "ubuntu:22.04",
			PodNamespace:  "sandbox",
			Config: session.SecurityConfig{
				MaxLifetime: 100 * time.Millisecond,
				IdleTimeout: 30 * time.Minute,
			},
		}

		sess, err := m.Create(ctx, req)
		require.NoError(t, err)

		// Mark as connected
		err = m.MarkClientConnected(sess.AgentThreadID)
		require.NoError(t, err)

		// Wait for expiration
		time.Sleep(150 * time.Millisecond)

		updated, _ := m.Get(sess.AgentThreadID)
		assert.True(t, updated.IsExpired(), "Connected session should still expire after max lifetime")
	})
}

// TestSessionLifecycle_Expiration_IdleTimeout tests idle timeout expiration
func TestSessionLifecycle_Expiration_IdleTimeout(t *testing.T) {
	ctx := context.Background()
	m := session.NewManager()

	t.Run("disconnected session expires after idle timeout", func(t *testing.T) {
		req := session.CreateRequest{
			AgentThreadID: "at_idle_timeout",
			Image:         "ubuntu:22.04",
			PodNamespace:  "sandbox",
			Config: session.SecurityConfig{
				MaxLifetime: 1 * time.Hour,
				IdleTimeout: 100 * time.Millisecond,
			},
		}

		sess, err := m.Create(ctx, req)
		require.NoError(t, err)

		// Mark as disconnected
		err = m.MarkClientDisconnected(sess.AgentThreadID)
		require.NoError(t, err)

		// Not expired initially
		updated, _ := m.Get(sess.AgentThreadID)
		assert.False(t, updated.IsExpired())

		// Wait for idle timeout
		time.Sleep(150 * time.Millisecond)

		updated, _ = m.Get(sess.AgentThreadID)
		assert.True(t, updated.IsExpired(), "Disconnected session should expire after idle timeout")
	})

	t.Run("connected session does not expire by idle timeout", func(t *testing.T) {
		req := session.CreateRequest{
			AgentThreadID: "at_no_idle_when_connected",
			Image:         "ubuntu:22.04",
			PodNamespace:  "sandbox",
			Config: session.SecurityConfig{
				MaxLifetime: 1 * time.Hour,
				IdleTimeout: 100 * time.Millisecond,
			},
		}

		sess, err := m.Create(ctx, req)
		require.NoError(t, err)

		// Mark as connected
		err = m.MarkClientConnected(sess.AgentThreadID)
		require.NoError(t, err)

		// Wait longer than idle timeout
		time.Sleep(150 * time.Millisecond)

		updated, _ := m.Get(sess.AgentThreadID)
		assert.False(t, updated.IsExpired(), "Connected session should not expire by idle timeout")
	})

	t.Run("activity updates prevent idle expiration", func(t *testing.T) {
		req := session.CreateRequest{
			AgentThreadID: "at_activity_prevents_idle",
			Image:         "ubuntu:22.04",
			PodNamespace:  "sandbox",
			Config: session.SecurityConfig{
				MaxLifetime: 1 * time.Hour,
				IdleTimeout: 200 * time.Millisecond,
			},
		}

		sess, err := m.Create(ctx, req)
		require.NoError(t, err)

		// Mark as disconnected
		err = m.MarkClientDisconnected(sess.AgentThreadID)
		require.NoError(t, err)

		// Wait and update activity before timeout
		time.Sleep(100 * time.Millisecond)
		err = m.UpdateActivity(sess.AgentThreadID)
		require.NoError(t, err)

		// Wait for what would be the original timeout
		time.Sleep(150 * time.Millisecond)

		updated, _ := m.Get(sess.AgentThreadID)
		assert.False(t, updated.IsExpired(), "Activity update should reset idle timeout")
	})

	t.Run("zero idle timeout means no idle expiration", func(t *testing.T) {
		req := session.CreateRequest{
			AgentThreadID: "at_no_idle_timeout",
			Image:         "ubuntu:22.04",
			PodNamespace:  "sandbox",
			Config: session.SecurityConfig{
				MaxLifetime: 1 * time.Hour,
				IdleTimeout: 0,
			},
		}

		sess, err := m.Create(ctx, req)
		require.NoError(t, err)

		// Mark as disconnected
		err = m.MarkClientDisconnected(sess.AgentThreadID)
		require.NoError(t, err)

		// Wait a bit
		time.Sleep(100 * time.Millisecond)

		updated, _ := m.Get(sess.AgentThreadID)
		assert.False(t, updated.IsExpired(), "Zero idle timeout should not cause expiration")
	})
}

// TestSessionLifecycle_GetExpiresAt tests the expiration time calculation
func TestSessionLifecycle_GetExpiresAt(t *testing.T) {
	ctx := context.Background()
	m := session.NewManager()

	t.Run("max lifetime determines expiry for connected sessions", func(t *testing.T) {
		req := session.CreateRequest{
			AgentThreadID: "at_expiry_connected",
			Image:         "ubuntu:22.04",
			PodNamespace:  "sandbox",
			Config: session.SecurityConfig{
				MaxLifetime: 4 * time.Hour,
				IdleTimeout: 30 * time.Minute,
			},
		}

		sess, err := m.Create(ctx, req)
		require.NoError(t, err)

		err = m.MarkClientConnected(sess.AgentThreadID)
		require.NoError(t, err)

		updated, _ := m.Get(sess.AgentThreadID)
		expectedExpiry := sess.CreatedAt.Add(4 * time.Hour)
		assert.WithinDuration(t, expectedExpiry, updated.ExpiresAt, time.Second)
	})

	t.Run("idle timeout determines expiry for disconnected sessions", func(t *testing.T) {
		req := session.CreateRequest{
			AgentThreadID: "at_expiry_disconnected",
			Image:         "ubuntu:22.04",
			PodNamespace:  "sandbox",
			Config: session.SecurityConfig{
				MaxLifetime: 4 * time.Hour,
				IdleTimeout: 30 * time.Minute,
			},
		}

		sess, err := m.Create(ctx, req)
		require.NoError(t, err)

		// Wait a bit to create a gap between CreatedAt and LastActivityAt
		time.Sleep(10 * time.Millisecond)

		err = m.MarkClientDisconnected(sess.AgentThreadID)
		require.NoError(t, err)

		updated, _ := m.Get(sess.AgentThreadID)
		maxExpiry := sess.CreatedAt.Add(4 * time.Hour)
		idleExpiry := updated.LastActivityAt.Add(30 * time.Minute)
		
		// Idle expiry should be used since it's earlier
		assert.Equal(t, idleExpiry.Truncate(time.Second), updated.ExpiresAt.Truncate(time.Second))
		assert.True(t, updated.ExpiresAt.Before(maxExpiry))
	})
}

// TestSessionLifecycle_PodInfo tests setting and retrieving pod information
func TestSessionLifecycle_PodInfo(t *testing.T) {
	ctx := context.Background()
	m := session.NewManager()

	sess, err := m.Create(ctx, session.CreateRequest{
		AgentThreadID: "at_pod_info",
		Image:         "ubuntu:22.04",
		PodNamespace:  "sandbox",
		Config:        session.SecurityConfig{MaxLifetime: time.Hour},
	})
	require.NoError(t, err)

	// Initially no pod name
	assert.Empty(t, sess.PodName)

	// Set pod info
	err = m.SetPodInfo(sess.AgentThreadID, "sandbox-pod-abc123")
	require.NoError(t, err)

	updated, _ := m.Get(sess.AgentThreadID)
	assert.Equal(t, "sandbox-pod-abc123", updated.PodName)
	assert.Equal(t, "sandbox", updated.PodNamespace)

	// Error on non-existent session
	err = m.SetPodInfo("nonexistent", "some-pod")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")
}

// TestSessionLifecycle_Delete tests session deletion
func TestSessionLifecycle_Delete(t *testing.T) {
	ctx := context.Background()
	m := session.NewManager()

	t.Run("delete existing session", func(t *testing.T) {
		sess, err := m.Create(ctx, session.CreateRequest{
			AgentThreadID: "at_delete_me",
			Image:         "ubuntu:22.04",
			PodNamespace:  "sandbox",
			Config:        session.SecurityConfig{MaxLifetime: time.Hour},
		})
		require.NoError(t, err)

		// Verify session exists
		_, ok := m.Get(sess.AgentThreadID)
		assert.True(t, ok)

		// Delete session
		m.Delete(sess.AgentThreadID)

		// Verify session is gone
		_, ok = m.Get(sess.AgentThreadID)
		assert.False(t, ok)
	})

	t.Run("delete non-existent session is no-op", func(t *testing.T) {
		// Should not panic
		m.Delete("nonexistent")
		_, ok := m.Get("nonexistent")
		assert.False(t, ok)
	})
}

// TestSessionLifecycle_MultipleSessions tests managing multiple sessions concurrently
func TestSessionLifecycle_MultipleSessions(t *testing.T) {
	ctx := context.Background()
	m := session.NewManager()

	// Create multiple sessions
	sessions := make([]*session.Session, 5)
	for i := 0; i < 5; i++ {
		sess, err := m.Create(ctx, session.CreateRequest{
			AgentThreadID: "at_multi_" + string(rune('a'+i)),
			Image:         "ubuntu:22.04",
			PodNamespace:  "sandbox",
			Config:        session.SecurityConfig{MaxLifetime: time.Hour},
		})
		require.NoError(t, err)
		sessions[i] = sess
	}

	// Verify all sessions exist
	for _, sess := range sessions {
		_, ok := m.Get(sess.AgentThreadID)
		assert.True(t, ok)
	}

	// Update each session differently
	err := m.UpdateState(sessions[0].AgentThreadID, session.StateReady)
	require.NoError(t, err)

	err = m.MarkClientConnected(sessions[1].AgentThreadID)
	require.NoError(t, err)

	err = m.SetPodInfo(sessions[2].AgentThreadID, "pod-123")
	require.NoError(t, err)

	// Verify updates didn't affect other sessions
	s0, _ := m.Get(sessions[0].AgentThreadID)
	assert.Equal(t, session.StateReady, s0.State)
	assert.False(t, s0.ClientConnected)

	s1, _ := m.Get(sessions[1].AgentThreadID)
	assert.Equal(t, session.StateCreating, s1.State)
	assert.True(t, s1.ClientConnected)

	s2, _ := m.Get(sessions[2].AgentThreadID)
	assert.Equal(t, "pod-123", s2.PodName)
	assert.False(t, s2.ClientConnected)

	// Delete one session
	m.Delete(sessions[3].AgentThreadID)
	_, ok := m.Get(sessions[3].AgentThreadID)
	assert.False(t, ok)

	// Other sessions still exist
	_, ok = m.Get(sessions[4].AgentThreadID)
	assert.True(t, ok)
}

// TestSessionLifecycle_CompleteWorkflow tests a complete session lifecycle workflow
func TestSessionLifecycle_CompleteWorkflow(t *testing.T) {
	ctx := context.Background()
	m := session.NewManager()

	// Step 1: Create session
	req := session.CreateRequest{
		AgentThreadID: "at_workflow",
		Image:         "ubuntu:22.04",
		Command:       []string{"/bin/bash"},
		Env:           map[string]string{"TERM": "xterm"},
		PodNamespace:  "sandbox",
		Config: session.SecurityConfig{
			MaxLifetime:           4 * time.Hour,
			IdleTimeout:           30 * time.Minute,
			AllowNetworkAccess:    true,
			ReadonlyFilesystem:    false,
			CPULimit:             "500m",
			MemoryLimit:          "512Mi",
			DropAllCapabilities:  true,
			AllowPrivileged:      false,
		},
	}

	sess, err := m.Create(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, session.StateCreating, sess.State)
	assert.False(t, sess.ClientConnected)

	// Step 2: Pod is created
	err = m.SetPodInfo(sess.AgentThreadID, "sandbox-at-workflow-abc123")
	require.NoError(t, err)

	// Step 3: Pod is ready
	err = m.UpdateState(sess.AgentThreadID, session.StateReady)
	require.NoError(t, err)
	updated, _ := m.Get(sess.AgentThreadID)
	assert.Equal(t, session.StateReady, updated.State)

	// Step 4: Client connects
	err = m.MarkClientConnected(sess.AgentThreadID)
	require.NoError(t, err)
	updated, _ = m.Get(sess.AgentThreadID)
	assert.True(t, updated.ClientConnected)

	// Step 5: Session activity while connected
	time.Sleep(10 * time.Millisecond)
	err = m.UpdateActivity(sess.AgentThreadID)
	require.NoError(t, err)

	// Step 6: Client disconnects
	err = m.MarkClientDisconnected(sess.AgentThreadID)
	require.NoError(t, err)
	updated, _ = m.Get(sess.AgentThreadID)
	assert.False(t, updated.ClientConnected)

	// Step 7: Session goes offline after inactivity
	err = m.UpdateState(sess.AgentThreadID, session.StateOffline)
	require.NoError(t, err)
	updated, _ = m.Get(sess.AgentThreadID)
	assert.Equal(t, session.StateOffline, updated.State)

	// Step 8: Delete session
	m.Delete(sess.AgentThreadID)
	_, ok := m.Get(sess.AgentThreadID)
	assert.False(t, ok, "Session should be deleted")
}
