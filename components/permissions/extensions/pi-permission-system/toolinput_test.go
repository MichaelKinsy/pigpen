package pi_permission_system

import (
	"strings"
	"testing"
)

// mockStringify replaces the serializer the prompt preview delegates to, as the original's vi.mock of json-safe-stringify does
// (ok false is mockReturnValue(undefined)); it restores the real one when the test ends. calls records every input it saw.
func mockStringify(t *testing.T, fn func(value any) (string, bool)) *[]any {
	t.Helper()
	calls := &[]any{}
	previous := safeJSONStringify
	safeJSONStringify = func(value any) (string, bool) {
		*calls = append(*calls, value)
		return fn(value)
	}
	t.Cleanup(func() { safeJSONStringify = previous })
	return calls
}

func returning(s string) func(any) (string, bool) { return func(any) (string, bool) { return s, true } }

func undefinedResult(any) (string, bool) { return "", false }

func TestToolInputPreviewTwins(t *testing.T) {
	const f = "tool-input/tool-input-preview"
	tw(t, f, "TOOL_INPUT_PREVIEW_MAX_LENGTH is 200", func(t *testing.T) { eq(t, ToolInputPreviewMaxLength, 200, "const") })
	tw(t, f, "TOOL_TEXT_SUMMARY_MAX_LENGTH is 80", func(t *testing.T) { eq(t, ToolTextSummaryMaxLength, 80, "const") })

	tw(t, f, "returns text unchanged when within maxLength", func(t *testing.T) { eq(t, TruncateInlineText("hello", 10), "hello", "text") })
	tw(t, f, "does not truncate when length equals maxLength", func(t *testing.T) {
		text := strings.Repeat("a", 200)
		eq(t, TruncateInlineText(text, 200), text, "text")
	})
	tw(t, f, "truncates and appends ellipsis when length exceeds maxLength", func(t *testing.T) {
		eq(t, TruncateInlineText(strings.Repeat("a", 201), 200), strings.Repeat("a", 200)+"\u2026", "text")
	})
	tw(t, f, "truncates long text and appends ellipsis", func(t *testing.T) { eq(t, TruncateInlineText("abcdef", 3), "abc\u2026", "text") })

	tw(t, f, "returns 0 for empty string", func(t *testing.T) { eq(t, CountTextLines(""), 0, "lines") })
	tw(t, f, "returns 1 for a single line with no newline", func(t *testing.T) { eq(t, CountTextLines("hello"), 1, "lines") })
	tw(t, f, "counts LF-separated lines", func(t *testing.T) { eq(t, CountTextLines("line1\nline2\nline3"), 3, "lines") })
	tw(t, f, "counts CRLF-separated lines", func(t *testing.T) { eq(t, CountTextLines("line1\r\nline2"), 2, "lines") })
	tw(t, f, "counts CR-separated lines", func(t *testing.T) { eq(t, CountTextLines("line1\rline2"), 2, "lines") })

	tw(t, f, "uses singular form for 1", func(t *testing.T) { eq(t, FormatCount(1, "line", "lines"), "1 line", "count") })
	tw(t, f, "uses plural form for 0", func(t *testing.T) { eq(t, FormatCount(0, "line", "lines"), "0 lines", "count") })
	tw(t, f, "uses plural form for 2+", func(t *testing.T) { eq(t, FormatCount(3, "line", "lines"), "3 lines", "count") })

	tw(t, f, "delegates serialization to safeJsonStringify", func(t *testing.T) {
		calls := mockStringify(t, returning(`{"key":"value"}`))
		input := jo(t, `{"key":"value"}`)
		eq(t, SerializeToolInputPreview(input), `{"key":"value"}`, "result")
		eq(t, *calls, []any{input}, "calls")
	})
	tw(t, f, "returns empty string when safeJsonStringify returns undefined", func(t *testing.T) {
		mockStringify(t, undefinedResult)
		eq(t, SerializeToolInputPreview(newObject()), "", "result")
	})
	tw(t, f, "returns empty string when serialized value is '{}'", func(t *testing.T) {
		mockStringify(t, returning("{}"))
		eq(t, SerializeToolInputPreview(newObject()), "", "result")
	})
	tw(t, f, "returns empty string when serialized value is 'null'", func(t *testing.T) {
		mockStringify(t, returning("null"))
		eq(t, SerializeToolInputPreview(nil), "", "result")
	})
	tw(t, f, "collapses whitespace in serialized output", func(t *testing.T) {
		mockStringify(t, returning("{\n  \"key\":  \"val\"\n}"))
		eq(t, SerializeToolInputPreview(newObject()), `{ "key": "val" }`, "result")
	})
}

func TestToolInputPromptFormattersTwins(t *testing.T) {
	const f = "tool-input/tool-input-prompt-formatters"
	rec := func(json string) *jsObject { return jo(t, json) }
	contains := func(t *testing.T, got string, want ...string) {
		t.Helper()
		for _, w := range want {
			if !strings.Contains(got, w) {
				t.Errorf("%q does not contain %q", got, w)
			}
		}
	}

	tw(t, f, "returns path from 'path' key", func(t *testing.T) {
		got, ok := GetPromptPath(rec(`{"path":"/foo/bar"}`))
		eq(t, [2]any{got, ok}, [2]any{"/foo/bar", true}, "path")
	})
	tw(t, f, "falls back to 'file_path' key", func(t *testing.T) {
		got, ok := GetPromptPath(rec(`{"file_path":"/baz"}`))
		eq(t, [2]any{got, ok}, [2]any{"/baz", true}, "path")
	})
	tw(t, f, "returns null when neither key is present", func(t *testing.T) {
		_, ok := GetPromptPath(rec(`{}`))
		eq(t, ok, false, "ok")
	})
	tw(t, f, "returns null when path is empty string", func(t *testing.T) {
		_, ok := GetPromptPath(rec(`{"path":""}`))
		eq(t, ok, false, "ok")
	})

	tw(t, f, "returns path-only description when no edits provided", func(t *testing.T) {
		eq(t, FormatEditInputForPrompt(rec(`{"path":"/foo.ts"}`)), "for '/foo.ts' with edit input", "text")
	})
	tw(t, f, "formats single replacement with line counts", func(t *testing.T) {
		got := FormatEditInputForPrompt(rec(`{"path":"/foo.ts","edits":[{"oldText":"line1\nline2","newText":"replaced"}]}`))
		contains(t, got, "for '/foo.ts'", "1 replacement", "2 lines", "1 line")
	})
	tw(t, f, "formats multiple replacements mentioning additional edits", func(t *testing.T) {
		got := FormatEditInputForPrompt(rec(`{"path":"/foo.ts","edits":[{"oldText":"a","newText":"b"},{"oldText":"c","newText":"d"},{"oldText":"e","newText":"f"}]}`))
		contains(t, got, "3 replacements", "2 additional edits")
	})
	tw(t, f, "falls back to oldText/newText when no edits array", func(t *testing.T) {
		contains(t, FormatEditInputForPrompt(rec(`{"path":"/bar.ts","oldText":"old","newText":"new"}`)), "for '/bar.ts'", "1 replacement")
	})
	tw(t, f, "works without a path", func(t *testing.T) {
		got := FormatEditInputForPrompt(rec(`{"edits":[{"oldText":"x","newText":"y"}]}`))
		eq(t, strings.Contains(got, "for '"), false, "path")
		contains(t, got, "1 replacement")
	})

	tw(t, f, "includes path, line count, and character count", func(t *testing.T) {
		contains(t, FormatWriteInputForPrompt(rec(`{"path":"/out.ts","content":"line1\nline2"}`)), "for '/out.ts'", "2 lines", "11 characters")
	})
	tw(t, f, "handles missing content as empty", func(t *testing.T) {
		contains(t, FormatWriteInputForPrompt(rec(`{"path":"/out.ts"}`)), "0 lines", "0 characters")
	})

	tw(t, f, "includes path", func(t *testing.T) {
		eq(t, FormatReadInputForPrompt(rec(`{"path":"/src/foo.ts"}`)), "for path '/src/foo.ts'", "text")
	})
	tw(t, f, "includes offset and limit when present", func(t *testing.T) {
		contains(t, FormatReadInputForPrompt(rec(`{"path":"/x","offset":10,"limit":50}`)), "offset 10", "limit 50")
	})
	tw(t, f, "returns empty string when no path and no options", func(t *testing.T) {
		eq(t, FormatReadInputForPrompt(rec(`{}`)), "", "text")
	})
}

func TestBuiltinToolInputFormattersTwins(t *testing.T) {
	const f = "tool-input/builtin-tool-input-formatters"
	format := func(json string) (string, bool) { return FormatMcpInputForPrompt.Format(jo(t, json)) }
	mustFormat := func(t *testing.T, json string) string {
		t.Helper()
		got, ok := format(json)
		if !ok {
			t.Fatalf("declined %s", json)
		}
		return got
	}
	tw(t, f, "returns undefined when arguments is absent", func(t *testing.T) {
		_, ok := format(`{"tool":"exa:search"}`)
		eq(t, ok, false, "ok")
	})
	tw(t, f, "returns undefined when arguments is an empty object", func(t *testing.T) {
		_, ok := format(`{"tool":"exa:search","arguments":{}}`)
		eq(t, ok, false, "ok")
	})
	tw(t, f, "returns a summary for a single string argument", func(t *testing.T) {
		got := mustFormat(t, `{"tool":"exa:search","arguments":{"query":"typescript generics"}}`)
		for _, w := range []string{"query", "typescript generics"} {
			eq(t, strings.Contains(got, w), true, w)
		}
		eq(t, strings.HasPrefix(got, "with "), true, "prefix")
	})
	tw(t, f, "returns a comma-separated summary for multiple arguments", func(t *testing.T) {
		got := mustFormat(t, `{"tool":"exa:search","arguments":{"query":"test","numResults":5}}`)
		for _, w := range []string{"query", "numResults", "5"} {
			eq(t, strings.Contains(got, w), true, w)
		}
	})
	tw(t, f, "renders number arguments without quotes", func(t *testing.T) {
		got := mustFormat(t, `{"arguments":{"count":42}}`)
		eq(t, strings.Contains(got, "42"), true, "number")
		eq(t, strings.Contains(got, `"42"`), false, "quoted")
	})
	tw(t, f, "renders boolean arguments without quotes", func(t *testing.T) {
		eq(t, strings.Contains(mustFormat(t, `{"arguments":{"verbose":true}}`), "true"), true, "bool")
	})
	tw(t, f, "renders array arguments as '[N items]'", func(t *testing.T) {
		eq(t, strings.Contains(mustFormat(t, `{"arguments":{"ids":[1,2,3]}}`), "[3 items]"), true, "array")
	})
	tw(t, f, "renders nested object arguments as '{…}'", func(t *testing.T) {
		eq(t, strings.Contains(mustFormat(t, `{"arguments":{"filter":{"type":"file"}}}`), "{\u2026}"), true, "object")
	})
	tw(t, f, "truncates the full summary when it exceeds the limit", func(t *testing.T) {
		// Need multiple long-valued args so the joined summary exceeds 160 chars.
		got := mustFormat(t, `{"arguments":{"first":"`+strings.Repeat("x", 80)+`","second":"`+strings.Repeat("y", 80)+`","third":"`+strings.Repeat("z", 80)+`"}}`)
		eq(t, strings.HasSuffix(got, "\u2026"), true, "ellipsis")
	})
	tw(t, f, "truncates long string argument values", func(t *testing.T) {
		got := mustFormat(t, `{"arguments":{"query":"`+strings.Repeat("x", 100)+`"}}`)
		// Should not include the full 100-char string verbatim.
		eq(t, strings.Contains(got, strings.Repeat("x", 100)), false, "verbatim")
	})

	tw(t, f, "registers the mcp formatter in the registry", func(t *testing.T) {
		registry := &ToolInputFormatterRegistry{}
		eq(t, RegisterBuiltinToolInputFormatters(registry), nil, "error")
		eq(t, registry.Get("mcp") == FormatMcpInputForPrompt, true, "identity")
	})
	tw(t, f, "throws if called twice (duplicate registration guard)", func(t *testing.T) {
		registry := &ToolInputFormatterRegistry{}
		eq(t, RegisterBuiltinToolInputFormatters(registry), nil, "first")
		err := RegisterBuiltinToolInputFormatters(registry)
		if err == nil || !strings.Contains(err.Error(), "mcp") {
			t.Fatalf("second registration: %v", err)
		}
	})
}

func TestToolRegistriesTwins(t *testing.T) {
	const fe = "tool-input/tool-access-extractor-registry"
	const ff = "tool-input/tool-input-formatter-registry"
	extractor := func(path string) ToolAccessExtractor {
		return NewToolAccessExtractor(func(*jsObject) (string, bool) { return path, true })
	}
	mustRegister := func(dispose func(), err error) func() {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return dispose
	}

	tw(t, fe, "stores an extractor so resolve() returns it as this node's own", func(t *testing.T) {
		registry := &ToolAccessExtractorRegistry{}
		noop := extractor("/tmp/x")
		mustRegister(registry.Register("my-tool", noop))
		eq(t, registry.Resolve("my-tool"), &ResolvedToolAccessExtractor{Extractor: noop, Origin: "local"}, "resolved")
	})
	tw(t, fe, "returns a disposer that removes the extractor", func(t *testing.T) {
		registry := &ToolAccessExtractorRegistry{}
		dispose, err := registry.Register("my-tool", extractor("/tmp/x"))
		mustRegister(dispose, err)()
		eq(t, registry.Resolve("my-tool") == nil, true, "removed")
	})
	tw(t, fe, "throws when an extractor is already registered for the same tool name", func(t *testing.T) {
		registry := &ToolAccessExtractorRegistry{}
		mustRegister(registry.Register("my-tool", extractor("/tmp/x")))
		_, err := registry.Register("my-tool", NewToolAccessExtractor(func(*jsObject) (string, bool) { return "", false }))
		if err == nil || !strings.Contains(err.Error(), "my-tool") {
			t.Fatalf("error: %v", err)
		}
	})
	tw(t, fe, "allows registering different tool names independently", func(t *testing.T) {
		registry := &ToolAccessExtractorRegistry{}
		a, b := extractor("/a"), extractor("/b")
		mustRegister(registry.Register("tool-a", a))
		mustRegister(registry.Register("tool-b", b))
		eq(t, registry.Resolve("tool-a").Extractor == a, true, "a")
		eq(t, registry.Resolve("tool-b").Extractor == b, true, "b")
	})
	tw(t, fe, "stale disposer does not evict a later registration", func(t *testing.T) {
		registry := &ToolAccessExtractorRegistry{}
		first, second := extractor("/first"), extractor("/second")
		disposeFirst, err := registry.Register("my-tool", first)
		mustRegister(disposeFirst, err)()                  // removes first
		mustRegister(registry.Register("my-tool", second)) // the second registration is now valid
		disposeFirst()                                     // calling the stale disposer again must not remove second
		eq(t, registry.Resolve("my-tool").Extractor == second, true, "second")
	})
	tw(t, fe, "returns undefined for an unregistered tool name", func(t *testing.T) {
		eq(t, (&ToolAccessExtractorRegistry{}).Resolve("unknown") == nil, true, "nil")
	})
	tw(t, fe, "the registered extractor is callable and returns its path", func(t *testing.T) {
		registry := &ToolAccessExtractorRegistry{}
		target := NewToolAccessExtractor(func(input *jsObject) (string, bool) { return input.str("target") })
		mustRegister(registry.Register("ffgrep", target))
		got, ok := registry.Resolve("ffgrep").Extractor.Extract(jo(t, `{"target":"/etc/hosts"}`))
		eq(t, [2]any{got, ok}, [2]any{"/etc/hosts", true}, "hit")
		_, ok = registry.Resolve("ffgrep").Extractor.Extract(jo(t, `{"other":true}`))
		eq(t, ok, false, "miss")
	})

	formatter := func(preview string) ToolInputFormatter { return constFormatter(preview, true) }
	tw(t, ff, "stores a formatter so get() returns it", func(t *testing.T) {
		registry := &ToolInputFormatterRegistry{}
		noop := formatter("preview")
		mustRegister(registry.Register("my-tool", noop))
		eq(t, registry.Get("my-tool") == noop, true, "get")
	})
	tw(t, ff, "returns a disposer that removes the formatter", func(t *testing.T) {
		registry := &ToolInputFormatterRegistry{}
		dispose, err := registry.Register("my-tool", formatter("preview"))
		mustRegister(dispose, err)()
		eq(t, registry.Get("my-tool") == nil, true, "removed")
	})
	tw(t, ff, "throws when a formatter is already registered for the same tool name", func(t *testing.T) {
		registry := &ToolInputFormatterRegistry{}
		mustRegister(registry.Register("my-tool", formatter("preview")))
		_, err := registry.Register("my-tool", constFormatter("", false))
		if err == nil || !strings.Contains(err.Error(), "my-tool") {
			t.Fatalf("error: %v", err)
		}
	})
	tw(t, ff, "allows registering different tool names independently", func(t *testing.T) {
		registry := &ToolInputFormatterRegistry{}
		a, b := formatter("a"), formatter("b")
		mustRegister(registry.Register("tool-a", a))
		mustRegister(registry.Register("tool-b", b))
		eq(t, registry.Get("tool-a") == a && registry.Get("tool-b") == b, true, "both")
	})
	tw(t, ff, "stale disposer does not evict a later registration", func(t *testing.T) {
		registry := &ToolInputFormatterRegistry{}
		first, second := formatter("first"), formatter("second")
		disposeFirst, err := registry.Register("my-tool", first)
		mustRegister(disposeFirst, err)()
		mustRegister(registry.Register("my-tool", second))
		disposeFirst()
		eq(t, registry.Get("my-tool") == second, true, "second")
	})
	tw(t, ff, "returns undefined for an unregistered tool name", func(t *testing.T) {
		eq(t, (&ToolInputFormatterRegistry{}).Get("unknown") == nil, true, "nil")
	})
	tw(t, ff, "the registered formatter is callable and returns its result", func(t *testing.T) {
		registry := &ToolInputFormatterRegistry{}
		fmtRun := NewToolInputFormatter(func(input *jsObject) (string, bool) {
			if cmd, ok := input.str("cmd"); ok {
				return "runs " + cmd, true
			}
			return "", false
		})
		mustRegister(registry.Register("run", fmtRun))
		got, ok := registry.Get("run").Format(jo(t, `{"cmd":"ls"}`))
		eq(t, [2]any{got, ok}, [2]any{"runs ls", true}, "hit")
		_, ok = registry.Get("run").Format(jo(t, `{"other":true}`))
		eq(t, ok, false, "miss")
	})
}
