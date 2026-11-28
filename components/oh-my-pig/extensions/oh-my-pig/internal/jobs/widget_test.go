package jobs

import (
	"testing"
	"time"
)

// TestFallbackWidgetViewMatchesOmp verifies omp's deterministic fallback job
// widget view (`job-widgets/runtime.ts:639-716`, `job-widgets/format.ts:24-42`,
// `job-widgets/spec.ts:157`).
func TestFallbackWidgetViewMatchesOmp(t *testing.T) {
	t0 := time.UnixMilli(1_700_000_000_000)

	// 1. Before any output: title truncated to 48 runes, blocks=[], no stage, updatedAt=startTime.
	w := NewFallbackWidgetTracker(t0)
	v0 := w.View("echo ignore1; echo ALERT_HIT_1; sleep 1.5; echo done", "", true, false, t0.Add(200*time.Millisecond))
	wantTitle := "echo ignore1; echo ALERT_HIT_1; sleep 1.5; echo…"
	if v0.Title != wantTitle {
		t.Fatalf("title = %q (len=%d), want %q (len=%d)", v0.Title, len([]rune(v0.Title)), wantTitle, len([]rune(wantTitle)))
	}
	if v0.Stage != "" {
		t.Fatalf("expected empty stage before output, got %q", v0.Stage)
	}
	if len(v0.Blocks) != 0 {
		t.Fatalf("expected empty blocks before output, got %+v", v0.Blocks)
	}
	if v0.State != "fallback" || v0.UpdatedAt != t0.UnixMilli() {
		t.Fatalf("unexpected state/updatedAt: %+v", v0)
	}

	// 2. Push multi-line output (including blank lines and partial trailing line).
	t1 := t0.Add(500 * time.Millisecond)
	w.PushChunk("ignore1\n\nALERT_HIT_1\n", t1)
	t2 := t0.Add(1500 * time.Millisecond)
	w.PushChunk("done", t2) // trailing partial line flushed on settle

	vSettled := w.View("echo ignore1; echo ALERT_HIT_1; sleep 1.5; echo done", "", true, true, t2)
	if vSettled.Stage != "done" {
		t.Fatalf("settled stage = %q, want %q", vSettled.Stage, "done")
	}
	if len(vSettled.Blocks) != 1 || vSettled.Blocks[0].Kind != "stat" || len(vSettled.Blocks[0].Items) != 2 {
		t.Fatalf("unexpected settled blocks: %+v", vSettled.Blocks)
	}
	if vSettled.Blocks[0].Items[0].Label != "lines" || vSettled.Blocks[0].Items[0].Text != "3" {
		t.Fatalf("lines stat = %+v, want {lines 3}", vSettled.Blocks[0].Items[0])
	}
	if vSettled.Blocks[0].Items[1].Label != "lines/s" || vSettled.Blocks[0].Items[1].Text != "2" {
		t.Fatalf("lines/s stat = %+v, want {lines/s 2} (3 lines / 1.5s)", vSettled.Blocks[0].Items[1])
	}
	if vSettled.UpdatedAt != t2.UnixMilli() {
		t.Fatalf("updatedAt = %d, want %d", vSettled.UpdatedAt, t2.UnixMilli())
	}

	// 3. Frozen view ignores subsequent chunks and later timestamps.
	w.PushChunk("late line\n", t2.Add(5*time.Second))
	vFrozen := w.View("other", "", true, true, t2.Add(10*time.Second))
	if vFrozen.Stage != "done" || vFrozen.Blocks[0].Items[0].Text != "3" {
		t.Fatalf("frozen view mutated: %+v", vFrozen)
	}

	// 4. Task job (lineStage=false) omits stage from raw output lines unless explicit stage given.
	wt := NewFallbackWidgetTracker(t0)
	wt.RecordToolCall("yield", "", `{"answer":42}`, t1)
	vt := wt.View("StructChild", "", false, true, t1)
	if vt.Stage != "" {
		t.Fatalf("task fallback widget should not set line-derived stage, got %q", vt.Stage)
	}
	if len(vt.Blocks) != 1 || vt.Blocks[0].Items[0].Text != "2" {
		t.Fatalf("task fallback widget blocks = %+v, want 2 lines", vt.Blocks)
	}

	// 5. FormatWidgetNumber matches omp `job-widgets/format.ts:24-42`.
	for _, tc := range []struct {
		v    float64
		want string
	}{
		{0, "0"},
		{3, "3"},
		{1.5, "1.50"},
		{12.34, "12.3"},
		{150.7, "151"},
		{12_340, "12.3k"},
		{123_400, "123k"},
		{4_500_000, "4.5M"},
	} {
		if got := FormatWidgetNumber(tc.v); got != tc.want {
			t.Errorf("FormatWidgetNumber(%v) = %q, want %q", tc.v, got, tc.want)
		}
	}
}
