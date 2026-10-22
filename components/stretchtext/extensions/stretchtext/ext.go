package stretchtext

import (
	"math"
	"strings"
	"sync"
)

// Core is the extension without the host: the state, the links and the animation, and the five things
// the host asks of them. It ports index.ts of pi-stretchtext. The SDK adapter (extension.go) binds
// each to an event, a shortcut or a Markdown transformer.
type Core struct {
	store    *Store
	links    *LinkTable
	animator *Animator
	theme    func() Theme
	redraw   func()

	mu       sync.Mutex
	attached bool
}

// NewCore returns a core. theme gives the host's theme, nil before it hands one over; redraw asks the
// host to draw the transcript again and may be called from any goroutine.
func NewCore(clock Clock, theme func() Theme, redraw func()) *Core {
	c := &Core{links: NewLinkTable(), animator: NewAnimator(clock), theme: theme, redraw: redraw}
	c.store = NewStore(c.requestRedraw)
	return c
}

// requestRedraw asks for the transcript to be drawn again, once the host has handed over its TUI:
// before that the original's Screen has nothing to draw with (src/screen.ts).
func (c *Core) requestRedraw() {
	c.mu.Lock()
	attached := c.attached
	c.mu.Unlock()
	if attached {
		c.redraw()
	}
}

// secondsFor is how long a stretch takes to grow or shrink: longer for a longer long form, as in
// cc-stretch.
func secondsFor(target Target) float64 {
	return math.Min(1.4, 0.45+float64(LengthOf(target.Stretch.Long))/70)
}

// ToggleStretch opens a closed stretch or closes an open one, growing or shrinking it in place.
func (c *Core) ToggleStretch(target Target) {
	isOpening := With(c.store, func(s *State) (bool, bool) { return s.toggle(target) })
	name := nameOf(target)
	from, to := 1.0, 0.0
	if isOpening {
		from, to = 0, 1
	}
	c.animator.Run(Animation{
		From:    from,
		To:      to,
		Seconds: secondsFor(target),
		Frame:   func(progress float64) { c.store.Run(func(s *State) bool { s.setGrowth(name, progress); return true }) },
		Done:    func() { c.store.Run(func(s *State) bool { s.settle(target, isOpening); return true }) },
	})
}

// Click toggles the stretch a clicked link address names. It reports whether the address was one of
// this extension's links.
func (c *Core) Click(address string) bool {
	target, ok := c.links.Resolve(address)
	if !ok {
		return false
	}
	c.ToggleStretch(target)
	return true
}

// Transform is the Markdown transformer: it draws a reply's stretches for the state.
func (c *Core) Transform(markdown, messageType string) string {
	if messageType != "assistant" || !strings.Contains(markdown, "{{") {
		return markdown
	}
	var segments []Segment
	c.store.View(func(s *State) { segments = s.parsed[markdown] })
	if segments == nil {
		segments = Parse(markdown)
		if len(Stretches(segments)) == 0 {
			return markdown
		}
	}
	c.store.Run(func(s *State) bool { return s.remember(markdown, segments) })
	var out string
	theme := c.themeNow()
	c.store.View(func(s *State) { out = Draw(s, Canvas{Links: c.links, Theme: theme}, markdown, segments) })
	return out
}

// StartLettering puts letters over the stretches mode acts on, and reports whether there were any.
func (c *Core) StartLettering(mode Mode) bool {
	return With(c.store, func(s *State) (bool, bool) {
		ok := s.startLettering(mode)
		return ok, ok
	})
}

// PickLetter takes the letters away and toggles the stretch the typed key stands for, if any.
func (c *Core) PickLetter(key string) {
	target, ok := With(c.store, func(s *State) (pick, bool) {
		t, ok := s.pickLetter(key)
		return pick{t, ok}, true
	}).unpack()
	if ok {
		c.ToggleStretch(target)
	}
}

type pick struct {
	target Target
	ok     bool
}

func (p pick) unpack() (Target, bool) { return p.target, p.ok }

// TakeReadNote is the note for the model about what the reader opened, and starts counting again.
func (c *Core) TakeReadNote() (string, bool) {
	type note struct {
		text string
		ok   bool
	}
	n := With(c.store, func(s *State) (note, bool) {
		text, ok := s.takeReadNote()
		return note{text, ok}, true
	})
	return n.text, n.ok
}

// Stop lands the running animation.
func (c *Core) Stop() { c.animator.Stop() }

// Attach is Screen.attach of the original: the host handed over its theme, so stretches are drawn in
// colour from now on, and the first time the replies drawn before it are drawn again.
func (c *Core) Attach() {
	c.mu.Lock()
	first := !c.attached
	c.attached = true
	c.mu.Unlock()
	if first {
		c.redraw()
	}
}

func (c *Core) themeNow() Theme {
	c.mu.Lock()
	attached := c.attached
	c.mu.Unlock()
	if !attached {
		return nil
	}
	return c.theme()
}

// LetterPrompt is the focused component that waits for the letter picking a stretch.
type LetterPrompt struct {
	core *Core
	mode Mode
}

// Alt is the alt+o and alt+c shortcuts: letters over each closed (ModeOpen) or open (ModeClose)
// stretch, and the prompt that takes the letter typed. It is nil, with the notice to show, when there
// is nothing to act on.
func (c *Core) Alt(mode Mode) (*LetterPrompt, string) {
	if !c.StartLettering(mode) {
		return nil, "stretchtext: nothing to " + string(mode)
	}
	c.Attach()
	return &LetterPrompt{core: c, mode: mode}, ""
}

// Render is the one line the prompt draws.
func (p *LetterPrompt) Render(width int) string {
	line := "stretch: type a letter to " + string(p.mode) + " (any other key cancels)"
	// line.slice(0, width): a negative width counts from the end.
	if width < 0 {
		width += len(line)
	}
	if width < len(line) {
		line = line[:max(width, 0)]
	}
	if theme := p.core.themeNow(); theme != nil {
		return theme.Fg("dim", line)
	}
	return line
}

// HandleInput takes the letters away and toggles the stretch the key stands for; the prompt is done
// after any key.
func (p *LetterPrompt) HandleInput(key string) { p.core.PickLetter(key) }

// Sections is the system prompt's named sections, as before_agent_start hands them over.
type Sections interface {
	Set(name, value string)
	Delete(name string)
}

// BeforeAgentStart adds the guide, and the note on what the reader opened, to the system prompt.
func (c *Core) BeforeAgentStart(sections Sections) {
	sections.Set("stretchtext", Guide)
	if note, ok := c.TakeReadNote(); ok {
		sections.Set("stretchtext_read", note)
	} else {
		sections.Delete("stretchtext_read")
	}
}
