package frontend

import (
	"fmt"
	"maps"
	"reflect"
	"strings"
	"unicode/utf16"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/stencil-hq/tern-sdk/go/tern"
)

// The transcript lives in main as nodes the member adds, patches and
// deletes with raw frame ops, under the region root that the rendered view
// creates and leaves alone (its view's main is an empty list). Ids are
// PiG's, namespaced by "n.", which keeps them apart from the region ids
// and from the ids the reconciler derives in the dock and the layer.

// nodeID namespaces PiG's ids, which may equal the surface's region ids.
func nodeID(id string) string { return "n." + id }

// mainEntry is a transcript node as last sent: its TSP id, boxed once for
// the ops that name it, and PiG's node, which an update is compared with.
// It holds no copy of the node's text.
type mainEntry struct {
	id   string
	ref  any
	node fe.Node
}

// convertMain queues the raw ops for an op on a node of main.
func (s *session) convertMain(op fe.Op) {
	switch op.Kind {
	case fe.Remove:
		if e := s.sent[op.ID]; e != nil {
			delete(s.sent, op.ID)
			s.order = removeID(s.order, op.Index)
			s.queue(tern.OpDel(e.id))
		}
	case fe.Insert:
		id := nodeID(op.ID)
		before := ""
		if op.Index < len(s.order) {
			before = s.order[op.Index]
		}
		s.order = insertID(s.order, op.Index, id)
		s.sent[op.ID] = &mainEntry{id: id, ref: id, node: op.Node}
		s.queue(tern.OpAdd("main", before, mainNode(id, op.Node)))
	default:
		e := s.sent[op.ID]
		if e == nil {
			return
		}
		prev := e.node
		e.node = op.Node
		if s.patchMain(e, prev, op.Node) {
			return
		}
		// A node whose kind or children change is replaced in place.
		before := ""
		if op.Index+1 < len(s.order) {
			before = s.order[op.Index+1]
		}
		s.queue(tern.OpDel(e.id), tern.OpAdd("main", before, mainNode(e.id, op.Node)))
	}
}

// patchMain queues the ops that turn the sent node prev into next, and
// reports false when next must replace it. Streamed text, the hot path,
// is compared field by field, without describing either node; other
// nodes are described and patched as TSP nodes.
func (s *session) patchMain(e *mainEntry, prev, next fe.Node) bool {
	id := e.id
	switch n := next.(type) {
	case fe.MarkdownText:
		p, ok := prev.(fe.MarkdownText)
		if !ok || p.Role != n.Role {
			return false
		}
		if n.Role == fe.RoleUser {
			s.patchText(id+".md", p.Text, n.Text)
			return true
		}
		s.patchText(e.ref, p.Text, n.Text)
		if p.Streaming != n.Streaming {
			s.queue(tern.OpSet(id, map[string]any{"stream": n.Streaming}))
		}
		return true
	case fe.Thinking:
		p, ok := prev.(fe.Thinking)
		if !ok {
			return false
		}
		if p.Hidden != n.Hidden {
			s.queue(tern.OpSet(id, map[string]any{"collapsed": n.Hidden}))
		}
		if p.Text != n.Text || p.Streaming != n.Streaming {
			child := id + ".md"
			s.patchText(child, p.Text, n.Text)
			if p.Streaming != n.Streaming {
				s.queue(tern.OpSet(child, map[string]any{"stream": n.Streaming}))
			}
		}
		return true
	}
	ops, ok := patchNode(mainNode(id, prev), mainNode(id, next))
	s.queue(ops...)
	return ok
}

// patchText queues the text op that turns old into text: an append when
// the text only grew, so Tern re-renders a streamed md node from its last
// open block on, and a replace otherwise. id is the node's TSP id.
func (s *session) patchText(id any, old, text string) {
	switch {
	case text == old:
	case old != "" && strings.HasPrefix(text, old):
		s.queue(tern.Op{"text", id, "append", text[len(old):]})
	default:
		s.queue(tern.Op{"text", id, "replace", text})
	}
}

func insertID(ids []string, at int, id string) []string {
	ids = append(ids, "")
	copy(ids[at+1:], ids[at:])
	ids[at] = id
	return ids
}

func removeID(ids []string, at int) []string {
	return append(ids[:at], ids[at+1:]...)
}

// patchNode returns the ops that turn the sent node prev into next. It
// reports false when next must replace prev whole: its kind changed, or its
// children are not the same nodes. Primary text changes as patchText says;
// other props change by set, a removed prop by a null.
func patchNode(prev, next tern.WireNode) ([]tern.Op, bool) {
	if prev.K != next.K || prev.ID != next.ID || len(prev.C) != len(next.C) {
		return nil, false
	}
	isText := tern.IsTextKind(next.K)
	var ops []tern.Op
	if isText {
		old, _ := prev.P["text"].(string)
		text, _ := next.P["text"].(string)
		switch {
		case text == old:
		case old != "" && strings.HasPrefix(text, old):
			ops = append(ops, tern.OpText(next.ID, "append", text[len(old):]))
		default:
			ops = append(ops, tern.OpText(next.ID, "replace", text))
		}
	}
	var changed map[string]any
	set := func(key string, value any) {
		if changed == nil {
			changed = map[string]any{}
		}
		changed[key] = value
	}
	for key, value := range next.P {
		if !(isText && key == "text") && !reflect.DeepEqual(prev.P[key], value) {
			set(key, value)
		}
	}
	for key := range prev.P {
		if _, ok := next.P[key]; !ok && !(isText && key == "text") {
			set(key, nil)
		}
	}
	if changed != nil {
		ops = append(ops, tern.OpSet(next.ID, changed))
	}
	for i, child := range next.C {
		childOps, ok := patchNode(prev.C[i], child)
		if !ok {
			return nil, false
		}
		ops = append(ops, childOps...)
	}
	return ops, true
}

// queue appends raw ops for the SDK, folding a text or set op into a
// pending one for the same node, so a pane nobody is watching does not
// accumulate every frame.
func (s *session) queue(ops ...tern.Op) {
	for _, op := range ops {
		if !s.fold(op) {
			s.pending = append(s.pending, op)
		}
	}
}

// fold merges op into a pending op for the same node. A text op and a set
// never carry the same prop, so they commute: a text replace replaces the
// last pending text op, a text append extends it, and a set merges into the
// last pending set, across ops of the other kind. Any other op on the node
// ends the search.
func (s *session) fold(op tern.Op) bool {
	kind, id := op[0], op[1]
	if kind != "set" && kind != "text" {
		return false
	}
	for i := len(s.pending) - 1; i >= 0; i-- {
		prev := s.pending[i]
		if len(prev) < 2 || prev[1] != id {
			continue
		}
		switch {
		case kind == "text" && prev[0] == "text":
			if op[2] == "append" {
				s.pending[i] = tern.OpText(id.(string), prev[2].(string), prev[3].(string)+op[3].(string))
			} else {
				s.pending[i] = op
			}
			return true
		case kind == "set" && prev[0] == "set":
			merged := maps.Clone(prev[2].(map[string]any))
			maps.Copy(merged, op[2].(map[string]any))
			s.pending[i] = tern.OpSet(id.(string), merged)
			return true
		case kind == "text" && prev[0] == "set", kind == "set" && prev[0] == "text":
			continue
		}
		return false
	}
	return false
}

// utf16Len is the length of text in UTF-16 code units, the unit of TSP
// offsets.
func utf16Len(text string) int {
	n := 0
	for _, r := range text {
		n += utf16.RuneLen(r)
	}
	return n
}

// mainNode maps one transcript node to a TSP node. The transcript is
// described as raw nodes rather than with tern/ui builders: a builder
// round-trips its props through JSON, which a streamed message would pay
// for its whole text on every token.
func mainNode(id string, node fe.Node) tern.WireNode {
	switch n := node.(type) {
	case fe.Thinking:
		return thinkingNode(id, n)
	case fe.ToolCard:
		return toolNode(id, n)
	case fe.MarkdownText:
		return markdownNode(id, n)
	case fe.Lines:
		return linesNode(id, n.Lines)
	default:
		return tern.WireNode{ID: id, K: tern.KindAnsi, P: map[string]any{"text": "", "hidden": true}}
	}
}

// linesNode draws ANSI lines; PiG spacers become hidden nodes, because Tern
// owns the spacing between blocks.
func linesNode(id string, lines []string) tern.WireNode {
	text := strings.Join(lines, "\n")
	return tern.WireNode{ID: id, K: tern.KindAnsi, P: map[string]any{"text": text, "hidden": blank(text)}}
}

// markdownNode draws assistant text as an md node in the transcript, where
// Tern's chat style lays it out, and a user message as an md node in a card
// with the user tone. While the text streams, md shows Tern's caret, and an
// update that only grew the source travels as a text append.
func markdownNode(id string, md fe.MarkdownText) tern.WireNode {
	if md.Role == fe.RoleUser {
		return tern.WireNode{ID: id, K: tern.KindCard, P: map[string]any{"role": "pig.user", "tone": "user"}, C: []tern.WireNode{
			{ID: id + ".md", K: tern.KindMd, P: map[string]any{"text": md.Text}},
		}}
	}
	return tern.WireNode{ID: id, K: tern.KindMd, P: map[string]any{"text": md.Text, "stream": md.Streaming, "role": "pig.assistant"}}
}

// thinkingNode draws a thinking run as a muted collapsible card around its
// Markdown. PiG's thinking toggle sets the fold; a fold the user flips in
// Tern stays local until PiG's state changes.
func thinkingNode(id string, thinking fe.Thinking) tern.WireNode {
	props := map[string]any{"head": "Thinking", "role": "pig.thinking", "tone": "muted", "collapsible": true, "collapsed": thinking.Hidden}
	return tern.WireNode{ID: id, K: tern.KindCard, P: props, C: []tern.WireNode{
		{ID: id + ".md", K: tern.KindMd, P: map[string]any{"text": thinking.Text, "stream": thinking.Streaming}},
	}}
}

func toolNode(id string, tool fe.ToolCard) tern.WireNode {
	title := tool.Name
	if title != "" {
		title = strings.ToUpper(title[:1]) + title[1:]
	}
	target, kind := toolTarget(tool)
	props := map[string]any{
		"name": tool.Name, "title": title, "target": target, "targetKind": kind,
		"status": string(tool.Status), "collapsible": true, "collapsed": !tool.Expanded,
		"preview": map[string]any{"tail": 5},
	}
	if tool.Elapsed > 0 {
		props["took"] = tool.Elapsed.Milliseconds()
	}
	// A result that carries a diff draws it natively, with its stat as meta.
	if tool.Diff != nil {
		props["meta"] = []any{diffStat(tool.Diff.Text)}
		return tern.WireNode{ID: id, K: tern.KindTool, P: props, C: []tern.WireNode{
			{ID: id + ".diff", K: tern.KindDiff, P: map[string]any{"text": tool.Diff.Text, "path": tool.Diff.Path}},
		}}
	}
	// A definition's rendering carries its styling (highlighted files);
	// an empty one falls back to the plain output.
	body := tool.Output
	if rendered := strings.Join(tool.Result, "\n"); !blank(rendered) {
		body = rendered
	}
	return tern.WireNode{ID: id, K: tern.KindTool, P: props, C: []tern.WireNode{
		{ID: id + ".out", K: tern.KindAnsi, P: map[string]any{"text": body}},
	}}
}

// diffStat counts a unified diff's added and removed lines as "+N −M", which
// Tern's tool meta colors like a diff stat. Only lines after the first hunk
// header count, so file headers do not.
func diffStat(diff string) string {
	var added, removed int
	inHunk := false
	for line := range strings.SplitSeq(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "@@"):
			inHunk = true
		case !inHunk:
		case strings.HasPrefix(line, "+"):
			added++
		case strings.HasPrefix(line, "-"):
			removed++
		}
	}
	return fmt.Sprintf("+%d −%d", added, removed)
}

// toolTarget picks the argument Tern draws as the tool's target: a command,
// a search pattern (which says more than its root), or a path. Other tools
// show PiG's header text.
func toolTarget(tool fe.ToolCard) (target, kind string) {
	for _, candidate := range []struct{ key, kind string }{
		{"command", "command"}, {"pattern", "pattern"}, {"path", "path"}, {"file_path", "path"},
	} {
		if value, ok := tool.Arguments[candidate.key].(string); ok && value != "" {
			return value, candidate.kind
		}
	}
	return tool.Header, "text"
}

// blank reports whether ANSI text shows nothing.
func blank(text string) bool {
	inEscape := false
	for _, r := range text {
		switch {
		case inEscape:
			if r >= 0x40 && r <= 0x7e && r != '[' {
				inEscape = false
			}
		case r == 0x1b:
			inEscape = true
		case r != ' ' && r != '\n' && r != '\t':
			return false
		}
	}
	return true
}
