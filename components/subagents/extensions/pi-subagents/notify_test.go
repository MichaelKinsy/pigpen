package pi_subagents

// Completion notifications must look like the original's (its runs/background/notify.ts: formatSingleCompletion,
// formatGroupedCompletion) and batch like its completion-batcher.ts.

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// noticeCall is one delivered message as the session's sender sees it (extension.go binds ctx.SendMessage).
type noticeCall struct {
	content string
	display bool
}

// captureCompletions binds a capturing sender and shortens the batch window so a test never waits a second.
func captureCompletions(t *testing.T) func() []noticeCall {
	t.Helper()
	completions.flush() // drain anything an earlier test left batched into its own sender
	var mu sync.Mutex
	var got []noticeCall
	setCompletionSender(func(content string, display bool) error {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, noticeCall{content, display})
		return nil
	})
	oldDebounce, oldWait := completionDebounce, completionMaxWait
	completionDebounce, completionMaxWait = time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() {
		setCompletionSender(nil)
		completionDebounce, completionMaxWait = oldDebounce, oldWait
		completions.flush()
	})
	return func() []noticeCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]noticeCall(nil), got...)
	}
}

// waitNotices waits until at least n notifications were delivered.
func waitNotices(t *testing.T, snap func() []noticeCall, n int) []noticeCall {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := snap(); len(got) >= n {
			return got
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("notices: got %d, want %d", len(snap()), n)
	return nil
}

func TestCompletionNoticeLooksLikeTheOriginal(t *testing.T) {
	eq(t, formatSingleCompletion(completionNotice{
		kind: "Background task", status: "completed", agent: "scout", preview: "all good",
	}), strings.Join([]string{
		"Background task completed: **scout**",
		"",
		"all good",
	}, "\n"), "a direct completion (notify.ts: formatSingleCompletion)")

	eq(t, formatSingleCompletion(completionNotice{
		kind: "Detached foreground task", status: "paused", agent: "worker", info: " (steer reply)",
		preview: "on it", session: "C:/s/child.jsonl",
	}), strings.Join([]string{
		"Detached foreground task paused: **worker** (steer reply)",
		"",
		"on it",
		"",
		"Session: C:/s/child.jsonl",
	}, "\n"), "with taskInfo and a session line")
}

func TestGroupedCompletionLooksLikeTheOriginal(t *testing.T) {
	eq(t, formatGroupedCompletion([]completionNotice{
		{kind: "Background task", status: "completed", agent: "scout", preview: "one"},
		{kind: "Background task", status: "completed", agent: "worker", preview: "two", session: "C:/s/w.jsonl"},
	}), strings.Join([]string{
		"Background tasks completed (2): **scout**, **worker**",
		"",
		"1. scout",
		"one",
		"",
		"2. worker",
		"two",
		"Session: C:/s/w.jsonl",
	}, "\n"), "its formatGroupedCompletion")
}

func TestCompletionPreviewIsBounded(t *testing.T) {
	eq(t, completionPreview("  \n"), "(no output)", "an empty one names itself")
	eq(t, completionPreview("a\n\n"), "a", "trailing blank lines go")
	long := strings.Repeat("x", completionPreviewBytes+100)
	got := completionPreview(long)
	eq(t, strings.HasSuffix(got, completionTruncation), true, "the truncation marker")
	eq(t, len(got), completionPreviewBytes, "the byte bound")
}

// The batcher holds sibling successes briefly for one grouped message; failures bypass it and flush what is held
// (its completion-batcher.ts: "Failure and attention signals bypass this batcher entirely").
func TestCompletionBatchingGroupsSuccessesAndBypassesFailures(t *testing.T) {
	oldDebounce, oldWait := completionDebounce, completionMaxWait
	t.Cleanup(func() { completionDebounce, completionMaxWait = oldDebounce, oldWait })
	completionDebounce, completionMaxWait = 5*time.Millisecond, 50*time.Millisecond
	var mu sync.Mutex
	var got []string
	c := &completionNotifier{send: func(content string, _ bool) error {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, content)
		return nil
	}}
	c.deliver(completionNotice{kind: "Background task", status: "completed", agent: "scout", preview: "one"})
	c.deliver(completionNotice{kind: "Background task", status: "completed", agent: "worker", preview: "two"})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	eq(t, got, []string{"Background tasks completed (2): **scout**, **worker**\n\n1. scout\none\n\n2. worker\ntwo"}, "siblings group")
	got = nil
	mu.Unlock()

	c.deliver(completionNotice{kind: "Background task", status: "completed", agent: "scout", preview: "held"})
	c.deliver(completionNotice{kind: "Background task", status: "failed", agent: "worker", preview: "boom"})
	mu.Lock()
	defer mu.Unlock()
	eq(t, got, []string{
		"Background task completed: **scout**\n\nheld",
		"Background task failed: **worker**\n\nboom",
	}, "the failure flushes the held success and goes at once")
}
