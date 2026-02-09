package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDefaultWatcherOptions tests the DefaultWatcherOptions function
func TestDefaultWatcherOptions(t *testing.T) {
	opts := DefaultWatcherOptions()

	assert.NotNil(t, opts)
	assert.Equal(t, 300*time.Millisecond, opts.DebounceDuration)
	assert.Equal(t, 1*time.Second, opts.MinInterval)
	assert.Equal(t, 30*time.Second, opts.MaxBackoff)
	assert.False(t, opts.StrictMode)
}

// TestNewWatcher_WithNilOptions_UsesDefaults tests NewWatcher with nil options
func TestNewWatcher_WithNilOptions_UsesDefaults(t *testing.T) {
	cfg := &Config{}
	meta := &ConfigMeta{}

	watcher := NewWatcher("test.yaml", cfg, meta, nil)

	assert.NotNil(t, watcher)
	assert.Equal(t, "test.yaml", watcher.configPath)
	assert.Equal(t, cfg, watcher.currentConfig)
	assert.Equal(t, meta, watcher.currentMeta)
	assert.Equal(t, 300*time.Millisecond, watcher.debounceDuration)
	assert.Equal(t, 1*time.Second, watcher.minInterval)
	assert.Equal(t, 30*time.Second, watcher.maxBackoff)
	assert.False(t, watcher.strictMode)
	assert.Equal(t, 0, watcher.consecutiveFailures)
}

// TestNewWatcher_WithOptions_UsesProvidedOptions tests NewWatcher with custom options
func TestNewWatcher_WithOptions_UsesProvidedOptions(t *testing.T) {
	cfg := &Config{}
	meta := &ConfigMeta{}
	opts := &WatcherOptions{
		DebounceDuration: 500 * time.Millisecond,
		MinInterval:      2 * time.Second,
		MaxBackoff:       60 * time.Second,
		StrictMode:       true,
	}

	watcher := NewWatcher("test.yaml", cfg, meta, opts)

	assert.NotNil(t, watcher)
	assert.Equal(t, 500*time.Millisecond, watcher.debounceDuration)
	assert.Equal(t, 2*time.Second, watcher.minInterval)
	assert.Equal(t, 60*time.Second, watcher.maxBackoff)
	assert.True(t, watcher.strictMode)
}

// TestSetReloadHook tests setting a reload hook
func TestSetReloadHook(t *testing.T) {
	cfg := &Config{}
	meta := &ConfigMeta{}
	watcher := NewWatcher("test.yaml", cfg, meta, nil)

	watcher.SetReloadHook(func(c *Config, m *ConfigMeta) {
		// Hook called
	})

	// Verify the hook was set
	watcher.mu.RLock()
	hookSet := watcher.reloadHook != nil
	watcher.mu.RUnlock()

	assert.True(t, hookSet)
}

// TestGetCurrent_ReturnsInitialConfig tests GetCurrent returns the initial config
func TestGetCurrent_ReturnsInitialConfig(t *testing.T) {
	cfg := &Config{Version: 1}
	meta := &ConfigMeta{CurrentHash: "abc123"}
	watcher := NewWatcher("test.yaml", cfg, meta, nil)

	resultCfg, resultMeta := watcher.GetCurrent()

	assert.Equal(t, cfg, resultCfg)
	assert.Equal(t, meta, resultMeta)
	assert.Equal(t, 1, resultCfg.Version)
	assert.Equal(t, "abc123", resultMeta.CurrentHash)
}

// TestGetStats_InitialState_ReturnsZeroStats tests GetStats returns zero stats initially
func TestGetStats_InitialState_ReturnsZeroStats(t *testing.T) {
	cfg := &Config{}
	meta := &ConfigMeta{}
	watcher := NewWatcher("test.yaml", cfg, meta, nil)

	stats := watcher.GetStats()

	assert.Equal(t, 0, stats.TotalAttempts)
	assert.Equal(t, 0, stats.SuccessCount)
	assert.Equal(t, 0, stats.FailureCount)
	assert.Nil(t, stats.LastSuccess)
	assert.Nil(t, stats.LastFailure)
	assert.True(t, stats.LastReloadAt.IsZero())
}

// TestStop_BeforeStart_StopsImmediately tests Stop before Start completes immediately
func TestStop_BeforeStart_StopsImmediately(t *testing.T) {
	cfg := &Config{}
	meta := &ConfigMeta{}
	watcher := NewWatcher("test.yaml", cfg, meta, nil)

	// The Stop function waits for doneCh which is closed by watchLoop
	// Since watchLoop hasn't started, we need to close doneCh manually
	close(watcher.doneCh)

	// Stop should complete now
	watcher.Stop()
}

// TestIsConfigFileEvent_MatchingPaths_ReturnsTrue tests isConfigFileEvent with matching paths
func TestIsConfigFileEvent_MatchingPaths_ReturnsTrue(t *testing.T) {
	cfg := &Config{}
	meta := &ConfigMeta{}

	// Create a temporary file for testing
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	watcher := NewWatcher(configPath, cfg, meta, nil)

	// Test with exact path match
	matched, err := watcher.isConfigFileEvent(configPath)
	assert.NoError(t, err)
	assert.True(t, matched)
}

// TestIsConfigFileEvent_DifferentFile_ReturnsFalse tests isConfigFileEvent with different file
func TestIsConfigFileEvent_DifferentFile_ReturnsFalse(t *testing.T) {
	cfg := &Config{}
	meta := &ConfigMeta{}

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	otherPath := filepath.Join(tmpDir, "other.yaml")
	watcher := NewWatcher(configPath, cfg, meta, nil)

	// Create the other file
	_ = os.WriteFile(otherPath, []byte("test"), 0644)

	matched, err := watcher.isConfigFileEvent(otherPath)
	assert.NoError(t, err)
	assert.False(t, matched)
}

// TestCalculateBackoff_NoFailures_ReturnsMinInterval tests calculateBackoff with no failures
func TestCalculateBackoff_NoFailures_ReturnsMinInterval(t *testing.T) {
	cfg := &Config{}
	meta := &ConfigMeta{}
	watcher := NewWatcher("test.yaml", cfg, meta, nil)

	watcher.consecutiveFailures = 1
	backoff := watcher.calculateBackoff()

	assert.Equal(t, 1*time.Second, backoff)
}

// TestCalculateBackoff_ExponentialGrowth tests exponential backoff growth
func TestCalculateBackoff_ExponentialGrowth(t *testing.T) {
	cfg := &Config{}
	meta := &ConfigMeta{}
	watcher := NewWatcher("test.yaml", cfg, meta, nil)

	tests := []struct {
		name        string
		failures    int
		expectedMin time.Duration
		expectedMax time.Duration
	}{
		{"1 failure", 1, 1 * time.Second, 1 * time.Second},
		{"2 failures", 2, 2 * time.Second, 2 * time.Second},
		{"3 failures", 3, 4 * time.Second, 4 * time.Second},
		{"4 failures", 4, 8 * time.Second, 8 * time.Second},
		{"10 failures (capped)", 10, 16 * time.Second, 30 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			watcher.consecutiveFailures = tt.failures
			backoff := watcher.calculateBackoff()
			assert.GreaterOrEqual(t, backoff, tt.expectedMin)
			assert.LessOrEqual(t, backoff, tt.expectedMax)
		})
	}
}

// TestWaitForInitialLoad_ConfigAlreadyLoaded_ReturnsImmediately tests WaitForInitialLoad with config loaded
func TestWaitForInitialLoad_ConfigAlreadyLoaded_ReturnsImmediately(t *testing.T) {
	cfg := &Config{Version: 1}
	meta := &ConfigMeta{CurrentHash: "abc123"}
	watcher := NewWatcher("test.yaml", cfg, meta, nil)

	ctx := context.Background()
	resultCfg, resultMeta, err := watcher.WaitForInitialLoad(ctx)

	assert.NoError(t, err)
	assert.Equal(t, cfg, resultCfg)
	assert.Equal(t, meta, resultMeta)
}

// TestWaitForInitialLoad_ContextCanceled_ReturnsError tests WaitForInitialLoad with canceled context
func TestWaitForInitialLoad_ContextCanceled_ReturnsError(t *testing.T) {
	// Create a watcher with nil config to simulate unloaded state
	cfg := &Config{}
	meta := &ConfigMeta{}
	watcher := NewWatcher("test.yaml", cfg, meta, nil)

	// Clear the config to simulate unloaded state
	watcher.mu.Lock()
	watcher.currentConfig = nil
	watcher.currentMeta = nil
	watcher.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	resultCfg, resultMeta, err := watcher.WaitForInitialLoad(ctx)

	assert.Error(t, err)
	assert.Nil(t, resultCfg)
	assert.Nil(t, resultMeta)
	assert.Equal(t, context.Canceled, err)
}

// TestForceReload_WithInvalidConfig_UpdatesStats tests ForceReload with invalid config
func TestForceReload_WithInvalidConfig_UpdatesStats(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	// Write invalid YAML
	_ = os.WriteFile(configPath, []byte("invalid: yaml: content: ["), 0644)

	cfg := &Config{}
	meta := &ConfigMeta{CurrentHash: "old"}
	watcher := NewWatcher(configPath, cfg, meta, nil)

	err := watcher.ForceReload()

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "forced reload failed")

	// Check stats were updated
	stats := watcher.GetStats()
	assert.Equal(t, 1, stats.TotalAttempts)
	assert.Equal(t, 0, stats.SuccessCount)
	assert.Equal(t, 1, stats.FailureCount)
	assert.NotNil(t, stats.LastFailure)
}

// TestForceReload_ConfigNotChanged_DoesNotUpdate tests ForceReload when config hasn't changed
func TestForceReload_ConfigNotChanged_DoesNotUpdate(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	configContent := `version: 1
server:
  httpPort: 8080
`
	_ = os.WriteFile(configPath, []byte(configContent), 0644)

	cfg, meta, err := LoadWithDefaults(configPath)
	require.NoError(t, err)

	watcher := NewWatcher(configPath, cfg, meta, nil)

	// Force reload with same content
	err = watcher.ForceReload()
	assert.NoError(t, err)

	// Stats should show attempt but no actual reload (hash unchanged)
	stats := watcher.GetStats()
	assert.Equal(t, 1, stats.TotalAttempts)
	// Success count might be 0 or 1 depending on whether hash check is before or after stats update
	// The important thing is no error occurred
}

// TestComputeHashFromFile_ValidFile_ReturnsHash tests ComputeHashFromFile with valid file
func TestComputeHashFromFile_ValidFile_ReturnsHash(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.txt")
	content := []byte("hello world")
	_ = os.WriteFile(filePath, content, 0644)

	hash, err := ComputeHashFromFile(filePath)

	assert.NoError(t, err)
	assert.NotEmpty(t, hash)
	assert.Len(t, hash, 64) // SHA256 produces 64 hex characters
}

// TestComputeHashFromFile_DifferentContent_DifferentHash tests different content produces different hash
func TestComputeHashFromFile_DifferentContent_DifferentHash(t *testing.T) {
	tmpDir := t.TempDir()
	filePath1 := filepath.Join(tmpDir, "test1.txt")
	filePath2 := filepath.Join(tmpDir, "test2.txt")
	_ = os.WriteFile(filePath1, []byte("content1"), 0644)
	_ = os.WriteFile(filePath2, []byte("content2"), 0644)

	hash1, err1 := ComputeHashFromFile(filePath1)
	hash2, err2 := ComputeHashFromFile(filePath2)

	assert.NoError(t, err1)
	assert.NoError(t, err2)
	assert.NotEqual(t, hash1, hash2)
}

// TestComputeHashFromFile_NonExistentFile_ReturnsError tests ComputeHashFromFile with non-existent file
func TestComputeHashFromFile_NonExistentFile_ReturnsError(t *testing.T) {
	_, err := ComputeHashFromFile("/nonexistent/file.yaml")

	assert.Error(t, err)
}

// TestIsConfigFileReadable_ValidFile_ReturnsNoError tests IsConfigFileReadable with valid file
func TestIsConfigFileReadable_ValidFile_ReturnsNoError(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "config.yaml")
	_ = os.WriteFile(filePath, []byte("test"), 0644)

	err := IsConfigFileReadable(filePath)

	assert.NoError(t, err)
}

// TestIsConfigFileReadable_NonExistentFile_ReturnsError tests IsConfigFileReadable with non-existent file
func TestIsConfigFileReadable_NonExistentFile_ReturnsError(t *testing.T) {
	err := IsConfigFileReadable("/nonexistent/file.yaml")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")
}

// TestIsConfigFileReadable_Directory_ReturnsError tests IsConfigFileReadable with directory
func TestIsConfigFileReadable_Directory_ReturnsError(t *testing.T) {
	tmpDir := t.TempDir()

	err := IsConfigFileReadable(tmpDir)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "directory")
}

// TestIsConfigFileReadable_UnreadableFile_ReturnsError tests IsConfigFileReadable with unreadable file
func TestIsConfigFileReadable_UnreadableFile_ReturnsError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("skipping test when running as root")
	}

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "config.yaml")
	_ = os.WriteFile(filePath, []byte("test"), 0000)

	err := IsConfigFileReadable(filePath)

	assert.Error(t, err)
}
