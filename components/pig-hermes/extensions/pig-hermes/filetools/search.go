package filetools

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

var defaultSkipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"__pycache__":  true,
	".venv":        true,
	".hg":          true,
	".svn":         true,
}

func (s *state) searchFilesToolDef() sdk.ToolDefinition {
	return sdk.ToolDefinition{
		Name:        "search_files",
		Label:       "Search Files",
		Description: "Search file contents (regex) or find files by name (glob). Uses ripgrep (rg) if available on PATH, with a pure-Go fallback.",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Regex for content search; glob (e.g. '*.go', '*config*') for file search.",
				},
				"target": map[string]any{
					"type":        "string",
					"enum":        []string{"content", "files"},
					"description": "'content' searches inside files (default); 'files' finds files by name.",
					"default":     "content",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Directory or file to search in (default '.').",
					"default":     ".",
				},
				"file_glob": map[string]any{
					"type":        "string",
					"description": "Content search only: filter files by glob pattern (e.g. '*.go').",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of results to return (default 50).",
					"default":     defaultSearchLimit,
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "Skip first N results for pagination (default 0).",
					"default":     0,
				},
				"output_mode": map[string]any{
					"type":        "string",
					"enum":        []string{"content", "files_only", "count"},
					"description": "Content search only: 'content' (default), 'files_only', or 'count'.",
					"default":     "content",
				},
				"context": map[string]any{
					"type":        "integer",
					"description": "Content search only: lines of context before and after each match (default 0).",
					"default":     0,
				},
			},
			"required": []string{"pattern"},
		},
		Execute: s.executeSearchFiles,
	}
}

func (s *state) executeSearchFiles(ctx sdk.Context, params map[string]any) (any, error) {
	pattern, _ := paramString(params, "pattern")
	rawTarget, _ := paramString(params, "target")
	switch strings.ToLower(strings.TrimSpace(rawTarget)) {
	case "", "content", "grep":
		rawTarget = "content"
	case "files", "find":
		rawTarget = "files"
	default:
		return errResult(fmt.Sprintf("search_files: invalid target %q (expected 'content' or 'files')", rawTarget)), nil
	}

	if rawTarget == "content" && pattern == "" {
		return errResult("search_files: missing required argument 'pattern' for content search"), nil
	}

	rawPath, _ := paramString(params, "path")
	if strings.TrimSpace(rawPath) == "" {
		rawPath = "."
	}
	absPath, err := resolvePath(safeCwd(ctx), rawPath)
	if err != nil {
		return errResult(fmt.Sprintf("search_files: %v", err)), nil
	}
	if _, err := os.Stat(absPath); err != nil {
		return errResult(fmt.Sprintf("search_files: %v", err)), nil
	}

	fileGlob, _ := paramString(params, "file_glob")
	limit := paramInt(params, "limit", defaultSearchLimit)
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	offset := paramInt(params, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	outputMode, _ := paramString(params, "output_mode")
	if outputMode == "" {
		outputMode = "content"
	}
	switch outputMode {
	case "content", "files_only", "count":
	default:
		return errResult(fmt.Sprintf("search_files: invalid output_mode %q", outputMode)), nil
	}
	contextLines := paramInt(params, "context", 0)
	if contextLines < 0 {
		contextLines = 0
	}

	if rawTarget == "files" {
		return s.searchFilesByName(absPath, pattern, offset, limit)
	}

	return s.searchContent(absPath, pattern, fileGlob, outputMode, contextLines, offset, limit)
}

func (s *state) searchFilesByName(root, pattern string, offset, limit int) (sdk.ToolResult, error) {
	hasGlobMeta := strings.ContainsAny(pattern, "*?[")
	lowerPattern := strings.ToLower(pattern)

	var matches []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && defaultSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			rel = p
		}
		slashRel := filepath.ToSlash(rel)
		if pattern == "" || pattern == "*" {
			matches = append(matches, slashRel)
			return nil
		}
		if hasGlobMeta {
			okBase, _ := filepath.Match(pattern, d.Name())
			okRel, _ := filepath.Match(pattern, slashRel)
			if okBase || okRel {
				matches = append(matches, slashRel)
			}
		} else if strings.Contains(strings.ToLower(slashRel), lowerPattern) {
			matches = append(matches, slashRel)
		}
		return nil
	})
	if err != nil {
		return errResult(fmt.Sprintf("search_files: %v", err)), nil
	}
	return formatSearchResults(matches, offset, limit, "files"), nil
}

func (s *state) searchContent(root, pattern, fileGlob, outputMode string, contextLines, offset, limit int) (sdk.ToolResult, error) {
	if rgPath, err := exec.LookPath("rg"); err == nil && rgPath != "" {
		if lines, ok, rgErr := runRipgrep(rgPath, root, pattern, fileGlob, outputMode, contextLines); ok {
			if rgErr != nil {
				return errResult(fmt.Sprintf("search_files: %v", rgErr)), nil
			}
			return formatSearchResults(lines, offset, limit, outputMode), nil
		}
	}
	return searchContentPureGo(root, pattern, fileGlob, outputMode, contextLines, offset, limit)
}

func runRipgrep(rgPath, root, pattern, fileGlob, outputMode string, contextLines int) ([]string, bool, error) {
	args := []string{
		"--no-heading",
		"--color=never",
		"--glob", "!**/.git/**",
		"--glob", "!**/node_modules/**",
		"--glob", "!**/__pycache__/**",
		"--glob", "!.git/**",
		"--glob", "!node_modules/**",
		"--glob", "!__pycache__/**",
	}
	switch outputMode {
	case "files_only":
		args = append(args, "--files-with-matches")
	case "count":
		args = append(args, "--count")
	default:
		args = append(args, "--line-number")
		if contextLines > 0 {
			args = append(args, "-C", strconv.Itoa(contextLines))
		}
	}
	if strings.TrimSpace(fileGlob) != "" {
		args = append(args, "--glob", fileGlob)
	}
	args = append(args, "--", pattern, root)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, rgPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 1 {
				// No matches found.
				return nil, true, nil
			}
			// Exit code 2 is typically regex syntax error or unreadable path.
			if msg := strings.TrimSpace(stderr.String()); msg != "" {
				return nil, true, fmt.Errorf("rg: %s", msg)
			}
		}
		return nil, false, nil
	}
	raw := strings.TrimSuffix(stdout.String(), "\n")
	if raw == "" {
		return nil, true, nil
	}
	lines := strings.Split(raw, "\n")
	// Normalize paths relative to root when root is a directory.
	if info, statErr := os.Stat(root); statErr == nil && info.IsDir() {
		prefix := filepath.Clean(root) + string(filepath.Separator)
		for i, l := range lines {
			if strings.HasPrefix(l, prefix) {
				lines[i] = filepath.ToSlash(strings.TrimPrefix(l, prefix))
			}
		}
	}
	return lines, true, nil
}

func searchContentPureGo(root, pattern, fileGlob, outputMode string, contextLines, offset, limit int) (sdk.ToolResult, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return errResult(fmt.Sprintf("search_files: invalid regex %q: %v", pattern, err)), nil
	}

	info, err := os.Stat(root)
	if err != nil {
		return errResult(fmt.Sprintf("search_files: %v", err)), nil
	}

	var results []string
	processFile := func(filePath, displayPath string) {
		if fileGlob != "" {
			okBase, _ := filepath.Match(fileGlob, filepath.Base(filePath))
			okRel, _ := filepath.Match(fileGlob, displayPath)
			if !okBase && !okRel {
				return
			}
		}
		if isBinaryFileQuick(filePath) {
			return
		}
		f, err := os.Open(filePath)
		if err != nil {
			return
		}
		defer f.Close()

		var lines []string
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
		for sc.Scan() {
			lines = append(lines, sc.Text())
		}

		matchIndices := make([]int, 0)
		for i, line := range lines {
			if re.MatchString(line) {
				matchIndices = append(matchIndices, i)
			}
		}
		if len(matchIndices) == 0 {
			return
		}

		switch outputMode {
		case "files_only":
			results = append(results, displayPath)
		case "count":
			results = append(results, fmt.Sprintf("%s:%d", displayPath, len(matchIndices)))
		default:
			if contextLines == 0 {
				for _, idx := range matchIndices {
					results = append(results, fmt.Sprintf("%s:%d:%s", displayPath, idx+1, lines[idx]))
				}
				return
			}
			emitted := make(map[int]bool)
			matchSet := make(map[int]bool, len(matchIndices))
			for _, idx := range matchIndices {
				matchSet[idx] = true
			}
			for _, idx := range matchIndices {
				start := idx - contextLines
				if start < 0 {
					start = 0
				}
				end := idx + contextLines
				if end >= len(lines) {
					end = len(lines) - 1
				}
				for j := start; j <= end; j++ {
					if emitted[j] {
						continue
					}
					emitted[j] = true
					sep := '-'
					if matchSet[j] {
						sep = ':'
					}
					results = append(results, fmt.Sprintf("%s%c%d%c%s", displayPath, sep, j+1, sep, lines[j]))
				}
			}
		}
	}

	if !info.IsDir() {
		processFile(root, filepath.Base(root))
	} else {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if d.IsDir() {
				if p != root && defaultSkipDirs[d.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				rel = p
			}
			processFile(p, filepath.ToSlash(rel))
			return nil
		})
	}

	return formatSearchResults(results, offset, limit, outputMode), nil
}

func formatSearchResults(items []string, offset, limit int, mode string) sdk.ToolResult {
	total := len(items)
	if total == 0 {
		return okResult("No matches found.", map[string]any{
			"total": 0,
			"mode":  mode,
		})
	}
	if offset >= total {
		return okResult(fmt.Sprintf("No results at offset %d (total matches: %d).", offset, total), map[string]any{
			"total":  total,
			"offset": offset,
			"mode":   mode,
		})
	}
	end := offset + limit
	if end > total {
		end = total
	}
	page := items[offset:end]
	out := strings.Join(page, "\n")
	truncated := end < total
	if truncated {
		out += fmt.Sprintf("\n... [truncated: showing %d-%d of %d matches; use offset=%d to continue]", offset+1, end, total, end)
	}
	return okResult(out, map[string]any{
		"total":     total,
		"offset":    offset,
		"returned":  len(page),
		"truncated": truncated,
		"mode":      mode,
	})
}
