package websearch

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Port of curator-page.ts: the HTML page of the search curator. The page (markup, CSS and the
// script that runs in the browser) is the original's, byte for byte, embedded from
// curator_page.html; GenerateCuratorPage only fills in what the original interpolates: the idle
// timeout, the provider buttons and one JSON blob of inputs. The asset is derived from the vendored
// original by port/record-curator-page.mjs, which also records the original's output for the
// inputs of curator_page_test.go so the test proves the bytes are equal.

//go:embed curator_page.html
var curatorPageAsset string

const (
	pageTimeoutMark = "@@CURATOR_TIMEOUT@@"
	pageButtonsMark = "@@CURATOR_PROVIDER_BUTTONS@@"
	pageInlineMark  = "@@CURATOR_INLINE_DATA@@"
)

// curatorPageParts is the asset split at its three markers: parts[0] timeout parts[1] buttons
// parts[2] inline data parts[3].
var curatorPageParts = splitCuratorPage(curatorPageAsset)

func splitCuratorPage(asset string) [4]string {
	var parts [4]string
	rest := asset
	for i, mark := range []string{pageTimeoutMark, pageButtonsMark, pageInlineMark} {
		at := strings.Index(rest, mark)
		if at < 0 {
			panic("curator_page.html lacks the marker " + mark)
		}
		parts[i] = rest[:at]
		rest = rest[at+len(mark):]
	}
	parts[3] = rest
	return parts
}

// ProviderAvailability maps a provider name to whether the curator offers it. Insertion order is
// kept because the page embeds the object as JSON in the order the original built it. Get of a
// name that was never set is false (the original reads a missing key as undefined).
type ProviderAvailability struct {
	keys  []string
	flags map[string]bool
}

// NewProviderAvailability builds an availability from name, flag pairs in order.
func NewProviderAvailability(pairs ...any) ProviderAvailability {
	var a ProviderAvailability
	for i := 0; i+1 < len(pairs); i += 2 {
		a.Set(pairs[i].(string), pairs[i+1].(bool))
	}
	return a
}

// Set records a flag, keeping the position of an existing key.
func (a *ProviderAvailability) Set(name string, ok bool) {
	if a.flags == nil {
		a.flags = map[string]bool{}
	}
	if _, present := a.flags[name]; !present {
		a.keys = append(a.keys, name)
	}
	a.flags[name] = ok
}

// Get is available[name].
func (a ProviderAvailability) Get(name string) bool { return a.flags[name] }

// Clone is an independent copy.
func (a ProviderAvailability) Clone() ProviderAvailability {
	out := ProviderAvailability{keys: append([]string(nil), a.keys...), flags: make(map[string]bool, len(a.flags))}
	for k, v := range a.flags {
		out.flags[k] = v
	}
	return out
}

// Keys are the names in insertion order.
func (a ProviderAvailability) Keys() []string { return append([]string(nil), a.keys...) }

// MarshalJSON writes the object in insertion order.
func (a ProviderAvailability) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range a.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		name, _ := json.Marshal(k)
		b.Write(name)
		b.WriteByte(':')
		b.WriteString(strconv.FormatBool(a.flags[k]))
	}
	b.WriteByte('}')
	return []byte(b.String()), nil
}

// SummaryModelChoice is one entry of the page's summary model dropdown.
type SummaryModelChoice struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// curatorProviderButtons are the provider buttons in the page's order (buildProviderButtons).
var curatorProviderButtons = []struct{ value, label string }{
	{"all", "All"}, {"openai", "OpenAI"}, {"exa", "Exa"}, {"brave", "Brave"}, {"parallel", "Parallel"},
	{"parallel-mcp", "Parallel MCP"}, {"tinyfish", "TinyFish"}, {"search1api", "Search1API"},
	{"searchinfinity", "Searchinfinity"}, {"querit", "Querit"}, {"tavily", "Tavily"}, {"firecrawl", "Firecrawl"},
	{"jina", "Jina"}, {"serpdive", "SERPdive"}, {"kagi", "Kagi"}, {"bocha", "Bocha"}, {"ollama", "Ollama"},
	{"searxng", "SearXNG"}, {"duckduckgo", "DuckDuckGo"}, {"perplexity", "Perplexity"}, {"gemini", "Gemini"},
	{"kimi", "Kimi"}, {"anysearch", "AnySearch"}, {"xcrawl", "XCrawl"}, {"xai", "xAI"}, {"mistral", "Mistral"},
	{"brightdata", "Bright Data"}, {"serpbase", "SerpBase"}, {"serpapi", "SerpApi"}, {"serper", "Serper"},
	{"serply", "Serply"}, {"you", "You.com"}, {"baizhi", "Baizhi"}, {"valyu", "Valyu"},
}

// providerButtonsHTML is buildProviderButtons: one button per available provider; the default
// provider starts in the loading state (and disabled) when there are initial queries.
func providerButtonsHTML(available ProviderAvailability, selected string, hasInitialQueries bool) string {
	var b strings.Builder
	for _, p := range curatorProviderButtons {
		if !available.Get(p.value) {
			continue
		}
		isDefault := p.value == selected
		state, disabled := "idle", ""
		if isDefault && hasInitialQueries {
			state, disabled = "loading", " disabled"
		}
		classes := "provider-btn " + state
		if isDefault {
			classes += " is-default"
		}
		fmt.Fprintf(&b, `<button type="button" class="%s" data-provider="%s" data-state="%s"%s>%s</button>`, classes, p.value, state, disabled, p.label)
	}
	return b.String()
}

// pageInlineData is the object the page script reads (the key order is the original's).
type pageInlineData struct {
	Queries             []string             `json:"queries"`
	SessionToken        string               `json:"sessionToken"`
	Timeout             int                  `json:"timeout"`
	DefaultProvider     string               `json:"defaultProvider"`
	SearchProvider      string               `json:"searchProvider"`
	SummaryModels       []SummaryModelChoice `json:"summaryModels"`
	DefaultSummaryModel *string              `json:"defaultSummaryModel"`
	AvailableProviders  ProviderAvailability `json:"availableProviders"`
}

// safeInlineJSON is JSON.stringify of the data with the characters that could end a script
// element escaped. encoding/json already writes <, > and & as \u003c, \u003e and \u0026 and
// U+2028 and U+2029 as \u2028 and \u2029, which are the replacements safeInlineJSON makes.
func safeInlineJSON(data pageInlineData) string {
	if data.Queries == nil {
		data.Queries = []string{}
	}
	if data.SummaryModels == nil {
		data.SummaryModels = []SummaryModelChoice{}
	}
	b, err := json.Marshal(data)
	if err != nil {
		panic(err) // strings, an integer and booleans cannot fail to encode
	}
	return string(b)
}

// GenerateCuratorPage is generateCuratorPage(queries, sessionToken, timeout, availableProviders,
// defaultProvider, searchProvider, summaryModels, defaultSummaryModel).
func GenerateCuratorPage(queries []string, sessionToken string, timeout int, available ProviderAvailability, defaultProvider, searchProvider string, summaryModels []SummaryModelChoice, defaultSummaryModel *string) string {
	inline := safeInlineJSON(pageInlineData{
		Queries: queries, SessionToken: sessionToken, Timeout: timeout, DefaultProvider: defaultProvider,
		SearchProvider: searchProvider, SummaryModels: summaryModels, DefaultSummaryModel: defaultSummaryModel,
		AvailableProviders: available,
	})
	buttons := providerButtonsHTML(available, defaultProvider, len(queries) > 0)
	var b strings.Builder
	b.Grow(len(curatorPageAsset) + len(inline) + len(buttons))
	b.WriteString(curatorPageParts[0])
	b.WriteString(strconv.Itoa(timeout))
	b.WriteString(curatorPageParts[1])
	b.WriteString(buttons)
	b.WriteString(curatorPageParts[2])
	b.WriteString(inline)
	b.WriteString(curatorPageParts[3])
	return b.String()
}
