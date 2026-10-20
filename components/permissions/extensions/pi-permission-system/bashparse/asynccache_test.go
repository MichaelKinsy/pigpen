package bashparse

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// Twins of test/access-intent/bash/async-cache.test.ts. The original memoizes a promise; the Go
// memo runs the factory synchronously for the first caller and shares that outcome with callers
// that arrive meanwhile, so each case keeps its observable claim: how often the factory runs, what
// each caller sees.

const fileAsyncCache = "access-intent/bash/async-cache"

func TestMemoizeAsyncWithRetry(t *testing.T) {
	tw(t, fileAsyncCache, "invokes the factory once and shares the resolved value across calls", func(t *testing.T) {
		var calls atomic.Int32
		memoized := memoizeWithRetry(func() (int, error) { calls.Add(1); return 42, nil })

		results := make([]int, 3)
		var wg sync.WaitGroup
		for i := range results {
			wg.Add(1)
			go func() {
				defer wg.Done()
				results[i], _ = memoized()
			}()
		}
		wg.Wait()

		for _, r := range results {
			if r != 42 {
				t.Errorf("results = %v", results)
			}
		}
		if calls.Load() != 1 {
			t.Errorf("factory ran %d times", calls.Load())
		}
	})

	tw(t, fileAsyncCache, "caches the same promise instance on success", func(t *testing.T) {
		// The Go analogue of "the same promise": the very same value comes back.
		type parser struct{ name string }
		var calls int
		memoized := memoizeWithRetry(func() (*parser, error) { calls++; return &parser{"parser"}, nil })

		a, _ := memoized()
		b, _ := memoized()
		if a != b || calls != 1 {
			t.Errorf("a == b: %v, factory ran %d times", a == b, calls)
		}
	})

	tw(t, fileAsyncCache, "surfaces the rejection to the caller each time the factory fails", func(t *testing.T) {
		failure := errors.New("init failed")
		memoized := memoizeWithRetry(func() (int, error) { return 0, failure })

		for i := 0; i < 2; i++ {
			if _, err := memoized(); !errors.Is(err, failure) {
				t.Errorf("call %d: err = %v", i, err)
			}
		}
	})

	tw(t, fileAsyncCache, "drops a rejected result so the next call re-invokes the factory", func(t *testing.T) {
		calls := 0
		memoized := memoizeWithRetry(func() (int, error) {
			calls++
			if calls == 1 {
				return 0, errors.New("transient")
			}
			return 7, nil
		})

		if _, err := memoized(); err == nil || err.Error() != "transient" {
			t.Fatalf("first call err = %v", err)
		}
		recovered, err := memoized()
		if err != nil || recovered != 7 || calls != 2 {
			t.Errorf("recovered = %d, err = %v, factory ran %d times", recovered, err, calls)
		}
	})
}
