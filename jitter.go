package retry

import (
	"math/rand"
	"time"
)

// Jitter is a randomization of the backoff delay. Randomizing the delay avoids
// thundering herd problems, for example when using optimistic locking.
//
// Jitter is a floating point number in the (0-1] range that controls the
// weight of the random number. Assuming that the current backoff delay is
// 100ms, Jitter 1.0 means the result is in the range [0,100) ms, Jitter 0.2
// means the result is in the range [80,100) ms.
//
// The following formula is used:
//   delay = Jitter * random_between(0, delay) + (1 - Jitter) * delay
//
// Special cases: the zero value is treated equally to FullJitter. Minus one
// (-1.0) deactivates jitter.
//
// An in-depth discussion of different jitter strategies and their impact on
// client work and server load is available at:
// https://aws.amazon.com/blogs/architecture/exponential-backoff-and-jitter/
//
// Implements the Option interface.
type Jitter float64

// EqualJitter produces random the delays in the [max/2,max) range.
// The name refers to the fact that the obligatory delay and the random range
// are of equal length.
const EqualJitter Jitter = 0.5

// FullJitter produces random the delays in the [0,max) range.
// This is the recommanded instance and the default behavior.
const FullJitter Jitter = 1.0

// WithoutJitter deactivates jitter and always returns max.
const WithoutJitter Jitter = -1.0

func (j Jitter) apply(o *internalOptions) {
	o.Jitter = j
}

func (j Jitter) jitter(d time.Duration) time.Duration {
	if j < 0.0 {
		return d
	}

	r := rand.Float64() * float64(d)
	if j > 0.0 && j < 1.0 {
		r = float64(j)*r + float64(1.0-j)*float64(d)
	}

	return time.Duration(r)
}
