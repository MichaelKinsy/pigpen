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
// travel as raw ops on the rendered nodes, like the transcript's. An
// extension surface that carries its components' view (D103) draws each
// component as a Tern element (view.go), its lines wherever one is not.
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

// defaultCellHeightPx is a cell's height until Tern reports one; the screen
// is sized with it.
const defaultCellHeightPx = 16

// PiG pads and wraps its lines to the columns Columns gives it, and Tern's
// ansi node wraps at its block's width, so those columns must fit the
// block. Tern says the pane's width in columns (hello and resize cols): the
// width main and dock share, in the terminal's face
// (https://docs.stencil.so/tern/protocol/handshake.md, surfaces.md
// "Aside"). An ansi block spanning the dock fits exactly cols in every
// geometry measured (Tern 0.4.5: panes 480 to 1400px, fonts 11 to 20px, the
// four layouts, every style and chat style). Its cell is not hello's
// cell.w, which is the face's advance rounded to whole pixels (13px: 8 for
// 7.76), so the member counts in Tern's columns, not in pixels.
//
// In main, Tern's chat style narrows the block further. It is a setting
// the member cannot see (Reader, the default, Spine or Console), so main
// takes the narrowest. The styles' lengths are in Tern's 13px design units
// (https://docs.stencil.so/tern/styles/variables.md#surface-grid-metrics:
// a region's CSS zoom is the font size ÷ 13), and a column is the face's
// advance, so a design px is 1/(13 × advance in em) columns whatever the
// font size; the advance depends on the face, also unseen. Tern 0.4.5's
// omp-chat-*.css, which the measurements match to the pixel:
//   - Reader: children at most 720px, inside 16px of padding on each side.
//   - Spine: main at most 920px, padded by 4% of the pane's region width
//     kept within 16px to 40px on each side, children 40px right of that.
//   - Console: main at most 1040px, a gutter of 3 cells.
//
// Reader's measure is the narrowest on wide panes and Spine's padding and
// indent on the others; Reader's padding and Console's gutter and maximum
// leave wider blocks at every width.
const (
	designFontPx = 13.0
	// The advance of monospace faces, in em: 0.5 (Iosevka, PragmataPro,
	// Ubuntu Mono) to 0.625 (Monaspace Neon, measured in Tern); Tern's own
	// SF Mono is 0.597. Paddings count with the narrowest, which makes them
	// the most columns, and maximum widths with the widest.
	narrowestAdvance = 0.5
	widestAdvance    = 0.625
	builtinAdvance   = 0.597

	readerMeasurePx = 720
	readerPaddingPx = 16
	spineMaxPx      = 920
	spinePadShare   = 0.04
	spinePadMinPx   = 16
	spinePadMaxPx   = 40
	spineIndentPx   = 40

	// columnMargin keeps lines a column short of the bound, against
	// subpixel rounding the measurements did not reach.
	columnMargin = 1
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
	// not yet handed to the SDK; queued counts the ops queued there.
	order   []string
	sent    map[string]*mainEntry
	pending []tern.Op
	queued  int
	// sessionNode reports that the node naming the session file was added,
	// and sessionFile is the file it names.
	sessionNode bool
	sessionFile string

	// theme is the palette of PiG's theme, and paletteSent reports that the
	// surface has it.
	theme       *fe.Theme
	paletteSent bool
	// mark is PiG's mark (mark.go), markDirty reports that the surface's
	// sheet does not draw it yet, and markInstalled that the surface has a
	// mark sheet. overlaySheetSent reports that the surface has the sheet
	// that fits a full overlay (overlaySheet).
	mark                     fe.Mark
	markDirty, markInstalled bool
	overlaySheetSent         bool
	// The views (view.go): themeTokens maps the theme's colors to their
	// tokens; viewRules are the themed views' rules by their root's TSP id,
	// viewsShown reports that a view was drawn, viewSheetSent that the
	// surface has the views' sheet, viewSheetText the sheet it has, and
	// viewSheetDirty that the rules changed since. blobQueue are images to
	// send, blobsSent the ones sent.
	themeTokens                               map[string]string
	viewRules                                 map[string]viewRule
	viewsShown, viewSheetSent, viewSheetDirty bool
	viewSheetText                             string
	blobQueue                                 []*fe.ViewImage
	blobsSent                                 map[string]bool

	// The terminal handoff (handoff.go): suspended reports that PiG lent
	// the terminal out, lent that the surface was closed for it, and
	// adopting that the surface was adopted back and Tern has sent nothing
	// on it since.
	suspended, lent, adopting bool

	// The dock and the overlays, in PiG's order, rendered whole; dirty
	// reports a change not rendered yet, renders counts the renders, and
	// renderedSendable is the editor's sendable in the last render.
	dock, overlays   []*entry
	dirty            bool
	renders          int
	renderedSendable bool
	// editor is the editor node shown, if hasEditor.
	editor    fe.Editor
	hasEditor bool

	// What HandleInput read from Tern: the pane's width in cells and a
	// cell's height in CSS pixels, the credits, the highest acknowledged
	// frame, whether the DA1 answer the hello asked for is still owed, and
	// whether the pane is hidden (another tab shows). Frames are counted
	// from the session's first; frameBase is the count before the surface's
	// first frame, whose acks count from 1.
	cols         int
	cellHeightPx float64
	credits      int64
	acked        int64
	frameBase    int64
	owedDA1      bool
	hidden       bool
	// reduceMotion reports that the user asked Tern for reduced motion, and
	// screen is the fullscreen overlay shown (screen.go), or nil.
	reduceMotion bool
	screen       *screen

	// progress reports that a Working node shows, and progressStop and
	// progressDone run the terminal progress keepalive meanwhile.
	progress                   bool
	progressStop, progressDone chan struct{}
}

func newSession(env fe.Env) *session {
	ctx, cancel := context.WithCancel(context.Background())
	return &session{
		env:          env,
		out:          &termWriter{w: env.Out},
		in:           newInputPipe(),
		ctx:          ctx,
		cancel:       cancel,
		connectDone:  make(chan struct{}),
		afterFunc:    time.AfterFunc,
		keepalive:    progressKeepalive,
		sent:         map[string]*mainEntry{},
		dirty:        true,
		cols:         env.Columns,
		cellHeightPx: defaultCellHeightPx,
		credits:      tern.DefaultCredits,
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
	s.mu.Lock()
	if s.closed || s.fellBack {
		s.mu.Unlock()
		_ = tsp.Close()
		return
	}
	s.tsp = tsp
	// A surface opened while PiG lends the terminal out would cover the
	// other program; Resume opens it instead.
	var openErr error
	if !s.suspended {
		openErr = s.openLocked()
	}
	if errors.Is(openErr, errRefused) {
		s.tsp = nil
	}
	s.mu.Unlock()
	if errors.Is(openErr, errRefused) {
		_ = tsp.Close()
		s.fallback(openErr.Error())
		return
	}
	if openErr != nil {
		s.fallback(openErr.Error())
	}
	go s.run(tsp)
}

// The fallback reasons of openLocked.
var (
	errRefused    = errors.New("Tern refused the surface")
	errFirstFlush = errors.New("writing the surface failed")
)

// openLocked opens the surface and sends what is pending.
func (s *session) openLocked() error {
	sf, err := s.tsp.Open(tern.SurfaceOptions{ID: surfaceID, Mode: tern.Inline, Title: "pig", Role: "pig.session"})
	if err != nil {
		return errRefused
	}
	s.sf = sf
	if s.flushLocked() != nil {
		return errFirstFlush
	}
	return nil
}

// run reads the events no node handler took: the surface going away ends
// the session. Node handlers run here too, inside Session.Run, in input
// order; unhandled sees what no handler took (tests wait on it).
func (s *session) run(tsp *tern.Session) {
	err := tsp.Run(context.Background(), func(in tern.Input) error {
		if gone, ok := in.(*tern.Gone); ok && slices.Contains(gone.IDs, surfaceID) && !s.reopen() {
			s.fallback("Tern closed the surface")
		}
		if action, ok := in.(*tern.Action); ok {
			s.transcriptClick(action)
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
// overlays' into the next rendered view, a session file into the node that
// names it, and a changed mark into its stylesheet. Then it sends what it
// may.
func (s *session) Apply(frame fe.Frame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if frame.SessionFile != nil {
		s.sessionFile = *frame.SessionFile
		s.publishSessionFile(s.sessionFile)
	}
	if frame.Theme != nil {
		theme := *frame.Theme
		s.theme, s.paletteSent, s.themeTokens = &theme, false, nil
		// The views' rules read on the new palette's tints, and views take
		// their spans' tokens from the theme's colors.
		s.rethemeViewRules()
		for _, e := range slices.Concat(s.dock, s.overlays) {
			if hasView(e.node) {
				s.rebuild(e, e.node, true)
			}
		}
	}
	if frame.Mark != nil && !sameMark(*frame.Mark, s.mark) {
		s.mark, s.markDirty = *frame.Mark, true
	}
	for _, op := range frame.Ops {
		switch op.Region {
		case fe.RegionMain:
			s.convertMain(op)
		case fe.RegionOverlay:
			if s.isScreenOp(op) {
				s.applyScreen(op)
			} else {
				s.applyOverlay(op)
			}
		default:
			s.applyDock(op)
		}
	}
	if s.inputReady {
		s.live = true
	}
	return s.flushLocked()
}

// flushLocked sends PiG's palette and the mark's stylesheet when they
// changed, renders the dock and the layer when they changed (the overlay
// sheet first, once a surface's layer holds an overlay), and hands the
// transcript's ops to the SDK. A render the credits hold back waits in the
// SDK, which then sends the latest view's difference; the transcript's ops
// wait here instead while every credit is in use, folding as they come (the
// SDK's queue keeps every op). Render goes first, so ops on a node it adds
// follow it, in its frame or a later one. While PiG lends the terminal out,
// nothing goes out.
//
// While the pane is hidden, Tern draws nothing and acknowledges nothing, so
// the raw ops (streamed text, the footer's values) and the renders wait
// here until it shows again, and go out then: a frame for the latest view
// and one for the folded ops. Only a render that turns the editor sendable
// or not goes out at once, while a credit is free: a prompt that Tern or
// Carly sends to the hidden pane waits for sendable, and goes to the editor
// only while it is.
func (s *session) flushLocked() error {
	if s.sf == nil || !s.live || s.fellBack || s.closed || s.suspended {
		return nil
	}
	if !s.paletteSent && s.theme != nil {
		if err := s.sendErr(s.sf.Palette(palette(*s.theme))); err != nil {
			return err
		}
		s.paletteSent = true
	}
	if s.markDirty {
		if css := markSheet(s.mark); css != "" || s.markInstalled {
			if err := s.sendErr(s.sf.Stylesheet(markSheetName, css)); err != nil {
				return err
			}
			s.markInstalled = css != ""
		}
		s.markDirty = false
	}
	if err := s.sendErr(s.flushBlobs()); err != nil {
		return err
	}
	if err := s.sendErr(s.flushViewSheet()); err != nil {
		return err
	}
	if s.dirty && (!s.hidden || s.sendable() != s.renderedSendable) {
		if len(s.overlays) > 0 && !s.overlaySheetSent {
			if err := s.sendErr(s.sf.Stylesheet(overlaySheetName, overlaySheet)); err != nil {
				return err
			}
			s.overlaySheetSent = true
		}
		if err := s.render(s.sf); err != nil {
			return s.sendErr(err)
		}
	}
	if err := s.sendErr(s.flushScreen()); err != nil {
		return err
	}
	if len(s.pending) == 0 || s.hidden || s.out.frames.Load()-s.acked >= s.credits {
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
	s.renderedSendable = s.sendable()
	for _, e := range s.dock {
		e.unsent = false
	}
	for _, e := range s.overlays {
		e.unsent = false
	}
	return sf.Render(s.view())
}

// sendable reports whether the editor shows and takes a prompt.
func (s *session) sendable() bool {
	return s.hasEditor && s.editor.Sendable
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
// the SDK's parser tells them from keys. It keeps the widths, credits, acks,
// evictions and visibility it reads, then passes the sequence to the SDK
// session. Everything else stays PiG's: keys, and the answers to PiG's own
// queries and Tern's light/dark reports (CSI ? 997 n), from which PiG reads
// Tern's colors and appearance; the theme event Tern sends with each report
// goes to the SDK unread.
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
	flush := false
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
			s.setCell(hello.Cols, hello.Cell)
			s.reduceMotion = hello.ReduceMotion
			if hello.Credits > 0 {
				s.credits = int64(hello.Credits)
			}
		case item.Event != nil:
			switch ev := item.Event.(type) {
			case *tern.Resize:
				s.setCell(ev.Cols, ev.Cell)
			case *tern.Ack:
				if ev.SF == surfaceID && s.frameBase+ev.S > s.acked {
					s.acked = s.frameBase + ev.S
					flush = true
					// Tern drew a frame sent after the adopt (Resume
					// counts the earlier ones acknowledged), so the
					// surface it adopted shows.
					s.adopting = false
				}
			case *tern.Gone:
				if ev.SF == surfaceID {
					s.evict(ev.IDs)
				}
			case *tern.Motion:
				s.reduceMotion = ev.Reduce
			case *tern.Visible:
				// The screen surface takes the view events while it covers
				// the pane.
				if ev.SF == surfaceID || ev.SF == screenID {
					s.hidden = !ev.Visible
					flush = flush || ev.Visible
				}
			}
		}
	}
	s.in.Write(input)
	if flush {
		// The SDK takes an ack on its own goroutine; ops handed over before
		// it does wait in the SDK's queue for the credit it returns.
		_ = s.flushLocked()
	}
	s.mu.Unlock()
	return true
}

// setCell keeps the pane's width in cells and a cell's height.
func (s *session) setCell(cols int, cell tern.Cell) {
	if cols > 0 {
		s.setCols(cols)
	}
	if cell.H > 0 {
		s.cellHeightPx = cell.H
	}
}

// Columns sizes PiG's lines for Tern's layout: the dock's are Tern's
// columns, and main's fit the narrowest chat style, a column short of each.
func (s *session) Columns() (main, dock int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.columnsLocked()
}

func (s *session) columnsLocked() (main, dock int) {
	if s.cols <= 0 {
		return 0, 0
	}
	return mainColumns(s.cols), max(1, s.cols-columnMargin)
}

// mainColumns is how many of a pane's cols an ansi block in main holds at
// least, under every chat style and face: Reader's measure, and Spine's
// block. The pane holds at least cols columns and fewer than cols+1, and
// Spine's padding grows with it, so the padding counts at cols+1.
func mainColumns(cols int) int {
	c := float64(cols)
	// Columns per design px, with the widest and the narrowest advance.
	few, many := 1/(designFontPx*widestAdvance), 1/(designFontPx*narrowestAdvance)
	spinePad := min(max(spinePadShare*(c+1), spinePadMinPx*many), spinePadMaxPx*many)
	spine := min(spineMaxPx*few, c) - 2*spinePad - spineIndentPx*many
	return max(1, int(math.Floor(min(readerMeasurePx*few, spine)))-columnMargin)
}

// readerMainColumn is the column, counted from the dock's first, where Reader
// shows main's lines: inside its padding, centered on its measure. It
// counts with Tern's own face; it only places a fullscreen animation's
// start over the transcript, where a column off shows for a frame.
func readerMainColumn(cols int) int {
	c, perPx := float64(cols), 1/(designFontPx*builtinAdvance)
	return int(readerPaddingPx*perPx + max(0, (c-(2*readerPaddingPx+readerMeasurePx)*perPx)/2))
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
	tsp, sf, fellBack, lent := s.tsp, s.sf, s.fellBack, s.lent
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
	// A fullscreen overlay still shown goes with the session.
	if s.screen != nil && s.screen.sf != nil {
		err = errors.Join(err, s.sendErr(s.screen.sf.Remove()))
	}
	s.screen = nil
	s.mu.Unlock()
	if tsp == nil || sf == nil {
		if tsp != nil {
			go tsp.Close()
		}
		return err
	}
	if fellBack {
		if lent {
			// Suspend closed the surface, keeping it in the pane; adopted
			// without listening, it can be removed.
			if adopted, openErr := tsp.Open(tern.SurfaceOptions{ID: surfaceID, Adopt: true, NoListen: true}); openErr == nil {
				sf = adopted
			}
		}
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
// per Write keeps them whole. It counts the frames written to the inline
// surface, which the transcript's folding compares with Tern's acks; the
// screen surface's frames have acks of their own, which the SDK counts.
type termWriter struct {
	mu     sync.Mutex
	w      io.Writer
	frames atomic.Int64
}

var (
	frameOpener = []byte("\x1b_tsp;f;")
	frameBody   = []byte(`{"sf":"` + surfaceID + `"`)
)

func (t *termWriter) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	n, err := t.w.Write(p)
	if err == nil && bytes.HasPrefix(p, frameOpener) {
		// A chunked frame's parameters come before its body.
		if body := bytes.IndexByte(p, '{'); body >= 0 && bytes.HasPrefix(p[body:], frameBody) {
			t.frames.Add(1)
		}
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
