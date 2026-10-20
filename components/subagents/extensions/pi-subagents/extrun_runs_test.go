package pi_subagents

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"testing"
)

// Twins of test/unit/external-runs.test.ts. The original's registry is a global behind Symbol.for(...); the Go registry is a
// package value, and a malformed cached record is a map (or a value that cannot be read) placed in it directly.

func exRunsFresh(t *testing.T) {
	t.Helper()
	clearExternalRunRegistry()
	t.Cleanup(clearExternalRunRegistry)
}

func exRun1(id, session string) ExternalRun {
	return ExternalRun{ID: id, SessionID: session, Source: "tool", Label: "Run " + id, State: "running", StartedAt: 1}
}

func exRunFloat(v float64) *float64 { return &v }

// exStoredRuns is the registry's cache of cached records, as the original's `registry.runs` Map.
func exStoredRuns(t *testing.T) *externalRunCache {
	t.Helper()
	return currentExternalRunRegistry().Runs
}

func TestExternalRunsTwins(t *testing.T) {
	const f = "external-runs"

	tw(t, f, "external runs register, update, list, and unregister cached display records", func(t *testing.T) {
		exRunsFresh(t)
		registered, err := RegisterExternalRun(ExternalRun{ID: "review-1083", SessionID: "session-a", Source: "interactive-shell", Label: "Dependency review", State: "running", StartedAt: 100, CurrentAction: "Inspecting package metadata"})
		if err != nil {
			t.Fatal(err)
		}
		equal(t, registered, ExternalRun{ID: "review-1083", SessionID: "session-a", Source: "interactive-shell", Label: "Dependency review", State: "running", StartedAt: 100, CurrentAction: "Inspecting package metadata"}, "registered")
		other, err := SnapshotExternalRuns("session-b", ExternalRunSnapshotOptions{})
		if err != nil || len(other) != 0 {
			t.Fatalf("another session: %v %v", other, err)
		}
		updated, err := UpdateExternalRun("session-a", "review-1083", map[string]any{"state": "completed", "updatedAt": float64(190), "endedAt": float64(200), "preview": "Review complete", "reportPath": "/tmp/review.md"})
		if err != nil {
			t.Fatal(err)
		}
		equal(t, updated, ExternalRun{ID: "review-1083", SessionID: "session-a", Source: "interactive-shell", Label: "Dependency review", State: "completed", StartedAt: 100, UpdatedAt: exRunFloat(190), EndedAt: exRunFloat(200),
			CurrentAction: "Inspecting package metadata", Preview: "Review complete", ReportPath: "/tmp/review.md"}, "updated")
		listed, _ := ListExternalRuns("session-a")
		snap, _ := SnapshotExternalRuns("session-a", ExternalRunSnapshotOptions{})
		equal(t, listed, snap, "list equals snapshot")
		deleted, err := UnregisterExternalRun("session-a", "review-1083")
		equal(t, deleted, true, "first unregister")
		deleted, _ = UnregisterExternalRun("session-a", "review-1083")
		equal(t, deleted, false, "second unregister")
		after, _ := SnapshotExternalRuns("session-a", ExternalRunSnapshotOptions{})
		equal(t, len(after), 0, "after unregister")
	})

	tw(t, f, "external snapshots reuse validated records, detect direct mutations, and clone reads", func(t *testing.T) {
		exRunsFresh(t)
		registered, err := RegisterExternalRun(ExternalRun{ID: "trusted", SessionID: "session-a", Source: "tool", Label: "Trusted run", State: "running", StartedAt: 1})
		if err != nil {
			t.Fatal(err)
		}
		stored := exStoredRuns(t).get("session-a\x00trusted").(map[string]any)
		if stored["label"] != "Trusted run" {
			t.Fatalf("the cached record: %v", stored)
		}
		snapshot, _ := SnapshotExternalRuns("session-a", ExternalRunSnapshotOptions{})
		equal(t, snapshot, []ExternalRun{registered}, "snapshot")
		snapshot[0].Label = "Changed outside the registry"
		again, _ := SnapshotExternalRuns("session-a", ExternalRunSnapshotOptions{})
		equal(t, again[0].Label, "Trusted run", "snapshot results remain cloned")

		stored["label"] = "Directly changed"
		changed, _ := SnapshotExternalRuns("session-a", ExternalRunSnapshotOptions{})
		equal(t, changed[0].Label, "Directly changed", "a direct mutation is noticed")
		stored["state"] = "invalid"
		_, err = SnapshotExternalRuns("session-a", ExternalRunSnapshotOptions{})
		hasMatch(t, err, "state is invalid")
		ignored, err := SnapshotExternalRuns("session-a", ExternalRunSnapshotOptions{IgnoreMalformed: true})
		if err != nil || len(ignored) != 0 {
			t.Fatalf("ignoring malformed: %v %v", ignored, err)
		}
	})

	tw(t, f, "external snapshots do not trust forged public-symbol metadata", func(t *testing.T) {
		exRunsFresh(t)
		malformed := map[string]any{"id": "forged", "sessionId": "session-a", "source": "tool", "label": "Forged", "state": "invalid", "startedAt": float64(1)}
		forged := map[string]any{"id": "forged", "sessionId": "session-a", "source": "tool", "label": "Forged trusted value", "state": "running", "startedAt": float64(1)}
		reg := newExternalRunRegistry()
		reg.Runs.set("session-a\x00forged", malformed)
		// The original's trust cache is private (a WeakMap): what a caller can publish under a public symbol is never read. Here a
		// record is trusted only through the registry that validated it, so a forged normalization of the same record is not used.
		reg.trustedForTest("session-a\x00forged", malformed, forged)
		setExternalRunRegistry(reg)
		_, err := SnapshotExternalRuns("session-a", ExternalRunSnapshotOptions{})
		hasMatch(t, err, "state is invalid")
		ignored, err := SnapshotExternalRuns("session-a", ExternalRunSnapshotOptions{IgnoreMalformed: true})
		if err != nil || len(ignored) != 0 {
			t.Fatalf("ignoring malformed: %v %v", ignored, err)
		}
	})

	tw(t, f, "external snapshots filter unrelated cache keys before normalizing records", func(t *testing.T) {
		exRunsFresh(t)
		if _, err := RegisterExternalRun(ExternalRun{ID: "good", SessionID: "session-a", Source: "tool", Label: "Good run", State: "running", StartedAt: 1}); err != nil {
			t.Fatal(err)
		}
		exStoredRuns(t).set("session-b\x00bad", exThrowingRun{})
		got, err := SnapshotExternalRuns("session-a", ExternalRunSnapshotOptions{})
		if err != nil {
			t.Fatal(err)
		}
		ids := []string{}
		for _, r := range got {
			ids = append(ids, r.ID)
		}
		equal(t, ids, []string{"good"}, "ids")
		_, err = SnapshotExternalRuns("session-b", ExternalRunSnapshotOptions{})
		hasMatch(t, err, "unrelated record was normalized")
	})

	tw(t, f, "external run validation is atomic and display text is bounded", func(t *testing.T) {
		exRunsFresh(t)
		if _, err := RegisterExternalRun(ExternalRun{ID: "run", SessionID: "session-a", Source: "tool", Label: "Initial", State: "running", StartedAt: 1}); err != nil {
			t.Fatal(err)
		}
		_, err := UpdateExternalRun("session-a", "run", map[string]any{"state": "invalid", "label": "Changed"})
		hasMatch(t, err, "state is invalid")
		snap, _ := SnapshotExternalRuns("session-a", ExternalRunSnapshotOptions{})
		equal(t, snap[0].Label, "Initial", "label after the failed update")
		updated, err := UpdateExternalRun("session-a", "run", map[string]any{
			"label":   "\x1b[31m" + strings.Repeat("x", ExternalRunLimits.MaxTextLength+20),
			"preview": "line 1\n" + strings.Repeat("p", ExternalRunLimits.MaxPreviewLength+20),
		})
		if err != nil {
			t.Fatal(err)
		}
		equal(t, len(updated.Label), ExternalRunLimits.MaxTextLength, "label length")
		if len(updated.Preview) > ExternalRunLimits.MaxPreviewLength {
			t.Errorf("preview length %d", len(updated.Preview))
		}
		if strings.Contains(updated.Label, "\x1b") || strings.Contains(updated.Preview, "\n") {
			t.Errorf("control characters survived: %q %q", updated.Label, updated.Preview)
		}
		_, err = RegisterExternalRun(ExternalRun{ID: "unsafe\nrun", SessionID: "session-a", Source: "tool", Label: "Unsafe", State: "running", StartedAt: 1})
		hasMatch(t, err, "display-safe")
		_, err = RegisterExternalRun(map[string]any{"id": "extra", "sessionId": "session-a", "source": "tool", "label": "Extra", "state": "running", "startedAt": float64(1), "cwd": "/private"})
		hasMatch(t, err, "unknown fields: cwd")
		_, err = RegisterExternalRun(ExternalRun{ID: "invalid-date", SessionID: "session-a", Source: "tool", Label: "Invalid date", State: "running", StartedAt: 9007199254740991})
		hasMatch(t, err, "safe timestamp")
	})

	tw(t, f, "external snapshots throw on malformed cached records by default", func(t *testing.T) {
		exRunsFresh(t)
		reg := newExternalRunRegistry()
		reg.Runs.set("session-a\x00bad", map[string]any{"id": "bad", "sessionId": "session-a", "source": "tool", "label": "Bad", "state": "running", "startedAt": math.NaN()})
		reg.Runs.set("session-a\x00good", map[string]any{"id": "good", "sessionId": "session-a", "source": "tool", "label": "Good", "state": "running", "startedAt": float64(1)})
		setExternalRunRegistry(reg)
		_, err := SnapshotExternalRuns("session-a", ExternalRunSnapshotOptions{})
		if err == nil || !regexp.MustCompile(`^Malformed cached external run 'session-a\x00bad': External run startedAt must be a non-negative safe timestamp`).MatchString(err.Error()) {
			t.Fatalf("error: %v", err)
		}
	})

	tw(t, f, "external snapshots can remove malformed cached records for display-only callers", func(t *testing.T) {
		exRunsFresh(t)
		reg := newExternalRunRegistry()
		reg.Runs.set("session-a\x00bad", map[string]any{"id": "bad", "sessionId": "session-a", "source": "tool", "label": "Bad", "state": "running", "startedAt": math.NaN()})
		reg.Runs.set("session-a\x00good", map[string]any{"id": "good", "sessionId": "session-a", "source": "tool", "label": "Good", "state": "running", "startedAt": float64(1)})
		setExternalRunRegistry(reg)
		var diagnostics []string
		got, err := SnapshotExternalRuns("session-a", ExternalRunSnapshotOptions{IgnoreMalformed: true, OnMalformedRecord: func(m string) { diagnostics = append(diagnostics, m) }})
		if err != nil {
			t.Fatal(err)
		}
		equal(t, len(got), 1, "kept")
		equal(t, got[0].ID, "good", "kept id")
		equal(t, diagnostics, []string{"Malformed cached external run 'session-a\x00bad': External run startedAt must be a non-negative safe timestamp."}, "diagnostics")
		again, err := SnapshotExternalRuns("session-a", ExternalRunSnapshotOptions{})
		if err != nil || len(again) != 1 || again[0].ID != "good" {
			t.Fatalf("after the removal: %v %v", again, err)
		}
	})

	tw(t, f, "external snapshots apply count and serialized byte caps without provider callbacks", func(t *testing.T) {
		exRunsFresh(t)
		for index := 0; index < ExternalRunLimits.MaxSnapshotRuns+5; index++ {
			state := "completed"
			if index == 0 {
				state = "running"
			}
			if _, err := RegisterExternalRun(ExternalRun{ID: fmt.Sprintf("run-%d", index), SessionID: "session-a", Source: "tool", Label: fmt.Sprintf("Run %d", index), State: state,
				StartedAt: float64(index), UpdatedAt: exRunFloat(float64(index)), Preview: strings.Repeat("p", ExternalRunLimits.MaxPreviewLength)}); err != nil {
				t.Fatal(err)
			}
		}
		snapshot, err := SnapshotExternalRuns("session-a", ExternalRunSnapshotOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if len(snapshot) > ExternalRunLimits.MaxSnapshotRuns {
			t.Errorf("snapshot has %d runs", len(snapshot))
		}
		if n := externalRunSnapshotBytes(snapshot); n > ExternalRunLimits.MaxSerializedBytes {
			t.Errorf("snapshot is %d bytes", n)
		}
		equal(t, snapshot[0].ID, "run-0", "active jobs sort ahead of terminal jobs")
	})
}

// exThrowingRun is a cached value that cannot be read: the original's getter that throws.
type exThrowingRun struct{}

func (exThrowingRun) externalRunFields() map[string]any { panic("unrelated record was normalized") }
