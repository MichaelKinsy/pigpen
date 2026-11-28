package lsp

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// upstream: packages/coding-agent/src/lsp/format-options.ts, packages/utils/src/tab-spacing.ts

const (
	minTabWidth          = 1
	maxTabWidth          = 16
	fallbackTabSize      = 2
	fallbackInsertSpaces = true
	nameMaxBytes         = 255
)

// LspFormattingOptions is the subset of LSP FormattingOptions sent on
// textDocument/formatting.
type LspFormattingOptions struct {
	TabSize                int  `json:"tabSize"`
	InsertSpaces           bool `json:"insertSpaces"`
	TrimTrailingWhitespace bool `json:"trimTrailingWhitespace"`
	InsertFinalNewline     bool `json:"insertFinalNewline"`
	TrimFinalNewlines      bool `json:"trimFinalNewlines"`
}

type DetectedIndent struct {
	TabSize      *int
	InsertSpaces *bool
}

// detectIndentFromContent sniffs insertSpaces and indent stride from content,
// skipping block-comment continuations so JSDoc ` *` lines never drag a 2-space
// stride down to 1 (issue #18544).
// upstream: packages/coding-agent/src/lsp/format-options.ts:detectIndentFromContent
func detectIndentFromContent(content string) DetectedIndent {
	if content == "" {
		return DetectedIndent{}
	}
	var insertSpaces *bool
	inBlockComment := false
	prevWidth := 0
	steps := make(map[int]int)

	for _, line := range strings.Split(content, "\n") {
		body := strings.TrimSpace(line)
		if body == "" {
			continue
		}
		if inBlockComment {
			if strings.Contains(body, "*/") {
				inBlockComment = false
			}
			continue
		}
		if strings.HasPrefix(body, "/*") && !strings.Contains(body[2:], "*/") {
			inBlockComment = true
		}
		if line[0] == '\t' {
			if insertSpaces == nil {
				f := false
				insertSpaces = &f
			}
			continue
		}
		width := 0
		for width < len(line) && line[width] == ' ' {
			width++
		}
		if width > 0 && insertSpaces == nil {
			t := true
			insertSpaces = &t
		}
		if width > prevWidth {
			step := width - prevWidth
			steps[step]++
		}
		prevWidth = width
	}

	var res DetectedIndent
	res.InsertSpaces = insertSpaces
	if insertSpaces != nil && *insertSpaces {
		if unit := mostFrequentStep(steps); unit > 0 {
			res.TabSize = &unit
		}
	}
	return res
}

func mostFrequentStep(steps map[int]int) int {
	best := 0
	bestCount := 0
	for step, count := range steps {
		if count > bestCount || (count == bestCount && (best == 0 || step < best)) {
			best = step
			bestCount = count
		}
	}
	return best
}

// resolveFormatOptions layers .editorconfig, content sniffing, and omp's 2-space fallback.
// upstream: packages/coding-agent/src/lsp/format-options.ts:resolveFormatOptions
func resolveFormatOptions(filePath, content string) LspFormattingOptions {
	fromConfig := getEditorConfigFormatting(filePath)
	detected := detectIndentFromContent(content)

	tabSize := fallbackTabSize
	if fromConfig.TabSize != nil {
		tabSize = *fromConfig.TabSize
	} else if detected.TabSize != nil {
		tabSize = *detected.TabSize
	}

	insertSpaces := fallbackInsertSpaces
	if fromConfig.InsertSpaces != nil {
		insertSpaces = *fromConfig.InsertSpaces
	} else if detected.InsertSpaces != nil {
		insertSpaces = *detected.InsertSpaces
	}

	return LspFormattingOptions{
		TabSize:                tabSize,
		InsertSpaces:           insertSpaces,
		TrimTrailingWhitespace: true,
		InsertFinalNewline:     true,
		TrimFinalNewlines:      true,
	}
}

type editorConfigSection struct {
	pattern    string
	properties map[string]string
}

type parsedEditorConfig struct {
	root     bool
	sections []editorConfigSection
}

func clampTabWidth(v int) int {
	if v < minTabWidth {
		return minTabWidth
	}
	if v > maxTabWidth {
		return maxTabWidth
	}
	return v
}

func parsePositiveInt(raw string) (int, bool) {
	if raw == "" {
		return 0, false
	}
	for i := range len(raw) {
		if raw[i] < '0' || raw[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n == 0 {
		return 0, false
	}
	return clampTabWidth(n), true
}

func parseEditorConfigFile(content string) parsedEditorConfig {
	var parsed parsedEditorConfig
	curIdx := -1
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") && len(line) >= 2 {
			pat := strings.TrimSpace(line[1 : len(line)-1])
			if pat == "" {
				curIdx = -1
				continue
			}
			parsed.sections = append(parsed.sections, editorConfigSection{
				pattern:    pat,
				properties: make(map[string]string),
			})
			curIdx = len(parsed.sections) - 1
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq == -1 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:eq]))
		val := strings.ToLower(strings.TrimSpace(line[eq+1:]))
		if key == "" {
			continue
		}
		if curIdx >= 0 {
			parsed.sections[curIdx].properties[key] = val
		} else if key == "root" {
			parsed.root = val == "true"
		}
	}
	return parsed
}

func matchesEditorConfigPattern(pattern, relativePath string) bool {
	norm := strings.TrimLeft(strings.ReplaceAll(pattern, "\\", "/"), "/")
	if norm == "" {
		return false
	}
	if opens, closes := strings.Count(norm, "{"), strings.Count(norm, "}"); opens > closes {
		norm += strings.Repeat("}", opens-closes)
	}
	if strings.Contains(norm, "/") {
		return matchGlob(norm, relativePath)
	}
	return matchGlob(norm, relativePath) || matchGlob("**/"+norm, relativePath)
}

func hasOverlongPathComponent(filePath string) bool {
	for _, part := range strings.FieldsFunc(filePath, func(r rune) bool { return r == '/' || r == '\\' }) {
		if len(part) > nameMaxBytes {
			return true
		}
	}
	return false
}

// getEditorConfigFormatting walks .editorconfig files upward from filePath.
// upstream: packages/utils/src/tab-spacing.ts:getEditorConfigFormatting
func getEditorConfigFormatting(filePath string) DetectedIndent {
	if filePath == "" {
		return DetectedIndent{}
	}
	abs, err := filepath.Abs(filePath)
	if err != nil || hasOverlongPathComponent(abs) {
		return DetectedIndent{}
	}

	type chainEntry struct {
		dir    string
		parsed parsedEditorConfig
	}
	var chain []chainEntry
	cursor := filepath.Dir(abs)
	for {
		cfgPath := filepath.Join(cursor, ".editorconfig")
		if data, readErr := os.ReadFile(cfgPath); readErr == nil {
			parsed := parseEditorConfigFile(string(data))
			chain = append(chain, chainEntry{dir: cursor, parsed: parsed})
			if parsed.root {
				break
			}
		}
		parent := filepath.Dir(cursor)
		if parent == cursor {
			break
		}
		cursor = parent
	}
	if len(chain) == 0 {
		return DetectedIndent{}
	}
	// Apply outermost -> innermost.
	var (
		style     string
		sizeKind  string // "spaces" | "tab"
		sizeN     int
		tabWidth  int
		hasTabWid bool
	)
	for i := len(chain) - 1; i >= 0; i-- {
		entry := chain[i]
		rel, relErr := filepath.Rel(entry.dir, abs)
		if relErr != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			rel = "."
		}
		relSlash := filepath.ToSlash(rel)
		for _, sec := range entry.parsed.sections {
			if !matchesEditorConfigPattern(sec.pattern, relSlash) {
				continue
			}
			if s, ok := sec.properties["indent_style"]; ok && (s == "space" || s == "tab") {
				style = s
			}
			if rawSize, ok := sec.properties["indent_size"]; ok {
				if rawSize == "tab" {
					sizeKind = "tab"
				} else if n, valid := parsePositiveInt(rawSize); valid {
					sizeKind = "spaces"
					sizeN = n
				}
			}
			if rawTW, ok := sec.properties["tab_width"]; ok {
				if n, valid := parsePositiveInt(rawTW); valid {
					tabWidth = n
					hasTabWid = true
				}
			}
		}
	}

	var out DetectedIndent
	if sizeKind == "spaces" {
		n := clampTabWidth(sizeN)
		out.TabSize = &n
	} else if hasTabWid {
		n := clampTabWidth(tabWidth)
		out.TabSize = &n
	}

	if style == "space" {
		t := true
		out.InsertSpaces = &t
	} else if style == "tab" || sizeKind == "tab" {
		f := false
		out.InsertSpaces = &f
	} else if sizeKind == "spaces" {
		t := true
		out.InsertSpaces = &t
	}
	return out
}
