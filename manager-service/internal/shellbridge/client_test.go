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

	// This will panic because conn is nil
	// In production, always call Connect() first
	defer func() {
		if r := recover(); r == nil {
			t.Error("ExecCommand() did not panic with nil conn, expected panic")
		}
	}()
	_ = client.ExecCommand(ctx, "bash", "echo test", nil)
}
