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
