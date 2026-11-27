package snapcompact

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/surface"
)

// upstream: packages/coding-agent/src/session/session-maintenance.ts:738-740,2569,3210,3357-3600,3925-4000
// upstream: packages/coding-agent/src/session/context-settings.ts:76-87,106-206,282-286
// upstream: packages/agent/src/compaction/compaction.ts:403-437

const (
	defaultKeepRecentTokens       = 20000
	defaultMidTurnFallbackPercent = 90
)

// midTurnState tracks per-session mid-turn compaction dead-end guard and
// overflow context-promotion state (guarded to at most 1 promotion per turn).
type midTurnState struct {
	// lastMidTurnFirstKept avoids re-emitting identical mid-turn compactions on
	// consecutive tool boundaries when the cut point has not advanced.
	lastMidTurnFirstKept string

	// overflowPending is armed by EventSessionCompactFailed or an overflow
	// error observed on EventTurnEnd, and consumed on EventAgentBeforeSettle.
	overflowPending        bool
	overflowReason         string
	overflowMessageEntryID string

	// promotedThisTurn enforces at most 1 context promotion per user turn to
	// prevent infinite promotion/retry loops. Reset on the next completed turn,
	// session start, or session tree navigation.
	promotedThisTurn bool
}

// ResolveMidTurnThreshold computes the token threshold that triggers mid-turn
// compaction for a model window and configured compaction settings.
//
// Precedence mirrors omp `resolveThresholdTokens` (compaction.ts:407-437):
//  1. `thresholdTokens > 0` takes priority (capped below contextWindow; if
//     >= contextWindow, falls back to the 90% mid-turn budget).
//  2. `thresholdPercent > 0` computes `floor(contextWindow * clamp(pct,1,99) / 100)`.
//  3. When both are <= 0 (unset / -1), returns 90% of contextWindow so mid-turn
//     compaction acts only as a high-water safety valve during active tool loops
//     without duplicating PiG's built-in reserve-based post-run compaction.
func ResolveMidTurnThreshold(contextWindow, thresholdTokens, thresholdPercent int) int {
	if contextWindow <= 0 {
		return 0
	}
	fallback := max(1, (contextWindow*defaultMidTurnFallbackPercent)/100)
	if fallback >= contextWindow {
		fallback = max(1, contextWindow-1)
	}
	if thresholdTokens > 0 {
		if thresholdTokens >= contextWindow {
			return fallback
		}
		return thresholdTokens
	}
	if thresholdPercent > 0 {
		clamped := min(99, max(1, thresholdPercent))
		return max(1, (contextWindow*clamped)/100)
	}
	return fallback
}

func resolveBoolSetting(ctx sdk.Context, key string, def bool) bool {
	if Setting == nil {
		return def
	}
	raw := Setting(ctx, key)
	if raw == nil {
		return def
	}
	if b, ok := raw.(bool); ok {
		return b
	}
	return def
}

func resolveIntSetting(ctx sdk.Context, key string, def int) int {
	if Setting == nil {
		return def
	}
	raw := Setting(ctx, key)
	if raw == nil {
		return def
	}
	switch v := raw.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n)
		}
	}
	return def
}

func resolveStringSetting(ctx sdk.Context, key string) string {
	if Setting == nil {
		return ""
	}
	if s, ok := Setting(ctx, key).(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

// resolvePromotionTarget resolves the target model spec for context promotion:
//  1. `contextPromotion.target` setting
//  2. active model's `contextPromotionTarget` metadata from ModelRegistry
//  3. `modelRoles.slow` setting (direct key or nested `modelRoles` map)
func resolvePromotionTarget(ctx sdk.Context, current ModelTarget) string {
	if target := resolveStringSetting(ctx, "contextPromotion.target"); target != "" {
		return target
	}
	if current.Provider != "" && current.ID != "" {
		if reg, err := ctx.ModelRegistry().Find(current.Provider, current.ID); err == nil && reg != nil {
			if s, ok := reg["contextPromotionTarget"].(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	if slow := resolveStringSetting(ctx, "modelRoles.slow"); slow != "" {
		return slow
	}
	if Setting != nil {
		if roles, ok := Setting(ctx, "modelRoles").(map[string]any); ok {
			if slow, ok := roles["slow"].(string); ok && strings.TrimSpace(slow) != "" {
				return strings.TrimSpace(slow)
			}
		}
	}
	return ""
}

func isSameModelSpec(current ModelTarget, targetSpec string) bool {
	spec := strings.TrimSpace(targetSpec)
	if spec == "" || current.ID == "" {
		return false
	}
	if strings.EqualFold(spec, current.ID) {
		return true
	}
	if current.Provider != "" && strings.EqualFold(spec, current.Provider+"/"+current.ID) {
		return true
	}
	return false
}

// isContextOverflowText matches common provider error messages indicating the
// request exceeded the model's context window.
func isContextOverflowText(msg string) bool {
	s := strings.ToLower(strings.TrimSpace(msg))
	if s == "" {
		return false
	}
	patterns := []string{
		"context length",
		"context_length_exceeded",
		"maximum context",
		"max context",
		"context window",
		"too many tokens",
		"prompt is too long",
		"input is too long",
		"exceeds the context",
		"token limit",
		"context overflow",
		"request too large",
		"exceeds maximum",
	}
	for _, pat := range patterns {
		if strings.Contains(s, pat) {
			return true
		}
	}
	return false
}

func isTurnAborted(data map[string]any) bool {
	if outcome, _ := data["outcome"].(string); outcome == "aborted" {
		return true
	}
	if msg, ok := data["message"].(map[string]any); ok {
		if strField(msg, "stopReason") == "aborted" {
			return true
		}
	}
	return false
}

func isTurnError(data map[string]any) (bool, string) {
	errMsg := ""
	if msg, ok := data["message"].(map[string]any); ok {
		errMsg = strField(msg, "errorMessage")
		if strField(msg, "stopReason") == "error" {
			return true, errMsg
		}
	}
	if outcome, _ := data["outcome"].(string); outcome == "error" {
		return true, errMsg
	}
	return false, errMsg
}

// onTurnEnd handles three boundary responsibilities in order:
//  1. Emits any durable `context_edit` drafts queued by `/shake`.
//  2. Records overflow error metadata when a turn ends with a context-overflow
//     error so `EventAgentBeforeSettle` can perform context promotion.
//  3. Triggers mid-turn snapcompact compaction during active tool loops
//     (`len(toolResults) > 0`) when context usage meets or exceeds the resolved
//     threshold (`thresholdTokens` > `thresholdPercent` > 90% fallback).
//
// Fast path: on small/unshaken sessions without tool calls or below threshold,
// returns `(nil, nil)` with zero side effects.
func (m *manager) onTurnEnd(ctx sdk.Context, data map[string]any) (any, error) {
	if isTurnAborted(data) {
		return nil, nil
	}

	// Check for errored turn first (may feed overflow context promotion on agent_before_settle).
	if errored, errMsg := isTurnError(data); errored {
		sid := sessionIDFor(ctx)
		if sid != "" {
			msgEntryID := strField(data, "messageEntryId")
			m.mu.Lock()
			st := m.stateLocked(sid)
			if isContextOverflowText(errMsg) {
				st.midTurn.overflowPending = true
				st.midTurn.overflowReason = "overflow"
				if msgEntryID != "" {
					st.midTurn.overflowMessageEntryID = msgEntryID
				}
			} else if st.midTurn.overflowPending && msgEntryID != "" && st.midTurn.overflowMessageEntryID == "" {
				st.midTurn.overflowMessageEntryID = msgEntryID
			}
			m.mu.Unlock()
		}
		return nil, nil
	}

	toolResults, _ := data["toolResults"].([]any)
	hasToolResults := len(toolResults) > 0

	// Fast zero-RPC path when no session has pending shake edits or overflow/promotion
	// state AND the turn has no tool results.
	m.mu.Lock()
	anySessionWork := false
	for _, st := range m.sessions {
		if len(st.shake.pendingOrder) > 0 || st.midTurn.overflowPending || st.midTurn.promotedThisTurn {
			anySessionWork = true
			break
		}
	}
	m.mu.Unlock()
	if !anySessionWork && !hasToolResults {
		return nil, nil
	}

	sid := sessionIDFor(ctx)

	// Normal (non-error) turn completed: reset per-turn promotion guard and drain pending shake edits.
	var shakeDrafts []any
	if sid != "" {
		m.mu.Lock()
		if st, ok := m.sessions[sid]; ok {
			st.midTurn.overflowPending = false
			st.midTurn.overflowReason = ""
			st.midTurn.overflowMessageEntryID = ""
			st.midTurn.promotedThisTurn = false
			shakeDrafts = st.shake.takePendingEdits()
			st.shake.elide = false
			st.shake.images = false
			st.shake.thinking = false
		}
		m.mu.Unlock()
	}

	// Mid-turn compaction only runs on active tool-loop turns (len(toolResults) > 0).
	var compactionDraft map[string]any
	if hasToolResults &&
		resolveBoolSetting(ctx, "compaction.enabled", true) &&
		resolveBoolSetting(ctx, "compaction.midTurnEnabled", true) {
		compactionDraft = m.maybeBuildMidTurnCompaction(ctx, sid, data)
	}
	if len(shakeDrafts) == 0 && compactionDraft == nil {
		return nil, nil
	}

	existing, _ := data["entries"].([]any)
	drafts := make([]any, 0, len(existing)+len(shakeDrafts)+1)
	drafts = append(drafts, existing...)
	drafts = append(drafts, shakeDrafts...)
	if compactionDraft != nil {
		drafts = append(drafts, compactionDraft)
	}
	return map[string]any{"entries": drafts}, nil
}

func (m *manager) maybeBuildMidTurnCompaction(ctx sdk.Context, sid string, turnData map[string]any) map[string]any {
	usage, err := ctx.GetContextUsage()
	if err != nil || usage == nil || usage.Tokens == nil || *usage.Tokens <= 0 || usage.ContextWindow <= 0 {
		return nil
	}

	thresholdTokens := resolveIntSetting(ctx, "compaction.thresholdTokens", -1)
	thresholdPercent := resolveIntSetting(ctx, "compaction.thresholdPercent", -1)
	threshold := ResolveMidTurnThreshold(usage.ContextWindow, thresholdTokens, thresholdPercent)
	if threshold <= 0 || *usage.Tokens < threshold {
		return nil
	}

	// Honor compaction.methodOrder and vision requirement for local snapcompact.
	order := resolveMethodOrder(ctx)
	wantSnap, _ := wantsSnapcompactForMethodOrder(order, "", "")
	if !wantSnap {
		return nil
	}
	target := resolveActiveModel(ctx)
	if target.ID == "" || !target.SupportsImages() {
		return nil
	}
	if target.ContextWindow <= 0 {
		target.ContextWindow = usage.ContextWindow
	}

	keepRecentTokens := resolveIntSetting(ctx, "compaction.keepRecentTokens", defaultKeepRecentTokens)
	if keepRecentTokens <= 0 {
		keepRecentTokens = defaultKeepRecentTokens
	}

	prep := buildMidTurnPreparation(ctx, *usage.Tokens, keepRecentTokens, turnData)
	if prep == nil || prep.FirstKeptEntryID == "" || len(prep.MessagesToSummarize) == 0 {
		return nil
	}

	// Dead-end guard: do not re-compact at the exact same cut point repeatedly.
	if sid != "" {
		m.mu.Lock()
		st := m.stateLocked(sid)
		alreadyTried := st.midTurn.lastMidTurnFirstKept == prep.FirstKeptEntryID
		m.mu.Unlock()
		if alreadyTried {
			return nil
		}
	}

	reserveTokens := resolveIntSetting(ctx, "compaction.reserveTokens", 0)
	variant := resolveShapeSetting(ctx)
	shape := ResolveShape(target, variant)
	maxFrames := ComputeMaxFrames(target, shape, reserveTokens, keepRecentTokens)

	includeThink := ReaderIncludesThinking(target)
	opts := CompactOptions{
		SerializeOptions: SerializeOptions{
			IncludeThinking: new(includeThink),
		},
		Model:     target,
		Shape:     &shape,
		Variant:   variant,
		MaxFrames: maxFrames,
	}

	res, cerr := Compact(*prep, opts)
	if cerr != nil {
		return nil
	}

	// Respect standing image payload budget (2-pass shrink matching onSessionBeforeCompact).
	frameByteBudget := FrameDataBytesBudget(target)
	for range 2 {
		payloadBytes := FrameDataBytes(res.Archive.Frames)
		if payloadBytes <= frameByteBudget || len(res.Archive.Frames) <= 1 {
			break
		}
		nextMax := max(1, (len(res.Archive.Frames)*frameByteBudget)/payloadBytes)
		if nextMax >= len(res.Archive.Frames) {
			break
		}
		opts.MaxFrames = nextMax
		nextRes, rerr := Compact(*prep, opts)
		if rerr != nil {
			break
		}
		res = nextRes
	}
	if FrameDataBytes(res.Archive.Frames) > frameByteBudget {
		return nil
	}

	// Do not inflate context compared to tokensBefore.
	if prep.TokensBefore > 0 && len(res.Archive.Frames) > 0 {
		frameToks := len(res.Archive.Frames) * PlannedFrameTokens(res.Shape)
		textToks := (len([]rune(res.Archive.TextHead)) + len([]rune(res.Archive.TextTail))) / 4
		if frameToks+textToks >= prep.TokensBefore {
			return nil
		}
	}

	if sid != "" {
		archiveCopy := res.Archive
		m.setCachedArchive(sid, summaryCacheKey(res.Summary), &archiveCopy)
		m.mu.Lock()
		m.stateLocked(sid).midTurn.lastMidTurnFirstKept = res.FirstKeptEntryID
		m.mu.Unlock()
	}

	tokensAfter := snapcompactTokensAfter(res)
	frameCount := len(res.Archive.Frames)
	details := map[string]any{
		"readFiles":     nonNilStrings(res.ReadFiles),
		"modifiedFiles": nonNilStrings(res.ModifiedFiles),
		"method":        "snapcompact",
		"phase":         "mid_turn",
		"shortSummary":  res.ShortSummary,
		"tokensAfter":   tokensAfter,
		"frameCount":    frameCount,
		PreserveKey:     res.Archive,
	}
	if isTopLevelForSurface(ctx) {
		surface.RecordCompaction(surface.CompactionMeta{
			Method:       "snapcompact",
			TokensBefore: res.TokensBefore,
			TokensAfter:  tokensAfter,
			FrameCount:   frameCount,
			Summary:      res.Summary,
		})
	}
	if !surface.Native() {
		ctx.Notify("Mid-turn compaction: "+res.ShortSummary, "info")
	}
	return map[string]any{
		"type":             "compaction",
		"summary":          res.Summary,
		"firstKeptEntryId": res.FirstKeptEntryID,
		"details":          details,
	}
}

// buildMidTurnPreparation inspects the active branch (and previous compaction
// archive/summary) to pick a valid cut point keeping up to keepRecentTokens
// of recent history while summarizing older messages.
func buildMidTurnPreparation(ctx sdk.Context, tokensBefore, keepRecentTokens int, turnData map[string]any) *Preparation {
	branch, err := ctx.GetBranch()
	if err != nil || len(branch) < 2 {
		return nil
	}
	rawEntries, _ := ctx.GetEntries()
	rawByID := make(map[string]map[string]any, len(rawEntries))
	for _, raw := range rawEntries {
		var obj map[string]any
		if json.Unmarshal(raw, &obj) != nil {
			continue
		}
		if id, _ := obj["id"].(string); id != "" {
			rawByID[id] = obj
		}
	}

	// Locate latest compaction on branch to carry forward previousArchive / previousSummary
	// and bound the entries being re-summarized.
	var prevArchive *Archive
	var prevSummary string
	startIdx := 0
	for i := len(branch) - 1; i >= 0; i-- {
		if branch[i].Type == "compaction" {
			if rawObj, ok := rawByID[branch[i].ID]; ok {
				prevSummary, _ = rawObj["summary"].(string)
				if a, ok := ExtractArchiveFromDetails(rawObj["details"]); ok {
					prevArchive = a
				} else if a, ok := ExtractArchiveFromDetails(rawObj["preserveData"]); ok {
					prevArchive = a
				}
			}
			if branch[i].FirstKeptEntryID != "" {
				for j := range i {
					if branch[j].ID == branch[i].FirstKeptEntryID {
						startIdx = j
						break
					}
				}
			} else {
				startIdx = i + 1
			}
			break
		}
	}

	type branchItem struct {
		id      string
		valid   bool // valid cut point (never cut at toolResult)
		tokens  int
		message map[string]any
	}
	// Collect latest context_edit per targetId so omitted entries are never archived
	// and replaced entries archive their replaced content.
	type editOverlay struct {
		omitted bool
		content any
	}
	edits := map[string]editOverlay{}
	for i := startIdx; i < len(branch); i++ {
		be := branch[i]
		if be.Type != "context_edit" {
			continue
		}
		rawObj, ok := rawByID[be.ID]
		if !ok {
			continue
		}
		targetID, _ := rawObj["targetId"].(string)
		if targetID == "" {
			continue
		}
		repl, ok := rawObj["replacement"].(map[string]any)
		if !ok || repl == nil {
			edits[targetID] = editOverlay{omitted: true}
		} else {
			edits[targetID] = editOverlay{content: repl["content"]}
		}
	}

	items := make([]branchItem, 0, len(branch)-startIdx)
	for i := startIdx; i < len(branch); i++ {
		be := branch[i]
		if be.Type == "compaction" || be.Type == "context_edit" {
			continue
		}
		ov, hasEdit := edits[be.ID]
		if hasEdit && ov.omitted {
			continue
		}
		var msg map[string]any
		if rawObj, ok := rawByID[be.ID]; ok {
			if be.Type == "message" {
				if mm, ok := rawObj["message"].(map[string]any); ok {
					if hasEdit {
						cloned := make(map[string]any, len(mm))
						for k, v := range mm {
							cloned[k] = v
						}
						cloned["content"] = ov.content
						mm = cloned
					}
					msg = mm
				}
			} else if be.Type == "custom_message" {
				content := rawObj["content"]
				if hasEdit {
					content = ov.content
				}
				msg = map[string]any{
					"role":    "user",
					"content": content,
				}
			}
		}
		if msg == nil && be.Type == "message" && be.Role != "" {
			msg = branchEntryToMessageMap(be)
			if hasEdit {
				msg["content"] = ov.content
			}
		}
		if msg == nil {
			continue
		}
		role := strField(msg, "role")
		validCut := be.ID != "" && (role == "user" || role == "assistant" || be.Type == "custom_message")
		items = append(items, branchItem{
			id:      be.ID,
			valid:   validCut,
			tokens:  estimateMessageMapTokens(msg),
			message: msg,
		})
	}
	if len(items) < 2 {
		return nil
	}

	// Pick the newest valid cut point that keeps ~keepRecentTokens (or at least the latest turn).
	// Walk backwards from the end accumulating tokens until reaching keepRecentTokens.
	cutIdx := -1
	accum := 0
	for i := len(items) - 1; i >= 1; i-- {
		accum += items[i].tokens
		if items[i].valid && accum >= keepRecentTokens {
			cutIdx = i
			break
		}
	}
	// If the whole kept window is smaller than keepRecentTokens, still allow cutting at the
	// latest valid cut point strictly after index 0 (e.g. current turn's assistant message).
	if cutIdx <= 0 {
		msgEntryID := strField(turnData, "messageEntryId")
		for i := len(items) - 1; i >= 1; i-- {
			if items[i].valid && (msgEntryID == "" || items[i].id == msgEntryID) {
				cutIdx = i
				break
			}
		}
	}
	if cutIdx <= 0 {
		for i := len(items) - 1; i >= 1; i-- {
			if items[i].valid {
				cutIdx = i
				break
			}
		}
	}
	if cutIdx <= 0 || items[cutIdx].id == "" {
		return nil
	}

	msgsToSummarize := make([]map[string]any, 0, cutIdx)
	for i := range cutIdx {
		msgsToSummarize = append(msgsToSummarize, items[i].message)
	}
	fileOps := collectFileOpsFromMessages(msgsToSummarize)

	return &Preparation{
		FirstKeptEntryID:    items[cutIdx].id,
		MessagesToSummarize: msgsToSummarize,
		TokensBefore:        tokensBefore,
		PreviousSummary:     prevSummary,
		PreviousArchive:     prevArchive,
		FileOps:             fileOps,
	}
}

func estimateMessageMapTokens(msg map[string]any) int {
	chars := 0
	switch c := msg["content"].(type) {
	case string:
		chars = len(c)
	case []any:
		for _, rawB := range c {
			bm, ok := rawB.(map[string]any)
			if !ok {
				continue
			}
			switch strField(bm, "type") {
			case "text":
				chars += len(strField(bm, "text"))
			case "thinking":
				chars += len(strField(bm, "thinking"))
			case "image":
				chars += 4000
			case "toolCall":
				chars += len(strField(bm, "name")) + 64
				if args, ok := bm["arguments"]; ok && args != nil {
					if raw, err := json.Marshal(args); err == nil {
						chars += len(raw)
					}
				}
			}
		}
	}
	return max(1, (chars+3)/4)
}

func collectFileOpsFromMessages(msgs []map[string]any) FileOps {
	var ops FileOps
	seenRead := map[string]bool{}
	seenWritten := map[string]bool{}
	seenEdited := map[string]bool{}
	for _, msg := range msgs {
		if strField(msg, "role") != "assistant" {
			continue
		}
		blocks, ok := msg["content"].([]any)
		if !ok {
			continue
		}
		for _, rawB := range blocks {
			bm, ok := rawB.(map[string]any)
			if !ok || strField(bm, "type") != "toolCall" {
				continue
			}
			name := strings.ToLower(strings.TrimSpace(strField(bm, "name")))
			args, _ := bm["arguments"].(map[string]any)
			p := strings.TrimSpace(strField(args, "path"))
			if p == "" {
				continue
			}
			base, _ := splitReadPathSelector(p)
			if base == "" {
				continue
			}
			switch name {
			case "read":
				if !seenRead[base] {
					seenRead[base] = true
					ops.Read = append(ops.Read, base)
				}
			case "write":
				if !seenWritten[base] {
					seenWritten[base] = true
					ops.Written = append(ops.Written, base)
				}
			case "edit", "hashline_edit", "ast_edit":
				if !seenEdited[base] {
					seenEdited[base] = true
					ops.Edited = append(ops.Edited, base)
				}
			}
		}
	}
	slices.Sort(ops.Read)
	slices.Sort(ops.Written)
	slices.Sort(ops.Edited)
	return ops
}

// onSessionCompactFailed records compaction failures (including overflow
// recovery failures) so EventAgentBeforeSettle can attempt context promotion
// when `contextPromotion.enabled` is true.
func (m *manager) onSessionCompactFailed(ctx sdk.Context, data map[string]any) (any, error) {
	if aborted, _ := data["aborted"].(bool); aborted {
		return nil, nil
	}
	sid := sessionIDFor(ctx)
	if sid == "" {
		return nil, nil
	}
	reason, _ := data["reason"].(string)
	errMsg, _ := data["errorMessage"].(string)
	if reason == "overflow" || isContextOverflowText(errMsg) {
		m.mu.Lock()
		st := m.stateLocked(sid)
		st.midTurn.overflowPending = true
		if reason != "" {
			st.midTurn.overflowReason = reason
		} else {
			st.midTurn.overflowReason = "overflow"
		}
		m.mu.Unlock()
	}
	return nil, nil
}

// onAgentBeforeSettle performs overflow context promotion when
// `contextPromotion.enabled` is true and the run ended with an overflow error
// or compaction failure. Switches to the configured larger-context target via
// `ctx.SetModelNamed`, emits a `context_edit` omitting the failed assistant turn
// when its entry ID is known, and returns `{"entries": ..., "continue": true}`
// (guarded to at most 1 promotion per turn).
func (m *manager) onAgentBeforeSettle(ctx sdk.Context, data map[string]any) (any, error) {
	outcome, _ := data["outcome"].(string)
	sid := sessionIDFor(ctx)

	// Fast path: non-error outcomes clear overflow/promotion state and do nothing.
	if outcome != "error" {
		if sid != "" {
			m.mu.Lock()
			if st, ok := m.sessions[sid]; ok {
				st.midTurn.overflowPending = false
				st.midTurn.overflowReason = ""
				st.midTurn.overflowMessageEntryID = ""
				st.midTurn.promotedThisTurn = false
			}
			m.mu.Unlock()
		}
		return nil, nil
	}

	if !resolveBoolSetting(ctx, "contextPromotion.enabled", false) {
		return nil, nil
	}

	var (
		overflowPending bool
		failedEntryID   string
		alreadyPromoted bool
	)
	if sid != "" {
		m.mu.Lock()
		if st, ok := m.sessions[sid]; ok {
			overflowPending = st.midTurn.overflowPending
			failedEntryID = st.midTurn.overflowMessageEntryID
			alreadyPromoted = st.midTurn.promotedThisTurn
		}
		m.mu.Unlock()
	}

	// Loop guard: at most 1 promotion per turn.
	if alreadyPromoted {
		if sid != "" {
			m.mu.Lock()
			if st, ok := m.sessions[sid]; ok {
				st.midTurn.overflowPending = false
			}
			m.mu.Unlock()
		}
		return nil, nil
	}

	// If neither EventSessionCompactFailed nor EventTurnEnd flagged overflow yet,
	// inspect the boundary preview context for a trailing overflow assistant error.
	previewFailedID, previewOverflow := inspectBoundaryPreviewForOverflow(data)
	if !overflowPending && !previewOverflow {
		return nil, nil
	}
	if failedEntryID == "" {
		failedEntryID = previewFailedID
	}
	if failedEntryID == "" {
		failedEntryID = findTrailingAssistantEntryID(ctx)
	}

	current := resolveActiveModel(ctx)
	targetSpec := resolvePromotionTarget(ctx, current)
	if targetSpec == "" || isSameModelSpec(current, targetSpec) {
		if sid != "" {
			m.mu.Lock()
			if st, ok := m.sessions[sid]; ok {
				st.midTurn.overflowPending = false
			}
			m.mu.Unlock()
		}
		return nil, nil
	}

	switched, err := ctx.SetModelNamed(targetSpec)
	if err != nil || !switched {
		if sid != "" {
			m.mu.Lock()
			if st, ok := m.sessions[sid]; ok {
				st.midTurn.overflowPending = false
			}
			m.mu.Unlock()
		}
		return nil, nil
	}

	if sid != "" {
		m.mu.Lock()
		st := m.stateLocked(sid)
		st.midTurn.promotedThisTurn = true
		st.midTurn.overflowPending = false
		st.midTurn.overflowReason = ""
		st.midTurn.overflowMessageEntryID = ""
		m.mu.Unlock()
	}

	ctx.Notify(fmt.Sprintf("Context overflow: promoted model to %s and retrying.", targetSpec), "info")

	existing, _ := data["entries"].([]any)
	drafts := make([]any, 0, len(existing)+1)
	drafts = append(drafts, existing...)
	if failedEntryID != "" {
		drafts = append(drafts, map[string]any{
			"type":        "context_edit",
			"targetId":    failedEntryID,
			"replacement": nil,
		})
	}
	return map[string]any{
		"entries":  drafts,
		"continue": true,
	}, nil
}

func inspectBoundaryPreviewForOverflow(data map[string]any) (entryID string, isOverflow bool) {
	ctxMap, _ := data["context"].(map[string]any)
	if ctxMap == nil {
		return "", false
	}
	ctxEntries, _ := ctxMap["contextEntries"].([]any)
	for i := len(ctxEntries) - 1; i >= 0; i-- {
		pe, ok := ctxEntries[i].(map[string]any)
		if !ok {
			continue
		}
		src, _ := pe["sourceEntry"].(map[string]any)
		if src == nil || strField(src, "type") != "message" {
			continue
		}
		msg, _ := src["message"].(map[string]any)
		if msg == nil || strField(msg, "role") != "assistant" {
			continue
		}
		if strField(msg, "stopReason") == "error" {
			id := strField(src, "id")
			if isContextOverflowText(strField(msg, "errorMessage")) {
				return id, true
			}
			return id, false
		}
		break
	}
	return "", false
}

func findTrailingAssistantEntryID(ctx sdk.Context) string {
	branch, err := ctx.GetBranch()
	if err != nil || len(branch) == 0 {
		return ""
	}
	for i := len(branch) - 1; i >= 0; i-- {
		be := branch[i]
		if be.Type == "message" && be.Role == "assistant" && be.ID != "" {
			return be.ID
		}
		if be.Type == "message" && be.Role == "user" {
			break
		}
	}
	return ""
}
