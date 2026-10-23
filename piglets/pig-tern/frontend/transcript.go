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
//
// A node that can no longer change is settled: Tern may then flatten it,
// and under its retention budget (64 MiB of document state per pane) it
// drops the oldest settled children of main, with a gone event. A patch on
// a settled node clears the hint, so the node is settled again after it.
// An evicted node gets no more ops: Tern would reject them as unknown.

// nodeID namespaces PiG's ids, which may equal the surface's region ids.
func nodeID(id string) string { return "n." + id }

// mainEntry is a transcript node as last sent: its TSP id, boxed once for
// the ops that name it, and PiG's node, which an update is compared with.
// It holds no copy of the node's text. settled reports that its last op
// was a settle, and gone that Tern evicted it.
type mainEntry struct {
	id            string
	ref           any
	node          fe.Node
	settled, gone bool
}

// convertMain queues the raw ops for an op on a node of main.
func (s *session) convertMain(op fe.Op) {
	switch op.Kind {
	case fe.Remove:
		if e := s.sent[op.ID]; e != nil {
			delete(s.sent, op.ID)
			s.order = removeID(s.order, op.Index)
			s.dropViewRules(e.id)
			if !e.gone {
				s.queue(tern.OpDel(e.id))
			}
		}
	case fe.Insert:
		id := nodeID(op.ID)
		before := s.liveFrom(op.Index)
		s.order = insertID(s.order, op.Index, id)
		e := &mainEntry{id: id, ref: id, node: op.Node}
		s.sent[op.ID] = e
		s.queue(tern.OpAdd("main", before, s.mainNode(id, op.Node)))
		s.settle(e, op.Index)
		// Lines settle once a node follows them.
		if op.Index > 0 {
			if above := s.sent[s.order[op.Index-1][len("n."):]]; above != nil {
				s.settle(above, op.Index-1)
			}
		}
	default:
		e := s.sent[op.ID]
		if e == nil {
			return
		}
		prev := e.node
		e.node = op.Node
		if e.gone {
			return
		}
		queued := s.queued
		if !s.patchMain(e, prev, op.Node) {
			// A node whose kind or children change is replaced in place.
			s.queue(tern.OpDel(e.id), tern.OpAdd("main", s.liveFrom(op.Index+1), s.mainNode(e.id, op.Node)))
		}
		if s.queued != queued {
			e.settled = false
		}
		s.settle(e, op.Index)
	}
}

// liveFrom is the TSP id of the first node at or after index at in main
// that Tern still has, the sibling to insert before, or "" for the end.
func (s *session) liveFrom(at int) string {
	for _, id := range s.order[min(at, len(s.order)):] {
		if e := s.sent[id[len("n."):]]; e == nil || !e.gone {
			return id
		}
	}
	return ""
}

// settle queues a settle for e, at index at in main, once its node can no
// longer change and it is not settled yet.
func (s *session) settle(e *mainEntry, at int) {
	if e.settled || e.gone || !final(e.node, at == len(s.order)-1) {
		return
	}
	e.settled = true
	s.queue(tern.OpSettle(e.id))
}

// final reports whether PiG is done with a transcript node: a message or a
// thinking run that stopped streaming, a tool call that ended, and lines
// another node follows (PiG redraws the last ones, such as a loader). PiG
// may still change a final node (a fold, a late result, a new width); the
// patch then clears the hint and the node settles again.
func final(node fe.Node, last bool) bool {
	switch n := node.(type) {
	case fe.MarkdownText:
		return !n.Streaming
	case fe.Thinking:
		return !n.Streaming
	case fe.ToolCard:
		return n.Status == fe.ToolDone || n.Status == fe.ToolError || n.Status == fe.ToolCancelled
	default:
		return !last
	}
}

// evict forgets the transcript nodes Tern dropped under its retention
// budget.
func (s *session) evict(ids []string) {
	for _, id := range ids {
		if pigID, ok := strings.CutPrefix(id, "n."); ok {
			if e := s.sent[pigID]; e != nil && e.id == id {
				e.gone = true
				s.dropViewRules(id)
			}
		}
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
	ops, ok := patchNode(s.mainNode(id, prev), s.mainNode(id, next))
	s.queue(ops...)
	return ok
}

// patchText queues the text op that turns old into text: an append when
// the text only grew, so Tern re-renders a streamed md node from its last
// open block on, and a replace otherwise. id is the node's TSP id.
//
// An append folds into a text op still pending on the node without copying
// the text: that op ends with old (it appends old's tail or replaces with
// old), so the folded op appends text's tail from the same offset, or
// replaces with text. A stream held back while Tern acknowledges nothing,
// as in a hidden pane, then costs one small op per token, not a copy of
// all it held.
func (s *session) patchText(id any, old, text string) {
	switch {
	case text == old:
	case old != "" && strings.HasPrefix(text, old):
		s.queued++
		if i := s.foldable(id, "text"); i >= 0 {
			if prev := s.pending[i]; prev[2] == "append" {
				s.pending[i] = tern.Op{"text", id, "append", text[len(old)-len(prev[3].(string)):]}
			} else {
				s.pending[i] = tern.Op{"text", id, "replace", text}
			}
			return
		}
		s.pending = append(s.pending, tern.Op{"text", id, "append", text[len(old):]})
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
// accumulate every frame. queued counts the ops queued.
func (s *session) queue(ops ...tern.Op) {
	s.queued += len(ops)
	for _, op := range ops {
		if !s.fold(op) {
			s.pending = append(s.pending, op)
		}
	}
}

// fold merges op into a pending op for the same node: a text replace
// replaces the pending text op, a text append extends it, and a set merges
// into the pending set.
func (s *session) fold(op tern.Op) bool {
	kind, _ := op[0].(string)
	if kind != "set" && kind != "text" {
		return false
	}
	i := s.foldable(op[1], kind)
	if i < 0 {
		return false
	}
	prev := s.pending[i]
	switch {
	case kind == "set":
		merged := maps.Clone(prev[2].(map[string]any))
		maps.Copy(merged, op[2].(map[string]any))
		s.pending[i] = tern.OpSet(op[1].(string), merged)
	case op[2] == "append":
		s.pending[i] = tern.OpText(op[1].(string), prev[2].(string), prev[3].(string)+op[3].(string))
	default:
		s.pending[i] = op
	}
	return true
}

// foldable returns the index of the pending op of kind ("text" or "set") on
// node id that a new one folds into, or -1: the last one, past pending ops
// of the other kind, since a text op and a set never carry the same prop
// and so commute. Any other op on the node ends the search, such as a
// settle: a later change then follows it, and the node settles again after
// the change.
func (s *session) foldable(id any, kind string) int {
	for i := len(s.pending) - 1; i >= 0; i-- {
		prev := s.pending[i]
		if len(prev) < 2 || prev[1] != id {
			continue
		}
		switch prev[0] {
		case kind:
			return i
		case "text", "set":
			continue
		}
		return -1
	}
	return -1
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
// for its whole text on every token. Lines and a tool's result that an
// extension's components drew draw as their view.
func (s *session) mainNode(id string, node fe.Node) tern.WireNode {
	switch n := node.(type) {
	case fe.Thinking:
		return thinkingNode(id, n)
	case fe.ToolCard:
		return s.toolNode(id, n)
	case fe.MarkdownText:
		return markdownNode(id, n)
	case fe.Lines:
		if n.Click != nil {
			return clickLinesNode(id, n.Lines, *n.Click)
		}
		if validView(n.View, n.Lines) {
			return wireNode(id, s.viewElement("", id, n.View, n.Lines, ""))
		}
		s.dropViewRules(id)
		return linesNode(id, n.Lines)
	default:
		return tern.WireNode{ID: id, K: tern.KindAnsi, P: map[string]any{"text": "", "hidden": true}}
	}
}

// linesNode draws ANSI lines; PiG spacers become hidden nodes, because Tern
// owns the spacing between blocks.
func linesNode(id string, lines []string) tern.WireNode {
	text := linesText(lines)
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

func (s *session) toolNode(id string, tool fe.ToolCard) tern.WireNode {
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
		s.dropViewRules(id)
		return tern.WireNode{ID: id, K: tern.KindTool, P: props, C: []tern.WireNode{
			{ID: id + ".diff", K: tern.KindDiff, P: map[string]any{"text": tool.Diff.Text, "path": tool.Diff.Path}},
		}}
	}
	// A definition's rendering carries its styling (highlighted files), or
	// is its renderer's view; an empty one falls back to the plain output.
	if validView(tool.ResultView, tool.Result) && len(tool.Result) > 0 {
		view := id + ".view"
		return tern.WireNode{ID: id, K: tern.KindTool, P: props, C: []tern.WireNode{
			wireNode(view, s.viewElement("", view, tool.ResultView, tool.Result, "")),
		}}
	}
	s.dropViewRules(id)
	body := tool.Output
	if rendered := linesText(tool.Result); !blank(rendered) {
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

// linesText is the text of an ansi node drawing PiG's lines: the lines
// joined, each without its padding. PiG pads a line with spaces to the
// columns it renders at, and Tern's ansi block wraps at its own width
// (https://docs.stencil.so/tern/elements/text.md#ansi), so padding that
// draws nothing would still wrap into a blank row wherever the block is a
// little narrower than those columns.
//
// Padding is the spaces after a line's last cell that draws something. A
// space draws when a background, reverse video, underline, strikethrough
// or overline is on, so those stay. The SGR state carries over from line
// to line, as in a terminal, and the escape sequences among the padding
// stay, so every line starts in the state it did.
func linesText(lines []string) string {
	var attrs sgrAttrs
	padded := false
	for _, line := range lines {
		end := paddingStart(line, &attrs)
		if strings.IndexByte(line[end:], ' ') >= 0 {
			padded = true
			break
		}
	}
	if !padded {
		return strings.Join(lines, "\n")
	}
	n := len(lines) - 1
	for _, line := range lines {
		n += len(line)
	}
	var b strings.Builder
	b.Grow(n)
	attrs = 0
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		end := paddingStart(line, &attrs)
		b.WriteString(line[:end])
		// The padding's escape sequences, without its spaces.
		tail := line[end:]
		for j := 0; j < len(tail); {
			if tail[j] != 0x1b {
				j++
				continue
			}
			k := escapeLen(tail[j:])
			b.WriteString(tail[j : j+k])
			j += k
		}
	}
	return b.String()
}

// paddingStart is the byte offset in line after its last cell that draws:
// what follows is spaces that draw nothing and escape sequences. attrs is
// the SGR state the line starts in, and becomes the one it ends in.
func paddingStart(line string, attrs *sgrAttrs) int {
	end := 0
	for i := 0; i < len(line); {
		switch c := line[i]; {
		case c == 0x1b:
			n := escapeLen(line[i:])
			if n >= 3 && line[i+1] == '[' && line[i+n-1] == 'm' {
				attrs.apply(line[i+2 : i+n-1])
			}
			i += n
		case c == ' ':
			i++
			if *attrs != 0 {
				end = i
			}
		default:
			// Any other byte draws, or (a control) may move the cursor; the
			// bytes of a multibyte rune are never a space or an escape.
			i++
			end = i
		}
	}
	return end
}

// sgrAttrs are the SGR attributes under which a space draws.
type sgrAttrs uint8

const (
	sgrBackground sgrAttrs = 1 << iota
	sgrReverse
	sgrUnderline
	sgrStrike
	sgrOverline
)

// apply applies an SGR sequence's parameters (between "ESC [" and "m").
// A color in the ";" form takes its arguments from the parameters that
// follow (5;index or 2;r;g;b); in the ":" form they are in its parameter.
func (a *sgrAttrs) apply(params string) {
	form, args := false, 0
	for param := range strings.SplitSeq(params, ";") {
		switch {
		case form:
			form = false
			switch param {
			case "5":
				args = 1
			case "2":
				args = 3
			}
			continue
		case args > 0:
			args--
			continue
		}
		code, sub, colon := strings.Cut(param, ":")
		switch n := sgrCode(code); {
		case n == 0:
			*a = 0
		case n == 4:
			a.set(sgrUnderline, sub != "0")
		case n == 21:
			*a |= sgrUnderline
		case n == 24:
			*a &^= sgrUnderline
		case n == 7:
			*a |= sgrReverse
		case n == 27:
			*a &^= sgrReverse
		case n == 9:
			*a |= sgrStrike
		case n == 29:
			*a &^= sgrStrike
		case n == 53:
			*a |= sgrOverline
		case n == 55:
			*a &^= sgrOverline
		case n >= 40 && n <= 47, n >= 100 && n <= 107:
			*a |= sgrBackground
		case n == 48:
			*a |= sgrBackground
			form = !colon
		case n == 49:
			*a &^= sgrBackground
		case n == 38, n == 58:
			form = !colon
		}
	}
}

// sgrCode is an SGR parameter's number, 0 when empty, or -1 when it is not
// a number.
func sgrCode(s string) int {
	n := 0
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' || n > 1000 {
			return -1
		}
		n = n*10 + int(s[i]-'0')
	}
	return n
}

func (a *sgrAttrs) set(attr sgrAttrs, on bool) {
	if on {
		*a |= attr
	} else {
		*a &^= attr
	}
}
