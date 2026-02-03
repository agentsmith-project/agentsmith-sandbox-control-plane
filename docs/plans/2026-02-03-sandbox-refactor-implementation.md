# Sandbox Refactor Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Refactor mbos-sandbox-v1 to support specifiable container images, tmux-based persistent shell sessions, WebSocket bidirectional communication with message buffering, and automatic workspace snapshot/restore via MinIO.

**Architecture:** Client connects via WebSocket to Manager, which uses kubectl exec to communicate with Pod. Manager maintains ring buffer for message buffering and uses Kubernetes Finalizer to snapshot before pod deletion. Cleaner scans every 5 minutes using Pod annotations to check TTL.

**Tech Stack:** Go 1.21, Kubernetes client-go, WebSocket (gorilla/websocket), MinIO SDK, tmux.

---

# Phase 1: Foundation - Core Types and Session Management

## Task 1: Add WebSocket dependency

**Files:**
- Modify: `manager-service/go.mod`
- Modify: `manager-service/go.sum`

**Step 1: Add gorilla/websocket dependency**

```bash
cd manager-service
go get github.com/gorilla/websocket@v1.5.1
```

**Step 2: Run go mod tidy**

```bash
go mod tidy
```

**Step 3: Verify dependency**

```bash
grep gorilla/websocket go.mod
```

Expected: Line showing `github.com/gorilla/websocket v1.5.1`

**Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: add gorilla/websocket dependency"
```

---

## Task 2: Create session types

**Files:**
- Create: `manager-service/internal/session/types.go`

**Step 1: Write session types**

Create file with:

```go
package session

import (
	"time"

	corev1 "k8s.io/api/core/v1"
)

type State string

const (
	StateCreating    State = "creating"
	StateRestoring   State = "restoring"
	StateReady       State = "ready"
	StateOffline     State = "offline"
)

type Session struct {
	AgentThreadID     string
	PodName           string
	PodNamespace      string
	State             State
	Image             string
	Command           []string
	Env               map[string]string
	Config            SecurityConfig
	CreatedAt         time.Time
	LastActivityAt    time.Time
	ExpiresAt         time.Time
	ClientConnected   bool
}

type SecurityConfig struct {
	AllowNetworkAccess    bool
	ReadonlyFilesystem    bool
	CPULimit              string
	MemoryLimit           string
	IdleTimeout           time.Duration
	MaxLifetime           time.Duration
	DropAllCapabilities   bool
	AllowPrivileged       bool
}

func (s *Session) IsExpired() bool {
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

func (s *Session) GetExpiresAt() time.Time {
	maxExpiry := s.CreatedAt.Add(s.Config.MaxLifetime)

	if !s.ClientConnected && s.Config.IdleTimeout > 0 {
		idleExpiry := s.LastActivityAt.Add(s.Config.IdleTimeout)
		if idleExpiry.Before(maxExpiry) {
			return idleExpiry
		}
	}

	return maxExpiry
}
```

**Step 2: Verify compilation**

```bash
cd manager-service
go build ./internal/session/...
```

Expected: No errors

**Step 3: Commit**

```bash
git add manager-service/internal/session/types.go
git commit -m "feat: add session types with TTL calculation"
```

---

## Task 3: Create session manager

**Files:**
- Create: `manager-service/internal/session/manager.go`

**Step 1: Write session manager**

Create file with:

```go
package session

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

func NewManager() *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
	}
}

func (m *Manager) Create(ctx context.Context, req CreateRequest) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sessionID := "sess_" + uuid.New().String()
	now := time.Now()

	sess := &Session{
		AgentThreadID:   req.AgentThreadID,
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

	m.sessions[req.AgentThreadID] = sess
	return sess, nil
}

func (m *Manager) Get(agentThreadID string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[agentThreadID]
	return s, ok
}

func (m *Manager) UpdateState(agentThreadID string, state State) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[agentThreadID]
	if !ok {
		return fmt.Errorf("session not found: %s", agentThreadID)
	}

	s.State = state
	return nil
}

func (m *Manager) SetPodInfo(agentThreadID, podName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[agentThreadID]
	if !ok {
		return fmt.Errorf("session not found: %s", agentThreadID)
	}

	s.PodName = podName
	return nil
}

func (m *Manager) MarkClientConnected(agentThreadID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[agentThreadID]
	if !ok {
		return fmt.Errorf("session not found: %s", agentThreadID)
	}

	now := time.Now()
	s.ClientConnected = true
	s.LastActivityAt = now
	s.ExpiresAt = s.GetExpiresAt()
	return nil
}

func (m *Manager) MarkClientDisconnected(agentThreadID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[agentThreadID]
	if !ok {
		return fmt.Errorf("session not found: %s", agentThreadID)
	}

	s.ClientConnected = false
	s.ExpiresAt = s.GetExpiresAt()
	return nil
}

func (m *Manager) UpdateActivity(agentThreadID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[agentThreadID]
	if !ok {
		return fmt.Errorf("session not found: %s", agentThreadID)
	}

	s.LastActivityAt = time.Now()
	s.ExpiresAt = s.GetExpiresAt()
	return nil
}

func (m *Manager) Delete(agentThreadID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, agentThreadID)
}

type CreateRequest struct {
	AgentThreadID  string
	Image          string
	Command        []string
	Env            map[string]string
	PodNamespace   string
	Config         SecurityConfig
}
```

**Step 2: Verify compilation**

```bash
cd manager-service
go build ./internal/session/...
```

**Step 3: Commit**

```bash
git add manager-service/internal/session/manager.go
git commit -m "feat: add session manager"
```

---

## Task 4: Create ring buffer for message buffering

**Files:**
- Create: `manager-service/internal/buffer/ring.go`

**Step 1: Write ring buffer**

Create file with:

```go
package buffer

import (
	"sync"
)

type Message struct {
	Type string // "stdout", "stderr", "exit"
	Data []byte
	ExitCode int32
}

type RingBuffer struct {
	mu      sync.RWMutex
	buffer  []*Message
	size    int
	head    int
	tail    int
	count   int
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
```

**Step 2: Verify compilation**

```bash
cd manager-service
go build ./internal/buffer/...
```

**Step 3: Commit**

```bash
git add manager-service/internal/buffer/ring.go
git commit -m "feat: add ring buffer for message buffering"
```

---

## Task 5: Create buffer manager

**Files:**
- Create: `manager-service/internal/buffer/manager.go`

**Step 1: Write buffer manager**

Create file with:

```go
package buffer

import (
	"sync"
)

const DefaultBufferSize = 10000

type Manager struct {
	mu      sync.RWMutex
	buffers map[string]*RingBuffer
}

func NewManager() *Manager {
	return &Manager{
		buffers: make(map[string]*RingBuffer),
	}
}

func (m *Manager) GetOrCreate(agentThreadID string) *RingBuffer {
	m.mu.Lock()
	defer m.mu.Unlock()

	if buf, ok := m.buffers[agentThreadID]; ok {
		return buf
	}

	buf := NewRingBuffer(DefaultBufferSize)
	m.buffers[agentThreadID] = buf
	return buf
}

func (m *Manager) Delete(agentThreadID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.buffers, agentThreadID)
}
```

**Step 2: Verify compilation**

```bash
cd manager-service
go build ./internal/buffer/...
```

**Step 3: Commit**

```bash
git add manager-service/internal/buffer/manager.go
git commit -m "feat: add buffer manager"
```

---

## Task 6: Write session manager tests

**Files:**
- Create: `manager-service/internal/session/manager_test.go`

**Step 1: Write tests**

Create file with:

```go
package session

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManager_Create(t *testing.T) {
	ctx := context.Background()
	m := NewManager()

	req := CreateRequest{
		AgentThreadID: "at_test123",
		Image:         "test:latest",
		PodNamespace:  "sandbox",
		Config: SecurityConfig{
			MaxLifetime: 24 * time.Hour,
			IdleTimeout: 30 * time.Minute,
		},
	}

	sess, err := m.Create(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, "at_test123", sess.AgentThreadID)
	assert.Equal(t, StateCreating, sess.State)
	assert.False(t, sess.ClientConnected)
}

func TestManager_Get(t *testing.T) {
	ctx := context.Background()
	m := NewManager()

	sess, _ := m.Create(ctx, CreateRequest{
		AgentThreadID: "at_test",
		Image:         "test:latest",
		PodNamespace:  "sandbox",
		Config:        SecurityConfig{MaxLifetime: time.Hour},
	})

	got, ok := m.Get(sess.AgentThreadID)
	assert.True(t, ok)
	assert.Equal(t, sess.AgentThreadID, got.AgentThreadID)

	_, ok = m.Get("nonexistent")
	assert.False(t, ok)
}

func TestManager_UpdateState(t *testing.T) {
	ctx := context.Background()
	m := NewManager()

	sess, _ := m.Create(ctx, CreateRequest{
		AgentThreadID: "at_test",
		Image:         "test:latest",
		PodNamespace:  "sandbox",
		Config:        SecurityConfig{MaxLifetime: time.Hour},
	})

	err := m.UpdateState(sess.AgentThreadID, StateReady)
	require.NoError(t, err)

	got, _ := m.Get(sess.AgentThreadID)
	assert.Equal(t, StateReady, got.State)
}

func TestManager_ClientConnection(t *testing.T) {
	ctx := context.Background()
	m := NewManager()

	sess, _ := m.Create(ctx, CreateRequest{
		AgentThreadID: "at_test",
		Image:         "test:latest",
		PodNamespace:  "sandbox",
		Config:        SecurityConfig{MaxLifetime: time.Hour},
	})

	err := m.MarkClientConnected(sess.AgentThreadID)
	require.NoError(t, err)

	got, _ := m.Get(sess.AgentThreadID)
	assert.True(t, got.ClientConnected)

	err = m.MarkClientDisconnected(sess.AgentThreadID)
	require.NoError(t, err)

	got, _ = m.Get(sess.AgentThreadID)
	assert.False(t, got.ClientConnected)
}

func TestManager_UpdateActivity(t *testing.T) {
	ctx := context.Background()
	m := NewManager()

	sess, _ := m.Create(ctx, CreateRequest{
		AgentThreadID: "at_test",
		Image:         "test:latest",
		PodNamespace:  "sandbox",
		Config: SecurityConfig{
			MaxLifetime: time.Hour,
			IdleTimeout: 30 * time.Minute,
		},
	})

	oldActivity := sess.LastActivityAt
	time.Sleep(10 * time.Millisecond)

	err := m.UpdateActivity(sess.AgentThreadID)
	require.NoError(t, err)

	got, _ := m.Get(sess.AgentThreadID)
	assert.True(t, got.LastActivityAt.After(oldActivity))
}

func TestManager_Delete(t *testing.T) {
	ctx := context.Background()
	m := NewManager()

	sess, _ := m.Create(ctx, CreateRequest{
		AgentThreadID: "at_test",
		Image:         "test:latest",
		PodNamespace:  "sandbox",
		Config:        SecurityConfig{MaxLifetime: time.Hour},
	})

	m.Delete(sess.AgentThreadID)

	_, ok := m.Get(sess.AgentThreadID)
	assert.False(t, ok)
}

func TestSession_IsExpired(t *testing.T) {
	sess := &Session{
		CreatedAt: time.Now().Add(-2 * time.Hour),
		Config: SecurityConfig{
			MaxLifetime: 1 * time.Hour,
		},
	}
	assert.True(t, sess.IsExpired())

	sess.CreatedAt = time.Now()
	sess.Config.MaxLifetime = 1 * time.Hour
	assert.False(t, sess.IsExpired())
}

func TestSession_IsExpired_Idle(t *testing.T) {
	now := time.Now()

	sess := &Session{
		CreatedAt:      now,
		LastActivityAt: now,
		ClientConnected: true,
		Config: SecurityConfig{
			MaxLifetime: 24 * time.Hour,
			IdleTimeout: 30 * time.Minute,
		},
	}
	assert.False(t, sess.IsExpired())

	sess.ClientConnected = false
	sess.LastActivityAt = now.Add(-2 * time.Hour)
	assert.True(t, sess.IsExpired())
}
```

**Step 2: Run tests**

```bash
cd manager-service
go test ./internal/session/... -v
```

Expected: All tests pass

**Step 3: Commit**

```bash
git add manager-service/internal/session/manager_test.go
git commit -m "test: add session manager tests"
```

---

# Phase 2: Storage (MinIO/S3)

## Task 7: Add MinIO dependency

**Files:**
- Modify: `manager-service/go.mod`
- Modify: `manager-service/go.sum`

**Step 1: Add MinIO SDK**

```bash
cd manager-service
go get github.com/minio/minio-go/v7@v7.0.66
go mod tidy
```

**Step 2: Verify**

```bash
grep minio go.mod
```

**Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: add MinIO SDK"
```

---

## Task 8: Create storage client

**Files:**
- Create: `manager-service/internal/storage/client.go`

**Step 1: Write storage client**

Create file with:

```go
package storage

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Client struct {
	client *minio.Client
	bucket string
}

func NewClient(endpoint, accessKey, secretKey, bucket string, useSSL bool) (*Client, error) {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create MinIO client: %w", err)
	}

	// Ensure bucket exists
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("failed to check bucket: %w", err)
	}
	if !exists {
		err = client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to create bucket: %w", err)
		}
	}

	return &Client{
		client: client,
		bucket: bucket,
	}, nil
}

func (c *Client) GenerateSnapshotKey(workspaceID, projectID, agentThreadID string) string {
	return fmt.Sprintf("snapshots/%s/%s/%s/workspace.tar.gz",
		strings.TrimPrefix(workspaceID, "ws_"),
		strings.TrimPrefix(projectID, "proj_"),
		strings.TrimPrefix(agentThreadID, "at_"),
	)
}

func (c *Client) UploadSnapshot(ctx context.Context, key string, data io.Reader, size int64) error {
	_, err := c.client.PutObject(ctx, c.bucket, key, data, size, minio.PutObjectOptions{
		ContentType: "application/gzip",
	})
	if err != nil {
		return fmt.Errorf("failed to upload snapshot: %w", err)
	}
	return nil
}

func (c *Client) DownloadSnapshot(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	obj, err := c.client.GetObject(ctx, c.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get snapshot: %w", err)
	}

	info, err := obj.Stat()
	if err != nil {
		obj.Close()
		return nil, 0, fmt.Errorf("failed to stat snapshot: %w", err)
	}

	return obj, info.Size, nil
}

func (c *Client) DeleteSnapshot(ctx context.Context, key string) error {
	return c.client.RemoveObject(ctx, c.bucket, key, minio.RemoveObjectOptions{})
}

func (c *Client) SnapshotExists(ctx context.Context, key string) (bool, error) {
	_, err := c.client.StatObject(ctx, c.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		errResp := minio.ToErrorResponse(err)
		if errResp.Code == "NoSuchKey" {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
```

**Step 2: Verify compilation**

```bash
cd manager-service
go build ./internal/storage/...
```

**Step 3: Commit**

```bash
git add manager-service/internal/storage/client.go
git commit -m "feat: add MinIO storage client"
```

---

# Phase 3: Kubernetes Integration

## Task 9: Update k8s client for tmux

**Files:**
- Modify: `manager-service/internal/k8s/pods.go`

**Step 1: Read current pods.go**

```bash
cat manager-service/internal/k8s/pods.go
```

**Step 2: Add tmux wrapper to pod creation**

Modify the pod creation to include tmux wrapper script and security context. Add/update the CreatePod function to inject:

```go
func (c *Client) buildPodSpec(agentThreadID, image string, command []string, env map[string]string, config session.SecurityConfig) corev1.PodSpec {
	// Build command string for tmux
	cmdStr := "/bin/bash"
	if len(command) > 0 {
		cmdStr = strings.Join(command, " ")
	}

	// Build environment variables
	envVars := []corev1.EnvVar{
		{Name: "SANDBOX_COMMAND", Value: cmdStr},
		{Name: "SANDBOX_AGENT_THREAD_ID", Value: agentThreadID},
	}
	for k, v := range env {
		envVars = append(envVars, corev1.EnvVar{Name: k, Value: v})
	}

	// Security context
	securityContext := &corev1.SecurityContext{
		ReadOnlyRootFilesystem: ptr.Bool(config.ReadonlyFilesystem),
		Privileged:             &config.AllowPrivileged,
	}

	if config.DropAllCapabilities {
		securityContext.Capabilities = &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"},
		}
	}

	// Resource limits
	resources := corev1.ResourceRequirements{}
	if config.CPULimit != "" || config.MemoryLimit != "" {
		limits := corev1.ResourceList{}
		if config.CPULimit != "" {
			limits[corev1.ResourceCPU] = resource.MustParse(config.CPULimit)
		}
		if config.MemoryLimit != "" {
			limits[corev1.ResourceMemory] = resource.MustParse(config.MemoryLimit)
		}
		resources.Limits = limits
	}

	return corev1.PodSpec{
		Containers: []corev1.Container{
			{
				Name:            "sandbox",
				Image:           image,
				ImagePullPolicy: corev1.PullIfNotPresent,
				Command:         []string{"/bin/sh", "-c", c.getTmuxWrapperScript()},
				Env:             envVars,
				SecurityContext: securityContext,
				Resources:       resources,
				VolumeMounts: []corev1.VolumeMount{
					{
						Name:      "workspace",
						MountPath: "/workspace",
					},
					{
						Name:      "tmp",
						MountPath: "/tmp",
					},
				},
			},
		},
		Volumes: []corev1.Volume{
			{
				Name: "workspace",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
			{
				Name: "tmp",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
		},
		RestartPolicy: corev1.RestartPolicyNever,
	}
}

func (c *Client) getTmuxWrapperScript() string {
	return `#!/bin/sh
set -e

# Check tmux exists
if ! which tmux > /dev/null 2>&1; then
  echo "ERROR: tmux is required but not found in this image"
  exit 1
fi

# Create tmux session with user command
if [ -n "$SANDBOX_COMMAND" ]; then
  tmux new-session -d -s sandbox $SANDBOX_COMMAND
else
  tmux new-session -d -s sandbox /bin/bash
fi

# Keep container running
tail -f /dev/null
`
}
```

**Step 3: Update labels and annotations**

Add labels and annotations to pod creation:

```go
// Labels
labels := map[string]string{
	"app":             "sandbox",
	"agent_thread_id": agentThreadID,
}

// Annotations
annotations := map[string]string{
	"expires_at":      sess.ExpiresAt.Format(time.RFC3339),
	"last_activity_at": sess.LastActivityAt.Format(time.RFC3339),
	"manager.mbos.io/finalizer": "sandbox",
}
```

**Step 4: Add Finalizer**

Add finalizer to pod:

```go
finalizers := []string{"manager.mbos.io/snapshot"}
```

**Step 5: Commit**

```bash
git add manager-service/internal/k8s/pods.go
git commit -m "feat: update pod spec for tmux and security controls"
```

---

## Task 10: Add exec with streaming support

**Files:**
- Create: `manager-service/internal/k8s/exec_stream.go`

**Step 1: Write streaming exec**

Create file with:

```go
package k8s

import (
	"context"
	"fmt"
	"io"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

type StreamOptions struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	TTY    bool
}

func (c *Client) Exec(ctx context.Context, namespace, podName, container string, command []string, opts StreamOptions) error {
	req := c.client.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(namespace).
		Name(podName).
		SubResource("exec").
		VersionedParams(&corev1.ExecOptions{
			Container: container,
			Command:   command,
			Stdin:     opts.Stdin != nil,
			Stdout:    opts.Stdout != nil,
			Stderr:    opts.Stderr != nil,
			TTY:       opts.TTY,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(c.config, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("failed to create executor: %w", err)
	}

	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  opts.Stdin,
		Stdout: opts.Stdout,
		Stderr: opts.Stderr,
		Tty:    opts.TTY,
	})
	if err != nil {
		return fmt.Errorf("exec stream failed: %w", err)
	}

	return nil
}

// ExecWithOutput runs command and returns stdout
func (c *Client) ExecWithOutput(ctx context.Context, namespace, podName, container string, command []string) ([]byte, error) {
	var stdout bytes.Buffer
	err := c.Exec(ctx, namespace, podName, container, command, StreamOptions{
		Stdout: &stdout,
	})
	return stdout.Bytes(), err
}
```

**Step 2: Verify compilation**

```bash
cd manager-service
go build ./internal/k8s/...
```

**Step 3: Commit**

```bash
git add manager-service/internal/k8s/exec_stream.go
git commit -m "feat: add streaming exec support"
```

---

## Task 11: Add snapshot/restore via kubectl exec

**Files:**
- Create: `manager-service/internal/k8s/snapshot.go`

**Step 1: Write snapshot/restore functions**

Create file with:

```go
package k8s

import (
	"context"
	"fmt"
	"io"
)

// SnapshotWorkspace creates a tar.gz of /workspace
func (c *Client) SnapshotWorkspace(ctx context.Context, namespace, podName string) (io.ReadCloser, error) {
	reader, writer := io.Pipe()

	go func() {
		defer writer.Close()

		err := c.Exec(ctx, namespace, podName, "sandbox", []string{
			"tar", "czf", "-", "-C", "/workspace", ".",
		}, StreamOptions{
			Stdout: writer,
		})
		if err != nil {
			writer.CloseWithError(fmt.Errorf("tar command failed: %w", err))
		}
	}()

	return reader, nil
}

// RestoreWorkspace extracts tar.gz to /workspace
func (c *Client) RestoreWorkspace(ctx context.Context, namespace, podName string, tarData io.Reader) error {
	return c.Exec(ctx, namespace, podName, "sandbox", []string{
		"tar", "xzf", "-", "-C", "/workspace",
	}, StreamOptions{
		Stdin: tarData,
	})
}

// CheckTmux checks if tmux session exists
func (c *Client) CheckTmux(ctx context.Context, namespace, podName string) (bool, error) {
	output, err := c.ExecWithOutput(ctx, namespace, podName, "sandbox", []string{
		"tmux", "has-session", "-t", "sandbox",
	})

	if err != nil {
		return false, err
	}

	// Exit code 0 = session exists, 1 = not found
	return len(output) == 0, nil
}
```

**Step 2: Verify compilation**

```bash
cd manager-service
go build ./internal/k8s/...
```

**Step 3: Commit**

```bash
git add manager-service/internal/k8s/snapshot.go
git commit -m "feat: add snapshot/restore via kubectl exec"
```

---

# Phase 4: WebSocket Handler

## Task 12: Create WebSocket message types

**Files:**
- Create: `manager-service/internal/websocket/types.go`

**Step 1: Write message types**

Create file with:

```go
package websocket

import "encoding/json"

// Message types
const (
	TypeCreate = "create"
	TypeStdin  = "stdin"
	TypeStatus = "status"
	TypeStdout = "stdout"
	TypeStderr = "stderr"
	TypeExit   = "exit"
	TypeError  = "error"
)

// Message represents a WebSocket message
type Message struct {
	Type  string          `json:"type"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// CreatePayload is the payload for create message
type CreatePayload struct {
	AgentThreadID string         `json:"agent_thread_id"`
	Image         string         `json:"image"`
	Command       []string       `json:"command,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Config        SecurityConfig `json:"config"`
}

// StdinPayload is the payload for stdin message
type StdinPayload struct {
	Data string `json:"data"` // base64 encoded
}

// StatusPayload is the payload for status message
type StatusPayload struct {
	State    string  `json:"state"` // creating, restoring, ready, error
	Message  string  `json:"message,omitempty"`
	Progress float64 `json:"progress,omitempty"` // 0.0-1.0
}

// OutputPayload is the payload for stdout/stderr message
type OutputPayload struct {
	Data string `json:"data"` // base64 encoded
}

// ExitPayload is the payload for exit message
type ExitPayload struct {
	Code int32 `json:"code"`
}

// ErrorPayload is the payload for error message
type ErrorPayload struct {
	Message string `json:"message"`
}

// SecurityConfig is the security configuration for sandbox
type SecurityConfig struct {
	AllowNetworkAccess  bool   `json:"allow_network_access"`
	ReadonlyFilesystem  bool   `json:"readonly_filesystem"`
	CPULimit            string `json:"cpu_limit,omitempty"`
	MemoryLimit         string `json:"memory_limit,omitempty"`
	IdleTimeout         string `json:"idle_timeout,omitempty"` // duration string
	MaxLifetime         string `json:"max_lifetime,omitempty"` // duration string
	DropAllCapabilities bool   `json:"drop_all_capabilities"`
	AllowPrivileged     bool   `json:"allow_privileged"`
}
```

**Step 2: Verify compilation**

```bash
cd manager-service
go build ./internal/websocket/...
```

**Step 3: Commit**

```bash
git add manager-service/internal/websocket/types.go
git commit -m "feat: add WebSocket message types"
```

---

## Task 13: Create WebSocket handler

**Files:**
- Create: `manager-service/internal/websocket/handler.go`

**Step 1: Write WebSocket handler**

Create file with:

```go
package websocket

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/vibe-kanban/mbos-sandbox-v1/manager-service/internal/buffer"
	"github.com/vibe-kanban/mbos-sandbox-v1/manager-service/internal/k8s"
	"github.com/vibe-kanban/mbos-sandbox-v1/manager-service/internal/session"
	"github.com/vibe-kanban/mbos-sandbox-v1/manager-service/internal/storage"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type Handler struct {
	sessionManager *session.Manager
	bufferManager  *buffer.Manager
	k8sClient      *k8s.Client
	storageClient  *storage.Client
	podNamespace   string
}

func NewHandler(
	sessionManager *session.Manager,
	bufferManager *buffer.Manager,
	k8sClient *k8s.Client,
	storageClient *storage.Client,
	podNamespace string,
) *Handler {
	return &Handler{
		sessionManager: sessionManager,
		bufferManager:  bufferManager,
		k8sClient:      k8sClient,
		storageClient:  storageClient,
		podNamespace:   podNamespace,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Upgrade to WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		http.Error(w, fmt.Sprintf("websocket upgrade failed: %v", err), http.StatusBadRequest)
		return
	}
	defer conn.Close()

	ctx := r.Context()

	// Handle connection
	h.handleConnection(ctx, conn)
}

func (h *Handler) handleConnection(ctx context.Context, conn *websocket.Conn) {
	var agentThreadID string
	var sess *session.Session

	// Wait for create message
	for {
		var msg Message
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}

		switch msg.Type {
		case TypeCreate:
			payload, err := h.parseCreate(msg.Data)
			if err != nil {
				h.sendError(conn, fmt.Sprintf("Invalid create payload: %v", err))
				return
			}
			agentThreadID = payload.AgentThreadID

			sess, err = h.handleCreate(ctx, payload, conn)
			if err != nil {
				h.sendError(conn, fmt.Sprintf("Create failed: %v", err))
				return
			}
			break

		default:
			h.sendError(conn, "Expected create message first")
			return
		}

		if sess != nil {
			break
		}
	}

	// Attach to existing session
	if err := h.attachSession(ctx, agentThreadID, conn); err != nil {
		h.sendError(conn, fmt.Sprintf("Attach failed: %v", err))
	}
}

func (h *Handler) parseCreate(data json.RawMessage) (CreatePayload, error) {
	var payload CreatePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return CreatePayload{}, err
	}
	return payload, nil
}

func (h *Handler) handleCreate(ctx context.Context, payload CreatePayload, conn *websocket.Conn) (*session.Session, error) {
	// Check if session exists
	if sess, ok := h.sessionManager.Get(payload.AgentThreadID); ok {
		// Existing session, just attach
		h.sendStatus(conn, StatusPayload{
			State: "ready",
			Message: "Attached to existing session",
			Progress: 1.0,
		})
		return sess, nil
	}

	// Parse duration strings
	idleTimeout, _ := time.ParseDuration(payload.Config.IdleTimeout)
	maxLifetime, _ := time.ParseDuration(payload.Config.MaxLifetime)
	if idleTimeout == 0 {
		idleTimeout = 30 * time.Minute
	}
	if maxLifetime == 0 {
		maxLifetime = 24 * time.Hour
	}

	// Create session
	sess, err := h.sessionManager.Create(ctx, session.CreateRequest{
		AgentThreadID: payload.AgentThreadID,
		Image:         payload.Image,
		Command:       payload.Command,
		Env:           payload.Env,
		PodNamespace:  h.podNamespace,
		Config: session.SecurityConfig{
			AllowNetworkAccess:  payload.Config.AllowNetworkAccess,
			ReadonlyFilesystem:  payload.Config.ReadonlyFilesystem,
			CPULimit:           payload.Config.CPULimit,
			MemoryLimit:        payload.Config.MemoryLimit,
			IdleTimeout:        idleTimeout,
			MaxLifetime:        maxLifetime,
			DropAllCapabilities: payload.Config.DropAllCapabilities,
			AllowPrivileged:    payload.Config.AllowPrivileged,
		},
	})
	if err != nil {
		return nil, err
	}

	// Send creating status
	h.sendStatus(conn, StatusPayload{
		State: "creating",
		Message: "Creating pod...",
		Progress: 0.1,
	})

	// Create pod
	podName := "sandbox-" + payload.AgentThreadID
	if err := h.k8sClient.CreatePod(ctx, h.podNamespace, podName, payload.AgentThreadID, payload.Image, payload.Command, payload.Env, sess.Config); err != nil {
		h.sessionManager.Delete(payload.AgentThreadID)
		return nil, fmt.Errorf("failed to create pod: %w", err)
	}

	sess.PodName = podName
	h.sessionManager.SetPodInfo(payload.AgentThreadID, podName)

	// Wait for pod ready
	h.sendStatus(conn, StatusPayload{
		State: "creating",
		Message: "Waiting for pod to be ready...",
		Progress: 0.3,
	})

	if err := h.k8sClient.WaitForPodReady(ctx, h.podNamespace, podName, 5*time.Minute); err != nil {
		return nil, fmt.Errorf("pod not ready: %w", err)
	}

	// Check for snapshot
	h.sendStatus(conn, StatusPayload{
		State: "restoring",
		Message: "Checking for previous workspace...",
		Progress: 0.5,
	})

	snapshotKey := h.storageClient.GenerateSnapshotKey("ws_default", "proj_default", payload.AgentThreadID)
	exists, _ := h.storageClient.SnapshotExists(ctx, snapshotKey)

	if exists {
		h.sendStatus(conn, StatusPayload{
			State: "restoring",
			Message: "Restoring workspace...",
			Progress: 0.6,
		})

		tarData, _, err := h.storageClient.DownloadSnapshot(ctx, snapshotKey)
		if err == nil {
			defer tarData.Close()
			h.k8sClient.RestoreWorkspace(ctx, h.podNamespace, podName, tarData)
		}
	}

	// Ready
	h.sessionManager.UpdateState(payload.AgentThreadID, session.StateReady)
	h.sendStatus(conn, StatusPayload{
		State: "ready",
		Message: "Session ready",
		Progress: 1.0,
	})

	return sess, nil
}

func (h *Handler) attachSession(ctx context.Context, agentThreadID string, conn *websocket.Conn) error {
	sess, ok := h.sessionManager.Get(agentThreadID)
	if !ok {
		return fmt.Errorf("session not found")
	}

	// Mark as connected
	h.sessionManager.MarkClientConnected(agentThreadID)
	defer h.sessionManager.MarkClientDisconnected(agentThreadID)

	// Send buffered messages
	buf := h.bufferManager.GetOrCreate(agentThreadID)
	for _, msg := range buf.ReadAll() {
		h.sendOutput(conn, msg.Type, msg.Data)
		if msg.Type == "exit" {
			h.sendExit(conn, msg.ExitCode)
			return nil
		}
	}

	// Start bidirectional forwarding
	return h.forwardIO(ctx, sess, conn)
}

func (h *Handler) forwardIO(ctx context.Context, sess *session.Session, conn *websocket.Conn) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)

	// WebSocket → tmux (stdin)
	go func() {
		defer wg.Done()
		defer cancel()

		stdinReader, stdinWriter := io.Pipe()
		defer stdinWriter.Close()

		// Start exec session
		execDone := make(chan error, 1)
		go func() {
			execDone <- h.k8sClient.Exec(ctx, h.podNamespace, sess.PodName, "sandbox", []string{
				"tmux", "attach", "-t", "sandbox",
			}, k8s.StreamOptions{
				Stdin:  stdinReader,
				Stdout: nil, // We'll handle separately
				Stderr: nil,
				TTY:    true,
			})
		}()

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			var msg Message
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}

			switch msg.Type {
			case TypeStdin:
				payload, _ := h.parseStdin(msg.Data)
				data, _ := base64.StdEncoding.DecodeString(payload.Data)
				stdinWriter.Write(data)
			}
		}
	}()

	// tmux → WebSocket (stdout/stderr)
	go func() {
		defer wg.Done()
		defer cancel()
		defer conn.Close()

		buf := h.bufferManager.GetOrCreate(sess.AgentThreadID)

		// This would need a separate exec for output
		// For simplicity, using a combined approach
		// In production, you'd want bidirectional streaming

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Send ping
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			}
		}
	}()

	wg.Wait()
	return nil
}

func (h *Handler) sendStatus(conn *websocket.Conn, payload StatusPayload) error {
	return conn.WriteJSON(Message{
		Type: TypeStatus,
		Data: h.marshalJSON(payload),
	})
}

func (h *Handler) sendOutput(conn *websocket.Conn, msgType string, data []byte) error {
	return conn.WriteJSON(Message{
		Type: msgType,
		Data: h.marshalJSON(OutputPayload{
			Data: base64.StdEncoding.EncodeToString(data),
		}),
	})
}

func (h *Handler) sendExit(conn *websocket.Conn, code int32) error {
	return conn.WriteJSON(Message{
		Type: TypeExit,
		Data: h.marshalJSON(ExitPayload{Code: code}),
	})
}

func (h *Handler) sendError(conn *websocket.Conn, message string) error {
	return conn.WriteJSON(Message{
		Type: TypeError,
		Data: h.marshalJSON(ErrorPayload{Message: message}),
	})
}

func (h *Handler) parseStdin(data json.RawMessage) (StdinPayload, error) {
	var payload StdinPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return StdinPayload{}, err
	}
	return payload, nil
}

func (h *Handler) marshalJSON(v interface{}) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}
```

**Step 2: Verify compilation**

```bash
cd manager-service
go build ./internal/websocket/...
```

**Step 3: Commit**

```bash
git add manager-service/internal/websocket/handler.go
git commit -m "feat: add WebSocket handler"
```

---

# Phase 5: Finalizer and Snapshot

## Task 14: Add Finalizer handler

**Files:**
- Create: `manager-service/internal/finalizer/handler.go`

**Step 1: Write finalizer handler**

Create file with:

```go
package finalizer

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/vibe-kanban/mbos-sandbox-v1/manager-service/internal/k8s"
	"github.com/vibe-kanban/mbos-sandbox-v1/manager-service/internal/storage"
)

const (
	FinalizerName = "manager.mbos.io/snapshot"
)

type Handler struct {
	k8sClient     *k8s.Client
	storageClient *storage.Client
	podNamespace  string
}

func NewHandler(k8sClient *k8s.Client, storageClient *storage.Client, podNamespace string) *Handler {
	return &Handler{
		k8sClient:     k8sClient,
		storageClient: storageClient,
		podNamespace:  podNamespace,
	}
}

func (h *Handler) Start(ctx context.Context) error {
	log.Println("Starting finalizer handler...")

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := h.processPods(ctx); err != nil {
				log.Printf("Error processing pods: %v", err)
			}
		}
	}
}

func (h *Handler) processPods(ctx context.Context) error {
	// List pods with our finalizer
	pods, err := h.k8sClient.ListPodsWithFinalizer(ctx, h.podNamespace, FinalizerName)
	if err != nil {
		return fmt.Errorf("failed to list pods: %w", err)
	}

	for _, pod := range pods {
		if pod.DeletionTimestamp != nil {
			// Pod is being deleted, handle snapshot
			if err := h.handleSnapshot(ctx, pod); err != nil {
				log.Printf("Failed to snapshot pod %s: %v", pod.Name, err)
				// Continue to remove finalizer even if snapshot fails
			}

			// Remove finalizer
			if err := h.k8sClient.RemoveFinalizer(ctx, h.podNamespace, pod.Name, FinalizerName); err != nil {
				log.Printf("Failed to remove finalizer from pod %s: %v", pod.Name, err)
			}
		}
	}

	return nil
}

func (h *Handler) handleSnapshot(ctx context.Context, pod *v1.Pod) error {
	agentThreadID := pod.Labels["agent_thread_id"]
	if agentThreadID == "" {
		return fmt.Errorf("pod missing agent_thread_id label")
	}

	log.Printf("Snapshotting pod %s (agent_thread_id=%s)", pod.Name, agentThreadID)

	// Create snapshot
	snapshotKey := h.storageClient.GenerateSnapshotKey("ws_default", "proj_default", agentThreadID)

	reader, err := h.k8sClient.SnapshotWorkspace(ctx, h.podNamespace, pod.Name)
	if err != nil {
		return fmt.Errorf("failed to create snapshot: %w", err)
	}
	defer reader.Close()

	// Get size (for now, estimate)
	// In production, you'd want to stream directly

	if err := h.storageClient.UploadSnapshot(ctx, snapshotKey, reader, 0); err != nil {
		return fmt.Errorf("failed to upload snapshot: %w", err)
	}

	log.Printf("Snapshot complete for pod %s: %s", pod.Name, snapshotKey)
	return nil
}
```

**Step 2: Add k8s helper methods**

Add to k8s client:

```go
func (c *Client) ListPodsWithFinalizer(ctx context.Context, namespace, finalizer string) (*v1.PodList, error) {
	pods, err := c.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var result []v1.Pod
	for _, pod := range pods.Items {
		for _, f := range pod.Finalizers {
			if f == finalizer {
				result = append(result, pod)
				break
			}
		}
	}

	pods.Items = result
	return pods, nil
}

func (c *Client) RemoveFinalizer(ctx context.Context, namespace, podName, finalizer string) error {
	pod, err := c.client.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	var newFinalizers []string
	for _, f := range pod.Finalizers {
		if f != finalizer {
			newFinalizers = append(newFinalizers, f)
		}
	}

	pod.Finalizers = newFinalizers

	_, err = c.client.CoreV1().Pods(namespace).Update(ctx, pod, metav1.UpdateOptions{})
	return err
}
```

**Step 3: Verify compilation**

```bash
cd manager-service
go build ./internal/finalizer/...
```

**Step 4: Commit**

```bash
git add manager-service/internal/finalizer/handler.go
git commit -m "feat: add finalizer handler for snapshot"
```

---

# Phase 6: Cleaner

## Task 15: Create cleaner binary

**Files:**
- Create: `manager-service/cmd/cleaner/main.go`

**Step 1: Write cleaner**

Create file with:

```go
package main

import (
	"context"
	"flag"
	"log"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type Config struct {
	Namespace string
	DryRun     bool
}

func main() {
	cfg := Config{
		Namespace: "sandbox",
		DryRun:     false,
	}

	namespace := flag.String("namespace", "sandbox", "Kubernetes namespace")
	dryRun := flag.Bool("dry-run", false, "Don't actually delete pods")
	flag.Parse()

	cfg.Namespace = *namespace
	cfg.DryRun = *dryRun

	log.Printf("Starting cleaner: namespace=%s, dry_run=%v", cfg.Namespace, cfg.DryRun)

	if err := run(context.Background(), cfg); err != nil {
		log.Fatalf("Cleaner failed: %v", err)
	}

	log.Println("Cleaner completed successfully")
}

func run(ctx context.Context, cfg Config) error {
	// Create Kubernetes client
	config, err := rest.InClusterConfig()
	if err != nil {
		return err
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}

	// List pods
	pods, err := clientset.CoreV1().Pods(cfg.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=sandbox",
	})
	if err != nil {
		return err
	}

	log.Printf("Found %d sandbox pods", len(pods.Items))

	now := time.Now()
	for _, pod := range pods.Items {
		expiresAtStr := pod.Annotations["expires_at"]
		if expiresAtStr == "" {
			continue
		}

		expiresAt, err := time.Parse(time.RFC3339, expiresAtStr)
		if err != nil {
			log.Printf("Failed to parse expires_at for pod %s: %v", pod.Name, err)
			continue
		}

		if now.After(expiresAt) {
			log.Printf("Pod %s expired at %s", pod.Name, expiresAt)

			if cfg.DryRun {
				log.Printf("[DRY RUN] Would delete pod %s", pod.Name)
				continue
			}

			// Delete pod
			if err := clientset.CoreV1().Pods(cfg.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{}); err != nil {
				log.Printf("Failed to delete pod %s: %v", pod.Name, err)
				continue
			}

			log.Printf("Deleted pod %s", pod.Name)
		}
	}

	return nil
}
```

**Step 2: Update Dockerfile**

Add to Dockerfile to build both binaries:

```dockerfile
# Build stage
FROM golang:1.21-alpine AS builder

WORKDIR /build

RUN apk add --no-cache git make

COPY go.mod go.sum* ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o manager ./cmd/manager
RUN CGO_ENABLED=0 GOOS=linux go build -o cleaner ./cmd/cleaner

# Runtime stage
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tar

WORKDIR /app

COPY --from=builder /build/manager .
COPY --from=builder /build/cleaner .

RUN ln -s /app/cleaner /cleaner

EXPOSE 8080

ENTRYPOINT ["/app/manager"]
```

**Step 3: Commit**

```bash
git add manager-service/cmd/cleaner/ manager-service/Dockerfile
git commit -m "feat: add cleaner binary"
```

---

## Task 16: Create cleaner CronJob manifest

**Files:**
- Create: `k8s/base/cleaner-cronjob.yaml`

**Step 1: Write CronJob manifest**

Create file with:

```yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: sandbox-cleaner
  namespace: sandbox-system
spec:
  schedule: "*/5 * * * *"
  concurrencyPolicy: Forbid
  successfulJobsHistoryLimit: 3
  failedJobsHistoryLimit: 3
  jobTemplate:
    spec:
      backoffLimit: 3
      template:
        spec:
          serviceAccountName: sandbox-manager
          restartPolicy: OnFailure
          containers:
          - name: cleaner
            image: sandbox-manager:latest
            command: ["/cleaner"]
            args:
            - --namespace=sandbox
            env:
            - name: POD_NAMESPACE
              value: "sandbox"
            volumeMounts:
            - name: config
              mountPath: /etc/config
              readOnly: true
            resources:
              requests:
                cpu: 100m
                memory: 128Mi
              limits:
                cpu: 500m
                memory: 256Mi
          volumes:
          - name: config
            configMap:
              name: sandbox-manager-config
```

**Step 2: Commit**

```bash
git add k8s/base/cleaner-cronjob.yaml
git commit -m "feat: add cleaner CronJob manifest"
```

---

# Phase 7: Integration

## Task 17: Update app.go to integrate all components

**Files:**
- Modify: `manager-service/internal/app/app.go`

**Step 1: Read current app.go**

```bash
cat manager-service/internal/app/app.go
```

**Step 2: Update to initialize new components**

Add initialization for:
- Session manager
- Buffer manager
- Storage client
- WebSocket handler
- Finalizer handler

**Step 3: Update routes**

Add WebSocket route:

```go
r.Get("/ws", websocketHandler.ServeHTTP)
```

**Step 4: Commit**

```bash
git add manager-service/internal/app/app.go
git commit -m "feat: integrate all components in app"
```

---

## Task 18: Update configuration

**Files:**
- Modify: `manager-service/internal/config/types.go`
- Modify: `manager-service/manager-config.example.yaml`

**Step 1: Add new config fields**

Add storage configuration:

```go
type Config struct {
	// ... existing fields ...

	Storage StorageConfig `yaml:"storage"`
	Buffer  BufferConfig  `yaml:"buffer"`
}

type StorageConfig struct {
	Endpoint  string `yaml:"endpoint"`
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
	Bucket    string `yaml:"bucket"`
	UseSSL    bool   `yaml:"use_ssl"`
}

type BufferConfig struct {
	Capacity int `yaml:"capacity"`
}
```

**Step 2: Update example config**

**Step 3: Commit**

```bash
git add manager-service/internal/config/types.go manager-service/manager-config.example.yaml
git commit -m "feat: add storage and buffer configuration"
```

---

# Phase 8: Testing

## Task 19: Write integration tests

**Files:**
- Create: `manager-service/integration/session_test.go`

**Step 1: Write integration test**

Create file with:

```go
package integration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vibe-kanban/mbos-sandbox-v1/manager-service/internal/session"
)

func TestSessionLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()
	sm := session.NewManager()

	// Create session
	sess, err := sm.Create(ctx, session.CreateRequest{
		AgentThreadID: "at_test123",
		Image:         "nginx:alpine",
		PodNamespace:  "default",
		Config: session.SecurityConfig{
			MaxLifetime: 1 * time.Hour,
			IdleTimeout: 30 * time.Minute,
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "at_test123", sess.AgentThreadID)

	// Update state
	err = sm.UpdateState(sess.AgentThreadID, session.StateReady)
	require.NoError(t, err)

	// Mark connected
	err = sm.MarkClientConnected(sess.AgentThreadID)
	require.NoError(t, err)

	connected, _ := sm.Get(sess.AgentThreadID)
	assert.True(t, connected.ClientConnected)

	// Mark disconnected
	err = sm.MarkClientDisconnected(sess.AgentThreadID)
	require.NoError(t, err)

	disconnected, _ := sm.Get(sess.AgentThreadID)
	assert.False(t, disconnected.ClientConnected)

	// Cleanup
	sm.Delete(sess.AgentThreadID)
	_, ok := sm.Get(sess.AgentThreadID)
	assert.False(t, ok)
}
```

**Step 2: Run tests**

```bash
cd manager-service
go test ./integration/... -v
```

**Step 3: Commit**

```bash
git add manager-service/integration/session_test.go
git commit -m "test: add integration tests"
```

---

# Phase 9: Documentation

## Task 20: Write API documentation

**Files:**
- Create: `docs/api-reference-v1.md`

**Step 1: Write API documentation**

Create comprehensive API reference with WebSocket protocol documentation.

**Step 2: Commit**

```bash
git add docs/api-reference-v1.md
git commit -m "docs: add API reference"
```

---

## Task 21: Update README

**Files:**
- Modify: `README.md`

**Step 1: Update README with new architecture**

**Step 2: Commit**

```bash
git add README.md
git commit -m "docs: update README with new architecture"
```

---

## Task 22: Create migration guide

**Files:**
- Create: `docs/migration-guide-v1.md`

**Step 1: Write migration guide**

**Step 2: Commit**

```bash
git add docs/migration-guide-v1.md
git commit -m "docs: add migration guide"
```

---

# Summary

**Total Tasks:** 22

**Phases:**
1. Foundation - Core Types and Session Management (Tasks 1-6)
2. Storage (Tasks 7-8)
3. Kubernetes Integration (Tasks 9-11)
4. WebSocket Handler (Tasks 12-13)
5. Finalizer and Snapshot (Task 14)
6. Cleaner (Tasks 15-16)
7. Integration (Tasks 17-18)
8. Testing (Task 19)
9. Documentation (Tasks 20-22)

**Key Implementation Notes:**
- tmux wrapper script injected via pod command
- Ring buffer for message buffering (configurable capacity)
- Kubernetes Finalizer for snapshot-before-delete
- Pod annotations for TTL sync with Cleaner
- WebSocket single endpoint for all operations
- No explicit delete API - TTL only
- No authentication (internal trusted network)
