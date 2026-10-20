package powerline_footer

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Twins of port/oracle/tests/queue-store.test.ts: same inputs, same expectations, real temporary directories.

func withStore(t *testing.T, fn func(store tstore, dir string)) {
	t.Helper()
	dir := t.TempDir()
	fn(tstore{newQueueStore(filepath.Join(dir, "inbox.jsonl"), filepath.Join(dir, "projects.json")), t}, dir)
}

// tstore wraps a store so that a failing call fails the test, the way an exception fails the upstream one.
type tstore struct {
	*queueStore
	t *testing.T
}

func (s tstore) list() []queueItem {
	s.t.Helper()
	v, err := s.queueStore.list()
	if err != nil {
		s.t.Fatal(err)
	}
	return v
}

func (s tstore) add(in createQueueItemInput) queueItem {
	s.t.Helper()
	v, err := s.queueStore.add(in)
	if err != nil {
		s.t.Fatal(err)
	}
	return v
}

func (s tstore) get(prefix string) *queueItem {
	s.t.Helper()
	v, err := s.queueStore.get(prefix)
	if err != nil {
		s.t.Fatal(err)
	}
	return v
}

func (s tstore) update(id string, u queueUpdates) *queueItem {
	s.t.Helper()
	v, err := s.queueStore.update(id, u)
	if err != nil {
		s.t.Fatal(err)
	}
	return v
}

func (s tstore) clear(id string) *queueItem {
	s.t.Helper()
	v, err := s.queueStore.clear(id)
	if err != nil {
		s.t.Fatal(err)
	}
	return v
}

func (s tstore) activeItems(c queueContext) []queueItem {
	s.t.Helper()
	v, err := s.queueStore.activeItems(c)
	if err != nil {
		s.t.Fatal(err)
	}
	return v
}

func (s tstore) summarize(c queueContext, compacting bool) queueSummary {
	s.t.Helper()
	v, err := s.queueStore.summarize(c, compacting)
	if err != nil {
		s.t.Fatal(err)
	}
	return v
}

func statOf(t *testing.T, path string) os.FileInfo {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

func readOf(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestQueueStoreTwins(t *testing.T) {
	tw(t, "queue-store", "queue store summarizes queued and blocked prompts", func(t *testing.T) {
		withStore(t, func(store tstore, _ string) {
			cwd := "/tmp/project-a"
			store.add(createQueueItemInput{Text: "run after compact", Source: queueSource{Cwd: cwd, SessionID: "s1"}, Target: queueTarget{Kind: targetCurrentSession}, Intent: intentPostCompact, Now: new(100.0)})
			blocked := store.add(createQueueItemInput{Text: "retry me", Source: queueSource{Cwd: cwd, SessionID: "s1"}, Target: queueTarget{Kind: targetProject, Cwd: cwd}, Intent: intentFollowUp, Now: new(101.0)})
			store.update(blocked.ID, queueUpdates{Status: statusBlocked})

			got := store.summarize(currentQueueContext(cwd, "s1"), true)
			eq(t, got, queueSummary{QueueCount: 2, BlockedCount: 1, Compacting: true, LeadingText: "retry me", LeadingIntent: intentFollowUp, LeadingStatus: statusBlocked}, "summary")
		})
	})
	tw(t, "queue-store", "current-session targets stay scoped to the source session when known", func(t *testing.T) {
		withStore(t, func(store tstore, _ string) {
			store.add(createQueueItemInput{Text: "session only", Source: queueSource{Cwd: "/tmp/project", SessionID: "s1"}, Target: queueTarget{Kind: targetCurrentSession}, Intent: intentPostCompact})
			eq(t, len(store.activeItems(currentQueueContext("/tmp/project", "s1"))), 1, "s1")
			eq(t, len(store.activeItems(currentQueueContext("/tmp/project", "s2"))), 0, "s2")
		})
	})
	tw(t, "queue-store", "queue delivery returns the prompt text", func(t *testing.T) {
		eq(t, formatQueueDeliveryText(queueItem{
			ID: "a1b2c3d4", Text: "check logs", CreatedAt: 1000, UpdatedAt: 1000,
			Source: queueSource{Cwd: "/tmp/project"}, Target: queueTarget{Kind: targetCurrentSession}, Intent: intentFollowUp, Status: statusQueued,
		}), "check logs", "text")
	})
	tw(t, "queue-store", "parseCompactQueuedPrompt treats /compact suffix as queued prompt text", func(t *testing.T) {
		parse := func(s string) string {
			text, ok := parseCompactQueuedPrompt(s)
			if !ok {
				return "<null>"
			}
			return text
		}
		eq(t, parse("/compact great lets proceed"), "great lets proceed", "plain")
		eq(t, parse("  /compact   great lets proceed  "), "great lets proceed", "spaced")
		eq(t, parse("/compact"), "<null>", "bare")
		eq(t, parse("/compactness great lets proceed"), "<null>", "word")
	})
	tw(t, "queue-store", "queue store clears items from active summary", func(t *testing.T) {
		withStore(t, func(store tstore, _ string) {
			item := store.add(createQueueItemInput{Text: "queued prompt", Source: queueSource{Cwd: "/tmp/project"}, Target: queueTarget{Kind: targetCurrentSession}, Intent: intentPostCompact})
			eq(t, store.summarize(currentQueueContext("/tmp/project", ""), false).QueueCount, 1, "before")
			store.clear(item.ID)
			eq(t, store.summarize(currentQueueContext("/tmp/project", ""), false).QueueCount, 0, "after")
		})
	})
	tw(t, "queue-store", "summaries follow external edits and replacement without losing writes or exposing cached items", func(t *testing.T) {
		withStore(t, func(store tstore, dir string) {
			path := filepath.Join(dir, "inbox.jsonl")
			context := currentQueueContext(dir, "")
			input := func(text string, now float64) createQueueItemInput {
				return createQueueItemInput{Text: text, Source: queueSource{Cwd: dir}, Target: queueTarget{Kind: targetGlobal}, Intent: intentFollowUp, Now: new(now)}
			}
			lead := func() string { return store.summarize(context, false).LeadingText }
			first := store.add(input("first", 100))
			eq(t, lead(), "first", "first")

			// Same-sized in-place edit, with mtime restored: ctime must still invalidate. The sleep keeps the edit out of the
			// coarse timestamp tick of the write above, so that ctime can differ at all.
			time.Sleep(20 * time.Millisecond)
			originalStat := statOf(t, path)
			data := readOf(t, path)
			if err := os.WriteFile(path, []byte(strings.Replace(string(data), "first", "other", 1)), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(path, originalStat.ModTime(), originalStat.ModTime()); err != nil {
				t.Fatal(err)
			}
			eq(t, lead(), "other", "in-place edit")

			listed := store.list()
			listed[0].Text = "mutated"
			listed = listed[:0]
			_ = listed
			eq(t, lead(), "other", "cached items never escape")

			external := tstore{newQueueStore(path, filepath.Join(dir, "projects.json")), t}
			second := external.add(input("second", 101))
			// Local read-modify-write must include the external addition, even with a warm snapshot.
			store.update(first.ID, queueUpdates{Status: statusBlocked})
			eq(t, store.summarize(context, false).QueueCount, 2, "external addition kept")
			external.update(second.ID, queueUpdates{Text: new("external change")})
			store.add(input("third", 102))
			got := store.get(second.ID)
			if got == nil {
				t.Fatal("second item lost")
			}
			eq(t, got.Text, "external change", "external update kept")

			eq(t, lead(), "other", "blocked item leads")

			replacement := filepath.Join(dir, "replacement.jsonl")
			time.Sleep(20 * time.Millisecond)
			beforeReplacement := statOf(t, path)
			data = readOf(t, path)
			if err := os.WriteFile(replacement, []byte(strings.Replace(string(data), "other", "newer", 1)), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(replacement, beforeReplacement.ModTime(), beforeReplacement.ModTime()); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(replacement, path); err != nil {
				t.Fatal(err)
			}
			eq(t, lead(), "newer", "replacement")
		})
	})
	tw(t, "queue-store", "summary snapshots recover after missing files and read errors", func(t *testing.T) {
		withStore(t, func(store tstore, dir string) {
			path := filepath.Join(dir, "inbox.jsonl")
			context := currentQueueContext(dir, "")
			store.add(createQueueItemInput{Text: "visible", Source: queueSource{Cwd: dir}, Target: queueTarget{Kind: targetGlobal}, Intent: intentSteer})
			content := readOf(t, path)
			eq(t, store.summarize(context, false).LeadingText, "visible", "visible")
			if runtime.GOOS != "windows" && os.Getuid() != 0 {
				if err := os.Chmod(path, 0); err != nil {
					t.Fatal(err)
				}
				_, err := store.queueStore.summarize(context, false)
				if !errors.Is(err, fs.ErrPermission) {
					t.Errorf("summarize with an unreadable inbox: err = %v, want EACCES", err)
				}
				if err := os.Chmod(path, 0o600); err != nil {
					t.Fatal(err)
				}
				eq(t, store.summarize(context, false).LeadingText, "visible", "after chmod back")
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			eq(t, store.summarize(context, false).QueueCount, 0, "missing file")
			if err := os.WriteFile(path, []byte(strings.Replace(string(content), "visible", "restored", 1)), 0o644); err != nil {
				t.Fatal(err)
			}
			eq(t, store.summarize(context, false).LeadingText, "restored", "restored")
		})
	})
	tw(t, "queue-store", "queue store times out instead of stealing an existing lock", func(t *testing.T) {
		withStore(t, func(store tstore, dir string) {
			lockPath := filepath.Join(dir, "inbox.jsonl.lock")
			if err := os.Mkdir(lockPath, 0o777); err != nil {
				t.Fatal(err)
			}
			_, err := store.queueStore.add(createQueueItemInput{Text: "blocked write", Source: queueSource{Cwd: "/tmp/project"}, Target: queueTarget{Kind: targetProject, Cwd: "/tmp/project"}, Intent: intentFollowUp})
			if err == nil || !strings.Contains(err.Error(), "Timed out waiting for Powerline queue store lock") {
				t.Errorf("err = %v, want the lock timeout", err)
			}
			if _, err := os.Stat(lockPath); err != nil {
				t.Errorf("the existing lock was removed: %v", err)
			}
		})
	})
}
