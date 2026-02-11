package shellbridge

import (
	"context"
	"testing"
	"time"
)

func TestNewClient(t *testing.T) {
	client := NewClient("10.0.0.1", 8080)
	if client == nil {
		t.Fatal("NewClient returned nil")
	}
	if client.url != "ws://10.0.0.1:8080/ws" {
		t.Errorf("URL = %s, want ws://10.0.0.1:8080/ws", client.url)
	}
}

func TestNewClientDefaultPort(t *testing.T) {
	client := NewClient("10.0.0.1", 0)
	if client == nil {
		t.Fatal("NewClient returned nil")
	}
	if client.url != "ws://10.0.0.1:8080/ws" {
		t.Errorf("URL = %s, want ws://10.0.0.1:8080/ws", client.url)
	}
}

func TestClientClose(t *testing.T) {
	client := NewClient("10.0.0.1", 8080)
	err := client.Close()
	if err != nil {
		t.Errorf("Close() returned error: %v", err)
	}
}

func TestClientConnectTimeout(t *testing.T) {
	// Test that connection fails with invalid host
	client := NewClient("invalid.host.for.testing", 8080)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := client.Connect(ctx)
	if err == nil {
		t.Error("Connect() succeeded with invalid host, expected error")
	}
}

func TestExecCommandWithoutConnect(t *testing.T) {
	client := NewClient("10.0.0.1", 8080)
	ctx := context.Background()

	// Should return error, not panic
	err := client.ExecCommand(ctx, "bash", "echo test", nil)
	if err == nil {
		t.Error("ExecCommand() succeeded without Connect, expected error")
	}
}

func TestReceiveOutputWithoutConnect(t *testing.T) {
	client := NewClient("10.0.0.1", 8080)
	ctx := context.Background()

	// Should return error, not panic
	_, err := client.ReceiveOutput(ctx)
	if err == nil {
		t.Error("ReceiveOutput() succeeded without Connect, expected error")
	}
}

func TestExecCommandAfterClose(t *testing.T) {
	client := NewClient("10.0.0.1", 8080)
	ctx := context.Background()

	// Close without connecting
	_ = client.Close()

	// Should return error, not panic
	err := client.ExecCommand(ctx, "bash", "echo test", nil)
	if err == nil {
		t.Error("ExecCommand() succeeded after Close, expected error")
	}
}

func TestReceiveOutputAfterClose(t *testing.T) {
	client := NewClient("10.0.0.1", 8080)
	ctx := context.Background()

	// Close without connecting
	_ = client.Close()

	// Should return error, not panic
	_, err := client.ReceiveOutput(ctx)
	if err == nil {
		t.Error("ReceiveOutput() succeeded after Close, expected error")
	}
}

func TestClientCloseIdempotent(t *testing.T) {
	client := NewClient("10.0.0.1", 8080)

	// Multiple closes should not panic
	err := client.Close()
	if err != nil {
		t.Errorf("First Close() failed: %v", err)
	}

	err = client.Close()
	if err != nil {
		t.Errorf("Second Close() failed: %v", err)
	}
}

func TestClientConcurrentCloseAndExec(t *testing.T) {
	client := NewClient("10.0.0.1", 8080)
	ctx := context.Background()

	done := make(chan bool)

	// Try to close while another goroutine tries to exec
	go func() {
		for i := 0; i < 100; i++ {
			_ = client.ExecCommand(ctx, "bash", "ls", nil)
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 100; i++ {
			_ = client.Close()
		}
		done <- true
	}()

	// Wait for both goroutines (with timeout to avoid hanging)
	timeout := time.After(5 * time.Second)
	completed := 0
	for completed < 2 {
		select {
		case <-done:
			completed++
		case <-timeout:
			t.Error("Test timed out - possible deadlock")
			return
		}
	}
}

func TestOnCloseCallback(t *testing.T) {
	client := NewClient("10.0.0.1", 8080)

	callbackCalled := false
	client.OnClose(func() {
		callbackCalled = true
	})

	// Close the client
	err := client.Close()
	if err != nil {
		t.Errorf("Close() returned error: %v", err)
	}

	// Verify callback was called
	if !callbackCalled {
		t.Error("OnClose callback was not called")
	}
}

func TestOnCloseCallbackMultiple(t *testing.T) {
	client := NewClient("10.0.0.1", 8080)

	firstCalled := false
	secondCalled := false

	client.OnClose(func() {
		firstCalled = true
	})

	// Set a new callback (should replace the old one)
	client.OnClose(func() {
		secondCalled = true
	})

	err := client.Close()
	if err != nil {
		t.Errorf("Close() returned error: %v", err)
	}

	// Only the second callback should be called
	if firstCalled {
		t.Error("First OnClose callback was called (should be replaced)")
	}
	if !secondCalled {
		t.Error("Second OnClose callback was not called")
	}
}

func TestOnCloseCallbackNil(t *testing.T) {
	client := NewClient("10.0.0.1", 8080)

	// Set nil callback (should not panic)
	client.OnClose(nil)

	// Close should not panic
	err := client.Close()
	if err != nil {
		t.Errorf("Close() returned error: %v", err)
	}
}

func TestIsActive(t *testing.T) {
	client := NewClient("10.0.0.1", 8080)

	// Initially not active
	if client.IsActive() {
		t.Error("Client should not be active initially")
	}

	// After close, still not active
	_ = client.Close()
	if client.IsActive() {
		t.Error("Client should not be active after Close")
	}
}

func TestSendSignal(t *testing.T) {
	client := NewClient("10.0.0.1", 8080)
	ctx := context.Background()

	// Sending signal without connection should return error
	err := client.SendSignal(ctx, "SIGTERM")
	if err == nil {
		t.Error("SendSignal() succeeded without Connect, expected error")
	}
	if err != ErrNotConnected {
		t.Errorf("SendSignal() returned wrong error: %v, want ErrNotConnected", err)
	}
}

func TestSendSignalAfterClose(t *testing.T) {
	client := NewClient("10.0.0.1", 8080)
	ctx := context.Background()

	// Close without connecting
	_ = client.Close()

	// Should return error
	err := client.SendSignal(ctx, "SIGKILL")
	if err == nil {
		t.Error("SendSignal() succeeded after Close, expected error")
	}
	if err != ErrNotConnected {
		t.Errorf("SendSignal() returned wrong error: %v, want ErrNotConnected", err)
	}
}

func TestReceiveLoopWithoutConnect(t *testing.T) {
	client := NewClient("10.0.0.1", 8080)
	ctx := context.Background()

	// Create a test handler
	handler := &testFrameHandler{}

	// Should return error
	err := client.ReceiveLoop(ctx, handler)
	if err == nil {
		t.Error("ReceiveLoop() succeeded without Connect, expected error")
	}
	if err != ErrNotConnected {
		t.Errorf("ReceiveLoop() returned wrong error: %v, want ErrNotConnected", err)
	}
}

func TestReceiveLoopNilHandler(t *testing.T) {
	client := NewClient("10.0.0.1", 8080)
	ctx := context.Background()

	// Close without connecting
	_ = client.Close()

	// Should return error (not panic)
	err := client.ReceiveLoop(ctx, nil)
	if err == nil {
		t.Error("ReceiveLoop() succeeded with closed client, expected error")
	}
}

// testFrameHandler is a test implementation of FrameHandler
type testFrameHandler struct {
	stdoutCalled bool
	stderrCalled bool
	resizeCalled bool
	closeCalled  bool
	stdoutData   []byte
	stderrData   []byte
	resizeData   []byte
}

func (h *testFrameHandler) OnStdout(data []byte) {
	h.stdoutCalled = true
	h.stdoutData = data
}

func (h *testFrameHandler) OnStderr(data []byte) {
	h.stderrCalled = true
	h.stderrData = data
}

func (h *testFrameHandler) OnResize(data []byte) {
	h.resizeCalled = true
	h.resizeData = data
}

func (h *testFrameHandler) OnClose() {
	h.closeCalled = true
}
