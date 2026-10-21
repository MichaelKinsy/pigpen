package pi_permission_system

import (
	"math"
	"strings"
)

// Port of src/presentation/dialog-renderer.ts: render a PromptPayload for a human deciding an ask (ADR 0011 section 5). The
// payload is complete by contract, so this is where elision happens. One fact per line, `label : value`, labels aligned; a fact
// whose text an earlier line already carries is not repeated. Pure functions over strings: no host call, bounded by the budget.

// RenderBudget is how much room a render has, as the operator configured it. MaxRows and FieldMaxWidth are JavaScript numbers (the
// complete view uses Infinity).
type RenderBudget struct {
	// MaxRows is the maximum rendered rows.
	MaxRows float64
	// FieldMaxWidth is the maximum characters (UTF-16 units) of any one field's text.
	FieldMaxWidth float64
}

// DialogBudget is a RenderBudget against the width its rows are counted at.
type DialogBudget struct {
	RenderBudget
	// Width is the terminal width the lines are wrapped to, so a row count is meaningful.
	Width int
}

// DefaultRenderBudget is the budget when the operator configures neither field (DEFAULT_RENDER_BUDGET): twenty-four rows and four
// hundred characters.
var DefaultRenderBudget = RenderBudget{MaxRows: 24, FieldMaxWidth: 400}

// PromptBudgetConfig is the two prompt-budget knobs, as the extension config carries them (nil is absent).
type PromptBudgetConfig struct {
	PromptMaxRows       *float64
	PromptFieldMaxWidth *float64
}

// ResolveRenderBudget is the configured budget, falling back per field to DefaultRenderBudget.
func ResolveRenderBudget(config PromptBudgetConfig) RenderBudget {
	budget := DefaultRenderBudget
	if config.PromptMaxRows != nil {
		budget.MaxRows = *config.PromptMaxRows
	}
	if config.PromptFieldMaxWidth != nil {
		budget.FieldMaxWidth = *config.PromptFieldMaxWidth
	}
	return budget
}

// HighlightPaint paints the flagged element (the command, path or target the rule fired on). A render concern: the fallback and the
// review log pass nil (plain text); only the TUI has a theme to paint with.
type HighlightPaint func(text string) string

// DialogView is what a renderer produced, and whether it had to leave anything out.
type DialogView struct {
	// Lines are wrapped to the budget's width: each entry is one visual row.
	Lines []string
	// Elided is true when any field was shortened or any entry dropped.
	Elided bool
}

// CompleteViewBudget is the budget that elides nothing: the complete view an operator must be able to reach while the decision is
// pending (ADR 0011 section 4).
func CompleteViewBudget(width int) DialogBudget {
	return DialogBudget{RenderBudget: RenderBudget{MaxRows: math.Inf(1), FieldMaxWidth: math.Inf(1)}, Width: width}
}

// promptFact is one rendered fact.
type promptFact struct{ label, text string }

// cappedFact is a fact narrowed to the budget, and whether that cost it anything.
type cappedFact struct {
	promptFact
	clipped bool
}

const (
	elisionMarker     = "\u2026"
	elisionMarkerRows = 1
)

// RenderPromptDialog renders a payload for a human deciding an ask. A nil paint leaves the text plain.
//
// The evidence is laid out and wrapped only as far as the row budget can show it: once the rows already counted overrun maxRows the
// rest cannot appear (an entry is shown whole or not at all), so a payload carrying thousands of entries costs no more to render than
// one that fits. The answer is the one the original computes by wrapping every entry first.
func RenderPromptDialog(payload PromptPayload, budget DialogBudget, paint HighlightPaint) DialogView {
	if paint == nil {
		paint = func(text string) string { return text }
	}
	var facts []cappedFact
	for _, fact := range coreFacts(payload) {
		facts = append(facts, capField(fact, budget.FieldMaxWidth))
	}
	coreCount := len(facts)
	for _, fact := range evidenceFacts(payload) {
		facts = append(facts, capField(fact, budget.FieldMaxWidth))
	}
	elided := false
	width := 0
	for _, f := range facts {
		elided = elided || f.clipped
		if n := utf16LenWTF(f.label); n > width {
			width = n
		}
	}
	flagged := FlaggedElements(payload)
	block := func(f cappedFact) []string {
		return FitLinesToWidth(layoutFact(f.promptFact, width, flagged, paint), budget.Width)
	}

	var coreLines []string
	for _, f := range facts[:coreCount] {
		coreLines = append(coreLines, block(f)...)
	}
	// The evidence blocks, wrapped until their rows (with the core's) first exceed the budget.
	var evidence [][]string
	total := 0
	for _, f := range facts[coreCount:] {
		b := block(f)
		evidence = append(evidence, b)
		total += len(b)
		if float64(len(coreLines)+total) > budget.MaxRows {
			break
		}
	}
	lines, dropped := fitToRows(coreLines, evidence, len(facts)-coreCount, budget.MaxRows)
	for i := range lines {
		lines[i] = toWellFormed(lines[i])
	}
	return DialogView{Lines: lines, Elided: elided || dropped}
}

// capField narrows one field's text to the budget: a quantity bound applied uniformly, never a content filter. The marker is a
// bare ellipsis.
func capField(fact promptFact, fieldMaxWidth float64) cappedFact {
	length := utf16LenWTF(fact.text)
	if float64(length) <= fieldMaxWidth {
		return cappedFact{fact, false}
	}
	end := jsSliceEnd(fieldMaxWidth, length)
	return cappedFact{promptFact{fact.label, utf16Prefix(fact.text, end) + elisionMarker}, true}
}

// fitToRows fits the rendered blocks into the row budget. The core is exempt and the evidence is what gives way; a drop costs one
// row for its marker, taken only when there is something to mark. An entry is shown whole or not at all. evidence holds the blocks
// rendered so far and entries the number of evidence entries there are: fewer blocks than entries means the rest were left unrendered
// because the rows already counted overrun the budget.
func fitToRows(core []string, evidence [][]string, entries int, maxRows float64) (lines []string, dropped bool) {
	total := 0
	for _, block := range evidence {
		total += len(block)
	}
	if len(evidence) == entries && float64(len(core)+total) <= maxRows {
		lines = append([]string{}, core...)
		for _, block := range evidence {
			lines = append(lines, block...)
		}
		return lines, false
	}
	limit := maxRows - elisionMarkerRows
	lines = append([]string{}, core...)
	for _, block := range evidence {
		if float64(len(lines)+len(block)) > limit {
			break
		}
		lines = append(lines, block...)
	}
	if float64(len(lines)) < maxRows {
		lines = append(lines, elisionMarker)
	}
	return lines, true
}

// coreFacts is the invariant core (ADR 0011 section 3), in reading order: who is asking, what they called, what gated it, the
// decision-relevant value, and what will actually run.
func coreFacts(payload PromptPayload) []promptFact {
	request := payload.Request
	var facts []promptFact
	if requester, ok := requesterFact(payload); ok {
		facts = append(facts, requester)
	}
	if request.ToolName != nil {
		facts = append(facts, promptFact{"tool", toolText(payload)})
	}
	// The surface is stated already when it is the tool name (a bash ask) or the word the value line is labelled with.
	label := ValueLabel(payload)
	if (request.ToolName == nil || request.Surface != *request.ToolName) && request.Surface != label {
		facts = append(facts, promptFact{"surface", request.Surface})
	}
	if request.MatchedPattern != nil {
		facts = append(facts, promptFact{"rule", *request.MatchedPattern})
	}
	if request.Value != "" && (request.ToolName == nil || request.Value != *request.ToolName) {
		facts = append(facts, promptFact{label, request.Value})
	}
	if request.ExecutedUnit != nil {
		facts = append(facts, promptFact{"runs", *request.ExecutedUnit})
	}
	if context, ok := DescribeBashCommandContext(request.CommandContext); ok {
		facts = append(facts, promptFact{"context", context})
	}
	return facts
}

// evidenceFacts is the decision evidence, in payload order. An entry's detail rides its own line rather than becoming a second
// entry, so an elision can never show a path while dropping what it resolves to.
func evidenceFacts(payload PromptPayload) []promptFact {
	facts := make([]promptFact, len(payload.Evidence))
	for i, entry := range payload.Evidence {
		text := entry.Text
		if entry.Detail != nil {
			text = entry.Text + " \u2192 " + *entry.Detail
		}
		facts[i] = promptFact{entry.Label, text}
	}
	return facts
}

// requesterFact is who is asking. A forwarded ask always names its requester; an unnamed local requester states nothing.
func requesterFact(payload PromptPayload) (promptFact, bool) {
	requester := payload.Request.Requester
	if !requester.Forwarded {
		if requester.AgentName != nil && *requester.AgentName != "" {
			return promptFact{"agent", *requester.AgentName}, true
		}
		return promptFact{}, false
	}
	// || intentional: a version-skewed request carries "" rather than null.
	name := "unknown"
	if requester.AgentName != nil && *requester.AgentName != "" {
		name = *requester.AgentName
	}
	if requester.SessionID != nil && *requester.SessionID != "" {
		return promptFact{"subagent", name + " \u00b7 session " + *requester.SessionID}, true
	}
	return promptFact{"subagent", name}, true
}

// toolText is the gated tool, and the name the agent actually called when they differ.
func toolText(payload PromptPayload) string {
	toolName, invoked := *payload.Request.ToolName, payload.Request.InvokedToolName
	if invoked == nil {
		return toolName
	}
	return toolName + " (invoked as " + *invoked + ")"
}

// jsPadEnd is String.prototype.padEnd(n) with the default space filler, over UTF-16 length.
func jsPadEnd(s string, n int) string {
	if pad := n - utf16LenWTF(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

// layoutFact aligns one fact's label into the `label : value` column of the given width. A field carrying its own newlines continues
// under the column rather than back at the margin.
func layoutFact(fact promptFact, width int, flagged []string, paint HighlightPaint) []string {
	// A fact that is the flagged element paints whole; any other line paints the whole-token occurrences of it.
	isFlagged := inStrings(flagged, fact.text)
	highlight := func(line string) string {
		if isFlagged {
			return paint(line)
		}
		return paintTokens(line, flagged, paint)
	}
	indent := strings.Repeat(" ", width+3)
	parts := strings.Split(fact.text, "\n")
	block := make([]string, len(parts))
	for j, line := range parts {
		if j == 0 {
			block[j] = jsPadEnd(fact.label, width) + " : " + highlight(line)
		} else {
			block[j] = indent + highlight(line)
		}
	}
	return block
}

// isTokenCharacter is /[\w/.-]/ on one UTF-16 unit: ASCII only, so a non-ASCII byte is never one.
func isTokenCharacter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '/' || c == '.' || c == '-'
}

// paintTokens paints every whole-token occurrence of each flagged text within one line.
func paintTokens(line string, flagged []string, paint HighlightPaint) string {
	for _, needle := range flagged {
		line = paintOccurrences(line, needle, paint)
	}
	return line
}

func paintOccurrences(line, needle string, paint HighlightPaint) string {
	if needle == "" || strings.Contains(needle, "\n") {
		return line
	}
	var result strings.Builder
	cursor := 0
	for {
		rel := strings.Index(line[cursor:], needle)
		if rel < 0 {
			break
		}
		at := cursor + rel
		end := at + len(needle)
		// A missing neighbour reads as a space, which is not a token character.
		whole := (at == 0 || !isTokenCharacter(line[at-1])) && (end >= len(line) || !isTokenCharacter(line[end]))
		result.WriteString(line[cursor:at])
		if whole {
			result.WriteString(paint(needle))
		} else {
			result.WriteString(needle)
		}
		cursor = end
	}
	result.WriteString(line[cursor:])
	return result.String()
}
