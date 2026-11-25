package frontend

import (
	"math"
	"strconv"
	"strings"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/stencil-hq/tern-sdk/go/tern"
	"github.com/stencil-hq/tern-sdk/go/tern/ui"
)

// ompComposerPlaceholder is omp's untitled-session placeholder (custom-editor.ts:54).
const ompComposerPlaceholder = "What are we cooking?"

// ompComposerElement builds omp's composer column (custom-editor.ts:1112):
//
//	col(role="omp.editor", tone="pending" while running):
//	  - meter(key="context", role="omp.composer.context", style="bar", ...)  [when footer present]
//	  - row(key="line", role="omp.composer.line", align="start", gap="sm"):
//	      - editor(key="input", placeholder, sendable, handlers)
//	  - row(key="bar", role="omp.composer.bar", gap="sm", align="center"):
//	      - row(key="model", role="omp.composer.model", ...)                 [when footer present]
//	          - effort(key="effort", role="omp.composer.model.effort", ...)  [when thinkingLevel != ""]
//	            OR icon(key="icon", name="model")                            [when thinkingLevel == ""]
//	          - text(key="name", spans=..., wrap="none")
//	          - icon(key="chev", name="chev")
//	      - status(key="extras", role="omp.composer.extras", transparent=true, grow=1)
//	      - text(key="usage", role="omp.composer.usage", ...)                [when footer present]
//	      - text(key="stop", role="omp.composer.stop", ...)                  [when running]
//	        OR kbd(key="send", role="omp.composer.send", keys=["enter"], ...) [when idle]
//
// In omp, the composer's input is a child keyed "input" under row "line", so
// its TSP id is "<editorTID>.line.input". Handlers for edit, undo, send and
// focus live on that input node. When PiG is working (running == true), the
// composer column gets tone "pending" and the submit control swaps from Send
// to Stop (non-interactive since frontend.Env has no interrupt callback).
func (s *session) ompComposerElement(e *entry, editor fe.Editor, interactive bool) tern.Element {
	running := s.progress
	placeholder := editor.Placeholder
	if placeholder == "" {
		placeholder = ompComposerPlaceholder
	}
	inputCommon := ui.Common{Key: "input"}
	inputField := ui.Field{
		Placeholder: ui.T(placeholder),
		Sendable:    interactive && editor.Sendable,
	}
	if !interactive {
		cursor := editor.Cursor
		inputField.Text = editor.Text
		inputField.Cursor = &cursor
	}
	inputNode := ui.Editor{Common: inputCommon, Field: inputField, MaxLines: 18}.Node()
	if interactive {
		inputNode.Handlers = s.editorHandlers(e)
	}
	line := ui.Row{
		Common:   ui.Common{Key: "line", Role: "omp.composer.line"},
		Align:    ui.AlignStart,
		Gap:      ui.GapSM,
		Children: []tern.Element{inputNode},
	}

	var barChildren []tern.Element
	var children []tern.Element
	if s.hasFooter {
		children = append(children, ompContextMeter(s.footer, interactive, s.runCommand))
		barChildren = append(barChildren, ompComposerModelChip(s.footer, s.resolveModelDisplayName(s.footer.Model), interactive, s.runCommand))
	}
	barChildren = append(barChildren, ompComposerExtras(s.hasFooter, s.footer))
	if s.hasFooter {
		barChildren = append(barChildren, ompComposerUsageText(s.footer, interactive, s.runCommand))
	}
	if running {
		barChildren = append(barChildren, ompComposerStopControl())
	} else {
		barChildren = append(barChildren, ompComposerSendControl(editor, interactive, s.submitEditorDraft))
	}
	bar := ui.Row{
		Common:   ui.Common{Key: "bar", Role: "omp.composer.bar"},
		Gap:      ui.GapSM,
		Align:    ui.AlignCenter,
		Children: barChildren,
	}
	children = append(children, line, bar)

	colCommon := ui.Common{Key: e.id, Role: "omp.editor"}
	if running {
		colCommon.Tone = ui.TonePending
	}
	return ui.Col{Common: colCommon, Children: children}
}

// editorInputID is the TSP id of the editor's actual `editor` node: for pig-tern
// it is e.tid ("dock.editor"), and for ompRoles it is the "input" child inside
// the "line" row ("dock.editor.line.input").
func (s *session) editorInputID(e *entry) string {
	if s.ompRoles {
		return "dock.editor.editor.line.input"
	}
	return e.tid
}

// submitEditorDraft submits the editor's current text through Env.Send, used by
// clicks on the omp.composer.send control.
func (s *session) submitEditorDraft() {
	s.mu.Lock()
	ok := s.sendable() && !s.closed && !s.fellBack
	text := s.editor.Text
	s.mu.Unlock()
	if ok && strings.TrimSpace(text) != "" && s.env.Send != nil {
		s.env.Send(text)
	}
}

// ompContextMeter builds omp's context hairline meter above the composer
// (status-line/component.ts:3925-3977).
func ompContextMeter(footer fe.Footer, interactive bool, run func(string)) ui.Meter {
	usage := footer.ContextUsage
	var value *float64
	var label ui.Rich
	if !usage.Unknown && usage.ContextWindow > 0 {
		v := math.Min(1, usage.Percent/100)
		value = &v
		label = ui.T(strconv.Itoa(int(math.Round(usage.Percent))) + "%")
	}
	var total ui.Rich
	if usage.ContextWindow > 0 {
		total = ui.T(ompFormatNumber(usage.ContextWindow))
	}
	lines := make([]string, 0, 3)
	if usage.Unknown {
		lines = append(lines, "Context usage unknown")
	} else {
		lines = append(lines, "Context "+strconv.Itoa(int(math.Round(usage.Percent)))+"% used")
	}
	if usage.ContextWindow > 0 {
		lines = append(lines, ompFormatNumber(usage.Tokens)+" of "+ompFormatNumber(usage.ContextWindow)+" tokens")
	} else {
		lines = append(lines, "No context window")
	}
	if footer.AutoCompact {
		lines = append(lines, "Auto-compact enabled")
	}
	common := ui.Common{
		Key:   "context",
		Role:  "omp.composer.context",
		Title: strings.Join(lines, "\n"),
	}
	if interactive && run != nil {
		common.Actions = &ui.Actions{Click: "status.context"}
		common.OnClick = func(*tern.Action) { run("/context") }
	}
	return ui.Meter{
		Common:     common,
		Value:      value,
		Style:      ui.MeterBar,
		Thresholds: ompContextThresholds(usage.ContextWindow),
		Label:      label,
		Total:      total,
	}
}

// ompContextThresholds mirrors omp's getContextMeterThresholds
// (chrome/context-thresholds.ts:71-76).
func ompContextThresholds(contextWindow int) *ui.Thresholds {
	warn := ompThresholdShare(contextWindow, 50, 150_000)
	bad := ompThresholdShare(contextWindow, 90, 500_000)
	return &ui.Thresholds{Warn: &warn, Bad: &bad}
}

func ompThresholdShare(contextWindow int, pctThreshold, tokenThreshold float64) float64 {
	pct := pctThreshold
	if contextWindow > 0 {
		if tokPct := (tokenThreshold / float64(contextWindow)) * 100; tokPct < pct {
			pct = tokPct
		}
	}
	return pct / 100
}

// ompComposerModelChip builds omp's model selector chip in the composer bar
// (custom-editor.ts:1262-1294). When the model reasons (ThinkingLevel != ""),
// the effort glyph sits as the model chip's leading icon with role
// "omp.composer.model.effort" and its own click handler for /thinking; clicking
// the model chip itself opens PiG's /model picker.
func ompComposerModelChip(footer fe.Footer, displayName string, interactive bool, run func(string)) ui.Row {
	common := ui.Common{
		Key:   "model",
		Role:  "omp.composer.model",
		Title: "Switch model  ⌥P",
	}
	if interactive {
		common.Actions = &ui.Actions{Click: "status.model"}
		common.OnClick = func(*tern.Action) { run("/model") }
	}
	var lead tern.Element
	if footer.ThinkingLevel != "" {
		effortCommon := ui.Common{
			Key:   "effort",
			Role:  "omp.composer.model.effort",
			Title: "Thinking effort: " + footer.ThinkingLevel + "  ⇧⇥",
		}
		if interactive {
			effortCommon.Actions = &ui.Actions{Click: "thinking.cycle"}
			effortCommon.OnClick = func(*tern.Action) { run("/thinking") }
		}
		lead = ui.Effort{Common: effortCommon, Level: ui.EffortLevel(footer.ThinkingLevel)}
	} else {
		lead = ui.Icon{Common: ui.Common{Key: "icon"}, Name: ui.IconName("model")}
	}
	return ui.Row{
		Common: common,
		Gap:    ui.GapXS,
		Align:  ui.AlignCenter,
		Children: []tern.Element{
			lead,
			ui.Text{Common: ui.Common{Key: "name"}, Spans: ompModelSpansWithName(footer, displayName, false), Wrap: ui.WrapNone},
			ui.Icon{Common: ui.Common{Key: "chev"}, Name: ui.IconChev},
		},
	}
}

// ompComposerExtras builds omp's flexible status strip in the composer bar
// (status-line/component.ts:3655-3684, 3765): transparent status with role
// "omp.composer.extras" and grow=1, holding git branch and extension statuses
// as `seg` nodes with role "omp.composer.fact".
func ompComposerExtras(hasFooter bool, footer fe.Footer) ui.Status {
	var segs []tern.Element
	if hasFooter {
		if footer.GitBranch != "" {
			segs = append(segs, ui.Seg{
				Common:   ui.Common{Key: "git", Role: "omp.composer.fact"},
				Icon:     ui.IconBranch,
				Spans:    []ui.Span{{T: footer.GitBranch, S: "statusLineGitClean"}},
				Priority: 1001,
			})
		}
		for i, st := range footer.ExtensionStatuses {
			text := strings.TrimSpace(stripANSI(st))
			if text == "" {
				continue
			}
			if seg, ok := ompModeFactSeg(text); ok {
				segs = append(segs, seg)
				continue
			}
			segs = append(segs, ui.Seg{
				Common:   ui.Common{Key: "hook-" + strconv.Itoa(i), Role: "omp.composer.fact"},
				Spans:    []ui.Span{{T: text}},
				Priority: 0,
			})
		}
	}
	return ui.Status{
		Common:      ui.Common{Key: "extras", Role: "omp.composer.extras", Grow: 1},
		Transparent: true,
		Children:    segs,
	}
}

// ompModeFactSeg converts PiG's extension status strings emitted by
// `internal/modes` (`"PLAN"`, `"PLAN (paused)"`, `"GOAL ..."`, `"GOAL (paused)"`)
// into omp's native `mode` segment (upstream: status-line/segments.ts:505-520,
// status-line/component.ts:80 `priority: 8`).
func ompModeFactSeg(text string) (ui.Seg, bool) {
	switch {
	case text == "PLAN":
		return ui.Seg{
			Common:   ui.Common{Key: "mode", Role: "omp.composer.fact"},
			Icon:     ui.IconName("plan"),
			Spans:    []ui.Span{{T: "Plan", S: "accent"}},
			Priority: 8,
		}, true
	case text == "PLAN (paused)":
		return ui.Seg{
			Common:   ui.Common{Key: "mode", Role: "omp.composer.fact"},
			Icon:     ui.IconName("plan"),
			Spans:    []ui.Span{{T: "Plan (paused)", S: "warning"}},
			Priority: 8,
		}, true
	case text == "GOAL (paused)":
		return ui.Seg{
			Common:   ui.Common{Key: "mode", Role: "omp.composer.fact"},
			Icon:     ui.IconName("goal"),
			Spans:    []ui.Span{{T: "Goal (paused)", S: "warning"}},
			Priority: 8,
		}, true
	case strings.HasPrefix(text, "GOAL"):
		label := "Goal" + text[len("GOAL"):]
		return ui.Seg{
			Common:   ui.Common{Key: "mode", Role: "omp.composer.fact"},
			Icon:     ui.IconName("goal"),
			Spans:    []ui.Span{{T: label, S: "accent"}},
			Priority: 8,
		}, true
	default:
		return ui.Seg{}, false
	}
}

// ompComposerUsageText builds omp's session cost text in the composer bar
// (status-line/component.ts:3740-3755).
func ompComposerUsageText(footer fe.Footer, interactive bool, run func(string)) ui.Text {
	cost := ompCostSummary(footer)
	title := ""
	if cost != "" {
		title = "Session cost " + cost
	}
	common := ui.Common{
		Key:   "usage",
		Role:  "omp.composer.usage",
		Title: title,
	}
	if interactive {
		common.Actions = &ui.Actions{Click: "status.cost"}
		common.OnClick = func(*tern.Action) { run("/usage") }
	}
	return ui.Text{
		Common: common,
		Text:   cost,
		Wrap:   ui.WrapNone,
	}
}

// ompComposerSendControl builds omp's Enter keycap send control in the composer
// bar (custom-editor.ts:1366-1377).
func ompComposerSendControl(editor fe.Editor, interactive bool, submit func()) ui.Kbd {
	common := ui.Common{
		Key:   "send",
		Role:  "omp.composer.send",
		Title: "Send  ⏎",
	}
	if !editor.Sendable {
		common.Tone = ui.ToneMuted
	}
	if interactive && editor.Sendable {
		common.Actions = &ui.Actions{Click: "submit"}
		common.OnClick = func(*tern.Action) { submit() }
	}
	return ui.Kbd{Common: common, Keys: []string{"enter"}}
}

// ompComposerStopControl builds omp's Stop indicator shown in place of Send
// while the agent is working (upstream: prompt/custom-editor.ts:1491-1503).
// PiG's frontend.Env has no interrupt callback, so clicking is omitted while
// the Escape key still interrupts via the PTY.
func ompComposerStopControl() ui.Text {
	return ui.Text{
		Common: ui.Common{
			Key:   "stop",
			Role:  "omp.composer.stop",
			Tone:  ui.ToneError,
			Title: "Stop  esc",
		},
		Text: "Stop",
	}
}

// resolveModelDisplayName returns the human display name for modelID when a
// derived Piglet provides Options.ModelDisplayName, falling back to modelID.
func (s *session) resolveModelDisplayName(modelID string) string {
	if s != nil && s.modelDisplayName != nil && modelID != "" {
		if name := strings.TrimSpace(s.modelDisplayName(modelID)); name != "" {
			return name
		}
	}
	return modelID
}

// ompModelSpans formats the model name as omp's model segment spans
// (status-line/segments.ts:391-404). When includeThinking is true (status bar),
// the thinking level follows after " · ".
func ompModelSpans(footer fe.Footer, includeThinking bool) []ui.Span {
	return ompModelSpansWithName(footer, footer.Model, includeThinking)
}

func ompModelSpansWithName(footer fe.Footer, modelName string, includeThinking bool) []ui.Span {
	name := modelName
	if name == "" {
		name = footer.Model
	}
	if name == "" {
		name = "no-model"
	}
	name = strings.TrimPrefix(name, "Claude ")
	if footer.Provider != "" {
		name = "(" + footer.Provider + ") " + name
	}
	if footer.Routed.Model != "" {
		name += " → " + strings.TrimPrefix(footer.Routed.Model, "Claude ")
	}
	spans := []ui.Span{{T: name, S: "statusLineModel"}}
	if includeThinking && footer.ThinkingLevel != "" {
		spans = append(spans,
			ui.Span{T: " · ", S: "dim"},
			ui.Span{T: footer.ThinkingLevel, S: ompThinkingLevelToken(footer.ThinkingLevel)},
		)
	}
	return spans
}

// ompThinkingLevelToken maps a thinking level name to omp's theme span token.
func ompThinkingLevelToken(level string) string {
	switch level {
	case "off":
		return "thinkingOff"
	case "minimal":
		return "thinkingMinimal"
	case "low":
		return "thinkingLow"
	case "medium":
		return "thinkingMedium"
	case "high":
		return "thinkingHigh"
	case "xhigh", "max":
		return "thinkingXhigh"
	default:
		return "muted"
	}
}

// ompCostSummary formats PiG's UsageTotals cost as omp's billing summary
// (status-line/metrics.ts:59-104), with two decimal places.
func ompCostSummary(footer fe.Footer) string {
	cost := footer.UsageTotals.Cost
	hasCost := cost != 0 && !math.IsNaN(cost)
	if !hasCost && !footer.UsingSubscription {
		return ""
	}
	if !hasCost {
		return "(sub)"
	}
	formatted := toFixed(cost, 2)
	if footer.UsingSubscription {
		return "S" + formatted
	}
	return "$" + formatted
}

// ompFormatNumber mirrors omp's formatNumber (packages/utils/src/format.ts:38-52).
func ompFormatNumber(n int) string {
	switch {
	case n < 1_000:
		return strconv.Itoa(n)
	case n < 10_000:
		return trimDotZero(toFixed(float64(n)/1_000, 1)) + "K"
	case n < 1_000_000:
		return strconv.Itoa(int(math.Floor(float64(n)/1_000+0.5))) + "K"
	case n < 10_000_000:
		return trimDotZero(toFixed(float64(n)/1_000_000, 1)) + "M"
	case n < 1_000_000_000:
		return strconv.Itoa(int(math.Floor(float64(n)/1_000_000+0.5))) + "M"
	default:
		return trimDotZero(toFixed(float64(n)/1_000_000_000, 1)) + "B"
	}
}

func trimDotZero(s string) string {
	return strings.TrimSuffix(s, ".0")
}

// rebuildOmpEditor triggers a rebuild of the dock editor entry when ompRoles is
// active and composer-fed state (footer or running status) changed.
func (s *session) rebuildOmpEditor() {
	if !s.ompRoles || !s.hasEditor {
		return
	}
	for _, e := range s.dock {
		if _, ok := e.node.(fe.Editor); ok {
			s.rebuild(e, e.node, true)
			return
		}
	}
}

// sameFooterComposerData reports whether two Footer snapshots produce the same
// omp composer bar, extras, and context meter.
func sameFooterComposerData(a, b fe.Footer) bool {
	if a.Model != b.Model ||
		a.Provider != b.Provider ||
		a.ThinkingLevel != b.ThinkingLevel ||
		a.Routed != b.Routed ||
		a.GitBranch != b.GitBranch ||
		a.ContextUsage != b.ContextUsage ||
		a.AutoCompact != b.AutoCompact ||
		a.UsageTotals.Cost != b.UsageTotals.Cost ||
		a.UsingSubscription != b.UsingSubscription ||
		len(a.ExtensionStatuses) != len(b.ExtensionStatuses) {
		return false
	}
	for i := range a.ExtensionStatuses {
		if a.ExtensionStatuses[i] != b.ExtensionStatuses[i] {
			return false
		}
	}
	return true
}

// ompFooterSuppressed reports whether dock.footer is omitted under ompRoles.
// Upstream status-line/component.ts:3810 ("on a TSP terminal, which draws no
// status strip"): omp NEVER mounts a separate dock status bar in Tern — even
// while a picker/selector replaces the editor — so suppress dock.footer
// whenever ompRoles is active.
func (s *session) ompFooterSuppressed() bool {
	return s.ompRoles
}

// syncOmpFooterMount reconciles dock.footer whenever the editor mounts or
// unmounts under ompRoles: entering composer mode drops pending footer ops and
// unmounts dock.footer; leaving composer mode (e.g. a selector taking the
// editor's place) rebuilds dock.footer fresh so the status strip reappears.
func (s *session) syncOmpFooterMount() {
	for _, e := range s.dock {
		if _, ok := e.node.(fe.Footer); ok {
			if s.ompFooterSuppressed() {
				s.dropPending(e.tid)
				e.view, e.unsent, e.raw = tern.Node{}, true, rawProps{}
				s.dirty = true
			} else {
				e.unsent = true
				s.rebuild(e, e.node, true)
			}
			return
		}
	}
}
