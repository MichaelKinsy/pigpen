package hashline

import (
	"slices"
	"strings"
)

// FindBlockContextLines scans fullLines lexically for brace/paren/bracket pairs
// and returns the 1-indexed line numbers of any off-window opener or closer
// whose partner is in visible.
// upstream: packages/coding-agent/src/utils/block-context.ts:192
func FindBlockContextLines(fullLines []string, visible map[int]bool) map[int]bool {
	if len(visible) == 0 || len(visible) >= len(fullLines) {
		return nil
	}
	type stackEntry struct {
		opener  byte
		lineNum int
		visible bool
	}
	var stack []stackEntry
	ctx := make(map[int]bool)
	sawBracket := false

	const (
		modeCode = iota
		modeSingle
		modeDouble
		modeTemplate
		modeBlockComment
	)
	mode := modeCode

	for lineIdx, line := range fullLines {
		lineNum := lineIdx + 1
		lineVisible := visible[lineNum]
		escaped := false

		for i := 0; i < len(line); i++ {
			ch := line[i]
			var next byte
			if i+1 < len(line) {
				next = line[i+1]
			}

			if mode == modeBlockComment {
				if ch == '*' && next == '/' {
					mode = modeCode
					i++
				}
				continue
			}

			if mode == modeSingle || mode == modeDouble || mode == modeTemplate {
				if escaped {
					escaped = false
					continue
				}
				if ch == '\\' && mode != modeTemplate {
					escaped = true
					continue
				}
				if (mode == modeSingle && ch == '\'') ||
					(mode == modeDouble && ch == '"') ||
					(mode == modeTemplate && ch == '`') {
					mode = modeCode
				}
				continue
			}

			if ch == '/' && next == '/' {
				break
			}
			if ch == '/' && next == '*' {
				mode = modeBlockComment
				i++
				continue
			}
			if ch == '\'' {
				mode = modeSingle
				escaped = false
				continue
			}
			if ch == '"' {
				mode = modeDouble
				escaped = false
				continue
			}
			if ch == '`' {
				mode = modeTemplate
				escaped = false
				continue
			}

			switch ch {
			case '{', '(', '[':
				sawBracket = true
				stack = append(stack, stackEntry{opener: ch, lineNum: lineNum, visible: lineVisible})
			case '}', ')', ']':
				var want byte
				switch ch {
				case '}':
					want = '{'
				case ')':
					want = '('
				case ']':
					want = '['
				}
				matchIdx := -1
				for j := len(stack) - 1; j >= 0; j-- {
					if stack[j].opener == want {
						matchIdx = j
						break
					}
				}
				if matchIdx >= 0 {
					matched := stack[matchIdx]
					stack = stack[:matchIdx]
					if lineVisible && !matched.visible {
						ctx[matched.lineNum] = true
					}
					if matched.visible && !lineVisible {
						ctx[lineNum] = true
					}
				}
			}
		}

		if mode == modeSingle || mode == modeDouble {
			mode = modeCode
			escaped = false
		}
	}

	if sawBracket {
		for ln := range visible {
			delete(ctx, ln)
		}
	}
	if len(ctx) == 0 {
		return nil
	}
	return ctx
}

// RenderLinesWithBlockContext renders visible 1-indexed lines plus any off-window
// block boundary lines, sorted in source order, with `…` (U+2026) across gaps.
// upstream: packages/coding-agent/src/utils/block-context.ts:329
func RenderLinesWithBlockContext(fullLines []string, visible map[int]bool) string {
	ctx := FindBlockContextLines(fullLines, visible)
	all := make([]int, 0, len(visible)+len(ctx))
	for ln := range visible {
		if ln >= 1 && ln <= len(fullLines) {
			all = append(all, ln)
		}
	}
	for ln := range ctx {
		if ln >= 1 && ln <= len(fullLines) && !visible[ln] {
			all = append(all, ln)
		}
	}
	slices.Sort(all)

	var out []string
	prev := 0
	for _, ln := range all {
		if prev > 0 && ln > prev+1 {
			out = append(out, "…")
		}
		out = append(out, FormatNumberedLine(ln, fullLines[ln-1]))
		prev = ln
	}
	return strings.Join(out, "\n")
}
