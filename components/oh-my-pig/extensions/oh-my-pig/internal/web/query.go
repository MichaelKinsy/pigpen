package web

// Structured web-search query parsing, formatting, and lenient constraint post-filtering.
// upstream: packages/coding-agent/src/web/search/query.ts

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// QueryTerm is one free-text token of a search query.
// upstream: packages/coding-agent/src/web/search/query.ts:22-35
type QueryTerm struct {
	Text    string
	Phrase  bool
	Negated bool
	Group   int // 0 means not part of an OR group
}

// StructuredQuery is a raw query decomposed into free text plus recognized directives.
// upstream: packages/coding-agent/src/web/search/query.ts:44-86
type StructuredQuery struct {
	Raw               string
	Text              string
	Terms             []QueryTerm
	Sites             []string
	ExcludedSites     []string
	InURL             []string
	ExcludedInURL     []string
	InTitle           []string
	ExcludedInTitle   []string
	InText            []string
	ExcludedInText    []string
	Filetypes         []string
	ExcludedFiletypes []string
	After             string
	Before            string
	Lang              string
	HasDirectives     bool
	HasConstraints    bool
}

// QuerySyntax describes which query operators a target search engine understands.
// upstream: packages/coding-agent/src/web/search/query.ts:94-113
type QuerySyntax struct {
	Phrases   bool
	Negation  bool
	Or        bool
	Site      bool
	InURL     bool
	InTitle   bool
	InText    bool
	Filetype  bool
	DateRange bool
}

// GoogleQuerySyntax enables the full classic operator set.
// upstream: packages/coding-agent/src/web/search/query.ts:116-126
var GoogleQuerySyntax = QuerySyntax{
	Phrases:   true,
	Negation:  true,
	Or:        true,
	Site:      true,
	InURL:     true,
	InTitle:   true,
	InText:    true,
	Filetype:  true,
	DateRange: true,
}

// ConstraintFilterResult holds surviving sources and any relaxed constraints.
// upstream: packages/coding-agent/src/web/search/query.ts:129-138
type ConstraintFilterResult struct {
	Sources []SearchSource
	Dropped []string
}

var (
	directivePattern   = regexp.MustCompile(`(?i)^([+-]?)([a-z][a-z-]*):(.*)$`)
	schemePrefixRe     = regexp.MustCompile(`^[a-z][a-z0-9+.-]*://`)
	isoDatePattern     = regexp.MustCompile(`^(\d{4})(?:[-/.](\d{1,2})(?:[-/.](\d{1,2}))?)?$`)
	mdyDatePattern     = regexp.MustCompile(`^(\d{1,2})[-/.](\d{1,2})[-/.](\d{4})$`)
	relativeAgePattern = regexp.MustCompile(`(?i)^(\d+)\s*(minute|min|hour|hr|day|week|month|mo|year|yr|[mhdwy])s?\s+ago$`)
)

type rawToken struct {
	text        string
	quoted      bool
	quotedValue bool
}

func isQuoteRune(r rune) bool {
	return r == '"' || r == '\u201c' || r == '\u201d'
}

func tokenizeQuery(raw string) []rawToken {
	runes := []rune(raw)
	n := len(runes)
	var tokens []rawToken
	i := 0
	for i < n {
		if unicode.IsSpace(runes[i]) {
			i++
			continue
		}
		if isQuoteRune(runes[i]) {
			j := i + 1
			var buf strings.Builder
			for j < n && !isQuoteRune(runes[j]) {
				buf.WriteRune(runes[j])
				j++
			}
			if trimmed := strings.TrimSpace(buf.String()); trimmed != "" {
				tokens = append(tokens, rawToken{text: trimmed, quoted: true})
			}
			i = j + 1
			continue
		}
		var buf strings.Builder
		quotedValue := false
		for i < n && !unicode.IsSpace(runes[i]) {
			c := runes[i]
			if isQuoteRune(c) && strings.HasSuffix(buf.String(), ":") {
				j := i + 1
				for j < n && !isQuoteRune(runes[j]) {
					buf.WriteRune(runes[j])
					j++
				}
				quotedValue = true
				i = j + 1
				continue
			}
			if isQuoteRune(c) {
				break
			}
			buf.WriteRune(c)
			i++
		}
		if buf.Len() > 0 {
			tokens = append(tokens, rawToken{text: buf.String(), quoted: false, quotedValue: quotedValue})
		}
	}
	return splitParens(tokens)
}

func splitParens(tokens []rawToken) []rawToken {
	out := make([]rawToken, 0, len(tokens))
	for _, tok := range tokens {
		if tok.quoted || tok.quotedValue {
			out = append(out, tok)
			continue
		}
		text := tok.text
		for strings.HasPrefix(text, "(") {
			out = append(out, rawToken{text: "("})
			text = text[1:]
		}
		trailing := 0
		for strings.HasSuffix(text, ")") {
			body := text[:len(text)-1]
			depth := 0
			for _, c := range body {
				if c == '(' {
					depth++
				} else if c == ')' {
					depth--
				}
			}
			if depth > 0 {
				break
			}
			text = body
			trailing++
		}
		if text != "" {
			out = append(out, rawToken{text: text})
		}
		for range trailing {
			out = append(out, rawToken{text: ")"})
		}
	}
	return out
}

func isoDate(year, month, day int) string {
	if year < 1000 || year > 9999 || month < 1 || month > 12 || day < 1 || day > 31 {
		return ""
	}
	return fmt.Sprintf("%04d-%02d-%02d", year, month, day)
}

// ParseDateValue parses a before:/after: date value into ISO YYYY-MM-DD, or empty string if invalid.
// upstream: packages/coding-agent/src/web/search/query.ts:254-266
func ParseDateValue(value string) string {
	t := strings.TrimSpace(value)
	if m := isoDatePattern.FindStringSubmatch(t); m != nil {
		y, _ := strconv.Atoi(m[1])
		mo := 1
		if m[2] != "" {
			mo, _ = strconv.Atoi(m[2])
		}
		d := 1
		if m[3] != "" {
			d, _ = strconv.Atoi(m[3])
		}
		return isoDate(y, mo, d)
	}
	if m := mdyDatePattern.FindStringSubmatch(t); m != nil {
		mo, _ := strconv.Atoi(m[1])
		d, _ := strconv.Atoi(m[2])
		y, _ := strconv.Atoi(m[3])
		if mo > 12 && d <= 12 {
			mo, d = d, mo
		}
		return isoDate(y, mo, d)
	}
	return ""
}

func normalizeSite(value string) string {
	site := strings.ToLower(strings.TrimSpace(value))
	site = schemePrefixRe.ReplaceAllString(site, "")
	site = strings.TrimPrefix(site, "*.")
	site = strings.TrimRight(site, "/.")
	return site
}

var directiveFields = map[string]string{
	"site":     "site",
	"domain":   "site",
	"host":     "site",
	"inurl":    "inUrl",
	"url":      "inUrl",
	"intitle":  "inTitle",
	"title":    "inTitle",
	"intext":   "inText",
	"inbody":   "inText",
	"inanchor": "inText",
	"filetype": "filetype",
	"ext":      "filetype",
	"before":   "before",
	"until":    "before",
	"after":    "after",
	"since":    "after",
	"lang":     "lang",
	"language": "lang",
}

var allModes = map[string]string{
	"allintitle": "inTitle",
	"allinurl":   "inUrl",
	"allintext":  "inText",
}

func isReservedToken(text string) bool {
	switch text {
	case "(", ")", "OR", "AND", "NOT", "|", "||", "&&", "!":
		return true
	}
	m := directivePattern.FindStringSubmatch(text)
	if m == nil {
		return false
	}
	name := strings.ToLower(m[2])
	_, isField := directiveFields[name]
	_, isAll := allModes[name]
	return isField || isAll
}

// ParseSearchQuery parses a raw query string into a StructuredQuery.
// upstream: packages/coding-agent/src/web/search/query.ts:338-553
func ParseSearchQuery(raw string) StructuredQuery {
	q := StructuredQuery{Raw: raw}
	tokens := tokenizeQuery(raw)
	negateNext := false
	orPending := false
	lastWasTerm := false
	groupSeq := 0
	allMode := ""

	pushConstraint := func(field, value string, negated bool) {
		q.HasDirectives = true
		orPending = false
		lastWasTerm = false
		v := strings.TrimSpace(value)
		if v == "" {
			return
		}
		switch field {
		case "site":
			site := normalizeSite(v)
			if site != "" {
				if negated {
					q.ExcludedSites = append(q.ExcludedSites, site)
				} else {
					q.Sites = append(q.Sites, site)
				}
			}
		case "inUrl":
			if negated {
				q.ExcludedInURL = append(q.ExcludedInURL, v)
			} else {
				q.InURL = append(q.InURL, v)
			}
		case "inTitle":
			if negated {
				q.ExcludedInTitle = append(q.ExcludedInTitle, v)
			} else {
				q.InTitle = append(q.InTitle, v)
			}
		case "inText":
			if negated {
				q.ExcludedInText = append(q.ExcludedInText, v)
			} else {
				q.InText = append(q.InText, v)
			}
		case "filetype":
			ext := strings.TrimLeft(strings.ToLower(v), ".")
			if ext != "" {
				if negated {
					q.ExcludedFiletypes = append(q.ExcludedFiletypes, ext)
				} else {
					q.Filetypes = append(q.Filetypes, ext)
				}
			}
		}
	}

	pushTerm := func(text string, phrase bool) {
		negated := negateNext
		negateNext = false
		if allMode != "" {
			pushConstraint(allMode, text, negated)
			return
		}
		term := QueryTerm{Text: text, Phrase: phrase, Negated: negated}
		if orPending && lastWasTerm && len(q.Terms) > 0 {
			prev := &q.Terms[len(q.Terms)-1]
			if prev.Group == 0 {
				groupSeq++
				prev.Group = groupSeq
			}
			term.Group = prev.Group
		}
		orPending = false
		lastWasTerm = true
		q.Terms = append(q.Terms, term)
	}

	for idx := 0; idx < len(tokens); idx++ {
		tok := tokens[idx]
		if tok.quoted {
			pushTerm(tok.text, true)
			continue
		}
		if tok.text == "(" || tok.text == ")" {
			continue
		}
		if tok.text == "OR" || tok.text == "|" || tok.text == "||" {
			orPending = true
			q.HasDirectives = true
			continue
		}
		if tok.text == "AND" || tok.text == "&&" {
			q.HasDirectives = true
			continue
		}
		if tok.text == "NOT" || tok.text == "!" {
			negateNext = true
			q.HasDirectives = true
			continue
		}
		if tok.text == "-" || tok.text == "+" {
			if tok.text == "-" && idx+1 < len(tokens) && tokens[idx+1].quoted {
				negateNext = true
			}
			continue
		}

		match := directivePattern.FindStringSubmatch(tok.text)
		var name, allMatch, field string
		if match != nil {
			name = strings.ToLower(match[2])
			allMatch = allModes[name]
			field = directiveFields[name]
		}

		if match != nil && allMatch != "" {
			allMode = allMatch
			q.HasDirectives = true
			if inline := strings.TrimSpace(match[3]); inline != "" {
				pushConstraint(allMatch, inline, match[1] == "-")
			}
			orPending = false
			lastWasTerm = false
			continue
		}

		if match != nil && field != "" {
			value := strings.TrimSpace(match[3])
			if value == "" && idx+1 < len(tokens) {
				next := tokens[idx+1]
				if next.quoted || !isReservedToken(next.text) {
					value = strings.TrimSpace(next.text)
					idx++
				}
			}
			if value == "" {
				q.HasDirectives = true
				continue
			}
			negated := match[1] == "-" || negateNext
			negateNext = false
			switch field {
			case "before", "after":
				iso := ParseDateValue(value)
				if iso == "" {
					pushTerm(tok.text, false)
					continue
				}
				if field == "before" {
					q.Before = iso
				} else {
					q.After = iso
				}
				q.HasDirectives = true
				orPending = false
				lastWasTerm = false
			case "lang":
				q.Lang = strings.ToLower(value)
				q.HasDirectives = true
				orPending = false
				lastWasTerm = false
			default:
				pushConstraint(field, value, negated)
			}
			continue
		}

		text := tok.text
		if strings.HasPrefix(text, "-") && len(text) > 1 {
			negateNext = true
			q.HasDirectives = true
			text = strings.TrimLeft(text, "-")
			if text == "" {
				continue
			}
			if negMatch := directivePattern.FindStringSubmatch(text); negMatch != nil {
				negField := directiveFields[strings.ToLower(negMatch[2])]
				if negField != "" && negField != "before" && negField != "after" && negField != "lang" {
					negateNext = false
					pushConstraint(negField, strings.TrimSpace(negMatch[3]), true)
					continue
				}
			}
			pushTerm(text, false)
			continue
		}
		if strings.HasPrefix(text, "+") && len(text) > 1 {
			pushTerm(text[1:], true)
			q.HasDirectives = true
			continue
		}
		pushTerm(text, false)
	}

	q.Text = renderTerms(q.Terms, QuerySyntax{Phrases: true, Negation: true, Or: true})
	q.HasConstraints = len(q.Sites) > 0 ||
		len(q.ExcludedSites) > 0 ||
		len(q.InURL) > 0 ||
		len(q.ExcludedInURL) > 0 ||
		len(q.InTitle) > 0 ||
		len(q.ExcludedInTitle) > 0 ||
		len(q.Filetypes) > 0 ||
		len(q.ExcludedFiletypes) > 0 ||
		q.Before != "" ||
		q.After != ""
	return q
}

func quoteValue(v string) string {
	for _, r := range v {
		if unicode.IsSpace(r) {
			return `"` + v + `"`
		}
	}
	return v
}

func renderTerm(term QueryTerm, syntax QuerySyntax) string {
	if term.Negated && !syntax.Negation {
		return ""
	}
	body := term.Text
	if term.Phrase && syntax.Phrases {
		body = `"` + term.Text + `"`
	}
	if term.Negated {
		return "-" + body
	}
	return body
}

func renderTerms(terms []QueryTerm, syntax QuerySyntax) string {
	var parts []string
	for i := 0; i < len(terms); i++ {
		term := terms[i]
		if term.Group != 0 && syntax.Or {
			var members []string
			j := i
			for ; j < len(terms) && terms[j].Group == term.Group; j++ {
				if r := renderTerm(terms[j], syntax); r != "" {
					members = append(members, r)
				}
			}
			i = j - 1
			if len(members) > 1 {
				parts = append(parts, "("+strings.Join(members, " OR ")+")")
			} else if len(members) == 1 {
				parts = append(parts, members[0])
			}
			continue
		}
		if r := renderTerm(term, syntax); r != "" {
			parts = append(parts, r)
		}
	}
	return strings.Join(parts, " ")
}

// FormatQuery rebuilds a query string for an engine supporting the given syntax.
// upstream: packages/coding-agent/src/web/search/query.ts:598-638
func FormatQuery(q StructuredQuery, syntax QuerySyntax) string {
	var parts []string
	if text := renderTerms(q.Terms, syntax); text != "" {
		parts = append(parts, text)
	}
	if syntax.Site {
		if len(q.Sites) > 1 && syntax.Or {
			clauses := make([]string, len(q.Sites))
			for i, s := range q.Sites {
				clauses[i] = "site:" + s
			}
			parts = append(parts, "("+strings.Join(clauses, " OR ")+")")
		} else {
			for _, s := range q.Sites {
				parts = append(parts, "site:"+s)
			}
		}
		for _, s := range q.ExcludedSites {
			parts = append(parts, "-site:"+s)
		}
	}
	if syntax.InURL {
		for _, v := range q.InURL {
			parts = append(parts, "inurl:"+quoteValue(v))
		}
		for _, v := range q.ExcludedInURL {
			parts = append(parts, "-inurl:"+quoteValue(v))
		}
	}
	if syntax.InTitle {
		for _, v := range q.InTitle {
			parts = append(parts, "intitle:"+quoteValue(v))
		}
		for _, v := range q.ExcludedInTitle {
			parts = append(parts, "-intitle:"+quoteValue(v))
		}
	}
	if syntax.InText {
		for _, v := range q.InText {
			parts = append(parts, "intext:"+quoteValue(v))
		}
		for _, v := range q.ExcludedInText {
			parts = append(parts, "-intext:"+quoteValue(v))
		}
	}
	if syntax.Filetype {
		if len(q.Filetypes) > 1 && syntax.Or {
			clauses := make([]string, len(q.Filetypes))
			for i, f := range q.Filetypes {
				clauses[i] = "filetype:" + f
			}
			parts = append(parts, "("+strings.Join(clauses, " OR ")+")")
		} else {
			for _, f := range q.Filetypes {
				parts = append(parts, "filetype:"+f)
			}
		}
		for _, f := range q.ExcludedFiletypes {
			parts = append(parts, "-filetype:"+f)
		}
	}
	if syntax.DateRange {
		if q.After != "" {
			parts = append(parts, "after:"+q.After)
		}
		if q.Before != "" {
			parts = append(parts, "before:"+q.Before)
		}
	}

	result := strings.TrimSpace(strings.Join(parts, " "))
	if result == "" {
		var fallback []string
		fallback = append(fallback, q.Sites...)
		fallback = append(fallback, q.InTitle...)
		fallback = append(fallback, q.InURL...)
		fallback = append(fallback, q.InText...)
		fallback = append(fallback, q.Filetypes...)
		result = strings.TrimSpace(strings.Join(fallback, " "))
	}
	if result == "" {
		return strings.TrimSpace(q.Raw)
	}
	return result
}

// FormatScraperQuery canonicalizes directives and demotes path-carrying site: and inurl: to plain terms.
// upstream: packages/coding-agent/src/web/search/query.ts:660-675
func FormatScraperQuery(query string, parsed *StructuredQuery, syntax QuerySyntax) string {
	var p StructuredQuery
	if parsed != nil {
		p = *parsed
	} else {
		p = ParseSearchQuery(query)
	}
	if !p.HasDirectives {
		return query
	}
	var bareSites, demoted []string
	for _, s := range p.Sites {
		if strings.Contains(s, "/") {
			demoted = append(demoted, s)
		} else {
			bareSites = append(bareSites, s)
		}
	}
	demoted = append(demoted, p.InURL...)
	terms := make([]QueryTerm, len(p.Terms), len(p.Terms)+len(demoted))
	copy(terms, p.Terms)
	for _, d := range demoted {
		terms = append(terms, QueryTerm{Text: d})
	}
	downgraded := p
	downgraded.Sites = bareSites
	downgraded.InURL = nil
	downgraded.Terms = terms
	return FormatQuery(downgraded, syntax)
}

func hostAndPath(rawURL string) (host, path string, ok bool) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", "", false
	}
	p := u.Path
	if p == "" {
		p = "/"
	}
	return strings.ToLower(u.Hostname()), p, true
}

// MatchesSite reports whether rawURL matches a normalized site: directive (host, subdomain, optional path prefix).
// upstream: packages/coding-agent/src/web/search/query.ts:691-700
func MatchesSite(rawURL, site string) bool {
	host, path, ok := hostAndPath(rawURL)
	if !ok {
		return false
	}
	siteHost := site
	sitePath := ""
	if idx := strings.IndexByte(site, '/'); idx >= 0 {
		siteHost = site[:idx]
		sitePath = site[idx:]
	}
	if host != siteHost && !strings.HasSuffix(host, "."+siteHost) {
		return false
	}
	if sitePath != "" && !strings.HasPrefix(strings.ToLower(path), strings.ToLower(sitePath)) {
		return false
	}
	return true
}

func matchesFiletype(rawURL, ext string) bool {
	_, path, ok := hostAndPath(rawURL)
	if !ok {
		return false
	}
	return strings.HasSuffix(strings.ToLower(path), "."+ext)
}

var relativeUnitSeconds = map[string]int64{
	"m":      60,
	"min":    60,
	"minute": 60,
	"h":      3600,
	"hr":     3600,
	"hour":   3600,
	"d":      86400,
	"day":    86400,
	"w":      604800,
	"week":   604800,
	"mo":     2592000,
	"month":  2592000,
	"y":      31536000,
	"yr":     31536000,
	"year":   31536000,
}

func sourceTime(src SearchSource) (time.Time, bool) {
	if src.AgeSeconds != nil {
		return nowFunc().Add(-time.Duration(*src.AgeSeconds) * time.Second), true
	}
	if src.PublishedDate == "" {
		return time.Time{}, false
	}
	trimmed := strings.TrimSpace(src.PublishedDate)
	if m := relativeAgePattern.FindStringSubmatch(trimmed); m != nil {
		n, err := strconv.ParseInt(m[1], 10, 64)
		unit := relativeUnitSeconds[strings.ToLower(m[2])]
		if err == nil && n > 0 && unit > 0 {
			return nowFunc().Add(-time.Duration(n*unit) * time.Second), true
		}
	}
	return parseFlexibleTime(trimmed)
}

type constraintDimension struct {
	label string
	pred  func(SearchSource) bool
}

func constraintDimensions(q StructuredQuery) []constraintDimension {
	var dims []constraintDimension

	if len(q.Sites) > 0 {
		labels := make([]string, len(q.Sites))
		for i, s := range q.Sites {
			labels[i] = "site:" + s
		}
		sites := q.Sites
		dims = append(dims, constraintDimension{
			label: strings.Join(labels, " OR "),
			pred: func(src SearchSource) bool {
				for _, s := range sites {
					if MatchesSite(src.URL, s) {
						return true
					}
				}
				return false
			},
		})
	}
	if len(q.ExcludedSites) > 0 {
		labels := make([]string, len(q.ExcludedSites))
		for i, s := range q.ExcludedSites {
			labels[i] = "-site:" + s
		}
		excluded := q.ExcludedSites
		dims = append(dims, constraintDimension{
			label: strings.Join(labels, " "),
			pred: func(src SearchSource) bool {
				for _, s := range excluded {
					if MatchesSite(src.URL, s) {
						return false
					}
				}
				return true
			},
		})
	}
	if len(q.InURL) > 0 {
		labels := make([]string, len(q.InURL))
		for i, v := range q.InURL {
			labels[i] = "inurl:" + v
		}
		inURL := q.InURL
		dims = append(dims, constraintDimension{
			label: strings.Join(labels, " "),
			pred: func(src SearchSource) bool {
				lowerURL := strings.ToLower(src.URL)
				for _, v := range inURL {
					if !strings.Contains(lowerURL, strings.ToLower(v)) {
						return false
					}
				}
				return true
			},
		})
	}
	if len(q.ExcludedInURL) > 0 {
		labels := make([]string, len(q.ExcludedInURL))
		for i, v := range q.ExcludedInURL {
			labels[i] = "-inurl:" + v
		}
		exInURL := q.ExcludedInURL
		dims = append(dims, constraintDimension{
			label: strings.Join(labels, " "),
			pred: func(src SearchSource) bool {
				lowerURL := strings.ToLower(src.URL)
				for _, v := range exInURL {
					if strings.Contains(lowerURL, strings.ToLower(v)) {
						return false
					}
				}
				return true
			},
		})
	}
	if len(q.InTitle) > 0 {
		labels := make([]string, len(q.InTitle))
		for i, v := range q.InTitle {
			labels[i] = "intitle:" + v
		}
		inTitle := q.InTitle
		dims = append(dims, constraintDimension{
			label: strings.Join(labels, " "),
			pred: func(src SearchSource) bool {
				lowerTitle := strings.ToLower(src.Title)
				for _, v := range inTitle {
					if !strings.Contains(lowerTitle, strings.ToLower(v)) {
						return false
					}
				}
				return true
			},
		})
	}
	if len(q.ExcludedInTitle) > 0 {
		labels := make([]string, len(q.ExcludedInTitle))
		for i, v := range q.ExcludedInTitle {
			labels[i] = "-intitle:" + v
		}
		exInTitle := q.ExcludedInTitle
		dims = append(dims, constraintDimension{
			label: strings.Join(labels, " "),
			pred: func(src SearchSource) bool {
				lowerTitle := strings.ToLower(src.Title)
				for _, v := range exInTitle {
					if strings.Contains(lowerTitle, strings.ToLower(v)) {
						return false
					}
				}
				return true
			},
		})
	}
	if len(q.Filetypes) > 0 {
		labels := make([]string, len(q.Filetypes))
		for i, f := range q.Filetypes {
			labels[i] = "filetype:" + f
		}
		fts := q.Filetypes
		dims = append(dims, constraintDimension{
			label: strings.Join(labels, " OR "),
			pred: func(src SearchSource) bool {
				for _, ext := range fts {
					if matchesFiletype(src.URL, ext) {
						return true
					}
				}
				return false
			},
		})
	}
	if len(q.ExcludedFiletypes) > 0 {
		labels := make([]string, len(q.ExcludedFiletypes))
		for i, f := range q.ExcludedFiletypes {
			labels[i] = "-filetype:" + f
		}
		exFts := q.ExcludedFiletypes
		dims = append(dims, constraintDimension{
			label: strings.Join(labels, " "),
			pred: func(src SearchSource) bool {
				for _, ext := range exFts {
					if matchesFiletype(src.URL, ext) {
						return false
					}
				}
				return true
			},
		})
	}
	if q.After != "" || q.Before != "" {
		var afterTime, beforeTime time.Time
		hasAfter, hasBefore := false, false
		var labelParts []string
		if q.After != "" {
			if t, err := time.Parse("2006-01-02", q.After); err == nil {
				afterTime = t
				hasAfter = true
			}
			labelParts = append(labelParts, "after:"+q.After)
		}
		if q.Before != "" {
			if t, err := time.Parse("2006-01-02", q.Before); err == nil {
				beforeTime = t
				hasBefore = true
			}
			labelParts = append(labelParts, "before:"+q.Before)
		}
		dims = append(dims, constraintDimension{
			label: strings.Join(labelParts, " "),
			pred: func(src SearchSource) bool {
				t, ok := sourceTime(src)
				if !ok {
					return true
				}
				if hasAfter && t.Before(afterTime) {
					return false
				}
				if hasBefore && !t.Before(beforeTime) {
					return false
				}
				return true
			},
		})
	}
	return dims
}

// MatchesQueryConstraints checks whether a single source satisfies all constraints in q.
// upstream: packages/coding-agent/src/web/search/query.ts:750-755
func MatchesQueryConstraints(src SearchSource, q StructuredQuery) bool {
	for _, dim := range constraintDimensions(q) {
		if !dim.pred(src) {
			return false
		}
	}
	return true
}

// ApplyQueryConstraints leniently filters sources, relaxing any dimension that would drop all results.
// upstream: packages/coding-agent/src/web/search/query.ts:840-850
func ApplyQueryConstraints(sources []SearchSource, q StructuredQuery) ConstraintFilterResult {
	current := make([]SearchSource, len(sources))
	copy(current, sources)
	var dropped []string
	if len(current) == 0 {
		return ConstraintFilterResult{Sources: current}
	}
	for _, dim := range constraintDimensions(q) {
		var kept []SearchSource
		for _, src := range current {
			if dim.pred(src) {
				kept = append(kept, src)
			}
		}
		if len(kept) > 0 {
			current = kept
		} else {
			dropped = append(dropped, dim.label)
		}
	}
	return ConstraintFilterResult{Sources: current, Dropped: dropped}
}
