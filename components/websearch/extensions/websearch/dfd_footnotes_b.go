package websearch

import (
	"sort"
	"strconv"
	"strings"
)

// Port of defuddle 0.19.3 dist/elements/footnotes.js, part 2: the remaining collectors, the inline reference
// standardization and the footnote list.

var (
	reBackrefLead = jsRE(`^[\s\^,.;]*$`, "")
	reAllSpace    = jsRE(`^\s*$`, "")
	reNonDigit    = jsRE(`\D`, "")
	reFootnoteWord = jsRE(`footnote`, "i")
	reCiteNote    = jsRE(`cite_note-(.+)`, "")
	reBibBib      = jsRE(`bib\.bib(\d+)`, "")
	reVS          = jsRE(`[\x{FE0E}\x{FE0F}]`, "")
	reLeadingHash = jsRE(`^[#]`, "")
	reTrailingDot = jsRE(`\.$`, "")
)

func (h *dfdFootnoteHandler) tryLooseFootnotes(element *ldNode, state *dfdFootnoteState) {
	paragraphs, toRemove, ok := h.findLooseFootnoteParagraphs(element)
	if !ok {
		return
	}
	toRemoveSet := map[*ldNode]bool{}
	for _, n := range toRemove {
		toRemoveSet[n] = true
	}
	for i := range paragraphs {
		defPara := paragraphs[i].el
		var nextDef *ldNode
		if i+1 < len(paragraphs) {
			nextDef = paragraphs[i+1].el
		}
		contentDiv := h.stripMarkerAndWrap(defPara)
		sibling := defPara.nextElementSibling()
		for sibling != nil && sibling != nextDef && toRemoveSet[sibling] {
			contentDiv.appendChild(sibling.cloneNode(true))
			sibling = sibling.nextElementSibling()
		}
		h.addFootnote(state, strconv.Itoa(paragraphs[i].num), contentDiv, nil)
	}
	h.pendingRemovals = append(h.pendingRemovals, toRemove...)
}

func (h *dfdFootnoteHandler) tryClassFootnote(element *ldNode, state *dfdFootnoteState) {
	var paras []dfdNumberedEl
	for _, p := range ldQuerySelectorAll(element, "p.footnote") {
		if n, ok := h.parseFootnoteNum(p); ok {
			paras = append(paras, dfdNumberedEl{n, p})
		}
	}
	for _, p := range paras {
		h.addFootnote(state, strconv.Itoa(p.num), h.stripMarkerAndWrap(p.el), nil)
	}
	for _, p := range paras {
		h.pendingRemovals = append(h.pendingRemovals, p.el)
	}
}

func (h *dfdFootnoteHandler) tryLabeledSection(element *ldNode, state *dfdFootnoteState) {
	for _, container := range ldQuerySelectorAll(element, "div, section, aside") {
		className := dfdClassName(container)
		id := container.id()
		if !reFootnoteWord.MatchString(className) && !reFootnoteWord.MatchString(id) {
			continue
		}
		heading := ldQuerySelector(container, "h1, h2, h3, h4, h5, h6")
		if heading == nil || !reFootnoteSection.MatchString(jsTrim(heading.textContent())) {
			continue
		}
		if !h.collectSectionParagraphs(container, state) && !h.collectSectionList(container, state) {
			continue
		}
		h.pendingRemovals = append(h.pendingRemovals, container)
		break
	}
}

func (h *dfdFootnoteHandler) tryBrSeparatedFootnotes(element *ldNode, state *dfdFootnoteState) {
	linked := map[string]bool{}
	for _, a := range ldQuerySelectorAll(element, `a[href^="#"]`) {
		if reFootnoteMarker.MatchString(jsTrim(a.textContent())) {
			if f := dfdHrefFragment(a); f != "" {
				linked[f] = true
			}
		}
	}
	if len(linked) < 2 {
		return
	}
	for _, block := range ldQuerySelectorAll(element, "p, div") {
		if ldQuerySelector(block, "br") == nil {
			continue
		}
		type def struct {
			id    string
			nodes []*ldNode
		}
		var defs []def
		for _, nodes := range h.splitOnBreaks(block) {
			anchor := h.leadingAnchor(nodes)
			if anchor == nil {
				continue
			}
			name, ok := anchor.getAttribute("name")
			id := name
			if !ok || name == "" {
				id = anchor.id()
			}
			id = strings.ToLower(id)
			if id == "" || !linked[id] {
				continue
			}
			var rest []*ldNode
			for _, n := range nodes {
				if n != anchor {
					rest = append(rest, n)
				}
			}
			defs = append(defs, def{id, rest})
		}
		if len(defs) < 2 {
			continue
		}
		for _, d := range defs {
			contentDiv := ldNewElement("div")
			for _, n := range d.nodes {
				contentDiv.appendChild(n.cloneNode(true))
			}
			h.removeBackrefs(contentDiv)
			h.trimLeadingWhitespace(contentDiv)
			h.addFootnote(state, d.id, contentDiv, nil)
		}
		h.pendingRemovals = append(h.pendingRemovals, block)
		if prev := block.previousElementSibling(); prev != nil && prev.name == "hr" {
			h.pendingRemovals = append(h.pendingRemovals, prev)
		}
		return
	}
}

func (h *dfdFootnoteHandler) splitOnBreaks(block *ldNode) [][]*ldNode {
	var segments [][]*ldNode
	var current []*ldNode
	for _, node := range block.childNodes() {
		if node.kind == ldElem && node.name == "br" {
			if len(current) > 0 {
				segments = append(segments, current)
			}
			current = nil
		} else {
			current = append(current, node)
		}
	}
	if len(current) > 0 {
		segments = append(segments, current)
	}
	return segments
}

func (h *dfdFootnoteHandler) leadingAnchor(nodes []*ldNode) *ldNode {
	for _, node := range nodes {
		if node.kind == ldText {
			if jsTrim(node.data) != "" {
				return nil
			}
			continue
		}
		if node.kind != ldElem {
			continue
		}
		if node.name != "a" {
			return nil
		}
		if node.hasAttribute("name") || node.id() != "" {
			return node
		}
		return nil
	}
	return nil
}

func (h *dfdFootnoteHandler) collectSectionParagraphs(container *ldNode, state *dfdFootnoteState) bool {
	var paragraphs []dfdNumberedEl
	for _, p := range ldQuerySelectorAll(container, "p") {
		if n, ok := h.parseFootnoteNum(p); ok {
			paragraphs = append(paragraphs, dfdNumberedEl{n, p})
		}
	}
	if len(paragraphs) == 0 {
		return false
	}
	numbered := map[*ldNode]bool{}
	for _, p := range paragraphs {
		numbered[p.el] = true
	}
	for _, p := range paragraphs {
		contentDiv := h.stripMarkerAndWrap(p.el)
		sibling := p.el.nextElementSibling()
		for sibling != nil && !numbered[sibling] {
			if jsTrim(sibling.textContent()) != "" {
				contentDiv.appendChild(sibling.cloneNode(true))
			}
			h.pendingRemovals = append(h.pendingRemovals, sibling)
			sibling = sibling.nextElementSibling()
		}
		h.addFootnote(state, strconv.Itoa(p.num), contentDiv, nil)
		h.pendingRemovals = append(h.pendingRemovals, p.el)
	}
	return true
}

func (h *dfdFootnoteHandler) collectSectionList(container *ldNode, state *dfdFootnoteState) bool {
	list := ldQuerySelector(container, "ol")
	if list == nil {
		return false
	}
	var items []*ldNode
	for _, c := range list.children() {
		if c.name == "li" {
			items = append(items, c)
		}
	}
	if len(items) == 0 {
		return false
	}
	for index, li := range items {
		id := strings.ToLower(li.id())
		if id == "" {
			id = strconv.Itoa(index + 1)
		}
		h.addFootnote(state, id, li.cloneNode(true), nil)
	}
	return true
}

func (h *dfdFootnoteHandler) trimLeadingWhitespace(parent *ldNode) {
	if first := parent.first; first != nil && first.kind == ldText {
		first.data = reLeadingSpace.ReplaceAllString(first.data, "")
	}
}

func (h *dfdFootnoteHandler) isBoldWrappedSup(el *ldNode) bool {
	return (el.name == "b" || el.name == "strong") && el.first == el.firstElementChild() && el.firstElementChild() != nil && el.firstElementChild().name == "sup"
}

func (h *dfdFootnoteHandler) stripMarkerAndWrap(el *ldNode) *ldNode {
	contentDiv := ldNewElement("div")
	clone := el.cloneNode(true)
	if marker := clone.firstElementChild(); marker != nil {
		if h.isBoldWrappedSup(marker) {
			marker.firstElementChild().remove()
			h.trimLeadingWhitespace(marker)
		} else {
			marker.remove()
			h.trimLeadingWhitespace(clone)
		}
	}
	contentDiv.appendChild(clone)
	return contentDiv
}

func (h *dfdFootnoteHandler) parseFootnoteNum(el *ldNode) (int, bool) {
	if el.first == nil {
		return 0, false
	}
	first := el.firstElementChild()
	if first == nil || first != el.first {
		return 0, false
	}
	tag := first.name
	if h.isBoldWrappedSup(first) {
		first = first.firstElementChild()
		tag = "sup"
	}
	if tag != "sup" && tag != "strong" {
		return 0, false
	}
	numText := jsTrim(first.textContent())
	num := jsParseInt(numText)
	if !jsIsNaN(num) && num >= 1 && jsNumberString(num) == numText {
		return int(num), true
	}
	return 0, false
}

func (h *dfdFootnoteHandler) crossValidate(element *ldNode, paragraphs []dfdNumberedEl) bool {
	numbered := map[int]bool{}
	for _, p := range paragraphs {
		numbered[p.num] = true
	}
	matched := map[int]bool{}
	for _, sup := range ldQuerySelectorAll(element, "sup") {
		inside := false
		for _, p := range paragraphs {
			if p.el.contains(sup) {
				inside = true
				break
			}
		}
		if inside {
			continue
		}
		if ldQuerySelector(sup, "a") != nil {
			continue
		}
		text := jsTrim(sup.textContent())
		n := jsParseInt(text)
		if !jsIsNaN(n) && n >= 1 && jsNumberString(n) == text && numbered[int(n)] {
			matched[int(n)] = true
		}
	}
	return len(matched) >= 2
}

func (h *dfdFootnoteHandler) findLooseFootnoteParagraphs(element *ldNode) (paragraphs []dfdNumberedEl, toRemove []*ldNode, ok bool) {
	allPs := ldQuerySelectorAll(element, "p")
	container := element
	if len(allPs) > 0 {
		if p := allPs[len(allPs)-1].parentElement(); p != nil {
			container = p
		}
	}
	children := container.children()
	for i := len(children) - 1; i >= 0; i-- {
		if children[i].name != "hr" {
			continue
		}
		var paras []dfdNumberedEl
		for j := i + 1; j < len(children); j++ {
			if n, ok := h.parseFootnoteNum(children[j]); ok {
				paras = append(paras, dfdNumberedEl{n, children[j]})
			}
		}
		if len(paras) >= 2 && h.crossValidate(element, paras) {
			return paras, append([]*ldNode(nil), children[i:]...), true
		}
		break
	}
	var trailing []dfdNumberedEl
	firstIdx := -1
	for i := len(children) - 1; i >= 0; i-- {
		child := children[i]
		tag := child.name
		if tag == "p" {
			if n, ok := h.parseFootnoteNum(child); ok {
				trailing = append([]dfdNumberedEl{{n, child}}, trailing...)
				firstIdx = i
				continue
			}
			break
		}
		if tag == "ul" || tag == "ol" || tag == "blockquote" {
			continue
		}
		break
	}
	if len(trailing) >= 2 && h.crossValidate(element, trailing) {
		rm := append([]*ldNode(nil), children[firstIdx:]...)
		if prev := trailing[0].el.previousElementSibling(); prev != nil {
			if reHeadingLower.MatchString(prev.name) && reFootnoteSection.MatchString(jsTrim(prev.textContent())) {
				rm = append([]*ldNode{prev}, rm...)
			}
		}
		return trailing, rm, true
	}
	half := len(allPs) / 2
	var scattered []dfdNumberedEl
	for i := half; i < len(allPs); i++ {
		if n, ok := h.parseFootnoteNum(allPs[i]); ok {
			scattered = append(scattered, dfdNumberedEl{n, allPs[i]})
		}
	}
	if len(scattered) >= 2 && h.crossValidate(element, scattered) {
		var rm []*ldNode
		for _, s := range scattered {
			rm = append(rm, s.el)
		}
		return scattered, rm, true
	}
	return nil, nil, false
}

func (h *dfdFootnoteHandler) isBackrefLink(el *ldNode) bool {
	if el.name != "a" {
		return false
	}
	text := reVS.ReplaceAllString(jsTrim(el.textContent()), "")
	if reBackrefSymbols.MatchString(text) || el.hasClass("footnote-backref") {
		return true
	}
	return reCiteRef.MatchString(el.attr("href"))
}

func (h *dfdFootnoteHandler) removeBackrefs(el *ldNode) {
	for _, a := range ldQuerySelectorAll(el, "a") {
		if h.isBackrefLink(a) {
			if parent := a.parentElement(); parent != nil && parent.name == "sup" && len(parent.children()) == 1 {
				parent.remove()
			} else {
				a.remove()
			}
		}
	}
	for el.first != nil && el.first.kind == ldText {
		text := el.first.data
		if text != "" && reBackrefLead.MatchString(text) && strings.Contains(text, "^") {
			el.first.remove()
		} else {
			break
		}
	}
	for el.last != nil && el.last.kind == ldText {
		if reAllSpace.MatchString(el.last.data) {
			el.last.remove()
		} else {
			break
		}
	}
}

func (h *dfdFootnoteHandler) getChildAnchorID(el *ldNode) string {
	anchor := ldQuerySelector(el, "a[id], a[name]")
	if anchor == nil {
		return ""
	}
	id := anchor.id()
	if id == "" {
		id = anchor.attr("name")
	}
	return strings.ToLower(id)
}

func (h *dfdFootnoteHandler) extractListItemIDAndContent(li *ldNode) (string, *ldNode) {
	if cd := ldQuerySelector(li, ".citations"); cd != nil && strings.HasPrefix(strings.ToLower(cd.id()), "r") {
		return strings.ToLower(cd.id()), ldQuerySelector(cd, ".citation-content")
	}
	rawID := strings.ToLower(li.id())
	for _, prefix := range []string{"bib.bib", "fn:", "fn"} {
		if strings.HasPrefix(rawID, prefix) {
			return rawID[len(prefix):], li
		}
	}
	if li.hasAttribute("data-counter") {
		return strings.ToLower(reTrailingDot.ReplaceAllString(li.attr("data-counter"), "")), li
	}
	segs := strings.Split(rawID, "/")
	if m := reCiteNote.FindStringSubmatch(segs[len(segs)-1]); m != nil {
		return m[1], li
	}
	return rawID, li
}

type dfdMatchedEl struct {
	el *ldNode
	id string
}

func (h *dfdFootnoteHandler) findMatchingFootnoteElements(container *ldNode, fragmentSet map[string]bool) []dfdMatchedEl {
	var results []dfdMatchedEl
	seen := map[string]bool{}
	for _, el := range ldQuerySelectorAll(container, "li, p, div") {
		id := ""
		if elID := el.id(); elID != "" && fragmentSet[strings.ToLower(elID)] {
			id = strings.ToLower(elID)
		} else if elID == "" {
			if anchorID := h.getChildAnchorID(el); anchorID != "" && fragmentSet[anchorID] {
				id = anchorID
			}
		}
		if id != "" && !seen[id] {
			results = append(results, dfdMatchedEl{el, id})
			seen[id] = true
		}
	}
	return results
}

func (h *dfdFootnoteHandler) replaceContainerPreservingText(container, footnoteRef *ldNode) {
	directText := ""
	hasChildElements := false
	for _, node := range container.childNodes() {
		if node.kind == ldText {
			directText += node.data
		} else if node.kind == ldElem {
			hasChildElements = true
		}
	}
	directText = jsTrim(directText)
	if directText != "" && hasChildElements {
		fragment := ldNewFragment()
		fragment.appendChild(ldNewText(directText))
		fragment.appendChild(footnoteRef)
		container.replaceWith(fragment)
	} else {
		container.replaceWith(footnoteRef)
	}
}

func (h *dfdFootnoteHandler) findOuterFootnoteContainer(el *ldNode) *ldNode {
	current := el
	parent := el.parentElement()
	for parent != nil {
		tag := parent.name
		if tag != "span" && tag != "sup" {
			break
		}
		if tag == "span" {
			hasNonFootnote := false
			for _, child := range parent.childNodes() {
				if child == current {
					continue
				}
				if child.kind == ldText && jsTrim(child.data) != "" {
					hasNonFootnote = true
					break
				}
				if child.kind == ldElem && child.name != "sup" {
					hasNonFootnote = true
					break
				}
			}
			if hasNonFootnote {
				break
			}
		}
		current = parent
		parent = parent.parentElement()
	}
	return current
}

func (h *dfdFootnoteHandler) createFootnoteReference(footnoteNumber, refID string) *ldNode {
	sup := ldNewElement("sup")
	sup.setAttribute("id", refID)
	link := ldNewElement("a")
	link.setAttribute("href", jsDecodeURI("#fn:"+footnoteNumber))
	link.setTextContent(footnoteNumber)
	sup.appendChild(link)
	return sup
}

func dfdSortedKeys(m map[int]*dfdFootnote) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}

func (h *dfdFootnoteHandler) collectInlineSidenotes(element *ldNode) map[int]*dfdFootnote {
	footnotes := map[int]*dfdFootnote{}
	containers := ldQuerySelectorAll(element, "span.footnote-container, span.sidenote-container, span.inline-footnote")
	if len(containers) == 0 {
		footrefs := ldQuerySelectorAll(element, "label.footref")
		if len(footrefs) > 0 {
			count := 1
			for _, label := range footrefs {
				sibling := label.nextElementSibling()
				if sibling != nil && sibling.tagName() == "INPUT" && sibling.hasClass("footref-toggle") {
					sibling = sibling.nextElementSibling()
				}
				if sibling == nil || sibling.tagName() != "SPAN" || !sibling.hasClass("sidenote") {
					continue
				}
				content := sibling.cloneNode(true)
				if leadingSup := ldQuerySelector(content, "sup"); leadingSup != nil && content.first == leadingSup {
					leadingSup.remove()
				}
				n := strconv.Itoa(count)
				footnotes[count] = &dfdFootnote{content: content, originalID: n, refs: []string{"fnref:" + n}}
				ref := h.createFootnoteReference(n, "fnref:"+n)
				if inputEl := label.nextElementSibling(); inputEl != nil && inputEl.tagName() == "INPUT" && inputEl.hasClass("footref-toggle") {
					inputEl.remove()
				}
				sibling.remove()
				label.replaceWith(ref)
				count++
			}
			for _, footer := range ldQuerySelectorAll(element, "footer") {
				if ldQuerySelector(footer, ".footdef") != nil {
					footer.remove()
				}
			}
			return footnotes
		}
		for _, s := range ldQuerySelectorAll(element, "span.sidenote") {
			s.remove()
		}
		return footnotes
	}
	count := 1
	for _, container := range containers {
		content := ldQuerySelector(container, "span.footnote, span.sidenote, span.footnoteContent")
		if content == nil {
			continue
		}
		n := strconv.Itoa(count)
		footnotes[count] = &dfdFootnote{content: content.cloneNode(true), originalID: n, refs: []string{"fnref:" + n}}
		container.replaceWith(h.createFootnoteReference(n, "fnref:"+n))
		count++
	}
	return footnotes
}

func (h *dfdFootnoteHandler) collectSidenotesColumn(element *ldNode) map[int]*dfdFootnote {
	footnotes := map[int]*dfdFootnote{}
	columns := ldQuerySelectorAll(element, ".sidenotes-column")
	if len(columns) == 0 {
		ancestor := element.parentElement()
		for i := 0; i < 3 && ancestor != nil && len(columns) == 0; i++ {
			columns = ldQuerySelectorAll(ancestor, ":scope > .sidenotes-column")
			ancestor = ancestor.parentElement()
		}
	}
	if len(columns) == 0 {
		return footnotes
	}
	count := 1
	for _, column := range columns {
		for _, sidenote := range ldQuerySelectorAll(column, ".sidenote[id]") {
			id := sidenote.id()
			if id == "" {
				continue
			}
			footnoteNumber := count
			if idSpan := ldQuerySelector(sidenote, ".sidenote__id"); idSpan != nil {
				if num := reNonDigit.ReplaceAllString(idSpan.textContent(), ""); num != "" {
					footnoteNumber = int(jsParseInt(num))
				}
			}
			contentDiv := ldNewElement("div")
			for _, node := range sidenote.childNodes() {
				if node.kind == ldElem && (node.hasClass("sidenote__id") || node.hasClass("sidenote__label") || node.hasClass("sn-backref")) {
					continue
				}
				contentDiv.appendChild(node.cloneNode(true))
			}
			h.removeBackrefs(contentDiv)
			footnotes[footnoteNumber] = &dfdFootnote{content: contentDiv, originalID: strings.ToLower(id)}
			count++
		}
		column.remove()
	}
	return footnotes
}

func (h *dfdFootnoteHandler) collectAsideFootnotes(element *ldNode) map[int]*dfdFootnote {
	footnotes := map[int]*dfdFootnote{}
	ols := ldQuerySelectorAll(element, "aside > ol[start]")
	for _, ol := range ols {
		aside := ol.parentElement()
		n := jsParseInt(ol.attr("start"))
		if jsIsNaN(n) || n < 1 {
			continue
		}
		items := ldQuerySelectorAll(ol, "li")
		if len(items) == 0 {
			continue
		}
		contentDiv := ldNewElement("div")
		if len(items) == 1 {
			dfdTransferContent(items[0].cloneNode(true), contentDiv)
		} else {
			for _, li := range items {
				p := ldNewElement("p")
				dfdTransferContent(li.cloneNode(true), p)
				contentDiv.appendChild(p)
			}
		}
		footnotes[int(n)] = &dfdFootnote{content: contentDiv, originalID: strconv.Itoa(int(n))}
		aside.remove()
	}
	return footnotes
}

func (h *dfdFootnoteHandler) collectHiddenAsideFootnotes(element *ldNode) map[int]*dfdFootnote {
	footnotes := map[int]*dfdFootnote{}
	refs := ldQuerySelectorAll(element, "span[data-definition]")
	if len(refs) == 0 {
		return footnotes
	}
	asideMap := map[string]*ldNode{}
	for _, a := range ldQuerySelectorAll(element, "aside[id]") {
		asideMap[a.id()] = a
	}
	count := 1
	for _, ref := range refs {
		defID := ref.attr("data-definition")
		if defID == "" {
			continue
		}
		aside := asideMap[defID]
		if aside == nil {
			continue
		}
		contentDiv := ldNewElement("div")
		dfdTransferContent(aside, contentDiv)
		aside.remove()
		n := strconv.Itoa(count)
		refID := "fnref:" + n
		footnotes[count] = &dfdFootnote{content: contentDiv, originalID: strings.ToLower(defID), refs: []string{refID}}
		ref.replaceWith(h.createFootnoteReference(n, refID))
		count++
	}
	return footnotes
}

type dfdSupRef struct {
	footnoteNumber, refID string
}

func (h *dfdFootnoteHandler) standardizeFootnotes(element *ldNode) {
	sidenotes := h.collectInlineSidenotes(element)
	footnotes := h.collectHiddenAsideFootnotes(element)
	dfdMergeFootnotes(footnotes, h.collectFootnotes(element))
	dfdMergeFootnotes(footnotes, h.collectSidenotesColumn(element))
	dfdMergeFootnotes(footnotes, h.collectAsideFootnotes(element))

	inlineRefs := ldQuerySelectorAll(element, dfdFootnoteInlineReferences)
	supGroups := map[*ldNode][]dfdSupRef{}
	var supOrder []*ldNode
	type entry struct {
		num  string
		data *dfdFootnote
	}
	byOriginalID := map[string]entry{}
	for _, k := range dfdSortedKeys(footnotes) {
		d := footnotes[k]
		byOriginalID[strings.ToLower(d.originalID)] = entry{strconv.Itoa(k), d}
	}
	for _, el := range inlineRefs {
		if el.parent == nil {
			continue
		}
		if jsTrim(el.textContent()) == "" {
			continue
		}
		if ldMatches(el, "cite.ltx_cite") {
			var refs []*ldNode
			for _, link := range ldQuerySelectorAll(el, "a") {
				href := link.attr("href")
				if href == "" {
					continue
				}
				segs := strings.Split(href, "/")
				m := reBibBib.FindStringSubmatch(segs[len(segs)-1])
				if m == nil {
					continue
				}
				e, ok := byOriginalID[strings.ToLower(m[1])]
				if !ok {
					continue
				}
				n, _ := strconv.Atoi(e.num)
				refID := dfdMakeRefID(n, len(e.data.refs))
				e.data.refs = append(e.data.refs, refID)
				refs = append(refs, h.createFootnoteReference(e.num, refID))
			}
			if len(refs) > 0 {
				container := h.findOuterFootnoteContainer(el)
				fragment := ldNewFragment()
				for i, ref := range refs {
					if i > 0 {
						fragment.appendChild(ldNewText(" "))
					}
					fragment.appendChild(ref)
				}
				container.replaceWith(fragment)
			}
			continue
		}
		footnoteID := ""
		for _, ex := range dfdInlineRefExtractors {
			if ldMatches(el, ex.selector) {
				footnoteID = ex.extract(el)
				break
			}
		}
		if footnoteID == "" {
			if href := el.attr("href"); href != "" {
				footnoteID = strings.ToLower(reLeadingHash.ReplaceAllString(href, ""))
			}
		}
		if footnoteID == "" {
			continue
		}
		e, ok := byOriginalID[strings.ToLower(footnoteID)]
		if !ok {
			continue
		}
		container := h.findOuterFootnoteContainer(el)
		isSup := container.name == "sup"
		if isSup {
			dup := false
			for _, r := range supGroups[container] {
				if r.footnoteNumber == e.num {
					dup = true
				}
			}
			if dup {
				continue
			}
		}
		n, _ := strconv.Atoi(e.num)
		refID := dfdMakeRefID(n, len(e.data.refs))
		e.data.refs = append(e.data.refs, refID)
		if isSup {
			if _, ok := supGroups[container]; !ok {
				supOrder = append(supOrder, container)
			}
			supGroups[container] = append(supGroups[container], dfdSupRef{e.num, refID})
		} else {
			h.replaceContainerPreservingText(container, h.createFootnoteReference(e.num, refID))
		}
	}

	var unmatched []int
	for _, k := range dfdSortedKeys(footnotes) {
		if len(footnotes[k].refs) == 0 {
			unmatched = append(unmatched, k)
		}
	}
	if len(unmatched) > 0 {
		idMap := map[string]entry{}
		numMap := map[string]entry{}
		for _, k := range unmatched {
			d := footnotes[k]
			idMap[d.originalID] = entry{strconv.Itoa(k), d}
			numMap[strconv.Itoa(k)] = entry{strconv.Itoa(k), d}
		}
		isInsideFootnotes := func(el *ldNode) bool {
			if ldClosest(el, `[id^="fnref:"]`) != nil || ldClosest(el, "#footnotes") != nil {
				return true
			}
			for _, g := range h.pendingRemovals {
				if g.contains(el) {
					return true
				}
			}
			return false
		}
		assignRef := func(el *ldNode, e entry) {
			n, _ := strconv.Atoi(e.num)
			refID := dfdMakeRefID(n, len(e.data.refs))
			e.data.refs = append(e.data.refs, refID)
			container := h.findOuterFootnoteContainer(el)
			h.replaceContainerPreservingText(container, h.createFootnoteReference(e.num, refID))
		}
		for _, link := range ldQuerySelectorAll(element, `a[href*="#"]`) {
			if link.parent == nil || isInsideFootnotes(link) {
				continue
			}
			fragment := dfdHrefFragment(link)
			if fragment == "" {
				continue
			}
			e, ok := idMap[fragment]
			if !ok {
				continue
			}
			if !reFootnoteMarker.MatchString(jsTrim(link.textContent())) {
				continue
			}
			assignRef(link, e)
		}
		hasUnmatched := false
		for _, d := range footnotes {
			if len(d.refs) == 0 {
				hasUnmatched = true
			}
		}
		if hasUnmatched {
			for _, el := range ldQuerySelectorAll(element, "sup, span.footnote-ref") {
				if el.parent == nil || strings.HasPrefix(el.id(), "fnref:") || ldClosest(el, "#footnotes") != nil {
					continue
				}
				m := reFootnoteMarker.FindStringSubmatch(jsTrim(el.textContent()))
				if m == nil {
					continue
				}
				e, ok := numMap[m[1]]
				if !ok {
					e, ok = idMap[m[1]]
				}
				if !ok || len(e.data.refs) > 0 {
					continue
				}
				assignRef(el, e)
			}
		}
	}
	for _, container := range supOrder {
		fragment := ldNewFragment()
		for _, r := range supGroups[container] {
			fragment.appendChild(h.createFootnoteReference(r.footnoteNumber, r.refID))
		}
		container.replaceWith(fragment)
	}
	newList := ldNewElement("div")
	newList.setAttribute("id", "footnotes")
	orderedList := ldNewElement("ol")
	all := map[int]*dfdFootnote{}
	for k, v := range sidenotes {
		all[k] = v
	}
	for k, v := range footnotes {
		all[k] = v
	}
	for _, k := range dfdSortedKeys(all) {
		d := all[k]
		orderedList.appendChild(h.createFootnoteItem(k, d.content, d.refs))
	}
	for _, list := range ldQuerySelectorAll(element, dfdFootnoteListSelectors) {
		list.remove()
	}
	for _, el := range h.pendingRemovals {
		if el.parent != nil {
			el.remove()
		}
	}
	dfdRemoveOrphanedDividers(element)
	if len(orderedList.children()) > 0 {
		newList.appendChild(orderedList)
		element.appendChild(newList)
	}
}

func dfdStandardizeFootnotes(element *ldNode) {
	h := &dfdFootnoteHandler{}
	h.standardizeFootnotes(element)
}
