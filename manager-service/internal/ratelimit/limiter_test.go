package ratelimit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLimiter_Allow_Global(t *testing.T) {
	cfg := &Config{
		GlobalRPS:   1,
		GlobalBurst: 1,
	}

	limiter := NewLimiter(cfg)

	ctx := context.Background()

	// First request should be allowed
	if !limiter.Allow(ctx, "192.168.1.1", "session1") {
		t.Error("First request should be allowed")
	}

	// Second request should be denied (global limit exceeded)
	if limiter.Allow(ctx, "192.168.1.2", "session2") {
		t.Error("Second request should be denied (global limit)")
	}
}

func TestLimiter_Allow_PerIP(t *testing.T) {
	cfg := &Config{
		GlobalRPS:   100,
		GlobalBurst: 100,
		PerIPRPS:    1,
		PerIPBurst:  1,
	}

	limiter := NewLimiter(cfg)

	ctx := context.Background()

	// First request from IP1 should be allowed
	if !limiter.Allow(ctx, "192.168.1.1", "session1") {
		t.Error("First request from IP1 should be allowed")
	}

	// Second request from IP1 should be denied
	if limiter.Allow(ctx, "192.168.1.1", "session2") {
		t.Error("Second request from IP1 should be denied (per-IP limit)")
	}

	// Request from IP2 should still be allowed
	if !limiter.Allow(ctx, "192.168.1.2", "session3") {
		t.Error("Request from IP2 should be allowed")
	}
}

func TestLimiter_Allow_PerSession(t *testing.T) {
	cfg := &Config{
		GlobalRPS:     100,
		GlobalBurst:   100,
		PerSessionRPS: 1,
		PerSessionBurst: 1,
	}

	limiter := NewLimiter(cfg)

	ctx := context.Background()

	// First request from session1 should be allowed
	if !limiter.Allow(ctx, "192.168.1.1", "session1") {
		t.Error("First request from session1 should be allowed")
	}

	// Second request from session1 should be denied
	if limiter.Allow(ctx, "192.168.1.1", "session1") {
		t.Error("Second request from session1 should be denied (per-session limit)")
	}

	// Request from session2 should still be allowed
	if !limiter.Allow(ctx, "192.168.1.1", "session2") {
		t.Error("Request from session2 should be allowed")
	}
}

func TestLimiter_Allow_NoIP(t *testing.T) {
	cfg := &Config{
		GlobalRPS:   100,
		GlobalBurst: 100,
		PerIPRPS:    10,
		PerIPBurst:  10,
	}

	limiter := NewLimiter(cfg)

	ctx := context.Background()

	// Request without IP should only be limited by global
	for i := 0; i < 10; i++ {
		if !limiter.Allow(ctx, "", "session1") {
			t.Errorf("Request %d without IP should be allowed", i)
		}
	}
}

func TestLimiter_Allow_NoSession(t *testing.T) {
	cfg := &Config{
		GlobalRPS:       100,
		GlobalBurst:     100,
		PerSessionRPS:   10,
		PerSessionBurst: 10,
	}

	limiter := NewLimiter(cfg)

	ctx := context.Background()

	// Request without session should only be limited by global + per-IP
	for i := 0; i < 10; i++ {
		if !limiter.Allow(ctx, "192.168.1.1", "") {
			t.Errorf("Request %d without session should be allowed", i)
		}
	}
}

func TestLimiter_Middleware(t *testing.T) {
	cfg := &Config{
		GlobalRPS:   1,
		GlobalBurst: 1,
	}

	limiter := NewLimiter(cfg)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	middleware := limiter.Middleware(handler)

	server := httptest.NewServer(middleware)
	defer server.Close()

	// First request should succeed
	resp1, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("First request should succeed, got status %d", resp1.StatusCode)
	}
	resp1.Body.Close()

	// Second request should be rate limited
	resp2, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if resp2.StatusCode != http.StatusTooManyRequests {
		t.Errorf("Second request should be rate limited, got status %d", resp2.StatusCode)
	}
	resp2.Body.Close()
}

func TestLimiter_Refill(t *testing.T) {
	cfg := &Config{
		GlobalRPS:   1,
		GlobalBurst: 1,
	}

	limiter := NewLimiter(cfg)

	ctx := context.Background()

	// First request should be allowed
	if !limiter.Allow(ctx, "192.168.1.1", "session1") {
		t.Error("First request should be allowed")
	}

	// Second request should be denied
	if limiter.Allow(ctx, "192.168.1.1", "session2") {
		t.Error("Second request should be denied")
	}

	// Wait for token refill
	time.Sleep(1100 * time.Millisecond)

	// Third request should be allowed again
	if !limiter.Allow(ctx, "192.168.1.1", "session3") {
		t.Error("Third request should be allowed after refill")
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.GlobalRPS != 100 {
		t.Errorf("Expected GlobalRPS 100, got %f", cfg.GlobalRPS)
	}
	if cfg.GlobalBurst != 200 {
		t.Errorf("Expected GlobalBurst 200, got %d", cfg.GlobalBurst)
	}
	if cfg.PerIPRPS != 10 {
		t.Errorf("Expected PerIPRPS 10, got %f", cfg.PerIPRPS)
	}
	if cfg.PerIPBurst != 20 {
		t.Errorf("Expected PerIPBurst 20, got %d", cfg.PerIPBurst)
	}
	if cfg.PerSessionRPS != 5 {
		t.Errorf("Expected PerSessionRPS 5, got %f", cfg.PerSessionRPS)
	}
	if cfg.PerSessionBurst != 10 {
		t.Errorf("Expected PerSessionBurst 10, got %d", cfg.PerSessionBurst)
	}
	if cfg.CleanupInterval != 5*time.Minute {
		t.Errorf("Expected CleanupInterval 5m, got %v", cfg.CleanupInterval)
	}
}

func TestLimiterCleanup(t *testing.T) {
	cfg := &Config{
		GlobalRPS:       100,
		GlobalBurst:     200,
		PerIPRPS:        10,
		PerIPBurst:      20,
		PerSessionRPS:   5,
		PerSessionBurst: 10,
		CleanupInterval: 100 * time.Millisecond,
	}

	limiter := NewLimiter(cfg)
	defer limiter.Stop()

	limiter.Allow(context.Background(), "ip1", "session1")
	limiter.Allow(context.Background(), "ip2", "session2")

	time.Sleep(500 * time.Millisecond)

	limiter.Stop()
	limiter.Stop() // Verify can be called multiple times
}
