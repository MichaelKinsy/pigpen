package pig_music

import (
	"fmt"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
	"github.com/MichaelKinsy/pigpen/pig-music/teahost"
	"github.com/MichaelKinsy/pigpen/pig-music/ui"
)

// quickRows is the height of the /music settings box: its heading, the now-playing line, the seven rows, a note and the hints.
const quickRows = 19

// quickScreen is the sdk.RemoteComponent of /music settings. Unlike the player it owns its model: the screen is made when
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

func (s *quickScreen) Render(width int) []string { return s.host.Lines(width, quickRows) }

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

func (s *quickScreen) SetInvalidate(f func()) {
	s.mu.Lock()
	s.invalidate = f
	s.mu.Unlock()
}

func (s *quickScreen) Dispose() {}

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
		note = "pigmusic only for now: the /music player and its quick commands use mpv"
	case "cookieBrowser":
	case "nowPlaying":
		note = ""
	case "coverArt":
		v = value == "true"
	case "palette", "pulse", "calm", "lowPower":
		v, note = value == "true", "" // how it looks and moves: applied to the running player below
	default:
		return "", fmt.Errorf("%q is not a setting this screen changes", key)
	}
	if err := music.UpdateSettingsValue(paths.Settings, key, v); err != nil {
		return "", err
	}
	switch key {
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
