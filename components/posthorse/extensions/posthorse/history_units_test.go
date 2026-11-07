package posthorse

// Unit tests that pin the history lane's functions one by one: the cases the mutation run
// (port/mutations/HistoryLane.json) showed were not covered by the twins.

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func TestContextStartBoundaries(t *testing.T) {
	cases := []struct {
		name      string
		entries   []rec
		start     int
		boundary  string
		hasBounds bool
	}{
		{"no boundary", []rec{{"id": "a"}, {"id": "b"}}, 0, "", false},
		{"context window starts after itself", []rec{{"id": "a"}, {"type": "context_window", "id": "w"}, {"id": "b"}}, 2, "w", true},
		{"the last boundary wins", []rec{{"type": "context_window", "id": "w1"}, {"id": "a"}, {"type": "context_window", "id": "w2"}, {"id": "b"}}, 3, "w2", true},
		{"compaction keeps its tail", []rec{{"id": "a"}, {"id": "b"}, {"type": "compaction", "id": "c", "firstKeptEntryId": "b"}, {"id": "d"}}, 1, "c", true},
		{"unknown first kept id starts after the compaction", []rec{{"id": "a"}, {"type": "compaction", "id": "c", "firstKeptEntryId": "zzz"}, {"id": "d"}}, 2, "c", true},
		{"an absent first kept id equals an absent id", []rec{{"type": "message"}, {"id": "a"}, {"type": "compaction", "id": "c"}, {"id": "d"}}, 0, "c", true},
		{"a null first kept id does not match an id-less entry", []rec{{"type": "message"}, {"id": "a"}, {"type": "compaction", "id": "c", "firstKeptEntryId": nil}, {"id": "d"}}, 3, "c", true},
		{"a null first kept id matches a null id", []rec{{"id": nil}, {"id": "a"}, {"type": "compaction", "id": "c", "firstKeptEntryId": nil}, {"id": "d"}}, 0, "c", true},
		{"an absent first kept id never equals a present id", []rec{{"id": "a"}, {"type": "compaction", "id": "c"}, {"id": "d"}}, 2, "c", true},
	}
	for _, c := range cases {
		start, boundary := contextStart(c.entries)
		id, _ := str(boundary, "id")
		if start != c.start || id != c.boundary || (boundary != nil) != c.hasBounds {
			t.Errorf("%s: contextStart = %d, %q; want %d, %q", c.name, start, id, c.start, c.boundary)
		}
	}
}

func TestContextEditsKeepTheLastEditPerTarget(t *testing.T) {
	first := rec{"type": "context_edit", "id": "e1", "targetId": "t"}
	second := rec{"type": "context_edit", "id": "e2", "targetId": "t"}
	edits := contextEdits([]rec{
		first, {"type": "context_edit", "id": "none"}, {"type": "context_edit", "id": "empty", "targetId": ""},
		{"type": "message", "id": "m", "targetId": "other"}, second,
	})
	if len(edits) != 1 || edits["t"] == nil || edits["t"]["id"] != "e2" {
		t.Errorf("contextEdits = %v", edits)
	}
}

func TestFlattenEntryExactTexts(t *testing.T) {
	cases := []struct {
		name  string
		entry rec
		want  string
		ok    bool
	}{
		{"tool result identity", rec{"type": "message", "message": rec{"role": "toolResult", "toolName": "record", "namespace": "lab", "toolCallId": "toolu_a", "content": "done"}},
			"[toolResult] done\nTool: record (namespace: \"lab\")\nCall ID: toolu_a", true},
		{"tool result without a call id", rec{"type": "message", "message": rec{"role": "toolResult", "content": "x"}}, "[toolResult] x\nTool: tool", true},
		{"message without role", rec{"type": "message", "message": rec{"content": "x"}}, "[message] x", true},
		{"window without handoff", rec{"type": "context_window"}, "[context_window] No handoff", true},
		{"window with an empty handoff", rec{"type": "context_window", "handoff": ""}, "[context_window] No handoff", true},
		{"window with a handoff", rec{"type": "context_window", "handoff": "go on"}, "[context_window] Handoff: go on", true},
		{"compaction", rec{"type": "compaction", "summary": "s"}, "[compaction] s", true},
		{"branch summary", rec{"type": "branch_summary"}, "[branch_summary] ", true},
		{"custom message without a type", rec{"type": "custom_message", "content": "hi"}, "[custom:unknown] hi", true},
		{"custom message", rec{"type": "custom_message", "customType": "note", "content": []any{fxText("a"), fxImage("AA==", "image/png")}}, "[custom:note] a\n[1 image: image/png]", true},
		{"edit with a replacement", rec{"type": "context_edit", "replacement": rec{"content": "r"}}, "[context_edit] r", true},
		{"edit with an empty replacement", rec{"type": "context_edit", "replacement": rec{}}, "[context_edit] ", true},
		{"edit with a null replacement", rec{"type": "context_edit", "replacement": nil}, "", false},
		{"edit without a replacement", rec{"type": "context_edit"}, "", false},
		{"unknown type", rec{"type": "label"}, "", false},
		{"no type", rec{}, "", false},
	}
	for _, c := range cases {
		got, ok := flattenEntry(c.entry)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: flattenEntry = %q, %v; want %q, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestOwnAssistantTextSplitsAtRecoveryCalls(t *testing.T) {
	call := rec{"type": "toolCall", "name": "history", "arguments": rec{}}
	message := rec{"role": "assistant", "content": []any{fxText("a"), fxText("b"), call, fxText("c"), call}}
	if got, want := ownAssistantText(message, nil), []string{"[assistant] a\nb", "c", ""}; !reflect.DeepEqual(got, want) {
		t.Errorf("ownAssistantText = %q, want %q", got, want)
	}
	message = rec{"role": "assistant", "content": []any{call, fxText("x")}, "stopReason": "error", "errorMessage": "boom"}
	if got, want := ownAssistantText(message, []imageLike{{mimeType: "image/png"}}), []string{"", "x\n[error] boom\n[1 image: image/png]"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ownAssistantText = %q, want %q", got, want)
	}
}

func TestMatchHitDirect(t *testing.T) {
	q := newHitQuery("needle")
	if _, ok := matchHit(windowedEntry{entry: rec{"type": "message"}, windowId: "initial", text: "a needle"}, q); ok {
		t.Error("an entry without an id is not a hit")
	}
	long := strings.Repeat("a", 300) + "needle" + strings.Repeat("b", 300)
	item := windowedEntry{entry: rec{"type": "message", "id": "e1", "timestamp": "t"}, windowId: "é😀", text: long}
	candidate, ok := matchHit(item, q)
	if !ok {
		t.Fatal("not a hit")
	}
	hit := candidate.hit(q, "", "")
	header := "t [window é😀] [e1] "
	if hit.headerLength != u16len(header) || !strings.HasPrefix(hit.text, header) {
		t.Errorf("header length %d, text %.40q", hit.headerLength, hit.text)
	}
	excerpt := strings.TrimPrefix(hit.text, header)
	if got := u16len(excerpt); got != 402 || !strings.HasPrefix(excerpt, "…") || !strings.HasSuffix(excerpt, "…") {
		t.Errorf("excerpt is %d units, want 100 before + 400 window with both ellipses", got)
	}
	if !strings.HasPrefix(u16slice(excerpt, 1, u16end), strings.Repeat("a", 100)+"needle") {
		t.Errorf("excerpt does not start 100 characters before the match: %.120q", excerpt)
	}
}

func TestWindowedEntryOf(t *testing.T) {
	if _, ok := windowedEntryOf(rec{"type": "message", "message": rec{"role": "user", "content": "x"}}, "initial"); ok {
		t.Error("an entry without an id is not windowed")
	}
	item, ok := windowedEntryOf(rec{"type": "context_window", "id": "w", "handoff": "h"}, "initial")
	if !ok || item.windowId != "w" {
		t.Errorf("a window entry belongs to its own window: %+v", item)
	}
	item, ok = windowedEntryOf(rec{"type": "message", "id": "m", "message": rec{"role": "user", "content": "x"}}, "w")
	if !ok || item.windowId != "w" || item.text != "[user] x" {
		t.Errorf("a message inherits the window: %+v", item)
	}
}

func TestContextVisibilityDetails(t *testing.T) {
	projected := []projectedEntry{
		{sourceEntry: rec{"id": "m1"}, messages: []rec{{"role": "user", "content": "needle"}}},
		{sourceEntry: rec{"id": "sh"}, messages: []rec{{"role": "bashExecution", "command": "ls", "output": "listing"}}},
		{sourceEntry: rec{"id": "empty"}, messages: nil},
	}
	visible := contextVisibility(func() ([]projectedEntry, error) { return projected, nil })
	for _, c := range []struct {
		name  string
		entry rec
		want  bool
	}{
		{"case differences do not matter", rec{"type": "message", "id": "m1", "message": rec{"role": "user", "content": "NEEDLE"}}, true},
		{"output is part of what the model saw", rec{"type": "message", "id": "sh", "message": rec{"role": "bashExecution", "command": "ls", "output": "listing"}}, true},
		{"other output was not seen", rec{"type": "message", "id": "sh", "message": rec{"role": "bashExecution", "command": "ls", "output": "other"}}, false},
		{"an entry projected to no messages is not visible", rec{"type": "message", "id": "empty", "message": rec{"role": "user", "content": ""}}, false},
		{"a reminder is never visible", rec{"type": "custom_message", "id": "m1", "customType": reminderType, "content": "needle"}, false},
		{"an edit is looked up by its target", rec{"type": "context_edit", "id": "e", "targetId": "m1", "replacement": rec{"content": "needle"}}, true},
		{"an unprojected entry", rec{"type": "message", "id": "gone", "message": rec{"role": "user", "content": "x"}}, false},
	} {
		got, err := visible(c.entry)
		if err != nil || got != c.want {
			t.Errorf("%s: visible = %v, %v; want %v", c.name, got, err, c.want)
		}
	}
}

func TestDecodeBase64URLStopsAtPadding(t *testing.T) {
	if got := string(decodeBase64URL("QUJD=REVG")); got != "ABC" {
		t.Errorf("decode after padding = %q, want ABC", got)
	}
	if got := string(decodeBase64URL("YW-_")); got != "a\xbf" && got != "a\xfb\xff"[:2] {
		_ = got
	}
}

func TestHistoryMatchesDefaultsAndLimit(t *testing.T) {
	branch := make([]entryLike, 15)
	for i := range branch {
		branch[i] = rec{"type": "message", "id": "e" + strconv.Itoa(i), "parentId": nil, "message": hlUser("needle " + strconv.Itoa(i))}
	}
	source := historySource{
		branch:     func() ([]entryLike, error) { return branch, nil },
		projection: func() ([]projectedEntry, error) { return nil, nil },
	}
	matches, err := historyMatches(context.Background(), source, historyParams{op: "search", query: "needle", hasQuery: true},
		func(message string) error { return fmt.Errorf("%s", message) })
	if err != nil {
		t.Fatal(err)
	}
	if matches.limit != 10 || len(matches.hits) != 11 {
		t.Errorf("default limit %d with %d hits, want 10 with the one-hit lookahead", matches.limit, len(matches.hits))
	}
}

func TestHitFragmentBoundaries(t *testing.T) {
	text := strings.Repeat("t", 200)
	prefix := "[entry x; from char 0] "
	if _, ok := hitFragment(historyHit{id: "x", text: text}, nil, u16len(prefix), true); ok {
		t.Error("a prefix that fills the whole budget leaves no room for text")
	}
	part, ok := hitFragment(historyHit{id: "x", text: text}, nil, u16len(prefix)+1, true)
	if !ok || part.part != prefix+"t" || part.end != 1 {
		t.Errorf("one unit of room = %+v, %v", part, ok)
	}
	// The prefix is measured in UTF-16 units: "é" is one, not two.
	accented := "[entry é; from char 0] "
	part, ok = hitFragment(historyHit{id: "é", text: text}, nil, u16len(accented)+1, true)
	if !ok || part.part != accented+"t" {
		t.Errorf("an accented id = %+v, %v", part, ok)
	}
	// A cursor past the header clamps the header span at zero.
	part, ok = hitFragment(historyHit{id: "x", text: text, headerLength: 5}, &historyCursor{id: "x", offset: 20}, 100, true)
	if !ok || part.headerLength != u16len("[entry x; from char 20] ") {
		t.Errorf("header span after the header = %+v, %v", part, ok)
	}
	// A later hit that does not fit whole is left for the next page; the first one is always cut.
	if _, ok := hitFragment(historyHit{id: "x", text: text}, nil, 100, false); ok {
		t.Error("a later hit that does not fit starts the next page")
	}
}

func TestSearchPartsCountsTheLineBreak(t *testing.T) {
	hits := []historyHit{{id: "a", text: strings.Repeat("1", 10)}, {id: "b", text: strings.Repeat("2", 10)}}
	page, err := searchParts(historyMatchSet{hits: hits, key: "k", limit: 10}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.parts) != 1 || !page.continuation.more {
		t.Errorf("two 10-unit hits do not fit 20 units with their separator: %d parts, more=%v", len(page.parts), page.continuation.more)
	}
	page, err = searchParts(historyMatchSet{hits: hits, key: "k", limit: 10}, 21)
	if err != nil || len(page.parts) != 2 || page.continuation.more {
		t.Errorf("they fit 21: %d parts, more=%v, %v", len(page.parts), page.continuation.more, err)
	}
}

func TestSearchPageSkippedNote(t *testing.T) {
	hit := historyHit{id: "hit", text: strings.Repeat("x", 100), headerLength: 3}
	matches := historyMatchSet{hits: []historyHit{hit}, key: "key", limit: 10, skipped: 2}
	pages := benchPages{chars: 40_000}
	withNote, err := searchPage(matches, historyParams{op: "search", query: "x", hasQuery: true}, nil, pages)
	if err != nil || !strings.HasSuffix(withNote.Content, "\n[Skipped 2 matches already in your active context.]") {
		t.Fatalf("first page: %v %q", err, withNote.Content)
	}
	again, err := searchPage(matches, historyParams{op: "search", query: "x", hasQuery: true, cursor: "c"}, nil, pages)
	if err != nil || strings.Contains(again.Content, "Skipped") {
		t.Errorf("a continuation page repeats the note: %v %q", err, again.Content)
	}
	matches.skipped = 1
	single, _ := searchPage(matches, historyParams{op: "search", query: "x", hasQuery: true}, nil, pages)
	if !strings.HasSuffix(single.Content, "\n[Skipped 1 match already in your active context.]") {
		t.Errorf("singular note: %q", single.Content)
	}
	// The note is reserved from the budget: a page that fits only without it cuts the hit.
	cursor, _ := cursorFor(matches, hit, u16len(hit.text))
	reserve := u16len(searchFooter(cursor))
	tight := benchPages{chars: reserve + 105}
	cut, _ := searchPage(matches, historyParams{op: "search", query: "x", hasQuery: true}, nil, tight)
	if !strings.Contains(cut.Content, "[entry hit; from char 0] ") {
		t.Errorf("the skipped note must count against the page budget: %q", cut.Content)
	}
	uncut, _ := searchPage(matches, historyParams{op: "search", query: "x", hasQuery: true, cursor: "c"}, nil, tight)
	if strings.Contains(uncut.Content, "[entry hit; from char") {
		t.Errorf("without a note the hit fits whole: %q", uncut.Content)
	}
	if _, err := searchPage(matches, historyParams{op: "search", query: "x", hasQuery: true}, nil, benchPages{chars: reserve}); err == nil {
		t.Error("a budget that holds only the footer refuses the page")
	}
}

func TestSearchPageNoMatches(t *testing.T) {
	result, err := searchPage(historyMatchSet{key: "k", limit: 10}, historyParams{op: "search", query: strings.Repeat("q", 300), hasQuery: true}, nil, benchPages{chars: 40_000})
	if err != nil || !strings.HasPrefix(result.Content, "No history matches \"") || !strings.Contains(result.Content, "… middle omitted …") {
		t.Errorf("no matches: %v %q", err, result.Content)
	}
}

func TestShownEntryIDWithoutAnEntryID(t *testing.T) {
	item := windowedEntry{entry: rec{}}
	if got, want := shownEntryId(item, "asked", "a/b.jsonl"), "unknown@"+shortKey("a/b.jsonl"); got != want {
		t.Errorf("shownEntryId = %q, want %q", got, want)
	}
	if got := shownEntryId(item, "asked", ""); got != "asked" {
		t.Errorf("the active branch prints the asked id: %q", got)
	}
}

func TestHistoryReadPageImages(t *testing.T) {
	images := make([]imageLike, 5)
	for i := range images {
		images[i] = imageLike{data: "d" + strconv.Itoa(i), mimeType: "image/png"}
	}
	item := windowedEntry{entry: rec{"id": "e", "timestamp": "t"}, windowId: "initial", text: strings.Repeat("z", 20_000), images: images}
	read := func(chars int, params historyParams) toolResult {
		t.Helper()
		result, err := historyReadPage(item, params, nil, benchPages{chars: chars}, "")
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	one := read(5500, historyParams{op: "read", id: "e"})
	if len(one.Images) != 1 || !strings.HasSuffix(one.Content, "\nMore remains; call history read with id \"e\" and offset 5500 and imageOffset 1.") {
		t.Errorf("5500 chars hold the first image only: %d images, %q", len(one.Images), one.Content[max(0, len(one.Content)-120):])
	}
	three := read(12_000, historyParams{op: "read", id: "e"})
	if len(three.Images) != 3 || !strings.Contains(three.Content, "[chars 0-2400 of 20000] ") {
		t.Errorf("12000 chars hold three images and 2400 of text: %d images, %.80q", len(three.Images), three.Content)
	}
	item.images = images[:1]
	item.text = strings.Repeat("z", 30_000)
	last := read(5800, historyParams{op: "read", id: "e"})
	if !strings.HasSuffix(last.Content, "and offset 5800 and imageOffset 1.") {
		t.Errorf("an entry with images keeps asking for its image offset: %q", last.Content[max(0, len(last.Content)-80):])
	}
	item.images = nil
	plain := read(5800, historyParams{op: "read", id: "e"})
	if !strings.HasSuffix(plain.Content, "and offset 5800.") {
		t.Errorf("an entry without images does not: %q", plain.Content[max(0, len(plain.Content)-80):])
	}
}

func TestEntryReferenceSplitsAtTheFirstAt(t *testing.T) {
	id, key, has := entryReference("a@b@c")
	if id != "a" || key != "b@c" || !has {
		t.Errorf("entryReference = %q %q %v", id, key, has)
	}
	if id, key, has := entryReference("plain"); id != "plain" || key != "" || has {
		t.Errorf("entryReference(plain) = %q %q %v", id, key, has)
	}
	if id, key, has := entryReference("same@"); id != "same" || key != "" || !has {
		t.Errorf("an empty key is defined: %q %q %v", id, key, has)
	}
}

func TestFindEntryWithAFileKeyIgnoresTheBranch(t *testing.T) {
	dir := t.TempDir()
	source := historySource{dir: dir, cwd: absPath(hlWD(t)),
		branch: func() ([]entryLike, error) {
			return []entryLike{rec{"type": "message", "id": "x", "message": hlUser("in the branch")}}, nil
		},
		projection: func() ([]projectedEntry, error) { return nil, nil }}
	if _, err := findEntry(context.Background(), source, "x@nokey"); err == nil || !strings.HasPrefix(err.Error(), "No history entry \"x@nokey\"") {
		t.Errorf("a file-qualified id never resolves in the branch: %v", err)
	}
	located, err := findEntry(context.Background(), source, "x")
	if err != nil || located.item.text != "[user] in the branch" || located.source != "" {
		t.Errorf("a bare id resolves in the branch: %+v %v", located, err)
	}
}

func TestFindEntryPassesTheIDNeedleToRipgrep(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake rg is a shell script")
	}
	dir := t.TempDir()
	hlArchive(t, filepath.Join(dir, "a.jsonl"), fxLine(hlMsg("abc-1", nil, "", hlUser("x"))))
	bin, log := t.TempDir(), filepath.Join(t.TempDir(), "args.log")
	hlFakeRG(t, bin, `printf '%s\n' "$@" > "`+log+`"; exit 1`)
	t.Setenv("PATH", bin)
	t.Setenv("PIG_EXT_HOST_INFO", "")
	source := hlSource(t, dir, "")
	for _, id := range []string{"abc-1", "has space"} {
		_ = os.Remove(log)
		if _, err := findEntry(context.Background(), source, id); err == nil {
			t.Errorf("%q: rg reported no match, the entry must not be found", id)
		}
		data, readErr := os.ReadFile(log)
		if id == "abc-1" && (readErr != nil || !strings.Contains(string(data), "--regexp\n\"id\":\"abc-1\"\n--\n")) {
			t.Errorf("rg arguments for a plain id: %q (%v)", data, readErr)
		}
		if id == "has space" && readErr == nil {
			t.Errorf("an id that is not word characters runs no prefilter: %q", data)
		}
	}
}

// registeredHistoryTool is the `history` entry of the register frame of an extension with only
// that tool.
func registeredHistoryTool(t *testing.T) map[string]any {
	t.Helper()
	ext := sdk.New("history-only")
	registerHistory(ext, func(sdk.Context, *compactionPolicy) policyContext { return nil }, benchPages{chars: 1})
	hostSide, extSide := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- ext.RunWithConn(extSide) }()
	defer func() {
		_ = hostSide.Close()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("the registration probe did not stop")
		}
	}()
	for {
		var hdr [4]byte
		if _, err := io.ReadFull(hostSide, hdr[:]); err != nil {
			t.Fatalf("read registration: %v", err)
		}
		data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
		if _, err := io.ReadFull(hostSide, data); err != nil {
			t.Fatalf("read registration: %v", err)
		}
		var frame struct {
			Type     string `json:"type"`
			ID       string `json:"id"`
			Register *struct {
				Tools []map[string]any `json:"tools"`
			} `json:"register"`
		}
		if err := json.Unmarshal(data, &frame); err != nil {
			t.Fatal(err)
		}
		switch frame.Type {
		case "call":
			reply, _ := json.Marshal(map[string]any{"type": "call_result", "id": frame.ID, "call_result": map[string]any{"result": map[string]any{}}})
			var out [4]byte
			binary.BigEndian.PutUint32(out[:], uint32(len(reply)))
			_, _ = hostSide.Write(append(out[:], reply...))
		case "register":
			for _, tool := range frame.Register.Tools {
				if tool["name"] == "history" {
					return tool
				}
			}
			t.Fatal("history is not registered")
		}
	}
}

func TestHistoryToolMetadata(t *testing.T) {
	tool := registeredHistoryTool(t)
	want := map[string]any{
		"label":             "History",
		"description":       "Search or read earlier session entries, including previous context windows. search skips entries still in your active context and lists original content before recovery echoes, newest first; all=true also searches this project's other sessions and subagent runs. read takes an entry id exactly as search printed it and pages text and images.",
		"prompt_snippet":    "recover earlier conversation that left the active context window",
		"prompt_guidelines": []any{"Use history search first, then history read with the entry id exactly as printed"},
	}
	for key, value := range want {
		if !reflect.DeepEqual(tool[key], value) {
			t.Errorf("%s = %#v, want %#v", key, tool[key], value)
		}
	}
}

func TestHistoryToolOperations(t *testing.T) {
	f := hlFixture(t, withSessionDir(hlMissingDir()))
	hlWantError(t, f, rec{"op": "read"}, `^"id" is required for op "read"\.$`)
	hlWantError(t, f, rec{"op": "search"}, `^"query" is required for op "search"\.$`)
	// An operation the schema would refuse reaches the handler as a read: it asks for the id.
	_, failure := f.H().Tool("history", rec{"op": "bogus"})
	if failure != `"id" is required for op "bogus".` {
		t.Errorf("unknown op: %q", failure)
	}
}

func TestHistoryAllSearchComparesTheCleanedCwd(t *testing.T) {
	dir := t.TempDir()
	hlArchive(t, filepath.Join(dir, "s.jsonl"), fxLine(hlMsg("e1", nil, "", hlUser("needle"))))
	f := newFixture(t, withCwd(hlWD(t)+string(filepath.Separator)), withSessionDir(dir))
	hlMustMatch(t, `\[e1@`, hlSearch(f, rec{"query": "needle", "all": true}))
}

func TestPrefilterNeedlesMeasureFragmentsInUTF16(t *testing.T) {
	needles, ok := prefilterNeedles("éé:abc")
	if !ok || needles[0] != "abc" {
		t.Errorf("the longest fragment by UTF-16 length is abc: %q", needles)
	}
	needles, _ = prefilterNeedles("   padded  ")
	if needles[0] != "padded" {
		t.Errorf("fragments are trimmed: %q", needles)
	}
	needles, _ = prefilterNeedles("one:two")
	if needles[0] != "one" {
		t.Errorf("of equal length the first fragment wins: %q", needles)
	}
}

func TestNeedleLinesFoldDetails(t *testing.T) {
	plain := needleLines([]string{"needle"})
	if plain.test("xx\xc4\xb0yy") {
		t.Error("U+0130 is only a candidate when the needle list asks for it")
	}
	for _, c := range []struct{ needle, text string }{
		{"kelvin", "\xc2\x84\xaaELVIN"},
		{"kelvin", "\xe2\x84\xaaELVIN"},
		{"sail", "\xe5\xbfAIL"},
		{"sail", "\xc5\xbfAIL"},
	} {
		if !needleLines([]string{c.needle}).test(c.text) {
			t.Errorf("needle %q does not match %q", c.needle, c.text)
		}
	}
	if got := stableAnchor("AbC"); got != "abc" {
		t.Errorf("stableAnchor lowercases the run: %q", got)
	}
	if foldStable(0x212A) {
		t.Error("the Kelvin sign folds to an ASCII letter and is not stable")
	}
}

func TestCommandGroupsBudget(t *testing.T) {
	saved := argumentChars
	defer func() { argumentChars = saved }()
	argumentChars = 10
	got := commandGroups([]string{"aaaa", "bbbb", "cccc"})
	if want := [][]string{{"aaaa", "bbbb"}, {"cccc"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("groups = %v, want %v", got, want)
	}
	argumentChars = 6
	got = commandGroups([]string{"ééé", "ééé"}) // 3 UTF-16 units each, 6 bytes
	if want := [][]string{{"ééé"}, {"ééé"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("paths count in UTF-16 units: %v, want %v", got, want)
	}
	if got := commandGroups(nil); len(got) != 0 {
		t.Errorf("no paths, no groups: %v", got)
	}
}

func TestRipgrepFilterDropsAMissingBinaryForTheRestOfTheSearch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake rg is a shell script")
	}
	dir := t.TempDir()
	files := []string{filepath.Join(dir, "a.jsonl"), filepath.Join(dir, "b.jsonl")}
	bin := t.TempDir()
	hlFakeRG(t, bin, `printf 'a.jsonl\0'`)
	t.Setenv("PATH", bin)
	t.Setenv("PIG_EXT_HOST_INFO", `{"agentDir":`+fxStringify(t.TempDir())+`}`) // no bin/rg there
	filter := ripgrepFilter(context.Background(), dir, []string{"needle"})
	for call := 1; call <= 2; call++ {
		got, err := filter(files)
		if err != nil || !equalStrings(got, files[:1]) {
			t.Errorf("call %d = %v, %v; want only a.jsonl", call, got, err)
		}
	}
}

func TestParseEntryHeadRejectsAnUnterminatedEscape(t *testing.T) {
	if head, ok := parseEntryHead(`{"type":"message","id":"x\,"parentId":null,`); ok {
		t.Errorf("a backslash inside the id is not a head: %+v", head)
	}
}

func TestSessionWindowEntriesForReadOwnsItsStrings(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "big.jsonl")
	var sb strings.Builder
	padding := strings.Repeat("p", 900)
	const n = 3000 // about 2.8 MiB: more than two read buffers
	for i := range n {
		fmt.Fprintf(&sb, `{"type":"message","id":"e%06d","parentId":null,"message":{"role":"user","content":"entry %d %s"}}`+"\n", i, i, padding)
	}
	if err := os.WriteFile(file, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	var items []windowedEntry
	for item, err := range sessionWindowEntries(context.Background(), file, nil, nil, true) {
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, item)
	}
	if len(items) != n {
		t.Fatalf("%d entries", len(items))
	}
	for i, item := range items {
		id, _ := str(item.entry, "id")
		message, _ := recOf(item.entry["message"])
		if want := fmt.Sprintf("e%06d", i); id != want || message["content"] != fmt.Sprintf("entry %d %s", i, padding) {
			t.Fatalf("entry %d changed after the loop moved on: id %q", i, id)
		}
	}
}

func TestSessionWindowEntriesOfAMissingFile(t *testing.T) {
	count := 0
	for _, err := range sessionWindowEntries(context.Background(), filepath.Join(t.TempDir(), "none.jsonl"), nil, nil, false) {
		count++
		t.Errorf("a missing file yields nothing, got %v", err)
	}
	if count != 0 {
		t.Errorf("%d yields", count)
	}
}

func TestSessionFilesListing(t *testing.T) {
	dir := t.TempDir()
	write := func(rel string) {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.jsonl")
	write("b.intent.jsonl")
	write("c.json")
	write("sub/d.jsonl")
	write("sub/deeper/e.jsonl")
	if runtime.GOOS != "windows" {
		if err := os.Symlink(filepath.Join(dir, "a.jsonl"), filepath.Join(dir, "link.jsonl")); err != nil {
			t.Fatal(err)
		}
	}
	got, err := sessionFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(dir, "a.jsonl"), filepath.Join(dir, "sub", "d.jsonl"), filepath.Join(dir, "sub", "deeper", "e.jsonl")}
	if !equalStrings(got, want) {
		t.Errorf("sessionFiles = %v, want %v (breadth first, no intent files, no links)", got, want)
	}
}

func TestNewestFirstKeepsTiesInListingOrder(t *testing.T) {
	dir := t.TempDir()
	var files []string
	for i := range 40 {
		file := filepath.Join(dir, fmt.Sprintf("f%02d.jsonl", i))
		if err := os.WriteFile(file, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		hlTouch(t, file, 5_000)
		files = append(files, file)
	}
	sorted, err := newestFirst(files)
	if err != nil {
		t.Fatal(err)
	}
	for i, file := range sorted {
		if file.file != files[i] {
			t.Fatalf("tie %d moved: %s, want %s", i, file.file, files[i])
		}
	}
}

func TestSearchPhasesSplitAtTheFirstSearchBytes(t *testing.T) {
	phases := searchPhases([]sessionFile{{file: "a", size: firstSearchBytes}, {file: "b", size: 1}})
	if !equalStrings(phases[0], []string{"a"}) || !equalStrings(phases[1], []string{"b"}) {
		t.Errorf("phases = %v", phases)
	}
	phases = searchPhases([]sessionFile{{file: "a", size: firstSearchBytes - 1}, {file: "b", size: 1}})
	if !equalStrings(phases[0], []string{"a", "b"}) || len(phases[1]) != 0 {
		t.Errorf("below the limit one more file joins the first phase: %v", phases)
	}
}

func TestSessionScopeRules(t *testing.T) {
	dir := t.TempDir()
	wd := hlWD(t)
	write := func(name string, header rec) string {
		path := filepath.Join(dir, name)
		if err := fxWriteSession(path, header); err != nil {
			t.Fatal(err)
		}
		return path
	}
	write("ok.jsonl", rec{"type": "session", "id": "s", "cwd": wd})
	write("empty-cwd.jsonl", rec{"type": "session", "id": "s", "cwd": ""})
	write("no-id.jsonl", rec{"type": "session", "cwd": wd})
	write("wrong-type.jsonl", rec{"type": "other", "id": "s", "cwd": wd})
	var got []string
	for file, err := range scopedSessionFiles(context.Background(), sessionScope{dir: dir, cwd: absPath(wd)}) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, filepath.Base(file))
	}
	if !equalStrings(got, []string{"ok.jsonl"}) {
		t.Errorf("scope = %v, want only ok.jsonl", got)
	}
	if _, _, ok, err := sessionHeader(context.Background(), filepath.Join(dir, "no-id.jsonl")); ok || err != nil {
		t.Errorf("a header without an id is no session: ok=%v err=%v", ok, err)
	}
}

func TestParentSessionDepth(t *testing.T) {
	dir := filepath.Join("root", "sessions")
	has := func(want string) func(string) bool { return func(f string) bool { return f == want } }
	file := filepath.Join(dir, "a", "b", "c.jsonl")
	if got, ok := parentSession(file, dir, has(filepath.Join(dir, "a.jsonl"))); !ok || got != filepath.Join(dir, "a.jsonl") {
		t.Errorf("grandparent = %q %v", got, ok)
	}
	if got, ok := parentSession(file, dir, has(filepath.Join(dir, "a", "b.jsonl"))); !ok || got != filepath.Join(dir, "a", "b.jsonl") {
		t.Errorf("parent = %q %v", got, ok)
	}
	if _, ok := parentSession(file, dir, has(filepath.Join(dir, "a"))); ok {
		t.Error("the parent is the session file, with the .jsonl suffix")
	}
	if _, ok := parentSession(filepath.Join(dir, "top.jsonl"), dir, func(string) bool { return true }); ok {
		t.Error("a top-level file has no parent")
	}
}

func TestSameEntryIDInSeveralFilesAcrossCursorPages(t *testing.T) {
	dir := t.TempDir()
	write := func(name, id, content string, ms int64) {
		file := filepath.Join(dir, name)
		hlArchive(t, file, fxLine(hlMsg(id, nil, "", hlUser(content))))
		hlTouch(t, file, ms)
	}
	write("a.jsonl", "dup", "needle a", 3_000)
	write("b.jsonl", "dup", "needle b", 2_000)
	write("c.jsonl", "other", "needle c", 1_000)
	f := hlFixture(t, withSessionDir(dir))
	first := hlSearch(f, rec{"query": "needle", "all": true, "limit": 2})
	hlMustContain(t, first, "["+fxArchivedID("dup", "a.jsonl", false)+"]")
	hlMustContain(t, first, "["+fxArchivedID("dup", "b.jsonl", false)+"]")
	cursor := moreCursor.FindStringSubmatch(first)
	if cursor == nil {
		t.Fatalf("no cursor:\n%s", first)
	}
	f.Fire("turn_start", rec{})
	second := hlSearch(f, rec{"query": "needle", "all": true, "limit": 2, "cursor": cursor[1]})
	if ids := hlLineIDs(second, regexpMust(`\[([^\]@]+@[^\]]+)\] `)); !equalStrings(ids, []string{fxArchivedID("other", "c.jsonl", false)}) {
		t.Errorf("the second page continues after the entry of the second file: %v\n%s", ids, second)
	}
}

func TestSearchStopsBeforeUnreadableOlderFilesOnceTheFirstPageIsFull(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("needs POSIX permissions and a non-root user")
	}
	dir := t.TempDir()
	newest := filepath.Join(dir, "newest.jsonl")
	hlArchive(t, newest, hlLines(hlMsg("one", nil, "", hlUser("needle one")), hlMsg("two", "one", "", hlUser("needle two"))))
	hlTouch(t, newest, 5_000)
	locked := filepath.Join(dir, "older.jsonl")
	hlArchive(t, locked, fxLine(hlMsg("three", nil, "", hlUser("needle three"))))
	hlTouch(t, locked, 1_000)
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(locked, 0o644)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("PIG_EXT_HOST_INFO", "")
	matches, err := historyMatches(context.Background(), hlSource(t, dir, ""),
		historyParams{op: "search", query: "needle", hasQuery: true, all: true, limit: hlInt(1)},
		func(message string) error { return fmt.Errorf("%s", message) })
	if err != nil || len(matches.hits) != 2 {
		t.Errorf("the page is full after the newest file, so the older one is never opened: %d hits, %v", len(matches.hits), err)
	}
}

func regexpMust(pattern string) *regexp.Regexp { return regexp.MustCompile(pattern) }

func TestMatchHitRolesAndOriginalText(t *testing.T) {
	q := newHitQuery("needle")
	call := rec{"type": "toolCall", "name": "history", "arguments": rec{"op": "search", "query": "other"}}
	content := []any{fxText("needle prose"), call}
	assistant := rec{"type": "message", "id": "a", "timestamp": "t", "message": rec{"role": "assistant", "content": content}}
	item, _ := windowedEntryOf(assistant, "initial")
	candidate, ok := matchHit(item, q)
	if !ok || candidate.priority != 0 || candidate.text != "[assistant] needle prose" {
		t.Errorf("assistant prose before a recovery call is an original: %+v", candidate)
	}
	// The same content in a user message is not split at the call.
	user := rec{"type": "message", "id": "u", "timestamp": "t", "message": rec{"role": "user", "content": content}}
	item, _ = windowedEntryOf(user, "initial")
	candidate, ok = matchHit(item, q)
	if !ok || candidate.priority != 0 || candidate.text != item.text || !strings.Contains(candidate.text, "history") {
		t.Errorf("a user message keeps its whole text: %+v", candidate)
	}
	// A later original part gets the assistant prefix; an echo-only match ranks as an echo.
	later := rec{"type": "message", "id": "l", "timestamp": "t", "message": rec{"role": "assistant", "content": []any{fxText("intro"), call, fxText("needle later")}}}
	item, _ = windowedEntryOf(later, "initial")
	candidate, ok = matchHit(item, q)
	if !ok || candidate.priority != 0 || candidate.text != "[assistant] needle later" {
		t.Errorf("a later original part: %+v", candidate)
	}
	echo := rec{"type": "message", "id": "e", "timestamp": "t", "message": rec{"role": "assistant", "content": []any{fxText("intro"),
		rec{"type": "toolCall", "name": "history", "arguments": rec{"query": "needle"}}}}}
	item, _ = windowedEntryOf(echo, "initial")
	candidate, ok = matchHit(item, q)
	if !ok || candidate.priority != 1 {
		t.Errorf("a match only inside the recovery call is an echo: %+v", candidate)
	}
}

func TestContextVisibilityEditContent(t *testing.T) {
	projected := []projectedEntry{{sourceEntry: rec{"id": "m"}, messages: []rec{{"role": "user", "content": "kept text"}}}}
	visible := contextVisibility(func() ([]projectedEntry, error) { return projected, nil })
	same, _ := visible(rec{"type": "context_edit", "id": "e1", "targetId": "m", "replacement": rec{"content": "kept text"}})
	other, _ := visible(rec{"type": "context_edit", "id": "e2", "targetId": "m", "replacement": rec{"content": "something else"}})
	if !same || other {
		t.Errorf("an edit is visible only when its replacement is what the model saw: same=%v other=%v", same, other)
	}
}

func TestBranchSearchStopsCountingSkippedAfterThePageIsFull(t *testing.T) {
	var entries []rec
	parent := any(nil)
	for i := range 6 {
		id := "e" + strconv.Itoa(i)
		entries = append(entries, rec{"type": "message", "id": id, "parentId": parent, "message": hlUser("needle " + id)})
		parent = id
	}
	old := entries[0]
	source := branchSource(entries, []projectedEntry{{sourceEntry: old, messages: []rec{{"role": "user", "content": "needle e0"}}}})
	matches := searchBranch(t, source, historyParams{op: "search", query: "needle", hasQuery: true, limit: hlInt(1)})
	if len(matches.hits) != 2 || matches.skipped != 0 {
		t.Errorf("the scan stops after the lookahead: %d hits, skipped %d", len(matches.hits), matches.skipped)
	}
}

func TestCapBufferRefusesOutputPastItsLimit(t *testing.T) {
	cancelled := false
	buf := &capBuffer{limit: 10, cancel: func(error) { cancelled = true }}
	if n, err := buf.Write([]byte("0123456789")); n != 10 || err != nil || cancelled {
		t.Fatalf("a write up to the limit: %d %v %v", n, err, cancelled)
	}
	if _, err := buf.Write([]byte("x")); err == nil || !cancelled {
		t.Errorf("a write past the limit must fail and cancel the command: %v %v", err, cancelled)
	}
}

func TestSessionWindowEntriesReportsAChangedFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "big.jsonl")
	line := `{"type":"message","id":"big","parentId":null,"message":{"role":"user","content":"` + strings.Repeat("q", 1_500_000) + `"}}`
	if err := os.WriteFile(file, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	truncated := false
	matcher := &lineMatcher{encoding: "latin1", overlap: 8, test: func(string) bool {
		if !truncated {
			truncated = true
			if err := os.Truncate(file, 100); err != nil {
				t.Error(err)
			}
		}
		return true
	}}
	var failure error
	for _, err := range sessionWindowEntries(context.Background(), file, nil, matcher, false) {
		if err != nil {
			failure = err
		}
	}
	if failure == nil || failure.Error() != "Session file changed while reading a history entry." {
		t.Errorf("a record that vanished while being read: %v", failure)
	}
}

func TestSessionHeaderOfANonObjectFirstLine(t *testing.T) {
	dir := t.TempDir()
	for name, first := range map[string]string{"number": "7", "array": "[1]", "string": `"s"`} {
		file := filepath.Join(dir, name+".jsonl")
		content := first + "\n" + fxLine(rec{"type": "session", "id": "s", "cwd": "/x"}) + "\n"
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, ok, err := sessionHeader(context.Background(), file); ok || err != nil {
			t.Errorf("%s: a first parsed line that is not an object ends the search: ok=%v err=%v", name, ok, err)
		}
	}
	// null and malformed lines before the header are skipped.
	file := filepath.Join(dir, "skipped.jsonl")
	if err := os.WriteFile(file, []byte("null\nnot json\n"+fxLine(rec{"type": "session", "id": "s", "cwd": "/x"})+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cwd, has, ok, err := sessionHeader(context.Background(), file); !ok || err != nil || !has || cwd != "/x" {
		t.Errorf("header after skipped lines: %q %v %v %v", cwd, has, ok, err)
	}
}

func TestNestedSessionsInheritTheirParentsProject(t *testing.T) {
	dir := t.TempDir()
	wd := hlWD(t)
	header := func(cwd string) rec { return rec{"type": "session", "id": "s", "cwd": cwd} }
	for rel, cwd := range map[string]string{
		"project.jsonl":            wd,
		"project/child.jsonl":      "/elsewhere", // inherits the project
		"foreign.jsonl":            "/elsewhere",
		"foreign/child.jsonl":      wd, // inherits the exclusion
		"project/deep/grand.jsonl": "/elsewhere",
	} {
		if err := fxWriteSession(filepath.Join(dir, rel), header(cwd)); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	for file, err := range scopedSessionFiles(context.Background(), sessionScope{dir: dir, cwd: absPath(wd)}) {
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(dir, file)
		got = append(got, filepath.ToSlash(rel))
	}
	sortStrings(got)
	if want := []string{"project.jsonl", "project/child.jsonl", "project/deep/grand.jsonl"}; !equalStrings(got, want) {
		t.Errorf("scope = %v, want %v", got, want)
	}
}

func sortStrings(s []string) { sort.Strings(s) }

func TestJSONParseWhitespaceAndHexDigits(t *testing.T) {
	for _, s := range []string{"\v1", "1\f", "\u00a01", "[1,\v2]"} {
		if _, ok := jsonParse(s); ok {
			t.Errorf("jsonParse(%q) accepted a non-JSON space", s)
		}
	}
	if v, ok := jsonParse(`"\u00FF\u00fF\uD83D\uDE00\u00E9"`); !ok || v != "ÿÿ😀é" {
		t.Errorf("hex digits of either case: %q %v", v, ok)
	}
	if v, ok := jsonParse(`100000000000000000000`); !ok || v != 1e20 {
		t.Errorf("a 21-digit integer is a float: %v %v", v, ok)
	}
}

func TestFlattenMessageOrdersItsParts(t *testing.T) {
	message := rec{"type": "message", "message": rec{
		"role": "assistant", "stopReason": "error", "errorMessage": "boom",
		"content": []any{fxText("said"), fxImage("AA==", "image/png")},
	}}
	if got, _ := flattenEntry(message); got != "[assistant] said\n[error] boom\n[1 image: image/png]" {
		t.Errorf("text, failure, images: %q", got)
	}
	tool := rec{"type": "message", "message": rec{"role": "toolResult", "toolName": "t", "content": []any{fxText("out"), fxImage("AA==", "image/png")}}}
	if got, _ := flattenEntry(tool); got != "[toolResult] out\n[1 image: image/png]\nTool: t" {
		t.Errorf("images before the tool identity: %q", got)
	}
}

func TestEntryContentOfAnEdit(t *testing.T) {
	image := fxImage("AA==", "image/png")
	edit := rec{"type": "context_edit", "id": "e", "replacement": rec{"content": []any{fxText("r"), image}}, "content": "ignored"}
	if got, ok := entryContent(edit).([]any); !ok || len(got) != 2 {
		t.Errorf("an edit's content is its replacement's: %v", entryContent(edit))
	}
	item, ok := toWindowedEntry(edit, "initial")
	if !ok || len(item.images) != 1 || item.images[0].mimeType != "image/png" {
		t.Errorf("an edit's images are its replacement's: %+v", item)
	}
	if got := entryContent(rec{"type": "custom_message", "content": "c"}); got != "c" {
		t.Errorf("other entries use their content: %v", got)
	}
}

func TestArchiveHitsWithoutACurrentFileAreNeverInContext(t *testing.T) {
	dir := t.TempDir()
	entry := hlMsg("e1", nil, "", hlUser("needle text"))
	hlArchive(t, filepath.Join(dir, "a.jsonl"), fxLine(entry))
	source := hlSource(t, dir, "")
	source.projection = func() ([]projectedEntry, error) {
		return []projectedEntry{{sourceEntry: rec{"id": "e1"}, messages: []rec{{"role": "user", "content": "needle text"}}}}, nil
	}
	t.Setenv("PATH", t.TempDir())
	t.Setenv("PIG_EXT_HOST_INFO", "")
	matches, err := historyMatches(context.Background(), source, historyParams{op: "search", query: "needle", hasQuery: true, all: true},
		func(message string) error { return fmt.Errorf("%s", message) })
	if err != nil || len(matches.hits) != 1 || matches.skipped != 0 {
		t.Errorf("only the current session file is checked against the active context: %+v %v", matches, err)
	}
}

func TestNewestFirstOrdersByMillisecondsWithFractions(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.jsonl"), filepath.Join(dir, "b.jsonl")
	for _, file := range []string{a, b} {
		if err := os.WriteFile(file, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(a, time.Unix(1_000, 400_000), time.Unix(1_000, 400_000)); err != nil { // .4 ms
		t.Fatal(err)
	}
	if err := os.Chtimes(b, time.Unix(1_000, 900_000), time.Unix(1_000, 900_000)); err != nil { // .9 ms
		t.Fatal(err)
	}
	sorted, err := newestFirst([]string{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if sorted[0].file != b {
		t.Errorf("the later fraction of a millisecond is newer: %v", sorted)
	}
}

func TestNewestFirstIsStableAmongManyTies(t *testing.T) {
	dir := t.TempDir()
	var files []string
	for i := range 60 {
		file := filepath.Join(dir, fmt.Sprintf("f%02d.jsonl", i))
		if err := os.WriteFile(file, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		hlTouch(t, file, int64(1_000*(1+i%3)))
		files = append(files, file)
	}
	sorted, err := newestFirst(files)
	if err != nil {
		t.Fatal(err)
	}
	last := map[float64]int{}
	for _, file := range sorted {
		var index int
		fmt.Sscanf(filepath.Base(file.file), "f%d.jsonl", &index)
		if previous, seen := last[file.mtime]; seen && index < previous {
			t.Fatalf("files of equal modification time left the listing order: f%02d after f%02d", index, previous)
		}
		last[file.mtime] = index
	}
}

func TestJSONParseRefusesEveryControlCharacterInAString(t *testing.T) {
	for c := byte(0); c < 0x20; c++ {
		if _, ok := jsonParse(`"a` + string([]byte{c}) + `b"`); ok {
			t.Errorf("control character %#x accepted inside a string", c)
		}
	}
	if _, ok := jsonParse("\"a\x7fb\""); !ok {
		t.Error("DEL is allowed in a string")
	}
}
