package hashline

import (
	"path/filepath"
	"strings"
)

// ResolveBlock resolves a 1-indexed anchor line to its enclosing multi-line block span.
// Pure-Go heuristic supporting Markdown headings, brace/bracket/paren blocks, and
// indentation-scoped blocks (Python/YAML/etc.).
// upstream: crates/pi-edit/src/modes/hashline/block.rs (tree-sitter in Rust; heuristic in pure Go)
func ResolveBlock(lines []string, path string, anchorLine int) (BlockSpan, bool) {
	if anchorLine < 1 || anchorLine > len(lines) {
		return BlockSpan{}, false
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".md" || ext == ".markdown" || ext == ".mdx" {
		if span, ok := resolveMarkdownHeadingBlock(lines, anchorLine); ok {
			return span, true
		}
	}
	if ext == ".py" || ext == ".pyi" || ext == ".yaml" || ext == ".yml" {
		if span, ok := resolveIndentBlock(lines, anchorLine); ok {
			return span, true
		}
	}
	if span, ok := resolveBraceBlock(lines, anchorLine); ok {
		return span, true
	}
	if span, ok := resolveIndentBlock(lines, anchorLine); ok {
		return span, true
	}
	return BlockSpan{}, false
}

// resolveMarkdownHeadingBlock resolves a Markdown ATX heading through deeper headings
// until the next heading of the same or higher level (or EOF).
func resolveMarkdownHeadingBlock(lines []string, anchorLine int) (BlockSpan, bool) {
	level := markdownHeadingLevel(lines[anchorLine-1])
	if level == 0 {
		return BlockSpan{}, false
	}
	end := len(lines)
	inFence := false
	for i := anchorLine; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if h := markdownHeadingLevel(lines[i]); h > 0 && h <= level {
			end = i // 1-indexed line before this heading
			break
		}
	}
	// Trim trailing blank lines before the next heading, keeping at least anchorLine.
	for end > anchorLine && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	if end <= anchorLine {
		return BlockSpan{}, false
	}
	return BlockSpan{Start: anchorLine, End: end}, true
}

func markdownHeadingLevel(line string) int {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 {
		return 0
	}
	n := 0
	for n < len(trimmed) && trimmed[n] == '#' {
		n++
	}
	if n == 0 || n > 6 {
		return 0
	}
	if n < len(trimmed) && trimmed[n] != ' ' && trimmed[n] != '\t' {
		return 0
	}
	return n
}

// resolveBraceBlock resolves a block opened by '{', '(', or '[' on anchorLine
// (or on a subsequent header line after decorators/attributes).
func resolveBraceBlock(lines []string, anchorLine int) (BlockSpan, bool) {
	startIdx := anchorLine - 1
	// Allow leading decorators/attributes/doc-comments before the opener line.
	scanIdx := startIdx
	for scanIdx < len(lines) && isDecoratorOrAttr(lines[scanIdx]) {
		scanIdx++
	}
	if scanIdx >= len(lines) {
		return BlockSpan{}, false
	}

	depth := 0
	opened := false
	var openChar, closeChar byte

	for i := scanIdx; i < len(lines); i++ {
		line := lines[i]
		inSingle := false
		inDouble := false
		inBacktick := false
		for j := 0; j < len(line); j++ {
			c := line[j]
			if inSingle {
				if c == '\\' {
					j++
				} else if c == '\'' {
					inSingle = false
				}
				continue
			}
			if inDouble {
				if c == '\\' {
					j++
				} else if c == '"' {
					inDouble = false
				}
				continue
			}
			if inBacktick {
				if c == '`' {
					inBacktick = false
				}
				continue
			}
			if c == '/' && j+1 < len(line) && line[j+1] == '/' {
				break // line comment
			}
			switch c {
			case '\'':
				inSingle = true
			case '"':
				inDouble = true
			case '`':
				inBacktick = true
			case '{', '(', '[':
				if !opened {
					opened = true
					openChar = c
					switch c {
					case '{':
						closeChar = '}'
					case '(':
						closeChar = ')'
					case '[':
						closeChar = ']'
					}
					depth = 1
				} else if c == openChar {
					depth++
				}
			case '}', ')', ']':
				if opened && c == closeChar {
					depth--
					if depth == 0 {
						endLine := i + 1
						if endLine > anchorLine {
							return BlockSpan{Start: anchorLine, End: endLine}, true
						}
						// Closed on the same line (e.g. `func a() {`); keep scanning for an outer multi-line opener.
						opened = false
					}
				}
			}
		}
		if !opened && i > scanIdx+4 {
			return BlockSpan{}, false
		}
	}
	return BlockSpan{}, false
}

func isDecoratorOrAttr(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "@") || strings.HasPrefix(t, "#[") || strings.HasPrefix(t, "///") || strings.HasPrefix(t, "//")
}

// resolveIndentBlock resolves an indentation-scoped block (e.g. Python def/class with optional @decorators).
func resolveIndentBlock(lines []string, anchorLine int) (BlockSpan, bool) {
	startIdx := anchorLine - 1
	trimmed := strings.TrimSpace(lines[startIdx])
	if trimmed == "" {
		return BlockSpan{}, false
	}
	baseIndent := leadingIndent(lines[startIdx])

	// Advance past leading decorators at the same indentation.
	headerIdx := startIdx
	for headerIdx < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[headerIdx]), "@") && leadingIndent(lines[headerIdx]) == baseIndent {
		headerIdx++
	}
	if headerIdx >= len(lines) {
		return BlockSpan{}, false
	}
	headerTrimmed := strings.TrimSpace(lines[headerIdx])
	if !strings.HasSuffix(headerTrimmed, ":") {
		return BlockSpan{}, false
	}

	end := headerIdx + 1 // 1-indexed
	foundBody := false
	for i := headerIdx + 1; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if t == "" {
			continue
		}
		if leadingIndent(lines[i]) > baseIndent {
			foundBody = true
			end = i + 1
		} else {
			break
		}
	}
	if !foundBody || end <= anchorLine {
		return BlockSpan{}, false
	}
	return BlockSpan{Start: anchorLine, End: end}, true
}

func leadingIndent(line string) int {
	n := 0
	for _, r := range line {
		switch r {
		case ' ':
			n++
		case '\t':
			n += 4
		default:
			return n
		}
	}
	return n
}
