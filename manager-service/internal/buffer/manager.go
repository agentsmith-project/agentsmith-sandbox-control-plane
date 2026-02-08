package buffer

import (
	"sync"
)

const DefaultBufferSize = 10000

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

func (m *Manager) Delete(agentThreadID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.buffers, agentThreadID)
}

// List returns all buffer IDs
func (m *Manager) List() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

 IDs := make([]string, 0, len(m.buffers))
	for id := range m.buffers {
		IDs = append(IDs, id)
	}
	return IDs
}

// Exists checks if a buffer exists for the given agent thread ID
func (m *Manager) Exists(agentThreadID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	_, ok := m.buffers[agentThreadID]
	return ok
}

// Clear deletes all buffers
func (m *Manager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.buffers = make(map[string]*RingBuffer)
}
