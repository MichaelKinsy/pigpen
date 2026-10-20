package pi_subagents

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// Twins of background-work.test.ts, wait-completions.test.ts, completion-replay.test.ts and auto-drain.test.ts.

func asyncState(sessionID string) *SubagentState {
	s := newSubagentState()
	s.CurrentSessionID = sessionID
	return s
}

func writeRunStatus(t *testing.T, root, id, state, sessionID string) {
	t.Helper()
	writeRunStatusPid(t, root, id, state, sessionID, 999999)
}

func writeRunStatusPid(t *testing.T, root, id, state, sessionID string, pid int) {
	t.Helper()
	dir := filepath.Join(root, id)
	now := nowMs()
	st := map[string]any{"runId": id, "mode": "single", "state": state, "sessionId": sessionID, "startedAt": now, "lastUpdate": now, "pid": pid,
		"steps": []any{map[string]any{"agent": "worker", "status": state}}}
	b, _ := json.Marshal(st)
	writeFile(t, filepath.Join(dir, "status.json"), string(b))
	if err := updateActiveRunIndex(dir, state, "", indexOptions{}); err != nil {
		t.Fatal(err)
	}
}

func aliveKill(int, int) error { return nil }

func bgWaitDeps(root string, state *SubagentState, work *BackgroundWorkDeps, sleep func(int64, <-chan struct{})) WaitDeps {
	return WaitDeps{State: state, BackgroundWork: work, Sleep: sleep, PollIntervalMs: 250, AsyncDirRoot: filepath.Join(root, "runs"), ResultsDir: filepath.Join(root, "results"), Kill: aliveKill}
}

func snap(providers []string, items ...RegisteredBackgroundWorkItem) BackgroundWorkSnapshot {
	if items == nil {
		items = []RegisteredBackgroundWorkItem{}
	}
	return BackgroundWorkSnapshot{Providers: providers, Items: items}
}

func noSleep(int64, <-chan struct{}) {}

func mustMatch(t *testing.T, pattern, text string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(text) {
		t.Errorf("%q does not match /%s/", text, pattern)
	}
}

func TestBackgroundWorkProviderProtocol(t *testing.T) {
	const f = "background-work"
	t.Cleanup(clearBackgroundWorkRegistry)
	tw(t, f, "replaces registrations safely across reloads and scopes snapshots to one session", func(t *testing.T) {
		clearBackgroundWorkRegistry()
		var ctx BackgroundWorkContext
		disposeOld, err := registerBackgroundWorkProvider(&BackgroundWorkProvider{Name: "patty", ListActiveWork: func(BackgroundWorkContext) ([]BackgroundWorkItem, error) {
			return []BackgroundWorkItem{{ID: "old", SessionID: "session-a"}}, nil
		}})
		eq(t, err, nil, "register old")
		replacement := &BackgroundWorkProvider{Name: "patty", WakeChannels: []string{"patty:finished"},
			Reconcile: func(c BackgroundWorkContext) error { ctx = c; return nil },
			ListActiveWork: func(BackgroundWorkContext) ([]BackgroundWorkItem, error) {
				return []BackgroundWorkItem{{ID: "mine", SessionID: "session-a"}, {ID: "theirs", SessionID: "session-b"}}, nil
			}}
		disposeReplacement, _ := registerBackgroundWorkProvider(replacement)
		disposeOld()
		providers, _ := listBackgroundWorkProviders()
		eq(t, len(providers), 1, "providers")
		eq(t, providers[0], replacement, "the replacement stays")
		ch, _ := listBackgroundWorkWakeChannels()
		eq(t, ch, []string{"patty:finished"}, "wake channels")
		s, err := snapshotBackgroundWork("session-a", 42)
		eq(t, err, nil, "snapshot")
		eq(t, s, BackgroundWorkSnapshot{Providers: []string{"patty"}, Items: []RegisteredBackgroundWorkItem{{Provider: "patty", ID: "mine", SessionID: "session-a"}}}, "snapshot")
		eq(t, ctx, BackgroundWorkContext{SessionID: "session-a", NowMs: 42}, "reconcile context")
		disposeReplacement()
		providers, _ = listBackgroundWorkProviders()
		eq(t, len(providers), 0, "removed")
	})
	tw(t, f, "passes an optional session context while preserving legacy providers and exact filtering", func(t *testing.T) {
		clearBackgroundWorkRegistry()
		var listCtx BackgroundWorkContext
		legacyCalled := false
		registerBackgroundWorkProvider(&BackgroundWorkProvider{Name: "legacy", ListActiveWork: func(BackgroundWorkContext) ([]BackgroundWorkItem, error) {
			legacyCalled = true
			return []BackgroundWorkItem{{ID: "legacy-job", SessionID: "session-a"}}, nil
		}})
		registerBackgroundWorkProvider(&BackgroundWorkProvider{Name: "scoped", ListActiveWork: func(c BackgroundWorkContext) ([]BackgroundWorkItem, error) {
			listCtx = c
			return []BackgroundWorkItem{{ID: "mine", SessionID: c.SessionID}, {ID: "theirs", SessionID: "session-b"}}, nil
		}})
		s, _ := snapshotBackgroundWork("session-a", 42)
		eq(t, s, BackgroundWorkSnapshot{Providers: []string{"legacy", "scoped"}, Items: []RegisteredBackgroundWorkItem{{"legacy", "legacy-job", "session-a"}, {"scoped", "mine", "session-a"}}}, "snapshot")
		eq(t, legacyCalled, true, "legacy called")
		eq(t, listCtx, BackgroundWorkContext{SessionID: "session-a", NowMs: 42}, "list context")
	})
	tw(t, f, "validates provider metadata and work items strictly", func(t *testing.T) {
		clearBackgroundWorkRegistry()
		list := func(items ...BackgroundWorkItem) func(BackgroundWorkContext) ([]BackgroundWorkItem, error) {
			return func(BackgroundWorkContext) ([]BackgroundWorkItem, error) { return items, nil }
		}
		_, err := registerBackgroundWorkProvider(&BackgroundWorkProvider{Name: " patty", ListActiveWork: list()})
		hasMatch(t, err, "leading or trailing")
		_, err = registerBackgroundWorkProvider(&BackgroundWorkProvider{Name: "patty", ListActiveWork: list(), WakeChannels: []string{"done", "done"}})
		hasMatch(t, err, "duplicates")
		registerBackgroundWorkProvider(&BackgroundWorkProvider{Name: "patty", ListActiveWork: list(BackgroundWorkItem{ID: "job", SessionID: "session-a", Extra: []string{"extra"}})})
		_, err = snapshotBackgroundWork("session-a", 0)
		hasMatch(t, err, "unknown fields: extra")
		clearBackgroundWorkRegistry()
		registerBackgroundWorkProvider(&BackgroundWorkProvider{Name: "patty", ListActiveWork: list(BackgroundWorkItem{ID: "job", SessionID: "session-a"}, BackgroundWorkItem{ID: "job", SessionID: "session-a"})})
		_, err = snapshotBackgroundWork("session-a", 0)
		hasMatch(t, err, "duplicate item 'job'")
	})
	tw(t, f, "validates scoped provider results before filtering them", func(t *testing.T) {
		clearBackgroundWorkRegistry()
		registerBackgroundWorkProvider(&BackgroundWorkProvider{Name: "scoped", ListActiveWork: func(BackgroundWorkContext) ([]BackgroundWorkItem, error) {
			return []BackgroundWorkItem{{ID: "job", SessionID: "session-a", Extra: []string{"extra"}}}, nil
		}})
		_, err := snapshotBackgroundWork("session-a", 0)
		hasMatch(t, err, "unknown fields: extra")
		clearBackgroundWorkRegistry()
		registerBackgroundWorkProvider(&BackgroundWorkProvider{Name: "scoped", ListActiveWork: func(c BackgroundWorkContext) ([]BackgroundWorkItem, error) {
			return []BackgroundWorkItem{{ID: "job", SessionID: c.SessionID}, {ID: "job", SessionID: c.SessionID}}, nil
		}})
		_, err = snapshotBackgroundWork("session-a", 0)
		hasMatch(t, err, "duplicate item 'job'")
	})
	tw(t, f, "accepts path-like session ids up to the bound and rejects longer ids", func(t *testing.T) {
		clearBackgroundWorkRegistry()
		long := strings.Repeat("/nested/", 511) + "xsession"
		over := long + "x"
		eq(t, len(long), 4096, "bound")
		registerBackgroundWorkProvider(&BackgroundWorkProvider{Name: "patty", ListActiveWork: func(BackgroundWorkContext) ([]BackgroundWorkItem, error) {
			return []BackgroundWorkItem{{ID: "job", SessionID: long}}, nil
		}})
		s, err := snapshotBackgroundWork(long, 0)
		eq(t, err, nil, "at the bound")
		eq(t, s, BackgroundWorkSnapshot{Providers: []string{"patty"}, Items: []RegisteredBackgroundWorkItem{{"patty", "job", long}}}, "snapshot")
		_, err = snapshotBackgroundWork(over, 0)
		hasMatch(t, err, "at most 4096 characters")
		clearBackgroundWorkRegistry()
		registerBackgroundWorkProvider(&BackgroundWorkProvider{Name: "patty", ListActiveWork: func(BackgroundWorkContext) ([]BackgroundWorkItem, error) {
			return []BackgroundWorkItem{{ID: "job", SessionID: over}}, nil
		}})
		_, err = snapshotBackgroundWork("session-a", 0)
		hasMatch(t, err, "at most 4096 characters")
	})
	tw(t, f, "preserves list and reconcile errors with provider context", func(t *testing.T) {
		clearBackgroundWorkRegistry()
		registerBackgroundWorkProvider(&BackgroundWorkProvider{Name: "broken-reconcile", Reconcile: func(BackgroundWorkContext) error { return fmt.Errorf("pid probe failed") },
			ListActiveWork: func(BackgroundWorkContext) ([]BackgroundWorkItem, error) { return nil, nil }})
		_, err := snapshotBackgroundWork("session-a", 0)
		mustMatch(t, `broken-reconcile.*pid probe failed`, err.Error())
		clearBackgroundWorkRegistry()
		registerBackgroundWorkProvider(&BackgroundWorkProvider{Name: "broken-list", ListActiveWork: func(BackgroundWorkContext) ([]BackgroundWorkItem, error) {
			return nil, fmt.Errorf("registry unavailable")
		}})
		_, err = snapshotBackgroundWork("session-a", 0)
		mustMatch(t, `broken-list.*registry unavailable`, err.Error())
	})
	tw(t, f, "discovers wake channels without reconciling or listing work", func(t *testing.T) {
		clearBackgroundWorkRegistry()
		called := false
		registerBackgroundWorkProvider(&BackgroundWorkProvider{Name: "patty", WakeChannels: []string{"patty:finished"},
			Reconcile:      func(BackgroundWorkContext) error { called = true; return nil },
			ListActiveWork: func(BackgroundWorkContext) ([]BackgroundWorkItem, error) { called = true; return nil, nil }})
		ch, _ := listBackgroundWorkWakeChannels()
		eq(t, ch, []string{"patty:finished"}, "channels")
		eq(t, called, false, "nothing called")
	})
}

func TestBgWaitWithBackgroundWorkProviders(t *testing.T) {
	const f = "background-work"
	tw(t, f, "detects completion when another item replaces it at the same count", func(t *testing.T) {
		root := t.TempDir()
		stage := 0
		work := &BackgroundWorkDeps{WakeChannels: func() []string { return nil }, Snapshot: func(string, int64) (BackgroundWorkSnapshot, error) {
			if stage == 0 {
				return snap([]string{"patty"}, RegisteredBackgroundWorkItem{"patty", "job-a", "session-a"}), nil
			}
			return snap([]string{"patty"}, RegisteredBackgroundWorkItem{"patty", "job-b", "session-a"}), nil
		}}
		r := waitForSubagents(WaitParams{}, nil, bgWaitDeps(root, asyncState("session-a"), work, func(int64, <-chan struct{}) { stage = 1 }))
		eq(t, r.IsError, false, "isError")
		mustMatch(t, `1 of 1 item\(s\) finished`, r.Text)
		mustMatch(t, `provider item\(s\) finished`, r.Text)
	})
	tw(t, f, "waits for both initial async and provider identities in all mode", func(t *testing.T) {
		root := t.TempDir()
		asyncRoot := filepath.Join(root, "runs")
		writeRunStatus(t, asyncRoot, "run-a", "running", "session-a")
		stage := 0
		work := &BackgroundWorkDeps{WakeChannels: func() []string { return nil }, Snapshot: func(string, int64) (BackgroundWorkSnapshot, error) {
			if stage < 2 {
				return snap([]string{"patty"}, RegisteredBackgroundWorkItem{"patty", "job-a", "session-a"}), nil
			}
			return snap([]string{"patty"}), nil
		}}
		r := waitForSubagents(WaitParams{All: true}, nil, bgWaitDeps(root, asyncState("session-a"), work, func(int64, <-chan struct{}) {
			stage++
			if stage == 1 {
				writeRunStatus(t, asyncRoot, "run-a", "complete", "session-a")
			}
		}))
		eq(t, r.IsError, false, "isError")
		mustMatch(t, `(?s)1 async run\(s\) and 1 provider item\(s\).*done`, r.Text)
		eq(t, stage, 2, "stage")
	})
	tw(t, f, "fails closed when a provider disappears or its snapshot throws", func(t *testing.T) {
		root := t.TempDir()
		calls := 0
		r := waitForSubagents(WaitParams{}, nil, bgWaitDeps(root, asyncState("session-a"), &BackgroundWorkDeps{WakeChannels: func() []string { return nil }, Snapshot: func(string, int64) (BackgroundWorkSnapshot, error) {
			calls++
			if calls == 1 {
				return snap([]string{"patty"}, RegisteredBackgroundWorkItem{"patty", "job-a", "session-a"}), nil
			}
			return snap(nil), nil
		}}, noSleep))
		eq(t, r.IsError, true, "disappeared")
		mustMatch(t, `provider 'patty' disappeared`, r.Text)
		r = waitForSubagents(WaitParams{}, nil, bgWaitDeps(root, asyncState("session-a"), &BackgroundWorkDeps{WakeChannels: func() []string { return nil }, Snapshot: func(string, int64) (BackgroundWorkSnapshot, error) {
			return BackgroundWorkSnapshot{}, fmt.Errorf("provider registry failed")
		}}, noSleep))
		eq(t, r.IsError, true, "failed")
		mustMatch(t, `provider registry failed`, r.Text)
	})
	tw(t, f, "can continue through needs-attention while draining", func(t *testing.T) {
		root := t.TempDir()
		asyncRoot := filepath.Join(root, "runs")
		writeRunStatus(t, asyncRoot, "run-a", "running", "session-a")
		statusPath := filepath.Join(asyncRoot, "run-a", "status.json")
		raw, _ := os.ReadFile(statusPath)
		var st map[string]any
		_ = json.Unmarshal(raw, &st)
		st["activityState"] = "needs_attention"
		b, _ := json.Marshal(st)
		writeFile(t, statusPath, string(b))
		polls := 0
		off := false
		deps := bgWaitDeps(root, asyncState("session-a"), &BackgroundWorkDeps{WakeChannels: func() []string { return nil }, Snapshot: func(string, int64) (BackgroundWorkSnapshot, error) { return snap(nil), nil }}, func(int64, <-chan struct{}) {
			polls++
			writeRunStatus(t, asyncRoot, "run-a", "complete", "session-a")
		})
		deps.StopOnAttention = &off
		r := waitForSubagents(WaitParams{All: true}, nil, deps)
		eq(t, r.IsError, false, "isError")
		eq(t, polls, 1, "polls")
		mustMatch(t, `done`, r.Text)
	})
	tw(t, f, "wakes on a provider channel instead of the poll interval", func(t *testing.T) {
		root := t.TempDir()
		var mu sync.Mutex
		active := true
		bus := newLocalBus()
		work := &BackgroundWorkDeps{WakeChannels: func() []string { return []string{"patty:finished"} }, Snapshot: func(string, int64) (BackgroundWorkSnapshot, error) {
			mu.Lock()
			defer mu.Unlock()
			if active {
				return snap([]string{"patty"}, RegisteredBackgroundWorkItem{"patty", "job-a", "session-a"}), nil
			}
			return snap([]string{"patty"}), nil
		}}
		deps := bgWaitDeps(root, asyncState("session-a"), work, func(ms int64, done <-chan struct{}) {
			select {
			case <-time.After(10 * time.Second):
			case <-done:
			}
		})
		deps.Events, deps.PollIntervalMs = bus, 10_000
		go func() {
			time.Sleep(15 * time.Millisecond)
			mu.Lock()
			active = false
			mu.Unlock()
			bus.Emit("patty:finished", nil)
		}()
		start := time.Now()
		r := waitForSubagents(WaitParams{}, nil, deps)
		eq(t, r.IsError, false, "isError")
		mustMatch(t, `provider item\(s\) finished`, r.Text)
		if time.Since(start) > 5*time.Second {
			t.Errorf("the wait slept the poll interval")
		}
	})
}

// ── completion-replay.test.ts and wait-completions.test.ts ──

func runSummary(id, sessionID, dir string) AsyncRunSummary {
	return AsyncRunSummary{ID: id, SessionID: sessionID, AsyncDir: dir, Mode: "single", State: "complete", StartedAt: nowMs()}
}

func chmodOrSkip(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("running as root: file permissions are not enforced")
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o755) })
}

func TestCompletionReplay(t *testing.T) {
	const f = "completion-replay"
	tw(t, f, "surfaces a consumed completion after in-memory watcher state is lost", func(t *testing.T) {
		resultsDir := t.TempDir()
		now := nowMs()
		recordWaitCompletion(asyncState("session-a"), "run-a", map[string]any{"runId": "run-a", "sessionId": "session-a", "agent": "worker", "mode": "single", "state": "complete", "success": true,
			"results": []any{map[string]any{"agent": "worker", "success": true, "outputState": "present", "output": "finished output", "contextOverflow": true}}}, now, 60_000,
			&struct{ ResultsDir, SessionID string }{resultsDir, "session-a"})
		replay, err := readCompletionReplay(resultsDir, "run-a", "session-a", now+1)
		eq(t, err, nil, "read")
		eq(t, replay.Version, 1, "version")
		eq(t, replay.Completion.ArchivePath, replay.ArchivePath, "archive path")
		archive, _ := readCompletionArchive(replay.ArchivePath)
		eq(t, archive.Entries[0], CompletionArchiveEntry{Agent: "worker", ResultIndex: intPtr(0), Source: "result-tail", Text: "finished output"}, "first entry")
		completions, err := collectWaitCompletions([]AsyncRunSummary{{ID: "run-a", SessionID: "session-a"}}, asyncState("session-a"), resultsDir, nil)
		eq(t, err, nil, "collect")
		eq(t, completions[0].RunID, "run-a", "run id")
		eq(t, completions[0].Results[0].Agent, "worker", "agent")
		eq(t, completions[0].Results[0].ContextOverflow, true, "overflow")
		eq(t, completions[0].ArchivePath, replay.ArchivePath, "archive")
	})
	tw(t, f, "surfaces pending completions when the direct session index is temporarily inaccessible", func(t *testing.T) {
		resultsDir := t.TempDir()
		publicPath := filepath.Join(resultsDir, "run-pending.json")
		eq(t, os.MkdirAll(publicPath, 0o755), nil, "mkdir")
		_, err := writeAsyncResultFile(publicPath, map[string]any{"id": "run-pending", "runId": "run-pending", "sessionId": "session-a", "agent": "worker", "success": true,
			"results": []any{map[string]any{"agent": "worker", "success": true, "outputState": "present"}}})
		eq(t, err, nil, "write")
		chmodOrSkip(t, filepath.Join(resultsDir, "result-index"), 0o000)
		completions, err := collectWaitCompletions([]AsyncRunSummary{{ID: "run-pending", SessionID: "session-a"}}, asyncState("session-a"), resultsDir, nil)
		eq(t, err, nil, "collect")
		eq(t, completions[0].RunID, "run-pending", "run id")
		eq(t, completions[0].Results[0].Agent, "worker", "agent")
	})
	tw(t, f, "surfaces malformed pending payload errors when the direct session index is inaccessible", func(t *testing.T) {
		resultsDir := t.TempDir()
		publicPath := filepath.Join(resultsDir, "run-pending.json")
		eq(t, os.MkdirAll(publicPath, 0o755), nil, "mkdir")
		_, _ = writeAsyncResultFile(publicPath, map[string]any{"id": "run-pending", "runId": "run-pending", "sessionId": "session-a", "success": true})
		eq(t, os.RemoveAll(publicPath), nil, "remove public")
		pendingDir := filepath.Join(resultsDir, "result-pending", "session-a")
		writeFile(t, filepath.Join(pendingDir, "run-pending.json"), "{")
		chmodOrSkip(t, filepath.Join(resultsDir, "result-index"), 0o000)
		_, err := collectWaitCompletions([]AsyncRunSummary{{ID: "run-pending", SessionID: "session-a"}}, asyncState("session-a"), resultsDir, nil)
		hasMatch(t, err, "Failed to read")
	})
	tw(t, f, "keeps UTF-8 tails valid at multibyte boundaries", func(t *testing.T) {
		text, truncated := utf8Tail("start-"+strings.Repeat("é", 10)+"-tail", 9)
		eq(t, truncated, true, "truncated")
		eq(t, text, "éé-tail", "text")
	})
	tw(t, f, "does not delete untrusted or cross-run archive paths from replay records", func(t *testing.T) {
		root := t.TempDir()
		resultsDir := filepath.Join(root, "results")
		replayDir := filepath.Join(resultsDir, "completion-replay")
		victim := filepath.Join(root, "victim.txt")
		writeFile(t, victim, "keep")
		replayPath := filepath.Join(replayDir, "run-c.json")
		record := func(runID, archive string) string {
			b, _ := json.Marshal(map[string]any{"version": 1, "runId": runID, "sessionId": "session-a", "completedAt": 1, "expiresAt": 2, "archivePath": archive, "completion": map[string]any{"runId": runID, "archivePath": archive}})
			return string(b)
		}
		writeFile(t, replayPath, record("run-c", victim))
		r, err := readCompletionReplay(resultsDir, "run-c", "session-a", 1)
		eq(t, err, nil, "read")
		eq(t, r == nil, true, "untrusted record reads as absent")
		got, _ := os.ReadFile(victim)
		eq(t, string(got), "keep", "victim kept")
		_, statErr := os.Stat(replayPath)
		eq(t, os.IsNotExist(statErr), true, "replay record removed")

		writeFile(t, replayPath, record("run-c", victim))
		cleanupCompletionReplay(resultsDir, 3, 60_000)
		got, _ = os.ReadFile(victim)
		eq(t, string(got), "keep", "victim kept after cleanup")
		_, statErr = os.Stat(replayPath)
		eq(t, os.IsNotExist(statErr), true, "replay removed by cleanup")

		victimArchive := completionArchivePath(resultsDir, "run-a")
		writeFile(t, victimArchive, "keep")
		writeFile(t, replayPath, record("run-a", victimArchive))
		cleanupCompletionReplay(resultsDir, 3, 60_000)
		got, _ = os.ReadFile(victimArchive)
		eq(t, string(got), "keep", "cross-run archive kept")
		_, statErr = os.Stat(replayPath)
		eq(t, os.IsNotExist(statErr), true, "cross-run record removed")
	})
	tw(t, f, "treats replay read deletion failures as best-effort cleanup", func(t *testing.T) {
		root := t.TempDir()
		resultsDir := filepath.Join(root, "results")
		replayDir := filepath.Join(resultsDir, "completion-replay")
		replayPath := filepath.Join(replayDir, "run-d.json")
		archivePath := completionArchivePath(resultsDir, "run-d")
		writeFile(t, archivePath, "archive")
		b, _ := json.Marshal(map[string]any{"version": 1, "runId": "run-d", "sessionId": "session-a", "completedAt": 1, "expiresAt": 2, "archivePath": archivePath, "completion": map[string]any{"runId": "run-d", "archivePath": archivePath}})
		writeFile(t, replayPath, string(b))
		chmodOrSkip(t, replayDir, 0o555)
		chmodOrSkip(t, filepath.Dir(archivePath), 0o555)
		r, err := readCompletionReplay(resultsDir, "run-d", "session-a", 3)
		eq(t, err, nil, "no error")
		eq(t, r == nil, true, "expired reads as absent")
		_, e1 := os.Stat(replayPath)
		_, e2 := os.Stat(archivePath)
		eq(t, e1, nil, "the record stays when it cannot be removed")
		eq(t, e2, nil, "the archive stays when it cannot be removed")
	})
	tw(t, f, "throttles cleanup while writing completion replay records", func(t *testing.T) {
		root := t.TempDir()
		scans := 0
		replayScanHook = func(string) { scans++ }
		t.Cleanup(func() { replayScanHook = nil })
		for i, id := range []string{"run-a", "run-b"} {
			_, err := writeCompletionReplay(root, id, "session-a", WaitCompletion{RunID: id}, map[string]any{"summary": "done"}, int64(10_000+i), 60_000)
			eq(t, err, nil, "write")
		}
		eq(t, scans, 1, "cleanup scans")
		eq(t, regularFileExists(completionReplayPath(root, "run-a")), true, "run-a replay")
		eq(t, regularFileExists(completionReplayPath(root, "run-b")), true, "run-b replay")
	})
	tw(t, f, "prefers saved outputs and bounds fallback output tails", func(t *testing.T) {
		root := t.TempDir()
		saved := filepath.Join(root, "saved-output.md")
		writeFile(t, saved, "saved")
		archivePath, err := writeCompletionArchive(root, "run-b", map[string]any{"results": []any{
			map[string]any{"agent": "saved", "output": "duplicate text", "artifactPaths": map[string]any{"outputPath": saved}},
			map[string]any{"agent": "fallback", "output": "start-" + strings.Repeat("x", 70*1024) + "-tail"},
		}}, nowMs())
		eq(t, err, nil, "write")
		archive, _ := readCompletionArchive(archivePath)
		eq(t, archive.Entries[0], CompletionArchiveEntry{Agent: "saved", ResultIndex: intPtr(0), Source: "output-artifact", Path: saved}, "saved entry")
		fb := archive.Entries[1]
		eq(t, fb.Source, "result-tail", "source")
		eq(t, fb.Agent, "fallback", "agent")
		eq(t, *fb.ResultIndex, 1, "index")
		eq(t, fb.Truncated, true, "truncated")
		if len(fb.Text) > 64*1024 {
			t.Errorf("tail too long: %d", len(fb.Text))
		}
		eq(t, strings.HasSuffix(fb.Text, "-tail"), true, "tail end kept")
		eq(t, strings.Contains(fb.Text, "duplicate text"), false, "no duplicate text")
	})
}

func collect(terminal []AsyncRunSummary, state *SubagentState, resultsDir string) ([]WaitCompletion, []string, error) {
	var refs []string
	c, err := collectWaitCompletions(terminal, state, resultsDir, func(s string) { refs = append(refs, s) })
	return c, refs, err
}

func TestWaitCompletionProjection(t *testing.T) {
	const f = "wait-completions"
	tw(t, f, "returns readable evidence references from the matched result namespace and its replay", func(t *testing.T) {
		root := t.TempDir()
		resultsDir := filepath.Join(root, "nested", "root-run")
		runID := "persona"
		data := map[string]any{"runId": runID, "sessionId": "owner", "results": []any{map[string]any{"agent": "persona", "output": "NESTED_FINDING"}}}
		resultPath := filepath.Join(resultsDir, runID+".json")
		_, err := writeAsyncResultFile(resultPath, data)
		eq(t, err, nil, "write nested")
		_, err = writeAsyncResultFile(filepath.Join(root, runID+".json"), map[string]any{"runId": runID, "sessionId": "owner", "results": []any{map[string]any{"output": "WRONG_NAMESPACE"}}})
		eq(t, err, nil, "write wrong")
		terminal := []AsyncRunSummary{runSummary(runID, "owner", root)}
		state := asyncState("owner")
		c, refs, err := collect(terminal, state, resultsDir)
		eq(t, err, nil, "collect")
		eq(t, c[0].RunID, runID, "run id")
		eq(t, refs, []string{fmt.Sprintf("Result [%s]: %s", runID, resultPath)}, "references")
		raw, _ := os.ReadFile(resultPath)
		mustMatch(t, "NESTED_FINDING", string(raw))
		completion, _ := toWaitCompletion(data, runID)
		replay, err := writeCompletionReplay(resultsDir, runID, "owner", completion, data, nowMs(), 60_000)
		eq(t, err, nil, "replay")
		eq(t, os.Remove(resultPath), nil, "remove result")
		c, refs, _ = collect(terminal, state, resultsDir)
		eq(t, c[0].ArchivePath, replay.ArchivePath, "archive")
		eq(t, refs, []string{fmt.Sprintf("Result [%s]: %s", runID, replay.ArchivePath)}, "archive reference")
		archive, _ := os.ReadFile(replay.ArchivePath)
		mustMatch(t, "NESTED_FINDING", string(archive))
		sibling := terminal[0]
		sibling.SessionID = "sibling"
		c, _, _ = collect([]AsyncRunSummary{sibling}, state, resultsDir)
		eq(t, c == nil, true, "a sibling session gets nothing")
	})
	tw(t, f, "only references an unindexed public fallback owned by the completed run session", func(t *testing.T) {
		resultsDir := t.TempDir()
		runID := "shared-run"
		terminal := []AsyncRunSummary{runSummary(runID, "owner", resultsDir)}
		state := asyncState("owner")
		recordWaitCompletion(state, runID, map[string]any{"runId": runID, "sessionId": "owner", "agent": "owner-agent", "success": true}, nowMs(), 60_000, nil)
		resultPath := filepath.Join(resultsDir, runID+".json")
		b, _ := json.Marshal(map[string]any{"runId": runID, "sessionId": "foreign", "agent": "foreign-agent", "state": "failed", "results": []any{map[string]any{"error": "FOREIGN_PAYLOAD"}}})
		writeFile(t, resultPath, string(b))
		c, refs, _ := collect(terminal, state, resultsDir)
		eq(t, len(refs), 0, "foreign references")
		eq(t, c[0].Agent, "owner-agent", "agent")
		eq(t, c[0].State, "", "state")
		eq(t, c[0].Results == nil, true, "results")
		out, _ := json.Marshal(c[0])
		eq(t, regexp.MustCompile(`foreign-agent|FOREIGN_PAYLOAD`).MatchString(string(out)), false, "no foreign data")
		b, _ = json.Marshal(map[string]any{"runId": runID, "sessionId": "owner", "agent": "owner-payload"})
		writeFile(t, resultPath, string(b))
		c, refs, _ = collect(terminal, state, resultsDir)
		eq(t, c[0].Agent, "owner-agent", "owner agent")
		eq(t, refs, []string{fmt.Sprintf("Result [%s]: %s", runID, resultPath)}, "owner references")
	})
	tw(t, f, "does not surface an in-memory completion after the run id is reused by another session", func(t *testing.T) {
		resultsDir := t.TempDir()
		runID := "shared-run"
		state := asyncState("session-b")
		durable := recordWaitCompletion(state, runID, map[string]any{"runId": runID, "sessionId": "session-a", "agent": "session-a-agent", "state": "failed", "results": []any{map[string]any{"error": "SESSION_A_ONLY"}}},
			nowMs(), 60_000, &struct{ ResultsDir, SessionID string }{resultsDir, "session-a"})
		eq(t, durable, true, "durable")
		c, refs, _ := collect([]AsyncRunSummary{{ID: runID, SessionID: "session-b"}}, state, resultsDir)
		eq(t, c == nil, true, "nothing surfaced")
		eq(t, len(refs), 0, "references")
		got, _ := state.CompletedResults.Get(runID)
		eq(t, got.SessionID, "session-a", "the record stays with its owner")
	})
	tw(t, f, "owner-gates the in-memory completion discovered during the result-file race", func(t *testing.T) {
		resultsDir := t.TempDir()
		runID := "shared-run"
		completion, _ := toWaitCompletion(map[string]any{"agent": "recorded-agent", "success": true}, runID)
		entry := completedResult{SessionID: "session-a", SeenAt: nowMs(), Completion: completion}
		collectFor := func(sessionID string) []WaitCompletion {
			state := asyncState(sessionID)
			state.CompletedResults = &lateStore{entry: entry}
			c, _ := collectWaitCompletions([]AsyncRunSummary{{ID: runID, SessionID: sessionID}}, state, resultsDir, nil)
			return c
		}
		eq(t, collectFor("session-b") == nil, true, "foreign session")
		eq(t, collectFor("session-a")[0].Agent, "recorded-agent", "owner")
	})
	tw(t, f, "refuses to record a completion without matching payload and persistence ownership", func(t *testing.T) {
		resultsDir := t.TempDir()
		state := asyncState("session-a")
		eq(t, recordWaitCompletion(state, "missing", map[string]any{"runId": "missing"}, nowMs(), 60_000, nil), false, "no session")
		eq(t, recordWaitCompletion(state, "expected-run", map[string]any{"runId": "foreign-run", "sessionId": "session-a", "agent": "foreign-agent"}, nowMs(), 60_000, nil), false, "foreign run")
		eq(t, recordWaitCompletion(state, "mismatch", map[string]any{"runId": "mismatch", "sessionId": "session-a"}, nowMs(), 60_000, &struct{ ResultsDir, SessionID string }{resultsDir, "session-b"}), false, "persistence owner")
		eq(t, state.CompletedResults == nil, true, "store untouched")
		eq(t, regularFileExists(filepath.Join(resultsDir, "completion-replay")), false, "no replay directory")
		_, err := os.Stat(filepath.Join(resultsDir, "completion-replay"))
		eq(t, os.IsNotExist(err), true, "no replay directory on disk")
	})
	tw(t, f, "rejects foreign unindexed public payloads before the watcher records completion", func(t *testing.T) {
		resultsDir := t.TempDir()
		runID := "shared-run"
		terminal := []AsyncRunSummary{runSummary(runID, "owner", resultsDir)}
		state := asyncState("owner")
		resultPath := filepath.Join(resultsDir, runID+".json")
		b, _ := json.Marshal(map[string]any{"runId": runID, "sessionId": "foreign", "agent": "foreign-agent", "state": "failed", "results": []any{map[string]any{"error": "FOREIGN_PAYLOAD"}}})
		writeFile(t, resultPath, string(b))
		c, refs, _ := collect(terminal, state, resultsDir)
		eq(t, c == nil, true, "foreign payload")
		eq(t, len(refs), 0, "references")
		b, _ = json.Marshal(map[string]any{"runId": runID, "sessionId": "owner", "agent": "owner-agent", "state": "complete", "success": true})
		writeFile(t, resultPath, string(b))
		c, refs, _ = collect(terminal, state, resultsDir)
		eq(t, c[0].Agent, "owner-agent", "agent")
		eq(t, c[0].State, "complete", "state")
		eq(t, *c[0].Success, true, "success")
		eq(t, refs, []string{fmt.Sprintf("Result [%s]: %s", runID, resultPath)}, "references")
	})
	tw(t, f, "validates stale indexed public payload ownership with and without memory", func(t *testing.T) {
		root := t.TempDir()
		for _, hasMemory := range []bool{false, true} {
			for _, owner := range []string{"owner", "foreign"} {
				name := "no-memory"
				if hasMemory {
					name = "memory"
				}
				resultsDir := filepath.Join(root, name+"-"+owner)
				runID := "shared-run"
				resultPath := filepath.Join(resultsDir, runID+".json")
				_, err := writeAsyncResultFile(resultPath, map[string]any{"runId": runID, "sessionId": "owner", "agent": "initial-owner"})
				eq(t, err, nil, "write")
				payload := map[string]any{"runId": runID, "sessionId": owner, "agent": owner + "-payload", "state": "failed"}
				if owner == "owner" {
					payload["state"] = "complete"
				} else {
					payload["results"] = []any{map[string]any{"error": "FOREIGN_PAYLOAD"}}
				}
				b, _ := json.Marshal(payload)
				writeFile(t, resultPath, string(b)) // the owner's index stays intact while the public payload is replaced
				terminal := []AsyncRunSummary{runSummary(runID, "owner", resultsDir)}
				state := asyncState("owner")
				if hasMemory {
					recordWaitCompletion(state, runID, map[string]any{"runId": runID, "sessionId": "owner", "agent": "memory-owner", "success": true}, nowMs(), 60_000, nil)
				}
				c, refs, _ := collect(terminal, state, resultsDir)
				if owner == "owner" {
					want := "owner-payload"
					if hasMemory {
						want = "memory-owner"
					}
					eq(t, c[0].Agent, want, "owner agent")
					eq(t, refs, []string{fmt.Sprintf("Result [%s]: %s", runID, resultPath)}, "owner reference")
				} else {
					if hasMemory {
						eq(t, c[0].Agent, "memory-owner", "memory agent")
					} else {
						eq(t, len(c), 0, "no completion")
					}
					eq(t, len(refs), 0, "no references")
					out, _ := json.Marshal(c)
					eq(t, regexp.MustCompile(`foreign-payload|FOREIGN_PAYLOAD|failed`).MatchString(string(out)), false, "no foreign data")
				}
			}
		}
	})
	tw(t, f, "reports malformed indexed payloads", func(t *testing.T) {
		resultsDir := t.TempDir()
		runID := "malformed-run"
		resultPath := filepath.Join(resultsDir, runID+".json")
		_, _ = writeAsyncResultFile(resultPath, map[string]any{"runId": runID, "sessionId": "owner", "agent": "initial-owner"})
		writeFile(t, resultPath, `{"runId":`)
		_, refs, err := collect([]AsyncRunSummary{runSummary(runID, "owner", resultsDir)}, asyncState("owner"), resultsDir)
		mustMatch(t, `Failed to read subagent result .*Unexpected end of JSON input`, err.Error())
		eq(t, len(refs), 0, "references")
	})
	tw(t, f, "omits absent and malformed receipt references", func(t *testing.T) {
		for _, receipt := range []any{nil, []any{}, "path", map[string]any{"path": ""}, map[string]any{"path": 42.0}} {
			c, err := toWaitCompletion(map[string]any{"workflowReceipt": receipt}, "run")
			eq(t, err, nil, "err")
			eq(t, c.WorkflowReceiptPath, "", "receipt path")
		}
	})
	tw(t, f, "retains the bounded workflow-child summary and excludes result output", func(t *testing.T) {
		c, err := toWaitCompletion(map[string]any{"agent": "workflow", "mode": "workflow", "workflowReceipt": map[string]any{"path": "/opaque/published-receipt.json", "receipt": map[string]any{"output": "must not be copied"}},
			"state": "complete", "success": true,
			"workflowChildren": map[string]any{"version": 1.0, "parentToolCallId": "tool-1", "workflowRunId": "workflow-1", "inventoryComplete": true, "workflowState": "completed",
				"children": []any{map[string]any{"childId": "review", "runId": "run-1", "agent": "reviewer", "model": "openai-codex/gpt", "thinking": "high", "state": "completed"}}},
			"results": []any{map[string]any{"agent": "reviewer", "runId": "run-1", "success": true, "sessionFile": "/sessions/run-1.jsonl",
				"usage":  map[string]any{"input": 10.0, "output": 2.0, "cacheRead": 30.0, "cacheWrite": 0.0, "cost": 0.04, "turns": 1.0},
				"output": "must not be copied", "task": "must not be copied"}}}, "workflow-1")
		eq(t, err, nil, "err")
		eq(t, c.WorkflowChildren.Children[0].ChildID, "review", "child id")
		eq(t, c.WorkflowReceiptPath, "/opaque/published-receipt.json", "receipt")
		eq(t, *c.Results[0].Usage, Usage{10, 2, 30, 0, 0.04, 1}, "usage")
		eq(t, c.Results[0].SessionFile, "/sessions/run-1.jsonl", "session file")
		out, _ := json.Marshal(c)
		eq(t, strings.Contains(string(out), "must not be copied"), false, "no output copied")
	})
	tw(t, f, "retains only bounded timeout recovery evidence in completion details", func(t *testing.T) {
		var files []any
		var names []string
		for i := 1; i <= 25; i++ {
			n := fmt.Sprintf("src/file-%02d.ts", i)
			files = append(files, n)
			names = append(names, n)
		}
		c, _ := toWaitCompletion(map[string]any{"state": "failed", "success": false, "results": []any{map[string]any{"agent": "worker", "success": false, "timeoutRecovery": map[string]any{
			"termination": "timed-out", "changedFiles": files, "truncated": true, "recoveryNeeded": true, "reason": "timed-out-with-dirty-worktree", "reportStatus": "missing",
			"message": "raw recovery message must not cross the completion boundary", "effects": map[string]any{"settlementDiagnostic": map[string]any{"finalTextPresent": true}}}}}}, "run-recovery")
		eq(t, *c.Results[0].TimeoutRecovery, TimeoutRecoveryProjection{Termination: "timed-out", ChangedFiles: names[:20], Truncated: true, RecoveryNeeded: true, Reason: "timed-out-with-dirty-worktree", ReportStatus: "missing"}, "projection")
		out, _ := json.Marshal(c)
		eq(t, regexp.MustCompile(`raw recovery message|settlementDiagnostic`).MatchString(string(out)), false, "no raw evidence")
	})
	tw(t, f, "retains captured structured output and its durable artifact path", func(t *testing.T) {
		c, _ := toWaitCompletion(map[string]any{"success": true, "results": []any{map[string]any{"agent": "delegate", "success": true, "output": "",
			"structuredOutput": map[string]any{"payload": map[string]any{"ok": true}, "contract_checks": map[string]any{}}, "structuredOutputPath": "/runs/structured-output/output.json"}}}, "run-structured")
		eq(t, c.Results[0].StructuredOutput, map[string]any{"payload": map[string]any{"ok": true}, "contract_checks": map[string]any{}}, "structured output")
		eq(t, c.Results[0].StructuredOutputPath, "/runs/structured-output/output.json", "path")
	})
	tw(t, f, "omits oversized structured output while retaining its artifact path", func(t *testing.T) {
		c, _ := toWaitCompletion(map[string]any{"success": true, "results": []any{map[string]any{"agent": "delegate",
			"structuredOutput": map[string]any{"payload": strings.Repeat("x", 8000)}, "structuredOutputPath": "/runs/structured-output/output.json"}}}, "run-large-structured")
		eq(t, c.Results[0].StructuredOutput, nil, "oversized dropped")
		eq(t, c.Results[0].StructuredOutputPath, "/runs/structured-output/output.json", "path kept")
	})
	tw(t, f, "rejects non-JSON structured output", func(t *testing.T) {
		_, err := toWaitCompletion(map[string]any{"success": true, "results": []any{map[string]any{"structuredOutput": make(chan int)}}}, "run-invalid-structured")
		hasMatch(t, err, "JSON-serializable")
	})
	tw(t, f, "rejects unbounded or unknown summary fields at the replay boundary", func(t *testing.T) {
		_, err := toWaitCompletion(map[string]any{"workflowChildren": map[string]any{"version": 1.0, "parentToolCallId": "tool", "workflowRunId": "run", "inventoryComplete": true, "workflowState": "completed", "children": []any{}, "output": "secret"}}, "run")
		hasMatch(t, err, "unsupported fields")
	})
	tw(t, f, "rejects a summary bound to another completion", func(t *testing.T) {
		_, err := toWaitCompletion(map[string]any{"workflowChildren": map[string]any{"version": 1.0, "parentToolCallId": "tool", "workflowRunId": "other", "inventoryComplete": true, "workflowState": "completed", "children": []any{}}}, "run")
		hasMatch(t, err, "does not match its completion run id")
	})
}

// lateStore misses the first read of its entry, as the original's LateCompletionMap does (the result-file race).
type lateStore struct {
	entry completedResult
	reads int
}

func (s *lateStore) Get(string) (completedResult, bool) {
	s.reads++
	if s.reads == 1 {
		return completedResult{}, false
	}
	return s.entry, true
}
func (s *lateStore) Set(string, completedResult) {}
func (s *lateStore) Prune(int64, int64)          {}

// ── auto-drain.test.ts ──

func drainWaitResult(text string, isError, windowElapsed bool) WaitResult {
	r := WaitResult{Text: text, IsError: isError}
	if windowElapsed {
		r.Wait = &WaitDetails{Reason: "window_elapsed", TimedOut: true, ActiveRunIDs: []string{"run-a"}, ActiveProviderItems: []ProviderID{}}
	}
	return r
}

func i64(v int64) *int64 { return &v }

func TestAutoDrain(t *testing.T) {
	const f = "auto-drain"
	tw(t, f, "discovers both owned storage scopes, forwards them to wait, and leaves sibling work alone", func(t *testing.T) {
		root := t.TempDir()
		t.Setenv("PI_SUBAGENTS_TEMP_ROOT", filepath.Join(root, "temp"))
		nestedRoot := "0b6e8a52-6f0d-4c1e-9a3b-aaaaaaaaaaaa"
		nestedAsync, _ := nestedRunScope(nestedRoot)
		ordinary := waitScope{filepath.Join(root, "runs"), filepath.Join(root, "results")}
		owned := []string{filepath.Join(ordinary.AsyncDirRoot, "workflow"), filepath.Join(nestedAsync, "persona")}
		write := func(dir, session, state string) {
			writeRunStatusPid(t, filepath.Dir(dir), filepath.Base(dir), state, session, os.Getpid())
		}
		for _, d := range owned {
			write(d, "owner", "running")
		}
		sibling := filepath.Join(nestedAsync, "sibling-persona")
		write(sibling, "sibling-session", "running")
		waits := 0
		err := drainOutstandingWork(AutoDrainDeps{State: asyncState("owner"), AsyncDirRoot: ordinary.AsyncDirRoot, ResultsDir: ordinary.ResultsDir, NestedRootRunID: nestedRoot,
			Wait: func(_ WaitParams, _ <-chan struct{}, deps WaitDeps) WaitResult {
				eq(t, deps.NestedRootRunID, nestedRoot, "nested root")
				eq(t, deps.AsyncDirRoot, ordinary.AsyncDirRoot, "async root")
				eq(t, deps.ResultsDir, ordinary.ResultsDir, "results dir")
				write(owned[waits], "owner", "complete")
				waits++
				return drainWaitResult("done", false, false)
			}})
		eq(t, err, nil, "drain")
		eq(t, waits, 2, "drain must continue when only nested owned work remains")
		st, _ := readStatus(sibling)
		eq(t, st.State, "running", "the sibling's run is left alone")
	})
	tw(t, f, "is a no-op when the exact session has no work", func(t *testing.T) {
		waited := false
		err := drainOutstandingWork(AutoDrainDeps{State: asyncState("session-a"), HasWork: func(string, int64) (bool, error) { return false, nil },
			Wait: func(WaitParams, <-chan struct{}, WaitDeps) WaitResult {
				waited = true
				return drainWaitResult("unexpected", false, false)
			}})
		eq(t, err, nil, "drain")
		eq(t, waited, false, "waited")
	})
	tw(t, f, "loops until work added while draining is also gone", func(t *testing.T) {
		checks := 0
		var waits []struct {
			params WaitParams
			deps   WaitDeps
		}
		err := drainOutstandingWork(AutoDrainDeps{State: asyncState("session-a"), TimeoutMs: i64(1000), Now: func() int64 { return int64(checks * 10) },
			HasWork: func(string, int64) (bool, error) { checks++; return checks-1 < 2, nil },
			Wait: func(p WaitParams, _ <-chan struct{}, d WaitDeps) WaitResult {
				waits = append(waits, struct {
					params WaitParams
					deps   WaitDeps
				}{p, d})
				return drainWaitResult("done", false, false)
			}})
		eq(t, err, nil, "drain")
		eq(t, len(waits), 2, "waits")
		for _, w := range waits {
			eq(t, w.params.All, true, "all")
			eq(t, w.deps.StopOnAttention != nil && !*w.deps.StopOnAttention, true, "stopOnAttention false")
			eq(t, w.deps.FailOnFailedRuns, true, "failOnFailedRuns")
			eq(t, w.deps.FailOnAttention, true, "failOnAttention")
		}
		if *waits[1].params.TimeoutMs >= *waits[0].params.TimeoutMs {
			t.Errorf("each wait must share one absolute deadline: %d then %d", *waits[0].params.TimeoutMs, *waits[1].params.TimeoutMs)
		}
	})
	tw(t, f, "preserves wait errors instead of treating them as a successful drain", func(t *testing.T) {
		err := drainOutstandingWork(AutoDrainDeps{State: asyncState("session-a"), HasWork: func(string, int64) (bool, error) { return true, nil },
			Wait: func(WaitParams, <-chan struct{}, WaitDeps) WaitResult {
				return drainWaitResult("provider 'patty' snapshot failed", true, false)
			}})
		mustMatch(t, `Auto-drain failed.*provider 'patty' snapshot failed`, err.Error())
	})
	tw(t, f, "propagates work-discovery errors", func(t *testing.T) {
		err := drainOutstandingWork(AutoDrainDeps{State: asyncState("session-a"), HasWork: func(string, int64) (bool, error) { return false, fmt.Errorf("provider reconcile failed") }})
		hasMatch(t, err, "provider reconcile failed")
	})
	tw(t, f, "keeps its absolute deadline strict after a non-error wait window elapses", func(t *testing.T) {
		clock := int64(0)
		err := drainOutstandingWork(AutoDrainDeps{State: asyncState("session-a"), TimeoutMs: i64(100), Now: func() int64 { return clock },
			HasWork: func(string, int64) (bool, error) { return true, nil },
			Wait: func(WaitParams, <-chan struct{}, WaitDeps) WaitResult {
				clock = 101
				return drainWaitResult("Wait window elapsed; work remains active.", false, true)
			}})
		mustMatch(t, `timed out after 100ms.*session 'session-a'`, err.Error())
	})
	tw(t, f, "fails without a session identity", func(t *testing.T) {
		err := drainOutstandingWork(AutoDrainDeps{State: asyncState("")})
		hasMatch(t, err, "without an active session identity")
	})
	tw(t, f, "does not settle while a remembered detached foreground descendant is still in flight", func(t *testing.T) {
		cur := asyncState("owner")
		cur.ForegroundRuns = map[string]*ForegroundRunRecord{"fg": {RunID: "fg", SessionID: "owner", Children: []ForegroundChildRecord{{Agent: "reviewer", Index: 0, Status: "detached"}}}}
		waits := 0
		err := drainOutstandingWork(AutoDrainDeps{State: cur, TimeoutMs: i64(1000), Now: func() int64 { return int64(waits * 10) },
			Wait: func(WaitParams, <-chan struct{}, WaitDeps) WaitResult {
				waits++
				cur.ForegroundRuns["fg"].Children[0].Status = "completed"
				return drainWaitResult("done", false, false)
			}, AsyncDirRoot: t.TempDir(), ResultsDir: t.TempDir()})
		eq(t, err, nil, "drain")
		eq(t, waits, 1, "waits")
	})
	tw(t, f, "yields to Pi for a pending supervisor turn and resumes draining the same work after resolution", func(t *testing.T) {
		pending, active, waits := true, true, 0
		deps := AutoDrainDeps{State: asyncState("session-a"), HasPendingSupervisorRequest: func() bool { return pending },
			HasWork: func(string, int64) (bool, error) { return active, nil },
			Wait: func(WaitParams, <-chan struct{}, WaitDeps) WaitResult {
				waits++
				active = false
				return drainWaitResult("done", false, false)
			}}
		eq(t, drainOutstandingWork(deps), nil, "first drain")
		eq(t, waits, 0, "the queued supervisor triggerTurn must get control before drain blocks again")
		eq(t, active, true, "yielding must leave the owned workflow active")
		pending = false
		eq(t, drainOutstandingWork(deps), nil, "second drain")
		eq(t, waits, 1, "the same workflow can continue and drain after the reply resolves the barrier")
		eq(t, active, false, "active")
	})
	tw(t, f, "latches an inner supervisor yield even when the live request clears before continuation", func(t *testing.T) {
		pending, clock, waits := false, int64(0), 0
		err := drainOutstandingWork(AutoDrainDeps{State: asyncState("session-a"), TimeoutMs: i64(50), Now: func() int64 { return clock },
			HasWork: func(string, int64) (bool, error) { return true, nil }, HasPendingSupervisorRequest: func() bool { return pending },
			Wait: func(_ WaitParams, _ <-chan struct{}, d WaitDeps) WaitResult {
				waits++
				eq(t, d.HasPendingSupervisorRequest != nil, true, "the supervisor probe is forwarded")
				clock, pending = 100, false
				return WaitResult{Text: "Wait yielded for a pending supervisor request.", Wait: &WaitDetails{Reason: "supervisor_request", ActiveRunIDs: []string{"run-a"}, ActiveProviderItems: []ProviderID{}}}
			}})
		eq(t, err, nil, "drain")
		eq(t, waits, 1, "waits")
	})
}
