package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// ── fakes ───────────────────────────────────────────────────────────────────

type stubPlayer struct {
	mu    sync.Mutex
	calls []string
	err   error
	state music.State
	// lastReplace is the track list of the latest Replace.
	lastReplace []music.Track
}

func (p *stubPlayer) rec(format string, a ...any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, fmt.Sprintf(format, a...))
	return p.err
}
func (p *stubPlayer) log() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...)
}
func (p *stubPlayer) Attach(context.Context) error { return nil }
func (p *stubPlayer) Replace(_ context.Context, ts []music.Track, at int) error {
	p.lastReplace = ts
	return p.rec("replace %d tracks at %d first=%s", len(ts), at, ts[0].ID)
}
func (p *stubPlayer) Enqueue(_ context.Context, ts ...music.Track) error {
	return p.rec("enqueue %s", ts[0].ID)
}
func (p *stubPlayer) Remove(_ context.Context, i int) error     { return p.rec("remove %d", i) }
func (p *stubPlayer) Move(_ context.Context, a, b int) error    { return p.rec("move %d %d", a, b) }
func (p *stubPlayer) Jump(_ context.Context, i int) error       { return p.rec("jump %d", i) }
func (p *stubPlayer) Next(context.Context) error                { return p.rec("next") }
func (p *stubPlayer) Prev(context.Context) error                { return p.rec("prev") }
func (p *stubPlayer) TogglePause(context.Context) error         { return p.rec("toggle") }
func (p *stubPlayer) SetPaused(_ context.Context, b bool) error { return p.rec("paused %v", b) }
func (p *stubPlayer) SeekRelative(_ context.Context, d time.Duration) error {
	return p.rec("seek %s", d)
}
func (p *stubPlayer) SetVolume(_ context.Context, v int) error { return p.rec("volume %d", v) }
func (p *stubPlayer) SetShuffle(_ context.Context, on bool) error {
	return p.rec("shuffle %v", on)
}
func (p *stubPlayer) SetRepeat(_ context.Context, m music.RepeatMode) error {
	return p.rec("repeat %q", string(m))
}
func (p *stubPlayer) State() music.State                        { return p.state }
func (p *stubPlayer) Sync(context.Context) (music.State, error) { return p.state, nil }
func (p *stubPlayer) Subscribe() <-chan music.State             { return nil }
func (p *stubPlayer) Close() error                              { return nil }
func (p *stubPlayer) Shutdown(context.Context) error            { return nil }

type stubSource struct {
	tracks []music.Track
	err    error
	asked  []string
}

func (s *stubSource) Search(_ context.Context, q string, limit int) ([]music.Track, error) {
	s.asked = append(s.asked, fmt.Sprintf("%s|%d", q, limit))
	return s.tracks, s.err
}
func (s *stubSource) Library(context.Context) ([]music.Collection, error) {
	return nil, errors.New("this needs your YouTube account: set cookieBrowser in the pig-music settings (not configured)")
}
func (s *stubSource) Tracks(context.Context, string) ([]music.Track, error) { return nil, nil }
func (s *stubSource) PlayURL(t music.Track) string                          { return "u/" + t.ID }

func tracks(n int) []music.Track {
	out := make([]music.Track, n)
	for i := range out {
		out[i] = music.Track{ID: fmt.Sprintf("id%02d", i), Title: fmt.Sprintf("Song %d", i+1), Artists: []string{"Artist"}, Duration: time.Duration(150+i) * time.Second}
	}
	return out
}

func init() {
	// the rig runs commands inline, so a real tick chain would never end: ticks are asked for and never delivered
	defaultTick = func(time.Duration) tea.Cmd { return func() tea.Msg { return nil } }
}

// rig drives a model the way the host does: messages in, commands run inline.
type rig struct {
	t      *testing.T
	m      tea.Model
	player *stubPlayer
	source *stubSource
	quit   bool
}

func newRig(t *testing.T, w, h int) *rig {
	t.Helper()
	r := &rig{t: t, player: &stubPlayer{}, source: &stubSource{tracks: tracks(5)}}
	r.m = New(Deps{Source: r.source, Player: r.player})
	r.send(tea.WindowSizeMsg{Width: w, Height: h})
	return r
}

// send delivers msg and then everything its commands produce.
func (r *rig) send(msg tea.Msg) {
	r.t.Helper()
	queue := []tea.Msg{msg}
	for len(queue) > 0 {
		msg, queue = queue[0], queue[1:]
		if _, ok := msg.(tea.QuitMsg); ok {
			r.quit = true
			continue
		}
		var cmd tea.Cmd
		r.m, cmd = r.m.Update(msg)
		if cmd == nil {
			continue
		}
		out := cmd()
		if batch, ok := out.(tea.BatchMsg); ok {
			for _, c := range batch {
				if c != nil {
					queue = append(queue, c())
				}
			}
		} else if out != nil {
			queue = append(queue, out)
		}
	}
}

func (r *rig) key(names ...string) {
	r.t.Helper()
	for _, n := range names {
		r.send(tea.KeyPressMsg(keyOf(n)))
	}
}

func (r *rig) typed(s string) {
	for _, c := range s {
		r.send(tea.KeyPressMsg(tea.Key{Code: c, Text: string(c)}))
	}
}

func keyOf(name string) tea.Key {
	switch name {
	case "space":
		return tea.Key{Code: tea.KeySpace, Text: " "}
	case "up":
		return tea.Key{Code: tea.KeyUp}
	case "down":
		return tea.Key{Code: tea.KeyDown}
	case "left":
		return tea.Key{Code: tea.KeyLeft}
	case "right":
		return tea.Key{Code: tea.KeyRight}
	case "enter":
		return tea.Key{Code: tea.KeyEnter}
	case "esc":
		return tea.Key{Code: tea.KeyEscape}
	case "tab":
		return tea.Key{Code: tea.KeyTab}
	case "shift+tab":
		return tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}
	case "backspace":
		return tea.Key{Code: tea.KeyBackspace}
	case "ctrl+c":
		return tea.Key{Code: 'c', Mod: tea.ModCtrl}
	}
	r := []rune(name)
	if len(r) != 1 {
		panic("keyOf: " + name)
	}
	return tea.Key{Code: r[0], Text: name}
}

func (r *rig) state(s music.State) { r.send(StateMsg(s)) }

func (r *rig) screen() []string {
	return strings.Split(r.m.View().Content, "\n")
}

func (r *rig) text() string { return ansi.Strip(strings.Join(r.screen(), "\n")) }

func playing(ts []music.Track, idx int) music.State {
	cur := ts[idx]
	return music.State{Connected: true, Track: &cur, Position: 50 * time.Second, Duration: 176 * time.Second, Volume: 40, Queue: ts, Index: idx}
}

// ── layout ──────────────────────────────────────────────────────────────────

func TestEveryScreenFillsTheTerminalExactlyAtEverySize(t *testing.T) {
	sizes := [][2]int{{120, 40}, {80, 24}, {60, 16}, {40, 12}, {30, 8}, {24, 6}, {20, 5}, {10, 3}, {1, 1}, {200, 10}, {12, 60}}
	for _, sz := range sizes {
		w, h := sz[0], sz[1]
		for _, tab := range []string{"search", "library", "player"} {
			r := newRig(t, w, h)
			r.state(playing(tracks(30), 3))
			r.key("/")
			r.typed("night 日本語 drive")
			r.key("enter")
			switch tab {
			case "library":
				r.key("esc", "tab")
			case "player":
				r.key("esc", "tab", "tab")
			}
			lines := r.screen()
			if len(lines) != h {
				t.Errorf("%s %dx%d: %d lines", tab, w, h, len(lines))
			}
			for i, l := range lines {
				if cw := ansi.StringWidth(l); cw > w {
					t.Errorf("%s %dx%d: line %d is %d cells: %q", tab, w, h, i, cw, ansi.Strip(l))
				}
			}
		}
	}
}

func TestTooSmallATerminalSaysSo(t *testing.T) {
	r := newRig(t, 18, 4)
	if txt := r.text(); !strings.Contains(txt, "too small") {
		t.Fatalf("screen:\n%s", txt)
	}
}

func TestWideCharactersAreClippedByCellsNotBytes(t *testing.T) {
	r := newRig(t, 50, 14)
	r.source.tracks = []music.Track{{ID: "a", Title: strings.Repeat("日本語のタイトル", 6), Artists: []string{"アーティスト"}}}
	r.key("/")
	r.typed("x")
	r.key("enter")
	for i, l := range r.screen() {
		if ansi.StringWidth(l) > 50 {
			t.Fatalf("line %d is %d cells", i, ansi.StringWidth(l))
		}
	}
}

// ── tabs, help, quit ────────────────────────────────────────────────────────

func TestTabCyclesTheThreeScreensBothWays(t *testing.T) {
	r := newRig(t, 100, 30)
	order := []string{"search", "library", "player", "search"}
	for i := 1; i < len(order); i++ {
		r.key("tab")
		if got := r.m.(Model).Tab(); got != order[i] {
			t.Fatalf("after %d tabs: %s, want %s", i, got, order[i])
		}
	}
	r.key("shift+tab")
	if got := r.m.(Model).Tab(); got != "player" {
		t.Fatalf("shift+tab: %s", got)
	}
}

func TestQuitKeysHideButNotWhileTyping(t *testing.T) {
	for _, k := range []string{"q", "esc", "ctrl+c"} {
		r := newRig(t, 100, 30)
		r.key(k)
		if !r.quit {
			t.Errorf("%s did not quit", k)
		}
	}
	r := newRig(t, 100, 30)
	r.key("/")
	r.typed("quiet")
	if r.quit || !strings.Contains(r.text(), "quiet") {
		t.Fatalf("typing q quit or was lost:\n%s", r.text())
	}
	r.key("esc")
	if r.quit {
		t.Fatal("esc in the input closed the player instead of the input")
	}
	r.key("esc")
	if !r.quit {
		t.Fatal("esc outside the input did not quit")
	}
	r = newRig(t, 100, 30)
	r.key("/")
	r.key("ctrl+c")
	if !r.quit {
		t.Fatal("ctrl+c must quit even in the input")
	}
}

func TestHelpListsTheKeysAndEscClosesItBeforeQuitting(t *testing.T) {
	r := newRig(t, 100, 30)
	r.key("?")
	txt := r.text()
	for _, want := range []string{"space", "play / pause", "next", "seek", "volume", "tab", "enter", "remove", "reorder", "search", "quit"} {
		if !strings.Contains(txt, want) {
			t.Errorf("help lacks %q:\n%s", want, txt)
		}
	}
	r.key("esc")
	if r.quit || strings.Contains(r.text(), "play / pause") {
		t.Fatal("esc did not just close the help")
	}
	r.key("?", "?")
	if strings.Contains(r.text(), "play / pause") {
		t.Fatal("? did not toggle the help off")
	}
}

// ── transport keys ──────────────────────────────────────────────────────────

func TestTransportKeys(t *testing.T) {
	cases := []struct {
		keys []string
		want string
	}{
		{[]string{"space"}, "toggle"},
		{[]string{"n"}, "next"},
		{[]string{"p"}, "prev"},
		{[]string{"right"}, "seek 5s"},
		{[]string{"left"}, "seek -5s"},
		{[]string{"+"}, "volume 45"},
		{[]string{"="}, "volume 45"},
		{[]string{"-"}, "volume 35"},
	}
	for _, c := range cases {
		r := newRig(t, 100, 30)
		r.state(playing(tracks(3), 0)) // volume 40
		r.key("tab", "tab")            // the player screen: left and right are not list keys there
		r.key(c.keys...)
		if log := r.player.log(); len(log) != 1 || log[0] != c.want {
			t.Errorf("%v: calls %v, want %q", c.keys, log, c.want)
		}
	}
}

func TestVolumeStaysBetweenZeroAndOneHundred(t *testing.T) {
	r := newRig(t, 100, 30)
	s := playing(tracks(1), 0)
	s.Volume = 98
	r.state(s)
	r.key("+")
	s.Volume = 2
	r.state(s)
	r.key("-")
	if log := r.player.log(); log[0] != "volume 100" || log[1] != "volume 0" {
		t.Fatalf("%v", log)
	}
}

func TestAFailedCommandShowsItsErrorAndTheNextKeyClearsIt(t *testing.T) {
	r := newRig(t, 100, 30)
	r.player.err = errors.New("already at the end of the queue")
	r.key("n")
	if !strings.Contains(r.text(), "already at the end of the queue") {
		t.Fatalf("no error on screen:\n%s", r.text())
	}
	r.player.err = nil
	r.key("p")
	if strings.Contains(r.text(), "already at the end") {
		t.Fatal("the error stayed after the next key")
	}
}

// ── search ──────────────────────────────────────────────────────────────────

func TestSearchTypesSubmitsAndListsResults(t *testing.T) {
	r := newRig(t, 100, 30)
	r.key("/")
	r.typed("night drive")
	r.key("backspace")
	r.typed("e")
	if !strings.Contains(r.text(), "night drive") {
		t.Fatalf("query not shown:\n%s", r.text())
	}
	r.key("enter")
	if len(r.source.asked) != 1 || r.source.asked[0] != "night drive|20" {
		t.Fatalf("searched %v", r.source.asked)
	}
	txt := r.text()
	for _, want := range []string{"Song 1", "Song 5", "Artist", "2:30"} {
		if !strings.Contains(txt, want) {
			t.Errorf("results lack %q:\n%s", want, txt)
		}
	}
}

func TestSearchFromAnotherScreenSwitchesToIt(t *testing.T) {
	r := newRig(t, 100, 30)
	r.key("tab", "tab")
	r.key("/")
	if r.m.(Model).Tab() != "search" || !r.m.(Model).Typing() {
		t.Fatalf("tab %s typing %v", r.m.(Model).Tab(), r.m.(Model).Typing())
	}
}

func TestAnEmptyQueryIsNotSearched(t *testing.T) {
	r := newRig(t, 100, 30)
	r.key("/", "enter")
	if len(r.source.asked) != 0 {
		t.Fatalf("searched %v", r.source.asked)
	}
}

func TestSearchErrorsAndEmptyResultsAreShown(t *testing.T) {
	r := newRig(t, 100, 30)
	r.source.err = errors.New("yt-dlp: ERROR: HTTP Error 429")
	r.key("/")
	r.typed("x")
	r.key("enter")
	if !strings.Contains(r.text(), "HTTP Error 429") {
		t.Fatalf("error not shown:\n%s", r.text())
	}
	r = newRig(t, 100, 30)
	r.source.tracks = nil
	r.key("/")
	r.typed("zzz")
	r.key("enter")
	if !strings.Contains(r.text(), "No results") {
		t.Fatalf("empty result not said:\n%s", r.text())
	}
}

func TestResultCursorMovesAndEnterPlaysTheListFromThere(t *testing.T) {
	r := newRig(t, 100, 30)
	r.key("/")
	r.typed("x")
	r.key("enter", "down", "down", "j", "k")
	r.key("enter")
	if log := r.player.log(); len(log) != 1 || log[0] != "replace 5 tracks at 2 first=id00" {
		t.Fatalf("calls %v", log)
	}
	r.key("tab") // playing moved to the player screen; back to the results
	r.key("up", "up", "up", "up")
	r.key("enter")
	if log := r.player.log(); log[1] != "replace 5 tracks at 0 first=id00" {
		t.Fatalf("cursor went above the first: %v", log)
	}
	r.key("tab")
	r.key("down", "down", "down", "down", "down", "down", "down")
	r.key("a")
	if log := r.player.log(); log[2] != "enqueue id04" {
		t.Fatalf("cursor went past the last or a does not enqueue: %v", log)
	}
}

func TestPlayingFromTheResultsMovesToThePlayerScreen(t *testing.T) {
	r := newRig(t, 100, 30)
	r.key("/")
	r.typed("x")
	r.key("enter", "enter")
	if r.m.(Model).Tab() != "player" {
		t.Fatalf("tab %s", r.m.(Model).Tab())
	}
}

// ── player screen ───────────────────────────────────────────────────────────

func TestPlayerScreenShowsNowPlayingTheQueueAndTheProgress(t *testing.T) {
	r := newRig(t, 110, 30)
	ts := tracks(6)
	r.state(playing(ts, 2))
	r.key("tab", "tab")
	txt := r.text()
	for _, want := range []string{"Now Playing", "Up Next", "Song 3", "Song 4", "Song 6", "0:50", "2:56", "40%", "Playing"} {
		if !strings.Contains(txt, want) {
			t.Errorf("player screen lacks %q:\n%s", want, txt)
		}
	}
	s := playing(ts, 2)
	s.Paused = true
	r.state(s)
	if !strings.Contains(r.text(), "Paused") {
		t.Errorf("paused not shown:\n%s", r.text())
	}
	r.state(music.State{Connected: true, Index: -1, Volume: 70})
	if !strings.Contains(r.text(), "Nothing playing") {
		t.Errorf("idle not shown:\n%s", r.text())
	}
}

func TestProgressBarFollowsThePosition(t *testing.T) {
	r := newRig(t, 100, 30)
	bar := func(pos time.Duration) int {
		s := playing(tracks(1), 0)
		s.Position, s.Duration = pos, 100*time.Second
		r.state(s)
		lines := r.screen()
		return strings.Count(ansi.Strip(lines[len(lines)-3]), "█")
	}
	a, b, c := bar(0), bar(50*time.Second), bar(100*time.Second)
	if !(a < b && b < c) || a != 0 {
		t.Fatalf("filled cells %d, %d, %d", a, b, c)
	}
}

func TestQueueKeysActOnTheSelectedEntry(t *testing.T) {
	r := newRig(t, 100, 30)
	r.state(playing(tracks(5), 1))
	r.key("tab", "tab")
	r.key("down", "down") // from the playing entry (1) to 3
	r.key("enter")
	r.key("x")
	r.key("[")
	r.key("]")
	want := []string{"jump 3", "remove 3", "move 3 2", "move 2 3"}
	log := r.player.log()
	if fmt.Sprint(log) != fmt.Sprint(want) {
		t.Fatalf("calls %v, want %v", log, want)
	}
}

func TestQueueCursorStartsOnTheCurrentTrackAndStaysInRange(t *testing.T) {
	r := newRig(t, 100, 30)
	r.state(playing(tracks(4), 2))
	r.key("tab", "tab", "enter")
	r.key("down", "down", "down", "enter")
	r.key("up", "up", "up", "up", "up", "up", "enter")
	r.state(playing(tracks(2), 0)) // the queue shrank under the cursor
	r.key("enter")
	want := []string{"jump 2", "jump 3", "jump 0", "jump 0"}
	if fmt.Sprint(r.player.log()) != fmt.Sprint(want) {
		t.Fatalf("calls %v, want %v", r.player.log(), want)
	}
}

func TestLibraryScreenExplainsThatItNeedsCookies(t *testing.T) {
	r := newRig(t, 100, 30)
	r.key("tab")
	if txt := r.text(); !strings.Contains(txt, "Library") || !strings.Contains(txt, "cookieBrowser") {
		t.Fatalf("library screen:\n%s", txt)
	}
}

// ── lazy enrichment of search results ───────────────────────────────────────

// enrichingSource is a Source whose listing has titles only, like a songs search, and which can enrich one track.
type enrichingSource struct {
	*stubSource
	enriched []string
	fail     map[string]error
}

func (s *enrichingSource) Enrich(_ context.Context, t music.Track) (music.Track, error) {
	s.enriched = append(s.enriched, t.ID)
	if err := s.fail[t.ID]; err != nil {
		return t, err
	}
	t.Artists, t.Duration, t.Album = []string{"Artist of " + t.ID}, 200*time.Second, "Album of "+t.ID
	return t, nil
}

func bareTracks(n int) []music.Track {
	out := make([]music.Track, n)
	for i := range out {
		out[i] = music.Track{ID: fmt.Sprintf("id%02d", i), Title: fmt.Sprintf("Song %d", i+1)}
	}
	return out
}

func enrichRig(t *testing.T, fail map[string]error) (*rig, *enrichingSource) {
	t.Helper()
	r := newRig(t, 100, 30)
	src := &enrichingSource{stubSource: &stubSource{tracks: bareTracks(5)}, fail: fail}
	r.m = New(Deps{Source: src, Player: r.player})
	r.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	return r, src
}

func TestAnEnrichedResultIsWhatPlaysAndWhatIsQueued(t *testing.T) {
	r, _ := enrichRig(t, nil)
	r.key("/")
	r.typed("x")
	r.key("enter")
	r.key("a")
	if log := r.player.log(); len(log) != 1 || !strings.Contains(log[0], "id00") {
		t.Fatalf("%v", log)
	}
	r.key("enter")
	if log := r.player.log(); len(log) != 2 || !strings.Contains(log[1], "replace 5 tracks at 0") {
		t.Fatalf("%v", log)
	}
}

func TestAnEnrichmentThatFailsIsShownNotHiddenAndDoesNotLoop(t *testing.T) {
	r, src := enrichRig(t, map[string]error{"id00": errors.New("yt-dlp 2026.08.19 is old or has no JS runtime: fix it")})
	r.key("/")
	r.typed("x")
	r.key("enter")
	if txt := r.text(); !strings.Contains(txt, "is old or has no JS runtime") {
		t.Fatalf("the failure is not shown:\n%s", txt)
	}
	r.key("up", "up", "down") // back on the failed row and away: not asked again, and the other rows were enriched
	count := map[string]int{}
	for _, id := range src.enriched {
		count[id]++
	}
	if count["id00"] != 1 || len(count) != 5 {
		t.Errorf("a failed row was asked again, or the others were skipped: %v", src.enriched)
	}
	if !strings.Contains(r.text(), "Artist of id03") {
		t.Errorf("a failure on one track stopped the rest:\n%s", r.text())
	}
}

func TestAnEnrichmentForAnOlderSearchIsIgnored(t *testing.T) {
	r, _ := enrichRig(t, nil)
	r.key("/")
	r.typed("x")
	r.key("enter")
	r.send(enrichedMsg{id: "gone", track: music.Track{ID: "gone", Title: "Old", Artists: []string{"Nobody"}}})
	if strings.Contains(r.text(), "Nobody") {
		t.Error("a stale enrichment appeared")
	}
}

func TestASourceWithoutEnrichAndTracksThatHaveALengthAreLeftAlone(t *testing.T) {
	r := newRig(t, 100, 30) // stubSource: tracks have lengths and the source cannot enrich
	r.key("/")
	r.typed("x")
	r.key("enter")
	r.key("down")
	if txt := r.text(); !strings.Contains(txt, "Song 2") {
		t.Fatalf("\n%s", txt)
	}
}
