package buffer

import (
	"sync"
)

const DefaultBufferSize = 10000

// Manager manages a collection of ring buffers indexed by agent thread ID.
//
// WARNING: Concurrent Access Safety
// The GetOrCreate and Delete methods are safe to call concurrently.
// However, the RingBuffer returned by GetOrCreate is NOT safe for concurrent use.
// If you need to use the buffer from multiple goroutines, you must add
// your own synchronization around the buffer operations.
type Manager struct {
	mu      sync.RWMutex
	buffers map[string]*RingBuffer
}

func NewManager() *Manager {
	return &Manager{
		buffers: make(map[string]*RingBuffer),
	}
}

func (m *Manager) GetOrCreate(agentThreadID string) *RingBuffer {
	m.mu.Lock()
	defer m.mu.Unlock()

	if buf, ok := m.buffers[agentThreadID]; ok {
		return buf
	}

	buf := NewRingBuffer(DefaultBufferSize)
	m.buffers[agentThreadID] = buf
	return buf
}

// Delete removes the buffer for the given agent thread ID.
// Returns true if the buffer was found and deleted, false if it didn't exist.
func (m *Manager) Delete(agentThreadID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, existed := m.buffers[agentThreadID]
	delete(m.buffers, agentThreadID)
	return existed
}
