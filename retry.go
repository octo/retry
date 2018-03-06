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
// • Jitter
//
// • Timeout
type Option interface {
	apply(*internalOptions)
}

// ExpBackoff sets custom backoff parameters. After the first
// failure, execution pauses for the duration specified by base. After each
// subsequent failure the delay is doubled until max is reached. Execution is
// never paused for longer than the duration max.
//
// Implements the Option interface.
type ExpBackoff struct {
	Base   time.Duration
	Max    time.Duration
	Factor float64
}

func (opt ExpBackoff) apply(opts *internalOptions) {
	opts.backoff = opt
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

// Timeout specifies the timeout for each individual attempt. When specified,
// the context passed to the callback is cancelled after this duration. When
// the timeout expires, the callback should return as quickly as possible. The
// retry logic continues without waiting for the callback to return, though, so
// callbacks should be thread-safe.
//
// Implements the Option interface.
type Timeout time.Duration

func (opt Timeout) apply(opts *internalOptions) {
	opts.Timeout = opt
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

// Abort wraps err so it implements the Error interface and reports a permanent
// condition. This causes Do() to return immediately with the wrapped error.
func Abort(err error) Error {
	return permanentError{err}
}

var contextAttemptKey struct{}

func withAttempt(ctx context.Context, attempt int) context.Context {
	return context.WithValue(ctx, contextAttemptKey, attempt)
}

// Attempt returns the number of previous attempts. In other words, it returns
// the zero-based index of the request.
//
// Only call this function from within a retried function.
func Attempt(ctx context.Context) int {
	i := ctx.Value(contextAttemptKey)
	if i == nil {
		return 0
	}

	return i.(int)
}

// Do repeatedly calls cb until it succeeds. After cb fails (returns a non-nil
// error), execution is paused for an exponentially increasing time. Execution
// can be cancelled at any time by cancelling the context.
//
// By default, this function behaves as if the following options were passed:
//   Attempts(4),
//   ExpBackoff{
//     Base:   100 * time.Millisecond,
//     Max:    2 * time.Second,
//     Factor: 2.0,
//   },
//   FullJitter,
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

func do(ctx context.Context, cb func(context.Context) error, opts internalOptions) error {
	ch := make(chan error)

	var err error
	for i := 0; Attempts(i) < opts.Attempts || opts.Attempts == 0; i++ {
		ctx := withAttempt(ctx, i)

		if !opts.budget.check(i != 0) {
			return errors.New("retry budget exhausted")
		}

		go func(ctx context.Context) {
			if opts.Timeout != 0 {
				ch <- callWithTimeout(ctx, cb, opts.Timeout)
			} else {
				ch <- cb(ctx)
			}
		}(ctx)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case err = <-ch:
			if err == nil {
				return nil
			}
			if retryErr, ok := err.(Error); ok && !retryErr.Temporary() {
				if p, ok := err.(permanentError); ok {
					return p.error
				}
				return err
			}
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

func callWithTimeout(ctx context.Context, cb func(context.Context) error, timeout Timeout) error {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout))
	defer cancel()

	ch := make(chan error)

	go func(ctx context.Context) {
		ch <- cb(ctx)
	}(ctx)

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-ch:
		return err
	}
}
