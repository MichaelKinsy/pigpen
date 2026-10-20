package websearch

import (
	"strings"
)

// A DOM with the semantics of linkedom 0.16.11 (the document extract.ts hands to Defuddle), over its own tree:
// the page is parsed the way htmlparser2 9.1 does (ldom_parse.go), not as HTML5 does, so implied tags, attribute
// name case, entity chunking and the html/head/body getters behave as the original's library does; selectors are
// css-select 5.2 (ldom_css.go) and serialization is linkedom's (ldom_serialize.go). Everything here mirrors a
// linkedom method by the name noted in its comment; what Defuddle never calls is not here.

type ldKind uint8

const (
	ldElem ldKind = iota + 1
	ldText
	ldComment
	ldDoc
	ldFrag
	ldDoctype
)

type ldAttr struct{ name, value string }

type ldNode struct {
	kind  ldKind
	name  string // an element's localName (lower case when parsed), a doctype's name
	svg   bool   // an element made with createElementNS(SVG_NAMESPACE): `'ownerSVGElement' in element`
	data  string // text and comment data, a doctype's serialization
	attrs []ldAttr

	parent, first, last, prev, next *ldNode

	// content is a template element's DocumentFragment; private is the template a fragment belongs to.
	content, private *ldNode
}

// jsThrow is a JavaScript exception thrown by the library code being ported. It is raised with panic and caught
// where the original has a try/catch (ldTry).
type jsThrow struct{ name, msg string }

func (e *jsThrow) Error() string { return e.msg }

func ldThrow(msg string)     { panic(&jsThrow{name: "Error", msg: msg}) }
func ldThrowType(msg string) { panic(&jsThrow{name: "TypeError", msg: msg}) }

// ldTry runs fn and returns the exception it threw, if any.
func ldTry(fn func()) (thrown *jsThrow) {
	defer func() {
		if r := recover(); r != nil {
			if t, ok := r.(*jsThrow); ok {
				thrown = t
				return
			}
			panic(r)
		}
	}()
	fn()
	return nil
}

func ldNewDocument() *ldNode { return &ldNode{kind: ldDoc, name: "#document"} }
func ldNewFragment() *ldNode { return &ldNode{kind: ldFrag, name: "#document-fragment"} }
func ldNewText(data string) *ldNode {
	return &ldNode{kind: ldText, name: "#text", data: data}
}
func ldNewComment(data string) *ldNode {
	return &ldNode{kind: ldComment, name: "#comment", data: data}
}

// ldNewElement is document.createElement(localName).
func ldNewElement(name string) *ldNode {
	name = strings.ToLower(name)
	n := &ldNode{kind: ldElem, name: name}
	if name == "template" {
		n.content = ldNewFragment()
		n.content.private = n
	}
	return n
}

// ldNewSVGElement is document.createElementNS(SVG_NAMESPACE, name).
func ldNewSVGElement(name string) *ldNode {
	return &ldNode{kind: ldElem, name: name, svg: true}
}

func (n *ldNode) isElem() bool { return n != nil && n.kind == ldElem }

// tagName is Element.tagName / nodeName: the upper-cased local name (the document ignores case).
func (n *ldNode) tagName() string {
	switch n.kind {
	case ldElem:
		return strings.ToUpper(n.name)
	}
	return n.name
}

// is reports whether n is an element with that lower-case local name.
func (n *ldNode) is(name string) bool { return n != nil && n.kind == ldElem && n.name == name }

// ---- tree ------------------------------------------------------------------------------------------------------

func (n *ldNode) hasChildNodes() bool { return n.first != nil }

func (n *ldNode) childNodes() []*ldNode {
	var out []*ldNode
	for c := n.first; c != nil; c = c.next {
		out = append(out, c)
	}
	return out
}

// children is ParentNode.children: the element children.
func (n *ldNode) children() []*ldNode {
	var out []*ldNode
	for c := n.first; c != nil; c = c.next {
		if c.kind == ldElem {
			out = append(out, c)
		}
	}
	return out
}

func (n *ldNode) firstElementChild() *ldNode {
	for c := n.first; c != nil; c = c.next {
		if c.kind == ldElem {
			return c
		}
	}
	return nil
}

func (n *ldNode) lastElementChild() *ldNode {
	for c := n.last; c != nil; c = c.prev {
		if c.kind == ldElem {
			return c
		}
	}
	return nil
}

func (n *ldNode) nextElementSibling() *ldNode {
	for s := n.next; s != nil; s = s.next {
		if s.kind == ldElem {
			return s
		}
	}
	return nil
}

func (n *ldNode) previousElementSibling() *ldNode {
	for s := n.prev; s != nil; s = s.prev {
		if s.kind == ldElem {
			return s
		}
	}
	return nil
}

// parentElement is the parentNode unless that is the document or a fragment.
func (n *ldNode) parentElement() *ldNode {
	if n.parent != nil && (n.parent.kind == ldDoc || n.parent.kind == ldFrag) {
		return nil
	}
	return n.parent
}

// contains is Node.contains: a node contains itself.
func (n *ldNode) contains(other *ldNode) bool {
	for p := other; p != nil; p = p.parent {
		if p == n {
			return true
		}
	}
	return false
}

// remove is ChildNode.remove.
func (n *ldNode) remove() {
	p := n.parent
	if p == nil {
		return
	}
	if n.prev != nil {
		n.prev.next = n.next
	} else {
		p.first = n.next
	}
	if n.next != nil {
		n.next.prev = n.prev
	} else {
		p.last = n.prev
	}
	n.parent, n.prev, n.next = nil, nil, nil
}

// insertBefore is ParentNode.insertBefore: a node already in the tree moves, a fragment gives up its children.
func (n *ldNode) insertBefore(node, before *ldNode) *ldNode {
	if node == before {
		return node
	}
	if node == n {
		ldThrow("unable to append a node to itself")
	}
	switch node.kind {
	case ldFrag:
		first, last := node.first, node.last
		if first != nil {
			// Detach the whole run, then link it before `before`.
			node.first, node.last = nil, nil
			for c := first; c != nil; c = c.next {
				c.parent = n
			}
			ldLinkRun(n, first, last, before)
			if node.private != nil {
				node.private.replaceChildren()
			}
		}
	case ldDoctype:
		node.parent = n
		ldLinkRun(n, node, node, before)
	default:
		node.remove()
		node.parent = n
		ldLinkRun(n, node, node, before)
	}
	return node
}

// ldLinkRun links the siblings first..last (already parented to p) in front of before, or at the end.
func ldLinkRun(p, first, last, before *ldNode) {
	if before == nil {
		first.prev = p.last
		if p.last != nil {
			p.last.next = first
		} else {
			p.first = first
		}
		last.next = nil
		p.last = last
		return
	}
	first.prev = before.prev
	if before.prev != nil {
		before.prev.next = first
	} else {
		p.first = first
	}
	last.next = before
	before.prev = last
}

func (n *ldNode) appendChild(node *ldNode) *ldNode { return n.insertBefore(node, nil) }

func (n *ldNode) prepend(nodes ...*ldNode) {
	for _, node := range nodes {
		n.insertBefore(node, n.first)
	}
}

func (n *ldNode) replaceChildren(nodes ...*ldNode) {
	for c := n.first; c != nil; {
		next := c.next
		c.remove()
		c = next
	}
	for _, node := range nodes {
		n.insertBefore(node, nil)
	}
}

// ldAsFragment is the ChildNode helper that moves the nodes into a fresh fragment.
func ldAsFragment(nodes []*ldNode) *ldNode {
	f := ldNewFragment()
	for _, node := range nodes {
		f.insertBefore(node, nil)
	}
	return f
}

// before is ChildNode.before.
func (n *ldNode) before(nodes ...*ldNode) {
	if n.parent != nil {
		n.parent.insertBefore(ldAsFragment(nodes), n)
	}
}

// after is ChildNode.after.
func (n *ldNode) after(nodes ...*ldNode) {
	if n.parent != nil {
		n.parent.insertBefore(ldAsFragment(nodes), n.next)
	}
}

// replaceWith is ChildNode.replaceWith. A node that is among its own replacements is first swapped for a shallow
// clone of itself, as the library does, so that it ends up where it was.
func (n *ldNode) replaceWith(nodes ...*ldNode) {
	parent := n.parent
	if parent == nil {
		return
	}
	node := n
	for _, x := range nodes {
		if x == n {
			node = n.cloneNode(false)
			n.replaceWith(node)
			break
		}
	}
	parent.insertBefore(ldAsFragment(nodes), node)
	node.remove()
}

// replaceChild is ParentNode.replaceChild.
func (n *ldNode) replaceChild(node, replaced *ldNode) *ldNode {
	next := replaced.next
	replaced.remove()
	n.insertBefore(node, next)
	return replaced
}

// cloneNode is Node.cloneNode: attributes always, children when deep.
func (n *ldNode) cloneNode(deep bool) *ldNode {
	switch n.kind {
	case ldText:
		return ldNewText(n.data)
	case ldComment:
		return ldNewComment(n.data)
	case ldDoctype:
		c := *n
		c.parent, c.prev, c.next, c.first, c.last = nil, nil, nil, nil, nil
		return &c
	case ldDoc:
		d := ldNewDocument()
		if deep {
			for c := n.first; c != nil; c = c.next {
				d.insertBefore(c.cloneNode(true), nil)
			}
		}
		return d
	case ldFrag:
		f := ldNewFragment()
		if deep {
			for c := n.first; c != nil; c = c.next {
				f.insertBefore(c.cloneNode(true), nil)
			}
		}
		return f
	}
	var c *ldNode
	if n.svg {
		c = ldNewSVGElement(n.name)
	} else {
		c = ldNewElement(n.name)
	}
	c.attrs = append(c.attrs, n.attrs...)
	if deep {
		for ch := n.first; ch != nil; ch = ch.next {
			c.insertBefore(ch.cloneNode(true), nil)
		}
	}
	return c
}

// normalize is ParentNode.normalize: empty text nodes go, adjacent ones merge into the earlier one.
func (n *ldNode) normalize() {
	ldWalk(n, func(c *ldNode) {
		if c.kind != ldText {
			return
		}
		if c.data == "" {
			c.remove()
		} else if c.prev != nil && c.prev.kind == ldText {
			c.prev.data += c.data
			c.remove()
		}
	})
}

// ldWalk visits the descendants of root in document order. The next node is taken before fn runs, so fn may
// remove the node it is given (the linkedom loops do the same through `$next`).
func ldWalk(root *ldNode, fn func(*ldNode)) {
	n := root.first
	for n != nil {
		next := ldNextNode(n, root)
		fn(n)
		n = next
	}
}

// ldNextNode is the node after n in document order below root, or nil.
func ldNextNode(n, root *ldNode) *ldNode {
	if n.first != nil {
		return n.first
	}
	for n != nil && n != root {
		if n.next != nil {
			return n.next
		}
		n = n.parent
	}
	return nil
}

// ---- text ------------------------------------------------------------------------------------------------------

// textContent is Element.textContent: the text nodes below the element, comments excluded.
func (n *ldNode) textContent() string {
	switch n.kind {
	case ldText, ldComment:
		return n.data
	case ldDoc, ldFrag, ldDoctype:
		return "" // null in linkedom
	}
	var b strings.Builder
	ldWalkAll(n, func(c *ldNode) {
		if c.kind == ldText {
			b.WriteString(c.data)
		}
	})
	return b.String()
}

// ldWalkAll visits every descendant in document order without tolerating removal.
func ldWalkAll(root *ldNode, fn func(*ldNode)) {
	for c := root.first; c != nil; c = c.next {
		fn(c)
		if c.first != nil {
			ldWalkAll(c, fn)
		}
	}
}

// setTextContent is Element.textContent = text.
func (n *ldNode) setTextContent(text string) {
	if n.kind == ldText || n.kind == ldComment {
		n.data = text
		return
	}
	n.replaceChildren()
	if text != "" {
		n.appendChild(ldNewText(text))
	}
}

// ---- attributes ------------------------------------------------------------------------------------------------

func (n *ldNode) attrIndex(name string) int {
	for i := range n.attrs {
		if n.attrs[i].name == name {
			return i
		}
	}
	return -1
}

// getAttribute is Element.getAttribute: the name must match exactly ("srcSet" is not "srcset"), and "class" is
// the class list, an empty string when there is no class attribute.
func (n *ldNode) getAttribute(name string) (string, bool) {
	if name == "class" {
		v, _ := n.attrValue("class")
		return v, true
	}
	return n.attrValue(name)
}

func (n *ldNode) attrValue(name string) (string, bool) {
	if i := n.attrIndex(name); i >= 0 {
		return n.attrs[i].value, true
	}
	return "", false
}

// attr is `getAttribute(name) || ”`.
func (n *ldNode) attr(name string) string {
	v, _ := n.getAttribute(name)
	return v
}

func (n *ldNode) hasAttribute(name string) bool { return n.attrIndex(name) >= 0 }

// ldClassValue is className = value: the tokens of the value, split on whitespace, without empties or repeats,
// joined by one space.
func ldClassValue(value string) string {
	tokens := strings.FieldsFunc(value, jsSpace)
	if len(tokens) < 2 {
		return strings.Join(tokens, " ")
	}
	seen := make(map[string]bool, len(tokens))
	out := tokens[:0]
	for _, t := range tokens {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return strings.Join(out, " ")
}

// setAttribute is Element.setAttribute: a new attribute goes in front of the others.
func (n *ldNode) setAttribute(name, value string) {
	if name == "class" {
		value = ldClassValue(value)
	}
	if i := n.attrIndex(name); i >= 0 {
		n.attrs[i].value = value
		return
	}
	n.attrs = append(n.attrs, ldAttr{})
	copy(n.attrs[1:], n.attrs)
	n.attrs[0] = ldAttr{name, value}
}

// removeAttribute is Element.removeAttribute.
func (n *ldNode) removeAttribute(name string) {
	if i := n.attrIndex(name); i >= 0 {
		n.attrs = append(n.attrs[:i:i], n.attrs[i+1:]...)
	}
}

// id is Element.id.
func (n *ldNode) id() string { return n.attr("id") }

// className is Element.className.
func (n *ldNode) className() string { return n.attr("class") }

// hasClass is classList.contains.
func (n *ldNode) hasClass(token string) bool {
	if token == "" {
		return false
	}
	for _, t := range strings.Split(n.className(), " ") {
		if t == token {
			return true
		}
	}
	return false
}

// ---- lookups ---------------------------------------------------------------------------------------------------

// getElementByID is NonElementParentNode.getElementById.
func (n *ldNode) getElementByID(id string) *ldNode {
	for c := n.first; c != nil; c = ldNextNode(c, n) {
		if c.kind == ldElem && c.id() == id {
			return c
		}
	}
	return nil
}

// getElementsByTagName is ParentNode.getElementsByTagName for an HTML document (the name matches the local name
// or its upper-case form).
func (n *ldNode) getElementsByTagName(tag string) []*ldNode {
	var out []*ldNode
	ldWalkAll(n, func(c *ldNode) {
		if c.kind == ldElem && (c.name == tag || strings.ToUpper(c.name) == tag) {
			out = append(out, c)
		}
	})
	return out
}

// ---- document --------------------------------------------------------------------------------------------------

// documentElement is Document.documentElement: the first element child.
func (n *ldNode) documentElement() *ldNode { return n.firstElementChild() }

// head is HTMLDocument.head: it makes the head element when the first child of the root is not one.
func (n *ldNode) head() *ldNode {
	de := n.documentElement()
	if de == nil {
		ldThrowType("Cannot destructure property 'firstElementChild' of 'documentElement' as it is null.")
	}
	first := de.firstElementChild()
	if first == nil || first.tagName() != "HEAD" {
		first = ldNewElement("head")
		de.prepend(first)
	}
	return first
}

// body is HTMLDocument.body: the element after the head, made when it is not a body.
func (n *ldNode) body() *ldNode {
	head := n.head()
	next := head.nextElementSibling()
	if next == nil || next.tagName() != "BODY" {
		next = ldNewElement("body")
		head.after(next)
	}
	return next
}

// title is HTMLDocument.title.
func (n *ldNode) title() string {
	head := n.head()
	for _, t := range head.getElementsByTagName("title") {
		return t.textContent()
	}
	return ""
}

// ldIsTextElement are the elements linkedom builds from TextElement: their innerHTML is their text.
func ldIsTextElement(n *ldNode) bool {
	if n.kind != ldElem || n.svg {
		return false
	}
	switch n.name {
	case "script", "style", "textarea", "title":
		return true
	}
	return false
}

// getTemplateContent is HTMLTemplateElement.content: the fragment is filled from the children on first use.
func (n *ldNode) templateContent() *ldNode {
	if n.content == nil {
		return nil
	}
	if n.hasChildNodes() && !n.content.hasChildNodes() {
		for c := n.first; c != nil; c = c.next {
			n.content.appendChild(c.cloneNode(true))
		}
	}
	return n.content
}
