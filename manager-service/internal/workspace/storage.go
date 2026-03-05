package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/jvs-project/jvs/pkg/jvs"
	"github.com/jvs-project/jvs/pkg/model"
)

// Storage manages persistent workspace directories backed by JVS on JuiceFS.
// Each workload gets an isolated JVS repository under basePath/{workspaceID}/{workloadID}/.
// The sandbox-manager mounts JuiceFS directly and performs all JVS operations
// (snapshot/restore) outside of workload pods, simplifying the pod specification.
type Storage struct {
	basePath string
	mu       sync.Mutex
	clients  map[string]*jvs.Client
}

// NewStorage creates a workspace storage backed by JVS on the given base path.
// basePath is typically a JuiceFS mount point (e.g., "/mnt/juicefs/workloads").
func NewStorage(basePath string) *Storage {
	return &Storage{
		basePath: basePath,
		clients:  make(map[string]*jvs.Client),
	}
}

// RepoPath returns the JVS repository root for a workload.
func (s *Storage) RepoPath(workspaceID, workloadID string) string {
	return filepath.Join(s.basePath, workspaceID, workloadID)
}

// EnsureRepo opens or initializes a JVS repository for the given workload.
// Safe to call multiple times; subsequent calls return the cached client.
func (s *Storage) EnsureRepo(ctx context.Context, workspaceID, workloadID string) (*jvs.Client, error) {
	key := workspaceID + "/" + workloadID

	s.mu.Lock()
	defer s.mu.Unlock()

	if c, ok := s.clients[key]; ok {
		return c, nil
	}

	repoPath := s.RepoPath(workspaceID, workloadID)
	if err := os.MkdirAll(repoPath, 0755); err != nil {
		return nil, fmt.Errorf("create repo directory: %w", err)
	}

	client, err := jvs.OpenOrInit(repoPath, jvs.InitOptions{
		Name: fmt.Sprintf("ws-%s-%s", workspaceID, workloadID),
	})
	if err != nil {
		return nil, fmt.Errorf("open or init JVS repo at %s: %w", repoPath, err)
	}

	s.clients[key] = client
	return client, nil
}

// PrepareWorkspace restores the latest snapshot (if any) and returns the
// payload path that should be mounted into the workload pod as /workspace.
// If no snapshots exist, the workspace is empty but ready for use.
func (s *Storage) PrepareWorkspace(ctx context.Context, workspaceID, workloadID string) (string, error) {
	client, err := s.EnsureRepo(ctx, workspaceID, workloadID)
	if err != nil {
		return "", err
	}

	payloadPath := client.WorktreePayloadPath("main")

	has, err := client.HasSnapshots(ctx, "main")
	if err != nil {
		return "", fmt.Errorf("check snapshots: %w", err)
	}

	if has {
		// Ensure payload directory exists before restore (it uses atomic rename
		// of the existing dir to a backup). If the pod was cleaned up or the
		// directory was removed, recreate it so restore's rename succeeds.
		if err := os.MkdirAll(payloadPath, 0755); err != nil {
			return "", fmt.Errorf("ensure payload dir: %w", err)
		}
		if err := client.RestoreLatest(ctx, "main"); err != nil {
			return "", fmt.Errorf("restore latest: %w", err)
		}
	} else {
		// New workspace with no snapshots: ensure payload dir exists for pod mount.
		if err := os.MkdirAll(payloadPath, 0755); err != nil {
			return "", fmt.Errorf("ensure payload dir: %w", err)
		}
	}

	return payloadPath, nil
}

// SaveWorkspace creates a JVS snapshot of the workload's current workspace.
// Should be called after the workload pod has been terminated to ensure consistency.
func (s *Storage) SaveWorkspace(ctx context.Context, workspaceID, workloadID, note string, tags []string) (*model.Descriptor, error) {
	client, err := s.EnsureRepo(ctx, workspaceID, workloadID)
	if err != nil {
		return nil, err
	}

	desc, err := client.Snapshot(ctx, jvs.SnapshotOptions{
		WorktreeName: "main",
		Note:         note,
		Tags:         tags,
	})
	if err != nil {
		return nil, fmt.Errorf("create snapshot: %w", err)
	}

	return desc, nil
}

// HasState checks whether there are any snapshots for this workload's workspace.
func (s *Storage) HasState(ctx context.Context, workspaceID, workloadID string) (bool, error) {
	client, err := s.EnsureRepo(ctx, workspaceID, workloadID)
	if err != nil {
		return false, err
	}
	return client.HasSnapshots(ctx, "main")
}

// Cleanup runs garbage collection on the workload's JVS repository.
// keepMin specifies the minimum number of snapshots to retain.
func (s *Storage) Cleanup(ctx context.Context, workspaceID, workloadID string, keepMin int) error {
	client, err := s.EnsureRepo(ctx, workspaceID, workloadID)
	if err != nil {
		return err
	}
	_, err = client.GC(ctx, jvs.GCOptions{
		KeepMinSnapshots: keepMin,
	})
	return err
}

// PayloadSubPath returns the relative subPath within the JuiceFS volume for
// mounting into a workload pod. This is used for the PVC subPath configuration.
// Example: "ws_123/wl_456/main"
func (s *Storage) PayloadSubPath(workspaceID, workloadID string) string {
	return filepath.Join(workspaceID, workloadID, "main")
}
