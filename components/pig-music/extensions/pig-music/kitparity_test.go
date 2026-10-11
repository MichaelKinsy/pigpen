package pig_music_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
	"github.com/charmbracelet/x/ansi"

	pig_music "github.com/MichaelKinsy/pigpen/pig-music"
	"github.com/MichaelKinsy/pigpen/pig-music/internal/screens"
	"github.com/MichaelKinsy/pigpen/pig-music/music"
	"github.com/MichaelKinsy/pigpen/pig-music/ui"
)

// staticView is a ViewComponent that shows one view and closes on any key.
type staticView struct{ v kit.View }

func (s staticView) View(int) kit.View { return s.v }
func (staticView) HandleInput(string) (sdk.RemoteComponentResult, error) {
	return sdk.RemoteComponentResult{Done: true}, nil
}

// wireOf sends view through the SDK to a fake host of width columns, with or without a frontend, and returns the view frame
// the host received: the SDK's own encoding, with the image bytes a first frame carries.
func wireOf(t *testing.T, view kit.View, width, height int, frontend bool) json.RawMessage {
	t.Helper()
	ext := sdk.New("pig-music")
	ext.RegisterCommand("show", sdk.CommandOptions{Description: "show a view", Handler: func(ctx sdk.Context, _ string) error {
		_, err := ctx.Custom(staticView{view}, nil)
		return err
	}})
	h := startHost(t, ext, "tui", width, height, frontend)
	done := h.command("show")
	h.waitOpen()
	snap := h.waitSnapshot("the view frame", func(s snapshot) bool { return s.View != nil })
	h.input("q")
	if failure := <-done; failure != "" {
		t.Fatalf("show: %s", failure)
	}
	if snap.Width != width {
		t.Fatalf("frame width %d, want %d", snap.Width, width)
	}
	return snap.View
}

// fixture is one screen's kit view as the SDK sent it, and the rows the terminal showed before the kit (View's rows).
type fixture struct {
	Name  string          `json:"name"`
	Width int             `json:"width"`
	View  json.RawMessage `json:"view"`
	Rows  []string        `json:"rows"`
	// Stacked is, for each row, the hstack that draws it: "stage" (the Player screen's columns), "tabs" (a row of tabs) or
	// "" (none). The host joins an hstack's nodes with resets of its own: those rows keep their cells, not their bytes.
	Stacked []string `json:"stacked"`
}

// tabsStack reports whether n is a row of tabs: an hstack of text nodes.
func tabsStack(n wireNode) bool {
	if n.Kind != "hstack" || len(n.Children) == 0 {
		return false
	}
	for _, c := range n.Children {
		if c.Kind != "truncated-text" {
			return false
		}
	}
	return true
}

// stackedRows marks the rows a view's hstacks cover (fixture.Stacked).
func stackedRows(n wireNode, width int) []string {
	var out []string
	var walk func(n wireNode)
	walk = func(n wireNode) {
		mark := ""
		switch {
		case n.Kind == "container":
			for _, c := range n.Children {
				walk(c)
			}
			return
		case tabsStack(n):
			mark = "tabs"
		case n.Kind == "hstack":
			mark = "stage"
		}
		rows, _ := renderNode(n, width)
		for range rows {
			out = append(out, mark)
		}
	}
	walk(n)
	return out
}

// tabNodes is each node of the row of tabs at row of a view, with the cell it starts at: what a frontend's click on the node
// names (its first cell). None when that row is not a row of tabs.
func tabNodes(n wireNode, width, row int) (nodes []wireNode, cols []int) {
	top := 0
	var walk func(n wireNode) bool
	walk = func(n wireNode) bool {
		if n.Kind == "container" {
			for _, c := range n.Children {
				if walk(c) {
					return true
				}
			}
			return false
		}
		rows, _ := renderNode(n, width)
		if row >= top+len(rows) {
			top += len(rows)
			return false
		}
		if tabsStack(n) {
			col := 0
			for _, c := range n.Children {
				nodes, cols = append(nodes, c), append(cols, col)
				col += *c.Stack.Basis
			}
		}
		return true
	}
	walk(n)
	return nodes, cols
}

// TestKitViewsReproduceTheScreens: every screen's kit view, sent through the SDK, draws the screen's own rows: byte for byte,
// except the Player screen's columns, which keep every cell (character and attributes). The view carries the structure a
// frontend draws (the cover's file, the bar, the lists, the sung line, the palette) only while a frontend is attached.
func TestKitViewsReproduceTheScreens(t *testing.T) {
	var fixtures []fixture
	for _, s := range screens.All() {
		rows := strings.Split(s.Model.View().Content, "\n")
		view := pig_music.ScreenView(s.Model, s.W)
		for _, frontend := range []bool{true, false} {
			raw := wireOf(t, view, s.W, s.H, frontend)
			v := decodeView(t, raw)
			got := mustRenderView(t, raw, s.W)
			stacked := stackedRows(v.Root, s.W)
			if len(got) != len(rows) || len(stacked) != len(rows) {
				t.Fatalf("%s: the view draws %d rows (%d placed), the screen has %d", s.Name, len(got), len(stacked), len(rows))
			}
			for i := range rows {
				if got[i] == rows[i] {
					continue
				}
				if same, why := sameCells(got[i], rows[i]); !same || stacked[i] == "" {
					t.Fatalf("%s: row %d differs (drawn by an hstack: %q; %s):\n got %q\nwant %q", s.Name, i, stacked[i], why, got[i], rows[i])
				}
			}
			checkStructure(t, s, v, frontend)
			if frontend {
				fixtures = append(fixtures, fixture{Name: s.Name, Width: s.W, View: raw, Rows: rows, Stacked: stacked})
			}
		}
	}
	if dir := os.Getenv("PIG_MUSIC_KITPARITY_DIR"); dir != "" {
		writeJSON(t, filepath.Join(dir, "fixtures.json"), fixtures)
		var runes []rune
		for _, g := range pig_music.AgreedRunes() {
			for r := g[0]; r <= g[1]; r++ {
				runes = append(runes, r)
			}
		}
		writeJSON(t, filepath.Join(dir, "runes.json"), runes)
	}
}

func checkStructure(t *testing.T, s screens.Screen, v wireView, frontend bool) {
	t.Helper()
	kinds := map[string]int{}
	var images, progress, lists, tabRows int
	walkNodes(v.Root, func(n wireNode) {
		kinds[n.Kind]++
		if tabsStack(n) {
			tabRows++
			for _, c := range n.Children { // Tern drops a node of spaces alone, and the gap it held between two tabs with it
				if strings.TrimLeft(ansi.Strip(c.Text), " ") == "" {
					t.Errorf("%s: a row of tabs has a node of spaces alone (%d cells) in %q", s.Name, *c.Stack.Basis, nodeTexts(n))
				}
			}
		}
		if n.Image != nil {
			images++
		}
		if n.Progress != nil {
			progress++
			want := 50.0
			if strings.Contains(s.Name, "end") {
				want = 208
			}
			if p := n.Progress; p.Value != want || p.Max != 214 {
				t.Errorf("%s: progress %v of %v, want %v of 214", s.Name, p.Value, p.Max, want)
			}
		}
		if n.List != nil {
			lists++
			if len(n.List.Items) != len(n.Content) || n.List.SelectedIndex < -1 || n.List.SelectedIndex >= len(n.List.Items) {
				t.Errorf("%s: a list of %d items (selected %d) on %d rows", s.Name, len(n.List.Items), n.List.SelectedIndex, len(n.Content))
			}
		}
	})
	full := s.H >= 6 // the full layout, with its tab bar; the strip has none
	if full {
		checkTabBar(t, s, v)
	}
	want := 0
	if full {
		want = 1
	}
	switch {
	case strings.HasPrefix(s.Name, "search-kinds"): // and the Songs, Albums, Artists line
		want++
	case strings.HasPrefix(s.Name, "search-home"): // and the Recents' heading with its clear key
		want++
	case strings.HasPrefix(s.Name, "player") && strings.Contains(s.Name, "lyrics") && s.W < 132 && full: // Lyrics, Up Next
		want++
	}
	if tabRows != want {
		t.Errorf("%s: %d rows of tabs, want %d", s.Name, tabRows, want)
	}
	if !frontend {
		if images+progress+lists+len(v.Images) != 0 {
			t.Errorf("%s: frontend-only annotations without a frontend: %d images, %d bars, %d lists, %d image files", s.Name, images, progress, lists, len(v.Images))
		}
		return
	}
	// a stage is an hstack from 50 cells: the columns from 80, the track's cover and text over the panel below that. The emoji
	// scene's queue column holds a character PiG may measure otherwise: no hstack for the columns that hold it.
	emoji := strings.Contains(s.Name, "emoji") && s.W >= 80
	player := strings.HasPrefix(s.Name, "player") && s.W >= 50 && s.H >= 6 && !emoji
	withCover := player && !strings.Contains(s.Name, "plain")
	if kinds["truncated-text"] == 0 {
		t.Errorf("%s: no text node", s.Name)
	}
	if player != (kinds["hstack"]-tabRows == 1) {
		t.Errorf("%s: %d hstacks (%d of tabs)", s.Name, kinds["hstack"], tabRows)
	}
	if withCover != (images == 1) {
		t.Errorf("%s: %d covers", s.Name, images)
	}
	if withCover {
		sum := sha256.Sum256(screens.CoverPNG)
		if len(v.Images) != 1 || v.Images[0].Ref != hex.EncodeToString(sum[:]) || v.Images[0].MimeType != "image/png" || string(v.Images[0].Data) != string(screens.CoverPNG) {
			t.Errorf("%s: the frame does not carry the cover's file", s.Name)
		}
	}
	idle := strings.HasPrefix(s.Name, "search-idle")
	if idle == (progress == 1) {
		t.Errorf("%s: %d bars", s.Name, progress)
	}
	if strings.HasPrefix(s.Name, "search-results") && lists != 1 {
		t.Errorf("%s: %d lists", s.Name, lists)
	}
	if strings.HasPrefix(s.Name, "search-home") && lists < 2 { // a list per section of the home that the rows reach
		t.Errorf("%s: %d lists", s.Name, lists)
	}
	vibes := !idle && !strings.Contains(s.Name, "plain") && !strings.Contains(s.Name, "nocolour")
	if vibes != (v.Theme != nil) || vibes && (len(v.Theme["accent"]) != 7 || v.Theme["muted"] == "") {
		t.Errorf("%s: theme %v", s.Name, v.Theme)
	}
}

// tabBarNodes are what the nodes of the tab bar hold, left to right, before the rest of the row that ends the last: each tab
// with the gap after it, the first with the margin before it.
var tabBarNodes = []string{"  Search  ", " Library  ", " Player "}

// nodeTexts is the text of each node of n, stripped.
func nodeTexts(n wireNode) []string {
	var out []string
	for _, c := range n.Children {
		out = append(out, ansi.Strip(c.Text))
	}
	return out
}

// checkTabBar: the tab bar (the screen's second row) is a node per tab, each holding that tab's label and the spaces around
// it (the margin, the gap after it, the rest of the row), so no node is spaces alone.
func checkTabBar(t *testing.T, s screens.Screen, v wireView) {
	t.Helper()
	nodes, _ := tabNodes(v.Root, s.W, 1)
	var got []string
	for _, n := range nodes {
		got = append(got, ansi.Strip(n.Text))
	}
	want := append([]string(nil), tabBarNodes...)
	want[len(want)-1] += strings.Repeat(" ", s.W-len(strings.Join(tabBarNodes, "")))
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("%s: the tab bar's nodes hold %q, want %q", s.Name, got, want)
	}
}

// TestKitParityHostRendered compares what PiG's own renderer drew of every fixture (kitparity/run.sh) with the screens' rows,
// and PiG's width of every character a text node may hold with this extension's.
func TestKitParityHostRendered(t *testing.T) {
	file := os.Getenv("PIG_MUSIC_KITPARITY_RENDERED")
	if file == "" {
		t.Skip("PIG_MUSIC_KITPARITY_RENDERED names the host's renderings (kitparity/run.sh)")
	}
	var fixtures []fixture
	readJSON(t, filepath.Join(filepath.Dir(file), "fixtures.json"), &fixtures)
	var rendered struct {
		Rows   map[string][]string `json:"rows"`
		Widths map[string]int      `json:"widths"`
	}
	readJSON(t, file, &rendered)
	bytesSame, cellsSame := 0, 0
	type tabRows struct{ screens, perTab, bytesSame, cellsSame, differ int }
	tabs := map[int]*tabRows{}
	for _, f := range fixtures {
		got, ok := rendered.Rows[f.Name]
		if !ok || len(got) != len(f.Rows) {
			t.Fatalf("%s: the host drew %d rows, the screen has %d", f.Name, len(got), len(f.Rows))
		}
		tr := tabs[f.Width]
		if tr == nil {
			tr = &tabRows{}
			tabs[f.Width] = tr
		}
		if !strings.HasPrefix(f.Name, "strip") {
			tr.screens++
			if f.Stacked[1] == "tabs" {
				tr.perTab++
			}
		}
		for i := range f.Rows {
			same, why := got[i] == f.Rows[i], ""
			if same {
				bytesSame++
			} else if same, why = sameCells(got[i], f.Rows[i]); same && f.Stacked[i] != "" {
				cellsSame++
			}
			if f.Stacked[i] == "tabs" {
				switch {
				case got[i] == f.Rows[i]:
					tr.bytesSame++
				case same:
					tr.cellsSame++
				default:
					tr.differ++
				}
			}
			if !same || got[i] != f.Rows[i] && f.Stacked[i] == "" {
				t.Errorf("%s: row %d (drawn by an hstack: %q): %s\n host %q\n want %q", f.Name, i, f.Stacked[i], why, got[i], f.Rows[i])
			}
		}
	}
	for _, w := range screens.Widths {
		tr := tabs[w]
		if tr == nil {
			t.Fatalf("no screen %d cells wide", w)
		}
		t.Logf("rows of tabs at %d cells: the tab bar a node per tab on %d of %d screens; %d rows of tabs (tab bars, kinds lines) cell-identical (%d of them byte-identical), %d differ; single-row fallback: %v",
			w, tr.perTab, tr.screens, tr.cellsSame+tr.bytesSame, tr.bytesSame, tr.differ, tr.perTab < tr.screens)
	}
	for key, w := range rendered.Widths {
		var r rune
		if _, err := fmt.Sscanf(key, "%d", &r); err != nil {
			t.Fatal(err)
		}
		if mine := ansi.StringWidth(string(r)); mine != w {
			t.Errorf("U+%04X: PiG measures %d cells, pig-music %d", r, w, mine)
		}
	}
	t.Logf("%d screens: %d rows byte-identical, %d rows of hstacks (stage, tabs) cell-identical; %d characters measured alike", len(fixtures), bytesSame, cellsSame, len(rendered.Widths))
}

// TestTheTabBarIsANodePerTabWhereItFits: at each width the view is drawn, the tab bar is a node per tab when its tabs fit the
// row (28 cells: the margin, three tabs and the gaps between them), and the one text row it was otherwise; either way the
// view draws the screen's rows.
func TestTheTabBarIsANodePerTabWhereItFits(t *testing.T) {
	for _, w := range append([]int{24, 26, 27, 28, 29}, screens.Widths...) {
		var m tea.Model = ui.New(ui.Deps{Source: fixedSource{songs(3)}})
		m, _ = m.Update(tea.WindowSizeMsg{Width: w, Height: 20})
		raw := wireOf(t, pig_music.ScreenView(m, w), w, 20, true)
		v := decodeView(t, raw)
		stacked := stackedRows(v.Root, w)
		rows := strings.Split(m.View().Content, "\n")
		got := mustRenderView(t, raw, w)
		if len(got) != len(rows) || len(stacked) != len(rows) {
			t.Fatalf("%d cells: the view draws %d rows (%d placed), the screen has %d", w, len(got), len(stacked), len(rows))
		}
		for i := range rows {
			if same, why := sameCells(got[i], rows[i]); !same || got[i] != rows[i] && stacked[i] == "" {
				t.Fatalf("%d cells: row %d differs (%s):\n got %q\nwant %q", w, i, why, got[i], rows[i])
			}
		}
		if perTab := stacked[1] == "tabs"; perTab != (w >= 28) {
			t.Errorf("%d cells: the tab bar a node per tab: %v", w, perTab)
		}
	}
}

// TestTheWidgetDrawsAsAStringListWithItsBarForAFrontend: the widget view is Text(line, 1, 0), as PiG lays out a string-list
// widget, and a bar of no rows that only a frontend sees.
func TestTheWidgetDrawsAsAStringListWithItsBarForAFrontend(t *testing.T) {
	st := music.State{Connected: true, Position: 30 * time.Second, Duration: 120 * time.Second}
	for _, frontend := range []bool{true, false} {
		raw := wireOf(t, pig_music.WidgetView("| Song - Artist 0:30/2:00", st, map[string]string{"accent": "#123456"}), 80, 24, frontend)
		v := decodeView(t, raw)
		if lines := mustWidgetLines(t, raw); len(lines) != 1 || lines[0] != "| Song - Artist 0:30/2:00" {
			t.Fatalf("widget text %q", lines)
		}
		var bars int
		walkNodes(v.Root, func(n wireNode) {
			if n.Kind == "lines" && len(n.Content) == 0 && n.Progress != nil && n.Progress.Value == 30 && n.Progress.Max == 120 {
				bars++
			}
		})
		if frontend != (bars == 1) || v.Theme["accent"] != "#123456" {
			t.Fatalf("frontend %v: %d bars, theme %v", frontend, bars, v.Theme)
		}
	}
}

func writeJSON(t *testing.T, file string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readJSON(t *testing.T, file string, v any) {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatal(err)
	}
}
