package pi_permission_system

import (
	"strings"
	"testing"
)

func TestToolPreviewFormatterTwins(t *testing.T) {
	const f = "tool-input/tool-preview-formatter"
	makeResult := func(toolName string, mod func(*PermissionCheckResult)) PermissionCheckResult {
		// This file's subject is the allow-path preview, so the wrapper defaults to allow.
		return makePermissionCheckResult(toolName, func(r *PermissionCheckResult) {
			r.State = "allow"
			if mod != nil {
				mod(r)
			}
		})
	}
	makeFormatter := func(mod func(*ToolPreviewFormatterOptions), lookup ToolInputFormatterLookup) *ToolPreviewFormatter {
		return makeToolPreviewFormatter(mod, lookup)
	}
	limits := func(preview, summary float64) func(*ToolPreviewFormatterOptions) {
		return func(o *ToolPreviewFormatterOptions) {
			if preview >= 0 {
				o.ToolInputPreviewMaxLength = preview
			}
			if summary >= 0 {
				o.ToolTextSummaryMaxLength = summary
			}
		}
	}
	rec := func(json string) *jsObject { return jo(t, json) }
	contains := func(t *testing.T, got string, want ...string) {
		t.Helper()
		for _, w := range want {
			if !strings.Contains(got, w) {
				t.Errorf("%q does not contain %q", got, w)
			}
		}
	}
	num := func(n float64) *float64 { return &n }

	// sanitizeInlineText
	tw(t, f, "collapses whitespace and trims", func(t *testing.T) {
		eq(t, makeFormatter(nil, nil).SanitizeInlineText("  hello   world  ", nil), "hello world", "text")
	})
	tw(t, f, "returns 'empty text' for blank string", func(t *testing.T) {
		fm := makeFormatter(nil, nil)
		eq(t, fm.SanitizeInlineText("", nil), "empty text", "empty")
		eq(t, fm.SanitizeInlineText("   ", nil), "empty text", "blank")
	})
	tw(t, f, "truncates at constructor toolTextSummaryMaxLength", func(t *testing.T) {
		eq(t, makeFormatter(limits(-1, 5), nil).SanitizeInlineText("hello world", nil), "hello\u2026", "text")
	})
	tw(t, f, "explicit maxLength override takes precedence over constructor default", func(t *testing.T) {
		eq(t, makeFormatter(limits(-1, 80), nil).SanitizeInlineText("hello world", num(5)), "hello\u2026", "text")
	})

	// formatJsonInputForPrompt
	tw(t, f, "returns empty string when serialization yields empty", func(t *testing.T) {
		mockStringify(t, undefinedResult)
		eq(t, makeFormatter(nil, nil).FormatJSONInputForPrompt(newObject()), "", "text")
	})
	tw(t, f, "returns prefixed JSON with 'with input' prefix", func(t *testing.T) {
		mockStringify(t, returning(`{"k":"v"}`))
		eq(t, makeFormatter(nil, nil).FormatJSONInputForPrompt(rec(`{"k":"v"}`)), `with input {"k":"v"}`, "text")
	})
	tw(t, f, "truncates at constructor toolInputPreviewMaxLength", func(t *testing.T) {
		mockStringify(t, returning(`"`+strings.Repeat("x", 20)+`"`))
		result := makeFormatter(limits(10, -1), nil).FormatJSONInputForPrompt(newObject())
		preview := result[len("with input "):]
		eq(t, utf16LenWTF(preview), 11, "length") // 10 + 1 for the ellipsis
		eq(t, strings.HasSuffix(preview, "\u2026"), true, "ellipsis")
	})
	tw(t, f, "does not truncate when within toolInputPreviewMaxLength", func(t *testing.T) {
		mockStringify(t, returning(`{"k":"v"}`))
		eq(t, makeFormatter(limits(200, -1), nil).FormatJSONInputForPrompt(rec(`{"k":"v"}`)), `with input {"k":"v"}`, "text")
	})

	// formatSearchInputForPrompt
	tw(t, f, "includes pattern and path", func(t *testing.T) {
		contains(t, makeFormatter(nil, nil).FormatSearchInputForPrompt("grep", rec(`{"pattern":"TODO","path":"/src"}`)), "pattern 'TODO'", "path '/src'")
	})
	tw(t, f, "truncates pattern at toolTextSummaryMaxLength", func(t *testing.T) {
		contains(t, makeFormatter(limits(-1, 5), nil).FormatSearchInputForPrompt("grep", rec(`{"pattern":"abcdefgh"}`)), "abcde\u2026")
	})
	tw(t, f, "uses 'current working directory' for find/grep/ls without path", func(t *testing.T) {
		for _, toolName := range []string{"find", "grep", "ls"} {
			contains(t, makeFormatter(nil, nil).FormatSearchInputForPrompt(toolName, rec(`{}`)), "current working directory")
		}
	})
	tw(t, f, "returns empty string for unknown tool with no input", func(t *testing.T) {
		eq(t, makeFormatter(nil, nil).FormatSearchInputForPrompt("other", rec(`{}`)), "", "text")
	})

	// formatToolInputForPrompt
	tw(t, f, "dispatches 'edit' to standalone formatEditInputForPrompt", func(t *testing.T) {
		mockStringify(t, undefinedResult)
		contains(t, makeFormatter(nil, nil).FormatToolInputForPrompt("edit", rec(`{"path":"/foo.ts","edits":[]}`)), "for '/foo.ts'")
	})
	tw(t, f, "dispatches 'write' to standalone formatWriteInputForPrompt", func(t *testing.T) {
		contains(t, makeFormatter(nil, nil).FormatToolInputForPrompt("write", rec(`{"path":"/out.ts","content":"hi"}`)), "for '/out.ts'")
	})
	tw(t, f, "dispatches 'read' to standalone formatReadInputForPrompt", func(t *testing.T) {
		contains(t, makeFormatter(nil, nil).FormatToolInputForPrompt("read", rec(`{"path":"/src/x.ts"}`)), "path '/src/x.ts'")
	})
	tw(t, f, "dispatches 'find'/'grep'/'ls' to formatSearchInputForPrompt", func(t *testing.T) {
		for _, tool := range []string{"find", "grep", "ls"} {
			contains(t, makeFormatter(nil, nil).FormatToolInputForPrompt(tool, rec(`{}`)), "current working directory")
		}
	})
	tw(t, f, "falls back to formatJsonInputForPrompt for unknown tools", func(t *testing.T) {
		mockStringify(t, returning(`{"x":1}`))
		contains(t, makeFormatter(nil, nil).FormatToolInputForPrompt("unknown", rec(`{"x":1}`)), `{"x":1}`)
	})
	tw(t, f, "unknown tool truncates at constructor toolInputPreviewMaxLength", func(t *testing.T) {
		mockStringify(t, returning(`{"k":"`+strings.Repeat("x", 50)+`"}`))
		result := makeFormatter(limits(10, -1), nil).FormatToolInputForPrompt("custom", newObject())
		preview := result[len("with input "):]
		eq(t, strings.HasSuffix(preview, "\u2026"), true, "ellipsis")
		eq(t, utf16LenWTF(preview), 11, "length") // 10 + the ellipsis
	})

	// formatToolInputForPrompt (custom formatter seam)
	makeLookup := func(toolName string, result string, ok bool) ToolInputFormatterLookup {
		return lookupFunc(func(name string) ToolInputFormatter {
			if name == toolName {
				return constFormatter(result, ok)
			}
			return nil
		})
	}
	tw(t, f, "uses a custom formatter's string result verbatim, bypassing the switch", func(t *testing.T) {
		eq(t, makeFormatter(nil, makeLookup("my-tool", "custom preview", true)).FormatToolInputForPrompt("my-tool", newObject()), "custom preview", "text")
	})
	tw(t, f, "falls through to the built-in switch when custom formatter returns undefined", func(t *testing.T) {
		mockStringify(t, returning(`{"x":1}`))
		got := makeFormatter(nil, makeLookup("unknown-tool", "", false)).FormatToolInputForPrompt("unknown-tool", rec(`{"x":1}`))
		// Falls through to JSON default for unknown tools.
		contains(t, got, `{"x":1}`)
	})
	tw(t, f, "custom formatter for a built-in tool overrides the built-in preview", func(t *testing.T) {
		// Would normally use formatReadInputForPrompt; custom overrides it.
		eq(t, makeFormatter(nil, makeLookup("read", "custom read summary", true)).FormatToolInputForPrompt("read", rec(`{"path":"/foo.ts"}`)), "custom read summary", "text")
	})
	tw(t, f, "absent lookup preserves current behaviour for all tool types", func(t *testing.T) {
		// The built-in path still works.
		contains(t, makeFormatter(nil, nil).FormatToolInputForPrompt("read", rec(`{"path":"/foo.ts"}`)), "/foo.ts")
	})

	// formatGenericToolInputForLog: the log path serializes through redactedJsonStringify, not the mocked prompt-path serializer.
	tw(t, f, "returns undefined when serialization yields empty string", func(t *testing.T) {
		_, ok := makeFormatter(nil, nil).FormatGenericToolInputForLog(newObject())
		eq(t, ok, false, "ok")
	})
	tw(t, f, "returns prefixed input preview", func(t *testing.T) {
		got, ok := makeFormatter(nil, nil).FormatGenericToolInputForLog(rec(`{"k":"v"}`))
		eq(t, [2]any{got, ok}, [2]any{`input {"k":"v"}`, true}, "preview")
	})
	tw(t, f, "does not truncate — the log writer bounds what it persists", func(t *testing.T) {
		got, _ := makeFormatter(nil, nil).FormatGenericToolInputForLog(rec(`{"k":"` + strings.Repeat("x", 2000) + `"}`))
		eq(t, got, `input {"k":"`+strings.Repeat("x", 2000)+`"}`, "preview")
	})
	tw(t, f, "masks a sensitive-keyed value in the logged preview", func(t *testing.T) {
		got, _ := makeFormatter(nil, nil).FormatGenericToolInputForLog(rec(`{"url":"https://example.test","authorization":"Bearer TEST_VALUE"}`))
		eq(t, got, `input {"url":"https://example.test","authorization":"[redacted]"}`, "preview")
	})

	// prompt-vs-log redaction boundary: both paths serialize for real here.
	pathBearing := map[string]bool{"read": true, "write": true, "edit": true}
	input := func() *jsObject { return rec(`{"authorization":"Bearer TEST_VALUE"}`) }
	tw(t, f, "masks the value in the log preview but not in the ask-prompt", func(t *testing.T) {
		fm := makeFormatter(nil, nil)
		got, _ := fm.FormatGenericToolInputForLog(input())
		eq(t, got, `input {"authorization":"[redacted]"}`, "log")
		// The user must see the real input to make a permission decision.
		eq(t, fm.FormatToolInputForPrompt("http", input()), `with input {"authorization":"Bearer TEST_VALUE"}`, "prompt")
	})
	tw(t, f, "masks a generic tool's input reaching the review log", func(t *testing.T) {
		got, _ := makeFormatter(nil, nil).GetToolInputPreviewForLog(makeResult("http", nil), input(), pathBearing)
		eq(t, got, `input {"authorization":"[redacted]"}`, "log")
	})

	// getToolInputPreviewForLog
	tw(t, f, "returns undefined for bash tool", func(t *testing.T) {
		_, ok := makeFormatter(nil, nil).GetToolInputPreviewForLog(makeResult("bash", nil), rec(`{"command":"ls"}`), pathBearing)
		eq(t, ok, false, "ok")
	})
	tw(t, f, "returns undefined for mcp tool", func(t *testing.T) {
		_, ok := makeFormatter(nil, nil).GetToolInputPreviewForLog(makeResult("mcp", nil), rec(`{}`), pathBearing)
		eq(t, ok, false, "ok")
	})
	tw(t, f, "returns undefined for mcp source", func(t *testing.T) {
		result := makeResult("some-server:some-tool", func(r *PermissionCheckResult) { r.Source = "mcp" })
		_, ok := makeFormatter(nil, nil).GetToolInputPreviewForLog(result, rec(`{}`), pathBearing)
		eq(t, ok, false, "ok")
	})
	tw(t, f, "previews a Pi MCP tool's arguments, which are its own input", func(t *testing.T) {
		result := makeResult("mcp__danger_srv__wipe", func(r *PermissionCheckResult) { r.Source = "mcp" })
		got, ok := makeFormatter(nil, nil).GetToolInputPreviewForLog(result, rec(`{"target":"prod"}`), pathBearing)
		eq(t, [2]any{got, ok}, [2]any{`input {"target":"prod"}`, true}, "preview")
	})
	tw(t, f, "returns path-based preview for path-bearing tools", func(t *testing.T) {
		got, _ := makeFormatter(nil, nil).GetToolInputPreviewForLog(makeResult("read", nil), rec(`{"path":"/src/foo.ts"}`), pathBearing)
		contains(t, got, "/src/foo.ts")
	})
	tw(t, f, "does not truncate a path preview — the log writer bounds it", func(t *testing.T) {
		longPath := "/src/" + strings.Repeat("a", 2000) + ".ts"
		got, ok := makeFormatter(nil, nil).GetToolInputPreviewForLog(makeResult("read", nil), rec(`{"path":"`+longPath+`"}`), pathBearing)
		eq(t, ok, true, "defined")
		contains(t, got, longPath)
	})
	tw(t, f, "returns generic JSON preview for non-path-bearing tools", func(t *testing.T) {
		mockStringify(t, returning(`{"n":1}`))
		got, _ := makeFormatter(nil, nil).GetToolInputPreviewForLog(makeResult("task", nil), rec(`{"n":1}`), pathBearing)
		contains(t, got, `{"n":1}`)
	})

	// getPermissionLogContext
	tw(t, f, "returns command, target, toolInputPreview, and origin fields", func(t *testing.T) {
		result := makeResult("bash", func(r *PermissionCheckResult) { r.Command = sp("ls -la") })
		ctx := makeFormatter(nil, nil).GetPermissionLogContext(result, rec(`{}`), pathBearing)
		eq(t, ctx.Command, sp("ls -la"), "command")
		eq(t, ctx.Target == nil, true, "target")
		eq(t, ctx.ToolInputPreview == nil, true, "preview")
	})
	tw(t, f, "includes toolInputPreview for non-bash path-bearing tools", func(t *testing.T) {
		ctx := makeFormatter(nil, nil).GetPermissionLogContext(makeResult("read", nil), rec(`{"path":"/foo.ts"}`), pathBearing)
		if ctx.ToolInputPreview == nil {
			t.Fatal("no preview")
		}
		contains(t, *ctx.ToolInputPreview, "/foo.ts")
	})
	tw(t, f, "includes origin from check result", func(t *testing.T) {
		ctx := makeFormatter(nil, nil).GetPermissionLogContext(makeResult("read", func(r *PermissionCheckResult) { r.Origin = "project" }), rec(`{}`), pathBearing)
		eq(t, ctx.Origin, "project", "origin")
	})
	tw(t, f, "toolInputPreview carries the whole path, for the writer to bound", func(t *testing.T) {
		longPath := "/src/" + strings.Repeat("a", 2000) + ".ts"
		ctx := makeFormatter(nil, nil).GetPermissionLogContext(makeResult("read", nil), rec(`{"path":"`+longPath+`"}`), pathBearing)
		if ctx.ToolInputPreview == nil {
			t.Fatal("no preview")
		}
		contains(t, *ctx.ToolInputPreview, longPath)
	})

	// resolveToolPreviewLimits: the two operator-facing caps are subsumed by the renderer budgets, so the limits are the built-in
	// constants alone.
	tw(t, f, "returns the three built-in constants", func(t *testing.T) {
		eq(t, ResolveToolPreviewLimits(), ToolPreviewFormatterOptions{ToolInputPreviewMaxLength: ToolInputPreviewMaxLength, ToolTextSummaryMaxLength: ToolTextSummaryMaxLength}, "limits")
	})
}
