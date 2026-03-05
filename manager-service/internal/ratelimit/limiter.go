package ratelimit

import (
	"net/http"

	"golang.org/x/time/rate"
)

// Config contains rate limiter configuration.
// Sandbox Manager is a server-to-server API called only by AgentSmith,
// so only a global rate limit is needed as a safety net.
type Config struct {
	GlobalRPS   float64 `yaml:"globalRPS"`
	GlobalBurst int     `yaml:"globalBurst"`
}

// Limiter implements global rate limiting for the sandbox manager API.
type Limiter struct {
	global *rate.Limiter
}

// NewLimiter creates a new rate limiter with the given configuration.
func NewLimiter(cfg *Config) *Limiter {
	return &Limiter{
		global: rate.NewLimiter(rate.Limit(cfg.GlobalRPS), cfg.GlobalBurst),
	}
}

// Allow checks if a request should be allowed based on the global rate limit.
func (l *Limiter) Allow() bool {
	return l.global.Allow()
}

// Middleware returns an HTTP middleware that enforces the global rate limit.
func (l *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.Allow() {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// DefaultConfig returns the default rate limiter configuration.
func DefaultConfig() *Config {
	return &Config{
		GlobalRPS:   100,
		GlobalBurst: 200,
	}
}

// ConfigFromRequestsPerMinute builds a Config with global rate derived from
// requests per minute. If rpm <= 0, returns DefaultConfig().
func ConfigFromRequestsPerMinute(rpm int) *Config {
	if rpm <= 0 {
		return DefaultConfig()
	}
	rps := float64(rpm) / 60.0
	burst := rpm / 2
	if burst < 1 {
		burst = 1
	}
	return &Config{
		GlobalRPS:   rps,
		GlobalBurst: burst,
	}
}
