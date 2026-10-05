package mapper

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Human-readable descriptions of what the agent is doing right now (port of src/pi/activity.ts).
// They are display strings a client renders verbatim next to its spinner.

const (
	// ThinkingActivity and RespondingActivity are the two model-side activities.
	ThinkingActivity   = "Thinking"
	RespondingActivity = "Responding"

	maxLength = 60
)

// shorten trims the middle rather than the end: the informative parts of a path or a shell
// command sit at the two ends.
func shorten(value string, limit ...int) string {
	lim := maxLength
	if len(limit) > 0 {
		lim = limit[0]
	}
	collapsed := collapseSpace(value)
	n := utf16Len(collapsed)
	if n <= lim {
		return collapsed
	}
	head := (lim - 1 + 1) / 2 // Math.ceil((limit-1)/2)
	tail := (lim - 1) / 2     // Math.floor((limit-1)/2)
	return sliceUTF16(collapsed, 0, head) + "…" + sliceUTF16(collapsed, n-tail, n)
}

// displayPath renders a path relative to the working directory when it sits underneath it.
func displayPath(value any, workingDirectory string) (string, bool) {
	s, ok := value.(string)
	if !ok || s == "" {
		return "", false
	}
	if workingDirectory != "" && strings.HasPrefix(s, workingDirectory+"/") {
		s = s[len(workingDirectory)+1:]
	}
	return shorten(s), true
}

func argsMap(args any) map[string]any {
	m, _ := args.(map[string]any)
	return m
}

// DescribeToolCall describes a tool invocation. pi's built-in tools are named individually;
// anything else falls back to its own name.
func DescribeToolCall(toolName string, args any, workingDirectory string) string {
	input := argsMap(args)
	path, hasPath := displayPath(input["path"], workingDirectory)
	switch toolName {
	case "read":
		if hasPath {
			return "Reading " + path
		}
		return "Reading a file"
	case "write":
		if hasPath {
			return "Writing " + path
		}
		return "Writing a file"
	case "edit":
		if hasPath {
			return "Editing " + path
		}
		return "Editing a file"
	case "bash":
		if c, ok := input["command"].(string); ok {
			if command := shorten(c); command != "" {
				return "Running " + command
			}
		}
		return "Running a command"
	case "ls":
		if hasPath {
			return "Listing " + path
		}
		return "Listing files"
	case "find":
		if p, ok := input["pattern"].(string); ok {
			if pattern := shorten(p, 30); pattern != "" {
				return "Finding " + pattern
			}
		}
		return "Finding files"
	case "grep":
		if p, ok := input["pattern"].(string); ok {
			if pattern := shorten(p, 30); pattern != "" {
				return "Searching for " + pattern
			}
		}
		return "Searching"
	default:
		return "Running " + shorten(toolName, 40)
	}
}

func stringArg(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok && s != ""
}

// fullArguments is JSON.stringify(args, null, 2). Go marshals object keys in sorted order where
// JavaScript keeps insertion order; Pi's events reach an extension already decoded, so the
// original order is not available (documented in PORT.md).
func fullArguments(args any) (string, bool) {
	if args == nil {
		return "", false
	}
	out, err := StringifyJSON(args, true)
	if err != nil {
		return "", false
	}
	return out, true
}

// StringifyJSON is JSON.stringify(v) (or JSON.stringify(v, null, 2) when indent is set): unlike
// encoding/json.Marshal it leaves <, > and & unescaped.
func StringifyJSON(v any, indent bool) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if indent {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

// ToolInputFor is the one argument that says what a tool call is about. Anything without an
// obvious subject keeps the full arguments. ok is false when there is nothing to show.
func ToolInputFor(toolName string, args any) (string, bool) {
	input := argsMap(args)
	switch toolName {
	case "bash":
		if s, ok := stringArg(input["command"]); ok {
			return s, true
		}
	case "grep", "find":
		if s, ok := searchInput(input); ok {
			return s, true
		}
	case "read", "write", "edit", "ls":
		if s, ok := stringArg(input["path"]); ok {
			return s, true
		}
	}
	return fullArguments(args)
}

// searchInput renders a search as its pattern plus whatever narrows it. Models restate the
// defaults (`path: "."`, a glob matching everything, `ignoreCase: false`), so only arguments that
// actually narrow the search are shown, in the order `rg` writes them.
func searchInput(input map[string]any) (string, bool) {
	pattern, ok := stringArg(input["pattern"])
	if !ok {
		return "", false
	}
	parts := []string{pattern}
	if glob, ok := stringArg(input["glob"]); ok && glob != "**/*" {
		parts = append(parts, "--glob "+glob)
	}
	if ic, _ := input["ignoreCase"].(bool); ic {
		parts = append(parts, "--ignore-case")
	}
	if path, ok := stringArg(input["path"]); ok && path != "." {
		parts = append(parts, "in "+path)
	}
	return strings.Join(parts, " "), true
}

var pastTenseVerbs = map[string]string{
	"Reading": "Read", "Writing": "Wrote", "Editing": "Edited", "Running": "Ran",
	"Listing": "Listed", "Finding": "Found", "Searching": "Searched",
}

// DescribeFinishedToolCall is the same description phrased for a call that has finished: only
// the leading verb changes, which keeps the subject identical.
func DescribeFinishedToolCall(toolName string, args any, workingDirectory string) string {
	description := DescribeToolCall(toolName, args, workingDirectory)
	verb, rest, _ := strings.Cut(description, " ")
	if past, ok := pastTenseVerbs[verb]; ok {
		if rest == "" && !strings.Contains(description, " ") {
			return past
		}
		return past + " " + rest
	}
	return description
}
