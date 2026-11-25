package files

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/dirs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

// LineRange is an inclusive 1-indexed line range. EndLine == 0 means open-ended (to EOF).
// upstream: packages/tui/src/tools/line-ranges.ts:7
type LineRange struct {
	StartLine int
	EndLine   int // 0 = open-ended
}

// SelectorKind is the parsed kind of a read path selector.
// upstream: packages/coding-agent/src/tools/read-selector.ts:6
type SelectorKind int

const (
	SelectorNone SelectorKind = iota
	SelectorRaw
	SelectorConflicts
	SelectorLines
	SelectorTail
)

// ParsedSelector is a parsed read path selector.
// upstream: packages/coding-agent/src/tools/read-selector.ts:6
type ParsedSelector struct {
	Kind   SelectorKind
	Ranges []LineRange
	Tail   int
	Raw    bool
}

// IsRaw reports whether verbatim/raw mode was requested (alone or combined with a range/tail).
func (p ParsedSelector) IsRaw() bool {
	return p.Kind == SelectorRaw || p.Raw
}

// IsMultiRange reports whether multiple disjoint line ranges were requested.
func (p ParsedSelector) IsMultiRange() bool {
	return (p.Kind == SelectorLines && len(p.Ranges) > 1) || (p.Kind == SelectorTail && len(p.Ranges) > 0)
}

// ResolveTail pins a :-N tail selector to an absolute range against totalLines.
// Ranges listed with the tail join it in ascending order, merged where they overlap or touch.
// upstream: packages/coding-agent/src/tools/read-selector.ts:45
func (p ParsedSelector) ResolveTail(totalLines int) ParsedSelector {
	if p.Kind != SelectorTail {
		return p
	}
	start := max(1, totalLines-p.Tail+1)
	end := max(start, totalLines)
	tailRange := LineRange{StartLine: start, EndLine: end}
	if len(p.Ranges) == 0 {
		return ParsedSelector{
			Kind:   SelectorLines,
			Ranges: []LineRange{tailRange},
			Raw:    p.Raw,
		}
	}
	combined := make([]LineRange, 0, len(p.Ranges)+1)
	combined = append(combined, p.Ranges...)
	combined = append(combined, tailRange)
	return ParsedSelector{
		Kind:   SelectorLines,
		Ranges: mergeLineRanges(combined),
		Raw:    p.Raw,
	}
}

// SplitPathAndSelPreferringLiteral splits rawPath into (basePath, selector),
// keeping rawPath intact if it exists literally on disk.
// upstream: packages/coding-agent/src/tools/path-utils.ts:261
func SplitPathAndSelPreferringLiteral(rawPath, cwd string) (basePath, sel string) {
	resolved := resolveToCwd(rawPath, cwd)
	if _, err := os.Stat(resolved); err == nil {
		return rawPath, ""
	}
	return SplitPathAndSel(rawPath)
}

// SplitPathAndSel splits a path and trailing selector (e.g. "foo.go:50-80", "foo.go:raw:2-4", "C:\foo.go:10").
func SplitPathAndSel(rawPath string) (basePath, sel string) {
	// Guard against Windows drive prefix like "C:\..." or "C:/...".
	// A one-letter relative path followed by a selector (e.g. "d:1-5", "a:2-3")
	// has no slash after the colon and must still split.
	minColon := 0
	if len(rawPath) >= 3 && isASCIILetter(rawPath[0]) && rawPath[1] == ':' && (rawPath[2] == '\\' || rawPath[2] == '/') {
		minColon = 2
	}

	// Check for compound ":raw:<range>" or ":<range>:raw" first.
	lastColon := strings.LastIndexByte(rawPath, ':')
	if lastColon < minColon {
		return rawPath, ""
	}
	suffix := rawPath[lastColon+1:]
	prefix := rawPath[:lastColon]

	// Check if there's an earlier colon forming a 2-part compound selector with "raw".
	if prevColon := strings.LastIndexByte(prefix, ':'); prevColon >= minColon {
		mid := prefix[prevColon+1:]
		if strings.EqualFold(mid, "raw") || strings.EqualFold(suffix, "raw") {
			cand := mid + ":" + suffix
			if looksLikeSelector(cand) {
				return prefix[:prevColon], cand
			}
		}
	}

	if looksLikeSelector(suffix) {
		return prefix, suffix
	}
	return rawPath, ""
}

func looksLikeSelector(s string) bool {
	if s == "" {
		return false
	}
	if strings.EqualFold(s, "raw") || strings.EqualFold(s, "conflicts") {
		return true
	}
	if strings.Contains(s, ":") {
		parts := strings.Split(s, ":")
		if len(parts) == 2 {
			a, b := parts[0], parts[1]
			if strings.EqualFold(a, "raw") && looksLikeRangeOrTail(b) {
				return true
			}
			if strings.EqualFold(b, "raw") && looksLikeRangeOrTail(a) {
				return true
			}
		}
		return false
	}
	return looksLikeRangeOrTail(s)
}

func looksLikeRangeOrTail(s string) bool {
	if s == "" {
		return false
	}
	// Comma list or single range/tail (`-N`, `1-2,-15`, `L5-10`).
	for _, chunk := range strings.Split(s, ",") {
		if chunk == "" {
			return false
		}
		if chunk[0] == '-' {
			if len(chunk) <= 1 || !allDigits(chunk[1:]) {
				return false
			}
			continue
		}
		c := chunk
		if c[0] == 'L' || c[0] == 'l' {
			c = c[1:]
		}
		if len(c) == 0 || c[0] < '0' || c[0] > '9' {
			return false
		}
	}
	return true
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// ParseSel parses a selector string (without leading ':').
// upstream: packages/coding-agent/src/tools/read-selector.ts:71
func ParseSel(sel string) (ParsedSelector, error) {
	if sel == "" {
		return ParsedSelector{Kind: SelectorNone}, nil
	}

	if strings.Contains(sel, ":") {
		chunks := strings.Split(sel, ":")
		if len(chunks) == 2 {
			a, b := chunks[0], chunks[1]
			aRaw := strings.EqualFold(a, "raw")
			bRaw := strings.EqualFold(b, "raw")
			if aRaw != bRaw {
				rangeChunk := b
				if bRaw {
					rangeChunk = a
				}
				p, ok, err := parseRangeOrTail(rangeChunk, true)
				if err != nil {
					return ParsedSelector{}, err
				}
				if ok {
					return p, nil
				}
			}
		}
		return ParsedSelector{}, invalidSelectorErr(sel)
	}

	if strings.EqualFold(sel, "raw") {
		return ParsedSelector{Kind: SelectorRaw, Raw: true}, nil
	}
	if strings.EqualFold(sel, "conflicts") {
		return ParsedSelector{Kind: SelectorConflicts}, nil
	}
	p, ok, err := parseRangeOrTail(sel, false)
	if err != nil {
		return ParsedSelector{}, err
	}
	if ok {
		return p, nil
	}
	return ParsedSelector{}, invalidSelectorErr(sel)
}

func invalidSelectorErr(sel string) error {
	return fmt.Errorf(
		"Invalid selector ':%s'. Use :N, :N-M, :N+K, :N- (open-ended), :-N (last N lines), a comma-separated list of ranges, :raw, :img for SVG rendering, or a range combined with raw (e.g. :raw:50-100).",
		sel,
	)
}

func parseRangeOrTail(chunk string, raw bool) (ParsedSelector, bool, error) {
	ranges, ok, err := parseLineRanges(chunk)
	if err != nil {
		return ParsedSelector{}, false, err
	}
	if ok {
		return ParsedSelector{Kind: SelectorLines, Ranges: ranges, Raw: raw}, true, nil
	}
	// A list with tails (`1-12,-15`, `-15`) keeps the longest tail.
	// upstream: packages/coding-agent/src/tools/read-selector.ts:75
	parts := strings.Split(chunk, ",")
	count := 0
	var listed []string
	for _, part := range parts {
		if strings.HasPrefix(part, "-") {
			rest := part[1:]
			if !allDigits(rest) {
				return ParsedSelector{}, false, nil
			}
			n, err := strconv.Atoi(rest)
			if err != nil || n < 1 {
				return ParsedSelector{}, false, fmt.Errorf("Tail selector -0 is invalid; use :-N with N >= 1 to read the last N lines.")
			}
			if n > count {
				count = n
			}
		} else {
			listed = append(listed, part)
		}
	}
	if count == 0 {
		return ParsedSelector{}, false, nil
	}
	var listedRanges []LineRange
	if len(listed) > 0 {
		r, ok, err := parseLineRanges(strings.Join(listed, ","))
		if err != nil {
			return ParsedSelector{}, false, err
		}
		if !ok {
			return ParsedSelector{}, false, nil
		}
		listedRanges = r
	}
	return ParsedSelector{Kind: SelectorTail, Tail: count, Ranges: listedRanges, Raw: raw}, true, nil
}

// mergeLineRanges sorts ranges ascending and merges overlapping or adjacent ones.
// upstream: packages/tui/src/tools/line-ranges.ts:57
func mergeLineRanges(parsed []LineRange) []LineRange {
	if len(parsed) == 0 {
		return nil
	}
	out := make([]LineRange, len(parsed))
	copy(out, parsed)
	slices.SortFunc(out, func(a, b LineRange) int {
		return a.StartLine - b.StartLine
	})
	merged := []LineRange{out[0]}
	for _, cur := range out[1:] {
		last := &merged[len(merged)-1]
		if last.EndLine == 0 {
			continue // open-ended absorbs everything after
		}
		if cur.StartLine <= last.EndLine+1 {
			if cur.EndLine == 0 || cur.EndLine > last.EndLine {
				last.EndLine = cur.EndLine
			}
			continue
		}
		merged = append(merged, cur)
	}
	return merged
}

// parseLineRanges parses comma-separated ranges, sorts ascending, and merges overlapping/adjacent ranges.
// upstream: packages/tui/src/tools/line-ranges.ts:57
func parseLineRanges(sel string) ([]LineRange, bool, error) {
	chunks := strings.Split(sel, ",")
	parsed := make([]LineRange, 0, len(chunks))
	for _, ch := range chunks {
		r, ok, err := parseLineRangeChunk(ch)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
		parsed = append(parsed, r)
	}
	if len(parsed) == 0 {
		return nil, false, nil
	}
	return mergeLineRanges(parsed), true, nil
}

func parseLineRangeChunk(chunk string) (LineRange, bool, error) {
	s := chunk
	if strings.HasPrefix(s, "L") || strings.HasPrefix(s, "l") {
		s = s[1:]
	}
	if s == "" || s[0] < '0' || s[0] > '9' {
		return LineRange{}, false, nil
	}
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	start, err := strconv.Atoi(s[:i])
	if err != nil {
		return LineRange{}, false, nil
	}
	if start < 1 {
		return LineRange{}, false, fmt.Errorf("Line selector 0 is invalid; lines are 1-indexed. Use :1.")
	}
	rest := s[i:]
	if rest == "" {
		// v19.1.1: bare `:N` is that single line; `:N-` is open-ended from N onward.
		// upstream: packages/tui/src/tools/line-ranges.ts:39
		return LineRange{StartLine: start, EndLine: start}, true, nil
	}
	var sep string
	switch {
	case strings.HasPrefix(rest, ".."):
		sep = "-"
		rest = rest[2:]
	case rest[0] == '-' || rest[0] == '+':
		sep = rest[:1]
		rest = rest[1:]
	default:
		return LineRange{}, false, nil
	}
	if strings.HasPrefix(rest, "L") || strings.HasPrefix(rest, "l") {
		rest = rest[1:]
	}

	if rest == "" {
		if sep == "+" {
			return LineRange{}, false, fmt.Errorf("Invalid range %d+0: count must be >= 1.", start)
		}
		return LineRange{StartLine: start, EndLine: 0}, true, nil
	}
	if !allDigits(rest) {
		return LineRange{}, false, nil
	}
	rhs, err := strconv.Atoi(rest)
	if err != nil {
		return LineRange{}, false, nil
	}
	if sep == "+" {
		if rhs < 1 {
			return LineRange{}, false, fmt.Errorf("Invalid range %d+%d: count must be >= 1.", start, rhs)
		}
		return LineRange{StartLine: start, EndLine: start + rhs - 1}, true, nil
	}
	if rhs < start {
		return LineRange{}, false, fmt.Errorf("Invalid range %d-%d: end must be >= start.", start, rhs)
	}
	return LineRange{StartLine: start, EndLine: rhs}, true, nil
}

// resolveToCwd resolves filePath relative to cwd, expanding ~ and treating bare "/" as cwd.
// upstream: packages/coding-agent/src/tools/path-utils.ts:320
func resolveToCwd(filePath, cwd string) string {
	expanded := expandPath(filePath)
	if isOnlySlashes(expanded) {
		return cwd
	}
	if filepath.IsAbs(expanded) {
		return filepath.Clean(expanded)
	}
	return filepath.Clean(filepath.Join(cwd, expanded))
}

func isOnlySlashes(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] != '/' {
			return false
		}
	}
	return true
}

// isWinAbsolutePath reports whether s is a Windows drive/UNC/root-relative absolute path.
func isWinAbsolutePath(s string) bool {
	if len(s) >= 2 && isASCIILetter(s[0]) && s[1] == ':' {
		return true
	}
	return strings.HasPrefix(s, `\\`) || strings.HasPrefix(s, `\`)
}

// normalizeAtPrefix strips a leading '@' only before '/', '~', '~/', Windows absolute paths,
// or a registered internal URL, keeping literal names like '@scope/pkg.txt' intact.
// upstream: packages/coding-agent/src/tools/path-utils.ts:75
func normalizeAtPrefix(filePath string) string {
	if !strings.HasPrefix(filePath, "@") {
		return filePath
	}
	withoutAt := filePath[1:]
	if strings.HasPrefix(withoutAt, "/") ||
		withoutAt == "~" ||
		strings.HasPrefix(withoutAt, "~/") ||
		strings.HasPrefix(withoutAt, `~\`) ||
		isWinAbsolutePath(withoutAt) ||
		urls.CanHandle(withoutAt) {
		return withoutAt
	}
	return filePath
}

// stripLeadingColonPrefix strips a stray leading ':' before a POSIX/Windows path shape
// (e.g. `:/abs/path`, `:../rel`, `:./src`, `:C:\repo\file`).
// upstream: packages/coding-agent/src/tools/path-utils.ts:118
func stripLeadingColonPrefix(s string) string {
	if len(s) < 2 || s[0] != ':' {
		return s
	}
	rest := s[1:]
	if rest[0] == '/' || rest[0] == '\\' || rest[0] == '~' ||
		strings.HasPrefix(rest, "./") || strings.HasPrefix(rest, `.\`) ||
		strings.HasPrefix(rest, "../") || strings.HasPrefix(rest, `..\`) ||
		(len(rest) >= 2 && isASCIILetter(rest[0]) && rest[1] == ':') {
		return rest
	}
	return s
}

// expandPath strips surrounding double quotes, stray leading ':', qualified '@', file://, and expands ~.
// upstream: packages/coding-agent/src/tools/path-utils.ts:109
func expandPath(filePath string) string {
	s := strings.TrimSpace(filePath)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	s = stripLeadingColonPrefix(s)
	s = normalizeAtPrefix(s)
	if len(s) >= 7 && strings.EqualFold(s[:7], "file://") {
		s = s[7:]
	}
	if s == "~" || strings.HasPrefix(s, "~/") || strings.HasPrefix(s, `~\`) {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			if s == "~" {
				return home
			}
			return filepath.Join(home, s[2:])
		}
	}
	return s
}

// formatPathRelativeToCwd returns a POSIX-slash path relative to cwd if inside cwd,
// or the absolute path with POSIX slashes if outside cwd.
// Only cwd (the project directory, standardized once by omp at startup) is standardized;
// an explicit or located absolute path outside cwd keeps its own spelling.
// upstream: packages/coding-agent/src/tools/path-utils.ts:512-531, packages/utils/src/dirs.ts:148-158
func formatPathRelativeToCwd(filePath, cwd string) string {
	resolvedCwd, err := filepath.Abs(cwd)
	if err != nil {
		resolvedCwd = filepath.Clean(cwd)
	}
	resolvedCwd = dirs.StandardizeMacOSPath(resolvedCwd)
	expanded := expandPath(filePath)
	var resolvedPath string
	if filepath.IsAbs(expanded) {
		resolvedPath = filepath.Clean(expanded)
	} else {
		resolvedPath = filepath.Clean(filepath.Join(resolvedCwd, expanded))
	}
	// Match against standardized cwd even if caller passed a /private-prefixed path inside cwd.
	cmpPath := dirs.StandardizeMacOSPath(resolvedPath)
	rel, err := filepath.Rel(resolvedCwd, cmpPath)
	if err == nil && rel != "" && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
		return filepath.ToSlash(rel)
	}
	if err == nil && rel == "" {
		return "."
	}
	return filepath.ToSlash(resolvedPath)
}

// unwrapHashlineHeaderPath delegates to the shared hashline.UnwrapHeaderPath so
// approval and write always resolve the exact same target string.
// upstream: packages/coding-agent/src/tools/hashline-header-path.ts:18-35
func unwrapHashlineHeaderPath(raw string) string {
	return hashline.UnwrapHeaderPath(raw)
}
