package pi_subagents

// Async (background) runs and the control actions: the run registry's control channel, the session continuation that
// steer and resume drive, and the actions' texts (the original's, where it has them).

import (
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// sdkContext is the unused action context in these tests.
func sdkContext() sdk.Context { return sdk.Context{} }

// blockingRunner is a fake child that runs until its done closes, like a real one.
func blockingRunner(t *testing.T) *[]childRequest {
	t.Helper()
	old := runChild
	t.Cleanup(func() { runChild = old })
	var mu sync.Mutex
	var got []childRequest
	runChild = func(done <-chan struct{}, req childRequest) (childResult, error) {
		mu.Lock()
		got = append(got, req)
		mu.Unlock()
		if req.OnOutput != nil {
			req.OnOutput("hi")
		}
		<-done
		return childResult{}, errCancelled
	}
	return &got
}

// answerRunner is a fake child that answers at once.
func answerRunner(t *testing.T, answer string) *[]childRequest {
	t.Helper()
	old := runChild
	t.Cleanup(func() { runChild = old })
	var mu sync.Mutex
	var got []childRequest
	runChild = func(done <-chan struct{}, req childRequest) (childResult, error) {
		mu.Lock()
		got = append(got, req)
		mu.Unlock()
		return childResult{Stdout: answer}, nil
	}
	return &got
}

// waitState waits until the run reaches state (the async goroutine finishes on its own time).
func waitState(t *testing.T, r *runRecord, want runState) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if r.state() == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("state: got %s, want %s", r.state(), want)
}

// waitStepState waits for one step to settle: a cancelled child finishes on its own goroutine after its run is
// terminated.
func waitStepState(t *testing.T, r *runRecord, i int, want runState) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fleet.mu.Lock()
		got := stateQueued
		if i < len(r.steps) {
			got = r.steps[i].state
		}
		fleet.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("step %d state: got %s, want %s", i, r.steps[i].state, want)
}

func TestAsyncRunIsTrackedAndStoppable(t *testing.T) {
	fleet.reset()
	t.Cleanup(fleet.reset)
	blockingRunner(t)
	run := startAsyncRun("/work", &AgentConfig{Name: "scout"}, "look around", "")
	waitState(t, run, stateRunning)
	eq(t, run.state(), stateRunning, "running in the background")

	// stop ends the child; the step takes the run's intent
	run.terminate(stateStopped)
	waitState(t, run, stateStopped)
	eq(t, run.steps[0].state, stateStopped, "stopped step")
}

func TestAsyncRunInterruptIsResumable(t *testing.T) {
	fleet.reset()
	t.Cleanup(fleet.reset)
	blockingRunner(t)
	run := startAsyncRun("/work", &AgentConfig{Name: "scout"}, "look around", "")
	waitState(t, run, stateRunning)

	run.terminate(statePaused) // interrupt
	waitState(t, run, statePaused)
	eq(t, run.resumable(), true, "an interrupted run resumes")
	eq(t, run.steps[0].state, statePaused, "paused step")
}

// A detached run's outcome is delivered to the session as the original's completion notification (its notify.ts): a
// wake with the formatted summary. A plain success stays out of the transcript (its display rule); a failure displays.
func TestAsyncRunCompletionsAreDelivered(t *testing.T) {
	fleet.reset()
	t.Cleanup(fleet.reset)
	notices := captureCompletions(t)

	answerRunner(t, "all good")
	run := startAsyncRun("/work", &AgentConfig{Name: "scout"}, "t", "")
	waitState(t, run, stateComplete)
	got := waitNotices(t, notices, 1)
	eq(t, len(got), 1, "one completion notification")
	eq(t, got[0].content, "Background task completed: **scout**\n\nall good", "the formatted wake")
	eq(t, got[0].display, false, "a plain success stays out of the transcript (its display rule)")

	runChild = func(done <-chan struct{}, req childRequest) (childResult, error) {
		return childResult{Exit: 2, Stderr: "boom"}, nil
	}
	run = startAsyncRun("/work", &AgentConfig{Name: "scout"}, "t", "")
	waitState(t, run, stateFailed)
	got = waitNotices(t, notices, 2)
	eq(t, got[1].content, "Background task failed: **scout**\n\nscout exited with code 2: boom", "the failure summary")
	eq(t, got[1].display, true, "a failure is displayed")
}

// A steer delivered over the parent link (link.go) joins the child's running turn at the host's next safe point: the
// turn is never interrupted and loses nothing in flight, and its answer is delivered as the steer reply.
func TestSteerDeliversToTheRunningTurn(t *testing.T) {
	fleet.reset()
	t.Cleanup(fleet.reset)
	t.Cleanup(closeSessionLink)
	notices := captureCompletions(t)
	startSessionLink("link-deliver")
	applied := make(chan string, 4)
	go serveParentLink("link-deliver", "child-1", func(op string, req map[string]any) error {
		if op == "steer" {
			message, _ := req["message"].(string)
			applied <- message
		}
		return nil
	})
	old := runChild
	t.Cleanup(func() { runChild = old })
	release := make(chan struct{})
	runChild = func(done <-chan struct{}, req childRequest) (childResult, error) {
		req.OnOutput("working")
		select {
		case <-release:
			return childResult{Stdout: "all done"}, nil
		case <-done:
			return childResult{}, errCancelled
		}
	}
	run := startAsyncRun("/work", &AgentConfig{Name: "scout"}, "t", "")
	waitState(t, run, stateRunning)
	run.steps[0].pinSession("d", "child-1")
	deadline := time.Now().Add(2 * time.Second)
	for sendToChild("child-1", map[string]any{"op": "ping"}) != nil {
		if time.Now().After(deadline) {
			t.Fatalf("the child never connected to the parent link: %v", sendToChild("child-1", map[string]any{"op": "ping"}))
		}
		time.Sleep(2 * time.Millisecond)
	}
	out, err := actions["steer"].run(sdkContext(), map[string]any{"id": run.id, "message": "hold that thought"})
	eq(t, err, nil, "steer")
	if !strings.Contains(out.(explicitOK).Content, "Steering delivered for run "+run.id) {
		t.Errorf("steer: %v", out)
	}
	eq(t, <-applied, "hold that thought", "the link carried the message into the running turn")
	eq(t, len(run.steps), 1, "no new turn")
	eq(t, run.steps[0].state, stateRunning, "the running turn is undisturbed")

	close(release)
	got := waitNotices(t, notices, 1)
	eq(t, got[0].content, "Background task completed: **scout** (steer reply)\n\nall done", "the turn's answer is the steer reply")
	eq(t, got[0].display, true, "a steer reply is displayed")
}

// A steer the link cannot deliver queues the message for the run's next turn: the current turn continues undisturbed
// (the port's steer no longer interrupts a turn and loses what is in flight).
func TestSteerQueuesTheNextTurnWithoutInterrupting(t *testing.T) {
	fleet.reset()
	t.Cleanup(fleet.reset)
	notices := captureCompletions(t)
	old := runChild
	t.Cleanup(func() { runChild = old })
	release := make(chan struct{})
	calls := 0
	runChild = func(done <-chan struct{}, req childRequest) (childResult, error) {
		calls++
		if calls == 1 {
			req.OnOutput("working")
			select {
			case <-release:
				return childResult{Stdout: "done"}, nil
			case <-done:
				return childResult{}, errCancelled
			}
		}
		return childResult{Stdout: "sure"}, nil
	}
	run := startAsyncRun("/work", &AgentConfig{Name: "scout"}, "t", "")
	waitState(t, run, stateRunning)
	out, err := actions["steer"].run(sdkContext(), map[string]any{"id": run.id, "message": "report please"})
	eq(t, err, nil, "steer")
	if !strings.Contains(out.(explicitOK).Content, "Steering queued for run "+run.id) {
		t.Errorf("steer: %v", out)
	}
	eq(t, len(run.steps), 1, "no new turn yet")
	eq(t, run.steps[0].state, stateRunning, "the running turn is undisturbed")

	close(release)
	got := waitNotices(t, notices, 1)
	eq(t, len(run.steps), 2, "the message ran as the next turn")
	eq(t, run.steps[1].task, "report please", "the message is the next turn's task")
	eq(t, got[0].content, "Detached foreground task completed: **scout** (steer reply)\n\nsure", "the queued turn's answer is the steer reply")
}

// follow_up always queues, and says so.
func TestSteerFollowUpQueues(t *testing.T) {
	fleet.reset()
	t.Cleanup(fleet.reset)
	notices := captureCompletions(t)
	old := runChild
	t.Cleanup(func() { runChild = old })
	release := make(chan struct{})
	calls := 0
	runChild = func(done <-chan struct{}, req childRequest) (childResult, error) {
		calls++
		if calls == 1 {
			select {
			case <-release:
				return childResult{Stdout: "done"}, nil
			case <-done:
				return childResult{}, errCancelled
			}
		}
		return childResult{Stdout: "follow"}, nil
	}
	run := startAsyncRun("/work", &AgentConfig{Name: "scout"}, "t", "")
	waitState(t, run, stateRunning)
	out, err := actions["steer"].run(sdkContext(), map[string]any{"id": run.id, "message": "and then", "mode": "follow_up"})
	eq(t, err, nil, "steer")
	if !strings.Contains(out.(explicitOK).Content, "Follow-up queued for run "+run.id) {
		t.Errorf("steer: %v", out)
	}
	close(release)
	got := waitNotices(t, notices, 1)
	eq(t, got[0].content, "Detached foreground task completed: **scout** (steer reply)\n\nfollow", "the follow-up runs as the next turn")
}

func TestSteeringReceiptLooksLikeTheOriginal(t *testing.T) {
	eq(t, steeringReceipt("do it", "Steering delivered for run run-1."),
		"Steering delivered for run run-1.\n\nMessage sent:\n```text\ndo it\n```", "a plain message")
	eq(t, steeringReceipt("with ``` fence", "Q"),
		"Q\n\nMessage sent:\n````text\nwith ``` fence\n````", "the fence out-fences the message")
	eq(t, steerMode(map[string]any{"mode": "follow_up"}), "follow_up", "mode")
	eq(t, steerMode(map[string]any{"mode": "plan"}), "steer", "an unknown mode is the default")
}

func TestContinuationRequestKeepsTheSessionAndDropsTheSystemPrompt(t *testing.T) {
	base := childRequest{Bin: "pig", Args: []string{"--print", "--system-prompt", "You help.", "--session-dir", "d", "--session-id", "i"}, Stdin: "Task: t", Cwd: "/w", Env: []string{"X=1"}}
	req := continuationRequest(base, "do the next thing")
	eq(t, req.Args, []string{"--print", "--session-dir", "d", "--session-id", "i"}, "no system prompt, same session")
	eq(t, req.Stdin, "do the next thing", "the message replaces the task")
	eq(t, req.Env, base.Env, "same environment")
}

func TestControlActions(t *testing.T) {
	fleet.reset()
	t.Cleanup(fleet.reset)
	blockingRunner(t)
	run := startAsyncRun("/work", &AgentConfig{Name: "scout"}, "t", "")
	waitState(t, run, stateRunning)
	id := map[string]any{"id": run.id}

	// steering delivery lives in TestSteerDeliversToTheRunningTurn / TestSteerQueuesTheNextTurnWithoutInterrupting
	_, err := actions["steer"].run(sdkContext(), map[string]any{"id": run.id, "message": ""})
	eq(t, err.Error(), "Steer requires message.", "message")

	// stop
	out, err := actions["stop"].run(sdkContext(), id)
	eq(t, err, nil, "stop")
	if !strings.Contains(out.(explicitOK).Content, "Stop requested for run "+run.id) {
		t.Errorf("stop: %v", out)
	}
	waitState(t, run, stateStopped)

	// a stopped run cannot be stopped or steered, and cannot be resumed
	_, err = actions["stop"].run(sdkContext(), id)
	if err == nil || !strings.Contains(err.Error(), "cannot be stopped") {
		t.Errorf("stop again: %v", err)
	}
	_, err = actions["steer"].run(sdkContext(), map[string]any{"id": run.id, "message": "x"})
	if err == nil || !strings.Contains(err.Error(), "cannot be steered") {
		t.Errorf("steer terminal: %v", err)
	}
	_, err = actions["resume"].run(sdkContext(), map[string]any{"id": run.id, "message": "x"})
	if err == nil || !strings.Contains(err.Error(), "stopped and cannot be resumed") {
		t.Errorf("resume stopped: %v", err)
	}

	// unknown ids and missing messages are the original's errors
	_, err = actions["interrupt"].run(sdkContext(), map[string]any{"id": "run-999"})
	eq(t, err.Error(), "Async run 'run-999' was not found in the active session.", "unknown id")
	_, err = actions["resume"].run(sdkContext(), map[string]any{"id": run.id})
	eq(t, err.Error(), "Resume requires message.", "message")
}

func TestInterruptAndResume(t *testing.T) {
	fleet.reset()
	t.Cleanup(fleet.reset)
	blockingRunner(t)
	run := startAsyncRun("/work", &AgentConfig{Name: "scout"}, "t", "")
	waitState(t, run, stateRunning)

	out, err := actions["interrupt"].run(sdkContext(), map[string]any{"id": run.id})
	eq(t, err, nil, "interrupt")
	if !strings.Contains(out.(explicitOK).Content, "Interrupt requested for async run "+run.id) {
		t.Errorf("interrupt: %v", out)
	}
	waitState(t, run, statePaused)
	_, err = actions["interrupt"].run(sdkContext(), map[string]any{"id": run.id})
	if err == nil || !strings.Contains(err.Error(), "no active child step") {
		t.Errorf("interrupt terminal: %v", err)
	}

	// resume continues the session as a new run
	answerRunner(t, "sure")
	out, err = actions["resume"].run(sdkContext(), map[string]any{"id": run.id, "message": "and then?"})
	eq(t, err, nil, "resume")
	text := out.(explicitOK).Content
	if !strings.Contains(text, "Follow-up run: run-") {
		t.Errorf("resume receipt: %s", text)
	}
	var next *runRecord
	fleet.mu.Lock()
	for _, r := range fleet.runs {
		if r.resumedFrom == run.id {
			next = r // the registry's live record: a snapshot copy would never settle
		}
	}
	fleet.mu.Unlock()
	if next == nil {
		t.Fatal("no follow-up run")
	}
	eq(t, next.task, "and then?", "the message is the follow-up turn")
	waitState(t, next, stateComplete)
}
