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
	tokens      int
	lastRefill  time.Time
	lastAccess  time.Time
}

func NewPerUserLimiter(maxReq int, window time.Duration) *UserLimiter {
	if maxReq <= 0 {
		panic("maxReq must be greater than 0")
	}
	if window <= 0 {
		panic("window must be greater than 0")
	}

	return &UserLimiter{
		limits: make(map[string]*TokenBucket),
		maxReq: maxReq,
		window: window,
	}
}

// Cleanup removes users that haven't been accessed in the specified duration
func (ul *UserLimiter) Cleanup(inactiveFor time.Duration) int {
	ul.mu.Lock()
	defer ul.mu.Unlock()

	now := time.Now()
	var removed int

	for userID, bucket := range ul.limits {
		if now.Sub(bucket.lastAccess) >= inactiveFor {
			delete(ul.limits, userID)
			removed++
		}
	}

	return removed
}

// GetActiveUserCount returns the number of active users
func (ul *UserLimiter) GetActiveUserCount() int {
	ul.mu.RLock()
	defer ul.mu.RUnlock()

	return len(ul.limits)
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
			lastAccess: now,
		}
		ul.limits[userCtx.UserID] = bucket
		return true
	}

	// Refill tokens based on time elapsed
	elapsed := now.Sub(bucket.lastRefill)
	if elapsed >= ul.window {
		bucket.tokens = ul.maxReq - 1
		bucket.lastRefill = now
		bucket.lastAccess = now
		return true
	}

	// Partial refill
	refillTokens := int(elapsed / (ul.window / time.Duration(ul.maxReq)))
	bucket.tokens += refillTokens
	if bucket.tokens > ul.maxReq {
		bucket.tokens = ul.maxReq
	}
	bucket.lastRefill = now
	bucket.lastAccess = now

	if bucket.tokens > 0 {
		bucket.tokens--
		bucket.lastAccess = now
		return true
	}

	return false
}