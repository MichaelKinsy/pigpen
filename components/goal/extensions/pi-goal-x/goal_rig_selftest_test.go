package pi_goal_x_test

import (
	"strings"
	"testing"
)

// The rig's own checks: what a slice's twins rely on it to observe.

func TestGoalRigObservesTheExtension(t *testing.T) {
	r := newGoalRig(t, rigOptions{StartSession: true})
	if failure := r.Command("goal-direct", "Ship the rig"); failure != "" {
		t.Fatal(failure)
	}
	if text, found := r.LastStatus("goal"); !found || text != "" {
		t.Errorf("status %q found=%v: the focused goal's footer status is cleared", text, found)
	}
	if lines := r.LastWidgetLines("goal"); !strings.Contains(lines, "Ship the rig") {
		t.Errorf("widget lines %q", lines)
	}
	es := r.Entries()
	if len(es) != 1 || es[0].CustomType != "pi-goal-focus" || es[0].Data["reason"] != "created" {
		t.Errorf("entries %+v", es)
	}
	if g := r.Probe.Goal(); g == nil || g["objective"] != "Ship the rig" || r.Probe.FocusedGoalID() != g["id"] {
		t.Errorf("probe goal %v", g)
	}
	if len(r.Branch()) != 1 {
		t.Errorf("an appended entry must be visible in the branch: %v", r.Branch())
	}
}

func TestGoalRigScriptsDialogsAndDropsRPCWidgets(t *testing.T) {
	r := newGoalRig(t, rigOptions{Mode: "rpc", Confirm: func(title, msg string) bool { return title == "Clear goal?" }, StartSession: true})
	r.Command("goal-direct", "Over rpc")
	for _, w := range r.Widgets() {
		if !w.Clear {
			t.Errorf("RPC host kept a widget push: %+v", w)
		}
	}
	r.Command("goal-clear", "")
	if n := r.LastNotice(); n != "info: Goal cleared and archived." {
		t.Errorf("notice %q", n)
	}
}
