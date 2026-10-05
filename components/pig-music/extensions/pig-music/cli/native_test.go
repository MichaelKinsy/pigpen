//go:build !windows

package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// nativeRig is a rig whose engine is the native one: the daemon is this test
// binary re-executed as `pigmusic serve`, playing synthetic tones through a
// software output, so nothing needs a network or a sound card.
func nativeRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t)
	t.Setenv(serveEnv, "1")
	t.Setenv("PIG_MUSIC_NATIVE_OUTPUT", "null")
	t.Setenv("PIG_MUSIC_NATIVE_OPENER", "synthetic")
	t.Cleanup(func() { // no daemon may outlive the test
		files, _ := filepath.Glob(filepath.Join(r.runDir, "pig-music", "native.sock"))
		_ = files
		killDaemons(r)
	})
	return r
}

func killDaemons(r *rig) {
	out, _ := os.ReadFile(filepath.Join(r.runDir, "pig-music", "native.log"))
	_ = out
	// The daemon's pid is the one holding native.lock; find it through /proc.
	entries, _ := os.ReadDir("/proc")
	lock := filepath.Join(r.runDir, "pig-music", "native.lock")
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		fds, _ := os.ReadDir("/proc/" + e.Name() + "/fd")
		for _, fd := range fds {
			if target, _ := os.Readlink("/proc/" + e.Name() + "/fd/" + fd.Name()); target == lock {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	}
}

func (r *rig) nativeEnv() Env {
	env := r.env()
	vars := map[string]string{"XDG_RUNTIME_DIR": r.runDir, "PIG_CODING_AGENT_DIR": r.agent, "HOME": r.agent, "PIG_MUSIC_ENGINE": "native", "PIG_MUSIC_SERVE": os.Args[0]}
	env.Getenv = func(k string) string { return vars[k] }
	env.Configure = nil
	return env
}

func (r *rig) runNative(args ...string) (string, string, int) {
	r.t.Helper()
	var out, errOut bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	code := Run(ctx, args, IO{Out: &out, Err: &errOut}, r.nativeEnv())
	return out.String(), errOut.String(), code
}

func (r *rig) nativeOK(args ...string) string {
	r.t.Helper()
	out, errOut, code := r.runNative(args...)
	if code != 0 {
		r.t.Fatalf("pigmusic %s exited %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), code, out, errOut)
	}
	return out
}

func TestNativeEngineThroughTheCommandLine(t *testing.T) {
	r := nativeRig(t)
	if out := r.nativeOK("status"); !strings.Contains(out, "stopped (the native player is not running)") {
		t.Fatalf("status with nothing running: %q", out)
	}
	if _, _, code := r.runNative("next"); code != 1 {
		t.Fatalf("next with nothing running exited %d", code)
	}
	r.nativeOK("search", "night", "drive") // the stub source: three tracks
	out := r.nativeOK("play", "2")
	if !strings.Contains(out, "Night Drive (Live)") {
		t.Fatalf("play: %q", out)
	}
	// Every command is a new process in real life: each call here builds a new Player and reattaches.
	deadline := time.Now().Add(5 * time.Second)
	for {
		out = r.nativeOK("status")
		if strings.Contains(out, "playing") && strings.Contains(out, "[2/3]") && !strings.Contains(out, "0:00 / 0:00") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never reached playing: %q", out)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(out, "5:01") { // the second result is 301 s long
		t.Fatalf("duration missing: %q", out)
	}
	for cmd, want := range map[string]string{"pause": "paused", "resume": "playing"} {
		if out := r.nativeOK(cmd); !strings.Contains(out, want) {
			t.Fatalf("%s: %q", cmd, out)
		}
	}
	r.nativeOK("volume", "50")
	if out := r.nativeOK("status"); !strings.Contains(out, "volume 50") {
		t.Fatalf("volume: %q", out)
	}
	r.nativeOK("seek", "30")
	if out := r.nativeOK("next"); !strings.Contains(out, "Night Drive Lofi") {
		t.Fatalf("next: %q", out)
	}
	if _, errOut, code := r.runNative("next"); code != 1 || !strings.Contains(errOut, "end of the queue") {
		t.Fatalf("next at the end: %d %q", code, errOut)
	}
	q := r.nativeOK("queue")
	if !strings.Contains(q, ">  3") || !strings.Contains(q, "Night Drive (Live)") {
		t.Fatalf("queue: %q", q)
	}
	r.nativeOK("jump", "1")
	r.nativeOK("move", "1", "3")
	if out := r.nativeOK("add", "1"); !strings.Contains(out, "queued") {
		t.Fatalf("add: %q", out)
	}
	if out := r.nativeOK("stop"); !strings.Contains(out, "stopped") {
		t.Fatalf("stop: %q", out)
	}
	if out := r.nativeOK("status"); !strings.Contains(out, "stopped (the native player is not running)") {
		t.Fatalf("status after stop: %q", out)
	}
	if _, err := os.Stat(filepath.Join(r.runDir, "pig-music", "native.sock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket left after stop: %v", err)
	}
}

func TestAutoFallsBackToNativeAndNamesWhy(t *testing.T) {
	r := nativeRig(t)
	env := r.nativeEnv()
	vars := map[string]string{"XDG_RUNTIME_DIR": r.runDir, "PIG_CODING_AGENT_DIR": r.agent, "HOME": r.agent, "PIG_MUSIC_SERVE": os.Args[0]}
	env.Getenv = func(k string) string { return vars[k] }
	env.LookPath = func(n string) (string, error) {
		if n == os.Args[0] {
			return n, nil
		}
		return "", errors.New("not found")
	}
	var out, errOut bytes.Buffer
	if code := Run(context.Background(), []string{"check"}, IO{Out: &out, Err: &errOut}, env); code != 0 {
		t.Fatalf("check: %d %q %q", code, out.String(), errOut.String())
	}
	if s := out.String(); !strings.Contains(s, "engine: native") || !strings.Contains(s, "mpv") || !strings.Contains(s, "player program") {
		t.Fatalf("check output:\n%s", s)
	}
}

func TestAnEngineNameThatIsNotOneFailsEveryCommand(t *testing.T) {
	r := newRig(t)
	env := r.env()
	vars := map[string]string{"XDG_RUNTIME_DIR": r.runDir, "PIG_CODING_AGENT_DIR": r.agent, "HOME": r.agent, "PIG_MUSIC_ENGINE": "winamp"}
	env.Getenv = func(k string) string { return vars[k] }
	var errOut bytes.Buffer
	if code := Run(context.Background(), []string{"status"}, IO{Out: &bytes.Buffer{}, Err: &errOut}, env); code != 1 || !strings.Contains(errOut.String(), "auto, mpv or native") {
		t.Fatalf("%d %q", code, errOut.String())
	}
}

func TestNativeOnTermuxIsRefusedWithTheFix(t *testing.T) {
	r := newRig(t)
	env := r.env()
	vars := map[string]string{"XDG_RUNTIME_DIR": r.runDir, "PIG_CODING_AGENT_DIR": r.agent, "HOME": r.agent, "PIG_MUSIC_ENGINE": "native", "TERMUX_VERSION": "0.118"}
	env.Getenv = func(k string) string { return vars[k] }
	var errOut bytes.Buffer
	if code := Run(context.Background(), []string{"search", "x"}, IO{Out: &bytes.Buffer{}, Err: &errOut}, env); code != 1 ||
		!strings.Contains(errOut.String(), "Termux") || !strings.Contains(errOut.String(), "pkg install mpv") {
		t.Fatalf("%d %q", code, errOut.String())
	}
}
