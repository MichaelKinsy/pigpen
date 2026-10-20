package pi_goal_x

import (
	"errors"
	"math"
	"regexp"
	"sync"
	"testing"
)

// Twins of tests/goal-evaluation-budget.test.ts. The module under test is the offline evaluation harness
// experiments/live/budget.mjs (EvaluationBudget); it is a test-only harness, so its Go port lives here with the twins.

type evalModel struct {
	provider      string
	id            string
	contextWindow float64
	maxTokens     float64
	cost          map[string]float64 // input, output, cacheRead, cacheWrite
	tiered        bool               // cost.inputTiers / cost.tiers present
}

type evalUsage struct {
	input, output, cacheRead, cacheWrite float64
	cost                                 *float64 // cost.total
}

type evalMessage struct {
	usage        *evalUsage
	errorMessage string
	stopReason   string
}

type evalStream struct {
	result func() (*evalMessage, error)
}

type evalOptions struct{ maxTokens, maxRetries float64 }

type evalRuntime struct {
	streamSimple func(model evalModel, context any, options evalOptions) (*evalStream, error)
}

type evalRequest struct {
	provider, model                      string
	reservation                          float64
	input, output, cacheRead, cacheWrite float64
	cost                                 float64
	uncertain                            bool
	errorMessage, stopReason             string
}

type evaluationBudget struct {
	mu           sync.Mutex
	pendingWG    sync.WaitGroup
	limit        float64
	maxTokens    float64
	spent        float64
	reserved     float64
	requests     []*evalRequest
	caseRequests int
	caseLimit    int
}

type evaluationBudgetOpts struct{ limit, maxTokens *float64 }

func newEvaluationBudget(o evaluationBudgetOpts) (*evaluationBudget, error) {
	limit, maxTokens := 5.0, 8192.0
	if o.limit != nil {
		limit = *o.limit
	}
	if o.maxTokens != nil {
		maxTokens = *o.maxTokens
	}
	if math.IsNaN(limit) || math.IsInf(limit, 0) || limit < 0 || maxTokens != math.Trunc(maxTokens) || math.IsInf(maxTokens, 0) || maxTokens <= 0 {
		return nil, errors.New("Invalid evaluation allowance")
	}
	return &evaluationBudget{limit: limit, maxTokens: maxTokens, caseLimit: 12}, nil
}

func (b *evaluationBudget) reservation(model evalModel) (float64, error) {
	var rates [4]float64
	for i, k := range []string{"input", "output", "cacheRead", "cacheWrite"} {
		r, ok := model.cost[k]
		if !ok || math.IsNaN(r) || math.IsInf(r, 0) || r < 0 {
			return 0, errors.New("Cannot safely price this model")
		}
		rates[i] = r
	}
	if math.IsNaN(model.contextWindow) || math.IsInf(model.contextWindow, 0) || model.contextWindow <= 0 ||
		math.IsNaN(model.maxTokens) || math.IsInf(model.maxTokens, 0) || model.maxTokens <= 0 {
		return 0, errors.New("Cannot safely price this model")
	}
	if model.tiered {
		return 0, errors.New("Tiered pricing requires explicit reservation support")
	}
	return (model.contextWindow*math.Max(rates[0], math.Max(rates[2], rates[3])) + math.Min(model.maxTokens, b.maxTokens)*rates[1]) / 1e6, nil
}

func (b *evaluationBudget) canRunPair(model evalModel) bool {
	r, err := b.reservation(model)
	if err != nil {
		panic(err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.spent+b.reserved+2*float64(b.caseLimit)*r <= b.limit
}

// install wraps runtime.streamSimple so every request reserves its worst-case cost before dispatch.
func (b *evaluationBudget) install(runtime *evalRuntime) {
	original := runtime.streamSimple
	runtime.streamSimple = func(model evalModel, context any, options evalOptions) (*evalStream, error) {
		reserve, err := b.reservation(model)
		if err != nil {
			return nil, err
		}
		b.mu.Lock()
		if b.caseRequests >= b.caseLimit || b.spent+b.reserved+reserve > b.limit {
			b.mu.Unlock()
			return nil, errors.New("Evaluation request/spend ceiling reached before dispatch")
		}
		b.caseRequests++
		b.reserved += reserve
		entry := &evalRequest{provider: model.provider, model: model.id, reservation: reserve}
		b.requests = append(b.requests, entry)
		b.mu.Unlock()
		options.maxTokens = math.Min(model.maxTokens, b.maxTokens)
		options.maxRetries = 0
		stream, err := original(model, context, options)
		if err != nil {
			b.mu.Lock()
			b.reserved -= reserve
			b.spent += reserve
			entry.cost = reserve
			entry.uncertain = true
			b.mu.Unlock()
			return nil, err
		}
		b.pendingWG.Add(1)
		go func() {
			defer b.pendingWG.Done()
			message, err := stream.result()
			b.mu.Lock()
			defer b.mu.Unlock()
			if err != nil {
				b.reserved -= reserve
				b.spent += reserve
				entry.cost = reserve
				entry.uncertain = true
				return
			}
			var u *evalUsage
			if message != nil {
				u = message.usage
			}
			if u != nil {
				entry.input, entry.output, entry.cacheRead, entry.cacheWrite = u.input, u.output, u.cacheRead, u.cacheWrite
			}
			if message != nil {
				entry.errorMessage, entry.stopReason = message.errorMessage, message.stopReason
			}
			// Missing usage on a failed/aborted request is not proof of zero billing.
			entry.cost = reserve
			if u != nil && u.cost != nil && !math.IsNaN(*u.cost) && !math.IsInf(*u.cost, 0) && *u.cost >= 0 && u.input+u.output+u.cacheRead+u.cacheWrite > 0 {
				entry.cost = *u.cost
			}
			if entry.cost == reserve {
				entry.uncertain = true
			}
			b.reserved -= reserve
			b.spent += entry.cost
		}()
		return stream, nil
	}
}

// settle waits for every dispatched request's result to be accounted.
func (b *evaluationBudget) settle() { b.pendingWG.Wait() }

func TestGoalEvaluationBudget(t *testing.T) {
	const f = "goal-evaluation-budget"
	model := evalModel{provider: "fixture", id: "fixture", contextWindow: 1_000_000, maxTokens: 10_000,
		cost: map[string]float64{"input": 1, "output": 2, "cacheRead": 0.1, "cacheWrite": 1}}
	total := 0.000143
	usage := &evalUsage{input: 100, output: 20, cacheRead: 30, cacheWrite: 0, cost: &total}

	tw(t, f, "evaluation allowance reserves concurrent parent/child requests before dispatch", func(t *testing.T) {
		limit := 2.1
		budget, err := newEvaluationBudget(evaluationBudgetOpts{limit: &limit})
		if err != nil {
			t.Fatal(err)
		}
		calls := 0
		var pending []chan *evalMessage
		runtime := &evalRuntime{streamSimple: func(_ evalModel, _ any, options evalOptions) (*evalStream, error) {
			calls++
			assertEq(t, options.maxTokens, 8192.0, "maxTokens")
			assertEq(t, options.maxRetries, 0.0, "maxRetries")
			ch := make(chan *evalMessage, 1)
			pending = append(pending, ch)
			return &evalStream{result: func() (*evalMessage, error) { return <-ch, nil }}, nil
		}}
		budget.install(runtime)
		if _, err := runtime.streamSimple(model, nil, evalOptions{}); err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.streamSimple(model, nil, evalOptions{}); err != nil {
			t.Fatal(err)
		}
		_, err = runtime.streamSimple(model, nil, evalOptions{})
		assertTrue(t, err != nil && regexp.MustCompile(`ceiling`).MatchString(err.Error()), "third request hits the ceiling")
		assertEq(t, calls, 2, "calls")
		assertTrue(t, budget.reserved > 2, "reserved above 2")
		for _, ch := range pending {
			ch <- &evalMessage{usage: usage}
		}
		budget.settle()
		assertEq(t, budget.reserved, 0.0, "reserved released")
		assertEq(t, budget.spent, 0.000286, "spent")
		assertEq(t, budget.requests[0].cacheRead, 30.0, "cacheRead recorded")
	})

	tw(t, f, "unknown billing consumes its reservation and invalid pricing fails closed", func(t *testing.T) {
		limit := 2.0
		budget, err := newEvaluationBudget(evaluationBudgetOpts{limit: &limit})
		if err != nil {
			t.Fatal(err)
		}
		runtime := &evalRuntime{streamSimple: func(evalModel, any, evalOptions) (*evalStream, error) {
			return &evalStream{result: func() (*evalMessage, error) { return &evalMessage{errorMessage: "interrupted"}, nil }}, nil
		}}
		budget.install(runtime)
		if _, err := runtime.streamSimple(model, nil, evalOptions{}); err != nil {
			t.Fatal(err)
		}
		budget.settle()
		want, _ := budget.reservation(model)
		assertEq(t, budget.spent, want, "spent equals the reservation")
		assertEq(t, budget.requests[0].uncertain, true, "uncertain")
		assertEq(t, budget.canRunPair(model), false, "canRunPair")
		unpriced := model
		unpriced.cost = map[string]float64{}
		_, err = runtime.streamSimple(unpriced, nil, evalOptions{})
		assertTrue(t, err != nil && regexp.MustCompile(`price`).MatchString(err.Error()), "invalid pricing fails closed")
		assertEq(t, len(budget.requests), 1, "no request recorded")
		nan := math.NaN()
		_, err = newEvaluationBudget(evaluationBudgetOpts{maxTokens: &nan})
		assertTrue(t, err != nil && regexp.MustCompile(`allowance`).MatchString(err.Error()), "NaN maxTokens rejected")
	})
}
