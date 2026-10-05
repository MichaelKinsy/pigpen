package pitypesafe

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

type evalFunc func(ctx context.Context, req typesafe.SystemOneRequest) (*typesafe.SystemOneResult, error)

func (f evalFunc) SystemOne(ctx context.Context, req typesafe.SystemOneRequest, _ *typesafe.RequestOptions) (*typesafe.SystemOneResult, error) {
	return f(ctx, req)
}

func TestOwnModelClientNeedsAnEvaluatorAndUsesNoKeyOrHost(t *testing.T) {
	isolate(t)
	if _, err := New(Options{Backend: "ownmodel"}); !hasCode(err, CodeConfiguration) || !strings.Contains(err.Error(), "Evaluator") {
		t.Fatalf("err = %v", err)
	}
	c, err := New(Options{Backend: "ownmodel", Ledger: OpenUsageLedger(LedgerOptions{Path: t.TempDir() + "/u.json"}), Evaluator: evalFunc(func(_ context.Context, req typesafe.SystemOneRequest) (*typesafe.SystemOneResult, error) {
		return &typesafe.SystemOneResult{Model: "m", Answers: map[string]typesafe.Answer{"yes": typesafe.NoulAnswer{Noul: 0.4}}, Usage: typesafe.Usage{InputTokens: 3}}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	ev, err := c.Evaluate(context.Background(), sampleRequest())
	if err != nil || ev.Model != "m" || c.GetUsage().InputTokens != 3 {
		t.Fatalf("ev=%v err=%v", ev, err)
	}
	// No key is involved, so nothing is written to the auth state.
	if s := mustAuth(t, nil); s.Verified {
		t.Error("an own-model success must not verify a TypeSafe key")
	}
	if _, err := c.ListModels(context.Background()); !hasCode(err, CodeConfiguration) {
		t.Errorf("list models: %v", err)
	}
}

func TestOwnModelTimeoutAndErrorsAreClassified(t *testing.T) {
	isolate(t)
	slow := evalFunc(func(ctx context.Context, _ typesafe.SystemOneRequest) (*typesafe.SystemOneResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	c, _ := New(Options{Backend: "ownmodel", Timeout: 10 * time.Millisecond, Evaluator: slow, Ledger: OpenUsageLedger(LedgerOptions{Path: t.TempDir() + "/u.json"})})
	if _, err := c.Evaluate(context.Background(), sampleRequest()); !hasCode(err, CodeTimeout) || !strings.Contains(err.Error(), "The configured model") {
		t.Fatalf("timeout: %v", err)
	}
	c2, _ := New(Options{Backend: "ownmodel", Evaluator: evalFunc(func(context.Context, typesafe.SystemOneRequest) (*typesafe.SystemOneResult, error) {
		return nil, errors.New("provider body with sk-secret")
	}), Ledger: OpenUsageLedger(LedgerOptions{Path: t.TempDir() + "/u.json"})})
	_, err := c2.Evaluate(context.Background(), sampleRequest())
	if !hasCode(err, CodeResponse) || strings.Contains(err.Error(), "sk-secret") {
		t.Fatalf("plain error: %v", err)
	}
}

func TestEvaluationJSONKeepsQuestionOrderAndAddsElapsed(t *testing.T) {
	ev := &Evaluation{
		SystemOneResult: &typesafe.SystemOneResult{Model: "m", Answers: map[string]typesafe.Answer{"b": typesafe.NoulAnswer{Noul: 0.5}, "a": typesafe.NoulAnswer{Noul: 0.25}}, Usage: typesafe.Usage{InputTokens: 2}},
		ElapsedMs:       7, Order: []string{"b", "a"},
	}
	out, err := ev.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !(strings.Index(s, `"b"`) < strings.Index(s, `"a"`)) || !strings.HasSuffix(s, `"elapsedMs":7}`) || strings.Index(s, `"model"`) > strings.Index(s, `"answers"`) {
		t.Fatalf("json = %s", s)
	}
	d, _ := ev.DetailsJSON()
	if !strings.Contains(string(d), `"order":["b","a"]`) {
		t.Fatalf("details = %s", d)
	}
}
