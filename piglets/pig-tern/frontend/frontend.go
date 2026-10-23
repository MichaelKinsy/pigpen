// Package frontend draws PiG's interactive mode in Tern through the Tern
// Surface Protocol (TSP), with Stencil's Go SDK (package tern). It is a
// Piglet frontend member: the Piglet Binary fuses it with slots.frontend,
// and PiG falls back to its ANSI renderer whenever this member declines or
// fails.
//
// The SDK speaks the wire: the hello handshake, framing and chunking, input
// parsing, credit flow control and the routing of events to node handlers.
// PiG keeps the terminal, so the SDK session reads the TSP input that
// HandleInput claims from a pipe and writes through PiG's output. The
// transcript (main) is kept incremental: PiG's ops on it become raw frame
// ops sent with Surface.Send, so a streamed token costs one small op however
// long the transcript is. The dock and the layer are small, so their
// structure is rendered whole with Surface.Render from tern/ui builders
// whose handlers turn Tern's events into PiG's Env actions, and only when
// it changes; the props that change while it stays (the editor's text, a
// picker's rows, the dock lines, the spinner's label, the footer's values)
// travel as raw ops on the rendered nodes, like the transcript's.
package frontend

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/stencil-hq/tern-sdk/go/tern"
)

// helloTimeout is how long Tern has to answer the hello once PiG reads
// input. Answers reach HandleInput only from PiG's input loop on, so the
// SDK's own Connect timeout, which runs from the hello, is not used.
const helloTimeout = time.Second

// connectWait bounds Connect itself; the hello timeout ends it sooner.
const connectWait = 24 * time.Hour

const surfaceID = "pig"

// Tern lays an inline surface out in CSS pixels: the regions sit inside
// 16px of padding on each side, and the Reader chat style, the narrowest
// built-in skin, centers main's children on a 720px measure. Spine and
// Console are wider, so lines sized for Reader fit every skin.
const (
	regionPaddingPx = 16
	mainMeasurePx   = 720
	// defaultCellPx is the cell width until Tern reports one.
	defaultCellPx = 8
)

// Frontend is the member's factory, the symbol the Piglet build registers.
func Frontend() fe.Frontend { return member{} }

type member struct{}

// Open starts a session only when the process runs directly in a Tern pane:
// a multiplexer between them swallows TSP, and PIG_TUI_NATIVE=0 (or the
// SDK's TERN_TSP=0) opts out. The hello goes out from a goroutine, so Open
// never waits for Tern.
func (member) Open(env fe.Env) (fe.Session, error) {
	if env.Getenv("PIG_TUI_NATIVE") == "0" || env.Getenv("TERN_TSP") == "0" || env.Getenv("TERM_PROGRAM") != "tern" {
		return nil, nil
	}
	for _, key := range []string{"TMUX", "STY", "ZELLIJ", "HERDR_ENV"} {
		if env.Getenv(key) != "" {
			return nil, nil
		}
	}
	s := newSession(env)
	s.start()
	return s, nil
}

type afterFunc func(time.Duration, func()) *time.Timer

// session keeps the surface document in step with PiG's frames. PiG calls
// its fe.Session methods on its owner loop; the SDK's goroutines run the
// node handlers and write ack-released frames. mu guards the state below it
// and is taken before the SDK's own lock, never while the SDK holds it.
type session struct {
	env fe.Env
	out *termWriter
	in  *inputPipe

	ctx         context.Context
	cancel      context.CancelFunc
	connectDone chan struct{}
	afterFunc   afterFunc
	keepalive   time.Duration
	unhandled   func(tern.Input)

	mu    sync.Mutex
	timer *time.Timer
	tsp   *tern.Session
	sf    *tern.Surface
	// fellBack reports that PiG was asked to fall back, and closed that
	// Close ran.
	fellBack, closed bool
	// inputReady reports that PiG's input loop runs, and live that a frame
	// went out (or waits for the connection) since. Tern acknowledges no
	// frame while the pane is hidden, so nothing goes out before PiG's first
	// frame after InputReady, which makes the editor sendable: the startup
	// then fits the two credits, and an agent started in a background tab
	// gets its prompt.
	inputReady, live bool

	// The transcript: main's TSP ids in order, the entry last sent for each
	// PiG id, and the raw ops (the transcript's and the dock's hot props)
	// not yet handed to the SDK.
	order   []string
	sent    map[string]*mainEntry
	pending []tern.Op
	// sessionNode reports that the node naming the session file was added.
	sessionNode bool

	// The dock and the overlays, in PiG's order, rendered whole; dirty
	// reports a change not rendered yet, and renders counts the renders.
	dock, overlays []*entry
	dirty          bool
	renders        int
	// editor is the editor node shown, if hasEditor.
	editor    fe.Editor
	hasEditor bool

	// What HandleInput read from Tern: the pane's width in cells and a
	// cell's width in CSS pixels, the credits, the highest acknowledged
	// frame, and whether the DA1 answer the hello asked for is still owed.
	cols    int
	cellPx  float64
	credits int64
	acked   int64
	owedDA1 bool

	// progressStop and progressDone run the terminal progress keepalive
	// while a Working node shows.
	progressStop, progressDone chan struct{}
}

func newSession(env fe.Env) *session {
	ctx, cancel := context.WithCancel(context.Background())
	return &session{
		env:         env,
		out:         &termWriter{w: env.Out},
		in:          newInputPipe(),
		ctx:         ctx,
		cancel:      cancel,
		connectDone: make(chan struct{}),
		afterFunc:   time.AfterFunc,
		keepalive:   progressKeepalive,
		sent:        map[string]*mainEntry{},
		dirty:       true,
		cols:        env.Columns,
		cellPx:      defaultCellPx,
		credits:     tern.DefaultCredits,
	}
}

// start connects in the background: the SDK writes the hello (and a DA1
// request) through PiG's output and reads Tern's answers from the pipe that
// HandleInput feeds. PiG's terminal is already raw.
func (s *session) start() {
	opts := tern.Options{
		App:        "pig",
		Version:    s.env.Version,
		Features:   []string{tern.FeatureEdit, tern.FeatureUndo, tern.FeatureSend},
		Timeout:    connectWait,
		In:         s.in,
		Out:        s.out,
		MakeRaw:    func() (func() error, error) { return func() error { return nil }, nil },
		CancelRead: s.in.Close,
		NoPaste:    true,
		NoKitty:    true,
	}
	go s.connect(opts)
}

// noHelloReason is the fallback reason when Tern does not speak TSP to the
// member. Tern answers no hello while its "Native program surfaces" setting
// (native_surfaces) is off, the usual cause in a plain Tern pane.
const noHelloReason = `Tern did not answer the surface protocol hello. Turn on "Native program surfaces" in Tern's settings to draw PiG natively.`

func (s *session) connect(opts tern.Options) {
	defer close(s.connectDone)
	tsp, err := tern.Connect(s.ctx, opts)
	if err != nil {
		// After Close, fallback does nothing: only the hello timeout and an
		// answer that is not TSP fall back. Tern stays silent when its
		// "Native program surfaces" setting is off, so the reason names it.
		s.fallback(noHelloReason)
		return
	}
	sf, err := tsp.Open(tern.SurfaceOptions{ID: surfaceID, Mode: tern.Inline, Title: "pig", Role: "pig.session"})
	s.mu.Lock()
	if err != nil || s.closed || s.fellBack {
		s.mu.Unlock()
		_ = tsp.Close()
		if err != nil {
			s.fallback("Tern refused the surface")
		}
		return
	}
	s.tsp, s.sf = tsp, sf
	flushErr := s.flushLocked()
	s.mu.Unlock()
	if flushErr != nil {
		s.fallback("writing the surface failed")
	}
	go s.run(tsp)
}

// run reads the events no node handler took: the surface going away ends
// the session. Node handlers run here too, inside Session.Run, in input
// order; unhandled sees what no handler took (tests wait on it).
func (s *session) run(tsp *tern.Session) {
	err := tsp.Run(context.Background(), func(in tern.Input) error {
		if gone, ok := in.(*tern.Gone); ok && slices.Contains(gone.IDs, surfaceID) {
			s.fallback("Tern closed the surface")
		}
		if s.unhandled != nil {
			s.unhandled(in)
		}
		return nil
	})
	if err != nil && !errors.Is(err, tern.ErrClosed) && !errors.Is(err, io.EOF) {
		s.fallback("the surface protocol session failed: " + err.Error())
	}
}

// fallback asks PiG, once, to continue with its ANSI renderer.
func (s *session) fallback(reason string) {
	s.mu.Lock()
	first := !s.fellBack && !s.closed
	s.fellBack = true
	s.mu.Unlock()
	if first && s.env.Fallback != nil {
		s.env.Fallback(reason)
	}
}

// InputReady starts the hello timeout: Tern answers within it once PiG reads
// input, and a reply that arrived during startup is already taken. It sends
// nothing: PiG's next frame carries the folded startup changes.
func (s *session) InputReady() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inputReady = true
	if s.tsp != nil || s.timer != nil {
		return
	}
	s.timer = s.afterFunc(helloTimeout, s.cancel)
}

// Apply converts PiG's ops: main's into raw ops, the dock's and the
// overlays' into the next rendered view, and a session file into the node
// that names it. Then it sends what it may.
func (s *session) Apply(frame fe.Frame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if frame.SessionFile != nil {
		s.publishSessionFile(*frame.SessionFile)
	}
	for _, op := range frame.Ops {
		switch op.Region {
		case fe.RegionMain:
			s.convertMain(op)
		case fe.RegionOverlay:
			s.applyOverlay(op)
		default:
			s.applyDock(op)
		}
	}
	if s.inputReady {
		s.live = true
	}
	return s.flushLocked()
}

// flushLocked renders the dock and the layer when they changed and hands the
// transcript's ops to the SDK. A render the credits hold back waits in the
// SDK, which then sends the latest view's difference; the transcript's ops
// wait here instead while every credit is in use, folding as they come (the
// SDK's queue keeps every op). Render goes first, so ops on a node it adds
// follow it, in its frame or a later one.
func (s *session) flushLocked() error {
	if s.sf == nil || !s.live || s.fellBack || s.closed {
		return nil
	}
	if s.dirty {
		if err := s.render(s.sf); err != nil {
			return s.sendErr(err)
		}
	}
	if len(s.pending) == 0 || s.out.frames.Load()-s.acked >= s.credits {
		return nil
	}
	err := s.sendErr(s.sf.Send(s.pending...))
	// The SDK copied the ops; the slice serves the next frame.
	clear(s.pending)
	s.pending = s.pending[:0]
	return err
}

// render renders the view of the dock and the layer. The SDK sends that
// view's difference, at once or when a credit returns, before any raw op
// handed to it later, so later changes of the nodes' props go raw.
func (s *session) render(sf *tern.Surface) error {
	s.dirty = false
	s.renders++
	for _, e := range s.dock {
		e.unsent = false
	}
	for _, e := range s.overlays {
		e.unsent = false
	}
	return sf.Render(s.view())
}

// sendErr drops the error of a surface Tern already closed, which falls
// back on its own.
func (s *session) sendErr(err error) error {
	if errors.Is(err, tern.ErrClosed) {
		return nil
	}
	return err
}

// HandleInput takes Tern's replies and events, in APC framing or, on
// Windows, OSC 877 framing, and the DA1 answer the SDK's hello asked for;
// the SDK's parser tells them from keys. It keeps the widths, credits and
// acks it reads, then passes the sequence to the SDK session.
func (s *session) HandleInput(data string) bool {
	input := []byte(data)
	var parser tern.Parser
	items := append(parser.Feed(input), parser.Flush()...)
	if len(items) == 0 {
		// A TSP message the parser dropped (malformed or oversized) is
		// still Tern's.
		return bytes.HasPrefix(input, []byte("\x1b_tsp;")) || bytes.HasPrefix(input, []byte("\x1b]877;tsp;"))
	}
	s.mu.Lock()
	acked := false
	for _, item := range items {
		switch {
		case item.Keys != nil:
			s.mu.Unlock()
			return false
		case item.DA1 && !s.owedDA1:
			// PiG's own queries (keyboard protocol, theme colors) end with
			// DA1 too; only the one after the hello reply is the SDK's.
			s.mu.Unlock()
			return false
		case item.DA1:
			s.owedDA1 = false
		case item.Reply != nil && item.Reply.Hello != nil:
			hello := item.Reply.Hello
			s.owedDA1 = true
			s.setWidth(hello.Cols, hello.Cell.W)
			if hello.Credits > 0 {
				s.credits = int64(hello.Credits)
			}
		case item.Event != nil:
			switch ev := item.Event.(type) {
			case *tern.Resize:
				s.setWidth(ev.Cols, ev.Cell.W)
			case *tern.Ack:
				if ev.SF == surfaceID && ev.S > s.acked {
					s.acked = ev.S
					acked = true
				}
			}
		}
	}
	s.in.Write(input)
	if acked {
		// The SDK takes the ack on its own goroutine; ops handed over before
		// it does wait in the SDK's queue for the credit it returns.
		_ = s.flushLocked()
	}
	s.mu.Unlock()
	return true
}

func (s *session) setWidth(cols int, cellPx float64) {
	if cols > 0 {
		s.cols = cols
	}
	if cellPx > 0 {
		s.setCellWidth(cellPx)
	}
}

// Columns sizes PiG's lines for Tern's layout: the dock spans the pane inside
// the region padding, and main fits the Reader measure.
func (s *session) Columns() (main, dock int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cols <= 0 {
		return 0, 0
	}
	padding := int(math.Ceil(2 * regionPaddingPx / s.cellPx))
	dock = max(1, s.cols-padding)
	return min(dock, int(mainMeasurePx/s.cellPx)), dock
}

// Close closes the surface. After a fallback it removes the surface, so the
// ANSI renderer's grid shows; otherwise what is pending goes out, past the
// credits, and the transcript stays until the shell's next prompt. The
// surface's last messages are written before Close returns. The SDK
// session then closes in the background: its Close drains input for
// tern.DrainDelay, but its input is the pipe HandleInput feeds, and PiG
// drains the terminal itself after Close, so waiting would only delay
// PiG's exit.
func (s *session) Close() error {
	s.mu.Lock()
	if s.timer != nil {
		s.timer.Stop()
	}
	s.closed = true
	s.mu.Unlock()
	s.setProgress(false)
	s.cancel()
	<-s.connectDone

	s.mu.Lock()
	tsp, sf, fellBack := s.tsp, s.sf, s.fellBack
	var err error
	if sf != nil && !fellBack && s.live {
		if s.dirty {
			err = s.sendErr(s.render(sf))
		}
		if len(s.pending) > 0 {
			err = errors.Join(err, s.sendErr(sf.Send(s.pending...)))
			s.pending = nil
		}
	}
	s.mu.Unlock()
	if tsp == nil {
		return err
	}
	if fellBack {
		err = errors.Join(err, sf.Remove())
	} else {
		err = errors.Join(err, sf.Close())
	}
	// With the surface closed and no terminal modes set (NoPaste, NoKitty,
	// a no-op MakeRaw), the session's Close writes nothing more.
	go tsp.Close()
	return err
}

// termWriter is PiG's terminal output, shared by the SDK session and the
// terminal progress keepalive: every SDK message is one Write, so a lock
// per Write keeps them whole. It counts the frames written, which the
// transcript's folding compares with Tern's acks.
type termWriter struct {
	mu     sync.Mutex
	w      io.Writer
	frames atomic.Int64
}

var frameOpener = []byte("\x1b_tsp;f;")

func (t *termWriter) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	n, err := t.w.Write(p)
	if err == nil && bytes.HasPrefix(p, frameOpener) {
		t.frames.Add(1)
	}
	return n, err
}

// inputPipe hands the TSP input that HandleInput claims to the SDK session's
// reader. Writes never block PiG's owner loop.
type inputPipe struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    []byte
	closed bool
}

func newInputPipe() *inputPipe {
	p := &inputPipe{}
	p.cond = sync.NewCond(&p.mu)
	return p
}

func (p *inputPipe) Write(data []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.buf = append(p.buf, data...)
	p.cond.Signal()
}

func (p *inputPipe) Read(out []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for len(p.buf) == 0 && !p.closed {
		p.cond.Wait()
	}
	if len(p.buf) == 0 {
		return 0, io.EOF
	}
	n := copy(out, p.buf)
	p.buf = p.buf[n:]
	if len(p.buf) == 0 {
		p.buf = nil
	}
	return n, nil
}

// Close ends the reader, as the SDK's CancelRead.
func (p *inputPipe) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.cond.Broadcast()
	return nil
}
