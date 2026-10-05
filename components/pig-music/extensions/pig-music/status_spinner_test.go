package pig_music

import (
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

func statusFor(pos time.Duration, paused bool) string {
	return statusText(music.State{
		Connected: true, Paused: paused, Position: pos, Duration: 200 * time.Second,
		Track: &music.Track{ID: "x", Title: "Song 1", Artists: []string{"Some Artist"}},
	})
}

// The compact form of the spinning disc: a one-character record spinner, chosen by the whole seconds of the position so the
// status line changes no more often than it already does (the position is in it).
func TestTheStatusLineSpinsWhilePlayingByTheSecond(t *testing.T) {
	want := []string{"|", "/", "-", "\\", "|", "/"}
	for s, w := range want {
		got := statusFor(time.Duration(s)*time.Second+400*time.Millisecond, false)
		if !strings.HasPrefix(got, w+" Song 1 - Some Artist ") {
			t.Errorf("at %ds: %q, want the spinner %q", s, got, w)
		}
	}
}

func TestThePausedStatusLineStandsStill(t *testing.T) {
	a, b := statusFor(3*time.Second, true), statusFor(4*time.Second, true)
	if !strings.HasPrefix(a, "|| Song 1") || !strings.HasPrefix(b, "|| Song 1") {
		t.Errorf("%q %q", a, b)
	}
}
