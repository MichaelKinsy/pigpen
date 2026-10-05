package warden

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// The action summary: what is shown to the user and what is sent to the judge. Every text field is
// redacted and truncated.

const (
	taskLimit    = 1500
	planLimit    = 500
	commandLimit = 2000
	excerptLimit = 1500
	editLimit    = 400
)

const dataTextNote = "heredoc bodies and quoted arguments of echo/printf/grep, git commit messages, and gh message flags in this command are text that is written, printed, searched, or recorded, not executed"

func displayPath(target, cwd string) (path, location string) {
	abs := target
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(cwd, target)
	}
	abs = filepath.Clean(abs)
	if isInside(abs, cwd) {
		absCwd, _ := filepath.Abs(cwd)
		rel, _ := filepath.Rel(absCwd, abs)
		if rel == "" {
			rel = "."
		}
		return filepath.ToSlash(rel), "inside_project"
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && (abs == home || strings.HasPrefix(abs, home+string(filepath.Separator))) {
		return "~" + abs[len(home):], "outside_project"
	}
	return abs, "outside_project"
}

// DescribeAction summarises a tool call without leaking secrets or absolute paths.
func DescribeAction(tool string, input map[string]any, cwd string) ActionSummary {
	s := ActionSummary{Tool: tool}
	if view, ok := CommandOf(tool, input); ok {
		s.Command = Redact(truncate(view.Command, commandLimit))
		if StripDataText(view.Command).Stripped {
			s.DataText = dataTextNote
		}
	}
	if p, ok := input["path"].(string); ok && jsTrim(p) != "" && tool != "ctx_execute_file" {
		s.Path, s.Location = displayPath(p, cwd)
		full := p
		if !filepath.IsAbs(full) {
			full = filepath.Join(cwd, p)
		}
		_, err := os.Stat(full)
		exists := err == nil
		s.Exists = &exists
	}
	if content, ok := input["content"].(string); ok && tool == "write" {
		n := len(content)
		s.Bytes = &n
		s.Excerpt = Redact(sample(content, excerptLimit))
	}
	if tool == "edit" {
		var edits []any
		switch e := input["edits"].(type) {
		case []any:
			edits = e
		case []map[string]any:
			for _, m := range e {
				edits = append(edits, m)
			}
		default:
			edits = nil
		}
		if _, isArray := input["edits"].([]any); isArray || input["edits"] != nil {
			if _, isSlice := input["edits"].([]map[string]any); isSlice || isArray {
				n := len(edits)
				s.EditCount = &n
				for i := 0; i < len(edits) && i < 3; i++ {
					m, _ := edits[i].(map[string]any)
					oldText, _ := m["oldText"].(string)
					newText, _ := m["newText"].(string)
					s.Edits = append(s.Edits, EditPair{OldText: Redact(truncate(oldText, editLimit)), NewText: Redact(truncate(newText, editLimit))})
				}
			}
		}
	}
	if s.Command == "" && s.Path == "" {
		raw, _ := json.Marshal(input)
		s.Input = Redact(truncate(string(raw), commandLimit))
	}
	return s
}
