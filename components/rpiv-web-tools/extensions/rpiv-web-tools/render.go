package rpiv_web_tools

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// toolContext is the request's cancellation as a context.Context.
func toolContext(ctx sdk.Context) context.Context {
	c, cancel := context.WithCancel(context.Background())
	go func() {
		<-ctx.Done()
		cancel()
	}()
	return c
}

// theme is what the renderers need of a theme (the host's UITheme satisfies it).
type theme interface {
	Fg(color, text string) string
	Bold(text string) string
}

const (
	searchResultPreviewLimit = 5
	fetchPreviewLineLimit    = 15
)

func renderSearchCallText(args map[string]any, th theme) string {
	query, _ := args["query"].(string)
	text := th.Fg("toolTitle", th.Bold("WebSearch "))
	text += th.Fg("accent", fmt.Sprintf("\"%s\"", query))
	if p, _ := args["provider"].(string); p != "" {
		text += th.Fg("dim", " via "+p)
	}
	return text
}

func renderSearchResultText(details map[string]any, expanded, isPartial bool, th theme) string {
	if isPartial {
		return th.Fg("warning", "Searching...")
	}
	count := 0
	if n, ok := details["resultCount"].(float64); ok {
		count = int(n)
	}
	plural := "s"
	if count == 1 {
		plural = ""
	}
	text := th.Fg("success", fmt.Sprintf("✓ %d result%s", count, plural))
	if rs, ok := details["results"].([]any); ok && expanded {
		for i, r := range rs {
			if i >= searchResultPreviewLimit {
				break
			}
			title, _ := r.(map[string]any)["title"].(string)
			text += "\n  " + th.Fg("dim", "• "+title)
		}
		if len(rs) > searchResultPreviewLimit {
			text += "\n  " + th.Fg("dim", fmt.Sprintf("... and %d more", len(rs)-searchResultPreviewLimit))
		}
	}
	return text
}

func renderFetchCallText(args map[string]any, th theme) string {
	u, _ := args["url"].(string)
	return th.Fg("toolTitle", th.Bold("WebFetch ")) + th.Fg("accent", u)
}

func renderFetchResultText(details map[string]any, content string, hasContent, expanded, isPartial bool, th theme) string {
	if isPartial {
		return th.Fg("warning", "Fetching...")
	}
	text := th.Fg("success", "✓ Fetched")
	if title, _ := details["title"].(string); title != "" {
		text += th.Fg("muted", ": "+title)
	}
	if t, ok := details["truncation"].(map[string]any); ok {
		if tr, _ := t["truncated"].(bool); tr {
			text += th.Fg("warning", " (truncated)")
		}
	}
	if expanded && hasContent {
		lines := strings.Split(content, "\n")
		for i, l := range lines {
			if i >= fetchPreviewLineLimit {
				break
			}
			text += "\n  " + th.Fg("dim", l)
		}
		if len(lines) > fetchPreviewLineLimit {
			text += "\n  " + th.Fg("muted", "... (use read tool to see full content)")
		}
	}
	return text
}

func renderSearchCall(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) ([]string, error) {
	return wrapText(renderSearchCallText(args, ctx.UITheme()), width), nil
}

func detailsOf(r sdk.ToolRenderResult) map[string]any { m, _ := r.Details.(map[string]any); return m }

func renderSearchResult(ctx sdk.Context, r sdk.ToolRenderResult, o sdk.ToolRenderResultOptions, _ sdk.ToolRenderContext, width int) ([]string, error) {
	return wrapText(renderSearchResultText(detailsOf(r), o.Expanded, o.IsPartial, ctx.UITheme()), width), nil
}

func renderFetchCall(ctx sdk.Context, args map[string]any, _ sdk.ToolRenderContext, width int) ([]string, error) {
	return wrapText(renderFetchCallText(args, ctx.UITheme()), width), nil
}

func renderFetchResult(ctx sdk.Context, r sdk.ToolRenderResult, o sdk.ToolRenderResultOptions, _ sdk.ToolRenderContext, width int) ([]string, error) {
	content, has := "", false
	if len(r.Content) > 0 && r.Content[0]["type"] == "text" {
		content, has = fmt.Sprint(r.Content[0]["text"]), true
	}
	return wrapText(renderFetchResultText(detailsOf(r), content, has, o.Expanded, o.IsPartial, ctx.UITheme()), width), nil
}
