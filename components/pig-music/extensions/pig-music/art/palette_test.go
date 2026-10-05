package art

import (
	"fmt"
	"image"
	"image/color"
	"math/rand"
	"testing"
)

func approx(t *testing.T, what string, got, want, tol float64) {
	t.Helper()
	if got < want-tol || got > want+tol {
		t.Errorf("%s = %.3f, want %.3f ± %.3f", what, got, want, tol)
	}
}

func TestContrastFollowsTheWCAGFormula(t *testing.T) {
	approx(t, "black on white", Contrast(RGB{0, 0, 0}, RGB{255, 255, 255}), 21, 0.01)
	approx(t, "white on black", Contrast(RGB{255, 255, 255}, RGB{0, 0, 0}), 21, 0.01)
	approx(t, "same", Contrast(RGB{90, 20, 200}, RGB{90, 20, 200}), 1, 0.0001)
	// #777 on white is the classic 4.48 : 1
	approx(t, "grey 777 on white", Contrast(RGB{0x77, 0x77, 0x77}, RGB{255, 255, 255}), 4.48, 0.02)
}

// What a palette promises, whatever it was made from: readable text on every row of the gradient.
func checkPalette(t *testing.T, name string, p Palette) {
	t.Helper()
	for row := 0; row <= 10; row++ {
		bg := p.At(row, 11)
		for what, c := range map[string]RGB{"text": p.Text} {
			if r := Contrast(c, bg); r < 7 {
				t.Errorf("%s: %s on row %d has contrast %.2f, want >= 7", name, what, row, r)
			}
		}
		for what, c := range map[string]RGB{"dim": p.Dim, "accent": p.Accent, "error": p.Error} {
			if r := Contrast(c, bg); r < 4.5 {
				t.Errorf("%s: %s on row %d has contrast %.2f, want >= 4.5", name, what, row, r)
			}
		}
	}
}

func TestExtractFindsTheDominantColoursMostPopulousFirst(t *testing.T) {
	img := solid(40, 40, func(x, y int) color.Color {
		if x < 28 {
			return color.RGBA{200, 30, 30, 255} // 70% red
		}
		return color.RGBA{30, 40, 210, 255} // 30% blue
	})
	got := Extract(img, 4)
	if len(got) < 2 {
		t.Fatalf("%v", got)
	}
	if !(got[0].R > 150 && got[0].B < 80) {
		t.Errorf("the most populous colour should be the red: %v", got)
	}
	foundBlue := false
	for _, c := range got[1:] {
		if c.B > 150 && c.R < 80 {
			foundBlue = true
		}
	}
	if !foundBlue {
		t.Errorf("no blue in %v", got)
	}
}

func TestExtractGivesOneColourForASolidImageAndNeverMoreThanAsked(t *testing.T) {
	if got := Extract(solid(30, 30, func(x, y int) color.Color { return color.RGBA{10, 200, 90, 255} }), 4); len(got) != 1 {
		t.Errorf("%v", got)
	}
	rnd := rand.New(rand.NewSource(1))
	noise := solid(60, 60, func(x, y int) color.Color {
		return color.RGBA{uint8(rnd.Intn(256)), uint8(rnd.Intn(256)), uint8(rnd.Intn(256)), 255}
	})
	for k := 1; k <= 5; k++ {
		if got := Extract(noise, k); len(got) < 1 || len(got) > k {
			t.Errorf("k=%d: %d colours", k, len(got))
		}
	}
	if Extract(nil, 3) != nil {
		t.Error("nil image should give nil")
	}
}

func TestExtractIgnoresTheBlackBarsAroundArtAndIsDeterministic(t *testing.T) {
	// a 16:9 thumbnail: black pillars left and right, a green square in the middle (the centre square is the art)
	img := solid(160, 90, func(x, y int) color.Color {
		if x < 35 || x >= 125 {
			return color.Black
		}
		return color.RGBA{20, 180, 60, 255}
	})
	a, b := Extract(img, 3), Extract(img, 3)
	if fmt.Sprint(a) != fmt.Sprint(b) {
		t.Errorf("not deterministic: %v %v", a, b)
	}
	if len(a) == 0 || !(a[0].G > 120 && a[0].R < 80) {
		t.Errorf("the art is green: %v", a)
	}
}

func TestEveryDerivedPaletteIsReadableWhateverTheArtWas(t *testing.T) {
	cases := map[string][]RGB{
		"white":   {{255, 255, 255}},
		"black":   {{0, 0, 0}},
		"yellow":  {{255, 255, 0}},
		"pastel":  {{250, 220, 230}, {220, 240, 250}},
		"red":     {{220, 20, 30}, {30, 30, 30}},
		"grey":    {{128, 128, 128}, {140, 140, 140}},
		"nothing": nil,
		"four":    {{255, 0, 0}, {0, 255, 0}, {0, 0, 255}, {255, 255, 255}},
	}
	for name, colors := range cases {
		checkPalette(t, name, Derive(colors))
	}
	rnd := rand.New(rand.NewSource(7))
	for i := 0; i < 300; i++ {
		n := 1 + rnd.Intn(4)
		var cs []RGB
		for j := 0; j < n; j++ {
			cs = append(cs, RGB{uint8(rnd.Intn(256)), uint8(rnd.Intn(256)), uint8(rnd.Intn(256))})
		}
		checkPalette(t, fmt.Sprint("random ", cs), Derive(cs))
	}
}

func TestADerivedPaletteKeepsTheHueOfTheArt(t *testing.T) {
	p := Derive([]RGB{{200, 30, 40}, {60, 20, 30}})
	if !(p.Top.R > p.Top.G && p.Top.R > p.Top.B) {
		t.Errorf("a red cover should give a reddish background: %+v", p.Top)
	}
	if p.Top == p.Bottom {
		t.Error("the gradient has two ends")
	}
	b := Derive([]RGB{{30, 40, 200}})
	if !(b.Top.B > b.Top.R && b.Top.B > b.Top.G) {
		t.Errorf("a blue cover should give a bluish background: %+v", b.Top)
	}
}

func TestTheAccentIsTheMostVividColourOfTheArt(t *testing.T) {
	p := Derive([]RGB{{90, 90, 90}, {240, 40, 150}, {20, 20, 20}})
	if !(p.Accent.R > p.Accent.G+60 && p.Accent.B > p.Accent.G) {
		t.Errorf("the accent should be the pink, not the grey: %+v", p.Accent)
	}
}

func TestAPaletteFromTheTracksMetadataIsDeterministicDistinctAndReadable(t *testing.T) {
	a, b := FromTrack("Artist One", "Song"), FromTrack("Artist One", "Song")
	if a != b {
		t.Error("not deterministic")
	}
	seen := map[RGB]bool{}
	for _, artist := range []string{"A", "B", "C", "D", "E", "F", "G", "H"} {
		p := FromTrack(artist, "x")
		checkPalette(t, artist, p)
		seen[p.Top] = true
	}
	if len(seen) < 5 {
		t.Errorf("only %d different backgrounds for 8 artists", len(seen))
	}
	checkPalette(t, "empty", FromTrack("", ""))
}

func TestMixFadesBetweenPalettesAndStaysReadableAtEveryStep(t *testing.T) {
	a, b := Derive([]RGB{{240, 240, 20}}), Derive([]RGB{{20, 20, 240}})
	if Mix(a, b, 0) != a || Mix(a, b, 1) != b {
		t.Error("the ends of the fade are the two palettes")
	}
	if Mix(a, b, -3) != a || Mix(a, b, 9) != b {
		t.Error("t is clamped")
	}
	rnd := rand.New(rand.NewSource(3))
	for i := 0; i < 50; i++ {
		x := Derive([]RGB{{uint8(rnd.Intn(256)), uint8(rnd.Intn(256)), uint8(rnd.Intn(256))}})
		y := Derive([]RGB{{uint8(rnd.Intn(256)), uint8(rnd.Intn(256)), uint8(rnd.Intn(256))}})
		for step := 0; step <= 8; step++ {
			checkPalette(t, fmt.Sprintf("mix %d step %d", i, step), Mix(x, y, float64(step)/8))
		}
	}
	mid := Mix(a, b, 0.5)
	if mid == a || mid == b {
		t.Error("the middle of a fade is neither end")
	}
}

func TestTheGradientRunsFromTheTopToTheBottomColour(t *testing.T) {
	p := Derive([]RGB{{200, 30, 40}, {30, 40, 200}})
	if p.At(0, 20) != p.Top || p.At(19, 20) != p.Bottom || p.At(0, 1) != p.Top {
		t.Errorf("%v %v %v", p.At(0, 20), p.At(19, 20), p.At(0, 1))
	}
	mid := p.At(10, 21)
	if mid == p.Top || mid == p.Bottom {
		t.Errorf("the middle row is a blend: %v", mid)
	}
	if p.At(-5, 20) != p.Top || p.At(99, 20) != p.Bottom {
		t.Error("rows outside are clamped")
	}
}

func TestEscapesFollowTheColourMode(t *testing.T) {
	c := RGB{10, 20, 30}
	if got := c.SGR(true, TrueColor); got != "38;2;10;20;30" {
		t.Errorf("%q", got)
	}
	if got := c.SGR(false, TrueColor); got != "48;2;10;20;30" {
		t.Errorf("%q", got)
	}
	if got := (RGB{255, 0, 0}).SGR(true, Color256); got != "38;5;196" {
		t.Errorf("%q", got)
	}
	if got := (RGB{255, 0, 0}).SGR(false, Color256); got != "48;5;196" {
		t.Errorf("%q", got)
	}
	if got := c.SGR(true, Mono); got != "" {
		t.Errorf("no colour at all in monochrome: %q", got)
	}
}

var _ image.Image

func TestAPulsedPaletteIsBrighterAndStillReadableAtEveryStrength(t *testing.T) {
	base := Derive([]RGB{{40, 60, 120}, {20, 20, 60}})
	if base.Pulsed(0) != base {
		t.Error("no pulse, no change")
	}
	if Luminance(base.Pulsed(1).Top) <= Luminance(base.Top) {
		t.Errorf("a full pulse should brighten the background: %v -> %v", base.Top, base.Pulsed(1).Top)
	}
	rnd := rand.New(rand.NewSource(11))
	for i := 0; i < 100; i++ {
		p := Derive([]RGB{{uint8(rnd.Intn(256)), uint8(rnd.Intn(256)), uint8(rnd.Intn(256))}, {uint8(rnd.Intn(256)), uint8(rnd.Intn(256)), uint8(rnd.Intn(256))}})
		for _, k := range []float64{0, 0.25, 0.5, 0.75, 1, 3, -1} {
			checkPalette(t, fmt.Sprintf("pulse %v of %v", k, p.Top), p.Pulsed(k))
		}
	}
}

func TestThePulseStaysWithinItsBrightnessCap(t *testing.T) {
	for _, c := range []RGB{{255, 255, 255}, {0, 0, 255}, {255, 255, 0}, {0, 0, 0}} {
		p := Derive([]RGB{c}).Pulsed(1)
		if Luminance(p.Top) > 0.0851 || Luminance(p.Bottom) > 0.0851 {
			t.Errorf("%v: background luminance %.3f %.3f is over the cap", c, Luminance(p.Top), Luminance(p.Bottom))
		}
	}
}
