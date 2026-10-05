package pitypesafe

import (
	"context"
	"sync"
	"time"

	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

// DefaultConcurrency is the default number of requests in flight. TypeSafe answers in isolation, so a small pool is enough.
const DefaultConcurrency = 4

// Settled is one item's outcome. Skipped marks work that was never started because of a cancellation or a stop rule.
type Settled[T any] struct {
	OK      bool
	Index   int
	Value   T
	Err     error
	Skipped bool
}

// FanOutOptions configure FanOut.
type FanOutOptions struct {
	// Concurrency is the number of requests in flight at once. Default: DefaultConcurrency.
	Concurrency int
	// StopOn stops launching new work once it returns true for a failure, for example a budget error.
	StopOn func(error) bool
}

// FanOut runs worker over items with bounded concurrency, preserving input order. It never fails: every item
// comes back as a settled result. A cancelled ctx stops starting new work; in-flight work still finishes.
// This is the pool the batching methods use, exported so script authors stop hand-rolling one.
func FanOut[I, O any](ctx context.Context, items []I, worker func(ctx context.Context, item I, index int) (O, error), opts FanOutOptions) []Settled[O] {
	concurrency := opts.Concurrency
	if concurrency < 1 {
		concurrency = DefaultConcurrency
	}
	results := make([]*Settled[O], len(items))
	var mu sync.Mutex
	next := 0
	stopped := false
	run := func() {
		for {
			mu.Lock()
			if stopped {
				mu.Unlock()
				return
			}
			index := next
			next++
			if index >= len(items) {
				mu.Unlock()
				return
			}
			if ctx.Err() != nil {
				stopped = true
				mu.Unlock()
				return
			}
			mu.Unlock()
			value, err := worker(ctx, items[index], index)
			mu.Lock()
			if err != nil {
				results[index] = &Settled[O]{Index: index, Err: err}
				if opts.StopOn != nil && opts.StopOn(err) {
					stopped = true
				}
			} else {
				results[index] = &Settled[O]{OK: true, Index: index, Value: value}
			}
			mu.Unlock()
		}
	}
	var wg sync.WaitGroup
	for range min(concurrency, len(items)) {
		wg.Add(1)
		go func() { defer wg.Done(); run() }()
	}
	wg.Wait()
	reason := "TypeSafe batch stopped after a failed request; this request was not submitted."
	if ctx.Err() != nil {
		reason = "TypeSafe batch cancelled before this request was submitted."
	}
	out := make([]Settled[O], len(items))
	for i := range items {
		if results[i] != nil {
			out[i] = *results[i]
		} else {
			out[i] = Settled[O]{Index: i, Err: newError(CodeAborted, reason), Skipped: true}
		}
	}
	return out
}

// BatchOptions configure EvaluateMany and EvaluateAll.
type BatchOptions struct {
	// Concurrency is the number of requests in flight at once. Default: DefaultConcurrency.
	Concurrency int
	// MaxQuestions is the chunk size for EvaluateAll. Default: DefaultMaxQuestions.
	MaxQuestions int
}

// BatchEvaluation holds per-request outcomes plus the merged view callers usually want.
type BatchEvaluation struct {
	// OK is true when every request succeeded.
	OK bool
	// Results are per-request outcomes in input order.
	Results []Settled[*Evaluation]
	// Failures counts failures, including requests that were never submitted.
	Failures int
	// Skipped counts work never started, because of a cancellation or a budget stop.
	Skipped int
	// Answers are merged in input order; a repeated question id keeps the last answer. Empty when nothing succeeded.
	Answers map[string]typesafe.Answer
	// AnswerOrder lists the answer ids in the order they were first merged.
	AnswerOrder []string
	// Model is the model of the first successful request, when there is one.
	Model string
	// Usage is summed over the requests that succeeded.
	Usage typesafe.Usage
	// Elapsed is the wall-clock time for the whole batch.
	ElapsedMs int64
}

func summarize(results []Settled[*Evaluation], elapsed time.Duration) *BatchEvaluation {
	out := &BatchEvaluation{Results: results, Answers: map[string]typesafe.Answer{}, ElapsedMs: elapsed.Milliseconds()}
	succeeded := 0
	for _, r := range results {
		if !r.OK {
			if r.Skipped {
				out.Skipped++
			}
			continue
		}
		succeeded++
		for _, id := range r.Value.Order {
			if _, seen := out.Answers[id]; !seen {
				out.AnswerOrder = append(out.AnswerOrder, id)
			}
			out.Answers[id] = r.Value.Answers[id]
		}
		out.Usage.InputTokens += r.Value.Usage.InputTokens
		out.Usage.OutputTokens += r.Value.Usage.OutputTokens
		if out.Model == "" {
			out.Model = r.Value.Model
		}
	}
	out.OK = succeeded == len(results)
	out.Failures = len(results) - succeeded
	return out
}

// stopsBatch is a failure worth stopping the batch for: no more requests will be accepted, or the caller cancelled.
func stopsBatch(err error) bool {
	ie, ok := err.(*IntegrationError)
	return ok && (ie.Code == CodeBudget || ie.Code == CodeAborted)
}

// Evaluator is the part of a TypeSafe client the batching functions use.
type Evaluator interface {
	Evaluate(ctx context.Context, request typesafe.SystemOneRequest) (*Evaluation, error)
}

// EvaluateMany sends several requests with bounded concurrency, in input order, and merges what came back.
// Each request passes through the same admission as Evaluate, so an invalid request is one settled failure, not
// an error. A budget or cancellation failure stops the rest from being submitted. It never fails.
func EvaluateMany(ctx context.Context, client Evaluator, requests []typesafe.SystemOneRequest, opts BatchOptions) *BatchEvaluation {
	start := time.Now()
	results := FanOut(ctx, requests, func(ctx context.Context, request typesafe.SystemOneRequest, _ int) (*Evaluation, error) {
		return client.Evaluate(ctx, request)
	}, FanOutOptions{Concurrency: opts.Concurrency, StopOn: stopsBatch})
	return summarize(results, time.Since(start))
}

// ChunkRequest splits a request that asks more questions than one request may carry into chunks of at most
// maxQuestions (default DefaultMaxQuestions). Sharing one state across several questions is one request; asking
// more than the per-request limit is the only reason to fan out, and the state is repeated in each chunk. The
// order of questions is preserved. A pure splitter: admission still happens once per chunk, in Evaluate.
func ChunkRequest(request typesafe.SystemOneRequest, maxQuestions int) []typesafe.SystemOneRequest {
	limit := maxQuestions
	if limit < 1 {
		limit = DefaultMaxQuestions
	}
	if len(request.Questions) <= limit {
		return []typesafe.SystemOneRequest{request}
	}
	var chunks []typesafe.SystemOneRequest
	for i := 0; i < len(request.Questions); i += limit {
		chunk := request
		chunk.Questions = append(typesafe.Questions(nil), request.Questions[i:min(i+limit, len(request.Questions))]...)
		chunks = append(chunks, chunk)
	}
	return chunks
}

// EvaluateAll asks any number of questions about one state: chunk to the per-request limit, fan out, and merge
// the answers, usage, and model. Use it when one coherent state carries many independent questions; use Evaluate
// for one request.
func EvaluateAll(ctx context.Context, client Evaluator, request typesafe.SystemOneRequest, opts BatchOptions) *BatchEvaluation {
	return EvaluateMany(ctx, client, ChunkRequest(request, opts.MaxQuestions), opts)
}
