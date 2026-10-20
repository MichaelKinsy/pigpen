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
	release    chan struct{}
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
	t.Cleanup(powerline.SetWelcomeTimers(r.timers))
	r.h = StartHost(t, powerline.Extension(), HostOptions{Mode: mode, Cwd: home, OnCallValue: func(method string, args map[string]any) (any, string) {
		switch method {
		case "getModelInfo":
			return map[string]any{"id": "test-model", "name": "Test model", "provider": "test", "reasoning": false, "contextWindow": 200000}, ""
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
			return map[string]any{"prompt": ""}, ""
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
				if n := len(r.h.CallsTo("ui.onTerminalInput")); n != 0 {
					t.Errorf("the keyboard was watched %d times in RPC mode", n)
				}
			})
		}
	})
}
