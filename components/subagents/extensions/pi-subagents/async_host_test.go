package pi_subagents_test

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	sub "github.com/MichaelKinsy/pigpen/pi-subagents"
)

// The async slice through the host boundary: the started message, the status
// action, bg_wait, stop and the completion notification.

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// asyncHost starts a host whose session id is fixed and whose child runner is faked.
func asyncHost(t *testing.T, outputs map[string]string) (*Host, *childCalls, string) {
	t.Helper()
	sub.ResetRuns(t)
	t.Setenv("PI_SUBAGENTS_TEMP_ROOT", filepath.Join(t.TempDir(), "async-artifacts"))
	calls := fakeChildren(t, outputs)
	cwd := tmp(t)
	withTempHome(t)
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "helper.md"), agentMD("helper", "Helps", "You help."))
	h := StartHost(t, Extension(), HostOptions{
		Cwd: cwd,
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "getSessionID" {
				return map[string]any{"sessionId": "session-test"}, ""
			}
			return nil, ""
		},
	})
	return h, calls, cwd
}

// resultContent decodes a tool result's text, so assertions read the text the
// model sees instead of its JSON escaping.
func resultContent(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var got struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("result %s: %v", raw, err)
	}
	return got.Content
}

var asyncIDRe = regexp.MustCompile(`Async: helper \[([0-9a-f-]+)\]`)

func TestAsyncDefaultRunReturnsTheStartedMessageAndCompletes(t *testing.T) {
	h, _, _ := asyncHost(t, nil)
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	sub.SetRunChild(t, func(done <-chan struct{}, req sub.ChildRequest) (sub.ChildResult, error) {
		started <- struct{}{}
		select {
		case <-release:
			return sub.ChildResult{Stdout: "all done"}, nil
		case <-done:
			return sub.ChildResult{}, sub.ErrCancelled
		}
	})
	raw, failure := h.Tool("subagent", map[string]any{"agent": "helper", "task": "do it"})
	eq(t, failure, "", "no failure")
	m := asyncIDRe.FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatalf("no Async: helper [id] in %s", raw)
	}
	runID := m[1]
	if !strings.Contains(string(raw), "The async run is detached and running in the background.") {
		t.Errorf("interactive guidance missing: %s", raw)
	}
	<-started

	// The status action sees the run while the child is still working.
	status, failure := h.Tool("subagent", map[string]any{"action": "status", "id": runID})
	eq(t, failure, "", "status")
	statusText := resultContent(t, status)
	for _, want := range []string{"Run: " + runID, "State: running", "Mode: single", "Dir: "} {
		if !strings.Contains(statusText, want) {
			t.Errorf("status missing %q: %s", want, statusText)
		}
	}

	// Release the child while bg_wait blocks; it reports the original's summary.
	go func() {
		time.Sleep(150 * time.Millisecond)
		close(release)
	}()
	waited, failure := h.Tool("bg_wait", map[string]any{})
	eq(t, failure, "", "bg_wait")
	if !regexp.MustCompile(`Waited .+; 1 of 1 run\(s\) finished\. Outcome: 1 complete\.`).Match(waited) {
		t.Errorf("bg_wait: %s", waited)
	}
	// The completion notice reached the host as the original's message.
	waitFor(t, func() bool {
		for _, c := range h.CallsTo("sendMessage") {
			msg, _ := c.Args["message"].(map[string]any)
			content, _ := msg["content"].(string)
			if strings.HasPrefix(content, "Background task completed: **helper**") {
				return true
			}
		}
		return false
	}, "the completion notification")
}

func TestStatusListFleetAndTranscriptThroughTheTool(t *testing.T) {
	h, _, _ := asyncHost(t, nil)
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	sub.SetRunChild(t, func(done <-chan struct{}, req sub.ChildRequest) (sub.ChildResult, error) {
		started <- struct{}{}
		select {
		case <-release:
			return sub.ChildResult{Stdout: "finished"}, nil
		case <-done:
			return sub.ChildResult{}, sub.ErrCancelled
		}
	})
	raw, failure := h.Tool("subagent", map[string]any{"agent": "helper", "task": "slow"})
	eq(t, failure, "", "start")
	runID := asyncIDRe.FindStringSubmatch(string(raw))[1]
	<-started

	// The active list: the original's heading and header.
	list, failure := h.Tool("subagent", map[string]any{"action": "status"})
	eq(t, failure, "", "list")
	if !strings.Contains(string(list), "Active async runs: 1") || !strings.Contains(string(list), "- "+runID+" | running |") {
		t.Errorf("list: %s", list)
	}

	// The fleet view: the original's commands block.
	fleet, failure := h.Tool("subagent", map[string]any{"action": "status", "view": "fleet"})
	eq(t, failure, "", "fleet")
	fleetText := resultContent(t, fleet)
	for _, want := range []string{"Subagent fleet: 1 tracked", "Async runs:", "status: subagent({ action: \"status\", id: \"" + runID + "\" })", "Refresh fleet: subagent({ action: \"status\", view: \"fleet\" })"} {
		if !strings.Contains(fleetText, want) {
			t.Errorf("fleet missing %q: %s", want, fleetText)
		}
	}

	// An unknown view keeps the original's error.
	_, failure = h.Tool("subagent", map[string]any{"action": "status", "view": "wide"})
	eq(t, failure, "Unknown status view: wide. Valid: fleet, transcript.", "view error")

	// Stop seals the run with the original's text.
	stopped, failure := h.Tool("subagent", map[string]any{"action": "stop", "id": runID})
	eq(t, failure, "", "stop")
	if !strings.Contains(resultContent(t, stopped), "Stop requested for async run "+runID+".") {
		t.Errorf("stop: %s", stopped)
	}
	waitFor(t, func() bool {
		got, failure := h.Tool("subagent", map[string]any{"action": "status", "id": runID})
		return failure == "" && strings.Contains(string(got), "State: stopped")
	}, "the stopped state")

	// A stopped run is not stoppable again.
	_, failure = h.Tool("subagent", map[string]any{"action": "stop", "id": runID})
	eq(t, failure, "No running or queued async run was found for '"+runID+"'.", "second stop")
	close(release)
}

func TestBgWaitWithNothingAndWithPrefixes(t *testing.T) {
	h, _, _ := asyncHost(t, nil)
	raw, failure := h.Tool("bg_wait", map[string]any{})
	eq(t, failure, "", "bg_wait")
	if !strings.Contains(string(raw), "No active async runs or registered provider work in this session. Nothing to wait for.") {
		t.Errorf("bg_wait: %s", raw)
	}
	raw, failure = h.Tool("bg_wait", map[string]any{"id": "no-such-run"})
	eq(t, failure, "", "bg_wait id")
	if !strings.Contains(string(raw), "No active run matched") || !strings.Contains(string(raw), "Nothing to wait for.") {
		t.Errorf("bg_wait id: %s", raw)
	}
	_, failure = h.Tool("bg_wait", map[string]any{"nonBlocking": true})
	eq(t, failure, "Non-blocking wait subscriptions require id so the registration can bind one exact run identity.", "nonBlocking needs id")
	_, failure = h.Tool("bg_wait", map[string]any{"nonBlocking": true, "id": "x", "all": true})
	eq(t, failure, "nonBlocking cannot be combined with all; subscribe to one exact run id.", "nonBlocking with all")
}

func TestForegroundIsExplicitAndDefaultFollowsConfig(t *testing.T) {
	h, calls, cwd := asyncHost(t, nil)
	_, failure := h.Tool("subagent", map[string]any{"agent": "helper", "task": "t", "async": false})
	eq(t, failure, "", "foreground")
	waitFor(t, func() bool { return calls.len() == 1 }, "the foreground child")

	// asyncByDefault:false in the original's config restores foreground default.
	home := withTempHome(t)
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "helper.md"), agentMD("helper", "Helps", "You help."))
	writeFile(t, filepath.Join(home, ".pi", "agent", "extensions", "subagent", "config.json"), `{"asyncByDefault": false}`)
	_, failure = h.Tool("subagent", map[string]any{"agent": "helper", "task": "t2"})
	eq(t, failure, "", "config default foreground")
	waitFor(t, func() bool { return calls.len() == 2 }, "the second child")
}

func TestNonInteractiveStartedMessageInPrintMode(t *testing.T) {
	sub.ResetRuns(t)
	t.Setenv("PI_SUBAGENTS_TEMP_ROOT", filepath.Join(t.TempDir(), "async-artifacts"))
	fakeChildren(t, nil)
	cwd := tmp(t)
	withTempHome(t)
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "helper.md"), agentMD("helper", "Helps", "You help."))
	hasUI := false
	h := StartHost(t, Extension(), HostOptions{
		Cwd: cwd, HasUI: &hasUI,
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "getSessionID" {
				return map[string]any{"sessionId": "s-headless"}, ""
			}
			return nil, ""
		},
	})
	raw, failure := h.Tool("subagent", map[string]any{"agent": "helper", "task": "t"})
	eq(t, failure, "", "start")
	if !strings.Contains(string(raw), "This is a non-interactive run: Pi auto-drains current-session subagent work at agent_end") {
		t.Errorf("headless started message: %s", raw)
	}
}
