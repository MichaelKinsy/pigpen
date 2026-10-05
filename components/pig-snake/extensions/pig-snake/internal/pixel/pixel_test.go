package pixel

import (
	"image/color"
	"regexp"
	"strings"
	"testing"
)

// Twins of PiG d86eb93 piglets/standard/internal/pixel/pixel_test.go (the six
// upstream cases, same inputs and expectations), plus the cases Pig Snake adds.

var sgr = regexp.MustCompile("\x1b\\[[0-9;]*m")

func cells(line string) int {
	n := 0
	for _, r := range sgr.ReplaceAllString(line, "") {
		n += CellWidth(r)
	}
	return n
}

func TestEncodePairsRowsIntoExactWidthHalfBlocks(t *testing.T) {
	var c Canvas
	c.Resize(5, 3)
	red, blue := color.RGBA{255, 0, 0, 255}, color.RGBA{0, 0, 255, 255}
	c.Rect(0, 0, 5, 1, red)
	c.Rect(0, 1, 5, 2, blue)
	e := Encoder{TrueColor: true}
	lines := e.Encode(&c, nil)
	if len(lines) != 2 {
		t.Fatalf("3 pixel rows encode to %d lines, want 2", len(lines))
	}
	for i, line := range lines {
		if cells(line) != 5 || !strings.HasSuffix(line, "\x1b[0m") {
			t.Fatalf("line %d = %q", i, line)
		}
	}
	if want := "\x1b[38;2;255;0;0m\x1b[48;2;0;0;255m▀▀▀▀▀" + "\x1b[0m"; lines[0] != want {
		t.Fatalf("line 0 = %q, want %q (colors emitted once per run)", lines[0], want)
	}
}

func TestEncodeFallsBackTo256Colors(t *testing.T) {
	var c Canvas
	c.Resize(2, 2)
	c.Rect(0, 0, 2, 2, color.RGBA{0x48, 0xA3, 0x81, 0xFF})
	lines := (&Encoder{}).Encode(&c, nil)
	if strings.Contains(lines[0], "38;2;") || !strings.Contains(lines[0], "\x1b[38;5;") {
		t.Fatalf("256-color line = %q", lines[0])
	}
}

func TestEncodeReusesUnchangedLinesWithoutAllocating(t *testing.T) {
	var c Canvas
	c.Resize(80, 40)
	c.Rect(0, 0, 80, 40, color.RGBA{10, 20, 30, 255})
	e := Encoder{TrueColor: true}
	dst := make([]string, 0, 20)
	dst = e.Encode(&c, dst[:0])
	if allocs := testing.AllocsPerRun(20, func() { dst = e.Encode(&c, dst[:0]) }); allocs != 0 {
		t.Fatalf("re-encoding an unchanged canvas allocates %.1f times", allocs)
	}
	c.Set(3, 7, color.RGBA{200, 0, 0, 255})
	if allocs := testing.AllocsPerRun(1, func() {
		c.Set(3, 7, color.RGBA{uint8(len(dst)), 1, 0, 255})
		dst = e.Encode(&c, dst[:0])
	}); allocs > 1 {
		t.Fatalf("one changed line allocates %.1f times, want at most 1", allocs)
	}
}

func TestFitLinePadsTruncatesAndKeepsStyles(t *testing.T) {
	if got := FitLine("\x1b[1mab\x1b[0m", 4); got != "\x1b[1mab\x1b[0m"+"\x1b[0m"+"  " {
		t.Fatalf("pad = %q", got)
	}
	if got := FitLine("abc💥d", 4); cells(got) != 4 || strings.Contains(got, "💥") || !strings.HasPrefix(got, "abc") {
		t.Fatalf("a wide rune that does not fit must be dropped: %q", got)
	}
	if got := FitLine("abcdef", 3); cells(got) != 3 {
		t.Fatalf("truncate = %q", got)
	}
}

func TestDrawTextUsesThePixelFont(t *testing.T) {
	var c Canvas
	c.Resize(TextWidth("HI"), GlyphHeight)
	ink := color.RGBA{255, 255, 255, 255}
	c.DrawText(0, 0, "hi", ink, color.RGBA{})
	if c.At(0, 0) != ink || c.At(1, 0) == ink || c.At(4, 0) != ink || c.At(5, 4) != ink {
		t.Fatal("lowercase text did not draw the uppercase H and I glyphs")
	}
}

func TestRGBTo256MatchesTheXtermPalette(t *testing.T) {
	for _, tc := range []struct {
		c    color.RGBA
		want uint8
	}{
		{color.RGBA{0, 0, 0, 255}, 16},
		{color.RGBA{255, 255, 255, 255}, 231},
		{color.RGBA{128, 128, 128, 255}, 244},
		{color.RGBA{255, 0, 0, 255}, 196},
	} {
		if got := RGBTo256(tc.c); got != tc.want {
			t.Fatalf("RGBTo256(%v) = %d, want %d", tc.c, got, tc.want)
		}
	}
}

// Pig Snake additions.

func TestCanvasClipsAndSkipsTransparentPixels(t *testing.T) {
	var c Canvas
	c.Resize(3, 3)
	red := color.RGBA{255, 0, 0, 255}
	c.Set(-1, 0, red)
	c.Set(3, 0, red)
	c.Set(0, 3, red)
	c.Rect(-2, -2, 4, 4, red) // covers only (0,0) and (1,1) inside, plus row/col 0..1
	if c.At(0, 0) != red || c.At(1, 1) != red || c.At(2, 2) == red {
		t.Fatalf("Rect clipping wrong: %v %v %v", c.At(0, 0), c.At(1, 1), c.At(2, 2))
	}
	c.Set(2, 2, color.RGBA{}) // transparent never draws
	if c.At(2, 2) != (color.RGBA{}) {
		t.Fatal("transparent pixel drew")
	}
	if c.At(9, 9) != (color.RGBA{}) {
		t.Fatal("At outside the canvas must be transparent")
	}
}

func TestBlitDrawsThroughThePaletteAndLeavesTransparentCells(t *testing.T) {
	var c Canvas
	c.Resize(4, 2)
	bg := color.RGBA{1, 2, 3, 255}
	c.Rect(0, 0, 4, 2, bg)
	pal := NewPalette(map[byte]color.RGBA{'A': {9, 9, 9, 255}, '.': {}})
	c.Blit(1, 0, []string{"A.", ".A"}, pal)
	if c.At(1, 0) != (color.RGBA{9, 9, 9, 255}) || c.At(2, 0) != bg || c.At(2, 1) != (color.RGBA{9, 9, 9, 255}) {
		t.Fatalf("blit result wrong: %v %v %v", c.At(1, 0), c.At(2, 0), c.At(2, 1))
	}
}

func TestLerpAndMixBlendChannels(t *testing.T) {
	a, b := color.RGBA{0, 0, 0, 255}, color.RGBA{200, 100, 50, 255}
	if got := Lerp(a, b, 0.5); got != (color.RGBA{100, 50, 25, 255}) {
		t.Fatalf("Lerp = %v", got)
	}
	if got := Mix(a, b, 255); got != b {
		t.Fatalf("Mix alpha 255 = %v", got)
	}
	if got := Mix(a, b, 0); got != a {
		t.Fatalf("Mix alpha 0 = %v", got)
	}
}

func TestSupportsTrueColorReadsTheEnvironment(t *testing.T) {
	t.Setenv("WT_SESSION", "")
	for value, want := range map[string]bool{"truecolor": true, "24BIT": true, "256color": false, "": false} {
		t.Setenv("COLORTERM", value)
		if got := SupportsTrueColor(); got != want {
			t.Errorf("COLORTERM=%q: %v, want %v", value, got, want)
		}
	}
	t.Setenv("COLORTERM", "")
	t.Setenv("WT_SESSION", "x")
	if !SupportsTrueColor() {
		t.Error("WT_SESSION should imply true color")
	}
}
