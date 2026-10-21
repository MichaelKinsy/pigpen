package pi_permission_system

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"
)

// Tests the port adds (not upstream cases): behaviours the mutation run showed the twins did not pin.

type failingTurnPrep struct{}

func (failingTurnPrep) Prepare(SessionContext) error { return errTest }

func TestAgentPrepStopsWhenTurnPreparationFails(t *testing.T) {
	a := makeAgentPrep(apOpts{denied: func(string) bool { return true }})
	a.handler.turnPrep = failingTurnPrep{}
	if err := a.handler.Handle(makeAPEvent("x").payload, apCtx()); err == nil {
		t.Fatal("the preparation error was swallowed")
	}
	eq(t, len(a.registry.setCalls), 0, "nothing was narrowed")
}

func TestServiceLifecycleForgetsItsSessionAtTeardown(t *testing.T) {
	s := makeServiceLifecycle()
	s.l.Activate(idCtx("forget-session"))
	defer UnpublishPermissionsService("forget-session", s.service)
	s.l.Teardown()
	_, ok := s.l.CurrentSessionID()
	eq(t, ok, false, "no session id after teardown")
	// a second teardown must not unpublish what a later generation published under the same id
	later := makeFakeService()
	PublishPermissionsService("forget-session", later)
	s.l.Teardown()
	eq(t, GetPermissionsService("forget-session") == PermissionsService(later), true, "the later publication stays")
}

func TestServiceMcpQueryCarriesTheValueAsPathValues(t *testing.T) {
	r := makeLocalService()
	r.service.CheckPermission("mcp", sp("srv:list"), sp("agent"))
	eq(t, len(r.resolver.prefixed), 1, "resolved as a path-values intent")
	eq(t, r.resolver.prefixed[0].Values, []string{"srv:list"}, "values")
	eq(t, len(r.resolver.intents), 0, "not as a tool intent")
}

func TestWriteBackSkillsNarrowsTheHostsCatalogue(t *testing.T) {
	keep := map[string]any{"name": "open"}
	data := map[string]any{"systemPromptOptions": map[string]any{"skills": []any{map[string]any{"name": "secret"}, keep}}}
	writeBackSkills(data, &PromptOptions{Skills: []PromptSkill{{Name: "open", Raw: keep}}})
	got := data["systemPromptOptions"].(map[string]any)["skills"].([]any)
	eq(t, len(got), 1, "one skill kept")
	eq(t, got[0].(map[string]any)["name"], "open", "the kept one")
	writeBackSkills(data, nil) // no options: nothing to write
}

func TestTurnPrepWarmsTheBashParser(t *testing.T) {
	compEnv(t)
	bashparse.ResetWarmBashParser()
	t.Cleanup(bashparse.ResetWarmBashParser)
	n := newCompNode(t)
	ctx, _ := makeBaseCtx(t.TempDir(), "warm-session", compCtxOpts{})
	n.sessionStart(ctx)
	n.beforeAgentStart(ctx, emptyPromptPayload())
	deadline := time.Now().Add(10 * time.Second)
	for bashparse.GetWarmBashParser() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	eq(t, bashparse.GetWarmBashParser() != nil, true, "before_agent_start warms the parser")
}

type failingActivation struct{ refreshed, announced int }

func (*failingActivation) Activate(SessionContext) error         { return errTest }
func (f *failingActivation) RefreshConfig(*SessionContext, bool) { f.refreshed++ }

func TestTurnPrepStopsWhenActivationFails(t *testing.T) {
	session := &failingActivation{}
	announcer := &fakeAnnouncer{order: &recordedOrder{}}
	issues := &fakeIssueReporter{}
	prep := NewSessionTurnPrep(session, func() {}, announcer, issues)
	ctx, _ := trustedCtx(true)
	if err := prep.Prepare(ctx); err == nil {
		t.Fatal("the activation error was swallowed")
	}
	eq(t, session.refreshed, 0, "no refresh after a failed activation")
	eq(t, issues.reports, 0, "no report")
	eq(t, len(announcer.ctxs), 0, "no ready announcement for a node that could not activate")
}

// The before_agent_start handler writes the narrowed skill catalogue back into the host's options (mutant cr-skills-not-written-back): a
// skill the policy denies is gone from systemPromptOptions.skills after the handler, which is what Pi renders `<skills>` from. The fake
// host cannot carry the SDK's section object through its JSON wire, so the handler's own step is driven directly.
func TestHandleBeforeAgentStartWritesTheNarrowedSkillsBack(t *testing.T) {
	compEnv(t)
	n := newCompNode(t, "read")
	ctx := makeChildCtx(t.TempDir(), "s1")
	n.sessionStart(ctx)
	options := map[string]any{
		"sections": &apSections{values: map[string]string{}},
		"skills":   []any{map[string]any{"name": "alpha"}, map[string]any{"name": "beta"}},
	}
	data := map[string]any{"systemPrompt": "x", "systemPromptOptions": options}
	payload := BeforeAgentStartPayload{SystemPrompt: "x", Options: promptOptionsOf(data)}
	if payload.Options == nil || len(payload.Options.Skills) != 2 {
		t.Fatalf("the options were not read: %+v", payload.Options)
	}
	// the agent prep narrows the catalogue to the skills the policy lets the model use: take one out as it would
	payload.Options.Skills = payload.Options.Skills[1:]
	if err := n.p.handleBeforeAgentStart(payload, ctx, data); err != nil {
		t.Fatal(err)
	}
	kept, _ := options["skills"].([]any)
	eq(t, len(kept), 1, "the host's catalogue is the narrowed one")
	if len(kept) == 1 {
		eq(t, kept[0].(map[string]any)["name"], "beta", "the kept skill")
	}
}
