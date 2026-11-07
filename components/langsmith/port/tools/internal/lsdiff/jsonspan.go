package lsdiff

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// node is a JSON value with its byte span in the source text, so a document can be re-emitted with some values
// replaced and everything else left as the sender wrote it, byte for byte.
type node struct {
	kind       byte // 'o' object, 'a' array, 's' string, 'n' number, 'l' literal (true false null)
	start, end int
	members    []member // object
	items      []*node  // array
}

type member struct {
	key      string // decoded
	keyStart int    // span of the raw key token
	keyEnd   int
	val      *node
}

func (n *node) get(key string) *node {
	if n == nil || n.kind != 'o' {
		return nil
	}
	for _, m := range n.members {
		if m.key == key {
			return m.val
		}
	}
	return nil
}

type parser struct {
	src []byte
	pos int
}

// parseJSON parses one JSON document (RFC 8259, surrounding whitespace allowed).
func parseJSON(src []byte) (*node, error) {
	p := &parser{src: src}
	p.ws()
	n, err := p.value(0)
	if err != nil {
		return nil, err
	}
	p.ws()
	if p.pos != len(src) {
		return nil, fmt.Errorf("trailing data at offset %d", p.pos)
	}
	return n, nil
}

func (p *parser) ws() {
	for p.pos < len(p.src) {
		switch p.src[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

func (p *parser) value(depth int) (*node, error) {
	if depth > 512 {
		return nil, errors.New("nesting too deep")
	}
	if p.pos >= len(p.src) {
		return nil, errors.New("unexpected end of input")
	}
	start := p.pos
	switch c := p.src[p.pos]; {
	case c == '{':
		n := &node{kind: 'o', start: start}
		p.pos++
		p.ws()
		if p.pos < len(p.src) && p.src[p.pos] == '}' {
			p.pos++
			n.end = p.pos
			return n, nil
		}
		for {
			p.ws()
			ks := p.pos
			if p.pos >= len(p.src) || p.src[p.pos] != '"' {
				return nil, fmt.Errorf("object key expected at offset %d", p.pos)
			}
			if err := p.str(); err != nil {
				return nil, err
			}
			var key string
			if err := json.Unmarshal(p.src[ks:p.pos], &key); err != nil {
				return nil, err
			}
			ke := p.pos
			p.ws()
			if p.pos >= len(p.src) || p.src[p.pos] != ':' {
				return nil, fmt.Errorf("':' expected at offset %d", p.pos)
			}
			p.pos++
			p.ws()
			v, err := p.value(depth + 1)
			if err != nil {
				return nil, err
			}
			n.members = append(n.members, member{key: key, keyStart: ks, keyEnd: ke, val: v})
			p.ws()
			if p.pos >= len(p.src) {
				return nil, errors.New("unterminated object")
			}
			if p.src[p.pos] == ',' {
				p.pos++
				continue
			}
			if p.src[p.pos] == '}' {
				p.pos++
				n.end = p.pos
				return n, nil
			}
			return nil, fmt.Errorf("',' or '}' expected at offset %d", p.pos)
		}
	case c == '[':
		n := &node{kind: 'a', start: start}
		p.pos++
		p.ws()
		if p.pos < len(p.src) && p.src[p.pos] == ']' {
			p.pos++
			n.end = p.pos
			return n, nil
		}
		for {
			p.ws()
			v, err := p.value(depth + 1)
			if err != nil {
				return nil, err
			}
			n.items = append(n.items, v)
			p.ws()
			if p.pos >= len(p.src) {
				return nil, errors.New("unterminated array")
			}
			if p.src[p.pos] == ',' {
				p.pos++
				continue
			}
			if p.src[p.pos] == ']' {
				p.pos++
				n.end = p.pos
				return n, nil
			}
			return nil, fmt.Errorf("',' or ']' expected at offset %d", p.pos)
		}
	case c == '"':
		if err := p.str(); err != nil {
			return nil, err
		}
		return &node{kind: 's', start: start, end: p.pos}, nil
	case c == '-' || (c >= '0' && c <= '9'):
		p.pos++
		for p.pos < len(p.src) {
			d := p.src[p.pos]
			if (d >= '0' && d <= '9') || d == '.' || d == 'e' || d == 'E' || d == '+' || d == '-' {
				p.pos++
				continue
			}
			break
		}
		if !json.Valid(p.src[start:p.pos]) {
			return nil, fmt.Errorf("bad number at offset %d", start)
		}
		return &node{kind: 'n', start: start, end: p.pos}, nil
	default:
		for _, lit := range []string{"true", "false", "null"} {
			if bytes.HasPrefix(p.src[p.pos:], []byte(lit)) {
				p.pos += len(lit)
				return &node{kind: 'l', start: start, end: p.pos}, nil
			}
		}
		return nil, fmt.Errorf("unexpected %q at offset %d", c, p.pos)
	}
}

// str advances over a string token starting at the opening quote.
func (p *parser) str() error {
	p.pos++
	for p.pos < len(p.src) {
		switch p.src[p.pos] {
		case '"':
			p.pos++
			return nil
		case '\\':
			p.pos += 2
		default:
			p.pos++
		}
	}
	return errors.New("unterminated string")
}

// doc is a parsed document with the edits collected for it.
type doc struct {
	src    []byte
	root   *node
	repl   map[*node]string // value replaced by this text
	sorted map[*node][]string
	// compact marks arrays inside a key-sorted scope: re-emitted without whitespace like the objects around them.
	compact map[*node]bool
	// drop marks object members (by their value node) that are left out when a key-sorted object is re-emitted.
	drop map[*node]bool
}

func newDoc(src []byte) (*doc, error) {
	root, err := parseJSON(src)
	if err != nil {
		return nil, err
	}
	return &doc{src: src, root: root, repl: map[*node]string{}, sorted: map[*node][]string{}, compact: map[*node]bool{}, drop: map[*node]bool{}}, nil
}

// sortDeep marks n and every object below it as key-order-canonical. lead lists keys that stay in front, in the
// order the sender wrote them (the keys the extension itself puts first), for n only.
func (d *doc) sortDeep(n *node, lead []string) {
	if n == nil {
		return
	}
	switch n.kind {
	case 'o':
		d.sorted[n] = lead
		for _, m := range n.members {
			d.sortDeep(m.val, nil)
		}
	case 'a':
		d.compact[n] = true
		for _, it := range n.items {
			d.sortDeep(it, nil)
		}
	}
}

// render emits the document: replaced values as given, key-sorted objects canonically (compact), everything else
// exactly as in the source.
func (d *doc) render() string {
	var b bytes.Buffer
	d.emit(&b, d.root)
	return b.String()
}

func (d *doc) emit(b *bytes.Buffer, n *node) {
	if r, ok := d.repl[n]; ok {
		b.WriteString(r)
		return
	}
	if lead, ok := d.sorted[n]; ok {
		ms := make([]member, 0, len(n.members))
		for _, m := range n.members {
			if !d.drop[m.val] {
				ms = append(ms, m)
			}
		}
		var front, rest []member
		for _, k := range lead {
			for i, m := range ms {
				if m.key == k {
					front = append(front, m)
					ms = append(ms[:i:i], ms[i+1:]...)
					break
				}
			}
		}
		// Lead keys stay in front only if the sender wrote them first; otherwise they are sorted like the rest.
		broken := false
		if !leadsFirst(ms0(n.members, d.drop), front) {
			ms = append(ms, front...)
			front, broken = nil, true
		}
		rest = ms
		sort.SliceStable(rest, func(i, j int) bool { return rest[i].key < rest[j].key })
		b.WriteByte('{')
		if broken {
			// Not JSON: a marker so that a lead key that moved is a visible difference, not sorted away.
			b.WriteString("<lead-key-moved>")
		}
		for i, m := range append(front, rest...) {
			if i > 0 {
				b.WriteByte(',')
			}
			b.Write(d.src[m.keyStart:m.keyEnd])
			b.WriteByte(':')
			d.emit(b, m.val)
		}
		b.WriteByte('}')
		return
	}
	if d.compact[n] {
		b.WriteByte('[')
		for i, it := range n.items {
			if i > 0 {
				b.WriteByte(',')
			}
			d.emit(b, it)
		}
		b.WriteByte(']')
		return
	}
	pos := n.start
	switch n.kind {
	case 'o':
		for _, m := range n.members {
			b.Write(d.src[pos:m.val.start])
			d.emit(b, m.val)
			pos = m.val.end
		}
	case 'a':
		for _, it := range n.items {
			b.Write(d.src[pos:it.start])
			d.emit(b, it)
			pos = it.end
		}
	}
	b.Write(d.src[pos:n.end])
}

// leadsFirst reports whether the lead members are the first members of the object, in that order.
func leadsFirst(all, lead []member) bool {
	if len(lead) > len(all) {
		return false
	}
	for i, m := range lead {
		if all[i].key != m.key {
			return false
		}
	}
	return true
}

// ms0 is the members that are not dropped.
func ms0(all []member, drop map[*node]bool) []member {
	out := make([]member, 0, len(all))
	for _, m := range all {
		if !drop[m.val] {
			out = append(out, m)
		}
	}
	return out
}
