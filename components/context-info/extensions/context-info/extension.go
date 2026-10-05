// Package contextinfo shows what fills the context window and what a session
// costs.
//
// Commands:
//
//	/context        context window composition and session info
//	/tools          all tools with active/inactive status and call counts
//	/cost           session cost breakdown with per-token pricing
//	/prompts        the main system prompt; /prompts agents lists agent definitions
//	/context-footer turn the two-line status footer on or off
//
// Shortcut: ctrl+shift+i shows the /context view.
//
// The status footer replaces PiG's footer, so it is off unless the
// --context-footer flag or /context-footer on turns it on. The rest of the
// extension only answers when asked.
package contextinfo

import (
	"cmp"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// ── ANSI color helpers ────────────────────────────────────────────────────────
// Palette: accent (cyan), muted (white), dim (gray), success (green),
// warning (yellow), error (red).

const (
	ansiReset   = "\033[0m"
	ansiBold    = "\033[1m"
	ansiDim     = "\033[2m"
	ansiCyan    = "\033[36m"
	ansiGreen   = "\033[32m"
	ansiYellow  = "\033[33m"
	ansiRed     = "\033[31m"
	ansiWhite   = "\033[37m"
	ansiMagenta = "\033[35m"
)

func accent(s string) string  { return ansiCyan + ansiBold + s + ansiReset }
func muted(s string) string   { return ansiWhite + s + ansiReset }
func dim(s string) string     { return ansiDim + s + ansiReset }
func success(s string) string { return ansiGreen + s + ansiReset }
func warning(s string) string { return ansiYellow + s + ansiReset }
func errClr(s string) string  { return ansiRed + s + ansiReset }

// ctxColor returns the colored context percentage string based on thresholds.
func ctxColor(pct float64, s string) string {
	if pct > 80 {
		return errClr(s)
	}
	if pct > 50 {
		return warning(s)
	}
	return success(s)
}

// ── State ─────────────────────────────────────────────────────────────────────

var (
	mu                sync.Mutex
	toolCallCounts    map[string]int
	totalToolCalls    int
	branchCountsReady bool

	// Self-calibrating chars/token ratio. Starts at 3.7 (reasonable for
	// mixed English + code). Calibrates against provider usage at branch-fetch
	// events (session start, resume, compaction, model switch) so a full branch
	// scan is never added to a hot per-turn path. Only affects the estimate
	// fallback shown when the host reports no live context usage.
	charsPerToken float64 = 3.7
	calibrated    bool

	// Per-message overhead: role markers, content-block wrappers, separators.
	msgOverheadTokens = 4
)

// ── Formatting helpers ────────────────────────────────────────────────────────

func fmtNum(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

func fmtCost(n float64) string {
	if n == 0 {
		return "$0.00"
	}
	if n < 0.01 {
		return fmt.Sprintf("$%.4f", n)
	}
	return fmt.Sprintf("$%.2f", n)
}

func commaFmt(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var buf strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			buf.WriteByte(',')
		}
		buf.WriteRune(c)
	}
	return buf.String()
}

func charsToTokens(chars int) int {
	return int(math.Ceil(float64(chars) / charsPerToken))
}

// ── System prompt analysis ────────────────────────────────────────────────────

type promptAnalysis struct {
	totalTokens int
	totalChars  int
	skillTokens int
	skillChars  int
	skillCount  int
	baseTokens  int
	baseChars   int
}

func analyzeSystemPrompt(prompt string) promptAnalysis {
	totalChars := len(prompt)
	totalTokens := charsToTokens(totalChars)

	// Count <skill>...</skill> blocks.
	var skillChars, skillCount int
	remaining := prompt
	for {
		start := strings.Index(remaining, "<skill")
		if start == -1 {
			break
		}
		end := strings.Index(remaining[start:], "</skill>")
		if end == -1 {
			break
		}
		end += start + len("</skill>")
		skillChars += end - start
		skillCount++
		remaining = remaining[end:]
	}
	skillTokens := charsToTokens(skillChars)
	baseChars := max(0, totalChars-skillChars)
	baseTokens := charsToTokens(baseChars)

	return promptAnalysis{
		totalTokens: totalTokens, totalChars: totalChars,
		skillTokens: skillTokens, skillChars: skillChars, skillCount: skillCount,
		baseTokens: baseTokens, baseChars: baseChars,
	}
}

// ── Conversation analysis ─────────────────────────────────────────────────────

type conversationAnalysis struct {
	textChars       int
	thinkingChars   int
	imageTokens     int
	imageCount      int
	messageCount    int
	toolCallCount   int
	toolResultCount int
	totalTokens     int
}

func analyzeConversation(branch []sdk.BranchEntry) conversationAnalysis {
	var a conversationAnalysis

	// ── Compaction-aware iteration ────────────────────────────────────────
	// Pi's buildSessionContext() sends to the LLM:
	//   1. compaction.summary, replacing entries before firstKeptEntryId
	//   2. kept entries from firstKeptEntryId up to the compaction marker
	//   3. entries after the compaction marker
	latestCompactionIdx := -1
	for i := len(branch) - 1; i >= 0; i-- {
		if branch[i].Type == "compaction" {
			latestCompactionIdx = i
			break
		}
	}
	iterStart := 0
	if latestCompactionIdx != -1 {
		comp := branch[latestCompactionIdx]
		a.textChars += len(comp.Content)
		a.messageCount++
		iterStart = latestCompactionIdx + 1
		if comp.FirstKeptEntryID != "" {
			for i := range branch {
				if branch[i].ID == comp.FirstKeptEntryID {
					iterStart = i
					break
				}
			}
		}
	}

	for i := iterStart; i < len(branch); i++ {
		e := branch[i]
		if i == latestCompactionIdx || e.Type != "message" {
			continue
		}
		a.messageCount++

		switch e.Role {
		case "user":
			a.textChars += len(e.Content)
		case "assistant":
			a.textChars += len(e.Content)
			a.thinkingChars += len(e.Thinking)
			a.toolCallCount += len(e.ToolCalls)
			for _, tc := range e.ToolCalls {
				a.textChars += len(tc.Name) + len(tc.Args)
			}
		case "toolResult":
			a.toolResultCount++
			a.textChars += len(e.ToolName) + len(e.ToolCallID) + len(e.Content)
		}
	}

	textTokens := charsToTokens(a.textChars)
	thinkingTokens := charsToTokens(a.thinkingChars)
	overheadTokens := a.messageCount * msgOverheadTokens
	a.totalTokens = textTokens + thinkingTokens + a.imageTokens + overheadTokens
	return a
}

// ── Tool definition token estimation ──────────────────────────────────────────

func estimateToolDefTokens(allTools []sdk.ToolInfo) (tokens, chars, activeCount, totalCount int) {
	totalCount = len(allTools)
	for _, t := range allTools {
		chars += len(t.Name) + len(t.Description)
	}
	activeCount = totalCount // we don't have active filter here, caller provides
	tokens = charsToTokens(chars)
	return
}

// ── Fallback cost table (GitHub Copilot) ──────────────────────────────────────
// A model whose registry entry carries no price (GitHub Copilot models are
// billed by subscription, not per token) would show $0.00. For those, /cost and
// the footer estimate what the tokens would cost at list price, from this table.
// Values are USD per 1M tokens, taken from
// https://docs.github.com/en/copilot/reference/copilot-billing/models-and-pricing
// as of 2025-06-01. They are estimates, not a bill, and will drift; models missing
// from the table are shown unpriced. Any model whose registry entry has a price
// uses that price instead.

type modelCost struct {
	Input, Output, CacheRead, CacheWrite float64
}

var copilotCostTable = map[string]modelCost{
	// Anthropic  (cacheWrite = 5-min cache = 1.25× base; cacheRead = 0.1× base)
	"claude-haiku-4.5":  {Input: 1.00, Output: 5.00, CacheRead: 0.10, CacheWrite: 1.25},
	"claude-sonnet-4":   {Input: 3.00, Output: 15.00, CacheRead: 0.30, CacheWrite: 3.75},
	"claude-sonnet-4.5": {Input: 3.00, Output: 15.00, CacheRead: 0.30, CacheWrite: 3.75},
	"claude-sonnet-4.6": {Input: 3.00, Output: 15.00, CacheRead: 0.30, CacheWrite: 3.75},
	"claude-opus-4.5":   {Input: 5.00, Output: 25.00, CacheRead: 0.50, CacheWrite: 6.25},
	"claude-opus-4.6":   {Input: 5.00, Output: 25.00, CacheRead: 0.50, CacheWrite: 6.25},
	"claude-opus-4.7":   {Input: 5.00, Output: 25.00, CacheRead: 0.50, CacheWrite: 6.25},
	"claude-opus-4.8":   {Input: 5.00, Output: 25.00, CacheRead: 0.50, CacheWrite: 6.25},
	// Google  (no cache write charges)
	"gemini-2.5-pro":         {Input: 1.25, Output: 10.00, CacheRead: 0.125},
	"gemini-3-flash-preview": {Input: 0.50, Output: 3.00, CacheRead: 0.05},
	"gemini-3.1-pro-preview": {Input: 2.00, Output: 12.00, CacheRead: 0.20},
	"gemini-3.5-flash":       {Input: 1.50, Output: 9.00, CacheRead: 0.15},
	// OpenAI  (no cache write charges)
	"gpt-4.1":       {Input: 2.00, Output: 8.00, CacheRead: 0.50},
	"gpt-4o":        {Input: 2.50, Output: 10.00, CacheRead: 1.25},
	"gpt-5-mini":    {Input: 0.25, Output: 2.00, CacheRead: 0.025},
	"gpt-5.2":       {Input: 1.75, Output: 14.00, CacheRead: 0.175},
	"gpt-5.2-codex": {Input: 1.75, Output: 14.00, CacheRead: 0.175},
	"gpt-5.3-codex": {Input: 1.75, Output: 14.00, CacheRead: 0.175},
	"gpt-5.4":       {Input: 2.50, Output: 15.00, CacheRead: 0.25},
	"gpt-5.4-mini":  {Input: 0.75, Output: 4.50, CacheRead: 0.075},
	"gpt-5.4-nano":  {Input: 0.20, Output: 1.25, CacheRead: 0.02},
	"gpt-5.5":       {Input: 5.00, Output: 30.00, CacheRead: 0.50},
	// GitHub fine-tuned
	"raptor-mini": {Input: 0.25, Output: 2.00, CacheRead: 0.025},
}

// patchModelCosts fills zeroed-out cost fields from the fallback table.
// Returns a shallow copy so the original ModelInfo is not mutated.
func patchModelCosts(m *sdk.ModelInfo) *sdk.ModelInfo {
	if m == nil || m.InputCostPer1M > 0 {
		return m // already has pricing or nil
	}
	if m.Provider != "github-copilot" {
		return m
	}
	c, ok := copilotCostTable[normalizeModelID(m.ID)]
	if !ok {
		return m
	}
	patched := *m // shallow copy
	patched.InputCostPer1M = c.Input
	patched.OutputCostPer1M = c.Output
	patched.CacheReadCostPer1M = c.CacheRead
	patched.CacheWriteCostPer1M = c.CacheWrite
	return &patched
}

// getCostRatesForModelID resolves per-1M-token rates for a model.
// Returns rates from the fallback table (or zero if unknown).
func normalizeModelID(modelID string) string {
	if before, after, ok := strings.Cut(modelID, "/"); ok {
		_ = before
		return after
	}
	return modelID
}

func modelIDForCost(modelID string, currentModel *sdk.ModelInfo) string {
	if modelID == "" || modelID == "unknown" {
		if currentModel != nil && currentModel.ID != "" {
			return currentModel.ID
		}
		return modelID
	}
	return normalizeModelID(modelID)
}

func getCostRatesForModelID(modelID string) modelCost {
	if c, ok := copilotCostTable[modelID]; ok {
		return c
	}
	if c, ok := copilotCostTable[normalizeModelID(modelID)]; ok {
		return c
	}
	return modelCost{}
}

// ── Cost data from session branch ─────────────────────────────────────────────

type costBucket struct {
	input, output, cacheRead, cacheWrite, totalTokens int
	cost                                              struct {
		input, output, cacheRead, cacheWrite, total float64
	}
}

type modelCostEntry struct {
	modelID                                           string
	msgs                                              int
	input, output, cacheRead, cacheWrite, totalTokens int
	cost                                              struct {
		input, output, cacheRead, cacheWrite, total float64
	}
}

type costData struct {
	input, output, cacheRead, cacheWrite, totalTokens int
	cost                                              struct {
		input, output, cacheRead, cacheWrite, total float64
	}
	byModel []modelCostEntry
}

// Cost data cache — avoids re-iterating all branch entries on every TUI frame.
type liveContextUsage struct {
	tokens        int
	percent       float64
	contextWindow int
	stale         bool
}

var (
	costCacheMu        sync.Mutex
	gitCacheMu         sync.Mutex
	costCacheResult    *costData
	costCacheBranchLen int = -1
	costCacheLeafID    string
	costCacheModelKey  string
	lastUsage          *liveContextUsage
	gitCacheCWD        string
	gitCacheBranch     string
	gitCacheAt         time.Time
)

func latestLeafID(branch []sdk.BranchEntry) string {
	if len(branch) == 0 {
		return ""
	}
	return branch[len(branch)-1].ID
}

func invalidateCostCache() {
	costCacheMu.Lock()
	costCacheResult = nil
	costCacheBranchLen = -1
	costCacheLeafID = ""
	costCacheModelKey = ""
	costCacheMu.Unlock()
}

func cachedSessionCostData() (costData, bool) {
	costCacheMu.Lock()
	defer costCacheMu.Unlock()
	if costCacheResult == nil {
		return costData{}, false
	}
	return *costCacheResult, true
}

func addAssistantUsageToCostCache(modelID string, usage sdk.UsageInfo, currentModel *sdk.ModelInfo) {
	if usage.Input == 0 && usage.Output == 0 && usage.CacheRead == 0 && usage.CacheWrite == 0 && usage.TotalTokens == 0 {
		return
	}
	mid := modelIDForCost(modelID, currentModel)
	if mid == "" {
		mid = "unknown"
	}
	rates := ratesForModel(mid, currentModel)
	modelKey := costCacheKey(currentModel)

	// Whole operation runs under the lock: handlers are dispatched on separate
	// goroutines (sdk loop: `go handleRequest`), so a concurrent message_end or
	// /cost read must never see a half-updated cache or share a mutated slice.
	costCacheMu.Lock()
	defer costCacheMu.Unlock()

	var cd costData
	if costCacheResult != nil {
		cd = *costCacheResult
		// Deep-copy byModel: the shallow struct copy above shares the backing
		// array with the published result; mutating it in place would corrupt
		// any snapshot a reader already returned.
		cd.byModel = append([]modelCostEntry(nil), costCacheResult.byModel...)
	}

	idx := -1
	for i := range cd.byModel {
		if cd.byModel[i].modelID == mid {
			idx = i
			break
		}
	}
	if idx == -1 {
		cd.byModel = append(cd.byModel, modelCostEntry{modelID: mid})
		idx = len(cd.byModel) - 1
	}

	entry := &cd.byModel[idx]
	entry.msgs++
	entry.input += usage.Input
	entry.output += usage.Output
	entry.cacheRead += usage.CacheRead
	entry.cacheWrite += usage.CacheWrite
	if usage.TotalTokens > 0 {
		entry.totalTokens += usage.TotalTokens
	} else {
		entry.totalTokens += usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
	}
	if rates.Input > 0 {
		entry.cost.input += float64(usage.Input) * rates.Input / 1_000_000
		entry.cost.output += float64(usage.Output) * rates.Output / 1_000_000
		entry.cost.cacheRead += float64(usage.CacheRead) * rates.CacheRead / 1_000_000
		entry.cost.cacheWrite += float64(usage.CacheWrite) * rates.CacheWrite / 1_000_000
		entry.cost.total = entry.cost.input + entry.cost.output + entry.cost.cacheRead + entry.cost.cacheWrite
	}

	cd.input += usage.Input
	cd.output += usage.Output
	cd.cacheRead += usage.CacheRead
	cd.cacheWrite += usage.CacheWrite
	if usage.TotalTokens > 0 {
		cd.totalTokens += usage.TotalTokens
	} else {
		cd.totalTokens += usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
	}
	cd.cost.input = 0
	cd.cost.output = 0
	cd.cost.cacheRead = 0
	cd.cost.cacheWrite = 0
	cd.cost.total = 0
	for _, b := range cd.byModel {
		cd.cost.input += b.cost.input
		cd.cost.output += b.cost.output
		cd.cost.cacheRead += b.cost.cacheRead
		cd.cost.cacheWrite += b.cost.cacheWrite
		cd.cost.total += b.cost.total
	}
	sort.Slice(cd.byModel, func(i, j int) bool {
		return cd.byModel[i].cost.total > cd.byModel[j].cost.total
	})

	costCacheResult = &cd
	costCacheModelKey = modelKey
	// The cache no longer matches any exact branch snapshot; force the next
	// getSessionCostData (cold event / command) to do a clean full rebuild
	// rather than trusting these incrementally-accumulated totals.
	costCacheBranchLen = -1
	costCacheLeafID = ""
}

func clearLastUsage() {
	costCacheMu.Lock()
	lastUsage = nil
	costCacheMu.Unlock()
}

func getLiveContextUsage(ctx sdk.Context) *liveContextUsage {
	usage := softContextUsage(ctx)
	// Pi reports tokens and percent as null right after compaction, until the
	// next response. That is "unknown", not zero: fall through to the last value.
	if usage != nil && usage.Tokens != nil && *usage.Tokens > 0 && usage.Percent != nil {
		current := &liveContextUsage{tokens: *usage.Tokens, percent: *usage.Percent, contextWindow: usage.ContextWindow}
		costCacheMu.Lock()
		lastUsage = &liveContextUsage{tokens: current.tokens, percent: current.percent, contextWindow: current.contextWindow, stale: false}
		costCacheMu.Unlock()
		return current
	}
	costCacheMu.Lock()
	defer costCacheMu.Unlock()
	if lastUsage == nil {
		return nil
	}
	stale := *lastUsage
	stale.stale = true
	return &stale
}

func costCacheKey(currentModel *sdk.ModelInfo) string {
	if currentModel == nil {
		return ""
	}
	return fmt.Sprintf("%s|%s|%.6f|%.6f|%.6f|%.6f", currentModel.Provider, currentModel.ID, currentModel.InputCostPer1M, currentModel.OutputCostPer1M, currentModel.CacheReadCostPer1M, currentModel.CacheWriteCostPer1M)
}

func ratesForModel(modelID string, currentModel *sdk.ModelInfo) modelCost {
	if currentModel != nil && normalizeModelID(currentModel.ID) == normalizeModelID(modelID) && currentModel.InputCostPer1M > 0 {
		return modelCost{Input: currentModel.InputCostPer1M, Output: currentModel.OutputCostPer1M, CacheRead: currentModel.CacheReadCostPer1M, CacheWrite: currentModel.CacheWriteCostPer1M}
	}
	return getCostRatesForModelID(modelID)
}

func getSessionCostData(branch []sdk.BranchEntry, currentModel *sdk.ModelInfo) costData {
	leafID := latestLeafID(branch)
	modelKey := costCacheKey(currentModel)
	costCacheMu.Lock()
	if costCacheResult != nil && len(branch) == costCacheBranchLen && leafID == costCacheLeafID && modelKey == costCacheModelKey {
		result := *costCacheResult
		costCacheMu.Unlock()
		return result
	}
	costCacheMu.Unlock()

	// Accumulate per-model buckets
	bucketMap := make(map[string]*modelCostEntry)
	var bucketOrder []string

	for _, e := range branch {
		if e.Role != "assistant" || e.Usage == nil {
			continue
		}
		mid := modelIDForCost(e.ModelID, currentModel)
		if mid == "" {
			mid = "unknown"
		}
		b, ok := bucketMap[mid]
		if !ok {
			b = &modelCostEntry{modelID: mid}
			bucketMap[mid] = b
			bucketOrder = append(bucketOrder, mid)
		}
		b.msgs++
		b.input += e.Usage.Input
		b.output += e.Usage.Output
		b.cacheRead += e.Usage.CacheRead
		b.cacheWrite += e.Usage.CacheWrite
		if e.Usage.TotalTokens > 0 {
			b.totalTokens += e.Usage.TotalTokens
		} else {
			b.totalTokens += e.Usage.Input + e.Usage.Output + e.Usage.CacheRead + e.Usage.CacheWrite
		}
	}

	// Calculate costs per model using per-model rates
	for _, mid := range bucketOrder {
		b := bucketMap[mid]
		rates := ratesForModel(mid, currentModel)
		if rates.Input > 0 {
			b.cost.input = float64(b.input) * rates.Input / 1_000_000
			b.cost.output = float64(b.output) * rates.Output / 1_000_000
			b.cost.cacheRead = float64(b.cacheRead) * rates.CacheRead / 1_000_000
			b.cost.cacheWrite = float64(b.cacheWrite) * rates.CacheWrite / 1_000_000
			b.cost.total = b.cost.input + b.cost.output + b.cost.cacheRead + b.cost.cacheWrite
		}
	}

	// Aggregate totals and sort by cost descending
	var d costData
	for _, mid := range bucketOrder {
		b := bucketMap[mid]
		d.input += b.input
		d.output += b.output
		d.cacheRead += b.cacheRead
		d.cacheWrite += b.cacheWrite
		d.totalTokens += b.totalTokens
		d.cost.input += b.cost.input
		d.cost.output += b.cost.output
		d.cost.cacheRead += b.cost.cacheRead
		d.cost.cacheWrite += b.cost.cacheWrite
		d.cost.total += b.cost.total
		d.byModel = append(d.byModel, *b)
	}

	// Sort by cost descending
	sort.Slice(d.byModel, func(i, j int) bool {
		return d.byModel[i].cost.total > d.byModel[j].cost.total
	})

	// Cache the result
	costCacheMu.Lock()
	costCacheResult = &d
	costCacheBranchLen = len(branch)
	costCacheLeafID = leafID
	costCacheModelKey = modelKey
	costCacheMu.Unlock()

	return d
}

func usageInt(m map[string]any, keys ...string) int {
	for _, key := range keys {
		switch v := m[key].(type) {
		case float64:
			return int(v)
		case int:
			return v
		case json.Number:
			if n, err := v.Int64(); err == nil {
				return int(n)
			}
		}
	}
	return 0
}

func usageInfoFromMap(m map[string]any) sdk.UsageInfo {
	return sdk.UsageInfo{
		Input:       usageInt(m, "input", "input_tokens", "InputTokens"),
		Output:      usageInt(m, "output", "output_tokens", "OutputTokens"),
		CacheRead:   usageInt(m, "cacheRead", "cache_read", "CacheRead"),
		CacheWrite:  usageInt(m, "cacheWrite", "cache_write", "CacheWrite"),
		TotalTokens: usageInt(m, "totalTokens", "total_tokens", "TotalTokens"),
	}
}

func assistantMapFromEvent(data map[string]any) (map[string]any, bool) {
	msg, ok := data["message"].(map[string]any)
	if !ok {
		return nil, false
	}
	if assistant, ok := msg["Assistant"].(map[string]any); ok {
		return assistant, true
	}
	if assistant, ok := msg["assistant"].(map[string]any); ok {
		return assistant, true
	}
	if role, _ := msg["role"].(string); role == "assistant" {
		return msg, true
	}
	return nil, false
}

func updateCostFromMessageEnd(ctx sdk.Context, data map[string]any) {
	assistant, ok := assistantMapFromEvent(data)
	if !ok {
		return
	}
	usageMap, ok := assistant["usage"].(map[string]any)
	if !ok {
		return
	}
	modelID, _ := assistant["model"].(string)
	if modelID == "" {
		modelID, _ = assistant["ModelID"].(string)
	}
	addAssistantUsageToCostCache(modelID, usageInfoFromMap(usageMap), softModelInfo(ctx))
}

// ── Calibration ───────────────────────────────────────────────────────────────

func calibrateFromBranch(ctx sdk.Context, branch []sdk.BranchEntry) {
	usage := softContextUsage(ctx)
	if usage == nil || usage.Tokens == nil || *usage.Tokens == 0 {
		return
	}
	tokens := *usage.Tokens

	prompt := softSystemPrompt(ctx)
	convo := analyzeConversation(branch)
	allTools := softAllTools(ctx)
	_, toolChars, _, _ := estimateToolDefTokens(allTools)

	totalTextChars := len(prompt) + toolChars + convo.textChars + convo.thinkingChars
	nonTextTokens := convo.imageTokens + (convo.messageCount+1)*msgOverheadTokens
	textTokensActual := max(1, tokens-nonTextTokens)

	if totalTextChars > 0 && textTokensActual > 100 {
		newRatio := float64(totalTextChars) / float64(textTokensActual)
		clamped := max(2.5, min(6.0, newRatio))

		mu.Lock()
		if !calibrated {
			charsPerToken = clamped
			calibrated = true
		} else {
			charsPerToken = charsPerToken*0.3 + clamped*0.7
		}
		mu.Unlock()
	}
}

// ── Tool count reconstruction (for resume) ───────────────────────────────────

func reconstructToolCountsFromBranch(branch []sdk.BranchEntry) {
	counts := make(map[string]int)
	total := 0
	for _, e := range branch {
		if e.Role != "assistant" {
			continue
		}
		for _, tc := range e.ToolCalls {
			name := tc.Name
			if name == "" {
				name = "unknown"
			}
			counts[name]++
			total++
		}
	}

	mu.Lock()
	toolCallCounts = counts
	totalToolCalls = total
	branchCountsReady = true
	mu.Unlock()
}

func reconstructInitialToolCounts(branch []sdk.BranchEntry) {
	reconstructToolCountsFromBranch(branch)
	markBranchCountsReady(len(branch) > 0)
}

func markBranchCountsReady(ready bool) {
	mu.Lock()
	branchCountsReady = ready
	mu.Unlock()
}

func needsBranchCountReconstruction() bool {
	mu.Lock()
	defer mu.Unlock()
	return !branchCountsReady
}

// ── Footer (replaces PiG's built-in footer while it is on) ───────────────────

func currentGitBranch(cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		return ""
	}
	gitCacheMu.Lock()
	if cwd == gitCacheCWD && time.Since(gitCacheAt) < 2*time.Second {
		branch := gitCacheBranch
		gitCacheMu.Unlock()
		return branch
	}
	gitCacheMu.Unlock()

	branch := readGitBranch(cwd)
	gitCacheMu.Lock()
	gitCacheCWD = cwd
	gitCacheBranch = branch
	gitCacheAt = time.Now()
	gitCacheMu.Unlock()
	return branch
}

func readGitBranch(cwd string) string {
	cmd := exec.Command("git", "-C", cwd, "branch", "--show-current")
	hideWindow(cmd)
	out, err := cmd.Output()
	if err == nil {
		if branch := strings.TrimSpace(string(out)); branch != "" {
			return branch
		}
	}
	cmd = exec.Command("git", "-C", cwd, "rev-parse", "--short", "HEAD")
	hideWindow(cmd)
	out, err = cmd.Output()
	if err != nil {
		return ""
	}
	sha := strings.TrimSpace(string(out))
	if sha == "" {
		return ""
	}
	return "detached@" + sha
}

func renderFooter(ctx sdk.Context) []string {
	return renderFooterWithBranch(ctx, nil)
}

func renderFooterWithBranch(ctx sdk.Context, branch []sdk.BranchEntry) []string {
	activeTools := softActiveTools(ctx)
	allTools := softAllTools(ctx)
	thinking := softThinkingLevel(ctx)
	modelName := ctx.Model()
	usage := getLiveContextUsage(ctx)
	ctxStr := formatContextStrWithBranch(ctx, usage, allTools, branch)

	if thinking == "" {
		thinking = "off"
	}

	// Retain what a re-layout needs so a resize does not repeat the four host
	// round-trips above. Each is a blocking call on the extension's message
	// loop, and a drag-resize delivers width_change continuously.
	storeFooterModel(footerModel{
		activeTools: len(activeTools),
		allTools:    len(allTools),
		thinking:    thinking,
		modelName:   modelName,
		ctxStr:      ctxStr,
	})

	mu.Lock()
	calMark := ""
	if calibrated {
		calMark = " " + dim("⊕")
	}
	calls := totalToolCalls
	mu.Unlock()

	sep := dim("│")

	// CWD prefix (like pi's footer)
	cwd := ctx.Cwd()
	home, _ := os.UserHomeDir()
	gitBranch := currentGitBranch(ctx.Cwd())
	if home != "" && strings.HasPrefix(cwd, home) {
		cwd = "~" + cwd[len(home):]
	}
	gitPart := ""
	if gitBranch != "" {
		gitPart = fmt.Sprintf(" %s %s %s %s", sep, accent("Git:"), muted(gitBranch), sep)
	}

	var cd costData
	if branch != nil {
		cd = getSessionCostData(branch, softModelInfo(ctx))
	} else if cached, ok := cachedSessionCostData(); ok {
		cd = cached
	}

	// ctx.Width() is the terminal width the host last reported. Fit to it here:
	// pig's setFooter takes static lines, so an over-wide line wraps and breaks
	// the renderer's one-line = one-row assumption.
	width := ctx.Width()

	// Line 1, ordered most to least valuable. cwd carries gitPart because the
	// separator that introduces the branch belongs with it.
	line1 := fitSegments([]string{
		muted(cwd) + gitPart,
		accent("Context:") + " " + ctxStr,
		accent("Model:") + " " + muted(modelName),
		accent("Think:") + " " + muted(thinking) + calMark,
	}, "  "+sep+" ", width)

	// Line 2: Cost + IO + Tools + Calls
	line2 := formatCostLine(cd, activeTools, allTools, calls, width)

	return []string{line1, line2}
}

// formatContextStr builds the context usage string used by both widget and footer.
func formatContextStr(ctx sdk.Context, usage *liveContextUsage, allTools []sdk.ToolInfo) string {
	return formatContextStrWithBranch(ctx, usage, allTools, nil)
}

func formatContextStrWithBranch(ctx sdk.Context, usage *liveContextUsage, allTools []sdk.ToolInfo, branch []sdk.BranchEntry) string {
	if usage != nil && usage.tokens > 0 && !usage.stale {
		pctStr := fmt.Sprintf("%.1f%%", usage.percent)
		detailStr := dim(fmt.Sprintf(" (%s/%s)", fmtNum(usage.tokens), fmtNum(usage.contextWindow)))
		return ctxColor(usage.percent, pctStr) + detailStr
	}
	if usage != nil && usage.tokens > 0 {
		pctStr := fmt.Sprintf("~%.1f%%", usage.percent)
		detailStr := dim(fmt.Sprintf(" (~%s/%s stale)", fmtNum(usage.tokens), fmtNum(usage.contextWindow)))
		return dim(pctStr) + detailStr
	}
	if branch == nil {
		return dim("…")
	}

	// Estimate from system prompt + tools + conversation.
	prompt := softSystemPrompt(ctx)
	sp := analyzeSystemPrompt(prompt)
	convo := analyzeConversation(branch)
	toolTokens, _, _, _ := estimateToolDefTokens(allTools)
	modelInfo := softModelInfo(ctx)
	ctxWin := 0
	if modelInfo != nil {
		ctxWin = modelInfo.ContextWindow
	}
	total := sp.totalTokens + toolTokens + convo.totalTokens + msgOverheadTokens
	if ctxWin > 0 && total > 0 {
		pct := float64(total) / float64(ctxWin) * 100
		pctStr := fmt.Sprintf("~%.1f%%", pct)
		detailStr := dim(fmt.Sprintf(" (~%s/%s est)", fmtNum(total), fmtNum(ctxWin)))
		return ctxColor(pct, pctStr) + detailStr
	}
	return dim("…")
}

// formatCostLine builds the second display line (cost, IO, tools, calls).
func formatCostLine(cd costData, activeTools []string, allTools []sdk.ToolInfo, calls int, width int) string {
	return formatCostLineCounts(cd, len(activeTools), len(allTools), calls, width)
}

// formatCostLineCounts is the layout, taking only the counts the line shows.
// Re-layout on resize calls this directly so it never has to hold or re-fetch
// the tool slices.
func formatCostLineCounts(cd costData, activeTools, allTools, calls int, width int) string {
	sep := dim("│")
	parts := []string{fmt.Sprintf("%s %s", accent("Cost:"), muted(fmtCost(cd.cost.total)))}
	if cd.input > 0 {
		parts = append(parts, accent("in:")+muted(fmtCost(cd.cost.input)))
	}
	if cd.output > 0 {
		parts = append(parts, accent("out:")+muted(fmtCost(cd.cost.output)))
	}
	if cd.cacheRead > 0 {
		parts = append(parts, accent("cR:")+muted(fmtCost(cd.cost.cacheRead)))
	}
	if cd.cacheWrite > 0 {
		parts = append(parts, accent("cW:")+muted(fmtCost(cd.cost.cacheWrite)))
	}

	tokenTotal := cd.input + cd.output + cd.cacheRead + cd.cacheWrite
	parts = append(parts, fmt.Sprintf("%s %s", accent("Total:"), muted(fmtNum(tokenTotal))))
	if cd.input > 0 {
		parts = append(parts, accent("↑")+muted(fmtNum(cd.input)))
	}
	if cd.output > 0 {
		parts = append(parts, accent("↓")+muted(fmtNum(cd.output)))
	}
	if cd.cacheRead > 0 {
		parts = append(parts, accent("R")+muted(fmtNum(cd.cacheRead)))
	}
	if cd.cacheWrite > 0 {
		parts = append(parts, accent("W")+muted(fmtNum(cd.cacheWrite)))
	}
	parts = append(parts,
		fmt.Sprintf("%s %s", accent("Tools:"), muted(fmt.Sprintf("%d/%d", activeTools, allTools))),
		fmt.Sprintf("%s %s", accent("Calls:"), muted(fmt.Sprintf("%d", calls))),
	)
	// Ordered most to least valuable: total cost first, per-token detail next,
	// raw token counts after that, and the tool/call tallies last.
	return fitSegments(parts, " "+sep+" ", width)
}

func updateFooter(ctx sdk.Context) {
	updateFooterWithBranch(ctx, nil)
}

func updateFooterWithBranch(ctx sdk.Context, branch []sdk.BranchEntry) {
	if !footerOn.Load() {
		return
	}
	lines := renderFooterWithBranch(ctx, branch)
	// A refused footer update only leaves the previous footer in place; the next
	// event repaints it, so it is not worth failing an agent event for.
	_ = ctx.SetFooter(lines)
}

// ── /context command ──────────────────────────────────────────────────────────

func contextCommand(ctx sdk.Context, _ string) error {
	usage, err := ctx.GetContextUsage()
	if err != nil {
		return fmt.Errorf("context usage: %w", err)
	}
	info, err := ctx.GetModelInfo()
	if err != nil {
		return fmt.Errorf("model info: %w", err)
	}
	modelInfo := patchModelCosts(info)
	modelName := ctx.Model()
	thinking, err := ctx.GetThinkingLevel()
	if err != nil {
		return fmt.Errorf("thinking level: %w", err)
	}
	activeTools, err := ctx.GetActiveTools()
	if err != nil {
		return fmt.Errorf("active tools: %w", err)
	}
	allTools, err := ctx.GetAllTools()
	if err != nil {
		return fmt.Errorf("tools: %w", err)
	}
	commands, err := ctx.GetCommands()
	if err != nil {
		return fmt.Errorf("commands: %w", err)
	}
	prompt, err := ctx.GetSystemPrompt()
	if err != nil {
		return fmt.Errorf("system prompt: %w", err)
	}
	branch, err := ctx.GetBranch()
	if err != nil {
		return fmt.Errorf("session branch: %w", err)
	}

	sp := analyzeSystemPrompt(prompt)
	convo := analyzeConversation(branch)
	toolTokens, _, _, _ := estimateToolDefTokens(allTools)

	var lines []string
	lines = append(lines, "═══ Context Window ═══")

	ctxWin := 0
	if modelInfo != nil {
		ctxWin = modelInfo.ContextWindow
	}

	// Pi reports tokens and percent as null after compaction until the next
	// response; that case takes the estimate branch below.
	if usage != nil && usage.Tokens != nil && *usage.Tokens > 0 && usage.Percent != nil {
		remaining := usage.ContextWindow - *usage.Tokens
		lines = append(lines, fmt.Sprintf("  Total:          %s / %s tokens (%.1f%%)",
			commaFmt(*usage.Tokens), commaFmt(usage.ContextWindow), *usage.Percent))
		lines = append(lines, fmt.Sprintf("  ├─ System prompt:   ~%s tokens (%s chars)",
			commaFmt(sp.totalTokens), commaFmt(sp.totalChars)))
		if sp.skillCount > 0 {
			lines = append(lines, fmt.Sprintf("  │    ├─ Skills:     ~%s tokens (%d skills)",
				commaFmt(sp.skillTokens), sp.skillCount))
		}
		lines = append(lines, fmt.Sprintf("  │    └─ Base prompt: ~%s tokens", commaFmt(sp.baseTokens)))
		lines = append(lines, fmt.Sprintf("  ├─ Tool defs:       ~%s tokens (%d active / %d total)",
			commaFmt(toolTokens), len(activeTools), len(allTools)))
		lines = append(lines, fmt.Sprintf("  ├─ Conversation:    ~%s tokens", commaFmt(convo.totalTokens)))
		lines = append(lines, fmt.Sprintf("  │    ├─ Text:       ~%s tokens (%d msgs)",
			commaFmt(charsToTokens(convo.textChars)), convo.messageCount))
		if convo.toolCallCount > 0 {
			lines = append(lines, fmt.Sprintf("  │    ├─ Tool calls:  counted in text (%d calls incl. args)", convo.toolCallCount))
		}
		if convo.toolResultCount > 0 {
			lines = append(lines, fmt.Sprintf("  │    ├─ Tool results: counted in text (%d results)", convo.toolResultCount))
		}
		if convo.thinkingChars > 0 {
			lines = append(lines, fmt.Sprintf("  │    ├─ Thinking:   ~%s tokens", commaFmt(charsToTokens(convo.thinkingChars))))
		}
		lines = append(lines, fmt.Sprintf("  │    └─ Overhead:   ~%s tokens (%d msgs × %d)",
			commaFmt(convo.messageCount*msgOverheadTokens), convo.messageCount, msgOverheadTokens))

		cd := getSessionCostData(branch, modelInfo)
		if cd.cacheRead > 0 || cd.cacheWrite > 0 {
			lines = append(lines, "  ├─ Cache:")
			lines = append(lines, fmt.Sprintf("  │    ├─ Read:       %s tokens", commaFmt(cd.cacheRead)))
			lines = append(lines, fmt.Sprintf("  │    ├─ Write:      %s tokens", commaFmt(cd.cacheWrite)))
			fresh := max(0, cd.input-cd.cacheRead-cd.cacheWrite)
			lines = append(lines, fmt.Sprintf("  │    └─ Fresh:      %s tokens", commaFmt(fresh)))
		}
		lines = append(lines, fmt.Sprintf("  └─ Available:       %s tokens remaining", commaFmt(remaining)))
	} else {
		estTotal := sp.totalTokens + toolTokens + convo.totalTokens + msgOverheadTokens
		estPct := float64(0)
		if ctxWin > 0 {
			estPct = float64(estTotal) / float64(ctxWin) * 100
		}
		remaining := ctxWin - estTotal
		lines = append(lines, fmt.Sprintf("  Total:          ~%s / %s tokens (~%.1f%%, est.)",
			commaFmt(estTotal), commaFmt(ctxWin), estPct))
		lines = append(lines, fmt.Sprintf("  ├─ System prompt:   ~%s tokens (%s chars)",
			commaFmt(sp.totalTokens), commaFmt(sp.totalChars)))
		if sp.skillCount > 0 {
			lines = append(lines, fmt.Sprintf("  │    ├─ Skills:     ~%s tokens (%d skills)", commaFmt(sp.skillTokens), sp.skillCount))
		}
		lines = append(lines, fmt.Sprintf("  │    └─ Base prompt: ~%s tokens", commaFmt(sp.baseTokens)))
		lines = append(lines, fmt.Sprintf("  ├─ Tool defs:       ~%s tokens (%d active / %d total)",
			commaFmt(toolTokens), len(activeTools), len(allTools)))
		lines = append(lines, fmt.Sprintf("  ├─ Conversation:    ~%s tokens", commaFmt(convo.totalTokens)))
		lines = append(lines, fmt.Sprintf("  └─ Available:       ~%s tokens remaining (est.)", commaFmt(remaining)))
	}

	// Model
	lines = append(lines, "", "═══ Model ═══")
	if modelInfo != nil {
		lines = append(lines, fmt.Sprintf("  Provider:  %s", modelInfo.Provider))
		lines = append(lines, fmt.Sprintf("  Model:     %s", modelInfo.Name))
		lines = append(lines, fmt.Sprintf("  Context:   %s tokens", commaFmt(modelInfo.ContextWindow)))
		lines = append(lines, fmt.Sprintf("  Max out:   %s tokens", commaFmt(modelInfo.MaxOutputTokens)))
		lines = append(lines, fmt.Sprintf("  Thinking:  %s", thinking))
		lines = append(lines, fmt.Sprintf("  Reasoning: %v", modelInfo.Reasoning))
	} else {
		lines = append(lines, fmt.Sprintf("  Model:     %s", modelName))
		lines = append(lines, fmt.Sprintf("  Thinking:  %s", thinking))
	}

	// Tools
	activeSet := make(map[string]bool, len(activeTools))
	for _, t := range activeTools {
		activeSet[t] = true
	}
	lines = append(lines, "", fmt.Sprintf("═══ Tools (%d active / %d total) ═══",
		len(activeTools), len(allTools)))
	for _, t := range allTools {
		icon := "✗"
		if activeSet[t.Name] {
			icon = "✓"
		}
		if t.Description != "" {
			desc := t.Description
			if len(desc) > 60 {
				desc = desc[:57] + "..."
			}
			lines = append(lines, fmt.Sprintf("  %s %s — %s", icon, t.Name, desc))
		} else {
			lines = append(lines, fmt.Sprintf("  %s %s", icon, t.Name))
		}
	}

	// Commands
	if len(commands) > 0 {
		lines = append(lines, "", fmt.Sprintf("═══ Commands (%d) ═══", len(commands)))
		var names []string
		for _, c := range commands {
			names = append(names, "/"+c.Name)
		}
		lines = append(lines, "  "+strings.Join(names, ", "))
	}

	// Tool calls
	mu.Lock()
	calls := totalToolCalls
	counts := make(map[string]int, len(toolCallCounts))
	for k, v := range toolCallCounts {
		counts[k] = v
	}
	mu.Unlock()

	if calls > 0 {
		lines = append(lines, "", fmt.Sprintf("═══ Tool Calls This Session (%d) ═══", calls))
		type kv struct {
			name  string
			count int
		}
		sorted := make([]kv, 0, len(counts))
		for k, v := range counts {
			sorted = append(sorted, kv{k, v})
		}
		slices.SortFunc(sorted, func(a, b kv) int { return cmp.Compare(b.count, a.count) })
		for _, s := range sorted {
			barLen := max(1, int(float64(s.count)/float64(calls)*20))
			bar := strings.Repeat("█", barLen)
			lines = append(lines, fmt.Sprintf("  %-20s %4d %s", s.name, s.count, bar))
		}
	}

	// Cost
	cd := getSessionCostData(branch, modelInfo)
	lines = append(lines, "", "═══ Session Cost ═══")
	lines = append(lines, fmt.Sprintf("  Input:       %s tokens  (%s)", commaFmt(cd.input), fmtCost(cd.cost.input)))
	lines = append(lines, fmt.Sprintf("  Output:      %s tokens  (%s)", commaFmt(cd.output), fmtCost(cd.cost.output)))
	if cd.cacheRead > 0 {
		lines = append(lines, fmt.Sprintf("  Cache read:  %s tokens  (%s)", commaFmt(cd.cacheRead), fmtCost(cd.cost.cacheRead)))
	}
	if cd.cacheWrite > 0 {
		lines = append(lines, fmt.Sprintf("  Cache write: %s tokens  (%s)", commaFmt(cd.cacheWrite), fmtCost(cd.cost.cacheWrite)))
	}
	lines = append(lines, fmt.Sprintf("  Total:       %s", fmtCost(cd.cost.total)))

	if modelInfo != nil && modelInfo.InputCostPer1M > 0 {
		lines = append(lines, "", "── Model Pricing (per 1M tokens) ──")
		lines = append(lines, fmt.Sprintf("  Input:       $%.2f", modelInfo.InputCostPer1M))
		lines = append(lines, fmt.Sprintf("  Output:      $%.2f", modelInfo.OutputCostPer1M))
		if modelInfo.CacheReadCostPer1M > 0 {
			lines = append(lines, fmt.Sprintf("  Cache read:  $%.2f", modelInfo.CacheReadCostPer1M))
		}
		if modelInfo.CacheWriteCostPer1M > 0 {
			lines = append(lines, fmt.Sprintf("  Cache write: $%.2f", modelInfo.CacheWriteCostPer1M))
		}
	}

	// Accuracy
	mu.Lock()
	calStr := "default — calibrates after first turn"
	if calibrated {
		calStr = "calibrated from provider ✓"
	}
	ratio := charsPerToken
	mu.Unlock()

	lines = append(lines, "", "═══ Estimation Accuracy ═══")
	lines = append(lines, fmt.Sprintf("  Token ratio:     %.2f chars/token (%s)", ratio, calStr))
	if usage != nil && usage.Tokens != nil && *usage.Tokens > 0 {
		lines = append(lines, "  Context total:   from provider usage response (exact)")
	} else {
		lines = append(lines, "  Context total:   estimated (no provider usage yet, or reset by compaction)")
	}
	lines = append(lines, "  System prompt:   chars ÷ ratio (±5–10% after calibration)")
	lines = append(lines, "  Tool defs:       schema serialization ÷ ratio (±10–15%)")
	lines = append(lines, fmt.Sprintf("  Conversation:    all content types counted (text, %d tool calls w/ args, %d tool results)",
		convo.toolCallCount, convo.toolResultCount))
	lines = append(lines, "  Per-message:     +4 tokens overhead per message for role/formatting")
	lines = append(lines, "  Cost:            provider-reported tokens x model rates (an estimate, not a bill)")

	if modelInfo != nil && strings.Contains(modelInfo.Provider, "copilot") {
		lines = append(lines, "")
		lines = append(lines, "  ⚠ GitHub Copilot may enforce a smaller context window than")
		lines = append(lines, "    the model's rated capacity.")
	}

	ctx.Notify(strings.Join(lines, "\n"), "info")
	return nil
}

// ── /tools command ────────────────────────────────────────────────────────────

func toolsCommand(ctx sdk.Context, _ string) error {
	activeTools, err := ctx.GetActiveTools()
	if err != nil {
		return fmt.Errorf("active tools: %w", err)
	}
	allTools, err := ctx.GetAllTools()
	if err != nil {
		return fmt.Errorf("tools: %w", err)
	}

	activeSet := make(map[string]bool, len(activeTools))
	for _, t := range activeTools {
		activeSet[t] = true
	}

	lines := []string{fmt.Sprintf("═══ Tools (%d active / %d total) ═══", len(activeTools), len(allTools)), ""}
	for _, t := range allTools {
		icon := "✗"
		if activeSet[t.Name] {
			icon = "✓"
		}
		mu.Lock()
		calls := toolCallCounts[t.Name]
		mu.Unlock()

		callStr := ""
		if calls > 0 {
			callStr = fmt.Sprintf(" (%d calls)", calls)
		}
		lines = append(lines, fmt.Sprintf("  %s %s%s", icon, t.Name, callStr))
		if t.Description != "" {
			desc := t.Description
			if len(desc) > 70 {
				desc = desc[:67] + "..."
			}
			lines = append(lines, "    "+desc)
		}
	}

	ctx.Notify(strings.Join(lines, "\n"), "info")
	return nil
}

// ── /cost command ─────────────────────────────────────────────────────────────

func costCommand(ctx sdk.Context, _ string) error {
	branch, err := ctx.GetBranch()
	if err != nil {
		return fmt.Errorf("session branch: %w", err)
	}
	info, err := ctx.GetModelInfo()
	if err != nil {
		return fmt.Errorf("model info: %w", err)
	}
	modelInfo := patchModelCosts(info)

	cd := getSessionCostData(branch, modelInfo)

	lines := []string{"═══ Session Cost (Main Agent) ═══", ""}
	lines = append(lines, fmt.Sprintf("  Input tokens:       %10s  %s", commaFmt(cd.input), fmtCost(cd.cost.input)))
	lines = append(lines, fmt.Sprintf("  Output tokens:      %10s  %s", commaFmt(cd.output), fmtCost(cd.cost.output)))
	if cd.cacheRead > 0 {
		lines = append(lines, fmt.Sprintf("  Cache read tokens:  %10s  %s", commaFmt(cd.cacheRead), fmtCost(cd.cost.cacheRead)))
	}
	if cd.cacheWrite > 0 {
		lines = append(lines, fmt.Sprintf("  Cache write tokens: %10s  %s", commaFmt(cd.cacheWrite), fmtCost(cd.cost.cacheWrite)))
	}
	lines = append(lines, fmt.Sprintf("  %s", strings.Repeat("─", 42)))
	lines = append(lines, fmt.Sprintf("  Total:              %10s  %s", commaFmt(cd.totalTokens), fmtCost(cd.cost.total)))

	// Per-model cost distribution
	if len(cd.byModel) > 0 {
		lines = append(lines, "", "── Cost by Model ──")
		for _, mb := range cd.byModel {
			pct := 0.0
			if cd.cost.total > 0 {
				pct = (mb.cost.total / cd.cost.total) * 100
			}
			lines = append(lines, fmt.Sprintf("  %-24s %10s  (%.1f%%)  %d msgs",
				mb.modelID, fmtCost(mb.cost.total), pct, mb.msgs))
			var parts []string
			if mb.input > 0 {
				parts = append(parts, fmt.Sprintf("in:%s=%s", fmtNum(mb.input), fmtCost(mb.cost.input)))
			}
			if mb.output > 0 {
				parts = append(parts, fmt.Sprintf("out:%s=%s", fmtNum(mb.output), fmtCost(mb.cost.output)))
			}
			if mb.cacheRead > 0 {
				parts = append(parts, fmt.Sprintf("cR:%s=%s", fmtNum(mb.cacheRead), fmtCost(mb.cost.cacheRead)))
			}
			if mb.cacheWrite > 0 {
				parts = append(parts, fmt.Sprintf("cW:%s=%s", fmtNum(mb.cacheWrite), fmtCost(mb.cost.cacheWrite)))
			}
			if len(parts) > 0 {
				lines = append(lines, "    "+strings.Join(parts, "  "))
			}
		}
	}

	// Current model pricing rates
	if modelInfo != nil && modelInfo.InputCostPer1M > 0 {
		lines = append(lines, "", fmt.Sprintf("── Active Model: %s (per 1M tokens) ──", modelInfo.ID))
		lines = append(lines, fmt.Sprintf("  Input:       $%.2f", modelInfo.InputCostPer1M))
		lines = append(lines, fmt.Sprintf("  Output:      $%.2f", modelInfo.OutputCostPer1M))
		if modelInfo.CacheReadCostPer1M > 0 {
			lines = append(lines, fmt.Sprintf("  Cache read:  $%.2f", modelInfo.CacheReadCostPer1M))
		}
		if modelInfo.CacheWriteCostPer1M > 0 {
			lines = append(lines, fmt.Sprintf("  Cache write: $%.2f", modelInfo.CacheWriteCostPer1M))
		}
	}

	ctx.Notify(strings.Join(lines, "\n"), "info")
	return nil
}

// ── /prompts command ──────────────────────────────────────────────────────────

func promptsCommand(ctx sdk.Context, args string) error {
	parts := strings.Fields(strings.TrimSpace(args))

	if len(parts) == 0 || parts[0] == "" {
		prompt, err := ctx.GetSystemPrompt()
		if err != nil {
			return fmt.Errorf("system prompt: %w", err)
		}
		if prompt == "" {
			ctx.Notify("No system prompt active", "info")
			return nil
		}
		mu.Lock()
		calLabel := "est."
		if calibrated {
			calLabel = "calibrated"
		}
		mu.Unlock()
		lines := []string{
			"═══ Main Agent System Prompt ═══",
			"",
			prompt,
			"",
			fmt.Sprintf("═══ %s chars · ~%s tokens (%s) ═══",
				commaFmt(len(prompt)), commaFmt(charsToTokens(len(prompt))), calLabel),
		}
		ctx.Notify(strings.Join(lines, "\n"), "info")
		return nil
	}

	if parts[0] == "agents" {
		configHome := ctx.ConfigHome()
		agentsDir := filepath.Join(configHome, "agents")
		entries, err := os.ReadDir(agentsDir)
		if err != nil {
			ctx.Notify("No agent definitions found", "info")
			return nil
		}
		lines := []string{"═══ Agent Definitions ═══", ""}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(agentsDir, e.Name()))
			if err != nil {
				continue
			}
			name, body := parseAgentFile(strings.TrimSuffix(e.Name(), ".md"), string(data))
			bodyLines := strings.SplitN(body, "\n", 4)
			preview := strings.Join(bodyLines[:min(3, len(bodyLines))], "\n")
			if len(bodyLines) > 3 {
				preview += fmt.Sprintf("\n  ... (%d lines total — use /prompts %s for full)", len(strings.Split(body, "\n")), name)
			}
			lines = append(lines, fmt.Sprintf("── %s (%s) ──", name, e.Name()))
			lines = append(lines, preview, "")
		}
		ctx.Notify(strings.Join(lines, "\n"), "info")
		return nil
	}

	// Specific agent name.
	agentName := strings.ToLower(strings.Join(parts, " "))
	configHome := ctx.ConfigHome()
	agentsDir := filepath.Join(configHome, "agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		ctx.Notify(fmt.Sprintf("Agent %q not found", agentName), "warning")
		return nil
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, _ := os.ReadFile(filepath.Join(agentsDir, e.Name()))
		name, body := parseAgentFile(strings.TrimSuffix(e.Name(), ".md"), string(data))
		if strings.ToLower(name) == agentName ||
			strings.ReplaceAll(strings.ToLower(name), " ", "-") == agentName {
			mu.Lock()
			calLabel := "est."
			if calibrated {
				calLabel = "calibrated"
			}
			mu.Unlock()
			lines := []string{
				fmt.Sprintf("═══ %s — System Prompt ═══", name),
				fmt.Sprintf("Source: %s/%s", agentsDir, e.Name()),
				"",
				body,
				"",
				fmt.Sprintf("═══ %s chars · ~%s tokens (%s) ═══",
					commaFmt(len(body)), commaFmt(charsToTokens(len(body))), calLabel),
			}
			ctx.Notify(strings.Join(lines, "\n"), "info")
			return nil
		}
	}
	ctx.Notify(fmt.Sprintf("Agent %q not found. Use /prompts agents to list all.", agentName), "warning")
	return nil
}

// parseAgentFile returns an agent definition's name and body. Only a file that
// starts with a "---" line has frontmatter; its "name:" overrides fallback. A file
// without frontmatter is all body, including any Markdown rule ("---") inside it.
func parseAgentFile(fallback, content string) (name, body string) {
	name = fallback
	rest, ok := strings.CutPrefix(content, "---\n")
	if !ok {
		return name, strings.TrimSpace(content)
	}
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return name, strings.TrimSpace(content)
	}
	for line := range strings.SplitSeq(rest[:end], "\n") {
		if value, found := strings.CutPrefix(line, "name:"); found {
			if value = strings.TrimSpace(value); value != "" {
				name = value
			}
			break
		}
	}
	body = rest[end+len("\n---"):]
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		body = body[i+1:]
	} else {
		body = ""
	}
	return name, strings.TrimSpace(body)
}

// ── Footer switch ─────────────────────────────────────────────────────────────

// footerOn is true while the extension owns PiG's footer. It is off by default:
// the footer replaces PiG's own, so it only appears when the user asks for it.
var footerOn atomic.Bool

// footerFlag is the flag that turns the footer on at startup.
const footerFlag = "context-footer"

func footerFlagSet(ctx sdk.Context) bool {
	value, err := ctx.GetFlag(footerFlag)
	if err != nil {
		return false
	}
	on, _ := value.(bool)
	return on
}

// enableFooter takes over the footer and paints it once.
func enableFooter(ctx sdk.Context) {
	if footerOn.Swap(true) {
		return
	}
	updateFooterWithBranch(ctx, softBranch(ctx))
}

// disableFooter gives PiG's footer back. Nothing repaints it afterwards.
func disableFooter(ctx sdk.Context) error {
	if !footerOn.Swap(false) {
		return nil
	}
	footerModelMu.Lock()
	lastFooterModel = footerModel{}
	footerModelMu.Unlock()
	return ctx.SetFooter(nil)
}

func contextFooterCommand(ctx sdk.Context, args string) error {
	switch strings.ToLower(strings.TrimSpace(args)) {
	case "on":
		enableFooter(ctx)
		ctx.Notify("Context footer on", "info")
	case "off":
		if err := disableFooter(ctx); err != nil {
			return fmt.Errorf("restore footer: %w", err)
		}
		ctx.Notify("Context footer off", "info")
	case "":
		if footerOn.Load() {
			return contextFooterCommand(ctx, "off")
		}
		return contextFooterCommand(ctx, "on")
	default:
		ctx.Notify("Usage: /context-footer [on|off]", "info")
	}
	return nil
}

// ── Main ──────────────────────────────────────────────────────────────────────

// Extension returns the context-info extension.
func Extension() *sdk.Extension {
	ext := sdk.New("context-info")
	footerOn.Store(false) // a new extension instance starts with PiG's own footer

	ext.Flag(footerFlag, sdk.FlagOptions{
		Description: "Replace PiG's footer with the context-info status footer (context, model, cost, tools)",
		Type:        sdk.FlagBoolean,
		Default:     false,
	})

	// Commands.
	ext.Command("context", "Show detailed context window composition and session info", contextCommand)
	ext.Command("tools", "Show all tools with active/inactive status", toolsCommand)
	ext.Command("cost", "Show session cost breakdown with per-token pricing", costCommand)
	ext.Command("prompts", "View system prompts: /prompts [agents|<agent-name>]", promptsCommand)
	ext.Command("context-footer", "Turn the context-info status footer on or off: /context-footer [on|off]", contextFooterCommand)

	// Shortcut: show full context detail.
	ext.Shortcut("ctrl+shift+i", "Show context window detail", func(ctx sdk.Context) error {
		return contextCommand(ctx, "")
	})

	// Events.
	ext.OnSessionStart(func(ctx sdk.Context, event map[string]any) (any, error) {
		// The footer is static lines, so it does not follow a resize on its own.
		// Re-lay-out from retained state; this makes no host reads.
		// A failure here only means the footer stops following resizes, which is
		// the pre-existing behavior, so it must not take the session down.
		if _, err := ctx.OnWidthChange(onWidthChange); err != nil {
			ctx.Notify("context-info: footer will not follow resizes: "+err.Error(), "warn")
		}
		mu.Lock()
		charsPerToken = 3.7
		calibrated = false
		mu.Unlock()

		// Rebuild tool counts from existing branch (handles resume/reload). If the
		// host has not populated the branch yet, before_agent_start gets one retry.
		branch := softBranch(ctx)
		reconstructInitialToolCounts(branch)
		calibrateFromBranch(ctx, branch)

		if footerFlagSet(ctx) {
			footerOn.Store(true)
			updateFooterWithBranch(ctx, branch)
		}
		return nil, nil
	})

	ext.OnEvent("message_end", func(ctx sdk.Context, data map[string]any) (any, error) {
		if footerOn.Load() {
			updateCostFromMessageEnd(ctx, data)
			updateFooter(ctx)
		}
		return nil, nil
	})

	ext.OnEvent("session_compact", func(ctx sdk.Context, _ map[string]any) (any, error) {
		clearLastUsage()
		invalidateCostCache()
		branch := softBranch(ctx)
		calibrateFromBranch(ctx, branch)
		updateFooterWithBranch(ctx, branch)
		return nil, nil
	})

	ext.OnEvent("before_agent_start", func(ctx sdk.Context, _ map[string]any) (any, error) {
		// session_start can fire before a resumed branch is populated. Fetch the
		// full branch only for that one reconstruction fallback; every-turn branch
		// fetches on this event delay the provider request in long sessions.
		if needsBranchCountReconstruction() {
			branch := softBranch(ctx)
			reconstructInitialToolCounts(branch)
			calibrateFromBranch(ctx, branch)
			updateFooterWithBranch(ctx, branch)
			return nil, nil
		}
		updateFooter(ctx)
		return nil, nil
	})

	ext.OnEvent("model_select", func(ctx sdk.Context, _ map[string]any) (any, error) {
		// Model cycling (Ctrl+P / Ctrl+Shift+P) makes this event hot, and it runs
		// synchronously on the keypress path, so it must stay cheap: no GetBranch.
		// Historical per-message costs don't change on a model switch (each message
		// is priced at the model active when it was processed), and future messages
		// accrue at the new model's rates via message_end. Calibration still runs at
		// session start / compaction / resume, where the branch is already in hand.
		updateFooter(ctx)
		return nil, nil
	})

	ext.OnEvent("thinking_level_select", func(ctx sdk.Context, _ map[string]any) (any, error) {
		updateFooter(ctx)
		return nil, nil
	})

	ext.OnEvent("tool_execution_end", func(ctx sdk.Context, event map[string]any) (any, error) {
		name, _ := event["toolName"].(string)
		if name == "" {
			name = "unknown"
		}
		mu.Lock()
		toolCallCounts[name]++
		totalToolCalls++
		mu.Unlock()
		updateFooter(ctx)
		return nil, nil
	})

	ext.OnEvent("turn_end", func(ctx sdk.Context, _ map[string]any) (any, error) {
		updateFooter(ctx)
		return nil, nil
	})

	return ext
}

// ── footer width discipline ───────────────────────────────────────────────────
//
// pig's ui.setFooter takes static lines, not a component. Nothing downstream
// re-measures them, so an over-wide line is the extension's bug: it wraps in the
// terminal while the renderer still counts it as one row, which desynchronizes
// the viewport arithmetic from the physical screen and shows up as duplicated
// rows and a scrolled-up reader being snapped to the bottom.
//
// The host clamps as a backstop, but clamping is a blunt cut mid-segment. Fitting
// here keeps whole segments and drops the least valuable ones, so a narrow pane
// loses "Calls" rather than half of a number.

// visibleWidth measures display columns, ignoring the SGR escapes accent/muted/
// dim add. It is deliberately narrow: this file emits only CSI SGR sequences.
func visibleWidth(s string) int {
	width, inEscape := 0, false
	for _, r := range s {
		switch {
		case r == '\033':
			inEscape = true
		case inEscape:
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEscape = false
			}
		case r == '\u2191', r == '\u2193', r == '\u2295', r == '\u2502':
			width++
		default:
			width += runeColumns(r)
		}
	}
	return width
}

// runeColumns reports the column count for a rune: 2 for the East Asian Wide and
// Fullwidth ranges, 0 for combining marks, 1 otherwise.
func runeColumns(r rune) int {
	switch {
	case r >= 0x0300 && r <= 0x036F:
		return 0
	case r >= 0x1100 && r <= 0x115F,
		r >= 0x2E80 && r <= 0xA4CF,
		r >= 0xAC00 && r <= 0xD7A3,
		r >= 0xF900 && r <= 0xFAFF,
		r >= 0xFF00 && r <= 0xFF60,
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F300 && r <= 0x1FAFF:
		return 2
	default:
		return 1
	}
}

// fitSegments joins as many leading segments as fit in width, in the order
// given. Callers order segments most to least important; a segment is kept only
// if it fits whole, so the result never needs a mid-segment cut.
//
// width <= 0 means the host has not reported a size yet, so everything is kept
// and the host's clamp remains the backstop.
func fitSegments(segments []string, sep string, width int) string {
	if len(segments) == 0 {
		return ""
	}
	// The first segment is never dropped, but it can be wider than the terminal on its own (a long path and
	// branch): cut it to the width so no footer line exceeds the render width.
	out := truncateToWidth(segments[0], width)
	if width <= 0 {
		for _, seg := range segments[1:] {
			out += sep + seg
		}
		return out
	}
	sepWidth := visibleWidth(sep)
	used := visibleWidth(out)
	for _, seg := range segments[1:] {
		next := used + sepWidth + visibleWidth(seg)
		if next > width {
			break
		}
		out += sep + seg
		used = next
	}
	return out
}

// truncateToWidth cuts s to at most width visible columns the way Pi's truncateToWidth does: by columns, not
// bytes, keeping the SGR styling it has already emitted, closing it before an ellipsis. A line that fits is
// returned unchanged, and width <= 0 (no size reported yet) never cuts.
func truncateToWidth(s string, width int) string {
	if width <= 0 || visibleWidth(s) <= width {
		return s
	}
	const ellipsis = "…"
	limit := width - 1 // one column for the ellipsis
	var b strings.Builder
	cols, inEscape := 0, false
	for _, r := range s {
		switch {
		case r == '\033':
			inEscape = true
			b.WriteRune(r)
		case inEscape:
			b.WriteRune(r)
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEscape = false
			}
		default:
			w := visibleWidth(string(r))
			if cols+w > limit {
				return b.String() + ansiReset + ellipsis
			}
			b.WriteRune(r)
			cols += w
		}
	}
	return b.String()
}

// ── resize re-layout ──────────────────────────────────────────────────────────
//
// A footer is pushed to the host as static lines, so it keeps the width it was
// built for until something re-pushes it. ctx.OnWidthChange is that trigger.
//
// Rebuilding it the normal way costs four blocking host round-trips on the
// extension's message loop, and a drag-resize delivers width_change
// continuously, so doing that per event stalls the loop and floods the socket.
// A resize does not change any of those values: only the layout width changed.
// So acquisition and layout are separated, and resize re-lays-out what the last
// full update already retained.

// footerModel is the acquired state a re-layout needs. It holds counts and
// pre-rendered fragments rather than the tool slices, because that is all the
// layout consumes and it keeps the retained footprint flat as a session grows.
type footerModel struct {
	activeTools int
	allTools    int
	thinking    string
	modelName   string
	ctxStr      string
	valid       bool
}

var (
	footerModelMu    sync.Mutex
	lastFooterModel  footerModel
	lastFooterWidth  int
	widthCoalesceGen uint64
)

// takeRelayout decides whether a resize warrants a re-push and claims the new
// width if so. Separated from the push so the decision is testable without a
// host connection, and so the lock is never held across IPC.
//
// A re-layout is skipped when no full update has retained a model yet (there is
// nothing to lay out, and acquiring here is what this exists to avoid), and when
// the width is unchanged, which is common because the host re-sends the current
// width on reconnect and on unrelated geometry events.
func takeRelayout(width int) (footerModel, bool) {
	footerModelMu.Lock()
	defer footerModelMu.Unlock()
	if !lastFooterModel.valid || width == lastFooterWidth {
		return footerModel{}, false
	}
	lastFooterWidth = width
	return lastFooterModel, true
}

func storeFooterModel(m footerModel) {
	m.valid = true
	footerModelMu.Lock()
	lastFooterModel = m
	footerModelMu.Unlock()
}

// relayoutFooterAtWidth re-renders the retained model at a new width and pushes
// it. It makes no host reads. Returns false when no full update has run yet, in
// which case there is nothing to re-lay-out and the next normal update will
// cover it.
func relayoutFooterAtWidth(ctx sdk.Context, width int) bool {
	if !footerOn.Load() {
		return false
	}
	m, ok := takeRelayout(width)
	if !ok {
		return false
	}

	mu.Lock()
	calMark := ""
	if calibrated {
		calMark = " " + dim("⊕")
	}
	calls := totalToolCalls
	mu.Unlock()

	var cd costData
	if cached, ok := cachedSessionCostData(); ok {
		cd = cached
	}

	sep := dim("│")
	cwd := ctx.Cwd()
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(cwd, home) {
		cwd = "~" + cwd[len(home):]
	}
	gitPart := ""
	if branch := currentGitBranch(ctx.Cwd()); branch != "" {
		gitPart = fmt.Sprintf(" %s %s %s %s", sep, accent("Git:"), muted(branch), sep)
	}

	line1 := fitSegments([]string{
		muted(cwd) + gitPart,
		accent("Context:") + " " + m.ctxStr,
		accent("Model:") + " " + muted(m.modelName),
		accent("Think:") + " " + muted(m.thinking) + calMark,
	}, "  "+sep+" ", width)
	line2 := formatCostLineCounts(cd, m.activeTools, m.allTools, calls, width)

	_ = ctx.SetFooter([]string{line1, line2})
	return true
}

// onWidthChange re-lays-out the footer, coalescing a resize storm.
//
// The handler runs on the message loop and must not block it, so it records the
// width and hands the push to a short-lived goroutine. Each arrival supersedes
// the previous one by generation, so a drag that delivers many widths issues one
// push for the width the user settled on rather than one per event.
func onWidthChange(ctx sdk.Context, width int) {
	footerModelMu.Lock()
	widthCoalesceGen++
	gen := widthCoalesceGen
	footerModelMu.Unlock()

	go func() {
		time.Sleep(40 * time.Millisecond)
		footerModelMu.Lock()
		superseded := gen != widthCoalesceGen
		footerModelMu.Unlock()
		if superseded {
			return
		}
		relayoutFooterAtWidth(ctx, width)
	}()
}
