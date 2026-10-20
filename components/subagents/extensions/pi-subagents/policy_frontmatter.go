package pi_subagents

import "fmt"

// parsePolicyFrontmatter reads the frontmatter fields of the run policy (agents.ts:2155-2176, :2272): the agent's default output
// file and mode and its default runtime deadline.
func parsePolicyFrontmatter(fm Frontmatter, a *AgentConfig, local string) error {
	if fm.Has("timeoutMs") {
		n, ok := normalizeNonNegativeInteger(fm.Get("timeoutMs"))
		if !ok || n <= 0 {
			return fmt.Errorf("Agent '%s' has invalid timeoutMs frontmatter; expected a positive integer.", local)
		}
		a.DefaultTimeoutMs = n
	}
	if fm.Has("outputMode") {
		m := fm.Get("outputMode")
		if m != "inline" && m != "file-only" {
			return fmt.Errorf("Agent '%s' has invalid outputMode frontmatter; expected 'inline' or 'file-only'.", local)
		}
		a.OutputMode = m
	}
	if fm.Has("output") {
		a.Output = fm.Get("output")
	}
	return nil
}
