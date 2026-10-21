package tintinweb_subagents

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func timeMs(ms int64) time.Time { return time.UnixMilli(ms) }

// captureStderr returns what fn wrote to os.Stderr.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() { b, _ := io.ReadAll(r); done <- string(b) }()
	func() {
		defer func() { os.Stderr = old; w.Close() }()
		fn()
	}()
	return <-done
}

// The mention rig: the subagents extension on a fake host, a fleet of scripted children, and the helpers the
// wiring tests share. upstream: test/helpers/boot-extension.ts.

type mentionRig struct {
	*rig
	cwd string

	mu       sync.Mutex
	cloneRan []mentionCloneOptions
	cloneRes mentionCloneResult
	cloneGo  chan struct{} // when set, the clone waits for it
	ready    chan struct{} // closed once the rig is built: the host answers a call made while the session starts
	provider string        // the id of the stacked autocomplete provider
	below    func(op string) any
	level    string // the session's thinking level
	conv     []any  // the session's resolved conversation
}

type mentionBoot struct {
	settings   obj
	agentFiles map[string]string
	mode       string
	session    string // the session file every child reports
	realClone  bool   // keep the real conversation clone instead of a scripted result
}

func startMention(t *testing.T, b mentionBoot) *mentionRig {
	t.Helper()
	cwd := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PIG_USE_PI_DIRS", "")
	if b.settings != nil {
		writeFile(t, filepath.Join(cwd, ".pi", "subagents.json"), mustJSON(b.settings))
	}
	for name, body := range b.agentFiles {
		writeFile(t, filepath.Join(cwd, ".pi", "agents", name+".md"), body)
	}
	m := &mentionRig{cwd: cwd, ready: make(chan struct{}), cloneRes: mentionCloneResult{Spawned: true}}
	m.rig = startRigIn2(t, cwd, b.mode, func(a *app) {
		if !b.realClone {
			a.cloneFn = func(ctx context.Context, o mentionCloneOptions) mentionCloneResult {
				m.mu.Lock()
				m.cloneRan = append(m.cloneRan, o)
				gate, res := m.cloneGo, m.cloneRes
				m.mu.Unlock()
				if gate != nil {
					<-gate
				}
				return res
			}
		}
	}, m.host)
	m.fleet.mu.Lock()
	m.fleet.session = b.session
	m.fleet.mu.Unlock()
	close(m.ready)
	return m
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// host answers the autocomplete calls: it plays the editor's side of stacking a provider on the one below.
func (m *mentionRig) host(method string, args map[string]any) (any, string, bool) {
	switch method {
	case "ui.addAutocompleteProvider":
		id, _ := args["factoryId"].(string)
		go func() {
			<-m.ready
			raw, failure := m.rig.Host.roundTrip(obj{"method": "autocomplete.sync", "args": obj{
				"id": "wrap-" + id, "factoryId": id, "operation": "wrap",
				"current": obj{"id": "below", "triggerCharacters": []string{"@", "#"}, "hasFileTrigger": false}}})
			if failure != "" {
				return
			}
			var d struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(raw, &d)
			m.mu.Lock()
			m.provider = d.ID
			m.mu.Unlock()
		}()
		return obj{}, "", true
	case "getThinkingLevel":
		m.mu.Lock()
		defer m.mu.Unlock()
		return obj{"level": m.level}, "", true
	case "getSystemPrompt":
		return obj{"prompt": "the live system prompt"}, "", true
	case "getModel":
		return obj{"id": "main-model", "provider": "anthropic", "api": "x"}, "", true
	case "getModelAuth":
		return obj{"ok": true, "apiKey": "the-key"}, "", true
	case "sessionRead":
		if args["method"] == "buildSessionContext" {
			m.mu.Lock()
			defer m.mu.Unlock()
			return obj{"messages": m.conv, "thinkingLevel": "off"}, "", true
		}
	case "ui.autocomplete.invoke":
		m.mu.Lock()
		fn := m.below
		m.mu.Unlock()
		if fn != nil {
			if v := fn(args["operation"].(string)); v != nil {
				return v, "", true
			}
		}
		return obj{"items": []any{}, "prefix": ""}, "", true
	}
	return nil, "", false
}

func (m *mentionRig) providerID(t *testing.T) string {
	t.Helper()
	for i := 0; i < 400; i++ {
		m.mu.Lock()
		id := m.provider
		m.mu.Unlock()
		if id != "" {
			return id
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no autocomplete provider was stacked")
	return ""
}

// suggestions asks the stacked provider the way the editor does.
func (m *mentionRig) suggestions(t *testing.T, line string) (values []string, prefix string) {
	t.Helper()
	raw, failure := m.rig.Host.roundTrip(obj{"method": "autocomplete.suggest", "args": obj{
		"id": m.providerID(t), "operation": "getSuggestions", "lines": []string{line}, "cursorLine": 0, "cursorCol": len(utf16Units(line))}})
	if failure != "" {
		t.Fatalf("getSuggestions: %s", failure)
	}
	var res struct {
		Items  []sdk.AutocompleteItem `json:"items"`
		Prefix string                 `json:"prefix"`
	}
	_ = json.Unmarshal(raw, &res)
	for _, i := range res.Items {
		values = append(values, i.Value)
	}
	return values, res.Prefix
}

func utf16Units(s string) []uint16 { return utf16.Encode([]rune(s)) }

// input delivers what the user typed and returns the hook's answer.
func (m *mentionRig) input(text, source string, extra obj) obj {
	m.t.Helper()
	data := obj{"text": text, "source": source}
	for k, v := range extra {
		data[k] = v
	}
	raw := m.Fire("input", data)
	var out obj
	if err := json.Unmarshal(raw, &out); err != nil {
		m.t.Fatalf("input result %q: %v", raw, err)
	}
	return out
}

func (m *mentionRig) send(text string) obj { return m.input(text, "interactive", nil) }

func (m *mentionRig) notifies() []string {
	var out []string
	for _, c := range m.CallsTo("ui.notify") {
		out = append(out, c.Args["message"].(string)+" | "+c.Args["level"].(string))
	}
	return out
}

func (m *mentionRig) hasNotify(msg, level string) bool {
	for _, n := range m.notifies() {
		if n == msg+" | "+level {
			return true
		}
	}
	return false
}

func (m *mentionRig) waitNotify(t *testing.T, msg, level string) {
	t.Helper()
	for i := 0; i < 600; i++ {
		if m.hasNotify(msg, level) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("never notified %q (%s); got %q", msg, level, m.notifies())
}

func (m *mentionRig) waitNotifyContaining(t *testing.T, sub, level string) string {
	t.Helper()
	for i := 0; i < 600; i++ {
		for _, n := range m.notifies() {
			if strings.Contains(n, sub) && strings.HasSuffix(n, " | "+level) {
				return n
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("never notified something containing %q (%s); got %q", sub, level, m.notifies())
	return ""
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 1000; i++ {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition never held: %s", what)
}

// spawnBg starts a background agent through the Agent tool and returns its record.
func (m *mentionRig) spawnBg(typ string) *agentRecord {
	m.t.Helper()
	text := m.must("Agent", obj{"prompt": "go", "description": "find flaky tests", "subagent_type": typ, "run_in_background": true})
	return m.app.mgr.get(idFrom(m.t, text))
}

func (m *mentionRig) status(r *agentRecord) string {
	m.app.mgr.mu.Lock()
	defer m.app.mgr.mu.Unlock()
	return r.Status
}

// finish settles child n (0-based) with a result and waits for its record to settle.
func (m *mentionRig) finish(t *testing.T, r *agentRecord, n int, text string) *fakeChild {
	t.Helper()
	c := m.fleet.wait(t, n+1)[n]
	c.finish(text)
	<-m.app.mgr.doneOf(r)
	return c
}

func (c *fakeChild) steers() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.steered...)
}

func (c *fakeChild) resumes() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.resumed...)
}

func (m *mentionRig) sentMessages() []string {
	var out []string
	for _, msg := range m.messages() {
		if inner, ok := msg["message"].(map[string]any); ok {
			out = append(out, inner["content"].(string))
		}
	}
	return out
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
