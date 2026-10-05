package art

import (
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

// Review of M8: the colours limited terminals draw keep the palette's promise (TextSGR never darker, BackSGR never lighter).

func shownOf(t *testing.T, sgr string) RGB {
	t.Helper()
	ps := strings.Split(sgr, ";")
	n := func(i int) int { v, _ := strconv.Atoi(ps[i]); return v }
	switch {
	case len(ps) == 3 && ps[1] == "5":
		return shownRGB[n(2)]
	case len(ps) == 1 && n(0) >= 90:
		return shownRGB[n(0)-90+8]
	case len(ps) == 1 && n(0) >= 40:
		return shownRGB[n(0)-40]
	case len(ps) == 1:
		return shownRGB[n(0)-30]
	}
	t.Fatalf("not a 256- or 16-colour code: %q", sgr)
	return RGB{}
}

func TestPalettesKeepTheirContrastInTheColoursA256Or16ColourTerminalDraws(t *testing.T) {
	r := rand.New(rand.NewSource(88))
	for _, mode := range []Mode{Color256, Color16} {
		for i := 0; i < 400; i++ {
			var cs []RGB
			for j := 0; j <= r.Intn(4); j++ {
				cs = append(cs, RGB{uint8(r.Intn(256)), uint8(r.Intn(256)), uint8(r.Intn(256))})
			}
			p := Derive(cs).Pulsed(r.Float64())
			for row := 0; row < 24; row++ {
				bg := shownOf(t, p.At(row, 24).BackSGR(mode))
				if c := Contrast(shownOf(t, p.Text.TextSGR(mode)), bg); c < textContrast {
					t.Fatalf("mode %d: text %.2f on row %d of %+v", mode, c, row, p)
				}
				for _, fg := range []RGB{p.Dim, p.Accent, p.Error} {
					if c := Contrast(shownOf(t, fg.TextSGR(mode)), bg); c < minContrast {
						t.Fatalf("mode %d: %v at %.2f on row %d", mode, fg, c, row)
					}
				}
			}
		}
	}
}

func TestShownColoursStayCloseWhenTheyCan(t *testing.T) {
	// an exact xterm colour is itself; 24-bit is untouched; a background never uses a bright 16-colour code
	if got := (RGB{255, 0, 0}).TextSGR(Color256); got != "38;5;196" {
		t.Errorf("%q", got)
	}
	if got := (RGB{0, 0, 95}).BackSGR(Color256); got != "48;5;17" {
		t.Errorf("%q", got)
	}
	if got := (RGB{10, 20, 30}).TextSGR(TrueColor); got != "38;2;10;20;30" {
		t.Errorf("%q", got)
	}
	if got := (RGB{255, 255, 255}).BackSGR(Color16); got != "47" {
		t.Errorf("%q", got)
	}
	if got := (RGB{10, 20, 30}).TextSGR(Mono); got != "" {
		t.Errorf("%q", got)
	}
}
