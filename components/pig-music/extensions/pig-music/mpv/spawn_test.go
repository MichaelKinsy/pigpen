//go:build !windows

package mpv

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// spawner is a player that starts this test binary as "mpv".
type spawner struct {
	paths  music.Paths
	record string
}

func newSpawner(t *testing.T) *spawner {
	t.Helper()
	dir := shortDir(t)
	s := &spawner{paths: music.PathsIn(dir), record: filepath.Join(dir, "rec")}
	t.Cleanup(func() { s.killAll() })
	return s
}

func (s *spawner) player(extra ...string) *Player {
	return New(Config{
		MPVPath: os.Args[0], Paths: s.paths, PlayURL: playURL, ExtraArgs: extra,
		Env: []string{fakeEnv + "=1", "MPVFAKE_RECORD=" + s.record},
	})
}

// processes returns the pids of the fake mpv processes started so far, and their arguments.
func (s *spawner) processes() map[int][]string {
	files, _ := filepath.Glob(s.record + ".*")
	out := map[int][]string{}
	for _, f := range files {
		pid, err := strconv.Atoi(strings.TrimPrefix(f, s.record+"."))
		if err != nil {
			continue
		}
		var args []string
		data, _ := os.ReadFile(f)
		_ = json.Unmarshal(data, &args)
		out[pid] = args
	}
	return out
}

func (s *spawner) killAll() {
	for pid := range s.processes() {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

func alive(pid int) bool {
	return syscall.Kill(pid, 0) == nil && !zombie(pid)
}

// sessionOf reads the session id from /proc/<pid>/stat (field 6).
func sessionOf(pid int) (int, bool) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(data[strings.LastIndex(string(data), ")")+1:]))
	if len(fields) < 4 {
		return 0, false
	}
	sid, err := strconv.Atoi(fields[3])
	return sid, err == nil
}

func zombie(pid int) bool {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	fields := strings.Fields(string(data[strings.LastIndex(string(data), ")")+1:]))
	return len(fields) > 0 && fields[0] == "Z"
}

func TestAttachStartsMpvInItsOwnSessionAndItOutlivesThePlayer(t *testing.T) {
	s := newSpawner(t)
	p := s.player("--ao=null")
	if err := p.Attach(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	procs := s.processes()
	if len(procs) != 1 {
		t.Fatalf("%d mpv processes started: %v", len(procs), procs)
	}
	var pid int
	var args []string
	for pid, args = range procs {
	}
	for _, want := range []string{"--idle=yes", "--no-video", "--input-terminal=no", "--msg-level=all=warn", "--ytdl-format=bestaudio", "--input-ipc-server=" + s.paths.Socket, "--ao=null"} {
		if !contains(args, want) {
			t.Errorf("mpv was not started with %s: %v", want, args)
		}
	}
	// /proc exists on Linux only; elsewhere the session is not checked.
	if sid, ok := sessionOf(pid); ok {
		if sid != pid {
			t.Errorf("mpv is not a session leader: session %d, pid %d", sid, pid)
		}
		if mine, _ := sessionOf(os.Getpid()); mine == pid {
			t.Error("mpv shares this process's session")
		}
	}
	info, err := os.Stat(s.paths.Dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("directory %v %v", info, err)
	}

	if err := p.Replace(ctx5(t), tracks(3), 2); err != nil {
		t.Fatal(err)
	}
	waitState(t, p, "queue", func(st music.State) bool { return len(st.Queue) == 3 && st.Index == 2 })
	_ = p.Close() // "kill the CLI"
	time.Sleep(100 * time.Millisecond)
	if !alive(pid) || !Running(ctx5(t), s.paths.Socket) {
		t.Fatal("mpv stopped when the player closed")
	}

	again := s.player("--ao=null")
	if err := again.Attach(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	st := again.State()
	if len(s.processes()) != 1 || st.Index != 2 || len(st.Queue) != 3 || st.Track == nil || st.Track.Title != "Title 2" {
		t.Fatalf("reattach: %d processes, state %+v", len(s.processes()), st)
	}

	if err := again.Shutdown(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "mpv to exit", func() bool { return !alive(pid) })
	for _, f := range []string{s.paths.Socket, s.paths.Meta} {
		if _, err := os.Stat(f); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s survived Shutdown (%v)", f, err)
		}
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestAStaleSocketIsRemovedAndReplaced(t *testing.T) {
	s := newSpawner(t)
	if err := s.paths.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", s.paths.Socket)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = ln.Close() // the file stays, nothing listens
	if _, err := os.Stat(s.paths.Socket); err != nil {
		t.Fatal("no stale socket file to test with")
	}
	p := s.player()
	if err := p.Attach(ctx5(t)); err != nil {
		t.Fatalf("Attach over a stale socket: %v", err)
	}
	defer p.Close()
	if len(s.processes()) != 1 || !p.State().Connected {
		t.Fatalf("processes %v state %+v", s.processes(), p.State())
	}
}

func TestMissingMpvNamesWhatToInstall(t *testing.T) {
	p := New(Config{Paths: music.PathsIn(shortDir(t)), MPVPath: "pig-music-no-such-mpv", LookPath: func(string) (string, error) { return "", errors.New("not found") }})
	err := p.Attach(ctx5(t))
	var missing *music.MissingError
	if !errors.As(err, &missing) || len(missing.Names) != 1 || missing.Names[0] != "mpv" || !strings.Contains(err.Error(), "mpv") {
		t.Fatalf("err = %v", err)
	}
}

func TestMpvThatExitsAtOnceIsReportedWithItsLog(t *testing.T) {
	dir := shortDir(t)
	p := New(Config{MPVPath: "/bin/false", Paths: music.PathsIn(dir), PlayURL: playURL})
	err := p.Attach(ctx5(t))
	if err == nil || !strings.Contains(err.Error(), "exited before it opened its socket") || !strings.Contains(err.Error(), "mpv.log") {
		t.Fatalf("err = %v", err)
	}
}

func TestMpvThatNeverOpensItsSocketTimesOut(t *testing.T) {
	dir := shortDir(t)
	pidFile := filepath.Join(dir, "pid")
	script := filepath.Join(dir, "mpv")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho $$ > "+pidFile+"\nexec sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if data, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	p := New(Config{MPVPath: script, Paths: music.PathsIn(dir), StartTimeout: 400 * time.Millisecond})
	start := time.Now()
	err := p.Attach(ctx5(t))
	if err == nil || !strings.Contains(err.Error(), "did not open") {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("took %s", time.Since(start))
	}
}

func TestTwoPlayersStartingTogetherStartOneMpv(t *testing.T) {
	s := newSpawner(t)
	var wg sync.WaitGroup
	players := make([]*Player, 4)
	errs := make([]error, len(players))
	for i := range players {
		players[i] = s.player()
		wg.Add(1)
		go func() { defer wg.Done(); errs[i] = players[i].Attach(ctx5(t)) }()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("player %d: %v", i, err)
		}
		defer players[i].Close()
	}
	if got := len(s.processes()); got != 1 {
		t.Fatalf("%d mpv processes for four players: %v", got, s.processes())
	}
	if err := players[0].Replace(ctx5(t), tracks(2), 0); err != nil {
		t.Fatal(err)
	}
	for i, p := range players {
		waitState(t, p, fmt.Sprintf("player %d sees the queue", i), func(st music.State) bool { return len(st.Queue) == 2 })
	}
}
