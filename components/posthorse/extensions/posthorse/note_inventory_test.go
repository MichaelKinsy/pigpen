package posthorse

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// note-inventory.ts: the depth-first, mtime-ordered walk of one scope.

func nsHook(t *testing.T, hook func(path string)) {
	t.Helper()
	noteStatHook = hook
	t.Cleanup(func() { noteStatHook = nil })
}

func nsRelPaths(root string, files []noteFile) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = filepath.ToSlash(strings.TrimPrefix(f.path, root+string(filepath.Separator)))
	}
	return out
}

func nsEqual(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestNoteInventory_listsNewestFirstAndKeepsListingOrderForTies(t *testing.T) {
	root := nsRealTemp(t)
	base := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	nsWriteNote(t, root, "b.md", "b", base)
	nsWriteNote(t, root, "a.md", "a", base)
	nsWriteNote(t, root, "dir/new.md", "n", base.Add(time.Hour))
	nsWriteNote(t, root, "old.md", "o", base.Add(-time.Hour))
	files, err := notesUnder(nil, noteScope{root: root}, nil, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	nsEqual(t, nsRelPaths(root, files), []string{"dir/new.md", "a.md", "b.md", "old.md"})
	if files[0].size != 1 || files[0].mtimeMs != msOf(base.Add(time.Hour)) {
		t.Fatalf("identity %#v", files[0].noteIdentity)
	}
}

func TestNoteInventory_folderFileAndMissingTargets(t *testing.T) {
	root := nsRealTemp(t)
	now := time.Now()
	nsWriteNote(t, root, "dir/a.md", "a", now)
	nsWriteNote(t, root, "dir/sub/b.md", "b", now.Add(-time.Minute))
	nsWriteNote(t, root, "top.md", "t", now)
	scope := noteScope{root: root}
	str := func(s string) *string { return &s }
	for _, folder := range []*string{nil, str(""), str(".")} {
		files, err := notesUnder(nil, scope, folder, func(string) {})
		if err != nil || len(files) != 3 {
			t.Fatalf("whole scope for %v: %v %v", folder, files, err)
		}
	}
	files, err := notesUnder(nil, scope, str("dir"), func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	nsEqual(t, nsRelPaths(root, files), []string{"dir/a.md", "dir/sub/b.md"})
	files, err = notesUnder(nil, scope, str("dir/a.md"), func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	nsEqual(t, nsRelPaths(root, files), []string{"dir/a.md"})
	if files, err = notesUnder(nil, scope, str("nope"), func(string) {}); err != nil || len(files) != 0 {
		t.Fatalf("missing folder: %v %v", files, err)
	}
	if files, err = notesUnder(nil, scope, str("top.md/x"), func(string) {}); err != nil || len(files) != 0 {
		t.Fatalf("ENOTDIR folder: %v %v", files, err)
	}
	if _, err = notesUnder(nil, scope, str("../escape"), func(string) {}); err == nil {
		t.Fatal("a folder outside the scope was accepted")
	}
	if files, err = notesUnder(nil, noteScope{root: filepath.Join(root, "missing")}, nil, func(string) {}); err != nil || len(files) != 0 {
		t.Fatalf("missing scope: %v %v", files, err)
	}
}

func TestNoteInventory_followsAliasesSkipsCyclesAndMissingTargets(t *testing.T) {
	root := nsRealTemp(t)
	now := time.Now()
	nsWriteNote(t, root, "nested/state.md", "checkpoint", now)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	must(os.Symlink("nested", filepath.Join(root, "alias")))
	must(os.Symlink("..", filepath.Join(root, "nested", "back")))
	must(os.Symlink("deleted.md", filepath.Join(root, "missing.md")))
	must(os.Symlink("deleted-directory", filepath.Join(root, "missing-directory")))
	files, err := notesUnder(nil, noteScope{root: root}, nil, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	nsEqual(t, nsRelPaths(root, files), []string{"alias/state.md", "nested/state.md"})
}

func TestNoteInventory_privateScopesDoNotIndexAliasesOutsideTheScope(t *testing.T) {
	root := nsRealTemp(t)
	scope := noteScope{root: root, scope: ".forks/2026-01-02T03-04-05-678Z_s1/notes"}
	directory := scopeDirectory(scope)
	now := time.Now()
	nsWriteNote(t, directory, "own.md", "own", now)
	nsWriteNote(t, root, "shared.md", "shared", now)
	if err := os.Symlink(filepath.Join(root, "shared.md"), filepath.Join(directory, "link.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(root, filepath.Join(directory, "dirlink")); err != nil {
		t.Fatal(err)
	}
	var notices []string
	files, err := notesUnder(nil, scope, nil, func(text string) { notices = append(notices, text) })
	if err != nil {
		t.Fatal(err)
	}
	nsEqual(t, nsRelPaths(directory, files), []string{"own.md"})
	if len(notices) != 2 {
		t.Fatalf("notices %q", notices)
	}
	for _, n := range notices {
		if !strings.HasPrefix(n, "Not indexed: alias outside private notes scope (") || !strings.HasSuffix(n, ").") {
			t.Fatalf("notice %q", n)
		}
	}
}

func TestNoteInventory_projectScopeNeverEntersManagedForks(t *testing.T) {
	root := nsRealTemp(t)
	now := time.Now()
	nsWriteNote(t, root, "project.md", "p", now)
	nsWriteNote(t, root, ".forks/2026-01-02T03-04-05-678Z_s1/notes/private.md", "x", now)
	files, err := notesUnder(nil, noteScope{root: root}, nil, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	// The managed directory is skipped as a whole: its real path is inside .forks (allowedTarget).
	nsEqual(t, nsRelPaths(root, files), []string{"project.md"})
	if err := os.Symlink(filepath.Join(root, ".forks"), filepath.Join(root, "managed")); err == nil {
		files, err := notesUnder(nil, noteScope{root: root}, nil, func(string) {})
		if err != nil {
			t.Fatal(err)
		}
		nsEqual(t, nsRelPaths(root, files), []string{"project.md"})
	}
}

func TestNoteInventory_skipsEntriesRemovedWhileAnEarlierFolderIsWalked(t *testing.T) {
	root := nsRealTemp(t)
	now := time.Now()
	nsWriteNote(t, root, "a-many/first.md", "needle in a-many", now)
	nsWriteNote(t, root, "z-doomed/note.md", "doomed", now)
	nsWriteNote(t, root, "z-doomed.md", "doomed", now)
	var removed bool
	nsHook(t, func(path string) {
		if path == filepath.Join(root, "a-many", "first.md") && !removed {
			_ = os.RemoveAll(filepath.Join(root, "z-doomed"))
			_ = os.Remove(filepath.Join(root, "z-doomed.md"))
			removed = true
		}
	})
	files, err := notesUnder(nil, noteScope{root: root}, nil, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Fatal("the cleanup did not run during the walk")
	}
	nsEqual(t, nsRelPaths(root, files), []string{"a-many/first.md"})
}

func TestNoteInventory_statsEveryEntryOnceInListingOrder(t *testing.T) {
	root := nsRealTemp(t)
	now := time.Now()
	for i := range 200 {
		nsWriteNote(t, root, fmt.Sprintf("n%03d.md", i), "x", now)
	}
	var mu sync.Mutex
	var seen []string
	nsHook(t, func(path string) { mu.Lock(); seen = append(seen, filepath.Base(path)); mu.Unlock() })
	files, err := notesUnder(nil, noteScope{root: root}, nil, func(string) {})
	if err != nil || len(files) != 200 {
		t.Fatalf("%d files, %v", len(files), err)
	}
	// Listing order for equal mtimes is the directory's (sorted) order.
	if files[0].path != filepath.Join(root, "n000.md") || files[199].path != filepath.Join(root, "n199.md") {
		t.Fatalf("order %s ... %s", files[0].path, files[199].path)
	}
	if len(seen) != 201 { // the scope directory itself plus every file
		t.Fatalf("stats %d", len(seen))
	}
}

func TestNoteInventory_stopsWhenCancelledMidWalk(t *testing.T) {
	root := nsRealTemp(t)
	now := time.Now()
	for topic := range 20 {
		for note := range 50 {
			nsWriteNote(t, root, fmt.Sprintf("topic-%d/note-%d.md", topic, note), "checkpoint", now)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	nsHook(t, func(path string) {
		if strings.HasSuffix(path, "note-0.md") {
			cancel()
		}
	})
	_, err := notesUnder(ctx, noteScope{root: root}, nil, func(string) {})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	// A context cancelled before the walk starts fails before reading anything.
	noteStatHook = nil
	pre, stop := context.WithCancel(context.Background())
	stop()
	if _, err := notesUnder(pre, noteScope{root: root}, nil, func(string) {}); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled err = %v", err)
	}
}

// A folder's later siblings are statted only after the folder was walked (note-inventory.ts:25-47).
func TestNoteInventory_laterSiblingsAreStattedAfterAFolderIsWalked(t *testing.T) {
	root := nsRealTemp(t)
	now := time.Now()
	nsWriteNote(t, root, "a-dir/inner.md", "x", now)
	nsWriteNote(t, root, "b.md", "x", now)
	nsWriteNote(t, root, "c.md", "x", now)
	var mu sync.Mutex
	var order []string
	nsHook(t, func(path string) { mu.Lock(); order = append(order, filepath.Base(path)); mu.Unlock() })
	if _, err := notesUnder(nil, noteScope{root: root}, nil, func(string) {}); err != nil {
		t.Fatal(err)
	}
	pos := map[string]int{}
	for i, name := range order {
		if _, seen := pos[name]; !seen {
			pos[name] = i
		}
	}
	if !(pos["inner.md"] < pos["b.md"] && pos["inner.md"] < pos["c.md"]) {
		t.Fatalf("stat order %v", order)
	}
}
