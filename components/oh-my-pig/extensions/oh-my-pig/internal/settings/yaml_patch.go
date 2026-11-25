package settings

import (
	"fmt"
	"strings"
)

// rawYAMLLine represents one line of the original YAML text along with its own
// exact line terminator (`"\r\n"`, `"\n"`, `"\r"`, or `""` for a final line with
// no trailing newline) so mixed-EOL files and missing final newlines are
// preserved byte-for-byte on unchanged lines (fixes T5, T7).
type rawYAMLLine struct {
	text string
	eol  string
}

func splitRawYAMLLines(body string) []rawYAMLLine {
	if body == "" {
		return nil
	}
	var out []rawYAMLLine
	start := 0
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c == '\r' {
			if i+1 < len(body) && body[i+1] == '\n' {
				out = append(out, rawYAMLLine{text: body[start:i], eol: "\r\n"})
				i++
				start = i + 1
			} else {
				out = append(out, rawYAMLLine{text: body[start:i], eol: "\r"})
				start = i + 1
			}
		} else if c == '\n' {
			out = append(out, rawYAMLLine{text: body[start:i], eol: "\n"})
			start = i + 1
		}
	}
	if start < len(body) {
		out = append(out, rawYAMLLine{text: body[start:], eol: ""})
	}
	return out
}

func defaultEOLForLines(lines []rawYAMLLine) string {
	for _, l := range lines {
		if l.eol != "" {
			return l.eol
		}
	}
	return "\n"
}

func joinRawYAMLLines(lines []rawYAMLLine, hasBOM bool) string {
	var b strings.Builder
	if hasBOM {
		b.WriteString("\uFEFF")
	}
	for _, l := range lines {
		b.WriteString(l.text)
		b.WriteString(l.eol)
	}
	return b.String()
}

// patchYAMLText updates or inserts a single setting path (`segments`) in raw
// YAML text while preserving existing comments, blank lines, key order, each
// line's own EOL (LF, CRLF, or lone CR), and absence of a trailing newline (N1, T5, T7).
//
// It returns (patchedText, true) when a surgical patch succeeds:
//   - scalar replacement of an existing single-line scalar key (top-level or nested)
//   - appending a new key under an existing parent block (or at top-level),
//     creating any missing nested mapping headers for a newly appended subtree
//
// It returns ("", false) when surgical patching does not apply (e.g. unset,
// replacing a multi-line value or flow mapping/sequence, flow-style parent block,
// or non-scalar value). In that case the caller falls back to MarshalYAMLOrdered.
func patchYAMLText(src string, segments []string, value any, unset bool) (string, bool) {
	if unset || len(segments) == 0 {
		return "", false
	}
	if !isYAMLSimpleScalar(value) {
		return "", false
	}

	hasBOM := strings.HasPrefix(src, "\uFEFF")
	body := strings.TrimPrefix(src, "\uFEFF")
	lines := splitRawYAMLLines(body)
	defEOL := defaultEOLForLines(lines)

	// Check if document is a flow mapping at root (e.g. `{}`) -> cannot surgically patch.
	for _, raw := range lines {
		ind, trimmed := leadingIndentAndCode(raw.text)
		if trimmed == "" || trimmed == "---" || trimmed == "..." {
			continue
		}
		if ind == 0 && (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) {
			return "", false
		}
		break
	}

	type blockScope struct {
		start      int // first line inside this mapping
		end        int // exclusive end line of this mapping
		indent     int // indentation of keys at this level (-1 if unknown/empty)
		parentLine int // line of the parent mapping header (-1 for root)
		parentInd  int // indent of parent header (-1 for root)
	}

	docEnd := len(lines)
	for i, raw := range lines {
		ind, trimmed := leadingIndentAndCode(raw.text)
		if ind == 0 && trimmed == "..." {
			docEnd = i
			break
		}
	}

	rootIndent := -1
	for i := 0; i < docEnd; i++ {
		ind, trimmed := leadingIndentAndCode(lines[i].text)
		if trimmed == "" || trimmed == "---" {
			continue
		}
		rootIndent = ind
		break
	}
	if rootIndent < 0 {
		rootIndent = 0
	}

	cur := blockScope{
		start:      0,
		end:        docEnd,
		indent:     rootIndent,
		parentLine: -1,
		parentInd:  -1,
	}

	matchedDepth := 0
	targetLine := -1

	for depth, seg := range segments {
		foundLine := -1
		foundRest := ""
		foundIndent := -1
		matchCount := 0

		for i := cur.start; i < cur.end; i++ {
			ind, code := leadingIndentAndCode(lines[i].text)
			if code == "" || (cur.parentLine < 0 && code == "---") {
				continue
			}
			if cur.indent < 0 {
				cur.indent = ind
			}
			if ind != cur.indent {
				continue
			}
			if isSequenceItem(code) {
				return "", false
			}
			rawKey, rest, ok := splitMappingLine(code)
			if !ok {
				return "", false
			}
			k, err := parseYAMLKey(rawKey, i+1)
			if err != nil {
				return "", false
			}
			if k == seg {
				matchCount++
				foundLine = i
				foundRest = rest
				foundIndent = ind
			}
		}
		if matchCount > 1 {
			return "", false
		}
		if foundLine < 0 {
			break
		}

		blockEnd := foundLine + 1
		lastChildLine := foundLine
		for j := foundLine + 1; j < cur.end; j++ {
			ind, code := leadingIndentAndCode(lines[j].text)
			if code == "" {
				continue
			}
			if ind > foundIndent {
				lastChildLine = j
				blockEnd = j + 1
				continue
			}
			break
		}

		if depth == len(segments)-1 {
			if lastChildLine != foundLine {
				return "", false
			}
			if foundRest != "" && (strings.HasPrefix(foundRest, "{") || strings.HasPrefix(foundRest, "[")) {
				return "", false
			}
			targetLine = foundLine
			matchedDepth = len(segments)
			break
		}

		if foundRest != "" {
			return "", false
		}
		childIndent := -1
		for j := foundLine + 1; j < blockEnd; j++ {
			ind, code := leadingIndentAndCode(lines[j].text)
			if code == "" {
				continue
			}
			if isSequenceItem(code) {
				return "", false
			}
			childIndent = ind
			break
		}
		cur = blockScope{
			start:      foundLine + 1,
			end:        blockEnd,
			indent:     childIndent,
			parentLine: foundLine,
			parentInd:  foundIndent,
		}
		matchedDepth = depth + 1
	}

	formattedVal := formatYAMLScalar(value)

	if targetLine >= 0 {
		// Surgical in-place replacement of targetLine, preserving indentation, key
		// spelling, trailing inline comment, and targetLine's exact EOL (including ""
		// when the original file had no final newline, fixes T7).
		patchedText, ok := replaceScalarOnLine(lines[targetLine].text, formattedVal)
		if !ok {
			return "", false
		}
		outLines := make([]rawYAMLLine, len(lines))
		copy(outLines, lines)
		outLines[targetLine].text = patchedText
		return joinRawYAMLLines(outLines, hasBOM), true
	}

	// Appending a new key (either inside an existing parent block or at top level).
	var (
		insertAt int
		newTexts []string
	)
	if matchedDepth > 0 && cur.parentLine >= 0 {
		childIndent := cur.indent
		if childIndent < 0 {
			childIndent = cur.parentInd + 2
		}
		newTexts = buildNestedScalarLines(segments[matchedDepth:], formattedVal, childIndent, 2)
		insertAt = cur.end
	} else {
		step := detectIndentStep(lines[:docEnd])
		newTexts = buildNestedScalarLines(segments, formattedVal, rootIndent, step)
		insertAt = docEnd
	}

	hadFinalNewline := len(lines) == 0 || lines[len(lines)-1].eol != ""
	insertEOL := defEOL
	if insertAt > 0 && lines[insertAt-1].eol != "" {
		insertEOL = lines[insertAt-1].eol
	}

	outLines := make([]rawYAMLLine, 0, len(lines)+len(newTexts))
	outLines = append(outLines, lines[:insertAt]...)
	// If the line immediately before insertAt lacked a trailing EOL, give it one so
	// the newly inserted line begins on its own line.
	if insertAt > 0 && outLines[insertAt-1].eol == "" {
		outLines[insertAt-1].eol = insertEOL
	}
	for i, txt := range newTexts {
		eol := insertEOL
		// If appending at EOF of a non-empty file that had no trailing newline, keep
		// that convention on the newly appended final line (T7).
		if insertAt == len(lines) && !hadFinalNewline && i == len(newTexts)-1 {
			eol = ""
		}
		outLines = append(outLines, rawYAMLLine{text: txt, eol: eol})
	}
	outLines = append(outLines, lines[insertAt:]...)
	return joinRawYAMLLines(outLines, hasBOM), true
}

// saveWillDropComments reports whether saving `value` at `segments` to `rawText`
// will fall back to full re-serialization and lose existing comments (T7).
func saveWillDropComments(rawText string, segments []string, value any, unset bool) bool {
	if !hasYAMLComments(rawText) {
		return false
	}
	_, ok := patchYAMLText(rawText, segments, value, unset)
	return !ok
}

func isYAMLSimpleScalar(v any) bool {
	if v == nil {
		return true
	}
	switch v.(type) {
	case bool, float64, int, int64, string:
		return true
	default:
		return false
	}
}

func leadingIndentAndCode(raw string) (int, string) {
	ind := 0
	for ind < len(raw) && raw[ind] == ' ' {
		ind++
	}
	code := trimRightASCIISpace(stripYAMLComment(raw[ind:]))
	return ind, code
}

func detectIndentStep(lines []rawYAMLLine) int {
	for _, raw := range lines {
		ind, code := leadingIndentAndCode(raw.text)
		if code != "" && ind > 0 {
			return ind
		}
	}
	return 2
}

func buildNestedScalarLines(segments []string, formattedVal string, baseIndent, step int) []string {
	if step <= 0 {
		step = 2
	}
	out := make([]string, 0, len(segments))
	for i, seg := range segments {
		prefix := strings.Repeat(" ", baseIndent+i*step)
		fk := formatYAMLKey(seg)
		if i == len(segments)-1 {
			out = append(out, fmt.Sprintf("%s%s: %s", prefix, fk, formattedVal))
		} else {
			out = append(out, fmt.Sprintf("%s%s:", prefix, fk))
		}
	}
	return out
}

func replaceScalarOnLine(rawLine, newFormattedVal string) (string, bool) {
	ind := 0
	for ind < len(rawLine) && rawLine[ind] == ' ' {
		ind++
	}
	rest := rawLine[ind:]
	codeNoComment := stripYAMLComment(rest)
	commentSuffix := rest[len(codeNoComment):]
	trailingSpace := ""
	if commentSuffix != "" {
		trimmed := trimRightASCIISpace(codeNoComment)
		trailingSpace = codeNoComment[len(trimmed):]
		if trailingSpace == "" {
			trailingSpace = " "
		}
		codeNoComment = trimmed
	} else {
		codeNoComment = trimRightASCIISpace(codeNoComment)
	}

	colonIdx := findMappingColonIndex(codeNoComment)
	if colonIdx < 0 {
		return "", false
	}
	keyPart := codeNoComment[:colonIdx]
	var b strings.Builder
	b.WriteString(rawLine[:ind])
	b.WriteString(keyPart)
	b.WriteString(": ")
	b.WriteString(newFormattedVal)
	if commentSuffix != "" {
		b.WriteString(trailingSpace)
		b.WriteString(commentSuffix)
	}
	return b.String(), true
}

func findMappingColonIndex(s string) int {
	inSingle := false
	inDouble := false
	escaped := false
	depth := 0
	canOpenQuote := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inDouble {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
			} else if c == '"' {
				inDouble = false
				canOpenQuote = false
			}
			continue
		}
		if inSingle {
			if c == '\'' {
				if i+1 < len(s) && s[i+1] == '\'' {
					i++
					continue
				}
				inSingle = false
				canOpenQuote = false
			}
			continue
		}
		switch c {
		case '"':
			if canOpenQuote {
				inDouble = true
			}
			canOpenQuote = false
		case '\'':
			if canOpenQuote {
				inSingle = true
			}
			canOpenQuote = false
		case '[', '{':
			depth++
			canOpenQuote = true
		case ']', '}':
			if depth > 0 {
				depth--
			}
			canOpenQuote = false
		case ':':
			if depth == 0 && (i+1 == len(s) || s[i+1] == ' ' || s[i+1] == '\t') {
				return i
			}
			canOpenQuote = false
		case ' ', '\t', ',':
			canOpenQuote = depth > 0
		default:
			canOpenQuote = false
		}
	}
	return -1
}
