package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

func stageRig(t *testing.T, w, h int) *rig {
	t.Helper()
	lp := &stubLyrics{byID: map[string]music.Lyrics{"id00": synced()}}
	r, _ := lyricRig(t, lp, nil)
	r.send(tea.WindowSizeMsg{Width: w, Height: h})
	r.state(at(tracks(4), 0, sec(12)))
	r.send(ShowPlayerMsg{})
	r.send(StateMsg(at(tracks(4), 0, sec(12.5))))
	return r
}

func TestOnAWideTerminalTheStageHasTheLyricsAndTheQueueAsARail(t *testing.T) {
	text := stageRig(t, 160, 36).text()
	for _, want := range []string{"Now Playing", "Lyrics", "Second example line", "Up Next", "Song 2", "Song 4"} {
		if !strings.Contains(text, want) {
			t.Errorf("lacks %q:\n%s", want, text)
		}
	}
}

// TestEachWidthBandHasItsLayout: the Player screen's layout per width band (see wideStage), with lyrics on.
func TestEachWidthBandHasItsLayout(t *testing.T) {
	type band struct {
		widths                                  []int
		nowPlaying, tabs, short, table, picture bool
	}
	for _, b := range []band{
		{widths: []int{132, 160}, nowPlaying: true, table: true, picture: true},                 // three columns: the queue is the rail
		{widths: []int{80, 107, 131}, nowPlaying: true, tabs: true, short: true, picture: true}, // two: the next tracks under the track
		{widths: []int{50, 56, 79}, tabs: true, picture: true},                                  // the track and its disc over the panel
		{widths: []int{30, 40, 49}, tabs: true},                                                 // the track's text over the panel
	} {
		for _, w := range b.widths {
			r := stageRig(t, w, 30)
			text := r.text()
			got := band{
				nowPlaying: strings.Contains(text, "Now Playing"),
				tabs:       strings.Contains(text, "Lyrics   Up Next L"),
				short:      strings.Contains(text, "Song 2 - Artist") && !strings.Contains(text, "Next Song 2"),
				table:      strings.Contains(text, "Title"),
				picture:    hasBraille(text),
			}
			got.widths = b.widths
			if fmt.Sprint(got) != fmt.Sprint(b) || !strings.Contains(text, "Second example line") || !strings.Contains(text, "Song 1") {
				t.Errorf("%d wide: %+v, want %+v:\n%s", w, got, b, text)
			}
			if w < twoColumns && !strings.Contains(text, "Next Song 2") {
				t.Errorf("%d wide: the track's text does not name the next one:\n%s", w, text)
			}
		}
	}
}

// TestTheTabsOverThePanelSwitchItByKeyAndByClick: below the wide stage, L and a click on the other tab switch the panel between
// the lyrics and the queue; a click on the tab shown does nothing.
func TestTheTabsOverThePanelSwitchItByKeyAndByClick(t *testing.T) {
	for _, w := range []int{40, 56, 107} {
		r := stageRig(t, w, 30)
		r.clickText("Up Next L", 0, 1)
		if text := r.text(); !strings.Contains(text, "Title") || !strings.Contains(text, "Lyrics L") || strings.Contains(text, "Second example line") {
			t.Fatalf("%d wide: a click on Up Next did not show the queue:\n%s", w, text)
		}
		r.clickText("Up Next", 0, 1)
		if !strings.Contains(r.text(), "Title") {
			t.Fatalf("%d wide: a click on the tab shown switched it", w)
		}
		r.clickText("Lyrics L", 0, 1)
		if !strings.Contains(r.text(), "Second example line") {
			t.Fatalf("%d wide: a click on Lyrics did not show them:\n%s", w, r.text())
		}
		r.clickText("Lyrics", 0, 1)
		r.key("L")
		if !strings.Contains(r.text(), "Title") {
			t.Fatalf("%d wide: L did not switch", w)
		}
	}
}

// TestAClickOnTheShortUpNextJumpsToTheTrack: the next tracks beside the lyrics share the queue's cursor; a double click jumps
// once.
func TestAClickOnTheShortUpNextJumpsToTheTrack(t *testing.T) {
	r := stageRig(t, 107, 30)
	r.doubleClick("Song 3 - Artist", 0)
	if r.model().queueCur != 2 || r.played() != "jump 2" {
		t.Fatalf("the cursor is on %d, want 2; the player did %q", r.model().queueCur, r.played())
	}
	r.clickText("Song 4 - Artist", 0, 1)
	if r.played() != "jump 2,jump 3" {
		t.Fatalf("a click on Song 4: %q", r.played())
	}
	r.clickText("Song 4 - Artist", 0, 1) // the cursor's row: chosen again, as enter does
	if r.played() != "jump 2,jump 3,jump 3" {
		t.Fatalf("a click on the cursor's row: %q", r.played())
	}
	r = stageRig(t, 107, 30)
	r.clickText("Song 3 - Artist", 0, 1)
	r.clickText("Song 2 - Artist", 0, 1)
	if r.played() != "jump 2,jump 1" {
		t.Fatalf("the click above the cursor's row: %q", r.played())
	}
	r.key("down")
	if r.played() != "jump 2,jump 1" {
		t.Fatalf("down: %q", r.played())
	}
	r.wheel(2, 0, 1) // the wheel over the tab bar does nothing
	x, y := cellOf(t, r.screen(), "Song 4 - Artist", 0)
	r.wheel(x, y, 1)
	if r.model().queueCur != 3 {
		t.Errorf("the wheel on the next tracks moved the cursor to %d, want 3", r.model().queueCur)
	}
}

// TestEveryBandsUpNextJumpsOnAClick: the queue shown by the Up Next tab (below the wide stage) or as the rail (on it) takes a
// click as a jump, once for a double click.
func TestEveryBandsUpNextJumpsOnAClick(t *testing.T) {
	for _, w := range []int{40, 56, 80, 107, 160} {
		r := stageRig(t, w, 30)
		if w < 132 {
			r.clickText("Up Next L", 0, 1)
		}
		_, head := cellOf(t, r.screen(), "Title", bodyTop) // the queue's table
		r.doubleClick("Song 3", head+1)
		if r.model().queueCur != 2 || r.played() != "jump 2" {
			t.Fatalf("%d wide: cursor %d, player %q:\n%s", w, r.model().queueCur, r.played(), r.text())
		}
	}
}

func TestEveryStageWidthFitsEverySize(t *testing.T) {
	for _, size := range [][2]int{{24, 6}, {40, 12}, {60, 16}, {80, 24}, {131, 30}, {132, 30}, {160, 36}, {200, 50}, {240, 60}} {
		r := stageRig(t, size[0], size[1])
		for _, st := range []music.State{at(tracks(4), 0, sec(12.5)), at(tracks(4), 3, sec(170))} {
			r.send(StateMsg(st))
			lines := r.screen()
			if len(lines) != size[1] {
				t.Errorf("%dx%d: %d lines", size[0], size[1], len(lines))
			}
			for _, l := range lines {
				if ansi.StringWidth(l) > size[0] {
					t.Errorf("%dx%d: %d wide: %q", size[0], size[1], ansi.StringWidth(l), l)
				}
			}
		}
	}
}

func TestInTheLastTenSecondsACardNamesTheNextTrack(t *testing.T) {
	r := newRig(t, 100, 30)
	ts := tracks(3)
	st := playing(ts, 0)
	r.send(ShowPlayerMsg{})
	st.Duration, st.Position = 200*time.Second, 189*time.Second
	r.state(st)
	if strings.Contains(r.text(), "Up next in") {
		t.Error("a card with 11 s left")
	}
	st.Position = 193 * time.Second
	r.state(st)
	text := r.text()
	if !strings.Contains(text, "Up next in 7s") || !strings.Contains(text, "Song 2") {
		t.Errorf("no card:\n%s", text)
	}
	st.Position = 150 * time.Second
	r.state(st)
	if strings.Contains(r.text(), "Up next in") {
		t.Error("a card far from the end")
	}
	last := playing(ts, 2)
	last.Duration, last.Position = 200*time.Second, 195*time.Second
	r.state(last)
	if strings.Contains(r.text(), "Up next in") {
		t.Error("a card after the last track with no repeat")
	}
	last.Repeat = music.RepeatAll
	r.state(last)
	if !strings.Contains(r.text(), "Up next in 5s") {
		t.Errorf("no card with repeat all:\n%s", r.text())
	}
}

func TestKeysConfirmThemselvesOnTheStatusLineForAMoment(t *testing.T) {
	ck := &fakeClock{t: time.Unix(1000, 0)}
	r := newRig(t, 100, 30)
	r.m = New(Deps{Source: r.source, Player: r.player, Now: ck.now})
	r.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	r.state(playing(tracks(3), 0)) // volume 40
	r.key("+")
	if text := r.text(); !strings.Contains(text, "Volume") || !strings.Contains(text, "45%") {
		t.Errorf("no volume toast:\n%s", text)
	}
	ck.t = ck.t.Add(3 * time.Second)
	if strings.Contains(r.text(), "Volume █") {
		t.Error("the toast stayed")
	}
	r.key("right")
	if !strings.Contains(r.text(), "Seek +5s") {
		t.Errorf("no seek toast:\n%s", r.text())
	}
	r.key("s")
	if !strings.Contains(r.text(), "Shuffle") {
		t.Errorf("no shuffle toast:\n%s", r.text())
	}
}

func TestInAStripOfTheTerminalThePlayerIsOneLineAndABar(t *testing.T) {
	for _, h := range []int{1, 2, 3, 5} {
		r := newRig(t, 60, h)
		r.state(playing(tracks(3), 0))
		lines := r.screen()
		if len(lines) != h {
			t.Fatalf("%d rows: %d lines", h, len(lines))
		}
		text := r.text()
		if !strings.Contains(text, "Song 1") || strings.Contains(text, "too small") {
			t.Errorf("%d rows:\n%s", h, text)
		}
		if h >= 2 && !strings.Contains(strings.Split(text, "\n")[1], "0:50") {
			t.Errorf("%d rows: no bar:\n%s", h, text)
		}
		for _, l := range lines {
			if ansi.StringWidth(l) > 60 {
				t.Errorf("wide line %q", l)
			}
		}
	}
	r := newRig(t, 60, 3)
	r.key(" ")
	if len(r.player.log()) == 0 {
		t.Error("keys do not work in the strip")
	}
}

// TestWriteStills renders the Player screen for the design review: PIG_MUSIC_STILLS=<dir> writes each still as an .ansi file
// (the escapes as drawn) and a .txt (plain). Not a test otherwise. The lyrics are invented.
func TestWriteStills(t *testing.T) {
	dir := os.Getenv("PIG_MUSIC_STILLS")
	if dir == "" {
		t.Skip("PIG_MUSIC_STILLS names the directory for the stills")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	words := []string{"Neon rain on the window glass", "we were counting every light", "the city hums a quiet song",
		"and nobody sleeps tonight", "", "Hold on, hold on to the sound", "turn it up and let it ring",
		"every heartbeat on the ground", "is a drum for everything", "", "Neon rain on the window glass",
		"we were counting every light", "the city hums a quiet song", "and nobody sleeps tonight"}
	var sheet music.Lyrics
	sheet.Synced, sheet.Source = true, "lrclib"
	for i, w := range words {
		sheet.Lines = append(sheet.Lines, music.LyricLine{At: sec(float64(4 + 4*i)), End: sec(float64(8 + 4*i)), Text: w})
	}
	still := func(name string, w, h int, mutate func(*Deps), st music.State, keys ...string) {
		ts := tracks(8)
		ts[0].Title, ts[0].Artists, ts[0].Album, ts[0].Duration = "Neon Rain", []string{"The Example Band"}, "Night Drive", sec(176)
		ts[1].Title, ts[1].Artists = "Counting Lights", []string{"The Example Band"}
		lp := &stubLyrics{byID: map[string]music.Lyrics{"id00": sheet}}
		r, _ := lyricRig(t, lp, mutate)
		r.send(tea.WindowSizeMsg{Width: w, Height: h})
		st.Queue = ts
		cur := ts[st.Index]
		st.Track = &cur
		r.state(st)
		r.send(ShowPlayerMsg{})
		r.state(st)
		for i := 0; i < 12; i++ {
			r.send(TickMsg{}) // the eased view settles
		}
		r.key(keys...)
		file := filepath.Join(dir, name)
		write := func(ext, s string) {
			if err := os.WriteFile(file+ext, []byte(s), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		write(".ansi", strings.Join(r.screen(), "\n")+"\x1b[0m\n")
		write(".txt", r.text()+"\n")
	}
	mid := music.State{Connected: true, Index: 0, Position: sec(25.5), Duration: sec(176), Volume: 65}
	end := mid
	end.Position = sec(169)
	for _, size := range [][2]int{{80, 24}, {120, 40}, {200, 60}} {
		still(fmt.Sprintf("player-%dx%d", size[0], size[1]), size[0], size[1], nil, mid)
	}
	still("player-120x40-upnext", 120, 40, nil, end)
	still("player-100x4-strip", 100, 4, nil, mid)
	still("player-80x24-nocolor", 80, 24, func(d *Deps) { d.NoColor, d.Vibes = true, false }, mid)
	still("player-120x40-queue", 120, 40, nil, mid, "L")
}
