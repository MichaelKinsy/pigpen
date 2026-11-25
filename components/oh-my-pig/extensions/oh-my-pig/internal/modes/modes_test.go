package modes

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/gate"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/jobs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/toolcmd"
)

func newTestExtension(t *testing.T, opts hostOptions) (*manager, *fakeHost) {
	t.Helper()
	m := newManager()
	m.loopWait = 0 // avoid 800ms wall-clock sleep in unit tests unless explicitly tested
	storeMu.Lock()
	prev := defaultStore
	defaultStore = m
	storeMu.Unlock()
	prevParent := ParentSession
	ParentSession = nil
	t.Cleanup(func() {
		storeMu.Lock()
		defaultStore = prev
		storeMu.Unlock()
		ParentSession = prevParent
		gate.ResetForTests()
		lifecycle.ResetForTests()
		toolcmd.ResetForTests()
	})
	ext := sdk.New("oh-my-pig-modes-test")
	registerWithManager(ext, m)
	h := startHost(t, ext, opts)
	return m, h
}

func TestRegisterDoesNotPanic(t *testing.T) {
	t.Cleanup(func() {
		gate.ResetForTests()
		lifecycle.ResetForTests()
		toolcmd.ResetForTests()
	})
	ext := sdk.New("oh-my-pig-test")
	Register(ext)
}

// ── Golden Prompt Rendering Tests ────────────────────────────────────────────

func TestGoldenPromptRenderingMatchesOmp(t *testing.T) {
	// Golden 1: plan-mode-active.md matches omp v19.1.1 prompt.render byte-for-byte.
	wantActiveBytes, err := os.ReadFile(filepath.Join("testdata", "plan-mode-active.golden.txt"))
	if err != nil {
		t.Fatalf("read golden active: %v", err)
	}
	gotActive := renderTemplate(planModeActiveTemplate, templateData{
		Vars: map[string]string{
			"planFilePath":  "local://PLAN.md",
			"askToolName":   "ask",
			"writeToolName": "write",
			"bashToolName":  "bash",
			"editToolName":  "edit",
		},
		Bools: map[string]bool{
			"planExists":         false,
			"proposeIsCommand":   false,
			"askAvailable":       true,
			"taskAvailable":      true,
			"isHashlineEditMode": true,
			"reentry":            false,
			"iterative":          false,
			"scoutAvailable":     true,
		},
	})
	if gotActive != string(wantActiveBytes) {
		t.Fatalf("plan-mode-active.md mismatch vs omp golden:\n--- GOT ---\n%q\n--- WANT ---\n%q", gotActive, string(wantActiveBytes))
	}

	// Golden 2: plan-mode-approved.md matches omp v19.1.1 prompt.render byte-for-byte,
	// including non-re-expansion of `{{planFilePath}}` inside planContent, blank-line
	// collapsing, and table compaction.
	wantApprovedBytes, err := os.ReadFile(filepath.Join("testdata", "plan-mode-approved.golden.txt"))
	if err != nil {
		t.Fatalf("read golden approved: %v", err)
	}
	gotApproved := renderPlanApprovedPrompt(
		"local://auth-plan.md",
		"# Auth Plan\n\nKeep literal {{planFilePath}} intact.\n\n\n| Col A | Col B |\n| ----- | ----- |\n| 1     | 2     |\n",
		true,
		[]string{"read", "edit", "todo"},
	)
	if gotApproved != string(wantApprovedBytes) {
		t.Fatalf("plan-mode-approved.md mismatch vs omp golden:\n--- GOT ---\n%q\n--- WANT ---\n%q", gotApproved, string(wantApprovedBytes))
	}
}

// ── Plan Mode & internal/gate Tests ──────────────────────────────────────────

func TestPlanTitleAndResolution(t *testing.T) {
	// upstream: test/plan-mode/approved-plan.test.ts: "maps a slug to its local plan URL"
	if got := PlanFileURLForSlug("auth-refactor"); got != "local://auth-refactor-plan.md" {
		t.Fatalf("PlanFileURLForSlug = %q", got)
	}

	// upstream: test/plan-mode/approved-plan.test.ts: "accepts a clean identifier as-is"
	title, fn, err := NormalizePlanTitle("my-plan")
	if err != nil || title != "my-plan" || fn != "my-plan.md" {
		t.Fatalf("NormalizePlanTitle(my-plan) = %q, %q, %v", title, fn, err)
	}

	// upstream: test/plan-mode/approved-plan.test.ts: "strips a trailing .md suffix provided by the model"
	title, fn, err = NormalizePlanTitle("my-plan.md")
	if err != nil || title != "my-plan" || fn != "my-plan.md" {
		t.Fatalf("NormalizePlanTitle(my-plan.md) = %q, %q, %v", title, fn, err)
	}

	// upstream: test/plan-mode/approved-plan.test.ts: "converts spaces to hyphens (natural-language titles)"
	title, fn, err = NormalizePlanTitle("My Improvement Plan")
	if err != nil || title != "My-Improvement-Plan" || fn != "My-Improvement-Plan.md" {
		t.Fatalf("NormalizePlanTitle spaces = %q, %q, %v", title, fn, err)
	}

	// upstream: test/plan-mode/approved-plan.test.ts: "throws for path separators"
	if _, _, err := NormalizePlanTitle("../etc/passwd"); err == nil || !strings.Contains(err.Error(), "path separators") {
		t.Fatalf("expected path separator error, got %v", err)
	}

	// upstream: test/plan-mode/approved-plan.test.ts: "replaces separators with spaces and capitalizes"
	if got := HumanizePlanTitle("migrate-mcp-loader"); got != "Migrate mcp loader" {
		t.Fatalf("HumanizePlanTitle = %q", got)
	}
	if got := HumanizePlanTitle("foo--bar__baz"); got != "Foo bar baz" {
		t.Fatalf("HumanizePlanTitle collapsed = %q", got)
	}

	// upstream: test/plan-mode/approved-plan.test.ts: "locates the plan from the supplied title's slug — no rename"
	reader := func(files map[string]string) func(string) (string, bool, error) {
		return func(u string) (string, bool, error) {
			c, ok := files[u]
			return c, ok, nil
		}
	}
	res, err := ResolveApprovedPlan("auth-refactor", "local://PLAN.md", reader(map[string]string{
		"local://auth-refactor-plan.md": "# Auth refactor\n\nbody",
	}), nil)
	if err != nil || res.PlanFilePath != "local://auth-refactor-plan.md" || res.Title != "auth-refactor" {
		t.Fatalf("ResolveApprovedPlan = %+v, %v", res, err)
	}

	// upstream: test/plan-mode/approved-plan.test.ts: "strips a trailing -plan from the supplied title before reconstructing the file"
	res, err = ResolveApprovedPlan("auth-plan", "local://PLAN.md", reader(map[string]string{
		"local://auth-plan.md": "# Auth\n\nbody",
	}), nil)
	if err != nil || res.PlanFilePath != "local://auth-plan.md" {
		t.Fatalf("ResolveApprovedPlan -plan = %+v, %v", res, err)
	}

	// upstream: test/plan-mode/approved-plan.test.ts: "prefers the newest listed plan over a completed state plan"
	res, err = ResolveApprovedPlan("Different title", "local://completed-plan.md", reader(map[string]string{
		"local://completed-plan.md": "# Old",
		"local://new-draft-plan.md": "# New",
	}), func() ([]string, error) {
		return []string{"local://new-draft-plan.md", "local://completed-plan.md"}, nil
	})
	if err != nil || res.PlanFilePath != "local://new-draft-plan.md" {
		t.Fatalf("ResolveApprovedPlan newest = %+v, %v", res, err)
	}

	// upstream: test/plan-mode/approved-plan.test.ts: "throws an actionable error when no plan file exists"
	if _, err := ResolveApprovedPlan("ghost", "local://PLAN.md", reader(map[string]string{}), nil); err == nil || !strings.Contains(err.Error(), "Plan file not found at local://ghost-plan.md") {
		t.Fatalf("expected missing plan error, got %v", err)
	}
}

func TestPlanModeGatePolicyAndSessionErrorFailClosed(t *testing.T) {
	cwd := t.TempDir()
	sessDir := t.TempDir()
	sessionFile := filepath.Join(sessDir, "session-gate.jsonl")
	localRoot := filepath.Join(sessDir, "session-gate", "local")
	if err := os.MkdirAll(localRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	// Create a symlink inside localRoot pointing outside to cwd (symlink escape test).
	escapeLink := filepath.Join(localRoot, "escape-plan.md")
	escapeTarget := filepath.Join(cwd, "escaped.go")
	if err := os.Symlink(escapeTarget, escapeLink); err != nil {
		t.Fatal(err)
	}

	m := newManager()
	m.loopWait = 0
	storeMu.Lock()
	prev := defaultStore
	defaultStore = m
	storeMu.Unlock()
	prevParent := ParentSession
	ParentSession = nil
	t.Cleanup(func() {
		storeMu.Lock()
		defaultStore = prev
		storeMu.Unlock()
		ParentSession = prevParent
		gate.ResetForTests()
		lifecycle.ResetForTests()
		toolcmd.ResetForTests()
	})

	ext := sdk.New("gate-test")
	registerWithManager(ext, m)
	var gateErrs []error
	var planActiveChild bool
	ext.RegisterCommand("run-gate-checks", sdk.CommandOptions{
		Description: "probe gate.Check inside real sdk.Context",
		Handler: func(ctx sdk.Context, _ string) error {
			reqs := []gate.Request{
				{Tool: "write", Op: gate.OpWrite, Path: filepath.Join(cwd, "src", "main.go")},
				{Tool: "write", Op: gate.OpWrite, Path: filepath.Join(localRoot, "auth-plan.md"), URL: "local://auth-plan.md"},
				{Tool: "write", Op: gate.OpWrite, Path: escapeLink, URL: "local://escape-plan.md"},
				{Tool: "edit", Op: gate.OpEdit, Path: filepath.Join(cwd, "src", "main.go")},
				{Tool: "edit", Op: gate.OpEdit, Path: filepath.Join(localRoot, "auth-plan.md")},
				{Tool: "edit", Op: gate.OpDelete, Path: filepath.Join(localRoot, "auth-plan.md")},
				{Tool: "edit", Op: gate.OpMove, Path: filepath.Join(localRoot, "auth-plan.md"), Dest: filepath.Join(cwd, "evil.go")},
				{Tool: "bash", Op: gate.OpExec, Detail: "git status"},
				{Tool: "github", Op: gate.OpExec, Detail: "pr_create"},
				{Tool: "eval", Op: gate.OpExec, Detail: "agent-custom-tools"},
				{Tool: "eval", Op: gate.OpExec, Detail: "1+1"},
			}
			gateErrs = make([]error, len(reqs))
			for i, r := range reqs {
				gateErrs[i] = gate.Check(ctx, r)
			}
			return nil
		},
	})
	h := startHost(t, ext, hostOptions{
		Cwd:         cwd,
		SessionID:   "sess-gate-parent",
		SessionFile: sessionFile,
	})

	// N1: Register must NOT install a `tool_call` hook — plan mode is enforced
	// strictly through `internal/gate` on resolved targets (as omp's enforce_write).
	if len(h.handlers[sdk.EventToolCall]) != 0 {
		t.Fatalf("expected no tool_call hook registered by modes, got %v", h.handlers[sdk.EventToolCall])
	}

	// Enter plan mode.
	if _, errMsg := h.callCommand("plan", ""); errMsg != "" {
		t.Fatalf("/plan failed: %s", errMsg)
	}
	if _, errMsg := h.callCommand("run-gate-checks", ""); errMsg != "" {
		t.Fatalf("run-gate-checks failed: %s", errMsg)
	}

	// Verify exact omp policy verdicts.
	wantBlocked := []struct {
		idx     int
		blocked bool
		substr  string
		label   string
	}{
		{0, true, "working tree is read-only", "write working tree"},
		{1, false, "", "write inside local:// sandbox"},
		{2, true, "working tree is read-only", "write via symlink escaping sandbox"},
		{3, true, "working tree is read-only", "edit working tree"},
		{4, false, "", "edit inside local:// sandbox"},
		{5, true, "deleting files is not allowed", "delete even inside sandbox"},
		{6, true, "renaming files is not allowed", "move/rename"},
		{7, false, "", "bash allowed (omp prompt-governed only)"},
		{8, true, "working tree is read-only", "github pr_create OpExec"},
		{9, true, "Eval-defined tools are unavailable in plan mode", "eval agent custom tools"},
		{10, false, "", "eval normal cell"},
	}
	for _, tc := range wantBlocked {
		err := gateErrs[tc.idx]
		if tc.blocked {
			if err == nil || !strings.Contains(err.Error(), tc.substr) {
				t.Errorf("%s: got err=%v, want containing %q", tc.label, err, tc.substr)
			}
		} else if err != nil {
			t.Errorf("%s: got unexpected err=%v", tc.label, err)
		}
	}

	// Verify child session inherits gate.PlanActive through ParentSession.
	ParentSession = func(ctx sdk.Context) (string, bool) {
		if id, _ := ctx.GetSessionID(); id == "sess-gate-child" {
			return "sess-gate-parent", true
		}
		return "", false
	}
	childExt := sdk.New("child-probe")
	childExt.RegisterCommand("probe-child", sdk.CommandOptions{
		Description: "probe child",
		Handler: func(ctx sdk.Context, _ string) error {
			planActiveChild = gate.PlanActive(ctx)
			return nil
		},
	})
	childHost := startHost(t, childExt, hostOptions{SessionID: "sess-gate-child"})
	if _, errMsg := childHost.callCommand("probe-child", ""); errMsg != "" {
		t.Fatalf("child probe failed: %s", errMsg)
	}
	if !planActiveChild {
		t.Fatalf("expected child session to see gate.PlanActive(ctx) == true when parent is planning")
	}

	// N4: When the host fails to return a session ID while plan mode is active,
	// planGatePolicy must fail closed rather than allowing working-tree writes.
	var errHostGateErr error
	errExt := sdk.New("err-sid-probe")
	errExt.RegisterCommand("probe-err-sid", sdk.CommandOptions{
		Description: "probe gate when getSessionID errors",
		Handler: func(ctx sdk.Context, _ string) error {
			errHostGateErr = gate.Check(ctx, gate.Request{
				Tool: "write",
				Op:   gate.OpWrite,
				Path: filepath.Join(cwd, "src", "main.go"),
			})
			return nil
		},
	})
	errHost := startHost(t, errExt, hostOptions{
		Cwd:                  cwd,
		OmitSessionIDInReady: true,
		OnCall: func(method string, _ map[string]any) (map[string]any, string) {
			if method == "getSessionID" {
				return nil, "session host disconnected"
			}
			return nil, ""
		},
	})
	if _, errMsg := errHost.callCommand("probe-err-sid", ""); errMsg != "" {
		t.Fatalf("probe-err-sid failed: %s", errMsg)
	}
	if errHostGateErr == nil || !strings.Contains(errHostGateErr.Error(), "working tree is read-only") {
		t.Fatalf("expected fail-closed gate error when session ID cannot be resolved while planning, got %v", errHostGateErr)
	}
}

// TestProposeSpecialCommandExposureAndMountedExecution verifies the new
// `internal/toolcmd` contract for `propose`:
//   - `propose` registers with `Exposure: sdk.ToolExposureCodemode` and `DefaultActive: false`.
//   - While bash transport is active (`bash` in activeTools), `toolcmd` keeps `propose`
//     out of activeTools and exposes it in `toolcmd.CommandNames(ctx)`.
//   - Even though `propose` is inactive in activeTools, a tool (simulating mounted
//     bash execution) can call it via `ctx.ExecuteTool("propose", ...)` because the
//     fake host enforces PiG's rule (`direct` requires active; `codemode` always callable).
//   - Conversely, a `direct` tool (`goal`) that is inactive is REFUSED by `ctx.ExecuteTool`.
//   - When bash transport is off (`bash` not active), `toolcmd` activates `propose`
//     top-level ONLY while plan mode is active and deactivates it on plan exit.
func TestProposeSpecialCommandExposureAndMountedExecution(t *testing.T) {
	cwd := t.TempDir()
	sessDir := t.TempDir()
	sessionFile := filepath.Join(sessDir, "propose-mounted.jsonl")
	planDir := filepath.Join(sessDir, "propose-mounted", "local")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(planDir, "mounted-plan.md"), []byte("# Mounted Plan\n\nStep 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := newManager()
	m.loopWait = 0
	storeMu.Lock()
	prev := defaultStore
	defaultStore = m
	storeMu.Unlock()
	t.Cleanup(func() {
		storeMu.Lock()
		defaultStore = prev
		storeMu.Unlock()
		gate.ResetForTests()
		lifecycle.ResetForTests()
		toolcmd.ResetForTests()
	})

	ext := sdk.New("propose-toolcmd-test")
	toolcmd.Register(ext)
	registerWithManager(ext, m)

	// A mock tool that invokes a nested tool via `ctx.ExecuteTool`,
	// exercising PiG's real exposure rule in the fake host.
	var nestedOutcome sdk.AgentToolCallOutcome
	var nestedErr error
	ext.RegisterTool(sdk.ToolDefinition{
		Name:        "run_nested",
		Description: "invokes ExecuteTool inside a tool context",
		Parameters:  sdk.Schema{"type": "object", "properties": map[string]any{"target": map[string]any{"type": "string"}, "args": map[string]any{"type": "object"}}},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			target, _ := params["target"].(string)
			nestedArgs, _ := params["args"].(map[string]any)
			nestedOutcome, nestedErr = ctx.ExecuteTool(target, nestedArgs, nil)
			return sdk.ToolResult{Content: nestedOutcome.Result.Text(), IsError: nestedOutcome.IsError}, nil
		},
	})

	h := startHost(t, ext, hostOptions{
		Cwd:         cwd,
		SessionID:   "sess-propose-mount",
		SessionFile: sessionFile,
		ActiveTools: []string{"read", "write", "edit", "bash", "run_nested"},
	})

	// Verify registered metadata: propose has codemode exposure and DefaultActive=false;
	// goal has direct exposure and DefaultActive=false.
	proposeMeta, ok := h.tools[proposeToolName]
	if !ok || proposeMeta.Exposure != string(sdk.ToolExposureCodemode) || proposeMeta.DefaultActive == nil || *proposeMeta.DefaultActive {
		t.Fatalf("propose registration = %+v, want exposure=codemode defaultActive=false", proposeMeta)
	}
	goalMeta, ok := h.tools[goalToolName]
	if !ok || goalMeta.Exposure != string(sdk.ToolExposureDirect) || goalMeta.DefaultActive == nil || *goalMeta.DefaultActive {
		t.Fatalf("goal registration = %+v, want exposure=direct defaultActive=false", goalMeta)
	}

	// Fire session_start on BOTH handlers (toolcmd + modes).
	for _, hid := range h.handlers[sdk.EventSessionStart] {
		raw, _ := json.Marshal(map[string]any{"type": "session_start"})
		_, errMsg := h.roundTrip(map[string]any{"method": "event", "event": sdk.EventSessionStart, "handler_id": hid, "args": json.RawMessage(raw)})
		if errMsg != "" {
			t.Fatalf("session_start handler %d failed: %s", hid, errMsg)
		}
	}

	// Enter plan mode while bash transport is active.
	if _, errMsg := h.callCommand("plan", ""); errMsg != "" {
		t.Fatalf("/plan failed: %s", errMsg)
	}

	// `propose` must stay OUT of activeTools (mounted as bash special command).
	h.mu.Lock()
	proposeActiveWithBash := containsString(h.activeTools, proposeToolName)
	h.mu.Unlock()
	if proposeActiveWithBash {
		t.Fatalf("propose must not be in activeTools while bash transport is active; activeTools=%v", h.activeTools)
	}

	// Calling inactive `propose` through host ExecuteTool MUST succeed (codemode exposure).
	if _, errMsg := h.callTool("run_nested", map[string]any{
		"target": "propose",
		"args":   map[string]any{"title": "mounted"},
	}); errMsg != "" {
		t.Fatalf("run_nested(propose) failed: %s", errMsg)
	}
	if nestedErr != nil || nestedOutcome.IsError || !strings.Contains(nestedOutcome.Result.Text(), "Plan ready for review.") {
		t.Fatalf("mounted ExecuteTool(propose) = outcome=%+v err=%v", nestedOutcome, nestedErr)
	}

	// Prove the fake host enforces the direct-exposure rule: inactive `goal` (direct) MUST fail.
	if _, errMsg := h.callTool("run_nested", map[string]any{
		"target": "goal",
		"args":   map[string]any{"op": "get"},
	}); errMsg != "" {
		t.Fatalf("run_nested(goal) failed: %s", errMsg)
	}
	if !nestedOutcome.IsError || !strings.Contains(nestedOutcome.Result.Text(), "not active") {
		t.Fatalf("expected inactive direct tool `goal` to be refused by host rule, got %+v", nestedOutcome)
	}

	// Now test transport OFF (`bash` absent): toolcmd must activate `propose` top-level
	// while plan mode is active, and remove it when plan mode exits.
	extNoBash := sdk.New("propose-toolcmd-nobash")
	toolcmd.Register(extNoBash)
	registerWithManager(extNoBash, m)
	hNoBash := startHost(t, extNoBash, hostOptions{
		Cwd:         cwd,
		SessionID:   "sess-propose-nobash",
		SessionFile: sessionFile,
		ActiveTools: []string{"read", "write", "edit"},
	})
	if _, errMsg := hNoBash.callCommand("plan", ""); errMsg != "" {
		t.Fatalf("no-bash /plan failed: %s", errMsg)
	}
	hNoBash.mu.Lock()
	proposeActiveNoBash := containsString(hNoBash.activeTools, proposeToolName)
	hNoBash.mu.Unlock()
	if !proposeActiveNoBash {
		t.Fatalf("expected toolcmd to activate propose top-level when bash transport is off and plan mode is active; activeTools=%v", hNoBash.activeTools)
	}
	// Pause plan mode -> toolcmd must deactivate `propose`.
	if _, errMsg := hNoBash.callCommand("plan", ""); errMsg != "" {
		t.Fatalf("no-bash /plan exit failed: %s", errMsg)
	}
	hNoBash.mu.Lock()
	proposeAfterExit := containsString(hNoBash.activeTools, proposeToolName)
	hNoBash.mu.Unlock()
	if proposeAfterExit {
		t.Fatalf("expected toolcmd to deactivate propose when plan mode exits; activeTools=%v", hNoBash.activeTools)
	}
}

func TestPlanApprovalPathsCommandVsEventContext(t *testing.T) {
	cwd := t.TempDir()
	sessDir := t.TempDir()
	sessionFile := filepath.Join(sessDir, "plan-appr.jsonl")
	planDir := filepath.Join(sessDir, "plan-appr", "local")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	planContent := "# Auth Refactor\n\n## Approach\n1. Update auth.go\n"
	if err := os.WriteFile(filepath.Join(planDir, "auth-refactor-plan.md"), []byte(planContent), 0o644); err != nil {
		t.Fatal(err)
	}
	// Secondary artifact to verify copyLocalArtifacts on Approve and execute.
	if err := os.WriteFile(filepath.Join(planDir, "notes.md"), []byte("scratch note"), 0o644); err != nil {
		t.Fatal(err)
	}

	var selectChoice string
	var compactCancel atomic.Bool
	m, h := newTestExtension(t, hostOptions{
		Cwd:               cwd,
		SessionID:         "sess-appr-1",
		SessionFile:       sessionFile,
		ReplacementSessID: "sess-appr-2",
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "ui.select" {
				return map[string]any{"ok": true, "selected": selectChoice}, ""
			}
			if method == "compact" && compactCancel.Load() {
				return nil, "Compaction cancelled"
			}
			return nil, ""
		},
	})
	h.fireEvent(sdk.EventSessionStart, map[string]any{"type": "session_start"})

	// 1. From `agent_end` (after `propose`), "Approve and execute" is NOT offered so
	// plan mode cannot be left half-exited by PiG's command-only NewSession guard.
	if _, errMsg := h.callCommand("plan", ""); errMsg != "" {
		t.Fatal(errMsg)
	}
	if _, errMsg := h.callTool("propose", map[string]any{"title": "auth-refactor"}); errMsg != "" {
		t.Fatalf("propose failed: %s", errMsg)
	}
	selectChoice = "Approve in fresh session (run /plan-review)"
	h.fireEvent(sdk.EventAgentEnd, map[string]any{"type": "agent_end", "messages": []any{}})
	// Plan mode must still be active and no newSession call attempted from event context.
	m.mu.Lock()
	stillPlanning := m.stateLocked("sess-appr-1").planEnabled
	m.mu.Unlock()
	if !stillPlanning {
		t.Fatalf("plan mode should remain active when agent_end directs user to /plan-review")
	}
	if len(h.callsTo("newSession")) != 0 {
		t.Fatalf("agent_end must not call command-only newSession")
	}

	// 2. From `/plan-review` (command context), "Approve and execute" uses
	// `withSession`, copies local artifacts, sets name on the NEW session, and
	// dispatches `plan-mode-approved` on the replacement context.
	selectChoice = "Approve and execute"
	if _, errMsg := h.callCommand("plan-review", ""); errMsg != "" {
		t.Fatalf("/plan-review Approve and execute failed: %s", errMsg)
	}
	if len(h.callsTo("newSession")) != 1 {
		t.Fatalf("expected 1 newSession call, got %d", len(h.callsTo("newSession")))
	}
	foundApproved := false
	for _, c := range h.callsTo("sendMessage") {
		msg, _ := c.Args["message"].(map[string]any)
		if msg["customType"] == "plan-mode-approved" && strings.Contains(msg["content"].(string), "Update auth.go") {
			foundApproved = true
		}
	}
	if !foundApproved {
		t.Fatalf("expected plan-mode-approved dispatched via withSession")
	}

	// 3. "Approve and compact context" when compaction is cancelled skips dispatch
	// (upstream interactive-mode.ts:7226-7237) and warns the user.
	m2, h2 := newTestExtension(t, hostOptions{
		Cwd:         cwd,
		SessionID:   "sess-appr-compact",
		SessionFile: sessionFile,
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "ui.select" {
				return map[string]any{"ok": true, "selected": "Approve and compact context"}, ""
			}
			if method == "compact" && compactCancel.Load() {
				return nil, "Compaction cancelled"
			}
			return nil, ""
		},
	})
	_ = m2
	h2.fireEvent(sdk.EventSessionStart, map[string]any{"type": "session_start"})
	_, _ = h2.callCommand("plan", "")
	compactCancel.Store(true)
	if _, errMsg := h2.callCommand("plan-review", ""); errMsg != "" {
		t.Fatalf("/plan-review compact cancel failed: %s", errMsg)
	}
	if !strings.Contains(h2.lastNotify(), "compaction was cancelled — execution not dispatched") {
		t.Fatalf("expected cancelled compaction warning, got %q", h2.lastNotify())
	}
	for _, c := range h2.callsTo("sendMessage") {
		if msg, _ := c.Args["message"].(map[string]any); msg["customType"] == "plan-mode-approved" {
			t.Fatalf("must not dispatch plan-mode-approved when compaction was cancelled")
		}
	}
}

// TestPlanApproveCompactFromAgentEndDoesNotDeadlock verifies N2:
// PiG's manual compaction (`coding/session.go:2947-2952`) waits for the active
// run to become idle before compacting. Here the fake host holds the `compact`
// RPC until AFTER `agent_end` has returned (simulating `WaitForIdle`), so any
// handler that blocks on compaction inside `agent_end` deadlocks and fails
// within < 2 s. It also tests both completion (dispatches `plan-mode-approved`)
// and cancellation (`"Compaction cancelled"` / `"This operation was aborted"`).
func TestPlanApproveCompactFromAgentEndDoesNotDeadlock(t *testing.T) {
	cwd := t.TempDir()
	sessDir := t.TempDir()
	sessionFile := filepath.Join(sessDir, "plan-compact-async.jsonl")
	planDir := filepath.Join(sessDir, "plan-compact-async", "local")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	planContent := "# Async Compact Plan\n\n## Steps\n1. Ship non-blocking compaction\n"
	if err := os.WriteFile(filepath.Join(planDir, "async-compact-plan.md"), []byte(planContent), 0o644); err != nil {
		t.Fatal(err)
	}

	runCase := func(t *testing.T, compactErrMsg string, expectDispatch bool) {
		t.Helper()
		agentEndReturned := make(chan struct{})
		compactStarted := make(chan struct{})
		var compactOnce sync.Once
		doneDispatched := make(chan struct{})
		var dispatchOnce sync.Once

		_, h := newTestExtension(t, hostOptions{
			Cwd:         cwd,
			SessionID:   "sess-compact-async-" + t.Name(),
			SessionFile: sessionFile,
			OnCall: func(method string, args map[string]any) (map[string]any, string) {
				switch method {
				case "ui.select":
					return map[string]any{"ok": true, "selected": "Approve and compact context"}, ""
				case "compact":
					compactOnce.Do(func() { close(compactStarted) })
					// Simulate PiG's WaitForIdle: compaction cannot finish until agent_end returns.
					select {
					case <-agentEndReturned:
					case <-time.After(1500 * time.Millisecond):
						return nil, "deadlock: compact waited for agent_end to return"
					}
					if compactErrMsg != "" {
						return nil, compactErrMsg
					}
					return map[string]any{"summary": "compacted summary"}, ""
				case "sendMessage":
					if msg, _ := args["message"].(map[string]any); msg["customType"] == "plan-mode-approved" {
						dispatchOnce.Do(func() { close(doneDispatched) })
					}
				case "ui.notify":
					if msg, _ := args["message"].(string); strings.Contains(msg, "compaction was cancelled") {
						dispatchOnce.Do(func() { close(doneDispatched) })
					}
				}
				return nil, ""
			},
		})
		h.fireEvent(sdk.EventSessionStart, map[string]any{"type": "session_start"})
		if _, errMsg := h.callCommand("plan", ""); errMsg != "" {
			t.Fatal(errMsg)
		}
		if _, errMsg := h.callTool("propose", map[string]any{"title": "async-compact"}); errMsg != "" {
			t.Fatalf("propose failed: %s", errMsg)
		}

		// agent_end MUST return before compaction finishes (< 1.5 s).
		_, ok := h.fireEventWithTimeout(sdk.EventAgentEnd, map[string]any{"type": "agent_end", "messages": []any{}}, 1500*time.Millisecond)
		if !ok {
			t.Fatalf("agent_end deadlocked waiting for compaction that needs the run to settle")
		}
		close(agentEndReturned)

		// Ensure compaction actually started and its OnComplete/OnError callback ran.
		select {
		case <-compactStarted:
		case <-time.After(1500 * time.Millisecond):
			t.Fatalf("expected CompactWithOptions to start compaction")
		}
		select {
		case <-doneDispatched:
		case <-time.After(1500 * time.Millisecond):
			t.Fatalf("timed out waiting for compaction completion callback to finish")
		}

		var foundApproved bool
		for _, c := range h.callsTo("sendMessage") {
			if msg, _ := c.Args["message"].(map[string]any); msg["customType"] == "plan-mode-approved" {
				foundApproved = true
			}
		}
		if expectDispatch && !foundApproved {
			t.Fatalf("expected plan-mode-approved message after compaction completed")
		}
		if !expectDispatch && foundApproved {
			t.Fatalf("must not dispatch plan-mode-approved when compaction was cancelled (%q)", compactErrMsg)
		}
		if expectDispatch {
			h.mu.Lock()
			gotName := h.sessionName
			h.mu.Unlock()
			if gotName != "Async compact" {
				t.Fatalf("expected humanized session name after compaction, got %q", gotName)
			}
		}
	}

	t.Run("completed", func(t *testing.T) { runCase(t, "", true) })
	t.Run("cancelled_exact", func(t *testing.T) { runCase(t, "Compaction cancelled", false) })
	t.Run("aborted_exact", func(t *testing.T) { runCase(t, "This operation was aborted", false) })
	t.Run("non_cancel_error_dispatches_best_effort", func(t *testing.T) {
		// Non-cancel compaction failure proceeds best-effort (upstream interactive-mode.ts:7143),
		// even if the provider message happens to contain the word "cancel".
		runCase(t, "provider refused request: cancel-token header invalid", true)
	})
}

func TestPlanModeConvergenceCapAndErrorOutcome(t *testing.T) {
	_, h := newTestExtension(t, hostOptions{SessionID: "sess-plan-conv"})
	_, _ = h.callCommand("plan", "")

	// Error outcome in agent_before_settle must NOT inject a reminder.
	errRaw := h.fireEvent(sdk.EventAgentBeforeSettle, map[string]any{
		"type":    "agent_before_settle",
		"outcome": "error",
		"entries": []any{},
	})
	if strings.Contains(string(errRaw), "plan-mode-reminder") {
		t.Fatalf("must not remind on error outcome: %s", errRaw)
	}

	// Completed turns remind up to PLAN_MODE_REMINDER_MAX (3) times, then stop.
	for i := range 3 {
		h.fireEvent(sdk.EventAgentStart, map[string]any{"type": "agent_start"})
		raw := h.fireEvent(sdk.EventAgentBeforeSettle, map[string]any{
			"type":    "agent_before_settle",
			"outcome": "completed",
			"entries": []any{},
		})
		if !strings.Contains(string(raw), "plan-mode-reminder") || !strings.Contains(string(raw), `"continue":true`) {
			t.Fatalf("reminder %d/3 failed: %s", i+1, raw)
		}
	}
	h.fireEvent(sdk.EventAgentStart, map[string]any{"type": "agent_start"})
	fourth := h.fireEvent(sdk.EventAgentBeforeSettle, map[string]any{
		"type":    "agent_before_settle",
		"outcome": "completed",
		"entries": []any{},
	})
	if strings.Contains(string(fourth), "plan-mode-reminder") {
		t.Fatalf("expected reminder cap at 3, got 4th reminder: %s", fourth)
	}
}

// ── Goal Mode Tests (including Compaction, Interrupt, Replay, Idle Cost) ─────

func TestGoalCompactionPreservesActiveGoalAndBranchOnlyReplay(t *testing.T) {
	// upstream: test/agent-session-goal-midrun-compaction.test.ts
	// Active goal must stay active across compaction (no silent pause), and
	// replay on session_start pauses active goals while keeping budget-limited enabled.
	budget := 100
	activeGoal := Goal{ID: "g-active", Objective: "Ship release", Status: GoalStatusActive, TokenBudget: &budget}
	blGoal := Goal{ID: "g-bl", Objective: "Budget hit", Status: GoalStatusBudgetLimited, TokenBudget: &budget, TokensUsed: 120}

	m, h := newTestExtension(t, hostOptions{
		SessionID: "sess-goal-compact",
		Branch: []map[string]any{
			{"id": "e1", "type": "custom", "customType": entryTypeModeChange, "data": map[string]any{"mode": "goal", "data": map[string]any{"goal": blGoal}}},
		},
		// Entry on an abandoned sibling branch (id "e-abandoned" not in Branch) must be ignored!
		Entries: []map[string]any{
			{"id": "e-abandoned", "type": "custom", "customType": entryTypeModeChange, "data": map[string]any{"mode": "plan", "data": map[string]any{"planFilePath": "local://old.md"}}},
		},
	})

	// Cold session_start replays from current branch only: budget-limited goal stays Enabled=true,
	// and abandoned sibling branch's plan mode is ignored.
	h.fireEvent(sdk.EventSessionStart, map[string]any{"type": "session_start"})
	m.mu.Lock()
	st := m.stateLocked("sess-goal-compact")
	if st.planEnabled {
		m.mu.Unlock()
		t.Fatalf("abandoned branch plan mode leaked into active branch replay")
	}
	if st.goalState == nil || !st.goalState.Enabled || st.goalState.Goal.Status != GoalStatusBudgetLimited {
		m.mu.Unlock()
		t.Fatalf("expected budget-limited goal to stay enabled after replay, got %+v", st.goalState)
	}
	m.mu.Unlock()

	// Drop budget-limited goal, start a fresh active goal, then simulate compaction:
	// there is no session_compact replay handler, so live state stays active.
	_, _ = h.callCommand("goal", "drop")
	_, _ = h.callCommand("goal", activeGoal.Objective)
	m.mu.Lock()
	if st.goalState == nil || !st.goalState.Enabled || st.goalState.Goal.Status != GoalStatusActive {
		m.mu.Unlock()
		t.Fatalf("expected active goal before compaction")
	}
	m.mu.Unlock()
	// Verify session_compact is not even registered to clobber state.
	if len(h.handlers[sdk.EventSessionCompact]) != 0 {
		t.Fatalf("session_compact should not be hooked to replay/pause active goals")
	}

	// Simulate Esc abort on agent_end (real PiG payload: {type, messages} with stopReason=aborted).
	// upstream: test/goals/goal-runtime.test.ts interrupt pauses active goal
	h.fireEvent(sdk.EventAgentEnd, map[string]any{
		"type": "agent_end",
		"messages": []any{
			map[string]any{"role": "assistant", "stopReason": "aborted", "content": []any{}},
		},
	})
	m.mu.Lock()
	if st.goalState == nil || st.goalState.Enabled || st.goalState.Goal.Status != GoalStatusPaused {
		m.mu.Unlock()
		t.Fatalf("expected Esc abort (stopReason=aborted) to pause active goal, got %+v", st.goalState)
	}
	m.mu.Unlock()
}

func TestGoalNoBranchWalkWhenInactive(t *testing.T) {
	// upstream: goals/runtime.ts:216-238: idle sessions skip usage walk.
	_, h := newTestExtension(t, hostOptions{SessionID: "sess-no-goal"})
	h.fireEvent(sdk.EventAgentStart, map[string]any{"type": "agent_start"})
	h.fireEvent(sdk.EventToolResult, map[string]any{"type": "tool_result", "toolName": "read", "isError": false})
	h.fireEvent(sdk.EventAgentBeforeSettle, map[string]any{"type": "agent_before_settle", "outcome": "completed", "entries": []any{}})
	h.fireEvent(sdk.EventAgentEnd, map[string]any{"type": "agent_end", "messages": []any{}})

	if n := len(h.callsTo("watchSessionLog")); n != 0 {
		t.Fatalf("expected 0 session log walks when no goal is active, got %d", n)
	}
}

func TestGoalStallDetectionAndBudgetFlip(t *testing.T) {
	// Two consecutive continuation turns with identical tool activity stop continuing.
	m, h := newTestExtension(t, hostOptions{
		SessionID: "sess-goal-stall",
		Branch: []map[string]any{
			{"id": "u1", "type": "message", "message": map[string]any{"role": "user", "content": "start"}},
			{"id": "a1", "type": "message", "message": map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{"type": "toolCall", "id": "c1", "name": "read", "arguments": map[string]any{"path": "a.go"}},
				},
				"usage": map[string]any{"input": 50, "output": 50},
			}},
		},
	})
	_, _ = h.callCommand("goal", "Finish task")

	// First settle triggers continuation #1.
	c1 := h.fireEvent(sdk.EventAgentBeforeSettle, map[string]any{"type": "agent_before_settle", "outcome": "completed", "entries": []any{}})
	if !strings.Contains(string(c1), "goal-continuation") {
		t.Fatalf("expected 1st continuation, got %s", c1)
	}
	// Second settle has non-empty activity (first time seeing it), continues.
	c2 := h.fireEvent(sdk.EventAgentBeforeSettle, map[string]any{"type": "agent_before_settle", "outcome": "completed", "entries": []any{}})
	if !strings.Contains(string(c2), "goal-continuation") {
		t.Fatalf("expected 2nd continuation, got %s", c2)
	}
	// Third settle has IDENTICAL activity -> suppressed (stall detected).
	c3 := h.fireEvent(sdk.EventAgentBeforeSettle, map[string]any{"type": "agent_before_settle", "outcome": "completed", "entries": []any{}})
	if strings.Contains(string(c3), "goal-continuation") {
		t.Fatalf("expected stall suppression on repeated identical activity, got %s", c3)
	}

	// Setting a budget below tokensUsed flips to budget-limited and sends steer.
	m.mu.Lock()
	m.stateLocked("sess-goal-stall").goalState.Goal.TokensUsed = 200
	m.mu.Unlock()
	_, _ = h.callCommand("goal", "budget 100")
	foundSteer := false
	for _, c := range h.callsTo("sendMessage") {
		if msg, _ := c.Args["message"].(map[string]any); msg["customType"] == "goal-budget-limit" {
			foundSteer = true
		}
	}
	if !foundSteer {
		t.Fatalf("expected goal-budget-limit steer when budget reached")
	}
}

// ── Loop Mode Tests (Abort, Provider Error, Timeout, Cancel) ─────────────────

func TestLoopPausesOnProviderErrorAndAbort(t *testing.T) {
	// upstream: test/interactive-mode-loop-provider-error.test.ts:
	// "pauses an armed loop after a terminal provider error instead of duplicating its @file attachment"
	m, h := newTestExtension(t, hostOptions{SessionID: "sess-loop-err"})
	_, _ = h.callCommand("loop", "5 @task.md")
	initialSends := len(h.callsTo("sendUserMessage"))

	// Terminal provider error on agent_end pauses the loop instead of resubmitting.
	h.fireEvent(sdk.EventAgentEnd, map[string]any{
		"type": "agent_end",
		"messages": []any{
			map[string]any{"role": "assistant", "stopReason": "error", "errorMessage": "rate limit"},
		},
	})
	if len(h.callsTo("sendUserMessage")) != initialSends {
		t.Fatalf("loop must not resubmit after stopReason=error")
	}
	m.mu.Lock()
	st := m.stateLocked("sess-loop-err")
	if !st.loopPaused || st.loopPrompt != "" {
		m.mu.Unlock()
		t.Fatalf("expected loop paused with cleared prompt on error, got paused=%v prompt=%q", st.loopPaused, st.loopPrompt)
	}
	m.mu.Unlock()

	// Re-arm with a prompt, then simulate Esc abort (stopReason=aborted).
	h.fireEvent(sdk.EventInput, map[string]any{"type": "input", "text": "retry task", "source": "interactive"})
	h.fireEvent(sdk.EventAgentEnd, map[string]any{
		"type": "agent_end",
		"messages": []any{
			map[string]any{"role": "assistant", "stopReason": "aborted"},
		},
	})
	m.mu.Lock()
	if !st.loopPaused || st.loopPrompt != "" {
		m.mu.Unlock()
		t.Fatalf("expected loop paused on stopReason=aborted")
	}
	m.mu.Unlock()
}

func TestLoopLimitConditionAndTimeout(t *testing.T) {
	// upstream: test/loop-limit.test.ts + test/loop-condition.test.ts
	p, errMsg := ParseLoopArgs("10 minutes keep fixing")
	if errMsg != "" || p.Limit == nil || p.Limit.DurationMs != 600_000 || p.Prompt != "keep fixing" {
		t.Fatalf("10 minutes = %+v, %q", p, errMsg)
	}
	p, errMsg = ParseLoopArgs(`5 --until 'go test ./...' fix failing tests`)
	if errMsg != "" || p.Limit.Iterations != 5 || !p.Condition.Until || p.Condition.Command != "go test ./..." {
		t.Fatalf("full loop parse = %+v, %q", p, errMsg)
	}

	// Timeout verdict when exec reports Killed without explicit cancel or truncation.
	// upstream: test/loop-condition.test.ts: "stops the loop when the condition outruns its deadline"
	_, h := newTestExtension(t, hostOptions{
		SessionID: "sess-loop-to",
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "exec" {
				return map[string]any{"stdout": "", "stderr": "", "code": 143, "killed": true, "truncated": false}, ""
			}
			return nil, ""
		},
	})
	_, _ = h.callCommand("loop", `--while 'sleep 60' keep going`)
	h.fireEvent(sdk.EventAgentEnd, map[string]any{
		"type":     "agent_end",
		"messages": []any{map[string]any{"role": "assistant", "stopReason": "stop"}},
	})
	if !strings.Contains(h.lastNotify(), "timed out after 30s. Loop mode disabled.") {
		t.Fatalf("expected timeout message, got %q", h.lastNotify())
	}
}

// TestLoopEscapeStopsGraceWaitAndConditionAndGoalAbortAndWindowsShell verifies N3:
//  1. Aborting ctx.Signal() (Escape) during the 800 ms grace wait stops the wait
//     promptly and pauses the loop without resubmitting.
//  2. Aborting ctx.Signal() (Escape) while a condition command is running sends
//     `exec.cancel` to the host and pauses the loop without disabling it.
//  3. When both goal and loop are active and a turn is aborted, BOTH are paused.
//  4. Windows shell resolution refuses with a clear error instead of falling back
//     to cmd.exe when no POSIX bash/sh is installed.
func TestLoopEscapeStopsGraceWaitAndConditionAndGoalAbortAndWindowsShell(t *testing.T) {
	// 1. Escape during grace wait.
	t.Run("escape_during_grace_wait", func(t *testing.T) {
		m, h := newTestExtension(t, hostOptions{SessionID: "sess-loop-esc-wait"})
		m.loopWait = 2 * time.Second
		_, _ = h.callCommand("loop", "5 keep working")
		initialSends := len(h.callsTo("sendUserMessage"))

		// Mark run active before agent_end; abort run_signal shortly after agent_end starts waiting.
		h.sendRunSignal(1, true, false)
		done := make(chan struct{})
		go func() {
			defer close(done)
			h.fireEvent(sdk.EventAgentEnd, map[string]any{
				"type":     "agent_end",
				"messages": []any{map[string]any{"role": "assistant", "stopReason": "stop"}},
			})
		}()
		time.Sleep(80 * time.Millisecond)
		h.sendRunSignal(1, true, true)

		select {
		case <-done:
		case <-time.After(1 * time.Second):
			t.Fatalf("Escape (run_signal abort) did not interrupt the loop grace wait")
		}
		if len(h.callsTo("sendUserMessage")) != initialSends {
			t.Fatalf("loop must not resubmit after Escape during grace wait")
		}
		m.mu.Lock()
		st := m.stateLocked("sess-loop-esc-wait")
		paused, enabled, prompt := st.loopPaused, st.loopEnabled, st.loopPrompt
		m.mu.Unlock()
		if !enabled || !paused || prompt != "" {
			t.Fatalf("expected loop enabled+paused with cleared prompt, got enabled=%v paused=%v prompt=%q", enabled, paused, prompt)
		}
	})

	// 2. Escape while a condition command is running cancels the exec and pauses the loop.
	t.Run("escape_during_condition_command", func(t *testing.T) {
		execStarted := make(chan struct{})
		execCancelled := make(chan struct{})
		var startOnce, cancelOnce sync.Once

		m, h := newTestExtension(t, hostOptions{
			SessionID: "sess-loop-esc-cond",
			OnCall: func(method string, args map[string]any) (map[string]any, string) {
				switch method {
				case "exec":
					startOnce.Do(func() { close(execStarted) })
					select {
					case <-execCancelled:
						return map[string]any{"stdout": "", "stderr": "", "code": 130, "killed": true, "truncated": false}, ""
					case <-time.After(1500 * time.Millisecond):
						return map[string]any{"stdout": "", "stderr": "", "code": 0, "killed": false, "truncated": false}, ""
					}
				case "exec.cancel":
					cancelOnce.Do(func() { close(execCancelled) })
					return map[string]any{}, ""
				}
				return nil, ""
			},
		})
		_, _ = h.callCommand("loop", `--while 'sleep 30' keep going`)
		initialSends := len(h.callsTo("sendUserMessage"))

		h.sendRunSignal(2, true, false)
		done := make(chan struct{})
		go func() {
			defer close(done)
			h.fireEvent(sdk.EventAgentEnd, map[string]any{
				"type":     "agent_end",
				"messages": []any{map[string]any{"role": "assistant", "stopReason": "stop"}},
			})
		}()

		select {
		case <-execStarted:
		case <-time.After(1 * time.Second):
			t.Fatalf("condition exec did not start")
		}
		h.sendRunSignal(2, true, true)

		select {
		case <-execCancelled:
		case <-time.After(1 * time.Second):
			t.Fatalf("Escape (run_signal abort) did not trigger exec.cancel on running condition")
		}
		select {
		case <-done:
		case <-time.After(1 * time.Second):
			t.Fatalf("agent_end did not finish after condition cancel")
		}
		if len(h.callsTo("sendUserMessage")) != initialSends {
			t.Fatalf("loop must not resubmit after Escape during condition command")
		}
		m.mu.Lock()
		st := m.stateLocked("sess-loop-esc-cond")
		paused, enabled, prompt := st.loopPaused, st.loopEnabled, st.loopPrompt
		m.mu.Unlock()
		if !enabled || !paused || prompt != "" {
			t.Fatalf("expected loop paused (not disabled) on Escape during condition, got enabled=%v paused=%v prompt=%q", enabled, paused, prompt)
		}
	})

	// 3. Goal + loop both active on aborted turn pauses BOTH.
	t.Run("goal_and_loop_both_paused_on_abort", func(t *testing.T) {
		m, h := newTestExtension(t, hostOptions{SessionID: "sess-goal-loop-abort"})
		if _, errMsg := h.callCommand("goal", "Ship feature"); errMsg != "" {
			t.Fatalf("/goal failed: %s", errMsg)
		}
		if _, errMsg := h.callCommand("loop", "5 iterate"); errMsg != "" {
			t.Fatalf("/loop failed: %s", errMsg)
		}
		h.fireEvent(sdk.EventAgentEnd, map[string]any{
			"type":     "agent_end",
			"messages": []any{map[string]any{"role": "assistant", "stopReason": "aborted"}},
		})
		m.mu.Lock()
		st := m.stateLocked("sess-goal-loop-abort")
		goalPaused := st.goalState != nil && !st.goalState.Enabled && st.goalState.Goal.Status == GoalStatusPaused
		loopPaused := st.loopEnabled && st.loopPaused && st.loopPrompt == ""
		m.mu.Unlock()
		if !goalPaused {
			t.Fatalf("expected active goal to be paused on abort")
		}
		if !loopPaused {
			t.Fatalf("expected active loop to ALSO be paused when goal+loop are aborted together")
		}
	})

	// 4. Windows shell lookup refuses instead of falling back to cmd.exe.
	t.Run("windows_shell_refuses_without_posix_bash", func(t *testing.T) {
		neverExists := func(string) bool { return false }
		neverFound := func(string) (string, error) { return "", errors.New("not found") }
		shell, args, err := resolveWindowsBashShell(`C:\Program Files`, `C:\Program Files (x86)`, neverExists, neverFound)
		if err == nil {
			t.Fatalf("expected error without POSIX bash on Windows, got shell=%q args=%v", shell, args)
		}
		if !strings.Contains(err.Error(), "cmd.exe is not supported") {
			t.Fatalf("expected clear cmd.exe refusal error, got %q", err.Error())
		}
		// When Git Bash exists, it is selected with -c.
		gitBash := `C:\Program Files\Git\bin\bash.exe`
		shell, args, err = resolveWindowsBashShell(`C:\Program Files`, "", func(p string) bool { return p == gitBash }, neverFound)
		if err != nil || shell != gitBash || len(args) != 1 || args[0] != "-c" {
			t.Fatalf("expected Git Bash -c, got shell=%q args=%v err=%v", shell, args, err)
		}
	})
}

// ── Magic Keywords Tests ─────────────────────────────────────────────────────

func TestMagicKeywordsUltrathinkAndChillaxMatchOmp(t *testing.T) {
	// 1. ultrathink injects notice, does NOT mutate pinned thinking level;
	//    +2 in prose does NOT inject any turn-budget-notice (omp sends no text for +Nk).
	m, h := newTestExtension(t, hostOptions{
		SessionID:        "sess-magic-pinned",
		ThinkingLevel:    "medium",
		PerMessageEffort: false, // model without per-message effort
		ActiveTools:      []string{"read", "task", "eval", "bash", "edit", "write", "todo"},
	})
	h.fireEvent(sdk.EventInput, map[string]any{
		"type":   "input",
		"text":   "please ultrathink and bump retry to +2",
		"source": "interactive",
	})
	h.mu.Lock()
	level := h.thinkingLevel
	h.mu.Unlock()
	if level != "medium" {
		t.Fatalf("ultrathink must not permanently mutate pinned thinking level, got %q", level)
	}
	for _, c := range h.callsTo("sendMessage") {
		msg, _ := c.Args["message"].(map[string]any)
		if msg["customType"] == "turn-budget-notice" {
			t.Fatalf("omp sends no turn-budget-notice for +N in prose")
		}
	}
	// Turn budget was recorded in state for TurnBudgetFor.
	m.mu.Lock()
	tb := m.stateLocked("sess-magic-pinned").turnBudget
	m.mu.Unlock()
	if tb == nil || tb.Total != 2 {
		t.Fatalf("expected recorded turn budget 2, got %+v", tb)
	}

	// 2. chillax does NOT change thinking level when model lacks per-message effort.
	// upstream: test/agent-session-magic-keywords.test.ts: "needs a stable effort"
	h.fireEvent(sdk.EventInput, map[string]any{
		"type":   "input",
		"text":   "chillax wrap up",
		"source": "interactive",
	})
	h.mu.Lock()
	level = h.thinkingLevel
	h.mu.Unlock()
	if level != "medium" {
		t.Fatalf("chillax must not lower effort on model without per-message effort, got %q", level)
	}

	// 3. chillax DOES lower pinned effort on a model with supportsPerMessageEffort.
	// upstream: test/agent-session-magic-keywords.test.ts: "takes per-message effort"
	_, h2 := newTestExtension(t, hostOptions{
		SessionID:        "sess-magic-chillax",
		ThinkingLevel:    "high",
		PerMessageEffort: true,
	})
	h2.fireEvent(sdk.EventInput, map[string]any{
		"type":   "input",
		"text":   "chillax just finish",
		"source": "interactive",
	})
	h2.mu.Lock()
	level2 := h2.thinkingLevel
	h2.mu.Unlock()
	if level2 != "minimal" {
		t.Fatalf("chillax on per-message-effort model: got %q, want minimal", level2)
	}
}

// ── BTW Sidecar & Full Context Tests ─────────────────────────────────────────

func TestBtwUsesSidecarAndIncludesToolResults(t *testing.T) {
	sessDir := t.TempDir()
	sessionFile := filepath.Join(sessDir, "btw-sess.jsonl")
	var capturedMessages []any
	_, h := newTestExtension(t, hostOptions{
		SessionID:   "sess-btw-sidecar",
		SessionFile: sessionFile,
		Branch: []map[string]any{
			{"id": "u1", "type": "message", "message": map[string]any{"role": "user", "content": "check foo"}},
			{"id": "a1", "type": "message", "message": map[string]any{
				"role":    "assistant",
				"content": []any{map[string]any{"type": "toolCall", "id": "c1", "name": "read", "arguments": map[string]any{"path": "foo.go"}}},
			}},
			{"id": "t1", "type": "message", "message": map[string]any{
				"role":     "toolResult",
				"toolName": "read",
				"content":  []any{map[string]any{"type": "text", "text": "package foo"}},
			}},
		},
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "complete" {
				if req, ok := args["request"].(map[string]any); ok {
					capturedMessages, _ = req["messages"].([]any)
				}
				return map[string]any{
					"role":       "assistant",
					"stopReason": "stop",
					"content":    []any{map[string]any{"type": "text", "text": "foo defines package foo."}},
				}, ""
			}
			return nil, ""
		},
	})

	if _, errMsg := h.callCommand("btw", "what package is foo.go?"); errMsg != "" {
		t.Fatalf("/btw failed: %s", errMsg)
	}
	// Must NOT write anything to the main session journal (`appendEntry`).
	if n := len(h.callsTo("appendEntry")); n != 0 {
		t.Fatalf("/btw must never append to main session journal, got %d appendEntry calls", n)
	}
	// Must include tool_call and tool_result context in the completion request.
	rawReq, _ := json.Marshal(capturedMessages)
	if !strings.Contains(string(rawReq), "package foo") || !strings.Contains(string(rawReq), "foo.go") {
		t.Fatalf("/btw request missing tool call/result context: %s", rawReq)
	}
	// Sidecar file must exist on disk and be readable by bare `/btw` without UI.
	sidecarDir := filepath.Join(sessDir, "btw-sess", "local", "btw")
	entries, err := os.ReadDir(sidecarDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected 1 sidecar file in %s, got %v (err=%v)", sidecarDir, entries, err)
	}
}

// ── /force + Plan Mode Interaction Test ──────────────────────────────────────

func TestForceSurvivesPlanMode(t *testing.T) {
	cwd := t.TempDir()
	_, h := newTestExtension(t, hostOptions{Cwd: cwd, SessionID: "sess-force-plan"})
	_, _ = h.callCommand("plan", "")
	_, _ = h.callCommand("force", "grep")

	bas := h.fireEvent(sdk.EventBeforeAgentStart, map[string]any{
		"type":                "before_agent_start",
		"systemPromptOptions": map[string]any{"cwd": cwd},
	})
	if !strings.Contains(string(bas), "plan-mode-context") {
		t.Fatalf("expected plan-mode-context in before_agent_start: %s", bas)
	}
	foundForce := false
	for _, c := range h.callsTo("sendMessage") {
		if msg, _ := c.Args["message"].(map[string]any); msg["customType"] == "forced-tool-notice" {
			foundForce = true
		}
	}
	if !foundForce {
		t.Fatalf("/force must not be dropped while plan mode is active")
	}
}

// ── Multi-Session & Lifecycle Isolation Tests ────────────────────────────────

func TestTwoSessionsAndRegisterTwiceLifecycleIsolation(t *testing.T) {
	// Integrator requirement:
	// 1. Call Register twice (simulating PiG creating a fresh extension per session).
	// 2. Two sessions active in one process; lifecycle.End(first) releases ONLY
	//    the first session's state, leaving the second session intact.
	// 3. Calling lifecycle.End twice and with empty/unknown IDs is safe.
	storeMu.Lock()
	prev := defaultStore
	defaultStore = newManager()
	storeMu.Unlock()
	t.Cleanup(func() {
		storeMu.Lock()
		defaultStore = prev
		storeMu.Unlock()
		gate.ResetForTests()
		lifecycle.ResetForTests()
		toolcmd.ResetForTests()
	})

	ext1 := sdk.New("instance-1")
	Register(ext1)
	h1 := startHost(t, ext1, hostOptions{SessionID: "sess-A"})

	ext2 := sdk.New("instance-2")
	Register(ext2)
	h2 := startHost(t, ext2, hostOptions{SessionID: "sess-B"})

	// Enable plan mode in sess-A and goal mode in sess-B.
	if _, errMsg := h1.callCommand("plan", ""); errMsg != "" {
		t.Fatalf("sess-A /plan failed: %s", errMsg)
	}
	if _, errMsg := h2.callCommand("goal", "Keep running"); errMsg != "" {
		t.Fatalf("sess-B /goal failed: %s", errMsg)
	}

	// Shut down sess-A via lifecycle.End (twice + unknown + empty).
	lifecycle.End("sess-A")
	lifecycle.End("sess-A")
	lifecycle.End("sess-unknown")
	lifecycle.End("")

	// sess-A state must be gone; sess-B state must still be active and working.
	defaultStore.mu.Lock()
	_, hasA := defaultStore.sessions["sess-A"]
	stB := defaultStore.sessions["sess-B"]
	bGoalOK := stB != nil && stB.goalState != nil && stB.goalState.Enabled && stB.goalState.Goal.Objective == "Keep running"
	defaultStore.mu.Unlock()

	if hasA {
		t.Fatalf("sess-A state was not released by lifecycle.End")
	}
	if !bGoalOK {
		t.Fatalf("sess-B goal state was corrupted when sess-A ended")
	}

	// sess-B commands keep functioning after sess-A shutdown.
	if _, errMsg := h2.callCommand("goal", "show"); errMsg != "" {
		t.Fatalf("sess-B /goal show failed after sess-A ended: %s", errMsg)
	}
	if !strings.Contains(h2.lastNotify(), "Keep running") {
		t.Fatalf("expected sess-B objective after sess-A ended, got %q", h2.lastNotify())
	}
}

// ── Remaining Slash Commands Smoke & Contract Tests ──────────────────────────

func TestRemainingSlashCommands(t *testing.T) {
	cwd := t.TempDir()
	branch := []map[string]any{
		{
			"id": "u1", "type": "message",
			"message": map[string]any{"role": "user", "content": "show me code"},
		},
		{
			"id": "a1", "type": "message",
			"message": map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{"type": "text", "text": "Here:\n```go\nfmt.Println(1)\n```\nSee https://example.com/docs."},
					map[string]any{"type": "toolCall", "id": "c1", "name": "bash", "arguments": map[string]any{"command": "go test ./..."}},
				},
				"usage": map[string]any{"input": 1000, "output": 250, "cacheRead": 500, "cacheWrite": 100, "totalTokens": 1850},
			},
		},
	}

	_, h := newTestExtension(t, hostOptions{
		Cwd:           cwd,
		SessionID:     "sess-cmds-1",
		SystemPrompt:  "You are an assistant.",
		ContextWindow: 100000,
		Branch:        branch,
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "ui.select" {
				if opts, ok := args["options"].([]any); ok && len(opts) > 0 {
					return map[string]any{"ok": true, "selected": opts[0]}, ""
				}
			}
			return nil, ""
		},
	})

	// /context, /usage, /handoff, /rewind, /tools, /effort, /switch, /rename, /dump, /copy <arg>, /queue, /jobs, /clear, /exit
	if _, errMsg := h.callCommand("context", ""); errMsg != "" || !strings.Contains(h.lastNotify(), "Context window: 100000 tokens") {
		t.Fatalf("/context failed: err=%q notify=%q", errMsg, h.lastNotify())
	}
	if _, errMsg := h.callCommand("usage", ""); errMsg != "" || !strings.Contains(h.lastNotify(), "Input tokens: 1000") {
		t.Fatalf("/usage failed: err=%q notify=%q", errMsg, h.lastNotify())
	}
	if _, errMsg := h.callCommand("handoff", "focus on parser"); errMsg != "" || len(h.callsTo("compact")) != 1 {
		t.Fatalf("/handoff failed: err=%q", errMsg)
	}
	if _, errMsg := h.callCommand("rewind", ""); errMsg != "" || len(h.callsTo("navigateTree")) != 1 {
		t.Fatalf("/rewind failed: err=%q", errMsg)
	}
	if _, errMsg := h.callCommand("tools", ""); errMsg != "" || !strings.Contains(h.lastNotify(), "* read") {
		t.Fatalf("/tools failed: err=%q notify=%q", errMsg, h.lastNotify())
	}
	if _, errMsg := h.callCommand("effort", "low"); errMsg != "" || !strings.Contains(h.lastNotify(), "Thinking set to low.") {
		t.Fatalf("/effort failed: err=%q notify=%q", errMsg, h.lastNotify())
	}
	if _, errMsg := h.callCommand("switch", "openai/gpt-5:medium"); errMsg != "" || !strings.Contains(h.lastNotify(), "Model switched to openai/gpt-5.") {
		t.Fatalf("/switch failed: err=%q notify=%q", errMsg, h.lastNotify())
	}
	if _, errMsg := h.callCommand("rename", "Auth Fix"); errMsg != "" || !strings.Contains(h.lastNotify(), "Session renamed to Auth Fix.") {
		t.Fatalf("/rename failed: err=%q notify=%q", errMsg, h.lastNotify())
	}
	if _, errMsg := h.callCommand("dump", ""); errMsg != "" || !strings.Contains(h.clipboard, "show me code") {
		t.Fatalf("/dump failed: err=%q clip=%q", errMsg, h.clipboard)
	}

	h.fireEvent(sdk.EventInput, map[string]any{"type": "input", "text": "/copy code", "source": "interactive"})
	if h.clipboard != "fmt.Println(1)" {
		t.Fatalf("/copy code clipboard = %q", h.clipboard)
	}
	h.fireEvent(sdk.EventInput, map[string]any{"type": "input", "text": "/copy cmd", "source": "interactive"})
	if h.clipboard != "go test ./..." {
		t.Fatalf("/copy cmd clipboard = %q", h.clipboard)
	}
	h.fireEvent(sdk.EventInput, map[string]any{"type": "input", "text": "/copy link", "source": "interactive"})
	if h.clipboard != "https://example.com/docs" {
		t.Fatalf("/copy link clipboard = %q", h.clipboard)
	}
	h.fireEvent(sdk.EventInput, map[string]any{"type": "input", "text": "/copy local", "source": "interactive"})
	if h.clipboard == "" {
		t.Fatalf("/copy local produced empty path")
	}

	if _, errMsg := h.callCommand("queue", "check logs next"); errMsg != "" || !strings.Contains(h.lastNotify(), "Queued follow-up message.") {
		t.Fatalf("/queue failed: err=%q notify=%q", errMsg, h.lastNotify())
	}

	jobs.ResetForTests()
	t.Cleanup(jobs.ResetForTests)
	fj := &fakeJob{id: "bg_1", kind: jobs.KindBash, label: "sleep 60", status: "running", started: time.Now().Add(-2 * time.Second), done: make(chan struct{})}
	jobs.Add("sess-cmds-1", fj)
	if _, errMsg := h.callCommand("jobs", ""); errMsg != "" || !strings.Contains(h.lastNotify(), "[bg_1] bash (running)") {
		t.Fatalf("/jobs failed: err=%q notify=%q", errMsg, h.lastNotify())
	}
	if _, errMsg := h.callCommand("jobs", "kill bg_1"); errMsg != "" || !fj.cancelled {
		t.Fatalf("/jobs kill failed: err=%q cancelled=%v", errMsg, fj.cancelled)
	}

	if _, errMsg := h.callCommand("clear", ""); errMsg != "" || len(h.callsTo("newSession")) != 1 {
		t.Fatalf("/clear failed: err=%q", errMsg)
	}
	if _, errMsg := h.callCommand("exit", ""); errMsg != "" || len(h.callsTo("shutdown")) != 1 {
		t.Fatalf("/exit failed: err=%q", errMsg)
	}
}

// ── Mounted-Tool Awareness: /force, /tools, /context, and Plan Save/Restore ──

// TestForceToolsContextWithMountedCommands verifies findings 5 and 6 from
// OmpToolcmd pass 3:
//   - `/force <mounted>` refuses with omp's exact `Tool "<name>" is not currently active.`
//     (`session/agent-session.ts:3573-3576`) and does not arm a forced turn,
//     while `/force <top-level>` succeeds.
//   - `/tools` prints `*` only for top-level advertised tools, `-` for inactive
//     and mounted tools, then one `~ <name>` line per mounted command
//     (`slash-commands/builtin-session.ts:543-553`).
//   - `/context` sums only top-level advertised tool schemas, excluding mounted
//     tools (`packages/tui/src/status-line/context-usage.ts:468-469`).
func TestForceToolsContextWithMountedCommands(t *testing.T) {
	m := newManager()
	m.loopWait = 0
	storeMu.Lock()
	prev := defaultStore
	defaultStore = m
	storeMu.Unlock()
	t.Cleanup(func() {
		storeMu.Lock()
		defaultStore = prev
		storeMu.Unlock()
		gate.ResetForTests()
		lifecycle.ResetForTests()
		toolcmd.ResetForTests()
	})

	ext := sdk.New("mounted-commands-test")
	toolcmd.Register(ext)
	registerWithManager(ext, m)

	// Register `lsp` and `debug` as mountable codemode tools with large
	// descriptions so `/context` clearly distinguishes whether they are summed.
	hugeDesc := strings.Repeat("lsp-schema-padding ", 400)
	toolcmd.Mountable("lsp", func(sdk.Context) bool { return true })
	toolcmd.Mountable("debug", func(sdk.Context) bool { return true })
	ext.RegisterTool(sdk.ToolDefinition{
		Name:        "lsp",
		Description: hugeDesc,
		Parameters:  sdk.Schema{"type": "object", "properties": map[string]any{"action": map[string]any{"type": "string"}}},
		Exposure:    sdk.ToolExposureCodemode,
		Execute:     func(sdk.Context, map[string]any) (any, error) { return sdk.ToolResult{Content: "ok"}, nil },
	})
	ext.RegisterTool(sdk.ToolDefinition{
		Name:        "debug",
		Description: hugeDesc,
		Parameters:  sdk.Schema{"type": "object", "properties": map[string]any{"action": map[string]any{"type": "string"}}},
		Exposure:    sdk.ToolExposureCodemode,
		Execute:     func(sdk.Context, map[string]any) (any, error) { return sdk.ToolResult{Content: "ok"}, nil },
	})

	h := startHost(t, ext, hostOptions{
		Cwd:           t.TempDir(),
		SessionID:     "sess-mounted-cmds",
		ContextWindow: 100000,
		SystemPrompt:  "sys",
		ActiveTools:   []string{"read", "bash", "grep", "lsp", "debug"},
		AllTools: []map[string]any{
			{"name": "read", "description": "read files", "parameters": map[string]any{"type": "object"}, "exposure": string(sdk.ToolExposureDirect)},
			{"name": "bash", "description": "run shell", "parameters": map[string]any{"type": "object"}, "exposure": string(sdk.ToolExposureDirect)},
			{"name": "grep", "description": "search text", "parameters": map[string]any{"type": "object"}, "exposure": string(sdk.ToolExposureDirect)},
			{"name": "write", "description": "write files", "parameters": map[string]any{"type": "object"}, "exposure": string(sdk.ToolExposureDirect)},
			{"name": "lsp", "description": hugeDesc, "parameters": map[string]any{"type": "object"}, "exposure": string(sdk.ToolExposureCodemode)},
			{"name": "debug", "description": hugeDesc, "parameters": map[string]any{"type": "object"}, "exposure": string(sdk.ToolExposureCodemode)},
		},
	})

	// Fire session_start on all handlers so toolcmd mounts `lsp` and `debug`
	// while keeping them in PiG's active list for direct fallback dispatch.
	for _, hid := range h.handlers[sdk.EventSessionStart] {
		raw, _ := json.Marshal(map[string]any{"type": "session_start"})
		if _, errMsg := h.roundTrip(map[string]any{"method": "event", "event": sdk.EventSessionStart, "handler_id": hid, "args": json.RawMessage(raw)}); errMsg != "" {
			t.Fatalf("session_start handler %d failed: %s", hid, errMsg)
		}
	}

	// Sanity check: lsp and debug remain in PiG's activeTools for direct fallback.
	h.mu.Lock()
	lspInActive := containsString(h.activeTools, "lsp")
	debugInActive := containsString(h.activeTools, "debug")
	h.mu.Unlock()
	if !lspInActive || !debugInActive {
		t.Fatalf("expected lsp and debug to stay in activeTools for fallback dispatch, got %v", h.activeTools)
	}

	// 1. `/force lsp` must refuse with omp's exact message and not arm a forced turn.
	if _, errMsg := h.callCommand("force", "lsp"); errMsg != "" {
		t.Fatalf("/force lsp returned protocol error: %s", errMsg)
	}
	wantRefuse := `Tool "lsp" is not currently active.`
	if got := h.lastNotify(); got != wantRefuse {
		t.Fatalf("/force lsp notify = %q, want %q", got, wantRefuse)
	}
	bas := h.fireEvent(sdk.EventBeforeAgentStart, map[string]any{
		"type":                "before_agent_start",
		"systemPromptOptions": map[string]any{"cwd": h.opts.Cwd},
	})
	if strings.Contains(string(bas), "forced-tool-notice") {
		t.Fatalf("/force lsp must not inject forced-tool-notice: %s", bas)
	}

	// Inactive non-mounted tool also gets omp's exact message.
	if _, errMsg := h.callCommand("force", "write"); errMsg != "" {
		t.Fatalf("/force write returned protocol error: %s", errMsg)
	}
	if got := h.lastNotify(); got != `Tool "write" is not currently active.` {
		t.Fatalf("/force write notify = %q, want Tool \"write\" is not currently active.", got)
	}

	// Top-level active tool succeeds.
	if _, errMsg := h.callCommand("force", "grep"); errMsg != "" {
		t.Fatalf("/force grep failed: %s", errMsg)
	}
	if got := h.lastNotify(); got != "Next turn forced to use grep." {
		t.Fatalf("/force grep notify = %q", got)
	}

	// 2. `/tools` marks only top-level advertised tools with `*`, mounted/inactive with `-`,
	//    and appends `~ <name>` lines for each mounted command.
	if _, errMsg := h.callCommand("tools", ""); errMsg != "" {
		t.Fatalf("/tools failed: %s", errMsg)
	}
	toolsOut := h.lastNotify()
	for _, want := range []string{"* read", "* bash", "* grep", "- write", "- lsp", "- debug", "~ lsp", "~ debug"} {
		if !strings.Contains(toolsOut, want) {
			t.Fatalf("/tools missing %q in:\n%s", want, toolsOut)
		}
	}
	for _, bad := range []string{"* lsp", "* debug"} {
		if strings.Contains(toolsOut, bad) {
			t.Fatalf("/tools must not mark mounted tool as top-level %q in:\n%s", bad, toolsOut)
		}
	}

	// 3. `/context` sums only top-level advertised tool schemas, NOT mounted hugeDesc schemas.
	if _, errMsg := h.callCommand("context", ""); errMsg != "" {
		t.Fatalf("/context failed: %s", errMsg)
	}
	ctxOut := h.lastNotify()
	// With hugeDesc (~1900 tokens each, ~3800 for lsp+debug), if mounted tools were counted
	// the Tools line would exceed 3500 tokens; without them it is < 100 tokens.
	var toolsTok int
	for _, line := range strings.Split(ctxOut, "\n") {
		if strings.Contains(line, "System tools") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[len(fields)-1] == "tokens" {
				if _, err := fmt.Sscanf(fields[len(fields)-2], "%d", &toolsTok); err != nil {
					t.Fatalf("failed to parse System tools line %q: %v", line, err)
				}
			}
		}
	}
	if toolsTok <= 0 || toolsTok >= 500 {
		t.Fatalf("/context System tools tokens = %d (want >0 and <500; mounted huge schemas must be excluded)\n%s", toolsTok, ctxOut)
	}
}

// TestPlanModeSaveRestorePreservesMountedToolsAndOrder verifies that entering
// and exiting plan mode saves and restores the exact active tool list including
// mounted tools and their original order, and never passes an empty list to
// SetActiveTools.
func TestPlanModeSaveRestorePreservesMountedToolsAndOrder(t *testing.T) {
	m := newManager()
	m.loopWait = 0
	storeMu.Lock()
	prev := defaultStore
	defaultStore = m
	storeMu.Unlock()
	t.Cleanup(func() {
		storeMu.Lock()
		defaultStore = prev
		storeMu.Unlock()
		gate.ResetForTests()
		lifecycle.ResetForTests()
		toolcmd.ResetForTests()
	})

	ext := sdk.New("plan-restore-order-test")
	toolcmd.Register(ext)
	registerWithManager(ext, m)

	toolcmd.Mountable("lsp", func(sdk.Context) bool { return true })
	ext.RegisterTool(sdk.ToolDefinition{
		Name:        "lsp",
		Description: "lsp tool",
		Parameters:  sdk.Schema{"type": "object"},
		Exposure:    sdk.ToolExposureCodemode,
		Execute:     func(sdk.Context, map[string]any) (any, error) { return sdk.ToolResult{Content: "ok"}, nil },
	})

	// Deliberately custom order without `write`, with mounted `lsp` in the middle.
	initialOrder := []string{"grep", "lsp", "bash", "read"}
	h := startHost(t, ext, hostOptions{
		Cwd:         t.TempDir(),
		SessionID:   "sess-plan-order",
		ActiveTools: initialOrder,
		AllTools: []map[string]any{
			{"name": "grep", "description": "grep", "parameters": map[string]any{"type": "object"}, "exposure": string(sdk.ToolExposureDirect)},
			{"name": "lsp", "description": "lsp", "parameters": map[string]any{"type": "object"}, "exposure": string(sdk.ToolExposureCodemode)},
			{"name": "bash", "description": "bash", "parameters": map[string]any{"type": "object"}, "exposure": string(sdk.ToolExposureDirect)},
			{"name": "read", "description": "read", "parameters": map[string]any{"type": "object"}, "exposure": string(sdk.ToolExposureDirect)},
			{"name": "write", "description": "write", "parameters": map[string]any{"type": "object"}, "exposure": string(sdk.ToolExposureDirect)},
			{"name": "propose", "description": "propose", "parameters": map[string]any{"type": "object"}, "exposure": string(sdk.ToolExposureCodemode)},
		},
	})

	// Enter plan mode: `write` is appended; mounted `lsp` and original order stay intact.
	if _, errMsg := h.callCommand("plan", ""); errMsg != "" {
		t.Fatalf("/plan enter failed: %s", errMsg)
	}
	h.mu.Lock()
	duringPlan := append([]string(nil), h.activeTools...)
	h.mu.Unlock()
	wantDuring := []string{"grep", "lsp", "bash", "read", "write"}
	if strings.Join(duringPlan, ",") != strings.Join(wantDuring, ",") {
		t.Fatalf("during plan activeTools = %v, want %v", duringPlan, wantDuring)
	}

	// Exit plan mode: exact saved list (including `lsp` and order) must be restored.
	if _, errMsg := h.callCommand("plan", ""); errMsg != "" {
		t.Fatalf("/plan exit failed: %s", errMsg)
	}
	h.mu.Lock()
	afterExit := append([]string(nil), h.activeTools...)
	h.mu.Unlock()
	if strings.Join(afterExit, ",") != strings.Join(initialOrder, ",") {
		t.Fatalf("after plan exit activeTools = %v, want %v", afterExit, initialOrder)
	}

	// Never pass an empty tool list to SetActiveTools at any point.
	for _, c := range h.callsTo("setActiveTools") {
		raw, _ := c.Args["tools"].([]any)
		if len(raw) == 0 {
			t.Fatalf("setActiveTools called with empty list: %+v", c.Args)
		}
	}
}

type fakeJob struct {
	id        string
	kind      jobs.Kind
	label     string
	status    string
	started   time.Time
	cancelled bool
	done      chan struct{}
}

func (f *fakeJob) ID() string            { return f.id }
func (f *fakeJob) Kind() jobs.Kind       { return f.kind }
func (f *fakeJob) Label() string         { return f.label }
func (f *fakeJob) Started() time.Time    { return f.started }
func (f *fakeJob) Status() string        { return f.status }
func (f *fakeJob) Done() <-chan struct{} { return f.done }
func (f *fakeJob) Output() string        { return "" }
func (f *fakeJob) Result() string        { return "" }
func (f *fakeJob) Cancel() {
	f.cancelled = true
	f.status = "cancelled"
	select {
	case <-f.done:
	default:
		close(f.done)
	}
}
