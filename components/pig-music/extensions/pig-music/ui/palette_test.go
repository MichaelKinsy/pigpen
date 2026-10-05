package ui

import (
	"fmt"
	"image"
	"image/color"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/MichaelKinsy/pigpen/pig-music/art"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// M8a: the overlay's colours come from the track. A dark gradient background and accent from the cover (or from the
// metadata when there is none), readable text on every row, and a cross-fade when the track changes.

func vibeRig(t *testing.T, w, h int, a music.Artwork, mode art.Mode) (*rig, *ticker) {
	t.Helper()
	r := newRig(t, w, h)
	k := &ticker{}
	r.m = New(Deps{Source: r.source, Player: r.player, Art: a, ArtMode: mode, Tick: k.tick, Vibes: true})
	r.send(tea.WindowSizeMsg{Width: w, Height: h})
	return r, k
}

var sgrRe = regexp.MustCompile(`\x1b\[([0-9;]*)m`)

// firstColours reads the foreground and background that open a line (24-bit).
func firstColours(t *testing.T, line string) (fg, bg art.RGB) {
	t.Helper()
	m := sgrRe.FindStringSubmatch(line)
	if m == nil {
		t.Fatalf("no colour opens the line %q", ansi.Strip(line))
	}
	var f, b [3]int
	if n, _ := fmt.Sscanf(m[1], "38;2;%d;%d;%d;48;2;%d;%d;%d", &f[0], &f[1], &f[2], &b[0], &b[1], &b[2]); n != 6 {
		t.Fatalf("the line does not open with a 24-bit foreground and background: %q", m[1])
	}
	return art.RGB{R: uint8(f[0]), G: uint8(f[1]), B: uint8(f[2])}, art.RGB{R: uint8(b[0]), G: uint8(b[1]), B: uint8(b[2])}
}

func redCover() *fakeArt {
	return &fakeArt{imgs: map[string]image.Image{
		"id00": paint(color.RGBA{200, 30, 40, 255}),
		"id01": paint(color.RGBA{30, 60, 210, 255}),
	}}
}

func TestEveryLineOpensWithAReadableTextColourOnItsRowOfTheGradient(t *testing.T) {
	r, _ := vibeRig(t, 100, 30, nil, art.TrueColor)
	r.state(playing(tracks(3), 0))
	r.send(ShowPlayerMsg{})
	lines := r.screen()
	if len(lines) != 30 {
		t.Fatalf("%d lines", len(lines))
	}
	pal := art.FromTrack("Artist", "Song 1")
	var prev art.RGB
	for i, l := range lines {
		fg, bg := firstColours(t, l)
		if want := pal.At(i, 30); bg != want {
			t.Errorf("row %d background %v, want %v", i, bg, want)
		}
		if c := art.Contrast(fg, bg); c < 7 {
			t.Errorf("row %d text contrast %.2f", i, c)
		}
		if i > 0 && bg == prev && i < 29 && pal.Top != pal.Bottom && i%7 == 0 {
			t.Logf("rows %d and %d share a background (a gradient has few steps in few rows)", i-1, i)
		}
		prev = bg
	}
}

func TestEveryResetInsideALineGoesBackToTheBackground(t *testing.T) {
	r, _ := vibeRig(t, 100, 30, nil, art.TrueColor)
	r.state(playing(tracks(3), 0))
	r.send(ShowPlayerMsg{})
	for i, l := range r.screen() {
		parts := strings.Split(l, "\x1b[m")
		for j := 1; j < len(parts)-1; j++ { // after the last reset the line ends
			if !strings.HasPrefix(parts[j], "\x1b[38;2;") {
				t.Errorf("row %d: a reset is followed by %q, not by the line's own colours", i, firstN(parts[j], 12))
				break
			}
		}
		if !strings.HasSuffix(l, "\x1b[m") {
			t.Errorf("row %d does not end with a reset", i)
		}
	}
}

func firstN(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func TestWidthsAndHeightsAreUnchangedByThePalette(t *testing.T) {
	fa := redCover()
	for _, sz := range [][2]int{{120, 40}, {80, 24}, {60, 16}, {40, 12}, {30, 8}, {24, 6}, {200, 10}} {
		for _, mode := range []art.Mode{art.TrueColor, art.Color256} {
			r, _ := vibeRig(t, sz[0], sz[1], fa, mode)
			r.state(playing(tracks(30), 0))
			r.send(ShowPlayerMsg{})
			lines := r.screen()
			if len(lines) != sz[1] {
				t.Errorf("%v: %d lines", sz, len(lines))
			}
			for i, l := range lines {
				if w := ansi.StringWidth(l); w != sz[0] {
					t.Errorf("%v mode %v line %d is %d wide", sz, mode, i, w)
					break
				}
			}
		}
	}
}

func TestWithoutColourOrWithTheSwitchOffNothingIsPainted(t *testing.T) {
	r, _ := vibeRig(t, 100, 30, nil, art.Mono) // NO_COLOR, a plain terminal
	r.state(playing(tracks(3), 0))
	r.send(ShowPlayerMsg{})
	for _, l := range r.screen() {
		if strings.Contains(l, "48;2;") || strings.Contains(l, "48;5;") {
			t.Fatalf("a background in monochrome: %q", l)
		}
	}
	off := newRig(t, 100, 30) // Vibes off, as in every other test
	off.m = New(Deps{Source: off.source, Player: off.player, ArtMode: art.TrueColor})
	off.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	off.state(playing(tracks(3), 0))
	for _, l := range off.screen() {
		if strings.Contains(l, "48;2;") {
			t.Fatalf("a background with the palette switched off: %q", l)
		}
	}
}

func TestThe256ColourFallbackUsesPaletteIndices(t *testing.T) {
	r, _ := vibeRig(t, 100, 30, nil, art.Color256)
	r.state(playing(tracks(3), 0))
	r.send(ShowPlayerMsg{})
	l := r.screen()[0]
	if !strings.Contains(l, "48;5;") || strings.Contains(l, "48;2;") {
		t.Errorf("%q", l)
	}
}

func TestTheFirstPaletteShowsAtOnceWithoutAFade(t *testing.T) {
	r, k := vibeRig(t, 100, 30, redCover(), art.TrueColor)
	r.state(playing(tracks(3), 0))
	r.send(ShowPlayerMsg{})
	cover := art.Derive(art.Extract(paint(color.RGBA{200, 30, 40, 255}), 4))
	if _, bg := firstColours(t, r.screen()[0]); bg != cover.Top {
		t.Errorf("%v, want the cover's %v", bg, cover.Top)
	}
	if k.count() > 1 { // the disc's one tick, but no fade
		t.Errorf("%d ticks for a first palette", k.count())
	}
}

func TestAChangeOfTrackCrossFadesInEvenFinerStepsThanTheDisc(t *testing.T) {
	r, k := vibeRig(t, 100, 30, redCover(), art.TrueColor)
	r.state(playing(tracks(3), 0))
	r.send(ShowPlayerMsg{})
	for i := 0; i < 3; i++ {
		r.send(TickMsg{})
	}
	_, settled := firstColours(t, r.screen()[0])
	r.state(playing(tracks(3), 1))
	_, first := firstColours(t, r.screen()[0])
	if first != settled {
		t.Fatalf("a track change must not jump: %v then %v", settled, first)
	}
	next := art.Derive(art.Extract(paint(color.RGBA{30, 60, 210, 255}), 4))
	var seen []art.RGB
	for i := 0; i < 8; i++ {
		r.send(TickMsg{})
		if i == 0 && k.d[len(k.d)-1] > 100*time.Millisecond { // the tick already on its way was the disc's; the next ones are the fade's
			t.Errorf("the fade ticks every %v; it should be finer than the disc's", k.d[len(k.d)-1])
		}
		_, bg := firstColours(t, r.screen()[0])
		seen = append(seen, bg)
	}
	if seen[len(seen)-1] != next.Top {
		t.Errorf("the fade ends on the new cover's palette: %v, want %v", seen[len(seen)-1], next.Top)
	}
	if seen[0] == settled || seen[0] == next.Top {
		t.Errorf("the first step is neither end: %v", seen[0])
	}
	distinct := map[art.RGB]bool{}
	for _, c := range seen {
		distinct[c] = true
	}
	if len(distinct) < 4 {
		t.Errorf("only %d different colours in a fade of 8 steps: %v", len(distinct), seen)
	}
}

func TestEveryStepOfTheFadeStaysReadable(t *testing.T) {
	r, _ := vibeRig(t, 100, 30, redCover(), art.TrueColor)
	r.state(playing(tracks(3), 0))
	r.send(ShowPlayerMsg{})
	r.state(playing(tracks(3), 1)) // a fade from the red cover to the blue one
	for step := 0; step < 9; step++ {
		for i, l := range r.screen() {
			fg, bg := firstColours(t, l)
			if c := art.Contrast(fg, bg); c < 7 {
				t.Fatalf("step %d row %d: contrast %.2f", step, i, c)
			}
		}
		r.send(TickMsg{})
	}
}

func TestAMissingCoverKeepsTheMetadataPalette(t *testing.T) {
	r, _ := vibeRig(t, 100, 30, &fakeArt{}, art.TrueColor) // every fetch fails
	r.state(playing(tracks(3), 0))
	r.send(ShowPlayerMsg{})
	for i := 0; i < 10; i++ {
		r.send(TickMsg{})
	}
	if _, bg := firstColours(t, r.screen()[0]); bg != art.FromTrack("Artist", "Song 1").Top {
		t.Errorf("%v", bg)
	}
}

func TestAHiddenScreenSnapsToTheNewPaletteWithoutAnimating(t *testing.T) {
	r, k := vibeRig(t, 100, 30, redCover(), art.TrueColor)
	r.state(playing(tracks(3), 0))
	r.send(ShowPlayerMsg{})
	r.send(ClosedMsg{})
	n := k.count()
	r.state(playing(tracks(3), 1))
	if k.count() != n {
		t.Errorf("a tick was asked for while hidden")
	}
	r.send(OpenedMsg{})
	want := art.Derive(art.Extract(paint(color.RGBA{30, 60, 210, 255}), 4))
	if _, bg := firstColours(t, r.screen()[0]); bg != want.Top {
		t.Errorf("after showing again: %v, want the new cover's %v", bg, want.Top)
	}
}

func TestTheAccentColoursTheProgressBarTheTitleAndTheDisc(t *testing.T) {
	r, _ := vibeRig(t, 100, 30, nil, art.TrueColor)
	r.state(playing(tracks(3), 0))
	r.send(ShowPlayerMsg{})
	acc := art.FromTrack("Artist", "Song 1").Accent.SGR(true, art.TrueColor)
	lines := r.screen()
	bar := lines[len(lines)-3] // the play bar sits above the status line and the hints
	if !strings.Contains(bar, acc) {
		t.Errorf("the progress bar lacks the accent %s: %q", acc, bar)
	}
	var title, disc bool
	for _, l := range lines {
		plain := ansi.Strip(l)
		if strings.Contains(plain, "Song 1") && strings.Contains(l, acc) && strings.Contains(plain, "Now Playing") == false && !strings.Contains(plain, "1.") {
			title = true
		}
		if hasBraille(plain) && strings.Contains(l, acc) {
			disc = true
		}
	}
	if !title || !disc {
		t.Errorf("title in the accent: %v, disc in the accent: %v\n%s", title, disc, strings.Join(lines, "\n"))
	}
}

func TestDimTextUsesThePalettesDimColourNotTheFaintAttribute(t *testing.T) {
	r, _ := vibeRig(t, 100, 30, nil, art.TrueColor)
	r.state(playing(tracks(3), 0))
	r.send(ShowPlayerMsg{})
	hints := r.screen()[29]
	if strings.Contains(hints, "\x1b[2m") {
		t.Errorf("the faint attribute lowers contrast below what was checked: %q", hints)
	}
	dimSeq := art.FromTrack("Artist", "Song 1").Dim.SGR(true, art.TrueColor)
	if !strings.Contains(hints, dimSeq) {
		t.Errorf("the hints are not in the dim colour %s: %q", dimSeq, hints)
	}
}

func TestNoEscapeBeyondColoursIsEverWritten(t *testing.T) {
	r, _ := vibeRig(t, 100, 30, redCover(), art.TrueColor)
	r.state(playing(tracks(3), 0))
	r.send(ShowPlayerMsg{})
	raw := strings.Join(r.screen(), "\n")
	for _, bad := range []string{"\x1b]", "\x1b_", "\x1bP", "\x1b[?", "\x1b[2J", "\x1b[H"} { // OSC (including 11), APC, DCS, modes, clear, home
		if strings.Contains(raw, bad) {
			t.Errorf("an escape %q that is not a colour", bad)
		}
	}
	for _, m := range sgrRe.FindAllStringSubmatch(raw, -1) {
		for _, p := range strings.Split(m[1], ";") {
			if _, err := strconv.Atoi(p); p != "" && err != nil {
				t.Errorf("a malformed SGR %q", m[0])
			}
		}
	}
}

func color_(r, g, b uint8) color.Color { return color.RGBA{r, g, b, 255} }
