package pi_subagents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Missions: the original's action texts and durable store.

func TestMissionLifecycle(t *testing.T) {
	isolateTemp(t)
	home := withTempHome(t)
	cwd := tmp(t)

	// create validates like the original.
	r := missionAction(cwd, "mission.create", map[string]any{})
	eq(t, r.text, "mission must be an object", "missing mission")
	r = missionAction(cwd, "mission.create", map[string]any{"mission": map[string]any{"bogus": 1}})
	eq(t, r.text, "mission.bogus is unknown", "unknown key")
	r = missionAction(cwd, "mission.create", map[string]any{"mission": map[string]any{"title": "T", "summary": "S"}})
	eq(t, r.text, "mission.title and mission.summary cannot both be set", "title and summary")
	r = missionAction(cwd, "mission.create", map[string]any{"mission": map[string]any{"goal": true}})
	eq(t, r.text, "mission.title or mission.summary must be a non-empty string", "no title")
	r = missionAction(cwd, "mission.create", map[string]any{"mission": map[string]any{"title": "T", "goal": true}})
	eq(t, r.text, "mission.budget is required when mission.goal is true", "goal needs budget")
	r = missionAction(cwd, "mission.create", map[string]any{"mission": map[string]any{"title": "T", "budget": map[string]any{"tokens": 0}}})
	eq(t, r.text, "mission.budget.tokens must be a positive integer", "budget validation")

	// create → the original's headline.
	r = missionAction(cwd, "mission.create", map[string]any{
		"mission": map[string]any{"title": "Ship the port", "objective": "Close the gaps", "labels": []any{"ports", "go"}},
	})
	if r.isError || !strings.HasPrefix(r.text, "Created mission ") || !strings.HasSuffix(r.text, ": Ship the port") {
		t.Fatalf("create: %q", r.text)
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.text, "Created mission "), ": Ship the port")

	// The record is durable in the original's store location.
	recordPath := filepath.Join(home, ".pi", "agent", "missions", "projects")
	if !dirExists(recordPath) {
		t.Fatalf("store missing: %s", recordPath)
	}

	// list/show.
	r = missionAction(cwd, "mission.list", map[string]any{})
	if !strings.Contains(r.text, id) || !strings.Contains(r.text, "planned") || !strings.Contains(r.text, "Ship the port") {
		t.Errorf("list: %q", r.text)
	}
	r = missionAction(cwd, "mission.show", map[string]any{"missionId": id})
	for _, want := range []string{"Mission: " + id, "Title: Ship the port", "Status: planned", "Objective: Close the gaps", "Labels: ports, go", "State: "} {
		if !strings.Contains(r.text, want) {
			t.Errorf("show missing %q:\n%s", want, r.text)
		}
	}

	// update + attach-run + resolve-decision change the record.
	r = missionAction(cwd, "mission.update", map[string]any{"missionId": id, "missionUpdate": map[string]any{
		"status":    "needs_decision",
		"decisions": []any{map[string]any{"id": "pick-a", "title": "Pick A or B"}},
	}})
	if !strings.Contains(r.text, "Updated mission "+id+".") || !strings.Contains(r.text, "pick-a: open — Pick A or B") {
		t.Errorf("update: %q", r.text)
	}
	r = missionAction(cwd, "mission.attach-run", map[string]any{"missionId": id, "runId": "run-123", "runMode": "single", "agent": "worker"})
	eq(t, r.text, "Attached run run-123 to mission "+id+".", "attach-run")
	r = missionAction(cwd, "mission.show", map[string]any{"missionId": id})
	if !strings.Contains(r.text, "run-123 (single)") || !strings.Contains(r.text, "Status: active") {
		t.Errorf("after attach:\n%s", r.text)
	}
	r = missionAction(cwd, "mission.resolve-decision", map[string]any{"missionId": id, "id": "pick-a", "summary": "Chose A."})
	if !strings.Contains(r.text, "Resolved decision pick-a for mission "+id+".") || !strings.Contains(r.text, "pick-a: resolved — Pick A or B; resolution: Chose A.") {
		t.Errorf("resolve: %q", r.text)
	}

	// close keeps the original's statuses and text.
	r = missionAction(cwd, "mission.close", map[string]any{"missionId": id, "missionStatus": "suspended"})
	eq(t, r.text, "mission.close missionStatus must be completed, failed, or cancelled", "close status validation")
	r = missionAction(cwd, "mission.close", map[string]any{"missionId": id, "summary": "Shipped."})
	eq(t, r.text, "Closed mission "+id+" as completed.", "close")
	r = missionAction(cwd, "mission.show", map[string]any{"missionId": id})
	if !strings.Contains(r.text, "Status: completed") || !strings.Contains(r.text, "Summary: Shipped.") {
		t.Errorf("after close:\n%s", r.text)
	}

	// An unknown mission keeps the original's message.
	r = missionAction(cwd, "mission.show", map[string]any{"missionId": "00000000-0000-0000-0000-000000000000"})
	eq(t, r.text, "Mission '00000000-0000-0000-0000-000000000000' not found.", "not found")
	// The global index is empty in this port (its workflows do not run).
	r = missionAction(cwd, "mission.list", map[string]any{"missionScope": "global"})
	eq(t, r.text, "No indexed missions.", "global list")
	r = missionAction(cwd, "mission.list", map[string]any{"missionScope": "team"})
	eq(t, r.text, "missionScope must be \"project\" or \"global\"", "scope validation")
}

// dirExists reports whether a path is a directory.
func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
