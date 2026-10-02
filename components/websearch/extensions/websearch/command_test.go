package websearch

import (
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Go tests for the parts of extension.go that need no host, and for the /search command
// (index.ts:3618; upstream has no test for it beyond the registration gate).

type fakeUI struct {
	notes   []string
	selects []string // titles asked
	answers []string // choices to give; "" = dismiss
	options [][]string
}

func (u *fakeUI) Notify(msg, level string) { u.notes = append(u.notes, level+": "+msg) }
func (u *fakeUI) Select(title string, options []string) (string, bool) {
	u.selects = append(u.selects, title)
	u.options = append(u.options, options)
	if len(u.answers) == 0 {
		return "", false
	}
	a := u.answers[0]
	u.answers = u.answers[1:]
	return a, a != ""
}

func storedFixtures(t *testing.T) (searchID, fetchID string) {
	t.Helper()
	ClearResults()
	t.Cleanup(ClearResults)
	now := nowMs()
	StoreResult("s1abcdef", &StoredSearchData{ID: "s1abcdef", Type: "search", Timestamp: now - 5*60000,
		Queries: []QueryResultData{{Query: "go generics", Results: []SearchResult{{Title: "a", URL: "https://a"}}}, {Query: "second"}}})
	errText := "boom"
	StoreResult("f2abcdef", &StoredSearchData{ID: "f2abcdef", Type: "fetch", Timestamp: now - 125*60000,
		URLs: []ExtractedContent{{URL: "https://example.com/" + strings.Repeat("x", 80), Content: "12345"}, {URL: "https://b", Error: &errText}}})
	return "s1abcdef", "f2abcdef"
}

func TestSearchCommandEmpty(t *testing.T) {
	newRuntime(t, "")
	ui := &fakeUI{}
	(&Runtime{}).SearchCommand(ui)
	if len(ui.notes) != 1 || ui.notes[0] != "info: No stored search results" || len(ui.selects) != 0 {
		t.Fatalf("%+v", ui)
	}
}

func TestSearchCommandBrowseViewDelete(t *testing.T) {
	r, _ := newRuntime(t, "")
	s, f := storedFixtures(t)
	StoreResult("h3abcdef", &StoredSearchData{ID: "h3abcdef", Type: "research", Timestamp: nowMs() - 60*60000})
	list := func() []string { ui := &fakeUI{}; r.SearchCommand(ui); return ui.options[0] }
	opts := list()
	joined := strings.Join(opts, "\n")
	if !strings.Contains(joined, "[h3abcd] research - 1h ago") {
		t.Fatalf("an age of exactly 60 minutes reads as hours: %q", opts)
	}
	if !strings.Contains(joined, `[s1abcd] "go generics" (2 queries) - 5m ago`) || !strings.Contains(joined, "[f2abcd] 2 URLs fetched - 2h ago") {
		t.Fatalf("%q", opts)
	}
	// View details of the fetch result.
	ui := &fakeUI{answers: []string{"[f2abcd] 2 URLs fetched - 2h ago", "View details"}}
	r.SearchCommand(ui)
	if len(ui.notes) != 1 || !strings.HasPrefix(ui.notes[0], "info: ID: "+f+"\nType: fetch\nAge: 125m\n\nURLs:\n- https://example.com/") ||
		!strings.Contains(ui.notes[0], "... (5 chars)") || !strings.Contains(ui.notes[0], "- https://b (boom)") {
		t.Fatalf("%q", ui.notes)
	}
	if ui.selects[1] != "Result f2abcd" {
		t.Fatal(ui.selects)
	}
	// Delete the search result.
	ui = &fakeUI{answers: []string{opts[0], "Delete"}}
	r.SearchCommand(ui)
	if ui.notes[0] != "info: Deleted s1abcd" || GetResult(s) != nil {
		t.Fatalf("%q %v", ui.notes, GetResult(s))
	}
	// Dismissing either dialog changes nothing.
	for _, answers := range [][]string{nil, {"[f2abcd] 2 URLs fetched - 2h ago"}, {"garbage"}} {
		ui = &fakeUI{answers: answers}
		r.SearchCommand(ui)
		if len(ui.notes) != 0 || GetResult(f) == nil {
			t.Fatalf("%v %v", answers, ui.notes)
		}
	}
}

func TestSearchDetailsSearchQueriesAreBounded(t *testing.T) {
	q := make([]QueryResultData, 12)
	for i := range q {
		q[i] = QueryResultData{Query: "q"}
	}
	got := searchDetails(&StoredSearchData{ID: "x", Type: "search", Timestamp: nowMs(), Queries: q})
	if strings.Count(got, "- \"q\"") != 10 || !strings.Contains(got, "... and 2 more\n") {
		t.Fatal(got)
	}
}

func TestEntriesFromBranch(t *testing.T) {
	branch := []map[string]any{
		{"type": "message"},
		{"type": "custom", "customType": "other", "data": map[string]any{"a": 1}},
		{"type": "custom", "customType": "web-search-results", "data": map[string]any{"id": "abc", "type": "search"}},
	}
	got := entriesFromBranch(branch)
	if len(got) != 1 || got[0].CustomType != "web-search-results" || string(got[0].Data) != `{"id":"abc","type":"search"}` {
		t.Fatalf("%+v", got)
	}
	if entriesFromBranch(nil) != nil {
		t.Fatal("empty branch")
	}
}

func TestToToolResult(t *testing.T) {
	res := toToolResult(ToolOutput{
		Content: []ContentBlock{{Type: "text", Text: "one"}, {Type: "text", Text: "two"}, {Type: "image", Data: "AAAA", MimeType: "image/png"}},
		Details: map[string]any{"k": 1}, IsError: true})
	want := sdk.ImageContent{Data: "AAAA", MimeType: "image/png"}
	if res.Content != "one\n\ntwo" || len(res.Images) != 1 || res.Images[0] != want || !res.IsError || res.Details == nil {
		t.Fatalf("%+v", res)
	}
	if toToolResult(ToolOutput{}).Details != nil {
		t.Fatal("nil details must stay nil")
	}
}
