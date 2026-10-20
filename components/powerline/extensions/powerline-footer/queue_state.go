package powerline_footer

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

// The queue half of the extension core: the summary the bar shows, item capture, delivery to the session and the delivery after
// a compaction. upstream: index.ts 1244-1263 (state), 1485-1750 (helpers) and the compaction/agent handlers that touch the queue
// (1836-1915, 1994, 2088, 2105-2150, 2474).
//
// Locking: the core is serialised by one mutex (queueState.mu, the app's in production). Every method here assumes the caller
// holds it, except the entry points (emit, the timers' callbacks and the command), which take it. deliver unlocks it around the
// host call that sends a message, because the host may deliver the events of the message it starts (before_agent_start,
// message_start) before it answers the call, and those take the lock.

const (
	queueSummaryCacheTTLMs  = 250 // QUEUE_SUMMARY_CACHE_TTL_MS
	postCompactionDelay     = 50 * time.Millisecond
	pendingDeliveryDeadline = 60 * time.Second
)

// queueHostAPI is what the queue needs from the host beyond hostAPI. Sender returns pi.sendUserMessage as a function bound to
// the current context (deliverAs "" sends without options): the queue calls it without the lock, when the context may change.
// IsIdle is ctx.isIdle() and Compact is ctx.compact({onError}); onError is called later, from another goroutine.
type queueHostAPI interface {
	Sender() func(text, deliverAs string) error
	IsIdle() (bool, error)
	SetEditorText(text string)
	Compact(onError func(error))
}

// queueTimer is a pending timeout or a running interval.
type queueTimer interface{ Stop() }

// queueClock makes the timers of the queue replaceable: the tests drive them by hand.
type queueClock interface {
	After(d time.Duration, f func()) queueTimer
	Every(d time.Duration, f func()) queueTimer
}

type realQueueClock struct{}

func (realQueueClock) After(d time.Duration, f func()) queueTimer {
	t := time.AfterFunc(d, f)
	return stopFunc(func() { t.Stop() })
}

func (realQueueClock) Every(d time.Duration, f func()) queueTimer {
	ticker := time.NewTicker(d)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				f()
			case <-done:
				return
			}
		}
	}()
	return stopFunc(func() { ticker.Stop(); close(done) })
}

type stopFunc func()

func (f stopFunc) Stop() { f() }

// noQueueHost serves a host that cannot send messages (the render tests' scripted host).
type noQueueHost struct{}

func (noQueueHost) Sender() func(string, string) error {
	return func(string, string) error { return errNoQueueHost }
}
func (noQueueHost) IsIdle() (bool, error) { return true, nil }
func (noQueueHost) SetEditorText(string)  {}
func (noQueueHost) Compact(func(error))   {}

type queueError string

func (e queueError) Error() string { return string(e) }

const errNoQueueHost = queueError("this host cannot send messages")

type queueSummaryCache struct {
	cwd, sessionID string
	compacting     bool
	expiresAt      int64
	summary        queueSummary
}

type postCompactionDelivery struct {
	generation int
	context    queueContext
}

type pendingQueueDelivery struct {
	text  string
	timer queueTimer
}

type queueState struct {
	p     *powerline
	mu    sync.Locker
	host  queueHostAPI
	clock queueClock
	store *queueStore

	summaryCache         *queueSummaryCache
	sessionGeneration    int
	compactionGeneration int
	// ctxLive is `currentCtx !== null`: false before the first session_start and after session_shutdown.
	ctxLive bool

	postCompaction  *postCompactionDelivery
	deliveryTimer   queueTimer
	deliveryTimerID int

	pending      map[string]*pendingQueueDelivery
	pendingOrder []string
	// spawn runs a host call that nothing waits for; the tests run it inline.
	spawn func(func())

	// The editor-facing half (queue_editor.go).
	pendingSend   *pendingSend
	editor        queueEditor
	requestRender func()
}

func newQueueState(p *powerline, host queueHostAPI) *queueState {
	return &queueState{p: p, mu: &sync.Mutex{}, host: host, clock: realQueueClock{}, store: defaultQueueStore(), pending: map[string]*pendingQueueDelivery{}, spawn: func(f func()) { go f() }}
}

// queueCollapseSpace is text.replace(/\s+/g, " ").trim().
func queueCollapseSpace(text string) string {
	return strings.Join(strings.FieldsFunc(text, isJSSpace), " ")
}

// queuePromptText is getPromptHistoryText of index.ts 435: the text of a message's content, white space collapsed.
func queuePromptText(content any) string {
	switch c := content.(type) {
	case string:
		return queueCollapseSpace(c)
	case []any:
		var parts []string
		for _, block := range c {
			b := asMap(block)
			if b == nil || b["type"] != "text" {
				continue
			}
			if text, ok := b["text"].(string); ok {
				parts = append(parts, text)
			}
		}
		return queueCollapseSpace(strings.Join(parts, "\n"))
	}
	return ""
}

// context is getQueueContext: the working directory and, when the host knows one, the session.
func (q *queueState) context() queueContext {
	cwd := q.p.h.Cwd()
	if cwd == "" {
		cwd = "."
	}
	return currentQueueContext(cwd, q.p.h.SessionID())
}

// summary is getQueueSummary: the store's summary, cached for 250 ms per context and compaction state.
func (q *queueState) summary() (queueSummary, error) {
	context := q.context()
	now := clock()
	if c := q.summaryCache; c != nil && c.cwd == context.Cwd && c.sessionID == context.SessionID && c.compacting == q.p.compacting && now < c.expiresAt {
		return c.summary, nil
	}
	summary, err := q.store.summarize(context, q.p.compacting)
	if err != nil {
		return queueSummary{}, err
	}
	q.summaryCache = &queueSummaryCache{context.Cwd, context.SessionID, q.p.compacting, now + queueSummaryCacheTTLMs, summary}
	return summary, nil
}

// segmentSummary is the queue summary of the render context: only a layout with the queue segment reads the inbox. A store
// that cannot be read leaves the bar without a queue (the original lets the exception reach the renderer).
func (q *queueState) segmentSummary(segments []string) queueSummary {
	for _, id := range segments {
		if id == "queue" {
			if s, err := q.summary(); err == nil {
				return s
			}
			break
		}
	}
	return queueSummary{Compacting: q.p.compacting}
}

// requestQueueRender is requestQueueRender: drop the cached summary and repaint the bar and the queue widgets.
func (q *queueState) requestQueueRender() {
	q.summaryCache = nil
	if q.p.enabled && q.p.h.HasUI() {
		q.p.base = nil
		q.p.h.Repaint()
	}
}

// captureQueueItem adds an item to the inbox for the current context.
func (q *queueState) captureQueueItem(text string, intent queueIntent, target queueTarget) (queueItem, error) {
	item, err := q.store.add(createQueueItemInput{Text: text, Intent: intent, Target: target, Source: q.context()})
	if err != nil {
		return queueItem{}, err
	}
	q.requestQueueRender()
	return item, nil
}

// capturePostCompactPrompt queues a prompt typed while the session compacts, for delivery when the compaction is done. The
// original's editor calls it for a prompt submitted during a compaction and for `/compact <prompt>`; it returns nil for a blank
// prompt. Nothing in this port calls it until the SDK gives an extension the editor's submit path.
func (q *queueState) capturePostCompactPrompt(text string) (*queueItem, error) {
	trimmed := jsTrim(text)
	if trimmed == "" {
		return nil, nil
	}
	item, err := q.captureQueueItem(trimmed, intentPostCompact, queueTarget{Kind: targetCurrentSession})
	if err != nil {
		return nil, err
	}
	q.p.h.Notify("Queued for after compaction ("+item.ID+")", "info")
	return &item, nil
}

// deliveryMode is deliveryModeForItem: an idle session takes the message as a prompt, a busy one as steering or a follow-up.
func (q *queueState) deliveryMode(item queueItem) string {
	idle, err := q.host.IsIdle()
	if err == nil && idle {
		return ""
	}
	if item.Intent == intentSteer {
		return "steer"
	}
	return "followUp"
}

func (q *queueState) clearPendingDelivery(id string) {
	pending, ok := q.pending[id]
	if !ok {
		return
	}
	pending.timer.Stop()
	delete(q.pending, id)
	for i, other := range q.pendingOrder {
		if other == id {
			q.pendingOrder = append(q.pendingOrder[:i], q.pendingOrder[i+1:]...)
			break
		}
	}
}

// trackPendingDelivery waits 60 seconds for the host to start the message; an item still "delivering" then goes back to queued.
func (q *queueState) trackPendingDelivery(item queueItem, text string) {
	q.clearPendingDelivery(item.ID)
	entry := &pendingQueueDelivery{text: text}
	q.pending[item.ID] = entry
	q.pendingOrder = append(q.pendingOrder, item.ID)
	entry.timer = q.clock.After(pendingDeliveryDeadline, func() {
		q.mu.Lock()
		defer q.mu.Unlock()
		if q.pending[item.ID] != entry {
			return
		}
		q.clearPendingDelivery(item.ID)
		current, err := q.store.get(item.ID)
		if err != nil {
			q.reportError(err)
			return
		}
		if current != nil && current.Status == statusDelivering {
			if _, err := q.store.update(item.ID, queueUpdates{Status: statusQueued, SetError: true, Error: "Queued message did not start"}); err != nil {
				q.reportError(err)
				return
			}
			q.requestQueueRender()
		}
	})
}

// requeuePendingDeliveries puts every item that was sent but never started back to queued.
func (q *queueState) requeuePendingDeliveries(message string) error {
	for _, id := range append([]string(nil), q.pendingOrder...) {
		q.clearPendingDelivery(id)
		current, err := q.store.get(id)
		if err != nil {
			return err
		}
		if current != nil && current.Status == statusDelivering {
			if _, err := q.store.update(id, queueUpdates{Status: statusQueued, SetError: true, Error: message}); err != nil {
				return err
			}
		}
	}
	return nil
}

// finishPendingDelivery marks the item whose text the host just started as sent (finishPendingQueueDelivery).
func (q *queueState) finishPendingDelivery(text string) error {
	normalized := queueCollapseSpace(text)
	for _, id := range append([]string(nil), q.pendingOrder...) {
		if queueCollapseSpace(q.pending[id].text) != normalized {
			continue
		}
		q.clearPendingDelivery(id)
		updated, err := q.store.update(id, queueUpdates{Status: statusSent, SetError: true})
		if err != nil || updated == nil {
			return err
		}
		q.p.h.Notify("Sent queued item "+id, "info")
		q.requestQueueRender()
		return nil
	}
	return nil
}

// deliverQueueItem hands an item to the session; it reports whether a message was handed over (the host's refusal comes later,
// see failDelivery). During a compaction the item stays queued.
func (q *queueState) deliverQueueItem(item queueItem) (bool, error) {
	if q.p.compacting {
		if _, err := q.store.update(item.ID, queueUpdates{Status: statusQueued}); err != nil {
			return false, err
		}
		q.requestQueueRender()
		return false, nil
	}
	if _, err := q.store.update(item.ID, queueUpdates{Status: statusDelivering, SetError: true}); err != nil {
		return false, err
	}
	q.requestQueueRender()

	deliverAs := q.deliveryMode(item)
	text := formatQueueDeliveryText(item)
	q.trackPendingDelivery(item, text)
	// pi.sendUserMessage does not wait for the message to start (the host reports a failure as an extension error). The host call
	// runs on its own goroutine, without the lock: PiG answers it only after the events of the message it starts
	// (before_agent_start, message_start) were handled, and those handlers take the lock. Waiting for it here would also put the
	// command's response behind them, where Pi's comes first.
	send := q.host.Sender()
	q.mu.Unlock()
	q.spawn(func() {
		err := send(text, deliverAs)
		if err == nil {
			return
		}
		q.mu.Lock()
		defer q.mu.Unlock()
		if ferr := q.failDelivery(item, err); ferr != nil {
			q.reportError(ferr)
		}
	})
	q.mu.Lock()
	return true, nil
}

// failDelivery marks an item the host refused as failed and tells the user.
func (q *queueState) failDelivery(item queueItem, err error) error {
	q.clearPendingDelivery(item.ID)
	if _, uerr := q.store.update(item.ID, queueUpdates{Status: statusFailed, SetError: true, Error: err.Error()}); uerr != nil {
		return uerr
	}
	q.p.h.Notify("Failed to send "+item.ID+": "+err.Error(), "error")
	q.requestQueueRender()
	return nil
}

func (q *queueState) cancelPostCompactionDelivery() {
	if q.deliveryTimer != nil {
		q.deliveryTimer.Stop()
	}
	q.deliveryTimer = nil
	q.deliveryTimerID++
	q.postCompaction = nil
}

// schedulePostCompactionDelivery delivers the next post-compaction item in 50 ms, when the session is idle; a busy session is
// asked again later, without reading the inbox.
func (q *queueState) schedulePostCompactionDelivery() {
	if q.postCompaction == nil {
		return
	}
	if q.deliveryTimer != nil {
		q.deliveryTimer.Stop()
	}
	pending := q.postCompaction
	q.deliveryTimerID++
	id := q.deliveryTimerID
	q.deliveryTimer = q.clock.After(postCompactionDelay, func() {
		q.mu.Lock()
		defer q.mu.Unlock()
		if q.deliveryTimerID != id {
			return
		}
		q.deliveryTimer = nil
		if err := q.deliverNextAfterCompaction(pending); err != nil {
			q.reportError(err)
		}
	})
}

func (q *queueState) deliverNextAfterCompaction(pending *postCompactionDelivery) error {
	if pending != q.postCompaction {
		return nil
	}
	if pending.generation != q.sessionGeneration || !q.ctxLive {
		q.cancelPostCompactionDelivery()
		return nil
	}
	// Busy readiness checks must not read the inbox, render, or enqueue a late follow-up. A host that cannot say is not idle.
	if idle, err := q.host.IsIdle(); err != nil || !idle {
		q.schedulePostCompactionDelivery()
		return nil
	}
	items, err := q.store.queuedDeliveryItems(pending.context, intentPostCompact)
	if err != nil {
		return err
	}
	if len(items) <= 1 {
		q.cancelPostCompactionDelivery()
	}
	if len(items) == 0 {
		return nil
	}
	if _, err := q.deliverQueueItem(items[0]); err != nil {
		return err
	}
	q.schedulePostCompactionDelivery()
	return nil
}

// reportError shows an error nobody is waiting for (a timer's): the original lets it escape the timer callback.
func (q *queueState) reportError(err error) {
	if q.p.h.HasUI() {
		q.p.h.Notify(err.Error(), "error")
	}
}

func queuePlural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// blockPostCompactionQueue keeps the queued post-compaction items of the context, blocked, after a failed compaction.
func (q *queueState) blockPostCompactionQueue(message string) error {
	items, err := q.store.queuedDeliveryItems(q.context(), intentPostCompact)
	if err != nil {
		return err
	}
	for _, item := range items {
		if _, err := q.store.update(item.ID, queueUpdates{Status: statusBlocked, SetError: true, Error: message}); err != nil {
			return err
		}
	}
	if len(items) > 0 {
		q.p.h.Notify("Kept "+strconv.Itoa(len(items))+" post-compaction item"+queuePlural(len(items))+" blocked", "warning")
		q.requestQueueRender()
	}
	return nil
}

func (q *queueState) finishFailedCompaction(message string) error {
	q.compactionGeneration++
	q.p.compacting = false
	q.cancelPostCompactionDelivery()
	err := q.blockPostCompactionQueue(message)
	q.requestQueueRender()
	return err
}

// onSessionStart is the queue's part of the session_start handler (the core calls it first, before it draws the bar).
func (q *queueState) onSessionStart() {
	q.sessionGeneration++
	q.ctxLive = true
	q.p.compacting = false
	q.cancelPostCompactionDelivery()
}

// emit is the queue's part of the event handlers. It takes the lock.
func (q *queueState) emit(name string, data map[string]any) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	switch name {
	case "session_start":
		q.onSessionStart()
	case "session_shutdown":
		q.sessionGeneration++
		q.restorePendingSend()
		q.cancelPostCompactionDelivery()
		err := q.requeuePendingDeliveries("Session ended before queued message started")
		q.p.compacting = false
		q.ctxLive = false
		return err
	case "session_before_compact":
		q.p.compacting = true
		q.ctxLive = true
		q.compactionGeneration++
		q.cancelPostCompactionDelivery()
		q.requestQueueRender()
	case "session_compact":
		q.p.compacting = false
		q.ctxLive = true
		q.compactionGeneration++
		q.cancelPostCompactionDelivery()
		context := q.context()
		items, err := q.store.queuedDeliveryItems(context, intentPostCompact)
		if err != nil {
			return err
		}
		if len(items) > 0 {
			q.postCompaction = &postCompactionDelivery{generation: q.sessionGeneration, context: context}
			q.schedulePostCompactionDelivery()
		}
		q.requestQueueRender()
	case "session_compact_failed":
		message, ok := data["errorMessage"].(string)
		if !ok {
			message = "Compaction cancelled"
		}
		q.ctxLive = true
		return q.finishFailedCompaction(message)
	case "agent_settled":
		q.schedulePostCompactionDelivery()
	case "agent_end":
		q.ctxLive = true
		q.schedulePostCompactionDelivery()
	case "before_agent_start":
		prompt, _ := data["prompt"].(string)
		return q.finishPendingDelivery(prompt)
	case "message_start":
		q.ctxLive = true
		if message := asMap(data["message"]); message != nil && message["role"] == "user" {
			return q.finishPendingDelivery(queuePromptText(message["content"]))
		}
	}
	return nil
}

// renderQueuePreviewLines is renderPowerlineQueuePreviewLines: one row under the editor with the leading queued item.
func renderQueuePreviewLines(summary queueSummary, width int, th theme) []string {
	if summary.LeadingText == "" {
		return []string{}
	}
	blocked := summary.LeadingStatus == statusBlocked || summary.LeadingStatus == statusFailed
	prefix, color := "queued: ", "dim"
	switch {
	case blocked:
		prefix, color = "blocked: ", "warning"
	case summary.LeadingStatus == statusDelivering:
		prefix = "sending: "
	}
	text := prefix + queueCollapseSpace(summary.LeadingText)
	colored, err := th.Fg(color, truncateToWidth(text, max(1, width-1), "…"))
	if err != nil {
		return []string{}
	}
	return []string{" " + colored}
}

// renderQueuePreview is the powerline-queue-preview widget at a width.
func (p *powerline) renderQueuePreview(width int) []string {
	q := p.queue
	if !q.ctxLive {
		return []string{}
	}
	summary, err := q.summary()
	if err != nil {
		return []string{}
	}
	return renderQueuePreviewLines(summary, width, p.h.Theme())
}

// renderPendingSend is the powerline-pending-send widget at a width.
func (p *powerline) renderPendingSend(width int) []string {
	return renderPendingSendLines(p.queue.pendingSend, width, p.h.Theme())
}
