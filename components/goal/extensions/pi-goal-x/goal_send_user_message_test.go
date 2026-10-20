package pi_goal_x_test

// Not a twin of an upstream test: the adapter's answer to a host difference. Pi's pi.sendUserMessage returns to the command handler
// before the run it starts runs its handlers; PiG's host prepares the run inside the call and waits for the extension's own
// before_agent_start handler, so a command that holds the core lock across SendUserMessage (the guided /goal, /sisyphus and
// /goal-tweak start) deadlocks against its own event handler. The adapter releases the lock around the call.

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	goal "github.com/MichaelKinsy/pigpen/pi-goal-x"
)

func TestSendUserMessageDoesNotHoldTheCoreLock(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	if err := os.MkdirAll(filepath.Join(cwd, ".pi"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".pi", "pi-goal-x-settings.json"), []byte(`{"maxAutonomousRuns": 0}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var (
		host    *Host
		mu      sync.Mutex
		fired   = make(chan struct{})
		started bool
	)
	host = StartHost(t, goal.Extension(), HostOptions{Mode: "rpc", Cwd: cwd, OnCallValue: func(method string, args map[string]any) (any, string) {
		switch method {
		case "getSessionID":
			return map[string]any{"sessionId": "send-user-message"}, ""
		case "isIdle":
			return map[string]any{"idle": true}, ""
		case "sessionRead":
			if args["method"] == "getBranch" {
				return []any{}, ""
			}
		case "sendUserMessage":
			// The host's preparation of the run delivers before_agent_start to the extension and waits for its reply.
			mu.Lock()
			started = true
			mu.Unlock()
			host.Fire("before_agent_start", map[string]any{"prompt": "[GOAL CONFIRMATION focus=goal]", "systemPrompt": "system", "systemPromptOptions": map[string]any{}})
			close(fired)
		}
		return map[string]any{}, ""
	}})
	host.Fire("session_start", map[string]any{"reason": "startup"})
	if failure := host.Command("goal", "Refactor the parser"); failure != "" {
		t.Fatalf("/goal failed: %s", failure)
	}
	mu.Lock()
	defer mu.Unlock()
	if !started {
		t.Fatal("the drafting prompt was never sent")
	}
	select {
	case <-fired:
	case <-time.After(5 * time.Second):
		t.Fatal("before_agent_start never ran")
	}
}

// Pi's RPC mode writes a dialog's request first and runs the handler's queued UI update (a microtask) while the dialog waits: after
// a settings change the next menu's select request precedes the status and widget refresh. The adapter must show the host that order.
func TestDialogRequestPrecedesTheQueuedUIUpdate(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	if err := os.MkdirAll(filepath.Join(cwd, ".pi"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".pi", "pi-goal-x-settings.json"), []byte(`{"maxAutonomousRuns": 0}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	answers := []string{"  hideUnfocusedBanner: false (default)", "Set project override to true", "Done"}
	host := StartHost(t, goal.Extension(), HostOptions{Mode: "rpc", Cwd: cwd, OnCallValue: func(method string, args map[string]any) (any, string) {
		switch method {
		case "getSessionID":
			return map[string]any{"sessionId": "dialog-order"}, ""
		case "isIdle":
			return map[string]any{"idle": true}, ""
		case "sessionRead":
			if args["method"] == "getBranch" {
				return []any{}, ""
			}
		case "ui.select":
			mu.Lock()
			defer mu.Unlock()
			if len(answers) == 0 {
				return map[string]any{"selected": "", "ok": false}, ""
			}
			a := answers[0]
			answers = answers[1:]
			return map[string]any{"selected": a, "ok": true}, ""
		}
		return map[string]any{}, ""
	}})
	host.Fire("session_start", map[string]any{"reason": "startup"})
	before := len(host.Calls())
	if failure := host.Command("goal-settings", ""); failure != "" {
		t.Fatalf("/goal-settings failed: %s", failure)
	}
	var order []string
	for _, c := range host.Calls()[before:] {
		if c.Method == "ui.select" || c.Method == "ui.setStatus" {
			order = append(order, c.Method)
		}
	}
	want := []string{"ui.select", "ui.select", "ui.select", "ui.setStatus"}
	if len(order) != len(want) {
		t.Fatalf("host calls %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("host calls %v, want %v", order, want)
		}
	}
}
