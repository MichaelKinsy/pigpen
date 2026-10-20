package pi_subagents

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// The retained workflow children of a session (runs/background/retained-children.ts): terminal single-child runs a workflow
// started, with whether each can still be resumed and why not.

const (
	maxRetainedChildren          = 10
	maxRetainedChildCandidates   = 100
	maxRetainedTaskSummaryLength = 120
)

type retainedChildResumability struct {
	State       string // resumable, not-resumable
	SessionPath string // resumable
	Reason      string // not-resumable
}

type retainedChild struct {
	RunID        string
	ParentRunID  string
	WorkflowKey  string
	State        string
	Agent        string
	TaskSummary  string
	CompletedAt  int64
	Resumability retainedChildResumability
	SessionPath  string
	TokenTotals  *TokenUsage
}

func extraString(extra map[string]json.RawMessage, key string) string {
	var s string
	if raw := extra[key]; raw != nil && json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

func retainedSessionFile(sessionFile string) retainedChildResumability {
	if sessionFile == "" {
		return retainedChildResumability{State: "not-resumable", Reason: "no persisted session file"}
	}
	if !strings.HasSuffix(sessionFile, ".jsonl") {
		return retainedChildResumability{State: "not-resumable", Reason: "persisted session file is not a .jsonl file"}
	}
	st, err := os.Lstat(sessionFile)
	if err != nil {
		if isNotExist(err) {
			return retainedChildResumability{State: "not-resumable", Reason: "persisted session file is missing: " + sessionFile}
		}
		return retainedChildResumability{State: "not-resumable", Reason: "persisted session file could not be inspected: " + err.Error()}
	}
	if !st.Mode().IsRegular() {
		return retainedChildResumability{State: "not-resumable", Reason: "persisted session file is not a regular file"}
	}
	return retainedChildResumability{State: "resumable", SessionPath: sessionFile}
}

func childResumability(run AsyncRunSummary, step AsyncRunStepSummary) retainedChildResumability {
	no := func(reason string) retainedChildResumability {
		return retainedChildResumability{State: "not-resumable", Reason: reason}
	}
	if run.State == "stopped" || step.Status == "stopped" {
		return no("stopped run")
	}
	if rn := step.Extra["runner"]; rn != nil {
		var r struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(rn, &r) == nil && (r.Type == "external-cli" || r.Type == "external-job") {
			return no("external runner")
		}
	}
	session := retainedSessionFile(firstNonEmpty(step.SessionFile, run.SessionFile))
	if session.State == "not-resumable" {
		return session
	}
	desc, err := readAsyncRecoveryDescriptor(run.AsyncDir)
	if err != nil {
		return no("invalid recovery descriptor: " + err.Error())
	}
	if desc == nil {
		return no("missing recovery descriptor")
	}
	if desc.SourceRunID != run.ID {
		return no("recovery descriptor belongs to run " + desc.SourceRunID)
	}
	if desc.Agent != step.Agent {
		return no("recovery descriptor belongs to agent " + desc.Agent)
	}
	required, ok, err := ResolveRetainedWorktreeCwd(ParallelHandoffPath(run.AsyncDir, ""), run.ID, step.Index)
	if err != nil {
		return no("resume dependency unavailable: " + err.Error())
	}
	if !ok {
		required = firstNonEmpty(desc.Cwd, run.Cwd)
	}
	if st, serr := os.Stat(required); required == "" || serr != nil || !st.IsDir() {
		shown := required
		if shown == "" {
			shown = "unknown"
		}
		return no("required cwd is missing: " + shown)
	}
	return session
}

func isTerminalRetainedStep(status string) bool {
	switch status {
	case "complete", "completed", "failed", "paused", "stopped":
		return true
	}
	return false
}

func boundedTaskSummary(v string) string {
	n := jsTrim(strings.Join(strings.FieldsFunc(v, isJSSpace), " "))
	u := []rune(n)
	if len(u) > maxRetainedTaskSummaryLength {
		return string(u[:maxRetainedTaskSummaryLength-1]) + "…"
	}
	return n
}

func listRetainedChildren(asyncDirRoot, sessionID string) ([]retainedChild, error) {
	runs, err := listAsyncRuns(asyncDirRoot, AsyncRunListOptions{SessionID: sessionID, States: []string{"complete", "failed", "paused", "stopped"}, EntryLimit: maxRetainedChildCandidates, NoReconcile: true})
	if err != nil {
		return nil, err
	}
	out := []retainedChild{}
	for _, run := range runs {
		if run.ParentWorkflowRunID == "" || len(run.Steps) != 1 {
			continue
		}
		switch run.State {
		case "complete", "failed", "partial", "paused", "stopped":
		default:
			continue
		}
		step := run.Steps[0]
		if !isTerminalRetainedStep(step.Status) {
			continue
		}
		var completed int64
		switch {
		case run.EndedAt != nil:
			completed = int64(*run.EndedAt)
		case run.LastUpdate != nil:
			completed = int64(*run.LastUpdate)
		default:
			continue
		}
		res := childResumability(run, step)
		c := retainedChild{RunID: run.ID, ParentRunID: run.ParentWorkflowRunID, WorkflowKey: run.WorkflowKey, State: run.State, Agent: step.Agent,
			TaskSummary: boundedTaskSummary(step.Description), CompletedAt: completed, Resumability: res}
		if res.State == "resumable" {
			c.SessionPath = res.SessionPath
		}
		if step.Tokens != nil {
			c.TokenTotals = step.Tokens
		} else {
			c.TokenTotals = run.TotalTokens
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CompletedAt > out[j].CompletedAt })
	return out, nil
}

const retainedNoKnownEligibleTargetGuidance = `children.list is workflow-only and is not an exhaustive list of direct native children. If the exact run id of the intended child is known, inspect it with subagent({ action: "status", id: "..." }); if status identifies the candidate, attempt subagent({ action: "resume", id: "...", message: "..." }), which authoritatively checks eligibility and may reject it. Launch a same-role fallback challenge, labeled as fallback, only when there is no known candidate or resume rejects eligibility.`

func formatRetainedChildren(children []retainedChild) string {
	if len(children) == 0 {
		return "No retained workflow children in the active parent session. " + retainedNoKnownEligibleTargetGuidance
	}
	retained := append([]retainedChild{}, children[:min(len(children), maxRetainedChildren)]...)
	anyResumable := false
	for _, c := range retained {
		anyResumable = anyResumable || c.Resumability.State == "resumable"
	}
	if !anyResumable && len(children) > maxRetainedChildren {
		for _, c := range children[maxRetainedChildren:] {
			if c.Resumability.State == "resumable" {
				if len(retained) == maxRetainedChildren {
					retained[maxRetainedChildren-1] = c
				}
				break
			}
		}
	}
	has := false
	lines := []string{fmt.Sprintf("Retained workflow children (up to %d; newest first, with a resumable child retained when available):", maxRetainedChildren)}
	for _, c := range retained {
		lines = append(lines, fmt.Sprintf("- %s | %s | %s | %s", c.RunID, c.Agent, c.State, time.UnixMilli(c.CompletedAt).UTC().Format("2006-01-02T15:04:05.000Z")))
		if c.ParentRunID != "" {
			wf := "  workflow: " + c.ParentRunID
			if c.WorkflowKey != "" {
				wf += " (" + c.WorkflowKey + ")"
			}
			lines = append(lines, wf)
		}
		summary := c.TaskSummary
		if summary == "" {
			summary = "(no task summary)"
		}
		lines = append(lines, "  task: "+summary)
		if c.Resumability.State == "resumable" {
			has = true
			lines = append(lines, "  resumability: resumable", "  session: "+c.Resumability.SessionPath, fmt.Sprintf(`  resume: subagent({ action: "resume", id: "%s", message: "..." })`, c.RunID))
		} else {
			lines = append(lines, "  resumability: not resumable ("+c.Resumability.Reason+")")
		}
		if t := c.TokenTotals; t != nil {
			lines = append(lines, fmt.Sprintf("  tokens: input %d, output %d, total %d", t.Input, t.Output, t.Total))
		}
	}
	if !has {
		lines = append(lines, "No resumable retained workflow child is listed. "+retainedNoKnownEligibleTargetGuidance)
	}
	return strings.Join(lines, "\n")
}
