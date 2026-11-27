package autoresearch

import (
	"fmt"
	"strings"
)

// renderHandlebars renders the narrow Handlebars subset used by upstream's
// embedded `.md` prompt templates (`{{var}}`, `{{#if k}}...{{else}}...{{/if}}`,
// `{{#unless k}}...{{/unless}}`, `{{#each list}}...{{/each}}`), then applies
// upstream's post-render blank-line / whitespace normalization (`prompt.format`).
//
// upstream:
//   - packages/utils/src/prompt.ts:230-350 (`formatPrompt`)
//   - packages/coding-agent/src/autoresearch/{prompt,prompt-setup,command-resume,resume-message}.md
func renderHandlebars(tmpl string, data map[string]any) string {
	raw := evalHandlebarsBlock(tmpl, data)
	return formatPostRender(raw)
}

func evalHandlebarsBlock(tmpl string, data map[string]any) string {
	var out strings.Builder
	pos := 0
	for pos < len(tmpl) {
		open := strings.Index(tmpl[pos:], "{{")
		if open < 0 {
			out.WriteString(tmpl[pos:])
			break
		}
		open += pos
		out.WriteString(tmpl[pos:open])
		closeIdx := strings.Index(tmpl[open+2:], "}}")
		if closeIdx < 0 {
			out.WriteString(tmpl[open:])
			break
		}
		closeIdx += open + 2
		tag := strings.TrimSpace(tmpl[open+2 : closeIdx])
		afterTag := closeIdx + 2

		switch {
		case strings.HasPrefix(tag, "#if "):
			key := strings.TrimSpace(tag[len("#if "):])
			// Standalone block tag line strips its trailing newline.
			bodyStart := skipStandaloneNewline(tmpl, open, afterTag, &out)
			trueBody, falseBody, endPos := splitBlock(tmpl, bodyStart, "if")
			if isTruthy(data[key]) {
				out.WriteString(evalHandlebarsBlock(trueBody, data))
			} else if falseBody != "" {
				out.WriteString(evalHandlebarsBlock(falseBody, data))
			}
			pos = endPos

		case strings.HasPrefix(tag, "#unless "):
			key := strings.TrimSpace(tag[len("#unless "):])
			bodyStart := skipStandaloneNewline(tmpl, open, afterTag, &out)
			body, _, endPos := splitBlock(tmpl, bodyStart, "unless")
			if !isTruthy(data[key]) {
				out.WriteString(evalHandlebarsBlock(body, data))
			}
			pos = endPos

		case strings.HasPrefix(tag, "#each "):
			key := strings.TrimSpace(tag[len("#each "):])
			bodyStart := skipStandaloneNewline(tmpl, open, afterTag, &out)
			body, _, endPos := splitBlock(tmpl, bodyStart, "each")
			if items, ok := data[key].([]map[string]any); ok {
				for _, item := range items {
					child := make(map[string]any, len(data)+len(item))
					for k, v := range data {
						child[k] = v
					}
					for k, v := range item {
						child[k] = v
					}
					out.WriteString(evalHandlebarsBlock(body, child))
				}
			}
			pos = endPos

		default:
			if v, ok := data[tag]; ok && v != nil {
				out.WriteString(fmt.Sprint(v))
			}
			pos = afterTag
		}
	}
	return out.String()
}

func isStandaloneTag(tmpl string, open, afterTag int) bool {
	// Check characters between previous newline and open are only spaces/tabs.
	lineStart := open
	for lineStart > 0 && tmpl[lineStart-1] != '\n' {
		lineStart--
	}
	for i := lineStart; i < open; i++ {
		if tmpl[i] != ' ' && tmpl[i] != '\t' {
			return false
		}
	}
	// Check characters afterTag up to newline/EOF are only spaces/tabs/CR.
	i := afterTag
	for i < len(tmpl) && (tmpl[i] == ' ' || tmpl[i] == '\t' || tmpl[i] == '\r') {
		i++
	}
	return i == len(tmpl) || tmpl[i] == '\n'
}

func skipStandaloneNewline(tmpl string, open, afterTag int, out *strings.Builder) int {
	if !isStandaloneTag(tmpl, open, afterTag) {
		return afterTag
	}
	// Trim any spaces written on this standalone line before the tag.
	s := out.String()
	trimmed := strings.TrimRight(s, " \t")
	if len(trimmed) != len(s) && (len(trimmed) == 0 || trimmed[len(trimmed)-1] == '\n') {
		out.Reset()
		out.WriteString(trimmed)
	}
	i := afterTag
	for i < len(tmpl) && (tmpl[i] == ' ' || tmpl[i] == '\t' || tmpl[i] == '\r') {
		i++
	}
	if i < len(tmpl) && tmpl[i] == '\n' {
		i++
	}
	return i
}

// splitBlock finds the matching `{{else}}` (at depth 0, for `if`) and `{{/<kind>}}`.
func splitBlock(tmpl string, start int, kind string) (trueBody, falseBody string, endPos int) {
	depth := 0
	elseStart := -1
	elseEnd := -1
	pos := start
	for pos < len(tmpl) {
		open := strings.Index(tmpl[pos:], "{{")
		if open < 0 {
			return tmpl[start:], "", len(tmpl)
		}
		open += pos
		closeIdx := strings.Index(tmpl[open+2:], "}}")
		if closeIdx < 0 {
			return tmpl[start:], "", len(tmpl)
		}
		closeIdx += open + 2
		tag := strings.TrimSpace(tmpl[open+2 : closeIdx])
		afterTag := closeIdx + 2

		switch {
		case strings.HasPrefix(tag, "#"+kind+" "):
			depth++
		case tag == "/"+kind:
			if depth == 0 {
				bodyEnd := open
				nextPos := afterTag
				if isStandaloneTag(tmpl, open, afterTag) {
					for bodyEnd > start && (tmpl[bodyEnd-1] == ' ' || tmpl[bodyEnd-1] == '\t') {
						bodyEnd--
					}
					for nextPos < len(tmpl) && (tmpl[nextPos] == ' ' || tmpl[nextPos] == '\t' || tmpl[nextPos] == '\r') {
						nextPos++
					}
					if nextPos < len(tmpl) && tmpl[nextPos] == '\n' {
						nextPos++
					}
				}
				if elseStart >= 0 {
					return tmpl[start:elseStart], tmpl[elseEnd:bodyEnd], nextPos
				}
				return tmpl[start:bodyEnd], "", nextPos
			}
			depth--
		case tag == "else" && kind == "if" && depth == 0 && elseStart < 0:
			elseStart = open
			elseEnd = afterTag
			if isStandaloneTag(tmpl, open, afterTag) {
				for elseStart > start && (tmpl[elseStart-1] == ' ' || tmpl[elseStart-1] == '\t') {
					elseStart--
				}
				for elseEnd < len(tmpl) && (tmpl[elseEnd] == ' ' || tmpl[elseEnd] == '\t' || tmpl[elseEnd] == '\r') {
					elseEnd++
				}
				if elseEnd < len(tmpl) && tmpl[elseEnd] == '\n' {
					elseEnd++
				}
			}
		}
		pos = afterTag
	}
	return tmpl[start:], "", len(tmpl)
}

func isTruthy(v any) bool {
	if v == nil {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x != ""
	case int:
		return x != 0
	case int64:
		return x != 0
	case float64:
		return x != 0
	case []map[string]any:
		return len(x) > 0
	default:
		return true
	}
}

// formatPostRender reproduces omp's `prompt.format(content, { renderPhase: "post-render" })`:
// trims trailing whitespace per line (preserving code fences), collapses 2+ consecutive
// blank lines (stripping the blank run, matching internal/prompt/format.go), and trims
// leading/trailing blank lines.
// upstream: packages/utils/src/prompt.ts:230-350
func formatPostRender(content string) string {
	lines := strings.Split(content, "\n")
	result := make([]string, 0, len(lines))
	inCodeBlock := false

	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], " \t\r")
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inCodeBlock = !inCodeBlock
			result = append(result, line)
			continue
		}
		if inCodeBlock {
			result = append(result, line)
			continue
		}
		if trimmed == "" {
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
		result = append(result, line)
	}
	for len(result) > 0 && result[len(result)-1] == "" {
		result = result[:len(result)-1]
	}
	return strings.Join(result, "\n")
}
