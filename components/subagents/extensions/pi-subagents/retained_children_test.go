package pi_subagents

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Twins of retained-children (runs/background/retained-children.ts).

type retainedRunOpts struct {
	sessionID           string
	noParent            bool
	state, stepStatus   string
	sessionFile         string // present, missing, omitted
	runner              string
	recoveryDescriptor  string // present, missing, invalid
	recoverySourceRunID string
	recoveryAgent       string
	recoveryCwd         string
}

func writeRetainedRun(t *testing.T, root string, index int, o retainedRunOpts) {
	t.Helper()
	runID := "child-" + strconv.Itoa(index)
	sessionFile := filepath.Join(root, "sessions", runID+".jsonl")
	if o.sessionFile != "missing" && o.sessionFile != "omitted" {
		writeFile(t, sessionFile, "")
	}
	asyncDir := filepath.Join(root, "runs", runID)
	state := o.state
	if state == "" {
		state = "complete"
	}
	stepStatus := o.stepStatus
	if stepStatus == "" {
		stepStatus = state
	}
	sid := o.sessionID
	if sid == "" {
		sid = "parent-a"
	}
	step := map[string]any{"agent": "worker", "status": stepStatus, "description": "  Task " + strconv.Itoa(index) + "  with   spacing " + strings.Repeat("x", 140) + "  ",
		"endedAt": 1000 + index, "tokens": map[string]any{"input": index, "output": index + 1, "total": index*2 + 1}}
	switch o.runner {
	case "external-cli":
		step["runner"] = map[string]any{"type": "external-cli", "command": "codex", "args": []any{}, "promptDelivery": "stdin"}
	case "external-job":
		step["runner"] = map[string]any{"type": "external-job", "provider": "surf-oracle", "options": map[string]any{}}
	}
	if o.sessionFile != "omitted" {
		step["sessionFile"] = sessionFile
	}
	status := map[string]any{"runId": runID, "sessionId": sid, "mode": "single", "state": state, "startedAt": index, "endedAt": 1000 + index, "lastUpdate": 1000 + index,
		"cwd": root, "steps": []any{step}}
	if !o.noParent {
		status["parentWorkflowRunId"] = "workflow-a"
		status["workflowKey"] = "lane-" + strconv.Itoa(index)
	}
	writeStatusJSON(t, asyncDir, status)
	if err := updateActiveRunIndex(asyncDir, state, "", indexOptions{}); err != nil {
		t.Fatal(err)
	}
	cwd := o.recoveryCwd
	if cwd == "" {
		cwd = root
	}
	switch o.recoveryDescriptor {
	case "invalid":
		writeFile(t, filepath.Join(asyncDir, "recovery-descriptor.json"), jsonOf(map[string]any{"version": 2, "sourceRunId": runID, "agent": "worker", "cwd": cwd, "systemPromptMode": "replace", "outputMode": "inline"}))
	case "missing":
	default:
		budget, err := createRunFanoutBudget(runID, 64)
		if err != nil {
			t.Fatal(err)
		}
		src, agent := o.recoverySourceRunID, o.recoveryAgent
		if src == "" {
			src = runID
		}
		if agent == "" {
			agent = "worker"
		}
		writeFile(t, filepath.Join(asyncDir, "recovery-descriptor.json"), jsonOf(map[string]any{"version": 1, "runFanoutBudget": map[string]any{"version": 1, "rootRunId": budget.RootRunID, "directory": budget.Directory, "limit": budget.Limit}, "sourceRunId": src, "agent": agent, "cwd": cwd,
			"systemPromptMode": "replace", "inheritGlobalContext": false, "inheritProjectContext": false, "inheritSkills": false, "outputMode": "inline", "maxSubagentDepth": 2, "share": false}))
	}
}

func TestRetainedChildrenTwins(t *testing.T) {
	const f = "retained-children"
	setup := func(t *testing.T) string {
		nestedTemp(t)
		withTempHome(t)
		return tmp(t)
	}
	ids := func(cs []retainedChild) []string {
		out := []string{}
		for _, c := range cs {
			out = append(out, c.RunID)
		}
		return out
	}
	tw(t, f, "returns terminal workflow children newest first and formats a 10-row roster", func(t *testing.T) {
		root := setup(t)
		for i := range 12 {
			writeRetainedRun(t, root, i, retainedRunOpts{})
		}
		writeRetainedRun(t, root, 50, retainedRunOpts{sessionID: "parent-b"})
		writeRetainedRun(t, root, 51, retainedRunOpts{noParent: true})
		writeRetainedRun(t, root, 52, retainedRunOpts{state: "failed"})
		children, err := listRetainedChildren(filepath.Join(root, "runs"), "parent-a")
		if err != nil {
			t.Fatal(err)
		}
		formatted := formatRetainedChildren(children)
		eq(t, len(children), 13, "children")
		want := []string{"child-52"}
		for off := range 12 {
			want = append(want, "child-"+strconv.Itoa(11-off))
		}
		eq(t, ids(children), want, "newest first")
		eq(t, len(regexp.MustCompile(`(?m)^- child-`).FindAllString(formatted, -1)), 10, "ten rows")
		if regexp.MustCompile(`(?m)^- child-2 `).MatchString(formatted) {
			t.Error("child-2 must be cut")
		}
		c := children[0]
		eq(t, c.Agent, "worker", "agent")
		eq(t, c.CompletedAt, int64(1052), "completed")
		eq(t, c.State, "failed", "state")
		eq(t, c.ParentRunID, "workflow-a", "workflow")
		eq(t, c.WorkflowKey, "lane-52", "key")
		if len([]rune(c.TaskSummary)) > 120 || !strings.HasPrefix(c.TaskSummary, "Task 52 with spacing") {
			t.Errorf("summary %q", c.TaskSummary)
		}
		eq(t, jsonOf(c.TokenTotals), `{"input":52,"output":53,"total":105}`, "tokens")
		eq(t, c.Resumability.State, "resumable", "resumable")
		hasRe(t, formatted, `resumability: resumable\n  session: .*child-52\.jsonl\n  resume: subagent\(\{ action: "resume", id: "child-52", message: "\.\.\." \}\)`)
		for _, id := range []string{"child-50", "child-51"} {
			if strings.Contains(strings.Join(ids(children), ","), id) {
				t.Errorf("%s must not be listed", id)
			}
		}
		if strings.Contains(formatted, "Launch a same-role fallback") {
			t.Error("no fallback guidance when something is resumable")
		}
	})
	tw(t, f, "explains exact-id inspection when the workflow-only roster is empty", func(t *testing.T) {
		root := setup(t)
		writeRetainedRun(t, root, 1, retainedRunOpts{noParent: true})
		children, _ := listRetainedChildren(filepath.Join(root, "runs"), "parent-a")
		formatted := formatRetainedChildren(children)
		eq(t, len(children), 0, "empty")
		hasRe(t, formatted, `children\.list is workflow-only and is not an exhaustive list of direct native children`)
		hasRe(t, formatted, `(?s)action: "status".*status identifies the candidate.*action: "resume".*authoritatively checks eligibility and may reject it`)
		hasRe(t, formatted, `(?s)fallback.*only when there is no known candidate or resume rejects eligibility`)
	})
	tw(t, f, "keeps an older resumable child visible behind ten newer non-resumable children", func(t *testing.T) {
		root := setup(t)
		writeRetainedRun(t, root, 0, retainedRunOpts{})
		for i := 1; i <= 10; i++ {
			writeRetainedRun(t, root, i, retainedRunOpts{recoveryDescriptor: "missing"})
		}
		children, _ := listRetainedChildren(filepath.Join(root, "runs"), "parent-a")
		formatted := formatRetainedChildren(children)
		eq(t, len(children), 11, "children")
		want := []string{}
		for off := range 11 {
			want = append(want, "child-"+strconv.Itoa(10-off))
		}
		eq(t, ids(children), want, "order")
		eq(t, len(regexp.MustCompile(`(?m)^- child-`).FindAllString(formatted, -1)), 10, "ten rows")
		hasRe(t, formatted, `resume: subagent\(\{ action: "resume", id: "child-0"`)
		if strings.Contains(formatted, "No resumable retained child is listed") {
			t.Error("a resumable child is listed")
		}
	})
	tw(t, f, "keeps non-resumable retained children visible with exact reasons", func(t *testing.T) {
		root := setup(t)
		writeRetainedRun(t, root, 1, retainedRunOpts{sessionFile: "omitted"})
		writeRetainedRun(t, root, 2, retainedRunOpts{sessionFile: "missing"})
		writeRetainedRun(t, root, 3, retainedRunOpts{state: "stopped", stepStatus: "stopped"})
		writeRetainedRun(t, root, 4, retainedRunOpts{runner: "external-cli"})
		writeRetainedRun(t, root, 5, retainedRunOpts{recoveryDescriptor: "missing"})
		writeRetainedRun(t, root, 6, retainedRunOpts{recoveryDescriptor: "invalid"})
		writeRetainedRun(t, root, 7, retainedRunOpts{recoverySourceRunID: "other-run"})
		writeRetainedRun(t, root, 8, retainedRunOpts{recoveryAgent: "reviewer"})
		writeRetainedRun(t, root, 9, retainedRunOpts{runner: "external-job"})
		children, _ := listRetainedChildren(filepath.Join(root, "runs"), "parent-a")
		formatted := formatRetainedChildren(children)
		got := [][2]string{}
		for _, c := range children {
			got = append(got, [2]string{c.RunID, c.Resumability.State + ": " + c.Resumability.Reason})
		}
		eq(t, got, [][2]string{
			{"child-9", "not-resumable: external runner"},
			{"child-8", "not-resumable: recovery descriptor belongs to agent reviewer"},
			{"child-7", "not-resumable: recovery descriptor belongs to run other-run"},
			{"child-6", "not-resumable: invalid recovery descriptor: Invalid async recovery descriptor '" + filepath.Join(root, "runs", "child-6", "recovery-descriptor.json") + "': version must be 1."},
			{"child-5", "not-resumable: missing recovery descriptor"},
			{"child-4", "not-resumable: external runner"},
			{"child-3", "not-resumable: stopped run"},
			{"child-2", "not-resumable: persisted session file is missing: " + filepath.Join(root, "sessions", "child-2.jsonl")},
			{"child-1", "not-resumable: no persisted session file"},
		}, "reasons")
		for _, re := range []string{`resumability: not resumable \(recovery descriptor belongs to agent reviewer\)`, `resumability: not resumable \(recovery descriptor belongs to run other-run\)`,
			`resumability: not resumable \(invalid recovery descriptor:`, `resumability: not resumable \(missing recovery descriptor\)`, `resumability: not resumable \(external runner\)`,
			`resumability: not resumable \(stopped run\)`, `resumability: not resumable \(no persisted session file\)`, `No resumable retained workflow child is listed`,
			`(?s)action: "status".*status identifies the candidate.*action: "resume".*authoritatively checks eligibility and may reject it`,
			`(?s)fallback.*only when there is no known candidate or resume rejects eligibility`} {
			hasRe(t, formatted, re)
		}
		if strings.Contains(formatted, "resume: subagent") {
			t.Error("no resume line for a non-resumable child")
		}
	})
	tw(t, f, "does not advertise a retained child when its required cwd is missing", func(t *testing.T) {
		root := setup(t)
		writeRetainedRun(t, root, 1, retainedRunOpts{recoveryCwd: filepath.Join(root, "removed-worktree")})
		children, _ := listRetainedChildren(filepath.Join(root, "runs"), "parent-a")
		formatted := formatRetainedChildren(children)
		eq(t, children[0].Resumability.State, "not-resumable", "state")
		hasRe(t, children[0].Resumability.Reason, `required cwd is missing|resume dependency unavailable`)
		if strings.Contains(formatted, "resume: subagent") {
			t.Error("must not advertise a resume")
		}
	})
}
