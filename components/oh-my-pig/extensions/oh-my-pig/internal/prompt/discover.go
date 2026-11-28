package prompt

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// SkillEntry is one skill surfaced in the <skills> section of the system prompt.
// upstream: packages/coding-agent/src/extensibility/skills.ts:31-50
type SkillEntry struct {
	Name        string
	Description string
	FilePath    string
	Hide        bool
}

const maxPreviewChars = 100

var (
	whitespaceRe = regexp.MustCompile(`\s+`)
	sentenceRe   = regexp.MustCompile(`^.*?[.!?](?:\s|$)`)
)

// PreviewSkillDescription produces omp's deterministic bounded routing hint (≤100 runes).
// upstream: packages/coding-agent/src/extensibility/skill-descriptions.ts:113-121
func PreviewSkillDescription(description string) string {
	text := strings.TrimSpace(whitespaceRe.ReplaceAllString(description, " "))
	runes := []rune(text)
	if len(runes) <= maxPreviewChars {
		return text
	}
	boundary := string(runes[:maxPreviewChars-1])
	if loc := sentenceRe.FindStringIndex(boundary); loc != nil {
		sentence := strings.TrimRight(boundary[:loc[1]], " \t")
		if len([]rune(sentence)) >= 40 {
			return sentence
		}
	}
	if idx := strings.LastIndex(boundary, " "); idx >= 0 {
		if word := strings.TrimRight(boundary[:idx], " \t"); word != "" {
			return word + "…"
		}
	}
	return boundary + "…"
}

// DiscoverContextFiles walks up from cwd (plus user-level agentDir) to find
// AGENTS.md / CLAUDE.md files following omp's precedence and home-ceiling rules,
// expands @-imports, and deduplicates contained files.
// upstream: packages/coding-agent/src/discovery/helpers.ts:721-777, builtin.ts:942-973, system-prompt.ts:349-383
func DiscoverContextFiles(cwd, agentDir, home string) []ContextFile {
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	absHome := ""
	if home != "" {
		absHome, _ = filepath.Abs(home)
	}
	absCwd := ""
	if cwd != "" {
		absCwd, _ = filepath.Abs(cwd)
	}

	var files []ContextFile
	seenPaths := map[string]bool{}

	addFile := func(path string, depth int, hasDepth bool) {
		key := pathKey(path)
		if seenPaths[key] {
			return
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return
		}
		content := strings.TrimPrefix(string(data), "\uFEFF")
		if content == "" {
			return
		}
		seenPaths[key] = true
		expanded := ExpandAtImports(content, path, absHome)
		files = append(files, ContextFile{
			Path:     path,
			Content:  expanded,
			Depth:    depth,
			HasDepth: hasDepth,
		})
	}

	// 1. User-level context file in agentDir (~/.pig/agent/AGENTS.md or ~/.omp/agent/AGENTS.md).
	if agentDir != "" {
		for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
			candidate := filepath.Join(agentDir, name)
			if isRegularNonEmpty(candidate) {
				addFile(candidate, 0, false)
				break
			}
		}
	}

	if absCwd == "" {
		return DedupeContainedContextFiles(files)
	}

	// 2. Standalone walk-up from cwd (AGENTS.md preferred over CLAUDE.md at the same depth).
	// upstream: discovery/helpers.ts:721-777 (loadStandaloneContextFiles)
	repoRoot := findGitRoot(absCwd)
	cwdUnderHome := absHome != "" && isWithinDir(absHome, absCwd)
	repoIsHome := repoRoot != "" && absHome != "" && sameCleanPath(absHome, repoRoot)
	repoUnderHome := repoRoot != "" && absHome != "" && isWithinDir(absHome, repoRoot) && !repoIsHome
	scanToHome := repoRoot != "" && cwdUnderHome && repoUnderHome

	boundary := repoRoot
	switch {
	case scanToHome:
		boundary = absHome
	case boundary == "":
		if cwdUnderHome {
			boundary = absHome
		} else {
			boundary = volumeRoot(absCwd)
		}
	}
	includeBoundary := false
	if repoRoot == "" {
		includeBoundary = cwdUnderHome
	} else {
		includeBoundary = absHome == "" || !sameCleanPath(boundary, absHome) || repoIsHome
	}
	excludeHome := scanToHome

	cur := absCwd
	depth := 0
	for {
		atBoundary := boundary != "" && sameCleanPath(cur, boundary)
		atHome := excludeHome && absHome != "" && sameCleanPath(cur, absHome)
		if !(atHome || (atBoundary && !includeBoundary)) {
			base := filepath.Base(cur)
			if !strings.HasPrefix(base, ".") {
				// Per-depth priority: AGENTS.override.md > AGENTS.md > CLAUDE.md (non-empty wins).
				for _, name := range []string{"AGENTS.override.md", "AGENTS.md", "CLAUDE.md"} {
					candidate := filepath.Join(cur, name)
					if isRegularNonEmpty(candidate) {
						addFile(candidate, depth, true)
						break
					}
				}
			}
		}
		if atBoundary {
			break
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
		depth++
	}

	return DedupeContainedContextFiles(files)
}

// LoadPersonalityOverride loads <agentDir>/PERSONALITY.md if present and non-empty.
// upstream: packages/coding-agent/src/system-prompt.ts:109-124
func LoadPersonalityOverride(agentDir string) string {
	if agentDir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(agentDir, "PERSONALITY.md"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// DiscoverStickyRules loads <agentDir>/RULES.md and nearest ancestor .omp/RULES.md
// as always-apply rules, matching omp's sticky RULES.md discovery.
// upstream: packages/coding-agent/src/discovery/builtin.ts:417-452
func DiscoverStickyRules(cwd, agentDir, home string) []AlwaysApplyRule {
	var rules []AlwaysApplyRule
	if agentDir != "" {
		p := filepath.Join(agentDir, "RULES.md")
		if body := readRuleBody(p); body != "" {
			rules = append(rules, AlwaysApplyRule{Name: "RULES", Content: body, Path: p})
		}
	}
	if cwd != "" {
		if dir := findNearestProjectOmpDir(cwd, home); dir != "" {
			p := filepath.Join(dir, "RULES.md")
			if body := readRuleBody(p); body != "" {
				rules = append(rules, AlwaysApplyRule{Name: "RULES@project", Content: body, Path: p})
			}
		}
	}
	return rules
}

func findNearestProjectOmpDir(cwd, home string) string {
	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		return ""
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	absHome := ""
	if home != "" {
		absHome, _ = filepath.Abs(home)
	}
	stopAt := findGitRoot(absCwd)
	if stopAt == "" {
		stopAt = absHome
	}
	ceilings := projectDiscoveryCeilings(absHome)

	cur := absCwd
	for {
		if ceilings[pathKey(cur)] {
			return ""
		}
		if absHome == "" || !sameCleanPath(cur, absHome) {
			ompDir := filepath.Join(cur, ".omp")
			if info, err := os.Stat(ompDir); err == nil && info.IsDir() {
				return ompDir
			}
		}
		if stopAt != "" && sameCleanPath(cur, stopAt) {
			return ""
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

func projectDiscoveryCeilings(absHome string) map[string]bool {
	ceilings := map[string]bool{}
	if absHome != "" {
		ceilings[pathKey(absHome)] = true
	}
	if tmp := os.TempDir(); tmp != "" {
		if absTmp, err := filepath.Abs(tmp); err == nil {
			ceilings[pathKey(absTmp)] = true
		}
	}
	if runtime.GOOS != "windows" {
		ceilings[pathKey("/tmp")] = true
		ceilings[pathKey("/var/tmp")] = true
	}
	return ceilings
}

// readRuleBody reads a markdown rule file, stripping optional YAML frontmatter
// and returning empty if enabled: false or empty body.
func readRuleBody(filePath string) string {
	info, err := os.Stat(filePath)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return ""
	}
	text := strings.TrimPrefix(string(data), "\uFEFF")
	fm, body := splitFrontmatter(text)
	if frontmatterDisabled(fm) {
		return ""
	}
	return strings.TrimSpace(body)
}

func splitFrontmatter(text string) (frontmatter, body string) {
	if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
		return "", text
	}
	rest := text[4:]
	if strings.HasPrefix(text, "---\r\n") {
		rest = text[5:]
	}
	lines := strings.Split(rest, "\n")
	for i, line := range lines {
		trimmed := strings.TrimRight(line, "\r")
		if trimmed == "---" {
			return strings.Join(lines[:i], "\n"), strings.Join(lines[i+1:], "\n")
		}
	}
	return "", text
}

func frontmatterDisabled(fm string) bool {
	for _, line := range strings.Split(fm, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.TrimSpace(k) == "enabled" && strings.EqualFold(strings.TrimSpace(v), "false") {
			return true
		}
	}
	return false
}

func isRegularNonEmpty(filePath string) bool {
	info, err := os.Stat(filePath)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

func isWithinDir(parent, child string) bool {
	p := filepath.Clean(parent)
	c := filepath.Clean(child)
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
		c = strings.ToLower(c)
	}
	rel, err := filepath.Rel(p, c)
	if err != nil {
		return false
	}
	return rel == "" || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

func volumeRoot(p string) string {
	vol := filepath.VolumeName(p)
	return vol + string(filepath.Separator)
}

func pathKey(p string) string {
	c := filepath.Clean(p)
	if runtime.GOOS == "windows" {
		return strings.ToLower(c)
	}
	return c
}
