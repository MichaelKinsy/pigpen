package tintinweb_subagents

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
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
	return decodeUTF8Lossy(data), true
}

// decodeUTF8Lossy decodes like Node's utf-8 decoder: one U+FFFD for each maximal subpart of an ill-formed sequence
// (Go's ToValidUTF8 would replace a whole run of bad bytes with one).
func decodeUTF8Lossy(data []byte) string {
	if utf8.Valid(data) {
		return string(data)
	}
	var b strings.Builder
	for i := 0; i < len(data); {
		if data[i] < utf8.RuneSelf {
			b.WriteByte(data[i])
			i++
			continue
		}
		if r, n := utf8.DecodeRune(data[i:]); r != utf8.RuneError || n > 1 {
			b.Write(data[i : i+n])
			i += n
			continue
		}
		// the longest prefix of a well-formed multi-byte sequence starting here
		lo, hi := byte(0x80), byte(0xBF)
		need := 0
		switch c := data[i]; {
		case c >= 0xC2 && c <= 0xDF:
			need = 1
		case c == 0xE0:
			need, lo = 2, 0xA0
		case c == 0xED:
			need, hi = 2, 0x9F
		case c >= 0xE1 && c <= 0xEF:
			need = 2
		case c == 0xF0:
			need, lo = 3, 0x90
		case c == 0xF4:
			need, hi = 3, 0x8F
		case c >= 0xF1 && c <= 0xF3:
			need = 3
		}
		i++
		for ; need > 0 && i < len(data) && data[i] >= lo && data[i] <= hi; need-- {
			i++
			lo, hi = 0x80, 0xBF
		}
		b.WriteString("\uFFFD")
	}
	return b.String()
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

// memoryTools is the tool list of an agent with `memory:` and whether it can write. An agent that can write (write or
// edit not on its denylist) gets any of read, write and edit it lacks; one that cannot gets read. toolNames is the
// agent's list. upstream: agent-runner.ts runAgent (persistent memory).
func memoryTools(cfg *agentConfig, toolNames []string) (out []string, writes bool) {
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
	if writes = has("write") || has("edit"); writes {
		extra = getMemoryToolNames(existing)
	} else {
		extra = getReadOnlyMemoryToolNames(existing)
	}
	out = toolNames
	if len(extra) > 0 {
		out = append(append([]string{}, toolNames...), extra...)
	}
	return out, writes
}

// memoryBlockFor is the memory section of an agent's system prompt: the read-write block (it makes the directory) for an
// agent that can write, the read-only block (it never does) for one that cannot. upstream: agent-runner.ts runAgent.
func memoryBlockFor(cfg *agentConfig, writes bool, cwd string) (string, error) {
	if writes {
		return buildMemoryBlock(cfg.Name, cfg.Memory, cwd)
	}
	return buildReadOnlyMemoryBlock(cfg.Name, cfg.Memory, cwd)
}
