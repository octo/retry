package retry

import (
	"math"
	"sync"
	"time"
)

// Budget implements a client-side retry budget, i.e. a rate limit for retries.
// Rate-limiting the amount of retries sent to a service helps to mitigate cascading failures.
//
// To add a retry budget for a specific service or backend, declare a Budget
// variable that is shared by all Do() calls. See the example for a demonstration.
//
// Budget implements a token bucket algorithm.
//
// Implements the Option interface.
type Budget struct {
	// Rate is the rate at which tokens are added to the bucket, i.e. the
	// sustained retry rate in retries per second.
	// Must be greater than zero.
	Rate float64
	// Burst is the maximum number of tokens in the bucket, i.e. the burst
	// size with which retries are performed.
	// Must be greater than one.
	Burst float64

	mu         sync.Mutex
	fill       float64
	lastUpdate time.Time
}

func (b *Budget) apply(opts *internalOptions) {
	opts.budget = b
}

func (b *Budget) check() bool {
	if b == nil {
		return true
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	// update b.fill
	if b.lastUpdate.IsZero() {
		b.fill = b.Burst
	} else {
		s := time.Since(b.lastUpdate).Seconds()
		b.fill = math.Min(b.fill+b.Rate*s, b.Burst)
	}
	b.lastUpdate = time.Now()

	if b.fill < 1.0 {
		// budget exhausted
		return false
	}

	b.fill -= 1.0
	return true
}
