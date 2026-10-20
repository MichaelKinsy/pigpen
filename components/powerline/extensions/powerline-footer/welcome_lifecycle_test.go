package powerline_footer_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	powerline "github.com/MichaelKinsy/pigpen/powerline-footer"
)

// The welcome lifecycle through the real SDK and a fake interactive host: tests/welcome.test.ts (the three harness tests) and the
// dismissals the original wires in index.ts. The original's harness drives a Pi extension API with a mocked setTimeout and a fake
// editor; here the timers are mocked (powerline.SetWelcomeTimers) and the host is the wire protocol. A key is a `terminal_input`
// request: the host asks the extension's listeners first and, when none consumes it, hands it to the editor itself, so "without
// losing input" is the verdict not consuming the chunk.

var wlANSI = regexp.MustCompile("\x1b\\[[0-9;]*m")

type wlTimers struct {
	mu       sync.Mutex
	next     int
	after    map[int]func()
	every    map[int]func()
	afterSet []time.Duration
}

func newWLTimers() *wlTimers { return &wlTimers{after: map[int]func(){}, every: map[int]func(){}} }

func (w *wlTimers) After(d time.Duration, f func()) func() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.next++
	id := w.next
	w.after[id] = f
	w.afterSet = append(w.afterSet, d)
	return func() { w.mu.Lock(); delete(w.after, id); w.mu.Unlock() }
}

func (w *wlTimers) Every(d time.Duration, f func()) func() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.next++
	id := w.next
	w.every[id] = f
	if d != time.Second {
		panic("the countdown ticks every second")
	}
	return func() { w.mu.Lock(); delete(w.every, id); w.mu.Unlock() }
}

// runStartupWork runs the pending timeouts, each on its own goroutine, and returns a wait for all of them.
func (w *wlTimers) runStartupWork() (wait func()) {
	w.mu.Lock()
	callbacks := make([]func(), 0, len(w.after))
	for id, f := range w.after {
		callbacks = append(callbacks, f)
		delete(w.after, id)
	}
	w.mu.Unlock()
	var wg sync.WaitGroup
	for _, f := range callbacks {
		wg.Add(1)
		go func() { defer wg.Done(); f() }()
	}
	return wg.Wait
}

func (w *wlTimers) pendingAfter() int { w.mu.Lock(); defer w.mu.Unlock(); return len(w.after) }

func (w *wlTimers) tick() {
	w.mu.Lock()
	var fs []func()
	for _, f := range w.every {
		fs = append(fs, f)
	}
	w.mu.Unlock()
	for _, f := range fs {
		f()
	}
}

func (w *wlTimers) intervals() int { w.mu.Lock(); defer w.mu.Unlock(); return len(w.every) }

type wlRig struct {
	t          *testing.T
	h          *Host
	timers     *wlTimers
	home       string
	commands   atomic.Int32 // getCommands calls
	editorText atomic.Value // string
	branch     atomic.Value // []any
	prompt     atomic.Value // string: the system prompt
	noModel    atomic.Bool
	model      atomic.Value  // map[string]any: the model the host reports
	holdHeader chan struct{} // when set, the call that installs the header waits for it
	release    chan struct{}
	watching   atomic.Int32 // the welcome's own keyboard watches: starts minus stops
	watches    atomic.Int32 // starts
}

var wlCommands = []any{
	map[string]any{"name": "user-prompt", "source": "prompt", "sourceInfo": map[string]any{"path": "/agent/prompts/user.md", "source": "user", "scope": "user", "origin": "top-level"}},
	map[string]any{"name": "project-prompt", "source": "prompt", "sourceInfo": map[string]any{"path": "/repo/.pi/prompts/project.md", "source": "project", "scope": "project", "origin": "top-level"}},
	map[string]any{"name": "package-prompt", "source": "prompt", "sourceInfo": map[string]any{"path": "/cache/pkg/prompts/pkg.md", "source": "pkg", "scope": "user", "origin": "package"}},
	map[string]any{"name": "user-skill", "source": "skill", "sourceInfo": map[string]any{"path": "/agent/skills/user/SKILL.md", "source": "user", "scope": "user", "origin": "top-level"}},
	map[string]any{"name": "project-skill", "source": "skill", "sourceInfo": map[string]any{"path": "/repo/.pi/skills/project/SKILL.md", "source": "project", "scope": "project", "origin": "top-level"}},
	map[string]any{"name": "package-skill", "source": "skill", "sourceInfo": map[string]any{"path": "/cache/pkg/skills/pkg/SKILL.md", "source": "pkg", "scope": "project", "origin": "package"}},
	map[string]any{"name": "an-extension-command", "source": "extension", "sourceInfo": map[string]any{"path": "/ext/a.ts", "source": "a", "scope": "user", "origin": "top-level"}},
}

// startWelcome starts the extension in a tui (or rpc) host with settings {quietStartup, powerline.welcome} and no session fired yet.
func startWelcome(t *testing.T, quiet bool, mode string, extra map[string]any) *wlRig {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", "")
	os.Unsetenv("PI_CODING_AGENT_DIR")
	t.Setenv("POWERLINE_NERD_FONTS", "0")
	settings := map[string]any{"quietStartup": quiet, "powerline": map[string]any{"welcome": true}}
	for k, v := range extra {
		settings[k] = v
	}
	data, _ := json.Marshal(settings)
	agent := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(agent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agent, "settings.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	r := &wlRig{t: t, home: home, timers: newWLTimers(), release: make(chan struct{})}
	r.editorText.Store("")
	r.branch.Store([]any{})
	r.prompt.Store("")
	r.model.Store(map[string]any{"id": "test-model", "name": "Test model", "provider": "acme-cloud", "reasoning": false, "contextWindow": 200000})
	t.Cleanup(powerline.SetWelcomeTimers(r.timers))
	t.Cleanup(powerline.SetWelcomeWatchHook(func(on bool) {
		if on {
			r.watching.Add(1)
			r.watches.Add(1)
		} else {
			r.watching.Add(-1)
		}
	}))
	r.h = StartHost(t, powerline.Extension(), HostOptions{Mode: mode, Cwd: home, OnCallValue: func(method string, args map[string]any) (any, string) {
		switch method {
		case "getModelInfo":
			if r.noModel.Load() {
				return nil, "no model"
			}
			return r.model.Load().(map[string]any), ""
		case "ui.setHeader":
			if hold := r.holdHeader; hold != nil && args["clear"] != true {
				<-hold
			}
			return map[string]any{}, ""
		case "getContextUsage":
			return nil, "no usage yet"
		case "getSessionName":
			return nil, "no session"
		case "getThinkingLevel":
			return map[string]any{"level": "off"}, ""
		case "ui.getEditorText":
			return map[string]any{"text": r.editorText.Load().(string)}, ""
		case "getCommands":
			r.commands.Add(1)
			return map[string]any{"commands": wlCommands}, ""
		case "getSystemPrompt":
			return map[string]any{"prompt": r.prompt.Load().(string)}, ""
		case "sessionRead":
			if args["method"] == "getBranch" {
				return r.branch.Load().([]any), ""
			}
			return nil, "not scripted"
		case "ui.custom":
			<-r.release // the overlay stays open until the test ends or closes it
			return map[string]any{}, ""
		}
		return map[string]any{}, ""
	}})
	t.Cleanup(func() { close(r.release) }) // runs before the host stops
	return r
}

func (r *wlRig) startSession(reason string) {
	r.t.Helper()
	r.h.Fire("session_start", map[string]any{"reason": reason})
}

// key delivers one terminal chunk the way the host does and reports whether an extension listener consumed it.
func (r *wlRig) key(data string) (consumed bool) {
	r.t.Helper()
	raw, failure := r.h.roundTrip(map[string]any{"method": "terminal_input", "args": map[string]any{"data": data}})
	if failure != "" {
		r.t.Fatalf("terminal_input: %s", failure)
	}
	var verdict struct {
		Consume bool    `json:"consume"`
		Data    *string `json:"data"`
	}
	if err := json.Unmarshal(raw, &verdict); err != nil {
		r.t.Fatalf("terminal_input verdict %s: %v", raw, err)
	}
	if verdict.Data != nil {
		r.t.Errorf("a listener rewrote the chunk %q to %q", data, *verdict.Data)
	}
	return verdict.Consume
}

func wlWait(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// headerInstalls and headerClears count the ui.setHeader calls that draw rows and that restore the built-in header.
func (r *wlRig) headerCalls() (installs, clears int) {
	for _, c := range r.h.CallsTo("ui.setHeader") {
		if c.Args["clear"] == true {
			clears++
		} else {
			installs++
		}
	}
	return
}

// requireWatchEnded waits for the welcome to stop watching the keyboard (the SDK shares one host subscription between the
// extension's listeners, so the host's off call is not the signal), and checks a key then changes nothing.
func (r *wlRig) requireWatchEnded() {
	r.t.Helper()
	wlWait(r.t, "the keyboard watch to end", func() bool { return r.watches.Load() > 0 && r.watching.Load() == 0 })
	i0, c0 := r.headerCalls()
	h0 := r.hides()
	if r.key("z") {
		r.t.Error("a key was consumed after the welcome ended")
	}
	r.quiescent()
	if i, c := r.headerCalls(); i != i0 || c != c0 || r.hides() != h0 {
		r.t.Errorf("a key after the welcome ended still changed it: header %d/%d -> %d/%d, hides %d -> %d", i0, c0, i, c, h0, r.hides())
	}
}

func (r *wlRig) lastHeader() string {
	var last []any
	for _, c := range r.h.CallsTo("ui.setHeader") {
		if c.Args["clear"] != true {
			last, _ = c.Args["lines"].([]any)
		}
	}
	var lines []string
	for _, l := range last {
		lines = append(lines, l.(string))
	}
	return wlANSI.ReplaceAllString(strings.Join(lines, "\n"), "")
}

func (r *wlRig) customCalls() []HostCall { return r.h.CallsTo("ui.custom") }

func (r *wlRig) hides() int {
	n := 0
	for _, c := range r.h.CallsTo("ui.custom.control") {
		if c.Args["action"] == "hide" {
			n++
		}
	}
	return n
}

// mounted tells the extension the host mounted overlay `call` (ui.custom.opened), as a host does after ui.custom.
func (r *wlRig) mounted(call HostCall) {
	r.h.write(map[string]any{"type": "notify", "notify": map[string]any{"method": "ui.custom.opened", "args": map[string]any{
		"key": call.Args["key"], "hidden": false, "focused": false, "visible": true}}})
}

func (r *wlRig) installations() int {
	installs, _ := r.headerCalls()
	return installs + len(r.customCalls())
}

func (r *wlRig) quiescent() {
	r.t.Helper()
	time.Sleep(50 * time.Millisecond) // nothing more may arrive
}

func TestWelcomeLifecycleTwins(t *testing.T) {
	tw := func(t *testing.T, file, title string, fn func(t *testing.T)) { t.Helper(); t.Run(title, fn) }

	tw(t, "welcome", "pending welcome discovery is cancelled by typing or nonblocking shutdown", func(t *testing.T) {
		for _, quiet := range []bool{true, false} {
			for _, action := range []string{"typing", "shutdown"} {
				name := "overlay: " + action
				if quiet {
					name = "header: " + action
				}
				t.Run(name, func(t *testing.T) {
					r := startWelcome(t, quiet, "tui", nil)
					sessions := filepath.Join(r.home, ".pi", "agent", "sessions")
					entered, release := make(chan struct{}), make(chan struct{})
					var once sync.Once
					t.Cleanup(powerline.SetWelcomeFSHook(func(op, path string) {
						if op == "realpath" && path == sessions {
							once.Do(func() { close(entered) })
							<-release
						}
					}))
					var released sync.Once
					releaseOnce := func() { released.Do(func() { close(release) }) }
					t.Cleanup(releaseOnce)

					r.startSession("startup")
					wait := r.timers.runStartupWork()
					select {
					case <-entered:
					case <-time.After(5 * time.Second):
						t.Fatal("discovery never reached the sessions directory")
					}
					if action == "typing" {
						r.key("x")
						r.key("\x7f") // empty again: cancellation, not draft text, must prevent resurrection
						if r.editorText.Load().(string) != "" {
							t.Fatal("the scripted editor must be empty")
						}
					} else {
						// Shutdown must finish even while the filesystem operation remains blocked.
						r.h.Fire("session_shutdown", map[string]any{"reason": "quit"})
					}
					releaseOnce()
					wait()
					r.quiescent()
					if n := r.installations(); n != 0 {
						t.Errorf("installations = %d, want 0", n)
					}
				})
			}
		}
	})

	tw(t, "welcome", "eligible welcome installs and dismisses without losing input", func(t *testing.T) {
		for _, quiet := range []bool{true, false} {
			name := "overlay"
			if quiet {
				name = "header"
			}
			t.Run(name, func(t *testing.T) {
				r := startWelcome(t, quiet, "tui", nil)
				r.startSession("startup")
				r.timers.runStartupWork()()
				if quiet {
					wlWait(t, "the header", func() bool { i, _ := r.headerCalls(); return i > 0 })
				} else {
					wlWait(t, "the overlay", func() bool { return len(r.customCalls()) > 0 })
				}
				if quiet {
					rendered := r.lastHeader()
					for _, want := range []string{"Test model", "3 skills", "3 prompt templates"} {
						if !strings.Contains(rendered, want) {
							t.Errorf("header lacks %q:\n%s", want, rendered)
						}
					}
				} else {
					call := r.customCalls()[0]
					if call.Args["overlay"] != true {
						t.Errorf("ui.custom args = %v, want an overlay", call.Args)
					}
					r.mounted(call)
				}
				if got := r.commands.Load(); got != 1 {
					t.Errorf("getCommands calls = %d, want 1", got)
				}
				if r.key("x") {
					t.Error("the key was consumed: the editor would lose it")
				}
				if quiet {
					wlWait(t, "the header to be removed", func() bool { _, c := r.headerCalls(); return c == 1 })
				} else {
					wlWait(t, "the overlay to be hidden", func() bool { return r.hides() == 1 })
				}
				if n := r.installations(); n != 1 {
					t.Errorf("installations = %d, want 1", n)
				}
			})
		}
	})

	tw(t, "welcome", "RPC clients never receive the startup welcome", func(t *testing.T) {
		for _, quiet := range []bool{true, false} {
			name := "overlay"
			if quiet {
				name = "header"
			}
			t.Run(name, func(t *testing.T) {
				r := startWelcome(t, quiet, "rpc", nil)
				r.startSession("startup")
				r.timers.runStartupWork()()
				r.quiescent()
				if n := r.installations(); n != 0 {
					t.Errorf("installations = %d, want 0", n)
				}
				if n := r.watches.Load(); n != 0 {
					t.Errorf("the keyboard was watched %d times in RPC mode", n)
				}
			})
		}
	})
}

// header starts a quiet-startup session and waits for the header.
func headerRig(t *testing.T, mutate func(r *wlRig)) *wlRig {
	t.Helper()
	r := startWelcome(t, true, "tui", nil)
	if mutate != nil {
		mutate(r)
	}
	r.startSession("startup")
	r.timers.runStartupWork()()
	wlWait(t, "the header", func() bool { i, _ := r.headerCalls(); return i > 0 })
	return r
}

// overlayRig starts a session with the overlay and tells the extension the host mounted it.
func overlayRig(t *testing.T) (*wlRig, HostCall) {
	t.Helper()
	r := startWelcome(t, false, "tui", nil)
	r.startSession("startup")
	r.timers.runStartupWork()()
	wlWait(t, "the overlay", func() bool { return len(r.customCalls()) > 0 })
	call := r.customCalls()[0]
	r.mounted(call)
	return r, call
}

func TestWelcomeIsDismissedByEveryEventTheOriginalWires(t *testing.T) {
	dismissals := map[string]func(r *wlRig){
		"agent_start": func(r *wlRig) { r.h.Fire("agent_start", nil) },
		"tool_call": func(r *wlRig) {
			r.h.Fire("tool_call", map[string]any{"toolName": "read", "toolCallId": "c1", "input": map[string]any{}})
		},
		"session_shutdown": func(r *wlRig) { r.h.Fire("session_shutdown", map[string]any{"reason": "quit"}) },
		"powerline_off":    func(r *wlRig) { r.h.Command("powerline", "") },
		"new_session":      func(r *wlRig) { r.h.Fire("session_start", map[string]any{"reason": "resume"}) },
	}
	for name, dismiss := range dismissals {
		t.Run("header/"+name, func(t *testing.T) {
			r := headerRig(t, nil)
			dismiss(r)
			wlWait(t, "the header to be removed", func() bool { _, c := r.headerCalls(); return c == 1 })
			if _, c := r.headerCalls(); c != 1 {
				t.Errorf("header clears = %d", c)
			}
		})
		t.Run("overlay/"+name, func(t *testing.T) {
			r, _ := overlayRig(t)
			dismiss(r)
			wlWait(t, "the overlay to be hidden", func() bool { return r.hides() == 1 })
			r.requireWatchEnded()
		})
	}
}

func TestWelcomeIsNotShownUnlessEligible(t *testing.T) {
	cases := map[string]struct {
		reason string
		extra  map[string]any
		setup  func(r *wlRig)
	}{
		"resume":                {reason: "resume"},
		"reload":                {reason: "reload"},
		"new":                   {reason: "new"},
		"welcome disabled":      {reason: "startup", extra: map[string]any{"powerline": map[string]any{"welcome": false}}},
		"a draft in the editor": {reason: "startup", setup: func(r *wlRig) { r.editorText.Store("draft") }},
		"an assistant message already": {reason: "startup", setup: func(r *wlRig) {
			r.branch.Store([]any{map[string]any{"type": "message", "message": map[string]any{"role": "assistant"}}})
		}},
		"a tool call already":   {reason: "startup", setup: func(r *wlRig) { r.branch.Store([]any{map[string]any{"type": "tool_call"}}) }},
		"a tool result already": {reason: "startup", setup: func(r *wlRig) { r.branch.Store([]any{map[string]any{"type": "tool_result"}}) }},
	}
	for name, c := range cases {
		for _, quiet := range []bool{true, false} {
			t.Run(name, func(t *testing.T) {
				r := startWelcome(t, quiet, "tui", c.extra)
				if c.setup != nil {
					c.setup(r)
				}
				r.startSession(c.reason)
				r.timers.runStartupWork()()
				r.quiescent()
				if n := r.installations(); n != 0 {
					t.Errorf("installations = %d, want 0", n)
				}
			})
		}
	}
	t.Run("a user message does not block it", func(t *testing.T) {
		headerRig(t, func(r *wlRig) {
			r.branch.Store([]any{map[string]any{"type": "message", "message": map[string]any{"role": "user"}}})
		})
	})
}

func TestWelcomeTimersAreTheOriginals(t *testing.T) {
	r := startWelcome(t, true, "tui", nil)
	r.startSession("startup")
	if got := r.timers.afterSet; len(got) != 1 || got[0] != 0 {
		t.Errorf("header timeouts = %v, want one of 0 ms", got)
	}
	r2 := startWelcome(t, false, "tui", nil)
	r2.startSession("startup")
	if got := r2.timers.afterSet; len(got) != 1 || got[0] != 100*time.Millisecond {
		t.Errorf("overlay timeouts = %v, want one of 100 ms", got)
	}
}

func TestWelcomeOverlayCountsDownThirtySecondsAndCloses(t *testing.T) {
	r, call := overlayRig(t)
	args, _ := json.Marshal(call.Args)
	var opts struct {
		Overlay        bool `json:"overlay"`
		HasHandle      bool `json:"hasHandle"`
		OverlayOptions struct {
			NonCapturing bool `json:"nonCapturing"`
		} `json:"overlayOptions"`
	}
	_ = json.Unmarshal(args, &opts)
	if !opts.Overlay || !opts.HasHandle || !opts.OverlayOptions.NonCapturing {
		t.Errorf("ui.custom args = %s, want a non-capturing overlay with a handle", args)
	}
	if r.timers.intervals() != 1 {
		t.Fatalf("countdown intervals = %d, want 1", r.timers.intervals())
	}
	for range 29 {
		r.timers.tick()
	}
	r.quiescent()
	if r.hides() != 0 {
		t.Fatal("hidden before the 30 s are up")
	}
	r.timers.tick()
	wlWait(t, "the overlay to be hidden at 0", func() bool { return r.hides() == 1 })
	if r.timers.intervals() != 0 {
		t.Error("the countdown keeps ticking after the dismissal")
	}
	r.requireWatchEnded()
}

func TestWelcomeKeyReleases(t *testing.T) {
	release := "\x1b[120;1:3u"
	t.Run("the editor never sees one, so the header stays", func(t *testing.T) {
		r := headerRig(t, nil)
		if r.key(release) {
			t.Error("consumed")
		}
		r.quiescent()
		if _, c := r.headerCalls(); c != 0 {
			t.Error("a key release removed the header")
		}
	})
	t.Run("the overlay wants them, so one dismisses it", func(t *testing.T) {
		r, _ := overlayRig(t)
		if r.key(release) {
			t.Error("consumed")
		}
		wlWait(t, "the overlay to be hidden", func() bool { return r.hides() == 1 })
	})
	t.Run("a bracketed paste is input, not a release", func(t *testing.T) {
		r := headerRig(t, nil)
		r.key("\x1b[200~text:3u\x1b[201~")
		wlWait(t, "the header to be removed", func() bool { _, c := r.headerCalls(); return c == 1 })
	})
}

func TestWelcomeOverlayThatTakesFocusPassesPrintableKeysToTheEditor(t *testing.T) {
	cases := map[string]struct {
		data  string
		paste string
	}{
		"a letter":          {"x", "x"},
		"text":              {"héllo 界", "héllo 界"},
		"a bracketed paste": {"\x1b[200~pasted\x1b[201~", "pasted"},
		"enter":             {"\r", ""},
		"escape":            {"\x1b", ""},
		"an arrow":          {"\x1b[A", ""},
		"a release":         {"x\x1b[120;1:3u", ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r, call := overlayRig(t)
			r.h.write(map[string]any{"type": "notify", "notify": map[string]any{"method": "ui.custom.input", "args": map[string]any{"key": call.Args["key"], "data": c.data}}})
			wlWait(t, "the overlay to be hidden", func() bool { return r.hides() == 1 })
			if c.paste != "" {
				wlWait(t, "the text to be pasted", func() bool { return len(r.h.CallsTo("ui.pasteToEditor")) > 0 })
			}
			r.quiescent()
			var pasted []string
			for _, p := range r.h.CallsTo("ui.pasteToEditor") {
				pasted = append(pasted, p.Args["text"].(string))
			}
			if c.paste == "" && len(pasted) != 0 || c.paste != "" && (len(pasted) != 1 || pasted[0] != c.paste) {
				t.Errorf("pasted = %q, want %q", pasted, c.paste)
			}
		})
	}
}

func TestWelcomeDismissedBeforeTheHostMountedItIsHiddenWhenItDoes(t *testing.T) {
	r := startWelcome(t, false, "tui", nil)
	r.startSession("startup")
	r.timers.runStartupWork()()
	wlWait(t, "the overlay", func() bool { return len(r.customCalls()) > 0 })
	r.h.Fire("agent_start", nil)
	r.quiescent()
	if r.hides() != 0 {
		t.Fatal("nothing to hide before the host mounted it")
	}
	r.mounted(r.customCalls()[0])
	wlWait(t, "the overlay to be hidden", func() bool { return r.hides() == 1 })
}

func TestWelcomeWatchesTheKeyboardOnlyWhileItIsPending(t *testing.T) {
	r := headerRig(t, nil)
	if n := r.watching.Load(); n != 1 {
		t.Errorf("welcome keyboard watches = %d, want 1 while pending", n)
	}
	r.key("x")
	wlWait(t, "the header to be removed by the key", func() bool { _, c := r.headerCalls(); return c == 1 })
	r.requireWatchEnded()
	r.key("y") // nothing is pending: no effect, nothing consumed
	r.quiescent()
	if _, c := r.headerCalls(); c != 1 {
		t.Errorf("header clears = %d, want 1", c)
	}
}

func TestWelcomeIsDecidedAfterTheBarIsInstalled(t *testing.T) {
	// upstream: remaining-regressions "startup welcome predicate respects powerline.welcome false" (the source assertion that
	// setupCustomEditor(ctx) runs before the shouldShowStartupWelcome decision).
	r := headerRig(t, nil)
	// The welcome's first visible act is installing its header; other slices subscribe to the keyboard or call the host while the
	// bar is set up, so the order is read from the footer and the header only.
	footer, header := -1, -1
	for i, c := range r.h.Calls() {
		switch {
		case c.Method == "ui.setFooter" && footer < 0:
			footer = i
		case c.Method == "ui.setHeader" && c.Args["clear"] != true && header < 0:
			header = i
		}
	}
	if footer < 0 || header < 0 || footer > header {
		t.Errorf("footer call at %d, welcome header at %d: the bar must be installed first", footer, header)
	}
}

func TestWelcomeHeaderContent(t *testing.T) {
	t.Run("recent sessions and the prompt estimate", func(t *testing.T) {
		r := headerRig(t, func(r *wlRig) {
			sessionFile := filepath.Join(r.home, ".pi", "agent", "sessions", "--x--", "s.jsonl")
			os.MkdirAll(filepath.Dir(sessionFile), 0o755)
			os.WriteFile(sessionFile, []byte(`{"cwd":"/work/pigpen"}`+"\n"), 0o644)
			r.prompt.Store(strings.Repeat("a", 7600))
		})
		out := r.lastHeader()
		for _, want := range []string{"• pigpen (just now)", "≈ 1.9k initial prompt tokens"} {
			if !strings.Contains(out, want) {
				t.Errorf("header lacks %q:\n%s", want, out)
			}
		}
	})
	t.Run("no model", func(t *testing.T) {
		r := headerRig(t, func(r *wlRig) { r.noModel.Store(true) })
		out := r.lastHeader()
		if !strings.Contains(out, "No model") || !strings.Contains(out, "Unknown") {
			t.Errorf("header lacks the model fallbacks:\n%s", out)
		}
	})
	t.Run("the header is drawn again at a new width", func(t *testing.T) {
		r := headerRig(t, nil)
		before, _ := r.headerCalls()
		r.h.write(map[string]any{"type": "notify", "notify": map[string]any{"method": "width_change", "args": map[string]any{"width": 120}}})
		wlWait(t, "the header at 120 columns", func() bool { i, _ := r.headerCalls(); return i > before })
		last := r.h.CallsTo("ui.setHeader")
		rows, _ := last[len(last)-1].Args["lines"].([]any)
		if first, _ := rows[0].(string); !strings.Contains(first, strings.Repeat("─", 10)) {
			t.Errorf("first row = %q", first)
		}
	})
}

func TestWelcomeNamesTheModelAsTheOriginalDoes(t *testing.T) {
	cases := map[string]struct {
		model map[string]any
		want  []string
	}{
		"name and provider": {map[string]any{"id": "m-id", "name": "Fancy Name", "provider": "acme-cloud"}, []string{"Fancy Name", "acme-cloud"}},
		"no name: the id":   {map[string]any{"id": "m-id", "name": "", "provider": "acme-cloud"}, []string{"m-id", "acme-cloud"}},
		"no provider":       {map[string]any{"id": "m-id", "name": "Fancy Name", "provider": ""}, []string{"Fancy Name", "Unknown"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := headerRig(t, func(r *wlRig) { r.model.Store(c.model) })
			out := r.lastHeader()
			for _, want := range c.want {
				if !strings.Contains(out, want) {
					t.Errorf("header lacks %q:\n%s", want, out)
				}
			}
		})
	}
	t.Run("the overlay reads the model at setup", func(t *testing.T) {
		// The original reads ctx.model when the overlay is set up and the header when its timer runs.
		r := startWelcome(t, false, "tui", nil)
		r.startSession("startup")
		r.model.Store(map[string]any{"id": "later", "name": "Later Model", "provider": "later-cloud"})
		before := len(r.h.CallsTo("getModelInfo"))
		r.timers.runStartupWork()()
		wlWait(t, "the overlay", func() bool { return len(r.customCalls()) > 0 })
		if after := len(r.h.CallsTo("getModelInfo")); after != before {
			t.Errorf("getModelInfo was called again at the timer (%d -> %d)", before, after)
		}
	})
}

func TestWelcomeEstimatesThePromptByFourCharactersAToken(t *testing.T) {
	r := headerRig(t, func(r *wlRig) { r.prompt.Store(strings.Repeat("a", 3996)) })
	if out := r.lastHeader(); !strings.Contains(out, "≈ 999 initial prompt tokens") {
		t.Errorf("header:\n%s", out)
	}
}

func TestWelcomeIsNotShownAfterThePowerlineWasToggledOff(t *testing.T) {
	r := startWelcome(t, true, "tui", nil)
	r.startSession("startup")
	r.h.Command("powerline", "") // off
	r.h.Fire("session_start", map[string]any{"reason": "startup"})
	r.timers.runStartupWork()()
	r.quiescent()
	if n := r.installations(); n != 0 {
		t.Errorf("installations = %d, want 0 while the powerline is off", n)
	}
}

func TestWelcomeEveryKeyReleaseSuffixIsARelease(t *testing.T) {
	for _, suffix := range []string{":3u", ":3~", ":3A", ":3B", ":3C", ":3D", ":3H", ":3F"} {
		t.Run(suffix, func(t *testing.T) {
			r := headerRig(t, nil)
			r.key("\x1b[1;1" + suffix)
			r.quiescent()
			if _, c := r.headerCalls(); c != 0 {
				t.Error("a key release removed the header")
			}
		})
	}
}

func TestWelcomeDeleteIsNotTextForTheEditor(t *testing.T) {
	r, call := overlayRig(t)
	r.h.write(map[string]any{"type": "notify", "notify": map[string]any{"method": "ui.custom.input", "args": map[string]any{"key": call.Args["key"], "data": "\x7f"}}})
	wlWait(t, "the overlay to be hidden", func() bool { return r.hides() == 1 })
	r.quiescent()
	if n := len(r.h.CallsTo("ui.pasteToEditor")); n != 0 {
		t.Errorf("pasted %d times", n)
	}
}

func TestWelcomeDismissalStopsThePendingTimer(t *testing.T) {
	r := startWelcome(t, true, "tui", nil)
	r.startSession("startup")
	if r.timers.pendingAfter() != 1 {
		t.Fatalf("pending timeouts = %d, want 1", r.timers.pendingAfter())
	}
	r.h.Fire("tool_call", map[string]any{"toolName": "read", "toolCallId": "c1", "input": map[string]any{}})
	if n := r.timers.pendingAfter(); n != 0 {
		t.Errorf("pending timeouts after the dismissal = %d, want 0 (clearTimeout)", n)
	}
}

func TestWelcomeHeaderIsRemovedOnlyAfterItWasInstalled(t *testing.T) {
	// The install and the removal are two host calls; a removal that overtook the install would leave the header up for good.
	r := startWelcome(t, true, "tui", nil)
	r.holdHeader = make(chan struct{})
	var released sync.Once
	release := func() { released.Do(func() { close(r.holdHeader) }) }
	t.Cleanup(release)
	r.startSession("startup")
	r.timers.runStartupWork()
	wlWait(t, "the install call", func() bool { i, _ := r.headerCalls(); return i == 1 })
	r.key("x") // dismisses while the install call is still outstanding
	r.quiescent()
	if _, c := r.headerCalls(); c != 0 {
		t.Fatal("the header was removed before its install call returned")
	}
	release()
	wlWait(t, "the removal", func() bool { _, c := r.headerCalls(); return c == 1 })
}

func TestWelcomeHeaderDismissedBetweenTheCheckAndTheInstallIsNotInstalled(t *testing.T) {
	r := startWelcome(t, true, "tui", nil)
	var once sync.Once
	t.Cleanup(powerline.SetWelcomeAfterHeaderMark(func() {
		once.Do(func() { r.h.Fire("agent_start", nil) })
	}))
	r.startSession("startup")
	r.timers.runStartupWork()()
	r.quiescent()
	if installs, _ := r.headerCalls(); installs != 0 {
		t.Errorf("the header was installed after it was dismissed (%d installs)", installs)
	}
}

func TestWelcomeOverlayThatTookFocusEndsAfterTheFirstKey(t *testing.T) {
	r, call := overlayRig(t)
	for _, key := range []string{"a", "b"} {
		r.h.write(map[string]any{"type": "notify", "notify": map[string]any{"method": "ui.custom.input", "args": map[string]any{"key": call.Args["key"], "data": key}}})
	}
	wlWait(t, "the first key to be pasted", func() bool { return len(r.h.CallsTo("ui.pasteToEditor")) > 0 })
	r.quiescent()
	if n := len(r.h.CallsTo("ui.pasteToEditor")); n != 1 {
		t.Errorf("pasted %d times: the second key reached a closed overlay", n)
	}
}

func TestWelcomeHeaderRemovalWaitsForAnInstallThatIsAlreadyDecided(t *testing.T) {
	// The install checked that the header is wanted, then a dismissal arrives. The removal must come after the install, or the
	// header it meant to remove would be drawn for good.
	r := startWelcome(t, true, "tui", nil)
	var once sync.Once
	t.Cleanup(powerline.SetWelcomeBeforeHeaderInstall(func() {
		once.Do(func() {
			go r.h.Fire("agent_start", nil)
			time.Sleep(150 * time.Millisecond) // the dismissal reaches the header lock
		})
	}))
	r.startSession("startup")
	r.timers.runStartupWork()()
	wlWait(t, "the removal", func() bool { _, c := r.headerCalls(); return c == 1 })
	var order []string
	for _, c := range r.h.CallsTo("ui.setHeader") {
		if c.Args["clear"] == true {
			order = append(order, "clear")
		} else {
			order = append(order, "install")
		}
	}
	if len(order) != 2 || order[0] != "install" || order[1] != "clear" {
		t.Errorf("header calls = %v, want install then clear", order)
	}
}
