package pig_music

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/pig-music/internal/keys"
)

const (
	// fallbackRows is the height assumed until the host reports one.
	fallbackRows = 24
	// keyLogSize is how many received keys the hello screen echoes.
	keyLogSize = 12
	// tickEvery is the redraw timer of the hello screen.
	tickEvery = time.Second
	// pollEvery is how often the component looks for a changed terminal height.
	// The host tells a component about a new width (it renders again), but it only
	// tells the extension process about a new height; nothing re-renders for it.
	pollEvery = 250 * time.Millisecond
)

// hello is the milestone 2 component: a full-terminal screen that shows the size
// it was given, echoes every key it receives, redraws once a second from a timer
// and closes on q. It proves the overlay, input and timer paths of the Go SDK.
//
// The value outlives one /music call: closing the screen and opening it again
// shows the same key log and counters, which is the behavior the player needs
// from its own state.
type hello struct {
	mu         sync.Mutex
	height     func() int
	tickEvery  time.Duration
	pollEvery  time.Duration
	now        func() time.Time
	invalidate func()
	stop       chan struct{}
	done       chan struct{}

	openedAt time.Time
	cols     int
	rows     int
	renders  int
	ticks    int
	received int
	log      []string
}

func newHello(height func() int) *hello {
	return &hello{height: height, tickEvery: tickEvery, pollEvery: pollEvery, now: time.Now, openedAt: time.Now()}
}

// SetInvalidate receives the SDK's render callback when the screen opens and nil
// when it closes. The timer runs between the two.
func (h *hello) SetInvalidate(invalidate func()) {
	h.mu.Lock()
	stop, done := h.stop, h.done
	h.stop, h.done = nil, nil
	h.invalidate = invalidate
	if invalidate != nil {
		h.openedAt = h.now()
		h.stop, h.done = make(chan struct{}), make(chan struct{})
		go h.run(invalidate, h.stop, h.done)
	}
	h.mu.Unlock()
	if stop != nil {
		close(stop)
		<-done
	}
}

func (h *hello) run(invalidate func(), stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	poll := time.NewTicker(h.pollEvery)
	defer poll.Stop()
	tick := time.NewTicker(h.tickEvery)
	defer tick.Stop()
	lastRows := h.rowsNow()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			h.mu.Lock()
			h.ticks++
			h.mu.Unlock()
			lastRows = h.rowsNow()
			invalidate()
		case <-poll.C:
			if rows := h.rowsNow(); rows != lastRows {
				lastRows = rows
				invalidate()
			}
		}
	}
}

func (h *hello) rowsNow() int {
	h.mu.Lock()
	height := h.height
	h.mu.Unlock()
	if height != nil {
		if rows := height(); rows > 0 {
			return rows
		}
	}
	return fallbackRows
}

// Render draws exactly one line per terminal row, each at most width cells wide.
func (h *hello) Render(width int) []string {
	rows := h.rowsNow()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cols, h.rows = width, rows
	h.renders++
	if width < 1 {
		return nil
	}
	body := []string{
		fmt.Sprintf("terminal %d columns x %d rows  (render width %d)", width, rows, width),
		fmt.Sprintf("open %s   redraw timer ticks %d   renders %d   clock %s",
			h.now().Sub(h.openedAt).Truncate(time.Second), h.ticks, h.renders, h.now().Format("15:04:05")),
		fmt.Sprintf("keys received %d", h.received),
		"",
		"last keys, newest first:",
	}
	for i := len(h.log) - 1; i >= 0; i-- {
		body = append(body, "  "+h.log[i])
	}
	return frame(body, "press q or Esc to close", "pig-music hello", width, rows)
}

// HandleInput echoes the key and closes on q, Esc or ctrl+c.
func (h *hello) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	key, ok := keys.Decode(data)
	h.mu.Lock()
	defer h.mu.Unlock()
	if ok && key.Release {
		return sdk.RemoteComponentResult{}, nil
	}
	h.received++
	entry := fmt.Sprintf("%-14s %s", "(unnamed)", ascii(strconv.Quote(data)))
	if ok {
		entry = fmt.Sprintf("%-14s %s", ascii(key.Name), ascii(strconv.Quote(data)))
	}
	h.log = append(h.log, entry)
	if len(h.log) > keyLogSize {
		h.log = h.log[len(h.log)-keyLogSize:]
	}
	if ok && (key.Name == "q" || key.Name == "escape" || key.Name == "ctrl+c") {
		return sdk.RemoteComponentResult{Done: true, Value: map[string]any{"keys": h.received, "ticks": h.ticks}}, nil
	}
	return sdk.RemoteComponentResult{}, nil
}

// Dispose runs when the screen closes. The state stays for the next /music.
func (h *hello) Dispose() {}

// Counters returns what the screen counted, for the closing notice.
func (h *hello) Counters() (keysReceived, ticks int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.received, h.ticks
}

// frame draws body inside a box of rows lines, each width cells wide, with title
// in the top edge and footer in the bottom edge. A terminal too small for a box
// gets bare lines.
func frame(body []string, footer, title string, width, rows int) []string {
	lines := make([]string, 0, rows)
	if width < 12 || rows < 4 {
		for i := 0; i < rows; i++ {
			text := ""
			if i < len(body) {
				text = body[i]
			}
			lines = append(lines, fit(text, width))
		}
		return lines
	}
	inner := width - 4
	lines = append(lines, edge("+- "+title+" ", width))
	for i := 0; i < rows-2; i++ {
		text := ""
		switch {
		case i == rows-3:
			text = footer
		case i < len(body):
			text = body[i]
		}
		lines = append(lines, "| "+fit(text, inner)+" |")
	}
	return append(lines, edge("+", width))
}

// edge pads a box edge with dashes to width and closes it with "+".
func edge(prefix string, width int) string {
	if len(prefix) > width-1 {
		prefix = prefix[:width-1]
	}
	return prefix + strings.Repeat("-", width-1-len(prefix)) + "+"
}

// fit pads or truncates ASCII text to exactly width cells.
func fit(s string, width int) string {
	if len(s) > width {
		s = s[:width]
	}
	return s + strings.Repeat(" ", width-len(s))
}

// ascii replaces every non-ASCII rune with a \u escape so that one rune is one cell.
func ascii(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return r >= utf8.RuneSelf }) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if r < utf8.RuneSelf {
			b.WriteRune(r)
		} else {
			fmt.Fprintf(&b, "\\u%04x", r)
		}
	}
	return b.String()
}
