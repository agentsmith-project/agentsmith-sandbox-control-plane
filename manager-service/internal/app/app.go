package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/sandbox/manager/internal/auth"
	"github.com/sandbox/manager/internal/buffer"
	"github.com/sandbox/manager/internal/config"
	"github.com/sandbox/manager/internal/finalizer"
	"github.com/sandbox/manager/internal/httpapi"
	"github.com/sandbox/manager/internal/k8s"
	"github.com/sandbox/manager/internal/observability"
	"github.com/sandbox/manager/internal/ratelimit"
	"github.com/sandbox/manager/internal/session"
	"github.com/sandbox/manager/internal/storage"
	"github.com/sandbox/manager/internal/websocket"
)

var version = "dev"

// Manager is the main service manager.
type Manager struct {
	cfg           *config.Config
	cfgMeta       *config.ConfigMeta
	cfgWatcher    *config.Watcher
	k8sClient     *k8s.Client
	k8sExecutor   *k8s.Executor
	authValidator *auth.ServiceKeyValidator
	healthChecker *observability.HealthChecker
	metrics       *observability.MetricsRegistry
	httpServer    *http.Server
	ctx           context.Context
	cancel        context.CancelFunc

	// New components
	sessionManager   *session.Manager
	bufferManager    *buffer.Manager
	storageClient    *storage.Client
	wsHandler        *websocket.Handler
	finalizerHandler *finalizer.Handler
	rateLimiter      *ratelimit.Limiter
}

// Main is the entry point for the sandbox manager.
func Main() {
	mainImpl()
}

func mainImpl() {
	log.Printf("Sandbox Manager v%s starting...", version)

	observability.InitLogging()
	observability.Info("Sandbox Manager v%s starting", version)

	bootCfg := loadBootConfig()

	cfg, cfgMeta, err := config.LoadWithDefaults(bootCfg.ConfigPath)
	if err != nil {
		log.Fatalf("Failed to load initial configuration: %v", err)
	}

	validation := cfg.Validate()
	if !validation.Valid {
		log.Printf("Configuration validation failed:")
		for _, e := range validation.Errors {
			log.Printf("  [%s] %s: %s", e.Code, e.FieldPath, e.Message)
		}
		log.Fatalf("Configuration validation failed (%d errors)", len(validation.Errors))
	}

	log.Printf("Configuration loaded (hash=%s, source=%s)", cfgMeta.CurrentHash, cfgMeta.SourcePath)

	k8sClient, err := k8s.NewClient(&k8s.ClientConfig{
		Namespace:      cfg.Sandbox.Defaults.Namespace,
		QPS:            cfg.Kubernetes.QPS,
		Burst:          cfg.Kubernetes.Burst,
		RequestTimeout: cfg.Kubernetes.RequestTimeout,
		Retry: &k8s.RetryConfig{
			Enabled:     cfg.Kubernetes.Retry.Enabled,
			MaxAttempts: cfg.Kubernetes.Retry.MaxAttempts,
			BaseBackoff: cfg.Kubernetes.Retry.BaseBackoff,
			MaxBackoff:  cfg.Kubernetes.Retry.MaxBackoff,
		},
	})
	if err != nil {
		log.Fatalf("Failed to create K8s client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := k8sClient.CheckReady(ctx); err != nil {
		log.Fatalf("K8s readiness check failed: %v", err)
	}

	k8sExecutor := k8s.NewExecutor(k8sClient)

	serviceKeys := auth.ParseServiceKeys(os.Getenv("SERVICE_KEYS"))
	authValidator, err := auth.NewServiceKeyValidator(serviceKeys)
	if err != nil {
		log.Fatalf("Failed to initialize service key validator: %v", err)
	}
	log.Printf("Service key validator initialized (%d keys)", authValidator.Count())

	cfgWatcher := config.NewWatcher(
		bootCfg.ConfigPath,
		cfg,
		cfgMeta,
		&config.WatcherOptions{
			DebounceDuration: bootCfg.DebounceDuration,
			MinInterval:      bootCfg.MinInterval,
			MaxBackoff:       bootCfg.MaxBackoff,
			StrictMode:       bootCfg.StrictMode,
		},
	)

	// Initialize new components
	log.Printf("Initializing session manager...")
	sessionManager := session.NewManager()

	log.Printf("Initializing buffer manager...")
	bufferManager := buffer.NewManager()

	// Initialize storage client
	// Try loading from credentials file first, fall back to environment variables
	storageCreds, err := storage.LoadCredentials()
	if err != nil {
		log.Printf("Failed to load storage credentials, using defaults: %v", err)
		// Use defaults for local development
		storageCreds = &storage.Credentials{
			Endpoint:  getEnvOrDefault("STORAGE_ENDPOINT", "localhost:9000"),
			AccessKey: getEnvOrDefault("STORAGE_ACCESS_KEY", "minioadmin"),
			SecretKey: getEnvOrDefault("STORAGE_SECRET_KEY", "minioadmin"),
			Bucket:    getEnvOrDefault("STORAGE_BUCKET", "sandboxes"),
			UseSSL:    os.Getenv("STORAGE_USE_SSL") == "true",
		}
	}

	log.Printf("Initializing storage client (endpoint=%s, bucket=%s, ssl=%v)",
		storageCreds.Endpoint, storageCreds.Bucket, storageCreds.UseSSL)
	storageClient, err := storage.NewClientWithCreds(storageCreds)
	if err != nil {
		log.Fatalf("Failed to create storage client: %v", err)
	}
	log.Printf("Storage client initialized successfully")

	// Initialize WebSocket handler with all dependencies
	log.Printf("Initializing WebSocket handler...")
	wsHandler := websocket.NewHandler(
		sessionManager,
		bufferManager,
		k8sClient,
		storageClient,
		cfg.Sandbox.Defaults.Namespace,
		cfg,
	)

	// Initialize rate limiter
	log.Printf("Initializing rate limiter...")
	rateLimiterConfig := &ratelimit.Config{
		GlobalRPS:       100,
		GlobalBurst:     200,
		PerIPRPS:        10,
		PerIPBurst:      20,
		PerSessionRPS:   5,
		PerSessionBurst: 10,
		CleanupInterval: 5 * time.Minute,
	}
	rateLimiter := ratelimit.NewLimiter(rateLimiterConfig)
	log.Printf("Rate limiter initialized")

	// Initialize finalizer handler
	log.Printf("Initializing finalizer handler...")
	finalizerHandler, err := finalizer.NewHandler(&finalizer.HandlerConfig{
		K8sClient:     k8sClient,
		StorageClient: storageClient,
		Namespace:     cfg.Sandbox.Defaults.Namespace,
		CheckInterval: 10 * time.Second,
	})
	if err != nil {
		log.Fatalf("Failed to create finalizer handler: %v", err)
	}

	// Create manager lifecycle context
	mgrCtx, mgrCancel := context.WithCancel(context.Background())

	mgr := &Manager{
		cfg:              cfg,
		cfgMeta:          cfgMeta,
		cfgWatcher:       cfgWatcher,
		k8sClient:        k8sClient,
		k8sExecutor:      k8sExecutor,
		authValidator:    authValidator,
		healthChecker:    observability.NewHealthChecker(),
		metrics:          observability.GetMetrics(),
		sessionManager:   sessionManager,
		bufferManager:    bufferManager,
		storageClient:    storageClient,
		wsHandler:        wsHandler,
		finalizerHandler: finalizerHandler,
		rateLimiter:      rateLimiter,
		ctx:              mgrCtx,
		cancel:           mgrCancel,
	}

	mgr.setupReadinessChecks()
	mgr.setupHTTPServer()

	// Start finalizer handler in background goroutine with manager lifecycle context
	go mgr.finalizerHandler.Start(mgr.ctx)
	log.Printf("Finalizer handler started")

	// Start session cleanup goroutine with manager lifecycle context
	go mgr.sessionManager.StartCleanup(mgr.ctx, 5*time.Minute)
	log.Printf("Session cleanup started (interval=5m)")

	if err := cfgWatcher.Start(context.Background()); err != nil {
		log.Printf("Warning: Config watcher failed to start (hot reload disabled): %v", err)
	} else {
		defer cfgWatcher.Stop()
	}

	log.Printf("Sandbox Manager started on port %d", cfg.Server.HTTPPort)
	log.Printf("  Health check: http://localhost:%d/healthz", cfg.Server.HTTPPort)
	log.Printf("  Readiness:    http://localhost:%d/readyz", cfg.Server.HTTPPort)
	log.Printf("  Metrics:      http://localhost:%d%s", cfg.Server.HTTPPort, cfg.Server.Metrics.Path)
	log.Printf("  WebSocket:    ws://localhost:%d/ws", cfg.Server.HTTPPort)
	log.Printf("  Debug config: http://localhost:%d%s", cfg.Server.HTTPPort, cfg.Server.Debug.ConfigPath)

	mgr.waitForShutdown()

	log.Printf("Sandbox Manager stopped")
}

type BootConfig struct {
	ConfigPath       string
	DebounceDuration time.Duration
	MinInterval      time.Duration
	MaxBackoff       time.Duration
	StrictMode       bool
}

func loadBootConfig() *BootConfig {
	cfg := &BootConfig{
		ConfigPath:       getEnvOrDefault("CONFIG_PATH", "/etc/sandbox-manager/manager-config.yaml"),
		DebounceDuration: parseDuration(getEnvOrDefault("CONFIG_RELOAD_DEBOUNCE", "300ms")),
		MinInterval:      parseDuration(getEnvOrDefault("CONFIG_RELOAD_MIN_INTERVAL", "1s")),
		MaxBackoff:       parseDuration(getEnvOrDefault("CONFIG_RELOAD_BACKOFF_MAX", "30s")),
		StrictMode:       os.Getenv("STRICT_CONFIG_RELOAD") == "true",
	}

	if err := config.IsConfigFileReadable(cfg.ConfigPath); err != nil {
		log.Printf("Warning: %v", err)
	}

	return cfg
}

func (m *Manager) setupReadinessChecks() {
	m.healthChecker.AddReadyCheck(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return m.k8sClient.CheckReady(ctx)
	})

	m.healthChecker.AddReadyCheck(func() error {
		if m.cfg == nil {
			return fmt.Errorf("configuration not loaded")
		}
		return nil
	})
}

func (m *Manager) setupHTTPServer() {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", m.healthChecker.HandleHealthz)
	mux.HandleFunc("/readyz", m.healthChecker.HandleReadyz)

	if m.cfg.Server.Metrics.Enabled {
		metricsHandler := m.metrics.Handler()
		if m.cfg.Server.Metrics.RequireServiceKey {
			authMiddleware := auth.ServiceKeyMiddleware(
				m.authValidator,
				m.cfg.Auth.HeaderName,
				m.cfg.Auth.AcceptAuthorization,
				m.cfg.Auth.AuthorizationScheme,
				m.cfg.Auth.FailStatusCode,
			)
			mux.Handle(m.cfg.Server.Metrics.Path, authMiddleware(http.HandlerFunc(metricsHandler)))
		} else {
			mux.HandleFunc(m.cfg.Server.Metrics.Path, metricsHandler)
		}
	}

	mux.HandleFunc(m.cfg.Server.Debug.ConfigPath, m.handleDebugConfig)

	// Add WebSocket route with service key authentication via query parameter
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		// 从 URL 参数获取 service key
		serviceKey := r.URL.Query().Get("service_key")

		// 验证 service key
		if !m.authValidator.Validate(serviceKey) {
			http.Error(w, "Unauthorized: invalid or missing service_key", http.StatusUnauthorized)
			return
		}

		// 认证通过，转发到 WebSocket handler
		m.wsHandler.ServeHTTP(w, r)
	})

	v1Handler := m.buildV1Handler()
	if m.cfg.Auth.Enabled {
		authMiddleware := auth.ServiceKeyMiddleware(
			m.authValidator,
			m.cfg.Auth.HeaderName,
			m.cfg.Auth.AcceptAuthorization,
			m.cfg.Auth.AuthorizationScheme,
			m.cfg.Auth.FailStatusCode,
		)
		v1Handler = authMiddleware(v1Handler)
	}

	reqIDMiddleware := observability.RequestIDMiddleware(m.cfg.Server.RequestIDHeader)
	v1Handler = reqIDMiddleware(v1Handler)
	v1Handler = m.observabilityMiddleware(v1Handler)
	v1Handler = m.rateLimiter.Middleware(v1Handler)

	mux.Handle("/v1/", v1Handler)

	m.httpServer = &http.Server{
		Addr:              fmt.Sprintf(":%d", m.cfg.Server.HTTPPort),
		Handler:           mux,
		ReadTimeout:       m.cfg.Server.Timeouts.Read,
		WriteTimeout:      m.cfg.Server.Timeouts.Write,
		IdleTimeout:       m.cfg.Server.Timeouts.Idle,
		ReadHeaderTimeout: m.cfg.Server.Timeouts.ReadHeader,
		MaxHeaderBytes:    m.cfg.Server.MaxHeaderBytes,
	}

	go func() {
		if err := m.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()
}

func (m *Manager) GetConfig() *config.Config {
	return m.cfg
}

func (m *Manager) GetK8sClient() *k8s.Client {
	return m.k8sClient
}

func (m *Manager) GetK8sExecutor() *k8s.Executor {
	return m.k8sExecutor
}

func (m *Manager) GetMetrics() *observability.MetricsRegistry {
	return m.metrics
}

func (m *Manager) GetSessionManager() *session.Manager {
	return m.sessionManager
}

func (m *Manager) GetBufferManager() *buffer.Manager {
	return m.bufferManager
}

func (m *Manager) GetStorageClient() *storage.Client {
	return m.storageClient
}

func (m *Manager) buildV1Handler() http.Handler {
	mux := http.NewServeMux()
	handlers := httpapi.NewHandlers(m)

	// Register explicit routes for /v1/sandboxes/{sessionId} endpoints
	// Order matters: more specific routes must be registered first
	mux.HandleFunc("/v1/sandboxes/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Parse the route to determine which handler to use
		route, sessionId := parseSandboxRoute(path)
		if sessionId == "" {
			httpapi.WriteError(w, r, httpapi.ErrBadRequest, "sessionId required")
			return
		}

		switch route {
		case "touch":
			if r.Method != http.MethodPost {
				httpapi.WriteError(w, r, httpapi.ErrBadRequest, "Method not allowed")
				return
			}
			handlers.HandleTouch(w, r)

		case "exec":
			if r.Method != http.MethodPost {
				httpapi.WriteError(w, r, httpapi.ErrBadRequest, "Method not allowed")
				return
			}
			handlers.HandleExec(w, r)

		case "files/upload":
			if r.Method != http.MethodPost {
				httpapi.WriteError(w, r, httpapi.ErrBadRequest, "Method not allowed")
				return
			}
			handlers.HandleUpload(w, r)

		case "files/download":
			if r.Method != http.MethodGet {
				httpapi.WriteError(w, r, httpapi.ErrBadRequest, "Method not allowed")
				return
			}
			handlers.HandleDownload(w, r)

		case "sandbox":
			// Direct /v1/sandboxes/{sessionId} endpoint
			switch r.Method {
			case http.MethodPut:
				handlers.HandleCreateSandbox(w, r)
			case http.MethodDelete:
				handlers.HandleDelete(w, r)
			default:
				httpapi.WriteError(w, r, httpapi.ErrBadRequest, "Method not allowed")
			}

		default:
			httpapi.WriteError(w, r, httpapi.ErrBadRequest, "Not found")
		}
	})

	return mux
}

// parseSandboxRoute parses the path and returns the route type and sessionId
// Path format: /v1/sandboxes/{sessionId}[/{action}]
// Returns route type and sessionId
func parseSandboxRoute(path string) (route string, sessionId string) {
	// Path format: /v1/sandboxes/{sessionId}/...
	// Expected parts: ["", "v1", "sandboxes", "{sessionId}", "..."]
	parts := strings.Split(path, "/")
	if len(parts) < 4 || parts[1] != "v1" || parts[2] != "sandboxes" {
		return "", ""
	}

	sessionId = parts[3]
	if sessionId == "" {
		return "", ""
	}

	// No additional parts - direct sandbox operation
	if len(parts) == 4 {
		return "sandbox", sessionId
	}

	// Check for action routes
	if len(parts) >= 5 {
		action := parts[4]
		switch action {
		case "touch":
			if len(parts) == 5 {
				return "touch", sessionId
			}
		case "exec":
			if len(parts) == 5 {
				return "exec", sessionId
			}
		case "files":
			if len(parts) == 6 {
				switch parts[5] {
				case "upload":
					return "files/upload", sessionId
				case "download":
					return "files/download", sessionId
				}
			}
		}
	}

	return "", ""
}

func (m *Manager) handleDebugConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	cfg, meta := m.cfgWatcher.GetCurrent()

	resp := httpapi.DebugConfigResponse{
		Meta: httpapi.DebugConfigMeta{
			SchemaVersion: meta.SchemaVersion,
			SourcePath:    meta.SourcePath,
			CurrentHash:   meta.CurrentHash,
			LoadedAt:      meta.LoadedAt.Format(time.RFC3339),
			ReloadCount:   meta.ReloadCount,
			LastError:     convertConfigError(meta.LastError),
		},
		Config: sanitizeConfig(cfg),
		Boot: httpapi.DebugConfigBoot{
			ConfigPath:       os.Getenv("CONFIG_PATH"),
			DebounceDuration: os.Getenv("CONFIG_RELOAD_DEBOUNCE"),
			MinInterval:      os.Getenv("CONFIG_RELOAD_MIN_INTERVAL"),
			MaxBackoff:       os.Getenv("CONFIG_RELOAD_BACKOFF_MAX"),
			StrictMode:       os.Getenv("STRICT_CONFIG_RELOAD") == "true",
		},
	}

	json.NewEncoder(w).Encode(resp)
}

func (m *Manager) observabilityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Wrap the response writer to capture status code
		wrapped := &observability.ResponseWriterWrapper{
			ResponseWriter: w,
			StatusCode:     http.StatusOK, // Default to 200
		}

		next.ServeHTTP(wrapped, r)

		duration := time.Since(start)
		method := r.Method

		// Use path patterns to avoid high cardinality
		// Replace session IDs with placeholder
		path := observability.PatternizePath(r.URL.Path)

		m.metrics.RecordHTTPRequest(method, path, wrapped.StatusCode, duration)
	})
}

func (m *Manager) waitForShutdown() {
	sigint := make(chan os.Signal, 1)
	signal.Notify(sigint, os.Interrupt, syscall.SIGTERM)
	<-sigint

	log.Printf("Shutdown signal received, gracefully shutting down...")

	// Cancel manager lifecycle context to stop all background goroutines
	// This includes the finalizer handler which was started with m.ctx
	if m.cancel != nil {
		m.cancel()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := m.httpServer.Shutdown(ctx); err != nil {
		log.Printf("HTTP server shutdown error: %v", err)
	}

	log.Printf("Shutdown complete")
}

func getEnvOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

func parseDuration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		log.Printf("Warning: invalid duration %q, using default", s)
		return 0
	}
	return d
}

func convertConfigError(err *config.ConfigError) *httpapi.ConfigError {
	if err == nil {
		return nil
	}
	return &httpapi.ConfigError{
		Code:      err.Code,
		Message:   err.Message,
		FieldPath: err.FieldPath,
		RuleID:    err.RuleID,
		Rule:      err.Rule,
		Timestamp: err.Timestamp,
	}
}

// sanitizeConfig creates a safe subset of configuration for debug output.
// NOTE: Storage configuration (AccessKey, SecretKey) is intentionally excluded
// to prevent credential exposure via debug endpoint.
func sanitizeConfig(cfg *config.Config) httpapi.DebugConfigConfig {
	return httpapi.DebugConfigConfig{
		Version: cfg.Version,
		Server: httpapi.DebugServerConfig{
			HTTPPort:        cfg.Server.HTTPPort,
			RequestIDHeader: cfg.Server.RequestIDHeader,
			Timeouts: map[string]string{
				"readHeader": cfg.Server.Timeouts.ReadHeader.String(),
				"read":       cfg.Server.Timeouts.Read.String(),
				"write":      cfg.Server.Timeouts.Write.String(),
				"idle":       cfg.Server.Timeouts.Idle.String(),
			},
			MaxHeaderBytes: cfg.Server.MaxHeaderBytes,
			Metrics: httpapi.DebugMetricsConfig{
				Enabled:           cfg.Server.Metrics.Enabled,
				Path:              cfg.Server.Metrics.Path,
				RequireServiceKey: cfg.Server.Metrics.RequireServiceKey,
			},
			Debug: httpapi.DebugDebugConfig{
				ConfigPath:  cfg.Server.Debug.ConfigPath,
				EnablePprof: cfg.Server.Debug.EnablePprof,
			},
		},
		Auth: httpapi.DebugAuthConfig{
			Enabled:             cfg.Auth.Enabled,
			HeaderName:          cfg.Auth.HeaderName,
			AcceptAuthorization: cfg.Auth.AcceptAuthorization,
			AuthorizationScheme: cfg.Auth.AuthorizationScheme,
			FailStatusCode:      cfg.Auth.FailStatusCode,
		},
		Kubernetes: httpapi.DebugK8sConfig{
			QPS:            cfg.Kubernetes.QPS,
			Burst:          cfg.Kubernetes.Burst,
			RequestTimeout: cfg.Kubernetes.RequestTimeout.String(),
			Retry: httpapi.DebugK8sRetryConfig{
				Enabled:     cfg.Kubernetes.Retry.Enabled,
				MaxAttempts: cfg.Kubernetes.Retry.MaxAttempts,
				BaseBackoff: cfg.Kubernetes.Retry.BaseBackoff.String(),
				MaxBackoff:  cfg.Kubernetes.Retry.MaxBackoff.String(),
			},
		},
		Exec: httpapi.DebugExecConfig{
			DefaultTimeout:    cfg.Exec.DefaultTimeout.String(),
			MaxTimeout:        cfg.Exec.MaxTimeout.String(),
			StdoutMaxBytes:    cfg.Exec.StdoutMaxBytes,
			StderrMaxBytes:    cfg.Exec.StderrMaxBytes,
			PreserveTailBytes: cfg.Exec.PreserveTailBytes,
			ExitCodeMarker: httpapi.DebugExitCodeMarker{
				Key:    cfg.Exec.ExitCodeMarker.Key,
				Stream: cfg.Exec.ExitCodeMarker.Stream,
			},
			Shell: httpapi.DebugShellConfig{
				Bin:  cfg.Exec.Shell.Bin,
				Args: cfg.Exec.Shell.Args,
			},
			Env: httpapi.DebugEnvConfig{
				AllowRegex: cfg.Exec.Env.AllowRegex,
			},
			Workdir: httpapi.DebugWorkdirConfig{
				AllowedPrefixes: cfg.Exec.Workdir.AllowedPrefixes,
			},
		},
		Files: httpapi.DebugFilesConfig{
			RootPrefix: cfg.Files.RootPrefix,
			Upload: httpapi.DebugFileUploadConfig{
				DefaultDest: cfg.Files.Upload.DefaultDest,
				MaxBytes:    cfg.Files.Upload.MaxBytes,
				Format:      cfg.Files.Upload.Format,
			},
			Download: httpapi.DebugFileDownloadConfig{
				DefaultSrc: cfg.Files.Download.DefaultSrc,
				Format:     cfg.Files.Download.Format,
			},
			Tar: httpapi.DebugTarConfig{
				Bin:            cfg.Files.Tar.Bin,
				RejectSymlinks: cfg.Files.Tar.RejectSymlinks,
			},
		},
	}
}
