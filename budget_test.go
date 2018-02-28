package retry

import (
	"context"
	"errors"
	"log"
	"sync"
	"testing"
	"time"
)

func ExampleBudget() {
	ctx := context.Background()

	// fooRetryBudget is a global variable holding the state of foo's retry budget.
	var fooRetryBudget = Budget{
		Rate:  1.0,
		Burst: 10,
	}

	// failingRPC is a fake RPC call simulating a permanent backend
	// failure.
	failingRPC := func(_ context.Context) error {
		return errors.New("permanent failure")
	}

	// Simulate 100 concurrent requests. Each request is tried initially,
	// but only 10 requests are retried, i.e. there will be 110 calls of
	// failingRPC in total.
	for i := 0; i < 100; i++ {
		go func() {
			// Pass a pointer to fooRetryBudget to all Do() calls,
			// i.e. all Do() calls receive a the same Budget{}.
			// This allows state to be shared between Do() calls.
			if err := Do(ctx, failingRPC, &fooRetryBudget); err != nil {
				log.Println(err)
			}
		}()
	}
}

func TestBudget(t *testing.T) {
	ctx := context.Background()

	b := &Budget{
		Rate:  60,
		Burst: 10,
	}

	rpcCalls := 0
	rpcCallsLock := &sync.Mutex{}
	failingRPC := func(_ context.Context) error {
		rpcCallsLock.Lock()
		defer rpcCallsLock.Unlock()

		rpcCalls++

		return errors.New("permanent failure")
	}

	wg := &sync.WaitGroup{}
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			Do(ctx, failingRPC, b,
				ExpBackoff{
					Base:   time.Millisecond,
					Max:    8 * time.Millisecond,
					Factor: 2.0,
				})
		}()
	}

	wg.Wait()
	if rpcCalls != 110 {
		t.Errorf("rpcCalls = %d, want 110", rpcCalls)
	}
}

func TestMovingRate(t *testing.T) {
	cases := []struct {
		calls []int
		want  float64
	}{
		{
			calls: []int{5},
			want:  0.0,
		},
		{
			calls: []int{5, 3},
			want:  5.0,
		},
		{
			calls: []int{5, 3, 1},
			want:  4.0,
		},
		{
			calls: []int{2, 2, 2, 2, 2, 2, 2, 2, 2, 2},
			want:  2.0,
		},
		{
			calls: []int{
				2, 0, 2, 0, 2, 0, 2, 0, 2, 0, // history
				100, // staging
			},
			want: 1.0,
		},
		{
			calls: []int{
				1000000,                      // old
				2, 2, 2, 2, 2, 2, 2, 2, 2, 2, // history
				1000000, // staging
			},
			want: 2.0,
		},
		{
			calls: []int{
				2, 2, 2, 2, 2, // old
				1, 1, 1, 1, 1, 0, 0, 0, 0, 0, // history
				0, // ts=15 staging
			},
			want: 0.5,
		},
	}

	for _, c := range cases {
		mr := &movingRate{
			historySize: 10,
		}

		var tm time.Time
		for i, n := range c.calls {
			tm = time.Date(2018, time.February, 22, 22, 24, 53, 0, time.UTC).Add(time.Duration(i) * time.Second)
			for j := 0; j < n; j++ {
				mr.Add(tm, 1)
			}
		}

		t.Logf("BEFORE mr = %+v", mr)

		if got := mr.Rate(tm); got != c.want {
			t.Errorf("mr.Rate(%v) = %g, want %g", tm, got, c.want)
		}

		t.Logf("AFTER  mr = %+v", mr)
	}
}
