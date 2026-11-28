package surface

import (
	"fmt"
	"sort"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui"
)

type hermesToolMeta struct {
	emoji string
	verb  string
}

func lookupHermesToolMeta(toolName string) hermesToolMeta {
	switch toolName {
	case "read_file":
		return hermesToolMeta{emoji: "📖", verb: "read"}
	case "write_file":
		return hermesToolMeta{emoji: "✏️", verb: "write"}
	case "patch":
		return hermesToolMeta{emoji: "🔧", verb: "patch"}
	case "search_files":
		return hermesToolMeta{emoji: "🔍", verb: "search"}
	case "terminal":
		return hermesToolMeta{emoji: "💻", verb: "$"}
	case "process_manage":
		return hermesToolMeta{emoji: "⚙️", verb: "proc"}
	case "memory":
		return hermesToolMeta{emoji: "🧠", verb: "memory"}
	case "skills_list", "skill_view", "skill_manage":
		return hermesToolMeta{emoji: "⚡", verb: "skill"}
	case "delegate_task":
		return hermesToolMeta{emoji: "⛓", verb: "delegate"}
	case "web_search", "web_extract":
		return hermesToolMeta{emoji: "🌐", verb: "web"}
	case "execute_code":
		return hermesToolMeta{emoji: "🐍", verb: "exec"}
	case "cronjob_manage":
		return hermesToolMeta{emoji: "⏰", verb: "cron"}
	case "todo_list":
		return hermesToolMeta{emoji: "☑", verb: "todo"}
	case "browser_exec":
		return hermesToolMeta{emoji: "🌍", verb: "browser"}
	case "vision_analyze":
		return hermesToolMeta{emoji: "👁", verb: "vision"}
	case "image_generate":
		return hermesToolMeta{emoji: "🎨", verb: "image"}
	case "text_to_speech":
		return hermesToolMeta{emoji: "🔊", verb: "tts"}
	case "tool_search", "tool_describe", "tool_call":
		return hermesToolMeta{emoji: "🔎", verb: "tools"}
	default:
		return hermesToolMeta{emoji: "☤", verb: toolName}
	}
}

func (s *state) toolRendererResolver(toolName string, next func() *sdk.ToolRenderers) *sdk.ToolRenderers {
	if !isHermesTool(toolName) {
		return next()
	}
	return &sdk.ToolRenderers{
		Shell: sdk.ToolRenderShellSelf,
		Call: func(_ sdk.Context, args map[string]any, render sdk.ToolRenderContext, width int) ([]string, error) {
			return s.renderHermesToolCall(toolName, args, render, width), nil
		},
		Result: func(_ sdk.Context, result sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, render sdk.ToolRenderContext, width int) ([]string, error) {
			return s.renderHermesToolResult(toolName, result, opts, render, width), nil
		},
	}
}

func (s *state) renderHermesToolCall(toolName string, args map[string]any, render sdk.ToolRenderContext, width int) []string {
	sk := s.currentSkin()
	accentHex := sk.Color("ui_accent", "#FFBF00")
	dimHex := sk.Color("banner_dim", "#B8860B")
	textHex := sk.Color("banner_text", "#FFF8DC")

	meta := lookupHermesToolMeta(toolName)
	preview := sanitizeToolDisplayText(smartToolArgPreview(toolName, args))
	pad := toolOutputPad(render.OutputPad)

	tree := colorize(dimHex, "├─ ")
	label := colorize(accentHex, fmt.Sprintf("%s %s", meta.emoji, meta.verb))
	line := pad + tree + label
	if preview != "" {
		line += " " + colorize(textHex, preview)
	}
	if width > 0 {
		line = clampToolRow(line, width)
	}
	return []string{line}
}

func (s *state) renderHermesToolResult(toolName string, result sdk.ToolRenderResult, opts sdk.ToolRenderResultOptions, render sdk.ToolRenderContext, width int) []string {
	sk := s.currentSkin()
	dimHex := sk.Color("banner_dim", "#B8860B")
	okHex := sk.Color("ui_ok", "#4caf50")
	errHex := sk.Color("ui_error", "#ef5350")
	textHex := sk.Color("banner_text", "#FFF8DC")

	pad := toolOutputPad(render.OutputPad)
	prefix := pad + colorize(dimHex, "└─ ")

	if opts.IsPartial {
		line := prefix + colorize(dimHex, "running…")
		if width > 0 {
			line = clampToolRow(line, width)
		}
		return []string{line}
	}

	fullText := sanitizeToolDisplayText(fullResultText(result))
	summary := firstNonEmptyLine(fullText)
	if summary == "" {
		summary = "done"
	}
	var statusGlyph string
	if render.IsError {
		statusGlyph = colorize(errHex, "✗")
	} else {
		statusGlyph = colorize(okHex, "✓")
	}

	dur := ""
	if render.DurationMs != nil && *render.DurationMs > 0 {
		dur = colorize(dimHex, fmt.Sprintf(" (%.1fs)", float64(*render.DurationMs)/1000.0))
	}

	summaryLine := prefix + statusGlyph + " "
	if render.IsError {
		summaryLine += colorize(errHex, summary) + dur
	} else {
		summaryLine += colorize(textHex, summary) + dur
	}
	if width > 0 {
		summaryLine = clampToolRow(summaryLine, width)
	}

	lines := []string{summaryLine}

	diffText := sanitizeToolDisplayText(extractUnifiedDiff(toolName, fullText, result.Details))
	if diffText != "" && (opts.Expanded || toolName == "patch") {
		lines = append(lines, renderGutteredDiff(diffText, pad, dimHex, okHex, errHex, textHex, width, opts.Expanded)...)
	} else if opts.Expanded && fullText != "" {
		rawLines := strings.Split(strings.TrimRight(fullText, "\n"), "\n")
		if len(rawLines) > 1 {
			gutter := pad + colorize(dimHex, "│ ")
			limit := len(rawLines)
			if limit > 40 {
				limit = 40
			}
			for _, rl := range rawLines[1:limit] {
				out := gutter + colorize(dimHex, rl)
				if width > 0 {
					out = clampToolRow(out, width)
				}
				lines = append(lines, out)
			}
			if len(rawLines) > limit {
				more := gutter + colorize(dimHex, fmt.Sprintf("… +%d more lines", len(rawLines)-limit))
				if width > 0 {
					more = clampToolRow(more, width)
				}
				lines = append(lines, more)
			}
		}
	}

	return lines
}

func sanitizeToolDisplayText(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\t':
			b.WriteString("  ")
		case r == '\n':
			b.WriteByte('\n')
		case r < 0x20 || r == 0x7f:
			// Strip/neutralize C0 control characters so host widthx.VisibleWidth agrees.
			continue
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func clampToolRow(line string, width int) string {
	if width <= 0 || tui.VisibleWidth(line) <= width {
		return line
	}
	if width == 1 {
		return tui.TruncateToWidth(line, 1, "", false)
	}
	return tui.TruncateToWidth(line, width, "…", false)
}

func toolOutputPad(n int) string {
	if n <= 0 {
		return ""
	}
	if n > 8 {
		n = 8
	}
	return strings.Repeat(" ", n)
}

func smartToolArgPreview(toolName string, args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	pick := func(keys ...string) string {
		for _, k := range keys {
			if raw, ok := args[k]; ok && raw != nil {
				v := strings.TrimSpace(fmt.Sprintf("%v", raw))
				if v != "" {
					return singleLineClip(v, 72)
				}
			}
		}
		return ""
	}

	switch toolName {
	case "terminal":
		if cmd := pick("command", "cmd"); cmd != "" {
			return cmd
		}
	case "read_file", "write_file", "patch":
		if p := pick("path", "file_path", "file"); p != "" {
			if off, ok := args["offset"]; ok {
				if lim, ok2 := args["limit"]; ok2 {
					return fmt.Sprintf("%s (%v+%v)", p, off, lim)
				}
				return fmt.Sprintf("%s (:%v)", p, off)
			}
			return p
		}
	case "search_files":
		pat := pick("pattern", "query")
		dir := pick("path", "dir")
		if pat != "" && dir != "" {
			return fmt.Sprintf("%q in %s", pat, dir)
		}
		if pat != "" {
			return fmt.Sprintf("%q", pat)
		}
	case "web_search", "web_extract":
		if q := pick("query", "url", "urls"); q != "" {
			return q
		}
	case "process_manage", "memory", "cronjob_manage", "todo_list", "skill_manage", "browser_exec":
		act := pick("action", "command", "op")
		tgt := pick("session_id", "target", "name", "id", "url", "selector")
		if act != "" && tgt != "" {
			return act + " " + tgt
		}
		if act != "" {
			return act
		}
	case "delegate_task":
		if goal := pick("goal", "prompt", "task"); goal != "" {
			return goal
		}
	case "skill_view":
		if name := pick("name", "skill"); name != "" {
			return name
		}
	case "execute_code":
		if code := pick("code", "script"); code != "" {
			return code
		}
	case "tool_search", "tool_describe", "tool_call":
		if q := pick("query", "name", "tool"); q != "" {
			return q
		}
	case "vision_analyze", "image_generate", "text_to_speech":
		if p := pick("prompt", "text", "image_path", "path"); p != "" {
			return p
		}
	}

	return summarizeToolArgs(args)
}

func singleLineClip(s string, maxChars int) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if maxChars > 0 && len(runes) > maxChars {
		if maxChars <= 1 {
			return string(runes[:maxChars])
		}
		return string(runes[:maxChars-1]) + "…"
	}
	return s
}

func fullResultText(result sdk.ToolRenderResult) string {
	var parts []string
	for _, block := range result.Content {
		if t, ok := block["text"].(string); ok && strings.TrimSpace(t) != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n")
}

func firstNonEmptyLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func extractUnifiedDiff(toolName, fullText string, details any) string {
	if m, ok := details.(map[string]any); ok {
		if d, ok := m["diff"].(string); ok && strings.TrimSpace(d) != "" {
			return d
		}
	}
	if looksLikeUnifiedDiff(fullText) {
		return fullText
	}
	if toolName == "patch" && strings.Contains(fullText, "\n+") && strings.Contains(fullText, "\n-") {
		return fullText
	}
	return ""
}

func looksLikeUnifiedDiff(text string) bool {
	if text == "" {
		return false
	}
	hasHunk := strings.Contains(text, "@@ ")
	hasHeaders := strings.Contains(text, "--- ") && strings.Contains(text, "+++ ")
	if hasHunk || hasHeaders {
		return true
	}
	var plusCount, minusCount int
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			plusCount++
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			minusCount++
		}
	}
	return plusCount > 0 && minusCount > 0
}

func renderGutteredDiff(diffText, pad, dimHex, okHex, errHex, textHex string, width int, expanded bool) []string {
	rawLines := strings.Split(strings.TrimRight(diffText, "\n"), "\n")
	maxLines := 16
	if expanded {
		maxLines = 60
	}
	gutter := pad + colorize(dimHex, "│ ")
	out := make([]string, 0, min(len(rawLines), maxLines)+1)
	shown := 0
	for _, dl := range rawLines {
		if shown >= maxLines {
			break
		}
		var colored string
		switch {
		case strings.HasPrefix(dl, "+++") || strings.HasPrefix(dl, "---") || strings.HasPrefix(dl, "@@"):
			colored = colorize(dimHex, dl)
		case strings.HasPrefix(dl, "+"):
			colored = colorize(okHex, dl)
		case strings.HasPrefix(dl, "-"):
			colored = colorize(errHex, dl)
		default:
			colored = colorize(textHex, dl)
		}
		line := gutter + colored
		if width > 0 {
			line = clampToolRow(line, width)
		}
		out = append(out, line)
		shown++
	}
	if len(rawLines) > shown {
		more := gutter + colorize(dimHex, fmt.Sprintf("… +%d more diff lines", len(rawLines)-shown))
		if width > 0 {
			more = clampToolRow(more, width)
		}
		out = append(out, more)
	}
	return out
}

func summarizeToolArgs(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, min(len(keys), 3))
	for _, k := range keys {
		if len(parts) >= 3 {
			break
		}
		v := fmt.Sprintf("%v", args[k])
		v = strings.ReplaceAll(v, "\n", " ")
		if len(v) > 40 {
			v = v[:37] + "..."
		}
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(parts, " ")
}
