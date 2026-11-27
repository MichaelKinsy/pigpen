package ratchet

import (
	_ "embed"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/eval"
)

// upstream: packages/coding-agent/src/slash-commands/builtin-modes.ts:203-222, 691-724
//           packages/coding-agent/src/prompts/ratchet-kickoff.md

//go:embed assets/ratchet-kickoff.md
var ratchetKickoffTemplate string

// requiredTools lists tools the ratchet loop needs: eval hosts `ratchet()`, task runs its analyzer.
// upstream: packages/coding-agent/src/slash-commands/builtin-modes.ts:204
var requiredTools = []string{"eval", "task"}

// PrepareResult holds the outcome of PrepareRatchet.
type PrepareResult struct {
	Kickoff string
	Error   string
}

// RenderKickoff renders `assets/ratchet-kickoff.md` with the user request and active tools
// in a single pass (so template syntax inside `request` is never re-expanded) followed by
// omp's post-render formatter (`packages/utils/src/prompt.ts:230-350, 586-590`).
// upstream: packages/coding-agent/src/slash-commands/builtin-modes.ts:220
// upstream: packages/coding-agent/src/prompts/ratchet-kickoff.md
func RenderKickoff(request string, tools []string) string {
	req := strings.TrimSpace(request)
	rendered := evalKickoffTemplate(ratchetKickoffTemplate, req, tools)
	return strings.TrimSpace(formatPostRender(rendered))
}

// evalKickoffTemplate evaluates `{{#if request}}`, `{{#has tools "..."}}`, and
// `{{request}}` in a single left-to-right pass over the template without
// re-scanning interpolated user request text.
func evalKickoffTemplate(tpl, request string, tools []string) string {
	var b strings.Builder
	i := 0
	for i < len(tpl) {
		openIdx := strings.Index(tpl[i:], "{{")
		if openIdx < 0 {
			b.WriteString(tpl[i:])
			break
		}
		openIdx += i
		b.WriteString(tpl[i:openIdx])

		tagEnd := strings.Index(tpl[openIdx+2:], "}}")
		if tagEnd < 0 {
			b.WriteString(tpl[openIdx:])
			break
		}
		tagEnd += openIdx + 2
		rawTag := tpl[openIdx+2 : tagEnd]

		if strings.HasPrefix(rawTag, "#") {
			header := strings.TrimSpace(rawTag[1:])
			kind := header
			arg := ""
			if sp := strings.IndexByte(header, ' '); sp >= 0 {
				kind = header[:sp]
				arg = strings.TrimSpace(header[sp+1:])
			}
			bodyStart := tagEnd + 2
			elseIdx, closeIdx, closeEnd := findKickoffBlockEnd(tpl, bodyStart, kind)
			if closeIdx < 0 {
				b.WriteString(tpl[openIdx:bodyStart])
				i = bodyStart
				continue
			}
			thenBody := tpl[bodyStart:closeIdx]
			elseBody := ""
			if elseIdx >= 0 {
				thenBody = tpl[bodyStart:elseIdx]
				elseBody = tpl[elseIdx+len("{{else}}") : closeIdx]
			}
			thenBody = stripLeadingNewline(thenBody)
			elseBody = stripLeadingNewline(elseBody)

			if evalKickoffCond(kind, arg, request, tools) {
				b.WriteString(evalKickoffTemplate(thenBody, request, tools))
			} else if elseBody != "" {
				b.WriteString(evalKickoffTemplate(elseBody, request, tools))
			}
			i = closeEnd
			if i < len(tpl) && tpl[i] == '\n' && (openIdx == 0 || tpl[openIdx-1] == '\n') {
				i++
			}
			continue
		}

		// Scalar variable interpolation — written verbatim, never re-scanned.
		if strings.TrimSpace(rawTag) == "request" {
			b.WriteString(request)
		}
		i = tagEnd + 2
	}
	return b.String()
}

func stripLeadingNewline(s string) string {
	if strings.HasPrefix(s, "\r\n") {
		return s[2:]
	}
	if strings.HasPrefix(s, "\n") {
		return s[1:]
	}
	return s
}

func findKickoffBlockEnd(s string, from int, kind string) (elseIdx, closeIdx, closeEnd int) {
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
		if strings.HasPrefix(s[next:], openToken) &&
			next+len(openToken) < len(s) &&
			(s[next+len(openToken)] == ' ' || s[next+len(openToken)] == '}') {
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

func evalKickoffCond(kind, arg, request string, tools []string) bool {
	switch kind {
	case "if":
		return strings.TrimSpace(arg) == "request" && request != ""
	case "has":
		fields := strings.Fields(arg)
		if len(fields) < 2 || fields[0] != "tools" {
			return false
		}
		target := strings.Trim(fields[1], `"'`)
		return slices.Contains(tools, target)
	default:
		return false
	}
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
			// Strip any run of 2+ consecutive blank lines entirely; preserve a single blank.
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

// PrepareRatchet arms `/ratchet`: validates required tools, enables `ratchet.enabled`
// as a session-only override (never persisted), checks that the `ratchet` eval prelude
// is available in this session (rolling back the override if not), and renders the kickoff prompt.
// upstream: packages/coding-agent/src/slash-commands/builtin-modes.ts:210-222
func PrepareRatchet(ctx sdk.Context, request string) PrepareResult {
	return prepareRatchetWith(ctx, request, nil, nil, nil, nil)
}

func prepareRatchetWith(
	ctx sdk.Context,
	request string,
	getTools func() []string,
	getEnabled func() bool,
	setOverride func(bool) error,
	hasPrelude func() bool,
) PrepareResult {
	if getTools == nil {
		getTools = func() []string {
			var tools []string
			func() {
				defer func() { _ = recover() }()
				if active, err := ctx.GetActiveTools(); err == nil {
					tools = active
				}
			}()
			return tools
		}
	}
	if getEnabled == nil {
		getEnabled = func() bool { return ratchetEnabled(ctx) }
	}
	if setOverride == nil {
		setOverride = func(val bool) error {
			if SetSessionOverride == nil {
				return nil
			}
			return SetSessionOverride(ctx, SettingRatchetEnabled, val)
		}
	}
	if hasPrelude == nil {
		hasPrelude = func() bool {
			for _, p := range eval.EnabledPreludes(ctx) {
				if p.Name == "ratchet" {
					return true
				}
			}
			return false
		}
	}

	tools := getTools()
	var missing []string
	for _, reqTool := range requiredTools {
		if !slices.Contains(tools, reqTool) {
			missing = append(missing, reqTool)
		}
	}
	if len(missing) > 0 {
		return PrepareResult{Error: fmt.Sprintf("/ratchet needs the %s tool active.", strings.Join(missing, " and "))}
	}

	previous := getEnabled()
	if !previous {
		if err := setOverride(true); err != nil {
			return PrepareResult{Error: fmt.Sprintf("Failed to enable ratchet for this session: %v", err)}
		}
	}
	if !hasPrelude() {
		if !previous {
			_ = setOverride(previous)
		}
		return PrepareResult{Error: "The ratchet eval prelude is unavailable in this session."}
	}
	return PrepareResult{Kickoff: RenderKickoff(request, tools)}
}

// Register installs the `ratchet` eval prelude (gated by `ratchet.enabled`, default false)
// and registers the `/ratchet` slash command on e. Safe to call multiple times across sessions.
// upstream: packages/coding-agent/src/sdk.ts:2930-2933; packages/coding-agent/src/slash-commands/builtin-modes.ts:691-724
func Register(e *sdk.Extension) {
	eval.RegisterPrelude(PreludeDefinition())
	if e == nil {
		return
	}
	e.RegisterCommand("ratchet", sdk.CommandOptions{
		Description: "Build (or reuse) an eval for an LLM flow, then hillclimb it unattended",
		Handler: func(ctx sdk.Context, args string) error {
			armed := PrepareRatchet(ctx, args)
			if armed.Error != "" {
				safeNotify(ctx, armed.Error, "warning")
				return nil
			}
			// Deliver the kickoff as a hidden message that starts or queues behind any in-flight turn,
			// matching omp's `handleTui` (`builtin-modes.ts:710-722`) and oh-my-pig's `/guided-goal`.
			return ctx.SendMessage("ratchet-kickoff", armed.Kickoff, false, sdk.SendMessageOptions{
				TriggerTurn: new(true),
				DeliverAs:   "followUp",
			})
		},
	})
}

func safeNotify(ctx sdk.Context, msg, level string) {
	var zero sdk.Context
	if ctx == zero {
		return
	}
	func() {
		defer func() { _ = recover() }()
		ctx.Notify(msg, level)
	}()
}
