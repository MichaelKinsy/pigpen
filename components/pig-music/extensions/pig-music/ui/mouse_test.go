package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// The mouse tests find what they click on the screen as drawn (its stripped text), never through the layout code mouse.go
// uses, so a layout that drew a thing somewhere other than where the mouse looks for it fails here.

// cellOf is the cell where text first shows on lines at or below row from: its column (in cells) and row.
func cellOf(t *testing.T, lines []string, text string, from int) (x, y int) {
	t.Helper()
	for y := from; y < len(lines); y++ {
		plain := ansi.Strip(lines[y])
		if i := strings.Index(plain, text); i >= 0 {
			return ansi.StringWidth(plain[:i]), y
		}
	}
	t.Fatalf("%q is not on the screen:\n%s", text, ansi.Strip(strings.Join(lines, "\n")))
	return 0, 0
}

func (r *rig) click(x, y, clicks int) {
	r.t.Helper()
	r.send(MouseMsg{X: x, Y: y, Clicks: clicks})
}

func (r *rig) wheel(x, y, lines int) {
	r.t.Helper()
	r.send(MouseMsg{X: x, Y: y, Wheel: lines})
}

// clickText clicks the middle of the first place text shows at or below row from.
func (r *rig) clickText(text string, from, clicks int) {
	r.t.Helper()
	x, y := cellOf(r.t, r.screen(), text, from)
	r.click(x+ansi.StringWidth(text)/2, y, clicks)
}

// mouseWidths are the sizes every region is clicked at: three widths of the full layout, and the collapsed narrow one (one
// column on the Player screen, below 60 cells).
var mouseWidths = []int{80, 120, 200, 50}

func searched(t *testing.T, w, h int) *rig {
	t.Helper()
	r := newRig(t, w, h)
	r.key("/")
	r.typed("x")
	r.key("enter")
	return r
}

func TestClickingATabShowsItAndItsGapsDoNothing(t *testing.T) {
	for _, w := range mouseWidths {
		r := newRig(t, w, 24)
		for _, tab := range []string{"Player", "Library", "Search", "Library", "Player", "Search"} {
			x, y := cellOf(t, r.screen(), " "+tab+" ", 0)
			if y != 1 {
				t.Fatalf("%d wide: the tab bar is on row %d", w, y)
			}
			r.click(x, y, 1) // the tab's first cell
			if got := r.model().Tab(); got != strings.ToLower(tab) {
				t.Fatalf("%d wide: clicking %s shows %s", w, tab, got)
			}
			end := x + ansi.StringWidth(" "+tab+" ")
			r.click(end, y, 1) // the gap after it
			if got := r.model().Tab(); got != strings.ToLower(tab) {
				t.Fatalf("%d wide: the gap after %s shows %s", w, tab, got)
			}
		}
	}
}

func TestClickingATabWhileTypingLeavesThePromptAndKeepsTheQuery(t *testing.T) {
	r := newRig(t, 80, 24)
	r.key("/")
	r.typed("abc")
	r.clickText(" Player ", 0, 1)
	m := r.model()
	if m.Typing() || m.Tab() != tabPlayer || m.query != "abc" {
		t.Fatalf("typing %v tab %s query %q", m.Typing(), m.Tab(), m.query)
	}
}

// doubleClick clicks the middle of the first place text shows at or below row from twice, as a terminal counts a double
// click on one cell: 1, then 2.
func (r *rig) doubleClick(text string, from int) {
	r.t.Helper()
	x, y := cellOf(r.t, r.screen(), text, from)
	x += ansi.StringWidth(text) / 2
	r.click(x, y, 1)
	r.click(x, y, 2)
}

func (r *rig) played() string { return strings.Join(r.player.log(), ",") }

func TestAClickPlaysATrackAndADoubleClickPlaysItOnce(t *testing.T) {
	for _, w := range mouseWidths {
		r := searched(t, w, 24)
		r.clickText("Song 4", bodyTop, 1)
		if got := r.played(); got != "replace 5 tracks at 3 first=id00" || r.model().Tab() != tabPlayer {
			t.Fatalf("%d wide: a click: %q, tab %s", w, got, r.model().Tab())
		}
		r = searched(t, w, 24)
		x, y := cellOf(t, r.screen(), "Song 3", bodyTop)
		for clicks := 1; clicks <= 3; clicks++ { // a triple click: the first click chose, the others land on the Player screen
			r.click(x, y, clicks)
		}
		if got := r.played(); got != "replace 5 tracks at 2 first=id00" {
			t.Fatalf("%d wide: a double click: %q", w, got)
		}
		r = searched(t, w, 24) // the keys are as they were: the arrows move, enter plays
		r.key("down", "down")
		if r.model().resCur != 2 || r.played() != "" {
			t.Fatalf("%d wide: the arrows: cursor %d, %q", w, r.model().resCur, r.played())
		}
		r.key("enter")
		if got := r.played(); got != "replace 5 tracks at 2 first=id00" {
			t.Fatalf("%d wide: enter: %q", w, got)
		}
	}
}

// TestTheSecondClickOfADoubleClickDoesNothingWhereverItLands: the first click may scroll the list under the pointer, or play
// a track and show the Player, whose queue shows rows of its own where the second click lands.
func TestTheSecondClickOfADoubleClickDoesNothingWhereverItLands(t *testing.T) {
	r := newRig(t, 80, 14)
	r.state(playing(tracks(30), 0))
	r.send(ShowPlayerMsg{})
	x, y := cellOf(t, r.screen(), "Song 5", bodyTop)
	r.click(x, y, 1)
	if _, y2 := cellOf(t, r.screen(), "Song 5", bodyTop); y2 == y {
		t.Fatal("the queue did not scroll: this test needs it to")
	}
	r.click(x, y, 2)
	r.click(x, y, 3)
	if r.model().queueCur != 4 || r.played() != "jump 4" {
		t.Fatalf("the queue: cursor %d, %q", r.model().queueCur, r.played())
	}

	r = newRig(t, 80, 14)
	r.source.tracks = tracks(30)
	r.state(playing(tracks(30), 0))
	r.key("/")
	r.typed("x")
	r.key("enter")
	_, y = cellOf(t, r.screen(), "Song 5", bodyTop)
	x = 60 // the row's artist column, under the queue once the Player shows
	r.click(x, y, 1)
	if r.model().Tab() != tabPlayer {
		t.Fatalf("the click left the screen on %s", r.model().Tab())
	}
	if !r.onListRow(x, y) {
		t.Fatalf("the second click lands on no row of the Player screen: this test needs it to:\n%s", r.text())
	}
	r.click(x, y, 2)
	if got := r.played(); got != "replace 30 tracks at 4 first=id00" {
		t.Fatalf("the results: %q", got)
	}
}

// onListRow is whether cell (x, y) of the screen shows a row of a list.
func (r *rig) onListRow(x, y int) bool {
	_, parts := r.model().drawn(true)
	p, _, row, ok := partAt(parts, x, y)
	return ok && p.Kind == PartList && p.List != nil && row < len(p.List.Items)
}

func TestAClickOnAnUpNextRowJumpsToItsTrack(t *testing.T) {
	for _, w := range mouseWidths {
		r := newRig(t, w, 24)
		r.state(playing(tracks(6), 0))
		r.send(ShowPlayerMsg{})
		r.clickText("Song 4", bodyTop, 1)
		if r.model().queueCur != 3 || r.played() != "jump 3" {
			t.Fatalf("%d wide: queue cursor %d, player %q", w, r.model().queueCur, r.played())
		}
		r.doubleClick("Song 5", bodyTop)
		if got := r.played(); got != "jump 3,jump 4" {
			t.Fatalf("%d wide: a double click: %q", w, got)
		}
		if w >= 60 { // the now-playing panel beside the queue takes no clicks, nor does the divider; the queue's first cell does
			_, y := cellOf(t, r.screen(), "Song 2", bodyTop)
			r.click(1, y, 1)
			div, _ := cellOf(t, r.screen(), "│", y)
			r.click(div, y, 1)
			if r.model().queueCur != 4 || len(r.player.log()) != 2 {
				t.Fatalf("%d wide: the left panel or the divider moved the queue: %d %v", w, r.model().queueCur, r.player.log())
			}
			r.click(div+1, y, 1)
			if r.model().queueCur != 1 || r.played() != "jump 3,jump 4,jump 1" {
				t.Fatalf("%d wide: the queue's first cell: %d %q", w, r.model().queueCur, r.played())
			}
		}
	}
}

func TestPartAtCountsTheCellFromItsPartAndColumn(t *testing.T) {
	list := &List{Items: []ListItem{{Label: "a"}, {Label: "b"}}}
	stage := Part{Kind: PartStage, Rows: 3, Columns: []Column{
		{Width: 10, Parts: []Part{{Kind: PartText, Rows: 3}}},
		{Width: 1, Parts: []Part{{Kind: PartRows, Rows: 3}}},
		{Width: 20, Parts: []Part{{Kind: PartText, Rows: 1}, {Kind: PartList, Rows: 2, List: list}}},
	}}
	parts := []Part{{Kind: PartText, Rows: 2}, stage, {Kind: PartBar, Rows: 1}}
	for _, c := range []struct {
		x, y, wantX, wantY int
		kind               PartKind
		ok                 bool
	}{
		{3, 1, 3, 1, PartText, true},
		{9, 2, 9, 0, PartText, true},
		{10, 2, 0, 0, PartRows, true},
		{11, 4, 0, 1, PartList, true},
		{30, 3, 19, 0, PartList, true},
		{31, 3, 0, 0, 0, false},
		{7, 5, 7, 0, PartBar, true},
		{7, 6, 0, 0, 0, false},
	} {
		p, x, y, ok := partAt(parts, c.x, c.y)
		if ok != c.ok || (ok && (p.Kind != c.kind || x != c.wantX || y != c.wantY)) {
			t.Errorf("(%d,%d): kind %d at (%d,%d) %v", c.x, c.y, p.Kind, x, y, ok)
		}
	}
}

func TestTheQueueRailBesideTheLyricsTakesClicks(t *testing.T) {
	for _, w := range []int{160, 200} {
		r := stageRig(t, w, 36)
		x, y := cellOf(t, r.screen(), "Song 3", bodyTop)
		if x < w/2 {
			t.Fatalf("%d wide: Song 3 at %d is not on the rail", w, x)
		}
		r.click(x, y, 1)
		if r.model().queueCur != 2 || r.played() != "jump 2" {
			t.Fatalf("%d wide: a click: cursor %d, %q", w, r.model().queueCur, r.played())
		}
		r.click(x, y, 2)
		if r.played() != "jump 2" {
			t.Fatalf("%d wide: a double click: %q", w, r.played())
		}
	}
}

func TestTheWheelMovesAListsCursorARowAtATimeOnlyOverTheList(t *testing.T) {
	for _, w := range mouseWidths {
		r := searched(t, w, 24)
		x, y := cellOf(t, r.screen(), "Song 2", bodyTop)
		r.wheel(x, y, 3)
		r.wheel(x, y, 1)
		if r.model().resCur != 2 {
			t.Fatalf("%d wide: cursor %d after two turns down", w, r.model().resCur)
		}
		r.wheel(x, y, -5)
		if r.model().resCur != 1 {
			t.Fatalf("%d wide: cursor %d after a turn up", w, r.model().resCur)
		}
		for _, row := range []int{0, tabRow, bodyTop, len(r.screen()) - 1} { // the header, the tabs, the prompt, the keys
			r.wheel(x, row, 1)
		}
		if r.model().resCur != 1 {
			t.Fatalf("%d wide: the wheel off the list moved it to %d", w, r.model().resCur)
		}
	}
}

// barOf is the play bar's row and its bar's first cell and width, read off the screen: the run of rule characters after the
// position.
func barOf(t *testing.T, lines []string) (y, start, width int) {
	t.Helper()
	for y, l := range lines {
		plain := []rune(ansi.Strip(l))
		for i, r := range plain {
			if r != '━' && r != '─' || i == 0 || plain[i-1] != ' ' {
				continue
			}
			n := 0
			for i+n < len(plain) && (plain[i+n] == '━' || plain[i+n] == '─') {
				n++
			}
			if n < len(plain)-1 { // not the rule across the screen
				return y, i, n
			}
			break
		}
	}
	t.Fatal("no play bar")
	return 0, 0, 0
}

func TestClickingThePlayBarSeeksToTheMiddleOfTheCell(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 30}, {200, 40}, {50, 24}, {80, 4}} { // the last is the strip
		r := newRig(t, size[0], size[1])
		st := playing(tracks(3), 0) // 0:50 of 2:56
		r.state(st)
		y, start, width := barOf(t, r.screen())
		for _, c := range []int{0, width / 3, width - 1} {
			r.click(start+c, y, 1)
			r.state(st) // the stub reports no new position
			want := time.Duration(int64(st.Duration)*int64(2*c+1)/int64(2*width)) - st.Position
			log := r.player.log()
			if got := log[len(log)-1]; got != "seek "+want.String() {
				t.Fatalf("%v cell %d of %d: %s, want seek %s", size, c, width, got, want)
			}
		}
		n := len(r.player.log())
		for _, x := range []int{0, start - 2, start - 1, start + width, size[0] - 1} { // the icon, the clocks, the margins
			r.click(x, y, 1)
		}
		if got := r.player.log(); len(got) != n {
			t.Fatalf("%v: clicks beside the bar seeked: %v", size, got[n:])
		}
	}
}

func TestSeekPointIsTheMiddleOfTheCellsShare(t *testing.T) {
	for _, c := range []struct {
		cell, width int
		dur, want   time.Duration
	}{
		{0, 10, 100 * time.Second, 5 * time.Second},
		{9, 10, 100 * time.Second, 95 * time.Second},
		{4, 10, 100 * time.Second, 45 * time.Second},
		{0, 4, 2 * time.Second, 250 * time.Millisecond},
		{2, 3, 90 * time.Second, 75 * time.Second},
	} {
		if got := seekPoint(c.cell, c.width, c.dur); got != c.want {
			t.Errorf("cell %d of %d of %s: %s, want %s", c.cell, c.width, c.dur, got, c.want)
		}
	}
}

func TestNothingToSeekWithoutALength(t *testing.T) {
	r := newRig(t, 80, 24)
	st := playing(tracks(3), 0)
	st.Duration = 0
	r.state(st)
	y, start, _ := barOf(t, r.screen())
	r.click(start+2, y, 1)
	if got := r.player.log(); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

func TestClickingASettingSelectsAndTogglesIt(t *testing.T) {
	f := newFake()
	r := settingsRig(t, f)
	r.key("o")
	r.clickText("Show the track", bodyTop, 1)
	if r.model().settings.cur != 1 || strings.Join(f.sets, ",") != "footerStatus=off" {
		t.Fatalf("cursor %d, saved %v", r.model().settings.cur, f.sets)
	}
	r.clickText("Stop the music", bodyTop, 1)
	r.clickText("Stop the music", bodyTop, 2) // every click is a toggle, as in Pi's settings list
	if r.model().settings.cur != 0 || strings.Join(f.sets, ",") != "footerStatus=off,stopOnExit=off,stopOnExit=on" {
		t.Fatalf("cursor %d, saved %v", r.model().settings.cur, f.sets)
	}
	r.clickText("mpv 0.37.0", bodyTop, 1) // the info is text
	x, y := cellOf(t, r.screen(), "Stop the music", bodyTop)
	r.wheel(x, y, 1)
	if r.model().settings.cur != 1 || len(f.sets) != 3 {
		t.Fatalf("cursor %d, saved %v", r.model().settings.cur, f.sets)
	}
}

func TestClickingAnAlbumsOrArtistsTabShowsThatKind(t *testing.T) {
	r, src := entRig(t)
	for _, c := range []struct{ label, kind string }{{"Albums 2", "albums"}, {"Artists 1", "artists"}, {"Songs 3", "songs"}} {
		r.clickText(c.label, bodyTop, 1)
		if r.model().entKind != c.kind {
			t.Fatalf("clicking %s shows %q", c.label, r.model().entKind)
		}
	}
	r.clickText("Albums 2", bodyTop, 1)
	r.clickText("Discovery", bodyTop, 1) // a click opens the album
	if m := r.model(); m.entCur != 1 || m.topPage() == nil || len(m.pages) != 1 || len(src.opened) != 1 {
		t.Fatalf("cursor %d, pages %d, opened %v", m.entCur, len(m.pages), src.opened)
	}
	r, src = entRig(t)
	r.clickText("Albums 2", bodyTop, 1)
	r.doubleClick("Discovery", bodyTop) // the second click lands on the album's page: nothing more opens or plays
	if m := r.model(); len(m.pages) != 1 || len(src.opened) != 1 || r.played() != "" {
		t.Fatalf("a double click: pages %d, opened %v, player %q", len(m.pages), src.opened, r.played())
	}
	r, src = entRig(t)
	r.clickText("Artists 1", bodyTop, 1)
	r.clickText("80.8M", bodyTop, 1) // the artist's second line is its row too
	if len(src.opened) != 1 || src.opened[0].Title != "Daft Punk" {
		t.Fatalf("the artist: opened %v", src.opened)
	}
}

// TestAClickOnTheLibraryOpensACollectionAndPlaysATrack: the second click of a double click on a collection lands on the
// opened collection's tracks, and plays none.
func TestAClickOnTheLibraryOpensACollectionAndPlaysATrack(t *testing.T) {
	for _, w := range mouseWidths {
		src := newLib()
		src.granted = true
		r := libRig(t, src, w, 24)
		r.key("tab")
		r.doubleClick("Road trip", bodyTop)
		if l := r.model().lib; l.open == nil || l.open.Title != "Road trip" || r.played() != "" {
			t.Fatalf("%d wide: a double click on Road trip: open %v, player %q:\n%s", w, l.open, r.played(), r.text())
		}
		r.key("backspace")
		r.clickText("Liked songs", bodyTop, 1)
		r.clickText("Liked two", bodyTop, 1)
		if got := r.played(); got != "replace 2 tracks at 1 first=aaaaaaaaaaa" || r.model().Tab() != tabPlayer {
			t.Fatalf("%d wide: a click on Liked two: %q, tab %s", w, got, r.model().Tab())
		}
	}
}

// TestAClickOnAPageOpensAnEntityAndPlaysATrack: an artist's page has songs, albums and artists; a click plays or opens one.
func TestAClickOnAPageOpensAnEntityAndPlaysATrack(t *testing.T) {
	for _, w := range mouseWidths {
		r, _ := navRig(t)
		r.send(tea.WindowSizeMsg{Width: w, Height: 30})
		r.key("A")
		r.clickText("Fans might also like", bodyTop, 1) // a heading: the cursor never rests there
		if len(r.model().pages) != 1 || r.model().topPage().cur != 1 || r.played() != "" {
			t.Fatalf("%d wide: a click on a heading: %d pages, cursor %d, player %q", w, len(r.model().pages), r.model().topPage().cur, r.played())
		}
		_, albums := cellOf(t, r.screen(), "Albums", bodyTop)
		r.doubleClick("Random Access Memories", albums+1)
		if p := r.model().topPage(); p == nil || len(r.model().pages) != 2 || r.played() != "" {
			t.Fatalf("%d wide: a double click on the album: %d pages, player %q:\n%s", w, len(r.model().pages), r.played(), r.text())
		}
		r.clickText("Giorgio by Moroder", bodyTop, 1)
		if got := r.played(); got != "replace 3 tracks at 2 first=alb0001" || r.model().Tab() != tabPlayer {
			t.Fatalf("%d wide: a click on a song of the album: %q", w, got)
		}
		r, _ = navRig(t) // from the last row up past two headings
		r.send(tea.WindowSizeMsg{Width: w, Height: 30})
		r.key("A", "down", "down", "down")
		r.clickText("Instant Crush", bodyTop, 1)
		if got := r.played(); got != "replace 2 tracks at 1 first=top0001" {
			t.Fatalf("%d wide: a click on a song above the cursor: %q", w, got)
		}
	}
}

func TestEventsOutsideTheRegionsChangeNothing(t *testing.T) {
	for _, w := range mouseWidths {
		r := searched(t, w, 24)
		r.key("down")
		before, cur := r.text(), r.model().resCur
		h := len(r.screen())
		_, head := cellOf(t, r.screen(), "Title", bodyTop) // the table's heading
		for _, c := range [][2]int{{1, 0}, {w - 3, 0}, {1, 2}, {1, bodyTop}, {1, head}, {1, h - 2}, {1, h - 1}, {-1, 5}, {w, 5}, {3, h}, {3, -1}} {
			for _, clicks := range []int{1, 2} {
				r.click(c[0], c[1], clicks)
			}
			r.wheel(c[0], c[1], 1)
		}
		r.send(MouseMsg{X: 3, Y: 5}) // neither a click nor the wheel
		if r.text() != before || r.model().resCur != cur || len(r.player.log()) != 0 {
			t.Fatalf("%d wide: changed:\n%s\nplayer %v", w, r.text(), r.player.log())
		}
	}
}

func TestTheHelpAndTheTooSmallScreenTakeNoClicks(t *testing.T) {
	r := newRig(t, 80, 24)
	r.key("?")
	before := r.text()
	for y := range 24 {
		r.click(5, y, 1)
	}
	if r.text() != before || !r.model().help {
		t.Fatal("a click changed the help")
	}
	r = newRig(t, 20, 5)
	r.state(playing(tracks(3), 0))
	for y := range 5 {
		for x := range 20 {
			r.click(x, y, 1)
		}
	}
	if len(r.player.log()) != 0 {
		t.Fatalf("%v", r.player.log())
	}
}

// ── quick settings ──────────────────────────────────────────────────────────

func (r *quickRig) click(text string, dx int) {
	r.t.Helper()
	x, y := cellOf(r.t, strings.Split(r.m.View().Content, "\n"), text, 0)
	r.send(MouseMsg{X: x + dx, Y: y, Clicks: 1})
}

func TestQuickSettingsRowsAreSelectedAndChangedByAClick(t *testing.T) {
	r := newQuickRig(t, quickPlaying())
	r.click("Calm", 0)
	if r.m.(QuickModel).row != rowCalm || len(r.port.sets) != 1 || r.port.sets[0] != [2]string{"calm", "true"} {
		t.Fatalf("row %d, sets %v", r.m.(QuickModel).row, r.port.sets)
	}
	r.click("Engine", 0)
	if r.m.(QuickModel).row != rowEngine || r.port.sets[1][0] != "engine" || r.port.sets[1][1] == "auto" {
		t.Fatalf("row %d, sets %v", r.m.(QuickModel).row, r.port.sets)
	}
	r.click("< 40 >", 0) // the "<"
	r.click("< 40 >", 5) // the ">"
	r.click("< 40 >", 2) // the number
	if got := strings.Join(r.player.log(), ","); got != "volume 35,volume 45,volume 45" {
		t.Fatalf("%s", got)
	}
	n := len(r.port.sets) + len(r.player.log())
	r.click("quick settings", 0)
	r.click("up/down choose", 0)
	if len(r.port.sets)+len(r.player.log()) != n || r.m.(QuickModel).row != rowVolume {
		t.Fatalf("a click off the rows acted: %v %v", r.port.sets, r.player.log())
	}
}

func TestQuickSettingsWheelMovesTheSelection(t *testing.T) {
	r := newQuickRig(t, quickPlaying())
	x, y := cellOf(t, strings.Split(r.m.View().Content, "\n"), "Shuffle", 0)
	r.send(MouseMsg{X: x, Y: y, Wheel: 2})
	r.send(MouseMsg{X: x, Y: y, Wheel: 1})
	r.send(MouseMsg{X: x, Y: y, Wheel: -1})
	if r.m.(QuickModel).row != 1 {
		t.Fatalf("row %d", r.m.(QuickModel).row)
	}
	r.send(MouseMsg{X: x, Y: 0, Wheel: 1}) // the heading
	if r.m.(QuickModel).row != 1 || len(r.port.sets) != 0 || len(r.player.log()) != 0 {
		t.Fatalf("row %d %v %v", r.m.(QuickModel).row, r.port.sets, r.player.log())
	}
}
