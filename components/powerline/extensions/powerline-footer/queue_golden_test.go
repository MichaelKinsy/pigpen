package powerline_footer

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// Replays testdata/queue-states.json against the Go queue and compares everything with testdata/queue-golden.json, which
// port/drive/queue-drive.mjs recorded from the ORIGINAL extension: the notifications, the messages it sends, the picker's
// overlays (the real pi-tui SelectList) after every key, the bar's queue segment, the queue preview row and the inbox file.

type qLevel struct {
	Widths []int    `json:"widths"`
	Keys   []string `json:"keys"`
}

type qStep struct {
	Event   string         `json:"event"`
	Data    map[string]any `json:"data"`
	Idle    *bool          `json:"idle"`
	Wait    int            `json:"wait"`
	Command string         `json:"command"`
	Args    string         `json:"args"`
	Levels  []qLevel       `json:"levels"`
}

type qState struct {
	Name      string            `json:"name"`
	SessionID string            `json:"sessionId"`
	Widths    []int             `json:"widths"`
	Powerline map[string]any    `json:"powerline"`
	Inbox     []json.RawMessage `json:"inbox"`
	Aliases   json.RawMessage   `json:"aliases"`
	Idle      *bool             `json:"idle"`
	Ack       bool              `json:"ack"`
	SendError string            `json:"sendError"`
	Steps     []qStep           `json:"steps"`
}

type qOverlay struct {
	Frames []map[string]any `json:"frames"`
	Result any              `json:"result"`
}

type qOut struct {
	Notes              [][]string                     `json:"notes"`
	Sends              [][]any                        `json:"sends"`
	EditorText         *string                        `json:"editorText"`
	Compacts           int                            `json:"compacts"`
	Overlays           []qOverlay                     `json:"overlays"`
	Widths             map[string]map[string][]string `json:"widths"`
	Placement          map[string]string              `json:"placement"`
	Inbox              *string                        `json:"inbox"`
	Aliases            *string                        `json:"aliases"`
	InboxAfterShutdown *string                        `json:"inboxAfterShutdown"`
}

// scriptedOverlayTheme is the driver's theme for the overlay: tokens become SGR 38;5;(17+index), bold draws nothing.
type scriptedOverlayTheme struct{ scriptedTheme }

func (s scriptedOverlayTheme) Fg(token, text string) string {
	out, err := s.scriptedTheme.Fg(token, text)
	if err != nil {
		panic(err)
	}
	return out
}
func (scriptedOverlayTheme) Bold(text string) string { return text }

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestQueueGolden(t *testing.T) {
	var states []qState
	readJSONFile(t, "queue-states.json", &states)
	var golden map[string]json.RawMessage
	readJSONFile(t, "queue-golden.json", &golden)
	var meta struct {
		ThemeTokens []string `json:"themeTokens"`
	}
	if err := json.Unmarshal(golden["_meta"], &meta); err != nil {
		t.Fatal(err)
	}
	oldClock := clock
	clock = func() int64 { return 1767323045000 } // 2026-01-02T03:04:05Z, the driver's frozen clock
	t.Cleanup(func() { clock = oldClock })
	for _, st := range states {
		t.Run(st.Name, func(t *testing.T) {
			var want qOut
			if err := json.Unmarshal(golden[st.Name], &want); err != nil {
				t.Fatalf("no golden for %s: %v", st.Name, err)
			}
			got := replayQueueState(t, st, meta.ThemeTokens)
			eq(t, got.Notes, want.Notes, "notifications")
			eq(t, got.Sends, want.Sends, "messages sent")
			eq(t, got.EditorText, want.EditorText, "editor text")
			eq(t, len(got.Overlays), len(want.Overlays), "overlays opened")
			for i := range want.Overlays {
				if i >= len(got.Overlays) {
					break
				}
				eq(t, len(got.Overlays[i].Frames), len(want.Overlays[i].Frames), "frames of overlay")
				for j := range want.Overlays[i].Frames {
					if j < len(got.Overlays[i].Frames) && !reflect.DeepEqual(got.Overlays[i].Frames[j], want.Overlays[i].Frames[j]) {
						reportFrameDiff(t, i, j, got.Overlays[i].Frames[j], want.Overlays[i].Frames[j])
					}
				}
				eq(t, got.Overlays[i].Result, want.Overlays[i].Result, "overlay result")
			}
			for width, keys := range want.Widths {
				for key, lines := range keys {
					if !reflect.DeepEqual(got.Widths[width][key], lines) {
						t.Errorf("%s at width %s:\n got %q\nwant %q", key, width, got.Widths[width][key], lines)
					}
				}
			}
			for key, placement := range want.Placement {
				if _, rendered := want.Widths[firstKey(want.Widths)][key]; rendered {
					eq(t, got.Placement[key], placement, "placement of "+key)
				}
			}
			eqText(t, got.Inbox, want.Inbox, "inbox")
			eqText(t, got.Aliases, want.Aliases, "aliases")
			eqText(t, got.InboxAfterShutdown, want.InboxAfterShutdown, "inbox after shutdown")
		})
	}
}

func firstKey[V any](m map[string]V) string {
	for k := range m {
		return k
	}
	return ""
}

func readJSONFile(t *testing.T, name string, into any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, into); err != nil {
		t.Fatal(err)
	}
}

func replayQueueState(t *testing.T, st qState, tokens []string) qOut {
	home := t.TempDir()
	agent, cwd := filepath.Join(home, "agent"), filepath.Join(home, "work", "proj")
	for _, dir := range []string{filepath.Join(agent, "powerline-footer"), cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sub := func(s string) string { return strings.NewReplacer("{CWD}", cwd, "{HOME}", home).Replace(s) }
	unsub := func(s string) string { return strings.NewReplacer(cwd, "{CWD}", home, "{HOME}").Replace(s) }
	var lines []string
	for _, raw := range st.Inbox {
		var compact bytes.Buffer
		if err := json.Compact(&compact, raw); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, sub(compact.String()))
	}
	if len(lines) > 0 {
		os.WriteFile(filepath.Join(agent, "powerline-footer", "inbox.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	}
	if st.Aliases != nil {
		var aliases map[string]string
		json.Unmarshal(st.Aliases, &aliases)
		keys := make([]string, 0, len(aliases))
		for k := range aliases {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		o := newObject()
		for _, k := range keys {
			o.set(k, sub(aliases[k]))
		}
		os.WriteFile(filepath.Join(agent, "powerline-footer", "projects.json"), []byte(marshalJSON(o, "  ")+"\n"), 0o644)
	}
	powerline := map[string]any{"welcome": false}
	for k, v := range st.Powerline {
		powerline[k] = v
	}
	settings, _ := json.Marshal(map[string]any{"powerline": powerline})
	th := scriptedTheme{tokens}
	r := newQueueRigAt(t, agent, cwd, st.SessionID, string(settings), th)
	t.Setenv("POWERLINE_NERD_FONTS", "0")
	r.host.idle = st.Idle == nil || *st.Idle
	r.host.sendHook = func(text, deliverAs string) error {
		if st.SendError != "" {
			return queueError(st.SendError)
		}
		if st.Ack {
			r.dispatch("before_agent_start", map[string]any{"prompt": text})
		}
		return nil
	}
	out := qOut{Notes: [][]string{}, Sends: [][]any{}, Widths: map[string]map[string][]string{}, Placement: map[string]string{}}

	r.p.sessionStart("startup")
	for _, step := range st.Steps {
		if step.Idle != nil {
			r.host.idle = *step.Idle
		}
		if step.Wait > 0 {
			r.clock.tick(time.Duration(step.Wait) * time.Millisecond)
		}
		if step.Event != "" {
			data := map[string]any{"type": step.Event}
			for k, v := range step.Data {
				data[k] = v
			}
			r.dispatch(step.Event, data)
		}
		if step.Command != "" {
			level := 0
			show := func(title, hint string, items []selectItem, maxVisible int) (*selectItem, error) {
				if level >= len(step.Levels) {
					return nil, nil
				}
				lv := step.Levels[level]
				level++
				o := newSelectOverlay(scriptedOverlayTheme{th}, title, hint, items, maxVisible)
				entry := qOverlay{Result: "open"}
				widths := lv.Widths
				if len(widths) == 0 {
					widths = []int{60, 100}
				}
				frame := func(after string) {
					f := map[string]any{"after": after}
					for _, w := range widths {
						f[jsNumber(float64(w))] = o.Render(w)
					}
					entry.Frames = append(entry.Frames, f)
				}
				frame("")
				var chosen *selectItem
				for _, key := range lv.Keys {
					res, err := o.HandleInput(key)
					if err != nil {
						t.Fatal(err)
					}
					if res.Done {
						entry.Result = nil
						if m, ok := res.Value.(map[string]any); ok {
							obj := map[string]any{"value": m["value"], "label": m["label"]}
							if d, _ := m["description"].(string); d != "" {
								obj["description"] = d
							}
							entry.Result = obj
							chosen = &selectItem{Value: m["value"].(string), Label: m["label"].(string), Description: m["description"].(string)}
						}
						break
					}
					frame(key)
				}
				out.Overlays = append(out.Overlays, entry)
				return chosen, nil
			}
			if err := r.q.command(sub(step.Args), show); err != nil {
				out.Notes = append(out.Notes, []string{"throw", err.Error()})
			}
		}
	}
	for _, w := range st.Widths {
		row := map[string][]string{
			"powerline-top":           r.p.renderTop(w),
			"powerline-queue-preview": r.p.renderQueuePreview(w),
			"powerline-pending-send":  r.p.renderPendingSend(w),
		}
		out.Widths[jsNumber(float64(w))] = row
	}
	for key, placement := range r.p.ui.Widgets {
		out.Placement[key] = placement
	}
	for _, n := range r.host.notes {
		out.Notes = append(out.Notes, []string{n[0], n[1]})
	}
	for _, s := range r.host.sends {
		var options any
		if s.DeliverAs != "" {
			options = map[string]any{"deliverAs": s.DeliverAs}
		}
		out.Sends = append(out.Sends, []any{s.Text, options})
	}
	if r.host.editor != "" {
		out.EditorText = &r.host.editor
	}
	out.Compacts = r.host.compacts
	read := func(name string) *string {
		data, err := os.ReadFile(filepath.Join(agent, "powerline-footer", name))
		if err != nil {
			return nil
		}
		s := unsub(string(data))
		return &s
	}
	out.Inbox, out.Aliases = read("inbox.jsonl"), read("projects.json")
	r.dispatch("session_shutdown", map[string]any{"type": "session_shutdown", "reason": "quit"})
	out.InboxAfterShutdown = read("inbox.jsonl")
	// The notes of the shutdown are not recorded by the driver either.
	_ = unsub
	return json2go(t, out, unsub)
}

// json2go normalizes through JSON (the driver's output is JSON: nulls, numbers) and removes the paths.
func json2go(t *testing.T, out qOut, unsub func(string) string) qOut {
	data := unsub(jsonOf(t, out))
	var back qOut
	if err := json.Unmarshal([]byte(data), &back); err != nil {
		t.Fatal(err)
	}
	return back
}

// dispatch delivers an event to the core and then to the queue, as the extension's handlers do.
func (r *queueRig) dispatch(name string, data map[string]any) {
	r.t.Helper()
	if slices.Contains([]string{"session_before_compact", "session_compact", "agent_end"}, name) {
		r.p.event(name, data)
	}
	if slices.Contains(queueEvents, name) {
		if err := r.q.emit(name, data); err != nil {
			r.t.Fatalf("%s: %v", name, err)
		}
	}
}

func eqText(t *testing.T, got, want *string, msg string) {
	t.Helper()
	switch {
	case got == nil && want == nil:
	case got == nil || want == nil:
		t.Errorf("%s: got %v, want %v", msg, got, want)
	case *got != *want:
		t.Errorf("%s:\n got %q\nwant %q", msg, *got, *want)
	}
}

// reportFrameDiff names the first row of the first width that differs.
func reportFrameDiff(t *testing.T, overlay, frame int, got, want map[string]any) {
	t.Helper()
	for key, w := range want {
		g := got[key]
		if reflect.DeepEqual(g, w) {
			continue
		}
		gl, _ := g.([]any)
		wl, _ := w.([]any)
		for row := 0; row < max(len(gl), len(wl)); row++ {
			var gr, wr any
			if row < len(gl) {
				gr = gl[row]
			}
			if row < len(wl) {
				wr = wl[row]
			}
			if !reflect.DeepEqual(gr, wr) {
				t.Errorf("overlay %d frame %d (after %q) width %s row %d:\n got %q\nwant %q", overlay, frame, want["after"], key, row, gr, wr)
				return
			}
		}
		t.Errorf("overlay %d frame %d key %s: got %v want %v", overlay, frame, key, g, w)
		return
	}
}
