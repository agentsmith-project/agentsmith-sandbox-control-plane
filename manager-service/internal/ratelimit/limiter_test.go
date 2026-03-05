package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestLimiter_Allow_Global(t *testing.T) {
	limiter := NewLimiter(&Config{GlobalRPS: 1, GlobalBurst: 1})

	if !limiter.Allow() {
		t.Error("First request should be allowed")
	}
	if limiter.Allow() {
		t.Error("Second request should be denied (global limit exceeded)")
	}
}

func TestLimiter_Allow_Refill(t *testing.T) {
	limiter := NewLimiter(&Config{GlobalRPS: 1, GlobalBurst: 1})

	if !limiter.Allow() {
		t.Error("First request should be allowed")
	}
	if limiter.Allow() {
		t.Error("Second request should be denied")
	}

	time.Sleep(1100 * time.Millisecond)

	if !limiter.Allow() {
		t.Error("Request should be allowed after token refill")
	}
}

func TestLimiter_Allow_BurstCapacity(t *testing.T) {
	limiter := NewLimiter(&Config{GlobalRPS: 10, GlobalBurst: 5})

	allowed := 0
	for i := 0; i < 10; i++ {
		if limiter.Allow() {
			allowed++
		}
	}
	if allowed != 5 {
		t.Errorf("Expected 5 allowed (burst), got %d", allowed)
	}
}

func TestLimiter_Middleware(t *testing.T) {
	limiter := NewLimiter(&Config{GlobalRPS: 1, GlobalBurst: 1})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	middleware := limiter.Middleware(handler)

	server := httptest.NewServer(middleware)
	defer server.Close()

	resp1, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("First request expected 200, got %d", resp1.StatusCode)
	}

	resp2, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusTooManyRequests {
		t.Errorf("Second request expected 429, got %d", resp2.StatusCode)
	}
}

func TestLimiter_Middleware_Unit(t *testing.T) {
	limiter := NewLimiter(&Config{GlobalRPS: 1, GlobalBurst: 1})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	middleware := limiter.Middleware(handler)

	req1 := httptest.NewRequest("GET", "/test", nil)
	rec1 := httptest.NewRecorder()
	middleware.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Errorf("First request expected 200, got %d", rec1.Code)
	}

	req2 := httptest.NewRequest("GET", "/test", nil)
	rec2 := httptest.NewRecorder()
	middleware.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("Second request expected 429, got %d", rec2.Code)
	}
}

func TestLimiter_Concurrent_Allow(t *testing.T) {
	limiter := NewLimiter(&Config{GlobalRPS: 1000, GlobalBurst: 1000})

	const goroutines = 50
	const requestsPerGoroutine = 10

	var wg sync.WaitGroup
	wg.Add(goroutines)

	allowed := make([]int, goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < requestsPerGoroutine; j++ {
				if limiter.Allow() {
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

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.GlobalRPS != 100 {
		t.Errorf("Expected GlobalRPS 100, got %f", cfg.GlobalRPS)
	}
	if cfg.GlobalBurst != 200 {
		t.Errorf("Expected GlobalBurst 200, got %d", cfg.GlobalBurst)
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

	cfg = ConfigFromRequestsPerMinute(1)
	if cfg.GlobalBurst < 1 {
		t.Errorf("rpm 1: burst should be >= 1, got %d", cfg.GlobalBurst)
	}
}
