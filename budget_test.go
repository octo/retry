package retry

import (
	"context"
	"errors"
	"log"
	"math"
	"math/rand"
	"sort"
	"sync"
	"testing"
	"time"
)

func ExampleBudget() {
	ctx := context.Background()

	// fooRetryBudget is a global variable holding the state of foo's retry budget.
	// You should have one retry budget per backend service.
	var fooRetryBudget = Budget{
		Rate:  1.0,
		Ratio: 0.1,
	}

	// failingRPC is a fake RPC call simulating a temporary backend failure.
	failingRPC := func(_ context.Context) error {
		return errors.New("temporary failure")
	}

	// Simulate 100 concurrent requests. Each request is tried initially,
	// but only ~10 requests are retried, i.e. there will be approximately
	// 110 calls of failingRPC in total.
	for i := 0; i < 100; i++ {
		go func() {
			// Pass a pointer to fooRetryBudget to all Do() calls,
			// i.e. all Do() calls receive a pointer to the same Budget{}.
			// This allows state to be shared between Do() calls.
			if err := Do(ctx, failingRPC, &fooRetryBudget); err != nil {
				log.Println(err)
			}
		}()
	}
}

func TestBudget(t *testing.T) {
	t.Parallel()

	var (
		callsCount int
		callsLock  sync.Mutex
		updateTime time.Time
		rates      []float64
	)

	rpcCall := func(ctx context.Context) error {
		callsLock.Lock()
		defer callsLock.Unlock()

		callsCount++

		now := time.Now()
		if updateTime.IsZero() {
			updateTime = now
		} else if s := now.Sub(updateTime).Seconds(); s >= 0.1 {
			rates = append(rates, float64(callsCount)/s)
			callsCount = 0
			updateTime = now
		}

		if rand.Float64() < 0.5 {
			return errors.New("temporary error")
		}

		return nil
	}

	ctx := context.Background()
	ticker := time.NewTicker(time.Second / 100)
	wg := &sync.WaitGroup{}
	budget := &Budget{
		Rate:  0,
		Ratio: 0.1,
	}

	for i := 0; i < 200; i++ {
		<-ticker.C

		wg.Add(1)
		go func(ctx context.Context) {
			defer wg.Done()
			if err := Do(ctx, rpcCall, budget); err != nil {
				// log.Printf("retry.Do() = %v", err)
			}
		}(ctx)
	}

	wg.Wait()

	sort.Float64s(rates)

	if got, want := rates[len(rates)/2], 110.0; math.Abs(got-want) > 1.0 {
		t.Errorf("got median rate %g, want %g", got, want)
	}
}

func TestMovingRate(t *testing.T) {
	cases := []struct {
		calls      []int
		wantCount  float64
		wantSecond float64
	}{
		{
			calls:      []int{5},
			wantCount:  5,
			wantSecond: 0.2,
		},
		{
			calls:      []int{5, 3},
			wantCount:  8,
			wantSecond: 1.2,
		},
		{
			calls:      []int{5, 5, 1},
			wantCount:  11,
			wantSecond: 2.2,
		},
		{
			calls:      []int{5, 5, 5, 5, 5, 5, 5, 5, 5, 1},
			wantCount:  9*5 + 1,
			wantSecond: 9.2,
		},
		{
			calls: []int{
				5, // partial value
				5, 5, 5, 5, 5, 5, 5, 5, 5, 1},
			wantCount:  5*.8 + 9*5 + 1,
			wantSecond: 10,
		},
		{
			calls: []int{
				1000000, // partial value
				2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
			},
			wantCount:  1000000*.8 + 20,
			wantSecond: 10.0,
		},
		{
			calls: []int{
				2, 2, 2, 2, // old
				5, // partial
				1, 1, 1, 1, 0, 0, 0, 0, 0, 1,
			},
			wantCount:  5*.8 + 5,
			wantSecond: 10.0,
		},
	}

	for _, c := range cases {
		mr := &movingRate{
			BucketLength: time.Second,
			BucketNum:    10,
		}

		tm := time.Date(2018, time.February, 22, 22, 24, 53, 200000000, time.UTC)
		for _, n := range c.calls {
			tm = tm.Add(mr.BucketLength)
			for j := 0; j < n; j++ {
				mr.Add(tm, 1)
			}
		}

		t.Logf("BEFORE mr = %+v", mr)

		if got, want := mr.count(), c.wantCount; got != want {
			t.Errorf("mr.count() = %g, want %g", got, want)
		}

		if got, want := mr.second(), c.wantSecond; got != want {
			t.Errorf("mr.second() = %g, want %g", got, want)
		}

		if got, want := mr.Rate(tm), c.wantCount/c.wantSecond; got != want {
			t.Errorf("mr.Rate(%v) = %g, want %g (= %g/%g)", tm, got, want, c.wantCount, c.wantSecond)
		}

		t.Logf("AFTER  mr = %+v", mr)
	}
}
