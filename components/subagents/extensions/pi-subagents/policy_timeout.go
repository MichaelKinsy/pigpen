package pi_subagents

import (
	"fmt"
	"math"
)

// Port of src/runs/shared/tool-timeout.ts.

const (
	toolTimeoutEnv           = "PI_SUBAGENT_TOOL_TIMEOUT_MS"
	defaultFastToolTimeoutMs = 300000
)

var (
	defaultFastToolTimeoutTools = []string{"read", "grep", "find", "ls", "edit", "write", "structured_output"}
	// toolTimeoutExemptTools are tools whose normal job can be to wait for a person or another run.
	toolTimeoutExemptTools = []string{"contact_supervisor", "intercom", "bg_wait"}
)

func isToolTimeoutExempt(tool string) bool {
	return tool != "" && contains(toolTimeoutExemptTools, tool)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// defaultToolTimeoutMs is 5 minutes for the known-fast tools, none for any other.
func defaultToolTimeoutMs(tool string) (int, bool) {
	if tool != "" && contains(defaultFastToolTimeoutTools, tool) {
		return defaultFastToolTimeoutMs, true
	}
	return 0, false
}

// effectiveToolTimeoutMs: no timeout for coordination tools, else the configured one, else the default for the tool.
func effectiveToolTimeoutMs(tool string, configured *int) (int, bool) {
	if isToolTimeoutExempt(tool) {
		return 0, false
	}
	if configured != nil {
		return *configured, true
	}
	return defaultToolTimeoutMs(tool)
}

func formatToolTimeoutMessage(tool string, timeoutMs int) string {
	return fmt.Sprintf("Tool '%s' exceeded its timeout of %dms.", tool, timeoutMs)
}

// toolTimeoutCallKey keys a tool call by its id, else by name and a fallback sequence number.
func toolTimeoutCallKey(toolCallID any, toolName any, fallbackID int) string {
	if id, ok := toolCallID.(string); ok && id != "" {
		return "id:" + id
	}
	name := "tool"
	if s, ok := toolName.(string); ok {
		name = s
	} else if !isUndefined(toolName) {
		name = fmt.Sprint(toolName)
	}
	return fmt.Sprintf("anon:%s:%d", name, fallbackID)
}

// toolTimeoutInput is the original's ToolTimeoutResolutionInput; a nil field is unset.
type toolTimeoutInput struct {
	CallValue   any     // per-call value from the subagent tool params (highest precedence)
	AgentValue  any     // agent frontmatter default
	ConfigValue any     // global config.toolTimeoutMs
	EnvValue    *string // PI_SUBAGENT_TOOL_TIMEOUT_MS (lowest precedence)
}

// resolveToolTimeoutMs resolves the configured hard timeout; unset (ok false, no error) when nothing is configured.
func resolveToolTimeoutMs(in toolTimeoutInput) (ms int, set bool, errMsg string) {
	type candidate struct {
		label string
		value any
	}
	var winner *candidate
	for _, c := range []candidate{{"toolTimeoutMs", in.CallValue}, {"agent.toolTimeoutMs", in.AgentValue}, {"config.toolTimeoutMs", in.ConfigValue}} {
		if !isUndefined(c.value) {
			c := c
			winner = &c
			break
		}
	}
	if winner == nil && in.EnvValue != nil && jsTrim(*in.EnvValue) != "" {
		winner = &candidate{toolTimeoutEnv, *in.EnvValue}
	}
	if winner == nil {
		return 0, false, ""
	}
	raw := winner.value
	if s, ok := raw.(string); ok && winner.label == toolTimeoutEnv {
		if f := jsStringToNumber(s); !math.IsNaN(f) && jsTrim(s) != "" {
			raw = f
		}
	}
	f, isNum := asNumber(raw)
	if !isNum || !isIntegerValue(raw) || f <= 0 || f > maxTimerDelayMs {
		return 0, false, fmt.Sprintf("%s must be a positive integer no larger than %d.", winner.label, maxTimerDelayMs)
	}
	return int(f), true, ""
}

// toolTimeoutFromEnv reads PI_SUBAGENT_TOOL_TIMEOUT_MS (nil when unset).
func toolTimeoutFromEnv(lookup envLookup) *string {
	if v, ok := lookup(toolTimeoutEnv); ok {
		return &v
	}
	return nil
}
