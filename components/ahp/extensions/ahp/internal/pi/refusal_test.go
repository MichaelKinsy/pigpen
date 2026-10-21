package pi_test

import (
	"testing"

	"github.com/MichaelKinsy/pigpen/ahp/internal/pi"
)

func TestUnsupportedWording(t *testing.T) {
	want := "@pi-in-go/pigpen-ahp (Go port) does not support creating a session yet. The original, " +
		"git:github.com/Qusic/pi-ahp@4065e98309b03c1d2d916300856c3c21a9c10674, is a standalone Node host and runs on its own, outside PiG."
	if got := pi.Unsupported("creating a session"); got != want {
		t.Fatalf("got %q", got)
	}
}
