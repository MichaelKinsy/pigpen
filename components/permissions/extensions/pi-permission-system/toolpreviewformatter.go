package pi_permission_system

import "strings"

// Port of src/tool-input/tool-preview-formatter.ts: formats tool inputs for permission prompts and review logs.

// ToolPreviewFormatterOptions are the limits a formatter truncates at (JavaScript numbers).
type ToolPreviewFormatterOptions struct {
	ToolInputPreviewMaxLength float64
	ToolTextSummaryMaxLength  float64
}

// ResolveToolPreviewLimits is the built-in ToolPreviewFormatterOptions. It takes no config: the two operator-facing caps are
// subsumed by the renderer budgets (ADR 0011 section 5), so an operator's values no longer take effect.
func ResolveToolPreviewLimits() ToolPreviewFormatterOptions {
	return ToolPreviewFormatterOptions{ToolInputPreviewMaxLength: ToolInputPreviewMaxLength, ToolTextSummaryMaxLength: ToolTextSummaryMaxLength}
}

// ToolPreviewFormatter formats tool inputs for permission prompts and review logs. The options are the single injection point for
// preview-length configuration.
type ToolPreviewFormatter struct {
	options          ToolPreviewFormatterOptions
	customFormatters ToolInputFormatterLookup
}

// NewToolPreviewFormatter is `new ToolPreviewFormatter(options, customFormatters)`; customFormatters may be nil.
func NewToolPreviewFormatter(options ToolPreviewFormatterOptions, customFormatters ToolInputFormatterLookup) *ToolPreviewFormatter {
	return &ToolPreviewFormatter{options: options, customFormatters: customFormatters}
}

// SanitizeInlineText collapses whitespace, trims, and truncates a string to fit inline. A non-nil maxLength overrides the
// constructor default.
func (f *ToolPreviewFormatter) SanitizeInlineText(value string, maxLength *float64) string {
	limit := f.options.ToolTextSummaryMaxLength
	if maxLength != nil {
		limit = *maxLength
	}
	normalized := jsTrim(collapseJSWhitespace(value))
	if normalized == "" {
		return "empty text"
	}
	return TruncateInlineText(normalized, limit)
}

// FormatJSONInputForPrompt serializes input to inline JSON and truncates it at ToolInputPreviewMaxLength.
func (f *ToolPreviewFormatter) FormatJSONInputForPrompt(input any) string {
	inline := SerializeToolInputPreview(input)
	if inline == "" {
		return ""
	}
	return "with input " + TruncateInlineText(inline, f.options.ToolInputPreviewMaxLength)
}

// FormatSearchInputForPrompt formats search-tool (grep, find, ls) input for a permission prompt.
func (f *ToolPreviewFormatter) FormatSearchInputForPrompt(toolName string, input *jsObject) string {
	var parts []string
	path, hasPath := GetPromptPath(input)
	pattern, hasPattern := recordString(input, "pattern")
	glob, hasGlob := recordString(input, "glob")

	if hasPattern {
		parts = append(parts, "pattern '"+f.SanitizeInlineText(pattern, nil)+"'")
	}
	if hasGlob {
		parts = append(parts, "glob '"+f.SanitizeInlineText(glob, nil)+"'")
	}
	if hasPath {
		parts = append(parts, "path '"+path+"'")
	} else if toolName == "find" || toolName == "grep" || toolName == "ls" {
		parts = append(parts, "current working directory")
	}
	if len(parts) == 0 {
		return ""
	}
	return "for " + strings.Join(parts, ", ")
}

// FormatToolInputForPrompt formats any tool input for display in a permission ask prompt. It dispatches to the pure formatter of
// a known tool and falls back to inline JSON for everything else; a registered custom formatter goes first and may decline.
func (f *ToolPreviewFormatter) FormatToolInputForPrompt(toolName string, input any) string {
	inputRecord := ToRecord(input)

	if f.customFormatters != nil {
		if custom := f.customFormatters.Get(toolName); custom != nil {
			if rendered, ok := custom.Format(inputRecord); ok {
				return rendered
			}
		}
	}

	switch toolName {
	case "edit":
		return FormatEditInputForPrompt(inputRecord)
	case "write":
		return FormatWriteInputForPrompt(inputRecord)
	case "read":
		return FormatReadInputForPrompt(inputRecord)
	case "find", "grep", "ls":
		return f.FormatSearchInputForPrompt(toolName, inputRecord)
	case "mcp":
		// The MCP target is already a request fact on the prompt payload. When no custom formatter is registered (or it declines),
		// produce no additional preview rather than leaking the raw event JSON.
		return ""
	}
	return f.FormatJSONInputForPrompt(input)
}

// FormatGenericToolInputForLog serializes input to inline JSON for the review log, masking sensitive-keyed values. It is unbounded
// here: the writer narrows every field it persists, so a second bound at the producer would be a limit the operator cannot see.
func (f *ToolPreviewFormatter) FormatGenericToolInputForLog(input any) (string, bool) {
	inline := SerializeRedactedToolInputPreview(input)
	if inline == "" {
		return "", false
	}
	return "input " + inline, true
}

// GetToolInputPreviewForLog derives a loggable input preview string for the review log, ok false for none.
func (f *ToolPreviewFormatter) GetToolInputPreviewForLog(result PermissionCheckResult, input any, pathBearingTools map[string]bool) (string, bool) {
	if ClassifyToolKind(result.ToolName) == ToolKindBash || IsProxyMcpCheck(McpKindFields{ToolName: result.ToolName, Source: result.Source}) {
		return "", false
	}
	if pathBearingTools[result.ToolName] {
		preview := f.FormatToolInputForPrompt(result.ToolName, input)
		return preview, preview != ""
	}
	return f.FormatGenericToolInputForLog(input)
}

// PermissionLogContext is the structured log context object of a permission review log entry. A nil pointer is undefined.
type PermissionLogContext struct {
	Command          *string
	Target           *string
	ToolInputPreview *string
	Origin           string
}

// GetPermissionLogContext builds the structured log context object for a permission review log entry.
func (f *ToolPreviewFormatter) GetPermissionLogContext(result PermissionCheckResult, input any, pathBearingTools map[string]bool) PermissionLogContext {
	context := PermissionLogContext{Command: result.Command, Target: result.Target, Origin: result.Origin}
	if preview, ok := f.GetToolInputPreviewForLog(result, input, pathBearingTools); ok {
		context.ToolInputPreview = &preview
	}
	return context
}
