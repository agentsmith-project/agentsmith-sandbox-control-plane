package workspace_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sandbox/manager/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewStorage(t *testing.T) {
	dir := t.TempDir()
	s := workspace.NewStorage(dir)
	require.NotNil(t, s)
}

func TestRepoPath(t *testing.T) {
	s := workspace.NewStorage("/mnt/juicefs/workloads")
	path := s.RepoPath("ws-123", "wl-456")
	assert.Equal(t, "/mnt/juicefs/workloads/ws-123/wl-456", path)
}

func TestPayloadSubPath(t *testing.T) {
	s := workspace.NewStorage("/mnt/juicefs/workloads")
	subPath := s.PayloadSubPath("ws-123", "wl-456")
	assert.Equal(t, "ws-123/wl-456/main", subPath)
}

func TestEnsureRepo_CreatesNewRepo(t *testing.T) {
	dir := t.TempDir()
	s := workspace.NewStorage(dir)
	ctx := context.Background()

	client, err := s.EnsureRepo(ctx, "ws1", "wl1")
	require.NoError(t, err)
	require.NotNil(t, client)

	assert.DirExists(t, filepath.Join(dir, "ws1", "wl1", ".jvs"))
}

func TestEnsureRepo_CachesClient(t *testing.T) {
	dir := t.TempDir()
	s := workspace.NewStorage(dir)
	ctx := context.Background()

	c1, err := s.EnsureRepo(ctx, "ws1", "wl1")
	require.NoError(t, err)
	c2, err := s.EnsureRepo(ctx, "ws1", "wl1")
	require.NoError(t, err)

	assert.Same(t, c1, c2)
}

func TestHasState_FalseOnFresh(t *testing.T) {
	dir := t.TempDir()
	s := workspace.NewStorage(dir)
	ctx := context.Background()

	has, err := s.HasState(ctx, "ws1", "wl1")
	require.NoError(t, err)
	assert.False(t, has)
}

func TestPrepareWorkspace_FreshWorkload(t *testing.T) {
	dir := t.TempDir()
	s := workspace.NewStorage(dir)
	ctx := context.Background()

	payloadPath, err := s.PrepareWorkspace(ctx, "ws1", "wl1")
	require.NoError(t, err)
	assert.DirExists(t, payloadPath)
	assert.Equal(t, filepath.Join(dir, "ws1", "wl1", "main"), payloadPath)
}

func TestSaveAndPrepareWorkspace(t *testing.T) {
	dir := t.TempDir()
	s := workspace.NewStorage(dir)
	ctx := context.Background()

	payloadPath, err := s.PrepareWorkspace(ctx, "ws1", "wl1")
	require.NoError(t, err)

	// Simulate workload writing files
	testFile := filepath.Join(payloadPath, "output.txt")
	require.NoError(t, os.WriteFile(testFile, []byte("workload output"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(payloadPath, "subdir"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(payloadPath, "subdir", "nested.txt"), []byte("nested"), 0644))

	// Save workspace (snapshot after pod shutdown)
	desc, err := s.SaveWorkspace(ctx, "ws1", "wl1", "auto: shutdown", []string{"auto"})
	require.NoError(t, err)
	require.NotNil(t, desc)
	assert.NotEmpty(t, desc.SnapshotID)

	has, err := s.HasState(ctx, "ws1", "wl1")
	require.NoError(t, err)
	assert.True(t, has)

	// Simulate file destruction (pod deleted, workspace corrupted)
	require.NoError(t, os.RemoveAll(payloadPath))

	// Prepare workspace again (restore for new pod)
	payloadPath2, err := s.PrepareWorkspace(ctx, "ws1", "wl1")
	require.NoError(t, err)
	assert.Equal(t, payloadPath, payloadPath2)

	// Verify files are restored
	data, err := os.ReadFile(filepath.Join(payloadPath2, "output.txt"))
	require.NoError(t, err)
	assert.Equal(t, "workload output", string(data))

	data, err = os.ReadFile(filepath.Join(payloadPath2, "subdir", "nested.txt"))
	require.NoError(t, err)
	assert.Equal(t, "nested", string(data))
}

func TestMultipleWorkloads_Independent(t *testing.T) {
	dir := t.TempDir()
	s := workspace.NewStorage(dir)
	ctx := context.Background()

	// Setup two different workloads
	path1, err := s.PrepareWorkspace(ctx, "ws1", "wl-a")
	require.NoError(t, err)
	path2, err := s.PrepareWorkspace(ctx, "ws1", "wl-b")
	require.NoError(t, err)
	assert.NotEqual(t, path1, path2)

	// Write different files to each
	require.NoError(t, os.WriteFile(filepath.Join(path1, "a.txt"), []byte("workload-a"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(path2, "b.txt"), []byte("workload-b"), 0644))

	// Snapshot both
	_, err = s.SaveWorkspace(ctx, "ws1", "wl-a", "a-snapshot", nil)
	require.NoError(t, err)
	_, err = s.SaveWorkspace(ctx, "ws1", "wl-b", "b-snapshot", nil)
	require.NoError(t, err)

	// Destroy both
	require.NoError(t, os.RemoveAll(path1))
	require.NoError(t, os.RemoveAll(path2))

	// Restore and verify isolation
	p1, err := s.PrepareWorkspace(ctx, "ws1", "wl-a")
	require.NoError(t, err)
	p2, err := s.PrepareWorkspace(ctx, "ws1", "wl-b")
	require.NoError(t, err)

	data1, err := os.ReadFile(filepath.Join(p1, "a.txt"))
	require.NoError(t, err)
	assert.Equal(t, "workload-a", string(data1))

	data2, err := os.ReadFile(filepath.Join(p2, "b.txt"))
	require.NoError(t, err)
	assert.Equal(t, "workload-b", string(data2))

	// Verify no cross-contamination
	_, err = os.Stat(filepath.Join(p1, "b.txt"))
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(p2, "a.txt"))
	assert.True(t, os.IsNotExist(err))
}

func TestCleanup(t *testing.T) {
	dir := t.TempDir()
	s := workspace.NewStorage(dir)
	ctx := context.Background()

	path, err := s.PrepareWorkspace(ctx, "ws1", "wl1")
	require.NoError(t, err)

	for i := 0; i < 3; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(path, "file.txt"), []byte{byte('0' + i)}, 0644))
		_, err := s.SaveWorkspace(ctx, "ws1", "wl1", "snapshot", nil)
		require.NoError(t, err)
	}

	err = s.Cleanup(ctx, "ws1", "wl1", 2)
	assert.NoError(t, err)
}

func TestFullLifecycle_MultipleIterations(t *testing.T) {
	dir := t.TempDir()
	s := workspace.NewStorage(dir)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		path, err := s.PrepareWorkspace(ctx, "ws1", "wl1")
		require.NoError(t, err)

		content := []byte("iteration-" + string(rune('0'+i)))
		require.NoError(t, os.WriteFile(filepath.Join(path, "state.txt"), content, 0644))

		_, err = s.SaveWorkspace(ctx, "ws1", "wl1", "iteration", nil)
		require.NoError(t, err)
	}

	path, err := s.PrepareWorkspace(ctx, "ws1", "wl1")
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(path, "state.txt"))
	require.NoError(t, err)
	assert.Equal(t, "iteration-2", string(data))
}
