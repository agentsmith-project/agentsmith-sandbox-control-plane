package ratelimit

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
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

func TestLimiter_StartCleanup_And_Stop(t *testing.T) {
	cfg := &Config{
		GlobalRPS:       100,
		GlobalBurst:     100,
		PerIPRPS:        1,
		PerIPBurst:      1,
		PerSessionRPS:   1,
		PerSessionBurst: 1,
		CleanupInterval: 50 * time.Millisecond,
	}
	limiter := NewLimiter(cfg)
	ctx := context.Background()

	// Exhaust per-IP limit
	if !limiter.Allow(ctx, "10.0.0.1", "sess1") {
		t.Fatal("First request should be allowed")
	}
	if limiter.Allow(ctx, "10.0.0.1", "sess2") {
		t.Fatal("Second request from same IP should be denied")
	}

	limiter.StartCleanup()

	// StartCleanup is idempotent — calling twice should not panic or start a second goroutine
	limiter.StartCleanup()

	// Wait for cleanup to evict stale limiters
	time.Sleep(120 * time.Millisecond)

	// After cleanup, per-IP limiter is gone; a fresh one is created
	if !limiter.Allow(ctx, "10.0.0.1", "sess3") {
		t.Error("Request should succeed after cleanup evicted the per-IP limiter")
	}

	limiter.Stop()

	// Stop is idempotent — calling twice should not panic
	limiter.Stop()
}

func TestLimiter_Allow_EmptyIPAndSession(t *testing.T) {
	cfg := &Config{
		GlobalRPS:       2,
		GlobalBurst:     2,
		PerIPRPS:        1,
		PerIPBurst:      1,
		PerSessionRPS:   1,
		PerSessionBurst: 1,
	}
	limiter := NewLimiter(cfg)
	ctx := context.Background()

	// Both IP and session empty — only global limit applies
	if !limiter.Allow(ctx, "", "") {
		t.Error("First request should be allowed (global burst=2)")
	}
	if !limiter.Allow(ctx, "", "") {
		t.Error("Second request should be allowed (global burst=2)")
	}
	if limiter.Allow(ctx, "", "") {
		t.Error("Third request should be denied (global limit exhausted)")
	}
}

func TestLimiter_Allow_AfterCleanupEvictsLimiters(t *testing.T) {
	cfg := &Config{
		GlobalRPS:       100,
		GlobalBurst:     100,
		PerIPRPS:        1,
		PerIPBurst:      1,
		PerSessionRPS:   1,
		PerSessionBurst: 1,
		CleanupInterval: 50 * time.Millisecond,
	}
	limiter := NewLimiter(cfg)
	ctx := context.Background()

	// Exhaust both per-IP and per-session limits
	if !limiter.Allow(ctx, "10.0.0.1", "sess-A") {
		t.Fatal("First request should be allowed")
	}
	if limiter.Allow(ctx, "10.0.0.1", "sess-A") {
		t.Fatal("Second request should be denied")
	}

	limiter.StartCleanup()
	time.Sleep(120 * time.Millisecond)

	// Fresh limiters after eviction
	if !limiter.Allow(ctx, "10.0.0.1", "sess-A") {
		t.Error("Request should be allowed after cleanup evicted limiters")
	}

	limiter.Stop()
}

func TestLimiter_Middleware_Integration_PerIP(t *testing.T) {
	cfg := &Config{
		GlobalRPS:   100,
		GlobalBurst: 100,
		PerIPRPS:    1,
		PerIPBurst:  1,
	}
	limiter := NewLimiter(cfg)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	middleware := limiter.Middleware(handler)

	// First request from IP A — allowed
	req1 := httptest.NewRequest("GET", "/test?id=session1", nil)
	req1.RemoteAddr = "192.168.1.1:1234"
	rec1 := httptest.NewRecorder()
	middleware.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Errorf("First request expected 200, got %d", rec1.Code)
	}

	// Second request from same IP — rate limited
	req2 := httptest.NewRequest("GET", "/test?id=session2", nil)
	req2.RemoteAddr = "192.168.1.1:1234"
	rec2 := httptest.NewRecorder()
	middleware.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("Second request from same IP expected 429, got %d", rec2.Code)
	}

	// Request from different IP — allowed
	req3 := httptest.NewRequest("GET", "/test?id=session3", nil)
	req3.RemoteAddr = "192.168.1.2:5678"
	rec3 := httptest.NewRecorder()
	middleware.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Errorf("Request from different IP expected 200, got %d", rec3.Code)
	}
}

func TestLimiter_Concurrent_Allow(t *testing.T) {
	cfg := &Config{
		GlobalRPS:       1000,
		GlobalBurst:     1000,
		PerIPRPS:        100,
		PerIPBurst:      100,
		PerSessionRPS:   100,
		PerSessionBurst: 100,
	}
	limiter := NewLimiter(cfg)
	ctx := context.Background()

	const goroutines = 50
	const requestsPerGoroutine = 10

	var wg sync.WaitGroup
	wg.Add(goroutines)

	allowed := make([]int, goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			ip := fmt.Sprintf("10.0.0.%d", idx%10)
			session := fmt.Sprintf("session-%d", idx%5)
			for j := 0; j < requestsPerGoroutine; j++ {
				if limiter.Allow(ctx, ip, session) {
					allowed[idx]++
				}
			}
		}(i)
	}

	wg.Wait()

	totalAllowed := 0
	for _, count := range allowed {
		totalAllowed += count
	}

	totalRequests := goroutines * requestsPerGoroutine
	if totalAllowed == 0 {
		t.Error("Expected some requests to be allowed in concurrent scenario")
	}
	if totalAllowed > totalRequests {
		t.Errorf("Allowed %d exceeds total %d requests", totalAllowed, totalRequests)
	}
}

func TestConfigFromRequestsPerMinute(t *testing.T) {
	cfg := ConfigFromRequestsPerMinute(0)
	if cfg == nil {
		t.Fatal("ConfigFromRequestsPerMinute(0) returned nil")
	}
	if cfg.GlobalRPS != 100 {
		t.Errorf("rpm 0: want GlobalRPS 100, got %v", cfg.GlobalRPS)
	}

	cfg = ConfigFromRequestsPerMinute(60)
	if cfg.GlobalRPS != 1.0 || cfg.GlobalBurst != 30 {
		t.Errorf("rpm 60: want RPS=1 burst=30, got RPS=%v burst=%v", cfg.GlobalRPS, cfg.GlobalBurst)
	}
}
