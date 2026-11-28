package search

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/dirs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

// upstream: packages/coding-agent/src/tools/grep.ts:90-115,
// packages/tui/src/tools/streaming-output.ts:14-16
const (
	grepDefaultFileLimit     = 20
	grepMultiFilePerFileCap  = 10
	grepSingleFilePerFileCap = 50
	grepInternalTotalCap     = 2000
	grepMaxFileBytes         = 4 * 1024 * 1024
	grepDefaultMaxColumn     = 512
	grepDefaultTimeout       = 30 * time.Second
	grepGitignoreProbeBudget = 5 * time.Second
	grepBinaryProbeBytes     = 64 * 1024
	grepMaxOutputBytes       = 50 * 1024
)

type grepContextLine struct {
	lineNumber int
	line       string
	truncated  bool
}

type grepMatch struct {
	key           string // absolute path or normalized internal URL
	relPath       string // display path (cwd-relative or internal URL)
	hostPath      string // backing host file path ("" for pure virtual/immutable)
	immutable     bool
	lineNumber    int
	line          string
	truncated     bool
	column        int // 1-based rune column when line was truncated
	contextBefore []grepContextLine
	contextAfter  []grepContextLine
}
type grepPathSpec struct {
	original               string
	clean                  string
	ranges                 []lineRange
	isURL                  bool
	literalFilesystemMatch bool
}

type grepCandidate struct {
	key       string // absolute path or internal URL
	relPath   string // display path
	hostPath  string // host path for disk reads & snapshot tags
	content   []byte // pre-loaded bytes for virtual resources
	size      int64
	immutable bool
	namedFile bool // explicitly targeted as a single file (search even if binary)
}

// pcreOnlyRe detects common PCRE2 constructs unsupported by Go's RE2 engine.
var pcreOnlyRe = regexp.MustCompile(`\(\?[=!<]|\\[1-9]`)

// ansiEscapeRe strips CSI/OSC/single-char ANSI escape sequences (mirroring Bun.stripANSI).
var ansiEscapeRe = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)|[@-Z\\-_])`)

// sanitizeText strips ANSI escape sequences and control characters (preserving \t and \n).
// upstream: packages/utils/src/sanitize-text.ts:21-40
func sanitizeText(text string) string {
	needsClean := false
	for i := range len(text) {
		c := text[i]
		if c < 0x20 && c != '\t' && c != '\n' {
			needsClean = true
			break
		}
		if c == 0x7f {
			needsClean = true
			break
		}
		if c == 0xc2 && i+1 < len(text) && text[i+1] >= 0x80 && text[i+1] <= 0x9f {
			needsClean = true
			break
		}
	}
	if !needsClean {
		return text
	}
	if strings.ContainsRune(text, '\x1b') {
		text = ansiEscapeRe.ReplaceAllString(text, "")
	}
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		if (r >= 0x00 && r <= 0x08) || (r >= 0x0b && r <= 0x1f) || (r >= 0x7f && r <= 0x9f) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// sanitizeBraces escapes `{` and `}` that do not open a repetition quantifier
// (`{N}`, `{N,}`, `{N,M}`) or braced escape (`\p{...}`, `\P{...}`, `\x{...}`, `\u{...}`).
// upstream: crates/xfs/src/grep/engine.rs:187-261
func sanitizeBraces(pattern string) string {
	if !strings.ContainsAny(pattern, "{}") {
		return pattern
	}
	var b strings.Builder
	b.Grow(len(pattern) + 8)
	n := len(pattern)
	for i := 0; i < n; {
		switch pattern[i] {
		case '\\':
			if i+1 < n {
				r, sz := utf8.DecodeRuneInString(pattern[i+1:])
				b.WriteByte('\\')
				b.WriteRune(r)
				i += 1 + sz
				if (r == 'p' || r == 'P' || r == 'x' || r == 'u') && i < n && pattern[i] == '{' {
					if end := strings.IndexByte(pattern[i:], '}'); end >= 0 {
						b.WriteString(pattern[i : i+end+1])
						i += end + 1
					} else {
						b.WriteString(pattern[i:])
						i = n
					}
				}
				continue
			}
			b.WriteByte('\\')
			i++
		case '{':
			if end, ok := findValidRepetition(pattern, i); ok {
				b.WriteString(pattern[i : end+1])
				i = end + 1
			} else {
				b.WriteString(`\{`)
				i++
			}
		case '}':
			b.WriteString(`\}`)
			i++
		default:
			r, sz := utf8.DecodeRuneInString(pattern[i:])
			b.WriteRune(r)
			i += sz
		}
	}
	return b.String()
}

func findValidRepetition(s string, start int) (int, bool) {
	digits := func(from int) int {
		i := from
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		return i
	}
	i := digits(start + 1)
	if i == start+1 || i >= len(s) {
		return 0, false
	}
	switch s[i] {
	case '}':
		return i, true
	case ',':
		j := digits(i + 1)
		if j < len(s) && s[j] == '}' {
			return j, true
		}
	}
	return 0, false
}

// escapeUnescapedParentheses escapes every unescaped `(` and `)`.
// upstream: crates/xfs/src/grep/engine.rs:263-293
func escapeUnescapedParentheses(pattern string) string {
	if !strings.ContainsAny(pattern, "()") {
		return pattern
	}
	var b strings.Builder
	b.Grow(len(pattern) + 4)
	n := len(pattern)
	for i := 0; i < n; {
		if pattern[i] == '\\' && i+1 < n {
			r, sz := utf8.DecodeRuneInString(pattern[i+1:])
			b.WriteByte('\\')
			b.WriteRune(r)
			i += 1 + sz
			continue
		}
		r, sz := utf8.DecodeRuneInString(pattern[i:])
		if r == '(' || r == ')' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
		i += sz
	}
	return b.String()
}

// compileGrepRegex mirrors crates/xfs/src/grep/engine.rs:125-152:
//  1. sanitize non-quantifier braces (`${platform}`),
//  2. compile as Go RE2 (returning an explicit error with rewrite hint only for
//     PCRE2-only look-around/backreferences that Go RE2 cannot execute),
//  3. on missing/unexpected parenthesis error, retry with unescaped parens escaped
//     so surrounding regex syntax (`foo.*(bar`) is preserved,
//  4. fall back to literal text (`regexp.QuoteMeta(pattern)`).
func compileGrepRegex(pattern string, caseSensitive bool, multiline bool) (*regexp.Regexp, error) {
	flags := "m"
	if !caseSensitive {
		flags = "im"
	}
	compile := func(expr string) (*regexp.Regexp, error) {
		return regexp.Compile("(?" + flags + ")" + expr)
	}

	sanitized := sanitizeBraces(pattern)
	re, err := compile(sanitized)
	if err == nil {
		return re, nil
	}
	// Preserve explicit error for PCRE2-only look-around and backreferences.
	if pcreOnlyRe.MatchString(pattern) {
		return nil, fmt.Errorf("Invalid regex: Go RE2 does not support look-around or backreferences (PCRE2 fallback not ported; rewrite without look-around/backreferences): %w", err)
	}
	// Stray parenthesis repair: keep surrounding regex syntax intact.
	msg := err.Error()
	if strings.Contains(msg, "missing closing )") || strings.Contains(msg, "unexpected )") {
		escaped := escapeUnescapedParentheses(sanitized)
		if escaped != sanitized {
			if re2, err2 := compile(escaped); err2 == nil {
				return re2, nil
			}
		}
	}
	// Final fallback: search the original pattern as literal text.
	if litRe, litErr := compile(regexp.QuoteMeta(pattern)); litErr == nil {
		return litRe, nil
	}
	return nil, fmt.Errorf("Invalid regex: %w", err)
}

func isMultilinePattern(pattern string) bool {
	return strings.Contains(pattern, "\n") || strings.Contains(pattern, `\n`)
}

func executeGrep(ctx sdk.Context, params map[string]any) (any, error) {
	bgCtx, cancel := context.WithTimeout(context.Background(), grepDefaultTimeout)
	defer cancel()
	if done := ctx.Done(); done != nil {
		go func() {
			select {
			case <-done:
				cancel()
			case <-bgCtx.Done():
			}
		}()
	}
	return runGrepWithStore(bgCtx, ctx.Cwd(), urls.Context{SDK: ctx, Cwd: ctx.Cwd()}, hashline.ForSession(ctx), params)
}

func runGrep(bgCtx context.Context, cwd string, params map[string]any) (any, error) {
	return runGrepWithStore(bgCtx, cwd, urls.Context{Cwd: cwd}, hashline.Default(), params)
}

func isStrictExternalURL(s string) bool {
	lower := strings.ToLower(s)
	return strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "ftp://") ||
		strings.HasPrefix(lower, "ws://") ||
		strings.HasPrefix(lower, "wss://")
}

func runGrepWithURLs(bgCtx context.Context, cwd string, urlCtx urls.Context, params map[string]any) (any, error) {
	store := hashline.Default()
	if hashline.HasSessionID(urlCtx.SDK) {
		store = hashline.ForSession(urlCtx.SDK)
	}
	return runGrepWithStore(bgCtx, cwd, urlCtx, store, params)
}

func runGrepWithStore(bgCtx context.Context, cwd string, urlCtx urls.Context, editStore *hashline.Store, params map[string]any) (any, error) {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	if urlCtx.Cwd == "" {
		urlCtx.Cwd = cwd
	}

	pattern, _ := params["pattern"].(string)
	if strings.TrimSpace(pattern) == "" {
		return nil, fmt.Errorf("Pattern must not be empty")
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
	if rawSkip, exists := params["skip"]; exists && rawSkip != nil {
		v, ok := rawSkip.(float64)
		if !ok || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return nil, fmt.Errorf("Skip must be a non-negative number")
		}
		skip = int(math.Floor(v))
	}

	// Context lines: optional explicit params override configured grep.contextBefore /
	// grep.contextAfter settings (default 0 preserves exact oracle output).
	// upstream: packages/coding-agent/src/tools/grep.ts:652-655,741-743
	contextBefore := grepContextSetting(urlCtx.SDK, "grep.contextBefore")
	contextAfter := grepContextSetting(urlCtx.SDK, "grep.contextAfter")
	if rawCtx, ok := grepContextParam(params, "context"); ok {
		contextBefore = rawCtx
		contextAfter = rawCtx
	}
	if rawBefore, ok := grepContextParam(params, "contextBefore", "context_before", "before"); ok {
		contextBefore = rawBefore
	}
	if rawAfter, ok := grepContextParam(params, "contextAfter", "context_after", "after"); ok {
		contextAfter = rawAfter
	}

	multiline := isMultilinePattern(pattern)
	re, err := compileGrepRegex(pattern, caseSensitive, multiline)
	if err != nil {
		return nil, err
	}

	urlfs := newSearchURLFS(urlCtx)
	scopedPaths := toPathList(pathInput)
	if len(scopedPaths) == 0 {
		scopedPaths = []string{"."}
	}
	expandedEntries := expandDelimitedPathEntries(scopedPaths, cwd, urlfs, true)

	// Separate `!glob` exclusions from search targets; real `!`-prefixed paths stay targets.
	// upstream: packages/coding-agent/src/tools/grep.ts:694-702
	var rawExclusions []string
	var searchEntries []string
	for _, entry := range expandedEntries {
		if isExclusionEntry(entry, cwd) {
			rawExclusions = append(rawExclusions, entry[1:])
		} else {
			searchEntries = append(searchEntries, entry)
		}
	}
	if len(searchEntries) == 0 {
		searchEntries = []string{"."}
	}

	// Parse path specs (selectors for internal URLs and host files).
	// upstream: packages/coding-agent/src/tools/grep.ts:203-272
	specs := make([]grepPathSpec, 0, len(searchEntries))
	for _, entry := range searchEntries {
		if isRegisteredURL(entry) {
			base, sel, hasSel := splitInternalURLSel(entry)
			var ranges []lineRange
			if hasSel {
				parsedRanges, valid := isReadSelectorGrammar(sel)
				if !valid {
					return nil, fmt.Errorf(`path entry %q has an invalid selector ":%s" — use ":N-M" line ranges, ":raw"/":conflicts", a range plus ":raw", or percent-encode a literal ":" as %%3A`, entry, sel)
				}
				if len(parsedRanges) > 0 && isInternalURLGlob(base) {
					return nil, fmt.Errorf("Line-range selector requires a single file, not a glob: %s", entry)
				}
				ranges = parsedRanges
			}
			specs = append(specs, grepPathSpec{
				original: entry,
				clean:    base,
				ranges:   ranges,
				isURL:    true,
			})
			continue
		}
		if isStrictExternalURL(entry) {
			return nil, fmt.Errorf("Cannot search %s: external URL grep is not supported (fetch with `read` instead)", entry)
		}
		if isRootOnlyPath(entry) {
			return nil, fmt.Errorf("Searching from root directory '/' is not allowed")
		}
		clean, sel, literal := splitHostPathAndSelectorPreferLiteral(entry, cwd)
		var ranges []lineRange
		if !literal && sel != "" {
			parsedRanges, ok := parseLineRanges(sel)
			if !ok {
				return nil, fmt.Errorf(`path entry %q — only line-range selectors like ":50-100" are supported (no ":raw"/":conflicts")`, entry)
			}
			if hasGlobChars(clean) && probeLiteralPathExists(clean, cwd) == "missing" {
				return nil, fmt.Errorf("Line-range selector requires a single file, not a glob: %s", entry)
			}
			ranges = parsedRanges
		}
		specs = append(specs, grepPathSpec{
			original:               entry,
			clean:                  clean,
			ranges:                 ranges,
			literalFilesystemMatch: literal,
		})
	}

	// Pre-resolve archive-member selectors (e.g. `bundle.zip:src/foo.ts`) into
	// temporary scratch files so the rest of grep handles them uniformly.
	// upstream: packages/coding-agent/src/tools/grep.ts:298-374,722-740
	archiveRes, err := resolveArchiveSearchPaths(specs, cwd)
	if err != nil {
		return nil, err
	}
	defer archiveRes.cleanup()
	if len(archiveRes.unreadable) > 0 && len(archiveRes.resolvedPaths) == len(archiveRes.unreadable) {
		return nil, fmt.Errorf(
			"Cannot search archive member(s): %s. Read the member with `read <archive>:<member>` and inspect the returned text, or pass a UTF-8 text member.",
			strings.Join(archiveRes.unreadable, ", "),
		)
	}

	type resolvedTarget struct {
		spec      grepPathSpec
		parsed    parsedGlobTarget
		urlNode   *urlNode // non-nil when parsed.isURL
		isDir     bool
		missing   bool
		hostSize  int64
		immutable bool
	}

	var targets []resolvedTarget
	var missingPaths []string
	rangesByKey := make(map[string][]lineRange)

	for idx, spec := range specs {
		resolvedPath := archiveRes.resolvedPaths[idx]
		parsed, parseErr := parseSearchTarget(resolvedPath, cwd)
		if parseErr != nil {
			return nil, parseErr
		}
		if !parsed.isURL && isRootOnlyPath(parsed.basePath) {
			return nil, fmt.Errorf("Searching from root directory '/' is not allowed")
		}
		if parsed.isURL {
			node, rErr := urlfs.resolve(parsed.basePath)
			if rErr != nil {
				if len(specs) == 1 {
					return nil, fmt.Errorf("Cannot search %s: %w", spec.clean, rErr)
				}
				missingPaths = append(missingPaths, spec.clean)
				targets = append(targets, resolvedTarget{spec: spec, parsed: parsed, missing: true})
				continue
			}
			isDir := node.kind == urlNodeHostDir || node.kind == urlNodeVirtualDir
			if len(spec.ranges) > 0 {
				if isDir {
					return nil, fmt.Errorf("Line-range selector requires a single file: %s is a directory", spec.original)
				}
				rangesByKey[node.url] = append(rangesByKey[node.url], spec.ranges...)
			}
			targets = append(targets, resolvedTarget{
				spec:      spec,
				parsed:    parsed,
				urlNode:   node,
				isDir:     isDir,
				hostSize:  node.size,
				immutable: node.immutable,
			})
			continue
		}

		info, statErr := os.Stat(parsed.basePath)
		if statErr != nil {
			if len(spec.ranges) > 0 {
				return nil, fmt.Errorf("Path not found for line-range selector: %s", spec.original)
			}
			missingPaths = append(missingPaths, resolvedPath)
			targets = append(targets, resolvedTarget{spec: spec, parsed: parsed, missing: true})
			continue
		}
		isDir := info.IsDir()
		if len(spec.ranges) > 0 {
			if isDir {
				return nil, fmt.Errorf("Line-range selector requires a single file: %s is a directory", spec.original)
			}
			rangesByKey[parsed.basePath] = append(rangesByKey[parsed.basePath], spec.ranges...)
		}
		targets = append(targets, resolvedTarget{
			spec:     spec,
			parsed:   parsed,
			isDir:    isDir,
			hostSize: info.Size(),
		})
	}

	var validTargets []resolvedTarget
	for _, t := range targets {
		if !t.missing {
			validTargets = append(validTargets, t)
		}
	}
	if len(validTargets) == 0 {
		// Single missing path emits bare `Path not found: <scopePath>` (path-utils.ts:1655-1656);
		// all-missing multi-path list adds the `; list each target` hint (grep.ts:804-806).
		if len(specs) == 1 && len(archiveRes.unreadable) == 0 {
			return nil, fmt.Errorf("Path not found: %s", formatRelToCwd(resolvePath(missingPaths[0], cwd), cwd, false))
		}
		archiveHint := ""
		if len(archiveRes.unreadable) > 0 {
			archiveHint = fmt.Sprintf(" (archive members were not searchable: %s)", strings.Join(archiveRes.unreadable, ", "))
		}
		return nil, fmt.Errorf("Path not found: %s; list each target in the semicolon-delimited `path`%s", strings.Join(missingPaths, ", "), archiveHint)
	}

	// Scope path and searchPath for GrepToolDetails.
	var validDisplayInputs []string
	for _, t := range validTargets {
		if disp, ok := archiveRes.displayMap[t.parsed.basePath]; ok {
			if len(validTargets) == 1 {
				validDisplayInputs = append(validDisplayInputs, disp)
			} else {
				validDisplayInputs = append(validDisplayInputs, t.parsed.basePath)
			}
		} else {
			validDisplayInputs = append(validDisplayInputs, formatRelToCwd(t.parsed.basePath, cwd, false))
		}
	}
	scopePath := strings.Join(validDisplayInputs, ", ")
	if len(archiveRes.displayMap) == 0 {
		scopePath = toScopeDisplay(validDisplayInputs, cwd)
	}
	searchPath := cwd
	searchPathRaw := false
	if len(validTargets) == 1 {
		searchPath = validTargets[0].parsed.basePath
		if disp, ok := archiveRes.displayMap[searchPath]; ok {
			scopePath = disp
			searchPathRaw = true
		} else if validTargets[0].parsed.hasGlob {
			scopePath = formatRelToCwd(searchPath, cwd, false)
		}
	} else if len(validTargets) > 1 {
		// Mirror omp's common-ancestor searchPath when mixing scratch paths and cwd files:
		// omp standardizes session.cwd once at startup while keeping mkdtemp scratch paths raw.
		if len(archiveRes.displayMap) > 0 {
			basePaths := make([]string, len(validTargets))
			for i, t := range validTargets {
				if _, isArchive := archiveRes.displayMap[t.parsed.basePath]; isArchive {
					basePaths[i] = t.parsed.basePath
				} else {
					basePaths[i] = dirs.StandardizeMacOSPath(t.parsed.basePath)
				}
			}
			searchPath = commonAncestorPath(basePaths)
			searchPathRaw = true
		}
	}
	isMultiScope := len(validTargets) > 1 || validTargets[0].isDir
	perFileCap := grepSingleFilePerFileCap
	if isMultiScope {
		perFileCap = grepMultiFilePerFileCap
	}

	// Collect candidates deterministically.
	seenKeys := make(map[string]struct{})
	var candidates []grepCandidate

	for _, t := range validTargets {
		excludes, exErr := rebaseExclusionsForRoot(rawExclusions, cwd, t.parsed.basePath, t.parsed.isURL)
		if exErr != nil {
			return nil, exErr
		}
		if !t.isDir {
			key := t.parsed.basePath
			if t.urlNode != nil {
				key = t.urlNode.url
			}
			if _, ok := seenKeys[key]; ok {
				continue
			}
			seenKeys[key] = struct{}{}
			relDisplay := formatRelToCwd(key, cwd, false)
			immutable := t.immutable
			if disp, ok := archiveRes.displayMap[key]; ok {
				relDisplay = disp
				immutable = true
			}
			cand := grepCandidate{
				key:       key,
				relPath:   relDisplay,
				size:      t.hostSize,
				immutable: immutable,
				namedFile: true,
			}
			if t.urlNode != nil {
				cand.relPath = t.urlNode.url
				cand.hostPath = t.urlNode.hostPath
				if t.urlNode.kind == urlNodeVirtualFile {
					cand.content = t.urlNode.content
				}
			} else {
				cand.hostPath = t.parsed.basePath
			}
			candidates = append(candidates, cand)
			continue
		}

		var includeMatcher *globMatcher
		if t.parsed.hasGlob {
			m, err := compileGrepScopeGlob(t.parsed.globPattern, t.parsed.bareGlob)
			if err != nil {
				return nil, fmt.Errorf("Invalid glob pattern: %w", err)
			}
			includeMatcher = m
		}

		// Virtual URL directory container.
		if t.urlNode != nil && t.urlNode.kind == urlNodeVirtualDir {
			vEntries, vErr := urlfs.walkVirtualDir(bgCtx, t.urlNode, includeMatcher, excludes, t.parsed.recursive, false, false)
			if vErr != nil {
				return nil, vErr
			}
			for _, ve := range vEntries {
				if ve.isDir {
					continue
				}
				if _, ok := seenKeys[ve.url]; ok {
					continue
				}
				seenKeys[ve.url] = struct{}{}
				content := ve.content
				if content == nil {
					if res, err := urls.Resolve(urlCtx, ve.url); err == nil && res != nil {
						if res.Bytes != nil {
							content = res.Bytes
						} else {
							content = []byte(res.Content)
						}
					}
				}
				candidates = append(candidates, grepCandidate{
					key:       ve.url,
					relPath:   ve.url,
					content:   content,
					size:      int64(len(content)),
					immutable: t.immutable,
				})
			}
			continue
		}

		// Host directory (either local path or file-backed internal URL directory).
		walkRoot := t.parsed.basePath
		followLinks := false
		if t.urlNode != nil {
			walkRoot = t.urlNode.hostPath
			followLinks = isSkillURL(t.urlNode.url)
		}
		includeNodeModules := pathMentionsSegment(t.parsed.basePath, "node_modules") ||
			pathMentionsSegment(t.parsed.globPattern, "node_modules")
		entries, wErr := walkDir(bgCtx, walkRoot, walkOptions{
			hidden:             true,
			gitignore:          useGitignore,
			recursive:          t.parsed.recursive,
			followLinks:        followLinks,
			excludes:           excludes,
			include:            includeMatcher,
			includeNodeModules: includeNodeModules,
		})
		if wErr != nil {
			if bgCtx.Err() == context.DeadlineExceeded {
				return nil, fmt.Errorf("Grep timed out after %ds; narrow paths or pattern, or scope with `glob` first", int(grepDefaultTimeout.Seconds()))
			}
			return nil, wErr
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].relPath < entries[j].relPath
		})
		for _, e := range entries {
			if e.isDir {
				continue
			}
			key := e.absPath
			relDisplay := formatRelToCwd(e.absPath, cwd, false)
			if t.urlNode != nil {
				key = resolveURLHostRelative(t.urlNode.url, walkRoot, e.absPath, false)
				relDisplay = key
			}
			if _, ok := seenKeys[key]; ok {
				continue
			}
			seenKeys[key] = struct{}{}
			candidates = append(candidates, grepCandidate{
				key:       key,
				relPath:   relDisplay,
				hostPath:  e.absPath,
				size:      e.size,
				immutable: t.immutable,
			})
		}
	}

	// Search candidates in ordered chunks in parallel and stop after the chunk in
	// which the running match total reaches grepInternalTotalCap (matching
	// crates/xfs/src/grep/mod.rs:209-211).
	const grepChunkSize = 256
	type candOutcome struct {
		matches  []grepMatch
		isBinary bool
	}

	var allMatches []grepMatch
	var binaryMatches []string
	seenMatchKeys := make(map[string]struct{})
	totalCapReached := false
	fetchCap := perFileCap + 1
	maxWorkers := max(1, runtime.GOMAXPROCS(0))

	for chunkStart := 0; chunkStart < len(candidates) && !totalCapReached; chunkStart += grepChunkSize {
		if bgCtx.Err() != nil {
			if bgCtx.Err() == context.DeadlineExceeded {
				return nil, fmt.Errorf("Grep timed out after %ds; narrow paths or pattern, or scope with `glob` first", int(grepDefaultTimeout.Seconds()))
			}
			return nil, bgCtx.Err()
		}
		chunkEnd := min(chunkStart+grepChunkSize, len(candidates))
		chunk := candidates[chunkStart:chunkEnd]
		outcomes := make([]candOutcome, len(chunk))
		workers := min(maxWorkers, len(chunk))
		if len(chunk) <= 4 {
			workers = 1
		}
		if workers == 1 {
			var scratch []byte
			for i, cand := range chunk {
				if bgCtx.Err() != nil {
					if bgCtx.Err() == context.DeadlineExceeded {
						return nil, fmt.Errorf("Grep timed out after %ds; narrow paths or pattern, or scope with `glob` first", int(grepDefaultTimeout.Seconds()))
					}
					return nil, bgCtx.Err()
				}
				fm, isBin := searchCandidateWithContext(cand, re, multiline, rangesByKey[cand.key], fetchCap, contextBefore, contextAfter, &scratch)
				outcomes[i] = candOutcome{matches: fm, isBinary: isBin}
			}
		} else {
			jobs := make(chan int, len(chunk))
			for i := range chunk {
				jobs <- i
			}
			close(jobs)
			var wg sync.WaitGroup
			for range workers {
				wg.Add(1)
				go func() {
					defer wg.Done()
					var scratch []byte
					for i := range jobs {
						if bgCtx.Err() != nil {
							return
						}
						cand := chunk[i]
						fm, isBin := searchCandidateWithContext(cand, re, multiline, rangesByKey[cand.key], fetchCap, contextBefore, contextAfter, &scratch)
						outcomes[i] = candOutcome{matches: fm, isBinary: isBin}
					}
				}()
			}
			wg.Wait()
			if bgCtx.Err() != nil {
				if bgCtx.Err() == context.DeadlineExceeded {
					return nil, fmt.Errorf("Grep timed out after %ds; narrow paths or pattern, or scope with `glob` first", int(grepDefaultTimeout.Seconds()))
				}
				return nil, bgCtx.Err()
			}
		}

		for i, cand := range chunk {
			out := outcomes[i]
			if out.isBinary {
				binaryMatches = append(binaryMatches, cand.relPath)
			}
			hasRanges := len(rangesByKey[cand.key]) > 0
			for _, m := range out.matches {
				mk := m.key + "\x00" + strconv.Itoa(m.lineNumber)
				if _, dup := seenMatchKeys[mk]; dup {
					continue
				}
				seenMatchKeys[mk] = struct{}{}
				allMatches = append(allMatches, m)
				if len(allMatches) >= grepInternalTotalCap && !hasRanges {
					totalCapReached = true
					break
				}
			}
			if totalCapReached {
				break
			}
		}
	}

	// Group matches by file in encounter order, applying pagination and per-file cap.
	// upstream: packages/coding-agent/src/tools/grep.ts:1082-1142
	type fileGroup struct {
		key       string
		relPath   string
		hostPath  string
		immutable bool
		count     int
		list      []grepMatch
	}
	groupIndex := make(map[string]int)
	var groups []*fileGroup
	var windowGroups []*fileGroup
	var cappedFiles []string

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
			g := &fileGroup{
				key:       m.key,
				relPath:   m.relPath,
				hostPath:  m.hostPath,
				immutable: m.immutable,
				count:     1,
			}
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
			if g.count == perFileCap+1 && g.list != nil {
				cappedFiles = append(cappedFiles, g.relPath)
			}
			continue
		}
		if g.list != nil {
			g.list = append(g.list, m)
		}
	}

	totalFiles := len(groups)
	totalFilesLabel := strconv.Itoa(totalFiles)
	if totalCapReached {
		totalFilesLabel = strconv.Itoa(totalFiles) + "+"
	}
	skipFiles := 0
	if canPaginate {
		skipFiles = min(skip, totalFiles)
	}
	fileLimitReached := canPaginate && totalFiles > skipFiles+grepDefaultFileLimit
	nextSkip := skipFiles + len(windowGroups)

	// Build warning notes matching grep.ts:1151-1203.
	var oversizedFiles []string
	if multiline {
		for _, t := range validTargets {
			if !t.isDir && t.hostSize > grepMaxFileBytes {
				oversizedFiles = append(oversizedFiles, formatRelToCwd(t.parsed.basePath, cwd, false))
			}
		}
	}
	limitMB := grepMaxFileBytes / (1024 * 1024)
	var oversizedNote string
	if len(oversizedFiles) > 0 {
		oversizedNote = fmt.Sprintf("Multiline search read only the first %dMB of large files (matches past the %dMB window are not shown; search without a newline in the pattern, or use `read`, for the rest): %s", limitMB, limitMB, strings.Join(oversizedFiles, ", "))
	}
	var binaryMatchNote string
	if len(binaryMatches) > 0 {
		binaryMatchNote = fmt.Sprintf("Binary files also match (name one as `path` to search it as text): %s", formatCappedFiles(binaryMatches))
	}
	var archiveNote string
	if len(archiveRes.unreadable) > 0 {
		archiveNote = "Skipped archive entries (search supports text members only): " + strings.Join(archiveRes.unreadable, ", ")
	}
	// Suppress entries already explained via archiveNote (upstream: grep.ts:1194-1199).
	var missingPathsForNote []string
	for _, mp := range missingPaths {
		if _, explained := archiveRes.unreadableSet[mp]; !explained {
			missingPathsForNote = append(missingPathsForNote, mp)
		}
	}
	var missingPathsNote string
	if len(missingPathsForNote) > 0 {
		missingPathsNote = "Skipped missing paths: " + strings.Join(missingPathsForNote, ", ")
	}
	var warningParts []string
	for _, note := range []string{archiveNote, missingPathsNote, oversizedNote, binaryMatchNote} {
		if note != "" {
			warningParts = append(warningParts, note)
		}
	}
	warningNote := strings.Join(warningParts, "\n")

	if len(windowGroups) == 0 {
		skipPastEnd := canPaginate && skip > 0 && totalFiles > 0 && skipFiles >= totalFiles
		var noMatchText string
		switch {
		case skipPastEnd:
			noMatchText = fmt.Sprintf("No more results (%s files total; skip=%d is past the end)", totalFilesLabel, skip)
		case oversizedNote != "":
			noMatchText = fmt.Sprintf("No matches in searched content (multiline search limited large files to their first %dMB)", limitMB)
		case binaryMatchNote != "":
			noMatchText = "No matches in text files"
		default:
			noMatchText = "No matches found"
		}

		// Gitignored probe: when a single directory search with gitignore=true found nothing,
		// check if gitignored files would have matched.
		// upstream: packages/coding-agent/src/tools/grep.ts:1224-1257
		var ignoredNote string
		if useGitignore && len(validTargets) == 1 && validTargets[0].isDir && !skipPastEnd &&
			(validTargets[0].urlNode == nil || validTargets[0].urlNode.kind == urlNodeHostDir) {
			if example := probeGitignoredMatch(bgCtx, cwd, validTargets[0].parsed, validTargets[0].urlNode, rawExclusions, re, multiline); example != "" {
				ignoredNote = fmt.Sprintf("Gitignored files match (e.g. %s); pass `gitignore: false` to search them.", example)
			}
		}

		var parts []string
		for _, p := range []string{noMatchText, ignoredNote, warningNote} {
			if p != "" {
				parts = append(parts, p)
			}
		}
		details := map[string]any{
			"scopePath":  scopePath,
			"searchPath": dirs.StandardizeMacOSPath(searchPath),
			"cwd":        dirs.StandardizeMacOSPath(cwd),
			"matchCount": 0,
			"fileCount":  0,
			"files":      []string{},
			"truncated":  false,
		}
		if len(binaryMatches) > 0 {
			details["binaryMatches"] = binaryMatches
		}
		if len(missingPaths) > 0 {
			details["missingPaths"] = missingPaths
		}
		return sdk.ToolResult{
			Content: strings.Join(parts, "\n"),
			Details: details,
		}, nil
	}

	fileList := make([]string, len(windowGroups))
	groupByRel := make(map[string]*fileGroup, len(windowGroups))
	fileMatchCounts := make(map[string]int, len(windowGroups))
	fileMatchesDetail := make([]map[string]any, len(windowGroups))
	totalSelected := 0
	for i, g := range windowGroups {
		fileList[i] = g.relPath
		groupByRel[g.relPath] = g
		fileMatchCounts[g.relPath] = len(g.list)
		fileMatchesDetail[i] = map[string]any{
			"path":  g.relPath,
			"count": len(g.list),
		}
		totalSelected += len(g.list)
	}

	// Per rendered row, record the 1-based line number (0 for ellipsis/cut rows) so
	// post-truncation seen-line recording only records rows whose output index < keptLines.
	type seenSection struct {
		relPath  string
		hostPath string
		rowLines []int // parallel to modelOut; 0 means non-anchorable (ellipsis or cut)
	}
	var seenSections []seenSection

	linesTruncated := false
	matchOutput := formatFileMatches(fileList, isMultiScope, func(relPath string) fileMatchSection {
		g := groupByRel[relPath]
		var tag string
		var snapPath string
		// Notebook JSON lines never anchor an edit (upstream: grep.ts:1278-1281).
		if !g.immutable && !strings.HasSuffix(strings.ToLower(relPath), ".ipynb") {
			snapPath = urlfs.snapshotHostPath(g.relPath, cwd)
			if snapPath == "" && g.hostPath != "" && !isRegisteredURL(g.relPath) {
				snapPath = g.hostPath
			}
		}
		if snapPath != "" {
			tag = snapshotTagForFileOnly(snapPath)
		}
		useHash := tag != ""

		// Compute line number width for display code frame gutter.
		lineNumWidth := 1
		for _, m := range g.list {
			rows := strings.Split(m.line, "\n")
			lastNum := m.lineNumber + len(rows) - 1
			if w := len(strconv.Itoa(lastNum)); w > lineNumWidth {
				lineNumWidth = w
			}
			for _, c := range m.contextBefore {
				if w := len(strconv.Itoa(c.lineNumber)); w > lineNumWidth {
					lineNumWidth = w
				}
			}
			for _, c := range m.contextAfter {
				if w := len(strconv.Itoa(c.lineNumber)); w > lineNumWidth {
					lineNumWidth = w
				}
			}
		}
		gutterPad := strings.Repeat(" ", lineNumWidth+1)

		var modelOut []string
		var displayOut []string
		var rowLines []int
		lastLine := 0
		pushLine := func(lineNum int, rowText string, isMatch bool, recordSeen bool) {
			if lastLine > 0 && lineNum <= lastLine {
				// Overlapping context/match line already emitted; skip duplicate context.
				if !isMatch {
					return
				}
			}
			cleanRow := sanitizeText(rowText)
			if lastLine > 0 && lineNum > lastLine+1 {
				modelOut = append(modelOut, "...")
				displayOut = append(displayOut, gutterPad+"│...")
				rowLines = append(rowLines, 0)
			}
			marker := " "
			if isMatch {
				marker = "*"
			}
			if useHash {
				modelOut = append(modelOut, fmt.Sprintf("%s%d:%s", marker, lineNum, cleanRow))
			} else {
				modelOut = append(modelOut, fmt.Sprintf("%s%d|%s", marker, lineNum, cleanRow))
			}
			displayOut = append(displayOut, formatCodeFrameLine(marker, lineNum, cleanRow, lineNumWidth))
			if recordSeen {
				rowLines = append(rowLines, lineNum)
			} else {
				rowLines = append(rowLines, 0)
			}
			lastLine = lineNum
		}
		for _, m := range g.list {
			ranges := rangesByKey[m.key]
			for _, c := range m.contextBefore {
				if len(ranges) > 0 && !lineInRanges(c.lineNumber, ranges) {
					continue
				}
				if c.truncated {
					linesTruncated = true
				}
				pushLine(c.lineNumber, c.line, false, !c.truncated)
			}
			rows := strings.Split(m.line, "\n")
			if m.truncated {
				linesTruncated = true
				if m.column > 0 && len(rows) > 0 {
					rows[0] = fmt.Sprintf("%s [col %d]", rows[0], m.column)
				}
			}
			for rIdx, rowText := range rows {
				lineNum := m.lineNumber + rIdx
				if len(ranges) > 0 && !lineInRanges(lineNum, ranges) {
					continue
				}
				seen := !m.truncated || (rIdx > 0 && rIdx < len(rows)-1)
				pushLine(lineNum, rowText, rIdx == 0, seen)
			}
			for _, c := range m.contextAfter {
				if len(ranges) > 0 && !lineInRanges(c.lineNumber, ranges) {
					continue
				}
				if c.truncated {
					linesTruncated = true
				}
				pushLine(c.lineNumber, c.line, false, !c.truncated)
			}
		}
		if snapPath != "" && tag != "" {
			seenSections = append(seenSections, seenSection{
				relPath:  relPath,
				hostPath: snapPath,
				rowLines: rowLines,
			})
		}
		return fileMatchSection{model: modelOut, display: displayOut, tag: tag}
	})

	// Build trailer (page limit, warnings, per-file cap notice) kept outside the byte cap.
	// upstream: packages/coding-agent/src/tools/grep.ts:1391-1430
	limitMessage := ""
	if fileLimitReached {
		limitMessage = fmt.Sprintf("Showing files %d-%d of %s. Use skip=%d for the next page, or narrow paths/pattern.", skipFiles+1, nextSkip, totalFilesLabel, nextSkip)
	}
	var cappedCandidates []string
	for _, rel := range cappedFiles {
		if fileMatchCounts[rel] == perFileCap {
			cappedCandidates = append(cappedCandidates, rel)
		}
	}
	buildTrailer := func(shownCapped []string) string {
		var capNotice string
		if len(shownCapped) > 0 {
			if isMultiScope {
				capNotice = fmt.Sprintf("More than %d matches in %s; grep keeps the first %d per file when searching several. Search one file alone for up to %d.",
					perFileCap, formatCappedFiles(shownCapped), perFileCap, grepSingleFilePerFileCap)
			} else {
				capNotice = fmt.Sprintf("More than %d matches; grep keeps the first %d in a file. Add a line range (`path:N-M`) or narrow the pattern for the rest.",
					perFileCap, perFileCap)
			}
		}
		var b strings.Builder
		for _, note := range []string{limitMessage, warningNote, capNotice} {
			if note != "" {
				b.WriteString("\n\n")
				b.WriteString(note)
			}
		}
		return b.String()
	}

	rawOutput := strings.Join(matchOutput.model, "\n")
	suffix := buildTrailer(cappedCandidates)
	var content string
	var byteTruncated bool
	var keptLines int
	for {
		reserved := len(suffix)
		budget := grepMaxOutputBytes - reserved
		if budget < 0 {
			budget = 0
		}
		content, keptLines, byteTruncated = truncateHeadLines(matchOutput.model, budget)
		shownCapped := cappedCandidates
		if isMultiScope {
			var surviving []string
			for _, rel := range cappedCandidates {
				if end, ok := matchOutput.modelEnds[rel]; ok && end <= keptLines {
					surviving = append(surviving, rel)
				}
			}
			shownCapped = surviving
		}
		nextSuffix := buildTrailer(shownCapped)
		if len(nextSuffix) <= reserved {
			suffix = nextSuffix
			break
		}
		suffix = nextSuffix
	}

	// Record ONLY rows whose index survived the 50 KB head cap (upstream: grep.ts:1432-1446).
	// When no full line survived (only header kept or every shown row was column-cut),
	// record []int{0} (NO_FULL_LINE) so provenance is present without marking the file
	// authored/unrestricted (upstream: edit/store.ts:22-41, grep.ts:1440-1443).
	for _, sec := range seenSections {
		end, ok := matchOutput.modelEnds[sec.relPath]
		if !ok {
			continue
		}
		firstRow := end - len(sec.rowLines)
		if keptLines < firstRow {
			continue // dropped with its header
		}
		keptRowCount := min(len(sec.rowLines), max(0, keptLines-firstRow))
		var keptSeen []int
		for i := range keptRowCount {
			if ln := sec.rowLines[i]; ln > 0 {
				keptSeen = append(keptSeen, ln)
			}
		}
		if len(keptSeen) == 0 {
			keptSeen = []int{0}
		}
		snapshotTagForFile(editStore, sec.hostPath, keptSeen)
	}

	output := content + suffix

	// Append output-meta bracket notice matching packages/tui/src/tools/output-meta.ts:314-327,370-425.
	var metaNotices []string
	if byteTruncated {
		metaNotices = append(metaNotices, fmt.Sprintf("Showing lines 1-%d of %d (%s limit)", keptLines, len(matchOutput.model), formatBytesCompact(grepMaxOutputBytes)))
	}
	if linesTruncated {
		metaNotices = append(metaNotices, fmt.Sprintf("Some lines truncated to %d bytes", grepDefaultMaxColumn))
	}
	if len(metaNotices) > 0 {
		output += "\n\n[" + strings.Join(metaNotices, ". ") + "]"
	}

	// Resolve displayTargets for internal URL headers in display output.
	var displayTargets map[string]string
	for _, line := range matchOutput.display {
		if !strings.HasPrefix(line, "#") {
			continue
		}
		trimmed := strings.TrimLeft(line, "#")
		trimmed = strings.TrimSpace(trimmed)
		if idx := strings.LastIndexByte(trimmed, '#'); idx > 0 && len(trimmed)-idx == 5 {
			trimmed = trimmed[:idx]
		}
		if isRegisteredURL(trimmed) {
			if target := urlfs.displayTarget(trimmed); target != "" {
				if displayTargets == nil {
					displayTargets = make(map[string]string)
				}
				displayTargets[trimmed] = target
			}
		}
	}

	truncated := fileLimitReached || perFileLimitReached || totalCapReached || byteTruncated || linesTruncated
	displaySearchPath := dirs.StandardizeMacOSPath(searchPath)
	if searchPathRaw {
		displaySearchPath = searchPath
	}
	details := map[string]any{
		"scopePath":      scopePath,
		"searchPath":     displaySearchPath,
		"cwd":            dirs.StandardizeMacOSPath(cwd),
		"matchCount":     totalSelected,
		"fileCount":      len(fileList),
		"files":          fileList,
		"fileMatches":    fileMatchesDetail,
		"truncated":      truncated,
		"displayContent": strings.Join(matchOutput.display, "\n"),
	}
	if fileLimitReached {
		details["fileLimitReached"] = grepDefaultFileLimit
	}
	if perFileLimitReached {
		details["perFileLimitReached"] = perFileCap
	}
	if len(binaryMatches) > 0 {
		details["binaryMatches"] = binaryMatches
	}
	if len(missingPaths) > 0 {
		details["missingPaths"] = missingPaths
	}
	if len(displayTargets) > 0 {
		details["displayTargets"] = displayTargets
	}
	if linesTruncated {
		details["linesTruncated"] = true
	}
	meta := map[string]any{}
	if linesTruncated {
		meta["limits"] = map[string]any{
			"columnTruncated": map[string]any{
				"maxColumn": grepDefaultMaxColumn,
				"unit":      "bytes",
			},
		}
	}
	if byteTruncated {
		truncInfo := map[string]any{
			"truncated":   true,
			"truncatedBy": "bytes",
			"outputLines": keptLines,
			"totalLines":  len(matchOutput.model),
			"outputBytes": len(content),
			"totalBytes":  len(rawOutput),
			"maxBytes":    grepMaxOutputBytes,
		}
		details["truncation"] = truncInfo
		meta["truncation"] = map[string]any{
			"direction":   "head",
			"truncatedBy": "bytes",
			"outputLines": keptLines,
			"totalLines":  len(matchOutput.model),
			"outputBytes": len(content),
			"totalBytes":  len(rawOutput),
			"maxBytes":    grepMaxOutputBytes,
			"shownRange":  map[string]any{"start": 1, "end": keptLines},
		}
	}
	if len(meta) > 0 {
		details["meta"] = meta
	}

	return sdk.ToolResult{
		Content: output,
		Details: details,
	}, nil
}

// formatCodeFrameLine mirrors packages/tui/src/render/render-utils.ts:468-478.
func formatCodeFrameLine(marker string, lineNumber int, content string, lineNumberWidth int) string {
	markerText := strings.TrimSpace(marker)
	numText := strconv.Itoa(lineNumber)
	gutter := numText
	if markerText != "" {
		gutter = markerText + numText
	}
	width := lineNumberWidth + 1
	if len(gutter) < width {
		gutter = strings.Repeat(" ", width-len(gutter)) + gutter
	}
	return gutter + "│" + content
}

// formatCappedFiles mirrors packages/coding-agent/src/tools/grep.ts:468-472.
func formatCappedFiles(paths []string) string {
	if len(paths) <= 5 {
		return strings.Join(paths, ", ")
	}
	shown := strings.Join(paths[:5], ", ")
	rest := len(paths) - 5
	noun := "files"
	if rest == 1 {
		noun = "file"
	}
	return fmt.Sprintf("%s and %d more %s", shown, rest, noun)
}

func truncateHeadLines(lines []string, maxBytes int) (content string, kept int, truncated bool) {
	if len(lines) == 0 {
		return "", 0, false
	}
	var b strings.Builder
	for i, line := range lines {
		need := len(line)
		if i > 0 {
			need++
		}
		if b.Len()+need > maxBytes {
			return b.String(), i, true
		}
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
	}
	return b.String(), len(lines), false
}

func probeGitignoredMatch(
	parentCtx context.Context,
	cwd string,
	parsed parsedGlobTarget,
	node *urlNode,
	rawExclusions []string,
	re *regexp.Regexp,
	multiline bool,
) string {
	probeCtx, cancel := context.WithTimeout(parentCtx, grepGitignoreProbeBudget)
	defer cancel()

	walkRoot := parsed.basePath
	followLinks := false
	if node != nil {
		walkRoot = node.hostPath
		followLinks = isSkillURL(node.url)
	}
	excludes, err := rebaseExclusionsForRoot(rawExclusions, cwd, parsed.basePath, parsed.isURL)
	if err != nil {
		return ""
	}
	var includeMatcher *globMatcher
	if parsed.hasGlob {
		m, err := compileGrepScopeGlob(parsed.globPattern, parsed.bareGlob)
		if err != nil {
			return ""
		}
		includeMatcher = m
	}
	includeNodeModules := pathMentionsSegment(parsed.basePath, "node_modules") ||
		pathMentionsSegment(parsed.globPattern, "node_modules")
	entries, err := walkDir(probeCtx, walkRoot, walkOptions{
		hidden:             true,
		gitignore:          false,
		recursive:          parsed.recursive,
		followLinks:        followLinks,
		excludes:           excludes,
		include:            includeMatcher,
		includeNodeModules: includeNodeModules,
	})
	if err != nil {
		return ""
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].relPath < entries[j].relPath
	})
	var scratch []byte
	for _, e := range entries {
		if e.isDir || probeCtx.Err() != nil {
			continue
		}
		relDisplay := formatRelToCwd(e.absPath, cwd, false)
		if node != nil {
			relDisplay = resolveURLHostRelative(node.url, walkRoot, e.absPath, false)
		}
		cand := grepCandidate{
			key:      e.absPath,
			relPath:  relDisplay,
			hostPath: e.absPath,
			size:     e.size,
		}
		matches, _ := searchCandidate(cand, re, multiline, nil, 1, &scratch)
		if len(matches) > 0 {
			return relDisplay
		}
	}
	return ""
}

func grepContextParam(params map[string]any, keys ...string) (int, bool) {
	for _, k := range keys {
		if raw, exists := params[k]; exists && raw != nil {
			if v, ok := floatFromAny(raw); ok && v >= 0 {
				return int(math.Floor(v)), true
			}
		}
	}
	return 0, false
}

func searchCandidate(cand grepCandidate, re *regexp.Regexp, multiline bool, ranges []lineRange, maxMatches int, scratch *[]byte) ([]grepMatch, bool) {
	return searchCandidateWithContext(cand, re, multiline, ranges, maxMatches, 0, 0, scratch)
}

func searchCandidateWithContext(cand grepCandidate, re *regexp.Regexp, multiline bool, ranges []lineRange, maxMatches, contextBefore, contextAfter int, scratch *[]byte) ([]grepMatch, bool) {
	var data []byte
	if cand.content != nil {
		data = cand.content
		if multiline && len(data) > grepMaxFileBytes {
			data = data[:grepMaxFileBytes]
		}
	} else {
		f, err := os.Open(cand.hostPath)
		if err != nil {
			return nil, false
		}
		defer f.Close()

		limit := grepMaxFileBytes
		if !multiline && cand.namedFile && cand.size > int64(limit) {
			limit = 64 * 1024 * 1024
		}
		// Size buffer from candidate stat size and reuse scratch per worker so
		// small files never trigger a 4 MiB allocation.
		want := limit
		if cand.size > 0 && cand.size < int64(want) {
			want = int(cand.size) + 1
		} else if cand.size == 0 {
			if info, statErr := f.Stat(); statErr == nil && info.Mode().IsRegular() {
				if info.Size() == 0 {
					return nil, false
				}
				if info.Size() < int64(want) {
					want = int(info.Size()) + 1
				}
			}
		}
		var buf []byte
		if scratch != nil {
			if cap(*scratch) < want {
				*scratch = make([]byte, want)
			}
			buf = (*scratch)[:want]
		} else {
			buf = make([]byte, want)
		}
		n := 0
		for n < len(buf) {
			k, readErr := f.Read(buf[n:])
			n += k
			if readErr != nil {
				break
			}
			if k == 0 {
				break
			}
		}
		data = buf[:n]
	}
	if len(data) == 0 {
		return nil, false
	}

	// Binary check: NUL in first 64KB (unless file was explicitly named).
	// upstream: crates/xfs/src/grep/scan.rs:24-28
	if !cand.namedFile {
		probeLen := min(len(data), grepBinaryProbeBytes)
		if bytes.IndexByte(data[:probeLen], 0) != -1 {
			if re.Match(data) {
				return nil, true
			}
			return nil, false
		}
	}

	var matches []grepMatch
	if multiline {
		locs := re.FindAllIndex(data, maxMatches*4)
		if len(locs) == 0 {
			return nil, false
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
			blockStart := bytes.LastIndexByte(data[:loc[0]], '\n') + 1
			matchEnd := loc[1]
			if matchEnd > blockStart && matchEnd <= len(data) && data[matchEnd-1] == '\n' {
				matchEnd--
			}
			endOff := bytes.IndexByte(data[matchEnd:], '\n')
			var blockBytes []byte
			if endOff == -1 {
				blockBytes = data[blockStart:]
			} else {
				blockBytes = data[blockStart : matchEnd+endOff]
			}
			blockStr := strings.TrimRight(strings.ReplaceAll(string(blockBytes), "\r\n", "\n"), " \t\r")
			relStart := loc[0] - blockStart
			relEnd := min(loc[1]-blockStart, len(blockStr))
			rendered, trunc, col := windowMatchedLine(blockStr, grepDefaultMaxColumn, relStart, relEnd)
			matches = append(matches, grepMatch{
				key:        cand.key,
				relPath:    cand.relPath,
				hostPath:   cand.hostPath,
				immutable:  cand.immutable,
				lineNumber: lineNum,
				line:       rendered,
				truncated:  trunc,
				column:     col,
			})
			if len(matches) >= maxMatches {
				break
			}
		}
		if contextBefore > 0 || contextAfter > 0 {
			attachGrepContext(data, matches, ranges, contextBefore, contextAfter)
		}
		return matches, false
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
		if !cand.namedFile && bytes.IndexByte(lineBytes, 0) != -1 {
			// NUL past the 64KB probe window ends the scan, keeping prior matches.
			if re.Match(lineBytes) {
				return matches, true
			}
			break
		}
		if !lineInRanges(lineNum, ranges) {
			continue
		}
		loc := re.FindIndex(lineBytes)
		if loc == nil {
			continue
		}
		lineStr := strings.TrimRight(string(lineBytes), " \t\r")
		startOff := min(loc[0], len(lineStr))
		endOff := min(loc[1], len(lineStr))
		rendered, trunc, col := windowMatchedLine(lineStr, grepDefaultMaxColumn, startOff, endOff)
		matches = append(matches, grepMatch{
			key:        cand.key,
			relPath:    cand.relPath,
			hostPath:   cand.hostPath,
			immutable:  cand.immutable,
			lineNumber: lineNum,
			line:       rendered,
			truncated:  trunc,
			column:     col,
		})
		if len(matches) >= maxMatches {
			break
		}
	}
	if len(matches) > 0 && (contextBefore > 0 || contextAfter > 0) {
		attachGrepContext(data, matches, ranges, contextBefore, contextAfter)
	}
	return matches, false
}

// attachGrepContext populates contextBefore / contextAfter on each match from data,
// deduplicating overlapping context lines across adjacent matches and respecting
// active line-range filters (upstream: packages/coding-agent/src/tools/grep.ts:1038-1044).
func attachGrepContext(data []byte, matches []grepMatch, ranges []lineRange, contextBefore, contextAfter int) {
	if len(matches) == 0 || (contextBefore <= 0 && contextAfter <= 0) {
		return
	}
	allLines := splitDataLines(data)
	totalLines := len(allLines)
	if totalLines == 0 {
		return
	}
	if contextBefore > totalLines {
		contextBefore = totalLines
	}
	if contextAfter > totalLines {
		contextAfter = totalLines
	}
	matchLineSet := make(map[int]struct{}, len(matches))
	for _, m := range matches {
		span := strings.Count(m.line, "\n") + 1
		for off := range span {
			matchLineSet[m.lineNumber+off] = struct{}{}
		}
	}
	hint := min(totalLines, len(matches)*(contextBefore+contextAfter+1))
	emitted := make(map[int]struct{}, hint)
	for i := range matches {
		m := &matches[i]
		span := strings.Count(m.line, "\n") + 1
		if contextBefore > 0 {
			start := max(1, m.lineNumber-contextBefore)
			for ln := start; ln < m.lineNumber; ln++ {
				if _, isM := matchLineSet[ln]; isM {
					continue
				}
				if _, seen := emitted[ln]; seen {
					continue
				}
				if len(ranges) > 0 && !lineInRanges(ln, ranges) {
					continue
				}
				emitted[ln] = struct{}{}
				rendered, trunc := truncateContextLine(allLines[ln-1], grepDefaultMaxColumn)
				m.contextBefore = append(m.contextBefore, grepContextLine{
					lineNumber: ln,
					line:       rendered,
					truncated:  trunc,
				})
			}
		}
		if contextAfter > 0 {
			lastMatchLine := m.lineNumber + span - 1
			end := min(totalLines, lastMatchLine+contextAfter)
			for ln := lastMatchLine + 1; ln <= end; ln++ {
				if _, isM := matchLineSet[ln]; isM {
					continue
				}
				if _, seen := emitted[ln]; seen {
					continue
				}
				if len(ranges) > 0 && !lineInRanges(ln, ranges) {
					continue
				}
				emitted[ln] = struct{}{}
				rendered, trunc := truncateContextLine(allLines[ln-1], grepDefaultMaxColumn)
				m.contextAfter = append(m.contextAfter, grepContextLine{
					lineNumber: ln,
					line:       rendered,
					truncated:  trunc,
				})
			}
		}
	}
}

func splitDataLines(data []byte) []string {
	var lines []string
	pos := 0
	for pos < len(data) {
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
		lines = append(lines, strings.TrimRight(string(lineBytes), " \t\r"))
	}
	return lines
}

func truncateContextLine(line string, maxBytes int) (string, bool) {
	if maxBytes <= 0 || len(line) <= maxBytes {
		return line, false
	}
	cut := max(0, maxBytes-3)
	cut = floorRuneBoundary(line, cut)
	return line[:cut] + "...", true
}

// windowMatchedLine mirrors crates/xfs/src/grep/render.rs:42-75.
// Fits a matched line wider than maxBytes into maxBytes centered on its first match,
// returning the windowed string, whether it was truncated, and the 1-based rune column.
func windowMatchedLine(line string, maxBytes, matchStart, matchEnd int) (string, bool, int) {
	if len(line) <= maxBytes {
		return line, false, 0
	}
	headEnd := floorRuneBoundary(line, max(0, maxBytes-3))
	start := floorRuneBoundary(line, min(matchStart, len(line)))
	col := utf8.RuneCountInString(line[:start]) + 1
	body := max(0, maxBytes-6)
	if matchEnd <= headEnd || body == 0 {
		return line[:headEnd] + "...", true, col
	}
	matchLen := max(0, matchEnd-start)
	lead := max(0, body-matchLen) / 2
	from := max(0, start-lead)
	if from+body >= len(line) {
		cutFrom := ceilRuneBoundary(line, max(0, len(line)-max(0, maxBytes-3)))
		return "..." + line[cutFrom:], true, col
	}
	cutFrom := ceilRuneBoundary(line, from)
	cutTo := floorRuneBoundary(line, min(len(line), cutFrom+body))
	if cutFrom == 0 {
		return line[:cutTo] + "...", true, col
	}
	return "..." + line[cutFrom:cutTo] + "...", true, col
}

func floorRuneBoundary(s string, idx int) int {
	if idx >= len(s) {
		return len(s)
	}
	if idx <= 0 {
		return 0
	}
	for idx > 0 && !utf8.RuneStart(s[idx]) {
		idx--
	}
	return idx
}

func ceilRuneBoundary(s string, idx int) int {
	if idx <= 0 {
		return 0
	}
	if idx >= len(s) {
		return len(s)
	}
	for idx < len(s) && !utf8.RuneStart(s[idx]) {
		idx++
	}
	return idx
}

type archiveSearchResolution struct {
	resolvedPaths []string
	displayMap    map[string]string
	unreadable    []string
	unreadableSet map[string]struct{}
	cleanup       func()
}

// resolveArchiveSearchPaths pre-resolves `paths` entries pointing at archive
// members (e.g. `bundle.zip:src/foo.ts`, `release.tar.gz:notes.md`) by
// extracting each UTF-8 text member into a temporary scratch file and
// recording the scratch path -> original selector mapping.
// upstream: packages/coding-agent/src/tools/grep.ts:298-374
func resolveArchiveSearchPaths(specs []grepPathSpec, cwd string) (archiveSearchResolution, error) {
	resolvedPaths := make([]string, len(specs))
	for i, spec := range specs {
		resolvedPaths[i] = spec.clean
	}
	displayMap := make(map[string]string)
	unreadableSet := make(map[string]struct{})
	var unreadable []string
	var tempDir string

	cleanup := func() {
		if tempDir != "" {
			_ = os.RemoveAll(tempDir)
		}
	}

	reader := getArchiveReader()
	if reader == nil || reader.ParseCandidates == nil || reader.ReadMember == nil {
		return archiveSearchResolution{
			resolvedPaths: resolvedPaths,
			displayMap:    displayMap,
			unreadable:    unreadable,
			unreadableSet: unreadableSet,
			cleanup:       cleanup,
		}, nil
	}

	for idx, spec := range specs {
		if spec.literalFilesystemMatch || spec.isURL {
			continue
		}
		entry := spec.clean
		candidates := reader.ParseCandidates(entry)
		var member *ArchivePathCandidate
		for i := range candidates {
			if candidates[i].SubPath != "" && candidates[i].ArchivePath != entry {
				member = &candidates[i]
				break
			}
		}
		if member == nil {
			continue
		}

		archiveAbs := resolvePath(member.ArchivePath, cwd)
		data, openErr, readErr := reader.ReadMember(archiveAbs, member.SubPath)
		if openErr != nil {
			unreadable = append(unreadable, fmt.Sprintf("%s (cannot open archive: %s)", entry, openErr.Error()))
			unreadableSet[entry] = struct{}{}
			continue
		}
		if readErr != nil {
			unreadable = append(unreadable, fmt.Sprintf("%s (%s)", entry, readErr.Error()))
			unreadableSet[entry] = struct{}{}
			continue
		}
		// UTF-8 only — binary members would just produce noise through grep.
		if bytes.IndexByte(data, 0) >= 0 {
			unreadable = append(unreadable, fmt.Sprintf("%s (binary archive entry)", entry))
			unreadableSet[entry] = struct{}{}
			continue
		}
		if !utf8.Valid(data) {
			unreadable = append(unreadable, fmt.Sprintf("%s (non-UTF-8 archive entry)", entry))
			unreadableSet[entry] = struct{}{}
			continue
		}

		if tempDir == "" {
			var err error
			tempDir, err = os.MkdirTemp("", "omp-search-archive-")
			if err != nil {
				cleanup()
				return archiveSearchResolution{}, err
			}
			if realDir, err := filepath.EvalSymlinks(tempDir); err == nil {
				tempDir = realDir
			}
		}
		safeBase := sanitizeArchiveScratchBase(path.Base(strings.ReplaceAll(member.SubPath, "\\", "/")))
		tempPath := filepath.Join(tempDir, fmt.Sprintf("%d-%s", idx, safeBase))
		if err := os.WriteFile(tempPath, data, 0o600); err != nil {
			cleanup()
			return archiveSearchResolution{}, err
		}
		resolvedPaths[idx] = tempPath
		displayMap[tempPath] = entry
	}

	return archiveSearchResolution{
		resolvedPaths: resolvedPaths,
		displayMap:    displayMap,
		unreadable:    unreadable,
		unreadableSet: unreadableSet,
		cleanup:       cleanup,
	}, nil
}

func sanitizeArchiveScratchBase(base string) string {
	var b strings.Builder
	b.Grow(len(base))
	inBad := false
	for i := range len(base) {
		c := base[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '.' || c == '-'
		if ok {
			b.WriteByte(c)
			inBad = false
		} else if !inBad {
			b.WriteByte('_')
			inBad = true
		}
	}
	if b.Len() == 0 {
		return "entry"
	}
	return b.String()
}

func commonAncestorPath(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	cur := filepath.Dir(paths[0])
	for _, p := range paths[1:] {
		dir := filepath.Dir(p)
		for cur != dir && !strings.HasPrefix(dir, cur+string(filepath.Separator)) && cur != filepath.Dir(cur) {
			cur = filepath.Dir(cur)
		}
	}
	return cur
}
