package retry

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fastRetryConfig(maxAttempts int) RetryConfig {
	return RetryConfig{
		MaxAttempts:    maxAttempts,
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff:     5 * time.Millisecond,
		BackoffFactor:  2.0,
	}
}

func TestDefaultRetryConfig(t *testing.T) {
	cfg := DefaultRetryConfig()

	assert.Equal(t, 3, cfg.MaxAttempts)
	assert.Equal(t, 1*time.Second, cfg.InitialBackoff)
	assert.Equal(t, 10*time.Second, cfg.MaxBackoff)
	assert.Equal(t, 2.0, cfg.BackoffFactor)
}

func TestRetry_SuccessOnFirstAttempt(t *testing.T) {
	var calls int
	err := Retry(context.Background(), fastRetryConfig(3), func() error {
		calls++
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 1, calls)
}

func TestRetry_SuccessOnSecondAttempt(t *testing.T) {
	var calls int
	err := Retry(context.Background(), fastRetryConfig(3), func() error {
		calls++
		if calls < 2 {
			return fmt.Errorf("transient error")
		}
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 2, calls)
}

func TestRetry_SuccessOnThirdAttempt(t *testing.T) {
	var calls int
	err := Retry(context.Background(), fastRetryConfig(3), func() error {
		calls++
		if calls < 3 {
			return fmt.Errorf("transient error %d", calls)
		}
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 3, calls)
}

func TestRetry_ExhaustsAllAttempts(t *testing.T) {
	sentinel := fmt.Errorf("persistent failure")
	var calls int

	err := Retry(context.Background(), fastRetryConfig(3), func() error {
		calls++
		return sentinel
	})

	require.Error(t, err)
	assert.Equal(t, 3, calls)
	assert.ErrorIs(t, err, sentinel)
	assert.Contains(t, err.Error(), "after 3 attempts")
}

func TestRetry_ContextCancelledDuringBackoff(t *testing.T) {
	cfg := RetryConfig{
		MaxAttempts:    5,
		InitialBackoff: 500 * time.Millisecond,
		MaxBackoff:     1 * time.Second,
		BackoffFactor:  2.0,
	}

	ctx, cancel := context.WithCancel(context.Background())
	var calls int

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := Retry(ctx, cfg, func() error {
		calls++
		return fmt.Errorf("fail")
	})

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, calls, "should have executed once before cancellation during backoff wait")
}

func TestRetry_ContextAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cfg := RetryConfig{
		MaxAttempts:    3,
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff:     5 * time.Millisecond,
		BackoffFactor:  2.0,
	}

	var calls int
	err := Retry(ctx, cfg, func() error {
		calls++
		if calls == 1 {
			return fmt.Errorf("fail")
		}
		return nil
	})

	// First attempt runs (attempt=0, no backoff wait), fails.
	// Second attempt tries backoff wait, ctx already cancelled -> returns ctx.Err().
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, calls)
}

func TestRetry_FunctionReturnsContextCanceled(t *testing.T) {
	var calls int
	err := Retry(context.Background(), fastRetryConfig(3), func() error {
		calls++
		return context.Canceled
	})

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, calls, "should not retry on context.Canceled from fn")
}

func TestRetry_FunctionReturnsDeadlineExceeded(t *testing.T) {
	var calls int
	err := Retry(context.Background(), fastRetryConfig(3), func() error {
		calls++
		return context.DeadlineExceeded
	})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, 1, calls, "should not retry on context.DeadlineExceeded from fn")
}

func TestRetry_BackoffCappedAtMaxBackoff(t *testing.T) {
	cfg := RetryConfig{
		MaxAttempts:    5,
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff:     3 * time.Millisecond,
		BackoffFactor:  10.0,
	}

	start := time.Now()
	var calls int
	err := Retry(context.Background(), cfg, func() error {
		calls++
		return fmt.Errorf("fail")
	})
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Equal(t, 5, calls)

	// Without cap: backoffs would be 1ms, 10ms, 100ms, 1000ms = ~1111ms total.
	// With cap at 3ms: backoffs are 1ms, 3ms, 3ms, 3ms = ~10ms total (plus jitter).
	// Allow generous upper bound but well below uncapped.
	assert.Less(t, elapsed, 200*time.Millisecond, "backoff should be capped, total time should be small")
}

func TestRetry_SingleAttempt(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		var calls int
		err := Retry(context.Background(), fastRetryConfig(1), func() error {
			calls++
			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, 1, calls)
	})

	t.Run("failure", func(t *testing.T) {
		sentinel := fmt.Errorf("only try")
		var calls int
		err := Retry(context.Background(), fastRetryConfig(1), func() error {
			calls++
			return sentinel
		})
		require.Error(t, err)
		assert.Equal(t, 1, calls)
		assert.ErrorIs(t, err, sentinel)
		assert.Contains(t, err.Error(), "after 1 attempts")
	})
}

func TestRetry_ZeroAttempts(t *testing.T) {
	var calls int
	err := Retry(context.Background(), fastRetryConfig(0), func() error {
		calls++
		return fmt.Errorf("should not run")
	})

	// Loop body never executes; lastErr is nil -> fmt.Errorf wraps nil.
	// The current implementation returns fmt.Errorf("after 0 attempts: %w", nil)
	// which is "after 0 attempts: <nil>".
	// This is arguably a bug: zero attempts should probably return nil or a
	// dedicated error. We test the actual behavior.
	assert.Equal(t, 0, calls, "fn should never be called with MaxAttempts=0")
	if err != nil {
		assert.Contains(t, err.Error(), "after 0 attempts")
	}
}

func TestIsContextError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil", nil, false},
		{"context.Canceled", context.Canceled, true},
		{"context.DeadlineExceeded", context.DeadlineExceeded, true},
		{"regular error", fmt.Errorf("some error"), false},
		{"wrapped context.Canceled", fmt.Errorf("wrap: %w", context.Canceled), false},
		{"wrapped DeadlineExceeded", fmt.Errorf("wrap: %w", context.DeadlineExceeded), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, IsContextError(tt.err))
		})
	}
}

func TestRetry_ConcurrentCalls(t *testing.T) {
	const goroutines = 10
	var wg sync.WaitGroup
	var successCount atomic.Int32

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			calls := 0
			err := Retry(context.Background(), fastRetryConfig(3), func() error {
				calls++
				if calls < 2 {
					return fmt.Errorf("transient")
				}
				return nil
			})
			if err == nil {
				successCount.Add(1)
			}
		}()
	}

	wg.Wait()
	assert.Equal(t, int32(goroutines), successCount.Load(), "all concurrent retries should succeed")
}
