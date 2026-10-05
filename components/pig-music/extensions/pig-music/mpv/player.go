package mpv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

var (
	// ErrEndOfQueue is returned by Next on the last entry.
	ErrEndOfQueue = errors.New("already at the end of the queue")
	// ErrNothingPlaying is returned by Next and Prev when the queue is idle.
	ErrNothingPlaying = errors.New("nothing is playing")
	// ErrNotSeekable is returned by SeekRelative while the track is still loading (mpv refuses to seek then)
	// and for a stream with no length.
	ErrNotSeekable = errors.New("the track cannot be sought yet: it is still loading, or it has no length")
	// ErrNotAttached is returned by a call made before Attach succeeded.
	ErrNotAttached = errors.New("not attached to mpv")
)

// restartAfter is how far into a track Prev restarts it instead of going back.
const restartAfter = 3 * time.Second

// Config configures a Player.
type Config struct {
	// MPVPath is the mpv program; empty means "mpv" from PATH.
	MPVPath string
	// YtdlPath is the yt-dlp program for mpv's hook; empty lets mpv find yt-dlp on PATH.
	YtdlPath string
	Paths    music.Paths
	// PlayURL turns a track into the URL mpv plays; Source.PlayURL in production.
	PlayURL func(music.Track) string
	// Prefetch, when set, is called (on its own goroutine, once for each) with the URL of the queue entry after the current one,
	// so that it can be resolved while the current track plays (package prefetch). It must not block the caller.
	Prefetch func(url string)
	// StreamFailed, when set, is called (on its own goroutine) with the URL of an entry mpv could not open, so that a
	// prefetched answer whose stream was refused is not used again.
	StreamFailed func(url string)
	// ExtraArgs are added to mpv's command line when the player starts it (for example --ao=null).
	ExtraArgs []string
	// Env is added to mpv's environment when the player starts it.
	Env []string
	// StartTimeout is how long Attach waits for a new mpv's socket; default 10 s.
	StartTimeout time.Duration
	// CommandTimeout bounds every IPC command, so that an mpv that has stopped
	// answering is reported instead of waited on; default 5 s.
	CommandTimeout time.Duration
	// LookPath finds a program; exec.LookPath when nil.
	LookPath func(string) (string, error)
}

// Player is a music.Player over one mpv process.
type Player struct {
	cfg Config

	opMu sync.Mutex // serializes multi-step operations (Replace, Move)

	mu     sync.Mutex
	client *Client
	v      view
	meta   map[string]music.Track
	st     music.State
	subs   []chan music.State
	closed bool

	offered string // the last URL handed to Config.Prefetch
}

var _ music.Player = (*Player)(nil)

// New returns a Player that is not attached yet.
func New(cfg Config) *Player {
	if cfg.MPVPath == "" {
		cfg.MPVPath = "mpv"
	}
	if cfg.StartTimeout == 0 {
		cfg.StartTimeout = 10 * time.Second
	}
	if cfg.CommandTimeout == 0 {
		cfg.CommandTimeout = 5 * time.Second
	}
	if cfg.LookPath == nil {
		cfg.LookPath = exec.LookPath
	}
	p := &Player{cfg: cfg, v: newView(), meta: map[string]music.Track{}}
	p.st = p.v.state(p.meta)
	return p
}

// Attach connects to the mpv listening on the socket, or starts one. A socket
// nobody answers on is stale: it is removed and a new mpv takes its place.
func (p *Player) Attach(ctx context.Context) error {
	p.mu.Lock()
	if p.client != nil {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()
	if err := p.cfg.Paths.EnsureDir(); err != nil {
		return err
	}
	client, err := p.dialExisting(ctx)
	var stopStarted func() // stops the mpv this call started, nil when it reused one
	if err != nil {
		if client, stopStarted, err = p.startAndDial(ctx); err != nil {
			return err
		}
	}
	if err := p.adopt(ctx, client); err != nil {
		// An mpv this call started and could not use would idle on, unseen, in a
		// session of its own.
		if stopStarted != nil {
			stopStarted()
		}
		return err
	}
	return nil
}

// dialExisting connects to a running mpv, or fails when there is none.
func (p *Player) dialExisting(ctx context.Context) (*Client, error) {
	if _, err := os.Stat(p.cfg.Paths.Socket); err != nil {
		return nil, err
	}
	dctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return Dial(dctx, p.cfg.Paths.Socket, p.onEvent)
}

// startAndDial starts mpv and connects to it. stop ends that mpv; it is nil
// when another process started the mpv while this one waited for the lock.
func (p *Player) startAndDial(ctx context.Context) (c *Client, stop func(), err error) {
	if _, err := p.cfg.LookPath(p.cfg.MPVPath); err != nil {
		return nil, nil, &music.MissingError{Names: []string{"mpv"}, GOOS: runtime.GOOS}
	}
	lctx, cancel := context.WithTimeout(ctx, p.cfg.StartTimeout)
	defer cancel()
	unlock, err := lockFile(lctx, p.cfg.Paths.Socket+".lock")
	if err != nil {
		return nil, nil, fmt.Errorf("waiting for another pig-music to start mpv: %w", err)
	}
	defer unlock()
	// The holder of the lock may have started mpv while this call waited.
	if c, err := p.dialExisting(ctx); err == nil {
		return c, nil, nil
	}
	_ = os.Remove(p.cfg.Paths.Socket) // stale: nothing answered
	return p.start(lctx)
}

// scriptOptValue is v as a value of mpv's key-value list option: as it is when that is safe, else in mpv's length-prefixed form
// %<bytes>%<value> (mpv has no backslash escape there: a comma would end the value).
func scriptOptValue(v string) string {
	if strings.ContainsAny(v, ",\"[]%") {
		return "%" + strconv.Itoa(len(v)) + "%" + v
	}
	return v
}

func (p *Player) mpvArgs() []string {
	args := []string{
		// No user configuration: an mpv.conf line such as ytdl-raw-options=cookies-from-browser=... (or an auto profile,
		// script-opts or a user script) would otherwise give the yt-dlp hook the browser's cookies for every track.
		// Playback never carries cookies; settings the player needs come from pig-music (PIG_MUSIC_MPV_ARGS for more).
		"--no-config",
		"--idle=yes", "--no-video", "--force-window=no", "--audio-display=no",
		"--ytdl-format=bestaudio",
		// mpv's terminal output, at warning level, is its log (start sends it to
		// mpv.log). --log-file is not used: it is verbose whatever --msg-level says
		// and records the signed stream URLs, with the listener's IP address, that
		// the yt-dlp hook resolves.
		"--msg-level=all=warn", "--quiet", "--input-terminal=no",
		"--input-ipc-server=" + p.cfg.Paths.Socket,
	}
	// Every queue entry is a YouTube watch URL that only the yt-dlp hook can resolve: mpv is told to run it at once instead of
	// first fetching the page as a plain file (230 ms, measured). ytdl_path names the yt-dlp to run, when there is one.
	opts := "ytdl_hook-try_ytdl_first=yes"
	if p.cfg.YtdlPath != "" {
		opts = "ytdl_hook-ytdl_path=" + scriptOptValue(p.cfg.YtdlPath) + "," + opts
	}
	args = append(args, "--script-opts="+opts)
	args = append(args, p.cfg.ExtraArgs...)
	// The hook's yt-dlp ignores the user's yt-dlp config, as the Source and the
	// doctor do: a --cookies-from-browser there would read browser cookies at
	// every track. Appended last so that a --ytdl-raw-options= in ExtraArgs
	// cannot replace it.
	return append(args, "--ytdl-raw-options-append=ignore-config=")
}

// start runs mpv in a session of its own and connects to it. An mpv that exits,
// or does not open its socket before ctx ends, is reported; one still running
// then is stopped, so that a failed start leaves nothing behind.
func (p *Player) start(ctx context.Context) (*Client, func(), error) {
	logPath := filepath.Join(p.cfg.Paths.Dir, "mpv.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("mpv log: %w", err)
	}
	defer logFile.Close() // mpv holds its own descriptor
	if err := logFile.Chmod(0o600); err != nil {
		return nil, nil, fmt.Errorf("mpv log: %w", err)
	}
	cmd := exec.Command(p.cfg.MPVPath, p.mpvArgs()...)
	cmd.Env = append(os.Environ(), p.cfg.Env...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, logFile, logFile
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("starting mpv: %w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	stop := func() {
		kill(cmd)
		select {
		case <-exited:
		case <-time.After(2 * time.Second):
		}
		_ = os.Remove(p.cfg.Paths.Socket)
	}
	for {
		dctx, cancel := context.WithTimeout(ctx, time.Second)
		c, err := Dial(dctx, p.cfg.Paths.Socket, p.onEvent)
		cancel()
		if err == nil {
			return c, stop, nil
		}
		select {
		case werr := <-exited:
			return nil, nil, fmt.Errorf("mpv exited before it opened its socket (%v); see %s", werr, logPath)
		case <-ctx.Done():
			stop()
			return nil, nil, fmt.Errorf("mpv did not open %s in time, and was stopped: %w", p.cfg.Paths.Socket, ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
}

// adopt makes c the player's connection, observes the properties and reads the live state.
func (p *Player) adopt(ctx context.Context, c *Client) error {
	p.mu.Lock()
	p.client = c
	p.v = newView()
	p.v.connected = true
	p.meta = loadMeta(p.cfg.Paths.Meta)
	p.mu.Unlock()
	go func() {
		<-c.Done()
		p.disconnected(c)
	}()
	for i, name := range propertyNames {
		if _, err := p.call(ctx, c, "observe_property", i+1, name); err != nil {
			p.drop(c)
			return fmt.Errorf("observe %s: %w", name, err)
		}
	}
	if _, err := p.Sync(ctx); err != nil {
		p.drop(c)
		return err
	}
	return nil
}

// drop forgets a connection that could not be adopted, so that Attach fails as a whole.
func (p *Player) drop(c *Client) {
	_ = c.Close()
	p.disconnected(c)
}

// call sends one command, bounded by the CommandTimeout as well as by ctx: mpv
// answers at once when it is well, so a missing answer means it is stuck.
func (p *Player) call(ctx context.Context, c *Client, args ...any) (json.RawMessage, error) {
	cctx, cancel := context.WithTimeout(ctx, p.cfg.CommandTimeout)
	defer cancel()
	data, err := c.Command(cctx, args...)
	if err != nil && ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
		return nil, fmt.Errorf("mpv did not answer %v within %s; it may be hung (its socket is %s)", args[0], p.cfg.CommandTimeout, p.cfg.Paths.Socket)
	}
	return data, err
}

func (p *Player) disconnected(c *Client) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.client != c {
		return
	}
	p.client = nil
	p.v.connected = false
	p.refresh()
	for _, ch := range p.subs {
		close(ch)
	}
	p.subs = nil
}

// onEvent runs for every message mpv sends unasked.
func (p *Player) onEvent(ev Event) {
	if ev.Event == "end-file" && ev.Reason == "error" {
		p.failed(ev.PlaylistEntryID)
		return
	}
	if ev.Event != "property-change" || ev.Name == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.v.apply(ev.Name, ev.Data)
	switch ev.Name {
	case "pause", "time-pos", "duration", "volume":
		// time-pos arrives many times a second; the queue is not rebuilt for it.
		p.st.Paused = p.v.pause
		p.st.Position = seconds(p.v.timePos)
		p.st.Duration = seconds(p.v.duration)
		p.st.Volume = int(p.v.volume + 0.5)
		p.publish()
	default:
		p.refresh()
	}
}

// failed reports the entry mpv could not open to Config.StreamFailed.
func (p *Player) failed(id int) {
	if p.cfg.StreamFailed == nil || id == 0 {
		return
	}
	p.mu.Lock()
	url := ""
	for _, e := range p.v.entries {
		if e.ID == id {
			url = e.Filename
		}
	}
	p.mu.Unlock()
	if url != "" {
		go p.cfg.StreamFailed(url)
	}
}

// refresh recomputes the whole state and publishes it. The caller holds p.mu.
func (p *Player) refresh() {
	p.st = p.v.state(p.meta)
	p.publish()
	p.offerNext()
}

// offerNext hands the URL of the entry after the current one to Config.Prefetch, once for each. The caller holds p.mu.
func (p *Player) offerNext() {
	if p.cfg.Prefetch == nil || p.v.pos < 0 || p.v.pos+1 >= len(p.v.entries) {
		return
	}
	next := p.v.entries[p.v.pos+1].Filename
	if next == "" || next == p.offered {
		return
	}
	p.offered = next
	go p.cfg.Prefetch(next)
}

// publish sends the state to every subscriber, replacing one they have not read. The caller holds p.mu.
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

// State returns the last state mpv reported.
func (p *Player) State() music.State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.st
}

// Subscribe delivers the current state at once and every change after it.
func (p *Player) Subscribe() <-chan music.State {
	p.mu.Lock()
	defer p.mu.Unlock()
	ch := make(chan music.State, 1)
	ch <- p.st
	if p.client == nil { // not attached, or already gone: the channel closes at once
		close(ch)
		return ch
	}
	p.subs = append(p.subs, ch)
	return ch
}

// Sync reads every observed property from mpv and returns the state.
func (p *Player) Sync(ctx context.Context) (music.State, error) {
	c, err := p.conn()
	if err != nil {
		return music.State{}, err
	}
	values := make(map[string]json.RawMessage, len(propertyNames))
	for _, name := range propertyNames {
		raw, err := p.call(ctx, c, "get_property", name)
		if err != nil {
			if err.Error() == "mpv: property unavailable" {
				raw = nil // idle: no position, duration or title
			} else {
				return music.State{}, fmt.Errorf("get %s: %w", name, err)
			}
		}
		values[name] = raw
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, name := range propertyNames {
		p.v.apply(name, values[name])
	}
	p.refresh()
	return p.st, nil
}

func (p *Player) conn() (*Client, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.client == nil {
		return nil, ErrNotAttached
	}
	return p.client, nil
}

func (p *Player) urlFor(t music.Track) (string, error) {
	if p.cfg.PlayURL == nil {
		return "", errors.New("mpv player has no PlayURL")
	}
	u := p.cfg.PlayURL(t)
	if u == "" {
		return "", fmt.Errorf("no URL for track %q", t.ID)
	}
	return u, nil
}

// remember records the tracks' metadata, keyed by the video ID of their URLs.
func (p *Player) remember(tracks []music.Track, urls []string) error {
	p.mu.Lock()
	for i, t := range tracks {
		if id := videoID(urls[i]); id != "" {
			p.meta[id] = t
		}
	}
	if len(p.meta) > maxMeta {
		keep := map[string]music.Track{}
		for _, e := range p.v.entries {
			if id := videoID(e.Filename); id != "" {
				keep[id] = p.meta[id]
			}
		}
		for i, t := range tracks {
			if id := videoID(urls[i]); id != "" {
				keep[id] = t
			}
		}
		p.meta = keep
	}
	meta := cloneMeta(p.meta)
	p.mu.Unlock()
	return saveMeta(p.cfg.Paths.Meta, meta)
}

func cloneMeta(m map[string]music.Track) map[string]music.Track {
	out := make(map[string]music.Track, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Replace sets the queue to tracks and plays tracks[startAt]. The track that
// plays is loaded first, so sound starts at once; the rest of the queue is built
// around it.
func (p *Player) Replace(ctx context.Context, tracks []music.Track, startAt int) error {
	if len(tracks) == 0 {
		return errors.New("nothing to play: the track list is empty")
	}
	if startAt < 0 || startAt >= len(tracks) {
		return fmt.Errorf("start index %d is outside the %d tracks", startAt, len(tracks))
	}
	c, err := p.conn()
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.v.shuffled = false // a new queue is in the order it was given
	p.mu.Unlock()
	urls := make([]string, len(tracks))
	for i, t := range tracks {
		if urls[i], err = p.urlFor(t); err != nil {
			return err
		}
	}
	if err := p.remember(tracks, urls); err != nil {
		return err
	}
	p.opMu.Lock()
	defer p.opMu.Unlock()
	if _, err := p.call(ctx, c, "loadfile", urls[startAt], "replace"); err != nil {
		return err
	}
	for _, u := range urls[startAt+1:] {
		if _, err := p.call(ctx, c, "loadfile", u, "append"); err != nil {
			return err
		}
	}
	// The tracks before startAt go in front of the playing one, in order.
	for i := 0; i < startAt; i++ {
		if _, err := p.call(ctx, c, "loadfile", urls[i], "append"); err != nil {
			return err
		}
		last := len(urls) - startAt + i // index of the entry just appended
		if _, err := p.call(ctx, c, "playlist-move", last, i); err != nil {
			return err
		}
	}
	return nil
}

// Enqueue appends tracks, and starts playing when nothing was.
func (p *Player) Enqueue(ctx context.Context, tracks ...music.Track) error {
	if len(tracks) == 0 {
		return nil
	}
	c, err := p.conn()
	if err != nil {
		return err
	}
	urls := make([]string, len(tracks))
	for i, t := range tracks {
		if urls[i], err = p.urlFor(t); err != nil {
			return err
		}
	}
	if err := p.remember(tracks, urls); err != nil {
		return err
	}
	p.opMu.Lock()
	defer p.opMu.Unlock()
	for _, u := range urls {
		if _, err := p.call(ctx, c, "loadfile", u, "append-play"); err != nil {
			return err
		}
	}
	return nil
}

func (p *Player) command(ctx context.Context, args ...any) error {
	c, err := p.conn()
	if err != nil {
		return err
	}
	_, err = p.call(ctx, c, args...)
	return err
}

func (p *Player) index(i int) error {
	n := len(p.State().Queue)
	if i < 0 || i >= n {
		return fmt.Errorf("queue position %d does not exist (the queue has %d)", i+1, n)
	}
	return nil
}

// Remove drops the queue entry at index.
func (p *Player) Remove(ctx context.Context, index int) error {
	if err := p.index(index); err != nil {
		return err
	}
	return p.command(ctx, "playlist-remove", index)
}

// Move puts the entry at from at position to. mpv's playlist-move puts an entry
// before the one at its second argument, so a move down names the entry after.
func (p *Player) Move(ctx context.Context, from, to int) error {
	if err := p.index(from); err != nil {
		return err
	}
	if err := p.index(to); err != nil {
		return err
	}
	if from == to {
		return nil
	}
	before := to
	if to > from {
		before = to + 1
	}
	p.opMu.Lock()
	defer p.opMu.Unlock()
	return p.command(ctx, "playlist-move", from, before)
}

// Jump plays the queue entry at index.
func (p *Player) Jump(ctx context.Context, index int) error {
	if err := p.index(index); err != nil {
		return err
	}
	return p.command(ctx, "playlist-play-index", index)
}

// Next plays the next entry. On the last entry it fails rather than stopping the player.
func (p *Player) Next(ctx context.Context) error {
	st := p.State()
	switch {
	case st.Index < 0:
		return ErrNothingPlaying
	case st.Index >= len(st.Queue)-1 && st.Repeat != music.RepeatAll:
		return ErrEndOfQueue // with repeat all, mpv's loop-playlist makes playlist-next wrap to the first entry
	}
	return p.command(ctx, "playlist-next")
}

// Prev restarts the current track when it is past the first moments or is the
// first in the queue, and otherwise plays the previous one.
func (p *Player) Prev(ctx context.Context) error {
	st := p.State()
	switch {
	case st.Index < 0:
		return ErrNothingPlaying
	case st.Index == 0 || st.Position > restartAfter:
		return p.command(ctx, "seek", 0, "absolute")
	}
	return p.command(ctx, "playlist-prev")
}

// TogglePause flips the pause state.
func (p *Player) TogglePause(ctx context.Context) error { return p.command(ctx, "cycle", "pause") }

// SetPaused sets the pause state.
func (p *Player) SetPaused(ctx context.Context, paused bool) error {
	return p.command(ctx, "set_property", "pause", paused)
}

// SeekRelative moves the playback position by d.
func (p *Player) SeekRelative(ctx context.Context, d time.Duration) error {
	st := p.State()
	switch {
	case st.Index < 0:
		return ErrNothingPlaying
	case st.Duration <= 0:
		return ErrNotSeekable
	}
	if err := p.command(ctx, "seek", d.Seconds(), "relative"); err != nil {
		return fmt.Errorf("seek: %w", err)
	}
	return nil
}

// SetVolume sets the volume, clamped to 0..100.
func (p *Player) SetVolume(ctx context.Context, v int) error {
	return p.command(ctx, "set_property", "volume", min(max(v, 0), 100))
}

// SetRepeat sets what happens at the end of a track or the queue: mpv's loop-file and loop-playlist.
func (p *Player) SetRepeat(ctx context.Context, mode music.RepeatMode) error {
	var list, file string
	switch mode {
	case music.RepeatOff:
		list, file = "no", "no"
	case music.RepeatAll:
		list, file = "inf", "no"
	case music.RepeatOne:
		list, file = "no", "inf"
	default:
		return fmt.Errorf("repeat mode %q is not one of off, all, one", string(mode))
	}
	if err := p.command(ctx, "set_property", "loop-playlist", list); err != nil {
		return err
	}
	return p.command(ctx, "set_property", "loop-file", file)
}

// SetShuffle reorders the queue at random with the current track first, still playing, and every other track after it
// (playlist-shuffle, then playlist-move), or puts the order back (playlist-unshuffle, which needs mpv 0.37). mpv's
// playlist-shuffle moves the playing entry too, to a random place; left there, the queue would end after it and the tracks
// shuffled in front of it would never play. Whether the queue is shuffled is remembered here, not by mpv, so it is
// forgotten when the extension restarts and a new queue starts unshuffled.
func (p *Player) SetShuffle(ctx context.Context, on bool) error {
	cmd := "playlist-unshuffle"
	if on {
		cmd = "playlist-shuffle"
	}
	p.opMu.Lock()
	defer p.opMu.Unlock()
	if err := p.command(ctx, cmd); err != nil {
		if !on && strings.Contains(err.Error(), "unknown command") {
			return errors.New("turning shuffle off needs mpv 0.37 or later (playlist-unshuffle); the queue stays in its shuffled order")
		}
		return err
	}
	if on {
		if err := p.playingFirst(ctx); err != nil {
			return err
		}
	}
	p.mu.Lock()
	p.v.shuffled = on
	p.refresh()
	p.mu.Unlock()
	return nil
}

// playingFirst moves the playing entry to the front of mpv's playlist. It asks mpv for the position rather than using the
// state, whose playlist events may still be on their way. The caller holds opMu.
func (p *Player) playingFirst(ctx context.Context) error {
	c, err := p.conn()
	if err != nil {
		return err
	}
	raw, err := p.call(ctx, c, "get_property", "playlist-pos")
	if err != nil {
		return err
	}
	var pos int
	if err := json.Unmarshal(raw, &pos); err != nil || pos <= 0 {
		return nil // nothing playing (-1), or already first
	}
	_, err = p.call(ctx, c, "playlist-move", pos, 0)
	return err
}

// Close drops the connection and leaves mpv playing.
func (p *Player) Close() error {
	p.mu.Lock()
	c := p.client
	p.closed = true
	p.mu.Unlock()
	if c == nil {
		return nil
	}
	return c.Close()
}

// Shutdown stops mpv and forgets the queue's metadata.
func (p *Player) Shutdown(ctx context.Context) error {
	c, err := p.conn()
	if err != nil {
		return err
	}
	if _, err := p.call(ctx, c, "quit"); err != nil && !errors.Is(err, ErrClosed) {
		return err
	}
	select {
	case <-c.Done():
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(p.cfg.CommandTimeout):
		return fmt.Errorf("mpv accepted quit but did not exit within %s", p.cfg.CommandTimeout)
	}
	_ = os.Remove(p.cfg.Paths.Meta)
	if err := os.Remove(p.cfg.Paths.Socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Running reports whether something answers on the socket, without attaching.
func Running(ctx context.Context, socket string) bool {
	var d net.Dialer
	dctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	conn, err := d.DialContext(dctx, "unix", socket)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
