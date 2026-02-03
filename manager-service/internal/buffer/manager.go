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
