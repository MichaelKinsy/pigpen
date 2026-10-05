package ui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/MichaelKinsy/pigpen/pig-music/music"
)

// pagingLib is a library whose liked songs come a page at a time, and which forgets them on Refresh.
type pagingLib struct {
	*libSource
	total     int
	pages     [][2]int
	refreshes int
	mu        sync.Mutex
	whole     int // Tracks calls: the screen must not need them
	hold      func(from int)
}

func (p *pagingLib) TracksPage(_ context.Context, id string, from, n int) ([]music.Track, bool, error) {
	if p.hold != nil {
		p.hold(from)
	}
	p.mu.Lock()
	p.pages = append(p.pages, [2]int{from, n})
	p.mu.Unlock()
	var out []music.Track
	for i := from; i < min(from+n, p.total); i++ {
		out = append(out, music.Track{ID: fmt.Sprintf("id%03d", i), Title: fmt.Sprintf("Liked %03d", i), Artists: []string{"A"}, Duration: 60e9})
	}
	return out, from+n < p.total, nil
}
func (p *pagingLib) Refresh() { p.refreshes++ }
func (p *pagingLib) Tracks(ctx context.Context, id string) ([]music.Track, error) {
	p.whole++
	return p.libSource.Tracks(ctx, id)
}

func pagingRig(t *testing.T, total int) (*rig, *pagingLib) {
	t.Helper()
	lib := newLib()
	lib.granted = true
	p := &pagingLib{libSource: lib, total: total}
	r := newRig(t, 100, 30)
	r.m = New(Deps{Source: p, Player: r.player})
	r.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	r.key("tab", "enter")
	return r, p
}

func TestALongCollectionShowsItsFirstPageAtOnceWithoutListingItAll(t *testing.T) {
	r, p := pagingRig(t, 566)
	if len(p.pages) != 1 || p.pages[0] != [2]int{0, libPage} || p.whole != 0 {
		t.Fatalf("pages %v, whole listings %d", p.pages, p.whole)
	}
	if !strings.Contains(r.text(), "Liked 000") {
		t.Errorf("the first page is not shown:\n%s", r.text())
	}
}

func TestTheNextPageIsFetchedWhenTheCursorNearsTheEndOfWhatIsLoaded(t *testing.T) {
	r, p := pagingRig(t, 566)
	for i := 0; i < libPage-libPageAhead-2; i++ {
		r.key("j")
	}
	if len(p.pages) != 1 {
		t.Fatalf("a page was fetched while the cursor was far from the end: %v", p.pages)
	}
	for i := 0; i < 4; i++ {
		r.key("j")
	}
	if len(p.pages) != 2 || p.pages[1] != [2]int{libPage, libPage} {
		t.Fatalf("pages %v", p.pages)
	}
	// holding the key does not start the same page twice
	for i := 0; i < 5; i++ {
		r.key("j")
	}
	if len(p.pages) != 2 {
		t.Errorf("pages %v", p.pages)
	}
}

func TestACollectionThatEndsStopsAskingForPages(t *testing.T) {
	r, p := pagingRig(t, 30)
	for i := 0; i < 40; i++ {
		r.key("j")
	}
	if len(p.pages) != 1 {
		t.Errorf("pages %v", p.pages)
	}
	if !strings.Contains(r.text(), "Liked 029") {
		t.Errorf("the last row is not shown:\n%s", r.text())
	}
}

func TestRForgetsWhatTheSourceRemembersAndListsAgain(t *testing.T) {
	r, p := pagingRig(t, 60)
	r.key("r")
	if p.refreshes != 1 || p.libCalls != 2 {
		t.Errorf("refreshes %d, library listings %d", p.refreshes, p.libCalls)
	}
}

func TestAFailedPageKeepsTheRowsAlreadyShown(t *testing.T) {
	r, p := pagingRig(t, 566)
	r.m, _ = r.m.Update(libTracksMsg{id: "LM", from: libPage, paged: true, err: fmt.Errorf("network down")})
	if !strings.Contains(r.text(), "Liked 000") {
		t.Errorf("the collection was closed by a failed page:\n%s", r.text())
	}
	_ = p
}

// Before M9c, enter on a collection queued all of it. With pages it queued the 50 rows loaded and said nothing, so the rest
// follows in the background: the player gets what is shown at once, then the other pages.
func TestEnterOnALongCollectionQueuesAllOfItNotJustTheLoadedPage(t *testing.T) {
	r, p := pagingRig(t, 566)
	r.key("enter")
	log := r.player.log()
	want := []string{"replace 50 tracks at 0 first=id000", "enqueue id050", "enqueue id250", "enqueue id450"}
	if strings.Join(log, "|") != strings.Join(want, "|") {
		t.Fatalf("player calls\n%v\nwant\n%v", log, want)
	}
	_ = p
}

func TestEnterOnACollectionThatIsAllLoadedQueuesJustThat(t *testing.T) {
	r, _ := pagingRig(t, 30)
	r.key("enter")
	if log := r.player.log(); len(log) != 1 || log[0] != "replace 30 tracks at 0 first=id000" {
		t.Fatalf("%v", log)
	}
}

// A later choice of what to play must not be added to by the pages of an earlier one.
func TestPagesOfAnEarlierChoiceAreNotAddedAfterALaterOne(t *testing.T) {
	r, p := pagingRig(t, 566)
	release := make(chan struct{})
	reached := make(chan struct{}, 1)
	held := false
	p.hold = func(from int) {
		if from == libPage && !held { // the first background page of the first enter
			held = true
			reached <- struct{}{}
			<-release
		}
	}
	m, cmd := r.m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	r.m = m.(Model)
	done := make(chan struct{})
	go func() { cmd(); close(done) }()
	select {
	case <-reached:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("the rest of the collection was never asked for")
	}
	r.key("enter") // the second choice: replaces the queue (the background page of this one is not held)
	close(release)
	<-done
	count := 0
	for _, l := range r.player.log() {
		if l == "enqueue id050" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("the first choice's pages were added after the second choice: %d x enqueue id050 in %v", count, r.player.log())
	}
}
