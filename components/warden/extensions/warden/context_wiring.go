package warden

import (
	"fmt"
	"os"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The context saver's state and event hooks (extension.ts: the ledger and its neighbours at 458-471, 745-770, 1035-1045,
// 1395, 1445-1458, 2205-2255, 2560-2565). The tool_result pipeline itself is tool_output.go.

// contextState is the session state of the saver.
type contextState struct {
	mu sync.Mutex
	// ledger counts the compressions and holds the stored full outputs; the compaction snapshot and warden_recall read it.
	ledger  *ContextLedger
	learner *CompressionLearner
	// seen is the text already in context on the current branch, for repeated-run detection; seenMu serialises its use.
	seenMu sync.Mutex
	seen   *SeenText
	// compactions are the relevance compactions of this session, for /warden status.
	compactions CompactionRecord
	// saving is the trace entry of this turn's latest saving; its ledger line was written before the turn counted,
	// so turn_end adds one that has.
	saving *TraceEntry
	// secretsSeen are the credential fingerprints the agent has already been warned about this session.
	secretsSeen map[string]bool
}

func newContextState() contextState {
	return contextState{ledger: NewContextLedger(), learner: &CompressionLearner{}, seen: NewSeenText(), secretsSeen: map[string]bool{}}
}

func (s *contextState) setSaving(e *TraceEntry) {
	s.mu.Lock()
	s.saving = e
	s.mu.Unlock()
}

func (s *contextState) takeSaving() *TraceEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.saving
	s.saving = nil
	return e
}

// contextSessionStart starts the saver's session over: the stored outputs of the previous session are removed.
func (w *warden) contextSessionStart() {
	st := &w.context
	// Clean up the stored outputs of the previous session (upstream removes each stored path).
	for _, path := range st.ledger.StoredPaths() {
		_ = os.RemoveAll(path)
	}
	st.ledger.Reset()
	st.mu.Lock()
	st.compactions = CompactionRecord{}
	st.saving = nil
	st.secretsSeen = map[string]bool{}
	st.mu.Unlock()
	st.seenMu.Lock()
	st.seen.Clear()
	st.seenMu.Unlock()
	st.learner.Reset()
}

// contextToolCall notes a read of a stored full output: the excerpt was not enough, which is the number that tunes
// context.confidence. A whole-file read also undoes the saving, so the kind of access is kept apart.
func (w *warden) contextToolCall(ctx sdk.Context, cfg Config, data map[string]any) {
	st := &w.context
	tool, _ := data["toolName"].(string)
	input, _ := data["input"].(map[string]any)
	if input == nil {
		input = map[string]any{}
	}
	serialized := jsStringify(input)
	if _, ok := st.ledger.StoredPathIn(serialized); !ok {
		return
	}
	path := ""
	for _, p := range st.ledger.StoredPaths() {
		if mentions(serialized, p) {
			path = p
			break
		}
	}
	kind := RecallKind(classifyRecall(tool, input, path))
	recalled, ok := st.ledger.NoteAccess(serialized, kind)
	if !ok {
		return
	}
	st.learner.NoteRecall(tool)
	access := "scoped access"
	if kind == RecallFull {
		access = "whole-file read"
	}
	w.record(ctx, cfg, GuardContext, RenderTemplate(cfg.Widget.Context, tokenSet("tool", tool, "retention", "full output recalled ("+string(kind)+")")),
		[]string{fmt.Sprintf("the agent went back to %s (%s)", recalled, access), FormatLedger(st.ledger.Snapshot())}, nil)
}

// contextTurnEnd: every turn that runs after a compression is a turn that did not carry the removed text.
func (w *warden) contextTurnEnd() {
	st := &w.context
	st.ledger.TurnEnd()
	if e := st.takeSaving(); e != nil {
		w.trace.Amend(e, "at turn end: "+FormatLedger(st.ledger.Snapshot()))
	}
}

// contextStatus is the saver's lines of /warden status.
func (w *warden) contextStatus(cfg Config) string {
	st := &w.context
	snap := st.ledger.Snapshot()
	st.mu.Lock()
	record := st.compactions
	st.mu.Unlock()
	lines := []string{FormatLedger(snap), FormatCompaction(cfg.Compaction.Enabled, record)}
	if cfg.Context.Filter.Enabled || snap.Filter.Requests > 0 || len(snap.Filter.Fallbacks) > 0 {
		lines = append(lines, FormatFilterLedger(snap))
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString("  " + l + "\n")
	}
	return b.String()
}

// ── Repeated runs ─────────────────────────────────────────────────────────

// seenEntryOf reads a session entry the host sends as a JSON object.
func seenEntryOf(m map[string]any) SeenEntry {
	e := SeenEntry{Content: m["content"]}
	e.ID, _ = m["id"].(string)
	e.Type, _ = m["type"].(string)
	e.CustomType, _ = m["customType"].(string)
	if msg, ok := m["message"].(map[string]any); ok {
		sm := &SeenMessage{Content: msg["content"]}
		sm.Role, _ = msg["role"].(string)
		sm.ToolName, _ = msg["toolName"].(string)
		sm.CustomType, _ = msg["customType"].(string)
		e.Message = sm
	}
	return e
}

// syncSeen matches the index to the text in context on the current branch. Compaction-aware when the host has it:
// text a compaction summarized is no longer in context, so nothing may point to it.
func (w *warden) syncSeen(ctx sdk.Context) {
	var items []SeenItem
	if entries, err := ctx.SessionManager().BuildContextEntries(); err == nil {
		for _, m := range entries {
			if item, ok := SeenItemOf(seenEntryOf(m)); ok {
				items = append(items, item)
			}
		}
	} else {
		for _, e := range w.branchOf(ctx) {
			entry := SeenEntry{ID: e.ID, Type: e.Type}
			if e.Type == "message" {
				entry.Message = &SeenMessage{Role: e.Role, ToolName: e.ToolName, Content: e.Content}
			}
			if item, ok := SeenItemOf(entry); ok {
				items = append(items, item)
			}
		}
	}
	st := &w.context
	st.seenMu.Lock()
	st.seen.Sync(items)
	st.seenMu.Unlock()
}

// dedupeText is `text` with each run that repeats context replaced by a pointer line; false when nothing repeats or
// the copy cannot be stored.
func (w *warden) dedupeText(ctx sdk.Context, cfg Config, text, source string) (string, bool) {
	st := &w.context
	st.seenMu.Lock()
	runs := st.seen.Find(text)
	st.seenMu.Unlock()
	if len(runs) == 0 {
		return "", false
	}
	path, err := SaveOutput(text)
	if err != nil {
		w.noteError(ctx, "Could not store full text; keeping the repeated run.", "")
		return "", false
	}
	collapsed := CollapseRuns(text, runs, path)
	bytesSaved := len(text) - len(collapsed)
	st.ledger.Repeat(path, bytesSaved, source, len(text))
	details := make([]string, 0, len(runs)+2)
	for _, run := range runs {
		details = append(details, fmt.Sprintf("%d lines (%d characters) repeat %s", run.Lines, run.Chars, run.Where))
	}
	details = append(details, fmt.Sprintf("saved %d bytes; full text: %s", bytesSaved, path), FormatLedger(st.ledger.Snapshot()))
	st.setSaving(w.record(ctx, cfg, GuardContext, RenderTemplate(cfg.Widget.Context, tokenSet("tool", source, "retention", "repeat", "bytesSaved", fmt.Sprint(bytesSaved))), details, nil))
	return collapsed, true
}

// dedupeContent cuts repeated runs from the text parts; images and other parts keep their place and bytes.
func (w *warden) dedupeContent(ctx sdk.Context, cfg Config, content []outputPart, source string) ([]outputPart, bool) {
	var changed []outputPart
	for i, part := range content {
		text, ok := part["text"].(string)
		if !isTextPart(part) || !ok {
			continue
		}
		replaced, ok := w.dedupeText(ctx, cfg, text, source)
		if !ok {
			continue
		}
		if changed == nil {
			changed = append([]outputPart(nil), content...)
		}
		changed[i] = withText(part, replaced)
	}
	return changed, changed != nil
}

// contextMessageEnd is opt-in (context.dedupeMessages): a relayed report that pastes earlier turns again keeps only its
// new part. The host persists the replaced message.
func (w *warden) contextMessageEnd(ctx sdk.Context, data map[string]any) (any, error) {
	cfg := w.config(ctx)
	if !cfg.Enabled || !cfg.Context.Enabled || !cfg.Context.DedupeRuns || !cfg.Context.DedupeMessages {
		return nil, nil
	}
	message, _ := data["message"].(map[string]any)
	role, _ := message["role"].(string)
	if role != "user" && role != "custom" {
		return nil, nil
	}
	w.syncSeen(ctx)
	source := "user message"
	if role == "custom" {
		customType, _ := message["customType"].(string)
		source = customType + " message"
	}
	replace := func(content any) (map[string]any, bool) {
		out := make(map[string]any, len(message))
		for k, v := range message {
			out[k] = v
		}
		out["content"] = content
		return out, true
	}
	if s, ok := message["content"].(string); ok {
		text, changed := w.dedupeText(ctx, cfg, s, source)
		if !changed {
			return nil, nil
		}
		patched, _ := replace(text)
		return map[string]any{"message": patched}, nil
	}
	deduped, changed := w.dedupeContent(ctx, cfg, outputPartsOf(message["content"]), source)
	if !changed {
		return nil, nil
	}
	parts := make([]any, len(deduped))
	for i, p := range deduped {
		parts[i] = p
	}
	patched, _ := replace(parts)
	return map[string]any{"message": patched}, nil
}
