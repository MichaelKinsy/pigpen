package warden

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

var askRequest = Request{State: map[string]any{"text": "hello"}, Questions: Questions{"fine": Noul("Is `text` fine?")}}

// twin: tests/ask.test.ts:12
func TestAskSuccessfulEvaluationComesBackWithAnswersModelAndElapsedTime(t *testing.T) {
	var seen []context.Context
	judge := &recordingJudge{handler: func(Request) (Evaluation, error) {
		return Evaluation{Model: "jev-test", ElapsedMs: 7, Answers: map[string]Answer{"fine": noul(0.9)}}, nil
	}}
	wrapped := judgeFunc(func(ctx context.Context, r Request) (Evaluation, error) {
		seen = append(seen, ctx)
		return judge.Evaluate(ctx, r)
	})
	caller, cancel := context.WithCancel(context.Background())
	result := Ask(caller, wrapped, askRequest, time.Second)
	if !result.OK || result.Answers["fine"].Noul != 0.9 || result.Model != "jev-test" || result.ElapsedMs != 7 {
		t.Fatalf("%+v", result)
	}
	if _, ok := seen[0].Deadline(); !ok || seen[0].Err() != nil && !errors.Is(seen[0].Err(), context.Canceled) {
		t.Fatal("one context covers the timeout and the caller's abort")
	}
	cancel()
	if seen[0].Err() == nil {
		t.Fatal("the caller's abort reaches the request")
	}
	plain := Ask(context.Background(), wrapped, askRequest, time.Second)
	if _, ok := seen[1].Deadline(); !plain.OK || !ok {
		t.Fatal("a timeout is passed even without a caller's cancel")
	}
}

// twin: tests/ask.test.ts:30
func TestAskIntegrationErrorsKeepTheirMessageAndCodeAnythingElseGetsAFixedMessage(t *testing.T) {
	fail := func(err error) AskAnswer {
		return Ask(context.Background(), judgeFunc(func(context.Context, Request) (Evaluation, error) { return Evaluation{}, err }), askRequest, time.Second)
	}
	if got := fail(&IntegrationError{Code: "budget", Message: "TypeSafe request limit reached (1 attempts)."}); got.OK || got.Error != "TypeSafe request limit reached (1 attempts)." || got.ErrorCode != "budget" {
		t.Fatalf("%+v", got)
	}
	if got := fail(&IntegrationError{Code: "timeout", Message: "TypeSafe did not answer in time."}); got.OK || got.ErrorCode != "timeout" {
		t.Fatalf("%+v", got)
	}
	if got := fail(errors.New("upstream body must not leak")); got.OK || got.Error != "TypeSafe request failed." || got.ErrorCode != "" {
		t.Fatalf("%+v", got)
	}
}

func answerSet(items map[string]typesafe.Answer) *typesafe.SystemOneResult {
	return &typesafe.SystemOneResult{Model: "jev-test", Answers: items, Usage: typesafe.Usage{InputTokens: 10, OutputTokens: 2}}
}

func choiceQuestion() Question {
	return Question{Type: "choice", Criteria: map[string]string{"a": "A", "b": "B"}, CriteriaOrder: []string{"a", "b"}}
}

func scoreQuestion() Question { return Question{Type: "score", Levels: []string{"zero", "one", "two"}} }

func TestValidResultAcceptsExactlyTheAskedQuestions(t *testing.T) {
	qs := Questions{"n": Noul("?"), "c": choiceQuestion(), "s": scoreQuestion()}
	good := func() map[string]typesafe.Answer {
		return map[string]typesafe.Answer{
			"n": typesafe.NoulAnswer{Noul: 0.4},
			"c": typesafe.ChoiceAnswer{Choice: "a", Confidence: 0.8, Probabilities: map[string]float64{"a": 0.8, "b": 0.2}},
			"s": typesafe.ScoreAnswer{Score: 1.5, Confidence: 0.7, Legend: map[int]any{0: "zero", 1: "one", 2: "two"}, Probabilities: map[int]float64{0: 0.1, 1: 0.5, 2: 0.4}},
		}
	}
	if !validResult(answerSet(good()), qs) {
		t.Fatal("a well-formed response is a judgment")
	}
	cases := []struct {
		name string
		edit func(m map[string]typesafe.Answer)
		res  func(r *typesafe.SystemOneResult)
	}{
		{"a missing answer", func(m map[string]typesafe.Answer) { delete(m, "n") }, nil},
		{"an extra answer", func(m map[string]typesafe.Answer) { m["x"] = typesafe.NoulAnswer{Noul: 0.1} }, nil},
		{"a wrong type", func(m map[string]typesafe.Answer) {
			m["n"] = typesafe.ChoiceAnswer{Choice: "a", Confidence: 1, Probabilities: map[string]float64{"a": 1, "b": 0}}
		}, nil},
		{"noul above one", func(m map[string]typesafe.Answer) { m["n"] = typesafe.NoulAnswer{Noul: 1.01} }, nil},
		{"noul below zero", func(m map[string]typesafe.Answer) { m["n"] = typesafe.NoulAnswer{Noul: -0.01} }, nil},
		{"a choice outside the criteria", func(m map[string]typesafe.Answer) {
			m["c"] = typesafe.ChoiceAnswer{Choice: "z", Confidence: 0.8, Probabilities: map[string]float64{"a": 0.8, "b": 0.2}}
		}, nil},
		{"choice probabilities over the wrong labels", func(m map[string]typesafe.Answer) {
			m["c"] = typesafe.ChoiceAnswer{Choice: "a", Confidence: 0.8, Probabilities: map[string]float64{"a": 0.8, "z": 0.2}}
		}, nil},
		{"choice probabilities for one label only", func(m map[string]typesafe.Answer) {
			m["c"] = typesafe.ChoiceAnswer{Choice: "a", Confidence: 0.8, Probabilities: map[string]float64{"a": 0.9}}
		}, nil},
		{"choice without probabilities", func(m map[string]typesafe.Answer) { m["c"] = typesafe.ChoiceAnswer{Choice: "a", Confidence: 0.8} }, nil},
		{"choice confidence above one", func(m map[string]typesafe.Answer) {
			m["c"] = typesafe.ChoiceAnswer{Choice: "a", Confidence: 1.5, Probabilities: map[string]float64{"a": 0.8, "b": 0.2}}
		}, nil},
		{"a choice probability above one", func(m map[string]typesafe.Answer) {
			m["c"] = typesafe.ChoiceAnswer{Choice: "a", Confidence: 0.8, Probabilities: map[string]float64{"a": 1.2, "b": 0.2}}
		}, nil},
		{"a score above the last level", func(m map[string]typesafe.Answer) {
			m["s"] = typesafe.ScoreAnswer{Score: 2.1, Confidence: 0.7, Legend: map[int]any{}, Probabilities: map[int]float64{0: 0.1, 1: 0.5, 2: 0.4}}
		}, nil},
		{"a negative score", func(m map[string]typesafe.Answer) {
			m["s"] = typesafe.ScoreAnswer{Score: -0.1, Confidence: 0.7, Legend: map[int]any{}, Probabilities: map[int]float64{0: 0.1, 1: 0.5, 2: 0.4}}
		}, nil},
		{"a score without a legend", func(m map[string]typesafe.Answer) {
			m["s"] = typesafe.ScoreAnswer{Score: 1, Confidence: 0.7, Probabilities: map[int]float64{0: 0.1, 1: 0.5, 2: 0.4}}
		}, nil},
		{"score probabilities over too few levels", func(m map[string]typesafe.Answer) {
			m["s"] = typesafe.ScoreAnswer{Score: 1, Confidence: 0.7, Legend: map[int]any{}, Probabilities: map[int]float64{0: 0.5, 1: 0.5}}
		}, nil},
		{"score probabilities for the wrong levels", func(m map[string]typesafe.Answer) {
			m["s"] = typesafe.ScoreAnswer{Score: 1, Confidence: 0.7, Legend: map[int]any{}, Probabilities: map[int]float64{0: 0.5, 1: 0.5, 7: 0}}
		}, nil},
		{"an unknown answer type", func(m map[string]typesafe.Answer) { m["n"] = typesafe.RawAnswer{Type: "mystery"} }, nil},
		{"no model", nil, func(r *typesafe.SystemOneResult) { r.Model = "" }},
		{"negative usage", nil, func(r *typesafe.SystemOneResult) { r.Usage.InputTokens = -1 }},
		{"no answers at all", nil, func(r *typesafe.SystemOneResult) { r.Answers = nil }},
	}
	for _, c := range cases {
		m := good()
		if c.edit != nil {
			c.edit(m)
		}
		res := answerSet(m)
		if c.res != nil {
			c.res(res)
		}
		if validResult(res, qs) {
			t.Errorf("%s is accepted", c.name)
		}
	}
	// A score exactly at either end, and a choice of the other label, are fine.
	m := good()
	m["s"] = typesafe.ScoreAnswer{Score: 2, Confidence: 0, Legend: map[int]any{}, Probabilities: map[int]float64{0: 0, 1: 0, 2: 1}}
	m["c"] = typesafe.ChoiceAnswer{Choice: "b", Confidence: 1, Probabilities: map[string]float64{"a": 0, "b": 1}}
	if !validResult(answerSet(m), qs) {
		t.Fatal("bounds are inclusive")
	}
	if validResult(nil, qs) {
		t.Fatal("no result")
	}
}

// The original fails such a response as a `response` error with its own message, and the guard fails open.
func TestATypeSafeResponseThatFailsTheAnswerCheckFailsTheRequestAsTheOriginalDoes(t *testing.T) {
	h := startHarness(t, harnessOpts{cfg: on(BackendTypeSafe)})
	h.ts.set(map[string]float64{"irreversible": 0.1})
	h.ts.mu.Lock()
	h.ts.shortAnswers = true
	h.ts.mu.Unlock()
	h.say("user", "run the tests")
	h.start()
	if r, held := blocked(h.toolCall("bash", "c1", map[string]any{"command": "npm test"})); held {
		t.Fatalf("a failed judgment fails open: %s", r)
	}
	var found bool
	for _, n := range h.notices() {
		if strings.Contains(n, unexpectedAnswerMessage) {
			found = true
		}
	}
	if !found {
		t.Fatalf("the user is told with the original's message: %q", h.notices())
	}
	if h.w.statsCopy().Judged != 0 || h.w.statsCopy().Errors != 1 {
		t.Fatalf("%+v", h.w.statsCopy())
	}
}
