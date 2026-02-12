package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sandbox/manager/internal/config"
	"github.com/sandbox/manager/internal/finalizer"
	"github.com/sandbox/manager/internal/httpapi"
	"github.com/sandbox/manager/internal/k8s"
	"github.com/sandbox/manager/internal/observability"
	"github.com/sandbox/manager/internal/storage"
)

var version = "dev"

// Manager is the main service manager.
type Manager struct {
	cfg            *config.Config
	cfgMeta        *config.ConfigMeta
	cfgWatcher     *config.Watcher
	k8sClient      *k8s.Client
	k8sExecutor    *k8s.Executor
	healthChecker  *observability.HealthChecker
	metrics        *observability.MetricsRegistry
	storageClient  *storage.Client
	finalizerHandler *finalizer.Handler
	httpServer     *http.Server
	httpErrCh      chan error // receives fatal HTTP server errors
	ctx            context.Context
	cancel         context.CancelFunc
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
		Namespace:        cfg.Sandbox.Defaults.Namespace,
		DefaultContainer: cfg.Sandbox.Defaults.ContainerName,
		QPS:              cfg.Kubernetes.QPS,
		Burst:            cfg.Kubernetes.Burst,
		RequestTimeout:   cfg.Kubernetes.RequestTimeout,
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

	// Initialize storage client
	storageCreds, err := storage.LoadCredentials()
	if err != nil {
		log.Printf("Failed to load storage credentials, using defaults: %v", err)
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
		healthChecker:    observability.NewHealthChecker(),
		metrics:          observability.GetMetrics(),
		storageClient:    storageClient,
		finalizerHandler: finalizerHandler,
		ctx:              mgrCtx,
		cancel:           mgrCancel,
	}

	mgr.setupReadinessChecks()
	mgr.setupHTTPServer()

	// Start finalizer handler (launches its own background goroutine)
	mgr.finalizerHandler.Start(mgr.ctx)
	log.Printf("Finalizer handler started")

	if err := cfgWatcher.Start(context.Background()); err != nil {
		log.Printf("Warning: Config watcher failed to start (hot reload disabled): %v", err)
	} else {
		defer cfgWatcher.Stop()
	}

	log.Printf("Sandbox Manager started on port %d", cfg.Server.HTTPPort)
	log.Printf("  Health check: http://localhost:%d/healthz", cfg.Server.HTTPPort)
	log.Printf("  Readiness:    http://localhost:%d/readyz", cfg.Server.HTTPPort)
	log.Printf("  Metrics:      http://localhost:%d%s", cfg.Server.HTTPPort, cfg.Server.Metrics.Path)
	log.Printf("  API:          http://localhost:%d/v1/sandboxes/", cfg.Server.HTTPPort)

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

	// Health and readiness endpoints (no auth)
	mux.HandleFunc("/healthz", m.healthChecker.HandleHealthz)
	mux.HandleFunc("/readyz", m.healthChecker.HandleReadyz)

	// Metrics endpoint
	if m.cfg.Server.Metrics.Enabled {
		metricsHandler := m.metrics.Handler()
		mux.HandleFunc(m.cfg.Server.Metrics.Path, metricsHandler)
	}

	// Debug config endpoint
	mux.HandleFunc(m.cfg.Server.Debug.ConfigPath, m.handleDebugConfig)

	// V1 API routes (no auth, no rate limiting)
	v1Handler := m.buildV1Handler()
	reqIDMiddleware := observability.RequestIDMiddleware(m.cfg.Server.RequestIDHeader)
	v1Handler = reqIDMiddleware(v1Handler)
	v1Handler = m.observabilityMiddleware(v1Handler)
	mux.Handle("/v1/", v1Handler)

	// WriteTimeout is intentionally set to 0 (disabled) because SSE exec
	// streaming can last up to exec.maxTimeout (default 300s). Go's
	// http.Server.WriteTimeout covers the entire response lifecycle and
	// would kill long-running SSE streams prematurely. Per-request
	// timeouts are enforced at the handler level (exec timeout, context
	// cancellation) which is the correct place for SSE.
	m.httpServer = &http.Server{
		Addr:              fmt.Sprintf(":%d", m.cfg.Server.HTTPPort),
		Handler:           mux,
		ReadTimeout:       m.cfg.Server.Timeouts.Read,
		WriteTimeout:      0,
		IdleTimeout:       m.cfg.Server.Timeouts.Idle,
		ReadHeaderTimeout: m.cfg.Server.Timeouts.ReadHeader,
		MaxHeaderBytes:    m.cfg.Server.MaxHeaderBytes,
	}

	m.httpErrCh = make(chan error, 1)
	go func() {
		if err := m.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			m.httpErrCh <- fmt.Errorf("HTTP server error: %w", err)
		}
	}()
}

// GetConfig returns the current (possibly hot-reloaded) configuration.
// If the Watcher is running, this returns the latest validated config.
// Otherwise it falls back to the initial config.
func (m *Manager) GetConfig() *config.Config {
	if m.cfgWatcher != nil {
		cfg, _ := m.cfgWatcher.GetCurrent()
		if cfg != nil {
			return cfg
		}
	}
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

func (m *Manager) GetStorageClient() *storage.Client {
	return m.storageClient
}

func (m *Manager) buildV1Handler() http.Handler {
	mux := http.NewServeMux()
	handlers := httpapi.NewHandlers(m)

	mux.HandleFunc("/v1/sandboxes/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
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
func parseSandboxRoute(path string) (route string, sessionId string) {
	parts := splitPath(path)
	if len(parts) < 4 || parts[1] != "v1" || parts[2] != "sandboxes" {
		return "", ""
	}

	sessionId = parts[3]
	if sessionId == "" {
		return "", ""
	}

	if len(parts) == 4 {
		return "sandbox", sessionId
	}

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

func splitPath(path string) []string {
	result := []string{}
	current := ""
	for _, c := range path {
		if c == '/' {
			result = append(result, current)
			current = ""
		} else {
			current += string(c)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}

func (m *Manager) handleDebugConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	cfg, meta := m.cfgWatcher.GetCurrent()

	type debugResponse struct {
		Meta struct {
			SchemaVersion int    `json:"schemaVersion"`
			SourcePath    string `json:"sourcePath"`
			CurrentHash   string `json:"currentHash"`
			LoadedAt      string `json:"loadedAt"`
			ReloadCount   int    `json:"reloadCount"`
		} `json:"meta"`
		Config interface{} `json:"config"`
	}

	resp := debugResponse{}
	resp.Meta.SchemaVersion = meta.SchemaVersion
	resp.Meta.SourcePath = meta.SourcePath
	resp.Meta.CurrentHash = meta.CurrentHash
	resp.Meta.LoadedAt = meta.LoadedAt.Format(time.RFC3339)
	resp.Meta.ReloadCount = meta.ReloadCount
	resp.Config = cfg

	json.NewEncoder(w).Encode(resp)
}

func (m *Manager) observabilityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		wrapped := &observability.ResponseWriterWrapper{
			ResponseWriter: w,
			StatusCode:     http.StatusOK,
		}

		next.ServeHTTP(wrapped, r)

		duration := time.Since(start)
		method := r.Method
		path := observability.PatternizePath(r.URL.Path)
		m.metrics.RecordHTTPRequest(method, path, wrapped.StatusCode, duration)
	})
}

func (m *Manager) waitForShutdown() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	// Wait for either an OS signal or a fatal HTTP server error.
	select {
	case sig := <-sigCh:
		log.Printf("Shutdown signal received (%v), gracefully shutting down...", sig)
	case err := <-m.httpErrCh:
		log.Printf("Fatal: %v — initiating shutdown...", err)
	}

	// Phase 1: Drain in-flight HTTP requests.
	// This must happen BEFORE cancelling the app context, because in-flight
	// request handlers may depend on the app context being active.
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer drainCancel()

	if err := m.httpServer.Shutdown(drainCtx); err != nil {
		log.Printf("HTTP server shutdown error: %v", err)
	}

	// Phase 2: Stop background goroutines (finalizer, config watcher, etc.)
	if m.cancel != nil {
		m.cancel()
	}

	// Phase 3: Wait for finalizer handler to finish its current work.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	if m.finalizerHandler != nil {
		if err := m.finalizerHandler.Shutdown(shutdownCtx); err != nil {
			log.Printf("Finalizer shutdown error: %v", err)
		}
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
