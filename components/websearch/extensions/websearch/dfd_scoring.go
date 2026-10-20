package websearch

import (
	"strings"
	"sync"
)

// Port of defuddle 0.19.3 dist/removals/scoring.js (ContentScorer).

var dfdContentIndicators = []string{"admonition", "article", "content", "entry", "image", "img", "font", "figure", "figcaption", "pre", "main", "post", "story", "table"}

var dfdNavigationIndicators = []string{
	"advertisement", "all rights reserved", "banner", "cookie", "comments", "copyright", "follow me", "follow us", "footer", "header",
	"homepage", "login", "menu", "more articles", "more like this", "most read", "nav", "navigation", "newsletter", "popular",
	"privacy", "recommended", "register", "related", "responses", "share", "sidebar", "sign in", "sign up", "signup", "social",
	"sponsored", "subscribe", "terms", "trending",
}

var dfdNonContentPatterns = []string{
	"advert", "ad-", "ads", "banner", "cookie", "copyright", "footer", "header", "homepage", "menu", "nav", "newsletter", "popular",
	"privacy", "recommended", "related", "rights", "share", "sidebar", "social", "sponsored", "subscribe", "terms", "trending", "widget",
}

var (
	reSocialSimple   = jsRE(`\b(?:linkedin\.com/(?:in|company)/|instagram\.com/\w|threads\.net/\w|mastodon\.\w)`, "i")
	reSocialLookNext = jsRE(`\b(twitter\.com/|x\.com/|facebook\.com/)`, "i")
	reScoreDate      = jsRE(`(?:(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)[a-z]*\s+\d{1,2}|\d{1,2}(?:st|nd|rd|th)?\s+(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)[a-z]*)`, "i")
	reByline         = jsRE(`\bBy\s+[A-Z]`, "")
	reTocHeading     = jsRE(`^(?:table of )?contents$|^on this page$|^in this (?:article|guide|post)$`, "i")
	reContentDate    = jsRE(`\b(?:(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)[a-z]*\s+\d{1,2},?\s+\d{4}|\d{1,2}(?:st|nd|rd|th)?\s+(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)[a-z]*,?\s+\d{4})\b`, "i")
	reContentAuthor  = jsRE(`\b(?:by|written by|author:)\s+[A-Za-z\s]+\b`, "i")
	reSentenceEnd    = mustRE(`[.?!]`)
)

// dfdNavREs are the navigation heading alternation and the indicator expressions, built on first use.
var dfdNavREs = sync.OnceValues(func() (heading *lazyRE, indicators []*lazyRE) {
	var alts []string
	for _, i := range dfdNavigationIndicators {
		alts = append(alts, strings.ReplaceAll(i, " ", `\s+`))
		indicators = append(indicators, jsRE(`\b`+strings.ReplaceAll(i, " ", `\s+`)+`\b`, ""))
	}
	return jsRE(strings.Join(alts, "|"), "i"), indicators
})

func dfdIsWordByte(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// dfdSocialProfile is socialProfilePattern.test(href) for a lower-cased href: twitter.com/ and x.com/ must not be
// followed by "intent" as a word, facebook.com/ not by "share", and all three by a word character.
func dfdSocialProfile(href string) bool {
	if reSocialSimple.MatchString(href) {
		return true
	}
	for _, m := range reSocialLookNext.FindAllStringSubmatchIndex(href, -1) {
		host := strings.ToLower(href[m[2]:m[3]])
		rest := href[m[1]:]
		if rest == "" || !dfdIsWordByte(rest[0]) {
			continue
		}
		banned := "intent"
		if host == "facebook.com/" {
			banned = "share"
		}
		if strings.HasPrefix(strings.ToLower(rest), banned) && (len(rest) == len(banned) || !dfdIsWordByte(rest[len(banned)])) {
			continue
		}
		return true
	}
	return false
}

func dfdCountCommas(text string) int { return strings.Count(text, ",") }

func dfdScoreElement(element *ldNode) float64 {
	score := 0.0
	text := element.textContent()
	words := dfdCountWords(text)
	score += float64(words)
	score += float64(len(element.getElementsByTagName("p")) * 10)
	score += float64(dfdCountCommas(text))
	images := len(element.getElementsByTagName("img"))
	w := float64(words)
	if words == 0 {
		w = 1
	}
	score -= float64(images) / w * 3
	style := element.attr("style")
	if strings.Contains(style, "float: right") || strings.Contains(style, "text-align: right") || element.attr("align") == "right" {
		score += 5
	}
	if reContentDate.MatchString(text) {
		score += 10
	}
	if reContentAuthor.MatchString(text) {
		score += 10
	}
	className := strings.ToLower(dfdClassName(element))
	if strings.Contains(className, "content") || strings.Contains(className, "article") || strings.Contains(className, "post") {
		score += 15
	}
	if ldQuerySelector(element, dfdFootnoteInlineReferences) != nil {
		score += 10
	}
	if ldQuerySelector(element, dfdFootnoteListSelectors) != nil {
		score += 10
	}
	score -= float64(len(element.getElementsByTagName("table")) * 5)
	if element.name == "td" {
		if parentTable := ldClosest(element, "table"); parentTable != nil {
			tableWidth := jsParseInt(orZero(parentTable.attr("width")))
			tableAlign := parentTable.attr("align")
			tableClass := strings.ToLower(dfdClassName(parentTable))
			isTableLayout := tableWidth > 400 || tableAlign == "center" || strings.Contains(tableClass, "content") || strings.Contains(tableClass, "article")
			if isTableLayout {
				allCells := parentTable.getElementsByTagName("td")
				cellIndex := -1
				for i, c := range allCells {
					if c == element {
						cellIndex = i
						break
					}
				}
				if cellIndex > 0 && cellIndex < len(allCells)-1 {
					score += 10
				}
			}
		}
	}
	linkTextLength := 0
	for _, a := range element.getElementsByTagName("a") {
		linkTextLength += jsLen(a.textContent())
	}
	textLength := jsLen(text)
	if textLength == 0 {
		textLength = 1
	}
	linkDensity := float64(linkTextLength) / float64(textLength)
	if linkDensity > 0.5 {
		linkDensity = 0.5
	}
	score *= 1 - linkDensity
	return score
}

func dfdFindBestElement(elements []*ldNode, minScore float64) *ldNode {
	var best *ldNode
	bestScore := 0.0
	for _, el := range elements {
		if s := dfdScoreElement(el); s > bestScore {
			bestScore = s
			best = el
		}
	}
	if bestScore > minScore {
		return best
	}
	return nil
}

func dfdScoreAndRemove(doc *ldNode, mainContent *ldNode) {
	toRemove := newDfdOrdered()
	for _, element := range ldQuerySelectorAll(doc, dfdBlockElementsSelector) {
		if toRemove.set[element] {
			continue
		}
		if mainContent != nil && element.contains(mainContent) {
			continue
		}
		if ldClosest(element, "pre") != nil {
			continue
		}
		if ldClosest(element, "[data-defuddle]") != nil {
			continue
		}
		if ldClosest(element, "td, th") != nil {
			continue
		}
		if dfdIsLikelyContent(element) {
			continue
		}
		if dfdScoreNonContentBlock(element) < 0 {
			toRemove.add(element)
		}
	}
	for _, el := range toRemove.order {
		el.remove()
	}
}

func dfdIsLikelyContent(element *ldNode) bool {
	if role, ok := element.getAttribute("role"); ok && (role == "article" || role == "main" || role == "contentinfo") {
		return true
	}
	className := strings.ToLower(dfdClassName(element))
	id := strings.ToLower(element.id())
	for _, ind := range dfdContentIndicators {
		if strings.Contains(className, ind) || strings.Contains(id, ind) {
			return true
		}
	}
	if ldQuerySelector(element, "pre, table, figure, picture") != nil {
		return true
	}
	text := element.textContent()
	words := dfdCountWords(text)
	if headingChild := ldQuerySelector(element, "h1, h2, h3, h4, h5, h6"); headingChild != nil {
		headingText := jsTrim(headingChild.textContent())
		if headingText != "" && headingText == jsTrim(text) {
			headingLower := strings.ToLower(headingText)
			if navHeading, _ := dfdNavREs(); !navHeading.MatchString(headingLower) && !reTocHeading.MatchString(headingLower) {
				return true
			}
		}
	}
	if words < 1000 {
		hasNavigationHeading := false
		navHeading, _ := dfdNavREs()
		for _, h := range ldQuerySelectorAll(element, "h1, h2, h3, h4, h5, h6") {
			if navHeading.MatchString(jsTrim(strings.ToLower(h.textContent()))) {
				hasNavigationHeading = true
				break
			}
		}
		if hasNavigationHeading {
			if words < 200 {
				return false
			}
			linkCount := len(element.getElementsByTagName("a"))
			w := float64(words)
			if words == 0 {
				w = 1
			}
			if float64(linkCount)/w > 0.2 {
				return false
			}
		}
	}
	if dfdIsCardGrid(element, words) {
		return false
	}
	if words < 80 {
		for _, a := range element.getElementsByTagName("a") {
			if dfdSocialProfile(strings.ToLower(a.attr("href"))) {
				return false
			}
		}
	}
	paragraphs := len(element.getElementsByTagName("p"))
	listItems := len(element.getElementsByTagName("li"))
	contentBlocks := paragraphs + listItems
	if words > 50 && contentBlocks > 1 {
		return true
	}
	if words > 100 {
		return true
	}
	if words > 30 && contentBlocks > 0 {
		return true
	}
	if words >= 10 && reSentenceEnd.MatchString(text) {
		linkCount := len(element.getElementsByTagName("a"))
		if float64(linkCount)/float64(words) < 0.1 {
			return true
		}
	}
	return false
}

func dfdScoreNonContentBlock(element *ldNode) float64 {
	skip := false
	_ = ldTry(func() {
		if ldMatches(element, dfdFootnoteListSelectors) || ldQuerySelector(element, dfdFootnoteListSelectors) != nil || ldClosest(element, dfdFootnoteListSelectors) != nil {
			skip = true
		}
	})
	if skip {
		return 0
	}
	score := 0.0
	text := element.textContent()
	words := dfdCountWords(text)
	if words < 3 {
		return 0
	}
	score += float64(dfdCountCommas(text))
	textLower := strings.ToLower(text)
	matches := 0
	_, indicators := dfdNavREs()
	for _, re := range indicators {
		if re.MatchString(textLower) {
			matches++
		}
	}
	score -= float64(matches * 10)
	linkElements := element.getElementsByTagName("a")
	links := len(linkElements)
	if float64(links)/float64(words) > 0.5 {
		score -= 15
	}
	if links > 1 && words < 80 {
		linkTextLength := 0
		for _, a := range linkElements {
			linkTextLength += jsLen(a.textContent())
		}
		total := jsLen(text)
		if total > 0 && float64(linkTextLength)/float64(total) > 0.8 {
			score -= 15
		}
	}
	lists := len(element.getElementsByTagName("ul")) + len(element.getElementsByTagName("ol"))
	if lists > 0 && links > lists*3 {
		score -= 10
	}
	if words < 80 {
		for _, a := range linkElements {
			if dfdSocialProfile(strings.ToLower(a.attr("href"))) {
				score -= 15
				break
			}
		}
	}
	if words < 15 && reByline.MatchString(text) && reScoreDate.MatchString(text) {
		score -= 10
	}
	if dfdIsCardGrid(element, words) {
		score -= 15
	}
	className := strings.ToLower(dfdClassName(element))
	id := strings.ToLower(element.id())
	for _, p := range dfdNonContentPatterns {
		if strings.Contains(className, p) || strings.Contains(id, p) {
			score -= 8
		}
	}
	return score
}

func dfdIsCardGrid(element *ldNode, words int) bool {
	if words < 3 || words >= 500 {
		return false
	}
	headings := ldQuerySelectorAll(element, "h2, h3, h4")
	if len(headings) < 3 {
		return false
	}
	if len(ldQuerySelectorAll(element, "img")) < 2 {
		return false
	}
	headingWordCount := 0
	for _, h := range headings {
		headingWordCount += dfdCountWords(h.textContent())
	}
	return float64(words-headingWordCount)/float64(len(headings)) < 20
}
