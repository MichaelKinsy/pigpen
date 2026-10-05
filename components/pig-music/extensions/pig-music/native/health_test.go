package native

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

func TestAudioCheckLinux(t *testing.T) {
	dir := t.TempDir()
	run := filepath.Join(dir, "run")
	_ = os.MkdirAll(filepath.Join(run, "pulse"), 0o700)
	sock := filepath.Join(run, "pulse", "native")
	env := map[string]string{"XDG_RUNTIME_DIR": run}
	getenv := func(k string) string { return env[k] }
	noLibs := func(string) []string { return nil }

	// Neither a sound server nor ALSA: a failing check with a fix command.
	c := audioCheck("linux", getenv, noLibs)
	if c.OK || c.Fix == "" || !strings.Contains(c.Fix, "libasound2") {
		t.Fatalf("%+v", c)
	}
	// A PulseAudio or PipeWire socket is enough.
	_ = os.WriteFile(sock, nil, 0o600)
	if c := audioCheck("linux", getenv, noLibs); !c.OK || !strings.Contains(c.Detail, "PulseAudio") {
		t.Fatalf("%+v", c)
	}
	_ = os.Remove(sock)
	// So is libasound.
	if c := audioCheck("linux", getenv, func(string) []string { return []string{"/usr/lib/x86_64-linux-gnu/libasound.so.2"} }); !c.OK || !strings.Contains(c.Detail, "ALSA") {
		t.Fatalf("%+v", c)
	}
	for _, goos := range []string{"darwin", "windows"} {
		if c := audioCheck(goos, getenv, noLibs); !c.OK {
			t.Errorf("%s needs nothing installed: %+v", goos, c)
		}
	}
	if c := audioCheck("android", getenv, noLibs); c.OK || !strings.Contains(c.Detail, "Termux") {
		t.Fatalf("android: %+v", c)
	}
}

func TestHealthReport(t *testing.T) {
	r := CheckHealth(context.Background(), HealthOptions{
		GOOS: "linux", Getenv: func(string) string { return "" }, Glob: func(string) []string { return []string{"/x/libasound.so.2"} },
		ServeFound: func() error { return errors.New("pigmusic was not found") },
	})
	if r.OK() {
		t.Fatal("a missing player program must fail the report")
	}
	var names []string
	for _, c := range r.Checks {
		names = append(names, c.Name)
		if !c.OK && c.Fix == "" {
			t.Errorf("%s fails without a fix to copy", c.Name)
		}
	}
	if strings.Join(names, ",") != "platform,audio output,player program" {
		t.Fatalf("checks %v", names)
	}
	if !strings.Contains(r.String(), "pigmusic was not found") {
		t.Fatalf("report text %q", r.String())
	}
	ok := CheckHealth(context.Background(), HealthOptions{GOOS: "darwin", Getenv: func(string) string { return "" }, ServeFound: func() error { return nil }})
	if !ok.OK() {
		t.Fatalf("%s", ok.String())
	}
}

func TestHealthRunsTheProbeItIsGiven(t *testing.T) {
	r := CheckHealth(context.Background(), HealthOptions{
		GOOS: "darwin", Getenv: func(string) string { return "" }, ServeFound: func() error { return nil },
		Probe: func(context.Context) Check { return Check{Name: "stream probe", OK: true, Detail: "resolved"} },
	})
	last := r.Checks[len(r.Checks)-1]
	if last.Name != "stream probe" || !last.OK {
		t.Fatalf("%+v\n%s", last, r.String())
	}
	none := CheckHealth(context.Background(), HealthOptions{GOOS: "darwin", Getenv: func(string) string { return "" }, ServeFound: func() error { return nil }})
	for _, c := range none.Checks {
		if c.Name == "stream probe" {
			t.Errorf("a probe ran that nobody asked for")
		}
	}
}

// `pigmusic serve` refuses on Termux with the same message as the engine
// choice, also when the binary was built for linux rather than android.
func TestServeRefusesTermux(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"TERMUX_VERSION": {"TERMUX_VERSION": "0.118"},
		"PREFIX":         {"PREFIX": "/data/data/com.termux/files/usr"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := shortDir(t)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			var out strings.Builder
			code := Serve(ctx, []string{"--dir", dir, "--output", "null", "--opener", "synthetic"}, func(k string) string { return env[k] }, &out, nil)
			if code != 1 || !strings.Contains(out.String(), "Termux") || !strings.Contains(out.String(), "pkg install mpv") {
				t.Fatalf("exit %d, output %q", code, out.String())
			}
			if _, err := os.Stat(Endpoint(music.PathsIn(dir))); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("a socket was created: %v", err)
			}
		})
	}
}
