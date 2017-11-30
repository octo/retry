// Package retry implements a wrapper to retry failing function calls.
package retry

import (
	"context"
	"time"
)

type internalOptions struct {
	expBackoff
	maxCalls int
}

// Option is an option for Do().
type Option interface {
	apply(*internalOptions)
}

type expBackoff struct {
	base time.Duration
	max  time.Duration
}

func (opt expBackoff) apply(opts *internalOptions) {
	opts.expBackoff = opt
}

// ExpBackoff sets custom backoff parameters. After the first
// failure, execution pauses for the duration specified by base. After each
// subsequent failure the delay is doubled until max is reached. Execution is
// never paused for longer than the duration max.
func ExpBackoff(base, max time.Duration) Option {
	return &expBackoff{
		base: base,
		max:  max,
	}
}

type maxCalls int

func (opt maxCalls) apply(opts *internalOptions) {
	opts.maxCalls = int(opt)
}

// Attempts sets the number of calls made to the callback, i.e. the call is
// attempted at most n times. If all calls fail, the error of the last call is
// returned by Do().
func Attempts(n int) Option {
	return maxCalls(n)
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
		expBackoff: expBackoff{
			base: 100 * time.Millisecond,
			max:  2 * time.Second,
		},
	}

	for _, o := range opts {
		o.apply(&intOpts)
	}

	return do(ctx, cb, intOpts)
}

func do(ctx context.Context, cb func(context.Context) error, opts internalOptions) error {
	delay := opts.expBackoff.base
	ch := make(chan error)

	var err error
	for i := 0; i < opts.maxCalls || opts.maxCalls == 0; i++ {
		go func() {
			ch <- cb(ctx)
		}()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case err = <-ch:
			if err == nil {
				return nil
			}
		}

		ticker := time.NewTicker(delay)
		select {
		case <-ctx.Done():
			ticker.Stop()
			return ctx.Err()
		case <-ticker.C:
			ticker.Stop()
		}

		delay = delay * 2
		if delay > opts.expBackoff.max {
			delay = opts.expBackoff.max
		}
	}

	return err
}
