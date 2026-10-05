package native

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// ErrAlreadyRunning is returned by RunDaemon when another daemon holds the player's lock.
var ErrAlreadyRunning = errors.New("another pig-music native player is already running")

// DaemonConfig configures RunDaemon.
type DaemonConfig struct {
	Paths  music.Paths
	Opener Opener
	Output Output
	// IdleExit ends the daemon after this long with no client connected and nothing
	// playing; zero means it runs until told to stop.
	IdleExit time.Duration
	// CommandTimeout bounds the handling of one request; default 10 s.
	CommandTimeout time.Duration
	// Tick is the engine's position interval.
	Tick time.Duration
	// Log receives one redacted line per event; nil discards.
	Log func(string)
}

const maxClients = 16

type daemon struct {
	cfg    DaemonConfig
	eng    *Engine
	ln     net.Listener
	quit   chan struct{}
	once   sync.Once
	mu     sync.Mutex
	conns  map[net.Conn]struct{}
	active time.Time // last time a client was connected or something played
}

func (c DaemonConfig) log(s string) {
	if c.Log != nil {
		c.Log(Redact(s))
	}
}

func (d *daemon) logf(format string, a ...any) {
	if d.cfg.Log != nil {
		d.cfg.Log(Redact(fmt.Sprintf(format, a...)))
	}
}

func (d *daemon) stop() { d.once.Do(func() { close(d.quit) }) }

// RunDaemon runs the player until ctx ends, a client sends shutdown, the idle
// timeout passes, or SIGTERM, SIGINT or SIGHUP arrives. It holds a lock for its
// whole life, so a second daemon for the same directory refuses to start, and a
// socket file left by a dead one is known to be stale and replaced. On the way
// out it stops playback, closes the device, removes its socket and releases the lock.
func RunDaemon(ctx context.Context, cfg DaemonConfig) error {
	if cfg.CommandTimeout <= 0 {
		cfg.CommandTimeout = 10 * time.Second
	}
	if err := cfg.Paths.EnsureDir(); err != nil {
		return err
	}
	unlock, ok, err := tryLockFile(LockPath(cfg.Paths))
	if err != nil {
		return err
	}
	if !ok {
		return ErrAlreadyRunning
	}
	defer unlock()
	endpoint := Endpoint(cfg.Paths)
	cleanEndpoint(endpoint) // the lock is ours, so a socket file here belongs to a dead daemon
	ln, err := listen(endpoint)
	if err != nil {
		if errors.Is(err, os.ErrExist) || errors.Is(err, syscall.EADDRINUSE) {
			return ErrAlreadyRunning
		}
		return fmt.Errorf("listening: %w", RedactError(err))
	}
	defer cleanEndpoint(endpoint)
	eng, err := NewEngine(EngineConfig{Opener: cfg.Opener, Output: cfg.Output, Tick: cfg.Tick, Log: func(s string) { cfg.log(s) }})
	if err != nil {
		_ = ln.Close()
		return fmt.Errorf("starting the player: %w", RedactError(err))
	}
	d := &daemon{cfg: cfg, eng: eng, ln: ln, quit: make(chan struct{}), conns: map[net.Conn]struct{}{}, active: time.Now()}
	d.logf("native player started (pid %d)", os.Getpid())

	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, shutdownSignals...)
	defer signal.Stop(sigs)
	go func() {
		select {
		case s := <-sigs:
			d.logf("stopping on %v", s)
			d.stop()
		case <-ctx.Done():
			d.stop()
		case <-d.quit:
		}
	}()
	if cfg.IdleExit > 0 {
		go d.watchIdle()
	}
	// wg counts the accept loop and every connection it serves; the loop holds
	// its own count, so an Add for a new connection never races the Wait below.
	var wg sync.WaitGroup
	accepting := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(accepting)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			d.mu.Lock()
			if len(d.conns) >= maxClients {
				d.mu.Unlock()
				_ = conn.Close()
				continue
			}
			d.conns[conn] = struct{}{}
			d.active = time.Now()
			d.mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				d.serve(conn)
			}()
		}
	}()
	<-d.quit
	_ = ln.Close()
	<-accepting // no connection is added after this
	d.mu.Lock()
	for c := range d.conns {
		_ = c.Close()
	}
	d.mu.Unlock()
	err = eng.Close()
	wg.Wait()
	d.logf("native player stopped")
	return err
}

func (d *daemon) watchIdle() {
	t := time.NewTicker(max(d.cfg.IdleExit/4, 10*time.Millisecond))
	defer t.Stop()
	for {
		select {
		case <-d.quit:
			return
		case <-t.C:
		}
		busy := d.eng.State().Index >= 0
		d.mu.Lock()
		if busy || len(d.conns) > 0 {
			d.active = time.Now()
		}
		idle := time.Since(d.active)
		d.mu.Unlock()
		if idle >= d.cfg.IdleExit {
			d.logf("idle for %v with no client: exiting", idle.Round(time.Second))
			d.stop()
			return
		}
	}
}

func (d *daemon) serve(conn net.Conn) {
	defer func() {
		_ = conn.Close()
		d.mu.Lock()
		delete(d.conns, conn)
		d.active = time.Now()
		d.mu.Unlock()
	}()
	c := newCodec(conn)
	var wmu sync.Mutex
	write := func(m *message) error {
		wmu.Lock()
		defer wmu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		return c.write(m)
	}
	done := make(chan struct{})
	defer close(done)
	subscribed := false
	for {
		m, err := c.read()
		if err != nil {
			return
		}
		resp := &message{ID: m.ID}
		if m.Cmd == cmdSubscribe {
			if !subscribed {
				subscribed = true
				ch := d.eng.Subscribe()
				go func() {
					defer d.eng.Unsubscribe(ch)
					for {
						select {
						case st, ok := <-ch:
							if !ok {
								return
							}
							if write(&message{Event: "state", State: &st, LastError: d.eng.LastError()}) != nil {
								_ = conn.Close()
								return
							}
						case <-done:
							return
						}
					}
				}()
			}
			resp.OK = true
		} else {
			d.handle(m, resp)
		}
		if write(resp) != nil {
			return
		}
		if m.Cmd == cmdShutdown {
			d.stop()
			return
		}
	}
}

// handle runs one request. A request that takes longer than the command timeout is answered with an error.
func (d *daemon) handle(m *message, resp *message) {
	ctx, cancel := context.WithTimeout(context.Background(), d.cfg.CommandTimeout)
	defer cancel()
	local := &message{} // written by the worker alone, copied once it is done
	done := make(chan error, 1)
	go func() { done <- d.run(ctx, m, local) }()
	var err error
	select {
	case err = <-done:
		*resp = *local
		resp.ID = m.ID
	case <-ctx.Done():
		err = fmt.Errorf("%s did not finish within %v", m.Cmd, d.cfg.CommandTimeout)
	}
	if err != nil {
		resp.OK, resp.Error, resp.Code = false, Redact(err.Error()), codeFor(err)
		return
	}
	resp.OK = true
}

func (d *daemon) run(ctx context.Context, m *message, resp *message) error {
	e := d.eng
	var err error
	switch m.Cmd {
	case cmdHello, cmdState:
		resp.PID, resp.Version = os.Getpid(), ProtocolVersion
	case cmdReplace:
		err = e.Replace(ctx, m.Tracks, m.Index)
	case cmdEnqueue:
		err = e.Enqueue(ctx, m.Tracks...)
	case cmdRemove:
		err = e.Remove(ctx, m.Index)
	case cmdMove:
		err = e.Move(ctx, m.Index, m.To)
	case cmdJump:
		err = e.Jump(ctx, m.Index)
	case cmdNext:
		err = e.Next(ctx)
	case cmdPrev:
		err = e.Prev(ctx)
	case cmdPause:
		err = e.SetPaused(ctx, m.Paused)
	case cmdToggle:
		err = e.TogglePause(ctx)
	case cmdSeek:
		err = e.SeekRelative(ctx, time.Duration(m.Millis)*time.Millisecond)
	case cmdVolume:
		err = e.SetVolume(ctx, m.Volume)
	case cmdMeter:
		e.SetMeter(m.Meter != nil && *m.Meter)
	case cmdLevel:
		rms, peak, ok := e.Level()
		resp.Level = &levelReply{RMS: rms, Peak: peak, OK: ok}
	case cmdShutdown:
	default:
		return fmt.Errorf("unknown command %q", m.Cmd)
	}
	st := e.State()
	resp.State, resp.LastError = &st, e.LastError()
	return err
}
