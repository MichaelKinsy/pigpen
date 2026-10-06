package policy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/hardening/profile"
)

type fn func(context.Context, Request) (Decision, error)

func (f fn) Decide(ctx context.Context, r Request) (Decision, error) { return f(ctx, r) }

func decide(t *testing.T, d Decider) Decision {
	t.Helper()
	dec, err := FailClosed(d).Decide(context.Background(), Request{Principal: "s", Action: "bash"})
	if err != nil {
		t.Fatalf("FailClosed returned an error: %v", err)
	}
	return dec
}

func TestAnAllowPassesThrough(t *testing.T) {
	dec := decide(t, fn(func(context.Context, Request) (Decision, error) {
		return Decision{Allow: true, Matched: []string{"policy0"}}, nil
	}))
	if !dec.Allow || dec.Reason != "" || len(dec.Matched) != 1 || dec.Matched[0] != "policy0" {
		t.Fatalf("%+v", dec)
	}
}

// An allow that carries a reason breaks the Decision contract (Reason is "" for an allow): the decider is
// confused, perhaps reporting a forbid it failed to apply. Deny wins over allow.
func TestAnAllowWithAReasonIsADeny(t *testing.T) {
	for _, reason := range []profile.Code{profile.PolicyForbidden, profile.PolicyError, "ignored"} {
		dec := decide(t, fn(func(context.Context, Request) (Decision, error) {
			return Decision{Allow: true, Reason: reason, Matched: []string{"policy0"}}, nil
		}))
		if dec.Allow || dec.Reason != profile.PolicyError {
			t.Errorf("%q: %+v", reason, dec)
		}
	}
}

func TestADenyPassesThroughWithItsReason(t *testing.T) {
	for _, code := range []profile.Code{profile.PolicyForbidden, profile.PolicyDenied, profile.PolicyError} {
		dec := decide(t, fn(func(context.Context, Request) (Decision, error) {
			return Decision{Reason: code, Matched: []string{"p1"}}, nil
		}))
		if dec.Allow || dec.Reason != code || len(dec.Matched) != 1 {
			t.Fatalf("%s: %+v", code, dec)
		}
	}
	dec := decide(t, fn(func(context.Context, Request) (Decision, error) { return Decision{}, nil }))
	if dec.Allow || dec.Reason != profile.PolicyDenied {
		t.Fatalf("a deny with no reason: %+v", dec)
	}
	dec = decide(t, fn(func(context.Context, Request) (Decision, error) { return Decision{Reason: "token=SECRET"}, nil }))
	if dec.Allow || dec.Reason != profile.PolicyDenied {
		t.Fatalf("a deny with a reason outside the set: %+v", dec)
	}
}

func TestAnErrorIsADenyEvenWhenTheDeciderSaidAllow(t *testing.T) {
	dec := decide(t, fn(func(context.Context, Request) (Decision, error) {
		return Decision{Allow: true}, errors.New("failed reading /secret/policy.cedar: token=SECRET")
	}))
	if dec.Allow || dec.Reason != profile.PolicyError {
		t.Fatalf("%+v", dec)
	}
	for _, c := range []profile.Code{profile.PolicyUnavailable, profile.PolicyError, profile.Cancelled} {
		dec = decide(t, fn(func(context.Context, Request) (Decision, error) {
			return Decision{Allow: true}, fmt.Errorf("wrapped: %w", profile.NewError(c, profile.FlagPolicyFailClosed, "permissions"))
		}))
		if dec.Allow || dec.Reason != c {
			t.Fatalf("%s: %+v", c, dec)
		}
	}
	// A typed error of another kind does not pick the reason.
	dec = decide(t, fn(func(context.Context, Request) (Decision, error) {
		return Decision{}, profile.NewError(profile.CredentialExpired, "", "x")
	}))
	if dec.Reason != profile.PolicyError {
		t.Fatalf("%+v", dec)
	}
}

func TestAPanicIsADenyAndItsValueIsDropped(t *testing.T) {
	for _, v := range []any{"token=SECRET", errors.New("boom"), 42, nil} {
		dec := decide(t, fn(func(context.Context, Request) (Decision, error) { panic(v) }))
		if dec.Allow || dec.Reason != profile.PolicyError || strings.Contains(fmt.Sprint(dec), "SECRET") {
			t.Fatalf("%+v", dec)
		}
	}
}

func TestANilDeciderIsADeny(t *testing.T) {
	dec := decide(t, nil)
	if dec.Allow || dec.Reason != profile.PolicyUnavailable {
		t.Fatalf("%+v", dec)
	}
	var typed *nilDecider
	dec = decide(t, typed)
	if dec.Allow || dec.Reason != profile.PolicyUnavailable {
		t.Fatalf("typed nil: %+v", dec)
	}
	var f fn
	dec = decide(t, f)
	if dec.Allow || dec.Reason != profile.PolicyUnavailable {
		t.Fatalf("nil func: %+v", dec)
	}
	// The wrapper itself is a value, never nil.
	if FailClosed(nil) == nil {
		t.Fatal("FailClosed(nil) is nil")
	}
}

type nilDecider struct{}

func (*nilDecider) Decide(context.Context, Request) (Decision, error) {
	return Decision{Allow: true}, nil
}

func TestAFinishedContextIsADenyAndTheDeciderIsNotAsked(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	asked := false
	dec, err := FailClosed(fn(func(context.Context, Request) (Decision, error) { asked = true; return Decision{Allow: true}, nil })).Decide(ctx, Request{})
	if err != nil || dec.Allow || dec.Reason != profile.Cancelled || asked {
		t.Fatalf("%+v %v asked=%v", dec, err, asked)
	}
	// A nil context is tolerated.
	dec, err = FailClosed(fn(func(context.Context, Request) (Decision, error) { return Decision{Allow: true}, nil })).Decide(nil, Request{}) //nolint:staticcheck
	if err != nil || !dec.Allow {
		t.Fatalf("%+v %v", dec, err)
	}
}

func TestMatchedIsCopiedAndBounded(t *testing.T) {
	src := make([]string, 40)
	for i := range src {
		src[i] = fmt.Sprintf("policy%d", i)
	}
	dec := decide(t, fn(func(context.Context, Request) (Decision, error) { return Decision{Allow: true, Matched: src}, nil }))
	if len(dec.Matched) != MaxMatched {
		t.Fatalf("%d", len(dec.Matched))
	}
	src[0] = "changed"
	if dec.Matched[0] != "policy0" {
		t.Fatal("Matched aliases the decider's slice")
	}
}

func TestDenyDecider(t *testing.T) {
	dec, err := Deny(profile.PolicyUnavailable).Decide(context.Background(), Request{})
	if err != nil || dec.Allow || dec.Reason != profile.PolicyUnavailable {
		t.Fatalf("%+v %v", dec, err)
	}
	dec, _ = Deny("nonsense").Decide(context.Background(), Request{})
	if dec.Allow || dec.Reason != profile.PolicyError {
		t.Fatalf("%+v", dec)
	}
}

func TestFailClosedRequestReachesTheDecider(t *testing.T) {
	var got Request
	_, _ = FailClosed(fn(func(_ context.Context, r Request) (Decision, error) { got = r; return Decision{Allow: true}, nil })).Decide(
		context.Background(), Request{Principal: "p", Action: "a", Resource: "r", Context: map[string]any{"k": 1}, Subject: "s", Tenant: "t", ActionGroups: []string{"Shell"}})
	if got.Principal != "p" || got.Action != "a" || got.Resource != "r" || got.Context["k"] != 1 || got.Subject != "s" || got.Tenant != "t" || got.ActionGroups[0] != "Shell" {
		t.Fatalf("%+v", got)
	}
}
