package jobs

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

// Deterministic fallback job widget view matching omp's `JobWidgetRuntime`
// fallback (`packages/coding-agent/src/job-widgets/runtime.ts:639-716`,
// `job-widgets/format.ts:18-42`, `job-widgets/spec.ts:157`,
// `job-widgets/registry.ts:146-238`).
//
// AI-authored widget specs (`state: "live"` / `"authoring"`) are NOT ported;
// this file implements the exact deterministic fallback view (`state: "fallback"`)
// that omp produces whenever no spec is authored.

const (
	widgetMaxTitleChars = 48
	widgetMaxStageChars = 200
	widgetMaxLineChars  = 4096
	widgetRateWindowMs  = 60_000
)

// WidgetStatItem is one metric item inside a fallback stat block.
// upstream: packages/tui/src/tools/job-widget.ts
type WidgetStatItem struct {
	Label string `json:"label"`
	Text  string `json:"text"`
}

// WidgetBlock is one block inside a JobWidgetView (`kind: "stat"` for fallback).
type WidgetBlock struct {
	Kind  string           `json:"kind"`
	Items []WidgetStatItem `json:"items"`
}

// WidgetView mirrors omp's `JobWidgetView` (`packages/tui/src/tools/job-widget.ts:128-140`)
// for the deterministic fallback state (`state: "fallback"`).
type WidgetView struct {
	Title     string        `json:"title"`
	Stage     string        `json:"stage,omitempty"`
	Blocks    []WidgetBlock `json:"blocks"`
	State     string        `json:"state"`
	UpdatedAt int64         `json:"updatedAt"`
}

// WidgetProvider is an optional interface a Job may implement to supply its
// deterministic fallback `JobWidgetView` for `wait`, `proc://` reads, and
// `proc://<id>/kill` snapshots.
type WidgetProvider interface {
	WidgetView(now time.Time) *WidgetView
}

type widgetLineBucket struct {
	t int64
	n int
}

// FallbackWidgetTracker tracks line counts, recent rate buckets, and the last
// non-empty output line for a single background job so `View()` produces omp's
// exact fallback `JobWidgetView`.
type FallbackWidgetTracker struct {
	mu           sync.Mutex
	startMs      int64
	lastLineAtMs int64
	totalLines   int
	lastLine     string
	partial      string
	recent       []widgetLineBucket
	frozen       bool
	frozenView   *WidgetView
}

// NewFallbackWidgetTracker constructs a tracker anchored at started.
func NewFallbackWidgetTracker(started time.Time) *FallbackWidgetTracker {
	return &FallbackWidgetTracker{
		startMs: started.UnixMilli(),
	}
}

// PushChunk ingests a raw output chunk (already ANSI-stripped or plain text),
// splitting CR/CRLF/LF into complete lines and carrying any trailing partial
// line until a newline or Flush.
func (w *FallbackWidgetTracker) PushChunk(chunk string, now time.Time) {
	if chunk == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.frozen {
		return
	}
	norm := strings.ReplaceAll(chunk, "\r\n", "\n")
	norm = strings.ReplaceAll(norm, "\r", "\n")
	combined := w.partial + norm
	parts := strings.Split(combined, "\n")
	last := parts[len(parts)-1]
	if len(last) > widgetMaxLineChars*2 {
		last = last[:widgetMaxLineChars*2]
	}
	w.partial = last
	w.recordLinesLocked(parts[:len(parts)-1], now.UnixMilli())
}

// Flush finalizes any trailing partial line when the job settles.
func (w *FallbackWidgetTracker) Flush(now time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.frozen {
		return
	}
	if w.partial != "" {
		p := w.partial
		w.partial = ""
		w.recordLinesLocked([]string{p}, now.UnixMilli())
	}
}

// RecordToolCallLine records one finished subagent tool call into a task job's
// fallback tracker, matching omp's `appendToolResult` (`task/widget-inputs.ts:25-29`):
// `\n» <tool> [<intent>]\n<text>\n`.
func (w *FallbackWidgetTracker) RecordToolCall(tool, intent, text string, now time.Time) {
	head := tool
	if intent != "" {
		head = tool + " " + intent
	}
	const maxToolResultChars = 64 * 1024
	if len(text) > maxToolResultChars {
		text = text[len(text)-maxToolResultChars:]
	}
	w.PushChunk("\n» "+head+"\n"+text+"\n", now)
}

func (w *FallbackWidgetTracker) recordLinesLocked(lines []string, t int64) {
	fresh := 0
	var lastNonEmpty string
	for _, raw := range lines {
		line := raw
		if len(line) > widgetMaxLineChars {
			line = line[:widgetMaxLineChars]
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		fresh++
		lastNonEmpty = line
	}
	if fresh == 0 {
		return
	}
	w.totalLines += fresh
	w.lastLine = lastNonEmpty
	w.lastLineAtMs = t
	w.recent = append(w.recent, widgetLineBucket{t: t, n: fresh})
	horizon := t - widgetRateWindowMs
	drop := 0
	for drop < len(w.recent) && w.recent[drop].t < horizon {
		drop++
	}
	if drop > 0 {
		w.recent = w.recent[drop:]
	}
}

// View computes omp's deterministic fallback `JobWidgetView`
// (`job-widgets/runtime.ts:640-688`, `job-widgets/registry.ts:225-238`).
//   - `displayLabel`: `intent` when set, else `label` (`jobDisplayLabel`).
//   - `stage`: explicit stage when non-empty; otherwise when `lineStage` is true
//     (bash jobs), the last non-empty line truncated to 200 runes.
//   - `settled`: when true, flushes any partial line and freezes the view.
func (w *FallbackWidgetTracker) View(displayLabel, stage string, lineStage, settled bool, now time.Time) *WidgetView {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.frozen && w.frozenView != nil {
		cp := cloneWidgetView(w.frozenView)
		return cp
	}
	nowMs := now.UnixMilli()
	if settled && w.partial != "" {
		p := w.partial
		w.partial = ""
		w.recordLinesLocked([]string{p}, nowMs)
	}
	updatedAt := w.startMs
	if w.lastLineAtMs > 0 {
		updatedAt = w.lastLineAtMs
	}
	effStage := strings.TrimSpace(stage)
	if effStage == "" && lineStage && w.lastLine != "" {
		effStage = truncateWidgetText(strings.TrimSpace(w.lastLine), widgetMaxStageChars)
	}
	blocks := w.fallbackBlocksLocked(nowMs)
	v := &WidgetView{
		Title:     truncateWidgetText(collapseWidgetWhitespace(displayLabel), widgetMaxTitleChars),
		Stage:     effStage,
		Blocks:    blocks,
		State:     "fallback",
		UpdatedAt: updatedAt,
	}
	if settled {
		w.frozen = true
		w.frozenView = cloneWidgetView(v)
	}
	return v
}

func (w *FallbackWidgetTracker) fallbackBlocksLocked(nowMs int64) []WidgetBlock {
	if w.totalLines == 0 {
		return []WidgetBlock{}
	}
	horizon := nowMs - widgetRateWindowMs
	recent := 0
	for _, b := range w.recent {
		if b.t >= horizon {
			recent += b.n
		}
	}
	winMs := nowMs - w.startMs
	if winMs < 1000 {
		winMs = 1000
	}
	if winMs > widgetRateWindowMs {
		winMs = widgetRateWindowMs
	}
	perSec := float64(recent) / (float64(winMs) / 1000.0)
	return []WidgetBlock{
		{
			Kind: "stat",
			Items: []WidgetStatItem{
				{Label: "lines", Text: FormatWidgetNumber(float64(w.totalLines))},
				{Label: "lines/s", Text: FormatWidgetNumber(perSec)},
			},
		},
	}
}

// BuildFallbackWidget builds a one-shot fallback WidgetView for a job that does
// not maintain a streaming FallbackWidgetTracker (e.g. a task job with no tool
// calls yet, or a test double).
func BuildFallbackWidget(displayLabel, stage string, started time.Time) *WidgetView {
	return &WidgetView{
		Title:     truncateWidgetText(collapseWidgetWhitespace(displayLabel), widgetMaxTitleChars),
		Stage:     strings.TrimSpace(stage),
		Blocks:    []WidgetBlock{},
		State:     "fallback",
		UpdatedAt: started.UnixMilli(),
	}
}

// WidgetViewToMap converts a WidgetView to a plain map[string]any matching omp's
// JSON shape for map-based details (`proc://`).
func WidgetViewToMap(v *WidgetView) map[string]any {
	if v == nil {
		return nil
	}
	blocks := make([]map[string]any, 0, len(v.Blocks))
	for _, b := range v.Blocks {
		items := make([]map[string]any, 0, len(b.Items))
		for _, it := range b.Items {
			items = append(items, map[string]any{
				"label": it.Label,
				"text":  it.Text,
			})
		}
		blocks = append(blocks, map[string]any{
			"kind":  b.Kind,
			"items": items,
		})
	}
	m := map[string]any{
		"title":     v.Title,
		"blocks":    blocks,
		"state":     v.State,
		"updatedAt": v.UpdatedAt,
	}
	if v.Stage != "" {
		m["stage"] = v.Stage
	}
	return m
}

func cloneWidgetView(v *WidgetView) *WidgetView {
	if v == nil {
		return nil
	}
	cp := *v
	cp.Blocks = make([]WidgetBlock, len(v.Blocks))
	for i, b := range v.Blocks {
		nb := WidgetBlock{Kind: b.Kind, Items: make([]WidgetStatItem, len(b.Items))}
		copy(nb.Items, b.Items)
		cp.Blocks[i] = nb
	}
	return &cp
}

func collapseWidgetWhitespace(s string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

// truncateWidgetText ports `packages/utils/src/format.ts:89-93` `truncate(str, maxLen, "…")`.
func truncateWidgetText(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 1 {
		return "…"
	}
	return string(runes[:maxLen-1]) + "…"
}

// FormatWidgetNumber ports `packages/coding-agent/src/job-widgets/format.ts:24-42`.
func FormatWidgetNumber(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "—"
	}
	mag := math.Abs(value)
	const compactFrom = 10_000.0
	if mag >= compactFrom {
		steps := []struct {
			step   float64
			suffix string
		}{
			{1e12, "T"},
			{1e9, "G"},
			{1e6, "M"},
			{1e3, "k"},
		}
		for _, st := range steps {
			if mag < st.step {
				continue
			}
			scaled := value / st.step
			if math.Abs(scaled) >= 100 {
				return fmt.Sprintf("%.0f%s", scaled, st.suffix)
			}
			return fmt.Sprintf("%.1f%s", scaled, st.suffix)
		}
	}
	if math.Trunc(value) == value {
		return fmt.Sprintf("%.0f", value)
	}
	if mag >= 100 {
		return fmt.Sprintf("%.0f", value)
	}
	if mag >= 10 {
		return fmt.Sprintf("%.1f", value)
	}
	return fmt.Sprintf("%.2f", value)
}
