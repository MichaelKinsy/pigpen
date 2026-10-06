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

//go:embed agent_description.txt
var agentDescriptionTemplate string

//go:embed tool_specs.json
var toolSpecsJSON []byte

//go:embed agent_guidelines.json
var agentGuidelinesJSON []byte

// The model-facing text of the three tools is the original's, extracted from the request Pi sends with the
// original loaded (port/gen-spec.py). The original's workflow, scheduling and worktree-isolation parts are left
// out, as they are when those three features are switched off in its settings.
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

func agentToolDescription(r *agentRegistry) string {
	return strings.NewReplacer("{{TYPELIST}}", buildTypeListText(r), "{{AGENTDIR}}", agentDir()).Replace(agentDescriptionTemplate)
}

// agentToolSchema is the Agent parameters with the available types and the agent dir filled in.
func agentToolSchema(r *agentRegistry, spec toolSpec) sdk.Schema {
	raw, _ := json.Marshal(spec.Parameters)
	text := strings.NewReplacer("{{TYPES}}", strings.Join(r.availableTypes(), ", "), "{{AGENTDIR}}", agentDir()).Replace(string(raw))
	var out sdk.Schema
	_ = json.Unmarshal([]byte(text), &out)
	return out
}

func (a *app) registerTools(e *sdk.Extension) {
	specs := loadToolSpecs()
	// The workspace is fixed for an extension process, so the custom agents can be read before the tool is registered.
	if wd, err := os.Getwd(); err == nil {
		a.reload(wd)
	}
	reg := currentRegistry()
	var guidelines []string
	_ = json.Unmarshal(agentGuidelinesJSON, &guidelines)
	e.RegisterTool(sdk.ToolDefinition{
		Name: "Agent", Label: "Agent", Description: agentToolDescription(reg),
		PromptSnippet:    "Launch autonomous sub-agents for complex multi-step tasks",
		PromptGuidelines: guidelines,
		Parameters:       agentToolSchema(reg, specs["Agent"]),
		Execute:          a.agentTool,
	})
	e.RegisterTool(sdk.ToolDefinition{
		Name: "get_subagent_result", Label: "Get Agent Result", Description: *specs["get_subagent_result"].Description,
		PromptSnippet: "Check status and retrieve results from a background agent",
		Parameters:    sdk.Schema(specs["get_subagent_result"].Parameters),
		Execute:       a.getResultTool,
	})
	e.RegisterTool(sdk.ToolDefinition{
		Name: "steer_subagent", Label: "Steer Agent", Description: *specs["steer_subagent"].Description,
		PromptSnippet: "Send a steering message to redirect a running background agent",
		Parameters:    sdk.Schema(specs["steer_subagent"].Parameters),
		Execute:       a.steerTool,
	})
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
	model, _ := p["model"].(string)
	thinking, _ := p["thinking"].(string)
	maxTurns := 0
	if f, ok := p["max_turns"].(float64); ok {
		maxTurns = int(f)
	}
	if v, _ := p["inherit_context"].(bool); v {
		return textResult("inherit_context is not supported by this port: an agent runs as a separate pig process and cannot fork this conversation. Put what it needs to know into the prompt."), nil
	}
	isolated, _ := p["isolated"].(bool)
	if resume, _ := p["resume"].(string); resume != "" {
		prev := a.mgr.resolve(resume)
		if prev == nil {
			return notFound(resume), nil
		}
		a.mgr.mu.Lock()
		status, result := prev.Status, prev.Result
		a.mgr.mu.Unlock()
		if status == statusRunning || status == statusQueued {
			return textResult(fmt.Sprintf("Agent %s is still running; use steer_subagent to reach it, or wait for it to finish before resuming.", prev.ID)), nil
		}
		// A child's session is not kept, so a resumed agent starts afresh with its earlier answer in the prompt.
		prompt = fmt.Sprintf("You are continuing earlier work. Your previous run ended with this answer:\n\n%s\n\nContinue with this: %s", strings.TrimSpace(result), prompt)
	}
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
	background := true
	if cfg := reg.get(resolved); cfg != nil && cfg.RunInBackground != nil {
		background = *cfg.RunInBackground
	}
	if b, ok := p["run_in_background"].(bool); ok {
		background = b
	}
	r, err := a.start(ctx, resolved, prompt, description, name, model, thinking, maxTurns, background, isolated)
	if err != nil {
		return nil, err
	}
	if background {
		a.mgr.mu.Lock()
		r.ToolCallID = ctx.ToolCallID()
		queued, limit := r.Status == statusQueued, a.mgr.maxConcurrent
		a.mgr.mu.Unlock()
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
	select {
	case <-r.done:
	case <-ctx.Done():
		a.mgr.abort(r)
		<-r.done
	}
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

// getResultTool upstream: index.ts get_subagent_result execute.
func (a *app) getResultTool(ctx sdk.Context, p map[string]any) (any, error) {
	id, _ := p["agent_id"].(string)
	r := a.mgr.resolve(id)
	if r == nil {
		return notFound(id), nil
	}
	if wait, _ := p["wait"].(bool); wait {
		select {
		case <-r.done:
		case <-ctx.Done():
			return textResult(fmt.Sprintf("Agent %s is still running (the wait was cancelled).", r.ID)), nil
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
	case statusRunning, statusQueued:
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
		a.mu.Lock()
		if t := a.timers[r.ID]; t != nil {
			t.Stop()
			delete(a.timers, r.ID)
		}
		a.mu.Unlock()
	}
	return textResult(out), nil
}

// steerTool upstream: index.ts steer_subagent execute.
func (a *app) steerTool(ctx sdk.Context, p map[string]any) (any, error) {
	id, _ := p["agent_id"].(string)
	msg, _ := p["message"].(string)
	r := a.mgr.resolve(id)
	if r == nil {
		return notFound(id), nil
	}
	a.mgr.mu.Lock()
	status := r.Status
	a.mgr.mu.Unlock()
	if status != statusRunning {
		return textResult(`Agent "` + id + `" is not running (status: ` + status + `). Cannot steer a non-running agent.`), nil
	}
	if err := a.mgr.steer(r, msg); err != nil {
		return textResult("Failed to steer agent: " + err.Error()), nil
	}
	_ = ctx.Events().Emit("subagents:steered", map[string]any{"id": r.ID, "message": msg})
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
