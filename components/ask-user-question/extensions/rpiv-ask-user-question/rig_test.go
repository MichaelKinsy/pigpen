package ask_user_question_test

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	ask "github.com/MichaelKinsy/pigpen/ask-user-question"
)

// rig is a fake host for the questionnaire: it answers the dialogs from a script and records the dialogs,
// the events the extension emits and the tool-list changes.
type rig struct {
	*Host
	mu      sync.Mutex
	script  []reply // answers to select and input dialogs, in the order they are asked
	dialogs []dialog
	events  []emitted
	active  []string
	sets    [][]string
	failUI  bool
}

type reply struct {
	Value string
	Ok    bool
}

type dialog struct {
	Method      string
	Title       string
	Options     []string
	Placeholder string
}

type emitted struct {
	Channel string
	Payload map[string]any
}

func pick(v string) reply { return reply{Value: v, Ok: true} }
func dismiss() reply      { return reply{} }

func startRig(t *testing.T, opts HostOptions, script ...reply) *rig {
	t.Helper()
	r := &rig{script: script}
	opts.OnCallValue = func(method string, args map[string]any) (any, string) {
		r.mu.Lock()
		defer r.mu.Unlock()
		switch method {
		case "ui.select", "ui.input":
			d := dialog{Method: method, Title: str(args["title"]), Placeholder: str(args["placeholder"])}
			if raw, ok := args["options"].([]any); ok {
				for _, o := range raw {
					d.Options = append(d.Options, str(o))
				}
			}
			r.dialogs = append(r.dialogs, d)
			if r.failUI {
				return nil, "dialog failed"
			}
			if len(r.script) == 0 {
				t.Errorf("unscripted dialog %s %q", method, d.Title)
				return map[string]any{}, ""
			}
			next := r.script[0]
			r.script = r.script[1:]
			if method == "ui.select" {
				return map[string]any{"selected": next.Value, "ok": next.Ok}, ""
			}
			return map[string]any{"text": next.Value, "ok": next.Ok}, ""
		case "events.emit":
			var payload map[string]any
			if raw, ok := args["json"]; ok {
				b, _ := json.Marshal(raw)
				var s string
				if json.Unmarshal(b, &s) == nil && s != "" {
					_ = json.Unmarshal([]byte(s), &payload)
				} else {
					_ = json.Unmarshal(b, &payload)
				}
			}
			r.events = append(r.events, emitted{Channel: str(args["channel"]), Payload: payload})
		case "getActiveTools":
			return map[string]any{"tools": append([]string{}, r.active...)}, ""
		case "setActiveTools":
			var names []string
			if raw, ok := args["tools"].([]any); ok {
				for _, n := range raw {
					names = append(names, str(n))
				}
			}
			r.sets = append(r.sets, names)
			r.active = names
		}
		return map[string]any{}, ""
	}
	r.Host = StartHost(t, ask.Extension(), opts)
	return r
}

func str(v any) string { s, _ := v.(string); return s }

// ask runs the tool and returns its text and details.
func (r *rig) ask(args map[string]any) (string, map[string]any) {
	r.t.Helper()
	raw, failure := r.Host.Tool("ask_user_question", args)
	if failure != "" {
		r.t.Fatalf("ask_user_question failed: %s", failure)
	}
	var res struct {
		Content string         `json:"content"`
		Details map[string]any `json:"details"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		r.t.Fatalf("tool result %s: %v", raw, err)
	}
	return res.Content, res.Details
}

func (r *rig) dialogCount() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.dialogs) }

func (r *rig) dialogAt(i int) dialog {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i < 0 || i >= len(r.dialogs) {
		return dialog{}
	}
	return r.dialogs[i]
}

func (r *rig) emittedEvents() []emitted {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]emitted{}, r.events...)
}

func (r *rig) setActive(names ...string) { r.mu.Lock(); r.active = names; r.mu.Unlock() }

func (r *rig) activeNow() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{}, r.active...)
}

func (r *rig) toolSets() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]string{}, r.sets...)
}

func (r *rig) hostMethods() []string {
	var out []string
	for _, c := range r.Calls() {
		out = append(out, c.Method)
	}
	return out
}

// args builders mirror the original tests' SINGLE and MULTI fixtures.
func optArg(label, description string) map[string]any {
	return map[string]any{"label": label, "description": description}
}

func questionArg(text, header string, multi bool, options ...any) map[string]any {
	q := map[string]any{"question": text, "header": header, "options": options}
	if multi {
		q["multiSelect"] = true
	}
	return q
}

func askArgs(qs ...any) map[string]any { return map[string]any{"questions": qs} }

func singleArgs() map[string]any {
	return askArgs(questionArg("Which?", "Pick", false, optArg("A", "a"), optArg("B", "b")))
}

func multiArgs() map[string]any {
	return askArgs(questionArg("Pick colors?", "Colors", true, optArg("red", "r"), optArg("green", "g"), optArg("blue", "b")))
}

func rpcOpts() HostOptions { return HostOptions{Mode: "rpc"} }

func fmtAnswer(q, a string) string { return fmt.Sprintf("%q=%q", q, a) }

func eq(t *testing.T, got, want any, what string) {
	t.Helper()
	x, _ := json.Marshal(got)
	y, _ := json.Marshal(want)
	if string(x) != string(y) {
		t.Fatalf("%s: got %s, want %s", what, x, y)
	}
}

func mustJSON(t *testing.T, raw []byte, into any) {
	t.Helper()
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
}

func answersOf(details map[string]any) []any { a, _ := details["answers"].([]any); return a }

// answerAt is the i-th answer of a result's details, or an empty map: a wrong result fails the assertion that
// follows instead of panicking.
func answerAt(details map[string]any, i int) map[string]any {
	a := answersOf(details)
	if i >= len(a) {
		return map[string]any{}
	}
	m, _ := a[i].(map[string]any)
	return m
}

func at[T any](s []T, i int) T {
	var zero T
	if i < 0 || i >= len(s) {
		return zero
	}
	return s[i]
}

func head(s string, n int) string {
	if n > len(s) {
		return s
	}
	return s[:n]
}
