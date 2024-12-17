package retry

import (
	"fmt"
	"math"
	"sync"
	"time"
)

// Budget implements a retry budget, i.e. a limit for retries.
// Limiting the amount of retries sent to a service helps to mitigate cascading failures.
//
// To add a retry budget for a specific service or backend, declare a Budget
// variable that is shared by all Do() calls. See the example for a demonstration.
//
// Budget calculates the rate of initial calls and the rate of retries over a
// moving one minute window. If the rate of retries exceeds Budget.Rate and the
// ratio of retries exceeds Budget.Ratio, then retries are dropped.
// The Do() function returns ErrExhausted in this case.
//
// Implements the Option interface.
type Budget struct {
	// Rate is the minimum rate of retries (in calls per second).
	// If fewer retries are attempted than this rate, retries are never throttled.
	Rate float64

	// Ratio is the maximum ratio of retries.
	// When used as an option to Do(), it's the ratio of retries to initial
	// calls. In that case ratio is a number in the [0.0, Attempts()]
	// range. The initial request is never dropped.
	// When used as part of BudgetHandler, it's the ratio of retries to
	// total requests. In that case ratio is a number in the [0.0, 1.0]
	// range.
	Ratio float64

	mu           sync.Mutex
	initialCalls *movingRate
	retriedCalls *movingRate
}

func (b *Budget) apply(opts *internalOptions) {
	opts.budget = b
}

// sendOK checks on the client side if a request should be sent. The first
// (non-retried) call is always permitted, blocked retries are not accounted.
func (b *Budget) sendOK(isRetry bool) bool {
	if b == nil {
		return true
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.retriedCalls == nil {
		b.retriedCalls = newMovingRate()
	}
	if b.initialCalls == nil {
		b.initialCalls = newMovingRate()
	}

	t := time.Now()

	if !isRetry {
		b.initialCalls.Add(t, 1)
		return true
	}

	initialRate := b.initialCalls.Rate(t)
	retriedRate := b.retriedCalls.Rate(t)
	if initialRate > b.Rate &&
		// not accounted
		retriedRate/initialRate > b.Ratio {
		return false
	}

	b.retriedCalls.Add(t, 1)
	return true
}

// overload checks on the server side if the cluster appears to be in overload.
// May return true even for initial (non-retried) requests and accounts all
// requests, even when overload is signaled.
func (b *Budget) overload(isRetry bool) bool {
	if b == nil {
		return true
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.retriedCalls == nil {
		b.retriedCalls = newMovingRate()
	}
	if b.initialCalls == nil {
		b.initialCalls = newMovingRate()
	}

	t := time.Now()

	if isRetry {
		b.retriedCalls.Add(t, 1)
	} else {
		b.initialCalls.Add(t, 1)
	}

	initialRate := b.initialCalls.Rate(t)
	retriedRate := b.retriedCalls.Rate(t)
	// TODO(octo): this calculates the ratio as retried/total, while
	// sendOK() uses retried/initial. That's confusing.
	totalRate := initialRate + retriedRate

	return totalRate > b.Rate && retriedRate/totalRate > b.Ratio
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

func newMovingRate() *movingRate {
	return &movingRate{
		BucketLength: time.Second,
		BucketNum:    60,
	}
}

func (mr *movingRate) count() float64 {
	// history is not yet fully initialized
	if len(mr.counts) <= mr.BucketNum {
		var s float64
		for _, c := range mr.counts {
			s += float64(c)
		}
		return s
	}

	oldestFraction := 1.0 -
		float64(mr.lastUpdate.Sub(timeRoundDown(mr.lastUpdate, mr.BucketLength)))/
			float64(mr.BucketLength)

	s := oldestFraction * float64(mr.counts[0])
	for i := 1; i < len(mr.counts); i++ {
		s += float64(mr.counts[i])
	}

	return s
}

func (mr *movingRate) second() float64 {
	if len(mr.counts) == 0 {
		return 0.0
	}

	// history is not yet fully initialized
	if len(mr.counts) <= mr.BucketNum {
		d := time.Duration(len(mr.counts)-1) * mr.BucketLength
		d += mr.lastUpdate.Sub(timeRoundDown(mr.lastUpdate, mr.BucketLength))
		return d.Seconds()
	}

	d := time.Duration(mr.BucketNum) * mr.BucketLength
	return d.Seconds()
}

func (mr *movingRate) shift(n int) {
	if n > mr.BucketNum+1 {
		n = mr.BucketNum + 1
	}

	zero := make([]int, n)
	mr.counts = append(mr.counts, zero...)

	// we actually keep BucketNum+1 buckets -- the newest and oldest
	// buckets are partially evaluated so the window length stays constant.
	if del := len(mr.counts) - (mr.BucketNum + 1); del > 0 {
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
	return mr.count() / mr.second()
}
