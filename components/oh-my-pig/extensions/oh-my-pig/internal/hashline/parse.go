package hashline

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseInput parses a multi-section hashline patch input into Sections.
// upstream: crates/pi-edit/src/modes/hashline/input.rs, tokenizer.rs, parser.rs
func ParseInput(input string) ([]Section, error) {
	norm := NormalizeToLF(input)
	lines := strings.Split(norm, "\n")

	type rawSection struct {
		path    string
		hash    string
		hasHash bool
		lines   []string
		lineNum int
	}

	var rawSections []rawSection
	var cur *rawSection

	for i, rawLine := range lines {
		lineNum := i + 1
		trimmed := strings.TrimSpace(rawLine)

		// Skip optional envelope markers.
		if trimmed == "*** Begin Patch" || trimmed == "*** End Patch" {
			continue
		}

		// File section header: [PATH#TAG] or [PATH]
		if path, hash, hasHash, ok := parseSectionHeader(trimmed); ok {
			rawSections = append(rawSections, rawSection{
				path:    path,
				hash:    hash,
				hasHash: hasHash,
				lineNum: lineNum,
			})
			cur = &rawSections[len(rawSections)-1]
			continue
		}

		if cur == nil {
			if trimmed == "" {
				continue
			}
			return nil, fmt.Errorf("line %d: expected file section header [PATH#TAG] before %q", lineNum, trimmed)
		}
		cur.lines = append(cur.lines, rawLine)
	}

	if len(rawSections) == 0 {
		return nil, fmt.Errorf("no file sections found; expected [PATH#TAG] header")
	}

	sections := make([]Section, 0, len(rawSections))
	for _, rs := range rawSections {
		sec, err := parseSectionBody(rs.path, rs.hash, rs.hasHash, rs.lines, rs.lineNum)
		if err != nil {
			return nil, err
		}
		sections = append(sections, sec)
	}
	return sections, nil
}

// parseSectionHeader parses `[PATH#TAG]` or `[PATH]`.
func parseSectionHeader(trimmed string) (path, hash string, hasHash, ok bool) {
	if len(trimmed) < 3 || trimmed[0] != '[' || trimmed[len(trimmed)-1] != ']' {
		return "", "", false, false
	}
	inner := trimmed[1 : len(trimmed)-1]
	if inner == "" {
		return "", "", false, false
	}
	if idx := strings.LastIndexByte(inner, '#'); idx >= 0 {
		cand := strings.TrimSpace(inner[idx+1:])
		if isFourHex(cand) {
			p := strings.TrimSpace(inner[:idx])
			if p == "" {
				return "", "", false, false
			}
			return p, strings.ToUpper(cand), true, true
		}
	}
	return strings.TrimSpace(inner), "", false, true
}

func isFourHex(s string) bool {
	if len(s) != 4 {
		return false
	}
	for i := range 4 {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func parseSectionBody(path, hash string, hasHash bool, bodyLines []string, headerLine int) (Section, error) {
	sec := Section{
		Path:     path,
		FileHash: hash,
		HasHash:  hasHash,
	}

	var curOp *Op
	flushOp := func() error {
		if curOp == nil {
			return nil
		}
		// Verify body requirements.
		switch curOp.Kind {
		case OpPutBefore, OpPutAfter, OpPutEOF, OpPutAfterBlock:
			if len(curOp.Payload) == 0 {
				return fmt.Errorf("line %d: insertion op requires at least one `+` body line", curOp.LineNum)
			}
		case OpPutRange, OpPutBlock:
			if len(curOp.Payload) == 0 {
				return fmt.Errorf("line %d: replacement op requires at least one `+` body line; use CUT to delete lines", curOp.LineNum)
			}
		}
		sec.Ops = append(sec.Ops, *curOp)
		curOp = nil
		return nil
	}

	for i, raw := range bodyLines {
		lineNum := headerLine + 1 + i
		// Payload lines start with '+'.
		if strings.HasPrefix(raw, "+") {
			if curOp == nil {
				return Section{}, fmt.Errorf("line %d: unexpected payload line outside a `:` operation", lineNum)
			}
			if !opTakesBody(curOp.Kind) {
				return Section{}, fmt.Errorf("line %d: operation does not take a `+` body", lineNum)
			}
			curOp.Payload = append(curOp.Payload, raw[1:])
			continue
		}

		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			// Blank line ends any active op block.
			if err := flushOp(); err != nil {
				return Section{}, err
			}
			continue
		}

		// Close any previous op before parsing the next directive.
		if err := flushOp(); err != nil {
			return Section{}, err
		}

		if trimmed == "REM" {
			if sec.FileOp.Kind != FileOpNone {
				return Section{}, fmt.Errorf("line %d: duplicate file operation", lineNum)
			}
			sec.FileOp = FileOp{Kind: FileOpDelete}
			continue
		}

		if strings.HasPrefix(trimmed, "MV ") || strings.HasPrefix(trimmed, "MV\t") {
			if sec.FileOp.Kind != FileOpNone {
				return Section{}, fmt.Errorf("line %d: duplicate file operation", lineNum)
			}
			dest, err := parseMoveDest(strings.TrimSpace(trimmed[2:]))
			if err != nil {
				return Section{}, fmt.Errorf("line %d: %w", lineNum, err)
			}
			sec.FileOp = FileOp{Kind: FileOpMove, Dest: dest}
			continue
		}

		op, err := parseOpHeader(trimmed, lineNum)
		if err != nil {
			return Section{}, err
		}
		if opTakesBody(op.Kind) {
			opCopy := op
			curOp = &opCopy
		} else {
			sec.Ops = append(sec.Ops, op)
		}
	}

	if err := flushOp(); err != nil {
		return Section{}, err
	}

	return sec, nil
}

func opTakesBody(k OpKind) bool {
	switch k {
	case OpPutRange, OpPutBlock, OpPutBefore, OpPutAfter, OpPutEOF, OpPutAfterBlock:
		return true
	default:
		return false
	}
}

func parseMoveDest(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("MV requires a destination path")
	}
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		unquoted := raw[1 : len(raw)-1]
		if unquoted == "" {
			return "", fmt.Errorf("MV destination path cannot be empty")
		}
		return unquoted, nil
	}
	return raw, nil
}

func parseOpHeader(trimmed string, lineNum int) (Op, error) {
	switch {
	case strings.HasPrefix(trimmed, "PUT ") || strings.HasPrefix(trimmed, "PUT\t"):
		return parsePutHeader(strings.TrimSpace(trimmed[3:]), lineNum)
	case strings.HasPrefix(trimmed, "CUT ") || strings.HasPrefix(trimmed, "CUT\t"):
		return parseCutHeader(strings.TrimSpace(trimmed[3:]), lineNum)
	default:
		return Op{}, fmt.Errorf("line %d: unrecognized hashline operation %q", lineNum, trimmed)
	}
}

func parsePutHeader(rest string, lineNum int) (Op, error) {
	hadColon := strings.HasSuffix(rest, ":")
	if hadColon {
		rest = strings.TrimSpace(rest[:len(rest)-1])
	}

	locator, reg, err := splitLocatorAndRegister(rest, lineNum)
	if err != nil {
		return Op{}, err
	}
	if reg != "" && hadColon {
		return Op{}, fmt.Errorf("line %d: register paste `PUT ... @%s` must not end with `:`", lineNum, reg)
	}

	// Gap locators: <N, >N, >$, >N*
	if strings.HasPrefix(locator, "<") {
		n, err := parsePositiveInt(locator[1:], lineNum)
		if err != nil {
			return Op{}, err
		}
		if hadColon {
			return Op{Kind: OpPutBefore, LineNum: lineNum, Start: n}, nil
		}
		return Op{Kind: OpPasteBefore, LineNum: lineNum, Start: n, Register: reg}, nil
	}
	if strings.HasPrefix(locator, ">") {
		after := locator[1:]
		if after == "$" {
			if hadColon {
				return Op{Kind: OpPutEOF, LineNum: lineNum}, nil
			}
			return Op{Kind: OpPasteEOF, LineNum: lineNum, Register: reg}, nil
		}
		if strings.HasSuffix(after, "*") {
			n, err := parsePositiveInt(after[:len(after)-1], lineNum)
			if err != nil {
				return Op{}, err
			}
			if hadColon {
				return Op{Kind: OpPutAfterBlock, LineNum: lineNum, Start: n}, nil
			}
			return Op{Kind: OpPasteAfterBlock, LineNum: lineNum, Start: n, Register: reg}, nil
		}
		n, err := parsePositiveInt(after, lineNum)
		if err != nil {
			return Op{}, err
		}
		if hadColon {
			return Op{Kind: OpPutAfter, LineNum: lineNum, Start: n}, nil
		}
		return Op{Kind: OpPasteAfter, LineNum: lineNum, Start: n, Register: reg}, nil
	}

	// Block locator: N*
	if strings.HasSuffix(locator, "*") {
		n, err := parsePositiveInt(locator[:len(locator)-1], lineNum)
		if err != nil {
			return Op{}, err
		}
		if hadColon {
			return Op{Kind: OpPutBlock, LineNum: lineNum, Start: n}, nil
		}
		if reg == "" {
			return Op{}, fmt.Errorf("line %d: `PUT %d*` without `:` requires a named register `@name`", lineNum, n)
		}
		return Op{Kind: OpPasteBlock, LineNum: lineNum, Start: n, Register: reg}, nil
	}

	// Range locator: N.=M (or single line N)
	start, end, err := parseRangeLocator(locator, lineNum)
	if err != nil {
		return Op{}, err
	}
	if hadColon {
		return Op{Kind: OpPutRange, LineNum: lineNum, Start: start, End: end}, nil
	}
	if reg == "" {
		return Op{}, fmt.Errorf("line %d: `PUT %s` without `:` requires a named register `@name`", lineNum, locator)
	}
	return Op{Kind: OpPasteRange, LineNum: lineNum, Start: start, End: end, Register: reg}, nil
}

func parseCutHeader(rest string, lineNum int) (Op, error) {
	if strings.HasSuffix(rest, ":") {
		rest = strings.TrimSpace(rest[:len(rest)-1])
	}
	locator, reg, err := splitLocatorAndRegister(rest, lineNum)
	if err != nil {
		return Op{}, err
	}
	if strings.HasSuffix(locator, "*") {
		n, err := parsePositiveInt(locator[:len(locator)-1], lineNum)
		if err != nil {
			return Op{}, err
		}
		return Op{Kind: OpCutBlock, LineNum: lineNum, Start: n, Register: reg}, nil
	}
	start, end, err := parseRangeLocator(locator, lineNum)
	if err != nil {
		return Op{}, err
	}
	return Op{Kind: OpCutRange, LineNum: lineNum, Start: start, End: end, Register: reg}, nil
}

func splitLocatorAndRegister(s string, lineNum int) (locator, reg string, err error) {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return "", "", fmt.Errorf("line %d: missing operation target", lineNum)
	}
	if len(fields) == 1 {
		return fields[0], "", nil
	}
	if len(fields) == 2 && strings.HasPrefix(fields[1], "@") && len(fields[1]) > 1 {
		return fields[0], fields[1][1:], nil
	}
	return "", "", fmt.Errorf("line %d: invalid operation header %q", lineNum, s)
}

func parseRangeLocator(s string, lineNum int) (start, end int, err error) {
	if idx := strings.Index(s, ".="); idx >= 0 {
		a, err := parsePositiveInt(s[:idx], lineNum)
		if err != nil {
			return 0, 0, err
		}
		b, err := parsePositiveInt(s[idx+2:], lineNum)
		if err != nil {
			return 0, 0, err
		}
		if b < a {
			return 0, 0, fmt.Errorf("line %d: invalid range %d.=%d: end must be >= start", lineNum, a, b)
		}
		return a, b, nil
	}
	// Single line shorthand (e.g., CUT 5)
	a, err := parsePositiveInt(s, lineNum)
	if err != nil {
		return 0, 0, err
	}
	return a, a, nil
}

func parsePositiveInt(s string, lineNum int) (int, error) {
	if s == "" || s[0] == '0' {
		return 0, fmt.Errorf("line %d: invalid line number %q (lines are 1-indexed)", lineNum, s)
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("line %d: invalid line number %q", lineNum, s)
	}
	return n, nil
}
