package ansi

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
)

func init() {
	registerTerminal("todo", Builder{Call: termTodoCall, Result: termTodoResult})
	registerTerminal("ask", Builder{Call: termAskCall, Result: termAskResult})
	registerTerminal("task", Builder{Call: termTaskCall, Result: termTaskResult})
	registerTerminal("subagent", Builder{Call: termTaskCall, Result: termTaskResult})
	registerTerminal("eval", Builder{Call: termEvalCall, Result: termEvalResult})
}

// ═══════════════════════════════════════════════════════════════════════════
// todo tool
// upstream: packages/tui/src/tools/todo.ts
// ═══════════════════════════════════════════════════════════════════════════

func phaseRomanNumeral(n int) string {
	if n <= 0 {
		return ""
	}
	pairs := []struct {
		v int
		s string
	}{
		{1000, "M"}, {900, "CM"}, {500, "D"}, {400, "CD"},
		{100, "C"}, {90, "XC"}, {50, "L"}, {40, "XL"},
		{10, "X"}, {9, "IX"}, {5, "V"}, {4, "IV"}, {1, "I"},
	}
	var b strings.Builder
	for _, pr := range pairs {
		for n >= pr.v {
			b.WriteString(pr.s)
			n -= pr.v
		}
	}
	return b.String()
}

func formatTodoOp(op map[string]any) string {
	verb := strField(op, "op")
	if verb == "" {
		verb = "update"
	}
	parts := []string{flattenLine(verb)}
	if t, ok := op["task"].(string); ok && t != "" {
		parts = append(parts, flattenLine(t))
	} else if ts := stringSliceField(op, "task"); len(ts) == 1 {
		parts = append(parts, flattenLine(ts[0]))
	} else if len(ts) > 1 {
		parts = append(parts, formatCount("task", len(ts)))
	}
	if ph := strField(op, "phase"); ph != "" {
		parts = append(parts, flattenLine(ph))
	}
	if items := asSlice(op["items"]); len(items) > 0 {
		parts = append(parts, formatCount("item", len(items)))
	}
	return strings.Join(parts, " ")
}

func normalizeTodoOps(args map[string]any) []map[string]any {
	if args == nil {
		return nil
	}
	if rawOps := asSlice(args["ops"]); len(rawOps) > 0 {
		var out []map[string]any
		for _, item := range rawOps {
			if m := asMap(item); m != nil {
				out = append(out, m)
			}
		}
		return out
	}
	if strField(args, "op") != "" {
		return []map[string]any{args}
	}
	return nil
}

func isClosedTodo(task map[string]any) bool {
	st := strField(task, "status")
	return st == "completed" || st == "abandoned"
}

func isActiveTodo(task map[string]any) bool {
	return strField(task, "status") == "in_progress"
}

// selectCollapsedTodos mirrors packages/tui/src/tools/todo.ts:219-277:
// 1 closed item of context above the open window, all in-progress tasks first,
// then following pending tasks up to cap, summary `… N more todos` (no expand hint).
func selectCollapsedTodos(tasks []map[string]any, capN int) ([]map[string]any, string) {
	var open, closed []map[string]any
	for _, t := range tasks {
		if isClosedTodo(t) {
			closed = append(closed, t)
		} else {
			open = append(open, t)
		}
	}
	selectWithin := func(base []map[string]any) ([]map[string]any, string) {
		if len(base) <= capN+1 {
			return base, ""
		}
		var active []map[string]any
		firstActiveIdx := 0
		foundActive := false
		for i, t := range base {
			if isActiveTodo(t) {
				if !foundActive {
					firstActiveIdx = i
					foundActive = true
				}
				active = append(active, t)
			}
		}
		if len(active) > capN {
			hidden := len(active) - capN
			return active[:capN], fmt.Sprintf("… %d more active %s", hidden, pluralize("todo", hidden))
		}
		var fill []map[string]any
		for i := firstActiveIdx; i < len(base) && len(active)+len(fill) < capN; i++ {
			if isActiveTodo(base[i]) {
				continue
			}
			fill = append(fill, base[i])
		}
		items := append(append([]map[string]any(nil), active...), fill...)
		hidden := len(base) - len(items)
		summary := ""
		if hidden > 0 {
			summary = formatMoreItems(hidden, "todo")
		}
		return items, summary
	}

	if len(open) == 0 {
		return selectWithin(tasks)
	}
	var lead []map[string]any
	if len(closed) > 0 {
		lead = closed[len(closed)-1:]
	}
	sel, summary := selectWithin(open)
	return append(lead, sel...), summary
}

func computeTouchedPhases(args map[string]any, phases []map[string]any, completed []any) map[string]bool {
	touched := map[string]bool{}
	// Active (in_progress) phases stay open (todo.ts:475-477).
	for _, ph := range phases {
		for _, t := range asSlice(ph["tasks"]) {
			if isActiveTodo(asMap(t)) {
				touched[strField(ph, "name")] = true
			}
		}
	}
	// Phases with a task that just transitioned to completed (todo.ts:479).
	for _, c := range completed {
		if ph := strField(asMap(c), "phase"); ph != "" {
			touched[ph] = true
		}
	}
	// Phases named by ops; `init` replaces the whole list so every phase is touched (todo.ts:482-497).
	for _, op := range normalizeTodoOps(args) {
		if strField(op, "op") == "init" {
			for _, ph := range phases {
				touched[strField(ph, "name")] = true
			}
			break
		}
		if ph := strField(op, "phase"); ph != "" {
			touched[ph] = true
		}
		for _, taskName := range stringSliceField(op, "task") {
			for _, ph := range phases {
				for _, t := range asSlice(ph["tasks"]) {
					if strField(asMap(t), "content") == taskName {
						touched[strField(ph, "name")] = true
					}
				}
			}
		}
	}
	if len(touched) == 0 {
		return nil
	}
	return touched
}

// formatTodoLine mirrors packages/tui/src/tools/todo.ts:432-460:
// checkbox.checked (`☑`) for completed, checkbox.unchecked (`☐`) for pending,
// in_progress (accent) and abandoned (error + strikethrough).
func (p palette) formatTodoLine(task map[string]any, _ bool) string {
	st := strField(task, "status")
	content := flattenLine(strField(task, "content"))
	switch st {
	case "completed":
		return p.fg("success", unicodeSymbols.checkboxChecked+" "+p.strike(content))
	case "in_progress":
		return p.fg("accent", unicodeSymbols.checkboxUnchecked+" "+content)
	case "abandoned":
		return p.fg("error", unicodeSymbols.checkboxUnchecked+" "+p.strike(content))
	default:
		return p.fg("dim", unicodeSymbols.checkboxUnchecked+" "+content)
	}
}

func termTodoCall(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	rawOps := normalizeTodoOps(args)
	var ops []string
	if len(rawOps) == 0 {
		ops = []string{"update"}
	} else {
		ops = make([]string, 0, len(rawOps))
		for _, op := range rawOps {
			ops = append(ops, formatTodoOp(op))
		}
	}
	// packages/tui/src/tools/todo.ts:609-616: ops go in meta, not description.
	header := p.renderStatusLine(statusLineOpts{icon: "pending", title: "Todo", meta: ops})
	return termWrappedTextNode(header, width), nil
}

func termTodoResult(ctx sdk.Context, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	details := asMap(res.Details)

	if rc.IsError {
		header := p.renderStatusLine(statusLineOpts{icon: "error", title: "Todo"})
		errText := resultText(res)
		if errText == "" {
			errText = "Todo operation failed"
		}
		rows := p.renderOutputBlock(blockOpts{
			header:      header,
			state:       "error",
			borderColor: "error",
			sections:    []blockSection{{lines: strings.Split(p.formatErrorDetail(errText), "\n")}},
			width:       width,
		})
		return termLinesNode(rows, width), nil
	}

	var phases []map[string]any
	totalTasks := 0
	for _, raw := range asSlice(details["phases"]) {
		ph := asMap(raw)
		tasks := asSlice(ph["tasks"])
		if len(tasks) > 0 {
			phases = append(phases, ph)
			totalTasks += len(tasks)
		}
	}

	// Literal `${n} tasks` as in packages/tui/src/tools/todo.ts:652.
	header := p.renderStatusLine(statusLineOpts{
		iconOverride: p.styledSym(unicodeSymbols.toolTodo, "accent"),
		title:        "Todo",
		meta:         []string{fmt.Sprintf("%d tasks", totalTasks)},
	})

	if totalTasks == 0 {
		fallback := flattenLine(resultText(res))
		if fallback == "" {
			fallback = "No todos"
		}
		return termLinesNode(fitHeaderedRows(header, []string{"  " + p.fg("dim", fallback)}, width), width), nil
	}

	completedList := asSlice(details["completedTasks"])
	completedByPhase := map[string]map[string]bool{}
	for _, raw := range completedList {
		c := asMap(raw)
		ph := strField(c, "phase")
		ct := strField(c, "content")
		if completedByPhase[ph] == nil {
			completedByPhase[ph] = map[string]bool{}
		}
		completedByPhase[ph][ct] = true
	}

	multiPhase := len(phases) > 1
	indent := ""
	if multiPhase {
		indent = "  "
	}
	var touched map[string]bool
	if !opts.Expanded && multiPhase {
		touched = computeTouchedPhases(rc.Args, phases, completedList)
	}

	contentW := outputBlockContentWidth(width, nil, nil)
	var bodyLines []string
	for pi, ph := range phases {
		phName := strField(ph, "name")
		rawTasks := asSlice(ph["tasks"])
		tasks := make([]map[string]any, 0, len(rawTasks))
		closedCount := 0
		for _, rt := range rawTasks {
			tm := asMap(rt)
			if tm != nil {
				tasks = append(tasks, tm)
				if isClosedTodo(tm) {
					closedCount++
				}
			}
		}
		displayName := phName
		if multiPhase {
			displayName = fmt.Sprintf("%s. %s", phaseRomanNumeral(pi+1), phName)
		}
		// packages/tui/src/tools/todo.ts:506-515: two spaces + closed/total (completed + abandoned),
		// no checkbox prefix on folded phase summaries.
		progress := p.fg("dim", fmt.Sprintf("  %d/%d", closedCount, len(tasks)))

		// Fold untouched phases to a one-line summary when collapsed (todo.ts:730-733).
		if touched != nil && !touched[phName] {
			bodyLines = append(bodyLines, p.fg("dim", p.bold(displayName))+progress)
			continue
		}

		if multiPhase {
			bodyLines = append(bodyLines, p.fg("accent", p.bold(displayName))+progress)
		}
		var selTasks []map[string]any
		var summary string
		if opts.Expanded {
			limit := min(len(tasks), maxExpandedOutputLines)
			selTasks = tasks[:limit]
			if len(tasks) > limit {
				summary = formatMoreItems(len(tasks)-limit, "todo")
			}
		} else {
			selTasks, summary = selectCollapsedTodos(tasks, previewCollapsedItems)
		}

		groups := make([][]string, len(selTasks))
		for ti, t := range selTasks {
			justDone := completedByPhase[phName][strField(t, "content")]
			groups[ti] = []string{p.formatTodoLine(t, justDone)}
		}
		treeLines := p.renderTreeGroups(groups, summary)
		for _, tl := range treeLines {
			bodyLines = append(bodyLines, wrapAnsi(indent+tl, contentW)...)
		}
	}

	todoState := "success"
	if opts.IsPartial {
		todoState = "partial"
	}
	rows := p.renderOutputBlock(blockOpts{
		header:      header,
		state:       todoState,
		noBg:        true, // packages/tui/src/tools/todo.ts:788 (applyBg: false)
		borderColor: "borderMuted",
		sections:    []blockSection{{lines: bodyLines}},
		width:       width,
	})
	return termLinesNode(rows, width), nil
}

// ═══════════════════════════════════════════════════════════════════════════
// ask tool
// upstream: packages/tui/src/tools/ask.ts
// ═══════════════════════════════════════════════════════════════════════════

const askTimedOutNote = "Timed out; unanswered fields were submitted as empty."

// termAskCall returns nil (empty container): per packages/tui/src/tools/ask.ts:227-230,
// the ask panel over the composer owns the questions while waiting.
func termAskCall(_ sdk.Context, _ map[string]any, _ sdk.ToolRenderContext, _ int) (kit.Node, error) {
	return nil, nil
}

func termAskResult(ctx sdk.Context, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, _ sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	details := asMap(res.Details)
	answers := asSlice(details["answers"])
	if len(answers) == 0 {
		fallback := flattenLine(resultText(res))
		header := p.renderStatusLine(statusLineOpts{icon: "warning", title: "Ask"})
		if fallback == "" {
			return termWrappedTextNode(header, width), nil
		}
		return termLinesNode(fitHeaderedRows(header, []string{p.fg("dim", fallback)}, width), width), nil
	}

	timedOut, _ := boolField(details, "timedOut")
	var hOpts statusLineOpts
	if timedOut {
		hOpts = statusLineOpts{icon: "warning", title: "Ask"}
	} else {
		hOpts = statusLineOpts{iconOverride: p.styledSym(unicodeSymbols.toolAsk, "accent"), title: "Ask"}
	}
	header := p.renderStatusLine(hOpts)

	notes := asSlice(details["notes"])
	contentW := outputBlockContentWidth(width, nil, nil)
	var body []string
	for _, raw := range answers {
		am := asMap(raw)
		q := flattenLine(strField(am, "question", "name"))
		body = append(body, wrapPrefixedLine(" ", " ", p.fg("text", q), contentW)...)

		ansText := formatAskAnswerValue(am)
		if ansText == "" {
			body = append(body, wrapPrefixedLine("   ", "   ", p.fg("dim", "(unanswered)"), contentW)...)
		} else {
			// packages/tui/src/tools/ask.ts:135-145: first line carries ` ✔ <answer>`,
			// continuation lines indent under the answer (`   `).
			ansPrefix := " " + p.statusIcon("success") + " "
			ansCont := strings.Repeat(" ", tui.VisibleWidth(ansPrefix))
			ansLines := sanitizeDisplayLines(ansText)
			limit := previewOutputCollapse
			if opts.Expanded {
				limit = previewOutputExpand
			}
			shown := min(len(ansLines), limit)
			for idx, al := range ansLines[:shown] {
				pref := ansCont
				if idx == 0 {
					pref = ansPrefix
				}
				body = append(body, wrapPrefixedLine(pref, ansCont, p.fg("toolOutput", al), contentW)...)
			}
			if rem := len(ansLines) - shown; rem > 0 {
				body = append(body, ansCont+p.fg("dim", formatMoreItems(rem, "line")+" "+p.formatExpandHint(opts.Expanded, true)))
			}
		}

		// Notes attached to this answer, wrapped and capped at 3/10 lines (ask.ts:262-283).
		qID := strField(am, "name", "id", "question")
		var noteLines []string
		for _, nr := range notes {
			nm := asMap(nr)
			if qID != "" && strField(nm, "question", "name", "field") != qID {
				continue
			}
			nLabel := fmt.Sprintf("✎ %s: %s", flattenLine(strField(nm, "option")), flattenLine(strField(nm, "text")))
			noteLines = append(noteLines, wrapPrefixedLine("   ", "   ", p.italic(p.fg("muted", nLabel)), contentW)...)
		}
		noteCap := previewOutputCollapse
		if opts.Expanded {
			noteCap = previewOutputExpand
		}
		if len(noteLines) > noteCap {
			hidden := len(noteLines) - noteCap
			body = append(body, noteLines[:noteCap]...)
			body = append(body, "   "+p.fg("dim", formatMoreItems(hidden, "line")+" "+p.formatExpandHint(opts.Expanded, true)))
		} else {
			body = append(body, noteLines...)
		}
	}
	if timedOut {
		body = append(body, wrapPrefixedLine(" ", " ", p.fg("dim", askTimedOutNote), contentW)...)
	}

	// Keep borderColor "borderMuted" even on timeout (ask.ts:291).
	askState := "success"
	if timedOut {
		askState = "warning"
	}
	rows := p.renderOutputBlock(blockOpts{
		header:      header,
		state:       askState,
		borderColor: "borderMuted",
		sections:    []blockSection{{lines: body}},
		width:       width,
	})
	return termLinesNode(rows, width), nil
}

func formatAskAnswerValue(am map[string]any) string {
	if am == nil {
		return ""
	}
	if s := strField(am, "answer", "value"); s != "" {
		return s
	}
	if vals := stringSliceField(am, "values"); len(vals) > 0 {
		return strings.Join(vals, ", ")
	}
	if vals := stringSliceField(am, "answers"); len(vals) > 0 {
		return strings.Join(vals, ", ")
	}
	if v, ok := am["value"]; ok && v != nil {
		return fmt.Sprint(v)
	}
	return ""
}

// ═══════════════════════════════════════════════════════════════════════════
// task / subagent tool
// upstream: packages/tui/src/tools/task.ts
// ═══════════════════════════════════════════════════════════════════════════

func formatTaskID(id string) string {
	return flattenLine(strings.ReplaceAll(id, ".", ">"))
}

// taskAgentTypeBadge mirrors packages/tui/src/tools/task.ts:514-518:
// dim `⟦agent⟧` badge for non-default agent types; empty for default `"task"`.
func (p palette) taskAgentTypeBadge(agent string) string {
	trimmed := strings.TrimSpace(flattenLine(agent))
	if trimmed == "" || trimmed == "task" {
		return ""
	}
	return " " + p.fg("dim", p.wrapBrackets(trimmed))
}

// formatAgentStatRun mirrors packages/tui/src/tools/agent-tree.ts:27-45:
// formats toolCount, requests, contextTokens/contextWindow (via formatContextUsage),
// and cost (`$X.XX` in statusLineCost) separated by ` · `.
func (p palette) formatAgentStatRun(m map[string]any) string {
	var b strings.Builder
	if tc, ok := intField(m, "toolCount"); ok && tc > 0 {
		b.WriteString(unicodeSymbols.sepDot)
		b.WriteString(p.fg("dim", fmt.Sprintf("%s %s", formatNumber(tc), unicodeSymbols.iconExtTool)))
	}
	if reqs, ok := intField(m, "requests"); ok && reqs > 0 {
		b.WriteString(unicodeSymbols.sepDot)
		b.WriteString(p.fg("dim", fmt.Sprintf("%s req", formatNumber(reqs))))
	}
	if ctxTok, ok := intField(m, "contextTokens"); ok && ctxTok > 0 {
		var ctxStr string
		if ctxWin, okW := intField(m, "contextWindow"); okW && ctxWin > 0 {
			pct := (float64(ctxTok) / float64(ctxWin)) * 100.0
			ctxStr = formatContextUsage(&pct, ctxWin, &ctxTok)
		} else {
			ctxStr = formatNumber(ctxTok)
		}
		b.WriteString(unicodeSymbols.sepDot)
		b.WriteString(p.fg("dim", ctxStr))
	}
	var cost float64
	if c, ok := floatField(m, "cost"); ok && c > 0 {
		cost = c
	} else if u := asMap(m["usage"]); u != nil {
		if cObj := asMap(u["cost"]); cObj != nil {
			if c, ok := floatField(cObj, "total"); ok && c > 0 {
				cost = c
			}
		}
	}
	if cost > 0 {
		b.WriteString(unicodeSymbols.sepDot)
		b.WriteString(p.fg("statusLineCost", fmt.Sprintf("$%.2f", cost)))
	}
	return b.String()
}

// renderTaskOutputSection mirrors packages/tui/src/tools/task.ts:336-460:
// renders JSON inline (collapsed) or tree (expanded, sibling root connectors),
// else `Output` heading with 3 lines collapsed / 12 lines expanded and `… N more lines`.
func (p palette) renderTaskOutputSection(output, continuePrefix string, expanded bool, maxCollapsed, maxExpanded int) []string {
	sanitized := strings.TrimRight(sanitizeDisplayText(output), " \t\r\n")
	if sanitized == "" {
		return nil
	}
	var lines []string
	if parsed, ok := tryParseTreeSafeJSON(sanitized); ok {
		if !expanded {
			inline := ""
			switch x := parsed.(type) {
			case map[string]any:
				inline = formatArgsInline(x, 80)
			default:
				inline = formatJSONScalar(x, 80)
			}
			if inline != "" {
				return []string{continuePrefix + p.fg("dim", inline)}
			}
		}
		lines = append(lines, continuePrefix+p.fg("dim", "Output"))
		treeLines, trunc := p.renderJSONTreeOpts(parsed, jsonTreeOpts{
			maxDepth:               6,
			maxLines:               24,
			maxScalarLen:           70,
			hiddenRootKeys:         nil,
			siblingRoots:           true,
			escapeStringWhitespace: false,
		})
		if len(treeLines) > 0 {
			for _, l := range treeLines {
				lines = append(lines, continuePrefix+"  "+l)
			}
			if trunc {
				lines = append(lines, continuePrefix+"  "+p.fg("dim", "…"))
			}
			return lines
		}
		lines = nil
	}
	lines = append(lines, continuePrefix+p.fg("dim", "Output"))
	outLines := sanitizeDisplayLines(sanitized)
	limit := maxCollapsed
	if expanded {
		limit = maxExpanded
	}
	shown := min(len(outLines), limit)
	for _, ol := range outLines[:shown] {
		lines = append(lines, continuePrefix+"  "+p.fg("dim", clipCollapsed(ol, 70, expanded)))
	}
	if len(outLines) > shown {
		lines = append(lines, continuePrefix+"  "+p.fg("dim", formatMoreItems(len(outLines)-shown, "line")))
	}
	return lines
}
func taskHeaderAgentLabel(args map[string]any) string {
	if a := strField(args, "agent"); a != "" {
		return a
	}
	if tasks := asSlice(args["tasks"]); len(tasks) > 0 {
		if a := strField(asMap(tasks[0]), "agent"); a != "" {
			return a
		}
	}
	return ""
}

func termTaskCall(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	tasks := asSlice(args["tasks"])
	var meta []string
	if len(tasks) > 0 {
		meta = []string{formatCount("agent", len(tasks))}
	} else if a := taskHeaderAgentLabel(args); a != "" {
		meta = []string{a}
	}
	header := p.renderStatusLine(statusLineOpts{
		iconOverride: p.styledSym(unicodeSymbols.toolTask, "accent"),
		title:        "Task",
		meta:         meta,
	})

	var body []string
	bullet := p.fg("dim", unicodeSymbols.formatBullet)
	if len(tasks) > 0 {
		capN := min(len(tasks), collapsedAgentLimit)
		for i := range capN {
			tm := asMap(tasks[i])
			idLbl := formatTaskID(strField(tm, "name"))
			if idLbl == "" {
				idLbl = fmt.Sprintf("#%d", i+1)
			}
			line := bullet + " " + p.fg("accent", p.bold(idLbl))
			if brief := previewLine(strField(tm, "task"), 64); brief != "" {
				line += ": " + p.fg("muted", brief)
			}
			line += p.taskAgentTypeBadge(strField(tm, "agent"))
			if iso, _ := boolField(tm, "isolated"); iso {
				line += p.fg("dim", " [isolated]")
			}
			body = append(body, line)
		}
		if len(tasks) > capN {
			body = append(body, bullet+" "+p.fg("dim", formatMoreItems(len(tasks)-capN, "agent")))
		}
	} else if brief := strField(args, "task"); brief != "" {
		contentW := outputBlockContentWidth(width, nil, nil)
		for _, l := range sanitizeDisplayLines(brief) {
			body = append(body, wrapAnsi(p.styleMarkdownLine(l), contentW)...)
		}
	}

	var sections []blockSection
	if len(body) > 0 {
		sections = []blockSection{{lines: body}}
	}
	rows := p.renderOutputBlock(blockOpts{
		header:      header,
		state:       "pending",
		borderColor: "borderMuted",
		sections:    sections,
		width:       width,
	})
	return termLinesNode(rows, width), nil
}

func (p palette) taskBriefSection(text string, contentW int) []string {
	trimmed := strings.TrimSpace(sanitizeDisplayText(text))
	if trimmed == "" {
		return nil
	}
	var out []string
	for _, line := range sanitizeDisplayLines(trimmed) {
		out = append(out, wrapAnsi(p.fg("muted", line), contentW)...)
	}
	return out
}

func (p palette) taskPrefixSections(args map[string]any, contentW int) []blockSection {
	var sections []blockSection
	if ctxLines := p.taskBriefSection(strField(args, "context"), contentW); len(ctxLines) > 0 {
		sections = append(sections, blockSection{lines: ctxLines})
	}
	if assignLines := p.taskBriefSection(strField(args, "task"), contentW); len(assignLines) > 0 {
		sections = append(sections, blockSection{lines: assignLines})
	}
	return sections
}

func termTaskResult(ctx sdk.Context, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	details := asMap(res.Details)
	fallbackText := strings.TrimSpace(resultText(res))
	agentLabel := taskHeaderAgentLabel(rc.Args)
	contentW := outputBlockContentWidth(width, nil, nil)
	prefixSections := p.taskPrefixSections(rc.Args, contentW)
	sessionID := sessionKeyFromCtx(ctx)

	if details == nil {
		icon := "success"
		border := "borderMuted"
		if rc.IsError {
			icon = "error"
			border = "error"
		}
		header := p.renderStatusLine(statusLineOpts{icon: icon, title: "Task", description: agentLabel})
		sections := append([]blockSection(nil), prefixSections...)
		if fallbackText != "" {
			lines := sanitizeDisplayLines(fallbackText)
			styled := make([]string, len(lines))
			tok := "dim"
			if rc.IsError {
				tok = "error"
			}
			for i, l := range lines {
				styled[i] = p.fg(tok, l)
			}
			sections = append(sections, blockSection{separator: len(sections) > 0, lines: styled})
		}
		taskNilState := "success"
		if rc.IsError {
			taskNilState = "error"
		}
		rows := p.renderOutputBlock(blockOpts{header: header, state: taskNilState, borderColor: border, sections: sections, width: width})
		return termLinesNode(rows, width), nil
	}

	results := asSlice(details["results"])
	progress := asSlice(details["progress"])

	abortedCount, failCount, mergeFailCount, successCount, reqTotal := 0, 0, 0, 0, 0
	for _, raw := range results {
		rm := asMap(raw)
		if req, ok := intField(rm, "requests"); ok {
			reqTotal += req
		}
		aborted, _ := boolField(rm, "aborted")
		exitCode, _ := intField(rm, "exitCode")
		errStr := strField(rm, "error")
		switch {
		case aborted:
			abortedCount++
		case exitCode != 0:
			failCount++
		case errStr != "":
			mergeFailCount++
		default:
			successCount++
		}
	}

	isErr := rc.IsError || abortedCount > 0 || failCount > 0
	mergeFailed := mergeFailCount > 0
	agentCount := len(results)
	if agentCount == 0 {
		agentCount = len(progress)
	}
	var meta []string
	if agentCount > 0 {
		meta = []string{formatCount("agent", agentCount)}
	} else if agentLabel != "" {
		meta = []string{agentLabel}
	}

	// Upstream task.ts:1429-1446: dispatch glyph `⇶` while running/partial, `✔`/`✘`/`⚠` when settled.
	var hOpts statusLineOpts
	switch {
	case opts.IsPartial:
		hOpts = statusLineOpts{iconOverride: p.styledSym(unicodeSymbols.toolTask, "accent"), title: "Task", meta: meta}
	case isErr:
		hOpts = statusLineOpts{icon: "error", title: "Task", meta: meta}
	case mergeFailed:
		hOpts = statusLineOpts{icon: "warning", title: "Task", meta: meta}
	default:
		hOpts = statusLineOpts{icon: "success", title: "Task", meta: meta}
	}
	header := p.renderStatusLine(hOpts)

	taskState := "success"
	switch {
	case opts.IsPartial:
		taskState = "partial"
	case isErr:
		taskState = "error"
	case mergeFailed:
		taskState = "warning"
	}
	borderColor := "borderMuted"
	if isErr {
		borderColor = "error"
	}

	// If both results and progress are empty (e.g. validation error like unknown agent),
	// ALWAYS render fallbackText so the error message is never dropped (task.ts:1571-1582).
	if len(results) == 0 && len(progress) == 0 {
		txt := fallbackText
		if txt == "" {
			txt = "No results"
		}
		tok := "dim"
		if isErr {
			tok = "error"
		}
		var errLines []string
		for _, l := range sanitizeDisplayLines(txt) {
			errLines = append(errLines, p.fg(tok, l))
		}
		rows := p.renderOutputBlock(blockOpts{
			header:      header,
			state:       taskState,
			borderColor: borderColor,
			sections:    []blockSection{{lines: errLines}},
			width:       width,
		})
		return termLinesNode(rows, width), nil
	}

	orderProgress := func(items []map[string]any) []map[string]any {
		// packages/tui/src/tools/task.ts:1305-1313: finished sorted by durationMs
		// asc (tie-break index), followed by unfinished (pending/running) in dispatch order.
		var finished, unfinished []map[string]any
		for _, pm := range items {
			st := strField(pm, "status")
			if st == "pending" || st == "running" {
				unfinished = append(unfinished, pm)
			} else {
				finished = append(finished, pm)
			}
		}
		slices.SortStableFunc(finished, func(a, b map[string]any) int {
			da, _ := int64Field(a, "durationMs")
			db, _ := int64Field(b, "durationMs")
			if da != db {
				if da < db {
					return -1
				}
				return 1
			}
			ia, _ := intField(a, "index")
			ib, _ := intField(b, "index")
			return ia - ib
		})
		return append(finished, unfinished...)
	}

	var bodyLines []string
	var pendingIDs []string
	if len(results) == 0 && len(progress) > 0 {
		raw := make([]map[string]any, 0, len(progress))
		for _, item := range progress {
			if pm := asMap(item); pm != nil {
				refreshed, stillLive := nvRefreshAsyncProgress(sessionID, pm)
				if stillLive {
					if id := strField(refreshed, "id"); id != "" {
						pendingIDs = append(pendingIDs, id)
					}
				}
				raw = append(raw, refreshed)
			}
		}
		ordered := orderProgress(raw)
		visible := ordered
		if !opts.Expanded && len(ordered) > collapsedAgentLimit {
			hidden := ordered[:len(ordered)-collapsedAgentLimit]
			visible = ordered[len(ordered)-collapsedAgentLimit:]
			bodyLines = append(bodyLines, p.formatHiddenProgress(hidden))
		}
		st := chromeSession(ctx)
		for _, pm := range visible {
			bodyLines = append(bodyLines, p.renderAgentProgress(pm, st, "", opts.Expanded)...)
		}
	} else {
		// Results present: order by durationMs asc, tie-break index (task.ts:1320-1322).
		ordered := make([]map[string]any, 0, len(results))
		resultIDs := make(map[string]bool, len(results))
		for _, raw := range results {
			if rm := asMap(raw); rm != nil {
				ordered = append(ordered, rm)
				if id := strField(rm, "id"); id != "" {
					resultIDs[id] = true
				}
			}
		}
		slices.SortStableFunc(ordered, func(a, b map[string]any) int {
			da, _ := int64Field(a, "durationMs")
			db, _ := int64Field(b, "durationMs")
			if da != db {
				if da < db {
					return -1
				}
				return 1
			}
			ia, _ := intField(a, "index")
			ib, _ := intField(b, "index")
			return ia - ib
		})
		visible := selectCollapsedTaskResults(ordered, opts.Expanded)
		st := chromeSession(ctx)
		for _, rm := range visible {
			bodyLines = append(bodyLines, p.renderAgentResult(rm, st, "", opts.Expanded)...)
		}
		if len(visible) < len(ordered) {
			hint := p.formatExpandHint(false, true)
			more := formatMoreItems(len(ordered)-len(visible), "agent")
			if hint != "" {
				more += " " + hint
			}
			bodyLines = append(bodyLines, p.fg("dim", more))
		}

		// Mixed blocking+async call: keep remaining progress rows not in results (task.ts:1525-1549).
		if len(progress) > 0 {
			var supp []map[string]any
			for _, raw := range progress {
				if pm := asMap(raw); pm != nil && !resultIDs[strField(pm, "id")] {
					refreshed, stillLive := nvRefreshAsyncProgress(sessionID, pm)
					if stillLive {
						if id := strField(refreshed, "id"); id != "" {
							pendingIDs = append(pendingIDs, id)
						}
					}
					supp = append(supp, refreshed)
				}
			}
			for _, pm := range orderProgress(supp) {
				bodyLines = append(bodyLines, p.renderAgentProgress(pm, st, "", opts.Expanded)...)
			}
		}
		// Summary row in brackets (task.ts:1551-1565).
		var summaryParts []string
		if abortedCount > 0 {
			summaryParts = append(summaryParts, p.fg("error", fmt.Sprintf("%d aborted", abortedCount)))
		}
		if successCount > 0 {
			summaryParts = append(summaryParts, p.fg("success", fmt.Sprintf("%d succeeded", successCount)))
		}
		if mergeFailCount > 0 {
			summaryParts = append(summaryParts, p.fg("warning", fmt.Sprintf("%d merge failed", mergeFailCount)))
		}
		if failCount > 0 {
			summaryParts = append(summaryParts, p.fg("error", fmt.Sprintf("%d failed", failCount)))
		}
		if reqTotal > 0 {
			summaryParts = append(summaryParts, p.fg("dim", fmt.Sprintf("%s req", formatNumber(reqTotal))))
		}
		if durMs, ok := int64Field(details, "totalDurationMs"); ok && durMs > 0 {
			summaryParts = append(summaryParts, p.fg("dim", formatDuration(durMs)))
		}
		if len(summaryParts) > 0 {
			bodyLines = append(bodyLines,
				p.fg("dim", unicodeSymbols.formatBracketLeft)+
					strings.Join(summaryParts, p.fg("dim", unicodeSymbols.sepDot))+
					p.fg("dim", unicodeSymbols.formatBracketRight),
			)
		}
	}
	if len(pendingIDs) > 0 {
		nvTrackAsyncTaskCard(ctx, rc, sessionID, pendingIDs)
	} else {
		nvUntrackAsyncTaskCard(rc)
	}

	sections := append([]blockSection(nil), prefixSections...)
	if len(bodyLines) > 0 {
		sections = append(sections, blockSection{separator: len(sections) > 0, lines: bodyLines})
	}
	rows := p.renderOutputBlock(blockOpts{
		header:      header,
		state:       taskState,
		borderColor: borderColor,
		sections:    sections,
		width:       width,
	})
	return termLinesNode(rows, width), nil
}

func selectCollapsedTaskResults(ordered []map[string]any, expanded bool) []map[string]any {
	if expanded || len(ordered) <= collapsedAgentLimit {
		if len(ordered) > maxExpandedOutputLines/4 {
			return ordered[:maxExpandedOutputLines/4]
		}
		return ordered
	}
	picked := make(map[int]bool, collapsedAgentLimit)
	// Problem rows claim slots first (task.ts:1357-1369).
	for i, rm := range ordered {
		if len(picked) >= collapsedAgentLimit {
			break
		}
		aborted, _ := boolField(rm, "aborted")
		exitCode, _ := intField(rm, "exitCode")
		if aborted || exitCode != 0 || strField(rm, "error") != "" {
			picked[i] = true
		}
	}
	for i := range ordered {
		if len(picked) >= collapsedAgentLimit {
			break
		}
		picked[i] = true
	}
	out := make([]map[string]any, 0, len(picked))
	for i, rm := range ordered {
		if picked[i] {
			out = append(out, rm)
		}
	}
	return out
}

func (p palette) formatHiddenProgress(hidden []map[string]any) string {
	done, running, pending, failed, aborted := 0, 0, 0, 0, 0
	for _, h := range hidden {
		switch strField(h, "status") {
		case "completed", "done":
			done++
		case "running":
			running++
		case "pending":
			pending++
		case "failed":
			failed++
		case "aborted":
			aborted++
		}
	}
	var parts []string
	if done > 0 {
		parts = append(parts, p.fg("success", fmt.Sprintf("%d done", done)))
	}
	if running > 0 {
		parts = append(parts, p.fg("accent", fmt.Sprintf("%d running", running)))
	}
	if pending > 0 {
		parts = append(parts, p.fg("dim", fmt.Sprintf("%d pending", pending)))
	}
	if failed > 0 {
		parts = append(parts, p.fg("error", fmt.Sprintf("%d failed", failed)))
	}
	if aborted > 0 {
		parts = append(parts, p.fg("error", fmt.Sprintf("%d aborted", aborted)))
	}
	bd := ""
	if len(parts) > 0 {
		bd = p.fg("dim", " (") + strings.Join(parts, p.fg("dim", unicodeSymbols.sepDot)) + p.fg("dim", ")")
	}
	hint := p.formatExpandHint(false, true)
	line := p.fg("dim", formatMoreItems(len(hidden), "agent")) + bd
	if hint != "" {
		line += " " + hint
	}
	return line
}

// renderAgentProgress mirrors packages/tui/src/tools/task.ts:733-827 and
// packages/tui/src/tools/agent-tree.ts:72-143.
func (p palette) agentColored(st *chromeState, agentID, text string) string {
	if text == "" {
		return ""
	}
	if agentID == "" || strings.EqualFold(agentID, "Main") {
		return p.fg("accent", text)
	}
	slot := 0
	if st != nil {
		slot = st.agentSlot(agentID)
	}
	if p.titanium && len(titaniumSubagentPalette) > 0 {
		c := titaniumSubagentPalette[slot%len(titaniumSubagentPalette)]
		return wrapForeground(rgbFgAnsi(c, p.mode), text)
	}
	return p.fg("success", text)
}

func (p palette) renderAgentProgress(pm map[string]any, st *chromeState, indent string, expanded bool) []string {
	status := strField(pm, "status")
	iconName := "running"
	nameColor := "accent"
	statusBadge := ""
	switch status {
	case "completed", "done":
		iconName = "success"
		nameColor = "text"
	case "failed":
		iconName = "error"
		statusBadge = " " + p.formatBadge("failed", "error")
	case "aborted":
		iconName = "aborted"
		statusBadge = " " + p.formatBadge("aborted", "error")
	case "pending":
		iconName = "pending"
	}
	rawID := strField(pm, "id")
	id := formatTaskID(rawID)
	desc := flattenLine(strField(pm, "description"))
	var title string
	if rawID != "" {
		nameText := id
		if desc != "" {
			nameText = p.bold(id)
		}
		name := p.agentColored(st, rawID, nameText)
		if desc != "" {
			title = name + p.fg(nameColor, ":") + " " + p.fg(nameColor, desc)
		} else {
			title = name
		}
	} else if desc != "" {
		title = p.fg(nameColor, p.bold(id)+": "+desc)
	} else {
		title = p.fg(nameColor, id)
	}
	line := indent + p.statusIcon(iconName) + " " + title + p.taskAgentTypeBadge(strField(pm, "agent")) + statusBadge
	if status == "running" && desc == "" {
		if preview := previewLine(strField(pm, "assignment", "task"), 40); preview != "" {
			line += " " + p.fg("muted", preview)
		}
	}
	if status == "running" {
		if pct, ok := intField(pm, "completionPercent"); ok {
			line += unicodeSymbols.sepDot + p.fg("accent", fmt.Sprintf("%d%%", pct))
		}
	}
	if status == "running" || status == "completed" || status == "done" {
		line += p.formatAgentStatRun(pm)
	}
	continuePrefix := indent + "  "
	lines := []string{line}
	if expanded {
		if taskText := strings.TrimSpace(sanitizeDisplayText(strField(pm, "assignment", "task"))); taskText != "" {
			lines = append(lines, continuePrefix+p.fg("dim", "Task"))
			tLines := sanitizeDisplayLines(taskText)
			capN := min(len(tLines), 20)
			for _, tl := range tLines[:capN] {
				lines = append(lines, continuePrefix+"  "+p.fg("dim", tl))
			}
			if len(tLines) > capN {
				lines = append(lines, continuePrefix+"  "+p.fg("dim", formatMoreItems(len(tLines)-capN, "line")))
			}
		}
	}

	if status == "running" {
		if stage := flattenLine(strField(pm, "stage")); stage != "" {
			lines = append(lines, continuePrefix+p.fg("muted", stage))
		}
		if curTool := flattenLine(strField(pm, "currentTool")); curTool != "" {
			toolLine := continuePrefix + unicodeSymbols.treeHook + " " + p.fg("muted", curTool)
			detail := flattenLine(strField(pm, "currentToolIntent"))
			if detail == "" {
				detail = flattenLine(strField(pm, "currentToolArgs"))
			}
			if detail != "" {
				toolLine += ": " + p.fg("dim", previewLine(detail, 40))
			}
			lines = append(lines, toolLine)
		} else if recentTools := asSlice(pm["recentTools"]); len(recentTools) > 0 {
			if rtm := asMap(recentTools[0]); rtm != nil {
				toolLine := continuePrefix + unicodeSymbols.treeHook + " " + p.fg("dim", flattenLine(strField(rtm, "tool")))
				detail := flattenLine(strField(rtm, "intent"))
				if detail == "" {
					detail = flattenLine(strField(rtm, "args"))
				}
				if detail != "" {
					toolLine += ": " + p.fg("dim", previewLine(detail, 40))
				}
				lines = append(lines, toolLine)
			}
		}
	}
	// Upstream packages/tui/src/tools/task.ts:943: expanded recentOutput only renders while running.
	if expanded && status == "running" {
		if recentOut := stringSliceField(pm, "recentOutput"); len(recentOut) > 0 {
			start := max(0, len(recentOut)-previewCollapsedLines)
			for _, o := range recentOut[start:] {
				lines = append(lines, continuePrefix+p.fg("dim", previewLine(o, truncateContent)))
			}
		}
	}
	return lines
}

// renderAgentResult mirrors packages/tui/src/tools/task.ts:1060-1275 and
// packages/tui/src/tools/agent-tree.ts:27-45.
func (p palette) renderAgentResult(rm map[string]any, st *chromeState, indent string, expanded bool) []string {
	aborted, _ := boolField(rm, "aborted")
	exitCode, _ := intField(rm, "exitCode")
	errStr := strField(rm, "error")
	rawID := strField(rm, "id")
	id := formatTaskID(rawID)

	iconName := "success"
	badgeLbl, badgeCol := "done", "success"
	titleColor := "text"
	switch {
	case aborted:
		iconName, badgeLbl, badgeCol, titleColor = "aborted", "aborted", "error", "accent"
	case exitCode != 0:
		iconName, badgeLbl, badgeCol, titleColor = "error", "failed", "error", "accent"
	case errStr != "":
		iconName, badgeLbl, badgeCol, titleColor = "warning", "merge failed", "warning", "accent"
	}

	desc := flattenLine(strField(rm, "description"))
	var title string
	nameText := id
	if desc != "" {
		nameText = p.bold(id)
	}
	name := p.agentColored(st, rawID, nameText)
	if desc != "" {
		title = name + p.fg(titleColor, ": "+desc)
	} else {
		title = name
	}

	line := indent + p.statusIcon(iconName) + " " + title +
		p.taskAgentTypeBadge(strField(rm, "agent")) +
		" " + p.formatBadge(badgeLbl, badgeCol) +
		p.formatAgentStatRun(rm)
	durMs, _ := int64Field(rm, "durationMs")
	line += unicodeSymbols.sepDot + p.fg("dim", formatDuration(durMs))
	if trunc, _ := boolField(rm, "truncated"); trunc {
		line += " " + p.fg("warning", "[truncated]")
	}
	continuePrefix := indent + "  "
	lines := []string{line}

	if expanded {
		if taskText := strings.TrimSpace(sanitizeDisplayText(strField(rm, "assignment", "task"))); taskText != "" {
			lines = append(lines, continuePrefix+p.fg("dim", "Task"))
			tLines := sanitizeDisplayLines(taskText)
			capN := min(len(tLines), 20)
			for _, tl := range tLines[:capN] {
				lines = append(lines, continuePrefix+"  "+p.fg("dim", tl))
			}
			if len(tLines) > capN {
				lines = append(lines, continuePrefix+"  "+p.fg("dim", formatMoreItems(len(tLines)-capN, "line")))
			}
		}
	}

	if aborted {
		if reason := strField(rm, "abortReason"); reason != "" {
			lines = append(lines, continuePrefix+p.statusIcon("aborted")+" "+p.fg("dim", clipCollapsed(flattenLine(reason), 80, expanded)))
		}
	} else if errStr != "" {
		lines = append(lines, continuePrefix+p.fg("error", flattenLine(errStr)))
	}

	// packages/tui/src/tools/task.ts:1258-1260: renderOutputSection(..., 3, 12)
	// shows 3 lines under `Output` when collapsed and 12 lines when expanded.
	if out := strField(rm, "output"); out != "" {
		lines = append(lines, p.renderTaskOutputSection(out, continuePrefix, expanded, 3, 12)...)
	}

	return lines
}

// ═══════════════════════════════════════════════════════════════════════════
// eval tool
// upstream: packages/tui/src/tools/eval.ts
// ═══════════════════════════════════════════════════════════════════════════

func evalCellsFromArgs(args map[string]any) []map[string]any {
	if rawCells := asSlice(args["cells"]); len(rawCells) > 0 {
		var out []map[string]any
		for _, c := range rawCells {
			if cm := asMap(c); cm != nil {
				out = append(out, cm)
			}
		}
		return out
	}
	if code := strField(args, "code"); code != "" {
		return []map[string]any{{
			"code":     code,
			"language": strField(args, "language"),
			"title":    strField(args, "title"),
		}}
	}
	return nil
}

func normalizeEvalLang(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "js", "javascript", "bun":
		return "javascript"
	case "py", "python", "":
		return "python"
	default:
		return lang
	}
}

func termEvalCall(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	cells := evalCellsFromArgs(args)
	if len(cells) == 0 {
		return termWrappedTextNode(p.fg("accent", ">>>")+" …", width), nil
	}
	var allRows []string
	for i, cell := range cells {
		lang := normalizeEvalLang(strField(cell, "language"))
		rows := p.renderCodeCellLines(codeCellOpts{
			code:         strField(cell, "code"),
			language:     lang,
			showLanguage: true,
			index:        i,
			total:        len(cells),
			title:        strField(cell, "title"),
			status:       "pending",
			codeTail:     true,
			codeMaxLines: previewWindowFallback,
			width:        width,
		})
		allRows = append(allRows, rows...)
		if i < len(cells)-1 {
			allRows = append(allRows, "")
		}
	}
	return termLinesNode(allRows, width), nil
}

func termEvalResult(ctx sdk.Context, res sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, rc sdk.ToolRenderContext, width int) (kit.Node, error) {
	p := newPalette(ctx)
	details := asMap(res.Details)
	defaultLang := normalizeEvalLang(strField(details, "language"))

	var trailing []string
	if notice := strField(details, "notice"); notice != "" {
		trailing = append(trailing, p.fg("dim", p.wrapBrackets(notice)))
	}
	if asyncObj := asMap(details["async"]); strField(asyncObj, "state") == "running" {
		if jid := strField(asyncObj, "jobId"); jid != "" {
			trailing = append(trailing, p.fg("dim", p.wrapBrackets("Backgrounded: "+jid)))
		}
	}
	if meta := asMap(details["meta"]); meta != nil {
		if w := p.formatMetaTruncation(meta); w != "" {
			trailing = append(trailing, w)
		}
	}

	rawCells := asSlice(details["cells"])
	if len(rawCells) > 0 {
		var allRows []string
		innerW := outputBlockContentWidth(width, nil, nil)
		for i, raw := range rawCells {
			cell := asMap(raw)
			lang := strField(cell, "language")
			if lang == "" {
				lang = defaultLang
			} else {
				lang = normalizeEvalLang(lang)
			}
			status := strField(cell, "status")
			if status == "" {
				if rc.IsError {
					status = "error"
				} else if opts.IsPartial {
					status = "running"
				} else {
					status = "complete"
				}
			}
			var durPtr *int64
			if d, ok := int64Field(cell, "durationMs"); ok {
				durPtr = &d
			}

			outLines := p.formatEvalCellOutput(cell, status == "error", opts.Expanded, innerW)
			statusEvents := asSlice(cell["statusEvents"])
			elided, _ := intField(cell, "statusEventsElided")
			statusLines := p.formatEvalStatusEvents(statusEvents, opts.Expanded, elided)
			if len(statusLines) > 0 {
				if len(outLines) > 0 {
					outLines = append(outLines, p.fg("dim", "Status"))
				}
				outLines = append(outLines, statusLines...)
			}

			rows := p.renderCodeCellLines(codeCellOpts{
				code:           strField(cell, "code"),
				language:       lang,
				showLanguage:   true,
				index:          i,
				total:          len(rawCells),
				title:          strField(cell, "title"),
				status:         status,
				durationMs:     durPtr,
				output:         strings.Join(outLines, "\n"),
				outputMaxLines: max(1, len(outLines)),
				codeTail:       true,
				codeMaxLines:   previewWindowFallback,
				expanded:       opts.Expanded,
				width:          width,
			})
			allRows = append(allRows, rows...)
			if i < len(rawCells)-1 {
				allRows = append(allRows, "")
			}
		}
		allRows = append(allRows, trailing...)
		return termLinesNode(allRows, width), nil
	}

	// Fallback single-cell output when details.cells is absent.
	outText := strings.TrimRight(stripOutputNotice(resultText(res), asMap(details["meta"])), "\r\n")
	status := "complete"
	if rc.IsError {
		status = "error"
	} else if opts.IsPartial {
		status = "running"
	}
	code := strField(rc.Args, "code")
	title := strField(rc.Args, "title")
	lang := normalizeEvalLang(strField(rc.Args, "language"))
	if lang == "" {
		lang = defaultLang
	}
	cellFake := map[string]any{"output": outText}
	innerW := outputBlockContentWidth(width, nil, nil)
	outLines := p.formatEvalCellOutput(cellFake, status == "error", opts.Expanded, innerW)
	rows := p.renderCodeCellLines(codeCellOpts{
		code:           code,
		language:       lang,
		showLanguage:   true,
		title:          title,
		status:         status,
		output:         strings.Join(outLines, "\n"),
		outputMaxLines: max(1, len(outLines)),
		codeTail:       true,
		codeMaxLines:   previewWindowFallback,
		expanded:       opts.Expanded,
		width:          width,
	})
	rows = append(rows, trailing...)
	return termLinesNode(rows, width), nil
}

// formatEvalCellOutput mirrors packages/tui/src/tools/eval.ts:634-709 & 1080-1090:
// error output uses "error" color, collapsed output puts `… N more lines ⟦Ctrl+O: Expand⟧`
// at the BOTTOM (not top), and JSON/Markdown views render inside the cell.
func (p palette) formatEvalCellOutput(cell map[string]any, isErr, expanded bool, innerW int) []string {
	output := strField(cell, "output")
	if strings.TrimSpace(output) == "" {
		return nil
	}
	tok := "toolOutput"
	if isErr {
		tok = "error"
	}
	rawLines := sanitizeDisplayLines(output)

	// Check for structured JSON or Markdown views in cell.views.
	if views := asSlice(cell["views"]); len(views) > 0 {
		var rendered []string
		matchedAny := false
		for _, vr := range views {
			vm := asMap(vr)
			viewObj := asMap(vm["view"])
			if viewObj == nil {
				continue
			}
			switch strField(viewObj, "kind") {
			case "json":
				matchedAny = true
				treeLines, trunc := p.renderJSONTree(viewObj["value"], expanded, nil)
				rendered = append(rendered, treeLines...)
				if trunc {
					rendered = append(rendered, p.fg("dim", "…"))
				}
			case "markdown":
				matchedAny = true
				for _, ml := range sanitizeDisplayLines(strField(viewObj, "text")) {
					rendered = append(rendered, wrapAnsi(p.styleMarkdownLine(ml), innerW)...)
				}
			}
		}
		if matchedAny && len(rendered) > 0 {
			rawLines = rendered
			tok = ""
		}
	}

	styleFn := func(l string) string {
		if tok == "" {
			return l
		}
		return p.fg(tok, l)
	}
	shown, hidden := p.formatOutputPaneLines(outputPaneOpts{
		lines:             rawLines,
		expanded:          expanded,
		collapsedMaxLines: min(defaultTerminalLines, previewWindowFallback),
		edge:              "tail",
		visual:            true,
		width:             innerW,
		styleLine:         styleFn,
		hideMarker:        true,
	})
	out := append([]string(nil), shown...)
	if !expanded && hidden > 0 {
		out = append(out, p.fg("dim", fmt.Sprintf("… %d more %s %s", hidden, pluralize("line", hidden), p.formatExpandHint(false, true))))
	}
	return out
}

func (p palette) formatEvalStatusEvents(events []any, expanded bool, elided int) []string {
	if len(events) == 0 && elided == 0 {
		return nil
	}
	const tailLimit = 8
	start := 0
	if !expanded && len(events) > tailLimit {
		start = len(events) - tailLimit
	}
	hidden := start + elided
	var items []string
	if hidden > 0 {
		items = append(items, p.fg("dim", fmt.Sprintf("… %d earlier %s", hidden, pluralize("event", hidden))))
	}
	for _, raw := range events[start:] {
		em := asMap(raw)
		if em == nil {
			continue
		}
		op := strField(em, "op")
		var parts []string
		if op != "" {
			parts = append(parts, p.fg("accent", op))
		}
		for _, k := range sortedMapKeys(em) {
			if k == "op" || k == "preview" || k == "resolvedThinkingLevel" {
				continue
			}
			v := em[k]
			switch val := v.(type) {
			case string:
				if val != "" {
					parts = append(parts, p.fg("muted", k+"=")+previewLine(val, truncateContent))
				}
			case float64, int, bool:
				parts = append(parts, p.fg("muted", k+"=")+fmt.Sprint(val))
			}
		}
		if len(parts) > 0 {
			items = append(items, strings.Join(parts, " "))
		}
	}
	return p.renderTreeLines(items, true, 0, "event")
}

// ═══════════════════════════════════════════════════════════════════════════
// Shared JSON tree & inline args formatter (used by eval, yield, default)
// upstream: packages/tui/src/tools/json-tree.ts
// ═══════════════════════════════════════════════════════════════════════════

const (
	jsonTreeMaxDepthCollapsed = 2
	jsonTreeMaxDepthExpanded  = 6
	jsonTreeMaxLinesCollapsed = 6
	jsonTreeMaxLinesExpanded  = 200
	jsonTreeScalarCollapsed   = 60
	jsonTreeScalarExpanded    = 2000
)

func formatJSONScalar(v any, maxLen int) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%g", x)
	case int:
		return fmt.Sprintf("%d", x)
	case int64:
		return fmt.Sprintf("%d", x)
	case string:
		esc := strings.ReplaceAll(strings.ReplaceAll(sanitizeDisplayText(x), "\n", `\n`), "\t", `\t`)
		return `"` + truncateWidth(esc, maxLen) + `"`
	case []any:
		return fmt.Sprintf("[%d items]", len(x))
	case map[string]any:
		return fmt.Sprintf("{%d keys}", len(x))
	default:
		return sanitizeDisplayText(fmt.Sprint(v))
	}
}

// formatArgsInline mirrors packages/tui/src/tools/json-tree.ts:75-136:
// joins `key=value` pairs with `, `, skipping `i` and `__partialJson`,
// budgeting each value so later keys stay visible.
func formatArgsInline(args map[string]any, maxWidth int) string {
	if len(args) == 0 || maxWidth <= 0 {
		return ""
	}
	keys := make([]string, 0, len(args))
	for _, k := range sortedMapKeys(args) {
		if k == "i" || k == "__partialJson" {
			continue
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return ""
	}
	var out string
	usedW := 0
	for i, k := range keys {
		cleanKey := flattenHeader(sanitizeDisplayText(k))
		sep := ""
		sepW := 0
		if usedW > 0 {
			sep = ", "
			sepW = 2
		}
		cur := usedW + sepW
		capW := maxWidth - cur - 1
		if capW <= 0 {
			return out + "…"
		}
		tailReserve := 0
		for j := i + 1; j < len(keys); j++ {
			tailReserve += 2 + tui.VisibleWidth(flattenHeader(sanitizeDisplayText(keys[j]))) + 1 + 4
		}
		pieceBudget := min(capW, maxWidth-cur-tailReserve)
		valBudget := max(1, pieceBudget-tui.VisibleWidth(cleanKey)-3)
		piece := cleanKey + "=" + formatJSONScalar(args[k], valBudget)
		pw := tui.VisibleWidth(piece)
		if pw > pieceBudget {
			return out + sep + truncateWidth(piece, capW)
		}
		out += sep + piece
		usedW = cur + pw
	}
	return out
}

func (p palette) styleJSONScalar(v any, maxLen int) string {
	s := formatJSONScalar(v, maxLen)
	switch v.(type) {
	case string:
		return p.fg("syntaxString", s)
	case float64, int, int64:
		return p.fg("syntaxNumber", s)
	case bool:
		return p.fg("syntaxKeyword", s)
	case nil:
		return p.fg("dim", s)
	default:
		return p.fg("muted", s)
	}
}

const jsonTreeInlineLen = 60

type jsonTreeOpts struct {
	maxDepth               int
	maxLines               int
	maxScalarLen           int
	hiddenRootKeys         map[string]bool
	siblingRoots           bool // false = hooked (`└ •`/`  •`), true = `├─`/`└─`
	escapeStringWhitespace bool
}

func isBareJSONKey(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '$' || r == '-') {
			return false
		}
	}
	return true
}

func jsonScalarColor(v any) string {
	switch v.(type) {
	case string:
		return "syntaxString"
	case float64, int, int64:
		return "syntaxNumber"
	case bool, nil:
		return "syntaxKeyword"
	default:
		return "dim"
	}
}

// formatInlineJSONContainer mirrors packages/tui/src/tools/json-tree.ts:169-205:
// renders an array or object as a single styled line if its plain character
// length is <= 60; returns ("", false) for scalars or larger containers.
func (p palette) formatInlineJSONContainer(val any) (string, bool) {
	switch val.(type) {
	case []any, map[string]any:
	default:
		return "", false
	}
	type run struct {
		text  string
		color string
	}
	var runs []run
	totalLen := 0
	push := func(text, color string) bool {
		totalLen += len(text)
		if totalLen > jsonTreeInlineLen {
			return false
		}
		if n := len(runs); n > 0 && runs[n-1].color == color {
			runs[n-1].text += text
		} else {
			runs = append(runs, run{text: text, color: color})
		}
		return true
	}
	var walk func(v any) bool
	walk = func(v any) bool {
		switch x := v.(type) {
		case []any:
			if !push("[", "muted") {
				return false
			}
			for i, elem := range x {
				if i > 0 && !push(", ", "muted") {
					return false
				}
				if !walk(elem) {
					return false
				}
			}
			return push("]", "muted")
		case map[string]any:
			if !push("{", "muted") {
				return false
			}
			keys := sortedMapKeys(x)
			for i, k := range keys {
				if i > 0 && !push(", ", "muted") {
					return false
				}
				name := flattenHeader(sanitizeDisplayText(k))
				keyStr := name
				if !isBareJSONKey(name) {
					if b, err := json.Marshal(name); err == nil {
						keyStr = string(b)
					}
				}
				if !push(keyStr+": ", "muted") {
					return false
				}
				if !walk(x[k]) {
					return false
				}
			}
			return push("}", "muted")
		case string:
			clean := sanitizeDisplayText(x)
			b, err := json.Marshal(clean)
			if err != nil {
				return push(`"`+clean+`"`, "syntaxString")
			}
			return push(string(b), "syntaxString")
		default:
			return push(sanitizeDisplayText(formatJSONScalar(v, jsonTreeInlineLen)), jsonScalarColor(v))
		}
	}
	if !walk(val) {
		return "", false
	}
	var b strings.Builder
	for _, r := range runs {
		b.WriteString(p.fg(r.color, r.text))
	}
	return b.String(), true
}

// renderJSONTree formats a JSON value as omp's bounded JSON tree with hooked
// root connectors (`└ •`/`  •`), inline containers <= 60 chars, `{N}`/`[N]`
// size labels, right-aligned array indices, `…` depth placeholders, and aligned
// multi-line strings (packages/tui/src/tools/json-tree.ts:268-437).
func (p palette) renderJSONTree(val any, expanded bool, hiddenRootKeys map[string]bool) ([]string, bool) {
	opts := jsonTreeOpts{
		maxDepth:               jsonTreeMaxDepthCollapsed,
		maxLines:               jsonTreeMaxLinesCollapsed,
		maxScalarLen:           jsonTreeScalarCollapsed,
		hiddenRootKeys:         hiddenRootKeys,
		escapeStringWhitespace: true,
	}
	if expanded {
		opts.maxDepth = jsonTreeMaxDepthExpanded
		opts.maxLines = jsonTreeMaxLinesExpanded
		opts.maxScalarLen = jsonTreeScalarExpanded
	}
	return p.renderJSONTreeOpts(val, opts)
}

func (p palette) renderJSONTreeOpts(val any, opts jsonTreeOpts) ([]string, bool) {
	type entry struct {
		key      string
		keyColor string
		val      any
	}
	entriesOf := func(v any, hidden map[string]bool) []entry {
		switch x := v.(type) {
		case []any:
			if len(x) == 0 {
				return nil
			}
			idxW := len(fmt.Sprintf("%d", len(x)-1))
			out := make([]entry, len(x))
			for i, elem := range x {
				out[i] = entry{key: fmt.Sprintf("%*d", idxW, i), keyColor: "dim", val: elem}
			}
			return out
		case map[string]any:
			keys := sortedMapKeys(x)
			out := make([]entry, 0, len(keys))
			for _, k := range keys {
				if hidden != nil && hidden[k] {
					continue
				}
				out = append(out, entry{key: k, keyColor: "muted", val: x[k]})
			}
			return out
		}
		return nil
	}

	var lines []string
	truncated := false
	addLine := func(l string) bool {
		if len(lines) >= opts.maxLines {
			truncated = true
			return false
		}
		lines = append(lines, l)
		return true
	}

	hook := unicodeSymbols.treeHook
	hookPad := strings.Repeat(" ", tui.VisibleWidth(hook))
	bullet := unicodeSymbols.formatBullet
	rootGutter := hookPad + " " + strings.Repeat(" ", tui.VisibleWidth(bullet)) + " "

	renderEntryBody := func(key, keyColor string, v any) (body []string, expandChildren bool) {
		if keyColor == "" {
			keyColor = "muted"
		}
		displayKey := flattenHeader(sanitizeDisplayText(key))
		if displayKey == "" {
			switch v.(type) {
			case []any:
				displayKey = "array"
			case map[string]any:
				displayKey = "object"
			default:
				displayKey = "value"
			}
		}
		label := p.fg(keyColor, displayKey)
		if inline, ok := p.formatInlineJSONContainer(v); ok {
			return []string{label + ": " + inline}, false
		}
		switch x := v.(type) {
		case []any:
			return []string{label + " " + p.fg("dim", fmt.Sprintf("[%d]", len(x)))}, len(x) > 0
		case map[string]any:
			return []string{label + " " + p.fg("dim", fmt.Sprintf("{%d}", len(x)))}, len(x) > 0
		case string:
			clean := sanitizeDisplayText(x)
			if strings.Contains(clean, "\n") {
				srcLines := strings.Split(clean, "\n")
				avail := max(1, opts.maxLines-len(lines))
				dispCount := min(len(srcLines), max(1, avail-1))
				head := label + ": "
				indent := strings.Repeat(" ", tui.VisibleWidth(StripANSI(head))+1)
				out := make([]string, 0, dispCount+1)
				out = append(out, head+p.fg("syntaxString", `"`+truncateWidth(replaceTabs(srcLines[0]), opts.maxScalarLen)))
				for _, sl := range srcLines[1:dispCount] {
					out = append(out, indent+p.fg("syntaxString", truncateWidth(replaceTabs(sl), opts.maxScalarLen)))
				}
				if len(srcLines) > dispCount {
					truncated = true
					out = append(out, indent+p.fg("dim", fmt.Sprintf("…(%d more lines)\"", len(srcLines)-dispCount)))
				} else {
					out[len(out)-1] += p.fg("syntaxString", `"`)
				}
				return out, false
			}
			var scalar string
			if opts.escapeStringWhitespace {
				scalar = formatJSONScalar(clean, opts.maxScalarLen)
			} else {
				scalar = `"` + truncateWidth(replaceTabs(clean), opts.maxScalarLen) + `"`
			}
			return []string{label + ": " + p.fg("syntaxString", scalar)}, false
		default:
			return []string{label + ": " + p.fg(jsonScalarColor(v), sanitizeDisplayText(formatJSONScalar(v, opts.maxScalarLen)))}, false
		}
	}

	var walkEntries func(items []entry, depth int, parentPrefix string) bool
	walkEntries = func(items []entry, depth int, parentPrefix string) bool {
		for i, item := range items {
			isLast := i == len(items)-1
			var firstPref, contPref, childParent string
			if depth == 1 && !opts.siblingRoots {
				lead := hookPad
				if i == 0 {
					lead = p.fg("dim", hook)
				}
				firstPref = lead + " " + p.fg("dim", bullet) + " "
				contPref = rootGutter
				childParent = rootGutter
			} else {
				br := unicodeSymbols.treeBranch
				cont := unicodeSymbols.treeVert + "  "
				if isLast {
					br = unicodeSymbols.treeLast
					cont = "   "
				}
				firstPref = parentPrefix + p.fg("dim", br) + " "
				contPref = parentPrefix + p.fg("dim", cont)
				childParent = contPref
			}
			body, hasKids := renderEntryBody(item.key, item.keyColor, item.val)
			for bi, bl := range body {
				pref := contPref
				if bi == 0 {
					pref = firstPref
				}
				if !addLine(pref + bl) {
					return false
				}
			}
			if hasKids {
				if depth >= opts.maxDepth {
					if !addLine(childParent + p.fg("dim", unicodeSymbols.treeLast) + " " + p.fg("dim", "…")) {
						return false
					}
				} else {
					kids := entriesOf(item.val, nil)
					if !walkEntries(kids, depth+1, childParent) {
						return false
					}
				}
			}
		}
		return true
	}

	switch val.(type) {
	case []any, map[string]any:
		roots := entriesOf(val, opts.hiddenRootKeys)
		if len(roots) == 0 {
			if inline, ok := p.formatInlineJSONContainer(val); ok {
				prefix := p.fg("dim", hook) + " " + p.fg("dim", bullet) + " "
				if opts.siblingRoots {
					prefix = p.fg("dim", unicodeSymbols.treeLast) + " "
				}
				kindLbl := "object"
				if _, isArr := val.([]any); isArr {
					kindLbl = "array"
				}
				addLine(prefix + p.fg("muted", kindLbl) + ": " + inline)
			}
			return lines, truncated
		}
		walkEntries(roots, 1, "")
	default:
		walkEntries([]entry{{key: "", keyColor: "muted", val: val}}, 1, "")
	}
	return lines, truncated
}

func isIdentOrDotByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' || b == '.'
}

// hasUnsafeJSONInteger mirrors packages/tui/src/tools/default-renderer.ts:169:
// rejects 16+ digit integer literals that float64 decoding would round.
func hasUnsafeJSONInteger(s string) bool {
	i := 0
	for i < len(s) {
		if s[i] < '0' || s[i] > '9' {
			i++
			continue
		}
		prevOK := i == 0 || !isIdentOrDotByte(s[i-1])
		if i > 0 && s[i-1] == '-' {
			prevOK = i == 1 || !isIdentOrDotByte(s[i-2])
		}
		j := i
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		nextOK := j == len(s) || !isIdentOrDotByte(s[j])
		if prevOK && nextOK && j-i >= 16 {
			return true
		}
		i = j
	}
	return false
}

func tryParseTreeSafeJSON(text string) (any, bool) {
	t := strings.TrimSpace(text)
	if len(t) == 0 || len(t) > 64*1024 {
		return nil, false
	}
	if t[0] != '{' && t[0] != '[' {
		return nil, false
	}
	if hasUnsafeJSONInteger(t) {
		return nil, false
	}
	var parsed any
	if err := json.Unmarshal([]byte(t), &parsed); err != nil {
		return nil, false
	}
	return parsed, true
}
