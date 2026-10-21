package tintinweb_subagents

import (
	"strconv"
	"strings"

	tui "github.com/MichaelKinsy/pigpen/components/pitui"
	"github.com/MichaelKinsy/pigpen/components/pitui/jsstring"
	"github.com/MichaelKinsy/pigpen/components/pitui/widthx"
)

// The inline transcript card for a running workflow: a header with the stats flush right, a phase tree of agent rows,
// the log() output, and a size warning. The layout is pure: it returns coloured segments and never touches a theme or
// a terminal, so the same layout draws the SubagentWorkflow tool's result and a standalone session entry (a workflow
// launched from a flag has no tool call to hang a result on). The glyphs key off the raw entry state, not the derived
// display state: a skipped or blocked agent is a plain cross here, and only the dialog tells them apart.
// upstream: src/ui/workflow-card.ts.

const (
	// workflowTickMs is the header re-render cadence: the clock changes once a second and the running glyph is static.
	workflowTickMs = 1000
	// workflowLabelColumnMax is the widest label column before stats stop being aligned and just follow the label.
	workflowLabelColumnMax = 28
	// workflowDefaultWidth is the width used when the caller does not know the terminal's.
	workflowDefaultWidth = 80
	// workflowReplayedAnnotation is what a row replayed from a resume journal says instead of a duration.
	workflowReplayedAnnotation = "from resume journal"
)

// workflowGlyphs is a glyph tier of the card.
type workflowGlyphs struct {
	pointer, tick, cross, running                            string
	groupTop, groupMid, groupBottom                          string
	vertical, branch, lastBranch, log, warning               string
}

var unicodeWorkflowGlyphs = workflowGlyphs{
	pointer: "▸", tick: "✔", cross: "✘", running: "⟳", groupTop: "╭─", groupMid: "├─", groupBottom: "╰─",
	vertical: "│", branch: "├─", lastBranch: "└─", log: "⎿", warning: "⚠",
}

// asciiWorkflowGlyphs is the `figures` ASCII tier, for terminals that cannot draw the box set. Every glyph keeps its
// unicode counterpart's column width.
var asciiWorkflowGlyphs = workflowGlyphs{
	pointer: ">", tick: "√", cross: "×", running: "*", groupTop: ",-", groupMid: "|-", groupBottom: "`-",
	vertical: "|", branch: "|-", lastBranch: "`-", log: "\\", warning: "!",
}

// workflowCardSegment is a run of text with a theme colour ("" leaves the terminal default) and an optional bold.
type workflowCardSegment struct {
	Text  string
	Color string
	Bold  bool
}

type workflowCardLine []workflowCardSegment

// workflowCardTask is the part of the task record the card reads.
type workflowCardTask struct {
	Status        string
	WorkflowName  string
	Summary       string
	Description   string
	StartTime     int64
	EndTime       *int64
	TotalPausedMs int64
}

type workflowCardInput struct {
	Progress   []workflowEntry
	Task       workflowCardTask
	Meta       *workflowMeta
	AgentCount int
	// TotalTokens is the total for the size warning; summed from the entries when nil.
	TotalTokens *int
	AgentCap    int
	TokenCap    int
	// Now is Date.now(); 0 reads the clock.
	Now   int64
	Width int // 0: the default width
	ASCII bool
	// ShowToolTitle leads with `▸ SubagentWorkflow`: true where the card stands alone (a session entry), false as a
	// tool result, where the call line directly above already says it.
	ShowToolTitle bool
}

func (t workflowCardTask) header() workflowHeaderTask {
	return workflowHeaderTask{Status: t.Status, WorkflowName: t.WorkflowName, Summary: t.Summary, Description: t.Description,
		StartTime: t.StartTime, EndTime: t.EndTime, TotalPausedMs: t.TotalPausedMs}
}

// formatCompactTokens is `18.4k` / `1.2M`: bare magnitude, since the row already reads as a stat.
func formatCompactTokens(count int) string {
	switch {
	case count >= 1_000_000:
		return jsstring.ToFixed(float64(count)/1_000_000, 1) + "M"
	case count >= 1_000:
		return jsstring.ToFixed(float64(count)/1_000, 1) + "k"
	}
	return strconv.Itoa(count)
}

// formatWorkflowModel is one label for the model pair. A fallback that never differed from the primary is noise, so it
// only shows when the run has two models in play. canonical prefers `provider/model-id` for surfaces with the width.
func formatWorkflowModel(entry *workflowAgentEntry, canonical bool) string {
	model := entry.Model
	if canonical && entry.ModelID != "" {
		model = entry.ModelID
	}
	primary := model
	switch {
	case model != "" && entry.FallbackModel != "" && model != entry.FallbackModel:
		primary = model + "→" + entry.FallbackModel
	case model == "":
		primary = entry.FallbackModel
	}
	if primary == "" {
		return ""
	}
	// Disclosed rather than substituted: a model an agent file pinned over the script's is still a model the script did
	// not get. Only when the two differ, so a request that was honoured says nothing.
	if entry.RequestedModel != "" && entry.RequestedModel != primary {
		return primary + " (asked " + entry.RequestedModel + ")"
	}
	return primary
}

// formatWorkflowThinking is the thinking level, and what was asked for when it was not honoured.
func formatWorkflowThinking(entry *workflowAgentEntry) string {
	if entry.Thinking == "" {
		return ""
	}
	if entry.RequestedThinking != "" && entry.RequestedThinking != entry.Thinking {
		return "thinking: " + entry.Thinking + " (asked " + entry.RequestedThinking + ")"
	}
	return "thinking: " + entry.Thinking
}

func intOr0(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func int64Or0(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// workflowAgentStatSegments is the `·`-separated tail of an agent row, in the recovered order: agentType, model, tokens,
// toolCalls, durationMs. Absent values drop out entirely.
func workflowAgentStatSegments(entry *workflowAgentEntry) []string {
	var parts []string
	if entry.AgentType != "" {
		parts = append(parts, entry.AgentType)
	}
	if model := formatWorkflowModel(entry, false); model != "" {
		parts = append(parts, model)
	}
	if t := intOr0(entry.Tokens); t != 0 {
		parts = append(parts, formatCompactTokens(t))
	}
	if n := intOr0(entry.ToolCalls); n != 0 {
		parts = append(parts, strconv.Itoa(n)+" tool "+map[bool]string{true: "call", false: "calls"}[n == 1])
	}
	if d := int64Or0(entry.DurationMs); d != 0 {
		parts = append(parts, workflowFormatDuration(float64(d)))
	}
	return parts
}

// workflowRowGlyph is the recovered inline mapping, keyed on the raw entry state.
func workflowRowGlyph(entry *workflowAgentEntry, g workflowGlyphs) workflowCardSegment {
	switch entry.State {
	case entryDone:
		return workflowCardSegment{Text: g.tick, Color: "success"}
	case entryError:
		return workflowCardSegment{Text: g.cross, Color: "error"}
	}
	return workflowCardSegment{Text: g.running}
}

// clampWorkflowLine trims a line to width, cutting inside whichever segment crosses the edge. truncateToWidth wraps its
// ellipsis in resets, which are stripped: the layout stays plain text until it is themed.
func clampWorkflowLine(line workflowCardLine, width int) workflowCardLine {
	clamped := make(workflowCardLine, 0, len(line))
	used := 0
	for _, seg := range line {
		w := widthx.VisibleWidth(seg.Text)
		if used+w <= width {
			clamped = append(clamped, seg)
			used += w
			continue
		}
		if room := width - used; room > 0 {
			cut := seg
			cut.Text = widthx.StripTerminalSequences(widthx.TruncateToWidth(seg.Text, room, "…", false))
			clamped = append(clamped, cut)
		}
		return clamped
	}
	return clamped
}

func workflowLineWidth(line workflowCardLine) int {
	sum := 0
	for _, s := range line {
		sum += widthx.VisibleWidth(s.Text)
	}
	return sum
}

func workflowGlyphsFor(ascii bool) workflowGlyphs {
	if ascii {
		return asciiWorkflowGlyphs
	}
	return unicodeWorkflowGlyphs
}

// layoutWorkflowCard builds the card. Everything derived (the phase tree, the header counts, the logs, the size
// warning) comes from the progress model; what happens here is arrangement.
func layoutWorkflowCard(in workflowCardInput) []workflowCardLine {
	g := workflowGlyphsFor(in.ASCII)
	width := in.Width
	if width == 0 {
		width = workflowDefaultWidth
	}
	width = max(1, width)
	now := in.Now
	if now == 0 {
		now = wfNowMs()
	}
	var declared []workflowPhaseMeta
	if in.Meta != nil {
		declared = in.Meta.Phases
	}
	groups := buildPhaseGroups(in.Progress, declared)
	collapsed := collapseWorkflowProgress(in.Progress)
	totals := progressStats(in.Progress, in.AgentCount)
	head := workflowHeaderLine(in.Task.header(), in.Meta, groups, in.AgentCount, now)

	var lines []workflowCardLine

	// Header: `<name>` with the stats flush right. The tool name appears only when nothing above the card carries it.
	var left workflowCardLine
	if in.ShowToolTitle {
		left = workflowCardLine{{Text: g.pointer + " ", Color: "toolTitle"}, {Text: "SubagentWorkflow", Color: "toolTitle", Bold: true},
			{Text: "  "}, {Text: head.Name, Color: "muted"}}
	} else {
		left = workflowCardLine{{Text: "  "}, {Text: head.Name, Color: "toolTitle", Bold: true}}
	}
	statsWidth := widthx.VisibleWidth(head.Stats)
	clampedLeft := clampWorkflowLine(left, max(0, width-statsWidth-1))
	gap := max(1, width-workflowLineWidth(clampedLeft)-statsWidth)
	lines = append(lines, append(append(workflowCardLine{}, clampedLeft...), workflowCardSegment{Text: strings.Repeat(" ", gap)}, workflowCardSegment{Text: head.Stats, Color: "dim"}))

	if head.Subtext != "" {
		lines = append(lines, clampWorkflowLine(workflowCardLine{{Text: "  " + head.Subtext, Color: "dim"}}, width))
	}

	// Phase tree. Stats line up in one column across the whole card; a label past the cap pushes its own stats along.
	labelColumn := 0
	for _, group := range groups {
		for _, a := range group.Agents {
			labelColumn = max(labelColumn, widthx.VisibleWidth(a.Label))
		}
	}
	labelColumn = min(workflowLabelColumnMax, labelColumn)

	for groupIndex, group := range groups {
		lastGroup := groupIndex == len(groups)-1
		// One box, not a stack of them: only the first group opens it and only the last closes it.
		opener := g.groupMid
		switch {
		case lastGroup:
			opener = g.groupBottom
		case groupIndex == 0:
			opener = g.groupTop
		}
		lines = append(lines, clampWorkflowLine(workflowCardLine{{Text: "  "}, {Text: opener + " ", Color: "dim"}, {Text: group.Title}}, width))

		rail := g.vertical + " "
		if lastGroup {
			rail = "  "
		}
		for agentIndex, entry := range group.Agents {
			lastAgent := agentIndex == len(group.Agents)-1
			branch := g.branch
			if lastAgent {
				branch = g.lastBranch
			}
			segments := workflowCardLine{{Text: "  "}, {Text: rail, Color: "dim"}, {Text: branch + " ", Color: "dim"}, workflowRowGlyph(entry, g), {Text: " "}}
			// Prepended rather than folded into the stat tail: a replayed agent otherwise renders as a tick with no
			// tokens and no duration, which reads as one that did the work for free.
			statParts := workflowAgentStatSegments(entry)
			if entry.Cached {
				statParts = append([]string{workflowReplayedAnnotation}, statParts...)
			}
			pad := max(0, labelColumn-widthx.VisibleWidth(entry.Label))
			if len(statParts) > 0 {
				segments = append(segments, workflowCardSegment{Text: entry.Label + strings.Repeat(" ", pad)})
			} else {
				segments = append(segments, workflowCardSegment{Text: entry.Label})
			}
			for _, part := range statParts {
				segments = append(segments, workflowCardSegment{Text: " · ", Color: "dim"}, workflowCardSegment{Text: part, Color: "dim"})
			}
			lines = append(lines, clampWorkflowLine(segments, width))
		}
	}

	// log() output, below the tree.
	for _, message := range collapsed.Logs {
		parts := strings.Split(message, "\n")
		lines = append(lines, clampWorkflowLine(workflowCardLine{{Text: "  " + g.log + "  " + parts[0], Color: "dim"}}, width))
		for _, continuation := range parts[1:] {
			lines = append(lines, clampWorkflowLine(workflowCardLine{{Text: "     " + continuation, Color: "dim"}}, width))
		}
	}

	// Size warning.
	totalTokens := 0
	if in.TotalTokens != nil {
		totalTokens = *in.TotalTokens
	} else {
		for _, a := range collapsed.Agents {
			totalTokens += intOr0(a.Tokens)
		}
	}
	if warning := workflowSizeWarning(workflowSizeWarningInput{ScheduledAgents: max(in.AgentCount, totals.Total), StartedAgents: totals.Started,
		TotalTokens: totalTokens, AgentCap: in.AgentCap, TokenCap: in.TokenCap}); warning != nil {
		lines = append(lines, clampWorkflowLine(workflowCardLine{{Text: "  " + g.warning + " Large workflow · /agents → Workflows to stop", Color: "warning"}}, width))
	}
	return lines
}

// plainWorkflowCardLines is the card as plain text.
func plainWorkflowCardLines(lines []workflowCardLine) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		var b strings.Builder
		for _, s := range line {
			b.WriteString(s.Text)
		}
		out[i] = b.String()
	}
	return out
}

// styleWorkflowCardLines applies the theme. Nothing here changes the layout, only its colours.
func styleWorkflowCardLines(lines []workflowCardLine, th uiTheme) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		var b strings.Builder
		for _, s := range line {
			text := s.Text
			if s.Bold {
				text = th.Bold(text)
			}
			if s.Color != "" {
				text = th.Fg(s.Color, text)
			}
			b.WriteString(text)
		}
		out[i] = b.String()
	}
	return out
}

// renderWorkflowCard is the card as the lines of one Text component rendered at width: a tool result or a session entry.
func renderWorkflowCard(in workflowCardInput, th uiTheme, width int) []string {
	return tui.NewText(strings.Join(styleWorkflowCardLines(layoutWorkflowCard(in), th), "\n"), 0, 0, nil).Render(width)
}

// renderWorkflowEntryCard is the card for a session entry, from the JSON a flag-launched run persisted. The same layout
// the tool result uses, not a second one: the only difference is showToolTitle, because a session entry stands alone.
// nil data draws nothing, which is what Pi's renderer contract wants.
func renderWorkflowEntryCard(data *workflowEntryData, th uiTheme, width int) []string {
	if data == nil {
		return nil
	}
	tokens := data.TotalTokens
	return renderWorkflowCard(workflowCardInput{Progress: data.Progress,
		Task: workflowCardTask{Status: data.Status, WorkflowName: data.Name, StartTime: data.StartTime, EndTime: data.EndTime},
		Meta: data.Meta, AgentCount: data.AgentCount, TotalTokens: &tokens, ShowToolTitle: true}, th, width)
}
