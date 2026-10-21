package schedule_prompt_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	schedule_prompt "github.com/MichaelKinsy/pigpen/schedule-prompt"
)

// Assertions that pin the mutation run's survivors in the extension's wiring (events, the widget in the host's modes).

func startRigWith(t *testing.T, opts HostOptions) *rig {
	t.Helper()
	cwd := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PIG_USE_PI_DIRS", "")
	r := &rig{cwd: cwd, session: "s1"}
	opts.Cwd, opts.OnCallValue = cwd, r.answer
	r.Host = StartHost(t, schedule_prompt.Extension(), opts)
	return r
}

func (r *rig) widgetViews() (shown, cleared int) {
	for _, c := range r.CallsTo("ui.setWidget") {
		if c.Args["view"] != nil {
			shown++
		} else {
			cleared++
		}
	}
	return
}

// In RPC mode a component widget is not drawn (only its removal reaches the client), as Pi's RPC mode does.
func TestWidgetViewIsNotSentInRPCMode(t *testing.T) {
	r := startRigWith(t, HostOptions{Mode: "rpc"})
	r.Fire("session_start", obj{"reason": "startup"})
	if _, failure := r.call(t, obj{"action": "add", "name": "w", "schedule": "0 0 9 * * *", "prompt": "p"}); failure != "" {
		t.Fatal(failure)
	}
	time.Sleep(700 * time.Millisecond)
	if shown, _ := r.widgetViews(); shown != 0 {
		t.Fatalf("%d widget views were sent in RPC mode", shown)
	}
	r.Fire("session_shutdown", obj{"reason": "quit"})
}

// Without a UI (print or JSON mode) nothing reaches ui.setWidget, not even the removal at the start.
func TestNoWidgetCallsWithoutAUI(t *testing.T) {
	no := false
	r := startRigWith(t, HostOptions{Mode: "json", HasUI: &no})
	r.Fire("session_start", obj{"reason": "startup"})
	if _, failure := r.call(t, obj{"action": "add", "name": "w", "schedule": "0 0 9 * * *", "prompt": "p"}); failure != "" {
		t.Fatal(failure)
	}
	time.Sleep(500 * time.Millisecond)
	r.Fire("session_shutdown", obj{"reason": "quit"})
	if n := len(r.CallsTo("ui.setWidget")); n != 0 {
		t.Fatalf("%d ui.setWidget calls without a UI", n)
	}
}

// A second `startup` start-up in one process (the original's reason check is the only guard) sweeps nothing: only the other reasons do.
func TestRepeatedStartupDoesNotSweep(t *testing.T) {
	seed := `{"jobs":[{"id":"mine","name":"mine","schedule":"0 0 * * * *","prompt":"p","enabled":false,"type":"cron","createdAt":"x","runCount":0,"session":"s1"}],"version":1}`
	r := startRig(t)
	r.Fire("session_start", obj{"reason": "startup"})
	if err := os.MkdirAll(filepath.Dir(r.storePath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.storePath(), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	r.Fire("session_start", obj{"reason": "startup"})
	if jobs := r.readStore(t)["jobs"].([]any); len(jobs) != 1 {
		t.Fatalf("a startup swept the disabled job: %v", jobs)
	}
	r.Fire("session_start", obj{"reason": "resume"})
	if jobs := r.readStore(t)["jobs"].([]any); len(jobs) != 0 {
		t.Fatalf("a resume kept the disabled job: %v", jobs)
	}
	r.Fire("session_shutdown", obj{"reason": "quit"})
}

func (r *rig) fires() int { return len(r.CallsTo("sendUserMessage")) }

// The scheduler of a session that ended fires no more.
func TestShutdownStopsTheScheduler(t *testing.T) {
	r := startRig(t)
	r.Fire("session_start", obj{"reason": "startup"})
	if _, failure := r.call(t, obj{"action": "add", "name": "tick", "schedule": "* * * * * *", "prompt": "p"}); failure != "" {
		t.Fatal(failure)
	}
	r.waitFor(t, "the first fire", func() bool { return r.fires() >= 1 })
	r.Fire("session_shutdown", obj{"reason": "quit"})
	time.Sleep(400 * time.Millisecond)
	n := r.fires()
	time.Sleep(2500 * time.Millisecond)
	if after := r.fires(); after != n {
		t.Fatalf("%d prompts after the shutdown, %d before", after, n)
	}
}

// A second session_start (reload, resume, fork) tears the first session's scheduler and widget down before it builds the new ones: the widget
// is removed, then drawn again (the old one would otherwise keep its refresh ticker and its scheduler would fire next to the new one).
func TestRestartTearsTheOldSessionDown(t *testing.T) {
	r := startRig(t)
	r.Fire("session_start", obj{"reason": "startup"})
	if _, failure := r.call(t, obj{"action": "add", "name": "w", "schedule": "0 0 9 * * *", "prompt": "p"}); failure != "" {
		t.Fatal(failure)
	}
	r.waitFor(t, "the widget", func() bool { shown, _ := r.widgetViews(); return shown >= 1 })
	time.Sleep(200 * time.Millisecond)
	mark := len(r.CallsTo("ui.setWidget"))
	r.Fire("session_start", obj{"reason": "reload"})
	r.waitFor(t, "the widget again", func() bool { return len(r.CallsTo("ui.setWidget")) >= mark+2 })
	calls := r.CallsTo("ui.setWidget")[mark:]
	if calls[0].Args["view"] != nil || calls[1].Args["view"] == nil {
		t.Fatalf("after the restart: %v", calls)
	}
	r.Fire("session_shutdown", obj{"reason": "quit"})
}
