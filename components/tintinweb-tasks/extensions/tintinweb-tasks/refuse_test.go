package tintinweb_tasks

import "testing"

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
