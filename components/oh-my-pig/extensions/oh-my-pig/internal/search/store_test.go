package search

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

// A grep hit is an anchor for edit: grep records the lines it displays in the store the file tools check.
func TestGrepRecordsDisplayedLinesInTheSharedStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("one\nneedle two\nthree\nfour\nneedle five\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := runToolText(t, runGrep, dir, map[string]any{"pattern": "needle", "path": path})
	tag := hashline.FileHash("one\nneedle two\nthree\nfour\nneedle five\n")
	if want := "#" + tag; !contains(out, want) {
		t.Fatalf("grep output lacks the tag %s:\n%s", want, out)
	}
	snap, ok := hashline.Default().ByHash(path, tag)
	if !ok {
		t.Fatalf("the shared store has no snapshot %s for the file grep displayed", tag)
	}
	// Lines 2 and 5 were displayed; the others were not, so edit refuses an anchor there once.
	if !snap.SeenLines[2] || !snap.SeenLines[5] || snap.SeenLines[3] || len(snap.SeenLines) != 2 {
		t.Fatalf("seen lines = %v, want exactly 2 and 5", snap.SeenLines)
	}
}

// N1 (blocking): grep with a session-bearing sdk.Context records into hashline.ForSession(ctx)
// (NOT hashline.Default()), so edit staging against that session's store accepts the seen line
// and refuses an unseen line, while another session remains isolated.
// upstream: packages/coding-agent/src/tools/grep.ts:1289,1432-1447
func TestGrepSessionStoreSharedWithEdit(t *testing.T) {
	lifecycle.ResetForTests()
	hashline.RegisterLifecycle()
	t.Cleanup(func() {
		hashline.ReleaseSession("sess-grep-edit")
		hashline.ReleaseSession("sess-other")
		lifecycle.ResetForTests()
	})

	dir := t.TempDir()
	filePath := filepath.Join(dir, "app.txt")
	content := "line one\nneedle two\nline three\nline four\n"
	writeFile(t, filePath, content)

	ext := sdk.New("search-session-store")
	Register(ext)
	h := startTestHost(t, ext, dir, "sess-grep-edit")
	defer h.close()

	argsJSON, _ := json.Marshal(map[string]any{"pattern": "needle", "path": "app.txt"})
	resp := h.sendReq(map[string]any{
		"method":       "tool_call",
		"tool":         "grep",
		"tool_call_id": "tc-grep-1",
		"args":         json.RawMessage(argsJSON),
	})
	if resp.Response == nil || resp.Response.Error != nil {
		t.Fatalf("grep tool_call failed: %+v", resp.Response)
	}
	var toolRes struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(resp.Response.Result, &toolRes); err != nil {
		t.Fatalf("unmarshal grep result: %v", err)
	}
	tag := hashline.FileHash(content)
	if !strings.Contains(toolRes.Content, "[app.txt#"+tag+"]") {
		t.Fatalf("grep output missing [app.txt#%s]:\n%s", tag, toolRes.Content)
	}

	// Session store has the snapshot with line 2 seen; default store & other session do not.
	sessStore := hashline.StoreForSession("sess-grep-edit")
	if _, ok := sessStore.ByHash(filePath, tag); !ok {
		t.Fatalf("expected snapshot %s in StoreForSession(sess-grep-edit)", tag)
	}
	if _, ok := hashline.StoreForSession("sess-other").ByHash(filePath, tag); ok {
		t.Fatalf("other session store must not receive sess-grep-edit's snapshot")
	}

	// Staging an edit against the session store: line 2 (displayed by grep) succeeds;
	// line 4 (unseen) is rejected with RejectionUnseenLine.
	files := &hashline.DiskFileSource{WorkDir: dir}
	opts := hashline.StageOptions{EnforceSeenLines: true, MaxColumns: 512}

	seenPatch := fmt.Sprintf("[app.txt#%s]\nPUT 2.=2:\n+needle TWO\n", tag)
	if _, editErr := hashline.StageInput(seenPatch, files, sessStore, opts); editErr != nil {
		t.Fatalf("expected edit on grep-seen line 2 to succeed, got: %v", editErr)
	}

	unseenPatch := fmt.Sprintf("[app.txt#%s]\nPUT 4.=4:\n+LINE FOUR\n", tag)
	_, editErr := hashline.StageInput(unseenPatch, files, sessStore, opts)
	if editErr == nil || editErr.Reason != hashline.RejectionUnseenLines {
		t.Fatalf("expected RejectionUnseenLines on unseen line 4, got: %v", editErr)
	}
}

// N2: when no full row of a section is anchorable (every shown row was column-cut, or only
// the header survived the 50 KB cap), grep records [NO_FULL_LINE] (0) instead of nil/empty,
// so edit REFUSES any anchor on that file instead of treating it as authored/unrestricted.
// upstream: packages/coding-agent/src/edit/store.ts:22-41, packages/coding-agent/src/tools/grep.ts:1440-1443
func TestGrepNoFullLineRecordsZeroAndRefusesEdit(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "wide.txt")
	// Line 1 is >512 bytes so the single matching row is column-cut (0 full lines shown);
	// line 2 is short and unseen.
	content := "needle " + strings.Repeat("x", 650) + "\nshort unseen line\n"
	writeFile(t, filePath, content)

	store := hashline.NewStore()
	resAny, err := runGrepWithStore(context.Background(), dir, urls.Context{Cwd: dir}, store, map[string]any{
		"pattern": "needle",
		"path":    "wide.txt",
	})
	if err != nil {
		t.Fatalf("grep failed: %v", err)
	}
	res := resAny.(sdk.ToolResult)
	tag := hashline.FileHash(content)
	if !strings.Contains(res.Content, "[wide.txt#"+tag+"]") {
		t.Fatalf("expected tag header in output:\n%s", res.Content)
	}

	snap, ok := store.ByHash(filePath, tag)
	if !ok {
		t.Fatalf("expected snapshot %s in store", tag)
	}
	if !snap.SeenLines[0] || len(snap.SeenLines) != 1 {
		t.Fatalf("expected SeenLines={0:true} (NO_FULL_LINE), got %v", snap.SeenLines)
	}

	// Edit targeting unseen short line 2 must be refused (NOT treated as authored/unrestricted).
	files := &hashline.DiskFileSource{WorkDir: dir}
	opts := hashline.StageOptions{EnforceSeenLines: true, MaxColumns: 512}
	patch := fmt.Sprintf("[wide.txt#%s]\nPUT 2.=2:\n+modified\n", tag)
	_, editErr := hashline.StageInput(patch, files, store, opts)
	if editErr == nil || editErr.Reason != hashline.RejectionUnseenLines {
		t.Fatalf("expected RejectionUnseenLines when only NO_FULL_LINE was recorded, got: %v", editErr)
	}
}

// N3: chunked parallel grep stops after the chunk in which the running match total reaches
// grepInternalTotalCap (2000), instead of scanning every remaining candidate file.
// upstream: crates/xfs/src/grep/mod.rs:209-211
func TestGrepChunkedScanStopsAtTotalCap(t *testing.T) {
	dir := t.TempDir()
	// 300 files × 11 matches each = 3300 potential matches (> grepInternalTotalCap = 2000).
	// The first chunk (256 files) hits the 2000-match cap, so files beyond chunk 1 are not
	// scanned and the pagination footer reports a lower bound ("<N>+").
	body := strings.Repeat("needle\n", 11)
	for i := range 300 {
		writeFile(t, filepath.Join(dir, fmt.Sprintf("f%04d.txt", i)), body)
	}
	out := runToolText(t, runGrep, dir, map[string]any{"pattern": "needle", "path": "."})
	if !regexp.MustCompile(`Showing files 1-20 of \d+\+\. Use skip=20`).MatchString(out) {
		t.Fatalf("expected total-cap lower-bound '+' total in pagination footer:\n%s", out)
	}
}

// N4: SetSetting and concurrent readSetting calls are race-free under -race.
func TestSetSettingConcurrentRaceFree(t *testing.T) {
	settingMu.RLock()
	prev := Setting
	settingMu.RUnlock()
	t.Cleanup(func() { SetSetting(prev) })

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range 200 {
			if i%2 == 0 {
				SetSetting(func(_ sdk.Context, key string) any {
					if key == "find.enabled" {
						return "on"
					}
					return nil
				})
			} else {
				SetSetting(nil)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for range 200 {
			_ = readSetting(sdk.Context{}, "find.enabled")
		}
	}()
	wg.Wait()
}

// N5: concurrent readPrefix / enumeratedLeaves on a shared searchURLFS (as find's parallel
// buildLexicalIndex invokes over a virtual Enumerator root) is race-free under -race.
func TestSearchURLFSConcurrentEnumeratorRaceFree(t *testing.T) {
	urls.ResetForTests()
	t.Cleanup(urls.ResetForTests)

	docs := make(map[string]string, 24)
	for i := range 24 {
		docs[fmt.Sprintf("doc%02d.md", i)] = fmt.Sprintf("content token %d\n", i)
	}
	if err := urls.Register(&fakeVirtualEnumHandler{scheme: "vconc", docs: docs}); err != nil {
		t.Fatal(err)
	}

	urlfs := newSearchURLFS(urls.Context{Cwd: t.TempDir()})
	entries := make([]findFileEntry, 0, len(docs))
	for k := range docs {
		entries = append(entries, findFileEntry{path: "vconc://" + k, rel: k, size: 16})
	}
	// Run parallel lexical indexing directly on a fresh searchURLFS so multiple workers
	// race to populate and read searchURLFS.leaves simultaneously.
	perFile := buildLexicalIndex(context.Background(), entries, []string{"token"}, urlfs)
	if len(perFile) != len(docs) {
		t.Fatalf("indexed %d virtual docs, want %d", len(perFile), len(docs))
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
