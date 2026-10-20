// Package pi_subagents is a Go port (partial) of pi-subagents: delegation to child sessions. It ports the agent and chain
// definitions, their discovery, the `subagent` tool's list and get actions, and the tool-activation loader; see port/PORT.md.
package pi_subagents

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const (
	extensionID   = "pi-subagents"
	loaderName    = "subagents_enable"
	subagentName  = "subagent"
	asyncWidget   = "subagent-async"
	promptSnippet = "For operator-requested delegation, use subagents; compose multi-child work in one workflow call."
)

//go:embed tooldefs.json
var tooldefsJSON []byte

type toolDef struct {
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

func toolDefs() map[string]toolDef {
	var defs map[string]toolDef
	if err := json.Unmarshal(tooldefsJSON, &defs); err != nil {
		panic("pi-subagents: tooldefs.json: " + err.Error())
	}
	return defs
}

// nextSelection is the active tool list after subagents_enable (include) or at session start: the subagent tool added or
// removed, the loader kept, no duplicates (the original's setSelection).
func nextSelection(active []string, include bool) []string {
	var next []string
	for _, n := range active {
		if include || n != subagentName {
			next = append(next, n)
		}
	}
	if include {
		next = append(next, subagentName)
	}
	has := false
	for _, n := range next {
		has = has || n == loaderName
	}
	if !has {
		next = append(next, loaderName)
	}
	seen := map[string]bool{}
	uniq := []string{}
	for _, n := range next {
		if !seen[n] {
			seen[n] = true
			uniq = append(uniq, n)
		}
	}
	return uniq
}

func setSelection(ctx sdk.Context, include bool) error {
	active, err := ctx.GetActiveTools()
	if err != nil {
		return err
	}
	ctx.SetActiveTools(nextSelection(active, include))
	return nil
}

func textResult(text string, isError bool) sdk.ToolResult {
	return sdk.ToolResult{Content: text, IsError: isError}
}

// extensionHooks add what a slice registers (tools, commands, events, flags) to the extension. A slice appends its hook from
// an init() in its own file, so no slice edits Extension() for another.
var extensionHooks []func(e *sdk.Extension)

// Extension returns the subagents extension: in a child process started with a role (childRoleEnv) only the tools of that role
// (child_role.go), otherwise the parent's tools and commands.
func Extension() *sdk.Extension {
	if role := childRole(); role != nil {
		return childExtension(role)
	}
	e := sdk.New(extensionID)
	defs := toolDefs()

	// The supervisor channel is declared so the model sees the same tools, and refuses (refuse.go); bg_wait is the
	// background runs' (async_actions.go).
	e.RegisterTool(sdk.ToolDefinition{
		Name: loaderName, Label: "Enable Subagents", Description: defs[loaderName].Description, Parameters: defs[loaderName].Parameters,
		PromptSnippet: "pi-subagents is installed. For authorized specialist, independent-review, or parallel work, call subagents_enable, then subagent. Authorization must come from the current request or applicable instructions; complexity alone is not authorization.",
		Execute: func(ctx sdk.Context, _ map[string]any) (any, error) {
			if err := setSelection(ctx, true); err != nil {
				return sdk.ToolResult{Content: "Activation failed: " + err.Error(), IsError: true, Details: map[string]any{"missing": []string{subagentName}}}, nil
			}
			active, _ := ctx.GetActiveTools()
			ok := false
			for _, n := range active {
				ok = ok || n == subagentName
			}
			if !ok {
				return sdk.ToolResult{Content: "Activation failed: subagent.", IsError: true, Details: map[string]any{"missing": []string{subagentName}}}, nil
			}
			return sdk.ToolResult{
				Content: "Enabled: subagent. On the next model request, call subagent({action:\"list\",capabilities:true}) for current capabilities. Some providers, such as bridges to another agent SDK, fix the tool list for a whole prompt. With those, subagent appears only after the next user prompt, so do not retry it in this one. The operator can start Pi with --exclude-tools subagents_enable to keep subagent always available.",
				Details: map[string]any{"enabled": []string{subagentName}},
			}, nil
		},
	})
	e.RegisterTool(sdk.ToolDefinition{Name: "subagent_supervisor", Label: "Subagent Supervisor", Description: defs["subagent_supervisor"].Description, Parameters: defs["subagent_supervisor"].Parameters,
		Execute: refuseTool("the supervisor channel (`subagent_supervisor`)")})
	e.RegisterTool(sdk.ToolDefinition{
		Name: subagentName, Label: "Subagent", Description: defs[subagentName].Description, Parameters: defs[subagentName].Parameters,
		PromptSnippet:    promptSnippet,
		PromptGuidelines: []string{"Do not invoke subagents unless the operator requested delegation directly or through applicable instructions."},
		Execute:          executeSubagent,
	})

	e.Command("run-chain", "Run a .chain.md chain: /run-chain <chain> <task> (Go port addition)", runChainCommand)

	e.OnEvent(sdk.EventSessionStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		// the subagent tool stays out of the model's tools until subagents_enable is called
		_ = setSelection(ctx, false)
		// The original asks git for the repository root while it starts (recovery of its worktrees); the port probes the
		// same way and has no worktrees to recover.
		_, _ = ctx.Exec("git", []string{"-C", ctx.Cwd(), "rev-parse", "--show-toplevel"})
		_ = ctx.SetWidget(asyncWidget, nil)
		return nil, nil
	})
	e.OnEvent(sdk.EventBeforeAgentStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		active, err := ctx.GetActiveTools()
		if err != nil {
			return nil, nil
		}
		for _, n := range active {
			if n == loaderName {
				return nil, nil
			}
		}
		ctx.SetActiveTools(append(active, loaderName))
		return nil, nil
	})
	e.OnEvent(sdk.EventSessionShutdown, func(ctx sdk.Context, _ map[string]any) (any, error) {
		_ = ctx.SetWidget(asyncWidget, nil)
		return nil, nil
	})
	for _, hook := range extensionHooks {
		hook(e)
	}
	return e
}

// executeSubagent runs a management action or one agent. What the port does not run is refused first (refuse.go); the public
// boundary (policy_publicexec.go) then checks and normalizes the call with the original's messages.
func executeSubagent(ctx sdk.Context, params map[string]any) (any, error) {
	action := ""
	if v := params["action"]; v != nil {
		s, isString := v.(string)
		if !isString || jsTrim(s) == "" {
			return nil, sdk.NewToolError("action must be a non-empty management/control action, or omit action and use workflowScript.")
		}
		action = jsTrim(s)
	}
	if msg := refuseUnsupported(params, action); msg != "" {
		return nil, sdk.NewToolError(msg)
	}
	norm := normalizePublicSubagentExecution(params)
	if !norm.OK {
		return nil, sdk.NewToolError(norm.Error)
	}
	if action == "" {
		return runOne(ctx, norm.Params)
	}
	return actions[action].run(ctx, norm.Params)
}

// explicitOK is a successful result that says so (the original's tool results carry isError: false).
type explicitOK struct {
	Content string
	Details any
}

func (r explicitOK) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Content string `json:"content"`
		Details any    `json:"details,omitempty"`
		IsError bool   `json:"is_error"`
	}{r.Content, r.Details, false})
}

// cancellation closes when the request is cancelled or the run is aborted (the user pressed Escape, the session ended):
// a child still running then is stopped.
func cancellation(ctx sdk.Context) (<-chan struct{}, func()) {
	var run <-chan struct{}
	if sig := ctx.Signal(); sig != nil {
		run = sig.Done()
	}
	return eitherDone(ctx.Done(), run)
}

// findChain returns the chain called name; of several with that name (user, then project) the last one wins.
func findChain(chains []ChainConfig, name string) *ChainConfig {
	var found *ChainConfig
	for i := range chains {
		if chains[i].Name == name || chains[i].LocalName == name {
			found = &chains[i]
		}
	}
	return found
}

// runOne is the single-agent run: {agent, task}, the original's one-child form.
func runOne(ctx sdk.Context, params map[string]any) (any, error) {
	if params["agent"] == nil && params["task"] == nil {
		return nil, sdk.NewToolError("Execution requires either { agent, task? } for one child, a named workflow resource, or a non-empty workflowScript or workflowScriptPath for orchestration.")
	}
	name, _ := params["agent"].(string)
	name = jsTrim(name)
	if name == "" {
		return nil, sdk.NewToolError("Structured single-child execution requires agent to be a non-empty string.")
	}
	task, taskIsString := params["task"].(string)
	if params["task"] != nil && !taskIsString {
		return nil, sdk.NewToolError("Structured single-child task must be a string when provided.")
	}
	if task == "" {
		return nil, sdk.NewToolError(refusal("a run without `task`"))
	}
	if msg := nestedBlocked(os.Getenv); msg != "" {
		return nil, sdk.NewToolError(msg)
	}
	scope := ResolveExecutionAgentScope(params["agentScope"])
	d := DiscoverAgents(ctx.Cwd(), scope)
	a, msg := ResolveAgentName(name, d.Agents)
	if a == nil {
		if diag := blockingDiagnostic(name, nil, d.Diagnostics); diag != nil {
			msg = fmt.Sprintf("Agent '%s' has invalid configuration: %s", name, diag.Error)
		} else if msg == "" {
			msg = fmt.Sprintf("Unknown agent '%s'. Available: %s.", name, strings.Join(sortedNames(d.Agents), ", "))
		}
		return nil, sdk.NewToolError(msg)
	}
	requested, _ := params["cwd"].(string)
	cwd, err := resolveRunCwd(ctx.Cwd(), requested)
	if err != nil {
		return nil, sdk.NewToolError(err.Error())
	}
	model, _ := params["model"].(string)
	if cfg := loadAsyncConfig(); wantsAsync(params, cfg) {
		if p := asyncUnsupportedPolicyParam(params); p != "" {
			return nil, sdk.NewToolError(refusal("the `" + p + "` option of a background (async) run; pass async: false to run in the foreground"))
		}
		return bgRT.launchBackground(ctx, params, a, task, cwd, model, cfg)
	}
	pol, err := applyRunPolicy(ctx, params, a, task, cwd)
	if err != nil {
		return nil, sdk.NewToolError(err.Error())
	}
	pol.writeInputArtifact()
	done, stop := cancellation(ctx)
	defer stop()
	defer activeRunStart()()
	out, err := runAgentOpts(done, cwd, pol.Agent, pol.Task, model, runOptions{TimeoutMs: pol.TimeoutMs})
	return finishRun(pol, runOutcome{Stdout: out, Err: err})
}

// runChainCommand is `/run-chain <chain> <task>`: the chain files are the original's format; running them this way is an
// addition (the original runs chains through its scripted workflows).
func runChainCommand(ctx sdk.Context, args string) error {
	fields := strings.SplitN(jsTrim(args), " ", 2)
	if fields[0] == "" || len(fields) < 2 || jsTrim(fields[1]) == "" {
		ctx.Notify("Usage: /run-chain <chain> <task>", "error")
		return nil
	}
	if msg := nestedBlocked(os.Getenv); msg != "" {
		ctx.Notify(msg, "error")
		return nil
	}
	d := DiscoverAgentsAll(ctx.Cwd())
	chain := findChain(d.Chains, fields[0])
	if chain == nil {
		ctx.Notify(fmt.Sprintf("Unknown chain '%s'.", fields[0]), "error")
		return nil
	}
	agents := MergeAgentsForScope(ScopeBoth, d.User, d.Project, d.Builtin, d.Package)
	done, stop := cancellation(ctx)
	defer stop()
	out, err := runChain(done, ctx.Cwd(), chain, agents, jsTrim(fields[1]), func(s string) { ctx.Notify(s, "info") })
	if err != nil {
		ctx.Notify(err.Error(), "error")
		return nil
	}
	ctx.Notify(out, "info")
	return nil
}
