package settings

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
)

// N1 (pass 4): Lock-order deadlock regression test matching the reviewer's exact
// stress shape: child readers + child override writer + two sessions saving +
// one OnChange listener for 20,000+ operations under -race with a 30s no-progress
// watchdog (fails if progress stalls, never a timing assertion on success).
func TestNoLockOrderDeadlockUnderChildReadsAndSaves(t *testing.T) {
	ResetForTests()
	t.Cleanup(func() {
		ResetForTests()
		lifecycle.ResetForTests()
	})
	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	_ = os.MkdirAll(agentDir, 0o755)
	cfgPath := filepath.Join(agentDir, "config.yml")
	_ = os.WriteFile(cfgPath, []byte("theme:\n  dark: nord\nread:\n  defaultLimit: 300\n"), 0o600)

	// Register parent, child, and peer sessions in the single sessions table.
	parentStore, err := NewStore(StoreOptions{AgentDir: agentDir, SessionID: "deadlock-parent"})
	if err != nil {
		t.Fatal(err)
	}
	childStore := buildSessionStore(StoreOptions{AgentDir: agentDir, SessionID: "deadlock-child"}, "deadlock-parent")
	peerStore, err := NewStore(StoreOptions{AgentDir: agentDir, SessionID: "deadlock-peer"})
	if err != nil {
		t.Fatal(err)
	}
	sessionsMu.Lock()
	sessions["deadlock-parent"] = &sessionState{store: parentStore, warned: map[string]bool{}}
	sessions["deadlock-child"] = &sessionState{store: childStore, warned: map[string]bool{}, parentID: "deadlock-parent"}
	sessions["deadlock-peer"] = &sessionState{store: peerStore, warned: map[string]bool{}}
	sessionsMu.Unlock()

	var listenerCalls atomic.Int64
	remove := OnChange(func(_, _ string) {
		listenerCalls.Add(1)
	})
	defer remove()

	var ops atomic.Int64
	const targetOps = 20000
	done := make(chan struct{})

	// Watchdog: fail immediately if no progress for 30s (deadlock detector).
	watchdogStop := make(chan struct{})
	watchdogErr := make(chan string, 1)
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		lastOps := ops.Load()
		lastAdvance := time.Now()
		for {
			select {
			case <-watchdogStop:
				return
			case <-ticker.C:
				cur := ops.Load()
				if cur != lastOps {
					lastOps = cur
					lastAdvance = time.Now()
				} else if time.Since(lastAdvance) >= 30*time.Second {
					watchdogErr <- fmt.Sprintf("deadlock detected: no progress for 30s stuck at %d/%d ops", cur, targetOps)
					return
				}
			}
		}
	}()

	var wg sync.WaitGroup
	// 4 child readers constantly reading settings + provenance (walking parent chain).
	for r := range 4 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				if ops.Load() >= targetOps {
					return
				}
				_ = childStore.String("theme.dark")
				_ = childStore.Int("read.defaultLimit")
				_ = childStore.Provenance("theme.dark")
				ops.Add(1)
			}
		}(r)
	}
	// 1 child override writer + 1 parent override writer.
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; ops.Load() < targetOps; i++ {
			if i%2 == 0 {
				_ = childStore.Set("theme.light", fmt.Sprintf("c-%d", i), false)
			} else {
				childStore.ClearOverride("theme.light")
			}
			ops.Add(1)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; ops.Load() < targetOps; i++ {
			_ = parentStore.Set("theme.dark", fmt.Sprintf("p-%d", i), false)
			ops.Add(1)
		}
	}()
	// 2 sessions performing real persisted saves (exercising snapshotAffectedSessionsForKey + file lock + sync).
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; ops.Load() < targetOps && i < 120; i++ {
			_ = parentStore.Set("read.defaultLimit", 300+(i%50), true)
			ops.Add(1)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; ops.Load() < targetOps && i < 120; i++ {
			_ = peerStore.Set("ask.timeout", i%60, true)
			ops.Add(1)
		}
	}()

	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case msg := <-watchdogErr:
		t.Fatal(msg)
	case <-done:
		close(watchdogStop)
	}
	if ops.Load() < targetOps {
		t.Fatalf("completed only %d ops, want >= %d", ops.Load(), targetOps)
	}
	if listenerCalls.Load() == 0 {
		t.Error("expected OnChange listener to be invoked during stress run")
	}
}

// N2 (pass 4): Reload / resume reuses the same session ID after session_shutdown.
// A late call between session_shutdown and the next session_start stays suppressed,
// then session_start clears the tombstone so project layer, session overrides, and
// OnChange all work again with the reviewer's exact test values.
func TestSessionReloadAndResumeReuseSameSessionID(t *testing.T) {
	ResetForTests()
	t.Cleanup(func() {
		ResetForTests()
		lifecycle.ResetForTests()
	})
	tmp := t.TempDir()
	t.Setenv("PIG_HOME", tmp)
	t.Setenv("PIG_CODING_AGENT_DIR", "")
	cwd := filepath.Join(tmp, "proj")
	_ = os.MkdirAll(filepath.Join(cwd, ".pig"), 0o755)
	_ = os.WriteFile(filepath.Join(cwd, ".pig", "config.yml"), []byte("theme:\n  dark: fromproject\n"), 0o600)

	var (
		mu     sync.Mutex
		events []string
	)
	remove := OnChange(func(sessionID, key string) {
		mu.Lock()
		events = append(events, sessionID+":"+key)
		mu.Unlock()
	})
	defer remove()

	// Instance 1 (before /reload)
	ext1 := sdk.New("reload-1")
	Register(ext1)
	var dark1 string
	var limit1 int
	ext1.Tool("op", "op", map[string]any{"type": "object"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		if v, ok := params["setLimit"].(float64); ok {
			_ = Set(ctx, "read.defaultLimit", int(v), false)
		}
		dark1 = String(ctx, "theme.dark")
		limit1 = Int(ctx, "read.defaultLimit")
		return sdk.ToolResult{Content: "ok"}, nil
	})
	h1 := startHost(t, ext1, hostOptions{
		Mode:           "tui",
		Cwd:            cwd,
		SessionID:      "same-sess",
		ProjectTrusted: true,
	})

	_, _ = h1.callTool("op", map[string]any{"setLimit": 42})
	if dark1 != "fromproject" || limit1 != 42 {
		t.Fatalf("before reload: theme.dark=%q read.defaultLimit=%d, want fromproject/42", dark1, limit1)
	}

	// Fire session_shutdown{reason: "reload"} -> tombstones "same-sess".
	_, fail := h1.fire(sdk.EventSessionShutdown, map[string]any{"type": sdk.EventSessionShutdown, "reason": "reload"})
	if fail != "" {
		t.Fatalf("session_shutdown{reload}: %s", fail)
	}

	// Late call BETWEEN shutdown and the next session_start must stay suppressed (no table entry, no OnChange).
	mu.Lock()
	events = nil
	mu.Unlock()
	_, _ = h1.callTool("op", map[string]any{"setLimit": 99})
	sessionsMu.Lock()
	_, recreatedEarly := sessions["same-sess"]
	sessionsMu.Unlock()
	if recreatedEarly {
		t.Fatal("late call between session_shutdown and session_start re-created sessions entry")
	}
	mu.Lock()
	earlyEvents := len(events)
	mu.Unlock()
	if earlyEvents != 0 {
		t.Fatalf("late call between shutdown and reload emitted %d OnChange events, want 0", earlyEvents)
	}

	// Instance 2 (fresh extension after /reload, SAME session ID) receives session_start{reload}.
	ext2 := sdk.New("reload-2")
	Register(ext2)
	var dark2 string
	var limit2 int
	ext2.Tool("op", "op", map[string]any{"type": "object"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		if v, ok := params["setLimit"].(float64); ok {
			_ = Set(ctx, "read.defaultLimit", int(v), false)
		}
		dark2 = String(ctx, "theme.dark")
		limit2 = Int(ctx, "read.defaultLimit")
		return sdk.ToolResult{Content: "ok"}, nil
	})
	h2 := startHost(t, ext2, hostOptions{
		Mode:           "tui",
		Cwd:            cwd,
		SessionID:      "same-sess",
		ProjectTrusted: true,
	})
	_, fail = h2.fire(sdk.EventSessionStart, map[string]any{"type": sdk.EventSessionStart, "reason": "reload"})
	if fail != "" {
		t.Fatalf("session_start{reload}: %s", fail)
	}

	// After reload: project layer is back (fromproject), fresh override (77) persists, and OnChange fires!
	_, _ = h2.callTool("op", map[string]any{"setLimit": 77})
	if dark2 != "fromproject" {
		t.Fatalf("after reload: theme.dark=%q, want fromproject (N2)", dark2)
	}
	if limit2 != 77 {
		t.Fatalf("after reload: read.defaultLimit=%d, want 77 (session override discarded, N2)", limit2)
	}
	// Read again in a separate tool call to confirm override persisted on the session entry.
	_, _ = h2.callTool("op", map[string]any{})
	if limit2 != 77 {
		t.Fatalf("after reload second read: read.defaultLimit=%d, want 77", limit2)
	}
	mu.Lock()
	gotEvents := append([]string(nil), events...)
	mu.Unlock()
	if len(gotEvents) != 1 || gotEvents[0] != "same-sess:read.defaultLimit" {
		t.Fatalf("after reload OnChange events = %v, want [same-sess:read.defaultLimit]", gotEvents)
	}
}

// N3 + F1: Stale `.<basename>.*.tmp` siblings left behind by a killed writer for
// THIS target are deleted under its file lock before writing; temp files for a
// different target in the same directory, unrelated files, directories, and
// symlinks are untouched.
func TestSaveCleansUpOrphanedConfigTempFiles(t *testing.T) {
	ResetForTests()
	t.Cleanup(ResetForTests)
	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	_ = os.MkdirAll(agentDir, 0o755)

	orphan1 := filepath.Join(agentDir, ".config.yml.11111.tmp")
	orphan2 := filepath.Join(agentDir, ".config.yml.dead.tmp")
	keepOtherTarget := filepath.Join(agentDir, ".other.yml.99999.tmp")
	keepOther := filepath.Join(agentDir, "other.tmp")
	keepDir := filepath.Join(agentDir, ".config.yml.dir.tmp")
	_ = os.WriteFile(orphan1, []byte("partial"), 0o600)
	_ = os.WriteFile(orphan2, []byte("partial"), 0o600)
	_ = os.WriteFile(keepOtherTarget, []byte("in-flight-other"), 0o600)
	_ = os.WriteFile(keepOther, []byte("keep"), 0o600)
	_ = os.MkdirAll(keepDir, 0o755)

	var symTmp string
	if runtime.GOOS != "windows" {
		target := filepath.Join(tmp, "real.txt")
		_ = os.WriteFile(target, []byte("real"), 0o600)
		symTmp = filepath.Join(agentDir, ".config.yml.symlink.tmp")
		_ = os.Symlink(target, symTmp)
	}

	st, err := NewStore(StoreOptions{AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Set("ask.timeout", 15, true); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{orphan1, orphan2} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("orphaned temp file %s was not cleaned up", p)
		}
	}
	for _, p := range []string{keepOtherTarget, keepOther, keepDir} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("unrelated sibling %s was deleted: %v", p, err)
		}
	}
	if symTmp != "" {
		if _, err := os.Lstat(symTmp); err != nil {
			t.Errorf("symlink %s should not be deleted by temp cleanup: %v", symTmp, err)
		}
	}
}

// F1: Two different config targets sharing a single directory (via symlinks into
// one dotfiles dir, e.g. dotfiles/a.yml and dotfiles/b.yml) saving concurrently
// from two goroutines AND from two real OS processes must suffer zero failed saves.
func TestTwoTargetsInSameDirectoryConcurrentSaves(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file symlinks require elevated privileges on Windows")
	}
	ResetForTests()
	t.Cleanup(ResetForTests)
	tmp := t.TempDir()
	sharedDir := filepath.Join(tmp, "dotfiles")
	agentA := filepath.Join(tmp, "agentA")
	agentB := filepath.Join(tmp, "agentB")
	_ = os.MkdirAll(sharedDir, 0o755)
	_ = os.MkdirAll(agentA, 0o755)
	_ = os.MkdirAll(agentB, 0o755)

	targetA := filepath.Join(sharedDir, "a.yml")
	targetB := filepath.Join(sharedDir, "b.yml")
	_ = os.WriteFile(targetA, []byte("read:\n  defaultLimit: 100\n"), 0o600)
	_ = os.WriteFile(targetB, []byte("read:\n  defaultLimit: 100\n"), 0o600)
	if err := os.Symlink(targetA, filepath.Join(agentA, "config.yml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(targetB, filepath.Join(agentB, "config.yml")); err != nil {
		t.Fatal(err)
	}

	// 1. Two goroutines saving concurrently to a.yml and b.yml in sharedDir.
	stA, err := NewStore(StoreOptions{AgentDir: agentA})
	if err != nil {
		t.Fatal(err)
	}
	stB, err := NewStore(StoreOptions{AgentDir: agentB})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	for idx, st := range []*Store{stA, stB} {
		wg.Add(1)
		go func(i int, s *Store) {
			defer wg.Done()
			for round := range 100 {
				if err := s.Set("read.defaultLimit", 200+round, true); err != nil {
					errCh <- fmt.Errorf("goroutine %d round %d: %w", i, round, err)
					return
				}
			}
		}(idx, st)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}

	// 2. Two real OS processes saving concurrently to a.yml and b.yml in sharedDir.
	if testing.Short() {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	var pwg sync.WaitGroup
	procErrs := make([]error, 2)
	procOuts := make([]string, 2)
	dirs := []string{agentA, agentB}
	for i, d := range dirs {
		pwg.Add(1)
		go func(idx int, dir string) {
			defer pwg.Done()
			cmd := exec.Command(exe, "-test.run=^TestRealProcessHelper$")
			cmd.Env = append(os.Environ(),
				"PIG_SETTINGS_PROC_HELPER=1",
				"HELPER_AGENT_DIR="+dir,
				"HELPER_KEY=ask.timeout",
				"HELPER_VAL=10",
				"HELPER_KIND=int",
				"HELPER_ITERS=100",
			)
			out, err := cmd.CombinedOutput()
			procOuts[idx] = string(out)
			procErrs[idx] = err
		}(i, d)
	}
	pwg.Wait()
	for i, err := range procErrs {
		if err != nil {
			t.Fatalf("process %d failed: %v\noutput:\n%s", i, err, procOuts[i])
		}
	}
}

// N4 (pass 4):
//  1. 300 saves with 4 concurrent reader goroutines produce EXACTLY 300 OnChange
//     events for the saving session (no duplicate external-refresh emission).
//  2. External edit that makes the global file malformed emits OnChange for keys
//     falling back to defaults.
func TestOnChangeExactlyOnceUnderConcurrentReadsAndOnMalformedExternalEdit(t *testing.T) {
	ResetForTests()
	t.Cleanup(ResetForTests)
	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	_ = os.MkdirAll(agentDir, 0o755)
	cfgPath := filepath.Join(agentDir, "config.yml")
	_ = os.WriteFile(cfgPath, []byte("read:\n  defaultLimit: 100\n"), 0o600)

	st, err := NewStore(StoreOptions{AgentDir: agentDir, SessionID: "sess-exact"})
	if err != nil {
		t.Fatal(err)
	}
	sessionsMu.Lock()
	sessions["sess-exact"] = &sessionState{store: st, warned: map[string]bool{}}
	sessionsMu.Unlock()

	var exactCount atomic.Int64
	remove := OnChange(func(sessionID, key string) {
		if sessionID == "sess-exact" && key == "read.defaultLimit" {
			exactCount.Add(1)
		}
	})
	defer remove()

	stopReaders := make(chan struct{})
	var rwg sync.WaitGroup
	for range 4 {
		rwg.Add(1)
		go func() {
			defer rwg.Done()
			for {
				select {
				case <-stopReaders:
					return
				default:
					_ = st.Int("read.defaultLimit")
				}
			}
		}()
	}

	const totalSaves = 150
	for i := range totalSaves {
		if err := st.Set("read.defaultLimit", 200+i, true); err != nil {
			close(stopReaders)
			rwg.Wait()
			t.Fatalf("save %d failed: %v", i, err)
		}
	}
	close(stopReaders)
	rwg.Wait()

	if got := exactCount.Load(); got != totalSaves {
		t.Fatalf("expected exactly %d OnChange events under concurrent readers, got %d (N4)", totalSaves, got)
	}

	// Part 2: external edit makes config.yml unparseable -> read.defaultLimit falls back
	// from 349 to default 300, and OnChange MUST fire.
	exactCount.Store(0)
	_ = os.WriteFile(cfgPath, []byte("read:\n  defaultLimit: \"unterminated\n"), 0o600)
	future := time.Now().Add(5 * time.Second)
	_ = os.Chtimes(cfgPath, future, future)

	if got := st.Int("read.defaultLimit"); got != 300 {
		t.Fatalf("after malformed external edit: read.defaultLimit = %d, want default 300", got)
	}
	if got := exactCount.Load(); got != 1 {
		t.Fatalf("malformed external edit emitted %d OnChange events, want 1 (N4)", got)
	}
	if st.LoadError() == nil || !strings.Contains(st.LoadError().Error(), "line 2") {
		t.Fatalf("expected LoadError naming line 2, got %v", st.LoadError())
	}
}
