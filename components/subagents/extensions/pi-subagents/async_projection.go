package pi_subagents

// The bounded public snapshot of the async jobs a session tracks (what the widget publishes to an RPC host). Ports
// runs/shared/async-status-projection.ts (projectAsyncStatusSnapshot), runs/background/async-status-snapshot.ts and the pieces
// they use: shared/display-text.ts (sanitizeDisplayText, truncateDisplayText), runs/shared/host-step-status.ts (the persisted
// host-step contract) and workflowGraphStageNodes of runs/shared/workflow-graph.ts.
//
// Jobs and their parts are JSON-shaped maps, as the original reads them: persisted status is untrusted, so every field is
// checked by type and a malformed one is dropped or widened to "partial", never trusted.

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

const (
	asyncStatusSnapshotKind         = "pi-subagents.async-status-snapshot"
	asyncStatusSnapshotVersion      = 1
	asyncStatusSnapshotWidgetPrefix = "PI_SUBAGENT_ASYNC_JSON:"

	snapshotDefaultMaxRuns            = 20
	snapshotDefaultMaxChildrenPerNode = 8
	snapshotDefaultMaxDepth           = 3
	snapshotDefaultMaxStringLength    = 160
	snapshotDefaultMaxSerializedBytes = 32 * 1024
)

type jm = map[string]any

// SnapshotActivity is AsyncStatusSnapshotActivity.
type SnapshotActivity struct {
	State                *string `json:"state,omitempty"`
	CurrentTool          string  `json:"currentTool,omitempty"`
	LastActivityAt       *int64  `json:"lastActivityAt,omitempty"`
	CurrentToolStartedAt *int64  `json:"currentToolStartedAt,omitempty"`
	TurnCount            *int64  `json:"turnCount,omitempty"`
	ToolCount            *int64  `json:"toolCount,omitempty"`
}

// SnapshotHostStep is AsyncStatusSnapshotHostStep.
type SnapshotHostStep struct {
	Kind       string `json:"kind"`
	Provider   string `json:"provider,omitempty"`
	Role       string `json:"role,omitempty"`
	State      string `json:"state"`
	Verdict    string `json:"verdict,omitempty"`
	ReasonCode string `json:"reasonCode,omitempty"`
	Detail     string `json:"detail,omitempty"`
	Target     string `json:"target,omitempty"`
	Stale      *bool  `json:"stale,omitempty"`
	Report     string `json:"report,omitempty"`
}

// SnapshotNode is AsyncStatusSnapshotNode.
type SnapshotNode struct {
	ID        string            `json:"id"`
	Kind      string            `json:"kind"`
	Label     string            `json:"label"`
	State     string            `json:"state"`
	StartedAt *int64            `json:"startedAt,omitempty"`
	UpdatedAt *int64            `json:"updatedAt,omitempty"`
	EndedAt   *int64            `json:"endedAt,omitempty"`
	Activity  *SnapshotActivity `json:"activity,omitempty"`
	HostStep  *SnapshotHostStep `json:"hostStep,omitempty"`
	Children  []*SnapshotNode   `json:"children,omitempty"`
}

// SnapshotCaps is AsyncStatusSnapshotCaps.
type SnapshotCaps struct {
	MaxRuns            int `json:"maxRuns"`
	MaxChildrenPerNode int `json:"maxChildrenPerNode"`
	MaxDepth           int `json:"maxDepth"`
	MaxStringLength    int `json:"maxStringLength"`
	MaxSerializedBytes int `json:"maxSerializedBytes"`
}

// SnapshotOmitted is AsyncStatusSnapshotOmitted.
type SnapshotOmitted struct {
	Runs              int  `json:"runs"`
	Children          int  `json:"children"`
	ByteLimitExceeded bool `json:"byteLimitExceeded"`
}

// AsyncStatusSnapshot is the published snapshot.
type AsyncStatusSnapshot struct {
	Kind        string          `json:"kind"`
	Version     int             `json:"version"`
	GeneratedAt int64           `json:"generatedAt"`
	Caps        SnapshotCaps    `json:"caps"`
	Omitted     SnapshotOmitted `json:"omitted"`
	Runs        []*SnapshotNode `json:"runs"`
}

// SnapshotOptions is AsyncStatusSnapshotOptions; a nil field takes its default.
type SnapshotOptions struct {
	GeneratedAt        *int64
	MaxRuns            *float64
	MaxChildrenPerNode *float64
	MaxDepth           *float64
	MaxStringLength    *float64
	MaxSerializedBytes *float64
}

type projectionContext struct {
	caps    SnapshotCaps
	omitted *SnapshotOmitted
}

// ── display-text.ts ──

// ── host-step-status.ts ──

const (
	hostStepMaxIDChars         = 128
	hostStepMaxLabelChars      = 80
	hostStepMaxRoleChars       = 32
	hostStepMaxProviderChars   = 64
	hostStepMaxReasonChars     = 64
	hostStepMaxDetailChars     = 200
	hostStepMaxTargetChars     = 128
	hostStepMaxRefChars        = 128
	hostStepMaxReportPathChars = 240
	hostStepMaxCount           = 32
)

var hostStepFields = map[string]bool{"version": true, "kind": true, "monitorKind": true, "id": true, "label": true, "role": true, "provider": true, "state": true, "verdict": true,
	"reasonCode": true, "detail": true, "target": true, "freshness": true, "reportPath": true, "exitCode": true, "updatedAt": true, "deadlineAt": true}

var freshnessFields = map[string]bool{"expectedRef": true, "observedRef": true, "stale": true}

func safeIntegerOf(v any) (int64, bool) {
	f, ok := v.(float64)
	if !ok || f != math.Trunc(f) || math.Abs(f) > 9007199254740991 {
		return 0, false
	}
	return int64(f), true
}

func sortedUnknown(m jm, allowed map[string]bool) []string {
	var out []string
	for _, k := range sortedKeys(m) {
		if !allowed[k] {
			out = append(out, k)
		}
	}
	return out
}

func assertBoundedString(m jm, key, field string, maxChars int, source string, required bool) error {
	v, has := m[key]
	if !has && !required {
		return nil
	}
	s, ok := v.(string)
	if !ok || jsTrim(s) == "" {
		return fmt.Errorf("Invalid host step '%s': %s must be a non-empty string.", source, field)
	}
	if len(jsUTF16(s)) > maxChars {
		return fmt.Errorf("Invalid host step '%s': %s exceeds %d characters.", source, field, maxChars)
	}
	if strings.ContainsAny(s, "\r\n") {
		return fmt.Errorf("Invalid host step '%s': %s must be single-line.", source, field)
	}
	return nil
}

func assertTimestamp(m jm, key, source string, required bool) error {
	v, has := m[key]
	if !has && !required {
		return nil
	}
	if n, ok := safeIntegerOf(v); !ok || n < 0 {
		return fmt.Errorf("Invalid host step '%s': %s must be a non-negative safe integer.", source, key)
	}
	return nil
}

// hostStepReportName is the last path segment of a report path.
func hostStepReportName(reportPath any) string {
	s, _ := reportPath.(string)
	last := ""
	for _, part := range splitAny(s, `\/`) {
		if part != "" {
			last = part
		}
	}
	return last
}

func splitAny(s, seps string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return strings.ContainsRune(seps, r) })
}

func assertHostStepNode(value any, source string) error {
	m, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("Invalid host step '%s': expected an object.", source)
	}
	if unknown := sortedUnknown(m, hostStepFields); len(unknown) > 0 {
		return fmt.Errorf("Invalid host step '%s': unsupported fields: %s.", source, strings.Join(unknown, ", "))
	}
	if v, _ := m["version"].(float64); v != 1 {
		return fmt.Errorf("Invalid host step '%s': version must be 1.", source)
	}
	if m["kind"] != "host-step" {
		return fmt.Errorf("Invalid host step '%s': kind must be 'host-step'.", source)
	}
	mk := m["monitorKind"]
	if mk != "command" && mk != "ci" && mk != "gate" {
		return fmt.Errorf("Invalid host step '%s': monitorKind must be 'command', 'ci', or 'gate'.", source)
	}
	for _, c := range []struct {
		key      string
		max      int
		required bool
	}{{"id", hostStepMaxIDChars, true}, {"label", hostStepMaxLabelChars, true}, {"role", hostStepMaxRoleChars, false}, {"provider", hostStepMaxProviderChars, false}} {
		if err := assertBoundedString(m, c.key, c.key, c.max, source, c.required); err != nil {
			return err
		}
	}
	state := m["state"]
	if state != "pending" && state != "running" && state != "done" && state != "cancelled" && state != "error" {
		return fmt.Errorf("Invalid host step '%s': state is invalid.", source)
	}
	verdict, hasVerdict := m["verdict"]
	if hasVerdict && verdict != "pass" && verdict != "fail" && verdict != "inconclusive" {
		return fmt.Errorf("Invalid host step '%s': verdict is invalid.", source)
	}
	if state != "done" && hasVerdict {
		return fmt.Errorf("Invalid host step '%s': verdict is only valid for done state.", source)
	}
	for _, c := range []struct {
		key string
		max int
	}{{"reasonCode", hostStepMaxReasonChars}, {"detail", hostStepMaxDetailChars}, {"target", hostStepMaxTargetChars}} {
		if err := assertBoundedString(m, c.key, c.key, c.max, source, false); err != nil {
			return err
		}
	}
	stale := false
	if f, has := m["freshness"]; has {
		fm, ok := f.(map[string]any)
		if !ok {
			return fmt.Errorf("Invalid host step '%s': freshness must be an object.", source)
		}
		if unknown := sortedUnknown(fm, freshnessFields); len(unknown) > 0 {
			return fmt.Errorf("Invalid host step '%s': freshness has unsupported fields: %s.", source, strings.Join(unknown, ", "))
		}
		if err := assertBoundedString(fm, "expectedRef", "freshness.expectedRef", hostStepMaxRefChars, source, true); err != nil {
			return err
		}
		if err := assertBoundedString(fm, "observedRef", "freshness.observedRef", hostStepMaxRefChars, source, false); err != nil {
			return err
		}
		if sv, has := fm["stale"]; has {
			b, ok := sv.(bool)
			if !ok {
				return fmt.Errorf("Invalid host step '%s': freshness.stale must be a boolean.", source)
			}
			stale = b
		}
	}
	if stale && (state != "done" || verdict != "inconclusive") {
		return fmt.Errorf("Invalid host step '%s': stale freshness requires done/inconclusive state.", source)
	}
	if err := assertBoundedString(m, "reportPath", "reportPath", hostStepMaxReportPathChars, source, false); err != nil {
		return err
	}
	if _, has := m["reportPath"]; has {
		if name := hostStepReportName(m["reportPath"]); name == "" || name == "." || name == ".." {
			return fmt.Errorf("Invalid host step '%s': reportPath must name a report.", source)
		}
	}
	if ec, has := m["exitCode"]; has {
		if ec != nil {
			if n, ok := safeIntegerOf(ec); !ok || n < 0 {
				return fmt.Errorf("Invalid host step '%s': exitCode must be a non-negative safe integer or null.", source)
			}
		}
		if mk != "command" {
			return fmt.Errorf("Invalid host step '%s': exitCode is only valid for command steps.", source)
		}
		if state == "pending" || state == "running" {
			return fmt.Errorf("Invalid host step '%s': exitCode is only valid after command settlement.", source)
		}
	}
	if err := assertTimestamp(m, "updatedAt", source, true); err != nil {
		return err
	}
	return assertTimestamp(m, "deadlineAt", source, false)
}

// validHostStepNodes returns only the valid host nodes of a graph, so an untrusted in-memory projection fails closed.
func validHostStepNodes(graph jm) []jm {
	nodes, _ := graph["nodes"].([]any)
	counts := map[string]int{}
	for _, n := range nodes {
		if nm, ok := n.(map[string]any); ok {
			counts[fmt.Sprint(nm["id"])]++
		}
	}
	var out []jm
	for i, n := range nodes {
		nm, ok := n.(map[string]any)
		if !ok || nm["kind"] != "host-step" {
			continue
		}
		if assertHostStepNode(nm["hostStep"], fmt.Sprintf("workflowGraph.nodes[%d].hostStep", i)) != nil {
			continue
		}
		hs := nm["hostStep"].(map[string]any)
		if nm["id"] != hs["id"] || nm["label"] != hs["label"] || counts[fmt.Sprint(hs["id"])] != 1 {
			continue
		}
		out = append(out, hs)
	}
	if len(out) > hostStepMaxCount {
		out = out[:hostStepMaxCount]
	}
	return out
}

func validHostStepList(source any) []jm {
	if g, ok := source.(map[string]any); ok {
		if _, has := g["nodes"]; has {
			return validHostStepNodes(g)
		}
		return nil
	}
	arr, _ := source.([]any)
	var out []jm
	for i, v := range arr {
		if i >= hostStepMaxCount {
			break
		}
		if assertHostStepNode(v, fmt.Sprintf("hostSteps[%d]", i)) == nil {
			out = append(out, v.(map[string]any))
		}
	}
	return out
}

// workflowGraphStageNodes is the displayable stages of a workflow graph: structural parallel groups are replaced by their
// children and host monitors are left out.
func workflowGraphStageNodes(graph any) []jm {
	g, _ := graph.(map[string]any)
	nodes, _ := g["nodes"].([]any)
	var stages []jm
	var visit func(n any)
	visit = func(n any) {
		nm, ok := n.(map[string]any)
		if !ok {
			return
		}
		if k := nm["kind"]; k == "parallel-group" || k == "dynamic-parallel-group" {
			children, _ := nm["children"].([]any)
			for _, c := range children {
				visit(c)
			}
			return
		}
		if nm["kind"] != "host-step" {
			stages = append(stages, nm)
		}
	}
	for _, n := range nodes {
		visit(n)
	}
	return stages
}

// ── async-status-projection.ts ──

func floorCap(v *float64, def float64, min float64) int {
	f := def
	if v != nil {
		f = *v
	}
	return int(math.Max(min, math.Floor(f)))
}

func resolveSnapshotCaps(o SnapshotOptions) SnapshotCaps {
	return SnapshotCaps{
		MaxRuns: floorCap(o.MaxRuns, snapshotDefaultMaxRuns, 0), MaxChildrenPerNode: floorCap(o.MaxChildrenPerNode, snapshotDefaultMaxChildrenPerNode, 0),
		MaxDepth: floorCap(o.MaxDepth, snapshotDefaultMaxDepth, 0), MaxStringLength: floorCap(o.MaxStringLength, snapshotDefaultMaxStringLength, 0),
		MaxSerializedBytes: floorCap(o.MaxSerializedBytes, snapshotDefaultMaxSerializedBytes, 256),
	}
}

func publicText(value any, fallback string, maxLength int) string {
	s, ok := value.(string)
	if !ok {
		return fallback
	}
	if n := max(0, maxLength*4); jsLen(s) > n {
		s = string(utf16Slice(s, n))
	}
	normalized := sanitizeDisplayText(s)
	if normalized == "" {
		normalized = fallback
	}
	return truncateDisplayText(normalized, maxLength)
}

func publicOptionalText(value any, maxLength int) string {
	s, ok := value.(string)
	if !ok {
		return ""
	}
	if n := max(0, maxLength*4); jsLen(s) > n {
		s = string(utf16Slice(s, n))
	}
	normalized := sanitizeDisplayText(s)
	if normalized == "" {
		return ""
	}
	return truncateDisplayText(normalized, maxLength)
}

func publicTime(v any) *int64 {
	if n, ok := safeIntegerOf(v); ok && n >= 0 {
		return &n
	}
	return nil
}

func normalizeSnapshotState(value string) string {
	switch value {
	case "completed":
		return "complete"
	case "pending":
		return "queued"
	case "queued", "running", "complete", "failed", "partial", "paused", "stopped", "rejected":
		return value
	}
	return "partial"
}

func snapshotTerminal(state string) bool {
	switch state {
	case "complete", "failed", "partial", "paused", "stopped", "rejected":
		return true
	}
	return false
}

func kindForMode(mode any) string {
	if mode == "workflow" {
		return "workflow"
	}
	return "subagent"
}

func labelForAgents(agents any, fallback string, maxLength int) string {
	arr, _ := agents.([]any)
	if len(arr) == 0 {
		return publicText(fallback, fallback, maxLength)
	}
	var visible []string
	for i, a := range arr {
		if i >= 3 {
			break
		}
		visible = append(visible, publicText(a, "agent", maxLength))
	}
	suffix := ""
	if len(arr) > 3 {
		suffix = fmt.Sprintf(", +%d more", len(arr)-3)
	}
	return truncateDisplayText(strings.Join(visible, ", ")+suffix, maxLength)
}

func activityFor(source jm, ctx *projectionContext) *SnapshotActivity {
	a := &SnapshotActivity{CurrentTool: publicOptionalText(source["currentTool"], ctx.caps.MaxStringLength)}
	empty := a.CurrentTool == ""
	if s, ok := source["activityState"].(string); ok {
		t := publicText(s, "unknown", ctx.caps.MaxStringLength)
		a.State, empty = &t, false
	}
	a.LastActivityAt, a.CurrentToolStartedAt, a.TurnCount, a.ToolCount = publicTime(source["lastActivityAt"]), publicTime(source["currentToolStartedAt"]), publicTime(source["turnCount"]), publicTime(source["toolCount"])
	if a.LastActivityAt != nil || a.CurrentToolStartedAt != nil || a.TurnCount != nil || a.ToolCount != nil {
		empty = false
	}
	if empty {
		return nil
	}
	return a
}

func appendBoundedChildren(children []*SnapshotNode, source []*SnapshotNode, ctx *projectionContext) []*SnapshotNode {
	remaining := max(0, ctx.caps.MaxChildrenPerNode-len(children))
	children = append(children, source[:min(remaining, len(source))]...)
	ctx.omitted.Children += max(0, len(source)-remaining)
	return children
}

func truthyText(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok && s != ""
}

func stepIdentity(step jm, index int, ctx *projectionContext) (id, label string) {
	idv := any(fmt.Sprintf("step:%d", index))
	if s, ok := truthyText(step["workflowKey"]); ok {
		idv = s
	} else if s, ok := truthyText(step["runId"]); ok {
		idv = s
	}
	labelv := step["agent"]
	if s, ok := truthyText(step["label"]); ok {
		labelv = s
	}
	return publicText(idv, fmt.Sprintf("step:%d", index), ctx.caps.MaxStringLength), publicText(labelv, "step", ctx.caps.MaxStringLength)
}

func mapList(v any) []jm {
	arr, _ := v.([]any)
	var out []jm
	for _, e := range arr {
		if m, ok := e.(map[string]any); ok {
			out = append(out, m)
		} else {
			out = append(out, jm{})
		}
	}
	return out
}

func strOf(v any) string { s, _ := v.(string); return s }

func projectStep(step jm, index, depth int, ctx *projectionContext) *SnapshotNode {
	state := normalizeSnapshotState(strOf(step["status"]))
	startedAt := publicTime(step["startedAt"])
	durationMs := publicTime(step["durationMs"])
	endedAt := publicTime(step["endedAt"])
	if endedAt == nil && snapshotTerminal(state) && startedAt != nil && durationMs != nil {
		if sum := float64(*startedAt) + float64(*durationMs); sum <= 9007199254740991 {
			v := *startedAt + *durationMs
			endedAt = &v
		}
	}
	updatedAt := endedAt
	if updatedAt == nil {
		updatedAt = publicTime(step["lastActivityAt"])
	}
	if updatedAt == nil {
		updatedAt = startedAt
	}
	id, label := stepIdentity(step, index, ctx)
	node := &SnapshotNode{ID: id, Kind: "step", Label: label, State: state, StartedAt: startedAt, UpdatedAt: updatedAt, Activity: activityFor(step, ctx)}
	if snapshotTerminal(state) {
		node.EndedAt = endedAt
	}
	children := mapList(step["children"])
	if depth < ctx.caps.MaxDepth && len(children) > 0 {
		var nested []*SnapshotNode
		for i, c := range children {
			nested = append(nested, projectNestedRun(c, i, depth+1, ctx))
		}
		node.Children = appendBoundedChildren(nil, nested, ctx)
	} else if len(children) > 0 {
		ctx.omitted.Children += len(children)
	}
	return node
}

func projectNestedRun(child jm, index, depth int, ctx *projectionContext) *SnapshotNode {
	state := normalizeSnapshotState(strOf(child["state"]))
	startedAt, endedAt := publicTime(child["startedAt"]), publicTime(child["endedAt"])
	updatedAt := publicTime(child["lastUpdate"])
	if updatedAt == nil {
		updatedAt = endedAt
	}
	if updatedAt == nil {
		updatedAt = publicTime(child["lastActivityAt"])
	}
	if updatedAt == nil {
		updatedAt = startedAt
	}
	label := ""
	if s, ok := truthyText(child["agent"]); ok {
		label = publicText(s, "subagent", ctx.caps.MaxStringLength)
	} else {
		fb := "subagent"
		if m, ok := truthyText(child["mode"]); ok {
			fb = m
		}
		label = labelForAgents(child["agents"], fb, ctx.caps.MaxStringLength)
	}
	node := &SnapshotNode{ID: publicText(child["id"], fmt.Sprintf("nested:%d", index), ctx.caps.MaxStringLength), Kind: kindForMode(child["mode"]), Label: label, State: state,
		StartedAt: startedAt, UpdatedAt: updatedAt, Activity: activityFor(child, ctx)}
	if snapshotTerminal(state) {
		node.EndedAt = endedAt
	}
	steps, children := mapList(child["steps"]), mapList(child["children"])
	if depth < ctx.caps.MaxDepth {
		var all []*SnapshotNode
		for i, s := range steps {
			all = append(all, projectStep(s, i, depth+1, ctx))
		}
		for i, c := range children {
			all = append(all, projectNestedRun(c, i, depth+1, ctx))
		}
		node.Children = appendBoundedChildren(nil, all, ctx)
	} else {
		ctx.omitted.Children += len(steps) + len(children)
	}
	return node
}

func hostStepSnapshotState(state, verdict string) string {
	switch {
	case state == "pending":
		return "queued"
	case state == "running":
		return "running"
	case state == "cancelled":
		return "stopped"
	case state == "error" || verdict == "fail":
		return "failed"
	case verdict == "" || verdict == "inconclusive":
		return "partial"
	}
	return "complete"
}

func projectHostStep(hs jm, ctx *projectionContext) *SnapshotNode {
	verdict := strOf(hs["verdict"])
	state := hostStepSnapshotState(strOf(hs["state"]), verdict)
	meta := &SnapshotHostStep{Kind: strOf(hs["monitorKind"]), State: strOf(hs["state"]), Verdict: verdict, Detail: publicOptionalText(hs["detail"], ctx.caps.MaxStringLength),
		Report: publicOptionalText(hostStepReportName(hs["reportPath"]), ctx.caps.MaxStringLength)}
	n := ctx.caps.MaxStringLength
	if s, ok := truthyText(hs["provider"]); ok {
		meta.Provider = publicText(s, "provider", n)
	}
	if s, ok := truthyText(hs["role"]); ok {
		meta.Role = publicText(s, "role", n)
	}
	if s, ok := truthyText(hs["reasonCode"]); ok {
		meta.ReasonCode = publicText(s, "reason", n)
	}
	if s, ok := truthyText(hs["target"]); ok {
		meta.Target = publicText(s, "target", n)
	}
	if f, ok := hs["freshness"].(map[string]any); ok {
		if b, ok := f["stale"].(bool); ok {
			meta.Stale = &b
		}
	}
	updated, _ := safeIntegerOf(hs["updatedAt"])
	node := &SnapshotNode{ID: publicText(hs["id"], "host-step", n), Kind: "host-step", Label: publicText(hs["label"], "host step", n), State: state, UpdatedAt: &updated, HostStep: meta}
	if snapshotTerminal(state) {
		node.EndedAt = &updated
	}
	return node
}

type projJob struct{ m jm }

func (j *projJob) str(k string) string { return strOf(j.m[k]) }

func jobRank(status string) int {
	switch status {
	case "running":
		return 0
	case "queued":
		return 1
	}
	return 2
}

func jobTime(j *projJob) float64 {
	for _, k := range []string{"updatedAt", "startedAt"} {
		if f, ok := j.m[k].(float64); ok {
			return f
		}
	}
	return 0
}

// asyncJobOrder sorts running before queued before the rest, newest first, then by id.
func asyncJobLess(l, r *projJob) bool {
	lr, rr := jobRank(l.str("status")), jobRank(r.str("status"))
	if lr != rr {
		return lr < rr
	}
	if lt, rt := jobTime(l), jobTime(r); lt != rt {
		return lt > rt
	}
	return localeLess(l.str("asyncId"), r.str("asyncId"))
}

func hasCyclicWorkflowParent(job *projJob, parents map[string]*projJob) bool {
	seen := map[string]bool{job.str("asyncId"): true}
	parentID := job.str("parentWorkflowRunId")
	for parentID != "" {
		if seen[parentID] {
			return true
		}
		seen[parentID] = true
		if p := parents[parentID]; p != nil {
			parentID = p.str("parentWorkflowRunId")
		} else {
			parentID = ""
		}
	}
	return false
}

type materialized struct {
	childrenByParent map[string][]*projJob
	liveRoots        map[*projJob]bool
}

func groupMaterializedChildren(jobs []*projJob) (roots []*projJob, m materialized) {
	parents := map[string]*projJob{}
	for _, j := range jobs {
		if j.m["mode"] == "workflow" {
			parents[j.str("asyncId")] = j
		}
	}
	m = materialized{childrenByParent: map[string][]*projJob{}, liveRoots: map[*projJob]bool{}}
	for _, j := range jobs {
		parent := parents[j.str("parentWorkflowRunId")]
		if j.str("parentWorkflowRunId") == "" {
			parent = nil
		}
		status := j.str("status")
		keepLiveRoot := (status == "running" || status == "queued") && (parent == nil || parent.str("status") != "running")
		if parent != nil && !hasCyclicWorkflowParent(j, parents) {
			id := parent.str("asyncId")
			m.childrenByParent[id] = append(m.childrenByParent[id], j)
			if keepLiveRoot {
				m.liveRoots[j] = true
				roots = append(roots, j)
			}
		} else {
			roots = append(roots, j)
		}
	}
	for _, c := range m.childrenByParent {
		sort.SliceStable(c, func(a, b int) bool { return asyncJobLess(c[a], c[b]) })
	}
	return roots, m
}

type lane struct {
	step  jm
	index int
}

// assignMaterializedChildren pairs each lane with the child job that runs it: by exact run id first, then by workflow key.
func assignMaterializedChildren(lanes []lane, children []*projJob) map[int]*projJob {
	assigned := map[int]*projJob{}
	claimed := map[*projJob]bool{}
	pick := func(match func(c *projJob) bool) *projJob {
		for _, c := range children {
			if !claimed[c] && match(c) {
				return c
			}
		}
		return nil
	}
	for i, l := range lanes {
		runID, ok := truthyText(l.step["runId"])
		if !ok {
			continue
		}
		if c := pick(func(c *projJob) bool { return c.str("asyncId") == runID }); c != nil {
			assigned[i], claimed[c] = c, true
		}
	}
	for i, l := range lanes {
		if _, done := assigned[i]; done {
			continue
		}
		key, ok := truthyText(l.step["workflowKey"])
		if !ok {
			continue
		}
		if c := pick(func(c *projJob) bool { return c.str("workflowKey") == key }); c != nil {
			assigned[i], claimed[c] = c, true
		}
	}
	return assigned
}

func workflowGraphStepStatus(status string) string {
	switch status {
	case "completed":
		return "complete"
	case "detached":
		return "paused"
	}
	return status
}

func projectMaterializedStep(step jm, index int, child *projJob, depth int, ctx *projectionContext, m materialized) *SnapshotNode {
	id, label := stepIdentity(step, index, ctx)
	var selfStep jm
	if mode := child.m["mode"]; mode == nil || mode == "single" {
		if steps := mapList(child.m["steps"]); len(steps) > 0 {
			selfStep = steps[0]
		}
	}
	live := projectRun(child, ctx, depth, m, selfStep != nil)
	laneActivity := activityFor(step, ctx)
	var selfActivity *SnapshotActivity
	if selfStep != nil {
		selfActivity = activityFor(selfStep, ctx)
	}
	node := *live
	node.ID, node.Kind, node.Label = id, "step", label
	if laneActivity != nil || selfActivity != nil || live.Activity != nil {
		merged := SnapshotActivity{}
		for _, a := range []*SnapshotActivity{laneActivity, selfActivity, live.Activity} {
			if a == nil {
				continue
			}
			if a.State != nil {
				merged.State = a.State
			}
			if a.CurrentTool != "" {
				merged.CurrentTool = a.CurrentTool
			}
			if a.LastActivityAt != nil {
				merged.LastActivityAt = a.LastActivityAt
			}
			if a.CurrentToolStartedAt != nil {
				merged.CurrentToolStartedAt = a.CurrentToolStartedAt
			}
			if a.TurnCount != nil {
				merged.TurnCount = a.TurnCount
			}
			if a.ToolCount != nil {
				merged.ToolCount = a.ToolCount
			}
		}
		node.Activity = &merged
	}
	return &node
}

func projectRun(job *projJob, ctx *projectionContext, depth int, m materialized, omitSteps bool) *SnapshotNode {
	state := normalizeSnapshotState(job.str("status"))
	startedAt := publicTime(job.m["startedAt"])
	updatedAt := publicTime(job.m["updatedAt"])
	if updatedAt == nil {
		updatedAt = startedAt
	}
	mode := "subagent"
	if s, ok := truthyText(job.m["mode"]); ok {
		mode = s
	}
	node := &SnapshotNode{ID: publicText(job.m["asyncId"], "async", ctx.caps.MaxStringLength), Kind: kindForMode(job.m["mode"]), Label: labelForAgents(job.m["agents"], mode, ctx.caps.MaxStringLength),
		State: state, StartedAt: startedAt, UpdatedAt: updatedAt, Activity: activityFor(job.m, ctx)}
	if snapshotTerminal(state) {
		node.EndedAt = updatedAt
	}
	var steps []jm
	if !omitSteps {
		steps = mapList(job.m["steps"])
	}
	mat := m.childrenByParent[job.str("asyncId")]
	var nestedSummaries []jm
	for _, c := range mapList(job.m["nestedChildren"]) {
		live := false
		for _, l := range mat {
			if l.str("asyncId") == strOf(c["id"]) {
				live = true
			}
		}
		if !live {
			nestedSummaries = append(nestedSummaries, c)
		}
	}
	loadedKeys := map[string]bool{}
	for _, s := range steps {
		if k, ok := truthyText(s["workflowKey"]); ok {
			loadedKeys[k] = true
		}
	}
	var lanes []lane
	for i, s := range steps {
		idx := i
		if n, ok := safeIntegerOf(s["index"]); ok {
			idx = int(n)
		}
		lanes = append(lanes, lane{s, idx})
	}
	if job.m["mode"] == "workflow" {
		for i, n := range workflowGraphStageNodes(job.m["workflowGraph"]) {
			if loadedKeys[strOf(n["id"])] {
				continue
			}
			agent := n["agent"]
			if agent == nil {
				agent = n["label"]
			}
			idx := i
			if f, ok := safeIntegerOf(n["flatIndex"]); ok {
				idx = int(f)
			}
			lanes = append(lanes, lane{jm{"agent": agent, "status": workflowGraphStepStatus(strOf(n["status"])), "workflowKey": n["id"], "label": n["label"]}, idx})
		}
	}
	assigned := assignMaterializedChildren(lanes, mat)
	claimed := map[*projJob]bool{}
	for _, c := range assigned {
		claimed[c] = true
	}
	unclaimed := func() []*projJob {
		var out []*projJob
		for _, c := range mat {
			if !claimed[c] && !m.liveRoots[c] {
				out = append(out, c)
			}
		}
		return out
	}
	hostSteps := validHostStepList(job.m["hostSteps"])
	if depth < ctx.caps.MaxDepth {
		childDepth := depth + 1
		projectLane := func(i int) *SnapshotNode {
			l := lanes[i]
			child := assigned[i]
			if child == nil {
				return projectStep(l.step, l.index, childDepth, ctx)
			}
			if m.liveRoots[child] {
				return nil
			}
			return projectMaterializedStep(l.step, l.index, child, childDepth, ctx, m)
		}
		var ordinary []*SnapshotNode
		for i := range lanes {
			if n := projectLane(i); n != nil {
				ordinary = append(ordinary, n)
			}
		}
		for i, c := range nestedSummaries {
			ordinary = append(ordinary, projectNestedRun(c, i, childDepth, ctx))
		}
		for _, c := range unclaimed() {
			ordinary = append(ordinary, projectRun(c, ctx, childDepth, m, false))
		}
		var hostChildren []*SnapshotNode
		for _, h := range hostSteps {
			hostChildren = append(hostChildren, projectHostStep(h, ctx))
		}
		retainedHost := hostChildren[:min(ctx.caps.MaxChildrenPerNode, len(hostChildren))]
		retainedOrdinary := ordinary[:max(0, min(len(ordinary), ctx.caps.MaxChildrenPerNode-len(retainedHost)))]
		bounded := append(append([]*SnapshotNode{}, retainedOrdinary...), retainedHost...)
		ctx.omitted.Children += len(ordinary) - len(retainedOrdinary) + len(hostChildren) - len(retainedHost)
		if len(bounded) > 0 {
			node.Children = bounded
		}
	} else {
		rootAssignments := 0
		for _, c := range assigned {
			if m.liveRoots[c] {
				rootAssignments++
			}
		}
		ctx.omitted.Children += len(lanes) - rootAssignments + len(nestedSummaries) + len(unclaimed()) + len(hostSteps)
	}
	return node
}

func snapshotBytes(s *AsyncStatusSnapshot) int {
	b, _ := json.Marshal(s)
	return len(b)
}

func enforceByteLimit(s *AsyncStatusSnapshot) {
	if snapshotBytes(s) <= s.Caps.MaxSerializedBytes {
		return
	}
	s.Omitted.ByteLimitExceeded = true
	runs, initialOmitted := s.Runs, s.Omitted.Runs
	lower, upper := 0, max(0, len(runs)-1)
	for lower < upper {
		retained := (lower + upper + 1) / 2
		s.Runs, s.Omitted.Runs = runs[:retained], initialOmitted+len(runs)-retained
		if snapshotBytes(s) <= s.Caps.MaxSerializedBytes {
			lower = retained
		} else {
			upper = retained - 1
		}
	}
	s.Runs, s.Omitted.Runs = runs[:lower], initialOmitted+len(runs)-lower
}

// projectAsyncStatusSnapshot projects already-loaded async job facts into the bounded public snapshot shape.
func projectAsyncStatusSnapshot(jobs []jm, o SnapshotOptions, now func() int64) *AsyncStatusSnapshot {
	caps := resolveSnapshotCaps(o)
	ctx := &projectionContext{caps: caps, omitted: &SnapshotOmitted{}}
	var all []*projJob
	for _, j := range jobs {
		all = append(all, &projJob{j})
	}
	roots, m := groupMaterializedChildren(all)
	sort.SliceStable(roots, func(a, b int) bool { return asyncJobLess(roots[a], roots[b]) })
	ctx.omitted.Runs += max(0, len(roots)-caps.MaxRuns)
	generated := now()
	if o.GeneratedAt != nil {
		generated = *o.GeneratedAt
	}
	snap := &AsyncStatusSnapshot{Kind: asyncStatusSnapshotKind, Version: asyncStatusSnapshotVersion, GeneratedAt: generated, Caps: caps, Omitted: *ctx.omitted, Runs: []*SnapshotNode{}}
	ctx.omitted = &snap.Omitted
	for _, j := range roots[:min(caps.MaxRuns, len(roots))] {
		snap.Runs = append(snap.Runs, projectRun(j, ctx, 0, m, false))
	}
	enforceByteLimit(snap)
	return snap
}

// ── async-status-snapshot.ts ──

// asyncStatusSnapshotJobsForState is the session's tracked jobs plus the retained fleet jobs, de-duplicated by id.
func asyncStatusSnapshotJobsForState(state *SubagentState, sessionID string) []*AsyncJobState {
	if state == nil || sessionID == "" || state.CurrentSessionID != sessionID {
		return nil
	}
	seen := map[string]bool{}
	var out []*AsyncJobState
	for _, id := range sortedKeys(state.AsyncJobs) {
		if j := state.AsyncJobs[id]; j.SessionID == sessionID {
			seen[j.AsyncID] = true
			out = append(out, j)
		}
	}
	for _, id := range sortedKeys(state.FleetJobs) {
		if j := state.FleetJobs[id]; j.SessionID == sessionID && !seen[j.AsyncID] {
			out = append(out, j)
		}
	}
	return out
}

// jobProjection is a tracked job as the projection reads it.
func jobProjection(j *AsyncJobState) jm {
	m := jm{"asyncId": j.AsyncID, "status": j.Status}
	if j.Mode != "" {
		m["mode"] = j.Mode
	}
	if j.Agents != nil {
		agents := make([]any, len(j.Agents))
		for i, a := range j.Agents {
			agents[i] = a
		}
		m["agents"] = agents
	}
	num := func(k string, v *ms) {
		if v != nil {
			m[k] = float64(*v)
		}
	}
	count := func(k string, v *int) {
		if v != nil {
			m[k] = float64(*v)
		}
	}
	m["startedAt"], m["updatedAt"] = float64(j.StartedAt), float64(j.UpdatedAt)
	if j.ActivityState != "" {
		m["activityState"] = j.ActivityState
	}
	if j.CurrentTool != "" {
		m["currentTool"] = j.CurrentTool
	}
	num("lastActivityAt", j.LastActivityAt)
	num("currentToolStartedAt", j.CurrentToolAt)
	count("turnCount", j.TurnCount)
	count("toolCount", j.ToolCount)
	var steps []any
	for _, s := range j.Steps {
		b, _ := json.Marshal(s.AsyncStep)
		var sm jm
		_ = json.Unmarshal(b, &sm)
		steps = append(steps, sm)
	}
	if steps != nil {
		m["steps"] = steps
	}
	return m
}

func projectionJobs(jobs []*AsyncJobState) []jm {
	out := make([]jm, len(jobs))
	for i, j := range jobs {
		out[i] = jobProjection(j)
	}
	return out
}

// encodeAsyncStatusSnapshotWidget is the widget payload an RPC host reads: one line, the prefix and the snapshot as JSON.
func encodeAsyncStatusSnapshotWidget(jobs []*AsyncJobState, o SnapshotOptions) []string {
	b, _ := json.Marshal(projectAsyncStatusSnapshot(projectionJobs(jobs), o, nowMs))
	return []string{asyncStatusSnapshotWidgetPrefix + string(b)}
}
