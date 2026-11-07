package posthorse

// Port of src/ui/cards.ts: the native cards of the four tools. The original
// returns pi-tui components from renderCall/renderResult; every one of them
// is a pure function of the width, so the Go SDK's line renderers
// (sdk.ToolRenderers.Call/Result: "renders lines at the requested width")
// draw the same rows. Pi owns the card shell and the click/Ctrl+O expansion
// (renderShell stays "default", cards.ts:335-359).

import (
	"math"
	"regexp"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
)

// cardToolName is ToolName (cards.ts:23).
type cardToolName = string

// cardTitle is `titles[name]` (cards.ts:24-29).
func cardTitle(name cardToolName) string {
	switch name {
	case "notes":
		return "Notes"
	case "history":
		return "History"
	case "get_context_remaining":
		return "Context budget"
	}
	return "New context"
}

// cardCallState is CallState (cards.ts:32-37).
type cardCallState struct {
	isError          bool
	isPartial        bool
	executionStarted bool
	expanded         bool
}

// argPresent is `args[key] !== undefined`: a decoded JSON null is a present key.
func argPresent(args map[string]any, key string) bool {
	_, ok := args[key]
	return ok
}

// cardCallTarget: cards.ts:39-47.
func cardCallTarget(name cardToolName, args map[string]any) string {
	if args["op"] == "search" {
		return " · “" + cardArgument(args["query"], "query") + "”"
	}
	if name == "notes" && (args["op"] != "list" || argPresent(args, "path")) {
		return " · " + cardArgument(args["path"], "path")
	}
	if name == "history" && args["op"] == "read" {
		return " · " + cardArgument(args["id"], "id")
	}
	return ""
}

// cardCallStatus: cards.ts:49-57.
func cardCallStatus(state cardCallState) string {
	if state.isError {
		return " · error"
	}
	if !state.isPartial {
		return ""
	}
	if state.executionStarted {
		return " · running"
	}
	return " · preparing"
}

// cardCallDetails: cards.ts:59-76.
func cardCallDetails(name cardToolName, args map[string]any) string {
	var parts []string
	if name == "history" && args["op"] == "search" {
		scope := "current branch"
		if all, _ := args["all"].(bool); all {
			scope = "all project sessions"
		}
		parts = append(parts, "Scope: "+scope)
	}
	for _, label := range [...][2]string{{"offset", "Offset"}, {"imageOffset", "Image offset"}, {"limit", "Limit"}} {
		value, present := args[label[0]]
		if !present {
			continue
		}
		if f, ok := value.(float64); ok {
			parts = append(parts, label[1]+": "+n(f))
		} else {
			parts = append(parts, label[1]+": [invalid "+label[0]+"]")
		}
	}
	return strings.Join(parts, " · ")
}

// cardCallComponent is cardCallComponent (cards.ts:78-112) rendered at width.
func cardCallComponent(name cardToolName, args map[string]any, theme sdk.UITheme, state cardCallState, width int) []string {
	action := ""
	switch name {
	case "notes", "history":
		action = " · " + cardArgument(args["op"], "op")
	case "new_context":
		action = " · request"
	}
	marker := "▸"
	if state.isPartial {
		marker = "…"
	} else if state.expanded {
		marker = "▾"
	}
	label := marker + " " + cardTitle(name) + action + cardCallStatus(state) + cardCallTarget(name, args)
	titleColor := "toolTitle"
	if state.isError {
		titleColor = colorError
	}
	heading := theme.Fg(titleColor, theme.Bold(label))
	if !state.expanded {
		return []string{tui.TruncateToWidth(uiCollapseSpaces(heading), width, tui.DefaultEllipsis, false)}
	}
	rows := piText(heading, width)
	return append(rows, cardBlock(cardCallDetails(name, args), theme, colorMuted)(width)...)
}

// cardResultFacts is ResultFacts (cards.ts:114-125).
type cardResultFacts struct {
	name       cardToolName
	raw        string
	args       map[string]any
	display    *posthorseDisplay
	isError    bool
	isPartial  bool
	expanded   bool
	imageCount int
	showImages bool
	expandKey  string
}

// cardSubmittedText: cards.ts:127-140.
func cardSubmittedText(facts *cardResultFacts) string {
	args := facts.args
	if handoff, ok := args["handoff"].(string); ok && facts.name == "new_context" {
		return "Handoff supplied:\n" + handoff
	}
	if facts.name == "notes" && (args["op"] == "write" || args["op"] == "append") {
		if content, ok := args["content"].(string); ok {
			if len(content) == 0 {
				content = "(empty)"
			}
			return "Submitted content:\n" + content
		}
	}
	return ""
}

// cardResultSummary: cards.ts:142-159.
func cardResultSummary(facts *cardResultFacts, sections []historySection) string {
	if facts.isError || facts.isPartial {
		return ""
	}
	display := facts.display
	summary := displaySummary(display, facts.args, sections != nil)
	if display != nil && (display.kind == "notes-list" || display.kind == "notes-search") &&
		display.page != nil && (display.page.offset > 0 || display.page.end < display.page.total) {
		more := display.more != nil && *display.more
		return summary + "\n" + pageSummary(*display.page, more)
	}
	return summary
}

// cardResultColor: cards.ts:161-171.
func cardResultColor(facts *cardResultFacts) outputColor {
	if facts.isError {
		return colorError
	}
	display := facts.display
	if facts.isPartial || display == nil || display.kind != "context" || display.rollover != "unsupported" {
		return colorToolOutput
	}
	if display.usage && display.usageTokens != nil {
		return colorWarning
	}
	return colorToolOutput
}

// cardResultBody: cards.ts:173-184.
func cardResultBody(facts *cardResultFacts) string {
	if len(uiTrimBoth(facts.raw)) > 0 {
		return facts.raw
	}
	if facts.isError {
		return "No error details returned."
	}
	if facts.isPartial {
		return "Running…"
	}
	if facts.name == "notes" && facts.args["op"] == "read" {
		return "Empty note"
	}
	return "No text returned."
}

// cardNoteHeaderLength: cards.ts:186-191.
// The number stays a double: `headerLength > 0` and `slice(headerLength)` (ToIntegerOrInfinity) of foreign JSON
// differ from an int conversion for 0.5 and 1e19.
func cardNoteHeaderLength(facts *cardResultFacts) float64 {
	display := facts.display
	if !facts.isError && display != nil && (display.kind == "note-read" || display.kind == "notes-list") && display.headerLength != nil {
		return *display.headerLength
	}
	return 0
}

// cardHistoryPrefix is the known history header of results that have no spans
// (cards.ts:204-206), the pattern of `/^…/gm` without the anchor: the
// scan below applies it at the start of every JS line.
var cardHistoryPrefix = regexp.MustCompile(`\A[^\n]*?\[window [^\]\n]+\] \[[^\]\n]+\](?: \[chars \d+-\d+ of \d+\])? `)

// cardStripHistoryPrefixes is `body.replace(cardHistoryPrefix, "")` with the `m`
// flag: `^` matches after every JS line terminator (\n, \r, U+2028, U+2029).
func cardStripHistoryPrefixes(body string) string {
	if !strings.Contains(body, "[window ") {
		return body
	}
	var b strings.Builder
	last := 0 // end of the text copied so far
	for cand := 0; cand >= 0 && cand <= len(body); {
		if m := cardHistoryPrefix.FindStringIndex(body[cand:]); m != nil {
			b.WriteString(body[last:cand])
			last = cand + m[1]
			// The pattern ends in a space, so the next line start is after the match.
			cand = cardNextLineStart(body, last)
			continue
		}
		cand = cardNextLineStart(body, cand)
	}
	b.WriteString(body[last:])
	return b.String()
}

// cardNextLineStart is the first line start after i, or -1.
func cardNextLineStart(s string, i int) int {
	for ; i < len(s); i++ {
		switch s[i] {
		case '\n', '\r':
			return i + 1
		case 0xe2:
			if strings.HasPrefix(s[i:], "\u2028") || strings.HasPrefix(s[i:], "\u2029") {
				return i + 3
			}
		}
	}
	return -1
}

// cardPreviewBody: cards.ts:193-209.
func cardPreviewBody(facts *cardResultFacts, sections []historySection) string {
	if sections != nil {
		var b strings.Builder
		for i, s := range sections {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(s.body)
		}
		return b.String()
	}
	body := cardResultBody(facts)
	if headerLength := cardNoteHeaderLength(facts); headerLength > 0 {
		return u16sliceF(body, headerLength, math.Inf(1))
	}
	// Old results have no spans. Only the preview sheds the known history prefix.
	if facts.name == "history" && !facts.isError {
		return cardStripHistoryPrefixes(body)
	}
	return body
}

// cardAttachmentSummary: cards.ts:211-216.
func cardAttachmentSummary(facts *cardResultFacts) string {
	if facts.imageCount == 0 {
		return ""
	}
	s := cardItoa(facts.imageCount) + " image"
	if facts.imageCount != 1 {
		s += "s"
	}
	s += " attached"
	if !facts.showImages {
		s += " (display hidden)"
	}
	return s
}

// cardFullBody: cards.ts:218-236.
func cardFullBody(facts *cardResultFacts, sections []historySection, theme sdk.UITheme, width int) []string {
	if sections == nil {
		return cardBlock(cardResultBody(facts), theme, cardResultColor(facts))(width)
	}
	var b strings.Builder
	for i, s := range sections {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(theme.Fg(colorToolOutput, cleanText(s.body)))
		b.WriteByte('\n')
		b.WriteString(theme.Fg(colorDim, cleanText(s.header)))
	}
	return piText(b.String(), width)
}

// cardSearchPreviews: cards.ts:238-248.
func cardSearchPreviews(facts *cardResultFacts, sections []historySection, theme sdk.UITheme) []string {
	if sections == nil || facts.display == nil || facts.display.kind != "history-search" {
		return nil
	}
	out := make([]string, 0, 3)
	for _, s := range sections[:min(3, len(sections))] {
		out = append(out, theme.Fg(colorToolOutput, uiCollapseSpaces(cleanText(s.body))))
	}
	return out
}

// cardExpandedView: cards.ts:252-274.
func cardExpandedView(facts *cardResultFacts, sections []historySection, theme sdk.UITheme, width int) []string {
	rows := cardFullBody(facts, sections, theme, width)
	rows = append(rows, cardBlock(cardAttachmentSummary(facts), theme, colorMuted)(width)...)
	display := facts.display
	footer := ""
	if sections != nil && display != nil && display.kind == "history-search" && display.footerLength != nil && *display.footerLength > 0 {
		rawLength := u16len(facts.raw)
		footer = u16slice(facts.raw, rawLength-int(*display.footerLength), rawLength)
	}
	rows = append(rows, cardBlock(footer, theme, colorMuted)(width)...)
	if submission := cardSubmittedText(facts); len(submission) > 0 {
		rows = append(rows, "")
		rows = append(rows, cardBlock(submission, theme, colorToolOutput)(width)...)
	}
	return rows
}

// cardPreviewSource: cards.ts:276-289. searches non-nil is the history search form.
func cardPreviewSource(facts *cardResultFacts, sections []historySection, theme sdk.UITheme) func(width int) []string {
	if summaryOnly(facts.display) && !facts.isError && !facts.isPartial {
		return func(int) []string { return nil }
	}
	if searches := cardSearchPreviews(facts, sections, theme); searches != nil {
		return func(width int) []string {
			out := make([]string, len(searches))
			for i, line := range searches {
				out[i] = tui.TruncateToWidth(line, width, tui.DefaultEllipsis, false)
			}
			return out
		}
	}
	return cardLeadingRows(cardPreviewBody(facts, sections), theme, cardResultColor(facts), 3)
}

// cardCollapsedView: cards.ts:291-321.
func cardCollapsedView(facts *cardResultFacts, sections []historySection, theme sdk.UITheme, width int) []string {
	summaryRows := cardBlock(cardResultSummary(facts, sections), theme, cardResultColor(facts))(width)
	if len(summaryRows) > 5 {
		summaryRows = summaryRows[:5]
	}
	rows := cardPreviewSource(facts, sections, theme)(width)
	attachment := cardAttachmentSummary(facts)
	details := summaryOnly(facts.display) || sections != nil || len(cardSubmittedText(facts)) > 0 || cardNoteHeaderLength(facts) > 0
	room := 5 - len(summaryRows)
	if len(attachment) > 0 {
		room--
	}
	shown := rows[:min(len(rows), max(0, min(3, room)))]
	out := make([]string, 0, len(summaryRows)+len(shown)+2)
	out = append(out, summaryRows...)
	out = append(out, shown...)
	if len(attachment) > 0 {
		out = append(out, tui.TruncateToWidth(theme.Fg(colorMuted, attachment), width, tui.DefaultEllipsis, false))
	}
	if details || len(rows) > len(shown) {
		out = append(out, cardHint(theme, width, facts.expandKey))
	}
	return out
}

// cardResultComponent: cards.ts:323-333.
func cardResultComponent(facts *cardResultFacts, theme sdk.UITheme, width int) []string {
	var sections []historySection
	if !facts.isError && !facts.isPartial {
		sections = historySections(facts.raw, facts.display)
	}
	if facts.expanded {
		return cardExpandedView(facts, sections, theme, width)
	}
	return cardCollapsedView(facts, sections, theme, width)
}

// toolCards is toolCards (cards.ts:335-359): the renderers of one tool.
// renderCall/renderResult map to the SDK's line renderers.
func toolCards(name cardToolName) sdk.ToolRenderers {
	return sdk.ToolRenderers{
		Call: func(ctx sdk.Context, args map[string]any, render sdk.ToolRenderContext, width int) ([]string, error) {
			state := cardCallState{
				isError:          render.IsError,
				isPartial:        render.IsPartial,
				executionStarted: render.ExecutionStarted,
				expanded:         render.Expanded,
			}
			return cardCallComponent(name, cardArgsOf(args), ctx.UITheme(), state, width), nil
		},
		Result: func(ctx sdk.Context, result sdk.ToolRenderResult, options sdk.ToolRenderResultOptions, render sdk.ToolRenderContext, width int) ([]string, error) {
			images := 0
			for _, part := range result.Content {
				if part["type"] == "image" {
					images++
				}
			}
			facts := &cardResultFacts{
				name:       name,
				raw:        cardTextOfBlocks(result.Content),
				args:       cardArgsOf(render.Args),
				display:    displayOf(result.Details),
				isError:    render.IsError,
				isPartial:  options.IsPartial,
				expanded:   options.Expanded,
				imageCount: images,
				showImages: render.ShowImages,
				// Pi injects keyText so the hint shows the user's configured keys.
				expandKey: render.KeyText("app.tools.expand"),
			}
			return cardResultComponent(facts, ctx.UITheme(), width), nil
		},
	}
}

// applyToolCards is `{ ...toolCards(name) }` spread into a tool definition.
func applyToolCards(def *sdk.ToolDefinition, name cardToolName) {
	cards := toolCards(name)
	def.RenderShell = cards.Shell
	def.RenderCall = cards.Call
	def.RenderResult = cards.Result
}
