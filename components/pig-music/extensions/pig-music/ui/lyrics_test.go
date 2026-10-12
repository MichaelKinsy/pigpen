package ui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MichaelKinsy/pigpen/pig-music/art"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

func sec(n float64) time.Duration { return time.Duration(n * float64(time.Second)) }

func synced() music.Lyrics {
	return music.Lyrics{Source: "lrclib", Synced: true, Lines: []music.LyricLine{
		{At: sec(5), End: sec(10), Text: "First example line"},
		{At: sec(10), End: sec(20), Text: "Second example line that is quite a bit longer than the first"},
		{At: sec(20), End: sec(25), Text: ""},
		{At: sec(25), Text: "Last example line"},
	}}
}

// stubLyrics is a provider with a script.
type stubLyrics struct {
	mu      sync.Mutex
	byID    map[string]music.Lyrics
	errs    map[string]error
	asked   []string
	offsets map[string]time.Duration
	sets    []time.Duration
}

func (s *stubLyrics) Lyrics(_ context.Context, t music.Track) (music.Lyrics, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asked = append(s.asked, t.ID)
	if err := s.errs[t.ID]; err != nil {
		return music.Lyrics{}, err
	}
	if l, ok := s.byID[t.ID]; ok {
		return l, nil
	}
	return music.Lyrics{}, music.ErrNoLyrics
}
func (s *stubLyrics) Offset(id string) time.Duration { return s.offsets[id] }
func (s *stubLyrics) SetOffset(id string, d time.Duration) {
	if s.offsets == nil {
		s.offsets = map[string]time.Duration{}
	}
	s.offsets[id] = d
	s.sets = append(s.sets, d)
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func lyricRig(t *testing.T, lp *stubLyrics, mutate func(*Deps)) (*rig, *fakeClock) {
	t.Helper()
	r := newRig(t, 120, 30)
	ck := &fakeClock{t: time.Unix(1000, 0)}
	d := Deps{Source: r.source, Player: r.player, Lyrics: lp, Now: ck.now, ArtMode: art.TrueColor, Vibes: true}
	if mutate != nil {
		mutate(&d)
	}
	r.m = New(d)
	r.send(tea.WindowSizeMsg{Width: 120, Height: 30})
	return r, ck
}

func at(ts []music.Track, idx int, pos time.Duration) music.State {
	st := playing(ts, idx)
	st.Position = pos
	return st
}

func TestTheSweepFollowsTheSongAcrossTheLineByCharactersWhenThereAreNoWordTimings(t *testing.T) {
	ln := music.LyricLine{At: sec(10), End: sec(16), Text: "abcdefghij"} // 10 runes over 0.9 of 6 s
	for _, c := range []struct {
		pos  time.Duration
		want int
	}{{sec(9), 0}, {sec(10), 0}, {sec(12.7), 5}, {sec(15.5), 10}, {sec(30), 10}} {
		if got := sungRunes(ln, c.pos); got != c.want {
			t.Errorf("at %v: %d, want %d", c.pos, got, c.want)
		}
	}
}

func TestTheSweepFollowsEachWordWhenTheLineHasWordTimings(t *testing.T) {
	ln := music.LyricLine{At: sec(10), End: sec(13), Text: "aaa bbbbb cc", Words: []music.LyricWord{{At: sec(10), Text: "aaa "}, {At: sec(11), Text: "bbbbb "}, {At: sec(12), Text: "cc"}}}
	for _, c := range []struct {
		pos  time.Duration
		want int
	}{{sec(10.5), 2}, {sec(11), 4}, {sec(11.5), 7}, {sec(12.99), 12}} {
		if got := sungRunes(ln, c.pos); abs(got-c.want) > 1 {
			t.Errorf("at %v: %d, want about %d", c.pos, got, c.want)
		}
	}
}

func TestACurrentLineIsTheLastOneThatHasStarted(t *testing.T) {
	l := synced()
	for pos, want := range map[time.Duration]int{sec(0): -1, sec(5): 0, sec(12): 1, sec(22): 2, sec(99): 3} {
		if got := currentLine(l, pos); got != want {
			t.Errorf("at %v: %d, want %d", pos, got, want)
		}
	}
}

func TestLongLinesWrapAndNoRowIsWiderThanThePanel(t *testing.T) {
	rows := wrapLyrics(synced(), 20)
	multi := 0
	for _, r := range rows {
		if ansi.StringWidth(r.text) > 20 {
			t.Errorf("%q is %d cells", r.text, ansi.StringWidth(r.text))
		}
		if r.line == 1 {
			multi++
		}
	}
	if multi < 3 {
		t.Errorf("the long line took %d rows", multi)
	}
	if rows[0].start != 0 {
		t.Errorf("%+v", rows[0])
	}
}

func TestTheLyricsOfThePlayingTrackAreFetchedOnceAndShownInPlaceOfTheQueue(t *testing.T) {
	lp := &stubLyrics{byID: map[string]music.Lyrics{"id00": synced()}}
	r, _ := lyricRig(t, lp, nil)
	r.state(at(tracks(3), 0, sec(12)))
	r.send(ShowPlayerMsg{})
	r.send(StateMsg(at(tracks(3), 0, sec(12.5))))
	if len(lp.asked) != 1 {
		t.Fatalf("asked %v", lp.asked)
	}
	text := r.text()
	for _, want := range []string{"Lyrics", "Second example line", "First example line", "Last example line", "lrclib"} {
		if !strings.Contains(text, want) {
			t.Errorf("lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Title") || !strings.Contains(text, "Up Next L") { // the table's heading; the tab that switches to it
		t.Errorf("the queue is still shown:\n%s", text)
	}
}

func TestTheCurrentLineMovesWithThePlayersPosition(t *testing.T) {
	lp := &stubLyrics{byID: map[string]music.Lyrics{"id00": synced()}}
	r, _ := lyricRig(t, lp, nil)
	r.send(ShowPlayerMsg{})
	r.state(at(tracks(3), 0, sec(6)))
	if got := r.model().lyr; got.status != lyricReady || currentLine(got.data, r.model().lyricPos()) != 0 {
		t.Fatalf("%+v", got)
	}
	r.state(at(tracks(3), 0, sec(26)))
	if currentLine(r.model().lyr.data, r.model().lyricPos()) != 3 {
		t.Errorf("position %v", r.model().lyricPos())
	}
}

func TestBetweenTwoReportsTheClockCarriesThePositionButNotWhilePausedNorFarFromTheLastReport(t *testing.T) {
	lp := &stubLyrics{byID: map[string]music.Lyrics{"id00": synced()}}
	r, ck := lyricRig(t, lp, nil)
	r.send(ShowPlayerMsg{})
	r.state(at(tracks(3), 0, sec(10)))
	ck.t = ck.t.Add(500 * time.Millisecond)
	if got := r.model().lyricPos(); got != sec(10.5) {
		t.Errorf("playing: %v", got)
	}
	ck.t = ck.t.Add(10 * time.Second) // the player has gone quiet: no guessing
	if got := r.model().lyricPos(); got != sec(10) {
		t.Errorf("stale: %v", got)
	}
	st := at(tracks(3), 0, sec(10))
	st.Paused = true
	r.state(st)
	ck.t = ck.t.Add(500 * time.Millisecond)
	if got := r.model().lyricPos(); got != sec(10) {
		t.Errorf("paused: %v", got)
	}
}

func TestATrackWithNoLyricsShowsAFriendlyCardAndAnInstrumentalSaysSo(t *testing.T) {
	lp := &stubLyrics{byID: map[string]music.Lyrics{"id01": {Instrumental: true}}}
	r, _ := lyricRig(t, lp, nil)
	r.send(ShowPlayerMsg{})
	r.state(at(tracks(3), 0, sec(6)))
	if text := r.text(); !strings.Contains(text, "No lyrics for this one") || !strings.Contains(text, "Song 1") {
		t.Errorf("none:\n%s", text)
	}
	r.state(at(tracks(3), 1, sec(6)))
	if text := r.text(); !strings.Contains(text, "Instrumental") {
		t.Errorf("instrumental:\n%s", text)
	}
}

func TestAFailedLookupSaysSoAndTogglingTwiceTriesAgain(t *testing.T) {
	lp := &stubLyrics{errs: map[string]error{"id00": errors.New("no network")}, byID: map[string]music.Lyrics{}}
	r, _ := lyricRig(t, lp, nil)
	r.send(ShowPlayerMsg{})
	r.state(at(tracks(3), 0, sec(6)))
	if text := r.text(); !strings.Contains(text, "Could not get the lyrics") || !strings.Contains(text, "no network") {
		t.Fatalf("%s", text)
	}
	delete(lp.errs, "id00")
	lp.byID["id00"] = synced()
	r.key("L", "L")
	if r.model().lyr.status != lyricReady || len(lp.asked) != 2 {
		t.Errorf("status %v, asked %v", r.model().lyr.status, lp.asked)
	}
}

func TestLHidesTheLyricsAndNothingIsFetchedWhileTheyAreHidden(t *testing.T) {
	lp := &stubLyrics{byID: map[string]music.Lyrics{"id00": synced(), "id01": synced()}}
	r, _ := lyricRig(t, lp, nil)
	r.send(ShowPlayerMsg{})
	r.key("L") // off before anything plays
	r.state(at(tracks(3), 0, sec(6)))
	if len(lp.asked) != 0 || !strings.Contains(r.text(), "Title") {
		t.Fatalf("asked %v:\n%s", lp.asked, r.text())
	}
	r.key("L")
	if len(lp.asked) != 1 || strings.Contains(r.text(), "Title") {
		t.Errorf("asked %v", lp.asked)
	}
}

func TestACommaAndAPeriodNudgeTheLyricsAndTheNudgeIsRememberedForTheTrack(t *testing.T) {
	lp := &stubLyrics{byID: map[string]music.Lyrics{"id00": synced()}, offsets: map[string]time.Duration{}}
	r, _ := lyricRig(t, lp, nil)
	r.send(ShowPlayerMsg{})
	r.state(at(tracks(3), 0, sec(12)))
	r.key(".", ".", ".", ",")
	if got := r.model().lyr.offset; got != 200*time.Millisecond || lp.offsets["id00"] != 200*time.Millisecond {
		t.Errorf("offset %v, stored %v", got, lp.offsets)
	}
	if got := r.model().lyricPos(); got != sec(12)-200*time.Millisecond {
		t.Errorf("position %v", got)
	}
	if !strings.Contains(r.text(), "offset +0.2s") {
		t.Errorf("the offset is not shown:\n%s", r.text())
	}
	// a new model for the same track (a new session) starts from the remembered nudge
	r2, _ := lyricRig(t, lp, nil)
	r2.send(ShowPlayerMsg{})
	r2.state(at(tracks(3), 0, sec(12)))
	if r2.model().lyr.offset != 200*time.Millisecond {
		t.Errorf("the nudge was not remembered: %v", r2.model().lyr.offset)
	}
}

func TestWithoutAProviderTheKeysAreNotTakenAndTheQueueStays(t *testing.T) {
	r := newRig(t, 120, 30)
	r.send(ShowPlayerMsg{})
	r.state(playing(tracks(3), 0))
	if !strings.Contains(r.text(), "Up Next") {
		t.Error("no queue")
	}
	r.key("L", ".")
	if !strings.Contains(r.text(), "Up Next") {
		t.Error("L changed something")
	}
}

func TestTheLevelBarMakesWayForTheLyrics(t *testing.T) {
	lp := &stubLyrics{byID: map[string]music.Lyrics{"id00": synced()}}
	r, _ := lyricRig(t, lp, nil)
	r.send(ShowPlayerMsg{})
	r.state(at(tracks(3), 0, sec(12)))
	if strings.Contains(r.text(), "Level") {
		t.Error("the level bar is still there")
	}
}

type tickCount struct{ n int }

func (c *tickCount) tick(time.Duration) tea.Cmd { c.n++; return func() tea.Msg { return nil } }

func TestTheSweepAsksForFramesOnlyWhileSyncedLyricsAreShownAndThePlayerPlays(t *testing.T) {
	for name, c := range map[string]struct {
		mutate func(*Deps)
		pause  bool
		want   bool
	}{
		"playing": {nil, false, true},
		"paused":  {nil, true, false},
		"calm":    {func(d *Deps) { d.Calm = true }, false, false},
	} {
		lp := &stubLyrics{byID: map[string]music.Lyrics{"id00": synced()}}
		tc := &tickCount{}
		r, _ := lyricRig(t, lp, func(d *Deps) {
			d.Tick = tc.tick
			if c.mutate != nil {
				c.mutate(d)
			}
		})
		r.send(ShowPlayerMsg{})
		st := at(tracks(3), 0, sec(12))
		st.Paused = c.pause
		r.state(st)
		if (tc.n > 0) != c.want {
			t.Errorf("%s: %d ticks asked", name, tc.n)
		}
	}
	// an unsynced sheet does not animate
	lp := &stubLyrics{byID: map[string]music.Lyrics{"id00": {Lines: []music.LyricLine{{Text: "a"}, {Text: "b"}}}}}
	r, _ := lyricRig(t, lp, nil)
	r.send(ShowPlayerMsg{})
	r.state(at(tracks(3), 0, sec(12)))
	if r.model().lyricsAnimating() {
		t.Error("a plain sheet animates")
	}
}

func TestAPlainSheetScrollsWithHowFarIntoTheTrackItIs(t *testing.T) {
	var lines []music.LyricLine
	for i := 0; i < 80; i++ {
		lines = append(lines, music.LyricLine{Text: strings.Repeat("x", 3) + string(rune('A'+i%26)) + string(rune('a'+i/26))})
	}
	lp := &stubLyrics{byID: map[string]music.Lyrics{"id00": {Source: "youtube", Lines: lines}}}
	r, _ := lyricRig(t, lp, nil)
	r.send(ShowPlayerMsg{})
	st := at(tracks(3), 0, 0)
	r.state(st)
	early := r.text()
	st.Position = st.Duration - sec(1)
	r.state(st)
	late := r.text()
	if !strings.Contains(early, "xxxAa") || strings.Contains(early, "xxxFc") || strings.Contains(late, "xxxAa") || !strings.Contains(late, "xxxFc") {
		t.Errorf("early:\n%s\nlate:\n%s", early, late)
	}
}

func TestEveryLyricRowFitsTheWidthAtEverySize(t *testing.T) {
	for _, sz := range [][2]int{{120, 40}, {80, 24}, {60, 16}, {40, 12}, {30, 8}, {24, 6}} {
		lp := &stubLyrics{byID: map[string]music.Lyrics{"id00": synced()}}
		r, _ := lyricRig(t, lp, nil)
		r.send(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		r.send(ShowPlayerMsg{})
		r.state(at(tracks(3), 0, sec(12)))
		lines := r.screen()
		if len(lines) != sz[1] {
			t.Errorf("%v: %d lines", sz, len(lines))
		}
		for _, l := range lines {
			if w := ansi.StringWidth(l); w > sz[0] {
				t.Errorf("%v: a line is %d cells", sz, w)
			}
		}
	}
}

func TestTheEasedViewReachesTheCurrentLineAndJumpsOnASeekOrUnderCalm(t *testing.T) {
	var lines []music.LyricLine
	for i := 0; i < 100; i++ {
		lines = append(lines, music.LyricLine{At: sec(float64(i * 2)), End: sec(float64(i*2 + 2)), Text: "line"})
	}
	l := music.Lyrics{Synced: true, Lines: lines}
	lp := &stubLyrics{byID: map[string]music.Lyrics{"id00": l}}
	r, _ := lyricRig(t, lp, nil)
	r.send(ShowPlayerMsg{})
	r.state(at(tracks(3), 0, sec(2)))
	r.state(at(tracks(3), 0, sec(60))) // a seek: far from where the view is
	far := r.model().lyr.top
	if far < 20 {
		t.Fatalf("a seek left the view at %d", far)
	}
	r.state(at(tracks(3), 0, sec(66)))
	first := r.model().lyr.top
	for i := 0; i < 20; i++ {
		r.send(TickMsg{})
	}
	if end := r.model().lyr.top; end <= first-1 || end < far {
		t.Errorf("eased from %d to %d", first, end)
	}
}
