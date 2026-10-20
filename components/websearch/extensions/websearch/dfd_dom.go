package websearch

import (
	"regexp"
	"strings"
)

// Port of defuddle 0.19.3 dist/utils/dom.js (the parts that do not parse or serialize) and dist/utils.js's
// element helpers.

func dfdClassName(el *ldNode) string { return el.attr("class") }

var reResponsiveShow = jsRE(`^(sm|md|lg|xl|2xl|min-\[|max-\[):(?:block|flex|grid|inline|table|contents)`, "")

func dfdHasResponsiveShowClass(className string) bool {
	for _, t := range jsSplitWS(className) {
		if reResponsiveShow.MatchString(t) {
			return true
		}
	}
	return false
}

var reDangerousStrip = jsRE(`[\s\x{0}-\x{1f}]+`, "")

// dfdIsDangerousURL is dom.isDangerousUrl.
func dfdIsDangerousURL(url string, allowInlineImage bool) bool {
	n := strings.ToLower(reDangerousStrip.ReplaceAllString(url, ""))
	if strings.HasPrefix(n, "javascript:") || strings.HasPrefix(n, "blob:") {
		return true
	}
	if strings.HasPrefix(n, "data:") {
		return !(allowInlineImage && strings.HasPrefix(n, "data:image/"))
	}
	return false
}

// dfdIsDirectTableChild is dom.isDirectTableChild: no table between the element and the ancestor.
func dfdIsDirectTableChild(el, ancestor *ldNode) bool {
	p := el.parent
	for p != nil && p != ancestor {
		if p.kind == ldElem && p.tagName() == "TABLE" {
			return false
		}
		p = p.parent
	}
	return p == ancestor
}

// dfdTransferContent is dom.transferContent: the children of source move to target, which is emptied first.
func dfdTransferContent(source, target *ldNode) {
	target.replaceChildren()
	for source.first != nil {
		target.appendChild(source.first)
	}
}

// dfdIsSVG is utils.isSVGElement: inside an svg element, or in the SVG namespace.
func dfdIsSVG(el *ldNode) bool {
	return ldClosest(el, "svg") != nil || el.svg
}

var _ = regexp.MustCompile
