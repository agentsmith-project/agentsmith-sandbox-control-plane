package session

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
	"unsafe"

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

func TestManager_StartCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := NewManager()

	// Create some sessions
	_, _ = m.Create(ctx, CreateRequest{
		AgentThreadID: "at_active",
		Image:         "test:latest",
		PodNamespace:  "sandbox",
		Config: SecurityConfig{
			MaxLifetime: 1 * time.Hour,
			IdleTimeout: 30 * time.Minute,
		},
	})

	// Create an expired session (old CreatedAt)
	_, _ = m.Create(ctx, CreateRequest{
		AgentThreadID: "at_expired",
		Image:         "test:latest",
		PodNamespace:  "sandbox",
		Config: SecurityConfig{
			MaxLifetime: 1 * time.Millisecond, // Very short lifetime
			IdleTimeout: 30 * time.Minute,
		},
	})

	// Wait for expiration
	time.Sleep(10 * time.Millisecond)

	// Run cleanup once
	m.cleanupExpired()

	// Active session should still exist
	_, ok := m.Get("at_active")
	assert.True(t, ok, "active session should still exist")

	// Expired session should be removed
	_, ok = m.Get("at_expired")
	assert.False(t, ok, "expired session should be removed")
}

func TestManager_StartCleanup_Concurrent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := NewManager()

	// Create multiple sessions
	for i := 0; i < 10; i++ {
		_, _ = m.Create(ctx, CreateRequest{
			AgentThreadID: fmt.Sprintf("at_%d", i),
			Image:         "test:latest",
			PodNamespace:  "sandbox",
			Config: SecurityConfig{
				MaxLifetime: 1 * time.Hour,
				IdleTimeout: 30 * time.Minute,
			},
		})
	}

	assert.Equal(t, 10, m.GetSessionCount())

	// Start cleanup in background
	done := make(chan struct{})
	go func() {
		m.StartCleanup(ctx, 100*time.Millisecond)
		close(done)
	}()

	// Wait a bit
	time.Sleep(150 * time.Millisecond)

	// All sessions should still be active
	assert.Equal(t, 10, m.GetSessionCount())

	// Cancel context to stop cleanup
	cancel()
	<-done
}

func TestManager_cleanupExpired_IdleTimeout(t *testing.T) {
	ctx := context.Background()
	m := NewManager()

	now := time.Now()

	// Create a disconnected session with old activity
	sess, _ := m.Create(ctx, CreateRequest{
		AgentThreadID: "at_idle",
		Image:         "test:latest",
		PodNamespace:  "sandbox",
		Config: SecurityConfig{
			MaxLifetime: 24 * time.Hour,
			IdleTimeout: 10 * time.Millisecond,
		},
	})

	// Mark as disconnected and set old activity
	sess.ClientConnected = false
	sess.LastActivityAt = now.Add(-1 * time.Hour)

	// Run cleanup
	m.cleanupExpired()

	// Idle expired session should be removed
	_, ok := m.Get("at_idle")
	assert.False(t, ok, "idle expired session should be removed")
}

func TestManager_GetSessionCount(t *testing.T) {
	ctx := context.Background()
	m := NewManager()

	assert.Equal(t, 0, m.GetSessionCount())

	// Add some sessions
	for i := 0; i < 5; i++ {
		_, _ = m.Create(ctx, CreateRequest{
			AgentThreadID: fmt.Sprintf("at_%d", i),
			Image:         "test:latest",
			PodNamespace:  "sandbox",
			Config:        SecurityConfig{MaxLifetime: 1 * time.Hour},
		})
	}

	assert.Equal(t, 5, m.GetSessionCount())

	// Delete one
	m.Delete("at_0")
	assert.Equal(t, 4, m.GetSessionCount())
}

func TestManager_GetOrCreate_ExistingSession(t *testing.T) {
	m := NewManager()

	// Create a session first
	ctx := context.Background()
	existingSess, _ := m.Create(ctx, CreateRequest{
		AgentThreadID: "at_test",
		Image:         "test:latest",
		PodNamespace:  "sandbox",
		Config:        SecurityConfig{MaxLifetime: 1 * time.Hour},
	})

	// GetOrCreate should return the existing session
	sess, wasCreated, err := m.GetOrCreate("at_test")
	require.NoError(t, err)
	assert.False(t, wasCreated, "should not create a new session")
	assert.Equal(t, existingSess.AgentThreadID, sess.AgentThreadID)
	assert.Equal(t, m.GetSessionCount(), 1, "should only have one session")
}

func TestManager_GetOrCreate_NewSession(t *testing.T) {
	m := NewManager()

	// GetOrCreate should create a new session
	sess, wasCreated, err := m.GetOrCreate("at_new")
	require.NoError(t, err)
	assert.True(t, wasCreated, "should create a new session")
	assert.Equal(t, "at_new", sess.AgentThreadID)
	assert.Equal(t, StateCreating, sess.State)
	assert.Equal(t, 1, m.GetSessionCount())
}

func TestManager_GetOrCreate_Concurrent(t *testing.T) {
	m := NewManager()

	const numGoroutines = 100
	const agentThreadID = "at_concurrent"

	// Channel to collect results
	results := make(chan struct {
		session     *Session
		wasCreated  bool
		sessionAddr uintptr
	}, numGoroutines)

	// Launch multiple goroutines trying to GetOrCreate the same session
	for i := 0; i < numGoroutines; i++ {
		go func() {
			sess, wasCreated, _ := m.GetOrCreate(agentThreadID)
			// Get the address of the session to verify it's the same instance
			results <- struct {
				session     *Session
				wasCreated  bool
				sessionAddr uintptr
			}{sess, wasCreated, uintptr(unsafe.Pointer(sess))}
		}()
	}

	// Collect all results
	var createdCount int
	var sessionAddrs []uintptr
	for i := 0; i < numGoroutines; i++ {
		result := <-results
		if result.wasCreated {
			createdCount++
		}
		sessionAddrs = append(sessionAddrs, result.sessionAddr)
	}

	// Verify only one session was created
	assert.Equal(t, 1, createdCount, "exactly one session should be created")
	assert.Equal(t, 1, m.GetSessionCount(), "manager should have exactly one session")

	// Verify all goroutines got the same session instance
	uniqueSessions := make(map[uintptr]bool)
	for _, addr := range sessionAddrs {
		uniqueSessions[addr] = true
	}
	assert.Equal(t, 1, len(uniqueSessions), "all goroutines should get the same session instance")
}

func TestManager_GetOrCreate_ConcurrentMultipleIDs(t *testing.T) {
	m := NewManager()

	const numIDs = 10
	const goroutinesPerID = 10

	ids := make([]string, numIDs)
	for i := 0; i < numIDs; i++ {
		ids[i] = fmt.Sprintf("at_%d", i)
	}

	// Launch goroutines for each ID
	var wg sync.WaitGroup
	wg.Add(numIDs * goroutinesPerID)

	createdCount := make(map[string]int)
	var mu sync.Mutex

	for _, id := range ids {
		for j := 0; j < goroutinesPerID; j++ {
			go func(agentThreadID string) {
				defer wg.Done()
				sess, wasCreated, _ := m.GetOrCreate(agentThreadID)
				require.NotNil(t, sess)
				if wasCreated {
					mu.Lock()
					createdCount[agentThreadID]++
					mu.Unlock()
				}
			}(id)
		}
	}

	wg.Wait()

	// Each ID should have exactly one session created
	assert.Equal(t, numIDs, m.GetSessionCount())
	for _, id := range ids {
		assert.Equal(t, 1, createdCount[id], "ID %s should have exactly one session created", id)
	}
}

func TestManager_GetOrCreate_RaceWithCreate(t *testing.T) {
	m := NewManager()
	ctx := context.Background()
	const agentThreadID = "at_race"

	// Test that GetOrCreate doesn't race with Create
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		_, _ = m.Create(ctx, CreateRequest{
			AgentThreadID: agentThreadID,
			Image:         "test:latest",
			PodNamespace:  "sandbox",
			Config:        SecurityConfig{MaxLifetime: 1 * time.Hour},
		})
	}()

	go func() {
		defer wg.Done()
		m.GetOrCreate(agentThreadID)
	}()

	wg.Wait()

	// Should have exactly one session
	assert.Equal(t, 1, m.GetSessionCount())
}
