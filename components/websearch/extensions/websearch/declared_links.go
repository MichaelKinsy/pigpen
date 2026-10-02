package websearch

import (
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// Port of declared-web-links.ts: machine-readable links a page declares itself (service docs, API
// catalogs, OpenAPI descriptions) in Link headers and rel attributes. Only registered relations
// count: no broad URL heuristics.

const (
	maxDeclaredLinks     = 20
	maxDeclaredURLLength = 4096
)

var declarationRelations = map[string]bool{"api-catalog": true, "describedby": true, "service-desc": true, "service-doc": true, "service-meta": true}

var relationLabels = map[string]string{
	"api-catalog":  "API catalog",
	"describedby":  "Description",
	"service-desc": "Service description",
	"service-doc":  "Service documentation",
	"service-meta": "Service metadata",
}

// DeclaredWebLink is a machine-readable link a page declares (service docs, API catalogs).
type DeclaredWebLink struct {
	URL       string
	Relations []string
	Type      string
}

type declaredCandidate struct {
	url       string
	ok        bool
	relations []string
	typ       string
}

type declaredSet struct {
	order []string
	links map[string]*DeclaredWebLink
}

func (s *declaredSet) add(c declaredCandidate) {
	if !c.ok || len(c.relations) == 0 {
		return
	}
	if existing := s.links[c.url]; existing != nil {
		for _, r := range c.relations {
			if !sliceHas(existing.Relations, r) {
				existing.Relations = append(existing.Relations, r)
			}
		}
		if existing.Type == "" {
			existing.Type = normalizeMetadata(c.typ)
		}
		return
	}
	if len(s.links) >= maxDeclaredLinks {
		return
	}
	s.order = append(s.order, c.url)
	s.links[c.url] = &DeclaredWebLink{URL: c.url, Relations: c.relations, Type: normalizeMetadata(c.typ)}
}

// DiscoverDeclaredWebLinks reads declarations from a Link header (nil = absent) and from the
// document's link/a elements that carry rel and href.
func DiscoverDeclaredWebLinks(doc *html.Node, linkHeader *string, responseURL string) []DeclaredWebLink {
	set := &declaredSet{links: map[string]*DeclaredWebLink{}}
	header := ""
	if linkHeader != nil {
		header = *linkHeader
	}
	for _, value := range splitLinkHeader(header) {
		target := linkTargetRE.FindStringSubmatch(value)
		if target == nil {
			continue
		}
		params := parseLinkParameters(value[len(target[0]):])
		if params == nil {
			continue
		}
		if _, anchored := params["anchor"]; anchored {
			continue
		}
		u, ok := resolveHTTPURL(target[1], responseURL)
		set.add(declaredCandidate{url: u, ok: ok, relations: declaredRelations(params["rel"]), typ: params["type"]})
		if len(set.links) >= maxDeclaredLinks {
			break
		}
	}
	if len(set.links) < maxDeclaredLinks && doc != nil {
		documentBase := responseURL
		if base := firstElement(doc, func(n *html.Node) bool { return n.Data == "base" && hasAttr(n, "href") }); base != nil {
			if u, ok := resolveHTTPURL(attrValue(base, "href"), responseURL); ok {
				documentBase = u
			}
		}
		var walk func(*html.Node) bool
		walk = func(n *html.Node) bool {
			if n.Type == html.ElementNode && (n.Data == "link" || n.Data == "a") && hasAttr(n, "rel") && hasAttr(n, "href") {
				u, ok := resolveHTTPURL(attrValue(n, "href"), documentBase)
				set.add(declaredCandidate{url: u, ok: ok, relations: declaredRelations(attrValue(n, "rel")), typ: attrValue(n, "type")})
				if len(set.links) >= maxDeclaredLinks {
					return true
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if walk(c) {
					return true
				}
			}
			return false
		}
		walk(doc)
	}
	out := make([]DeclaredWebLink, 0, len(set.order))
	for _, u := range set.order {
		out = append(out, *set.links[u])
	}
	return out
}

// AppendDeclaredWebLinks adds a "Declared links" section to content.
func AppendDeclaredWebLinks(content string, links []DeclaredWebLink) string {
	if len(links) == 0 {
		return content
	}
	lines := []string{"## Declared links", ""}
	for _, l := range links {
		lines = append(lines, formatDeclaredLink(l))
	}
	section := strings.Join(lines, "\n")
	if trimmed := jsTrim(content); trimmed != "" {
		return trimmed + "\n\n" + section
	}
	return section
}

var (
	linkTargetRE = regexp.MustCompile(`^\s*<([^>]*)>`)
	linkParamRE  = regexp.MustCompile("^\\s*([!#$%&'*+\\-.^_`|~A-Za-z0-9]+)(?:\\s*=\\s*(?:\"((?:\\\\.|[^\"])*)\"|(\\S+)))?\\s*$")
	unescapeRE   = regexp.MustCompile(`\\(.)`)
	spacesRE     = regexp.MustCompile(`\s+`)
)

func declaredRelations(value string) []string {
	var out []string
	for _, r := range strings.Fields(strings.ToLower(jsTrim(value))) {
		if declarationRelations[r] && !sliceHas(out, r) {
			out = append(out, r)
		}
	}
	return out
}

func resolveHTTPURL(value, base string) (string, bool) {
	if value == "" || jsLen(value) > maxDeclaredURLLength {
		return "", false
	}
	var baseURL = mustBase(base)
	u, err := ParseURL(value, baseURL)
	if err != nil {
		return "", false
	}
	href := u.String()
	if jsLen(href) > maxDeclaredURLLength || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false
	}
	return href, true
}

func splitOutsideSyntax(input string, separator byte, protectTargets bool) ([]string, bool) {
	var parts []string
	start := 0
	inTarget, inQuotes, escaped := false, false, false
	for i := 0; i < len(input); i++ {
		c := input[i]
		if inQuotes {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inQuotes = false
			}
			continue
		}
		switch {
		case c == '"' && !inTarget:
			inQuotes = true
		case protectTargets && c == '<':
			inTarget = true
		case protectTargets && c == '>':
			inTarget = false
		case c == separator && !inTarget:
			parts = append(parts, input[start:i])
			start = i + 1
		}
	}
	if inQuotes || inTarget {
		return nil, false
	}
	return append(parts, input[start:]), true
}

func splitLinkHeader(header string) []string {
	parts, ok := splitOutsideSyntax(header, ',', true)
	if !ok {
		return nil
	}
	var out []string
	for _, p := range parts {
		if p = jsTrim(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseLinkParameters(input string) map[string]string {
	parts, ok := splitOutsideSyntax(input, ';', false)
	if !ok || len(parts) == 0 || jsTrim(parts[0]) != "" {
		return nil
	}
	params := map[string]string{}
	for _, part := range parts[1:] {
		m := linkParamRE.FindStringSubmatchIndex(part)
		if m == nil {
			return nil
		}
		name := strings.ToLower(part[m[2]:m[3]])
		value := ""
		switch {
		case m[4] >= 0:
			value = unescapeRE.ReplaceAllString(part[m[4]:m[5]], "$1")
		case m[6] >= 0:
			value = part[m[6]:m[7]]
		}
		if _, dup := params[name]; !dup {
			params[name] = value
		}
	}
	return params
}

func normalizeMetadata(value string) string {
	if value == "" {
		return ""
	}
	return jsSlice(jsTrim(spacesRE.ReplaceAllString(value, " ")), 0, 160)
}

func formatDeclaredLink(l DeclaredWebLink) string {
	rels := make([]string, len(l.Relations))
	for i, r := range l.Relations {
		rels[i] = inlineCode(r)
	}
	typ := ""
	if l.Type != "" {
		typ = "; " + inlineCode(l.Type)
	}
	label := relationLabels[l.Relations[0]]
	if label == "" {
		label = "Declared link"
	}
	return "- " + label + " (" + strings.Join(rels, ", ") + typ + "): <" + l.URL + ">"
}

func inlineCode(v string) string { return "`" + strings.ReplaceAll(v, "`", "'") + "`" }

func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Key == key {
			return true
		}
	}
	return false
}

func attrValue(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func firstElement(n *html.Node, match func(*html.Node) bool) *html.Node {
	if n.Type == html.ElementNode && match(n) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if f := firstElement(c, match); f != nil {
			return f
		}
	}
	return nil
}
