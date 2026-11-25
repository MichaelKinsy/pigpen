package ansi

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
)

// Above-editor status-band widget (`omp-status-band`) rendered via PiG SDK's
// Context.SetWidgetRenderer over PiG's own stock editor:
//   - Optional pinned provider-error banner (`RenderErrorBanner`).
//   - Optional `  ⎋ Working…` working row (`RenderWorkingRow`) paired with
//     `ctx.SetWorkingVisible(false)` so PiG's own working border/loader is
//     suppressed and never duplicated.
//   - omp's filled powerline status band with embedded context gauge
//     (`▶─0.4%───╎───┃───128K─`).
//
// PiG's stock editor draws the prompt itself (no Context.SetEditorComponent
// delegation): slash (`/`) and `@` file completion, history, bracketed paste,
// and clean `/quit` / Ctrl+C / Ctrl+D exit remain PiG's native paths.
//
// upstream:
//   packages/tui/src/components/composer/band.ts:10-46
//   packages/tui/src/status-line/component.ts:3201-3636
//   packages/tui/src/status-line/segments.ts:218-232,365-381
//   packages/tui/src/overlays/error-banner.ts:26-53
//   packages/coding-agent/src/modes/interactive-mode.ts:9246-9259

const (
	statusBandWidgetKey        = "omp-status-band"
	nativeErrorBannerWidgetKey = "omp-error-banner"
)

// BuildNativeErrorBannerView builds the structured kit.View for Tern's dock
// error banner (`omp.errorBanner`), using omp's formatted provider error text
// and omitting the terminal-only "Dismissed when you send your next message."
// hint (Tern's frontend adds Details/Dismiss buttons per overlays/error-banner.ts:60-82).
// Returns (zero, false) when message is empty.
func BuildNativeErrorBannerView(message string) (kit.View, bool) {
	formatted := strings.TrimSpace(sanitizeDisplayText(formatProviderErrorMessage(message)))
	if formatted == "" {
		return kit.View{}, false
	}
	msgNode := kit.NewText(formatted, 0, 0)
	msgNode.ID = "omp.errorBanner.message"
	root := kit.NewContainer(msgNode)
	root.ID = "omp.errorBanner"
	return kit.View{Root: root}, true
}

// httpStatusJSONErrorRe matches PiG's OpenAI/HTTP provider error string format:
// `400: {"message":...}` or `400 {"error":{"message":...}}` (optionally prefixed
// with `Error:` or `HTTP`).
var httpStatusJSONErrorRe = regexp.MustCompile(`^(?:Error:\s*)?(?:HTTP\s+)?(\d{3})\s*:?\s*(\{[\s\S]*\})\s*$`)

// formatProviderErrorMessage unpacks raw HTTP JSON error payloads into omp's
// human-readable format: `<status> <message> (type=<type> param=<param> code=<code>)`.
// Non-JSON or already-formatted messages pass through unchanged.
// upstream: packages/ai/src/utils/http-inspector.ts:317-346
func formatProviderErrorMessage(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	statusPrefix := ""
	bodyText := trimmed
	if m := httpStatusJSONErrorRe.FindStringSubmatch(trimmed); m != nil {
		statusPrefix = m[1] + " "
		bodyText = strings.TrimSpace(m[2])
	} else if !strings.HasPrefix(trimmed, "{") {
		return trimmed
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(bodyText), &payload); err != nil || payload == nil {
		return trimmed
	}
	errObj := asMap(payload["error"])
	if errObj == nil {
		errObj = payload
	}
	strProp := func(m map[string]any, key string) string {
		if m == nil {
			return ""
		}
		if s, ok := m[key].(string); ok {
			return strings.TrimSpace(s)
		}
		return ""
	}
	core := strProp(errObj, "message")
	if core == "" {
		core = strProp(payload, "message")
	}
	if core == "" && asMap(payload["error"]) == nil {
		core = strProp(payload, "error")
	}
	if core == "" {
		return trimmed
	}
	var extras []string
	if v := strProp(errObj, "type"); v != "" {
		extras = append(extras, "type="+v)
	} else if v := strProp(payload, "type"); v != "" {
		extras = append(extras, "type="+v)
	}
	if v := strProp(errObj, "param"); v != "" {
		extras = append(extras, "param="+v)
	} else if v := strProp(payload, "param"); v != "" {
		extras = append(extras, "param="+v)
	}
	if v := strProp(errObj, "code"); v != "" {
		extras = append(extras, "code="+v)
	} else if v := strProp(payload, "code"); v != "" {
		extras = append(extras, "code="+v)
	}
	if len(extras) == 0 {
		return statusPrefix + core
	}
	return statusPrefix + core + " (" + strings.Join(extras, " ") + ")"
}

// RenderErrorBanner renders omp's pinned provider-error banner above the status
// band (between two red horizontal rules with "Dismissed when you send your
// next message.").
// upstream: packages/tui/src/overlays/error-banner.ts:37-53,
//
//	packages/tui/src/chrome/error-block.ts:34-63
func RenderErrorBanner(theme sdk.UITheme, message string, width int) []string {
	msg := strings.TrimSpace(sanitizeDisplayText(formatProviderErrorMessage(message)))
	if msg == "" || width <= 0 {
		return nil
	}
	p := paletteFromTheme(theme)
	rule := p.fg("error", strings.Repeat(unicodeSymbols.boxHoriz, width))
	const maxBannerRows = 4
	const contIndent = "  "
	contentWidth := max(1, width-2) // Text(..., 1, 0) horizontal padding
	wrapWidth := max(1, contentWidth-len(contIndent))
	rawLines := strings.Split(replaceTabs(msg), "\n")
	var logical []string
	for _, l := range rawLines {
		if t := strings.TrimSpace(l); t != "" {
			logical = append(logical, t)
		}
	}
	if len(logical) == 0 {
		logical = []string{"Unknown error"}
	}
	var wrapped []string
	for idx, l := range logical {
		var styled string
		if idx == 0 {
			styled = p.bold(p.fg("error", unicodeSymbols.statusError+" "+l))
		} else {
			styled = p.fg("error", l)
		}
		for _, row := range wrapAnsi(styled, wrapWidth) {
			if len(wrapped) == 0 {
				wrapped = append(wrapped, " "+row)
			} else {
				wrapped = append(wrapped, " "+contIndent+row)
			}
		}
	}
	if len(wrapped) > maxBannerRows {
		hidden := len(wrapped) - maxBannerRows
		wrapped = wrapped[:maxBannerRows]
		wrapped = append(wrapped, " "+p.fg("dim", fmt.Sprintf("%s… +%d more %s (%s to expand)", contIndent, hidden, pluralize("line", hidden), expandKeyHint())))
	}

	out := make([]string, 0, len(wrapped)+4)
	out = append(out, "")
	out = append(out, rule)
	out = append(out, wrapped...)
	out = append(out, " "+p.fg("dim", "Dismissed when you send your next message."))
	out = append(out, rule)
	return clampRows(out, width)
}

// RenderStatusBandWidget builds the above-editor widget rows directly above
// PiG's editor:
//   - Optional pinned provider-error banner (cleared on next user turn)
//   - Optional `  ⎋ Working…` row while working
//   - The 1-row omp status band with embedded context gauge
func RenderStatusBandWidget(theme sdk.UITheme, d StatusLineData, width int) []string {
	if width <= 0 {
		return nil
	}
	var rows []string
	if d.PinnedError != "" {
		rows = append(rows, RenderErrorBanner(theme, d.PinnedError, width)...)
		rows = append(rows, "")
	}
	if d.Working {
		msg := d.WorkingMessage
		if msg == "" {
			msg = defaultWorkingMessage
		}
		rows = append(rows, RenderWorkingRow(theme, msg, width))
	}
	if band := RenderStatusBand(theme, d, width); band != "" {
		rows = append(rows, band)
	}
	return clampRows(rows, width)
}

// RenderWorkingRow draws omp's terminal working row placed above the band:
// `  ⎋ Working…` (or the current intent label), muted foreground, clamped to width.
// upstream: packages/coding-agent/src/modes/interactive-mode.ts:9246-9259,
// packages/tui/src/components/loader.ts
func RenderWorkingRow(theme sdk.UITheme, message string, width int) string {
	if width <= 0 {
		return ""
	}
	p := paletteFromTheme(theme)
	msg := flattenLine(message)
	if msg == "" {
		msg = defaultWorkingMessage
	}
	// Loader pads 1 space on the left; interactive-mode.ts:9258 passes
	// ` ${appKey("app.interrupt")}` (" ⎋"), producing two leading spaces before ⎋.
	line := " " + p.fg("muted", " ⎋") + " " + p.fg("muted", msg)
	return clampRow(line, width)
}

// formatBrandTimer mirrors packages/tui/src/status-line/segments.ts:257-262:
// whole seconds → minutes → hours (capped at 99h).
func formatBrandTimer(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	secs := int(d / time.Second)
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	if secs < 3600 {
		return fmt.Sprintf("%dm", secs/60)
	}
	hours := secs / 3600
	if hours > 99 {
		hours = 99
	}
	return fmt.Sprintf("%dh", hours)
}

// formatEmbeddedContextPercent mirrors packages/tui/src/status-line/component.ts:643-645:
// one decimal when 0 < pct < 1 (e.g. `0.4%`), otherwise rounded integer (`3%`).
func formatEmbeddedContextPercent(percent float64) string {
	if percent > 0 && percent < 1 {
		return fmt.Sprintf("%.1f%%", percent)
	}
	return fmt.Sprintf("%d%%", int(math.Round(percent)))
}

// embeddedContextGaugeMinWidth mirrors packages/tui/src/status-line/component.ts:648-651.
func embeddedContextGaugeMinWidth(percent *float64, contextWindow int) int {
	pctW := 0
	if percent != nil {
		pctW = len(formatEmbeddedContextPercent(*percent)) + 2
	}
	return pctW + len(formatNumber(contextWindow)) + 2
}

// defaultCompactionBoundaries mirrors omp's default compaction boundary
// positions when no per-model override is active:
//   - effectiveReserveTokens = max(floor(window * 0.15), 16384) (compaction.ts:341-343)
//   - resolveBudgetReserveTokens falls back to proportionalReserveTokens when
//     defaulted reserve >= window - proportionalReserveTokens (compaction.ts:357-366)
//   - thresholdTokens = window - reserveTokens (compaction.ts:403-405)
//   - speculationLeadTokens = clamp(floor(threshold * 0.125), 8192, 32000) (speculation-lead.ts:13-24)
func defaultCompactionBoundaries(contextWindow int) (speculationPct float64, thresholdPct float64, ok bool) {
	if contextWindow <= 0 {
		return 0, 0, false
	}
	const defaultReserve = 16384
	propReserve := max(1, int(math.Floor(float64(contextWindow)*0.15)))
	reserve := max(propReserve, defaultReserve)
	if reserve >= contextWindow-propReserve || reserve >= contextWindow {
		reserve = propReserve
	}
	threshold := max(0, min(contextWindow-1, contextWindow-reserve))
	if threshold <= 0 || threshold > contextWindow {
		return 0, 0, false
	}
	lead := int(math.Floor(float64(threshold) * 0.125))
	if lead < 8192 {
		lead = 8192
	}
	if lead > 32000 {
		lead = 32000
	}
	specTokens := max(0, threshold-lead)
	return (float64(specTokens) / float64(contextWindow)) * 100.0,
		(float64(threshold) / float64(contextWindow)) * 100.0,
		true
}

// buildContextGaugeFill ports packages/tui/src/status-line/component.ts:3488-3636
// (`#buildContextGaugeFill`) for embedded mode (the omp v19.1.1 default).
func (p palette) buildContextGaugeFill(gapWidth int, d StatusLineData, embedContext bool) string {
	if gapWidth <= 0 {
		return ""
	}
	usedColor := p.fgAnsi("borderAccent")
	horizontal := unicodeSymbols.boxHoriz
	if d.ContextLineMode == "off" || (d.ContextPercent == nil && d.ContextWindow <= 0) {
		return "\x1b[49m" + usedColor + strings.Repeat(horizontal, gapWidth) + "\x1b[39m"
	}

	clampedPct := 0.0
	if d.ContextPercent != nil {
		clampedPct = math.Min(100, math.Max(0, *d.ContextPercent))
	}
	percentOverflow := d.ContextPercent != nil && *d.ContextPercent > 100

	percentLabel := ""
	windowLabel := ""
	percentStart := -1
	windowStart := -1
	scaleWidth := gapWidth
	if embedContext && d.ContextWindow > 0 {
		candidatePct := ""
		if d.ContextPercent != nil {
			if percentOverflow {
				candidatePct = formatEmbeddedContextPercent(*d.ContextPercent)
			} else {
				candidatePct = formatEmbeddedContextPercent(clampedPct)
			}
		}
		candidateWin := formatNumber(d.ContextWindow)
		if gapWidth >= embeddedContextGaugeMinWidth(d.ContextPercent, d.ContextWindow) {
			percentLabel = candidatePct
			windowLabel = candidateWin
			if percentOverflow {
				percentStart = gapWidth - len(percentLabel)
				windowStart = percentStart - 1 - len(windowLabel)
			} else {
				windowStart = gapWidth - len(windowLabel) - 1
			}
			scaleWidth = windowStart
		}
	}

	usedCount := min(scaleWidth, max(1, int(math.Round((clampedPct/100.0)*float64(scaleWidth)))))
	unusedColor := p.fgAnsi("border")

	speculationIdx := -1
	thresholdIdx := -1
	mode := d.ContextLineMode
	if mode == "" {
		mode = "embedded"
	}
	if (mode == "annotated" || mode == "embedded") && !d.AutoCompactDisabled && gapWidth >= 8 && scaleWidth > 0 {
		if specPct, threshPct, ok := defaultCompactionBoundaries(d.ContextWindow); ok {
			cellFor := func(pct float64) int {
				return min(scaleWidth-1, max(0, int(math.Round((pct/100.0)*float64(scaleWidth)))))
			}
			thresholdIdx = cellFor(threshPct)
			speculationIdx = cellFor(specPct)
			if speculationIdx == thresholdIdx {
				speculationIdx = -1
			}
		}
	}

	if percentLabel != "" && percentStart < 0 {
		maxStart := scaleWidth - len(percentLabel) - 1
		preferredStart := min(maxStart, max(1, usedCount))
		overlapsBoundary := func(start int) bool {
			end := start + len(percentLabel)
			return (speculationIdx >= start && speculationIdx < end) || (thresholdIdx >= start && thresholdIdx < end)
		}
		for dist := 0; dist <= maxStart; dist++ {
			left := preferredStart - dist
			if left >= 1 && !overlapsBoundary(left) {
				percentStart = left
				break
			}
			if dist == 0 {
				continue
			}
			right := preferredStart + dist
			if right <= maxStart && !overlapsBoundary(right) {
				percentStart = right
				break
			}
		}
	}

	speculationGlyph := unicodeSymbols.contextSpeculation
	thresholdGlyph := unicodeSymbols.contextCompaction
	speculationColor := p.fgAnsi("muted")
	overflowColor := p.fgAnsi("error")
	thresholdColor := usedColor
	if p.titanium {
		if accent, ok := titaniumFgColors["borderAccent"]; ok {
			thresholdColor = rgbFgAnsi(adjustHsvRGB(accent, 0.7, 0.75), p.mode)
		}
	}

	percentEnd := -1
	if percentStart >= 0 {
		percentEnd = percentStart + len(percentLabel)
	}
	windowEnd := -1
	if windowStart >= 0 {
		windowEnd = windowStart + len(windowLabel)
	}

	var out strings.Builder
	out.WriteString("\x1b[49m")
	activeColor := ""
	i := 0
	for i < gapWidth {
		var color, text string
		switch {
		case i >= percentStart && i < percentEnd:
			end := min(percentEnd, gapWidth)
			if percentOverflow {
				color = overflowColor
			} else {
				color = usedColor
			}
			text = percentLabel[i-percentStart : end-percentStart]
			i = end
		case i == thresholdIdx:
			color = thresholdColor
			text = thresholdGlyph
			i++
		case i == speculationIdx:
			color = speculationColor
			text = speculationGlyph
			i++
		default:
			inWindow := i >= windowStart && i < windowEnd
			var end int
			if inWindow {
				end = min(windowEnd, gapWidth)
			} else if i < usedCount {
				end = min(usedCount, gapWidth)
			} else {
				end = gapWidth
			}
			if percentStart > i && percentStart < end {
				end = percentStart
			}
			if thresholdIdx > i && thresholdIdx < end {
				end = thresholdIdx
			}
			if speculationIdx > i && speculationIdx < end {
				end = speculationIdx
			}
			if !inWindow && windowStart > i && windowStart < end {
				end = windowStart
			}
			if inWindow {
				color = thresholdColor
				text = windowLabel[i-windowStart : end-windowStart]
			} else {
				if i < usedCount {
					color = usedColor
				} else {
					color = unusedColor
				}
				text = strings.Repeat(horizontal, end-i)
			}
			i = end
		}
		if color != activeColor {
			out.WriteString(color)
			activeColor = color
		}
		out.WriteString(text)
	}
	out.WriteString("\x1b[39m")
	return out.String()
}

// RenderStatusBand renders omp's band-composer top row:
//
//	π > ◑ Proof 1 > 🗑 path ▶─0.4%───────────╎───────┃─────────128K─
//
// With `statusLine.contextLine` == "embedded" (the default) and contextWindow > 0,
// the standalone `◫ 0.4%/128K` segment is omitted and drawn into the gauge fill.
// upstream: packages/tui/src/status-line/component.ts:3201-3476
func RenderStatusBand(theme sdk.UITheme, d StatusLineData, width int) string {
	p := paletteFromTheme(theme)
	if width <= 0 {
		return ""
	}
	contextMode := d.ContextLineMode
	if contextMode == "" {
		contextMode = "embedded"
	}
	embedContext := contextMode == "embedded" && d.ContextWindow > 0

	leftParts, leftIDs := buildStatusLineLeftSegments(p, d, !embedContext)
	if len(leftParts) == 0 {
		return ""
	}

	bgAnsi := p.bgAnsi("statusLineBg")
	transparentBg := bgAnsi == "" || bgAnsi == "\x1b[49m"
	fgAnsi := p.fgAnsi("text")
	sepAnsi := p.fgAnsi("statusLineSep")
	if sepAnsi == "" {
		sepAnsi = p.fgAnsi("dim")
	}
	sep := unicodeSymbols.sepPowerlineThinL
	sepW := tui.VisibleWidth(sep)

	capGlyph := ""
	capW := 0
	if !transparentBg {
		capGlyph = unicodeSymbols.sepPowerlineLeft // ▶
		capW = tui.VisibleWidth(capGlyph)
	}

	groupWidth := func(parts []string) int {
		if len(parts) == 0 {
			return 0
		}
		total := 2 + capW // leading + trailing space + right cap
		for idx, part := range parts {
			total += tui.VisibleWidth(part)
			if idx > 0 {
				total += sepW + 2
			}
		}
		return total
	}

	minGaugeW := 1
	if embedContext {
		minGaugeW = embeddedContextGaugeMinWidth(d.ContextPercent, d.ContextWindow)
	}

	// Overflow handling (status-line/component.ts:3378-3431):
	// First shrink path if present, then drop non-path segments right-to-left.
	leftW := groupWidth(leftParts)
	if leftW+minGaugeW > width {
		pathIdx := -1
		for idx, id := range leftIDs {
			if id == "path" {
				pathIdx = idx
				break
			}
		}
		if pathIdx >= 0 {
			curPathW := tui.VisibleWidth(leftParts[pathIdx])
			overflow := leftW + minGaugeW - width
			minPathW := 8
			if shrinkable := curPathW - minPathW; shrinkable > 0 {
				shrinkBy := min(shrinkable, overflow)
				newMaxLen := max(4, min(40, curPathW)-shrinkBy)
				leftParts[pathIdx] = formatPathSegment(p, d.Cwd, newMaxLen)
				leftW = groupWidth(leftParts)
			}
		}
		for leftW+minGaugeW > width && len(leftParts) > 1 {
			dropIdx := len(leftParts) - 1
			for i := len(leftIDs) - 1; i >= 0; i-- {
				if leftIDs[i] != "path" {
					dropIdx = i
					break
				}
			}
			leftParts = append(leftParts[:dropIdx], leftParts[dropIdx+1:]...)
			leftIDs = append(leftIDs[:dropIdx], leftIDs[dropIdx+1:]...)
			leftW = groupWidth(leftParts)
		}
	}

	var leftGroup strings.Builder
	leftGroup.WriteString(bgAnsi)
	leftGroup.WriteString(fgAnsi)
	leftGroup.WriteByte(' ')
	sepJoin := " " + sepAnsi + sep + fgAnsi + " "
	leftGroup.WriteString(strings.Join(leftParts, sepJoin))
	leftGroup.WriteString(" \x1b[0m")
	if capGlyph != "" {
		capPrefix := strings.Replace(bgAnsi, "\x1b[48;", "\x1b[38;", 1)
		leftGroup.WriteString(capPrefix)
		leftGroup.WriteString(capGlyph)
		leftGroup.WriteString("\x1b[0m")
	}

	gapWidth := max(0, width-leftW)
	if gapWidth == 0 {
		return clampRow(leftGroup.String(), width)
	}
	gauge := p.buildContextGaugeFill(gapWidth, d, embedContext)
	return clampRow(leftGroup.String()+gauge, width)
}
