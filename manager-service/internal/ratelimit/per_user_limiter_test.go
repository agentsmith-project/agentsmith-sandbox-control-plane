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