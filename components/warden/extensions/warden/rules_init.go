package warden

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// `/warden init` (src/init.ts): the starter rules file and the prompt that asks the session's agent to write the
// project's own.

// instructionFiles are the fallback files whose content is instruction text to compile into small rules; a
// README-style file stays whole text.
var instructionFiles = []string{"AGENTS.md", "CLAUDE.md"}

// contextCandidates are the paths scanned for project context when generating a starter rules file.
var contextCandidates = []string{"package.json", "tsconfig.json", "Cargo.toml", "pyproject.toml", "go.mod"}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// detectProjectType is a rough project type from common manifest files: typescript, rust, python, go, javascript or
// generic.
func detectProjectType(cwd string) string {
	switch {
	case fileExists(filepath.Join(cwd, "tsconfig.json")):
		return "typescript"
	case fileExists(filepath.Join(cwd, "Cargo.toml")):
		return "rust"
	case fileExists(filepath.Join(cwd, "pyproject.toml")):
		return "python"
	case fileExists(filepath.Join(cwd, "go.mod")):
		return "go"
	case fileExists(filepath.Join(cwd, "package.json")):
		return "javascript"
	}
	return "generic"
}

// jsKeyOrder lists the keys of a JSON object as JavaScript's Object.keys does: array-index keys ascending first, then
// the rest in insertion order.
func jsKeyOrder(raw json.RawMessage) []string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil
	}
	var keys []string
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return nil
		}
		keys = append(keys, key.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return nil
		}
	}
	var indexed, named []string
	seen := map[string]bool{}
	for _, k := range keys {
		if seen[k] {
			continue
		}
		seen[k] = true
		if n, err := strconv.ParseUint(k, 10, 32); err == nil && strconv.FormatUint(n, 10) == k && n < 1<<32-1 {
			indexed = append(indexed, k)
		} else {
			named = append(named, k)
		}
	}
	sort.Slice(indexed, func(i, j int) bool {
		a, _ := strconv.ParseUint(indexed[i], 10, 32)
		b, _ := strconv.ParseUint(indexed[j], 10, 32)
		return a < b
	})
	return append(indexed, named...)
}

// manifestSummary reads a manifest file and extracts a brief summary (name, scripts, dependencies); "" when the file
// does not exist, does not parse, or holds nothing worth naming.
func manifestSummary(cwd, filename string) string {
	data, err := os.ReadFile(filepath.Join(cwd, filename))
	if err != nil {
		return ""
	}
	var raw map[string]json.RawMessage
	// Best-effort: an unparseable manifest is not a failure, just skip this candidate.
	if json.Unmarshal(data, &raw) != nil {
		return ""
	}
	var parts []string
	if name, ok := raw["name"]; ok {
		var s string
		if json.Unmarshal(name, &s) == nil && s != "" {
			parts = append(parts, "name: "+s)
		} else if n := strings.TrimSpace(string(name)); n != "null" && n != "false" && n != "0" && n != `""` && s == "" && json.Unmarshal(name, new(float64)) == nil {
			parts = append(parts, "name: "+n)
		}
	}
	for _, section := range []struct {
		key, label string
		limit      int
	}{{"scripts", "scripts", 8}, {"dependencies", "deps", 10}} {
		value, ok := raw[section.key]
		if !ok {
			continue
		}
		keys := jsKeyOrder(value)
		if len(keys) > section.limit {
			keys = keys[:section.limit]
		}
		if len(keys) > 0 {
			parts = append(parts, section.label+": "+strings.Join(keys, ", "))
		}
	}
	return strings.Join(parts, "; ")
}

// buildProjectContext is the project context for the starter rules file, capped at 8000 characters.
func buildProjectContext(cwd string) string {
	parts := []string{"Project type: " + detectProjectType(cwd)}
	for _, candidate := range contextCandidates {
		if summary := manifestSummary(cwd, candidate); summary != "" {
			parts = append(parts, candidate+": "+summary)
		}
	}
	context := strings.Join(parts, "\n")
	if utf16Len(context) > 8000 {
		return utf16Slice(context, 0, 8000) + "\n...(truncated)"
	}
	return context
}

// readExistingRules is the first existing fallback rules file's content.
func readExistingRules(cwd string) (string, bool) {
	for _, file := range fallbackFiles {
		path := filepath.Join(cwd, file)
		if !fileExists(path) {
			continue
		}
		if data, err := os.ReadFile(path); err == nil {
			return decodeUTF8(data), true
		}
	}
	return "", false
}

// readExistingPiWarden is the existing pi-warden.md content.
func readExistingPiWarden(cwd string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(cwd, RulesFile))
	if err != nil {
		return "", false
	}
	return decodeUTF8(data), true
}

// readNumberedInstructions is the first instruction file's lines with their 1-based line numbers.
func readNumberedInstructions(cwd string) (file, text string, ok bool) {
	for _, name := range instructionFiles {
		path := filepath.Join(cwd, name)
		if !fileExists(path) {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		lines := reLineBreak.Split(decodeUTF8(data), -1)
		if lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		blank := true
		for _, l := range lines {
			if jsTrim(l) != "" {
				blank = false
				break
			}
		}
		if blank {
			continue
		}
		numbered := make([]string, len(lines))
		for i, l := range lines {
			numbered[i] = strconv.Itoa(i+1) + ": " + l
		}
		return name, strings.Join(numbered, "\n"), true
	}
	return "", "", false
}

// safetyRules are the standard safety rules that ship in every starter pi-warden.md.
var safetyRules = strings.ReplaceAll(`# No hardcoded secrets
Source code must not contain passwords, API keys, tokens, or connection URLs with credentials.
Read them from the environment, a function parameter, or the config module.

# Comments explain why, not what
A comment states a reason, a constraint, a workaround, or a non-obvious invariant. A comment
that restates what the next line plainly does is a violation.

# Errors are not swallowed
A ‡catch‡ block must handle the error, report it, or re-raise it. An empty catch block, or
one whose body is only a comment, is a violation.

# No partial implementations
Implement features fully. A comment that says "for now", "simplified", or "later", or a
stub body, is a violation. If a part genuinely cannot be done, say so in your reply instead
of stubbing it.

# Do not run destructive commands that erase uncommitted work
‡git reset --hard‡, ‡git checkout -- .‡, ‡git clean -fd‡, and similar commands that discard
untracked or uncommitted changes are forbidden. These destroy work that has no backup. If a
clean tree is needed, create a worktree instead or ask the user.
`, "‡", "`")

// generateStarterRules generates a starter pi-warden.md. It returns the content and does not write it.
func generateStarterRules(cwd string) string {
	projectType := detectProjectType(cwd)
	lines := []string{
		"Copy this file to the root of your project as `pi-warden.md` and edit it.",
		"Every `#` heading below is one rule. The text under a heading is what Jev reads when it judges a write or an edit.",
		"",
		"<!-- Project context: " + buildProjectContext(cwd) + " -->",
		"",
		safetyRules,
	}
	if existing, ok := readExistingPiWarden(cwd); ok && existing != "" {
		lines = append(lines, "", "## Existing pi-warden.md rules (preserved)", existing, "")
	} else if rules, ok := readExistingRules(cwd); ok && rules != "" {
		lines = append(lines, "", "## Existing project rules (preserved from fallback file)", rules, "")
	}
	// Add type-specific rules.
	if projectType == "typescript" || projectType == "javascript" {
		lines = append(lines,
			"# No explicit any",
			"paths: **/*.ts, **/*.tsx",
			"Do not use the `any` type. Use a precise type, `unknown` with a runtime check, or a generic parameter.",
			"",
			"# Exported functions declare their return type",
			"paths: src/**/*.ts",
			"Every exported function or method declares its return type instead of relying on inference.",
			"",
		)
	}
	lines = append(lines,
		"# New exported functions get a test",
		"paths: src/**",
		"A newly added exported function or class comes with at least one test that exercises its main behaviour.",
		"",
	)
	return strings.Join(lines, "\n")
}

// buildInitPrompt is a ready-to-paste prompt for the model to generate pi-warden.md. It includes the extracted project
// context and the standard safety rules as a foundation.
func buildInitPrompt(cwd string) string {
	projectType := detectProjectType(cwd)
	lines := []string{
		"Create a `pi-warden.md` file for this project. Every `#` heading is one rule; the text under it is what Jev judges against.",
		"",
		"## Project context",
		buildProjectContext(cwd),
		"",
		"## Rules to always include",
		safetyRules,
	}
	if existing, ok := readExistingPiWarden(cwd); ok && existing != "" {
		lines = append(lines, "", "## Existing pi-warden.md rules (preserve these)", existing, "")
	} else if file, text, ok := readNumberedInstructions(cwd); ok {
		lines = append(lines,
			"",
			"## Existing instructions to compile (from "+file+", each line prefixed with its line number)",
			text,
			"",
			"Compile these instructions into rules: one small rule per instruction that can be judged from one changed file alone, without other files, repository history, or the task.",
			"- Give each rule a `source: <file>:<line>` header naming the instruction line it came from, for example `source: AGENTS.md:65`.",
			"- Add a `paths:` header when the instruction concerns certain files only.",
			"- Keep each rule's wording close to the source instruction.",
			"- Name the concrete pattern that shows a violation: a symbol, a comment phrase, a command, a file shape.",
			"- Stay under the rule cap: at most "+strconv.Itoa(maxRules)+" rules.",
			"",
			"Leave these instructions out of pi-warden.md, and list each left-out instruction in your reply with the reason:",
			"- an instruction that needs other files, repository history, or the task to judge",
			"- an instruction that a linter, formatter, or type checker already enforces",
			"",
			"In your reply, suggest running `/warden rules check` when the user has a TypeSafe key. Suggest it only; never run it as part of this task.",
			"",
		)
	} else if rules, ok := readExistingRules(cwd); ok && rules != "" {
		lines = append(lines, "", "## Existing project rules (preserve these)", rules, "")
	}
	if projectType == "typescript" || projectType == "javascript" {
		lines = append(lines,
			"## Type-specific rules",
			"- No explicit `any` in **/*.ts, **/*.tsx",
			"- Exported functions declare their return type in src/**/*.ts",
			"",
		)
	}
	lines = append(lines,
		"## Requirements",
		"- Under 50 rules",
		"- Each rule is a `#` heading with a description under it",
		"- Optional `paths:` line under a heading limits the rule to matching files",
		"- No stubs, no TODOs, no placeholders",
		"- Write the file to pi-warden.md at the project root",
	)
	return strings.Join(lines, "\n")
}

// InitResult is the result of an init attempt.
type InitResult struct {
	// AlreadyExists: a pi-warden.md already existed.
	AlreadyExists bool
	// Path is the path that was written (or would be overwritten).
	Path string
	// Content is the content written.
	Content string
}

// writeStarterRules writes a starter pi-warden.md to the project root. If it already exists it returns
// AlreadyExists without writing, unless overwrite is set.
func writeStarterRules(cwd string, overwrite bool) (InitResult, error) {
	target := filepath.Join(cwd, RulesFile)
	exists := fileExists(target)
	if exists && !overwrite {
		data, err := os.ReadFile(target)
		if err != nil {
			return InitResult{}, err
		}
		return InitResult{AlreadyExists: true, Path: target, Content: decodeUTF8(data)}, nil
	}
	content := generateStarterRules(cwd)
	if err := os.WriteFile(target, []byte(content), 0o666); err != nil {
		return InitResult{}, err
	}
	return InitResult{AlreadyExists: exists, Path: target, Content: content}, nil
}
