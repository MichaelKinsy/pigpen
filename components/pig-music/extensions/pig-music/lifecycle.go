package pig_music

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/pig-music/mpv"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
	"github.com/MichaelKinsy/pigpen/pig-music/ui"
)

// statusKey is the footer slot pig-music writes while its screen is hidden.
const statusKey = "pig-music"

const maxStatusTitle = 60

// statusText is the footer line for a state: the track, whether it plays, and where it is. Nothing playing is "".
// spinner is the compact disc of the status line.
var spinner = []rune{'|', '/', '-', '\\'}

func statusText(st music.State) string { return statusLine(st, false) }

// statusLine is statusText; with calm (no motion) the mark of a playing track stands still as ">".
func statusLine(st music.State, calm bool) string {
	if !st.Connected || st.Track == nil {
		return ""
	}
	// A record spinner while playing, by the whole second of the position, so the line changes no more often than the
	// position in it already does; paused, it stands still as "||".
	mark := string(spinner[int(st.Position/time.Second)%len(spinner)])
	switch {
	case st.Paused:
		mark = "||"
	case calm:
		mark = ">"
	}
	title := ui.Clean(st.Track.Title)
	if len(st.Track.Artists) > 0 {
		title += " - " + ui.Clean(strings.Join(st.Track.Artists, ", "))
	}
	if utf8.RuneCountInString(title) > maxStatusTitle {
		title = string([]rune(title)[:maxStatusTitle-1]) + "~"
	}
	return fmt.Sprintf("%s %s %s/%s", mark, title, clock(st.Position), clock(st.Duration))
}

func clock(d time.Duration) string {
	s := int(d.Round(time.Second) / time.Second)
	if s < 0 {
		s = 0
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// footer writes the status line from player states, while the screen is hidden. The Context comes from a handler that
// has returned (the host keeps answering its calls), which is how a status can change with no command running.
type footer struct {
	mu      sync.Mutex
	ctx     *sdk.Context
	shown   bool // the screen is open: the footer is cleared and left alone
	last    string
	enabled bool
	ended   bool // the generation ended: its Context is dropped and nothing is written
	widget  bool // nowPlaying is "widget": the line is a one-line widget above the editor instead of a footer status
	cur     music.State
	calm    atomic.Bool // the calm setting: the spinner stands still
}

// text is the line for st, as the calm setting has it.
func (f *footer) text(st music.State) string { return statusLine(st, f.calm.Load()) }

// widgetKey names the now-playing widget.
const widgetKey = "pig-music"

// release drops the Context: after session_shutdown it belongs to a generation the host has let go.
func (f *footer) release() {
	f.mu.Lock()
	f.ctx, f.ended = nil, true
	f.mu.Unlock()
}

func (f *footer) setEnabled(on bool) {
	f.mu.Lock()
	f.enabled = on
	f.mu.Unlock()
}

func (f *footer) capture(ctx sdk.Context) {
	f.mu.Lock()
	if !f.ended {
		f.ctx = &ctx
	}
	f.mu.Unlock()
}

// setMode picks where the line goes: "widget" or anything else (the footer status). A change clears the old place and writes
// the current line in the new one.
func (f *footer) setMode(mode string) {
	f.mu.Lock()
	widget := mode == "widget"
	if f.widget == widget {
		f.mu.Unlock()
		return
	}
	ctx := f.ctx
	f.widget, f.last = widget, ""
	shown, cur := f.shown, f.cur
	f.mu.Unlock()
	if ctx != nil { // the place it leaves is emptied
		if widget {
			ctx.SetStatus(statusKey, "")
		} else {
			_ = ctx.SetWidget(widgetKey, nil)
		}
	}
	if !shown {
		f.set(f.text(cur))
	}
}

// set writes text unless it is what the line already says. The caller holds no lock: the call goes to the host.
func (f *footer) set(text string) {
	f.mu.Lock()
	ctx, last, ok, widget := f.ctx, f.last, f.enabled, f.widget
	if ctx == nil || text == last || (!ok && text != "") {
		f.mu.Unlock()
		return
	}
	f.last = text
	f.mu.Unlock()
	switch {
	case !widget:
		ctx.SetStatus(statusKey, text)
	case text == "":
		_ = ctx.SetWidget(widgetKey, nil)
	default:
		_ = ctx.SetWidget(widgetKey, []string{text}) // one line; widget_push needs no answer from the host
	}
}

// state is a player state: shown while hidden.
func (f *footer) state(st music.State) {
	f.mu.Lock()
	shown := f.shown
	f.cur = st
	f.mu.Unlock()
	if !shown {
		f.set(f.text(st))
	}
}

// screen is called as the screen opens (true: clear the footer) and closes (false: show the current state).
func (f *footer) screen(open bool, current music.State) {
	f.mu.Lock()
	f.shown, f.cur = open, current
	f.mu.Unlock()
	if open {
		f.set("")
		return
	}
	f.set(f.text(current))
}

// running reports whether an mpv answers on the player's socket, without starting anything.
func (a *app) running(ctx context.Context) bool {
	paths, _, err := a.load()
	return err == nil && mpv.Running(ctx, paths.Socket)
}

// onSessionStart keeps the footer right after a /reload or a restart: an mpv that is still playing is attached, so its
// track shows in the footer without a /music.
func (a *app) onSessionStart(ctx sdk.Context) {
	a.foot.capture(ctx)
	if !ctx.HasUI() || ctx.Mode() != "tui" {
		return
	}
	go func() {
		bg := context.Background()
		if a.d.Player == nil && !a.running(bg) {
			return // nothing to follow, and starting mpv is /music's job
		}
		_, _ = a.ensure(bg) // a failure is left for /music to report
	}()
}

// stopMPV ends the mpv this user has playing, whether or not this process attached to it. It reports whether one was running.
func (a *app) stopMPV(ctx context.Context) (bool, error) {
	a.mu.Lock()
	player := a.player
	a.mu.Unlock()
	if player == nil {
		player = a.d.Player
	}
	if player == nil {
		paths, _, err := a.load()
		if err != nil {
			return false, err
		}
		if !mpv.Running(ctx, paths.Socket) {
			return false, nil
		}
		p := mpv.New(mpv.Config{Paths: paths})
		defer p.Close()
		if err := p.Attach(ctx); err != nil {
			return false, err
		}
		return true, p.Shutdown(ctx)
	}
	if !player.State().Connected {
		// Attach would start an mpv when none answers, which is the opposite of what is wanted here.
		if !a.running(ctx) {
			return false, nil
		}
		if err := player.Attach(ctx); err != nil {
			return false, err
		}
	}
	return true, player.Shutdown(ctx)
}

// onSessionShutdown ends this generation of the extension (see app) and, when PiG quits, stops the music if stopOnExit is
// on (the default) and this PiG owns the music. A reload, or a switch to a new, resumed or forked session, keeps playing:
// it is the same PiG. Another PiG, or a print, JSON or RPC run (`pig -p`), never opened the player and so does not own it.
func (a *app) onSessionShutdown(data map[string]any) {
	defer a.release()
	if reason, _ := data["reason"].(string); reason != "quit" {
		return
	}
	_, settings, err := a.load()
	if err != nil || !settings.StopOnExitOrDefault() || !a.ownsMusic() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if stopped, err := a.stopMPV(ctx); stopped && err == nil { // PiG is exiting; there is nobody left to tell an error
		a.disown()
	}
}

// errEnded is what ensure says after session_shutdown.
var errEnded = errors.New("pig-music: this session has ended")

// release lets go of everything this generation holds, and leaves mpv playing: the footer's Context, the model's loop and
// the mpv connection (whose follower goroutine ends with it). A build still in progress in ensure sees gone and drops what
// it built.
func (a *app) release() {
	a.foot.release()
	a.gone.Store(true)
	if !a.mu.TryLock() {
		return // ensure holds the lock; it checks gone before it keeps anything
	}
	player, host := a.player, a.host
	a.player, a.host = nil, nil
	a.mu.Unlock()
	if host != nil {
		host.Stop()
	}
	if player != nil {
		_ = player.Close()
	}
}

// processToken stands for this PiG process. Package state outlives a generation (pig 0.4.0 runs a /reload in the same
// process, and a Piglet Binary runs the extension inside PiG itself), so a /reload keeps it and another PiG has its own.
var processToken = newToken()

func newToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("pid-%d-%d", os.Getpid(), time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func (a *app) token() string {
	if a.d.Token != "" {
		return a.d.Token
	}
	return processToken
}

func ownerFile(p music.Paths) string { return filepath.Join(p.Dir, "owner") }

// claim records that this PiG owns the music: it opened the player, so its quit stops it (stopOnExit). The last PiG to
// open the player owns it. The file holds a random token, nothing else, mode 0600, next to mpv's socket.
func (a *app) claim() {
	paths, _, err := a.load()
	if err != nil || paths.EnsureDir() != nil {
		return
	}
	tmp, err := os.CreateTemp(paths.Dir, ".owner-*")
	if err != nil {
		return
	}
	_, werr := tmp.WriteString(a.token())
	if cerr := tmp.Close(); werr != nil || cerr != nil {
		_ = os.Remove(tmp.Name())
		return
	}
	if os.Rename(tmp.Name(), ownerFile(paths)) != nil {
		_ = os.Remove(tmp.Name())
	}
}

func (a *app) ownsMusic() bool {
	paths, _, err := a.load()
	if err != nil {
		return false
	}
	b, err := os.ReadFile(ownerFile(paths))
	return err == nil && strings.TrimSpace(string(b)) == a.token()
}

// disown forgets the owner once the music has stopped.
func (a *app) disown() {
	if paths, _, err := a.load(); err == nil {
		_ = os.Remove(ownerFile(paths))
	}
}

func (a *app) currentState() music.State {
	a.mu.Lock()
	p := a.player
	a.mu.Unlock()
	if p == nil {
		return music.State{Index: -1}
	}
	return p.State()
}

// settingsPort is the settings screen's access to settings.json and to what the doctor found at start.
type settingsPort struct{ a *app }

func (p settingsPort) Get() (bool, bool, error) {
	_, s, err := p.a.load()
	return s.StopOnExitOrDefault(), s.FooterStatusOrDefault(), err
}

// Set writes the switch to the settings file (keeping every other key) and applies it now: a footer switched off is cleared.
func (p settingsPort) Set(key string, on bool) error {
	paths, _, err := p.a.load()
	if err != nil {
		return err
	}
	if err := music.UpdateSettingsFile(paths.Settings, key, on); err != nil {
		return err
	}
	if key == "footerStatus" {
		p.a.foot.setEnabled(on)
		if !on {
			p.a.foot.set("")
		}
	}
	return nil
}

func (p settingsPort) Info() []string {
	p.a.mu.Lock()
	rep := p.a.report
	p.a.mu.Unlock()
	if rep == nil {
		return nil
	}
	var out []string
	for _, id := range []string{"mpv", "yt-dlp", "js-runtime", "library"} {
		if c, ok := rep.Check(id); ok {
			out = append(out, c.Name+": "+c.Detail)
		}
	}
	return out
}
