package pi_subagents

import (
	"strings"
	"testing"
)

// Twins of test/unit/workflow-launch-params.test.ts.
// obj parses a JSON literal into ordered host data (an object literal of the original's tests).
func jobj(t *testing.T, text string) *jsObject {
	t.Helper()
	v, err := parseJSON([]byte(text))
	if err != nil {
		t.Fatalf("bad literal %s: %v", text, err)
	}
	return v.(*jsObject)
}

func prep(t *testing.T, defaults, child, key string, opt ...WorkflowLaunchOptions) *jsObject {
	t.Helper()
	var o WorkflowLaunchOptions
	if len(opt) > 0 {
		o = opt[0]
	}
	out, err := prepareWorkflowLaunchParams(jobj(t, defaults), jobj(t, child), "workflow-run", key, o)
	if err != nil {
		t.Fatalf("prepareWorkflowLaunchParams: %v", err)
	}
	return out
}

func absent(t *testing.T, o *jsObject, k string) {
	t.Helper()
	if _, ok := lpGet(o, k); ok {
		t.Errorf("%s = %v, want undefined", k, o.vals[k])
	}
}

func TestWorkflowLaunchParamsTwins(t *testing.T) {
	const f = "workflow-launch-params"
	tw(t, f, "keeps remote workflow cwd out of local discovery for explicit and agent-pinned machines", func(t *testing.T) {
		workflowCwd := "/local/workflow"
		var calls []string
		pinned := AgentConfig{Name: "pinned", Description: "Pinned agent", SystemPrompt: "Run remotely.", SystemPromptMode: "replace", Source: SourceProject, FilePath: "/local/workflow/.pi/agents/pinned.md", Machine: "workmac"}
		discover := func(cwd string, scope AgentScope) []AgentConfig {
			calls = append(calls, cwd)
			return []AgentConfig{pinned}
		}
		eq(t, resolveWorkflowChildLocalCwd(workflowCwd, discover, nil, nil, jobj(t, `{"agent":"worker","machine":"workmac","cwd":"/remote/repo"}`)), workflowCwd, "explicit machine")
		eq(t, len(calls), 0, "no discovery")
		eq(t, resolveWorkflowChildLocalCwd(workflowCwd, discover, nil, nil, jobj(t, `{"agent":"pinned","cwd":"/remote/repo"}`)), workflowCwd, "pinned agent")
		eq(t, calls, []string{workflowCwd}, "discovery")
	})
	tw(t, f, "preserves omitted workflow child async defaults and awaits background resolution", func(t *testing.T) {
		jsonEq(t, prep(t, `{}`, `{"agent":"worker","task":"Run"}`, "run"), `{"agent":"worker","task":"Run","workflowAwaitAsync":true,"workflowParentRunId":"workflow-run","workflowKey":"run"}`)
	})
	tw(t, f, "forwards workflow baseRef only to launches where it can affect allocation", func(t *testing.T) {
		d := `{"baseRef":"refs/heads/release"}`
		eq(t, prep(t, d, `{"agent":"worker","task":"Run"}`, "run").vals["baseRef"], "refs/heads/release", "inherited")
		absent(t, prep(t, d, `{"resume":"retained-run","task":"Continue"}`, "resume"), "baseRef")
		eq(t, prep(t, d, `{"resume":"retained-run","task":"Continue","baseRef":"refs/heads/topic"}`, "resume-explicit").vals["baseRef"], "refs/heads/topic", "explicit resume")
		eq(t, prep(t, d, `{"agent":"worker","task":"Run","baseRef":"refs/heads/topic"}`, "override").vals["baseRef"], "refs/heads/topic", "override")
	})
	tw(t, f, "does not forward workflow capacity overrides to children", func(t *testing.T) {
		p := prep(t, `{"globalConcurrencyLimit":2,"maxSubagentSpawnsPerRun":3}`, `{"agent":"worker","task":"Run","globalConcurrencyLimit":4,"maxSubagentSpawnsPerRun":5}`, "run")
		absent(t, p, "globalConcurrencyLimit")
		absent(t, p, "maxSubagentSpawnsPerRun")
	})
	tw(t, f, "merges partial control overrides into workflow child defaults", func(t *testing.T) {
		p := prep(t, `{"control":{"needsAttentionAfterMs":111,"activeNoticeAfterMs":222}}`, `{"agent":"worker","task":"Run","control":{"activeNoticeAfterMs":333}}`, "run")
		jsonEq(t, p.vals["control"], `{"needsAttentionAfterMs":111,"activeNoticeAfterMs":333}`)
	})
	tw(t, f, "marks only new async workflow children to preserve live supervisor-detach awaits", func(t *testing.T) {
		on := WorkflowLaunchOptions{AwaitDetachedChild: true}
		eq(t, prep(t, `{}`, `{"agent":"worker","task":"Run"}`, "run", on).vals["workflowAwaitDetached"], true, "new child")
		absent(t, prep(t, `{}`, `{"resume":"retained-run","task":"Continue"}`, "resume", on), "workflowAwaitDetached")
	})
	tw(t, f, "scrubs the live workflow detach bridge from prompt-audit redo params", func(t *testing.T) {
		child := prep(t, `{}`, `{"agent":"worker","task":"Run"}`, "run", WorkflowLaunchOptions{AwaitDetachedChild: true})
		eq(t, child.vals["workflowAwaitDetached"], true, "bridge")
		redo, err := promptAuditRedoParams(child, "Run with narrower guidance")
		if err != nil {
			t.Fatal(err)
		}
		absent(t, redo, "workflowAwaitDetached")
		absent(t, redo, "workflowParentRunId")
		absent(t, redo, "workflowKey")
		eq(t, redo.vals["async"], false, "async")
		eq(t, redo.vals["task"], "Run with narrower guidance", "task")
		_, err = promptAuditRedoParams(nil, "x")
		hasMatch(t, err, "Prompt redo is missing a safe live launch contract.")
	})
	deadline := 1e12
	tw(t, f, "passes an omitted child timeout parent deadline for default resolution", func(t *testing.T) {
		p := prep(t, `{}`, `{"agent":"worker","task":"Run"}`, "run", WorkflowLaunchOptions{ParentDeadlineAt: &deadline})
		absent(t, p, "async")
		eq(t, p.vals["workflowAwaitAsync"], true, "await")
		absent(t, p, "timeoutMs")
		eq(t, p.vals["workflowParentDeadlineAt"], deadline, "deadline")
	})
	tw(t, f, "preserves explicit child timeout aliases over the parent deadline", func(t *testing.T) {
		opt := WorkflowLaunchOptions{ParentDeadlineAt: &deadline}
		eq(t, prep(t, `{}`, `{"agent":"worker","task":"Run","timeoutMs":90000}`, "timeout", opt).vals["timeoutMs"], float64(90000), "timeoutMs")
		p := prep(t, `{}`, `{"agent":"worker","task":"Run","maxRuntimeMs":90000}`, "max-runtime", opt)
		eq(t, p.vals["maxRuntimeMs"], float64(90000), "maxRuntimeMs")
		absent(t, p, "timeoutMs")
	})
	tw(t, f, "preserves explicit async workflow children", func(t *testing.T) {
		jsonEq(t, prep(t, `{}`, `{"agent":"worker","task":"Run","async":true}`, "run"), `{"agent":"worker","task":"Run","async":true,"workflowParentRunId":"workflow-run","workflowKey":"run"}`)
	})
	tw(t, f, "runs omitted external workflow children async while preserving awaited semantics", func(t *testing.T) {
		ext := WorkflowLaunchOptions{ExternalAsyncRequired: true}
		jsonEq(t, prep(t, `{}`, `{"agent":"gpt-pro","task":"Check weather"}`, "weather", ext), `{"agent":"gpt-pro","task":"Check weather","async":true,"workflowAwaitAsync":true,"workflowParentRunId":"workflow-run","workflowKey":"weather"}`)
		jsonEq(t, prep(t, `{}`, `{"agent":"gpt-pro","task":"Check weather","async":true}`, "weather", ext), `{"agent":"gpt-pro","task":"Check weather","async":true,"workflowParentRunId":"workflow-run","workflowKey":"weather"}`)
		jsonEq(t, prep(t, `{}`, `{"agent":"gpt-pro","task":"Check weather","async":false}`, "weather", ext), `{"agent":"gpt-pro","task":"Check weather","async":false,"workflowParentRunId":"workflow-run","workflowKey":"weather"}`)
	})
	tw(t, f, "keeps a bridge override scoped to the target workflow child", func(t *testing.T) {
		jsonEq(t, prep(t, `{}`, `{"agent":"worker","task":"Run","intercomBridge":{"mode":"off"}}`, "isolated"), `{"agent":"worker","task":"Run","intercomBridge":{"mode":"off"},"workflowAwaitAsync":true,"workflowParentRunId":"workflow-run","workflowKey":"isolated"}`)
		absent(t, prep(t, `{}`, `{"agent":"worker","task":"Run"}`, "sibling"), "intercomBridge")
	})
	tw(t, f, "canonicalizes child extension bindings without leaking them to siblings", func(t *testing.T) {
		bindings := `{"shepherd.dispatch/1":{"writeScope":["src/a.ts"],"role":"coder"}}`
		child := prep(t, `{"extensionBindings":{"defaults.policy/1":true}}`, `{"agent":"worker","task":"Run","extensionBindings":`+bindings+`}`, "bound")
		jsonEq(t, child.vals["extensionBindings"], bindings)
		absent(t, prep(t, `{}`, `{"agent":"worker","task":"Run"}`, "plain"), "extensionBindings")
	})
	tw(t, f, "keeps managed worktree children on the single-run contract", func(t *testing.T) {
		jsonEq(t, prep(t, `{}`, `{"agent":"worker","task":"Implement","worktree":true,"gate":"npm test"}`, "gated"),
			`{"agent":"worker","task":"Implement","worktree":true,"workflowAwaitAsync":true,"workflowParentRunId":"workflow-run","workflowKey":"gated","acceptance":{"level":"verified","verify":[{"id":"gate","command":"npm test"}]}}`)
	})
	tw(t, f, "projects an object gate into a typed verify command", func(t *testing.T) {
		gate := `{"command":"classify.sh --report r.md","output":"json","schema":{"type":"object"},"timeoutMs":5000}`
		jsonEq(t, prep(t, `{}`, `{"agent":"reviewer","task":"Review","gate":`+gate+`}`, "typed"),
			`{"agent":"reviewer","task":"Review","workflowAwaitAsync":true,"workflowParentRunId":"workflow-run","workflowKey":"typed","acceptance":{"level":"verified","verify":[{"id":"gate","command":"classify.sh --report r.md","output":"json","schema":{"type":"object"},"timeoutMs":5000}]}}`)
	})
	tw(t, f, "preserves a bridge override for retained workflow children", func(t *testing.T) {
		jsonEq(t, prep(t, `{}`, `{"resume":"retained-run","task":"Continue","intercomBridge":{"mode":"off"}}`, "continue"),
			`{"action":"resume","id":"retained-run","message":"Continue","workflowParentRunId":"workflow-run","workflowKey":"continue","intercomBridge":{"mode":"off"}}`)
	})
	tw(t, f, "forwards control defaults and overrides to retained workflow children", func(t *testing.T) {
		jsonEq(t, prep(t, `{"control":{"needsAttentionAfterMs":111,"activeNoticeAfterMs":222}}`, `{"resume":"retained-run","task":"Continue","control":{"activeNoticeAfterMs":333}}`, "continue"),
			`{"action":"resume","id":"retained-run","message":"Continue","workflowParentRunId":"workflow-run","workflowKey":"continue","control":{"needsAttentionAfterMs":111,"activeNoticeAfterMs":333}}`)
	})
	tw(t, f, "lets retained workflow child control overrides amend recovered control configs", func(t *testing.T) {
		n := func(v float64) *float64 { return &v }
		recovered := wfResolveControlConfig(nil, &wfControlConfig{NeedsAttentionAfterMs: n(111), ActiveNoticeAfterMs: n(222)})
		empty := []string{}
		control := wfResolveRevivalControlConfig(nil, &wfControlConfig{ActiveNoticeAfterMs: n(333), NotifyChannels: &empty}, &recovered)
		eq(t, *control.NeedsAttentionAfterMs, 111.0, "needsAttentionAfterMs")
		eq(t, *control.ActiveNoticeAfterMs, 333.0, "activeNoticeAfterMs")
		eq(t, *control.NotifyChannels, []string{}, "notifyChannels")
	})
	tw(t, f, "does not inherit parent deadlines for retained workflow children", func(t *testing.T) {
		d := 1e12
		jsonEq(t, prep(t, `{}`, `{"resume":"retained-run","task":"Continue"}`, "continue", WorkflowLaunchOptions{ParentDeadlineAt: &d}),
			`{"action":"resume","id":"retained-run","message":"Continue","workflowParentRunId":"workflow-run","workflowKey":"continue"}`)
	})
	tw(t, f, "preserves worktree isolation for retained workflow children", func(t *testing.T) {
		jsonEq(t, prep(t, `{}`, `{"resume":"retained-run","task":"Continue","worktree":true}`, "continue"),
			`{"action":"resume","id":"retained-run","message":"Continue","workflowParentRunId":"workflow-run","workflowKey":"continue","worktree":true}`)
	})
	tw(t, f, "rejects gate defaults on retained resume items", func(t *testing.T) {
		_, err := prepareWorkflowLaunchParams(jobj(t, `{"gate":"npm test"}`), jobj(t, `{"resume":"retained-run","task":"Continue"}`), "workflow-run", "continue", WorkflowLaunchOptions{})
		hasMatch(t, err, "gate is not supported with retained resume")
		_, err = prepareWorkflowLaunchParams(jobj(t, `{}`), jobj(t, `{"resume":"retained-run","task":"Continue","gate":"npm test"}`), "workflow-run", "continue", WorkflowLaunchOptions{})
		hasMatch(t, err, "gate is not supported with retained resume")
	})
	tw(t, f, "rejects extension binding amendments on retained resume items", func(t *testing.T) {
		_, err := prepareWorkflowLaunchParams(jobj(t, `{"extensionBindings":{"defaults.policy/1":true}}`), jobj(t, `{"resume":"retained-run","task":"Continue"}`), "workflow-run", "continue", WorkflowLaunchOptions{})
		hasMatch(t, err, "original retained child binding")
	})
	tw(t, f, "preserves execution limits and fan-out identity when routing retained resume items", func(t *testing.T) {
		budget := jobj(t, `{"version":1,"rootRunId":"root-run","directory":"/tmp/fanout","limit":64,"parentPath":"parent"}`)
		jsonEq(t, prep(t, `{"toolBudget":{"hard":12,"block":["read"]}}`, `{"resume":" retained-run ","task":"Continue carefully","maxRuntimeMs":5000,"toolBudget":{"soft":2,"hard":4,"block":"*"}}`, "continue", WorkflowLaunchOptions{MissionDetached: true, RunFanoutBudget: budget}),
			`{"action":"resume","id":"retained-run","message":"Continue carefully","workflowParentRunId":"workflow-run","workflowKey":"continue","runFanoutBudget":{"version":1,"rootRunId":"root-run","directory":"/tmp/fanout","limit":64,"parentPath":"parent/workflow[continue]"},"mission":false,"timeoutMs":5000,"toolBudget":{"soft":2,"hard":4,"block":"*"}}`)
	})
	tw(t, f, "replaces Windows-invalid characters and trims separators", func(t *testing.T) {
		eq(t, sanitizeRunPathSegment("call_VfdHQygxGeL1L49ez04A4tf7|WtnJ9gVpB/jdQGWbnKhfMgDqPGUGmNw", 120), "call_VfdHQygxGeL1L49ez04A4tf7_WtnJ9gVpB_jdQGWbnKhfMgDqPGUGmNw", "pipe")
		eq(t, sanitizeRunPathSegment(":::path//sub?*<file>|name:::", 120), "path_sub_file_name", "colons")
		eq(t, sanitizeRunPathSegment("   ___invalid___   ", 120), "invalid", "spaces")
	})
	tw(t, f, "falls back to unknown for empty or all-invalid strings", func(t *testing.T) {
		for _, s := range []string{"", "   ", "???///|||"} {
			eq(t, sanitizeRunPathSegment(s, 120), "unknown", s)
		}
	})
	tw(t, f, "bounds oversized segments to the maximum byte length", func(t *testing.T) {
		got := sanitizeRunPathSegment(strings.Repeat("a", 200), 120)
		eq(t, len(got), 120, "length")
		eq(t, got, strings.Repeat("a", 120), "value")
	})
}
