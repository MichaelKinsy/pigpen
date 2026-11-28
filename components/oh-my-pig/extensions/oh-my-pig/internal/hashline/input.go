package hashline

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"unicode"
)

// Top-level parser for hashline file sections.
// upstream: crates/edit/src/modes/hashline/input.rs

var (
	envelopeMarkers = [3]string{BeginPatchMarker, EndPatchMarker, AbortMarker}

	applyPatchPathNoiseRe = regexp.MustCompile(`(?i)^\*{0,3}\s*(?:(?:update|add|delete|move)[^A-Za-z0-9]*(?:file|to)?[^A-Za-z0-9]*:)?\s*\*{0,3}\s*`)
	recoveryTagRe         = regexp.MustCompile(`#([0-9A-Fa-f]{4})\s*$`)
	placeholderTagRe      = regexp.MustCompile(`#[^\s#./\\:\[\]]{0,16}\s*$`)
)

type rawSection struct {
	path        string
	fileHash    string
	hasHash     bool
	diff        string
	interleaved bool
	placeholder bool
}

// PatchSection is one target file section with a lazily memoized parsed body.
// upstream: crates/edit/src/modes/hashline/input.rs:61
type PatchSection struct {
	Path        string
	FileHash    string
	HasHash     bool
	Diff        string
	interleaved bool

	parseOnce sync.Once
	parsed    Parsed
	parseErr  string
}

// NewPatchSection constructs a section from its authored path, optional tag, and body.
// upstream: crates/edit/src/modes/hashline/input.rs:70
func NewPatchSection(path, fileHash string, hasHash bool, diff string) *PatchSection {
	return &PatchSection{Path: path, FileHash: fileHash, HasHash: hasHash, Diff: diff}
}

func sectionFromRaw(raw rawSection) *PatchSection {
	return &PatchSection{
		Path:        raw.path,
		FileHash:    raw.fileHash,
		HasHash:     raw.hasHash,
		Diff:        raw.diff,
		interleaved: raw.interleaved,
	}
}

// Parse parses and memoizes this section's body.
// upstream: crates/edit/src/modes/hashline/input.rs:85
func (s *PatchSection) Parse() (*Parsed, *EditError) {
	s.parseOnce.Do(func() {
		parsed, err := ParsePatch(s.Diff)
		if err != nil {
			s.parseErr = err.Error()
			return
		}
		if s.interleaved && HasClipboardEdit(parsed.Edits) {
			s.parseErr = ClipboardInterleavedSections
			return
		}
		if parsed.FileOp != nil && parsed.FileOp.Kind == FileOpMove {
			parsed.FileOp.Dest = normalizeHashlinePath(parsed.FileOp.Dest, "")
		}
		s.parsed = parsed
	})
	if s.parseErr != "" {
		return nil, ParseError(s.parseErr)
	}
	return &s.parsed, nil
}

// Edits returns the parsed low-level edits.
// upstream: crates/edit/src/modes/hashline/input.rs:103
func (s *PatchSection) Edits() ([]Edit, *EditError) {
	p, err := s.Parse()
	if err != nil {
		return nil, err
	}
	return p.Edits, nil
}

// FileOp returns the optional whole-file operation.
// upstream: crates/edit/src/modes/hashline/input.rs:108
func (s *PatchSection) FileOp() (*FileOp, *EditError) {
	p, err := s.Parse()
	if err != nil {
		return nil, err
	}
	return p.FileOp, nil
}

// Warnings returns parser recovery warnings.
// upstream: crates/edit/src/modes/hashline/input.rs:113
func (s *PatchSection) Warnings() ([]string, *EditError) {
	p, err := s.Parse()
	if err != nil {
		return nil, err
	}
	return p.Warnings, nil
}

// HasAnchorScopedEdit reports whether any edit targets concrete source content.
// upstream: crates/edit/src/modes/hashline/input.rs:118
func (s *PatchSection) HasAnchorScopedEdit() (bool, *EditError) {
	edits, err := s.Edits()
	if err != nil {
		return false, err
	}
	return hasAnchorScopedEdit(edits), nil
}

// CollectAnchorLines collects concrete anchor lines in ascending order without duplicates.
// upstream: crates/edit/src/modes/hashline/input.rs:129
func (s *PatchSection) CollectAnchorLines() ([]uint32, *EditError) {
	edits, err := s.Edits()
	if err != nil {
		return nil, err
	}
	return collectAnchorLines(edits), nil
}

func collectAnchorLines(edits []Edit) []uint32 {
	var lines []uint32
	for _, edit := range edits {
		switch edit.Kind {
		case EditDelete, EditBlock:
			lines = append(lines, edit.Anchor.Line)
		case EditCut:
			for ln := edit.Range.Start.Line; ; ln++ {
				lines = append(lines, ln)
				if ln == edit.Range.End.Line {
					break
				}
			}
		case EditPaste:
			if edit.PasteAt.Kind == PasteTargetSpan {
				for ln := edit.PasteAt.Range.Start.Line; ; ln++ {
					lines = append(lines, ln)
					if ln == edit.PasteAt.Range.End.Line {
						break
					}
				}
			} else if edit.PasteAt.Cursor.Kind == CursorBeforeAnchor || edit.PasteAt.Cursor.Kind == CursorAfterAnchor {
				lines = append(lines, edit.PasteAt.Cursor.Anchor.Line)
			}
		case EditInsert:
			if edit.Cursor.Kind == CursorBeforeAnchor || edit.Cursor.Kind == CursorAfterAnchor {
				lines = append(lines, edit.Cursor.Anchor.Line)
			}
		}
	}
	slices.Sort(lines)
	lines = slices.Compact(lines)
	return lines
}

// WithPath rebinds this section to another path.
// upstream: crates/edit/src/modes/hashline/input.rs:151
func (s *PatchSection) WithPath(path string) *PatchSection {
	next := &PatchSection{
		Path:        path,
		FileHash:    s.FileHash,
		HasHash:     s.HasHash,
		Diff:        s.Diff,
		interleaved: s.interleaved,
	}
	return next
}

// Patch is a parsed multi-file hashline patch.
// upstream: crates/edit/src/modes/hashline/input.rs:168
type Patch struct {
	Sections []*PatchSection
}

// SplitOptions configures section splitting and headerless streaming recovery.
// upstream: crates/edit/src/modes/hashline/input.rs:173
type SplitOptions struct {
	Cwd  string
	Path string
}

// ParsePatchInput splits and coalesces authored file sections.
// upstream: crates/edit/src/modes/hashline/input.rs:181
func ParsePatchInput(input string, options SplitOptions) (*Patch, *EditError) {
	raw, err := splitRawSections(input, options)
	if err != nil {
		return nil, err
	}
	merged, err := mergeSamePathSections(raw)
	if err != nil {
		return nil, err
	}
	sections := make([]*PatchSection, len(merged))
	for i, r := range merged {
		sections[i] = sectionFromRaw(r)
	}
	return &Patch{Sections: sections}, nil
}

// ParseSingleSection parses exactly the first section, rejecting an empty patch.
// upstream: crates/edit/src/modes/hashline/input.rs:187
func ParseSingleSection(input string, options SplitOptions) (*PatchSection, *EditError) {
	patch, err := ParsePatchInput(input, options)
	if err != nil {
		return nil, err
	}
	if len(patch.Sections) == 0 {
		return nil, ParseError("Patch input did not produce any sections.")
	}
	return patch.Sections[0], nil
}

// ContainsRecognizableHashlineOperations reports whether any input row is an op header.
// upstream: crates/edit/src/modes/hashline/input.rs:197
func ContainsRecognizableHashlineOperations(input string) bool {
	tok := NewTokenizer()
	for line := range strings.SplitSeq(input, "\n") {
		if tok.IsOp(strings.TrimSuffix(line, "\r")) {
			return true
		}
	}
	return false
}

// FirstHeaderPath returns the path of the first section header, for header-only inputs.
// upstream: crates/edit/src/modes/hashline/input.rs:206
func FirstHeaderPath(input string, options SplitOptions) (string, bool) {
	input = strings.TrimPrefix(input, BOM)
	for line := range strings.SplitSeq(input, "\n") {
		clean := unbracketEnvelopeMarkers(strings.TrimRightFunc(strings.TrimSuffix(line, "\r"), unicode.IsSpace))
		hdr, err := parseHeaderLine(clean, options.Cwd)
		if err == nil && hdr != nil {
			return hdr.path, true
		}
	}
	return "", false
}

func unquoteStr(p string) string {
	if len(p) >= 2 && ((p[0] == '"' && p[len(p)-1] == '"') || (p[0] == '\'' && p[len(p)-1] == '\'')) {
		return p[1 : len(p)-1]
	}
	return p
}

func normalizeHashlinePath(raw, cwd string) string {
	cleaned := applyPatchPathNoiseRe.ReplaceAllString(unquoteStr(strings.TrimSpace(raw)), "")
	if cwd == "" {
		return cleaned
	}
	if !hasPathRoot(cleaned) {
		return cleaned
	}
	normPath := lexicalNormalizePosixOrOS(cleaned)
	normCwd := lexicalNormalizePosixOrOS(cwd)
	if rel, ok := stripLexicalPrefix(normPath, normCwd); ok {
		if rel == "" {
			return "."
		}
		return rel
	}
	return cleaned
}

func hasPathRoot(p string) bool {
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "\\") {
		return true
	}
	return len(p) >= 3 && ((p[0] >= 'a' && p[0] <= 'z') || (p[0] >= 'A' && p[0] <= 'Z')) && p[1] == ':' && (p[2] == '/' || p[2] == '\\')
}

func lexicalNormalizePosixOrOS(p string) string {
	slash := strings.ReplaceAll(p, "\\", "/")
	clean := filepath.ToSlash(filepath.Clean(slash))
	return clean
}

func stripLexicalPrefix(path, prefix string) (string, bool) {
	if path == prefix {
		return "", true
	}
	p := strings.TrimSuffix(prefix, "/") + "/"
	if rest, ok := strings.CutPrefix(path, p); ok {
		return rest, true
	}
	return "", false
}

// parseHeaderLine matches crates/edit/src/modes/hashline/input.rs:260.
func parseHeaderLine(line, cwd string) (*rawSection, *EditError) {
	trimmed := unbracketEnvelopeMarkers(strings.TrimRightFunc(line, unicode.IsSpace))
	if !strings.HasPrefix(trimmed, "[") {
		return nil, nil
	}
	tok := NewTokenizer()
	token := tok.Tokenize(trimmed, 0)
	if token.Kind == TokenHeader {
		path := normalizeHashlinePath(token.Path, cwd)
		if path == "" {
			return nil, ParseError(`Input header "[]" is empty; provide a file path.`)
		}
		return &rawSection{
			path:     path,
			fileHash: token.FileHash,
			hasHash:  token.HasHash,
		}, nil
	}
	if recovered := recoverHeader(trimmed, cwd); recovered != nil {
		return recovered, nil
	}
	return nil, ParseError(fmt.Sprintf(
		"Input header must be [PATH#TAG] with a %d-hex content-hash tag; got %s.",
		HLFileHashLength, JSONQuote(trimmed),
	))
}

// recoverHeader matches crates/edit/src/modes/hashline/input.rs:288.
func recoverHeader(line, cwd string) *rawSection {
	if len(line) < 2 || line[0] != '[' || line[len(line)-1] != ']' {
		return nil
	}
	body := strings.TrimSpace(line[1 : len(line)-1])
	body = applyPatchPathNoiseRe.ReplaceAllString(body, "")
	if body == "" {
		return nil
	}
	var pathText string
	var fileHash string
	hasHash := false
	placeholder := false
	if loc := recoveryTagRe.FindStringSubmatchIndex(body); loc != nil {
		pathText = body[:loc[0]]
		fileHash = strings.ToUpper(body[loc[2]:loc[3]])
		hasHash = true
	} else if loc := placeholderTagRe.FindStringIndex(body); loc != nil {
		pathText = body[:loc[0]]
		placeholder = true
	} else {
		pathText = strings.TrimRightFunc(body, unicode.IsSpace)
	}
	if (!hasHash && strings.Contains(pathText, "#")) || HeaderPathHasOrphanBracket(pathText) {
		return nil
	}
	path := normalizeHashlinePath(pathText, cwd)
	if path == "" {
		return nil
	}
	return &rawSection{
		path:        path,
		fileHash:    fileHash,
		hasHash:     hasHash,
		placeholder: placeholder,
	}
}

// unbracketEnvelopeMarkers matches crates/edit/src/modes/hashline/input.rs:332.
func unbracketEnvelopeMarkers(line string) string {
	rest := line
	for {
		inner, ok := strings.CutPrefix(rest, "[")
		if !ok {
			return rest
		}
		inner = strings.TrimLeftFunc(inner, unicode.IsSpace)
		matched := ""
		for _, m := range envelopeMarkers {
			if strings.HasPrefix(inner, m) {
				matched = m
				break
			}
		}
		if matched == "" {
			return rest
		}
		tail := strings.TrimLeftFunc(inner[len(matched):], unicode.IsSpace)
		tail = strings.TrimPrefix(tail, "]")
		tail = strings.TrimLeftFunc(tail, unicode.IsSpace)
		if tail == "" {
			return matched
		}
		rest = tail
	}
}

func stripLeadingBlanks(input string) string {
	input = strings.TrimPrefix(input, BOM)
	tok := NewTokenizer()
	lines := strings.Split(input, "\n")
	start := 0
	for start < len(lines) {
		clean := unbracketEnvelopeMarkers(strings.TrimSuffix(lines[start], "\r"))
		if strings.TrimSpace(clean) == "" || tok.Tokenize(clean, 0).Kind == TokenEnvelopeBegin {
			start++
		} else {
			break
		}
	}
	return strings.Join(lines[start:], "\n")
}

func splitRawSections(input string, options SplitOptions) ([]rawSection, *EditError) {
	input, err := normalizeFallback(input, options)
	if err != nil {
		return nil, err
	}
	stripped := stripLeadingBlanks(input)
	rawLines := strings.Split(stripped, "\n")
	lines := make([]string, len(rawLines))
	for i, l := range rawLines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	first := ""
	if len(lines) > 0 {
		first = lines[0]
	}
	firstHdr, err := parseHeaderLine(first, options.Cwd)
	if err != nil {
		return nil, err
	}
	if firstHdr == nil {
		if unifiedHunkRe.MatchString(strings.TrimRightFunc(first, unicode.IsSpace)) {
			return nil, ParseError(
				"unified-diff hunk header (`@@ -N,M +N,M @@`) is not valid in hashline. File sections start with `[path#HASH]`; use `replace`, `delete`, or `insert` ops.",
			)
		}
		preview := first
		if runes := []rune(first); len(runes) > 120 {
			preview = string(runes[:120])
		}
		return nil, ParseError(fmt.Sprintf(
			"input must begin with \"[PATH#HASH]\" on the first non-blank line for anchored edits; got: %s. Example: \"[src/foo.ts#%s]\" then edit ops.",
			JSONQuote(preview), HLFileHashExamples[0],
		))
	}

	tok := NewTokenizer()
	var sections []rawSection
	var current *rawSection
	var body []string
	for _, line := range lines {
		clean := unbracketEnvelopeMarkers(strings.TrimRightFunc(line, unicode.IsSpace))
		token := tok.Tokenize(clean, 0)
		if token.Kind == TokenEnvelopeEnd || token.Kind == TokenAbort {
			break
		}
		if token.Kind == TokenEnvelopeBegin {
			continue
		}
		if strings.HasPrefix(clean, "[") {
			hdr, hdrErr := parseHeaderLine(clean, options.Cwd)
			if hdrErr != nil {
				return nil, hdrErr
			}
			if hdr != nil {
				flushRawSection(&sections, &current, &body)
				current = hdr
				continue
			}
		}
		body = append(body, line)
	}
	flushRawSection(&sections, &current, &body)
	return sections, nil
}

func normalizeFallback(input string, options SplitOptions) (string, *EditError) {
	stripped := strings.TrimPrefix(input, BOM)
	for line := range strings.SplitSeq(stripped, "\n") {
		hdr, err := parseHeaderLine(strings.TrimSuffix(line, "\r"), options.Cwd)
		if err != nil {
			return "", err
		}
		if hdr != nil {
			return input, nil
		}
	}
	if options.Path == "" {
		return input, nil
	}
	if !ContainsRecognizableHashlineOperations(input) {
		return input, nil
	}
	path := normalizeHashlinePath(options.Path, options.Cwd)
	if path == "" {
		return input, nil
	}
	return fmt.Sprintf("[%s]\n%s", path, input), nil
}

func flushRawSection(sections *[]rawSection, current **rawSection, body *[]string) {
	if *current == nil {
		*body = nil
		return
	}
	sec := **current
	*current = nil
	hasContentLine := false
	for _, l := range *body {
		if strings.TrimSpace(l) != "" {
			hasContentLine = true
			break
		}
	}
	if hasContentLine {
		sec.diff = strings.Join(*body, "\n")
		*sections = append(*sections, sec)
	}
	*body = nil
}

// mergeSamePathSections matches crates/edit/src/modes/hashline/input.rs:449.
func mergeSamePathSections(sections []rawSection) ([]rawSection, *EditError) {
	var result []rawSection
	positions := make(map[string]int)
	var previous string
	hasPrevious := false
	for _, section := range sections {
		if idx, ok := positions[section.path]; ok {
			existing := &result[idx]
			if existing.hasHash && section.hasHash && existing.fileHash != section.fileHash {
				return nil, ParseError(fmt.Sprintf(
					"Conflicting hashline snapshot tags for %s: #%s and #%s. Re-read the file and retry with one current header.",
					section.path, existing.fileHash, section.fileHash,
				))
			}
			if !existing.hasHash && section.hasHash {
				existing.fileHash = section.fileHash
				existing.hasHash = true
			}
			if section.placeholder || existing.placeholder {
				existing.placeholder = true
				existing.fileHash = ""
				existing.hasHash = false
			}
			if hasPrevious && previous != section.path {
				existing.interleaved = true
			}
			existing.diff += "\n" + section.diff
			previous = section.path
			hasPrevious = true
			continue
		}
		positions[section.path] = len(result)
		previous = section.path
		hasPrevious = true
		result = append(result, section)
	}
	return result, nil
}
