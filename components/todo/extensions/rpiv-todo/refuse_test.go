package rpiv_todo

import "testing"

func TestRefusesMaxWidgetLinesAboveTwelve(t *testing.T) {
	w, _ := configHome(t)
	want := "@pi-in-go/pigpen-todo (Go port) does not support maxWidgetLines above 12 yet. Install the original extension, npm:@juicesharp/rpiv-todo@2.11.0, which runs unchanged in PiG."
	w(`{"maxWidgetLines":13}`)
	err := refuseUnportedConfig()
	if err == nil || err.Error() != want {
		t.Fatalf("got %v", err)
	}
	for _, c := range []string{`{"maxWidgetLines":12}`, `{"maxWidgetLines":5}`, `{}`, `{"maxWidgetLines":"99"}`} {
		w(c)
		if err := refuseUnportedConfig(); err != nil {
			t.Fatalf("%s: %v", c, err)
		}
	}
}
