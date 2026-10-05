package art

import (
	"image"
	"image/color"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func solid(w, h int, f func(x, y int) color.Color) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, f(x, y))
		}
	}
	return img
}

var (
	red   = color.RGBA{255, 0, 0, 255}
	blue  = color.RGBA{0, 0, 255, 255}
	green = color.RGBA{0, 255, 0, 255}
)

func TestModeFromTheEnvironment(t *testing.T) {
	env := func(kv ...string) func(string) string {
		return func(k string) string {
			for i := 0; i+1 < len(kv); i += 2 {
				if kv[i] == k {
					return kv[i+1]
				}
			}
			return ""
		}
	}
	cases := []struct {
		name string
		get  func(string) string
		want Mode
	}{
		{"truecolor", env("COLORTERM", "truecolor", "TERM", "xterm"), TrueColor},
		{"24bit", env("COLORTERM", "24bit"), TrueColor},
		{"256", env("TERM", "xterm-256color"), Color256},
		{"plain xterm", env("TERM", "xterm"), Mono},
		{"xterm-color is 16 colours", env("TERM", "xterm-color"), Color16},
		{"linux console", env("TERM", "linux"), Color16},
		{"screen", env("TERM", "screen"), Color16},
		{"truecolor beats 16", env("TERM", "xterm-color", "COLORTERM", "truecolor"), TrueColor},
		{"NO_COLOR beats 16", env("TERM", "linux", "NO_COLOR", "1"), Mono},
		{"dumb", env("TERM", "dumb", "COLORTERM", "truecolor"), Mono},
		{"nothing set", env(), Mono},
		{"NO_COLOR wins over everything", env("NO_COLOR", "1", "COLORTERM", "truecolor", "TERM", "xterm-256color"), Mono},
	}
	for _, c := range cases {
		if got := ModeFromEnv(c.get); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestHalfBlocksCarryTheUpperPixelAsForegroundAndTheLowerAsBackground(t *testing.T) {
	img := solid(8, 8, func(x, y int) color.Color {
		if y < 4 {
			return red
		}
		return blue
	})
	lines := Render(img, 4, 2, TrueColor)
	if len(lines) != 2 {
		t.Fatalf("%d lines", len(lines))
	}
	if !strings.Contains(lines[0], "38;2;255;0;0") || !strings.Contains(lines[0], "48;2;255;0;0") {
		t.Errorf("row 0 is red over red: %q", lines[0])
	}
	if !strings.Contains(lines[1], "38;2;0;0;255") || !strings.Contains(lines[1], "48;2;0;0;255") {
		t.Errorf("row 1 is blue over blue: %q", lines[1])
	}
	// one cell holds an upper and a lower pixel: with two pixel rows of different colours inside one cell
	mixed := solid(4, 4, func(x, y int) color.Color {
		if y%2 == 0 {
			return red
		}
		return blue
	})
	l := Render(mixed, 2, 2, TrueColor)[0]
	if !strings.Contains(l, "38;2;255;0;0") || !strings.Contains(l, "48;2;0;0;255") {
		t.Errorf("a cell is red above blue: %q", l)
	}
}

func TestEveryLineIsExactlyAsWideAsAsked(t *testing.T) {
	img := solid(37, 91, func(x, y int) color.Color { return color.RGBA{uint8(x * 6), uint8(y * 2), 90, 255} })
	for _, mode := range []Mode{TrueColor, Color256, Mono} {
		for _, size := range [][2]int{{1, 1}, {7, 3}, {20, 10}, {33, 5}} {
			lines := Render(img, size[0], size[1], mode)
			if len(lines) != size[1] {
				t.Fatalf("mode %v size %v: %d lines", mode, size, len(lines))
			}
			for i, l := range lines {
				if w := ansi.StringWidth(l); w != size[0] {
					t.Errorf("mode %v size %v line %d is %d wide", mode, size, i, w)
				}
			}
		}
	}
}

func TestANonSquareImageIsCroppedAtTheCentre(t *testing.T) {
	// 16x8: the middle 8x8 holds left half green, right half red
	img := solid(16, 8, func(x, y int) color.Color {
		switch {
		case x < 4 || x >= 12:
			return color.RGBA{255, 255, 255, 255}
		case x < 8:
			return green
		}
		return red
	})
	line := Render(img, 4, 2, TrueColor)[0]
	plain := ansi.Strip(line)
	if plain != "▀▀▀▀" {
		t.Fatalf("%q", plain)
	}
	if strings.Contains(line, "255;255;255") {
		t.Errorf("the borders were not cropped away: %q", line)
	}
	if i, j := strings.Index(line, "0;255;0"), strings.Index(line, "255;0;0"); i < 0 || j < 0 || i > j {
		t.Errorf("green should come before red: %q", line)
	}
}

func TestTheTwoFiftySixColourModeUsesTheCubeAndNeverTrueColour(t *testing.T) {
	img := solid(4, 4, func(x, y int) color.Color { return red })
	l := Render(img, 2, 2, Color256)[0]
	if !strings.Contains(l, "38;5;196") || strings.Contains(l, "38;2;") {
		t.Errorf("%q", l)
	}
}

func TestTheMonochromeModeWritesNoEscapesAndMapsBrightnessToABlockRamp(t *testing.T) {
	img := solid(8, 8, func(x, y int) color.Color {
		if x < 4 {
			return color.Black
		}
		return color.White
	})
	for _, l := range Render(img, 4, 2, Mono) {
		if strings.Contains(l, "\x1b") {
			t.Fatalf("an escape in monochrome: %q", l)
		}
		if []rune(l)[0] != ' ' || []rune(l)[3] != '█' {
			t.Errorf("black should be blank and white solid: %q", l)
		}
	}
}

func TestDegenerateSizesDrawNothing(t *testing.T) {
	img := solid(4, 4, func(x, y int) color.Color { return red })
	if Render(img, 0, 3, TrueColor) != nil || Render(img, 3, 0, TrueColor) != nil || Render(nil, 3, 3, TrueColor) != nil {
		t.Error("expected nil")
	}
}

func TestTheSixteenColourModeUsesOnlyTheBasicCodes(t *testing.T) {
	img := solid(8, 8, func(x, y int) color.Color { return color.RGBA{uint8(x * 30), uint8(y * 30), 90, 255} })
	re := regexp.MustCompile(`\x1b\[([0-9;]*)m`)
	for _, l := range Render(img, 4, 2, Color16) {
		if ansi.StringWidth(l) != 4 {
			t.Errorf("width %d", ansi.StringWidth(l))
		}
		for _, m := range re.FindAllStringSubmatch(l, -1) {
			for _, part := range strings.Split(m[1], ";") {
				n, _ := strconv.Atoi(part)
				if n != 0 && !(n >= 30 && n <= 37 || n >= 90 && n <= 97 || n >= 40 && n <= 47) {
					t.Errorf("code %d is not a 16-colour code: %q", n, m[0])
				}
			}
		}
	}
}

func TestSixteenColourCodesPickTheNearestBasicColour(t *testing.T) {
	cases := []struct {
		c    RGB
		fg   bool
		want string
	}{
		{RGB{255, 255, 255}, true, "97"},
		{RGB{0, 0, 0}, true, "30"},
		{RGB{255, 0, 0}, true, "91"},
		{RGB{100, 100, 255}, true, "94"},
		{RGB{248, 248, 248}, true, "97"},
		{RGB{10, 20, 60}, false, "40"},    // a dark background is black, never a bright one
		{RGB{120, 10, 10}, false, "41"},   // a dark red
		{RGB{255, 255, 255}, false, "47"}, // backgrounds use the normal colours only (bright backgrounds are not everywhere)
	}
	for _, c := range cases {
		if got := c.c.SGR(c.fg, Color16); got != c.want {
			t.Errorf("%v fg=%v: %q, want %q", c.c, c.fg, got, c.want)
		}
	}
}
