package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeTempYAML creates a temporary YAML file in dir and returns its path.
func writeTempYAML(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	return path
}

// ---------- Load ----------

func TestLoad_ValidFile(t *testing.T) {
	dir := t.TempDir()
	yaml := `
version: 2
server:
  httpPort: 9090
  requestIdHeader: "X-Trace-Id"
sandbox:
  defaults:
    namespace: "test-ns"
    runnerImage: "runner:2.0.0"
exec:
  defaultTimeout: 60s
`
	path := writeTempYAML(t, dir, "valid.yaml", yaml)

	cfg, meta, err := Load(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.NotNil(t, meta)

	// Verify parsed fields
	assert.Equal(t, 2, cfg.Version)
	assert.Equal(t, 9090, cfg.Server.HTTPPort)
	assert.Equal(t, "X-Trace-Id", cfg.Server.RequestIDHeader)
	assert.Equal(t, "test-ns", cfg.Sandbox.Defaults.Namespace)
	assert.Equal(t, "runner:2.0.0", cfg.Sandbox.Defaults.RunnerImage)
	assert.Equal(t, 60*time.Second, cfg.Exec.DefaultTimeout)

	// Verify meta
	assert.Equal(t, 2, meta.SchemaVersion)
	assert.Equal(t, path, meta.SourcePath)
	assert.NotEmpty(t, meta.CurrentHash)
	assert.Equal(t, 0, meta.ReloadCount)
	assert.Nil(t, meta.LastError)

	// Verify hash matches manual computation
	raw, _ := os.ReadFile(path)
	expectedHash := ComputeHash(raw)
	assert.Equal(t, expectedHash, meta.CurrentHash)
}

func TestLoad_MissingFile(t *testing.T) {
	_, _, err := Load("/tmp/does_not_exist_config_test.yaml")
	require.Error(t, err)

	var cfgErr *ConfigError
	require.True(t, errors.As(err, &cfgErr), "error should be *ConfigError")
	assert.Equal(t, "CONFIG_READ_FAILED", cfgErr.Code)
	assert.NotEmpty(t, cfgErr.Message)
	assert.NotEmpty(t, cfgErr.Timestamp)
}

func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	// Malformed YAML: tab indentation mixed with bad mapping
	content := "version: 1\nserver:\n\t- bad: [unterminated"
	path := writeTempYAML(t, dir, "bad.yaml", content)

	_, _, err := Load(path)
	require.Error(t, err)

	var cfgErr *ConfigError
	require.True(t, errors.As(err, &cfgErr), "error should be *ConfigError")
	assert.Equal(t, "CONFIG_PARSE_FAILED", cfgErr.Code)
	assert.NotEmpty(t, cfgErr.Message)
	assert.NotEmpty(t, cfgErr.Timestamp)
}

// ---------- LoadWithDefaults ----------

func TestLoadWithDefaults_AppliesDefaults(t *testing.T) {
	dir := t.TempDir()
	// Minimal config — only version is set; everything else should get defaults.
	path := writeTempYAML(t, dir, "minimal.yaml", "version: 1\n")

	cfg, meta, err := LoadWithDefaults(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.NotNil(t, meta)

	defaults := DefaultConfig()

	// Server defaults
	assert.Equal(t, defaults.Server.HTTPPort, cfg.Server.HTTPPort,
		"HTTPPort should be defaulted to %d", defaults.Server.HTTPPort)
	assert.Equal(t, defaults.Server.RequestIDHeader, cfg.Server.RequestIDHeader)
	assert.Equal(t, defaults.Server.Timeouts.ReadHeader, cfg.Server.Timeouts.ReadHeader)
	assert.Equal(t, defaults.Server.Timeouts.Read, cfg.Server.Timeouts.Read)
	assert.Equal(t, defaults.Server.Timeouts.Write, cfg.Server.Timeouts.Write)
	assert.Equal(t, defaults.Server.Timeouts.Idle, cfg.Server.Timeouts.Idle)
	assert.Equal(t, defaults.Server.MaxHeaderBytes, cfg.Server.MaxHeaderBytes)
	assert.Equal(t, defaults.Server.Metrics.Path, cfg.Server.Metrics.Path)
	assert.Equal(t, defaults.Server.Debug.ConfigPath, cfg.Server.Debug.ConfigPath)

	// Kubernetes defaults
	assert.Equal(t, defaults.Kubernetes.QPS, cfg.Kubernetes.QPS)
	assert.Equal(t, defaults.Kubernetes.Burst, cfg.Kubernetes.Burst)
	assert.Equal(t, defaults.Kubernetes.RequestTimeout, cfg.Kubernetes.RequestTimeout)

	// Sandbox defaults
	assert.Equal(t, "sandbox", cfg.Sandbox.Defaults.Namespace)
	assert.Equal(t, "sandbox-runner:1.0.0", cfg.Sandbox.Defaults.RunnerImage)
	assert.Equal(t, defaults.Sandbox.Defaults.ImagePullPolicy, cfg.Sandbox.Defaults.ImagePullPolicy)
	assert.Equal(t, defaults.Sandbox.Defaults.TTLSeconds, cfg.Sandbox.Defaults.TTLSeconds)
	assert.Equal(t, defaults.Sandbox.Defaults.PodReadyWait, cfg.Sandbox.Defaults.PodReadyWait)
	assert.Equal(t, defaults.Sandbox.Defaults.PodPollInterval, cfg.Sandbox.Defaults.PodPollInterval)
	assert.Equal(t, defaults.Sandbox.Defaults.ContainerName, cfg.Sandbox.Defaults.ContainerName)
	assert.Equal(t, defaults.Sandbox.Defaults.Workdir, cfg.Sandbox.Defaults.Workdir)

	// Exec defaults
	assert.Equal(t, 30*time.Second, cfg.Exec.DefaultTimeout)
	assert.Equal(t, defaults.Exec.MaxTimeout, cfg.Exec.MaxTimeout)
	assert.Equal(t, defaults.Exec.Shell.Bin, cfg.Exec.Shell.Bin)
	assert.Equal(t, defaults.Exec.Shell.Args, cfg.Exec.Shell.Args)
	assert.Equal(t, defaults.Exec.ExitCodeMarker.Key, cfg.Exec.ExitCodeMarker.Key)

	// Files defaults
	assert.Equal(t, defaults.Files.RootPrefix, cfg.Files.RootPrefix)
	assert.Equal(t, defaults.Files.Upload.DefaultDest, cfg.Files.Upload.DefaultDest)
	assert.Equal(t, defaults.Files.Upload.MaxBytes, cfg.Files.Upload.MaxBytes)
	assert.Equal(t, defaults.Files.Tar.Bin, cfg.Files.Tar.Bin)
}

// ---------- ComputeHash ----------

func TestComputeHash_Deterministic(t *testing.T) {
	data := []byte("hello sandbox config")
	h1 := ComputeHash(data)
	h2 := ComputeHash(data)
	assert.Equal(t, h1, h2, "same input must produce same hash")

	different := ComputeHash([]byte("different content"))
	assert.NotEqual(t, h1, different, "different input must produce different hash")
}

func TestComputeHash_SHA256(t *testing.T) {
	data := []byte("test")
	got := ComputeHash(data)

	// Manually compute expected SHA256
	sum := sha256.Sum256(data)
	expected := hex.EncodeToString(sum[:])

	assert.Equal(t, expected, got)
	// SHA256 hex string is always 64 characters
	assert.Len(t, got, 64)
}

// ---------- Clone ----------

func TestClone_DeepCopy(t *testing.T) {
	original := DefaultConfig()

	cloned, err := original.Clone()
	require.NoError(t, err)
	require.NotNil(t, cloned)

	// Modify the clone's scalar and nested fields
	cloned.Version = 99
	cloned.Server.HTTPPort = 1234
	cloned.Server.RequestIDHeader = "X-Changed"
	cloned.Sandbox.Defaults.Namespace = "modified-ns"
	cloned.Sandbox.Defaults.RunnerImage = "modified:latest"
	cloned.Exec.DefaultTimeout = 999 * time.Second
	cloned.Files.RootPrefix = "/changed"

	// Modify clone's map to verify deep copy of maps
	cloned.Sandbox.Defaults.Labels["newKey"] = "newVal"

	// Verify original is NOT affected
	assert.Equal(t, 1, original.Version)
	assert.Equal(t, 8080, original.Server.HTTPPort)
	assert.Equal(t, "X-Request-Id", original.Server.RequestIDHeader)
	assert.Equal(t, "sandbox", original.Sandbox.Defaults.Namespace)
	assert.Equal(t, "sandbox-runner:1.0.0", original.Sandbox.Defaults.RunnerImage)
	assert.Equal(t, 30*time.Second, original.Exec.DefaultTimeout)
	assert.Equal(t, "/workspace", original.Files.RootPrefix)
	assert.NotContains(t, original.Sandbox.Defaults.Labels, "newKey",
		"modifying clone's map should not affect original")
}

func TestClone_PreservesValues(t *testing.T) {
	original := DefaultConfig()

	cloned, err := original.Clone()
	require.NoError(t, err)
	require.NotNil(t, cloned)

	// All scalar and nested values should match
	assert.Equal(t, original.Version, cloned.Version)

	// Server
	assert.Equal(t, original.Server.HTTPPort, cloned.Server.HTTPPort)
	assert.Equal(t, original.Server.RequestIDHeader, cloned.Server.RequestIDHeader)
	assert.Equal(t, original.Server.Timeouts, cloned.Server.Timeouts)
	assert.Equal(t, original.Server.MaxHeaderBytes, cloned.Server.MaxHeaderBytes)
	assert.Equal(t, original.Server.Metrics, cloned.Server.Metrics)
	assert.Equal(t, original.Server.Debug, cloned.Server.Debug)

	// Kubernetes
	assert.Equal(t, original.Kubernetes.QPS, cloned.Kubernetes.QPS)
	assert.Equal(t, original.Kubernetes.Burst, cloned.Kubernetes.Burst)
	assert.Equal(t, original.Kubernetes.RequestTimeout, cloned.Kubernetes.RequestTimeout)
	assert.Equal(t, original.Kubernetes.Retry, cloned.Kubernetes.Retry)

	// Sandbox
	assert.Equal(t, original.Sandbox.Defaults.Namespace, cloned.Sandbox.Defaults.Namespace)
	assert.Equal(t, original.Sandbox.Defaults.RunnerImage, cloned.Sandbox.Defaults.RunnerImage)
	assert.Equal(t, original.Sandbox.Defaults.ImagePullPolicy, cloned.Sandbox.Defaults.ImagePullPolicy)
	assert.Equal(t, original.Sandbox.Defaults.TTLSeconds, cloned.Sandbox.Defaults.TTLSeconds)
	assert.Equal(t, original.Sandbox.Defaults.PodReadyWait, cloned.Sandbox.Defaults.PodReadyWait)
	assert.Equal(t, original.Sandbox.Defaults.ContainerName, cloned.Sandbox.Defaults.ContainerName)
	assert.Equal(t, original.Sandbox.Defaults.Workdir, cloned.Sandbox.Defaults.Workdir)
	assert.Equal(t, original.Sandbox.Defaults.Resources, cloned.Sandbox.Defaults.Resources)
	assert.Equal(t, original.Sandbox.Defaults.Labels, cloned.Sandbox.Defaults.Labels)
	assert.Equal(t, original.Sandbox.Defaults.Volumes, cloned.Sandbox.Defaults.Volumes)

	// Exec
	assert.Equal(t, original.Exec.DefaultTimeout, cloned.Exec.DefaultTimeout)
	assert.Equal(t, original.Exec.MaxTimeout, cloned.Exec.MaxTimeout)
	assert.Equal(t, original.Exec.StdoutMaxBytes, cloned.Exec.StdoutMaxBytes)
	assert.Equal(t, original.Exec.StderrMaxBytes, cloned.Exec.StderrMaxBytes)
	assert.Equal(t, original.Exec.Shell, cloned.Exec.Shell)
	assert.Equal(t, original.Exec.ExitCodeMarker, cloned.Exec.ExitCodeMarker)
	assert.Equal(t, original.Exec.Env, cloned.Exec.Env)
	assert.Equal(t, original.Exec.Workdir, cloned.Exec.Workdir)

	// Files
	assert.Equal(t, original.Files.RootPrefix, cloned.Files.RootPrefix)
	assert.Equal(t, original.Files.Upload, cloned.Files.Upload)
	assert.Equal(t, original.Files.Download, cloned.Files.Download)
	assert.Equal(t, original.Files.Tar, cloned.Files.Tar)

	// Storage
	assert.Equal(t, original.Storage, cloned.Storage)
}
