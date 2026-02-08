package buffer

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewManager_CreatesEmptyManager(t *testing.T) {
	mgr := NewManager()

	assert.NotNil(t, mgr)
	assert.NotNil(t, mgr.buffers)
}

func TestManager_GetOrCreate_CreatesNewBuffer(t *testing.T) {
	mgr := NewManager()

	buf := mgr.GetOrCreate("agent-1")

	assert.NotNil(t, buf)
	assert.IsType(t, &RingBuffer{}, buf)
}

func TestManager_GetOrCreate_ReturnsSameBuffer(t *testing.T) {
	mgr := NewManager()

	buf1 := mgr.GetOrCreate("agent-1")
	buf2 := mgr.GetOrCreate("agent-1")

	assert.Same(t, buf1, buf2)
}

func TestManager_GetOrCreate_DifferentAgents(t *testing.T) {
	mgr := NewManager()

	buf1 := mgr.GetOrCreate("agent-1")
	buf2 := mgr.GetOrCreate("agent-2")

	assert.NotSame(t, buf1, buf2)
}

func TestManager_Delete_RemovesBuffer(t *testing.T) {
	mgr := NewManager()

	buf := mgr.GetOrCreate("agent-1")
	mgr.Delete("agent-1")

	buf2 := mgr.GetOrCreate("agent-1")

	// Should create a new buffer after deletion
	assert.NotSame(t, buf, buf2)
}

func TestManager_Delete_NonExistent_NoPanic(t *testing.T) {
	mgr := NewManager()

	assert.NotPanics(t, func() {
		mgr.Delete("non-existent")
	})
}

func TestManager_ConcurrentAccess(t *testing.T) {
	mgr := NewManager()
	var wg sync.WaitGroup

	numGoroutines := 100
	numOperations := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			agentID := string(rune('a' + (n % 26)))

			for j := 0; j < numOperations; j++ {
				buf := mgr.GetOrCreate(agentID)
				buf.Write(&Message{Type: "test", Data: []byte("data")})

				if j%10 == 0 {
					mgr.Delete(agentID)
				}
			}
		}(i)
	}

	wg.Wait()

	// Manager should still be functional
	buf := mgr.GetOrCreate("test")
	assert.NotNil(t, buf)
}

func TestManager_GetOrCreate_BufferCapacity(t *testing.T) {
	mgr := NewManager()

	buf := mgr.GetOrCreate("agent-1")

	// Write more than DefaultBufferSize messages
	for i := 0; i < DefaultBufferSize+100; i++ {
		buf.Write(&Message{Type: "test", Data: []byte("data")})
	}

	messages := buf.ReadAll()
	// Should have DefaultBufferSize messages (old ones overwritten)
	assert.Len(t, messages, DefaultBufferSize)
}

func TestManager_MultipleAgentsIsolation(t *testing.T) {
	mgr := NewManager()

	buf1 := mgr.GetOrCreate("agent-1")
	buf2 := mgr.GetOrCreate("agent-2")

	buf1.Write(&Message{Type: "test", Data: []byte("data-1")})
	buf2.Write(&Message{Type: "test", Data: []byte("data-2")})

	messages1 := buf1.ReadAll()
	messages2 := buf2.ReadAll()

	assert.Len(t, messages1, 1)
	assert.Len(t, messages2, 1)
	assert.Equal(t, []byte("data-1"), messages1[0].Data)
	assert.Equal(t, []byte("data-2"), messages2[0].Data)
}

func TestManager_GetOrCreate_ThreadSafe(t *testing.T) {
	mgr := NewManager()
	var wg sync.WaitGroup

	results := make(chan *RingBuffer, 100)

	// Try to get/create the same buffer from multiple goroutines
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := mgr.GetOrCreate("same-agent")
			results <- buf
		}()
	}

	wg.Wait()
	close(results)

	// All goroutines should have gotten the same buffer
	var firstBuf *RingBuffer
	for buf := range results {
		if firstBuf == nil {
			firstBuf = buf
		} else {
			assert.Same(t, firstBuf, buf)
		}
	}
}

func TestManager_Delete_ThreadSafe(t *testing.T) {
	mgr := NewManager()
	var wg sync.WaitGroup

	// Create buffer
	mgr.GetOrCreate("agent-1")

	// Delete and get concurrently
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			mgr.Delete("agent-1")
		}()
		go func() {
			defer wg.Done()
			mgr.GetOrCreate("agent-1")
		}()
	}

	wg.Wait()

	// Should not panic and manager should still work
	buf := mgr.GetOrCreate("test")
	assert.NotNil(t, buf)
}
