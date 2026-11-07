package posthorse

// Twins of the history cases of test/posthorse.test.ts (the ledger file "posthorse"): cases 14-22,
// 28, 29, 36, 37 and 50-58, driven through the fake host and the real extension.
//
// Adaptations (each case keeps its inputs and expectations):
//   - Promise.all of independent calls (cases 14, 29) runs as a loop: a fixture holds its host for
//     the whole call.
//   - A signal aborted before the call (case 22) is staged by calling historyMatches with an
//     already cancelled context: the fake host cannot deliver a cancel frame before the handler
//     starts (fixture_test.go, model item 4).
//   - PATH="" and PI_CODING_AGENT_DIR (case 56) become t.Setenv("PATH", ""): the original runs
//     ripgrep through child_process, here os/exec reads the same PATH; the managed binary of
//     getAgentDir() is absent in both.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func hlWD(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return wd
}

// hlFixture is the original's setup(): the extension on the fake host with ctx.cwd = process.cwd().
func hlFixture(t *testing.T, opts ...fixOpt) *fixture {
	t.Helper()
	return newFixture(t, append([]fixOpt{withCwd(hlWD(t))}, opts...)...)
}

// hlMissingDir is `join(tmpdir(), "missing")`, the session dir of cases that must not scan one.
func hlMissingDir() string { return filepath.Join(os.TempDir(), "missing") }

func hlArchive(t *testing.T, file, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fxWriteArchive(file, content); err != nil {
		t.Fatal(err)
	}
}

func hlLines(entries ...rec) string {
	lines := make([]string, len(entries))
	for i, entry := range entries {
		lines[i] = fxLine(entry)
	}
	return strings.Join(lines, "\n")
}

func hlTouch(t *testing.T, file string, ms int64) {
	t.Helper()
	when := time.UnixMilli(ms)
	if err := os.Chtimes(file, when, when); err != nil {
		t.Fatal(err)
	}
}

func hlMsg(id string, parent any, ts string, message rec) rec {
	entry := rec{"type": "message", "id": id, "parentId": parent, "message": message}
	if ts != "" {
		entry["timestamp"] = ts
	}
	return entry
}

func hlUser(content any) rec { return rec{"role": "user", "content": content} }

func hlMustMatch(t *testing.T, pattern, text string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(text) {
		t.Errorf("text does not match /%s/:\n%s", pattern, hlShort(text))
	}
}

func hlMustNotMatch(t *testing.T, pattern, text string) {
	t.Helper()
	if regexp.MustCompile(pattern).MatchString(text) {
		t.Errorf("text matches /%s/:\n%s", pattern, hlShort(text))
	}
}

func hlMustContain(t *testing.T, text, part string) {
	t.Helper()
	if !strings.Contains(text, part) {
		t.Errorf("text does not contain %q:\n%s", part, hlShort(text))
	}
}

func hlMustNotContain(t *testing.T, text, part string) {
	t.Helper()
	if strings.Contains(text, part) {
		t.Errorf("text contains %q:\n%s", part, hlShort(text))
	}
}

func reflectEqual(a, b any) bool { return reflect.DeepEqual(a, b) }

// base64Of is `Buffer.from(text).toString("base64")`.
func base64Of(text string) string { return base64.StdEncoding.EncodeToString([]byte(text)) }

// hlSource is the historySource of an archive search over dir, for calls made without the host.
func hlSource(t *testing.T, dir, currentFile string) historySource {
	t.Helper()
	return historySource{
		dir: dir, cwd: absPath(hlWD(t)), currentFile: currentFile,
		branch:     func() ([]entryLike, error) { return nil, nil },
		projection: func() ([]projectedEntry, error) { return nil, nil },
	}
}

func hlShort(text string) string {
	if len(text) > 1500 {
		return text[:1500] + "…"
	}
	return text
}

func hlEqual(t *testing.T, got, want any, what string) {
	t.Helper()
	if !reflectEqual(got, want) {
		t.Errorf("%s:\n got  %#v\n want %#v", what, got, want)
	}
}

var windowIDPattern = regexp.MustCompile(`\[window [^\]]+\] \[([^\]]+)\]`)

// hlIDs is `[...text.matchAll(/\[window [^\]]+\] \[([^\]]+)\]/g)].map(m => m[1])`.
func hlIDs(text string) []string {
	var ids []string
	for _, m := range windowIDPattern.FindAllStringSubmatch(text, -1) {
		ids = append(ids, m[1])
	}
	return ids
}

// hlLineIDs is `text.split("\n").map(line => line.match(re)?.[1])`; "" is undefined.
func hlLineIDs(text string, re *regexp.Regexp) []string {
	lines := strings.Split(text, "\n")
	ids := make([]string, len(lines))
	for i, line := range lines {
		if m := re.FindStringSubmatch(line); m != nil {
			ids[i] = m[1]
		}
	}
	return ids
}

var initialWindowID = regexp.MustCompile(`\[window initial\] \[([^\]]+)\]`)

func hlDetails(t *testing.T, res fixResult) rec {
	t.Helper()
	if res.Details == nil {
		t.Fatal("result has no details")
	}
	return res.Details
}

var moreCursor = regexp.MustCompile(`\[More results; continue with cursor "([^"]+)"`)

func hlSearch(f *fixture, params rec) string {
	f.g.t.Helper()
	params["op"] = "search"
	return f.Run("history", params).Text()
}

func hlWantError(t *testing.T, f *fixture, params rec, pattern string) {
	t.Helper()
	_, failure := f.RunErr("history", params)
	if failure == "" {
		t.Errorf("history %v succeeded, want an error matching /%s/", params, pattern)
		return
	}
	hlMustMatch(t, pattern, failure)
}

func hlBranchEntries(entries []rec) []rec {
	out := make([]rec, len(entries))
	for i, entry := range entries {
		out[i] = fxClone(entry)
		out[i]["timestamp"] = strconv.Itoa(i)
		if i == 0 {
			out[i]["parentId"] = nil
		} else {
			out[i]["parentId"] = entries[i-1]["id"]
		}
	}
	return out
}

func TestTwinPosthorseHistory(t *testing.T) {
	tw(t, "posthorse", "history reads current entries without opening every archived session", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "unrelated.jsonl"), 0o755); err != nil {
			t.Fatal(err)
		}
		f := hlFixture(t, withSessionDir(dir), withSession(fxIndexedSession(
			hlMsg("current", nil, "1", hlUser("keep me")))))
		// Promise.all of five reads: a fixture holds its host for a whole call, so they run in turn.
		for range 5 {
			hlMustMatch(t, `keep me`, f.Run("history", rec{"op": "read", "id": "current"}).Text())
		}
	})

	tw(t, "posthorse", "all-session history recurses and returns newest matching entries first", func(t *testing.T) {
		dir := t.TempDir()
		oldFile, newFile := filepath.Join(dir, "old.jsonl"), filepath.Join(dir, "subagents", "new.jsonl")
		hlArchive(t, oldFile, fxLine(hlMsg("old", nil, "1", hlUser("recursive needle old"))))
		hlArchive(t, newFile, hlLines(
			rec{"type": "context_window", "id": "nested-window", "parentId": nil, "timestamp": "2", "handoff": "resume"},
			hlMsg("nested-old", "nested-window", "3", hlUser("recursive needle nested old")),
			hlMsg("nested-new", "nested-old", "4", hlUser("recursive needle nested newest")),
		))
		hlTouch(t, oldFile, 1_000)
		hlTouch(t, newFile, 2_000)
		f := hlFixture(t, withSessionDir(dir))
		f.ResetReads()
		text := hlSearch(f, rec{"query": "recursive needle", "all": true, "limit": 2})
		hlMustMatch(t, `(?s)^4 \[subagent\] \[window nested-window\] \[nested-new@[\w-]{10}\] .+nested newest`, text)
		hlMustNotMatch(t, `new\.jsonl`, text)
		nestedNew := fxArchivedID("nested-new", "subagents/new.jsonl", false)
		hlMustContain(t, text, "["+nestedNew+"]")
		if strings.Index(text, "nested-new") >= strings.Index(text, "nested-old") {
			t.Errorf("nested-new must precede nested-old:\n%s", text)
		}
		hlMustNotContain(t, text, "["+fxArchivedID("old", "old.jsonl", false)+"]")
		// all-session search must not normalize the current branch
		if reads := f.Reads("sessionRead.getBranch") + f.Reads("watchSessionLog"); reads != 0 {
			t.Errorf("all-session search read the current branch %d times", reads)
		}
		read := f.Run("history", rec{"op": "read", "id": nestedNew}).Text()
		hlMustMatch(t, `(?s)^4 \[subagent\] \[window nested-window\] .+nested newest`, read)
		legacy := f.Run("history", rec{"op": "read", "id": fxArchivedID("nested-new", "subagents/new.jsonl", true)}).Text()
		hlMustMatch(t, `nested newest`, legacy)
	})

	tw(t, "posthorse", "history keeps the selected session after a cwd override but excludes foreign and unidentified archives", func(t *testing.T) {
		dir := t.TempDir()
		entry := func(id, content string) string { return fxLine(hlMsg(id, nil, "", hlUser(content))) }
		header := func(cwd string) string {
			h := rec{"type": "session", "id": "fixture"}
			if cwd != "" {
				h["cwd"] = cwd
			}
			return fxLine(h)
		}
		write := func(name, content string) {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		current := filepath.Join(dir, "current.jsonl")
		write("current.jsonl", header(filepath.Join(dir, "prior-cwd"))+"\n"+entry("current", "scope needle current"))
		write("same.jsonl", "invalid prefix\nnull\n"+header(hlWD(t))+"\n"+entry("same", "scope needle same"))
		write("foreign.jsonl", header(filepath.Join(dir, "foreign"))+"\n"+entry("foreign", "scope needle foreign"))
		write("legacy.jsonl", header("")+"\n"+entry("legacy", "scope needle legacy"))
		f := hlFixture(t, withSessionDir(dir), withSessionFile(current))
		hits := hlSearch(f, rec{"query": "scope needle", "all": true})
		hlMustMatch(t, `\[user\] scope needle current`, hits)
		hlMustMatch(t, `\[user\] scope needle same`, hits)
		hlMustNotMatch(t, `\[user\] scope needle (foreign|legacy)`, hits)
		hlMustMatch(t, `scope needle current`, f.Run("history", rec{"op": "read", "id": fxArchivedID("current", "current.jsonl", false)}).Text())
		for _, source := range []string{"foreign", "legacy"} {
			hlWantError(t, f, rec{"op": "read", "id": fxArchivedID(source, source+".jsonl", false)}, `No history entry`)
		}
		hlWantError(t, f, rec{"op": "read", "id": "same@"}, `No history entry`)
		hlWantError(t, f, rec{"op": "read", "id": "21:45:25.912Z"}, `Pass an id exactly as history search prints it, such as 1a2f07de`)
	})

	tw(t, "posthorse", "history searches normalized text and reports native window ids", func(t *testing.T) {
		branch := []rec{
			hlMsg("needle-only-in-id", nil, "1", hlUser("plain")),
			hlMsg("before", "needle-only-in-id", "2", hlUser("needle before")),
			{"type": "context_window", "id": "window-2", "parentId": "before", "timestamp": "3", "handoff": "continue"},
			hlMsg("after", "window-2", "4", rec{"role": "assistant", "content": "needle after"}),
			hlMsg("long", "after", "5", hlUser(strings.Repeat("x", 40_100)+"needle tail")),
		}
		f := hlFixture(t, withSession(fxIndexedSession(branch...)), withEmptyProjection(), withSessionDir(hlMissingDir()))
		text := hlSearch(f, rec{"query": "needle"})
		hlMustNotMatch(t, `needle-only-in-id`, text)
		if strings.Index(text, "[long]") >= strings.Index(text, "[after]") {
			t.Errorf("[long] must precede [after]:\n%s", hlShort(text))
		}
		hlMustMatch(t, `\[window initial\] \[before\]`, text)
		hlMustMatch(t, `\[window window-2\] \[after\]`, text)
		first := f.Run("history", rec{"op": "read", "id": "long"}).Text()
		hlMustMatch(t, `offset 40000`, first)
		second := f.Run("history", rec{"op": "read", "id": "long", "offset": 40_000}).Text()
		hlMustMatch(t, `needle tail`, second)
	})

	tw(t, "posthorse", "history search skips entries already in the active context and says how many", func(t *testing.T) {
		branch := []rec{
			hlMsg("earlier", nil, "1", hlUser("needle from an earlier window")),
			{"type": "context_window", "id": "window-2", "parentId": "earlier", "timestamp": "2", "handoff": "needle handoff"},
			hlMsg("current", "window-2", "3", hlUser("needle already visible")),
			{"type": "context_edit", "id": "edit", "parentId": "current", "timestamp": "4", "targetId": "current", "replacement": rec{"content": "needle replacement visible"}},
			{"type": "custom_message", "id": "reminder", "parentId": "edit", "timestamp": "5", "customType": "posthorse-reminder", "content": "needle checkpoint", "display": true},
		}
		sess := fxIndexedSession(branch...)
		// Pi projects the edited entry under its own id with the replacement text, as the model sees it. Pi also
		// projects checkpoint reminders that Posthorse may strip from model input.
		f := hlFixture(t, withSession(sess), withSessionDir(hlMissingDir()), withProjection(
			rec{"sourceEntry": branch[1], "messages": []any{rec{"role": "custom", "content": "Context window window-2 starts here.\n\nHandoff from the previous window:\nneedle handoff"}}},
			rec{"sourceEntry": branch[2], "messages": []any{rec{"role": "user", "content": "needle replacement visible"}}},
			rec{"sourceEntry": branch[4], "messages": []any{rec{"role": "custom", "content": "needle checkpoint"}}},
		))
		res := f.Run("history", rec{"op": "search", "query": "needle"})
		text := res.Text()
		hlMustMatch(t, `\[earlier\] \[user\] needle from an earlier window`, text)
		// The model sees only the replacement, so the original stays searchable even though "needle" survived the edit.
		hlMustMatch(t, `\[current\] \[user\] needle already visible`, text)
		hlMustMatch(t, `\[reminder\] \[custom:posthorse-reminder\] needle checkpoint`, text)
		hlMustNotMatch(t, `replacement visible|needle handoff`, text)
		hlMustMatch(t, `\n\[Skipped 2 matches already in your active context\.\]$`, text)
		d := hlDetails(t, res)
		hlEqual(t, []any{d["kind"], d["skipped"], d["more"]}, []any{"history-search", float64(2), false}, "details")

		// An edit that keeps the text but drops an image leaves the image recoverable.
		shot := rec{"type": "message", "id": "shot", "parentId": nil, "timestamp": "6",
			"message": rec{"role": "user", "content": []any{fxText("keep this text"), fxImage("AA==", "image/png")}}}
		trimmed := f.With(
			withSession(fxIndexedSession(shot, rec{"type": "context_edit", "id": "shot-edit", "parentId": "shot", "timestamp": "7", "targetId": "shot", "replacement": rec{"content": "keep this text"}})),
			withProjection(rec{"sourceEntry": shot, "messages": []any{rec{"role": "user", "content": "keep this text"}}}),
		)
		image := hlSearch(trimmed, rec{"query": "image/png"})
		hlMustMatch(t, `\[shot\] \[user\] keep this text\n\[1 image: image/png\]`, image)
		hlMustNotMatch(t, `Skipped`, image)
	})

	tw(t, "posthorse", "history ranks matching original content before recovery and lookup echoes without hiding either", func(t *testing.T) {
		image := fxImage("UE5HREFUQQ==", "image/png")
		raw := []rec{
			{"type": "message", "id": "owner", "message": rec{"role": "user", "content": "needle original request"}},
			{"type": "message", "id": "assistant", "message": rec{"role": "assistant", "content": "needle original response"}},
			{"type": "message", "id": "result", "message": rec{"role": "toolResult", "toolName": "read", "content": "needle original output"}},
			{"type": "context_window", "id": "window", "handoff": "needle checkpoint"},
			{"type": "compaction", "id": "summary", "summary": "needle summary"},
			{"type": "branch_summary", "id": "branch-summary", "summary": "needle branch summary"},
			{"type": "custom_message", "id": "reminder", "customType": "posthorse-reminder", "content": "needle reminder"},
			{"type": "message", "id": "notes", "message": rec{"role": "toolResult", "toolName": "notes", "content": "needle saved note"}},
			{"type": "message", "id": "lookup", "message": rec{"role": "toolResult", "toolName": "history", "content": []any{fxText("needle recovered output"), image}}},
			{"type": "message", "id": "rollover", "message": rec{"role": "assistant", "content": []any{
				rec{"type": "toolCall", "name": "new_context", "arguments": rec{"handoff": "needle"}}}}},
			{"type": "message", "id": "mixed-call", "message": rec{"role": "assistant", "content": []any{
				rec{"type": "toolCall", "name": "notes", "arguments": rec{"op": "write", "content": "needle ECHO " + strings.Repeat("x", 500)}},
				rec{"type": "toolCall", "name": "bash", "arguments": rec{"command": "needle MATCHING SIBLING"}}}}},
			{"type": "message", "id": "mixed-prose", "message": rec{"role": "assistant", "content": []any{
				fxText("needle MATCHING PROSE"), fxText("second line"),
				rec{"type": "toolCall", "name": "history", "arguments": rec{"op": "search", "query": "needle ECHO " + strings.Repeat("x", 500)}}}}},
			{"type": "message", "id": "mixed-echo", "message": rec{"role": "assistant", "content": []any{
				rec{"type": "toolCall", "name": "history", "arguments": rec{"op": "search", "query": "needle lookup-only"}},
				fxText("ordinary prose does not match")}}},
			{"type": "message", "id": "current-search", "message": rec{"role": "assistant", "content": []any{
				rec{"type": "toolCall", "name": "history", "arguments": rec{"op": "search", "query": "[assistant] needle MATCHING PROSE"}}}}},
		}
		f := hlFixture(t, withSession(fxIndexedSession(hlBranchEntries(raw)...)), withEmptyProjection(), withSessionDir(hlMissingDir()))
		search := func(query string, limit int) string {
			return f.Run("history", rec{"op": "search", "query": query, "limit": limit}).Text()
		}
		all := search("NEEDLE", 50)
		allIDs := hlIDs(all)
		hlEqual(t, allIDs, []string{"mixed-prose", "mixed-call", "result", "assistant", "owner", "current-search", "mixed-echo",
			"rollover", "lookup", "notes", "reminder", "branch-summary", "summary", "window"}, "ranking")
		originals := search("needle", 5)
		hlEqual(t, hlIDs(originals), allIDs[:5], "originals")
		hlMustMatch(t, `MATCHING PROSE`, originals)
		hlMustMatch(t, `MATCHING SIBLING`, originals)
		hlMustNotMatch(t, `ECHO`, originals)
		hlEqual(t, hlIDs(search("needle", 1)), []string{"mixed-prose"}, "limit 1")
		hlEqual(t, hlIDs(search("needle MATCHING PROSE\nsecond line", 1)), []string{"mixed-prose"}, "multi-line query")
		hlEqual(t, hlIDs(search("[assistant] needle MATCHING PROSE", 1)), []string{"mixed-prose"}, "assistant prefix query")
		hlEqual(t, hlIDs(search("lookup-only", 50)), []string{"mixed-echo"}, "lookup-only")
		hlMustMatch(t, `\[window window\] \[mixed-call\]`, all)
		read := f.Run("history", rec{"op": "read", "id": "mixed-call"}).Text()
		hlMustMatch(t, `needle ECHO`, read)
		hlMustMatch(t, `MATCHING SIBLING`, read)
		recovered := f.Run("history", rec{"op": "read", "id": "lookup"})
		hlMustMatch(t, `needle recovered output`, recovered.Text())
		hlEqual(t, recovered.Images(), []rec{image}, "recovered images")
	})

	tw(t, "posthorse", "archived history preserves Unicode separators across chunks and an unterminated final record", func(t *testing.T) {
		dir := t.TempDir()
		body := strings.Repeat("x", 66_000) + "unicode needle\u2028line\u2029paragraph"
		entries := []rec{
			{"type": "context_window", "id": "unicode-window", "parentId": nil, "handoff": "prior\u2028checkpoint"},
			{"type": "message", "id": "unicode", "parentId": "unicode-window", "message": hlUser(body)},
			{"type": "message", "id": "last", "parentId": "unicode", "message": hlUser("final needle\u2029tail")},
		}
		lines := make([]string, len(entries))
		for i, entry := range entries {
			// JSON.stringify keeps U+2028 and U+2029 raw.
			lines[i] = strings.NewReplacer(`\u2028`, "\u2028", `\u2029`, "\u2029").Replace(fxLine(entry))
		}
		hlArchive(t, filepath.Join(dir, "session.jsonl"), strings.Join(lines, "\r\n"))
		f := hlFixture(t, withSessionDir(dir))
		hits := hlSearch(f, rec{"query": "needle", "all": true})
		hlMustContain(t, hits, "[window unicode-window] ["+fxArchivedID("unicode", "session.jsonl", false)+"]")
		hlMustContain(t, hits, "[window unicode-window] ["+fxArchivedID("last", "session.jsonl", false)+"]")
		read := f.Run("history", rec{"op": "read", "id": "unicode", "offset": 66_000}).Text()
		if want := u16slice(body, 66_000-len("[user] "), u16end); !strings.HasSuffix(read, want) {
			t.Errorf("read does not end with the body tail %q: …%q", want, read[max(0, len(read)-80):])
		}
		last := f.Run("history", rec{"op": "read", "id": "last"}).Text()
		if !strings.HasSuffix(last, "[user] final needle\u2029tail") {
			t.Errorf("last read = %q", last)
		}
	})

	tw(t, "posthorse", "history preserves assistant failures and ranks them ahead of recovery echoes", func(t *testing.T) {
		branch := []rec{
			{"type": "message", "id": "failed", "message": rec{"role": "assistant", "content": []any{}, "stopReason": "error", "errorMessage": "HTTP 429 QUOTA_EXCEEDED"}},
			{"type": "message", "id": "partial", "message": rec{"role": "assistant", "content": []any{
				fxText("Partial response"),
				rec{"type": "toolCall", "name": "history", "arguments": rec{"op": "search", "query": "prior state"}}},
				"stopReason": "aborted", "errorMessage": "QUOTA_EXCEEDED while streaming"}},
			{"type": "message", "id": "echo", "message": rec{"role": "toolResult", "toolName": "notes", "content": "QUOTA_EXCEEDED copied into notes"}},
		}
		f := hlFixture(t, withSession(fxIndexedSession(branch...)), withEmptyProjection(), withSessionDir(hlMissingDir()))
		failed := f.Run("history", rec{"op": "read", "id": "failed"}).Text()
		if !strings.HasSuffix(failed, "[assistant] [error] HTTP 429 QUOTA_EXCEEDED") {
			t.Errorf("failed read = %q", failed)
		}
		partial := f.Run("history", rec{"op": "read", "id": "partial"}).Text()
		hlMustMatch(t, `Partial response`, partial)
		hlMustMatch(t, `\[aborted\] QUOTA_EXCEEDED while streaming`, partial)
		hit := f.Run("history", rec{"op": "search", "query": "QUOTA_EXCEEDED", "limit": 1}).Text()
		hlMustMatch(t, `\[partial\] \[assistant\] \[aborted\] QUOTA_EXCEEDED while streaming`, hit)
		hlMustNotMatch(t, `\[echo\]`, hit)
	})

	tw(t, "posthorse", "all-session ranking keeps older originals ahead of newer echoes before applying the result limit", func(t *testing.T) {
		dir := t.TempDir()
		old := rec{"type": "message", "id": "old", "message": hlUser("needle oldest original")}
		newer := rec{"type": "message", "id": "newer", "message": rec{"role": "toolResult", "toolName": "bash", "content": "needle newer original"}}
		echoes := make([]rec, 6)
		for i := range echoes {
			echoes[i] = rec{"type": "message", "id": "echo-" + strconv.Itoa(i),
				"message": rec{"role": "toolResult", "toolName": "history", "content": "needle echo-only " + strconv.Itoa(i)}}
		}
		for i, entries := range [][]rec{{old}, append([]rec{newer}, echoes...), {echoes[5]}} {
			file := filepath.Join(dir, strconv.Itoa(i)+".jsonl")
			hlArchive(t, file, hlLines(entries...))
			hlTouch(t, file, int64(i+1)*1000)
		}
		f := hlFixture(t, withSessionDir(dir))
		search := func(query string, limit int) string {
			return f.Run("history", rec{"op": "search", "query": query, "all": true, "limit": limit}).Text()
		}
		id := func(name, file string) string { return fxArchivedID(name, file, false) }
		ids := func(text string) []string {
			var out []string
			for _, m := range initialWindowID.FindAllStringSubmatch(text, -1) {
				out = append(out, m[1])
			}
			return out
		}
		hlEqual(t, ids(search("needle", 1)), []string{id("newer", "1.jsonl")}, "limit 1")
		hlEqual(t, ids(search("needle", 2)), []string{id("newer", "1.jsonl"), id("old", "0.jsonl")}, "limit 2")
		hlEqual(t, ids(search("needle", 4)), []string{id("newer", "1.jsonl"), id("old", "0.jsonl"), id("echo-5", "2.jsonl"), id("echo-5", "1.jsonl")}, "limit 4")
		all := search("needle", 50)
		want := []string{id("newer", "1.jsonl"), id("old", "0.jsonl"), id("echo-5", "2.jsonl")}
		for i := len(echoes) - 1; i >= 0; i-- {
			want = append(want, id(echoes[i]["id"].(string), "1.jsonl"))
		}
		hlEqual(t, ids(all), want, "all")
		found := false
		for _, line := range strings.Split(all, "\n") {
			if strings.Contains(line, "["+id("echo-5", "2.jsonl")+"]") && !strings.Contains(line, "[subagent]") {
				found = true
			}
		}
		if !found {
			t.Errorf("no top-level echo-5 line without [subagent]:\n%s", hlShort(all))
		}
		hlMustNotMatch(t, `\d\.jsonl`, all)
		hlEqual(t, ids(search("echo-only", 2)), []string{id("echo-5", "2.jsonl"), id("echo-5", "1.jsonl")}, "echo-only")
		// A signal aborted before the call: see the file header.
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		source := hlSource(t, dir, "")
		_, err := historyMatches(ctx, source, historyParams{op: "search", query: "needle", hasQuery: true, all: true},
			func(message string) error { return errors.New(message) })
		if !errors.Is(err, context.Canceled) {
			t.Errorf("aborted search error = %v, want a cancellation", err)
		}
	})

	tw(t, "posthorse", "history returns stored images for a requested entry and summarizes them elsewhere", func(t *testing.T) {
		png := fxImage("UE5HREFUQQ==", "image/png")
		jpeg := fxImage("SlBFR0RBVEE=", "image/jpeg")
		webp := fxImage("V0VCUERBVEE=", "image/webp")
		branch := []rec{
			{"type": "message", "id": "img-user", "parentId": nil, "timestamp": "1", "message": hlUser([]any{png})},
			{"type": "message", "id": "img-tool", "parentId": "img-user", "timestamp": "2", "message": rec{"role": "toolResult", "toolName": "screenshot", "content": []any{jpeg}}},
			{"type": "message", "id": "mixed", "parentId": "img-tool", "timestamp": "3", "message": hlUser([]any{fxText("look at this"), webp, fxText("and fix it")})},
		}
		f := hlFixture(t, withSession(fxIndexedSession(branch...)), withEmptyProjection(), withSessionDir(hlMissingDir()))
		user := f.Run("history", rec{"op": "read", "id": "img-user"})
		hlMustMatch(t, `\[user\] \[1 image: image/png\]`, user.Content[0]["text"].(string))
		hlEqual(t, user.Content[1:], []rec{png}, "user images")
		tool := f.Run("history", rec{"op": "read", "id": "img-tool"})
		hlMustMatch(t, `\[toolResult\] \[1 image: image/jpeg\]`, tool.Content[0]["text"].(string))
		hlEqual(t, tool.Content[1:], []rec{jpeg}, "tool images")
		mixed := f.Run("history", rec{"op": "read", "id": "mixed"})
		hlMustMatch(t, `look at this\nand fix it\n\[1 image: image/webp\]`, mixed.Content[0]["text"].(string))
		hlEqual(t, mixed.Content[1:], []rec{webp}, "mixed images")
		later := f.Run("history", rec{"op": "read", "id": "mixed", "offset": 5})
		if len(later.Content) != 1 {
			t.Errorf("images ride along with the first page only: %d parts", len(later.Content))
		}

		longImage := fxIndexedSession(rec{"type": "message", "id": "long-image", "parentId": nil, "timestamp": "4",
			"message": hlUser([]any{fxText(strings.Repeat("z", 25_000)), png})})
		tight := f.With(withUsage(100_000, 82_117), withSession(longImage))
		hlWantError(t, tight, rec{"op": "read", "id": "long-image"}, `Too little context remains`)

		search := hlSearch(f, rec{"query": "image/"})
		hlMustMatch(t, `\[img-tool\] \[toolResult\] \[1 image: image/jpeg\]`, search)
		hlMustMatch(t, `\[img-user\] \[user\] \[1 image: image/png\]`, search)
		hlMustNotMatch(t, `UE5HREFUQQ|SlBFR0RBVEE`, search)

		handoff := f.AutomaticHandoff(branch)
		hlMustMatch(t, `\[owner input \| 1 \| entry img-user\]\n\[1 image: image/png\] — recover with history read id img-user`, handoff)
		hlMustMatch(t, `look at this\nand fix it\n\[1 image: image/webp\] — recover with history read id mixed`, handoff)
		hlMustNotMatch(t, `UE5HREFUQQ|SlBFR0RBVEE|V0VCUERBVEE=`, handoff)
	})

	tw(t, "posthorse", "history image continuations preserve order, text, and retry offsets", func(t *testing.T) {
		for _, text := range []string{"short", strings.Repeat("x", 25_000)} {
			images := make([]rec, 13)
			for i := range images {
				images[i] = fxImage(base64Of("image "+strconv.Itoa(i)), "image/png")
			}
			content := []any{fxText(text)}
			for _, image := range images {
				content = append(content, image)
			}
			f := hlFixture(t, withUsage(32_000, 1_000), withEmptyProjection(), withSessionDir(hlMissingDir()),
				withSession(fxIndexedSession(rec{"type": "message", "id": "images", "message": hlUser(content)})))
			offset, imageOffset := 0, 0
			recoveredText := ""
			var recoveredImages []rec
			for page := 0; page < 30; page++ {
				f.Fire("turn_start", rec{})
				// The next page depends on this receipt's text and image continuation offsets.
				res := f.Run("history", rec{"op": "read", "id": "images", "offset": offset, "imageOffset": imageOffset})
				d := hlDetails(t, res)
				if d["kind"] != "history-read" {
					t.Fatalf("details kind = %v", d["kind"])
				}
				headerLength, end, off := int(d["headerLength"].(float64)), int(d["end"].(float64)), int(d["offset"].(float64))
				recoveredText += u16slice(res.Text(), headerLength, headerLength+end-off)
				recoveredImages = append(recoveredImages, res.Content[1:]...)
				next := regexp.MustCompile(`and offset (\d+) and imageOffset (\d+)\.`).FindStringSubmatch(res.Text())
				if next == nil {
					break
				}
				offset, _ = strconv.Atoi(next[1])
				imageOffset, _ = strconv.Atoi(next[2])
				prevImage := 0
				if v, ok := d["imageOffset"].(float64); ok {
					prevImage = int(v)
				}
				if !(offset > off || imageOffset > prevImage) {
					t.Fatalf("page %d does not advance: offset %d image offset %d", page, offset, imageOffset)
				}
			}
			if want := "[user] " + text + "\n[13 images: image/png]"; recoveredText != want {
				t.Errorf("recovered text = %.80q (%d), want %.80q (%d)", recoveredText, len(recoveredText), want, len(want))
			}
			hlEqual(t, recoveredImages, images, "recovered images")
			hlWantError(t, f, rec{"op": "read", "id": "images", "imageOffset": 14}, `Image offset 14 is past the end`)
			tightView := f.With(withUsage(32_000, 14_117))
			hlWantError(t, tightView, rec{"op": "read", "id": "images", "offset": 5, "imageOffset": 4}, `retry with offset 5 and imageOffset 4`)
		}
	})

	tw(t, "posthorse", "history cursors finish partial headers and progress past growing lookup echoes", func(t *testing.T) {
		sess := fxIndexedSession(
			rec{"type": "message", "id": "old", "timestamp": "1", "message": hlUser("needle oldest")},
			rec{"type": "message", "id": "long", "parentId": "old", "timestamp": strings.Repeat("T", 4000), "message": hlUser("needle long metadata")},
			rec{"type": "message", "id": "echo", "parentId": "long", "message": rec{"role": "toolResult", "toolName": "history", "content": "needle prior lookup"}},
		)
		f := hlFixture(t, withUsage(100_000, 98_700), withReserve(16_384), withEnabled(false), withEmptyProjection(),
			withSessionDir(hlMissingDir()), withSession(sess)).PageContext()
		cursor := ""
		recovered := ""
		var returned []string
		pages := 0
		for ; pages < 20; pages++ {
			f.Fire("turn_start", rec{})
			args := rec{"op": "search", "query": "needle"}
			if cursor != "" {
				args["cursor"] = cursor
			}
			sess.push(rec{"type": "message", "id": "call-" + strconv.Itoa(pages), "message": rec{"role": "assistant", "content": []any{
				rec{"type": "toolCall", "name": "history", "arguments": args}}}})
			// Persist this lookup echo and obtain its cursor before appending the next lookup.
			call := rec{"op": "search", "query": "needle", "limit": 1}
			if cursor != "" {
				call["cursor"] = cursor
			}
			res := f.Run("history", call)
			output, body, id := historySearchPage(t, res)
			returned = append(returned, id)
			if id == "old" || id == "long" {
				recovered += regexp.MustCompile(`^\[entry [^;]+; from char \d+\] `).ReplaceAllString(body, "")
			}
			sess.push(rec{"type": "message", "id": "result-" + strconv.Itoa(pages), "message": rec{"role": "toolResult", "toolName": "history", "content": output}})
			m := regexp.MustCompile(`\[More results; continue with cursor "([^"]+)" and the same query/scope\.\]$`).FindStringSubmatch(output)
			next := ""
			if m != nil {
				next = m[1]
			}
			if next == cursor && next != "" {
				t.Fatalf("every continued page advances: cursor %q repeated", next)
			}
			if next == "" {
				break
			}
			cursor = next
		}
		if pages >= 20 {
			t.Fatalf("new echoes must not keep the search alive forever: %v", returned)
		}
		hlMustContain(t, recovered, strings.Repeat("T", 4000)+" [window initial] [long] [user] needle long metadata")
		if n := strings.Count(recovered, "[old] [user] needle oldest"); n != 1 {
			t.Errorf("oldest recovered %d times", n)
		}
		count := func(want string) (n int) {
			for _, id := range returned {
				if id == want {
					n++
				}
			}
			return
		}
		if count("echo") != 1 || count("old") != 1 {
			t.Errorf("echo returned %d times, old %d times: %v", count("echo"), count("old"), returned)
		}
		for _, params := range []rec{
			{"query": "different", "cursor": cursor},
			{"query": "needle", "cursor": cursor, "all": true},
			{"query": "needle", "cursor": "not-json"},
		} {
			f.Fire("turn_start", rec{})
			// Each invalid-cursor call follows its own turn reservation reset.
			params["op"] = "search"
			hlWantError(t, f, params, `Invalid history cursor`)
		}
	})

	tw(t, "posthorse", "all-session cursors retain ranking and per-file entries across pages", func(t *testing.T) {
		dir := t.TempDir()
		original := rec{"type": "message", "id": "shared", "message": hlUser("needle shared ancestor")}
		older := []rec{original, {"type": "message", "id": "older", "message": hlUser("needle older source")}}
		current := []rec{original,
			{"type": "message", "id": "newer", "message": hlUser("needle newer source")},
			{"type": "message", "id": "prior-echo", "message": rec{"role": "toolResult", "toolName": "notes", "content": "needle prior echo"}},
		}
		oldFile, activeFile := filepath.Join(dir, "old.jsonl"), filepath.Join(dir, "current.jsonl")
		hlArchive(t, oldFile, hlLines(older...))
		f := hlFixture(t, withSessionDir(dir), withNoSessionFile())
		cursor := ""
		var ids []string
		for page := 0; page < 20; page++ {
			f.Fire("turn_start", rec{})
			args := rec{"op": "search", "query": "needle"}
			if cursor != "" {
				args["cursor"] = cursor
			}
			current = append(current, rec{"type": "message", "id": "lookup-" + strconv.Itoa(page), "message": rec{"role": "assistant", "content": []any{
				rec{"type": "toolCall", "name": "history", "arguments": args}}}})
			hlArchive(t, activeFile, hlLines(current...))
			hlTouch(t, oldFile, 1000)
			hlTouch(t, activeFile, int64(2000+page))
			// Write this page's lookup echo before reopening archives with its returned cursor.
			call := rec{"op": "search", "query": "needle", "limit": 1, "all": true}
			if cursor != "" {
				call["cursor"] = cursor
			}
			text := f.Run("history", call).Text()
			m := regexp.MustCompile(`^[^\n]*?\[window [^\]]+\] \[([^\]]+)\]`).FindStringSubmatch(text)
			if m == nil {
				t.Fatalf("page %d has no entry id:\n%s", page, hlShort(text))
			}
			ids = append(ids, m[1])
			current = append(current, rec{"type": "message", "id": "result-" + strconv.Itoa(page), "message": rec{"role": "toolResult", "toolName": "history", "content": text}})
			next := regexp.MustCompile(`\[More results; continue with cursor "([^"]+)" and the same query/scope\.\]$`).FindStringSubmatch(text)
			if next == nil {
				break
			}
			cursor = next[1]
		}
		id := func(name, file string) string { return fxArchivedID(name, file, false) }
		if len(ids) < 3 {
			t.Fatalf("ids = %v", ids)
		}
		hlEqual(t, ids[:3], []string{id("newer", "current.jsonl"), id("shared", "current.jsonl"), id("older", "old.jsonl")}, "first pages")
		seen := map[string]bool{}
		for _, one := range ids {
			if seen[one] {
				t.Errorf("duplicate id %s in %v", one, ids)
			}
			seen[one] = true
		}
		if !seen[id("shared", "old.jsonl")] {
			t.Errorf("copied entries keep distinct file references: %v", ids)
		}
		if !seen[id("prior-echo", "current.jsonl")] {
			t.Errorf("pagination reaches the oldest echo and terminates: %v", ids)
		}
		if len(ids) >= 20 {
			t.Errorf("search did not terminate: %d pages", len(ids))
		}
	})

	tw(t, "posthorse", "history flattens bashExecution entries and honors excludeFromContext", func(t *testing.T) {
		branch := []rec{
			{"type": "message", "id": "sh", "parentId": nil, "timestamp": "1", "message": rec{"role": "bashExecution", "command": "ls -la", "output": "total 0\nnotes.md"}},
			{"type": "message", "id": "hidden", "parentId": "sh", "timestamp": "2", "message": rec{"role": "bashExecution", "command": "cat token", "output": "TOPSECRET", "excludeFromContext": true}},
		}
		f := hlFixture(t, withSession(fxIndexedSession(branch...)), withEmptyProjection(), withSessionDir(hlMissingDir()))
		hlMustMatch(t, `\[sh\] \[bashExecution\] \$ ls -la\ntotal 0\nnotes\.md`, hlSearch(f, rec{"query": "notes.md"}))
		hlMustMatch(t, `\[bashExecution\] \$ ls -la\ntotal 0\nnotes\.md$`, f.Run("history", rec{"op": "read", "id": "sh"}).Text())
		hlMustMatch(t, `No history matches`, hlSearch(f, rec{"query": "TOPSECRET"}))
		hlMustMatch(t, `No history matches`, hlSearch(f, rec{"query": "cat token"}))
		hidden := f.Run("history", rec{"op": "read", "id": "hidden"}).Text()
		hlMustMatch(t, `\[bashExecution\] \(excluded from model context by Pi\)$`, hidden)
		hlMustNotMatch(t, `TOPSECRET|cat token`, hidden)
	})

	tw(t, "posthorse", "all-session search returns fork copies with their source-qualified references", func(t *testing.T) {
		dir := t.TempDir()
		original, fork, grandchild := filepath.Join(dir, "original.jsonl"), filepath.Join(dir, "fork.jsonl"), filepath.Join(dir, "grandchild.jsonl")
		shared := fxLine(hlMsg("shared", nil, "", hlUser("fork needle shared")))
		forkNew := fxLine(hlMsg("fork-new", "shared", "", hlUser("fork needle newer")))
		hlArchive(t, original, shared)
		hlArchive(t, fork, strings.Join([]string{shared, forkNew}, "\n"))
		hlArchive(t, grandchild, strings.Join([]string{shared, forkNew, fxLine(hlMsg("grand-new", "fork-new", "", hlUser("fork needle latest")))}, "\n"))
		hlTouch(t, original, 1_000)
		hlTouch(t, fork, 2_000)
		hlTouch(t, grandchild, 3_000)
		f := hlFixture(t, withSessionDir(dir))
		text := hlSearch(f, rec{"query": "fork needle", "all": true, "limit": 10})
		hlEqual(t, hlLineIDs(text, initialWindowID), []string{
			fxArchivedID("grand-new", "grandchild.jsonl", false),
			fxArchivedID("fork-new", "grandchild.jsonl", false),
			fxArchivedID("shared", "grandchild.jsonl", false),
			fxArchivedID("fork-new", "fork.jsonl", false),
			fxArchivedID("shared", "fork.jsonl", false),
			fxArchivedID("shared", "original.jsonl", false),
		}, "fork copies")
	})

	tw(t, "posthorse", "all-session history reads the searched entry when independent sessions reuse an entry id", func(t *testing.T) {
		dir := t.TempDir()
		for _, c := range []struct {
			name, content string
			time          int64
		}{{"older", "needle approved requirement", 1_000}, {"newer", "needle unrelated request", 2_000}} {
			file := filepath.Join(dir, c.name+".jsonl")
			stamp := time.UnixMilli(c.time).UTC().Format("2006-01-02T15:04:05.000Z")
			header := rec{"type": "session", "version": float64(3), "id": "session-" + c.name, "timestamp": stamp, "cwd": hlWD(t)}
			err := fxWriteSession(file, header,
				hlMsg("deadbeef", nil, stamp, hlUser(c.content)),
				hlMsg("feedface", "deadbeef", time.UnixMilli(3_000).UTC().Format("2006-01-02T15:04:05.000Z"),
					hlUser([]any{rec{"type": "image", "mimeType": "image/png", "data": base64Of(c.name)}})))
			if err != nil {
				t.Fatal(err)
			}
			hlTouch(t, file, c.time)
		}
		f := hlFixture(t, withSessionDir(dir))
		search := func(query, cursor string) string {
			params := rec{"op": "search", "query": query, "all": true, "limit": 1}
			if cursor != "" {
				params["cursor"] = cursor
			}
			return f.Run("history", params).Text()
		}
		older := search("approved", "")
		id := windowIDPattern.FindStringSubmatch(older)
		if id == nil || id[1] != fxArchivedID("deadbeef", "older.jsonl", false) {
			t.Fatalf("older id = %v in\n%s", id, older)
		}
		hlMustMatch(t, `\[user\] needle approved requirement$`, f.Run("history", rec{"op": "read", "id": id[1]}).Text())

		first := search("needle", "")
		hlMustMatch(t, `^\S+ \[window initial\] \[`+regexp.QuoteMeta(fxArchivedID("deadbeef", "newer.jsonl", false))+`\] \[user\] needle unrelated request`, first)
		cursor := moreCursor.FindStringSubmatch(first)
		if cursor == nil {
			t.Fatal("the older session must not be hidden by a colliding id")
		}
		second := search("needle", cursor[1])
		hlMustMatch(t, `\[user\] needle approved requirement`, second)
		hlMustContain(t, second, "["+id[1]+"]") // the paged hit must use the same read reference

		images := hlSearch(f, rec{"query": "image/png", "all": true})
		hlMustContain(t, images, "["+fxArchivedID("feedface", "newer.jsonl", false)+"]")
		hlMustContain(t, images, "["+fxArchivedID("feedface", "older.jsonl", false)+"]")
		olderImage := f.Run("history", rec{"op": "read", "id": fxArchivedID("feedface", "older.jsonl", false)})
		hlEqual(t, olderImage.Content[1:], []rec{{"type": "image", "mimeType": "image/png", "data": base64Of("older")}}, "older image")
	})

	tw(t, "posthorse", "all-session history keeps identical entries from unrelated sessions separate", func(t *testing.T) {
		dir := t.TempDir()
		for _, c := range []struct {
			name string
			time int64
		}{{"first", 1_000}, {"second", 2_000}} {
			file := filepath.Join(dir, c.name+".jsonl")
			header := rec{"type": "session", "version": float64(3), "id": "session-" + c.name, "timestamp": time.UnixMilli(c.time).UTC().Format("2006-01-02T15:04:05.000Z"), "cwd": hlWD(t)}
			if err := fxWriteSession(file, header, hlMsg("deadbeef", nil, "1970-01-01T00:00:03.000Z", hlUser("needle repeated request"))); err != nil {
				t.Fatal(err)
			}
			hlTouch(t, file, c.time)
		}
		f := hlFixture(t, withSessionDir(dir))
		hits := hlSearch(f, rec{"query": "needle", "all": true})
		hlMustContain(t, hits, "["+fxArchivedID("deadbeef", "first.jsonl", false)+"]")
		hlMustContain(t, hits, "["+fxArchivedID("deadbeef", "second.jsonl", false)+"]")
	})

	tw(t, "posthorse", "all-session history keeps new entries in sibling forks separate", func(t *testing.T) {
		dir := t.TempDir()
		root := filepath.Join(dir, "root.jsonl")
		stamp := func(ms int64) string { return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z") }
		rootHeader := rec{"type": "session", "version": float64(3), "id": "session-root", "timestamp": stamp(0), "cwd": hlWD(t)}
		ancestor := hlMsg("ancestor", nil, stamp(500), hlUser("start"))
		repeated := hlMsg("deadbeef", "ancestor", stamp(3_000), hlUser("needle same request"))
		if err := fxWriteSession(root, rootHeader, ancestor); err != nil {
			t.Fatal(err)
		}
		hlTouch(t, root, 500)
		for _, c := range []struct {
			name string
			time int64
		}{{"first", 1_000}, {"second", 2_000}} {
			file := filepath.Join(dir, c.name+".jsonl")
			header := rec{"type": "session", "version": float64(3), "id": "session-" + c.name, "parentSession": root, "timestamp": stamp(c.time), "cwd": hlWD(t)}
			if err := fxWriteSession(file, header, ancestor, repeated); err != nil {
				t.Fatal(err)
			}
			hlTouch(t, file, c.time)
		}
		if err := fxWriteSession(root, rootHeader, ancestor, repeated); err != nil {
			t.Fatal(err)
		}
		hlTouch(t, root, 4_000)
		f := hlFixture(t, withSessionDir(dir))
		hits := hlSearch(f, rec{"query": "needle", "all": true})
		hlMustContain(t, hits, "["+fxArchivedID("deadbeef", "first.jsonl", false)+"]")
		hlMustContain(t, hits, "["+fxArchivedID("deadbeef", "second.jsonl", false)+"]")
		hlMustContain(t, hits, "["+fxArchivedID("deadbeef", "root.jsonl", false)+"]")
	})

	tw(t, "posthorse", "all-session history cursors fit the declared limit for nested session files", func(t *testing.T) {
		dir := t.TempDir()
		source := filepath.Join(strings.Repeat("a", 180), strings.Repeat("b", 180), "newest.jsonl")
		nested := filepath.Join(dir, strings.Repeat("a", 180), strings.Repeat("b", 180))
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct {
			file, id string
			time     int64
		}{{filepath.Join(nested, "newest.jsonl"), "newest", 2_000}, {filepath.Join(dir, "older.jsonl"), "older", 1_000}} {
			hlArchive(t, c.file, fxLine(hlMsg(c.id, nil, "", hlUser("needle"))))
			hlTouch(t, c.file, c.time)
		}
		f := hlFixture(t, withSessionDir(dir))
		first := hlSearch(f, rec{"query": "needle", "all": true, "limit": 1})
		hlMustContain(t, first, "["+fxArchivedID("newest", source, false)+"]")
		cursor := moreCursor.FindStringSubmatch(first)
		if cursor == nil {
			t.Fatal("no continuation cursor")
		}
		if len(cursor[1]) > 512 {
			t.Errorf("cursor is %d characters", len(cursor[1]))
		}
		next := hlSearch(f, rec{"query": "needle", "all": true, "limit": 1, "cursor": cursor[1]})
		hlMustContain(t, next, "["+fxArchivedID("older", "older.jsonl", false)+"]")
		hlMustContain(t, first, "[subagent]")
		hlMustNotContain(t, next, "[subagent]")
		read := f.Run("history", rec{"op": "read", "id": fxArchivedID("newest", source, false)}).Text()
		hlMustContain(t, read, "[subagent] [window initial] ["+fxArchivedID("newest", source, false)+"]")
		if !strings.HasSuffix(read, "[user] needle") {
			t.Errorf("read = %q", read)
		}
	})

	tw(t, "posthorse", "all-session search finds the same entries with or without ripgrep, including text raw JSONL cannot show", func(t *testing.T) {
		dir := t.TempDir()
		archive := func(name, content string) {
			hlArchive(t, filepath.Join(dir, name+".jsonl"), fxLine(hlMsg(name, nil, "", hlUser(content))))
		}
		archive("quoted", `say "needle" twice`)
		archive("plain", "a plain needle")
		archive("other", "nothing here")
		archive("dotted", "İSTANBUL")
		archive("lone", "half \xed\xa0\xbd moon") // "half \ud83d moon": a lone surrogate
		hlArchive(t, filepath.Join(dir, "bare.jsonl"), fxLine(rec{"type": "context_window", "id": "bare", "parentId": nil}))
		f := hlFixture(t, withSessionDir(dir))
		hits := func(query any) []string {
			text := hlSearch(f, rec{"query": query, "all": true})
			var ids []string
			for _, m := range regexp.MustCompile(`\[window \S+\] \[([^@\]]+)@`).FindAllStringSubmatch(text, -1) {
				ids = append(ids, m[1])
			}
			sort.Strings(ids)
			return ids
		}
		// Session JSONL stores quotes and lone surrogates escaped, "No handoff" exists only in the
		// rendered entry, and İ lowercases to two characters, so a plain raw-text prefilter would
		// drop these files.
		// The lone surrogate crosses the wire as the JSON escape JS would send; Go's encoder would turn the
		// raw WTF-8 bytes into U+FFFD.
		queries := []any{"needle", `"needle"`, "No handoff", "İstanbul", json.RawMessage(`"half \ud83d"`)}
		expected := [][]string{{"plain", "quoted"}, {"quoted"}, {"bare"}, {"dotted"}, {"lone"}}
		for i, query := range queries {
			hlEqual(t, hits(query), expected[i], "with ripgrep: "+fmt.Sprint(query))
		}
		t.Setenv("PATH", "")
		for i, query := range queries {
			// without ripgrep every session file is scanned
			hlEqual(t, hits(query), expected[i], "without ripgrep: "+fmt.Sprint(query))
		}
	})

	tw(t, "posthorse", "all-session search still finds text that exists only in normalized entries", func(t *testing.T) {
		dir := t.TempDir()
		image := func(mimeType string, set bool) rec {
			m := rec{"type": "image", "data": "AA=="}
			if set {
				m["mimeType"] = mimeType
			}
			return m
		}
		// One entry per flattenEntry branch, so every label it adds is exercised. Ids, headers, and paths carry
		// no label words or digits, so a query can only reach an entry's file through the entry itself.
		entries := []rec{
			{"type": "message", "id": "pix", "message": rec{"role": "user", "content": []any{
				fxText("two shots"), image("image/png", true), image("image/jpeg", true), image("", true), image("", false)}}},
			{"type": "message", "id": "invoke", "message": rec{"role": "assistant", "content": []any{
				rec{"type": "toolCall", "id": "toolu_a", "name": "record", "namespace": "lab", "arguments": rec{"target": "a"}, "executionArguments": rec{"target": "b"}}},
				"stopReason": "error", "errorMessage": "quota hit"}},
			{"type": "message", "id": "outcome", "message": rec{"role": "toolResult", "toolName": "record", "namespace": "lab", "toolCallId": "toolu_a",
				"content": []any{fxText("done"), image("image/png", true)}}},
			{"type": "message", "id": "sh", "message": rec{"role": "bashExecution", "command": "ls", "output": "notes.md"}},
			{"type": "message", "id": "quiet", "message": rec{"role": "bashExecution", "command": "cat secret", "output": "x", "excludeFromContext": true}},
			{"type": "compaction", "id": "digest", "summary": "summarized"},
			{"type": "branch_summary", "id": "fork", "summary": "branched"},
			{"type": "custom_message", "id": "note", "customType": "intercom_message", "content": []any{fxText("ping"), image("image/png", true)}},
			{"type": "context_window", "id": "fresh"},
			{"type": "context_window", "id": "handed", "handoff": "carry on"},
			{"type": "custom_message", "id": "plain", "content": "hush"},
			{"type": "context_edit", "id": "trim", "targetId": "outcome", "replacement": rec{"content": "trimmed"}},
		}
		for _, entry := range entries {
			// Pi's key order (type, id, parentId, timestamp, then the fields), which lets scans skip lines
			// without parsing them.
			rest := fxClone(entry)
			delete(rest, "type")
			delete(rest, "id")
			line := `{"type":` + fxStringify(entry["type"]) + `,"id":` + fxStringify(entry["id"]) + `,"parentId":null,"timestamp":"t"`
			if len(rest) == 0 {
				line += "}"
			} else {
				line += "," + fxStringify(rest)[1:]
			}
			if err := os.WriteFile(filepath.Join(dir, entry["id"].(string)+".jsonl"),
				[]byte(fxLine(rec{"type": "session", "id": "s", "cwd": "/"})+"\n"+line), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		f := hlFixture(t, withCwd("/"), withSessionDir(dir))
		var missed []string
		for _, entry := range entries {
			name := entry["id"].(string)
			id := fxArchivedID(name, name+".jsonl", false)
			// Process one archive at a time; its read establishes the queries scanned below.
			read := f.Run("history", rec{"op": "read", "id": id}).Text()
			chars := strings.Index(read, "[chars ")
			normalized := jsToLowerCase(read[strings.Index(read[chars:], "] ")+chars+2:])
			raw := jsToLowerCase(fxLine(entry))
			for _, query := range normalizedOnlyQueries(normalized, raw) {
				// Limit archive scans and their ripgrep children to one query at a time.
				hits := f.Run("history", rec{"op": "search", "query": query, "all": true, "limit": 50}).Text()
				if !strings.Contains(hits, "["+id+"]") {
					missed = append(missed, name+": "+strconv.Quote(query))
				}
			}
		}
		if len(missed) != 0 {
			t.Errorf("a ripgrep prefilter must never drop a normalized match: %v", missed)
		}
	})

	tw(t, "posthorse", "session search and archived reads preserve modification-time ordering including ties", func(t *testing.T) {
		dir := t.TempDir()
		for _, c := range []struct {
			name  string
			mtime int64
		}{{"z", 1000}, {"m", 2000}, {"a", 2000}} {
			file := filepath.Join(dir, c.name+".jsonl")
			hlArchive(t, file, hlLines(
				rec{"type": "message", "id": "shared", "message": hlUser("shared ancestor")},
				rec{"type": "message", "id": c.name, "parentId": "shared", "message": hlUser("mtime needle")},
			))
			hlTouch(t, file, c.mtime)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		type named struct {
			name  string
			mtime time.Time
		}
		var expected []named
		for _, entry := range entries {
			info, err := entry.Info()
			if err != nil {
				t.Fatal(err)
			}
			expected = append(expected, named{entry.Name(), info.ModTime()})
		}
		sort.SliceStable(expected, func(a, b int) bool { return expected[a].mtime.After(expected[b].mtime) })
		f := hlFixture(t, withSessionDir(dir))
		search := hlSearch(f, rec{"query": "mtime needle", "all": true})
		var want []string
		for _, file := range expected {
			want = append(want, fxArchivedID(strings.TrimSuffix(file.name, ".jsonl"), file.name, false))
		}
		hlEqual(t, hlLineIDs(search, initialWindowID), want, "ordering")
		read := f.Run("history", rec{"op": "read", "id": "shared"}).Text()
		// a bare id resolves in the newest session and prints its qualified id
		hlMustContain(t, read, "["+fxArchivedID("shared", expected[0].name, false)+"]")
	})
}

// historySearchPage is historySearchPage of test/posthorse.test.ts:3098.
func historySearchPage(t *testing.T, result fixResult) (output, body, id string) {
	t.Helper()
	output = result.Text()
	if n := u16len(output); n > 1200 {
		t.Errorf("page of %d characters exceeds the declared limit", n)
	}
	d := hlDetails(t, result)
	if d["kind"] != "history-search" {
		t.Fatalf("details kind = %v", d["kind"])
	}
	entries, _ := arrOf(d["entries"])
	footer := 0
	if v, ok := d["footerLength"].(float64); ok {
		footer = int(v)
	}
	sum := -1
	for _, item := range entries {
		span, _ := recOf(item)
		length, _ := num(span, "length")
		header, _ := num(span, "headerLength")
		sum += int(length) + 1
		if header < 0 || header > length {
			t.Errorf("span %v has a header beyond its length", span)
		}
	}
	if sum != u16len(output)-footer {
		t.Errorf("spans cover %d characters, output minus footer is %d", sum, u16len(output)-footer)
	}
	body = u16slice(output, 0, u16len(output)-footer)
	if m := regexp.MustCompile(`^\[entry ([^;]+); from char \d+\]`).FindStringSubmatch(body); m != nil {
		id = m[1]
	} else if m := windowIDPattern.FindStringSubmatch(body); m != nil {
		id = m[1]
	}
	if id == "" {
		t.Fatalf("every partial page keeps a usable native entry id:\n%s", output)
	}
	return output, body, id
}

var normalizedWord = regexp.MustCompile(`[\p{L}\p{N}_./-]+`)

// normalizedOnlyQueries is normalizedOnlyQueries of test/posthorse.test.ts:4534.
func normalizedOnlyQueries(normalized, raw string) []string {
	words := normalizedWord.FindAllStringIndex(normalized, -1)
	var queries []string
	for first, word := range words {
		for last := first; last < min(len(words), first+3); last++ {
			query := normalized[word[0]:words[last][1]]
			if !strings.Contains(raw, query) {
				queries = append(queries, query)
			}
		}
	}
	return queries
}
