package search

import (
	"context"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// upstream: packages/coding-agent/src/tools/glob.ts:43-45
const (
	globDefaultLimit   = 200
	globMaxLimit       = 200
	globDefaultTimeout = 5 * time.Second
)

func executeGlob(ctx sdk.Context, params map[string]any) (any, error) {
	bgCtx, cancel := context.WithTimeout(context.Background(), globDefaultTimeout)
	defer cancel()
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-bgCtx.Done():
		}
	}()
	return runGlob(bgCtx, ctx.Cwd(), params)
}

func runGlob(bgCtx context.Context, cwd string, params map[string]any) (any, error) {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	pathInput, _ := params["path"].(string)
	includeHidden := true
	if v, ok := params["hidden"].(bool); ok {
		includeHidden = v
	}
	useGitignore := true
	if v, ok := params["gitignore"].(bool); ok {
		useGitignore = v
	}
	requestedLimit := globDefaultLimit
	if v, ok := params["limit"].(float64); ok {
		if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
			return nil, fmt.Errorf("Limit must be a positive number")
		}
		requestedLimit = int(math.Floor(v))
	}
	effectiveLimit := min(globMaxLimit, max(1, requestedLimit))
	var clampNotice string
	if requestedLimit > globMaxLimit {
		clampNotice = fmt.Sprintf("Requested limit %d clamped to the max of %d", requestedLimit, globMaxLimit)
	}

	rawEntries := toPathList(pathInput)
	includes, excludes, err := splitExclusions(rawEntries)
	if err != nil {
		return nil, err
	}
	if len(includes) == 0 {
		includes = []string{"."}
	}

	for _, entry := range includes {
		if isRootOnlyPath(entry) {
			return nil, fmt.Errorf("Searching from root directory '/' is not allowed")
		}
	}

	type globResultEntry struct {
		path  string
		mtime time.Time
	}

	var missingPaths []string
	var collected []globResultEntry
	seen := make(map[string]struct{})
	validTargets := 0

	for _, entry := range includes {
		parsed := parseGlobTarget(entry, cwd)
		if isRootOnlyPath(parsed.basePath) {
			return nil, fmt.Errorf("Searching from root directory '/' is not allowed")
		}
		info, statErr := os.Stat(parsed.basePath)
		if statErr != nil {
			if len(includes) == 1 {
				return nil, fmt.Errorf("Path not found: %s", entry)
			}
			missingPaths = append(missingPaths, entry)
			continue
		}
		validTargets++
		if !parsed.hasGlob && !info.IsDir() {
			rel := formatRelToCwd(parsed.basePath, cwd, false)
			if _, ok := seen[rel]; !ok {
				seen[rel] = struct{}{}
				collected = append(collected, globResultEntry{path: rel, mtime: info.ModTime()})
			}
			continue
		}
		if !info.IsDir() {
			if len(includes) == 1 {
				return nil, fmt.Errorf("Path is not a directory: %s", parsed.basePath)
			}
			continue
		}
		var includeMatcher *globMatcher
		if parsed.hasGlob {
			m, err := compileGlob(parsed.globPattern)
			if err != nil {
				return nil, fmt.Errorf("invalid glob %q: %w", parsed.globPattern, err)
			}
			includeMatcher = m
		}
		entries, err := walkDir(bgCtx, parsed.basePath, walkOptions{
			hidden:     includeHidden,
			gitignore:  useGitignore,
			recursive:  parsed.recursive,
			excludes:   excludes,
			include:    includeMatcher,
			includeDir: true,
		})
		if err != nil {
			if bgCtx.Err() == context.DeadlineExceeded {
				return nil, fmt.Errorf("Glob timed out after %ds before finding any matches — the scan is incomplete, NOT proof of absence.", int(globDefaultTimeout.Seconds()))
			}
			return nil, err
		}
		for _, e := range entries {
			rel := formatRelToCwd(e.absPath, cwd, e.isDir)
			if _, ok := seen[rel]; ok {
				continue
			}
			seen[rel] = struct{}{}
			collected = append(collected, globResultEntry{path: rel, mtime: e.mtime})
		}
	}

	if validTargets == 0 {
		return nil, fmt.Errorf("Path not found: %s", strings.Join(missingPaths, ", "))
	}

	// Sort newest-first by mtime, breaking ties deterministically by path.
	sort.Slice(collected, func(i, j int) bool {
		if !collected[i].mtime.Equal(collected[j].mtime) {
			return collected[i].mtime.After(collected[j].mtime)
		}
		return collected[i].path < collected[j].path
	})

	var missingPathsNote string
	if len(missingPaths) > 0 {
		missingPathsNote = "Skipped missing paths: " + strings.Join(missingPaths, ", ")
	}

	if len(collected) == 0 {
		parts := []string{"No files found matching pattern"}
		if missingPathsNote != "" {
			parts = append(parts, missingPathsNote)
		}
		return sdk.ToolResult{
			Content: strings.Join(parts, "\n"),
			Details: map[string]any{
				"fileCount": 0,
				"files":     []string{},
				"truncated": false,
			},
		}, nil
	}

	// upstream: packages/tui/src/tools/list-limit.ts:26-35, packages/coding-agent/src/tools/glob.ts:334-345,
	// packages/tui/src/tools/output-meta.ts:322-332,365
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
	if clampNotice != "" {
		trailingNotes = append(trailingNotes, clampNotice)
	}
	if missingPathsNote != "" {
		trailingNotes = append(trailingNotes, missingPathsNote)
	}
	rawOutput := baseOutput
	if len(trailingNotes) > 0 {
		rawOutput += "\n\n" + strings.Join(trailingNotes, "\n")
	}
	if resultLimitReached {
		suggestion := min(effectiveLimit*2, globMaxLimit)
		if suggestion > effectiveLimit {
			rawOutput += fmt.Sprintf("\n\n[%d results limit reached. Use limit=%d for more]", effectiveLimit, suggestion)
		} else {
			rawOutput += fmt.Sprintf("\n\n[%d results limit reached]", effectiveLimit)
		}
	}

	return sdk.ToolResult{
		Content: rawOutput,
		Details: map[string]any{
			"fileCount": len(files),
			"files":     files,
			"truncated": resultLimitReached,
		},
	}, nil
}
