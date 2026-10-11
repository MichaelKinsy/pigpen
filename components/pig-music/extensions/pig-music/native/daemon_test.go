package native

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// shortDir makes a directory with a path short enough for a unix socket.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "pmn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

type daemonRig struct {
	paths  music.Paths
	o      *fakeOpener
	d      *fakeOut
	done   chan error
	cancel context.CancelFunc
	logs   *logBuf
}

type logBuf struct {
	mu    sync.Mutex
	lines []string
}

func (l *logBuf) add(s string) { l.mu.Lock(); l.lines = append(l.lines, s); l.mu.Unlock() }
func (l *logBuf) all() string  { l.mu.Lock(); defer l.mu.Unlock(); return strings.Join(l.lines, "\n") }

func startDaemon(t *testing.T, idle time.Duration) *daemonRig {
	t.Helper()
	r := &daemonRig{paths: music.PathsIn(shortDir(t)), o: newOpener(map[string]int64{}), d: &fakeOut{}, done: make(chan error, 1), logs: &logBuf{}}
	for i := 1; i <= 5; i++ {
		r.o.frames["t"+string(rune('0'+i))] = OutputRate * 60
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	go func() {
		r.done <- RunDaemon(ctx, DaemonConfig{Paths: r.paths, Opener: r.o, Output: r.d, IdleExit: idle, Tick: 10 * time.Millisecond, CommandTimeout: time.Second, Log: r.logs.add})
	}()
	deadline := time.Now().Add(3 * time.Second)
	for !Running(context.Background(), r.paths) {
		if time.Now().After(deadline) {
			t.Fatal("daemon did not come up")
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Cleanup(func() {
		cancel()
		select {
		case <-r.done:
		case <-time.After(3 * time.Second):
		}
	})
	return r
}

func (r *daemonRig) player() *Player {
	return NewPlayer(ClientConfig{Paths: r.paths, CommandTimeout: 2 * time.Second})
}

func TestDaemonServesAClient(t *testing.T) {
	r := startDaemon(t, 0)
	p := r.player()
	if err := p.Attach(bg); err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if st := p.State(); !st.Connected || st.Index != -1 {
		t.Fatalf("fresh state %+v", st)
	}
	ch := p.Subscribe()
	<-ch
	if err := p.Replace(bg, tracks(3), 1); err != nil {
		t.Fatal(err)
	}
	eventually(t, "track 2 playing", func() bool { st := p.State(); return st.Index == 1 && st.Duration == 60*time.Second })
	if st := p.State(); st.Track == nil || st.Track.Title != "Track 2" || len(st.Queue) != 3 {
		t.Fatalf("%+v", st)
	}
	// Events reach the subscriber without asking.
	got := false
	deadline := time.After(2 * time.Second)
	for !got {
		select {
		case st := <-ch:
			got = st.Index == 1 && st.Duration > 0
		case <-deadline:
			t.Fatal("no pushed state")
		}
	}
	if err := p.SetPaused(bg, true); err != nil {
		t.Fatal(err)
	}
	if !p.State().Paused {
		t.Fatal("not paused in the reply")
	}
	if err := p.SetVolume(bg, 40); err != nil || p.State().Volume != 40 {
		t.Fatalf("volume: %v %+v", err, p.State())
	}
	if err := p.Move(bg, 0, 2); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := p.Next(bg); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Next(bg); !errors.Is(err, ErrEndOfQueue) {
		t.Fatalf("Next at the end: %v (the sentinel must survive the wire)", err)
	}
	if err := p.Jump(bg, 7); err == nil {
		t.Fatal("bad jump accepted")
	}
	st, err := p.Sync(bg)
	if err != nil || st.Index != 2 {
		t.Fatalf("Sync: %+v %v", st, err)
	}
}

func TestDaemonErrorSentinelsCrossTheWire(t *testing.T) {
	r := startDaemon(t, 0)
	p := r.player()
	if err := p.Attach(bg); err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.Next(bg); !errors.Is(err, ErrNothingPlaying) {
		t.Fatalf("%v", err)
	}
	if err := p.SeekRelative(bg, time.Second); !errors.Is(err, ErrNothingPlaying) {
		t.Fatalf("%v", err)
	}
	if err := p.Replace(bg, nil, 0); err == nil {
		t.Fatal("empty replace")
	}
	r.o.gate["t1"] = make(chan struct{})
	_ = p.Replace(bg, tracks(1), 0)
	if err := p.SeekRelative(bg, time.Second); !errors.Is(err, ErrNotSeekable) {
		t.Fatalf("%v", err)
	}
}

func TestSecondClientSeesTheSameQueue(t *testing.T) {
	r := startDaemon(t, 0)
	a := r.player()
	if err := a.Attach(bg); err != nil {
		t.Fatal(err)
	}
	_ = a.Replace(bg, tracks(3), 2)
	eventually(t, "playing", func() bool { return a.State().Duration > 0 })
	_ = a.Close() // the extension reloads
	b := r.player()
	if err := b.Attach(bg); err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	st := b.State()
	if st.Index != 2 || len(st.Queue) != 3 || st.Track == nil || st.Track.Title != "Track 3" || st.Duration != 60*time.Second {
		t.Fatalf("reattached state %+v", st)
	}
	if !r.d.isRunning() {
		t.Fatal("playback stopped when the first client left")
	}
}

func TestSubscriptionClosesWhenTheDaemonGoes(t *testing.T) {
	r := startDaemon(t, 0)
	p := r.player()
	_ = p.Attach(bg)
	ch := p.Subscribe()
	if err := p.Shutdown(bg); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				if p.State().Connected {
					t.Fatal("still Connected")
				}
				if err := p.Next(bg); !errors.Is(err, ErrNotAttached) {
					t.Fatalf("call after loss: %v", err)
				}
				return
			}
		case <-time.After(3 * time.Second):
			t.Fatal("subscription never closed")
		}
	}
}

func TestShutdownCleansUp(t *testing.T) {
	r := startDaemon(t, 0)
	p := r.player()
	_ = p.Attach(bg)
	_ = p.Replace(bg, tracks(2), 0)
	eventually(t, "playing", func() bool { return r.d.isRunning() })
	if err := p.Shutdown(bg); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-r.done:
		if err != nil {
			t.Fatalf("daemon returned %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not return")
	}
	if Running(bg, r.paths) {
		t.Fatal("still answering")
	}
	if _, err := os.Stat(Endpoint(r.paths)); runtime.GOOS != "windows" && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket left behind: %v", err)
	}
	if !r.d.closed || r.d.isRunning() {
		t.Fatal("audio device not stopped and closed")
	}
	// The lock is free again: a new daemon can start in the same directory.
	unlock, ok, err := tryLockFile(LockPath(r.paths))
	if err != nil || !ok {
		t.Fatalf("lock still held: %v %v", ok, err)
	}
	unlock()
}

func TestSecondDaemonRefusesAndStaleSocketIsReplaced(t *testing.T) {
	r := startDaemon(t, 0)
	err := RunDaemon(bg, DaemonConfig{Paths: r.paths, Opener: newOpener(nil), Output: &fakeOut{}})
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second daemon: %v", err)
	}
	if !Running(bg, r.paths) {
		t.Fatal("the first daemon was disturbed")
	}
	r.cancel()
	<-r.done

	// A socket file left by a daemon that died: nothing listens, no lock is held.
	if runtime.GOOS == "windows" {
		return
	}
	ln, err := net.Listen("unix", Endpoint(r.paths))
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = ln.Close()
	if _, err := os.Stat(Endpoint(r.paths)); err != nil {
		t.Fatal("no stale socket to test with")
	}
	r2 := &daemonRig{paths: r.paths, done: make(chan error, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		r2.done <- RunDaemon(ctx, DaemonConfig{Paths: r.paths, Opener: newOpener(nil), Output: &fakeOut{}})
	}()
	deadline := time.Now().Add(3 * time.Second)
	for !Running(bg, r.paths) {
		if time.Now().After(deadline) {
			t.Fatalf("stale socket was not replaced (daemon: %v)", len(r2.done))
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-r2.done
}

func TestIdleDaemonExitsWhenNobodyIsThere(t *testing.T) {
	r := startDaemon(t, 300*time.Millisecond)
	p := r.player()
	_ = p.Attach(bg)
	time.Sleep(500 * time.Millisecond)
	select {
	case <-r.done:
		t.Fatal("exited with a client connected")
	default:
	}
	_ = p.Close()
	select {
	case <-r.done:
	case <-time.After(3 * time.Second):
		t.Fatal("did not exit when idle with no client")
	}
}

func TestPlayingDaemonDoesNotIdleExit(t *testing.T) {
	r := startDaemon(t, 200*time.Millisecond)
	p := r.player()
	_ = p.Attach(bg)
	_ = p.Replace(bg, tracks(1), 0)
	eventually(t, "playing", func() bool { return r.d.isRunning() })
	_ = p.Close()
	time.Sleep(700 * time.Millisecond)
	select {
	case <-r.done:
		t.Fatal("a daemon with music queued exited")
	default:
	}
}

func rawConn(t *testing.T, r *daemonRig) (net.Conn, *bufio.Reader) {
	t.Helper()
	c, err := dial(bg, Endpoint(r.paths))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, bufio.NewReader(c)
}

func TestDaemonSurvivesMalformedTraffic(t *testing.T) {
	r := startDaemon(t, 0)
	c, br := rawConn(t, r)
	_, _ = c.Write([]byte("this is not json\n"))
	// The connection is dropped, the daemon is not.
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := br.ReadString('\n'); err == nil {
		t.Fatal("expected the connection to close")
	}
	c2, br2 := rawConn(t, r)
	_, _ = c2.Write([]byte(`{"id":1,"cmd":"nonsense"}` + "\n"))
	line, err := br2.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var m message
	_ = json.Unmarshal([]byte(line), &m)
	if m.OK || !strings.Contains(m.Error, "unknown command") || m.ID != 1 {
		t.Fatalf("reply %s", line)
	}
	// An oversized line is refused too.
	c3, _ := rawConn(t, r)
	big := strings.Repeat("a", maxLine+10)
	go func() { _, _ = c3.Write([]byte(big + "\n")) }()
	time.Sleep(200 * time.Millisecond)
	if !Running(bg, r.paths) {
		t.Fatal("daemon died on a huge line")
	}
}

func TestDaemonLimitsClients(t *testing.T) {
	r := startDaemon(t, 0)
	var conns []net.Conn
	for i := 0; i < maxClients+3; i++ {
		c, err := dial(bg, Endpoint(r.paths))
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
	}
	defer func() {
		for _, c := range conns {
			_ = c.Close()
		}
	}()
	time.Sleep(100 * time.Millisecond)
	// The surplus connections were closed by the daemon: a read returns EOF at once.
	closed := 0
	for _, c := range conns {
		_ = c.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
		if _, err := c.Read(make([]byte, 1)); err != nil && !os.IsTimeout(err) {
			closed++
		}
	}
	if closed != 3 {
		t.Fatalf("%d connections were refused, want 3", closed)
	}
}

func TestDaemonLogHasNoURLs(t *testing.T) {
	r := startDaemon(t, 0)
	p := r.player()
	_ = p.Attach(bg)
	r.o.fail["t1"] = errors.New(`Get "https://rr1.googlevideo.com/videoplayback?sig=SECRET": dial tcp 203.0.113.9:443: refused`)
	_ = p.Replace(bg, tracks(2), 0)
	eventually(t, "the failure to be reported", func() bool { return p.LastError() != "" })
	if msg := p.LastError(); strings.Contains(msg, "SECRET") || strings.Contains(msg, "203.0.113.9") || strings.Contains(msg, "googlevideo") {
		t.Fatalf("LastError leaks: %s", msg)
	}
	_ = p.Shutdown(bg)
	log := r.logs.all()
	if log == "" {
		t.Fatal("no log lines at all")
	}
	if strings.Contains(log, "SECRET") || strings.Contains(log, "203.0.113.9") || strings.Contains(log, "googlevideo") {
		t.Fatalf("log leaks: %s", log)
	}
	_ = filepath.Join
}

func TestCommandsAreBoundedWhenTheDaemonStopsAnswering(t *testing.T) {
	paths := music.PathsIn(shortDir(t))
	_ = paths.EnsureDir()
	ln, err := listen(Endpoint(paths))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() { // a daemon that says hello and subscribes, then goes silent
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
						_ = cod.write(&message{ID: m.ID, OK: true, PID: 4242, Version: ProtocolVersion})
					}
				}
			}(c)
		}
	}()
	p := NewPlayer(ClientConfig{Paths: paths, CommandTimeout: 200 * time.Millisecond})
	if err := p.Attach(bg); err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	start := time.Now()
	err = p.Next(bg)
	if err == nil || !strings.Contains(err.Error(), "did not answer") || time.Since(start) > 2*time.Second {
		t.Fatalf("err = %v after %v", err, time.Since(start))
	}
	if strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("leaked: %v", err)
	}
}

func TestHelloVersionMismatchIsRefused(t *testing.T) {
	paths := music.PathsIn(shortDir(t))
	_ = paths.EnsureDir()
	ln, err := listen(Endpoint(paths))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		cod := newCodec(c)
		m, _ := cod.read()
		_ = cod.write(&message{ID: m.ID, OK: true, PID: 1, Version: ProtocolVersion + 7})
	}()
	p := NewPlayer(ClientConfig{Paths: paths})
	err = p.Attach(bg)
	if err == nil || !strings.Contains(err.Error(), "protocol") {
		t.Fatalf("err = %v", err)
	}
	// Review of M9b (F8): a pigmusic program of another version starts the same kind of daemon again, so stopping the running one
	// is not the whole advice.
	if !strings.Contains(err.Error(), "pigmusic stop") || !strings.Contains(err.Error(), "build") {
		t.Errorf("the message does not say what to do when stopping the player does not help: %v", err)
	}
}

// gainGate is a device whose volume control hangs once armed, as a wedged audio driver would.
type gainGate struct {
	*fakeOut
	armed   chan struct{} // closed: SetGain blocks
	release chan struct{}
}

func (g *gainGate) SetGain(x float64) {
	select {
	case <-g.armed:
		<-g.release
	default:
	}
	g.fakeOut.SetGain(x)
}

// The daemon answers a request it cannot finish within its command timeout
// with an error, instead of leaving the client to give up on its own.
func TestDaemonBoundsARequestThatHangs(t *testing.T) {
	paths := music.PathsIn(shortDir(t))
	out := &gainGate{fakeOut: &fakeOut{}, armed: make(chan struct{}), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- RunDaemon(ctx, DaemonConfig{Paths: paths, Opener: newOpener(nil), Output: out, CommandTimeout: 100 * time.Millisecond})
	}()
	t.Cleanup(func() {
		close(out.release)
		cancel()
		<-done
	})
	eventually(t, "the daemon to listen", func() bool { return Running(bg, paths) })
	p := NewPlayer(ClientConfig{Paths: paths, CommandTimeout: 5 * time.Second})
	if err := p.Attach(bg); err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	close(out.armed)
	start := time.Now()
	err := p.SetVolume(bg, 40)
	if err == nil || !strings.Contains(err.Error(), "volume did not finish within 100ms") {
		t.Fatalf("a hung volume change: %v, want the daemon's own time-out", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("answered after %v; the daemon's bound is 100ms", d)
	}
}

// Attach called from several goroutines at once makes one connection: none is
// left open behind the Player, where it would hold a client slot and keep the
// daemon from ever going idle.
func TestConcurrentAttachLeavesNoStrayConnection(t *testing.T) {
	r := startDaemon(t, 300*time.Millisecond)
	// The daemon's idle clock starts when it comes up, so on a loaded host the 8 attaches could begin after the 300 ms
	// idle exit and find no daemon. A held connection keeps it alive while they run and is dropped before the check:
	// afterwards only a connection an Attach left behind could keep the daemon from exiting.
	holder, err := dial(bg, Endpoint(r.paths))
	if err != nil {
		t.Fatal(err)
	}
	p := r.player()
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := p.Attach(bg); err != nil {
				t.Error(err)
			}
		}()
	}
	close(start)
	wg.Wait()
	_ = holder.Close()
	_ = p.Close()
	select {
	case err := <-r.done:
		r.done <- err // for the rig's cleanup
	case <-time.After(3 * time.Second):
		t.Fatal("the daemon still has a client after the only Player closed")
	}
}

// Stopping while clients are still connecting joins every goroutine the
// daemon started, the accept loop and the connection that arrived as it
// stopped included. Run it with -race: the bug it guards was a WaitGroup.Add
// in the accept loop racing the final Wait.
func TestStopWhileClientsConnect(t *testing.T) {
	for round := 0; round < 20; round++ {
		paths := music.PathsIn(shortDir(t))
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- RunDaemon(ctx, DaemonConfig{Paths: paths, Opener: newOpener(nil), Output: &fakeOut{}})
		}()
		eventually(t, "the daemon to listen", func() bool { return Running(bg, paths) })
		stop := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
					}
					if c, err := dial(bg, Endpoint(paths)); err == nil {
						_ = c.Close()
					}
				}
			}()
		}
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("round %d: %v", round, err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("round %d: the daemon did not stop while clients were connecting", round)
		}
		close(stop)
		wg.Wait()
	}
}
