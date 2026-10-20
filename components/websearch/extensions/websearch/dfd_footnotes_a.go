package websearch

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Port of defuddle 0.19.3 dist/elements/footnotes.js, part 1: the handler, the collection of footnote definitions
// and the fallbacks that find them (the standardization of the inline references follows in dfd_footnotes_b.go).

var (
	reFootnoteSection = jsRE(`^(foot\s*notes?|end\s*notes?|notes?|references?)$`, "i")
	reBackrefSymbols  = jsRE(`^[\^\x{21A9}\x{21A5}\x{2191}\x{21B5}\x{2934}\x{2935}\x{23CE}]+$`, "")
	reCiteRef         = regexp.MustCompile(`^#cite_ref-`)
	reFootnoteMarker  = jsRE(`^\[?\(?(\d{1,4})\)?\]?$`, "")
	reFootnoteDefNum  = regexp.MustCompile(`^footnote-(\d+)$`)
	reLeadingDot      = jsRE(`^\s*\.\s*`, "")
	reNumDotTail      = jsRE(`^\d+[.)]*\s*$`, "")
	reLeadingNumDot   = jsRE(`^\d+\.\s*`, "")
	reLeadingSpace    = jsRE(`^\s+`, "")
	reWordFtnRef      = regexp.MustCompile(`^_ftnref(\d+)$`)
	reGDocID          = regexp.MustCompile(`^ftnt(\d+)$`)
	reHeadingLower    = regexp.MustCompile(`^h[1-6]$`)
)

func dfdHrefFragment(anchor *ldNode) string {
	if anchor == nil {
		return ""
	}
	href := anchor.attr("href")
	parts := strings.Split(href, "#")
	return strings.ToLower(parts[len(parts)-1])
}

type dfdInlineRefExtractor struct {
	selector string
	extract  func(el *ldNode) string
}

func dfdMatchLast(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

var dfdInlineRefExtractors = []dfdInlineRefExtractor{
	{"sup.footnoteref", func(el *ldNode) string {
		link := ldQuerySelector(el, `a[id^="footnoteref-"]`)
		if link == nil {
			return ""
		}
		return dfdMatchLast(regexp.MustCompile(`^footnoteref-(\d+)$`), link.id())
	}},
	{`a[id^="ref-link"]`, func(el *ldNode) string { return jsTrim(el.textContent()) }},
	{`a[role="doc-biblioref"]`, func(el *ldNode) string {
		if rid := el.attr("data-xml-rid"); rid != "" {
			return rid
		}
		href := el.attr("href")
		if strings.HasPrefix(href, "#core-R") {
			return strings.Replace(href, "#core-", "", 1)
		}
		return ""
	}},
	{"a.footnote-anchor, span.footnote-hovercard-target a", func(el *ldNode) string {
		return strings.ToLower(strings.Replace(el.id(), "footnote-anchor-", "", 1))
	}},
	{"sup.reference", func(el *ldNode) string {
		id := ""
		for _, link := range ldQuerySelectorAll(el, "a") {
			href := link.attr("href")
			segs := strings.Split(href, "/")
			if m := regexp.MustCompile(`(?:cite_note|cite_ref)-(.+)`).FindStringSubmatch(segs[len(segs)-1]); m != nil {
				id = strings.ToLower(m[1])
			}
		}
		return id
	}},
	{`sup[id^="fnref:"], span[id^="fnref:"]`, func(el *ldNode) string { return strings.ToLower(strings.Replace(el.id(), "fnref:", "", 1)) }},
	{`sup[id^="fnr"]`, func(el *ldNode) string { return strings.ToLower(strings.Replace(el.id(), "fnr", "", 1)) }},
	{"sup.footnote-reference", func(el *ldNode) string { return dfdHrefFragment(ldQuerySelector(el, `a[href^="#"]`)) }},
	{"span.footnote-reference", func(el *ldNode) string {
		if id := el.attr("data-footnote-id"); id != "" {
			return id
		}
		if strings.HasPrefix(el.id(), "fnref") {
			return strings.ToLower(strings.Replace(el.id(), "fnref", "", 1))
		}
		return ""
	}},
	{"span.footnote-link", func(el *ldNode) string { return el.attr("data-footnote-id") }},
	{"a.citation", func(el *ldNode) string { return jsTrim(el.textContent()) }},
	{`a[id^="fnref"]`, func(el *ldNode) string { return strings.ToLower(strings.Replace(el.id(), "fnref", "", 1)) }},
	{`a[data-type="noteref"]`, func(el *ldNode) string { return dfdHrefFragment(el) }},
}

type dfdFootnote struct {
	content    any // string or *ldNode
	originalID string
	refs       []string
}

type dfdFootnoteState struct {
	footnotes    map[int]*dfdFootnote
	processedIDs map[string]bool
	count        int
}

type dfdFootnoteHandler struct {
	pendingRemovals []*ldNode
}

func dfdMakeRefID(footnoteNumber, refsLength int) string {
	if refsLength > 0 {
		return "fnref:" + strconv.Itoa(footnoteNumber) + "-" + strconv.Itoa(refsLength+1)
	}
	return "fnref:" + strconv.Itoa(footnoteNumber)
}

func dfdMergeFootnotes(target, source map[int]*dfdFootnote) {
	for n, d := range source {
		if target[n] == nil {
			target[n] = d
		}
	}
}

// addFootnote records a footnote if the id is non-empty and unseen.
func (h *dfdFootnoteHandler) addFootnote(state *dfdFootnoteState, id string, content any, explicitNum *int) bool {
	if id == "" || state.processedIDs[id] {
		return false
	}
	key := state.count
	if explicitNum != nil {
		key = *explicitNum
	}
	state.footnotes[key] = &dfdFootnote{content: content, originalID: id}
	state.processedIDs[id] = true
	if explicitNum == nil {
		state.count++
	} else if *explicitNum >= state.count {
		state.count = *explicitNum + 1
	}
	return true
}

func (h *dfdFootnoteHandler) createFootnoteItem(footnoteNumber int, content any, refs []string) *ldNode {
	newItem := ldNewElement("li")
	newItem.setAttribute("class", "footnote")
	newItem.setAttribute("id", "fn:"+strconv.Itoa(footnoteNumber))
	if s, ok := content.(string); ok {
		paragraph := ldNewElement("p")
		paragraph.appendChild(ldParseFragment(s))
		newItem.appendChild(paragraph)
	} else {
		node := content.(*ldNode)
		children := node.children()
		hasParagraphs, hasBlockChildren := false, false
		for _, c := range children {
			if c.name == "p" {
				hasParagraphs = true
			}
			if dfdBlockLevelElements[c.name] {
				hasBlockChildren = true
			}
		}
		switch {
		case !hasParagraphs && !hasBlockChildren:
			paragraph := ldNewElement("p")
			dfdTransferContent(node, paragraph)
			h.removeBackrefs(paragraph)
			newItem.appendChild(paragraph)
		case !hasParagraphs && hasBlockChildren:
			for _, child := range children {
				if h.isBackrefLink(child) {
					continue
				}
				clone := child.cloneNode(true)
				h.removeBackrefs(clone)
				newItem.appendChild(clone)
			}
		default:
			for _, child := range children {
				if h.isBackrefLink(child) {
					continue
				}
				if child.name == "p" {
					if jsTrim(child.textContent()) == "" && ldQuerySelector(child, "img, br") == nil {
						continue
					}
					newP := ldNewElement("p")
					dfdTransferContent(child, newP)
					h.removeBackrefs(newP)
					newItem.appendChild(newP)
				} else {
					clone := child.cloneNode(true)
					h.removeBackrefs(clone)
					newItem.appendChild(clone)
				}
			}
		}
	}
	lastParagraph := ldQuerySelector(newItem, "p:last-of-type")
	if lastParagraph == nil {
		lastParagraph = newItem
	}
	for index, refID := range refs {
		backlink := ldNewElement("a")
		backlink.setAttribute("href", jsDecodeURI("#"+refID))
		backlink.setAttribute("title", "return to article")
		backlink.setAttribute("class", "footnote-backref")
		text := "\u21a9"
		if index < len(refs)-1 {
			text += " "
		}
		backlink.setTextContent(text)
		lastParagraph.appendChild(backlink)
	}
	return newItem
}

func (h *dfdFootnoteHandler) collectFootnotes(element *ldNode) map[int]*dfdFootnote {
	state := &dfdFootnoteState{footnotes: map[int]*dfdFootnote{}, processedIDs: map[string]bool{}, count: 1}
	for _, list := range ldQuerySelectorAll(element, dfdFootnoteListSelectors) {
		if ldMatches(list, "div.footnotes-footer") {
			for _, div := range ldQuerySelectorAll(list, "div.footnote-footer") {
				m := reFootnoteDefNum.FindStringSubmatch(div.id())
				if m == nil {
					continue
				}
				id := m[1]
				if state.processedIDs[id] {
					continue
				}
				clone := div.cloneNode(true)
				if back := ldQuerySelector(clone, "a"); back != nil {
					back.remove()
				}
				text := reLeadingDot.ReplaceAllString(ldInnerHTML(clone), "")
				contentDiv := ldNewElement("div")
				contentDiv.appendChild(ldParseFragment(jsTrim(text)))
				h.addFootnote(state, id, contentDiv, nil)
			}
			continue
		}
		if ldMatches(list, "div.footnote-definition") && !(list.parentElement() != nil && ldMatches(list.parentElement(), "div.footnote-definitions")) {
			id := strings.ToLower(list.id())
			clone := list.cloneNode(true)
			if label := ldQuerySelector(clone, "sup.footnote-definition-label"); label != nil {
				label.remove()
			}
			h.addFootnote(state, id, clone, nil)
			continue
		}
		if ldMatches(list, "div.footnote-definitions") {
			for _, def := range ldQuerySelectorAll(list, "div.footnote-definition") {
				supEl := ldQuerySelector(def, "sup[id]")
				body := ldQuerySelector(def, ".footnote-body")
				if supEl == nil || body == nil {
					continue
				}
				h.addFootnote(state, strings.ToLower(supEl.id()), body.cloneNode(true), nil)
			}
			if parent := list.parentElement(); parent != nil && parent != element && parent.hasClass("footnotes") {
				h.pendingRemovals = append(h.pendingRemovals, parent)
			}
			continue
		}
		if ldMatches(list, "ol.easy-footnotes-wrapper") {
			for _, li := range ldQuerySelectorAll(list, "li.easy-footnote-single") {
				idSpan := ldQuerySelector(li, `span[id^="easy-footnote-bottom-"]`)
				if idSpan == nil {
					continue
				}
				clone := li.cloneNode(true)
				if s := ldQuerySelector(clone, `span[id^="easy-footnote-bottom-"]`); s != nil {
					s.remove()
				}
				if a := ldQuerySelector(clone, "a.easy-footnote-to-top"); a != nil {
					a.remove()
				}
				h.addFootnote(state, strings.ToLower(idSpan.id()), clone, nil)
			}
			for _, span := range ldQuerySelectorAll(element, "span.easy-footnote-margin-adjust") {
				h.pendingRemovals = append(h.pendingRemovals, span)
			}
			continue
		}
		if ldMatches(list, "div.footnotes-segment") {
			for _, heading := range ldQuerySelectorAll(list, "h5.footnote-body-heading") {
				id := ""
				if a := ldQuerySelector(heading, "a[id]"); a != nil {
					id = strings.ToLower(a.id())
				}
				if id == "" {
					continue
				}
				contentDiv := ldNewElement("div")
				sibling := heading.nextElementSibling()
				for sibling != nil && !(sibling.name == "h5" && sibling.hasClass("footnote-body-heading")) {
					if jsTrim(sibling.textContent()) != "" || ldQuerySelector(sibling, "img, br") != nil {
						contentDiv.appendChild(sibling.cloneNode(true))
					}
					sibling = sibling.nextElementSibling()
				}
				h.addFootnote(state, id, contentDiv, nil)
			}
			h.pendingRemovals = append(h.pendingRemovals, list)
			continue
		}
		if ldMatches(list, `div.footnote[data-component-name="FootnoteToDOM"]`) {
			anchor := ldQuerySelector(list, "a.footnote-number")
			content := ldQuerySelector(list, ".footnote-content")
			if anchor != nil && content != nil {
				h.addFootnote(state, strings.ToLower(strings.Replace(anchor.id(), "footnote-", "", 1)), content, nil)
			}
			continue
		}
		for _, li := range ldQuerySelectorAll(list, `li, div[role="listitem"]`) {
			id, content := h.extractListItemIDAndContent(li)
			if content == nil {
				content = li
			}
			h.addFootnote(state, id, content, nil)
		}
	}
	for _, fallback := range []func(*ldNode, *dfdFootnoteState){
		h.tryDataTypeFootnotes, h.tryGenericIDDetection, h.tryWordExport, h.tryGoogleDocs, h.tryLabeledSection,
		h.tryLooseFootnotes, h.tryClassFootnote, h.tryBrSeparatedFootnotes,
	} {
		if state.count > 1 {
			break
		}
		fallback(element, state)
	}
	return state.footnotes
}

func (h *dfdFootnoteHandler) tryDataTypeFootnotes(element *ldNode, state *dfdFootnoteState) {
	for _, def := range ldQuerySelectorAll(element, `p[data-type="footnote"][id]`) {
		id := strings.ToLower(def.id())
		if id == "" {
			continue
		}
		contentDiv := ldNewElement("div")
		clone := def.cloneNode(true)
		if marker := clone.firstElementChild(); marker != nil && marker.name == "sup" && ldQuerySelector(marker, `a[href*="#"]`) != nil {
			marker.remove()
			h.trimLeadingWhitespace(clone)
		}
		contentDiv.appendChild(clone)
		h.addFootnote(state, id, contentDiv, nil)
		h.pendingRemovals = append(h.pendingRemovals, def)
	}
}

func (h *dfdFootnoteHandler) tryGenericIDDetection(element *ldNode, state *dfdFootnoteState) {
	candidateRefs := map[string][]*ldNode{}
	var fragOrder []string
	for _, a := range ldQuerySelectorAll(element, `a[href*="#"]`) {
		fragment := dfdHrefFragment(a)
		if fragment == "" {
			continue
		}
		if !reFootnoteMarker.MatchString(jsTrim(a.textContent())) {
			continue
		}
		if _, ok := candidateRefs[fragment]; !ok {
			fragOrder = append(fragOrder, fragment)
		}
		candidateRefs[fragment] = append(candidateRefs[fragment], a)
	}
	if len(candidateRefs) < 2 {
		return
	}
	fragmentSet := map[string]bool{}
	for k := range candidateRefs {
		fragmentSet[k] = true
	}
	var bestContainer *ldNode
	bestMatchCount := 0
	for _, container := range ldQuerySelectorAll(element, "div, section, aside, footer, ol, ul") {
		if container == element {
			continue
		}
		matchCount := len(h.findMatchingFootnoteElements(container, fragmentSet))
		if matchCount >= 2 && matchCount >= bestMatchCount {
			bestMatchCount = matchCount
			bestContainer = container
		}
	}
	if bestContainer == nil {
		return
	}
	ordered := h.findMatchingFootnoteElements(bestContainer, fragmentSet)
	footnoteFragments := map[string]bool{}
	for _, o := range ordered {
		footnoteFragments[o.id] = true
	}
	externalTotal, externalMatch := 0, 0
	for _, frag := range fragOrder {
		backlink := false
		for _, a := range candidateRefs[frag] {
			if bestContainer.contains(a) {
				backlink = true
				break
			}
		}
		if backlink {
			continue
		}
		externalTotal++
		if footnoteFragments[frag] {
			externalMatch++
		}
	}
	need := dfdCeilMul(externalTotal, 0.75)
	if need < 2 {
		need = 2
	}
	if externalMatch < need {
		bestContainer = nil
	}
	for _, o := range ordered {
		el, id := o.el, o.id
		if state.processedIDs[id] {
			continue
		}
		contentDiv := ldNewElement("div")
		clone := el.cloneNode(true)
		if idAnchor := ldTryQS(clone, `a[id="`+id+`"]`); idAnchor != nil {
			t := jsTrim(idAnchor.textContent())
			if t == "" || reNumDotTail.MatchString(t) {
				idAnchor.remove()
			}
		}
		if named := ldQuerySelector(clone, "a[name]"); named != nil && strings.ToLower(named.attr("name")) == id {
			named.remove()
		}
		if first := clone.first; first != nil && first.kind == ldText {
			first.data = reLeadingSpace.ReplaceAllString(reLeadingNumDot.ReplaceAllString(first.data, ""), "")
		}
		if ldMatches(clone, "li") {
			dfdTransferContent(clone, contentDiv)
		} else {
			contentDiv.appendChild(clone)
		}
		sibling := el.nextElementSibling()
		for sibling != nil && sibling.id() == "" {
			if sibID := h.getChildAnchorID(sibling); sibID != "" && fragmentSet[sibID] {
				break
			}
			contentDiv.appendChild(sibling.cloneNode(true))
			sibling = sibling.nextElementSibling()
		}
		h.addFootnote(state, id, contentDiv, nil)
	}
	if bestContainer != nil {
		h.pendingRemovals = append(h.pendingRemovals, bestContainer)
	}
}

// dfdCeilMul is Math.ceil(n * f).
func dfdCeilMul(n int, f float64) int {
	v := float64(n) * f
	i := int(v)
	if float64(i) < v {
		i++
	}
	return i
}

// ldTryQS is querySelector for a selector built from page data: a selector that cannot be parsed throws, as in the
// original, so the caller's try block sees it.
func ldTryQS(root *ldNode, selector string) *ldNode { return ldQuerySelector(root, selector) }

type dfdNumberedEl struct {
	num int
	el  *ldNode
}

func (h *dfdFootnoteHandler) tryWordExport(element *ldNode, state *dfdFootnoteState) {
	backrefs := ldQuerySelectorAll(element, `a[href*="#_ftnref"]`)
	if len(backrefs) < 2 {
		return
	}
	var pairs []dfdNumberedEl
	for _, anchor := range backrefs {
		if m := reWordFtnRef.FindStringSubmatch(dfdHrefFragment(anchor)); m != nil {
			n, _ := strconv.Atoi(m[1])
			pairs = append(pairs, dfdNumberedEl{n, anchor})
		}
	}
	sort.SliceStable(pairs, func(i, j int) bool { return pairs[i].num < pairs[j].num })
	for _, p := range pairs {
		num, anchor := p.num, p.el
		originalID := "_ftn" + strconv.Itoa(num)
		if state.processedIDs[originalID] {
			continue
		}
		container := anchor.parentElement()
		for container != nil && container != element {
			if t := container.name; t == "p" || t == "div" || t == "li" {
				break
			}
			container = container.parentElement()
		}
		if container == nil || container == element {
			continue
		}
		clone := container.cloneNode(true)
		if back := ldQuerySelector(clone, `a[href*="_ftnref"]`); back != nil {
			if sup := ldClosest(back, "sup"); sup != nil {
				sup.remove()
			} else {
				back.remove()
			}
		}
		contentDiv := ldNewElement("div")
		contentDiv.appendChild(clone)
		n := num
		h.addFootnote(state, originalID, contentDiv, &n)
		h.pendingRemovals = append(h.pendingRemovals, container)
	}
}

func (h *dfdFootnoteHandler) tryGoogleDocs(element *ldNode, state *dfdFootnoteState) {
	var pairs []dfdNumberedEl
	for _, p := range ldQuerySelectorAll(element, `p[id^="ftnt"]`) {
		if m := reGDocID.FindStringSubmatch(p.id()); m != nil {
			n, _ := strconv.Atoi(m[1])
			pairs = append(pairs, dfdNumberedEl{n, p})
		}
	}
	if len(pairs) < 2 {
		return
	}
	sort.SliceStable(pairs, func(i, j int) bool { return pairs[i].num < pairs[j].num })
	for _, p := range pairs {
		num, el := p.num, p.el
		originalID := "ftnt" + strconv.Itoa(num)
		if state.processedIDs[originalID] {
			continue
		}
		clone := el.cloneNode(true)
		if a := ldQuerySelector(clone, `a[href*="#ftnt_ref"]`); a != nil {
			a.remove()
		}
		contentDiv := ldNewElement("div")
		contentDiv.appendChild(clone)
		n := num
		h.addFootnote(state, originalID, contentDiv, &n)
		h.pendingRemovals = append(h.pendingRemovals, el)
		if parent := el.parentElement(); parent != nil && parent != element && parent.name == "div" && len(parent.children()) == 1 {
			h.pendingRemovals = append(h.pendingRemovals, parent)
		}
	}
	firstEl := pairs[0].el
	firstParent := firstEl.parentElement()
	scanFrom := firstEl
	if firstParent != nil && firstParent != element && firstParent.name == "div" {
		scanFrom = firstParent
	}
	if prev := scanFrom.previousElementSibling(); prev != nil && reHeadingLower.MatchString(prev.name) && reFootnoteSection.MatchString(jsTrim(prev.textContent())) {
		h.pendingRemovals = append(h.pendingRemovals, prev)
	}
}
