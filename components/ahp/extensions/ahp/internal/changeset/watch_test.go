package changeset_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/ahp/internal/changeset"
)

type eventLog struct {
	mu    sync.Mutex
	paths []string
}

func (l *eventLog) add(p string) { l.mu.Lock(); l.paths = append(l.paths, p); l.mu.Unlock() }

func (l *eventLog) seen(p string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, x := range l.paths {
		if x == p {
			return true
		}
	}
	return false
}

func (l *eventLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := append([]string(nil), l.paths...)
	sort.Strings(out)
	return out
}

func (l *eventLog) reset() { l.mu.Lock(); l.paths = nil; l.mu.Unlock() }

// The polling watcher reports additions, changes and removals of files and directories, skips
// ignored paths, and stops when closed.
func TestPollWatch(t *testing.T) {
	root := realDir(t, t.TempDir())
	ignored := filepath.Join(root, "node_modules")
	if err := os.Mkdir(ignored, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(ignored, "pkg.js"), "x")
	write(t, filepath.Join(root, "existing.txt"), "a")
	events := &eventLog{}
	missing := filepath.Join(root, "later.lock") // a watched path that does not exist yet
	w, err := changeset.PollWatch(10*time.Millisecond)(context.Background(), []string{root, missing},
		func(p string) bool { return p == ignored }, events.add, func(err error) { t.Errorf("watch failed: %v", err) })
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = w.Close()
		}
	}()
	waitFor := func(what string, path string) {
		t.Helper()
		eventually(t, what, func() bool { return events.seen(path) })
		events.reset()
	}

	if got := events.all(); len(got) != 0 {
		t.Fatalf("the initial scan reports nothing: %v", got)
	}
	added := filepath.Join(root, "added.txt")
	write(t, added, "1")
	waitFor("an added file", added)
	write(t, added, "123456")
	waitFor("a changed file", added)
	if err := os.Remove(added); err != nil {
		t.Fatal(err)
	}
	waitFor("a removed file", added)

	dir := filepath.Join(root, "sub")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	waitFor("an added directory", dir)
	inner := filepath.Join(dir, "inner.txt")
	write(t, inner, "x")
	waitFor("a file in a new directory", inner)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	waitFor("a removed directory", dir)

	write(t, missing, "")
	waitFor("a watched path that appears", missing)

	// Ignored paths are not walked, so nothing under them is ever reported.
	write(t, filepath.Join(ignored, "other.js"), "y")
	write(t, filepath.Join(root, "after-ignored.txt"), "z")
	waitFor("a file next to the ignored directory", filepath.Join(root, "after-ignored.txt"))
	for _, p := range events.all() {
		if strings.HasPrefix(p, ignored) {
			t.Fatalf("event for an ignored path: %s", p)
		}
	}

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	write(t, filepath.Join(root, "after-close.txt"), "z")
	time.Sleep(100 * time.Millisecond)
	if events.seen(filepath.Join(root, "after-close.txt")) {
		t.Fatal("a closed watcher must not report")
	}
}
