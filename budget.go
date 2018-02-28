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

type movingRate struct {
	historyCounts    []int
	historyTimestamp int64
	historySize      int

	stagingCount     int
	stagingTimestamp int64
}

func newMovingRate(size int) *movingRate {
	return &movingRate{
		historySize: size,
	}
}

// commitToHistory appends the provided counts to historyCounts.
// If the number of elements in historyCounts exceeds historySize, the oldest entries are purged.
// historyTimestamp is advanced by len(count).
func (mr *movingRate) commitToHistory(count ...int) {
	mr.historyCounts = append(mr.historyCounts, count...)
	if len(mr.historyCounts) > mr.historySize {
		idx := len(mr.historyCounts) - mr.historySize
		mr.historyCounts = mr.historyCounts[idx:]
	}

	mr.historyTimestamp += int64(len(count))
}

// clearHistory clears the history and updates historyTimestamp.
func (mr *movingRate) clearHistory(ts int64) {
	mr.historyCounts = []int{}
	mr.historyTimestamp = ts
}

// forwardHistory ensures that the newest element in historyCounts represents the timestamp ts.
// If ts is much larger than historyTimestamp (i.e. all elements would be
// zero), then historyCounts is reset to an empty slice.
func (mr *movingRate) forwardHistory(ts int64) {
	if ts <= mr.historyTimestamp {
		return
	}

	if mr.historyTimestamp+int64(mr.historySize) <= ts {
		mr.clearHistory(ts)
		return
	}

	zero := make([]int, ts-mr.historyTimestamp)
	mr.commitToHistory(zero...)
}

// persistStaging appends stagingCount to historyCounts.
func (mr *movingRate) persistStaging() {
	if mr.stagingTimestamp == 0 {
		return
	}

	mr.forwardHistory(mr.stagingTimestamp - 1)
	mr.commitToHistory(mr.stagingCount)

	mr.stagingCount = 0
	mr.stagingTimestamp++
}

// forwardStaging updates all internal state so that stagingTimestamp == ts and
// historyTimestamp == ts-1.
func (mr *movingRate) forwardStaging(ts int64) {
	if ts <= mr.stagingTimestamp {
		return
	}

	mr.persistStaging()
	mr.forwardHistory(ts - 1)
	mr.stagingTimestamp = ts
}

func (mr *movingRate) sum() float64 {
	var s float64
	for _, c := range mr.historyCounts {
		s += float64(c)
	}

	return s
}

func (mr *movingRate) num() float64 {
	return float64(len(mr.historyCounts))
}

func (mr *movingRate) Rate(t time.Time) float64 {
	if t.Unix() < mr.stagingTimestamp {
		return math.NaN()
	}

	mr.forwardStaging(t.Unix())

	if len(mr.historyCounts) == 0 {
		return 0.0
	}

	return mr.sum() / mr.num()
}

func (mr *movingRate) Add(t time.Time, n int) {
	if t.Unix() < mr.stagingTimestamp {
		return
	}

	mr.forwardStaging(t.Unix())

	mr.stagingCount += n
}
