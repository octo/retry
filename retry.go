// Package retry implements a wrapper to retry failing function calls.
package retry

import (
	"context"
	"math"
	"time"
)

type backoff interface {
	delay(attempt int) time.Duration
}

type internalOptions struct {
	Attempts
	backoff
	Timeout
}

// Option is an option for Do().
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
	d := time.Duration(float64(b.Base) * math.Pow(b.Factor, float64(attempt)))

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

// Do repeatedly calls cb until is succeeds. After cb fails (returns a non-nil
// error), execution is paused for an exponentially increasing time. Execution
// can be cancelled at any time by cancelling the context.
//
// By default, this function behaves as if
//     ExpBackoff(100 * time.Millisecond, 2 * time.Second)
// was passed in opts.
func Do(ctx context.Context, cb func(context.Context) error, opts ...Option) error {
	intOpts := internalOptions{
		backoff: ExpBackoff{
			Base:   100 * time.Millisecond,
			Max:    2 * time.Second,
			Factor: 2.0,
		},
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

		delay := opts.backoff.delay(i)
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
