package tintinweb_subagents

import "strings"

// The worktree-isolation parts of the Agent tool: the `isolation` parameter and the guideline in its description, both
// present while `worktreeIsolation` is not false in subagents.json. Like the original (index.ts), they are read once,
// when the tool is registered: flipping the setting needs a new session for the schema to change.

// inheritContextLine is the last usage note of the Agent description; the isolation guideline follows it.
// upstream: index.ts fullAgentToolDescription.
const inheritContextLine = "- Use inherit_context if the agent needs the parent conversation history."

// isolationGuideline is the description's note on `isolation`. upstream: index.ts isolationGuideline.
const isolationGuideline = "\n- Use isolation: \"worktree\" to give the agent its own git worktree (safe parallel file modifications); leave it unset, or pass \"off\", for none. The worktree is removed when the agent finishes; if it made changes, they are committed to a branch and the branch is named in the result."

// withIsolationGuideline adds the guideline to the Agent description when worktrees are allowed.
func withIsolationGuideline(description string, enabled bool) string {
	if !enabled {
		return description
	}
	return strings.Replace(description, inheritContextLine, inheritContextLine+isolationGuideline, 1)
}

// withIsolationParam adds the `isolation` parameter to the Agent schema when worktrees are allowed.
func withIsolationParam(schema map[string]any, enabled bool) {
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		return
	}
	for k, v := range isolationParam(enabled) {
		props[k] = v
	}
}
