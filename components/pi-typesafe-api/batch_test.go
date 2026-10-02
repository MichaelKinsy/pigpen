package pitypesafe

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

// noulAnswering answers every question with a fixed Noul probability, so merging is observable per question id.
func noulAnswering(counter *atomic.Int32) doerFunc {
	return func(r *http.Request) (*http.Response, error) {
		counter.Add(1)
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Questions json.RawMessage `json:"questions"`
		}
		_ = json.Unmarshal(body, &req)
		questions, _ := typesafe.ParseQuestions(req.Questions)
		answers := map[string]any{}
		for _, q := range questions {
			answers[q.Name] = map[string]any{"type": "noul", "noul": 0.75}
		}
		return jsonResponse(200, map[string]any{"model": "jev-test", "answers": answers, "usage": map[string]any{"input_tokens": 10, "output_tokens": 0}}, nil), nil
	}
}

func batchClient(t *testing.T, name string, opts Options, counter *atomic.Int32) *TypeSafe {
	t.Helper()
	isolate(t)
	t.Setenv("TYPESAFE_API_KEY", "batch-test-key")
	opts.Ledger = OpenUsageLedger(LedgerOptions{Path: filepath.Join(t.TempDir(), name+".json")})
	opts.HTTPClient = noulAnswering(counter)
	c, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestBatch(t *testing.T) {
	tw(t, "batch", "fan-out bounds concurrency, preserves order, and never throws", func(t *testing.T) {
		var inFlight, peak atomic.Int32
		results := FanOut(context.Background(), []int{1, 2, 3, 4, 5, 6, 7}, func(_ context.Context, v int, _ int) (int, error) {
			n := inFlight.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			inFlight.Add(-1)
			if v == 4 {
				return 0, errors.New("bad 4")
			}
			return v * 2, nil
		}, FanOutOptions{Concurrency: 3})
		if peak.Load() != 3 {
			t.Errorf("peak = %d", peak.Load())
		}
		var got []any
		for i, r := range results {
			if r.Index != i {
				t.Errorf("index %d = %d", i, r.Index)
			}
			if r.OK {
				got = append(got, r.Value)
			} else {
				got = append(got, "failed")
			}
		}
		if !reflect.DeepEqual(got, []any{2, 4, 6, "failed", 10, 12, 14}) || results[3].Skipped {
			t.Fatalf("results = %v", got)
		}
	})
	tw(t, "batch", "fan-out stops launching after a stop rule and marks the rest skipped", func(t *testing.T) {
		var started []int
		results := FanOut(context.Background(), []int{0, 1, 2, 3, 4, 5}, func(_ context.Context, v int, _ int) (int, error) {
			started = append(started, v)
			if v == 2 {
				return 0, newError(CodeBudget, "cap reached")
			}
			return v, nil
		}, FanOutOptions{Concurrency: 1, StopOn: func(err error) bool { return hasCode(err, CodeBudget) }})
		skipped := 0
		for _, r := range results {
			if !r.OK && r.Skipped {
				skipped++
			}
		}
		if !reflect.DeepEqual(started, []int{0, 1, 2}) || skipped != 3 || results[2].Skipped || results[2].OK {
			t.Fatalf("started=%v skipped=%d", started, skipped)
		}
	})
	tw(t, "batch", "fan-out stops launching once the caller's signal aborts", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		results := FanOut(ctx, []int{0, 1, 2, 3}, func(_ context.Context, v int, _ int) (int, error) {
			if v == 1 {
				cancel()
			}
			return v, nil
		}, FanOutOptions{Concurrency: 1})
		if !results[0].OK || !results[1].OK || results[2].OK || !results[2].Skipped || !hasCode(results[2].Err, CodeAborted) {
			t.Fatalf("results = %+v", results)
		}
	})
	tw(t, "batch", "the splitter is pure: a request that already fits is one chunk, unchanged", func(t *testing.T) {
		req := sampleRequest()
		if got := ChunkRequest(req, 0); len(got) != 1 || !reflect.DeepEqual(got[0], req) {
			t.Fatalf("chunks = %+v", got)
		}
		// No validation here: an invalid request is passed through and fails once, per chunk, at admission.
		invalid := typesafe.SystemOneRequest{State: typesafe.Text("synthetic")}
		if got := ChunkRequest(invalid, 0); len(got) != 1 || !reflect.DeepEqual(got[0], invalid) {
			t.Fatalf("invalid = %+v", got)
		}
	})
	tw(t, "batch", "an invalid request is one settled failure, not a thrown error", func(t *testing.T) {
		var calls atomic.Int32
		c := batchClient(t, "invalid", Options{}, &calls)
		invalid := typesafe.SystemOneRequest{State: typesafe.Text("synthetic")}
		batch := c.EvaluateMany(context.Background(), []typesafe.SystemOneRequest{invalid}, BatchOptions{})
		if calls.Load() != 0 || batch.OK || len(batch.Results) != 1 || batch.Results[0].OK || !hasCode(batch.Results[0].Err, CodeValidation) || batch.Results[0].Skipped {
			t.Fatalf("batch = %+v", batch)
		}
		if _, err := c.Evaluate(context.Background(), invalid); !hasCode(err, CodeValidation) {
			t.Fatalf("err = %v", err)
		}
	})
	tw(t, "batch", "more questions than one request may carry are split in order", func(t *testing.T) {
		questions := manyQuestions(DefaultMaxQuestions + 3)
		chunks := ChunkRequest(typesafe.SystemOneRequest{State: typesafe.Text("many"), Questions: questions}, 0)
		if len(chunks) != 2 || len(chunks[0].Questions) != DefaultMaxQuestions || len(chunks[1].Questions) != 3 {
			t.Fatalf("chunks = %d", len(chunks))
		}
		var ids, want []string
		for _, c := range chunks {
			for _, q := range c.Questions {
				ids = append(ids, q.Name)
			}
			if c.State.Data() != "many" {
				t.Error("the state is repeated in each chunk")
			}
		}
		for _, q := range questions {
			want = append(want, q.Name)
		}
		if !reflect.DeepEqual(ids, want) {
			t.Fatalf("order = %v", ids)
		}
	})
	tw(t, "batch", "evaluateAll spends one request per chunk and merges answers, usage, and model", func(t *testing.T) {
		var calls atomic.Int32
		c := batchClient(t, "many", Options{}, &calls)
		batch := c.EvaluateAll(context.Background(), typesafe.SystemOneRequest{State: typesafe.Text("many"), Questions: manyQuestions(DefaultMaxQuestions + 2)}, BatchOptions{Concurrency: 2})
		if calls.Load() != 2 || !batch.OK || batch.Failures != 0 || batch.Skipped != 0 || len(batch.Answers) != DefaultMaxQuestions+2 || batch.Model != "jev-test" || batch.Usage.InputTokens != 20 || c.GetUsage().RequestsSucceeded != 2 {
			t.Fatalf("batch = %+v calls=%d", batch, calls.Load())
		}
		if len(batch.AnswerOrder) != DefaultMaxQuestions+2 || batch.AnswerOrder[0] != "q0" || batch.AnswerOrder[DefaultMaxQuestions+1] != "q"+itoa(DefaultMaxQuestions+1) {
			t.Errorf("order = %v", batch.AnswerOrder)
		}
	})
	tw(t, "batch", "evaluateMany reports per-request failures and stops submitting after a budget error", func(t *testing.T) {
		var calls atomic.Int32
		c := batchClient(t, "budget", Options{MaxRequests: 1}, &calls)
		req := sampleRequest()
		batch := c.EvaluateMany(context.Background(), []typesafe.SystemOneRequest{req, req, req}, BatchOptions{Concurrency: 1})
		// One request succeeded, one failed on the session cap, and the third was never submitted.
		if calls.Load() != 1 || batch.OK || batch.Failures != 2 || batch.Skipped != 1 || !batch.Results[0].OK || len(batch.Answers) != 1 || batch.ElapsedMs < 0 {
			t.Fatalf("batch = %+v calls=%d", batch, calls.Load())
		}
	})
	tw(t, "batch", "a daily cap stops the batch with the cap named", func(t *testing.T) {
		var calls atomic.Int32
		c := batchClient(t, "day-cap", Options{MaxRequestsPerDay: 1}, &calls)
		req := sampleRequest()
		batch := c.EvaluateMany(context.Background(), []typesafe.SystemOneRequest{req, req}, BatchOptions{})
		// JavaScript runs the first request to its cap check before the second starts; two goroutines race for the
		// one allowed request, so which of the two is refused is not fixed. Exactly one is, and it names the cap.
		var refused *Settled[*Evaluation]
		for i := range batch.Results {
			if !batch.Results[i].OK {
				refused = &batch.Results[i]
			}
		}
		if calls.Load() != 1 || refused == nil || batch.Failures != 1 || !hasCode(refused.Err, CodeBudget) || !contains(refused.Err.Error(), "daily request cap") {
			t.Fatalf("batch = %+v calls=%d", batch, calls.Load())
		}
	})
}

var _ sync.Mutex
