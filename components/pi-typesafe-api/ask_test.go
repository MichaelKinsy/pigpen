package pitypesafe

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

type judgeFunc func(ctx context.Context, r typesafe.SystemOneRequest) (*Evaluation, error)

func (f judgeFunc) Evaluate(ctx context.Context, r typesafe.SystemOneRequest) (*Evaluation, error) {
	return f(ctx, r)
}

func TestAsk(t *testing.T) {
	tw(t, "ask", "a successful ask returns the typed answers, model, usage, and duration", func(t *testing.T) {
		judge := judgeFunc(func(context.Context, typesafe.SystemOneRequest) (*Evaluation, error) {
			return &Evaluation{SystemOneResult: &typesafe.SystemOneResult{Model: "jev-test", Answers: map[string]typesafe.Answer{"yes": typesafe.NoulAnswer{Noul: 0.9}}, Usage: typesafe.Usage{InputTokens: 12}}, ElapsedMs: 7, Order: []string{"yes"}}, nil
		})
		a := Ask(context.Background(), judge, sampleRequest(), AskOptions{})
		yes, _ := a.Answers["yes"].(typesafe.NoulAnswer)
		if !a.OK || yes.Noul != 0.9 || a.Model != "jev-test" || a.Usage.InputTokens != 12 || a.ElapsedMs != 7 {
			t.Fatalf("answer = %+v", a)
		}
	})
	tw(t, "ask", "a typed failure comes back as data, with pi-typesafe's own code and message", func(t *testing.T) {
		fail := func(err error) Judge {
			return judgeFunc(func(context.Context, typesafe.SystemOneRequest) (*Evaluation, error) { return nil, err })
		}
		budget := Ask(context.Background(), fail(newError(CodeBudget, "TypeSafe request limit reached (1 attempts per client instance).")), sampleRequest(), AskOptions{})
		if !reflect.DeepEqual(budget, AskAnswer{Error: "TypeSafe request limit reached (1 attempts per client instance).", ErrorCode: CodeBudget}) {
			t.Fatalf("budget = %+v", budget)
		}
		e := &IntegrationError{Code: CodeHTTP, Message: "TypeSafe returned HTTP 401. Check TYPESAFE_API_KEY. No automatic retry was made.", Status: 401}
		if h := Ask(context.Background(), fail(e), sampleRequest(), AskOptions{}); h.OK || h.ErrorCode != CodeHTTP {
			t.Fatalf("http = %+v", h)
		}
	})
	tw(t, "ask", "an unknown failure is replaced by a fixed message so nothing from the transport escapes", func(t *testing.T) {
		judge := judgeFunc(func(context.Context, typesafe.SystemOneRequest) (*Evaluation, error) {
			return nil, errors.New("upstream body with a key: sk-secret")
		})
		a := Ask(context.Background(), judge, sampleRequest(), AskOptions{})
		if !reflect.DeepEqual(a, AskAnswer{Error: "TypeSafe request failed."}) {
			t.Fatalf("answer = %+v", a)
		}
		if data, _ := json.Marshal(a); contains(string(data), "sk-secret") {
			t.Fatal("secret escaped")
		}
	})
	tw(t, "ask", "the ask deadline and the caller's signal both cancel the request", func(t *testing.T) {
		slow := judgeFunc(func(ctx context.Context, _ typesafe.SystemOneRequest) (*Evaluation, error) {
			<-ctx.Done()
			return nil, newError(CodeAborted, "TypeSafe request cancelled before submission.")
		})
		timedOut := Ask(context.Background(), slow, sampleRequest(), AskOptions{Timeout: 5 * time.Millisecond})
		if !reflect.DeepEqual(timedOut, AskAnswer{Error: "TypeSafe request cancelled before submission.", ErrorCode: CodeAborted}) {
			t.Fatalf("timed out = %+v", timedOut)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan AskAnswer, 1)
		go func() { done <- Ask(ctx, slow, sampleRequest(), AskOptions{Timeout: 5 * time.Second}) }()
		cancel()
		if a := <-done; a.OK {
			t.Fatal("a cancelled ask must fail")
		}
		if DefaultAskTimeout != 15*time.Second {
			t.Errorf("default = %v", DefaultAskTimeout)
		}
	})
}
