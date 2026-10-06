// SPDX-License-Identifier: MIT

package rpiv_web_tools

import (
	"os"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The registration layer's own tests: what the extension publishes to the host, and the parameter readers the two
// tools rely on. The tool bodies themselves are the ported logic under test; these check the wiring.

func TestExtensionPublishes(t *testing.T) {
	configHome(t)
	e := Extension()
	if e == nil {
		t.Fatal("the extension must not be nil")
	}
}

func TestToolDefinitions(t *testing.T) {
	configHome(t)
	app := newApp()
	app.client.doer = &fakeHTTP{status: 200, body: "{}"}

	search := app.searchToolDefinition()
	eq(t, search.Name, "web_search", "search tool name")
	eq(t, search.Label, "Web Search", "search tool label")
	eq(t, search.PromptSnippet, defaultWebSearchSnippet, "search snippet from the config")
	eq(t, len(search.PromptGuidelines), 5, "search guidelines")
	eq(t, search.Parameters["type"], "object", "parameters are an object")

	fetch := app.fetchToolDefinition()
	eq(t, fetch.Name, "web_fetch", "fetch tool name")
	eq(t, fetch.Label, "Web Fetch", "fetch tool label")
	eq(t, fetch.PromptSnippet, defaultWebFetchSnippet, "fetch snippet from the config")
	eq(t, len(fetch.PromptGuidelines), 4, "fetch guidelines")
}

func TestSearchParameterSchema(t *testing.T) {
	configHome(t)
	app := newApp()
	props, _ := app.searchToolDefinition().Parameters["properties"].(map[string]any)

	query, ok := props["query"].(map[string]any)
	if !ok {
		t.Fatal("the query parameter must be declared")
	}
	eq(t, query["type"], "string", "query type")
	eq(t, query["description"], "The search query. Be specific and use natural language.", "query description")

	maxResults, ok := props["max_results"].(map[string]any)
	if !ok {
		t.Fatal("max_results must be declared")
	}
	eq(t, maxResults["minimum"], 1.0, "minimum")
	eq(t, maxResults["maximum"], 10.0, "maximum")
	eq(t, maxResults["default"], 5.0, "default")

	provider, ok := props["provider"].(map[string]any)
	if !ok {
		t.Fatal("the provider override must be declared")
	}
	items, ok := provider["items"].(map[string]any)
	if !ok {
		t.Fatal("the provider enum must be declared")
	}
	enum, ok := items["enum"].([]any)
	if !ok || len(enum) != len(knownProviderNames()) {
		t.Fatalf("the enum must carry every provider, got %#v", items["enum"])
	}
	if !strings.Contains(provider["description"].(string), "no silent fallback") {
		t.Fatal("the override description must keep its no-silent-fallback clause")
	}
}

func TestFetchParameterSchema(t *testing.T) {
	configHome(t)
	app := newApp()
	props, _ := app.fetchToolDefinition().Parameters["properties"].(map[string]any)
	if _, ok := props["url"].(map[string]any); !ok {
		t.Fatal("the url parameter must be declared")
	}
	raw, ok := props["raw"].(map[string]any)
	if !ok {
		t.Fatal("the raw parameter must be declared")
	}
	eq(t, raw["type"], "boolean", "raw type")
	eq(t, raw["default"], false, "raw default")
}

func TestParameterReaders(t *testing.T) {
	t.Run("reads the provider override as a string or a one-element array", func(t *testing.T) {
		eq(t, mustOverride(map[string]any{"provider": "exa"}), "exa", "string form")
		eq(t, mustOverride(map[string]any{"provider": []string{"exa"}}), "exa", "string slice")
		eq(t, mustOverride(map[string]any{"provider": []any{"exa"}}), "exa", "any slice")
		if _, ok := providerOverrideParam(map[string]any{}); ok {
			t.Fatal("an absent override is not an override")
		}
		if _, ok := providerOverrideParam(map[string]any{"provider": "  "}); ok {
			t.Fatal("a blank override is not an override")
		}
		if _, ok := providerOverrideParam(map[string]any{"provider": []any{}}); ok {
			t.Fatal("an empty array is not an override")
		}
	})
	t.Run("reads a numeric parameter in the shapes a host delivers", func(t *testing.T) {
		if v, ok := numberFromParams(float64(7)); !ok || v != 7 {
			t.Fatalf("float64 form: %v %v", v, ok)
		}
		if v, ok := numberFromParams(7); !ok || v != 7 {
			t.Fatalf("int form: %v %v", v, ok)
		}
		if v, ok := numberFromParams("9"); !ok || v != 9 {
			t.Fatalf("string form: %v %v", v, ok)
		}
		if _, ok := numberFromParams(true); ok {
			t.Fatal("a boolean is not a number")
		}
	})
}

// mustOverride is the reader's result as a plain string, for the assertions above.
func mustOverride(params map[string]any) string {
	value, _ := providerOverrideParam(params)
	return value
}

func TestRegistrationPieces(t *testing.T) {
	configHome(t)
	t.Run("the interceptor chain is installed from the config at registration", func(t *testing.T) {
		app := newApp()
		eq(t, len(app.registry.interceptors()), 0, "off by default")
		write, _ := configHome(t)
		write(`{"interceptors":{"github":true}}`)
		app = newApp()
		eq(t, len(app.registry.interceptors()), 1, "the object form installs it")
	})
	t.Run("the raw terminal listener holds back chunks while the interceptor is installed", func(t *testing.T) {
		write, _ := configHome(t)
		write(`{"interceptors":{"github":true}}`)
		app := newApp()
		res := app.onTerminalInput("\x1d")
		eq(t, res.Consume, true, "the collapse key is consumed")
		eq(t, len(app.drainCollapseRequests()), 1, "and handed to the session")
	})
	t.Run("without the interceptor every chunk is left to the host", func(t *testing.T) {
		configHome(t)
		app := newApp()
		res := app.onTerminalInput("x")
		eq(t, res.Consume, false, "the host keeps its input")
		eq(t, len(app.drainCollapseRequests()), 0, "nothing is held back")
	})
	t.Run("the theme falls back to the plain one when the host has none", func(t *testing.T) {
		// The renderer must produce text with no theme at all, which is what a headless host offers.
		th := plainTheme()
		eq(t, searchRenderCall(th, "q", "", false), `WebSearch "q"`, "rendered without a theme")
	})
	t.Run("the search details are read back out of a render result", func(t *testing.T) {
		result := sdk.ToolRenderResult{
			Details: map[string]any{
				"query": "q", "backend": "brave", "resultCount": 2.0,
				"results": []searchResult{{Title: "A"}},
			},
			Content: []map[string]any{{"type": "text", "text": "body"}},
		}
		details, results := searchRenderDetails(result)
		eq(t, details.Query, "q", "query")
		eq(t, details.Backend, "brave", "backend")
		eq(t, details.ResultCount, 2, "count")
		eq(t, len(results), 1, "rows")
	})
	t.Run("the fetch details are read back out of a render result", func(t *testing.T) {
		result := sdk.ToolRenderResult{
			Details: map[string]any{"title": "Doc", "truncation": map[string]any{"truncated": true}},
			Content: []map[string]any{{"type": "text", "text": "the body"}},
		}
		title, hasTitle, truncated, content, hasContent := fetchRenderDetails(result)
		eq(t, title, "Doc", "title")
		eq(t, hasTitle, true, "title present")
		eq(t, truncated, true, "truncation flag")
		eq(t, content, "the body", "content")
		eq(t, hasContent, true, "content present")
	})
	t.Run("a partial fetch result has no content to preview", func(t *testing.T) {
		title, hasTitle, truncated, content, hasContent := fetchRenderDetails(sdk.ToolRenderResult{})
		eq(t, title, "", "no title")
		eq(t, hasTitle, false, "title absent")
		eq(t, truncated, false, "not truncated")
		eq(t, content, "", "no content")
		eq(t, hasContent, false, "content absent")
	})
}

func TestFetchBodyBudget(t *testing.T) {
	t.Run("the inline budget matches the original's 50 KiB", func(t *testing.T) {
		eq(t, maxFetchBytes, 50*1024, "budget")
	})
	t.Run("the temp spill keeps its prefix and file name", func(t *testing.T) {
		eq(t, fetchTempDirPrefix, "rpiv-fetch-", "temp dir prefix")
		eq(t, fetchTempFileName, "content.txt", "temp file name")
	})
	t.Run("a spilled body is readable and removable", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", "")
		path, err := spillFullContentToTempFile("body")
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, string(data), "body", "spilled content")
		if err := os.RemoveAll(filepathDir(path)); err != nil {
			t.Fatal(err)
		}
	})
}

// filepathDir is the directory of a path, kept explicit so the assertion reads as cleanup.
func filepathDir(path string) string {
	if idx := strings.LastIndex(path, "/"); idx > 0 {
		return path[:idx]
	}
	return "."
}

func TestRenderHooksUseTheHostFlags(t *testing.T) {
	configHome(t)
	app := newApp()
	// A partial call carries no content yet, which is exactly what a content-emptiness guess would misread; only the
	// host's flag knows it is still running.
	result := sdk.ToolRenderResult{
		Details: map[string]any{"query": "q", "backend": "brave", "resultCount": float64(0)},
		Content: nil,
	}
	collapsed, err := app.renderSearchResult(sdk.Context{}, result,
		sdk.ToolRenderResultOptions{Expanded: false, IsPartial: true}, sdk.ToolRenderContext{}, 80)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, collapsed[0], "Searching...", "the host's partial flag drives the line")
	done, err := app.renderSearchResult(sdk.Context{}, result,
		sdk.ToolRenderResultOptions{Expanded: false, IsPartial: false}, sdk.ToolRenderContext{}, 80)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, done[0], "✓ 0 results", "a finished call reports its count")
	fetching, err := app.renderFetchResult(sdk.Context{}, sdk.ToolRenderResult{},
		sdk.ToolRenderResultOptions{Expanded: false, IsPartial: true}, sdk.ToolRenderContext{}, 80)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, fetching[0], "Fetching...", "the fetch partial line")
}
