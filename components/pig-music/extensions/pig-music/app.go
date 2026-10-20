package pig_music

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/MichaelKinsy/pigpen/pig-music/art"
	"github.com/MichaelKinsy/pigpen/pig-music/cookies"
	"github.com/MichaelKinsy/pigpen/pig-music/doctor"
	"github.com/MichaelKinsy/pigpen/pig-music/lyrics"
	"github.com/MichaelKinsy/pigpen/pig-music/mpv"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
	"github.com/MichaelKinsy/pigpen/pig-music/native"
	"github.com/MichaelKinsy/pigpen/pig-music/prefetch"
	"github.com/MichaelKinsy/pigpen/pig-music/selfmanage"
	"github.com/MichaelKinsy/pigpen/pig-music/teahost"
	"github.com/MichaelKinsy/pigpen/pig-music/ui"
	"github.com/MichaelKinsy/pigpen/pig-music/ytdlp"
)

// deps are what the extension plays with. The zero value is the real thing: the
// environment, yt-dlp and mpv.
type deps struct {
	Getenv   func(string) string
	LookPath func(string) (string, error)
	// Source and Player replace yt-dlp and mpv (tests).
	Source music.Source
	Player music.Player
	// MPVEnv is added to mpv's environment when the player starts it (tests).
	MPVEnv []string
	MPVBin string
	// Doctor replaces the machine the doctor looks at (tests).
	Doctor *doctor.Env
	// Token stands for this PiG process; empty means processToken (tests give another PiG another token).
	Token string
	// Art replaces the cover art cache (tests: with a test Source there is no cache).
	Art music.Artwork
}

// app is the player as the extension holds it. It outlives the overlay: hiding
// the screen leaves the model, the player connection and the search results where
// they are, and /music shows them again. It lives as long as one generation of the
// extension: pig 0.4.0 runs a /reload (and a session switch) as a new generation in
// the same process, so session_shutdown releases the app (its mpv connection, model
// and footer) and the next generation's app attaches to the mpv that kept playing.
type app struct {
	d deps

	gone atomic.Bool // the generation ended (session_shutdown): nothing is built or written any more

	mu     sync.Mutex // guards player, host and art; held while they are built
	player music.Player
	host   *teahost.Host
	art    music.Artwork // the cover art source, whether the coverArt setting draws with it or not; nil when there is none

	report    *doctor.Report        // the doctor's findings at start, for explaining a 403
	installer *selfmanage.Installer // downloads and updates, nil when the source is injected
	access    cookies.Access        // the user's consent to the library reading a browser's cookies

	foot footer
	// drawn is host for readers that must not take mu: the widget's theme, read while ensure holds mu.
	drawn atomic.Pointer[teahost.Host]

	redrawMu sync.Mutex // guards redraw; the model's loop takes it, so it must never wait on mu
	redraw   func()

	box quickBox // the open /music settings box, which follow keeps up to date
}

func newApp(d deps) *app {
	if d.Getenv == nil {
		d.Getenv = os.Getenv
	}
	if d.LookPath == nil {
		d.LookPath = lookPath
	}
	a := &app{d: d}
	a.foot.theme = a.theme
	return a
}

// theme is the player's palette as kit theme overrides (kitview.go), nil before the player is built or when it is not painted.
func (a *app) theme() map[string]string {
	host := a.drawn.Load()
	if host == nil {
		return nil
	}
	var theme map[string]string
	host.Peek(func(m tea.Model) {
		if m, ok := m.(ui.Model); ok {
			theme = paletteTheme(m.Palette())
		}
	})
	return theme
}

// ensure builds the player and the model on first use. A missing program or a
// mpv that will not start is returned as an error with a message fit to show.
func (a *app) ensure(ctx context.Context) (*teahost.Host, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.gone.Load() {
		return nil, errEnded
	}
	if a.host != nil {
		if a.player.State().Connected {
			return a.host, nil
		}
		// mpv went away while the extension held it (pigmusic stop, a crash, a
		// kill): attach again, which starts a new mpv when none answers, and
		// follow its states. A failure is reported like a first start's.
		if err := attach(ctx, a.player); err != nil {
			return nil, err
		}
		a.follow(a.player, a.host)
		return a.host, nil
	}
	paths, settings, err := a.load()
	if err != nil {
		return nil, err
	}
	if err := a.refuseNativeEngine(settings); err != nil {
		return nil, err
	}
	a.foot.setEnabled(settings.FooterStatusOrDefault())
	a.foot.setMode(settings.NowPlayingMode())
	a.foot.calm.Store(settings.CalmOrDefault())
	mpvPath := first(a.d.Getenv("PIG_MUSIC_MPV"), settings.MPVPath, a.d.MPVBin)
	ytPath := first(a.d.Getenv("PIG_MUSIC_YTDLP"), settings.YtdlpPath)
	extra := strings.Fields(a.d.Getenv("PIG_MUSIC_MPV_ARGS"))
	source := a.d.Source
	if source != nil {
		saving := savingSource{Source: source, a: a}
		source = saving
		if es, ok := a.d.Source.(music.EntitySearcher); ok {
			source = savingEntitySource{saving, es}
		}
	}
	if source == nil {
		// The doctor looks before anything starts: a missing or too old program, or no way to make sound, is
		// refused here with one message and one fix each, instead of failing silently inside mpv.
		rep := doctor.Run(ctx, doctor.ConfigFrom(a.d.Getenv, settings, paths), a.doctorEnv(), doctor.Options{})
		if err := rep.Err(); err != nil {
			return nil, err
		}
		mpvPath, ytPath = rep.MPVPath, rep.YtdlpPath
		extra = append(extra, rep.MPVArgs()...)
		a.report = &rep
		a.installer = selfmanage.NewInstaller(paths.Data, a.doctorEnv())
		if rep.SelfManaged {
			// At most daily, in the background: yt-dlp breaks when YouTube changes and a stale copy is the usual cause.
			go a.installer.Update(context.Background(), rep.YtdlpPath, false) //nolint:errcheck // reported by the doctor's age check
		}
		access := cookies.Access{
			Setting: settings.CookieBrowser,
			Env:     doctor.CookieEnv(context.Background(), a.doctorEnv()),
			Consent: cookies.Consent{Path: doctor.ConsentPath(paths.Data)},
		}
		a.access = access
		// Cookies are asked for by library calls and the home's suggestions alone (see ytdlp.Source): search, enrichment, the
		// charts and mpv never carry them.
		source = a.guarded(&ytdlp.Source{Bin: ytPath, Cookies: access.Spec, JSRuntime: rep.JSRuntimeArg, Inner: native.NewSearcher(a.d.Getenv), Account: ytdlp.NewAccount(a.d.Getenv, ytPath, access.Spec), CacheDir: libraryCacheDir(paths.Data)})
	}
	player := a.d.Player
	if player == nil {
		cfg := mpv.Config{
			MPVPath: mpvPath, YtdlPath: ytPath, Paths: paths, PlayURL: source.PlayURL,
			ExtraArgs: extra, Env: a.d.MPVEnv, LookPath: a.d.LookPath,
			// A track whose stream YouTube refuses is resolved afresh and resumed where it stopped, once; refused again, it is
			// skipped and the reason shown.
			Recover: true, Unplayable: a.unplayable,
		}
		if a.d.Source == nil && !strings.EqualFold(a.d.Getenv("PIG_MUSIC_PREFETCH"), "off") {
			a.prefetching(&cfg, paths)
		}
		player = mpv.New(cfg)
	}
	if err := attach(ctx, player); err != nil {
		return nil, err
	}
	if lv, ok := player.(music.Levels); ok {
		// mpv outlives the extension: one that ended while measuring left its filter running. The model asks again when
		// it pulses.
		lctx, cancel := context.WithTimeout(ctx, time.Second)
		_ = lv.SetLevels(lctx, false)
		cancel()
	}
	if a.gone.Load() {
		// The session ended while mpv was being found (release could not take the lock): drop what was built.
		_ = player.Close()
		return nil, errEnded
	}
	// Cover art is a plain HTTPS fetch of the track's public thumbnail (no cookies), cached in the data directory; with a
	// test Source there is none, so no test reaches the network. coverArt:false leaves only the disc (and fetches nothing),
	// and the settings screen's switch hands the source to the running model or takes it away (quickPort.Set).
	switch {
	case a.d.Art != nil:
		a.art = a.d.Art
	case a.d.Source == nil && paths.Data != "":
		a.art = &art.Cache{Dir: filepath.Join(paths.Data, "art")}
	}
	var cover music.Artwork
	if settings.CoverArtOrDefault() {
		cover = a.art
	}
	var recents music.Recents
	if paths.Data != "" {
		recents = &music.RecentsFile{Path: music.RecentsPath(paths.Data)}
	}
	host := teahost.New(ui.New(ui.Deps{
		Source: source, Player: player, Settings: settingsPort{a}, Recents: recents,
		Art: cover, Lyrics: a.lyricsProvider(source, paths.Data), ArtMode: art.ModeFromEnv(a.d.Getenv), NoColor: art.NoColor(a.d.Getenv), Vibes: settings.PaletteOrDefault(), Pulse: settings.PulseOrDefault(), Calm: settings.CalmOrDefault(), LowPower: settings.LowPowerOrDefault(),
	}).Hidden(), teahost.Options{Redraw: a.requestRedraw, Draw: drawScreen})
	host.Start(80, 24)
	// No screen is open yet (session_start builds the player for the footer alone): nothing animates or measures until
	// the overlay opens and sends OpenedMsg.
	host.Send(ui.ClosedMsg{})
	a.follow(player, host)
	if st := player.State(); st.Track != nil || len(st.Queue) > 0 {
		host.Send(ui.ShowPlayerMsg{}) // an mpv that was already playing: show its queue, not an empty search
	}
	a.player, a.host = player, host
	a.drawn.Store(host)
	return host, nil
}

func attach(ctx context.Context, player music.Player) error {
	actx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return player.Attach(actx)
}

// follow hands the player's state to the model now and after every change, so the
// progress bar moves whether the screen is shown or not. The subscription ends
// when mpv goes away; the model then shows it stopped (the last state says so).
func (a *app) follow(player music.Player, host *teahost.Host) {
	host.Send(ui.StateMsg(player.State()))
	go func() {
		for st := range player.Subscribe() {
			host.Send(ui.StateMsg(st))
			a.foot.state(st)
			a.box.state(st)
		}
		last := player.State()
		host.Send(ui.StateMsg(last))
		a.foot.state(last)
		a.box.state(last)
	}()
}

// unplayable says why the player moved past a track whose stream YouTube refused (mpv.Config.Unplayable): on the player's
// status line, and as a PiG notification while the player is hidden.
func (a *app) unplayable(reason string) {
	if host := a.drawn.Load(); host != nil {
		host.Send(ui.NoticeMsg(reason))
	}
	a.foot.notify("pig-music: " + reason)
}

func first(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func (a *app) setRedraw(f func()) {
	a.redrawMu.Lock()
	a.redraw = f
	a.redrawMu.Unlock()
}

func (a *app) requestRedraw() {
	a.redrawMu.Lock()
	f := a.redraw
	a.redrawMu.Unlock()
	if f != nil {
		f()
	}
}

func lookPath(name string) (string, error) { return exec.LookPath(name) }

// refuseNativeEngine refuses to start the in-PiG player while the native engine is selected: the /music player and its quick
// commands only drive mpv, so running them anyway would silently ignore the choice.
func (a *app) refuseNativeEngine(s music.Settings) error {
	engine := first(a.d.Getenv("PIG_MUSIC_ENGINE"), s.Engine)
	if strings.EqualFold(strings.TrimSpace(engine), "native") {
		return errNativeEngine
	}
	return nil
}

var errNativeEngine = errors.New("@pi-in-go/pigpen-pig-music does not support the native engine in the /music player yet (engine is \"native\" in settings.json or PIG_MUSIC_ENGINE); it only drives mpv here. Set engine to \"mpv\" or \"auto\", or use the pigmusic program, which supports the native engine")

func (a *app) load() (music.Paths, music.Settings, error) {
	paths, err := music.DefaultPaths(a.d.Getenv)
	if err != nil {
		return paths, music.Settings{}, err
	}
	settings, err := music.LoadSettings(paths.Settings)
	return paths, settings, err
}

func (a *app) doctorEnv() doctor.Env {
	if a.d.Doctor != nil {
		return *a.d.Doctor
	}
	return doctor.EnvFrom(a.d.Getenv, a.d.LookPath)
}

// rememberSearch keeps the latest search where `/music play <number>` and `pigmusic play <number>` find it. A failure to keep it
// is no failure of the search.
func (a *app) rememberSearch(tracks []music.Track) {
	if paths, _, err := a.load(); err == nil {
		_ = music.SaveSearch(paths.Search, tracks)
	}
}

// savingSource is a Source handed in by a test, with the same memory of the last search as the real one.
type savingSource struct {
	music.Source
	a *app
}

// Watch and OpenPage pass through to a test source that has pages.
func (s savingSource) Watch(ctx context.Context, id string) (music.Watch, error) {
	if n, ok := s.Source.(music.Navigator); ok {
		return n.Watch(ctx, id)
	}
	return music.Watch{}, errNoPages
}

func (s savingSource) OpenPage(ctx context.Context, e music.Entity) (music.Page, error) {
	if n, ok := s.Source.(music.Navigator); ok {
		return n.OpenPage(ctx, e)
	}
	return music.Page{}, errNoPages
}

func (s savingSource) Search(ctx context.Context, q string, limit int) ([]music.Track, error) {
	t, err := s.Source.Search(ctx, q, limit)
	if err == nil {
		s.a.rememberSearch(t)
	}
	return t, err
}

// savingEntitySource is a savingSource over a test source that finds albums and artists too.
type savingEntitySource struct {
	savingSource
	music.EntitySearcher
}

// guarded wraps the source so that a 403 or a missing format, which is almost always an old yt-dlp or no JavaScript
// runtime, is reported as that, with the fix, and a pig-music-managed yt-dlp is updated.
func (a *app) guarded(s music.Source) music.Source { return guard{Source: s, a: a} }

type guard struct {
	music.Source
	a *app
}

func (g guard) Search(ctx context.Context, q string, limit int) ([]music.Track, error) {
	t, err := g.Source.Search(ctx, q, limit)
	if err == nil {
		g.a.rememberSearch(t)
	}
	return t, g.a.explain(err)
}

func (g guard) Library(ctx context.Context) ([]music.Collection, error) {
	c, err := g.Source.Library(ctx)
	return c, g.a.explain(err)
}

func (g guard) Tracks(ctx context.Context, id string) ([]music.Track, error) {
	t, err := g.Source.Tracks(ctx, id)
	return t, g.a.explain(err)
}

// Enrich passes through to the source when it can enrich, with the same explanation of a 403.
func (g guard) Enrich(ctx context.Context, t music.Track) (music.Track, error) {
	e, ok := g.Source.(music.Enricher)
	if !ok {
		return t, nil
	}
	got, err := e.Enrich(ctx, t)
	return got, g.a.explain(err)
}

// GrantCookieAccess records the user's yes, given in the Library tab, to reading the named browser's cookies for the
// library.
func (g guard) GrantCookieAccess(ctx context.Context, browser string) error {
	return g.a.access.Grant(ctx, browser)
}

// TracksPage passes through to a source that pages; one that does not lists everything as a single page.
func (g guard) TracksPage(ctx context.Context, id string, from, n int) ([]music.Track, bool, error) {
	if p, ok := g.Source.(music.TrackPager); ok {
		t, more, err := p.TracksPage(ctx, id, from, n)
		return t, more, g.a.explain(err)
	}
	all, err := g.Tracks(ctx, id)
	if err != nil || from >= len(all) {
		return nil, false, err
	}
	end := min(from+n, len(all))
	return all[from:end], end < len(all), nil
}

// Refresh makes a source that remembers the account ask it again.
func (g guard) Refresh() {
	if r, ok := g.Source.(music.Refresher); ok {
		r.Refresh()
	}
}

// CachedLibrary and CachedTracks pass through to a source that keeps the last session's listing on disk.
func (g guard) CachedLibrary(ctx context.Context) ([]music.Collection, bool) {
	if c, ok := g.Source.(music.LibraryCache); ok {
		return c.CachedLibrary(ctx)
	}
	return nil, false
}

func (g guard) CachedTracks(ctx context.Context, id string, n int) ([]music.Track, bool, bool) {
	if c, ok := g.Source.(music.LibraryCache); ok {
		return c.CachedTracks(ctx, id, n)
	}
	return nil, false, false
}

// SearchEntities and OpenEntity pass through to a source that lists albums and artists.
func (g guard) SearchEntities(ctx context.Context, q string, limit int) ([]music.Entity, error) {
	if e, ok := g.Source.(music.EntitySearcher); ok {
		return e.SearchEntities(ctx, q, limit)
	}
	return nil, nil
}

func (g guard) OpenEntity(ctx context.Context, e music.Entity) (string, []music.Track, error) {
	if o, ok := g.Source.(music.EntitySearcher); ok {
		t, tracks, err := o.OpenEntity(ctx, e)
		return t, tracks, g.a.explain(err)
	}
	return "", nil, errors.New("this source cannot open albums or artists")
}

// Watch and OpenPage (artist, album, radio and related pages) pass through to a source that has them.
func (g guard) Watch(ctx context.Context, id string) (music.Watch, error) {
	if n, ok := g.Source.(music.Navigator); ok {
		w, err := n.Watch(ctx, id)
		return w, g.a.explain(err)
	}
	return music.Watch{}, errNoPages
}

func (g guard) OpenPage(ctx context.Context, e music.Entity) (music.Page, error) {
	if n, ok := g.Source.(music.Navigator); ok {
		p, err := n.OpenPage(ctx, e)
		return p, g.a.explain(err)
	}
	return music.Page{}, errNoPages
}

// Popular, Suggested and CachedHome (the Search screen's home) pass through to a source that has them.
func (g guard) Popular(ctx context.Context) (music.Section, error) {
	if h, ok := g.Source.(music.HomeFeed); ok {
		sec, err := h.Popular(ctx)
		return sec, g.a.explain(err)
	}
	return music.Section{}, errors.New("this source has no charts")
}

func (g guard) Suggested(ctx context.Context) ([]music.Section, error) {
	if h, ok := g.Source.(music.HomeFeed); ok {
		return h.Suggested(ctx)
	}
	return nil, music.ErrNoAccount
}

func (g guard) CachedHome(ctx context.Context) (music.Section, []music.Section, bool) {
	if h, ok := g.Source.(music.HomeFeed); ok {
		return h.CachedHome(ctx)
	}
	return music.Section{}, nil, false
}

var errNoPages = errors.New("artist and album pages need the direct YouTube Music source (it is off: PIG_MUSIC_SEARCH=ytdlp)")

var (
	_ music.Navigator       = guard{}
	_ music.EntitySearcher  = guard{}
	_ music.LibraryCache    = guard{}
	_ music.Enricher        = guard{}
	_ music.CookieConsenter = guard{}
	_ music.TrackPager      = guard{}
	_ music.Refresher       = guard{}
	_ music.HomeFeed        = guard{}
)

func (a *app) explain(err error) error {
	if err == nil || !doctor.LooksLikeExtractionFailure(err.Error()) {
		return err
	}
	a.mu.Lock()
	rep, in := a.report, a.installer
	a.mu.Unlock()
	if rep == nil {
		return err
	}
	msg := doctor.Explain(rep.YtdlpVersion, rep.SelfManaged, rep.GOOS)
	if rep.SelfManaged && in != nil {
		// Right after a 403, but no more than once an hour; it runs in the background and can take a while.
		go in.Update(context.Background(), rep.YtdlpPath, true) //nolint:errcheck
		msg += " (pig-music is updating its yt-dlp now; try again in a minute)"
	}
	return fmt.Errorf("%s [%w]", msg, err)
}

// libraryCacheDir is where the library's metadata is kept between sessions ("" without a data directory: no cache).
func libraryCacheDir(data string) string {
	if data == "" {
		return ""
	}
	return filepath.Join(data, "library")
}

// prefetchDelay is how long a new next entry waits before its stream is resolved, so that the track that is starting gets the
// network and the CPU first (a var for tests).
var prefetchDelay = 4 * time.Second

// prefetching makes mpv ask the prefetch cache first for the stream of each track (mpv's yt-dlp is replaced by a script that
// answers from it, see package prefetch) and has the entry after the current one resolved while the current plays. Where the
// script cannot be written (Windows has no shell) mpv keeps using yt-dlp as it is, and nothing is prefetched.
func (a *app) prefetching(cfg *mpv.Config, paths music.Paths) {
	if cfg.YtdlPath == "" {
		return
	}
	cache := &prefetch.Cache{Dir: filepath.Join(paths.Dir, "ytdl"), Bin: cfg.YtdlPath, Runner: ytdlp.ExecRunner}
	if a.report != nil {
		cache.JSRuntime = a.report.JSRuntimeArg // what mpv's hook is given (doctor.Report.MPVArgs)
	}
	shim, err := cache.Shim()
	if err != nil {
		return
	}
	cfg.YtdlPath = shim
	var latest atomic.Value // the URL offered last: only the entry that is still next when the delay is over is resolved
	cfg.Prefetch = func(url string) {
		latest.Store(url)
		time.Sleep(prefetchDelay)
		if a.gone.Load() || latest.Load() != url {
			return // next was pressed, the queue shuffled or edited: a later offer resolves what is next now
		}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		_ = cache.Warm(ctx, url) // a failure costs only what there was before: mpv resolves the stream itself
	}
	cfg.StreamFailed = cache.Forget // a refused stream (another network since it was resolved, or expired) is resolved afresh
}

// lyricsProvider finds lyrics for the Player screen: LRCLIB, then YouTube Music's own when the direct source is on. It asks
// nothing until the lyrics are on screen. PIG_MUSIC_LYRICS=off, a test source or no data directory: no lyrics view.
func (a *app) lyricsProvider(source music.Source, data string) music.LyricsProvider {
	if a.d.Source != nil || data == "" || strings.EqualFold(strings.TrimSpace(a.d.Getenv("PIG_MUSIC_LYRICS")), "off") {
		return nil
	}
	svc := &lyrics.Service{Dir: filepath.Join(data, "lyrics")}
	if yl, ok := innerOf(source).(interface {
		YouTubeLyrics(ctx context.Context, videoID string) (string, error)
	}); ok {
		svc.Fallback = yl.YouTubeLyrics
	}
	return svc
}

// innerOf is the direct YouTube Music source behind a guarded yt-dlp source, nil when there is none.
func innerOf(source music.Source) music.Source {
	if g, ok := source.(guard); ok {
		source = g.Source
	}
	if y, ok := source.(*ytdlp.Source); ok {
		return y.Inner
	}
	return nil
}
