package search

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// toPathList splits a `;`-delimited path string (or JSON string array) into trimmed non-empty entries.
// upstream: packages/tui/src/render/render-utils.ts (toPathList)
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
	parts := strings.Split(trimmed, ";")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// splitExclusions separates `!glob` exclusion entries from positive path entries.
func splitExclusions(entries []string) (includes []string, excludes []*globMatcher, err error) {
	for _, e := range entries {
		if strings.HasPrefix(e, "!") {
			pat := strings.TrimPrefix(e, "!")
			if pat == "" {
				continue
			}
			// Bare exclusion like !*.test.ts should match at any depth.
			if !strings.Contains(pat, "/") && !strings.HasPrefix(pat, "**/") {
				pat = "**/" + pat
			}
			m, err := compileGlob(pat)
			if err != nil {
				return nil, nil, fmt.Errorf("invalid exclusion glob %q: %w", e, err)
			}
			excludes = append(excludes, m)
			continue
		}
		includes = append(includes, e)
	}
	return includes, excludes, nil
}

// hasGlobChars reports whether s contains glob metacharacters.
func hasGlobChars(s string) bool {
	return strings.ContainsAny(s, "*?[{")
}

// splitPathAndSelector peels a trailing `:N-M`, `:N+K`, `:N,M`, or `:N` line selector
// off a path entry, unless the literal path exists on disk.
// upstream: packages/coding-agent/src/tools/grep.ts:135-190
func splitPathAndSelector(entry, cwd string) (clean string, ranges []lineRange, err error) {
	// If the literal path exists as-is, don't split a colon out of its name.
	cand := resolvePath(entry, cwd)
	if _, statErr := os.Stat(cand); statErr == nil {
		return entry, nil, nil
	}
	idx := strings.LastIndexByte(entry, ':')
	if idx <= 0 {
		return entry, nil, nil
	}
	// On Windows, don't split drive letter like C:\foo
	if idx == 1 && ((entry[0] >= 'a' && entry[0] <= 'z') || (entry[0] >= 'A' && entry[0] <= 'Z')) {
		return entry, nil, nil
	}
	sel := entry[idx+1:]
	if sel == "" {
		return entry, nil, nil
	}
	parsed, ok := parseLineRanges(sel)
	if !ok {
		return entry, nil, nil
	}
	return entry[:idx], parsed, nil
}

// parseLineRanges parses omp's line range syntax: `50-100`, `50+10`, `50-`, `50`, `5-16,960-973`.
// Rejects `:-N` tail selectors (not valid for grep).
func parseLineRanges(sel string) ([]lineRange, bool) {
	parts := strings.Split(sel, ",")
	var out []lineRange
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || strings.HasPrefix(part, "-") {
			return nil, false
		}
		if plus := strings.IndexByte(part, '+'); plus > 0 {
			start, err1 := strconv.Atoi(part[:plus])
			count, err2 := strconv.Atoi(part[plus+1:])
			if err1 != nil || err2 != nil || start < 1 || count < 1 {
				return nil, false
			}
			out = append(out, lineRange{start: start, end: start + count - 1})
			continue
		}
		if dash := strings.IndexByte(part, '-'); dash > 0 {
			start, err1 := strconv.Atoi(part[:dash])
			if err1 != nil || start < 1 {
				return nil, false
			}
			rest := part[dash+1:]
			if rest == "" {
				out = append(out, lineRange{start: start, end: 0})
				continue
			}
			end, err2 := strconv.Atoi(rest)
			if err2 != nil || end < start {
				return nil, false
			}
			out = append(out, lineRange{start: start, end: end})
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 1 {
			return nil, false
		}
		out = append(out, lineRange{start: n, end: n})
	}
	return out, len(out) > 0
}

// parsedGlobTarget is the base directory, glob pattern, and recursion policy for one path entry.
type parsedGlobTarget struct {
	basePath    string // resolved absolute base directory or file
	globPattern string // pattern relative to basePath ("" if none)
	hasGlob     bool
	recursive   bool
}

// parseGlobTarget splits a path entry like `*.ts`, `dir/*.ts`, `dir/**/*.ts`, or `src`
// into its base directory, relative glob pattern, and recursion policy.
// Bare glob `*.ts` matches any depth; `dir/*.ts` only `dir`'s direct children (`dir/**/*.ts` recurses).
func parseGlobTarget(entry, cwd string) parsedGlobTarget {
	norm := strings.ReplaceAll(entry, "\\", "/")
	if !hasGlobChars(norm) {
		return parsedGlobTarget{
			basePath:  resolvePath(norm, cwd),
			hasGlob:   false,
			recursive: true,
		}
	}
	// Bare glob without any directory separator: matches at any depth under cwd.
	if !strings.Contains(norm, "/") {
		return parsedGlobTarget{
			basePath:    resolvePath(".", cwd),
			globPattern: "**/" + norm,
			hasGlob:     true,
			recursive:   true,
		}
	}
	// Split at the first segment containing a glob character.
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
	recursive := strings.Contains(globPart, "**") || strings.Contains(globPart, "/")
	return parsedGlobTarget{
		basePath:    resolvePath(basePart, cwd),
		globPattern: globPart,
		hasGlob:     true,
		recursive:   recursive,
	}
}

func resolvePath(p, cwd string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(cwd, p))
}

// formatRelToCwd formats absPath relative to cwd with forward slashes when inside cwd,
// or as an absolute forward-slash path when outside cwd.
func formatRelToCwd(absPath, cwd string, trailingSlash bool) string {
	rel, err := filepath.Rel(cwd, absPath)
	var out string
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		out = filepath.ToSlash(rel)
	} else {
		out = filepath.ToSlash(absPath)
	}
	if trailingSlash && !strings.HasSuffix(out, "/") {
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
