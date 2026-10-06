// SPDX-License-Identifier: MIT

package rpiv_web_tools

import (
	"strings"
)

// The tool-call renderer and the per-call provider override. upstream: web-tools.ts renderCall, renderResult,
// renderSearchResultsPreview, renderFetchedContentPreview and instantiateProvider.
//
// The renderer is written against a plain theme of styling functions rather than a TUI type, so its text is a pure
// function of the result and a theme the twins supply: the original's `theme.fg(token, text)` becomes a func here.

// theme styles text the way PiG's theme does. upstream: the Theme argument of renderCall and renderResult.
type theme struct {
	fg   func(token, text string) string
	bold func(text string) string
}

// plainTheme is a theme that returns its input unchanged, so a twin asserts the text itself rather than the styling.
func plainTheme() theme {
	return theme{
		fg:   func(_ string, text string) string { return text },
		bold: func(text string) string { return text },
	}
}

// searchRenderCall is the one-line header above a web_search call. upstream: web-tools.ts renderCall for web_search.
func searchRenderCall(th theme, query, provider string, hasProvider bool) string {
	text := th.fg("toolTitle", th.bold("WebSearch "))
	text += th.fg("accent", `"`+query+`"`)
	if hasProvider {
		text += th.fg("dim", " via "+provider)
	}
	return text
}

// searchRenderResult is the result line: the count while running, then the count with an optional preview. upstream:
// web-tools.ts renderResult for web_search.
func searchRenderResult(th theme, partial, expanded bool, details searchEnvelopeDetails, results []searchResult) string {
	if partial {
		return th.fg("warning", "Searching...")
	}
	count := details.ResultCount
	suffix := "s"
	if count == 1 {
		suffix = ""
	}
	text := th.fg("success", "✓ "+itoa(count)+" result"+suffix)
	if expanded {
		text += renderSearchResultsPreview(results, th)
	}
	return text
}

// renderSearchResultsPreview lists at most five result titles and an overflow line when there are more. upstream:
// web-tools.ts renderSearchResultsPreview.
func renderSearchResultsPreview(results []searchResult, th theme) string {
	text := ""
	limit := searchResultPreviewLimit
	if len(results) < limit {
		limit = len(results)
	}
	for _, r := range results[:limit] {
		text += "\n  " + th.fg("dim", "• "+r.Title)
	}
	if len(results) > searchResultPreviewLimit {
		text += "\n  " + th.fg("dim", "... and "+itoa(len(results)-searchResultPreviewLimit)+" more")
	}
	return text
}

// fetchRenderCall is the one-line header above a web_fetch call. upstream: web-tools.ts renderCall for web_fetch.
func fetchRenderCall(th theme, target string) string {
	return th.fg("toolTitle", th.bold("WebFetch ")) + th.fg("accent", target)
}

// fetchRenderResult is the result line: running, then success with the optional title and truncation markers and, when
// expanded, the content preview. upstream: web-tools.ts renderResult for web_fetch.
func fetchRenderResult(th theme, partial, expanded bool, title string, hasTitle, truncated bool, content string, hasContent bool) string {
	if partial {
		return th.fg("warning", "Fetching...")
	}
	text := th.fg("success", "✓ Fetched")
	if hasTitle {
		text += th.fg("muted", ": "+title)
	}
	if truncated {
		text += th.fg("warning", " (truncated)")
	}
	if expanded && hasContent {
		text += renderFetchedContentPreview(content, th)
	}
	return text
}

// renderFetchedContentPreview lists at most fifteen lines and an overflow hint pointing at the read tool. upstream:
// web-tools.ts renderFetchedContentPreview.
func renderFetchedContentPreview(content string, th theme) string {
	lines := strings.Split(content, "\n")
	limit := fetchPreviewLineLimit
	if len(lines) < limit {
		limit = len(lines)
	}
	text := ""
	for _, line := range lines[:limit] {
		text += "\n  " + th.fg("dim", line)
	}
	if len(lines) > fetchPreviewLineLimit {
		text += "\n  " + th.fg("muted", "... (use read tool to see full content)")
	}
	return text
}

// instantiateProvider is the four-tier provider resolution: the per-call override wins, then WEB_SEARCH_PROVIDER, then
// config.provider, then brave. The override is validated; the env var is validated only when it is the tier that won,
// so a bogus env var cannot defeat a valid per-call override. upstream: web-tools.ts instantiateProvider.
func instantiateProvider(cfg config, override string, hasOverride bool, env func(string) string) (string, error) {
	var providerName string
	if hasOverride {
		if err := assertKnownProvider(override); err != nil {
			return "", err
		}
		providerName = override
	} else {
		active := resolveActiveProviderName(cfg, env)
		if active.Source == sourceEnv {
			if err := assertKnownProvider(active.Name); err != nil {
				return "", err
			}
		}
		providerName = active.Name
	}
	return providerName, nil
}
