package prediction

import (
	"context"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const (
	// DefaultDeadline is the maximum time a prediction request may remain
	// in-flight before being aborted and discarded.
	// upstream: packages/coding-agent/src/modes/controllers/composer-prediction-controller.ts:32
	DefaultDeadline = 15 * time.Second

	// SettingPredictions is the boolean setting key enabling composer predictions (default true).
	SettingPredictions = "composer.predictions"

	// SettingAlternatives is the boolean setting key enabling up to 4 alternatives (default false).
	SettingAlternatives = "composer.predictionAlternatives"
)

type sessionState struct {
	gen       uint64
	cancel    context.CancelFunc
	choices   []string
	anchorID  string
	ctx       sdk.Context
	hasCtx    bool
	published bool
	// widgetMu serializes publishWidget and clearWidget calls for this session
	// so a concurrent Cancel/Dispose can never be overwritten by an earlier
	// goroutine's publishWidget landing afterwards.
	widgetMu sync.Mutex
}

// Controller manages per-session asynchronous composer prediction requests,
// cancellation, generation tracking, and serialized widget publication.
type Controller struct {
	mu          sync.Mutex
	sessions    map[string]*sessionState
	deadline    time.Duration
	complete    CompleteFunc
	publishHook func(ctx sdk.Context, choices []string)
	clearHook   func(ctx sdk.Context)
	wg          sync.WaitGroup
}

// NewController creates a Controller with default deadline and completion hook.
func NewController() *Controller {
	return &Controller{
		sessions:    make(map[string]*sessionState),
		deadline:    DefaultDeadline,
		complete:    defaultComplete,
		publishHook: publishWidget,
		clearHook:   clearWidget,
	}
}

func (c *Controller) stateLocked(sid string) *sessionState {
	st := c.sessions[sid]
	if st == nil {
		st = &sessionState{}
		c.sessions[sid] = st
	}
	return st
}

// Choices returns a copy of the currently active prediction choices for sid,
// or nil if none are active.
func (c *Controller) Choices(sid string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.sessions[sid]
	if st == nil || len(st.choices) == 0 {
		return nil
	}
	out := make([]string, len(st.choices))
	copy(out, st.choices)
	return out
}

// Request starts a new asynchronous prediction for the completed turn if all
// safety gates pass, cancelling and retiring any prior prediction for the
// session.
// upstream: composer-prediction-controller.ts:160-183, event-controller.ts:2619
func (c *Controller) Request(ctx sdk.Context, eventData map[string]any) {
	sid := sessionKey(ctx)
	if sid == "" {
		return
	}

	// Always retire any earlier prediction first (upstream: `this.cancel()`).
	c.Cancel(ctx)

	// CRITICAL zero-leak gating: interactive UI sessions only, never
	// print/headless/RPC/oracle mode and never subagent sessions.
	if !ctx.HasUI() || ctx.Mode() != "tui" {
		return
	}
	if IsSubagent != nil && IsSubagent(ctx) {
		return
	}
	if !predictionsEnabled(ctx) {
		return
	}

	// Only predict when the turn settled cleanly (`state === "done"` in upstream
	// event-controller.ts:2560-2566,2619).
	if !settledDone(eventData) {
		return
	}

	// Any draft (even whitespace) suppresses the billed prediction request.
	if draft, err := ctx.GetEditorText(); err != nil || draft != "" {
		return
	}

	sessMessages, err := loadSessionContextMessages(ctx)
	if err != nil || !contextHasCompletedAssistant(sessMessages) {
		return
	}

	alternatives := alternativesEnabled(ctx)
	modelObj, req, auth, ok := buildPredictionRequest(ctx, sessMessages, alternatives)
	if !ok {
		return
	}

	anchor := contextAnchor(ctx, sessMessages)

	c.mu.Lock()
	st := c.stateLocked(sid)
	st.gen++
	gen := st.gen
	st.ctx = ctx
	st.hasCtx = true
	st.anchorID = anchor
	st.choices = nil
	if st.cancel != nil {
		st.cancel()
	}
	deadline := c.deadline
	if deadline <= 0 {
		deadline = DefaultDeadline
	}
	reqCtx, cancel := context.WithTimeout(context.Background(), deadline)
	st.cancel = cancel
	completeFn := c.complete
	if completeFn == nil {
		completeFn = defaultComplete
	}
	publishFn := c.publishHook
	if publishFn == nil {
		publishFn = publishWidget
	}
	c.wg.Add(1)
	c.mu.Unlock()

	go func() {
		defer c.wg.Done()
		defer cancel()

		raw, err := completeFn(reqCtx, ctx, modelObj, req, auth)
		if err != nil || reqCtx.Err() != nil {
			c.clearCancelIfGen(sid, gen)
			return
		}
		replyText, err := extractCompleteText(raw)
		if err != nil {
			c.clearCancelIfGen(sid, gen)
			return
		}
		choices := ParseReply(replyText, alternatives)
		if len(choices) == 0 {
			c.clearCancelIfGen(sid, gen)
			return
		}

		// Re-check editor text before offering so a user who started typing
		// while the request was in-flight doesn't get a stale widget.
		if draft, err := ctx.GetEditorText(); err != nil || draft != "" {
			c.clearCancelIfGen(sid, gen)
			return
		}

		// Re-check conversation anchor so new messages invalidate stale replies.
		if curMessages, err := loadSessionContextMessages(ctx); err == nil {
			if contextAnchor(ctx, curMessages) != anchor {
				c.clearCancelIfGen(sid, gen)
				return
			}
		}

		// Serialize widget publication with Cancel/Dispose for this session:
		// acquire st.widgetMu first, then verify generation under c.mu before
		// setting published=true and calling publishFn while holding st.widgetMu.
		// Because Cancel/Dispose also acquires st.widgetMu before clearWidget,
		// publish and clear are strictly ordered per session.
		st.widgetMu.Lock()
		defer st.widgetMu.Unlock()

		c.mu.Lock()
		cur := c.sessions[sid]
		if cur != st || st.gen != gen {
			c.mu.Unlock()
			return
		}
		st.cancel = nil
		st.choices = choices
		st.published = true
		c.mu.Unlock()

		publishFn(ctx, choices)
	}()
}

func (c *Controller) clearCancelIfGen(sid string, gen uint64) {
	c.mu.Lock()
	if st := c.sessions[sid]; st != nil && st.gen == gen {
		st.cancel = nil
	}
	c.mu.Unlock()
}

// Cancel aborts any in-flight prediction and clears any published widget for
// ctx's session. Serializes with any concurrent publish via per-session widgetMu.
func (c *Controller) Cancel(ctx sdk.Context) {
	sid := sessionKey(ctx)
	if sid == "" {
		return
	}

	c.mu.Lock()
	st := c.sessions[sid]
	if st == nil {
		c.mu.Unlock()
		return
	}
	st.gen++
	cancel := st.cancel
	st.cancel = nil
	st.choices = nil
	clearFn := c.clearHook
	if clearFn == nil {
		clearFn = clearWidget
	}
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	st.widgetMu.Lock()
	c.mu.Lock()
	wasPublished := st.published
	st.published = false
	c.mu.Unlock()
	if wasPublished {
		clearFn(ctx)
	}
	st.widgetMu.Unlock()
}

// Dispose releases all state for sessionID (called from lifecycle.OnEnd and
// session_shutdown). Serializes widget clearing with any concurrent publish.
func (c *Controller) Dispose(sessionID string) {
	if sessionID == "" {
		return
	}

	c.mu.Lock()
	st := c.sessions[sessionID]
	if st == nil {
		c.mu.Unlock()
		return
	}
	st.gen++
	cancel := st.cancel
	st.cancel = nil
	st.choices = nil
	ctx := st.ctx
	hasCtx := st.hasCtx
	clearFn := c.clearHook
	if clearFn == nil {
		clearFn = clearWidget
	}
	delete(c.sessions, sessionID)
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	st.widgetMu.Lock()
	c.mu.Lock()
	shouldClear := st.published && hasCtx
	st.published = false
	c.mu.Unlock()
	if shouldClear {
		clearFn(ctx)
	}
	st.widgetMu.Unlock()
}

// Wait blocks until all in-flight prediction goroutines complete (used in tests).
func (c *Controller) Wait() {
	c.wg.Wait()
}

// settledDone mirrors omp's `event-controller.ts:2560-2566` (`#settledRunStatus`).
// Returns true only when `eventData` carries a non-error, non-aborted assistant
// message (or when `messages` is omitted by a minimal harness and `interrupted`
// is not set, in which case `contextHasCompletedAssistant` verifies the turn).
func settledDone(data map[string]any) bool {
	if data == nil {
		return true
	}
	if interrupted, _ := data["interrupted"].(bool); interrupted {
		return false
	}
	raw, hasMessages := data["messages"]
	if !hasMessages || raw == nil {
		return true
	}
	msgs, ok := raw.([]any)
	if !ok {
		return false
	}
	if len(msgs) == 0 {
		return false
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		m, ok := msgs[i].(map[string]any)
		if !ok {
			continue
		}
		role, _ := m["role"].(string)
		if role != "assistant" {
			continue
		}
		stopReason, _ := m["stopReason"].(string)
		if stopReason == "error" || stopReason == "aborted" {
			return false
		}
		return true
	}
	return false
}

func predictionsEnabled(ctx sdk.Context) bool {
	if Setting == nil {
		return true
	}
	v := Setting(ctx, SettingPredictions)
	if b, ok := v.(bool); ok {
		return b
	}
	return true
}

func alternativesEnabled(ctx sdk.Context) bool {
	if Setting == nil {
		return false
	}
	v := Setting(ctx, SettingAlternatives)
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

func sessionKey(ctx sdk.Context) string {
	if id, err := ctx.GetSessionID(); err == nil {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
