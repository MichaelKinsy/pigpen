package pi_subagents

// Async (background) runs and the control actions. `subagent({ agent, task, async: true })` detaches the child from the
// tool call: the call returns at once and the run lives on in the registry (the original's async runs, its
// async-execution.ts). `stop`, `interrupt`, `resume` and `steer` manage it (the original's control channel; here the
// run's own done channel and a session continuation child). `bg_wait` is not ported.

import (
	"fmt"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// childSessionDir is the directory children pin their session files to (steer and resume continue them). The extension
// sets it at session start; without it a child has no session file and cannot be resumed.
var childSessionDir sync.Map // key "dir" -> string

func setChildSessionDir(dir string) {
	if dir == "" {
		childSessionDir.Delete("dir")
		return
	}
	childSessionDir.Store("dir", dir)
}

func childSessionDirFor() string {
	if v, ok := childSessionDir.Load("dir"); ok {
		s, _ := v.(string)
		return s
	}
	return ""
}

// sessionArgs pins a child's session file: the transcript lands in dir under id, so a later turn can continue it.
func sessionArgs(dir string) (args []string, id string) {
	if dir == "" {
		return nil, ""
	}
	id = uuidV4()
	return []string{"--session-dir", dir, "--session-id", id}, id
}

// continuationRequest is the child's request for a later turn on the same session: the message replaces the task, and
// the system prompt is not set again (the session already has it).
func continuationRequest(base childRequest, message string) childRequest {
	args := make([]string, 0, len(base.Args))
	for i := 0; i < len(base.Args); i++ {
		switch base.Args[i] {
		case "--system-prompt", "--append-system-prompt":
			i++ // the value follows
			continue
		}
		args = append(args, base.Args[i])
	}
	req := base
	req.Args = args
	req.Stdin = message
	return req
}

// startAsyncRun launches a child detached from the tool call (the original's async run). The child runs hidden (a pane
// is a foreground surface); its completion is delivered to the session as a completion notification that wakes the
// model (the original's tool guidance: "ordinary async subagent runs do not need a wait call because their completion
// is delivered natively").
func startAsyncRun(cwd string, a *AgentConfig, task, model string) *runRecord {
	run := fleet.beginRun("single", a.Name, task, cwd)
	go func() {
		_, st, _ := runAgent(nil, cwd, a, task, model, nil, run)
		st.setNotice("Background task", "")
		notifyTurnEnd(run)
		fleet.changed()
	}()
	return run
}

// notifyTurnEnd delivers the completion notification once the run has settled (every turn terminal): whichever turn's
// goroutine returns last sees the settled run and delivers the last turn's outcome exactly once (run.noticeSent). A
// steer's interrupted turn therefore never notifies — its pivoted turn is the run's answer — and the steered turn's
// answer is delivered as its steer reply.
func notifyTurnEnd(run *runRecord) {
	fleet.mu.Lock()
	for _, s := range run.steps {
		if s.state == stateRunning || s.state == stateQueued {
			fleet.mu.Unlock()
			return // the run continues
		}
	}
	if run.noticeSent || len(run.steps) == 0 {
		fleet.mu.Unlock()
		return
	}
	last := *run.steps[len(run.steps)-1]
	if last.noticeKind == "" {
		fleet.mu.Unlock()
		return // a turn its tool call reports itself
	}
	run.noticeSent = true
	fleet.mu.Unlock()
	status := "stopped"
	switch last.state {
	case stateComplete:
		status = "completed"
	case stateFailed:
		status = "failed"
	case statePaused:
		status = "paused"
	}
	preview := last.output
	if last.err != "" {
		preview = last.err
		if strings.TrimSpace(last.output) != "" {
			preview += "\n\n" + last.output
		}
	}
	n := completionNotice{
		kind:    last.noticeKind,
		status:  status,
		agent:   last.agent,
		info:    last.noticeInfo,
		preview: completionPreview(preview),
		session: last.sessionPath(),
	}
	n.display = n.kind == "Detached foreground task" || n.status != "completed" // its sendCompletion display rule
	completions.deliver(n)
}

// steerRun interrupts the run's current turn and continues its session with the message (the port's steer: a print
// child cannot be redirected mid-turn, so the child pivots at once; the run keeps its id). The next turn is queued
// before the pivot, so the interrupted turn's goroutine sees it and stays quiet; the steered turn's answer is delivered
// as a completion notification when it settles (its steer reply).
func steerRun(run *runRecord, message string) {
	thinking := run.lastThinking()
	st := run.addStep(run.name, message, thinking)
	st.setNotice("Detached foreground task", " (steer reply)")
	run.terminate(statePaused)
	run.rearm()
	base, _, _ := run.getLaunch()
	req := continuationRequest(base, message)
	done := run.currentDone()
	go func() {
		_, _ = runStep(done, run, st, req, nil)
		notifyTurnEnd(run)
	}()
	fleet.changed()
}

// resumeRun starts a follow-up run on the stopped one's session (the original's resume: a new run id per pass).
func resumeRun(old *runRecord, message string) *runRecord {
	base, dir, id := old.getLaunch()
	req := continuationRequest(base, message)
	run := fleet.beginRun("single", old.name, message, old.cwd)
	run.setResumedFrom(old.id)
	run.setLaunch(req, dir, id)
	st := run.addStep(old.name, message, old.lastThinking())
	st.setNotice("Background task", "")
	go func() {
		_, _ = runStep(run.currentDone(), run, st, req, nil)
		notifyTurnEnd(run)
	}()
	return run
}

// asyncStartedMessage is the original's formatAsyncStartedMessage for one launched child (its guidance, adapted to the
// port's completion reporting).
func asyncStartedMessage(agent, id string) string {
	return strings.Join([]string{
		fmt.Sprintf("Async: %s [%s]", agent, id),
		"",
		"The async run is detached. Return control to the user now: its completion notification wakes this session with the result when the subagent completes or needs attention. Do not run sleep timers or polling loops to wait for it.",
		fmt.Sprintf(`Use subagent({ action: "status", id: "%s" }) when you need a one-shot status/result or to inspect a stopped run; do not poll in a loop.`, id),
	}, "\n")
}

// followUpStartedMessage is the original's resume receipt (its subagent-executor.ts lines).
func followUpStartedMessage(id string) string {
	return strings.Join([]string{
		fmt.Sprintf("Follow-up run: %s", id),
		fmt.Sprintf(`Status if needed: subagent({ action: "status", id: "%s" })`, id),
		"",
		"The async run is detached. Its completion notification wakes this session with the result; do not run sleep timers or polling loops to wait for it.",
		fmt.Sprintf(`Use subagent({ action: "status", id: "%s" }) when you need a one-shot status/result or to inspect a stopped run; do not poll in a loop.`, id),
	}, "\n")
}

// controlTarget resolves the run a control action names, or returns the action's error text.
func controlTarget(params map[string]any, action string) (*runRecord, string) {
	id, _ := params["id"].(string)
	if id == "" {
		id, _ = params["runId"].(string)
	}
	if id == "" {
		return nil, fmt.Sprintf("Specify 'id' for %s.", action)
	}
	run := fleet.findLive(id)
	if run == nil {
		return nil, fmt.Sprintf("Async run '%s' was not found in the active session.", id)
	}
	return run, ""
}

func init() {
	// The control actions (the original's run-status and control channels). Each one refuses what it cannot run.
	registerAction("stop", []string{"id", "runId"}, func(_ sdk.Context, params map[string]any) (any, error) {
		run, msg := controlTarget(params, "stop")
		if msg != "" {
			return nil, sdk.NewToolError(msg)
		}
		if !run.isLive() {
			return nil, sdk.NewToolError(fmt.Sprintf("Run '%s' is %s and cannot be stopped.", run.id, run.state()))
		}
		run.terminate(stateStopped)
		return controlOK(fmt.Sprintf("Stop requested for run %s.", run.id)), nil
	})
	registerAction("interrupt", []string{"id", "runId"}, func(_ sdk.Context, params map[string]any) (any, error) {
		run, msg := controlTarget(params, "interrupt")
		if msg != "" {
			return nil, sdk.NewToolError(msg)
		}
		if !run.isLive() {
			return nil, sdk.NewToolError(fmt.Sprintf("Async run '%s' has no active child step to interrupt.", run.id))
		}
		run.terminate(statePaused)
		return controlOK(fmt.Sprintf("Interrupt requested for async run %s.", run.id)), nil
	})
	registerAction("steer", []string{"id", "runId", "message"}, func(_ sdk.Context, params map[string]any) (any, error) {
		run, msg := controlTarget(params, "steer")
		if msg != "" {
			return nil, sdk.NewToolError(msg)
		}
		message, _ := params["message"].(string)
		if jsTrim(message) == "" {
			return nil, sdk.NewToolError("Steer requires message.")
		}
		if !run.isLive() {
			return nil, sdk.NewToolError(fmt.Sprintf("Async run '%s' is not running or queued and cannot be steered.", run.id))
		}
		steerRun(run, message)
		return controlOK(fmt.Sprintf("Steered run %s: its current turn was interrupted and turn %d continues the session with your message.\nStatus: subagent({ action: \"status\", id: \"%s\" })", run.id, len(run.steps), run.id)), nil
	})
	registerAction("resume", []string{"id", "runId", "message"}, func(_ sdk.Context, params map[string]any) (any, error) {
		run, msg := controlTarget(params, "resume")
		if msg != "" {
			return nil, sdk.NewToolError(msg)
		}
		message, _ := params["message"].(string)
		if jsTrim(message) == "" {
			return nil, sdk.NewToolError("Resume requires message.")
		}
		if run.isLive() {
			return nil, sdk.NewToolError(fmt.Sprintf("Async run '%s' is still running; steer it or stop it first.", run.id))
		}
		if run.state() == stateStopped {
			return nil, sdk.NewToolError(fmt.Sprintf("Run '%s' is stopped and cannot be resumed.", run.id))
		}
		base, _, _ := run.getLaunch()
		if base.Bin == "" {
			return nil, sdk.NewToolError(fmt.Sprintf("Run '%s' has no child session file to resume.", run.id))
		}
		next := resumeRun(run, message)
		return controlOK(followUpStartedMessage(next.id)), nil
	})
}

// controlOK is a control action's result (the original's management detail shape).
func controlOK(text string) any {
	return explicitOK{Content: text, Details: map[string]any{"mode": "management", "results": []any{}}}
}

// asyncParam reads `async`: absent or false runs in the foreground, true detaches the child. The original defaults to
// async; the port keeps the foreground default (its README says so).
func asyncParam(params map[string]any) (bool, string) {
	v, ok := params["async"]
	if !ok || v == nil {
		return false, ""
	}
	if b, isBool := v.(bool); isBool {
		return b, ""
	}
	return false, "async must be a boolean when provided."
}
