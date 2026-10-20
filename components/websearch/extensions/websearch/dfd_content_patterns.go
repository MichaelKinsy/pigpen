package websearch

import (
	"regexp"
	"strings"
)

// Port of defuddle 0.19.3 dist/content-boundary.js and dist/removals/content-patterns.js.

// dfdIndexOf is String.prototype.indexOf for a string and a needle, in UTF-16 units.
func dfdIndexOf(hay, needle string) int {
	i := strings.Index(hay, needle)
	if i < 0 {
		return -1
	}
	return jsLen(hay[:i])
}

// dfdSubstring0 is s.substring(0, n) in UTF-16 units.
func dfdSubstring0(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return jsSlice(s, 0, n)
}

// dfdCompareDocumentPosition is Node.compareDocumentPosition as linkedom computes it: from the number of
// ancestors, not from the document order, so a node that is shallower than the target "follows" it.
func dfdCompareDocumentPosition(this, target *ldNode) int {
	count := func(n *ldNode) int {
		c := 0
		for p := n.parent; p != nil; p = p.parent {
			c++
		}
		return c
	}
	result := 0
	if this != target {
		self, other := count(this), count(target)
		switch {
		case self < other:
			result += 4
			if this.contains(target) {
				result += 16
			}
		case other < self:
			result += 2
			if target.contains(this) {
				result += 8
			}
		case self != 0 && other != 0:
			idxThis, idxTarget := -1, -1
			for i, c := range this.parent.childNodes() {
				if c == this {
					idxThis = i
				}
				if c == target {
					idxTarget = i
				}
			}
			if idxThis < idxTarget {
				result += 4
			} else {
				result += 2
			}
		}
		if self == 0 || other == 0 {
			result += 32
			result += 1
		}
	}
	return result
}

// ---- content-boundary.js ---------------------------------------------------------------------------------------

var (
	reBoundaryDate    = jsRE(`(?:(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)[a-z]*\s+\d{1,2}|\d{1,2}(?:st|nd|rd|th)?\s+(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)[a-z]*|\d{4}[-/]\d{1,2}[-/]\d{1,2})`, "i")
	reBoundaryByline  = jsRE(`^by\s+\S`, "i")
	reSentencePunct   = jsRE(`[.!?]`, "")
	reHiddenClass     = jsRE(`\b(?:isHidden(?:-[A-Za-z0-9_]+)?|is-hidden)\b`, "")
)

const (
	dfdDialogSelector       = `[role="dialog"], [role="alertdialog"]`
	dfdSkipAncestorSelector = `aside, nav, header, footer, form, ` + dfdDialogSelector
)

func dfdFindTitleElement(mainContent *ldNode, title string) *ldNode {
	normalized := dfdNormalizeText(title)
	if normalized == "" {
		return nil
	}
	for _, h := range ldQuerySelectorAll(mainContent, "h1, h2") {
		if dfdNormalizeText(h.textContent()) == normalized {
			return h
		}
	}
	return nil
}

func dfdLinkTextLength(el *ldNode) int {
	n := 0
	for _, a := range ldQuerySelectorAll(el, "a") {
		n += jsLen(a.textContent())
	}
	return n
}

func dfdIsProseBlock(el *ldNode) bool {
	switch el.tagName() {
	case "P", "DIV", "SECTION", "ARTICLE", "BLOCKQUOTE", "FONT":
	default:
		return false
	}
	if ldClosest(el, dfdSkipAncestorSelector) != nil {
		return false
	}
	className := dfdClassName(el)
	if el.svg {
		className = ""
	}
	if reHiddenClass.MatchString(className) {
		return false
	}
	if ldQuerySelector(el, dfdDialogSelector) != nil {
		return false
	}
	if ldQuerySelector(el, "script, style") != nil {
		return false
	}
	text := jsTrim(el.textContent())
	if text == "" {
		return false
	}
	words := dfdCountWords(text)
	if words < 7 {
		return false
	}
	if !reSentencePunct.MatchString(text) {
		return false
	}
	if reBoundaryByline.MatchString(text) && words < 15 {
		return false
	}
	if reBoundaryDate.MatchString(text) && words < 20 {
		return false
	}
	if float64(dfdLinkTextLength(el)) > float64(jsLen(text))*0.7 {
		return false
	}
	if el.tagName() == "DIV" && ldQuerySelector(el, "p") == nil {
		return false
	}
	return true
}

// dfdFindContentStart is findContentStart. The original's tree walker is built with the list of all the elements
// below the root, and setting its currentNode does not move it, so the walk always starts at the first element
// and the title anchor only decides whether there is a retry.
func dfdFindContentStart(mainContent *ldNode, title string) *ldNode {
	start := dfdFindTitleElement(mainContent, title)
	var leafHit, containerHit *ldNode
	ldWalkAll(mainContent, func(n *ldNode) {
		if n.kind != ldElem || leafHit != nil {
			return
		}
		if dfdIsProseBlock(n) {
			switch n.tagName() {
			case "P", "BLOCKQUOTE", "FONT":
				leafHit = n
			default:
				if containerHit == nil {
					containerHit = n
				}
			}
		}
	})
	if leafHit != nil {
		return leafHit
	}
	if containerHit != nil {
		result := containerHit
		for {
			var qualifying *ldNode
			multiple := false
			for _, child := range result.children() {
				if dfdIsProseBlock(child) {
					if qualifying != nil {
						multiple = true
						break
					}
					qualifying = child
				}
			}
			if qualifying != nil && !multiple {
				result = qualifying
			} else {
				break
			}
		}
		return result
	}
	if start != nil {
		return dfdFindContentStart(mainContent, "")
	}
	return nil
}

func dfdIsAboveContentStart(el, boundary *ldNode) bool {
	if boundary == nil || el == boundary {
		return false
	}
	pos := dfdCompareDocumentPosition(el, boundary)
	if pos&1 != 0 {
		return false
	}
	return pos&4 != 0
}

// ---- content-patterns.js: the patterns -------------------------------------------------------------------------

var (
	reCPDate          = jsRE(`(?:(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)[a-z]*\s+\d{1,2}|\d{1,2}(?:st|nd|rd|th)?\s+(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)[a-z]*|\d{4}[-/]\d{1,2}[-/]\d{1,2})`, "i")
	reRelativeTime    = jsRE(`\b\d+\s+(?:second|minute|hour|day|week|month|year)s?\s+ago\b`, "i")
	reReadTime        = jsRE(`\d+\s*min(?:ute)?s?\s+read\b|(?:read(?:ing)?\s+time)\s*:?\s*\d+\s*min(?:ute)?s?\b`, "i")
	reBylineUpper     = regexp.MustCompile(`^\p{Lu}`)
	reStartsWithBy    = jsRE(`^(?:posted\s+)?by\s+\S`, "i")
	reMetadataLabel   = jsRE(`^(?:date|published|updated|posted|from|to|subject)\s*:`, "i")
	reNewsletter      = jsRE(`\bsubscribe\b[\s\S]{0,40}\bnewsletter\b|\bnewsletter\b[\s\S]{0,40}\bsubscribe\b|\bsign[- ]up\b[\s\S]{0,80}\b(?:newsletter|email alert)|\b(?:don[\u2019']?t (?:want to )?miss|never miss)\b[\s\S]{0,80}\b(?:latest|best|exclusive|reports?|updates?|source)`, "i")
	reSocialCounter   = jsRE(`^\d+\s+(?:Likes?|Comments?|Shares?|Retweets?|Reposts?|Restacks?)$`, "i")
	reTimezoneWidget  = jsRE(`^current time in$`, "i")
	rePinnedLabel     = jsRE(`^pinned$`, "i")
	reAuthorContact   = jsRE(`^(?:written by|(?:author|contact|reporter|correspondent)s?)$`, "i")
	reShareAuthor     = jsRE(`^(?:share|follow|authors?|written\s+by)$`, "i")
	reEmail           = jsRE(`[\w.-]+@[\w.-]+\.\w+`, "")
	rePhone           = jsRE(`\(?\d{3}\)?[\s.\x{2011}\x{2013}-]?\d{3}[\s.\x{2011}\x{2013}-]?\d{4}`, "")
	reHeadingTag2     = jsRE(`^H[1-6]$`, "")
	reCamel           = regexp.MustCompile(`([a-z])([A-Z])`)
	reCurlyQuotes     = jsRE(`[\u2018\u2019]`, "")
	reRelatedHeading  = jsRE(`^(?:related (?:posts?|articles?|content|stories|reads?|reading)|you (?:might|may|could) (?:also )?(?:like|enjoy|be interested in)|read (?:next|more|also)|further reading|see also|more (?:from .*|from|articles?|posts?|like this)|more to (?:read|explore)|explore more|about (?:the )?author|latest (?:news|events?|posts?|articles?|stories)(?:\s*[&+]\s*(?:news|events?|posts?|articles?|stories))?)$`, "i")
	reCTAHeading      = jsRE(`^(?:subscribe|sign up|follow us|share this|stay (?:updated|connected)|join (?:us|our)|search (?:the |our )?(?:site|blog|archives?|newsroom|website|catalog|store|shop|database))$`, "i")
	reRelatedIntro    = jsRE(`^for more (?:on|about)\b`, "i")
	reListenWidget    = jsRE(`\blisten\s+to\s+(?:this\s+)?(?:article|story|post|episode|podcast)\b`, "i")
	reBreadcrumbHref  = jsRE(`^/[a-zA-Z0-9_-]+/?$`, "")
	reTOCLabel        = jsRE(`^(?:table of )?contents$|^on this page$|^in this (?:article|guide|post)$`, "i")
	rePromoSentence   = jsRE(`[.!?]\s`, "")
	reSentenceEndEnd  = jsRE(`[.!?]$`, "")
	reMetaStripSplit  = jsRE(`[,\s/\-]+`, "")
	reParentIndex     = jsRE(`^index\.(html?|php)$`, "i")
	reLastPathSeg     = jsRE(`/[^/]*$`, "")
	reWWW             = jsRE(`^www\.`, "")

	dfdBoilerplatePatterns = []*regexp.Regexp{
		jsRE(`^This (?:article|story|piece) (?:appeared|was published|originally appeared) in\b`, "i"),
		jsRE(`^A version of this (?:article|story) (?:appeared|was published) in\b`, "i"),
		jsRE(`^Originally (?:published|appeared) (?:in|on|at)\b`, "i"),
		jsRE(`^Any re-?use permitted\b`, "i"),
		jsRE(`^\x{a9}\s*(?:Copyright\s+)?\d{4}`, "i"),
		jsRE(`^Comments?$`, "i"),
		jsRE(`^Leave a (?:comment|reply)$`, "i"),
		jsRE(`^Loading\.{3}$`, ""),
		jsRE(`^Affiliate links\b.*\b(?:earn|commission)`, "i"),
		jsRE(`\bRead our Comment Policy\b`, "i"),
		jsRE(`^Thank you for (?:being part of|joining) our community\b`, "i"),
	}

	metaStripBase = []*regexp.Regexp{
		jsRE(`\b(?:Jan(?:uary)?|Feb(?:ruary)?|Mar(?:ch)?|Apr(?:il)?|May|Jun(?:e)?|Jul(?:y)?|Aug(?:ust)?|Sep(?:t(?:ember)?)?|Oct(?:ober)?|Nov(?:ember)?|Dec(?:ember)?)\b`, "i"),
		jsRE(`\b(?:Mon(?:day)?|Tue(?:s(?:day)?)?|Wed(?:nesday)?|Thu(?:rs(?:day)?)?|Fri(?:day)?|Sat(?:urday)?|Sun(?:day)?)\b`, "i"),
		jsRE(`\b\d+(?:st|nd|rd|th)?\b`, ""),
		jsRE(`\d{4}[-/]\d{1,2}[-/]\d{1,2}`, ""),
	}
	readTimeStrip = append(append([]*regexp.Regexp{}, metaStripBase...),
		jsRE(`\bmin(?:ute)?s?\b`, "i"), jsRE(`\bread(?:ing)?\b`, "i"), jsRE(`\btime\b`, "i"), jsRE(`\bestimated\b`, "i"),
		jsRE(`[/|\x{b7}\x{2022}\x{2014}\x{2013}\-,:.\s]+`, ""))
	bylineStrip = append(append([]*regexp.Regexp{}, metaStripBase...),
		jsRE(`\bby\b`, "i"), jsRE(`[/|\x{b7}\x{2022}\x{2014}\x{2013}\-,]+`, ""))

	dfdContentElementNoImg = strings.Replace(dfdContentElementSelector, "img, picture, ", "", 1)
)

const dfdHeadingSelector = "h1, h2, h3, h4, h5, h6"

func dfdIsOrContainsHeading(el *ldNode) bool {
	return reHeadingTag2.MatchString(el.tagName()) || ldQuerySelector(el, dfdHeadingSelector) != nil
}

func dfdIsNewsletterElement(el *ldNode, maxWords int) bool {
	text := jsTrim(el.textContent())
	words := dfdCountWords(text)
	if words < 2 || words > maxWords {
		return false
	}
	if ldQuerySelector(el, dfdContentElementSelector) != nil {
		return false
	}
	normalized := reCamel.ReplaceAllString(text, "$1 $2")
	normalized = strings.NewReplacer("\u2018", "'", "\u2019", "'").Replace(normalized)
	return reNewsletter.MatchString(normalized)
}

func dfdHasFollowingProse(el *ldNode, minWords int) bool {
	for sibling := el.nextElementSibling(); sibling != nil; sibling = sibling.nextElementSibling() {
		if sibling.tagName() == "P" && dfdCountWords(sibling.textContent()) >= minWords {
			return true
		}
		for _, p := range ldQuerySelectorAll(sibling, "p") {
			if dfdCountWords(p.textContent()) >= minWords {
				return true
			}
		}
	}
	return false
}

func dfdPrecedingProseWords(el, mainContent *ldNode) int {
	words := 0
	for node := el; node != nil && node != mainContent; node = node.parentElement() {
		for sibling := node.previousElementSibling(); sibling != nil; sibling = sibling.previousElementSibling() {
			if sibling.tagName() == "P" {
				words += dfdCountWords(sibling.textContent())
			} else {
				for _, p := range ldQuerySelectorAll(sibling, "p") {
					words += dfdCountWords(p.textContent())
				}
			}
		}
	}
	return words
}

func dfdWalkUpToWrapper(el *ldNode, text string, mainContent *ldNode) *ldNode {
	target := el
	for target.parentElement() != nil && target.parentElement() != mainContent {
		if jsTrim(target.parentElement().textContent()) != text {
			break
		}
		target = target.parentElement()
	}
	return target
}

func dfdRemoveTrailingSiblings(element *ldNode, removeSelf bool) {
	sibling := element.nextElementSibling()
	for sibling != nil {
		next := sibling.nextElementSibling()
		if sibling.id() == "footnotes" {
			sibling = next
			continue
		}
		sibling.remove()
		sibling = next
	}
	if removeSelf {
		element.remove()
	}
}

func dfdRemoveTrailingWithCascade(target, mainContent *ldNode) {
	var ancestors []*ldNode
	for anc := target.parentElement(); anc != nil && anc != mainContent; anc = anc.parentElement() {
		ancestors = append(ancestors, anc)
	}
	dfdRemoveTrailingSiblings(target, true)
	for _, a := range ancestors {
		dfdRemoveTrailingSiblings(a, false)
	}
}

func dfdWalkUpIsolated(el, mainContent *ldNode) *ldNode {
	target := el
	for target.parentElement() != nil && target.parentElement() != mainContent {
		preceding := 0
		for sib := target.previousElementSibling(); sib != nil; sib = sib.previousElementSibling() {
			preceding += dfdCountWords(sib.textContent())
			if preceding > 10 {
				break
			}
		}
		if preceding > 10 {
			break
		}
		target = target.parentElement()
	}
	return target
}

func dfdRemoveThinPrecedingSection(target *ldNode) {
	prev := target.previousElementSibling()
	if prev == nil {
		return
	}
	if dfdCountWords(prev.textContent()) >= 50 {
		return
	}
	if ldQuerySelector(prev, dfdContentElementSelector) != nil {
		return
	}
	if beforePrev := prev.previousElementSibling(); beforePrev != nil && dfdIsOrContainsHeading(beforePrev) {
		return
	}
	prev.remove()
}

func dfdRemoveHeroHeader(mainContent, contentStart *ldNode) {
	times := ldQuerySelectorAll(mainContent, "time")
	if len(times) == 0 {
		return
	}
	for _, t := range times {
		if !dfdIsAboveContentStart(t, contentStart) {
			continue
		}
		var best *ldNode
		current := t.parentElement()
		for current != nil && current != mainContent {
			if ldQuerySelector(current, "h1, h2") != nil && ldQuerySelector(current, "time") != nil {
				totalWords := dfdCountWords(jsTrim(current.textContent()))
				var metadataEls []*ldNode
				for _, el := range ldQuerySelectorAll(current, "h1, h2, h3, time, [aria-label]") {
					dominated := false
					for _, existing := range metadataEls {
						if existing.contains(el) {
							dominated = true
							break
						}
					}
					if !dominated {
						metadataEls = append(metadataEls, el)
					}
				}
				metadataWords := 0
				for _, el := range metadataEls {
					metadataWords += dfdCountWords(el.textContent())
				}
				if totalWords-metadataWords < 30 {
					best = current
				} else {
					break
				}
			}
			current = current.parentElement()
		}
		if best != nil {
			best.remove()
			return
		}
	}
}

func dfdIsBreadcrumbList(list *ldNode) bool {
	items := ldQuerySelectorAll(list, "li")
	if len(items) < 2 || len(items) > 8 {
		return false
	}
	links := ldQuerySelectorAll(list, "a")
	if len(links) < 1 || len(links) >= len(items) {
		return false
	}
	if ldQuerySelector(list, "img, p, figure, blockquote") != nil {
		return false
	}
	for _, item := range items {
		if dfdCountWords(item.textContent()) > 8 {
			return false
		}
	}
	allInternal, hasBreadcrumbLink, shortLinkTexts := true, false, true
	for _, a := range links {
		href := a.attr("href")
		if strings.HasPrefix(href, "http") || strings.HasPrefix(href, "//") {
			allInternal = false
			break
		}
		if href == "/" || reBreadcrumbHref.MatchString(href) {
			hasBreadcrumbLink = true
		}
		n := 0
		for _, w := range jsSplitWS(jsTrim(a.textContent())) {
			if w != "" {
				n++
			}
		}
		if n > 5 {
			shortLinkTexts = false
		}
	}
	return allInternal && hasBreadcrumbLink && shortLinkTexts
}

func dfdRemoveEyebrowLabel(mainContent *ldNode) {
	firstHeading := ldQuerySelector(mainContent, "h1")
	if firstHeading == nil {
		firstHeading = ldQuerySelector(mainContent, "h2")
	}
	if firstHeading == nil {
		return
	}
	current := firstHeading
	for current.parentElement() != nil && current.parentElement() != mainContent && current.previousElementSibling() == nil {
		current = current.parentElement()
	}
	prev := current.previousElementSibling()
	if prev == nil {
		return
	}
	text := jsTrim(prev.textContent())
	words := dfdCountWords(text)
	if words < 1 || words > 6 {
		return
	}
	if jsLen(text) > 40 {
		return
	}
	if reSentencePunct.MatchString(text) {
		return
	}
	if reCPDate.MatchString(text) {
		return
	}
	if ldQuerySelector(prev, "img, picture, video, iframe, figure, table, pre, code, time, [datetime], h1, h2, h3, h4, h5, h6, ul, ol, blockquote") != nil {
		return
	}
	prev.remove()
}

func dfdStripAll(res []*regexp.Regexp, s string) string {
	for _, re := range res {
		s = re.ReplaceAllString(s, "")
	}
	return s
}

func dfdParsedPath(link *ldNode, url string) (*wurl, bool) {
	return newURL(link.attr("href"), url)
}

func dfdRemoveByContentPattern(mainContent *ldNode, url, title, description string) {
	contentStart := dfdFindContentStart(mainContent, title)
	isPreContent := func(el *ldNode) bool { return dfdIsAboveContentStart(el, contentStart) }
	normalizedTitle := dfdNormalizeText(title)
	normalizedDesc := dfdNormalizeText(description)

	if firstList := ldQuerySelector(mainContent, "ul, ol"); firstList != nil && dfdIsBreadcrumbList(firstList) {
		target := firstList
		for target.parentElement() != nil && target.parentElement() != mainContent && len(target.parentElement().children()) == 1 {
			target = target.parentElement()
		}
		target.remove()
	}

	if firstH1 := ldQuerySelector(mainContent, "h1"); firstH1 != nil {
		for _, link := range ldQuerySelectorAll(mainContent, "a[href]") {
			if link.parent == nil {
				continue
			}
			if dfdCompareDocumentPosition(link, firstH1)&4 == 0 {
				continue
			}
			if ldQuerySelector(link, "div") == nil {
				continue
			}
			if ldQuerySelector(link, "img, picture, video") != nil {
				continue
			}
			text := jsTrim(link.textContent())
			if dfdCountWords(text) > 25 {
				continue
			}
			if rePromoSentence.MatchString(text) {
				continue
			}
			link.remove()
		}
	}

	dfdRemoveHeroHeader(mainContent, contentStart)

	for _, media := range ldQuerySelectorAll(mainContent, "audio, video") {
		if media.parent == nil {
			continue
		}
		if media.attr("src") == "" && ldQuerySelector(media, "source") == nil {
			continue
		}
		container := media
		for container.parentElement() != nil && container.parentElement() != mainContent {
			if dfdCountWords(jsTrim(container.parentElement().textContent())) > 25 {
				break
			}
			container = container.parentElement()
		}
		containerText := jsTrim(container.textContent())
		isListen := reListenWidget.MatchString(containerText)
		isPreContentPlayer := !isListen && isPreContent(container) && dfdCountWords(containerText) <= 25
		if isListen || isPreContentPlayer {
			container.remove()
		}
	}

	contentText := mainContent.textContent()
	contentLen := jsLen(contentText)
	parsedPageURL, parsedOK := newURL(url, "")

	for _, list := range ldQuerySelectorAll(mainContent, "ul, ol") {
		if list.parent == nil {
			continue
		}
		if ldClosest(list, "#footnotes") != nil {
			continue
		}
		listText := jsTrim(list.textContent())
		listPos := dfdIndexOf(contentText, dfdSubstring0(listText, 60))
		if listPos < 0 || float64(listPos) > float64(contentLen)*0.3 {
			continue
		}
		links := ldQuerySelectorAll(list, "a[href]")
		if len(links) < 3 {
			continue
		}
		if ldQuerySelector(list, dfdContentElementSelector) != nil {
			continue
		}
		anchorCount := 0
		for _, link := range links {
			href := link.attr("href")
			if strings.HasPrefix(href, "#") {
				anchorCount++
			} else if parsedOK && strings.Contains(href, "#") {
				if resolved, ok := newURL(href, url); ok {
					if resolved.pathname() == parsedPageURL.pathname() && resolved.hostname() == parsedPageURL.hostname() {
						anchorCount++
					}
				}
			}
		}
		if anchorCount < 3 || float64(anchorCount)/float64(len(links)) < 0.8 {
			continue
		}
		target := list
		for target.parentElement() != nil && target.parentElement() != mainContent && len(target.parentElement().children()) == 1 {
			target = target.parentElement()
		}
		if prevEl := target.previousElementSibling(); prevEl != nil && reHeadingTag2.MatchString(prevEl.tagName()) {
			if reTOCLabel.MatchString(jsTrim(prevEl.textContent())) {
				prevEl.remove()
			}
		}
		prevSib := target.previousElementSibling()
		nextSib := target.nextElementSibling()
		target.remove()
		if prevSib != nil && prevSib.tagName() == "HR" {
			prevSib.remove()
		}
		if nextSib != nil && nextSib.tagName() == "HR" {
			nextSib.remove()
		}
		break
	}

	candidates := ldQuerySelectorAll(mainContent, "p, span, div, time")
	bylineFound, authorDateFound := false, false
	for _, el := range candidates {
		if el.parent == nil {
			continue
		}
		text := jsTrim(el.textContent())
		words := dfdCountWords(text)
		if words > 15 || words == 0 {
			continue
		}
		if ldClosest(el, "pre, code") != nil {
			continue
		}
		tag := el.tagName()
		hasDate := reCPDate.MatchString(text)
		pos := -2
		getPos := func() int {
			if pos == -2 {
				pos = dfdIndexOf(contentText, text)
			}
			return pos
		}
		if reTimezoneWidget.MatchString(text) && getPos() <= 300 {
			target := el
			if target.parentElement() != nil && target.parentElement() != mainContent {
				target = target.parentElement()
			}
			target.remove()
			continue
		}
		if words == 1 && rePinnedLabel.MatchString(text) {
			el.remove()
			continue
		}
		for _, pair := range [2]string{normalizedTitle, normalizedDesc} {
			if pair != "" && words >= 3 && isPreContent(el) && dfdNormalizeText(text) == pair {
				el.remove()
				break
			}
		}
		if el.parent == nil {
			continue
		}
		if (tag == "DIV" || tag == "P") && words >= 1 && words <= 10 && (hasDate || reRelativeTime.MatchString(text)) && !reMetadataLabel.MatchString(text) && !reSentencePunct.MatchString(text) && isPreContent(el) {
			big := false
			for _, b := range ldQuerySelectorAll(el, "p, h1, h2, h3, h4, h5, h6") {
				if dfdCountWords(b.textContent()) > 8 {
					big = true
					break
				}
			}
			if !big {
				el.remove()
				continue
			}
		}
		if tag == "DIV" && words >= 1 && words <= 5 && !reSentencePunct.MatchString(text) && isPreContent(el) && ldQuerySelector(el, "img") != nil {
			links := ldQuerySelectorAll(el, "a[href]")
			if len(links) > 0 {
				linkTextLen := 0
				for _, l := range links {
					linkTextLen += jsLen(jsTrim(l.textContent()))
				}
				tl := jsLen(text)
				if tl == 0 {
					tl = 1
				}
				if float64(linkTextLen)/float64(tl) >= 0.8 {
					el.remove()
					continue
				}
			}
		}
		if !bylineFound && reStartsWithBy.MatchString(text) && words >= 2 && !reSentenceEndEnd.MatchString(text) && isPreContent(el) {
			target := dfdWalkUpToWrapper(el, text, mainContent)
			target.remove()
			bylineFound = true
			continue
		}
		if reReadTime.MatchString(text) {
			var cond bool
			if hasDate {
				cond = len(ldQuerySelectorAll(el, "p, div, section, article")) == 0
			} else {
				cond = words <= 5 && isPreContent(el)
			}
			if cond {
				cleaned := dfdStripAll(readTimeStrip, text)
				if jsLen(jsTrim(cleaned)) == 0 {
					target := el
					if !hasDate {
						target = dfdWalkUpToWrapper(el, text, mainContent)
					}
					target.remove()
					continue
				}
			}
		}
		if !authorDateFound && words >= 2 && words <= 10 && hasDate && !reMetadataLabel.MatchString(text) && isPreContent(el) {
			residual := jsTrim(dfdStripAll(bylineStrip, text))
			if residual != "" {
				var nameWords []string
				for _, w := range jsSplitWS(residual) {
					if w != "" {
						nameWords = append(nameWords, w)
					}
				}
				ok := len(nameWords) >= 1 && len(nameWords) <= 4
				for _, w := range nameWords {
					if !reBylineUpper.MatchString(w) {
						ok = false
					}
				}
				if ok {
					target := dfdWalkUpToWrapper(el, text, mainContent)
					target.remove()
					authorDateFound = true
					continue
				}
			}
		}
		if hasDate && words <= 5 && isPreContent(el) {
			residual := dfdStripAll(metaStripBase, text)
			residual = jsTrim(reMetaStripSplit.ReplaceAllString(residual, ""))
			if len(residual) == 0 {
				target := dfdWalkUpToWrapper(el, text, mainContent)
				target.remove()
				continue
			}
		}
	}

	for _, t := range ldQuerySelectorAll(mainContent, "time") {
		if t.parent == nil {
			continue
		}
		target := t
		targetText := jsTrim(target.textContent())
		for target.parentElement() != nil && target.parentElement() != mainContent {
			parentTag := strings.ToLower(target.parentElement().tagName())
			parentText := jsTrim(target.parentElement().textContent())
			if parentTag == "p" && parentText == targetText {
				target = target.parentElement()
				break
			}
			switch parentTag {
			case "i", "em", "span", "b", "strong", "small":
				if parentText == targetText {
					target = target.parentElement()
					targetText = parentText
					continue
				}
			}
			break
		}
		text := jsTrim(target.textContent())
		if dfdCountWords(text) > 10 {
			continue
		}
		pos := dfdIndexOf(contentText, text)
		distFromEnd := contentLen - (pos + jsLen(text))
		if pos > 200 && distFromEnd > 200 {
			continue
		}
		target.remove()
	}

	for _, list := range ldQuerySelectorAll(mainContent, "ul, ol, dl") {
		if list.parent == nil {
			continue
		}
		if ldClosest(list, "#footnotes") != nil {
			continue
		}
		isDl := list.tagName() == "DL"
		var items []*ldNode
		for _, c := range list.children() {
			if (isDl && c.tagName() == "DD") || (!isDl && c.tagName() == "LI") {
				items = append(items, c)
			}
		}
		minItems := 2
		if isDl {
			minItems = 1
		}
		if len(items) < minItems || len(items) > 8 {
			continue
		}
		listText := jsTrim(list.textContent())
		listPos := dfdIndexOf(contentText, listText)
		distFromEnd := contentLen - (listPos + jsLen(listText))
		if listPos > 500 && distFromEnd > 500 {
			continue
		}
		if prev := list.previousElementSibling(); prev != nil {
			if dfdIsOrContainsHeading(prev) {
				continue
			}
			if strings.HasSuffix(jsTrim(prev.textContent()), ":") {
				continue
			}
		}
		isMetadata := true
		for _, item := range items {
			text := jsTrim(item.textContent())
			if dfdCountWords(text) > 8 || reSentenceEndEnd.MatchString(text) {
				isMetadata = false
				break
			}
		}
		if !isMetadata {
			continue
		}
		if dfdCountWords(listText) > 30 {
			continue
		}
		dfdWalkUpToWrapper(list, listText, mainContent).remove()
	}

	urlPath, pageHost := "", ""
	if parsedOK {
		urlPath = parsedPageURL.pathname()
		pageHost = reWWW.ReplaceAllString(parsedPageURL.hostname(), "")
	}
	if urlPath != "" {
		firstHeading := ldQuerySelector(mainContent, "h1, h2, h3")
		for _, el := range ldQuerySelectorAll(mainContent, "div, span, p, a[href]") {
			if el.parent == nil {
				continue
			}
			text := jsTrim(el.textContent())
			if dfdCountWords(text) > 10 {
				continue
			}
			if len(ldQuerySelectorAll(el, "p, div, section, article")) > 0 {
				continue
			}
			isLink := ldMatches(el, "a[href]")
			if isLink && el.parentElement() != nil && el.parentElement() != mainContent {
				parentText := jsTrim(el.parentElement().textContent())
				if parentText != text {
					if ldClosest(el, "p") != nil {
						continue
					}
					if firstHeading == nil {
						continue
					}
					if dfdCompareDocumentPosition(el, firstHeading)&4 == 0 {
						continue
					}
				}
			}
			var link *ldNode
			if isLink {
				link = el
			} else {
				link = ldQuerySelector(el, "a[href]")
			}
			if link == nil {
				continue
			}
			if u, ok := newURL(link.attr("href"), url); ok {
				linkPath := u.pathname()
				linkDir := reLastPathSeg.ReplaceAllString(linkPath, "/")
				segs := strings.Split(linkPath, "/")
				last := segs[len(segs)-1]
				isParentIndex := reParentIndex.MatchString(last) && strings.HasPrefix(urlPath, linkDir)
				if linkPath != "/" && linkPath != urlPath && (strings.HasPrefix(urlPath, linkPath) || isParentIndex) {
					el.remove()
				}
			}
		}
	}

	if pageHost != "" {
		for _, heading := range ldQuerySelectorAll(mainContent, "h2, h3, h4, h5, h6") {
			if heading.parent == nil {
				continue
			}
			list := heading.nextElementSibling()
			if list == nil || (list.tagName() != "UL" && list.tagName() != "OL") {
				continue
			}
			var items []*ldNode
			for _, c := range list.children() {
				if c.tagName() == "LI" {
					items = append(items, c)
				}
			}
			if len(items) < 2 {
				continue
			}
			trailing := false
			for check := list; check != nil && check != mainContent; check = check.parentElement() {
				for sib := check.nextElementSibling(); sib != nil; sib = sib.nextElementSibling() {
					if len(jsTrim(sib.textContent())) > 0 {
						trailing = true
						break
					}
				}
				if trailing {
					break
				}
			}
			if trailing {
				continue
			}
			allExternal := true
			for _, item := range items {
				links := ldQuerySelectorAll(item, "a[href]")
				if len(links) == 0 {
					allExternal = false
					break
				}
				itemText := jsTrim(item.textContent())
				linkTextLen := 0
				for _, l := range links {
					linkTextLen += jsLen(jsTrim(l.textContent()))
					if u, ok := newURL(l.attr("href"), url); ok {
						if reWWW.ReplaceAllString(u.hostname(), "") == pageHost {
							allExternal = false
							break
						}
					}
				}
				if !allExternal {
					break
				}
				if float64(linkTextLen) < float64(jsLen(itemText))*0.6 {
					allExternal = false
					break
				}
			}
			if !allExternal {
				continue
			}
			list.remove()
			heading.remove()
		}
	}

	lastChild := mainContent.lastElementChild()
	for lastChild != nil && (lastChild.tagName() == "HR" || lastChild.tagName() == "BR") {
		lastChild = lastChild.previousElementSibling()
	}
	if lastChild != nil && (lastChild.tagName() == "SECTION" || lastChild.tagName() == "DIV" || lastChild.tagName() == "ASIDE") {
		var paras []*ldNode
		hasNonPara := false
		for _, child := range lastChild.children() {
			if jsTrim(child.textContent()) == "" {
				continue
			}
			if child.tagName() == "P" {
				paras = append(paras, child)
			} else if child.tagName() != "BR" {
				hasNonPara = true
				break
			}
		}
		if len(paras) >= 2 && !hasNonPara {
			allLinkDense := true
			for _, p := range paras {
				text := jsCollapseWS(jsTrim(p.textContent()))
				links := ldQuerySelectorAll(p, "a[href]")
				if len(links) == 0 {
					allLinkDense = false
					break
				}
				linkTextLen := 0
				for _, l := range links {
					linkTextLen += jsLen(jsTrim(l.textContent()))
				}
				tl := jsLen(text)
				if tl == 0 {
					tl = 1
				}
				if float64(linkTextLen)/float64(tl) <= 0.6 {
					allLinkDense = false
					break
				}
				nonLink := text
				for _, l := range links {
					nonLink = strings.ReplaceAll(nonLink, jsTrim(l.textContent()), "")
				}
				if reSentencePunct.MatchString(nonLink) {
					allLinkDense = false
					break
				}
			}
			if allLinkDense {
				lastChild.remove()
			}
		}
	}

	totalWords := dfdCountWords(mainContent.textContent())
	if totalWords > 300 {
		var trailingEls []*ldNode
		trailingWords := 0
		child := mainContent.lastElementChild()
		for child != nil {
			if child.id() == "footnotes" {
				child = child.previousElementSibling()
				continue
			}
			if child.tagName() == "HR" {
				trailingEls = append(trailingEls, child)
				break
			}
			svgWords := 0
			for _, svg := range ldQuerySelectorAll(child, "svg") {
				svgWords += dfdCountWords(svg.textContent())
			}
			words := dfdCountWords(jsTrim(child.textContent())) - svgWords
			if words > 25 {
				break
			}
			trailingWords += words
			trailingEls = append(trailingEls, child)
			child = child.previousElementSibling()
		}
		if len(trailingEls) >= 1 && float64(trailingWords) < float64(totalWords)*0.15 {
			hasHeading, hasContent := false, false
			for _, el := range trailingEls {
				if dfdIsOrContainsHeading(el) {
					hasHeading = true
				}
				if ldQuerySelector(el, dfdContentElementSelector) != nil {
					hasContent = true
				}
			}
			proseParagraphs := 0
			for _, el := range trailingEls {
				if el.tagName() == "P" && dfdCountWords(el.textContent()) > 5 {
					proseParagraphs++
				}
			}
			if hasHeading && !hasContent && proseParagraphs < 2 {
				for _, el := range trailingEls {
					el.remove()
				}
			}
		}
	}

	fullText := mainContent.textContent()
	for _, el := range ldQuerySelectorAll(mainContent, "p, div, span, section") {
		if el.parent == nil {
			continue
		}
		if ldClosest(el, "pre, code") != nil {
			continue
		}
		text := jsTrim(el.textContent())
		words := dfdCountWords(text)
		if words > 50 || words < 1 {
			continue
		}
		for _, pattern := range dfdBoilerplatePatterns {
			if pattern.MatchString(text) {
				target := el
				for target.parentElement() != nil && target.parentElement() != mainContent {
					if target.nextElementSibling() != nil {
						break
					}
					target = target.parentElement()
				}
				targetPos := dfdIndexOf(fullText, target.textContent())
				if targetPos < 200 {
					if target != el && el.nextElementSibling() == nil {
						el.remove()
					}
					continue
				}
				dfdRemoveTrailingWithCascade(target, mainContent)
				break
			}
		}
	}

	for _, heading := range ldQuerySelectorAll(mainContent, "h2, h3, h4, h5, h6") {
		if heading.parent == nil {
			continue
		}
		headingText := jsTrim(heading.textContent())
		isCta := reCTAHeading.MatchString(headingText)
		if !isCta && !reRelatedHeading.MatchString(headingText) {
			continue
		}
		if dfdIndexOf(contentText, headingText) < 500 {
			continue
		}
		target := dfdWalkUpIsolated(heading, mainContent)
		if dfdHasFollowingProse(target, 25) {
			if target == heading {
				continue
			}
			target.remove()
			continue
		}
		if target == heading {
			if !isCta {
				continue
			}
			dfdRemoveTrailingSiblings(heading, true)
		} else {
			dfdRemoveThinPrecedingSection(target)
			dfdRemoveTrailingWithCascade(target, mainContent)
		}
		break
	}

	for _, el := range ldQuerySelectorAll(mainContent, "p") {
		if el.parent == nil {
			continue
		}
		text := jsTrim(el.textContent())
		if !reRelatedIntro.MatchString(text) {
			continue
		}
		if dfdCountWords(text) > 20 {
			continue
		}
		if ldQuerySelector(el, dfdContentElementSelector) != nil {
			continue
		}
		el.remove()
	}

	contentWordCount := dfdCountWords(contentText)
	for _, el := range ldQuerySelectorAll(mainContent, "div, ul, ol") {
		if el.parent == nil {
			continue
		}
		if len(el.children()) < 2 {
			continue
		}
		if len(ldQuerySelectorAll(el, "img, picture")) < 2 {
			continue
		}
		children := el.children()
		cardCount := 0
		for _, c := range children {
			if ldQuerySelector(c, "img, picture") != nil && (ldQuerySelector(c, "h2, h3, h4") != nil || ldQuerySelector(c, "a[href]") != nil) {
				cardCount++
			}
		}
		if cardCount < 2 || float64(cardCount) < float64(len(children))*0.7 {
			continue
		}
		firstText := dfdSubstring0(jsTrim(children[0].textContent()), 30)
		var followsContent bool
		if jsLen(firstText) >= 5 {
			followsContent = dfdIndexOf(contentText, firstText) >= 500
		} else {
			all := true
			for _, c := range children {
				if ldQuerySelector(c, "a[href]") == nil {
					all = false
					break
				}
			}
			followsContent = all && dfdPrecedingProseWords(el, mainContent) >= 100
		}
		if !followsContent {
			continue
		}
		gridWords := dfdCountWords(el.textContent())
		if contentWordCount > 0 && float64(gridWords)/float64(contentWordCount) > 0.3 {
			continue
		}
		target := dfdWalkUpIsolated(el, mainContent)
		if target == el {
			continue
		}
		if dfdCountWords(target.textContent()) > gridWords*2+15 {
			continue
		}
		if dfdHasFollowingProse(target, 25) {
			continue
		}
		dfdRemoveThinPrecedingSection(target)
		dfdRemoveTrailingSiblings(target, true)
		break
	}

	for _, el := range ldQuerySelectorAll(mainContent, "div, section, aside") {
		if el.parent == nil {
			continue
		}
		if ldClosest(el, "pre, code") != nil {
			continue
		}
		if !dfdIsNewsletterElement(el, 60) {
			continue
		}
		elWords := dfdCountWords(jsTrim(el.textContent()))
		target := el
		for target.parentElement() != nil && target.parentElement() != mainContent {
			if dfdCountWords(jsTrim(target.parentElement().textContent())) > elWords*2+15 {
				break
			}
			target = target.parentElement()
		}
		target.remove()
		break
	}

	for _, el := range ldQuerySelectorAll(mainContent, "ul") {
		if el.parent == nil {
			continue
		}
		if !dfdIsNewsletterElement(el, 30) {
			continue
		}
		el.remove()
		break
	}

	for _, el := range ldQuerySelectorAll(mainContent, "div, section") {
		if el.parent == nil {
			continue
		}
		text := jsTrim(el.textContent())
		words := dfdCountWords(text)
		if words < 2 || words > 40 {
			continue
		}
		pos := dfdIndexOf(contentText, dfdSubstring0(text, 60))
		if pos < 0 {
			continue
		}
		if contentLen-(pos+jsLen(text)) > 300 {
			continue
		}
		hasLabel := false
		for _, child := range ldQuerySelectorAll(el, "div, span, p, dt, dd, li") {
			if reAuthorContact.MatchString(jsTrim(child.textContent())) {
				hasLabel = true
				break
			}
		}
		if !hasLabel {
			continue
		}
		if !(reEmail.MatchString(text) || rePhone.MatchString(text) || ldQuerySelector(el, `a[href^="mailto:"]`) != nil) {
			continue
		}
		dfdWalkUpIsolated(el, mainContent).remove()
		break
	}

	for _, el := range ldQuerySelectorAll(mainContent, "p, span, div") {
		if el.parent == nil {
			continue
		}
		if !reShareAuthor.MatchString(jsTrim(el.textContent())) {
			continue
		}
		container := el
		for container.parentElement() != nil && container.parentElement() != mainContent {
			parent := container.parentElement()
			if dfdCountWords(jsTrim(parent.textContent())) > 15 {
				break
			}
			container = parent
		}
		if ldQuerySelector(container, dfdContentElementNoImg) != nil {
			continue
		}
		container.remove()
	}

	for _, el := range ldQuerySelectorAll(mainContent, "a, p, div, span") {
		if el.parent == nil {
			continue
		}
		text := jsTrim(el.textContent())
		if !reSocialCounter.MatchString(text) {
			continue
		}
		if el.tagName() == "A" && el.attr("href") != "" {
			continue
		}
		if el.tagName() != "A" {
			pos := dfdIndexOf(contentText, text)
			if contentLen-(pos+jsLen(text)) > 200 {
				continue
			}
		}
		dfdWalkUpToWrapper(el, text, mainContent).remove()
	}

	for _, el := range ldQuerySelectorAll(mainContent, "div") {
		if el.parent == nil {
			continue
		}
		text := jsTrim(el.textContent())
		words := dfdCountWords(text)
		if words < 1 || words > 10 {
			continue
		}
		if reSentencePunct.MatchString(text) {
			continue
		}
		if ldQuerySelector(el, dfdContentElementSelector) != nil {
			continue
		}
		pos := dfdIndexOf(contentText, text)
		if pos < 0 {
			continue
		}
		if contentLen-(pos+jsLen(text)) > 300 {
			continue
		}
		links := ldQuerySelectorAll(el, "a[href]")
		if len(links) == 0 {
			continue
		}
		linkTextLen := 0
		for _, l := range links {
			linkTextLen += jsLen(jsTrim(l.textContent()))
		}
		tl := jsLen(text)
		if tl == 0 {
			tl = 1
		}
		if float64(linkTextLen)/float64(tl) < 0.8 {
			continue
		}
		el.remove()
	}
}
