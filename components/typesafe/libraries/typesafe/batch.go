package typesafe

import (
	"context"
	"sync"
)

// BatchItem is one request of a batch.
type BatchItem struct {
	Request SystemOneRequest
	Options *RequestOptions
}

// BatchResult is the outcome of one item; exactly one of Result and Err is set.
type BatchResult struct {
	// Index is the position of the item in the input.
	Index  int
	Result *SystemOneResult
	Err    error
}

// BatchOptions configure [EvaluateBatch].
type BatchOptions struct {
	// Concurrency is the number of requests in flight; zero means DefaultBatchConcurrency.
	Concurrency int
}

// DefaultBatchConcurrency is the number of in-flight requests when none is set.
const DefaultBatchConcurrency = 4

// EvaluateBatch runs the items through ev with bounded concurrency and returns one
// result per item, in input order. An item's failure is its own Err and does not stop
// the others; when ctx ends, items not yet started get an *APIUserAbortError. A
// panic in ev becomes that item's error. The TypeScript SDK has no batch call: this
// is a helper added for the extensions' batched evaluate tools.
func EvaluateBatch(ctx context.Context, ev Evaluator, items []BatchItem, opts BatchOptions) []BatchResult {
	results := make([]BatchResult, len(items))
	for i := range results {
		results[i].Index = i
	}
	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = DefaultBatchConcurrency
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	abortFrom := func(i int) {
		for ; i < len(items); i++ {
			results[i].Err = newAbortError(context.Cause(ctx))
		}
	}
loop:
	for i := range items {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			abortFrom(i)
			break loop
		}
		if ctx.Err() != nil {
			<-sem
			abortFrom(i)
			break loop
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					results[i].Result = nil
					results[i].Err = errorf("The evaluator panicked: %v", r)
				}
			}()
			results[i].Result, results[i].Err = ev.SystemOne(ctx, items[i].Request, items[i].Options)
		}(i)
	}
	wg.Wait()
	return results
}
