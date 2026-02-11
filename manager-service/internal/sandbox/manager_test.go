package sandbox

import (
	"context"
	"testing"
	"time"

	"github.com/sandbox/manager/internal/observability"
)

func init() {
	// Initialize logger for tests
	observability.InitLoggerForTest()
}

func TestManager_Create(t *testing.T) {
	mgr := NewManager()
	ctx := context.Background()

	req := CreateRequest{
		SandboxID:    "test-123",
		Image:        "ubuntu:latest",
		Command:      []string{"/bin/bash"},
		Env:          map[string]string{"TEST": "value"},
		PodNamespace: "default",
		Config: SecurityConfig{
			MaxLifetime: 1 * time.Hour,
		},
	}

	sandbox, err := mgr.Create(ctx, req)
	if err != nil {
		t.Fatalf("Create() failed: %v", err)
	}

	if sandbox.SandboxID != "test-123" {
		t.Errorf("expected SandboxID test-123, got %s", sandbox.SandboxID)
	}
	if sandbox.State != StateCreating {
		t.Errorf("expected StateCreating, got %s", sandbox.State)
	}
	if sandbox.Image != "ubuntu:latest" {
		t.Errorf("expected ubuntu:latest, got %s", sandbox.Image)
	}
}

func TestManager_Get(t *testing.T) {
	mgr := NewManager()
	ctx := context.Background()

	req := CreateRequest{
		SandboxID: "test-456",
		Config: SecurityConfig{
			MaxLifetime: 1 * time.Hour,
		},
	}

	// Get non-existent should fail
	_, ok := mgr.Get("test-456")
	if ok {
		t.Error("expected false for non-existent sandbox")
	}

	// Create then get should succeed
	_, err := mgr.Create(ctx, req)
	if err != nil {
		t.Fatalf("Create() failed: %v", err)
	}

	sandbox, ok := mgr.Get("test-456")
	if !ok {
		t.Error("expected true for existing sandbox")
	}
	if sandbox.SandboxID != "test-456" {
		t.Errorf("expected test-456, got %s", sandbox.SandboxID)
	}
}

func TestManager_GetOrCreate(t *testing.T) {
	mgr := NewManager()

	// First call should create
	sandbox1, created1, err := mgr.GetOrCreate("test-789")
	if err != nil {
		t.Fatalf("GetOrCreate() failed: %v", err)
	}
	if !created1 {
		t.Error("expected created=true on first call")
	}

	// Second call should get existing
	sandbox2, created2, err := mgr.GetOrCreate("test-789")
	if err != nil {
		t.Fatalf("GetOrCreate() failed: %v", err)
	}
	if created2 {
		t.Error("expected created=false on second call")
	}
	if sandbox1.SandboxID != sandbox2.SandboxID {
		t.Error("expected same sandbox")
	}
}

func TestManager_UpdateState(t *testing.T) {
	mgr := NewManager()
	ctx := context.Background()

	req := CreateRequest{
		SandboxID: "test-state",
		Config: SecurityConfig{
			MaxLifetime: 1 * time.Hour,
		},
	}

	_, err := mgr.Create(ctx, req)
	if err != nil {
		t.Fatalf("Create() failed: %v", err)
	}

	err = mgr.UpdateState("test-state", StateReady)
	if err != nil {
		t.Errorf("UpdateState() failed: %v", err)
	}

	sandbox, ok := mgr.Get("test-state")
	if !ok {
		t.Fatal("sandbox not found")
	}
	if sandbox.State != StateReady {
		t.Errorf("expected StateReady, got %s", sandbox.State)
	}
}

func TestManager_SetPodInfo(t *testing.T) {
	mgr := NewManager()
	ctx := context.Background()

	req := CreateRequest{
		SandboxID: "test-pod",
		Config: SecurityConfig{
			MaxLifetime: 1 * time.Hour,
		},
	}

	_, err := mgr.Create(ctx, req)
	if err != nil {
		t.Fatalf("Create() failed: %v", err)
	}

	err = mgr.SetPodInfo("test-pod", "pod-123")
	if err != nil {
		t.Errorf("SetPodInfo() failed: %v", err)
	}

	sandbox, ok := mgr.Get("test-pod")
	if !ok {
		t.Fatal("sandbox not found")
	}
	if sandbox.PodName != "pod-123" {
		t.Errorf("expected pod-123, got %s", sandbox.PodName)
	}
}

func TestManager_SetPodIP(t *testing.T) {
	mgr := NewManager()
	ctx := context.Background()

	req := CreateRequest{
		SandboxID: "test-ip",
		Config: SecurityConfig{
			MaxLifetime: 1 * time.Hour,
		},
	}

	_, err := mgr.Create(ctx, req)
	if err != nil {
		t.Fatalf("Create() failed: %v", err)
	}

	err = mgr.SetPodIP("test-ip", "10.0.0.1")
	if err != nil {
		t.Errorf("SetPodIP() failed: %v", err)
	}

	sandbox, ok := mgr.Get("test-ip")
	if !ok {
		t.Fatal("sandbox not found")
	}
	if sandbox.PodIP != "10.0.0.1" {
		t.Errorf("expected 10.0.0.1, got %s", sandbox.PodIP)
	}
}

func TestManager_MarkClientConnected(t *testing.T) {
	mgr := NewManager()
	ctx := context.Background()

	req := CreateRequest{
		SandboxID: "test-conn",
		Config: SecurityConfig{
			MaxLifetime: 1 * time.Hour,
			IdleTimeout: 30 * time.Minute,
		},
	}

	_, err := mgr.Create(ctx, req)
	if err != nil {
		t.Fatalf("Create() failed: %v", err)
	}

	// Mark connected
	err = mgr.MarkClientConnected("test-conn")
	if err != nil {
		t.Errorf("MarkClientConnected() failed: %v", err)
	}

	sandbox, ok := mgr.Get("test-conn")
	if !ok {
		t.Fatal("sandbox not found")
	}
	if !sandbox.ClientConnected {
		t.Error("expected ClientConnected=true")
	}

	// Mark disconnected
	err = mgr.MarkClientDisconnected("test-conn")
	if err != nil {
		t.Errorf("MarkClientDisconnected() failed: %v", err)
	}

	sandbox, _ = mgr.Get("test-conn")
	if sandbox.ClientConnected {
		t.Error("expected ClientConnected=false")
	}
}

func TestManager_UpdateActivity(t *testing.T) {
	mgr := NewManager()
	ctx := context.Background()

	req := CreateRequest{
		SandboxID: "test-activity",
		Config: SecurityConfig{
			MaxLifetime: 1 * time.Hour,
		},
	}

	sandbox, err := mgr.Create(ctx, req)
	if err != nil {
		t.Fatalf("Create() failed: %v", err)
	}

	oldActivity := sandbox.LastActivityAt
	time.Sleep(10 * time.Millisecond)

	err = mgr.UpdateActivity("test-activity")
	if err != nil {
		t.Errorf("UpdateActivity() failed: %v", err)
	}

	sandbox, _ = mgr.Get("test-activity")
	if !sandbox.LastActivityAt.After(oldActivity) {
		t.Error("expected LastActivityAt to be updated")
	}
}

func TestManager_Delete(t *testing.T) {
	mgr := NewManager()
	ctx := context.Background()

	req := CreateRequest{
		SandboxID: "test-delete",
		Config: SecurityConfig{
			MaxLifetime: 1 * time.Hour,
		},
	}

	_, err := mgr.Create(ctx, req)
	if err != nil {
		t.Fatalf("Create() failed: %v", err)
	}

	// Verify exists
	_, ok := mgr.Get("test-delete")
	if !ok {
		t.Error("expected sandbox to exist")
	}

	// Delete
	mgr.Delete("test-delete")

	// Verify deleted
	_, ok = mgr.Get("test-delete")
	if ok {
		t.Error("expected sandbox to be deleted")
	}
}

func TestManager_GetSessionCount(t *testing.T) {
	mgr := NewManager()
	ctx := context.Background()

	if mgr.GetSandboxCount() != 0 {
		t.Errorf("expected 0 sandboxes, got %d", mgr.GetSandboxCount())
	}

	req := CreateRequest{
		SandboxID: "test-count",
		Config: SecurityConfig{
			MaxLifetime: 1 * time.Hour,
		},
	}

	_, err := mgr.Create(ctx, req)
	if err != nil {
		t.Fatalf("Create() failed: %v", err)
	}

	if mgr.GetSandboxCount() != 1 {
		t.Errorf("expected 1 sandbox, got %d", mgr.GetSandboxCount())
	}
}
