package scene

import (
	"image/color"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/pig-play/libraries/pixel"
)

// Cases Pig Snake added to its own copy of the pixel package. The package is now the shared one (pig-play, libraries/pixel,
// which carries the six upstream twins); these keep the behaviour the scene depends on pinned from this side.

func TestCanvasClipsAndSkipsTransparentPixels(t *testing.T) {
	var c pixel.Canvas
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
	var c pixel.Canvas
	c.Resize(4, 2)
	bg := color.RGBA{1, 2, 3, 255}
	c.Rect(0, 0, 4, 2, bg)
	pal := pixel.NewPalette(map[byte]color.RGBA{'A': {9, 9, 9, 255}, '.': {}})
	c.Blit(1, 0, []string{"A.", ".A"}, pal)
	if c.At(1, 0) != (color.RGBA{9, 9, 9, 255}) || c.At(2, 0) != bg || c.At(2, 1) != (color.RGBA{9, 9, 9, 255}) {
		t.Fatalf("blit result wrong: %v %v %v", c.At(1, 0), c.At(2, 0), c.At(2, 1))
	}
}

func TestLerpAndMixBlendChannels(t *testing.T) {
	a, b := color.RGBA{0, 0, 0, 255}, color.RGBA{200, 100, 50, 255}
	if got := pixel.Lerp(a, b, 0.5); got != (color.RGBA{100, 50, 25, 255}) {
		t.Fatalf("Lerp = %v", got)
	}
	if got := pixel.Mix(a, b, 255); got != b {
		t.Fatalf("Mix alpha 255 = %v", got)
	}
	if got := pixel.Mix(a, b, 0); got != a {
		t.Fatalf("Mix alpha 0 = %v", got)
	}
}

func TestSupportsTrueColorReadsTheEnvironment(t *testing.T) {
	t.Setenv("WT_SESSION", "")
	for value, want := range map[string]bool{"truecolor": true, "24BIT": true, "256color": false, "": false} {
		t.Setenv("COLORTERM", value)
		if got := pixel.SupportsTrueColor(); got != want {
			t.Errorf("COLORTERM=%q: %v, want %v", value, got, want)
		}
	}
	t.Setenv("COLORTERM", "")
	t.Setenv("WT_SESSION", "x")
	if !pixel.SupportsTrueColor() {
		t.Error("WT_SESSION should imply true color")
	}
}
