package memoryskills

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/MichaelKinsy/pigpen/components/pig-hermes-core/paths"
)

// Context file manifest status values (matching Hermes's agent/context_file_sources.py).
const (
	ContextStatusLoaded    = "loaded"
	ContextStatusShadowed  = "shadowed"
	ContextStatusTruncated = "truncated"
	ContextStatusEmpty     = "empty"
	ContextStatusMissing   = "missing"
)

// ContextFileEntry describes one candidate context file in the read-only /context manifest.
type ContextFileEntry struct {
	Kind      string `json:"kind"`
	Label     string `json:"label"`
	Path      string `json:"path"`
	Status    string `json:"status"` // loaded, shadowed, truncated, empty, missing
	Loaded    bool   `json:"loaded"`
	Chars     int    `json:"chars"`
	EstTokens int    `json:"est_tokens"`
}

// BuildContextManifest inspects every candidate context file in Hermes's priority order
// (SOUL.md, HERMES.md/.hermes.md, AGENTS.override.md/AGENTS.md/agents.md, CLAUDE.md/claude.md,
// .cursorrules, and .cursor/rules/*.mdc) without mutating prompt state or caches.
func BuildContextManifest(cwd string, p paths.ProfilePaths) []ContextFileEntry {
	var entries []ContextFileEntry

	// 1. Profile SOUL.md (always independent of project context priority).
	soulPath := strings.TrimSpace(p.SoulFile)
	if soulPath == "" && strings.TrimSpace(p.Home) != "" {
		soulPath = filepath.Join(p.Home, "SOUL.md")
	}
	if soulPath != "" {
		entries = append(entries, inspectSingleContextFile("soul", "SOUL.md", soulPath, false, false))
	}

	absCwd := ""
	if strings.TrimSpace(cwd) != "" {
		if abs, err := filepath.Abs(cwd); err == nil {
			absCwd = abs
		}
	}
	if absCwd == "" {
		return entries
	}

	// Track which project context kind won (first non-empty kind wins; lower-priority kinds are shadowed).
	winner := ""

	// 2. .hermes.md / HERMES.md (nearest match walking cwd -> git root).
	hermesEntries, hermesWon := inspectHermesMDCandidates(absCwd, winner)
	entries = append(entries, hermesEntries...)
	if hermesWon && winner == "" {
		winner = "hermes_md"
	}

	// 3. AGENTS.override.md / AGENTS.md / agents.md (git root -> cwd directory chain).
	agentsEntries, agentsWon := inspectAgentsMDCandidates(absCwd, winner)
	entries = append(entries, agentsEntries...)
	if agentsWon && winner == "" {
		winner = "agents_md"
	}

	// 4. CLAUDE.md / claude.md (cwd only).
	claudeEntries, claudeWon := inspectClaudeMDCandidates(absCwd, winner)
	entries = append(entries, claudeEntries...)
	if claudeWon && winner == "" {
		winner = "claude_md"
	}

	// 5. .cursorrules + .cursor/rules/*.mdc (cwd only).
	cursorEntries, _ := inspectCursorRulesCandidates(absCwd, winner)
	entries = append(entries, cursorEntries...)

	return entries
}

// RenderContextManifest formats a ContextFileEntry slice as a human-readable table for /context.
func RenderContextManifest(entries []ContextFileEntry) string {
	if len(entries) == 0 {
		return "  (no context files configured)"
	}

	width := 0
	for _, e := range entries {
		if len(e.Label) > width {
			width = len(e.Label)
		}
	}

	var b strings.Builder
	for i, e := range entries {
		if i > 0 {
			b.WriteByte('\n')
		}
		glyph, note := contextStatusDisplay(e.Status)
		suffix := ""
		if note != "" {
			suffix = "  (" + note + ")"
		}
		fmt.Fprintf(&b, "  %s %-*s  %-9s  %6d chars  ~%5d tok%s",
			glyph, width, e.Label, e.Status, e.Chars, e.EstTokens, suffix)
	}
	return b.String()
}

func contextStatusDisplay(status string) (glyph, note string) {
	switch status {
	case ContextStatusLoaded:
		return "✓", ""
	case ContextStatusTruncated:
		return "◐", "truncated — over context file cap"
	case ContextStatusShadowed:
		return "○", "not loaded — higher-priority context wins"
	case ContextStatusEmpty:
		return "○", "not loaded — empty file"
	case ContextStatusMissing:
		return "·", "not present"
	default:
		return "•", ""
	}
}

func inspectHermesMDCandidates(absCwd, winner string) ([]ContextFileEntry, bool) {
	gitRoot := findGitRoot(absCwd)
	dirs := []string{absCwd}
	if gitRoot != "" {
		dirs = walkUpToRoot(absCwd, gitRoot)
	}

	var found []ContextFileEntry
	won := false
	for _, dir := range dirs {
		for _, name := range []string{".hermes.md", "HERMES.md"} {
			candidate := filepath.Join(dir, name)
			if !fileExists(candidate) {
				continue
			}
			label := relOrBase(absCwd, candidate)
			shadowed := winner != "" || won
			entry := inspectSingleContextFile("hermes_md", label, candidate, shadowed, true)
			found = append(found, entry)
			if entry.Loaded {
				won = true
			}
		}
	}
	if len(found) == 0 {
		// Report canonical HERMES.md slot as missing so /context shows the full priority table.
		found = append(found, ContextFileEntry{
			Kind:   "hermes_md",
			Label:  "HERMES.md",
			Path:   filepath.Join(absCwd, "HERMES.md"),
			Status: ContextStatusMissing,
		})
	}
	return found, won
}

func inspectAgentsMDCandidates(absCwd, winner string) ([]ContextFileEntry, bool) {
	gitRoot := findGitRoot(absCwd)
	var chain []string
	if gitRoot == "" || gitRoot == absCwd {
		chain = []string{absCwd}
	} else {
		up := walkUpToRoot(absCwd, gitRoot)
		chain = make([]string, len(up))
		for i := range up {
			chain[i] = up[len(up)-1-i]
		}
	}

	var found []ContextFileEntry
	won := false
	for _, dir := range chain {
		dirWinner := false
		for _, name := range []string{"AGENTS.override.md", "AGENTS.md", "agents.md"} {
			candidate := filepath.Join(dir, name)
			if !fileExists(candidate) {
				continue
			}
			label := relOrBase(absCwd, candidate)
			shadowed := winner != "" || dirWinner
			entry := inspectSingleContextFile("agents_md", label, candidate, shadowed, false)
			found = append(found, entry)
			if entry.Loaded {
				dirWinner = true
				won = true
			}
		}
	}
	if len(found) == 0 {
		found = append(found, ContextFileEntry{
			Kind:   "agents_md",
			Label:  "AGENTS.md",
			Path:   filepath.Join(absCwd, "AGENTS.md"),
			Status: ContextStatusMissing,
		})
	}
	return found, won
}

func inspectClaudeMDCandidates(absCwd, winner string) ([]ContextFileEntry, bool) {
	var found []ContextFileEntry
	won := false
	for _, name := range []string{"CLAUDE.md", "claude.md"} {
		candidate := filepath.Join(absCwd, name)
		if !fileExists(candidate) {
			continue
		}
		shadowed := winner != "" || won
		entry := inspectSingleContextFile("claude_md", name, candidate, shadowed, false)
		found = append(found, entry)
		if entry.Loaded {
			won = true
		}
	}
	if len(found) == 0 {
		found = append(found, ContextFileEntry{
			Kind:   "claude_md",
			Label:  "CLAUDE.md",
			Path:   filepath.Join(absCwd, "CLAUDE.md"),
			Status: ContextStatusMissing,
		})
	}
	return found, won
}

func inspectCursorRulesCandidates(absCwd, winner string) ([]ContextFileEntry, bool) {
	var found []ContextFileEntry
	won := false
	shadowed := winner != ""

	crPath := filepath.Join(absCwd, ".cursorrules")
	if fileExists(crPath) {
		entry := inspectSingleContextFile("cursorrules", ".cursorrules", crPath, shadowed, false)
		found = append(found, entry)
		if entry.Loaded {
			won = true
		}
	}

	rulesDir := filepath.Join(absCwd, ".cursor", "rules")
	if dirEntries, err := os.ReadDir(rulesDir); err == nil {
		var mdcNames []string
		for _, de := range dirEntries {
			if !de.IsDir() && strings.HasSuffix(de.Name(), ".mdc") {
				mdcNames = append(mdcNames, de.Name())
			}
		}
		sort.Strings(mdcNames)
		for _, name := range mdcNames {
			p := filepath.Join(rulesDir, name)
			entry := inspectSingleContextFile("cursorrules", ".cursor/rules/"+name, p, shadowed, false)
			found = append(found, entry)
			if entry.Loaded {
				won = true
			}
		}
	}

	if len(found) == 0 {
		found = append(found, ContextFileEntry{
			Kind:   "cursorrules",
			Label:  ".cursorrules",
			Path:   crPath,
			Status: ContextStatusMissing,
		})
	}
	return found, won
}

func inspectSingleContextFile(kind, label, path string, shadowed, stripFrontmatter bool) ContextFileEntry {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return ContextFileEntry{
			Kind:   kind,
			Label:  label,
			Path:   path,
			Status: ContextStatusMissing,
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ContextFileEntry{
			Kind:   kind,
			Label:  label,
			Path:   path,
			Status: ContextStatusEmpty,
		}
	}
	content := strings.TrimSpace(strings.TrimPrefix(string(raw), "\ufeff"))
	if stripFrontmatter && content != "" {
		content = strings.TrimSpace(stripYAMLFrontmatter(content))
	}
	if content == "" {
		return ContextFileEntry{
			Kind:   kind,
			Label:  label,
			Path:   path,
			Status: ContextStatusEmpty,
		}
	}

	chars := len(content)
	estTokens := estimateTokensRough(content)
	if shadowed {
		return ContextFileEntry{
			Kind:      kind,
			Label:     label,
			Path:      path,
			Status:    ContextStatusShadowed,
			Loaded:    false,
			Chars:     chars,
			EstTokens: estTokens,
		}
	}

	renderedLen := chars
	if kind != "soul" {
		renderedLen = len(fmt.Sprintf("## %s\n\n%s", label, content))
	}
	status := ContextStatusLoaded
	if renderedLen > defaultContextFileMaxChars {
		status = ContextStatusTruncated
	}
	return ContextFileEntry{
		Kind:      kind,
		Label:     label,
		Path:      path,
		Status:    status,
		Loaded:    true,
		Chars:     chars,
		EstTokens: estTokens,
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// estimateTokensRough matches Hermes's rough 4-chars-per-token heuristic.
func estimateTokensRough(content string) int {
	n := len(content)
	if n == 0 {
		return 0
	}
	return (n + 3) / 4
}
