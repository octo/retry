package retry

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// cancelAfter returns a context that is cancelled after d.
func cancelAfter(t *testing.T, d time.Duration) context.Context {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	time.AfterFunc(d, cancel)

	return ctx
}

func TestGracePeriod(t *testing.T) {
	t.Parallel()

	const (
		cancelAt = 100 * time.Millisecond
		cleanup  = 100 * time.Millisecond
	)

	errCleanup := errors.New("cleanup failed")

	tests := []struct {
		name  string
		grace GracePeriod
		// cleanup is how long the callback takes to return after its context is cancelled.
		cleanup  time.Duration
		cbErr    error
		wantErr  error
		wantTime time.Duration
		// wantDone is whether the callback returned before Do().
		wantDone bool
	}{
		{
			name:     "callback returns nil within the grace period",
			grace:    GracePeriod(time.Second),
			cleanup:  cleanup,
			wantTime: cancelAt + cleanup,
			wantDone: true,
		},
		{
			name:     "callback returns an error within the grace period",
			grace:    GracePeriod(time.Second),
			cleanup:  cleanup,
			cbErr:    errCleanup,
			wantErr:  errCleanup,
			wantTime: cancelAt + cleanup,
			wantDone: true,
		},
		{
			name:     "grace period expires",
			grace:    GracePeriod(cleanup / 2),
			cleanup:  cleanup,
			wantErr:  context.Canceled,
			wantTime: cancelAt + cleanup/2,
		},
		{
			name:     "zero grace period does not wait",
			cleanup:  cleanup,
			wantErr:  context.Canceled,
			wantTime: cancelAt,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var done atomic.Bool

			cb := func(ctx context.Context) error {
				<-ctx.Done()
				time.Sleep(tt.cleanup)
				done.Store(true)

				return tt.cbErr
			}

			start := time.Now()
			err := Do(cancelAfter(t, cancelAt), cb, Indefinitely, tt.grace)
			got := time.Since(start)

			if !errors.Is(err, tt.wantErr) || (tt.wantErr == nil && err != nil) {
				t.Errorf("Do() = %v, want %v", err, tt.wantErr)
			}

			if !durationEqual(got, tt.wantTime) {
				t.Errorf("Do() took %v, want %v", got, tt.wantTime)
			}

			if got := done.Load(); got != tt.wantDone {
				t.Errorf("callback returned before Do() = %v, want %v", got, tt.wantDone)
			}
		})
	}
}

// TestGracePeriodTimeout checks that after an attempt's Timeout expires, the
// next attempt starts only once the previous callback returned, if it does so
// within the grace period.
func TestGracePeriodTimeout(t *testing.T) {
	t.Parallel()

	var firstDone, overlap atomic.Bool

	cb := func(ctx context.Context) error {
		if Attempt(ctx) == 0 {
			<-ctx.Done()
			time.Sleep(50 * time.Millisecond)
			firstDone.Store(true)

			return ctx.Err()
		}

		overlap.Store(!firstDone.Load())

		return nil
	}

	// The backoff is shorter than the first callback's cleanup, so that only the
	// grace period keeps the attempts from overlapping.
	backoff := ExpBackoff{Base: time.Millisecond, Max: time.Millisecond, Factor: 1}

	err := Do(context.Background(), cb, Attempts(2), Timeout(50*time.Millisecond), GracePeriod(time.Second), backoff, WithoutJitter)
	if err != nil {
		t.Errorf("Do() = %v, want <nil>", err)
	}

	if overlap.Load() {
		t.Error("the second attempt started before the first callback returned")
	}
}
