package config

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// ReloadEvent represents a configuration reload event
type ReloadEvent struct {
	Success   bool          `json:"success"`
	Hash      string        `json:"hash"`
	Timestamp time.Time     `json:"timestamp"`
	Duration  time.Duration `json:"duration"`
	Error     *ConfigError  `json:"error,omitempty"`
}

// ReloadStats tracks configuration reload statistics
type ReloadStats struct {
	mu            sync.RWMutex
	TotalAttempts int          `json:"totalAttempts"`
	SuccessCount  int          `json:"successCount"`
	FailureCount  int          `json:"failureCount"`
	LastSuccess   *ReloadEvent `json:"lastSuccess,omitempty"`
	LastFailure   *ReloadEvent `json:"lastFailure,omitempty"`
	LastReloadAt  time.Time    `json:"lastReloadAt"`
}

// ReloadStatsSnapshot is a copy-safe view of ReloadStats for external consumers.
type ReloadStatsSnapshot struct {
	TotalAttempts int          `json:"totalAttempts"`
	SuccessCount  int          `json:"successCount"`
	FailureCount  int          `json:"failureCount"`
	LastSuccess   *ReloadEvent `json:"lastSuccess,omitempty"`
	LastFailure   *ReloadEvent `json:"lastFailure,omitempty"`
	LastReloadAt  time.Time    `json:"lastReloadAt"`
}

// Watcher handles configuration file watching and hot reloading
type Watcher struct {
	mu            sync.RWMutex
	configPath    string
	currentConfig *Config
	currentMeta   *ConfigMeta
	stats         ReloadStats
	reloadHook    func(*Config, *ConfigMeta)
	stopCh        chan struct{}
	doneCh        chan struct{}

	// Boot parameters (not hot-reloadable)
	debounceDuration time.Duration
	minInterval      time.Duration
	maxBackoff       time.Duration
	strictMode       bool

	// Backoff state
	consecutiveFailures int
	lastReloadTime      time.Time
}

// WatcherOptions contains options for the configuration watcher
type WatcherOptions struct {
	// DebounceDuration is the time to wait after detecting a change before reloading
	DebounceDuration time.Duration
	// MinInterval is the minimum time between reload attempts
	MinInterval time.Duration
	// MaxBackoff is the maximum backoff time after consecutive failures
	MaxBackoff time.Duration
	// StrictMode causes the service to fail fast on reload errors
	StrictMode bool
}

// DefaultWatcherOptions returns the default watcher options
func DefaultWatcherOptions() *WatcherOptions {
	return &WatcherOptions{
		DebounceDuration: 300 * time.Millisecond,
		MinInterval:      1 * time.Second,
		MaxBackoff:       30 * time.Second,
		StrictMode:       false,
	}
}

// NewWatcher creates a new configuration watcher
func NewWatcher(configPath string, initialConfig *Config, initialMeta *ConfigMeta, opts *WatcherOptions) *Watcher {
	if opts == nil {
		opts = DefaultWatcherOptions()
	}

	return &Watcher{
		configPath:       configPath,
		currentConfig:    initialConfig,
		currentMeta:      initialMeta,
		stopCh:           make(chan struct{}),
		doneCh:           make(chan struct{}),
		debounceDuration: opts.DebounceDuration,
		minInterval:      opts.MinInterval,
		maxBackoff:       opts.MaxBackoff,
		strictMode:       opts.StrictMode,
		lastReloadTime:   time.Now(),
	}
}

// SetReloadHook sets a callback function to be called after successful reload
func (w *Watcher) SetReloadHook(hook func(*Config, *ConfigMeta)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.reloadHook = hook
}

// GetCurrent returns the current configuration and metadata
func (w *Watcher) GetCurrent() (*Config, *ConfigMeta) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.currentConfig, w.currentMeta
}

// GetStats returns the current reload statistics
func (w *Watcher) GetStats() ReloadStatsSnapshot {
	w.stats.mu.RLock()
	defer w.stats.mu.RUnlock()
	return ReloadStatsSnapshot{
		TotalAttempts: w.stats.TotalAttempts,
		SuccessCount:  w.stats.SuccessCount,
		FailureCount:  w.stats.FailureCount,
		LastSuccess:   w.stats.LastSuccess,
		LastFailure:   w.stats.LastFailure,
		LastReloadAt:  w.stats.LastReloadAt,
	}
}

// Start begins watching the configuration file for changes
func (w *Watcher) Start(ctx context.Context) error {
	// Create a new file system watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create file watcher: %w", err)
	}

	// Watch the directory containing the config file
	// This is necessary because ConfigMap updates may involve symlinks
	configDir := filepath.Dir(w.configPath)
	if err := watcher.Add(configDir); err != nil {
		watcher.Close()
		return fmt.Errorf("failed to watch directory %s: %w", configDir, err)
	}

	log.Printf("ConfigWatcher: started watching %s", w.configPath)

	go w.watchLoop(ctx, watcher)

	return nil
}

// Stop stops watching the configuration file
func (w *Watcher) Stop() {
	close(w.stopCh)
	<-w.doneCh
	log.Printf("ConfigWatcher: stopped")
}

// watchLoop is the main watch loop
func (w *Watcher) watchLoop(ctx context.Context, watcher *fsnotify.Watcher) {
	defer close(w.doneCh)
	defer watcher.Close()

	var debounceTimer *time.Timer
	debounceCh := make(chan struct{}, 1)

	for {
		select {
		case <-ctx.Done():
			log.Printf("ConfigWatcher: context cancelled, stopping")
			return

		case <-w.stopCh:
			log.Printf("ConfigWatcher: stop requested")
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			return

		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			// Check if the event is for our config file
			// We need to check the actual file path, resolving symlinks if needed
			matched, err := w.isConfigFileEvent(event.Name)
			if err != nil {
				log.Printf("ConfigWatcher: error checking event: %v", err)
				continue
			}
			if !matched {
				continue
			}

			// Filter for create, write, and rename events
			if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Rename) == 0 {
				continue
			}

			log.Printf("ConfigWatcher: detected change: %s (op: %s)", event.Name, event.Op)

			// Debounce: reset the timer
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			debounceTimer = time.AfterFunc(w.debounceDuration, func() {
				select {
				case debounceCh <- struct{}{}:
				default:
					// Channel already has a pending reload
				}
			})

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("ConfigWatcher: watcher error: %v", err)

		case <-debounceCh:
			// Check min interval
			timeSinceLastReload := time.Since(w.lastReloadTime)
			if timeSinceLastReload < w.minInterval {
				waitTime := w.minInterval - timeSinceLastReload
				log.Printf("ConfigWatcher: min interval not reached, waiting %v", waitTime)
				time.Sleep(waitTime)
			}

			// Perform the reload
			w.reload()
		}
	}
}

// isConfigFileEvent checks if an event is for our config file
// It handles symlinks which ConfigMaps may use
func (w *Watcher) isConfigFileEvent(eventPath string) (bool, error) {
	// Resolve the event path to its real path
	eventRealPath, err := filepath.EvalSymlinks(eventPath)
	if err != nil {
		// If we can't resolve, check the basename as a fallback
		return filepath.Base(eventPath) == filepath.Base(w.configPath), nil
	}

	// Resolve our config path to its real path
	configRealPath, err := filepath.EvalSymlinks(w.configPath)
	if err != nil {
		return filepath.Base(eventRealPath) == filepath.Base(w.configPath), nil
	}

	// Compare the real paths
	return eventRealPath == configRealPath, nil
}

// reload performs a configuration reload
func (w *Watcher) reload() {
	startTime := time.Now()
	event := &ReloadEvent{
		Timestamp: startTime,
	}

	w.stats.mu.Lock()
	w.stats.TotalAttempts++
	w.stats.mu.Unlock()

	log.Printf("ConfigWatcher: attempting reload (attempt #%d, consecutive failures: %d)",
		w.stats.TotalAttempts, w.consecutiveFailures)

	// Load the new configuration
	newConfig, newMeta, err := LoadWithDefaults(w.configPath)
	if err != nil {
		w.handleReloadError(event, err)
		return
	}

	// Check if the hash has changed
	w.mu.RLock()
	currentHash := w.currentMeta.CurrentHash
	w.mu.RUnlock()

	if newMeta.CurrentHash == currentHash {
		log.Printf("ConfigWatcher: hash unchanged (%s), skipping reload", currentHash)
		return
	}

	// Validate the new configuration
	validation := newConfig.Validate()
	if !validation.Valid {
		cfgErr := &ConfigError{
			Code:      "CONFIG_VALIDATION_FAILED",
			Message:   fmt.Sprintf("Configuration validation failed with %d errors", len(validation.Errors)),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}
		// Log all validation errors
		for _, e := range validation.Errors {
			log.Printf("ConfigWatcher: validation error - [%s] %s: %s", e.Code, e.FieldPath, e.Message)
		}
		w.handleReloadError(event, cfgErr)
		return
	}

	// Update reload count
	newMeta.ReloadCount = w.currentMeta.ReloadCount + 1

	// Atomically swap the configuration
	w.mu.Lock()
	oldConfig := w.currentConfig
	w.currentConfig = newConfig
	w.currentMeta = newMeta
	w.mu.Unlock()

	// Reset backoff on success
	w.consecutiveFailures = 0
	w.lastReloadTime = time.Now()

	// Update stats
	event.Success = true
	event.Hash = newMeta.CurrentHash
	event.Duration = time.Since(startTime)

	w.stats.mu.Lock()
	w.stats.SuccessCount++
	w.stats.LastSuccess = event
	w.stats.LastReloadAt = time.Now()
	w.stats.mu.Unlock()

	log.Printf("ConfigWatcher: reload successful (hash: %s, duration: %v, reload #%d)",
		event.Hash, event.Duration, newMeta.ReloadCount)

	// Call the reload hook if set
	if w.reloadHook != nil {
		// Call the hook without holding the lock to avoid deadlock
		go func(cfg *Config, meta *ConfigMeta) {
			w.reloadHook(cfg, meta)
		}(newConfig, newMeta)
	}

	_ = oldConfig // Avoid unused variable warning when not in strict mode
}

// handleReloadError handles a reload error
func (w *Watcher) handleReloadError(event *ReloadEvent, err error) {
	// Convert error to ConfigError if needed
	var cfgErr *ConfigError
	if e, ok := err.(*ConfigError); ok {
		cfgErr = e
	} else {
		cfgErr = &ConfigError{
			Code:      "CONFIG_RELOAD_FAILED",
			Message:   err.Error(),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}
	}

	event.Error = cfgErr
	event.Duration = time.Since(event.Timestamp)

	// Update stats
	w.stats.mu.Lock()
	w.stats.FailureCount++
	w.stats.LastFailure = event
	w.stats.mu.Unlock()

	// Increment consecutive failures
	w.consecutiveFailures++

	log.Printf("ConfigWatcher: reload failed (consecutive failures: %d): [%s] %s",
		w.consecutiveFailures, cfgErr.Code, cfgErr.Message)

	// Calculate backoff
	backoff := w.calculateBackoff()
	log.Printf("ConfigWatcher: waiting %v before next reload attempt", backoff)

	// In strict mode, we could mark the service as unhealthy
	// For now, we keep the old config and log the error
	if w.strictMode {
		log.Printf("ConfigWatcher: STRICT MODE - reload failure puts service in unhealthy state")
	}

	// Store the error in metadata
	w.mu.Lock()
	if w.currentMeta != nil {
		w.currentMeta.LastError = cfgErr
	}
	w.mu.Unlock()
}

// calculateBackoff calculates the backoff duration based on consecutive failures
func (w *Watcher) calculateBackoff() time.Duration {
	// Exponential backoff: 2^n seconds, capped at maxBackoff
	backoff := time.Duration(1<<uint(w.consecutiveFailures-1)) * time.Second
	if backoff > w.maxBackoff {
		backoff = w.maxBackoff
	}
	if backoff < w.minInterval {
		backoff = w.minInterval
	}
	return backoff
}

// WaitForInitialLoad waits for the initial configuration to be loaded
func (w *Watcher) WaitForInitialLoad(ctx context.Context) (*Config, *ConfigMeta, error) {
	w.mu.RLock()
	cfg := w.currentConfig
	meta := w.currentMeta
	w.mu.RUnlock()

	if cfg != nil && meta != nil {
		return cfg, meta, nil
	}

	// Wait for config to be loaded
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-ticker.C:
			w.mu.RLock()
			cfg = w.currentConfig
			meta = w.currentMeta
			w.mu.RUnlock()

			if cfg != nil && meta != nil {
				return cfg, meta, nil
			}
		}
	}
}

// ForceReload forces a configuration reload
func (w *Watcher) ForceReload() error {
	// Store the old consecutive failures count
	oldFailures := w.consecutiveFailures

	// Perform the reload
	w.reload()

	w.mu.RLock()
	newMeta := w.currentMeta
	lastError := newMeta.LastError
	w.mu.RUnlock()

	// If we just had a failure after a forced reload, return an error
	if lastError != nil && w.consecutiveFailures > oldFailures {
		return fmt.Errorf("forced reload failed: [%s] %s", lastError.Code, lastError.Message)
	}

	return nil
}

// ComputeHashFromFile computes the SHA256 hash of a file
func ComputeHashFromFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

// IsConfigFileReadable checks if the config file exists and is readable
func IsConfigFileReadable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("config file does not exist: %s", path)
		}
		return fmt.Errorf("cannot access config file: %w", err)
	}

	if info.IsDir() {
		return fmt.Errorf("config path is a directory, not a file: %s", path)
	}

	// Try to read the file
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot open config file: %w", err)
	}
	file.Close()

	return nil
}
