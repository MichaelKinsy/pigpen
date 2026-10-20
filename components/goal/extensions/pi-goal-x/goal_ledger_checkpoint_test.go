package pi_goal_x

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const ledgerCheckpointFile0 = "goal-ledger-checkpoint"

func ledgerCheckpointPath(ctx storage) string {
	return filepath.Join(ctx.cwd, ".pi", "goals", ledgerCheckpointFile)
}

// appendGoal appends goal_created and goal_focused (plus extra) events for one goal.
func appendGoal(ctx storage, goalID string, extra ...*jsObject) {
	events := []*jsObject{
		createdEv(goalID, "objective "+goalID, "2026-09-01T00:00:00.500Z"),
		ledgerEv("goal_focused", goalID, "2026-09-01T00:00:00.600Z", "reason", "created"),
	}
	for _, e := range append(events, extra...) {
		appendGoalEvent(ctx, e)
	}
}

func TestGoalLedgerCheckpoint(t *testing.T) {
	tw(t, ledgerCheckpointFile0, "checkpoint: loadLedgerState falls back to full parse and writes a fresh checkpoint", func(t *testing.T) {
		ctx := ledgerTempCtx(t)
		appendGoal(ctx, "g1")
		appendGoal(ctx, "g2")
		fi, _ := os.Stat(goalLedgerPath(ctx))
		ledgerBytes := fi.Size()
		if _, err := os.Stat(ledgerCheckpointPath(ctx)); err == nil {
			t.Error("no checkpoint before first load")
		}
		invalidateGoalLedgerCache() // append only extends an existing cache entry
		result := loadLedgerState(ctx)
		eq(t, result.source, "full", "source")
		eq(t, result.coveredEvents, 4, "coveredEvents")
		eq(t, result.state.goals.size(), 2, "goals")
		eq(t, derefStr(result.state.focusedGoalID), "g2", "focus")
		eq(t, len(result.recentEventsByGoal["g1"]), 2, "recent g1")
		// A fresh checkpoint is written so the next cold start is bounded.
		if _, err := os.Stat(ledgerCheckpointPath(ctx)); err != nil {
			t.Error("checkpoint written after full load")
		}
		// Second load from a cold cache serves the checkpoint.
		invalidateGoalLedgerCache()
		hit := loadLedgerState(ctx)
		eq(t, hit.source, "checkpoint", "source")
		eq(t, hit.coveredBytes, ledgerBytes, "coveredBytes")
		eq(t, derefStr(hit.state.focusedGoalID), "g2", "focus")
		eq(t, len(hit.recentEventsByGoal["g1"]), 2, "recent g1")
		eq(t, hit.malformed, 0, "malformed")
	})
	tw(t, ledgerCheckpointFile0, "checkpoint: warm cache serves zero-op and matches the checkpoint view", func(t *testing.T) {
		ctx := ledgerTempCtx(t)
		appendGoal(ctx, "g1")
		invalidateGoalLedgerCache()
		eq(t, loadLedgerState(ctx).source, "full", "cold load warms the cache")
		fromCache := loadLedgerState(ctx)
		eq(t, fromCache.source, "cache", "source")
		eq(t, derefStr(fromCache.state.focusedGoalID), "g1", "focus")
	})
	tw(t, ledgerCheckpointFile0, "checkpoint: tail replay applies only the ledger bytes past the checkpoint", func(t *testing.T) {
		ctx := ledgerTempCtx(t)
		appendGoal(ctx, "g1")
		// Bootstrap a checkpoint covering the current ledger.
		invalidateGoalLedgerCache()
		first := loadLedgerState(ctx)
		eq(t, first.source, "full", "source")
		coveredBefore := first.coveredBytes

		// Grow the ledger externally (no in-process append).
		external := createdEv("g2", "external", nowIso())
		must(0, appendFileString(goalLedgerPath(ctx), marshalJSON(external, "")+"\n"))

		invalidateGoalLedgerCache()
		result := loadLedgerState(ctx)
		eq(t, result.source, "tail", "source")
		eq(t, result.coveredBytes > coveredBefore, true, "coverage advanced past the old checkpoint")
		eq(t, result.coveredEvents, 3, "coveredEvents")
		eq(t, result.state.goals.size(), 2, "goals")
		eq(t, derefStr(result.state.focusedGoalID), "g1", "focus")
		eq(t, len(result.recentEventsByGoal["g2"]), 1, "recent g2")

		// The refreshed checkpoint now serves cold reads directly.
		invalidateGoalLedgerCache()
		hit := loadLedgerState(ctx)
		eq(t, hit.source, "checkpoint", "source")
		eq(t, derefStr(hit.state.focusedGoalID), "g1", "focus")
	})
	tw(t, ledgerCheckpointFile0, "checkpoint: version mismatch falls back to the full parse", func(t *testing.T) {
		ctx := ledgerTempCtx(t)
		appendGoal(ctx, "g1")
		invalidateGoalLedgerCache()
		loadLedgerState(ctx) // writes checkpoint
		// Corrupt the version to simulate a future format.
		data, err := os.ReadFile(ledgerCheckpointPath(ctx))
		if err != nil {
			t.Fatal(err)
		}
		raw := string(data)
		if !strings.Contains(raw, `"version":3`) {
			t.Fatal(raw)
		}
		must(0, os.WriteFile(ledgerCheckpointPath(ctx), []byte(strings.Replace(raw, `"version":3`, fmt.Sprintf(`"version":%d`, ledgerCheckpointVersion+1), 1)), 0o644))

		invalidateGoalLedgerCache()
		result := loadLedgerState(ctx)
		eq(t, result.source, "full", "source")
		eq(t, derefStr(result.state.focusedGoalID), "g1", "focus")
	})
	tw(t, ledgerCheckpointFile0, "checkpoint: malformed checkpoint body falls back to the full parse", func(t *testing.T) {
		ctx := ledgerTempCtx(t)
		appendGoal(ctx, "g1")
		must(0, os.WriteFile(ledgerCheckpointPath(ctx), []byte("{not json at all"), 0o644))
		invalidateGoalLedgerCache()
		result := loadLedgerState(ctx)
		eq(t, result.source, "full", "source")
		eq(t, result.state.goals.size(), 1, "goals")
	})
	tw(t, ledgerCheckpointFile0, "checkpoint: ledger truncated below checkpoint coverage falls back to the full parse", func(t *testing.T) {
		ctx := ledgerTempCtx(t)
		appendGoal(ctx, "g1")
		appendGoal(ctx, "g2")
		invalidateGoalLedgerCache()
		loadLedgerState(ctx) // checkpoint covers 4 events
		// Truncate the ledger (external rewrite).
		lines := ledgerLines(t, goalLedgerPath(ctx))
		must(0, os.WriteFile(goalLedgerPath(ctx), []byte(strings.Join(lines[:2], "\n")+"\n"), 0o644))

		invalidateGoalLedgerCache()
		result := loadLedgerState(ctx)
		eq(t, result.source, "full", "source")
		eq(t, result.state.goals.size(), 1, "goals")
		eq(t, result.coveredEvents, 2, "coveredEvents")
	})
	tw(t, ledgerCheckpointFile0, "checkpoint: external ledger growth beyond a cold checkpoint is replayed", func(t *testing.T) {
		ctx := ledgerTempCtx(t)
		appendGoal(ctx, "g1")
		invalidateGoalLedgerCache()
		loadLedgerState(ctx) // checkpoint covers g1's events only
		// External process appends g2 without going through this module's cache.
		must(0, appendFileString(goalLedgerPath(ctx), marshalJSON(createdEv("g2", "external", nowIso()), "")+"\n"))

		invalidateGoalLedgerCache()
		result := loadLedgerState(ctx)
		eq(t, result.source, "tail", "source")
		eq(t, result.state.goals.size(), 2, "goals")
		eq(t, derefStr(result.state.focusedGoalID), "g1", "focus")
		eq(t, len(result.recentEventsByGoal["g2"]), 1, "recent g2")
	})
	tw(t, ledgerCheckpointFile0, "checkpoint: mutation-path maintenance keeps the checkpoint current without a cold load", func(t *testing.T) {
		ctx := ledgerTempCtx(t)
		appendGoal(ctx, "g1")
		// Bootstrap via a full load, then append with the cache warm. The disk checkpoint write is throttled (32 appends / 2s),
		// so drive 16 goal appends (32 events) past the boundary.
		invalidateGoalLedgerCache()
		loadLedgerState(ctx)
		for i := 2; i <= 17; i++ {
			appendGoal(ctx, fmt.Sprintf("g%d", i))
		}
		data, err := os.ReadFile(ledgerCheckpointPath(ctx))
		if err != nil {
			t.Fatal(err)
		}
		cpRaw := jo(t, string(data))
		version, _ := cpRaw.num("version")
		covered, _ := cpRaw.num("coveredEvents")
		eq(t, version, float64(ledgerCheckpointVersion), "version")
		eq(t, covered >= 2+32, true, fmt.Sprintf("disk checkpoint advanced past the throttle boundary (%v)", covered))

		// A cold read (fresh cache) now hits the checkpoint with all events.
		invalidateGoalLedgerCache()
		result := loadLedgerState(ctx)
		eq(t, result.source, "checkpoint", "source")
		eq(t, result.state.goals.size(), 17, "goals")
		eq(t, derefStr(result.state.focusedGoalID), "g17", "focus")
	})
	tw(t, ledgerCheckpointFile0, "checkpoint: readGoalLedger (full-events API) is untouched and consistent", func(t *testing.T) {
		ctx := ledgerTempCtx(t)
		appendGoal(ctx, "g1")
		full := readGoalLedger(ctx)
		eq(t, len(full.events), 2, "events")
		eq(t, full.malformed, 0, "malformed")
		// The checkpoint file lives next to the ledger and does not change the JSONL event stream.
		eq(t, len(ledgerLines(t, goalLedgerPath(ctx))), 2, "lines")
	})
}
