package shell

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

// upstream: packages/coding-agent/src/async/notify.ts

const (
	// NotifyBatchWindow is how long matching lines coalesce into one batch (200ms).
	NotifyBatchWindow = 200 * time.Millisecond
	// NotifyMaxLines is the max matching lines kept per batch (50).
	NotifyMaxLines = 50
	// NotifyMaxLineChars is the max characters kept of one reported line (400).
	NotifyMaxLineChars = 400
	// NotifyBurst is the batch allowance before notify switches itself off (30).
	NotifyBurst = 30
	// NotifyRefillInterval is how long one batch of refilling allowance takes (20s).
	NotifyRefillInterval = 20 * time.Second
)

// NotifyPattern is a compiled `notify` regex.
type NotifyPattern struct {
	Source string
	RE     *regexp.Regexp
}

// CompileNotifyPattern compiles a linear-time RE2 regex for `bash.notify`.
// upstream: async/notify.ts:50
func CompileNotifyPattern(source string) (*NotifyPattern, error) {
	re, err := regexp.Compile(source)
	if err != nil {
		return nil, fmt.Errorf("Invalid notify regex: %v", err)
	}
	return &NotifyPattern{Source: source, RE: re}, nil
}

// NotifyBatch is one coalesced batch of lines matching a `notify` pattern.
type NotifyBatch struct {
	Lines   []string
	Omitted int
	Stopped bool
}

// OutputNotifier watches streaming text chunks, matches complete lines against
// a NotifyPattern, and emits bounded batches with token-bucket rate limiting.
// upstream: async/notify.ts:87
type OutputNotifier struct {
	mu          sync.Mutex
	pattern     *NotifyPattern
	emit        func(NotifyBatch)
	refill      bool
	batchWindow time.Duration
	refillEvery time.Duration
	nowFn       func() time.Time

	partial    string
	lines      []string
	omitted    int
	timer      *time.Timer
	allowance  int
	refilledAt time.Time
	stopped    bool
}

// OutputNotifierOptions configures an OutputNotifier.
type OutputNotifierOptions struct {
	Refill      *bool
	BatchWindow time.Duration
	RefillEvery time.Duration
	Now         func() time.Time
}

// NewOutputNotifier creates an OutputNotifier for pattern.
func NewOutputNotifier(pattern *NotifyPattern, emit func(NotifyBatch), opts OutputNotifierOptions) *OutputNotifier {
	refill := true
	if opts.Refill != nil {
		refill = *opts.Refill
	}
	window := opts.BatchWindow
	if window <= 0 {
		window = NotifyBatchWindow
	}
	refillEvery := opts.RefillEvery
	if refillEvery <= 0 {
		refillEvery = NotifyRefillInterval
	}
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	return &OutputNotifier{
		pattern:     pattern,
		emit:        emit,
		refill:      refill,
		batchWindow: window,
		refillEvery: refillEvery,
		nowFn:       nowFn,
		allowance:   NotifyBurst,
		refilledAt:  nowFn(),
	}
}

// Push feeds raw text (normalized to \n) and schedules a batch flush if any
// complete line matched.
func (n *OutputNotifier) Push(chunk string) {
	if chunk == "" {
		return
	}
	chunk = strings.ReplaceAll(chunk, "\r\n", "\n")
	chunk = strings.ReplaceAll(chunk, "\r", "\n")

	n.mu.Lock()
	defer n.mu.Unlock()
	if n.stopped {
		return
	}
	data := n.partial + chunk
	var complete []string
	for {
		idx := strings.IndexByte(data, '\n')
		if idx < 0 {
			break
		}
		complete = append(complete, data[:idx])
		data = data[idx+1:]
	}
	n.partial = data
	n.collectLocked(complete)
	if len(n.lines) > 0 && n.timer == nil {
		n.timer = time.AfterFunc(n.batchWindow, n.flush)
	}
}

// Finish flushes any remaining matched lines, including an unterminated final
// line, and stops the notifier.
func (n *OutputNotifier) Finish() {
	n.mu.Lock()
	if n.stopped {
		n.mu.Unlock()
		return
	}
	if n.partial != "" {
		n.collectLocked([]string{n.partial})
		n.partial = ""
	}
	batch := NotifyBatch{
		Lines:   n.lines,
		Omitted: n.omitted,
		Stopped: false,
	}
	n.stopLocked()
	emit := n.emit
	n.mu.Unlock()

	if len(batch.Lines) > 0 && emit != nil {
		emit(batch)
	}
}

// Stop immediately stops the notifier and drops unsent matches.
func (n *OutputNotifier) Stop() {
	n.mu.Lock()
	n.stopLocked()
	n.mu.Unlock()
}

func (n *OutputNotifier) stopLocked() {
	n.stopped = true
	if n.timer != nil {
		n.timer.Stop()
		n.timer = nil
	}
	n.lines = nil
	n.omitted = 0
}

func (n *OutputNotifier) collectLocked(lines []string) {
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !n.pattern.RE.MatchString(line) {
			continue
		}
		if len(n.lines) < NotifyMaxLines {
			n.lines = append(n.lines, truncateRunes(line, NotifyMaxLineChars))
		} else {
			n.omitted++
		}
	}
}

func (n *OutputNotifier) flush() {
	n.mu.Lock()
	if n.stopped || len(n.lines) == 0 {
		n.timer = nil
		n.mu.Unlock()
		return
	}
	now := n.nowFn()
	if n.refill {
		elapsed := now.Sub(n.refilledAt)
		if elapsed >= n.refillEvery {
			refillCount := int(elapsed / n.refillEvery)
			n.allowance = min(NotifyBurst, n.allowance+refillCount)
			n.refilledAt = n.refilledAt.Add(time.Duration(refillCount) * n.refillEvery)
		}
	}
	n.allowance--
	batch := NotifyBatch{
		Lines:   n.lines,
		Omitted: n.omitted,
		Stopped: n.allowance <= 0,
	}
	n.timer = nil
	n.lines = nil
	n.omitted = 0
	if batch.Stopped {
		n.stopped = true
	}
	emit := n.emit
	n.mu.Unlock()

	if emit != nil {
		emit(batch)
	}
}

func truncateRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	if maxRunes <= 1 {
		return "…"
	}
	return string(runes[:maxRunes-1]) + "…"
}
