// Package brand defines oh-my-pig's visual mark: a pixel-art mashup of omp's
// π and PiG's 16×14 pixel pig head (Candidate 3: the pig head sitting atop the
// π bar, centred over the two legs, overlapping the bar while the legs hang
// below with omp's checkerboard fade on the left leg).
//
// It is a public package of the oh-my-pig extension module so both the
// extension's plain-terminal renderer (`internal/ansi`) and the Piglet's
// frontend member (`piglets/oh-my-pig/frontend`, via `pig-tern/frontend` tests)
// share one exact grid definition.
package brand

import (
	"fmt"
	"strings"
)

// Canvas geometry of the π-plus-pig mashup in art pixels (origin top-left).
const (
	CanvasWidth  = 24
	CanvasHeight = 22

	// HeadWidth and HeadHeight are the dimensions of PiG's sprite pig head.
	HeadWidth  = 16
	HeadHeight = 14

	// HeadOriginX and HeadOriginY place the 16×14 head grid on the 24×22
	// canvas: the empty top row (y=0 of the head) falls off the canvas at
	// y=-1, the ears start at y=0, and the bottom outline row lands on y=11,
	// overlapping the middle of the π bar (y=9..12) so the bar shows left,
	// right, and one row below the head.
	HeadOriginX = 4
	HeadOriginY = -1

	// PiColorToken is the sentinel Color string for π pixels in a composed
	// Mark, resolved to a concrete colour (or CSS var(--t1)) by each renderer.
	PiColorToken = "pi"

	// PiDarkHex and PiLightHex are the concrete colours used for the π when a
	// renderer cannot follow Tern's live CSS variable `var(--t1)`.
	PiDarkHex  = "#EEDAAD"
	PiLightHex = "#1B1C20"
)

// Pixel is one opaque pixel at (X, Y) with a colour string (`#rrggbb` or
// PiColorToken).
type Pixel struct {
	X, Y  int
	Color string
}

// Head is a 16×14 pixel pig head (opaque pixels only).
type Head struct {
	Width, Height int
	Pixels        []Pixel
}

// defaultPigRows is PiG's default green mascot grid (`pig-default` in PiG's
// coding/piglogin/art.go and variants.go).
var defaultPigRows = [HeadHeight]string{
	"................",
	"...OOOO..OOOO...",
	"...OeeO..OeeO...",
	"..OeePPPPPPeeO..",
	".OPPPpPPPPpPPPO.",
	".OPPWWPPPPWWPPO.",
	".OPPWKPPPPKWPPO.",
	".OPbbPssssPbbPO.",
	".OPPPsKssKsPPPO.",
	".OPPPPssssPPPPO.",
	".OPPPPPPPPPPPPO.",
	"..OPPPPPPPPPPO..",
	"...OOOOOOOOOO...",
	"................",
}

// defaultPigPalette maps each non-transparent symbol of defaultPigRows to its
// `pig-default` RGB hex colour (PiG coding/piglogin/variants.go).
var defaultPigPalette = map[byte]string{
	'O': "#18151d",
	'K': "#18151d",
	'P': "#48a381",
	'p': "#64be9e",
	's': "#32775e",
	'b': "#82dbba",
	'W': "#fefefe",
	'e': "#48a381",
}

// DefaultHead returns PiG's default green 16×14 pig head in row-major order.
func DefaultHead() Head {
	pixels := make([]Pixel, 0, 140)
	for y, row := range defaultPigRows {
		for x := range len(row) {
			ch := row[x]
			if ch == '.' {
				continue
			}
			if hex, ok := defaultPigPalette[ch]; ok {
				pixels = append(pixels, Pixel{X: x, Y: y, Color: hex})
			}
		}
	}
	return Head{Width: HeadWidth, Height: HeadHeight, Pixels: pixels}
}

// Compose builds the 24×22 π-plus-pig mashup from head, painting the π gate
// first with piColor (or PiColorToken if empty) and the head on top at
// (HeadOriginX, HeadOriginY). If head has no pixels, DefaultHead() is used.
// Returned pixels are in deterministic row-major order (Y ascending, then X
// ascending) and cover only opaque cells; every other cell is transparent.
func Compose(head Head, piColor string) []Pixel {
	if len(head.Pixels) == 0 {
		head = DefaultHead()
	}
	if piColor == "" {
		piColor = PiColorToken
	}

	var grid [CanvasHeight][CanvasWidth]string

	// 1. π bar: x 0..23, y 9..12 (4 rows).
	for y := 9; y <= 12; y++ {
		for x := range CanvasWidth {
			grid[y][x] = piColor
		}
	}
	// 2. Left leg: x 6..9, y 13..17 solid, y 18..20 checkerboard ((x+y)%2 == 0).
	for y := 13; y <= 17; y++ {
		for x := 6; x <= 9; x++ {
			grid[y][x] = piColor
		}
	}
	for y := 18; y <= 20; y++ {
		for x := 6; x <= 9; x++ {
			if (x+y)%2 == 0 {
				grid[y][x] = piColor
			}
		}
	}
	// 3. Right leg: x 14..17, y 13..21 solid.
	for y := 13; y <= 21; y++ {
		for x := 14; x <= 17; x++ {
			grid[y][x] = piColor
		}
	}
	// 4. Pig head drawn last, on top, with grid origin at (HeadOriginX, HeadOriginY).
	for _, p := range head.Pixels {
		if p.Color == "" {
			continue
		}
		gx := HeadOriginX + p.X
		gy := HeadOriginY + p.Y
		if gx >= 0 && gx < CanvasWidth && gy >= 0 && gy < CanvasHeight {
			grid[gy][gx] = p.Color
		}
	}

	out := make([]Pixel, 0, 280)
	for y := range CanvasHeight {
		for x := range CanvasWidth {
			if c := grid[y][x]; c != "" {
				out = append(out, Pixel{X: x, Y: y, Color: c})
			}
		}
	}
	return out
}

// ParseHexRGB parses a `#rrggbb` colour string into 8-bit RGB channels.
func ParseHexRGB(hex string) (r, g, b uint8, ok bool) {
	s := strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(s) != 6 {
		return 0, 0, 0, false
	}
	var rv, gv, bv uint8
	n, err := fmt.Sscanf(s, "%02x%02x%02x", &rv, &gv, &bv)
	if err != nil || n != 3 {
		return 0, 0, 0, false
	}
	return rv, gv, bv, true
}
