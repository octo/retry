// Package retry implements a wrapper to retry failing function calls.
package retry

import (
	"context"
	"time"
)

type internalOptions struct {
	ExpBackoff
	Attempts
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
	opts.ExpBackoff = opt
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

// Do repeatedly calls cb until is succeeds. After cb fails (returns a non-nil
// error), execution is paused for an exponentially increasing time. Execution
// can be cancelled at any time by cancelling the context.
//
// By default, this function behaves as if
//     ExpBackoff(100 * time.Millisecond, 2 * time.Second)
// was passed in opts.
func Do(ctx context.Context, cb func(context.Context) error, opts ...Option) error {
	intOpts := internalOptions{
		ExpBackoff: ExpBackoff{
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
	delay := opts.ExpBackoff.Base
	ch := make(chan error)

	var err error
	for i := 0; Attempts(i) < opts.Attempts || opts.Attempts == 0; i++ {
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

		delay = time.Duration(float64(delay) * opts.ExpBackoff.Factor)
		if delay > opts.ExpBackoff.Max {
			delay = opts.ExpBackoff.Max
		}
	}

	return err
}
