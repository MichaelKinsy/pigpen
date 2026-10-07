package frontend

import (
	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/stencil-hq/tern-sdk/go/tern"
)

// Tern draws a surface in the program's palette when the program sends one
// (the t verb), unless the user turned "Use program colors" off: spans and
// tones naming a token take its color, and the fills tint cards, the
// selected row and the status strip. PiG's themes use Pi's token names,
// which are the names Tern reads (omp's). A theme is designed for a dark or
// a light background, so it is the palette's variant for that appearance,
// and Tern uses a palette's only variant whatever its own appearance. PiG
// picks the variant of a light/dark theme pair, and the system theme's
// colors, from Tern's appearance: it reads Tern's answers to its terminal
// color query and Tern's light/dark reports, which HandleInput leaves to it,
// so a switch of Tern's appearance switches PiG's theme. PiG reports its
// theme in the first frame and again whenever its colors change; the
// palette goes out before the frame that carries it, and before the
// surface's first frame.

// palette is PiG's theme as a program palette.
func palette(theme fe.Theme) tern.Palette {
	var p tern.Palette
	var names tern.PaletteNames
	if theme.Dark {
		p.Dark, names.Dark = theme.Colors, theme.Name
	} else {
		p.Light, names.Light = theme.Colors, theme.Name
	}
	if theme.Name != "" {
		p.Name = &names
	}
	return p
}
