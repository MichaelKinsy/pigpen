package modes

import (
	_ "embed"
	"regexp"
	"strings"
	"unicode"
)

// Embedded upstream omp v19.1.1 prompt files.
// 21 templates under packages/coding-agent/src/prompts/ plus handoff-document.md
// from packages/agent/src/compaction/prompts/.
var (
	//go:embed prompts/plan-mode-active.md
	planModeActiveTemplate string
	//go:embed prompts/plan-mode-approved.md
	planModeApprovedTemplate string
	//go:embed prompts/plan-mode-reference.md
	planModeReferenceTemplate string
	//go:embed prompts/plan-mode-compact-instructions.md
	planModeCompactInstructionsTemplate string
	//go:embed prompts/plan-mode-tool-decision-reminder.md
	planModeToolDecisionReminderTemplate string

	//go:embed prompts/goal-mode-active.md
	goalModeActiveTemplate string
	//go:embed prompts/goal-continuation.md
	goalContinuationTemplate string
	//go:embed prompts/goal-budget-limit.md
	goalBudgetLimitTemplate string
	//go:embed prompts/guided-goal-interview.md
	guidedGoalInterviewTemplate string
	//go:embed prompts/goal.md
	goalToolPrompt string

	//go:embed prompts/btw-user.md
	btwUserTemplate string

	//go:embed prompts/ultrathink-notice.md
	ultrathinkNoticeRaw string
	//go:embed prompts/chillax-notice.md
	chillaxNoticeRaw string
	//go:embed prompts/orchestrate-notice.md
	orchestrateNoticeTemplate string
	//go:embed prompts/workflow-notice.md
	workflowNoticeTemplate string
	//go:embed prompts/jevify-notice.md
	jevifyNoticeRaw string

	//go:embed prompts/handoff-document.md
	handoffDocumentTemplate string

	//go:embed prompts/commit-request.md
	commitRequestTemplate string

	//go:embed prompts/prewalk-plan.md
	prewalkPlanPrompt string
	//go:embed prompts/prewalk-continue.md
	prewalkContinuePrompt string
	//go:embed prompts/prewalk-checklist.md
	prewalkChecklistPrompt string
	//go:embed prompts/omfg-user.md
	omfgUserTemplate string
	//go:embed prompts/goal-todo-context.md
	goalTodoContextTemplate string
)

// templateData holds values evaluated by renderTemplate (minimal Handlebars
// subset matching the expressions used in omp's mode and keyword prompt files).
type templateData struct {
	Vars  map[string]string
	Bools map[string]bool
	Lists map[string][]string
	Items map[string][]templateData
}

// renderTemplate evaluates omp's Handlebars constructs in a single pass so
// `{{...}}` placeholders inside substituted values are never re-expanded, then
// applies omp's post-render formatting (`packages/utils/src/prompt.ts:238-350`).
// upstream: packages/utils/src/prompt.ts:586-590
func renderTemplate(tpl string, data templateData) string {
	rendered := evalTemplate(tpl, data)
	return formatPostRender(rendered)
}

// evalTemplate evaluates block helpers (`{{#if}}`, `{{#unless}}`, `{{#ifAll}}`,
// `{{#ifAny}}`, `{{#has}}`) and scalar interpolations (`{{key}}`) in a single
// left-to-right pass over the template without re-scanning inserted text.
func evalTemplate(s string, data templateData) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		openIdx := strings.Index(s[i:], "{{")
		if openIdx < 0 {
			b.WriteString(s[i:])
			break
		}
		openIdx += i
		b.WriteString(s[i:openIdx])

		tagEnd := strings.Index(s[openIdx+2:], "}}")
		if tagEnd < 0 {
			b.WriteString(s[openIdx:])
			break
		}
		tagEnd += openIdx + 2
		rawTag := s[openIdx+2 : tagEnd]

		if strings.HasPrefix(rawTag, "#") {
			header := strings.TrimSpace(rawTag[1:])
			kind := header
			arg := ""
			if sp := strings.IndexByte(header, ' '); sp >= 0 {
				kind = header[:sp]
				arg = strings.TrimSpace(header[sp+1:])
			}
			bodyStart := tagEnd + 2
			elseIdx, closeIdx, closeEnd := findBlockEnd(s, bodyStart, kind)
			if closeIdx < 0 {
				b.WriteString(s[openIdx:bodyStart])
				i = bodyStart
				continue
			}

			thenBody := s[bodyStart:closeIdx]
			elseBody := ""
			if elseIdx >= 0 {
				thenBody = s[bodyStart:elseIdx]
				elseBody = s[elseIdx+len("{{else}}") : closeIdx]
			}
			thenBody = stripLeadingBlockNewline(thenBody)
			elseBody = stripLeadingBlockNewline(elseBody)

			if kind == "each" {
				if items, ok := data.Items[arg]; ok {
					for _, item := range items {
						childVars := make(map[string]string, len(data.Vars)+len(item.Vars))
						for k, v := range data.Vars {
							childVars[k] = v
						}
						for k, v := range item.Vars {
							childVars[k] = v
						}
						childBools := make(map[string]bool, len(data.Bools)+len(item.Bools))
						for k, v := range data.Bools {
							childBools[k] = v
						}
						for k, v := range item.Bools {
							childBools[k] = v
						}
						childLists := make(map[string][]string, len(data.Lists)+len(item.Lists))
						for k, v := range data.Lists {
							childLists[k] = v
						}
						for k, v := range item.Lists {
							childLists[k] = v
						}
						childItems := make(map[string][]templateData, len(data.Items)+len(item.Items))
						for k, v := range data.Items {
							childItems[k] = v
						}
						for k, v := range item.Items {
							childItems[k] = v
						}
						child := templateData{Vars: childVars, Bools: childBools, Lists: childLists, Items: childItems}
						b.WriteString(evalTemplate(thenBody, child))
					}
				} else {
					for _, item := range data.Lists[arg] {
						childVars := make(map[string]string, len(data.Vars)+1)
						for k, v := range data.Vars {
							childVars[k] = v
						}
						childVars["this"] = item
						child := templateData{Vars: childVars, Bools: data.Bools, Lists: data.Lists, Items: data.Items}
						b.WriteString(evalTemplate(thenBody, child))
					}
				}
			} else if evalCondition(kind, arg, data) {
				b.WriteString(evalTemplate(thenBody, data))
			} else if elseBody != "" {
				b.WriteString(evalTemplate(elseBody, data))
			}
			i = closeEnd
			if i < len(s) && s[i] == '\n' && isStandaloneLineBefore(s, openIdx) {
				i++
			}
			continue
		}

		// Scalar variable interpolation `{{key}}` — inserted verbatim, never re-scanned.
		key := strings.TrimSpace(rawTag)
		if val, ok := data.Vars[key]; ok {
			b.WriteString(val)
		}
		i = tagEnd + 2
	}
	return b.String()
}

var (
	tableRowRE = regexp.MustCompile(`^\|.*\|$`)
	tableSepRE = regexp.MustCompile(`^\|[-:\s|]+\|$`)
)

// formatPostRender ports omp's `format(content, { renderPhase: "post-render" })`.
// upstream: packages/utils/src/prompt.ts:230-350
func formatPostRender(content string) string {
	lines := strings.Split(content, "\n")
	result := make([]string, 0, len(lines))
	inCodeBlock := false

	for i := 0; i < len(lines); i++ {
		line := strings.TrimRightFunc(lines[i], unicode.IsSpace)
		indent := lineIndent(line)

		if indent < len(line) && (line[indent] == '`' || line[indent] == '~') && isFenceLine(line, indent) {
			inCodeBlock = !inCodeBlock
			result = append(result, line)
			continue
		}
		if inCodeBlock {
			result = append(result, line)
			continue
		}

		isClosingLine := false
		if indent < len(line) {
			trimmedStart := line[indent:]
			switch trimmedStart[0] {
			case '<':
				if len(trimmedStart) >= 4 && trimmedStart[1] == '/' && isClosingXMLTag(trimmedStart) {
					isClosingLine = true
				}
			case '|':
				if tableSepRE.MatchString(trimmedStart) {
					line = line[:indent] + compactTableSep(trimmedStart)
				} else if tableRowRE.MatchString(trimmedStart) {
					line = line[:indent] + compactTableRow(trimmedStart)
				}
			}
		}

		if indent >= len(line) {
			// Blank line: strip any run of 2+ consecutive blank lines entirely;
			// preserve a single blank line between non-blank lines.
			if i+1 >= len(lines) || strings.TrimSpace(lines[i+1]) == "" {
				for len(result) > 0 && result[len(result)-1] == "" {
					result = result[:len(result)-1]
				}
				j := i + 1
				for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
					j++
				}
				i = j - 1
				continue
			}
			if len(result) == 0 || result[len(result)-1] == "" {
				continue
			}
		}

		if isClosingLine {
			for len(result) > 0 && result[len(result)-1] == "" {
				result = result[:len(result)-1]
			}
		}

		result = append(result, line)
	}

	for len(result) > 0 && result[len(result)-1] == "" {
		result = result[:len(result)-1]
	}
	return strings.Join(result, "\n")
}

func lineIndent(line string) int {
	for i, r := range line {
		if !unicode.IsSpace(r) {
			return i
		}
	}
	return len(line)
}

func isFenceLine(line string, indent int) bool {
	return strings.HasPrefix(line[indent:], "```") || strings.HasPrefix(line[indent:], "~~~")
}

func isClosingXMLTag(s string) bool {
	if len(s) < 4 || s[0] != '<' || s[1] != '/' || s[len(s)-1] != '>' {
		return false
	}
	for j := 2; j < len(s)-1; j++ {
		c := s[j]
		if !((c >= 'a' && c <= 'z') || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func compactTableRow(line string) string {
	cells := strings.Split(line, "|")
	for i, c := range cells {
		cells[i] = strings.TrimSpace(c)
	}
	return strings.Join(cells, "|")
}

func compactTableSep(line string) string {
	cells := strings.Split(line, "|")
	var norm []string
	for _, c := range cells {
		t := strings.TrimSpace(c)
		if t == "" {
			continue
		}
		left := strings.HasPrefix(t, ":")
		right := strings.HasSuffix(t, ":")
		switch {
		case left && right:
			norm = append(norm, ":---:")
		case left:
			norm = append(norm, ":---")
		case right:
			norm = append(norm, "---:")
		default:
			norm = append(norm, "---")
		}
	}
	return "|" + strings.Join(norm, "|") + "|"
}

func isStandaloneLineBefore(s string, pos int) bool {
	if pos == 0 {
		return true
	}
	return s[pos-1] == '\n'
}

func stripLeadingBlockNewline(body string) string {
	if strings.HasPrefix(body, "\r\n") {
		return body[2:]
	}
	if strings.HasPrefix(body, "\n") {
		return body[1:]
	}
	return body
}

func findBlockEnd(s string, from int, kind string) (elseIdx, closeIdx, closeEnd int) {
	openToken := "{{#" + kind
	closeToken := "{{/" + kind + "}}"
	depth := 1
	elseIdx = -1
	pos := from
	for pos < len(s) {
		next := strings.Index(s[pos:], "{{")
		if next < 0 {
			return -1, -1, -1
		}
		next += pos
		if strings.HasPrefix(s[next:], openToken) && (next+len(openToken) < len(s) && (s[next+len(openToken)] == ' ' || s[next+len(openToken)] == '}')) {
			depth++
			pos = next + len(openToken)
			continue
		}
		if strings.HasPrefix(s[next:], closeToken) {
			depth--
			if depth == 0 {
				return elseIdx, next, next + len(closeToken)
			}
			pos = next + len(closeToken)
			continue
		}
		if depth == 1 && elseIdx < 0 && strings.HasPrefix(s[next:], "{{else}}") {
			elseIdx = next
			pos = next + len("{{else}}")
			continue
		}
		pos = next + 2
	}
	return -1, -1, -1
}

func evalCondition(kind, arg string, data templateData) bool {
	switch kind {
	case "if":
		return evalTruthy(arg, data)
	case "unless":
		return !evalTruthy(arg, data)
	case "ifAll":
		fields := strings.Fields(arg)
		for _, f := range fields {
			if !evalTruthy(f, data) {
				return false
			}
		}
		return len(fields) > 0
	case "ifAny":
		rest := arg
		for {
			start := strings.IndexByte(rest, '(')
			if start < 0 {
				break
			}
			end := strings.IndexByte(rest[start:], ')')
			if end < 0 {
				break
			}
			expr := strings.TrimSpace(rest[start+1 : start+end])
			if evalSubexpr(expr, data) {
				return true
			}
			rest = rest[start+end+1:]
		}
		for _, f := range strings.Fields(arg) {
			if strings.HasPrefix(f, "(") || strings.HasSuffix(f, ")") || strings.HasPrefix(f, `"`) {
				continue
			}
			if evalTruthy(f, data) {
				return true
			}
		}
		return false
	case "has":
		return evalHas(arg, data)
	default:
		return false
	}
}

func evalSubexpr(expr string, data templateData) bool {
	if strings.HasPrefix(expr, "includes ") {
		return evalHas(strings.TrimSpace(strings.TrimPrefix(expr, "includes ")), data)
	}
	return evalTruthy(expr, data)
}

func evalHas(arg string, data templateData) bool {
	fields := strings.Fields(arg)
	if len(fields) < 2 {
		return false
	}
	listName := fields[0]
	target := strings.Trim(fields[1], `"'`)
	for _, item := range data.Lists[listName] {
		if item == target {
			return true
		}
	}
	return false
}

func evalTruthy(key string, data templateData) bool {
	if b, ok := data.Bools[key]; ok {
		return b
	}
	if v, ok := data.Vars[key]; ok {
		return v != ""
	}
	if list, ok := data.Lists[key]; ok {
		return len(list) > 0
	}
	if base, ok := strings.CutSuffix(key, ".length"); ok {
		if list, ok := data.Lists[base]; ok {
			return len(list) > 0
		}
	}
	return false
}

// escapeXMLText matches omp's escapeXmlText for safe interpolation of user text
// inside `<objective>` / `<rough-goal>` tags.
func escapeXMLText(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
	)
	return r.Replace(s)
}
