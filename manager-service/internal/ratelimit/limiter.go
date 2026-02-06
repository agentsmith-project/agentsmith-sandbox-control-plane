package ratelimit

import (
	"context"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Config contains rate limiter configuration
type Config struct {
	// Global rate limiting
	GlobalRPS   float64 `yaml:"globalRPS"`
	GlobalBurst int     `yaml:"globalBurst"`

	// Per-IP rate limiting
	PerIPRPS   float64 `yaml:"perIPRPS"`
	PerIPBurst int     `yaml:"perIPBurst"`

	// Per-Session rate limiting
	PerSessionRPS   float64 `yaml:"perSessionRPS"`
	PerSessionBurst int     `yaml:"perSessionBurst"`

	// Cleanup interval for stale limiters
	CleanupInterval time.Duration `yaml:"cleanupInterval"`
}

// Limiter implements three-tier rate limiting (global + per-IP + per-session)
type Limiter struct {
	global     *rate.Limiter
	perIP      sync.Map // map[string]*rate.Limiter
	perSession sync.Map // map[string]*rate.Limiter
	cfg        *Config

	// cleanup management
	stopCleanup chan struct{}
}

// NewLimiter creates a new rate limiter with the given configuration
func NewLimiter(cfg *Config) *Limiter {
	return &Limiter{
		global:     rate.NewLimiter(rate.Limit(cfg.GlobalRPS), cfg.GlobalBurst),
		cfg:        cfg,
		stopCleanup: make(chan struct{}),
	}
}

// Allow checks if a request should be allowed based on the rate limits
// Returns true if the request is allowed, false if it should be rate limited
func (l *Limiter) Allow(ctx context.Context, ip, sessionID string) bool {
	// Check global limit first (cheap)
	if !l.global.Allow() {
		return false
	}

	// Check per-IP limit
	if ip != "" && l.cfg.PerIPRPS > 0 {
		limiter, _ := l.perIP.LoadOrStore(ip,
			rate.NewLimiter(rate.Limit(l.cfg.PerIPRPS), l.cfg.PerIPBurst))

		if !limiter.(*rate.Limiter).Allow() {
			return false
		}
	}

	// Check per-session limit
	if sessionID != "" && l.cfg.PerSessionRPS > 0 {
		limiter, _ := l.perSession.LoadOrStore(sessionID,
			rate.NewLimiter(rate.Limit(l.cfg.PerSessionRPS), l.cfg.PerSessionBurst))

		if !limiter.(*rate.Limiter).Allow() {
			return false
		}
	}

	return true
}

// Middleware returns an HTTP middleware that enforces rate limiting
func (l *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := r.RemoteAddr
		sessionID := r.URL.Query().Get("id")

		if !l.Allow(r.Context(), ip, sessionID) {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// DefaultConfig returns the default rate limiter configuration
func DefaultConfig() *Config {
	return &Config{
		GlobalRPS:       100,
		GlobalBurst:     200,
		PerIPRPS:        10,
		PerIPBurst:      20,
		PerSessionRPS:   5,
		PerSessionBurst: 10,
		CleanupInterval: 5 * time.Minute,
	}
}
