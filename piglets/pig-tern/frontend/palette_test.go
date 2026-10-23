package frontend

import (
	"testing"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

// PiG's theme goes out as the surface's program palette, under the variant
// of its appearance, right after the surface opens and before its first
// frame; again, whole, when PiG reports a change, even in a frame with no
// ops; and not when the frame carries no theme.
func TestThemeIsTheProgramPalette(t *testing.T) {
	h := newHarness(t)
	h.reply(`,"credits":64,"cols":137,"cell":{"w":8,"h":16},"dark":false`)
	h.session.InputReady()
	h.messages()
	if err := h.session.Apply(fe.Frame{
		Theme: &fe.Theme{Name: "dark", Dark: true, Colors: map[string]string{"accent": "#8abeb7", "toolSuccessBg": "#283228"}},
		Ops:   []fe.Op{{Kind: fe.Insert, Region: fe.RegionMain, ID: "a", Node: fe.Lines{Lines: []string{"one"}}}},
	}); err != nil {
		t.Fatal(err)
	}
	expectWire(t, "first frame", h.messages(),
		`t {"dark":{"accent":"#8abeb7","toolSuccessBg":"#283228"},"name":{"dark":"dark"},"sf":"pig"}`,
		`f {"ops":[["add","main","pig",null,{"id":"main","k":"col"}],["add","dock","pig",null,{"id":"dock","k":"col"}],["add","layer","pig",null,{"id":"layer","k":"col"}]],"s":1,"sf":"pig"}`,
		`f {"ops":[["add","n.a","main",null,{"id":"n.a","k":"ansi","p":{"hidden":false,"text":"one"}}]],"s":2,"sf":"pig"}`,
	)
	h.ack()
	h.messages()

	if err := h.session.Apply(fe.Frame{Theme: &fe.Theme{Name: "light", Colors: map[string]string{"accent": "#5a8080"}}}); err != nil {
		t.Fatal(err)
	}
	expectWire(t, "theme switch", h.messages(), `t {"light":{"accent":"#5a8080"},"name":{"light":"light"},"sf":"pig"}`)

	h.apply(fe.Op{Kind: fe.Update, Region: fe.RegionMain, ID: "a", Node: fe.Lines{Lines: []string{"one!"}}})
	expectWire(t, "no theme", h.messages(), `f {"ops":[["text","n.a","append","!"]],"s":3,"sf":"pig"}`)

	if err := h.session.Apply(fe.Frame{Theme: &fe.Theme{Colors: map[string]string{"accent": "#000000"}}}); err != nil {
		t.Fatal(err)
	}
	expectWire(t, "unnamed theme", h.messages(), `t {"light":{"accent":"#000000"},"sf":"pig"}`)
}

// The palette stays with a surface adopted after a handoff; a new surface
// gets it again (TestAdoptAnsweredGoneOpensANewSurfaceWithTheWholeDocument).
func TestAdoptKeepsThePalette(t *testing.T) {
	h := ready(t)
	if err := h.session.Apply(fe.Frame{Theme: &fe.Theme{Name: "dark", Dark: true, Colors: map[string]string{"accent": "#8abeb7"}}}); err != nil {
		t.Fatal(err)
	}
	expectWire(t, "palette", h.messages(), `t {"dark":{"accent":"#8abeb7"},"name":{"dark":"dark"},"sf":"pig"}`)
	h.session.Suspend()
	h.session.Resume()
	for _, m := range h.messages() {
		if m.verb == "t" {
			t.Fatalf("palette sent again on adopt: %#v", m)
		}
	}
}
