package ternbrowser

import (
	"fmt"
	"regexp"
	"strings"
)

// Selector is a parsed browser-tool selector, JSON-safe for the Tern page kit.
// upstream: packages/coding-agent/src/tools/browser/tern/selectors.ts:11
type Selector struct {
	Engine string `json:"engine"`
	Query  string `json:"query,omitempty"`
	Name   string `json:"name,omitempty"`
	Role   string `json:"role,omitempty"`
	Ref    string `json:"ref,omitempty"`
	ID     int    `json:"id,omitempty"`
	Token  string `json:"token,omitempty"`
}

var (
	prefixedEngines = []string{"text", "xpath", "pierce", "label", "placeholder", "testid", "alt", "title", "role"}
	legacyPrefixes  = []string{"p-aria/", "p-text/", "p-xpath/", "p-pierce/"}
	ariaRefPrefixes = []string{"aria-ref=", "aria-ref/", "ariaref/"}
	legacyPrefixRE  = regexp.MustCompile(`^p-[a-z]+/`)
	ariaRefIDRE     = regexp.MustCompile(`^e\d+$`)
	bareAriaRefRE   = regexp.MustCompile(`^@?(e\d+)$`)
	// upstream: packages/coding-agent/src/tools/browser/aria/aria-snapshot.ts:101-102
	playwrightOnlyRE = regexp.MustCompile(`:has-text\(|:text\(|:text-is\(|:text-matches\(|:visible\b|:hidden\b|:nth-match\(|:near\(|:above\(|:below\(|:right-of\(|:left-of\(`)
	ariaAttrRE       = regexp.MustCompile(`\[\s*(\w+)\s*=\s*(?:"([^"]*)"|'([^']*)')\s*\]`)
	legacyAriaNameRE = regexp.MustCompile(`\[\s*name\s*=\s*(?:"([^"]+)"|'([^']+)'|([^\]]+))\s*\]`)
)

func hasEnginePrefix(selector string) bool {
	if strings.HasPrefix(selector, "aria/") || strings.HasPrefix(selector, "p-") {
		return true
	}
	for _, e := range prefixedEngines {
		if strings.HasPrefix(selector, e+"/") {
			return true
		}
	}
	for _, p := range ariaRefPrefixes {
		if strings.HasPrefix(selector, p) {
			return true
		}
	}
	return false
}

// ParseAriaRefSelector recognizes snapshot-ref selectors (aria-ref=e5, aria-ref= e5,
// aria-ref/e5, ariaref/e5, e5, @e5) and returns the bare ref id, or "".
// upstream: packages/coding-agent/src/tools/browser/aria/aria-snapshot.ts:146-157
func ParseAriaRefSelector(selector string) string {
	trimmed := strings.TrimSpace(selector)
	for _, prefix := range ariaRefPrefixes {
		if strings.HasPrefix(trimmed, prefix) {
			id := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
			if ariaRefIDRE.MatchString(id) {
				return id
			}
			return ""
		}
	}
	if m := bareAriaRefRE.FindStringSubmatch(trimmed); m != nil {
		return m[1]
	}
	return ""
}

func legacyToModern(selector string) string {
	if strings.HasPrefix(selector, "p-aria/") {
		rest := strings.TrimPrefix(selector, "p-aria/")
		if m := legacyAriaNameRE.FindStringSubmatch(rest); m != nil {
			name := m[1]
			if name == "" {
				name = m[2]
			}
			if name == "" {
				name = m[3]
			}
			if name != "" {
				return "aria/" + strings.TrimSpace(name)
			}
		}
		return "aria/" + rest
	}
	return strings.TrimPrefix(selector, "p-")
}

func parseAriaQuery(query string) (Selector, error) {
	if len(query) > 10_000 {
		return Selector{}, fmt.Errorf("Selector %s is too long", query)
	}
	sel := Selector{Engine: "aria"}
	var badAttr string
	defaultName := ariaAttrRE.ReplaceAllStringFunc(query, func(match string) string {
		sub := ariaAttrRE.FindStringSubmatch(match)
		if len(sub) < 4 {
			return match
		}
		attr := sub[1]
		val := sub[2]
		if val == "" {
			val = sub[3]
		}
		switch attr {
		case "name":
			sel.Name = val
		case "role":
			sel.Role = val
		default:
			badAttr = attr
		}
		return ""
	})
	if badAttr != "" {
		return Selector{}, fmt.Errorf("Unknown aria attribute %q in selector", badAttr)
	}
	if defaultName != "" && sel.Name == "" {
		sel.Name = defaultName
	}
	return sel, nil
}

// ParseSelector parses a browser-tool selector string into a Selector.
// upstream: packages/coding-agent/src/tools/browser/tern/selectors.ts:68
func ParseSelector(selector string) (Selector, error) {
	if !hasEnginePrefix(selector) && playwrightOnlyRE.MatchString(selector) {
		return Selector{}, fmt.Errorf(
			"Playwright-only selector %s is not supported by the browser tool. Use a puppeteer text selector (\"text/Allow all\"), an aria selector (\"aria/Name\"), CSS, or \"xpath/...\".",
			jsonQuote(selector),
		)
	}
	if legacyPrefixRE.MatchString(selector) {
		matched := false
		for _, p := range legacyPrefixes {
			if strings.HasPrefix(selector, p) {
				matched = true
				break
			}
		}
		if !matched {
			return Selector{}, fmt.Errorf(
				"Unsupported selector prefix. Use CSS or puppeteer query handlers (aria/, text/, xpath/, pierce/). Got: %s",
				selector,
			)
		}
		return ParseSelector(legacyToModern(selector))
	}
	if ref := ParseAriaRefSelector(selector); ref != "" {
		return Selector{Engine: "ariaRef", Ref: ref}, nil
	}
	trimmed := strings.TrimSpace(selector)
	for _, p := range ariaRefPrefixes {
		if strings.HasPrefix(trimmed, p) {
			return Selector{}, fmt.Errorf("Invalid ARIA ref selector %s; expected e.g. \"aria-ref=e5\".", jsonQuote(selector))
		}
	}
	if strings.HasPrefix(selector, "aria/") {
		return parseAriaQuery(strings.TrimPrefix(selector, "aria/"))
	}
	for _, engine := range prefixedEngines {
		prefix := engine + "/"
		if strings.HasPrefix(selector, prefix) {
			return Selector{Engine: engine, Query: strings.TrimPrefix(selector, prefix)}, nil
		}
	}
	return Selector{Engine: "css", Query: selector}, nil
}
