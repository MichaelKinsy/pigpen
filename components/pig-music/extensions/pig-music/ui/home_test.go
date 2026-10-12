package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// feedSource is stubSource with the home's feeds and pages to open.
type feedSource struct {
	*stubSource
	popular    music.Section
	popErr     error
	suggested  []music.Section
	sugErr     error
	cachedPop  music.Section
	cachedSug  []music.Section
	askedPop   int
	askedSug   int
	askedCache int
}

func (f *feedSource) Popular(context.Context) (music.Section, error) {
	f.askedPop++
	return f.popular, f.popErr
}

func (f *feedSource) Suggested(context.Context) ([]music.Section, error) {
	f.askedSug++
	return f.suggested, f.sugErr
}

func (f *feedSource) CachedHome(context.Context) (music.Section, []music.Section, bool) {
	f.askedCache++
	return f.cachedPop, f.cachedSug, len(f.cachedPop.Tracks)+len(f.cachedSug) > 0
}

func (f *feedSource) Watch(context.Context, string) (music.Watch, error) { return music.Watch{}, nil }
func (f *feedSource) OpenPage(_ context.Context, e music.Entity) (music.Page, error) {
	return music.Page{Kind: e.Kind, ID: e.ID, Title: e.Title, Sections: []music.Section{{Title: "Tracks", Tracks: tracks(2)}}}, nil
}

// memRecents is a history in memory.
type memRecents struct {
	list    []music.Recent
	cleared int
}

func (r *memRecents) Load() ([]music.Recent, error) { return r.list, nil }
func (r *memRecents) Add(x music.Recent) ([]music.Recent, error) {
	out := []music.Recent{x}
	for _, o := range r.list {
		if !(o.Track == nil && x.Track == nil && o.Query == x.Query) && !(o.Track != nil && x.Track != nil && o.Track.ID == x.Track.ID) {
			out = append(out, o)
		}
	}
	r.list = out
	return out, nil
}
func (r *memRecents) Clear() error { r.list, r.cleared = nil, r.cleared+1; return nil }

func (r *memRecents) String() string {
	var out []string
	for _, x := range r.list {
		if x.Track != nil {
			out = append(out, "t:"+x.Track.ID)
		} else {
			out = append(out, "q:"+x.Query)
		}
	}
	return strings.Join(out, " ")
}

func chart(n int) music.Section {
	ts := make([]music.Track, n)
	for i := range ts {
		ts[i] = music.Track{ID: fmt.Sprintf("ch%02d", i), Title: fmt.Sprintf("Hit %d", i+1), Artists: []string{"Star"}, Duration: 200 * time.Second}
	}
	return music.Section{Title: "Top 100 Music Videos", Tracks: ts}
}

func suggestions() []music.Section {
	return []music.Section{
		{Title: "Quick picks", Tracks: []music.Track{{ID: "qp00", Title: "Pick One", Artists: []string{"Someone"}}}},
		{Title: "Mixed for you", Entities: []music.Entity{{Kind: "playlist", ID: "VLmix", Title: "Super Mix", Subtitle: "Mix"}}},
	}
}

// homeRig is a model with a history and feeds, shown at w x h: built hidden when hidden is set (as the extension builds it).
func homeRig(t *testing.T, w, h int, hidden bool) (*rig, *feedSource, *memRecents) {
	t.Helper()
	r := newRig(t, w, h)
	feed := &feedSource{stubSource: r.source, popular: chart(30), suggested: suggestions()}
	rec := &memRecents{list: []music.Recent{{Query: "night drive"}, {Track: &music.Track{ID: "old0", Title: "Old Song", Artists: []string{"Old Band"}}}}}
	m := New(Deps{Source: feed, Player: r.player, Recents: rec})
	if hidden {
		m = m.Hidden()
	}
	r.m = m
	r.send(tea.WindowSizeMsg{Width: w, Height: h})
	return r, feed, rec
}

func TestTheHomeLoadsWhenFirstSeenAndOnlyOnce(t *testing.T) {
	r, feed, _ := homeRig(t, 100, 40, true)
	r.state(playing(tracks(3), 0))
	if feed.askedPop+feed.askedSug+feed.askedCache != 0 {
		t.Fatalf("the home loaded while hidden: %d %d %d", feed.askedPop, feed.askedSug, feed.askedCache)
	}
	r.send(OpenedMsg{})
	r.key("down", "up")
	r.send(ClosedMsg{})
	r.send(OpenedMsg{})
	if feed.askedPop != 1 || feed.askedSug != 1 || feed.askedCache != 1 {
		t.Errorf("asked %d %d %d, want once each", feed.askedPop, feed.askedSug, feed.askedCache)
	}
	r.key("r")
	if feed.askedPop != 2 || feed.askedSug != 2 || feed.askedCache != 1 {
		t.Errorf("r: asked %d %d %d", feed.askedPop, feed.askedSug, feed.askedCache)
	}
}

func TestTheHomeIsRecentsSuggestedAndPopular(t *testing.T) {
	r, _, _ := homeRig(t, 100, 60, false)
	text := r.text()
	order := []string{"Press / to search", "Recents  C clear", "night drive", "Old Song", "Old Band", "Suggested  Quick picks", "Pick One", "Suggested  Mixed for you", "Super Mix", "Playlist · Mix", "Popular  Top 100 Music Videos", "1.   Hit 1", "Hit 20"}
	at := 0
	for _, want := range order {
		i := strings.Index(text[at:], want)
		if i < 0 {
			t.Fatalf("%q missing or out of order:\n%s", want, text)
		}
		at += i
	}
	if strings.Contains(text, "Hit 21") {
		t.Error("more of the chart than the home lists")
	}
	_, parts := r.model().Draw()
	lists := 0
	for _, p := range parts {
		if p.Kind == PartList {
			lists++
			if !p.List.shared {
				t.Error("a home list does not share the cursor")
			}
		}
	}
	if lists != 4 { // Recents, Quick picks, Mixed for you, Popular
		t.Errorf("%d lists, want 4", lists)
	}
}

func TestEnterOnAHomeRowDoesWhatTheRowIs(t *testing.T) {
	r, _, rec := homeRig(t, 100, 60, false)
	r.key("enter") // the search in the Recents
	if got := strings.Join(r.source.asked, " "); got != "night drive|20" {
		t.Fatalf("searched %q", got)
	}
	if rec.String() != "q:night drive t:old0" || r.model().searched != "night drive" {
		t.Errorf("history %s, searched %q", rec, r.model().searched)
	}

	r, _, rec = homeRig(t, 100, 60, false)
	r.key("down", "down", "down", "down", "down") // night drive, Old Song, Pick One, Super Mix, Hit 1, Hit 2
	r.key("enter")
	if got := strings.Join(r.player.log(), " "); got != "replace 30 tracks at 1 first=ch00" || r.model().tab != tabPlayer {
		t.Errorf("played %q on %s", got, r.model().tab)
	}
	if rec.String() != "t:ch01 q:night drive t:old0" {
		t.Errorf("history %s", rec)
	}

	r, _, _ = homeRig(t, 100, 60, false)
	r.key("down", "down", "down", "enter") // Super Mix opens as a page over the home
	if p := r.model().topPage(); p == nil || p.title != "Super Mix" {
		t.Fatalf("no page: %+v", p)
	}
	r.key("esc")
	if !strings.Contains(r.text(), "Recents") {
		t.Error("esc did not go back to the home")
	}

	r, _, _ = homeRig(t, 100, 60, false)
	r.key("down", "a") // add the Old Song to the queue
	if got := strings.Join(r.player.log(), " "); got != "enqueue old0" {
		t.Errorf("a: %q", got)
	}
}

func TestSearchingAndPlayingAreRemembered(t *testing.T) {
	r, _, rec := homeRig(t, 100, 40, false)
	r.key("/")
	r.typed("lofi")
	r.key("enter")
	r.key("down", "enter")
	if rec.String() != "t:id01 q:lofi q:night drive t:old0" {
		t.Errorf("history %s", rec)
	}
	r.key("tab", "backspace") // back on the Search screen, backspace leaves the results for the home, which lists them
	text := r.text()
	if r.model().searched != "" || !strings.Contains(text, "Song 2") || !strings.Contains(text, "lofi") {
		t.Errorf("not back on the home:\n%s", text)
	}
	r.key("down", "/")
	r.typed("x")
	r.key("enter", "/")
	for range 3 {
		r.key("backspace")
	}
	r.key("enter") // an empty search goes back to the home too
	if r.model().searched != "" || !strings.Contains(r.text(), "Recents  C clear") {
		t.Errorf("an empty search:\n%s", r.text())
	}
}

func TestTheRecentsAreClearedByCAndByAClickOnClear(t *testing.T) {
	for _, click := range []bool{false, true} {
		r, _, rec := homeRig(t, 100, 40, false)
		if click {
			r.clickText("C clear", 0, 1)
		} else {
			r.key("C")
		}
		text := r.text()
		if rec.cleared != 1 || rec.list != nil || strings.Contains(text, "Recents  C clear") || strings.Contains(text, "night drive") || !strings.Contains(text, "Recents cleared") {
			t.Errorf("click %v: cleared %d:\n%s", click, rec.cleared, text)
		}
		if !strings.Contains(text, "Popular") {
			t.Errorf("click %v: the feeds went too", click)
		}
	}
}

// TestAClickOnAHomeRowChoosesIt: one cursor runs through every section, so a click in another section than the cursor's
// chooses that row: a track plays (once, for a double click), a playlist opens, a recent search runs again.
func TestAClickOnAHomeRowChoosesIt(t *testing.T) {
	for _, w := range []int{50, 56, 100, 107} {
		r, _, _ := homeRig(t, w, 60, false)
		r.doubleClick("Hit 3", 0)
		if got := r.played(); got != "replace 30 tracks at 2 first=ch00" || r.model().home.cur != 6 || r.model().Tab() != tabPlayer {
			t.Fatalf("%d wide: a double click on Popular's Hit 3 played %q, cursor %d", w, got, r.model().home.cur)
		}
		r, _, _ = homeRig(t, w, 60, false)
		r.clickText("Old Song", 0, 1)
		if got := r.played(); got != "replace 1 tracks at 0 first=old0" || r.model().home.cur != 1 {
			t.Fatalf("%d wide: a click on a recent track played %q, cursor %d", w, got, r.model().home.cur)
		}
		r, _, _ = homeRig(t, w, 60, false)
		r.clickText("Pick One", 0, 1)
		if got := r.played(); got != "replace 1 tracks at 0 first=qp00" {
			t.Fatalf("%d wide: a click on a suggested track played %q", w, got)
		}
		r, _, _ = homeRig(t, w, 60, false)
		r.doubleClick("night drive", 0)
		if m := r.model(); m.searched != "night drive" || r.played() != "" {
			t.Fatalf("%d wide: a double click on a recent search: searched %q, player %q", w, m.searched, r.played())
		}
		r, _, _ = homeRig(t, w, 60, false)
		r.clickText("Super Mix", 0, 1)
		if m := r.model(); m.home.cur != 3 || m.topPage() == nil {
			t.Fatalf("%d wide: a click on a suggested playlist: cursor %d, page %v:\n%s", w, m.home.cur, m.topPage(), r.text())
		}
		r, _, _ = homeRig(t, w, 60, false)
		x, y := cellOf(t, r.screen(), "Pick One", 0)
		r.wheel(x, y, 1)
		r.wheel(x, y, 1)
		if r.model().home.cur != 2 {
			t.Errorf("%d wide: the wheel moved the cursor to %d", w, r.model().home.cur)
		}
	}
}

func TestOfflineTheHomeSaysSoAndKeepsWhatItHad(t *testing.T) {
	offline := errors.New("dial tcp: lookup music.youtube.com: no such host")
	r := newRig(t, 100, 40)
	feed := &feedSource{stubSource: r.source, popErr: offline, sugErr: offline, cachedPop: chart(3)}
	r.m = New(Deps{Source: feed, Player: r.player})
	r.send(tea.WindowSizeMsg{Width: 100, Height: 40})
	text := r.text()
	if !strings.Contains(text, "Hit 3") || !strings.Contains(text, "not updated: dial tcp") || !strings.Contains(text, "Could not load the suggestions: dial tcp") {
		t.Fatalf("offline with a cache:\n%s", text)
	}
	feed.cachedPop = music.Section{}
	r.m = New(Deps{Source: feed, Player: r.player})
	r.send(tea.WindowSizeMsg{Width: 100, Height: 40})
	if text := r.text(); !strings.Contains(text, "Could not load the chart: dial tcp") || !strings.Contains(text, "(r tries again)") {
		t.Fatalf("offline without a cache:\n%s", text)
	}
	feed.popErr, feed.popular = nil, chart(2)
	r.key("r")
	if text := r.text(); !strings.Contains(text, "Hit 2") || strings.Contains(text, "Could not load the chart") {
		t.Errorf("r did not load again:\n%s", text)
	}
}

func TestWithoutConsentTheSuggestionsAreAHintAndWithoutAnAccountNothing(t *testing.T) {
	r := newRig(t, 100, 40)
	feed := &feedSource{stubSource: r.source, popular: chart(2), sugErr: &music.NeedsConsentError{Browser: "chrome"}}
	r.m = New(Deps{Source: feed, Player: r.player})
	r.send(tea.WindowSizeMsg{Width: 100, Height: 40})
	if text := r.text(); !strings.Contains(text, "Allow your YouTube Music account on the Library tab") {
		t.Errorf("no hint:\n%s", text)
	}
	feed.sugErr, feed.suggested = nil, suggestions()
	r.send(grantedMsg{}) // the OK given on the Library tab; the home is seen again
	r.key("tab", "tab", "tab")
	if text := r.text(); feed.askedSug != 2 || !strings.Contains(text, "Pick One") {
		t.Errorf("the consent given, the suggestions were asked %d times:\n%s", feed.askedSug, text)
	}
	for _, err := range []error{music.ErrNoAccount, &music.NoBrowserError{Reason: "none"}} {
		feed.sugErr = err
		r.m = New(Deps{Source: feed, Player: r.player})
		r.send(tea.WindowSizeMsg{Width: 100, Height: 40})
		if text := r.text(); strings.Contains(text, "Suggested") || !strings.Contains(text, "Hit 2") {
			t.Errorf("%v:\n%s", err, text)
		}
	}
}

// TestTheHomeScrollsToKeepTheCursorInSight: the first row stays; the cursor's row is always on the screen.
func TestTheHomeScrollsToKeepTheCursorInSight(t *testing.T) {
	r, _, _ := homeRig(t, 80, 16, false)
	for i := range 23 {
		r.key("down")
		items := r.model().homeItems()
		want := items[r.model().home.cur]
		name := "night drive"
		switch {
		case want.track != nil:
			name = want.track.Title
		case want.ent != nil:
			name = want.ent.Title
		}
		lines := r.screen()
		if !strings.Contains(r.text(), name) || !strings.Contains(lines[3], "Press / to search") {
			t.Fatalf("after %d downs the cursor's row %q is not shown:\n%s", i+1, name, r.text())
		}
	}
	if r.model().home.cur != 23 {
		t.Errorf("the cursor stopped at %d", r.model().home.cur)
	}
}

// TestWithoutAHistoryOrFeedsTheSearchScreenIsAsItWas: a source with no feeds and no history keeps the bare hint.
func TestWithoutAHistoryOrFeedsTheSearchScreenIsAsItWas(t *testing.T) {
	r := newRig(t, 100, 30)
	if text := r.text(); !strings.Contains(text, "Then type a song or artist") || strings.Contains(text, "Recents") {
		t.Errorf("%s", text)
	}
	if r.model().homeActive() {
		t.Error("a home without anything to show")
	}
}

// TestAnAnswerOfAnEarlierLoadIsDropped: after r, what the load before it answers late does not replace the rows.
func TestAnAnswerOfAnEarlierLoadIsDropped(t *testing.T) {
	r, feed, _ := homeRig(t, 100, 40, false)
	old := r.model().home.gen
	feed.popular = chart(2)
	r.key("r")
	r.send(popularMsg{gen: old, sec: music.Section{Title: "Stale chart", Tracks: chart(5).Tracks}})
	r.send(popularMsg{gen: old, err: errors.New("late failure")})
	r.send(suggestedMsg{gen: old, err: &music.NeedsConsentError{}})
	r.send(homeCachedMsg{gen: old, popular: music.Section{Title: "Stale cache", Tracks: chart(4).Tracks}})
	text := r.text()
	if strings.Contains(text, "Stale") || strings.Contains(text, "late failure") || strings.Contains(text, "Hit 3") || !strings.Contains(text, "Pick One") {
		t.Errorf("an earlier load's answer changed the home:\n%s", text)
	}
}
