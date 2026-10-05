package warden

import (
	"strings"
)

// Knowledge of which tool inputs carry shell commands (src/tools.ts). PiG's built-ins plus
// context-mode's ctx_* tools. Unknown tools return nothing and are judged from their JSON input.

// ContentPart is one part of a tool result's content.
type ContentPart struct {
	Type string
	Text string
}

// CommandView is the shell text a tool call would execute.
type CommandView struct {
	Command string
	// Shell is false when the code is not shell (JavaScript, Python...): pattern rules still run,
	// the read-only shortcut does not.
	Shell bool
}

var shellLanguages = map[string]bool{"shell": true, "bash": true, "sh": true, "zsh": true, "powershell": true}

func stringField(input map[string]any, key string) (string, bool) {
	s, ok := input[key].(string)
	return s, ok
}

// CommandOf extracts the command text a tool call would execute; ok is false for tools that run no commands.
func CommandOf(tool string, input map[string]any) (CommandView, bool) {
	switch tool {
	case "bash", "powershell":
		if c, ok := stringField(input, "command"); ok {
			return CommandView{Command: c, Shell: true}, true
		}
	case "ctx_execute", "ctx_execute_file":
		code, ok := stringField(input, "code")
		if !ok {
			return CommandView{}, false
		}
		lang, _ := stringField(input, "language")
		return CommandView{Command: code, Shell: shellLanguages[strings.ToLower(lang)]}, true
	case "ctx_batch_execute":
		var commands []string
		switch items := input["commands"].(type) {
		case []any:
			for _, item := range items {
				if m, ok := item.(map[string]any); ok {
					if c, ok := m["command"].(string); ok {
						commands = append(commands, c)
					}
				}
			}
		case []map[string]any:
			for _, m := range items {
				if c, ok := m["command"].(string); ok {
					commands = append(commands, c)
				}
			}
		}
		if len(commands) > 0 {
			return CommandView{Command: strings.Join(commands, "\n"), Shell: true}, true
		}
	}
	return CommandView{}, false
}

var (
	reExitCode = lazyRE(`(?:^|\n)\s*Command exited with code [1-9]\d*\b`)
	reTimedOut = lazyRE(`(?:^|\n)\s*\(timed out\)\s*$`)
)

// OutputReportsFailure is true for text that signals failure in tool output when the tool itself did
// not flag an error (context-mode reports exit codes inline).
func OutputReportsFailure(text string) bool {
	return reExitCode.MatchString(text) || reTimedOut.MatchString(text)
}
