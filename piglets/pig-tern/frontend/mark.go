package frontend

import (
	"slices"
	"strconv"
	"strings"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

// PiG's mark, the pig head of its active sprite, is the brand of its
// settings: Tern draws a prefs element's brand tile (.pf-mark) with its own
// π icon (.ico) and has no prop for another, so a program stylesheet hides
// the icon and draws the mark in the tile's ::before as pixel art, one CSS
// pixel per pixel, each an offset copy of a 1px box's shadow. A program
// stylesheet reaches no image (url() is dropped), and its selectors match
// only inside the surface, not the region element that carries the chat
// style, so the art cannot be sized per style: at one CSS pixel per pixel
// the 16-by-14 head fits every tile Tern 0.4.5 draws (26px on a page, 22px
// on a sheet and in Spine's round tile, 18px in Console, a 1px rim inside
// each), its pixels on whole device pixels and so crisp. The tile keeps
// Tern's gradient: the head's dark outline reads on it in light and dark,
// where on a transparent tile it vanishes into a dark background. A frame
// carries the mark when it changed; the sheet goes out before the frame
// that carries it, and before the surface's first frame. A zero mark sends
// nothing, so Tern's π stays, and removes a sheet already sent.

// markSheetName names the stylesheet that draws the mark. Tern takes 1 to
// 64 of A-Z, a-z, 0-9, _ and - and rejects any other name ("bad sheet
// name").
const markSheetName = "pig-mark"

// markSheet is the stylesheet that draws mark in the brand tile, or "" for
// the zero mark. The tile centers the 1px box, which sits a pixel up and
// left of the art, so that the art's top-left pixel lands on a whole CSS
// pixel when the tile's content box is a whole even number of pixels.
func markSheet(mark fe.Mark) string {
	if len(mark.Pixels) == 0 {
		return ""
	}
	var b strings.Builder
	b.Grow(160 + len(mark.Pixels)*20)
	b.WriteString(`.pf-mark .ico{display:none}.pf-mark::before{content:"";width:1px;height:1px;position:relative;left:-`)
	b.WriteString(strconv.Itoa(mark.Width / 2))
	b.WriteString(`.5px;top:-`)
	b.WriteString(strconv.Itoa(mark.Height / 2))
	b.WriteString(`.5px;box-shadow:`)
	for i, pixel := range mark.Pixels {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Itoa(pixel.X + 1))
		b.WriteString("px ")
		b.WriteString(strconv.Itoa(pixel.Y + 1))
		b.WriteString("px ")
		b.WriteString(pixel.Color)
	}
	b.WriteString("}")
	return b.String()
}

// sameMark reports whether two marks draw the same art.
func sameMark(a, b fe.Mark) bool {
	return a.Width == b.Width && a.Height == b.Height && slices.Equal(a.Pixels, b.Pixels)
}
