package powerline_footer

import (
	"math"
	"strings"
	"time"
)

// The editor-facing half of the queue: delayed send (powerline.sendDelayMs) and the capture of what the user types while the
// session compacts. upstream: index.ts 1554-1603 (pending send), 3335-3384 (the editor's handleInput wrapper) and 3052-3058.
//
// Both work through the powerline editor (editor_ext.go), installed with Context.SetEditorComponent (PiG v0.5.0). The original
// wraps the editor's onSubmit property, which the SDK editor does not expose, so a submit is recognised as the Enter key
// (Pi's default tui.input.submit; the SDK has no keybinding lookup, E10) and a delayed prompt is sent again later by putting its
// text in the editor and pressing Enter there, which runs Pi's own submit path (slash commands, templates, streaming
// behaviour). The draft typed during the countdown is put back after it.

// queueEditor is the part of Pi's editor the queue uses.
type queueEditor interface {
	GetExpandedText() string
	SetText(text string)
	AddToHistory(text string)
}

// queueKeyEditor is the editor as editorKey uses it.
type queueKeyEditor interface {
	queueEditor
	// ShowingAutocomplete is editor.isShowingAutocomplete().
	ShowingAutocomplete() bool
	// SubmitText sends text through the editor's submit path, as the editor's onSubmit does.
	SubmitText(text string)
}

type pendingSend struct {
	text     string
	submit   func(text string)
	deadline int64
	timer    queueTimer
	ticker   queueTimer
}

func (q *queueState) render() {
	if q.requestRender != nil {
		q.requestRender()
	}
}

// takePendingSend stops the countdown and returns what was waiting.
func (q *queueState) takePendingSend() *pendingSend {
	pending := q.pendingSend
	if pending == nil {
		return nil
	}
	q.pendingSend = nil
	pending.timer.Stop()
	pending.ticker.Stop()
	q.render()
	return pending
}

// sendPendingNow sends the waiting prompt now. During a compaction it is queued for after it instead, so that a delayed prompt
// never lands in Pi's native compaction queue.
func (q *queueState) sendPendingNow() {
	pending := q.takePendingSend()
	if pending == nil {
		return
	}
	if q.p.compacting && q.ctxLive {
		if q.editor != nil {
			q.editor.AddToHistory(pending.text)
		}
		if _, err := q.capturePostCompactPrompt(pending.text); err != nil {
			q.reportError(err)
		}
		return
	}
	pending.submit(pending.text)
}

// restorePendingSend returns the waiting prompt to the editor, before whatever was typed since.
func (q *queueState) restorePendingSend() {
	pending := q.takePendingSend()
	if pending == nil || q.editor == nil {
		return
	}
	// setText drops paste markers, so a draft typed during the countdown is kept in expanded form.
	draft := q.editor.GetExpandedText()
	if jsTrim(draft) != "" {
		q.editor.SetText(pending.text + "\n\n" + draft)
		return
	}
	q.editor.SetText(pending.text)
}

// submitWithDelay is the editor's submit under powerline.sendDelayMs: a prompt waits that long (Esc takes it back, Enter on an
// empty editor sends it now); slash and bang commands and an unset delay submit at once.
func (q *queueState) submitWithDelay(text string, submit func(text string)) {
	trimmed := jsTrim(text)
	if trimmed == "" {
		q.sendPendingNow()
		return
	}
	delay := q.p.config.SendDelayMs
	if delay <= 0 || strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "!") {
		submit(text)
		return
	}
	q.sendPendingNow()
	q.pendingSend = &pendingSend{
		text: trimmed, submit: submit, deadline: clock() + int64(delay),
		timer: q.clock.After(time.Duration(delay*float64(time.Millisecond)), func() {
			q.mu.Lock()
			defer q.mu.Unlock()
			q.sendPendingNow()
		}),
		ticker: q.clock.Every(time.Second, func() {
			q.mu.Lock()
			defer q.mu.Unlock()
			q.render()
		}),
	}
	q.render()
}

// collapseSpaceRuns is text.replace(/\s+/g, " "): each run of white space becomes one space, the ends are kept.
func collapseSpaceRuns(text string) string {
	var b strings.Builder
	inSpace := false
	for _, r := range text {
		if isJSSpace(r) {
			if !inSpace {
				b.WriteByte(' ')
			}
			inSpace = true
			continue
		}
		inSpace = false
		b.WriteRune(r)
	}
	return b.String()
}

// renderPendingSendLines is the powerline-pending-send widget: the countdown, the key hints and the waiting prompt.
func renderPendingSendLines(pending *pendingSend, width int, th theme) []string {
	if pending == nil {
		return []string{}
	}
	seconds := math.Max(1, math.Ceil(float64(pending.deadline-clock())/1000))
	hint, err := th.Fg("accent", "sending in "+jsNumber(seconds)+"s · esc to edit · enter to send now")
	if err != nil {
		return []string{}
	}
	preview, err := th.Fg("dim", collapseSpaceRuns(pending.text))
	if err != nil {
		return []string{}
	}
	return []string{truncateToWidth(" "+hint+"  "+preview, width, "…")}
}

// editorInput describes one key the editor received, as the original's wrapper decides it with Pi's keybindings (the caller,
// which has them, fills it in): Interrupt is app.interrupt, Submit is tui.input.submit without tui.input.newLine and
// FollowUpSubmit is app.message.followUp.
type editorInput struct {
	Interrupt, Submit, FollowUpSubmit bool
	AutocompleteShowing               bool
	BashModeActive                    bool
}

// editorVerdict is what the editor does with the key after interceptEditorInput.
type editorVerdict int

const (
	editorContinue    editorVerdict = iota // the key is not the queue's: the editor goes on with it
	editorHandled                          // the queue consumed the key
	editorPassThrough                      // hand the key to the editor's own handleInput and stop (a slash command during compaction)
)

// interceptEditorInput is the first part of the original's editor wrapper (index.ts 3335-3384): Esc takes a delayed prompt
// back, `/compact` and `/compact <prompt>` start a compaction the queue knows of, and a prompt submitted during a compaction is
// queued for after it. It assumes the lock is held.
func (q *queueState) interceptEditorInput(ed queueEditor, in editorInput) editorVerdict {
	if q.pendingSend != nil && !in.BashModeActive && in.Interrupt && !in.AutocompleteShowing {
		q.restorePendingSend()
		return editorHandled
	}
	if !q.p.compacting && !in.BashModeActive && in.Submit {
		text := jsTrim(ed.GetExpandedText())
		var queued string
		hasQueued := false
		if q.p.config.CompactPromptMode == "queue" {
			queued, hasQueued = parseCompactQueuedPrompt(text)
		}
		if text == "/compact" || hasQueued {
			ed.AddToHistory(text)
			ed.SetText("")
			if hasQueued {
				if _, err := q.capturePostCompactPrompt(queued); err != nil {
					q.reportError(err)
				}
			}
			q.p.compacting = true
			q.cancelPostCompactionDelivery()
			generation := q.sessionGeneration
			q.compactionGeneration++
			operation := q.compactionGeneration
			q.requestQueueRender()
			q.host.Compact(func(err error) {
				q.mu.Lock()
				defer q.mu.Unlock()
				// Preparation can fail before session_before_compact increments the operation.
				if generation != q.sessionGeneration || q.compactionGeneration > operation+1 || !q.p.compacting {
					return
				}
				if ferr := q.finishFailedCompaction(err.Error()); ferr != nil {
					q.reportError(ferr)
				}
				q.p.h.Notify(err.Error(), "error")
			})
			return editorHandled
		}
	}
	if q.p.compacting && !in.BashModeActive && (in.Submit || in.FollowUpSubmit) {
		text := jsTrim(ed.GetExpandedText())
		if text == "" {
			q.sendPendingNow()
			return editorHandled
		}
		if strings.HasPrefix(text, "/") {
			return editorPassThrough
		}
		ed.AddToHistory(text)
		ed.SetText("")
		if _, err := q.capturePostCompactPrompt(text); err != nil {
			q.reportError(err)
		}
		return editorHandled
	}
	return editorContinue
}

// editorKey is the queue's part of the editor's handleInput for one key: the interception of interceptEditorInput, and the
// delay of a submitted prompt under powerline.sendDelayMs. It assumes the lock is held. data is compared with Pi's default key
// bindings.
func (q *queueState) editorKey(ed queueKeyEditor, data string, bashModeActive bool) editorVerdict {
	in := editorInput{
		Interrupt:      piKeyMatches(data, "app.interrupt"),
		Submit:         piKeyMatches(data, "tui.input.submit") && !piKeyMatches(data, "tui.input.newLine"),
		FollowUpSubmit: piKeyMatches(data, "app.message.followUp"),
		BashModeActive: bashModeActive,
	}
	if !in.Interrupt && !in.Submit && !in.FollowUpSubmit {
		return editorContinue
	}
	if in.Interrupt && q.pendingSend != nil {
		in.AutocompleteShowing = ed.ShowingAutocomplete()
	}
	if verdict := q.interceptEditorInput(ed, in); verdict != editorContinue {
		return verdict
	}
	if !in.Submit || bashModeActive || q.p.config.SendDelayMs <= 0 {
		return editorContinue
	}
	// Pi's editor submits the expanded text and, before it calls onSubmit, adds it to its history and clears itself.
	text := jsTrim(ed.GetExpandedText())
	if text == "" {
		q.sendPendingNow()
		return editorHandled
	}
	if strings.HasPrefix(text, "/") || strings.HasPrefix(text, "!") {
		return editorContinue
	}
	ed.AddToHistory(text)
	ed.SetText("")
	q.submitWithDelay(text, ed.SubmitText)
	return editorHandled
}
