package tintinweb_subagents

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The `input` hook that routes `@handle message` typed at the prompt to a subagent instead of the main model. The
// handle names the agent, not one process, so one syntax covers its whole lifecycle: message it while it runs, resume it
// once it has finished, reopen it once it was evicted, start it if it never ran. Everything that is not an agent
// mention falls through untouched, which keeps `@src/foo.ts summarize this`, a bare `@handle` and prose working.
// upstream: src/index.ts (pi.on("input"), startBackgroundResume), the stacked provider in session_start.
//
// The host runs this hook on the goroutine that owns the terminal, so it does no process I/O and never waits for an
// agent to start: the record is created and the answer returned, and a steer or a start's outcome is carried out and
// announced afterwards (port/PORT.md).

var (
	inputContinue = map[string]any{"action": "continue"}
	inputHandled  = map[string]any{"action": "handled"}
)

// maxQueuedSteers bounds the messages waiting to be delivered to agent processes.
const maxQueuedSteers = 256

// serialQueue runs functions one at a time, in order, off the caller's path.
type serialQueue struct {
	mu      sync.Mutex
	pending []func()
	running bool
}

// do queues fn; it reports false when maxQueuedSteers are already waiting.
func (q *serialQueue) do(fn func()) bool {
	q.mu.Lock()
	if len(q.pending) >= maxQueuedSteers {
		q.mu.Unlock()
		return false
	}
	q.pending = append(q.pending, fn)
	start := !q.running
	q.running = true
	q.mu.Unlock()
	if start {
		goGuarded("steer queue", q.drain)
	}
	return true
}

func (q *serialQueue) drain() {
	for {
		q.mu.Lock()
		if len(q.pending) == 0 {
			q.running = false
			q.mu.Unlock()
			return
		}
		fn := q.pending[0]
		q.pending = q.pending[1:]
		q.mu.Unlock()
		guard("queued steer", fn)
	}
}

func (a *app) mentionModeNow() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.mentionMode
}

// onInput is the extension's input handler. A panic reads as "not a mention": the text goes to the main model.
func (a *app) onInput(ctx sdk.Context, data map[string]any) (res any, err error) {
	res = inputContinue
	guard("input hook", func() { res = a.handleInput(ctx, data) })
	return res, nil
}

func (a *app) handleInput(ctx sdk.Context, data map[string]any) any {
	// Never hijack text the extension layer itself submitted: only something a person typed can be a mention.
	if src, _ := data["source"].(string); src == "extension" {
		return inputContinue
	}
	mode := a.mentionModeNow()
	if mode == "off" {
		return inputContinue
	}
	// Claiming the turn is TUI only, matching the `@` completion that teaches the syntax: a headless claim would answer
	// with silence. `model` mode queues a clone and lets the answer arrive as a notification, so it alone acts headless.
	canDispatchDirectly := ctx.Mode() == "tui"
	if !canDispatchDirectly && mode != "model" {
		return inputContinue
	}
	text, _ := data["text"].(string)
	handle, message, ok := parseMention(text)
	if !ok {
		return inputContinue
	}
	// `@main` addresses the main conversation, never a subagent: the escape hatch for text that reads as a mention.
	if isReservedHandle(handle) {
		out := map[string]any{"action": "transform", "text": message}
		if images, ok := data["images"]; ok && images != nil {
			out["images"] = images
		}
		return out
	}
	// As typed first, so an agent actually called `agent-foo` wins over Claude Code's `@agent-` + `foo` spelling.
	stripped, hasStripped := stripAgentPrefix(handle)
	live, dead := a.mgr.resolveMention(handle)
	if live == nil && dead == nil && hasStripped {
		live, dead = a.mgr.resolveMention(stripped)
	}
	// Steering and resuming are direct in every mode, so headless they are not available at all. Falling through here,
	// rather than to the start path, matters: the handle names an agent that exists.
	if (live != nil || dead != nil) && !canDispatchDirectly {
		return inputContinue
	}
	if live != nil {
		if a.mentionLive(ctx, live, handle, message) {
			return inputHandled
		}
		// A live record with no session never got far enough to continue: it starts fresh, like Claude's `no_transcript`.
	}
	if dead != nil {
		a.mentionReopen(ctx, *dead, message)
		return inputHandled
	}
	types := getAvailableTypes()
	typ, found := resolveHandleToType(handle, types)
	if !found && hasStripped {
		typ, found = resolveHandleToType(stripped, types)
	}
	if !found {
		return inputContinue
	}
	if mode == "model" {
		a.mentionViaClone(ctx, typ, message)
	} else {
		a.mentionStart(ctx, typ, message)
	}
	return inputHandled
}

// target is how a notification names the agent: its alias, else its handle.
func targetName(alias, handle, typed string) string {
	for _, s := range []string{alias, handle, typed} {
		if s != "" {
			return "@" + s
		}
	}
	return "@" + typed
}

// mentionLive messages a running agent or resumes a finished one. It reports false for a finished agent with no
// conversation to continue.
func (a *app) mentionLive(ctx sdk.Context, r *agentRecord, typed, message string) bool {
	a.mgr.mu.Lock()
	status, id, typ, desc := r.Status, r.ID, r.Type, r.Description
	target := targetName(r.Alias, r.Handle, typed)
	if status == statusRunning || status == statusQueued {
		// Un-consume the result so the agent's reply to this message is still relayed even if the model read its last answer.
		r.Consumed = false
	}
	a.mgr.mu.Unlock()
	if status == statusRunning || status == statusQueued {
		// Steering interrupts after the current tool call, exactly like the steer_subagent tool.
		if !a.steers.do(func() { a.mgr.steerRecord(r, message) }) {
			ctx.Notify("Could not send to "+target+" — too many messages are waiting.", "warning")
			return true
		}
		_ = ctx.Events().Emit("subagents:steered", map[string]any{"id": id, "message": message})
		ctx.Notify("Sent to "+target, "info")
		return true
	}
	if !a.mgr.canResume(r) {
		return false
	}
	// The completion notification names the call that started the run; a mention-resume has none, so the id the spawn
	// left on the record is cleared (an empty id). upstream: startBackgroundResume.
	if !a.mgr.resume(r, message, true, "") {
		ctx.Notify("Could not resume "+target+" — it is still running.", "warning")
		return true
	}
	_ = ctx.Events().Emit("subagents:created", map[string]any{"id": id, "type": typ, "description": desc, "isBackground": true})
	ctx.Notify("Resuming "+target, "info")
	return true
}

// mentionReopen reopens an evicted agent's conversation: an ordinary spawn carrying its session file, so the new record
// picks up the notification unchanged, and `reclaim` hands it back the names the tombstone held.
func (a *app) mentionReopen(ctx sdk.Context, entry tombstone, message string) {
	target := targetName(entry.Alias, entry.Handle, entry.Handle)
	// Checked here rather than left to the child, whose failure would land on the record: a `/new` elsewhere or a manual
	// delete makes the conversation unrecoverable, so the entry goes (a row that can only fail is worse than none).
	if _, err := os.Stat(entry.SessionFile); err != nil {
		a.mgr.dropTombstone(entry.Handle)
		ctx.Notify("Could not resume "+target+" — its session is gone.", "warning")
		return
	}
	// The Agent tool falls back to general-purpose for a type it cannot resolve, which covers a deleted file AND a merely
	// disabled one. A resume must not inherit that: reopening this conversation under another agent's prompt is not
	// continuing it. The tombstone stays, so re-enabling the agent makes the handle work again.
	a.reloadAgents()
	res := resolveSpawnType(entry.Type)
	if !res.OK || res.FellBackFrom != "" {
		ctx.Notify("Could not resume "+target+" — the "+entry.Type+" agent is no longer available.", "warning")
		return
	}
	r, err := a.start(ctx, startRequest{Type: res.Type, Prompt: message, Description: entry.Description, Background: true,
		ResumeFile: entry.SessionFile, Reclaim: &reclaim{Handle: entry.Handle, Alias: entry.Alias}})
	if err != nil {
		ctx.Notify("Could not resume "+target+": "+err.Error(), "warning")
		return
	}
	// The tombstone deliberately stays: resolveMention prefers the live record holding these names, and if this run dies
	// before establishing a session the original transcript is still the right thing for the next mention to reopen.
	a.afterStart(r, func() { ctx.Notify("Resuming "+target, "info") },
		func(err error) { ctx.Notify("Could not resume "+target+": "+err.Error(), "warning") })
}

// mentionStart starts an agent of a type here, with no model call.
func (a *app) mentionStart(ctx sdk.Context, typ, message string) {
	label := "@" + handleBase(typ)
	// Nothing else to pass: the child resolves model, thinking and max turns from the agent's own config.
	r, err := a.start(ctx, startRequest{Type: typ, Prompt: message, Description: describeMention(message), Background: true})
	if err != nil {
		ctx.Notify("Could not start "+label+": "+err.Error(), "error")
		return
	}
	a.afterStart(r, func() { ctx.Notify("Started "+label, "info") },
		func(err error) { ctx.Notify("Could not start "+label+": "+err.Error(), "error") })
}

// afterStart reports how an agent's startup ended, without holding the caller: a startup failure is a failed start, not
// a "Started" notice for an agent that never ran, and the agent's record (and the handle it took) is dropped.
func (a *app) afterStart(r *agentRecord, ok func(), fail func(error)) {
	goGuarded("mention startup", func() {
		err := a.mgr.awaitStartup(r, 30*time.Second)
		if err == nil {
			// The process exists once startup is over (a queued agent has none yet and is not waited for). Without one the
			// launch failed: the run settles the record as an error, which is a failed start, not an agent that ran.
			a.mgr.mu.Lock()
			queued, launched, done := r.Status == statusQueued, r.child != nil, r.done
			a.mgr.mu.Unlock()
			if !queued && !launched {
				select {
				case <-done:
				case <-time.After(5 * time.Second):
				}
				a.mgr.mu.Lock()
				failed, msg := r.Status == statusError, r.Error
				a.mgr.mu.Unlock()
				if failed {
					err = errors.New(msg)
					a.mgr.discard(r)
				}
			}
		}
		if err != nil {
			fail(err)
			return
		}
		ok()
	})
}

// mentionViaClone starts the agent through a clone of the conversation: same messages, same system prompt, off-screen,
// holding only the Agent tool. Nothing reaches the chat, and what it starts is an ordinary top-level agent.
func (a *app) mentionViaClone(ctx sdk.Context, typ, message string) {
	label := "@" + handleBase(typ)
	// "Prompting", not "Starting": nothing starts until the clone has taken a whole model turn writing the agent's prompt.
	ctx.Notify("Prompting "+label+"…", "info")
	goGuarded("mention clone", func() {
		cctx, stop := contextOf(ctx)
		defer stop()
		res := a.cloneFn(cctx, mentionCloneOptions{
			Host: sessionCloneHost{ctx}, Type: typ, Message: message,
			Tool: cloneTool{Name: a.agentDef.Name, Description: a.agentDef.Description, Parameters: a.agentDef.Parameters, Run: func(params map[string]any) (string, bool) {
				// The registered handler, on the MAIN context and with no tool-call id: the clone's own would file the
				// transcript under the throwaway, and an id the real session never issued is a dangling <tool-use-id>.
				out, err := a.agentTool(ctx, params)
				if err != nil {
					return err.Error(), true
				}
				if tr, ok := out.(sdk.ToolResult); ok {
					return tr.Content, tr.IsError
				}
				return "", false
			}},
		})
		if res.Spawned {
			return
		}
		// A clone that could not run must not swallow the mention: start the agent the direct way.
		a.reloadAgents()
		r, err := a.start(ctx, startRequest{Type: typ, Prompt: message, Description: describeMention(message), Background: true})
		if err != nil {
			ctx.Notify("Could not start "+label+": "+err.Error(), "error")
			return
		}
		a.afterStart(r, func() { ctx.Notify("Started "+label+" directly — "+res.Error, "warning") },
			func(err error) { ctx.Notify("Could not start "+label+": "+err.Error(), "error") })
	})
}

// contextOf is a context that ends with the request's runtime, and a function that releases it.
func contextOf(ctx sdk.Context) (context.Context, func()) {
	c, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	goGuarded("mention context", func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-done:
		}
	})
	return c, func() { close(done); cancel() }
}

// reloadAgents re-reads the agent files of the workspace before a spawn, so a file added mid-session is spawnable.
func (a *app) reloadAgents() {
	a.mu.Lock()
	cwd := a.cwd
	a.mu.Unlock()
	reloadAgentFiles(cwd)
}

// mentionTypes is the agent types `@` can start, in the shape the roster wants.
func mentionTypes() []typeInfo {
	var out []typeInfo
	for _, name := range getAvailableTypes() {
		desc := name
		if c := getAgentConfig(name); c != nil && c.Description != "" {
			desc = c.Description
		}
		out = append(out, typeInfo{Name: name, Description: desc})
	}
	return out
}

// stackMentionProvider stacks the `@handle` suggestions on the host's autocomplete, once per activation (the host
// appends wrappers to a list it never prunes, so a second registration would layer a duplicate). TUI only: no other mode
// has an editor to complete into. It is registered off the session_start handler, whose host round trip it must not hold.
func (a *app) stackMentionProvider(ctx sdk.Context) {
	if ctx.Mode() != "tui" {
		return
	}
	a.mu.Lock()
	if a.mentionHooked {
		a.mu.Unlock()
		return
	}
	a.mentionHooked = true
	a.mu.Unlock()
	goGuarded("mention provider", func() {
		err := ctx.AddAutocompleteProvider(func(_ sdk.Context, current *sdk.AutocompleteProvider) (*sdk.AutocompleteProvider, error) {
			return newMentionProvider(current,
				// Plain text, not a coloured name: the description cannot carry ANSI.
				func() []mentionTarget {
					return mentionRoster(a.mgr, mentionTypes(), func(t string) string { return displayName(a, t) })
				},
				func() bool { return a.mentionModeNow() != "off" }), nil
		})
		if err != nil {
			a.mu.Lock()
			a.mentionHooked = false
			a.mu.Unlock()
		}
	})
}
