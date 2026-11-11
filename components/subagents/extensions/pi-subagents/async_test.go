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
			req.OnOutput()
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

func TestAsyncRunIsTrackedAndStoppable(t *testing.T) {
	fleet.reset()
	t.Cleanup(fleet.reset)
	blockingRunner(t)
	run := startAsyncRun("/work", &AgentConfig{Name: "scout"}, "look around", "", func(string, string) {})
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
	run := startAsyncRun("/work", &AgentConfig{Name: "scout"}, "look around", "", func(string, string) {})
	waitState(t, run, stateRunning)

	run.terminate(statePaused) // interrupt
	waitState(t, run, statePaused)
	eq(t, run.resumable(), true, "an interrupted run resumes")
	eq(t, run.steps[0].state, statePaused, "paused step")
}

func TestAsyncRunCompletionIsQuietAndFailureNotifies(t *testing.T) {
	fleet.reset()
	t.Cleanup(fleet.reset)
	var notes []string
	notify := func(m, _ string) { notes = append(notes, m) }

	answerRunner(t, "all good")
	run := startAsyncRun("/work", &AgentConfig{Name: "scout"}, "t", "", notify)
	waitState(t, run, stateComplete)
	eq(t, len(notes), 0, "a successful completion stays quiet")

	runChild = func(done <-chan struct{}, req childRequest) (childResult, error) {
		return childResult{Exit: 2, Stderr: "boom"}, nil
	}
	run = startAsyncRun("/work", &AgentConfig{Name: "scout"}, "t", "", notify)
	waitState(t, run, stateFailed)
	if len(notes) != 1 || !strings.Contains(notes[0], "failed") {
		t.Errorf("failure notice: %q", notes)
	}
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
	run := startAsyncRun("/work", &AgentConfig{Name: "scout"}, "t", "", func(string, string) {})
	waitState(t, run, stateRunning)
	id := map[string]any{"id": run.id}

	// steer interrupts the turn and continues the session in the same run
	out, err := actions["steer"].run(sdkContext(), map[string]any{"id": run.id, "message": "look left"})
	eq(t, err, nil, "steer")
	if !strings.Contains(out.(explicitOK).Content, "Steered run "+run.id) {
		t.Errorf("steer: %v", out)
	}
	waitState(t, run, stateRunning) // the pivoted turn runs
	eq(t, len(run.steps), 2, "two turns")
	eq(t, run.steps[0].state, statePaused, "the first turn was interrupted")

	// stop
	out, err = actions["stop"].run(sdkContext(), id)
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
	run := startAsyncRun("/work", &AgentConfig{Name: "scout"}, "t", "", func(string, string) {})
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
	for _, r := range fleet.snapshot() {
		if r.resumedFrom == run.id {
			next = r
		}
	}
	if next == nil {
		t.Fatal("no follow-up run")
	}
	eq(t, next.task, "and then?", "the message is the follow-up turn")
	waitState(t, next, stateComplete)
}
