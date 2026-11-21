package pi_subagents_test

// A resumed session gets its fleet back (sidecar.go): session_start re-attaches the session's runs — the async widget
// shows them and the status action reads them — session_shutdown leaves the sidecar behind for the next start, and a
// session without one starts with an empty fleet. The host runs in rpc mode so the widget's rows arrive as
// ui.setWidget calls the fake host records.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resumeSidecar is a sidecar as a recovered session writes it: one paused run with its child session pinned and its
// launch recorded — no environment (the restore rebuilds it).
const resumeSidecar = `{
  "version": 1,
  "session": "s1",
  "next": 8,
  "runs": [
    {
      "id": "run-7",
      "mode": "single",
      "name": "delegate",
      "task": "the dialogs task",
      "cwd": "C:/work/dialogs",
      "startedAt": "2026-11-21T05:17:17Z",
      "endedAt": "2026-11-21T06:18:36Z",
      "turnNotice": "Background task",
      "sessionDir": "C:/sessions",
      "sessionId": "child-7",
      "child": {
        "bin": "pig",
        "args": ["--print", "--session-dir", "C:/sessions", "--session-id", "child-7"],
        "stdin": "Task: the dialogs task",
        "cwd": "C:/work/dialogs"
      },
      "steps": [
        {
          "agent": "delegate",
          "task": "the dialogs task",
          "state": "paused",
          "startedAt": "2026-11-21T05:17:18Z",
          "endedAt": "2026-11-21T06:18:36Z",
          "session": "C:/sessions/child-7.jsonl",
          "noticeKind": "Background task"
        }
      ]
    }
  ]
}
`

// widgetRows is the last rows the named widget was given; a content that is not an array clears it.
func widgetRows(t *testing.T, h *Host, key string) []string {
	t.Helper()
	var rows []string
	seen := false
	for _, c := range h.CallsTo("ui.setWidget") {
		if c.Args["key"] != key {
			continue
		}
		seen, rows = true, nil
		if l, ok := c.Args["content"].([]any); ok {
			for _, s := range l {
				rows = append(rows, s.(string))
			}
		}
	}
	if !seen {
		t.Fatalf("the %s widget was never set", key)
	}
	return rows
}

func TestResumeReattachesTheFleet(t *testing.T) {
	cwd := tmp(t)
	sessions := tmp(t)
	writeFile(t, filepath.Join(sessions, "s1.subagents.json"), resumeSidecar)
	session := "s1"
	h := StartHost(t, Extension(), HostOptions{Cwd: cwd, Mode: "rpc", OnCallValue: func(method string, args map[string]any) (any, string) {
		switch method {
		case "getSessionFile":
			return map[string]any{"sessionFile": filepath.Join(sessions, session+".jsonl")}, ""
		case "sessionRead":
			switch args["method"] {
			case "getSessionDir":
				return sessions, ""
			case "getSessionId":
				return session, ""
			}
		}
		return map[string]any{}, ""
	}})

	h.Fire("session_start", map[string]any{"reason": "resume"})

	// the async widget shows the restored run again
	rows := widgetRows(t, h, "subagent-async")
	if len(rows) == 0 || !strings.Contains(strings.Join(rows, "\n"), "paused") {
		t.Errorf("the async widget does not show the restored run: %q", rows)
	}

	// the status action reads it: the run, its paused state and the hint to revive its child session
	raw, failure := h.Tool("subagent", map[string]any{"action": "status", "id": "run-7"})
	eq(t, failure, "", "status")
	for _, want := range []string{"Run: run-7", "State: paused", "Revive: subagent"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("status is missing %q: %s", want, raw)
		}
	}

	// the session's shutdown leaves the sidecar for the next resume to find
	h.Fire("session_shutdown", map[string]any{"reason": "quit"})
	data, err := os.ReadFile(filepath.Join(sessions, "s1.subagents.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"state": "paused"`) || !strings.Contains(string(data), "run-7") {
		t.Errorf("the shutdown damaged the sidecar: %s", data)
	}

	// a session with no sidecar starts clean
	session = "s2"
	h.Fire("session_start", map[string]any{"reason": "startup"})
	if rows := widgetRows(t, h, "subagent-async"); len(rows) != 0 {
		t.Errorf("a session without a sidecar starts with rows: %q", rows)
	}
	if _, failure := h.Tool("subagent", map[string]any{"action": "status", "id": "run-7"}); !strings.Contains(failure, "not found") {
		t.Errorf("run-7 leaked into the new session: %q", failure)
	}
	h.Fire("session_shutdown", map[string]any{"reason": "quit"})
}
