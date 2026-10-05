// Package ui is the pig-music player screen: a Bubble Tea v2 model with a
// Search, a Library and a Player screen, a play bar and a key map after Orpheus's
// (read from its README and screenshots only; none of its code, themes or assets
// is used). It talks to music.Source and music.Player and knows nothing of PiG:
// package teahost runs it inside the overlay.
package ui

import (
	"context"
	"fmt"
	"image"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/MichaelKinsy/pigpen/pig-music/art"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// Deps are what the model plays with.
type Deps struct {
	Source music.Source
	Player music.Player
	// Settings is the settings screen's way to the settings file; nil when there is none.
	Settings SettingsPort
	// Art fetches the cover of a track; nil means no cover art, and the Player screen shows the disc.
	Art music.Artwork
	// ArtMode is how much colour the cover is drawn with (art.ModeFromEnv in the extension); the zero value is monochrome.
	ArtMode art.Mode
	// Vibes paints the overlay with a palette from the cover or the track: a gradient background, an accent and readable
	// text. It is off in monochrome (NO_COLOR) whatever this says.
	Vibes bool
	// Pulse moves the overlay with the music: the background brightens on a beat, the disc turns faster, a meter shows the
	// loudness. It needs Vibes, colour, and a Player that is a music.Levels; Calm switches all motion off.
	Pulse bool
	// LowPower slows everything that moves: the pulse to about 6 frames a second, the disc to half speed, and a palette change
	// snaps instead of fading.
	LowPower bool
	// Calm: no motion at all (no disc turning, no pulse, no cross-fade).
	Calm bool
	// NoColor: the user asked for no colour (art.NoColor: NO_COLOR, TERM=dumb), so not even the accents' basic colours are
	// written; they become bold.
	NoColor bool
	// Tick schedules the next animation frame; nil means tea.Tick. Tests replace it.
	Tick func(time.Duration) tea.Cmd
}

// StateMsg carries a new player state; the host forwards Player.Subscribe into it.
type StateMsg music.State

type searchDoneMsg struct {
	query  string
	tracks []music.Track
	err    error
	gen    int // the search it answers (Model.searchGen): the same query searched again is a newer search
}

// entitiesDoneMsg is the albums and artists for a search (a Source that has them), asked at the same time as the songs.
type entitiesDoneMsg struct {
	query string
	ents  []music.Entity
	err   error
	gen   int
}

// entityOpenedMsg is the tracks of an opened album or artist.
type entityOpenedMsg struct {
	id     string
	title  string
	tracks []music.Track
	err    error
	gen    int // the opening it answers (entityPage.gen): the same album opened again is a newer one
}

// ShowPlayerMsg switches to the Player screen: the host sends it when the player was already playing before the screen
// was ever opened (after a /reload), so that /music shows the queue rather than an empty search.
type ShowPlayerMsg struct{}

// OpenedMsg tells the model its screen was just shown again after being hidden. If the person left it on a Library
// screen that cannot list anything (no consent, no browser, an error) and nothing is playing, it shows Search: the error
// belongs on the Library tab, not in the way of the music. A working library, a playing track, and every other screen
// stay as they were.
type OpenedMsg struct{}

// enrichedMsg is the artist, album and length of one track, asked for in the background.
type enrichedMsg struct {
	id    string
	track music.Track
	err   error
}

// StyleMsg replaces the four switches of how the player looks and moves while it runs: the settings screen sends it.
type StyleMsg struct{ Palette, Pulse, Calm, LowPower bool }

// ClosedMsg tells the model its screen was hidden: the animation stops until OpenedMsg.
type ClosedMsg struct{}

// TickMsg is one frame of the disc's animation.
type TickMsg struct{}

// artMsg is the cover of one track, fetched in the background.
type artMsg struct {
	id     string
	img    image.Image
	err    error
	pal    art.Palette // the cover's palette, worked out off the drawing path
	hasPal bool
}

// levelMsg is one reading of the music's loudness.
type levelMsg struct {
	l  music.Level
	ok bool
}

type opDoneMsg struct{ err error }

const (
	tabSearch  = "search"
	tabLibrary = "library"
	tabPlayer  = "player"

	searchLimit = 20
	seekStep    = 5 * time.Second
	volumeStep  = 5
	opTimeout   = 30 * time.Second
	// libTimeout bounds a library listing, which pages through a whole collection; a little over the Source's own bound.
	libTimeout = 2*time.Minute + 10*time.Second
)

var tabOrder = []string{tabSearch, tabLibrary, tabPlayer}

// Model is the player screen.
type Model struct {
	deps Deps
	w, h int

	tab    string
	help   bool
	status string // an error or notice shown above the key hints

	state music.State

	// search
	typing   bool
	query    string // what is being typed
	searched string // what the results are for
	loading  bool
	results  []music.Track
	resCur   int
	enr      *enrichState

	// albums and artists of the search, and the one that is open (entities.go)
	ents    []music.Entity
	entKind string // "songs", "albums" or "artists": which list the search screen shows
	entCur  int
	ent     *entityPage
	// stop the requests of the search on screen and of the album or artist being opened: a newer search, or leaving the
	// page, cancels them rather than let them run on for up to opTimeout/libTimeout.
	searchStop, entStop context.CancelFunc
	searchGen, entGen   int // counts searches and openings, so a cancelled answer of an earlier one is told apart

	// animation and cover art
	hidden  bool // the screen is hidden (ClosedMsg): nothing animates
	ticking bool // a tick is on its way, so there is never a second chain
	frame   int  // the disc's picture
	artID   string
	artImg  image.Image // the cover of artID, nil until it has loaded
	artReq  string      // the track whose cover was asked for last
	artBad  map[string]bool
	artDraw *artDrawn

	// palette: pal is what is drawn; during a cross-fade it moves from palFrom to palTo as fade goes from 0 to 1
	pal              art.Palette
	palFrom, palTo   art.Palette
	fade             float64
	fading, palShown bool
	palID            string // the track the palette was chosen for

	// pulse: levelsOn is whether the player was asked to measure; pend that a reading is on its way; avg the slow average of
	// the loudness a beat is measured against; beat the current pulse strength (0 to 1); rms what the meter shows
	levelsOn, pend bool
	avg, beat, rms float64
	haveLevel      bool
	warm           bool           // the average has its first reading
	frameDue       bool           // a tick's frame waits for the reading it asked for
	lvSync         *levelsSync    // shared by the model's copies: the player's measuring, in the order the model asked
	queueGen       *atomic.Uint64 // counts the choices of what to play; shared by the copies, read by a collection being queued
	lastTick       time.Duration  // the length of the tick on its way, for the disc's clock
	discAcc        time.Duration

	lib      library
	settings settingsView

	// player
	queueCur int
}

// New returns the model in its starting state: the search screen, nothing playing.
func New(d Deps) Model {
	return Model{deps: d, tab: tabSearch, state: music.State{Index: -1}, enr: newEnrichState(), artBad: map[string]bool{}, artDraw: &artDrawn{}, lvSync: &levelsSync{}, queueGen: &atomic.Uint64{}, pal: art.FromTrack("", "")}
}

// Tab names the screen being shown: "search", "library" or "player".
func (m Model) Tab() string { return m.tab }

// Typing reports whether the search input has the keyboard.
func (m Model) Typing() bool { return m.typing }

// Init has nothing to start: the host forwards player states.
func (m Model) Init() tea.Cmd { return nil }

// Update handles one message, then starts what the new state calls for: a cover to fetch, the disc's next frame.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	nm, ok := next.(Model)
	if !ok {
		return next, cmd
	}
	nm, extra := nm.settle()
	return nm, tea.Batch(cmd, extra)
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case TickMsg:
		m.ticking = false
		if m.pulsing() {
			// While pulsing, a tick is one frame: it asks for a reading, and the reading moves everything at once, so the
			// host repaints once per tick, not once for the disc and again for the level.
			if m.pend {
				break // the last tick's reading is still on its way; it draws that frame
			}
			if lv, ok := m.deps.Player.(music.Levels); ok {
				m.pend, m.frameDue = true, true
				return m, func() tea.Msg {
					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					defer cancel()
					l, got := lv.Level(ctx)
					return levelMsg{l: l, ok: got}
				}
			}
		}
		m = m.advance()
	case levelMsg:
		m.pend = false
		m = m.takeLevel(msg)
		if m.frameDue {
			m.frameDue = false
			m = m.advance()
		}
	case StyleMsg:
		m.deps.Vibes, m.deps.Pulse, m.deps.Calm, m.deps.LowPower = msg.Palette, msg.Pulse, msg.Calm, msg.LowPower
		if (m.deps.Calm || m.deps.LowPower) && m.fading { // no motion: the new colours are simply there
			m.pal, m.fading = m.palTo, false
		}
	case ClosedMsg:
		m.hidden = true
		if m.fading { // nobody is looking: no animation, the new colours are simply there
			m.pal, m.fading = m.palTo, false
		}
	case artMsg:
		if cur := m.state.Track; cur != nil && cur.ID == msg.id {
			if msg.err != nil || msg.img == nil {
				m.artBad[msg.id] = true // the disc stays; a cover that is not there is not worth an error
			} else {
				m.artID, m.artImg = msg.id, msg.img
				if msg.hasPal && m.vibes() {
					m.palID = msg.id
					m = m.fadeTo(msg.pal)
				}
			}
		}
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case StateMsg:
		m = m.withState(music.State(msg))
		return m.pump()
	case settingsLoadedMsg:
		if msg.err != nil {
			m.settings.loadErr = msg.err.Error()
			break
		}
		m.settings.stopOnExit, m.settings.footerStatus, m.settings.info = msg.stop, msg.footer, msg.info
	case settingsSavedMsg:
		m = m.settingsSaved(msg)
	case OpenedMsg:
		m.hidden = false
		if m.tab == tabLibrary && m.state.Track == nil {
			switch m.lib.state {
			case libConsent, libDeclined, libProblem:
				m.tab = tabSearch
			}
		}
	case ShowPlayerMsg:
		m.tab = tabPlayer
		if m.state.Index >= 0 {
			m.queueCur = m.state.Index
		}
	case searchDoneMsg:
		if msg.query == m.searched && msg.gen == m.searchGen { // else a later search replaced this one
			m.loading = false
			m.results, m.resCur = msg.tracks, 0
			if msg.err != nil {
				m.status = msg.err.Error()
			}
			return m.pump()
		}
	case entitiesDoneMsg:
		m = m.entitiesDone(msg)
	case entityOpenedMsg:
		m = m.entityOpened(msg)
		return m.pump() // an artist's top songs come without a length
	case enrichedMsg:
		m.enr.inflight--
		if msg.err != nil {
			m.enr.failed[msg.id] = true
			m.enr.failures++
			if m.enr.failures == 1 && m.status == "" {
				m.status = "artist and length are missing for some tracks: " + msg.err.Error()
			}
		} else {
			m.enr.info[msg.id] = msg.track
		}
		return m.pump() // the next track in line
	case libCachedMsg:
		m = m.libraryCached(msg)
	case libraryDoneMsg:
		m = m.libraryDone(msg)
	case libTracksMsg:
		m = m.libTracksDone(msg)
		return m.pump()
	case grantedMsg:
		if msg.err != nil {
			m = m.libraryDone(libraryDoneMsg{err: msg.err}) // another browser in use now is asked about, not granted
			break
		}
		return m.loadLibrary(true)
	case opDoneMsg:
		if msg.err != nil {
			m.status = msg.err.Error()
		}
	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m Model) withState(s music.State) Model {
	followed := s.Index >= 0 && s.Index != m.state.Index
	m.state = s
	if followed {
		m.queueCur = s.Index
	}
	m.queueCur = clamp(m.queueCur, len(s.Queue))
	return m
}

func clamp(i, n int) int {
	if i >= n {
		i = n - 1
	}
	if i < 0 {
		i = 0
	}
	return i
}

// op runs a player command off the update loop and reports its error.
func op(f func(ctx context.Context) error) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		return opDoneMsg{f(ctx)}
	}
}

func (m Model) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	m.status = ""
	if k == "ctrl+c" {
		return m, tea.Quit
	}
	if m.typing {
		return m.typingKey(msg, k)
	}
	if m.settings.open {
		return m.settingsKey(k)
	}
	if m.help {
		if k == "esc" || k == "?" || k == "q" {
			m.help = false
		}
		return m, nil
	}
	p := m.deps.Player
	if m.tab == tabLibrary {
		switch {
		case k == "esc" && m.lib.state == libReady && m.lib.open != nil:
			m.lib.open, m.lib.tracks, m.lib.loading = nil, nil, false // back to the list; q and esc on the list hide
			return m, nil
		case m.lib.state == libConsent && (k == "y" || k == "n"):
			return m.libraryKey(k) // n here means "not now", not "next track"
		}
	}
	switch k {
	case "q", "esc":
		return m, tea.Quit
	case "?":
		m.help = true
	case "o":
		return m.openSettings()
	case "/":
		m.tab, m.typing = tabSearch, true
	case "tab":
		return m.goTab(1)
	case "shift+tab":
		return m.goTab(-1)
	case "space":
		return m, op(func(ctx context.Context) error { return p.TogglePause(ctx) })
	case "n":
		return m, op(func(ctx context.Context) error { return p.Next(ctx) })
	case "p":
		return m, op(func(ctx context.Context) error { return p.Prev(ctx) })
	case "right":
		return m, op(func(ctx context.Context) error { return p.SeekRelative(ctx, seekStep) })
	case "left":
		return m, op(func(ctx context.Context) error { return p.SeekRelative(ctx, -seekStep) })
	case "s":
		modes, ok := p.(music.Modes)
		if !ok {
			m.status = "shuffle is not available with this player"
			return m, nil
		}
		on := !m.state.Shuffle
		return m, op(func(ctx context.Context) error { return modes.SetShuffle(ctx, on) })
	case "l":
		modes, ok := p.(music.Modes)
		if !ok {
			m.status = "repeat is not available with this player"
			return m, nil
		}
		next := map[music.RepeatMode]music.RepeatMode{music.RepeatOff: music.RepeatAll, music.RepeatAll: music.RepeatOne, music.RepeatOne: music.RepeatOff}[m.state.Repeat]
		return m, op(func(ctx context.Context) error { return modes.SetRepeat(ctx, next) })
	case "+", "=":
		v := min(m.state.Volume+volumeStep, 100)
		return m, op(func(ctx context.Context) error { return p.SetVolume(ctx, v) })
	case "-", "_":
		v := max(m.state.Volume-volumeStep, 0)
		return m, op(func(ctx context.Context) error { return p.SetVolume(ctx, v) })
	default:
		switch m.tab {
		case tabSearch:
			return m.searchKey(k)
		case tabPlayer:
			return m.playerKey(k)
		case tabLibrary:
			return m.libraryKey(k)
		}
	}
	return m, nil
}

func (m Model) goTab(delta int) (tea.Model, tea.Cmd) {
	i := 0
	for j, t := range tabOrder {
		if t == m.tab {
			i = j
		}
	}
	m.tab = tabOrder[(i+delta+len(tabOrder))%len(tabOrder)]
	switch m.tab {
	case tabPlayer:
		if m.state.Index >= 0 {
			m.queueCur = m.state.Index
		}
		m.queueCur = clamp(m.queueCur, len(m.state.Queue))
	case tabLibrary:
		if m.lib.state == libIdle {
			return m.loadLibrary(true)
		}
	}
	return m, nil
}

func (m Model) typingKey(msg tea.KeyPressMsg, k string) (tea.Model, tea.Cmd) {
	switch k {
	case "esc":
		m.typing = false
	case "enter":
		m.typing = false
		q := trimSpace(m.query)
		if q == "" {
			return m, nil
		}
		m.searched, m.loading, m.results, m.resCur = q, true, nil, 0
		m.ents, m.entKind, m.entCur, m.ent = nil, "", 0, nil
		m = m.stopEntity()
		if m.searchStop != nil {
			m.searchStop() // the search this one replaces
		}
		parent, stop := context.WithCancel(context.Background())
		m.searchStop = stop
		m.searchGen++
		gen := m.searchGen
		m.enr.retryFailed()
		src := m.deps.Source
		songs := func() tea.Msg {
			ctx, cancel := context.WithTimeout(parent, opTimeout)
			defer cancel()
			tracks, err := src.Search(ctx, q, searchLimit)
			return searchDoneMsg{q, tracks, err, gen}
		}
		if es, ok := src.(music.EntitySearcher); ok {
			return m, tea.Batch(songs, func() tea.Msg {
				ctx, cancel := context.WithTimeout(parent, opTimeout)
				defer cancel()
				ents, err := es.SearchEntities(ctx, q, entitiesPerKind)
				return entitiesDoneMsg{q, ents, err, gen}
			})
		}
		return m, songs
	case "backspace":
		if r := []rune(m.query); len(r) > 0 {
			m.query = string(r[:len(r)-1])
		}
	default:
		if t := msg.Key().Text; t != "" {
			m.query += t
		}
	}
	return m, nil
}

func (m Model) searchKey(k string) (tea.Model, tea.Cmd) {
	if next, cmd, handled := m.entityKey(k); handled {
		return next, cmd
	}
	n := len(m.results)
	switch k {
	case "down", "j":
		m.resCur = clamp(m.resCur+1, n)
		return m.pump()
	case "up", "k":
		m.resCur = clamp(m.resCur-1, n)
		return m.pump()
	case "enter":
		if n == 0 {
			return m, nil
		}
		p, res, at := m.deps.Player, m.filled(m.results), m.resCur
		m.tab = tabPlayer
		m.queueGen.Add(1) // a collection still being added to the queue is not the queue any more
		return m, op(func(ctx context.Context) error { return p.Replace(ctx, res, at) })
	case "a":
		if n == 0 {
			return m, nil
		}
		p, t := m.deps.Player, m.fill(m.results[m.resCur])
		return m, op(func(ctx context.Context) error { return p.Enqueue(ctx, t) })
	}
	return m, nil
}

func (m Model) playerKey(k string) (tea.Model, tea.Cmd) {
	n := len(m.state.Queue)
	p, cur := m.deps.Player, m.queueCur
	switch k {
	case "down", "j":
		m.queueCur = clamp(cur+1, n)
		return m.pump()
	case "up", "k":
		m.queueCur = clamp(cur-1, n)
		return m.pump()
	case "enter":
		if n > 0 {
			return m, op(func(ctx context.Context) error { return p.Jump(ctx, cur) })
		}
	case "x":
		if n > 0 {
			return m, op(func(ctx context.Context) error { return p.Remove(ctx, cur) })
		}
	case "[":
		if cur > 0 {
			m.queueCur = cur - 1
			return m, op(func(ctx context.Context) error { return p.Move(ctx, cur, cur-1) })
		}
	case "]":
		if cur < n-1 {
			m.queueCur = cur + 1
			return m, op(func(ctx context.Context) error { return p.Move(ctx, cur, cur+1) })
		}
	}
	return m, nil
}

func trimSpace(s string) string {
	b, e := 0, len(s)
	for b < e && (s[b] == ' ' || s[b] == '\t') {
		b++
	}
	for e > b && (s[e-1] == ' ' || s[e-1] == '\t') {
		e--
	}
	return s[b:e]
}

func clock(d time.Duration) string {
	s := int(d.Round(time.Second) / time.Second)
	if s < 0 {
		s = 0
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
