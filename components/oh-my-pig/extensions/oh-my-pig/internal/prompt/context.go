package prompt

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ContextFile is one discovered or host-provided instruction file (AGENTS.md / CLAUDE.md).
// upstream: packages/coding-agent/src/system-prompt.ts:323-383
type ContextFile struct {
	Path    string
	Content string
	// Depth is the directory distance from cwd (0 = cwd, 1 = parent, ...).
	// HasDepth false marks user-level / undated files, which sort as less authoritative than any project file.
	Depth    int
	HasDepth bool
}

// AlwaysApplyRule is an always-apply rule body injected under <generic-rules>.
// upstream: packages/coding-agent/src/system-prompt.ts:126-130
type AlwaysApplyRule struct {
	Name    string
	Content string
	Path    string
}

// Rule is a domain-scoped rule entry rendered under <domain-rules>.
// upstream: packages/coding-agent/src/system-prompt.ts:527
type Rule struct {
	Name        string
	Description string
	Path        string
	Globs       []string
}

// DedupeContainedContextFiles drops any context file whose normalized paragraph sequence
// appears contiguously inside a more-authoritative (lower depth, closer-to-cwd) file.
// Returns files sorted by depth descending (farther from cwd first, closer last).
// upstream: packages/coding-agent/src/system-prompt.ts:323-342
func DedupeContainedContextFiles(files []ContextFile) []ContextFile {
	if len(files) == 0 {
		return nil
	}
	sorted := make([]ContextFile, len(files))
	copy(sorted, files)
	sort.SliceStable(sorted, func(i, j int) bool {
		// !HasDepth is +Inf (farther / less authoritative, comes first).
		if !sorted[i].HasDepth && !sorted[j].HasDepth {
			return false
		}
		if !sorted[i].HasDepth {
			return true
		}
		if !sorted[j].HasDepth {
			return false
		}
		return sorted[i].Depth > sorted[j].Depth
	})

	blocks := make([][]string, len(sorted))
	for i, f := range sorted {
		blocks[i] = splitComparablePromptBlocks(f.Content)
	}

	out := make([]ContextFile, 0, len(sorted))
	for i, f := range sorted {
		contained := false
		for j := i + 1; j < len(sorted); j++ {
			if promptBlocksContain(blocks[j], blocks[i]) {
				contained = true
				break
			}
		}
		if !contained {
			out = append(out, f)
		}
	}
	return out
}

// dedupeAlwaysApplyRules omits always-apply rules already contained in another prompt source.
// upstream: packages/coding-agent/src/system-prompt.ts:184-193
func dedupeAlwaysApplyRules(rules []AlwaysApplyRule, sources []string) []AlwaysApplyRule {
	if len(rules) == 0 {
		return nil
	}
	sourceBlocks := make([][]string, len(sources))
	for i, s := range sources {
		sourceBlocks[i] = splitComparablePromptBlocks(s)
	}
	var out []AlwaysApplyRule
	for _, r := range rules {
		ruleBlocks := splitComparablePromptBlocks(r.Content)
		dup := false
		for _, sb := range sourceBlocks {
			if promptBlocksContain(sb, ruleBlocks) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, r)
		}
	}
	return out
}

var fenceOpenRe = regexp.MustCompile(`^\s*(?:` + "```" + `|~~~)`)

// splitComparablePromptBlocks normalizes content and splits on blank-line paragraph
// boundaries outside fenced code blocks.
// upstream: packages/coding-agent/src/system-prompt.ts:136-163
func splitComparablePromptBlocks(content string) []string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil
	}
	rendered := strings.TrimSpace(formatPrompt(trimmed))
	var blocks []string
	var current []string
	inFence := false
	for _, line := range strings.Split(rendered, "\n") {
		if fenceOpenRe.MatchString(line) {
			inFence = !inFence
			current = append(current, line)
			continue
		}
		if !inFence && strings.TrimSpace(line) == "" && len(current) > 0 && strings.TrimSpace(current[len(current)-1]) != "" {
			if block := strings.TrimSpace(strings.Join(current, "\n")); block != "" {
				blocks = append(blocks, block)
			}
			current = current[:0]
			continue
		}
		current = append(current, line)
	}
	if tail := strings.TrimSpace(strings.Join(current, "\n")); tail != "" {
		blocks = append(blocks, tail)
	}
	return blocks
}

// promptBlocksContain checks whether ruleBlocks appears as a contiguous subsequence of sourceBlocks.
// upstream: packages/coding-agent/src/system-prompt.ts:170-178
func promptBlocksContain(sourceBlocks, ruleBlocks []string) bool {
	if len(sourceBlocks) == 0 || len(ruleBlocks) == 0 || len(ruleBlocks) > len(sourceBlocks) {
		return false
	}
	for start := 0; start <= len(sourceBlocks)-len(ruleBlocks); start++ {
		match := true
		for offset := range ruleBlocks {
			if sourceBlocks[start+offset] != ruleBlocks[offset] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// contextDepth computes directory depth relative to cwd when filePath is at or above cwd;
// returns HasDepth=false for files outside cwd's ancestor chain (e.g. ~/.pig/agent/AGENTS.md).
func contextDepth(cwd, filePath string) (int, bool) {
	if cwd == "" || filePath == "" {
		return 0, false
	}
	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		return 0, false
	}
	absFile, err := filepath.Abs(filePath)
	if err != nil {
		return 0, false
	}
	fileDir := filepath.Dir(absFile)
	dir := absCwd
	depth := 0
	for {
		if sameCleanPath(dir, fileDir) {
			return depth, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return 0, false
		}
		dir = parent
		depth++
	}
}

// MaxAtImportDepth caps recursive @-imports inside context files.
// upstream: packages/coding-agent/src/discovery/at-imports.ts:33
const (
	// MaxAtImportDepth is the maximum recursive @-import hop depth.
	// upstream: packages/coding-agent/src/discovery/at-imports.ts:38
	MaxAtImportDepth = 5
	// MaxAtImportExpansions caps total file reads in one top-level document.
	// upstream: packages/coding-agent/src/discovery/at-imports.ts:41
	MaxAtImportExpansions = 256
)

var (
	atImportRe    = regexp.MustCompile(`(^|[ \t])@([./~A-Za-z0-9_-](?:\\ |[^\s])*)`)
	trailingPunct = regexp.MustCompile(`[.,;:!?)\]}"']+$`)
)

// ExpandAtImports expands @path/to/file includes in a context file relative to filePath's directory,
// skipping fenced code blocks and inline code spans and breaking cycles.
// upstream: packages/coding-agent/src/discovery/at-imports.ts:75-199
func ExpandAtImports(content, filePath, home string) string {
	if !strings.Contains(content, "@") {
		return content
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	absSource, err := filepath.Abs(filePath)
	if err != nil {
		absSource = filePath
	}
	chain := []string{absSource}
	remaining := MaxAtImportExpansions
	return expandAtImportsRec(content, filepath.Dir(absSource), 0, MaxAtImportDepth, home, chain, &remaining)
}

func expandAtImportsRec(content, baseDir string, depth, maxDepth int, home string, chain []string, remaining *int) string {
	if depth >= maxDepth {
		return content
	}
	segments := splitMarkdownSegments(content)
	var out strings.Builder
	out.Grow(len(content))
	for _, seg := range segments {
		if seg.isCode {
			out.WriteString(seg.text)
			continue
		}
		out.WriteString(expandAtTextSegment(seg.text, baseDir, depth, maxDepth, home, chain, remaining))
	}
	return out.String()
}

func expandAtTextSegment(text, baseDir string, depth, maxDepth int, home string, chain []string, remaining *int) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = expandAtLine(line, baseDir, depth, maxDepth, home, chain, remaining)
	}
	return strings.Join(lines, "\n")
}
func expandAtLine(line, baseDir string, depth, maxDepth int, home string, chain []string, remaining *int) string {
	if !strings.Contains(line, "@") {
		return line
	}
	indices := atImportRe.FindAllStringSubmatchIndex(line, -1)
	if len(indices) == 0 {
		return line
	}

	type matchSpan struct {
		start      int
		end        int
		importPath string
	}
	var matches []matchSpan
	for _, idx := range indices {
		leadingLen := idx[3] - idx[2]
		atPos := idx[0] + leadingLen
		if isInsideInlineCode(line, atPos) {
			continue
		}
		rawToken := line[idx[4]:idx[5]]
		trimmedToken := trailingPunct.ReplaceAllString(rawToken, "")
		if trimmedToken == "" {
			continue
		}
		matches = append(matches, matchSpan{
			start:      atPos,
			end:        atPos + 1 + len(trimmedToken),
			importPath: strings.ReplaceAll(trimmedToken, `\ `, " "),
		})
	}
	if len(matches) == 0 {
		return line
	}

	var out strings.Builder
	cursor := 0
	for _, m := range matches {
		out.WriteString(line[cursor:m.start])
		if expanded, ok := resolveAndExpandAt(m.importPath, baseDir, depth, maxDepth, home, chain, remaining); ok {
			out.WriteString(expanded)
		} else {
			out.WriteString(line[m.start:m.end])
		}
		cursor = m.end
	}
	out.WriteString(line[cursor:])
	return out.String()
}

func resolveAndExpandAt(importPath, baseDir string, depth, maxDepth int, home string, chain []string, remaining *int) (string, bool) {
	if *remaining <= 0 {
		return "", false
	}
	resolved := resolveImportPath(importPath, baseDir, home)
	// Only an ancestor in the current import chain counts as a cycle; a file
	// imported multiple times from unrelated places is expanded at each occurrence.
	// upstream: packages/coding-agent/src/discovery/at-imports.ts:184-189
	for _, ancestor := range chain {
		if ancestor == resolved {
			return "", false
		}
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return "", false
	}
	*remaining--
	nextChain := make([]string, len(chain)+1)
	copy(nextChain, chain)
	nextChain[len(chain)] = resolved
	return expandAtImportsRec(string(data), filepath.Dir(resolved), depth+1, maxDepth, home, nextChain, remaining), true
}
func resolveImportPath(importPath, baseDir, home string) string {
	switch {
	case importPath == "~":
		abs, _ := filepath.Abs(home)
		return abs
	case strings.HasPrefix(importPath, "~/"):
		abs, _ := filepath.Abs(filepath.Join(home, importPath[2:]))
		return abs
	case filepath.IsAbs(importPath):
		abs, _ := filepath.Abs(importPath)
		return abs
	default:
		abs, _ := filepath.Abs(filepath.Join(baseDir, importPath))
		return abs
	}
}

type markdownSegment struct {
	isCode bool
	text   string
}

func splitMarkdownSegments(content string) []markdownSegment {
	lines := strings.Split(content, "\n")
	var segments []markdownSegment
	var buf strings.Builder
	inCode := false
	var fenceChar byte
	fenceLen := 0

	flush := func() {
		if buf.Len() == 0 {
			return
		}
		segments = append(segments, markdownSegment{isCode: inCode, text: buf.String()})
		buf.Reset()
	}

	for i, line := range lines {
		isLast := i == len(lines)-1
		lineText := line
		if !isLast {
			lineText += "\n"
		}
		ch, flen := matchFence(line)
		if flen > 0 && !inCode {
			flush()
			inCode = true
			fenceChar = ch
			fenceLen = flen
			buf.WriteString(lineText)
		} else if flen > 0 && inCode && ch == fenceChar && flen >= fenceLen {
			buf.WriteString(lineText)
			flush()
			inCode = false
			fenceChar = 0
			fenceLen = 0
		} else {
			buf.WriteString(lineText)
		}
		if isLast {
			flush()
		}
	}
	return segments
}

func matchFence(line string) (byte, int) {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i >= len(line) || (line[i] != '`' && line[i] != '~') {
		return 0, 0
	}
	ch := line[i]
	n := 0
	for i+n < len(line) && line[i+n] == ch {
		n++
	}
	if n < 3 {
		return 0, 0
	}
	return ch, n
}

func isInsideInlineCode(line string, position int) bool {
	inSpan := false
	i := 0
	for i < position && i < len(line) {
		if line[i] == '`' {
			for i < len(line) && line[i] == '`' {
				i++
			}
			inSpan = !inSpan
		} else {
			i++
		}
	}
	return inSpan
}
