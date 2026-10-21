package pig_music

import (
	"fmt"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
	"github.com/MichaelKinsy/pigpen/pig-music/teahost"
	"github.com/MichaelKinsy/pigpen/pig-music/ui"
)

// quickRows is the height of the /music settings box: its heading, the now-playing line, the seven rows, a note and the hints.
const quickRows = 19

// quickScreen is the sdk.ViewComponent of /music settings. Unlike the player it owns its model: the screen is made when
// the command runs and gone when the box closes.
type quickScreen struct {
	host *teahost.Host

	mu         sync.Mutex
	invalidate func()
}

func newQuickScreen(host *teahost.Host, _ func() int) *quickScreen { return &quickScreen{host: host} }

// redraw asks PiG to draw the box again; the model's changes (a new volume) call it through the host.
func (s *quickScreen) redraw() {
	s.mu.Lock()
	f := s.invalidate
	s.mu.Unlock()
	if f != nil {
		f()
	}
}

// View draws the box at the width PiG gives it.
func (s *quickScreen) View(width int) kit.View {
	lines, shape := s.host.Frame(width, quickRows)
	return screenView(lines, shape, width)
}

func (s *quickScreen) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	msg, ok := keyMsg(data)
	if !ok {
		return sdk.RemoteComponentResult{}, nil
	}
	if s.host.Input(msg) {
		return sdk.RemoteComponentResult{Done: true}, nil
	}
	return sdk.RemoteComponentResult{}, nil
}

// HandleMouse gives the box a click or a turn of the wheel (in fullscreen, where Pi routes the mouse to components).
func (s *quickScreen) HandleMouse(e sdk.MouseEvent) (sdk.RemoteComponentResult, error) {
	msg, ok := mouseMsg(e)
	if !ok {
		return sdk.RemoteComponentResult{}, nil
	}
	return sdk.RemoteComponentResult{Done: s.host.Input(msg)}, nil
}

func (s *quickScreen) SetInvalidate(f func()) {
	s.mu.Lock()
	s.invalidate = f
	s.mu.Unlock()
}

func (s *quickScreen) Dispose() {}

// quickBox hands the open /music settings box the player's state when what the box shows of it changes: the track and
// whether it plays, its clock to the second, the volume, shuffle and repeat. It rides on follow's subscription, with no timer
// of its own: a closed box costs nothing, and an open one is drawn again about once a second while the music plays (the clock,
// as on the Player screen; the box has no motion for calm or low power to stop) and only on a change while it is paused or
// stopped.
type quickBox struct {
	mu   sync.Mutex
	host *teahost.Host // the box's, nil while none is open
	seen quickShown    // what it shows, as last handed to it
}

// quickShown is what the settings box shows of a player's state.
type quickShown struct {
	live, playing, paused, shuffle bool
	id, title, artists             string
	pos, dur                       time.Duration // to the second
	volume                         int
	repeat                         music.RepeatMode
}

func shownOf(st music.State) quickShown {
	s := quickShown{live: st.Connected || st.Track != nil, paused: st.Paused, shuffle: st.Shuffle, volume: st.Volume, repeat: st.Repeat}
	if t := st.Track; t != nil {
		s.playing, s.id, s.title, s.artists = true, t.ID, t.Title, strings.Join(t.Artists, ", ")
		s.pos, s.dur = st.Position.Round(time.Second), st.Duration.Round(time.Second)
	}
	return s
}

// open makes host the open box's, which shows st now.
func (q *quickBox) open(host *teahost.Host, st music.State) {
	q.mu.Lock()
	q.host, q.seen = host, shownOf(st)
	q.mu.Unlock()
}

// close forgets the box: it closed.
func (q *quickBox) close() {
	q.mu.Lock()
	q.host = nil
	q.mu.Unlock()
}

// state hands st to the open box when it shows something else than the box does.
func (q *quickBox) state(st music.State) {
	s := shownOf(st)
	q.mu.Lock()
	host := q.host
	if host == nil || s == q.seen {
		q.mu.Unlock()
		return
	}
	q.seen = s
	q.mu.Unlock()
	host.Send(ui.StateMsg(st))
}

// quickPort writes the quick screen's choices to settings.json and applies what can be applied at once.
type quickPort struct{ a *app }

var _ ui.QuickPort = quickPort{}

func (p quickPort) Get() (ui.QuickValues, error) {
	_, s, err := p.a.load()
	return ui.QuickValues{
		Engine: s.Engine, CookieBrowser: s.CookieBrowser, NowPlaying: s.NowPlaying, CoverArt: s.CoverArtOrDefault(),
		Palette: s.PaletteOrDefault(), Pulse: s.PulseOrDefault(), Calm: s.CalmOrDefault(), LowPower: s.LowPowerOrDefault(),
	}, err
}

func (p quickPort) Set(key, value string) (string, error) {
	paths, _, err := p.a.load()
	if err != nil {
		return "", err
	}
	var v any = value
	note := "takes effect after /reload"
	switch key {
	case "engine":
		note = "used from the next /music command; native needs the pigmusic program"
	case "cookieBrowser":
	case "nowPlaying":
		note = ""
	case "coverArt", "palette", "pulse", "calm", "lowPower":
		v, note = value == "true", "" // what the player draws and how it moves: applied to the running player below
	default:
		return "", fmt.Errorf("%q is not a setting this screen changes", key)
	}
	if err := music.UpdateSettingsValue(paths.Settings, key, v); err != nil {
		return "", err
	}
	switch key {
	case "coverArt":
		p.a.applyCoverArt()
	case "palette", "pulse", "calm", "lowPower":
		p.a.restyle()
	}
	if key == "nowPlaying" {
		p.a.foot.setMode(music.Settings{NowPlaying: value}.NowPlayingMode()) // the line moves now
	}
	return note, nil
}

// restyle tells the running player model the current look-and-motion settings, so a switch in the settings box shows at once.
func (a *app) restyle() {
	_, s, err := a.load()
	if err == nil {
		a.foot.calm.Store(s.CalmOrDefault()) // the status line's spinner too
	}
	a.mu.Lock()
	host := a.host
	a.mu.Unlock()
	if err != nil || host == nil {
		return // no player yet: it reads the settings when it starts
	}
	host.Send(ui.StyleMsg{Palette: s.PaletteOrDefault(), Pulse: s.PulseOrDefault(), Calm: s.CalmOrDefault(), LowPower: s.LowPowerOrDefault()})
}

// applyCoverArt hands the running player model the cover art source when the coverArt setting draws covers and takes it
// away when it does not, so the Player screen switches between the cover and the disc at once.
func (a *app) applyCoverArt() {
	_, s, err := a.load()
	a.mu.Lock()
	host, cover := a.host, a.art
	a.mu.Unlock()
	if err != nil || host == nil {
		return // no player yet: it reads the settings when it starts
	}
	if !s.CoverArtOrDefault() {
		cover = nil
	}
	host.Send(ui.ArtMsg{Art: cover})
}
