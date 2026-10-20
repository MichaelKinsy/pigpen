package pi_goal_x

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const sessionHealthFile = "goal-session-health"

var healthEntryN int

// healthEntry is the upstream entry(): a pi-goal-event custom message with the given overrides.
func healthEntry(overrides ...any) *jsObject {
	healthEntryN++
	o := newObject()
	o.set("type", "custom_message")
	o.set("id", "e"+jsNumber(float64(healthEntryN)))
	o.set("parentId", nil)
	o.set("timestamp", "2026-08-23T00:00:00.000Z")
	o.set("customType", "pi-goal-event")
	o.set("content", "")
	o.set("display", false)
	o.set("details", newObject())
	for i := 0; i+1 < len(overrides); i += 2 {
		o.set(overrides[i].(string), overrides[i+1])
	}
	return o
}

func detailsOf(kv ...any) *jsObject {
	o := newObject()
	for i := 0; i+1 < len(kv); i += 2 {
		o.set(kv[i].(string), kv[i+1])
	}
	return o
}

func TestGoalSessionHealth(t *testing.T) {
	tw(t, sessionHealthFile, "counts only pi-goal-event custom messages", func(t *testing.T) {
		stubSafe(t, func() {
			health := inspectCheckpointHealth([]any{
				healthEntry("content", checkpointTriggerPrompt("g1"), "details", detailsOf("version", 2.0, "kind", "checkpoint", "goalId", "g1")),
				healthEntry("customType", "pi-goal-audit-event", "content", "audit stuff"),
				healthEntry("customType", "other", "content", strings.Repeat("x", 5000)),
				jo(t, `{"type":"message","message":{"role":"user"}}`),
				nil,
				"junk",
			})
			eq(t, health.total, 1, "total")
			eq(t, health.v2Minimal, 1, "v2Minimal")
			eq(t, health.legacyFull, 0, "legacyFull")
		})
	})
	tw(t, sessionHealthFile, "classifies legacy full checkpoints as recoverable", func(t *testing.T) {
		legacy := "[GOAL CHECKPOINT goalId=g1]\n" + strings.Repeat("x", 6000)
		health := inspectCheckpointHealth([]any{healthEntry("content", legacy, "details", detailsOf("kind", "checkpoint", "goalId", "g1", "objective", "big"))})
		eq(t, health.total, 1, "total")
		eq(t, health.legacyFull, 1, "legacyFull")
		eq(t, health.recoverableChars, utf16Len(legacy), "recoverableChars")
		eq(t, health.largestCheckpointChars, utf16Len(legacy), "largest")
	})
	tw(t, sessionHealthFile, "requires both marker shape and version 2 for v2 classification", func(t *testing.T) {
		stubSafe(t, func() {
			// Right shape but missing details.version -> treated as legacy.
			health := inspectCheckpointHealth([]any{
				healthEntry("content", checkpointTriggerPrompt("g1"), "details", detailsOf("kind", "checkpoint", "goalId", "g1")),
			})
			eq(t, health.v2Minimal, 0, "v2Minimal")
			eq(t, health.legacyFull, 1, "legacyFull")
		})
	})
	tw(t, sessionHealthFile, "returns zeros for an entry list without checkpoints", func(t *testing.T) {
		health := inspectCheckpointHealth([]any{jo(t, `{"type":"message"}`)})
		eq(t, health.total, 0, "total")
		eq(t, health.recoverableChars, 0, "recoverableChars")
	})
	tw(t, sessionHealthFile, "reads a session JSONL including header and malformed lines", func(t *testing.T) {
		stubSafe(t, func() {
			dir := t.TempDir()
			file := filepath.Join(dir, "session.jsonl")
			header := marshalJSON(detailsOf("type", "session", "id", "s1", "cwd", dir), "")
			v2 := marshalJSON(healthEntry("content", checkpointTriggerPrompt("g1"), "details", detailsOf("version", 2.0, "kind", "checkpoint", "goalId", "g1")), "")
			legacy := marshalJSON(healthEntry("content", "[GOAL CHECKPOINT goalId=g2]\n"+strings.Repeat("y", 300), "details", detailsOf("kind", "checkpoint", "goalId", "g2", "objective", "old")), "")
			must(0, os.WriteFile(file, []byte(strings.Join([]string{header, "{broken json", v2, legacy}, "\n")+"\n"), 0o644))
			health := readSessionCheckpointHealth(file)
			if health == nil {
				t.Fatal("nil health")
			}
			eq(t, health.total, 2, "total")
			eq(t, health.v2Minimal, 1, "v2Minimal")
			eq(t, health.legacyFull, 1, "legacyFull")
		})
	})
	tw(t, sessionHealthFile, "returns null for a missing file", func(t *testing.T) {
		eq(t, readSessionCheckpointHealth("/nonexistent/session.jsonl") == nil, true, "nil")
	})
	tw(t, sessionHealthFile, "projects post-recovery size to the repaired-marker floor", func(t *testing.T) {
		legacy := strings.Repeat("z", 6400)
		health := inspectCheckpointHealth([]any{healthEntry("content", legacy, "details", newObject()), healthEntry("content", legacy, "details", newObject())})
		projected := projectedContentCharsAfterRecovery(health)
		eq(t, float64(projected) < float64(health.recoverableChars)*0.05, true, "projected must be under 5% of recoverable")
	})
	tw(t, sessionHealthFile, "report includes totals and the recovery hint only when repair is possible", func(t *testing.T) {
		health := inspectCheckpointHealth([]any{healthEntry("content", strings.Repeat("x", 100), "details", newObject())})
		session := "/tmp/session.jsonl"
		report := formatCheckpointHealthReport(health, &session)
		for _, re := range []string{`Session checkpoints:`, `legacy full checkpoints: 1`, `pi-goal-x-recover --session <path> --dry-run`} {
			eq(t, regexp.MustCompile(re).MatchString(report), true, re)
		}
		clean := inspectCheckpointHealth(nil)
		eq(t, strings.Contains(formatCheckpointHealthReport(clean, nil), "pi-goal-x-recover"), false, "no hint")
	})
}
