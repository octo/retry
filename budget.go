package retry

import (
	"fmt"
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

func timeRoundDown(t time.Time, d time.Duration) time.Time {
	rt := t.Round(d)
	if rt.After(t) {
		rt = rt.Add(-d)
	}

	return rt
}

type movingRate struct {
	BucketLength time.Duration
	BucketNum    int

	counts     []int
	lastUpdate time.Time
}

func (mr *movingRate) count() float64 {
	var s float64
	for _, c := range mr.counts {
		s += float64(c)
	}

	return s
}

func (mr *movingRate) second() float64 {
	if len(mr.counts) == 0 {
		return 0.0
	}

	d := time.Duration(len(mr.counts)-1) * mr.BucketLength
	d += mr.lastUpdate.Sub(timeRoundDown(mr.lastUpdate, mr.BucketLength))

	return d.Seconds()
}

func (mr *movingRate) shift(n int) {
	if n > mr.BucketNum {
		n = mr.BucketNum
	}

	zero := make([]int, n)
	mr.counts = append(mr.counts, zero...)

	if del := len(mr.counts) - mr.BucketNum; del > 0 {
		mr.counts = mr.counts[del:]
	}

	mr.lastUpdate = timeRoundDown(mr.lastUpdate, mr.BucketLength).Add(time.Duration(n) * mr.BucketLength)

}

func (mr *movingRate) forward(t time.Time) {
	defer func() {
		mr.lastUpdate = t
	}()

	if mr.lastUpdate.IsZero() {
		mr.counts = []int{0}
		return
	}

	rt := timeRoundDown(t, mr.BucketLength)
	if !rt.After(mr.lastUpdate) {
		return
	}

	n := int(rt.Sub(timeRoundDown(mr.lastUpdate, mr.BucketLength)) / mr.BucketLength)
	if n <= 0 {
		panic(fmt.Sprintf("assertion failure: n = %d, want >0; rt = %v, mr.lastUpdate = %v, mr.BucketLength = %v",
			n, rt, mr.lastUpdate, mr.BucketLength))
	}

	mr.shift(n)
}

func (mr *movingRate) Add(t time.Time, n int) {
	if t.Before(mr.lastUpdate) {
		return
	}

	mr.forward(t)
	mr.counts[len(mr.counts)-1] += n
}

func (mr *movingRate) Rate(t time.Time) float64 {
	if t.Before(mr.lastUpdate) {
		return math.NaN()
	}

	mr.forward(t)

	cnt := mr.count()
	if cnt == 0.0 {
		return 0.0
	}

	return cnt / mr.second()
}
