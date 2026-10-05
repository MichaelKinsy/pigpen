//go:build !windows

package native

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

func spawnPlayer(t *testing.T, mode string, extra ...string) (*Player, music.Paths) {
	t.Helper()
	paths := music.PathsIn(shortDir(t))
	p := NewPlayer(ClientConfig{
		Paths: paths, Binary: os.Args[0], Env: []string{testModeEnv + "=" + mode}, Args: extra,
		StartTimeout: 10 * time.Second, CommandTimeout: 3 * time.Second,
	})
	t.Cleanup(func() {
		_ = p.Close()
		for _, pid := range daemonPIDs(paths) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	return p, paths
}

// daemonPIDs lists the fake daemons started so far in paths' directory.
func daemonPIDs(p music.Paths) []int {
	files, _ := filepath.Glob(filepath.Join(p.Dir, "pid.*"))
	var out []int
	for _, f := range files {
		if n, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(f), "pid.")); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func sessionID(pid int) int {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return -1
	}
	fields := strings.Fields(string(data[strings.LastIndex(string(data), ")")+1:]))
	if len(fields) < 4 {
		return -1
	}
	n, _ := strconv.Atoi(fields[3])
	return n
}

func TestAttachStartsADetachedDaemonThatOutlivesTheClient(t *testing.T) {
	p, paths := spawnPlayer(t, "serve")
	if err := p.Attach(bg); err != nil {
		t.Fatal(err)
	}
	pids := daemonPIDs(paths)
	if len(pids) != 1 {
		t.Fatalf("daemons started: %v", pids)
	}
	pid := pids[0]
	if args := fmt.Sprint(pids); args == "" {
		t.Fatal()
	}
	if sid := sessionID(pid); sid != pid && sid != -1 {
		t.Fatalf("daemon is in session %d, want its own (%d)", sid, pid)
	}
	if err := p.Replace(bg, tracks(3), 1); err != nil {
		t.Fatal(err)
	}
	eventually(t, "playing", func() bool { st := p.State(); return st.Duration > 0 })
	time.Sleep(400 * time.Millisecond)
	first := p.State().Position
	if first <= 0 {
		t.Fatalf("position did not move in real time: %v", first)
	}
	_ = p.Close() // "kill the extension"
	if !pidAlive(pid) {
		t.Fatal("the daemon died with its client")
	}
	// A new client (the reloaded extension) finds the same daemon, queue and track.
	q := NewPlayer(ClientConfig{Paths: paths, Binary: os.Args[0], Env: []string{testModeEnv + "=serve"}, CommandTimeout: 3 * time.Second})
	defer q.Close()
	if err := q.Attach(bg); err != nil {
		t.Fatal(err)
	}
	if got := daemonPIDs(paths); len(got) != 1 {
		t.Fatalf("reattaching started another daemon: %v", got)
	}
	st := q.State()
	if st.Index != 1 || len(st.Queue) != 3 || st.Track.Title != "Track 2" || st.Position < first {
		t.Fatalf("reattached state %+v (position before %v)", st, first)
	}
	// The log exists, is private, and says nothing sensitive.
	fi, err := os.Stat(LogPath(paths))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("log: %v %v", fi, err)
	}
	// Shutdown ends the process and removes everything.
	if err := q.Shutdown(bg); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the daemon to be gone", func() bool { return !pidAlive(pid) })
	if _, err := os.Stat(Endpoint(paths)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket left behind: %v", err)
	}
	if Running(bg, paths) {
		t.Fatal("still answering")
	}
}

func TestParallelStartersStartOneDaemon(t *testing.T) {
	_, paths := spawnPlayer(t, "serve")
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q := NewPlayer(ClientConfig{Paths: paths, Binary: os.Args[0], Env: []string{testModeEnv + "=serve"}, CommandTimeout: 3 * time.Second})
			defer q.Close()
			errs <- q.Attach(bg)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := daemonPIDs(paths); len(got) != 1 {
		t.Fatalf("%d daemons started: %v", len(got), got)
	}
}

func TestStartFailureIsReportedWithoutLeaks(t *testing.T) {
	p, paths := spawnPlayer(t, "exit")
	err := p.Attach(bg)
	if err == nil {
		t.Fatal("expected failure")
	}
	msg := err.Error()
	if !strings.Contains(msg, "exited before") || !strings.Contains(msg, "native.log") {
		t.Fatalf("message %q should say the player exited and where its log is", msg)
	}
	for _, bad := range []string{"SECRET", "googlevideo", "203.0.113.9"} {
		if strings.Contains(msg, bad) {
			t.Fatalf("message leaks %q: %s", bad, msg)
		}
	}
	_ = paths
}

func TestStartTimeoutKillsTheHungDaemon(t *testing.T) {
	paths := music.PathsIn(shortDir(t))
	p := NewPlayer(ClientConfig{Paths: paths, Binary: os.Args[0], Env: []string{testModeEnv + "=hang"}, StartTimeout: 400 * time.Millisecond})
	start := time.Now()
	err := p.Attach(bg)
	if err == nil || !strings.Contains(err.Error(), "in time") || time.Since(start) > 5*time.Second {
		t.Fatalf("err = %v after %v", err, time.Since(start))
	}
	pids := daemonPIDs(paths)
	if len(pids) != 1 {
		t.Fatalf("pids %v", pids)
	}
	eventually(t, "the hung daemon to be stopped", func() bool { return !pidAlive(pids[0]) })
}

func TestStaleSocketFromADeadDaemonIsReplaced(t *testing.T) {
	p, paths := spawnPlayer(t, "serve")
	if err := paths.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", Endpoint(paths))
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = ln.Close()
	if err := p.Attach(bg); err != nil {
		t.Fatalf("a stale socket must not block a start: %v", err)
	}
	if err := p.Shutdown(bg); err != nil {
		t.Fatal(err)
	}
}

func TestSIGTERMStopsTheDaemonCleanly(t *testing.T) {
	p, paths := spawnPlayer(t, "serve")
	if err := p.Attach(bg); err != nil {
		t.Fatal(err)
	}
	_ = p.Replace(bg, tracks(1), 0)
	pid := daemonPIDs(paths)[0]
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the daemon to exit", func() bool { return !pidAlive(pid) })
	if _, err := os.Stat(Endpoint(paths)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket left behind after SIGTERM: %v", err)
	}
	if _, ok, _ := tryLockFile(LockPath(paths)); !ok {
		t.Fatal("lock left held")
	}
}

func TestSecondServeForTheSameDirectoryExitsQuietly(t *testing.T) {
	p, paths := spawnPlayer(t, "serve")
	if err := p.Attach(bg); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "serve", "--dir", paths.Dir)
	cmd.Env = append(os.Environ(), testModeEnv+"=serve")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("a second serve must exit 0: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "already running") {
		t.Fatalf("output %q", out)
	}
	if !Running(bg, paths) {
		t.Fatal("the first daemon was disturbed")
	}
}

func TestShutdownForcesAHungDaemon(t *testing.T) {
	// A daemon process that ignores everything: stopped by pid after the timeout.
	paths := music.PathsIn(shortDir(t))
	_ = paths.EnsureDir()
	cmd := exec.Command("sleep", "60")
	detach(cmd)
	if err := cmd.Start(); err != nil {
		t.Skip("no sleep")
	}
	go func() { _ = cmd.Wait() }()
	ln, err := listen(Endpoint(paths))
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
			go func(c net.Conn) {
				cod := newCodec(c)
				for {
					m, err := cod.read()
					if err != nil {
						return
					}
					if m.Cmd == cmdHello || m.Cmd == cmdSubscribe {
						_ = cod.write(&message{ID: m.ID, OK: true, PID: cmd.Process.Pid, Version: ProtocolVersion})
					}
				}
			}(c)
		}
	}()
	p := NewPlayer(ClientConfig{Paths: paths, CommandTimeout: 300 * time.Millisecond})
	if err := p.Attach(bg); err != nil {
		t.Fatal(err)
	}
	err = p.Shutdown(context.Background())
	if err == nil || !strings.Contains(err.Error(), "ended") {
		t.Fatalf("err = %v", err)
	}
	eventually(t, "the process to be killed", func() bool { return !pidAlive(cmd.Process.Pid) })
}

func TestFindServeBinary(t *testing.T) {
	_, err := FindServeBinary("", func(string) string { return "" }, func(string) (string, error) { return "", errors.New("not found") })
	if err == nil || !strings.Contains(err.Error(), "pigmusic") {
		t.Fatalf("err = %v", err)
	}
	got, err := FindServeBinary("", func(k string) string {
		if k == "PIG_MUSIC_SERVE" {
			return "/opt/x/pigmusic"
		}
		return ""
	}, func(s string) (string, error) { return s, nil })
	if err != nil || got != "/opt/x/pigmusic" {
		t.Fatalf("env: %q %v", got, err)
	}
	if _, err := FindServeBinary("/nope", nil, func(string) (string, error) { return "", errors.New("missing") }); err == nil || !strings.Contains(err.Error(), "/nope") {
		t.Fatalf("configured but missing: %v", err)
	}
}

// Only the user can reach the daemon: the runtime directory is 0700 (an
// existing looser one is tightened), the socket 0600 whatever the umask, and
// the lock file 0600.
func TestDaemonFilesAreUserOnly(t *testing.T) {
	old := syscall.Umask(0)
	t.Cleanup(func() { syscall.Umask(old) })
	dir := filepath.Join(shortDir(t), "rt")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	paths := music.PathsIn(dir)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunDaemon(ctx, DaemonConfig{Paths: paths, Opener: newOpener(nil), Output: &fakeOut{}}) }()
	t.Cleanup(func() { cancel(); <-done })
	eventually(t, "the daemon to listen", func() bool { return Running(bg, paths) })
	for path, want := range map[string]os.FileMode{dir: 0o700, Endpoint(paths): 0o600, LockPath(paths): 0o600} {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != want {
			t.Errorf("%s: mode %o, want %o", filepath.Base(path), got, want)
		}
	}
}
