package tintinweb_subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Pure helpers for the `/agents` file-editing operations: locating an agent's .md file, toggling its `enabled:`
// frontmatter flag, and the file the create wizard writes. Reading whether a file is disabled asks the same parser the
// loader uses (YAML's boolean spellings and the fence scan are the loader's to get right); editing is line-wise, because
// re-serializing a parsed document would reformat a file the README tells users to hand-author. Removal is therefore
// best-effort: it recognizes a lowercase bare `false` and reports changed == false for the spellings it cannot rewrite,
// so the caller refuses honestly rather than announcing a change it did not make. The serializer of an ejected default
// is serialize.go. upstream: src/agent-file-toggle.ts.

type agentFileLocation string

const (
	locationProject   agentFileLocation = "project"
	locationWorkspace agentFileLocation = "workspace"
	locationPersonal  agentFileLocation = "personal"
)

type agentFile struct {
	Path     string
	Location agentFileLocation
}

func projectAgentsDir(cwd string) string   { return filepath.Join(cwd, ".pi", "agents") }
func workspaceAgentsDir(cwd string) string { return filepath.Join(cwd, ".agents", "agents") }
func personalAgentsDir() string            { return filepath.Join(agentDir(), "agents") }

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// findAgentFile is the file of a custom agent by name, in discovery-precedence order (project, workspace, then global).
// It mirrors the load-side precedence: if the two drift, `/agents` edits a file the loader is not reading.
func findAgentFile(name, cwd string) *agentFile {
	if p := filepath.Join(projectAgentsDir(cwd), name+".md"); fileExists(p) {
		return &agentFile{p, locationProject}
	}
	if p := filepath.Join(workspaceAgentsDir(cwd), name+".md"); fileExists(p) {
		return &agentFile{p, locationWorkspace}
	}
	if p := filepath.Join(personalAgentsDir(), name+".md"); fileExists(p) {
		return &agentFile{p, locationPersonal}
	}
	return nil
}

// locateAgentFile is the file behind a loaded agent, preferring the path the loader actually read over the `<type>.md`
// guess: an agent's type comes from its frontmatter `name:`, so `reviewer.md` declaring `name: code-reviewer` is loaded
// as `code-reviewer`, and probing for `code-reviewer.md` would write a NEW stub that loses to `reviewer.md` on load,
// leaving the agent enabled while reporting success. The probe stays as the fallback: a built-in that was never ejected
// has no source path, and a path can go stale between a load and this call.
func locateAgentFile(name, sourcePath, cwd string) *agentFile {
	if sourcePath != "" && fileExists(sourcePath) {
		return &agentFile{sourcePath, classifyAgentDir(sourcePath, cwd)}
	}
	return findAgentFile(name, cwd)
}

// classifyAgentDir is which discovery location a loaded agent's file came from. It only ever names a directory in a
// confirmation prompt, so an unrecognized parent reports as personal.
func classifyAgentDir(path, cwd string) agentFileLocation {
	sep := string(filepath.Separator)
	switch {
	case strings.HasPrefix(path, projectAgentsDir(cwd)+sep):
		return locationProject
	case strings.HasPrefix(path, workspaceAgentsDir(cwd)+sep):
		return locationWorkspace
	}
	return locationPersonal
}

type disableOutcome string

const (
	outcomeDisabled        disableOutcome = "disabled"
	outcomeAlreadyDisabled disableOutcome = "already-disabled"
	outcomeNoFrontmatter   disableOutcome = "no-frontmatter"
)

var (
	enabledFalseLine = regexp.MustCompile(`^enabled:[ \t]*false[ \t]*$`)
	fenceLine        = regexp.MustCompile(`^---[ \t]*$`)
)

// splitLinesKeepEOL is content.split(/(?<=\n)/): lines keep their terminators.
func splitLinesKeepEOL(content string) []string {
	var lines []string
	for len(content) > 0 {
		i := strings.IndexByte(content, '\n')
		if i < 0 {
			lines = append(lines, content)
			break
		}
		lines = append(lines, content[:i+1])
		content = content[i+1:]
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func trimEOL(l string) string { return strings.TrimSuffix(strings.TrimSuffix(l, "\n"), "\r") }

type frontmatterBlock struct {
	lines    []string
	closeIdx int
	eol      string
}

// splitFrontmatter splits a file into its frontmatter lines and everything else, agreeing with what the loader considers
// a frontmatter block, BOM normalisation included (the BOM stays in lines[0]: it belongs to the file's encoding, and an
// edit must not strip it). Lines keep their terminators, so an edit preserves the file's line endings. nil when there is
// no usable block.
func splitFrontmatter(content string) *frontmatterBlock {
	lines := splitLinesKeepEOL(content)
	first := lines[0]
	first = trimEOL(strings.TrimPrefix(first, "\ufeff"))
	if !fenceLine.MatchString(first) {
		return nil
	}
	closeIdx := -1
	for i := 1; i < len(lines); i++ {
		if fenceLine.MatchString(trimEOL(lines[i])) {
			closeIdx = i
			break
		}
	}
	if closeIdx == -1 {
		return nil
	}
	eol := "\n"
	if strings.HasSuffix(lines[0], "\r\n") {
		eol = "\r\n"
	}
	return &frontmatterBlock{lines: lines, closeIdx: closeIdx, eol: eol}
}

// isDisabledContent is whether the loader considers this file disabled: a READ, so it asks the loader's own parser
// rather than mirroring it. A parse failure means the file is already unloadable, which is what the loader sees too: it
// skips the agent, so there is no "disabled" state to report.
func isDisabledContent(content string) bool {
	fm, _, err := parseFrontmatter(content)
	if err != nil {
		return false
	}
	b, ok := fm["enabled"].(bool)
	return ok && !b
}

// disableInContent adds `enabled: false` to a file's frontmatter. The outcome distinguishes a real edit from a no-op so
// the caller can report honestly.
func disableInContent(content string) (string, disableOutcome) {
	block := splitFrontmatter(content)
	if block == nil {
		return content, outcomeNoFrontmatter
	}
	if isDisabledContent(content) {
		return content, outcomeAlreadyDisabled
	}
	lines := make([]string, 0, len(block.lines)+1)
	lines = append(lines, block.lines[0], "enabled: false"+block.eol)
	lines = append(lines, block.lines[1:]...)
	return strings.Join(lines, ""), outcomeDisabled
}

// enableInContent removes `enabled: false` from a file's frontmatter, wherever it appears in the block (the loader
// honors the key at any position, so the two must agree or a hand-authored agent can be disabled and never re-enabled).
// changed is false when the key was not found, so the caller can avoid reporting "Enabled <name>" for a write that did
// nothing.
func enableInContent(content string) (string, bool) {
	block := splitFrontmatter(content)
	if block == nil {
		return content, false
	}
	kept := make([]string, 0, len(block.lines))
	for i, l := range block.lines {
		if i > 0 && i < block.closeIdx && enabledFalseLine.MatchString(trimEOL(l)) {
			continue
		}
		kept = append(kept, l)
	}
	if len(kept) == len(block.lines) {
		return content, false
	}
	return strings.Join(kept, ""), true
}

// isEmptyStub is whether this is the empty stub `/agents` writes when disabling a built-in default.
func isEmptyStub(content string) bool {
	return jsTrim(strings.ReplaceAll(content, "\r\n", "\n")) == "---\n---"
}

// newAgentInput is what `/agents → Create agent → Manual` collects, before serialization.
type newAgentInput struct {
	Description string
	// Tools is the already-resolved `tools:` value ("none", "all", or a CSV of tool names).
	Tools string
	// Model is `provider/modelId`, "" to inherit the parent's model.
	Model string
	// Thinking is a pi thinking level, "" to inherit.
	Thinking     string
	SystemPrompt string
}

// jsonString is JSON.stringify of a string.
func jsonString(s string) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

// buildNewAgentFile builds the .md file the create wizard writes. The description and the model come straight from a
// free-text prompt, so they are quoted rather than interpolated: a colon ("Scout: find things") would make the file
// unparseable (and an unparseable agent file is skipped, so the wizard would report success for an agent that does not
// exist) and a `#` would open a comment and truncate the value. tools and thinking are chosen from fixed menus, and
// tools is a CSV that must stay a bare scalar for the loader's parser.
func buildNewAgentFile(in newAgentInput) string {
	modelLine := ""
	if in.Model != "" {
		modelLine = "\nmodel: " + jsonString(in.Model)
	}
	thinkingLine := ""
	if in.Thinking != "" {
		thinkingLine = "\nthinking: " + in.Thinking
	}
	return "---\ndescription: " + jsonString(in.Description) + "\ntools: " + in.Tools + modelLine + thinkingLine +
		"\nprompt_mode: replace\n---\n\n" + in.SystemPrompt + "\n"
}
