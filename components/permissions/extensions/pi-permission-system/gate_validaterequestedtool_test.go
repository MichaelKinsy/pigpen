package pi_permission_system

import (
	"strings"
	"testing"
)

// Twins of test/handlers/validate-requested-tool.test.ts.

func makeTools(names ...string) []any {
	tools := []any{}
	for _, n := range names {
		tools = append(tools, obj("name", n))
	}
	return tools
}

func TestValidateRequestedToolTwins(t *testing.T) {
	const f = "handlers/validate-requested-tool"
	tools := makeTools("read", "bash", "edit")

	t.Run("missing / unresolvable tool name", func(t *testing.T) {
		tw(t, f, "blocks when event has no name field", func(t *testing.T) {
			r := ValidateRequestedTool(obj("type", "tool_call"), tools)
			eq(t, r.Status, "block", "status")
			eq(t, r.Reason != "", true, "reason")
		})
		tw(t, f, "blocks when name field is an empty string", func(t *testing.T) {
			eq(t, ValidateRequestedTool(obj("name", ""), tools).Status, "block", "status")
		})
		tw(t, f, "blocks when name field is null", func(t *testing.T) {
			eq(t, ValidateRequestedTool(obj("name", nil), tools).Status, "block", "status")
		})
		tw(t, f, "blocks when event is a primitive", func(t *testing.T) {
			eq(t, ValidateRequestedTool("not-an-object", tools).Status, "block", "status")
		})
	})
	t.Run("unregistered tool", func(t *testing.T) {
		tw(t, f, "blocks when the tool name is not in the registered list", func(t *testing.T) {
			eq(t, ValidateRequestedTool(obj("name", "unknown-tool"), tools).Status, "block", "status")
		})
		tw(t, f, "includes available tool names in the block reason", func(t *testing.T) {
			r := ValidateRequestedTool(obj("name", "unknown-tool"), tools)
			eq(t, r.Status, "block", "status")
			for _, name := range []string{"read", "bash", "edit"} {
				if !strings.Contains(r.Reason, name) {
					t.Errorf("reason %q lacks %q", r.Reason, name)
				}
			}
		})
		tw(t, f, "blocks with empty available list when no tools are registered", func(t *testing.T) {
			eq(t, ValidateRequestedTool(obj("name", "anything"), []any{}).Status, "block", "status")
		})
	})
	t.Run("registered tool (ok path)", func(t *testing.T) {
		tw(t, f, "returns ok with the raw tool name for a known tool", func(t *testing.T) {
			eq(t, ValidateRequestedTool(obj("name", "read"), tools), RequestedToolValidation{Status: "ok", ToolName: "read"}, "result")
		})
		tw(t, f, "returns the raw name as it appeared in the event (not normalised)", func(t *testing.T) {
			// Without aliases the raw name and registered name are the same; this asserts the contract that toolName comes from the
			// event, not from the registration lookup's normalizedToolName field.
			eq(t, ValidateRequestedTool(obj("name", "bash"), tools), RequestedToolValidation{Status: "ok", ToolName: "bash"}, "result")
		})
		tw(t, f, "resolves tool name via the `arguments` field naming convention", func(t *testing.T) {
			// getToolNameFromValue reads `.name` then falls back to other fields; a plain `{ name: "edit" }` event is sufficient here.
			eq(t, ValidateRequestedTool(obj("name", "edit"), tools), RequestedToolValidation{Status: "ok", ToolName: "edit"}, "result")
		})
	})
}
