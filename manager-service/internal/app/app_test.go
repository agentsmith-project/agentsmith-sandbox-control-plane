package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sandbox/manager/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestManager creates a minimal Manager instance for testing
func setupTestManager(t *testing.T) *Manager {
	t.Helper()

	cfg := &config.Config{
		Version: 1,
		Server: config.ServerConfig{
			HTTPPort:        8080,
			RequestIDHeader: "X-Request-Id",
			Timeouts: config.ServerTimeouts{
				ReadHeader: 5 * time.Second,
				Read:       30 * time.Second,
				Write:      60 * time.Second,
				Idle:       120 * time.Second,
			},
			MaxHeaderBytes: 1 << 20,
			Metrics: config.MetricsConfig{
				Enabled:           true,
				Path:              "/metrics",
				RequireServiceKey: false,
			},
			Debug: config.DebugConfig{
				ConfigPath:  "/debug/config",
				EnablePprof: false,
			},
		},
		Auth: config.AuthConfig{
			Enabled:             true,
			HeaderName:          "X-Service-Key",
			AcceptAuthorization: true,
			AuthorizationScheme: "ServiceKey",
			FailStatusCode:      401,
		},
		Kubernetes: config.K8sConfig{
			QPS:            50,
			Burst:          100,
			RequestTimeout: 15 * time.Second,
			Retry: config.K8sRetryConfig{
				Enabled:     true,
				MaxAttempts: 3,
				BaseBackoff: 200 * time.Millisecond,
				MaxBackoff:  2 * time.Second,
			},
		},
		Sandbox: config.SandboxConfig{
			Defaults: config.SandboxDefaults{
				Namespace:               "sandbox",
				RunnerImage:             "sandbox-runner:1.0.0",
				ImagePullPolicy:         "IfNotPresent",
				TTLSeconds:              900,
				PodReadyWait:            5 * time.Minute,
				PodPollInterval:         500 * time.Millisecond,
				TerminationGraceSeconds: 1,
				ActiveDeadlineSeconds:   0,
				ContainerName:           "runner",
				Workdir:                 "/workspace",
				Volumes:                 make(map[string]config.Volume),
				Resources: config.ResourceRequirements{
					Requests: config.ResourceList{
						CPU:    "100m",
						Memory: "256Mi",
					},
					Limits: config.ResourceList{
						CPU:              "1",
						Memory:           "1Gi",
						EphemeralStorage: "2Gi",
					},
				},
				Labels:      make(map[string]string),
				Annotations: make(map[string]string),
			},
		},
		Exec: config.ExecConfig{
			DefaultTimeout:    30 * time.Second,
			MaxTimeout:        300 * time.Second,
			StdoutMaxBytes:    1 << 20,
			StderrMaxBytes:    1 << 20,
			PreserveTailBytes: 4096,
			ExitCodeMarker: config.ExitCodeMarker{
				Key:    "__SBX_EXIT_CODE__",
				Stream: "stderr",
			},
			Shell: config.ShellConfig{
				Bin:  "sh",
				Args: []string{"-lc"},
			},
			Env: config.EnvConfig{
				AllowRegex: "^[A-Z_][A-Z0-9_]*$",
			},
			Workdir: config.WorkdirConfig{
				AllowedPrefixes: []string{"/workspace"},
			},
		},
		Files: config.FilesConfig{
			RootPrefix: "/workspace",
			Upload: config.FileUploadConfig{
				DefaultDest: "/workspace",
				MaxBytes:    50 << 20,
				Format:      "tar.gz",
			},
			Download: config.FileDownloadConfig{
				DefaultSrc: "/workspace",
				Format:     "tar.gz",
			},
			Tar: config.TarConfig{
				Bin:            "tar",
				RejectSymlinks: true,
			},
		},
		Storage: config.StorageConfig{
			Endpoint:  "https://minio.example.com",
			AccessKey: "secret-access-key-12345",
			SecretKey: "secret-secret-key-67890",
			Bucket:    "test-bucket",
			UseSSL:    true,
		},
		Buffer: config.BufferConfig{
			Capacity: 10000,
		},
		WebSocket: config.WebSocketConfig{
			ReadBufferSize:          1024,
			WriteBufferSize:         1024,
			AllowedOrigins:          []string{"http://localhost:3000"},
			AllowNonBrowserRequests: true,
			HandshakeTimeout:        "10s",
		},
	}

	// Create minimal config meta
	cfgMeta := &config.ConfigMeta{
		SchemaVersion: 1,
		SourcePath:    "/test/config.yaml",
		CurrentHash:   "test-hash-12345",
		LoadedAt:      time.Now(),
		ReloadCount:   0,
		LastError:     nil,
	}

	// Create config watcher
	cfgWatcher := config.NewWatcher(
		"/test/config.yaml",
		cfg,
		cfgMeta,
		&config.WatcherOptions{
			DebounceDuration: 300 * time.Millisecond,
			MinInterval:      1 * time.Second,
			MaxBackoff:       30 * time.Second,
			StrictMode:       false,
		},
	)

	// Create minimal Manager with only the fields needed for handleDebugConfig
	mgr := &Manager{
		cfg:        cfg,
		cfgMeta:    cfgMeta,
		cfgWatcher: cfgWatcher,
	}

	return mgr
}

func TestHandleDebugConfig_DoesNotExposeStorageCredentials(t *testing.T) {
	mgr := setupTestManager(t)

	// Create test request
	req := httptest.NewRequest("GET", "/debug/config", nil)
	w := httptest.NewRecorder()

	// Call handleDebugConfig
	mgr.handleDebugConfig(w, req)

	// Verify response status
	assert.Equal(t, http.StatusOK, w.Code)

	// Parse response
	var result map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &result)
	require.NoError(t, err)

	// Verify Config exists
	config, ok := result["config"].(map[string]interface{})
	require.True(t, ok, "config field should exist")

	// CRITICAL VERIFICATION: Storage field should NOT exist
	_, hasStorage := config["storage"]
	assert.False(t, hasStorage, "storage field should NOT be exposed in debug config")

	// Verify other expected fields exist
	_, hasVersion := config["version"]
	_, hasServer := config["server"]
	_, hasAuth := config["auth"]
	_, hasKubernetes := config["kubernetes"]
	_, hasSandbox := config["sandbox"]
	_, hasExec := config["exec"]
	_, hasFiles := config["files"]

	assert.True(t, hasVersion, "version field should exist")
	assert.True(t, hasServer, "server field should exist")
	assert.True(t, hasAuth, "auth field should exist")
	assert.True(t, hasKubernetes, "kubernetes field should exist")
	assert.True(t, hasSandbox, "sandbox field should exist")
	assert.True(t, hasExec, "exec field should exist")
	assert.True(t, hasFiles, "files field should exist")

	// Verify response body as JSON does not contain storage credentials
	bodyStr := w.Body.String()
	assert.NotContains(t, bodyStr, "secret-access-key-12345", "Response should not contain storage access key")
	assert.NotContains(t, bodyStr, "secret-secret-key-67890", "Response should not contain storage secret key")
	assert.NotContains(t, bodyStr, "AccessKey", "Response should not contain AccessKey field")
	assert.NotContains(t, bodyStr, "SecretKey", "Response should not contain SecretKey field")
}

func TestSanitizeConfig_ExcludesStorage(t *testing.T) {
	cfg := &config.Config{
		Version: 1,
		Storage: config.StorageConfig{
			Endpoint:  "https://minio.example.com",
			AccessKey: "secret-key",
			SecretKey: "secret",
			Bucket:    "test-bucket",
			UseSSL:    true,
		},
		Server: config.ServerConfig{
			HTTPPort: 8080,
			Timeouts: config.ServerTimeouts{
				ReadHeader: 5 * time.Second,
				Read:       30 * time.Second,
				Write:      60 * time.Second,
				Idle:       120 * time.Second,
			},
			MaxHeaderBytes: 1 << 20,
			Metrics: config.MetricsConfig{
				Enabled:           true,
				Path:              "/metrics",
				RequireServiceKey: false,
			},
			Debug: config.DebugConfig{
				ConfigPath:  "/debug/config",
				EnablePprof: false,
			},
		},
		Auth: config.AuthConfig{
			Enabled:             true,
			HeaderName:          "X-Service-Key",
			AcceptAuthorization: true,
			AuthorizationScheme: "ServiceKey",
			FailStatusCode:      401,
		},
		Kubernetes: config.K8sConfig{
			QPS:            50,
			Burst:          100,
			RequestTimeout: 15 * time.Second,
			Retry: config.K8sRetryConfig{
				Enabled:     true,
				MaxAttempts: 3,
				BaseBackoff: 200 * time.Millisecond,
				MaxBackoff:  2 * time.Second,
			},
		},
		Sandbox: config.SandboxConfig{
			Defaults: config.SandboxDefaults{
				Namespace:               "sandbox",
				RunnerImage:             "sandbox-runner:1.0.0",
				ImagePullPolicy:         "IfNotPresent",
				TTLSeconds:              900,
				PodReadyWait:            5 * time.Minute,
				PodPollInterval:         500 * time.Millisecond,
				TerminationGraceSeconds: 1,
				ActiveDeadlineSeconds:   0,
				ContainerName:           "runner",
				Workdir:                 "/workspace",
				Volumes:                 make(map[string]config.Volume),
				Resources: config.ResourceRequirements{
					Requests: config.ResourceList{
						CPU:    "100m",
						Memory: "256Mi",
					},
					Limits: config.ResourceList{
						CPU:              "1",
						Memory:           "1Gi",
						EphemeralStorage: "2Gi",
					},
				},
				Labels:      make(map[string]string),
				Annotations: make(map[string]string),
			},
		},
		Exec: config.ExecConfig{
			DefaultTimeout:    30 * time.Second,
			MaxTimeout:        300 * time.Second,
			StdoutMaxBytes:    1 << 20,
			StderrMaxBytes:    1 << 20,
			PreserveTailBytes: 4096,
			ExitCodeMarker: config.ExitCodeMarker{
				Key:    "__SBX_EXIT_CODE__",
				Stream: "stderr",
			},
			Shell: config.ShellConfig{
				Bin:  "sh",
				Args: []string{"-lc"},
			},
			Env: config.EnvConfig{
				AllowRegex: "^[A-Z_][A-Z0-9_]*$",
			},
			Workdir: config.WorkdirConfig{
				AllowedPrefixes: []string{"/workspace"},
			},
		},
		Files: config.FilesConfig{
			RootPrefix: "/workspace",
			Upload: config.FileUploadConfig{
				DefaultDest: "/workspace",
				MaxBytes:    50 << 20,
				Format:      "tar.gz",
			},
			Download: config.FileDownloadConfig{
				DefaultSrc: "/workspace",
				Format:     "tar.gz",
			},
			Tar: config.TarConfig{
				Bin:            "tar",
				RejectSymlinks: true,
			},
		},
		Buffer: config.BufferConfig{
			Capacity: 10000,
		},
		WebSocket: config.WebSocketConfig{
			ReadBufferSize:          1024,
			WriteBufferSize:         1024,
			AllowedOrigins:          []string{"http://localhost:3000"},
			AllowNonBrowserRequests: true,
			HandshakeTimeout:        "10s",
		},
	}

	result := sanitizeConfig(cfg)

	// Verify: The result should not have Storage field
	// This is verified at compile time since DebugConfigConfig has no Storage field
	// If someone tries to add Storage to sanitizeConfig, it won't compile

	// Verify other fields are copied correctly
	assert.Equal(t, cfg.Version, result.Version)
	assert.Equal(t, cfg.Server.HTTPPort, result.Server.HTTPPort)
	assert.Equal(t, cfg.Auth.Enabled, result.Auth.Enabled)
	assert.Equal(t, cfg.Kubernetes.QPS, result.Kubernetes.QPS)

	// Verify no storage in JSON output
	resultJSON, _ := json.Marshal(result)
	assert.NotContains(t, string(resultJSON), "storage", "sanitized config should not contain storage field")
	assert.NotContains(t, string(resultJSON), cfg.Storage.AccessKey, "sanitized config should not contain access key")
	assert.NotContains(t, string(resultJSON), cfg.Storage.SecretKey, "sanitized config should not contain secret key")
}

func TestHandleDebugConfig_OnlyGetAllowed(t *testing.T) {
	mgr := setupTestManager(t)

	// POST request should be rejected
	req := httptest.NewRequest("POST", "/debug/config", nil)
	w := httptest.NewRecorder()
	mgr.handleDebugConfig(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)

	// PUT request should be rejected
	req = httptest.NewRequest("PUT", "/debug/config", nil)
	w = httptest.NewRecorder()
	mgr.handleDebugConfig(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)

	// DELETE request should be rejected
	req = httptest.NewRequest("DELETE", "/debug/config", nil)
	w = httptest.NewRecorder()
	mgr.handleDebugConfig(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)

	// GET request should succeed
	req = httptest.NewRequest("GET", "/debug/config", nil)
	w = httptest.NewRecorder()
	mgr.handleDebugConfig(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHandleDebugConfig_ResponseStructure(t *testing.T) {
	mgr := setupTestManager(t)

	req := httptest.NewRequest("GET", "/debug/config", nil)
	w := httptest.NewRecorder()

	mgr.handleDebugConfig(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var result map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &result)
	require.NoError(t, err)

	// Verify top-level structure
	_, hasMeta := result["meta"]
	_, hasConfig := result["config"]
	_, hasBoot := result["boot"]

	assert.True(t, hasMeta, "meta field should exist")
	assert.True(t, hasConfig, "config field should exist")
	assert.True(t, hasBoot, "boot field should exist")
}

func TestHandleDebugConfig_MetaStructure(t *testing.T) {
	mgr := setupTestManager(t)

	req := httptest.NewRequest("GET", "/debug/config", nil)
	w := httptest.NewRecorder()

	mgr.handleDebugConfig(w, req)

	var result map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &result)
	require.NoError(t, err)

	meta, ok := result["meta"].(map[string]interface{})
	require.True(t, ok, "meta should be an object")

	// Verify meta fields
	_, hasSchemaVersion := meta["schemaVersion"]
	_, hasSourcePath := meta["sourcePath"]
	_, hasCurrentHash := meta["currentHash"]
	_, hasLoadedAt := meta["loadedAt"]
	_, hasReloadCount := meta["reloadCount"]

	assert.True(t, hasSchemaVersion, "meta.schemaVersion should exist")
	assert.True(t, hasSourcePath, "meta.sourcePath should exist")
	assert.True(t, hasCurrentHash, "meta.currentHash should exist")
	assert.True(t, hasLoadedAt, "meta.loadedAt should exist")
	assert.True(t, hasReloadCount, "meta.reloadCount should exist")
}
