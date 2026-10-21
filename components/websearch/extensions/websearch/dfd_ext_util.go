package websearch

import "strings"

// Small helpers the extractors share: the optional-chaining reads of the original as functions that accept nil.

// extText is `el?.textContent?.trim() || ”`.
func extText(el *ldNode) string {
	if el == nil {
		return ""
	}
	return jsTrim(el.textContent())
}

// extRawText is `el?.textContent || ”`.
func extRawText(el *ldNode) string {
	if el == nil {
		return ""
	}
	return el.textContent()
}

// extAttr is `el?.getAttribute(name) || ”`.
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

// classListRemove is el.classList.remove(token) on a freshly read list (linkedom's DOMTokenList is a Set of the
// tokens of the class attribute): the attribute is rewritten as the remaining distinct tokens joined by a space,
// and made if it was missing.
func (n *ldNode) classListRemove(token string) {
	seen := map[string]bool{}
	var kept []string
	for _, t := range reWSRun.Split(n.attr("class"), -1) {
		if t == "" || t == token || seen[t] {
			continue
		}
		seen[t] = true
		kept = append(kept, t)
	}
	n.setAttribute("class", strings.Join(kept, " "))
}
