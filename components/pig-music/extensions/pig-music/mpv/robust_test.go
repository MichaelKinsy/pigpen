//go:build !windows

package mpv

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// hangingMpv writes a stand-in "mpv" that records its pid and never opens its
// socket. It returns the script and a function reading the pid once it has started.
func hangingMpv(t *testing.T, dir string) (script string, pid func() int) {
	t.Helper()
	pidFile := filepath.Join(dir, "pid")
	script = filepath.Join(dir, "mpv")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho $$ > "+pidFile+"\nexec sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	read := func() int {
		data, err := os.ReadFile(pidFile)
		if err != nil {
			return 0
		}
		n, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		return n
	}
	t.Cleanup(func() {
		if p := read(); p > 0 {
			_ = syscall.Kill(p, syscall.SIGKILL)
		}
	})
	return script, read
}

func TestAnMpvThatNeverOpensItsSocketIsNotLeftRunning(t *testing.T) {
	dir := shortDir(t)
	script, pid := hangingMpv(t, dir)
	p := New(Config{MPVPath: script, Paths: music.PathsIn(dir), StartTimeout: 400 * time.Millisecond})
	if err := p.Attach(ctx5(t)); err == nil || !strings.Contains(err.Error(), "did not open") {
		t.Fatalf("err = %v", err)
	}
	if pid() == 0 {
		t.Fatal("the stand-in mpv never started")
	}
	waitUntil(t, "the mpv that never opened its socket to be stopped", func() bool { return !alive(pid()) })
}

func TestCancellingAttachWhileMpvStartsStopsIt(t *testing.T) {
	dir := shortDir(t)
	script, pid := hangingMpv(t, dir)
	p := New(Config{MPVPath: script, Paths: music.PathsIn(dir), StartTimeout: 5 * time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		waitUntil(t, "mpv to start", func() bool { return pid() > 0 })
		cancel()
	}()
	if err := p.Attach(ctx); err == nil {
		t.Fatal("Attach succeeded with no mpv socket")
	}
	waitUntil(t, "the mpv of a cancelled Attach to be stopped", func() bool { return pid() > 0 && !alive(pid()) })
}

// attachWithin runs Attach and fails the test if it has not returned by limit.
func attachWithin(t *testing.T, p *Player, limit time.Duration) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- p.Attach(context.Background()) }()
	select {
	case err := <-done:
		return err
	case <-time.After(limit):
		t.Fatalf("Attach was still waiting on an mpv that does not answer after %s", limit)
		return nil
	}
}

func TestAnMpvThatDoesNotAnswerIsReportedNotWaitedOn(t *testing.T) {
	paths := music.PathsIn(shortDir(t))
	if err := paths.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", paths.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close() // read nothing, answer nothing: a stopped mpv
		}
	}()
	p := New(Config{Paths: paths, PlayURL: playURL, CommandTimeout: 200 * time.Millisecond})
	err = attachWithin(t, p, 3*time.Second)
	if err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("err = %v", err)
	}
}

func TestAnMpvThisPlayerStartedIsStoppedWhenAttachFailsAfterwards(t *testing.T) {
	s := newSpawner(t)
	p := New(Config{
		MPVPath: os.Args[0], Paths: s.paths, PlayURL: playURL, CommandTimeout: 200 * time.Millisecond,
		Env: []string{fakeEnv + "=silent", "MPVFAKE_RECORD=" + s.record},
	})
	if err := attachWithin(t, p, 5*time.Second); err == nil {
		t.Fatal("Attach succeeded against an mpv that does not answer")
	}
	procs := s.processes()
	if len(procs) != 1 {
		t.Fatalf("%d mpv processes: %v", len(procs), procs)
	}
	for pid := range procs {
		waitUntil(t, "the unusable mpv this player started to be stopped", func() bool { return !alive(pid) })
	}
}

func TestMpvLogsOnlyItsWarningsToAPrivateFile(t *testing.T) {
	p := New(Config{Paths: music.PathsIn("/run/pm")})
	for _, arg := range p.mpvArgs() {
		// --log-file is verbose whatever --msg-level says: it records the signed stream
		// URLs (with the listener's IP address) that mpv's yt-dlp hook resolves.
		if strings.HasPrefix(arg, "--log-file") || arg == "--no-terminal" {
			t.Errorf("mpv argument %s", arg)
		}
	}
	for _, want := range []string{"--msg-level=all=warn", "--input-terminal=no", "--quiet"} {
		if !contains(p.mpvArgs(), want) {
			t.Errorf("mpv is not started with %s: %v", want, p.mpvArgs())
		}
	}

	dir := shortDir(t)
	script := filepath.Join(dir, "mpv")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho 'Error parsing option bogus' >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	paths := music.PathsIn(filepath.Join(dir, "run"))
	err := New(Config{MPVPath: script, Paths: paths}).Attach(ctx5(t))
	if err == nil || !strings.Contains(err.Error(), "mpv.log") {
		t.Fatalf("err = %v", err)
	}
	log := filepath.Join(paths.Dir, "mpv.log")
	data, rerr := os.ReadFile(log)
	if rerr != nil || !strings.Contains(string(data), "Error parsing option bogus") {
		t.Fatalf("mpv's own error is not in its log: %q %v", data, rerr)
	}
	if info, err := os.Stat(log); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("log mode %v %v", info.Mode().Perm(), err)
	}
}

func TestAPositionOutsideTheQueueIsNoTrack(t *testing.T) {
	v := newView()
	v.apply("playlist", json.RawMessage(`[{"filename":"https://music.youtube.com/watch?v=aaaaaaaaaaa"}]`))
	v.apply("playlist-pos", json.RawMessage(`3`))
	st := v.state(nil)
	if st.Index != -1 || st.Track != nil || len(st.Queue) != 1 {
		t.Fatalf("playlist-pos past the queue gave %+v", st)
	}
}

func TestPrevInTheFirstSecondsPlaysThePreviousTrack(t *testing.T) {
	p, srv, _ := inProcess(t)
	if err := p.Replace(ctx5(t), tracks(3), 1); err != nil {
		t.Fatal(err)
	}
	waitState(t, p, "queue", func(s music.State) bool { return len(s.Queue) == 3 && s.Index == 1 })
	srv.Advance(2)
	waitState(t, p, "two seconds in", func(s music.State) bool { return s.Position == 2*time.Second })
	if err := p.Prev(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	waitState(t, p, "the previous track", func(s music.State) bool { return s.Index == 0 })
}

func TestCommandsHaveATimeoutByDefault(t *testing.T) {
	// The command line passes a context with no deadline; without a default, a
	// stopped mpv makes `pigmusic status` wait forever.
	if d := New(Config{}).cfg.CommandTimeout; d <= 0 || d > 30*time.Second {
		t.Fatalf("default command timeout %s", d)
	}
}
