//go:build linux || darwin

package svc

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
)

// Twins of upstream test/pty.test.ts: a real shell on a real pseudoterminal.

type ptyRun struct {
	mu     sync.Mutex
	output strings.Builder
	exited chan int
	onData func(all string)
}

func spawnRun(t *testing.T, file string, args []string, onData func(*ptyRun, string)) (*ptyRun, PtyProcess) {
	t.Helper()
	run := &ptyRun{exited: make(chan int, 1)}
	pty, err := SpawnPty(file, args, PtyOptions{Name: "xterm-256color", Cols: 80, Rows: 24, Cwd: t.TempDir()}, PtyHandlers{
		OnData: func(data string) {
			run.mu.Lock()
			run.output.WriteString(data)
			all := run.output.String()
			run.mu.Unlock()
			if onData != nil {
				onData(run, all)
			}
		},
		OnExit: func(code int) { run.exited <- code },
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	t.Cleanup(func() { _ = pty.Kill() })
	return run, pty
}

func (r *ptyRun) Output() string { r.mu.Lock(); defer r.mu.Unlock(); return r.output.String() }

func (r *ptyRun) wait(t *testing.T) int {
	t.Helper()
	select {
	case code := <-r.exited:
		return code
	case <-time.After(5 * time.Second):
		t.Fatalf("PTY did not exit; output: %q", r.Output())
		return -1
	}
}

func TestPty(t *testing.T) {
	twin.Run(t, "pty", "round-trips input through a resized TTY", func(t *testing.T) {
		marker := "pi-ahp-" + newUUID()
		script := `test -t 0 && test -t 1 && test -t 2 || exit 70; test "$TERM" = xterm-256color || exit 71; IFS= read -r line; set -- $(stty size); printf "PTY:%s:%sx%s\n" "$line" "$1" "$2"`
		run, pty := spawnRun(t, "sh", []string{"-c", script}, nil)
		if err := pty.Resize(97, 31); err != nil {
			t.Fatal(err)
		}
		if err := pty.Write(marker + "\r"); err != nil {
			t.Fatal(err)
		}
		if code := run.wait(t); code != 0 {
			t.Fatalf("PTY setup failed (exit %d); output: %q", code, run.Output())
		}
		expected := "PTY:" + marker + ":31x97"
		if !strings.Contains(run.Output(), expected) {
			t.Fatalf("missing %s in %q", expected, run.Output())
		}
	})

	twin.Run(t, "pty", "delivers Ctrl-C to the foreground job without killing the shell", func(t *testing.T) {
		marker := strings.ReplaceAll(newUUID(), "-", "")
		childScript := `trap 'printf "INT:%s\n" "$MARKER"; exit 42' INT; printf "READY:%s\n" "$MARKER"; while :; do sleep 1; done`
		stage := 0
		var pty PtyProcess
		var mu sync.Mutex
		run, p := spawnRun(t, "sh", nil, func(_ *ptyRun, all string) {
			mu.Lock()
			defer mu.Unlock()
			if stage == 0 && strings.Contains(all, "READY:"+marker) {
				stage = 1
				_ = pty.Write("\x03")
			}
			if stage == 1 && strings.Contains(all, "INT:"+marker) {
				stage = 2
				_ = pty.Write("printf 'AFTER:%s\\n' '" + marker + "'; exit\r")
			}
		})
		mu.Lock()
		pty = p
		mu.Unlock()
		// Expected markers are assembled by the child, so terminal echo cannot satisfy them.
		quoted := "'" + strings.ReplaceAll(childScript, "'", `'\''`) + "'"
		if err := p.Write("MARKER='" + marker + "' sh -c " + quoted + "\r"); err != nil {
			t.Fatal(err)
		}
		code := run.wait(t)
		mu.Lock()
		defer mu.Unlock()
		if code != 0 {
			t.Fatalf("interactive shell died (exit %d): %q", code, run.Output())
		}
		if stage != 2 || !strings.Contains(run.Output(), "AFTER:"+marker) {
			t.Fatalf("Ctrl-C flow stopped at stage %d: %q", stage, run.Output())
		}
	})
}
