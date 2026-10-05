package native

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// ── fakes ──────────────────────────────────────────────────────────────────

// fakeDec is a track whose left channel is the frame number and whose right is the track's number.
type fakeDec struct {
	mu         sync.Mutex
	no         int
	frames     int64
	pos        int64
	closed     bool
	unseekable bool
	seekDelay  time.Duration
	seeks      []int64
	readGate   chan struct{} // when set, Read blocks until it is closed or the context is done
	ctx        context.Context
}

func (d *fakeDec) Read(p []float32) (int, error) {
	if d.ctx != nil && d.ctx.Err() != nil { // a real decoder reads the network with this context
		return 0, d.ctx.Err()
	}
	if d.readGate != nil {
		select {
		case <-d.readGate:
		case <-d.ctx.Done():
			return 0, d.ctx.Err()
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	n := min(int64(len(p)/2), d.frames-d.pos)
	if n <= 0 {
		return 0, io.EOF
	}
	for i := int64(0); i < n; i++ {
		p[2*i], p[2*i+1] = float32(d.pos+i), float32(d.no)
	}
	d.pos += n
	return int(n), nil
}

func (d *fakeDec) SeekFrame(f int64) error {
	time.Sleep(d.seekDelay)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seeks = append(d.seeks, f)
	d.pos = f
	return nil
}
func (d *fakeDec) Frames() int64 {
	if d.unseekable {
		return -1
	}
	return d.frames
}
func (d *fakeDec) Close() error   { d.mu.Lock(); d.closed = true; d.mu.Unlock(); return nil }
func (d *fakeDec) isClosed() bool { d.mu.Lock(); defer d.mu.Unlock(); return d.closed }

type fakeOpener struct {
	mu        sync.Mutex
	frames    map[string]int64 // by track ID; missing = fail
	fail      map[string]error
	opens     []string
	decs      map[string]*fakeDec
	gate      map[string]chan struct{} // Open blocks until closed
	readGate  map[string]chan struct{}
	seekDelay time.Duration
}

func newOpener(frames map[string]int64) *fakeOpener {
	return &fakeOpener{frames: frames, fail: map[string]error{}, decs: map[string]*fakeDec{}, gate: map[string]chan struct{}{}, readGate: map[string]chan struct{}{}}
}

func (o *fakeOpener) Open(ctx context.Context, t music.Track) (Decoded, error) {
	o.mu.Lock()
	o.opens = append(o.opens, t.ID)
	gate := o.gate[t.ID]
	o.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.fail[t.ID]; err != nil {
		return nil, err
	}
	n, ok := o.frames[t.ID]
	if !ok {
		return nil, errors.New("no such track")
	}
	no, _ := strconv.Atoi(t.ID[1:])
	d := &fakeDec{no: no, frames: n, readGate: o.readGate[t.ID], ctx: ctx, seekDelay: o.seekDelay}
	o.decs[t.ID] = d
	return d, nil
}

func (o *fakeOpener) openCount(id string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := 0
	for _, x := range o.opens {
		if x == id {
			n++
		}
	}
	return n
}
func (o *fakeOpener) dec(id string) *fakeDec { o.mu.Lock(); defer o.mu.Unlock(); return o.decs[id] }

type fakeOut struct {
	mu       sync.Mutex
	src      io.Reader
	running  bool
	gain     float64
	buffered int
	flushes  int
	closed   bool
}

func (f *fakeOut) Start(src io.Reader) error { f.src = src; return nil }
func (f *fakeOut) Resume()                   { f.mu.Lock(); f.running = true; f.mu.Unlock() }
func (f *fakeOut) Pause()                    { f.mu.Lock(); f.running = false; f.mu.Unlock() }
func (f *fakeOut) SetGain(g float64)         { f.mu.Lock(); f.gain = g; f.mu.Unlock() }
func (f *fakeOut) Buffered() int             { f.mu.Lock(); defer f.mu.Unlock(); return f.buffered }
func (f *fakeOut) Flush()                    { f.mu.Lock(); f.flushes++; f.buffered = 0; f.mu.Unlock() }
func (f *fakeOut) Close() error {
	f.mu.Lock()
	f.closed = true
	f.running = false
	f.mu.Unlock()
	return nil
}
func (f *fakeOut) isRunning() bool   { f.mu.Lock(); defer f.mu.Unlock(); return f.running }
func (f *fakeOut) getGain() float64  { f.mu.Lock(); defer f.mu.Unlock(); return f.gain }
func (f *fakeOut) setBuffered(n int) { f.mu.Lock(); f.buffered = n; f.mu.Unlock() }
func (f *fakeOut) nFlushes() int     { f.mu.Lock(); defer f.mu.Unlock(); return f.flushes }

// pull asks the source for n frames, as the device would, and returns left and right.
func (f *fakeOut) pull(t *testing.T, n int) (left, right []float32) {
	t.Helper()
	buf := make([]byte, n*8)
	got, err := io.ReadFull(f.src, buf)
	if err != nil || got != len(buf) {
		t.Fatalf("pull: %d bytes, %v", got, err)
	}
	for i := 0; i < n; i++ {
		left = append(left, math.Float32frombits(binary.LittleEndian.Uint32(buf[8*i:])))
		right = append(right, math.Float32frombits(binary.LittleEndian.Uint32(buf[8*i+4:])))
	}
	return
}

func tracks(n int) []music.Track {
	var out []music.Track
	for i := 1; i <= n; i++ {
		out = append(out, music.Track{ID: "t" + strconv.Itoa(i), Title: "Track " + strconv.Itoa(i)})
	}
	return out
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

type rig struct {
	e *Engine
	o *fakeOpener
	d *fakeOut
}

func newRig(t *testing.T, frames map[string]int64) *rig {
	t.Helper()
	o, d := newOpener(frames), &fakeOut{}
	e, err := NewEngine(EngineConfig{Opener: o, Output: d, Tick: 5 * time.Millisecond, OpenTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return &rig{e, o, d}
}

func (r *rig) playing(t *testing.T) {
	t.Helper()
	eventually(t, "a track to be playing", func() bool { return r.d.isRunning() && r.e.State().Duration > 0 })
}

var bg = context.Background()

// ── tests ──────────────────────────────────────────────────────────────────

func TestPlaysTheTrackFromTheStart(t *testing.T) {
	r := newRig(t, map[string]int64{"t1": 1000, "t2": 1000})
	if err := r.e.Replace(bg, tracks(2), 0); err != nil {
		t.Fatal(err)
	}
	r.playing(t)
	st := r.e.State()
	if st.Index != 0 || st.Track == nil || st.Track.ID != "t1" || len(st.Queue) != 2 || !st.Connected {
		t.Fatalf("state %+v", st)
	}
	if st.Duration != framesToDuration(1000) {
		t.Fatalf("duration %v", st.Duration)
	}
	l, rt := r.d.pull(t, 100)
	if l[0] != 0 || l[99] != 99 || rt[0] != 1 {
		t.Fatalf("first frames wrong: %v %v", l[:3], rt[:3])
	}
	// The position follows what the device took, less what it still holds.
	r.d.setBuffered(40 * 8)
	if got, want := r.e.State().Position, framesToDuration(60); got != want {
		t.Fatalf("position %v, want %v", got, want)
	}
}

func TestStartsAtTheChosenTrack(t *testing.T) {
	r := newRig(t, map[string]int64{"t1": 10, "t2": 10, "t3": 10})
	_ = r.e.Replace(bg, tracks(3), 2)
	r.playing(t)
	if st := r.e.State(); st.Index != 2 || st.Track.ID != "t3" {
		t.Fatalf("%+v", st)
	}
	_, rt := r.d.pull(t, 1)
	if rt[0] != 3 {
		t.Fatalf("played track %v", rt[0])
	}
	if err := r.e.Replace(bg, nil, 0); err == nil {
		t.Error("an empty queue must be refused")
	}
	if err := r.e.Replace(bg, tracks(3), 3); err == nil {
		t.Error("an index past the end must be refused")
	}
}

func TestGaplessAcrossTracksAndIdleAtTheEnd(t *testing.T) {
	r := newRig(t, map[string]int64{"t1": 100, "t2": 100})
	_ = r.e.Replace(bg, tracks(2), 0)
	r.playing(t)
	// Let the second track open in the background, as it does while the first plays.
	eventually(t, "the next track to be preloaded", func() bool { return r.o.openCount("t2") == 1 })
	time.Sleep(20 * time.Millisecond)
	l, rt := r.d.pull(t, 200)
	for i := 0; i < 100; i++ {
		if l[i] != float32(i) || rt[i] != 1 || l[100+i] != float32(i) || rt[100+i] != 2 {
			t.Fatalf("frame %d: left %v right %v / %v %v: not contiguous across the change", i, l[i], rt[i], l[100+i], rt[100+i])
		}
	}
	eventually(t, "track 2 to be current", func() bool { return r.e.State().Index == 1 })
	// Run off the end: the player goes idle and keeps the queue, like mpv.
	_ = r.d.src
	buf := make([]byte, 8*100)
	_, _ = r.d.src.Read(buf)
	eventually(t, "idle", func() bool { st := r.e.State(); return st.Index == -1 && st.Track == nil && !r.d.isRunning() })
	if n := len(r.e.State().Queue); n != 2 {
		t.Fatalf("queue lost: %d entries", n)
	}
	if r.o.openCount("t2") != 1 {
		t.Fatalf("t2 opened %d times", r.o.openCount("t2"))
	}
	eventually(t, "decoders closed", func() bool { return r.o.dec("t1").isClosed() && r.o.dec("t2").isClosed() })
}

func TestPauseAndResume(t *testing.T) {
	r := newRig(t, map[string]int64{"t1": 1000})
	_ = r.e.Replace(bg, tracks(1), 0)
	r.playing(t)
	_ = r.e.SetPaused(bg, true)
	eventually(t, "the device to pause", func() bool { return !r.d.isRunning() })
	if !r.e.State().Paused {
		t.Fatal("state not paused")
	}
	_ = r.e.TogglePause(bg)
	eventually(t, "the device to resume", r.d.isRunning)
	if r.e.State().Paused {
		t.Fatal("state still paused")
	}
	// Paused before anything loads: stays silent once it loads.
	_ = r.e.SetPaused(bg, true)
	_ = r.e.Replace(bg, tracks(1), 0)
	time.Sleep(50 * time.Millisecond)
	if r.d.isRunning() {
		t.Fatal("a paused player started the device")
	}
}

func TestNextPrevJumpAndTheirErrors(t *testing.T) {
	r := newRig(t, map[string]int64{"t1": 1 << 20, "t2": 1 << 20, "t3": 1 << 20})
	if err := r.e.Next(bg); !errors.Is(err, ErrNothingPlaying) {
		t.Fatalf("Next while idle: %v", err)
	}
	if err := r.e.Prev(bg); !errors.Is(err, ErrNothingPlaying) {
		t.Fatalf("Prev while idle: %v", err)
	}
	_ = r.e.Replace(bg, tracks(3), 0)
	r.playing(t)
	if err := r.e.Next(bg); err != nil {
		t.Fatal(err)
	}
	eventually(t, "track 2", func() bool { return r.e.State().Index == 1 && r.e.State().Duration > 0 })
	if r.d.nFlushes() == 0 {
		t.Error("a skip must empty the device")
	}
	// Prev early in a track goes back; later it restarts the track.
	if err := r.e.Prev(bg); err != nil {
		t.Fatal(err)
	}
	eventually(t, "track 1", func() bool { return r.e.State().Index == 0 && r.e.State().Duration > 0 })
	if err := r.e.Prev(bg); err != nil { // first track: restart
		t.Fatal(err)
	}
	if err := r.e.Jump(bg, 2); err != nil {
		t.Fatal(err)
	}
	eventually(t, "track 3", func() bool { return r.e.State().Index == 2 && r.e.State().Duration > 0 })
	if err := r.e.Next(bg); !errors.Is(err, ErrEndOfQueue) {
		t.Fatalf("Next on the last: %v", err)
	}
	if err := r.e.Jump(bg, 3); err == nil {
		t.Error("Jump past the end must fail")
	}
	// Past three seconds, Prev restarts instead of going back.
	r.d.pull(t, 1)
	r.d.setBuffered(0)
	r.e.mu.Lock()
	r.e.cur.pos.Store(durationToFrames(10 * time.Second))
	r.e.mu.Unlock()
	if err := r.e.Prev(bg); err != nil {
		t.Fatal(err)
	}
	eventually(t, "restart of track 3", func() bool { return r.o.dec("t3").seekedTo(0) })
	if r.e.State().Index != 2 {
		t.Fatalf("Prev past 3 s changed track: %d", r.e.State().Index)
	}
}

func (d *fakeDec) seekedTo(f int64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, s := range d.seeks {
		if s == f {
			return true
		}
	}
	return false
}

func TestNextUsesThePreloadedTrack(t *testing.T) {
	r := newRig(t, map[string]int64{"t1": 1 << 20, "t2": 1 << 20})
	_ = r.e.Replace(bg, tracks(2), 0)
	r.playing(t)
	eventually(t, "preload", func() bool { return r.o.openCount("t2") == 1 })
	_ = r.e.Next(bg)
	eventually(t, "track 2", func() bool { return r.e.State().Index == 1 && r.e.State().Duration > 0 })
	if n := r.o.openCount("t2"); n != 1 {
		t.Fatalf("t2 opened %d times, want the preload reused", n)
	}
}

func TestSeekRelative(t *testing.T) {
	r := newRig(t, map[string]int64{"t1": OutputRate * 100, "t2": 100})
	_ = r.e.Replace(bg, tracks(2), 0)
	r.playing(t)
	r.d.pull(t, OutputRate*10) // 10 s in
	flushes := r.d.nFlushes()
	if err := r.e.SeekRelative(bg, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the decoder to be sought", func() bool { return r.o.dec("t1").seekedTo(OutputRate * 15) })
	eventually(t, "playing again", r.d.isRunning)
	if r.d.nFlushes() <= flushes {
		t.Error("a seek must empty the device")
	}
	if p := r.e.State().Position; p != 15*time.Second {
		t.Fatalf("position after seek %v", p)
	}
	l, _ := r.d.pull(t, 1)
	if l[0] != float32(OutputRate*15) {
		t.Fatalf("first frame after seek %v", l[0])
	}
	// Back, clamped at the start.
	_ = r.e.SeekRelative(bg, -time.Hour)
	eventually(t, "seek to zero", func() bool { return r.o.dec("t1").seekedTo(0) })
	// Past the end plays the next track.
	_ = r.e.SeekRelative(bg, time.Hour)
	eventually(t, "track 2", func() bool { return r.e.State().Index == 1 })
}

func TestSeekIsNotPossibleWhileLoadingOrWithoutLength(t *testing.T) {
	r := newRig(t, map[string]int64{"t1": 1000, "t2": 1000})
	if err := r.e.SeekRelative(bg, time.Second); !errors.Is(err, ErrNothingPlaying) {
		t.Fatalf("idle: %v", err)
	}
	gate := make(chan struct{})
	r.o.gate["t1"] = gate
	_ = r.e.Replace(bg, tracks(1), 0)
	if err := r.e.SeekRelative(bg, time.Second); !errors.Is(err, ErrNotSeekable) {
		t.Fatalf("loading: %v", err)
	}
	st := r.e.State()
	if st.Track == nil || st.Position != 0 || st.Duration != 0 || st.Index != 0 {
		t.Fatalf("while loading the state is 0:00 / 0:00 with the track known, got %+v", st)
	}
	close(gate)
	r.playing(t)
}

func TestARapidSecondSeekWins(t *testing.T) {
	r := newRig(t, map[string]int64{"t1": OutputRate * 100})
	r.o.seekDelay = 60 * time.Millisecond
	_ = r.e.Replace(bg, tracks(1), 0)
	r.playing(t)
	_ = r.e.SeekRelative(bg, 10*time.Second)
	time.Sleep(10 * time.Millisecond)
	_ = r.e.SeekRelative(bg, 10*time.Second) // from the pending target
	eventually(t, "the last target", func() bool { return r.o.dec("t1").seekedTo(OutputRate * 20) })
	eventually(t, "playing", r.d.isRunning)
	if p := r.e.State().Position; p != 20*time.Second {
		t.Fatalf("position %v, want 20s", p)
	}
}

func TestFailedTrackIsSkippedAndReported(t *testing.T) {
	r := newRig(t, map[string]int64{"t2": 50})
	r.o.fail["t1"] = fmt.Errorf("resolve: %w", errors.New("dial tcp 203.0.113.9:443: refused"))
	_ = r.e.Replace(bg, tracks(2), 0)
	eventually(t, "track 2 to play", func() bool { return r.e.State().Index == 1 && r.d.isRunning() })
	msg := r.e.LastError()
	if msg == "" || !contains(msg, "Track 1") {
		t.Fatalf("LastError = %q", msg)
	}
	if contains(msg, "203.0.113.9") {
		t.Fatalf("address leaked: %q", msg)
	}
	// Every track failing ends idle, not in a loop.
	r2 := newRig(t, map[string]int64{})
	_ = r2.e.Replace(bg, tracks(3), 0)
	eventually(t, "idle after all failed", func() bool { return r2.e.State().Index == -1 && r2.e.LastError() != "" })
	if r2.d.isRunning() {
		t.Fatal("device running with nothing to play")
	}
	if n := len(r2.e.State().Queue); n != 3 {
		t.Fatalf("queue %d", n)
	}
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

func TestQueueEditing(t *testing.T) {
	r := newRig(t, map[string]int64{"t1": 1 << 20, "t2": 1 << 20, "t3": 1 << 20, "t4": 1 << 20})
	ids := func() string {
		s := ""
		for _, q := range r.e.State().Queue {
			s += q.ID + " "
		}
		return s
	}
	_ = r.e.Replace(bg, tracks(3), 1) // playing t2
	r.playing(t)
	_ = r.e.Enqueue(bg, music.Track{ID: "t4"})
	if ids() != "t1 t2 t3 t4 " || r.e.State().Index != 1 {
		t.Fatalf("after enqueue %s %d", ids(), r.e.State().Index)
	}
	// Move keeps the playing track playing, with its index following it.
	_ = r.e.Move(bg, 0, 3)
	if ids() != "t2 t3 t4 t1 " || r.e.State().Index != 0 {
		t.Fatalf("after move %s idx %d", ids(), r.e.State().Index)
	}
	_ = r.e.Move(bg, 3, 0)
	if ids() != "t1 t2 t3 t4 " || r.e.State().Index != 1 {
		t.Fatalf("after move back %s idx %d", ids(), r.e.State().Index)
	}
	_ = r.e.Move(bg, 1, 2)
	if ids() != "t1 t3 t2 t4 " || r.e.State().Index != 2 {
		t.Fatalf("after moving the playing one %s idx %d", ids(), r.e.State().Index)
	}
	if r.e.Move(bg, 0, 9) == nil || r.e.Remove(bg, 9) == nil {
		t.Error("out of range must fail")
	}
	// Removing before the playing track shifts the index; the track keeps playing.
	_ = r.e.Remove(bg, 0)
	if ids() != "t3 t2 t4 " || r.e.State().Index != 1 || r.e.State().Track.ID != "t2" {
		t.Fatalf("after remove before %s idx %d", ids(), r.e.State().Index)
	}
	if r.o.openCount("t2") != 1 {
		t.Errorf("t2 reopened %d times", r.o.openCount("t2"))
	}
	// Removing the playing one plays the entry that takes its place.
	_ = r.e.Remove(bg, 1)
	eventually(t, "t4", func() bool { st := r.e.State(); return st.Index == 1 && st.Track.ID == "t4" && st.Duration > 0 })
	// Removing the last playing entry goes idle with the rest kept.
	_ = r.e.Remove(bg, 1)
	eventually(t, "idle", func() bool { st := r.e.State(); return st.Index == -1 && st.Track == nil })
	if ids() != "t3 " {
		t.Fatalf("queue %s", ids())
	}
	// Enqueue while idle starts playing the new entry.
	_ = r.e.Enqueue(bg, music.Track{ID: "t1"})
	eventually(t, "t1 to play", func() bool { st := r.e.State(); return st.Index == 1 && st.Track.ID == "t1" && r.d.isRunning() })
}

func TestVolume(t *testing.T) {
	r := newRig(t, map[string]int64{})
	_ = r.e.SetVolume(bg, 50)
	if r.e.State().Volume != 50 || math.Abs(r.d.getGain()-0.125) > 1e-9 {
		t.Fatalf("volume %d gain %v (cubic like mpv: 0.5^3)", r.e.State().Volume, r.d.getGain())
	}
	_ = r.e.SetVolume(bg, 250)
	if r.e.State().Volume != 100 || r.d.getGain() != 1 {
		t.Fatal("not clamped high")
	}
	_ = r.e.SetVolume(bg, -5)
	if r.e.State().Volume != 0 || r.d.getGain() != 0 {
		t.Fatal("not clamped low")
	}
}

func TestSubscribeDeliversCurrentThenChangesAndClosesOnClose(t *testing.T) {
	r := newRig(t, map[string]int64{"t1": 1 << 20})
	ch := r.e.Subscribe()
	first := <-ch
	if first.Index != -1 || !first.Connected {
		t.Fatalf("first %+v", first)
	}
	_ = r.e.Replace(bg, tracks(1), 0)
	deadline := time.After(2 * time.Second)
	for {
		select {
		case st := <-ch:
			if st.Index == 0 && st.Duration > 0 {
				goto closed
			}
		case <-deadline:
			t.Fatal("no state with a loaded track")
		}
	}
closed:
	_ = r.e.Close()
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-time.After(2 * time.Second):
			t.Fatal("subscription not closed")
		}
	}
}

func TestPositionPublishedWhilePlaying(t *testing.T) {
	r := newRig(t, map[string]int64{"t1": OutputRate * 60})
	ch := r.e.Subscribe()
	_ = r.e.Replace(bg, tracks(1), 0)
	r.playing(t)
	r.d.pull(t, OutputRate)
	deadline := time.After(2 * time.Second)
	for {
		select {
		case st := <-ch:
			if st.Position >= time.Second {
				return
			}
		case <-deadline:
			t.Fatal("position never advanced in published state")
		}
	}
}

func TestCloseDoesNotWaitOnTheNetwork(t *testing.T) {
	r := newRig(t, map[string]int64{"t1": 1000, "t2": 1000})
	r.o.gate["t1"] = make(chan struct{}) // never opens
	_ = r.e.Replace(bg, tracks(2), 0)
	time.Sleep(20 * time.Millisecond)
	done := make(chan struct{})
	go func() { _ = r.e.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close hung on a track that was still opening")
	}
	if err := r.e.Replace(bg, tracks(1), 0); err == nil {
		t.Error("Replace after Close must fail")
	}
	if !r.d.closed {
		t.Error("device not closed")
	}
}

func TestJumpWhileAReadBlocksOnTheNetwork(t *testing.T) {
	r := newRig(t, map[string]int64{"t1": 1 << 20, "t2": 1 << 20})
	r.o.readGate["t1"] = make(chan struct{}) // t1's reads stall (a slow network)
	_ = r.e.Replace(bg, tracks(2), 0)
	r.playing(t)
	go func() { // the device is stuck inside a read
		buf := make([]byte, 800)
		_, _ = r.d.src.Read(buf)
	}()
	time.Sleep(20 * time.Millisecond)
	_ = r.e.Jump(bg, 1)
	eventually(t, "track 2 to play despite the stalled read", func() bool {
		st := r.e.State()
		return st.Index == 1 && st.Duration > 0 && r.d.isRunning()
	})
	eventually(t, "the stalled decoder to be closed", func() bool { return r.o.dec("t1").isClosed() })
}

func TestTheDecoderContextOutlivesTheOpen(t *testing.T) {
	// The context handed to Open is the one the stream is read with: it must not
	// be cancelled when Open returns, or every real track would end at once.
	r := newRig(t, map[string]int64{"t1": 1 << 20})
	_ = r.e.Replace(bg, tracks(1), 0)
	r.playing(t)
	time.Sleep(50 * time.Millisecond)
	l, _ := r.d.pull(t, 100)
	if l[0] != 0 || l[99] != 99 {
		t.Fatalf("frames %v", l[:3])
	}
	if r.e.State().Index != 0 {
		t.Fatal("the track ended")
	}
}

func TestSlowOpenTimesOutAndSkips(t *testing.T) {
	o, d := newOpener(map[string]int64{"t2": 100}), &fakeOut{}
	e, err := NewEngine(EngineConfig{Opener: o, Output: d, Tick: 5 * time.Millisecond, OpenTimeout: 80 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	o.gate["t1"] = make(chan struct{}) // never opens
	_ = e.Replace(bg, tracks(2), 0)
	eventually(t, "track 2 after the timeout", func() bool { return e.State().Index == 1 && d.isRunning() })
	if msg := e.LastError(); !contains(msg, "longer than") {
		t.Fatalf("LastError = %q", msg)
	}
}

// A queue edit made while the playing entry is still opening must move that
// entry's position too: when it ends, the entry after it in the edited queue plays.
func TestQueueEditWhileTheTrackOpensKeepsItsPlace(t *testing.T) {
	cases := []struct {
		name string
		edit func(e *Engine) error
		next string // what plays after t2 ends
		idx  int    // its queue position
	}{
		{"remove before it", func(e *Engine) error { return e.Remove(bg, 0) }, "t3", 1},
		{"move it to the front", func(e *Engine) error { return e.Move(bg, 1, 0) }, "t1", 1},
		{"move one from before it to after it", func(e *Engine) error { return e.Move(bg, 0, 2) }, "t3", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t, map[string]int64{"t1": 1 << 20, "t2": 10, "t3": 1 << 20})
			gate := make(chan struct{})
			r.o.gate["t2"] = gate
			_ = r.e.Replace(bg, tracks(3), 1) // t2, still opening
			if err := c.edit(r.e); err != nil {
				t.Fatal(err)
			}
			close(gate)
			r.playing(t)
			if st := r.e.State(); st.Track == nil || st.Track.ID != "t2" {
				t.Fatalf("playing %+v, want t2", st.Track)
			}
			eventually(t, "the next track to be preloaded", func() bool { return r.o.openCount(c.next) >= 1 })
			_, _ = r.d.pull(t, 20) // t2 ends inside this pull
			eventually(t, c.next+" to play after t2", func() bool {
				st := r.e.State()
				return st.Index == c.idx && st.Track != nil && st.Track.ID == c.next
			})
		})
	}
}
