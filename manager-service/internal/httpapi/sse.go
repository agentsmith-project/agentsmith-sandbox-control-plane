package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

// SSEWriter writes Server-Sent Events to an http.ResponseWriter.
// It is safe for concurrent use from multiple goroutines.
type SSEWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
	mu      sync.Mutex
}

// NewSSEWriter creates a new SSE writer. Returns nil if the ResponseWriter
// does not support flushing (required for SSE).
func NewSSEWriter(w http.ResponseWriter) *SSEWriter {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil
	}
	return &SSEWriter{w: w, flusher: flusher}
}

// WriteHeaders sets the required SSE response headers and flushes them.
func (s *SSEWriter) WriteHeaders() {
	s.w.Header().Set("Content-Type", "text/event-stream")
	s.w.Header().Set("Cache-Control", "no-cache")
	s.w.Header().Set("Connection", "keep-alive")
	s.w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering
	s.w.WriteHeader(http.StatusOK)
	s.flusher.Flush()
}

// SSEOutputEvent is the JSON payload for stdout/stderr SSE events.
type SSEOutputEvent struct {
	Data string `json:"data"`
}

// SSEExitEvent is the JSON payload for the exit SSE event.
type SSEExitEvent struct {
	ExitCode   int   `json:"exit_code"`
	DurationMs int64 `json:"duration_ms"`
}

// SSEErrorEvent is the JSON payload for error SSE events.
type SSEErrorEvent struct {
	Message string `json:"message"`
}

// WriteEvent writes a single SSE event. The data is JSON-encoded.
func (s *SSEWriter) WriteEvent(event string, data interface{}) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal SSE data: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	_, err = fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, jsonData)
	if err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

// StdoutWriter returns an io.Writer that emits SSE "stdout" events.
// Each Write call produces one SSE event with base64-encoded data.
func (s *SSEWriter) StdoutWriter() *StreamWriter {
	return &StreamWriter{sse: s, event: "stdout"}
}

// StderrWriter returns an io.Writer that emits SSE "stderr" events.
// Each Write call produces one SSE event with base64-encoded data.
func (s *SSEWriter) StderrWriter() *StreamWriter {
	return &StreamWriter{sse: s, event: "stderr"}
}

// tailBufferSize is the number of bytes kept in the tail buffer.
// This must be large enough to contain the exit code marker line
// (e.g., "__SBX_EXIT_CODE__=123\n" is ~25 bytes; 512 is generous).
const tailBufferSize = 512

// StreamWriter implements io.Writer and emits SSE events for each write.
// It also keeps a tail buffer of the most recent bytes so the server can
// extract the exit code marker after streaming completes.
type StreamWriter struct {
	sse   *SSEWriter
	event string // "stdout" or "stderr"
	mu    sync.Mutex
	tail  []byte
}

// Write implements io.Writer. Each call emits an SSE event with base64 data.
func (w *StreamWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	// Append to tail buffer (keep last tailBufferSize bytes)
	w.mu.Lock()
	w.tail = append(w.tail, p...)
	if len(w.tail) > tailBufferSize {
		w.tail = w.tail[len(w.tail)-tailBufferSize:]
	}
	w.mu.Unlock()

	encoded := base64.StdEncoding.EncodeToString(p)
	if err := w.sse.WriteEvent(w.event, SSEOutputEvent{Data: encoded}); err != nil {
		return 0, err
	}
	return len(p), nil
}

// String returns the tail of the captured output. This allows the exec layer
// to extract the exit code marker from stderr even in streaming mode.
func (w *StreamWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.tail)
}
