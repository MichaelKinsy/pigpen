package pi_subagents

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func budgetFor(t *testing.T, limit int) runFanoutBudgetDescriptor {
	t.Helper()
	t.Setenv("PI_SUBAGENTS_TEMP_ROOT", t.TempDir())
	d, err := createRunFanoutBudget("test-"+newUUID(), limit)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestRunFanoutBudget(t *testing.T) {
	tskip(t, "run-fanout-budget", "degrades status projection when persisted budget state is removed", "projects through summarizeAsyncStatus of the original's background status module (async-status.ts), which the SubAsync lane ports; the fan-out snapshot fallback it tests lives there")
	tskip(t, "run-fanout-budget", "keeps nested status projection when persisted budget state is removed", "projects through listAsyncRuns and the nested-events route files of the original's background status modules (async-status.ts, nested-events.ts), which other lanes port")
	tw(t, "run-fanout-budget", "persists claims and rejects the responsible next path at the exact cap", func(t *testing.T) {
		d := budgetFor(t, 2)
		s, err := claimRunFanoutBatch(d, []string{"tasks[0]", "tasks[1]"})
		equal(t, [2]any{s, err}, [2]any{runFanoutBudgetSnapshot{2, 2, 0}, error(nil)}, "claim")
		s, _ = getRunFanoutBudgetSnapshot(d)
		equal(t, s, runFanoutBudgetSnapshot{2, 2, 0}, "snapshot")
		_, err = claimRunFanoutBatch(d, []string{"tasks[2]"})
		var le *runFanoutLimitError
		if !errors.As(err, &le) {
			t.Fatalf("got %v", err)
		}
		equal(t, le.Rejection.Path, "tasks[2]", "path")
		equal(t, le.Rejection.Used, 2, "used")
		equal(t, le.Rejection.Remaining, 0, "remaining")
		equal(t, le.Error(), "Run fan-out limit reached at tasks[2] (2/2 used; 1 requested, 0 remaining). No children from this admission group were started. Start a new top-level run or raise config.maxSubagentSpawnsPerRun.", "message")
	})
	tw(t, "run-fanout-budget", "rolls back only the failed admission batch", func(t *testing.T) {
		d := budgetFor(t, 2)
		claimRunFanoutBatch(d, []string{"single"})
		_, err := claimRunFanoutBatch(d, []string{"chain[0]", "chain[1]"})
		var le *runFanoutLimitError
		if !errors.As(err, &le) {
			t.Fatalf("got %v", err)
		}
		s, _ := getRunFanoutBudgetSnapshot(d)
		equal(t, s, runFanoutBudgetSnapshot{1, 2, 1}, "snapshot")
	})
	tw(t, "run-fanout-budget", "rolls back a batch when its commit fails", func(t *testing.T) {
		d := budgetFor(t, 2)
		claimRunFanoutBatch(d, []string{"existing"})
		err := claimRunFanoutBatchWithCommit(d, []string{"append"}, func() error { return errors.New("enqueue failed") })
		polMatches(t, err.Error(), `enqueue failed`)
		s, _ := getRunFanoutBudgetSnapshot(d)
		equal(t, s, runFanoutBudgetSnapshot{1, 2, 1}, "snapshot")
	})
	tw(t, "run-fanout-budget", "reports claims-directory I/O errors instead of ordinary exhaustion", func(t *testing.T) {
		d := budgetFor(t, 2)
		claims := filepath.Join(d.Directory, "claims")
		os.RemoveAll(claims)
		writeFile(t, claims, "not a directory")
		_, err := getRunFanoutBudgetSnapshot(d)
		if err == nil {
			t.Fatal("no error")
		}
		polMatches(t, err.Error(), `claims directory is unreadable.*ENOTDIR`)
	})
	tw(t, "run-fanout-budget", "admits exactly one simultaneous two-slot batch at limit two", func(t *testing.T) {
		for i := 0; i < 12; i++ {
			d := budgetFor(t, 2)
			results := make([]string, 2)
			var wg sync.WaitGroup
			for n, name := range []string{"group-a", "group-b"} {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if _, err := claimRunFanoutBatch(d, []string{name + "[0]", name + "[1]"}); err == nil {
						results[n] = "admitted"
					} else {
						results[n] = "rejected"
					}
				}()
			}
			wg.Wait()
			if !(results[0] == "admitted" && results[1] == "rejected" || results[0] == "rejected" && results[1] == "admitted") {
				t.Fatalf("iteration %d: %v", i, results)
			}
			s, _ := getRunFanoutBudgetSnapshot(d)
			equal(t, s, runFanoutBudgetSnapshot{2, 2, 0}, "snapshot")
		}
	})
	tw(t, "run-fanout-budget", "fails closed when descriptor identity or managed-root containment is invalid", func(t *testing.T) {
		d := budgetFor(t, 2)
		bad := d.object()
		bad.set("limit", 3.0)
		_, err := validateRunFanoutBudgetDescriptor(bad)
		polMatches(t, err.Error(), `does not match`)
		outside := t.TempDir()
		os.Mkdir(filepath.Join(outside, "claims"), 0o755)
		m := newObject()
		m.set("version", 1.0)
		m.set("rootRunId", d.RootRunID)
		m.set("limit", 2.0)
		m.set("createdAt", 1.0)
		writeFile(t, filepath.Join(outside, "manifest.json"), marshalJSON(m, ""))
		linked := filepath.Join(filepath.Dir(d.Directory), "linked-x")
		if err := os.Symlink(outside, linked); err != nil {
			t.Skip("cannot create a symlink here: " + err.Error())
		}
		escape := d.object()
		escape.set("directory", linked)
		_, err = validateRunFanoutBudgetDescriptor(escape)
		if err == nil {
			t.Fatal("outside directory accepted")
		}
		polMatches(t, err.Error(), `resolves outside`)
	})
}
