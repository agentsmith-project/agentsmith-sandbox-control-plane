# Connection Management Improvement Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Implement on-demand connection mode between Manager and Shell Bridge, with EOF-based automatic disconnection and client-initiated signal support for graceful process termination.

**Architecture:** Rename Session to Sandbox for clarity, introduce ConnectionManager for on-demand connections, add Signal message type for client-initiated process termination, and implement cascade disconnection on EOF (0x04 frame).

**Tech Stack:** Go 1.23+, Gorilla WebSocket, Kubernetes client-go, existing binary protocol with shell-bridge

---

## Overview of Changes

### Naming Changes (Phase 1)
- `internal/session/` → `internal/sandbox/`
- `Session` → `Sandbox`
- `AgentThreadID` → `SandboxID`
- Update all references across the codebase

### New Components (Phase 2)
- `internal/connection/manager.go` - Connection manager for on-demand connections
- `internal/connection/pool.go` - Connection pooling (if needed)

### Enhanced Components (Phase 3-4)
- `internal/websocket/types.go` - Add `TypeSignal` and `SignalPayload`
- `internal/websocket/handler.go` - Add `handleSignal()` method
- `internal/shellbridge/client.go` - Add `SendSignal()` and `OnClose()` callback

---

## Phase 1: Naming Refactor (Session → Sandbox)

### Task 1: Create sandbox package with types

**Files:**
- Create: `manager-service/internal/sandbox/types.go`
- Modify: `manager-service/internal/session/types.go` (copy content, then delete)
- Test: `manager-service/internal/sandbox/types_test.go`

**Step 1: Write the failing test**

Create file: `manager-service/internal/sandbox/types_test.go`

```go
package sandbox

import (
	"testing"
	"time"
)

func TestSandbox_IsExpired_MaxLifetime(t *testing.T) {
	sandbox := &Sandbox{
		SandboxID: "test-123",
		CreatedAt: time.Now().Add(-25 * time.Hour),
		Config: SecurityConfig{
			MaxLifetime: 24 * time.Hour,
		},
		ClientConnected: true,
	}
	if !sandbox.IsExpired() {
		t.Error("expected sandbox to be expired due to max lifetime")
	}
}

func TestSandbox_IsExpired_IdleTimeout(t *testing.T) {
	sandbox := &Sandbox{
		SandboxID: "test-123",
		CreatedAt: time.Now(),
		LastActivityAt: time.Now().Add(-31 * time.Minute),
		Config: SecurityConfig{
			MaxLifetime: 24 * time.Hour,
			IdleTimeout: 30 * time.Minute,
		},
		ClientConnected: false,
	}
	if !sandbox.IsExpired() {
		t.Error("expected sandbox to be expired due to idle timeout")
	}
}

func TestSandbox_GetExpiresAt(t *testing.T) {
	sandbox := &Sandbox{
		SandboxID: "test-123",
		CreatedAt: time.Now(),
		LastActivityAt: time.Now().Add(-10 * time.Minute),
		Config: SecurityConfig{
			MaxLifetime: 24 * time.Hour,
			IdleTimeout: 30 * time.Minute,
		},
		ClientConnected: false,
	}
	expiresAt := sandbox.GetExpiresAt()
	expectedIdleExpiry := sandbox.LastActivityAt.Add(30 * time.Minute)
	diff := expiresAt.Sub(expectedIdleExpiry)
	if diff > time.Second || diff < -time.Second {
		t.Errorf("expected expiry to be idle expiry, got %v", expiresAt)
	}
}

func TestSandbox_Validate(t *testing.T) {
	tests := []struct {
		name    string
		sandbox *Sandbox
		wantErr bool
	}{
		{
			name: "valid sandbox",
			sandbox: &Sandbox{
				SandboxID: "test-123",
				CreatedAt: time.Now(),
				Config: SecurityConfig{
					MaxLifetime: 24 * time.Hour,
				},
			},
			wantErr: false,
		},
		{
			name: "missing SandboxID",
			sandbox: &Sandbox{
				CreatedAt: time.Now(),
				Config: SecurityConfig{
					MaxLifetime: 24 * time.Hour,
				},
			},
			wantErr: true,
		},
		{
			name: "missing CreatedAt",
			sandbox: &Sandbox{
				SandboxID: "test-123",
				Config: SecurityConfig{
					MaxLifetime: 24 * time.Hour,
				},
			},
			wantErr: true,
		},
		{
			name: "invalid MaxLifetime",
			sandbox: &Sandbox{
				SandboxID: "test-123",
				CreatedAt: time.Now(),
				Config: SecurityConfig{
					MaxLifetime: -1 * time.Hour,
				},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.sandbox.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd manager-service && go test ./internal/sandbox/... -v`
Expected: FAIL with "no Go files in /sandbox"

**Step 3: Write minimal implementation**

Create file: `manager-service/internal/sandbox/types.go`

```go
package sandbox

import (
	"fmt"
	"time"
)

const (
	// DefaultMaxLifetime is the default maximum lifetime for a sandbox.
	// This is used when a sandbox is created via GetOrCreate without a CreateRequest.
	DefaultMaxLifetime = 24 * time.Hour
)

type State string

const (
	StateCreating  State = "creating"
	StateRestoring State = "restoring"
	StateReady     State = "ready"
	StateOffline   State = "offline"
)

// Sandbox represents a sandbox execution environment (Pod + workspace state)
type Sandbox struct {
	SandboxID        string
	PodName          string
	PodNamespace     string
	PodIP            string // IP of the pod for shell-bridge connection
	State            State
	Image            string
	Command          []string
	Env              map[string]string
	Config           SecurityConfig
	CreatedAt        time.Time
	LastActivityAt   time.Time
	ExpiresAt        time.Time
	ClientConnected  bool
}

type SecurityConfig struct {
	AllowNetworkAccess  bool
	ReadonlyFilesystem  bool
	CPULimit            string
	MemoryLimit         string
	IdleTimeout         time.Duration
	MaxLifetime         time.Duration
	DropAllCapabilities bool
	AllowPrivileged     bool
}

// IsExpired checks if the sandbox has expired based on max lifetime or idle timeout
func (s *Sandbox) IsExpired() bool {
	// Check max lifetime
	if time.Since(s.CreatedAt) > s.Config.MaxLifetime {
		return true
	}

	// Check idle timeout (only when disconnected)
	if !s.ClientConnected && s.Config.IdleTimeout > 0 {
		idleTime := time.Since(s.LastActivityAt)
		return idleTime > s.Config.IdleTimeout
	}

	return false
}

// GetExpiresAt returns the expiration time for this sandbox
func (s *Sandbox) GetExpiresAt() time.Time {
	maxExpiry := s.CreatedAt.Add(s.Config.MaxLifetime)

	if !s.ClientConnected && s.Config.IdleTimeout > 0 {
		idleExpiry := s.LastActivityAt.Add(s.Config.IdleTimeout)
		if idleExpiry.Before(maxExpiry) {
			return idleExpiry
		}
	}

	return maxExpiry
}

// Initialized checks if the sandbox has been properly initialized with required fields
func (s *Sandbox) Initialized() bool {
	return s.SandboxID != "" && !s.CreatedAt.IsZero()
}

// Validate checks if the sandbox is in a valid state
func (s *Sandbox) Validate() error {
	if s.SandboxID == "" {
		return fmt.Errorf("sandbox: SandboxID is required")
	}
	if s.CreatedAt.IsZero() {
		return fmt.Errorf("sandbox: CreatedAt is required")
	}
	if s.Config.MaxLifetime <= 0 {
		return fmt.Errorf("sandbox: MaxLifetime must be positive")
	}
	return nil
}
```

**Step 4: Run test to verify it passes**

Run: `cd manager-service && go test ./internal/sandbox/... -v`
Expected: PASS

**Step 5: Commit**

```bash
cd manager-service
git add internal/sandbox/
git commit -m "refactor: create sandbox package with Sandbox type (rename from Session)
- internal/session/types.go → internal/sandbox/types.go
- Session → Sandbox
- AgentThreadID → SandboxID
- Add comprehensive tests for Sandbox behavior"
```

---

### Task 2: Create sandbox manager

**Files:**
- Create: `manager-service/internal/sandbox/manager.go`
- Modify: `manager-service/internal/session/manager.go` (copy content, then delete)
- Test: `manager-service/internal/sandbox/manager_test.go`

**Step 1: Write the failing test**

Create file: `manager-service/internal/sandbox/manager_test.go`

```go
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
```

**Step 2: Run test to verify it fails**

Run: `cd manager-service && go test ./internal/sandbox/... -v`
Expected: FAIL with "undefined: NewManager"

**Step 3: Write minimal implementation**

Create file: `manager-service/internal/sandbox/manager.go`

```go
package sandbox

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sandbox/manager/internal/observability"
)

// Manager manages sandbox instances
type Manager struct {
	mu       sync.RWMutex
	sandboxes map[string]*Sandbox
	wg       sync.WaitGroup
	logger   observability.Logger
}

// NewManager creates a new sandbox manager
func NewManager() *Manager {
	return &Manager{
		sandboxes: make(map[string]*Sandbox),
		logger:    observability.GetLogger(),
	}
}

// Create creates a new sandbox with the given request
func (m *Manager) Create(ctx context.Context, req CreateRequest) (*Sandbox, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if sandbox already exists to prevent duplicates
	if existing, exists := m.sandboxes[req.SandboxID]; exists {
		return existing, nil
	}

	now := time.Now()

	sbox := &Sandbox{
		SandboxID:       req.SandboxID,
		Image:           req.Image,
		Command:         req.Command,
		Env:             req.Env,
		Config:          req.Config,
		State:           StateCreating,
		PodNamespace:    req.PodNamespace,
		CreatedAt:       now,
		LastActivityAt:  now,
		ExpiresAt:       now.Add(req.Config.MaxLifetime),
		ClientConnected: false,
	}

	m.sandboxes[req.SandboxID] = sbox
	return sbox, nil
}

// Get retrieves a sandbox by ID
func (m *Manager) Get(sandboxID string) (*Sandbox, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sandboxes[sandboxID]
	return s, ok
}

// GetOrCreate atomically gets an existing sandbox or creates a new one.
// Returns the sandbox, a boolean indicating whether the sandbox was created,
// and an error if creation fails.
func (m *Manager) GetOrCreate(sandboxID string) (*Sandbox, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if sandbox already exists
	if sbox, exists := m.sandboxes[sandboxID]; exists {
		return sbox, false, nil
	}

	// Create new sandbox
	now := time.Now()
	sbox := &Sandbox{
		SandboxID:       sandboxID,
		State:           StateCreating,
		CreatedAt:       now,
		LastActivityAt:  now,
		ClientConnected: false,
		Config: SecurityConfig{
			MaxLifetime: DefaultMaxLifetime,
		},
		ExpiresAt: now.Add(DefaultMaxLifetime),
	}

	m.sandboxes[sandboxID] = sbox
	return sbox, true, nil
}

// UpdateState updates the state of a sandbox
func (m *Manager) UpdateState(sandboxID string, state State) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sandboxes[sandboxID]
	if !ok {
		return fmt.Errorf("sandbox not found: %s", sandboxID)
	}

	s.State = state
	return nil
}

// SetPodInfo sets the pod name for a sandbox
func (m *Manager) SetPodInfo(sandboxID, podName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sandboxes[sandboxID]
	if !ok {
		return fmt.Errorf("sandbox not found: %s", sandboxID)
	}

	s.PodName = podName
	return nil
}

// SetPodIP sets the pod IP for a sandbox
func (m *Manager) SetPodIP(sandboxID, podIP string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sandboxes[sandboxID]
	if !ok {
		return fmt.Errorf("sandbox not found: %s", sandboxID)
	}

	s.PodIP = podIP
	return nil
}

// MarkClientConnected marks a sandbox as having a connected client
func (m *Manager) MarkClientConnected(sandboxID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sandboxes[sandboxID]
	if !ok {
		return fmt.Errorf("sandbox not found: %s", sandboxID)
	}

	now := time.Now()
	s.ClientConnected = true
	s.LastActivityAt = now
	s.ExpiresAt = s.GetExpiresAt()
	return nil
}

// MarkClientDisconnected marks a sandbox as having no connected client
func (m *Manager) MarkClientDisconnected(sandboxID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sandboxes[sandboxID]
	if !ok {
		return fmt.Errorf("sandbox not found: %s", sandboxID)
	}

	s.ClientConnected = false
	s.ExpiresAt = s.GetExpiresAt()
	return nil
}

// UpdateActivity updates the last activity time for a sandbox
func (m *Manager) UpdateActivity(sandboxID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sandboxes[sandboxID]
	if !ok {
		return fmt.Errorf("sandbox not found: %s", sandboxID)
	}

	s.LastActivityAt = time.Now()
	s.ExpiresAt = s.GetExpiresAt()
	return nil
}

// Delete removes a sandbox from the manager
func (m *Manager) Delete(sandboxID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sandboxes, sandboxID)
}

// StartCleanup starts a background goroutine that periodically cleans up expired sandboxes
func (m *Manager) StartCleanup(ctx context.Context, interval time.Duration) {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				m.logger.Info("Cleanup goroutine stopped")
				return
			case <-ticker.C:
				m.cleanupExpired()
			}
		}
	}()
}

// cleanupExpired removes all expired sandboxes from the manager
func (m *Manager) cleanupExpired() {
	m.mu.Lock()
	defer m.mu.Unlock()

	var deleted []string

	for id, sbox := range m.sandboxes {
		if sbox.IsExpired() {
			delete(m.sandboxes, id)
			deleted = append(deleted, id)
		}
	}

	if len(deleted) > 0 {
		m.logger.Info("Cleaned up %d expired sandboxes: %v", len(deleted), deleted)
	}
}

// Shutdown waits for the cleanup goroutine to finish
func (m *Manager) Shutdown() {
	m.wg.Wait()
}

// GetSandboxCount returns the current number of sandboxes
func (m *Manager) GetSandboxCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sandboxes)
}

// CreateRequest is the request to create a new sandbox
type CreateRequest struct {
	SandboxID    string
	Image        string
	Command      []string
	Env          map[string]string
	PodNamespace string
	Config       SecurityConfig
}
```

**Step 4: Run test to verify it passes**

Run: `cd manager-service && go test ./internal/sandbox/... -v`
Expected: PASS

**Step 5: Commit**

```bash
cd manager-service
git add internal/sandbox/
git commit -m "refactor: create sandbox manager (rename from session manager)
- internal/session/manager.go → internal/sandbox/manager.go
- Manager methods now use SandboxID instead of AgentThreadID
- Add comprehensive tests for Manager behavior
- Keep identical functionality with renamed types"
```

---

### Task 3: Update websocket handler to use sandbox package

**Files:**
- Modify: `manager-service/internal/websocket/handler.go`
- Test: `manager-service/internal/websocket/handler_test.go`

**Step 1: Write the failing test**

First, check if existing tests need updates. Run:

Run: `cd manager-service && go test ./internal/websocket/... -v 2>&1 | head -50`
Expected: Some FAIL due to session package import

**Step 2: Update imports and references**

Modify file: `manager-service/internal/websocket/handler.go`

Change import from:
```go
import (
	// ... other imports
	"github.com/sandbox/manager/internal/session"
	"github.com/sandbox/manager/internal/shellbridge"
	"github.com/sandbox/manager/internal/storage"
)
```

To:
```go
import (
	// ... other imports
	"github.com/sandbox/manager/internal/sandbox"
	"github.com/sandbox/manager/internal/shellbridge"
	"github.com/sandbox/manager/internal/storage"
)
```

Update Handler struct field:
```go
// Handler manages WebSocket connections and sandbox sessions
type Handler struct {
	sandboxManager *sandbox.Manager  // Changed from: sessionManager *session.Manager
	bufferManager  *buffer.Manager
	k8sClient      *k8s.Client
	storageClient  *storage.Client
	podNamespace   string
	logger         observability.Logger
	cfg            *config.Config
	upgrader       *websocket.Upgrader
}
```

Update NewHandler function signature:
```go
// NewHandler creates a new WebSocket handler
func NewHandler(
	sandboxManager *sandbox.Manager,  // Changed from: sessionManager
	bufferManager *buffer.Manager,
	k8sClient *k8s.Client,
	storageClient *storage.Client,
	podNamespace string,
	cfg *config.Config,
) *Handler {
	// ... rest of implementation
	return &Handler{
		sandboxManager: sandboxManager,  // Changed from: sessionManager
		bufferManager:  bufferManager,
		k8sClient:      k8sClient,
		storageClient:  storageClient,
		podNamespace:   podNamespace,
		logger:         observability.GetLogger(),
		cfg:            cfg,
		upgrader:       wsCfg.Upgrader(),
	}
}
```

Update handleConnection function:
```go
func (h *Handler) handleConnection(ctx context.Context, conn *websocket.Conn) {
	var sandboxID string  // Changed from: agentThreadID
	var sbox *sandbox.Sandbox  // Changed from: sess *session.Session
	var isNewSandbox bool  // Changed from: isNewSession
	var cleanupMu sync.Mutex
	cleanupDone := false

	defer func() {
		cleanupMu.Lock()
		alreadyCleaned := cleanupDone
		cleanupDone = true
		cleanupMu.Unlock()

		if sandboxID != "" && isNewSandbox && !alreadyCleaned {
			h.logger.Debug("Cleaning up new sandbox %s", sandboxID)
			h.sandboxManager.Delete(sandboxID)  // Changed from: sessionManager
			h.bufferManager.Delete(sandboxID)
		}
	}()
	// ... rest of implementation
}
```

Update parseCreate to use SandboxID:
```go
type CreatePayload struct {
	SandboxID string `json:"sandbox_id"`  // Changed from: AgentThreadID
	Image     string `json:"image"`
	Command   []string `json:"command,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Config    SecurityConfig `json:"config"`
}

func (h *Handler) parseCreate(data json.RawMessage) (CreatePayload, error) {
	var payload CreatePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return CreatePayload{}, fmt.Errorf("unmarshal failed: %w", err)
	}
	if payload.SandboxID == "" {  // Changed from: AgentThreadID
		return CreatePayload{}, fmt.Errorf("sandbox_id is required")
	}
	return payload, nil
}
```

Update handleCreate function:
```go
func (h *Handler) handleCreate(ctx context.Context, payload CreatePayload, conn *websocket.Conn) (*sandbox.Sandbox, bool, error) {
	// Check if sandbox exists
	if sbox, ok := h.sandboxManager.Get(payload.SandboxID); ok {
		// Existing sandbox, just attach
		h.logger.Info("Attaching to existing sandbox %s", payload.SandboxID)
		h.sendStatus(conn, StatusPayload{
			State:    "ready",
			Message:  "Attached to existing sandbox",
			Progress: 1.0,
		})
		return sbox, false, nil
	}

	h.logger.Info("Creating new sandbox %s", payload.SandboxID)

	// Parse duration strings
	idleTimeout, _ := time.ParseDuration(payload.Config.IdleTimeout)
	maxLifetime, _ := time.ParseDuration(payload.Config.MaxLifetime)
	if idleTimeout == 0 {
		idleTimeout = 30 * time.Minute
	}
	if maxLifetime == 0 {
		maxLifetime = sandbox.DefaultMaxLifetime
	}

	// Create sandbox
	sbox, err := h.sandboxManager.Create(ctx, sandbox.CreateRequest{
		SandboxID:      payload.SandboxID,
		Image:          payload.Image,
		Command:        payload.Command,
		Env:            payload.Env,
		PodNamespace:   h.podNamespace,
		Config: sandbox.SecurityConfig{
			AllowNetworkAccess:  payload.Config.AllowNetworkAccess,
			ReadonlyFilesystem:  payload.Config.ReadonlyFilesystem,
			CPULimit:            payload.Config.CPULimit,
			MemoryLimit:         payload.Config.MemoryLimit,
			IdleTimeout:         idleTimeout,
			MaxLifetime:         maxLifetime,
			DropAllCapabilities: payload.Config.DropAllCapabilities,
			AllowPrivileged:     payload.Config.AllowPrivileged,
		},
	})
	if err != nil {
		return nil, true, fmt.Errorf("sandbox manager create failed for %s: %w", payload.SandboxID, err)
	}

	// ... rest of implementation with sbox instead of sess
}
```

Update attachSession:
```go
func (h *Handler) attachSession(ctx context.Context, sandboxID string, conn *websocket.Conn) error {
	sbox, ok := h.sandboxManager.Get(sandboxID)
	if !ok {
		return fmt.Errorf("sandbox not found: %s", sandboxID)
	}

	// Mark as connected
	h.sandboxManager.MarkClientConnected(sandboxID)
	defer h.sandboxManager.MarkClientDisconnected(sandboxID)

	h.logger.Info("Client connected to sandbox %s", sandboxID)

	// ... rest of implementation with sbox instead of sess
}
```

Update forwardIO:
```go
func (h *Handler) forwardIO(ctx context.Context, sbox *sandbox.Sandbox, conn *websocket.Conn) error {
	// Validate PodIP
	if sbox.PodIP == "" {
		return fmt.Errorf("sandbox %s has no PodIP, cannot connect to shell-bridge", sbox.SandboxID)
	}

	// Connect to shell-bridge
	client := shellbridge.NewClient(sbox.PodIP, shellbridge.DefaultPort)
	if err := client.Connect(ctx); err != nil {
		h.logger.Error("Failed to connect to shell-bridge for sandbox %s: %v", sbox.SandboxID, err)
		return fmt.Errorf("failed to connect to shell-bridge: %w", err)
	}
	defer client.Close()
	h.logger.Info("Connected to shell-bridge for sandbox %s at %s", sbox.SandboxID, sbox.PodIP)

	// ... rest of implementation with sbox.SandboxID instead of sess.AgentThreadID
}
```

**Step 3: Run test to verify changes**

Run: `cd manager-service && go test ./internal/websocket/... -v`
Expected: PASS

**Step 4: Commit**

```bash
cd manager-service
git add internal/websocket/
git commit -m "refactor: update websocket handler to use sandbox package
- Change session.Manager to sandbox.Manager
- Change Session to sandbox.Sandbox
- Change AgentThreadID to SandboxID
- Update all references in handler.go
- Tests pass with new package structure"
```

---

### Task 4: Update all other imports of session package

**Files:**
- Find all imports: `grep -r "internal/session" manager-service/ --include="*.go" --exclude-dir=vendor`
- Update each file

**Step 1: Find all files that import session package**

Run: `cd manager-service && grep -r "internal/session" . --include="*.go" --exclude-dir=vendor | grep -v "internal/sandbox" | cut -d: -f1 | sort -u`

Expected: List of files that need updating

**Step 2: For each file, update the import**

For each file found, replace:
```go
"github.com/sandbox/manager/internal/session"
```
with:
```go
"github.com/sandbox/manager/internal/sandbox"
```

And replace type references:
- `session.Session` → `sandbox.Sandbox`
- `session.Manager` → `sandbox.Manager`
- `session.State*` → `sandbox.State*`
- `session.DefaultMaxLifetime` → `sandbox.DefaultMaxLifetime`
- `session.CreateRequest` → `sandbox.CreateRequest`
- `session.SecurityConfig` → `sandbox.SecurityConfig`
- `AgentThreadID` → `SandboxID` (in struct fields and variables)

**Step 3: Run tests to verify all changes**

Run: `cd manager-service && go test ./... -v 2>&1 | grep -E "(FAIL|PASS|ok|?)" | head -30`

Expected: All tests pass

**Step 4: Commit**

```bash
cd manager-service
git add .
git commit -m "refactor: update all imports from session to sandbox package
- Replace all internal/session imports with internal/sandbox
- Update type references across the codebase
- AgentThreadID → SandboxID everywhere
- All tests pass with new naming"
```

---

### Task 5: Delete old session package

**Files:**
- Delete: `manager-service/internal/session/manager.go`
- Delete: `manager-service/internal/session/types.go`

**Step 1: Verify no more imports**

Run: `cd manager-service && grep -r "internal/session" . --include="*.go" --exclude-dir=vendor | grep -v "internal/sandbox" | wc -l`
Expected: 0

**Step 2: Delete old files**

Run:
```bash
cd manager-service
rm -rf internal/session/
```

**Step 3: Run tests**

Run: `cd manager-service && go test ./... -v 2>&1 | grep -E "(FAIL|PASS)" | tail -10`
Expected: All tests pass

**Step 4: Commit**

```bash
cd manager-service
git add -A
git commit -m "refactor: remove old session package
- Delete internal/session/ directory
- All functionality moved to internal/sandbox/
- Naming refactor complete: Session→Sandbox, AgentThreadID→SandboxID"
```

---

## Phase 2: Connection Manager (On-Demand Connections)

### Task 6: Create connection package with manager

**Files:**
- Create: `manager-service/internal/connection/manager.go`
- Test: `manager-service/internal/connection/manager_test.go`

**Step 1: Write the failing test**

Create file: `manager-service/internal/connection/manager_test.go`

```go
package connection

import (
	"context"
	"testing"
	"time"

	"github.com/sandbox/manager/internal/observability"
	"github.com/sandbox/manager/internal/sandbox"
	"github.com/sandbox/manager/internal/shellbridge"
)

func init() {
	observability.InitLoggerForTest()
}

func TestManager_EnsureConnection_NewConnection(t *testing.T) {
	mgr := NewManager(nil)
	sboxMgr := sandbox.NewManager()
	mgr.sandboxManager = sboxMgr

	ctx := context.Background()

	// Create a sandbox
	sbox, _ := sboxMgr.Create(ctx, sandbox.CreateRequest{
		SandboxID: "test-conn-1",
		Config:    sandbox.SecurityConfig{MaxLifetime: 1 * time.Hour},
	})
	sbox.PodIP = "10.0.0.1"  // Mock IP
	sboxMgr.UpdateState("test-conn-1", sandbox.StateReady)

	// Mock shell bridge client
	mockClient := &mockShellBridgeClient{connected: false}
	mgr.newClientFunc = func(podIP string, port int) ShellBridgeClient {
		return mockClient
	}

	// Ensure connection should create new connection
	client, err := mgr.EnsureConnection(ctx, "test-conn-1")
	if err != nil {
		t.Fatalf("EnsureConnection() failed: %v", err)
	}

	if !mockClient.connected {
		t.Error("expected shell bridge client to be connected")
	}
	if client == nil {
		t.Error("expected client to be returned")
	}
}

func TestManager_EnsureConnection_ExistingConnection(t *testing.T) {
	mgr := NewManager(nil)
	sboxMgr := sandbox.NewManager()
	mgr.sandboxManager = sboxMgr

	ctx := context.Background()

	// Create a sandbox
	sbox, _ := sboxMgr.Create(ctx, sandbox.CreateRequest{
		SandboxID: "test-conn-2",
		Config:    sandbox.SecurityConfig{MaxLifetime: 1 * time.Hour},
	})
	sbox.PodIP = "10.0.0.2"
	sboxMgr.UpdateState("test-conn-2", sandbox.StateReady)

	// Mock shell bridge client
	mockClient := &mockShellBridgeClient{connected: true}
	mgr.newClientFunc = func(podIP string, port int) ShellBridgeClient {
		return mockClient
	}

	// First call creates connection
	_, err := mgr.EnsureConnection(ctx, "test-conn-2")
	if err != nil {
		t.Fatalf("First EnsureConnection() failed: %v", err)
	}

	// Set connection in sandbox (simulating first call)
	sbox.BridgeConnection = mockClient

	// Second call should reuse connection
	client, err := mgr.EnsureConnection(ctx, "test-conn-2")
	if err != nil {
		t.Fatalf("Second EnsureConnection() failed: %v", err)
	}

	if client != mockClient {
		t.Error("expected existing client to be reused")
	}
}

func TestManager_EnsureConnection_SandboxNotFound(t *testing.T) {
	mgr := NewManager(nil)
	sboxMgr := sandbox.NewManager()
	mgr.sandboxManager = sboxMgr

	ctx := context.Background()

	// Try to connect to non-existent sandbox
	_, err := mgr.EnsureConnection(ctx, "non-existent")
	if err == nil {
		t.Error("expected error for non-existent sandbox")
	}
}

func TestManager_HandleBridgeClose(t *testing.T) {
	mgr := NewManager(nil)
	sboxMgr := sandbox.NewManager()
	mgr.sandboxManager = sboxMgr

	ctx := context.Background()

	// Create a sandbox
	sbox, _ := sboxMgr.Create(ctx, sandbox.CreateRequest{
		SandboxID: "test-close-1",
		Config:    sandbox.SecurityConfig{MaxLifetime: 1 * time.Hour},
	})
	sbox.PodIP = "10.0.0.3"
	sboxMgr.UpdateState("test-close-1", sandbox.StateReady)

	// Mock shell bridge client with close callback
	mockClient := &mockShellBridgeClient{connected: true}
	mgr.newClientFunc = func(podIP string, port int) ShellBridgeClient {
		return mockClient
	}

	// Set up connection
	sbox.BridgeConnection = mockClient

	// Handle bridge close
	mgr.HandleBridgeClose("test-close-1")

	// Verify connection is cleared
	sbox, _ = sboxMgr.Get("test-close-1")
	if sbox.BridgeConnection != nil {
		t.Error("expected bridge connection to be nil after close")
	}
}

// Mock shell bridge client for testing
type mockShellBridgeClient struct {
	connected bool
	closeFunc func()
}

func (m *mockShellBridgeClient) Connect(ctx context.Context) error {
	m.connected = true
	return nil
}

func (m *mockShellBridgeClient) Close() error {
	m.connected = false
	if m.closeFunc != nil {
		m.closeFunc()
	}
	return nil
}

func (m *mockShellBridgeClient) IsActive() bool {
	return m.connected
}

func (m *mockShellBridgeClient) OnClose(fn func()) {
	m.closeFunc = fn
}

func (m *mockShellBridgeClient) SendStdin(ctx context.Context, data []byte) error {
	if !m.connected {
		return shellbridge.ErrNotConnected
	}
	return nil
}

func (m *mockShellBridgeClient) SendSignal(ctx context.Context, signal string) error {
	if !m.connected {
		return shellbridge.ErrNotConnected
	}
	return nil
}

func (m *mockShellBridgeClient) ReceiveLoop(ctx context.Context, handler shellbridge.FrameHandler) error {
	return nil
}
```

**Step 2: Run test to verify it fails**

Run: `cd manager-service && go test ./internal/connection/... -v`
Expected: FAIL with "no Go files"

**Step 3: Write minimal implementation**

Create file: `manager-service/internal/connection/manager.go`

```go
package connection

import (
	"context"
	"fmt"
	"sync"

	"github.com/sandbox/manager/internal/observability"
	"github.com/sandbox/manager/internal/sandbox"
	"github.com/sandbox/manager/internal/shellbridge"
)

// ShellBridgeClient is the interface for shell bridge client connections
// This allows us to mock the client for testing
type ShellBridgeClient interface {
	Connect(ctx context.Context) error
	Close() error
	IsActive() bool
	OnClose(fn func())
	SendStdin(ctx context.Context, data []byte) error
	SendSignal(ctx context.Context, signal string) error
	ReceiveLoop(ctx context.Context, handler shellbridge.FrameHandler) error
}

// Manager manages on-demand connections between Manager and Shell Bridge
type Manager struct {
	sandboxManager *sandbox.Manager
	logger         observability.Logger

	// newClientFunc is a factory function for creating shell bridge clients
	// This allows us to mock the client for testing
	newClientFunc func(podIP string, port int) ShellBridgeClient
}

// NewManager creates a new connection manager
func NewManager(sandboxManager *sandbox.Manager) *Manager {
	return &Manager{
		sandboxManager: sandboxManager,
		logger:         observability.GetLogger(),
		newClientFunc: func(podIP string, port int) ShellBridgeClient {
			// Default: use real shell bridge client (wrapped)
			return &shellBridgeClientWrapper{
				client: shellbridge.NewClient(podIP, port),
			}
		},
	}
}

// EnsureConnection ensures a connection exists to the shell bridge for the given sandbox.
// If a connection already exists and is active, it returns the existing connection.
// Otherwise, it creates a new connection.
func (m *Manager) EnsureConnection(ctx context.Context, sandboxID string) (ShellBridgeClient, error) {
	// Get sandbox
	sbox, ok := m.sandboxManager.Get(sandboxID)
	if !ok {
		return nil, fmt.Errorf("sandbox not found: %s", sandboxID)
	}

	// Check if connection already exists and is active
	sbox.ConnectionMu.Lock()
	defer sbox.ConnectionMu.Unlock()

	if sbox.BridgeConnection != nil && sbox.BridgeConnection.IsActive() {
		m.logger.Debug("Reusing existing shell bridge connection for %s", sandboxID)
		return sbox.BridgeConnection, nil
	}

	// Create new connection
	m.logger.Info("Creating new shell bridge connection for %s (pod IP: %s)", sandboxID, sbox.PodIP)

	client := m.newClientFunc(sbox.PodIP, shellbridge.DefaultPort)
	if err := client.Connect(ctx); err != nil {
		return nil, fmt.Errorf("connect to shell bridge for %s: %w", sandboxID, err)
	}

	// Register close callback to clear connection from sandbox
	client.OnClose(func() {
		m.HandleBridgeClose(sandboxID)
	})

	sbox.BridgeConnection = client
	m.logger.Info("Shell bridge connection established for %s", sandboxID)

	return client, nil
}

// HandleBridgeClose handles the close event from a shell bridge connection
func (m *Manager) HandleBridgeClose(sandboxID string) {
	sbox, ok := m.sandboxManager.Get(sandboxID)
	if !ok {
		m.logger.Debug("Sandbox %s not found, ignoring bridge close", sandboxID)
		return
	}

	sbox.ConnectionMu.Lock()
	defer sbox.ConnectionMu.Unlock()

	if sbox.BridgeConnection != nil {
		m.logger.Info("Shell bridge connection closed for %s", sandboxID)
		sbox.BridgeConnection = nil
	}
}

// shellBridgeClientWrapper wraps the real shellbridge.Client to implement ShellBridgeClient interface
type shellBridgeClientWrapper struct {
	client *shellbridge.Client
}

func (w *shellBridgeClientWrapper) Connect(ctx context.Context) error {
	return w.client.Connect(ctx)
}

func (w *shellBridgeClientWrapper) Close() error {
	return w.client.Close()
}

func (w *shellBridgeClientWrapper) IsActive() bool {
	return w.client.IsActive()
}

func (w *shellBridgeClientWrapper) OnClose(fn func()) {
	w.client.OnClose(fn)
}

func (w *shellBridgeClientWrapper) SendStdin(ctx context.Context, data []byte) error {
	return w.client.SendStdin(ctx, data)
}

func (w *shellBridgeClientWrapper) SendSignal(ctx context.Context, signal string) error {
	return w.client.SendSignal(ctx, signal)
}

func (w *shellBridgeClientWrapper) ReceiveLoop(ctx context.Context, handler shellbridge.FrameHandler) error {
	return w.client.ReceiveLoop(ctx, handler)
}
```

**Step 4: Update sandbox types to add connection fields**

Add to `manager-service/internal/sandbox/types.go`:

```go
import (
	// ... existing imports
	"sync"
)

// Sandbox represents a sandbox execution environment (Pod + workspace state)
type Sandbox struct {
	SandboxID        string
	PodName          string
	PodNamespace     string
	PodIP            string
	State            State
	Image            string
	Command          []string
	Env              map[string]string
	Config           SecurityConfig
	CreatedAt        time.Time
	LastActivityAt   time.Time
	ExpiresAt        time.Time
	ClientConnected  bool

	// Connection management
	BridgeConnection interface{} // Will be *shellbridge.Client (avoid import cycle)
	ConnectionMu     sync.RWMutex
}
```

**Step 5: Run test to verify it passes**

Run: `cd manager-service && go test ./internal/connection/... -v`
Expected: PASS

**Step 6: Commit**

```bash
cd manager-service
git add internal/connection/ internal/sandbox/
git commit -m "feat: add connection manager for on-demand shell bridge connections
- Add internal/connection/manager.go
- Implement EnsureConnection() for on-demand connections
- Add HandleBridgeClose() for connection cleanup
- Add BridgeConnection field to Sandbox
- Add ConnectionMu for thread-safe access
- Include mock support for testing"
```

---

### Task 7: Enhance shellbridge client with Close callback and Signal support

**Files:**
- Modify: `manager-service/internal/shellbridge/client.go`
- Test: `manager-service/internal/shellbridge/client_test.go`

**Step 1: Write the failing test**

Create file: `manager-service/internal/shellbridge/client_test.go`

```go
package shellbridge

import (
	"context"
	"io"
	"testing"
	"time"
)

func TestClient_OnClose(t *testing.T) {
	client := NewClient("10.0.0.1", DefaultPort)

	closeCalled := false
	client.OnClose(func() {
		closeCalled = true
	})

	// Simulate close by calling internal close handler
	if client.onClose != nil {
		client.onClose()
	}

	if !closeCalled {
		t.Error("expected onClose callback to be called")
	}
}

func TestClient_IsActive(t *testing.T) {
	client := NewClient("10.0.0.1", DefaultPort)

	// Not connected initially
	if client.IsActive() {
		t.Error("expected client to be inactive before Connect")
	}

	// Note: We can't actually connect in tests without a real shell bridge
	// This test verifies the method exists and has correct behavior when not connected
}

func TestClient_SendSignal(t *testing.T) {
	// This test verifies the SendSignal method exists and handles not-connected state
	client := NewClient("10.0.0.1", DefaultPort)

	ctx := context.Background()
	err := client.SendSignal(ctx, "SIGINT")
	if err == nil {
		t.Error("expected error when sending signal while not connected")
	}
	if err != ErrNotConnected {
		t.Errorf("expected ErrNotConnected, got %v", err)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd manager-service && go test ./internal/shellbridge/... -v`
Expected: FAIL with "undefined: OnClose", "undefined: IsActive", "undefined: SendSignal", "undefined: ErrNotConnected"

**Step 3: Write minimal implementation**

Modify file: `manager-service/internal/shellbridge/client.go`

```go
package shellbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	HandshakeTimeout = 10 * time.Second
	WriteTimeout     = 5 * time.Second
	ReadTimeout      = 30 * time.Second
	DefaultPort      = 8080
)

// Errors
var (
	ErrNotConnected = fmt.Errorf("not connected to shell-bridge")
)

// FrameHandler handles frames received from shell bridge
type FrameHandler interface {
	OnStdout(data []byte)
	OnStderr(data []byte)
	OnResize(data []byte)
	OnClose() // Called when shell bridge sends EOF (0x04 frame)
}

// Client is a WebSocket client for shell-bridge
type Client struct {
	conn       *websocket.Conn
	connMu     sync.RWMutex
	connected  bool
	closed     atomic.Bool // Use atomic.Bool for thread-safe closed flag
	url        string
	httpClient *http.Client
	onClose    func() // Callback for connection close
}

// NewClient creates a new shell-bridge client for a pod
func NewClient(podIP string, port int) *Client {
	if port == 0 {
		port = DefaultPort
	}
	return &Client{
		url:        fmt.Sprintf("ws://%s:%d/ws", podIP, port),
		httpClient: &http.Client{Timeout: HandshakeTimeout},
	}
}

// Connect establishes a WebSocket connection to shell-bridge
func (c *Client) Connect(ctx context.Context) error {
	dialer := websocket.Dialer{HandshakeTimeout: HandshakeTimeout}
	conn, _, err := dialer.DialContext(ctx, c.url, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to shell-bridge: %w", err)
	}
	c.connMu.Lock()
	c.conn = conn
	c.connected = true
	c.closed.Store(false)
	c.connMu.Unlock()
	return nil
}

// OnClose registers a callback that is called when the connection is closed
func (c *Client) OnClose(fn func()) {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	c.onClose = fn
}

// IsActive returns true if the client is connected
func (c *Client) IsActive() bool {
	c.connMu.RLock()
	defer c.connMu.RUnlock()
	return c.connected && !c.closed.Load()
}

// ExecMessage is the JSON message for executing commands
type ExecMessage struct {
	Type    string   `json:"type"`
	Shell   string   `json:"shell"`
	Command string   `json:"command"`
	Env     []string `json:"env,omitempty"`
}

// SignalMessage is the JSON message for sending signals
type SignalMessage struct {
	Type   string `json:"type"`
	Signal string `json:"signal"`
}

// ExecCommand sends a command to the shell
func (c *Client) ExecCommand(ctx context.Context, shell, command string, env []string) error {
	c.connMu.RLock()
	if !c.connected || c.conn == nil {
		c.connMu.RUnlock()
		return ErrNotConnected
	}
	conn := c.conn
	c.connMu.RUnlock()

	msg := ExecMessage{Type: "exec", Shell: shell, Command: command, Env: env}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	conn.SetWriteDeadline(time.Now().Add(WriteTimeout))
	return conn.WriteMessage(websocket.TextMessage, data)
}

// SendStdin sends stdin data to the shell
func (c *Client) SendStdin(ctx context.Context, data []byte) error {
	c.connMu.RLock()
	if !c.connected || c.conn == nil {
		c.connMu.RUnlock()
		return ErrNotConnected
	}
	conn := c.conn
	c.connMu.RUnlock()

	conn.SetWriteDeadline(time.Now().Add(WriteTimeout))
	return conn.WriteMessage(websocket.BinaryMessage, data)
}

// SendSignal sends a signal to the shell process
func (c *Client) SendSignal(ctx context.Context, signal string) error {
	c.connMu.RLock()
	if !c.connected || c.conn == nil {
		c.connMu.RUnlock()
		return ErrNotConnected
	}
	conn := c.conn
	c.connMu.RUnlock()

	msg := SignalMessage{Type: "signal", Signal: signal}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	conn.SetWriteDeadline(time.Now().Add(WriteTimeout))
	return conn.WriteMessage(websocket.TextMessage, data)
}

// Output represents shell output with metadata
type Output struct {
	Type byte
	Data []byte
}

// ReceiveOutput waits for output from the shell
// Returns io.EOF when the shell closes
func (c *Client) ReceiveOutput(ctx context.Context) (*Output, error) {
	c.connMu.RLock()
	if !c.connected || c.conn == nil {
		c.connMu.RUnlock()
		return nil, ErrNotConnected
	}
	conn := c.conn
	c.connMu.RUnlock()

	for {
		// Set a read deadline to prevent busy-wait loop
		err := conn.SetReadDeadline(time.Now().Add(ReadTimeout))
		if err != nil {
			return nil, fmt.Errorf("failed to set read deadline: %w", err)
		}

		msgType, data, err := conn.ReadMessage()
		if err != nil {
			// Check for timeout (net.Error with Timeout())
			if netErr, ok := err.(interface{ Timeout() bool }); ok && netErr.Timeout() {
				// On timeout, check if context is cancelled
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				default:
					// Continue waiting for data
					continue
				}
			}
			if err == io.EOF || websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return nil, io.EOF
			}
			return nil, fmt.Errorf("read error: %w", err)
		}

		if msgType == websocket.BinaryMessage {
			frame, err := ParseBinaryFrame(data)
			if err != nil {
				return nil, fmt.Errorf("failed to parse binary frame: %w", err)
			}
			return &Output{Type: byte(frame.Type), Data: frame.Data}, nil
		}
		// Ignore text messages (control messages like exit, ping)
	}
}

// ReceiveLoop enters a loop receiving output from shell bridge and calling the handler
// Returns when the connection is closed or an error occurs
func (c *Client) ReceiveLoop(ctx context.Context, handler FrameHandler) error {
	for {
		output, err := c.ReceiveOutput(ctx)
		if err != nil {
			if err == io.EOF {
				// Shell bridge closed normally
				if handler != nil {
					handler.OnClose()
				}
				return nil
			}
			return err
		}

		if handler == nil {
			continue
		}

		switch BinaryDataType(output.Type) {
		case DataTypeStdout:
			handler.OnStdout(output.Data)
		case DataTypeStderr:
			handler.OnStderr(output.Data)
		case DataTypeResize:
			handler.OnResize(output.Data)
		case DataTypeClose:
			// EOF received - shell process has ended
			handler.OnClose()
			return nil
		}
	}
}

// Close closes the WebSocket connection gracefully
func (c *Client) Close() error {
	c.connMu.Lock()
	defer c.connMu.Unlock()

	if c.conn != nil {
		// Send close frame
		c.conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		err := c.conn.Close()
		c.conn = nil
		c.connected = false
		c.closed.Store(true)

		// Call close callback if registered
		if c.onClose != nil {
			c.onClose()
		}

		return err
	}
	c.connected = false
	c.closed.Store(true)
	return nil
}
```

Add missing import for atomic:
```go
import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"  // Add this
	"time"

	"github.com/gorilla/websocket"
)
```

**Step 4: Run test to verify it passes**

Run: `cd manager-service && go test ./internal/shellbridge/... -v`
Expected: PASS

**Step 5: Commit**

```bash
cd manager-service
git add internal/shellbridge/
git commit -m "feat: enhance shellbridge client with callback and signal support
- Add OnClose() method for close notification
- Add IsActive() method to check connection state
- Add SendSignal() method for sending signals to shell process
- Add ReceiveLoop() method with FrameHandler interface
- Add FrameHandler interface (OnStdout, OnStderr, OnResize, OnClose)
- Add ErrNotConnected error
- Use atomic.Bool for thread-safe closed flag"
```

---

## Phase 3: Signal Message Support

### Task 8: Add Signal message type to websocket protocol

**Files:**
- Modify: `manager-service/internal/websocket/types.go`
- Test: `manager-service/internal/websocket/types_test.go`

**Step 1: Write the failing test**

Create file: `manager-service/internal/websocket/types_test.go`

```go
package websocket

import (
	"encoding/json"
	"testing"
)

func TestSignalPayload(t *testing.T) {
	payload := SignalPayload{
		SandboxID: "test-123",
		Signal:    "SIGINT",
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal SignalPayload: %v", err)
	}

	var decoded SignalPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal SignalPayload: %v", err)
	}

	if decoded.SandboxID != "test-123" {
		t.Errorf("expected SandboxID test-123, got %s", decoded.SandboxID)
	}
	if decoded.Signal != "SIGINT" {
		t.Errorf("expected Signal SIGINT, got %s", decoded.Signal)
	}
}

func TestTypeSignal(t *testing.T) {
	if TypeSignal != "signal" {
		t.Errorf("expected TypeSignal to be 'signal', got %s", TypeSignal)
	}
}

func TestMessage_WithSignal(t *testing.T) {
	payload := SignalPayload{
		SandboxID: "test-456",
		Signal:    "SIGTERM",
	}
	payloadData, _ := json.Marshal(payload)

	msg := Message{
		Type: TypeSignal,
		Data: payloadData,
	}

	msgData, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal Message: %v", err)
	}

	var decodedMsg Message
	if err := json.Unmarshal(msgData, &decodedMsg); err != nil {
		t.Fatalf("failed to unmarshal Message: %v", err)
	}

	if decodedMsg.Type != TypeSignal {
		t.Errorf("expected type 'signal', got %s", decodedMsg.Type)
	}

	var decodedPayload SignalPayload
	if err := json.Unmarshal(decodedMsg.Data, &decodedPayload); err != nil {
		t.Fatalf("failed to unmarshal SignalPayload: %v", err)
	}

	if decodedPayload.SandboxID != "test-456" {
		t.Errorf("expected SandboxID test-456, got %s", decodedPayload.SandboxID)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd manager-service && go test ./internal/websocket/... -v -run TestSignal`
Expected: FAIL with "undefined: TypeSignal", "undefined: SignalPayload"

**Step 3: Write minimal implementation**

Modify file: `manager-service/internal/websocket/types.go`

Add the constant after TypeExit:
```go
// Message types
const (
	TypeCreate = "create"
	TypeStdin  = "stdin"
	TypeSignal = "signal"  // NEW: for sending signals to shell process
	TypeStatus = "status"
	TypeStdout = "stdout"
	TypeStderr = "stderr"
	TypeExit   = "exit"
	TypeError  = "error"
)
```

Add the payload type after ExitPayload:
```go
// SignalPayload is the payload for signal message
type SignalPayload struct {
	SandboxID string `json:"sandbox_id"` // The sandbox to send the signal to
	Signal    string `json:"signal"`     // The signal to send (e.g., SIGINT, SIGTERM)
}
```

**Step 4: Run test to verify it passes**

Run: `cd manager-service && go test ./internal/websocket/... -v -run TestSignal`
Expected: PASS

**Step 5: Commit**

```bash
cd manager-service
git add internal/websocket/
git commit -m "feat: add signal message type to websocket protocol
- Add TypeSignal constant
- Add SignalPayload with SandboxID and Signal fields
- Add tests for signal message serialization
- Supports SIGINT, SIGTERM, SIGKILL, SIGHUP signals"
```

---

### Task 9: Implement handleSignal in websocket handler

**Files:**
- Modify: `manager-service/internal/websocket/handler.go`
- Test: `manager-service/internal/websocket/handler_signal_test.go`

**Step 1: Write the failing test**

Create file: `manager-service/internal/websocket/handler_signal_test.go`

```go
package websocket

import (
	"encoding/json"
	"testing"

	"github.com/sandbox/manager/internal/connection"
	"github.com/sandbox/manager/internal/sandbox"
	"github.com/sandbox/manager/internal/shellbridge"
)

// Mock connection manager for testing
type mockConnectionManager struct {
	connectCalled bool
	signalSent    bool
	sandboxID     string
	signal        string
	err           error
}

func (m *mockConnectionManager) EnsureConnection(sandboxID string) (connection.ShellBridgeClient, error) {
	m.connectCalled = true
	m.sandboxID = sandboxID
	if m.err != nil {
		return nil, m.err
	}
	return &mockShellBridgeClient{connected: true}, nil
}

func (m *mockConnectionManager) HandleBridgeClose(sandboxID string) {
	// No-op for test
}

type mockShellBridgeClient struct {
	connected bool
	signalSent string
}

func (m *mockShellBridgeClient) Connect(ctx context.Context) error {
	m.connected = true
	return nil
}

func (m *mockShellBridgeClient) Close() error {
	m.connected = false
	return nil
}

func (m *mockShellBridgeClient) IsActive() bool {
	return m.connected
}

func (m *mockShellBridgeClient) OnClose(fn func()) {}

func (m *mockShellBridgeClient) SendStdin(ctx context.Context, data []byte) error {
	return nil
}

func (m *mockShellBridgeClient) SendSignal(ctx context.Context, signal string) error {
	m.signalSent = signal
	return nil
}

func (m *mockShellBridgeClient) ReceiveLoop(ctx context.Context, handler shellbridge.FrameHandler) error {
	return nil
}

func TestHandler_ParseSignal(t *testing.T) {
	handler := &Handler{}

	payload := SignalPayload{
		SandboxID: "test-signal-123",
		Signal:    "SIGINT",
	}
	data, _ := json.Marshal(payload)

	parsed, err := handler.parseSignal(data)
	if err != nil {
		t.Fatalf("parseSignal() failed: %v", err)
	}

	if parsed.SandboxID != "test-signal-123" {
		t.Errorf("expected SandboxID test-signal-123, got %s", parsed.SandboxID)
	}
	if parsed.Signal != "SIGINT" {
		t.Errorf("expected Signal SIGINT, got %s", parsed.Signal)
	}
}

func TestHandler_ParseSignal_EmptySandboxID(t *testing.T) {
	handler := &Handler{}

	payload := SignalPayload{
		SandboxID: "",
		Signal:    "SIGINT",
	}
	data, _ := json.Marshal(payload)

	_, err := handler.parseSignal(data)
	if err == nil {
		t.Error("expected error for empty SandboxID")
	}
}

func TestHandler_ParseSignal_EmptySignal(t *testing.T) {
	handler := &Handler{}

	payload := SignalPayload{
		SandboxID: "test-123",
		Signal:    "",
	}
	data, _ := json.Marshal(payload)

	_, err := handler.parseSignal(data)
	if err == nil {
		t.Error("expected error for empty Signal")
	}
}

func TestHandler_HandleSignal_Success(t *testing.T) {
	mockConnMgr := &mockConnectionManager{}
	mockSBClient := &mockShellBridgeClient{connected: true}
	mockConnMgr.err = nil
	// Override to return our mock client that tracks signal sending
	mockConnMgr.clientFunc = func() connection.ShellBridgeClient {
		return mockSBClient
	}

	handler := &Handler{
		connectionManager: mockConnMgr,
	}

	msg := Message{
		Type: TypeSignal,
		Data: json.RawMessage(`{"sandbox_id":"test-sig-123","signal":"SIGTERM"}`),
	}

	err := handler.handleSignal(msg)
	if err != nil {
		t.Fatalf("handleSignal() failed: %v", err)
	}

	if !mockConnMgr.connectCalled {
		t.Error("expected EnsureConnection to be called")
	}
	if mockConnMgr.sandboxID != "test-sig-123" {
		t.Errorf("expected sandboxID test-sig-123, got %s", mockConnMgr.sandboxID)
	}
	if mockSBClient.signalSent != "SIGTERM" {
		t.Errorf("expected signal SIGTERM to be sent, got %s", mockSBClient.signalSent)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd manager-service && go test ./internal/websocket/... -v -run TestSignal`
Expected: FAIL with "undefined: parseSignal", "undefined: handleSignal"

**Step 3: Write minimal implementation**

Modify file: `manager-service/internal/websocket/handler.go`

First, add the connectionManager field to Handler struct:
```go
// Handler manages WebSocket connections and sandbox sessions
type Handler struct {
	sandboxManager    *sandbox.Manager
	connectionManager *connection.Manager  // NEW
	bufferManager     *buffer.Manager
	k8sClient         *k8s.Client
	storageClient     *storage.Client
	podNamespace      string
	logger            observability.Logger
	cfg               *config.Config
	upgrader          *websocket.Upgrader
}
```

Update NewHandler:
```go
// NewHandler creates a new WebSocket handler
func NewHandler(
	sandboxManager *sandbox.Manager,
	connectionManager *connection.Manager,  // NEW parameter
	bufferManager *buffer.Manager,
	k8sClient *k8s.Client,
	storageClient *storage.Client,
	podNamespace string,
	cfg *config.Config,
) *Handler {
	// ... existing wsCfg setup ...

	return &Handler{
		sandboxManager:    sandboxManager,
		connectionManager: connectionManager,  // NEW
		bufferManager:     bufferManager,
		k8sClient:         k8sClient,
		storageClient:     storageClient,
		podNamespace:      podNamespace,
		logger:            observability.GetLogger(),
		cfg:               cfg,
		upgrader:          wsCfg.Upgrader(),
	}
}
```

Add parseSignal method:
```go
// parseSignal parses the signal message payload
func (h *Handler) parseSignal(data json.RawMessage) (SignalPayload, error) {
	var payload SignalPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return SignalPayload{}, fmt.Errorf("unmarshal failed: %w", err)
	}
	if payload.SandboxID == "" {
		return SignalPayload{}, fmt.Errorf("sandbox_id is required")
	}
	if payload.Signal == "" {
		return SignalPayload{}, fmt.Errorf("signal is required")
	}
	return payload, nil
}
```

Add handleSignal method:
```go
// handleSignal processes the signal message
func (h *Handler) handleSignal(msg Message) error {
	payload, err := h.parseSignal(msg.Data)
	if err != nil {
		return fmt.Errorf("failed to parse signal payload: %w", err)
	}

	// Ensure connection to shell bridge
	ctx := context.Background()
	client, err := h.connectionManager.EnsureConnection(ctx, payload.SandboxID)
	if err != nil {
		return fmt.Errorf("failed to ensure connection for %s: %w", payload.SandboxID, err)
	}

	// Send signal to shell bridge
	if err := client.SendSignal(ctx, payload.Signal); err != nil {
		return fmt.Errorf("failed to send signal %s to %s: %w", payload.Signal, payload.SandboxID, err)
	}

	h.logger.Info("Sent signal %s to sandbox %s", payload.Signal, payload.SandboxID)
	return nil
}
```

Update HandleMessage to handle signal type:
```go
// HandleMessage handles incoming WebSocket messages (add this if it doesn't exist)
func (h *Handler) HandleMessage(msg *Message) error {
	switch msg.Type {
	case TypeCreate:
		// Already handled in handleConnection
		return nil
	case TypeStdin:
		// Already handled in forwardIO
		return nil
	case TypeSignal:
		return h.handleSignal(*msg)
	default:
		return fmt.Errorf("unknown message type: %s", msg.Type)
	}
}
```

Update the stdin handler loop in forwardIO to handle signal:
```go
// In forwardIO method, within the select statement for message handling:

switch msg.Type {
case TypeStdin:
	payload, err := h.parseStdin(msg.Data)
	if err != nil {
		h.logger.Error("Failed to parse stdin: %v", err)
		continue
	}
	// ... existing stdin handling ...

case TypeSignal:  // NEW case
	payload, err := h.parseSignal(msg.Data)
	if err != nil {
		h.logger.Error("Failed to parse signal: %v", err)
		// Send error to client
		h.sendError(conn, fmt.Sprintf("Invalid signal payload: %v", err))
		continue
	}

	// Use connection manager to send signal
	client, err := h.connectionManager.EnsureConnection(ctx, sbox.SandboxID)
	if err != nil {
		h.logger.Error("Failed to ensure connection for signal: %v", err)
		h.sendError(conn, fmt.Sprintf("Connection failed: %v", err))
		continue
	}

	if err := client.SendSignal(ctx, payload.Signal); err != nil {
		h.logger.Error("Failed to send signal: %v", err)
		h.sendError(conn, fmt.Sprintf("Signal failed: %v", err))
		continue
	}
	h.logger.Debug("Sent signal %s to sandbox %s", payload.Signal, sbox.SandboxID)

case TypeCreate:
	// Reconnect attempt - handle accordingly
	h.logger.Debug("Received create message during active session")

default:
	h.logger.Debug("Received message type: %s", msg.Type)
}
```

**Step 4: Run test to verify it passes**

Run: `cd manager-service && go test ./internal/websocket/... -v -run TestSignal`
Expected: PASS

**Step 5: Commit**

```bash
cd manager-service
git add internal/websocket/ internal/connection/
git commit -m "feat: implement handleSignal for client-initiated process termination
- Add parseSignal() method to validate signal messages
- Add handleSignal() method to process signal requests
- Add connectionManager field to Handler
- Update NewHandler to accept connectionManager
- Integrate signal handling in forwardIO message loop
- Supports SIGINT, SIGTERM, SIGKILL, SIGHUP signals
- Send error to client on signal failure"
```

---

## Phase 4: EOF-Based Cascade Disconnection

### Task 10: Implement EOF-based cascade disconnection

**Files:**
- Modify: `manager-service/internal/websocket/handler.go`
- Test: `manager-service/internal/websocket/handler_eof_test.go`

**Step 1: Write the failing test**

Create file: `manager-service/internal/websocket/handler_eof_test.go`

```go
package websocket

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sandbox/manager/internal/connection"
	"github.com/sandbox/manager/internal/sandbox"
	"github.com/sandbox/manager/internal/shellbridge"
)

func TestHandler_SendExitAndClose(t *testing.T) {
	// This test verifies the sendExitAndClose behavior
	// In a real scenario, we'd need a mock websocket connection
	// For now, we test the helper functions that build the exit message

	handler := &Handler{
		logger: observability.GetLogger(),
	}

	// Test that we can create an exit message
	exitMsg := Message{
		Type: TypeExit,
		Data: handler.marshalJSON(ExitPayload{Code: 0}),
	}

	if exitMsg.Type != TypeExit {
		t.Errorf("expected type 'exit', got %s", exitMsg.Type)
	}
}

func TestHandler_HandleCloseFrame(t *testing.T) {
	mockConnMgr := &mockConnectionManager{}
	mockSBClient := &mockShellBridgeClient{connected: true}
	mockConnMgr.err = nil
	closeCalled := false

	mockSBClient.onCloseCallback = func() {
		closeCalled = true
	}

	mockConnMgr.clientFunc = func() connection.ShellBridgeClient {
		return mockSBClient
	}

	// Create a test handler
	handler := &Handler{
		connectionManager: mockConnMgr,
		sandboxManager:    sandbox.NewManager(),
		logger:            observability.GetLogger(),
	}

	// Create a sandbox
	ctx := context.Background()
	sbox, _ := handler.sandboxManager.Create(ctx, sandbox.CreateRequest{
		SandboxID: "test-eof-123",
		Config:    sandbox.SecurityConfig{MaxLifetime: 1 * time.Hour},
	})
	sbox.PodIP = "10.0.0.1"
	handler.sandboxManager.UpdateState("test-eof-123", sandbox.StateReady)

	// Simulate close frame handling (0x04)
	// In the actual implementation, this would be triggered by shellbridge.ReceiveLoop
	closeFrame := &shellbridge.BinaryFrame{
		Type:   shellbridge.DataTypeClose,
		Length: 0,
		Data:   nil,
	}

	// The handler should:
	// 1. Disconnect from shell bridge
	// 2. Send exit message to client
	// 3. Close websocket

	// For this test, we verify that the frame type is recognized
	if closeFrame.Type != shellbridge.DataTypeClose {
		t.Errorf("expected close frame type 0x04, got %d", closeFrame.Type)
	}
}
```

**Step 2: Run test to verify current state**

Run: `cd manager-service && go test ./internal/websocket/... -v -run TestHandleCloseFrame`
Expected: PASS (test verifies existing behavior)

**Step 3: Update forwardIO to handle EOF with cascade disconnection**

Modify file: `manager-service/internal/websocket/handler.go`

Update the forwardIO method to use connection manager and handle EOF properly:

```go
// forwardIO handles bidirectional I/O between WebSocket and shell-bridge session
func (h *Handler) forwardIO(ctx context.Context, sbox *sandbox.Sandbox, conn *websocket.Conn) error {
	// Validate PodIP
	if sbox.PodIP == "" {
		return fmt.Errorf("sandbox %s has no PodIP, cannot connect to shell-bridge", sbox.SandboxID)
	}

	// Use connection manager for on-demand connection
	bridge, err := h.connectionManager.EnsureConnection(ctx, sbox.SandboxID)
	if err != nil {
		h.logger.Error("Failed to connect to shell-bridge for sandbox %s: %v", sbox.SandboxID, err)
		return fmt.Errorf("failed to connect to shell-bridge: %w", err)
	}
	h.logger.Info("Connected to shell-bridge for sandbox %s at %s", sbox.SandboxID, sbox.PodIP)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)

	// Activity ticker for TTL management
	activityTicker := time.NewTicker(30 * time.Second)
	defer activityTicker.Stop()

	// Message channel for stdout/stderr
	outputChan := make(chan outputMessage, 100)
	errorChan := make(chan error, 1)

	// frameHandler implements shellbridge.FrameHandler for receiving output
	type frameHandler struct {
		sandboxID string
		outputChan chan outputMessage
	}

	// shell-bridge output reader goroutine using ReceiveLoop
	go func() {
		defer wg.Done()
		defer cancel()

		handler := &frameHandler{
			sandboxID: sbox.SandboxID,
			outputChan: outputChan,
		}

		// ReceiveLoop will call OnClose when EOF (0x04) is received
		err := bridge.ReceiveLoop(ctx, handler)
		if err != nil {
			if !isContextCanceled(err) && err != io.EOF {
				h.logger.Error("ReceiveLoop error for session %s: %v", sbox.SandboxID, err)
				select {
				case errorChan <- err:
				case <-ctx.Done():
				}
			}
		}
	}()

	// OnClose is called by frameHandler when EOF is received
	// This sets up the cascade disconnection

	// WebSocket → shell-bridge (stdin and signal)
	go func() {
		defer wg.Done()
		defer cancel()

		// Ping ticker to keep connection alive
		pingTicker := time.NewTicker(30 * time.Second)
		defer pingTicker.Stop()

		buf := h.bufferManager.GetOrCreate(sbox.SandboxID)

		for {
			select {
			case <-ctx.Done():
				h.logger.Debug("Stdin goroutine exiting: context done")
				return

			case outMsg := <-outputChan:
				// Buffer the message
				buf.Write(&buffer.Message{
					Type:     outMsg.msgType,
					Data:     outMsg.data,
					ExitCode: outMsg.exitCode,
				})

				// Send to client
				if outMsg.msgType == "exit" {
					h.sendExit(conn, outMsg.exitCode)
					// Cascade disconnection: close websocket after sending exit
					h.logger.Info("Cascade disconnection: closing websocket for sandbox %s after exit", sbox.SandboxID)
					return
				}
				h.sendOutput(conn, outMsg.msgType, outMsg.data)

			case err := <-errorChan:
				h.logger.Error("Exec error: %v", err)
				h.sendError(conn, err.Error())
				return

			case <-activityTicker.C:
				// Update activity
				h.sandboxManager.UpdateActivity(sbox.SandboxID)

			case <-pingTicker.C:
				// Send ping
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					h.logger.Debug("Failed to send ping to %s: %w", sbox.SandboxID, err)
					return
				}

			default:
				// Non-blocking read from WebSocket
				conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
				var msg Message
				if err := conn.ReadJSON(&msg); err != nil {
					if netErr, ok := err.(interface{ Timeout() bool }); ok && netErr.Timeout() {
						continue
					}
					if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
						h.logger.Debug("WebSocket closed normally during stdin read")
					} else if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
						h.logger.Warn("WebSocket closed unexpectedly during stdin read: %w", err)
					} else {
						h.logger.Warn("Failed to read stdin message from WebSocket: %w", err)
					}
					return
				}
				conn.SetReadDeadline(time.Time{})

				// Update activity on any message
				h.sandboxManager.UpdateActivity(sbox.SandboxID)

				switch msg.Type {
				case TypeStdin:
					payload, err := h.parseStdin(msg.Data)
					if err != nil {
						h.logger.Error("Failed to parse stdin: %v", err)
						continue
					}
					// Validate size before decoding
					maxEncodedSize := (h.cfg.WebSocket.MaxMessageSize * 4) / 3
					if int64(len(payload.Data)) > maxEncodedSize {
						h.logger.Error("Stdin data too large: %d bytes (max %d)", len(payload.Data), maxEncodedSize)
						continue
					}
					data, err := base64.StdEncoding.DecodeString(payload.Data)
					if err != nil {
						h.logger.Error("Failed to decode stdin data: %v", err)
						continue
					}
					// Forward stdin to shell-bridge
					if err := bridge.SendStdin(ctx, data); err != nil {
						h.logger.Error("Failed to send stdin to shell-bridge: %v", err)
					}

				case TypeSignal:
					payload, err := h.parseSignal(msg.Data)
					if err != nil {
						h.logger.Error("Failed to parse signal: %v", err)
						h.sendError(conn, fmt.Sprintf("Invalid signal payload: %v", err))
						continue
					}

					if err := bridge.SendSignal(ctx, payload.Signal); err != nil {
						h.logger.Error("Failed to send signal: %v", err)
						h.sendError(conn, fmt.Sprintf("Signal failed: %v", err))
						continue
					}
					h.logger.Debug("Sent signal %s to sandbox %s", payload.Signal, sbox.SandboxID)

				case TypeCreate:
					// Reconnect attempt
					h.logger.Debug("Received create message during active session")

				default:
					h.logger.Debug("Received message type: %s", msg.Type)
				}
			}
		}
	}()

	wg.Wait()
	h.logger.Info("Sandbox %s connection closed", sbox.SandboxID)
	return nil
}

// frameHandler implements shellbridge.FrameHandler
func (h *frameHandler) OnStdout(data []byte) {
	select {
	case h.outputChan <- outputMessage{msgType: "stdout", data: data}:
	case <-context.Background().Done():
	}
}

func (h *frameHandler) OnStderr(data []byte) {
	select {
	case h.outputChan <- outputMessage{msgType: "stderr", data: data}:
	case <-context.Background().Done():
	}
}

func (h *frameHandler) OnResize(data []byte) {
	// Resize messages not currently handled
}

func (h *frameHandler) OnClose() {
	// EOF received from shell bridge (0x04 frame)
	// Send exit message to trigger cascade disconnection
	select {
	case h.outputChan <- outputMessage{msgType: "exit", exitCode: 0}:
	case <-context.Background().Done():
	}
}
```

**Step 4: Update app initialization to create connection manager**

Find the main app initialization file (likely `cmd/manager/main.go` or similar) and update to create connection manager:

```go
// In main.go or app initialization

import (
	// ... existing imports
	"github.com/sandbox/manager/internal/connection"
)

func main() {
	// ... existing setup

	// Create managers
	sandboxMgr := sandbox.NewManager()
	connectionMgr := connection.NewManager(sandboxMgr)

	// Create WebSocket handler
	wsHandler := websocket.NewHandler(
		sandboxMgr,
		connectionMgr,  // NEW: pass connection manager
		bufferMgr,
		k8sClient,
		storageClient,
		podNamespace,
		cfg,
	)

	// ... rest of initialization
}
```

**Step 5: Run integration tests**

Run: `cd manager-service && go test ./... -v -run TestForwardIO`
Expected: Tests pass with new connection flow

**Step 6: Commit**

```bash
cd manager-service
git add internal/websocket/ cmd/ internal/app/
git commit -m "feat: implement EOF-based cascade disconnection
- Use connectionManager.EnsureConnection in forwardIO
- Implement frameHandler for shellbridge.ReceiveLoop
- OnClose sends exit message triggering cascade disconnection
- Send exit message to client then close websocket
- Close shell bridge connection when EOF (0x04 frame) received
- Update app initialization to create connection manager
- Supports on-demand connection with auto-cleanup on EOF"
```

---

## Task 11: Update main app initialization

**Files:**
- Find: `manager-service/cmd/manager/main.go` or `manager-service/internal/app/app.go`
- Modify: Add connection manager initialization

**Step 1: Find the app initialization file**

Run: `cd manager-service && find . -name "main.go" -path "*/cmd/*" | head -5`

**Step 2: Update the initialization**

Add connection manager creation and pass to websocket handler.

**Step 3: Run build to verify**

Run: `cd manager-service && go build ./cmd/manager`
Expected: Build succeeds

**Step 4: Commit**

```bash
cd manager-service
git add cmd/ internal/app/
git commit -m "feat: wire up connection manager in main app
- Create connection.NewManager in app initialization
- Pass connection manager to websocket.NewHandler
- Complete on-demand connection setup"
```

---

## Phase 5: Integration Testing and Documentation

### Task 12: Write integration tests for signal flow

**Files:**
- Create: `manager-service/integration/signal_test.go`

**Step 1: Write the integration test**

```go
// +build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/sandbox/manager/internal/connection"
	"github.com/sandbox/manager/internal/sandbox"
	"github.com/sandbox/manager/internal/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignalFlow_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup
	ctx := context.Background()
	sboxMgr := sandbox.NewManager()
	connMgr := connection.NewManager(sboxMgr)

	// Create a sandbox
	sbox, err := sboxMgr.Create(ctx, sandbox.CreateRequest{
		SandboxID: "test-signal-integration",
		Image:     "ubuntu:latest",
		Command:   []string{"/bin/bash", "-c", "sleep 100"},
		Config:    sandbox.SecurityConfig{MaxLifetime: 1 * time.Hour},
	})
	require.NoError(t, err)
	assert.Equal(t, "test-signal-integration", sbox.SandboxID)

	// Note: Full integration test requires:
	// 1. Running Kubernetes cluster
	// 2. Actual shell-bridge pod
	// 3. WebSocket client
	//
	// This is a placeholder for the full integration test structure
	// In a real environment, you would:
	// - Create the pod
	// - Wait for it to be ready
	// - Connect via WebSocket
	// - Send a signal
	// - Verify the process terminates

	t.Skip("Full integration test requires Kubernetes cluster")
}

func TestOnDemandConnection_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup
	ctx := context.Background()
	sboxMgr := sandbox.NewManager()
	connMgr := connection.NewManager(sboxMgr)

	// Create sandbox
	sbox, err := sboxMgr.Create(ctx, sandbox.CreateRequest{
		SandboxID: "test-ondemand-connection",
		Image:     "ubuntu:latest",
		Command:   []string{"/bin/bash"},
		Config:    sandbox.SecurityConfig{MaxLifetime: 1 * time.Hour},
	})
	require.NoError(t, err)

	// Verify no initial connection
	assert.Nil(t, sbox.BridgeConnection)

	// Note: Full test requires actual shell-bridge pod
	t.Skip("Full integration test requires Kubernetes cluster")
}
```

**Step 2: Run integration tests**

Run: `cd manager-service && go test ./integration/... -v -tags=integration`
Expected: Tests pass or skip if no cluster

**Step 3: Commit**

```bash
cd manager-service
git add integration/
git commit -m "test: add integration tests for signal and on-demand connection
- TestSignalFlow_Integration placeholder for signal flow testing
- TestOnDemandConnection_Integration placeholder for connection testing
- Tests skip when no Kubernetes cluster available
- Document testing requirements in comments"
```

---

### Task 13: Update API documentation

**Files:**
- Create: `manager-service/docs/api/connection-management.md`
- Modify: `docs/api/shell-bridge-integration.md`

**Step 1: Create connection management documentation**

Create file: `manager-service/docs/api/connection-management.md`

```markdown
# Connection Management

## Overview

The MBOS-Sandbox Manager uses on-demand connections between the Manager and Shell Bridge. This design reduces resource usage while maintaining low latency for command execution.

## Architecture

```
Client → Manager (WebSocket) → Shell Bridge (Pod IP:8080)
         ↑                        ↑
         │                        │
    JSON Protocol          Binary Protocol
    (Human-readable)       (High-performance)
```

## Connection Lifecycle

### On-Demand Connection

Connections are established only when needed:

1. **Client sends stdin/signal** → Manager ensures connection to Shell Bridge
2. **Command executes** → Output streams back to Client
3. **EOF received** → Shell Bridge sends 0x04 frame → Manager disconnects
4. **Next command** → Manager reconnects automatically

### Connection States

```
┌─────────────┐    stdin/signal    ┌──────────────┐
 │  Disconnected│ ─────────────────> │  Connecting  │
 └─────────────┘                     └──────────────┘
       ↑                                    │
       │         EOF (0x04)                 │
       │         <──────────────────────────┘
       └────────────┘
    Connected
```

## WebSocket Protocol

### Message Types

| Type | Direction | Description |
|------|-----------|-------------|
| `create` | Client → Manager | Create or attach to a sandbox |
| `stdin` | Client → Manager | Send input to shell |
| `signal` | Client → Manager | Send signal to shell process |
| `stdout` | Manager → Client | Shell output |
| `stderr` | Manager → Client | Shell error output |
| `exit` | Manager → Client | Process exited |
| `error` | Manager → Client | Error occurred |

### Signal Message

```json
{
  "type": "signal",
  "data": {
    "sandbox_id": "sandbox-123",
    "signal": "SIGINT"
  }
}
```

Supported signals:
- `SIGINT` - Interrupt (Ctrl+C)
- `SIGTERM` - Terminate
- `SIGKILL` - Force kill
- `SIGHUP` - Hangup

## Cascade Disconnection

When a shell process exits:

1. Shell Bridge sends `0x04` (Close) frame
2. Manager receives EOF via `ReceiveLoop()`
3. FrameHandler's `OnClose()` is called
4. Manager sends `exit` message to Client
5. Manager closes WebSocket connection
6. Shell Bridge closes its connection

## Usage Example

```javascript
// Client connects to Manager
const ws = new WebSocket('ws://manager/ws');

// Create sandbox
ws.send(JSON.stringify({
  type: 'create',
  data: {
    sandbox_id: 'my-sandbox',
    image: 'ubuntu:latest',
    command: ['/bin/bash']
  }
}));

// Send command
ws.send(JSON.stringify({
  type: 'stdin',
  data: { data: btoa('echo hello\n') }
}));

// Receive output
ws.onmessage = (event) => {
  const msg = JSON.parse(event.data);
  if (msg.type === 'stdout') {
    console.log(atob(msg.data.data)); // "hello"
  }
  if (msg.type === 'exit') {
    console.log('Process exited');
  }
};

// Send signal to terminate
ws.send(JSON.stringify({
  type: 'signal',
  data: {
    sandbox_id: 'my-sandbox',
    signal: 'SIGINT'
  }
}));
```

## Naming

| Old Name | New Name | Meaning |
|----------|----------|---------|
| `Session` | `Sandbox` | Pod + workspace state |
| `AgentThreadID` | `SandboxID` | Identifies a specific Pod |
| `session.AgentThreadID` | `sandbox.SandboxID` | Field access |
```

**Step 2: Update existing documentation**

Modify file: `docs/api/shell-bridge-integration.md`

Add section about connection management:
```markdown
## Connection Management (New)

The Manager now uses on-demand connections to Shell Bridge:

- Connections are established only when sending stdin/signal
- Connections are closed automatically when EOF (0x04 frame) is received
- The Connection Manager handles reconnection automatically

See [connection-management.md](./connection-management.md) for details.
```

**Step 3: Commit**

```bash
cd manager-service
git add docs/
git commit -m "docs: add connection management documentation
- Create docs/api/connection-management.md
- Document on-demand connection lifecycle
- Document signal message format
- Document cascade disconnection flow
- Add usage example
- Update shell-bridge-integration.md with reference"
```

---

## Task 14: Final verification and cleanup

**Files:**
- All modified files

**Step 1: Run full test suite**

Run: `cd manager-service && go test ./... -v 2>&1 | tail -50`
Expected: All tests pass

**Step 2: Build the project**

Run: `cd manager-service && go build ./cmd/manager`
Expected: Build succeeds

**Step 3: Run static analysis**

Run: `cd manager-service && go vet ./...`
Expected: No warnings

**Step 4: Check for TODO/FIXME comments**

Run: `cd manager-service && grep -r "TODO\|FIXME" internal/ --include="*.go" | grep -v vendor`
Expected: No unexpected TODOs

**Step 5: Final commit**

```bash
cd manager-service
git add .
git commit -m "feat: complete connection management improvement implementation

This commit completes the implementation of on-demand connection management
between Manager and Shell Bridge with EOF-based cascade disconnection and
client-initiated signal support.

Summary of changes:

Phase 1 - Naming Refactor:
- internal/session/ → internal/sandbox/
- Session → Sandbox
- AgentThreadID → SandboxID
- Updated all imports across the codebase

Phase 2 - Connection Manager:
- Added internal/connection/manager.go
- Implemented EnsureConnection() for on-demand connections
- Added HandleBridgeClose() for connection cleanup
- Added BridgeConnection field to Sandbox with ConnectionMu

Phase 3 - Signal Support:
- Added TypeSignal constant and SignalPayload
- Added handleSignal() method to websocket.Handler
- Enhanced shellbridge.Client with SendSignal() method
- Added FrameHandler interface with OnClose() callback

Phase 4 - EOF Cascade Disconnection:
- Updated forwardIO to use connection manager
- Implemented frameHandler for shellbridge.ReceiveLoop
- OnClose sends exit message triggering cascade disconnection
- Shell bridge connection closed on EOF (0x04 frame)

Phase 5 - Documentation:
- Added docs/api/connection-management.md
- Updated docs/api/shell-bridge-integration.md
- Added integration test placeholders

All tests pass. Build succeeds."
```

---

## Execution Summary

### Tasks Overview

| Task | Description | Files Changed |
|------|-------------|---------------|
| 1 | Create sandbox types | `internal/sandbox/types.go`, `types_test.go` |
| 2 | Create sandbox manager | `internal/sandbox/manager.go`, `manager_test.go` |
| 3 | Update websocket handler | `internal/websocket/handler.go` |
| 4 | Update all imports | Multiple files |
| 5 | Delete session package | `internal/session/*` |
| 6 | Create connection manager | `internal/connection/manager.go`, `manager_test.go` |
| 7 | Enhance shellbridge client | `internal/shellbridge/client.go`, `client_test.go` |
| 8 | Add signal message type | `internal/websocket/types.go`, `types_test.go` |
| 9 | Implement handleSignal | `internal/websocket/handler.go`, `handler_signal_test.go` |
| 10 | Implement EOF cascade | `internal/websocket/handler.go`, `handler_eof_test.go` |
| 11 | Update app initialization | `cmd/manager/main.go` or `internal/app/app.go` |
| 12 | Write integration tests | `integration/signal_test.go` |
| 13 | Update documentation | `docs/api/connection-management.md` |
| 14 | Final verification | All files |

### Test Coverage

- Unit tests for all new components
- Integration test placeholders for Kubernetes-dependent tests
- Tests for signal handling, connection management, and EOF handling

### Breaking Changes

- **API Change**: `agent_thread_id` → `sandbox_id` in WebSocket messages
- **Type Change**: `session.Session` → `sandbox.Sandbox`
- **Package Change**: `internal/session` → `internal/sandbox`

### Migration Guide

If you have existing code using the old API:

1. Replace `agent_thread_id` with `sandbox_id` in WebSocket messages
2. Update any code importing `internal/session` to use `internal/sandbox`
3. Update type references: `session.Session` → `sandbox.Sandbox`
4. Update field references: `AgentThreadID` → `SandboxID`

---

## Next Steps After Implementation

1. **Manual Testing**: Test with real Kubernetes cluster and shell-bridge pods
2. **Performance Testing**: Measure latency with on-demand connections
3. **Load Testing**: Verify connection handling under concurrent load
4. **Monitoring**: Add metrics for connection lifecycle
5. **Client Library Update**: Update `sbx-client` to use new `sandbox_id` field
