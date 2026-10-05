package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// libSource is a Source with a library. Until Grant is called it answers the way ytdlp.Source does without consent.
type libSource struct {
	*stubSource
	need      error // returned by Library and Tracks until granted
	granted   bool
	libErr    error
	cols      []music.Collection
	tracksOf  map[string][]music.Track
	libCalls  int
	trackCall []string
	grants    int
}

func (s *libSource) gate() error {
	if !s.granted && s.need != nil {
		return s.need
	}
	return nil
}

func (s *libSource) Library(context.Context) ([]music.Collection, error) {
	s.libCalls++
	if err := s.gate(); err != nil {
		return nil, err
	}
	return s.cols, s.libErr
}

func (s *libSource) Tracks(_ context.Context, id string) ([]music.Track, error) {
	s.trackCall = append(s.trackCall, id)
	if err := s.gate(); err != nil {
		return nil, err
	}
	return s.tracksOf[id], nil
}

func (s *libSource) GrantCookieAccess(context.Context, string) error {
	s.grants++
	s.granted = true
	return nil
}

func newLib() *libSource {
	return &libSource{
		stubSource: &stubSource{tracks: tracks(5)},
		cols: []music.Collection{
			{ID: "LM", Kind: "liked", Title: "Liked songs"},
			{ID: "PLroadtrip0000001", Kind: "playlist", Title: "Road trip", Count: 12},
			{ID: "PLfocus00000000002", Kind: "playlist", Title: "Focus"},
		},
		tracksOf: map[string][]music.Track{
			"LM":                {{ID: "aaaaaaaaaaa", Title: "Liked one"}, {ID: "bbbbbbbbbbb", Title: "Liked two", Artists: []string{"Some Artist"}, Duration: 125 * time.Second}},
			"PLroadtrip0000001": {{ID: "ccccccccccc", Title: "Highway song"}},
		},
	}
}

func libRig(t *testing.T, src *libSource, w, h int) *rig {
	t.Helper()
	r := newRig(t, w, h)
	r.m = New(Deps{Source: src, Player: r.player})
	r.send(tea.WindowSizeMsg{Width: w, Height: h})
	return r
}

func consentNeeded() *music.NeedsConsentError {
	return &music.NeedsConsentError{Browser: "chrome", Description: "chrome (the default browser, com.google.chrome)", Notes: "Chrome: macOS asks to let yt-dlp use the browser's \"Safe Storage\" item in your Keychain; choose Allow."}
}

func TestTheLibraryListsLikedSongsAndPlaylistsWhenAllowed(t *testing.T) {
	src := newLib()
	src.granted = true
	r := libRig(t, src, 100, 30)
	r.key("tab")
	txt := r.text()
	for _, want := range []string{"Liked songs", "Road trip", "12", "Focus"} {
		if !strings.Contains(txt, want) {
			t.Errorf("lacks %q:\n%s", want, txt)
		}
	}
	if src.libCalls != 1 {
		t.Errorf("%d library calls", src.libCalls)
	}
	r.key("tab", "tab") // around again: not asked a second time
	r.key("tab")
	if src.libCalls != 1 {
		t.Errorf("the library was listed again on revisiting: %d", src.libCalls)
	}
}

func TestWithoutConsentTheTabAsksOncePerBrowserAndNeverReadsBeforeYes(t *testing.T) {
	src := newLib()
	src.need = consentNeeded()
	r := libRig(t, src, 100, 30)
	r.key("tab")
	txt := strings.Join(strings.Fields(r.text()), " ") // the prompt wraps
	for _, want := range []string{"chrome (the default browser, com.google.chrome)", "cookies", "library", "Keychain", "[y]", "[n]", "never use"} {
		if !strings.Contains(txt, want) {
			t.Errorf("the prompt lacks %q:\n%s", want, txt)
		}
	}
	if src.grants != 0 || src.libCalls != 1 {
		t.Fatalf("grants %d, library calls %d", src.grants, src.libCalls)
	}
	r.key("n")
	if src.grants != 0 || !strings.Contains(r.text(), "not allowed") {
		t.Errorf("after n:\n%s", r.text())
	}
	r.key("tab", "tab", "tab") // leaving and returning does not ask again by itself
	if src.libCalls != 1 {
		t.Errorf("asked again without being told to: %d", src.libCalls)
	}
	r.key("r") // ...but r does
	if !strings.Contains(r.text(), "[y]") {
		t.Errorf("r did not ask again:\n%s", r.text())
	}
	r.key("y")
	if src.grants != 1 || !strings.Contains(r.text(), "Road trip") {
		t.Errorf("after y (grants %d):\n%s", src.grants, r.text())
	}
}

func TestWithNoBrowserTheTabSaysWhyAndNamesTheSettingAndOffersNoPrompt(t *testing.T) {
	src := newLib()
	src.need = &music.NoBrowserError{Reason: `there is no graphical session here (no DISPLAY or WAYLAND_DISPLAY), so there is no default browser to read. Set "cookieBrowser" in the pig-music settings to one of: brave, chrome.`}
	r := libRig(t, src, 100, 30)
	r.key("tab")
	txt := strings.Join(strings.Fields(r.text()), " ")
	if !strings.Contains(txt, "no graphical session") || !strings.Contains(txt, "cookieBrowser") || strings.Contains(txt, "[y]") {
		t.Errorf("\n%s", txt)
	}
	r.key("y")
	if src.grants != 0 {
		t.Error("y granted without a prompt")
	}
}

func TestACollectionOpensItsTracksAndPlayingGoesToThePlayerAndEscGoesBack(t *testing.T) {
	src := newLib()
	src.granted = true
	r := libRig(t, src, 100, 30)
	r.key("tab", "down", "enter") // Road trip
	if len(src.trackCall) != 1 || src.trackCall[0] != "PLroadtrip0000001" || !strings.Contains(r.text(), "Highway song") || !strings.Contains(r.text(), "Road trip") {
		t.Fatalf("calls %v\n%s", src.trackCall, r.text())
	}
	r.key("esc")
	if r.quit || !strings.Contains(r.text(), "Focus") {
		t.Fatalf("esc inside a collection must go back to the list, not quit:\n%s", r.text())
	}
	r.key("up", "enter") // Liked songs
	if !strings.Contains(r.text(), "Liked two") {
		t.Fatalf("\n%s", r.text())
	}
	r.key("down", "enter")
	if log := r.player.log(); len(log) != 1 || log[0] != "replace 2 tracks at 1 first=aaaaaaaaaaa" {
		t.Fatalf("%v", log)
	}
	if r.m.(Model).Tab() != "player" {
		t.Errorf("tab %s", r.m.(Model).Tab())
	}
	r.key("shift+tab", "esc") // back on the library: still inside the collection; esc backs out
	if r.quit {
		t.Fatal("quit")
	}
	r.key("esc")
	if !r.quit {
		t.Error("esc on the list does not quit")
	}
}

func TestAQueueKeyAddsTheSelectedLibraryTrackAndQHidesEverywhere(t *testing.T) {
	src := newLib()
	src.granted = true
	r := libRig(t, src, 100, 30)
	r.key("tab", "enter", "down", "a")
	if log := r.player.log(); len(log) != 1 || !strings.Contains(log[0], "bbbbbbbbbbb") {
		t.Fatalf("%v", log)
	}
	r.key("q")
	if !r.quit {
		t.Error("q inside a collection did not hide")
	}
}

func TestLibraryErrorsAreShownAsTheyAre(t *testing.T) {
	src := newLib()
	src.granted = true
	src.libErr = errors.New("ERROR: unable to download webpage: HTTP Error 403: Forbidden")
	r := libRig(t, src, 100, 30)
	r.key("tab")
	if !strings.Contains(r.text(), "HTTP Error 403") {
		t.Errorf("\n%s", r.text())
	}
	src.libErr = nil
	r.key("r")
	if !strings.Contains(r.text(), "Road trip") {
		t.Errorf("r did not reload:\n%s", r.text())
	}
}

func TestLibraryTextFromOutsideCannotInjectControlCharacters(t *testing.T) {
	src := newLib()
	src.granted = true
	src.cols[1].Title = "Bad\x1b]0;owned\x07 title\nsplit"
	r := libRig(t, src, 100, 30)
	r.key("tab")
	if strings.ContainsAny(r.m.View().Content, "\x07") || strings.Contains(r.m.View().Content, "\x1b]") {
		t.Errorf("control characters reached the screen: %q", r.m.View().Content)
	}
}

func TestEveryLibraryScreenFillsTheTerminalAtEverySize(t *testing.T) {
	for _, sz := range [][2]int{{120, 40}, {80, 24}, {60, 16}, {40, 12}, {30, 8}, {24, 6}, {20, 5}, {1, 1}, {200, 10}} {
		w, h := sz[0], sz[1]
		views := map[string]func(*libSource){
			"list":    func(s *libSource) { s.granted = true },
			"consent": func(s *libSource) { s.need = consentNeeded() },
			"nobrowser": func(s *libSource) {
				s.need = &music.NoBrowserError{Reason: "日本語 no browser " + strings.Repeat("x", 300)}
			},
		}
		for name, setup := range views {
			src := newLib()
			src.cols[1].Title = "日本語のプレイリスト 🎵"
			setup(src)
			r := libRig(t, src, w, h)
			r.key("tab")
			checkLayout(t, fmt.Sprintf("%s %dx%d", name, w, h), r, w, h)
			if name == "list" {
				r.key("down", "enter")
				checkLayout(t, fmt.Sprintf("tracks %dx%d", w, h), r, w, h)
			}
		}
	}
}

func checkLayout(t *testing.T, what string, r *rig, w, h int) {
	t.Helper()
	lines := r.screen()
	if len(lines) != h {
		t.Errorf("%s: %d lines, want %d", what, len(lines), h)
	}
	for i, l := range lines {
		if cw := ansi.StringWidth(l); cw > w {
			t.Errorf("%s: line %d is %d cells wide, over %d: %q", what, i, cw, w, ansi.Strip(l))
		}
	}
}

func TestShowPlayerMsgOpensThePlayerScreenWithTheCursorOnTheCurrentTrack(t *testing.T) {
	r := newRig(t, 100, 30)
	r.state(playing(tracks(5), 3))
	r.send(ShowPlayerMsg{})
	if r.m.(Model).Tab() != "player" || !strings.Contains(r.text(), "Up Next") {
		t.Fatalf("tab %s\n%s", r.m.(Model).Tab(), r.text())
	}
	r.key("enter")
	if log := r.player.log(); len(log) != 1 || log[0] != "jump 3" {
		t.Errorf("%v", log)
	}
}

// ── what the screen opens on ────────────────────────────────────────────────

func TestOpeningWithTheLibraryUnavailableAndNothingPlayingShowsSearchNotTheError(t *testing.T) {
	for name, need := range map[string]error{
		"no consent": consentNeeded(),
		"no browser": &music.NoBrowserError{Reason: "no graphical session"},
		"load error": errors.New("ERROR: Operation not permitted: Cookies.binarycookies"),
	} {
		src := newLib()
		src.need = need
		src.libErr = nil
		r := libRig(t, src, 100, 30)
		r.key("tab")
		if r.m.(Model).Tab() != "library" {
			t.Fatalf("%s: not on the library", name)
		}
		r.send(OpenedMsg{}) // the screen was hidden and /music opened it again
		if r.m.(Model).Tab() != "search" || !strings.Contains(r.text(), "Press / to search") {
			t.Errorf("%s: tab %s\n%s", name, r.m.(Model).Tab(), r.text())
		}
		for _, leak := range []string{"Operation not permitted", "[y]", "no graphical session"} {
			if strings.Contains(r.text(), leak) {
				t.Errorf("%s: the Search screen shows the library's %q", name, leak)
			}
		}
		r.key("tab") // the error is still there, on the Library tab
		if txt := strings.Join(strings.Fields(r.text()), " "); !strings.Contains(txt, "Library") || r.m.(Model).Tab() != "library" {
			t.Errorf("%s: %s", name, txt)
		}
	}
}

func TestOpeningStaysWhereItWasWhenThereIsSomethingToShow(t *testing.T) {
	// a working library
	src := newLib()
	src.granted = true
	r := libRig(t, src, 100, 30)
	r.key("tab")
	r.send(OpenedMsg{})
	if r.m.(Model).Tab() != "library" {
		t.Errorf("a working library was left: %s", r.m.(Model).Tab())
	}
	// the library has an error but a track is playing: the person may be on it on purpose; leave the tab alone
	src = newLib()
	src.need = consentNeeded()
	r = libRig(t, src, 100, 30)
	r.state(playing(tracks(3), 0))
	r.key("tab")
	r.send(OpenedMsg{})
	if r.m.(Model).Tab() != "library" {
		t.Errorf("tab %s", r.m.(Model).Tab())
	}
	// the Player and Search screens are never moved
	r = libRig(t, newLib(), 100, 30)
	r.key("tab", "tab")
	r.send(OpenedMsg{})
	if r.m.(Model).Tab() != "player" {
		t.Errorf("tab %s", r.m.(Model).Tab())
	}
}

func TestTheEmptySearchScreenSaysHowToStart(t *testing.T) {
	r := newRig(t, 100, 30)
	txt := r.text()
	for _, want := range []string{"Press / to search", "type a song or artist", "enter to search", "enter on a result to play"} {
		if !strings.Contains(txt, want) {
			t.Errorf("the empty search screen lacks %q:\n%s", want, txt)
		}
	}
	// narrow terminals keep the first instruction
	r = newRig(t, 30, 10)
	if !strings.Contains(r.text(), "Press / to") {
		t.Errorf("\n%s", r.text())
	}
}
