package buffer

import (
	"errors"
	"sync"
)

const (
	// MaxMessageSize is the maximum size (in bytes) allowed for a message
	// This prevents potential denial-of-service attacks from oversized messages
	MaxMessageSize = 1 * 1024 * 1024 // 1MB
)

var (
	// ErrMessageTooLarge is returned when a message exceeds the maximum allowed size
	ErrMessageTooLarge = errors.New("message size exceeds maximum allowed size")
)

type Message struct {
	Type     string // "stdout", "stderr", "exit"
	Data     []byte
	ExitCode int32
}

type RingBuffer struct {
	mu     sync.RWMutex
	buffer []*Message
	size   int
	head   int
	tail   int
	count  int
}

func NewRingBuffer(size int) *RingBuffer {
	return &RingBuffer{
		buffer: make([]*Message, size),
		size:   size,
	}
}

func (rb *RingBuffer) Write(msg *Message) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	rb.buffer[rb.tail] = msg
	rb.tail = (rb.tail + 1) % rb.size

	if rb.count < rb.size {
		rb.count++
	} else {
		rb.head = (rb.head + 1) % rb.size
	}
}

// Add adds a message to the ring buffer with size validation.
// Returns ErrMessageTooLarge if the message exceeds MaxMessageSize.
func (rb *RingBuffer) Add(msg *Message) error {
	if msg == nil {
		return errors.New("cannot add nil message")
	}

	// Check message size
	if len(msg.Data) > MaxMessageSize {
		return ErrMessageTooLarge
	}

	rb.Write(msg)
	return nil
}

func (rb *RingBuffer) ReadAll() []*Message {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	result := make([]*Message, 0, rb.count)
	idx := rb.head

	for i := 0; i < rb.count; i++ {
		result = append(result, rb.buffer[idx])
		idx = (idx + 1) % rb.size
	}

	return result
}

func (rb *RingBuffer) Clear() {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	rb.head = 0
	rb.tail = 0
	rb.count = 0
}
