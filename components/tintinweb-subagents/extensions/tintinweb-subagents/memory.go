package tintinweb_subagents

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf16"
)

// Persistent agent memory: per-agent memory directories that persist across sessions. upstream: src/memory.ts.
//
// Scopes: "user" is <agent dir>/agent-memory/<name>/, "project" is <cwd>/.pi/agent-memory/<name>/, "local" is
// <cwd>/.pi/agent-memory-local/<name>/. The user scope once hardcoded ~/.pi/agent-memory/; that location is still
// used (read and write) when it exists and the new one does not, so existing memories are not orphaned.

// maxMemoryLines is how many lines of MEMORY.md are read. upstream: memory.ts MAX_MEMORY_LINES.
const maxMemoryLines = 200

var safeName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// isUnsafeName: only alphanumerics, hyphens, underscores and dots (no leading dot), at most 128 UTF-16 units.
func isUnsafeName(name string) bool {
	if name == "" || len(utf16.Encode([]rune(name))) > 128 {
		return true
	}
	return !safeName.MatchString(name)
}

// isSymlink is true for a symlink (defense against symlink attacks); a missing path is not one.
func isSymlink(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode()&os.ModeSymlink != 0
}

// safeReadFile reads a file, refusing a symlink; ok is false for a missing, symlinked or unreadable file.
func safeReadFile(path string) (string, bool) {
	if _, err := os.Stat(path); err != nil {
		return "", false
	}
	if isSymlink(path) {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return strings.ToValidUTF8(string(data), "\uFFFD"), true
}

// resolveMemoryDir is the memory directory of an agent for a scope; an unsafe name is an error.
func resolveMemoryDir(agentName, scope, cwd string) (string, error) {
	if isUnsafeName(agentName) {
		return "", fmt.Errorf("Unsafe agent name for memory directory: %q", agentName)
	}
	switch scope {
	case "user":
		current := filepath.Join(agentDir(), "agent-memory", agentName)
		home, _ := os.UserHomeDir()
		legacy := filepath.Join(home, ".pi", "agent-memory", agentName)
		if !exists(current) && exists(legacy) && !isSymlink(legacy) {
			return legacy, nil
		}
		return current, nil
	case "project":
		return filepath.Join(cwd, ".pi", "agent-memory", agentName), nil
	case "local":
		return filepath.Join(cwd, ".pi", "agent-memory-local", agentName), nil
	}
	return "", fmt.Errorf("unknown memory scope %q", scope)
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

// ensureMemoryDir creates the directory; it refuses a symlinked one.
func ensureMemoryDir(dir string) error {
	if exists(dir) {
		if isSymlink(dir) {
			return errors.New("Refusing to use symlinked memory directory: " + dir)
		}
		return nil
	}
	return os.MkdirAll(dir, 0o777)
}

// readMemoryIndex is the first 200 lines of MEMORY.md; ok is false when there is none or the path is a symlink.
func readMemoryIndex(dir string) (string, bool) {
	if isSymlink(dir) {
		return "", false
	}
	content, ok := safeReadFile(filepath.Join(dir, "MEMORY.md"))
	if !ok {
		return "", false
	}
	lines := strings.Split(content, "\n")
	if len(lines) > maxMemoryLines {
		return strings.Join(lines[:maxMemoryLines], "\n") + "\n... (truncated at 200 lines)", true
	}
	return content, true
}

// buildMemoryBlock is the memory block of an agent's system prompt; it creates the memory directory.
func buildMemoryBlock(agentName, scope, cwd string) (string, error) {
	memoryDir, err := resolveMemoryDir(agentName, scope, cwd)
	if err != nil {
		return "", err
	}
	if err := ensureMemoryDir(memoryDir); err != nil {
		return "", err
	}
	existing, ok := readMemoryIndex(memoryDir)
	header := "# Agent Memory\n\nYou have a persistent memory directory at: " + memoryDir + "/\nMemory scope: " + scope +
		"\n\nThis memory persists across sessions. Use it to build up knowledge over time."
	content := "\n\nNo MEMORY.md exists yet. Create one at " + filepath.Join(memoryDir, "MEMORY.md") + " to start building persistent memory."
	if ok && existing != "" {
		content = "\n\n## Current MEMORY.md\n" + existing
	}
	instructions := "\n\n## Memory Instructions\n" +
		"- MEMORY.md is an index file — keep it concise (under 200 lines). Lines after 200 are truncated.\n" +
		"- Store detailed memories in separate files within " + memoryDir + "/ and link to them from MEMORY.md.\n" +
		"- Each memory file should use this frontmatter format:\n" +
		"  ```markdown\n  ---\n  name: <memory name>\n  description: <one-line description>\n  type: <user|feedback|project|reference>\n  ---\n  <memory content>\n  ```\n" +
		"- Update or remove memories that become outdated. Check for existing memories before creating duplicates.\n" +
		"- You have Read, Write, and Edit tools available for managing memory files."
	return header + content + instructions, nil
}

// buildReadOnlyMemoryBlock is the memory block for an agent without write or edit tools; it never creates the directory.
func buildReadOnlyMemoryBlock(agentName, scope, cwd string) (string, error) {
	memoryDir, err := resolveMemoryDir(agentName, scope, cwd)
	if err != nil {
		return "", err
	}
	existing, ok := readMemoryIndex(memoryDir)
	header := "# Agent Memory (read-only)\n\nMemory scope: " + scope +
		"\nYou have read-only access to memory. You can reference existing memories but cannot create or modify them."
	content := "\n\nNo memory is available yet. Other agents or sessions with write access can create memories for you to consume."
	if ok && existing != "" {
		content = "\n\n## Current MEMORY.md\n" + existing
	}
	return header + content, nil
}

// memoryExtras is the memory block of an agent with `memory:` and the tool list it needs. An agent that can write
// (write or edit not on its denylist) gets the read-write block and any of read, write and edit it lacks; one that
// cannot gets the read-only block, which never creates the directory, and read. tools is the spec's list (nil: the
// child's default). upstream: agent-runner.ts runAgent (persistent memory).
func memoryExtras(cfg *agentConfig, typ string, tools []string, cwd string) (block string, out []string, err error) {
	toolNames := getToolNamesForType(typ)
	existing := map[string]bool{}
	for _, n := range toolNames {
		existing[n] = true
	}
	denied := map[string]bool{}
	for _, n := range cfg.DisallowedTools {
		denied[n] = true
	}
	has := func(name string) bool { return existing[name] && !denied[name] }
	var extra []string
	if has("write") || has("edit") {
		extra = getMemoryToolNames(existing)
		block, err = buildMemoryBlock(cfg.Name, cfg.Memory, cwd)
	} else {
		extra = getReadOnlyMemoryToolNames(existing)
		block, err = buildReadOnlyMemoryBlock(cfg.Name, cfg.Memory, cwd)
	}
	out = tools
	if len(extra) > 0 {
		out = append(append([]string{}, toolNames...), extra...)
	}
	return block, out, err
}
