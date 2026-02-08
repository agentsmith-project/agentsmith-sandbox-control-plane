package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sandbox/manager/internal/auth"
	"github.com/sandbox/manager/internal/config"
	"github.com/sandbox/manager/internal/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestChaosSessionCreation tests session creation under chaos conditions
func TestChaosSessionCreation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping chaos test in short mode")
	}

	t.Run("ConcurrentSessionCreation", func(t *testing.T) {
		manager := session.NewManager()
		ctx := context.Background()

		const numSessions = 100
		var successful, failed int32

		// Create sessions concurrently
		done := make(chan bool, numSessions)
		for i := 0; i < numSessions; i++ {
			go func(n int) {
				defer func() { done <- true }()
				sess, err := manager.Create(ctx, session.CreateRequest{
					AgentThreadID: fmt.Sprintf("chaos-session-%d", n),
					Image:         "test:latest",
					PodNamespace:  "sandbox",
					OwnerID:       "chaos-user",
					Config: session.SecurityConfig{
						MaxLifetime: 1 * time.Hour,
					},
				})
				if err != nil {
					atomic.AddInt32(&failed, 1)
					return
				}
				if sess != nil {
					atomic.AddInt32(&successful, 1)
				}
			}(i)
		}

		// Wait for all goroutines
		for i := 0; i < numSessions; i++ {
			<-done
		}

		// All sessions should be created successfully
		assert.Equal(t, int32(numSessions), successful)
		assert.Equal(t, int32(0), failed)

		// Verify session count
		assert.Equal(t, numSessions, manager.GetSessionCount())
	})

	t.Run("SessionCreationWithTimeout", func(t *testing.T) {
		manager := session.NewManager()

		// Create context with very short timeout
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
		defer cancel()

		// Give time for context to timeout
		time.Sleep(10 * time.Millisecond)

		_, err := manager.Create(ctx, session.CreateRequest{
			AgentThreadID: "timeout-session",
			Image:         "test:latest",
			PodNamespace:  "sandbox",
			Config:        session.SecurityConfig{MaxLifetime: 1 * time.Hour},
		})

		// Should fail with context error
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "context deadline")
	})
}

// TestChaosStateTransitions tests state machine under chaos
func TestChaosStateTransitions(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping chaos test in short mode")
	}

	t.Run("ConcurrentStateTransitions", func(t *testing.T) {
		sm := session.NewStateMachine(session.StateCreating)

		const numTransitions = 50
		errors := make(chan error, numTransitions)

		// Attempt concurrent transitions
		for i := 0; i < numTransitions; i++ {
			go func(n int) {
				targetState := session.StateReady
				if n%2 == 0 {
					targetState = session.StateFailed
				}
				errors <- sm.Transition(targetState)
			}(i)
		}

		// Collect results
		var successCount, failCount int
		for i := 0; i < numTransitions; i++ {
			if <-errors == nil {
				successCount++
			} else {
				failCount++
			}
		}

		// Exactly one transition should succeed
		assert.Equal(t, 1, successCount)
		assert.Equal(t, numTransitions-1, failCount)

		// Final state should be deterministic
		finalState := sm.CurrentState()
		assert.True(t, finalState == session.StateReady || finalState == session.StateFailed)
	})

	t.Run("InvalidStateTransitions", func(t *testing.T) {
		tests := []struct {
			from     session.State
			to       session.State
			shouldFail bool
		}{
			{session.StateReady, session.StateCreating, true},
			{session.StateTerminated, session.StateReady, true},
			{session.StateCreating, session.StateReady, false},
			{session.StateReady, session.StateTerminating, false},
		}

		for _, tt := range tests {
			t.Run(fmt.Sprintf("%s->%s", tt.from, tt.to), func(t *testing.T) {
				sm := session.NewStateMachine(tt.from)
				err := sm.Transition(tt.to)

				if tt.shouldFail {
					assert.Error(t, err)
				} else {
					assert.NoError(t, err)
				}
			})
		}
	})
}

// TestChaosAuthentication tests auth under chaos conditions
func TestChaosAuthentication(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping chaos test in short mode")
	}

	t.Run("ConcurrentTokenValidation", func(t *testing.T) {
		authenticator := auth.NewTokenAuthenticator("test-secret")

		// Generate valid token
		token, err := authenticator.GenerateToken("user123", "session456", 1*time.Hour)
		require.NoError(t, err)

		// Validate token concurrently
		const numValidations = 100
		results := make(chan error, numValidations)

		for i := 0; i < numValidations; i++ {
			go func() {
				_, err := authenticator.ValidateToken(token)
				results <- err
			}()
		}

		// All validations should succeed
		for i := 0; i < numValidations; i++ {
			assert.NoError(t, <-results)
		}
	})

	t.Run("TokenGenerationStress", func(t *testing.T) {
		authenticator := auth.NewTokenAuthenticator("test-secret")

		const numTokens = 1000
		var successCount int32

		done := make(chan bool, numTokens)
		for i := 0; i < numTokens; i++ {
			go func(n int) {
				defer func() { done <- true }()
				token, err := authenticator.GenerateToken(
					fmt.Sprintf("user%d", n),
					fmt.Sprintf("session%d", n),
					1*time.Hour,
				)
				if err == nil && token != "" {
					atomic.AddInt32(&successCount, 1)
				}
			}(i)
		}

		// Wait for all goroutines
		for i := 0; i < numTokens; i++ {
			<-done
		}

		// All tokens should be generated
		assert.Equal(t, int32(numTokens), successCount)
	})
}

// TestChaosRateLimiting tests rate limiting under stress
func TestChaosRateLimiting(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping chaos test in short mode")
	}

	t.Run("BurstRateLimiting", func(t *testing.T) {
		// Test rate limiter handles burst traffic
		limiter := auth.NewUserLimiter(10, 1*time.Minute)

		ctx := context.Background()
		userID := "burst-user"

		var allowed, denied int32

		// Send 100 requests rapidly
		for i := 0; i < 100; i++ {
			go func() {
				if limiter.Allow(ctx, userID) {
					atomic.AddInt32(&allowed, 1)
				} else {
					atomic.AddInt32(&denied, 1)
				}
			}()
		}

		// Wait for all requests
		time.Sleep(500 * time.Millisecond)

		// First 10 should be allowed, rest denied
		assert.Equal(t, int32(10), allowed)
		assert.Greater(t, denied, int32(0))
	})
}

// TestChaosConfigReload tests configuration reload under stress
func TestChaosConfigReload(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping chaos test in short mode")
	}

	t.Run("ConcurrentConfigReads", func(t *testing.T) {
		cfg := &config.Config{
			WebSocket: config.WebSocketConfig{
				ReadBufferSize:  4096,
				WriteBufferSize: 4096,
			},
		}

		const numReads = 1000
		errors := make(chan error, numReads)

		// Concurrent config reads
		for i := 0; i < numReads; i++ {
			go func() {
				// Simulate config access
				_ = cfg.DeepCopy()
				errors <- nil
			}()
		}

		// All reads should succeed
		for i := 0; i < numReads; i++ {
			assert.NoError(t, <-errors)
		}
	})
}

// TestChaosHTTPConnections tests HTTP handling under chaos
func TestChaosHTTPConnections(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping chaos test in short mode")
	}

	t.Run("ConnectionStress", func(t *testing.T) {
		requestCount := 0
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestCount++
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(fmt.Sprintf(`{"status":"ok","count":%d}`, requestCount)))
		})

		server := httptest.NewServer(handler)
		defer server.Close()

		const numRequests = 500
		var successCount int32

		done := make(chan bool, numRequests)
		for i := 0; i < numRequests; i++ {
			go func() {
				defer func() { done <- true }()
				resp, err := http.Get(server.URL + "/test")
				if err != nil {
					return
				}
				defer resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					atomic.AddInt32(&successCount, 1)
				}
			}()
		}

		// Wait for all requests
		for i := 0; i < numRequests; i++ {
			<-done
		}

		// Most requests should succeed
		successRate := float64(successCount) / float64(numRequests)
		assert.Greater(t, successRate, 0.95, "Success rate should be >95%")
	})

	t.Run("SlowLorisAttack", func(t *testing.T) {
		// Test slow request handling
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(100 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		})

		server := httptest.NewServer(handler)
		defer server.Close()

		// Start many slow connections
		const numConnections = 20
		for i := 0; i < numConnections; i++ {
			go func() {
				client := &http.Client{Timeout: 5 * time.Second}
				resp, err := client.Get(server.URL + "/slow")
				if err == nil {
					resp.Body.Close()
				}
			}()
		}

		// Server should still respond
		time.Sleep(200 * time.Millisecond)
		resp, err := http.Get(server.URL + "/test")
		assert.NoError(t, err)
		resp.Body.Close()
	})
}
