package ask_user_question

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/ask-user-question/internal/ui"
	tui "github.com/MichaelKinsy/pigpen/components/pitui"
)

// fakeUIHost is the Go form of the TS drivers' fake `ctx.ui` (driveCustom in factory.test.ts, driveWithListener in
// ask-user-question.listener.test.ts, ctxWithCustom in the execute tests): a `host` whose Custom runs a script
// against the REAL questionnaire component and resolves with the component's Done value, or the script's own.

// identityTheme is rpiv-test-utils makeTheme(): every styling function returns its text unchanged.
type identityTheme struct{}

func (identityTheme) Fg(_ string, text string) string  { return text }
func (identityTheme) Bg(_ string, text string) string  { return text }
func (identityTheme) Bold(text string) string          { return text }
func (identityTheme) Italic(text string) string        { return text }
func (identityTheme) Underline(text string) string     { return text }
func (identityTheme) Strikethrough(text string) string { return text }
func (identityTheme) Inverse(text string) string       { return text }

// fakeHandle is the TS FakeHandle: an overlay handle that is focused exactly while it is visible, unless
// the test overrides isFocused (another overlay on top).
type fakeHandle struct {
	hidden    bool
	isFocused func() bool
}

func (h *fakeHandle) SetHidden(hidden bool) { h.hidden = hidden }
func (h *fakeHandle) IsHidden() bool        { return h.hidden }
func (h *fakeHandle) IsFocused() bool {
	if h.isFocused != nil {
		return h.isFocused()
	}
	return !h.hidden
}

type notice struct{ Message, Level string }

type emission struct {
	Channel string
	Payload any
}

type dialogCall struct {
	Method  string
	Title   string
	Options []string
}

type dialogReply struct {
	Value string
	Ok    bool
}

// scriptComp is the component as the script sees it: Render and HandleInput of the real component, with the
// first Done result remembered as the custom() resolution.
type scriptComp struct {
	t     *testing.T
	f     *fakeUIHost
	inner sdk.RemoteComponent
}

func (c *scriptComp) render(width int) []string { return c.inner.Render(width) }

// in is `component.handleInput(data)`.
func (c *scriptComp) in(data string) {
	c.t.Helper()
	res, err := c.inner.HandleInput(data)
	if err != nil {
		c.t.Fatalf("HandleInput(%q): %v", data, err)
	}
	if res.Done {
		c.f.resolve(c.t, res.Value)
	}
}

type fakeUIHost struct {
	t           *testing.T
	writeConfig func(contents string) // writes ~/.config/rpiv-ask-user-question/config.json

	noUI         bool
	mode         string
	cols, rows   int
	rawInput     bool // OnTerminalInput supported
	handle       *fakeHandle
	script       func(c *scriptComp, done func(any))
	customResult any // what Custom resolves with when there is no script
	customNil    bool

	// recorded
	notices      []notice
	emissions    []emission
	dialogs      []dialogCall
	replies      []dialogReply
	selectPick   func(options []string) string // when set, Select answers with its result
	customCalls  int
	rawHandler   func(string) sdk.TerminalInputResult
	rawRemovals  int
	rawRegisters int
	renders      int // render requests the component made through its invalidation callback

	resolved bool
	value    any
}

func newFakeUI(t *testing.T) *fakeUIHost {
	t.Helper()
	// The collapse key comes from ~/.config; never read the developer's own.
	return &fakeUIHost{t: t, writeConfig: configHome(t), mode: "tui", cols: 120, rows: 24}
}

// jsonRoundTrip is what the SDK delivers from the host: a decoded JSON value.
func jsonRoundTrip(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %#v: %v", v, err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal %s: %v", b, err)
	}
	return out
}

func (f *fakeUIHost) resolve(t *testing.T, v any) {
	if f.resolved {
		return
	}
	f.resolved = true
	f.value = jsonRoundTrip(t, v)
}

func (f *fakeUIHost) HasUI() bool  { return !f.noUI }
func (f *fakeUIHost) Mode() string { return f.mode }
func (f *fakeUIHost) Width() int   { return f.cols }
func (f *fakeUIHost) Height() int  { return f.rows }
func (f *fakeUIHost) Theme() ui.Theme {
	return identityTheme{}
}
func (f *fakeUIHost) Keybindings() tui.Keybindings { return tui.NewKeybindings(nil) }
func (f *fakeUIHost) Emit(channel string, payload any) {
	f.emissions = append(f.emissions, emission{channel, payload})
}
func (f *fakeUIHost) Notify(message, level string) {
	f.notices = append(f.notices, notice{message, level})
}

func (f *fakeUIHost) nextReply() dialogReply {
	if len(f.replies) == 0 {
		return dialogReply{}
	}
	r := f.replies[0]
	f.replies = f.replies[1:]
	return r
}

func (f *fakeUIHost) Select(title string, options []string) (string, bool, error) {
	f.dialogs = append(f.dialogs, dialogCall{"select", title, options})
	if f.selectPick != nil {
		return f.selectPick(options), true, nil
	}
	r := f.nextReply()
	return r.Value, r.Ok, nil
}

func (f *fakeUIHost) Input(title, _ string) (string, bool, error) {
	f.dialogs = append(f.dialogs, dialogCall{"input", title, nil})
	r := f.nextReply()
	return r.Value, r.Ok, nil
}

func (f *fakeUIHost) OnTerminalInput(handler func(string) sdk.TerminalInputResult) (func(), bool) {
	if !f.rawInput {
		return nil, false
	}
	f.rawRegisters++
	f.rawHandler = handler
	return func() { f.rawRemovals++ }, true
}

// raw is the captured onTerminalInput listener invoked with one chunk.
func (f *fakeUIHost) raw(data string) sdk.TerminalInputResult {
	f.t.Helper()
	if f.rawHandler == nil {
		f.t.Fatal("no raw terminal input listener was registered")
	}
	return f.rawHandler(data)
}

func (f *fakeUIHost) Custom(component sdk.RemoteComponent, onHandle func(ui.OverlayHandle)) (any, error) {
	f.customCalls++
	if inv, ok := component.(sdk.RemoteComponentInvalidator); ok {
		inv.SetInvalidate(func() { f.renders++ })
	}
	if f.script == nil {
		if f.customNil {
			return nil, nil
		}
		return jsonRoundTrip(f.t, f.customResult), nil
	}
	if f.handle != nil && onHandle != nil {
		onHandle(f.handle)
	}
	f.resolved, f.value = false, nil
	f.script(&scriptComp{t: f.t, f: f, inner: component}, func(v any) { f.resolve(f.t, v) })
	if !f.resolved {
		return nil, nil
	}
	return f.value, nil
}

// run is `tool.execute(...)`: the text of the first content block and the details as the host receives them.
func (f *fakeUIHost) run(args map[string]any) (string, map[string]any) {
	f.t.Helper()
	res, err := (&app{}).run(f, args)
	if err != nil {
		f.t.Fatalf("run: %v", err)
	}
	tr, ok := res.(sdk.ToolResult)
	if !ok {
		f.t.Fatalf("run returned %T", res)
	}
	d, _ := jsonRoundTrip(f.t, tr.Details).(map[string]any)
	return tr.Content, d
}

// ---- argument builders ----

func askQ(question, header string, multi bool, options ...any) map[string]any {
	q := map[string]any{"question": question, "header": header, "options": options}
	if multi {
		q["multiSelect"] = true
	}
	return q
}

func labels(ls ...string) []any {
	out := make([]any, len(ls))
	for i, l := range ls {
		out[i] = map[string]any{"label": l}
	}
	return out
}

func askArgsOf(qs ...map[string]any) map[string]any {
	list := make([]any, len(qs))
	for i, q := range qs {
		list[i] = q
	}
	return map[string]any{"questions": list}
}

// ---- expectations (toMatchObject and friends) ----

// matchObject is vitest toMatchObject: every key of want is present in got and matches, recursively; arrays
// match element for element.
func matchObject(t *testing.T, got, want any, what string) {
	t.Helper()
	if msg := subsetMismatch(got, want, what); msg != "" {
		t.Fatal(msg)
	}
}

func subsetMismatch(got, want any, path string) string {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return fmt.Sprintf("%s: got %#v, want object %#v", path, got, want)
		}
		keys := make([]string, 0, len(w))
		for k := range w {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			gv, present := g[k]
			if !present {
				return fmt.Sprintf("%s.%s: missing in %#v", path, k, g)
			}
			if m := subsetMismatch(gv, w[k], path+"."+k); m != "" {
				return m
			}
		}
		return ""
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return fmt.Sprintf("%s: got %#v, want %#v", path, got, want)
		}
		for i := range w {
			if m := subsetMismatch(g[i], w[i], fmt.Sprintf("%s[%d]", path, i)); m != "" {
				return m
			}
		}
		return ""
	}
	if !reflect.DeepEqual(got, want) {
		return fmt.Sprintf("%s: got %#v, want %#v", path, got, want)
	}
	return ""
}

func answersOf(t *testing.T, details map[string]any) []any {
	t.Helper()
	a, ok := details["answers"].([]any)
	if !ok {
		t.Fatalf("details has no answers array: %#v", details)
	}
	return a
}

// answerAt is `details.answers[i]`.
func answerAt(t *testing.T, details map[string]any, i int) map[string]any {
	t.Helper()
	a := answersOf(t, details)
	if i >= len(a) {
		t.Fatalf("answers[%d] missing in %#v", i, a)
	}
	m, _ := a[i].(map[string]any)
	return m
}

func anyLine(lines []string, pred func(string) bool) bool {
	for _, l := range lines {
		if pred(l) {
			return true
		}
	}
	return false
}

func lineHas(sub string) func(string) bool {
	return func(l string) bool { return strings.Contains(l, sub) }
}

// Real key sequences from pi-tui.
const (
	keyEnter    = "\r"
	keyEsc      = "\x1b"
	keyDown     = "\x1b[B"
	keyUp       = "\x1b[A"
	keyTab      = "\t"
	keyShiftTab = "\x1b[Z"
	keySpace    = " "
	ctrlRBr     = "\x1d" // GS, what legacy terminals send for Ctrl+]
)

func typeText(c *scriptComp, s string) {
	for _, r := range s {
		c.in(string(r))
	}
}
