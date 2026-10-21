package pi_subagents

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Tests for the survivors of the mutations of the agent management lane: runtime acknowledgements, launch bindings, prompt audit,
// refinements, doctor, guide, profiles, admin panel and slash commands.

func TestRuntimeAcknowledgementsStopAtFinalizeAndRefuseParentSegments(t *testing.T) {
	c := &runtimeAckCollector{sink: func([]string) {}}
	c.acknowledge("ext.one")
	c.finalize()
	c.acknowledge("late")
	eq(t, c.ids, []string{"ext.one"}, "an id acknowledged after the final serialization is dropped")
	eq(t, isRuntimeAcknowledgedExtensionID("a.b"), true, "a dot")
	eq(t, isRuntimeAcknowledgedExtensionID("a..b"), false, "a parent segment")
}

func TestLaunchBindingProjectionIsVersionTwo(t *testing.T) {
	eq(t, projectLaunchBinding(launchBindingInput{})["version"], any(float64(2)), "projection version")
}

func TestBindingJSONRefusesNonFiniteNumbers(t *testing.T) {
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		props := 0
		_, err := canonicalizeBindingJSON(v, "bindings.x", 0, map[uintptr]bool{}, &props)
		hasMatch(t, err, `bindings.x must contain only finite JSON numbers.`)
	}
	props := 0
	got, err := canonicalizeBindingJSON(1.5, "bindings.x", 0, map[uintptr]bool{}, &props)
	eq(t, err, nil, "a finite number")
	eq(t, got, any(1.5), "kept")
}

func TestPromptAuditHelpers(t *testing.T) {
	eq(t, promptRuntimeAdditions("task", "an effective prompt without it"), "(runtime additions unavailable)", "the authored task is not in the prompt")
	eq(t, opencodeSessionHeaders("opencode", "", ""), map[string]any(nil), "no session id, no headers")
	eq(t, opencodeSessionHeaders("opencode", "", "s1"), map[string]any{"x-opencode-session": "s1", "x-opencode-client": "pi"}, "opencode")
	eq(t, opencodeSessionHeaders("other", "https://example.com", "s1"), map[string]any(nil), "another provider")
	eq(t, opencodeSessionHeaders("other", "https://opencode.ai", "s1") != nil, true, "the opencode host")
}

func TestRefinementPathRefusesParentSegmentsInsideAName(t *testing.T) {
	dir := tmp(t)
	_, err := getAgentRefinementPath(dir, "a..b")
	hasMatch(t, err, `cannot be used as a refinement file name`)
	p, err := getAgentRefinementPath(dir, "a.b")
	eq(t, err, nil, "a dot")
	eq(t, filepath.Base(p), "a.b.md", "file name")
}

func TestRefinementEvidenceIsBoundedByCountAndAge(t *testing.T) {
	withTempHome(t)
	dir := tmp(t)
	a := filepath.Join(dir, ".pi/subagents", "artifacts")
	for i := range 10 {
		writeFile(t, filepath.Join(a, fmt.Sprintf("run%d_worker_meta.json", i)), marshalJSON(obj("runId", fmt.Sprintf("run%d", i), "agent", "worker", "exitCode", float64(1),
			"timestamp", float64(time.Now().UnixMilli())), ""))
	}
	eq(t, len(collectBoundedRefinementEvidence(dir, "worker")), 8, "at most eight items")
	iso := func(d time.Duration) string { return time.Now().Add(-d).UTC().Format("2006-01-02T15:04:05.000Z") }
	eq(t, withinEvidenceAge(iso(13*24*time.Hour)), true, "13 days")
	eq(t, withinEvidenceAge(iso(15*24*time.Hour)), false, "15 days")
	eq(t, withinEvidenceAge(""), true, "no timestamp")
}

func TestRefinementProposalRefusesFences(t *testing.T) {
	edit := obj("title", "t", "guidance", "Use ```sh\nrm -rf x\n``` first.", "evidenceIds", []any{"e1"}, "rationale", "r")
	p, msg := validateRefinementProposal(obj("summary", "x", "residualRisks", []any{}, "edits", []any{edit}), []string{"e1"})
	eq(t, p == nil, true, "a fenced block is refused")
	eq(t, msg, "Refinement proposal edit 0 contains disallowed guidance.", "message")
}

func TestRefinementRollbackBumpsTheRevisionAndRunsUseTheOverlay(t *testing.T) {
	withTempHome(t)
	t.Setenv("PI_OFFLINE", "true")
	clearAgentDiscoveryCache()
	t.Cleanup(clearAgentDiscoveryCache)
	dir := tmp(t)
	writeFile(t, filepath.Join(dir, ".pi", "agents", "worker.md"), "---\nname: worker\ndescription: Test worker\n---\n\nBase prompt for worker.\n")
	art := filepath.Join(dir, ".pi/subagents", "artifacts")
	writeFile(t, filepath.Join(art, "run_worker_meta.json"), marshalJSON(obj("runId", "run", "agent", "worker", "exitCode", float64(1), "error", "missed required final report",
		"timestamp", float64(time.Now().UnixMilli())), ""))
	call := 0
	ctx := refinementContext{cwd: dir, launchProposalChild: func(string, *jsObject) proposalChildResult {
		call++
		return proposalChildResult{Structured: obj("summary", "proposal", "residualRisks", []any{}, "edits", []any{
			obj("title", "edit", "guidance", fmt.Sprintf("Guidance number %d for the worker.", call), "evidenceIds", []any{"artifact:run_worker"}, "rationale", "The artifact shows it."),
		})}
	}}
	for range 2 {
		r := handleRefinementAction("refine", map[string]any{"agent": "worker"}, ctx)
		eq(t, r.isError, false, "refine: "+r.text)
	}
	rollback := handleRefinementAction("refine.rollback", map[string]any{"agent": "worker"}, ctx)
	eq(t, rollback.isError, false, "rollback: "+rollback.text)
	polMatches(t, rollback.text, `Rolled back refinement overlay for 'worker' to revision 3\.`)

	calls := fakeChildren(t, nil)
	if _, err := runAgent(nil, dir, &AgentConfig{Name: "worker", SystemPrompt: "Base prompt for worker.", SystemPromptMode: "replace"}, "do it", ""); err != nil {
		t.Fatal(err)
	}
	args := strings.Join((*calls)[0].Args, "\x00")
	eq(t, strings.Contains(args, "<pi-subagents-refinement agent=\"worker\""), true, "the child's system prompt carries the overlay")
	eq(t, strings.Contains(args, "Guidance number 1 for the worker."), true, "the rolled back guidance")
}

func TestDoctorReportsTheRunFanoutSourceAndAnUnlimitedAsyncCapacity(t *testing.T) {
	root := tmp(t)
	deps := doctorDeps{
		IsAsyncAvailable:        func() bool { return true },
		DiscoverAgentsAll:       func(string) (DiscoveryAll, error) { return DiscoveryAll{UserDir: root, ProjectDir: root}, nil },
		DiscoverAvailableSkills: func(string) ([]AvailableSkill, error) { return nil, nil },
		DiagnoseIntercomBridge:  func() intercomDiagnostic { return intercomDiagnostic{Mode: "off", ExtensionDir: "none"} },
	}
	t.Setenv("PI_SUBAGENT_MAX_SPAWNS_PER_RUN", "7")
	report := buildDoctorReport(doctorInput{Cwd: root, Deps: deps})
	polMatches(t, report, `Run fan-out budget\n- configured limit: 7 \(environment\)`)
	polMatches(t, report, `Active async capacity\n- usage: 0/unlimited used`)
}

func TestSkillSourceCountsFollowTheSourceOrder(t *testing.T) {
	got := formatSkillSourceCounts([]AvailableSkill{{Name: "a", Source: skillProjectSettings}, {Name: "b", Source: skillProject}, {Name: "c", Source: skillBuiltin}})
	eq(t, got, fmt.Sprintf("%s 1, %s 1, %s 1", skillProject, skillProjectSettings, skillBuiltin), "order")
	eq(t, formatSkillSourceCounts(nil), "none", "none")
}

func TestGuideServesEveryTopic(t *testing.T) {
	for _, topic := range guideTopics {
		text, err := readSubagentGuide(topic)
		eq(t, err, nil, topic)
		eq(t, text != "" && !strings.HasPrefix(text, "Unknown subagents guide topic"), true, topic+" is served")
	}
	text, _ := readSubagentGuide("nope")
	eq(t, text, "Unknown subagents guide topic 'nope'. Valid topics: overview, workflows, agents, missions, observability, tool-reference, configuration, models, watchdog, extension-api, council. No files were changed.", "unknown topic")
}

func TestProfileHelpers(t *testing.T) {
	for _, bad := range []string{"a b", "-x", ".hidden", "a/b", ".."} {
		_, err := normalizePathToken(bad, "Profile name")
		hasMatch(t, err, `must be a safe file name`)
	}
	got, err := normalizePathToken(" ok.name-1_x ", "Profile name")
	eq(t, err, nil, "a safe name")
	eq(t, got, "ok.name-1_x", "trimmed")

	eq(t, classifyModel(classInput{ID: "flash", Name: "flash"}, classContext{}).ProfileRank, -125, "a fast model is ranked 125 lower")

	cheap := catalogModel{ID: "a", Cost: map[string]float64{"input": 1, "output": 1}, Derived: classification{ProfileRank: 10}}
	dear := catalogModel{ID: "b", Cost: map[string]float64{"input": 2, "output": 2}, Derived: classification{ProfileRank: 5}}
	kept := filterDominated([]catalogModel{cheap, dear})
	eq(t, len(kept), 1, "the dominated model is dropped")
	eq(t, kept[0].ID, "a", "the dominating model stays")

	old := &providerCatalog{RefreshedAt: time.Now().Add(-20 * 24 * time.Hour).UTC().Format(time.RFC3339Nano)}
	fresh := &providerCatalog{RefreshedAt: time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339Nano)}
	eq(t, isProviderModelCatalogStale(old, 14), true, "20 days old")
	eq(t, isProviderModelCatalogStale(fresh, 14), false, "1 day old")
	eq(t, isProviderModelCatalogStale(&providerCatalog{RefreshedAt: "not a time"}, 14), true, "unreadable time")
}

func TestProfileCheckProbesEachModelOnce(t *testing.T) {
	home := withTempHome(t)
	t.Setenv("PI_OFFLINE", "true")
	clearAgentDiscoveryCache()
	t.Cleanup(clearAgentDiscoveryCache)
	t.Setenv("PIG_SUBAGENT_PIG_BINARY", filepath.Join(home, "pi-test-bin"))
	wj(t, filepath.Join(profilesRootDir(), "demo.json"), js(t, `{"subagents":{"agentOverrides":{"scout":{"model":"openai-codex/gpt-5.3"},"worker":{"model":"openai-codex/gpt-5.3"}}}}`))
	probes := 0
	host := profileHost{exec: func(string, []string, string, int) (string, string, int, bool, error) {
		probes++
		return "OK\n", "", 0, false, nil
	}, models: func() []map[string]any { return []map[string]any{{"provider": "openai-codex", "id": "gpt-5.3"}} }}
	r, err := host.checkSubagentProfile("demo")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, len(r.Results), 2, "both agents are checked")
	eq(t, probes, 1, "the same model is probed once")
}

func TestSavesThroughSettingsFollowsTheField(t *testing.T) {
	a := AgentConfig{Source: SourceUser, Model: "m2", Thinking: "high", SystemPrompt: "p2",
		Override: &OverrideInfo{Scope: "user", Base: OverrideBase{Model: "m1", Thinking: "low", SystemPrompt: "p1"}}}
	eq(t, savesThroughSettings(a, "model"), true, "a model that differs from the base")
	eq(t, savesThroughSettings(a, "thinking"), true, "thinking that differs")
	eq(t, savesThroughSettings(a, "systemPrompt"), true, "a prompt that differs")
	same := a
	same.Model, same.Thinking, same.SystemPrompt = "m1", "low", "p1"
	eq(t, savesThroughSettings(same, "model"), false, "the same model")
	eq(t, savesThroughSettings(same, "thinking"), false, "the same thinking")
	eq(t, savesThroughSettings(same, "systemPrompt"), false, "the same prompt")
	other := a
	other.Source = SourceProject
	eq(t, savesThroughSettings(other, "model"), false, "an override of another scope stays in the frontmatter")
}

func TestAdminPanelDoesNotListADisabledAgent(t *testing.T) {
	e := newMgmtEnv(t)
	r := e.act("disable", map[string]any{"agent": "reviewer"})
	eq(t, r.isError, false, "disable: "+r.text)
	rig := newAdminRig(e, nil, func(string, []string) (string, bool) { return "", false })
	if err := rig.host.openSubagentsAdmin("reviewer"); err != nil {
		t.Fatal(err)
	}
	eq(t, len(rig.sent), 1, "one message")
	polMatches(t, rig.sent[0], `^Subagent 'reviewer' not found\.\n\nAvailable subagents:\n`)
	polNotMatches(t, rig.sent[0], `- reviewer `)
}

func TestRefreshProviderArgsCarryTheForceFlag(t *testing.T) {
	for _, c := range []struct {
		args, provider string
		force          bool
		usage          bool
	}{
		{"openai", "openai", false, false},
		{"openai --force", "openai", true, false},
		{"openai force", "openai", true, false},
		{"  openai   --force ", "openai", true, false},
		{"", "", false, true},
		{"a b", "", false, true},
	} {
		provider, force, usage := parseRefreshProviderArgs(c.args)
		eq(t, []any{provider, force, usage != ""}, []any{c.provider, c.force, c.usage}, c.args)
	}
	_, _, usage := parseRefreshProviderArgs("")
	eq(t, usage, "Usage: /subagents-refresh-provider-models <provider> [--force]", "usage text")
}
