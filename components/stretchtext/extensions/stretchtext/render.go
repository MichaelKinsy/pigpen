package stretchtext

import "strings"

// How a reply is drawn for a given state: each closed stretch as its underlined short form, each open
// one as its long form in its colour, followed by a «. Both are links a click finds again. A port of
// src/render.ts of pi-stretchtext.

func underline(text string) string { return "\x1b[4m" + text + "\x1b[24m" }
func reverse(text string) string   { return "\x1b[7m" + text + "\x1b[27m" }

func easeInOut(t float64) float64 {
	if t < 0.5 {
		return 2 * t * t
	}
	u := -2*t + 2
	return 1 - u*u/2
}

// Canvas is what a reply is drawn with besides its state: the links a click finds, and the host's
// theme for colour (nil before the host hands it over).
type Canvas struct {
	Links *LinkTable
	Theme Theme
}

// Draw draws segments of reply for the state.
func Draw(state *State, canvas Canvas, reply string, segments []Segment) string {
	var b strings.Builder
	drawInto(&b, state, canvas, reply, segments)
	return b.String()
}

func drawInto(b *strings.Builder, state *State, canvas Canvas, reply string, segments []Segment) {
	for _, segment := range segments {
		if segment.Stretch == nil {
			b.WriteString(segment.Text)
			continue
		}
		switch segment.Stretch.DrawnAs() {
		case DrawnText:
			b.WriteString(segment.Stretch.Short)
		case DrawnOpen:
			drawInto(b, state, canvas, reply, segment.Stretch.Long)
		case DrawnLink:
			b.WriteString(drawLink(state, canvas, Target{Reply: reply, Stretch: segment.Stretch}))
		}
	}
}

func drawLink(state *State, canvas Canvas, target Target) string {
	name := nameOf(target)
	hue, isOpen := state.open[name]
	if !isOpen {
		return state.letter(name, ModeOpen) + canvas.Links.Link(target, underline(target.Stretch.Short))
	}
	progress := 1.0
	growing := state.growth != nil && state.growth.Name == name
	if growing {
		progress = easeInOut(state.growth.Progress)
	}
	long := Grow(
		target.Stretch.Short,
		target.Stretch.Long,
		progress,
		func(nested *Stretch) string { return Draw(state, canvas, target.Reply, []Segment{{Stretch: nested}}) },
		wordsPaint(canvas.Theme, hue, state.newest == name && state.hasNewest),
	)
	// While it grows or shrinks, a stretch has no « to click.
	if growing {
		return long
	}
	return long + " " + state.letter(name, ModeClose) + canvas.Links.Link(target, closerPaint(canvas.Theme, hue)("«"))
}
