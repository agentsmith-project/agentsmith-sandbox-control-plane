package ratelimit_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/sandbox/manager/internal/auth"
	"github.com/sandbox/manager/internal/ratelimit"
)

func TestPerUserLimiter_Allow(t *testing.T) {
	limiter := ratelimit.NewPerUserLimiter(10, time.Minute)

	userCtx := &auth.UserContext{UserID: "user-123"}

	// First 10 requests should be allowed
	for i := 0; i < 10; i++ {
		allowed := limiter.Allow(userCtx)
		assert.True(t, allowed)
	}

	// 11th request should be denied
	allowed := limiter.Allow(userCtx)
	assert.False(t, allowed)
}

func TestPerUserLimiter_DifferentUsers(t *testing.T) {
	limiter := ratelimit.NewPerUserLimiter(5, time.Minute)

	user1 := &auth.UserContext{UserID: "user-1"}
	user2 := &auth.UserContext{UserID: "user-2"}

	// Each user should have independent limits
	for i := 0; i < 5; i++ {
		assert.True(t, limiter.Allow(user1))
		assert.True(t, limiter.Allow(user2))
	}

	// Both should be rate limited now
	assert.False(t, limiter.Allow(user1))
	assert.False(t, limiter.Allow(user2))
}

func TestPerUserLimiter_InvalidConstructor(t *testing.T) {
	assert.Panics(t, func() {
		ratelimit.NewPerUserLimiter(0, time.Minute)
	})

	assert.Panics(t, func() {
		ratelimit.NewPerUserLimiter(10, 0)
	})

	assert.Panics(t, func() {
		ratelimit.NewPerUserLimiter(-1, time.Minute)
	})

	assert.Panics(t, func() {
		ratelimit.NewPerUserLimiter(10, -time.Minute)
	})
}

func TestPerUserLimiter_RefillBehavior(t *testing.T) {
	// Test that the rate limiter refills tokens after the window period
	limiter := ratelimit.NewPerUserLimiter(1, 50*time.Millisecond)
	userCtx := &auth.UserContext{UserID: "user-123"}

	// First request should be allowed
	assert.True(t, limiter.Allow(userCtx))

	// Second request should be denied immediately
	assert.False(t, limiter.Allow(userCtx))

	// Wait for window to pass
	time.Sleep(60*time.Millisecond)

	// Should be allowed again after window
	assert.True(t, limiter.Allow(userCtx))
}

func TestPerUserLimiter_Cleanup(t *testing.T) {
	limiter := ratelimit.NewPerUserLimiter(10, time.Minute)
	user1 := &auth.UserContext{UserID: "user-1"}
	user2 := &auth.UserContext{UserID: "user-2"}

	// Add user1
	assert.True(t, limiter.Allow(user1))
	assert.Equal(t, 1, limiter.GetActiveUserCount())

	// Add user2
	assert.True(t, limiter.Allow(user2))
	assert.Equal(t, 2, limiter.GetActiveUserCount())

	// Wait a bit to ensure timestamps are different
	time.Sleep(20 * time.Millisecond)

	// Access user1 again - this updates lastAccess
	assert.True(t, limiter.Allow(user1))

	// Now user1 should have recent access, user2 should not
	time.Sleep(10 * time.Millisecond)

	// Cleanup with short duration - should only remove user2
	removed := limiter.Cleanup(20 * time.Millisecond)
	assert.Equal(t, 1, removed) // user2 should be removed

	// Now only 1 user should remain
	assert.Equal(t, 1, limiter.GetActiveUserCount())
}

func TestPerUserLimiter_Cleanup_Expired(t *testing.T) {
	limiter := ratelimit.NewPerUserLimiter(10, time.Minute)
	user := &auth.UserContext{UserID: "user-123"}

	// Add a user
	assert.True(t, limiter.Allow(user))
	assert.Equal(t, 1, limiter.GetActiveUserCount())

	// Cleanup with a very short inactive time
	time.Sleep(10 * time.Millisecond)
	removed := limiter.Cleanup(5 * time.Millisecond)
	assert.Equal(t, 1, removed) // User should be removed
	assert.Equal(t, 0, limiter.GetActiveUserCount())
}