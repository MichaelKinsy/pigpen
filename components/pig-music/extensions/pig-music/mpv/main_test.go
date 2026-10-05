package mpv

import (
	"os"
	"testing"

	"github.com/MichaelKinsy/pigpen/pig-music/internal/mpvfake"
)

// fakeEnv makes the test binary act as mpv when the player starts it.
const fakeEnv = "PIG_MUSIC_FAKE_MPV"

func TestMain(m *testing.M) {
	if os.Getenv(fakeEnv) == "silent" {
		if err := mpvfake.RunSilent(os.Args[1:]); err != nil {
			os.Stderr.WriteString(err.Error() + "\n")
			os.Exit(1)
		}
		return
	}
	if os.Getenv(fakeEnv) == "1" {
		if err := mpvfake.Run(os.Args[1:]); err != nil {
			os.Stderr.WriteString(err.Error() + "\n")
			os.Exit(1)
		}
		return
	}
	os.Exit(m.Run())
}
