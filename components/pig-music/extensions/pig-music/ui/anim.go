package ui

import (
	"context"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/MichaelKinsy/pigpen/pig-music/art"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// tickEvery is the disc's frame time: a turn of art.DiscFrames frames takes about two seconds.
const tickEvery = 160 * time.Millisecond

// fadeEvery is the frame time of a palette cross-fade, which is short (fadeSteps frames) and finer than the disc's.
const (
	fadeEvery = 80 * time.Millisecond
	fadeSteps = 8
)

// defaultTick is tea.Tick; it is a variable so that the tests' rig, which runs commands inline, can do without real waiting.
var defaultTick = func(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return TickMsg{} })
}

// artDrawn remembers the lines of the cover for one size and colour mode, so drawing a frame does not scale the picture
// again. It is touched only on the host's one drawing and updating path.
type artDrawn struct {
	key   string
	lines []string
}

// settle starts what the model's state calls for, after every message: the cover of the current track when it is not
// known, and the disc's tick chain when the disc is on screen and turning.
func (m Model) settle() (Model, tea.Cmd) {
	var cmds []tea.Cmd
	vibes := m.vibes()
	cur := m.state.Track
	if cur != nil && m.artID != cur.ID {
		m.artImg = nil // the old cover is not this track's
	}
	if a := m.deps.Art; a != nil && cur != nil && m.artID != cur.ID && m.artReq != cur.ID && !m.artBad[cur.ID] {
		m.artReq = cur.ID
		t := m.fill(*cur)
		cmds = append(cmds, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
			defer cancel()
			img, err := a.Image(ctx, t)
			msg := artMsg{id: t.ID, img: img, err: err}
			if err == nil && vibes {
				msg.pal, msg.hasPal = art.Derive(art.Extract(img, 4)), true // here, not while drawing
			}
			return msg
		})
	}
	// a track whose cover is not coming (no art source, or it failed) gets a palette from its metadata
	if vibes && cur != nil && m.palID != cur.ID && (m.deps.Art == nil || m.artBad[cur.ID]) {
		m.palID = cur.ID
		f := m.fill(*cur)
		artist := ""
		if len(f.Artists) > 0 {
			artist = f.Artists[0]
		}
		m = m.fadeTo(art.FromTrack(artist, f.Title))
	}
	// measuring runs only while the pulse does; the player is told once when it starts and once when it stops
	if lv, ok := m.deps.Player.(music.Levels); ok && m.pulsing() != m.levelsOn {
		m.levelsOn = m.pulsing()
		on := m.levelsOn
		if !on {
			m.beat, m.rms, m.avg, m.haveLevel, m.pend, m.warm, m.frameDue = 0, 0, 0, false, false, false, false
		}
		if m.lvSync == nil {
			m.lvSync = &levelsSync{}
		}
		m.lvSync.want.Store(on) // here, in order; the command may run before or after another one
		ls := m.lvSync
		cmds = append(cmds, func() tea.Msg {
			ls.apply(lv)
			return nil
		})
	}
	if m.wantTick() && !m.ticking {
		m.ticking = true
		tick := m.deps.Tick
		if tick == nil {
			tick = defaultTick
		}
		every := tickEvery
		switch {
		case m.pulsing() && m.deps.LowPower:
			every = tickEvery
		case m.pulsing():
			every = pulseEvery
		case m.fading:
			every = fadeEvery
		}
		m.lastTick = every
		cmds = append(cmds, tick(every))
	}
	return m, tea.Batch(cmds...)
}

// levelsSync brings the player's measuring to what the model asked last. The host runs each command in its own goroutine, so
// a switch-off can overtake the switch-on before it; each command therefore applies the latest wish, one at a time, and the
// player ends as asked whatever the order.
type levelsSync struct {
	mu      sync.Mutex
	want    atomic.Bool
	applied bool
	known   bool // applied is what the player was last told successfully
}

func (s *levelsSync) apply(lv music.Levels) {
	s.mu.Lock()
	defer s.mu.Unlock()
	on := s.want.Load()
	if s.known && s.applied == on {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := lv.SetLevels(ctx, on) // a player that cannot measure just gives no readings
	s.applied, s.known = on, err == nil
}

// pulseEvery caps the pulse at about 15 frames a second.
const pulseEvery = 66 * time.Millisecond

// pulsing: the overlay moves with the music. It needs the palette and colour, a player that can measure, a track playing, and
// a screen that is shown; paused, stopped, hidden or calm, nothing is measured and nothing ticks.
func (m Model) pulsing() bool {
	if !m.deps.Pulse || m.deps.Calm || !m.vibes() || m.hidden {
		return false
	}
	if _, ok := m.deps.Player.(music.Levels); !ok {
		return false
	}
	t := m.state.Track
	return m.state.Connected && t != nil && !m.state.Paused
}

// takeLevel folds one reading into the pulse. A beat is loudness above the slow average, so steady loud music is no beat; the
// pulse decays when there is no reading or no news.
func (m Model) takeLevel(msg levelMsg) Model {
	x := 0.0
	if msg.ok {
		x = msg.l.RMS
	}
	m.haveLevel = msg.ok
	m.rms = math.Max(x, m.rms*0.85)
	target := 0.0
	if msg.ok && !m.warm { // the first reading is the average: there is nothing to be louder than yet
		m.avg, m.warm = x, true
	}
	if d := x - m.avg - 0.1*m.avg - 0.02; msg.ok && d > 0 { // a dead band, so ripple is no beat
		target = math.Min(d/(0.25*math.Max(m.avg, 0.1)), 1)
	}
	if msg.ok {
		m.avg += 0.05 * (x - m.avg)
	}
	m.beat = math.Max(target, m.beat*0.82)
	if m.beat < 0.01 {
		m.beat = 0
	}
	return m
}

// vibes reports whether the overlay is painted: the setting is on and the terminal has colour.
func (m Model) vibes() bool { return m.deps.Vibes && m.deps.ArtMode != art.Mono }

// fadeTo starts a cross-fade to p from what is on screen now. The first palette of a session, and any change while the screen
// is hidden, is simply there: there is nothing to fade from, or nobody to see it.
func (m Model) fadeTo(p art.Palette) Model {
	if !m.palShown || m.hidden || m.deps.Calm || m.deps.LowPower {
		m.pal, m.fading, m.palShown = p, false, true
		return m
	}
	m.palFrom, m.palTo, m.fade, m.fading = m.pal, p, 0, true
	return m
}

// advance moves what animates by one tick: the disc (faster on a beat, at half speed in low power) and a cross-fade.
func (m Model) advance() Model {
	if m.discTurning() {
		m.discAcc += time.Duration(float64(m.lastTick) * (1 + m.beat)) // a beat spins it up
		step := tickEvery
		if m.deps.LowPower {
			step = 2 * tickEvery // half speed
		}
		for m.discAcc >= step {
			m.frame = (m.frame + 1) % art.DiscFrames
			m.discAcc -= step
		}
	}
	if m.fading {
		m = m.stepFade()
	}
	return m
}

func (m Model) stepFade() Model {
	m.fade += 1.0 / fadeSteps
	if m.fade >= 1 {
		m.pal, m.fading = m.palTo, false
		return m
	}
	m.pal = art.Mix(m.palFrom, m.palTo, m.fade)
	return m
}

// wantTick: something is animating: a palette cross-fade, or the disc turning.
func (m Model) wantTick() bool { return (m.fading && !m.hidden) || m.discTurning() || m.pulsing() }

// discTurning: the disc turns only while a track plays, the screen is shown on the Player screen with a disc on it. Paused,
// stopped, hidden, on another screen, or with a cover drawn: nothing animates, so nothing redraws.
func (m Model) discTurning() bool {
	t := m.state.Track
	switch {
	case m.deps.Calm: // no motion at all
		return false
	case m.hidden || m.tab != tabPlayer || m.settings.open || m.help:
		return false
	case !m.state.Connected || t == nil || m.state.Paused:
		return false
	case m.artImg != nil && m.artID == t.ID:
		return false
	}
	return m.coverRows() > 0
}

// coverRows is the height of the picture on the Player screen, 0 when it does not fit.
func (m Model) coverRows() int {
	if m.w < minWidth || m.h < minHeight || m.w < 60 {
		return 0
	}
	leftW := min(max(m.w/3, 22), 38)
	_, rows := coverSize(leftW, m.h-6-2-len(m.nowText())-1)
	return rows
}

// coverSize is the picture's size in cells for a panel w wide with free rows to spare, as 2:1 so it comes out round.
// A picture under 4 rows is not worth drawing.
func coverSize(w, free int) (cols, rows int) {
	rows = min(free, (w-2)/2, 16)
	if rows < 4 {
		return 0, 0
	}
	return rows * 2, rows
}

// cover draws the picture, margin included, or nil: the loaded cover, else the disc.
func (m Model) cover(w, free int) []string {
	cols, rows := coverSize(w, free)
	if rows == 0 {
		return nil
	}
	var lines []string
	if t := m.state.Track; t != nil && m.artImg != nil && m.artID == t.ID {
		key := fmt.Sprintf("%s|%d|%d|%d", t.ID, cols, rows, m.deps.ArtMode)
		if m.artDraw.key != key {
			m.artDraw.key, m.artDraw.lines = key, art.Render(m.artImg, cols, rows, m.deps.ArtMode)
		}
		lines = m.artDraw.lines
	} else {
		lines = art.Disc(m.frame, cols, rows)
		if m.vibes() {
			coloured := make([]string, len(lines))
			for i, l := range lines {
				coloured[i] = accent.Render(l)
			}
			lines = coloured
		}
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = " " + l
	}
	return out
}
