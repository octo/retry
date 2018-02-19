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
