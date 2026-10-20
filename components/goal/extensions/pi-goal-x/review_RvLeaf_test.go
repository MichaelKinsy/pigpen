package pi_goal_x

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Review twins: behaviors of the original (storage/goal-files.ts, goal-policy.ts, goal-core.ts, goal-recovery.ts, goal-format.ts,
// scripts/recover-session-checkpoints.mjs) that the earlier port diverged from.

func TestReviewRvLeaf(t *testing.T) {
	t.Run("timestampForFile reads any ISO form like new Date(iso)", func(t *testing.T) {
		local := time.Date(2026, 3, 4, 5, 6, 7, 0, time.Local)
		iso := local.UTC().Format("2006-01-02T15:04:05Z")
		eq(t, timestampForFile(iso), "2026030405060700", "no milliseconds")
		eq(t, timestampForFile(local.UTC().Format("2006-01-02T15:04:05.250Z")), "2026030405060725", "hundredths")
	})
	t.Run("task policy messages keep the id verbatim", func(t *testing.T) {
		g := jo(t, `{"id":"g","taskList":{"tasks":[{"id":"a","title":"A","status":"complete"}]}}`)
		eq(t, validateTaskCompletion(g, `x"y`).message, `Task "x"y" not found.`, "not found")
		eq(t, validateTaskCompletion(g, "a").message, `Task "a" is already complete.`, "complete")
	})
	t.Run("localeCompare follows ICU for ASCII", func(t *testing.T) {
		eq(t, localeCompare("a", "A") < 0, true, "lowercase first")
		eq(t, localeCompare("a_b", "a1b") < 0, true, "underscore before digit")
		eq(t, localeCompare("a-b", "a_b") > 0, true, "underscore before hyphen")
		eq(t, localeCompare("b", "A") > 0, true, "case-insensitive primary")
	})
	t.Run("expanded goal event keeps an empty currentGoalId", func(t *testing.T) {
		d := normalizeGoalEventDetails(jo(t, `{"goalId":"g","currentGoalId":"","currentStatus":"active"}`))
		eq(t, d.hasCurrentGoalId && !d.currentGoalIdNull && d.currentGoalId == "", true, "details")
	})
	t.Run("external ledger append refuses a symlinked ledger", func(t *testing.T) {
		root := t.TempDir()
		pool := filepath.Join(root, "pool")
		if err := os.MkdirAll(pool, 0o755); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(root, "elsewhere.jsonl")
		if err := os.WriteFile(target, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(pool, "goal_events.jsonl")); err != nil {
			t.Skip("symlinks unavailable")
		}
		s := storage{cwd: root, root0: pool}
		res := appendGoalEvent(s, ledgerEvent("goal_unfocused", "reason", "x", "at", "2026-01-01T00:00:00.000Z"))
		eq(t, res.ok, false, "refused")
		eq(t, res.err != nil && strings.Contains(res.err.Error(), "Goal ledger is a symlink"), true, "message")
	})
	t.Run("session recover reports a missing file like Node", func(t *testing.T) {
		var out, errb strings.Builder
		code := recoverSessionCheckpointsMain([]string{"--session", "/nonexistent/s.jsonl"}, "/", &out, &errb)
		eq(t, code, 1, "exit")
		eq(t, strings.Contains(errb.String(), "ENOENT: no such file or directory, lstat '/nonexistent/s.jsonl'"), true, "message")
	})
}
