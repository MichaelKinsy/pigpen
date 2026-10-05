package art

import (
	"math"
	"strings"
)

// DiscFrames is the number of pictures in one turn of the disc.
const DiscFrames = 12

// braille dot bits by (column, row) inside a cell.
var dotBit = [2][4]rune{{0x01, 0x02, 0x04, 0x40}, {0x08, 0x10, 0x20, 0x80}}

// Disc draws a record as braille (2x4 dots per cell) in w x h cells, turned to frame (any integer; it wraps). It has a rim,
// two glints on the grooves and a label with a radial mark, and the glints and the mark move from frame to frame, which is
// what makes it look like it is spinning. Empty cells are plain spaces. It returns nil when it would be too small to read as a
// disc (under 4 cells wide or 2 high). Terminal cells are about twice as tall as wide, so w == 2*h is round.
func Disc(frame, w, h int) []string {
	if w < 4 || h < 2 {
		return nil
	}
	frame = ((frame % DiscFrames) + DiscFrames) % DiscFrames
	rot := float64(frame) * 2 * math.Pi / DiscFrames
	W, H := 2*w, 4*h
	r := float64(min(W, H))/2 - 0.5
	cx, cy := float64(W)/2, float64(H)/2

	cells := make([]rune, w*h)
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			if lit(float64(x)+0.5-cx, float64(y)+0.5-cy, r, rot) {
				cells[(y/4)*w+x/2] |= dotBit[x%2][y%4]
			}
		}
	}
	out := make([]string, h)
	for y := 0; y < h; y++ {
		var b strings.Builder
		for x := 0; x < w; x++ {
			if c := cells[y*w+x]; c == 0 {
				b.WriteByte(' ')
			} else {
				b.WriteRune(0x2800 + c)
			}
		}
		out[y] = b.String()
	}
	return out
}

func lit(dx, dy, r, rot float64) bool {
	d := math.Hypot(dx, dy)
	theta := math.Atan2(dy, dx)
	switch {
	case math.Abs(d-r) < 0.9: // the rim
		return true
	case d < 0.6: // the spindle hole
		return false
	case math.Abs(d-0.36*r) < 0.7: // the edge of the label
		return true
	case d < 0.36*r: // the mark on the label
		return d > 0.12*r && angle(theta, rot) < 0.2
	case d > 0.5*r && d < 0.9*r && math.Mod(math.Floor(d/1.5), 2) == 0: // grooves, seen only where the light catches them
		return angle(theta, rot+math.Pi/4) < 0.6 || angle(theta, rot+math.Pi/4+math.Pi) < 0.6
	}
	return false
}

// angle is the distance between two directions, 0 to pi.
func angle(a, b float64) float64 {
	d := math.Mod(math.Abs(a-b), 2*math.Pi)
	if d > math.Pi {
		d = 2*math.Pi - d
	}
	return d
}
