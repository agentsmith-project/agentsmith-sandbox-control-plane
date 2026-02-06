package observability

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPoller_Success(t *testing.T) {
	poller := NewPoller(10*time.Millisecond, 100*time.Millisecond)

	count := 0
	ctx := context.Background()

	err := poller.Poll(ctx, func() (bool, error) {
		count++
		if count >= 3 {
			return true, nil
		}
		return false, nil
	})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 polls, got %d", count)
	}
}

func TestPoller_Timeout(t *testing.T) {
	poller := NewPoller(10*time.Millisecond, 50*time.Millisecond)

	ctx := context.Background()

	err := poller.Poll(ctx, func() (bool, error) {
		return false, nil // Never returns true
	})

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestPoller_ContextCanceled(t *testing.T) {
	poller := NewPoller(10*time.Millisecond, 1*time.Second)

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after first poll
	go func() {
		time.Sleep(15 * time.Millisecond)
		cancel()
	}()

	err := poller.Poll(ctx, func() (bool, error) {
		return false, nil
	})

	if err == nil {
		t.Fatal("expected context canceled error, got nil")
	}
}

func TestPoller_CheckError(t *testing.T) {
	poller := NewPoller(10*time.Millisecond, 100*time.Millisecond)

	ctx := context.Background()
	expectedErr := errors.New("check failed")

	err := poller.Poll(ctx, func() (bool, error) {
		return false, expectedErr
	})

	if err != expectedErr {
		t.Fatalf("expected check error, got %v", err)
	}
}

func TestPollerWithRetry_Success(t *testing.T) {
	poller := NewPoller(10*time.Millisecond, 100*time.Millisecond)

	count := 0
	ctx := context.Background()

	shouldRetry := func(err error) bool {
		return err.Error() == "transient"
	}

	err := poller.PollWithRetry(ctx, func() (bool, error) {
		count++
		if count == 1 {
			return false, errors.New("transient")
		}
		if count == 2 {
			return false, errors.New("transient")
		}
		return true, nil
	}, shouldRetry)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 polls, got %d", count)
	}
}

func TestPollerWithRetry_NonRetryableError(t *testing.T) {
	poller := NewPoller(10*time.Millisecond, 100*time.Millisecond)

	count := 0
	ctx := context.Background()

	shouldRetry := func(err error) bool {
		return err.Error() == "transient"
	}

	expectedErr := errors.New("permanent")

	err := poller.PollWithRetry(ctx, func() (bool, error) {
		count++
		if count == 1 {
			return false, errors.New("transient")
		}
		return false, expectedErr
	}, shouldRetry)

	if err != expectedErr {
		t.Fatalf("expected permanent error, got %v", err)
	}
}

func TestDefaultPoller(t *testing.T) {
	poller := DefaultPoller()

	if poller.interval != 2*time.Second {
		t.Fatalf("expected interval 2s, got %v", poller.interval)
	}
	if poller.timeout != 5*time.Minute {
		t.Fatalf("expected timeout 5m, got %v", poller.timeout)
	}
}
