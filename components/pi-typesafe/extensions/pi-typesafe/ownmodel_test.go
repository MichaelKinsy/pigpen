package pi_typesafe_test

import (
	"context"
	"strings"
	"testing"

	pi_typesafe "github.com/MichaelKinsy/pigpen/components/pi-typesafe/extensions/pi-typesafe"
	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

// fakeOwn is an own-model evaluator: it answers every question and records that it was used.
type fakeOwn struct{ calls int }

func (f *fakeOwn) SystemOne(_ context.Context, req typesafe.SystemOneRequest, _ *typesafe.RequestOptions) (*typesafe.SystemOneResult, error) {
	f.calls++
	answers := map[string]typesafe.Answer{}
	for _, q := range req.Questions {
		answers[q.Name] = typesafe.NoulAnswer{Noul: 0.25}
	}
	return &typesafe.SystemOneResult{Model: "own-test", Answers: answers, Usage: typesafe.Usage{InputTokens: 5}}, nil
}

func TestOwnModelBackendSendsNothingToTypeSafe(t *testing.T) {
	own := &fakeOwn{}
	r := newRig(t, func(o *pi_typesafe.Options) { o.Evaluator = own })
	r.command("backend ownmodel")
	if !strings.Contains(r.lastNotice(), "consent to sending content to the provider of the model PiG is configured with") {
		t.Fatalf("switch notice = %s", r.lastNotice())
	}
	// Consent is per destination: the switch leaves the tool disabled.
	if _, failure := r.tool(nil); !strings.Contains(failure, "disabled") {
		t.Fatalf("failure = %q", failure)
	}
	r.confirmResult = true
	r.command("enable")
	if !strings.Contains(r.confirmBodies[len(r.confirmBodies)-1], "Nothing is sent to api.typesafe.ai") {
		t.Fatalf("the consent dialog must name the own-model destination: %v", r.confirmBodies)
	}
	details, failure := r.tool(nil)
	if failure != "" || details["model"] != "own-test" || own.calls != 1 {
		t.Fatalf("details=%v failure=%q calls=%d", details, failure, own.calls)
	}
	if r.network.Load() != 0 || r.modelList.Load() != 0 {
		t.Fatalf("the own-model backend must not call the TypeSafe API (network=%d)", r.network.Load())
	}
	r.command("status")
	if !strings.Contains(r.lastNotice(), "no key needed") || !strings.Contains(r.lastNotice(), "Nothing is sent to api.typesafe.ai") {
		t.Fatalf("status = %s", r.lastNotice())
	}
	r.command("login")
	if !strings.Contains(r.lastNotice(), "needs no key") {
		t.Fatalf("login = %s", r.lastNotice())
	}
	r.command("backend typesafe")
	if _, failure := r.tool(nil); !strings.Contains(failure, "disabled") {
		t.Fatalf("switching back must disable again: %q", failure)
	}
	r.command("backend nonsense")
	if !strings.Contains(r.lastNotice(), "Unknown backend") {
		t.Fatalf("notice = %s", r.lastNotice())
	}
}

func TestStartupBackendFromEnvironment(t *testing.T) {
	t.Setenv("PI_TYPESAFE_BACKEND", "ownmodel")
	r := newRig(t, func(o *pi_typesafe.Options) { o.Evaluator = &fakeOwn{} })
	t.Setenv("PI_TYPESAFE_BACKEND", "ownmodel")
	r.startSession("startup")
	r.command("backend")
	if !strings.Contains(r.lastNotice(), "Judgment backend: ownmodel") {
		t.Fatalf("notice = %s", r.lastNotice())
	}
}

func TestHeadlessCommandsReportThroughMessages(t *testing.T) {
	agent := isolate(t)
	_ = agent
	no := false
	ext := pi_typesafe.New(pi_typesafe.Options{})
	var messages []string
	host := StartHost(t, ext, HostOptions{HasUI: &no, Mode: "print", OnCall: func(method string, args map[string]any) (map[string]any, string) {
		if method == "sendMessage" {
			messages = append(messages, args["message"].(map[string]any)["content"].(string))
		}
		return nil, ""
	}})
	if failure := host.Command("typesafe", "enable"); failure != "" {
		t.Fatal(failure)
	}
	if len(messages) != 1 || !strings.Contains(messages[0], "needs interactive Pi") {
		t.Fatalf("messages = %v", messages)
	}
	if failure := host.Command("typesafe", "status"); failure != "" || len(messages) != 2 || !strings.Contains(messages[1], "TypeSafe: disabled.") {
		t.Fatalf("status: %q %v", failure, messages)
	}
	if failure := host.Command("typesafe", "bogus"); failure != "" || !strings.Contains(messages[2], "Usage: /typesafe login | logout | setup | status | enable | disable | test | playground") {
		t.Fatalf("usage: %q %v", failure, messages)
	}
}
