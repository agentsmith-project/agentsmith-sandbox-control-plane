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

// DefaultPoller returns a Poller with common defaults.
func DefaultPoller() *Poller {
	return NewPoller(2*time.Second, 5*time.Minute)
}
