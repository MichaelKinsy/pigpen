package search

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/dirs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

// lineRange is an inclusive 1-based line range [start, end] (end==0 means unbounded).
type lineRange struct {
	start int
	end   int
}

func (r lineRange) contains(line int) bool {
	if line < r.start {
		return false
	}
	if r.end > 0 && line > r.end {
		return false
	}
	return true
}

func lineInRanges(line int, ranges []lineRange) bool {
	if len(ranges) == 0 {
		return true
	}
	for _, r := range ranges {
		if r.contains(line) {
			return true
		}
	}
	return false
}

// normalizePathLikeInput trims whitespace and matching outer double quotes.
// upstream: packages/coding-agent/src/tools/path-utils.ts:540-542,588-590
func normalizePathLikeInput(input string) string {
	s := strings.TrimSpace(input)
	if len(s) > 1 && strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) {
		s = s[1 : len(s)-1]
	}
	return s
}

// expandPath handles @-prefix shorthand, stray leading ':', file:// URLs, and ~ expansion.
// upstream: packages/coding-agent/src/tools/path-utils.ts:75-123
func expandPath(filePath string) string {
	s := filePath
	if strings.HasPrefix(s, "@") {
		withoutAt := s[1:]
		if strings.HasPrefix(withoutAt, "/") ||
			withoutAt == "~" ||
			strings.HasPrefix(withoutAt, "~/") ||
			filepath.IsAbs(withoutAt) ||
			isRegisteredURL(withoutAt) {
			s = withoutAt
		}
	}
	if strings.HasPrefix(strings.ToLower(s), "file://") {
		rest := s[len("file://"):]
		if strings.HasPrefix(rest, "localhost/") {
			rest = rest[len("localhost"):]
		}
		if rest != "" {
			s = rest
		}
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

// toPathList parses a raw path argument (string or JSON-encoded string array) into raw entries.
// Individual delimited entries are expanded separately by expandDelimitedPathEntries.
// upstream: packages/tui/src/render/render-utils.ts:1313-1316
func toPathList(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
		var arr []string
		if err := json.Unmarshal([]byte(trimmed), &arr); err == nil {
			var out []string
			for _, s := range arr {
				s = strings.TrimSpace(s)
				if s != "" {
					out = append(out, s)
				}
			}
			return out
		}
	}
	return []string{trimmed}
}

// splitTopLevelSemicolon splits entry on unescaped top-level `;` outside `{...}` braces.
func splitTopLevelSemicolon(entry string) []string {
	var parts []string
	var cur strings.Builder
	braceDepth := 0
	for i := 0; i < len(entry); i++ {
		c := entry[i]
		if c == '\\' && i+1 < len(entry) {
			next := entry[i+1]
			if next == ';' || next == ',' || next == ' ' {
				cur.WriteByte(c)
				cur.WriteByte(next)
				i++
				continue
			}
		}
		switch c {
		case '{':
			braceDepth++
			cur.WriteByte(c)
		case '}':
			if braceDepth > 0 {
				braceDepth--
			}
			cur.WriteByte(c)
		case ';':
			if braceDepth == 0 {
				if p := strings.TrimSpace(cur.String()); p != "" {
					parts = append(parts, p)
				}
				cur.Reset()
				continue
			}
			cur.WriteByte(c)
		default:
			cur.WriteByte(c)
		}
	}
	if p := strings.TrimSpace(cur.String()); p != "" {
		parts = append(parts, p)
	}
	return parts
}

// probeLiteralPathExists checks whether the exact filesystem path exists via Lstat.
// Returns "exists", "missing", or "unknown".
// upstream: packages/coding-agent/src/tools/path-utils.ts:234-244
func probeLiteralPathExists(filePath, cwd string) string {
	resolved := resolvePath(filePath, cwd)
	_, err := os.Lstat(resolved)
	if err == nil {
		return "exists"
	}
	if os.IsNotExist(err) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.ENAMETOOLONG) {
		return "missing"
	}
	return "unknown"
}

// isExclusionEntry reports whether an entry is a `!glob` exclusion rather than a
// real file whose name starts with `!`.
// upstream: packages/coding-agent/src/tools/grep.ts:482-485
func isExclusionEntry(entry, cwd string) bool {
	if !strings.HasPrefix(entry, "!") || len(entry) <= 1 {
		return false
	}
	if strings.Contains(entry, "://") {
		return false
	}
	peeled, _, _ := splitHostPathAndSelectorPreferLiteral(entry, cwd)
	return probeLiteralPathExists(peeled, cwd) == "missing"
}

func isExclusionPartSyntax(part string) bool {
	return len(part) > 1 && strings.HasPrefix(part, "!") && !strings.Contains(part, "://")
}

// expandDelimitedPathEntries splits `;`-delimited path entries while preserving
// literal files containing `;` and single internal URLs containing `;`.
// upstream: packages/coding-agent/src/tools/path-utils.ts:756-889
func expandDelimitedPathEntries(entries []string, cwd string, urlfs *searchURLFS, allowExclusions bool) []string {
	var out []string
	for _, raw := range entries {
		norm := normalizePathLikeInput(raw)
		if norm == "" {
			continue
		}
		split := splitDelimitedPathEntry(norm, cwd, urlfs, allowExclusions)
		if len(split) > 0 {
			out = append(out, split...)
		} else {
			out = append(out, norm)
		}
	}
	return out
}

func splitDelimitedPathEntry(entry, cwd string, urlfs *searchURLFS, allowExclusions bool) []string {
	if !strings.Contains(entry, ";") {
		return nil
	}
	parts := splitTopLevelSemicolon(entry)
	if len(parts) <= 1 {
		// Handle trailing semicolon like `local://report;` vs `a.txt;`
		if isRegisteredURL(entry) {
			return nil
		}
		if len(parts) == 1 && probeLiteralPathExists(entry, cwd) == "missing" {
			return parts
		}
		return nil
	}
	// If entry starts with a registered internal URL:
	if isRegisteredURL(entry) {
		allURLOrExcl := true
		for _, p := range parts {
			if !isRegisteredURL(p) && !(allowExclusions && isExclusionPartSyntax(p)) {
				allURLOrExcl = false
				break
			}
		}
		if allURLOrExcl {
			return parts
		}
		// Mixed list starting with an internal URL (`local://1.log;notes.txt`):
		// keep whole if the full entry resolves as an existing non-absent URL.
		if urlfs != nil && urlfs.probeURLTarget(entry) {
			return nil
		}
		return parts
	}
	// Literal filesystem entry containing `;` wins over splitting.
	if probeLiteralPathExists(entry, cwd) != "missing" {
		return nil
	}
	peeled, _, hasSel := splitHostPathAndSelector(entry)
	if hasSel && probeLiteralPathExists(peeled, cwd) != "missing" {
		return nil
	}
	return parts
}

// hasGlobChars reports whether s contains glob metacharacters.
func hasGlobChars(s string) bool {
	return strings.ContainsAny(s, "*?[{")
}

// splitHostPathAndSelector strictly peels a trailing selector (`:N-M`, `:raw`, etc.) off a host path.
func splitHostPathAndSelector(entry string) (clean string, sel string, hasSel bool) {
	idx := strings.LastIndexByte(entry, ':')
	if idx <= 0 {
		return entry, "", false
	}
	// Preserve Windows drive letters like C:\foo
	if idx == 1 && ((entry[0] >= 'a' && entry[0] <= 'z') || (entry[0] >= 'A' && entry[0] <= 'Z')) {
		return entry, "", false
	}
	cand := entry[idx+1:]
	if cand == "" {
		return entry, "", false
	}
	if !isHostSelectorCandidate(cand) {
		return entry, "", false
	}
	base := entry[:idx]
	// Check compound selector like `:1-50:raw` or `:raw:1-50`
	if strings.EqualFold(cand, "raw") {
		if inner := strings.LastIndexByte(base, ':'); inner > 0 {
			if !(inner == 1 && ((base[0] >= 'a' && base[0] <= 'z') || (base[0] >= 'A' && base[0] <= 'Z'))) {
				innerCand := base[inner+1:]
				if _, ok := parseLineRanges(innerCand); ok || isTailSelector(innerCand) {
					return base[:inner], innerCand + ":" + cand, true
				}
			}
		}
	} else if _, ok := parseLineRanges(cand); ok || isTailSelector(cand) {
		if inner := strings.LastIndexByte(base, ':'); inner > 0 {
			if strings.EqualFold(base[inner+1:], "raw") {
				return base[:inner], base[inner+1:] + ":" + cand, true
			}
		}
	}
	return base, cand, true
}

func isHostSelectorCandidate(cand string) bool {
	lower := strings.ToLower(cand)
	if lower == "raw" || lower == "conflicts" || lower == "img" {
		return true
	}
	if _, ok := parseLineRanges(cand); ok {
		return true
	}
	return isTailSelector(cand)
}

func isTailSelector(cand string) bool {
	if !strings.HasPrefix(cand, "-") || len(cand) < 2 {
		return false
	}
	for i := 1; i < len(cand); i++ {
		if cand[i] < '0' || cand[i] > '9' {
			return false
		}
	}
	return true
}

// splitHostPathAndSelectorPreferLiteral peels a selector unless the full path exists literally on disk.
// upstream: packages/coding-agent/src/tools/path-utils.ts:256-268
func splitHostPathAndSelectorPreferLiteral(entry, cwd string) (clean string, sel string, literalMatch bool) {
	base, strictSel, hasSel := splitHostPathAndSelector(entry)
	if !hasSel {
		return entry, "", false
	}
	probe := probeLiteralPathExists(entry, cwd)
	if probe == "exists" || probe == "unknown" {
		return entry, "", true
	}
	return base, strictSel, false
}

// parseLineRanges parses omp's line range syntax: `50-100`, `50..100`, `50+10`, `50-`, `50..`, `50`, `5-16,960-973`.
// Rejects `:-N` tail selectors (not valid for grep).
// upstream: packages/tui/src/tools/line-ranges.ts
func parseLineRanges(sel string) ([]lineRange, bool) {
	parts := strings.Split(sel, ",")
	var out []lineRange
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || strings.HasPrefix(part, "-") {
			return nil, false
		}
		if plus := strings.IndexByte(part, '+'); plus > 0 {
			start, ok1 := parsePosInt(part[:plus])
			count, ok2 := parsePosInt(part[plus+1:])
			if !ok1 || !ok2 {
				return nil, false
			}
			out = append(out, lineRange{start: start, end: start + count - 1})
			continue
		}
		if dots := strings.Index(part, ".."); dots > 0 {
			start, ok1 := parsePosInt(part[:dots])
			if !ok1 {
				return nil, false
			}
			rest := part[dots+2:]
			if rest == "" {
				out = append(out, lineRange{start: start, end: 0})
				continue
			}
			end, ok2 := parsePosInt(rest)
			if !ok2 || end < start {
				return nil, false
			}
			out = append(out, lineRange{start: start, end: end})
			continue
		}
		if dash := strings.IndexByte(part, '-'); dash > 0 {
			start, ok1 := parsePosInt(part[:dash])
			if !ok1 {
				return nil, false
			}
			rest := part[dash+1:]
			if rest == "" {
				out = append(out, lineRange{start: start, end: 0})
				continue
			}
			end, ok2 := parsePosInt(rest)
			if !ok2 || end < start {
				return nil, false
			}
			out = append(out, lineRange{start: start, end: end})
			continue
		}
		n, ok := parsePosInt(part)
		if !ok {
			return nil, false
		}
		out = append(out, lineRange{start: n, end: n})
	}
	return out, len(out) > 0
}

func parsePosInt(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for i := range len(s) {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
		if n > 1_000_000_000 {
			return 0, false
		}
	}
	return n, n >= 1
}

// parsedGlobTarget is the base directory, glob pattern, and recursion policy for one path entry.
type parsedGlobTarget struct {
	basePath      string // resolved absolute base directory/file or normalized internal URL
	globPattern   string // pattern relative to basePath ("" if none)
	hasGlob       bool
	recursive     bool
	bareGlob      bool
	directoryOnly bool
	isURL         bool
}

// parseURLSearchTarget splits a registered internal URL at its first glob segment.
// Returns nil when input is not a registered internal URL.
// upstream: packages/coding-agent/src/tools/path-utils.ts:959-977
func parseURLSearchTarget(input string) (*parsedGlobTarget, error) {
	norm := normalizeInternalURL(input)
	if !urls.CanHandle(norm) {
		return nil, nil
	}
	if !isInternalURLGlob(norm) {
		return &parsedGlobTarget{
			basePath:  norm,
			hasGlob:   false,
			recursive: true,
			isURL:     true,
		}, nil
	}
	u, err := urls.Parse(norm)
	if err != nil {
		return nil, err
	}
	rootLen := len(u.Scheme) + 3
	rest := norm[rootLen:]
	segments := strings.Split(rest, "/")
	from := 0
	if h, ok := urls.Lookup(u.Scheme); ok && h.Spec().PortAuthority {
		from = 1
	}
	firstGlob := -1
	for i := from; i < len(segments); i++ {
		if hasGlobChars(segments[i]) {
			firstGlob = i
			break
		}
	}
	if firstGlob == -1 {
		return &parsedGlobTarget{
			basePath:  norm,
			hasGlob:   false,
			recursive: true,
			isURL:     true,
		}, nil
	}
	basePath := norm[:rootLen] + strings.Join(segments[:firstGlob], "/")
	rawGlobSegs := segments[firstGlob:]
	decodedSegs := make([]string, len(rawGlobSegs))
	for i, seg := range rawGlobSegs {
		d, err := decodeURLGlobSegment(seg, input)
		if err != nil {
			return nil, err
		}
		decodedSegs[i] = d
	}
	globPart := strings.Join(decodedSegs, "/")
	dirOnly := strings.HasSuffix(globPart, "/")
	if dirOnly {
		globPart = strings.TrimRight(globPart, "/")
		if globPart == "" {
			globPart = "**/*"
		}
	}
	bareGlob := strings.HasSuffix(basePath, "://")
	recursive := strings.Contains(globPart, "**") || strings.Contains(globPart, "/") || bareGlob
	return &parsedGlobTarget{
		basePath:      basePath,
		globPattern:   globPart,
		hasGlob:       true,
		recursive:     recursive,
		bareGlob:      bareGlob,
		directoryOnly: dirOnly,
		isURL:         true,
	}, nil
}

// parseSearchTarget splits a grep path entry into basePath, glob pattern, and recursion policy.
// Prefers literal filesystem match when a path with glob chars (e.g. `apps/[id]/page.tsx`) exists.
// upstream: packages/coding-agent/src/tools/path-utils.ts:979-1019
func parseSearchTarget(entry, cwd string) (parsedGlobTarget, error) {
	if urlTarget, err := parseURLSearchTarget(entry); err != nil {
		return parsedGlobTarget{}, err
	} else if urlTarget != nil {
		return *urlTarget, nil
	}

	norm := strings.ReplaceAll(expandPath(entry), "\\", "/")
	if !hasGlobChars(norm) {
		return parsedGlobTarget{
			basePath:  resolvePath(norm, cwd),
			hasGlob:   false,
			recursive: true,
		}, nil
	}
	// Prefer literal match if the path exists on disk (e.g. Next.js `apps/[id]/page.tsx`).
	if _, err := os.Stat(resolvePath(norm, cwd)); err == nil {
		return parsedGlobTarget{
			basePath:  resolvePath(norm, cwd),
			hasGlob:   false,
			recursive: true,
		}, nil
	}
	return parseHostGlobParts(entry, norm, cwd, false), nil
}

// parseFindPattern splits a glob tool path entry into basePath, globPattern, hasGlob, and directoryOnly.
// upstream: packages/coding-agent/src/tools/path-utils.ts:1021-1057
func parseFindPattern(entry, cwd string) (parsedGlobTarget, error) {
	if urlTarget, err := parseURLSearchTarget(entry); err != nil {
		return parsedGlobTarget{}, err
	} else if urlTarget != nil {
		if !urlTarget.hasGlob {
			urlTarget.globPattern = "**/*"
		} else {
			// In glob tool, URL globs stay anchored to their URL base like dir/* unless ** is present.
			urlTarget.recursive = strings.Contains(urlTarget.globPattern, "**") || strings.Contains(urlTarget.globPattern, "/")
		}
		return *urlTarget, nil
	}
	norm := strings.ReplaceAll(expandPath(entry), "\\", "/")
	if !hasGlobChars(norm) {
		return parsedGlobTarget{
			basePath:    resolvePath(norm, cwd),
			globPattern: "**/*",
			hasGlob:     false,
			recursive:   true,
		}, nil
	}
	return parseHostGlobParts(entry, norm, cwd, true), nil
}

func parseHostGlobParts(rawEntry, norm, cwd string, forGlobTool bool) parsedGlobTarget {
	// Bare glob without directory separator: matches at any depth under cwd,
	// EXCEPT exact brace unions like `{src,test}` which stay anchored at root
	// (upstream: crates/xfs/src/glob.rs:46-69).
	if !strings.Contains(norm, "/") {
		pat := norm
		if !strings.HasPrefix(pat, "**/") && !strings.HasPrefix(pat, "**") && !isExactBraceUnion(pat) {
			pat = "**/" + pat
		}
		return parsedGlobTarget{
			basePath:    resolvePath(".", cwd),
			globPattern: pat,
			hasGlob:     true,
			recursive:   true,
			bareGlob:    !strings.Contains(rawEntry, "/") && !strings.Contains(rawEntry, "\\"),
		}
	}
	segments := strings.Split(norm, "/")
	splitAt := len(segments)
	for i, seg := range segments {
		if hasGlobChars(seg) {
			splitAt = i
			break
		}
	}
	basePart := strings.Join(segments[:splitAt], "/")
	if basePart == "" {
		if strings.HasPrefix(norm, "/") {
			basePart = "/"
		} else {
			basePart = "."
		}
	}
	globPart := strings.Join(segments[splitAt:], "/")
	dirOnly := false
	if forGlobTool && strings.HasSuffix(globPart, "/") {
		dirOnly = true
		globPart = strings.TrimRight(globPart, "/")
		if globPart == "" {
			globPart = "**/*"
		}
	}
	if forGlobTool && splitAt == 0 && !strings.HasPrefix(globPart, "**/") && globPart != "**" {
		globPart = "**/" + globPart
	}
	recursive := strings.Contains(globPart, "**") || strings.Contains(globPart, "/")
	return parsedGlobTarget{
		basePath:      resolvePath(basePart, cwd),
		globPattern:   globPart,
		hasGlob:       true,
		recursive:     recursive,
		bareGlob:      !strings.Contains(rawEntry, "/") && !strings.Contains(rawEntry, "\\"),
		directoryOnly: dirOnly,
	}
}

// rebaseExclusionsForRoot rebases cwd-relative `!glob` exclusions onto one search root.
// Patterns without `/` (or starting with `**`) match at any depth; patterns with `/`
// are anchored to cwd and rebased relative to root (omitted if outside root).
// upstream: packages/coding-agent/src/tools/grep.ts:487-538
func rebaseExclusionsForRoot(rawPatterns []string, cwd, root string, rootIsURL bool) ([]*globMatcher, error) {
	var out []*globMatcher
	for _, raw := range rawPatterns {
		pat := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
		pat = strings.TrimRight(pat, "/")
		if pat == "" {
			continue
		}
		if !strings.Contains(pat, "/") || strings.HasPrefix(pat, "**") {
			// Match at any depth: both basename and nested paths.
			built := pat
			if !strings.HasPrefix(built, "**/") && !strings.HasPrefix(built, "**") {
				built = "**/" + built
			}
			m, err := compileGlob(built)
			if err != nil {
				return nil, fmt.Errorf("invalid exclusion glob %q: %w", raw, err)
			}
			out = append(out, m)
			continue
		}
		if rootIsURL {
			continue
		}
		target := resolvePath(pat, cwd)
		rel, err := filepath.Rel(root, target)
		if err != nil || rel == "" || rel == "." || filepath.IsAbs(rel) {
			continue
		}
		posix := filepath.ToSlash(rel)
		if posix == ".." || strings.HasPrefix(posix, "../") {
			continue
		}
		m, err := compileGlobAnchored(posix)
		if err != nil {
			return nil, fmt.Errorf("invalid exclusion glob %q: %w", raw, err)
		}
		out = append(out, m)
	}
	return out, nil
}

func resolvePath(p, cwd string) string {
	p = expandPath(p)
	if isRootOnlyPath(p) {
		return "/"
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(cwd, p))
}

// formatRelToCwd formats absPath relative to cwd with forward slashes when inside cwd,
// or as an absolute forward-slash path when outside cwd. Internal URLs pass through.
func formatRelToCwd(absPath, cwd string, trailingSlash bool) string {
	if isRegisteredURL(absPath) {
		out := normalizeInternalURL(absPath)
		if trailingSlash && !strings.HasSuffix(out, "/") {
			out += "/"
		}
		return out
	}
	stdAbs := dirs.StandardizeMacOSPath(absPath)
	stdCwd := dirs.StandardizeMacOSPath(cwd)
	rel, err := filepath.Rel(stdCwd, stdAbs)
	var out string
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		out = filepath.ToSlash(rel)
	} else {
		out = filepath.ToSlash(stdAbs)
	}
	if trailingSlash && out != "." && !strings.HasSuffix(out, "/") {
		out += "/"
	}
	return out
}

func isRootOnlyPath(p string) bool {
	trimmed := strings.TrimSpace(p)
	if trimmed == "" {
		return false
	}
	for i := range len(trimmed) {
		if trimmed[i] != '/' {
			return false
		}
	}
	return true
}

func toScopeDisplay(items []string, cwd string) string {
	out := make([]string, len(items))
	for i, item := range items {
		trailing := strings.HasSuffix(item, "/") || strings.HasSuffix(item, "\\")
		if isRegisteredURL(item) {
			out[i] = formatRelToCwd(item, cwd, trailing)
		} else {
			out[i] = formatRelToCwd(resolvePath(item, cwd), cwd, trailing)
		}
	}
	return strings.Join(out, ", ")
}
