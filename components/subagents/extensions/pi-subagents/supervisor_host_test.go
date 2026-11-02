package pi_subagents_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	sub "github.com/MichaelKinsy/pigpen/pi-subagents"
)

// The supervisor tool through the host boundary.

func TestSupervisorToolThroughTheHost(t *testing.T) {
	sub.ResetRuns(t)
	tempRoot := filepath.Join(t.TempDir(), "async-artifacts")
	t.Setenv("PI_SUBAGENTS_TEMP_ROOT", tempRoot)
	fakeChildren(t, nil)
	cwd := tmp(t)
	withTempHome(t)
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "helper.md"), agentMD("helper", "Helps", "You help."))
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	sub.SetRunChild(t, func(done <-chan struct{}, req sub.ChildRequest) (sub.ChildResult, error) {
		started <- struct{}{}
		select {
		case <-release:
			return sub.ChildResult{Stdout: "ok"}, nil
		case <-done:
			return sub.ChildResult{}, sub.ErrCancelled
		}
	})
	h := StartHost(t, Extension(), HostOptions{
		Cwd: cwd,
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "getSessionID" {
				return map[string]any{"sessionId": "session-supervisor"}, ""
			}
			return nil, ""
		},
	})
	raw, failure := h.Tool("subagent", map[string]any{"agent": "helper", "task": "t"})
	eq(t, failure, "", "start a run")
	runID := asyncIDRe.FindStringSubmatch(string(raw))[1]
	<-started

	// Status with nothing pending carries the original's sentence.
	status, failure := h.Tool("subagent_supervisor", map[string]any{"action": "status"})
	eq(t, failure, "", "status")
	if !strings.Contains(resultContent(t, status), "Native supervisor channel active. Pending replies: 0.") {
		t.Errorf("status: %s", status)
	}
	pending, failure := h.Tool("subagent_supervisor", map[string]any{"action": "pending"})
	eq(t, failure, "", "pending")
	eq(t, resultContent(t, pending), "No pending supervisor requests.", "empty pending")

	// The child asks a question: the file lands in the run's channel directory.
	runDir := filepath.Join(tempRoot, "async-subagent-runs", runID)
	reqDir := filepath.Join(runDir, "supervisor", "requests")
	if err := os.MkdirAll(reqDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(reqDir, "ask-1.json"), `{"id":"ask-1","message":"Proceed with the migration?","ts":1}`)

	pending, failure = h.Tool("subagent_supervisor", map[string]any{"action": "list"})
	eq(t, failure, "", "list")
	text := resultContent(t, pending)
	if !strings.Contains(text, "- ask-1: helper [") || !strings.Contains(text, "Proceed with the migration?") || !strings.Contains(text, "Reply: subagent_supervisor({ action: \"reply\", replyTo: \"ask-1\"") {
		t.Errorf("pending list:\n%s", text)
	}

	// The run's status reports the attention state while the question is open.
	status, failure = h.Tool("subagent", map[string]any{"action": "status", "id": runID})
	eq(t, failure, "", "run status")
	if !strings.Contains(resultContent(t, status), "Activity: needs attention") {
		t.Errorf("run status: %s", status)
	}

	// An unknown replyTo keeps the original's error.
	_, failure = h.Tool("subagent_supervisor", map[string]any{"action": "reply", "replyTo": "nope", "message": "x"})
	eq(t, failure, "No pending supervisor request found for replyTo 'nope'.", "unknown reply")

	// The reply resolves it and lands as the child's answer file.
	reply, failure := h.Tool("subagent_supervisor", map[string]any{"action": "reply", "replyTo": "ask-1", "message": "Proceed."})
	eq(t, failure, "", "reply")
	eq(t, resultContent(t, reply), "Replied to supervisor request ask-1.", "reply text")
	body, err := os.ReadFile(filepath.Join(runDir, "supervisor", "replies", "ask-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"message":"Proceed."`) {
		t.Errorf("reply file: %s", body)
	}
	pending, failure = h.Tool("subagent_supervisor", map[string]any{"action": "pending"})
	eq(t, resultContent(t, pending), "No pending supervisor requests.", "resolved")
	close(release)
}
