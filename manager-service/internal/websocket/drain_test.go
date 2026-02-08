package websocket

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// mockConn is a mock WebSocket connection for testing.
type mockConn struct {
	mu                sync.Mutex
	writeDeadline     time.Time
	closed            bool
	messagesWritten   []writtenMessage
	writeDelay        time.Duration
	writeShouldFail   bool
	writeFailAfter    int
	currentWriteIndex int
}

type writtenMessage struct {
	messageType int
	data        []byte
}

func newMockConn() *mockConn {
	return &mockConn{
		messagesWritten: make([]writtenMessage, 0),
	}
}

func (m *mockConn) WriteMessage(messageType int, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return errors.New("connection closed")
	}

	if m.writeDelay > 0 {
		time.Sleep(m.writeDelay)
	}

	if m.writeShouldFail && m.currentWriteIndex >= m.writeFailAfter {
		return errors.New("write failed")
	}

	m.messagesWritten = append(m.messagesWritten, writtenMessage{
		messageType: messageType,
		data:        make([]byte, len(data)),
	})
	copy(m.messagesWritten[len(m.messagesWritten)-1].data, data)

	m.currentWriteIndex++
	return nil
}

func (m *mockConn) SetWriteDeadline(t time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writeDeadline = t
	return nil
}

func (m *mockConn) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *mockConn) getMessagesWritten() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.messagesWritten)
}

func (m *mockConn) isClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

// TestNewConnectionDrain tests creating a new ConnectionDrain.
func TestNewConnectionDrain(t *testing.T) {
	mock := newMockConn()

	timeouts := DrainTimeouts{
		DrainTimeout: 3 * time.Second,
		FlushTimeout: 1 * time.Second,
	}

	cd := NewConnectionDrain(mock, timeouts)

	assert.NotNil(t, cd)
	assert.False(t, cd.IsDraining())
	assert.Equal(t, 3*time.Second, cd.timeouts.DrainTimeout)
	assert.Equal(t, 1*time.Second, cd.timeouts.FlushTimeout)
}

// TestNewConnectionDrain_DefaultTimeouts tests creating a drain with default timeouts.
func TestNewConnectionDrain_DefaultTimeouts(t *testing.T) {
	mock := newMockConn()

	cd := NewConnectionDrain(mock, DrainTimeouts{})

	assert.NotNil(t, cd)
	defaults := DefaultDrainTimeouts()
	assert.Equal(t, defaults.DrainTimeout, cd.timeouts.DrainTimeout)
	assert.Equal(t, defaults.FlushTimeout, cd.timeouts.FlushTimeout)
}

// TestDefaultDrainTimeouts tests the default timeout values.
func TestDefaultDrainTimeouts(t *testing.T) {
	defaults := DefaultDrainTimeouts()

	assert.Equal(t, 5*time.Second, defaults.DrainTimeout)
	assert.Equal(t, 2*time.Second, defaults.FlushTimeout)
}

// TestConnectionDrain_IsDraining tests the IsDraining method.
func TestConnectionDrain_IsDraining(t *testing.T) {
	mock := newMockConn()
	cd := NewConnectionDrain(mock, DrainTimeouts{})

	// Not draining initially
	assert.False(t, cd.IsDraining())

	// Start draining
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_ = cd.StartDrain(ctx)

	// Should be draining now
	assert.True(t, cd.IsDraining())
}

// TestConnectionDrain_WriteMessage_BeforeDrain tests writing messages before drain starts.
func TestConnectionDrain_WriteMessage_BeforeDrain(t *testing.T) {
	mock := newMockConn()
	cd := NewConnectionDrain(mock, DrainTimeouts{})

	// Write some messages
	err := cd.WriteMessage(1, []byte("test1"))
	assert.NoError(t, err)

	err = cd.WriteMessage(2, []byte("test2"))
	assert.NoError(t, err)

	// Give sender time to process
	time.Sleep(100 * time.Millisecond)

	// Close the drain
	_ = cd.Close()

	// Verify messages were written
	assert.Equal(t, 2, mock.getMessagesWritten())
}

// TestConnectionDrain_WriteMessage_DuringDrain tests that messages are rejected during drain.
func TestConnectionDrain_WriteMessage_DuringDrain(t *testing.T) {
	mock := newMockConn()
	cd := NewConnectionDrain(mock, DrainTimeouts{
		DrainTimeout: 100 * time.Millisecond,
		FlushTimeout: 50 * time.Millisecond,
	})

	// Start draining
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	drainDone := make(chan error, 1)
	go func() {
		drainDone <- cd.StartDrain(ctx)
	}()

	// Wait a bit for drain to start
	time.Sleep(20 * time.Millisecond)

	// Try to write - should be rejected
	err := cd.WriteMessage(1, []byte("should fail"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "draining")

	// Wait for drain to complete
	err = <-drainDone
	assert.NoError(t, err)
}

// TestConnectionDrain_WriteMessage_ChannelFull tests behavior when send channel is full.
func TestConnectionDrain_WriteMessage_ChannelFull(t *testing.T) {
	mock := newMockConn()
	cd := NewConnectionDrain(mock, DrainTimeouts{})

	// Fill the channel (buffer size is 100)
	for i := 0; i < 100; i++ {
		_ = cd.WriteMessage(1, []byte("fill"))
	}

	// This should fail because channel is full
	err := cd.WriteMessage(1, []byte("overflow"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "full")

	_ = cd.Close()
}

// TestConnectionDrain_StartDrain_ContextCancellation tests drain cancellation.
func TestConnectionDrain_StartDrain_ContextCancellation(t *testing.T) {
	mock := newMockConn()
	cd := NewConnectionDrain(mock, DrainTimeouts{
		DrainTimeout: 10 * time.Second,
		FlushTimeout: 1 * time.Second,
	})

	// Write some messages first
	for i := 0; i < 5; i++ {
		_ = cd.WriteMessage(1, []byte("test"))
	}

	// Cancel context immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := cd.StartDrain(ctx)
	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)

	// Connection should be closed
	assert.True(t, mock.isClosed())
}

// TestConnectionDrain_StartDrain_Timeout tests drain timeout.
func TestConnectionDrain_StartDrain_Timeout(t *testing.T) {
	mock := newMockConn()
	// Set a long write delay to cause timeout
	mock.writeDelay = 200 * time.Millisecond

	cd := NewConnectionDrain(mock, DrainTimeouts{
		DrainTimeout: 50 * time.Millisecond,
		FlushTimeout: 25 * time.Millisecond,
	})

	// Write many messages that will take too long
	for i := 0; i < 10; i++ {
		_ = cd.WriteMessage(1, []byte("test"))
	}

	ctx := context.Background()
	err := cd.StartDrain(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")

	// Connection should be closed
	assert.True(t, mock.isClosed())
}

// TestConnectionDrain_StartDrain_CompletesSuccessfully tests successful drain completion.
func TestConnectionDrain_StartDrain_CompletesSuccessfully(t *testing.T) {
	mock := newMockConn()
	cd := NewConnectionDrain(mock, DrainTimeouts{
		DrainTimeout: 1 * time.Second,
		FlushTimeout: 500 * time.Millisecond,
	})

	// Write some messages
	for i := 0; i < 5; i++ {
		err := cd.WriteMessage(1, []byte("test"))
		assert.NoError(t, err)
	}

	// Start drain - should complete successfully
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := cd.StartDrain(ctx)
	assert.NoError(t, err)

	// All messages should be written
	assert.Equal(t, 5, mock.getMessagesWritten())
}

// TestConnectionDrain_Close tests closing the drain.
func TestConnectionDrain_Close(t *testing.T) {
	mock := newMockConn()
	cd := NewConnectionDrain(mock, DrainTimeouts{})

	// Write some messages
	for i := 0; i < 3; i++ {
		_ = cd.WriteMessage(1, []byte("test"))
	}

	// Close the drain
	err := cd.Close()
	assert.NoError(t, err)

	// Connection should be marked as draining
	assert.True(t, cd.IsDraining())

	// Connection should be closed
	assert.True(t, mock.isClosed())
}

// TestConnectionDrain_WriteMessageWithRetry tests the retry logic.
func TestConnectionDrain_WriteMessageWithRetry(t *testing.T) {
	t.Run("succeeds on first try", func(t *testing.T) {
		mock := newMockConn()
		cd := NewConnectionDrain(mock, DrainTimeouts{})

		ctx := context.Background()
		err := cd.WriteMessageWithRetry(ctx, 1, []byte("test"), 3)
		assert.NoError(t, err)

		_ = cd.Close()
	})

	t.Run("respects context cancellation", func(t *testing.T) {
		mock := newMockConn()
		cd := NewConnectionDrain(mock, DrainTimeouts{})

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		err := cd.WriteMessageWithRetry(ctx, 1, []byte("test"), 3)
		assert.Error(t, err)
		assert.Equal(t, context.Canceled, err)

		_ = cd.Close()
	})

	t.Run("respects draining state", func(t *testing.T) {
		mock := newMockConn()
		cd := NewConnectionDrain(mock, DrainTimeouts{
			DrainTimeout: 100 * time.Millisecond,
		})

		// Start drain
		ctx := context.Background()
		go cd.StartDrain(ctx)
		time.Sleep(20 * time.Millisecond)

		// Try to write with retry
		retryCtx := context.Background()
		err := cd.WriteMessageWithRetry(retryCtx, 1, []byte("test"), 3)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "draining")

		_ = cd.Close()
	})
}

// TestConnectionDrain_MultipleStartDrain tests that multiple StartDrain calls are safe.
func TestConnectionDrain_MultipleStartDrain(t *testing.T) {
	mock := newMockConn()
	cd := NewConnectionDrain(mock, DrainTimeouts{})

	ctx := context.Background()

	// First call
	err := cd.StartDrain(ctx)
	assert.NoError(t, err)

	// Second call should be safe (already draining)
	err = cd.StartDrain(ctx)
	assert.NoError(t, err)

	assert.True(t, cd.IsDraining())
}

// BenchmarkWriteMessage benchmarks writing messages through the drain.
func BenchmarkWriteMessage(b *testing.B) {
	mock := newMockConn()
	cd := NewConnectionDrain(mock, DrainTimeouts{})

	// Start sender goroutine
	go cd.sender()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cd.WriteMessage(1, []byte("test"))
	}

	_ = cd.Close()
}
