package todo

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
	sdktui "github.com/MichaelKinsy/PiG/extensions/sdk/tui"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/ansi"
)

const (
	// WidgetKey is the SDK widget identifier for the todo HUD above the editor.
	WidgetKey = "oh-my-pig:todo"

	// subsequentStageCap and activeTaskCap match omp's HUD budgets.
	// upstream: packages/coding-agent/src/modes/interactive-mode.ts:5484-5485
	subsequentStageCap = 4
	activeTaskCap      = 5
	// collapsedClosedContext is the number of closed tasks kept directly above
	// the open window so a completion remains visible as it lands.
	// upstream: packages/tui/src/tools/todo.ts:151
	collapsedClosedContext = 1
	// hudPaddingX matches `new WidthAwareText(formatRows, 1, 0)` in
	// packages/coding-agent/src/modes/interactive-mode.ts:5618.
	hudPaddingX = 1
)

var (
	ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_]`)
)

// hudPalette resolves omp theme tokens through internal/ansi so the terminal
// TODO HUD shares the exact titanium dark / host UITheme palette without
// duplicating RGB tables.
type hudPalette struct {
	theme sdk.UITheme
}

func newHudPalette(t sdk.UITheme) hudPalette {
	return hudPalette{theme: t}
}

func (p hudPalette) fg(token, text string) string { return ansi.Fg(p.theme, token, text) }
func (p hudPalette) bold(text string) string      { return ansi.Bold(p.theme, text) }
func (p hudPalette) italic(text string) string    { return ansi.Italic(p.theme, text) }
func (p hudPalette) strike(text string) string    { return ansi.Strikethrough(p.theme, text) }

// hudNoteSupDigits maps ASCII digits to Unicode superscripts for HUD note counts.
// upstream: packages/coding-agent/src/modes/interactive-mode.ts:641-661
var hudNoteSupDigits = map[rune]rune{
	'0': '\u2070', '1': '\u00b9', '2': '\u00b2', '3': '\u00b3', '4': '\u2074',
	'5': '\u2075', '6': '\u2076', '7': '\u2077', '8': '\u2078', '9': '\u2079',
}

func formatHudNoteMarker(count int, p *hudPalette) string {
	if count <= 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(" \u207a")
	for _, r := range strconv.Itoa(count) {
		if sup, ok := hudNoteSupDigits[r]; ok {
			b.WriteRune(sup)
		} else {
			b.WriteRune(r)
		}
	}
	raw := b.String()
	if p == nil {
		return raw
	}
	return p.fg("dim", p.italic(raw))
}

// sanitizeDisplayText strips ANSI escape sequences and C0/C1 control characters
// and replaces tabs with spaces so hostile task labels cannot corrupt terminal output.
// upstream: packages/tui/src/tools/todo.ts:301-303
func sanitizeDisplayText(s string) string {
	s = ansiEscapeRe.ReplaceAllString(s, "")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\t':
			b.WriteString("   ")
		case r < 0x20 || r == 0x7f:
			// Strip C0 control chars and DEL.
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// PhaseRomanNumeral returns the one-based ASCII roman numeral (I, II, III, IV, …).
// upstream: packages/tui/src/tools/todo.ts:280-291
func PhaseRomanNumeral(oneBased int) string {
	if oneBased <= 0 {
		return ""
	}
	pairs := []struct {
		val int
		sym string
	}{
		{1000, "M"}, {900, "CM"}, {500, "D"}, {400, "CD"},
		{100, "C"}, {90, "XC"}, {50, "L"}, {40, "XL"},
		{10, "X"}, {9, "IX"}, {5, "V"}, {4, "IV"}, {1, "I"},
	}
	var b strings.Builder
	rem := oneBased
	for _, p := range pairs {
		for rem >= p.val {
			b.WriteString(p.sym)
			rem -= p.val
		}
	}
	return b.String()
}

// FormatPhaseDisplayName formats a multi-phase display header: `I. Foundation`.
// upstream: packages/tui/src/tools/todo.ts:312-314
func FormatPhaseDisplayName(name string, oneBased int) string {
	return PhaseRomanNumeral(oneBased) + ". " + sanitizeDisplayText(name)
}

// CollapsedTodoSelection is the result of SelectCollapsedTodos.
// upstream: packages/tui/src/tools/todo.ts:138-141
type CollapsedTodoSelection struct {
	Items   []TodoItem
	Summary string
}

func pluralize(noun string, count int) string {
	if count == 1 {
		return noun
	}
	return noun + "s"
}

func formatMoreItems(count int, noun string) string {
	return fmt.Sprintf("… %d more %s", count, pluralize(noun, count))
}

func normalizeForTodoMatch(value string) string {
	var b strings.Builder
	inSep := false
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			if inSep && b.Len() > 0 {
				b.WriteByte(' ')
			}
			inSep = false
			b.WriteRune(r)
		} else {
			inSep = true
		}
	}
	return b.String()
}

// TodoMatchesAnyDescription reports whether content overlaps any active subagent description.
// upstream: packages/tui/src/tools/todo.ts:62-122
func TodoMatchesAnyDescription(content string, descriptions []string) bool {
	const minOverlap = 6
	target := normalizeForTodoMatch(content)
	if target == "" {
		return false
	}
	targetLen := len([]rune(target))
	for _, desc := range descriptions {
		cand := normalizeForTodoMatch(desc)
		if cand == "" {
			continue
		}
		if target == cand {
			return true
		}
		candLen := len([]rune(cand))
		if targetLen >= minOverlap && strings.Contains(cand, target) {
			return true
		}
		if candLen >= minOverlap && strings.Contains(target, cand) {
			return true
		}
	}
	return false
}

func isActiveTodo(task TodoItem, isMatched func(TodoItem) bool) bool {
	if task.Status == StatusInProgress {
		return true
	}
	return task.Status == StatusPending && isMatched != nil && isMatched(task)
}

func selectWithinCap(base []TodoItem, isMatched func(TodoItem) bool, capN int) CollapsedTodoSelection {
	if len(base) <= capN+1 {
		return CollapsedTodoSelection{Items: append([]TodoItem(nil), base...)}
	}
	var active []TodoItem
	firstActiveIdx := -1
	for i, task := range base {
		if isActiveTodo(task, isMatched) {
			if firstActiveIdx == -1 {
				firstActiveIdx = i
			}
			active = append(active, task)
		}
	}
	if len(active) > capN {
		hiddenActive := len(active) - capN
		return CollapsedTodoSelection{
			Items:   append([]TodoItem(nil), active[:capN]...),
			Summary: fmt.Sprintf("… %d more active %s", hiddenActive, pluralize("todo", hiddenActive)),
		}
	}
	startIdx := 0
	if firstActiveIdx >= 0 {
		startIdx = firstActiveIdx
	}
	var fill []TodoItem
	for i := startIdx; i < len(base) && len(active)+len(fill) < capN; i++ {
		if isActiveTodo(base[i], isMatched) {
			continue
		}
		fill = append(fill, base[i])
	}
	items := make([]TodoItem, 0, len(active)+len(fill))
	items = append(items, active...)
	items = append(items, fill...)
	hidden := len(base) - len(items)
	summary := ""
	if hidden > 0 {
		summary = formatMoreItems(hidden, "todo")
	}
	return CollapsedTodoSelection{Items: items, Summary: summary}
}

// SelectCollapsedTodos applies omp's walking-viewport selection for a phase's collapsed preview.
// upstream: packages/tui/src/tools/todo.ts:264-276
func SelectCollapsedTodos(tasks []TodoItem, capN int) CollapsedTodoSelection {
	return SelectCollapsedTodosMatched(tasks, nil, capN)
}

// SelectCollapsedTodosMatched applies omp's walking-viewport selection with an optional
// subagent-match predicate.
// upstream: packages/tui/src/tools/todo.ts:264-276
func SelectCollapsedTodosMatched(tasks []TodoItem, isMatched func(TodoItem) bool, capN int) CollapsedTodoSelection {
	var open []TodoItem
	var closed []TodoItem
	for _, t := range tasks {
		if IsClosedTodo(t) {
			closed = append(closed, t)
		} else {
			open = append(open, t)
		}
	}
	if len(open) == 0 {
		return selectWithinCap(tasks, isMatched, capN)
	}
	var lead []TodoItem
	if len(closed) > 0 {
		start := len(closed) - collapsedClosedContext
		if start < 0 {
			start = 0
		}
		lead = closed[start:]
	}
	sel := selectWithinCap(open, isMatched, capN)
	items := make([]TodoItem, 0, len(lead)+len(sel.Items))
	items = append(items, lead...)
	items = append(items, sel.Items...)
	return CollapsedTodoSelection{Items: items, Summary: sel.Summary}
}

// formatTodoMarkLine formats one HUD task line using omp's checkbox marks and,
// when p is non-nil, omp's semantic theme styling.
// upstream: packages/coding-agent/src/modes/interactive-mode.ts:5315-5329
func formatTodoMarkLine(todo TodoItem, p *hudPalette) string {
	content := sanitizeDisplayText(todo.Content)
	marker := formatHudNoteMarker(len(todo.Notes), p)
	if p == nil {
		switch todo.Status {
		case StatusCompleted:
			return "☑ " + content + marker
		default:
			return "☐ " + content + marker
		}
	}
	switch todo.Status {
	case StatusCompleted:
		return p.fg("success", "☑ "+p.strike(content)) + marker
	case StatusInProgress:
		return p.fg("accent", "☐ "+content) + marker
	case StatusAbandoned:
		return p.fg("error", "☐ "+p.strike(content)) + marker
	default:
		return p.fg("dim", "☐ "+content) + marker
	}
}

// activePhaseIndex mirrors InteractiveMode.#getActivePhase
// (packages/coding-agent/src/modes/interactive-mode.ts:5432-5438):
// returns the first non-empty phase with an open (pending or in_progress) task,
// falling back to the last non-empty phase when every phase is settled.
func activePhaseIndex(phases []TodoPhase) int {
	for i, p := range phases {
		for _, t := range p.Tasks {
			if t.Status == StatusPending || t.Status == StatusInProgress {
				return i
			}
		}
	}
	if len(phases) == 0 {
		return 0
	}
	return len(phases) - 1
}

type hudEntry struct {
	spineGlyph string // "├─ " for stage/overflow rows, "│  " for task rows
	innerFirst string // "" for stage rows, "├─ " or "└─ " for task/summary rows
	innerCont  string // "" for stage rows, "│  " or "   " for task/summary continuations
	content    string // styled (or plain) logical row content
}

func (e hudEntry) prefixes(spineColor string, p *hudPalette) (firstPrefix, contPrefix string) {
	spineFirst := " " + e.spineGlyph
	spineCont := " │  "
	innerF := e.innerFirst
	innerC := e.innerCont
	if p != nil {
		spineFirst = " " + p.fg(spineColor, e.spineGlyph)
		spineCont = " " + p.fg(spineColor, "│  ")
		if innerF != "" {
			// renderTreeList styles only the connector glyph (`├─` / `└─`) in dim,
			// leaving the trailing space uncolored (tree-list.ts:54-55).
			innerF = p.fg("dim", strings.TrimRight(innerF, " ")) + " "
		}
		if strings.HasPrefix(innerC, "│") {
			innerC = p.fg("dim", "│") + innerC[len("│"):]
		}
	}
	return spineFirst + innerF, spineCont + innerC
}

func buildWidgetEntries(phases []TodoPhase, expanded bool) []hudEntry {
	return buildWidgetEntriesStyled(phases, expanded, nil)
}

func buildWidgetEntriesStyled(phases []TodoPhase, expanded bool, p *hudPalette) []hudEntry {
	var nonEmpty []TodoPhase
	for _, pPhase := range phases {
		if len(pPhase.Tasks) > 0 {
			nonEmpty = append(nonEmpty, pPhase)
		}
	}
	if len(nonEmpty) == 0 {
		return nil
	}

	multiPhase := len(nonEmpty) > 1
	activeIdx := activePhaseIndex(nonEmpty)

	baseIdx := activeIdx
	endIdx := activeIdx + 1 + subsequentStageCap
	if expanded {
		baseIdx = 0
		endIdx = len(nonEmpty)
	}
	if endIdx > len(nonEmpty) {
		endIdx = len(nonEmpty)
	}
	phaseSlice := nonEmpty[baseIdx:endIdx]
	hiddenStages := len(nonEmpty) - baseIdx - len(phaseSlice)

	var entries []hudEntry
	for i, phase := range phaseSlice {
		oneBased := baseIdx + i + 1
		isActive := baseIdx+i == activeIdx
		label := sanitizeDisplayText(phase.Name)
		if multiPhase {
			label = FormatPhaseDisplayName(phase.Name, oneBased)
		}
		done := 0
		for _, t := range phase.Tasks {
			if IsClosedTodo(t) {
				done++
			}
		}
		progress := fmt.Sprintf(" · %d/%d", done, len(phase.Tasks))
		var header string
		if p == nil {
			header = label + progress
		} else if isActive {
			// upstream: packages/coding-agent/src/modes/interactive-mode.ts:5531
			header = p.bold(p.fg("accent", label)) + p.fg("dim", progress)
		} else {
			// upstream: packages/coding-agent/src/modes/interactive-mode.ts:5528
			header = p.fg("muted", label) + p.fg("dim", progress)
		}
		entries = append(entries, hudEntry{
			spineGlyph: "├─ ",
			content:    header,
		})
		if !isActive && !expanded {
			continue
		}
		var items []TodoItem
		summary := ""
		if expanded {
			items = phase.Tasks
		} else {
			sel := SelectCollapsedTodos(phase.Tasks, activeTaskCap)
			items = sel.Items
			summary = sel.Summary
		}
		total := len(items)
		if summary != "" {
			total++
		}
		for j, item := range items {
			innerF := "├─ "
			innerC := "│  "
			if j == total-1 {
				innerF = "└─ "
				innerC = "   "
			}
			entries = append(entries, hudEntry{
				spineGlyph: "│  ",
				innerFirst: innerF,
				innerCont:  innerC,
				content:    formatTodoMarkLine(item, p),
			})
		}
		if summary != "" {
			summaryText := summary
			if p != nil {
				// upstream: packages/tui/src/render/tree-list.ts:89
				summaryText = p.fg("muted", summary)
			}
			entries = append(entries, hudEntry{
				spineGlyph: "│  ",
				innerFirst: "└─ ",
				innerCont:  "   ",
				content:    summaryText,
			})
		}
	}

	if hiddenStages > 0 {
		moreText := formatMoreItems(hiddenStages, "stage")
		if p != nil {
			// upstream: packages/coding-agent/src/modes/interactive-mode.ts:5562
			moreText = p.fg("muted", moreText)
		}
		entries = append(entries, hudEntry{
			spineGlyph: "├─ ",
			content:    moreText,
		})
	}
	return entries
}

// hudProgressFill computes how many path cells (content rows + 6-cell tail) are
// lit in `accent` vs `dim` across all phases.
// upstream: packages/coding-agent/src/modes/interactive-mode.ts:5567-5580
func hudProgressFill(phases []TodoPhase, contentLen int) int {
	const tailLen = 6
	totalTasks := 0
	closedTasks := 0
	for _, p := range phases {
		for _, t := range p.Tasks {
			totalTasks++
			if IsClosedTodo(t) {
				closedTasks++
			}
		}
	}
	if totalTasks == 0 {
		return 0
	}
	pathLen := contentLen + tailLen
	filled := int(math.Round(float64(closedTasks) / float64(totalTasks) * float64(pathLen)))
	if closedTasks > 0 && filled < 1 {
		filled = 1
	}
	if closedTasks < totalTasks && filled > pathLen-1 {
		filled = pathLen - 1
	}
	return filled
}

// formatHudTail formats the closing ` └─────` elbow tail, splitting lit
// (`accent`) and unlit (`dim`) cells along the overall progress path.
// upstream: packages/coding-agent/src/modes/interactive-mode.ts:5567-5583
func formatHudTail(filled, contentLen int, p *hudPalette) string {
	tailRunes := []rune("└─────")
	if p == nil {
		return " " + string(tailRunes)
	}
	tailFilled := filled - contentLen
	if tailFilled < 0 {
		tailFilled = 0
	}
	if tailFilled > len(tailRunes) {
		tailFilled = len(tailRunes)
	}
	return " " + p.fg("accent", string(tailRunes[:tailFilled])) + p.fg("dim", string(tailRunes[tailFilled:]))
}

// clampWidgetRow ensures a single rendered row never exceeds width columns and
// never contains embedded CR/LF bytes (which PiG's TUI treats as fatal).
func clampWidgetRow(row string, width int) string {
	row = strings.ReplaceAll(row, "\r", "")
	row = strings.ReplaceAll(row, "\n", "")
	if width <= 0 {
		return ""
	}
	if sdktui.VisibleWidth(row) <= width {
		return row
	}
	row = sdktui.TruncateToWidth(row, width, "", false)
	if sdktui.VisibleWidth(row) > width {
		return ""
	}
	return row
}

// wrapWidgetEntry wraps one logical HUD row inside contentWidth (already
// reduced by left+right hudPaddingX) with hanging indentation matching omp's
// `formatRows` + `wrapTreeLine` (interactive-mode.ts:5602-5616, hanging-wrap.ts:5-31),
// then prepends the left margin and clamps to the full panel width.
func wrapWidgetEntry(firstPrefix, contPrefix, content string, width, contentWidth int, styled bool) []string {
	const leftPad = " "
	const segmentReset = "\x1b[0m"
	prefixW := max(sdktui.VisibleWidth(firstPrefix), sdktui.VisibleWidth(contPrefix))
	if contentWidth <= prefixW {
		wrapped := sdktui.WrapTextWithAnsi(firstPrefix+content, contentWidth)
		out := make([]string, 0, len(wrapped))
		for _, r := range wrapped {
			out = append(out, clampWidgetRow(leftPad+r, width))
		}
		return out
	}
	wrapped := sdktui.WrapTextWithAnsi(content, contentWidth-prefixW)
	if len(wrapped) == 0 {
		return []string{clampWidgetRow(leftPad+firstPrefix, width)}
	}
	out := make([]string, 0, len(wrapped))
	last := len(wrapped) - 1
	for i, r := range wrapped {
		p := firstPrefix
		if i > 0 {
			p = contPrefix
		}
		suffix := ""
		if styled && i < last {
			suffix = segmentReset
		}
		out = append(out, clampWidgetRow(leftPad+p+r+suffix, width))
	}
	return out
}

// RenderWidgetLines builds the unwrapped plain-text logical HUD widget lines
// with omp's 1-column left padding (`WidthAwareText(formatRows, 1, 0)`).
// Returns nil when there are no non-empty phases.
// upstream: packages/coding-agent/src/modes/interactive-mode.ts:5471-5618
func RenderWidgetLines(phases []TodoPhase, expanded bool) []string {
	entries := buildWidgetEntries(phases, expanded)
	if len(entries) == 0 {
		return nil
	}
	const leftPad = " "
	lines := make([]string, 0, len(entries)+2)
	lines = append(lines, leftPad+"TODO")
	for _, e := range entries {
		firstP, _ := e.prefixes("dim", nil)
		lines = append(lines, leftPad+firstP+e.content)
	}
	lines = append(lines, leftPad+" └─────")
	return lines
}

// RenderWidgetLinesForWidth renders the plain-text HUD widget wrapped to width
// columns using omp's `WidthAwareText(formatRows, 1, 0)` margins.
// Every returned row has VisibleWidth <= width and contains no CR/LF.
// upstream: packages/coding-agent/src/modes/interactive-mode.ts:5585-5618,
// packages/tui/src/render/width-aware-text.ts:87-96
func RenderWidgetLinesForWidth(phases []TodoPhase, expanded bool, width int) []string {
	return renderWidgetLinesForWidth(phases, expanded, width, nil)
}

// RenderStyledWidgetLinesForWidth renders the ANSI-styled HUD widget wrapped to
// width columns using theme tokens matching omp's `InteractiveMode.#renderTodoList`
// (`interactive-mode.ts:5315-5618`).
func RenderStyledWidgetLinesForWidth(theme sdk.UITheme, phases []TodoPhase, expanded bool, width int) []string {
	p := newHudPalette(theme)
	return renderWidgetLinesForWidth(phases, expanded, width, &p)
}

func renderWidgetLinesForWidth(phases []TodoPhase, expanded bool, width int, p *hudPalette) []string {
	if width <= 0 {
		return nil
	}
	entries := buildWidgetEntriesStyled(phases, expanded, p)
	if len(entries) == 0 {
		return nil
	}
	const leftPad = " "
	contentWidth := max(1, width-hudPaddingX*2)
	filled := hudProgressFill(phases, len(entries))
	title := "TODO"
	if p != nil {
		// upstream: packages/coding-agent/src/modes/interactive-mode.ts:5581
		title = p.bold(p.fg("accent", "TODO"))
	}
	lines := make([]string, 0, len(entries)+2)
	lines = append(lines, clampWidgetRow(leftPad+title, width))
	for i, e := range entries {
		spineColor := "dim"
		if i < filled {
			spineColor = "accent"
		}
		firstP, contP := e.prefixes(spineColor, p)
		lines = append(lines, wrapWidgetEntry(firstP, contP, e.content, width, contentWidth, p != nil)...)
	}
	tail := formatHudTail(filled, len(entries), p)
	lines = append(lines, clampWidgetRow(leftPad+tail, width))
	return lines
}

// hudNativeCheckbox returns the checkbox glyph for a native HUD checklist item.
func hudNativeCheckbox(status TodoStatus) string {
	switch status {
	case StatusCompleted:
		return "☑"
	case StatusInProgress:
		return "◐"
	case StatusAbandoned:
		return "☒"
	default:
		return "☐"
	}
}

// hudNativeChecklistStatus maps omp's todo status to Tern's checklist item status.
// upstream: packages/coding-agent/src/modes/interactive-mode.ts:5685-5692
func hudNativeChecklistStatus(status TodoStatus) string {
	switch status {
	case StatusCompleted:
		return "done"
	case StatusInProgress:
		return "active"
	case StatusAbandoned:
		return "dropped"
	default:
		return "pending"
	}
}

// BuildNativeWidgetView constructs the structured kit.View for the TODO HUD
// dock widget on Tern's native surface (mapped by pig-tern/frontend into
// `checklist[omp.hud.todo]{mode:"hud", phases:[...]}`).
// Returns (zero, false) when there are no non-empty phases so the caller clears
// the widget; stays mounted even when all tasks are completed/abandoned, matching
// omp's `interactive-mode.ts:5676-5703`.
func BuildNativeWidgetView(phases []TodoPhase) (kit.View, bool) {
	var nonEmpty []TodoPhase
	for _, p := range phases {
		if len(p.Tasks) > 0 {
			nonEmpty = append(nonEmpty, p)
		}
	}
	if len(nonEmpty) == 0 {
		return kit.View{}, false
	}

	multiPhase := len(nonEmpty) > 1
	phaseNodes := make([]kit.StackChild, 0, len(nonEmpty))
	for pIdx, phase := range nonEmpty {
		title := sanitizeDisplayText(phase.Name)
		if multiPhase {
			title = FormatPhaseDisplayName(phase.Name, pIdx+1)
		}
		pChildren := make([]kit.StackChild, 0, 1+len(phase.Tasks)*3)
		pChildren = append(pChildren, &kit.Text{
			ID:   fmt.Sprintf("omp.todo.phase.head#%d", pIdx),
			Text: title,
		})
		for tIdx, task := range phase.Tasks {
			content := sanitizeDisplayText(task.Content)
			pChildren = append(pChildren,
				&kit.Text{
					ID:   fmt.Sprintf("omp.todo.item#%d.%d", pIdx, tIdx),
					Text: fmt.Sprintf("%s %s", hudNativeCheckbox(task.Status), content),
				},
				&kit.Text{
					ID:   fmt.Sprintf("omp.todo.item.status#%d.%d", pIdx, tIdx),
					Text: hudNativeChecklistStatus(task.Status),
				},
			)
			if len(task.Notes) > 0 {
				lastNote := sanitizeDisplayText(task.Notes[len(task.Notes)-1])
				pChildren = append(pChildren, &kit.Text{
					ID:   fmt.Sprintf("omp.todo.item.note#%d.%d", pIdx, tIdx),
					Text: lastNote,
				})
			}
		}
		phaseNodes = append(phaseNodes, &kit.VStack{
			ID:       fmt.Sprintf("omp.todo.phase#%d", pIdx),
			Gap:      0,
			Children: pChildren,
		})
	}

	root := &kit.VStack{
		ID:       "omp.hud.todo",
		Gap:      0,
		Children: phaseNodes,
	}
	return kit.View{Root: root}, true
}
