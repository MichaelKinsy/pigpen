// Package ast ports omp's `ast_grep` and `ast_edit` tools (plus the staged-preview
// `resolve`/`reject` special commands) using an external verified `ast-grep` CLI.
//
// upstream: packages/coding-agent/src/tools/ast-grep.ts, ast-edit.ts, resolve.ts
package ast

import (
	"context"
	_ "embed"
	"fmt"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/gate"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/schemaorder"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/toolcmd"
)

//go:embed prompts/ast-grep.md
var astGrepPrompt string

//go:embed prompts/ast-edit.md
var astEditPrompt string

//go:embed prompts/resolve-reminder.md
var resolveReminderPrompt string

const resolveReminderCustomType = "resolve-reminder"
const (
	// SettingAstGrepEnabled matches omp's cfgAstGrepEnabled ("astGrep.enabled", default false).
	SettingAstGrepEnabled = "astGrep.enabled"
	// SettingAstEditEnabled matches omp's cfgAstEditEnabled ("astEdit.enabled", default true).
	SettingAstEditEnabled = "astEdit.enabled"
)

// Enabled reads a boolean setting key with default def. The settings lane may
// replace this variable with a host-backed reader; the default returns def.
var Enabled = func(_ sdk.Context, _ string, def bool) bool {
	return def
}

// PlanProposalPending reports whether a plan proposal is currently awaiting approval
// for ctx (used to append omp's `propose` hint when resolve/reject has no pending
// preview; upstream: packages/coding-agent/src/tools/resolve.ts:298-300).
// Defaults to gate.PlanActive(ctx); the integrator may override if needed.
var PlanProposalPending = func(ctx sdk.Context) bool {
	return gate.PlanActive(ctx)
}

// PendingPreview is one staged preview queued for `resolve` (apply) or `reject` (discard).
// Mirrors omp's `queueResolveHandler` options in packages/coding-agent/src/tools/resolve.ts:150-158.
type PendingPreview struct {
	Label          string
	SourceToolName string
	// Apply receives both a cancellable Go context and the live sdk.Context of the
	// `resolve` call (never a stale context from the preview call).
	Apply func(ctx context.Context, sdkCtx sdk.Context, reason string) (sdk.ToolResult, error)
	// Reject is optional; when nil, discarding returns omp's default discard message.
	Reject func(ctx context.Context, sdkCtx sdk.Context, reason string) (*sdk.ToolResult, error)
}

type manager struct {
	procs   *processTracker
	mu      sync.Mutex
	pending map[string][]*PendingPreview // sessionID -> LIFO stack (matching ToolChoiceQueue)
}

func newManager() *manager {
	return &manager{
		procs:   newProcessTracker(),
		pending: make(map[string][]*PendingPreview),
	}
}

// sharedManager is ONE package-level registry keyed by sessionID, shared across
// every Register call and every extension instance in the process. Calling
// Register again never resets live sessions' pending previews or running children,
// and lifecycle.OnEnd("ast", ...) always reaches the exact same registry.
var sharedManager = newManager()

// QueueResolveHandler registers a staged preview for the calling session so
// `resolve` / `reject` can apply or discard it, and injects omp's hidden
// `resolve-reminder` custom message so the next model turn sees it.
// Exported for custom tools and sibling areas
// (upstream: packages/coding-agent/src/tools/resolve.ts:150-216).
func QueueResolveHandler(ctx sdk.Context, preview PendingPreview) error {
	key := sessionKey(ctx)
	if key == "" {
		return fmt.Errorf("cannot queue resolve handler without a session id")
	}
	p := preview
	sharedManager.pushPending(key, &p)
	sendResolveReminder(ctx, p.SourceToolName)
	return nil
}

// resolveUsesCommandTransport reports whether `resolve`/`reject` are invoked via
// `bash` tool commands in ctx's session (tools.commands enabled and bash active),
// matching omp's `session.toolCommands !== undefined` check in
// packages/coding-agent/src/tools/resolve.ts:207.
func resolveUsesCommandTransport(ctx sdk.Context) bool {
	if !toolcmd.Enabled(ctx, "tools.commands", true) {
		return false
	}
	active, err := ctx.GetActiveTools()
	if err != nil {
		return false
	}
	for _, name := range active {
		if toolcmd.TransportTools[name] {
			return true
		}
	}
	return false
}

// renderResolveReminder renders prompts/resolve-reminder.md verbatim for the
// given source tool name and command-transport mode.
// upstream: packages/coding-agent/src/tools/resolve.ts:197-216,
//
//	packages/coding-agent/src/prompts/system/resolve-reminder.md
func renderResolveReminder(sourceToolName string, isCommand bool) string {
	if sourceToolName == "" {
		sourceToolName = "ast_edit"
	}
	text := strings.TrimSpace(resolveReminderPrompt)
	const ifMarker = "{{#if isCommand}}"
	const elseMarker = "{{else}}"
	const endMarker = "{{/if}}"
	if start := strings.Index(text, ifMarker); start >= 0 {
		if mid := strings.Index(text[start+len(ifMarker):], elseMarker); mid >= 0 {
			mid += start + len(ifMarker)
			if end := strings.Index(text[mid+len(elseMarker):], endMarker); end >= 0 {
				end += mid + len(elseMarker)
				var chosen string
				if isCommand {
					chosen = text[start+len(ifMarker) : mid]
				} else {
					chosen = text[mid+len(elseMarker) : end]
				}
				text = text[:start] + chosen + text[end+len(endMarker):]
			}
		}
	}
	text = strings.ReplaceAll(text, "{{toolName}}", sourceToolName)
	text = strings.ReplaceAll(text, "{{bashToolName}}", "bash")
	return strings.TrimSpace(text)
}

// sendResolveReminder injects omp's hidden `resolve-reminder` steering message
// after a staged preview so the next model call sees it without churning the
// cached prefix.
// upstream: packages/coding-agent/src/tools/resolve.ts:197-216,
//
//	packages/coding-agent/src/session/agent-session.ts:3543-3554
func sendResolveReminder(ctx sdk.Context, sourceToolName string) {
	var zero sdk.Context
	if ctx == zero {
		return
	}
	if sourceToolName == "" {
		sourceToolName = "ast_edit"
	}
	content := renderResolveReminder(sourceToolName, resolveUsesCommandTransport(ctx))
	triggerFalse := false
	_ = ctx.SendMessage(resolveReminderCustomType, content, false, sdk.SendMessageOptions{
		DeliverAs:   "steer",
		TriggerTurn: &triggerFalse,
		Details: map[string]any{
			"toolName": sourceToolName,
		},
	})
}

// HasPendingPreview reports whether ctx's session currently has a staged preview
// awaiting `resolve` or `reject`.
func HasPendingPreview(ctx sdk.Context) bool {
	return sharedManager.peekPending(sessionKey(ctx)) != nil
}

// ExecuteResolve runs the `resolve` special command against the session's pending preview.
// Exported for direct callers and tests.
func ExecuteResolve(ctx sdk.Context, params map[string]any) (any, error) {
	return sharedManager.handleResolve(ctx, params)
}

// ExecuteReject runs the `reject` special command against the session's pending preview.
// Exported for direct callers and tests.
func ExecuteReject(ctx sdk.Context, params map[string]any) (any, error) {
	return sharedManager.handleReject(ctx, params)
}

// sessionKey returns the session's id, or "" if it cannot be resolved.
// Never collapses unknown sessions into a shared "default" bucket.
func sessionKey(ctx sdk.Context) string {
	id, err := ctx.GetSessionID()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(id)
}

func (m *manager) pushPending(key string, p *PendingPreview) {
	if key == "" {
		return
	}
	m.mu.Lock()
	m.pending[key] = append(m.pending[key], p)
	m.mu.Unlock()
}

func (m *manager) peekPending(key string) *PendingPreview {
	if key == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	stack := m.pending[key]
	if len(stack) == 0 {
		return nil
	}
	return stack[len(stack)-1]
}

func (m *manager) popPending(key string, target *PendingPreview) {
	if key == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	stack := m.pending[key]
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == target {
			stack = append(stack[:i], stack[i+1:]...)
			break
		}
	}
	if len(stack) == 0 {
		delete(m.pending, key)
	} else {
		m.pending[key] = stack
	}
}

func (m *manager) endSession(sessionID string) {
	if sessionID == "" {
		return
	}
	m.procs.killSession(sessionID)
	m.mu.Lock()
	delete(m.pending, sessionID)
	m.mu.Unlock()
}

func (m *manager) handleResolve(ctx sdk.Context, params map[string]any) (any, error) {
	reason, _ := params["reason"].(string)
	return m.resolvePending(ctx, "apply", reason)
}

func (m *manager) handleReject(ctx sdk.Context, params map[string]any) (any, error) {
	reason, _ := params["reason"].(string)
	return m.resolvePending(ctx, "discard", reason)
}

// resolvePending applies or discards the latest staged preview for ctx's session.
// upstream: packages/coding-agent/src/tools/resolve.ts:224-316
func (m *manager) resolvePending(ctx sdk.Context, action, rawReason string) (sdk.ToolResult, error) {
	reason := strings.TrimSpace(rawReason)
	key := sessionKey(ctx)
	p := m.peekPending(key)
	if p == nil {
		proposeHint := ""
		if PlanProposalPending(ctx) {
			proposeHint = " To submit the plan for approval, use `propose` with its title instead."
		}
		if action == "discard" {
			return sdk.ToolResult{
				Content: "Nothing to reject; no pending action remains." + proposeHint,
				Details: map[string]any{
					"action": "discard",
					"reason": reason,
				},
			}, nil
		}
		return sdk.ToolResult{}, fmt.Errorf("No pending action to apply — `resolve` is only valid while a staged preview is pending.%s", proposeHint)
	}

	baseDetails := map[string]any{
		"action":         action,
		"reason":         reason,
		"sourceToolName": p.SourceToolName,
		"label":          p.Label,
	}

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-runCtx.Done():
		}
	}()

	if action == "discard" {
		if p.Reject != nil {
			customRes, err := p.Reject(runCtx, ctx, reason)
			if err != nil {
				return sdk.ToolResult{}, err
			}
			m.popPending(key, p)
			if customRes != nil {
				if customRes.Details != nil {
					baseDetails["sourceResultDetails"] = customRes.Details
				}
				return sdk.ToolResult{
					Content: customRes.Content,
					Details: baseDetails,
					IsError: customRes.IsError,
				}, nil
			}
			return sdk.ToolResult{
				Content: fmt.Sprintf("Discarded: %s. Reason: %s", p.Label, reason),
				Details: baseDetails,
			}, nil
		}
		m.popPending(key, p)
		return sdk.ToolResult{
			Content: fmt.Sprintf("Discarded: %s. Reason: %s", p.Label, reason),
			Details: baseDetails,
		}, nil
	}

	res, err := p.Apply(runCtx, ctx, reason)
	if err != nil {
		// Keep preview pending on error so the model can reject or retry (resolve.ts:172-177).
		return sdk.ToolResult{}, fmt.Errorf("Apply failed: %w", err)
	}
	m.popPending(key, p)
	if res.Details != nil {
		baseDetails["sourceResultDetails"] = res.Details
	}
	return sdk.ToolResult{
		Content: res.Content,
		Details: baseDetails,
		IsError: res.IsError,
	}, nil
}

func (m *manager) onSessionShutdown(ctx sdk.Context, _ map[string]any) (any, error) {
	if id := sessionKey(ctx); id != "" {
		lifecycle.End(id)
	}
	return nil, nil
}

// Register wires omp's ast_grep and ast_edit tools (plus special staged-preview
// tools resolve/reject) onto e with ToolExposureCodemode and DefaultActive: false,
// declares them to internal/toolcmd via Mountable / RegisterSpecialCommand so
// toolcmd owns top-level vs bash-mounted activation and executes them strictly
// through PiG's host (Context.ExecuteTool), and installs per-session cleanup via
// lifecycle.OnEnd("ast", ...).
// Safe to call multiple times in one process (once per PiG session/reload);
// never resets live session state and does no I/O at registration time.
func Register(e *sdk.Extension) {
	m := sharedManager

	defaultOff := false

	// upstream: packages/coding-agent/src/tools/ast-grep.ts:144-195
	astGrepDef := schemaorder.Apply(sdk.ToolDefinition{
		Name:          "ast_grep",
		Label:         "AST Grep",
		Description:   strings.TrimSpace(astGrepPrompt),
		PromptSnippet: "Search code with AST patterns (SHOULD be preferred for structural search)",
		Parameters:    astGrepSchema(),
		Exposure:      sdk.ToolExposureCodemode,
		DefaultActive: &defaultOff,
		Execute:       m.handleAstGrep,
	})
	e.RegisterTool(intent.Tool(astGrepDef))

	// upstream: packages/coding-agent/src/tools/ast-edit.ts:173-256
	astEditDef := schemaorder.Apply(sdk.ToolDefinition{
		Name:          "ast_edit",
		Label:         "AST Edit",
		Description:   strings.TrimSpace(astEditPrompt),
		PromptSnippet: "Perform AST-aware code edits (SHOULD be preferred for codemods)",
		Parameters:    astEditSchema(),
		Exposure:      sdk.ToolExposureCodemode,
		DefaultActive: &defaultOff,
		Execute:       m.handleAstEdit,
	})
	e.RegisterTool(intent.Tool(astEditDef))

	// Special staged-action tools (upstream: packages/coding-agent/src/tools/resolve.ts:334-365,
	// tools/index.ts:944-987): codemode exposure + DefaultActive: false so their schemas never
	// ride every request while bash tool commands are active; toolcmd exposes them in the bash
	// preamble or activates them top-level whenever ast_edit is enabled and transport is off.
	e.RegisterTool(intent.Tool(schemaorder.Apply(sdk.ToolDefinition{
		Name:          "resolve",
		Label:         "Resolve",
		Description:   "Apply the pending staged action (e.g. a tool preview), giving a one-sentence reason.",
		PromptSnippet: "Apply the pending staged action",
		Parameters:    resolveSchema(),
		Exposure:      sdk.ToolExposureCodemode,
		DefaultActive: &defaultOff,
		Execute:       m.handleResolve,
	})))

	e.RegisterTool(intent.Tool(schemaorder.Apply(sdk.ToolDefinition{
		Name:          "reject",
		Label:         "Reject",
		Description:   "Discard the pending staged action (e.g. a tool preview), giving a one-sentence reason.",
		PromptSnippet: "Discard the pending staged action",
		Parameters:    rejectSchema(),
		Exposure:      sdk.ToolExposureCodemode,
		DefaultActive: &defaultOff,
		Execute:       m.handleReject,
	})))
	intent.RegisterSync(e, astGrepDef, astEditDef)

	// Declare mountable discoverable tools and special commands to internal/toolcmd.
	// toolcmd owns active-tool activation (top-level when transport is off, mounted
	// in bash when transport is on) and executes through PiG's host ExecuteTool.
	astEditEnabled := func(ctx sdk.Context) bool {
		return Enabled(ctx, SettingAstEditEnabled, true)
	}
	toolcmd.Mountable("ast_grep", func(ctx sdk.Context) bool {
		return Enabled(ctx, SettingAstGrepEnabled, false)
	})
	toolcmd.Mountable("ast_edit", astEditEnabled)
	// Per omp tools/index.ts:974-981 (specialToolApplies): resolve/reject apply
	// statically whenever a deferrable tool (ast_edit) is enabled, so they are
	// already available top-level in the same run that stages a preview when bash
	// or tools.commands is off. HasPendingPreview remains exported for prompt hints.
	toolcmd.RegisterSpecialCommand("resolve", astEditEnabled)
	toolcmd.RegisterSpecialCommand("reject", astEditEnabled)
	// Register package-level per-session cleanup (safe on repeated Register calls
	// because sharedManager is package-level and keyed by sessionID).
	lifecycle.OnEnd("ast", m.endSession)

	e.OnSessionShutdown(m.onSessionShutdown)
}
