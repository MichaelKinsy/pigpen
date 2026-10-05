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

	"github.com/MichaelKinsy/pigpen/pig-music/art"
	"github.com/MichaelKinsy/pigpen/pig-music/cookies"
	"github.com/MichaelKinsy/pigpen/pig-music/doctor"
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

	mu     sync.Mutex // guards player and host; held while they are built
	player music.Player
	host   *teahost.Host

	report    *doctor.Report        // the doctor's findings at start, for explaining a 403
	installer *selfmanage.Installer // downloads and updates, nil when the source is injected
	access    cookies.Access        // the user's consent to the library reading a browser's cookies

	foot footer

	redrawMu sync.Mutex // guards redraw; the model's loop takes it, so it must never wait on mu
	redraw   func()
}

func newApp(d deps) *app {
	if d.Getenv == nil {
		d.Getenv = os.Getenv
	}
	if d.LookPath == nil {
		d.LookPath = lookPath
	}
	return &app{d: d}
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
	a.foot.setEnabled(settings.FooterStatusOrDefault())
	a.foot.setMode(settings.NowPlayingMode())
	a.foot.calm.Store(settings.CalmOrDefault())
	mpvPath := first(a.d.Getenv("PIG_MUSIC_MPV"), settings.MPVPath, a.d.MPVBin)
	ytPath := first(a.d.Getenv("PIG_MUSIC_YTDLP"), settings.YtdlpPath)
	extra := strings.Fields(a.d.Getenv("PIG_MUSIC_MPV_ARGS"))
	source := a.d.Source
	if source != nil {
		source = savingSource{Source: source, a: a}
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
		// Cookies are asked for by library calls alone (see ytdlp.Source): search, enrichment and mpv never carry them.
		source = a.guarded(&ytdlp.Source{Bin: ytPath, Cookies: access.Spec, JSRuntime: rep.JSRuntimeArg, Inner: native.NewSearcher(a.d.Getenv), Account: ytdlp.NewAccount(a.d.Getenv, ytPath, access.Spec), CacheDir: libraryCacheDir(paths.Data)})
	}
	player := a.d.Player
	if player == nil {
		cfg := mpv.Config{
			MPVPath: mpvPath, YtdlPath: ytPath, Paths: paths, PlayURL: source.PlayURL,
			ExtraArgs: extra, Env: a.d.MPVEnv, LookPath: a.d.LookPath,
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
	// test Source there is none, so no test reaches the network. coverArt:false leaves only the disc.
	var cover music.Artwork
	if a.d.Source == nil && settings.CoverArtOrDefault() && paths.Data != "" {
		cover = &art.Cache{Dir: filepath.Join(paths.Data, "art")}
	}
	host := teahost.New(ui.New(ui.Deps{
		Source: source, Player: player, Settings: settingsPort{a},
		Art: cover, ArtMode: art.ModeFromEnv(a.d.Getenv), NoColor: art.NoColor(a.d.Getenv), Vibes: settings.PaletteOrDefault(), Pulse: settings.PulseOrDefault(), Calm: settings.CalmOrDefault(), LowPower: settings.LowPowerOrDefault(),
	}), teahost.Options{Redraw: a.requestRedraw})
	host.Start(80, 24)
	// No screen is open yet (session_start builds the player for the footer alone): nothing animates or measures until
	// the overlay opens and sends OpenedMsg.
	host.Send(ui.ClosedMsg{})
	a.follow(player, host)
	if st := player.State(); st.Track != nil || len(st.Queue) > 0 {
		host.Send(ui.ShowPlayerMsg{}) // an mpv that was already playing: show its queue, not an empty search
	}
	a.player, a.host = player, host
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
		}
		last := player.State()
		host.Send(ui.StateMsg(last))
		a.foot.state(last)
	}()
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

func (s savingSource) Search(ctx context.Context, q string, limit int) ([]music.Track, error) {
	t, err := s.Source.Search(ctx, q, limit)
	if err == nil {
		s.a.rememberSearch(t)
	}
	return t, err
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

var (
	_ music.EntitySearcher  = guard{}
	_ music.LibraryCache    = guard{}
	_ music.Enricher        = guard{}
	_ music.CookieConsenter = guard{}
	_ music.TrackPager      = guard{}
	_ music.Refresher       = guard{}
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
	cfg.StreamFailed = cache.Forget // a refused stream (another network since it was resolved) is resolved afresh next time
}
