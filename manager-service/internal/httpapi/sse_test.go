package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockFlusher implements http.ResponseWriter and http.Flusher
type mockFlusher struct {
	*httptest.ResponseRecorder
	flushed int
}

func (m *mockFlusher) Flush() {
	m.flushed++
}

func newMockFlusher() *mockFlusher {
	return &mockFlusher{ResponseRecorder: httptest.NewRecorder()}
}

// nonFlusher wraps a ResponseWriter without implementing http.Flusher
type nonFlusher struct {
	header     http.Header
	statusCode int
	body       []byte
}

func (n *nonFlusher) Header() http.Header       { return n.header }
func (n *nonFlusher) Write(p []byte) (int, error) { n.body = append(n.body, p...); return len(p), nil }
func (n *nonFlusher) WriteHeader(code int)        { n.statusCode = code }

func TestNewSSEWriter(t *testing.T) {
	t.Run("returns nil for non-flusher", func(t *testing.T) {
		w := &nonFlusher{header: http.Header{}}
		sse := NewSSEWriter(w)
		if sse != nil {
			t.Error("expected nil for non-flusher ResponseWriter")
		}
	})

	t.Run("returns writer for flusher", func(t *testing.T) {
		w := newMockFlusher()
		sse := NewSSEWriter(w)
		if sse == nil {
			t.Error("expected non-nil SSEWriter")
		}
	})
}

func TestSSEWriter_WriteHeaders(t *testing.T) {
	w := newMockFlusher()
	sse := NewSSEWriter(w)
	sse.WriteHeaders()

	header := w.Header()
	if ct := header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/event-stream")
	}
	if cc := header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want %q", cc, "no-cache")
	}
	if conn := header.Get("Connection"); conn != "keep-alive" {
		t.Errorf("Connection = %q, want %q", conn, "keep-alive")
	}
	if xab := header.Get("X-Accel-Buffering"); xab != "no" {
		t.Errorf("X-Accel-Buffering = %q, want %q", xab, "no")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if w.flushed < 1 {
		t.Error("expected Flush to be called")
	}
}

func TestSSEWriter_WriteEvent(t *testing.T) {
	w := newMockFlusher()
	sse := NewSSEWriter(w)
	sse.WriteHeaders()

	err := sse.WriteEvent("stdout", SSEOutputEvent{Data: "aGVsbG8="})
	if err != nil {
		t.Fatalf("WriteEvent failed: %v", err)
	}

	body := w.Body.String()
	if !strings.Contains(body, "event: stdout\n") {
		t.Errorf("body missing event line, got: %q", body)
	}
	if !strings.Contains(body, `"data":"aGVsbG8="`) {
		t.Errorf("body missing data, got: %q", body)
	}
}

func TestSSEWriter_ExitEvent(t *testing.T) {
	w := newMockFlusher()
	sse := NewSSEWriter(w)
	sse.WriteHeaders()

	err := sse.WriteEvent("exit", SSEExitEvent{ExitCode: 42, DurationMs: 1234})
	if err != nil {
		t.Fatalf("WriteEvent failed: %v", err)
	}

	body := w.Body.String()
	if !strings.Contains(body, "event: exit\n") {
		t.Errorf("body missing exit event, got: %q", body)
	}

	// Extract the data payload
	lines := strings.Split(body, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			var evt SSEExitEvent
			if err := json.Unmarshal([]byte(data), &evt); err != nil {
				t.Fatalf("failed to unmarshal exit event: %v", err)
			}
			if evt.ExitCode != 42 {
				t.Errorf("exit_code = %d, want 42", evt.ExitCode)
			}
			if evt.DurationMs != 1234 {
				t.Errorf("duration_ms = %d, want 1234", evt.DurationMs)
			}
			return
		}
	}
	t.Error("no data line found in response")
}

func TestStreamWriter_Write(t *testing.T) {
	w := newMockFlusher()
	sse := NewSSEWriter(w)
	sse.WriteHeaders()

	writer := sse.StdoutWriter()
	input := []byte("hello world")
	n, err := writer.Write(input)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != len(input) {
		t.Errorf("wrote %d bytes, want %d", n, len(input))
	}

	body := w.Body.String()
	expected := base64.StdEncoding.EncodeToString(input)
	if !strings.Contains(body, expected) {
		t.Errorf("body missing base64 data %q, got: %q", expected, body)
	}
	if !strings.Contains(body, "event: stdout") {
		t.Errorf("body missing stdout event, got: %q", body)
	}
}

func TestStreamWriter_EmptyWrite(t *testing.T) {
	w := newMockFlusher()
	sse := NewSSEWriter(w)
	sse.WriteHeaders()

	writer := sse.StderrWriter()
	n, err := writer.Write([]byte{})
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != 0 {
		t.Errorf("wrote %d bytes, want 0", n)
	}
}

func TestStreamWriter_StderrEvent(t *testing.T) {
	w := newMockFlusher()
	sse := NewSSEWriter(w)
	sse.WriteHeaders()

	writer := sse.StderrWriter()
	_, err := writer.Write([]byte("error msg"))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	body := w.Body.String()
	if !strings.Contains(body, "event: stderr") {
		t.Errorf("body missing stderr event, got: %q", body)
	}
}

func TestStreamWriter_String_TailBuffer(t *testing.T) {
	w := newMockFlusher()
	sse := NewSSEWriter(w)
	sse.WriteHeaders()

	writer := sse.StderrWriter()

	t.Run("captures written data", func(t *testing.T) {
		_, _ = writer.Write([]byte("hello world"))
		got := writer.String()
		if !strings.Contains(got, "hello world") {
			t.Errorf("String() = %q, want it to contain 'hello world'", got)
		}
	})

	t.Run("captures exit code marker", func(t *testing.T) {
		_, _ = writer.Write([]byte("\n__SBX_EXIT_CODE__=42\n"))
		got := writer.String()
		if !strings.Contains(got, "__SBX_EXIT_CODE__=42") {
			t.Errorf("String() = %q, want it to contain exit code marker", got)
		}
	})
}

func TestStreamWriter_String_TailBufferTruncation(t *testing.T) {
	w := newMockFlusher()
	sse := NewSSEWriter(w)
	sse.WriteHeaders()

	writer := sse.StderrWriter()

	// Write more than tailBufferSize bytes
	bigData := strings.Repeat("x", tailBufferSize+100)
	_, _ = writer.Write([]byte(bigData))

	// Then write a marker
	marker := "__SBX_EXIT_CODE__=7\n"
	_, _ = writer.Write([]byte(marker))

	got := writer.String()
	if len(got) > tailBufferSize {
		t.Errorf("tail buffer exceeded max size: got %d, max %d", len(got), tailBufferSize)
	}
	// The marker should still be present (it was the most recent write)
	if !strings.Contains(got, "__SBX_EXIT_CODE__=7") {
		t.Errorf("String() = %q, want it to contain exit code marker", got)
	}
}
