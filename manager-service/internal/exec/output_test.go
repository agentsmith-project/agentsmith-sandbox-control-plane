package exec

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewTailBufferWriter_DefaultValues(t *testing.T) {
	writer := NewTailBufferWriter(1000, 100)

	assert.NotNil(t, writer)
	assert.Equal(t, int64(1000), writer.limit)
	assert.Equal(t, 100, writer.maxSize)
}

func TestNewTailBufferWriter_CustomTailSize(t *testing.T) {
	writer := NewTailBufferWriter(1000, 500)

	assert.Equal(t, 500, writer.maxSize) // min(1000, 500)
}

func TestTailBufferWriter_Write_SmallData(t *testing.T) {
	writer := NewTailBufferWriter(1000, 100)

	data := []byte("hello world")
	n, err := writer.Write(data)

	assert.NoError(t, err)
	assert.Equal(t, len(data), n)
	assert.Equal(t, int64(len(data)), writer.Written())
}

func TestTailBufferWriter_Write_LargeData(t *testing.T) {
	writer := NewTailBufferWriter(100, 50)

	// Write more than maxBytes
	data := bytes.Repeat([]byte("x"), 200)
	n, err := writer.Write(data)

	assert.NoError(t, err)
	assert.Equal(t, len(data), n)
	assert.Equal(t, int64(200), writer.Written())
	assert.True(t, writer.Truncated())
}

func TestTailBufferWriter_Bytes_ReturnsTail(t *testing.T) {
	writer := NewTailBufferWriter(100, 100)

	// Write exactly maxBytes
	data := bytes.Repeat([]byte("a"), 100)
	writer.Write(data)

	result := writer.Bytes()
	assert.Equal(t, 100, len(result))
	assert.Equal(t, data, result)
}

func TestTailBufferWriter_Bytes_Truncated(t *testing.T) {
	writer := NewTailBufferWriter(100, 50)

	// Write more than maxBytes
	data := bytes.Repeat([]byte("a"), 200)
	writer.Write(data)

	result := writer.Bytes()
	// Should return the last maxSize bytes (50)
	assert.Equal(t, 50, len(result))
	assert.Equal(t, bytes.Repeat([]byte("a"), 50), result)
}

func TestTailBufferWriter_String(t *testing.T) {
	writer := NewTailBufferWriter(1000, 100)

	data := []byte("hello world")
	writer.Write(data)

	assert.Equal(t, "hello world", writer.String())
}

func TestTailBufferWriter_Len(t *testing.T) {
	writer := NewTailBufferWriter(1000, 100)

	data := []byte("hello world")
	writer.Write(data)

	assert.Equal(t, len(data), writer.Len())
}

func TestTailBufferWriter_Len_Truncated(t *testing.T) {
	writer := NewTailBufferWriter(100, 50)

	data := bytes.Repeat([]byte("x"), 200)
	writer.Write(data)

	// The buffer keeps maxSize bytes (50)
	assert.Equal(t, 50, writer.Len())
}

func TestTailBufferWriter_Written_TracksTotal(t *testing.T) {
	writer := NewTailBufferWriter(100, 50)

	writer.Write(bytes.Repeat([]byte("x"), 50))
	assert.Equal(t, int64(50), writer.Written())

	writer.Write(bytes.Repeat([]byte("x"), 75))
	assert.Equal(t, int64(125), writer.Written())
}

func TestTailBufferWriter_Truncated_BeforeLimit(t *testing.T) {
	writer := NewTailBufferWriter(100, 50)

	writer.Write(bytes.Repeat([]byte("x"), 50))

	assert.False(t, writer.Truncated())
}

func TestTailBufferWriter_Truncated_AfterLimit(t *testing.T) {
	writer := NewTailBufferWriter(100, 50)

	writer.Write(bytes.Repeat([]byte("x"), 150))

	assert.True(t, writer.Truncated())
}

func TestTailBufferWriter_Reset_ClearsState(t *testing.T) {
	writer := NewTailBufferWriter(100, 50)

	writer.Write(bytes.Repeat([]byte("x"), 150))
	writer.Reset()

	assert.Equal(t, int64(0), writer.Written())
	assert.Equal(t, 0, writer.Len())
	assert.False(t, writer.Truncated())
}

func TestTailBufferWriter_Reset_RestoresState(t *testing.T) {
	writer := NewTailBufferWriter(100, 50)

	// Truncate the buffer
	writer.Write(bytes.Repeat([]byte("x"), 200))
	assert.Equal(t, int64(200), writer.Written())

	// Reset and write again
	writer.Reset()
	writer.Write(bytes.Repeat([]byte("y"), 50))

	assert.Equal(t, 50, writer.Len())
	assert.Equal(t, int64(50), writer.Written())
	assert.False(t, writer.Truncated())
}

func TestTailBufferWriter_MultipleWrites(t *testing.T) {
	writer := NewTailBufferWriter(1000, 100)

	writer.Write([]byte("hello "))
	writer.Write([]byte("world "))
	writer.Write([]byte("test"))

	assert.Equal(t, int64(16), writer.Written())
	assert.Equal(t, "hello world test", writer.String())
}

func TestTailBufferWriter_MultipleWrites_Truncation(t *testing.T) {
	writer := NewTailBufferWriter(100, 50)

	writer.Write(bytes.Repeat([]byte("a"), 60))
	writer.Write(bytes.Repeat([]byte("b"), 60))

	// Total written is 120, but buffer only keeps maxSize (50) bytes
	assert.Equal(t, int64(120), writer.Written())
	assert.Equal(t, 50, writer.Len())
	assert.True(t, writer.Truncated())

	result := writer.Bytes()
	// Should have the last 50 bytes (all 'b's)
	assert.Equal(t, bytes.Repeat([]byte("b"), 50), result)
}

func TestNewLimitWriter(t *testing.T) {
	buf := &bytes.Buffer{}
	writer := NewLimitWriter(buf, 100)

	assert.NotNil(t, writer)
}

func TestLimitWriter_Write_WithinLimit(t *testing.T) {
	buf := &bytes.Buffer{}
	writer := NewLimitWriter(buf, 100)

	data := []byte("hello world")
	n, err := writer.Write(data)

	assert.NoError(t, err)
	assert.Equal(t, len(data), n)
	assert.Equal(t, "hello world", buf.String())
}

func TestLimitWriter_Write_ExceedsLimit(t *testing.T) {
	buf := &bytes.Buffer{}
	writer := NewLimitWriter(buf, 10)

	data := bytes.Repeat([]byte("x"), 100)
	n, err := writer.Write(data)

	assert.NoError(t, err)
	assert.Equal(t, 10, n) // Only first 10 bytes are written
	// Buffer should only have first 10 bytes (the limit)
	assert.Equal(t, 10, buf.Len())
	assert.Equal(t, bytes.Repeat([]byte("x"), 10), buf.Bytes())
}

func TestLimitWriter_Write_Empty(t *testing.T) {
	buf := &bytes.Buffer{}
	writer := NewLimitWriter(buf, 100)

	data := []byte("")
	n, err := writer.Write(data)

	assert.NoError(t, err)
	assert.Equal(t, 0, n)
	assert.Equal(t, "", buf.String())
}

func TestLimitWriter_Tracked(t *testing.T) {
	buf := &bytes.Buffer{}
	writer := NewLimitWriter(buf, 100)

	writer.Write(bytes.Repeat([]byte("x"), 50))
	assert.Equal(t, int64(50), writer.Written())

	writer.Write(bytes.Repeat([]byte("x"), 75))
	assert.Equal(t, int64(100), writer.Written()) // capped at limit
}

func TestLimitWriter_Truncated(t *testing.T) {
	buf := &bytes.Buffer{}
	writer := NewLimitWriter(buf, 100)

	assert.False(t, writer.Truncated())

	writer.Write(bytes.Repeat([]byte("x"), 150))
	assert.True(t, writer.Truncated())
}

func TestNewMultiWriter(t *testing.T) {
	buf1 := &bytes.Buffer{}
	buf2 := &bytes.Buffer{}
	writer := NewMultiWriter(buf1, buf2)

	assert.NotNil(t, writer)
}

func TestMultiWriter_Write(t *testing.T) {
	buf1 := &bytes.Buffer{}
	buf2 := &bytes.Buffer{}
	writer := NewMultiWriter(buf1, buf2)

	data := []byte("hello world")
	n, err := writer.Write(data)

	assert.NoError(t, err)
	assert.Equal(t, len(data), n)
	assert.Equal(t, "hello world", buf1.String())
	assert.Equal(t, "hello world", buf2.String())
}

func TestNewTeeWriter(t *testing.T) {
	buf := NewTailBufferWriter(100, 50)
	w := &bytes.Buffer{}
	tee := NewTeeWriter(buf, w)

	assert.NotNil(t, tee)
	assert.Equal(t, buf, tee.buf)
	assert.Equal(t, w, tee.w)
}

func TestTeeWriter_Write(t *testing.T) {
	buf := NewTailBufferWriter(100, 50)
	w := &bytes.Buffer{}
	tee := NewTeeWriter(buf, w)

	data := []byte("hello world")
	n, err := tee.Write(data)

	assert.NoError(t, err)
	assert.Equal(t, len(data), n)
	assert.Equal(t, "hello world", buf.String())
	assert.Equal(t, "hello world", w.String())
}

func TestTeeWriter_Write_OnlyBuffer(t *testing.T) {
	buf := NewTailBufferWriter(100, 50)
	tee := NewTeeWriter(buf, nil)

	data := []byte("hello world")
	n, err := tee.Write(data)

	assert.NoError(t, err)
	assert.Equal(t, len(data), n)
	assert.Equal(t, "hello world", buf.String())
}
