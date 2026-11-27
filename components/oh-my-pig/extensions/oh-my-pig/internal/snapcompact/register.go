package snapcompact

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/surface"
)

// upstream: packages/coding-agent/src/session/compact-modes.ts
// upstream: packages/coding-agent/src/session/compaction-methods.ts
// upstream: packages/coding-agent/src/session/session-maintenance.ts

// Setting reads an effective setting for ctx (wired to internal/settings.Any by wiring.go).
var Setting func(ctx sdk.Context, key string) any

// HandoffPrompt renders omp's handoff document prompt for `/compact handoff [focus]`
// (wired by wiring.go to internal/modes).
var HandoffPrompt func(focus string) string

// IsSubagent reports whether ctx belongs to a spawned child subagent session
// (wired by wiring.go to internal/agents.IsSubagent).
var IsSubagent func(ctx sdk.Context) bool

func isTopLevelForSurface(ctx sdk.Context) bool {
	return IsSubagent == nil || !IsSubagent(ctx)
}

// DefaultMethodOrder mirrors packages/coding-agent/src/session/compaction-methods.ts.
var DefaultMethodOrder = []string{"remote", "snapcompact", "handoff", "shake", "soft"}

// ParsedCompactArgs holds the result of parsing `/compact [mode] [focus]`.
type ParsedCompactArgs struct {
	Mode         string // "", "soft", "remote", "snapcompact", "handoff"
	Instructions string
	Err          string
}

// ParseCompactArgs splits `/compact` args into a leading mode subcommand + focus text.
// upstream: packages/coding-agent/src/session/compact-modes.ts parseCompactArgs
func ParseCompactArgs(args string) ParsedCompactArgs {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return ParsedCompactArgs{}
	}
	first := trimmed
	rest := ""
	if idx := strings.IndexAny(trimmed, " \t\n\r"); idx >= 0 {
		first = trimmed[:idx]
		rest = strings.TrimSpace(trimmed[idx+1:])
	}
	mode := strings.ToLower(first)
	switch mode {
	case "soft", "remote", "snapcompact", "handoff":
		if mode == "snapcompact" && rest != "" {
			return ParsedCompactArgs{
				Err: "/compact snapcompact does not take focus instructions (it archives history without an LLM summary).",
			}
		}
		return ParsedCompactArgs{Mode: mode, Instructions: rest}
	default:
		return ParsedCompactArgs{Instructions: trimmed}
	}
}

type sessionState struct {
	// pendingMode is a one-shot mode override from `/compact <mode>` consumed by
	// the next session_before_compact event.
	pendingMode string

	// cachedArchive holds the latest snapcompact archive for the session so
	// EventContext does not re-scan session JSONL entries on every turn.
	cachedKey     string // summary + timestamp key
	cachedArchive *Archive
	cacheLoaded   bool

	shake   shakeState
	midTurn midTurnState
	inline  inlineState
}

type manager struct {
	mu       sync.Mutex
	sessions map[string]*sessionState
}

func newManager() *manager {
	return &manager{sessions: make(map[string]*sessionState)}
}

var defaultManager = newManager()

// SetPendingMode arms a one-shot compaction mode override for the session
// (used when `/compact snapcompact|soft|handoff|remote` invokes ctx.Compact).
func SetPendingMode(sessionID, mode string) {
	if sessionID == "" {
		return
	}
	defaultManager.mu.Lock()
	defer defaultManager.mu.Unlock()
	st := defaultManager.stateLocked(sessionID)
	st.pendingMode = mode
}

func (m *manager) stateLocked(sid string) *sessionState {
	st, ok := m.sessions[sid]
	if !ok {
		st = &sessionState{}
		m.sessions[sid] = st
	}
	return st
}

func (m *manager) release(sid string) {
	m.mu.Lock()
	delete(m.sessions, sid)
	m.mu.Unlock()
}

func (m *manager) consumePendingMode(sid string) string {
	if sid == "" {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.sessions[sid]
	if !ok {
		return ""
	}
	mode := st.pendingMode
	st.pendingMode = ""
	return mode
}

func (m *manager) setCachedArchive(sid, key string, archive *Archive) {
	if sid == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.stateLocked(sid)
	st.cachedKey = key
	st.cachedArchive = archive
	st.cacheLoaded = true
}

func (m *manager) clearCache(sid string) {
	if sid == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if st, ok := m.sessions[sid]; ok {
		st.cachedKey = ""
		st.cachedArchive = nil
		st.cacheLoaded = false
		st.inline.clear()
	}
}

func (m *manager) getCachedArchive(sid, key string) (*Archive, bool) {
	if sid == "" {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.sessions[sid]
	if !ok || !st.cacheLoaded || st.cachedKey != key {
		return nil, false
	}
	return st.cachedArchive, true
}

// Register attaches snapcompact's lifecycle hooks onto ext:
//   - Command `/shake` + EventInput: `/compact [mode] [focus]` and `/shake [elide|images|thinking|all]`
//   - EventSessionBeforeCompact: runs local bitmap-frame snapcompact by default on vision models
//   - EventSessionCompact / EventSessionCompactFailed / EventSessionTree / EventSessionStart: cache & overflow state
//   - EventContext: re-attaches snapcompact image frames and applies `/shake` / superseded-read pruning
//   - EventTurnEnd: commits durable `/shake` context_edit drafts and runs mid-turn compaction when over threshold
//   - EventAgentBeforeSettle: promotes to a larger-context model on context overflow when enabled
func Register(ext *sdk.Extension) {
	registerWithManager(ext, defaultManager)
}

func registerWithManager(ext *sdk.Extension, m *manager) {
	lifecycle.OnEnd("snapcompact", func(sid string) {
		m.release(sid)
	})
	m.registerShake(ext)
	ext.OnEvent(sdk.EventInput, m.onInput)
	ext.OnEvent(sdk.EventSessionBeforeCompact, m.onSessionBeforeCompact)
	ext.OnEvent(sdk.EventSessionCompact, m.onSessionCompact)
	ext.OnEvent(sdk.EventSessionCompactFailed, m.onSessionCompactFailed)
	ext.OnEvent(sdk.EventSessionTree, m.onSessionTree)
	ext.OnSessionStart(m.onSessionStart)
	ext.OnEvent(sdk.EventContext, m.onContext)
	ext.OnEvent(sdk.EventContextWithSystem, m.onContextWithSystem)
	ext.OnEvent(sdk.EventTurnEnd, m.onTurnEnd)
	ext.OnEvent(sdk.EventAgentBeforeSettle, m.onAgentBeforeSettle)
}

func sessionIDFor(ctx sdk.Context) string {
	id, err := ctx.GetSessionID()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(id)
}

// onInput intercepts `/compact <mode> [focus]` so users can explicitly choose
// `/compact snapcompact`, `/compact soft`, `/compact handoff [focus]`, or `/compact remote`.
// Bare `/compact` and `/compact <custom focus>` pass through to PiG's built-in `/compact`,
// where onSessionBeforeCompact honors compaction.methodOrder (snapcompact first when no focus).
func (m *manager) onInput(ctx sdk.Context, data map[string]any) (any, error) {
	text, _ := data["text"].(string)
	trimmed := strings.TrimSpace(text)
	if handled, resp := m.handleShakeInput(ctx, trimmed); handled {
		return resp, nil
	}
	if !strings.HasPrefix(trimmed, "/compact ") {
		return map[string]any{"action": "continue"}, nil
	}
	args := strings.TrimSpace(trimmed[len("/compact "):])
	parsed := ParseCompactArgs(args)
	if parsed.Err != "" {
		ctx.Notify(parsed.Err, "error")
		return map[string]any{"action": "handled"}, nil
	}
	if parsed.Mode == "" {
		// Plain focus instructions — let PiG's built-in /compact handle it (and
		// onSessionBeforeCompact will skip snapcompact since CustomInstructions != "").
		return map[string]any{"action": "continue"}, nil
	}

	sid := sessionIDFor(ctx)
	switch parsed.Mode {
	case "snapcompact":
		m.mu.Lock()
		if sid != "" {
			m.stateLocked(sid).pendingMode = "snapcompact"
		}
		m.mu.Unlock()
		ctx.Compact(nil)
		return map[string]any{"action": "handled"}, nil
	case "soft", "remote":
		m.mu.Lock()
		if sid != "" {
			m.stateLocked(sid).pendingMode = parsed.Mode
		}
		m.mu.Unlock()
		opts := map[string]any{}
		if parsed.Instructions != "" {
			opts["customInstructions"] = parsed.Instructions
		}
		ctx.Compact(opts)
		return map[string]any{"action": "handled"}, nil
	case "handoff":
		m.mu.Lock()
		if sid != "" {
			m.stateLocked(sid).pendingMode = "handoff"
		}
		m.mu.Unlock()
		instr := parsed.Instructions
		if HandoffPrompt != nil {
			instr = HandoffPrompt(parsed.Instructions)
		}
		ctx.Compact(map[string]any{"customInstructions": instr})
		ctx.Notify("Handoff compaction requested.", "info")
		return map[string]any{"action": "handled"}, nil
	}
	return map[string]any{"action": "continue"}, nil
}

func (m *manager) onSessionStart(ctx sdk.Context, _ map[string]any) (any, error) {
	sid := sessionIDFor(ctx)
	m.clearCache(sid)
	m.resetTransientState(sid)
	if isTopLevelForSurface(ctx) {
		hydrateSurfaceCompactions(ctx)
	}
	return nil, nil
}

func (m *manager) onSessionTree(ctx sdk.Context, _ map[string]any) (any, error) {
	sid := sessionIDFor(ctx)
	m.clearCache(sid)
	m.resetTransientState(sid)
	if isTopLevelForSurface(ctx) {
		hydrateSurfaceCompactions(ctx)
	}
	return nil, nil
}

func (m *manager) resetTransientState(sid string) {
	if sid == "" {
		return
	}
	m.mu.Lock()
	if st, ok := m.sessions[sid]; ok {
		st.shake.pendingEdits = nil
		st.shake.pendingOrder = nil
		st.midTurn = midTurnState{}
	}
	m.mu.Unlock()
}

func (m *manager) onSessionCompact(ctx sdk.Context, data map[string]any) (any, error) {
	sid := sessionIDFor(ctx)
	if sid == "" {
		return nil, nil
	}
	m.mu.Lock()
	if st, ok := m.sessions[sid]; ok {
		st.midTurn.lastMidTurnFirstKept = ""
		st.midTurn.overflowPending = false
		st.midTurn.overflowReason = ""
		st.midTurn.overflowMessageEntryID = ""
		st.inline.clear()
	}
	m.mu.Unlock()
	entry, _ := data["compactionEntry"].(map[string]any)
	if entry == nil {
		m.clearCache(sid)
		return nil, nil
	}
	summary, _ := entry["summary"].(string)
	if archive, ok := ExtractArchiveFromDetails(entry["details"]); ok {
		m.setCachedArchive(sid, summaryCacheKey(summary), archive)
	} else {
		m.setCachedArchive(sid, summaryCacheKey(summary), nil)
	}
	if isTopLevelForSurface(ctx) {
		if meta, ok := compactionMetaFromEntry(entry); ok {
			surface.RecordCompaction(meta)
		}
	}
	return nil, nil
}

func resolveMethodOrder(ctx sdk.Context) []string {
	if Setting == nil {
		return DefaultMethodOrder
	}
	raw := Setting(ctx, "compaction.methodOrder")
	if raw == nil {
		return DefaultMethodOrder
	}
	var out []string
	switch v := raw.(type) {
	case []string:
		out = append(out, v...)
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
	}
	if len(out) == 0 {
		return DefaultMethodOrder
	}
	return out
}

func resolveShapeSetting(ctx sdk.Context) string {
	if Setting == nil {
		return "auto"
	}
	if s, ok := Setting(ctx, "snapcompact.shape").(string); ok && s != "" {
		return s
	}
	return "auto"
}

func wantsSnapcompactForMethodOrder(order []string, forcedMode string, customInstructions string) (want bool, strict bool) {
	if forcedMode != "" {
		return forcedMode == "snapcompact", forcedMode == "snapcompact"
	}
	// Focus instructions require an LLM summary (matches omp session-maintenance.ts:1554-1556).
	if strings.TrimSpace(customInstructions) != "" {
		return false, false
	}
	// Walk configured methodOrder: skip "remote" (host-only server compaction) and
	// "shake" (in-place pruning); if "snapcompact" comes before "soft"/"handoff", use it.
	for _, method := range order {
		switch strings.ToLower(strings.TrimSpace(method)) {
		case "snapcompact":
			return true, false
		case "soft", "handoff":
			return false, false
		}
	}
	return false, false
}

func resolveActiveModel(ctx sdk.Context) ModelTarget {
	mi, err := ctx.GetModelInfo()
	if err != nil || mi == nil {
		return ModelTarget{}
	}
	target := ModelTarget{
		ID:            mi.ID,
		Provider:      mi.Provider,
		ContextWindow: mi.ContextWindow,
	}
	if reg, err := ctx.ModelRegistry().Find(mi.Provider, mi.ID); err == nil && reg != nil {
		if api, ok := reg["api"].(string); ok {
			target.API = api
		}
		if baseURL, ok := reg["baseUrl"].(string); ok {
			target.BaseURL = baseURL
		}
		if target.ContextWindow <= 0 {
			if cw, ok := numberField(reg, "contextWindow"); ok {
				target.ContextWindow = cw
			}
		}
		if rawIn, ok := reg["input"].([]any); ok {
			for _, item := range rawIn {
				if s, ok := item.(string); ok {
					target.Input = append(target.Input, s)
				}
			}
		} else if rawInStr, ok := reg["input"].([]string); ok {
			target.Input = append(target.Input, rawInStr...)
		}
	}
	return target
}

func numberField(m map[string]any, key string) (int, bool) {
	switch v := m[key].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case int64:
		return int(v), true
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n), true
		}
	}
	return 0, false
}

// onSessionBeforeCompact intercepts compaction and runs local bitmap-frame
// snapcompact when enabled by compaction.methodOrder and supported by the active model.
// On non-vision models, unsupported character sets, or when focus instructions /
// soft mode are requested, it returns (nil, nil) so PiG falls back seamlessly to
// standard LLM compaction (or cancels with an error if the user explicitly ran
// `/compact snapcompact`).
func (m *manager) onSessionBeforeCompact(ctx sdk.Context, data map[string]any) (any, error) {
	sid := sessionIDFor(ctx)
	forcedMode := m.consumePendingMode(sid)
	customInstructions, _ := data["customInstructions"].(string)
	// PiG's builtin `/compact <args>` bypasses EventInput and passes `<args>`
	// directly as customInstructions to session_before_compact. Parse any
	// leading mode keyword (`snapcompact`, `soft`, `remote`, `handoff`) here so
	// `/compact snapcompact` runs snapcompact even when EventInput did not fire.
	if forcedMode == "" && strings.TrimSpace(customInstructions) != "" {
		parsed := ParseCompactArgs(customInstructions)
		if parsed.Err != "" {
			ctx.Notify(parsed.Err, "error")
			return map[string]any{"cancel": true}, nil
		}
		if parsed.Mode != "" {
			forcedMode = parsed.Mode
			customInstructions = parsed.Instructions
		}
	}
	order := resolveMethodOrder(ctx)

	want, strict := wantsSnapcompactForMethodOrder(order, forcedMode, customInstructions)
	if !want {
		return nil, nil
	}

	target := resolveActiveModel(ctx)
	if target.ID == "" {
		return nil, nil
	}
	if !target.SupportsImages() {
		if strict {
			ctx.Notify(fmt.Sprintf("snapcompact needs a vision-capable model (%s is text-only)", target.ID), "error")
			return map[string]any{"cancel": true}, nil
		}
		return nil, nil
	}

	prepMap, _ := data["preparation"].(map[string]any)
	if prepMap == nil {
		return nil, nil
	}
	firstKept, _ := prepMap["firstKeptEntryId"].(string)
	if firstKept == "" {
		return nil, nil
	}
	tokensBefore, _ := numberField(prepMap, "tokensBefore")
	prevSummary, _ := prepMap["previousSummary"].(string)

	msgsToSum := toMapSlice(prepMap["messagesToSummarize"])
	turnPrefix := toMapSlice(prepMap["turnPrefixMessages"])
	fileOps := extractFileOps(prepMap["fileOps"])
	prevArchive := findLatestArchiveInBranch(data["branchEntries"])

	reserveTokens := 0
	keepRecentTokens := 20000
	if sm, ok := prepMap["settings"].(map[string]any); ok {
		if v, ok := numberField(sm, "reserveTokens"); ok && v > 0 {
			reserveTokens = v
		}
		if v, ok := numberField(sm, "keepRecentTokens"); ok && v > 0 {
			keepRecentTokens = v
		}
	}

	variant := resolveShapeSetting(ctx)
	shape := ResolveShape(target, variant)
	maxFrames := ComputeMaxFrames(target, shape, reserveTokens, keepRecentTokens)

	prep := Preparation{
		FirstKeptEntryID:    firstKept,
		MessagesToSummarize: msgsToSum,
		TurnPrefixMessages:  turnPrefix,
		TokensBefore:        tokensBefore,
		PreviousSummary:     prevSummary,
		PreviousArchive:     prevArchive,
		FileOps:             fileOps,
	}

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

	res, err := Compact(prep, opts)
	if err != nil {
		if strict {
			ctx.Notify(err.Error(), "error")
			return map[string]any{"cancel": true}, nil
		}
		return nil, nil
	}

	// Re-render with fewer frames if base64 payload exceeds standing frame budget
	// (mirrors omp session-maintenance.ts #renderSnapcompactArchive).
	frameByteBudget := FrameDataBytesBudget(target)
	for rerender := 0; rerender < 2; rerender++ {
		payloadBytes := FrameDataBytes(res.Archive.Frames)
		if payloadBytes <= frameByteBudget || len(res.Archive.Frames) <= 1 {
			break
		}
		nextMax := (len(res.Archive.Frames) * frameByteBudget) / payloadBytes
		if nextMax < 1 {
			nextMax = 1
		}
		if nextMax >= len(res.Archive.Frames) {
			break
		}
		opts.MaxFrames = nextMax
		nextRes, rerr := Compact(prep, opts)
		if rerr != nil {
			break
		}
		res = nextRes
	}
	if FrameDataBytes(res.Archive.Frames) > frameByteBudget {
		if strict {
			ctx.Notify("snapcompact produced too much standing image payload for one request", "error")
			return map[string]any{"cancel": true}, nil
		}
		return nil, nil
	}
	// Guard against inflating context when not explicitly forced via `/compact snapcompact`.
	if !strict && tokensBefore > 0 && len(res.Archive.Frames) > 0 {
		frameToks := len(res.Archive.Frames) * PlannedFrameTokens(res.Shape)
		textToks := (len([]rune(res.Archive.TextHead)) + len([]rune(res.Archive.TextTail))) / 4
		if frameToks+textToks >= tokensBefore {
			return nil, nil
		}
	}
	// Cache immediately so the very next EventContext call has zero JSONL scan cost.
	archiveCopy := res.Archive
	m.setCachedArchive(sid, summaryCacheKey(res.Summary), &archiveCopy)

	tokensAfter := snapcompactTokensAfter(res)
	frameCount := len(res.Archive.Frames)
	details := map[string]any{
		"readFiles":     nonNilStrings(res.ReadFiles),
		"modifiedFiles": nonNilStrings(res.ModifiedFiles),
		"method":        "snapcompact",
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
		ctx.Notify(res.ShortSummary, "info")
	}
	return map[string]any{
		"compaction": map[string]any{
			"summary":          res.Summary,
			"firstKeptEntryId": res.FirstKeptEntryID,
			"tokensBefore":     res.TokensBefore,
			"details":          details,
		},
	}, nil
}

// onContext attaches persisted snapcompact history blocks (verbatim text edges +
// PNG frames) to any `compactionSummary` message in the active context before PiG's
// ConvertToLLM turns it into a provider user message.
//
// Because PiG's agent/messages.go converts `role: "compactionSummary"` into a single
// plain-text user block (ignoring image blocks), we rewrite the `compactionSummary`
// message into a `role: "user"` message whose `content` array carries:
//
//	[ {type: "text", text: summary}, ...HistoryBlocks(archive)... ]
//
// matching omp's packages/agent/src/compaction/messages.ts:248-268 verbatim.
//
// If the active model does not support image input, we serve omp's ViewWithoutFrames
// text-only fallback so switching to a text-only model mid-session keeps working.
func (m *manager) onContext(ctx sdk.Context, data map[string]any) (any, error) {
	messages, ok := data["messages"].([]any)
	if !ok || len(messages) == 0 {
		return nil, nil
	}

	protectedSummaryIdx := -1

	// Fast path: most turns have no compactionSummary message at all.
	summaryIdx := -1
	var summaryMsg map[string]any
	for i, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if strField(msg, "role") == "compactionSummary" {
			summaryIdx = i
			summaryMsg = msg
			break
		}
	}
	if summaryIdx >= 0 {
		summaryText := strField(summaryMsg, "summary")
		if isSnapcompactSummary(summaryText) {
			if archive := m.resolveArchiveForSummary(ctx, summaryText); archive != nil {
				target := resolveActiveModel(ctx)
				includeThink := ReaderIncludesThinking(target)
				vision := target.ID == "" || target.SupportsImages()

				var blocks []any
				if !vision || (!includeThink && ArchiveIncludesThinking(summaryText, *archive)) {
					// Text-only fallback (non-vision reader or Claude reading a thinking-bearing archive).
					renderedSummary, plainHistory := ViewWithoutFrames(summaryText, *archive, includeThink)
					fullText := renderedSummary
					if plainHistory != "" {
						if fullText != "" && !strings.HasSuffix(fullText, "\n") {
							fullText += "\n"
						}
						fullText += plainHistory
					}
					blocks = []any{
						map[string]any{"type": "text", "text": fullText},
					}
				} else {
					histBlocks := HistoryBlocks(*archive, FrameDataBytesBudget(target))
					blocks = make([]any, 0, 1+len(histBlocks))
					blocks = append(blocks, map[string]any{"type": "text", "text": summaryText})
					for _, hb := range histBlocks {
						blocks = append(blocks, hb.ToMap())
					}
				}

				// Replace the compactionSummary map in-place with a user message carrying the
				// multimodal content array so PiG's ConvertToLLM forwards all image frames intact
				// and list identity (and positional system messages) are preserved.
				ts := summaryMsg["timestamp"]
				clear(summaryMsg)
				summaryMsg["role"] = "user"
				summaryMsg["content"] = blocks
				if ts != nil {
					summaryMsg["timestamp"] = ts
				}
				protectedSummaryIdx = summaryIdx
			}
		}
	}

	m.applyShakeOnContext(ctx, messages, protectedSummaryIdx)
	return nil, nil
}

func isSnapcompactSummary(summary string) bool {
	return strings.HasSuffix(strings.TrimRight(summary, " \t\r\n"), historySectionHead)
}

func summaryCacheKey(summary string) string {
	return summary
}

func (m *manager) resolveArchiveForSummary(ctx sdk.Context, summary string) *Archive {
	sid := sessionIDFor(ctx)
	key := summaryCacheKey(summary)
	if cached, ok := m.getCachedArchive(sid, key); ok {
		return cached
	}
	rawEntries, err := ctx.GetEntries()
	if err != nil || len(rawEntries) == 0 {
		return nil
	}
	// Restrict to current branch when GetBranch succeeds so abandoned branches don't match.
	branchIDs := map[string]bool{}
	if branch, berr := ctx.GetBranch(); berr == nil && len(branch) > 0 {
		for _, be := range branch {
			if be.ID != "" {
				branchIDs[be.ID] = true
			}
		}
	}
	filterByBranch := len(branchIDs) > 0

	var found *Archive
	for i := len(rawEntries) - 1; i >= 0; i-- {
		var env struct {
			ID      string `json:"id"`
			Type    string `json:"type"`
			Summary string `json:"summary"`
			Details any    `json:"details"`
		}
		if json.Unmarshal(rawEntries[i], &env) != nil || env.Type != "compaction" {
			continue
		}
		if filterByBranch && env.ID != "" && !branchIDs[env.ID] {
			continue
		}
		if env.Summary != summary {
			continue
		}
		if a, ok := ExtractArchiveFromDetails(env.Details); ok {
			found = a
		}
		break
	}
	m.setCachedArchive(sid, key, found)
	return found
}

func findLatestArchiveInBranch(raw any) *Archive {
	entries, ok := raw.([]any)
	if !ok {
		return nil
	}
	for i := len(entries) - 1; i >= 0; i-- {
		m, ok := entries[i].(map[string]any)
		if !ok || strField(m, "type") != "compaction" {
			continue
		}
		if a, ok := ExtractArchiveFromDetails(m["details"]); ok {
			return a
		}
		if a, ok := ExtractArchiveFromDetails(m["preserveData"]); ok {
			return a
		}
		return nil
	}
	return nil
}

func toMapSlice(raw any) []map[string]any {
	items, ok := raw.([]any)
	if !ok || len(items) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func extractFileOps(raw any) FileOps {
	m, ok := raw.(map[string]any)
	if !ok || m == nil {
		return FileOps{}
	}
	return FileOps{
		Read:    stringSlice(m["read"]),
		Written: stringSlice(m["written"]),
		Edited:  stringSlice(m["edited"]),
	}
}

func stringSlice(raw any) []string {
	switch v := raw.(type) {
	case []string:
		return slices.Clone(v)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func snapcompactTokensAfter(res Result) int {
	return len(res.Archive.Frames)*PlannedFrameTokens(res.Shape) +
		(len([]rune(res.Archive.TextHead))+len([]rune(res.Archive.TextTail)))/4 +
		len([]rune(res.Summary))/4
}

func compactionMetaFromEntry(entry map[string]any) (surface.CompactionMeta, bool) {
	if entry == nil {
		return surface.CompactionMeta{}, false
	}
	summary, _ := entry["summary"].(string)
	tokensBefore, _ := numberField(entry, "tokensBefore")

	var method, warning string
	var tokensAfter, frameCount int
	if details, ok := entry["details"].(map[string]any); ok && details != nil {
		method, _ = details["method"].(string)
		warning, _ = details["warning"].(string)
		tokensAfter, _ = numberField(details, "tokensAfter")
		frameCount, _ = numberField(details, "frameCount")
	}
	if archive, ok := ExtractArchiveFromDetails(entry["details"]); ok && archive != nil {
		if method == "" {
			method = "snapcompact"
		}
		if frameCount <= 0 {
			frameCount = len(archive.Frames)
		}
	} else if archive, ok := ExtractArchiveFromDetails(entry["preserveData"]); ok && archive != nil {
		if method == "" {
			method = "snapcompact"
		}
		if frameCount <= 0 {
			frameCount = len(archive.Frames)
		}
	}
	meta := surface.CompactionMeta{
		Method:       method,
		TokensBefore: tokensBefore,
		TokensAfter:  tokensAfter,
		FrameCount:   frameCount,
		Summary:      summary,
		Warning:      warning,
	}
	if meta.TokensBefore <= 0 && meta.Summary == "" && meta.Method == "" {
		return surface.CompactionMeta{}, false
	}
	return meta, true
}

func hydrateSurfaceCompactions(ctx sdk.Context) {
	rawEntries, err := ctx.GetEntries()
	if err != nil {
		return
	}
	if len(rawEntries) == 0 {
		surface.SetCompactions(nil)
		return
	}
	// Restrict to current branch when GetBranch succeeds so abandoned branches
	// don't pollute lookup after a tree switch.
	branchIDs := map[string]bool{}
	if branch, berr := ctx.GetBranch(); berr == nil && len(branch) > 0 {
		for _, be := range branch {
			if be.ID != "" {
				branchIDs[be.ID] = true
			}
		}
	}
	filterByBranch := len(branchIDs) > 0

	var list []surface.CompactionMeta
	for _, raw := range rawEntries {
		var entry map[string]any
		if json.Unmarshal(raw, &entry) != nil {
			continue
		}
		if strField(entry, "type") != "compaction" {
			continue
		}
		if id := strField(entry, "id"); filterByBranch && id != "" && !branchIDs[id] {
			continue
		}
		if meta, ok := compactionMetaFromEntry(entry); ok {
			list = append(list, meta)
		}
	}
	surface.SetCompactions(list)
}
