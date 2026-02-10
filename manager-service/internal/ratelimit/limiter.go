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

// limiterEntry wraps a rate limiter with its last access time
type limiterEntry struct {
	limiter     *rate.Limiter
	lastAccess  time.Time
}

// Limiter implements three-tier rate limiting (global + per-IP + per-session)
type Limiter struct {
	global     *rate.Limiter
	perIP      sync.Map // map[string]*limiterEntry
	perSession sync.Map // map[string]*limiterEntry
	cfg        *Config

	// cleanup management
	stopCleanup chan struct{}
	wg          sync.WaitGroup
	stopped     sync.Mutex
}

// NewLimiter creates a new rate limiter with the given configuration
func NewLimiter(cfg *Config) *Limiter {
	l := &Limiter{
		global:      rate.NewLimiter(rate.Limit(cfg.GlobalRPS), cfg.GlobalBurst),
		cfg:         cfg,
		stopCleanup: make(chan struct{}),
	}
	if cfg.CleanupInterval > 0 {
		l.startCleanup()
	}
	return l
}

// startCleanup begins the background cleanup goroutine
func (l *Limiter) startCleanup() {
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		if l.cfg.CleanupInterval <= 0 {
			return
		}
		ticker := time.NewTicker(l.cfg.CleanupInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				l.cleanupStaleEntries()
			case <-l.stopCleanup:
				return
			}
		}
	}()
}

// cleanupStaleEntries removes limiters that haven't been accessed recently
func (l *Limiter) cleanupStaleEntries() {
	cutoff := time.Now().Add(-3 * l.cfg.CleanupInterval)

	// Cleanup per-IP limiters
	l.perIP.Range(func(key, value interface{}) bool {
		entry := value.(*limiterEntry)
		if entry.lastAccess.Before(cutoff) {
			l.perIP.Delete(key)
		}
		return true
	})

	// Cleanup per-session limiters
	l.perSession.Range(func(key, value interface{}) bool {
		entry := value.(*limiterEntry)
		if entry.lastAccess.Before(cutoff) {
			l.perSession.Delete(key)
		}
		return true
	})
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
		entry, _ := l.perIP.LoadOrStore(ip, &limiterEntry{
			limiter:    rate.NewLimiter(rate.Limit(l.cfg.PerIPRPS), l.cfg.PerIPBurst),
			lastAccess: time.Now(),
		})

		limiterEntry := entry.(*limiterEntry)
		limiterEntry.lastAccess = time.Now() // Update access time

		if !limiterEntry.limiter.Allow() {
			return false
		}
	}

	// Check per-session limit
	if sessionID != "" && l.cfg.PerSessionRPS > 0 {
		entry, _ := l.perSession.LoadOrStore(sessionID, &limiterEntry{
			limiter:    rate.NewLimiter(rate.Limit(l.cfg.PerSessionRPS), l.cfg.PerSessionBurst),
			lastAccess: time.Now(),
		})

		limiterEntry := entry.(*limiterEntry)
		limiterEntry.lastAccess = time.Now() // Update access time

		if !limiterEntry.limiter.Allow() {
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

// Stop gracefully shuts down the cleanup goroutine
// Can be called multiple times safely
func (l *Limiter) Stop() {
	l.stopped.Lock()
	defer l.stopped.Unlock()

	select {
	case <-l.stopCleanup:
		// Already stopped
		return
	default:
		close(l.stopCleanup)
		l.wg.Wait()
	}
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
