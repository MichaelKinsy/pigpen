package warden

// Helpers shared by the twin tests: a scriptable judge in place of pi-typesafe's stub judges
// (tests/guard.test.ts `judge`, tests/action-guard.test.ts `stubJudge`).

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func f(v float64) *float64 { return &v }
func deref(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func text(v string) []ContentPart { return []ContentPart{{Type: "text", Text: v}} }

// recordingJudge records requests and answers through handler.
type recordingJudge struct {
	mu       sync.Mutex
	requests []Request
	handler  func(Request) (Evaluation, error)
	// gate, when non-nil, holds every evaluation until the test closes it.
	gate chan struct{}
}

func (j *recordingJudge) Evaluate(ctx context.Context, request Request) (Evaluation, error) {
	j.mu.Lock()
	j.requests = append(j.requests, request)
	gate := j.gate
	j.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return Evaluation{}, ctx.Err()
		}
	}
	return j.handler(request)
}

func (j *recordingJudge) calls() []Request {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]Request(nil), j.requests...)
}

func (j *recordingJudge) reset() {
	j.mu.Lock()
	j.requests = nil
	j.mu.Unlock()
}

func noul(v float64) Answer { return Answer{Type: "noul", Noul: v} }

// actionAnswers is the base answer set of an action request (guard.test.ts `answers`).
func actionAnswers(irreversible, offTask float64, scope string, mutates *float64) Evaluation {
	a := map[string]Answer{
		"irreversible": noul(irreversible),
		"off_task":     noul(offTask),
		"scope":        {Type: "choice", Choice: scope, Confidence: 0.9},
	}
	if mutates != nil {
		a["mutates"] = noul(*mutates)
	}
	return Evaluation{Model: "jev-test", ElapsedMs: 12, Answers: a}
}

// actionJudge is guard.test.ts `judge(irreversible, offTask, scope?, mutates?)`.
func actionJudge(irreversible, offTask float64, scope string, mutates *float64) *recordingJudge {
	if scope == "" {
		scope = "expected_step"
	}
	return &recordingJudge{handler: func(Request) (Evaluation, error) { return actionAnswers(irreversible, offTask, scope, mutates), nil }}
}

func failingJudge(code string) Judge {
	return &recordingJudge{handler: func(Request) (Evaluation, error) {
		return Evaluation{}, &IntegrationError{Code: code, Message: "synthetic " + code}
	}}
}

func has(q Questions, id string) bool { _, ok := q[id]; return ok }

func mustEqual(t *testing.T, got, want any, msg string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: got %#v, want %#v", msg, got, want)
	}
}

func mustMatch(t *testing.T, s, pattern, msg string) {
	t.Helper()
	if !regexpMatch(pattern, s) {
		t.Fatalf("%s: %q does not match /%s/", msg, s, pattern)
	}
}

func mustNotContain(t *testing.T, s, sub, msg string) {
	t.Helper()
	if strings.Contains(s, sub) {
		t.Fatalf("%s: %q contains %q", msg, s, sub)
	}
}

func hasHit(hits []PatternHit, pred func(PatternHit) bool) bool {
	for _, h := range hits {
		if pred(h) {
			return true
		}
	}
	return false
}

func hitByID(hits []PatternHit, id string) (PatternHit, bool) {
	for _, h := range hits {
		if h.ID == id {
			return h, true
		}
	}
	return PatternHit{}, false
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func regexpMatch(pattern, s string) bool {
	re, err := regexpCompile(pattern)
	if err != nil {
		panic(err)
	}
	return re.MatchString(s)
}

// nth returns the i-th request the judge saw, failing the test instead of panicking when there is none.
func nth(t *testing.T, j *recordingJudge, i int) Request {
	t.Helper()
	calls := j.calls()
	if i >= len(calls) {
		t.Fatalf("the judge saw %d requests, wanted request %d", len(calls), i)
	}
	return calls[i]
}
