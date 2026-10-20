package tintinweb_subagents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Nested delegation: an agent whose file says `allowed_subagents` can start agents of its own. upstream:
// src/nested-tools.ts, agent-runner.ts:843-866, agent-manager.ts (parentAgentId, depth, abortOwnedChildren).
//
// Design (PORT.md D6). An agent here is a child `pig --mode rpc` process, so "the same manager" of the original
// does not exist across levels. Each level runs its own manager instead:
//
//   - The parent's runner starts a child that may delegate with its place in the tree in the environment: its depth
//     (TINTINWEB_SUBAGENT_DEPTH, a top-level agent's child is 1), the cap of the branch, its own agent id, the agent
//     file's allowed_subagents, the config root, and a usage file (childEnv).
//   - The child loads this same extension, which sees TINTINWEB_SUBAGENT_DEPTH and registers the three nested tools
//     (below) in place of the top-level ones, and nothing else: no RPC, no /agents, no events, no notifications.
//   - The nested tools spawn the child's own children through the child's own manager and runner, one process level
//     deeper. A record in that manager is owned by that agent (ParentID), so ownership is checked as in the original,
//     and what another agent owns is not even in the map: "not found or not owned by this parent".
//   - Stopping reaches down the tree. A parent's settle (the child's `agent_settled`) and its shutdown (pig runs the
//     extensions' session_shutdown on SIGTERM and on the end of its input) abort the children it owns; each aborted
//     child is a process group of its own that the runner signals, and its own shutdown does the same one level down.
//   - Spend: a nested child's tokens are written to the owner's usage file, which the owner's runner adds to the
//     owner's own count when it settles; so the top-level record counts the whole tree. upstream: onAssistantUsage.

var nestedToolNames = []string{"Agent", "get_subagent_result", "steer_subagent"}

// The branch cap. upstream: nested-tools.ts maxSubagentDepth: main session = 0, its subagents = 1, their children = 2.
var maxSubagentDepth atomic.Int64

func init() { maxSubagentDepth.Store(2) }

func getMaxSubagentDepth() int { return int(maxSubagentDepth.Load()) }

// setMaxSubagentDepth floors the value and never stores a negative one: a negative cap would make `depth >= max` true
// everywhere and switch nesting off with no error. upstream: setMaxSubagentDepth.
func setMaxSubagentDepth(n float64) {
	maxSubagentDepth.Store(int64(math.Max(0, math.Floor(n))))
}

// nestedConfig is what a child that may delegate knows about its place. upstream: NestedToolContext.
type nestedConfig struct {
	AgentID   string // the agent this extension runs in: the owner of the children it spawns
	Depth     int    // this agent's depth
	MaxDepth  int    // the branch's cap
	Allowed   any    // "all" or a non-empty []string
	ConfigCwd string // root for agent discovery; may differ from the working directory
	UsageFile string // where the tokens the owned children spent are written ("" when nobody reads them)

	tokens atomic.Int64 // spent by the children this agent owned
}

// toolsEnabled: at or past the cap an agent can never spawn, so it can never own a child to fetch from or steer
// either; it gets no nested tools. upstream: agent-runner.ts:852.
func (n *nestedConfig) toolsEnabled() bool { return n.Allowed != nil && n.Depth < n.MaxDepth }

// nestedConfigFromEnv reads childEnv's variables; nil unless the child was given an allowance.
func nestedConfigFromEnv() *nestedConfig {
	raw := os.Getenv(allowedEnv)
	if raw == "" {
		return nil
	}
	var allowed any
	if json.Unmarshal([]byte(raw), &allowed) != nil {
		return nil
	}
	switch v := allowed.(type) {
	case string:
		if v != "all" {
			return nil
		}
	case []any:
		list := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok {
				list = append(list, s)
			}
		}
		if len(list) == 0 {
			return nil
		}
		allowed = list
	default:
		return nil
	}
	depth, _ := strconv.Atoi(os.Getenv(depthEnv))
	maxDepth, _ := strconv.Atoi(os.Getenv(maxDepthEnv))
	cwd := os.Getenv(configCwdEnv)
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	return &nestedConfig{AgentID: os.Getenv(agentIDEnv), Depth: depth, MaxDepth: maxDepth, Allowed: allowed, ConfigCwd: cwd, UsageFile: os.Getenv(usageEnv)}
}

// nestedCall is what a nested tool needs from the call: its cancellation and the calling session's view.
type nestedCall struct {
	Done <-chan struct{}
	View parentView
}

func (a *app) callOf(ctx sdk.Context) nestedCall {
	return nestedCall{Done: ctx.Done(), View: a.viewOf(ctx)}
}

// registerNested gives a child the nested tools and the hooks that keep what it owns from outliving it.
func (a *app) registerNested(e *sdk.Extension, n *nestedConfig) {
	a.nested = n
	if wd, err := os.Getwd(); err == nil {
		a.reload(wd)
	}
	for _, def := range a.nestedToolDefs(n) {
		e.RegisterTool(def)
	}
	e.OnEvent(sdk.EventSessionStart, func(ctx sdk.Context, _ map[string]any) (any, error) { a.touch(ctx); return nil, nil })
	e.OnEvent(sdk.EventBeforeAgentStart, func(ctx sdk.Context, _ map[string]any) (any, error) { a.touch(ctx); return nil, nil })
	// The agent's run is over: what it owns is hidden and only it could read it, so it stops.
	e.OnEvent(sdk.EventAgentSettled, func(sdk.Context, map[string]any) (any, error) { a.nestedSettled(); return nil, nil })
	e.OnSessionShutdown(func(sdk.Context, map[string]any) (any, error) { a.nestedShutdown(); return nil, nil })
}

// nestedToolDefs are the three nested tools. upstream: nested-tools.ts createNestedSubagentTools.
func (a *app) nestedToolDefs(n *nestedConfig) []sdk.ToolDefinition {
	return []sdk.ToolDefinition{{
		Name: "Agent", Label: "Agent",
		Description: "Launch a child-safe nested subagent for bounded delegated work. " +
			"Only use agent types allowed by this parent agent; nesting is depth-limited.",
		Parameters: nestedAgentSchema(n.availableIn(a.branchRegistry(n))),
		Execute: func(ctx sdk.Context, p map[string]any) (any, error) {
			a.touch(ctx)
			return a.nestedAgent(a.callOf(ctx), p)
		},
	}, {
		Name: "get_subagent_result", Label: "Get Nested Agent Result",
		Description: "Check or wait for a background nested agent owned by this parent.",
		Parameters: sdk.Schema{"type": "object", "properties": map[string]any{
			"agent_id": map[string]any{"type": "string"}, "wait": map[string]any{"type": "boolean"}}, "required": []any{"agent_id"}},
		Execute: func(ctx sdk.Context, p map[string]any) (any, error) { return a.nestedResult(ctx.Done(), p) },
	}, {
		Name: "steer_subagent", Label: "Steer Nested Agent",
		Description: "Send guidance to a running nested agent owned by this parent.",
		Parameters: sdk.Schema{"type": "object", "properties": map[string]any{
			"agent_id": map[string]any{"type": "string"}, "message": map[string]any{"type": "string"}}, "required": []any{"agent_id", "message"}},
		Execute: func(_ sdk.Context, p map[string]any) (any, error) { return a.nestedSteer(p) },
	}}
}

// nestedSettled runs when this agent's run is over: the children it owns stop. upstream: agent-manager.ts abortOwnedChildren.
func (a *app) nestedSettled() { a.mgr.abortOwnedChildren(a.nested.AgentID) }

// nestedShutdown runs when the agent's session ends, which pig does on SIGTERM and on the end of its input too: nothing
// it started may outlive it.
func (a *app) nestedShutdown() { a.mgr.abortAll() }

// branchRegistry is the agents of this branch's config root, built for the call and never registered: the
// process-wide registry is not this branch's. upstream: nested-tools.ts loadRegistry.
func (a *app) branchRegistry(n *nestedConfig) *agentRegistry {
	custom, _ := loadCustomAgents(n.ConfigCwd)
	return buildAgentRegistry(custom)
}

// allowedTypes is the allowance as agent types of the registry, in the order the file named them; nil is "all".
func (n *nestedConfig) allowedTypes(reg *agentRegistry) []string {
	list, ok := n.Allowed.([]string)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, name := range list {
		if k, found := reg.resolveKey(name); found {
			name = k
		}
		if !inList(out, name) {
			out = append(out, name)
		}
	}
	return out
}

// availableIn is the enabled types this parent may use.
func (n *nestedConfig) availableIn(reg *agentRegistry) []string {
	allowed := n.allowedTypes(reg)
	var out []string
	for _, name := range reg.availableTypes() {
		if allowed == nil || inList(allowed, name) {
			out = append(out, name)
		}
	}
	return out
}

// nestedAgentSchema is the Agent tool's parameters. The worktree-isolation parameter is left out, as it is from the
// top-level tool: that feature is not ported. upstream: nested-tools.ts agentTool.
func nestedAgentSchema(available []string) sdk.Schema {
	list := strings.Join(available, ", ")
	if list == "" {
		list = "none"
	}
	return sdk.Schema{"type": "object", "properties": map[string]any{
		"prompt":            map[string]any{"description": "Self-contained task for the nested agent.", "type": "string"},
		"description":       map[string]any{"description": "Short 3-5 word task description.", "type": "string"},
		"subagent_type":     map[string]any{"description": "Allowed nested agent type. Available: " + list + ".", "type": "string"},
		"model":             map[string]any{"description": "Optional provider/model override.", "type": "string"},
		"thinking":          map[string]any{"description": "Optional thinking level.", "type": "string"},
		"max_turns":         map[string]any{"minimum": 1, "type": "number"},
		"run_in_background": map[string]any{"description": "Defaults to false for nested spawns — the call blocks and returns the child's result inline. Set true only for work you will collect later with get_subagent_result; a detached child is stopped when you finish.", "type": "boolean"},
		"resume":            map[string]any{"description": "Resume a nested agent owned by this parent.", "type": "string"},
		"isolated":          map[string]any{"type": "boolean"},
		"inherit_context":   map[string]any{"type": "boolean"},
	}, "required": []any{"prompt", "description", "subagent_type"}}
}

func nestedText(text string, isError bool) (sdk.ToolResult, error) {
	return sdk.ToolResult{Content: text, IsError: isError}, nil
}

// ownsRecord: the record exists and this agent owns it. upstream: ownsRecord.
func (n *nestedConfig) ownsRecord(r *agentRecord) bool { return r != nil && r.ParentID == n.AgentID }

// resultPosition is how the caller received a record, which decides the outcome wording. A foreground spawn holds the
// whole output and no id, so the note must not invite a fetch; a background child's owner holds an id and can poll.
type resultPosition int

const (
	inline resultPosition = iota
	fetched
)

// formatNestedRecord is the text of a settled record. upstream: nested-tools.ts formatRecord.
func formatNestedRecord(r *agentRecord, status, result, errText string, pos resultPosition) string {
	if status == statusError {
		if errText == "" {
			errText = "unknown error"
		}
		return "Agent failed: " + errText + partialOutputSuffix(result)
	}
	if status == statusQueued || status == statusRunning {
		return fmt.Sprintf("Agent %s is %s.", r.ID, status)
	}
	text := strings.TrimSpace(result)
	if text == "" {
		text = strings.TrimSpace(errText)
	}
	if text == "" {
		text = "No output."
	}
	note := statusNote(status)
	if pos == inline {
		note = foregroundOutcomeNote(status)
	}
	if note != "" {
		return "Nested agent" + note + ".\n\n" + text
	}
	return text
}

// snapshot reads a record's mutable fields under the manager's lock.
func (m *manager) snapshot(r *agentRecord) (status, result, errText string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return r.Status, r.Result, r.Error
}

// nestedAgent is the nested Agent tool. upstream: nested-tools.ts agentTool.execute.
func (a *app) nestedAgent(c nestedCall, p map[string]any) (sdk.ToolResult, error) {
	n := a.nested
	prompt, _ := p["prompt"].(string)
	description, _ := p["description"].(string)
	rawType, _ := p["subagent_type"].(string)
	if resume, _ := p["resume"].(string); resume != "" {
		if !n.ownsRecord(a.mgr.get(resume)) {
			return nestedText(`Nested agent not found or not owned by this parent: "`+resume+`".`, true)
		}
		return sdk.ToolResult{}, refusal("resuming a finished agent's session (resume)")
	}
	if n.Depth >= n.MaxDepth {
		return nestedText(fmt.Sprintf("Nested subagent call blocked (depth=%d, max=%d). Complete the task directly.", n.Depth, n.MaxDepth), true)
	}
	// Reloaded per call so new agent files are picked up without a restart.
	reg := a.branchRegistry(n)
	// Strict resolve, never the fallback policy: a project-level fallbackSubagent must not hand a nested caller an
	// agent its allowlist never named. The list stays filtered so a typo cannot enumerate agents this parent may not reach.
	resolved := resolveEnabledTypeIn(reg, rawType)
	if resolved == "" {
		return nestedText(fmt.Sprintf(`Unknown or disabled nested agent type: "%s". Allowed: %s.`, rawType, listOrNone(n.availableIn(reg))), true)
	}
	if allowed := n.allowedTypes(reg); allowed != nil && !inList(allowed, resolved) {
		return nestedText(fmt.Sprintf(`Nested agent type "%s" is not allowed for this parent. Allowed: %s.`, resolved, strings.Join(allowed, ", ")), true)
	}
	if feature := unportedNestedParam(p); feature != "" {
		return sdk.ToolResult{}, refusal(feature)
	}
	cfg := reg.get(resolved)
	// The agent file outranks the call, and a nested spawn is foreground unless one of them says otherwise: a detached
	// child is stopped when its parent finishes and has no notification path. upstream: resolveAgentInvocationConfig.
	background, _ := p["run_in_background"].(bool)
	if cfg.RunInBackground != nil {
		background = *cfg.RunInBackground
	}
	isolated, _ := p["isolated"].(bool)
	if cfg.Isolated != nil {
		isolated = *cfg.Isolated
	}
	model, _ := p["model"].(string)
	if cfg.Model != "" {
		model = "" // the agent file's own model is applied by the spawn
	}
	thinking, _ := p["thinking"].(string)
	if cfg.Thinking != "" {
		thinking = ""
	}
	maxTurns := 0
	if f, ok := p["max_turns"].(float64); ok {
		maxTurns = int(f)
	}
	if cfg.MaxTurns != nil {
		maxTurns = 0
	}
	r, err := a.startWith(c.View, startInput{Type: resolved, Prompt: prompt, Description: description, Model: model, Thinking: thinking,
		MaxTurns: maxTurns, Background: background, Isolated: isolated, Registry: reg,
		Owner: &owner{ParentID: n.AgentID, Depth: n.Depth + 1, MaxDepth: n.MaxDepth, ConfigCwd: n.ConfigCwd}})
	if err != nil {
		return nestedText(err.Error(), true)
	}
	if background {
		if err := a.mgr.awaitStartup(r, 30*time.Second); err != nil {
			return nestedText(err.Error(), true)
		}
		// A child that is already in error did not start: the failure is the call's, not a result to collect later.
		if status, _, errText := a.mgr.snapshot(r); status == statusError {
			return nestedText(errText, true)
		}
		return nestedText("Nested agent started in background. Agent ID: "+r.ID, false)
	}
	select {
	case <-r.done:
	case <-c.Done:
		a.mgr.abort(r)
		<-r.done
	}
	status, result, errText := a.mgr.snapshot(r)
	return nestedText(formatNestedRecord(r, status, result, errText, inline), status == statusError)
}

func listOrNone(list []string) string {
	if len(list) == 0 {
		return "none"
	}
	return strings.Join(list, ", ")
}

// unportedNestedParam names the first parameter of the nested Agent tool that asks for something not ported, or "".
// Its own list: the nested tool must refuse what it cannot do even where the top-level tool gains it.
func unportedNestedParam(p map[string]any) string {
	if v, _ := p["isolation"].(string); v != "" && v != "off" {
		return `worktree isolation (isolation: "` + v + `")`
	}
	if v, _ := p["inherit_context"].(bool); v {
		return "inheriting the parent conversation (inherit_context)"
	}
	return ""
}

// nestedResult is the nested get_subagent_result tool. upstream: nested-tools.ts resultTool.
func (a *app) nestedResult(done <-chan struct{}, p map[string]any) (sdk.ToolResult, error) {
	id, _ := p["agent_id"].(string)
	r := a.mgr.get(id)
	if !a.nested.ownsRecord(r) {
		return nestedText(`Nested agent not found or not owned by this parent: "`+id+`".`, true)
	}
	status, _, _ := a.mgr.snapshot(r)
	if wait, _ := p["wait"].(bool); wait && (status == statusQueued || status == statusRunning) {
		// Cancelling stops only this wait: the child keeps running and stays unconsumed.
		select {
		case <-r.done:
		case <-done:
			return sdk.ToolResult{}, context.Canceled
		}
	}
	status, result, errText := a.mgr.snapshot(r)
	return nestedText(formatNestedRecord(r, status, result, errText, fetched), status == statusError)
}

// nestedSteer is the nested steer_subagent tool. upstream: nested-tools.ts steerTool.
func (a *app) nestedSteer(p map[string]any) (sdk.ToolResult, error) {
	id, _ := p["agent_id"].(string)
	message, _ := p["message"].(string)
	r := a.mgr.get(id)
	if !a.nested.ownsRecord(r) {
		return nestedText(`Running nested agent not found or not owned by this parent: "`+id+`".`, true)
	}
	a.mgr.mu.Lock()
	if r.Status != statusRunning {
		a.mgr.mu.Unlock()
		return nestedText(`Running nested agent not found or not owned by this parent: "`+id+`".`, true)
	}
	// Not up yet: the steer is kept and sent when it is, as the original flushes it when the session exists.
	if r.child == nil {
		r.pendingSteers = append(r.pendingSteers, message)
		a.mgr.mu.Unlock()
		return nestedText("Steering message queued for nested agent "+id+".", false)
	}
	c := r.child
	a.mgr.mu.Unlock()
	if err := c.steer(message); err != nil {
		return nestedText("Failed to steer nested agent: "+err.Error(), true)
	}
	return nestedText("Steering message sent to nested agent "+id+".", false)
}

// accountNested adds a settled child's spend to what its owner reports upward: the owner's runner reads the usage file
// when the owner settles, and the owner's own children's spend is in the count of each child. upstream: onAssistantUsage.
func (a *app) accountNested(r *agentRecord) {
	n := a.nested
	if n == nil || n.UsageFile == "" {
		return
	}
	a.mgr.mu.Lock()
	spent := r.Tokens
	a.mgr.mu.Unlock()
	writeUsage(n.UsageFile, n.tokens.Add(int64(spent)))
}

var usageMu sync.Mutex

// writeUsage replaces the usage file's content with the total, atomically for a reader.
func writeUsage(path string, total int64) {
	usageMu.Lock()
	defer usageMu.Unlock()
	tmp := path + ".tmp"
	if os.WriteFile(tmp, []byte(strconv.FormatInt(total, 10)), 0o600) == nil {
		_ = os.Rename(tmp, path)
	}
}

// readUsage is the total in a usage file, 0 for no file or none yet.
func readUsage(path string) int {
	if path == "" {
		return 0
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0
	}
	return n
}

func removeUsage(path string) {
	if path != "" {
		_ = os.Remove(path)
		_ = os.Remove(path + ".tmp")
	}
}

var _ = errors.New
