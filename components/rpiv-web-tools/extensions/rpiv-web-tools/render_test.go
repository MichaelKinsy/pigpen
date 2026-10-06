// SPDX-License-Identifier: MIT

package rpiv_web_tools

import (
	"strings"
	"testing"
)

const fRender = "web-tools.render"
const fOverride = "index"

func TestSearchRender(t *testing.T) {
	th := plainTheme()
	tw(t, fRender, "emits \"WebSearch\" label and quoted query", func(t *testing.T) {
		eq(t, searchRenderCall(th, "go generics", "", false), `WebSearch "go generics"`, "header")
	})
	tw(t, fRender, "returns \"Searching...\" while partial", func(t *testing.T) {
		eq(t, searchRenderResult(th, true, false, searchEnvelopeDetails{}, nil), "Searching...", "partial")
	})
	tw(t, fRender, "pluralizes result count (0 → results, 1 → result, 3 → results)", func(t *testing.T) {
		for _, c := range []struct {
			count int
			want  string
		}{{0, "✓ 0 results"}, {1, "✓ 1 result"}, {3, "✓ 3 results"}} {
			got := searchRenderResult(th, false, false, searchEnvelopeDetails{ResultCount: c.count}, nil)
			eq(t, got, c.want, "count line")
		}
	})
	tw(t, fRender, "collapsed view shows count only (no result titles)", func(t *testing.T) {
		got := searchRenderResult(th, false, false, searchEnvelopeDetails{ResultCount: 2},
			[]searchResult{{Title: "One"}, {Title: "Two"}})
		eq(t, got, "✓ 2 results", "no titles when collapsed")
		if strings.Contains(got, "One") {
			t.Fatalf("a collapsed view must not list titles, got %q", got)
		}
	})
	tw(t, fRender, "expanded view lists all results when count <= preview limit (5)", func(t *testing.T) {
		results := []searchResult{{Title: "A"}, {Title: "B"}, {Title: "C"}}
		got := searchRenderResult(th, false, true, searchEnvelopeDetails{ResultCount: 3}, results)
		eq(t, got, "✓ 3 results\n  • A\n  • B\n  • C", "expanded preview")
	})
	tw(t, fRender, "expanded view caps at 5 and shows overflow line when count > 5", func(t *testing.T) {
		results := []searchResult{}
		for _, title := range []string{"A", "B", "C", "D", "E", "F", "G"} {
			results = append(results, searchResult{Title: title})
		}
		got := searchRenderResult(th, false, true, searchEnvelopeDetails{ResultCount: 7}, results)
		eq(t, got, "✓ 7 results\n  • A\n  • B\n  • C\n  • D\n  • E\n  ... and 2 more", "capped preview")
	})
}

func TestFetchRender(t *testing.T) {
	th := plainTheme()
	tw(t, fRender, "emits \"WebFetch\" label and the URL", func(t *testing.T) {
		eq(t, fetchRenderCall(th, "https://example.com"), "WebFetch https://example.com", "header")
	})
	tw(t, fRender, "returns \"Fetching...\" while partial", func(t *testing.T) {
		eq(t, fetchRenderResult(th, true, false, "", false, false, "", false), "Fetching...", "partial")
	})
	tw(t, fRender, "collapsed: success marker only when no title/no truncation", func(t *testing.T) {
		eq(t, fetchRenderResult(th, false, false, "", false, false, "body", true), "✓ Fetched", "collapsed")
	})
	tw(t, fRender, "collapsed: includes title suffix when details.title set", func(t *testing.T) {
		eq(t, fetchRenderResult(th, false, false, "Doc", true, false, "", false), "✓ Fetched: Doc", "title suffix")
	})
	tw(t, fRender, "collapsed: shows (truncated) when details.truncation.truncated", func(t *testing.T) {
		eq(t, fetchRenderResult(th, false, false, "", false, true, "", false), "✓ Fetched (truncated)", "truncation marker")
	})
	tw(t, fRender, "collapsed: shows both title and (truncated) when both present", func(t *testing.T) {
		eq(t, fetchRenderResult(th, false, false, "Doc", true, true, "", false), "✓ Fetched: Doc (truncated)", "both markers")
	})
	tw(t, fRender, "expanded: preview shows all lines when content has ≤ 15 lines", func(t *testing.T) {
		lines := make([]string, 0, 15)
		for i := 1; i <= 15; i++ {
			lines = append(lines, "line"+itoa(i))
		}
		got := fetchRenderResult(th, false, true, "", false, false, strings.Join(lines, "\n"), true)
		eq(t, strings.Count(got, "\n"), 15, "every line shown")
		if strings.Contains(got, "read tool") {
			t.Fatal("no overflow hint when everything fits")
		}
	})
	tw(t, fRender, "expanded: preview caps at 15 lines and shows overflow hint when content exceeds limit", func(t *testing.T) {
		lines := make([]string, 0, 20)
		for i := 1; i <= 20; i++ {
			lines = append(lines, "line"+itoa(i))
		}
		got := fetchRenderResult(th, false, true, "", false, false, strings.Join(lines, "\n"), true)
		eq(t, strings.Count(got, "\n"), 16, "15 lines plus the hint")
		if !strings.Contains(got, "... (use read tool to see full content)") {
			t.Fatalf("the overflow hint is missing, got %q", got)
		}
	})
}

func TestProviderOverride(t *testing.T) {
	tw(t, fOverride, "schema declares the provider enum with all known names", func(t *testing.T) {
		values := providerEnumValues()
		eq(t, len(values), 10, "ten providers")
		for _, name := range knownProviderNames() {
			if !contains(values, name) {
				t.Fatalf("the enum omits %q", name)
			}
		}
	})
	tw(t, fOverride, "routes to the override provider when its key is configured", func(t *testing.T) {
		name, err := instantiateProvider(config{}, "tavily", true, noEnv)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, name, "tavily", "override wins")
	})
	tw(t, fOverride, "override omitted falls back to config.provider (default path unchanged)", func(t *testing.T) {
		name, err := instantiateProvider(config{Provider: "exa"}, "", false, noEnv)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, name, "exa", "config provider")
	})
	tw(t, fOverride, "WEB_SEARCH_PROVIDER beats config.provider", func(t *testing.T) {
		env := envMap(map[string]string{"WEB_SEARCH_PROVIDER": "jina"})
		name, err := instantiateProvider(config{Provider: "exa"}, "", false, env)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, name, "jina", "env wins")
	})
	tw(t, fOverride, "per-call provider override beats WEB_SEARCH_PROVIDER", func(t *testing.T) {
		env := envMap(map[string]string{"WEB_SEARCH_PROVIDER": "jina"})
		name, err := instantiateProvider(config{}, "exa", true, env)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, name, "exa", "override wins over env")
	})
	tw(t, fOverride, "valid per-call override succeeds even when WEB_SEARCH_PROVIDER is bogus", func(t *testing.T) {
		// The override wins without consulting the env, so the bogus env var never gets validated.
		env := envMap(map[string]string{"WEB_SEARCH_PROVIDER": "nope"})
		name, err := instantiateProvider(config{}, "exa", true, env)
		if err != nil {
			t.Fatalf("a valid override must survive a bogus env var, got %v", err)
		}
		eq(t, name, "exa", "override")
	})
	tw(t, fOverride, "whitespace-only WEB_SEARCH_PROVIDER is treated as unset (config wins)", func(t *testing.T) {
		env := envMap(map[string]string{"WEB_SEARCH_PROVIDER": "   "})
		name, err := instantiateProvider(config{Provider: "exa"}, "", false, env)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, name, "exa", "config wins over whitespace")
	})
	tw(t, fOverride, "unknown WEB_SEARCH_PROVIDER name throws (no silent fallback)", func(t *testing.T) {
		env := envMap(map[string]string{"WEB_SEARCH_PROVIDER": "nope"})
		_, err := instantiateProvider(config{Provider: "exa"}, "", false, env)
		if err == nil {
			t.Fatal("a bogus env provider must throw")
		}
		eq(t, err.Error(),
			`Unknown web_search provider: "nope". Valid providers: `+strings.Join(knownProviderNames(), ", ")+`.`,
			"error text")
	})
	tw(t, fOverride, "override with an unknown provider name throws a clear error", func(t *testing.T) {
		_, err := instantiateProvider(config{}, "nope", true, noEnv)
		if err == nil {
			t.Fatal("an unknown override must throw")
		}
		if !strings.HasPrefix(err.Error(), `Unknown web_search provider: "nope"`) {
			t.Fatalf("unexpected error %q", err.Error())
		}
	})
	tw(t, fOverride, "reports source: env when WEB_SEARCH_PROVIDER is set", func(t *testing.T) {
		eq(t, resolveActiveProviderName(config{}, envMap(map[string]string{"WEB_SEARCH_PROVIDER": "jina"})),
			activeProvider{Name: "jina", Source: sourceEnv}, "source env")
	})
	tw(t, fOverride, "reports source: config when config.provider is set and no env", func(t *testing.T) {
		eq(t, resolveActiveProviderName(config{Provider: "exa"}, noEnv),
			activeProvider{Name: "exa", Source: sourceConfig}, "source config")
	})
	tw(t, fOverride, "reports source: default when neither env nor config is set", func(t *testing.T) {
		eq(t, resolveActiveProviderName(config{}, noEnv),
			activeProvider{Name: "brave", Source: sourceDefault}, "source default")
	})
	tw(t, fOverride, "lists the env-named provider first and marks only it ✓ when no config", func(t *testing.T) {
		env := envMap(map[string]string{"WEB_SEARCH_PROVIDER": "jina"})
		labels, active := providerPickerLabels(config{}, env)
		eq(t, active, "jina", "active provider")
		eq(t, labels[0], "Jina ✓", "the env-named provider is first and marked")
		checked := 0
		for _, label := range labels {
			if strings.Contains(label, "✓") {
				checked++
			}
		}
		eq(t, checked, 1, "only one marker")
	})
}
