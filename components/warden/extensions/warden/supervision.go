package warden

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Supervision: the subagent report triage (extension.ts checkSubagentReports and the agent_settled hook), the call
// waste hooks, the stuck-loop diff's memory, and the registries through which the other lanes' pieces of the
// `message_start`, `message_update`, `message_end`, `input` and `agent_settled` events slot in, in upstream's order.
//
// Each of those events has ONE handler registered with the host (below), and the handler runs the registered pieces
// in registration order. A lane adds its piece with one line in newWarden, in the position upstream's handler ran it:
//
//	message_start   security (runaway.reset), then conscience (delivery observed, queued-prompt admission)
//	message_update  security (runaway feed and check)
//	message_end     learning (settle watched steers on an assistant message), then context (dedupe; may return a
//	                replacement message; the first piece that returns a value wins)
//	input           blocks in registration order: init/rules tune (rules), audit (audit), index (conscience); then
//	                the pass-through pieces: conscience generation bump (conscience); settle watched steers (here)
//	agent_settled   subagent triage (here), then conscience finalisation (conscience)

// eventPiece is one lane's piece of an event handler.
type eventPiece func(ctx sdk.Context, data map[string]any) (any, error)

// inputBlock swallows a user message while busy reports true: the user is told why and the message is handled.
type inputBlock struct {
	busy    func() bool
	message string
}

const (
	// subagentPendingLimit bounds the reports waiting for the next scan: upstream reads the newest 8 unseen ones.
	subagentPendingLimit = 8
	// stuckDiffOutputs bounds the failed outputs kept for the stuck-loop diff.
	stuckDiffOutputs = 64
)

// fullOutput is the last failed output of one call, and where its full copy was saved.
type fullOutput struct {
	text string
	path string
}

// supervision is the state of this module and the piece registries.
type supervision struct {
	waste           *WasteTracker
	wasteTipOffered bool
	subagentSeen    map[string]bool
	subagentPending []SubagentEntry
	subagentSeq     int
	wake            *WakePolicy
	fullOutputs     map[string]fullOutput

	messageStart  []eventPiece
	messageUpdate []eventPiece
	messageEnd    []eventPiece
	inputBlocks   []inputBlock
	inputPass     []func(ctx sdk.Context)
	settled       []func(ctx sdk.Context)
}

func newSupervision() *supervision {
	return &supervision{waste: NewWasteTracker(), subagentSeen: map[string]bool{}, wake: NewWakePolicy(0), fullOutputs: map[string]fullOutput{}}
}

// reset is session_start (extension.ts 1028-1048).
func (s *supervision) reset() {
	s.waste.Reset()
	s.wasteTipOffered = false
	s.subagentSeen = map[string]bool{}
	s.subagentPending = nil
	s.wake.Reset()
	s.fullOutputs = map[string]fullOutput{}
}

func (s *supervision) onMessageStart(p eventPiece)  { s.messageStart = append(s.messageStart, p) }
func (s *supervision) onMessageUpdate(p eventPiece) { s.messageUpdate = append(s.messageUpdate, p) }
func (s *supervision) onMessageEnd(p eventPiece)    { s.messageEnd = append(s.messageEnd, p) }
func (s *supervision) onSettled(p func(ctx sdk.Context)) {
	s.settled = append(s.settled, p)
}
func (s *supervision) onInputPass(p func(ctx sdk.Context)) { s.inputPass = append(s.inputPass, p) }

// blockInput registers a busy flag: while it is true, a user message is answered with message and handled.
func (s *supervision) blockInput(busy func() bool, message string) {
	s.inputBlocks = append(s.inputBlocks, inputBlock{busy: busy, message: message})
}

func messageOf(data map[string]any) map[string]any {
	m, _ := data["message"].(map[string]any)
	return m
}

// runPieces runs each piece in order and returns the first value one returns.
func runPieces(pieces []eventPiece, ctx sdk.Context, data map[string]any) (any, error) {
	for _, p := range pieces {
		if v, err := p(ctx, data); v != nil || err != nil {
			return v, err
		}
	}
	return nil, nil
}

// registerSupervisionEvents subscribes to the events that have pieces. A host sends a subscribed event to the
// extension and waits for its answer, and message_update arrives once per streamed token, so an event nobody has a
// piece for is not subscribed to. message_end always is: the subagent reports are collected there.
func (w *warden) registerSupervisionEvents(e *sdk.Extension) {
	if len(w.sup.messageStart) > 0 {
		e.OnEvent(sdk.EventMessageStart, w.onMessageStart)
	}
	if len(w.sup.messageUpdate) > 0 {
		e.OnEvent(sdk.EventMessageUpdate, w.onMessageUpdate)
	}
	e.OnEvent(sdk.EventMessageEnd, w.onMessageEnd)
	e.OnEvent(sdk.EventInput, w.onInput)
	e.OnEvent(sdk.EventAgentSettled, w.onAgentSettled)
}

// onMessageStart is the `message_start` handler: Pi does not forward the stream's own "start" event, so lanes
// reset their per-message state here.
func (w *warden) onMessageStart(ctx sdk.Context, data map[string]any) (any, error) {
	return runPieces(w.sup.messageStart, ctx, data)
}

// onMessageUpdate is the `message_update` handler: per token, so a piece must stay cheap.
func (w *warden) onMessageUpdate(ctx sdk.Context, data map[string]any) (any, error) {
	return runPieces(w.sup.messageUpdate, ctx, data)
}

// onMessageEnd is the `message_end` handler. The first piece is the subagent report collection: the session branch
// the host hands an extension carries no custom type, so the reports are read from the custom messages as they
// end, which is the same set the branch holds.
func (w *warden) onMessageEnd(ctx sdk.Context, data map[string]any) (any, error) {
	w.collectSubagentReport(messageOf(data))
	return runPieces(w.sup.messageEnd, ctx, data)
}

// collectSubagentReport keeps a subagent notify message for the next scan (subagent.ts newReports reads
// `custom_message` entries of the notify types whose content is a string).
func (w *warden) collectSubagentReport(msg map[string]any) {
	if msg == nil {
		return
	}
	if role, _ := msg["role"].(string); role != "custom" {
		return
	}
	customType, _ := msg["customType"].(string)
	if !IsNotifyType(customType) {
		return
	}
	content, _ := msg["content"].(string)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sup.subagentSeq++
	id, _ := msg["id"].(string)
	if id == "" {
		id = customType + "#" + strconv.Itoa(w.sup.subagentSeq)
	}
	w.sup.subagentPending = append(w.sup.subagentPending, SubagentEntry{ID: id, Type: "custom_message", CustomType: customType, Content: content})
	if over := len(w.sup.subagentPending) - subagentPendingLimit; over > 0 {
		w.sup.subagentPending = append([]SubagentEntry(nil), w.sup.subagentPending[over:]...)
	}
}

// onInput is the `input` handler (extension.ts 2259): a message typed while a generation runs is swallowed with a
// notice; any other input invalidates what was in flight and settles the steers watched for the user's reply.
func (w *warden) onInput(ctx sdk.Context, data map[string]any) (any, error) {
	// D16: a message the extension itself sends (`source` "extension": /warden audit, init and index send their prompt
	// with sendUserMessage) is never answered by a busy lane. The original's handler (extension.ts 2258-2268) has no such
	// check and swallows the prompt of the very command that is running (port/PORT.md D16).
	fromExtension := data["source"] == "extension"
	for _, b := range w.sup.inputBlocks {
		if !fromExtension && b.busy() {
			ctx.Notify(b.message, "warning")
			return map[string]any{"action": "handled"}, nil
		}
	}
	for _, p := range w.sup.inputPass {
		p(ctx)
	}
	w.settleSteers(w.config(ctx), w.adaptor.user())
	return nil, nil
}

// onAgentSettled is the `agent_settled` handler: the agent has caught up and the host will not continue on its
// own, the one moment a wake costs the user nothing.
func (w *warden) onAgentSettled(ctx sdk.Context, _ map[string]any) (any, error) {
	for _, p := range w.sup.settled {
		p(ctx)
	}
	return nil, nil
}

// checkSubagentReports reads the subagent reports that arrived since the last scan and decides whether one
// deserves a wake: a report with no failure, blocker, or question costs no request and wakes nobody. Silent
// reports still leave one trace line.
func (w *warden) checkSubagentReports(ctx sdk.Context) {
	cfg := w.config(ctx)
	if !cfg.Enabled || !cfg.Subagent.Enabled {
		return
	}
	w.mu.Lock()
	reports := NewReports(w.sup.subagentPending, w.sup.subagentSeen, subagentPendingLimit)
	if len(reports) == 0 {
		w.mu.Unlock()
		return
	}
	for _, r := range reports {
		w.sup.subagentSeen[r.ID] = true
	}
	w.sup.subagentPending = nil
	w.stats.SubagentReports += len(reports)
	w.sup.wake.CooldownMs = int64(cfg.Subagent.CooldownMs)
	w.mu.Unlock()
	var judge Judge
	if cfg.Subagent.Wake {
		judge = w.judgeFor(ctx, cfg)
	}
	task, hasTask := w.latestUserPrompt(ctx)
	var batch []string
	for _, r := range reports {
		verdict := TriageReport(context.Background(), r, TriageOptions{Config: cfg.Subagent, Judge: judge, Timeout: w.timeout(cfg), Task: task, HasTask: hasTask})
		label := ReportLabel(r)
		wake, status := "silent", "appended silently"
		switch {
		case verdict.Source == "error":
			status = "judgment failed; stayed quiet"
		case verdict.Wake:
			wake, status = "wake", "woke the agent"
		}
		told := "nothing; the report is in context and warden stayed quiet"
		if verdict.Wake {
			told = "woken with a pointer to this report"
		}
		w.record(ctx, cfg, GuardSubagent, RenderTemplate(cfg.Widget.Subagent, Tokens{"agent": label, "kind": r.CustomType, "wake": wake, "status": status}), []string{
			"report: " + r.CustomType + ", " + strconv.Itoa(utf16Len(r.Text)) + " chars, " + verdict.Source + " (" + verdict.Reason + ")",
			"agent told: " + told,
		}, nil)
		if !verdict.Wake {
			continue
		}
		w.mu.Lock()
		queued := w.sup.wake.Offer(label, time.Now().UnixMilli())
		w.mu.Unlock()
		batch = append(batch, queued...)
	}
	if len(batch) > 0 {
		w.bump(func(s *stats) { s.SubagentWoken += len(batch) })
		w.steer(cfg, []SteerGuard{SteerGuardSubagent}, ctx, FormatWake(batch), SteerOptions{DeliverAs: "followUp", TriggerTurn: true})
	}
}

// wasteTip appends the call-waste tip to the system prompt the host builds, on every run, and says so once per
// session (extension.ts 1097-1112). `appendSystemPrompt` is the host's own append slot, so the tip lands after the
// prompt instead of replacing it.
func (w *warden) wasteTip(ctx sdk.Context, cfg Config, data map[string]any) {
	opts, _ := data["systemPromptOptions"].(map[string]any)
	if !cfg.Enabled || !cfg.Waste.Enabled || !cfg.Waste.Tip || opts == nil {
		return
	}
	current, _ := opts["appendSystemPrompt"].(string)
	opts["appendSystemPrompt"] = WithWasteTip(current, WasteTip)
	w.mu.Lock()
	first := !w.sup.wasteTipOffered
	w.sup.wasteTipOffered = true
	w.mu.Unlock()
	if first {
		w.record(ctx, cfg, GuardWaste, "warden · waste · session tip · instructions_supplied", []string{
			"trigger: before_agent_start",
			"delivery: appended to the system prompt (appendSystemPrompt)",
			"tip: " + WasteTip,
		}, nil)
	}
}

// wasteNote records a finished call and returns the note to attach to its result, with its trace line already
// written (extension.ts 1828-1831 and withWaste 2088-2107). The note never reaches a hold, the judge, or the steer
// budget.
func (w *warden) wasteNote(ctx sdk.Context, cfg Config, tool string, input map[string]any, failed bool, text string) *WasteNudge {
	w.mu.Lock()
	nudge := w.sup.waste.Record(WasteCall{Tool: tool, Input: input, Cwd: ctx.Cwd(), Failed: failed, Result: text}, cfg.Waste)
	w.mu.Unlock()
	return nudge
}

// recordWasteNote is the trace line for a note that rides a result.
func (w *warden) recordWasteNote(ctx sdk.Context, cfg Config, tool string, input map[string]any, n *WasteNudge) {
	trigger := ""
	if cmd, ok := input["command"].(string); tool == "bash" && ok {
		trigger = " " + utf16Slice(collapseSpace(Redact(cmd)), 0, 200)
	}
	w.record(ctx, cfg, GuardWaste, "warden · waste · "+string(n.Detector)+" · "+n.Subject+" · agent told", []string{
		"trigger: " + tool + trigger,
		"detector: " + string(n.Detector),
		"subject: " + Redact(n.Subject),
		"nudge: " + n.Text,
		"rate limit: at most one " + string(n.Detector) + " note per " + strconv.Itoa(cfg.Waste.Every) + " calls; no steer, no request",
	}, nil)
}

// withWasteNote appends the note to the last text part of the result content; the second result is false when the
// content has no text part (the note is then dropped, as upstream).
func withWasteNote(content []any, note string) ([]any, bool) {
	last := -1
	for i, p := range content {
		if m, ok := p.(map[string]any); ok && m["type"] == "text" {
			last = i
		}
	}
	if last < 0 {
		return content, false
	}
	out := append([]any(nil), content...)
	m := map[string]any{}
	for k, v := range out[last].(map[string]any) {
		m[k] = v
	}
	text, _ := m["text"].(string)
	m["text"] = text + "\n\n" + note
	out[last] = m
	return out, true
}

// saveFullOutput stores an exact copy of an output where the agent can read it back; the stuck-loop diff points at
// it. Never trust a path advertised in untrusted tool text: warden stores its own copy before replacing a result
// (output.ts saveOutput). The context lane's stored-output ledger may replace this with its own saver.
var saveFullOutput = func(text string) (string, error) {
	dir, err := os.MkdirTemp("", "pi-warden-output-")
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, "output.txt")
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		_, err = f.WriteString(text)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return p, nil
}

func joinedText(content []any) string {
	var parts []string
	for _, p := range content {
		if m, ok := p.(map[string]any); ok && m["type"] == "text" {
			s, _ := m["text"].(string)
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n")
}

// stuckDiffResult is the stuck-loop diff (extension.ts 2025-2078): on a stuck verdict for a failed result, and a
// previous failed output of the same call, the result is replaced by a short diff note (computed against the
// current content, so the diff never grows the result). It returns the content to deliver and whether it changed.
func (w *warden) stuckDiffResult(ctx sdk.Context, cfg Config, verdict *StuckVerdict, failed bool, text string, content []any) ([]any, bool) {
	if !failed || text == "" {
		// Only a failed output is stored or diffed (extension.ts 2025-2078): a passing result costs nothing here.
		return content, false
	}
	var key string
	w.mu.Lock()
	if w.window != nil && len(w.window.Attempts) > 0 {
		key = w.window.Attempts[len(w.window.Attempts)-1].Key
	}
	prev, hasPrev := w.sup.fullOutputs[key]
	w.mu.Unlock()
	remember := func(o fullOutput) {
		w.mu.Lock()
		if len(w.sup.fullOutputs) >= stuckDiffOutputs {
			w.sup.fullOutputs = map[string]fullOutput{}
		}
		w.sup.fullOutputs[key] = o
		w.mu.Unlock()
	}
	replace := func(note string) ([]any, bool) {
		if len(note) < len(joinedText(content)) {
			out := []any{map[string]any{"type": "text", "text": note}}
			for _, p := range content {
				if m, ok := p.(map[string]any); !ok || m["type"] != "text" {
					out = append(out, p)
				}
			}
			return out, true
		}
		return content, false
	}
	switch {
	case verdict != nil && verdict.Stuck && failed && !verdict.SuccessRepeat && !verdict.Churn && text != "":
		d := DiffOptions{DiffLimit: cfg.Stuck.DiffLimit, TailLimit: cfg.Stuck.TailLimit}
		switch {
		case key != "" && hasPrev && prev.text != text:
			saved, err := saveFullOutput(text)
			if err != nil {
				w.noteError(ctx, "Could not store full output for stuck diff; keeping it unchanged.", "")
				remember(fullOutput{text: text})
				return content, false
			}
			d.FullPath = saved
			out, changed := replace(StuckDiff(prev.text, text, d))
			remember(fullOutput{text: text, path: saved})
			return out, changed
		case key != "" && hasPrev && prev.text == text && prev.path != "":
			d.FullPath = prev.path
			return replace(StuckDiff(text, text, d))
		case key != "":
			remember(fullOutput{text: text})
		}
	case failed && text != "" && key != "":
		remember(fullOutput{text: text})
	}
	return content, false
}
