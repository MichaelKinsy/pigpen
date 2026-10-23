package frontend

import (
	"math"
	"slices"
	"strings"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/stencil-hq/tern-sdk/go/tern"
	"github.com/stencil-hq/tern-sdk/go/tern/ui"
)

// The dock and the overlays are small, so their structure is rendered whole
// with Surface.Render: the SDK diffs the view against the last one, with ids
// derived from keys (PiG's ids, so the editor is "dock.editor"), and routes
// Tern's events on a node to its handlers. A selector or the settings that
// take the editor's place in the dock draw as a sheet in the layer, over
// the transcript, after the overlays.
//
// A node's view is built when it is added, with every prop, and again only
// when its structure (its kind, children and fixed props) changes. The props
// that change while the structure stays travel as raw ops on the rendered
// nodes, like the transcript's: the editor's text and cursor, a picker's
// catalog, order, query and selection, the dock lines' text, the working
// indicator's label and tone, and the footer's texts, tones, meter and
// effort glyph. Their values in the view stay as the node was added, so the
// reconciler, which diffs only views, never sends them again, and an update
// that keeps the structure costs a few small ops and no render.

// entry is one dock or overlay node in PiG's order.
type entry struct {
	// id is PiG's id, and tid the node's TSP id.
	id, tid string
	node    fe.Node
	// view is the node's view as last built, its children built too, so a
	// render runs no builder, with the node's handlers. tree reports a
	// picker view laid out as a tree. unsent reports a fresh view no render
	// took yet, which a change builds again whole.
	view     tern.Node
	handlers tern.Handlers
	tree     bool
	unsent   bool
	// raw are the raw props last queued on the rendered node.
	raw rawProps
	// A sheet's state: the picker's catalog, and the prefs row whose
	// choice menu is open, or "".
	catalog pickerCatalog
	editing string
}

// rawProps are the props of a dock node sent as raw ops.
type rawProps struct {
	// text is the editor's, the dock lines' or a spinnerless indicator's
	// text, and cursor the editor's cursor.
	text   string
	cursor int
	// hot are the dock lines' or the spinner's other props.
	hot hot
	// A picker's rows and query.
	items           []any
	order, current  []string
	selected, query string
	searchable      bool
	footer          footerProps
}

// hot are a dock node's raw props besides a text node's text: a text
// sent under the name the node's kind gives it (text, label or level), a
// tone, whether the node hides, and a meter's value as float64 bits.
type hot struct {
	text     string
	tone     ui.Tone
	hidden   bool
	hasValue bool
	value    uint64
}

// setProps queues one set of the props in next that differ from old, with
// the text under textKey, and keeps next as sent. A prop at its zero value
// is removed.
func (s *session) setProps(id, textKey string, old *hot, next hot) {
	if *old == next {
		return
	}
	set := make(map[string]any, 2)
	if next.text != old.text {
		set[textKey] = nullable(next.text)
	}
	if next.tone != old.tone {
		set["tone"] = nullable(string(next.tone))
	}
	if next.hidden != old.hidden {
		set["hidden"] = nil
		if next.hidden {
			set["hidden"] = true
		}
	}
	if next.hasValue != old.hasValue || next.value != old.value {
		set["value"] = nil
		if next.hasValue {
			set["value"] = math.Float64frombits(next.value)
		}
	}
	s.queue(tern.OpSet(id, set))
	*old = next
}

// dropPending drops the raw ops still pending on node id and on its
// children: the render that deletes the node goes out before them, and Tern
// rejects an op on an unknown id. Another dock node whose id extends id is
// not a child.
func (s *session) dropPending(id string) {
	s.pending = slices.DeleteFunc(s.pending, func(op tern.Op) bool {
		if len(op) < 2 {
			return false
		}
		target, ok := op[1].(string)
		if !ok || target != id && !strings.HasPrefix(target, id+".") {
			return false
		}
		return target == id || !slices.ContainsFunc(s.dock, func(e *entry) bool { return e.tid == target })
	})
}

// applyDock applies an op on the dock, which a fullscreen overlay's screen
// mirrors.
func (s *session) applyDock(op fe.Op) {
	if s.screen != nil {
		s.screen.dirty = true
	}
	at := slices.IndexFunc(s.dock, func(e *entry) bool { return e.id == op.ID })
	if op.Kind == fe.Remove {
		if at >= 0 {
			e := s.dock[at]
			s.dock = slices.Delete(s.dock, at, at+1)
			s.dirty = true
			s.dockNodeChanged(e, e.node, nil)
			s.dropViewRules(e.tid)
		}
		return
	}
	if at < 0 {
		at = min(max(op.Index, 0), len(s.dock))
		s.dock = slices.Insert(s.dock, at, &entry{id: op.ID})
		s.dirty = true
	}
	e := s.dock[at]
	prev := e.node
	e.node = op.Node
	s.dockNodeChanged(e, prev, op.Node)
	s.rebuild(e, prev, false)
}

// kindOf numbers the node kinds of the dock and the layer; 0 is no node.
func kindOf(node fe.Node) int {
	switch node.(type) {
	case nil:
		return 0
	case fe.Editor:
		return 1
	case fe.Working:
		return 2
	case fe.Footer:
		return 3
	case fe.Lines:
		return 4
	case fe.Selector:
		return 5
	case fe.Settings:
		return 6
	case fe.Overlay:
		return 7
	}
	return -1
}

// dockNodeChanged starts and ends what a node kind brings along: the
// editor's actions act on the editor shown, a Working node reports terminal
// progress, a sheet's state lives as long as its kind, and the raw ops on a
// node removed or replaced by another kind are dropped.
func (s *session) dockNodeChanged(e *entry, prev, next fe.Node) {
	_, wasEditor := prev.(fe.Editor)
	if editor, ok := next.(fe.Editor); ok {
		s.editor, s.hasEditor = editor, true
	} else if wasEditor {
		s.editor, s.hasEditor = fe.Editor{}, false
	}
	_, wasWorking := prev.(fe.Working)
	_, isWorking := next.(fe.Working)
	if isWorking != wasWorking {
		s.setProgress(isWorking)
	}
	if kindOf(prev) == kindOf(next) {
		return
	}
	if prev != nil {
		s.dropPending(e.tid)
	}
	if next != nil {
		e.catalog, e.editing = pickerCatalog{}, ""
		e.handlers = s.handlers(e, next)
		e.tid = "dock." + e.id
		if isLayerNode(next) {
			e.tid = "layer." + e.id
		}
	}
}

// restructured reports whether next needs another view than prev, the node
// e's view was last made for: another kind, or other fixed props. rows are
// a selector's rows.
func restructured(e *entry, prev, next fe.Node, rows pickerRows) bool {
	switch n := next.(type) {
	case fe.Editor:
		p, ok := prev.(fe.Editor)
		return !ok || p.Placeholder != n.Placeholder || p.Sendable != n.Sendable
	case fe.Working:
		p, ok := prev.(fe.Working)
		return !ok || spinnerless(p) != spinnerless(n)
	case fe.Footer:
		p, ok := prev.(fe.Footer)
		return !ok || len(p.ExtensionStatuses) != len(n.ExtensionStatuses)
	case fe.Lines:
		p, ok := prev.(fe.Lines)
		if !ok {
			return true
		}
		view := validView(n.View, n.Lines)
		return view != validView(p.View, p.Lines) || view && (!sameView(p.View, n.View) || !slices.Equal(p.Lines, n.Lines))
	case fe.Selector:
		p, ok := prev.(fe.Selector)
		return !ok || rows.tree != e.tree || !samePickerShell(p, n)
	case fe.Settings:
		p, ok := prev.(fe.Settings)
		return !ok || !sameSettings(p, n)
	case fe.Overlay:
		p, ok := prev.(fe.Overlay)
		return !ok || !sameOverlay(p, n)
	}
	return true
}

// rebuild brings e up to date after its node changed from prev (nil for a
// new node), or after the inputs of its view did (restructure): a changed
// structure builds the view again for the next render, and the raw props
// queue what changed. A node the next render adds anew (new, or of another
// kind) is fresh: its view holds its props, and an editor or a sheet takes
// the keys: an editor so Tern shows its caret and takes IME composition
// there, a sheet from any field, so they reach PiG's selector. The editor's
// text and cursor and a picker's rows still follow as raw ops.
func (s *session) rebuild(e *entry, prev fe.Node, restructure bool) {
	var rows pickerRows
	sel, isSelector := e.node.(fe.Selector)
	if isSelector {
		rows = rowsOf(sel, &e.raw)
	}
	fresh := false
	if e.unsent || restructure || restructured(e, prev, e.node, rows) {
		e.tree = rows.tree
		view := buildNode(s.element(e, true))
		view.Handlers = e.handlers
		fresh = e.unsent || e.view.Kind != view.Kind
		if _, ok := e.node.(fe.Footer); ok && !fresh {
			// The segments kept show their raw props; their views stay
			// as they were added.
			view.Children = keepChildren(e.view.Children, view.Children)
		}
		e.view, e.unsent = view, fresh
		s.dirty = true
	}
	if fresh {
		s.dropPending(e.tid)
		e.raw = rawProps{}
	}
	switch n := e.node.(type) {
	case fe.Editor:
		s.queueEditor(e, n, fresh)
		if fresh {
			s.queue(tern.OpFocus(e.tid))
		}
	case fe.Working:
		s.queueWorking(e, n, fresh)
	case fe.Footer:
		p, _ := prev.(fe.Footer)
		s.queueFooter(e, p, n, fresh)
	case fe.Lines:
		s.queueLines(e, prev, n, fresh)
	case fe.Selector:
		s.queuePicker(e, sel, rows, fresh)
		if fresh {
			s.queue(tern.OpFocus(""))
		}
	case fe.Settings:
		if fresh {
			s.queue(tern.OpFocus(""))
		}
	}
}

// buildNode builds el and its children into nodes, which a render takes as
// they are.
func buildNode(el tern.Element) tern.Node {
	node := el.Node()
	if len(node.Children) > 0 {
		children := make([]tern.Element, len(node.Children))
		for i, child := range node.Children {
			children[i] = buildNode(child)
		}
		node.Children = children
	}
	return node
}

// keepChildren returns next's children with each one old has under the same
// key replaced by old's.
func keepChildren(old, next []tern.Element) []tern.Element {
	for i, child := range next {
		key := child.(tern.Node).Props["key"]
		for _, kept := range old {
			if kept.(tern.Node).Props["key"] == key {
				next[i] = kept
				break
			}
		}
	}
	return next
}

// applyOverlay applies an op on PiG's overlays.
func (s *session) applyOverlay(op fe.Op) {
	at := slices.IndexFunc(s.overlays, func(e *entry) bool { return e.id == op.ID })
	switch {
	case op.Kind == fe.Remove:
		if at >= 0 {
			s.dropViewRules(s.overlays[at].tid)
			s.overlays = slices.Delete(s.overlays, at, at+1)
			s.dirty = true
		}
		return
	case at < 0:
		at = min(max(op.Index, 0), len(s.overlays))
		s.overlays = slices.Insert(s.overlays, at, &entry{id: op.ID, tid: "layer." + op.ID})
	}
	e := s.overlays[at]
	prev := e.node
	e.node = op.Node
	s.rebuild(e, prev, false)
}

// view is the surface's view: main is an empty list, so the reconciler adds
// the region root and never touches the transcript's raw nodes under it.
func (s *session) view() tern.View {
	dock := make(tern.Nodes, 0, len(s.dock))
	layer := make(tern.Nodes, 0, len(s.overlays)+1)
	for _, e := range s.overlays {
		layer = append(layer, e.view)
	}
	for _, e := range s.dock {
		if isLayerNode(e.node) {
			layer = append(layer, e.view)
		} else {
			dock = append(dock, e.view)
		}
	}
	return tern.View{Main: tern.Nodes{}, Dock: dock, Layer: layer}
}

// setCols keeps the pane's width in cells and rebuilds the overlays, whose
// cards hold columns of it.
func (s *session) setCols(cols int) {
	if cols == s.cols {
		return
	}
	s.cols = cols
	for _, e := range s.overlays {
		s.rebuild(e, e.node, true)
	}
}

// element describes a node's view, without handlers. clicks reports whether
// the elements of the node's view take clicks, as PiG's clicks on the node.
func (s *session) element(e *entry, clicks bool) tern.Element {
	var node string
	if clicks {
		node = e.id
	}
	switch n := e.node.(type) {
	case fe.Editor:
		return ui.Editor{Common: ui.Common{Key: e.id}, Field: ui.Field{Placeholder: n.Placeholder, Sendable: n.Sendable}}
	case fe.Working:
		return workingElement(e.id, n)
	case fe.Footer:
		return footerElement(e.id, n)
	case fe.Selector:
		return pickerElement(e.id, n, e.tree)
	case fe.Settings:
		return prefsElement(e.id, n, e.editing)
	case fe.Overlay:
		var body tern.Element
		if validView(n.View, n.Lines) {
			body = s.viewElement("view", e.tid+".view", n.View, n.Lines, node)
		} else {
			s.dropViewRules(e.tid)
		}
		return overlayElement(e.id, n, s.cols, body)
	case fe.Lines:
		if validView(n.View, n.Lines) {
			return s.viewElement(e.id, e.tid, n.View, n.Lines, node)
		}
		s.dropViewRules(e.tid)
		text := linesText(n.Lines)
		return ui.Ansi{Common: ui.Common{Key: e.id, Hidden: blank(text)}, Text: text}
	}
	return ui.Ansi{Common: ui.Common{Key: e.id, Hidden: true}}
}

// handlers are a node's handlers, made once for its kind.
func (s *session) handlers(e *entry, node fe.Node) tern.Handlers {
	switch node.(type) {
	case fe.Editor:
		return s.editorHandlers(e)
	case fe.Selector:
		return s.pickerHandlers(e.id)
	case fe.Settings:
		return s.prefsHandlers(e.id)
	}
	return tern.Handlers{}
}

// queueLines queues a change of the dock lines' text and of whether they
// hide. Fresh lines' view holds them.
func (s *session) queueLines(e *entry, prev fe.Node, lines fe.Lines, fresh bool) {
	if validView(lines.View, lines.Lines) {
		// The view's elements hold the lines; a change renders them.
		return
	}
	if p, ok := prev.(fe.Lines); ok && !fresh && slices.Equal(p.Lines, lines.Lines) {
		return
	}
	text := linesText(lines.Lines)
	if fresh {
		e.raw.text, e.raw.hot = text, hot{hidden: blank(text)}
		return
	}
	s.patchText(e.tid, e.raw.text, text)
	e.raw.text = text
	s.setProps(e.tid, "text", &e.raw.hot, hot{hidden: blank(text)})
}

// queueEditor queues the editor's text and cursor. PiG's editor owns them:
// keys still reach PiG on the pty and come back as new text and cursor.
func (s *session) queueEditor(e *entry, editor fe.Editor, fresh bool) {
	if fresh {
		if editor.Text != "" {
			s.queue(tern.OpText(e.tid, "replace", editor.Text))
		}
	} else {
		s.patchText(e.tid, e.raw.text, editor.Text)
	}
	if fresh || editor.Cursor != e.raw.cursor {
		s.queue(tern.OpSet(e.tid, map[string]any{"cursor": editor.Cursor}))
	}
	e.raw.text, e.raw.cursor = editor.Text, editor.Cursor
}

// editorHandlers turn Tern's native editing, undo key and sends on the
// editor into PiG's editor actions. An edit computed against other text than
// the editor's is dropped. A click that asks for the keys gets them back,
// since the editor is the only field.
func (s *session) editorHandlers(e *entry) tern.Handlers {
	return tern.Handlers{
		OnEdit: func(ev *tern.Edit) {
			s.mu.Lock()
			current := s.hasEditor && ev.Len == utf16Len(s.editor.Text)
			s.mu.Unlock()
			if current && s.env.Edit != nil {
				s.env.Edit(fe.Edit{From: ev.From, To: ev.To, Text: ev.Text, Cursor: ev.Cursor, Len: ev.Len})
			}
		},
		OnUndo: func(*tern.Undo) {
			if s.env.Undo != nil {
				s.env.Undo()
			}
		},
		OnSend: func(ev *tern.Send) {
			if s.env.Send != nil {
				s.env.Send(ev.Text)
			}
		},
		OnFocus: func(*tern.Focus) {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.queue(tern.OpFocus(e.tid))
			_ = s.flushLocked()
		},
	}
}
