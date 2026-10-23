package frontend

import (
	"math"
	"strconv"
	"strings"
)

// Tern corrects the program palette (the t verb) for contrast, but a
// view's theme overrides reach it as CSS in the member's view sheet, which
// Tern applies as sent: an extension designed for a dark terminal, such as
// pig-music with its near-white text and pale yellow accent, would draw
// white on Tern's light surface. So the member corrects the overrides the
// way Tern corrects a palette: each text color reads at
// least 4.5:1 against every background it can land on, 3:1 for the quiet
// tokens (muted, dim, borders), Tern's thresholds for the palette
// (protocol/theme.md, "Program palette"), which are WCAG 2.2's minimums for
// text (SC 1.4.3) and for non-text contrast (SC 1.4.11). A color that reads
// stays as sent. One that does not moves toward black or white, whichever
// takes the shorter step, which keeps its hue, so each track keeps its
// accent; when neither reaches the ratio, it is the one that reads best.
//
// A view's text lands on Tern's surfaces, in Tern's colors for the
// appearance shown, which the member does not see: the kit's page, surface
// (--tv-bg), cards (--card) and overlay card (--pop-bg), and the overlay
// card of Tern's default theme, lighter in dark; marked text and a
// selected item land on the selection's tint, and a box's or text's
// content on its fill's card tint. A tint is a fill at 60% over the
// surface, as Tern tints the palette's: the view's override of the fill,
// else PiG's palette's, which Tern uses in either appearance (a mark
// without a selectedBg tints with --accent at 16%). The view's root rule
// reads on the surface, and a rule for each tint its text can land on
// reads on that tint. A view that overrides a fill also corrects the text
// it inherits onto it, from PiG's palette or Tern's defaults, so a dark
// selection under Tern's dark text reads.
//
// A value holds both appearances as light-dark(), which picks one per
// element from Tern's appearance, so a light/dark switch recolors the view
// at once; PiG then resends its theme and the member rebuilds its views'
// rules against the new palette.

const (
	textContrast  = 4.5
	quietContrast = 3.0
	// tintAlpha is the opacity of a fill tint; markAlpha that of the
	// mark's accent tint without a selectedBg.
	tintAlpha = 0.6
	markAlpha = 0.16
)

// appearance is Tern's light or dark look: the surfaces a view's text
// lands on (bases), the accent its mark tints with, and the colors of the
// tokens neither the view nor PiG's palette sets. The colors are Tern's kit
// defaults, as its CSS variables reference documents them, and the
// overlay card of Tern's default dark theme (#33363e, as Tern draws it).
type appearance struct {
	bases    []rgb
	accent   rgb
	defaults map[string]rgb
}

// appearances are Tern's light and dark looks, in that order.
var appearances = [2]appearance{
	{
		bases:  []rgb{{0xef, 0xef, 0xec}, {0xfc, 0xfc, 0xfb}, {0xff, 0xff, 0xff}},
		accent: rgb{0x3d, 0x6f, 0xff},
		defaults: map[string]rgb{
			"text": {0x1f, 0x21, 0x26}, "accent": {0x24, 0x48, 0xc4}, "muted": {0x6a, 0x6c, 0x74}, "dim": {0xb3, 0xb5, 0xbb},
			"success": {0x2f, 0xae, 0x74}, "warning": {0xd9, 0x8a, 0x0b}, "error": {0xe5, 0x48, 0x4d},
		},
	},
	{
		// The dark look's overlay card is the lightest base.
		bases:  []rgb{{0x0b, 0x0b, 0x0c}, {0x16, 0x16, 0x19}, {0x23, 0x23, 0x27}, {0x33, 0x36, 0x3e}},
		accent: rgb{0x5a, 0x8c, 0xff},
		defaults: map[string]rgb{
			"text": {0xe4, 0xe4, 0xe7}, "accent": {0x9d, 0xbc, 0xff}, "muted": {0x8a, 0x8a, 0x92}, "dim": {0x55, 0x55, 0x5c},
			"success": {0x3e, 0xcf, 0x8e}, "warning": {0xf5, 0xa5, 0x24}, "error": {0xff, 0x61, 0x66},
		},
	},
}

// minContrast is the ratio a token's color must reach.
func minContrast(token string) float64 {
	switch token {
	case "muted", "dim", "border", "borderAccent", "borderMuted":
		return quietContrast
	}
	return textContrast
}

// rgb is an sRGB color.
type rgb [3]uint8

func parseRGB(hex string) (rgb, bool) {
	if !hexColor.MatchString(hex) {
		return rgb{}, false
	}
	n, _ := strconv.ParseUint(hex[1:], 16, 32)
	return rgb{uint8(n >> 16), uint8(n >> 8), uint8(n)}, true
}

func (c rgb) String() string {
	return "#" + strconv.FormatUint(uint64(c[0])<<16|uint64(c[1])<<8|uint64(c[2])|1<<24, 16)[1:]
}

// linear is each sRGB channel value in linear light.
var linear = func() (t [256]float64) {
	for i := range t {
		v := float64(i) / 255
		if v <= 0.04045 {
			t[i] = v / 12.92
		} else {
			t[i] = math.Pow((v+0.055)/1.055, 2.4)
		}
	}
	return t
}()

// luminance is WCAG's relative luminance.
func (c rgb) luminance() float64 {
	return 0.2126*linear[c[0]] + 0.7152*linear[c[1]] + 0.0722*linear[c[2]]
}

// contrast is WCAG's contrast ratio of two luminances.
func contrast(a, b float64) float64 {
	return (max(a, b) + 0.05) / (min(a, b) + 0.05)
}

// over is fill at alpha over base.
func over(fill, base rgb, alpha float64) rgb {
	var c rgb
	for i := range c {
		c[i] = uint8(math.Round(float64(fill[i])*alpha + float64(base[i])*(1-alpha)))
	}
	return c
}

// worst is c's lowest contrast against the backgrounds' luminances.
func worst(c rgb, bgs []float64) float64 {
	l := c.luminance()
	ratio := math.Inf(1)
	for _, bg := range bgs {
		ratio = min(ratio, contrast(l, bg))
	}
	return ratio
}

// readable is c, or c moved toward black or white by the fewest of 255
// steps, reading at ratio against every background (bgs, luminances), and
// toward black on a tie. Without such a step it is black or white,
// whichever reads better.
func readable(c rgb, bgs []float64, ratio float64) rgb {
	if worst(c, bgs) >= ratio {
		return c
	}
	ends := [2]rgb{{}, {0xff, 0xff, 0xff}}
	best, steps := rgb{}, 256
	for _, end := range ends {
		for i := 1; i < steps; i++ {
			if m := over(end, c, float64(i)/255); worst(m, bgs) >= ratio {
				best, steps = m, i
				break
			}
		}
	}
	if steps < 256 {
		return best
	}
	if worst(ends[1], bgs) > worst(ends[0], bgs) {
		return ends[1]
	}
	return ends[0]
}

// cardFill is the palette token whose tint the card of a box or text that
// fills token shows: a tone card's own, the custom message's, and other
// cards' (a selected one's too) cardBg.
func cardFill(token string) string {
	switch token {
	case "toolSuccessBg", "toolErrorBg", "toolPendingBg", "userMessageBg", "customMessageBg":
		return token
	}
	return "cardBg"
}

// viewColors resolves a view's colors in one appearance on one background:
// overrides are the view's (its ancestors' and its own), palette PiG's.
type viewColors struct {
	a                  appearance
	overrides, palette map[string]string
	bgs                []float64
}

// newViewColors resolves colors on the surface when fill is "", else on
// fill's tint; ok is false for a card fill without a color, whose card is
// the surface's.
func newViewColors(a appearance, overrides, palette map[string]string, fill string) (v *viewColors, ok bool) {
	v = &viewColors{a: a, overrides: overrides, palette: palette}
	tint := func(c rgb, alpha float64) {
		v.bgs = v.bgs[:0]
		for _, base := range a.bases {
			v.bgs = append(v.bgs, over(c, base, alpha).luminance())
		}
	}
	if fill == "" {
		tint(rgb{}, 0)
		return v, true
	}
	if c, ok := v.fill(fill); ok {
		tint(c, tintAlpha)
		return v, true
	}
	if fill != "selectedBg" {
		return nil, false
	}
	// The mark tints with --accent: Tern's, or the view's when it sets one,
	// which is itself read against the tint, so the accent and its tint go
	// to their fixed point.
	if _, ok := parseRGB(overrides["accent"]); !ok {
		tint(a.accent, markAlpha)
		return v, true
	}
	tint(rgb{}, 0)
	accent, _ := v.color("accent")
	for range 4 {
		tint(accent, markAlpha)
		next, _ := v.color("accent")
		if next == accent {
			break
		}
		accent = next
	}
	return v, true
}

// fill is the color of a fill token's tint: the view's override, else the
// palette's.
func (v *viewColors) fill(token string) (rgb, bool) {
	if _, overridable := themeVars[token]; overridable {
		if c, ok := parseRGB(v.overrides[token]); ok {
			return c, true
		}
	}
	return parseRGB(v.palette[token])
}

// themeScope is a rule's colors on one background, in light and dark.
type themeScope struct{ light, dark *viewColors }

// newThemeScope resolves colors on the surface (fill ""), the selection
// ("selectedBg") or a card fill's tint, as newViewColors.
func newThemeScope(overrides, palette map[string]string, fill string) (themeScope, bool) {
	light, ok := newViewColors(appearances[0], overrides, palette, fill)
	if !ok {
		return themeScope{}, false
	}
	dark, _ := newViewColors(appearances[1], overrides, palette, fill)
	return themeScope{light, dark}, true
}

// value is the CSS value of a foreground token here, or "" for none:
// without a color, or, for a token the rule's root does not set (mine),
// Tern's default where it reads.
func (s themeScope) value(token string, mine bool) string {
	l, ok := s.light.color(token)
	if !ok {
		return ""
	}
	d, _ := s.dark.color(token)
	if !mine {
		srcL, deflt, _ := s.light.source(token)
		srcD, _, _ := s.dark.source(token)
		if deflt && l == srcL && d == srcD {
			return ""
		}
	}
	return lightDark(l, d)
}

// source is a foreground token's color before correction: the view's, PiG
// palette's or Tern's default, and whether Tern's default it is.
func (v *viewColors) source(token string) (c rgb, deflt, ok bool) {
	if c, ok := parseRGB(v.overrides[token]); ok {
		return c, false, true
	}
	if c, ok := parseRGB(v.palette[token]); ok {
		return c, false, true
	}
	c, ok = v.a.defaults[token]
	return c, true, ok
}

// color is a foreground token's corrected color.
func (v *viewColors) color(token string) (rgb, bool) {
	c, _, ok := v.source(token)
	if !ok {
		return rgb{}, false
	}
	return readable(c, v.bgs, minContrast(token)), true
}

// lightDark is the CSS value of a color per appearance.
func lightDark(light, dark rgb) string {
	if light == dark {
		return light.String()
	}
	return "light-dark(" + light.String() + "," + dark.String() + ")"
}

// isFill reports a background token.
func isFill(token string) bool {
	return strings.HasSuffix(token, "Bg")
}
