package frontend

import (
	"strings"
	"testing"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

// markSheetWire is the s message that draws mark in the brand tile.
func markSheetWire(css string) string {
	return `s {"css":"` + strings.ReplaceAll(css, `"`, `\"`) + `","name":"pig-mark","sf":"pig"}`
}

var (
	pinkMark = fe.Mark{Width: 4, Height: 2, Pixels: []fe.MarkPixel{{X: 0, Y: 0, Color: "#ffa8b7"}, {X: 3, Y: 1, Color: "#18141e"}}}
	blueMark = fe.Mark{Width: 16, Height: 14, Pixels: []fe.MarkPixel{{X: 7, Y: 0, Color: "#5b8def"}, {X: 15, Y: 13, Color: "#18141e"}}}
)

const (
	pinkSheet = `.pf-mark .ico{display:none}.pf-mark::before{content:"";width:1px;height:1px;position:relative;left:-2.5px;top:-1.5px;box-shadow:1px 1px #ffa8b7,4px 2px #18141e}`
	blueSheet = `.pf-mark .ico{display:none}.pf-mark::before{content:"";width:1px;height:1px;position:relative;left:-8.5px;top:-7.5px;box-shadow:8px 1px #5b8def,16px 14px #18141e}`
)

// PiG's mark goes out as the stylesheet that hides the brand tile's icon
// and draws the mark's pixels in its place, after the palette and before
// the surface's first frame; again when PiG reports another mark, even in
// a frame with no ops; and not when a frame carries no mark or the same
// one. The zero mark removes the sheet, so Tern's π shows again, once.
func TestMarkIsTheBrandTilesStylesheet(t *testing.T) {
	h := newHarness(t)
	h.reply(`,"credits":64,"cols":137,"cell":{"w":8,"h":16},"dark":false`)
	h.session.InputReady()
	h.messages()
	if err := h.session.Apply(fe.Frame{
		Theme: &fe.Theme{Name: "dark", Dark: true, Colors: map[string]string{"accent": "#8abeb7"}},
		Mark:  &pinkMark,
		Ops:   []fe.Op{{Kind: fe.Insert, Region: fe.RegionMain, ID: "a", Node: fe.Lines{Lines: []string{"one"}}}},
	}); err != nil {
		t.Fatal(err)
	}
	expectWire(t, "first frame", h.messages(),
		`t {"dark":{"accent":"#8abeb7"},"name":{"dark":"dark"},"sf":"pig"}`,
		markSheetWire(pinkSheet),
		`f {"ops":[["add","main","pig",null,{"id":"main","k":"col"}],["add","dock","pig",null,{"id":"dock","k":"col"}],["add","layer","pig",null,{"id":"layer","k":"col"}]],"s":1,"sf":"pig"}`,
		`f {"ops":[["add","n.a","main",null,{"id":"n.a","k":"ansi","p":{"hidden":false,"text":"one"}}]],"s":2,"sf":"pig"}`,
	)
	h.ack()
	h.messages()

	step := func(what string, mark *fe.Mark, ops []fe.Op, want ...string) {
		t.Helper()
		if err := h.session.Apply(fe.Frame{Mark: mark, Ops: ops}); err != nil {
			t.Fatal(err)
		}
		expectWire(t, what, h.messages(), want...)
		h.ack()
	}
	step("the same mark", &fe.Mark{Width: 4, Height: 2, Pixels: []fe.MarkPixel{{X: 0, Y: 0, Color: "#ffa8b7"}, {X: 3, Y: 1, Color: "#18141e"}}}, nil)
	step("no mark", nil, []fe.Op{{Kind: fe.Update, Region: fe.RegionMain, ID: "a", Node: fe.Lines{Lines: []string{"one!"}}}},
		`f {"ops":[["text","n.a","append","!"]],"s":3,"sf":"pig"}`)
	step("another mark", &blueMark, nil, markSheetWire(blueSheet))
	step("a recolored pixel", &fe.Mark{Width: 16, Height: 14, Pixels: []fe.MarkPixel{{X: 7, Y: 0, Color: "#5b8def"}, {X: 15, Y: 13, Color: "#000000"}}}, nil,
		markSheetWire(strings.Replace(blueSheet, "#18141e", "#000000", 1)))
	step("the zero mark", &fe.Mark{}, nil, `s {"css":null,"name":"pig-mark","sf":"pig"}`)
	step("the zero mark again", &fe.Mark{}, nil)
	step("a mark again", &pinkMark, nil, markSheetWire(pinkSheet))
	step("a wider grid", &fe.Mark{Width: 6, Height: 2, Pixels: pinkMark.Pixels}, nil, markSheetWire(strings.Replace(pinkSheet, "left:-2.5px", "left:-3.5px", 1)))
}

// A first frame with the zero mark, PiG reporting none, sends no sheet:
// Tern's π stays.
func TestZeroMarkSendsNoSheet(t *testing.T) {
	h := newHarness(t)
	h.reply(`,"credits":64,"cols":137,"cell":{"w":8,"h":16}`)
	h.session.InputReady()
	h.messages()
	if err := h.session.Apply(fe.Frame{Mark: &fe.Mark{}}); err != nil {
		t.Fatal(err)
	}
	for _, m := range h.messages() {
		if m.verb != "f" {
			t.Fatalf("zero mark sent %s %v", m.verb, m.body)
		}
	}
}

// The sheet stays with a surface adopted after a handoff; a new surface
// gets it again, after its open and before its first frame.
func TestMarkSheetStaysOnAdoptAndGoesToANewSurface(t *testing.T) {
	h := ready(t)
	if err := h.session.Apply(fe.Frame{Mark: &pinkMark}); err != nil {
		t.Fatal(err)
	}
	expectWire(t, "mark", h.messages(), markSheetWire(pinkSheet))
	h.ack()
	h.session.Suspend()
	h.session.Resume()
	for _, m := range h.messages() {
		if m.verb == "s" {
			t.Fatalf("sheet sent again on adopt: %#v", m)
		}
	}
	h.session.mu.Lock()
	h.session.credits = 2
	h.session.mu.Unlock()
	h.event(`{"ev":"gone","ids":["pig"]}`)
	h.eventually("the new surface", func() bool { return strings.Count(h.out.peek(), "\x1b_tsp;f;") == 1 })
	expectWire(t, "reopen", h.messages(),
		`o {"id":"pig","mode":"inline","role":"pig.session","title":"pig"}`,
		markSheetWire(pinkSheet),
		`f {"ops":[["add","main","pig",null,{"id":"main","k":"col"}],["add","dock","pig",null,{"id":"dock","k":"col"}],["add","layer","pig",null,{"id":"layer","k":"col"}]],"s":1,"sf":"pig"}`,
	)
}

// The sheet of a 16-by-14 head centers it in the tile: the 1px box sits
// 8.5 and 7.5 pixels up and left of the tile's center, a pixel up and left
// of the art, and an odd size rounds the art's corner to a whole pixel.
func TestMarkSheetCentersTheArt(t *testing.T) {
	for _, tc := range []struct {
		width, height int
		want          string
	}{
		{16, 14, "left:-8.5px;top:-7.5px;"},
		{15, 13, "left:-7.5px;top:-6.5px;"},
	} {
		css := markSheet(fe.Mark{Width: tc.width, Height: tc.height, Pixels: []fe.MarkPixel{{Color: "#ffffff"}}})
		if !strings.Contains(css, tc.want) {
			t.Fatalf("%dx%d sheet %q, want %q", tc.width, tc.height, css, tc.want)
		}
	}
}
