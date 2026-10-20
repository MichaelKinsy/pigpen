package tintinweb_subagents

import (
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// invocation is what an Agent call asks for once the agent file has had its say: the file outranks the call.
type invocation struct {
	InheritContext, Background, Isolated bool
}

// resolveInvocation is the precedence of the original's resolveAgentInvocationConfig for the parameters this port
// handles: the agent file's value, else the call's, else the default (inherit nothing, run in the background,
// not isolated). upstream: src/invocation-config.ts resolveAgentInvocationConfig.
func resolveInvocation(cfg *agentConfig, p map[string]any) invocation {
	pick := func(fromFile *bool, key string, def bool) bool {
		if fromFile != nil {
			return *fromFile
		}
		if b, ok := p[key].(bool); ok {
			return b
		}
		return def
	}
	var inv invocation
	var file struct{ inherit, background, isolated *bool }
	if cfg != nil {
		file.inherit, file.background, file.isolated = cfg.InheritContext, cfg.RunInBackground, cfg.Isolated
	}
	inv.InheritContext = pick(file.inherit, "inherit_context", false)
	inv.Background = pick(file.background, "run_in_background", true)
	inv.Isolated = pick(file.isolated, "isolated", false)
	return inv
}

// resumeTool is the Agent tool's `resume` branch: it continues a finished agent's conversation with a new prompt,
// in the background (detached, notified at the end) or inline. upstream: index.ts Agent execute (Resume existing agent).
func (a *app) resumeTool(ctx sdk.Context, id, prompt string, background bool) (any, error) {
	existing := a.mgr.get(id)
	if existing == nil {
		return notFound(id), nil
	}
	if !a.mgr.canResume(existing) {
		return textResult(`Agent "` + id + `" has no active session to resume.`), nil
	}
	a.mgr.mu.Lock()
	status, typ, desc := existing.Status, existing.Type, existing.Description
	a.mgr.mu.Unlock()
	busy := status == statusRunning || status == statusQueued
	if background {
		// A detached resume hands control back while the record stays running, so the same agent could be resumed again
		// mid-run: say why that cannot work where the model can act on it.
		if busy {
			return textResult(fmt.Sprintf("Agent \"%s\" is still %s — it can only be resumed once its current run finishes.\n"+
				"Use steer_subagent to send it a message mid-run, or get_subagent_result to wait for it.", id, status)), nil
		}
		// The completion notification names the tool call that started this run; a refused resume leaves the record's
		// own id alone.
		if !a.mgr.resume(existing, prompt, true, ctx.ToolCallID()) {
			return textResult(`Failed to resume agent "` + id + `".`), nil
		}
		_ = ctx.Events().Emit("subagents:created", map[string]any{"id": existing.ID, "type": typ, "description": desc, "isBackground": true})
		a.mgr.mu.Lock()
		queued, limit := existing.Status == statusQueued, a.mgr.maxConcurrent
		a.mgr.mu.Unlock()
		state, pos := "resumed", ""
		if queued {
			state, pos = "queued", fmt.Sprintf("Position: queued (max %d concurrent)\n", limit)
		}
		return textResult(fmt.Sprintf("Agent %s in background.\nAgent ID: %s\nType: %s\n%s\nYou will be notified when this agent completes.\n"+
			"Use get_subagent_result to retrieve full results, or steer_subagent to send it messages.", state, existing.ID, typ, pos)), nil
	}
	if busy {
		// What pi's session.prompt throws for a prompt sent to a session that is mid-run.
		return textResult("Agent failed: " + errStillProcessing), nil
	}
	if !a.mgr.resume(existing, prompt, false, "") {
		return textResult(`Failed to resume agent "` + id + `".`), nil
	}
	done := a.mgr.doneOf(existing)
	select {
	case <-done:
	case <-ctx.Done():
		a.mgr.abort(existing)
		<-done
	}
	a.mgr.mu.Lock()
	status, result, errText := existing.Status, existing.Result, existing.Error
	a.mgr.mu.Unlock()
	// A failed resume surfaces the error, plus any partial output THIS resume produced, never the previous turn's
	// answer (#144).
	if status == statusError {
		return textResult("Agent failed: " + errText + partialOutputSuffix(result)), nil
	}
	if t := strings.TrimSpace(result); t != "" {
		return textResult(t), nil
	}
	return textResult("No output."), nil
}
