package ownmodel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

// twin marks a test as the twin of upstream cases: ids are pytest node ids from
// port/twins/system-one-adapter-python.txt. skipTwin records cases with no Go counterpart.
func twin(t testing.TB, ids ...string) { t.Helper() }

func skipTwin(t testing.TB, reason string, ids ...string) {
	t.Helper()
	t.Skip(reason)
}

func ptr[T any](v T) *T { return &v }

// scripted is the analog of the oracle's _ScriptedProvider: it answers with a scripted
// sequence of payloads (encoded to JSON), raw strings, Results and errors; the last step
// repeats once the script is exhausted.
type scripted struct {
	mu         sync.Mutex
	steps      []any
	usage      [2]int
	calls      [][]Message
	structured []bool
	schemas    []map[string]any
}

func newScripted(steps ...any) *scripted { return &scripted{steps: steps, usage: [2]int{11, 7}} }

func (s *scripted) Name() string { return "fake-model" }

func (s *scripted) Complete(ctx context.Context, req Request) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, append([]Message(nil), req.Messages...))
	s.structured = append(s.structured, req.Structured)
	s.schemas = append(s.schemas, req.Schema)
	i := len(s.calls) - 1
	if i >= len(s.steps) {
		i = len(s.steps) - 1
	}
	switch step := s.steps[i].(type) {
	case error:
		return Result{}, step
	case Result:
		return step, nil
	case string:
		return Result{Text: step, InputTokens: ptr(s.usage[0]), OutputTokens: ptr(s.usage[1])}, nil
	default:
		raw, err := json.Marshal(step)
		if err != nil {
			panic(err)
		}
		return Result{Text: string(raw), InputTokens: ptr(s.usage[0]), OutputTokens: ptr(s.usage[1])}, nil
	}
}

func (s *scripted) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func (s *scripted) lastCall() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[len(s.calls)-1]
}

// providerError is the error a real model source returns after translating an HTTP failure.
func providerError(status int) error {
	return typesafe.NewAPIError(status, map[string]any{"message": "unavailable"}, http.Header{})
}

func mustNew(t testing.TB, opts Options) *Backend {
	t.Helper()
	b, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return b
}

func evaluate(t testing.TB, b *Backend, state any, qs typesafe.Questions, opts *typesafe.RequestOptions) (*Evaluation, error) {
	t.Helper()
	return b.Evaluate(context.Background(), typesafe.SystemOneRequest{State: typesafe.EntryOf(state), Questions: qs}, opts)
}

func eq[T any](t testing.TB, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func noErr(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error %T: %v", err, err)
	}
}

func contains(t testing.TB, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Fatalf("%q does not contain %q", s, sub)
	}
}

func mustAs[T any](t testing.TB, err error) T {
	t.Helper()
	var target T
	if !errors.As(err, &target) {
		t.Fatalf("error %T (%v) is not %T", err, err, target)
	}
	return target
}

func questionSet() typesafe.Questions {
	return typesafe.Questions{
		typesafe.Ask("positive", typesafe.Noul("The review is positive.")),
		typesafe.Ask("stars", typesafe.Score("Rating.", "Bad.", "Good.")),
		typesafe.Ask("genre", typesafe.Choice("Genre.", typesafe.Opt("fiction", "A story."), typesafe.Opt("nonfiction", "Facts."))),
	}
}

func answerNoul(name string) typesafe.Questions {
	return typesafe.Questions{typesafe.Ask(name, typesafe.Noul("The review is positive."))}
}

func categories(reasons []RetryReason) []string {
	out := []string{}
	for _, r := range reasons {
		out = append(out, r.Category)
	}
	return out
}

func fastRetry(max int) typesafe.RetryOverrides {
	return typesafe.RetryOverrides{MaxRetries: typesafe.Ptr(max), BackoffInitial: typesafe.Ptr(durMS(1)), BackoffJitter: typesafe.Ptr(0.0)}
}

func durMS(n int) time.Duration { return time.Duration(n) * time.Millisecond }
