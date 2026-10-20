package pi_goal_x

// Twins of tests/fault-injection.test.ts: crash/interruption states and multi-process contention through the persistence layer (torn
// writes, torn ledger tails, stale locks, cross-process appends). The children are this test binary re-run as a helper process.

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func faultCwd(t *testing.T) string {
	t.Helper()
	cwd, err := os.MkdirTemp("", "goal-fault-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(cwd) })
	if err := os.MkdirAll(filepath.Join(cwd, ".pi", "goals", "archived"), 0o755); err != nil {
		t.Fatal(err)
	}
	return cwd
}

func faultInvalidateAll() {
	invalidateGoalPoolCache()
	invalidateGoalLedgerCache()
}

// TestFaultChildProcess is the child the multi-process twins run (the original's `node -e` scripts); a plain run does nothing.
func TestFaultChildProcess(t *testing.T) {
	role, cwd := os.Getenv("GOAL_FAULT_ROLE"), os.Getenv("GOAL_FAULT_CWD")
	if role == "" {
		t.Skip("helper process")
	}
	st := storage{cwd: cwd}
	switch role {
	case "hold-lock":
		lock, err := acquireGoalLock(st, "g2", 0, 0)
		if err != nil {
			fmt.Println("ERR", err)
			os.Exit(2)
		}
		fmt.Println("HELD")
		time.Sleep(8 * time.Second)
		lock.release()
	case "write":
		g := createGoal(os.Getenv("GOAL_FAULT_OBJECTIVE"), true, false, false, time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC).UnixMilli())
		for i := 0; i < 50; i++ {
			if _, err := st.writeActiveGoalFile(g); err != nil {
				fmt.Println("ERR", err)
				os.Exit(2)
			}
		}
	case "append":
		g := createGoal("child goal", true, false, false, time.Date(2026, 9, 7, 11, 1, 0, 0, time.UTC).UnixMilli())
		if _, err := st.writeActiveGoalFile(g); err != nil {
			os.Exit(2)
		}
		line := marshalJSON(ledgerEvent("goal_created", "goalId", gstr(g, "id"), "objective", "child goal", "sisyphus", false, "autoContinue", true, "at", nowIso()), "") + "\n"
		f, err := os.OpenFile(goalLedgerPath(st), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
		if err != nil {
			os.Exit(2)
		}
		_, _ = f.WriteString(line)
		_ = f.Close()
	}
}

func faultChild(cwd, role string, extra ...string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^TestFaultChildProcess$", "-test.count=1")
	cmd.Env = append(os.Environ(), "GOAL_FAULT_ROLE="+role, "GOAL_FAULT_CWD="+cwd)
	cmd.Env = append(cmd.Env, extra...)
	cmd.Dir = cwd
	return cmd
}

func TestFaultInjection(t *testing.T) {
	const f = "fault-injection"

	tw(t, f, "fault: torn goal-file write is never observed as partial content", func(t *testing.T) {
		cwd := faultCwd(t)
		st := storage{cwd: cwd}
		goal, err := st.writeActiveGoalFile(createGoal("first version", true, false, false, time.Date(2026, 9, 7, 9, 0, 0, 0, time.UTC).UnixMilli()))
		if err != nil {
			t.Fatal(err)
		}
		filePath := filepath.Join(cwd, gstr(goal, "activePath"))
		before := readFileOrFail(t, filePath)

		// Simulate a crash between the temp write and the rename: a partial temp file is left next to the goal file, and the goal file
		// itself is truncated (a torn rename target). Readers must ignore the temp file and never observe a hybrid.
		dir := filepath.Dir(filePath)
		tmpName := fmt.Sprintf("%s.tmp-%d", filepath.Base(filePath), os.Getpid())
		if err := os.WriteFile(filepath.Join(dir, tmpName), []byte(before[:40]), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filePath, []byte(before[:120]), 0o644); err != nil {
			t.Fatal(err)
		}

		// parseGoalFile on the truncated file returns null (malformed), and the pool scan ignores the .tmp file entirely.
		if parsed := parseGoalFile(filePath); parsed != nil {
			t.Error("truncated goal file is detected as malformed")
		}
		faultInvalidateAll()
		if pool := st.readActiveGoalPool(); pool.size() != 0 {
			t.Errorf("torn goal is excluded from the pool; temp files are never parsed: %d", pool.size())
		}

		// A subsequent extension write atomically restores a valid file.
		restored, err := st.writeActiveGoalFile(createGoal("restored", true, false, false, time.Date(2026, 9, 7, 9, 1, 0, 0, time.UTC).UnixMilli()))
		if err != nil {
			t.Fatal(err)
		}
		reparsed := parseGoalFile(filepath.Join(cwd, gstr(restored, "activePath")))
		if reparsed == nil {
			t.Fatal("re-write restores a fully parseable goal file")
		}
		if gstr(reparsed, "objective") != "restored" {
			t.Errorf("objective %q", gstr(reparsed, "objective"))
		}
	})

	tw(t, f, "fault: torn ledger tail is counted malformed and never breaks reads or tail replay", func(t *testing.T) {
		cwd := faultCwd(t)
		st := storage{cwd: cwd}
		appendGoalEvent(st, ledgerEvent("goal_created", "goalId", "g1", "objective", "o", "sisyphus", false, "autoContinue", true, "at", nowIso()))
		faultInvalidateAll()
		// Build a checkpoint over the first event.
		loadLedgerState(st)

		// Crash-simulated torn append: a partial JSON line at the tail.
		fh, err := os.OpenFile(goalLedgerPath(st), os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fh.WriteString(`{"type": "goal_focused", "goalId": "g1", "reason": "crea`)
		_ = fh.Close()

		faultInvalidateAll()
		read := readGoalLedger(st)
		if read.malformed != 1 {
			t.Errorf("torn line counted as malformed: %d", read.malformed)
		}
		if len(read.events) != 1 {
			t.Errorf("valid events before the torn tail are intact: %d", len(read.events))
		}

		// Checkpoint tail replay tolerates the torn tail too.
		faultInvalidateAll()
		state := loadLedgerState(st)
		if state.malformed != 1 {
			t.Errorf("state malformed %d", state.malformed)
		}
		if state.state.goals.size() != 1 {
			t.Errorf("goals %d", state.state.goals.size())
		}
		if state.state.focusedGoalID != nil {
			t.Error("no focus event survived; the torn tail did not corrupt state")
		}
	})

	tw(t, f, "fault: stale lock (dead pid) is reclaimed promptly; live-holder lock fails fast", func(t *testing.T) {
		cwd := faultCwd(t)
		st := storage{cwd: cwd}
		// Stale lock: dead pid + old mtime.
		lockDir := filepath.Join(cwd, ".pi", "goals", ".locks")
		if err := os.MkdirAll(lockDir, 0o755); err != nil {
			t.Fatal(err)
		}
		lockPath := filepath.Join(lockDir, "g1.lock")
		stale := fmt.Sprintf(`{"pid":999999999,"startedAt":"%s"}`, time.Now().Add(-120*time.Second).UTC().Format("2006-01-02T15:04:05.000Z"))
		if err := os.WriteFile(lockPath, []byte(stale), 0o644); err != nil {
			t.Fatal(err)
		}
		past := time.Now().Add(-120 * time.Second)
		if err := os.Chtimes(lockPath, past, past); err != nil {
			t.Fatal(err)
		}
		t0 := time.Now()
		lock, err := acquireGoalLock(st, "g1", 0, 0)
		if elapsed := time.Since(t0); err != nil || lock == nil || elapsed > 5*time.Second {
			t.Fatalf("stale lock reclaimed promptly: %v (%v)", err, elapsed)
		}
		lock.release()

		// Live holder: a child process acquires the lock and holds it; the parent's acquisition must fail fast (bounded), not block.
		holder := faultChild(cwd, "hold-lock")
		out, err := holder.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := holder.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = holder.Process.Kill(); _ = holder.Wait() }()
		held := make(chan bool, 1)
		go func() {
			sc := bufio.NewScanner(out)
			for sc.Scan() {
				if strings.Contains(sc.Text(), "HELD") {
					held <- true
					return
				}
			}
			held <- false
		}()
		select {
		case ok := <-held:
			if !ok {
				t.Fatal("holder never acquired the lock")
			}
		case <-time.After(10 * time.Second):
			t.Fatal("holder never acquired the lock")
		}
		t1 := time.Now()
		res, err := acquireGoalLock(st, "g2", 0, 0)
		contended := time.Since(t1)
		if res != nil && err == nil {
			t.Error("contended acquisition fails fast (either nil or typed error)")
		}
		if contended > 2*time.Second {
			t.Errorf("contended acquisition bounded (%v)", contended)
		}
	})

	tw(t, f, "fault: multi-process goal-file writes never tear (one complete writer wins)", func(t *testing.T) {
		cwd := faultCwd(t)
		// Two children write the same goal id concurrently via the extension's own atomic writer; the final file must be one of the two
		// complete versions — never a hybrid.
		for _, obj := range []string{"AAAA", "BBBB"} {
			if out, err := faultChild(cwd, "write", "GOAL_FAULT_OBJECTIVE="+obj).CombinedOutput(); err != nil {
				t.Fatalf("writer %s: %v\n%s", obj, err, out)
			}
		}
		faultInvalidateAll()
		pool := storage{cwd: cwd}.readActiveGoalPool()
		if pool.size() < 1 {
			t.Fatal("at least one goal survived the concurrent writes")
		}
		for _, g := range pool.values() {
			parsed := parseGoalFile(filepath.Join(cwd, gstr(g, "activePath")))
			if parsed == nil {
				t.Fatal("every surviving goal file parses cleanly")
			}
			if o := gstr(parsed, "objective"); o != "AAAA" && o != "BBBB" {
				t.Errorf("objective is one complete writer's version, never a hybrid: %q", o)
			}
		}
	})

	tw(t, f, "fault: child-process append is picked up by the refresh diff (cross-process invalidation)", func(t *testing.T) {
		cwd := faultCwd(t)
		st := storage{cwd: cwd}
		if _, err := st.writeActiveGoalFile(createGoal("base", true, false, false, time.Date(2026, 9, 7, 11, 0, 0, 0, time.UTC).UnixMilli())); err != nil {
			t.Fatal(err)
		}
		faultInvalidateAll()
		st.readActiveGoalPool() // warm the pool cache
		readGoalLedger(st)      // warm the ledger cache

		// A child (separate process) writes a goal file + ledger line directly.
		if out, err := faultChild(cwd, "append").CombinedOutput(); err != nil {
			t.Fatalf("child: %v\n%s", err, out)
		}

		// Parent's warm caches still show the old state (mid-session staleness).
		ledgerBefore := len(readGoalLedger(st).events)
		poolBefore := st.readActiveGoalPool().size()
		if poolBefore != 1 {
			t.Errorf("warm cache before the external write: %d", poolBefore)
		}
		if ledgerBefore != 0 {
			t.Errorf("warm ledger before the external write: %d", ledgerBefore)
		}

		// The refresh diff (before=warm caches, after=cold re-read) reports both.
		faultInvalidateAll()
		afterPool := st.readActiveGoalPool()
		afterLedger := readGoalLedger(st)
		changes := diffGoalRefreshState(
			goalRefreshState{poolIds: []string{"base"}, ledgerEvents: ledgerBefore, ledgerMalformed: 0, settings: "s"},
			goalRefreshState{poolIds: poolIDs(afterPool), ledgerEvents: len(afterLedger.events), ledgerMalformed: afterLedger.malformed, settings: "s"})
		joined := strings.Join(changes, "\n")
		if !strings.Contains(joined, "added") {
			t.Errorf("pool addition reported: %v", changes)
		}
		if !strings.Contains(joined, "ledger: 0 -> 1 events") {
			t.Errorf("ledger growth reported: %v", changes)
		}
	})
}

func readFileOrFail(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

