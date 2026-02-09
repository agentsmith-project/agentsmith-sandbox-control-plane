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
