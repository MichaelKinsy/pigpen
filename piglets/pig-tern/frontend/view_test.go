package frontend

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"reflect"
	"strconv"
	"strings"
	"testing"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/stencil-hq/tern-sdk/go/tern"
)

// coverPNG is a 3×2 PNG, an extension's cover image.
func coverPNG(t *testing.T) *fe.ViewImage {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	img.Set(0, 0, color.RGBA{R: 224, G: 160, B: 64, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return &fe.ViewImage{Ref: tern.BlobID(buf.Bytes()), MimeType: "image/png", Data: buf.Bytes(), Filename: "cover.png"}
}

// kitTheme is a theme whose accent and muted colors an extension's text
// carries.
var kitTheme = fe.Theme{Name: "dark", Dark: true, Colors: map[string]string{"accent": "#e0a040", "muted": "#808080", "border": "#e0a040"}}

// kitView is a view of every component kind, 20 cells wide, and the 26
// lines it describes. Rows a node draws natively hold "x"; the rows of the
// nodes the member draws from their lines hold what they show.
func kitView(t *testing.T) (*fe.View, []string) {
	t.Helper()
	cover := coverPNG(t)
	text := func(kind fe.ViewKind, text string, width int, stack *fe.ViewStack) fe.ViewNode {
		return fe.ViewNode{Kind: kind, Text: text, Rows: 1, Width: width, Stack: stack}
	}
	root := fe.ViewNode{Kind: fe.ViewKindContainer, Rows: 26, Width: 20, Children: []fe.ViewNode{
		{Kind: fe.ViewKindDynamicBorder, Color: "accent", Rows: 1, Width: 20},
		{Kind: fe.ViewKindText, Text: "Pick \x1b[1ma\x1b[22m track", PaddingX: 1, Bg: "customMessageBg", Rows: 1, Width: 20},
		{Kind: fe.ViewKindMarkdown, Text: "- **one**", Rows: 1, Width: 20},
		{Kind: fe.ViewKindHStack, Gap: 1, Align: "stretch", Rows: 1, Width: 20, Children: []fe.ViewNode{
			text(fe.ViewKindTruncatedText, "left\nhidden", 8, &fe.ViewStack{Basis: -1, Grow: 1, Shrink: 1, MaxSize: -1}),
			text(fe.ViewKindTruncatedText, "right", 5, &fe.ViewStack{Basis: 5, Shrink: 1, MinSize: 2, MaxSize: 8}),
			{Kind: "gauge", Rows: 1, Width: 5, Stack: &fe.ViewStack{Basis: -1, Shrink: 1, MaxSize: -1}},
		}},
		{Kind: fe.ViewKindSpacer, Rows: 1, Width: 20},
		{Kind: fe.ViewKindSelectList, ID: "tracks", Rows: 3, Width: 20, MaxVisible: 5, Selected: 1, Items: []fe.ViewItem{
			{Value: "t1", Label: "Blue in Green", Description: "Miles Davis"},
			{Value: "t2", Label: "So What"},
			{Value: "t3", Label: "\x1b[38;2;224;160;64mFreddie\x1b[39m"},
		}},
		{Kind: fe.ViewKindSettingsList, ID: "opts", Rows: 3, Width: 20, MaxVisible: 2, Selected: 0, Query: "th", Searchable: true, Items: []fe.ViewItem{
			{ID: "auto", Label: "Auto", CurrentValue: "on", Values: []string{"on", "off"}},
			{ID: "theme", Label: "Theme", Description: "Colors", CurrentValue: "dark", Submenu: true},
		}},
		{Kind: fe.ViewKindBox, PaddingX: 1, PaddingY: 1, Rows: 3, Width: 20, Children: []fe.ViewNode{
			text(fe.ViewKindText, "inside", 18, nil),
		}},
		// Pi draws the image at most MaxWidthCells wide.
		{Kind: fe.ViewKindImage, Image: &fe.ViewImage{Ref: cover.Ref, MimeType: cover.MimeType, Data: cover.Data, Filename: cover.Filename, MaxWidthCells: 3}, Rows: 2, Width: 4},
		{Kind: fe.ViewKindLoader, Text: "Loading...", Frames: []string{"⠋", "⠙"}, Color: "accent", MessageColor: "muted", Rows: 1, Width: 20},
		{Kind: fe.ViewKindLines, Lines: []string{"\x1b[31mraw\x1b[39m  "}, Rows: 1, Width: 20},
		{Kind: fe.ViewKindLines, Lines: []string{"1:00 ━━── 4:00"}, Progress: &fe.ViewProgress{Value: 60, Max: 240}, Rows: 1, Width: 20},
		// The rows depicting the image are narrower than the node.
		{Kind: fe.ViewKindLines, Lines: []string{"▀▀", "▀▀"}, Image: &fe.ViewImage{Ref: cover.Ref, MimeType: cover.MimeType, Data: cover.Data}, Rows: 2, Width: 20},
		{Kind: fe.ViewKindVStack, Gap: 1, Align: "center", Rows: 3, Width: 20, Children: []fe.ViewNode{
			text(fe.ViewKindText, "a", 20, &fe.ViewStack{Basis: -1, Shrink: 1, MaxSize: 3}),
			text(fe.ViewKindText, "b", 20, &fe.ViewStack{Basis: -1, Shrink: 1, MaxSize: -1}),
		}},
		{Kind: "future", Rows: 1, Width: 20},
		{Kind: fe.ViewKindMarkdown, Text: "\x1b[1mbold\x1b[0m", Rows: 1, Width: 20},
	}}
	var lines []string
	for i, n := range root.Children {
		switch i {
		case 3:
			lines = append(lines, "left     right \x1b[32mgauge\x1b[0m")
		case 10:
			lines = append(lines, n.Lines...)
		case 14:
			lines = append(lines, "future row")
		case 15:
			lines = append(lines, "\x1b[1mbold\x1b[0m")
		default:
			for range n.Rows {
				lines = append(lines, "x")
			}
		}
	}
	return &fe.View{Root: root, Seq: 1}, lines
}

// kitWant is kitView drawn natively under the TSP id id.
func kitWant(id, blob string) map[string]any {
	c := func(i string) string { return id + ".0." + i }
	return map[string]any{"id": id, "k": "col", "p": map[string]any{"role": "pig.view"}, "c": []any{
		map[string]any{"id": id + ".0", "k": "col", "c": []any{
			map[string]any{"id": c("0"), "k": "rule", "p": map[string]any{"tone": "accent"}},
			map[string]any{"id": c("1"), "k": "card", "p": map[string]any{"role": "pig.bg.customMessageBg"}, "c": []any{
				map[string]any{"id": c("1.0"), "k": "text", "p": map[string]any{"spans": []any{
					map[string]any{"t": "Pick "}, map[string]any{"t": "a", "s": "strong"}, map[string]any{"t": " track"},
				}}},
			}},
			map[string]any{"id": c("2"), "k": "md", "p": map[string]any{"text": "- **one**"}},
			map[string]any{"id": c("3"), "k": "row", "p": map[string]any{"align": "stretch", "gap": "xs"}, "c": []any{
				map[string]any{"id": c("3.0"), "k": "text", "p": map[string]any{"text": "left", "wrap": "none", "truncate": "end", "grow": 1.0}},
				map[string]any{"id": c("3.1"), "k": "text", "p": map[string]any{"text": "right", "wrap": "none", "truncate": "end", "basis": 0.25, "min": map[string]any{"w": "2ch"}, "max": map[string]any{"w": "8ch"}}},
				map[string]any{"id": c("3.2"), "k": "ansi", "p": map[string]any{"text": "\x1b[32mgauge\x1b[0m"}},
			}},
			map[string]any{"id": c("4"), "k": "spacer", "p": map[string]any{"size": "xs"}},
			map[string]any{"id": c("5"), "k": "list", "p": map[string]any{"max": map[string]any{"lines": 5.0}, "selected": c("5.t2"), "empty": "No matching commands"}, "c": []any{
				map[string]any{"id": c("5.t1"), "k": "item", "p": map[string]any{"key": "t1", "label": "Blue in Green", "detail": "Miles Davis"}},
				map[string]any{"id": c("5.t2"), "k": "item", "p": map[string]any{"key": "t2", "label": "So What"}},
				map[string]any{"id": c("5.t3"), "k": "item", "p": map[string]any{"key": "t3", "label": []any{map[string]any{"t": "Freddie", "s": "accent"}}}},
			}},
			map[string]any{"id": c("6"), "k": "list", "p": map[string]any{"max": map[string]any{"lines": 2.0}, "selected": c("6.auto"), "filter": "th", "empty": "No matching settings"}, "c": []any{
				map[string]any{"id": c("6.auto"), "k": "item", "p": map[string]any{"key": "auto", "label": "Auto", "value": "on"}},
				map[string]any{"id": c("6.theme"), "k": "item", "p": map[string]any{"key": "theme", "label": "Theme", "detail": "Colors", "value": "dark"}},
			}},
			map[string]any{"id": c("7"), "k": "card", "p": map[string]any{"variant": "bare"}, "c": []any{
				map[string]any{"id": c("7.0"), "k": "text", "p": map[string]any{"text": "inside"}},
			}},
			map[string]any{"id": c("8"), "k": "image", "p": map[string]any{"blob": blob, "alt": "cover.png", "w": 1023.0, "h": 682.0, "max": map[string]any{"w": "3ch", "h": "2lines"}}},
			map[string]any{"id": c("9"), "k": "spinner", "p": map[string]any{"label": []any{map[string]any{"t": "Loading...", "s": "muted"}}, "tone": "accent"}},
			map[string]any{"id": c("10"), "k": "ansi", "p": map[string]any{"text": "\x1b[31mraw\x1b[39m"}},
			map[string]any{"id": c("11"), "k": "col", "c": []any{
				map[string]any{"id": c("11.0"), "k": "ansi", "p": map[string]any{"text": "", "hidden": true}},
				map[string]any{"id": c("11.1"), "k": "row", "p": map[string]any{"align": "center"}, "c": []any{
					map[string]any{"id": c("11.1.0"), "k": "ansi", "p": map[string]any{"text": "1:00", "min": map[string]any{"w": "5ch"}, "max": map[string]any{"w": "5ch"}}},
					map[string]any{"id": c("11.1.1"), "k": "progress", "p": map[string]any{"value": 0.25, "grow": 1.0, "min": map[string]any{"w": "3ch"}}},
					map[string]any{"id": c("11.1.2"), "k": "ansi", "p": map[string]any{"text": " 4:00", "min": map[string]any{"w": "6ch"}, "max": map[string]any{"w": "6ch"}}},
				}},
				map[string]any{"id": c("11.2"), "k": "ansi", "p": map[string]any{"text": "", "hidden": true}},
			}},
			map[string]any{"id": c("12"), "k": "image", "p": map[string]any{"blob": blob, "alt": "image", "w": 1023.0, "h": 682.0, "max": map[string]any{"w": "2ch", "h": "2lines"}}},
			map[string]any{"id": c("13"), "k": "col", "p": map[string]any{"gap": "xs", "align": "center"}, "c": []any{
				map[string]any{"id": c("13.0"), "k": "text", "p": map[string]any{"text": "a", "max": map[string]any{"h": "3lines"}}},
				map[string]any{"id": c("13.1"), "k": "text", "p": map[string]any{"text": "b"}},
			}},
			map[string]any{"id": c("14"), "k": "ansi", "p": map[string]any{"text": "future row"}},
			map[string]any{"id": c("15"), "k": "ansi", "p": map[string]any{"text": "\x1b[1mbold\x1b[0m"}},
		}},
	}}
}

// blobs returns the blob messages written since the last call, by id, and
// the verbs in order.
func (h *harness) blobsAndVerbs() (map[string]string, []string) {
	blobs := map[string]string{}
	var verbs []string
	for _, m := range h.messages() {
		verbs = append(verbs, m.verb)
		if m.verb == "b" {
			blobs[m.params["id"]] = m.params["mime"]
		}
	}
	return blobs, verbs
}

// Every component kind of a transcript view draws as its Tern element, in
// one add: the structure, the stack entries as flex props, the lists'
// selections and filters, the image blob, the loader's spinner, the
// progress and image annotations, and the rows of the nodes the member
// cannot draw natively (an unknown kind, also inside an hstack where its
// columns are cut out, and markdown carrying escape sequences). The image
// bytes go out once, as a blob, before the frame that shows them.
func TestTranscriptViewDrawsEveryKind(t *testing.T) {
	h := ready(t)
	view, lines := kitView(t)
	theme := kitTheme
	if err := h.session.Apply(fe.Frame{Theme: &theme, Ops: []fe.Op{{Kind: fe.Insert, Region: fe.RegionMain, ID: "ext", Node: fe.Lines{Lines: lines, View: view}}}}); err != nil {
		t.Fatal(err)
	}
	var ops []any
	var verbs []string
	blobs := map[string]string{}
	for _, m := range h.messages() {
		verbs = append(verbs, m.verb)
		switch m.verb {
		case "b":
			blobs[m.params["id"]] = m.params["mime"]
		case "f":
			ops = append(ops, m.body["ops"].([]any)...)
		}
	}
	ref := view.Root.Children[8].Image.Ref
	if !reflect.DeepEqual(blobs, map[string]string{ref: "image/png"}) {
		t.Fatalf("blobs = %#v", blobs)
	}
	if strings.Join(verbs, "") != "tbsf" {
		t.Fatalf("messages = %q, want the palette, the blob and the view sheet before the frame", verbs)
	}
	want := jsonOps(t, []any{"add", "n.ext", "main", nil, kitWant("n.ext", ref)})
	if !reflect.DeepEqual(ops, want) {
		t.Fatalf("view ops =\n%#v\nwant\n%#v", ops, want)
	}

	// The same image in a later view goes out no more.
	next := *view
	next.Seq = 2
	h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "ext2", Index: 1, Node: fe.Lines{Lines: lines, View: &next}})
	if blobs, _ := h.blobsAndVerbs(); len(blobs) != 0 {
		t.Fatalf("image sent again: %#v", blobs)
	}
}

// A view whose rows are not the surface's lines is not drawn: the lines
// are, as without one. So are lines whose click area takes clicks.
func TestViewThatDoesNotCoverTheLinesDrawsTheLines(t *testing.T) {
	h := ready(t)
	view, lines := kitView(t)
	ops := h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "ext", Node: fe.Lines{Lines: lines[:25], View: view}})
	if n := node(ops, "n.ext"); n["k"] != "ansi" || n["c"] != nil {
		t.Fatalf("short lines = %#v", n)
	}
	ops = h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "hit", Index: 1, Node: fe.Lines{Lines: lines, View: view, Click: &fe.Area{Rows: 1, Columns: 1}}})
	if n := node(ops, "n.hit.hit"); n == nil {
		t.Fatalf("clickable lines = %#v", ops)
	}
}

// A tool's result that its renderer's components drew is its view, in
// place of the rendered lines.
func TestToolResultViewIsTheToolsBody(t *testing.T) {
	h := ready(t)
	view, lines := kitView(t)
	tool := fe.ToolCard{Name: "play", Status: fe.ToolDone, Output: "raw", Result: lines, ResultView: view}
	theme := kitTheme
	if err := h.session.Apply(fe.Frame{Theme: &theme}); err != nil {
		t.Fatal(err)
	}
	ops := h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "t", Node: tool})
	n := node(ops, "n.t")
	if n["k"] != "tool" || len(n["c"].([]any)) != 1 {
		t.Fatalf("tool = %#v", n)
	}
	got := n["c"].([]any)[0]
	if want := jsonOps(t, kitWant("n.t.view", view.Root.Children[8].Image.Ref))[0]; !reflect.DeepEqual(got, want) {
		t.Fatalf("tool body =\n%#v\nwant\n%#v", got, want)
	}
	tool.ResultView = nil
	ops = h.applyOps(fe.Op{Kind: fe.Update, Region: fe.RegionMain, ID: "t", Node: tool})
	if n := node(ops, "n.t.out"); n == nil || n["k"] != "ansi" {
		t.Fatalf("tool without its view = %#v", ops)
	}
}

// A changed transcript view patches the elements it keeps: a selection
// move is one set on the list.
func TestTranscriptViewPatches(t *testing.T) {
	h := ready(t)
	view, lines := kitView(t)
	h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "ext", Node: fe.Lines{Lines: lines, View: view}})
	h.messages()
	next := *view
	next.Seq = 2
	next.Root.Children = append([]fe.ViewNode(nil), view.Root.Children...)
	next.Root.Children[5].Selected = 2
	ops := h.applyOps(fe.Op{Kind: fe.Update, Region: fe.RegionMain, ID: "ext", Node: fe.Lines{Lines: lines, View: &next}})
	if want := jsonOps(t, []any{"set", "n.ext.0.5", map[string]any{"selected": "n.ext.0.5.t3"}}); !reflect.DeepEqual(ops, want) {
		t.Fatalf("selection move = %#v", ops)
	}
}

// A view's theme overrides color its own element only: its root takes a
// role of its own, and the view sheet sets Tern's color variables for the
// overridden tokens under that role, and on its marks, read in light and
// dark (contrast_test.go). Another view keeps the surface's palette. A new
// override color resends the sheet; a view without overrides, or removed,
// leaves only the sheet's fills.
func TestViewThemeOverridesAreScopedToTheirView(t *testing.T) {
	h := ready(t)
	widget := func(kind fe.OpKind, id string, theme map[string]string, seq uint64) fe.Op {
		view := &fe.View{Root: fe.ViewNode{Kind: fe.ViewKindDynamicBorder, Color: "accent", Rows: 1, Width: 20}, Theme: theme, Seq: seq}
		return fe.Op{Kind: kind, Region: fe.RegionDock, ID: id, Node: fe.Lines{Lines: []string{"────"}, View: view}}
	}
	sheets := func() []string {
		var css []string
		for _, m := range h.messages() {
			if m.verb == "s" && m.body["name"] == viewSheetName {
				text, _ := m.body["css"].(string)
				css = append(css, text)
			}
		}
		return css
	}
	h.apply(widget(fe.Insert, "music", map[string]string{"accent": "#E0A040", "customMessageBg": "#203040"}, 1), widget(fe.Insert, "plain", nil, 1))
	root := `[data-role='pig.view.dock.music']`
	music := rule(root, "accent", "light-dark(#8d6528,#e0a040)", "customMessageBg", "rgb(from #203040 r g b/60%)") +
		rule(marks(root), "accent", "light-dark(#7a5723,#e5b162)")
	// The sheet's fills: a box or text on customMessageBg takes the
	// custom-message tint.
	fills := `.sf-card[data-role='pig.bg.customMessageBg']{background:var(--sf-tint-custom,var(--sf-card-bg))}`
	if got := sheets(); !reflect.DeepEqual(got, []string{fills + music}) {
		t.Fatalf("sheets = %q", got)
	}
	shown := h.session.view()
	roles := map[string]any{}
	for _, el := range shown.Dock.(tern.Nodes) {
		n := el.Node()
		roles[n.Props["key"].(string)] = n.Props["role"]
	}
	if roles["music"] != "pig.view.dock.music" || roles["plain"] != "pig.view" {
		t.Fatalf("roles = %#v", roles)
	}
	h.apply(widget(fe.Update, "music", map[string]string{"accent": "#40a0e0"}, 2))
	if got := sheets(); !reflect.DeepEqual(got, []string{fills + rule(root, "accent", "light-dark(#2e72a0,#4ba6e2)") + rule(marks(root), "accent", "light-dark(#27638a,#83c2eb)")}) {
		t.Fatalf("new track color sheets = %q", got)
	}
	h.apply(fe.Op{Kind: fe.Remove, Region: fe.RegionDock, ID: "music"})
	if got := sheets(); !reflect.DeepEqual(got, []string{fills}) {
		t.Fatalf("sheets after removal = %q", got)
	}
	// An overlay's view is scoped the same way, under its card.
	view, lines := overlayView("tracks")
	view.Theme = map[string]string{"error": "#ff0000"}
	h.apply(fe.Op{Kind: fe.Insert, Region: fe.RegionOverlay, ID: "overlay.1", Node: fe.Overlay{Lines: lines, Width: 30, View: view}})
	root = `[data-role='pig.view.layer.overlay.1.view']`
	overlay := rule(root, "error", "light-dark(#dc0000,#ff7171)") + rule(marks(root), "error", "light-dark(#c40000,#ff9292)")
	if got := sheets(); !reflect.DeepEqual(got, []string{fills + overlay}) {
		t.Fatalf("overlay sheets = %q", got)
	}
	h.apply(fe.Op{Kind: fe.Remove, Region: fe.RegionOverlay, ID: "overlay.1"})
	if got := sheets(); !reflect.DeepEqual(got, []string{fills}) {
		t.Fatalf("sheets after removal = %q", got)
	}
}

// The tokens in an override rule are Pi's closed set; a malformed color
// is skipped, and a view without a valid one has no rule.
func TestThemeRuleTokens(t *testing.T) {
	own := map[string]string{"muted": "#111111", "text": "#222222", "warning": "red", "borderMuted": "#333333", "customMessageBg": "#444444"}
	got := themeRule("n.x", own, own, nil, nil)
	root := `[data-role='pig.view.n.x']`
	want := rule(root, "borderMuted", "light-dark(#333333,#7f7f7f)", "customMessageBg", "rgb(from #444444 r g b/60%)",
		"muted", "light-dark(#111111,#7f7f7f)", "text", "light-dark(#222222,#9e9e9e)") +
		rule(marks(root), "borderMuted", "light-dark(#333333,#8f8f8f)", "muted", "light-dark(#111111,#8f8f8f)", "text", "light-dark(#222222,#b1b1b1)")
	if got != want {
		t.Fatalf("rule = %q\nwant %q", got, want)
	}
	if rule := themeRule("n.x", map[string]string{"accent": "e0a040"}, nil, nil, nil); rule != "" {
		t.Fatalf("malformed rule = %q", rule)
	}
	if got := themeRule("a'b", map[string]string{"dim": "#010203"}, nil, nil, nil); !strings.HasPrefix(got, `[data-role='pig.view.a\'b']`) {
		t.Fatalf("escaped id = %q", got)
	}
}

// ANSI text becomes spans: a truecolor foreground that is a theme token's
// takes the token's name, a basic color its meaning, bold strong, faint
// dim, italic em, reverse video a mark, an OSC 8 link the span's href;
// another color draws in the text's color, and text that draws in no style
// is plain.
func TestANSISpans(t *testing.T) {
	tokens := colorTokens(map[string]string{"accent": "#E0A040", "borderAccent": "#e0a040", "mdHeading": "#102030", "toolSuccessBg": "#000000"})
	for _, tc := range []struct {
		text  string
		spans []any
		plain string
	}{
		{"plain", nil, "plain"},
		{"\x1b[0mno style\x1b[39m", nil, "no style"},
		{"\x1b[38;2;224;160;64ma\x1b[39mb", []any{map[string]any{"t": "a", "s": "accent"}, map[string]any{"t": "b"}}, ""},
		{"\x1b[38:2::16:32:48mh\x1b[0m", []any{map[string]any{"t": "h", "s": "mdHeading"}}, ""},
		{"\x1b[38;2;1;2;3mx\x1b[48;2;0;0;0my", nil, "xy"},
		{"\x1b[31me\x1b[92ms\x1b[33mw\x1b[94mi\x1b[36ma\x1b[90md\x1b[38;5;1mE", []any{
			map[string]any{"t": "e", "s": "error"}, map[string]any{"t": "s", "s": "success"}, map[string]any{"t": "w", "s": "warning"},
			map[string]any{"t": "i", "s": "info"}, map[string]any{"t": "a", "s": "accent"}, map[string]any{"t": "d", "s": "dim"}, map[string]any{"t": "E", "s": "error"},
		}, ""},
		{"\x1b[1;3mbi\x1b[22;2mf\x1b[0;7mr\x1b[27m.", []any{
			map[string]any{"t": "bi", "s": "strong em"}, map[string]any{"t": "f", "s": "dim em"}, map[string]any{"t": "r", "s": "mark"}, map[string]any{"t": "."},
		}, ""},
		{"see \x1b]8;;https://pi.dev\x1b\\site\x1b]8;;\x1b\\ now", []any{
			map[string]any{"t": "see "}, map[string]any{"t": "site", "href": "https://pi.dev"}, map[string]any{"t": " now"},
		}, ""},
	} {
		spans, plain := ansiSpans(tc.text, tokens)
		if !reflect.DeepEqual(spans, tc.spans) || plain != tc.plain {
			t.Errorf("%q: spans %#v plain %q, want %#v %q", tc.text, spans, plain, tc.spans, tc.plain)
		}
	}
}

// A changed theme maps the dock views' spans to the new colors' tokens.
func TestThemeChangeRemapsDockViews(t *testing.T) {
	h := ready(t)
	theme := kitTheme
	view := &fe.View{Root: fe.ViewNode{Kind: fe.ViewKindText, Text: "\x1b[38;2;16;32;48mtrack\x1b[39m", Rows: 1, Width: 20}, Seq: 1}
	op := fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "w", Node: fe.Lines{Lines: []string{"track"}, View: view}}
	if err := h.session.Apply(fe.Frame{Theme: &theme, Ops: []fe.Op{op}}); err != nil {
		t.Fatal(err)
	}
	h.messages()
	light := fe.Theme{Name: "light", Colors: map[string]string{"accent": "#102030"}}
	if err := h.session.Apply(fe.Frame{Theme: &light}); err != nil {
		t.Fatal(err)
	}
	var ops []any
	for _, f := range h.frames() {
		ops = append(ops, f.body["ops"].([]any)...)
	}
	want := jsonOps(t,
		[]any{"set", "dock.w.0", map[string]any{"spans": []any{map[string]any{"t": "track", "s": "accent"}}}},
		[]any{"set", "dock.w.0", map[string]any{"text": nil}})
	if !reflect.DeepEqual(ops, want) {
		t.Fatalf("remap = %#v", ops)
	}
}

func overlayView(focus string) (*fe.View, []string) {
	list := fe.ViewNode{Kind: fe.ViewKindSelectList, ID: "tracks", Rows: 2, Width: 30, MaxVisible: 5, Selected: 0, Items: []fe.ViewItem{
		{Value: "t1", Label: "Blue in Green"}, {Value: "t2", Label: "So What"},
	}}
	settings := fe.ViewNode{Kind: fe.ViewKindSettingsList, ID: "opts", Rows: 1, Width: 30, MaxVisible: 1, Selected: 0, Items: []fe.ViewItem{
		{ID: "shuffle", Label: "Shuffle", CurrentValue: "off", Values: []string{"on", "off"}},
	}}
	return &fe.View{Root: fe.ViewNode{Kind: fe.ViewKindContainer, Rows: 3, Width: 30, Children: []fe.ViewNode{list, settings}}, Focus: focus, Seq: 1}, []string{"→ Blue in Green", "  So What", "Shuffle  off"}
}

// clickView is a view, 20 cells wide, of the elements that take clicks in
// nested containers, and the 16 lines it describes: text, an hstack of
// text, a box around text and a vstack of a spacer and markdown, a select
// list, a searchable settings list, a lines list, a dynamic border, text
// with theme overrides and a settings list whose submenu is open.
func clickView() (*fe.View, []string) {
	text := func(text string, width int) fe.ViewNode {
		return fe.ViewNode{Kind: fe.ViewKindText, Text: text, Rows: 1, Width: width}
	}
	submenu := fe.ViewNode{Kind: fe.ViewKindSelectList, ID: "pick", Rows: 1, Width: 20, MaxVisible: 3, Items: []fe.ViewItem{{Value: "dark", Label: "dark"}}}
	root := fe.ViewNode{Kind: fe.ViewKindContainer, Rows: 16, Width: 20, Children: []fe.ViewNode{
		text("Title", 20),
		{Kind: fe.ViewKindHStack, Rows: 3, Width: 20, Children: []fe.ViewNode{
			text("L", 5),
			{Kind: fe.ViewKindBox, PaddingX: 1, PaddingY: 1, Rows: 3, Width: 9, Children: []fe.ViewNode{text("in", 7)}},
			{Kind: fe.ViewKindVStack, Rows: 2, Width: 6, Children: []fe.ViewNode{
				{Kind: fe.ViewKindSpacer, Rows: 1, Width: 6},
				{Kind: fe.ViewKindMarkdown, Text: "*m*", Rows: 1, Width: 6},
			}},
		}},
		{Kind: fe.ViewKindSelectList, ID: "tracks", Rows: 2, Width: 20, MaxVisible: 5, Selected: 0, Items: []fe.ViewItem{
			{Value: "t1", Label: "Blue in Green"}, {Value: "t2", Label: "So What"},
		}},
		{Kind: fe.ViewKindSettingsList, ID: "opts", Rows: 4, Width: 20, MaxVisible: 2, Selected: 0, Searchable: true, Items: []fe.ViewItem{
			{ID: "auto", Label: "Auto", CurrentValue: "on"}, {ID: "theme", Label: "Theme", CurrentValue: "dark"},
		}},
		{Kind: fe.ViewKindLines, Lines: []string{"> Naima", "  Alabama"}, Rows: 2, Width: 20, List: &fe.ViewList{Selected: -1, Items: []fe.ViewListItem{
			{Label: "Naima"}, {Label: "Alabama"},
		}}},
		{Kind: fe.ViewKindDynamicBorder, Rows: 1, Width: 20},
		{Kind: fe.ViewKindText, Text: "themed", Rows: 1, Width: 20, Theme: map[string]string{"accent": "#40a0e0"}},
		{Kind: fe.ViewKindSettingsList, ID: "prefs", Rows: 1, Width: 20, MaxVisible: 3, Selected: 0, Submenu: &submenu, Items: []fe.ViewItem{
			{ID: "theme", Label: "Theme", CurrentValue: "dark", Submenu: true},
		}},
	}}
	lines := make([]string, 16)
	for i := range lines {
		lines[i] = "x"
	}
	lines[11], lines[12] = "> Naima", "  Alabama"
	return &fe.View{Root: root, Seq: 1}, lines
}

// clickWant is clickView drawn under the TSP id id, its elements taking
// clicks when clicks holds.
func clickWant(id string, clicks bool) map[string]any {
	c := func(i string) string { return id + ".0." + i }
	rule := map[string]any{"id": c("5"), "k": "rule"}
	if clicks {
		rule["p"] = map[string]any{"actions": map[string]any{"click": "click"}}
	}
	p := func(props map[string]any) map[string]any {
		if clicks {
			props["actions"] = map[string]any{"click": "click"}
		}
		return props
	}
	return map[string]any{"id": id, "k": "col", "p": map[string]any{"role": "pig.view"}, "c": []any{
		map[string]any{"id": id + ".0", "k": "col", "c": []any{
			map[string]any{"id": c("0"), "k": "text", "p": p(map[string]any{"text": "Title"})},
			map[string]any{"id": c("1"), "k": "row", "p": map[string]any{"align": "stretch"}, "c": []any{
				map[string]any{"id": c("1.0"), "k": "text", "p": p(map[string]any{"text": "L"})},
				map[string]any{"id": c("1.1"), "k": "card", "p": p(map[string]any{"variant": "bare"}), "c": []any{
					map[string]any{"id": c("1.1.0"), "k": "text", "p": p(map[string]any{"text": "in"})},
				}},
				map[string]any{"id": c("1.2"), "k": "col", "c": []any{
					map[string]any{"id": c("1.2.0"), "k": "spacer", "p": p(map[string]any{"size": "xs"})},
					map[string]any{"id": c("1.2.1"), "k": "md", "p": p(map[string]any{"text": "*m*"})},
				}},
			}},
			map[string]any{"id": c("2"), "k": "list", "p": map[string]any{"max": map[string]any{"lines": 5.0}, "selected": c("2.t1"), "empty": "No matching commands"}, "c": []any{
				map[string]any{"id": c("2.t1"), "k": "item", "p": map[string]any{"key": "t1", "label": "Blue in Green"}},
				map[string]any{"id": c("2.t2"), "k": "item", "p": map[string]any{"key": "t2", "label": "So What"}},
			}},
			map[string]any{"id": c("3"), "k": "list", "p": map[string]any{"max": map[string]any{"lines": 2.0}, "selected": c("3.auto"), "empty": "No settings available"}, "c": []any{
				map[string]any{"id": c("3.auto"), "k": "item", "p": map[string]any{"key": "auto", "label": "Auto", "value": "on"}},
				map[string]any{"id": c("3.theme"), "k": "item", "p": map[string]any{"key": "theme", "label": "Theme", "value": "dark"}},
			}},
			map[string]any{"id": c("4"), "k": "list", "c": []any{
				map[string]any{"id": c("4.0"), "k": "item", "p": map[string]any{"label": "Naima"}},
				map[string]any{"id": c("4.1"), "k": "item", "p": map[string]any{"label": "Alabama"}},
			}},
			rule,
			map[string]any{"id": c("6"), "k": "col", "p": map[string]any{"role": "pig.view." + c("6")}, "c": []any{
				map[string]any{"id": c("6.0"), "k": "text", "p": p(map[string]any{"text": "themed"})},
			}},
			map[string]any{"id": c("7"), "k": "col", "c": []any{
				map[string]any{"id": c("7.0"), "k": "list", "p": map[string]any{"max": map[string]any{"lines": 3.0}, "selected": c("7.0.dark"), "empty": "No matching commands"}, "c": []any{
					map[string]any{"id": c("7.0.dark"), "k": "item", "p": map[string]any{"key": "dark", "label": "dark"}},
				}},
			}},
		}},
	}}
}

// clickEvents plays Tern's pointer events on each element of clickView
// drawn under the TSP id id on surface sf.
func clickEvents(h *harness, sf, id string) {
	c := func(i string) string { return id + ".0." + i }
	click := func(id string) {
		h.event(`{"ev":"action","sf":"` + sf + `","id":"` + id + `","act":"click"}`)
	}
	item := func(ev, list, item string) {
		h.event(`{"ev":"` + ev + `","sf":"` + sf + `","id":"` + list + `","item":"` + item + `"}`)
	}
	click(c("0"))
	click(c("1"))
	click(c("1.0"))
	click(c("1.1"))
	click(c("1.1.0"))
	click(c("1.2"))
	click(c("1.2.0"))
	click(c("1.2.1"))
	click(c("2"))
	item("select", c("2"), c("2.t2"))
	item("activate", c("2"), c("2.t1"))
	item("select", c("2"), c("2.gone"))
	item("activate", c("2"), c("2.gone"))
	item("select", c("3"), c("3.theme"))
	item("activate", c("3"), c("3.theme"))
	item("select", c("4"), c("4.1"))
	item("activate", c("4"), c("4.1"))
	item("select", c("4"), c("4.2"))
	item("activate", c("4"), c("4.2"))
	click(c("5"))
	click(c("6"))
	click(c("6.0"))
	item("select", c("7.0"), c("7.0.dark"))
	click(c("7"))
	h.synced()
}

// clickWantClicks are PiG's clicks clickEvents makes on node's clickView:
// one per click on an element, at the path of its view node; a select
// list's item by its value and a settings list's by its id; a lines
// list's row; an activate as that click with Count 2. A container, a stack,
// an item not shown and an open submenu take none.
func clickWantClicks(node string) []fe.Click {
	return []fe.Click{
		{Node: node, ViewPath: []int{0}},
		{Node: node, ViewPath: []int{1, 0}},
		{Node: node, ViewPath: []int{1, 1}},
		{Node: node, ViewPath: []int{1, 1, 0}},
		{Node: node, ViewPath: []int{1, 2, 0}},
		{Node: node, ViewPath: []int{1, 2, 1}},
		{Node: node, ViewPath: []int{2}, Item: "t2"},
		{Node: node, ViewPath: []int{2}, Item: "t1", Count: 2},
		{Node: node, ViewPath: []int{3}, Item: "theme"},
		{Node: node, ViewPath: []int{3}, Item: "theme", Count: 2},
		{Node: node, ViewPath: []int{4}, Row: 1},
		{Node: node, ViewPath: []int{4}, Row: 1, Count: 2},
		{Node: node, ViewPath: []int{5}},
		{Node: node, ViewPath: []int{6}},
	}
}

// An overlay's view draws in its card, and a click on one of its elements
// is PiG's click on the overlay at that element's view node. An overlay
// that leaves the keys takes clicks too: PiG hands them to its component.
func TestOverlayViewClicksArePigClicks(t *testing.T) {
	for _, nonCapturing := range []bool{false, true} {
		h := dockWithLines(t)
		view, lines := clickView()
		overlay := fe.Overlay{Lines: lines, Width: 20, Anchor: "center", View: view, NonCapturing: nonCapturing}
		ops := h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionOverlay, ID: "overlay.1", Node: overlay})
		card := node(ops, "layer.overlay.1")
		if card["k"] != "overlay" || len(card["c"].([]any)) != 1 {
			t.Fatalf("card = %#v", card)
		}
		want := jsonOps(t, clickWant("layer.overlay.1.view", true))[0].(map[string]any)
		want["p"].(map[string]any)["key"] = "view"
		if got := node(ops, "layer.overlay.1.view"); !reflect.DeepEqual(got, want) {
			t.Fatalf("overlay view =\n%#v\nwant\n%#v", got, want)
		}
		clickEvents(h, "pig", "layer.overlay.1.view")
		if want := clickWantClicks("overlay.1"); !reflect.DeepEqual(h.clicks, want) {
			t.Fatalf("non-capturing %v: clicks =\n%#v\nwant\n%#v", nonCapturing, h.clicks, want)
		}
		if len(h.actions) != 0 {
			t.Fatalf("actions = %#v", h.actions)
		}
	}
}

// A titled overlay's frame is its card, and its body is the view node in
// the middle of the frame's hstack: a click inside the body names the
// body's path under the frame.
func TestFramedOverlayViewClicksNameTheBodysPath(t *testing.T) {
	h := dockWithLines(t)
	side := fe.ViewNode{Kind: fe.ViewKindLines, Lines: []string{"│"}, Rows: 1, Width: 1}
	lines := []string{"┌─ Kit ────┐", "│ab        │", "└──────────┘"}
	view := &fe.View{Seq: 1, Root: fe.ViewNode{Kind: fe.ViewKindContainer, Rows: 3, Width: 12, Children: []fe.ViewNode{
		{Kind: fe.ViewKindLines, Lines: lines[:1], Rows: 1, Width: 12},
		{Kind: fe.ViewKindHStack, Rows: 1, Width: 12, Children: []fe.ViewNode{
			side,
			{Kind: fe.ViewKindBox, Rows: 1, Width: 10, Children: []fe.ViewNode{{Kind: fe.ViewKindText, Text: "ab", Rows: 1, Width: 10}}},
			side,
		}},
		{Kind: fe.ViewKindLines, Lines: lines[2:], Rows: 1, Width: 12},
	}}}
	ops := h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionOverlay, ID: "overlay.2", Node: fe.Overlay{Lines: lines, Width: 12, View: view}})
	id := "layer.overlay.2.view"
	click := map[string]any{"click": "click"}
	want := jsonOps(t, map[string]any{"id": id, "k": "col", "p": map[string]any{"key": "view", "role": "pig.view"}, "c": []any{
		map[string]any{"id": id + ".0", "k": "col", "c": []any{
			map[string]any{"id": id + ".0.0", "k": "text", "p": map[string]any{"spans": []any{map[string]any{"t": "Kit", "s": "strong"}}}},
			map[string]any{"id": id + ".0.1", "k": "card", "p": map[string]any{"variant": "bare", "actions": click}, "c": []any{
				map[string]any{"id": id + ".0.1.0", "k": "text", "p": map[string]any{"text": "ab", "actions": click}},
			}},
		}},
	}})[0]
	if got := node(ops, id); !reflect.DeepEqual(got, want) {
		t.Fatalf("framed view =\n%#v\nwant\n%#v", got, want)
	}
	// The title is the frame's border, which takes no click.
	for _, target := range []string{id + ".0.0", id + ".0.1", id + ".0.1.0"} {
		h.event(`{"ev":"action","sf":"pig","id":"` + target + `","act":"click"}`)
	}
	h.synced()
	if want := []fe.Click{{Node: "overlay.2", ViewPath: []int{1, 1}}, {Node: "overlay.2", ViewPath: []int{1, 1, 0}}}; !reflect.DeepEqual(h.clicks, want) {
		t.Fatalf("clicks = %#v", h.clicks)
	}
}

// Tern's events on lines lists, as the owner's real window sent them on
// pig-music's Player (the Up Next list, at view path 3.4.2) and Library
// pages: a select per click and, after a double click's second, an
// activate. Each select is PiG's click on the item's row and the activate
// is that click again as a double click's second, so PiG can hand the
// component the second click with clickCount 2 (TuiSurface.Click).
func TestRecordedListSelectsAndActivatesArePigClicks(t *testing.T) {
	h := dockWithLines(t)
	text := func(width int) fe.ViewNode { return fe.ViewNode{Kind: fe.ViewKindText, Text: "x", Rows: 1, Width: width} }
	list := func(n, width int) fe.ViewNode {
		l := fe.ViewNode{Kind: fe.ViewKindLines, Rows: n, Width: width, List: &fe.ViewList{Selected: n / 2}}
		for i := range n {
			l.Lines = append(l.Lines, "track "+strconv.Itoa(i))
			l.List.Items = append(l.List.Items, fe.ViewListItem{Label: "track " + strconv.Itoa(i), Columns: []string{"3:00"}})
		}
		return l
	}
	upNext := fe.ViewNode{Kind: fe.ViewKindVStack, Rows: 57, Width: 40, Children: []fe.ViewNode{text(40), text(40), list(54, 40), text(40)}}
	root := fe.ViewNode{Kind: fe.ViewKindContainer, Rows: 62, Width: 107, Children: []fe.ViewNode{
		text(107), text(107), text(107),
		{Kind: fe.ViewKindHStack, Rows: 57, Width: 107, Children: []fe.ViewNode{text(30), text(1), text(30), text(1), upNext}},
		list(2, 107),
	}}
	lines := make([]string, 62)
	for i := range lines {
		lines[i] = "x"
	}
	h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionOverlay, ID: "overlay.2", Node: fe.Overlay{Lines: lines, Width: 107, Anchor: "top", View: &fe.View{Root: root, Seq: 1}}})
	for _, event := range []string{
		`{"ev":"select","id":"layer.overlay.2.view.0.3.4.2","item":"layer.overlay.2.view.0.3.4.2.15","sf":"pig"}`,
		`{"ev":"select","id":"layer.overlay.2.view.0.3.4.2","item":"layer.overlay.2.view.0.3.4.2.27","sf":"pig"}`,
		`{"ev":"select","id":"layer.overlay.2.view.0.3.4.2","item":"layer.overlay.2.view.0.3.4.2.27","sf":"pig"}`,
		`{"ev":"activate","id":"layer.overlay.2.view.0.3.4.2","item":"layer.overlay.2.view.0.3.4.2.27","sf":"pig"}`,
		`{"ev":"select","id":"layer.overlay.2.view.0.4","item":"layer.overlay.2.view.0.4.1","sf":"pig"}`,
		`{"ev":"select","id":"layer.overlay.2.view.0.4","item":"layer.overlay.2.view.0.4.1","sf":"pig"}`,
		`{"ev":"activate","id":"layer.overlay.2.view.0.4","item":"layer.overlay.2.view.0.4.1","sf":"pig"}`,
	} {
		h.event(event)
	}
	h.synced()
	want := []fe.Click{
		{Node: "overlay.2", ViewPath: []int{3, 4, 2}, Row: 15},
		{Node: "overlay.2", ViewPath: []int{3, 4, 2}, Row: 27},
		{Node: "overlay.2", ViewPath: []int{3, 4, 2}, Row: 27},
		{Node: "overlay.2", ViewPath: []int{3, 4, 2}, Row: 27, Count: 2},
		{Node: "overlay.2", ViewPath: []int{4}, Row: 1},
		{Node: "overlay.2", ViewPath: []int{4}, Row: 1},
		{Node: "overlay.2", ViewPath: []int{4}, Row: 1, Count: 2},
	}
	if !reflect.DeepEqual(h.clicks, want) {
		t.Fatalf("clicks =\n%#v\nwant\n%#v", h.clicks, want)
	}
}

// A click on an element of a dock node's view is PiG's click on the dock
// node; a view that is one element names the root, an empty path.
func TestDockViewClicksArePigClicks(t *testing.T) {
	h := ready(t)
	view, lines := clickView()
	ops := h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "w", Node: fe.Lines{Lines: lines, View: view}})
	want := jsonOps(t, clickWant("dock.w", true))[0].(map[string]any)
	want["p"].(map[string]any)["key"] = "w"
	if got := node(ops, "dock.w"); !reflect.DeepEqual(got, want) {
		t.Fatalf("dock view =\n%#v\nwant\n%#v", got, want)
	}
	clickEvents(h, "pig", "dock.w")
	if want := clickWantClicks("w"); !reflect.DeepEqual(h.clicks, want) {
		t.Fatalf("clicks =\n%#v\nwant\n%#v", h.clicks, want)
	}

	h.clicks = nil
	text := &fe.View{Root: fe.ViewNode{Kind: fe.ViewKindText, Text: "now", Rows: 1, Width: 20}, Seq: 1}
	ops = h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "one", Index: 1, Node: fe.Lines{Lines: []string{"now"}, View: text}})
	if want := jsonOps(t, map[string]any{"id": "dock.one.0", "k": "text", "p": map[string]any{"text": "now", "actions": map[string]any{"click": "click"}}})[0]; !reflect.DeepEqual(node(ops, "dock.one.0"), want) {
		t.Fatalf("root text = %#v", node(ops, "dock.one.0"))
	}
	h.event(`{"ev":"action","sf":"pig","id":"dock.one.0","act":"click"}`)
	h.synced()
	if want := []fe.Click{{Node: "one", ViewPath: []int{}}}; !reflect.DeepEqual(h.clicks, want) {
		t.Fatalf("root click = %#v", h.clicks)
	}
}

// The transcript's views take no click: PiG has no component to hand one
// to. Their elements bind no action, and a click on one is not PiG's.
func TestTranscriptViewTakesNoClick(t *testing.T) {
	h := ready(t)
	view, lines := clickView()
	ops := h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "ext", Node: fe.Lines{Lines: lines, View: view}})
	want := jsonOps(t, []any{"add", "n.ext", "main", nil, clickWant("n.ext", false)})
	if !reflect.DeepEqual(ops, want) {
		t.Fatalf("transcript view =\n%#v\nwant\n%#v", ops, want)
	}
	clickEvents(h, "pig", "n.ext")
	if len(h.clicks) != 0 {
		t.Fatalf("clicks = %#v", h.clicks)
	}
}

// handled lists the TSP ids of the elements under n, whose id is id, that
// have a pointer handler.
func handled(n tern.Node, id string) []string {
	var ids []string
	h := n.Handlers
	if h.OnClick != nil || h.OnSelect != nil || h.OnActivate != nil || h.OnDblClick != nil {
		ids = append(ids, id)
	}
	for i, child := range n.Children {
		c := child.(tern.Node)
		key, ok := c.Props["key"].(string)
		if !ok {
			key = strconv.Itoa(i)
		}
		ids = append(ids, handled(c, id+"."+key)...)
	}
	return ids
}

// Without Env.Click, or without a node to name, a view binds nothing: the
// same elements, without actions or handlers.
func TestViewWithoutClickBindsNothing(t *testing.T) {
	view, lines := clickView()
	record := func(fe.Click) {}
	for _, tc := range []struct {
		name string
		s    *session
		node string
	}{
		{"no Env.Click", &session{}, "overlay.1"},
		{"no node", &session{env: fe.Env{Click: record}}, ""},
	} {
		n := tc.s.viewElement("", "v", view, lines, tc.node)
		if got, want := jsonOps(t, wireNode("v", n))[0], jsonOps(t, clickWant("v", false))[0]; !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: view =\n%#v\nwant\n%#v", tc.name, got, want)
		}
		if ids := handled(n, "v"); len(ids) != 0 {
			t.Fatalf("%s: handlers on %q", tc.name, ids)
		}
	}
	n := (&session{env: fe.Env{Click: record}}).viewElement("", "v", view, lines, "overlay.1")
	want := []string{"v.0.0", "v.0.1.0", "v.0.1.1", "v.0.1.1.0", "v.0.1.2.0", "v.0.1.2.1", "v.0.2", "v.0.3", "v.0.4", "v.0.5", "v.0.6.0"}
	if ids := handled(n, "v"); !reflect.DeepEqual(ids, want) {
		t.Fatalf("handlers on %q, want %q", ids, want)
	}
}

// A dock view's change that keeps its structure goes out as the
// reconciler's sets, and a view's removal leaves plain lines raw again.
func TestDockViewUpdatesAreSets(t *testing.T) {
	h := ready(t)
	loader := func(seq uint64, message string) fe.Op {
		view := &fe.View{Root: fe.ViewNode{Kind: fe.ViewKindLoader, Text: message, Frames: []string{"-"}, Rows: 1, Width: 20}, Seq: seq}
		return fe.Op{Kind: fe.Update, Region: fe.RegionDock, ID: "w", Node: fe.Lines{Lines: []string{"- " + message}, View: view}}
	}
	first := loader(1, "Loading")
	first.Kind = fe.Insert
	ops := h.applyOps(first)
	if n := node(ops, "dock.w.0"); n["k"] != "spinner" || props(n)["label"] != "Loading" {
		t.Fatalf("loader = %#v", ops)
	}
	ops = h.applyOps(loader(2, "Fetching"))
	if want := jsonOps(t, []any{"set", "dock.w.0", map[string]any{"label": "Fetching"}}); !reflect.DeepEqual(ops, want) {
		t.Fatalf("loader update = %#v", ops)
	}
	ops = h.applyOps(fe.Op{Kind: fe.Update, Region: fe.RegionDock, ID: "w", Node: fe.Lines{Lines: []string{"done"}}})
	if n := node(ops, "dock.w"); n["k"] != "ansi" || props(n)["text"] != "done" {
		t.Fatalf("lines after the view = %#v", ops)
	}
	renders := h.session.renders
	ops = h.applyOps(fe.Op{Kind: fe.Update, Region: fe.RegionDock, ID: "w", Node: fe.Lines{Lines: []string{"done!"}}})
	if want := jsonOps(t, []any{"text", "dock.w", "append", "!"}); !reflect.DeepEqual(ops, want) || h.session.renders != renders {
		t.Fatalf("raw lines again = %#v", ops)
	}
}

// A settings list whose submenu is open draws the submenu in its place.
func TestSettingsSubmenuDrawsInTheListsPlace(t *testing.T) {
	h := ready(t)
	submenu := fe.ViewNode{Kind: fe.ViewKindSelectList, ID: "pick", Rows: 1, Width: 20, MaxVisible: 3, Selected: 0, Items: []fe.ViewItem{{Value: "dark", Label: "dark"}}}
	view := &fe.View{Root: fe.ViewNode{Kind: fe.ViewKindSettingsList, ID: "opts", Rows: 1, Width: 20, MaxVisible: 3, Selected: 0, Submenu: &submenu, Items: []fe.ViewItem{{ID: "theme", Label: "Theme", CurrentValue: "dark", Submenu: true}}}, Seq: 1}
	ops := h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "s", Node: fe.Lines{Lines: []string{"→ dark"}, View: view}})
	if n := node(ops, "n.s.0.0"); n["k"] != "list" || props(n)["selected"] != "n.s.0.0.dark" {
		t.Fatalf("submenu = %#v", ops)
	}
}

// A subtree's own overrides, as each widget of the dock's combined view
// carries them, scope to that subtree: it is a col with a role of its own,
// the sheet's rule names it, and its text in the override color takes the
// overridden token, while a sibling's text in that color does not.
func TestSubtreeThemeOverridesScopeToTheSubtree(t *testing.T) {
	h := ready(t)
	theme := kitTheme
	tracked := "\x1b[38;2;64;160;224mtrack\x1b[39m"
	view := &fe.View{Seq: 1, Root: fe.ViewNode{Kind: fe.ViewKindContainer, Rows: 2, Width: 20, Children: []fe.ViewNode{
		{Kind: fe.ViewKindText, Text: tracked, Rows: 1, Width: 20, Theme: map[string]string{"accent": "#40a0e0"}},
		{Kind: fe.ViewKindText, Text: tracked, Rows: 1, Width: 20},
	}}}
	op := fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "widgets", Node: fe.Lines{Lines: []string{"track", "track"}, View: view}}
	if err := h.session.Apply(fe.Frame{Theme: &theme, Ops: []fe.Op{op}}); err != nil {
		t.Fatal(err)
	}
	var ops []any
	var css []string
	for _, m := range h.messages() {
		switch {
		case m.verb == "f":
			ops = append(ops, m.body["ops"].([]any)...)
		case m.verb == "s" && m.body["name"] == viewSheetName:
			css = append(css, m.body["css"].(string))
		}
	}
	themed := node(ops, "dock.widgets.0.0")
	if themed["k"] != "col" || props(themed)["role"] != "pig.view.dock.widgets.0.0" {
		t.Fatalf("themed subtree = %#v", themed)
	}
	if got := props(node(ops, "dock.widgets.0.0.0"))["spans"]; !reflect.DeepEqual(got, []any{map[string]any{"t": "track", "s": "accent"}}) {
		t.Fatalf("themed text = %#v", got)
	}
	if got := props(node(ops, "dock.widgets.0.1")); got["text"] != "track" || got["spans"] != nil {
		t.Fatalf("sibling text = %#v", got)
	}
	root := `[data-role='pig.view.dock.widgets.0.0']`
	if len(css) != 1 || !strings.HasSuffix(css[0], rule(root, "accent", "light-dark(#2e72a0,#4ba6e2)")+rule(marks(root), "accent", "light-dark(#27638a,#83c2eb)")) {
		t.Fatalf("sheets = %q", css)
	}
	// The next frame without the override drops its rule.
	next := *view
	next.Seq = 2
	next.Root.Children = []fe.ViewNode{view.Root.Children[1], view.Root.Children[1]}
	op.Kind, op.Node = fe.Update, fe.Lines{Lines: []string{"track", "track"}, View: &next}
	h.apply(op)
	css = nil
	for _, m := range h.messages() {
		if m.verb == "s" && m.body["name"] == viewSheetName {
			css = append(css, m.body["css"].(string))
		}
	}
	if len(css) != 1 || strings.Contains(css[0], "pig.view.dock") {
		t.Fatalf("sheets without the override = %q", css)
	}
}

// Rows that show a position keep their text around a native bar: the
// longest run of bar glyphs, a play bar's, is the bar and the text beside
// it keeps its cells (a colored icon and the times); rows above and below
// draw as they are; rows without a run keep their whole text with the bar
// after it.
func TestProgressRowsKeepTheirTextAroundTheBar(t *testing.T) {
	show := func(lines ...string) []any {
		b := &viewer{}
		n := b.node(&fe.ViewNode{Kind: fe.ViewKindLines, Lines: lines, Progress: &fe.ViewProgress{Value: 50, Max: 176}, Rows: len(lines), Width: 30}, "p", region{}, nil)
		return jsonOps(t, wireNode("p", n))[0].(map[string]any)["c"].([]any)
	}
	part := func(parts []any, i int) map[string]any { return props(parts[i].(map[string]any)) }
	parts := show("Blue in Green", " \x1b[33m▶\x1b[39m 0:50 \x1b[33m━━━━\x1b[39m──── 2:56 ")
	if got := part(parts, 0)["text"]; got != "Blue in Green" {
		t.Fatalf("row above = %#v", got)
	}
	bar := parts[1].(map[string]any)["c"].([]any)
	// The bar's color sequence stays with the text before it, as the
	// terminal reads it.
	if got := part(bar, 0); got["text"] != " \x1b[33m▶\x1b[39m 0:50\x1b[33m" || got["min"].(map[string]any)["w"] != "8ch" {
		t.Fatalf("left of the bar = %#v", got)
	}
	if got := part(bar, 1)["value"]; got != 50.0/176 {
		t.Fatalf("bar = %#v", got)
	}
	// The text after the bar starts in the SGR state the line had there.
	if got := part(bar, 2); got["text"] != "\x1b[33m\x1b[39m\x1b[33m\x1b[39m 2:56" || got["min"].(map[string]any)["w"] != "6ch" {
		t.Fatalf("right of the bar = %#v", got)
	}
	if got := part(parts, 2)["hidden"]; got != true {
		t.Fatalf("no rows below = %#v", part(parts, 2))
	}
	// Two glyphs are a separator, not a bar.
	parts = show("◐ Title ── Artist 1:23/3:45")
	bar = parts[1].(map[string]any)["c"].([]any)
	if got := part(bar, 0); got["text"] != "◐ Title ── Artist 1:23/3:45" || part(bar, 2)["hidden"] != true || part(parts, 0)["hidden"] != true {
		t.Fatalf("a row without a bar = %#v / %#v", got, part(bar, 2))
	}
}

// Rows that are a list draw as a list: each item's label, its detail and
// every column but the last as the detail, the last column as the value,
// and the selection. Items that are not one per row leave the rows as
// they are.
func TestLinesListIsANativeList(t *testing.T) {
	b := &viewer{}
	rows := []string{"> Blue in Green  Miles Davis  Kind of Blue  5:37", "  Naima          John Coltrane  Giant Steps  4:21"}
	list := &fe.ViewList{Selected: 1, Items: []fe.ViewListItem{
		{Label: "Blue in Green", Detail: "Miles Davis", Columns: []string{"Kind of Blue", "5:37"}},
		{Label: "\x1b[1mNaima\x1b[22m", Columns: []string{"4:21"}},
	}}
	n := b.node(&fe.ViewNode{Kind: fe.ViewKindLines, Lines: rows, List: list, Rows: 2, Width: 50}, "l", region{}, nil)
	want := jsonOps(t, map[string]any{"id": "l", "k": "list", "p": map[string]any{"selected": "l.1"}, "c": []any{
		map[string]any{"id": "l.0", "k": "item", "p": map[string]any{"label": "Blue in Green", "detail": "Miles Davis · Kind of Blue", "value": "5:37"}},
		map[string]any{"id": "l.1", "k": "item", "p": map[string]any{"label": []any{map[string]any{"t": "Naima", "s": "strong"}}, "value": "4:21"}},
	}})
	if got := jsonOps(t, wireNode("l", n)); !reflect.DeepEqual(got, want) {
		t.Fatalf("list =\n%#v\nwant\n%#v", got, want)
	}
	list.Selected = -1
	n = b.node(&fe.ViewNode{Kind: fe.ViewKindLines, Lines: rows, List: list, Rows: 2, Width: 50}, "l", region{}, nil)
	if _, ok := n.Props["selected"]; ok {
		t.Fatalf("no selection = %#v", n.Props)
	}
	n = b.node(&fe.ViewNode{Kind: fe.ViewKindLines, Lines: rows[:1], List: list, Rows: 1, Width: 50}, "l", region{}, nil)
	if n.Kind != tern.KindAnsi {
		t.Fatalf("items that are not the rows = %#v", n)
	}
}

// A titled overlay's view is its frame around the component's: the card
// is the frame, so the title heads the body in strong text and the border
// rows and columns go.
func TestTitledOverlayFrameIsTheCard(t *testing.T) {
	border := func(lines ...string) fe.ViewNode {
		return fe.ViewNode{Kind: fe.ViewKindLines, Lines: lines, Rows: len(lines), Width: 12}
	}
	side := fe.ViewNode{Kind: fe.ViewKindLines, Lines: []string{"│"}, Rows: 1, Width: 1, Stack: &fe.ViewStack{Basis: 1, MaxSize: -1}}
	lines := []string{"┌─ Kit ────┐", "│ab        │", "└──────────┘"}
	view := &fe.View{Seq: 1, Root: fe.ViewNode{Kind: fe.ViewKindContainer, Rows: 3, Width: 12, Children: []fe.ViewNode{
		border("\x1b[2m┌─ \x1b[22mKit\x1b[2m ────┐\x1b[22m"),
		{Kind: fe.ViewKindHStack, Rows: 1, Width: 12, Align: "stretch", Children: []fe.ViewNode{
			side, {Kind: fe.ViewKindText, Text: "ab", Rows: 1, Width: 10, Stack: &fe.ViewStack{Basis: -1, Grow: 1, Shrink: 1, MaxSize: -1}}, side,
		}},
		border("└──────────┘"),
	}}}
	got := jsonOps(t, wireNode("v", (&session{}).viewElement("", "v", view, lines, "")))
	want := jsonOps(t, map[string]any{"id": "v", "k": "col", "p": map[string]any{"role": "pig.view"}, "c": []any{
		map[string]any{"id": "v.0", "k": "col", "c": []any{
			map[string]any{"id": "v.0.0", "k": "text", "p": map[string]any{"spans": []any{map[string]any{"t": "Kit", "s": "strong"}}}},
			map[string]any{"id": "v.0.1", "k": "text", "p": map[string]any{"text": "ab"}},
		}},
	}})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("titled overlay =\n%#v\nwant\n%#v", got, want)
	}
	// Without the frame's side columns it is an ordinary container.
	view.Root.Children[1].Children[0].Lines = []string{"x"}
	if got := jsonOps(t, wireNode("v", (&session{}).viewElement("", "v", view, lines, ""))); reflect.DeepEqual(got, want) {
		t.Fatal("a container that is not a frame lost its rows")
	}
}
