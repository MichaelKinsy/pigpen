package pi_subagents

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The prompt-template delegation bridge (src/slash/prompt-template-bridge.ts, delegation-request.ts, delegation-adapters.ts and
// src/api/delegation.ts): another extension asks, over the shared event bus, for one structured subagent run and receives
// started, update and response events. A legacy direct payload is rejected as in the original.

const (
	delegationRequestEvent  = "prompt-template:subagent:request"
	delegationStartedEvent  = "prompt-template:subagent:started"
	delegationUpdateEvent   = "prompt-template:subagent:update"
	delegationResponseEvent = "prompt-template:subagent:response"
	delegationCancelEvent   = "prompt-template:subagent:cancel"

	maxDelegationSchemaBytes  = 64 * 1024
	maxDelegationTaskBytes    = 1024 * 1024
	maxDelegationCwdBytes     = 32 * 1024
	maxDelegationShortBytes   = 1024
	maxDelegationSkillEntries = 256
	maxDelegationSkillBytes   = 64 * 1024
	maxDelegationResultBytes  = 1024 * 1024
	maxDelegationJSONDepth    = 64
	maxDelegationJSONEntries  = 100_000
)

var delegationSupportedFields = map[string]bool{"requestId": true, "ownerRunId": true, "nodeId": true, "agent": true, "task": true, "context": true, "cwd": true,
	"model": true, "thinking": true, "timeoutMs": true, "toolBudget": true, "skill": true, "artifacts": true, "intercomBridge": true, "result": true}

var delegationThinkingLevels = map[string]bool{"off": true, "minimal": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true}

func trimmedNonEmpty(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok && jsTrim(s) != ""
}

var newlineRe = regexp.MustCompile(`[\r\n]`)

func validDelegationID(v any) (string, bool) {
	s, ok := trimmedNonEmpty(v)
	if !ok || jsLen(s) > 256 || newlineRe.MatchString(s) {
		return "", false
	}
	return s, true
}

// cloneJSONWithinByteLimit copies plain JSON data within a depth, entry and encoded-size bound ("invalid" or "too_large" on failure).
func cloneJSONWithinByteLimit(input any, maxBytes int) (value any, encodedBytes int, reason string) {
	entries := 0
	var visit func(v any, depth int) (any, string)
	visit = func(v any, depth int) (any, string) {
		if depth > maxDelegationJSONDepth {
			return nil, "invalid"
		}
		switch x := v.(type) {
		case nil, bool, string:
			return x, ""
		case float64:
			if math.IsNaN(x) || math.IsInf(x, 0) {
				return nil, "invalid"
			}
			return x, ""
		case []any:
			out := make([]any, 0, len(x))
			for _, e := range x {
				entries++
				if entries > maxDelegationJSONEntries {
					return nil, "invalid"
				}
				c, r := visit(e, depth+1)
				if r != "" {
					return nil, r
				}
				out = append(out, c)
			}
			return out, ""
		case *jsObject:
			out := newObject()
			for _, k := range x.order() {
				entries++
				if entries > maxDelegationJSONEntries {
					return nil, "invalid"
				}
				c, r := visit(x.vals[k], depth+1)
				if r != "" {
					return nil, r
				}
				out.set(k, c)
			}
			return out, ""
		}
		return nil, "invalid"
	}
	out, r := visit(input, 0)
	if r != "" {
		return nil, 0, r
	}
	n := len(marshalJSON(out, ""))
	if n > maxBytes {
		return nil, 0, "too_large"
	}
	return out, n, ""
}

// validateIntercomBridgeConfig validates untrusted bridge config; label prefixes each error.
func validateIntercomBridgeConfig(value any, label string) (*jsObject, string) {
	b, ok := value.(*jsObject)
	if !ok {
		return nil, label + " must be an object."
	}
	for _, f := range b.order() {
		if f != "mode" && f != "instructionFile" && f != "resultDelivery" {
			return nil, fmt.Sprintf("%s.%s is not supported.", label, f)
		}
	}
	if m, has := b.vals["mode"]; has && b.has("mode") && m != "off" && m != "fork-only" && m != "always" {
		return nil, label + ".mode is invalid."
	}
	if v, has := b.vals["instructionFile"]; has && b.has("instructionFile") {
		if _, isStr := v.(string); !isStr {
			return nil, label + ".instructionFile must be a string."
		}
	}
	if v, has := b.vals["resultDelivery"]; has && b.has("resultDelivery") {
		if _, isBool := v.(bool); !isBool {
			return nil, label + ".resultDelivery must be a boolean."
		}
	}
	out := newObject()
	for _, k := range []string{"mode", "instructionFile", "resultDelivery"} {
		if b.has(k) {
			out.set(k, b.vals[k])
		}
	}
	return out, ""
}

// delegationParseFailure is a malformed request, correlated by the valid identity fields it supplied.
type delegationParseFailure struct{ RequestID, OwnerRunID, NodeID, Error string }

// parseSubagentDelegationRequest validates a structured request; the returned object is a copy.
func parseSubagentDelegationRequest(data any) (*jsObject, *delegationParseFailure) {
	value, ok := data.(*jsObject)
	if !ok {
		return nil, &delegationParseFailure{Error: "Delegation request must be an object."}
	}
	requestID, ok := validDelegationID(value.vals["requestId"])
	if !ok {
		return nil, &delegationParseFailure{Error: "Delegation requestId must be a non-empty string of at most 256 characters without newlines."}
	}
	ownerRunID, ok := validDelegationID(value.vals["ownerRunId"])
	if !ok {
		return nil, &delegationParseFailure{RequestID: requestID, Error: "Delegation ownerRunId must be a non-empty string of at most 256 characters without newlines."}
	}
	nodeID, ok := validDelegationID(value.vals["nodeId"])
	if !ok {
		return nil, &delegationParseFailure{RequestID: requestID, OwnerRunID: ownerRunID, Error: "Delegation nodeId must be a non-empty string of at most 256 characters without newlines."}
	}
	fail := func(msg string) (*jsObject, *delegationParseFailure) {
		return nil, &delegationParseFailure{requestID, ownerRunID, nodeID, msg}
	}
	for _, k := range value.order() {
		if !delegationSupportedFields[k] {
			return fail(fmt.Sprintf("Unsupported delegation field: %s.", k))
		}
	}
	if _, ok := trimmedNonEmpty(value.vals["agent"]); !ok {
		return fail("Delegation agent must be a non-empty string.")
	}
	if _, ok := trimmedNonEmpty(value.vals["task"]); !ok {
		return fail("Delegation task must be a non-empty string.")
	}
	if c := value.vals["context"]; c != "fresh" && c != "fork" {
		return fail("Delegation context must be fresh or fork.")
	}
	if _, ok := trimmedNonEmpty(value.vals["cwd"]); !ok {
		return fail("Delegation cwd must be a non-empty string.")
	}
	if value.has("model") {
		if _, ok := trimmedNonEmpty(value.vals["model"]); !ok {
			return fail("model must be a non-empty string when provided.")
		}
	}
	if value.has("timeoutMs") {
		f, isNum := value.vals["timeoutMs"].(float64)
		if !isNum || f != math.Trunc(f) || f < 1 {
			return fail("timeoutMs must be an integer >= 1.")
		}
		if f > 2147483647 {
			return fail("timeoutMs must be <= 2147483647.")
		}
	}
	if tb, ok := value.vals["toolBudget"].(*jsObject); ok {
		for _, k := range tb.order() {
			if k != "soft" && k != "hard" && k != "block" {
				return fail(fmt.Sprintf("toolBudget.%s is not supported.", k))
			}
		}
	}
	var tbRaw any = undef{}
	if value.has("toolBudget") {
		tbRaw = value.vals["toolBudget"]
	}
	if _, msg := validateToolBudgetConfig(tbRaw, "toolBudget", 0); msg != "" {
		return fail(msg)
	}
	if value.has("skill") {
		valid := false
		switch s := value.vals["skill"].(type) {
		case bool:
			valid = true
		case string:
			valid = jsTrim(s) != ""
		case []any:
			valid = len(s) > 0
			for _, e := range s {
				if _, ok := trimmedNonEmpty(e); !ok {
					valid = false
				}
			}
		}
		if !valid {
			return fail("skill must be a boolean, non-empty string, or non-empty string array.")
		}
	}
	if value.has("artifacts") {
		if _, ok := value.vals["artifacts"].(bool); !ok {
			return fail("artifacts must be a boolean.")
		}
	}
	var bridge *jsObject
	if value.has("intercomBridge") {
		b, msg := validateIntercomBridgeConfig(value.vals["intercomBridge"], "intercomBridge")
		if msg != "" {
			return fail(msg)
		}
		if f, ok := b.str("instructionFile"); ok && len(f) > maxDelegationShortBytes {
			return fail("intercomBridge.instructionFile exceeds 1 KiB when UTF-8 encoded.")
		}
		bridge = b
	}
	if len(value.vals["task"].(string)) > maxDelegationTaskBytes {
		return fail("Delegation task exceeds 1 MiB when UTF-8 encoded.")
	}
	if len(value.vals["cwd"].(string)) > maxDelegationCwdBytes {
		return fail("Delegation cwd exceeds 32 KiB when UTF-8 encoded.")
	}
	if len(value.vals["agent"].(string)) > maxDelegationShortBytes {
		return fail("Delegation agent exceeds 1 KiB when UTF-8 encoded.")
	}
	if m, ok := value.vals["model"].(string); ok && len(m) > maxDelegationShortBytes {
		return fail("Delegation model exceeds 1 KiB when UTF-8 encoded.")
	}
	var skills []string
	switch s := value.vals["skill"].(type) {
	case string:
		skills = []string{s}
	case []any:
		for _, e := range s {
			skills = append(skills, e.(string))
		}
	}
	if len(skills) > maxDelegationSkillEntries {
		return fail("Delegation skill supports at most 256 entries.")
	}
	total := 0
	for _, e := range skills {
		if len(e) > maxDelegationShortBytes {
			return fail("Delegation skill entry exceeds 1 KiB when UTF-8 encoded.")
		}
		total += len(e)
	}
	if total > maxDelegationSkillBytes {
		return fail("Delegation skill entries exceed 64 KiB in aggregate when UTF-8 encoded.")
	}
	if value.has("thinking") {
		if s, ok := value.vals["thinking"].(string); !ok || !delegationThinkingLevels[s] {
			return fail("thinking must be one of off, minimal, low, medium, high, xhigh, or max.")
		}
	}
	result, ok := value.vals["result"].(*jsObject)
	if !ok {
		return fail(`result must be { kind: "text" } or { kind: "structured", schema: object }.`)
	}
	var schema any
	switch result.vals["kind"] {
	case "text":
		for _, k := range result.order() {
			if k != "kind" {
				return fail(fmt.Sprintf("result.%s is not supported for text results.", k))
			}
		}
	case "structured":
		for _, k := range result.order() {
			if k != "kind" && k != "schema" {
				return fail(fmt.Sprintf("result.%s is not supported for structured results.", k))
			}
		}
		if _, ok := result.vals["schema"].(*jsObject); !ok {
			return fail("result.schema must be a JSON Schema object.")
		}
		clone, _, reason := cloneJSONWithinByteLimit(result.vals["schema"], maxDelegationSchemaBytes)
		if reason != "" {
			if reason == "too_large" {
				return fail("result.schema exceeds 64 KiB when encoded.")
			}
			return fail("result.schema must be plain JSON data.")
		}
		schema = clone
	default:
		return fail("result.kind must be text or structured.")
	}
	req := value.clone()
	if bridge != nil {
		req.set("intercomBridge", bridge)
	}
	if schema != nil {
		req.set("result", obj("kind", "structured", "schema", schema))
	} else {
		req.set("result", obj("kind", "text"))
	}
	return req, nil
}

// delegationExecParams is DelegatedSubagentExecutionParams: the structured request as the executor takes it.
func delegationExecParams(req *jsObject) *jsObject {
	p := newObject()
	for _, k := range []string{"agent", "task", "context", "cwd", "model", "timeoutMs", "toolBudget", "skill"} {
		if req.has(k) {
			p.set(k, req.vals[k])
		}
	}
	if r := req.obj("result"); r != nil && r.vals["kind"] == "structured" {
		p.set("outputSchema", r.vals["schema"])
	}
	p.set("acceptance", false)
	for _, k := range []string{"artifacts", "intercomBridge"} {
		if req.has(k) {
			p.set(k, req.vals[k])
		}
	}
	if req.has("thinking") {
		p.set("delegatedThinkingOverride", req.vals["thinking"])
	}
	p.set("delegatedAllowZeroToolBudget", true)
	p.set("async", false)
	p.set("foregroundOnly", true)
	p.set("clarify", false)
	return p
}

// --- updates ---

type recentTool struct{ Tool, Args string }

type updateUsage struct{ Input, Output, CacheRead, CacheWrite, Turns float64 }

// delegationUpdate is SubagentDelegationUpdate without its identity; nil pointers are unset.
type delegationUpdate struct {
	RunID, CurrentTool, CurrentToolArgs, RecentOutput, Model string
	RecentOutputLines                                        []string
	RecentTools                                              []recentTool
	ToolCount, DurationMs, Tokens                            *float64
	Usage                                                    *updateUsage
}

func filterRecentOutput(lines []any) []string {
	var out []string
	for _, l := range lines {
		if s, ok := l.(string); ok && jsTrim(s) != "" && jsTrim(s) != "(running...)" {
			out = append(out, s)
		}
	}
	return out
}

func sanitizeRecentTools(tools []any) []recentTool {
	var out []recentTool
	for _, t := range tools {
		o, ok := t.(*jsObject)
		if !ok {
			continue
		}
		name, ok := o.str("tool")
		if !ok || jsTrim(name) == "" {
			continue
		}
		args := ""
		if a, ok := o.vals["args"]; ok && a != nil {
			if s, isStr := a.(string); isStr {
				args = s
			} else {
				args = fmt.Sprint(a)
			}
		}
		out = append(out, recentTool{name, args})
	}
	return out
}

func resolveProgressModel(update *jsObject, entry *jsObject) string {
	details := update.obj("details")
	if details == nil {
		return ""
	}
	results, _ := details.vals["results"].([]any)
	if len(results) == 0 {
		return ""
	}
	modelOf := func(v any) string {
		if o, ok := v.(*jsObject); ok {
			s, _ := o.str("model")
			return s
		}
		return ""
	}
	if idx, ok := entry.num("index"); ok && idx >= 0 && int(idx) < len(results) {
		if m := modelOf(results[int(idx)]); m != "" {
			return m
		}
	}
	if agent, _ := entry.str("agent"); agent != "" {
		for _, r := range results {
			if o, ok := r.(*jsObject); ok {
				if a, _ := o.str("agent"); a == agent && modelOf(r) != "" {
					return modelOf(r)
				}
			}
		}
	}
	for _, r := range results {
		if m := modelOf(r); m != "" {
			return m
		}
	}
	return ""
}

func finiteNonNegative(o *jsObject, k string) (float64, bool) {
	f, ok := o.num(k)
	return f, ok && !math.IsNaN(f) && !math.IsInf(f, 0) && f >= 0
}

func numPtr(o *jsObject, k string) *float64 {
	if f, ok := o.num(k); ok {
		return &f
	}
	return nil
}

// toDelegationUpdate projects an executor update (nil when it carries no progress).
func toDelegationUpdate(update *jsObject) *delegationUpdate {
	details := update.obj("details")
	if details == nil {
		return nil
	}
	progressList, _ := details.vals["progress"].([]any)
	if len(progressList) == 0 {
		return nil
	}
	progress, ok := progressList[0].(*jsObject)
	if !ok {
		return nil
	}
	u := &delegationUpdate{}
	u.RunID, _ = details.str("runId")
	u.CurrentTool, _ = progress.str("currentTool")
	u.CurrentToolArgs, _ = progress.str("currentToolArgs")
	recent, _ := progress.vals["recentOutput"].([]any)
	if len(recent) > 0 {
		if last, ok := recent[len(recent)-1].(string); ok && jsTrim(last) != "" && last != "(running...)" {
			u.RecentOutput = last
		}
	}
	u.RecentOutputLines = filterRecentOutput(recent)
	tools, _ := progress.vals["recentTools"].([]any)
	u.RecentTools = sanitizeRecentTools(tools)
	u.Model = resolveProgressModel(update, progress)
	u.ToolCount, u.DurationMs, u.Tokens = numPtr(progress, "toolCount"), numPtr(progress, "durationMs"), numPtr(progress, "tokens")
	in, ok1 := finiteNonNegative(progress, "inputTokens")
	out, ok2 := finiteNonNegative(progress, "outputTokens")
	cr, ok3 := finiteNonNegative(progress, "cacheRead")
	cw, ok4 := finiteNonNegative(progress, "cacheWrite")
	turns, ok5 := finiteNonNegative(progress, "turnCount")
	if ok1 && ok2 && ok3 && ok4 && ok5 {
		u.Usage = &updateUsage{in, out, cr, cw, turns}
	}
	return u
}

// subagentDelegationUpdate is the update event payload of a structured request.
func subagentDelegationUpdate(req *jsObject, update *jsObject) *jsObject {
	u := toDelegationUpdate(update)
	if u == nil {
		return nil
	}
	o := obj("requestId", req.vals["requestId"], "ownerRunId", req.vals["ownerRunId"], "nodeId", req.vals["nodeId"])
	str := func(k, v string) {
		if v != "" {
			o.set(k, v)
		}
	}
	str("runId", u.RunID)
	str("currentTool", u.CurrentTool)
	str("currentToolArgs", u.CurrentToolArgs)
	str("recentOutput", u.RecentOutput)
	if len(u.RecentOutputLines) > 0 {
		o.set("recentOutputLines", jsStrings(u.RecentOutputLines))
	}
	if len(u.RecentTools) > 0 {
		tools := make([]any, len(u.RecentTools))
		for i, t := range u.RecentTools {
			tools[i] = obj("tool", t.Tool, "args", t.Args)
		}
		o.set("recentTools", tools)
	}
	str("model", u.Model)
	for _, p := range []struct {
		k string
		v *float64
	}{{"toolCount", u.ToolCount}, {"durationMs", u.DurationMs}, {"tokens", u.Tokens}} {
		if p.v != nil {
			o.set(p.k, *p.v)
		}
	}
	if u.Usage != nil {
		o.set("usage", obj("input", u.Usage.Input, "output", u.Usage.Output, "cacheRead", u.Usage.CacheRead, "cacheWrite", u.Usage.CacheWrite, "turns", u.Usage.Turns))
	}
	return o
}

// sameDelegationProgress: a duration alone is a heartbeat, not visible progress.
func sameDelegationProgress(a, b *jsObject) bool {
	if a == nil || b == nil {
		return a == b
	}
	for _, k := range append(append([]string{}, a.order()...), b.order()...) {
		if k == "durationMs" {
			continue
		}
		if marshalJSON(a.vals[k], "") != marshalJSON(b.vals[k], "") || a.has(k) != b.has(k) {
			return false
		}
	}
	return true
}

// --- terminal responses ---

func firstTextContent(content any) string {
	arr, _ := content.([]any)
	for _, p := range arr {
		o, ok := p.(*jsObject)
		if !ok {
			continue
		}
		if t, _ := o.str("type"); t != "text" {
			continue
		}
		if s, ok := o.str("text"); ok && jsTrim(s) != "" {
			return jsTrim(s)
		}
	}
	return ""
}

func boolOf(o *jsObject, k string) bool { b, _ := o.flag(k); return b }

func resolveDelegationStatus(result *jsObject, aborted bool) string {
	if aborted {
		return "cancelled"
	}
	details := result.obj("details")
	var child *jsObject
	if details != nil {
		if rs, _ := details.vals["results"].([]any); len(rs) > 0 {
			child, _ = rs[0].(*jsObject)
		}
	}
	if child == nil {
		return "failed"
	}
	if boolOf(details, "timedOut") || boolOf(child, "timedOut") {
		return "timed_out"
	}
	if boolOf(child, "structuredOutputFailed") {
		return "structured_output_failed"
	}
	if boolOf(child, "toolBudgetBlocked") {
		return "tool_budget_exhausted"
	}
	if acc := child.obj("acceptance"); acc != nil {
		if s, _ := acc.str("status"); s == "rejected" && boolOf(acc, "explicit") {
			return "acceptance_failed"
		}
	}
	if boolOf(details, "stopped") || boolOf(child, "stopped") || boolOf(child, "interrupted") {
		return "interrupted"
	}
	code, hasCode := child.num("exitCode")
	if boolOf(result, "isError") || child.has("error") && child.vals["error"] != "" || hasCode && code != 0 {
		return "failed"
	}
	return "completed"
}

// subagentDelegationResponse is the terminal response of a structured request.
func subagentDelegationResponse(req *jsObject, result *jsObject, aborted bool) *jsObject {
	details := result.obj("details")
	if details == nil {
		details = newObject()
	}
	var child *jsObject
	if rs, _ := details.vals["results"].([]any); len(rs) > 0 {
		child, _ = rs[0].(*jsObject)
	}
	if child == nil {
		child = newObject()
	}
	progress := child.obj("progressSummary")
	if progress == nil {
		if pl, _ := details.vals["progress"].([]any); len(pl) > 0 {
			progress, _ = pl[0].(*jsObject)
		}
	}
	if progress == nil {
		progress = newObject()
	}
	status := resolveDelegationStatus(result, aborted)
	errText, _ := child.str("error")
	if errText == "" && status == "failed" {
		errText = firstTextContent(result.vals["content"])
	}
	var projected any
	reqResult := req.obj("result")
	if status == "completed" {
		if reqResult.vals["kind"] == "text" {
			text, isStr := child.vals["finalOutput"].(string)
			switch {
			case !isStr:
				status, errText = "failed", "Delegated subagent did not capture a text result."
			case len(text) > maxDelegationResultBytes:
				status, errText = "failed", "Delegated text result exceeds 1 MiB when UTF-8 encoded."
			default:
				projected = obj("kind", "text", "text", text)
			}
		} else if !child.has("structuredOutput") {
			status, errText = "failed", "Delegated subagent did not capture the requested structured result."
		} else {
			clone, _, reason := cloneJSONWithinByteLimit(child.vals["structuredOutput"], maxDelegationResultBytes)
			switch {
			case reason == "too_large":
				status, errText = "failed", "Delegated structured result exceeds 1 MiB when encoded."
			case reason != "":
				status, errText = "failed", "Delegated structured result is not plain JSON data."
			default:
				projected = obj("kind", "structured", "value", clone)
			}
		}
	}
	o := obj("requestId", req.vals["requestId"], "ownerRunId", req.vals["ownerRunId"], "nodeId", req.vals["nodeId"], "status", status)
	setStr := func(k string, from *jsObject, fk string) {
		if s, ok := from.str(fk); ok && s != "" {
			o.set(k, s)
		}
	}
	if errText != "" {
		o.set("error", errText)
	}
	setStr("runId", details, "runId")
	setStr("agent", child, "agent")
	setStr("model", child, "model")
	setStr("thinking", child, "thinking")
	if code, ok := child.num("exitCode"); ok {
		o.set("exitCode", code)
	}
	setStr("launchContractDigest", child, "launchContractDigest")
	if projected != nil {
		o.set("result", projected)
	}
	if usage := child.obj("usage"); usage != nil {
		toolCalls, _ := progress.num("toolCount")
		duration, _ := progress.num("durationMs")
		get := func(k string) float64 { f, _ := usage.num(k); return f }
		o.set("usage", obj("input", get("input"), "output", get("output"), "cacheRead", get("cacheRead"), "cacheWrite", get("cacheWrite"), "cost", get("cost"), "turns", get("turns"), "toolCalls", toolCalls, "durationMs", duration))
	}
	return o
}

// --- the bridge ---

// delegationEvents is the shared event bus the bridge listens on.
type delegationEvents interface {
	On(event string, handler func(data any)) func()
	Emit(event string, data any)
}

// delegationExecutor runs one delegated request; done closes on cancellation, onUpdate reports progress.
type delegationExecutor func(requestID string, params *jsObject, done <-chan struct{}, ctx any, onUpdate func(update *jsObject)) (*jsObject, error)

type delegationController struct {
	done      chan struct{}
	closeOnce sync.Once
}

func newDelegationController() *delegationController {
	return &delegationController{done: make(chan struct{})}
}
func (c *delegationController) abort() { c.closeOnce.Do(func() { close(c.done) }) }
func (c *delegationController) aborted() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

type delegationBridge struct {
	events     delegationEvents
	getContext func() any
	execute    delegationExecutor

	mu                sync.Mutex
	legacyPending     map[string]bool
	attempts          map[string]*delegationController
	pendingAttempt    map[string]bool
	activeNodes       map[string]*delegationController
	settled           map[string]bool
	subscriptions     []func()
	disposed          bool
	identitySaturated bool
	wg                sync.WaitGroup
}

func validBridgeID(v any) (string, bool) { return validDelegationID(v) }

func hasStructuredMarker(data any) bool {
	o, ok := data.(*jsObject)
	if !ok {
		return false
	}
	for _, k := range []string{"ownerRunId", "nodeId", "result", "version"} {
		if _, has := o.vals[k]; has {
			return true
		}
	}
	return false
}

func attemptKey(requestID, ownerRunID, nodeID string) string {
	b, _ := json.Marshal([]string{requestID, ownerRunID, nodeID})
	return string(b)
}

func nodeKey(ownerRunID, nodeID string) string {
	b, _ := json.Marshal([]string{ownerRunID, nodeID})
	return string(b)
}

const maxDelegationIdentities = 8192

// registerPromptTemplateDelegationBridge subscribes the bridge to its request and cancel events.
func registerPromptTemplateDelegationBridge(events delegationEvents, getContext func() any, execute delegationExecutor) *delegationBridge {
	b := &delegationBridge{events: events, getContext: getContext, execute: execute, legacyPending: map[string]bool{},
		attempts: map[string]*delegationController{}, pendingAttempt: map[string]bool{}, activeNodes: map[string]*delegationController{}, settled: map[string]bool{}}
	b.subscriptions = append(b.subscriptions, events.On(delegationCancelEvent, b.onCancel), events.On(delegationRequestEvent, b.onRequest))
	return b
}

func (b *delegationBridge) rememberIdentity(m map[string]bool, key string) {
	if m[key] || b.identitySaturated {
		return
	}
	m[key] = true
	if len(m) == maxDelegationIdentities {
		// Exact cancellation and terminal-attempt facts are security state, not an LRU cache: once full, fail closed.
		b.identitySaturated = true
	}
}

func (b *delegationBridge) emitTerminal(key string, payload *jsObject) {
	b.mu.Lock()
	if b.disposed || b.settled[key] {
		b.mu.Unlock()
		return
	}
	b.rememberIdentity(b.settled, key)
	b.mu.Unlock()
	b.events.Emit(delegationResponseEvent, payload)
}

func (b *delegationBridge) onCancel(data any) {
	value, ok := data.(*jsObject)
	if !ok {
		return
	}
	requestID, ok := validBridgeID(value.vals["requestId"])
	if !ok {
		return
	}
	if hasStructuredMarker(data) {
		for _, k := range value.order() {
			if k != "requestId" && k != "ownerRunId" && k != "nodeId" {
				return
			}
		}
		ownerRunID, ok1 := validBridgeID(value.vals["ownerRunId"])
		nodeID, ok2 := validBridgeID(value.vals["nodeId"])
		if !ok1 || !ok2 {
			return
		}
		key := attemptKey(requestID, ownerRunID, nodeID)
		b.mu.Lock()
		c := b.attempts[key]
		if c == nil {
			b.rememberIdentity(b.pendingAttempt, key)
		}
		b.mu.Unlock()
		if c != nil {
			c.abort()
		}
		return
	}
	b.mu.Lock()
	b.legacyPending[requestID] = true
	b.mu.Unlock()
}

func (b *delegationBridge) onRequest(data any) {
	if !hasStructuredMarker(data) {
		b.rejectLegacy(data)
		return
	}
	req, failure := parseSubagentDelegationRequest(data)
	if failure != nil {
		b.mu.Lock()
		disposed := b.disposed
		b.mu.Unlock()
		if disposed || failure.RequestID == "" {
			return
		}
		payload := obj("requestId", failure.RequestID)
		if failure.OwnerRunID != "" {
			payload.set("ownerRunId", failure.OwnerRunID)
		}
		if failure.NodeID != "" {
			payload.set("nodeId", failure.NodeID)
		}
		payload.set("status", "invalid_request")
		payload.set("error", failure.Error)
		if failure.OwnerRunID != "" && failure.NodeID != "" {
			key := attemptKey(failure.RequestID, failure.OwnerRunID, failure.NodeID)
			b.mu.Lock()
			_, active := b.attempts[key]
			b.mu.Unlock()
			if !active {
				b.emitTerminal(key, payload)
			}
		} else {
			b.events.Emit(delegationResponseEvent, payload)
		}
		return
	}
	requestID, _ := req.str("requestId")
	ownerRunID, _ := req.str("ownerRunId")
	nodeID, _ := req.str("nodeId")
	key := attemptKey(requestID, ownerRunID, nodeID)
	terminal := func(status string, extra ...any) {
		p := obj("requestId", requestID, "ownerRunId", ownerRunID, "nodeId", nodeID, "status", status)
		for i := 0; i+1 < len(extra); i += 2 {
			p.set(extra[i].(string), extra[i+1])
		}
		b.emitTerminal(key, p)
	}
	b.mu.Lock()
	if b.attempts[key] != nil || b.settled[key] {
		b.mu.Unlock()
		return
	}
	if b.pendingAttempt[key] {
		delete(b.pendingAttempt, key)
		b.mu.Unlock()
		terminal("cancelled")
		return
	}
	if b.identitySaturated {
		b.mu.Unlock()
		b.events.Emit(delegationResponseEvent, obj("requestId", requestID, "ownerRunId", ownerRunID, "nodeId", nodeID, "status", "unavailable_context",
			"error", "Delegation identity capacity is exhausted for this extension context."))
		return
	}
	if b.activeNodes[nodeKey(ownerRunID, nodeID)] != nil {
		b.mu.Unlock()
		terminal("duplicate_node")
		return
	}
	b.mu.Unlock()
	ctx := b.getContext()
	if ctx == nil {
		terminal("unavailable_context", "error", "No active extension context for delegated subagent execution.")
		return
	}
	c := newDelegationController()
	b.mu.Lock()
	b.attempts[key] = c
	b.activeNodes[nodeKey(ownerRunID, nodeID)] = c
	b.mu.Unlock()
	b.events.Emit(delegationStartedEvent, obj("requestId", requestID, "ownerRunId", ownerRunID, "nodeId", nodeID))
	b.wg.Add(1)
	go b.run(req, key, c, ctx)
}

func (b *delegationBridge) owns(key string, c *delegationController) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return !b.disposed && b.attempts[key] == c
}

func (b *delegationBridge) run(req *jsObject, key string, c *delegationController, ctx any) {
	defer b.wg.Done()
	requestID, _ := req.str("requestId")
	ownerRunID, _ := req.str("ownerRunId")
	nodeID, _ := req.str("nodeId")
	defer func() {
		b.mu.Lock()
		if b.attempts[key] == c {
			delete(b.attempts, key)
		}
		if b.activeNodes[nodeKey(ownerRunID, nodeID)] == c {
			delete(b.activeNodes, nodeKey(ownerRunID, nodeID))
		}
		b.mu.Unlock()
	}()
	var last *jsObject
	result, err := b.execute(requestID, delegationExecParams(req), c.done, ctx, func(update *jsObject) {
		if !b.owns(key, c) {
			return
		}
		payload := subagentDelegationUpdate(req, update)
		if payload != nil && (last == nil || !sameDelegationProgress(last, payload)) {
			last = payload
			b.events.Emit(delegationUpdateEvent, payload)
		}
	})
	if !b.owns(key, c) {
		return
	}
	if err != nil {
		status := "failed"
		p := obj("requestId", requestID, "ownerRunId", ownerRunID, "nodeId", nodeID)
		if c.aborted() {
			status = "cancelled"
			p.set("status", status)
		} else {
			p.set("status", status)
			p.set("error", err.Error())
		}
		b.emitTerminal(key, p)
		return
	}
	b.emitTerminal(key, subagentDelegationResponse(req, result, c.aborted()))
}

// rejectLegacy answers a payload without the structured identity: the legacy direct delegation was removed.
func (b *delegationBridge) rejectLegacy(data any) {
	o, ok := data.(*jsObject)
	if !ok {
		return
	}
	if o.has("tasks") || o.has("worktree") {
		if id, ok := o.vals["requestId"].(string); ok && id != "" {
			b.events.Emit(delegationResponseEvent, obj("requestId", id, "messages", []any{}, "isError", true,
				"errorText", "Legacy prompt-template tasks/worktree orchestration was removed; use workflowScript."))
			return
		}
	}
	// parsePromptTemplateRequest: every field a legacy request carries must be present and valid.
	id, _ := o.str("requestId")
	agent, _ := o.str("agent")
	task, _ := o.str("task")
	model, _ := o.str("model")
	cwd, _ := o.str("cwd")
	ctxKind, _ := o.str("context")
	if id == "" || agent == "" || task == "" || model == "" || cwd == "" || (ctxKind != "fresh" && ctxKind != "fork") {
		return
	}
	b.events.Emit(delegationResponseEvent, obj("requestId", id, "agent", agent, "task", task, "context", ctxKind, "model", model, "cwd", cwd, "messages", []any{}, "isError", true,
		"errorText", "Legacy prompt-template direct delegation was removed; use workflowScript through the subagent tool or structured delegation."))
}

// cancelAll aborts every run and forgets the identities.
func (b *delegationBridge) cancelAll() {
	b.mu.Lock()
	for _, c := range b.attempts {
		c.abort()
	}
	b.attempts, b.legacyPending, b.pendingAttempt, b.activeNodes, b.settled = map[string]*delegationController{}, map[string]bool{}, map[string]bool{}, map[string]*delegationController{}, map[string]bool{}
	b.identitySaturated = false
	b.mu.Unlock()
}

// dispose aborts every run and unsubscribes.
func (b *delegationBridge) dispose() {
	b.mu.Lock()
	b.disposed = true
	for _, c := range b.attempts {
		c.abort()
	}
	subs := b.subscriptions
	b.subscriptions = nil
	b.attempts, b.legacyPending, b.pendingAttempt, b.activeNodes, b.settled = map[string]*delegationController{}, map[string]bool{}, map[string]bool{}, map[string]*delegationController{}, map[string]bool{}
	b.mu.Unlock()
	for _, unsub := range subs {
		unsub()
	}
}

// --- registration on the session's event bus ---

type sdkDelegationBus struct{ e *sdk.Extension }

func (s sdkDelegationBus) On(event string, handler func(data any)) func() {
	unsub, err := s.e.Events().On(event, func(_ sdk.Context, data any) error {
		raw, merr := json.Marshal(data)
		if merr != nil {
			return nil
		}
		v, perr := parseJSON(raw)
		if perr != nil {
			return nil
		}
		handler(v)
		return nil
	})
	if err != nil {
		return func() {}
	}
	return unsub
}

func (s sdkDelegationBus) Emit(event string, data any) { _ = s.e.Events().Emit(event, data) }

// delegationContext is what a delegated run needs of the session.
type delegationContext struct{ Cwd, Provider string }

func currentDelegationContext() any {
	adv.mu.Lock()
	defer adv.mu.Unlock()
	if !adv.hasCtx {
		return nil
	}
	return &delegationContext{adv.cwd, adv.provider}
}

// delegatedOptions are the request options the port's child runs do not honor.
var delegatedOptions = []struct{ field, feature string }{
	{"timeoutMs", "the `timeoutMs` option of a delegated run"}, {"toolBudget", "the `toolBudget` option of a delegated run"},
	{"skill", "the `skill` option of a delegated run"}, {"outputSchema", "structured results of a delegated run"},
	{"artifacts", "the `artifacts` option of a delegated run"}, {"intercomBridge", "the `intercomBridge` option of a delegated run"},
	{"delegatedThinkingOverride", "the `thinking` option of a delegated run"},
}

// executeDelegated runs one delegated request as one child: its text result is the child's final output.
func executeDelegated(_ string, params *jsObject, done <-chan struct{}, ctx any, _ func(update *jsObject)) (*jsObject, error) {
	for _, o := range delegatedOptions {
		if params.has(o.field) {
			return nil, fmt.Errorf("%s", refusal(o.feature))
		}
	}
	if params.vals["context"] == "fork" {
		return nil, fmt.Errorf("%s", refusal("forked context of a delegated run"))
	}
	if msg := nestedBlocked(os.Getenv); msg != "" {
		return nil, fmt.Errorf("%s", msg)
	}
	dc, _ := ctx.(*delegationContext)
	if dc == nil {
		return nil, fmt.Errorf("No active extension context for delegated subagent execution.")
	}
	name, _ := params.str("agent")
	d, err := discoverAgents(dc.Cwd, ScopeBoth, dc.Provider, adv.options())
	if err != nil {
		return nil, err
	}
	agent, msg := ResolveAgentName(jsTrim(name), d.Agents)
	if agent == nil {
		if msg == "" {
			msg = fmt.Sprintf("Unknown agent '%s'. Available: %s.", name, strings.Join(sortedNames(d.Agents), ", "))
		}
		return nil, fmt.Errorf("%s", msg)
	}
	cwd, _ := params.str("cwd")
	cwd, err = resolveRunCwd(dc.Cwd, cwd)
	if err != nil {
		return nil, err
	}
	task, _ := params.str("task")
	model, _ := params.str("model")
	out, err := runAgent(done, cwd, agent, task, model)
	if err != nil {
		return nil, err
	}
	return obj("details", obj("mode", "single", "results", []any{obj("agent", agent.Name, "finalOutput", out, "exitCode", float64(0))})), nil
}

func init() {
	extensionHooks = append(extensionHooks, func(e *sdk.Extension) {
		bridge := registerPromptTemplateDelegationBridge(sdkDelegationBus{e}, currentDelegationContext, executeDelegated)
		e.OnEvent(sdk.EventSessionShutdown, func(sdk.Context, map[string]any) (any, error) {
			bridge.cancelAll()
			return nil, nil
		})
	})
}
