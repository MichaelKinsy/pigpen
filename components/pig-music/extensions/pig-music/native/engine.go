package native

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/mpv"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// Errors shared with the mpv engine, so that a caller treats both alike.
var (
	ErrEndOfQueue     = mpv.ErrEndOfQueue
	ErrNothingPlaying = mpv.ErrNothingPlaying
	ErrNotSeekable    = mpv.ErrNotSeekable
)

// Opener turns a track into audio: it resolves the stream, connects and demuxes.
// ctx lives as long as the audio is wanted; cancelling it must stop any read the
// returned Decoded has in flight.
type Opener interface {
	Open(ctx context.Context, t music.Track) (Decoded, error)
}

// Output is the audio device. It pulls 32-bit float little-endian stereo PCM at
// OutputRate from the reader it is started with.
type Output interface {
	// Start hands the device its source. Playback begins paused.
	Start(src io.Reader) error
	Resume()
	Pause()
	// SetGain sets the amplitude scale, 0..1.
	SetGain(g float64)
	// Buffered is the number of bytes pulled from the source and not yet played.
	Buffered() int
	// Flush discards buffered audio. The device must not be reading when it returns.
	Flush()
	Close() error
}

// EngineConfig configures an Engine.
type EngineConfig struct {
	Opener Opener
	Output Output
	// Tick is how often a playing position is published; default 250 ms.
	Tick time.Duration
	// OpenTimeout bounds opening one track; default 30 s.
	OpenTimeout time.Duration
	// StallTimeout gives up on a read that has not returned for this long; default 45 s.
	StallTimeout time.Duration
	// Log receives a line when a track cannot be played; the text has no URLs or addresses.
	Log func(string)
}

const (
	restartAfter  = 3 * time.Second
	bytesPerFrame = 8
)

// Engine is the player's core: the queue, the position and the audio path, over
// an Opener and an Output. It runs inside the daemon, or in a test.
type Engine struct {
	cfg EngineConfig

	outMu sync.Mutex // serializes use of the Output; taken before mu, never after

	mu       sync.Mutex // guards everything below
	queue    []music.Track
	index    int // -1 when idle
	paused   bool
	volume   int
	cur      *playing // the track being played; nil while loading or idle
	loading  bool     // index names a track that is being opened
	next     *playing // the track after cur, opened ahead of time (or being)
	opening  *playing // the track a start is waiting for; nil once it is current
	flush    bool     // the device holds audio of the old position
	gen      int      // bumped for every change of what plays; stale loads check it
	lastErr  string
	subs     []chan music.State
	closed   bool
	loadStop context.CancelFunc

	wake chan struct{}
	done chan struct{}
	wg   sync.WaitGroup

	// the reader's scratch
	fbuf []float32
	rem  []byte

	// levels: measured in pcmRead when switched on, read by Level
	meter  atomic.Bool
	lvlMu  sync.Mutex
	lvlRMS float64
	lvlPk  float64
	lvlAt  time.Time
}

// playing is one open track.
type playing struct {
	idx     int
	track   music.Track
	dec     Decoded
	cancel  context.CancelFunc
	dmu     sync.Mutex   // held while dec is read or sought
	pos     atomic.Int64 // frame handed to the device next
	total   int64
	seekG   atomic.Int64
	ready   chan struct{}
	err     error // set before ready closes when opening failed
	closed  bool
	seeking bool // guarded by Engine.mu: a seek is in progress and the device is held
}

// NewEngine starts an engine. Close it when done.
func NewEngine(cfg EngineConfig) (*Engine, error) {
	if cfg.Tick <= 0 {
		cfg.Tick = 250 * time.Millisecond
	}
	if cfg.OpenTimeout <= 0 {
		cfg.OpenTimeout = 30 * time.Second
	}
	if cfg.StallTimeout <= 0 {
		cfg.StallTimeout = 45 * time.Second
	}
	e := &Engine{cfg: cfg, index: -1, volume: 100, wake: make(chan struct{}, 1), done: make(chan struct{})}
	if err := cfg.Output.Start(readerFunc(e.pcmRead)); err != nil {
		return nil, err
	}
	cfg.Output.SetGain(gain(e.volume))
	e.wg.Add(1)
	go e.loop()
	return e, nil
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

// Seek lets the device clear its buffer (oto only flushes a source that can seek); the engine positions itself.
func (f readerFunc) Seek(int64, int) (int64, error) { return 0, nil }

// gain maps a volume of 0..100 to an amplitude. mpv's volume scale is cubic.
func gain(v int) float64 {
	x := float64(min(max(v, 0), 100)) / 100
	return x * x * x
}

func (e *Engine) kick() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

// loop applies the output state, publishes position while playing, and keeps the next track ready.
func (e *Engine) loop() {
	defer e.wg.Done()
	t := time.NewTicker(e.cfg.Tick)
	defer t.Stop()
	for {
		select {
		case <-e.done:
			return
		case <-e.wake:
		case <-t.C:
			e.mu.Lock()
			playing := e.cur != nil && !e.paused
			e.mu.Unlock()
			if !playing {
				continue
			}
		}
		e.syncOutput()
		e.ensureNext()
		e.mu.Lock()
		e.publishLocked()
		e.mu.Unlock()
	}
}

// syncOutput makes the device match the state: playing only while a track is
// ready and not paused, and empty after a jump or seek. It must not be called
// with mu held.
func (e *Engine) syncOutput() {
	e.outMu.Lock()
	defer e.outMu.Unlock()
	e.mu.Lock()
	run := e.cur != nil && !e.cur.seeking && !e.paused && !e.closed
	flush := e.flush
	e.flush = false
	e.mu.Unlock()
	if !run || flush {
		e.cfg.Output.Pause()
	}
	if flush {
		e.cfg.Output.Flush()
	}
	if run {
		e.cfg.Output.Resume()
	}
}

// ── state ───────────────────────────────────────────────────────────────────

// State is the engine's state as the player interface reports it.
func (e *Engine) State() music.State {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stateLocked()
}

func (e *Engine) stateLocked() music.State {
	st := music.State{Connected: !e.closed, Volume: e.volume, Paused: e.paused, Index: e.index}
	st.Queue = append([]music.Track(nil), e.queue...)
	if e.index >= 0 && e.index < len(e.queue) {
		t := e.queue[e.index]
		st.Track = &t
	} else {
		st.Index = -1
	}
	if c := e.cur; c != nil {
		st.Position = e.positionLocked(c)
		if c.total > 0 {
			st.Duration = framesToDuration(c.total)
		}
	}
	return st
}

func (e *Engine) positionLocked(c *playing) time.Duration {
	buffered := int64(e.cfg.Output.Buffered() / bytesPerFrame)
	return framesToDuration(max(c.pos.Load()-buffered, 0))
}

func framesToDuration(f int64) time.Duration {
	return time.Duration(f) * time.Second / OutputRate
}

func durationToFrames(d time.Duration) int64 { return int64(d) * OutputRate / int64(time.Second) }

// LastError is the reason the last track could not be played, empty when none failed since the queue was set.
func (e *Engine) LastError() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastErr
}

// Subscribe delivers the current state at once and every change after it. A slow reader sees the latest.
func (e *Engine) Subscribe() <-chan music.State {
	e.mu.Lock()
	defer e.mu.Unlock()
	ch := make(chan music.State, 1)
	ch <- e.stateLocked()
	if e.closed {
		close(ch)
		return ch
	}
	e.subs = append(e.subs, ch)
	return ch
}

// Unsubscribe stops deliveries to a channel returned by Subscribe and closes it.
func (e *Engine) Unsubscribe(ch <-chan music.State) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i, c := range e.subs {
		if (<-chan music.State)(c) == ch {
			e.subs = append(e.subs[:i], e.subs[i+1:]...)
			close(c)
			return
		}
	}
}

func (e *Engine) publishLocked() {
	if len(e.subs) == 0 {
		return
	}
	st := e.stateLocked()
	for _, ch := range e.subs {
		select {
		case ch <- st:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- st:
			default:
			}
		}
	}
}

// changed publishes at once and lets the loop fix the output.
func (e *Engine) changed() {
	e.mu.Lock()
	e.publishLocked()
	e.mu.Unlock()
	e.kick()
}

// ── the audio path ──────────────────────────────────────────────────────────

// pcmRead is called by the device for more audio. It never blocks on the
// engine's state for long, and never returns an error or EOF: the device is
// paused when there is nothing to play, and silence covers a race with that.
func (e *Engine) pcmRead(p []byte) (int, error) {
	n := 0
	if len(e.rem) > 0 {
		n = copy(p, e.rem)
		e.rem = e.rem[n:]
	}
	for n < len(p) {
		frames := min((len(p)-n)/bytesPerFrame, 4096)
		if frames == 0 { // less than a frame of room: keep the rest for the next call
			frames = 1
		}
		got := e.readFrames(frames)
		if got == 0 {
			clear(p[n:])
			return len(p), nil // silence; the loop will pause the device
		}
		if cap(e.rem) < bytesPerFrame*4096 {
			e.rem = make([]byte, 0, bytesPerFrame*4096)
		}
		if e.meter.Load() {
			e.noteLevel(e.fbuf[:2*got])
		}
		buf := e.rem[:0]
		for _, f := range e.fbuf[:2*got] {
			buf = binary.LittleEndian.AppendUint32(buf, math.Float32bits(f))
		}
		c := copy(p[n:], buf)
		n += c
		e.rem = buf[c:]
		if len(e.rem) > 0 {
			break
		}
	}
	return n, nil
}

// readFrames decodes up to frames frames into e.fbuf, moving on to the next
// track at the end of one without a gap. It returns 0 when there is nothing to play.
func (e *Engine) readFrames(frames int) int {
	if cap(e.fbuf) < 2*frames {
		e.fbuf = make([]float32, 2*4096)
	}
	e.fbuf = e.fbuf[:2*frames]
	for tries := 0; tries < 4; tries++ {
		e.mu.Lock()
		c := e.cur
		hold := e.paused || (c != nil && c.seeking)
		e.mu.Unlock()
		if c == nil || hold {
			return 0
		}
		if !c.dmu.TryLock() { // a seek holds the decoder
			return 0
		}
		n, err := 0, error(nil)
		if !c.closed {
			// A read that never returns would hold the device on one track for
			// good: after the stall timeout the track's context is cancelled,
			// which fails the read, and the track is skipped.
			var stalled atomic.Bool
			t := time.AfterFunc(e.cfg.StallTimeout, func() { stalled.Store(true); c.cancel() })
			n, err = c.dec.Read(e.fbuf)
			t.Stop()
			if stalled.Load() {
				n, err = 0, fmt.Errorf("the stream stalled for %v", e.cfg.StallTimeout)
			}
		} else {
			err = io.EOF
		}
		if n > 0 {
			c.pos.Add(int64(n))
		}
		c.dmu.Unlock()
		if n > 0 {
			return n
		}
		if err != nil && !errors.Is(err, io.EOF) {
			e.trackFailed(c, fmt.Errorf("decoding: %w", err))
		} else {
			e.trackEnded(c)
		}
	}
	return 0
}

// trackEnded moves to the next track when c finishes, or goes idle after the last.
func (e *Engine) trackEnded(c *playing) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cur != c {
		return
	}
	e.advanceLocked(c.idx+1, false)
}

// trackFailed records why a track stopped and skips it.
func (e *Engine) trackFailed(c *playing, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cur != c {
		return
	}
	e.failLocked(c.track, err)
	e.advanceLocked(c.idx+1, false)
}

// advanceLocked makes queue entry idx the one that plays: the preloaded track
// when it is that one, else a fresh load. Past the end the engine goes idle,
// the queue kept, as mpv does.
func (e *Engine) advanceLocked(idx int, flush bool) {
	old := e.cur
	if idx >= len(e.queue) {
		e.index, e.cur, e.loading = -1, nil, false
		e.gen++
		e.dropNextLocked()
		e.dropOpeningLocked()
		e.retire(old)
		e.flush = false
		go e.kick()
		return
	}
	if n := e.next; n != nil && n.idx == idx && n.isReady() {
		e.next = nil
		e.index, e.cur, e.loading = idx, n, false
		e.gen++
		e.flush = e.flush || flush
		e.retire(old)
		go e.changed()
		return
	}
	e.retire(old)
	e.startLocked(idx)
}

func (p *playing) isReady() bool {
	select {
	case <-p.ready:
		return p.err == nil
	default:
		return false
	}
}

// retire stops reading a finished or replaced track and closes it without holding the engine up.
func (e *Engine) retire(p *playing) {
	if p == nil {
		return
	}
	p.cancel()
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		<-p.ready
		if p.dec == nil {
			return
		}
		p.dmu.Lock()
		defer p.dmu.Unlock()
		if !p.closed {
			p.closed = true
			_ = p.dec.Close()
		}
	}()
}

func (e *Engine) dropNextLocked() {
	if e.next != nil {
		e.retire(e.next)
		e.next = nil
	}
}

// startLocked begins playing queue entry idx: stops the old track, empties the device and opens the new one.
func (e *Engine) startLocked(idx int) {
	e.gen++
	e.index = idx
	e.retire(e.cur)
	e.cur = nil
	e.dropOpeningLocked()
	e.loading = true
	e.flush = true
	if n := e.next; n != nil && n.idx == idx {
		e.next = nil
		e.adoptLocked(n)
		return
	}
	e.dropNextLocked()
	p := e.openLocked(idx)
	e.adoptLocked(p)
}

// dropOpeningLocked stops waiting for a track that is no longer wanted and lets it close.
func (e *Engine) dropOpeningLocked() {
	if e.opening != nil {
		e.retire(e.opening)
		e.opening = nil
	}
}

// adoptLocked waits (without the lock) for p to open, then makes it current if the queue still wants it.
func (e *Engine) adoptLocked(p *playing) {
	gen := e.gen
	e.opening = p
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		select {
		case <-p.ready:
		case <-e.done:
			return
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.gen != gen || e.closed {
			return // whoever replaced it has retired it
		}
		e.opening = nil
		if p.err != nil {
			e.failLocked(p.track, p.err)
			e.retire(p)
			e.advanceLocked(p.idx+1, true)
			return
		}
		e.cur, e.loading = p, false
		go e.changed()
	}()
}

// openLocked starts opening queue entry idx in the background.
func (e *Engine) openLocked(idx int) *playing {
	ctx, cancel := context.WithCancel(context.Background())
	p := &playing{idx: idx, track: e.queue[idx], cancel: cancel, ready: make(chan struct{})}
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		defer close(p.ready)
		// ctx must outlive the open: the decoder reads the network with it for as
		// long as the track plays. The time limit only cancels it while opening.
		var timedOut atomic.Bool
		timer := time.AfterFunc(e.cfg.OpenTimeout, func() { timedOut.Store(true); cancel() })
		dec, err := e.cfg.Opener.Open(ctx, p.track)
		timer.Stop()
		if timedOut.Load() {
			if err == nil {
				_ = dec.Close()
			}
			p.err = fmt.Errorf("opening took longer than %v: %w", e.cfg.OpenTimeout, context.DeadlineExceeded)
			return
		}
		if err == nil && ctx.Err() != nil {
			_ = dec.Close()
			err = ctx.Err()
		}
		if err != nil {
			p.err = err
			return
		}
		p.dec, p.total = dec, dec.Frames()
	}()
	return p
}

// ensureNext opens the track after the current one ahead of time, so that the
// change is gapless and a skip is instant.
func (e *Engine) ensureNext() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.cur == nil || e.index < 0 {
		return
	}
	want := e.index + 1
	if want >= len(e.queue) {
		e.dropNextLocked()
		return
	}
	if n := e.next; n != nil {
		if n.idx == want && n.track.ID == e.queue[want].ID {
			return // opened, or opening; a failed preload fails again when its turn comes
		}
		e.dropNextLocked()
	}
	e.next = e.openLocked(want)
}

func describeFailure(t music.Track, err error) string {
	name := t.Title
	if name == "" {
		name = t.ID
	}
	return fmt.Sprintf("could not play %q: %s", Clean(name), Redact(err.Error()))
}

// failLocked records why a track was skipped. The caller holds e.mu.
func (e *Engine) failLocked(t music.Track, err error) {
	e.lastErr = describeFailure(t, err)
	if e.cfg.Log != nil {
		e.cfg.Log(e.lastErr)
	}
}

// ── control ─────────────────────────────────────────────────────────────────

func (e *Engine) checkOpen() error {
	if e.closed {
		return errors.New("the player is shut down")
	}
	return nil
}

// Replace sets a new queue and plays it from startAt.
func (e *Engine) Replace(_ context.Context, tracks []music.Track, startAt int) error {
	if len(tracks) == 0 {
		return errors.New("nothing to play: the track list is empty")
	}
	if startAt < 0 || startAt >= len(tracks) {
		return fmt.Errorf("start index %d is outside the %d tracks", startAt, len(tracks))
	}
	e.mu.Lock()
	if err := e.checkOpen(); err != nil {
		e.mu.Unlock()
		return err
	}
	e.queue = append([]music.Track(nil), tracks...)
	e.lastErr = ""
	e.dropNextLocked()
	e.startLocked(startAt)
	e.mu.Unlock()
	e.changed()
	return nil
}

// Enqueue appends tracks, and starts playing the first of them when nothing was playing.
func (e *Engine) Enqueue(_ context.Context, tracks ...music.Track) error {
	if len(tracks) == 0 {
		return nil
	}
	e.mu.Lock()
	if err := e.checkOpen(); err != nil {
		e.mu.Unlock()
		return err
	}
	first := len(e.queue)
	e.queue = append(e.queue, tracks...)
	if e.index < 0 {
		e.startLocked(first)
	}
	e.mu.Unlock()
	e.changed()
	return nil
}

func (e *Engine) indexLocked(i int) error {
	if i < 0 || i >= len(e.queue) {
		return fmt.Errorf("queue position %d does not exist (the queue has %d)", i+1, len(e.queue))
	}
	return nil
}

// Remove drops entry index. Removing the playing one plays the entry that takes its place.
func (e *Engine) Remove(_ context.Context, index int) error {
	e.mu.Lock()
	if err := e.indexLocked(index); err != nil {
		e.mu.Unlock()
		return err
	}
	e.queue = append(e.queue[:index:index], e.queue[index+1:]...)
	switch {
	case index == e.index:
		e.dropNextLocked()
		if index < len(e.queue) {
			e.startLocked(index)
		} else {
			e.retire(e.cur)
			e.dropOpeningLocked()
			e.cur, e.index, e.loading, e.gen, e.flush = nil, -1, false, e.gen+1, true
		}
	case index < e.index:
		e.index--
		e.followIndexLocked()
		e.dropNextLocked()
	default:
		e.dropNextLocked()
	}
	e.mu.Unlock()
	e.changed()
	return nil
}

// Move puts the entry at from at position to; the playing track keeps playing.
func (e *Engine) Move(_ context.Context, from, to int) error {
	e.mu.Lock()
	if err := e.indexLocked(from); err != nil {
		e.mu.Unlock()
		return err
	}
	if err := e.indexLocked(to); err != nil {
		e.mu.Unlock()
		return err
	}
	if from != to {
		t := e.queue[from]
		rest := append(append([]music.Track(nil), e.queue[:from]...), e.queue[from+1:]...)
		e.queue = append(rest[:to:to], append([]music.Track{t}, rest[to:]...)...)
		switch {
		case e.index == from:
			e.index = to
		case from < e.index && to >= e.index:
			e.index--
		case from > e.index && to <= e.index:
			e.index++
		}
		e.followIndexLocked()
		e.dropNextLocked()
	}
	e.mu.Unlock()
	e.changed()
	return nil
}

// followIndexLocked gives the playing entry, or the one still opening for it,
// its new queue position after an edit, so that the entry after it in the
// edited queue is the one that plays next.
func (e *Engine) followIndexLocked() {
	if e.cur != nil {
		e.cur.idx = e.index
	}
	if e.opening != nil {
		e.opening.idx = e.index
	}
}

// Jump plays entry index.
func (e *Engine) Jump(_ context.Context, index int) error {
	e.mu.Lock()
	if err := e.indexLocked(index); err != nil {
		e.mu.Unlock()
		return err
	}
	e.startLocked(index)
	e.mu.Unlock()
	e.changed()
	return nil
}

// Next plays the next entry; on the last one it fails and leaves the player as it was.
func (e *Engine) Next(_ context.Context) error {
	e.mu.Lock()
	switch {
	case e.index < 0:
		e.mu.Unlock()
		return ErrNothingPlaying
	case e.index >= len(e.queue)-1:
		e.mu.Unlock()
		return ErrEndOfQueue
	}
	e.advanceLocked(e.index+1, true)
	e.mu.Unlock()
	e.changed()
	return nil
}

// Prev restarts the track when it is past its first moments or is the first, else plays the previous one.
func (e *Engine) Prev(ctx context.Context) error {
	e.mu.Lock()
	if e.index < 0 {
		e.mu.Unlock()
		return ErrNothingPlaying
	}
	if e.index == 0 || (e.cur != nil && e.positionLocked(e.cur) > restartAfter) {
		c := e.cur
		e.mu.Unlock()
		if c == nil {
			return e.Jump(ctx, 0)
		}
		return e.seekTo(c, 0)
	}
	e.startLocked(e.index - 1)
	e.mu.Unlock()
	e.changed()
	return nil
}

// SetPaused pauses or resumes.
func (e *Engine) SetPaused(_ context.Context, paused bool) error {
	e.mu.Lock()
	e.paused = paused
	e.mu.Unlock()
	e.changed()
	return nil
}

// TogglePause flips the pause state.
func (e *Engine) TogglePause(ctx context.Context) error {
	e.mu.Lock()
	e.paused = !e.paused
	e.mu.Unlock()
	e.changed()
	return nil
}

// SetVolume sets the volume, clamped to 0..100.
func (e *Engine) SetVolume(_ context.Context, v int) error {
	e.mu.Lock()
	e.volume = min(max(v, 0), 100)
	g := gain(e.volume)
	e.mu.Unlock()
	e.outMu.Lock()
	e.cfg.Output.SetGain(g)
	e.outMu.Unlock()
	e.changed()
	return nil
}

// SeekRelative moves the position by d. A seek that runs past the end plays the next track.
func (e *Engine) SeekRelative(_ context.Context, d time.Duration) error {
	e.mu.Lock()
	if e.index < 0 {
		e.mu.Unlock()
		return ErrNothingPlaying
	}
	c := e.cur
	if c == nil || c.total <= 0 {
		e.mu.Unlock()
		return ErrNotSeekable
	}
	target := durationToFrames(e.positionLocked(c) + d)
	if d < 0 && target < 0 {
		target = 0
	}
	if target >= c.total {
		e.advanceLocked(e.index+1, true)
		e.mu.Unlock()
		e.changed()
		return nil
	}
	e.mu.Unlock()
	return e.seekTo(c, max(target, 0))
}

// seekTo moves c to frame. The device is emptied at once and the position shows
// the target; the decoder catches up in the background, which can take a while
// on a long track that was not read that far yet. A later seek supersedes it.
func (e *Engine) seekTo(c *playing, frame int64) error {
	e.mu.Lock()
	if e.cur != c {
		e.mu.Unlock()
		return nil
	}
	g := c.seekG.Add(1)
	c.seeking = true
	e.flush = true
	c.pos.Store(frame)
	e.mu.Unlock()
	e.changed()
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.syncOutput() // empties the device and waits for a read in flight
		c.dmu.Lock()
		var err error
		if g == c.seekG.Load() && !c.closed {
			err = c.dec.SeekFrame(frame)
		}
		c.dmu.Unlock()
		e.mu.Lock()
		defer e.mu.Unlock()
		if g != c.seekG.Load() {
			return
		}
		c.seeking = false
		if e.cur != c {
			return
		}
		if err != nil {
			e.failLocked(c.track, err)
			e.advanceLocked(c.idx+1, true)
			return
		}
		go e.changed()
	}()
	return nil
}

// Close stops playback and the device. It does not wait on the network.
func (e *Engine) Close() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	close(e.done)
	cur, next := e.cur, e.next
	e.cur, e.next, e.index = nil, nil, -1
	e.retire(cur)
	e.retire(next)
	e.dropOpeningLocked()
	for _, ch := range e.subs {
		close(ch)
	}
	e.subs = nil
	e.mu.Unlock()
	e.outMu.Lock()
	e.cfg.Output.Pause()
	e.cfg.Output.Flush()
	err := e.cfg.Output.Close()
	e.outMu.Unlock()
	e.wg.Wait()
	return err
}

// ── levels ──────────────────────────────────────────────────────────────────

// levelStale is how long a reading stays current: the device pulls audio every few milliseconds while it plays, so no new
// reading for this long means nothing is playing (paused, stopped, loading).
const levelStale = 300 * time.Millisecond

// SetMeter starts or stops measuring the loudness of what is decoded. Off, the audio path does no extra work.
func (e *Engine) SetMeter(on bool) {
	e.meter.Store(on)
	if !on {
		e.lvlMu.Lock()
		e.lvlAt = time.Time{}
		e.lvlMu.Unlock()
	}
}

// Level is the loudness of the latest audio handed to the device (RMS and peak, 0 to 1, before the volume), and whether
// there is one: measuring is on and audio went out within levelStale.
func (e *Engine) Level() (rms, peak float64, ok bool) {
	if !e.meter.Load() {
		return 0, 0, false
	}
	e.lvlMu.Lock()
	defer e.lvlMu.Unlock()
	if e.lvlAt.IsZero() || time.Since(e.lvlAt) > levelStale {
		return 0, 0, false
	}
	return e.lvlRMS, e.lvlPk, true
}

func (e *Engine) noteLevel(samples []float32) {
	rms, peak := levelOf(samples)
	e.lvlMu.Lock()
	e.lvlRMS, e.lvlPk, e.lvlAt = rms, peak, time.Now()
	e.lvlMu.Unlock()
}

// levelOf is the RMS and the peak of interleaved samples, each clamped to 1.
func levelOf(samples []float32) (rms, peak float64) {
	if len(samples) == 0 {
		return 0, 0
	}
	var sum float64
	for _, s := range samples {
		v := math.Abs(float64(s))
		sum += v * v
		if v > peak {
			peak = v
		}
	}
	return math.Min(math.Sqrt(sum/float64(len(samples))), 1), math.Min(peak, 1)
}
