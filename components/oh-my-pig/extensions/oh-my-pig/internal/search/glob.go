package search

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/dirs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

// upstream: packages/coding-agent/src/tools/glob.ts:46-84
const (
	globDefaultLimit   = 200
	globMaxLimit       = 200
	globDefaultTimeout = 5 * time.Second
)

var allowedGlobArgs = map[string]struct{}{
	"path":      {},
	"hidden":    {},
	"gitignore": {},
	"limit":     {},
	"pattern":   {},
}

func executeGlob(ctx sdk.Context, params map[string]any) (any, error) {
	bgCtx, cancel := context.WithTimeout(context.Background(), globDefaultTimeout)
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
	return runGlobWithURLs(bgCtx, ctx.Cwd(), urls.Context{SDK: ctx, Cwd: ctx.Cwd()}, globDefaultTimeout, params)
}

func runGlob(bgCtx context.Context, cwd string, params map[string]any) (any, error) {
	return runGlobWithURLs(bgCtx, cwd, urls.Context{Cwd: cwd}, globDefaultTimeout, params)
}

// globSearchPath merges an optional misplaced `pattern` argument into `path`.
// upstream: packages/coding-agent/src/tools/glob.ts:56-79
func globSearchPath(params map[string]any) (string, error) {
	base, hasBase := params["path"].(string)
	rawPat, hasPat := params["pattern"]
	if !hasPat || rawPat == nil {
		if hasBase {
			return base, nil
		}
		return "", nil
	}
	patStr, ok := rawPat.(string)
	if !ok {
		return "", fmt.Errorf("`pattern` must be a string glob")
	}
	var patterns []string
	for _, p := range strings.Split(patStr, ";") {
		if p = strings.TrimSpace(p); p != "" {
			patterns = append(patterns, p)
		}
	}
	var bases []string
	if hasBase {
		for _, b := range strings.Split(base, ";") {
			if b = strings.TrimSpace(b); b != "" {
				bases = append(bases, b)
			}
		}
	}
	if len(bases) == 0 {
		return strings.Join(patterns, ";"), nil
	}
	seen := make(map[string]struct{})
	var scoped []string
	for _, dir := range bases {
		cleanDir := strings.TrimRight(dir, `/\`)
		for _, pat := range patterns {
			standalone := filepath.IsAbs(pat) || strings.HasPrefix(pat, "~") || isURLLikePath(pat)
			var entry string
			if standalone {
				entry = pat
			} else {
				entry = cleanDir + "/" + pat
			}
			if _, ok := seen[entry]; !ok {
				seen[entry] = struct{}{}
				scoped = append(scoped, entry)
			}
		}
	}
	return strings.Join(scoped, ";"), nil
}

// formatGlobActual mirrors omptype's describeValue/describeKind formatting for
// findSchema errors (upstream: packages/omptype/src/errors.ts:146-198).
func formatGlobActual(v any, domain bool) string {
	if v == nil {
		return "null"
	}
	switch x := v.(type) {
	case string:
		if domain {
			return "a string"
		}
		if len(x) <= 40 {
			b, _ := json.Marshal(x)
			return string(b)
		}
		return fmt.Sprintf("a string (length %d)", len(x))
	case bool:
		if domain {
			return "boolean"
		}
		if x {
			return "true"
		}
		return "false"
	case float64:
		if math.IsNaN(x) {
			return "NaN"
		}
		if domain {
			return "a number"
		}
		return strconv.FormatFloat(x, 'g', -1, 64)
	case int:
		if domain {
			return "a number"
		}
		return strconv.Itoa(x)
	case int64:
		if domain {
			return "a number"
		}
		return strconv.FormatInt(x, 10)
	case []any:
		if domain {
			return "an object"
		}
		return "an array"
	case map[string]any:
		if domain {
			return "an object"
		}
		b, err := json.Marshal(x)
		if err != nil {
			return "an object"
		}
		return string(b)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// validateGlobArguments mirrors omp's lenientArgValidation handling in
// GlobTool.execute (upstream: packages/coding-agent/src/tools/glob.ts:238-246).
// It runs both in PrepareArguments (before PiG's host schema validator) and at
// the start of runGlobWithURLs (for direct Go callers).
func validateGlobArguments(params map[string]any) error {
	if params == nil {
		return nil
	}
	var undeclared []string
	for k := range params {
		if _, ok := allowedGlobArgs[k]; !ok {
			undeclared = append(undeclared, "`"+k+"`")
		}
	}
	if len(undeclared) > 0 {
		sort.Strings(undeclared)
		return fmt.Errorf("glob has no %s argument; put the glob in `path`", strings.Join(undeclared, ", "))
	}
	if rawPat, hasPat := params["pattern"]; hasPat && rawPat != nil {
		if _, ok := rawPat.(string); !ok {
			return fmt.Errorf("`pattern` must be a string glob")
		}
	} else if hasPat && rawPat == nil {
		return fmt.Errorf("`pattern` must be a string glob")
	}

	var errs []string
	if v, ok := params["path"]; ok {
		if _, isStr := v.(string); !isStr {
			errs = append(errs, fmt.Sprintf("path must be a string (was %s)", formatGlobActual(v, true)))
		}
	}
	if v, ok := params["hidden"]; ok {
		if _, isBool := v.(bool); !isBool {
			errs = append(errs, fmt.Sprintf("hidden must be boolean (was %s)", formatGlobActual(v, true)))
		}
	}
	if v, ok := params["gitignore"]; ok {
		if _, isBool := v.(bool); !isBool {
			errs = append(errs, fmt.Sprintf("gitignore must be boolean (was %s)", formatGlobActual(v, true)))
		}
	}
	if v, ok := params["limit"]; ok {
		var f float64
		var isNum bool
		switch n := v.(type) {
		case float64:
			f, isNum = n, true
		case int:
			f, isNum = float64(n), true
		case int64:
			f, isNum = float64(n), true
		}
		if !isNum || math.IsNaN(f) || math.IsInf(f, 0) {
			errs = append(errs, fmt.Sprintf("limit must be an integer (was %s)", formatGlobActual(v, false)))
		} else {
			var limExpected []string
			if f != math.Floor(f) {
				limExpected = append(limExpected, "an integer")
			}
			if f < 1 {
				limExpected = append(limExpected, "a number at least 1")
			}
			if f > globMaxLimit {
				limExpected = append(limExpected, fmt.Sprintf("a number at most %d", globMaxLimit))
			}
			if len(limExpected) == 1 {
				errs = append(errs, fmt.Sprintf("limit must be %s (was %s)", limExpected[0], formatGlobActual(f, false)))
			} else if len(limExpected) > 1 {
				var b strings.Builder
				fmt.Fprintf(&b, "limit (%s) must be...", formatGlobActual(f, false))
				for _, exp := range limExpected {
					b.WriteString("\n  ◦ ")
					b.WriteString(exp)
				}
				errs = append(errs, b.String())
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "\n"))
	}
	return nil
}

func runGlobWithURLs(bgCtx context.Context, cwd string, urlCtx urls.Context, timeout time.Duration, params map[string]any) (any, error) {
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
	if timeout <= 0 {
		timeout = globDefaultTimeout
	}

	if err := validateGlobArguments(params); err != nil {
		return nil, err
	}

	pathInput, err := globSearchPath(params)
	if err != nil {
		return nil, err
	}

	includeHidden := true
	if v, ok := params["hidden"].(bool); ok {
		includeHidden = v
	}
	useGitignore := true
	if v, ok := params["gitignore"].(bool); ok {
		useGitignore = v
	}

	effectiveLimit := globDefaultLimit
	if rawLimit, exists := params["limit"]; exists && rawLimit != nil {
		switch n := rawLimit.(type) {
		case float64:
			effectiveLimit = int(n)
		case int:
			effectiveLimit = n
		case int64:
			effectiveLimit = int(n)
		}
	}

	urlfs := newSearchURLFS(urlCtx)
	scopedPaths := toPathList(pathInput)
	if len(scopedPaths) == 0 {
		scopedPaths = []string{"."}
	}
	rawEntries := expandDelimitedPathEntries(scopedPaths, cwd, urlfs, false)
	if len(rawEntries) == 0 {
		return nil, fmt.Errorf("`path` must contain non-empty globs or paths")
	}

	for _, entry := range rawEntries {
		if entry == "" {
			return nil, fmt.Errorf("`path` must contain non-empty globs or paths")
		}
		if !isRegisteredURL(entry) && isRootOnlyPath(entry) {
			return nil, fmt.Errorf("Searching from root directory '/' is not allowed")
		}
	}

	type globResultEntry struct {
		path        string
		mtimeMs     int64
		targetIndex int
	}

	mtimeMillis := func(t time.Time) int64 {
		if t.IsZero() {
			return 0
		}
		return t.UnixMilli()
	}

	isSingle := len(rawEntries) == 1
	var missingPaths []string
	var validEntries []string
	var collected []globResultEntry
	seen := make(map[string]struct{})
	timedOut := false

	appendWalkEntries := func(targetIdx int, entries []walkEntry, urlBase, hostRoot string) {
		for _, e := range entries {
			var display string
			if urlBase != "" {
				display = resolveURLHostRelative(urlBase, hostRoot, e.absPath, e.isDir)
			} else {
				display = formatRelToCwd(e.absPath, cwd, e.isDir)
			}
			if _, ok := seen[display]; ok {
				continue
			}
			seen[display] = struct{}{}
			collected = append(collected, globResultEntry{
				path:        display,
				mtimeMs:     mtimeMillis(e.mtime),
				targetIndex: targetIdx,
			})
		}
	}

	for targetIdx, entry := range rawEntries {
		if bgCtx.Err() != nil {
			if bgCtx.Err() == context.DeadlineExceeded {
				timedOut = true
				break
			}
			return nil, bgCtx.Err()
		}
		parsed, pErr := parseFindPattern(entry, cwd)
		if pErr != nil {
			return nil, pErr
		}
		if !parsed.isURL && isRootOnlyPath(parsed.basePath) {
			return nil, fmt.Errorf("Searching from root directory '/' is not allowed")
		}

		includeNodeModules := pathMentionsSegment(parsed.basePath, "node_modules") ||
			pathMentionsSegment(parsed.globPattern, "node_modules")
		includeGit := pathMentionsSegment(parsed.globPattern, ".git")

		if parsed.isURL {
			node, rErr := urlfs.resolve(parsed.basePath)
			if rErr != nil {
				if isSingle {
					return nil, fmt.Errorf("Cannot glob %s: %w", parsed.basePath, rErr)
				}
				missingPaths = append(missingPaths, entry)
				continue
			}
			validEntries = append(validEntries, entry)
			isDir := node.kind == urlNodeHostDir || node.kind == urlNodeVirtualDir
			if !parsed.hasGlob && !isDir {
				if _, ok := seen[node.url]; !ok {
					seen[node.url] = struct{}{}
					collected = append(collected, globResultEntry{path: node.url, mtimeMs: mtimeMillis(node.mtime), targetIndex: targetIdx})
				}
				continue
			}
			if !isDir {
				if isSingle {
					return nil, fmt.Errorf("Path is not a directory: %s", parsed.basePath)
				}
				continue
			}

			var includeMatcher *globMatcher
			if parsed.hasGlob {
				m, err := compileGlob(parsed.globPattern)
				if err != nil {
					return nil, fmt.Errorf("Invalid glob pattern: %w", err)
				}
				includeMatcher = m
			}

			if node.kind == urlNodeVirtualDir {
				vEntries, vErr := urlfs.walkVirtualDir(bgCtx, node, includeMatcher, nil, parsed.recursive, true, parsed.directoryOnly)
				for _, ve := range vEntries {
					display := ve.url
					if ve.isDir && !strings.HasSuffix(display, "/") {
						display += "/"
					}
					if _, ok := seen[display]; ok {
						continue
					}
					seen[display] = struct{}{}
					collected = append(collected, globResultEntry{path: display, targetIndex: targetIdx})
				}
				if vErr != nil {
					if bgCtx.Err() == context.DeadlineExceeded {
						timedOut = true
						break
					}
					return nil, vErr
				}
				continue
			}

			// File-backed URL directory: walk host directory and map results back to URLs.
			walkRoot := node.hostPath
			entries, wErr := walkDir(bgCtx, walkRoot, walkOptions{
				hidden:             includeHidden,
				gitignore:          useGitignore,
				recursive:          parsed.recursive,
				followLinks:        isSkillURL(node.url),
				includeSymlinks:    true,
				include:            includeMatcher,
				includeDir:         true,
				dirOnly:            parsed.directoryOnly,
				includeNodeModules: includeNodeModules,
				includeGit:         includeGit,
			})
			appendWalkEntries(targetIdx, entries, node.url, walkRoot)
			if wErr != nil {
				if bgCtx.Err() == context.DeadlineExceeded {
					timedOut = true
					break
				}
				return nil, wErr
			}
			continue
		}

		info, statErr := os.Stat(parsed.basePath)
		if statErr != nil {
			if isSingle {
				return nil, fmt.Errorf("Path not found: %s", formatRelToCwd(parsed.basePath, cwd, false))
			}
			missingPaths = append(missingPaths, entry)
			continue
		}
		validEntries = append(validEntries, entry)
		if !parsed.hasGlob && !info.IsDir() {
			rel := formatRelToCwd(parsed.basePath, cwd, false)
			if _, ok := seen[rel]; !ok {
				seen[rel] = struct{}{}
				collected = append(collected, globResultEntry{path: rel, mtimeMs: mtimeMillis(info.ModTime()), targetIndex: targetIdx})
			}
			continue
		}
		if !info.IsDir() {
			if isSingle {
				return nil, fmt.Errorf("Path is not a directory: %s", parsed.basePath)
			}
			continue
		}
		var includeMatcher *globMatcher
		if parsed.hasGlob {
			m, err := compileGlob(parsed.globPattern)
			if err != nil {
				return nil, fmt.Errorf("Invalid glob pattern: %w", err)
			}
			includeMatcher = m
		}
		entries, wErr := walkDir(bgCtx, parsed.basePath, walkOptions{
			hidden:             includeHidden,
			gitignore:          useGitignore,
			recursive:          parsed.recursive,
			includeSymlinks:    true,
			include:            includeMatcher,
			includeDir:         true,
			dirOnly:            parsed.directoryOnly,
			includeNodeModules: includeNodeModules,
			includeGit:         includeGit,
		})
		appendWalkEntries(targetIdx, entries, "", "")
		if wErr != nil {
			if bgCtx.Err() == context.DeadlineExceeded {
				timedOut = true
				break
			}
			return nil, wErr
		}
	}

	if len(validEntries) == 0 && !timedOut {
		var b strings.Builder
		fmt.Fprintf(&b, "None of the %d paths exist:", len(missingPaths))
		for _, m := range missingPaths {
			b.WriteString("\n- ")
			b.WriteString(m)
		}
		return nil, fmt.Errorf("%s", b.String())
	}

	scopePath := toScopeDisplay(validEntries, cwd)
	if len(validEntries) == 1 {
		parsed, _ := parseFindPattern(validEntries[0], cwd)
		scopePath = formatRelToCwd(parsed.basePath, cwd, false)
	}

	// Sort newest-first by millisecond mtime, preserving target order on ties
	// and breaking ties within the same target by path.
	// upstream: packages/coding-agent/src/tools/glob.ts:703, crates/xfs/src/glob.rs:331-334
	sort.Slice(collected, func(i, j int) bool {
		if collected[i].mtimeMs != collected[j].mtimeMs {
			return collected[i].mtimeMs > collected[j].mtimeMs
		}
		if collected[i].targetIndex != collected[j].targetIndex {
			return collected[i].targetIndex < collected[j].targetIndex
		}
		return collected[i].path < collected[j].path
	})

	var missingPathsNote string
	if len(missingPaths) > 0 {
		missingPathsNote = "Skipped missing paths: " + strings.Join(missingPaths, ", ")
	}
	var timeoutNotice string
	if timedOut {
		timeoutNotice = globTimeoutNotice(len(collected), timeout)
	}

	if len(collected) == 0 {
		details := map[string]any{
			"scopePath": scopePath,
			"fileCount": 0,
			"files":     []string{},
			"truncated": timedOut,
			"cwd":       dirs.StandardizeMacOSPath(cwd),
		}
		if timedOut {
			details["timedOut"] = true
		}
		if len(missingPaths) > 0 {
			details["missingPaths"] = missingPaths
		}
		var parts []string
		if !timedOut {
			parts = append(parts, "No files found matching pattern")
		}
		if timeoutNotice != "" {
			parts = append(parts, timeoutNotice)
		}
		if missingPathsNote != "" {
			parts = append(parts, missingPathsNote)
		}
		return sdk.ToolResult{
			Content: strings.Join(parts, "\n"),
			Details: details,
		}, nil
	}

	// upstream: packages/tui/src/tools/list-limit.ts:26-35, packages/coding-agent/src/tools/glob.ts:383-424
	resultLimitReached := len(collected) >= effectiveLimit
	if len(collected) > effectiveLimit {
		collected = collected[:effectiveLimit]
	}

	files := make([]string, len(collected))
	for i, e := range collected {
		files[i] = e.path
	}

	baseOutput := formatGroupedPaths(files)
	var trailingNotes []string
	if timeoutNotice != "" {
		trailingNotes = append(trailingNotes, timeoutNotice)
	}
	if missingPathsNote != "" {
		trailingNotes = append(trailingNotes, missingPathsNote)
	}
	rawOutput := baseOutput
	if len(trailingNotes) > 0 {
		rawOutput += "\n\n" + strings.Join(trailingNotes, "\n")
	}

	rawLines := strings.Split(rawOutput, "\n")
	content, keptLines, byteTruncated := truncateHeadLines(rawLines, grepMaxOutputBytes)
	output := content

	// Build output-meta bracket notice matching output-meta.ts:370-425.
	var metaNotices []string
	meta := map[string]any{}
	if byteTruncated {
		metaNotices = append(metaNotices, fmt.Sprintf("Showing lines 1-%d of %d (%s limit)", keptLines, len(rawLines), formatBytesCompact(grepMaxOutputBytes)))
		meta["truncation"] = map[string]any{
			"direction":   "head",
			"truncatedBy": "bytes",
			"outputLines": keptLines,
			"totalLines":  len(rawLines),
			"outputBytes": len(content),
			"totalBytes":  len(rawOutput),
			"maxBytes":    grepMaxOutputBytes,
			"shownRange":  map[string]any{"start": 1, "end": keptLines},
		}
	}
	if resultLimitReached {
		suggestion := min(effectiveLimit*2, globMaxLimit)
		if suggestion > effectiveLimit {
			metaNotices = append(metaNotices, fmt.Sprintf("%d results limit reached. Use limit=%d for more", effectiveLimit, suggestion))
			meta["limits"] = map[string]any{
				"resultLimit": map[string]any{"reached": effectiveLimit, "suggestion": suggestion},
			}
		} else {
			metaNotices = append(metaNotices, fmt.Sprintf("%d results limit reached", effectiveLimit))
			meta["limits"] = map[string]any{
				"resultLimit": map[string]any{"reached": effectiveLimit},
			}
		}
	}
	if len(metaNotices) > 0 {
		output += "\n\n[" + strings.Join(metaNotices, ". ") + "]"
	}

	details := map[string]any{
		"scopePath": scopePath,
		"fileCount": len(files),
		"files":     files,
		"truncated": timedOut || resultLimitReached || byteTruncated,
		"cwd":       dirs.StandardizeMacOSPath(cwd),
	}
	if timedOut {
		details["timedOut"] = true
	}
	if resultLimitReached {
		details["resultLimitReached"] = effectiveLimit
	}
	if len(missingPaths) > 0 {
		details["missingPaths"] = missingPaths
	}
	if byteTruncated {
		details["truncation"] = map[string]any{
			"truncated":   true,
			"truncatedBy": "bytes",
			"outputLines": keptLines,
			"totalLines":  len(rawLines),
			"totalBytes":  len(rawOutput),
			"outputBytes": len(content),
			"maxBytes":    grepMaxOutputBytes,
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

// globTimeoutNotice mirrors packages/coding-agent/src/tools/glob.ts:123-132.
func globTimeoutNotice(partialCount int, timeout time.Duration) string {
	ms := timeout.Milliseconds()
	var sec string
	if ms%1000 == 0 {
		sec = fmt.Sprintf("%d", ms/1000)
	} else {
		sec = fmt.Sprintf("%.1f", float64(ms)/1000)
	}
	if partialCount > 0 {
		return fmt.Sprintf("glob timed out after %ss; returning %d partial matches — results are incomplete, scope to a deeper directory instead of retrying blindly", sec, partialCount)
	}
	return fmt.Sprintf("Glob timed out after %ss before finding any matches — the scan is incomplete, NOT proof of absence. The walk is bounded by directory size, not pattern width; scope the search to a deeper directory (e.g. `sub/dir/*.ext` instead of `*.ext` at a huge root).", sec)
}
