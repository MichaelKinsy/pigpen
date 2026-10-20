package pi_subagents

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
)

// Ports of src/api/delegation.ts (the structured delegation contract), src/slash/delegation-json.ts,
// src/slash/delegation-request.ts, src/slash/delegation-adapters.ts and src/slash/prompt-template-bridge.ts: another extension
// delegates one owned leaf to a subagent over the event bus (the prompt-template event family) and receives started, update and
// terminal events. A payload is the JSON value the event bus carries: map[string]any, []any, string, float64, bool and nil.
//
// Not ported: the original's legacy prompt-template controllers (`legacyControllers`, `pendingLegacyCancels`). Every legacy
// request is rejected before it registers a controller, so nothing ever reads that state.

const (
	subagentDelegationRequestEvent  = "prompt-template:subagent:request"
	subagentDelegationStartedEvent  = "prompt-template:subagent:started"
	subagentDelegationUpdateEvent   = "prompt-template:subagent:update"
	subagentDelegationResponseEvent = "prompt-template:subagent:response"
	subagentDelegationCancelEvent   = "prompt-template:subagent:cancel"
)

const (
	delegationMaxSchemaBytes    = 64 * 1024
	delegationMaxTaskBytes      = 1024 * 1024
	delegationMaxCwdBytes       = 32 * 1024
	delegationMaxShortTextBytes = 1024
	delegationMaxSkillEntries   = 256
	delegationMaxSkillAggregate = 64 * 1024
	delegationMaxResultBytes    = 1024 * 1024
	delegationMaxJSONDepth      = 64
	delegationMaxJSONEntries    = 100_000
	delegationIdentityCapacity  = 8_192
	delegationMaxTimeoutMs      = 2_147_483_647
)

// ---- delegation-json.ts ----------------------------------------------------------------------------------------------------

// cloneJSONWithinByteLimit clones plain JSON data without running anything it holds (a function is not data). reason is
// "invalid" or "too_large" when ok is false.
func cloneJSONWithinByteLimit(input any, maxBytes int) (value any, encodedBytes int, reason string) {
	minimum, entries := 0, 0
	add := func(n int) bool {
		minimum += n
		return minimum <= maxBytes
	}
	type fail string
	var visit func(v any, depth int) any
	visit = func(v any, depth int) any {
		if depth > delegationMaxJSONDepth {
			panic(fail("invalid"))
		}
		switch x := v.(type) {
		case nil:
			if !add(4) {
				panic(fail("too_large"))
			}
			return nil
		case bool:
			n := 5
			if x {
				n = 4
			}
			if !add(n) {
				panic(fail("too_large"))
			}
			return x
		case int:
			return visit(float64(x), depth)
		case float64:
			if math.IsNaN(x) || math.IsInf(x, 0) {
				panic(fail("invalid"))
			}
			if x == 0 {
				x = 0 // -0 encodes as 0
			}
			if !add(len(marshalJSON(x, ""))) {
				panic(fail("too_large"))
			}
			return x
		case string:
			if !add(len(x) + 2) { // quoting and escaping can only increase the encoded size
				panic(fail("too_large"))
			}
			return x
		case []any:
			if !add(2 + max(0, len(x)-1)) {
				panic(fail("too_large"))
			}
			out := make([]any, 0, len(x))
			for _, e := range x {
				if entries++; entries > delegationMaxJSONEntries {
					panic(fail("invalid"))
				}
				out = append(out, visit(e, depth+1))
			}
			return out
		case map[string]any:
			if !add(2 + max(0, len(x)-1)) {
				panic(fail("too_large"))
			}
			out := make(map[string]any, len(x))
			for k, e := range x {
				if entries++; entries > delegationMaxJSONEntries {
					panic(fail("invalid"))
				}
				if !add(len(marshalJSON(k, "")) + 1) {
					panic(fail("too_large"))
				}
				out[k] = visit(e, depth+1)
			}
			return out
		case *jsObject:
			m := make(map[string]any, len(x.keys))
			for _, k := range x.keys {
				m[k] = x.vals[k]
			}
			return visit(m, depth)
		}
		panic(fail("invalid"))
	}
	defer func() {
		if r := recover(); r != nil {
			f, ok := r.(fail)
			if !ok {
				panic(r)
			}
			value, encodedBytes, reason = nil, 0, string(f)
		}
	}()
	cloned := visit(input, 0)
	n := len(marshalJSON(cloned, ""))
	if n > maxBytes {
		return nil, 0, "too_large"
	}
	return cloned, n, ""
}

// ---- delegation-request.ts -------------------------------------------------------------------------------------------------

// delegationParseResult is SubagentDelegationParseResult: Request is the validated copy (never aliasing the caller's data).
type delegationParseResult struct {
	OK                            bool
	Request                       map[string]any
	RequestID, OwnerRunID, NodeID string
	Error                         string
}

var delegationSupportedFields = map[string]bool{"requestId": true, "ownerRunId": true, "nodeId": true, "agent": true, "task": true, "context": true,
	"cwd": true, "model": true, "thinking": true, "timeoutMs": true, "toolBudget": true, "skill": true, "artifacts": true, "intercomBridge": true, "result": true}

var delegationThinkingLevels = map[string]bool{"off": true, "minimal": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true}

func nonEmptyDelegationString(v any) bool {
	s, ok := v.(string)
	return ok && jsTrim(s) != ""
}

func validDelegationID(v any) (string, bool) {
	s, ok := v.(string)
	if !ok || jsTrim(s) == "" || jsLen(s) > 256 || strings.ContainsAny(s, "\r\n") {
		return "", false
	}
	return s, true
}

// delegationFields is an object's fields: insertion order for a *jsObject, sorted for a map.
func delegationFields(v any) (*jsObject, bool) { return asObject(v) }

func delegationField(o *jsObject, k string) (any, bool) {
	v, ok := o.vals[k]
	if ok && !isUndefined(v) || ok && v == nil {
		return v, true
	}
	return nil, false
}

// parseSubagentDelegationRequest strictly parses the structured owned-leaf request.
func parseSubagentDelegationRequest(data any) delegationParseResult {
	value, ok := delegationFields(data)
	if !ok {
		return delegationParseResult{Error: "Delegation request must be an object."}
	}
	requestID, ok := validDelegationID(value.vals["requestId"])
	if !ok {
		return delegationParseResult{Error: "Delegation requestId must be a non-empty string of at most 256 characters without newlines."}
	}
	ownerRunID, ok := validDelegationID(value.vals["ownerRunId"])
	if !ok {
		return delegationParseResult{RequestID: requestID, Error: "Delegation ownerRunId must be a non-empty string of at most 256 characters without newlines."}
	}
	nodeID, ok := validDelegationID(value.vals["nodeId"])
	if !ok {
		return delegationParseResult{RequestID: requestID, OwnerRunID: ownerRunID, Error: "Delegation nodeId must be a non-empty string of at most 256 characters without newlines."}
	}
	fail := func(msg string) delegationParseResult {
		return delegationParseResult{RequestID: requestID, OwnerRunID: ownerRunID, NodeID: nodeID, Error: msg}
	}
	for _, k := range value.keys {
		if !delegationSupportedFields[k] {
			return fail("Unsupported delegation field: " + k + ".")
		}
	}
	if !nonEmptyDelegationString(value.vals["agent"]) {
		return fail("Delegation agent must be a non-empty string.")
	}
	if !nonEmptyDelegationString(value.vals["task"]) {
		return fail("Delegation task must be a non-empty string.")
	}
	if c := value.vals["context"]; c != "fresh" && c != "fork" {
		return fail("Delegation context must be fresh or fork.")
	}
	if !nonEmptyDelegationString(value.vals["cwd"]) {
		return fail("Delegation cwd must be a non-empty string.")
	}
	if m, has := delegationField(value, "model"); has && !nonEmptyDelegationString(m) {
		return fail("model must be a non-empty string when provided.")
	}
	if t, has := delegationField(value, "timeoutMs"); has {
		f, isNum := asNumber(t)
		if !isNum || !isIntegerValue(t) || f < 1 {
			return fail("timeoutMs must be an integer >= 1.")
		}
		if f > delegationMaxTimeoutMs {
			return fail("timeoutMs must be <= 2147483647.")
		}
	}
	if tb, ok := asObject(value.vals["toolBudget"]); ok {
		for _, k := range tb.keys {
			if k != "soft" && k != "hard" && k != "block" {
				return fail("toolBudget." + k + " is not supported.")
			}
		}
	}
	if _, msg := validateToolBudgetConfig(value.vals["toolBudget"], "toolBudget", 0); msg != "" {
		return fail(msg)
	}
	skill, hasSkill := delegationField(value, "skill")
	if hasSkill {
		_, isBool := skill.(bool)
		arr, isArr := asArray(skill)
		valid := isBool || nonEmptyDelegationString(skill)
		if isArr && len(arr) > 0 {
			valid = true
			for _, e := range arr {
				valid = valid && nonEmptyDelegationString(e)
			}
		}
		if !valid {
			return fail("skill must be a boolean, non-empty string, or non-empty string array.")
		}
	}
	if a, has := delegationField(value, "artifacts"); has {
		if _, isBool := a.(bool); !isBool {
			return fail("artifacts must be a boolean.")
		}
	}
	var bridge map[string]any
	if b, has := delegationField(value, "intercomBridge"); has {
		cfg, msg := validateIntercomBridgeConfig(b, "intercomBridge")
		if msg != "" {
			return fail(msg)
		}
		bridge = map[string]any{}
		if cfg.Mode != "" {
			bridge["mode"] = cfg.Mode
		}
		if cfg.InstructionFile != nil {
			if len(*cfg.InstructionFile) > delegationMaxShortTextBytes {
				return fail("intercomBridge.instructionFile exceeds 1 KiB when UTF-8 encoded.")
			}
			bridge["instructionFile"] = *cfg.InstructionFile
		}
		if cfg.ResultDelivery != nil {
			bridge["resultDelivery"] = *cfg.ResultDelivery
		}
	}
	if len(value.vals["task"].(string)) > delegationMaxTaskBytes {
		return fail("Delegation task exceeds 1 MiB when UTF-8 encoded.")
	}
	if len(value.vals["cwd"].(string)) > delegationMaxCwdBytes {
		return fail("Delegation cwd exceeds 32 KiB when UTF-8 encoded.")
	}
	if len(value.vals["agent"].(string)) > delegationMaxShortTextBytes {
		return fail("Delegation agent exceeds 1 KiB when UTF-8 encoded.")
	}
	if m, ok := value.vals["model"].(string); ok && len(m) > delegationMaxShortTextBytes {
		return fail("Delegation model exceeds 1 KiB when UTF-8 encoded.")
	}
	var skillEntries []string
	if s, ok := skill.(string); ok {
		skillEntries = []string{s}
	} else if arr, ok := asArray(skill); ok {
		for _, e := range arr {
			skillEntries = append(skillEntries, e.(string))
		}
	}
	if len(skillEntries) > delegationMaxSkillEntries {
		return fail("Delegation skill supports at most 256 entries.")
	}
	total := 0
	for _, e := range skillEntries {
		if len(e) > delegationMaxShortTextBytes {
			return fail("Delegation skill entry exceeds 1 KiB when UTF-8 encoded.")
		}
		total += len(e)
	}
	if total > delegationMaxSkillAggregate {
		return fail("Delegation skill entries exceed 64 KiB in aggregate when UTF-8 encoded.")
	}
	if th, has := delegationField(value, "thinking"); has {
		if s, isStr := th.(string); !isStr || !delegationThinkingLevels[s] {
			return fail("thinking must be one of off, minimal, low, medium, high, xhigh, or max.")
		}
	}
	result, ok := asObject(value.vals["result"])
	if !ok {
		return fail(`result must be { kind: "text" } or { kind: "structured", schema: object }.`)
	}
	var schema any
	switch result.vals["kind"] {
	case "text":
		for _, k := range result.keys {
			if k != "kind" {
				return fail("result." + k + " is not supported for text results.")
			}
		}
	case "structured":
		for _, k := range result.keys {
			if k != "kind" && k != "schema" {
				return fail("result." + k + " is not supported for structured results.")
			}
		}
		if _, isObj := asObject(result.vals["schema"]); !isObj {
			return fail("result.schema must be a JSON Schema object.")
		}
		cloned, _, reason := cloneJSONWithinByteLimit(result.vals["schema"], delegationMaxSchemaBytes)
		switch reason {
		case "too_large":
			return fail("result.schema exceeds 64 KiB when encoded.")
		case "invalid":
			return fail("result.schema must be plain JSON data.")
		}
		schema = cloned
	default:
		return fail("result.kind must be text or structured.")
	}
	request := map[string]any{}
	for _, k := range value.keys {
		request[k] = value.vals[k]
	}
	// The validated copy replaces the caller's objects so the request never aliases untrusted input.
	if bridge != nil {
		request["intercomBridge"] = bridge
	}
	if schema != nil {
		request["result"] = map[string]any{"kind": "structured", "schema": schema}
	} else {
		request["result"] = map[string]any{"kind": "text"}
	}
	return delegationParseResult{OK: true, Request: request, RequestID: requestID, OwnerRunID: ownerRunID, NodeID: nodeID}
}

// ---- delegation-adapters.ts ------------------------------------------------------------------------------------------------

type bridgeUsage struct{ Input, Output, CacheRead, CacheWrite, Cost, Turns float64 }

type bridgeProgressSummary struct{ ToolCount, DurationMs, Tokens *float64 }

// bridgeChild is one entry of PromptTemplateBridgeResult.details.results.
type bridgeChild struct {
	Agent, Model, Thinking, Error, LaunchContractDigest string
	FinalOutput                                         *string
	ExitCode                                            *float64
	StructuredOutput                                    any
	StructuredOutputSet                                 bool
	Interrupted, TimedOut, Stopped                      bool
	ToolBudgetBlocked, StructuredOutputFailed           bool
	AcceptanceStatus                                    string
	AcceptanceExplicit                                  bool
	Usage                                               *bridgeUsage
	ProgressSummary                                     *bridgeProgressSummary
}

type bridgeTool struct{ Tool, Args string }

// bridgeProgress is one entry of details.progress.
type bridgeProgress struct {
	Index                                                       *float64
	Agent, Status, CurrentTool, CurrentToolArgs                 string
	RecentOutput                                                []string
	RecentTools                                                 []bridgeTool
	ToolCount, DurationMs, Tokens                               *float64
	InputTokens, OutputTokens, CacheRead, CacheWrite, TurnCount *float64
}

type bridgeDetails struct {
	Mode, RunID       string
	TimedOut, Stopped bool
	Results           []bridgeChild
	Progress          []bridgeProgress
}

// bridgeResult is PromptTemplateBridgeResult. Content is the tool result's content (a list of {type, text} parts).
type bridgeResult struct {
	IsError bool
	Content []any
	Details *bridgeDetails
}

func (r *bridgeResult) child() *bridgeChild {
	if r == nil || r.Details == nil || len(r.Details.Results) == 0 {
		return nil
	}
	return &r.Details.Results[0]
}

func firstTextContent(content []any) string {
	for _, part := range content {
		o, ok := asObject(part)
		if !ok || o.vals["type"] != "text" {
			continue
		}
		if s, ok := o.vals["text"].(string); ok && jsTrim(s) != "" {
			return jsTrim(s)
		}
	}
	return ""
}

func filterRecentOutput(lines []string) []string {
	var out []string
	for _, l := range lines {
		if jsTrim(l) != "" && jsTrim(l) != "(running...)" {
			out = append(out, l)
		}
	}
	return out
}

func sanitizeRecentTools(tools []bridgeTool) []bridgeTool {
	var out []bridgeTool
	for _, t := range tools {
		if jsTrim(t.Tool) != "" {
			out = append(out, t)
		}
	}
	return out
}

func resolveProgressModel(r *bridgeResult, entry bridgeProgress) string {
	if r.Details == nil || len(r.Details.Results) == 0 {
		return ""
	}
	results := r.Details.Results
	if entry.Index != nil && *entry.Index >= 0 && *entry.Index < float64(len(results)) {
		if m := results[int(*entry.Index)].Model; m != "" {
			return m
		}
	}
	if entry.Agent != "" {
		for _, c := range results {
			if c.Agent == entry.Agent && c.Model != "" {
				return c.Model
			}
		}
	}
	for _, c := range results {
		if c.Model != "" {
			return c.Model
		}
	}
	return ""
}

func finiteNonNegative(p *float64) bool {
	return p != nil && !math.IsNaN(*p) && !math.IsInf(*p, 0) && *p >= 0
}

// delegationUpdateUsage is the cumulative usage of one attempt: present only when every counter is known, finite and
// non-negative.
type delegationUpdateUsage struct{ Input, Output, CacheRead, CacheWrite, Turns float64 }

type delegationUpdate struct {
	RunID, CurrentTool, CurrentToolArgs, RecentOutput, Model string
	RecentOutputLines                                        []string
	RecentTools                                              []bridgeTool
	ToolCount, DurationMs, Tokens                            *float64
	Usage                                                    *delegationUpdateUsage
}

func lastSafeOutput(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	last := lines[len(lines)-1]
	if jsTrim(last) != "" && last != "(running...)" {
		return last
	}
	return ""
}

// toSubagentDelegationUpdate projects one executor update; nil when the update carries no progress.
func toSubagentDelegationUpdate(r *bridgeResult) *delegationUpdate {
	if r.Details == nil || len(r.Details.Progress) == 0 {
		return nil
	}
	p := r.Details.Progress[0]
	u := &delegationUpdate{RunID: r.Details.RunID, CurrentTool: p.CurrentTool, CurrentToolArgs: p.CurrentToolArgs, RecentOutput: lastSafeOutput(p.RecentOutput),
		RecentOutputLines: filterRecentOutput(p.RecentOutput), RecentTools: sanitizeRecentTools(p.RecentTools), Model: resolveProgressModel(r, p),
		ToolCount: p.ToolCount, DurationMs: p.DurationMs, Tokens: p.Tokens}
	if finiteNonNegative(p.InputTokens) && finiteNonNegative(p.OutputTokens) && finiteNonNegative(p.CacheRead) && finiteNonNegative(p.CacheWrite) && finiteNonNegative(p.TurnCount) {
		u.Usage = &delegationUpdateUsage{*p.InputTokens, *p.OutputTokens, *p.CacheRead, *p.CacheWrite, *p.TurnCount}
	}
	return u
}

func (u *delegationUpdate) payload(request map[string]any) map[string]any {
	m := map[string]any{"requestId": request["requestId"], "ownerRunId": request["ownerRunId"], "nodeId": request["nodeId"]}
	putIf(m, "runId", u.RunID)
	putIf(m, "currentTool", u.CurrentTool)
	putIf(m, "currentToolArgs", u.CurrentToolArgs)
	putIf(m, "recentOutput", u.RecentOutput)
	if len(u.RecentOutputLines) > 0 {
		m["recentOutputLines"] = anyStrings(u.RecentOutputLines)
	}
	if len(u.RecentTools) > 0 {
		tools := make([]any, len(u.RecentTools))
		for i, t := range u.RecentTools {
			tools[i] = map[string]any{"tool": t.Tool, "args": t.Args}
		}
		m["recentTools"] = tools
	}
	putIf(m, "model", u.Model)
	for k, p := range map[string]*float64{"toolCount": u.ToolCount, "durationMs": u.DurationMs, "tokens": u.Tokens} {
		if p != nil && !math.IsNaN(*p) {
			m[k] = *p
		}
	}
	if u.Usage != nil {
		m["usage"] = map[string]any{"input": u.Usage.Input, "output": u.Usage.Output, "cacheRead": u.Usage.CacheRead, "cacheWrite": u.Usage.CacheWrite, "turns": u.Usage.Turns}
	}
	return m
}

func sameFloatPtr(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// sameDelegationUpdateProgress: duration is a heartbeat clock, not delegation-visible progress; terminal usage stays authoritative.
// A cache-only or turn-only change with unchanged tokens and tool progress still counts as a change.
func sameDelegationUpdateProgress(l, r *delegationUpdate) bool {
	if l.RunID != r.RunID || l.CurrentTool != r.CurrentTool || l.CurrentToolArgs != r.CurrentToolArgs || l.RecentOutput != r.RecentOutput || l.Model != r.Model {
		return false
	}
	if len(l.RecentOutputLines) != len(r.RecentOutputLines) || len(l.RecentTools) != len(r.RecentTools) {
		return false
	}
	for i := range l.RecentOutputLines {
		if l.RecentOutputLines[i] != r.RecentOutputLines[i] {
			return false
		}
	}
	for i := range l.RecentTools {
		if l.RecentTools[i] != r.RecentTools[i] {
			return false
		}
	}
	if !sameFloatPtr(l.ToolCount, r.ToolCount) || !sameFloatPtr(l.Tokens, r.Tokens) {
		return false
	}
	if l.Usage == nil || r.Usage == nil {
		return l.Usage == r.Usage
	}
	return *l.Usage == *r.Usage
}

// toSubagentDelegationExecutionParams is the request as the foreground executor takes it.
func toSubagentDelegationExecutionParams(request map[string]any) map[string]any {
	p := map[string]any{"agent": request["agent"], "task": request["task"], "context": request["context"], "cwd": request["cwd"]}
	for _, k := range []string{"model", "timeoutMs", "toolBudget", "skill", "artifacts"} {
		if v, has := request[k]; has && v != nil {
			p[k] = v
		}
	}
	if result, _ := request["result"].(map[string]any); result["kind"] == "structured" {
		p["outputSchema"] = result["schema"]
	}
	p["acceptance"] = false
	if v, has := request["intercomBridge"]; has {
		p["intercomBridge"] = v
	}
	if v, has := request["thinking"]; has && v != nil {
		p["delegatedThinkingOverride"] = v
	}
	p["delegatedAllowZeroToolBudget"] = true
	p["async"] = false
	p["foregroundOnly"] = true
	p["clarify"] = false
	return p
}

func resolveSubagentDelegationStatus(r *bridgeResult, aborted bool) string {
	if aborted {
		return "cancelled"
	}
	child := r.child()
	if child == nil {
		return "failed"
	}
	switch {
	case r.Details.TimedOut || child.TimedOut:
		return "timed_out"
	case child.StructuredOutputFailed:
		return "structured_output_failed"
	case child.ToolBudgetBlocked:
		return "tool_budget_exhausted"
	case child.AcceptanceStatus == "rejected" && child.AcceptanceExplicit:
		return "acceptance_failed"
	case r.Details.Stopped || child.Stopped || child.Interrupted:
		return "interrupted"
	case r.IsError || child.Error != "" || (child.ExitCode != nil && *child.ExitCode != 0):
		return "failed"
	}
	return "completed"
}

// toSubagentDelegationResponse is the terminal response of one attempt.
func toSubagentDelegationResponse(request map[string]any, r *bridgeResult, aborted bool) map[string]any {
	child := r.child()
	status := resolveSubagentDelegationStatus(r, aborted)
	errText := ""
	if child != nil {
		errText = child.Error
	}
	if errText == "" && status == "failed" {
		errText = firstTextContent(r.Content)
	}
	var projected map[string]any
	resultReq, _ := request["result"].(map[string]any)
	if status == "completed" {
		if resultReq["kind"] == "text" {
			switch {
			case child == nil || child.FinalOutput == nil:
				status, errText = "failed", "Delegated subagent did not capture a text result."
			case len(*child.FinalOutput) > delegationMaxResultBytes:
				status, errText = "failed", "Delegated text result exceeds 1 MiB when UTF-8 encoded."
			default:
				projected = map[string]any{"kind": "text", "text": *child.FinalOutput}
			}
		} else if child == nil || !child.StructuredOutputSet {
			status, errText = "failed", "Delegated subagent did not capture the requested structured result."
		} else {
			cloned, _, reason := cloneJSONWithinByteLimit(child.StructuredOutput, delegationMaxResultBytes)
			switch reason {
			case "too_large":
				status, errText = "failed", "Delegated structured result exceeds 1 MiB when encoded."
			case "invalid":
				status, errText = "failed", "Delegated structured result is not plain JSON data."
			default:
				projected = map[string]any{"kind": "structured", "value": cloned}
			}
		}
	}
	m := map[string]any{"requestId": request["requestId"], "ownerRunId": request["ownerRunId"], "nodeId": request["nodeId"], "status": status}
	putIf(m, "error", errText)
	if r.Details != nil {
		putIf(m, "runId", r.Details.RunID)
	}
	if child != nil {
		putIf(m, "agent", child.Agent)
		putIf(m, "model", child.Model)
		putIf(m, "thinking", child.Thinking)
		if child.ExitCode != nil {
			m["exitCode"] = *child.ExitCode
		}
		putIf(m, "launchContractDigest", child.LaunchContractDigest)
		if projected != nil {
			m["result"] = projected
		}
		if u := child.Usage; u != nil {
			toolCalls, durationMs := 0.0, 0.0
			var ps *bridgeProgressSummary
			if child.ProgressSummary != nil {
				ps = child.ProgressSummary
			} else if len(r.Details.Progress) > 0 {
				p := r.Details.Progress[0]
				ps = &bridgeProgressSummary{ToolCount: p.ToolCount, DurationMs: p.DurationMs, Tokens: p.Tokens}
			}
			if ps != nil && ps.ToolCount != nil {
				toolCalls = *ps.ToolCount
			}
			if ps != nil && ps.DurationMs != nil {
				durationMs = *ps.DurationMs
			}
			m["usage"] = map[string]any{"input": u.Input, "output": u.Output, "cacheRead": u.CacheRead, "cacheWrite": u.CacheWrite, "cost": u.Cost, "turns": u.Turns,
				"toolCalls": toolCalls, "durationMs": durationMs}
		}
	}
	return m
}

// ---- prompt-template-bridge.ts ---------------------------------------------------------------------------------------------

// delegationEvents is the event bus the bridge listens and answers on (PromptTemplateBridgeEvents). Emit may block until the
// listeners have run; the bridge never emits while holding its lock.
type delegationEvents interface {
	On(event string, handler func(data any)) (unsubscribe func())
	Emit(event string, data any)
}

// delegationExecutor runs one delegated leaf. ctx ends when the attempt is cancelled.
type delegationExecutor func(requestID string, params map[string]any, ctx context.Context, bridgeCtx any, onUpdate func(*bridgeResult)) (*bridgeResult, error)

type delegationBridgeOptions struct {
	Events     delegationEvents
	GetContext func() (any, bool)
	// Execute is the legacy executor (never called: the legacy request is rejected); ExecuteStructured is the concurrent-safe one.
	Execute           delegationExecutor
	ExecuteStructured delegationExecutor
}

type delegationAttempt struct {
	cancel context.CancelFunc
	ctx    context.Context
}

type delegationOwnedNode struct {
	attemptKey string
	attempt    *delegationAttempt
}

type delegationBridge struct {
	opts                  delegationBridgeOptions
	mu                    sync.Mutex
	attempts              map[string]*delegationAttempt
	pendingAttemptCancels map[string]bool
	activeOwnedNodes      map[string]delegationOwnedNode
	settledAttempts       map[string]bool
	subscriptions         []func()
	disposed              bool
	identitySaturated     bool
}

func hasStructuredDelegationMarker(data any) bool {
	o, ok := asObject(data)
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

func delegationKey(parts ...string) string {
	a := make([]any, len(parts))
	for i, p := range parts {
		a[i] = p
	}
	return marshalJSON(a, "")
}

// registerPromptTemplateDelegationBridge subscribes the bridge to the request and cancel events.
func registerPromptTemplateDelegationBridge(opts delegationBridgeOptions) *delegationBridge {
	b := &delegationBridge{opts: opts, attempts: map[string]*delegationAttempt{}, pendingAttemptCancels: map[string]bool{}, activeOwnedNodes: map[string]delegationOwnedNode{}, settledAttempts: map[string]bool{}}
	b.subscribe(subagentDelegationCancelEvent, b.onCancel)
	b.subscribe(subagentDelegationRequestEvent, b.onRequest)
	return b
}

func (b *delegationBridge) subscribe(event string, handler func(any)) {
	if unsubscribe := b.opts.Events.On(event, handler); unsubscribe != nil {
		b.mu.Lock()
		b.subscriptions = append(b.subscriptions, unsubscribe)
		b.mu.Unlock()
	}
}

// rememberIdentity records exact cancellation and terminal-attempt facts. They are security state, not an LRU cache: once the
// table is full the bridge fails closed instead of evicting. The caller holds b.mu.
func (b *delegationBridge) rememberIdentity(m map[string]bool, key string) {
	if m[key] || b.identitySaturated {
		return
	}
	if len(m) >= delegationIdentityCapacity {
		b.identitySaturated = true
		return
	}
	m[key] = true
	if len(m) == delegationIdentityCapacity {
		b.identitySaturated = true
	}
}

// emitTerminal emits the one terminal response of an attempt.
func (b *delegationBridge) emitTerminal(key string, payload map[string]any) {
	b.mu.Lock()
	if b.disposed || b.settledAttempts[key] {
		b.mu.Unlock()
		return
	}
	b.rememberIdentity(b.settledAttempts, key)
	b.mu.Unlock()
	b.opts.Events.Emit(subagentDelegationResponseEvent, payload)
}

func (b *delegationBridge) onCancel(data any) {
	value, ok := asObject(data)
	if !ok {
		return
	}
	requestID, ok := validDelegationID(value.vals["requestId"])
	if !ok || !hasStructuredDelegationMarker(data) {
		return // a legacy cancel names no controller: legacy requests are rejected before one exists
	}
	for _, k := range value.keys {
		if k != "requestId" && k != "ownerRunId" && k != "nodeId" {
			return
		}
	}
	ownerRunID, ok1 := validDelegationID(value.vals["ownerRunId"])
	nodeID, ok2 := validDelegationID(value.vals["nodeId"])
	if !ok1 || !ok2 {
		return
	}
	key := delegationKey(requestID, ownerRunID, nodeID)
	b.mu.Lock()
	defer b.mu.Unlock()
	if a := b.attempts[key]; a != nil {
		a.cancel()
		return
	}
	b.rememberIdentity(b.pendingAttemptCancels, key)
}

func (b *delegationBridge) onRequest(data any) {
	if !hasStructuredDelegationMarker(data) {
		b.rejectLegacy(data)
		return
	}
	parsed := parseSubagentDelegationRequest(data)
	if !parsed.OK {
		b.rejectInvalid(parsed)
		return
	}
	request := parsed.Request
	requestID, ownerRunID, nodeID := parsed.RequestID, parsed.OwnerRunID, parsed.NodeID
	key := delegationKey(requestID, ownerRunID, nodeID)
	nodeKey := delegationKey(ownerRunID, nodeID)
	identity := func(extra map[string]any) map[string]any {
		m := map[string]any{"requestId": requestID, "ownerRunId": ownerRunID, "nodeId": nodeID}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}

	b.mu.Lock()
	if b.attempts[key] != nil || b.settledAttempts[key] {
		b.mu.Unlock()
		return
	}
	if b.pendingAttemptCancels[key] {
		delete(b.pendingAttemptCancels, key)
		b.mu.Unlock()
		b.emitTerminal(key, identity(map[string]any{"status": "cancelled"}))
		return
	}
	if b.identitySaturated {
		b.mu.Unlock()
		b.opts.Events.Emit(subagentDelegationResponseEvent, identity(map[string]any{"status": "unavailable_context", "error": "Delegation identity capacity is exhausted for this extension context."}))
		return
	}
	if _, active := b.activeOwnedNodes[nodeKey]; active {
		b.mu.Unlock()
		b.emitTerminal(key, identity(map[string]any{"status": "duplicate_node"}))
		return
	}
	b.mu.Unlock()

	bridgeCtx, ok := b.opts.GetContext()
	if !ok {
		b.emitTerminal(key, identity(map[string]any{"status": "unavailable_context", "error": "No active extension context for delegated subagent execution."}))
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	attempt := &delegationAttempt{cancel: cancel, ctx: ctx}
	b.mu.Lock()
	b.attempts[key] = attempt
	b.activeOwnedNodes[nodeKey] = delegationOwnedNode{attemptKey: key, attempt: attempt}
	b.mu.Unlock()

	b.opts.Events.Emit(subagentDelegationStartedEvent, map[string]any{"requestId": requestID, "ownerRunId": ownerRunID, "nodeId": nodeID})

	// The original awaits the executor inside the handler; here the executor runs on its own goroutine, so the emitter is not
	// held until the delegation ends.
	go b.run(request, key, nodeKey, attempt, bridgeCtx, identity)
}

func (b *delegationBridge) owns(key string, a *delegationAttempt) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return !b.disposed && b.attempts[key] == a
}

// settle ends an attempt: it frees the attempt and its owned node and, when the attempt still owns its identity, marks it
// settled in the same critical section, so a terminal event is never visible while the node is still taken and a
// retransmission cannot start the attempt again. It reports whether the terminal response is still to be emitted.
func (b *delegationBridge) settle(key, nodeKey string, attempt *delegationAttempt) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	owned := !b.disposed && b.attempts[key] == attempt
	if b.attempts[key] == attempt {
		delete(b.attempts, key)
	}
	if b.activeOwnedNodes[nodeKey].attempt == attempt {
		delete(b.activeOwnedNodes, nodeKey)
	}
	if !owned || b.settledAttempts[key] {
		return false
	}
	b.rememberIdentity(b.settledAttempts, key)
	return true
}

func (b *delegationBridge) run(request map[string]any, key, nodeKey string, attempt *delegationAttempt, bridgeCtx any, identity func(map[string]any) map[string]any) {
	defer attempt.cancel()
	execute := b.opts.ExecuteStructured
	if execute == nil {
		execute = b.opts.Execute
	}
	var last *delegationUpdate
	var updateMu sync.Mutex
	var result *bridgeResult
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("%v", r)
			}
		}()
		result, err = execute(request["requestId"].(string), toSubagentDelegationExecutionParams(request), attempt.ctx, bridgeCtx, func(update *bridgeResult) {
			if !b.owns(key, attempt) {
				return
			}
			payload := toSubagentDelegationUpdate(update)
			if payload == nil {
				return
			}
			updateMu.Lock()
			if last != nil && sameDelegationUpdateProgress(last, payload) {
				updateMu.Unlock()
				return
			}
			last = payload
			updateMu.Unlock()
			b.opts.Events.Emit(subagentDelegationUpdateEvent, payload.payload(request))
		})
	}()
	aborted := attempt.ctx.Err() != nil
	var response map[string]any
	if err != nil {
		response = identity(map[string]any{"status": "failed"})
		if aborted {
			response["status"] = "cancelled"
		} else {
			response["error"] = err.Error()
		}
	} else {
		response = toSubagentDelegationResponse(request, result, aborted)
	}
	if b.settle(key, nodeKey, attempt) {
		b.opts.Events.Emit(subagentDelegationResponseEvent, response)
	}
}

func (b *delegationBridge) rejectInvalid(parsed delegationParseResult) {
	b.mu.Lock()
	disposed := b.disposed
	b.mu.Unlock()
	if disposed || parsed.RequestID == "" {
		return
	}
	payload := map[string]any{"requestId": parsed.RequestID, "status": "invalid_request", "error": parsed.Error}
	putIf(payload, "ownerRunId", parsed.OwnerRunID)
	putIf(payload, "nodeId", parsed.NodeID)
	if parsed.OwnerRunID != "" && parsed.NodeID != "" {
		key := delegationKey(parsed.RequestID, parsed.OwnerRunID, parsed.NodeID)
		b.mu.Lock()
		running := b.attempts[key] != nil
		b.mu.Unlock()
		if !running {
			b.emitTerminal(key, payload)
		}
		return
	}
	b.opts.Events.Emit(subagentDelegationResponseEvent, payload)
}

// rejectLegacy answers an unversioned prompt-template request: the direct delegation fallback was removed.
func (b *delegationBridge) rejectLegacy(data any) {
	o, ok := asObject(data)
	if !ok {
		return
	}
	if _, tasks := delegationField(o, "tasks"); tasks || o.has("worktree") {
		if id, isStr := o.vals["requestId"].(string); isStr && id != "" {
			b.opts.Events.Emit(subagentDelegationResponseEvent, map[string]any{"requestId": id, "messages": []any{}, "isError": true,
				"errorText": "Legacy prompt-template tasks/worktree orchestration was removed; use workflowScript."})
			return
		}
	}
	legacy := map[string]any{}
	for _, f := range []string{"requestId", "agent", "task", "model", "cwd"} {
		s, isStr := o.vals[f].(string)
		if !isStr || s == "" {
			return
		}
		legacy[f] = s
	}
	if c := o.vals["context"]; c != "fresh" && c != "fork" {
		return
	}
	legacy["context"] = o.vals["context"]
	legacy["messages"] = []any{}
	legacy["isError"] = true
	legacy["errorText"] = "Legacy prompt-template direct delegation was removed; use workflowScript through the subagent tool or structured delegation."
	b.opts.Events.Emit(subagentDelegationResponseEvent, legacy)
}

// cancelAll aborts every attempt and forgets the identity facts.
func (b *delegationBridge) cancelAll() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, a := range b.attempts {
		a.cancel()
	}
	b.attempts = map[string]*delegationAttempt{}
	b.pendingAttemptCancels = map[string]bool{}
	b.activeOwnedNodes = map[string]delegationOwnedNode{}
	b.settledAttempts = map[string]bool{}
	b.identitySaturated = false
}

// dispose unsubscribes the bridge and aborts what it runs.
func (b *delegationBridge) dispose() {
	b.mu.Lock()
	b.disposed = true
	for _, a := range b.attempts {
		a.cancel()
	}
	b.attempts = map[string]*delegationAttempt{}
	subs := b.subscriptions
	b.subscriptions = nil
	b.pendingAttemptCancels = map[string]bool{}
	b.activeOwnedNodes = map[string]delegationOwnedNode{}
	b.settledAttempts = map[string]bool{}
	b.mu.Unlock()
	for _, u := range subs {
		u()
	}
}
