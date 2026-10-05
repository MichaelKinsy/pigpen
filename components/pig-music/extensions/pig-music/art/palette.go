package art

import (
	"hash/fnv"
	"image"
	"math"
	"sort"
	"strings"
)

// RGB is an 8-bit colour.
type RGB struct{ R, G, B uint8 }

// Palette is the colours of the player's overlay for one track: a background gradient from Top to Bottom, and the colours of
// text on it. Every palette that leaves this package (Derive, FromTrack, Mix) is already readable: Text has a contrast of at
// least 7:1 against every row of the gradient, and Dim, Accent and Error at least 4.5:1 (WCAG AAA and AA for normal text).
type Palette struct {
	Top, Bottom RGB
	Text        RGB // plain text
	Dim         RGB // hints and secondary text
	Accent      RGB // the title, the disc and the progress bar
	Error       RGB // a failure on the status line
}

const (
	maxBackgroundLum = 0.06  // the backgrounds stay dark, so a light text is always readable on them
	pulseCap         = 0.085 // a beat may brighten them a little further: the text keeps more than 7:1 even here
	pulseLift        = 0.025 // the most luminance a full beat adds
	textContrast     = 7.0
	minContrast      = 4.5
)

// Luminance is the WCAG relative luminance, 0 (black) to 1 (white).
func Luminance(c RGB) float64 {
	f := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*f(c.R) + 0.7152*f(c.G) + 0.0722*f(c.B)
}

// Contrast is the WCAG contrast ratio of two colours, 1 to 21.
func Contrast(a, b RGB) float64 {
	la, lb := Luminance(a), Luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// ── extracting colours ──────────────────────────────────────────────────────

// Extract returns up to k dominant colours of img, the most populous first. It looks at the centre square (as Render does, so
// the black bars of a 16:9 thumbnail around square art do not count), at 32x32 pixels, ignores near-black pixels unless there
// are no others, and quantises by median cut. The result is deterministic. It is pure Go.
func Extract(img image.Image, k int) []RGB {
	if img == nil || k < 1 {
		return nil
	}
	px := scale(img, 32, 32)
	if px == nil {
		return nil
	}
	var pts []pixel
	for _, p := range px {
		if lum(p) >= 12 {
			pts = append(pts, p)
		}
	}
	if len(pts) == 0 {
		pts = px
	}
	boxes := [][]pixel{pts}
	for len(boxes) < k {
		best, bestScore, bestCh := -1, 0, 0
		for i, b := range boxes {
			ch, rng := widest(b)
			if score := rng * len(b); rng > 0 && score > bestScore {
				best, bestScore, bestCh = i, score, ch
			}
		}
		if best < 0 {
			break // every box is one colour
		}
		b := boxes[best]
		sort.SliceStable(b, func(i, j int) bool { return channel(b[i], bestCh) < channel(b[j], bestCh) })
		mid := len(b) / 2
		boxes = append(append(boxes[:best:best], b[:mid], b[mid:]), boxes[best+1:]...)
	}
	type entry struct {
		c RGB
		n int
	}
	var out []entry
	for _, b := range boxes {
		var r, g, bl int
		for _, p := range b {
			r, g, bl = r+int(p.r), g+int(p.g), bl+int(p.b)
		}
		n := len(b)
		out = append(out, entry{RGB{uint8(r / n), uint8(g / n), uint8(bl / n)}, n})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].n != out[j].n {
			return out[i].n > out[j].n
		}
		a, b := out[i].c, out[j].c // equal populations: a fixed order, so the result is deterministic
		if a.R != b.R {
			return a.R < b.R
		}
		if a.G != b.G {
			return a.G < b.G
		}
		return a.B < b.B
	})
	cs := make([]RGB, len(out))
	for i, e := range out {
		cs[i] = e.c
	}
	return cs
}

func channel(p pixel, ch int) int {
	switch ch {
	case 0:
		return int(p.r)
	case 1:
		return int(p.g)
	}
	return int(p.b)
}

// widest is the colour channel with the largest spread in a box, and that spread.
func widest(b []pixel) (ch, rng int) {
	for c := 0; c < 3; c++ {
		lo, hi := 255, 0
		for _, p := range b {
			v := channel(p, c)
			lo, hi = min(lo, v), max(hi, v)
		}
		if hi-lo > rng {
			ch, rng = c, hi-lo
		}
	}
	return ch, rng
}

// ── deriving a palette ──────────────────────────────────────────────────────

// Derive makes a palette from the dominant colours of a cover (see Extract): a dark gradient from the first colour to the
// second, the most vivid colour as the accent. No colours gives a calm blue one.
func Derive(colors []RGB) Palette {
	if len(colors) == 0 {
		return FromTrack("", "")
	}
	top := colors[0]
	bottom := mixRGB(colors[0], RGB{}, 0.45)
	if len(colors) > 1 {
		bottom = colors[1]
	}
	accent, best := RGB{200, 210, 255}, 0.15
	for _, c := range colors {
		hi, lo := max(c.R, c.G, c.B), min(c.R, c.G, c.B)
		if hi == 0 {
			continue
		}
		if score := float64(hi-lo) / float64(hi) * float64(hi) / 255; score > best {
			accent, best = c, score
		}
	}
	return Palette{Top: top, Bottom: bottom, Text: RGB{248, 248, 248}, Accent: accent, Error: RGB{255, 110, 110}}.Ensure()
}

// FromTrack makes a palette from the track's metadata alone, for when there is no cover: the same artist and title always give
// the same colours.
func FromTrack(artist, title string) Palette {
	h := fnv.New32a()
	h.Write([]byte(strings.ToLower(artist) + "\x00" + strings.ToLower(title)))
	hue := float64(h.Sum32() % 360)
	return Palette{
		Top:    hsl(hue, 0.55, 0.20),
		Bottom: hsl(math.Mod(hue+40, 360), 0.50, 0.10),
		Text:   RGB{248, 248, 248},
		Accent: hsl(math.Mod(hue+180, 360), 0.80, 0.70),
		Error:  RGB{255, 110, 110},
	}.Ensure()
}

// Ensure makes the palette keep its promise: dark backgrounds, and text, dim text, accent and error colours readable on every
// row of the gradient. A palette that already keeps it comes back unchanged.
func (p Palette) Ensure() Palette { return p.ensureCap(maxBackgroundLum) }

// Pulsed is the palette on a beat of strength k (0 to 1): the background brightened toward the accent by a little, never past
// pulseCap, so the guarantee holds at every strength. A strength of 0 or less gives the palette itself.
func (p Palette) Pulsed(k float64) Palette {
	if k <= 0 {
		return p
	}
	k = math.Min(k, 1)
	lift := func(c RGB) RGB {
		target := math.Min(Luminance(c)+pulseLift*k, pulseCap)
		bright := mixRGB(c, p.Accent, 0.5*k)
		if Luminance(bright) <= target {
			return bright
		}
		return darkenTo(bright, target)
	}
	p.Top, p.Bottom = lift(p.Top), lift(p.Bottom)
	return p.ensureCap(pulseCap)
}

func (p Palette) ensureCap(cap float64) Palette {
	p.Top, p.Bottom = darkenTo(p.Top, cap), darkenTo(p.Bottom, cap)
	if p.Text == (RGB{}) || p.worst(p.Text) < textContrast {
		p.Text = RGB{248, 248, 248}
	}
	// the dim text is the plain text moved toward the background as far as the contrast allows; always computed, so it is stable
	mid := mixRGB(p.Top, p.Bottom, 0.5)
	for f := 0.45; ; f -= 0.05 {
		p.Dim = mixRGB(p.Text, mid, math.Max(f, 0))
		if f <= 0 || p.worst(p.Dim) >= minContrast+0.1 {
			break
		}
	}
	p.Accent = p.lighten(p.Accent)
	p.Error = p.lighten(p.Error)
	return p
}

// worst is the lowest contrast of c against the two ends of the gradient (every row between them is in between).
func (p Palette) worst(c RGB) float64 { return math.Min(Contrast(c, p.Top), Contrast(c, p.Bottom)) }

// lighten moves c toward white until it reads on the gradient.
func (p Palette) lighten(c RGB) RGB {
	orig := c
	for i := 1; i <= 50 && p.worst(c) < minContrast+0.05; i++ {
		c = mixRGB(orig, RGB{255, 255, 255}, float64(i)/50)
	}
	return c
}

// Mix is the palette a fraction t of the way from a to b (t is clamped to 0..1), made readable again: the middle of a
// cross-fade never dips below the promise.
func Mix(a, b Palette, t float64) Palette {
	switch {
	case t <= 0:
		return a
	case t >= 1:
		return b
	}
	return Palette{
		Top: mixRGB(a.Top, b.Top, t), Bottom: mixRGB(a.Bottom, b.Bottom, t), Text: mixRGB(a.Text, b.Text, t),
		Dim: mixRGB(a.Dim, b.Dim, t), Accent: mixRGB(a.Accent, b.Accent, t), Error: mixRGB(a.Error, b.Error, t),
	}.Ensure()
}

// At is the background colour of row of rows, from Top on the first to Bottom on the last.
func (p Palette) At(row, rows int) RGB {
	if rows <= 1 || row <= 0 {
		return p.Top
	}
	if row >= rows-1 {
		return p.Bottom
	}
	return mixRGB(p.Top, p.Bottom, float64(row)/float64(rows-1))
}

// SGR is the escape parameters that set the colour as foreground (fg) or background: 24-bit, xterm-256 or, in Mono, nothing.
func (c RGB) SGR(fg bool, mode Mode) string {
	base := 48
	if fg {
		base = 38
	}
	switch mode {
	case TrueColor:
		return sgrf("%d;2;%d;%d;%d", base, c.R, c.G, c.B)
	case Color256:
		return sgrf("%d;5;%d", base, xterm256(pixel{c.R, c.G, c.B}))
	case Color16:
		return code16(nearest16(pixel{c.R, c.G, c.B}, !fg), fg)
	}
	return ""
}

// TextSGR is the escape parameters of c as the foreground of text on a palette's background. In 24-bit it is SGR itself. With
// 256 or 16 colours it is the nearest colour the terminal draws that is no darker than c, and BackSGR's is no lighter, so the
// contrast a Palette promises holds in the colours really drawn (the nearest colour alone can halve it). The 256 colours are
// xterm's standard 16-255; the 16 basic colours are xterm's defaults, which a user's theme can change.
func (c RGB) TextSGR(mode Mode) string { return c.shownSGR(true, mode) }

// BackSGR is the escape parameters of c as a palette's background: see TextSGR.
func (c RGB) BackSGR(mode Mode) string { return c.shownSGR(false, mode) }

func (c RGB) shownSGR(fg bool, mode Mode) string {
	switch mode {
	case Color256:
		base := 48
		if fg {
			base = 38
		}
		return sgrf("%d;5;%d", base, shown(c, fg, 16, 256))
	case Color16:
		n := 8 // backgrounds: the normal colours only
		if fg {
			n = 16
		}
		return code16(shown(c, fg, 0, n), fg)
	}
	return c.SGR(fg, mode)
}

// shownRGB is the colour xterm draws for each index; shownLum its luminance.
var shownRGB, shownLum = func() (rgb [256]RGB, lum [256]float64) {
	lv := [6]uint8{0, 95, 135, 175, 215, 255}
	for i := range rgb {
		switch {
		case i < 16:
			rgb[i] = RGB{basic16[i].r, basic16[i].g, basic16[i].b}
		case i >= 232:
			v := uint8(8 + 10*(i-232))
			rgb[i] = RGB{v, v, v}
		default:
			j := i - 16
			rgb[i] = RGB{lv[j/36], lv[(j/6)%6], lv[j%6]}
		}
		lum[i] = Luminance(rgb[i])
	}
	return rgb, lum
}()

// shown is the index in [from, to) nearest to c whose luminance is at least c's (lighter) or at most c's. White and black are
// in both ranges, so there is always one.
func shown(c RGB, lighter bool, from, to int) int {
	l := Luminance(c)
	best, bestD := -1, math.MaxInt
	for i := from; i < to; i++ {
		if (lighter && shownLum[i] < l) || (!lighter && shownLum[i] > l) {
			continue
		}
		s := shownRGB[i]
		dr, dg, db := int(c.R)-int(s.R), int(c.G)-int(s.G), int(c.B)-int(s.B)
		if d := dr*dr + dg*dg + db*db; d < bestD {
			best, bestD = i, d
		}
	}
	if best < 0 { // not reached: white and black are always candidates
		best = from
	}
	return best
}

// ── colour arithmetic ───────────────────────────────────────────────────────

func mixRGB(a, b RGB, t float64) RGB {
	l := func(x, y uint8) uint8 { return uint8(math.Round(float64(x) + (float64(y)-float64(x))*t)) }
	return RGB{l(a.R, b.R), l(a.G, b.G), l(a.B, b.B)}
}

// darkenTo scales c toward black until its luminance is at most maxL; a colour that is dark enough is left alone.
func darkenTo(c RGB, maxL float64) RGB {
	if Luminance(c) <= maxL {
		return c
	}
	lo, hi := 0.0, 1.0
	for i := 0; i < 24; i++ {
		mid := (lo + hi) / 2
		if Luminance(scaleRGB(c, mid)) > maxL {
			hi = mid
		} else {
			lo = mid
		}
	}
	return scaleRGB(c, lo)
}

func scaleRGB(c RGB, k float64) RGB {
	s := func(v uint8) uint8 { return uint8(math.Floor(float64(v) * k)) }
	return RGB{s(c.R), s(c.G), s(c.B)}
}

// hsl converts hue (0..360), saturation and lightness (0..1) to RGB.
func hsl(h, s, l float64) RGB {
	c := (1 - math.Abs(2*l-1)) * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := l - c/2
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	to := func(v float64) uint8 { return uint8(math.Round((v + m) * 255)) }
	return RGB{to(r), to(g), to(b)}
}
