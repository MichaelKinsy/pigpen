package tintinweb_subagents

import (
	"strconv"
	"strings"
	"sync"
	"time"

	tui "github.com/MichaelKinsy/pigpen/components/pitui"
	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

// The `/agents → Workflows` two-pane inspector: phases on the left and the selected phase's agents on the right;
// opening an agent moves that phase's agents to the left and makes the right the agent's Prompt / Activity / Outcome
// detail. The layout is pure (coloured segments, no theme, no terminal, no runtime) and `handleWorkflowDialogKey` maps
// a keypress to the next state plus an optional action; the component is the thin shell that wires them to the UI.
// The glyphs are the dialog's own: it keys off the derived display state, which splits skipped, blocked, queued and
// interrupted, where the card draws a plain cross or a static running glyph. upstream: src/ui/workflow-dialog.ts.

const (
	// workflowLeftPaneWidth is the inner width of the left pane at any comfortable terminal size.
	workflowLeftPaneWidth = 18
	// defaultPaneBodyRows is the most rows the frame will ever draw between its top and bottom edges: a cap, so a
	// 200-agent fan-out scrolls inside the pane.
	defaultPaneBodyRows = 22
	// minPaneBodyRows is the fewest rows the frame will draw: below it a pane stops reading as a pane, and the box
	// would resize on nearly every keypress.
	minPaneBodyRows = 6
	// promptCollapsedLines is the prompt lines shown before `expand` is offered.
	promptCollapsedLines = 4
	// workflowDialogSpinnerMs is the spinner cadence: unlike the card's 1s header tick, this row really animates.
	workflowDialogSpinnerMs = 80
)

type workflowDialogGlyphs struct {
	tick, cross, queued, pointer, focus string
	spinner                             []string
	box                                 workflowDialogBox
	ellipsis, upDown, enter             string
}

type workflowDialogBox struct {
	topLeft, topRight, bottomLeft, bottomRight, horizontal, vertical, topTee, bottomTee string
}

var unicodeDialogGlyphs = workflowDialogGlyphs{
	tick: unicodeWorkflowGlyphs.tick, cross: unicodeWorkflowGlyphs.cross, queued: "◌", pointer: "❯", focus: unicodeWorkflowGlyphs.pointer,
	spinner:  spinner,
	box:      workflowDialogBox{"╭", "╮", "╰", "╯", "─", "│", "┬", "┴"},
	ellipsis: "…", upDown: "↑↓", enter: "⏎",
}

// asciiDialogGlyphs is the ASCII tier, one column per glyph so the panes stay aligned either way.
var asciiDialogGlyphs = workflowDialogGlyphs{
	tick: asciiWorkflowGlyphs.tick, cross: asciiWorkflowGlyphs.cross, queued: "o", pointer: ">", focus: asciiWorkflowGlyphs.pointer,
	spinner:  []string{"-", "\\", "|", "/"},
	box:      workflowDialogBox{"+", "+", "+", "+", "-", "|", "+", "+"},
	ellipsis: "~", upDown: "up/down", enter: "enter",
}

// dialogRowGlyph is the recovered dialog mapping, keyed on the display state. Claude Code's `permission` colour has no
// pi equivalent; a blocked agent is waiting on the user, so it maps to `warning` (selection maps to `accent`).
func dialogRowGlyph(state string, g workflowDialogGlyphs, spinnerFrame int) workflowCardSegment {
	switch state {
	case displayDone:
		return workflowCardSegment{Text: g.tick, Color: "success"}
	case displayFailed:
		return workflowCardSegment{Text: g.cross, Color: "error"}
	case displaySkipped:
		return workflowCardSegment{Text: g.cross, Color: "dim"}
	case displayBlocked:
		return workflowCardSegment{Text: g.cross, Color: "warning"}
	case displayQueued, displayInterrupted:
		return workflowCardSegment{Text: g.queued, Color: "dim"}
	}
	return workflowCardSegment{Text: g.spinner[spinnerFrame%len(g.spinner)], Color: "dim"}
}

// Claude Code's own strings, kept together so none drifts under an edit.
const (
	copyWaitingForSlot     = "Waiting for an agent slot."
	copyAvailableOnceStart = "Available once the agent starts."
	copyNotAvailableYet    = "Not available yet (agent still running)."
	copyNoTranscript       = "Transcript not available."
	copyStoppedEarly       = "The workflow stopped before this agent finished."
	copySkippedByUser      = "Skipped by user."
	copyNoToolCallsYet     = "No tool calls yet."
	copyNoToolCalls        = "No tool calls."
	copyNoAgents           = "No agents"
)

// The two drill-down levels. phases is the overview; agent is the subview reached by opening one agent.
const (
	dialogLevelPhases = "phases"
	dialogLevelAgent  = "agent"
)

// workflowDialogFilters is the order `f` cycles through.
var workflowDialogFilters = []string{"all", displayRunning, displayQueued, displayDone, displayFailed, displayBlocked, displaySkipped, displayInterrupted}

type workflowDialogState struct {
	// SelectedPhase and SelectedAgent are raw selections; the resolved view clamps them.
	SelectedPhase, SelectedAgent int
	Level                        string
	Filter                       string
	PromptExpanded               bool
}

func initialWorkflowDialogState(initialPhaseIndex int) workflowDialogState {
	return workflowDialogState{SelectedPhase: initialPhaseIndex, Level: dialogLevelPhases, Filter: "all"}
}

// workflowDialogSource is everything the dialog reads about a run, so it can be driven from a stub.
type workflowDialogSource struct {
	Progress []workflowEntry
	Task     workflowCardTask
	Meta     *workflowMeta
	// AgentCount is the agents the runtime has scheduled, which can exceed those that reported.
	AgentCount int
	// Version identifies the progress log's revision (0: unknown). With Groups it lets a caller that renders on a
	// timer reuse the phase groups instead of folding the whole log per frame.
	Version int
}

type workflowDialogInput struct {
	workflowDialogSource
	State workflowDialogState
	// Available says which actions the caller wired; an absent key defaults to available, so layout tests and
	// read-only callers keep the full footer.
	Available map[string]bool
	Now       int64
	// Width is the terminal width; the content width is derived from it. 0: the default.
	Width        int
	ASCII        bool
	SpinnerFrame int
	// BodyRows overrides defaultPaneBodyRows as the ceiling (0: the default).
	BodyRows int
	// Groups are the phase groups of Progress and Meta when the caller has them already.
	Groups []phaseGroup
}

type workflowDialogAction struct {
	Kind     string // cancel, kill, pause, resume, skip, retry, open
	Index    int
	RecordID string
}

type resolvedWorkflowDialog struct {
	Groups        []phaseGroup
	ClampedPhase  int
	ClampedAgent  int
	VisibleAgents []*workflowAgentEntry
	// SelectedEntry is nil when there is no agent selected.
	SelectedEntry  *workflowAgentEntry
	WorkflowActive bool
	Paused         bool
}

// workflowDialogContentWidth is the content width; the 6 columns are the dialog's border and padding.
func workflowDialogContentWidth(terminalWidth int) int { return max(12, terminalWidth-6) }

func clampWorkflowIndex(index, length int) int {
	if length == 0 {
		return 0
	}
	return min(max(0, index), length-1)
}

// resolveWorkflowDialog settles the selection against the data actually present. Selection is stored raw and clamped on
// read, so a phase finishing never leaves the cursor pointing past the end.
func resolveWorkflowDialog(in workflowDialogInput) resolvedWorkflowDialog {
	groups := in.Groups
	if groups == nil {
		var declared []workflowPhaseMeta
		if in.Meta != nil {
			declared = in.Meta.Phases
		}
		groups = buildPhaseGroups(in.Progress, declared)
	}
	active := in.Task.Status == runRunning || in.Task.Status == runPaused
	phase := clampWorkflowIndex(in.State.SelectedPhase, len(groups))
	var all []*workflowAgentEntry
	if phase < len(groups) {
		all = groups[phase].Agents
	}
	visible := make([]*workflowAgentEntry, 0, len(all))
	for _, e := range all {
		if in.State.Filter == "all" || workflowDisplayState(e, active) == in.State.Filter {
			visible = append(visible, e)
		}
	}
	agent := clampWorkflowIndex(in.State.SelectedAgent, len(visible))
	out := resolvedWorkflowDialog{Groups: groups, ClampedPhase: phase, ClampedAgent: agent, VisibleAgents: visible, WorkflowActive: active,
		Paused: in.Task.Status == runPaused}
	if agent < len(visible) {
		out.SelectedEntry = visible[agent]
	}
	return out
}

// workflowSubStatusAnnotations are the `·`-separated annotations between an agent's label and its stats: why a row looks
// the way it does (a retry and its cause, a cache hit replayed from the resume journal, how long a queued agent has waited).
func workflowSubStatusAnnotations(entry *workflowAgentEntry, state string, now int64) []string {
	var parts []string
	if entry.Isolation != "" {
		parts = append(parts, entry.Isolation)
	}
	if entry.Cached {
		parts = append(parts, workflowReplayedAnnotation)
	}
	if entry.LastAttemptReason != "" {
		if entry.LastAttemptReason == "user-retry" {
			parts = append(parts, "user retry")
		} else {
			parts = append(parts, entry.LastAttemptReason)
		}
	}
	if entry.Attempt > 1 {
		parts = append(parts, "attempt "+strconv.Itoa(entry.Attempt))
	}
	if state == displayQueued && entry.QueuedAt != nil {
		parts = append(parts, "waiting "+workflowFormatDuration(float64(max(0, now-*entry.QueuedAt))))
	}
	return parts
}

// rightAlignWorkflowLine places right flush to width, cutting left first so the stats survive.
func rightAlignWorkflowLine(left, right workflowCardLine, width int) workflowCardLine {
	rightWidth := workflowLineWidth(right)
	clampedLeft := clampWorkflowLine(left, max(0, width-rightWidth-1))
	gap := max(1, width-workflowLineWidth(clampedLeft)-rightWidth)
	out := append(workflowCardLine{}, clampedLeft...)
	out = append(out, workflowCardSegment{Text: strings.Repeat(" ", gap)})
	out = append(out, right...)
	return clampWorkflowLine(out, width)
}

// windowRange windows a list around its selection, so a 200-agent fan-out still shows the row you are on.
func windowRange(selected, total, maxRows int) (start, end int) {
	visible := min(maxRows, total)
	if selected >= visible {
		start = selected - visible + 1
	}
	return start, start + visible
}

// leftPaneWidth is the inner width of the left pane: fixed at usable sizes, giving way on a narrow terminal.
func leftPaneWidth(width int) int {
	// The two cells share everything except the three border columns, and the right one must keep at least a column.
	available := max(2, width-3)
	return max(1, min(workflowLeftPaneWidth, available/3, available-1))
}

// padWorkflowTitle fills a title out to the cell width with rule, so the corners stay put.
func padWorkflowTitle(line workflowCardLine, width int, horizontal string) workflowCardLine {
	if gap := max(0, width-workflowLineWidth(line)); gap > 0 {
		return append(append(workflowCardLine{}, line...), workflowCardSegment{Text: strings.Repeat(horizontal, gap), Color: "dim"})
	}
	return line
}

// padWorkflowCell is one pane row, padded to exactly width so the frame stays vertical.
func padWorkflowCell(line workflowCardLine, width int) workflowCardLine {
	clamped := clampWorkflowLine(line, width)
	if gap := max(0, width-workflowLineWidth(clamped)); gap > 0 {
		return append(append(workflowCardLine{}, clamped...), workflowCardSegment{Text: strings.Repeat(" ", gap)})
	}
	return clamped
}

// frameWorkflowTitle is a title as it sits in the frame's top edge: ` Title ` then rule to width.
func frameWorkflowTitle(title string, width int, g workflowDialogGlyphs) workflowCardLine {
	room := max(0, width-2)
	// Truncated with the marker rather than simply cut, so `Discover · 1 ag…` reads as "there was more".
	shown := widthx.StripTerminalSequences(widthx.TruncateToWidth(title, room, g.ellipsis, false))
	rule := max(0, width-widthx.VisibleWidth(shown)-2)
	// Clamped as well as computed: a title that overflows tears the frame open on every row below it.
	return clampWorkflowLine(workflowCardLine{
		{Text: " ", Color: "dim"},
		{Text: shown, Color: "muted", Bold: true},
		{Text: " " + strings.Repeat(g.box.horizontal, rule), Color: "dim"},
	}, width)
}

// paneFrame draws the two panes into one framed block. Both columns are padded to bodyRows so the frame is the same
// height however much either side holds.
func paneFrame(leftTitle, rightTitle string, leftRows, rightRows []workflowCardLine, width, bodyRows int, g workflowDialogGlyphs) []workflowCardLine {
	box := g.box
	left := leftPaneWidth(width)
	right := max(1, width-left-3)
	var lines []workflowCardLine
	top := workflowCardLine{{Text: box.topLeft, Color: "dim"}}
	top = append(top, padWorkflowTitle(frameWorkflowTitle(leftTitle, left, g), left, box.horizontal)...)
	top = append(top, workflowCardSegment{Text: box.topTee, Color: "dim"})
	top = append(top, padWorkflowTitle(frameWorkflowTitle(rightTitle, right, g), right, box.horizontal)...)
	top = append(top, workflowCardSegment{Text: box.topRight, Color: "dim"})
	lines = append(lines, top)
	row := func(rows []workflowCardLine, i int) workflowCardLine {
		if i < len(rows) {
			return rows[i]
		}
		return nil
	}
	for i := range bodyRows {
		line := workflowCardLine{{Text: box.vertical, Color: "dim"}}
		line = append(line, padWorkflowCell(row(leftRows, i), left)...)
		line = append(line, workflowCardSegment{Text: box.vertical, Color: "dim"})
		line = append(line, padWorkflowCell(row(rightRows, i), right)...)
		line = append(line, workflowCardSegment{Text: box.vertical, Color: "dim"})
		lines = append(lines, line)
	}
	lines = append(lines, workflowCardLine{
		{Text: box.bottomLeft, Color: "dim"}, {Text: strings.Repeat(box.horizontal, left), Color: "dim"}, {Text: box.bottomTee, Color: "dim"},
		{Text: strings.Repeat(box.horizontal, right), Color: "dim"}, {Text: box.bottomRight, Color: "dim"}})
	return lines
}

func previewWorkflowLines(preview string) []string {
	if preview == "" {
		return nil
	}
	return strings.Split(preview, "\n")
}

// workflowActivityBody is what the Activity body says: a count of tool calls, never a list.
func workflowActivityBody(entry *workflowAgentEntry, state string) string {
	if state == displayQueued {
		return copyAvailableOnceStart
	}
	if intOr0(entry.ToolCalls) > 0 {
		return copyNoTranscript
	}
	if workflowEntryLive(entry) {
		return copyNoToolCallsYet
	}
	return copyNoToolCalls
}

// workflowOutcomeBody is what the Outcome body says, a different sentence for every state.
func workflowOutcomeBody(entry *workflowAgentEntry, state string) string {
	switch state {
	case displaySkipped:
		return copySkippedByUser
	case displayInterrupted:
		return copyStoppedEarly
	case displayQueued:
		return copyWaitingForSlot
	case displayRunning:
		return copyNotAvailableYet
	case displayFailed, displayBlocked:
		if entry.Error != "" {
			return entry.Error
		}
		return copyNoTranscript
	}
	if entry.ResultPreview != "" {
		return entry.ResultPreview
	}
	return copyNoTranscript
}

// workflowAgentActions are the per-agent actions the selected row can currently take. The window for both is the one
// in which the agent's agent() call is still unanswered; retry needs a child to stop and start again.
func workflowAgentActions(entry *workflowAgentEntry, workflowActive bool) (skip, retry bool) {
	if entry == nil || !workflowActive {
		return false, false
	}
	state := workflowDisplayState(entry, workflowActive)
	return state == displayQueued || state == displayRunning, state == displayRunning
}

func workflowStatusWord(state string) string {
	switch state {
	case displayDone:
		return "Completed"
	case displayFailed:
		return "Failed"
	case displaySkipped:
		return "Skipped"
	case displayBlocked:
		return "Blocked"
	case displayQueued:
		return "Queued"
	case displayInterrupted:
		return "Stopped"
	}
	return "Running"
}

// workflowDetailHeading is `Prompt · 5 lines · ⏎ expand`: a detail heading and its dim suffixes.
func workflowDetailHeading(title string, suffixes []string, width int) workflowCardLine {
	line := workflowCardLine{{Text: " "}, {Text: title, Color: "muted", Bold: true}}
	for _, s := range suffixes {
		line = append(line, workflowCardSegment{Text: " · ", Color: "dim"}, workflowCardSegment{Text: s, Color: "dim"})
	}
	return clampWorkflowLine(line, width)
}

func workflowDetailBody(text string, width int) workflowCardLine {
	return clampWorkflowLine(workflowCardLine{{Text: "   " + text, Color: "dim"}}, width)
}

func workflowRowStatSegments(entry *workflowAgentEntry) []string {
	if t := intOr0(entry.Tokens); t != 0 {
		return []string{formatCompactTokens(t) + " tok"}
	}
	return nil
}

// workflowAgentRow is the row for one agent, as it appears in whichever pane is listing agents.
func workflowAgentRow(entry *workflowAgentEntry, selected, compact bool, width int, g workflowDialogGlyphs, active bool, spinnerFrame int, now int64) workflowCardLine {
	display := workflowDisplayState(entry, active)
	pointer := " "
	if selected {
		pointer = g.pointer
	}
	labelColor := ""
	if selected {
		labelColor = "accent"
	}
	head := workflowCardLine{{Text: " "}, {Text: pointer, Color: "accent"}, {Text: " "}, dialogRowGlyph(display, g, spinnerFrame), {Text: " "},
		{Text: entry.Label, Color: labelColor}}
	// The narrow pane holds the label and nothing else; there is no room for a stat tail.
	if compact {
		return clampWorkflowLine(head, width)
	}
	if model := formatWorkflowModel(entry, false); model != "" {
		head = append(head, workflowCardSegment{Text: " " + model, Color: "dim"})
	}
	for _, part := range append(workflowSubStatusAnnotations(entry, display, now), workflowRowStatSegments(entry)...) {
		head = append(head, workflowCardSegment{Text: " · ", Color: "dim"}, workflowCardSegment{Text: part, Color: "dim"})
	}
	// The duration sits flush right, so a column of rows reads as a column of durations.
	if d := int64Or0(entry.DurationMs); d != 0 {
		return rightAlignWorkflowLine(head, workflowCardLine{{Text: workflowFormatDuration(float64(d)) + " ", Color: "dim"}}, width)
	}
	return clampWorkflowLine(head, width)
}

func workflowPlural(n int, singular string) string {
	if n == 1 {
		return singular
	}
	return singular + "s"
}

// layoutWorkflowDialog builds the dialog: two panes side by side inside one frame, and two levels of depth. The frame
// is a fixed height so the dialog does not jump as the selection moves through runs of very different sizes.
func layoutWorkflowDialog(in workflowDialogInput) []workflowCardLine {
	g := unicodeDialogGlyphs
	if in.ASCII {
		g = asciiDialogGlyphs
	}
	termWidth := in.Width
	if termWidth == 0 {
		termWidth = workflowDefaultWidth
	}
	width := workflowDialogContentWidth(termWidth)
	now := in.Now
	if now == 0 {
		now = wfNowMs()
	}
	view := resolveWorkflowDialog(in)
	state := in.State
	// What the panes may hold; the frame's actual height is settled below, once there is something to measure.
	bodyRows := in.BodyRows
	if bodyRows == 0 {
		bodyRows = defaultPaneBodyRows
	}
	capacity := max(minPaneBodyRows, bodyRows)
	spinnerFrame := in.SpinnerFrame

	var lines []workflowCardLine

	// Header: the run's name, then its description with the stats flush right.
	head := workflowHeaderLine(in.Task.header(), in.Meta, view.Groups, in.AgentCount, now)
	lines = append(lines, clampWorkflowLine(workflowCardLine{{Text: " "}, {Text: head.Name, Color: "toolTitle", Bold: true}}, width))
	var subtext workflowCardLine
	if head.Subtext != "" {
		subtext = workflowCardLine{{Text: " "}, {Text: head.Subtext, Color: "dim"}}
	}
	lines = append(lines, rightAlignWorkflowLine(subtext, workflowCardLine{{Text: head.Stats, Color: "dim"}}, width))
	lines = append(lines, workflowCardLine{})

	frameWidth := width - 1
	leftWidth := leftPaneWidth(frameWidth)
	rightWidth := max(1, frameWidth-leftWidth-3)
	inPhases := state.Level == dialogLevelPhases
	entry := view.SelectedEntry

	// The pane listing phases, shown only at the overview level.
	var phaseRows []workflowCardLine
	digits := len(strconv.Itoa(len(view.Groups)))
	start, end := windowRange(view.ClampedPhase, len(view.Groups), capacity)
	for i := start; i < min(end, len(view.Groups)); i++ {
		group := view.Groups[i]
		selected := i == view.ClampedPhase
		color := "dim"
		switch {
		case selected:
			color = "accent"
		case group.Status == "done":
			color = "success"
		case group.Status == "failed":
			color = "error"
		}
		// An unfinished phase shows its number where a finished one shows a glyph, so the list doubles as a numbered plan.
		glyph := strconv.Itoa(i + 1)
		switch group.Status {
		case "done":
			glyph = g.tick
		case "failed":
			glyph = g.cross
		}
		pointer := " "
		if selected {
			pointer = g.pointer
		}
		var count workflowCardLine
		if group.TotalCount != 0 {
			count = workflowCardLine{{Text: strconv.Itoa(group.DoneCount) + "/" + strconv.Itoa(group.TotalCount) + " ", Color: color}}
		}
		phaseRows = append(phaseRows, rightAlignWorkflowLine(workflowCardLine{{Text: " "}, {Text: pointer, Color: "accent"}, {Text: " "},
			{Text: padStartWidth(glyph, digits), Color: color}, {Text: " "}, {Text: group.Title, Color: color}}, count, leftWidth))
	}

	// The pane listing this phase's agents: the right pane at the overview level and the left in the subview.
	agentPaneWidth := leftWidth
	if inPhases {
		agentPaneWidth = rightWidth
	}
	var agentRows []workflowCardLine
	if len(view.VisibleAgents) == 0 {
		agentRows = append(agentRows, clampWorkflowLine(workflowCardLine{{Text: "   " + copyNoAgents, Color: "dim"}}, agentPaneWidth))
	} else {
		start, end := windowRange(view.ClampedAgent, len(view.VisibleAgents), capacity)
		for i := start; i < min(end, len(view.VisibleAgents)); i++ {
			agentRows = append(agentRows, workflowAgentRow(view.VisibleAgents[i], i == view.ClampedAgent, !inPhases, agentPaneWidth, g, view.WorkflowActive, spinnerFrame, now))
		}
	}

	// The detail pane, in the subview only.
	var detailRows []workflowCardLine
	if !inPhases && entry != nil {
		display := workflowDisplayState(entry, view.WorkflowActive)
		// Prefers the canonical provider/model-id here: this pane has the width for it.
		model := formatWorkflowModel(entry, true)
		first := workflowCardLine{{Text: " "}, dialogRowGlyph(display, g, spinnerFrame), {Text: " " + workflowStatusWord(display), Color: "muted"}}
		if model != "" {
			first = append(first, workflowCardSegment{Text: " · ", Color: "dim"}, workflowCardSegment{Text: model, Color: "dim"})
		}
		detailRows = append(detailRows, clampWorkflowLine(first, rightWidth))
		// Rebuilt rather than filtered out of the row's stat segments: the model and the agent type are already on the
		// line above, and the token count wants its unit here exactly as it has one in the row.
		var stats []string
		if thinking := formatWorkflowThinking(entry); thinking != "" {
			stats = append(stats, thinking)
		}
		if t := intOr0(entry.Tokens); t != 0 {
			stats = append(stats, formatCompactTokens(t)+" tok")
		}
		if n := intOr0(entry.ToolCalls); n != 0 {
			stats = append(stats, strconv.Itoa(n)+" tool "+workflowPlural(n, "call"))
		}
		if d := int64Or0(entry.DurationMs); d != 0 {
			stats = append(stats, workflowFormatDuration(float64(d)))
		}
		if len(stats) > 0 {
			detailRows = append(detailRows, clampWorkflowLine(workflowCardLine{{Text: " " + strings.Join(stats, " · "), Color: "dim"}}, rightWidth))
		}

		prompt := previewWorkflowLines(entry.PromptPreview)
		collapsed := !state.PromptExpanded && len(prompt) > promptCollapsedLines
		var promptSuffix []string
		if len(prompt) > 0 {
			promptSuffix = append(promptSuffix, strconv.Itoa(len(prompt))+" "+workflowPlural(len(prompt), "line"))
		}
		if len(prompt) > promptCollapsedLines {
			verb := "expand"
			if state.PromptExpanded {
				verb = "collapse"
			}
			promptSuffix = append(promptSuffix, g.enter+" "+verb)
		}
		detailRows = append(detailRows, workflowCardLine{}, workflowDetailHeading("Prompt", promptSuffix, rightWidth))
		if len(prompt) == 0 {
			detailRows = append(detailRows, workflowDetailBody(copyAvailableOnceStart, rightWidth))
		} else {
			shown := prompt
			if collapsed {
				shown = prompt[:promptCollapsedLines]
			}
			for _, text := range shown {
				detailRows = append(detailRows, workflowDetailBody(text, rightWidth))
			}
			// Named rather than silently cut: the reader has to know the prompt goes on.
			if collapsed {
				hidden := len(prompt) - promptCollapsedLines
				detailRows = append(detailRows, workflowDetailBody(g.ellipsis+" "+strconv.Itoa(hidden)+" more "+workflowPlural(hidden, "line"), rightWidth))
			}
		}

		toolCalls := intOr0(entry.ToolCalls)
		var activitySuffix []string
		if toolCalls > 0 {
			activitySuffix = []string{strconv.Itoa(toolCalls) + " tool " + workflowPlural(toolCalls, "call")}
		}
		detailRows = append(detailRows, workflowCardLine{}, workflowDetailHeading("Activity", activitySuffix, rightWidth),
			workflowDetailBody(workflowActivityBody(entry, display), rightWidth))

		detailRows = append(detailRows, workflowCardLine{}, workflowDetailHeading("Outcome", nil, rightWidth))
		// Wrapped, not clamped: the outcome is the thing the reader came for.
		for _, text := range widthx.WrapTextWithAnsi(workflowOutcomeBody(entry, display), max(1, rightWidth-4)) {
			detailRows = append(detailRows, workflowDetailBody(text, rightWidth))
		}
	}

	phaseTitle := "Phases"
	if view.ClampedPhase < len(view.Groups) {
		phaseTitle = view.Groups[view.ClampedPhase].Title
	}
	// With a filter on, the count is of what survived it, so the title says which filter.
	shown := len(view.VisibleAgents)
	agentPaneTitle := phaseTitle + " · " + strconv.Itoa(shown) + " " + workflowPlural(shown, "agent")
	if state.Filter != "all" {
		agentPaneTitle = phaseTitle + " · " + strconv.Itoa(shown) + " " + state.Filter
	}
	leftRows, rightRows := agentRows, detailRows
	leftTitle, rightTitle := agentPaneTitle, copyNoAgents
	if entry != nil {
		rightTitle = entry.Label
	}
	if inPhases {
		leftRows, rightRows = phaseRows, agentRows
		leftTitle, rightTitle = "Phases", agentPaneTitle
	}
	// Indented one column, so the frame's left edge lines up under the name and the description.
	for _, line := range paneFrame(leftTitle, rightTitle, leftRows, rightRows, width-1,
		// Tall enough for whichever pane holds more, and no taller.
		min(capacity, max(minPaneBodyRows, len(leftRows), len(rightRows))), g) {
		lines = append(lines, append(workflowCardLine{{Text: " "}}, line...))
	}

	// Key hints: only the actions the run can currently take, so the footer never advertises a key that does nothing.
	can := func(action string) bool {
		if v, ok := in.Available[action]; ok {
			return v
		}
		return true
	}
	var hints []string
	if inPhases {
		hints = append(hints, g.upDown+" select")
		if len(view.VisibleAgents) > 0 {
			hints = append(hints, g.enter+" open")
		}
		hints = append(hints, "f filter")
	} else {
		hints = append(hints, g.upDown+" agent")
		if len(previewWorkflowLines(entryPrompt(entry))) > promptCollapsedLines {
			hints = append(hints, g.enter+" prompt")
		}
		skip, retry := workflowAgentActions(entry, view.WorkflowActive)
		if skip && can("onSkipAgent") {
			hints = append(hints, "s skip")
		}
		if retry && can("onRetryAgent") {
			hints = append(hints, "r retry")
		}
	}
	if view.Paused && can("onResume") {
		hints = append(hints, "p resume")
	} else if view.WorkflowActive && can("onPause") {
		hints = append(hints, "p pause")
	}
	if view.WorkflowActive && can("onKill") {
		hints = append(hints, "x stop")
	}
	if inPhases {
		hints = append(hints, "esc close")
	} else {
		hints = append(hints, "esc back")
	}
	// Advertised at BOTH levels, because the key works at both, and only for a row that has a record id. Last and
	// abbreviated: the footer is clamped rather than wrapped, so what sits at the end is what an 80-column terminal drops.
	if view.SelectedEntry != nil && view.SelectedEntry.RecordID != "" && can("onOpenAgent") {
		hints = append(hints, "c convo")
	}
	lines = append(lines, clampWorkflowLine(workflowCardLine{{Text: " " + strings.Join(hints, " · "), Color: "dim"}}, width))
	return lines
}

func entryPrompt(e *workflowAgentEntry) string {
	if e == nil {
		return ""
	}
	return e.PromptPreview
}

// padStartWidth is String.padStart for a short ASCII or single-cell text.
func padStartWidth(s string, width int) string {
	if gap := width - widthx.VisibleWidth(s); gap > 0 {
		return strings.Repeat(" ", gap) + s
	}
	return s
}

func nextWorkflowFilter(filter string) string {
	for i, f := range workflowDialogFilters {
		if f == filter {
			return workflowDialogFilters[(i+1)%len(workflowDialogFilters)]
		}
	}
	return workflowDialogFilters[0]
}

// workflowDialogKeyResult is the next state and, where the key is an action, what the caller should do about it.
type workflowDialogKeyResult struct {
	State  workflowDialogState
	Action *workflowDialogAction
}

// handleWorkflowDialogKey maps a keypress to the next state and, where the key is an action, what the caller should do
// about it. Pure: nil means "not ours". Movement clamps at both ends rather than wrapping.
func handleWorkflowDialogKey(data string, state workflowDialogState, view resolvedWorkflowDialog) *workflowDialogKeyResult {
	act := func(a workflowDialogAction) *workflowDialogKeyResult {
		return &workflowDialogKeyResult{State: state, Action: &a}
	}
	// Ctrl+C is the reflex for backing out of a full-screen overlay, so it closes outright from either level.
	if tui.MatchesKey(data, "ctrl+c") {
		return act(workflowDialogAction{Kind: "cancel"})
	}
	// Back one level before out of the dialog: esc in the subview returns to the overview, and only closes from there.
	if tui.MatchesKey(data, "escape") || tui.MatchesKey(data, "q") {
		if state.Level == dialogLevelAgent {
			next := state
			next.Level, next.PromptExpanded = dialogLevelPhases, false
			return &workflowDialogKeyResult{State: next}
		}
		return act(workflowDialogAction{Kind: "cancel"})
	}
	if tui.MatchesKey(data, "left") && state.Level == dialogLevelAgent {
		next := state
		next.Level, next.PromptExpanded = dialogLevelPhases, false
		return &workflowDialogKeyResult{State: next}
	}

	down := tui.MatchesKey(data, "j") || tui.MatchesKey(data, "down")
	up := tui.MatchesKey(data, "k") || tui.MatchesKey(data, "up")
	if down || up {
		delta := -1
		if down {
			delta = 1
		}
		if state.Level == dialogLevelPhases {
			next := clampWorkflowIndex(view.ClampedPhase+delta, len(view.Groups))
			// Changing phase re-points the agent list at a different set of rows, so the old row index is meaningless.
			if next == view.ClampedPhase {
				return &workflowDialogKeyResult{State: state}
			}
			s := state
			s.SelectedPhase, s.SelectedAgent = next, 0
			return &workflowDialogKeyResult{State: s}
		}
		s := state
		s.SelectedAgent = clampWorkflowIndex(view.ClampedAgent+delta, len(view.VisibleAgents))
		return &workflowDialogKeyResult{State: s}
	}

	// One key, two jobs: open the selected phase's agents, then expand the prompt of the one you opened.
	if tui.MatchesKey(data, "enter") || tui.MatchesKey(data, "right") {
		s := state
		if state.Level == dialogLevelPhases {
			// Nothing to open, so nothing happens: entering an empty pane would strand the reader in a subview.
			if len(view.VisibleAgents) == 0 {
				return &workflowDialogKeyResult{State: state}
			}
			s.Level, s.PromptExpanded = dialogLevelAgent, false
			return &workflowDialogKeyResult{State: s}
		}
		s.PromptExpanded = !state.PromptExpanded
		return &workflowDialogKeyResult{State: s}
	}
	if tui.MatchesKey(data, "e") {
		s := state
		s.PromptExpanded = !state.PromptExpanded
		return &workflowDialogKeyResult{State: s}
	}

	// The one key that opens something instead of changing the run, so it works at both levels and on a settled agent.
	// A row with no record id has no conversation to open, and falls through as unbound.
	if tui.MatchesKey(data, "c") {
		if view.SelectedEntry == nil || view.SelectedEntry.RecordID == "" {
			return nil
		}
		return act(workflowDialogAction{Kind: "open", RecordID: view.SelectedEntry.RecordID})
	}

	// The filter re-points the agent list, so it belongs to the level that shows the whole list.
	if tui.MatchesKey(data, "f") && state.Level == dialogLevelPhases {
		s := state
		s.Filter, s.SelectedAgent = nextWorkflowFilter(state.Filter), 0
		return &workflowDialogKeyResult{State: s}
	}

	// Gated on the run being live, exactly as the footer's hints are: a settled run has nothing left to stop.
	if tui.MatchesKey(data, "x") {
		if view.WorkflowActive {
			return act(workflowDialogAction{Kind: "kill"})
		}
		return nil
	}
	if tui.MatchesKey(data, "p") {
		if !view.WorkflowActive {
			return nil
		}
		if view.Paused {
			return act(workflowDialogAction{Kind: "resume"})
		}
		return act(workflowDialogAction{Kind: "pause"})
	}
	skip, retry := workflowAgentActions(view.SelectedEntry, view.WorkflowActive)
	if tui.MatchesKey(data, "s") && skip && view.SelectedEntry != nil {
		return act(workflowDialogAction{Kind: "skip", Index: view.SelectedEntry.Index})
	}
	if tui.MatchesKey(data, "r") && retry && view.SelectedEntry != nil {
		return act(workflowDialogAction{Kind: "retry", Index: view.SelectedEntry.Index})
	}
	return nil
}

// workflowDialogActions are the actions the dialog needs from the workflow runtime, injected; a nil function is an
// action the caller did not wire (the footer then does not advertise it).
type workflowDialogActions struct {
	OnKill, OnPause, OnResume func()
	// OnSkipAgent and OnRetryAgent take the entry's stable index, not its row position.
	OnSkipAgent, OnRetryAgent func(index int)
	// OnOpenAgent opens the selected agent's conversation: the only key that shows something rather than changing the
	// run. recordId is the manager's id for the child.
	OnOpenAgent func(recordID string)
}

func (a workflowDialogActions) available() map[string]bool {
	return map[string]bool{"onKill": a.OnKill != nil, "onPause": a.OnPause != nil, "onResume": a.OnResume != nil,
		"onSkipAgent": a.OnSkipAgent != nil, "onRetryAgent": a.OnRetryAgent != nil, "onOpenAgent": a.OnOpenAgent != nil}
}

// workflowDialog is the `/agents → Workflows` overlay: deliberately thin. It owns the spinner timer and the theme, and
// delegates everything else to the two pure functions above. The source is re-read every render so a live run updates
// in place without any subscription plumbing; the phase groups are cached against the log's revision, so a spinner
// frame costs the visible rows and not a fold of the whole log.
type workflowDialog struct {
	mu           sync.Mutex
	host         viewerHost
	source       func() workflowDialogSource
	theme        uiTheme
	done         func()
	actions      workflowDialogActions
	state        workflowDialogState
	spinnerFrame int
	closed       bool
	stop         chan struct{}

	groups        []phaseGroup
	groupsVersion int
	groupsMeta    *workflowMeta
}

func newWorkflowDialog(host viewerHost, source func() workflowDialogSource, th uiTheme, done func(), actions workflowDialogActions, initialPhaseIndex int) *workflowDialog {
	d := &workflowDialog{host: host, source: source, theme: th, done: done, actions: actions, state: initialWorkflowDialogState(initialPhaseIndex),
		stop: make(chan struct{})}
	stop := d.stop
	safeGo("workflow dialog spinner", func() {
		ticker := time.NewTicker(workflowDialogSpinnerMs * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				d.mu.Lock()
				d.spinnerFrame++
				closed := d.closed
				d.mu.Unlock()
				if !closed {
					host.RequestRender()
				}
			}
		}
	})
	return d
}

// input is the source plus the dialog's own state, with the phase groups reused while the log is unchanged.
func (d *workflowDialog) input(state workflowDialogState) workflowDialogInput {
	src := d.source()
	in := workflowDialogInput{workflowDialogSource: src, State: state}
	if src.Version != 0 {
		if d.groups == nil || d.groupsVersion != src.Version || d.groupsMeta != src.Meta {
			var declared []workflowPhaseMeta
			if src.Meta != nil {
				declared = src.Meta.Phases
			}
			d.groups, d.groupsVersion, d.groupsMeta = buildPhaseGroups(src.Progress, declared), src.Version, src.Meta
		}
		in.Groups = d.groups
	}
	return in
}

func (d *workflowDialog) HandleInput(data string) {
	d.mu.Lock()
	in := d.input(d.state)
	result := handleWorkflowDialogKey(data, d.state, resolveWorkflowDialog(in))
	if result == nil {
		d.mu.Unlock()
		return
	}
	d.state = result.State
	var action *workflowDialogAction
	if result.Action != nil {
		a := *result.Action
		action = &a
		if a.Kind == "cancel" {
			d.closed = true
		}
	}
	d.mu.Unlock()
	if action != nil {
		d.dispatch(*action)
	}
	d.host.RequestRender()
}

func (d *workflowDialog) Render(width int) []string {
	d.mu.Lock()
	in := d.input(d.state)
	in.Width, in.SpinnerFrame = width, d.spinnerFrame
	// Derived from what was actually injected, so the footer advertises only the keys this dialog can service.
	in.Available = d.actions.available()
	d.mu.Unlock()
	return styleWorkflowCardLines(layoutWorkflowDialog(in), d.theme)
}

func (d *workflowDialog) Dispose() {
	d.mu.Lock()
	d.closed = true
	stop := d.stop
	d.stop = nil
	d.mu.Unlock()
	if stop != nil {
		close(stop)
	}
}

func (d *workflowDialog) dispatch(a workflowDialogAction) {
	switch a.Kind {
	case "cancel":
		d.done()
	case "kill":
		if d.actions.OnKill != nil {
			d.actions.OnKill()
		}
	case "pause":
		if d.actions.OnPause != nil {
			d.actions.OnPause()
		}
	case "resume":
		if d.actions.OnResume != nil {
			d.actions.OnResume()
		}
	case "skip":
		if d.actions.OnSkipAgent != nil {
			d.actions.OnSkipAgent(a.Index)
		}
	case "retry":
		if d.actions.OnRetryAgent != nil {
			d.actions.OnRetryAgent(a.Index)
		}
	case "open":
		if d.actions.OnOpenAgent != nil {
			d.actions.OnOpenAgent(a.RecordID)
		}
	}
}

// plainWorkflowDialogLines is the dialog as plain text.
func plainWorkflowDialogLines(lines []workflowCardLine) []string {
	return plainWorkflowCardLines(lines)
}
