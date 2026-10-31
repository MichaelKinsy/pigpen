package pi_subagents

import (
	"fmt"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Completion delivery (src/runs/background/notify.ts): a finished async run
// sends the original's notice into the session with pi.sendMessage
// (customType "subagent-notify"), which wakes the parent through the host's
// native completion path. Completed runs batch briefly, as the original's
// completion batcher does; a failed or stopped run flushes and sends at once.

const (
	notifyCustomType = "subagent-notify"
	notifyDedupeTTL  = int64(10 * 60 * 1000)
	notifyDebounceMs = 150
	notifyMaxWaitMs  = 1000
)

// notifyState is the process-wide delivery bookkeeping (dedupe + batching).
type notifyState struct {
	mu      sync.Mutex
	seen    map[string]int64
	pending map[string]*notifyBatch
}

type notifyBatch struct {
	items   []*asyncRun
	started int64
}

var notifier = &notifyState{seen: map[string]int64{}, pending: map[string]*notifyBatch{}}

// buildSingleCompletion is the original's formatSingleCompletion for a run of
// this port: the header, the result preview and the correlation lines.
func buildSingleCompletion(r *asyncRun) string {
	status := completionStatus(r)
	lines := []string{fmt.Sprintf("Background task %s: **%s**", status, r.Agent), "", completionPreview(r)}
	correlation := []string{fmt.Sprintf("Retention-managed async directory: %s", r.Dir)}
	lines = append(lines, "")
	lines = append(lines, correlation...)
	return strings.Join(lines, "\n")
}

// buildGroupedCompletion is the original's formatGroupedCompletion.
func buildGroupedCompletion(rs []*asyncRun) string {
	heads := []string{}
	for _, r := range rs {
		heads = append(heads, fmt.Sprintf("**%s**", r.Agent))
	}
	blocks := []string{fmt.Sprintf("Background tasks completed (%d): %s", len(rs), strings.Join(heads, ", ")), ""}
	for i, r := range rs {
		blocks = append(blocks, fmt.Sprintf("%d. %s", i+1, r.Agent))
		blocks = append(blocks, completionPreview(r))
		blocks = append(blocks, fmt.Sprintf("Retention-managed async directory: %s", r.Dir))
		blocks = append(blocks, "")
	}
	return strings.Join(blocks, "\n")
}

// completionSnapshot reads agent, status and output under one lock.
func (r *asyncRun) completionSnapshot() (agent, status, output string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.Agent, completionStatusLocked(r), r.Output
}

// completionStatus is the original's status derivation (stopped, paused, failed, completed).
func completionStatus(r *asyncRun) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return completionStatusLocked(r)
}

func completionStatusLocked(r *asyncRun) string {
	switch {
	case r.Stopped || r.State == "stopped":
		return "stopped"
	case r.Success:
		return "completed"
	}
	return "failed"
}

// completionPreview is the result preview of the notice: the child's answer,
// else the failure, else the original's placeholder.
func completionPreview(r *asyncRun) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.TrimSpace(r.Output) != "" {
		return r.Output
	}
	if r.Error != "" {
		return r.Error
	}
	return "(no output)"
}

// completionKey is the dedupe key of one run's notice.
func completionKey(r *asyncRun) string {
	return "session:" + r.SessionID + "|" + r.ID + "|" + completionStatus(r)
}

// deliverCompletion sends (or batches) the run's completion notice.
func deliverCompletion(ctx sdk.Context, r *asyncRun) {
	key := completionKey(r)
	notifier.mu.Lock()
	if seenAt, ok := notifier.seen[key]; ok && nowMs()-seenAt <= notifyDedupeTTL {
		notifier.mu.Unlock()
		return
	}
	status := completionStatus(r)
	if status != "completed" {
		notifier.mu.Unlock()
		sendCompletion(ctx, []*asyncRun{r}, key)
		return
	}
	batch := notifier.pending[key]
	if batch == nil {
		batch = &notifyBatch{started: nowMs()}
		notifier.pending[key] = batch
	}
	batch.items = append(batch.items, r)
	// flush the batch after the debounce window, bounded by maxWait from the first item
	wait := int64(notifyDebounceMs)
	if since := nowMs() - batch.started; since+wait > notifyMaxWaitMs {
		wait = notifyMaxWaitMs - since
		if wait < 0 {
			wait = 0
		}
	}
	notifier.mu.Unlock()
	time.AfterFunc(time.Duration(wait)*time.Millisecond, func() { flushBatch(ctx, key) })
}

// flushBatch sends one batched group of completed runs.
func flushBatch(ctx sdk.Context, key string) {
	notifier.mu.Lock()
	batch := notifier.pending[key]
	if batch == nil || len(batch.items) == 0 {
		delete(notifier.pending, key)
		notifier.mu.Unlock()
		return
	}
	delete(notifier.pending, key)
	notifier.mu.Unlock()
	sendCompletion(ctx, batch.items, key)
}

// sendCompletion sends the notice; the host wakes the session (triggerTurn).
// A send that races the session's teardown is dropped, never fatal.
func sendCompletion(ctx sdk.Context, items []*asyncRun, dedupeKey string) {
	if len(items) == 0 {
		return
	}
	defer func() { _ = recover() }()
	content := buildSingleCompletion(items[0])
	display := false
	if len(items) > 1 {
		content = buildGroupedCompletion(items)
	} else if completionStatus(items[0]) != "completed" {
		display = true
	}
	trigger := true
	if err := ctx.SendMessage(notifyCustomType, content, display, sdk.SendMessageOptions{TriggerTurn: &trigger}); err != nil {
		return
	}
	notifier.mu.Lock()
	for _, r := range items {
		notifier.seen[completionKey(r)] = nowMs()
	}
	_ = dedupeKey
	notifier.mu.Unlock()
}
