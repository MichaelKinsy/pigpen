package pi_typesafe_test

import (
	"strings"
	"sync"
	"testing"

	pi_typesafe "github.com/MichaelKinsy/pigpen/components/pi-typesafe/extensions/pi-typesafe"
)

// Cases added by the review (rev-pigpen-typesafe).

// Consent is per destination. A /typesafe backend switch may arrive while a tool call is running (PiG runs
// extension commands during a turn); the call that was admitted under the TypeSafe consent must not be sent
// to the own model, whose destination the operator has not consented to. It stops and says so.
func TestBackendSwitchAfterAdmissionKeepsTheConsentedDestination(t *testing.T) {
	own := &fakeOwn{}
	r := newRig(t, func(o *pi_typesafe.Options) { o.Evaluator = own })
	t.Setenv("TYPESAFE_API_KEY", "offline-test-key")
	t.Setenv("PI_TYPESAFE_ENABLED", "1")
	r.startSession("startup")
	var once sync.Once
	var switchFailure string
	restore := pi_typesafe.SetAdmittedHook(func() {
		once.Do(func() { switchFailure = r.host.Command("typesafe", "backend ownmodel") })
	})
	defer restore()
	_, failure := r.tool(nil)
	if switchFailure != "" {
		t.Fatalf("switch: %s", switchFailure)
	}
	if own.calls != 0 {
		t.Fatalf("an admitted TypeSafe call reached the own model, a destination nobody consented to (calls=%d)", own.calls)
	}
	if !strings.Contains(failure, "nothing was sent") || r.network.Load() != 0 {
		t.Fatalf("the call stops and says so: failure=%q network=%d", failure, r.network.Load())
	}
	if _, failure := r.tool(nil); !strings.Contains(failure, "disabled") || own.calls != 0 {
		t.Fatalf("after the switch the tool is disabled: %q calls=%d", failure, own.calls)
	}
}

// The same for /typesafe test: the operator confirmed sending to api.typesafe.ai; if the backend changed while
// the dialog was open, nothing is sent.
func TestSampleSendsNothingWhenTheBackendChangedDuringConsent(t *testing.T) {
	own := &fakeOwn{}
	r := newRig(t, func(o *pi_typesafe.Options) { o.Evaluator = own })
	t.Setenv("TYPESAFE_API_KEY", "offline-test-key")
	var once sync.Once
	r.onConfirm = func() { once.Do(func() { _ = r.host.Command("typesafe", "backend ownmodel") }) }
	r.command("test")
	if !strings.Contains(r.confirmBodies[0], "api.typesafe.ai") {
		t.Fatalf("consent body = %s", r.confirmBodies[0])
	}
	if own.calls != 0 || r.network.Load() != 0 || len(r.entries) != 0 {
		t.Fatalf("nothing may be sent after the destination changed: own=%d network=%d entries=%d", own.calls, r.network.Load(), len(r.entries))
	}
	if !strings.Contains(r.lastNotice(), "nothing was sent") {
		t.Fatalf("the operator is told: %s", r.lastNotice())
	}
}

// The original reads the whole argument text as the action (`args.trim() || "status"`), so an action with
// trailing words is not an action: it gets the usage text, and nothing runs. Only this port's `backend`
// takes an argument.
func TestAnActionWithTrailingWordsGetsTheUsageText(t *testing.T) {
	r := newRig(t)
	t.Setenv("TYPESAFE_API_KEY", "offline-test-key")
	for _, args := range []string{"status now", "enable yes", "disable all", "logout please", "test twice"} {
		r.command(args)
		if !strings.HasPrefix(r.lastNotice(), "Usage: /typesafe login | logout") {
			t.Fatalf("/typesafe %s: %s", args, r.lastNotice())
		}
	}
	if r.confirmations != 0 || r.network.Load() != 0 {
		t.Fatalf("nothing runs: confirmations=%d network=%d", r.confirmations, r.network.Load())
	}
	r.command("  status  ")
	if !strings.HasPrefix(r.lastNotice(), "TypeSafe: disabled.") {
		t.Fatalf("surrounding space is trimmed: %s", r.lastNotice())
	}
	r.command("backend ownmodel")
	if !strings.Contains(r.lastNotice(), "Backend set to ownmodel") {
		t.Fatalf("backend takes its argument: %s", r.lastNotice())
	}
}

// Off by default: only the exact value 1 enables the tool from the environment, at construction and at
// every session start, as in the original (`process.env.PI_TYPESAFE_ENABLED === "1"`).
func TestOnlyTheExactValueOneEnablesFromTheEnvironment(t *testing.T) {
	for _, value := range []string{"0", "true", "yes", " 1", "1 "} {
		r := newRig(t, func(*pi_typesafe.Options) { t.Setenv("PI_TYPESAFE_ENABLED", value) })
		t.Setenv("TYPESAFE_API_KEY", "offline-test-key")
		if _, failure := r.tool(nil); !strings.Contains(failure, "TypeSafe is disabled") {
			t.Fatalf("PI_TYPESAFE_ENABLED=%q at load enabled the tool: %q", value, failure)
		}
		r.startSession("reload")
		if _, failure := r.tool(nil); !strings.Contains(failure, "TypeSafe is disabled") {
			t.Fatalf("PI_TYPESAFE_ENABLED=%q at session start enabled the tool: %q", value, failure)
		}
		if r.network.Load() != 0 {
			t.Fatalf("PI_TYPESAFE_ENABLED=%q sent a request", value)
		}
	}
	r := newRig(t, func(*pi_typesafe.Options) { t.Setenv("PI_TYPESAFE_ENABLED", "1") })
	t.Setenv("TYPESAFE_API_KEY", "offline-test-key")
	if _, failure := r.tool(nil); failure != "" || r.network.Load() != 1 {
		t.Fatalf("PI_TYPESAFE_ENABLED=1 enables: %q network=%d", failure, r.network.Load())
	}
}

// The production own-model evaluator (hostmodel), not an injected one: with no model selected in PiG the
// call fails with the reason (not a generic "unreadable response"), the callout does not blame a key the
// backend does not use, and nothing is sent anywhere; status names the configured model.
func TestOwnModelBackendOnTheHostModel(t *testing.T) {
	r := newRig(t, func(*pi_typesafe.Options) {
		t.Setenv("PI_TYPESAFE_BACKEND", "ownmodel")
		t.Setenv("PI_TYPESAFE_ENABLED", "1")
	})
	before := r.noticeCount()
	_, failure := r.tool(nil)
	if !strings.Contains(failure, "No model is configured in PiG; select one with /model first.") || r.network.Load() != 0 || r.modelList.Load() != 0 {
		t.Fatalf("failure=%q network=%d", failure, r.network.Load())
	}
	said := strings.Join(r.noticesSince(before), "\n")
	if !strings.Contains(said, "No model is configured in PiG") || strings.Contains(said, "key") || strings.Contains(said, "not authenticated") {
		t.Fatalf("callout = %q", said)
	}
	r.modelInfo = map[string]any{"id": "model-one", "provider": "provider-one", "name": "Model One"}
	r.command("status")
	if !strings.Contains(r.lastNotice(), "Model: provider-one/model-one.") || !strings.Contains(r.lastNotice(), "Nothing is sent to api.typesafe.ai") {
		t.Fatalf("status = %s", r.lastNotice())
	}
}
