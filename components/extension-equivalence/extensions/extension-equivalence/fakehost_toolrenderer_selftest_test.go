//go:build pigsdk_tool_renderer

package extension_equivalence_test

// These tests need a PiG SDK with tool renderers (D89: Extension.ToolRenderer, SetToolRenderers), which
// ship in the PiG 0.4.1 content, not in v0.4.0. `npm run test:go-ports` adds the build tag when the selected SDK
// has them; with the v0.4.0 SDK they are not compiled.

import (
	"fmt"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// D89 (PiG 0.4.1, Pi 1.0.1 pi.registerToolRenderer): an extension's tool renderer resolvers are counted in
// its register frame (`tool_renderers`) and by a `tool_renderers` notify for a later registration; the host
// asks them with `resolve_tool_renderers` and draws the renderers they return with `render_tool` naming
// the returned id. A port of an extension that registers a resolver needs all three in its layer-1 tests.
func TestFakeHostCountsToolRendererResolversAtRegistrationAndLater(t *testing.T) {
	keep := func(toolName string, next func() *sdk.ToolRenderers) *sdk.ToolRenderers { return next() }
	e := sdk.New("selftest")
	e.ToolRenderer(keep)
	e.Command("more", "registers another resolver", func(ctx sdk.Context, args string) error {
		e.ToolRenderer(keep)
		return nil
	})
	h := StartHost(t, e, HostOptions{})
	if got := h.ToolRenderers(); got != 1 {
		t.Fatalf("resolvers after registration = %d, want 1", got)
	}
	if failure := h.Command("more", ""); failure != "" {
		t.Fatalf("command failed: %s", failure)
	}
	if got := h.ToolRenderers(); got != 2 {
		t.Errorf("resolvers after a later registration = %d, want 2", got)
	}
}

func TestFakeHostResolvesToolRenderersAndDrawsTheReturnedOnes(t *testing.T) {
	e := sdk.New("selftest")
	e.ToolRenderer(func(toolName string, next func() *sdk.ToolRenderers) *sdk.ToolRenderers {
		switch toolName {
		case "mcp_search":
			return &sdk.ToolRenderers{
				Call: func(ctx sdk.Context, args map[string]any, render sdk.ToolRenderContext, width int) ([]string, error) {
					render.State["calls"] = 1
					return []string{fmt.Sprintf("search %v at %d (%s)", args["q"], width, render.ToolCallID)}, nil
				},
				Result: func(ctx sdk.Context, result sdk.ToolRenderResult, options sdk.ToolRenderResultOptions, render sdk.ToolRenderContext, width int) ([]string, error) {
					render.Invalidate()
					return []string{fmt.Sprintf("%v hits, calls=%v, expanded=%v", result.Content[0]["text"], render.State["calls"], options.Expanded)}, nil
				},
			}
		case "hidden":
			return nil
		}
		return next()
	})
	h := StartHost(t, e, HostOptions{})

	if got, failure := h.ResolveToolRenderers("read", &ToolRenderersDecl{RendersCall: true, RendersResult: true}); failure != "" || got.Use != "next" {
		t.Errorf("read resolved to %+v (%s), want next()", got, failure)
	}
	if got, failure := h.ResolveToolRenderers("unknown", nil); failure != "" || got.Use != "none" {
		t.Errorf("an unknown tool whose next() is none resolved to %+v (%s), want none", got, failure)
	}
	if got, failure := h.ResolveToolRenderers("hidden", &ToolRenderersDecl{RendersCall: true}); failure != "" || got.Use != "none" {
		t.Errorf("hidden resolved to %+v (%s), want none", got, failure)
	}
	own, failure := h.ResolveToolRenderers("mcp_search", nil)
	if failure != "" || own.Use != "own" || !own.RendersCall || !own.RendersResult || own.RenderShell != "" || own.Renderers == "" {
		t.Fatalf("mcp_search resolved to %+v (%s), want own call and result renderers", own, failure)
	}

	lines, failure := h.RenderTool("mcp_search", ToolRender{Card: "card-1", Renderers: own.Renderers, Phase: "call",
		Args: map[string]any{"q": "pigs"}, Context: map[string]any{"toolCallId": "call-7"}, Width: 40})
	if failure != "" || len(lines) != 1 || lines[0] != "search pigs at 40 (call-7)" {
		t.Errorf("call render = %q (%s)", lines, failure)
	}
	result := map[string]any{"content": []any{map[string]any{"type": "text", "text": "3"}}}
	lines, failure = h.RenderTool("mcp_search", ToolRender{Card: "card-1", Renderers: own.Renderers, Phase: "result",
		Result: result, Options: map[string]any{"expanded": true}})
	if failure != "" || len(lines) != 1 || lines[0] != "3 hits, calls=1, expanded=true" {
		t.Errorf("result render (state shared with the call renderer of the card) = %q (%s)", lines, failure)
	}
	if got := h.Invalidated(); len(got) != 1 || got[0] != "card-1" {
		t.Errorf("invalidated cards = %q, want [card-1]", got)
	}

	// A released card's renderer state is gone: the next render of that card starts empty.
	h.ReleaseToolCard("card-1")
	lines, failure = h.RenderTool("mcp_search", ToolRender{Card: "card-1", Renderers: own.Renderers, Phase: "result", Result: result})
	if failure != "" || len(lines) != 1 || lines[0] != "3 hits, calls=<nil>, expanded=false" {
		t.Errorf("result render after release = %q (%s)", lines, failure)
	}
	if _, failure := h.RenderTool("mcp_search", ToolRender{Card: "card-2", Renderers: "r-unknown", Phase: "call"}); failure == "" {
		t.Error("rendering with renderers the extension never returned succeeded")
	}
}

// A tool's own renderers (SetToolRenderers) are drawn by render_tool without a renderers id.
func TestFakeHostDrawsARegisteredToolsOwnRenderers(t *testing.T) {
	e := sdk.New("selftest")
	e.Tool("greet", "greets", map[string]any{"type": "object"}, func(ctx sdk.Context, params map[string]any) (any, error) { return "hi", nil })
	e.SetToolRenderers("greet", sdk.ToolRenderers{Shell: sdk.ToolRenderShellSelf,
		Call: func(ctx sdk.Context, args map[string]any, render sdk.ToolRenderContext, width int) ([]string, error) {
			return []string{fmt.Sprintf("greet %v", args["name"])}, nil
		}})
	h := StartHost(t, e, HostOptions{})
	lines, failure := h.RenderTool("greet", ToolRender{Card: "c", Phase: "call", Args: map[string]any{"name": "Wilbur"}})
	if failure != "" || len(lines) != 1 || lines[0] != "greet Wilbur" {
		t.Errorf("render = %q (%s)", lines, failure)
	}
	if _, failure := h.RenderTool("greet", ToolRender{Card: "c", Phase: "result"}); failure == "" {
		t.Error("a result render of a tool with no result renderer succeeded")
	}
}
