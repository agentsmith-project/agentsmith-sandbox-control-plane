package observability

import (
	"context"
	"fmt"
	"time"
)

// Poller provides context-aware polling functionality with proper cancellation support.
type Poller struct {
	interval time.Duration // Polling interval between checks
	timeout  time.Duration // Maximum time to poll before timeout
}

// NewPoller creates a new Poller with the specified interval and timeout.
func NewPoller(interval, timeout time.Duration) *Poller {
	return &Poller{
		interval: interval,
		timeout:  timeout,
	}
}

// Poll executes the check function repeatedly until it returns true (done)
// or the context is canceled/timeout occurs.
// The check function returns (done, error) where:
// - done: true indicates the condition is satisfied and polling should stop
// - error: any error that occurred during the check (will stop polling)
func (p *Poller) Poll(ctx context.Context, check func() (bool, error)) error {
	// Create a context with timeout for this polling operation
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	// Create a ticker for periodic checks
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Context was canceled or timeout exceeded
			return fmt.Errorf("poll canceled: %w", ctx.Err())

		case <-ticker.C:
			// Time to perform a check
			done, err := check()
			if err != nil {
				// Check returned an error
				return err
			}
			if done {
				// Condition satisfied, polling complete
				return nil
			}
		}
	}
}

// PollWithRetry is like Poll but allows retry on specific errors.
// shouldRetry function determines whether to retry based on the error.
// If shouldRetry returns false, the error is returned immediately.
func (p *Poller) PollWithRetry(ctx context.Context, check func() (bool, error), shouldRetry func(error) bool) error {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	var lastErr error

	for {
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("poll canceled: %w (last error: %v)", ctx.Err(), lastErr)
			}
			return fmt.Errorf("poll canceled: %w", ctx.Err())

		case <-ticker.C:
			done, err := check()
			if err != nil {
				lastErr = err
				if !shouldRetry(err) {
					return err
				}
				// Continue polling for retryable errors
				continue
			}
			if done {
				return nil
			}
		}
	}
}

// DefaultPoller returns a Poller with common defaults.
func DefaultPoller() *Poller {
	return NewPoller(2*time.Second, 5*time.Minute)
}
