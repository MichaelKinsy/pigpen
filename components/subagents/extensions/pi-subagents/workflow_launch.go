package pi_subagents

import (
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"
)

// Port of the launch-parameter half of src/runs/foreground/subagent-executor.ts (prepareWorkflowLaunchParams,
// resolveWorkflowChildLocalCwd, promptAuditRedoParams, sanitizeRunPathSegment, resolveRevivalControlConfig) and of the small
// helpers they call (normalizeGateAcceptance, normalizeExtensionBindings, intersectSubagentCapabilityCeilings,
// resolveControlConfig). Parameters are ordered JSON objects (*jsObject): the original merges them with object spread.

// lpGet returns the value of an own property that is not undefined.
func lpGet(o *jsObject, k string) (any, bool) {
	if o == nil {
		return nil, false
	}
	v, ok := o.vals[k]
	if !ok {
		return nil, false
	}
	if _, u := v.(undef); u {
		return nil, false
	}
	return v, true
}

// lpNullish is `value ?? fallback`'s test: undefined or null.
func lpNullish(o *jsObject, k string) bool {
	v, ok := lpGet(o, k)
	return !ok || v == nil
}

// lpFirst is `a ?? b ?? c ...` over properties: the first value of (object, key) pairs that is neither undefined nor null.
func lpFirst(pairs ...any) (any, bool) {
	for i := 0; i+1 < len(pairs); i += 2 {
		o, _ := pairs[i].(*jsObject)
		k := pairs[i+1].(string)
		if v, ok := lpGet(o, k); ok && v != nil {
			return v, true
		}
	}
	return nil, false
}

// lpSpread is `{ ...a, ...b }` for the objects given: a later object's property replaces an earlier one's and keeps its place.
func lpSpread(objs ...*jsObject) *jsObject {
	out := newObject()
	for _, o := range objs {
		if o == nil {
			continue
		}
		for _, k := range o.order() {
			out.set(k, o.vals[k])
		}
	}
	return out
}

// lpWithout is `{ a, b, ...rest }` destructuring: o without the named properties.
func lpWithout(o *jsObject, names ...string) *jsObject {
	out := newObject()
	if o == nil {
		return out
	}
	drop := map[string]bool{}
	for _, n := range names {
		drop[n] = true
	}
	for _, k := range o.order() {
		if !drop[k] {
			out.set(k, o.vals[k])
		}
	}
	return out
}

// WorkflowLaunchOptions are the options of prepareWorkflowLaunchParams.
type WorkflowLaunchOptions struct {
	MissionDetached               bool
	SuppressRoutineResultIntercom bool
	AwaitDetachedChild            bool
	// RunFanoutBudget is the descriptor of the run's fan-out budget; its parentPath grows by workflow[<key>].
	RunFanoutBudget *jsObject
	// ParentDeadlineAt (epoch ms) is the workflow's deadline; a child that sets no timeout of its own resolves its default from it.
	ParentDeadlineAt      *float64
	ExternalAsyncRequired bool
	CapabilityCeiling     *jsObject
	OutputClaimPath       string
}

// prepareWorkflowLaunchParams is prepareWorkflowLaunchParams (subagent-executor.ts:4911): the request a workflow child is
// launched with, from the workflow-level defaults and the child's own parameters (the child's win).
func prepareWorkflowLaunchParams(defaults, child *jsObject, parentWorkflowRunID, workflowKey string, opt WorkflowLaunchOptions) (*jsObject, error) {
	defaults = lpWithout(defaults, "globalConcurrencyLimit", "maxSubagentSpawnsPerRun")
	child = lpWithout(child, "globalConcurrencyLimit", "maxSubagentSpawnsPerRun")
	defaultsCeiling, _ := defaults.vals["capabilityCeiling"].(*jsObject)
	ceiling := intersectCapabilityCeilings(defaultsCeiling, opt.CapabilityCeiling)

	label := fmt.Sprintf("workflow child '%s'.lane", workflowKey)
	laneValue, laneOwn := child.vals["lane"]
	if !laneOwn {
		laneValue, _ = defaults.vals["lane"]
		if laneValue == nil && !defaults.has("lane") {
			laneValue = undef{}
		}
	}
	lane, err := NormalizeWorkflowLaneMetadata(laneValue, label)
	if err != nil {
		return nil, err
	}
	if err := AssertWorkflowLaneKey(lane, workflowKey, label); err != nil {
		return nil, err
	}
	hasParentDeadline := opt.ParentDeadlineAt != nil &&
		lpNullishUndefined(child, "timeoutMs") && lpNullishUndefined(child, "maxRuntimeMs") &&
		lpNullishUndefined(defaults, "timeoutMs") && lpNullishUndefined(defaults, "maxRuntimeMs")

	fanout := func() *jsObject {
		if opt.RunFanoutBudget == nil {
			return nil
		}
		b := opt.RunFanoutBudget.clone()
		prefix := ""
		if p, _ := b.vals["parentPath"].(string); p != "" {
			prefix = p + "/"
		}
		b.set("parentPath", prefix+"workflow["+workflowKey+"]")
		return b
	}

	if resume, ok := child.vals["resume"].(string); ok {
		return prepareRetainedResumeParams(defaults, child, resume, parentWorkflowRunID, workflowKey, opt, lane, ceiling, fanout())
	}

	control := mergeWorkflowControlOverrides(defaults.vals["control"], child.vals["control"])
	asyncOmitted := lpNullishUndefined(child, "async") && lpNullishUndefined(defaults, "async")
	launch := lpSpread(defaults)
	if opt.ExternalAsyncRequired && asyncOmitted {
		launch.set("async", true)
	}
	launch = lpSpread(launch, child)
	if control != nil {
		launch.set("control", control)
	}
	if asyncOmitted {
		launch.set("workflowAwaitAsync", true)
	}
	if opt.MissionDetached {
		launch.set("mission", false)
	}
	launch.set("workflowParentRunId", parentWorkflowRunID)
	launch.set("workflowKey", workflowKey)
	if opt.OutputClaimPath != "" {
		launch.set("workflowOutputClaimPath", opt.OutputClaimPath)
	}
	if opt.AwaitDetachedChild {
		launch.set("workflowAwaitDetached", true)
	}
	if lane != nil {
		launch.set("lane", lane)
	}
	if hasParentDeadline {
		launch.set("workflowParentDeadlineAt", *opt.ParentDeadlineAt)
	}
	if b := fanout(); b != nil {
		launch.set("runFanoutBudget", b)
	}
	if opt.SuppressRoutineResultIntercom {
		launch.set("suppressRoutineResultIntercom", true)
	}
	if ceiling != nil {
		launch.set("capabilityCeiling", ceiling)
	}
	if bindings, ok := lpGet(launch, "extensionBindings"); ok {
		normalized, err := normalizeWorkflowExtensionBindings(bindings)
		if err != nil {
			return nil, err
		}
		launch.set("extensionBindings", normalized)
	}
	return normalizeWorkflowGateParams(launch)
}

// lpNullishUndefined is `object.field === undefined` for the places the original compares with undefined: null counts as set.
func lpNullishUndefined(o *jsObject, k string) bool {
	_, ok := lpGet(o, k)
	return !ok
}

func prepareRetainedResumeParams(defaults, child *jsObject, resume, parentRunID, key string, opt WorkflowLaunchOptions, lane, ceiling, fanout *jsObject) (*jsObject, error) {
	if !lpNullishUndefined(child, "extensionBindings") || !lpNullishUndefined(defaults, "extensionBindings") {
		return nil, errors.New("extensionBindings is not supported with retained resume; resume uses the original retained child binding.")
	}
	if !lpNullishUndefined(child, "gate") || !lpNullishUndefined(defaults, "gate") {
		return nil, errors.New("gate is not supported with retained resume; resume uses the retained child contract.")
	}
	timeout, hasTimeout := lpFirst(child, "timeoutMs", child, "maxRuntimeMs", defaults, "timeoutMs", defaults, "maxRuntimeMs")
	toolBudget, hasBudget := lpFirst(child, "toolBudget", defaults, "toolBudget")
	bridge, hasBridge := lpFirst(child, "intercomBridge", defaults, "intercomBridge")
	worktree, hasWorktree := lpGet(child, "worktree")
	if !hasWorktree || worktree == nil {
		worktree, hasWorktree = lpGet(defaults, "worktree")
		if hasWorktree && worktree == nil {
			hasWorktree = false // `?? ` skipped null in both
		}
	}
	baseRef, hasBaseRef := lpOwnDefined(child, "baseRef", nil)
	outputSchema, hasSchema := lpOwnDefined(child, "outputSchema", defaults)
	if hasSchema && outputSchema != false {
		if o, ok := outputSchema.(*jsObject); !ok || o == nil {
			return nil, errors.New("outputSchema must be a JSON Schema object.")
		}
	}
	contract, hasContract := lpOwnDefined(child, "agentContract", defaults)
	if hasContract {
		if o, ok := contract.(*jsObject); !ok || o == nil || o.vals["version"] != float64(1) {
			return nil, errors.New("agentContract must be { version: 1 }.")
		}
	}
	acceptance, hasAcceptance := lpOwnDefined(child, "acceptance", defaults)
	output, hasOutput := lpOwnDefined(child, "output", defaults)
	if hasOutput {
		switch output.(type) {
		case string, bool:
		default:
			return nil, errors.New("output must be a path string or boolean.")
		}
	}
	outputMode, hasOutputMode := lpOwnDefined(child, "outputMode", defaults)
	if hasOutputMode && outputMode != "inline" && outputMode != "file-only" {
		return nil, errors.New("outputMode must be 'inline' or 'file-only'.")
	}
	control := mergeWorkflowControlOverrides(defaults.vals["control"], child.vals["control"])

	out := jo("action", "resume", "id", jsTrim(resume))
	if idx, ok := child.vals["index"].(float64); ok && idx == math.Trunc(idx) && !math.IsInf(idx, 0) {
		out.set("index", idx)
	}
	message := ""
	if t, ok := child.vals["task"].(string); ok {
		message = jsTrim(t)
	}
	out.set("message", message)
	out.set("workflowParentRunId", parentRunID)
	out.set("workflowKey", key)
	if opt.OutputClaimPath != "" {
		out.set("workflowOutputClaimPath", opt.OutputClaimPath)
	}
	if lane != nil {
		out.set("lane", lane)
	}
	if hasWorktree {
		out.set("worktree", worktree)
	}
	if hasBaseRef {
		out.set("baseRef", baseRef)
	}
	if hasSchema {
		out.set("outputSchema", outputSchema)
	}
	if hasContract {
		out.set("agentContract", contract)
	}
	if hasAcceptance {
		out.set("acceptance", acceptance)
	}
	if hasOutput {
		out.set("output", output)
	}
	if hasOutputMode {
		out.set("outputMode", outputMode)
	}
	if fanout != nil {
		out.set("runFanoutBudget", fanout)
	}
	if opt.MissionDetached {
		out.set("mission", false)
	}
	if hasTimeout {
		out.set("timeoutMs", timeout)
	}
	if hasBudget {
		out.set("toolBudget", toolBudget)
	}
	if control != nil {
		out.set("control", control)
	}
	if hasBridge {
		out.set("intercomBridge", bridge)
	}
	if ceiling != nil {
		out.set("capabilityCeiling", ceiling)
	}
	return out, nil
}

// lpOwnDefined is `Object.hasOwn(child, k) ? child[k] : defaults[k]`, reported only when the result is not undefined.
func lpOwnDefined(child *jsObject, k string, defaults *jsObject) (any, bool) {
	if v, own := child.vals[k]; own {
		if _, u := v.(undef); u {
			return nil, false
		}
		return v, true
	}
	if defaults == nil {
		return nil, false
	}
	return lpGet(defaults, k)
}

// mergeWorkflowControlOverrides is `{ ...workflowControl, ...childControl }` (a child's control wins by key); nil when neither is set.
func mergeWorkflowControlOverrides(workflowControl, childControl any) *jsObject {
	cc, childSet := childControl.(*jsObject)
	wc, workflowSet := workflowControl.(*jsObject)
	switch {
	case !childSet:
		return wc
	case !workflowSet:
		return cc
	}
	return lpSpread(wc, cc)
}

// ---- gate: normalizeGateParams / normalizeGateAcceptance (acceptance.ts) ----

func normalizeWorkflowGateParams(params *jsObject) (*jsObject, error) {
	gate, hasGate := lpGet(params, "gate")
	if hasGate {
		if action, _ := params.vals["action"].(string); action == "resume" {
			return nil, errors.New("gate is not supported with action='resume'; resume uses the retained child contract.")
		}
	}
	acceptanceValue, hasAcceptance := lpGet(params, "acceptance")
	normalized, err := normalizeGateAcceptance(gate, hasGate, acceptanceValue, hasAcceptance)
	if err != nil {
		return nil, err
	}
	if !hasGate {
		return params, nil
	}
	rest := lpWithout(params, "gate")
	if normalized != nil {
		rest.set("acceptance", normalized)
	}
	return rest, nil
}

// normalizeGateAcceptance: the acceptance policy a gate stands for, the acceptance itself normalized when there is no gate,
// nil when neither is set.
func normalizeGateAcceptance(gate any, hasGate bool, acceptance any, hasAcceptance bool) (any, error) {
	if !hasGate {
		if !hasAcceptance {
			return nil, nil
		}
		return normalizeAcceptanceValue(acceptance, "acceptance")
	}
	if msg := workflowGateShapeErrorGo(gate); msg != "" {
		return nil, errors.New(msg)
	}
	if hasAcceptance && acceptance != false {
		return nil, errors.New("gate cannot be combined with acceptance; use one gate command or acceptance.verify." + workflowGateAcceptanceConflictGo(gate, acceptance))
	}
	verify := jo("id", "gate")
	switch g := gate.(type) {
	case string:
		verify.set("command", jsTrim(g))
	case *jsObject:
		cmd, _ := g.vals["command"].(string)
		verify.set("command", jsTrim(cmd))
		if g.vals["output"] == "json" {
			verify.set("output", "json")
		}
		if sc, ok := lpGet(g, "schema"); ok {
			verify.set("schema", sc)
		}
		if to, ok := lpGet(g, "timeoutMs"); ok {
			verify.set("timeoutMs", to)
		}
	}
	return jo("level", "verified", "verify", []any{verify}), nil
}

// normalizeAcceptanceValue is normalizeAcceptanceValue (acceptance.ts:127): a JSON-encoded acceptance object string is decoded.
func normalizeAcceptanceValue(input any, pathLabel string) (any, error) {
	s, ok := input.(string)
	if !ok {
		return input, nil
	}
	trimmed := jsTrim(s)
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return input, nil
	}
	if msg := workflowJSONError(trimmed); msg != "" {
		return nil, fmt.Errorf("%s JSON string must encode a valid acceptance object: %s", pathLabel, msg)
	}
	parsed, err := parseJSON([]byte(trimmed))
	if err != nil {
		return nil, fmt.Errorf("%s JSON string must encode a valid acceptance object: %s", pathLabel, err.Error())
	}
	if _, isObj := parsed.(*jsObject); !isObj {
		kind := "an array"
		if parsed == nil {
			kind = "null"
		}
		return nil, fmt.Errorf("%s JSON string must encode an object; got %s.", pathLabel, kind)
	}
	return parsed, nil
}

// ---- extension bindings (extension-bindings.ts) ----

var (
	extensionBindingNamespace = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]{0,62})/[1-9][0-9]{0,8}$`)
)

const (
	maxExtensionBindingNamespaces = 16
	maxExtensionBindingsBytes     = 16 * 1024
	maxExtensionBindingsDepth     = 16
	maxExtensionBindingsProps     = 256
)

func canonicalBindingJSON(v any, path string, depth int, props *int) (any, error) {
	switch x := v.(type) {
	case nil, string, bool:
		return v, nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, fmt.Errorf("%s must contain only finite JSON numbers.", path)
		}
		return v, nil
	case []any:
		if depth > maxExtensionBindingsDepth {
			return nil, fmt.Errorf("%s exceeds the maximum nesting depth of %d.", path, maxExtensionBindingsDepth)
		}
		out := make([]any, len(x))
		for i, e := range x {
			c, err := canonicalBindingJSON(e, fmt.Sprintf("%s[%d]", path, i), depth+1, props)
			if err != nil {
				return nil, err
			}
			out[i] = c
		}
		return out, nil
	case *jsObject:
		if depth > maxExtensionBindingsDepth {
			return nil, fmt.Errorf("%s exceeds the maximum nesting depth of %d.", path, maxExtensionBindingsDepth)
		}
		keys := append([]string(nil), x.order()...)
		sort.Slice(keys, func(i, j int) bool { return jsLess(keys[i], keys[j]) })
		out := newObject()
		for _, k := range keys {
			*props++
			if *props > maxExtensionBindingsProps {
				return nil, fmt.Errorf("extensionBindings exceeds %d total properties.", maxExtensionBindingsProps)
			}
			c, err := canonicalBindingJSON(x.vals[k], path+"."+k, depth+1, props)
			if err != nil {
				return nil, err
			}
			out.set(k, c)
		}
		return out, nil
	}
	return nil, fmt.Errorf("%s must contain only plain JSON values.", path)
}

// jsLess is the default Array.prototype.sort order of strings: by UTF-16 code unit.
func jsLess(a, b string) bool {
	ua, ub := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			return ua[i] < ub[i]
		}
	}
	return len(ua) < len(ub)
}

// normalizeWorkflowExtensionBindings is normalizeExtensionBindings(...).value: the canonical (key-sorted) bindings.
func normalizeWorkflowExtensionBindings(input any) (*jsObject, error) {
	o, ok := input.(*jsObject)
	if !ok || o == nil {
		return nil, errors.New("extensionBindings must be a plain JSON object.")
	}
	keys := o.order()
	if len(keys) > maxExtensionBindingNamespaces {
		return nil, fmt.Errorf("extensionBindings supports at most %d namespaces.", maxExtensionBindingNamespaces)
	}
	for _, k := range keys {
		if !extensionBindingNamespace.MatchString(k) {
			return nil, fmt.Errorf("extensionBindings namespace '%s' must use a package-like name followed by '/<positive-version>'.", k)
		}
	}
	props := 0
	c, err := canonicalBindingJSON(o, "extensionBindings", 0, &props)
	if err != nil {
		return nil, err
	}
	canonical := c.(*jsObject)
	if n := len(marshalJSON(canonical, "")); n > maxExtensionBindingsBytes {
		return nil, fmt.Errorf("extensionBindings canonical JSON is %d bytes; maximum is %d.", n, maxExtensionBindingsBytes)
	}
	return canonical, nil
}

// ---- capability ceilings (capability-ceiling.ts) ----

// intersectCapabilityCeilings is intersectSubagentCapabilityCeilings: the narrowest ceiling the given ones allow together.
func intersectCapabilityCeilings(ceilings ...*jsObject) *jsObject {
	var active []*jsObject
	for _, c := range ceilings {
		if c != nil {
			active = append(active, c)
		}
	}
	if len(active) == 0 {
		return nil
	}
	intersect := func(field string) ([]any, bool) {
		var lists []map[string]bool
		var first []any
		for _, c := range active {
			arr, ok := c.vals[field].([]any)
			if !ok {
				continue
			}
			set := map[string]bool{}
			for _, e := range arr {
				if s, ok := e.(string); ok {
					set[s] = true
				}
			}
			if lists == nil {
				for k := range set {
					first = append(first, k)
				}
			}
			lists = append(lists, set)
		}
		if lists == nil {
			return nil, false
		}
		var out []string
		for k := range lists[0] {
			all := true
			for _, l := range lists {
				all = all && l[k]
			}
			if all {
				out = append(out, k)
			}
		}
		sort.Slice(out, func(i, j int) bool { return jsLess(out[i], out[j]) })
		res := make([]any, len(out))
		for i, s := range out {
			res[i] = s
		}
		return res, true
	}
	out := jo("version", float64(1))
	if t, ok := intersect("allowedTools"); ok {
		out.set("allowedTools", t)
	}
	if a, ok := intersect("allowedAgents"); ok {
		out.set("allowedAgents", a)
	}
	deny := false
	srcSet := map[string]bool{}
	for _, c := range active {
		if b, _ := c.vals["denyExtensions"].(bool); b {
			deny = true
		}
		if arr, ok := c.vals["sources"].([]any); ok {
			for _, e := range arr {
				if s, ok := e.(string); ok {
					srcSet[s] = true
				}
			}
		}
	}
	out.set("denyExtensions", deny)
	sources := make([]string, 0, len(srcSet))
	for s := range srcSet {
		sources = append(sources, s)
	}
	sort.Slice(sources, func(i, j int) bool { return jsLess(sources[i], sources[j]) })
	arr := make([]any, len(sources))
	for i, s := range sources {
		arr[i] = s
	}
	out.set("sources", arr)
	return out
}

// ---- the rest ----

// resolveWorkflowChildLocalCwd is resolveWorkflowChildLocalCwd (subagent-executor.ts:3743): the local directory a workflow child
// is discovered and routed from. A child on a remote machine never uses its (remote) cwd locally.
func resolveWorkflowChildLocalCwd(workflowCwd string, discoverAgents func(cwd string, scope AgentScope) []AgentConfig, agents []AgentConfig, workflowAgentScope any, params *jsObject) string {
	if params.has("machine") {
		return workflowCwd
	}
	if name, ok := params.vals["agent"].(string); ok {
		scopeValue := params.vals["agentScope"]
		if scopeValue == nil || !params.has("agentScope") {
			scopeValue = workflowAgentScope
		}
		scope := ResolveExecutionAgentScope(scopeValue)
		agent, _ := ResolveAgentName(name, discoverAgents(workflowCwd, scope))
		if agent == nil {
			agent, _ = ResolveAgentName(name, agents)
		}
		if agent != nil && agent.Machine != "" {
			return workflowCwd
		}
	}
	if c, ok := params.vals["cwd"].(string); ok && c != "" {
		if filepath.IsAbs(c) {
			return c
		}
		return filepath.Join(workflowCwd, c)
	}
	return workflowCwd
}

// promptAuditRedoParams is promptAuditRedoParams (subagent-executor.ts:590): the live launch request with the workflow
// detach bridge scrubbed, rerun in the foreground with a rewritten task.
func promptAuditRedoParams(value any, rewrittenTask string) (*jsObject, error) {
	o, ok := value.(*jsObject)
	if !ok || o == nil {
		return nil, errors.New("Prompt redo is missing a safe live launch contract.")
	}
	params := o.clone()
	params.set("task", rewrittenTask)
	params.set("async", false)
	out := lpWithout(params, "workflowParentRunId", "workflowKey", "workflowChildAsyncId", "workflowAwaitDetached", "workflowParentDeadlineAt", "suppressRoutineResultIntercom")
	if params.vals["worktree"] == true {
		if _, isList := params.vals["tasks"].([]any); isList {
			out = lpWithout(out, "cwd")
		}
	}
	return out, nil
}

var runPathSegmentUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// sanitizeRunPathSegment is sanitizeRunPathSegment (subagent-executor.ts:3646): a run id as a safe path segment.
func sanitizeRunPathSegment(value string, maxBytes int) string {
	s := runPathSegmentUnsafe.ReplaceAllString(jsTrim(value), "_")
	s = strings.Trim(s, "_")
	if s == "" {
		return "unknown"
	}
	if len(s) <= maxBytes {
		return s
	}
	s = strings.TrimRight(s[:maxBytes], "_")
	if s == "" {
		return "unknown"
	}
	return s
}

// ---- resolveControlConfig (subagent-control.ts) ----

// wfControlConfig is ControlConfig and ResolvedControlConfig of the original: a pointer field is an unset option.
type wfControlConfig struct {
	Enabled                           *bool
	NeedsAttentionAfterMs             *float64
	NeedsAttentionAfterMsIsExplicit   *bool
	ActiveNoticeAfterMs               *float64
	ActiveNoticeAfterTurns            *float64
	ActiveNoticeAfterTokens           *float64
	FailedToolAttemptsBeforeAttention *float64
	NotifyOn                          *[]string
	NotifyChannels                    *[]string
}

func positiveInt(v *float64) *float64 {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) || *v != math.Trunc(*v) || *v < 1 {
		return nil
	}
	return v
}

func parseControlList(v *[]string, allowed ...string) *[]string {
	if v == nil {
		return nil
	}
	if len(*v) == 0 {
		return &[]string{}
	}
	var out []string
	seen := map[string]bool{}
	for _, e := range *v {
		for _, a := range allowed {
			if e == a && !seen[e] {
				seen[e] = true
				out = append(out, e)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return &out
}

func firstFloat(vs ...*float64) *float64 {
	for _, v := range vs {
		if v != nil {
			return v
		}
	}
	return nil
}

// wfResolveControlConfig is resolveControlConfig: an override beats the global config, which beats the defaults.
func wfResolveControlConfig(global, override *wfControlConfig) wfControlConfig {
	if global == nil {
		global = &wfControlConfig{}
	}
	if override == nil {
		override = &wfControlConfig{}
	}
	def := func(p *float64, d float64) *float64 {
		if p != nil {
			return p
		}
		return &d
	}
	events := []string{"active_long_running", "needs_attention"}
	channels := []string{"event", "async", "intercom"}
	enabled := true
	if override.Enabled != nil {
		enabled = *override.Enabled
	} else if global.Enabled != nil {
		enabled = *global.Enabled
	}
	overrideNeeds := positiveInt(override.NeedsAttentionAfterMs)
	globalNeeds := positiveInt(global.NeedsAttentionAfterMs)
	globalExplicit := globalNeeds != nil
	if global.NeedsAttentionAfterMsIsExplicit != nil {
		globalExplicit = *global.NeedsAttentionAfterMsIsExplicit
	}
	explicit := overrideNeeds != nil || globalExplicit
	notifyOn := parseControlList(override.NotifyOn, events...)
	if notifyOn == nil {
		notifyOn = parseControlList(global.NotifyOn, events...)
	}
	if notifyOn == nil {
		notifyOn = &events
	}
	notifyChannels := parseControlList(override.NotifyChannels, channels...)
	if notifyChannels == nil {
		notifyChannels = parseControlList(global.NotifyChannels, channels...)
	}
	if notifyChannels == nil {
		notifyChannels = &channels
	}
	on := append([]string{}, *notifyOn...)
	ch := append([]string{}, *notifyChannels...)
	return wfControlConfig{
		Enabled:                           &enabled,
		NeedsAttentionAfterMs:             def(firstFloat(overrideNeeds, globalNeeds), 60000),
		NeedsAttentionAfterMsIsExplicit:   &explicit,
		ActiveNoticeAfterMs:               def(firstFloat(positiveInt(override.ActiveNoticeAfterMs), positiveInt(global.ActiveNoticeAfterMs)), 240000),
		ActiveNoticeAfterTurns:            firstFloat(positiveInt(override.ActiveNoticeAfterTurns), positiveInt(global.ActiveNoticeAfterTurns)),
		ActiveNoticeAfterTokens:           firstFloat(positiveInt(override.ActiveNoticeAfterTokens), positiveInt(global.ActiveNoticeAfterTokens)),
		FailedToolAttemptsBeforeAttention: def(firstFloat(positiveInt(override.FailedToolAttemptsBeforeAttention), positiveInt(global.FailedToolAttemptsBeforeAttention)), 3),
		NotifyOn:                          &on,
		NotifyChannels:                    &ch,
	}
}

// wfResolveRevivalControlConfig is resolveRevivalControlConfig: a revived child keeps the control config it was recovered with, and a request amends it.
func wfResolveRevivalControlConfig(global, requested, recovered *wfControlConfig) wfControlConfig {
	if requested == nil {
		if recovered != nil {
			return *recovered
		}
		return wfResolveControlConfig(global, nil)
	}
	base := global
	if recovered != nil {
		base = recovered
	}
	return wfResolveControlConfig(base, requested)
}
