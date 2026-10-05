package main

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/pig-music/native/wax"
)

// Review of M9: the audio device is not linked into the extension (fused into the batteries Binary), so `pigmusic serve`,
// the program that plays, must still have it, together with the opener and the decoder.
func TestPigmusicHasTheEngineAndTheAudioDevice(t *testing.T) {
	d := wax.Deps()
	if d.NewOpener == nil || d.NewOutput == nil {
		t.Fatal("pigmusic was built without the native engine: `pigmusic serve` could not play")
	}
}

// Review of M9 (and M9b): the audio device and the rest of the native engine live in native/wax, which only this program
// links; `pigmusic serve` is the program that plays, so it must still have them.
func TestPigmusicHasTheAudioDevice(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list")
	}
	out, err := exec.Command("go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	deps := " " + strings.Join(strings.Fields(string(out)), " ") + " "
	for _, want := range []string{"github.com/ebitengine/oto/v3", "github.com/MichaelKinsy/pigpen/pig-music/native/wax"} {
		if !strings.Contains(deps, " "+want+" ") {
			t.Errorf("pigmusic does not link %s: `pigmusic serve` could not play", want)
		}
	}
}
