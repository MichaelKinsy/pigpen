package websearch

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// Port of duckduckgo.ts: the keyless HTML endpoint, explicit-only (never part of auto or all).

const (
	duckduckgoSearchURL = "https://html.duckduckgo.com/html/"
	duckduckgoTimeout   = 30 * time.Second
)

// IsDuckDuckGoAvailable is always true (no credential).
func IsDuckDuckGoAvailable() bool { return true }

func hasClass(n *html.Node, class string) bool {
	for _, a := range n.Attr {
		if a.Key == "class" {
			for _, c := range strings.Fields(a.Val) {
				if c == class {
					return true
				}
			}
		}
	}
	return false
}

func findAll(n *html.Node, class string, out *[]*html.Node) {
	if n.Type == html.ElementNode && hasClass(n, class) {
		*out = append(*out, n)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		findAll(c, class, out)
	}
}

func findFirst(n *html.Node, class string) *html.Node {
	var found []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		findAll(c, class, &found)
		if len(found) > 0 {
			return found[0]
		}
	}
	return nil
}

func textContent(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func decodeDuckDuckGoURL(href string) string {
	base, _ := ParseURL(duckduckgoSearchURL, nil)
	link, err := ParseURL(href, base)
	if err != nil {
		return ""
	}
	dest := link.Query().Get("uddg")
	if !link.Query().Has("uddg") {
		dest = link.String()
	}
	u, err := ParseURL(dest, nil)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return u.String()
}

// SearchWithDuckDuckGo scrapes the DuckDuckGo HTML results page.
func SearchWithDuckDuckGo(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	tctx, cancel := withTimeout(ctx, duckduckgoTimeout)
	defer cancel()
	resp, err := FetchWithCredentialRedirects(tctx, duckduckgoSearchURL+"?q="+formEscape(query), RequestInit{Method: "GET",
		Header: http.Header{"Accept": {"text/html"}, "User-Agent": {"Mozilla/5.0 (compatible; pi-web-access/1.0; +https://github.com/nicobailon/pi-web-access)"}}}, nil)
	if err != nil {
		return nil, timeoutFix(ctx, tctx, err)
	}
	body, err := readBody(resp)
	if err != nil {
		return nil, timeoutFix(ctx, tctx, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("DuckDuckGo search error %d: %s", resp.StatusCode, slice300(body))
	}
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("DuckDuckGo returned no parseable results (invalid response)")
	}
	filters := normalizeDomainFilters(o.DomainFilter)
	limit := NormalizeSearchResultCount(o.NumResults)
	var containers []*html.Node
	findAll(doc, "result", &containers)
	results := []SearchResult{}
	parseable := 0
	for _, c := range containers {
		if hasClass(c, "result--ad") {
			continue
		}
		anchor := findFirst(c, "result__a")
		if anchor == nil {
			continue
		}
		title := jsTrim(textContent(anchor))
		href := jsTrim(attr(anchor, "href"))
		resultURL := ""
		if href != "" {
			resultURL = decodeDuckDuckGoURL(href)
		}
		if title == "" || resultURL == "" {
			continue
		}
		parseable++
		if !filters.matches(resultURL) {
			continue
		}
		snippet := ""
		if s := findFirst(c, "result__snippet"); s != nil {
			snippet = jsTrim(textContent(s))
		}
		results = append(results, SearchResult{Title: title, URL: resultURL, Snippet: snippet})
		if len(results) >= limit {
			break
		}
	}
	if parseable == 0 {
		return nil, fmt.Errorf("DuckDuckGo returned no parseable results (invalid response)")
	}
	return &SearchResponse{Answer: sourceLines(results), Results: results}, nil
}
