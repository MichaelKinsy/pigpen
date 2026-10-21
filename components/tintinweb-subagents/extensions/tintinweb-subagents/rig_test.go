package tintinweb_subagents

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

type obj = map[string]any

// fakeChild is a scripted child: it settles when released, or when aborted. A resumed run is released again.
type fakeChild struct {
	spec    childSpec
	release chan struct{}
	res     *childResult
	err     error
	session string // the session file it reports ("": in memory)
	dead    bool   // the process has ended: it cannot take a prompt

	mu      sync.Mutex
	steered []string
	aborted bool
	resumed []string
	closed  bool
	// what a live child reports (liveChild): the context percent and the conversation so far
	livePercent  *float64
	liveMessages []map[string]any
}

func (c *fakeChild) stats() *float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.livePercent
}
func (c *fakeChild) messages() []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.liveMessages
}

func (c *fakeChild) wait() (*childResult, error) {
	c.mu.Lock()
	release := c.release
	c.mu.Unlock()
	<-release
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.aborted {
		return &childResult{Text: "partial"}, nil
	}
	return c.res, c.err
}
func (c *fakeChild) steer(m string) error {
	c.mu.Lock()
	c.steered = append(c.steered, m)
	c.mu.Unlock()
	return nil
}
func (c *fakeChild) abort() {
	c.mu.Lock()
	c.aborted = true
	c.mu.Unlock()
	c.releaseNow()
}
func (c *fakeChild) releaseNow() {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.release:
	default:
		close(c.release)
	}
}
func (c *fakeChild) finish(text string) {
	c.mu.Lock()
	c.res = &childResult{Text: text, ToolUses: 3, Turns: 2}
	c.mu.Unlock()
	if c.spec.Hooks.Usage != nil {
		c.spec.Hooks.Usage(lifetimeUsage{Input: 1500}) // what the child's stream reports
	}
	c.releaseNow()
}
// die is the process ending because its context ended: the run in progress fails and no more prompts are taken.
func (c *fakeChild) die() {
	c.mu.Lock()
	c.dead = true
	if c.res == nil {
		c.err = errors.New("the child exited")
	}
	c.mu.Unlock()
	c.releaseNow()
}
func (c *fakeChild) sessionFile() string { return c.session }
func (c *fakeChild) alive() bool         { c.mu.Lock(); defer c.mu.Unlock(); return !c.dead && !c.closed }
func (c *fakeChild) close()              { c.mu.Lock(); c.closed = true; c.mu.Unlock() }
func (c *fakeChild) resume(prompt string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dead || c.closed {
		return errors.New("the agent's process has ended")
	}
	c.resumed = append(c.resumed, prompt)
	c.aborted = false
	c.release = make(chan struct{})
	return nil
}

// fleet starts fake children and remembers them.
type fleet struct {
	mu       sync.Mutex
	children []*fakeChild
	failWith error
	session  string // the session file every child reports
}

// start is a childStarter whose children are bound to ctx, as a process started with exec.CommandContext is: when the
// context ends the child dies, whatever it was doing and whether or not a run is in progress.
func (f *fleet) start(ctx context.Context, spec childSpec) (child, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return nil, f.failWith
	}
	c := &fakeChild{spec: spec, release: make(chan struct{}), session: f.session}
	f.children = append(f.children, c)
	go func() {
		<-ctx.Done()
		c.die()
	}()
	return c, nil
}

func (f *fleet) wait(t *testing.T, n int) []*fakeChild {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		if len(f.children) >= n {
			out := append([]*fakeChild{}, f.children...)
			f.mu.Unlock()
			return out
		}
		f.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("fewer than %d children started", n)
	return nil
}

// rig is a fake host for the subagents extension with an event bus: an emit reaches the extension's listeners
// and the Go-side ones.
type rig struct {
	*Host
	app   *app
	fleet *fleet

	exec func(command string, args []string) (stdout string, code int)
	// modelState answers the host's model registry (`ctx.ModelRegistry()`): {"models": [...], "providers": {...}}.
	modelState any
	// What the host answers about the parent session.
	sessionFile string // getSessionFile
	branch      []any  // sessionRead getBranch
	registry    []any  // getAllTools
	trusted     bool   // isProjectTrusted
	systemPrompt string // getSystemPrompt
	mu          sync.Mutex
	extBus      map[string][]string
	goBus       map[string][]func(any)
	emits       []string
}

func startRig(t *testing.T) *rig { return startRigIn(t, t.TempDir()) }

// startRigIn is startRig with the workspace the session starts in.
func startRigIn(t *testing.T, cwd string) *rig { return startRigTuned(t, cwd, nil) }

// startRigTuned is startRigIn with a change to the app made before the session starts.
func startRigTuned(t *testing.T, cwd string, tune func(*app)) *rig {
	t.Helper()
	resetTypes()
	t.Cleanup(resetTypes)
	// Hermetic: a rig never reads a subagents.json of the machine it runs on (a test that wants a global dir sets its own first).
	if os.Getenv("PIG_CODING_AGENT_DIR") == "" || os.Getenv("PIG_USE_PI_DIRS") == "1" {
		t.Setenv("PIG_USE_PI_DIRS", "")
		t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	}
	r := &rig{fleet: &fleet{}, extBus: map[string][]string{}, goBus: map[string][]func(any){}}
	e := sdk.New("tintinweb-subagents")
	r.app = newApp(e, r.fleet.start)
	if tune != nil {
		tune(r.app)
	}
	r.app.register(e)
	r.Host = StartHost(t, e, HostOptions{Cwd: cwd, OnCallValue: r.answer})
	r.Fire("session_start", obj{"reason": "startup"})
	t.Cleanup(func() { r.app.shutdown() })
	return r
}

func (r *rig) answer(method string, args map[string]any) (any, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch method {
	case "getModelRegistryState":
		if r.modelState != nil {
			return r.modelState, ""
		}
	case "sessionRead":
		if args["method"] == "getBranch" {
			return append([]any{}, r.branch...), ""
		}
	case "getSessionFile":
		return obj{"sessionFile": r.sessionFile}, ""
	case "isProjectTrusted":
		return obj{"trusted": r.trusted}, ""
	case "getAllTools":
		return obj{"tools": append([]any{}, r.registry...)}, ""
	case "getSystemPrompt":
		if r.systemPrompt != "" {
			return obj{"prompt": r.systemPrompt}, ""
		}
	case "getSessionID":
		return obj{"sessionId": "session-1"}, ""
	case "exec":
		if r.exec != nil {
			var argv []string
			for _, a := range args["args"].([]any) {
				argv = append(argv, a.(string))
			}
			out, code := r.exec(args["command"].(string), argv)
			return obj{"stdout": out, "stderr": "", "code": float64(code), "killed": false}, ""
		}
	case "events.on":
		ch, _ := args["channel"].(string)
		id, _ := args["handlerId"].(string)
		r.extBus[ch] = append(r.extBus[ch], id)
	case "events.emit":
		ch, _ := args["channel"].(string)
		r.emits = append(r.emits, ch)
		goL := append([]func(any){}, r.goBus[ch]...)
		ids := append([]string{}, r.extBus[ch]...)
		data := args["json"]
		go func() {
			for _, fn := range goL {
				fn(data)
			}
			for _, id := range ids {
				a, _ := json.Marshal(obj{"handlerId": id, "channel": ch, "json": data})
				r.Host.roundTrip(obj{"method": "events.dispatch", "args": json.RawMessage(a)})
			}
		}()
	}
	return obj{}, ""
}

func (r *rig) onBus(channel string, fn func(any)) {
	r.mu.Lock()
	r.goBus[channel] = append(r.goBus[channel], fn)
	r.mu.Unlock()
}

// request sends an RPC request on the bus and waits for its reply.
func (r *rig) request(channel string, req obj) obj {
	r.t.Helper()
	id := "req-" + time.Now().Format("150405.000000")
	req["requestId"] = id
	got := make(chan obj, 1)
	r.onBus(channel+":reply:"+id, func(d any) {
		m, _ := d.(map[string]any)
		got <- m
	})
	r.emit(channel, req)
	select {
	case m := <-got:
		return m
	case <-time.After(5 * time.Second):
		r.t.Fatalf("no reply on %s", channel)
	}
	return nil
}

func (r *rig) emit(ch string, data any) {
	r.mu.Lock()
	goL := append([]func(any){}, r.goBus[ch]...)
	ids := append([]string{}, r.extBus[ch]...)
	r.mu.Unlock()
	for _, fn := range goL {
		fn(data)
	}
	for _, id := range ids {
		a, _ := json.Marshal(obj{"handlerId": id, "channel": ch, "json": data})
		r.Host.roundTrip(obj{"method": "events.dispatch", "args": json.RawMessage(a)})
	}
}

func (r *rig) emitted(ch string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.emits {
		if c == ch {
			return true
		}
	}
	return false
}

func (r *rig) waitEmitted(ch string) {
	r.t.Helper()
	for i := 0; i < 1000; i++ {
		if r.emitted(ch) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.t.Fatalf("never emitted %s", ch)
}

// tool runs a tool; it returns the text, or the failure message.
func (r *rig) tool(name string, params obj) (string, string) {
	r.t.Helper()
	raw, failure := r.Host.Tool(name, params)
	if failure != "" {
		return "", failure
	}
	var res struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		r.t.Fatalf("tool result %s: %v", raw, err)
	}
	return res.Content, ""
}

func (r *rig) must(name string, params obj) string {
	r.t.Helper()
	text, failure := r.tool(name, params)
	if failure != "" {
		r.t.Fatalf("%s failed: %s", name, failure)
	}
	return text
}

func (r *rig) messages() []obj {
	var out []obj
	for _, c := range r.CallsTo("sendMessage") {
		out = append(out, c.Args)
	}
	return out
}

func idFrom(t *testing.T, text string) string {
	t.Helper()
	for _, l := range strings.Split(text, "\n") {
		if v, ok := strings.CutPrefix(l, "Agent ID: "); ok {
			return v
		}
	}
	t.Fatalf("no agent id in %q", text)
	return ""
}

var errBoom = errors.New("boom")

func (f *fleet) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.children) }
func (f *fleet) setFail(err error) {
	f.mu.Lock()
	f.failWith = err
	f.mu.Unlock()
}

// waitPrompt waits for the child that was given the prompt (children start in no fixed order).
func (f *fleet) waitPrompt(t *testing.T, prompt string) *fakeChild {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		for _, c := range f.children {
			if c.spec.Prompt == prompt {
				f.mu.Unlock()
				return c
			}
		}
		f.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no child was given %q", prompt)
	return nil
}
