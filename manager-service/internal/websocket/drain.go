package websocket

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// DrainTimeouts defines timeout durations for connection draining.
type DrainTimeouts struct {
	// DrainTimeout is the maximum time to wait for in-flight messages to be sent.
	DrainTimeout time.Duration

	// FlushTimeout is the maximum time to wait for the connection to flush.
	FlushTimeout time.Duration
}

// DefaultDrainTimeouts returns the default drain timeout values.
func DefaultDrainTimeouts() DrainTimeouts {
	return DrainTimeouts{
		DrainTimeout: 5 * time.Second,
		FlushTimeout: 2 * time.Second,
	}
}

// WebSocketConn defines the interface for WebSocket write operations.
// This allows for easier testing with mock connections.
type WebSocketConn interface {
	WriteMessage(messageType int, data []byte) error
	SetWriteDeadline(t time.Time) error
	Close() error
}

// ConnectionDrain manages graceful shutdown of WebSocket connections.
// It ensures that in-flight messages are sent before closing the connection.
type ConnectionDrain struct {
	conn         WebSocketConn
	timeouts     DrainTimeouts
	mu           sync.Mutex
	draining     atomic.Bool
	done         chan struct{}
	sendChan     chan sendMessage
}

// sendMessage represents a message to be sent during draining.
type sendMessage struct {
	messageType int
	data        []byte
}

// NewConnectionDrain creates a new ConnectionDrain for the given WebSocket connection.
func NewConnectionDrain(conn WebSocketConn, timeouts DrainTimeouts) *ConnectionDrain {
	if timeouts.DrainTimeout == 0 {
		timeouts.DrainTimeout = DefaultDrainTimeouts().DrainTimeout
	}
	if timeouts.FlushTimeout == 0 {
		timeouts.FlushTimeout = DefaultDrainTimeouts().FlushTimeout
	}

	cd := &ConnectionDrain{
		conn:     conn,
		timeouts: timeouts,
		done:     make(chan struct{}),
		sendChan: make(chan sendMessage, 100),
	}

	// Start sender goroutine
	go cd.sender()

	return cd
}

// StartDrain begins the graceful shutdown process.
// It blocks until the drain is complete or the context is cancelled.
// Returns an error if the drain fails or times out.
func (cd *ConnectionDrain) StartDrain(ctx context.Context) error {
	// Mark as draining - this is irreversible
	if !cd.draining.CompareAndSwap(false, true) {
		// Already draining
		return nil
	}

	// Create drain context with timeout
	drainCtx, cancel := context.WithTimeout(ctx, cd.timeouts.DrainTimeout)
	defer cancel()

	// Wait for sender to finish or drain timeout
	select {
	case <-cd.done:
		// Sender finished gracefully
		return nil
	case <-drainCtx.Done():
		// Drain timeout or context cancelled
		cd.mu.Lock()
		defer cd.mu.Unlock()

		// Close the connection forcefully
		if cd.conn != nil {
			_ = cd.conn.Close()
		}

		if errors.Is(drainCtx.Err(), context.DeadlineExceeded) {
			return errors.New("connection drain timed out")
		}
		return drainCtx.Err()
	}
}

// IsDraining returns true if the connection is in draining state.
func (cd *ConnectionDrain) IsDraining() bool {
	return cd.draining.Load()
}

// WriteMessage writes a message to the WebSocket connection.
// If the connection is draining, it returns an error immediately.
// Otherwise, it queues the message for sending by the sender goroutine.
func (cd *ConnectionDrain) WriteMessage(messageType int, data []byte) error {
	if cd.IsDraining() {
		return errors.New("connection is draining, new messages rejected")
	}

	// Try to send without blocking
	select {
	case cd.sendChan <- sendMessage{messageType: messageType, data: data}:
		return nil
	default:
		return errors.New("send channel full, message rejected")
	}
}

// sender runs in a goroutine to send messages queued in sendChan.
// It stops when draining starts and the queue is empty, or when an error occurs.
func (cd *ConnectionDrain) sender() {
	defer close(cd.done)

	for {
		select {
		case msg, ok := <-cd.sendChan:
			if !ok {
				// Channel closed
				return
			}

			// Set write deadline for each message
			writeDeadline := time.Now().Add(cd.timeouts.FlushTimeout)
			if err := cd.conn.SetWriteDeadline(writeDeadline); err != nil {
				return
			}

			// Write the message
			if err := cd.conn.WriteMessage(msg.messageType, msg.data); err != nil {
				return
			}

		case <-time.After(10 * time.Millisecond):
			// Check if we should stop
			if cd.IsDraining() && len(cd.sendChan) == 0 {
				// Draining and no more messages
				return
			}
		}
	}
}

// Close closes the drain and releases all resources.
func (cd *ConnectionDrain) Close() error {
	cd.draining.Store(true)
	return cd.conn.Close()
}

// WriteMessageWithRetry attempts to write a message with retry logic.
// This is useful during the drain phase when you want to ensure critical messages are sent.
func (cd *ConnectionDrain) WriteMessageWithRetry(ctx context.Context, messageType int, data []byte, maxRetries int) error {
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		// Check context
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Check if draining
		if cd.IsDraining() && i > 0 {
			return errors.New("connection is draining")
		}

		// Try to write
		err := cd.WriteMessage(messageType, data)
		if err == nil {
			return nil
		}

		lastErr = err

		// Wait before retry
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(i+1) * 50 * time.Millisecond):
			// Continue retry
		}
	}

	return lastErr
}
