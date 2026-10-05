package typesafe

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Added by the mutation check (port/mutations.json: batch-default-concurrency-one): with no
// Concurrency set, DefaultBatchConcurrency requests run at once.
func TestEvaluateBatch_DefaultConcurrencyRunsSeveralRequestsAtOnce(t *testing.T) {
	var mu sync.Mutex
	inflight, peak := 0, 0
	release := make(chan struct{})
	var once sync.Once
	ev := evalFunc(func(ctx context.Context, req SystemOneRequest, _ *RequestOptions) (*SystemOneResult, error) {
		mu.Lock()
		inflight++
		if inflight > peak {
			peak = inflight
		}
		if inflight == DefaultBatchConcurrency {
			once.Do(func() { close(release) })
		}
		mu.Unlock()
		select {
		case <-release:
		case <-time.After(2 * time.Second):
		}
		mu.Lock()
		inflight--
		mu.Unlock()
		return &SystemOneResult{}, nil
	})
	out := EvaluateBatch(context.Background(), ev, batchItems(2*DefaultBatchConcurrency), BatchOptions{})
	eq(t, len(out), 2*DefaultBatchConcurrency)
	eq(t, peak, DefaultBatchConcurrency)
}
