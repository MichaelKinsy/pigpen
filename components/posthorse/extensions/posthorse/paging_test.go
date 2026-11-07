package posthorse

import (
	"strconv"
	"strings"
	"sync"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func noToolTokens(t testing.TB) toolTokensFunc {
	return func(sdk.Context) (int, error) {
		t.Helper()
		t.Error("tool tokens must not be read")
		return 0, nil
	}
}

func TestReconcile(t *testing.T) {
	n := func(v int) *int { return &v }
	for _, tc := range []struct {
		name    string
		have    pageAccounting
		usage   *int
		pending int
	}{
		{"usage grew by 30", pageAccounting{pendingTokens: 100, usage: n(1000)}, n(1030), 70},
		{"usage grew more than pending", pageAccounting{pendingTokens: 10, usage: n(1000)}, n(1030), 0},
		{"usage shrank", pageAccounting{pendingTokens: 100, usage: n(1000)}, n(900), 100},
		{"no previous usage", pageAccounting{pendingTokens: 100}, n(900), 100},
		{"usage became unknown", pageAccounting{pendingTokens: 100, usage: n(1000)}, nil, 100},
	} {
		got := reconcile(tc.have, tc.usage)
		if got.pendingTokens != tc.pending {
			t.Errorf("%s: pending %d, want %d", tc.name, got.pendingTokens, tc.pending)
		}
		if (got.usage == nil) != (tc.usage == nil) || got.usage != nil && *got.usage != *tc.usage {
			t.Errorf("%s: usage not carried", tc.name)
		}
	}
}

func TestPageSize(t *testing.T) {
	p := budgetPolicy(100_000, 1000, 16_384, true)
	g := guidanceCost(t, p)
	capacity := 83_617 - 1000 - g
	pages := newPageReservations(noToolTokens(t))
	got, err := pages.size(p, pageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if want := min(maxPageChars, (capacity-pageMarginTokens)*4); got != want {
		t.Errorf("size = %d, want %d", got, want)
	}
	// Images take their estimate out of the page.
	got, _ = pages.size(p, pageRequest{imageCount: 2})
	if want := min(maxPageChars, (capacity-pageMarginTokens)*4-2*estimatedImageChars); got != want {
		t.Errorf("size with images = %d, want %d", got, want)
	}
}

func TestPageSizeRefusesBelowTheMinimumPage(t *testing.T) {
	probe := budgetPolicy(100_000, 0, 16_384, true)
	g := guidanceCost(t, probe)
	// 1,500 tokens of capacity leave 500 after the margin: 2,000 characters; 750 leave nothing usable.
	usage := 83_617 - g - 1_500
	p := budgetPolicy(100_000, usage, 16_384, true)
	pages := newPageReservations(noToolTokens(t))
	if got, err := pages.size(p, pageRequest{}); err != nil || got != 2000 {
		t.Fatalf("a 2,000-character page: %d, %v", got, err)
	}
	tight := budgetPolicy(100_000, usage+750, 16_384, true)
	pages = newPageReservations(noToolTokens(t))
	imageOffset := 3
	for _, tc := range []struct {
		request pageRequest
		want    string
	}{
		{pageRequest{offset: 5}, "Too little context remains to read a page safely. Call new_context first, then retry with offset 5."},
		{pageRequest{offset: 5, imageOffset: &imageOffset}, "Too little context remains to read a page safely. Call new_context first, then retry with offset 5 and imageOffset 3."},
		{pageRequest{offset: 5, cursor: "abc"}, "Too little context remains to read a page safely. Call new_context first, then retry with the same cursor."},
	} {
		pages.reset()
		_, err := pages.size(tight, tc.request)
		if err == nil || err.Error() != tc.want {
			t.Errorf("refusal: %v, want %q", err, tc.want)
		}
		if want := (u16len(tc.want) + 3) / 4; pages.accounting.pendingTokens != want {
			t.Errorf("a refusal reserves its own text: %d, want %d", pages.accounting.pendingTokens, want)
		}
	}
}

func TestRefusalOmitsGuidanceThatNoLongerFits(t *testing.T) {
	probe := budgetPolicy(100_000, 0, 16_384, true)
	g := guidanceCost(t, probe)
	exhausted := budgetPolicy(100_000, 83_617-g, 16_384, true) // capacity 0
	pages := newPageReservations(noToolTokens(t))
	_, err := pages.size(exhausted, pageRequest{offset: 1})
	if err == nil || err.Error() != "" {
		t.Fatalf("the refusal text is dropped when it no longer fits, got %v", err)
	}
	if pages.accounting.pendingTokens != 0 {
		t.Errorf("an empty refusal reserves nothing: %d", pages.accounting.pendingTokens)
	}
}

func TestPageResultReservesTextAndImages(t *testing.T) {
	// 4,000 tokens of capacity: a 12,000-character page, below the 40,000 cap.
	g := guidanceCost(t, budgetPolicy(100_000, 0, 16_384, true))
	p := budgetPolicy(100_000, 83_617-g-4_000, 16_384, true)
	pages := newPageReservations(noToolTokens(t))
	fresh, err := pages.size(p, pageRequest{})
	if err != nil || fresh != 12_000 {
		t.Fatalf("fresh page = %d, %v", fresh, err)
	}
	result := pages.result(strings.Repeat("a", 9), []imageLike{{data: "d", mimeType: "image/png"}}, displayKind("note-write"))
	if result.Content != strings.Repeat("a", 9) || len(result.Images) != 1 || result.Details == nil {
		t.Errorf("result = %+v", result)
	}
	reserved := 3 + 1200
	if pages.accounting.pendingTokens != reserved {
		t.Errorf("reservation = %d, want %d", pages.accounting.pendingTokens, reserved)
	}
	// The reservation shrinks the next page by exactly its tokens (4 characters each).
	next, err := pages.size(p, pageRequest{})
	if err != nil || next != fresh-4*reserved {
		t.Errorf("next page = %d, %v; want %d", next, err, fresh-4*reserved)
	}
	pages.reset()
	if pages.accounting != (pageAccounting{}) {
		t.Errorf("reset clears the accounting: %+v", pages.accounting)
	}
}

func TestPageErrorReservesTheMessage(t *testing.T) {
	p := budgetPolicy(100_000, 1000, 16_384, true)
	pages := newPageReservations(noToolTokens(t))
	err := pages.error(p, "Offset is past the end; restart with offset 0.", "")
	if err == nil || err.Error() != "Offset is past the end; restart with offset 0." {
		t.Fatalf("error = %v", err)
	}
	if want := (u16len(err.Error()) + 3) / 4; pages.accounting.pendingTokens != want {
		t.Errorf("reserved %d, want %d", pages.accounting.pendingTokens, want)
	}
}

func TestPageNotes(t *testing.T) {
	p := budgetPolicy(100_000, 1000, 16_384, true)
	t.Run("whole text, no footer", func(t *testing.T) {
		pages := newPageReservations(noToolTokens(t))
		result, err := pages.notes(p, notesPage{rows: []string{"a", "b", "c"}, kind: "notes-list", empty: "none", header: "H:\n"})
		if err != nil {
			t.Fatal(err)
		}
		if result.Content != "H:\na\nb\nc" {
			t.Errorf("content = %q", result.Content)
		}
		display, _ := result.Details.(*posthorseDisplay)
		if display == nil || display.kind != "notes-list" || display.count != 3 {
			t.Errorf("details = %+v", result.Details)
		}
	})
	t.Run("empty page prints the empty text", func(t *testing.T) {
		pages := newPageReservations(noToolTokens(t))
		result, _ := pages.notes(p, notesPage{kind: "notes-search", empty: "No notes match."})
		if result.Content != "No notes match." {
			t.Errorf("content = %q", result.Content)
		}
	})
	t.Run("row limit cuts the page and adds the footer", func(t *testing.T) {
		rows := make([]string, 150)
		for i := range rows {
			rows[i] = "r"
		}
		pages := newPageReservations(noToolTokens(t))
		result, err := pages.notes(p, notesPage{rows: rows, kind: "notes-list"})
		if err != nil {
			t.Fatal(err)
		}
		text := strings.Join(rows, "\n")
		want := text[:199] + "\n[chars 0-199 of 299; continue with offset 199]"
		if result.Content != want {
			t.Errorf("content = %q, want %q", result.Content, want)
		}
		display, _ := result.Details.(*posthorseDisplay)
		if display.count != 100 {
			t.Errorf("count = %v, want 100", display.count)
		}
	})
	t.Run("search pages 20 rows and says more matches follow", func(t *testing.T) {
		rows := make([]string, 30)
		for i := range rows {
			rows[i] = "match " + strconv.Itoa(i)
		}
		pages := newPageReservations(noToolTokens(t))
		result, _ := pages.notes(p, notesPage{rows: rows, kind: "notes-search", more: true})
		if !strings.HasSuffix(result.Content, "; more matches follow; continue with offset "+strconv.Itoa(u16len(strings.Join(rows[:20], "\n")))+"]") {
			t.Errorf("content tail = %q", result.Content[max(0, len(result.Content)-80):])
		}
	})
	t.Run("offset past the end", func(t *testing.T) {
		pages := newPageReservations(noToolTokens(t))
		_, err := pages.notes(p, notesPage{rows: []string{"a"}, kind: "notes-list", offset: 5})
		if err == nil || err.Error() != "Offset is past the end; restart with offset 0." {
			t.Errorf("error = %v", err)
		}
	})
	t.Run("a later page starts at its offset", func(t *testing.T) {
		pages := newPageReservations(noToolTokens(t))
		result, _ := pages.notes(p, notesPage{rows: []string{"abc", "def"}, kind: "notes-list", offset: 4})
		if result.Content != "def" {
			t.Errorf("content = %q", result.Content)
		}
	})
}

func TestNotesRange(t *testing.T) {
	rows := []string{"aa", "bb", "cc", "dd"}
	end, starts := notesRange(rows, 0, 11, 100)
	if end != 11 || len(starts) != 4 || starts[3] != 9 {
		t.Errorf("all rows: end %d starts %v", end, starts)
	}
	end, starts = notesRange(rows, 3, 11, 100) // starts after the offset only
	if end != 11 || len(starts) != 3 || starts[0] != 3 {
		t.Errorf("offset: end %d starts %v", end, starts)
	}
	end, starts = notesRange(rows, 0, 11, 2) // the row limit trims the end to the last full row
	if end != 5 || len(starts) != 2 {
		t.Errorf("limit: end %d starts %v", end, starts)
	}
	end, starts = notesRange(rows, 0, 4, 100) // stops once a row starts at the end
	if end != 4 || len(starts) != 2 {
		t.Errorf("short end: end %d starts %v", end, starts)
	}
	// Row lengths count UTF-16 units.
	_, starts = notesRange([]string{"😀", "x"}, 0, 99, 100)
	if len(starts) != 2 || starts[1] != 3 {
		t.Errorf("astral row: %v", starts)
	}
}

func TestPageReservationsAreAtomicUnderConcurrency(t *testing.T) {
	pages := newPageReservations(noToolTokens(t))
	var wg sync.WaitGroup
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pages.result("abcd", nil, nil)
		}()
	}
	wg.Wait()
	if pages.accounting.pendingTokens != 64 {
		t.Errorf("pending = %d, want 64", pages.accounting.pendingTokens)
	}
}

func BenchmarkPageSize(b *testing.B) {
	p := budgetPolicy(100_000, 1000, 16_384, true)
	pages := newPageReservations(func(sdk.Context) (int, error) { return 0, nil })
	b.ReportAllocs()
	for range b.N {
		pages.reset()
		_, _ = pages.size(p, pageRequest{})
	}
}

func BenchmarkPageNotes(b *testing.B) {
	p := budgetPolicy(100_000, 1000, 16_384, true)
	rows := make([]string, 400)
	for i := range rows {
		rows[i] = "notes/folder/file-" + strconv.Itoa(i) + ".md  1234B  2026-01-01T00:00Z"
	}
	pages := newPageReservations(func(sdk.Context) (int, error) { return 0, nil })
	b.ReportAllocs()
	for range b.N {
		pages.reset()
		_, _ = pages.notes(p, notesPage{rows: rows, kind: "notes-list", header: "Notes directory: /x\n"})
	}
}

func BenchmarkNotesRange(b *testing.B) {
	rows := make([]string, 400)
	for i := range rows {
		rows[i] = "notes/folder/file-" + strconv.Itoa(i) + ".md  1234B  2026-01-01T00:00Z"
	}
	b.ReportAllocs()
	for range b.N {
		_, _ = notesRange(rows, 0, 1<<20, 100)
	}
}
