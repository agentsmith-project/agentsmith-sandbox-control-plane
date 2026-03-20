package app

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/sandbox/manager/internal/auth"
	"github.com/sandbox/manager/internal/config"
	"github.com/sandbox/manager/internal/k8s"
	"github.com/sandbox/manager/internal/observability"
	"github.com/sandbox/manager/internal/ratelimit"
	"github.com/sandbox/manager/internal/workload"
	"github.com/sandbox/manager/internal/workspacebinding"
)

var version = "dev"

type Manager struct {
	cfg                     *config.Config
	k8sClient               *k8s.Client
	k8sExecutor             *k8s.Executor
	authValidator           *auth.ServiceKeyValidator
	healthChecker           *observability.HealthChecker
	metrics                 *observability.MetricsRegistry
	workloadHandler         *workload.Handler
	workspaceBindingHandler *workspacebinding.Handler
	rateLimiter             *ratelimit.Limiter
	httpServer              *http.Server
	ctx                     context.Context
	cancel                  context.CancelFunc
}

func Main() {
	mainImpl()
}

func mainImpl() {
	observability.InitLogging()
	observability.Info("Sandbox Manager v%s starting", version)

	bootCfg := loadBootConfig()

	cfg, err := config.LoadWithDefaults(bootCfg.ConfigPath)
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

	log.Printf("Configuration loaded from %s", bootCfg.ConfigPath)

	k8sNamespace := strings.TrimSpace(os.Getenv("K8S_NAMESPACE"))
	if k8sNamespace == "" {
		k8sNamespace = cfg.Sandbox.Defaults.Namespace
	}
	if k8sNamespace == "" {
		k8sNamespace = "sandbox-workloads"
	}
	k8sClient, err := k8s.NewClient(&k8s.ClientConfig{
		Namespace:      k8sNamespace,
		QPS:            cfg.Kubernetes.QPS,
		Burst:          cfg.Kubernetes.Burst,
		RequestTimeout: cfg.Kubernetes.RequestTimeout,
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

	workloadHandler := workload.NewHandler(k8sClient, k8sExecutor)
	workspaceBindingHandler := workspacebinding.NewHandler(k8sClient, workspacebinding.Options{
		Namespace:             k8sNamespace,
		CSIDriver:             getEnvOrDefault("JUICEFS_CSI_DRIVER", "csi.juicefs.com"),
		StorageCapacity:       getEnvOrDefault("JUICEFS_STORAGE_CAPACITY", "1Pi"),
		StorageClassName:      os.Getenv("JUICEFS_STORAGE_CLASS_NAME"),
		MountOptions:          parseMountOptions(os.Getenv("JUICEFS_MOUNT_OPTIONS")),
		Subdir:                os.Getenv("JUICEFS_SUBDIR"),
		MountServiceAccount:   os.Getenv("JUICEFS_MOUNT_SERVICE_ACCOUNT"),
		MountImage:            os.Getenv("JUICEFS_MOUNT_IMAGE"),
		StorageEndpoint:       getEnvOrDefault("JUICEFS_STORAGE_ENDPOINT", "http://localhost:19000"),
		StorageAccessKey:      os.Getenv("JUICEFS_STORAGE_ACCESS_KEY"),
		StorageSecretKey:      os.Getenv("JUICEFS_STORAGE_SECRET_KEY"),
	})
	log.Printf("Workload handler initialized with workspace binding model (csiDriver=%s)", getEnvOrDefault("JUICEFS_CSI_DRIVER", "csi.juicefs.com"))

	rateLimitCfg := ratelimit.ConfigFromRequestsPerMinute(cfg.RateLimit.RequestsPerMinute)
	rateLimiter := ratelimit.NewLimiter(rateLimitCfg)
	log.Printf("Rate limiter initialized (global RPS=%.1f)", rateLimitCfg.GlobalRPS)

	mgrCtx, mgrCancel := context.WithCancel(context.Background())

	mgr := &Manager{
		cfg:                     cfg,
		k8sClient:               k8sClient,
		k8sExecutor:             k8sExecutor,
		authValidator:           authValidator,
		healthChecker:           observability.NewHealthChecker(),
		metrics:                 observability.GetMetrics(),
		workloadHandler:         workloadHandler,
		workspaceBindingHandler: workspaceBindingHandler,
		rateLimiter:             rateLimiter,
		ctx:                     mgrCtx,
		cancel:                  mgrCancel,
	}

	mgr.setupReadinessChecks()
	mgr.setupHTTPServer()

	log.Printf("Sandbox Manager started on port %d", cfg.Server.HTTPPort)
	log.Printf("  Health check: http://localhost:%d/healthz", cfg.Server.HTTPPort)
	log.Printf("  Readiness:    http://localhost:%d/readyz", cfg.Server.HTTPPort)
	log.Printf("  Metrics:      http://localhost:%d%s", cfg.Server.HTTPPort, cfg.Server.Metrics.Path)
	log.Printf("  API:          http://localhost:%d/v1/workspaces/...", cfg.Server.HTTPPort)

	mgr.waitForShutdown()

	log.Printf("Sandbox Manager stopped")
}

type BootConfig struct {
	ConfigPath string
}

func loadBootConfig() *BootConfig {
	return &BootConfig{
		ConfigPath: getEnvOrDefault("CONFIG_PATH", "/etc/sandbox-manager/manager-config.yaml"),
	}
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
	v1Mux.Handle("/v1/workspaces/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/workspace-bindings/") {
			m.workspaceBindingHandler.ServeHTTP(w, r)
			return
		}
		m.workloadHandler.ServeHTTP(w, r)
	}))

	var v1Handler http.Handler = v1Mux
	v1Handler = authMiddleware(v1Handler)
	v1Handler = m.rateLimiter.Middleware(v1Handler)

	mux.Handle("/v1/", v1Handler)

	// Apply request-ID and observability middlewares globally so all routes
	// (including /healthz, /readyz, /metrics) are instrumented consistently.
	var rootHandler http.Handler = mux
	rootHandler = m.observabilityMiddleware(rootHandler)
	rootHandler = observability.RequestIDMiddleware(m.cfg.Server.RequestIDHeader)(rootHandler)

	m.httpServer = &http.Server{
		Addr:              fmt.Sprintf(":%d", m.cfg.Server.HTTPPort),
		Handler:           rootHandler,
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
	// rateLimiter is a stateless global limiter; no cleanup needed.

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

func parseMountOptions(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '\n'
	})
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
