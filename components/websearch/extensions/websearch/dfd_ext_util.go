package websearch

// Small helpers the extractors share: the optional-chaining reads of the original as functions that accept nil.

// extText is `el?.textContent?.trim() || ''`.
func extText(el *ldNode) string {
	if el == nil {
		return ""
	}
	return jsTrim(el.textContent())
}

// extRawText is `el?.textContent || ''`.
func extRawText(el *ldNode) string {
	if el == nil {
		return ""
	}
	return el.textContent()
}

// extAttr is `el?.getAttribute(name) || ''`.
func extAttr(el *ldNode, name string) string {
	if el == nil {
		return ""
	}
	return el.attr(name)
}

// extQuery is `el?.querySelector(selector)`.
func extQuery(el *ldNode, selector string) *ldNode {
	if el == nil {
		return nil
	}
	return ldQuerySelector(el, selector)
}

// extRemoveAll is `el.querySelectorAll(selector).forEach(e => e.remove())`.
func extRemoveAll(root *ldNode, selector string) {
	for _, e := range ldQuerySelectorAll(root, selector) {
		e.remove()
	}
}

// extSerialize is dom.serializeHTML: the innerHTML.
func extSerialize(el *ldNode) string { return ldInnerHTML(el) }

// extTruncate50 is `text.length > 50 ? text.slice(0, 50) + '...' : text` (UTF-16 units).
func extTruncate50(text string) string {
	if jsLen(text) > 50 {
		return jsSlice(text, 0, 50) + "..."
	}
	return text
}
