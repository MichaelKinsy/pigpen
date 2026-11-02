package pi_subagents_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	sub "github.com/MichaelKinsy/pigpen/pi-subagents"
)

// Steering through the host boundary: the original's checks and receipts,
// queued to the run's steer directory.

func TestSteerActionThroughTheHost(t *testing.T) {
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
				return map[string]any{"sessionId": "session-steer"}, ""
			}
			return nil, ""
		},
	})
	raw, failure := h.Tool("subagent", map[string]any{"agent": "helper", "task": "t"})
	eq(t, failure, "", "start a run")
	runID := asyncIDRe.FindStringSubmatch(string(raw))[1]
	<-started

	// A live run takes the message and the receipt is the original's.
	steered, failure := h.Tool("subagent", map[string]any{"action": "steer", "id": runID, "message": "Prefer the smaller diff."})
	eq(t, failure, "", "steer")
	text := resultContent(t, steered)
	if !strings.HasPrefix(text, "Steering scheduled for async run "+runID+" (request ") || !strings.Contains(text, ").\n\nMessage sent:\n") {
		t.Errorf("receipt line: %s", text)
	}
	if !strings.Contains(text, "\n\nMessage sent:\n```text\nPrefer the smaller diff.\n```") {
		t.Errorf("fenced message:\n%s", text)
	}
	// The message waits in the run's steer directory.
	steerDir := filepath.Join(tempRoot, "async-subagent-runs", runID, "supervisor", "steer")
	entries, err := os.ReadDir(steerDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("steer dir: %v %v", entries, err)
	}
	body, err := os.ReadFile(filepath.Join(steerDir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"message":"Prefer the smaller diff."`) || !strings.Contains(string(body), `"source":"steer-action"`) {
		t.Errorf("steer file: %s", body)
	}

	// A fenced message gets a longer fence, as the original's receipt does.
	steered, failure = h.Tool("subagent", map[string]any{"action": "steer", "id": runID, "message": "see ```this```"})
	if !strings.Contains(resultContent(t, steered), "````text\nsee ```this```\n````") {
		t.Errorf("fence growth: %s", steered)
	}

	// A pending supervisor ask blocks steering with the reply hint.
	runDir := filepath.Join(tempRoot, "async-subagent-runs", runID)
	reqDir := filepath.Join(runDir, "supervisor", "requests")
	if err := os.MkdirAll(reqDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(reqDir, "ask-9.json"), `{"id":"ask-9","message":"Which branch?","ts":1}`)
	_, failure = h.Tool("subagent", map[string]any{"action": "steer", "id": runID, "message": "go on"})
	eq(t, failure, "Steering not delivered or queued: async run "+runID+" is blocked on a pending supervisor ask. No reply or recovery was attempted. Reply explicitly:\nsubagent_supervisor({ \"action\": \"reply\", \"replyTo\": \"ask-9\", \"message\": \"<explicit answer>\" })", "blocked steer")
	// Answering unblocks it.
	_, failure = h.Tool("subagent_supervisor", map[string]any{"action": "reply", "replyTo": "ask-9", "message": "main."})
	eq(t, failure, "", "reply")

	// An unknown id is reported as not in the session.
	_, failure = h.Tool("subagent", map[string]any{"action": "steer", "id": "nope", "message": "x"})
	eq(t, failure, "Async run 'nope' was not found in the active session.", "unknown id")

	// A stopped run cannot be steered.
	_, failure = h.Tool("subagent", map[string]any{"action": "stop", "id": runID})
	eq(t, failure, "", "stop")
	waitFor(t, func() bool {
		status, failure := h.Tool("subagent", map[string]any{"action": "status", "id": runID})
		return failure == "" && strings.Contains(string(status), "State: stopped")
	}, "the stopped run")
	_, failure = h.Tool("subagent", map[string]any{"action": "steer", "id": runID, "message": "x"})
	eq(t, failure, "Async run '"+runID+"' is not running or queued and cannot be steered.", "terminal steer")
	close(release)
}

func TestSteerCommandThroughTheHost(t *testing.T) {
	sub.ResetRuns(t)
	t.Setenv("PI_SUBAGENTS_TEMP_ROOT", filepath.Join(t.TempDir(), "async-artifacts"))
	fakeChildren(t, nil)
	cwd := tmp(t)
	withTempHome(t)
	writeFile(t, filepath.Join(cwd, ".pi", "agents", "helper.md"), agentMD("helper", "Helps", "You help."))
	h := StartHost(t, Extension(), HostOptions{
		Cwd: cwd,
		OnCall: func(method string, args map[string]any) (map[string]any, string) {
			if method == "getSessionID" {
				return map[string]any{"sessionId": "session-steer-cmd"}, ""
			}
			return nil, ""
		},
	})
	// Without arguments the command reports the usage.
	eq(t, h.Command("subagents-steer", ""), "", "usage")
	texts := slashTexts(h, "subagent-slash-text")
	if !strings.Contains(texts[len(texts)-1], "Usage: /subagents-steer <run-id> [--child <child-id>] <message>") {
		t.Errorf("usage: %v", texts)
	}
}
