package ratelimit

import (
	"net/http"

	"github.com/sandbox/manager/internal/auth"
)

func PerUserRateLimitMiddleware(limiter *UserLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userCtx, ok := auth.GetUserContext(r)
			if !ok {
				// No user context, skip per-user limiting
				next.ServeHTTP(w, r)
				return
			}

			if !limiter.Allow(userCtx) {
				http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}