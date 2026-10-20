package tintinweb_subagents

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func textResult(s string) sdk.ToolResult { return sdk.ToolResult{Content: s} }

//go:embed tool_specs.json
var toolSpecsJSON []byte

//go:embed agent_guidelines.json
var agentGuidelinesJSON []byte

// The model-facing text of the three tools is the original's, extracted from the request Pi sends with the
// original loaded and from its source (port/gen-spec.py). The Agent description and its parameters are built by
// description.go for the features this port offers (features.go); the other two tools are fixed.
type toolSpec struct {
	Parameters  map[string]any `json:"parameters"`
	Description *string        `json:"description"`
}

func loadToolSpecs() map[string]toolSpec {
	var m map[string]toolSpec
	if err := json.Unmarshal(toolSpecsJSON, &m); err != nil {
		panic(err)
	}
	return m
}

// formatToolsSuffix is how a type's built-in tools read in the type list. upstream: index.ts formatToolsSuffix.
func formatToolsSuffix(c *agentConfig) string {
	tools := c.BuiltinToolNames
	if tools == nil {
		return "*"
	}
	if len(tools) == 0 {
		if (c.Isolated != nil && *c.Isolated) || c.Extensions == false {
			return "none"
		}
		return "no built-ins, extension tools only"
	}
	if len(tools) == len(builtinToolNames) {
		full := true
		for _, n := range builtinToolNames {
			if !slices.Contains(tools, n) {
				full = false
			}
		}
		if full {
			return "*"
		}
	}
	return strings.Join(tools, ", ")
}

var datedModel = regexp.MustCompile(`-\d{8}$`)

// modelLabel is a model's short name: no provider prefix, no trailing date.
func modelLabel(model string) string {
	if i := strings.LastIndex(model, "/"); i >= 0 {
		model = model[i+1:]
	}
	return datedModel.ReplaceAllString(model, "")
}

// buildTypeListText is one line per available agent type. upstream: index.ts buildTypeListText.
func buildTypeListText(r *agentRegistry) string {
	var lines []string
	for _, name := range r.availableTypes() {
		c := r.m[name]
		suffix := ""
		if c.Model != "" {
			suffix = " (" + modelLabel(c.Model) + ")"
		}
		lines = append(lines, fmt.Sprintf("- %s: %s%s (Tools: %s)", name, c.Description, suffix, formatToolsSuffix(c)))
	}
	return strings.Join(lines, "\n")
}

func (a *app) registerTools(e *sdk.Extension) {
	wd, _ := os.Getwd()
	for _, def := range a.toolDefinitions(wd) {
		e.RegisterTool(def)
	}
}

// toolDefinitions reads the settings and the custom agents of the workspace once, then builds the tools in the order
// the original registers them. The workspace is fixed for an extension process, and the description and the schema are
// fixed once pi has them. upstream: index.ts (applyAndEmitLoaded, the description picked once at registration).
func (a *app) toolDefinitions(wd string) []sdk.ToolDefinition {
	specs := loadToolSpecs()
	a.startup(wd)
	reg := currentRegistry()
	flags := descriptionFlagsNow(settingsView(), a.runtime().toolDescriptionMode)
	dc := descriptionContext{reg: reg, agentDir: agentDir(), cwd: wd, warn: descriptionWarn}
	var guidelines []string
	_ = json.Unmarshal(agentGuidelinesJSON, &guidelines)
	defs := map[string]sdk.ToolDefinition{
		"Agent": {
			Name: "Agent", Label: "Agent", Description: agentDescription(flags, dc),
			PromptSnippet:    "Launch autonomous sub-agents for complex multi-step tasks",
			PromptGuidelines: guidelines,
			Parameters:       agentSchemaFor(flags, dc, specs["Agent"]),
			Execute:          a.agentTool,
		},
		"get_subagent_result": {
			Name: "get_subagent_result", Label: "Get Agent Result", Description: *specs["get_subagent_result"].Description,
			PromptSnippet: "Check status and retrieve results from a background agent",
			Parameters:    sdk.Schema(specs["get_subagent_result"].Parameters),
			Execute:       a.getResultTool,
		},
		"steer_subagent": {
			Name: "steer_subagent", Label: "Steer Agent", Description: *specs["steer_subagent"].Description,
			PromptSnippet: "Send a steering message to redirect a running background agent",
			Parameters:    sdk.Schema(specs["steer_subagent"].Parameters),
			Execute:       a.steerTool,
		},
	}
	var out []sdk.ToolDefinition
	for _, name := range registeredToolNames(flags) {
		if def, ok := defs[name]; ok {
			out = append(out, def)
		}
	}
	return out
}

// formatMs is seconds with one decimal. upstream: ui/agent-widget.ts formatMs.
func formatMs(ms int64) string { return fmt.Sprintf("%.1fs", float64(ms)/1000) }

// formatTokens is "" for no tokens, else the count. upstream: index.ts formatLifetimeTokens, ui/agent-widget.ts formatTokens.
func formatTokens(n int) string {
	switch {
	case n <= 0:
		return ""
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM token", float64(n)/1_000_000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk token", float64(n)/1000)
	}
	return fmt.Sprintf("%d token", n)
}

// notFound is the reply for an unknown agent reference. upstream: index.ts:1974, 2756, 2839.
func notFound(ref string) sdk.ToolResult {
	return textResult(`Agent not found: "` + ref + `". It may have been cleaned up.`)
}

func displayName(a *app, typ string) string {
	if c := getAgentConfig(typ); c != nil {
		if c.DisplayName != "" {
			return c.DisplayName
		}
		return c.Name
	}
	return typ
}

// agentTool runs the Agent tool. upstream: index.ts Agent execute.
func (a *app) agentTool(ctx sdk.Context, p map[string]any) (any, error) {
	a.touch(ctx)
	prompt, _ := p["prompt"].(string)
	description, _ := p["description"].(string)
	typ, _ := p["subagent_type"].(string)
	name, _ := p["name"].(string)
	if feature := unportedCallParam(p); feature != "" {
		return nil, refusal(feature)
	}
	// Reload the agent files so a file added mid-session is picked up (a bad one only warns).
	a.reregisterAgents()
	res := resolveSpawnType(typ)
	if !res.OK {
		return textResult(res.Message), nil
	}
	reg := currentRegistry()
	resolved, fellBack := res.Type, res.FellBackFrom
	// A fallback happened when the requested type itself is not an enabled type (a blank request included).
	// upstream: index.ts:1796-1798.
	fallbackNote := ""
	if resolveEnabledTypeIn(reg, strings.TrimSpace(typ)) == "" {
		fallbackNote = `Note: Unknown agent type "` + fellBack + `" — using ` + resolved + ".\n\n"
	}
	cfg := reg.get(resolved)
	cfgModel := ""
	if cfg != nil {
		cfgModel = cfg.Model
	}
	rt := a.runtime()
	inv := resolveInvocationConfig(cfg, invocationParamsOf(p), invocationOptions{
		worktreeDisallowed: !settingsView().worktreeIsolation(), defaultRunInBackground: rt.backgroundByDefault})

	// The model: the agent file's first, the call's only fills the gap. One that does not resolve is an error for the
	// call's (the orchestrator can pick again) and a silent fallback to the parent's for the file's.
	modelReg := registryOf(ctx)
	model := ""
	var effProvider, effID string
	if inv.modelInput != "" {
		found, msg := resolveModel(inv.modelInput, modelReg)
		if msg != "" {
			if inv.modelFromParams {
				return textResult(msg), nil
			}
		} else {
			effProvider, effID = str(found["provider"]), str(found["id"])
			model = effProvider + "/" + effID
		}
	}
	if model == "" { // the model the agent will run: the parent's, unless its file names an available one
		if q := resolveDefaultModel(ctx.ModelQualified(), modelReg, cfgModel); q != "" {
			effProvider, effID, _ = strings.Cut(q, "/")
			if effID == "" {
				effProvider, effID = "", q
			}
		}
	}
	label := resolved
	if cfg != nil && cfg.DisplayName != "" {
		label = cfg.DisplayName
	}
	switch v := checkModelScope(modelScopeCheck{provider: effProvider, id: effID, cwd: ctx.Cwd(), registry: modelReg,
		callerSupplied: inv.modelFromParams, agentLabel: label, modelInput: inv.modelInput}); v.kind {
	case "error":
		return textResult(v.message), nil
	case "warn":
		ctx.Notify(v.message, "warning")
	}

	background := inv.runInBackground
	opts := spawnOptions{Name: name, Background: background, Blocking: !background}
	if !background {
		opts.Signal = ctx.Done() // the turn is interrupted: stop the agent, queued or running
	}
	r, err := a.start(ctx, resolved, prompt, description, model, inv.thinking, inv.maxTurns, inv.isolated, opts)
	if err != nil {
		return nil, err
	}
	if background {
		joinMode := resolveJoinMode(rt.defaultJoinMode, true)
		a.mgr.mu.Lock()
		r.JoinMode, r.ToolCallID = joinMode, ctx.ToolCallID()
		queued, limit := r.Status == statusQueued, a.mgr.maxConcurrent
		a.mgr.mu.Unlock()
		a.joinBatch(r.ID, joinMode)
		pos := ""
		if queued {
			pos = fmt.Sprintf("Position: queued (max %d concurrent)\n", limit)
		}
		_ = ctx.Events().Emit("subagents:created", map[string]any{"id": r.ID, "type": resolved, "description": description, "isBackground": true})
		state := "started"
		if queued {
			state = "queued"
		}
		return textResult(fmt.Sprintf("%sAgent %s in background.\nAgent ID: %s\nType: %s\nDescription: %s\n%s\nYou will be notified when this agent completes.\n"+
			"Use get_subagent_result to retrieve full results, or steer_subagent to send it messages.\nDo not duplicate this agent's work.",
			fallbackNote, state, r.ID, displayName(a, resolved), description, pos)), nil
	}
	// Foreground: the call blocks. It is charged to the foreground pool (maxConcurrentForeground, unlimited unless
	// set); a queued call waits for its slot, and an aborted one comes back as a stopped result, never an error.
	_ = ctx.Events().Emit("subagents:created", map[string]any{"id": r.ID, "type": resolved, "description": description, "isBackground": false})
	a.mgr.wait(r)
	a.mgr.mu.Lock()
	status, result, errText, tokens, uses, dur := r.Status, r.Result, r.Error, r.Tokens, r.ToolUses, r.CompletedAt.Sub(r.StartedAt).Milliseconds()
	a.mgr.mu.Unlock()
	if status == statusError {
		return textResult(fmt.Sprintf("%sAgent failed: %s%s", fallbackNote, errText, partialOutputSuffix(result))), nil
	}
	stats := []string{fmt.Sprintf("%d tool uses", uses)}
	if t := formatTokens(tokens); t != "" {
		stats = append(stats, t)
	}
	out := strings.TrimSpace(result)
	if out == "" {
		out = "No output."
	}
	return textResult(fmt.Sprintf("%sAgent completed in %s (%s)%s.\n\n%s", fallbackNote, formatMs(dur), strings.Join(stats, ", "), foregroundOutcomeNote(status), out)), nil
}

// invocationParamsOf reads the Agent call's parameters an agent file can also set.
func invocationParamsOf(p map[string]any) invocationParams {
	out := invocationParams{Model: str(p["model"]), Thinking: str(p["thinking"]), Isolation: p["isolation"]}
	if f, ok := p["max_turns"].(float64); ok {
		n := int(f)
		out.MaxTurns = &n
	}
	for key, dst := range map[string]**bool{"run_in_background": &out.RunInBackground, "inherit_context": &out.InheritContext, "isolated": &out.Isolated} {
		if b, ok := p[key].(bool); ok {
			*dst = &b
		}
	}
	return out
}

// getResultTool upstream: index.ts get_subagent_result execute.
func (a *app) getResultTool(ctx sdk.Context, p map[string]any) (any, error) {
	id, _ := p["agent_id"].(string)
	r := a.mgr.resolve(id)
	if r == nil || !r.topLevel() {
		return notFound(id), nil
	}
	// A wait is cancelled with the call and nothing else: the agent keeps running and stays unconsumed, so its
	// completion notification can still be delivered. A queued agent is waited out through the queue too.
	if wait, _ := p["wait"].(bool); wait {
		a.mgr.mu.Lock()
		active := r.Status == statusRunning || r.Status == statusQueued
		a.mgr.mu.Unlock()
		if active {
			select {
			case <-r.done:
			case <-ctx.Done():
				return nil, context.Canceled
			}
		}
	}
	a.mgr.mu.Lock()
	status, result, errText := r.Status, r.Result, r.Error
	uses, tokens := r.ToolUses, r.Tokens
	start, end := r.StartedAt, r.CompletedAt
	a.mgr.mu.Unlock()
	// upstream: ui/agent-widget.ts formatDuration (a running agent's time so far, marked).
	duration := formatMs(end.Sub(start).Milliseconds())
	if end.IsZero() {
		duration = formatMs(time.Since(start).Milliseconds()) + " (running)"
	}
	stats := []string{fmt.Sprintf("Tool uses: %d", uses)}
	if t := formatTokens(tokens); t != "" {
		stats = append(stats, t)
	}
	stats = append(stats, "Duration: "+duration)
	out := fmt.Sprintf("Agent: %s\nType: %s | Status: %s%s | %s\nDescription: %s\n\n", r.ID, displayName(a, r.Type), status, statusNote(status), strings.Join(stats, " | "), r.Description)
	switch status {
	case statusRunning:
		out += "Agent is still running. Use wait: true or check back later."
	case statusError:
		out += "Error: " + errText + partialOutputSuffix(result)
	default:
		if t := strings.TrimSpace(result); t != "" {
			out += t
		} else {
			out += "No output."
		}
	}
	if status != statusRunning && status != statusQueued {
		a.mgr.mu.Lock()
		r.Consumed = true
		a.mgr.mu.Unlock()
		a.cancelNudge(id)
	}
	return textResult(out), nil
}

// steerTool upstream: index.ts steer_subagent execute.
func (a *app) steerTool(ctx sdk.Context, p map[string]any) (any, error) {
	id, _ := p["agent_id"].(string)
	msg, _ := p["message"].(string)
	r := a.mgr.resolve(id)
	if r == nil || !r.topLevel() {
		return notFound(id), nil
	}
	a.mgr.mu.Lock()
	status := r.Status
	a.mgr.mu.Unlock()
	if status != statusRunning {
		return textResult(`Agent "` + id + `" is not running (status: ` + status + `). Cannot steer a non-running agent.`), nil
	}
	queued, err := a.mgr.steer(r, msg)
	if err != nil {
		return textResult("Failed to steer agent: " + err.Error()), nil
	}
	_ = ctx.Events().Emit("subagents:steered", map[string]any{"id": r.ID, "message": msg})
	if queued { // the child does not exist yet: delivered, in order, once it starts
		return textResult("Steering message queued for agent " + r.ID + ". It will be delivered once the session initializes."), nil
	}
	a.mgr.mu.Lock()
	uses, tokens := r.ToolUses, r.Tokens
	a.mgr.mu.Unlock()
	var state []string
	if t := formatTokens(tokens); t != "" {
		state = append(state, t)
	}
	use := "uses"
	if uses == 1 {
		use = "use"
	}
	state = append(state, fmt.Sprintf("%d tool %s", uses, use))
	return textResult(fmt.Sprintf("Steering message sent to agent %s. The agent will process it after its current tool execution.\nCurrent state: %s", r.ID, strings.Join(state, " · "))), nil
}

// agentsCommand lists the agent types and the agents of this session.
func (a *app) agentsCommand(ctx sdk.Context, _ string) error {
	a.touch(ctx)
	reg := currentRegistry()
	lines := []string{"Agent types:"}
	for _, name := range reg.keys {
		c := reg.m[name]
		state := ""
		if !c.Enabled {
			state = " (disabled)"
		}
		src := "default"
		if !c.IsDefault {
			src = c.Source
		}
		lines = append(lines, fmt.Sprintf("  %s [%s]%s — %s", name, src, state, firstLine(c.Description)))
	}
	recs := a.mgr.list()
	lines = append(lines, "", "Agents:")
	if len(recs) == 0 {
		lines = append(lines, "  (none)")
	}
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].StartedAt.Before(recs[j].StartedAt) })
	for _, r := range recs {
		a.mgr.mu.Lock()
		lines = append(lines, fmt.Sprintf("  %s %s %s — %s", r.ID, displayName(a, r.Type), r.Status, r.Description))
		a.mgr.mu.Unlock()
	}
	ctx.Notify(strings.Join(lines, "\n"), "info")
	return nil
}

func firstLine(s string) string {
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[:i]
	}
	if u := []rune(s); len(u) > 100 {
		s = string(u[:100]) + "…"
	}
	return s
}

var _ = context.Background
