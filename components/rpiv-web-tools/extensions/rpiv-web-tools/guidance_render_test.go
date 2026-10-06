package rpiv_web_tools

import (
	"strings"
	"testing"
)

func TestGuidance(t *testing.T) {
	const f = "web-tools.guidance"
	search := func(cfg webToolsConfig) (string, []string) {
		return guidanceFor(cfg, "web_search", defaultWebSearchSnippet, defaultWebSearchGuidelines)
	}
	fetch := func(cfg webToolsConfig) (string, []string) {
		return guidanceFor(cfg, "web_fetch", defaultWebFetchSnippet, defaultWebFetchGuidelines)
	}
	tw(t, f, "uses built-in defaults when no config file exists", func(t *testing.T) {
		clearEnv(t)
		s, g := search(readConfig())
		fs, fg := fetch(readConfig())
		eq(t, s, defaultWebSearchSnippet)
		eq(t, len(g), len(defaultWebSearchGuidelines))
		eq(t, fs, defaultWebFetchSnippet)
		eq(t, len(fg), len(defaultWebFetchGuidelines))
	})
	tw(t, f, "overrides web_search snippet only, web_fetch uses defaults", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"guidance": obj{"web_search": obj{"promptSnippet": "Custom search snippet"}}})
		s, g := search(readConfig())
		fs, _ := fetch(readConfig())
		eq(t, s, "Custom search snippet")
		eq(t, len(g), len(defaultWebSearchGuidelines))
		eq(t, fs, defaultWebFetchSnippet)
	})
	tw(t, f, "overrides web_fetch snippet only, web_search uses defaults", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"guidance": obj{"web_fetch": obj{"promptSnippet": "Custom fetch snippet"}}})
		s, _ := search(readConfig())
		fs, _ := fetch(readConfig())
		eq(t, s, defaultWebSearchSnippet)
		eq(t, fs, "Custom fetch snippet")
	})
	tw(t, f, "overrides both tools independently", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"guidance": obj{
			"web_search": obj{"promptSnippet": "Search custom", "promptGuidelines": arr{"Rule S"}},
			"web_fetch":  obj{"promptSnippet": "Fetch custom", "promptGuidelines": arr{"Rule F"}}}})
		s, g := search(readConfig())
		fs, fg := fetch(readConfig())
		eq(t, s, "Search custom")
		eq(t, g, []string{"Rule S"})
		eq(t, fs, "Fetch custom")
		eq(t, fg, []string{"Rule F"})
	})
	tw(t, f, "falls back to defaults on invalid guidance types", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"guidance": obj{"web_search": obj{"promptSnippet": float64(123)}, "web_fetch": obj{"promptGuidelines": "not-array"}}})
		s, _ := search(readConfig())
		_, fg := fetch(readConfig())
		eq(t, s, defaultWebSearchSnippet)
		eq(t, len(fg), len(defaultWebFetchGuidelines))
	})
	tw(t, f, "falls back to defaults on empty promptSnippet", func(t *testing.T) {
		clearEnv(t)
		writeConfigFile(t, obj{"guidance": obj{"web_search": obj{"promptSnippet": ""}}})
		s, _ := search(readConfig())
		eq(t, s, defaultWebSearchSnippet)
	})
}

func TestRender(t *testing.T) {
	const f = "web-tools.render"
	th := plainTheme{}
	results := func(n int) []any {
		out := []any{}
		for i := 1; i <= n; i++ {
			out = append(out, obj{"title": "R" + string(rune('0'+i)), "url": "https://x/" + string(rune('0'+i)), "snippet": ""})
		}
		return out
	}
	tw(t, f, `emits "WebSearch" label and quoted query`, func(t *testing.T) {
		text := renderSearchCallText(obj{"query": "ai news"}, th)
		eq(t, strings.Contains(text, "WebSearch"), true)
		eq(t, strings.Contains(text, `"ai news"`), true)
	})
	tw(t, f, `returns "Searching..." while partial`, func(t *testing.T) {
		eq(t, strings.Contains(renderSearchResultText(obj{}, false, true, th), "Searching..."), true)
	})
	tw(t, f, "pluralizes result count (0 → results, 1 → result, 3 → results)", func(t *testing.T) {
		node := func(n int) string {
			return renderSearchResultText(obj{"resultCount": float64(n), "results": arr{}}, false, false, th)
		}
		eq(t, strings.Contains(node(0), "0 results"), true)
		eq(t, strings.Contains(node(1), "1 result"), true)
		eq(t, strings.Contains(node(1), "1 results"), false)
		eq(t, strings.Contains(node(3), "3 results"), true)
	})
	tw(t, f, "collapsed view shows count only (no result titles)", func(t *testing.T) {
		rs := arr{obj{"title": "First", "url": "https://a", "snippet": ""}, obj{"title": "Second", "url": "https://b", "snippet": ""}}
		text := renderSearchResultText(obj{"resultCount": float64(2), "results": rs}, false, false, th)
		eq(t, strings.Contains(text, "2 results"), true)
		eq(t, strings.Contains(text, "First"), false)
		eq(t, strings.Contains(text, "Second"), false)
	})
	tw(t, f, "expanded view lists all results when count <= preview limit (5)", func(t *testing.T) {
		text := renderSearchResultText(obj{"resultCount": float64(4), "results": results(4)}, true, false, th)
		eq(t, strings.Contains(text, "R1"), true)
		eq(t, strings.Contains(text, "R4"), true)
		eq(t, strings.Contains(text, "more"), false)
	})
	tw(t, f, "expanded view caps at 5 and shows overflow line when count > 5", func(t *testing.T) {
		text := renderSearchResultText(obj{"resultCount": float64(7), "results": results(7)}, true, false, th)
		eq(t, strings.Contains(text, "R1"), true)
		eq(t, strings.Contains(text, "R5"), true)
		eq(t, strings.Contains(text, "R6"), false)
		eq(t, strings.Contains(text, "... and 2 more"), true)
	})
	tw(t, f, `emits "WebFetch" label and the URL`, func(t *testing.T) {
		text := renderFetchCallText(obj{"url": "https://example.com/page"}, th)
		eq(t, strings.Contains(text, "WebFetch"), true)
		eq(t, strings.Contains(text, "https://example.com/page"), true)
	})
	fetchText := func(details obj, content string, expanded, partial bool) string {
		return renderFetchResultText(details, content, true, expanded, partial, th)
	}
	tw(t, f, `returns "Fetching..." while partial`, func(t *testing.T) {
		eq(t, strings.Contains(fetchText(obj{"url": "https://x"}, "", false, true), "Fetching..."), true)
	})
	tw(t, f, "collapsed: success marker only when no title/no truncation", func(t *testing.T) {
		text := fetchText(obj{"url": "https://x"}, "body", false, false)
		eq(t, strings.Contains(text, "✓ Fetched"), true)
		eq(t, strings.Contains(text, ":"), false)
		eq(t, strings.Contains(text, "(truncated)"), false)
	})
	tw(t, f, "collapsed: includes title suffix when details.title set", func(t *testing.T) {
		eq(t, strings.Contains(fetchText(obj{"url": "https://x", "title": "My Page"}, "body", false, false), ": My Page"), true)
	})
	tw(t, f, "collapsed: shows (truncated) when details.truncation.truncated", func(t *testing.T) {
		eq(t, strings.Contains(fetchText(obj{"url": "https://x", "truncation": obj{"truncated": true}}, "body", false, false), "(truncated)"), true)
	})
	tw(t, f, "collapsed: shows both title and (truncated) when both present", func(t *testing.T) {
		text := fetchText(obj{"url": "https://x", "title": "T", "truncation": obj{"truncated": true}}, "body", false, false)
		eq(t, strings.Contains(text, ": T"), true)
		eq(t, strings.Contains(text, "(truncated)"), true)
	})
	body := func(n int) string {
		var ls []string
		for i := 1; i <= n; i++ {
			ls = append(ls, "line "+itoa(i))
		}
		return strings.Join(ls, "\n")
	}
	tw(t, f, "expanded: preview shows all lines when content has ≤ 15 lines", func(t *testing.T) {
		text := fetchText(obj{"url": "https://x"}, body(10), true, false)
		eq(t, strings.Contains(text, "line 1"), true)
		eq(t, strings.Contains(text, "line 10"), true)
		eq(t, strings.Contains(text, "use read tool"), false)
	})
	tw(t, f, "expanded: preview caps at 15 lines and shows overflow hint when content exceeds limit", func(t *testing.T) {
		text := fetchText(obj{"url": "https://x"}, body(20), true, false)
		eq(t, strings.Contains(text, "line 15"), true)
		eq(t, strings.Contains(text, "line 16"), false)
		eq(t, strings.Contains(text, "use read tool"), true)
	})
}
