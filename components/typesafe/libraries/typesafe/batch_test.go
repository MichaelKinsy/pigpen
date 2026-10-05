package typesafe

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type evalFunc func(ctx context.Context, req SystemOneRequest, opts *RequestOptions) (*SystemOneResult, error)

func (f evalFunc) SystemOne(ctx context.Context, req SystemOneRequest, opts *RequestOptions) (*SystemOneResult, error) {
	return f(ctx, req, opts)
}

func batchItems(n int) []BatchItem {
	items := make([]BatchItem, n)
	for i := range items {
		items[i] = BatchItem{Request: SystemOneRequest{Model: string(rune('a' + i)), Questions: Questions{Ask("q", Noul("?"))}}}
	}
	return items
}

func TestEvaluateBatch_ReturnsOneResultPerItemInInputOrder(t *testing.T) {
	ev := evalFunc(func(ctx context.Context, req SystemOneRequest, _ *RequestOptions) (*SystemOneResult, error) {
		// Later items finish first.
		time.Sleep(time.Duration(10-int(req.Model[0]-'a')) * time.Millisecond)
		return &SystemOneResult{Model: req.Model}, nil
	})
	out := EvaluateBatch(context.Background(), ev, batchItems(8), BatchOptions{Concurrency: 8})
	eq(t, len(out), 8)
	for i, r := range out {
		eq(t, r.Index, i)
		noErr(t, r.Err)
		eq(t, r.Result.Model, string(rune('a'+i)))
	}
}

func TestEvaluateBatch_BoundsConcurrency(t *testing.T) {
	var inflight, peak atomic.Int32
	ev := evalFunc(func(ctx context.Context, req SystemOneRequest, _ *RequestOptions) (*SystemOneResult, error) {
		n := inflight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		inflight.Add(-1)
		return &SystemOneResult{}, nil
	})
	EvaluateBatch(context.Background(), ev, batchItems(12), BatchOptions{Concurrency: 3})
	if peak.Load() > 3 || peak.Load() < 2 {
		t.Fatalf("peak concurrency %d, want 2..3", peak.Load())
	}
	peak.Store(0)
	EvaluateBatch(context.Background(), ev, batchItems(20), BatchOptions{})
	if peak.Load() > DefaultBatchConcurrency {
		t.Fatalf("default concurrency exceeded: %d", peak.Load())
	}
}

func TestEvaluateBatch_AnItemsFailureDoesNotStopTheOthers(t *testing.T) {
	boom := errors.New("boom")
	ev := evalFunc(func(ctx context.Context, req SystemOneRequest, _ *RequestOptions) (*SystemOneResult, error) {
		if req.Model == "b" {
			return nil, boom
		}
		return &SystemOneResult{Model: req.Model}, nil
	})
	out := EvaluateBatch(context.Background(), ev, batchItems(3), BatchOptions{Concurrency: 1})
	noErr(t, out[0].Err)
	if !errors.Is(out[1].Err, boom) || out[1].Result != nil {
		t.Fatalf("item 1: %+v", out[1])
	}
	noErr(t, out[2].Err)
}

func TestEvaluateBatch_ContextEndAbortsItemsNotYetStarted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var started atomic.Int32
	ev := evalFunc(func(ctx context.Context, req SystemOneRequest, _ *RequestOptions) (*SystemOneResult, error) {
		if started.Add(1) == 1 {
			cancel()
		}
		return &SystemOneResult{}, nil
	})
	out := EvaluateBatch(ctx, ev, batchItems(6), BatchOptions{Concurrency: 1})
	noErr(t, out[0].Err)
	for _, r := range out[1:] {
		mustAs[*APIUserAbortError](t, r.Err)
		if !errors.Is(r.Err, context.Canceled) {
			t.Fatalf("abort must carry the context error: %v", r.Err)
		}
	}
	eq(t, int(started.Load()), 1)
}

func TestEvaluateBatch_APanicBecomesTheItemsError(t *testing.T) {
	ev := evalFunc(func(ctx context.Context, req SystemOneRequest, _ *RequestOptions) (*SystemOneResult, error) {
		if req.Model == "a" {
			panic("kaboom")
		}
		return &SystemOneResult{}, nil
	})
	out := EvaluateBatch(context.Background(), ev, batchItems(2), BatchOptions{})
	if out[0].Err == nil {
		t.Fatal("panic swallowed")
	}
	contains(t, out[0].Err.Error(), "kaboom")
	noErr(t, out[1].Err)
}

func TestEvaluateBatch_EmptyAndPerItemOptions(t *testing.T) {
	eq(t, len(EvaluateBatch(context.Background(), evalFunc(nil), nil, BatchOptions{})), 0)
	var seen sync.Map
	ev := evalFunc(func(ctx context.Context, req SystemOneRequest, o *RequestOptions) (*SystemOneResult, error) {
		seen.Store(req.Model, o)
		return &SystemOneResult{}, nil
	})
	items := batchItems(2)
	items[1].Options = &RequestOptions{Timeout: time.Second}
	EvaluateBatch(context.Background(), ev, items, BatchOptions{})
	if o, _ := seen.Load("a"); o.(*RequestOptions) != nil {
		t.Fatal("no options for item a")
	}
	if o, _ := seen.Load("b"); o.(*RequestOptions).Timeout != time.Second {
		t.Fatal("options for item b not passed")
	}
}
