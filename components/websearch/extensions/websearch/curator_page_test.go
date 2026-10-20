package websearch

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The curator page against the original's output (port/record-curator-page.mjs wrote the fixture
// from port/oracle/curator-page.ts) and the twins of the upstream tests that read the page.

type curatorPageCase struct {
	ID                  string               `json:"id"`
	Queries             []string             `json:"queries"`
	SessionToken        string               `json:"sessionToken"`
	Timeout             int                  `json:"timeout"`
	Available           [][2]any             `json:"available"`
	DefaultProvider     string               `json:"defaultProvider"`
	SearchProvider      string               `json:"searchProvider"`
	SummaryModels       []SummaryModelChoice `json:"summaryModels"`
	DefaultSummaryModel *string              `json:"defaultSummaryModel"`
	Bytes               int                  `json:"bytes"`
	SHA256              string               `json:"sha256"`
}

func (c curatorPageCase) render() string {
	var available ProviderAvailability
	for _, pair := range c.Available {
		available.Set(pair[0].(string), pair[1].(bool))
	}
	return GenerateCuratorPage(c.Queries, c.SessionToken, c.Timeout, available, c.DefaultProvider, c.SearchProvider, c.SummaryModels, c.DefaultSummaryModel)
}

func loadCuratorPageCases(t *testing.T) []curatorPageCase {
	t.Helper()
	data, err := os.ReadFile("testdata/curator-page.json")
	noErr(t, err)
	var fixture struct {
		Cases []curatorPageCase `json:"cases"`
	}
	noErr(t, json.Unmarshal(data, &fixture))
	if len(fixture.Cases) == 0 {
		t.Fatal("no cases")
	}
	return fixture.Cases
}

// TestCuratorPageMatchesTheOriginalByteForByte renders every recorded input with the port and
// requires the length and SHA-256 of the original's output; the minimal case is compared byte for
// byte so a difference is shown, not just detected.
func TestCuratorPageMatchesTheOriginalByteForByte(t *testing.T) {
	for _, c := range loadCuratorPageCases(t) {
		t.Run(c.ID, func(t *testing.T) {
			page := c.render()
			sum := sha256.Sum256([]byte(page))
			if len(page) != c.Bytes || hex.EncodeToString(sum[:]) != c.SHA256 {
				t.Fatalf("the page differs from the original's: %d bytes sha256 %x, want %d bytes sha256 %s", len(page), sum, c.Bytes, c.SHA256)
			}
		})
	}
	want, err := os.ReadFile("testdata/curator-page-minimal.html")
	noErr(t, err)
	got := loadCuratorPageCases(t)[0].render()
	if got != string(want) {
		at := 0
		for at < len(got) && at < len(want) && got[at] == want[at] {
			at++
		}
		t.Fatalf("the minimal page differs at byte %d: %q vs %q", at, got[max(0, at-40):min(len(got), at+40)], string(want[max(0, at-40):min(len(want), at+40)]))
	}
}

func TestCuratorPageAssetHasOneOfEachMarker(t *testing.T) {
	for _, mark := range []string{pageTimeoutMark, pageButtonsMark, pageInlineMark} {
		if n := strings.Count(curatorPageAsset, mark); n != 1 {
			t.Errorf("%s occurs %d times", mark, n)
		}
	}
}

func TestCuratorPageKeepsAUserTextThatLooksLikeAMarker(t *testing.T) {
	queries := []string{pageTimeoutMark, pageButtonsMark, pageInlineMark}
	page := GenerateCuratorPage(queries, "t", 20, NewProviderAvailability("all", false), "exa", "auto", nil, nil)
	if !strings.Contains(page, `"queries":["`+pageTimeoutMark+`","`+pageButtonsMark+`","`+pageInlineMark+`"]`) {
		t.Fatal("a query that looks like a marker was replaced")
	}
}

func TestProviderAvailabilityKeepsItsOrder(t *testing.T) {
	a := NewProviderAvailability("all", true, "exa", false)
	a.Set("brave", true)
	a.Set("all", false)
	raw, err := json.Marshal(a)
	noErr(t, err)
	if string(raw) != `{"all":false,"exa":false,"brave":true}` || a.Get("openai") {
		t.Fatalf("%s", raw)
	}
}

// pagePair builds an availability the way the upstream tests write the object literal.
func pageFor(queries []string, token string, avail ProviderAvailability, def, search string) string {
	return GenerateCuratorPage(queries, token, 20, avail, def, search, nil, nil)
}

func TestUpstream_curator_page_providers(t *testing.T) {
	// One page, one provider: the button, its label and the provider tag of the page's style sheet.
	one := func(name string, flags ...any) string {
		return pageFor([]string{name + " query"}, "session-token", NewProviderAvailability(flags...), name, name)
	}
	tw(t, "brightdata-serp", "curator page exposes Bright Data as a manual provider", func(t *testing.T) {
		page := one("brightdata", "openai", false, "brave", false, "parallel", false, "tinyfish", false, "tavily", false, "serpdive", false, "brightdata", true, "searxng", false, "perplexity", false, "exa", false, "gemini", false, "kimi", false, "anysearch", false)
		matchRE(t, `data-provider="brightdata"`, page)
		matchRE(t, `>Bright Data</button>`, page)
		matchRE(t, `provider-tag\.provider-brightdata`, page)
	})
	tw(t, "jina-search", "curator page exposes Jina Search as a manual provider", func(t *testing.T) {
		adapted(t, "the original also reads README.md for JINA_API_KEY and jinaApiKey; the port's README is checked by the Jina Search slice")
		page := one("jina", "all", false, "openai", false, "brave", false, "parallel", false, "tinyfish", false, "search1api", false, "searchinfinity", false, "querit", false, "tavily", false, "jina", true, "serpdive", false, "kagi", false, "ollama", false, "searxng", false, "perplexity", false, "exa", false, "gemini", false, "kimi", false, "anysearch", false, "xai", false, "brightdata", false, "serpbase", false)
		matchRE(t, `data-provider="jina"`, page)
		matchRE(t, `>Jina</button>`, page)
		matchRE(t, `provider-tag\.provider-jina`, page)
	})
	tw(t, "mistral-provider", "Curator page exposes Mistral as an available manual provider", func(t *testing.T) {
		page := pageFor([]string{"query"}, "token", NewProviderAvailability("all", false, "openai", false, "brave", false, "parallel", false, "parallel-mcp", false, "tinyfish", false, "search1api", false, "searchinfinity", false, "querit", false, "tavily", false, "firecrawl", false, "jina", false, "serpdive", false, "kagi", false, "bocha", false, "ollama", false, "searxng", false, "duckduckgo", false, "perplexity", false, "exa", false, "gemini", false, "kimi", false, "anysearch", false, "xcrawl", false, "xai", false, "mistral", true, "brightdata", false, "serpbase", false, "serper", false, "valyu", false), "mistral", "mistral")
		matchRE(t, `data-provider="mistral"`, page)
		matchRE(t, `>Mistral</button>`, page)
		matchRE(t, `provider === "mistral"\) return "Mistral"`, page)
	})
	tw(t, "new-search-providers", "Curator page exposes Bocha as a manual provider", func(t *testing.T) {
		page := one("bocha", "all", false, "openai", false, "brave", false, "parallel", false, "tinyfish", false, "search1api", false, "searchinfinity", false, "querit", false, "tavily", false, "jina", false, "serpdive", false, "kagi", false, "bocha", true, "ollama", false, "searxng", false, "duckduckgo", false, "perplexity", false, "exa", false, "gemini", false, "kimi", false, "anysearch", false, "xai", false, "brightdata", false, "serpbase", false)
		matchRE(t, `data-provider="bocha"`, page)
		matchRE(t, `>Bocha</button>`, page)
		matchRE(t, `"bocha"`, page)
		matchRE(t, `provider === "bocha"\) return "Bocha"`, page)
	})
	tw(t, "parallel-mcp-provider", "Curator page exposes a usable Parallel MCP provider entry", func(t *testing.T) {
		page := pageFor([]string{"query"}, "session-token", NewProviderAvailability("all", false, "openai", false, "brave", false, "parallel", false, "parallel-mcp", true, "tinyfish", false, "search1api", false, "searchinfinity", false, "querit", false, "tavily", false, "firecrawl", false, "jina", false, "serpdive", false, "kagi", false, "bocha", false, "ollama", false, "searxng", false, "duckduckgo", false, "perplexity", false, "exa", false, "gemini", false, "kimi", false, "anysearch", false, "xai", false, "brightdata", false, "serpbase", false, "serper", false, "valyu", false), "parallel-mcp", "parallel-mcp")
		matchRE(t, `data-provider="parallel-mcp"`, page)
		matchRE(t, `>Parallel MCP</button>`, page)
		matchRE(t, `"parallel", "parallel-mcp", "tinyfish"`, page)
	})
	tw(t, "querit-provider", "curator page exposes Querit as a manual provider", func(t *testing.T) {
		page := one("querit", "all", false, "openai", false, "brave", false, "parallel", false, "tinyfish", false, "search1api", false, "querit", true, "tavily", false, "serpdive", false, "searxng", false, "perplexity", false, "exa", false, "gemini", false, "kimi", false, "anysearch", false)
		matchRE(t, `data-provider="querit"`, page)
		matchRE(t, `>Querit</button>`, page)
		matchRE(t, `provider-tag\.provider-querit`, page)
	})
	tw(t, "search1api", "curator page exposes Search1API as a manual provider", func(t *testing.T) {
		page := one("search1api", "all", false, "openai", false, "brave", false, "parallel", false, "tinyfish", false, "search1api", true, "querit", false, "tavily", false, "serpdive", false, "searxng", false, "perplexity", false, "exa", false, "gemini", false, "kimi", false, "anysearch", false)
		matchRE(t, `data-provider="search1api"`, page)
		matchRE(t, `>Search1API</button>`, page)
		matchRE(t, `provider-tag\.provider-search1api`, page)
	})
	tw(t, "searchinfinity", "curator page exposes Searchinfinity as a manual provider", func(t *testing.T) {
		page := one("searchinfinity", "all", false, "openai", false, "brave", false, "parallel", false, "tinyfish", false, "search1api", false, "searchinfinity", true, "tavily", false, "serpdive", false, "searxng", false, "perplexity", false, "exa", false, "gemini", false, "kimi", false, "anysearch", false)
		matchRE(t, `data-provider="searchinfinity"`, page)
		matchRE(t, `>Searchinfinity</button>`, page)
		matchRE(t, `provider-tag\.provider-searchinfinity`, page)
	})
	tw(t, "tinyfish", "curator page exposes TinyFish as a manual provider", func(t *testing.T) {
		page := one("tinyfish", "all", false, "openai", false, "brave", false, "parallel", false, "tinyfish", true, "tavily", false, "serpdive", false, "brightdata", false, "searxng", false, "perplexity", false, "exa", false, "gemini", false, "kimi", false, "anysearch", false)
		matchRE(t, `data-provider="tinyfish"`, page)
		matchRE(t, `>TinyFish</button>`, page)
		matchRE(t, `provider-tag\.provider-tinyfish`, page)
	})
	tw(t, "valyu-serper-provider", "Curator page exposes usable Serper and Valyu provider entries", func(t *testing.T) {
		page := pageFor([]string{"query"}, "session-token", NewProviderAvailability("all", false, "openai", false, "brave", false, "parallel", false, "tinyfish", false, "search1api", false, "searchinfinity", false, "querit", false, "tavily", false, "firecrawl", false, "jina", false, "serpdive", false, "kagi", false, "bocha", false, "ollama", false, "searxng", false, "duckduckgo", false, "perplexity", false, "exa", false, "gemini", false, "kimi", false, "anysearch", false, "xai", false, "brightdata", false, "serpbase", false, "serper", true, "valyu", true), "serper", "valyu")
		matchRE(t, `data-provider="serper"`, page)
		matchRE(t, `>Serper</button>`, page)
		matchRE(t, `data-provider="valyu"`, page)
		matchRE(t, `>Valyu</button>`, page)
		matchRE(t, `"serpbase", "serpapi", "serper", "serply", "valyu"`, page)
	})
	tw(t, "xai-provider", "curator page exposes xAI as a manual provider", func(t *testing.T) {
		page := one("xai", "all", false, "openai", false, "brave", false, "parallel", false, "tinyfish", false, "search1api", false, "searchinfinity", false, "querit", false, "tavily", false, "serpdive", false, "searxng", false, "perplexity", false, "exa", false, "gemini", false, "kimi", false, "anysearch", false, "xai", true)
		matchRE(t, `>xAI</button>`, page)
		matchRE(t, `"xai":true`, page)
	})
	// The three below test the provider's API error redaction and then the page; a key that was
	// never set reads as false, as it does through the upstream Proxy.
	redactedAndListed := func(t *testing.T, name, label string) {
		adapted(t, "the redaction of the provider's API error is checked by the provider's own fixtures (its slice); this case proves the page half")
		page := pageFor([]string{"query"}, "token", NewProviderAvailability("all", false, name, true), name, name)
		matchRE(t, `data-provider="`+name+`"`, page)
		matchRE(t, `>`+label+`</button>`, page)
		matchRE(t, `provider === "`+name+`"\) return "`+label+`"`, page)
	}
	tw(t, "serpapi-provider", "SerpApi redacts API errors and appears in the Curator", func(t *testing.T) { redactedAndListed(t, "serpapi", "SerpApi") })
	tw(t, "serply-provider", "Serply redacts API errors and appears in the Curator", func(t *testing.T) { redactedAndListed(t, "serply", "Serply") })
	tw(t, "you-provider", "You.com redacts API errors and appears in the Curator", func(t *testing.T) { redactedAndListed(t, "you", `You\.com`) })
	tw(t, "baizhi-provider", "Baizhi is explicit-only, available to provider arrays, labels and the Curator", func(t *testing.T) {
		adapted(t, "the Baizhi provider itself is searched by its slice; this case checks what belongs here: it is not in the provider 'all', its label, and the curator button")
		if sliceHas(AllSearchProviders, "baizhi") {
			t.Fatal("baizhi is searched by provider all")
		}
		if ProviderLabel("baizhi") != "Baizhi" {
			t.Fatal(ProviderLabel("baizhi"))
		}
		page := pageFor([]string{"q"}, "token", NewProviderAvailability("all", false, "baizhi", true), "baizhi", "baizhi")
		matchRE(t, `data-provider="baizhi"`, page)
	})
	tw(t, "all-provider", `"all" is a Curator provider but remains invalid inside sequential searchRouting`, func(t *testing.T) {
		page := pageFor([]string{"combined query"}, "session-token", NewProviderAvailability("all", true, "openai", false, "brave", true, "parallel", false, "tinyfish", true, "search1api", false, "searchinfinity", false, "querit", false, "tavily", false, "serpdive", false, "searxng", false, "perplexity", false, "exa", true, "gemini", false, "kimi", false, "anysearch", true), "all", "all")
		matchRE(t, `data-provider="all"`, page)
		matchRE(t, `>All</button>`, page)
		matchRE(t, `provider-tag\.provider-all`, page)
		matchRE(t, `function applySearchResponseEntries`, page)
		matchRE(t, `data\.slotIndex`, page)
		// The curator timeout must not start while initial provider searches are still running.
		matchRE(t, `var idleSec = searchesDone \? Math\.floor`, page)

		geminiOnly := pageFor([]string{"gemini web only"}, "session-token", NewProviderAvailability("all", false, "openai", false, "brave", false, "parallel", false, "tinyfish", false, "search1api", false, "searchinfinity", false, "querit", false, "tavily", false, "serpdive", false, "searxng", false, "perplexity", false, "exa", false, "gemini", true, "kimi", false, "anysearch", false), "gemini", "gemini")
		noMatchRE(t, `data-provider="all"`, geminiOnly)
		matchRE(t, `data-provider="gemini"`, geminiOnly)

		_, dir := isolate(t)
		writeConfig(t, dir, `{"searchRouting":{"providers":["all"],"fallbackOn":["network"]}}`)
		_, err := Search(bg(), "invalid routing", FullSearchOptions{Provider: Auto})
		wantErr(t, err, `searchRouting\.providers .*invalid provider: all`)
	})
}
