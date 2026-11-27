package pi_subagents

// Completion notifications (the original's runs/background/notify.ts and completion-batcher.ts): an async completion or
// a steer reply is delivered to the session as a `subagent-notify` message with triggerTurn — the "native completion
// notification" its tool description promises ("Pi will wake you through the native completion notification when this
// subagent completes or needs attention"). Successful completions batch briefly so siblings that finish together arrive
// as one grouped message; failures, pauses and stops bypass the batcher and deliver at once. The message is the
// original's formatSingleCompletion/formatGroupedCompletion.

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// the original's completion-batcher.ts defaults (its straggler windows are not ported)
var (
	completionDebounce = 150 * time.Millisecond
	completionMaxWait  = 1 * time.Second
)

// the result preview bound (its CHILD_OUTPUT_PREVIEW_MAX_BYTES and PREVIEW_TRUNCATION_MARKER)
const (
	completionPreviewBytes = 4 * 1024
	completionTruncation   = "...[preview truncated]"
)

// completionNotice is one settled turn as its notification shows it (the original's SubagentNotifyDetails for a direct
// run; the port has no workflow receipts, child outputs or watchdog blockers).
type completionNotice struct {
	kind    string // "Background task" (a detached launch) or "Detached foreground task" (a steer turn)
	status  string // "completed", "failed", "paused", "stopped"
	agent   string
	info    string // the header's parenthetical (task indexes, children running; the port: steer replies)
	preview string // the result preview
	session string // the child's session file, when it has one
	display bool   // shown in the transcript (its sendCompletion display rule)
}

// formatSingleCompletion is the original's formatSingleCompletion for one direct run.
func formatSingleCompletion(n completionNotice) string {
	lines := []string{
		fmt.Sprintf("%s %s: **%s**%s", n.kind, n.status, n.agent, n.info),
		"",
		n.preview,
	}
	if n.session != "" {
		lines = append(lines, "", "Session: "+n.session)
	}
	return strings.Join(lines, "\n")
}

// formatGroupedCompletion is its formatGroupedCompletion: sibling completions delivered as one wake.
func formatGroupedCompletion(items []completionNotice) string {
	names := make([]string, len(items))
	for i, n := range items {
		names[i] = fmt.Sprintf("**%s**%s", n.agent, n.info)
	}
	blocks := []string{
		fmt.Sprintf("Background tasks completed (%d): %s", len(items), strings.Join(names, ", ")),
		"",
	}
	for i, n := range items {
		blocks = append(blocks, fmt.Sprintf("%d. %s%s", i+1, n.agent, n.info), n.preview)
		if n.session != "" {
			blocks = append(blocks, "Session: "+n.session)
		}
		blocks = append(blocks, "")
	}
	return strings.TrimRight(strings.Join(blocks, "\n"), "\n")
}

// completionPreview bounds a result preview (its truncateUtf8Head), naming an empty one as it does.
func completionPreview(text string) string {
	text = strings.TrimRight(sanitizeDisplay(text), "\n")
	if strings.TrimSpace(text) == "" {
		return "(no output)"
	}
	if len(text) > completionPreviewBytes {
		cut := completionPreviewBytes - len(completionTruncation)
		for cut > 0 && text[cut]&0xc0 == 0x80 {
			cut-- // keep UTF-8 whole
		}
		text = text[:cut] + completionTruncation
	}
	return text
}

// completionNotifier delivers notices to the session and batches sibling successes (its completion-batcher: a short
// debounce and a max wait measured from the first held item).
type completionNotifier struct {
	mu      sync.Mutex
	send    func(content string, display bool) error
	pending []completionNotice
	timer   *time.Timer
	firstAt time.Time
}

// completions is this session's notifier.
var completions completionNotifier

// setCompletionSender binds the session's delivery (extension.go: the original's pi.sendMessage with triggerTurn) or
// clears it when the session ends.
func setCompletionSender(send func(content string, display bool) error) {
	completions.mu.Lock()
	defer completions.mu.Unlock()
	completions.send = send
}

// deliver records one settled turn. A success waits briefly for siblings; anything else flushes held successes and
// sends at once (the original: failures and attention signals never wait behind a batch).
func (c *completionNotifier) deliver(n completionNotice) {
	if n.status != "completed" {
		c.flush()
		c.emit([]completionNotice{n})
		return
	}
	c.mu.Lock()
	if len(c.pending) == 0 {
		c.firstAt = time.Now()
	}
	c.pending = append(c.pending, n)
	wait := completionDebounce
	if left := completionMaxWait - time.Since(c.firstAt); left < wait {
		wait = left
	}
	if wait <= 0 {
		held := c.pending
		c.pending = nil
		c.mu.Unlock()
		c.emit(held)
		return
	}
	if c.timer == nil {
		c.timer = time.AfterFunc(wait, func() { c.flush() })
	}
	c.mu.Unlock()
}

// flush delivers any held successes at once (its batcher's flush).
func (c *completionNotifier) flush() {
	c.mu.Lock()
	held := c.pending
	c.pending = nil
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	c.mu.Unlock()
	c.emit(held)
}

// emit sends one message for the group.
func (c *completionNotifier) emit(items []completionNotice) {
	if len(items) == 0 {
		return
	}
	content, display := formatSingleCompletion(items[0]), items[0].display
	if len(items) > 1 {
		content = formatGroupedCompletion(items)
		display = false
		for _, n := range items {
			display = display || n.display
		}
	}
	c.mu.Lock()
	send := c.send
	c.mu.Unlock()
	if send == nil {
		return // a session that is gone keeps only the fleet views
	}
	_ = send(content, display) // delivery is best-effort; the registry still holds every result
}
