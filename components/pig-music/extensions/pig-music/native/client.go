package native

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/mpv"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// ErrNotAttached is returned by a call made before Attach succeeded.
var ErrNotAttached = mpv.ErrNotAttached

// ClientConfig configures a Player.
type ClientConfig struct {
	Paths music.Paths
	// Binary is the `pigmusic` program that runs `serve`. Empty finds it (see FindServeBinary).
	Binary string
	// Args are added to the `serve` command line (for example --output null).
	Args []string
	// Env is added to the daemon's environment when the player starts it.
	Env []string
	// StartTimeout is how long Attach waits for a new daemon's socket; default 10 s.
	StartTimeout time.Duration
	// CommandTimeout bounds every request, so that a daemon that has stopped
	// answering is reported instead of waited on; default 5 s.
	CommandTimeout time.Duration
}

// Player is a music.Player that talks to a `pigmusic serve` daemon, starting
// one when none runs. The daemon owns the queue and the audio, so playback goes
// on when this process exits or the extension reloads.
type Player struct {
	cfg ClientConfig

	attachMu sync.Mutex // one Attach at a time: a second would replace the first one's connection mid-handshake

	mu      sync.Mutex
	conn    net.Conn
	cod     *codec
	pending map[int64]chan *message
	nextID  int64
	st      music.State
	lastErr string
	pid     int
	subs    []chan music.State
	done    chan struct{}
	wmu     sync.Mutex
}

var _ music.Player = (*Player)(nil)

// NewPlayer returns a Player that is not attached yet.
func NewPlayer(cfg ClientConfig) *Player {
	if cfg.StartTimeout <= 0 {
		cfg.StartTimeout = 10 * time.Second
	}
	if cfg.CommandTimeout <= 0 {
		cfg.CommandTimeout = 5 * time.Second
	}
	return &Player{cfg: cfg, st: music.State{Index: -1, Volume: 100}}
}

// Running reports whether a daemon answers on paths' endpoint, without attaching.
func Running(ctx context.Context, p music.Paths) bool {
	dctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	conn, err := dial(dctx, Endpoint(p))
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// LogPath is the daemon's log. It holds no URL or address.
func LogPath(p music.Paths) string { return filepath.Join(p.Dir, "native.log") }

// Attach connects to the daemon, or starts one.
func (p *Player) Attach(ctx context.Context) error {
	p.attachMu.Lock()
	defer p.attachMu.Unlock()
	p.mu.Lock()
	if p.conn != nil {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()
	if err := p.cfg.Paths.EnsureDir(); err != nil {
		return err
	}
	conn, err := p.dialExisting(ctx)
	var stopStarted func()
	if err != nil {
		if conn, stopStarted, err = p.startAndDial(ctx); err != nil {
			return err
		}
	}
	if err := p.adopt(ctx, conn); err != nil {
		// A daemon this call started and could not use would play on, unseen, in a session of its own.
		if stopStarted != nil {
			stopStarted()
		}
		return err
	}
	return nil
}

func (p *Player) dialExisting(ctx context.Context) (net.Conn, error) {
	dctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return dial(dctx, Endpoint(p.cfg.Paths))
}

// FindServeBinary finds the program that runs `serve`: the configured path, the
// PIG_MUSIC_SERVE variable, this executable when it is pigmusic, a pigmusic next
// to this executable, then PATH.
func FindServeBinary(configured string, getenv func(string) string, lookPath func(string) (string, error)) (string, error) {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if getenv == nil {
		getenv = os.Getenv
	}
	name := "pigmusic"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	for _, c := range []string{configured, getenv("PIG_MUSIC_SERVE")} {
		if c != "" {
			// A bare name is looked up on PATH (exec.LookPath refuses one found in the working directory). A relative path
			// would resolve against the working directory, which is the project PiG was started in: refused.
			if !filepath.IsAbs(c) && strings.ContainsAny(c, `/\`) {
				return "", fmt.Errorf("the native player program %q is a relative path, which would be looked up in the working directory: use an absolute path or a bare name on PATH", c)
			}
			p, err := lookPath(c)
			if err != nil {
				return "", fmt.Errorf("the native player program %q was not found: %w", c, err)
			}
			return p, nil
		}
	}
	if exe, err := os.Executable(); err == nil {
		if strings.HasPrefix(strings.ToLower(filepath.Base(exe)), "pigmusic") {
			return exe, nil
		}
		sibling := filepath.Join(filepath.Dir(exe), name)
		if st, err := os.Stat(sibling); err == nil && !st.IsDir() {
			return sibling, nil
		}
	}
	if p, err := lookPath(name); err == nil {
		return p, nil
	}
	return "", errors.New("the native engine runs its player as `pigmusic serve`, and pigmusic was not found next to this program or on PATH; install pigmusic or set PIG_MUSIC_SERVE to its path (the extension does not carry the engine; `pigmusic` does), or use mpv (engine = mpv)")
}

func (p *Player) startAndDial(ctx context.Context) (conn net.Conn, stop func(), err error) {
	bin, err := FindServeBinary(p.cfg.Binary, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	lctx, cancel := context.WithTimeout(ctx, p.cfg.StartTimeout)
	defer cancel()
	unlock, err := lockFile(lctx, filepath.Join(p.cfg.Paths.Dir, "native.start.lock"))
	if err != nil {
		return nil, nil, fmt.Errorf("waiting for another pig-music to start the native player: %w", err)
	}
	defer unlock()
	// The holder of the lock may have started the daemon while this call waited.
	if c, err := p.dialExisting(ctx); err == nil {
		return c, nil, nil
	}
	return p.start(lctx, bin)
}

func (p *Player) start(ctx context.Context, bin string) (net.Conn, func(), error) {
	logPath := LogPath(p.cfg.Paths)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("native player log: %w", err)
	}
	defer logFile.Close() // the daemon holds its own descriptor
	_ = logFile.Chmod(0o600)
	args := append([]string{"serve", "--dir", p.cfg.Paths.Dir}, p.cfg.Args...)
	cmd := exec.Command(bin, args...)
	cmd.Env = CleanEnv(append(os.Environ(), p.cfg.Env...))
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, logFile, logFile
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("starting the native player: %w", RedactError(err))
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	stop := func() {
		killTree(cmd)
		select {
		case <-exited:
		case <-time.After(2 * time.Second):
		}
		cleanEndpoint(Endpoint(p.cfg.Paths))
	}
	for {
		dctx, cancel := context.WithTimeout(ctx, time.Second)
		c, err := dial(dctx, Endpoint(p.cfg.Paths))
		cancel()
		if err == nil {
			return c, stop, nil
		}
		select {
		case werr := <-exited:
			return nil, nil, fmt.Errorf("the native player exited before it opened its socket (%v): %s (log: %s)", werr, lastLogLine(logPath), logPath)
		case <-ctx.Done():
			stop()
			return nil, nil, fmt.Errorf("the native player did not open its socket in time, and was stopped: %w", ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
}

// lastLogLine is the daemon's last log line, already free of URLs and addresses.
func lastLogLine(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "no log"
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 || lines[len(lines)-1] == "" {
		return "no output"
	}
	return Redact(lines[len(lines)-1])
}

// adopt makes conn the player's connection: reads its messages, says hello and subscribes.
func (p *Player) adopt(ctx context.Context, conn net.Conn) error {
	p.mu.Lock()
	p.conn, p.cod = conn, newCodec(conn)
	p.pending = map[int64]chan *message{}
	p.done = make(chan struct{})
	p.st = music.State{Connected: true, Index: -1, Volume: 100}
	cod, done := p.cod, p.done
	p.mu.Unlock()
	go p.readLoop(conn, cod, done)
	hello, err := p.call(ctx, &message{Cmd: cmdHello})
	if err == nil && hello.Version != ProtocolVersion {
		err = fmt.Errorf("the running native player speaks protocol %d, this pig-music expects %d: stop it (pigmusic stop) and try again; if it comes back, the pigmusic program that is found is of another version than this pig-music: build it again from this checkout (cd components/pig-music/extensions/pig-music/cmd/pigmusic && go build)", hello.Version, ProtocolVersion)
	}
	if err == nil {
		p.mu.Lock()
		p.pid = hello.PID
		p.mu.Unlock()
		_, err = p.call(ctx, &message{Cmd: cmdSubscribe})
	}
	if err != nil {
		_ = conn.Close()
		return err
	}
	return nil
}

func (p *Player) readLoop(conn net.Conn, cod *codec, done chan struct{}) {
	defer close(done)
	for {
		m, err := cod.read()
		if err != nil {
			break
		}
		if m.Event == "state" && m.State != nil {
			p.mu.Lock()
			if p.conn == conn { // a replaced connection must not overwrite the current one's state
				p.apply(m)
			}
			p.mu.Unlock()
			continue
		}
		p.mu.Lock()
		ch := p.pending[m.ID]
		delete(p.pending, m.ID)
		p.mu.Unlock()
		if ch != nil {
			ch <- m
		}
	}
	_ = conn.Close()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn != conn {
		return
	}
	p.conn = nil
	p.st.Connected = false
	for id, ch := range p.pending {
		close(ch)
		delete(p.pending, id)
	}
	p.publish()
	for _, ch := range p.subs {
		close(ch)
	}
	p.subs = nil
}

// apply records a state the daemon sent. The caller holds p.mu.
func (p *Player) apply(m *message) {
	if m.State == nil {
		return
	}
	p.st = *m.State
	p.st.Connected = true
	p.lastErr = m.LastError
	p.publish()
}

func (p *Player) publish() {
	for _, ch := range p.subs {
		select {
		case ch <- p.st:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- p.st:
			default:
			}
		}
	}
}

// call sends one request and waits for its reply, bounded by ctx and the command timeout.
func (p *Player) call(ctx context.Context, m *message) (*message, error) {
	p.mu.Lock()
	if p.conn == nil {
		p.mu.Unlock()
		return nil, ErrNotAttached
	}
	p.nextID++
	m.ID = p.nextID
	ch := make(chan *message, 1)
	p.pending[m.ID] = ch
	conn, cod, pid := p.conn, p.cod, p.pid
	p.mu.Unlock()

	forget := func() {
		p.mu.Lock()
		delete(p.pending, m.ID)
		p.mu.Unlock()
	}
	p.wmu.Lock()
	_ = conn.SetWriteDeadline(time.Now().Add(p.cfg.CommandTimeout))
	err := cod.write(m)
	p.wmu.Unlock()
	if err != nil {
		forget()
		return nil, fmt.Errorf("sending %s to the native player: %w", m.Cmd, RedactError(err))
	}
	timer := time.NewTimer(p.cfg.CommandTimeout)
	defer timer.Stop()
	select {
	case r, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("the native player closed the connection during %s", m.Cmd)
		}
		if r.State != nil {
			p.mu.Lock()
			if p.conn == conn { // not after the connection was lost: that would resurrect it
				p.apply(r)
			}
			p.mu.Unlock()
		}
		if !r.OK {
			return r, errorFor(r.Code, r.Error)
		}
		return r, nil
	case <-ctx.Done():
		forget()
		return nil, ctx.Err()
	case <-timer.C:
		forget()
		return nil, fmt.Errorf("the native player did not answer %s within %s; it may be hung (pid %d, log %s)", m.Cmd, p.cfg.CommandTimeout, pid, LogPath(p.cfg.Paths))
	}
}

func (p *Player) do(ctx context.Context, m *message) error {
	_, err := p.call(ctx, m)
	return err
}

// State returns the last state the daemon reported.
func (p *Player) State() music.State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.st
}

// LastError is why the daemon last skipped a track, empty when it has not.
func (p *Player) LastError() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastErr
}

// Sync reads the live state from the daemon.
func (p *Player) Sync(ctx context.Context) (music.State, error) {
	if _, err := p.call(ctx, &message{Cmd: cmdState}); err != nil {
		return music.State{}, err
	}
	return p.State(), nil
}

// Subscribe delivers the current state at once and every change after it; the
// channel closes when the daemon goes away.
func (p *Player) Subscribe() <-chan music.State {
	p.mu.Lock()
	defer p.mu.Unlock()
	ch := make(chan music.State, 1)
	ch <- p.st
	if p.conn == nil {
		close(ch)
		return ch
	}
	p.subs = append(p.subs, ch)
	return ch
}

func (p *Player) Replace(ctx context.Context, tracks []music.Track, startAt int) error {
	if len(tracks) == 0 {
		return errors.New("nothing to play: the track list is empty")
	}
	if startAt < 0 || startAt >= len(tracks) {
		return fmt.Errorf("start index %d is outside the %d tracks", startAt, len(tracks))
	}
	return p.do(ctx, &message{Cmd: cmdReplace, Tracks: tracks, Index: startAt})
}

func (p *Player) Enqueue(ctx context.Context, tracks ...music.Track) error {
	if len(tracks) == 0 {
		return nil
	}
	return p.do(ctx, &message{Cmd: cmdEnqueue, Tracks: tracks})
}

func (p *Player) Remove(ctx context.Context, index int) error {
	return p.do(ctx, &message{Cmd: cmdRemove, Index: index})
}

func (p *Player) Move(ctx context.Context, from, to int) error {
	return p.do(ctx, &message{Cmd: cmdMove, Index: from, To: to})
}

func (p *Player) Jump(ctx context.Context, index int) error {
	return p.do(ctx, &message{Cmd: cmdJump, Index: index})
}
func (p *Player) Next(ctx context.Context) error { return p.do(ctx, &message{Cmd: cmdNext}) }
func (p *Player) Prev(ctx context.Context) error { return p.do(ctx, &message{Cmd: cmdPrev}) }
func (p *Player) TogglePause(ctx context.Context) error {
	return p.do(ctx, &message{Cmd: cmdToggle})
}
func (p *Player) SetPaused(ctx context.Context, paused bool) error {
	return p.do(ctx, &message{Cmd: cmdPause, Paused: paused})
}
func (p *Player) SeekRelative(ctx context.Context, d time.Duration) error {
	return p.do(ctx, &message{Cmd: cmdSeek, Millis: d.Milliseconds()})
}
func (p *Player) SetVolume(ctx context.Context, v int) error {
	return p.do(ctx, &message{Cmd: cmdVolume, Volume: min(max(v, 0), 100)})
}

var _ music.Levels = (*Player)(nil)

// SetLevels switches the daemon's level measuring on or off.
func (p *Player) SetLevels(ctx context.Context, on bool) error {
	return p.do(ctx, &message{Cmd: cmdMeter, Meter: &on})
}

// Level reads the daemon's latest level; ok is false when it is not measuring or nothing is playing.
func (p *Player) Level(ctx context.Context) (music.Level, bool) {
	resp, err := p.call(ctx, &message{Cmd: cmdLevel})
	if err != nil || resp == nil || resp.Level == nil || !resp.Level.OK {
		return music.Level{}, false
	}
	return music.Level{RMS: resp.Level.RMS, Peak: resp.Level.Peak}, true
}

// Close drops the connection and leaves the daemon playing.
func (p *Player) Close() error {
	p.mu.Lock()
	c := p.conn
	p.mu.Unlock()
	if c == nil {
		return nil
	}
	return c.Close()
}

// Shutdown stops the daemon: it asks politely, waits for the connection to
// close, and ends the process if it does not go. Its socket is gone afterwards.
func (p *Player) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	pid, done := p.pid, p.done
	p.mu.Unlock()
	if _, err := p.call(ctx, &message{Cmd: cmdShutdown}); err != nil && !errors.Is(err, ErrNotAttached) {
		// An answer that never came is the case to force below.
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(p.cfg.CommandTimeout):
		}
	}
	if pid == os.Getpid() {
		return nil // a daemon inside this very process (a test): there is no other process to end
	}
	// The daemon removes its socket itself; give it a moment, then make sure it is gone.
	deadline := time.Now().Add(p.cfg.CommandTimeout)
	for pidAlive(pid) && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	if pidAlive(pid) {
		killPID(pid)
		cleanEndpoint(Endpoint(p.cfg.Paths))
		return fmt.Errorf("the native player did not stop within %s and was ended", p.cfg.CommandTimeout)
	}
	return nil
}
