package websearch

import (
	"regexp"
	"strings"
)

// linkedom's serialization (interface/element.js toString, text.js, comment.js, attr.js, html/text-element.js,
// shared/text-escaper.js, document-type.js) for an HTML document.

// ldEmptyAttributes is shared/attributes.js emptyAttributes: those written without a value when they have none.
var ldEmptyAttributes = map[string]bool{
	"allowfullscreen": true, "allowpaymentrequest": true, "async": true, "autofocus": true, "autoplay": true,
	"checked": true, "class": true, "contenteditable": true, "controls": true, "default": true, "defer": true,
	"disabled": true, "draggable": true, "formnovalidate": true, "hidden": true, "id": true, "ismap": true,
	"itemscope": true, "loop": true, "multiple": true, "muted": true, "nomodule": true, "novalidate": true,
	"open": true, "playsinline": true, "readonly": true, "required": true, "reversed": true, "selected": true,
	"style": true, "truespeed": true,
}

var ldVoidRE = regexp.MustCompile(`(?i)^(?:area|base|br|col|embed|hr|img|input|keygen|link|menuitem|meta|param|source|track|wbr)$`)

// ldEscape is the text escaper: &, <, > and the no-break space.
func ldEscape(s string) string {
	if !strings.ContainsAny(s, "<>&\u00a0") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 16)
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '\u00a0':
			b.WriteString("&#160;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func ldWriteAttr(b *strings.Builder, a ldAttr) {
	if ldEmptyAttributes[a.name] && a.value == "" {
		switch a.name {
		case "id", "class", "style":
			// The element's loop drops these three when they are the bare name.
			return
		}
		b.WriteByte(' ')
		b.WriteString(a.name)
		return
	}
	b.WriteByte(' ')
	b.WriteString(a.name)
	b.WriteString(`="`)
	b.WriteString(strings.ReplaceAll(a.value, `"`, "&quot;"))
	b.WriteByte('"')
}

// ldOuterHTML is Node.toString() (outerHTML for an element).
func ldOuterHTML(n *ldNode) string {
	var b strings.Builder
	ldSerialize(&b, n)
	return b.String()
}

// ldInnerHTML is innerHTML: the serialization of the children; a TextElement's innerHTML is its text.
func ldInnerHTML(n *ldNode) string {
	if ldIsTextElement(n) {
		return n.textContent()
	}
	var b strings.Builder
	for c := n.first; c != nil; c = c.next {
		ldSerialize(&b, c)
	}
	return b.String()
}

func ldSerialize(b *strings.Builder, n *ldNode) {
	switch n.kind {
	case ldText:
		b.WriteString(ldEscape(n.data))
	case ldComment:
		b.WriteString("<!--" + n.data + "-->")
	case ldDoctype:
		b.WriteString(n.data)
	case ldDoc:
		for c := n.first; c != nil; c = c.next {
			ldSerialize(b, c)
		}
	case ldFrag:
		b.WriteString("<#document-fragment>")
		for c := n.first; c != nil; c = c.next {
			ldSerialize(b, c)
		}
		b.WriteString("</#document-fragment>")
	case ldElem:
		if ldIsTextElement(n) {
			ldSerializeTextElement(b, n)
			return
		}
		ldSerializeElement(b, n)
	}
}

func ldSerializeElement(b *strings.Builder, n *ldNode) {
	b.WriteByte('<')
	b.WriteString(n.name)
	for _, a := range n.attrs {
		ldWriteAttr(b, a)
	}
	if n.first == nil {
		switch {
		case n.svg:
			b.WriteString(" />")
		case ldVoidRE.MatchString(n.name):
			b.WriteByte('>')
		default:
			b.WriteString("></" + n.name + ">")
		}
		return
	}
	b.WriteByte('>')
	for c := n.first; c != nil; c = c.next {
		ldSerialize(b, c)
	}
	b.WriteString("</" + n.name + ">")
}

// ldSerializeTextElement is TextElement.toString: the shallow serialization with the first `><` replaced by the
// text between them, through String.prototype.replace and its `$` patterns.
func ldSerializeTextElement(b *strings.Builder, n *ldNode) {
	var shallow strings.Builder
	ldSerializeElement(&shallow, &ldNode{kind: ldElem, name: n.name, attrs: n.attrs})
	outer := shallow.String()
	i := strings.Index(outer, "><")
	if i < 0 {
		b.WriteString(outer)
		return
	}
	b.WriteString(outer[:i])
	b.WriteString(jsExpandReplacement(">"+n.textContent()+"<", "><", outer[:i], outer[i+2:]))
	b.WriteString(outer[i+2:])
}

// jsExpandReplacement expands the `$` patterns of a String.prototype.replace replacement string for a match
// without capture groups: $$, $&, $` and $'. Anything else stays as written.
func jsExpandReplacement(repl, match, before, after string) string {
	if !strings.Contains(repl, "$") {
		return repl
	}
	var b strings.Builder
	for i := 0; i < len(repl); i++ {
		if repl[i] != '$' || i+1 >= len(repl) {
			b.WriteByte(repl[i])
			continue
		}
		switch repl[i+1] {
		case '$':
			b.WriteByte('$')
			i++
		case '&':
			b.WriteString(match)
			i++
		case '`':
			b.WriteString(before)
			i++
		case '\'':
			b.WriteString(after)
			i++
		default:
			b.WriteByte('$')
		}
	}
	return b.String()
}

// ldSetInnerHTML is the innerHTML setter: a TextElement takes the text as it is; any other element gets the
// nodes of the parsed markup.
func ldSetInnerHTML(n *ldNode, html string) {
	if ldIsTextElement(n) {
		n.setTextContent(html)
		return
	}
	doc := ldNewDocument()
	if html != "" {
		ldParseInto(doc, html)
	}
	n.replaceChildren(doc.childNodes()...)
}

// ldParseFragment is dom.parseHTML(doc, html): the markup through a template element, as a fragment.
func ldParseFragment(html string) *ldNode {
	if html == "" {
		return ldNewFragment()
	}
	t := ldNewElement("template")
	ldSetInnerHTML(t, html)
	return t.templateContent()
}
