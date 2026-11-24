package search

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// upstream: packages/coding-agent/src/tools/grep.ts:73-94
const (
	grepDefaultFileLimit     = 20
	grepMultiFilePerFileCap  = 20
	grepSingleFilePerFileCap = 200
	grepInternalTotalCap     = 2000
	grepMaxFileBytes         = 4 * 1024 * 1024
	grepDefaultMaxColumn     = 500
	grepDefaultTimeout       = 30 * time.Second
	grepBinaryProbeBytes     = 8192
	grepMaxOutputBytes       = 50 * 1024
)

type grepMatch struct {
	absPath    string
	relPath    string
	lineNumber int
	line       string
	truncated  bool
	column     int // 1-based column when line was truncated
}

type grepTarget struct {
	entry   string
	clean   string
	ranges  []lineRange
	parsed  parsedGlobTarget
	isFile  bool
	missing bool
}

// pcreOnlyHint detects common PCRE2 constructs unsupported by Go's RE2 engine.
var pcreOnlyRe = regexp.MustCompile(`\(\?[=!<]|\\[1-9]`)

func compileGrepRegex(pattern string, caseSensitive bool, multiline bool) (*regexp.Regexp, error) {
	// Normalize literal \\n to \n when multiline is enabled.
	norm := pattern
	flags := ""
	if !caseSensitive {
		flags += "i"
	}
	if multiline {
		flags += "m"
	}
	expr := norm
	if flags != "" {
		expr = "(?" + flags + ")" + norm
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		if pcreOnlyRe.MatchString(pattern) {
			return nil, fmt.Errorf("Invalid regex: Go RE2 does not support look-around or backreferences (PCRE2 fallback not ported): %w", err)
		}
		return nil, fmt.Errorf("Invalid regex: %w", err)
	}
	return re, nil
}

func isMultilinePattern(pattern string) bool {
	return strings.Contains(pattern, "\n") || strings.Contains(pattern, `\n`)
}

func executeGrep(ctx sdk.Context, params map[string]any) (any, error) {
	bgCtx, cancel := context.WithTimeout(context.Background(), grepDefaultTimeout)
	defer cancel()
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-bgCtx.Done():
		}
	}()
	return runGrep(bgCtx, ctx.Cwd(), params)
}

func runGrep(bgCtx context.Context, cwd string, params map[string]any) (any, error) {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}

	pattern, _ := params["pattern"].(string)
	if pattern == "" {
		return nil, fmt.Errorf("`pattern` must be a non-empty string")
	}

	pathInput, _ := params["path"].(string)
	caseSensitive := true
	if v, ok := params["case"].(bool); ok {
		caseSensitive = v
	}
	useGitignore := true
	if v, ok := params["gitignore"].(bool); ok {
		useGitignore = v
	}
	skip := 0
	if v, ok := params["skip"].(float64); ok && !math.IsNaN(v) && !math.IsInf(v, 0) {
		if v < 0 {
			return nil, fmt.Errorf("`skip` must be a non-negative integer")
		}
		skip = int(v)
	}

	multiline := isMultilinePattern(pattern)
	re, err := compileGrepRegex(pattern, caseSensitive, multiline)
	if err != nil {
		return nil, err
	}

	rawEntries := toPathList(pathInput)
	includes, excludes, err := splitExclusions(rawEntries)
	if err != nil {
		return nil, err
	}
	if len(includes) == 0 {
		includes = []string{"."}
	}

	var targets []grepTarget
	var missingPaths []string
	rangesByAbs := make(map[string][]lineRange)

	for _, entry := range includes {
		if isRootOnlyPath(entry) {
			return nil, fmt.Errorf("Searching from root directory '/' is not allowed")
		}
		clean, ranges, err := splitPathAndSelector(entry, cwd)
		if err != nil {
			return nil, err
		}
		parsed := parseGlobTarget(clean, cwd)
		if isRootOnlyPath(parsed.basePath) {
			return nil, fmt.Errorf("Searching from root directory '/' is not allowed")
		}
		info, statErr := os.Stat(parsed.basePath)
		if statErr != nil {
			missingPaths = append(missingPaths, entry)
			targets = append(targets, grepTarget{entry: entry, clean: clean, ranges: ranges, parsed: parsed, missing: true})
			continue
		}
		isFile := !info.IsDir()
		if len(ranges) > 0 {
			if !isFile {
				return nil, fmt.Errorf("line-range selector on %q is only supported for files", entry)
			}
			rangesByAbs[parsed.basePath] = ranges
		}
		targets = append(targets, grepTarget{
			entry:  entry,
			clean:  clean,
			ranges: ranges,
			parsed: parsed,
			isFile: isFile,
		})
	}

	validTargets := 0
	for _, t := range targets {
		if !t.missing {
			validTargets++
		}
	}
	if validTargets == 0 {
		return nil, fmt.Errorf("Path not found: %s; list each target in the semicolon-delimited `path`", strings.Join(missingPaths, ", "))
	}

	isMultiScope := validTargets > 1 || !targets[0].isFile
	perFileCap := grepSingleFilePerFileCap
	if isMultiScope {
		perFileCap = grepMultiFilePerFileCap
	}

	// Collect candidate files in deterministic sorted order per target.
	type fileCandidate struct {
		absPath string
		relPath string
		size    int64
	}
	seenFiles := make(map[string]struct{})
	var candidates []fileCandidate

	for _, t := range targets {
		if t.missing {
			continue
		}
		if t.isFile {
			if _, ok := seenFiles[t.parsed.basePath]; !ok {
				seenFiles[t.parsed.basePath] = struct{}{}
				info, _ := os.Stat(t.parsed.basePath)
				var sz int64
				if info != nil {
					sz = info.Size()
				}
				candidates = append(candidates, fileCandidate{
					absPath: t.parsed.basePath,
					relPath: formatRelToCwd(t.parsed.basePath, cwd, false),
					size:    sz,
				})
			}
			continue
		}
		var includeMatcher *globMatcher
		if t.parsed.hasGlob {
			m, err := compileGlob(t.parsed.globPattern)
			if err != nil {
				return nil, fmt.Errorf("invalid glob %q: %w", t.parsed.globPattern, err)
			}
			includeMatcher = m
		}
		entries, err := walkDir(bgCtx, t.parsed.basePath, walkOptions{
			hidden:    true,
			gitignore: useGitignore,
			recursive: t.parsed.recursive,
			excludes:  excludes,
			include:   includeMatcher,
		})
		if err != nil {
			if bgCtx.Err() == context.DeadlineExceeded {
				return nil, fmt.Errorf("Grep timed out after %ds; narrow paths or pattern, or scope with `glob` first", int(grepDefaultTimeout.Seconds()))
			}
			return nil, err
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].relPath < entries[j].relPath
		})
		for _, e := range entries {
			if e.isDir {
				continue
			}
			if _, ok := seenFiles[e.absPath]; ok {
				continue
			}
			seenFiles[e.absPath] = struct{}{}
			candidates = append(candidates, fileCandidate{
				absPath: e.absPath,
				relPath: formatRelToCwd(e.absPath, cwd, false),
				size:    e.size,
			})
		}
	}

	// Search candidates up to grepInternalTotalCap.
	var allMatches []grepMatch
	limitReached := false
	for _, cand := range candidates {
		if bgCtx.Err() != nil {
			if bgCtx.Err() == context.DeadlineExceeded {
				return nil, fmt.Errorf("Grep timed out after %ds; narrow paths or pattern, or scope with `glob` first", int(grepDefaultTimeout.Seconds()))
			}
			return nil, bgCtx.Err()
		}
		ranges := rangesByAbs[cand.absPath]
		// Fetch perFileCap+1 so we know if the file overflowed.
		fetchCap := perFileCap + 1
		if len(allMatches)+fetchCap > grepInternalTotalCap {
			fetchCap = grepInternalTotalCap - len(allMatches)
			if fetchCap <= 0 {
				limitReached = true
				break
			}
		}
		fileMatches := searchFile(cand.absPath, cand.relPath, re, multiline, ranges, fetchCap)
		allMatches = append(allMatches, fileMatches...)
		if len(allMatches) >= grepInternalTotalCap {
			limitReached = true
			break
		}
	}

	// Group matches by file in encounter order, applying pagination and per-file cap.
	// upstream: packages/coding-agent/src/tools/grep.ts:785-842
	type fileGroup struct {
		absPath string
		relPath string
		count   int
		list    []grepMatch
	}
	groupIndex := make(map[string]int)
	var groups []*fileGroup
	var windowGroups []*fileGroup

	canPaginate := isMultiScope
	windowStart := 0
	windowEnd := math.MaxInt
	if canPaginate {
		windowStart = skip
		windowEnd = skip + grepDefaultFileLimit
	}
	perFileLimitReached := false

	for _, m := range allMatches {
		idx, ok := groupIndex[m.relPath]
		if !ok {
			order := len(groups)
			g := &fileGroup{absPath: m.absPath, relPath: m.relPath, count: 1}
			if order >= windowStart && order < windowEnd {
				g.list = append(g.list, m)
				windowGroups = append(windowGroups, g)
			}
			groupIndex[m.relPath] = order
			groups = append(groups, g)
			continue
		}
		g := groups[idx]
		g.count++
		if g.count > perFileCap {
			perFileLimitReached = true
			continue
		}
		if g.list != nil {
			g.list = append(g.list, m)
		}
	}

	totalFiles := len(groups)
	totalFilesLabel := fmt.Sprintf("%d", totalFiles)
	if limitReached {
		totalFilesLabel = fmt.Sprintf("%d+", totalFiles)
	}
	skipFiles := 0
	if canPaginate {
		skipFiles = min(skip, totalFiles)
	}
	fileLimitReached := canPaginate && totalFiles > skipFiles+grepDefaultFileLimit
	nextSkip := skipFiles + len(windowGroups)

	var warningParts []string
	if len(missingPaths) > 0 {
		warningParts = append(warningParts, "Skipped missing paths: "+strings.Join(missingPaths, ", "))
	}
	warningNote := strings.Join(warningParts, "\n")

	if len(windowGroups) == 0 {
		var noMatchText string
		if canPaginate && skip > 0 && totalFiles > 0 && skipFiles >= totalFiles {
			noMatchText = fmt.Sprintf("No more results (%s files total; skip=%d is past the end)", totalFilesLabel, skip)
		} else {
			noMatchText = "No matches found"
		}
		if warningNote != "" {
			noMatchText += "\n" + warningNote
		}
		return sdk.ToolResult{
			Content: noMatchText,
			Details: map[string]any{
				"matchCount": 0,
				"fileCount":  0,
				"files":      []string{},
				"truncated":  false,
			},
		}, nil
	}

	fileList := make([]string, len(windowGroups))
	groupByRel := make(map[string]*fileGroup, len(windowGroups))
	totalSelected := 0
	for i, g := range windowGroups {
		fileList[i] = g.relPath
		groupByRel[g.relPath] = g
		totalSelected += len(g.list)
	}

	linesTruncated := false
	outputLines := formatFileMatches(fileList, isMultiScope, func(relPath string) fileMatchSection {
		g := groupByRel[relPath]
		shown := make([]int, 0, len(g.list))
		for _, m := range g.list {
			shown = append(shown, m.lineNumber)
		}
		tag := snapshotTagForFile(g.absPath, shown)
		useHash := tag != ""
		var lines []string
		lastLine := 0
		for _, m := range g.list {
			if lastLine > 0 && m.lineNumber > lastLine+1 {
				lines = append(lines, "...")
			}
			text := m.line
			if m.truncated {
				linesTruncated = true
				if m.column > 0 {
					text = fmt.Sprintf("%s [col %d]", text, m.column)
				}
			}
			if useHash {
				lines = append(lines, fmt.Sprintf("*%d:%s", m.lineNumber, text))
			} else {
				lines = append(lines, fmt.Sprintf("*%d|%s", m.lineNumber, text))
			}
			lastLine = m.lineNumber
		}
		return fileMatchSection{model: lines, tag: tag}
	})

	if fileLimitReached {
		outputLines = append(outputLines, "", fmt.Sprintf("Showing files %d-%d of %s. Use skip=%d for the next page, or narrow paths/pattern.", skipFiles+1, nextSkip, totalFilesLabel, nextSkip))
	}
	if warningNote != "" {
		outputLines = append(outputLines, "", warningNote)
	}

	rawOutput := strings.Join(outputLines, "\n")
	output, byteTruncated := truncateBytes(rawOutput, grepMaxOutputBytes)
	// upstream: packages/coding-agent/src/tools/grep.ts:1072, packages/tui/src/tools/output-meta.ts:337-365
	if linesTruncated {
		output += fmt.Sprintf("\n\n[Some lines truncated to %d bytes]", grepDefaultMaxColumn)
	}

	return sdk.ToolResult{
		Content: output,
		Details: map[string]any{
			"matchCount": totalSelected,
			"fileCount":  len(fileList),
			"files":      fileList,
			"truncated":  fileLimitReached || perFileLimitReached || limitReached || byteTruncated || linesTruncated,
		},
	}, nil
}

func searchFile(absPath, relPath string, re *regexp.Regexp, multiline bool, ranges []lineRange, maxMatches int) []grepMatch {
	f, err := os.Open(absPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	// Read up to grepMaxFileBytes.
	buf := make([]byte, grepMaxFileBytes)
	n, _ := f.Read(buf)
	data := buf[:n]
	if len(data) == 0 {
		return nil
	}
	// Skip binary files (NUL byte in first 8KB).
	probeLen := min(len(data), grepBinaryProbeBytes)
	if bytes.IndexByte(data[:probeLen], 0) != -1 {
		return nil
	}

	var matches []grepMatch
	if multiline {
		locs := re.FindAllIndex(data, maxMatches*4)
		if len(locs) == 0 {
			return nil
		}
		seenLines := make(map[int]struct{})
		for _, loc := range locs {
			lineNum := 1 + bytes.Count(data[:loc[0]], []byte{'\n'})
			if _, seen := seenLines[lineNum]; seen {
				continue
			}
			if !lineInRanges(lineNum, ranges) {
				continue
			}
			seenLines[lineNum] = struct{}{}
			lineStart := bytes.LastIndexByte(data[:loc[0]], '\n') + 1
			lineEnd := bytes.IndexByte(data[lineStart:], '\n')
			var lineBytes []byte
			if lineEnd == -1 {
				lineBytes = data[lineStart:]
			} else {
				lineBytes = data[lineStart : lineStart+lineEnd]
			}
			lineStr := strings.TrimRight(string(lineBytes), "\r")
			col := loc[0] - lineStart + 1
			trimmed, trunc := truncateLine(lineStr, grepDefaultMaxColumn)
			matches = append(matches, grepMatch{
				absPath:    absPath,
				relPath:    relPath,
				lineNumber: lineNum,
				line:       trimmed,
				truncated:  trunc,
				column:     col,
			})
			if len(matches) >= maxMatches {
				break
			}
		}
		return matches
	}

	// Single-line scan.
	lineNum := 0
	pos := 0
	for pos < len(data) {
		lineNum++
		nl := bytes.IndexByte(data[pos:], '\n')
		var lineBytes []byte
		if nl == -1 {
			lineBytes = data[pos:]
			pos = len(data)
		} else {
			lineBytes = data[pos : pos+nl]
			pos += nl + 1
		}
		if len(lineBytes) > 0 && lineBytes[len(lineBytes)-1] == '\r' {
			lineBytes = lineBytes[:len(lineBytes)-1]
		}
		if !lineInRanges(lineNum, ranges) {
			continue
		}
		loc := re.FindIndex(lineBytes)
		if loc == nil {
			continue
		}
		trimmed, trunc := truncateLine(string(lineBytes), grepDefaultMaxColumn)
		matches = append(matches, grepMatch{
			absPath:    absPath,
			relPath:    relPath,
			lineNumber: lineNum,
			line:       trimmed,
			truncated:  trunc,
			column:     loc[0] + 1,
		})
		if len(matches) >= maxMatches {
			break
		}
	}
	return matches
}

func truncateLine(s string, maxBytes int) (string, bool) {
	if len(s) <= maxBytes {
		return s, false
	}
	// Trim on UTF-8 rune boundary.
	cut := maxBytes - 3
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + "...", true
}

func truncateBytes(s string, maxBytes int) (string, bool) {
	if len(s) <= maxBytes {
		return s, false
	}
	cut := maxBytes
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + "\n... (output truncated)", true
}
