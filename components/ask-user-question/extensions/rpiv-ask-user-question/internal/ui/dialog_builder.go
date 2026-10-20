package ui

import (
	"strings"

	tui "github.com/MichaelKinsy/pigpen/components/pitui"
)

const (
	HintPartEnter   = "Enter to select"
	HintPartNav     = "↑/↓ to navigate"
	HintPartNewLine = "Shift+Enter for newline"
	HintPartClear   = "Ctrl+U to clear"
	HintPartToggle  = "Space to toggle"
	HintPartNotes   = "n to add notes"
	HintPartTab     = "Tab to switch questions"
	HintPartCancel  = "Esc to cancel"
)

// KeyPlaceholder templates the collapse/expand hint copy: the shortcut is configurable (collapseKey) and the
// locale tables carry the same `{key}` placeholder; call sites replace it with FormatKeySpecForDisplay(collapseKey)
// after lookup, so per-locale word order is preserved.
const (
	KeyPlaceholder           = "{key}"
	HintPartCollapseTemplate = KeyPlaceholder + " to collapse"
	HintPartExpandTemplate   = KeyPlaceholder + " to expand"
	CollapsedHintTemplate    = HintPartExpandTemplate + " · " + HintPartCancel
)

// HintPartCollapse is the default-key (`Ctrl+]`) rendering of the collapse template, for tests and default-config
// assertions.
var HintPartCollapse = strings.Replace(HintPartCollapseTemplate, KeyPlaceholder, FormatKeySpecForDisplay(DefaultCollapseKey), 1)

// HintSingle and HintMulti are the resting core hint for NON-multiSelect question tabs only: BuildHintText drops
// NOTES while the notes editor is open or the "Type something." row is capturing text, and on multiSelect tabs it
// interleaves TOGGLE between NAV and NOTES. The collapse affordance is appended AFTER cancel so the resting core
// stays a contiguous prefix substring of the rendered line.
var (
	HintSingle = strings.Join([]string{HintPartEnter, HintPartNav, HintPartNotes, HintPartCancel}, " · ")
	HintMulti  = strings.Join([]string{HintPartEnter, HintPartNav, HintPartNotes, HintPartTab, HintPartCancel}, " · ")
)

const (
	ReviewHeading           = "Review your answers"
	ReadyPrompt             = "Ready to submit your answers?"
	IncompleteWarningPrefix = "⚠ Answer remaining questions before submitting:"
)

const (
	overflowUp   = "↑"
	overflowDown = "↓"
	overflowBoth = "↕"
)

// renderFitsTerminal is the no-overflow path: append the residual spacer rows after the footer.
func renderFitsTerminal(natural []string, spacerRows int) []string {
	if spacerRows <= 0 {
		return natural
	}
	return append(append(make([]string, 0, len(natural)+spacerRows), natural...), make([]string, spacerRows)...)
}

// renderChromeOnly: terminal too small for any middle content; show just chrome, hard-clamped to termRows.
func renderChromeOnly(natural []string, topFixed, bottomFixed, termRows int) []string {
	chromeOnly := append(append([]string{}, natural[:min(topFixed, len(natural))]...), natural[max(0, len(natural)-bottomFixed):]...)
	if len(chromeOnly) > termRows {
		return chromeOnly[:max(0, termRows)]
	}
	return chromeOnly
}

// computeScrollStart is the scroll window start, centered on the focused option; top-anchored when there is no
// interactive focus.
func computeScrollStart(hasRange bool, rangeStart, rangeEnd, headingCount, availableMiddle, middleRows int) int {
	if !hasRange {
		return 0
	}
	focusedRowInMiddle := headingCount + rangeStart
	focusedHeight := rangeEnd - rangeStart
	// Center the focused item vertically in the available middle space.
	idealStart := focusedRowInMiddle - max(0, availableMiddle-focusedHeight)/2
	return max(0, min(idealStart, middleRows-availableMiddle))
}

// decorateOverflow marks the scroll window edges with overflow arrows; combined ↕ on a single-row middle.
func decorateOverflow(scrollableMiddle []string, hasUp, hasDown bool, theme Theme) {
	if hasUp && hasDown && len(scrollableMiddle) == 1 {
		// Single-row middle: combined ↕ avoids the prior collision where ↓ overwrote ↑.
		scrollableMiddle[0] = theme.Fg("dim", overflowBoth)
		return
	}
	if hasUp && len(scrollableMiddle) > 0 {
		scrollableMiddle[0] = theme.Fg("dim", overflowUp)
	}
	if hasDown && len(scrollableMiddle) > 0 {
		scrollableMiddle[len(scrollableMiddle)-1] = theme.Fg("dim", overflowDown)
	}
}

// DialogState is the canonical state the dialog renders.
type DialogState = QuestionnaireState

// DialogProps is the per-tick projection of dialog state. Written by the adapter; read by the strategy thunk.
type DialogProps struct {
	State             *DialogState
	ActivePreviewPane StatefulView[PreviewPaneProps]
}

// DialogConfig is the construction-time config of DialogView, frozen after construction.
type DialogConfig struct {
	Theme     Theme
	Questions []QuestionData
	// TabBar is a nil interface where the original has undefined (a *TabBar is a Component; the tests stub it).
	TabBar     tui.Component
	NotesInput tui.Component
	IsMulti    bool
	// TabsByIndex is one TabComponents per question.
	TabsByIndex []TabComponents
	// SubmitPicker is optional so single-question mode and non-submit tests can omit it (nil); SubmitTabStrategy
	// falls back to Spacer rows.
	SubmitPicker tui.Component
	// GetBodyHeight is the worst-case body height across all tabs/options; it fixes the stable overall dialog footprint.
	GetBodyHeight func(width int) int
	// GetCurrentBodyHeight is the body height of the CURRENTLY active tab/option. The chrome subtracts it from
	// GetBodyHeight to absorb the residual OUTSIDE the bordered region.
	GetCurrentBodyHeight func(width int) int
	// GetTerminalRows reads the terminal height at render time.
	GetTerminalRows func() int
	// CollapseKey is the resolved collapse/expand key spec ("ctrl+]", "alt+o", or "off"). Construction-time
	// config, NOT canonical state. The footer hint interpolates it and drops the collapse part when "off".
	CollapseKey string
}

// DialogView is the 7th renderable. SetProps writes the live cell read by the strategy thunk during Render.
// ActivePreviewPane is a resolved pane reference threaded by the adapter per tick.
type DialogView struct {
	liveProps         DialogProps
	config            DialogConfig
	questionStrategy  TabContentStrategy
	submitStrategy    TabContentStrategy
	maxFooterRowCount int
}

func NewDialogView(config DialogConfig, initialProps DialogProps) *DialogView {
	d := &DialogView{config: config, liveProps: initialProps}
	d.questionStrategy = NewQuestionTabStrategy(QuestionTabStrategyConfig{
		Theme:                config.Theme,
		Questions:            config.Questions,
		GetPreviewPane:       func() StatefulView[PreviewPaneProps] { return d.liveProps.ActivePreviewPane },
		TabsByIndex:          config.TabsByIndex,
		NotesInput:           config.NotesInput,
		IsMulti:              config.IsMulti,
		GetCurrentBodyHeight: config.GetCurrentBodyHeight,
		CollapseKey:          config.CollapseKey,
	})
	d.maxFooterRowCount = d.questionStrategy.FooterRowCount()
	if config.IsMulti {
		d.submitStrategy = NewSubmitTabStrategy(SubmitTabStrategyConfig{
			Theme:        config.Theme,
			Questions:    config.Questions,
			SubmitPicker: config.SubmitPicker,
			NotesInput:   config.NotesInput,
		})
		d.maxFooterRowCount = max(d.maxFooterRowCount, d.submitStrategy.FooterRowCount())
	}
	return d
}

func (d *DialogView) SetProps(props DialogProps) { d.liveProps = props }

func (d *DialogView) HandleInput(string) {}

// Invalidate: invalidation is driven by the props adapter, which owns the full set of renderables (binding
// registries plus extras like notesInput). DialogView has no cached layout of its own.
func (d *DialogView) Invalidate() {}

func (d *DialogView) Render(width int) []string {
	state := d.liveProps.State
	onSubmit := d.config.IsMulti && state.CurrentTab == len(d.config.Questions)
	strategy := d.questionStrategy
	if onSubmit && d.submitStrategy != nil {
		strategy = d.submitStrategy
	}

	// Cache heading rows (avoid double construction in render and container build).
	headingRowCache := strategy.HeadingRows(state)
	headingCount := len(headingRowCache)

	// Build container WITHOUT residual spacer: the spacer is handled below based on overflow.
	natural := d.buildContainerFromStrategy(strategy, headingRowCache).Render(width)

	// Fixed region sizes (deterministic from structure). TabBar.Render returns [tabLine, ""]: always 2 rows.
	topFixed := 1 + 1
	if d.config.IsMulti && d.config.TabBar != nil {
		topFixed += 2
	}
	bottomFixed := 1 + strategy.FooterRowCount()
	middleRows := len(natural) - topFixed - bottomFixed

	// Residual spacer: equalizes total height across tabs (only needed when no overflow).
	spacerRows := max(0, d.config.GetBodyHeight(width)+d.maxFooterRowCount-strategy.BodyHeight(width, state)-strategy.FooterRowCount())

	termRows := d.config.GetTerminalRows()

	if len(natural)+spacerRows <= termRows {
		return renderFitsTerminal(natural, spacerRows)
	}

	// OVERFLOW: apply the 3-region partition with scroll-to-focus.
	availableMiddle := max(0, termRows-topFixed-bottomFixed)
	if availableMiddle == 0 {
		return renderChromeOnly(natural, topFixed, bottomFixed, termRows)
	}

	rs, re, hasRange := strategy.FocusedItemRowRange(width, state)
	scrollStart := computeScrollStart(hasRange, rs, re, headingCount, availableMiddle, middleRows)
	lo := min(len(natural), topFixed+scrollStart)
	hi := min(len(natural), topFixed+scrollStart+availableMiddle)
	scrollableMiddle := append([]string{}, natural[lo:hi]...)
	decorateOverflow(scrollableMiddle, scrollStart > 0, scrollStart+availableMiddle < middleRows, d.config.Theme)

	result := append(append(append([]string{}, natural[:min(topFixed, len(natural))]...), scrollableMiddle...), natural[max(0, len(natural)-bottomFixed):]...)
	// Safety: never exceed terminal rows (covers the availableMiddle == 0 case where topFixed + bottomFixed > termRows).
	if len(result) > termRows {
		return result[:max(0, termRows)]
	}
	return result
}

func (d *DialogView) buildContainerFromStrategy(strategy TabContentStrategy, headingRowCache []tui.Component) *tui.Container {
	theme, isMulti, tabBar := d.config.Theme, d.config.IsMulti, d.config.TabBar
	state := d.liveProps.State
	container := tui.NewContainer()
	border := func() tui.Component {
		return tui.NewDynamicBorder(func(s string) string { return theme.Fg("accent", s) })
	}

	container.AddChild(border())
	if isMulti && tabBar != nil {
		container.AddChild(tabBar)
	}
	container.AddChild(tui.NewSpacer(1))

	for _, c := range headingRowCache {
		container.AddChild(c)
	}
	container.AddChild(strategy.BodyComponent(state))
	container.AddChild(tui.NewSpacer(1))
	for _, c := range strategy.MidRows(state) {
		container.AddChild(c)
	}

	container.AddChild(border())
	for _, c := range strategy.FooterRows(state) {
		container.AddChild(c)
	}

	// BodyResidualSpacer moved out of Container: handled in Render.
	return container
}
