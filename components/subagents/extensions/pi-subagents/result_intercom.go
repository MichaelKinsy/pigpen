package pi_subagents

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Port of src/intercom/result-intercom.ts: the grouped result a run sends over intercom, its receipt, and the delivery event
// (resolveSubagentResultStatus is async_notify.go's).

const (
	subagentResultIntercomEvent         = "subagent:result-intercom"
	subagentResultIntercomDeliveryEvent = "subagent:result-intercom-delivery"
)

// resultIntercomChild is SubagentResultIntercomChild.
type resultIntercomChild struct {
	Agent          string              `json:"agent"`
	SessionName    string              `json:"sessionName,omitempty"`
	Status         string              `json:"status"`
	OutputState    string              `json:"outputState,omitempty"`
	Summary        string              `json:"summary"`
	Index          *int                `json:"index,omitempty"`
	ArtifactPath   string              `json:"artifactPath,omitempty"`
	SessionPath    string              `json:"sessionPath,omitempty"`
	IntercomTarget string              `json:"intercomTarget,omitempty"`
	Children       []*nestedRunSummary `json:"children,omitempty"`
}

// resultIntercomPayload is SubagentResultIntercomPayload.
type resultIntercomPayload struct {
	To                  string                `json:"to"`
	Message             string                `json:"message"`
	RequestID           string                `json:"requestId,omitempty"`
	RunID               string                `json:"runId"`
	Mode                string                `json:"mode"`
	Status              string                `json:"status"`
	Summary             string                `json:"summary"`
	Source              string                `json:"source"`
	Children            []resultIntercomChild `json:"children"`
	AsyncID             string                `json:"asyncId,omitempty"`
	AsyncDir            string                `json:"asyncDir,omitempty"`
	ChainSteps          *int                  `json:"chainSteps,omitempty"`
	Agent               string                `json:"agent,omitempty"`
	Index               *int                  `json:"index,omitempty"`
	ArtifactPath        string                `json:"artifactPath,omitempty"`
	SessionPath         string                `json:"sessionPath,omitempty"`
	ParallelHandoffPath string                `json:"-"`
}

var resultStatusOrder = []string{"running", "completed", "failed", "stopped", "paused", "detached"}

func countResultStatuses(children []resultIntercomChild) map[string]int {
	c := map[string]int{}
	for _, ch := range children {
		c[ch.Status]++
	}
	return c
}

func formatStatusCounts(c map[string]int) string {
	var parts []string
	for _, k := range resultStatusOrder {
		if c[k] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", c[k], k))
		}
	}
	if len(parts) == 0 {
		return "0 results"
	}
	return strings.Join(parts, ", ")
}

func formatOutputCounts(children []resultIntercomChild) string {
	c := map[string]int{}
	for _, ch := range children {
		state := ch.OutputState
		if state == "" {
			state = "unknown"
		}
		c[state]++
	}
	var parts []string
	for _, k := range []string{"present", "absent", "unknown"} {
		if c[k] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", c[k], k))
		}
	}
	if len(parts) == 0 {
		return "0 outputs"
	}
	return strings.Join(parts, ", ")
}

func resolveGroupedStatus(children []resultIntercomChild) string {
	c := countResultStatuses(children)
	switch {
	case c["failed"] > 0:
		return "failed"
	case c["stopped"] > 0:
		return "stopped"
	case c["paused"] > 0:
		return "paused"
	case c["running"] > 0:
		return "running"
	case c["completed"] > 0:
		return "completed"
	case c["detached"] > 0:
		return "detached"
	}
	return "failed"
}

// compactNestedRun is the public, bounded view of a nested run: no route secrets (control inbox, capability token), at most 4 path
// entries, 12 steps and 8 children per level, two levels deep.
func compactNestedRun(run *nestedRunSummary, depth int) *nestedRunSummary {
	out := &nestedRunSummary{ID: run.ID, ParentRunID: run.ParentRunID, ParentStepIndex: run.ParentStepIndex, ParentAgent: run.ParentAgent, Depth: run.Depth,
		AsyncDir: run.AsyncDir, SessionID: run.SessionID, SessionName: run.SessionName, SessionFile: run.SessionFile, IntercomTarget: run.IntercomTarget,
		OwnerIntercomTarget: run.OwnerIntercomTarget, LeafIntercomTarget: run.LeafIntercomTarget, OwnerState: run.OwnerState, Mode: run.Mode, State: run.State,
		Agent: run.Agent, Model: run.Model, Thinking: run.Thinking, CurrentStep: run.CurrentStep, ChainStepCount: run.ChainStepCount, ActivityState: run.ActivityState,
		LastActivityAt: run.LastActivityAt, CurrentTool: run.CurrentTool, CurrentToolStartedAt: run.CurrentToolStartedAt, CurrentPath: run.CurrentPath,
		TurnCount: run.TurnCount, ToolCount: run.ToolCount, TotalTokens: run.TotalTokens, StartedAt: run.StartedAt, EndedAt: run.EndedAt, LastUpdate: run.LastUpdate, Error: run.Error}
	out.Path = []nestedPathEntry{}
	for i, p := range run.Path {
		if i >= 4 {
			break
		}
		out.Path = append(out.Path, p)
	}
	if len(run.Agents) > 0 {
		out.Agents = run.Agents[:min(12, len(run.Agents))]
	}
	if len(run.ParallelGroups) > 0 && string(run.ParallelGroups) != "[]" {
		out.ParallelGroups = run.ParallelGroups
	}
	for i, step := range run.Steps {
		if i >= 12 {
			break
		}
		cs := nestedStepSummary{Agent: step.Agent, SessionName: step.SessionName, Status: step.Status, Model: step.Model, Thinking: step.Thinking, SessionFile: step.SessionFile,
			ActivityState: step.ActivityState, LastActivityAt: step.LastActivityAt, CurrentTool: step.CurrentTool, CurrentToolStartedAt: step.CurrentToolStartedAt,
			CurrentPath: step.CurrentPath, TurnCount: step.TurnCount, ToolCount: step.ToolCount, StartedAt: step.StartedAt, EndedAt: step.EndedAt, Error: step.Error}
		if depth < 2 && len(step.Children) > 0 {
			for j, c := range step.Children {
				if j >= 8 {
					break
				}
				cs.Children = append(cs.Children, compactNestedRun(c, depth+1))
			}
		}
		out.Steps = append(out.Steps, cs)
	}
	if depth < 2 && len(run.Children) > 0 {
		for j, c := range run.Children {
			if j >= 8 {
				break
			}
			out.Children = append(out.Children, compactNestedRun(c, depth+1))
		}
	}
	return out
}

// compactNestedResultChildren is the bounded list of at most 16 nested runs (nil for none).
func compactNestedResultChildren(children []*nestedRunSummary) []*nestedRunSummary {
	if len(children) == 0 {
		return nil
	}
	var out []*nestedRunSummary
	for i, c := range children {
		if i >= 16 {
			break
		}
		out = append(out, compactNestedRun(c, 0))
	}
	return out
}

// attachNestedChildrenToResultChildren puts each nested run under the result child that launched it (a single child also takes the
// ones with no step index).
func attachNestedChildrenToResultChildren(runID string, children []resultIntercomChild, nested []*nestedRunSummary) []resultIntercomChild {
	compact := compactNestedResultChildren(nested)
	out := make([]resultIntercomChild, len(children))
	if len(compact) == 0 {
		for i, ch := range children {
			ch.Children = compactNestedResultChildren(ch.Children)
			out[i] = ch
		}
		return out
	}
	for i, ch := range children {
		idx := i
		if ch.Index != nil {
			idx = *ch.Index
		}
		already := map[string]bool{}
		for _, n := range ch.Children {
			already[n.ID] = true
		}
		var attached []*nestedRunSummary
		for _, n := range compact {
			if n.ParentRunID == runID && n.ParentStepIndex != nil && int(*n.ParentStepIndex) == idx && !already[n.ID] {
				attached = append(attached, n)
			}
		}
		if len(children) == 1 {
			for _, n := range compact {
				if n.ParentRunID == runID && n.ParentStepIndex == nil && !already[n.ID] {
					attached = append(attached, n)
				}
			}
		}
		merged := compactNestedResultChildren(append(append([]*nestedRunSummary{}, ch.Children...), attached...))
		ch.Children = merged
		out[i] = ch
	}
	return out
}

func formatNestedResultLines(children []*nestedRunSummary) []string {
	if len(children) == 0 {
		return nil
	}
	lines := []string{"Nested subagents:"}
	remaining := 10
	var appendRuns func(runs []*nestedRunSummary, indent string)
	appendRuns = func(runs []*nestedRunSummary, indent string) {
		for _, run := range runs {
			if remaining <= 0 {
				lines = append(lines, indent+"↳ +more nested runs; inspect status for full tree")
				return
			}
			remaining--
			label := run.Agent
			if label == "" {
				label = strings.Join(run.Agents, "+")
			}
			if label == "" {
				label = run.ID
			}
			lines = append(lines, fmt.Sprintf("%s↳ %s — %s [%s]", indent, label, run.State, run.ID))
			if run.SessionFile != "" {
				lines = append(lines, indent+"  Session: "+run.SessionFile)
			}
			appendRuns(run.Children, indent+"  ")
			for _, step := range run.Steps {
				appendRuns(step.Children, indent+"    ")
			}
		}
	}
	appendRuns(children, "")
	return lines
}

func asyncResumeGuidance(source string, children []resultIntercomChild, asyncID string) string {
	if source != "async" || asyncID == "" {
		return ""
	}
	var resumable []int
	for i, c := range children {
		if c.SessionPath != "" {
			if _, err := os.Stat(c.SessionPath); err == nil {
				resumable = append(resumable, i)
			}
		}
	}
	if len(children) == 1 && len(resumable) == 1 {
		return fmt.Sprintf(`Revive: subagent({ action: "resume", id: "%s", message: "..." })`, asyncID)
	}
	if len(resumable) > 0 {
		first := resumable[0]
		idx := first
		if children[first].Index != nil {
			idx = *children[first].Index
		}
		return fmt.Sprintf(`Revive child: subagent({ action: "resume", id: "%s", index: %d, message: "..." })`, asyncID, idx)
	}
	return "Resume: unavailable; no child session file was persisted."
}

// groupedResultInput is the input of buildSubagentResultIntercomPayload.
type groupedResultInput struct {
	To, RunID, Mode, Source string
	Children                []resultIntercomChild
	AsyncID, AsyncDir       string
	ChainSteps              *int
	ParallelHandoffPath     string
}

func formatSubagentResultIntercomMessage(p *resultIntercomPayload) string {
	counts := countResultStatuses(p.Children)
	lines := []string{"subagent results", "", "Run: " + p.RunID, "Mode: " + p.Mode, "Process status: " + p.Status, "Children: " + formatStatusCounts(counts),
		"Outputs: " + formatOutputCounts(p.Children) + " (semantic adequacy unassessed)"}
	for _, c := range p.Children {
		if c.Status == "failed" && c.OutputState == "present" {
			lines = append(lines, "Recovery: At least one failed process produced output. Inspect that output before retrying; output presence does not establish task completion.")
			break
		}
	}
	if p.Mode == "chain" && p.ChainSteps != nil {
		lines = append(lines, fmt.Sprintf("Chain steps: %d", *p.ChainSteps))
	}
	if p.AsyncID != "" {
		lines = append(lines, "Async id: "+p.AsyncID)
	}
	if p.AsyncDir != "" {
		lines = append(lines, "Async dir: "+p.AsyncDir)
	}
	if p.ParallelHandoffPath != "" {
		lines = append(lines, "Parallel handoff: "+p.ParallelHandoffPath)
	}
	if g := asyncResumeGuidance(p.Source, p.Children, p.AsyncID); g != "" {
		lines = append(lines, g)
	}
	for _, c := range p.Children {
		if c.IntercomTarget != "" {
			lines = append(lines, "")
			if p.Source == "async" {
				lines = append(lines, "Previous intercom targets below identify child sessions used while they were running. Inspect artifacts or session logs if resume is unavailable.")
			} else {
				lines = append(lines, "Intercom targets below identify child sessions used while they were running; completed child sessions may no longer be reachable. Inspect artifacts or session logs for follow-up.")
			}
			break
		}
	}
	for i, c := range p.Children {
		lines = append(lines, "")
		output := c.OutputState
		if output == "" {
			output = "unknown"
		}
		lines = append(lines, fmt.Sprintf("%d. %s — process %s · output %s", i+1, c.Agent, c.Status, output))
		if c.IntercomTarget != "" {
			label := "Run intercom target"
			if p.Source == "async" {
				label = "Previous intercom target"
			}
			lines = append(lines, label+": "+c.IntercomTarget)
		}
		if c.ArtifactPath != "" {
			lines = append(lines, "Output artifact: "+c.ArtifactPath)
		}
		if c.SessionPath != "" {
			lines = append(lines, "Session: "+c.SessionPath)
		}
		lines = append(lines, formatNestedResultLines(c.Children)...)
		lines = append(lines, "Summary:", c.Summary)
	}
	return strings.Join(lines, "\n")
}

// buildSubagentResultIntercomPayload builds the one grouped payload a run delivers: counts, per-child sections, the message text.
func buildSubagentResultIntercomPayload(in groupedResultInput) *resultIntercomPayload {
	children := make([]resultIntercomChild, len(in.Children))
	for i, c := range in.Children {
		if c.OutputState == "" {
			c.OutputState = "unknown"
		}
		if jsTrim(c.Summary) == "" {
			c.Summary = "(no output)"
		} else {
			c.Summary = jsTrim(c.Summary)
		}
		c.Children = compactNestedResultChildren(c.Children)
		children[i] = c
	}
	p := &resultIntercomPayload{To: in.To, RunID: in.RunID, Mode: in.Mode, Status: resolveGroupedStatus(children), Summary: formatStatusCounts(countResultStatuses(children)), Source: in.Source,
		Children: children, AsyncID: in.AsyncID, AsyncDir: in.AsyncDir, ChainSteps: in.ChainSteps, ParallelHandoffPath: in.ParallelHandoffPath}
	if len(children) > 0 {
		first := children[0]
		p.Agent, p.Index, p.ArtifactPath, p.SessionPath = first.Agent, first.Index, first.ArtifactPath, first.SessionPath
	}
	p.Message = formatSubagentResultIntercomMessage(p)
	return p
}

// formatSubagentResultReceipt is the compact receipt of a grouped delivery: counts, artifacts, run intercom targets, sessions.
func formatSubagentResultReceipt(mode, runID string, p *resultIntercomPayload) string {
	label := "chain subagent results"
	switch mode {
	case "single":
		label = "single subagent result"
	case "parallel":
		label = "parallel subagent results"
	}
	lines := []string{"Delivered " + label + " via intercom.", "Run: " + runID, "Children: " + formatStatusCounts(countResultStatuses(p.Children))}
	if p.ParallelHandoffPath != "" {
		lines = append(lines, "Parallel handoff: "+p.ParallelHandoffPath)
	}
	section := func(title string, pick func(resultIntercomChild) string) {
		var entries []string
		for _, c := range p.Children {
			if v := pick(c); v != "" {
				entries = append(entries, fmt.Sprintf("- %s [%s]: %s", c.Agent, c.Status, v))
			}
		}
		if len(entries) > 0 {
			lines = append(lines, title)
			lines = append(lines, entries...)
		}
	}
	section("Artifacts:", func(c resultIntercomChild) string { return c.ArtifactPath })
	section("Run intercom targets (may be inactive after completion):", func(c resultIntercomChild) string { return c.IntercomTarget })
	section("Sessions:", func(c resultIntercomChild) string { return c.SessionPath })
	lines = append(lines, "Full grouped output was sent over intercom.")
	return strings.Join(lines, "\n")
}

// stripDetailsOutputsForIntercomReceipt drops the heavy per-result fields (messages, final output, truncation) of a result's
// details; each result is a decoded object.
func stripDetailsOutputsForIntercomReceipt(details map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range details {
		out[k] = v
	}
	if results, ok := details["results"].([]any); ok {
		stripped := make([]any, len(results))
		for i, r := range results {
			if m, ok := r.(map[string]any); ok {
				c := map[string]any{}
				for k, v := range m {
					if k != "messages" && k != "finalOutput" && k != "truncation" {
						c[k] = v
					}
				}
				stripped[i] = c
			} else {
				stripped[i] = r
			}
		}
		out["results"] = stripped
	}
	return out
}

// intercomEventBus is the part of an event bus the delivery needs (the original's IntercomEventBus).
type intercomEventBus interface {
	On(channel string, handler func(any)) func()
	Emit(channel string, payload any)
}

// deliverSubagentIntercomMessageEvent asks the intercom to deliver a message and waits for its delivery event (default 500 ms): true
// when the intercom confirmed delivery of this request, false on a timeout, a refusal or a bus that cannot do it.
func deliverSubagentIntercomMessageEvent(events intercomEventBus, to, message string, timeout time.Duration, extra map[string]any) bool {
	if events == nil {
		return false
	}
	requestID, _ := extra["requestId"].(string)
	if requestID == "" {
		requestID = randomUUID()
	}
	result := make(chan bool, 1)
	finish := func(delivered bool) {
		select {
		case result <- delivered:
		default:
		}
	}
	off := events.On(subagentResultIntercomDeliveryEvent, func(data any) {
		m, ok := data.(map[string]any)
		if !ok || m["requestId"] != requestID {
			return
		}
		finish(m["delivered"] == true)
	})
	defer off()
	payload := map[string]any{}
	for k, v := range extra {
		payload[k] = v
	}
	payload["to"], payload["message"], payload["requestId"] = to, message, requestID
	func() {
		defer func() {
			if recover() != nil {
				finish(false)
			}
		}()
		events.Emit(subagentResultIntercomEvent, payload)
	}()
	select {
	case ok := <-result:
		return ok
	case <-time.After(timeout):
		return false
	}
}
