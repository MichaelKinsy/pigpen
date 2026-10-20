package websearch

import (
	"regexp"
	"strconv"
	"strings"
)

// Port of defuddle 0.19.3 dist/utils.js and dist/metadata.js (MetadataExtractor).

// dfdCountWords is utils.countWords: whitespace-separated words, each CJK character one word.
func dfdCountWords(text string) int {
	if text == "" {
		return 0
	}
	cjk, words := 0, 0
	inWord := false
	for _, r := range text {
		code := int(r)
		switch {
		case (code >= 0x3040 && code <= 0x309f) || (code >= 0x30a0 && code <= 0x30ff) || (code >= 0x3400 && code <= 0x4dbf) ||
			(code >= 0x4e00 && code <= 0x9fff) || (code >= 0xf900 && code <= 0xfaff) || (code >= 0xac00 && code <= 0xd7af):
			cjk++
			inWord = false
		case code <= 32:
			inWord = false
		case !inWord:
			words++
			inWord = true
		}
	}
	return cjk + words
}

// dfdNormalizeText is utils.normalizeText.
var (
	reNBSP        = jsRE(`\x{a0}`, "")
	reQuoteSingle = jsRE(`[\x{2018}\x{2019}\x{201a}\x{201b}]`, "")
	reDashes      = jsRE(`[\x{2012}\x{2013}\x{2014}\x{2015}]`, "")
	reQuoteDouble = jsRE(`[\x{201c}\x{201d}\x{201e}\x{201f}]`, "")
)

func dfdNormalizeText(text string) string {
	text = reNBSP.ReplaceAllString(text, " ")
	text = reQuoteSingle.ReplaceAllString(text, "'")
	text = reDashes.ReplaceAllString(text, "-")
	text = reQuoteDouble.ReplaceAllString(text, `"`)
	text = strings.ReplaceAll(text, "\u2026", "...")
	text = jsCollapseWS(text)
	return strings.ToLower(jsTrim(text))
}

type dfdMetaTag struct {
	name, property *string
	content        string
}

type dfdMetadata struct {
	title, description, domain, favicon, image, language, published, author, site string
	schemaOrgData                                                                 []any
}

var (
	reMetaBraces   = jsRE(`[{}]`, "")
	reMetaHash     = jsRE(`^#[a-zA-Z]`, "")
	reHasLetterNum = regexp.MustCompile(`[\p{L}\p{N}]`)
)

func dfdIsPlaceholderValue(s string) bool {
	if reMetaBraces.MatchString(s) || reMetaHash.MatchString(s) {
		return true
	}
	return !reHasLetterNum.MatchString(s)
}

func dfdFirstValid(thunks ...func() string) string {
	for _, t := range thunks {
		if v := t(); v != "" && !dfdIsPlaceholderValue(v) {
			return v
		}
	}
	return ""
}

func dfdGetMetaContents(metaTags []dfdMetaTag, attr, value string) []string {
	var out []string
	lv := strings.ToLower(value)
	for _, tag := range metaTags {
		a := tag.property
		if attr == "name" {
			a = tag.name
		}
		if a != nil && strings.ToLower(*a) == lv {
			out = append(out, jsTrim(tag.content))
		}
	}
	return out
}

func dfdGetMetaContent(metaTags []dfdMetaTag, attr, value string) string {
	if c := dfdGetMetaContents(metaTags, attr, value); len(c) > 0 {
		return c[0]
	}
	return ""
}

// dfdExtractMetadata is MetadataExtractor.extract. location is document.location.href.
func dfdExtractMetadata(doc *ldNode, schema []any, metaTags []dfdMetaTag, location string) dfdMetadata {
	domain := ""
	url := location
	if url == "" {
		url = firstNonEmpty(
			dfdGetMetaContent(metaTags, "property", "og:url"),
			dfdGetMetaContent(metaTags, "property", "twitter:url"),
			dfdSchemaProperty(schema, "url", ""),
			dfdSchemaProperty(schema, "mainEntityOfPage.url", ""),
			dfdSchemaProperty(schema, "mainEntity.url", ""),
			dfdSchemaProperty(schema, "WebSite.url", ""),
		)
		if url == "" {
			if l := ldQuerySelector(doc, `link[rel="canonical"]`); l != nil {
				url = l.attr("href")
			}
		}
	}
	if url != "" {
		if u, ok := newURL(url, ""); ok {
			domain = strings.TrimPrefix(u.hostname(), "www.")
			if strings.HasPrefix(u.hostname(), "www.") {
				domain = u.hostname()[4:]
			}
		}
	}
	siteName := dfdGetSiteName(schema, metaTags)
	title, detected := dfdCleanTitle(dfdGetBestTitle(doc, schema, metaTags, domain, siteName), siteName)
	author := dfdGetAuthor(doc, schema, metaTags)
	authorAsSite := ""
	if author != "" && !strings.Contains(author, ",") {
		authorAsSite = author
	}
	site := firstNonEmpty(siteName, detected, authorAsSite, domain)
	return dfdMetadata{
		title:         title,
		description:   dfdGetDescription(schema, metaTags),
		domain:        domain,
		favicon:       dfdGetFavicon(doc, url, metaTags),
		image:         dfdGetImage(schema, metaTags),
		language:      dfdGetLanguage(doc, schema, metaTags),
		published:     dfdGetPublished(doc, schema, metaTags, url),
		author:        author,
		site:          site,
		schemaOrgData: schema,
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

var (
	reByPrefix     = jsRE(`^by\s+`, "i")
	reAuthorURL    = jsRE(`\(?\s*https?://\S+\s*\)?`, "i")
	reAndSep       = jsRE(`,?\s+and\s+`, "i")
	reTrailSep     = jsRE(`\s*[-\x{2013}\x{2014}|]\s*$`, "")
	reCommaSpace   = jsRE(`(.*),\s(.*)`, "")
	reTrailComma   = jsRE(`,$`, "")
	reBylineMatch  = jsRE(`^By\s+([A-Z].+)$`, "i")
	reSpaceRuns    = jsRE(`\s+`, "")
)

func dfdCleanAuthorString(s string) string {
	s = reByPrefix.ReplaceAllString(s, "")
	s = reAuthorURL.ReplaceAllString(s, "")
	s = reAndSep.ReplaceAllString(s, ", ")
	s = reTrailSep.ReplaceAllString(s, "")
	return jsTrim(s)
}

func dfdUniq(in []string) []string {
	seen := make(map[string]bool, len(in))
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func dfdGetVisibleText(el *ldNode) string {
	clone := el.cloneNode(true)
	for _, s := range ldQuerySelectorAll(clone, "script, style, noscript") {
		s.remove()
	}
	return jsTrim(jsCollapseWS(clone.textContent()))
}

func dfdGetAuthorName(el *ldNode) string {
	clone := el.cloneNode(true)
	for _, s := range ldQuerySelectorAll(clone, "script, style, noscript") {
		s.remove()
	}
	text := jsTrim(jsCollapseWS(clone.textContent()))
	if text == "" {
		return ""
	}
	for _, child := range ldQuerySelectorAll(clone, "span, a, p") {
		childText := jsTrim(jsCollapseWS(child.textContent()))
		if l := jsLen(childText); l >= 2 && l <= 50 && childText != text {
			return childText
		}
	}
	if jsLen(text) <= 100 {
		return text
	}
	return ""
}

func dfdExtractByline(el *ldNode) (string, bool) {
	candidates := append([]*ldNode{el}, ldQuerySelectorAll(el, "p, span, address")...)
	for _, c := range candidates {
		text := reNBSP.ReplaceAllString(jsTrim(c.textContent()), " ")
		if l := jsLen(text); l > 0 && l < 50 {
			if m := reBylineMatch.FindStringSubmatch(text); m != nil {
				return jsTrim(m[1]), true
			}
		}
	}
	return "", false
}

func dfdGetAuthor(doc *ldNode, schema []any, metaTags []dfdMetaTag) string {
	authorsString := dfdFirstValid(
		func() string { return dfdGetMetaContent(metaTags, "name", "sailthru.author") },
		func() string { return dfdGetMetaContent(metaTags, "property", "article:author") },
		func() string { return dfdGetMetaContent(metaTags, "property", "author") },
		func() string { return dfdGetMetaContent(metaTags, "name", "author") },
		func() string { return dfdGetMetaContent(metaTags, "name", "byl") },
		func() string { return dfdGetMetaContent(metaTags, "name", "authorList") },
	)
	if authorsString != "" {
		if cleaned := dfdCleanAuthorString(authorsString); cleaned != "" {
			return cleaned
		}
	}
	filterPlaceholders := func(in []string) []string {
		var out []string
		for _, s := range in {
			if !dfdIsPlaceholderValue(s) {
				out = append(out, s)
			}
		}
		return out
	}
	authorsStrings := filterPlaceholders(dfdGetMetaContents(metaTags, "name", "citation_author"))
	if len(authorsStrings) == 0 {
		authorsStrings = filterPlaceholders(dfdGetMetaContents(metaTags, "property", "dc.creator"))
	}
	if len(authorsStrings) > 0 {
		parts := make([]string, len(authorsStrings))
		for i, s := range authorsStrings {
			if !strings.Contains(s, ",") {
				parts[i] = jsTrim(s)
				continue
			}
			if m := reCommaSpace.FindStringSubmatch(s); m != nil {
				parts[i] = m[2] + " " + m[1]
				continue
			}
			parts[i] = jsTrim(s)
		}
		return strings.Join(parts, ", ")
	}
	schemaAuthors := firstNonEmpty(dfdSchemaProperty(schema, "author.name", ""), dfdSchemaProperty(schema, "author.[].name", ""))
	if schemaAuthors != "" {
		var parts []string
		for _, part := range strings.Split(schemaAuthors, ",") {
			p := jsTrim(reTrailComma.ReplaceAllString(jsTrim(part), ""))
			if p != "" && !dfdIsPlaceholderValue(p) {
				parts = append(parts, p)
			}
		}
		if len(parts) > 0 {
			unique := dfdUniq(parts)
			if len(unique) > 10 {
				unique = unique[:10]
			}
			return strings.Join(unique, ", ")
		}
	}
	relAuthorEls := ldQuerySelectorAll(doc, `a[rel~="author"], address[rel~="author"]`)
	if len(relAuthorEls) > 0 && len(relAuthorEls) <= 3 {
		var relNames []string
		for _, el := range relAuthorEls {
			text := dfdGetVisibleText(el)
			lower := strings.ToLower(text)
			if text != "" && jsLen(text) < 100 && lower != "author" && lower != "authors" && !dfdIsPlaceholderValue(text) {
				relNames = append(relNames, text)
			}
		}
		if u := dfdUniq(relNames); len(u) > 0 {
			return strings.Join(u, ", ")
		}
	}
	var collected []string
	addDomAuthor := func(value string) {
		if value == "" {
			return
		}
		for _, namePart := range strings.Split(value, ",") {
			cleaned := jsTrim(reTrailComma.ReplaceAllString(jsTrim(jsCollapseWS(namePart)), ""))
			lower := strings.ToLower(cleaned)
			if cleaned != "" && lower != "author" && lower != "authors" && !dfdIsPlaceholderValue(cleaned) {
				collected = append(collected, cleaned)
			}
		}
	}
	for _, s := range []struct {
		selector   string
		maxMatches int
	}{{`[itemprop="author"]`, 0}, {`.author`, 3}, {`[href*="/author/"]`, 3}, {`.authors a`, 3}} {
		matches := ldQuerySelectorAll(doc, s.selector)
		if s.maxMatches > 0 && len(matches) > s.maxMatches {
			continue
		}
		for _, el := range matches {
			addDomAuthor(dfdGetAuthorName(el))
		}
	}
	if len(collected) > 0 {
		var trimmed []string
		for _, n := range collected {
			if t := jsTrim(n); t != "" {
				trimmed = append(trimmed, t)
			}
		}
		unique := dfdUniq(trimmed)
		if len(unique) > 1 {
			var filtered []string
			for _, a := range unique {
				super := false
				for _, b := range unique {
					if b != a && strings.Contains(a, b) {
						super = true
						break
					}
				}
				if !super {
					filtered = append(filtered, a)
				}
			}
			unique = filtered
		}
		if len(unique) > 0 {
			if len(unique) > 10 {
				unique = unique[:10]
			}
			return strings.Join(unique, ", ")
		}
	}
	if h1 := ldQuerySelector(doc, "h1"); h1 != nil {
		sibling := h1.nextElementSibling()
		for i := 0; i < 3 && sibling != nil; i++ {
			siblingText := jsTrim(sibling.textContent())
			childEls := ldQuerySelectorAll(sibling, "p, time")
			hasDateChild := false
			for _, el := range childEls {
				if dfdParseDateText(jsTrim(el.textContent())) != "" {
					hasDateChild = true
					break
				}
			}
			hasSiblingDate := dfdParseDateText(siblingText) != "" || hasDateChild
			if hasSiblingDate {
				links := ldQuerySelectorAll(sibling, "a")
				if len(links) == 1 {
					linkText := reNBSP.ReplaceAllString(jsTrim(links[0].textContent()), " ")
					if l := jsLen(linkText); l > 0 && l < 100 && dfdParseDateText(linkText) == "" {
						return linkText
					}
				}
				if hasDateChild && jsLen(siblingText) < 300 {
					for _, p := range childEls {
						if p.tagName() != "P" {
							continue
						}
						pText := reNBSP.ReplaceAllString(jsTrim(p.textContent()), " ")
						if l := jsLen(pText); l > 0 && l < 150 && dfdParseDateText(pText) == "" {
							return pText
						}
					}
				}
			}
			sibling = sibling.nextElementSibling()
		}
		scope := h1
		for depth := 0; depth < 3 && scope != nil; depth++ {
			cand := scope.previousElementSibling()
			for i := 0; i < 3 && cand != nil; i++ {
				if r, ok := dfdExtractByline(cand); ok {
					return r
				}
				cand = cand.previousElementSibling()
			}
			cand = scope.nextElementSibling()
			for i := 0; i < 3 && cand != nil; i++ {
				if r, ok := dfdExtractByline(cand); ok {
					return r
				}
				cand = cand.nextElementSibling()
			}
			scope = scope.parentElement()
		}
	}
	return ""
}

func dfdGetSiteName(schema []any, metaTags []dfdMetaTag) string {
	candidate := dfdFirstValid(
		func() string { return dfdSchemaProperty(schema, "publisher.name", "") },
		func() string { return dfdGetMetaContent(metaTags, "property", "og:site_name") },
		func() string { return dfdGetMetaContent(metaTags, "name", "og:site_name") },
		func() string { return dfdSchemaProperty(schema, "WebSite.name", "") },
		func() string { return dfdSchemaProperty(schema, "sourceOrganization.name", "") },
		func() string { return dfdGetMetaContent(metaTags, "name", "copyright") },
		func() string { return dfdSchemaProperty(schema, "copyrightHolder.name", "") },
		func() string { return dfdSchemaProperty(schema, "isPartOf.name", "") },
		func() string { return dfdGetMetaContent(metaTags, "name", "application-name") },
	)
	if candidate != "" && dfdCountWords(candidate) > 6 {
		return ""
	}
	return candidate
}

func dfdGetBestTitle(doc *ldNode, schema []any, metaTags []dfdMetaTag, domain, siteName string) string {
	var titleText, h1Text string
	if t := ldQuerySelector(doc, "title"); t != nil {
		titleText = jsTrim(t.textContent())
	}
	if h := ldQuerySelector(doc, "h1"); h != nil {
		h1Text = jsTrim(h.textContent())
	}
	var candidates []string
	for _, c := range []string{
		dfdGetMetaContent(metaTags, "property", "og:title"),
		dfdGetMetaContent(metaTags, "name", "twitter:title"),
		dfdSchemaProperty(schema, "headline", ""),
		dfdGetMetaContent(metaTags, "name", "title"),
		dfdGetMetaContent(metaTags, "name", "sailthru.title"),
		titleText,
		h1Text,
	} {
		if c != "" && !dfdIsPlaceholderValue(c) {
			candidates = append(candidates, c)
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	authorMeta := firstNonEmpty(dfdGetMetaContent(metaTags, "property", "author"), dfdGetMetaContent(metaTags, "name", "author"))
	authorNorm := strings.ToLower(jsTrim(authorMeta))
	siteNorm := strings.ToLower(jsTrim(siteName))
	domainNorm := ""
	if domain != "" {
		d := domain
		if i := strings.LastIndex(d, "."); i >= 0 && i < len(d)-1 && strings.Index(d[i:], ".") == 0 {
			d = d[:i]
		}
		domainNorm = reNonAlnum.ReplaceAllString(strings.ToLower(d), "")
	}
	for _, c := range candidates {
		if !dfdIsSiteIdentifier(c, authorNorm, siteNorm, domainNorm) {
			return c
		}
	}
	return candidates[0]
}

var reNonAlnum = jsRE(`[^a-z0-9]`, "")

func dfdIsSiteIdentifier(candidate, authorNorm, siteNorm, domainNorm string) bool {
	norm := strings.ToLower(jsTrim(candidate))
	if authorNorm != "" && norm == authorNorm {
		return true
	}
	if siteNorm != "" && norm == siteNorm {
		return true
	}
	if domainNorm != "" && reNonAlnum.ReplaceAllString(norm, "") == domainNorm {
		return true
	}
	return false
}

type dfdSepPos struct{ index, length int }

func dfdCleanTitle(title, siteName string) (string, string) {
	if title == "" {
		return title, ""
	}
	const separators = `[|\-\x{2013}\x{2014}/\x{b7}]`
	if siteName != "" && strings.ToLower(siteName) != strings.ToLower(title) && dfdCountWords(siteName) <= 6 {
		siteNameLower := strings.ToLower(siteName)
		esc := regexp.QuoteMeta(siteName)
		for _, pattern := range []string{`\s*` + separators + `\s*` + esc + `\s*$`, `^\s*` + esc + `\s*` + separators + `\s*`} {
			re := jsRE(pattern, "i")
			if re.MatchString(title) {
				return jsTrim(jsReplaceFirst(re, title, func([]string) string { return "" })), siteName
			}
		}
		allSep := jsRE(`\s+`+separators+`\s+`, "")
		var all []dfdSepPos
		for _, m := range allSep.FindAllStringIndex(title, -1) {
			all = append(all, dfdSepPos{m[0], m[1] - m[0]})
		}
		if len(all) > 0 {
			lastPos := all[len(all)-1]
			lastSegment := strings.ToLower(jsTrim(title[lastPos.index+lastPos.length:]))
			if lastSegment != "" && strings.Contains(siteNameLower, lastSegment) {
				cut := lastPos.index
				for i := len(all) - 2; i >= 0; i-- {
					pos := all[i]
					segment := jsTrim(title[pos.index+pos.length : cut])
					if dfdCountWords(segment) > 3 {
						break
					}
					cut = pos.index
				}
				return jsTrim(title[:cut]), siteName
			}
			firstPos := all[0]
			prefix := strings.ToLower(jsTrim(title[:firstPos.index]))
			if prefix != "" && strings.Contains(siteNameLower, prefix) {
				cut := firstPos.index + firstPos.length
				for i := 1; i < len(all); i++ {
					pos := all[i]
					segment := jsTrim(title[cut:pos.index])
					if dfdCountWords(segment) > 3 {
						break
					}
					cut = pos.index + pos.length
				}
				return jsTrim(title[cut:]), siteName
			}
		}
	}
	if t, s, ok := dfdTrySeparatorSplit(title, jsRE(`\s+([|/\x{b7}])\s+`, ""), false, func(tW, sW int) bool { return sW <= 3 && tW >= 2 && tW >= sW*2 }); ok {
		return t, s
	}
	if t, s, ok := dfdTrySeparatorSplit(title, jsRE(`\s+[\-\x{2013}\x{2014}]\s+`, ""), true, func(tW, sW int) bool { return sW <= 2 && tW >= 2 && tW > sW }); ok {
		return t, s
	}
	return jsTrim(title), ""
}

func dfdTrySeparatorSplit(title string, pattern *regexp.Regexp, suffixOnly bool, guard func(tW, sW int) bool) (string, string, bool) {
	var positions []dfdSepPos
	for _, m := range pattern.FindAllStringIndex(title, -1) {
		positions = append(positions, dfdSepPos{m[0], m[1] - m[0]})
	}
	if len(positions) == 0 {
		return "", "", false
	}
	last := positions[len(positions)-1]
	suffixTitle := jsTrim(title[:last.index])
	suffixSite := jsTrim(title[last.index+last.length:])
	if guard(dfdCountWords(suffixTitle), dfdCountWords(suffixSite)) {
		return suffixTitle, suffixSite, true
	}
	if !suffixOnly {
		first := positions[0]
		prefixSite := jsTrim(title[:first.index])
		prefixTitle := jsTrim(title[first.index+first.length:])
		if guard(dfdCountWords(prefixTitle), dfdCountWords(prefixSite)) {
			return prefixTitle, prefixSite, true
		}
	}
	return "", "", false
}

func dfdGetDescription(schema []any, metaTags []dfdMetaTag) string {
	return dfdFirstValid(
		func() string { return dfdGetMetaContent(metaTags, "name", "description") },
		func() string { return dfdGetMetaContent(metaTags, "property", "description") },
		func() string { return dfdGetMetaContent(metaTags, "property", "og:description") },
		func() string { return dfdSchemaProperty(schema, "description", "") },
		func() string { return dfdGetMetaContent(metaTags, "name", "twitter:description") },
		func() string { return dfdGetMetaContent(metaTags, "name", "sailthru.description") },
	)
}

func dfdGetImage(schema []any, metaTags []dfdMetaTag) string {
	return firstNonEmpty(
		dfdGetMetaContent(metaTags, "property", "og:image"),
		dfdGetMetaContent(metaTags, "name", "twitter:image"),
		dfdSchemaProperty(schema, "image.url", ""),
		dfdGetMetaContent(metaTags, "name", "sailthru.image.full"),
	)
}

func dfdNormalizeLangCode(code string) string { return strings.ReplaceAll(code, "_", "-") }

func dfdGetLanguage(doc *ldNode, schema []any, metaTags []dfdMetaTag) string {
	if de := doc.documentElement(); de != nil {
		if l, ok := de.getAttribute("lang"); ok {
			if t := jsTrim(l); t != "" {
				return dfdNormalizeLangCode(t)
			}
		}
	}
	if c := firstNonEmpty(dfdGetMetaContent(metaTags, "name", "content-language"), dfdGetMetaContent(metaTags, "property", "og:locale")); c != "" {
		return dfdNormalizeLangCode(c)
	}
	if m := ldQuerySelector(doc, `meta[http-equiv="Content-Language" i]`); m != nil {
		if c, ok := m.getAttribute("content"); ok {
			if t := jsTrim(c); t != "" {
				return dfdNormalizeLangCode(t)
			}
		}
	}
	if l := dfdSchemaProperty(schema, "inLanguage", ""); l != "" {
		return dfdNormalizeLangCode(l)
	}
	return ""
}

var reHTTPPrefix = jsRE(`^https?://`, "")

func dfdGetFavicon(doc *ldNode, baseURL string, metaTags []dfdMetaTag) string {
	if i := dfdGetMetaContent(metaTags, "property", "og:image:favicon"); i != "" {
		return i
	}
	if l := ldQuerySelector(doc, "link[rel='icon']"); l != nil {
		if h, ok := l.getAttribute("href"); ok && h != "" {
			return h
		}
	}
	if l := ldQuerySelector(doc, "link[rel='shortcut icon']"); l != nil {
		if h, ok := l.getAttribute("href"); ok && h != "" {
			return h
		}
	}
	if baseURL != "" && reHTTPPrefix.MatchString(baseURL) {
		if u, ok := newURL("/favicon.ico", baseURL); ok {
			return u.href()
		}
	}
	return ""
}

func dfdGetPublished(doc *ldNode, schema []any, metaTags []dfdMetaTag, url string) string {
	result := dfdFirstValid(
		func() string { return dfdSchemaProperty(schema, "datePublished", "") },
		func() string { return dfdGetMetaContent(metaTags, "name", "publishDate") },
		func() string { return dfdGetMetaContent(metaTags, "property", "article:published_time") },
		func() string {
			if a := ldQuerySelector(doc, `abbr[itemprop="datePublished"]`); a != nil {
				return jsTrim(a.attr("title"))
			}
			return ""
		},
		func() string { return dfdGetTimeElement(doc, url) },
		func() string { return dfdGetMetaContent(metaTags, "name", "sailthru.date") },
	)
	if result != "" {
		return result
	}
	if h1 := ldQuerySelector(doc, "h1"); h1 != nil {
		scan := func(start *ldNode, step func(*ldNode) *ldNode, childrenOnly bool) string {
			sibling := start
			for i := 0; i < 3 && sibling != nil; i++ {
				for _, child := range ldQuerySelectorAll(sibling, "p, time") {
					if parsed := dfdParseDateText(jsTrim(child.textContent())); parsed != "" {
						return parsed
					}
				}
				if !childrenOnly {
					if parsed := dfdParseDateText(jsTrim(sibling.textContent())); parsed != "" {
						return parsed
					}
				}
				sibling = step(sibling)
			}
			return ""
		}
		found := dfdFirstValid(
			func() string {
				return scan(h1.nextElementSibling(), func(e *ldNode) *ldNode { return e.nextElementSibling() }, false)
			},
			func() string {
				return scan(h1.previousElementSibling(), func(e *ldNode) *ldNode { return e.previousElementSibling() }, true)
			},
		)
		if found != "" {
			return found
		}
	}
	return ""
}

func dfdGetTimeElement(doc *ldNode, url string) string {
	for _, el := range ldQuerySelectorAll(doc, "time") {
		if dfdIsLinkedToOtherPage(el, url) {
			continue
		}
		content := jsTrim(el.attr("datetime"))
		if content == "" {
			content = jsTrim(el.textContent())
		}
		if content != "" {
			return content
		}
	}
	return ""
}

var reTrailSlashes = jsRE(`/+$`, "")

func dfdIsLinkedToOtherPage(el *ldNode, pageURL string) bool {
	if pageURL == "" {
		return false
	}
	anchor := ldClosest(el, "a[href]")
	if anchor == nil {
		return false
	}
	href := jsTrim(anchor.attr("href"))
	if href == "" || strings.HasPrefix(href, "#") {
		return false
	}
	target, ok := newURL(href, pageURL)
	if !ok {
		return false
	}
	current, ok := newURL(pageURL, "")
	if !ok {
		return false
	}
	if target.origin() != current.origin() {
		return false
	}
	return reTrailSlashes.ReplaceAllString(target.pathname(), "") != reTrailSlashes.ReplaceAllString(current.pathname(), "")
}

var dfdMonthMap = map[string]string{
	"january": "01", "february": "02", "march": "03", "april": "04", "may": "05", "june": "06", "july": "07",
	"august": "08", "september": "09", "october": "10", "november": "11", "december": "12",
}

const dfdMonths = `(January|February|March|April|May|June|July|August|September|October|November|December)`

var (
	reDateDMY = jsRE(`\b(\d{1,2})\s+`+dfdMonths+`\s+(\d{4})\b`, "i")
	reDateMDY = jsRE(`\b`+dfdMonths+`\s+(\d{1,2}),?\s+(\d{4})\b`, "i")
)

func dfdParseDateText(text string) string {
	if m := reDateDMY.FindStringSubmatch(text); m != nil {
		day := m[1]
		if len(day) < 2 {
			day = "0" + day
		}
		return m[3] + "-" + dfdMonthMap[strings.ToLower(m[2])] + "-" + day + "T00:00:00+00:00"
	}
	if m := reDateMDY.FindStringSubmatch(text); m != nil {
		day := m[2]
		if len(day) < 2 {
			day = "0" + day
		}
		return m[3] + "-" + dfdMonthMap[strings.ToLower(m[1])] + "-" + day + "T00:00:00+00:00"
	}
	return ""
}

// ---- schema.org lookups ----------------------------------------------------------------------------------------

var reIndexProp = jsRE(`^\[\d+\]$`, "")

// dfdSchemaProperty is MetadataExtractor.getSchemaProperty.
func dfdSchemaProperty(schema []any, property, def string) string {
	if schema == nil {
		return def
	}
	props := strings.Split(property, ".")
	results := dfdSearchSchema(schema, props, true)
	if len(results) == 0 {
		results = dfdSearchSchema(schema, props, false)
	}
	var unique []string
	seen := map[string]bool{}
	for _, r := range results {
		if r != "" && !seen[r] {
			seen[r] = true
			unique = append(unique, r)
		}
	}
	if len(unique) > 0 {
		return strings.Join(unique, ", ")
	}
	return def
}

func dfdSearchSchema(data any, props []string, exact bool) []string {
	switch d := data.(type) {
	case string:
		if len(props) == 0 {
			return []string{d}
		}
		return nil
	case []any:
		if len(props) > 0 && reIndexProp.MatchString(props[0]) {
			idx, _ := strconv.Atoi(props[0][1 : len(props[0])-1])
			if idx < len(d) && jsTruthy(d[idx]) {
				return dfdSearchSchema(d[idx], props[1:], exact)
			}
			return nil
		}
		if len(props) == 0 {
			all := true
			for _, item := range d {
				switch item.(type) {
				case string, float64:
				default:
					all = false
				}
			}
			if all {
				out := make([]string, len(d))
				for i, item := range d {
					if s, ok := item.(string); ok {
						out[i] = s
					} else {
						out[i] = jsNumberString(item.(float64))
					}
				}
				return out
			}
		}
		var out []string
		for _, item := range d {
			out = append(out, dfdSearchSchema(item, props, exact)...)
		}
		return out
	case *jsObj:
		if len(props) == 0 || props[0] == "" {
			if v, ok := d.get("name"); ok && jsTruthy(v) {
				return []string{jsToString(v)}
			}
			return nil
		}
		cur, rest := props[0], props[1:]
		if v, ok := d.get(cur); ok {
			return dfdSearchSchema(v, rest, true)
		}
		if !exact {
			var nested []string
			for _, k := range d.keys {
				v := d.m[k]
				switch v.(type) {
				case nil, *jsObj, []any:
					nested = append(nested, dfdSearchSchema(v, props, false)...)
				}
			}
			if len(nested) > 0 {
				return nested
			}
		}
		return nil
	}
	return nil
}

// jsToString is String(v) for a parsed JSON value that is a string or a number.
func jsToString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return jsNumberString(t)
	case bool:
		if t {
			return "true"
		}
		return "false"
	}
	return ""
}
