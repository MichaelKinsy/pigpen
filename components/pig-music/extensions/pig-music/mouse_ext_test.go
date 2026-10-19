package pig_music_test

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// cellIn is the cell where text first shows in a snapshot's rows: its column and row, -1 when it does not show.
func cellIn(s snapshot, text string) (x, y int) {
	for y, l := range s.Lines {
		plain := ansi.Strip(l)
		if i := strings.Index(plain, text); i >= 0 {
			return ansi.StringWidth(plain[:i]), y
		}
	}
	return -1, -1
}

// gesture sends what Pi sends for a left click whose press the component took: press, release, then click with its count.
func gesture(h *overlayHost, x, y, count int) {
	for _, typ := range []string{"press", "release"} {
		h.mouse(map[string]any{"type": typ, "button": "left", "x": x, "y": y, "screenX": x, "screenY": y, "width": 100, "height": 30})
	}
	h.mouse(map[string]any{"type": "click", "button": "left", "x": x, "y": y, "screenX": x, "screenY": y, "width": 100, "height": 30, "clickCount": count})
}

func TestTheMouseDrivesThePlayerThroughTheSDK(t *testing.T) {
	h, srv := player(t)
	done := h.command("music")
	h.waitOpen()
	first := h.waitSnapshot("the search screen", hasText("Press / to search"))
	if !first.Mouse {
		t.Fatal("the player's frames do not say it takes the mouse")
	}
	for _, in := range []string{"/", "x", "\r"} {
		h.input(in)
	}
	results := h.waitSnapshot("results", hasText("Song 5"))
	x, y := cellIn(results, "Song 3")
	for _, typ := range []string{"move", "drag"} { // hovering and dragging change nothing
		h.mouse(map[string]any{"type": typ, "button": "none", "x": x, "y": y, "width": 100, "height": 30})
	}
	h.mouse(map[string]any{"type": "wheel", "button": "none", "x": x, "y": y, "width": 100, "height": 30, "wheelDelta": 1})
	gesture(h, x, y, 1)
	gesture(h, x, y, 2)
	h.waitSnapshot("the player screen", func(s snapshot) bool {
		return strings.Contains(screenText(s), "Up Next") && strings.Contains(screenText(s), "Playing")
	})
	waitFor(t, "mpv playing the third result", func() bool {
		urls, pos := srv.Playlist()
		return len(urls) == 5 && pos == 2
	})
	player := h.waitSnapshot("the player screen", hasText("Up Next"))
	tx, ty := cellIn(player, " Search ")
	gesture(h, tx, ty, 1)
	h.waitSnapshot("the search screen again", hasText(`Results for "x"`))
	h.input("q")
	if failure := <-done; failure != "" {
		t.Fatal(failure)
	}
}

// entitySource is fixedSource with an album and an artist for every search.
type entitySource struct{ fixedSource }

func (entitySource) SearchEntities(context.Context, string, int) ([]music.Entity, error) {
	return []music.Entity{{Kind: "album", ID: "MPREb_1", Title: "Discovery LP"}, {Kind: "artist", ID: "UC1", Title: "Night Driver"}}, nil
}

func (entitySource) OpenEntity(context.Context, music.Entity) (string, []music.Track, error) {
	return "", nil, nil
}

// TestATernClickOnEachTabShowsIt: Tern sends a click on a node of the view as a click at the node's first cell. The tab
// bar is a node per tab, so a click there shows that tab: every tab, from each of the others; and so is the Songs, Albums,
// Artists line of a search. The cell is found as a frontend finds it, in the view the extension sent (the row of tabs' node
// holding the tab, at its column in the row).
func TestATernClickOnEachTabShowsIt(t *testing.T) {
	h, _, _, _ := playerEnvOn(t, entitySource{fixedSource{songs(5)}}, nil, true)
	done := h.command("music")
	h.waitOpen()
	s := h.waitSnapshot("the search screen", func(s snapshot) bool { return s.View != nil && shownTab(t, s) == "Search" })
	// Every ordered pair of tabs: from Search to Player, Player to Library, ... back to Search.
	for _, to := range []string{"Player", "Library", "Search", "Library", "Player", "Search"} {
		from := shownTab(t, s)
		x, y := tabCell(t, s, 0, to)
		gesture(h, x, y, 1)
		s = h.waitSnapshot("the "+to+" tab after a click on its first cell ("+from+" shown before)", func(s snapshot) bool {
			return s.View != nil && shownTab(t, s) == to
		})
	}
	for _, in := range []string{"/", "x", "\r"} {
		h.input(in)
	}
	s = h.waitSnapshot("the kinds of result", func(s snapshot) bool {
		_, nodes, _ := tabRowOf(t, s, 1)
		return len(nodes) > 0 && strings.Contains(screenText(s), "Song 3")
	})
	shows := map[string]func(snapshot) bool{
		"Songs": func(s snapshot) bool { return hasText("Song 3")(s) && !hasText("Discovery LP")(s) },
		"Albums": func(s snapshot) bool {
			return hasText("Discovery LP")(s) && !hasText("Song 3")(s)
		},
		"Artists": func(s snapshot) bool {
			return hasText("Night Driver")(s) && !hasText("Song 3")(s) && !hasText("Discovery LP")(s)
		},
	}
	from := "Songs"
	for _, to := range []string{"Albums", "Artists", "Songs", "Artists", "Albums", "Songs"} {
		x, y := tabCell(t, s, 1, to)
		gesture(h, x, y, 1)
		s = h.waitSnapshot(to+" after a click on its first cell ("+from+" shown before)", func(s snapshot) bool {
			return s.View != nil && shows[to](s)
		})
		from = to
	}
	h.input("q")
	if failure := <-done; failure != "" {
		t.Fatal(failure)
	}
}

// tabRowOf is the row of a view drawn by its nth row of tabs (0 the tab bar, 1 the kinds of search result), and that row's
// nodes with their first cells; no nodes when there is no such row.
func tabRowOf(t *testing.T, s snapshot, nth int) (row int, nodes []wireNode, cols []int) {
	t.Helper()
	v := decodeView(t, s.View)
	for i, mark := range stackedRows(v.Root, s.Width) {
		if mark != "tabs" {
			continue
		}
		if nth == 0 {
			nodes, cols = tabNodes(v.Root, s.Width, i)
			return i, nodes, cols
		}
		nth--
	}
	return 0, nil, nil
}

// tabCell is the first cell of the node of the nth row of tabs that holds name: where Tern sends a click on it.
func tabCell(t *testing.T, s snapshot, nth int, name string) (x, y int) {
	t.Helper()
	row, nodes, cols := tabRowOf(t, s, nth)
	for i, n := range nodes {
		if strings.Contains(ansi.Strip(n.Text), name) {
			return cols[i], row
		}
	}
	t.Fatalf("no node of row of tabs %d holds %q", nth, name)
	return 0, 0
}

// shownTab is the tab whose node is drawn reversed, read from the node on its own (it carries the styles it starts with).
func shownTab(t *testing.T, s snapshot) string {
	t.Helper()
	_, nodes, _ := tabRowOf(t, s, 0)
	for _, n := range nodes {
		name := strings.TrimSpace(ansi.Strip(n.Text))
		if name == "" {
			continue
		}
		cells, err := cellsOf(n.Text)
		if err != nil {
			t.Fatal(err)
		}
		if len(cells) > 1 && cells[1].style.reverse {
			return name
		}
	}
	return ""
}
