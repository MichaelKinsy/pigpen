package tintinweb_subagents

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// preloadedSkill is a skill whose content goes into the agent's system prompt. upstream: src/skill-loader.ts PreloadedSkill.
type preloadedSkill struct {
	Name    string
	Content string
}

// preloadSkills loads the named skills. Roots, in precedence order: <cwd>/.pi/skills, <cwd>/.agents/skills, <agent
// dir>/skills, ~/.agents/skills and ~/.pi/skills. Per root a skill is <root>/<name>.md or <root>/.../<name>/SKILL.md; a
// directory that holds a SKILL.md is a skill and is not searched further; dotfile directories and node_modules are
// skipped; symlinks are refused. A skill that is not found, or whose name is unsafe, is a placeholder text, not an
// error. upstream: src/skill-loader.ts preloadSkills.
func preloadSkills(names []string, cwd string) []preloadedSkill {
	out := make([]preloadedSkill, 0, len(names))
	for _, name := range names {
		out = append(out, preloadedSkill{Name: name, Content: loadSkillContent(name, cwd)})
	}
	return out
}

func loadSkillContent(name, cwd string) string {
	if isUnsafeName(name) {
		return `(Skill "` + name + `" skipped: name contains path traversal characters)`
	}
	home, _ := os.UserHomeDir()
	roots := []string{
		filepath.Join(cwd, ".pi", "skills"),
		filepath.Join(cwd, ".agents", "skills"),
		filepath.Join(agentDir(), "skills"),
		filepath.Join(home, ".agents", "skills"),
		filepath.Join(home, ".pi", "skills"),
	}
	for _, root := range roots {
		if content, ok := findInRoot(root, name); ok {
			return content
		}
	}
	return `(Skill "` + name + `" not found in .pi/skills/, .agents/skills/, or global skill locations)`
}

func findInRoot(root, name string) (string, bool) {
	if isSymlink(root) { // a symlinked root is rejected entirely
		return "", false
	}
	if flat, ok := safeReadFile(filepath.Join(root, name+".md")); ok {
		return strings.TrimSpace(flat), true
	}
	return findSkillDirectory(root, name)
}

// findSkillDirectory searches breadth first under root for a directory called name that holds a SKILL.md, in byte
// order of the names.
func findSkillDirectory(root, name string) (string, bool) {
	if _, err := os.Stat(root); err != nil {
		return "", false
	}
	queue := []string{root}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		entries, err := os.ReadDir(current)
		if err != nil {
			continue
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			// A DirEntry reports the entry itself, so a symlinked directory is not a directory here.
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" {
				continue
			}
			path := filepath.Join(current, entry.Name())
			skillMd := filepath.Join(path, "SKILL.md")
			if _, err := os.Stat(skillMd); err == nil { // a skill directory: skills do not nest
				if entry.Name() == name {
					if content, ok := safeReadFile(skillMd); ok {
						return strings.TrimSpace(content), true
					}
				}
				continue
			}
			queue = append(queue, path)
		}
	}
	return "", false
}
