package pi_subagents

import (
	"math"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Port of src/workflows/workflow-checklist.ts: the checklist a status surface shows for a workflow, fused from the graph, the
// loaded child steps, the host monitors, the preflight declaration and the trace, so that no child appears twice.
// Optional strings are "" when absent and optional numbers are nil (the one difference from the original: an explicitly empty
// label or key reads as absent).

// WorkflowChecklistStep is one loaded child (an async status step or a foreground child).
type WorkflowChecklistStep struct {
	Key                  string
	Thinking             string
	WorkflowKey          string
	RunID                string
	Label                string
	Description          string
	Phase                string
	Agent                string
	Status               string
	Context              string // fresh or fork
	ActivityState        string
	StartedAt            *float64
	EndedAt              *float64
	DurationMs           *float64
	CurrentTool          string
	CurrentToolStartedAt *float64
	CurrentPath          string
	TurnCount            *float64
	ToolCount            *float64
	OutputName           string
	Error                string
	ToolBudgetBlocked    bool
	TurnBudgetExceeded   bool
	TimedOut             bool
	Stopped              bool
	AcceptanceStatus     string
	ReviewResultStatus   string // acceptance.reviewResult.status
	ReviewStatus         string
	WatchdogPhase        string
}

// WorkflowChecklistTraceEntry is the part of a trace entry the checklist reads.
type WorkflowChecklistTraceEntry struct {
	Operation        string
	Key              string
	State            string
	Agent            string
	RunID            string
	Phase            string
	Label            string
	GeneratedLaneKey string
	DurationMs       *float64
	Error            string
}

// WorkflowChecklistItem is one row of a phase.
type WorkflowChecklistItem struct {
	Key                  string
	Label                string
	Phase                string
	State                string // complete, running, queued, blocked, failed, paused, stopped
	Agent                string
	Thinking             string
	Context              string
	StartedAt            *float64
	DurationMs           *float64
	CurrentTool          string
	CurrentToolStartedAt *float64
	CurrentPath          string
	ToolCount            *float64
	OutputName           string
	Error                string
	Preflight            *WorkflowPreflightLane
	Kind                 string // child or host
	MonitorKind          string
	Provider             string
	Role                 string
	Verdict              string
	Target               string
	ReasonCode           string
	Stale                *bool
	ReportPath           string
}

// WorkflowChecklistPhase groups the rows of one phase.
type WorkflowChecklistPhase struct {
	Key      string
	Label    string
	State    string
	Items    []*WorkflowChecklistItem
	Total    int
	Done     int
	Running  int
	Queued   int
	Blocked  int
	Failed   int
	Paused   int
	Stopped  int
	Parallel bool
}

// WorkflowChecklistProjection is the whole checklist.
type WorkflowChecklistProjection struct {
	Phases     []*WorkflowChecklistPhase
	Total      int
	Done       int
	Running    int
	Queued     int
	Blocked    int
	Failed     int
	Paused     int
	Stopped    int
	Bottleneck *WorkflowChecklistItem
}

// WorkflowChecklistInput is the argument of projectWorkflowChecklist.
type WorkflowChecklistInput struct {
	Graph     *WorkflowGraphSnapshot
	Steps     []WorkflowChecklistStep
	HostSteps []*WorkflowHostStep
	Preflight *WorkflowPreflight
	Trace     []WorkflowChecklistTraceEntry
	Now       *float64
}

const checklistMaxText = 160

var checklistSpaceRe = regexp.MustCompile(`[\s\x{feff}\x{a0}]+`)

// checklistText is text(): display-safe, one line, at most 160 UTF-16 units; "" when nothing is left.
func checklistText(value string) string {
	clean := jsTrim(checklistSpaceRe.ReplaceAllString(wfSanitizeDisplayText(value), " "))
	if clean == "" {
		return ""
	}
	return string(utf16Slice(clean, checklistMaxText))
}

func checklistKeyText(value, fallback string) string {
	if t := checklistText(value); t != "" {
		return t
	}
	return fallback
}

func checklistNormalizedStatus(value string) string {
	if value == "" {
		return "queued"
	}
	return strings.ToLower(value)
}

func checklistFinite(v *float64) *float64 {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
		return nil
	}
	return v
}

func checklistCount(v *float64) *float64 {
	f := checklistFinite(v)
	if f == nil || *f < 0 {
		return nil
	}
	r := jsRound(*f)
	return &r
}

// checklistSource is what explicitBlocked and checklistState read of a step, a node or a host monitor.
type checklistSource struct {
	Status             string
	ActivityState      string
	ToolBudgetBlocked  bool
	TurnBudgetExceeded bool
	TimedOut           bool
	AcceptanceStatus   string
	ReviewResultStatus string
	ReviewStatus       string
	WatchdogPhase      string
	Verdict            string
	Stale              bool
}

func (s WorkflowChecklistStep) source() checklistSource {
	return checklistSource{Status: s.Status, ActivityState: s.ActivityState, ToolBudgetBlocked: s.ToolBudgetBlocked, TurnBudgetExceeded: s.TurnBudgetExceeded, TimedOut: s.TimedOut,
		AcceptanceStatus: s.AcceptanceStatus, ReviewResultStatus: s.ReviewResultStatus, ReviewStatus: s.ReviewStatus, WatchdogPhase: s.WatchdogPhase}
}

func checklistExplicitBlocked(s checklistSource) bool {
	acceptance := checklistNormalizedStatus(s.AcceptanceStatus)
	reviewSource := s.ReviewStatus
	if reviewSource == "" {
		reviewSource = s.ReviewResultStatus
	}
	review := checklistNormalizedStatus(reviewSource)
	return s.ToolBudgetBlocked || s.TurnBudgetExceeded || s.ActivityState == "needs_attention" || s.TimedOut || s.WatchdogPhase == "stale" || s.Stale ||
		s.Verdict == "inconclusive" || acceptance == "rejected" || acceptance == "blockers" || review == "blockers" || review == "review-required"
}

func checklistState(s checklistSource) string {
	if checklistExplicitBlocked(s) {
		return "blocked"
	}
	switch checklistNormalizedStatus(s.Status) {
	case "complete", "completed", "done", "pass", "accepted":
		return "complete"
	case "running", "started", "active":
		return "running"
	case "failed", "error", "fail":
		return "failed"
	case "blocked", "rejected", "partial", "needs_attention":
		return "blocked"
	case "paused", "detached":
		return "paused"
	case "stopped", "cancelled", "canceled":
		return "stopped"
	}
	return "queued"
}

func checklistTerminal(state string) bool {
	switch state {
	case "complete", "blocked", "failed", "paused", "stopped":
		return true
	}
	return false
}

func checklistDuration(step WorkflowChecklistStep, now *float64, state string) *float64 {
	explicit := checklistFinite(step.DurationMs)
	started := checklistFinite(step.StartedAt)
	if explicit != nil {
		v := math.Max(0, *explicit)
		return &v
	}
	if started == nil {
		return nil
	}
	end := checklistFinite(step.EndedAt)
	if state == "running" {
		end = now
	}
	if end == nil {
		return nil
	}
	v := math.Max(0, *end-*started)
	return &v
}

func checklistStepKey(s WorkflowChecklistStep) string {
	for _, k := range []string{s.Key, s.WorkflowKey, s.RunID} {
		if k != "" {
			return k
		}
	}
	return ""
}

func checklistThinking(s WorkflowChecklistStep) string {
	switch s.Thinking {
	case "off", "minimal", "low", "medium", "high", "xhigh", "max":
		return s.Thinking
	}
	return ""
}

// checklistStepItem is stepItem. label "" is the default parameter: label, description, key, agent, then key.
func checklistStepItem(step WorkflowChecklistStep, index int, phase, key, label string, preflight *WorkflowPreflightLane) *WorkflowChecklistItem {
	if key == "" {
		key = checklistStepKey(step)
	}
	if key == "" {
		key = "step-" + itoa(index+1)
	}
	if label == "" {
		for _, c := range []string{step.Label, step.Description, checklistStepKey(step), step.Agent, key} {
			if c != "" {
				label = c
				break
			}
		}
	}
	state := checklistState(step.source())
	item := &WorkflowChecklistItem{
		Key: checklistKeyText(key, "step-"+itoa(index+1)), Label: checklistKeyText(label, key), Phase: phase, State: state,
		Agent: checklistText(step.Agent), Thinking: checklistThinking(step), Context: step.Context, StartedAt: checklistFinite(step.StartedAt),
		DurationMs: checklistDuration(step, nil, state), CurrentTool: checklistText(step.CurrentTool), CurrentToolStartedAt: checklistFinite(step.CurrentToolStartedAt),
		CurrentPath: checklistText(step.CurrentPath), ToolCount: checklistCount(step.ToolCount), OutputName: checklistText(step.OutputName), Error: checklistText(step.Error),
		Preflight: preflight,
	}
	return item
}

func checklistHostItem(host *WorkflowHostStep, phase, key string) *WorkflowChecklistItem {
	stale := host.Freshness != nil && host.Freshness.Stale != nil && *host.Freshness.Stale
	state := checklistState(checklistSource{Status: host.State, Verdict: host.Verdict, Stale: stale})
	item := &WorkflowChecklistItem{
		Key: checklistKeyText(key, "host-step"), Label: checklistKeyText(host.Label, "host step"), Phase: phase, State: state, Kind: "host", MonitorKind: host.MonitorKind,
		Provider: checklistText(host.Provider), Role: checklistText(host.Role), Verdict: host.Verdict, Target: checklistText(host.Target), ReasonCode: checklistText(host.ReasonCode),
		Error: checklistText(host.Detail), ReportPath: checklistText(host.ReportPath),
	}
	if host.Freshness != nil && host.Freshness.Stale != nil {
		s := *host.Freshness.Stale
		item.Stale = &s
	}
	return item
}

func checklistGraphNodes(graph *WorkflowGraphSnapshot) []*WorkflowGraphNode {
	var out []*WorkflowGraphNode
	var visit func(n *WorkflowGraphNode)
	visit = func(n *WorkflowGraphNode) {
		if n.Kind == "parallel-group" || n.Kind == "dynamic-parallel-group" {
			for _, c := range n.Children {
				visit(c)
			}
			if len(n.Children) == 0 {
				out = append(out, n)
			}
			return
		}
		out = append(out, n)
	}
	if graph != nil {
		for _, n := range graph.Nodes {
			visit(n)
		}
	}
	return out
}

func checklistTraceSources(trace []WorkflowChecklistTraceEntry) []WorkflowChecklistTraceEntry {
	var order []string
	latest := map[string]WorkflowChecklistTraceEntry{}
	overlay := func(base, e WorkflowChecklistTraceEntry) WorkflowChecklistTraceEntry {
		set := func(dst *string, v string) {
			if v != "" {
				*dst = v
			}
		}
		set(&base.Operation, e.Operation)
		set(&base.Key, e.Key)
		set(&base.State, e.State)
		set(&base.Agent, e.Agent)
		set(&base.RunID, e.RunID)
		set(&base.Phase, e.Phase)
		set(&base.Label, e.Label)
		set(&base.GeneratedLaneKey, e.GeneratedLaneKey)
		set(&base.Error, e.Error)
		if e.DurationMs != nil {
			base.DurationMs = e.DurationMs
		}
		return base
	}
	for _, e := range trace {
		state := checklistNormalizedStatus(e.State)
		if (e.Operation != "" && e.Operation != "run" && e.Operation != "host") || e.Key == "" || state == "delivered" || state == "missed" {
			continue
		}
		existing, had := latest[e.Key]
		if !had {
			order = append(order, e.Key)
		}
		merged := overlay(existing, e)
		if state == "reused" && had {
			merged.State = existing.State
		}
		latest[e.Key] = merged
	}
	out := make([]WorkflowChecklistTraceEntry, len(order))
	for i, k := range order {
		out[i] = latest[k]
	}
	return out
}

func checklistTraceItem(e WorkflowChecklistTraceEntry, index int, preflight *WorkflowPreflightLane) *WorkflowChecklistItem {
	phaseSource := e.GeneratedLaneKey
	if preflight != nil {
		phaseSource = preflight.Key
	} else if phaseSource == "" {
		phaseSource = e.Phase
	}
	phase := checklistKeyText(phaseSource, "Workflow")
	status := e.State
	if status == "started" {
		status = "running"
	}
	label := e.Label
	if label == "" {
		label = e.Key
	}
	item := checklistStepItem(WorkflowChecklistStep{Key: e.Key, Label: e.Label, Phase: phase, Agent: e.Agent, Status: status, DurationMs: e.DurationMs, Error: e.Error}, index, phase, e.Key, label, preflight)
	if e.Operation == "host" {
		item.Kind = "host"
	}
	return item
}

type checklistPhases struct {
	order []string
	byKey map[string]*WorkflowChecklistPhase
}

func (p *checklistPhases) get(label string) *WorkflowChecklistPhase {
	if ph, ok := p.byKey[label]; ok {
		return ph
	}
	ph := &WorkflowChecklistPhase{Key: label, Label: label, State: "queued"}
	p.byKey[label] = ph
	p.order = append(p.order, label)
	return ph
}

func (p *checklistPhases) add(phase string, item *WorkflowChecklistItem) {
	ph := p.get(phase)
	ph.Items = append(ph.Items, item)
}

func checklistMergeNodeStep(node *WorkflowGraphNode, step WorkflowChecklistStep, phase string, trace *WorkflowChecklistTraceEntry, preflight *WorkflowPreflightLane) *WorkflowChecklistItem {
	state := checklistState(step.source())
	nodeState := checklistState(checklistSource{Status: node.Status, AcceptanceStatus: node.AcceptanceStatus})
	var status string
	switch {
	case checklistTerminal(nodeState) && !checklistTerminal(state):
		status = nodeState
	case trace != nil && !checklistTerminal(state):
		status = trace.State
		if status == "started" {
			status = "running"
		}
	case checklistNormalizedStatus(step.Status) == "pending" && checklistNormalizedStatus(node.Status) != "pending":
		status = node.Status
	default:
		status = step.Status
	}
	first := func(vs ...string) string {
		for _, v := range vs {
			if v != "" {
				return v
			}
		}
		return ""
	}
	merged := step
	merged.Status = status
	merged.Key = node.ID
	merged.Label = first(step.Label, node.Label)
	merged.Phase = first(step.Phase, phase)
	merged.Agent = first(step.Agent, node.Agent)
	merged.OutputName = first(step.OutputName, node.OutputName)
	traceError := ""
	if trace != nil {
		traceError = trace.Error
	}
	merged.Error = first(step.Error, traceError, node.Error)
	merged.AcceptanceStatus = first(step.AcceptanceStatus, node.AcceptanceStatus)
	if merged.DurationMs == nil && trace != nil {
		merged.DurationMs = trace.DurationMs
	}
	flat := 0
	if node.FlatIndex != nil {
		flat = *node.FlatIndex
	}
	return checklistStepItem(merged, flat, phase, node.ID, first(step.Label, node.Label), preflight)
}

func checklistPriority(item *WorkflowChecklistItem) int {
	switch item.State {
	case "blocked":
		return 0
	case "failed":
		return 1
	case "running":
		return 2
	case "paused":
		return 3
	case "stopped":
		return 4
	case "queued":
		return 5
	}
	return 6
}

func checklistApplyNow(item *WorkflowChecklistItem, now *float64) *WorkflowChecklistItem {
	if item.State == "running" && item.StartedAt != nil && now != nil {
		c := *item
		d := math.Max(0, *now-*item.StartedAt)
		c.DurationMs = &d
		return &c
	}
	return item
}

func checklistFinalize(ph *WorkflowChecklistPhase) {
	count := func(state string) int {
		n := 0
		for _, i := range ph.Items {
			if i.State == state {
				n++
			}
		}
		return n
	}
	ph.Total = len(ph.Items)
	ph.Done, ph.Running, ph.Queued, ph.Blocked = count("complete"), count("running"), count("queued"), count("blocked")
	ph.Failed, ph.Paused, ph.Stopped = count("failed"), count("paused"), count("stopped")
	ph.Parallel = ph.Total > 1
	switch {
	case ph.Blocked > 0:
		ph.State = "blocked"
	case ph.Failed > 0:
		ph.State = "failed"
	case ph.Running > 0:
		ph.State = "running"
	case ph.Paused > 0:
		ph.State = "paused"
	case ph.Stopped > 0:
		ph.State = "stopped"
	case ph.Queued > 0:
		ph.State = "queued"
	default:
		ph.State = "complete"
	}
}

func checklistLane(p *WorkflowPreflight, key string, preferred ...string) *WorkflowPreflightLane {
	return WorkflowPreflightLaneForRuntimeKey(p, key, preferred...)
}

// projectWorkflowChecklist is projectWorkflowChecklist.
func projectWorkflowChecklist(in WorkflowChecklistInput) WorkflowChecklistProjection {
	phases := &checklistPhases{byKey: map[string]*WorkflowChecklistPhase{}}
	nodes := checklistGraphNodes(in.Graph)
	trace := checklistTraceSources(in.Trace)
	traceByKey := map[string]*WorkflowChecklistTraceEntry{}
	for i := range trace {
		traceByKey[trace[i].Key] = &trace[i]
	}
	phaseByNode := map[string]string{}
	if in.Graph != nil {
		for _, ph := range in.Graph.Phases {
			title := checklistKeyText(ph.Title, "Workflow")
			phases.get(title)
			for _, id := range ph.NodeIDs {
				if _, has := phaseByNode[id]; !has {
					phaseByNode[id] = title
				}
			}
		}
	}

	type stepRef struct {
		step  WorkflowChecklistStep
		index int
	}
	stepsByKey := map[string][]stepRef{}
	for i, s := range in.Steps {
		if k := checklistStepKey(s); k != "" {
			stepsByKey[k] = append(stepsByKey[k], stepRef{s, i})
		}
	}
	used := map[int]bool{}
	var hostOrder []string
	hostByID := map[string]*WorkflowHostStep{}
	for _, h := range in.HostSteps {
		if _, has := hostByID[h.ID]; !has {
			hostOrder = append(hostOrder, h.ID)
		}
		hostByID[h.ID] = h
	}
	graphKeys := map[string]bool{}
	for _, n := range nodes {
		graphKeys[n.ID] = true
	}
	hostKeys := map[string]bool{}
	for id := range hostByID {
		hostKeys[id] = true
	}

	for _, node := range nodes {
		host := node.HostStep
		if host == nil {
			host = hostByID[node.ID]
		}
		phaseSource, hasPhase := phaseByNode[node.ID]
		if !hasPhase {
			if node.Phase != "" {
				phaseSource = node.Phase
			} else if host != nil {
				phaseSource = host.Label
			} else {
				phaseSource = node.Label
			}
		}
		phase := checklistKeyText(phaseSource, "Workflow")
		if host != nil {
			phases.add(phase, checklistHostItem(host, phase, node.ID))
			delete(hostByID, node.ID)
			continue
		}
		var matches []stepRef
		for _, m := range stepsByKey[node.ID] {
			if !used[m.index] {
				matches = append(matches, m)
			}
		}
		if len(matches) > 0 {
			for _, m := range matches {
				used[m.index] = true
				key := checklistStepKey(m.step)
				if key == "" {
					key = node.ID
				}
				phases.add(phase, checklistApplyNow(checklistMergeNodeStep(node, m.step, phase, traceByKey[key], checklistLane(in.Preflight, node.ID, phase)), in.Now))
			}
			continue
		}
		synthetic := WorkflowChecklistStep{Key: node.ID, Label: node.Label, Phase: phase, Agent: node.Agent, Status: node.Status, OutputName: node.OutputName, Error: node.Error, AcceptanceStatus: node.AcceptanceStatus}
		phases.add(phase, checklistApplyNow(checklistMergeNodeStep(node, synthetic, phase, traceByKey[node.ID], checklistLane(in.Preflight, node.ID, phase)), in.Now))
	}

	for i, step := range in.Steps {
		if used[i] {
			continue
		}
		key := checklistKeyText(checklistStepKey(step), "step-"+itoa(i+1))
		traceEntry := traceByKey[key]
		var traceLane, tracePhase string
		if traceEntry != nil {
			traceLane, tracePhase = traceEntry.GeneratedLaneKey, traceEntry.Phase
		}
		lane := checklistLane(in.Preflight, key, step.Phase, traceLane, tracePhase)
		phaseSource := ""
		for _, c := range []string{step.Phase, traceLane, tracePhase} {
			if c != "" {
				phaseSource = c
				break
			}
		}
		if lane != nil {
			phaseSource = lane.Key
		}
		phase := checklistKeyText(phaseSource, "Workflow")
		label := step.Label
		if label == "" {
			label = step.Key
		}
		if label == "" {
			label = step.Agent
		}
		phases.add(phase, checklistApplyNow(checklistStepItem(step, i, phase, key, label, lane), in.Now))
	}

	for _, id := range hostOrder {
		host, still := hostByID[id]
		if !still {
			continue
		}
		label := checklistKeyText(host.Label, "Host")
		phases.add(label, checklistHostItem(host, label, host.ID))
	}
	stepKeys := map[string]bool{}
	for k := range stepsByKey {
		stepKeys[k] = true
	}
	for _, e := range trace {
		if graphKeys[e.Key] || stepKeys[e.Key] || hostKeys[e.Key] {
			continue
		}
		item := checklistTraceItem(e, len(phases.order), checklistLane(in.Preflight, e.Key, e.GeneratedLaneKey, e.Phase))
		phases.add(item.Phase, item)
	}

	out := WorkflowChecklistProjection{Phases: []*WorkflowChecklistPhase{}}
	var all []*WorkflowChecklistItem
	for _, label := range phases.order {
		ph := phases.byKey[label]
		if len(ph.Items) == 0 {
			continue
		}
		for i, item := range ph.Items {
			ph.Items[i] = checklistApplyNow(item, in.Now)
		}
		checklistFinalize(ph)
		out.Phases = append(out.Phases, ph)
		all = append(all, ph.Items...)
	}
	count := func(state string) int {
		n := 0
		for _, i := range all {
			if i.State == state {
				n++
			}
		}
		return n
	}
	out.Total, out.Done, out.Running, out.Queued = len(all), count("complete"), count("running"), count("queued")
	out.Blocked, out.Failed, out.Paused, out.Stopped = count("blocked"), count("failed"), count("paused"), count("stopped")
	var best *WorkflowChecklistItem
	for _, i := range all {
		if best == nil || checklistPriority(i) < checklistPriority(best) {
			best = i
		}
	}
	if best != nil && best.State != "complete" {
		out.Bottleneck = best
	}
	return out
}

func checklistStateLabel(state string) string {
	if state == "running" {
		return "active"
	}
	return state
}

func checklistJoin(parts []string, sep string) string { return strings.Join(parts, sep) }

// formatWorkflowChecklistSummary is formatWorkflowChecklistSummary.
func formatWorkflowChecklistSummary(p WorkflowChecklistProjection) string {
	if p.Total == 0 {
		return ""
	}
	parts := []string{itoa(p.Done) + "/" + itoa(p.Total) + " done"}
	for _, c := range []struct {
		n    int
		name string
	}{{p.Running, "active"}, {p.Queued, "queued"}, {p.Blocked, "blocked"}, {p.Failed, "failed"}, {p.Paused, "paused"}, {p.Stopped, "stopped"}} {
		if c.n > 0 {
			parts = append(parts, itoa(c.n)+" "+c.name)
		}
	}
	return checklistJoin(parts, " · ")
}

// formatWorkflowChecklistPhase is formatWorkflowChecklistPhase.
func formatWorkflowChecklistPhase(ph *WorkflowChecklistPhase) string {
	var counts []string
	if ph.Total > 1 && ph.Done > 0 {
		counts = append(counts, itoa(ph.Done)+" done")
	}
	for _, c := range []struct {
		n    int
		name string
	}{{ph.Running, "active"}, {ph.Queued, "queued"}, {ph.Blocked, "blocked"}, {ph.Failed, "failed"}, {ph.Paused, "paused"}, {ph.Stopped, "stopped"}} {
		if c.n > 0 {
			counts = append(counts, itoa(c.n)+" "+c.name)
		}
	}
	if len(counts) == 0 {
		return ph.Label
	}
	return ph.Label + " " + checklistJoin(counts, " · ")
}

func checklistDurationText(ms float64) string {
	seconds := int(math.Max(0, jsRound(ms/1000)))
	if seconds < 60 {
		return itoa(seconds) + "s"
	}
	return itoa(seconds/60) + "m " + itoa(seconds%60) + "s"
}

var checklistOutputWordRe = regexp.MustCompile(`\bOutput:`)

func checklistIdentity(item *WorkflowChecklistItem) string {
	parts := []string{}
	if item.Label != "" {
		parts = append(parts, item.Label)
	}
	if item.Agent != "" && item.Agent != item.Label {
		parts = append(parts, item.Agent)
	}
	if id := checklistJoin(parts, " · "); id != "" {
		return id
	}
	return item.Key
}

func checklistFormatNumber(f float64) string { return jsNumber(f) }

// formatWorkflowChecklistBottleneck is formatWorkflowChecklistBottleneck; includeOutput and includeError default to true.
func formatWorkflowChecklistBottleneck(item *WorkflowChecklistItem, includeOutput, includeError bool) string {
	if item == nil {
		return ""
	}
	var details []string
	if item.Context != "" {
		details = append(details, "("+item.Context+")")
	}
	if item.CurrentTool != "" {
		d := item.CurrentTool
		if item.DurationMs != nil {
			d += " " + checklistDurationText(*item.DurationMs)
		}
		details = append(details, d)
	}
	if item.CurrentTool == "" && item.CurrentPath != "" {
		details = append(details, item.CurrentPath)
	}
	if item.CurrentTool == "" && item.DurationMs != nil {
		details = append(details, checklistDurationText(*item.DurationMs))
	}
	if item.ToolCount != nil {
		details = append(details, checklistFormatNumber(*item.ToolCount)+" tools")
	}
	if includeOutput && item.OutputName != "" {
		details = append(details, "out:"+item.OutputName)
	}
	if includeError && item.Error != "" {
		details = append(details, "error:"+checklistOutputWordRe.ReplaceAllString(item.Error, "output:"))
	}
	return checklistJoin(append([]string{checklistIdentity(item)}, details...), " · ")
}

func formatWorkflowChecklistItem(item *WorkflowChecklistItem) string {
	var details []string
	if item.Kind != "" && item.MonitorKind != "" {
		details = append(details, item.MonitorKind)
	}
	add := func(prefix, v string) {
		if v != "" {
			details = append(details, prefix+v)
		}
	}
	add("provider:", item.Provider)
	add("role:", item.Role)
	add("", item.Target)
	add("", item.CurrentTool)
	if item.CurrentTool == "" {
		add("", item.CurrentPath)
	}
	if item.DurationMs != nil {
		details = append(details, checklistDurationText(*item.DurationMs))
	}
	if item.ToolCount != nil {
		details = append(details, checklistFormatNumber(*item.ToolCount)+" tools")
	}
	add("out:", item.OutputName)
	add("out:", item.ReportPath)
	if item.Stale != nil && *item.Stale {
		details = append(details, "stale")
	}
	add("reason:", item.ReasonCode)
	if item.Error != "" {
		details = append(details, "error:"+checklistOutputWordRe.ReplaceAllString(item.Error, "output:"))
	}
	text := checklistIdentity(item)
	if item.Context != "" {
		text += " (" + item.Context + ")"
	}
	if item.State != "complete" {
		text += " · " + checklistStateLabel(item.State)
	}
	if len(details) > 0 {
		text += " · " + checklistJoin(details, " · ")
	}
	return text
}

func checklistMarker(state string) string {
	switch state {
	case "complete":
		return "✓"
	case "running":
		return "⠼"
	case "blocked":
		return "!"
	case "failed":
		return "✗"
	case "paused", "stopped":
		return "■"
	}
	return "◦"
}

// formatWorkflowChecklistText is formatWorkflowChecklistText; includeItems false leaves out the item rows.
func formatWorkflowChecklistText(p WorkflowChecklistProjection, indent string, includeItems bool) []string {
	if p.Total == 0 {
		return nil
	}
	lines := []string{indent + "Workflow checklist: " + formatWorkflowChecklistSummary(p)}
	for _, ph := range p.Phases {
		lines = append(lines, indent+"  "+checklistMarker(ph.State)+" "+formatWorkflowChecklistPhase(ph))
		if !includeItems {
			continue
		}
		for _, item := range ph.Items {
			lines = append(lines, indent+"    "+checklistMarker(item.State)+" "+formatWorkflowChecklistItem(item))
		}
	}
	if b := formatWorkflowChecklistBottleneck(p.Bottleneck, true, !includeItems); b != "" {
		lines = append(lines, indent+"  bottleneck · "+b)
	}
	return lines
}

// wfSanitizeDisplayText is sanitizeDisplayText (display-text.ts): whitespace, control characters and terminal escape sequences
// (CSI, OSC and the other control strings) collapse to single spaces; the ends are trimmed.
func wfSanitizeDisplayText(value string) string {
	var out strings.Builder
	pending := false
	space := func() {
		if out.Len() > 0 {
			pending = true
		}
	}
	consumeControlString := func(i int, osc bool) int {
		for i < len(value) {
			cp, w := utf8.DecodeRuneInString(value[i:])
			switch {
			case osc && cp == 0x07, cp == 0x9c:
				return i + w
			case cp == 0x1b && i+1 < len(value) && value[i+1] == 0x5c:
				return i + 2
			}
			i += w
		}
		return len(value)
	}
	consumeCsi := func(i int) int {
		for i < len(value) {
			cp, w := utf8.DecodeRuneInString(value[i:])
			if cp >= 0x40 && cp <= 0x7e {
				return i + w
			}
			i += w
		}
		return len(value)
	}
	for i := 0; i < len(value); {
		cp, w := utf8.DecodeRuneInString(value[i:])
		if cp == 0x1b {
			var next byte
			if i+1 < len(value) {
				next = value[i+1]
			}
			space()
			switch next {
			case 0x5b:
				i = consumeCsi(i + 2)
			case 0x5d, 0x50, 0x58, 0x5e, 0x5f:
				i = consumeControlString(i+2, next == 0x5d)
			default:
				if next != 0 {
					i += 2
				} else {
					i++
				}
			}
			continue
		}
		switch cp {
		case 0x9b:
			space()
			i = consumeCsi(i + w)
			continue
		case 0x90, 0x98, 0x9d, 0x9e, 0x9f:
			space()
			i = consumeControlString(i+w, cp == 0x9d)
			continue
		}
		if cp <= 0x20 || (cp >= 0x7f && cp <= 0x9f) || (cp >= 0xd800 && cp <= 0xdfff) || isJSSpace(cp) {
			space()
		} else {
			if pending {
				out.WriteByte(' ')
			}
			out.WriteRune(cp)
			pending = false
		}
		i += w
	}
	return out.String()
}
