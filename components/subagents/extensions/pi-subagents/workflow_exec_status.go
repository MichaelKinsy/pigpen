package pi_subagents

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The durable record of an async workflow (status.json, events.jsonl, the lane graph and the host rows), kept by the process
// that runs the script: the original's runner does not run workflow scripts, the extension does (subagent-executor.ts:5421-5980).

func rawJSON(v any) json.RawMessage {
	b, err := jsonMarshalNoEscape(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return json.RawMessage(b)
}

func setExtra(m *map[string]json.RawMessage, k string, v any) {
	if *m == nil {
		*m = map[string]json.RawMessage{}
	}
	(*m)[k] = rawJSON(v)
}

func delExtra(m *map[string]json.RawMessage, k string) {
	if *m != nil {
		delete(*m, k)
	}
}

func stepExtraString(s *AsyncStep, k string) string {
	var out string
	if raw, ok := s.Extra[k]; ok {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

// traceJSON is the trace as ordered JSON.
func traceJSON(trace []WorkflowScriptTraceEntry) []any {
	out := make([]any, len(trace))
	for i, e := range trace {
		out[i] = jsAny(e)
	}
	return out
}

// jsAny is a Go value (struct, slice) as the ordered JSON the status and result writers take.
func jsAny(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	out, err := parseJSON(b)
	if err != nil {
		return nil
	}
	return out
}

func traceLike(trace []WorkflowScriptTraceEntry) []WorkflowTraceLike {
	out := make([]WorkflowTraceLike, len(trace))
	for i, e := range trace {
		out[i] = WorkflowTraceLike{Operation: e.Operation, Key: e.Key, Phase: e.Phase, GeneratedLaneKey: e.GeneratedLaneKey, Warning: e.Warning}
	}
	return out
}

// annotatedTrace is annotateWorkflowPreflightTrace over the trace rows.
func annotatedTrace(trace []WorkflowScriptTraceEntry, p *WorkflowPreflight) []WorkflowScriptTraceEntry {
	if p == nil {
		return trace
	}
	like := AnnotateWorkflowPreflightTrace(traceLike(trace), p)
	out := append([]WorkflowScriptTraceEntry(nil), trace...)
	for i := range out {
		if i < len(like) {
			out[i].Warning = like[i].Warning
		}
	}
	return out
}

// ---- the lane graph (buildWorkflowLaneGraph, applyWorkflowLaneTrace) ----

func graphNodes(g *jsObject) []any {
	n, _ := g.vals["nodes"].([]any)
	return n
}

// buildWorkflowLaneGraph is buildWorkflowLaneGraph: the stages a runs.lanes plan declares, as nodes of the workflow graph.
func buildWorkflowLaneGraph(runID string, lanes []any, existing *jsObject) *jsObject {
	planned := map[string]bool{}
	for _, l := range lanes {
		lo, _ := l.(*jsObject)
		stages, _ := lo.vals["stages"].([]any)
		for _, s := range stages {
			if so, ok := s.(*jsObject); ok {
				planned[objStr(so, "generatedKey")] = true
			}
		}
	}
	previous := map[string]*jsObject{}
	var prevNodes []any
	var prevPhases []any
	if existing != nil {
		prevNodes = graphNodes(existing)
		prevPhases, _ = existing.vals["phases"].([]any)
	}
	for _, n := range prevNodes {
		if no, ok := n.(*jsObject); ok {
			previous[objStr(no, "id")] = no
		}
	}
	nodes := []any{}
	phases := []any{}
	flat := 0
	for _, l := range lanes {
		lo, _ := l.(*jsObject)
		stages, _ := lo.vals["stages"].([]any)
		var ids []any
		for si, s := range stages {
			so, _ := s.(*jsObject)
			gk := objStr(so, "generatedKey")
			prev := previous[gk]
			pick := func(k string) any {
				if v, ok := so.vals[k]; ok && !isUndefinedValue(v) && v != nil && v != "" {
					return v
				}
				if prev != nil {
					return prev.vals[k]
				}
				return nil
			}
			node := newObject()
			node.set("id", gk)
			node.set("kind", "step")
			if v := pick("agent"); v != nil {
				node.set("agent", v)
			}
			if v := pick("phase"); v != nil {
				node.set("phase", v)
			}
			label := objStr(so, "label")
			if label == "" {
				label = objStr(so, "key")
			}
			node.set("label", label)
			status := "pending"
			if si == 0 {
				status = "running"
			}
			if prev != nil && objStr(prev, "status") != "" {
				status = objStr(prev, "status")
			}
			node.set("status", status)
			node.set("flatIndex", float64(flat))
			node.set("stepIndex", float64(flat))
			if out := objStr(so, "outputName"); out != "" {
				node.set("outputName", out)
			} else if prev != nil && objStr(prev, "outputName") != "" {
				node.set("outputName", objStr(prev, "outputName"))
			}
			if v, ok := so.vals["structured"]; ok && !isUndefinedValue(v) {
				node.set("structured", v)
			} else if prev != nil && prev.has("structured") {
				node.set("structured", prev.vals["structured"])
			}
			if prev != nil {
				if a := objStr(prev, "acceptanceStatus"); a != "" {
					node.set("acceptanceStatus", a)
				}
				if e := objStr(prev, "error"); e != "" {
					node.set("error", e)
				}
			}
			nodes = append(nodes, node)
			ids = append(ids, gk)
			flat++
		}
		if len(ids) > 0 {
			phases = append(phases, jo("title", objStr(lo, "key"), "nodeIds", ids))
		}
	}
	for _, n := range prevNodes {
		if no, ok := n.(*jsObject); ok && !planned[objStr(no, "id")] {
			nodes = append(nodes, no)
		}
	}
	for _, p := range prevPhases {
		po, ok := p.(*jsObject)
		if !ok {
			continue
		}
		ids, _ := po.vals["nodeIds"].([]any)
		var retained []any
		for _, id := range ids {
			if s, _ := id.(string); !planned[s] {
				retained = append(retained, id)
			}
		}
		if len(retained) == 0 {
			continue
		}
		merged := false
		for _, cur := range phases {
			co := cur.(*jsObject)
			if objStr(co, "title") == objStr(po, "title") {
				co.set("nodeIds", append(co.vals["nodeIds"].([]any), retained...))
				merged = true
				break
			}
		}
		if !merged {
			phases = append(phases, jo("title", objStr(po, "title"), "nodeIds", retained))
		}
	}
	g := jo("runId", runID, "mode", "workflow", "phases", phases, "nodes", nodes)
	currentID := ""
	for _, n := range nodes {
		if no := n.(*jsObject); objStr(no, "status") == "running" {
			currentID = objStr(no, "id")
			break
		}
	}
	if currentID == "" && existing != nil {
		currentID = objStr(existing, "currentNodeId")
	}
	if currentID != "" {
		g.set("currentNodeId", currentID)
	}
	return g
}

func workflowLaneTraceStatus(state string) string {
	switch state {
	case "started":
		return "running"
	case "completed":
		return "completed"
	case "failed":
		return "failed"
	case "detached":
		return "detached"
	case "stopped":
		return "stopped"
	}
	return ""
}

// applyWorkflowLaneTrace is applyWorkflowLaneTrace: the trace's states onto the lane nodes.
func applyWorkflowLaneTrace(g *jsObject, trace []WorkflowScriptTraceEntry) {
	byID := map[string]*jsObject{}
	for _, n := range graphNodes(g) {
		if no, ok := n.(*jsObject); ok {
			byID[objStr(no, "id")] = no
		}
	}
	for _, e := range trace {
		if e.Operation != "run" {
			continue
		}
		node := byID[e.Key]
		if node == nil {
			continue
		}
		status := workflowLaneTraceStatus(e.State)
		if status != "" {
			node.set("status", status)
		}
		if e.Agent != "" {
			node.set("agent", e.Agent)
		}
		if e.Phase != "" && (e.Phase != "auto-resume" || !node.has("phase")) {
			node.set("phase", e.Phase)
		}
		if e.Label != "" {
			node.set("label", e.Label)
		}
		if e.Error != "" {
			node.set("error", e.Error)
		} else if status == "completed" || status == "running" {
			delete(node.vals, "error")
			node.keys = wfRemoveKey(node.keys, "error")
		}
	}
	current := ""
	for _, n := range graphNodes(g) {
		if no, ok := n.(*jsObject); ok && objStr(no, "status") == "running" {
			current = objStr(no, "id")
			break
		}
	}
	if current != "" {
		g.set("currentNodeId", current)
	} else {
		delete(g.vals, "currentNodeId")
		g.keys = wfRemoveKey(g.keys, "currentNodeId")
	}
}

func wfRemoveKey(keys []string, k string) []string {
	out := keys[:0:0]
	for _, x := range keys {
		if x != k {
			out = append(out, x)
		}
	}
	return out
}

// hostStepNodeOf is the parsed host row of a host callback.
func hostStepNodeOf(n WorkflowHostStepNode) *WorkflowHostStep {
	h := &WorkflowHostStep{MonitorKind: "command", ID: n.ID, Label: n.Label, Role: n.Role, Provider: n.Provider, State: n.State, Verdict: n.Verdict, ReasonCode: n.ReasonCode, Detail: n.Detail, ReportPath: n.ReportPath, UpdatedAt: n.UpdatedAt}
	if n.DeadlineAt != 0 {
		d := n.DeadlineAt
		h.DeadlineAt = &d
	}
	if n.State == "done" || n.State == "error" || n.State == "cancelled" {
		h.HasExitCode, h.ExitCode = true, n.ExitCode
	}
	return h
}

func hostStepGraphStatus(h *WorkflowHostStep) string {
	switch {
	case h.State == "pending":
		return "pending"
	case h.State == "running":
		return "running"
	case h.State == "cancelled":
		return "stopped"
	case h.State == "error":
		return "failed"
	case h.Verdict == "" || h.Verdict == "inconclusive":
		return "partial"
	case h.Verdict == "fail":
		return "failed"
	}
	return "completed"
}

// upsertHostStep is upsertHostStep (host-step-status.ts:188): the host row of a command in the workflow graph, at most
// WorkflowHostStepMaxCount of them.
func upsertHostStep(g *jsObject, runID string, h *WorkflowHostStep) (*jsObject, error) {
	if g == nil {
		g = jo("runId", runID, "mode", "workflow", "phases", []any{}, "nodes", []any{})
	}
	nodes := append([]any(nil), graphNodes(g)...)
	idx := -1
	hostCount := 0
	for i, n := range nodes {
		no, _ := n.(*jsObject)
		if no == nil {
			continue
		}
		if objStr(no, "kind") == "host-step" {
			hostCount++
		}
		if objStr(no, "id") == h.ID {
			idx = i
		}
	}
	if idx >= 0 && objStr(nodes[idx].(*jsObject), "kind") != "host-step" {
		return g, fmt.Errorf("Host step '%s' conflicts with an existing workflow node.", h.ID)
	}
	if idx < 0 && hostCount >= WorkflowHostStepMaxCount {
		return g, fmt.Errorf("Host step limit of %d has been reached.", WorkflowHostStepMaxCount)
	}
	node := jo("id", h.ID, "kind", "host-step", "label", h.Label, "status", hostStepGraphStatus(h), "hostStep", h.Object())
	if idx >= 0 {
		nodes[idx] = node
	} else {
		nodes = append(nodes, node)
	}
	next := g.clone()
	next.set("nodes", nodes)
	return next, nil
}

// ---- the status writer ----

// wfStatus is the durable state of one async workflow.
type wfStatus struct {
	x         *wfExec
	mu        sync.Mutex
	asyncDir  string
	resultDir string
	st        AsyncStatus
	steps     map[string]*AsyncStep // by workflow key
	workflow  *jsObject
	graph     *jsObject
	trace     []WorkflowScriptTraceEntry
	projected int
	tail      WorkflowScriptTraceEntry
	closed    bool
	initial   bool
	indexed   string
	degraded  bool
}

func (s *wfStatus) eventsPath() string { return filepath.Join(s.asyncDir, "events.jsonl") }

// appendEvent is appendWorkflowEvent: the journal, not workflow truth, so a write that fails is logged only.
func (s *wfStatus) appendEvent(event *jsObject) {
	rec := jo("ts", float64(time.Now().UnixMilli()), "runId", s.st.RunID)
	for _, k := range event.order() {
		rec.set(k, event.vals[k])
	}
	f, err := os.OpenFile(s.eventsPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		_, err = f.WriteString(marshalJSON(rec, "") + "\n")
		f.Close()
	}
	if err != nil {
		logAsyncError(fmt.Sprintf("Failed to append async workflow event '%s':", s.eventsPath()), err)
	}
}

func workflowStateOf(state string) string {
	switch state {
	case "complete":
		return "completed"
	case "failed", "rejected":
		return "failed"
	case "paused":
		return "paused"
	case "stopped":
		return "stopped"
	}
	return "running"
}

func (s *wfStatus) stepViews() []WorkflowStepStatus {
	out := make([]WorkflowStepStatus, 0, len(s.st.Steps))
	for i := range s.st.Steps {
		st := &s.st.Steps[i]
		out = append(out, WorkflowStepStatus{WorkflowKey: st.WorkflowKey, Async: st.Async, RunID: st.RunID, Status: st.Status, SessionFile: st.SessionFile, Model: st.Model, Agent: st.Agent, SessionName: st.SessionName, Thinking: st.Thinking})
	}
	return out
}

// persistLocked writes status.json and the active-run index. final writes fail loudly; progress writes only log.
func (s *wfStatus) persistLocked(strict bool) error {
	if s.closed {
		return nil
	}
	ws := workflowStateOf(s.st.State)
	summary, err := WorkflowChildSummaryOf(WorkflowChildSummaryInput{ParentToolCallID: s.st.ToolCallID, WorkflowRunID: s.st.RunID, WorkflowState: ws, InventoryComplete: ws != "running", Trace: s.trace, Steps: s.stepViews()})
	if err == nil {
		setExtra(&s.st.Extra, "workflowChildren", summary)
	}
	now := ms(nowMs())
	s.st.LastUpdate = &now
	if s.workflow != nil {
		setExtra(&s.st.Extra, "workflow", s.workflow)
	}
	if s.graph != nil {
		setExtra(&s.st.Extra, "workflowGraph", s.graph)
	}
	if err := writeStatus(s.asyncDir, &s.st); err != nil {
		if strict {
			return err
		}
		msg := fmt.Sprintf("Failed to persist async workflow state %s: %s", filepath.Join(s.asyncDir, "status.json"), err.Error())
		logAsyncError(msg)
		if !s.degraded {
			s.degraded = true
			s.appendEvent(jo("type", "subagent.workflow.status_write_failed", "error", msg))
		}
		return nil
	}
	s.degraded = false
	if s.indexed != s.st.State {
		if err := updateActiveRunIndex(s.asyncDir, s.st.State, s.st.ToolCallID, indexOptions{retryCapacityErrors: true}); err != nil {
			logAsyncError(fmt.Sprintf("Failed to update async workflow index '%s':", s.asyncDir), err)
		} else {
			s.indexed = s.st.State
		}
	}
	if rt := bgRT; rt != nil {
		rt.refreshJob(s.asyncDir)
		rt.refreshWidget()
	}
	return nil
}

func (s *wfStatus) persist() {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.persistLocked(false)
}

// updateTrace is updateTrace of the original: the trace into the steps, the graph and the workflow record.
func (s *wfStatus) updateTrace(trace []WorkflowScriptTraceEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.trace = trace
	preflight := s.x.preflight
	projected := annotatedTrace(trace, preflight)
	wf := s.workflow
	wf.set("trace", traceJSON(projected))
	if preflight != nil {
		if w := WorkflowPreflightWarnings(preflight, traceLike(trace), false); len(w) > 0 {
			wf.set("preflightWarnings", anyStrings(w))
		} else {
			delete(wf.vals, "preflightWarnings")
			wf.keys = wfRemoveKey(wf.keys, "preflightWarnings")
		}
	}
	if s.graph != nil {
		applyWorkflowLaneTrace(s.graph, trace)
	}
	same := func(a, b WorkflowScriptTraceEntry) bool {
		return a.Operation == b.Operation && a.Key == b.Key && a.State == b.State && a.RunID == b.RunID && a.Phase == b.Phase && a.Label == b.Label && a.Agent == b.Agent
	}
	rebuild := len(trace) < s.projected || (s.projected > 0 && !same(trace[s.projected-1], s.tail))
	if rebuild {
		s.steps = map[string]*AsyncStep{}
		for i := range s.st.Steps {
			if k := s.st.Steps[i].WorkflowKey; k != "" {
				s.steps[k] = &s.st.Steps[i]
			}
		}
		s.projected = 0
	}
	for i := s.projected; i < len(trace); i++ {
		e := trace[i]
		if e.Operation != "run" {
			continue
		}
		label := jsTrim(e.Label)
		phase := jsTrim(e.Phase)
		existing := s.steps[e.Key]
		if existing != nil {
			if label != "" {
				setExtra(&existing.Extra, "label", label)
			}
			if phase != "" && (phase != "auto-resume" || stepExtraString(existing, "phase") == "") {
				setExtra(&existing.Extra, "phase", phase)
			}
		}
		if e.State == "reused" && existing != nil {
			continue
		}
		mapped := "failed"
		switch e.State {
		case "started", "reused":
			mapped = "running"
		case "completed":
			mapped = "completed"
		case "stopped":
			mapped = "stopped"
		case "detached":
			mapped = "paused"
		}
		if existing != nil {
			existing.Status = mapped
			if e.Agent != "" {
				existing.Agent = e.Agent
			}
			if e.RunID != "" {
				existing.RunID = e.RunID
			}
			if e.State == "failed" && e.RunID == "" && existing.Async == nil {
				f := false
				existing.Async = &f
			}
			if e.State == "detached" {
				existing.ActivityState = "needs_attention"
			} else if mapped != "running" {
				existing.ActivityState = ""
			}
			if e.State == "stopped" {
				t := true
				existing.Stopped = &t
			} else {
				existing.Stopped = nil
			}
			existing.Error = e.Error
			if e.DurationMs != nil {
				d := ms(*e.DurationMs)
				existing.DurationMs = &d
			} else {
				existing.DurationMs = nil
			}
		} else {
			agent := e.Agent
			if agent == "" {
				agent = e.Key
			}
			started := ms(nowMs())
			step := AsyncStep{Agent: agent, WorkflowKey: e.Key, Status: mapped, StartedAt: &started, RunID: e.RunID}
			lab := label
			if lab == "" {
				lab = e.Key
			}
			setExtra(&step.Extra, "label", lab)
			setExtra(&step.Extra, "parentWorkflowRunId", s.st.RunID)
			if phase != "" {
				setExtra(&step.Extra, "phase", phase)
			}
			if e.State == "failed" && e.RunID == "" {
				f := false
				step.Async = &f
			}
			if e.State == "detached" {
				step.ActivityState = "needs_attention"
			}
			if e.State == "stopped" {
				t := true
				step.Stopped = &t
			}
			s.st.Steps = append(s.st.Steps, step)
			// the slice may have moved: rebuild the index
			s.steps = map[string]*AsyncStep{}
			for j := range s.st.Steps {
				if k := s.st.Steps[j].WorkflowKey; k != "" {
					s.steps[k] = &s.st.Steps[j]
				}
			}
		}
		if e.State == "stopped" {
			if ps := s.steps[e.Key]; ps != nil {
				ev := jo("type", "subagent.child-status", "version", 1.0, "childId", e.Key, "status", "stopped", "reason", "subagent-action", "source", "async", "agent", ps.Agent, "workflowKey", e.Key)
				if ps.RunID != "" {
					ev.set("childRunId", ps.RunID)
				}
				s.appendEvent(ev)
			}
		}
	}
	s.projected = len(trace)
	if len(trace) > 0 {
		s.tail = trace[len(trace)-1]
	}
	_ = s.persistLocked(false)
	s.appendEvent(jo("type", "subagent.workflow.trace", "trace", traceJSON(trace)))
}

// childStep is the step of a workflow key.
func (s *wfStatus) childStep(key string) *AsyncStep {
	for i := range s.st.Steps {
		if s.st.Steps[i].WorkflowKey == key {
			return &s.st.Steps[i]
		}
	}
	return nil
}

// childLaunched records what the launch of a child learned (its agent and run id) in its step.
func (s *wfStatus) childLaunched(key, agent, runID string, async bool, lane any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.childStep(key)
	if st == nil {
		return
	}
	if agent != "" {
		st.Agent = agent
	}
	st.Async = &async
	if runID != "" {
		st.RunID = runID
	}
	if lane != nil {
		setExtra(&st.Extra, "lane", lane)
	}
	if st.RunID != "" {
		s.announceLocked(key, st)
	}
	_ = s.persistLocked(false)
}

// announceLocked emits the child-status event of a child with a run id, once.
func (s *wfStatus) announceLocked(key string, st *AsyncStep) {
	if s.x.announced[key] {
		return
	}
	s.x.announced[key] = true
	idx := -1
	for i := range s.st.Steps {
		if &s.st.Steps[i] == st {
			idx = i
		}
	}
	ev := jo("type", "subagent.child-status", "version", 1.0, "runId", s.st.RunID, "childId", key, "status", "started", "ts", float64(time.Now().UnixMilli()), "source", "async", "asyncDir", s.asyncDir)
	if idx >= 0 {
		ev.set("stepIndex", float64(idx))
	}
	ev.set("agent", st.Agent)
	if st.RunID != "" {
		ev.set("childRunId", st.RunID)
	}
	ev.set("workflowKey", key)
	if p := stepExtraString(st, "phase"); p != "" {
		ev.set("phase", p)
	}
	if l := stepExtraString(st, "label"); l != "" {
		ev.set("label", l)
	}
	s.appendEvent(ev)
	if rt := bgRT; rt != nil {
		if ctx, ok := rt.context(); ok {
			_ = ctx.Events().Emit("subagent:child-status", ev)
		}
	}
}

func (s *wfStatus) lanePlan(lanes []any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.graph = buildWorkflowLaneGraph(s.st.RunID, lanes, s.graph)
	applyWorkflowLaneTrace(s.graph, s.trace)
	_ = s.persistLocked(false)
}

func (s *wfStatus) hostStep(n WorkflowHostStepNode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := hostStepNodeOf(n)
	g, err := upsertHostStep(s.graph, s.st.RunID, h)
	if err != nil {
		logAsyncError(err.Error())
		return
	}
	s.graph = g
	if last := ms(n.UpdatedAt); s.st.LastUpdate == nil || last > *s.st.LastUpdate {
		s.st.LastUpdate = &last
	}
	_ = s.persistLocked(false)
}

func (s *wfStatus) emits(emits []any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workflow.set("emits", emits)
	_ = s.persistLocked(false)
	if len(emits) > 0 {
		s.appendEvent(jo("type", "subagent.workflow.emit", "value", emits[len(emits)-1]))
	}
}

func joinOutputs(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " ")
}
