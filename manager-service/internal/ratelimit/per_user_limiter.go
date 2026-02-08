package ratelimit

import (
	"sync"
	"time"

	"github.com/sandbox/manager/internal/auth"
)

type UserLimiter struct {
	mu     sync.RWMutex
	limits map[string]*TokenBucket
	maxReq int
	window time.Duration
}

type TokenBucket struct {
	tokens     int
	lastRefill time.Time
}

func NewPerUserLimiter(maxReq int, window time.Duration) *UserLimiter {
	return &UserLimiter{
		limits: make(map[string]*TokenBucket),
		maxReq: maxReq,
		window: window,
	}
}

func (ul *UserLimiter) Allow(userCtx *auth.UserContext) bool {
	ul.mu.Lock()
	defer ul.mu.Unlock()

	now := time.Now()
	bucket, exists := ul.limits[userCtx.UserID]

	if !exists {
		bucket = &TokenBucket{
			tokens:     ul.maxReq - 1,
			lastRefill: now,
		}
		ul.limits[userCtx.UserID] = bucket
		return true
	}

	// Refill tokens based on time elapsed
	elapsed := now.Sub(bucket.lastRefill)
	if elapsed >= ul.window {
		bucket.tokens = ul.maxReq - 1
		bucket.lastRefill = now
		return true
	}

	// Partial refill
	refillTokens := int(elapsed / (ul.window / time.Duration(ul.maxReq)))
	bucket.tokens += refillTokens
	if bucket.tokens > ul.maxReq {
		bucket.tokens = ul.maxReq
	}
	bucket.lastRefill = now

	if bucket.tokens > 0 {
		bucket.tokens--
		return true
	}

	return false
}