package art

import (
	"fmt"
	"image"
	"math"
	"strconv"
	"strings"
)

// Mode is how much colour the terminal is believed to have.
type Mode int

const (
	// Mono uses no colour at all: a ramp of block shades.
	Mono Mode = iota
	// Color16 uses the 16 basic colours (the codes 30-37, 90-97 and 40-47).
	Color16
	// Color256 uses the xterm 256-colour palette.
	Color256
	// TrueColor uses 24-bit colour.
	TrueColor
)

// NoColor reports that the user asked for no colour at all: NO_COLOR (any value, see no-color.org) or TERM=dumb. Unlike a
// terminal whose colours are unknown (Mono from ModeFromEnv), this also rules out the basic colours of the accents.
func NoColor(getenv func(string) string) bool {
	return getenv("NO_COLOR") != "" || getenv("TERM") == "dumb"
}

// ModeFromEnv decides the colour mode from the environment as given by getenv (os.Getenv in the extension, a map in tests):
// NO_COLOR (any value) and TERM=dumb mean none, COLORTERM=truecolor or 24bit means 24-bit, a TERM ending in 256color means
// 256 colours, a TERM that names colour (xterm-color, linux, screen, rxvt...) means the 16 basic colours, and anything else is
// treated as having no colour, which is the safe side.
func ModeFromEnv(getenv func(string) string) Mode {
	if NoColor(getenv) {
		return Mono
	}
	term := getenv("TERM")
	if term == "dumb" {
		return Mono
	}
	switch strings.ToLower(getenv("COLORTERM")) {
	case "truecolor", "24bit":
		return TrueColor
	}
	if strings.Contains(term, "256color") {
		return Color256
	}
	switch {
	case strings.Contains(term, "color"), term == "linux", term == "screen", term == "tmux", term == "ansi", term == "cygwin", strings.HasPrefix(term, "rxvt"):
		return Color16
	}
	return Mono
}

type pixel struct{ r, g, b uint8 }

// Render draws img, cropped to its centre square, as rows lines of exactly cols cells. A cell shows two pixels, so the
// picture is cols x 2*rows pixels; callers who want it undistorted pass cols == 2*rows. It returns nil for a size under one
// cell or for no image.
func Render(img image.Image, cols, rows int, mode Mode) []string {
	if img == nil || cols < 1 || rows < 1 {
		return nil
	}
	px := scale(img, cols, rows*2)
	if px == nil {
		return nil
	}
	lines := make([]string, rows)
	for y := 0; y < rows; y++ {
		var b strings.Builder
		var last string
		for x := 0; x < cols; x++ {
			up, down := px[(2*y)*cols+x], px[(2*y+1)*cols+x]
			if mode == Mono {
				b.WriteRune(shade(up, down))
				continue
			}
			sgr := colours(up, down, mode)
			if sgr != last {
				b.WriteString("\x1b[" + sgr + "m")
				last = sgr
			}
			b.WriteRune('▀')
		}
		if mode != Mono {
			b.WriteString("\x1b[0m")
		}
		lines[y] = b.String()
	}
	return lines
}

// scale crops img to its centre square and box-averages it down (or nearest-samples it up) to w x h pixels.
func scale(img image.Image, w, h int) []pixel {
	b := img.Bounds()
	side := min(b.Dx(), b.Dy())
	if side < 1 {
		return nil
	}
	x0, y0 := b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2
	out := make([]pixel, w*h)
	for py := 0; py < h; py++ {
		ya, yb := y0+py*side/h, y0+(py+1)*side/h
		if yb <= ya {
			yb = ya + 1
		}
		for px := 0; px < w; px++ {
			xa, xb := x0+px*side/w, x0+(px+1)*side/w
			if xb <= xa {
				xb = xa + 1
			}
			var r, g, bl, n uint64
			for y := ya; y < yb; y++ {
				for x := xa; x < xb; x++ {
					cr, cg, cb, _ := img.At(x, y).RGBA() // premultiplied: alpha over black
					r, g, bl, n = r+uint64(cr>>8), g+uint64(cg>>8), bl+uint64(cb>>8), n+1
				}
			}
			out[py*w+px] = pixel{uint8(r / n), uint8(g / n), uint8(bl / n)}
		}
	}
	return out
}

func colours(up, down pixel, mode Mode) string {
	if mode == TrueColor {
		return fmt.Sprintf("38;2;%d;%d;%d;48;2;%d;%d;%d", up.r, up.g, up.b, down.r, down.g, down.b)
	}
	if mode == Color16 {
		return RGB{up.r, up.g, up.b}.SGR(true, Color16) + ";" + RGB{down.r, down.g, down.b}.SGR(false, Color16)
	}
	return fmt.Sprintf("38;5;%d;48;5;%d", xterm256(up), xterm256(down))
}

// xterm256 is the nearest palette index: the 6x6x6 cube, or the grey ramp for colours without a hue.
func xterm256(p pixel) int {
	hi, lo := max(p.r, p.g, p.b), min(p.r, p.g, p.b)
	if hi-lo < 10 {
		avg := (int(p.r) + int(p.g) + int(p.b)) / 3
		switch {
		case avg < 5:
			return 16
		case avg > 246:
			return 231
		}
		return 232 + (avg-8)*23/239
	}
	c := func(v uint8) int { return (int(v)*5 + 127) / 255 }
	return 16 + 36*c(p.r) + 6*c(p.g) + c(p.b)
}

var ramp = []rune(" ░▒▓█")

// shade is one block for two pixels, by brightness.
func shade(up, down pixel) rune {
	l := (lum(up) + lum(down)) / 2
	i := l * len(ramp) / 256
	return ramp[min(i, len(ramp)-1)]
}

func lum(p pixel) int { return (299*int(p.r) + 587*int(p.g) + 114*int(p.b)) / 1000 }

func sgrf(format string, a ...any) string { return fmt.Sprintf(format, a...) }

// The 16 basic colours as xterm draws them, for picking the nearest one.
var basic16 = [16]pixel{
	{0, 0, 0}, {205, 0, 0}, {0, 205, 0}, {205, 205, 0}, {0, 0, 238}, {205, 0, 205}, {0, 205, 205}, {229, 229, 229},
	{127, 127, 127}, {255, 0, 0}, {0, 255, 0}, {255, 255, 0}, {92, 92, 255}, {255, 0, 255}, {0, 255, 255}, {255, 255, 255},
}

// nearest16 is the index of the nearest basic colour; a background may only be one of the first eight (bright backgrounds are
// not drawn everywhere).
func nearest16(p pixel, background bool) int {
	best, bestD := 0, math.MaxInt
	n := 16
	if background {
		n = 8
	}
	for i := 0; i < n; i++ {
		dr, dg, db := int(p.r)-int(basic16[i].r), int(p.g)-int(basic16[i].g), int(p.b)-int(basic16[i].b)
		if d := dr*dr + dg*dg + db*db; d < bestD {
			best, bestD = i, d
		}
	}
	return best
}

func code16(idx int, fg bool) string {
	switch {
	case !fg:
		return strconv.Itoa(40 + idx)
	case idx < 8:
		return strconv.Itoa(30 + idx)
	}
	return strconv.Itoa(90 + idx - 8)
}
