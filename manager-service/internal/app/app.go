package app

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sandbox/manager/internal/auth"
	"github.com/sandbox/manager/internal/config"
	"github.com/sandbox/manager/internal/k8s"
	"github.com/sandbox/manager/internal/observability"
	"github.com/sandbox/manager/internal/ratelimit"
	"github.com/sandbox/manager/internal/workload"
	"github.com/sandbox/manager/internal/workspace"
)

var version = "dev"

type Manager struct {
	cfg              *config.Config
	cfgWatcher       *config.Watcher
	k8sClient        *k8s.Client
	k8sExecutor      *k8s.Executor
	authValidator    *auth.ServiceKeyValidator
	healthChecker    *observability.HealthChecker
	metrics          *observability.MetricsRegistry
	workspaceStorage *workspace.Storage
	workloadHandler  *workload.Handler
	rateLimiter      *ratelimit.Limiter
	httpServer       *http.Server
	ctx              context.Context
	cancel           context.CancelFunc
}

func Main() {
	mainImpl()
}

func mainImpl() {
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

	k8sNamespace := getEnvOrDefault("K8S_NAMESPACE", "sandbox")
	k8sClient, err := k8s.NewClient(&k8s.ClientConfig{
		Namespace:      k8sNamespace,
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

	juicefsBasePath := getEnvOrDefault("JUICEFS_BASE_PATH", "/mnt/juicefs/workloads")
	workspaceStorage := workspace.NewStorage(juicefsBasePath)
	log.Printf("Workspace storage initialized (basePath=%s)", juicefsBasePath)

	juicefsPVCName := getEnvOrDefault("JUICEFS_PVC_NAME", "juicefs-workloads-pvc")
	workloadHandler := workload.NewHandler(k8sClient, k8sExecutor, workspaceStorage, juicefsPVCName)
	log.Printf("Workload handler initialized (pvc=%s)", juicefsPVCName)

	rateLimitCfg := ratelimit.ConfigFromRequestsPerMinute(cfg.RateLimit.RequestsPerMinute)
	rateLimiter := ratelimit.NewLimiter(rateLimitCfg)
	rateLimiter.StartCleanup()
	log.Printf("Rate limiter initialized (global RPS=%.1f)", rateLimitCfg.GlobalRPS)

	mgrCtx, mgrCancel := context.WithCancel(context.Background())

	mgr := &Manager{
		cfg:              cfg,
		cfgWatcher:       cfgWatcher,
		k8sClient:        k8sClient,
		k8sExecutor:      k8sExecutor,
		authValidator:    authValidator,
		healthChecker:    observability.NewHealthChecker(),
		metrics:          observability.GetMetrics(),
		workspaceStorage: workspaceStorage,
		workloadHandler:  workloadHandler,
		rateLimiter:      rateLimiter,
		ctx:              mgrCtx,
		cancel:           mgrCancel,
	}

	mgr.setupReadinessChecks()
	mgr.setupHTTPServer()

	if err := cfgWatcher.Start(context.Background()); err != nil {
		log.Printf("Warning: Config watcher failed to start (hot reload disabled): %v", err)
	} else {
		defer cfgWatcher.Stop()
	}

	log.Printf("Sandbox Manager started on port %d", cfg.Server.HTTPPort)
	log.Printf("  Health check: http://localhost:%d/healthz", cfg.Server.HTTPPort)
	log.Printf("  Readiness:    http://localhost:%d/readyz", cfg.Server.HTTPPort)
	log.Printf("  Metrics:      http://localhost:%d%s", cfg.Server.HTTPPort, cfg.Server.Metrics.Path)
	log.Printf("  API:          http://localhost:%d/v1/workspaces/...", cfg.Server.HTTPPort)

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
		mux.HandleFunc(m.cfg.Server.Metrics.Path, metricsHandler)
	}

	authMiddleware := auth.ServiceKeyMiddleware(m.authValidator, m.cfg.Auth.HeaderName)

	v1Mux := http.NewServeMux()
	m.workloadHandler.RegisterRoutes(v1Mux)

	var v1Handler http.Handler = v1Mux
	v1Handler = authMiddleware(v1Handler)
	v1Handler = observability.RequestIDMiddleware(m.cfg.Server.RequestIDHeader)(v1Handler)
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
	sigint := make(chan os.Signal, 1)
	signal.Notify(sigint, os.Interrupt, syscall.SIGTERM)
	<-sigint

	log.Printf("Shutdown signal received, gracefully shutting down...")

	if m.cancel != nil {
		m.cancel()
	}
	if m.rateLimiter != nil {
		m.rateLimiter.Stop()
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
