package exec

import (
	"io"
)

// TailBufferWriter is an io.Writer that keeps only the last N bytes
// This ensures the exit code marker is not truncated
type TailBufferWriter struct {
	buf     []byte
	size    int
	maxSize int
	written int64
	limit   int64
}

// NewTailBufferWriter creates a new tail buffer writer
func NewTailBufferWriter(maxBytes int64, preserveTailBytes int64) *TailBufferWriter {
	// Use the smaller of maxBytes and preserveTailBytes for the buffer size
	bufSize := int(maxBytes)
	if preserveTailBytes < maxBytes {
		bufSize = int(preserveTailBytes)
	}

	return &TailBufferWriter{
		buf:     make([]byte, 0, bufSize),
		size:    0,
		maxSize: bufSize,
		limit:   maxBytes,
	}
}

// Write implements io.Writer
func (t *TailBufferWriter) Write(p []byte) (int, error) {
	t.written += int64(len(p))

	// If we've hit the limit, discard the write but track that we wrote it
	if t.written > t.limit {
		// Still try to keep the tail in the buffer
		t.keepTail(p)
		return len(p), nil
	}

	// Add data to buffer, keeping only the last maxSize bytes
	t.keepTail(p)

	return len(p), nil
}

// keepTail keeps only the last maxSize bytes in the buffer
func (t *TailBufferWriter) keepTail(p []byte) {
	// Append new data to buffer
	t.buf = append(t.buf, p...)
	t.size += len(p)

	// If buffer exceeds maxSize, keep only the last maxSize bytes
	if t.size > t.maxSize {
		keep := t.size - t.maxSize
		copy(t.buf, t.buf[keep:])
		t.buf = t.buf[:t.maxSize]
		t.size = t.maxSize
	}
}

// Bytes returns the buffered bytes
func (t *TailBufferWriter) Bytes() []byte {
	return t.buf
}

// String returns the buffered bytes as a string
func (t *TailBufferWriter) String() string {
	return string(t.buf)
}

// Len returns the number of bytes buffered
func (t *TailBufferWriter) Len() int {
	return t.size
}

// Written returns the total number of bytes written
func (t *TailBufferWriter) Written() int64 {
	return t.written
}

// Truncated returns true if output was truncated
func (t *TailBufferWriter) Truncated() bool {
	return t.written > t.limit
}

// Reset clears the buffer
func (t *TailBufferWriter) Reset() {
	t.buf = t.buf[:0]
	t.size = 0
	t.written = 0
}

// LimitWriter is an io.Writer that limits the total bytes written
// and discards anything beyond the limit
type LimitWriter struct {
	w       io.Writer
	limit   int64
	written int64
}

// NewLimitWriter creates a new limit writer
func NewLimitWriter(w io.Writer, limit int64) *LimitWriter {
	return &LimitWriter{
		w:     w,
		limit: limit,
	}
}

// Write implements io.Writer
func (l *LimitWriter) Write(p []byte) (int, error) {
	remaining := l.limit - l.written
	if remaining <= 0 {
		// Discard everything
		return len(p), nil
	}

	if int64(len(p)) > remaining {
		p = p[:remaining]
	}

	n, err := l.w.Write(p)
	l.written += int64(n)
	return n, err
}

// Written returns the total number of bytes written
func (l *LimitWriter) Written() int64 {
	return l.written
}

// Truncated returns true if output was truncated
func (l *LimitWriter) Truncated() bool {
	return l.written >= l.limit
}

// MultiWriter writes to multiple writers
type MultiWriter struct {
	writers []io.Writer
}

// NewMultiWriter creates a new multi writer
func NewMultiWriter(writers ...io.Writer) *MultiWriter {
	return &MultiWriter{
		writers: writers,
	}
}

// Write implements io.Writer
func (m *MultiWriter) Write(p []byte) (int, error) {
	for _, w := range m.writers {
		n, err := w.Write(p)
		if err != nil {
			return n, err
		}
	}
	return len(p), nil
}

// TeeWriter writes to both a tail buffer and another writer
type TeeWriter struct {
	buf *TailBufferWriter
	w   io.Writer
}

// NewTeeWriter creates a new tee writer
func NewTeeWriter(buf *TailBufferWriter, w io.Writer) *TeeWriter {
	return &TeeWriter{
		buf: buf,
		w:   w,
	}
}

// Write implements io.Writer
func (t *TeeWriter) Write(p []byte) (int, error) {
	// Write to buffer
	t.buf.Write(p)

	// Write to underlying writer
	if t.w != nil {
		return t.w.Write(p)
	}
	return len(p), nil
}
