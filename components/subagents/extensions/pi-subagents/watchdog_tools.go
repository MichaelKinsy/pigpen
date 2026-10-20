package pi_subagents

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The read-only tools of a watchdog reviewer (createReadOnlyTools of the host in the original): read, grep, find and ls, rooted at the
// reviewed cwd. A tool handler of the SDK cannot call the host's own tools from an event handler, so these are the extension's own,
// with the parameters and limits of the host's.

const (
	wdToolMaxBytes     = 50 * 1024
	wdReadMaxLines     = 2000
	wdGrepDefaultLimit = 100
	wdGrepMaxLineChars = 500
	wdFindDefaultLimit = 1000
	wdLsDefaultLimit   = 500
)

func wdResolveToolPath(cwd, p string) string {
	p = expandHomePath(p)
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(cwd, p)
}

func wdArgString(args *jsObject, key string) (string, bool) {
	s, ok := args.vals[key].(string)
	return s, ok
}

func wdArgInt(args *jsObject, key string, fallback int) int {
	if f, ok := args.vals[key].(float64); ok && f >= 0 {
		return int(f)
	}
	return fallback
}

func wdTruncateBytes(text string) (string, bool) {
	if len(text) <= wdToolMaxBytes {
		return text, false
	}
	cut := wdToolMaxBytes
	for cut > 0 && !isRuneStart(text[cut]) {
		cut--
	}
	return text[:cut], true
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

func wdReadTool(cwd string) wdAgentTool {
	return wdAgentTool{
		Spec: wdToolSpec{Name: "read", Description: "Read the contents of a file. For text files, output is truncated to 2000 lines or 50KB (whichever is hit first). Use offset/limit for large files. When you need the full file, continue with offset until complete.",
			Parameters: wdToolSchema(map[string]any{
				"path":   map[string]any{"type": "string", "description": "Path to the file to read (relative or absolute)"},
				"offset": map[string]any{"type": "number", "description": "Line number to start reading from (1-indexed)"},
				"limit":  map[string]any{"type": "number", "description": "Maximum number of lines to read"},
			}, []string{"path"})},
		Execute: func(args *jsObject) (string, error) {
			p, _ := wdArgString(args, "path")
			full := wdResolveToolPath(cwd, p)
			b, err := os.ReadFile(full)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return "", fmt.Errorf("File not found: %s", p)
				}
				return "", err
			}
			lines := strings.Split(string(b), "\n")
			start := wdArgInt(args, "offset", 1)
			if start < 1 {
				start = 1
			}
			if start > len(lines) {
				return "", fmt.Errorf("Offset %d is beyond end of file (%d lines total)", start, len(lines))
			}
			end := len(lines)
			if limit := wdArgInt(args, "limit", 0); limit > 0 && start-1+limit < end {
				end = start - 1 + limit
			}
			truncated := false
			if end-(start-1) > wdReadMaxLines {
				end, truncated = start-1+wdReadMaxLines, true
			}
			text := strings.Join(lines[start-1:end], "\n")
			if cut, over := wdTruncateBytes(text); over {
				text, truncated = cut, true
				end = start - 1 + strings.Count(text, "\n") + 1
			}
			if truncated || end < len(lines) {
				text += fmt.Sprintf("\n\n[Showing lines %d-%d of %d. Use offset=%d to continue.]", start, end, len(lines), end+1)
			}
			return text, nil
		},
	}
}

// wdListFiles lists the files under root (relative, slash separated): the ones Git knows (tracked and not ignored) in a repository,
// else a walk that skips .git and node_modules.
func wdListFiles(root string) []string {
	cmd := exec.Command("git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if out, err := cmd.Output(); err == nil {
		var files []string
		for _, f := range strings.Split(string(out), "\x00") {
			if f == "" {
				continue
			}
			if st, err := os.Stat(filepath.Join(root, filepath.FromSlash(f))); err == nil && st.Mode().IsRegular() {
				files = append(files, f)
			}
		}
		sort.Strings(files)
		return files
	}
	var files []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if d.Type().IsRegular() {
			if rel, err := filepath.Rel(root, p); err == nil {
				files = append(files, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	sort.Strings(files)
	return files
}

func wdGlobRegexp(pattern string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	runes := []rune(pattern)
	for i := 0; i < len(runes); i++ {
		switch r := runes[i]; r {
		case '*':
			if i+1 < len(runes) && runes[i+1] == '*' {
				i++
				if i+1 < len(runes) && runes[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil
	}
	return re
}

func wdGlobMatches(re *regexp.Regexp, pattern, rel string) bool {
	if re == nil {
		return false
	}
	if !strings.Contains(pattern, "/") {
		return re.MatchString(filepath.Base(rel)) || re.MatchString(rel)
	}
	return re.MatchString(rel)
}

func wdGrepTool(cwd string) wdAgentTool {
	return wdAgentTool{
		Spec: wdToolSpec{Name: "grep", Description: "Search file contents for a pattern. Returns matching lines with file paths and line numbers. Respects .gitignore. Output is truncated to 100 matches or 50KB (whichever is hit first). Long lines are truncated to 500 chars.",
			Parameters: wdToolSchema(map[string]any{
				"pattern":    map[string]any{"type": "string", "description": "Search pattern (regex or literal string)"},
				"path":       map[string]any{"type": "string", "description": "Directory or file to search (default: current directory)"},
				"glob":       map[string]any{"type": "string", "description": "Filter files by glob pattern, e.g. '*.ts' or '**/*.spec.ts'"},
				"ignoreCase": map[string]any{"type": "boolean", "description": "Case-insensitive search (default: false)"},
				"literal":    map[string]any{"type": "boolean", "description": "Treat pattern as literal string instead of regex (default: false)"},
				"context":    map[string]any{"type": "number", "description": "Number of lines to show before and after each match (default: 0)"},
				"limit":      map[string]any{"type": "number", "description": "Maximum number of matches to return (default: 100)"},
			}, []string{"pattern"})},
		Execute: func(args *jsObject) (string, error) {
			pattern, _ := wdArgString(args, "pattern")
			if literal, _ := args.vals["literal"].(bool); literal {
				pattern = regexp.QuoteMeta(pattern)
			}
			if ic, _ := args.vals["ignoreCase"].(bool); ic {
				pattern = "(?i)" + pattern
			}
			re, err := regexp.Compile(pattern)
			if err != nil {
				return "", fmt.Errorf("Invalid pattern: %v", err)
			}
			searchPath := cwd
			if p, ok := wdArgString(args, "path"); ok && p != "" {
				searchPath = wdResolveToolPath(cwd, p)
			}
			st, err := os.Stat(searchPath)
			if err != nil {
				return "", fmt.Errorf("Path not found: %s", searchPath)
			}
			var files []string
			root := searchPath
			if st.IsDir() {
				files = wdListFiles(searchPath)
			} else {
				root = filepath.Dir(searchPath)
				files = []string{filepath.Base(searchPath)}
			}
			globPattern, _ := wdArgString(args, "glob")
			globRe := wdGlobRegexp(globPattern)
			limit := wdArgInt(args, "limit", wdGrepDefaultLimit)
			if limit < 1 {
				limit = wdGrepDefaultLimit
			}
			contextLines := wdArgInt(args, "context", 0)
			var out []string
			matches := 0
			for _, rel := range files {
				if globPattern != "" && !wdGlobMatches(globRe, globPattern, rel) {
					continue
				}
				f, err := os.Open(filepath.Join(root, filepath.FromSlash(rel)))
				if err != nil {
					continue
				}
				var lines []string
				sc := bufio.NewScanner(f)
				sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
				binary := false
				for sc.Scan() {
					if strings.ContainsRune(sc.Text(), 0) {
						binary = true
						break
					}
					lines = append(lines, sc.Text())
				}
				f.Close()
				if binary {
					continue
				}
				shown := map[int]bool{}
				for i, line := range lines {
					if !re.MatchString(line) {
						continue
					}
					matches++
					for j := max(0, i-contextLines); j <= min(len(lines)-1, i+contextLines); j++ {
						if shown[j] {
							continue
						}
						shown[j] = true
						text := lines[j]
						if wdLen(text) > wdGrepMaxLineChars {
							text = wdSlice(text, 0, wdGrepMaxLineChars) + "... [truncated]"
						}
						sep := ": "
						if j != i {
							sep = "- "
						}
						out = append(out, fmt.Sprintf("%s:%d%s%s", rel, j+1, sep, text))
					}
					if matches >= limit {
						break
					}
				}
				if matches >= limit {
					out = append(out, fmt.Sprintf("\n[%d matches limit reached. Use limit=%d for more, or refine pattern]", limit, limit*2))
					break
				}
			}
			if len(out) == 0 {
				return "No matches found", nil
			}
			text, over := wdTruncateBytes(strings.Join(out, "\n"))
			if over {
				text += "\n\n[Output truncated at 50KB]"
			}
			return text, nil
		},
	}
}

func wdFindTool(cwd string) wdAgentTool {
	return wdAgentTool{
		Spec: wdToolSpec{Name: "find", Description: "Search for files by glob pattern. Returns matching file paths relative to the search directory. Respects .gitignore. Output is truncated to 1000 results or 50KB (whichever is hit first).",
			Parameters: wdToolSchema(map[string]any{
				"pattern": map[string]any{"type": "string", "description": "Glob pattern to match files, e.g. '*.ts', '**/*.json', or 'src/**/*.spec.ts'"},
				"path":    map[string]any{"type": "string", "description": "Directory to search in (default: current directory)"},
				"limit":   map[string]any{"type": "number", "description": "Maximum number of results (default: 1000)"},
			}, []string{"pattern"})},
		Execute: func(args *jsObject) (string, error) {
			pattern, _ := wdArgString(args, "pattern")
			searchPath := cwd
			if p, ok := wdArgString(args, "path"); ok && p != "" {
				searchPath = wdResolveToolPath(cwd, p)
			}
			if st, err := os.Stat(searchPath); err != nil || !st.IsDir() {
				return "", fmt.Errorf("Path not found: %s", searchPath)
			}
			limit := wdArgInt(args, "limit", wdFindDefaultLimit)
			if limit < 1 {
				limit = wdFindDefaultLimit
			}
			re := wdGlobRegexp(pattern)
			var out []string
			for _, rel := range wdListFiles(searchPath) {
				if wdGlobMatches(re, pattern, rel) {
					out = append(out, rel)
					if len(out) >= limit {
						break
					}
				}
			}
			if len(out) == 0 {
				return "No files found matching pattern", nil
			}
			text, over := wdTruncateBytes(strings.Join(out, "\n"))
			if over {
				text += "\n\n[Output truncated at 50KB]"
			} else if len(out) >= limit {
				text += fmt.Sprintf("\n\n[%d results limit reached. Use limit=%d for more, or refine pattern]", limit, limit*2)
			}
			return text, nil
		},
	}
}

func wdLsTool(cwd string) wdAgentTool {
	return wdAgentTool{
		Spec: wdToolSpec{Name: "ls", Description: "List directory contents. Returns entries sorted alphabetically, with '/' suffix for directories. Includes dotfiles. Output is truncated to 500 entries or 50KB (whichever is hit first).",
			Parameters: wdToolSchema(map[string]any{
				"path":  map[string]any{"type": "string", "description": "Directory to list (default: current directory)"},
				"limit": map[string]any{"type": "number", "description": "Maximum number of entries to return (default: 500)"},
			}, nil)},
		Execute: func(args *jsObject) (string, error) {
			dir := cwd
			if p, ok := wdArgString(args, "path"); ok && p != "" {
				dir = wdResolveToolPath(cwd, p)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return "", fmt.Errorf("Path not found: %s", dir)
				}
				return "", err
			}
			limit := wdArgInt(args, "limit", wdLsDefaultLimit)
			if limit < 1 {
				limit = wdLsDefaultLimit
			}
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				name := e.Name()
				if e.IsDir() {
					name += "/"
				} else if e.Type()&fs.ModeSymlink != 0 {
					if st, err := os.Stat(filepath.Join(dir, e.Name())); err == nil && st.IsDir() {
						name += "/"
					}
				}
				names = append(names, name)
			}
			sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
			if len(names) == 0 {
				return "(empty directory)", nil
			}
			truncated := len(names) > limit
			if truncated {
				names = names[:limit]
			}
			text, over := wdTruncateBytes(strings.Join(names, "\n"))
			if truncated {
				text += fmt.Sprintf("\n\n[%d entries limit reached. Use limit=%d for more]", limit, limit*2)
			} else if over {
				text += "\n\n[Output truncated at 50KB]"
			}
			return text, nil
		},
	}
}

// wdReadOnlyTools is createReadOnlyTools: read, grep, find and ls.
func wdReadOnlyTools(cwd string) []wdAgentTool {
	return []wdAgentTool{wdReadTool(cwd), wdGrepTool(cwd), wdFindTool(cwd), wdLsTool(cwd)}
}
