package frontend

import (
	"fmt"
	"math"
	"math/rand/v2"
	"reflect"
	"regexp"
	"strings"
	"testing"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

// wcag is WCAG 2.2's contrast ratio of two #rrggbb colors, computed apart
// from the member's tables.
func wcag(t *testing.T, a, b rgb) float64 {
	t.Helper()
	lum := func(c rgb) float64 {
		var l [3]float64
		for i, v := range c {
			s := float64(v) / 255
			if s <= 0.04045 {
				l[i] = s / 12.92
			} else {
				l[i] = math.Pow((s+0.055)/1.055, 2.4)
			}
		}
		return 0.2126*l[0] + 0.7152*l[1] + 0.0722*l[2]
	}
	la, lb := lum(a), lum(b)
	return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
}

func mustRGB(t *testing.T, hex string) rgb {
	t.Helper()
	c, ok := parseRGB(hex)
	if !ok {
		t.Fatalf("bad color %q", hex)
	}
	return c
}

// The ratio is WCAG's: black on white is 21:1, and #767676 is the lightest
// gray that reads 4.5:1 on white, #777777 just misses it.
func TestContrastIsWCAG(t *testing.T) {
	white := mustRGB(t, "#ffffff")
	for _, tc := range []struct {
		a    string
		want float64
	}{{"#000000", 21}, {"#767676", 4.54}, {"#777777", 4.48}, {"#ffffff", 1}} {
		c := mustRGB(t, tc.a)
		if got := contrast(c.luminance(), white.luminance()); math.Abs(got-tc.want) > 0.005 {
			t.Errorf("%s on white = %.3f, want %.2f", tc.a, got, tc.want)
		}
	}
	if got := mustRGB(t, "#0A1b2C").String(); got != "#0a1b2c" {
		t.Errorf("round trip = %s", got)
	}
}

// Every color reads after correction on each appearance's surfaces, at
// the body and the quiet ratio: it stays when it reads, and otherwise it
// is the first step toward black or white that reads, which keeps its hue
// (each channel moves the same way, in proportion). One step less does
// not read, and the other end reads no sooner (black wins a tie).
func TestReadableReadsAndKeepsHue(t *testing.T) {
	for ap, a := range appearances {
		var bgs []float64
		for _, base := range a.bases {
			bgs = append(bgs, base.luminance())
		}
		for _, ratio := range []float64{textContrast, quietContrast} {
			for r := 0; r < 256; r += 15 {
				for g := 0; g < 256; g += 15 {
					for b := 0; b < 256; b += 15 {
						c := rgb{uint8(r), uint8(g), uint8(b)}
						got := readable(c, bgs, ratio)
						reads := func(x rgb) bool {
							for _, base := range a.bases {
								if wcag(t, x, base) < ratio {
									return false
								}
							}
							return true
						}
						if !reads(got) {
							t.Fatalf("appearance %d ratio %.1f: %s -> %s does not read", ap, ratio, c, got)
						}
						if reads(c) {
							if got != c {
								t.Fatalf("appearance %d: readable %s moved to %s", ap, c, got)
							}
							continue
						}
						end := rgb{}
						if got[0] >= c[0] && got[1] >= c[1] && got[2] >= c[2] {
							end = rgb{0xff, 0xff, 0xff}
						}
						step := -1
						for i := 1; i < 256; i++ {
							if over(end, c, float64(i)/255) == got {
								step = i
								break
							}
						}
						if step < 0 {
							t.Fatalf("appearance %d: %s -> %s is not a step toward %s", ap, c, got, end)
						}
						if step > 1 && reads(over(end, c, float64(step-1)/255)) {
							t.Fatalf("appearance %d: %s -> %s overshoots", ap, c, got)
						}
						other, last := rgb{0xff, 0xff, 0xff}, step-1
						if end == other {
							other, last = rgb{}, step
						}
						for i := 1; i <= last; i++ {
							if reads(over(other, c, float64(i)/255)) {
								t.Fatalf("appearance %d: %s reads as soon toward %s (%d, %d steps)", ap, c, other, i, step)
							}
						}
					}
				}
			}
		}
	}
}

// Where no step reads on every background (a dark surface and a light
// tint), the color is black or white, whichever reads better.
func TestReadableFallsBackToTheBetterEnd(t *testing.T) {
	bgs := []float64{mustRGB(t, "#0b0b0c").luminance(), mustRGB(t, "#8c9094").luminance()}
	if got := readable(mustRGB(t, "#f0dd75"), bgs, textContrast); got != (rgb{0xff, 0xff, 0xff}) {
		t.Fatalf("got %s, want white", got)
	}
	bgs = []float64{mustRGB(t, "#ffffff").luminance(), mustRGB(t, "#5a5a5a").luminance()}
	if got := readable(mustRGB(t, "#f0dd75"), bgs, textContrast); got != (rgb{}) {
		t.Fatalf("got %s, want black", got)
	}
}

// pigMusic is pig-music's dark palette, which it sends as its views' theme
// overrides; pigLight and pigDark are PiG's light and dark palettes as the
// e2e runs sent them (the tokens a view's rules read).
var (
	pigMusic = map[string]string{"accent": "#f0dd75", "dim": "#9492a3", "error": "#ff6e6e", "muted": "#9492a3", "text": "#f8f8f8"}
	pigLight = fe.Theme{Name: "light", Colors: map[string]string{
		"accent": "#b705b7", "border": "#4286de", "borderAccent": "#cf31ce", "borderMuted": "#a3a3a3", "customMessageBg": "#f0e3ef",
		"dim": "#909090", "error": "#c73030", "muted": "#717171", "selectedBg": "#e2e8f1", "success": "#238821", "toolErrorBg": "#eee5e4",
		"toolPendingBg": "#e7e7e7", "toolSuccessBg": "#e1ebe0", "userMessageBg": "#e2e8f1", "warning": "#74770f",
	}}
	pigDark = fe.Theme{Name: "dark", Dark: true, Colors: map[string]string{
		"accent": "#d06fed", "border": "#00a2e6", "borderAccent": "#c867e5", "borderMuted": "#727987", "customMessageBg": "#3f1d49",
		"dim": "#7a818f", "error": "#f9686d", "muted": "#989eaa", "selectedBg": "#103145", "success": "#00bd63", "toolErrorBg": "#482020",
		"toolPendingBg": "#2c2e32", "toolSuccessBg": "#003919", "userMessageBg": "#103145", "warning": "#c29821",
	}}
)

// rule is the CSS rule of selector setting each token's variables to its
// value, given as token, value pairs.
func rule(selector string, pairs ...string) string {
	var decls []string
	for i := 0; i < len(pairs); i += 2 {
		for _, name := range themeVars[pairs[i]] {
			decls = append(decls, name+":"+pairs[i+1])
		}
	}
	return selector + "{" + strings.Join(decls, ";") + "}"
}

// marks is the selector of the marks and selected items under a view's
// root selector.
func marks(root string) string {
	return root + " .sf-t-mark," + root + " mark.sf-mark," + root + " .sf-item.sel"
}

// viewSheets returns the view sheets sent since the last call.
func (h *harness) viewSheets() []string {
	var css []string
	for _, m := range h.messages() {
		if m.verb == "s" && m.body["name"] == viewSheetName {
			text, _ := m.body["css"].(string)
			css = append(css, text)
		}
	}
	return css
}

// pig-music's dark palette reads on Tern's light and dark surfaces: on the
// light one its near-white text, pale yellow accent, light red error and
// pale muted and dim go dark enough to read 4.5:1 (3:1 for muted and dim)
// in their own hues, on the surface and, in the rule for its marks and
// selected items, on PiG's selection tint; on the dark one they stay as
// sent, but the error, a shade lighter to read on Tern's lighter overlay
// card. PiG's dark palette, after a switch to dark, recolors the marks:
// its selection tint is dark.
func TestPigMusicOverridesReadInLightAndDark(t *testing.T) {
	h := ready(t)
	light := pigLight
	view, lines := overlayView("tracks")
	view.Theme = pigMusic
	if err := h.session.Apply(fe.Frame{Theme: &light, Ops: []fe.Op{{Kind: fe.Insert, Region: fe.RegionOverlay, ID: "overlay.1", Node: fe.Overlay{Lines: lines, Width: 30, View: view}}}}); err != nil {
		t.Fatal(err)
	}
	root := `[data-role='pig.view.layer.overlay.1.view']`
	surface := `[data-role='pig.view.layer.overlay.1.view']{` +
		`--accent:light-dark(#776d3a,#f0dd75);--accent-ink:light-dark(#776d3a,#f0dd75);--sf-accent:light-dark(#776d3a,#f0dd75);--sf-c-accent:light-dark(#776d3a,#f0dd75);--sf-p-accent:light-dark(#776d3a,#f0dd75);` +
		`--sf-c-dim:light-dark(#8a8898,#9492a3);--sf-p-dim:light-dark(#8a8898,#9492a3);` +
		`--bad:light-dark(#b14c4c,#ff7171);--sf-bad:light-dark(#b14c4c,#ff7171);--sf-c-error:light-dark(#b14c4c,#ff7171);--sf-p-error:light-dark(#b14c4c,#ff7171);` +
		`--sf-muted:light-dark(#8a8898,#9492a3);--sf-c-muted:light-dark(#8a8898,#9492a3);--sf-p-muted:light-dark(#8a8898,#9492a3);` +
		`--sf-p-text:light-dark(#6c6c6c,#f8f8f8);color:light-dark(#6c6c6c,#f8f8f8)}`
	lightMarks := rule(marks(root), "accent", "light-dark(#736a38,#2c2916)", "dim", "light-dark(#878595,#44444b)", "error", "light-dark(#ad4b4b,#441d1d)",
		"muted", "light-dark(#878595,#44444b)", "text", "light-dark(#6a6a6a,#292929)")
	if got := h.viewSheets(); !reflect.DeepEqual(got, []string{viewSheetBase + surface + lightMarks}) {
		t.Fatalf("light sheets =\n%q\nwant\n%q", got, []string{viewSheetBase + surface + lightMarks})
	}
	// The light values read on the light surfaces and PiG's light
	// selection tint, the dark ones on the dark surfaces.
	for _, tc := range []struct {
		hex, bg string
		ratio   float64
	}{
		{"#776d3a", "#efefec", 4.5}, {"#6c6c6c", "#efefec", 4.5}, {"#b14c4c", "#efefec", 4.5}, {"#8a8898", "#efefec", 3},
		{"#736a38", "#e7ecf1", 4.5}, {"#6a6a6a", "#e7ecf1", 4.5}, {"#878595", "#e7ecf1", 3},
		{"#f0dd75", "#33363e", 4.5}, {"#f8f8f8", "#33363e", 4.5}, {"#ff7171", "#33363e", 4.5}, {"#9492a3", "#33363e", 3},
	} {
		if r := wcag(t, mustRGB(t, tc.hex), mustRGB(t, tc.bg)); r < tc.ratio {
			t.Errorf("%s on %s = %.2f", tc.hex, tc.bg, r)
		}
	}
	// The accent keeps its hue: each channel scales by one factor.
	if got := mustRGB(t, "#776d3a"); math.Abs(float64(got[0])/0xf0-float64(got[2])/0x75) > 0.01 {
		t.Errorf("accent %s left the hue of #f0dd75", got)
	}

	dark := pigDark
	if err := h.session.Apply(fe.Frame{Theme: &dark}); err != nil {
		t.Fatal(err)
	}
	darkMarks := rule(marks(root), "accent", "light-dark(#0f0e07,#f0dd75)", "dim", "light-dark(#313136,#9492a3)", "error", "light-dark(#170a0a,#ff6e6e)",
		"muted", "light-dark(#313136,#9492a3)", "text", "light-dark(#0d0d0d,#f8f8f8)")
	if got := h.viewSheets(); !reflect.DeepEqual(got, []string{viewSheetBase + surface + darkMarks}) {
		t.Fatalf("dark sheets =\n%q\nwant\n%q", got, []string{viewSheetBase + surface + darkMarks})
	}
}

// Overrides that read stay as sent: a quiet color that reads 3:1 on light
// and dark surfaces is one value, and the accent and text colors of a
// light palette keep their light values (their dark ones go light enough
// to read on the dark surfaces). Text keeps its light value on the marks
// too, which tint with the accent at 16%, where the muted gray goes a step
// darker.
func TestReadableOverridesStay(t *testing.T) {
	own := map[string]string{"muted": "#808080", "text": "#3a3a3a", "accent": "#1f5fa8"}
	got := themeRule("n.x", own, own, nil, nil)
	root := `[data-role='pig.view.n.x']`
	want := rule(root, "accent", "light-dark(#1f5fa8,#7ca2cc)", "muted", "#808080", "text", "light-dark(#3a3a3a,#9e9e9e)") +
		rule(marks(root), "accent", "light-dark(#1f5ea7,#a4bedc)", "muted", "light-dark(#797979,#989898)", "text", "light-dark(#3a3a3a,#bbbbbb)")
	if got != want {
		t.Fatalf("rule =\n%q\nwant\n%q", got, want)
	}
}

// A view that overrides the selection's fill also sets the text it
// inherits onto it: on a dark selection on Tern's light surface, PiG's
// palette colors and Tern's own text go light or dark enough to read
// (a token PiG's palette leaves out takes Tern's default, and only where
// that does not read).
func TestOverriddenFillCorrectsInheritedText(t *testing.T) {
	own := map[string]string{"selectedBg": "#303050"}
	got := themeRule("n.x", own, own, pigLight.Colors, nil)
	root := `[data-role='pig.view.n.x']`
	want := rule(root, "selectedBg", "rgb(from #303050 r g b/60%)") + rule(marks(root),
		"accent", "light-dark(#2a012a,#d878d8)", "border", "light-dark(#1a3558,#4286de)", "borderAccent", "light-dark(#5b165a,#d033cf)",
		"borderMuted", "light-dark(#343434,#a3a3a3)", "dim", "light-dark(#343434,#909090)", "error", "light-dark(#280a0a,#dd8282)",
		"muted", "light-dark(#343434,#7d7d7d)", "success", "light-dark(#061706,#65ac64)", "text", "light-dark(#131417,#e4e4e7)",
		"warning", "light-dark(#141503,#9da057)")
	if got != want {
		t.Fatalf("rule =\n%q\nwant\n%q", got, want)
	}
	// Without a palette, a gray selection: Tern's own text reads on it in
	// light and dark and stays Tern's; its other defaults go a step.
	own = map[string]string{"selectedBg": "#808080"}
	got = themeRule("n.x", own, own, nil, nil)
	want = rule(root, "selectedBg", "rgb(from #808080 r g b/60%)") + rule(marks(root),
		"accent", "light-dark(#1c3797,#cedeff)", "dim", "light-dark(#595a5d,#b6b6b9)", "error", "light-dark(#752527,#ffd3d4)",
		"muted", "light-dark(#595a61,#b6b6bb)", "success", "light-dark(#144a31,#adebcf)", "warning", "light-dark(#5b3a05,#fbd9a1)")
	if got != want {
		t.Fatalf("gray rule =\n%q\nwant\n%q", got, want)
	}
}

// A subtree that overrides the selection's fill corrects the text its view
// overrides, not Tern's: the view's near-white text goes dark on the
// subtree's selection in light.
func TestSubtreeFillReadsTheViewsOverrides(t *testing.T) {
	h := ready(t)
	view := &fe.View{Seq: 1, Theme: map[string]string{"text": "#f8f8f8"}, Root: fe.ViewNode{Kind: fe.ViewKindContainer, Rows: 1, Width: 20, Children: []fe.ViewNode{
		{Kind: fe.ViewKindText, Text: "track", Rows: 1, Width: 20, Theme: map[string]string{"selectedBg": "#303050"}},
	}}}
	h.apply(fe.Op{Kind: fe.Insert, Region: fe.RegionDock, ID: "w", Node: fe.Lines{Lines: []string{"track"}, View: view}})
	sub := `[data-role='pig.view.dock.w.0.0']`
	want := rule(sub, "selectedBg", "rgb(from #303050 r g b/60%)") + rule(marks(sub),
		"accent", "light-dark(#09112f,#9dbcff)", "dim", "light-dark(#e6e7e9,#7c7c81)", "error", "light-dark(#250c0c,#ff6b70)",
		"muted", "light-dark(#333438,#8a8a92)", "success", "light-dark(#06170f,#3ecf8e)", "text", "light-dark(#141414,#f8f8f8)",
		"warning", "light-dark(#1d1201,#f5a524)")
	if got := h.viewSheets(); len(got) != 1 || !strings.HasSuffix(got[0], want) {
		t.Fatalf("sheets =\n%q\nwant suffix\n%q", got, want)
	}
}

// A box's or text's card fill gets a rule of its own: the root's colors
// read on its tint, and an overridden custom-message fill also sets the
// text the root inherits onto it.
func TestCardFillsGetTheirRules(t *testing.T) {
	own := map[string]string{"accent": "#f0dd75", "customMessageBg": "#203040"}
	got := themeRule("n.x", own, own, pigLight.Colors, []string{"customMessageBg", "toolSuccessBg"})
	root := `[data-role='pig.view.n.x']`
	for _, part := range []string{
		rule(root, "accent", "light-dark(#776d3a,#f0dd75)", "customMessageBg", "rgb(from #203040 r g b/60%)"),
		rule(marks(root), "accent", "light-dark(#736a38,#2c2916)"),
		rule(root+` [data-role='pig.bg.customMessageBg']`, "accent", "light-dark(#100f08,#f0dd75)", "border", "light-dark(#193355,#4286de)"),
		rule(root+` [data-role='pig.bg.toolSuccessBg']`, "accent", "light-dark(#756b39,#2c2916)"),
	} {
		if !strings.Contains(got, strings.TrimSuffix(part, "}")) {
			t.Fatalf("rule =\n%q\nlacks\n%q", got, part)
		}
	}
}

// A transcript view's rule follows a new palette: its marks read on the
// new selection tint.
func TestThemeChangeRecolorsTranscriptViewRules(t *testing.T) {
	h := ready(t)
	light := pigLight
	view := &fe.View{Root: fe.ViewNode{Kind: fe.ViewKindText, Text: "x", Rows: 1, Width: 20}, Theme: pigMusic, Seq: 1}
	if err := h.session.Apply(fe.Frame{Theme: &light, Ops: []fe.Op{{Kind: fe.Insert, Region: fe.RegionMain, ID: "m", Node: fe.Lines{Lines: []string{"x"}, View: view}}}}); err != nil {
		t.Fatal(err)
	}
	if got := h.viewSheets(); len(got) != 1 || !strings.Contains(got[0], "--sf-p-text:light-dark(#6a6a6a,#292929)") {
		t.Fatalf("light sheets = %q", got)
	}
	dark := pigDark
	if err := h.session.Apply(fe.Frame{Theme: &dark}); err != nil {
		t.Fatal(err)
	}
	if got := h.viewSheets(); len(got) != 1 || !strings.Contains(got[0], "--sf-p-text:light-dark(#0d0d0d,#f8f8f8)") {
		t.Fatalf("dark sheets = %q", got)
	}
}

// Whatever a view overrides and PiG's palette holds, every color a rule
// sets reads on the backgrounds its rule's elements show, in light and in
// dark: the surfaces, the selection tint and the card tints.
func TestThemeRuleReadsOnEveryBackground(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	hex := func() string { return fmt.Sprintf("#%06x", rng.IntN(1<<24)) }
	decl := regexp.MustCompile(`([-a-zA-Z]+):(light-dark\((#[0-9a-f]{6}),(#[0-9a-f]{6})\)|#[0-9a-f]{6})`)
	ratioOf := map[string]float64{}
	for token, names := range themeVars {
		for _, name := range names {
			ratioOf[name] = minContrast(token)
		}
	}
	for range 300 {
		own := map[string]string{}
		for _, token := range []string{"accent", "text", "muted", "dim", "error", "selectedBg", "customMessageBg"} {
			if rng.IntN(2) == 0 {
				own[token] = hex()
			}
		}
		palette := map[string]string{"selectedBg": hex(), "toolErrorBg": hex(), "text": hex()}
		if rng.IntN(3) == 0 {
			delete(palette, "selectedBg")
		}
		css := themeRule("n", own, own, palette, []string{"toolErrorBg", "customMessageBg"})
		for _, r := range strings.Split(strings.TrimSuffix(css, "}"), "}") {
			selector, body, _ := strings.Cut(r, "{")
			fill := ""
			switch {
			case strings.Contains(selector, ".sf-t-mark"):
				fill = "selectedBg"
			case strings.Contains(selector, "pig.bg."):
				fill = selector[strings.Index(selector, "pig.bg.")+len("pig.bg.") : len(selector)-2]
			}
			found := decl.FindAllStringSubmatch(body, -1)
			var accent [2]string
			for _, m := range found {
				if m[1] == "--accent" {
					accent = [2]string{m[2], m[2]}
					if m[3] != "" {
						accent = [2]string{m[3], m[4]}
					}
				}
			}
			for _, m := range found {
				name, values := m[1], [2]string{m[2], m[2]}
				if m[3] != "" {
					values = [2]string{m[3], m[4]}
				}
				ratio, ok := ratioOf[name]
				if !ok || name == "--sf-sel" || name == "--sf-mark-bg" || name == "--sf-tint-custom" {
					continue
				}
				for i, a := range appearances {
					c := mustRGB(t, values[i])
					for _, bg := range backgrounds(t, a, own, palette, fill, accent[i]) {
						if r := wcag(t, c, bg); r < ratio && !isEnd(c) {
							t.Fatalf("own %v palette %v: %s %s=%s reads %.2f on %s (appearance %d)", own, palette, selector, name, c, r, bg, i)
						}
					}
				}
			}
		}
	}
}

// isEnd reports black or white, the fallback where nothing reads.
func isEnd(c rgb) bool { return c == rgb{} || c == rgb{0xff, 0xff, 0xff} }

// backgrounds are what a rule's element shows in appearance a, worked out
// apart from the member: the surfaces, or a fill's tint at 60% over them
// (the mark's accent at 16% without a selectedBg).
func backgrounds(t *testing.T, a appearance, own, palette map[string]string, fill, accent string) []rgb {
	t.Helper()
	if fill == "" {
		return a.bases
	}
	color, alpha := "", 0.6
	switch fill {
	case "selectedBg":
		color = own["selectedBg"]
		if color == "" {
			color = palette["selectedBg"]
		}
		if color == "" {
			// The mark tints with the accent this rule sets, else Tern's.
			alpha = 0.16
			color = a.accent.String()
			if accent != "" {
				color = accent
			}
		}
	case "customMessageBg":
		color = own["customMessageBg"]
		if color == "" {
			color = palette["customMessageBg"]
		}
	default:
		color = palette[fill]
	}
	if color == "" {
		return a.bases
	}
	c := mustRGB(t, color)
	var out []rgb
	for _, base := range a.bases {
		out = append(out, over(c, base, alpha))
	}
	return out
}
