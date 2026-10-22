package stretchtext

import "math"

// The colours open stretches take in turn, read from Pi's theme, so they follow it, light or dark,
// and change with it. The order is cc-stretch's: yellow, violet, green, then blue. Each open stretch
// keeps its colour; all but the newest take a faded form of it, mixed 35% toward the theme's
// background side, quieter but still that colour. Before the host hands over its theme, stretches are
// drawn without colour. A port of src/palette.ts of pi-stretchtext.

// Paint wraps text in a colour.
type Paint func(string) string

// Theme is what the palette reads of the host's theme: Pi's Theme.fg, appearance, colors and
// getColorMode.
type Theme interface {
	// Fg paints text with a theme token.
	Fg(token, text string) string
	// Dark reports whether the theme is made for a dark background.
	Dark() bool
	// ColorOf is the concrete colour of a token.
	ColorOf(token string) (Color, bool)
	// ColorMode is "truecolor" or "256color".
	ColorMode() string
}

var colours = [...]string{"warning", "accent", "success", "mdLink"}

const fade = 0.35

func colourAt(count int) string { return colours[count%len(colours)] }

func plain(text string) string { return text }

// hueAfter is count, or the next count when count would give the colour of the stretch holding it,
// opened at parent.
func hueAfter(count int, parent *int) int {
	if parent != nil && *parent%len(colours) == count%len(colours) {
		return count + 1
	}
	return count
}

// wordsPaint is the paint for the words a stretch opened at count adds: full for the newest stretch,
// else faded. A nil theme draws without colour.
func wordsPaint(theme Theme, count int, isNewest bool) Paint {
	if theme == nil {
		return plain
	}
	if isNewest {
		return full(theme, colourAt(count))
	}
	return faded(theme, colourAt(count))
}

// closerPaint is the paint for the « closing a stretch opened at count, which always takes its full
// colour.
func closerPaint(theme Theme, count int) Paint {
	if theme == nil {
		return plain
	}
	return full(theme, colourAt(count))
}

func full(theme Theme, colour string) Paint {
	return func(text string) string { return theme.Fg(colour, text) }
}

// faded is colour mixed toward the background the theme is made for. Only the colour changes, not
// bold or dim, so Markdown's own marks stand.
func faded(theme Theme, colour string) Paint {
	from, ok := theme.ColorOf(colour)
	if !ok {
		return plain
	}
	target := rgb{255, 255, 255}
	if theme.Dark() {
		target = rgb{0, 0, 0}
	}
	a := from.rgb()
	mixed := rgb{a.r + (target.r-a.r)*fade, a.g + (target.g-a.g)*fade, a.b + (target.b-a.b)*fade}
	ansi := foregroundAnsi(mixed, theme.ColorMode())
	return func(text string) string { return ansi + text + "\x1b[39m" }
}

// Color is a concrete theme colour: indexed, RGB or OKLCH, as pi-tui's Color.
type Color interface{ rgb() rgb }

// Indexed is an ANSI colour index, 0 to 255.
type Indexed int

// RGB is a colour with channels 0 to 255.
type RGB struct{ R, G, B float64 }

// OKLCH is a colour by lightness, chroma and hue in degrees.
type OKLCH struct{ L, C, H float64 }

type rgb struct{ r, g, b float64 }

func (c Indexed) rgb() rgb { return indexedToRgb(int(c)) }
func (c RGB) rgb() rgb     { return rgb{c.R, c.G, c.B} }
func (c OKLCH) rgb() rgb   { return oklchToRgb(c) }

func jsRound(v float64) float64 { return math.Floor(v + 0.5) }
