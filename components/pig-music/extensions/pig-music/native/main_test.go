package native

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// The test binary doubles as `pigmusic serve`: set PIG_MUSIC_NATIVE_TEST to a
// mode and run it with `serve --dir D ...`.
const testModeEnv = "PIG_MUSIC_NATIVE_TEST"

func TestMain(m *testing.M) {
	if mode := os.Getenv(testModeEnv); mode != "" {
		os.Exit(fakeServe(mode, os.Args[1:]))
	}
	os.Exit(m.Run())
}

func fakeServe(mode string, args []string) int {
	if len(args) > 0 && args[0] == "native" {
		return fakeHelper(mode, args[1:])
	}
	dir := ""
	for i, a := range args {
		if a == "--dir" && i+1 < len(args) {
			dir = args[i+1]
		}
	}
	if dir != "" {
		_ = os.MkdirAll(dir, 0o700)
		// Record the process, as the mpv tests do, so a test can find it.
		_ = os.WriteFile(filepath.Join(dir, "pid."+strconv.Itoa(os.Getpid())), []byte(fmt.Sprint(args)), 0o600)
	}
	switch mode {
	case "exit":
		fmt.Fprintln(os.Stderr, `boom: Get "https://rr1.googlevideo.com/videoplayback?sig=SECRET": dial tcp 203.0.113.9:443: refused`)
		return 3
	case "hang":
		time.Sleep(time.Hour)
		return 0
	}
	frames := map[string]int64{}
	for i := 1; i <= 20; i++ {
		frames["t"+strconv.Itoa(i)] = OutputRate * 120
	}
	deps := &ServeDeps{
		NewOpener: func() (Opener, error) { return newOpener(frames), nil },
		NewOutput: func(string) (Output, error) { return NewNullOutput(), nil },
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return Serve(ctx, args[1:], os.Getenv, os.Stderr, deps)
}
