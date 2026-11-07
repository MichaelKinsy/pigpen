package posthorse

// Twins of the notes tool and note guard cases of test/posthorse.test.ts (lane C2).

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// notesRealDir is `realpathSync(mkdtempSync(...))`.
func notesRealDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func notesMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func notesWrite(t *testing.T, path string, content string) {
	t.Helper()
	notesMust(t, os.MkdirAll(filepath.Dir(path), 0o755))
	notesMust(t, os.WriteFile(path, []byte(content), 0o644))
}

// notesTouch is utimesSync(path, at, at).
func notesTouch(t *testing.T, path string, at time.Time) {
	t.Helper()
	notesMust(t, os.Chtimes(path, at, at))
}

// notesPageOf reads details.page of a notes list/search result: offset, end, total.
func notesPageOf(t *testing.T, details rec) (offset, end, total int) {
	t.Helper()
	page, ok := details["page"].(map[string]any)
	if !ok {
		t.Fatalf("details has no page: %v", details)
	}
	return int(page["offset"].(float64)), int(page["end"].(float64)), int(page["total"].(float64))
}

// notesWalkHook installs noteStatHook for one test and restores it.
func notesWalkHook(t *testing.T, hook func(path string)) {
	t.Helper()
	previous := noteStatHook
	noteStatHook = hook
	t.Cleanup(func() { noteStatHook = previous })
}

func TestTwinPosthorseNotes(t *testing.T) {
	// Posthorse test 34 (posthorse.test.ts:2932).
	tw(t, "posthorse", "notes list and search page every result through the shared budget, including oversized paths", func(t *testing.T) {
		dir := notesRealDir(t)
		f := newFixture(t)
		page := f.With(withUsage(100_000, 98_750), withReserve(16_384), withEnabled(false), withCwd(dir)).PageContext()
		root := filepath.Join(dir, ".pi", "notes")
		nested := strings.Join([]string{strings.Repeat("d", 180), strings.Repeat("d", 180), strings.Repeat("d", 180), strings.Repeat("d", 180)}, "/")
		notesMust(t, os.MkdirAll(filepath.Join(root, nested), 0o755))
		files := make([]string, 0, 121)
		for index := range 120 {
			files = append(files, fmt.Sprintf("%03d-%s.md", index, strings.Repeat("n", 180)))
		}
		files = append(files, nested+"/long-"+strings.Repeat("f", 70)+".md")
		modified := func(index int) time.Time {
			return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(len(files)-index) * time.Minute)
		}
		for index, path := range files {
			notesWrite(t, filepath.Join(root, path), "needle sample "+strings.Repeat("s", 180))
			notesTouch(t, filepath.Join(root, path), modified(index))
		}
		for _, op := range []string{"list", "search"} {
			expectedRows := make([]string, len(files))
			for index, path := range files {
				if op == "list" {
					expectedRows[index] = fmt.Sprintf("%s  194B  %sZ", path, modified(index).Format("2006-01-02T15:04"))
				} else {
					expectedRows[index] = path + ":1: needle sample " + strings.Repeat("s", 180)
				}
			}
			expected := strings.Join(expectedRows, "\n")
			header := ""
			if op == "list" {
				header = "Notes directory: " + root + "\n"
			}
			offset := 0
			recovered := ""
			for range 100 {
				page.Fire("turn_start", rec{})
				// Each page supplies the exact next offset and completes the current turn reservation.
				result := page.Run("notes", rec{"op": op, "query": "needle", "offset": float64(offset)})
				display := result.Details
				if kind := display["kind"]; kind != "notes-list" && kind != "notes-search" {
					t.Fatalf("details kind %v", kind)
				}
				if display["page"] == nil {
					t.Fatal("list/search must expose continuation metadata")
				}
				_, end, total := notesPageOf(t, display)
				text := result.Text()
				if !strings.HasPrefix(text, header) {
					t.Fatalf("every listing page identifies the shared directory: %.80q", text)
				}
				if u16len(text) > 1000 {
					t.Fatalf("headers and continuation fit the admitted page: %d", u16len(text))
				}
				if display["more"] == true {
					// Search stops reading once the page is decided, so its total is a lower bound.
					if !(op == "search" && end < total && total <= len(expected)) {
						t.Fatalf("more matches: op=%s end=%d total=%d", op, end, total)
					}
					if !strings.Contains(text, "; more matches follow; continue with offset") {
						t.Fatalf("no more-matches footer: %q", text[len(text)-80:])
					}
				} else if total != len(expected) {
					t.Fatalf("total %d, want %d", total, len(expected))
				}
				position := 0
				count := 0
				for _, row := range expectedRows {
					if position < end && position+len(row) > offset {
						count++
					}
					position += len(row) + 1
				}
				if got := int(display["count"].(float64)); got != count {
					t.Fatalf("count describes only row portions actually returned: %d, want %d", got, count)
				}
				if op == "search" && count > 20 {
					t.Fatalf("search page holds %d rows", count)
				}
				recovered += u16slice(text, u16len(header), u16len(header)+end-offset)
				if end <= offset {
					t.Fatalf("page makes no progress: offset %d end %d", offset, end)
				}
				if end == total {
					break
				}
				if !strings.Contains(text, fmt.Sprintf("continue with offset %d", end)) {
					t.Fatalf("no continuation at %d", end)
				}
				offset = end
			}
			if recovered != expected {
				t.Fatalf("%s: a result tail is lost even when a single path spans pages (%d vs %d chars)", op, len(recovered), len(expected))
			}
		}
	})

	// Posthorse test 38 (posthorse.test.ts:3336); the TS case is skipped on win32.
	tw(t, "posthorse", "note replacement keeps the existing checkpoint on a real file-size failure", func(t *testing.T) {
		runNoteWriteFailure(t)
	})

	// Posthorse test 43 (posthorse.test.ts:3640).
	tw(t, "posthorse", "notes list and search follow aliases while skipping cycles and missing targets", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("directory symlinks need privileges on Windows; the upstream suite does not run there")
		}
		dir := notesRealDir(t)
		notesDir := filepath.Join(dir, ".pi", "notes")
		notesWrite(t, filepath.Join(notesDir, "nested", "state.md"), "checkpoint")
		notesMust(t, os.Symlink("nested", filepath.Join(notesDir, "alias")))
		notesMust(t, os.Symlink("..", filepath.Join(notesDir, "nested", "back")))
		notesMust(t, os.Symlink("deleted.md", filepath.Join(notesDir, "missing.md")))
		notesMust(t, os.Symlink("deleted-directory", filepath.Join(notesDir, "missing-directory")))
		f := newFixture(t).With(withCwd(dir))

		listing := f.Run("notes", rec{"op": "list"}).Text()
		if first := strings.SplitN(listing, "\n", 2)[0]; first != "Notes directory: "+notesDir {
			t.Fatalf("header %q", first)
		}
		rows := strings.SplitN(listing, "\n", 2)[1]
		if !regexp.MustCompile(`^alias/state\.md  10B  \S+\nnested/state\.md  10B  \S+$`).MatchString(rows) {
			t.Fatalf("listing rows %q", rows)
		}
		if got := f.Run("notes", rec{"op": "search", "query": "checkpoint"}).Text(); got != "alias/state.md:1: checkpoint\nnested/state.md:1: checkpoint" {
			t.Fatalf("search %q", got)
		}
	})

	// Posthorse test 44 (posthorse.test.ts:3698). The TS case mocks fs.promises.stat; the Go walk
	// reports each completed stat through noteStatHook.
	tw(t, "posthorse", "notes list and search skip entries removed while an earlier folder is walked", func(t *testing.T) {
		dir := notesRealDir(t)
		root := filepath.Join(dir, ".pi", "notes")
		notesWrite(t, filepath.Join(root, "a-many", "first.md"), "needle in a-many")
		f := newFixture(t).With(withCwd(dir))
		// Another session's cleanup removes later entries once the walk is inside a-many.
		cleanupDuring := func(params rec) string {
			notesWrite(t, filepath.Join(root, "z-doomed", "note.md"), "needle in a removed folder")
			notesWrite(t, filepath.Join(root, "z-doomed.md"), "needle in a removed note")
			var once sync.Once
			removed := false
			notesWalkHook(t, func(path string) {
				if path == filepath.Join(root, "a-many", "first.md") {
					once.Do(func() {
						notesMust(t, os.RemoveAll(filepath.Join(root, "z-doomed")))
						notesMust(t, os.Remove(filepath.Join(root, "z-doomed.md")))
						removed = true
					})
				}
			})
			result := f.Run("notes", params)
			if !removed {
				t.Fatal("the cleanup ran during the walk")
			}
			return result.Text()
		}
		if got := cleanupDuring(rec{"op": "list"}); !regexp.MustCompile(`^Notes directory: .+\na-many/first\.md {2}16B {2}\S+$`).MatchString(got) {
			t.Fatalf("list %q", got)
		}
		if got := cleanupDuring(rec{"op": "search", "query": "needle"}); got != "a-many/first.md:1: needle in a-many" {
			t.Fatalf("search %q", got)
		}
	})

	// Posthorse test 45 (posthorse.test.ts:3737).
	tw(t, "posthorse", "notes list, folder reads and search let Pi keep running and stop when cancelled mid-walk", func(t *testing.T) {
		notesWalkCancellation(t)
	})

	// Posthorse test 46 (posthorse.test.ts:3798).
	tw(t, "posthorse", "notes list newest first with sizes, read folders as listings, and search only text notes", func(t *testing.T) {
		dir := notesRealDir(t)
		root := filepath.Join(dir, ".pi", "notes")
		files := []struct {
			path    string
			content string
			time    int64
		}{
			{"old.md", "needle old", 1_000},
			{"task/fixture.sqlite", "SQLite format 3\x00needle inside binary", 2_000},
			{"task/current.md", "needle current", 3_000},
		}
		for _, file := range files {
			notesWrite(t, filepath.Join(root, file.path), file.content)
			notesTouch(t, filepath.Join(root, file.path), time.UnixMilli(file.time))
		}
		f := newFixture(t).With(withCwd(dir))
		notes := func(params rec) string { return f.Run("notes", params).Text() }
		want := strings.Join([]string{
			"Notes directory: " + root,
			"task/current.md  14B  1970-01-01T00:00Z",
			"task/fixture.sqlite  36B  1970-01-01T00:00Z",
			"old.md  10B  1970-01-01T00:00Z",
		}, "\n")
		if got := notes(rec{"op": "list"}); got != want {
			t.Fatalf("list %q, want %q", got, want)
		}
		folder := notes(rec{"op": "list", "path": "task"})
		if rows := strings.SplitN(folder, "\n", 2)[1]; !regexp.MustCompile(`^task/current\.md .+\ntask/fixture\.sqlite .+$`).MatchString(rows) {
			t.Fatalf("folder rows %q", rows)
		}
		if got := notes(rec{"op": "read", "path": "task"}); got != folder {
			t.Fatalf("reading a folder lists it instead of failing: %q vs %q", got, folder)
		}
		if got := notes(rec{"op": "search", "query": "needle"}); got != "task/current.md:1: needle current\nold.md:1: needle old" {
			t.Fatalf("search %q", got)
		}
		_, message := f.RunErr("notes", rec{"op": "read", "path": "task/fixture.sqlite"})
		if !regexp.MustCompile(`binary file \(36B\); notes read returns text only`).MatchString(message) {
			t.Fatalf("binary read error %q", message)
		}
	})

	// Posthorse test 47 (posthorse.test.ts:3856).
	tw(t, "posthorse", "notes search stops reading at the requested page, skips notes over 1 MiB and still pages every match", func(t *testing.T) {
		dir := t.TempDir()
		root := filepath.Join(dir, ".pi", "notes")
		oldest := filepath.Join(root, "oldest.md")
		at := func(minutes int) time.Time {
			return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(minutes) * time.Minute)
		}
		notesWrite(t, filepath.Join(root, "huge.log"), "needle in a log\n"+strings.Repeat("l", 1024*1024))
		notesTouch(t, filepath.Join(root, "huge.log"), at(1000))
		rows := make([]string, 300)
		for index := range rows {
			name := fmt.Sprintf("note-%03d.md", index)
			line := fmt.Sprintf("needle %d %s", index, strings.Repeat("n", 150))
			notesWrite(t, filepath.Join(root, name), line+"\n")
			notesTouch(t, filepath.Join(root, name), at(900-index))
			rows[index] = name + ":1: " + line
		}
		notesWrite(t, oldest, "the needle in the oldest note")
		notesTouch(t, oldest, at(0))
		expected := strings.Join(append(rows, "oldest.md:1: the needle in the oldest note"), "\n")
		header := "[Not searched: 1 note over 1 MiB.]\n"
		f := newFixture(t).With(withCwd(dir))
		search := func(query string, offset int) (fixResult, string) {
			f.Fire("turn_start", rec{})
			return f.RunErr("notes", rec{"op": "search", "query": query, "offset": float64(offset)})
		}

		// Root reads mode-000 files, so only an unprivileged run shows that a full scan fails here.
		if runtime.GOOS != "windows" && os.Getuid() != 0 {
			notesMust(t, os.Chmod(oldest, 0o000))
			_, failure := search("oldest", 0)
			if !strings.Contains(failure, "EACCES") {
				t.Fatalf("a full scan reaches the oldest note: %q", failure)
			}
		}
		first, failure := search("needle", 0)
		if failure != "" {
			t.Fatal(failure)
		}
		if first.Details["kind"] != "notes-search" || first.Details["page"] == nil {
			t.Fatalf("details %v", first.Details)
		}
		if first.Details["more"] != true {
			t.Fatal("first page must report more matches")
		}
		_, end, _ := notesPageOf(t, first.Details)
		want := fmt.Sprintf("%s%s\n[chars 0-%d; more matches follow; continue with offset %d]", header, u16slice(expected, 0, end), end, end)
		if first.Text() != want {
			t.Fatalf("first page %q", first.Text())
		}
		if _, failure := search("needle", 1_000_000); !strings.Contains(failure, "search for something more specific") {
			t.Fatalf("offset limit: %q", failure)
		}
		notesMust(t, os.Chmod(oldest, 0o644))

		offset := 0
		recovered := ""
		for range 100 {
			// Each page continues from the offset the previous page returned.
			result, failure := search("needle", offset)
			if failure != "" {
				t.Fatal(failure)
			}
			text := result.Text()
			if result.Details["kind"] != "notes-search" || result.Details["page"] == nil {
				t.Fatalf("details %v", result.Details)
			}
			if !strings.HasPrefix(text, header) {
				t.Fatal("every page says which notes were not searched")
			}
			_, pageEnd, total := notesPageOf(t, result.Details)
			recovered += u16slice(text, u16len(header), u16len(header)+pageEnd-offset)
			if result.Details["more"] != true && pageEnd == total {
				break
			}
			offset = pageEnd
		}
		if recovered != expected {
			t.Fatal("paging recovers every match from every searched note")
		}
	})

	// Posthorse test 49 (posthorse.test.ts:3990).
	tw(t, "posthorse", "long notes page fully, empty writes clear, appends stay separated, and search centers on the match", func(t *testing.T) {
		dir := notesRealDir(t)
		f := newFixture(t).With(withCwd(dir))
		notes := func(params rec) string { return f.Run("notes", params).Text() }
		failure := func(params rec) string {
			_, message := f.RunErr("notes", params)
			return message
		}
		read := func(path string) string {
			bytes, err := os.ReadFile(filepath.Join(dir, ".pi", "notes", path))
			notesMust(t, err)
			return string(bytes)
		}
		header := "File: " + filepath.Join(dir, ".pi", "notes", "big.md") + "\n"
		pageChars := 40_000 - len(header)
		body := strings.Repeat("a", 40_000) + strings.Repeat("b", 40_000) + strings.Repeat("c", 5_000)
		notes(rec{"op": "write", "path": "big.md", "content": body})
		want := fmt.Sprintf("%s%s\n[chars 0-%d of 85000; continue with offset %d]", header, body[:pageChars], pageChars, pageChars)
		if got := notes(rec{"op": "read", "path": "big.md"}); got != want {
			t.Fatalf("first page differs (%d chars)", len(got))
		}
		want = fmt.Sprintf("%s%s\n[chars %d-%d of 85000; continue with offset %d]", header, body[pageChars:pageChars*2], pageChars, pageChars*2, pageChars*2)
		if got := notes(rec{"op": "read", "path": "big.md", "offset": float64(pageChars)}); got != want {
			t.Fatalf("second page differs (%d chars)", len(got))
		}
		if got := notes(rec{"op": "read", "path": "big.md", "offset": float64(pageChars * 2)}); got != header+body[pageChars*2:] {
			t.Fatalf("third page differs (%d chars)", len(got))
		}
		if got := failure(rec{"op": "read", "path": "big.md", "offset": float64(85_000)}); !strings.Contains(got, "past the end") {
			t.Fatalf("offset past the end: %q", got)
		}

		notes(rec{"op": "write", "path": "big.md", "content": ""})
		if got := read("big.md"); got != "" {
			t.Fatalf("empty write must clear: %q", got)
		}
		if got := notes(rec{"op": "read", "path": "big.md"}); got != header {
			t.Fatalf("cleared note read %q", got)
		}
		if got := failure(rec{"op": "write", "path": "big.md"}); !strings.Contains(got, `"content" is required for op "write"`) {
			t.Fatalf("write without content: %q", got)
		}

		notes(rec{"op": "append", "path": "log.md", "content": "A"})
		notes(rec{"op": "append", "path": "log.md", "content": "B\n"})
		if got := read("log.md"); got != "A\nB\n" {
			t.Fatalf("appends %q", got)
		}
		notes(rec{"op": "write", "path": "log.md", "content": "X"})
		notes(rec{"op": "append", "path": "log.md", "content": "Y"})
		if got := read("log.md"); got != "X\nY\n" {
			t.Fatalf("append after write %q", got)
		}
		if got := failure(rec{"op": "append", "path": "log.md", "content": ""}); !strings.Contains(got, `"content" is required`) {
			t.Fatalf("append without content: %q", got)
		}

		notes(rec{
			"op":      "write",
			"path":    "search.md",
			"content": strings.Repeat(" ", 100) + strings.Repeat("x", 250) + " needle-here " + strings.Repeat("y", 100) + "\nsecond line",
		})
		hits := notes(rec{"op": "search", "query": "NEEDLE-here"})
		if !regexp.MustCompile(`^search\.md:1: …x{49} needle-here y{100}$`).MatchString(hits) {
			t.Fatalf("search excerpt %q", hits)
		}
	})

	// Posthorse test 59 (posthorse.test.ts:4754): two real processes append to one note.
	tw(t, "posthorse", "appends from concurrent Pi processes never merge records", func(t *testing.T) {
		runConcurrentNoteAppends(t)
	})

	// Posthorse test 61 (posthorse.test.ts:5154).
	tw(t, "posthorse", "note guards associate a move only with the immediately preceding update header", func(t *testing.T) {
		for _, newline := range []string{"\n", "\r\n", "\r"} {
			patch := func(headers ...string) string {
				return strings.Join(append(append([]string{"*** Begin Patch"}, headers...), "*** End Patch"), newline)
			}
			same := func(got, want []noteOperation) {
				t.Helper()
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("newline %q: got %+v, want %+v", newline, got, want)
				}
			}
			same(noteOperations("apply_patch", patch("*** Update File: source", "*** Move to: target")),
				[]noteOperation{{path: "source", action: "delete"}, {path: "target", action: "create"}})
			for _, c := range []struct{ header, action string }{
				{"*** Add File: source", "create"},
				{"*** Delete File: source", "delete"},
			} {
				same(noteOperations("apply_patch", patch(c.header, "*** Move to: target")),
					[]noteOperation{{path: "source", action: c.action}})
			}
			same(noteOperations("apply_patch", patch("*** Move to: target")), []noteOperation{})
			same(noteOperations("apply_patch", patch("*** Update File: source", "@@", "*** Move to: target")),
				[]noteOperation{{path: "source", action: "edit"}})
		}
	})
}

// notesWalkCancellation is posthorse.test.ts:3737-3796. The TS case mocks fs.promises.stat and
// aborts an AbortController from it; the Go walk reports each completed stat through
// noteStatHook and the abort is the host's cancel frame.
//
// The TS "turned" check (a setImmediate callback ran before the call finished) has no Go
// counterpart as such: an extension handler runs on its own goroutine and never holds Pi's
// event loop. The Go proof of the same claim is stronger: another request (a turn_start
// event) completes while the walk is still in progress.
func notesWalkCancellation(t *testing.T) {
	dir := notesRealDir(t)
	root := filepath.Join(dir, ".pi", "notes", "project")
	for topic := range 20 {
		for note := range 50 {
			notesWrite(t, filepath.Join(root, fmt.Sprintf("topic-%d", topic), fmt.Sprintf("note-%d.md", note)), "checkpoint")
		}
	}
	f := newFixture(t).With(withCwd(dir))
	exercise := func(params rec) string {
		var turned atomic.Bool
		var once sync.Once
		notesWalkHook(t, func(string) {
			once.Do(func() {
				f.Fire("turn_start", rec{})
				turned.Store(true)
			})
		})
		text := f.Run("notes", params).Text()
		if !turned.Load() {
			t.Fatalf("%v let Pi run other work meanwhile", params)
		}
		return text
	}
	cancelMidWalk := func(params rec) {
		started := make(chan *fixRun, 1)
		var once sync.Once
		notesWalkHook(t, func(path string) {
			if !strings.HasSuffix(path, "note-0.md") {
				return
			}
			once.Do(func() {
				run := <-started
				run.Cancel("abort")
				// The cancel frame is applied by the SDK's read loop before it reads the next
				// frame, so this completed event is a barrier: the walk resumes cancelled.
				f.Fire("turn_start", rec{})
			})
		})
		run := f.StartRun("notes", params)
		started <- run
		_, failure := run.Wait()
		if !strings.Contains(failure, "aborted") {
			t.Fatalf("%v: want an abort error, got %q", params, failure)
		}
	}
	listed := regexp.MustCompile(`(?m)^project/topic-\d+/note-\d+\.md {2}10B `)
	if got := exercise(rec{"op": "list"}); !listed.MatchString(got) {
		t.Fatalf("list %.200q", got)
	}
	if got := exercise(rec{"op": "read", "path": "project"}); !listed.MatchString(got) {
		t.Fatalf("read folder %.200q", got)
	}
	if got := exercise(rec{"op": "search", "query": "checkpoint"}); !regexp.MustCompile(`(?m)^project/topic-\d+/note-\d+\.md:1: checkpoint$`).MatchString(got) {
		t.Fatalf("search %.200q", got)
	}

	// A walk that continues after the abort reaches this loop and fails with ELOOP instead.
	notesMust(t, os.Symlink("zz-loop", filepath.Join(root, "zz-loop")))
	cancelMidWalk(rec{"op": "list"})
	cancelMidWalk(rec{"op": "read", "path": "project"})
	cancelMidWalk(rec{"op": "search", "query": "checkpoint"})
}
