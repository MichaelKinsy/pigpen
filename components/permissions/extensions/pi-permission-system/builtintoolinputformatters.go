package pi_permission_system

import "strings"

// Port of src/tool-input/builtin-tool-input-formatters.ts: the built-in tool input formatters, registered through the public seam
// at startup, so each one goes through exactly the path a third-party extension would use.

const (
	// mcpArgsSummaryMaxLength is the maximum length of the generated argument summary (before the "with " prefix).
	mcpArgsSummaryMaxLength = 160
	// mcpArgValueMaxLength is the maximum length of a single string argument value (before quoting).
	mcpArgValueMaxLength = 60
)

// renderArgValue renders a single MCP argument value as a compact fragment: strings quoted and truncated, numbers and booleans
// plain, arrays as `[N items]`, objects as `{…}`, everything else as its plain string conversion.
func renderArgValue(value any) string {
	switch v := value.(type) {
	case string:
		return `"` + TruncateInlineText(v, mcpArgValueMaxLength) + `"`
	case float64:
		return jsNumber(v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	case []any:
		return "[" + jsNumber(float64(len(v))) + " items]"
	case nil:
		return "null"
	case undef:
		return "undefined"
	}
	if IsPlainRecord(value) {
		return "{\u2026}"
	}
	return "undefined"
}

// FormatMcpInputForPrompt formats an MCP tool call's `arguments` payload as a human-readable summary. It declines (ok false) when
// `arguments` is absent or empty, so the MCP ask prompt is left unchanged. It is registered as the "mcp" formatter.
var FormatMcpInputForPrompt = NewToolInputFormatter(func(input *jsObject) (string, bool) {
	args := ToRecord(input.vals["arguments"])
	keys := args.order()
	if len(keys) == 0 {
		return "", false
	}
	parts := make([]string, len(keys))
	for i, key := range keys {
		parts[i] = key + ": " + renderArgValue(args.vals[key])
	}
	return "with " + TruncateInlineText(strings.Join(parts, ", "), mcpArgsSummaryMaxLength), true
})

// RegisterBuiltinToolInputFormatters registers all built-in tool input formatters into registry. It is called once from the
// extension factory immediately after the registry is constructed, before any third-party registration can occur; the error is
// the original's thrown duplicate-registration error.
func RegisterBuiltinToolInputFormatters(registry ToolInputFormatterRegistrar) error {
	_, err := registry.Register("mcp", FormatMcpInputForPrompt)
	return err
}
