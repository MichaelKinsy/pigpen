package snapcompact

import (
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"strconv"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// upstream: packages/coding-agent/src/session/shake-types.ts
// upstream: packages/coding-agent/src/session/session-maintenance.ts:865-910,1025-1247
// upstream: packages/coding-agent/src/slash-commands/builtin-lifecycle.ts:123-130,320-347
// upstream: packages/agent/src/compaction/shake.ts
// upstream: packages/agent/src/compaction/pruning.ts:668-687

const (
	// shakeElideMinBytes is the minimum byte size of an older toolResult body
	// before `/shake` (elide) replaces it with a head/tail preview + optional spill path.
	shakeElideMinBytes = 8 * 1024

	// shakePreviewHeadChars / shakePreviewTailChars keep a readable head and tail
	// snippet when eliding a large toolResult body.
	shakePreviewHeadChars = 400
	shakePreviewTailChars = 200

	// supersedeReadsMinMessages guards automatic superseded-read elision so short
	// sessions (including single-turn / oracle proofs) pay zero cost and see zero
	// context mutation unless explicitly shaken.
	supersedeReadsMinMessages = 8
)

// WriteSpill persists an elided toolResult body to a session spill file and
// returns its reference path/URL (e.g. `local://N.log`), or "" when unavailable.
// Wired by wiring.go to shell.WriteSessionSpill.
var WriteSpill func(ctx sdk.Context, label, content string) string

// ShakeMode selects what `/shake` strips from conversation context.
type ShakeMode string

const (
	ShakeModeElide    ShakeMode = "elide"
	ShakeModeImages   ShakeMode = "images"
	ShakeModeThinking ShakeMode = "thinking"
	ShakeModeAll      ShakeMode = "all"
)

// ShakeResult summarizes what a shake pass reclaimed.
type ShakeResult struct {
	Mode                  ShakeMode
	ToolResultsElided     int
	SupersededReadsElided int
	ImagesDropped         int
	ThinkingBlocksDropped int
	CharsFreed            int
	TokensFreed           int
	ContextEditsQueued    int
}

// ParseShakeMode parses `/shake [elide|images|thinking|all]`; empty defaults to elide.
// upstream: packages/coding-agent/src/slash-commands/builtin-lifecycle.ts:124-130
func ParseShakeMode(args string) (ShakeMode, string) {
	verb := strings.ToLower(strings.TrimSpace(args))
	switch verb {
	case "", "elide":
		return ShakeModeElide, ""
	case "images":
		return ShakeModeImages, ""
	case "thinking":
		return ShakeModeThinking, ""
	case "all":
		return ShakeModeAll, ""
	default:
		return "", fmt.Sprintf("Unknown /shake mode %q. Use elide, images, thinking, or all.", verb)
	}
}

// FormatShakeSummary formats a one-line user notification summarizing a ShakeResult.
// upstream: packages/coding-agent/src/session/shake-types.ts:31-58
func FormatShakeSummary(r ShakeResult) string {
	switch r.Mode {
	case ShakeModeImages:
		if r.ImagesDropped == 0 {
			return "No older images found to shake in this session."
		}
		noun := "images"
		if r.ImagesDropped == 1 {
			noun = "image"
		}
		return fmt.Sprintf("Dropped %d %s (%d chars, ~%d tokens freed).", r.ImagesDropped, noun, r.CharsFreed, r.TokensFreed)
	case ShakeModeThinking:
		if r.ThinkingBlocksDropped == 0 {
			return "No older thinking blocks found to shake in this session."
		}
		noun := "thinking blocks"
		if r.ThinkingBlocksDropped == 1 {
			noun = "thinking block"
		}
		return fmt.Sprintf("Dropped %d %s (%d chars, ~%d tokens freed).", r.ThinkingBlocksDropped, noun, r.CharsFreed, r.TokensFreed)
	default:
		var parts []string
		if r.ToolResultsElided > 0 {
			noun := "tool results"
			if r.ToolResultsElided == 1 {
				noun = "tool result"
			}
			parts = append(parts, fmt.Sprintf("%d %s", r.ToolResultsElided, noun))
		}
		if r.SupersededReadsElided > 0 {
			noun := "superseded reads"
			if r.SupersededReadsElided == 1 {
				noun = "superseded read"
			}
			parts = append(parts, fmt.Sprintf("%d %s", r.SupersededReadsElided, noun))
		}
		if r.ImagesDropped > 0 {
			noun := "images"
			if r.ImagesDropped == 1 {
				noun = "image"
			}
			parts = append(parts, fmt.Sprintf("%d %s", r.ImagesDropped, noun))
		}
		if r.ThinkingBlocksDropped > 0 {
			noun := "thinking blocks"
			if r.ThinkingBlocksDropped == 1 {
				noun = "thinking block"
			}
			parts = append(parts, fmt.Sprintf("%d %s", r.ThinkingBlocksDropped, noun))
		}
		if len(parts) == 0 {
			return "Nothing to shake."
		}
		return fmt.Sprintf("Shook %s (%d chars, ~%d tokens freed).", strings.Join(parts, " + "), r.CharsFreed, r.TokensFreed)
	}
}

// shakeCompletions provides tab-completion items for `/shake <mode>`.
func shakeCompletions(prefix string) ([]sdk.AutocompleteItem, error) {
	all := []sdk.AutocompleteItem{
		{Value: "elide", Label: "elide", Description: "Elide large (>8 KB) older tool results (default)"},
		{Value: "images", Label: "images", Description: "Strip inline images from older messages"},
		{Value: "thinking", Label: "thinking", Description: "Drop thinking blocks from older assistant messages"},
		{Value: "all", Label: "all", Description: "Elide large tool results, strip older images and thinking blocks"},
	}
	p := strings.ToLower(strings.TrimSpace(prefix))
	if p == "" {
		return all, nil
	}
	var out []sdk.AutocompleteItem
	for _, item := range all {
		if strings.HasPrefix(item.Value, p) {
			out = append(out, item)
		}
	}
	return out, nil
}

// shakeState tracks per-session shake flags and pending durable context_edit drafts.
type shakeState struct {
	elide    bool
	images   bool
	thinking bool

	// pendingEdits holds durable `context_edit` boundary drafts queued by `/shake`
	// (or superseded-read detection) for emission at the next EventTurnEnd.
	// Keyed by targetId so duplicate edits coalesce (latest wins).
	pendingEdits map[string]map[string]any
	pendingOrder []string

	// spillPaths caches spill URLs by content fingerprint so repeated EventContext
	// passes do not write duplicate spill files for the same tool result.
	spillPaths map[string]string
}

func (s *shakeState) active() bool {
	return s != nil && (s.elide || s.images || s.thinking)
}

func (s *shakeState) arm(mode ShakeMode) {
	switch mode {
	case ShakeModeElide:
		s.elide = true
	case ShakeModeImages:
		s.images = true
	case ShakeModeThinking:
		s.thinking = true
	case ShakeModeAll:
		s.elide = true
		s.images = true
		s.thinking = true
	}
}

func (s *shakeState) queueEdit(targetID string, replacementContent any) {
	if targetID == "" {
		return
	}
	if s.pendingEdits == nil {
		s.pendingEdits = make(map[string]map[string]any)
	}
	if _, exists := s.pendingEdits[targetID]; !exists {
		s.pendingOrder = append(s.pendingOrder, targetID)
	}
	s.pendingEdits[targetID] = map[string]any{
		"type":     "context_edit",
		"targetId": targetID,
		"replacement": map[string]any{
			"content": replacementContent,
		},
	}
}

func (s *shakeState) takePendingEdits() []any {
	if len(s.pendingOrder) == 0 {
		return nil
	}
	out := make([]any, 0, len(s.pendingOrder))
	for _, id := range s.pendingOrder {
		if draft, ok := s.pendingEdits[id]; ok {
			out = append(out, draft)
		}
	}
	s.pendingEdits = nil
	s.pendingOrder = nil
	return out
}

func (m *manager) registerShake(ext *sdk.Extension) {
	ext.RegisterCommand("shake", sdk.CommandOptions{
		Description:            "Drop heavy content from context (elide, images, thinking, all)",
		GetArgumentCompletions: shakeCompletions,
		Handler: func(ctx sdk.Context, args string) error {
			m.handleShakeCommand(ctx, args)
			return nil
		},
	})
}

func (m *manager) handleShakeInput(ctx sdk.Context, trimmed string) (bool, map[string]any) {
	if trimmed != "/shake" && !strings.HasPrefix(trimmed, "/shake ") {
		return false, nil
	}
	args := ""
	if len(trimmed) > len("/shake") {
		args = strings.TrimSpace(trimmed[len("/shake"):])
	}
	m.handleShakeCommand(ctx, args)
	return true, map[string]any{"action": "handled"}
}

func (m *manager) handleShakeCommand(ctx sdk.Context, args string) {
	mode, errMsg := ParseShakeMode(args)
	if errMsg != "" {
		ctx.Notify(errMsg, "error")
		return
	}
	sid := sessionIDFor(ctx)
	m.mu.Lock()
	st := m.stateLocked(sid)
	st.shake.arm(mode)
	m.mu.Unlock()

	res := m.planShakeOnBranch(ctx, sid, mode)
	if res.CharsFreed > 0 || res.ImagesDropped > 0 || res.ThinkingBlocksDropped > 0 || res.ToolResultsElided > 0 || res.SupersededReadsElided > 0 {
		ctx.Notify(FormatShakeSummary(res), "info")
		return
	}
	// Branch may be empty or unavailable in headless tests; still confirm mode is armed for outgoing context.
	ctx.Notify(FormatShakeSummary(res)+" Active for outgoing context.", "info")
}

// branchEntryRecord pairs an entry ID on the active branch with its raw decoded message map.
type branchEntryRecord struct {
	id      string
	typ     string
	message map[string]any
	// customContent is set for custom_message entries whose content is an array.
	customContent any
}

// loadActiveBranchRecords loads the active branch's raw entries (post-latest-compaction
// when a compaction exists) so shake can inspect full content blocks and attach exact targetIds.
func loadActiveBranchRecords(ctx sdk.Context) []branchEntryRecord {
	branch, err := ctx.GetBranch()
	if err != nil || len(branch) == 0 {
		return nil
	}
	rawEntries, err := ctx.GetEntries()
	byID := make(map[string]map[string]any, len(rawEntries))
	if err == nil && len(rawEntries) > 0 {
		for _, raw := range rawEntries {
			var obj map[string]any
			if json.Unmarshal(raw, &obj) != nil {
				continue
			}
			if id, _ := obj["id"].(string); id != "" {
				byID[id] = obj
			}
		}
	}

	// Find latest compaction boundary so we never rewrite summarized-away history.
	startIdx := 0
	for i := len(branch) - 1; i >= 0; i-- {
		if branch[i].Type == "compaction" {
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

	// Collect latest context_edit per targetId (mirrors PiG session_manager_projection.go:211-221)
	// so omitted (replacement: null) entries are never resurrected and prior replacements stay intact.
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
		rawObj, ok := byID[be.ID]
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

	records := make([]branchEntryRecord, 0, len(branch)-startIdx)
	for i := startIdx; i < len(branch); i++ {
		be := branch[i]
		if be.Type == "compaction" || be.Type == "context_edit" {
			continue
		}
		ov, hasEdit := edits[be.ID]
		if hasEdit && ov.omitted {
			continue
		}
		if rawObj, ok := byID[be.ID]; ok {
			if be.Type == "message" {
				if msg, ok := rawObj["message"].(map[string]any); ok {
					if hasEdit {
						cloned := make(map[string]any, len(msg))
						for k, v := range msg {
							cloned[k] = v
						}
						cloned["content"] = ov.content
						msg = cloned
					}
					records = append(records, branchEntryRecord{
						id:      be.ID,
						typ:     "message",
						message: msg,
					})
					continue
				}
			} else if be.Type == "custom_message" {
				content := rawObj["content"]
				if hasEdit {
					content = ov.content
				}
				records = append(records, branchEntryRecord{
					id:            be.ID,
					typ:           "custom_message",
					customContent: content,
				})
				continue
			}
		}
		// Fallback to flattened sdk.BranchEntry when GetEntries wasn't populated (e.g. unit harness).
		if be.Type == "message" && be.Role != "" {
			msg := branchEntryToMessageMap(be)
			if hasEdit {
				msg["content"] = ov.content
			}
			records = append(records, branchEntryRecord{
				id:      be.ID,
				typ:     "message",
				message: msg,
			})
		}
	}
	return records
}

func branchEntryToMessageMap(be sdk.BranchEntry) map[string]any {
	msg := map[string]any{
		"role": be.Role,
	}
	if be.ToolName != "" {
		msg["toolName"] = be.ToolName
	}
	if be.ToolCallID != "" {
		msg["toolCallId"] = be.ToolCallID
	}
	if be.IsError {
		msg["isError"] = true
	}
	switch be.Role {
	case "assistant":
		var blocks []any
		if be.Thinking != "" {
			blocks = append(blocks, map[string]any{"type": "thinking", "thinking": be.Thinking})
		}
		if be.Content != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": be.Content})
		}
		for _, tc := range be.ToolCalls {
			var parsedArgs any
			if tc.Args != "" {
				_ = json.Unmarshal([]byte(tc.Args), &parsedArgs)
			}
			blocks = append(blocks, map[string]any{
				"type":      "toolCall",
				"id":        tc.ID,
				"name":      tc.Name,
				"arguments": parsedArgs,
			})
		}
		msg["content"] = blocks
	case "toolResult":
		msg["content"] = []any{map[string]any{"type": "text", "text": be.Content}}
	default:
		msg["content"] = be.Content
	}
	return msg
}

// planShakeOnBranch inspects the active branch, computes reclaimed chars/tokens,
// and queues durable `context_edit` drafts for the next EventTurnEnd.
func (m *manager) planShakeOnBranch(ctx sdk.Context, sid string, mode ShakeMode) ShakeResult {
	res := ShakeResult{Mode: mode}
	records := loadActiveBranchRecords(ctx)
	if len(records) == 0 {
		return res
	}

	msgs := make([]map[string]any, len(records))
	for i, rec := range records {
		if rec.message != nil {
			msgs[i] = cloneMessageShallow(rec.message)
		} else if rec.typ == "custom_message" {
			msgs[i] = map[string]any{
				"role":    "custom",
				"content": rec.customContent,
			}
		}
	}

	wantElide := mode == ShakeModeElide || mode == ShakeModeAll
	wantImages := mode == ShakeModeImages || mode == ShakeModeAll
	wantThinking := mode == ShakeModeThinking || mode == ShakeModeAll
	wantSupersede := wantElide && resolveSupersedeReads(ctx)

	edited := make(map[int]any) // index -> replacement content

	if wantImages {
		n, chars, changed := stripOlderImages(msgs)
		res.ImagesDropped += n
		res.CharsFreed += chars
		for idx, content := range changed {
			edited[idx] = content
		}
	}
	if wantThinking {
		n, chars, changed := stripOlderThinking(msgs)
		res.ThinkingBlocksDropped += n
		res.CharsFreed += chars
		for idx, content := range changed {
			edited[idx] = content
		}
	}
	if wantSupersede {
		n, chars, changed := elideSupersededReads(msgs)
		res.SupersededReadsElided += n
		res.CharsFreed += chars
		for idx, content := range changed {
			edited[idx] = content
		}
	}
	if wantElide {
		n, chars, changed := m.elideLargeToolResults(ctx, sid, msgs)
		res.ToolResultsElided += n
		res.CharsFreed += chars
		for idx, content := range changed {
			edited[idx] = content
		}
	}

	res.TokensFreed = estimateTokensFromChars(res.CharsFreed)

	if len(edited) > 0 {
		m.mu.Lock()
		st := m.stateLocked(sid)
		for i := range records {
			rep, ok := edited[i]
			if !ok || records[i].id == "" {
				continue
			}
			// Only user, assistant, toolResult, custom_message are valid PiG context_edit targets.
			if !isEditableRecord(records[i]) {
				continue
			}
			st.shake.queueEdit(records[i].id, rep)
			res.ContextEditsQueued++
		}
		m.mu.Unlock()
	}
	return res
}

func isEditableRecord(rec branchEntryRecord) bool {
	if rec.id == "" {
		return false
	}
	if rec.typ == "custom_message" {
		return true
	}
	if rec.typ == "message" && rec.message != nil {
		switch strField(rec.message, "role") {
		case "user", "assistant", "toolResult":
			return true
		}
	}
	return false
}

// applyShakeOnContext applies armed `/shake` modes (and automatic superseded-read
// elision on longer sessions) directly to outgoing EventContext messages in-place.
//
// Safety: when no shake mode is armed AND len(messages) <= supersedeReadsMinMessages,
// returns immediately with zero allocations and zero side effects.
func (m *manager) applyShakeOnContext(ctx sdk.Context, messages []any, protectedSummaryIdx int) bool {
	// Zero-RPC fast path: if no session has armed shake and message count is at or
	// below supersedeReadsMinMessages, skip GetSessionID and return immediately.
	m.mu.Lock()
	anyArmed := false
	for _, st := range m.sessions {
		if st.shake.active() {
			anyArmed = true
			break
		}
	}
	m.mu.Unlock()

	var sid string
	var elideOn, imagesOn, thinkingOn bool
	if anyArmed {
		sid = sessionIDFor(ctx)
		m.mu.Lock()
		if st, ok := m.sessions[sid]; ok {
			elideOn = st.shake.elide
			imagesOn = st.shake.images
			thinkingOn = st.shake.thinking
		}
		m.mu.Unlock()
	}

	explicitShake := elideOn || imagesOn || thinkingOn
	if !explicitShake {
		return false
	}
	msgMaps := make([]map[string]any, len(messages))
	for i, raw := range messages {
		if mm, ok := raw.(map[string]any); ok {
			msgMaps[i] = mm
		}
	}

	mutated := false
	if imagesOn {
		n, _, changed := stripOlderImagesSkipping(msgMaps, protectedSummaryIdx)
		if n > 0 {
			for idx, content := range changed {
				msgMaps[idx]["content"] = content
			}
			mutated = true
		}
	}
	if thinkingOn {
		n, _, changed := stripOlderThinking(msgMaps)
		if n > 0 {
			for idx, content := range changed {
				msgMaps[idx]["content"] = content
			}
			mutated = true
		}
	}
	if elideOn && resolveSupersedeReads(ctx) {
		n, _, changed := elideSupersededReads(msgMaps)
		if n > 0 {
			for idx, content := range changed {
				msgMaps[idx]["content"] = content
			}
			mutated = true
		}
	}
	if elideOn {
		n, _, changed := m.elideLargeToolResults(ctx, sid, msgMaps)
		if n > 0 {
			for idx, content := range changed {
				msgMaps[idx]["content"] = content
			}
			mutated = true
		}
	}
	return mutated
}

func resolveSupersedeReads(ctx sdk.Context) bool {
	if Setting == nil {
		return true
	}
	raw := Setting(ctx, "compaction.supersedeReads")
	if raw == nil {
		return true
	}
	if b, ok := raw.(bool); ok {
		return b
	}
	return true
}

func cloneMessageShallow(m map[string]any) map[string]any {
	cp := make(map[string]any, len(m))
	maps.Copy(cp, m)
	return cp
}

func estimateTokensFromChars(chars int) int {
	if chars <= 0 {
		return 0
	}
	return (chars + 3) / 4
}

// stripOlderImages removes inline `image` blocks from all messages except the newest
// user message (keeping the newest user message's images intact).
func stripOlderImages(msgs []map[string]any) (int, int, map[int]any) {
	return stripOlderImagesSkipping(msgs, -1)
}

func stripOlderImagesSkipping(msgs []map[string]any, skipIdx int) (int, int, map[int]any) {
	newestUserIdx := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i] == nil || i == skipIdx {
			continue
		}
		if strField(msgs[i], "role") == "user" {
			newestUserIdx = i
			break
		}
	}

	dropped := 0
	charsFreed := 0
	changed := map[int]any{}

	for i, msg := range msgs {
		if msg == nil || i == skipIdx || i == newestUserIdx {
			continue
		}
		role := strField(msg, "role")
		if newestUserIdx >= 0 && i > newestUserIdx && role == "toolResult" {
			continue
		}
		if role != "user" && role != "toolResult" && role != "custom" {
			continue
		}
		blocks, ok := msg["content"].([]any)
		if !ok || len(blocks) == 0 {
			continue
		}
		kept := make([]any, 0, len(blocks))
		msgDropped := 0
		for _, rawB := range blocks {
			bm, ok := rawB.(map[string]any)
			if ok && strField(bm, "type") == "image" {
				msgDropped++
				charsFreed += estimateImageBlockChars(bm)
				continue
			}
			kept = append(kept, rawB)
		}
		if msgDropped == 0 {
			continue
		}
		if len(kept) == 0 {
			kept = []any{map[string]any{"type": "text", "text": "[image removed]"}}
		}
		dropped += msgDropped
		changed[i] = kept
		msg["content"] = kept
	}
	return dropped, charsFreed, changed
}

func estimateImageBlockChars(bm map[string]any) int {
	if data, ok := bm["data"].(string); ok && len(data) > 0 {
		// Base64 image payloads typically translate to ~1000-1600 tokens (~4000+ chars);
		// use len(data) with a floor of 4000 chars so token estimates reflect real savings.
		if len(data) > 4000 {
			return len(data)
		}
		return 4000
	}
	return 4000
}

// stripOlderThinking removes `thinking` and `redactedThinking` blocks from older
// assistant messages (preserving the latest assistant message when no newer user
// message follows it).
func stripOlderThinking(msgs []map[string]any) (int, int, map[int]any) {
	lastUserIdx := -1
	lastAssistantIdx := -1
	for i, msg := range msgs {
		if msg == nil {
			continue
		}
		switch strField(msg, "role") {
		case "user":
			lastUserIdx = i
		case "assistant":
			lastAssistantIdx = i
		}
	}

	dropped := 0
	charsFreed := 0
	changed := map[int]any{}

	for i, msg := range msgs {
		if msg == nil || strField(msg, "role") != "assistant" {
			continue
		}
		// An assistant turn is "older" if another assistant turn or user turn follows it.
		if i == lastAssistantIdx && i > lastUserIdx {
			continue
		}
		blocks, ok := msg["content"].([]any)
		if !ok || len(blocks) == 0 {
			continue
		}
		kept := make([]any, 0, len(blocks))
		msgDropped := 0
		for _, rawB := range blocks {
			bm, ok := rawB.(map[string]any)
			if ok {
				bt := strField(bm, "type")
				if bt == "thinking" || bt == "redactedThinking" {
					msgDropped++
					if t, ok := bm["thinking"].(string); ok {
						charsFreed += len(t)
					} else if t, ok := bm["text"].(string); ok {
						charsFreed += len(t)
					} else if d, ok := bm["data"].(string); ok {
						charsFreed += len(d)
					}
					continue
				}
			}
			kept = append(kept, rawB)
		}
		if msgDropped == 0 {
			continue
		}
		dropped += msgDropped
		changed[i] = kept
		msg["content"] = kept
	}
	return dropped, charsFreed, changed
}

// elideLargeToolResults replaces large (>8 KB) older toolResult text bodies with a
// compact head/tail preview and an optional spill path returned by WriteSpill.
func (m *manager) elideLargeToolResults(ctx sdk.Context, sid string, msgs []map[string]any) (int, int, map[int]any) {
	// A toolResult is "older" when at least one user or assistant message appears after it.
	lastBoundaryIdx := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i] == nil {
			continue
		}
		role := strField(msgs[i], "role")
		if role == "user" || role == "assistant" {
			lastBoundaryIdx = i
			break
		}
	}

	elided := 0
	charsFreed := 0
	changed := map[int]any{}

	for i, msg := range msgs {
		if msg == nil || i >= lastBoundaryIdx {
			continue
		}
		if strField(msg, "role") != "toolResult" {
			continue
		}
		toolName := strField(msg, "toolName")
		if isProtectedShakeTool(toolName) {
			continue
		}
		origText, nonTextBlocks := extractToolResultTextAndNonText(msg)
		if len(origText) <= shakeElideMinBytes {
			continue
		}
		if isAlreadyElidedText(origText) {
			continue
		}

		spillPath := m.maybeWriteShakeSpill(ctx, sid, toolName, origText)
		preview := formatElidedToolResult(toolName, origText, spillPath)
		if len(preview) >= len(origText) {
			continue
		}

		newBlocks := make([]any, 0, 1+len(nonTextBlocks))
		newBlocks = append(newBlocks, map[string]any{"type": "text", "text": preview})
		newBlocks = append(newBlocks, nonTextBlocks...)

		elided++
		charsFreed += len(origText) - len(preview)
		changed[i] = newBlocks
		msg["content"] = newBlocks
	}
	return elided, charsFreed, changed
}

func isProtectedShakeTool(toolName string) bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "skill", "ask", "propose":
		return true
	}
	return false
}

func isAlreadyElidedText(text string) bool {
	return strings.Contains(text, "[…shook ") || strings.HasPrefix(text, "[Superseded by a later read")
}

func extractToolResultTextAndNonText(msg map[string]any) (string, []any) {
	switch c := msg["content"].(type) {
	case string:
		return c, nil
	case []any:
		var textParts []string
		var nonText []any
		for _, rawB := range c {
			bm, ok := rawB.(map[string]any)
			if !ok {
				nonText = append(nonText, rawB)
				continue
			}
			if strField(bm, "type") == "text" {
				if t, ok := bm["text"].(string); ok {
					textParts = append(textParts, t)
				}
			} else {
				nonText = append(nonText, rawB)
			}
		}
		return strings.Join(textParts, "\n"), nonText
	default:
		return "", nil
	}
}

func (m *manager) maybeWriteShakeSpill(ctx sdk.Context, sid, label, content string) string {
	if WriteSpill == nil {
		return ""
	}
	fp := shakeContentFingerprint(label, content)
	if sid != "" {
		m.mu.Lock()
		st := m.stateLocked(sid)
		if p, ok := st.shake.spillPaths[fp]; ok {
			m.mu.Unlock()
			return p
		}
		m.mu.Unlock()
	}
	if label == "" {
		label = "tool-result"
	}
	path := strings.TrimSpace(WriteSpill(ctx, label, content))
	if path != "" && sid != "" {
		m.mu.Lock()
		st := m.stateLocked(sid)
		if st.shake.spillPaths == nil {
			st.shake.spillPaths = make(map[string]string)
		}
		st.shake.spillPaths[fp] = path
		m.mu.Unlock()
	}
	return path
}

func shakeContentFingerprint(label, content string) string {
	n := len(content)
	head := content
	if len(head) > 64 {
		head = head[:64]
	}
	tail := content
	if len(tail) > 64 {
		tail = tail[len(tail)-64:]
	}
	return label + ":" + strconv.Itoa(n) + ":" + head + "|" + tail
}

func formatElidedToolResult(toolName, origText, spillPath string) string {
	runes := []rune(origText)
	headLen := min(shakePreviewHeadChars, len(runes))
	tailLen := min(shakePreviewTailChars, len(runes)-headLen)
	head := string(runes[:headLen])
	tail := ""
	if tailLen > 0 {
		tail = string(runes[len(runes)-tailLen:])
	}
	omittedChars := len(runes) - headLen - tailLen
	label := toolName
	if label == "" {
		label = "toolResult"
	}
	var marker string
	if spillPath != "" {
		marker = fmt.Sprintf("[…shook %s: %d chars elided; full output saved to %s…]", label, omittedChars, spillPath)
	} else {
		marker = fmt.Sprintf("[…shook %s: %d chars elided…]", label, omittedChars)
	}
	if tail == "" {
		return head + "\n" + marker
	}
	return head + "\n" + marker + "\n" + tail
}

// ── Superseded Read Elision (`compaction.supersedeReads`) ───────────────────

type readCallInfo struct {
	key  string // canonical path + range key
	path string // normalized file path without range selector
}

// elideSupersededReads conservatively elides an earlier `read` toolResult ONLY
// when the exact same file+range was read again later in the same session and no
// write/edit/bash mutation intervened between the two reads.
// upstream: packages/agent/src/compaction/pruning.ts readToolSupersedeKey
func elideSupersededReads(msgs []map[string]any) (int, int, map[int]any) {
	callsByID := map[string]readCallInfo{}
	// activeByKey maps readKey -> index of the most recent un-invalidated toolResult for that key.
	activeByKey := map[string]int{}
	// activeKeyPath tracks the base file path for each key in activeByKey.
	activeKeyPath := map[string]string{}
	supersededIdx := map[int]string{} // toolResult msg index -> display path

	for i, msg := range msgs {
		if msg == nil {
			continue
		}
		role := strField(msg, "role")
		switch role {
		case "assistant":
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
				switch name {
				case "read":
					id := strField(bm, "id")
					if id == "" {
						continue
					}
					if info, ok := parseReadSupersedeKey(args); ok {
						callsByID[id] = info
					}
				case "write", "edit", "hashline_edit", "ast_edit", "notebook_edit":
					targetPath := normalizeReadBasePath(strField(args, "path"))
					if targetPath == "" {
						// Unknown path: conservatively clear all tracked reads.
						clear(activeByKey)
						clear(activeKeyPath)
					} else {
						for k, p := range activeKeyPath {
							if p == targetPath {
								delete(activeByKey, k)
								delete(activeKeyPath, k)
							}
						}
					}
				case "bash", "shell", "python":
					// Shell / script execution may mutate any file on disk.
					clear(activeByKey)
					clear(activeKeyPath)
				}
			}
		case "toolResult":
			if isErr, _ := msg["isError"].(bool); isErr {
				continue
			}
			tcID := strField(msg, "toolCallId")
			if tcID == "" {
				continue
			}
			info, ok := callsByID[tcID]
			if !ok {
				continue
			}
			if prevIdx, exists := activeByKey[info.key]; exists {
				supersededIdx[prevIdx] = info.path
			}
			activeByKey[info.key] = i
			activeKeyPath[info.key] = info.path
		}
	}

	if len(supersededIdx) == 0 {
		return 0, 0, nil
	}

	elided := 0
	charsFreed := 0
	changed := map[int]any{}
	for idx, path := range supersededIdx {
		msg := msgs[idx]
		origText, nonText := extractToolResultTextAndNonText(msg)
		if len(origText) == 0 || isAlreadyElidedText(origText) {
			continue
		}
		placeholder := fmt.Sprintf("[Superseded by a later read of %s]", path)
		if len(placeholder) >= len(origText) {
			continue
		}
		newBlocks := make([]any, 0, 1+len(nonText))
		newBlocks = append(newBlocks, map[string]any{"type": "text", "text": placeholder})
		newBlocks = append(newBlocks, nonText...)
		elided++
		charsFreed += len(origText) - len(placeholder)
		changed[idx] = newBlocks
		msg["content"] = newBlocks
	}
	return elided, charsFreed, changed
}

func parseReadSupersedeKey(args map[string]any) (readCallInfo, bool) {
	if args == nil {
		return readCallInfo{}, false
	}
	rawPath, _ := args["path"].(string)
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" || strings.Contains(rawPath, "://") {
		return readCallInfo{}, false
	}
	base, sel := splitReadPathSelector(rawPath)
	base = normalizeReadBasePath(base)
	if base == "" {
		return readCallInfo{}, false
	}
	// Include explicit offset/limit arguments if provided separately.
	offset, hasOffset := numberField(args, "offset")
	limit, hasLimit := numberField(args, "limit")
	var rangeKey strings.Builder
	rangeKey.WriteString(sel)
	if hasOffset {
		rangeKey.WriteString("|o=")
		rangeKey.WriteString(strconv.Itoa(offset))
	}
	if hasLimit {
		rangeKey.WriteString("|l=")
		rangeKey.WriteString(strconv.Itoa(limit))
	}
	key := base
	if rangeKey.Len() > 0 {
		key = base + "\x00" + rangeKey.String()
	}
	return readCallInfo{key: key, path: base}, true
}

func splitReadPathSelector(p string) (base, selector string) {
	// Match trailing :<selector> like :50-200, :10, :-5, :raw, :2-4:raw.
	// Skip Windows drive letter colons at index 1.
	idx := -1
	for i := len(p) - 1; i > 1; i-- {
		if p[i] == '/' || p[i] == '\\' {
			break
		}
		if p[i] == ':' {
			candidate := p[i+1:]
			if isReadSelectorToken(candidate) {
				idx = i
			} else {
				break
			}
		}
	}
	if idx > 0 {
		return p[:idx], p[idx+1:]
	}
	return p, ""
}

func isReadSelectorToken(s string) bool {
	if s == "" {
		return false
	}
	if s == "raw" || s == "conflicts" {
		return true
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= '0' && c <= '9') || c == '-' || c == '+' || c == ',' || c == ':' {
			continue
		}
		if strings.HasPrefix(s[i:], "raw") {
			i += 2
			continue
		}
		return false
	}
	return true
}

func normalizeReadBasePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	return filepath.Clean(p)
}
