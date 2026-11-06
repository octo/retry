// Package retry implements a wrapper to retry failing function calls.
package retry

import (
	"context"
	"errors"
	"math"
	"time"
)

type backoff interface {
	delay(attempt int) time.Duration
}

type internalOptions struct {
	Attempts
	backoff
	budget *Budget
	Jitter
	Timeout
	GracePeriod
}

// Option is an option for Do().
//
// The following types implement Option:
//
// • Attempts
//
// • Budget
//
// • ExpBackoff
//
// • GracePeriod
//
// • Jitter
//
// • Timeout
type Option interface {
	apply(*internalOptions)
}

// ExpBackoff sets custom backoff parameters.
// After the first failure, execution pauses for the duration specified by base.
// After each subsequent failure the delay is multiplied by Factor until max is reached.
// Execution is never paused for longer than the Max duration.
//
// Implements the Option interface.
type ExpBackoff struct {
	Base   time.Duration
	Max    time.Duration
	Factor float64
}

func (b ExpBackoff) apply(opts *internalOptions) {
	opts.backoff = b
}

func (b ExpBackoff) delay(attempt int) time.Duration {
	f := float64(b.Base) * math.Pow(b.Factor, float64(attempt))

	d := time.Duration(f)
	if d < b.Base {
		return b.Base
	} else if d > b.Max {
		return b.Max
	}
	return d
}

// Attempts sets the number of calls made to the callback, i.e. the call is
// attempted at most n times. If all calls fail, the error of the last call is
// returned by Do().
//
// Special case: the zero value retries indefinitely.
//
// Implements the Option interface.
type Attempts int

func (opt Attempts) apply(opts *internalOptions) {
	opts.Attempts = opt
}

// Indefinitely removes the limit on the number of attempts. Do() then calls
// the callback until it succeeds, returns a permanent error, the Budget is
// exhausted, or the context is cancelled.
const Indefinitely Attempts = 0

// Timeout specifies the timeout for each individual attempt. When specified,
// the context passed to the callback is cancelled after this duration. When
// the timeout expires, the callback should return as quickly as possible. The
// retry logic waits for the callback to return for at most the GracePeriod,
// which is zero by default, and then continues with the next attempt. A
// callback that is still running then runs concurrently with the next attempt,
// so callbacks should be thread-safe.
//
// Implements the Option interface.
type Timeout time.Duration

func (opt Timeout) apply(opts *internalOptions) {
	opts.Timeout = opt
}

// GracePeriod specifies how long Do() waits for a running callback to return
// after the callback's context is cancelled, either because the context passed
// to Do() is cancelled or because the attempt's Timeout expired.
//
// If the callback returns within the grace period, its result is used: Do()
// returns it if the context passed to Do() was cancelled, and otherwise retries
// or returns it as usual. If the callback does not return in time, the context's
// error is used instead, and the callback keeps running in the background.
//
// The zero value does not wait, so Do() returns as soon as its context is
// cancelled.
//
// Implements the Option interface.
type GracePeriod time.Duration

func (opt GracePeriod) apply(opts *internalOptions) {
	opts.GracePeriod = opt
}

// Error is an error type that controls retry behavior. If Temporary() returns
// false, Do() returns immediately and does not continue to call the callback
// function.
//
// Error is specifically designed to be a subset of net.Error.
type Error interface {
	Temporary() bool
	error
}

// permanentError is a persisting error condition.
type permanentError struct {
	error
}

func (permanentError) Temporary() bool { return false }

func (e permanentError) Unwrap() error {
	return e.error
}

// Abort wraps err so it implements the Error interface and reports a permanent
// condition. This causes Do() to return immediately with the wrapped error.
func Abort(err error) Error {
	return permanentError{err}
}

type ctxKey struct{}

func withAttempt(ctx context.Context, attempt int) context.Context {
	return context.WithValue(ctx, ctxKey{}, attempt)
}

// Attempt returns the number of previous attempts. In other words, it returns
// the zero-based index of the request.
//
// Only call this function from within a retried function.
func Attempt(ctx context.Context) int {
	i := ctx.Value(ctxKey{})
	if i == nil {
		return 0
	}

	return i.(int)
}

// Do repeatedly calls cb until it succeeds. After cb fails (returns a non-nil
// error), execution is paused for an exponentially increasing time. Execution
// can be cancelled at any time by cancelling the context; use GracePeriod to
// give a running callback time to return when that happens.
//
// By default, this function behaves as if the following options were passed:
//
//	Attempts(4),
//	ExpBackoff{
//	  Base:   100 * time.Millisecond,
//	  Max:    2 * time.Second,
//	  Factor: 2.0,
//	},
//	FullJitter,
func Do(ctx context.Context, cb func(context.Context) error, opts ...Option) error {
	intOpts := internalOptions{
		Attempts: Attempts(4),
		backoff: ExpBackoff{
			Base:   100 * time.Millisecond,
			Max:    2 * time.Second,
			Factor: 2.0,
		},
		Jitter: FullJitter,
	}

	for _, o := range opts {
		o.apply(&intOpts)
	}

	return do(ctx, cb, intOpts)
}

// ErrExhausted is returned by Do() when the retry budget is exhausted.
var ErrExhausted = errors.New("retry budget exhausted")

func do(ctx context.Context, cb func(context.Context) error, opts internalOptions) error {
	var err error
	for i := 0; Attempts(i) < opts.Attempts || opts.Attempts == 0; i++ {
		ctx := withAttempt(ctx, i)

		if !opts.budget.sendOK(i != 0) {
			return ErrExhausted
		}

		err = call(ctx, cb, opts)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return err
		}
		if retryErr, ok := err.(Error); ok && !retryErr.Temporary() {
			if p, ok := err.(permanentError); ok {
				return p.error
			}
			return err
		}

		delay := opts.delay(i)
		delay = opts.jitter(delay)

		ticker := time.NewTicker(delay)
		select {
		case <-ctx.Done():
			ticker.Stop()
			return ctx.Err()
		case <-ticker.C:
			ticker.Stop()
		}
	}

	return err
}

// call makes a single attempt: it runs cb in a goroutine and returns its
// result. If ctx is cancelled or the attempt's Timeout expires before cb
// returns, call waits for cb for at most the GracePeriod and returns the
// context's error if cb does not return in time.
func call(ctx context.Context, cb func(context.Context) error, opts internalOptions) error {
	if opts.Timeout != 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(opts.Timeout))
		defer cancel()
	}

	// The channel is buffered so that the goroutine running cb can exit when cb
	// returns after call stopped waiting for it.
	ch := make(chan error, 1)

	go func(ctx context.Context) {
		ch <- cb(ctx)
	}(ctx)

	select {
	case err := <-ch:
		return err
	case <-ctx.Done():
	}

	if opts.GracePeriod <= 0 {
		return ctx.Err()
	}

	timer := time.NewTimer(time.Duration(opts.GracePeriod))
	defer timer.Stop()

	select {
	case err := <-ch:
		return err
	case <-timer.C:
		return ctx.Err()
	}
}
