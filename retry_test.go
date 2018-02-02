package retry

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"testing"
	"time"
)

func durationEqual(d0, d1 time.Duration) bool {
	var diff time.Duration
	if d0 > d1 {
		diff = d0 - d1
	} else {
		diff = d1 - d0
	}

	return diff < 10*time.Millisecond
}

func TestDo(t *testing.T) {
	ctx := context.Background()
	start := time.Now()

	n := 3
	got := make([]time.Duration, 0, 4)
	cb := func(_ context.Context) error {
		got = append(got, time.Since(start))

		if n == 0 {
			return nil
		}
		n--
		return fmt.Errorf("n=%d", n)
	}

	if err := Do(ctx, cb); err != nil {
		t.Errorf("Do() = %v", err)
	}

	want := []time.Duration{
		0 * time.Millisecond,
		100 * time.Millisecond,
		300 * time.Millisecond,
		700 * time.Millisecond,
	}

	for i := range want {
		if !durationEqual(got[i], want[i]) {
			t.Errorf("got[%d] = %v, want[%d] = %v", i, got[i], i, want[i])
		}
	}
}

func TestCancelInCallback(t *testing.T) {
	want := 500 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), want)
	defer cancel()

	cb := func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}

	start := time.Now()
	if err := Do(ctx, cb); err != context.DeadlineExceeded {
		t.Errorf("Do() = %v, want %v", err, context.DeadlineExceeded)
	}
	got := time.Since(start)

	if !durationEqual(got, want) {
		t.Errorf("got = %v, want = %v", got, want)
	}
}

func TestCancelInTimer(t *testing.T) {
	want := 500 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), want)
	defer cancel()

	cb := func(ctx context.Context) error {
		return fmt.Errorf("oh no")
	}

	start := time.Now()
	if err := Do(ctx, cb); err != context.DeadlineExceeded {
		t.Errorf("Do() = %v, want %v", err, context.DeadlineExceeded)
	}
	got := time.Since(start)

	if !durationEqual(got, want) {
		t.Errorf("got = %v, want = %v", got, want)
	}
}

func TestAbort(t *testing.T) {
	ctx := context.Background()
	var n int

	cb := func(ctx context.Context) error {
		n++

		err := fmt.Errorf("n = %d", n)
		if n == 1 {
			return err
		}
		return Abort(err)
	}

	if err := Do(ctx, cb); err == nil || err.Error() != "n = 2" {
		t.Errorf("Do() = %v, want %v", err, fmt.Errorf("n = %d", 2))
	}
}

// TestError ensures that net.Error is a superset of Error.
func TestError(t *testing.T) {
	// Give the net.Error interface a local name (by wrapping it in an
	// otherwise empty interface) so that the compiler does not get
	// confused by Error (the embedded type) and Error (the method required
	// by the "error" interface).
	type errInterface interface {
		net.Error
	}

	// errType is a type implementing net.Error by embedding the interface.
	type errType struct {
		errInterface
	}

	// Assert that we can handle errType as an Error, too.
	var err net.Error = errType{}
	if _, ok := err.(Error); !ok {
		t.Fatalf("net.Error does not implement the Error interface")
	}
}

func ExampleDo() {
	ctx := context.Background()

	// cb is a function that may or may not fail.
	cb := func(_ context.Context) error {
		return nil // or error
	}

	// Call cb via Do() until it succeeds.
	if err := Do(ctx, cb); err != nil {
		log.Printf("cb() = %v", err)
	}
}

// This example demonstrates how responses to an HTTP request might be handled.
// Responses with an error code between 400 and 499 will abort the Do() call,
// since the server indicates that there is problem on the client side and
// retrying the callback would just do the same thing again.
func ExampleAbort() {
	ctx := context.Background()

	cb := func(ctx context.Context) error {
		req, err := http.NewRequest(http.MethodGet, "http://example.com/", nil)
		if err != nil {
			return err
		}
		req = req.WithContext(ctx)

		res, err := http.DefaultClient.Do(req)
		if err != nil {
			// This is likely a networking problem since the default client doesn't have any policies configured.
			// Specifically, it may be a net.Error which implements the Error interface.
			// Returning this may or may not abort the Do() call, depending on the error.
			return err
		}
		if res.StatusCode >= 400 && res.StatusCode < 500 {
			// Client error, i.e. we're doing someting wrong
			// -> Abort
			return Abort(fmt.Errorf("HTTP status %d (%q)", res.StatusCode, res.Status))
		}
		if res.StatusCode >= 500 {
			// Server error, i.e. not our fault
			// -> Try again
			return fmt.Errorf("HTTP status %d (%q)", res.StatusCode, res.Status)
		}

		// TODO: do something meaningful with res.

		return nil // Success
	}

	// Call cb via Do() until it succeeds or Abort() is returned.
	if err := Do(ctx, cb); err != nil {
		log.Printf("cb() = %v", err)
	}
}

// This example demonstrates how to cancel a retried function call after a specific time.
func ExampleDo_withTimeout() {
	// Create a context which is cancelled after 10 seconds.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// cb is a function that may or may not fail.
	cb := func(_ context.Context) error {
		return nil // or error
	}

	// Call cb via Do() until is succeeds or the 10 second timeout is reached.
	if err := Do(ctx, cb); err != nil {
		log.Printf("cb() = %v", err)
	}
}

func ExampleExpBackoff() {
	ctx := context.Background()

	// cb is a function that may or may not fail.
	cb := func(_ context.Context) error {
		return nil // or error
	}

	opts := []Option{
		ExpBackoff{
			Base:   10 * time.Millisecond,
			Max:    5 * time.Second,
			Factor: 2.0,
		},
	}

	// Call cb via Do() with custom backoff parameters.
	if err := Do(ctx, cb, opts...); err != nil {
		log.Printf("cb() = %v", err)
	}
}

func ExampleAttempts() {
	ctx := context.Background()

	// cb is a function that may or may not fail.
	cb := func(_ context.Context) error {
		return nil // or error
	}

	// Call cb via Do() at most 5 times.
	if err := Do(ctx, cb, Attempts(5)); err != nil {
		log.Printf("cb() = %v", err)
	}
}
