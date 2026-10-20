package tintinweb_tasks

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRefusesMaxVisibleAboveTen(t *testing.T) {
	want := "@pi-in-go/pigpen-tintinweb-tasks (Go port) does not support maxVisible above 10 yet. Install the original extension, npm:@tintinweb/pi-tasks@0.9.0, which runs unchanged in PiG."
	a := &app{cfg: tasksConfig{"maxVisible": float64(11)}}
	if err := a.refuseUnported(); err == nil || err.Error() != want {
		t.Fatalf("got %v", err)
	}
	for _, c := range []tasksConfig{{"maxVisible": float64(10)}, {"maxVisible": 5}, {}} {
		a.cfg = c
		if err := a.refuseUnported(); err != nil {
			t.Fatalf("%v: %v", c, err)
		}
	}
}

func TestRefusesNamedListAtOriginalLocation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PIG_HOME", filepath.Join(home, ".pig"))
	a := &app{cfg: tasksConfig{}, piTasks: "team"}
	if err := a.refuseUnported(); err != nil {
		t.Fatalf("no original list: %v", err)
	}
	orig := filepath.Join(home, ".pi", "tasks", "team.json")
	if err := os.MkdirAll(filepath.Dir(orig), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orig, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.refuseUnported(); err == nil {
		t.Fatal("expected refusal")
	}
	own := filepath.Join(home, ".pig", "agent", "tasks", "team.json")
	if err := os.MkdirAll(filepath.Dir(own), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(own, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.refuseUnported(); err != nil {
		t.Fatalf("list present in port dir: %v", err)
	}
}
