// Package frontend draws PiG's interactive mode in Tern through the Tern
// Surface Protocol (TSP). It is a Piglet frontend member: the Piglet Binary
// fuses it with build.frontend, and PiG falls back to its ANSI renderer
// whenever this member declines or fails.
package frontend

import (
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// helloTimeout is how long Tern has to answer the hello before PiG falls
// back, as the TSP handshake specifies for a program that opens its surface
// without waiting.
const helloTimeout = time.Second

const surfaceID = "pig"

// Tern lays an inline surface out in CSS pixels: the regions sit inside
// 16px of padding on each side, and the Reader chat style, the narrowest
// built-in skin, centers main's children on a 720px measure. Spine and
// Console are wider, so lines sized for Reader fit every skin.
const (
	regionPaddingPx = 16
	mainMeasurePx   = 720
	// defaultCellPx is the cell width until Tern reports one.
	defaultCellPx = 8
)

// Frontend is the member's factory, the symbol the Piglet build registers.
func Frontend() fe.Frontend { return member{} }

type member struct{}

// Open starts a session only when the process runs directly in a Tern pane:
// a multiplexer between them swallows TSP, and PIG_TUI_NATIVE=0 opts out.
func (member) Open(env fe.Env) (fe.Session, error) {
	if env.Getenv("PIG_TUI_NATIVE") == "0" || env.Getenv("TERM_PROGRAM") != "tern" {
		return nil, nil
	}
	for _, key := range []string{"TMUX", "STY", "ZELLIJ", "HERDR_ENV"} {
		if env.Getenv(key) != "" {
			return nil, nil
		}
	}
	s := newSession(env.Out, env.Fallback, time.AfterFunc)
	s.cols = env.Columns
	s.edit, s.undo, s.send = env.Edit, env.Undo, env.Send
	if err := s.start(env.Version); err != nil {
		return nil, err
	}
	return s, nil
}

type afterFunc func(time.Duration, func()) *time.Timer

// session keeps the surface document in step with PiG's frames. Every method
// runs on PiG's owner loop except the hello timer, which only calls fallback.
type session struct {
	out      io.Writer
	fallback func(string)

	mu       sync.Mutex
	timer    *time.Timer
	replied  bool
	fellBack bool
	credits  int
	seq      int
	acked    int
	pending  [][]any
	// applied reports that PiG applied its first frame, and inputReady that
	// PiG's input loop runs. Tern acknowledges no frame while the pane is
	// hidden, so startup sends exactly two frames: PiG's first tree, with the
	// regions, and after InputReady the next one, which folds in everything
	// startup changed and the editor turning sendable. An agent started in a
	// background tab then fits the credits and gets its prompt.
	applied, inputReady bool
	order               map[fe.Region][]string
	// sent is the last description sent for each node in a region, which
	// an update is patched against.
	sent      map[string]map[string]any
	writeErr  error
	afterFunc afterFunc
	// cols and cellPx are the pane's width in cells and a cell's width in
	// CSS pixels, from the hello reply and resize events.
	cols, cellPx int
	// edit, undo and send are PiG's editor actions, and editorID the TSP id
	// of the editor node they act on, or "" while the dock shows none.
	edit     func(fe.Edit)
	undo     func()
	send     func(text string)
	editorID string
}

func newSession(out io.Writer, fallback func(string), after afterFunc) *session {
	return &session{
		out:       out,
		fallback:  fallback,
		credits:   2,
		cellPx:    defaultCellPx,
		order:     map[fe.Region][]string{},
		sent:      map[string]map[string]any{},
		afterFunc: after,
	}
}

// start sends the hello and opens the surface. It queues the regions, which
// go out with PiG's first frame (see session.applied).
func (s *session) start(version string) error {
	hello := map[string]any{"q": "hello", "v": []int{1}, "app": "pig", "ver": version, "features": []string{"edit", "undo", "send"}}
	open := map[string]any{"id": surfaceID, "mode": "inline", "title": "pig", "role": "pig.session"}
	s.queue(
		[]any{"add", "main", surfaceID, nil, map[string]any{"id": "main", "k": "col"}},
		[]any{"add", "dock", surfaceID, nil, map[string]any{"id": "dock", "k": "col"}},
	)
	return s.write(apc("q", hello) + apc("o", open))
}

// InputReady starts the hello timeout: Tern answers within it once PiG reads
// input, and a reply that arrived during startup is already queued. It sends
// nothing: PiG's next frame carries the folded startup changes.
func (s *session) InputReady() {
	s.inputReady = true
	s.mu.Lock()
	replied := s.replied
	s.mu.Unlock()
	if replied || s.timer != nil {
		return
	}
	s.timer = s.afterFunc(helloTimeout, func() {
		s.mu.Lock()
		missed := !s.replied && !s.fellBack
		s.fellBack = s.fellBack || missed
		s.mu.Unlock()
		if missed {
			s.fallback("Tern did not answer the surface protocol hello")
		}
	})
}

type frame struct {
	Surface string  `json:"sf"`
	Seq     int     `json:"s"`
	Ops     [][]any `json:"ops"`
}

func apc(verb string, body any) string {
	data, err := json.Marshal(body)
	if err != nil {
		panic(fmt.Sprintf("tsp %s body: %v", verb, err))
	}
	return "\x1b_tsp;" + verb + ";" + string(data) + "\x1b\\"
}

func (s *session) write(data string) error {
	if s.writeErr != nil {
		return s.writeErr
	}
	_, s.writeErr = io.WriteString(s.out, data)
	return s.writeErr
}

// Apply converts PiG's ops into TSP ops and sends them when a credit is free.
func (s *session) Apply(frame fe.Frame) error {
	s.applied = true
	for _, op := range frame.Ops {
		s.queue(s.convert(op)...)
	}
	return s.flush()
}

// nodeID namespaces PiG's ids, which may equal the surface's region ids.
func nodeID(id string) string { return "n." + id }

func (s *session) convert(op fe.Op) [][]any {
	ids := s.order[op.Region]
	id := nodeID(op.ID)
	switch op.Kind {
	case fe.Remove:
		s.order[op.Region] = removeID(ids, op.Index)
		delete(s.sent, id)
		if id == s.editorID {
			s.editorID = ""
		}
		return [][]any{{"del", id}}
	case fe.Insert:
		var before any
		if op.Index < len(ids) {
			before = ids[op.Index]
		}
		s.order[op.Region] = insertID(ids, op.Index, id)
		node := tspNode(id, op.Node)
		s.sent[id] = node
		return s.focusEditor(op.Node, id, [][]any{{"add", id, string(op.Region), before, node}})
	default:
		node := tspNode(id, op.Node)
		prev := s.sent[id]
		s.sent[id] = node
		if ops, ok := patchNode(prev, node); ok {
			return ops
		}
		// A node whose kind or children change is replaced in place.
		var before any
		if op.Index+1 < len(ids) {
			before = ids[op.Index+1]
		}
		return s.focusEditor(op.Node, id, [][]any{{"del", id}, {"add", id, string(op.Region), before, node}})
	}
}

// focusEditor gives the keys to an editor node that ops add: Tern then shows
// its caret and takes IME composition there.
func (s *session) focusEditor(node fe.Node, id string, ops [][]any) [][]any {
	if _, ok := node.(fe.Editor); !ok {
		return ops
	}
	s.editorID = id
	return append(ops, []any{"focus", id})
}

// editorText is the text of the editor node last sent, which Tern computes
// native edits against.
func (s *session) editorText() string {
	props, _ := s.sent[s.editorID]["p"].(map[string]any)
	text, _ := props["text"].(string)
	return text
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

func insertID(ids []string, at int, id string) []string {
	ids = append(ids, "")
	copy(ids[at+1:], ids[at:])
	ids[at] = id
	return ids
}

func removeID(ids []string, at int) []string {
	return append(ids[:at], ids[at+1:]...)
}

// textKinds keep their primary text in the text prop, which the text op
// addresses.
var textKinds = map[any]bool{"ansi": true, "md": true, "text": true, "code": true, "editor": true}

// patchNode returns the ops that turn the sent node prev into next. It
// reports false when next must replace prev whole: its kind changed, or its
// children are not the same nodes. Primary text that only grew is appended,
// so Tern re-renders a streamed md node from its last open block on; any
// other text change replaces it. Other props change by set, a removed prop
// by a null.
func patchNode(prev, next map[string]any) ([][]any, bool) {
	if prev == nil || prev["k"] != next["k"] || prev["id"] != next["id"] {
		return nil, false
	}
	prevChildren, _ := prev["c"].([]any)
	nextChildren, _ := next["c"].([]any)
	if len(prevChildren) != len(nextChildren) {
		return nil, false
	}
	id := next["id"]
	prevProps, _ := prev["p"].(map[string]any)
	nextProps, _ := next["p"].(map[string]any)
	isText := textKinds[next["k"]]
	var ops [][]any
	if isText {
		old, _ := prevProps["text"].(string)
		text, _ := nextProps["text"].(string)
		switch {
		case text == old:
		case old != "" && strings.HasPrefix(text, old):
			ops = append(ops, []any{"text", id, "append", text[len(old):]})
		default:
			ops = append(ops, []any{"text", id, "replace", text})
		}
	}
	changed := map[string]any{}
	for key, value := range nextProps {
		if !(isText && key == "text") && !reflect.DeepEqual(prevProps[key], value) {
			changed[key] = value
		}
	}
	for key := range prevProps {
		if _, ok := nextProps[key]; !ok && !(isText && key == "text") {
			changed[key] = nil
		}
	}
	if len(changed) > 0 {
		ops = append(ops, []any{"set", id, changed})
	}
	for i, child := range nextChildren {
		childOps, ok := patchNode(prevChildren[i].(map[string]any), child.(map[string]any))
		if !ok {
			return nil, false
		}
		ops = append(ops, childOps...)
	}
	return ops, true
}

// queue appends ops, folding a text or set op into a pending one for the
// same node so a session nobody is watching does not accumulate every frame.
func (s *session) queue(ops ...[]any) {
	for _, op := range ops {
		if folded := s.fold(op); !folded {
			s.pending = append(s.pending, op)
		}
	}
}

// fold merges op into a pending op for the same node. A text op and a set
// never carry the same prop, so they commute: a text replace replaces the
// last pending text op, a text append extends it, and a set merges into the
// last pending set, across ops of the other kind. Any other op on the node
// ends the search.
func (s *session) fold(op []any) bool {
	kind, id := op[0], op[1]
	if kind != "set" && kind != "text" {
		return false
	}
	for i := len(s.pending) - 1; i >= 0; i-- {
		prev := s.pending[i]
		if prev[1] != id {
			continue
		}
		switch {
		case kind == "text" && prev[0] == "text":
			if op[2] == "append" {
				s.pending[i] = []any{"text", id, prev[2], prev[3].(string) + op[3].(string)}
			} else {
				s.pending[i] = op
			}
			return true
		case kind == "set" && prev[0] == "set":
			merged := map[string]any{}
			for key, value := range prev[2].(map[string]any) {
				merged[key] = value
			}
			for key, value := range op[2].(map[string]any) {
				merged[key] = value
			}
			s.pending[i] = []any{"set", id, merged}
			return true
		case kind == "text" && prev[0] == "set", kind == "set" && prev[0] == "text":
			continue
		}
		return false
	}
	return false
}

func (s *session) flush() error {
	s.mu.Lock()
	fellBack := s.fellBack
	s.mu.Unlock()
	if fellBack || !s.applied || (!s.inputReady && s.seq > 0) || len(s.pending) == 0 || s.seq-s.acked >= s.credits {
		return nil
	}
	s.seq++
	ops := s.pending
	s.pending = nil
	return s.write(apc("f", frame{Surface: surfaceID, Seq: s.seq, Ops: ops}))
}

// HandleInput takes Tern's replies and events, in APC framing or, on
// Windows, OSC 877 framing.
func (s *session) HandleInput(data string) bool {
	var body string
	switch {
	case strings.HasPrefix(data, "\x1b_tsp;"):
		body = strings.TrimSuffix(strings.TrimPrefix(data, "\x1b_tsp;"), "\x1b\\")
	case strings.HasPrefix(data, "\x1b]877;tsp;"):
		body = strings.TrimPrefix(data, "\x1b]877;tsp;")
		body = strings.TrimSuffix(strings.TrimSuffix(body, "\x07"), "\x1b\\")
	default:
		return false
	}
	verb, payload, ok := strings.Cut(body, ";")
	if !ok {
		return true
	}
	var message struct {
		Reply   string `json:"r"`
		Event   string `json:"ev"`
		Credits int    `json:"credits"`
		Seq     int    `json:"s"`
		Surface string `json:"sf"`
		Cols    int    `json:"cols"`
		Cell    struct {
			W int `json:"w"`
		} `json:"cell"`
		// The editor events' fields.
		ID     string `json:"id"`
		From   int    `json:"from"`
		To     int    `json:"to"`
		Text   string `json:"text"`
		Cursor int    `json:"cursor"`
		Len    int    `json:"len"`
	}
	if json.Unmarshal([]byte(payload), &message) != nil {
		return true
	}
	if (verb == "r" && message.Reply == "hello") || (verb == "e" && message.Event == "resize") {
		if message.Cols > 0 {
			s.cols = message.Cols
		}
		if message.Cell.W > 0 {
			s.cellPx = message.Cell.W
		}
	}
	switch {
	case verb == "r" && message.Reply == "hello":
		s.mu.Lock()
		s.replied = true
		s.mu.Unlock()
		if s.timer != nil {
			s.timer.Stop()
		}
		if message.Credits > 0 {
			s.credits = message.Credits
		}
		_ = s.flush()
	case verb == "e" && message.Event == "ack":
		s.acked = max(s.acked, message.Seq)
		_ = s.flush()
	case verb == "e" && message.Event == "gone" && message.Surface == surfaceID:
		s.mu.Lock()
		first := !s.fellBack
		s.fellBack = true
		s.mu.Unlock()
		if first {
			s.fallback("Tern closed the surface")
		}
	case verb == "e" && message.Surface == surfaceID && message.ID != "" && message.ID == s.editorID:
		s.editorEvent(message.Event, fe.Edit{From: message.From, To: message.To, Text: message.Text, Cursor: message.Cursor, Len: message.Len})
	}
	return true
}

// editorEvent hands Tern's editing events on the editor node to PiG: a
// native edit unless the text changed since Tern computed it, an undo, and a
// send, whose text is the edit's Text. A click that asks for the keys gets
// them back, since the editor is the only field.
func (s *session) editorEvent(event string, edit fe.Edit) {
	switch event {
	case "edit":
		if s.edit != nil && edit.Len == utf16Len(s.editorText()) {
			s.edit(edit)
		}
	case "undo":
		if s.undo != nil {
			s.undo()
		}
	case "send":
		if s.send != nil {
			s.send(edit.Text)
		}
	case "focus":
		s.queue([]any{"focus", s.editorID})
		_ = s.flush()
	}
}

// Columns sizes PiG's lines for Tern's layout: the dock spans the pane inside
// the region padding, and main fits the Reader measure.
func (s *session) Columns() (main, dock int) {
	if s.cols <= 0 {
		return 0, 0
	}
	padding := (2*regionPaddingPx + s.cellPx - 1) / s.cellPx
	dock = max(1, s.cols-padding)
	return min(dock, mainMeasurePx/s.cellPx), dock
}

// Close closes the surface. After a fallback it removes the surface, so the
// ANSI renderer's grid shows; otherwise the transcript stays until the
// shell's next prompt.
func (s *session) Close() error {
	if s.timer != nil {
		s.timer.Stop()
	}
	s.mu.Lock()
	keep := !s.fellBack
	s.mu.Unlock()
	if keep {
		s.acked = s.seq
		_ = s.flush()
	}
	return s.write(apc("x", map[string]any{"id": surfaceID, "keep": keep}))
}

// tspNode maps one PiG node to a TSP node.
func tspNode(id string, node fe.Node) map[string]any {
	switch n := node.(type) {
	case fe.Thinking:
		return thinkingNode(id, n)
	case fe.ToolCard:
		return toolNode(id, n)
	case fe.MarkdownText:
		return markdownNode(id, n)
	case fe.Editor:
		return editorNode(id, n)
	case fe.Lines:
		text := strings.Join(n.Lines, "\n")
		return map[string]any{"id": id, "k": "ansi", "p": map[string]any{"text": text, "hidden": blank(text)}}
	default:
		return map[string]any{"id": id, "k": "ansi", "p": map[string]any{"text": "", "hidden": true}}
	}
}

// editorNode draws PiG's input editor as a Tern editor. Keys still reach PiG
// on the pty and come back as new text and cursor; Tern's native selection,
// undo key and sends arrive as events.
func editorNode(id string, editor fe.Editor) map[string]any {
	return map[string]any{"id": id, "k": "editor", "p": map[string]any{
		"text":        editor.Text,
		"cursor":      editor.Cursor,
		"placeholder": editor.Placeholder,
		"sendable":    editor.Sendable,
	}}
}

// markdownNode draws assistant text as an md node in the transcript, where
// Tern's chat style lays it out, and a user message as an md node in a card
// with the user tone. While the text streams, md shows Tern's caret, and an
// update that only grew the source travels as a text append.
func markdownNode(id string, md fe.MarkdownText) map[string]any {
	if md.Role == fe.RoleUser {
		return map[string]any{"id": id, "k": "card", "p": map[string]any{"role": "pig.user", "tone": "user"}, "c": []any{
			map[string]any{"id": id + ".md", "k": "md", "p": map[string]any{"text": md.Text}},
		}}
	}
	return map[string]any{"id": id, "k": "md", "p": map[string]any{"text": md.Text, "stream": md.Streaming, "role": "pig.assistant"}}
}

// thinkingNode draws a thinking run as a muted collapsible card around its
// Markdown. PiG's thinking toggle sets the fold; a fold the user flips in
// Tern stays local until PiG's state changes.
func thinkingNode(id string, thinking fe.Thinking) map[string]any {
	props := map[string]any{"head": "Thinking", "role": "pig.thinking", "tone": "muted", "collapsible": true, "collapsed": thinking.Hidden}
	return map[string]any{"id": id, "k": "card", "p": props, "c": []any{
		map[string]any{"id": id + ".md", "k": "md", "p": map[string]any{"text": thinking.Text, "stream": thinking.Streaming}},
	}}
}

func toolNode(id string, tool fe.ToolCard) map[string]any {
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
		return map[string]any{"id": id, "k": "tool", "p": props, "c": []any{
			map[string]any{"id": id + ".diff", "k": "diff", "p": map[string]any{"text": tool.Diff.Text, "path": tool.Diff.Path}},
		}}
	}
	// A definition's rendering carries its styling (highlighted files);
	// an empty one falls back to the plain output.
	body := tool.Output
	if rendered := strings.Join(tool.Result, "\n"); !blank(rendered) {
		body = rendered
	}
	return map[string]any{"id": id, "k": "tool", "p": props, "c": []any{
		map[string]any{"id": id + ".out", "k": "ansi", "p": map[string]any{"text": body}},
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

// blank reports whether ANSI text shows nothing: PiG spacers become hidden
// nodes, because Tern owns the spacing between blocks.
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
