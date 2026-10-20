package warden

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// Twins of tests/subagent.test.ts.

func subOptions(j Judge, c SubagentConfig) TriageOptions {
	return TriageOptions{Config: c, Judge: j, Timeout: time.Second}
}

func wakeJudge(p float64) *recordingJudge {
	return &recordingJudge{handler: func(Request) (Evaluation, error) {
		return Evaluation{Model: "jev-test", ElapsedMs: 3, Answers: map[string]Answer{"wake": noul(p)}}, nil
	}}
}

func subReport(text string, incremental bool, customType string) SubagentReport {
	return SubagentReport{ID: "e1", CustomType: customType, Incremental: incremental, Text: text}
}

func subEntry(id, customType, content string) SubagentEntry {
	return SubagentEntry{ID: id, Type: "custom_message", CustomType: customType, Content: content}
}

const (
	subFailure    = "Background tasks completed (1): **explorer**\n\n1. explorer\nResult: could not finish; the migration step failed with exit code 1\nParallel handoff: /tmp/handoff.md"
	subProgress   = "Background task progress: **explorer** is still reading src/config.ts (12 files inspected so far)"
	subCompletion = "Background tasks completed (1): **explorer**\n\n1. explorer\nResult: rewrote the parser and ran the test suite; all 12 tests pass"
)

func reportIDs(rs []SubagentReport) []string {
	out := []string{}
	for _, r := range rs {
		out = append(out, r.ID)
	}
	return out
}

// twin: tests/subagent.test.ts:26
func TestNewReportsReadsOnlyKnownSubagentNotifyEntriesOnceEachNewestLast(t *testing.T) {
	entries := []SubagentEntry{
		{ID: "m1", Type: "message", Content: "ordinary turn"},
		subEntry("e1", "subagent-incremental-child-notify", subProgress),
		subEntry("e2", "subagent-notify", subCompletion),
		subEntry("e3", "someone-elses-custom-type", "not a report"),
		{ID: "e4", Type: "custom_message", CustomType: "subagent-notify", Content: "   "},
		subEntry("e5", "subagent_supervisor_request", "child asks: which branch should I merge into?"),
	}
	found := NewReports(entries, map[string]bool{}, 8)
	mustEqual(t, reportIDs(found), []string{"e1", "e2", "e5"}, "ids")
	if !found[0].Incremental || found[1].Incremental {
		t.Error("an incremental notify is marked as progress")
	}
	mustEqual(t, reportIDs(NewReports(entries, map[string]bool{"e1": true, "e2": true}, 8)), []string{"e5"}, "seen entries are skipped")
	mustEqual(t, reportIDs(NewReports(entries, map[string]bool{}, 2)), []string{"e2", "e5"}, "bounded to the newest reports")
	// An entry without an id is keyed by its type and position.
	mustEqual(t, reportIDs(NewReports([]SubagentEntry{{Type: "custom_message", CustomType: "subagent-notify", Content: "x"}}, map[string]bool{}, 8)), []string{"subagent-notify#0"}, "synthetic id")
}

// twin: tests/subagent.test.ts:43
func TestMentionsTroubleReadsTroubleInCode(t *testing.T) {
	for _, s := range []string{subFailure, "child blocked: needs a decision on the schema", "the child was stopped by the watchdog", "timed out after 900 s", "1 task failed", "asks you to confirm the release"} {
		if !MentionsTrouble(s) {
			t.Errorf("trouble: %s", s)
		}
	}
	for _, s := range []string{subCompletion, subProgress, "Background tasks completed (2): **a**, **b**\nResult: both wrote their files; nothing to do", "still working: 3 of 9 steps done"} {
		if MentionsTrouble(s) {
			t.Errorf("no trouble: %s", s)
		}
	}
}

// twin: tests/subagent.test.ts:48
func TestTriageReportAnswersTheCheapCasesOfflineAsksJevOnlyAboutTroubleAndStaysQuietWhenItCannotAsk(t *testing.T) {
	ctx := context.Background()
	cfg := DefaultConfig().Subagent
	inc := TriageReport(ctx, subReport(subProgress, true, "subagent-incremental-child-notify"), subOptions(wakeJudge(0.99), cfg))
	if inc.Wake || inc.Source != "offline" || !strings.Contains(inc.Reason, "incremental") {
		t.Errorf("incremental %+v", inc)
	}
	clean := TriageReport(ctx, subReport(subCompletion, false, "subagent-notify"), subOptions(wakeJudge(0.99), cfg))
	if clean.Wake || clean.Source != "offline" || !strings.Contains(clean.Reason, "no failure") {
		t.Errorf("clean %+v", clean)
	}
	j := wakeJudge(0.93)
	asked := TriageReport(ctx, subReport(subFailure, false, "subagent-notify"), subOptions(j, cfg))
	if len(j.calls()) != 1 || !asked.Wake || asked.Source != "jev" || asked.Probability != 0.93 {
		t.Errorf("asked %+v calls %d", asked, len(j.calls()))
	}
	r := subReport(subFailure, false, "subagent-notify")
	if TriageReport(ctx, r, subOptions(wakeJudge(0.4), cfg)).Wake {
		t.Error("below the threshold it stays silent")
	}
	off := cfg
	off.Wake = false
	if TriageReport(ctx, r, subOptions(wakeJudge(0.93), off)).Wake {
		t.Error("wake off keeps the offline layer only")
	}
	high := cfg
	high.Threshold = 0.95
	if TriageReport(ctx, r, subOptions(wakeJudge(0.93), high)).Wake {
		t.Error("a raised threshold stays conservative")
	}
	failed := &recordingJudge{handler: func(Request) (Evaluation, error) { return Evaluation{}, errors.New("upstream unavailable") }}
	e := TriageReport(ctx, r, subOptions(failed, cfg))
	if e.Wake || e.Source != "error" {
		t.Errorf("error %+v", e)
	}
	n := TriageReport(ctx, r, subOptions(nil, cfg))
	if n.Wake || n.Source != "offline" {
		t.Errorf("no judge %+v", n)
	}
}

// twin: tests/subagent.test.ts:77
func TestAReportReachesJevBoundedAndRedactedAndTheWakeNamesTheReportWithoutRepeatingIt(t *testing.T) {
	long := strings.Repeat("padding line\n", 400) + "TOKEN=sk-synthetic-0123456789abcdef\nResult: the child failed"
	digest := ReportDigest(long)
	if utf16Len(digest) >= utf16Len(long) {
		t.Error("digest not shorter")
	}
	mustMatch(t, digest, `\[unsampled middle\]`, "middle marker")
	mustMatch(t, digest, `Result: the child failed$`, "tail keeps the status line")
	mustNotContain(t, digest, "sk-synthetic", "redacted")
	label := ReportLabel(subReport("explorer failed: "+strings.Repeat("header words ", 30)+"\nsecond line of the report", false, "subagent-notify"))
	if utf16Len(label) != 161 {
		t.Errorf("label %d", utf16Len(label))
	}
	wake := FormatWake([]string{label, "explorer failed: exit code 1"})
	mustMatch(t, wake, `^pi-warden: 2 subagent reports need you:`, "header")
	mustMatch(t, wake, `explorer failed: header words`, "pointer")
	mustNotContain(t, wake, "second line of the report", "never the report")
	mustMatch(t, wake, `The full reports are already in your context; this is only a pointer\.$`, "footer")
	mustMatch(t, FormatWake([]string{"one line"}), `^pi-warden: one subagent report needs you:`, "singular")
}

// twin: tests/subagent.test.ts:94
func TestWakePolicySendsOneBatchedWakePerWindowAndHoldsTheRestUntilTheNextFlush(t *testing.T) {
	p := NewWakePolicy(60_000)
	mustEqual(t, p.Offer("first", 1_000), []string{"first"}, "first")
	if p.Offer("second", 1_001) != nil || p.Waiting() != 1 {
		t.Error("inside the window nothing is sent")
	}
	if p.Offer("third", 60_999) != nil {
		t.Error("third held")
	}
	mustEqual(t, p.Offer("fourth", 61_000), []string{"second", "third", "fourth"}, "the next window carries the whole batch")
	if p.Waiting() != 0 || p.Flush(61_001) != nil {
		t.Error("an empty batch sends nothing")
	}
	p.Reset()
	if p.Waiting() != 0 {
		t.Error("reset")
	}
	mustEqual(t, p.Offer("after reset", 1), []string{"after reset"}, "a new session wakes at once")
}
