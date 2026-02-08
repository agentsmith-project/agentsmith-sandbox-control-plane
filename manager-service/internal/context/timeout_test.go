package context

import (
	stdcontext "context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTimeoutConstants verifies that timeout constants are set correctly.
func TestTimeoutConstants(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		expected time.Duration
	}{
		{
			name:     "ShortTimeout is 5 seconds",
			duration: ShortTimeout,
			expected: 5 * time.Second,
		},
		{
			name:     "DefaultTimeout is 30 seconds",
			duration: DefaultTimeout,
			expected: 30 * time.Second,
		},
		{
			name:     "LongTimeout is 5 minutes",
			duration: LongTimeout,
			expected: 5 * time.Minute,
		},
		{
			name:     "SnapshotTimeout is 10 minutes",
			duration: SnapshotTimeout,
			expected: 10 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.duration != tt.expected {
				t.Errorf("%s: expected %v, got %v", tt.name, tt.expected, tt.duration)
			}
		})
	}
}

// TestTimeoutHierarchy verifies that timeouts form a proper hierarchy.
func TestTimeoutHierarchy(t *testing.T) {
	// Verify the hierarchy: Short < Default < Long < Snapshot
	assert.Less(t, ShortTimeout, DefaultTimeout, "ShortTimeout should be less than DefaultTimeout")
	assert.Less(t, DefaultTimeout, LongTimeout, "DefaultTimeout should be less than LongTimeout")
	assert.Less(t, LongTimeout, SnapshotTimeout, "LongTimeout should be less than SnapshotTimeout")
}

// TestWithDefaultTimeout creates a context with DefaultTimeout and verifies it times out.
func TestWithDefaultTimeout(t *testing.T) {
	ctx, cancel := WithDefaultTimeout(stdcontext.Background())
	defer cancel()

	// Verify deadline is set
	deadline, ok := ctx.Deadline()
	require.True(t, ok, "Context should have a deadline")

	// Verify deadline is approximately DefaultTimeout from now
	expectedDeadline := time.Now().Add(DefaultTimeout)
	diff := expectedDeadline.Sub(deadline)

	// Allow 100ms tolerance for test execution time
	if diff < 0 {
		diff = -diff
	}
	assert.Less(t, diff, 100*time.Millisecond, "Deadline should be approximately DefaultTimeout from now")
}

// TestWithShortTimeout creates a context with ShortTimeout and verifies it times out.
func TestWithShortTimeout(t *testing.T) {
	ctx, cancel := WithShortTimeout(stdcontext.Background())
	defer cancel()

	deadline, ok := ctx.Deadline()
	require.True(t, ok, "Context should have a deadline")

	expectedDeadline := time.Now().Add(ShortTimeout)
	diff := expectedDeadline.Sub(deadline)

	if diff < 0 {
		diff = -diff
	}
	assert.Less(t, diff, 100*time.Millisecond, "Deadline should be approximately ShortTimeout from now")
}

// TestWithLongTimeout creates a context with LongTimeout and verifies it times out.
func TestWithLongTimeout(t *testing.T) {
	ctx, cancel := WithLongTimeout(stdcontext.Background())
	defer cancel()

	deadline, ok := ctx.Deadline()
	require.True(t, ok, "Context should have a deadline")

	expectedDeadline := time.Now().Add(LongTimeout)
	diff := expectedDeadline.Sub(deadline)

	if diff < 0 {
		diff = -diff
	}
	assert.Less(t, diff, 100*time.Millisecond, "Deadline should be approximately LongTimeout from now")
}

// TestWithSnapshotTimeout creates a context with SnapshotTimeout and verifies it times out.
func TestWithSnapshotTimeout(t *testing.T) {
	ctx, cancel := WithSnapshotTimeout(stdcontext.Background())
	defer cancel()

	deadline, ok := ctx.Deadline()
	require.True(t, ok, "Context should have a deadline")

	expectedDeadline := time.Now().Add(SnapshotTimeout)
	diff := expectedDeadline.Sub(deadline)

	if diff < 0 {
		diff = -diff
	}
	assert.Less(t, diff, 100*time.Millisecond, "Deadline should be approximately SnapshotTimeout from now")
}

// TestWithDefaultTimeout_Cancellation verifies parent context cancellation is propagated.
func TestWithDefaultTimeout_Cancellation(t *testing.T) {
	parentCtx, parentCancel := stdcontext.WithCancel(stdcontext.Background())
	ctx, cancel := WithDefaultTimeout(parentCtx)
	defer cancel()

	// Cancel parent
	parentCancel()

	// Child should be cancelled
	select {
	case <-ctx.Done():
		// Expected
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Child context should be cancelled when parent is cancelled")
	}

	assert.Equal(t, stdcontext.Canceled, ctx.Err(), "Error should be Canceled")
}

// TestWithShortTimeout_PropagatesParentDeadline verifies parent deadline is respected.
func TestWithShortTimeout_PropagatesParentDeadline(t *testing.T) {
	// Parent with very short deadline
	parentCtx, parentCancel := stdcontext.WithTimeout(stdcontext.Background(), 10*time.Millisecond)
	defer parentCancel()

	// Child with longer deadline
	ctx, cancel := WithLongTimeout(parentCtx)
	defer cancel()

	// Child should respect parent's shorter deadline
	<-ctx.Done()
	assert.Equal(t, stdcontext.DeadlineExceeded, ctx.Err(), "Error should be DeadlineExceeded")
}

// TestWithDefaultTimeout_PropagatesParentValues verifies values from parent are available.
func TestWithDefaultTimeout_PropagatesParentValues(t *testing.T) {
	parentCtx := stdcontext.Background()
	type key string
	k := key("test-key")
	parentCtx = stdcontext.WithValue(parentCtx, k, "test-value")

	ctx, cancel := WithDefaultTimeout(parentCtx)
	defer cancel()

	// Value should be available in child context
	value := ctx.Value(k)
	assert.Equal(t, "test-value", value, "Child context should have parent's values")
}
