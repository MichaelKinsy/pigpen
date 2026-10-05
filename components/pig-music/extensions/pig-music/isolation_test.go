package pig_music_test

import (
	"fmt"
	"os"
	"testing"
)

// TestMain keeps the package's tests off the real runtime and agent directories. Some tests build the extension without an
// environment of their own (it then reads the process's): before this, they wrote an "owner" token into the real
// $XDG_RUNTIME_DIR/pig-music, where a player that is really running keeps the token that decides whether quitting PiG stops
// the music, and read settings under the real PiG agent directory.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "pm-iso")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for k, sub := range map[string]string{"XDG_RUNTIME_DIR": "run", "PIG_HOME": "pighome", "PIG_CODING_AGENT_DIR": "agent"} {
		p := dir + "/" + sub
		_ = os.MkdirAll(p, 0o700)
		os.Setenv(k, p)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
