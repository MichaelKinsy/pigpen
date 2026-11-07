package posthorse

// Test fixtures for the Posthorse twins: the Go equivalent of test/harness.ts and the `setup()` /
// helper block of test/posthorse.test.ts:36-400 (plus host-helpers.ts, results.ts, fixture-files.ts). Test code only.
//
// Companion file fixture_session_test.go holds the fake session manager (fakeSession) and the session-file / entry
// builders. fakehost_test.go and twin_test.go are the template copies and are not edited. Every package-level
// identifier of the fixture files is `fx*`, `fix*`, `fake*` or a `with*` option, so it cannot clash with production names.
//
// ---------------------------------------------------------------------------------------------------------------
// API SUMMARY (also in port/lane-TestInfra.md)
//
//	f := newFixture(t, opts...)       // the extension (newPosthorse(reader)) on the fake host + a fixture session
//	f.With(opts...) *fixture          // a VIEW: same host, per-call context override (usageContext, per-test cwd ...)
//	f.Set(opts...)                    // persistent change (a TS `let tokens` that the test mutates between turns)
//
// Context options (the TestContext fields of the original; every one is a fixOpt):
//
//	withUsage(window, tokens)  withUsageUnknown(window) (tokens:null)  withNoUsage() (getContextUsage undefined)
//	withReserve(n)  withEnabled(b)  withKeepRecent(n)  withModelWindow(n)  withSystemPrompt(s)  withPending(b)
//	withCwd(dir)  withSession(*fakeSession)  withSessionFile(path)|withNoSessionFile()  withSessionDir(dir)
//	withProjection(entries ...rec)  withRealProjection()  withEmptyProjection()  withActiveTools(names...)
//	withAppendToSession()  withHostAnswer(key, fn)  withHostError(key, msg)  withTokensDelta(d)
//
// Construction-only options: withFactory(func(policyReader) *sdk.Extension)  withPolicyReader(policyReader)
//
//	withHostMode("tui"|"rpc"|"json"|"print")  withStartCwd(dir)
//
// Executing:
//
//	res := f.Run("notes", rec{...})       // TS run(tools, name, params, ctx): fires tool_execution_start / _end around the tool
//	res, errText := f.RunErr(...)          // a tool that throws: errText = the error text, no tool_execution_end (as TS)
//	res.Text()  res.Details  res.Content   // toolText(result); details = PosthorseDisplay as a rec (validated like results.ts)
//	h := f.StartRun(name, params); h.Cancel("reason"); res, errText := h.Wait()   // AbortSignal.abort() mid-call
//	f.NotesIn(cwd, params)                 // the TS notesIn(): one in-memory session per cwd (a host per cwd, see below)
//
// Events (handlers.get(name)(event, ctx): the FIRST registered handler of the event, like the original's callbacks[0]):
//	f.BeforeAgentStart(prompt, options) fixPrompt  // before_agent_start: .Result, .Sections, .Options left by the handler
//	f.FireWire(event, data) rec            // the reply exactly as the SDK sent it (_pigBoundaryResult, _pigPrompt* wrappers)
//
//	f.Fire(event, data) rec                // nil = the handler returned undefined; f.FireAll(event, data) []rec = every handler
//	f.TurnEnd(toolResults ...rec) rec      // turnEnd(): returns the BoundaryResult ({entries, continue}) or nil
//	f.TurnEndEvent(event) rec              // a hand-built turn_end event
//	f.ContextEvent(messages ...rec) rec    // the `context` event: nil = unchanged (`undefined`), else {messages: [...]}
//	f.GuidanceFor() string                 // guidanceFor(): the context_with_system section "posthorse"
//	f.PageContext() *fixture               // pageContext(): a view whose usage is calibrated by the guidance tokens
//	f.AutomaticResult(branchEntries, projected...) rec / f.AutomaticHandoff(branchEntries) string  // session_before_compact
//
// Records and counters:
//
//	f.Messages() []fixSent                 // pi.sendMessage log (CustomType, Content string, Display, Details, Options)
//	f.UserMessages() []rec  f.Appended() []rec   // sendUserMessage / appendEntry logs
//	f.Reads("sessionRead.getEntry") int    // host reads by wire key: "sessionRead.<method>", "watchSessionLog", "getContextUsage" ...
//	f.CallsTo(method) []HostCall           // every host call of every host of the fixture
//	f.H() *Host                            // the host of the view's cwd (Command, Registered, Calls ...)
//	f.MutationLog() []string               // fileMutation.enter/exit log ("enter <abs path>", "exit <abs path>")
//
// Helpers without a fixture: entry builders (fxText, fxImage, fxToolCall, fxUser, fxAssistant, fxToolResult,
// fxMessageEntry, fxCustomMessage, fxCustom, fxCompaction, fxBoundary, fxContextWindow), session files (fxWriteSession,
// fxWriteArchive, fxArchivedID, fxHeader, fxJSONL, fxReadSession), fake sessions (fxMemorySession, fxIndexedSession,
// fxPersistedSession, fxOpenSession, fxForkSession), fakePolicy (a policyContext for budget unit tests), fxCheckDisplay /
// fxCheckResult (results.ts), fxSchemaCheck (the registered tool schema validation of setup()).
//
// ---------------------------------------------------------------------------------------------------------------
// MODEL AND KNOWN DIFFERENCES FROM THE TS HARNESS (read before porting a case)
//
//  1. Context is host state. TS passes a `context` object to each handler; here every `ctx.*` read is a host call that
//     the fixture answers from its state. A view's options apply for the duration of each call made through the view,
//     so usageContext(base, w, t) is f.With(withUsage(w, t)). Calls through two different views at the same time are
//     refused (the single host cannot answer two contexts at once); use Set for state that must change while a call runs.
//  2. ctx.cwd is the host's "ready" cwd, fixed per host. A view whose cwd differs from every started host starts a new
//     host (and extension instance) for that cwd, sharing recorders, tools and state; extension-internal state is
//     therefore per cwd. Messages, counters and the session are shared.
//  3. Session reads: the Go SDK reads the branch either through ctx.SessionManager() (wire `sessionRead`) or through its
//     replicated log (`watchSessionLog` once, then state pushes). Both are answered from the same fakeSession; the
//     fixture pushes a full `state_update` before a call when the session changed since the last push and the
//     extension has subscribed. Count reads with f.Reads: the replicated log answers locally after its first fetch, so a
//     "must not read history" assertion should count "watchSessionLog" and "sessionRead.getBranch" together.
//  4. Abort: ctx.signal.aborted is request cancellation (ctx.Done()). StartRun().Cancel() sends the host's cancel frame
//     while the call runs. A signal that is aborted BEFORE the handler starts cannot be staged deterministically on this
//     wire; port such cases with a gated host answer (withHostAnswer) and Cancel.
//  5. Tool schema: the fake host does not validate arguments (the real host does, before the handler). Run validates the
//     params against the registered JSON schema (fxSchemaCheck: type, properties, required, additionalProperties,
//     enum, const, items, anyOf/oneOf/allOf, min/max, minLength/maxLength, minItems/maxItems, pattern) exactly where
//     setup() asserts Check(registration.definition.parameters, params).
//  6. Default context: an implicit-ancestry session (indexedBranch([])) with an EMPTY model projection, session dir =
//     os.TempDir(), no session file, usage {tokens: 1000, window: 100000, percent: 1}, compaction {enabled, reserve
//     16384}, system prompt "You are a test assistant.", all registered tools active, appendEntry recorded only (the
//     original's setup() stubs it), cwd = a fresh t.TempDir(). withSession(a real fakeSession) switches the projection
//     to the real one unless withEmptyProjection() follows.
//  7. The default policy reader answers {enabled, reserveTokens} from the fixture's compaction settings and drops
//     keepRecentTokens, exactly like fixturePolicy (harness.ts:14-26).
//  8. fixture methods hold the view for the whole call; do not use t.Parallel with a fixture (sdk.WithFileMutationQueue
//     rides the process-wide "first running extension" connection).
//  9. Not modelled: the `signal` field of session_before_compact (use ctx.Done), Pi's compaction `systemMessage`,
//     SettingsManager graph objects, TypeBox `Check` beyond fxSchemaCheck's keyword set. PageContext's calibration is
//     dropped if a later view replaces the usage (the TS spread replaces the function the same way).

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// ---------------------------------------------------------------------------------------------------------------
// State

// fixUsage is one getContextUsage answer; nil Tokens / Percent are JS null.
type fixUsage struct {
	Tokens  *int
	Window  int
	Percent *float64
}

type fixProj int

const (
	fixProjAuto fixProj = iota // implicit sessions project nothing, real sessions project for real
	fixProjEmpty
	fixProjReal
	fixProjFixed
)

type fixAnswer func(args map[string]any) (any, string)

// fixState is what the fake host answers from.
type fixState struct {
	cwd             string
	session         *fakeSession
	sessionFile     *string
	sessionFileSet  bool
	sessionDir      *string
	sessionDirSet   bool
	usage           *fixUsage
	modelWindow     int
	enabled         bool
	reserve         int
	keepRecent      *int
	systemPrompt    string
	activeTools     []string
	activeSet       bool
	pending         bool
	appendToSession bool
	projection      fixProj
	fixed           []rec
	answers         map[string]fixAnswer
	tokensDelta     int

	// construction only
	factory  func(policyReader) *sdk.Extension
	policy   policyReader
	mode     string
	agentDir string
	settings rec
	live     bool
	extra    []string
}

func (s *fixState) clone() *fixState {
	c := *s
	c.answers = make(map[string]fixAnswer, len(s.answers))
	for k, v := range s.answers {
		c.answers[k] = v
	}
	return &c
}

type fixOpt func(*fixState)

func fxPercent(tokens, window int) *float64 {
	p := float64(tokens) / float64(window) * 100
	return &p
}

// withUsage: a context whose usage sits at `tokens` inside a window of `window` (usageContext / the default context).
func withUsage(window, tokens int) fixOpt {
	return func(s *fixState) {
		s.modelWindow = window
		s.usage = &fixUsage{Tokens: &tokens, Window: window, Percent: fxPercent(tokens, window)}
	}
}

// withUsageUnknown: usage with unknown tokens, `{tokens: null, contextWindow, percent: null}` (after a compaction).
func withUsageUnknown(window int) fixOpt {
	return func(s *fixState) {
		s.modelWindow = window
		s.usage = &fixUsage{Window: window}
	}
}

// withNoUsage: getContextUsage() is undefined.
func withNoUsage() fixOpt { return func(s *fixState) { s.usage = nil } }

// withTokensDelta shifts the tokens of the usage in effect when the call starts (pageContext's calibration).
func withTokensDelta(d int) fixOpt { return func(s *fixState) { s.tokensDelta += d } }

func withReserve(n int) fixOpt         { return func(s *fixState) { s.reserve = n } }
func withEnabled(b bool) fixOpt        { return func(s *fixState) { s.enabled = b } }
func withKeepRecent(n int) fixOpt      { return func(s *fixState) { s.keepRecent = &n } }
func withModelWindow(n int) fixOpt     { return func(s *fixState) { s.modelWindow = n } }
func withSystemPrompt(p string) fixOpt { return func(s *fixState) { s.systemPrompt = p } }
func withPending(b bool) fixOpt        { return func(s *fixState) { s.pending = b } }
func withCwd(dir string) fixOpt        { return func(s *fixState) { s.cwd = dir } }

// withSession replaces the session manager of the context.
func withSession(sess *fakeSession) fixOpt { return func(s *fixState) { s.session = sess } }

// withSessionFile / withNoSessionFile override getSessionFile independent of the session's own file.
func withSessionFile(path string) fixOpt {
	return func(s *fixState) { s.sessionFile, s.sessionFileSet = &path, true }
}
func withNoSessionFile() fixOpt {
	return func(s *fixState) { s.sessionFile, s.sessionFileSet = nil, true }
}
func withSessionDir(dir string) fixOpt {
	return func(s *fixState) { s.sessionDir, s.sessionDirSet = &dir, true }
}

// withProjection fixes buildSessionProjection to these entries: a rec with "sourceEntry" is a projected item
// `{sourceEntry, messages: [...]}`; any other rec is a source entry projected like the original's automaticResult
// default (type message -> its own message, else nothing).
func withProjection(entries ...rec) fixOpt {
	return func(s *fixState) { s.projection, s.fixed = fixProjFixed, entries }
}
func withRealProjection() fixOpt  { return func(s *fixState) { s.projection = fixProjReal } }
func withEmptyProjection() fixOpt { return func(s *fixState) { s.projection = fixProjEmpty } }

// withActiveTools restricts getActiveTools (default: every registered tool).
func withActiveTools(names ...string) fixOpt {
	return func(s *fixState) { s.activeTools, s.activeSet = names, true }
}

// withAppendToSession makes the `appendEntry` host call append a custom entry to the session (the tests that wire
// `appendEntry: (type, data) => sessionManager.appendCustomEntry(type, data)`).
func withAppendToSession() fixOpt { return func(s *fixState) { s.appendToSession = true } }

// withHostAnswer overrides one host read. key is the wire method ("getContextUsage", "getSystemPrompt", "exec", ...)
// or "sessionRead.<method>" ("sessionRead.getBranch", "sessionRead.getLeafId", ...) or "watchSessionLog". The
// function gets the call's args (for sessionRead: the inner args) and returns (result, "") or (nil, errorMessage); a
// nil result with no error is JSON null. Top-level getLeafID/getSessionFile/getSessionID honour the "sessionRead.*" key.
func withHostAnswer(key string, fn func(args map[string]any) (any, string)) fixOpt {
	return func(s *fixState) { s.answers[key] = fn }
}

// withHostError makes one host read fail with a host error (a ctx.sessionManager.getBranch that throws).
func withHostError(key, message string) fixOpt {
	return withHostAnswer(key, func(map[string]any) (any, string) { return nil, message })
}

func withFactory(fn func(policyReader) *sdk.Extension) fixOpt {
	return func(s *fixState) { s.factory = fn }
}
func withPolicyReader(r policyReader) fixOpt { return func(s *fixState) { s.policy = r } }
func withHostMode(mode string) fixOpt        { return func(s *fixState) { s.mode = mode } }
func withStartCwd(dir string) fixOpt         { return withCwd(dir) }

// withAgentDir sets the host's agent directory (PIG_EXT_HOST_INFO agentDir, which sdk.AgentDir reads); default: a fresh
// temp directory per fixture. Construction only.
func withAgentDir(dir string) fixOpt { return func(s *fixState) { s.agentDir = dir } }

// withSettings sets the effective settings the host replicates to the extension (`pi.getSettings()` / ctx.GetSettings();
// pushed in a state_update before the next call). Only useful together with withLivePolicy.
func withSettings(settings rec) fixOpt { return func(s *fixState) { s.settings = settings } }

// withLivePolicy passes a nil policy reader to newPosthorse (Extension()'s live path: Pi's settings through a
// SettingsManager). The fake host does not implement the SettingsManager graph objects that path needs, so a
// twin of the live reader is a harness scenario, not a layer-1 test; the option exists for the failure path
// (the extension must report the host error). Construction only.
func withLivePolicy() fixOpt { return func(s *fixState) { s.live = true } }

// withExtraTools lists more tool names in getAllTools (direct exposure, no namespace, description "", parameters {} =
// Type.Unknown()), like the original's `toolDefinitions.push({ name: "ask_question" })` after setup(). They are NOT
// active (setup()'s getActiveTools lists only the registered tools): add them with withActiveTools if a case needs it.
func withExtraTools(names ...string) fixOpt {
	return func(s *fixState) { s.extra = append(append([]string(nil), s.extra...), names...) }
}

// ---------------------------------------------------------------------------------------------------------------
// Group / views

type fixTool struct {
	Name        string
	Description string
	Parameters  json.RawMessage
	schema      any
}

// fixSent is one pi.sendMessage the extension made.
type fixSent struct {
	CustomType string
	Content    string
	Display    bool
	Details    rec // as given; nil when the message had no details
	Options    rec
}

// WindowID is details.windowId ("" when absent).
func (m fixSent) WindowID() string {
	w, _ := m.Details["windowId"].(string)
	return w
}

type fixHost struct {
	h           *Host
	cwd         string
	pushSession *fakeSession
	pushVersion uint64
	pushed      bool
	settings    string
}

type fixGroup struct {
	t       *testing.T
	mu      sync.Mutex
	base    *fixState
	cur     *fixState
	depth   int
	active  int
	nextVw  int
	hosts   map[string]*fixHost
	tools   []fixTool
	handler []string
	factory func(policyReader) *sdk.Extension

	sent     []fixSent
	userMsgs []rec
	appended []rec
	reads    map[string]int
	unknown  map[string]int
	sessions map[string]*fakeSession // NotesIn cache
	fm       fxFileMutations
}

// fixture is a view onto a group (see the header: f.With).
type fixture struct {
	g  *fixGroup
	id int
	ov []fixOpt
}

func fxDefaultState(t *testing.T) *fixState {
	tokens := 1000
	s := &fixState{
		cwd:          t.TempDir(),
		session:      fxIndexedSession(),
		usage:        &fixUsage{Tokens: &tokens, Window: 100_000, Percent: fxPercent(1000, 100_000)},
		modelWindow:  100_000,
		enabled:      true,
		reserve:      16_384,
		systemPrompt: "You are a test assistant.",
		answers:      map[string]fixAnswer{},
		mode:         "tui",
	}
	tmp := os.TempDir()
	s.sessionDir, s.sessionDirSet = &tmp, true
	return s
}

// newFixture starts the extension on the fake host with the default context (header, item 6).
func newFixture(t *testing.T, opts ...fixOpt) *fixture {
	t.Helper()
	base := fxDefaultState(t)
	for _, o := range opts {
		o(base)
	}
	g := &fixGroup{t: t, base: base, hosts: map[string]*fixHost{}, reads: map[string]int{}, unknown: map[string]int{}, sessions: map[string]*fakeSession{}}
	// The host facts of sdk.AgentDir / sdk.ConfigDirName (notesDir calls them for a project that has a .git).
	if base.agentDir == "" {
		base.agentDir = filepath.Join(t.TempDir(), "agent")
	}
	if err := os.MkdirAll(base.agentDir, 0o755); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	info, _ := json.Marshal(map[string]any{"agentDir": base.agentDir, "configDirName": ".pi", "version": "1.5.0", "packageDir": t.TempDir()})
	t.Setenv("PIG_EXT_HOST_INFO", string(info))
	g.factory = base.factory
	if g.factory == nil {
		g.factory = func(read policyReader) *sdk.Extension { return newPosthorse(read) }
	}
	g.tools, g.handler = fxCaptureRegistration(t, g.factory(g.policyFor(base)))
	f := &fixture{g: g}
	g.hostFor(base.cwd)
	t.Cleanup(func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		for method, n := range g.unknown {
			t.Errorf("fixture: the extension made %d host call(s) to %q that the fixture does not answer (add withHostAnswer)", n, method)
		}
	})
	return f
}

// policyFor is the default policy reader: fixturePolicy of harness.ts, over the state in effect at call time.
func (g *fixGroup) policyFor(base *fixState) policyReader {
	if base.live {
		return nil
	}
	if base.policy != nil {
		return base.policy
	}
	return func(sdk.Context) (compactionPolicy, error) {
		st := g.state()
		return compactionPolicy{enabled: st.enabled, reserveTokens: st.reserve}, nil
	}
}

func (g *fixGroup) effective() *fixState {
	if g.cur != nil {
		return g.cur
	}
	return g.base
}

// state is a copy of the state in effect now.
func (g *fixGroup) state() fixState {
	g.mu.Lock()
	defer g.mu.Unlock()
	return *g.effective()
}

// With returns a view whose options apply during the calls made through it.
func (f *fixture) With(opts ...fixOpt) *fixture {
	f.g.mu.Lock()
	f.g.nextVw++
	id := f.g.nextVw
	f.g.mu.Unlock()
	return &fixture{g: f.g, id: id, ov: append(append([]fixOpt(nil), f.ov...), opts...)}
}

// Set changes the persistent state (and the call in progress, if any).
func (f *fixture) Set(opts ...fixOpt) {
	g := f.g
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, o := range opts {
		o(g.base)
		if g.cur != nil {
			o(g.cur)
		}
	}
}

// Session is the session the view resolves to.
func (f *fixture) Session() *fakeSession { return f.resolve().session }

func (f *fixture) resolve() *fixState {
	g := f.g
	g.mu.Lock()
	c := g.base.clone()
	g.mu.Unlock()
	for _, o := range f.ov {
		o(c)
	}
	return c
}

// enter makes the view's state the effective state for one call and returns the host and the leave function.
func (f *fixture) enter() (*fixHost, func()) {
	f.g.t.Helper()
	g := f.g
	g.mu.Lock()
	if g.depth > 0 && g.active != f.id {
		g.mu.Unlock()
		g.t.Fatalf("fixture: concurrent calls through two different views are not supported; use f.Set for state that changes during a call")
	}
	if g.depth == 0 {
		st := g.base.clone()
		for _, o := range f.ov {
			o(st)
		}
		if st.tokensDelta != 0 && st.usage != nil && st.usage.Tokens != nil {
			tokens := *st.usage.Tokens + st.tokensDelta
			st.usage = &fixUsage{Tokens: &tokens, Window: st.usage.Window, Percent: fxPercent(tokens, st.usage.Window)}
			st.tokensDelta = 0
		}
		g.cur, g.active = st, f.id
	}
	g.depth++
	cwd := g.cur.cwd
	g.mu.Unlock()
	host := g.hostFor(cwd)
	host.syncMirror(g)
	host.syncSettings(g)
	return host, func() {
		g.mu.Lock()
		g.depth--
		if g.depth == 0 {
			g.cur = nil
		}
		g.mu.Unlock()
	}
}

// H is the host serving the view's cwd.
func (f *fixture) H() *Host {
	host, leave := f.enter()
	leave()
	return host.h
}

// hostFor returns (starting on demand) the host whose ready cwd is cwd.
func (g *fixGroup) hostFor(cwd string) *fixHost {
	g.t.Helper()
	g.mu.Lock()
	if h := g.hosts[cwd]; h != nil {
		g.mu.Unlock()
		return h
	}
	mode := g.base.mode
	g.mu.Unlock()
	ext := g.factory(g.policyFor(g.base))
	fh := &fixHost{cwd: cwd}
	fh.h = StartHost(g.t, ext, HostOptions{Mode: mode, Cwd: cwd, OnCallValue: func(method string, args map[string]any) (any, string) {
		return g.answer(fh, method, args)
	}})
	g.mu.Lock()
	g.hosts[cwd] = fh
	g.mu.Unlock()
	return fh
}

// ---------------------------------------------------------------------------------------------------------------
// Registration capture (tools for getAllTools / schema validation)

// fxCaptureRegistration runs a throw-away instance of the extension against a bare pipe and reads its register frame.
func fxCaptureRegistration(t *testing.T, ext *sdk.Extension) ([]fixTool, []string) {
	t.Helper()
	hostSide, extSide := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- ext.RunWithConn(extSide) }()
	defer func() {
		_ = hostSide.Close()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Errorf("fixture: the registration probe did not stop")
		}
	}()
	for {
		var hdr [4]byte
		if _, err := io.ReadFull(hostSide, hdr[:]); err != nil {
			t.Fatalf("fixture: read registration: %v", err)
		}
		data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
		if _, err := io.ReadFull(hostSide, data); err != nil {
			t.Fatalf("fixture: read registration: %v", err)
		}
		var f struct {
			Type     string `json:"type"`
			ID       string `json:"id"`
			Register *struct {
				Tools []struct {
					Name        string          `json:"name"`
					Description string          `json:"description"`
					Parameters  json.RawMessage `json:"parameters"`
				} `json:"tools"`
				Handlers []struct {
					Event string `json:"event"`
				} `json:"handlers"`
			} `json:"register"`
		}
		if err := json.Unmarshal(data, &f); err != nil {
			t.Fatalf("fixture: decode registration frame: %v", err)
		}
		switch f.Type {
		case "call":
			reply, _ := json.Marshal(map[string]any{"type": "call_result", "id": f.ID, "call_result": map[string]any{"result": map[string]any{}}})
			var out [4]byte
			binary.BigEndian.PutUint32(out[:], uint32(len(reply)))
			_, _ = hostSide.Write(append(out[:], reply...))
		case "register":
			var tools []fixTool
			for _, tl := range f.Register.Tools {
				var schema any
				_ = json.Unmarshal(tl.Parameters, &schema)
				tools = append(tools, fixTool{Name: tl.Name, Description: tl.Description, Parameters: tl.Parameters, schema: schema})
			}
			var events []string
			for _, h := range f.Register.Handlers {
				events = append(events, h.Event)
			}
			return tools, events
		}
	}
}

// ToolNames is the registered tool names in registration order.
func (f *fixture) ToolNames() []string {
	var out []string
	for _, tl := range f.g.tools {
		out = append(out, tl.Name)
	}
	return out
}

// RegisteredEvents is the event name of every registered handler, in registration order (duplicates kept).
func (f *fixture) RegisteredEvents() []string { return append([]string(nil), f.g.handler...) }

// HandlerCount is how many handlers the extension registered for event.
func (f *fixture) HandlerCount(event string) int {
	n := 0
	for _, e := range f.g.handler {
		if e == event {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------------------------------------------------------
// Host answers

var fxNull = json.RawMessage("null")

// fxFileMutations is the host side of sdk.WithFileMutationQueue: a FIFO turn per absolute path.
type fxFileMutations struct {
	mu      sync.Mutex
	tails   map[string]chan struct{}
	tickets map[string]chan struct{}
	paths   map[string]string
	n       int
	log     []string
}

func (m *fxFileMutations) enter(path string) string {
	m.mu.Lock()
	if m.tails == nil {
		m.tails, m.tickets, m.paths = map[string]chan struct{}{}, map[string]chan struct{}{}, map[string]string{}
	}
	prev := m.tails[path]
	mine := make(chan struct{})
	m.tails[path] = mine
	m.n++
	ticket := "ticket-" + strconv.Itoa(m.n)
	m.tickets[ticket], m.paths[ticket] = mine, path
	m.mu.Unlock()
	if prev != nil {
		<-prev
	}
	m.mu.Lock()
	m.log = append(m.log, "enter "+path)
	m.mu.Unlock()
	return ticket
}

func (m *fxFileMutations) exit(ticket string) {
	m.mu.Lock()
	mine, path := m.tickets[ticket], m.paths[ticket]
	delete(m.tickets, ticket)
	delete(m.paths, ticket)
	m.log = append(m.log, "exit "+path)
	m.mu.Unlock()
	if mine != nil {
		close(mine)
	}
}

// MutationLog is the sequence of fileMutation.enter / exit the extension made.
func (f *fixture) MutationLog() []string {
	f.g.fm.mu.Lock()
	defer f.g.fm.mu.Unlock()
	return append([]string(nil), f.g.fm.log...)
}

func fxStrOrNull(s string) any {
	if s == "" {
		return fxNull
	}
	return s
}

func (g *fixGroup) count(key string) {
	g.mu.Lock()
	g.reads[key]++
	g.mu.Unlock()
}

// Reads counts the host reads with this wire key (see withHostAnswer).
func (f *fixture) Reads(key string) int {
	f.g.mu.Lock()
	defer f.g.mu.Unlock()
	return f.g.reads[key]
}

// ResetReads zeroes the read counters.
func (f *fixture) ResetReads() {
	f.g.mu.Lock()
	f.g.reads = map[string]int{}
	f.g.mu.Unlock()
}

// CallsTo is every host call to method, across the fixture's hosts.
func (f *fixture) CallsTo(method string) []HostCall {
	f.g.mu.Lock()
	hosts := make([]*fixHost, 0, len(f.g.hosts))
	for _, h := range f.g.hosts {
		hosts = append(hosts, h)
	}
	f.g.mu.Unlock()
	var out []HostCall
	for _, h := range hosts {
		out = append(out, h.h.CallsTo(method)...)
	}
	return out
}

// Messages is the pi.sendMessage log.
func (f *fixture) Messages() []fixSent {
	f.g.mu.Lock()
	defer f.g.mu.Unlock()
	return append([]fixSent(nil), f.g.sent...)
}

// UserMessages is the sendUserMessage log; Appended the appendEntry log (`{customType, data}`).
func (f *fixture) UserMessages() []rec {
	f.g.mu.Lock()
	defer f.g.mu.Unlock()
	return append([]rec(nil), f.g.userMsgs...)
}

func (f *fixture) Appended() []rec {
	f.g.mu.Lock()
	defer f.g.mu.Unlock()
	return append([]rec(nil), f.g.appended...)
}

var fxBenign = []string{"ui.", "events.", "setStatus", "setWorkingMessage", "setTitle", "setLabel", "setSessionName", "setActiveTools", "refreshTools", "shutdown", "abort"}

func (g *fixGroup) answer(fh *fixHost, method string, args map[string]any) (any, string) {
	st := g.state()
	key := method
	inner := args
	if method == "sessionRead" {
		name, _ := args["method"].(string)
		key = "sessionRead." + name
		inner, _ = args["args"].(map[string]any)
	}
	g.count(key)
	if method != key {
		g.count(method)
	}
	if fn, ok := st.answers[key]; ok {
		return fn(inner)
	}
	switch method {
	case "sessionRead":
		name, _ := args["method"].(string)
		return g.sessionValue(&st, name, inner)
	case "watchSessionLog":
		cursor := 0
		if c, ok := args["cursor"].(float64); ok {
			cursor = int(c)
		}
		entries := st.session.getEntries()
		page := []any{}
		for i := cursor; i < len(entries); i++ {
			page = append(page, entries[i])
		}
		leaf := ""
		if v, e := g.sessionValue(&st, "getLeafId", nil); e == "" {
			leaf, _ = v.(string)
		}
		return rec{"entries": page, "entryCount": len(entries), "hasMore": false, "leafId": leaf}, ""
	case "getContextUsage":
		if st.usage == nil {
			return fxNull, ""
		}
		out := rec{"contextWindow": st.usage.Window, "tokens": nil, "percent": nil}
		if st.usage.Tokens != nil {
			out["tokens"] = *st.usage.Tokens
		}
		if st.usage.Percent != nil {
			out["percent"] = *st.usage.Percent
		}
		return out, ""
	case "getSystemPrompt":
		return rec{"prompt": st.systemPrompt}, ""
	case "getActiveTools":
		names := st.activeTools
		if !st.activeSet {
			names = nil
			for _, tl := range g.tools {
				names = append(names, tl.Name)
			}
		}
		if names == nil {
			names = []string{}
		}
		return rec{"tools": names}, ""
	case "getAllTools":
		list := make([]any, 0, len(g.tools))
		for _, tl := range g.tools {
			list = append(list, rec{"name": tl.Name, "description": tl.Description, "parameters": tl.Parameters, "exposure": "direct",
				"sourceInfo": rec{"path": "<fixture>", "source": "extension", "scope": "temporary", "origin": "top-level"}})
		}
		for _, name := range st.extra {
			list = append(list, rec{"name": name, "description": "", "parameters": rec{}, "exposure": "direct",
				"sourceInfo": rec{"path": "<fixture>", "source": "extension", "scope": "temporary", "origin": "top-level"}})
		}
		return rec{"tools": list}, ""
	case "hasPendingMessages":
		return rec{"pending": st.pending}, ""
	case "getSessionFile":
		v, e := g.sessionValue(&st, "getSessionFile", nil)
		if e != "" {
			return nil, e
		}
		s, _ := v.(string)
		return rec{"sessionFile": s}, ""
	case "getSessionID":
		v, e := g.sessionValue(&st, "getSessionId", nil)
		if e != "" {
			return nil, e
		}
		return rec{"sessionId": v}, ""
	case "getLeafID":
		v, e := g.sessionValue(&st, "getLeafId", nil)
		if e != "" {
			return nil, e
		}
		s, _ := v.(string)
		return rec{"leafId": s}, ""
	case "getSessionName":
		return rec{"name": ""}, ""
	case "getModelInfo":
		return rec{"id": "fixture-model", "name": "fixture-model", "provider": "fixture", "contextWindow": st.modelWindow, "maxOutputTokens": 1000}, ""
	case "isIdle":
		return rec{"idle": true}, ""
	case "isProjectTrusted":
		return rec{"trusted": true}, ""
	case "sendMessage":
		g.recordMessage(args)
		return rec{}, ""
	case "sendUserMessage":
		g.mu.Lock()
		g.userMsgs = append(g.userMsgs, args)
		g.mu.Unlock()
		return rec{}, ""
	case "appendEntry":
		g.mu.Lock()
		g.appended = append(g.appended, args)
		g.mu.Unlock()
		if st.appendToSession {
			ct, _ := args["customType"].(string)
			if st.session.implicit {
				st.session.push(fxCustom("", ct, args["data"]))
			} else {
				st.session.appendCustomEntry(ct, args["data"])
			}
		}
		return rec{}, ""
	case "fileMutation.enter":
		path, _ := args["path"].(string)
		return rec{"ticket": g.fm.enter(path)}, ""
	case "fileMutation.exit":
		ticket, _ := args["ticket"].(string)
		g.fm.exit(ticket)
		return rec{}, ""
	}
	for _, p := range fxBenign {
		if strings.HasPrefix(method, p) {
			return rec{}, ""
		}
	}
	g.mu.Lock()
	g.unknown[method]++
	g.mu.Unlock()
	return rec{}, ""
}

func (g *fixGroup) recordMessage(args map[string]any) {
	m, _ := args["message"].(map[string]any)
	opts, _ := args["options"].(map[string]any)
	sm := fixSent{Options: opts}
	sm.CustomType, _ = m["customType"].(string)
	content, isString := m["content"].(string)
	if !isString {
		g.t.Errorf("fixture: sendMessage content must be a string (setup() asserts it), got %T", m["content"])
	}
	sm.Content = content
	sm.Display, _ = m["display"].(bool)
	if d, ok := m["details"].(map[string]any); ok {
		if w, has := d["windowId"]; has {
			if _, isStr := w.(string); !isStr {
				g.t.Errorf("fixture: sendMessage details.windowId must be a string (setup() asserts it), got %T", w)
			}
		}
		sm.Details = d
	}
	g.mu.Lock()
	g.sent = append(g.sent, sm)
	g.mu.Unlock()
}

// sessionValue answers one ReadonlySessionManager read (wire sessionRead) from the state.
func (g *fixGroup) sessionValue(st *fixState, name string, args map[string]any) (any, string) {
	if over, ok := st.answers["sessionRead."+name]; ok {
		v, e := over(args)
		if v == nil && e == "" {
			return fxNull, ""
		}
		return v, e
	}
	s := st.session
	switch name {
	case "getCwd":
		return st.cwd, ""
	case "getSessionDir":
		if st.sessionDirSet && st.sessionDir != nil {
			return *st.sessionDir, ""
		}
		return s.getSessionDir(), ""
	case "getSessionId":
		return s.sessionID(), ""
	case "getSessionFile":
		file := s.getSessionFile()
		if st.sessionFileSet {
			file = ""
			if st.sessionFile != nil {
				file = *st.sessionFile
			}
		}
		return fxStrOrNull(file), ""
	case "getSessionName":
		return fxNull, ""
	case "getLeafId":
		if l := s.getLeafID(); l != nil {
			return *l, ""
		}
		return fxNull, ""
	case "getHeader":
		return s.getHeader(), ""
	case "getLeafEntry":
		if l := s.getLeafID(); l != nil {
			if e := s.getEntry(*l); e != nil {
				return e, ""
			}
		}
		return fxNull, ""
	case "getEntry":
		id, _ := args["id"].(string)
		if e := s.getEntry(id); e != nil {
			return e, ""
		}
		return fxNull, ""
	case "getLabel":
		id, _ := args["id"].(string)
		if l := s.getLabel(id); l != nil {
			return *l, ""
		}
		return fxNull, ""
	case "getEntries":
		return fxRecs(s.getEntries()), ""
	case "getBranch":
		var from *string
		if id, ok := args["fromId"].(string); ok {
			from = &id
		}
		return fxRecs(s.getBranch(from)), ""
	case "getChildren":
		var parent *string
		if id, ok := args["parentId"].(string); ok {
			parent = &id
		}
		return fxRecs(s.getChildren(parent)), ""
	case "getTree":
		return s.getTree(), ""
	case "buildContextEntries":
		return fxRecs(s.buildContextEntries()), ""
	case "buildSessionProjection":
		return g.projection(st), ""
	case "buildSessionContext":
		p := g.projection(st)
		return rec{"messages": p["messages"], "thinkingLevel": p["thinkingLevel"], "model": p["model"]}, ""
	case "isPersisted":
		return s.persist, ""
	case "usesDefaultSessionDir":
		return false, ""
	}
	return nil, fmt.Sprintf("fixture: sessionRead %q is not modelled", name)
}

func fxRecs(list []rec) []any {
	out := make([]any, len(list))
	for i, e := range list {
		out[i] = e
	}
	return out
}

func (g *fixGroup) projection(st *fixState) rec {
	mode := st.projection
	if mode == fixProjAuto {
		mode = fixProjReal
		if st.session.implicit {
			mode = fixProjEmpty
		}
	}
	switch mode {
	case fixProjEmpty:
		return rec{"entries": []any{}, "messages": []any{}, "thinkingLevel": "off", "model": nil}
	case fixProjFixed:
		return fxFixedProjection(st.fixed)
	}
	return st.session.buildSessionProjection()
}

// fxFixedProjection is a projection of the given entries (see withProjection).
func fxFixedProjection(entries []rec) rec {
	list := make([]any, 0, len(entries))
	messages := []any{}
	for _, e := range entries {
		item := e
		if _, ok := e["sourceEntry"]; !ok {
			ms := []any{}
			if e["type"] == "message" {
				ms = append(ms, e["message"])
			}
			item = rec{"sourceEntry": e, "messages": ms}
		}
		list = append(list, item)
		switch ms := item["messages"].(type) {
		case []any:
			messages = append(messages, ms...)
		case []rec:
			for _, m := range ms {
				messages = append(messages, m)
			}
		}
	}
	return rec{"entries": list, "messages": messages, "thinkingLevel": "off", "model": nil}
}

// syncSettings replicates the fixture's settings (withSettings) when they changed since the last push.
func (fh *fixHost) syncSettings(g *fixGroup) {
	st := g.state()
	if st.settings == nil {
		return
	}
	raw, _ := json.Marshal(st.settings)
	if string(raw) == fh.settings {
		return
	}
	fh.settings = string(raw)
	fh.h.write(map[string]any{"type": "notify", "notify": map[string]any{"method": "state_update", "args": map[string]any{"state": map[string]any{"settings": json.RawMessage(raw)}}}})
}

// syncMirror pushes the session as a full state_update when it changed and the extension subscribed to the replicated log.
func (fh *fixHost) syncMirror(g *fixGroup) {
	if len(fh.h.CallsTo("watchSessionLog")) == 0 {
		return
	}
	st := g.state()
	s := st.session
	s.mu.Lock()
	version := s.version
	s.mu.Unlock()
	if fh.pushed && fh.pushSession == s && fh.pushVersion == version {
		return
	}
	fh.pushed, fh.pushSession, fh.pushVersion = true, s, version
	entries := s.getEntries()
	leaf := ""
	if l := s.getLeafID(); l != nil {
		leaf = *l
	}
	fh.h.write(map[string]any{"type": "notify", "notify": map[string]any{"method": "state_update", "args": map[string]any{"state": map[string]any{
		"session": map[string]any{"sessionId": s.sessionID(), "leafId": leaf, "entriesAppended": fxRecs(entries), "entryCount": len(entries)},
	}}}})
}

// ---------------------------------------------------------------------------------------------------------------
// Tool results (results.ts) and execution (setup().tools)

// fixResult is a tool result normalised to Pi's AgentToolResult shape: content blocks plus details.
type fixResult struct {
	Content []rec
	Details rec
	IsError bool
	Raw     json.RawMessage
}

// Text is toolText(): the text of every content part, joined with "\n".
func (r fixResult) Text() string {
	parts := make([]string, len(r.Content))
	for i, c := range r.Content {
		parts[i], _ = c["text"].(string)
	}
	return strings.Join(parts, "\n")
}

// Images is the image blocks of the result.
func (r fixResult) Images() []rec {
	var out []rec
	for _, c := range r.Content {
		if c["type"] == "image" {
			out = append(out, c)
		}
	}
	return out
}

// fxParseResult decodes the SDK's wire tool result ({content: string | blocks, details, is_error, ...}).
func fxParseResult(raw json.RawMessage) (fixResult, error) {
	res := fixResult{Raw: raw}
	var w struct {
		Content any  `json:"content"`
		Details any  `json:"details"`
		IsError bool `json:"is_error"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		return res, fmt.Errorf("tool result %s: %v", raw, err)
	}
	res.IsError = w.IsError
	switch c := w.Content.(type) {
	case string:
		res.Content = []rec{fxText(c)}
	case []any:
		for _, p := range c {
			b, ok := p.(map[string]any)
			if !ok {
				return res, fmt.Errorf("tool result content part %v is not an object", p)
			}
			res.Content = append(res.Content, b)
		}
	case nil:
		res.Content = []rec{}
	default:
		return res, fmt.Errorf("tool result content is %T", w.Content)
	}
	if d, ok := w.Details.(map[string]any); ok {
		res.Details = d
	} else if w.Details != nil {
		return res, fmt.Errorf("tool result details is %T, not an object", w.Details)
	}
	return res, nil
}

func fxIsNum(v any) bool  { _, ok := v.(float64); return ok }
func fxIsStr(v any) bool  { _, ok := v.(string); return ok }
func fxIsBool(v any) bool { _, ok := v.(bool); return ok }
func fxNumOrNull(v any) bool {
	return v == nil || fxIsNum(v)
}
func fxObjOK(v any, key string, check func(any) bool) bool {
	m, _ := v.(map[string]any)
	x, present := m[key]
	return present && check(x)
}
func fxOptOK(m map[string]any, key string, check func(any) bool) bool {
	x, present := m[key]
	return !present || check(x)
}

// fxCheckDisplay is fixtureDisplay (results.ts:8-61): the details of a Posthorse tool result must be one of the display
// shapes. It returns the first violation, nil when valid. TypeBox objects admit extra members.
func fxCheckDisplay(d rec) error {
	kind, _ := d["kind"].(string)
	span := func(v any) bool {
		return fxObjOK(v, "headerLength", fxIsNum) && fxObjOK(v, "length", fxIsNum)
	}
	page := func(m rec) bool {
		return fxObjOK(m, "offset", fxIsNum) && fxObjOK(m, "end", fxIsNum) && fxObjOK(m, "total", fxIsNum)
	}
	fail := func(why string) error { return fmt.Errorf("invalid Posthorse display metadata (%s): %s", kind, why) }
	switch kind {
	case "context":
		if u, ok := d["usage"]; ok {
			m, isMap := u.(map[string]any)
			if !isMap || !fxObjOK(m, "tokens", fxNumOrNull) || !fxObjOK(m, "contextWindow", fxIsNum) || !fxObjOK(m, "percent", fxNumOrNull) {
				return fail("usage")
			}
		}
		if r, ok := d["rollover"]; ok {
			if s, _ := r.(string); s != "enabled" && s != "disabled" && s != "unsupported" {
				return fail("rollover")
			}
		}
		if !fxOptOK(d, "rolloverAt", fxIsNum) {
			return fail("rolloverAt")
		}
	case "notes-list", "notes-search":
		if !fxObjOK(d, "count", fxIsNum) || !fxOptOK(d, "headerLength", fxIsNum) || !fxOptOK(d, "more", fxIsBool) {
			return fail("count/headerLength/more")
		}
		if p, ok := d["page"]; ok {
			m, isMap := p.(map[string]any)
			if !isMap || !page(m) {
				return fail("page")
			}
		}
	case "note-read":
		if !page(d) || !fxOptOK(d, "headerLength", fxIsNum) {
			return fail("page/headerLength")
		}
	case "note-write", "note-append", "new-context":
	case "history-search":
		list, isList := d["entries"].([]any)
		if !isList {
			return fail("entries")
		}
		for _, e := range list {
			if !span(e) {
				return fail("entries[]")
			}
		}
		if !fxOptOK(d, "footerLength", fxIsNum) || !fxOptOK(d, "more", fxIsBool) || !fxOptOK(d, "skipped", fxIsNum) {
			return fail("footerLength/more/skipped")
		}
	case "history-read":
		if !page(d) || !fxObjOK(d, "headerLength", fxIsNum) || !fxOptOK(d, "imageOffset", fxIsNum) || !fxOptOK(d, "imageEnd", fxIsNum) || !fxOptOK(d, "imageTotal", fxIsNum) {
			return fail("page/headerLength/image*")
		}
	default:
		return fmt.Errorf("invalid Posthorse display metadata: unknown kind %q", kind)
	}
	return nil
}

// fxCheckResult is fixtureResult (results.ts:62-84): text/image content parts and valid display details.
func fxCheckResult(r fixResult) error {
	for _, c := range r.Content {
		switch c["type"] {
		case "text":
			if !fxIsStr(c["text"]) {
				return fmt.Errorf("invalid Posthorse tool result: text part without a string text: %v", c)
			}
		case "image":
			if !fxIsStr(c["data"]) || !fxOptOK(c, "mimeType", fxIsStr) {
				return fmt.Errorf("invalid Posthorse tool result: bad image part: %v", c)
			}
		default:
			return fmt.Errorf("invalid Posthorse tool result: unknown content type %v", c["type"])
		}
	}
	if r.Details != nil {
		return fxCheckDisplay(r.Details)
	}
	return nil
}

// ---- requests that can be cancelled -----------------------------------------------------------------------------

type fxReq struct {
	h  *Host
	id string
	ch chan json.RawMessage
}

func fxStartRequest(h *Host, request map[string]any) *fxReq {
	h.mu.Lock()
	h.nextID++
	id := fmt.Sprintf("r%d", h.nextID)
	ch := make(chan json.RawMessage, 1)
	h.pending[id] = ch
	h.mu.Unlock()
	h.write(map[string]any{"type": "request", "id": id, "request": request})
	return &fxReq{h: h, id: id, ch: ch}
}

func (r *fxReq) cancel(reason string) {
	c := map[string]any{"request_id": r.id}
	if reason != "" {
		c["reason"] = reason
	}
	r.h.write(map[string]any{"type": "cancel", "id": r.id, "cancel": c})
}

// fxEmptyError is the text RunErr/Wait report for a tool that threw an error with an EMPTY message (paging.ts refuse()
// does when even the refusal text no longer fits): the host stores "" for it, so a caller that tests `text != ""` would
// take the failure for a success. RunFailed / WaitFailed return the exact text and the failed flag.
const fxEmptyError = "(the tool failed with an empty error message)"

// fxWaitTimeout bounds how long a tool call, event or command waits for the extension's reply. A test with a very long
// single call (43,000 host round trips under -race) raises it for its own duration and restores it.
var fxWaitTimeout = 10 * time.Second

// waitFailed returns the reply and, when the reply carried an error object, its message (possibly "") and failed = true.
func (r *fxReq) waitFailed() (raw json.RawMessage, message string, failed bool) {
	r.h.t.Helper()
	select {
	case result := <-r.ch:
		r.h.mu.Lock()
		message, failed = r.h.failures[r.id]
		r.h.mu.Unlock()
		return result, message, failed
	case <-time.After(fxWaitTimeout):
		r.h.t.Fatalf("fixture: no response to request %s within %v", r.id, fxWaitTimeout)
		return nil, "", false
	}
}

func (r *fxReq) wait() (json.RawMessage, string) {
	r.h.t.Helper()
	raw, message, failed := r.waitFailed()
	if failed && message == "" {
		message = fxEmptyError
	}
	return raw, message
}

// fxNormalize passes v through JSON so numbers are float64 like the wire's.
func fxNormalize(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("fixture: %v", err))
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		panic(fmt.Sprintf("fixture: %v", err))
	}
	return out
}

func fxToolRequest(name string, params rec, id string) map[string]any {
	if params == nil {
		params = rec{}
	}
	argv, _ := json.Marshal(params)
	return map[string]any{"method": "tool_call", "tool": name, "tool_call_id": id, "args": json.RawMessage(argv)}
}

func fxEventRequest(event string, handlerID int, data rec) map[string]any {
	d := fxClone(data)
	d["type"] = event
	args, _ := json.Marshal(d)
	return map[string]any{"method": "event", "event": event, "handler_id": handlerID, "args": json.RawMessage(args)}
}

// fixRun is a tool call in flight.
type fixRun struct {
	f    *fixture
	name string
	req  *fxReq
	h    *fixHost
	prm  rec
	left func()
}

// StartRun starts a tool call (after tool_execution_start) and returns before it completes.
func (f *fixture) StartRun(name string, params rec) *fixRun {
	f.g.t.Helper()
	host, leave := f.enter()
	f.checkTool(name, params)
	f.fireFirst(host, "tool_execution_start", rec{"toolCallId": "id", "toolName": name, "args": params})
	return &fixRun{f: f, name: name, req: fxStartRequest(host.h, fxToolRequest(name, params, "id")), h: host, prm: params, left: leave}
}

// Cancel aborts the call (the host's cancel frame: ctx.Done() fires, ctx.CancellationReason() is reason).
func (r *fixRun) Cancel(reason string) { r.req.cancel(reason) }

// Wait returns the call's outcome and fires tool_execution_end like Run. A failure with an empty message is reported
// as fxEmptyError (see WaitFailed for the exact text).
func (r *fixRun) Wait() (fixResult, string) {
	r.f.g.t.Helper()
	res, message, failed := r.WaitFailed()
	if failed && message == "" {
		message = fxEmptyError
	}
	return res, message
}

// WaitFailed is Wait with the exact error message ("" is possible) and an explicit failed flag.
func (r *fixRun) WaitFailed() (fixResult, string, bool) {
	r.f.g.t.Helper()
	defer r.left()
	return r.f.finishRun(r.h, r.name, r.prm, r.req)
}

func (f *fixture) checkTool(name string, params rec) {
	f.g.t.Helper()
	for _, tl := range f.g.tools {
		if tl.Name == name {
			if err := fxSchemaCheck(tl.schema, fxNormalize(map[string]any(params))); err != nil {
				f.g.t.Fatalf("Unit tool arguments must satisfy the registered schema: %v (tool %s, params %v)", err, name, params)
			}
			return
		}
	}
	f.g.t.Fatalf("fixture: tool %q is not registered (have %v)", name, f.ToolNames())
}

func (f *fixture) finishRun(host *fixHost, name string, params rec, req *fxReq) (fixResult, string, bool) {
	f.g.t.Helper()
	raw, message, failed := req.waitFailed()
	if failed {
		return fixResult{}, message, true
	}
	res, err := fxParseResult(raw)
	if err != nil {
		f.g.t.Fatalf("fixture: %v", err)
	}
	if err := fxCheckResult(res); err != nil {
		f.g.t.Fatalf("Expected a valid Posthorse tool result: %v\n%s", err, raw)
	}
	result := rec{"content": fxRecs(res.Content)}
	if res.Details != nil {
		result["details"] = res.Details
	}
	f.fireFirst(host, "tool_execution_end", rec{"toolCallId": "id", "toolName": name, "result": result, "isError": false})
	return res, "", false
}

// RunErr executes a tool like setup().tools.get(name).execute: schema check, tool_execution_start, the tool,
// tool_execution_end (not fired when the tool throws). It returns the error text of a failing tool (fxEmptyError when
// the tool threw an error with an empty message, so `text != ""` always means failure; RunFailed has the exact text).
func (f *fixture) RunErr(name string, params rec) (fixResult, string) {
	f.g.t.Helper()
	return f.StartRun(name, params).Wait()
}

// RunFailed is RunErr with the exact error message (possibly "": `throw new Error("")`) and an explicit failed flag.
func (f *fixture) RunFailed(name string, params rec) (res fixResult, message string, failed bool) {
	f.g.t.Helper()
	return f.StartRun(name, params).WaitFailed()
}

// Run is RunErr that fails the test on a tool error.
func (f *fixture) Run(name string, params rec) fixResult {
	f.g.t.Helper()
	res, failure := f.RunErr(name, params)
	if failure != "" {
		f.g.t.Fatalf("tool %s failed: %s", name, failure)
	}
	return res
}

// NotesIn is notesIn(): the notes tool in another cwd with that cwd's own in-memory session (cached per cwd).
func (f *fixture) NotesIn(cwd string, params rec) fixResult {
	f.g.t.Helper()
	f.g.mu.Lock()
	sess := f.g.sessions[cwd]
	if sess == nil {
		sess = fxMemorySession(cwd)
		f.g.sessions[cwd] = sess
	}
	f.g.mu.Unlock()
	return f.With(withCwd(cwd), withSession(sess), withSessionDir("")).Run("notes", params)
}

// ---------------------------------------------------------------------------------------------------------------
// Events

func (f *fixture) fireFirst(host *fixHost, event string, data rec) json.RawMessage {
	f.g.t.Helper()
	h := host.h
	h.mu.Lock()
	ids := append([]int(nil), h.allHandlers[event]...)
	h.mu.Unlock()
	if len(ids) == 0 {
		return nil
	}
	return f.dispatch(host, event, ids[0], data)
}

func (f *fixture) dispatch(host *fixHost, event string, id int, data rec) json.RawMessage {
	f.g.t.Helper()
	h := host.h
	result, failure := fxStartRequest(h, fxEventRequest(event, id, data)).wait()
	if failure != "" {
		f.g.t.Errorf("%s handler failed: %s", event, failure)
	}
	if event == "tool_call" {
		return h.toolCallResult(result)
	}
	return result
}

// fxUnwrapEvent turns the reply the SDK sends for an event into the handler's own result: turn_end and
// agent_before_settle replies carry {_pigBoundaryEntries, _pigBoundaryResult}, before_agent_start replies
// {_pigPrompt*, _pigPromptResult}, `context*` replies {messages, _pigContextUnchanged} (left to ContextEvent).
func fxUnwrapEvent(event string, wire rec) rec {
	switch event {
	case "turn_end", "agent_before_settle":
		r, _ := wire["_pigBoundaryResult"].(map[string]any)
		if len(r) == 0 {
			return nil
		}
		return r
	case "before_agent_start":
		r, _ := wire["_pigPromptResult"].(map[string]any)
		if len(r) == 0 {
			return nil
		}
		return r
	}
	return wire
}

// fxDecodeEvent turns a handler reply into a rec; nil for `undefined` (no reply, null, or an empty object).
func fxDecodeEvent(raw json.RawMessage) rec {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var out rec
	if err := json.Unmarshal(raw, &out); err != nil {
		panic(fmt.Sprintf("fixture: event result %s: %v", raw, err))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Fire delivers one event to the first registered handler (TS `handlers.get(name)(event, ctx)`) and returns its result
// (nil when the handler returned undefined, or none is registered; the SDK's reply wrappers of turn_end,
// agent_before_settle and before_agent_start are removed, see FireWire for the raw reply). A handler error fails the test.
func (f *fixture) Fire(event string, data rec) rec {
	f.g.t.Helper()
	host, leave := f.enter()
	defer leave()
	wire := fxDecodeEvent(f.fireFirst(host, event, data))
	if wire == nil {
		return nil
	}
	return fxUnwrapEvent(event, wire)
}

// FireWire is Fire without the unwrapping: the reply exactly as the SDK sent it (nil when none).
func (f *fixture) FireWire(event string, data rec) rec {
	f.g.t.Helper()
	host, leave := f.enter()
	defer leave()
	return fxDecodeEvent(f.fireFirst(host, event, data))
}

// fixPrompt is the outcome of a before_agent_start handler: its result plus what it left in the event's shared options.
type fixPrompt struct {
	Result   rec // the handler's own result ({systemPrompt, message}) or nil
	Sections rec // event.systemPromptOptions.sections after the handler ran
	Options  rec // event.systemPromptOptions after the handler ran
}

// BeforeAgentStart fires before_agent_start with {systemPrompt, systemPromptOptions: options} (options may carry a
// `sections` object, customPrompt, forceSystemPrompt ...) and returns the handler's result and the options it left.
func (f *fixture) BeforeAgentStart(systemPrompt string, options rec) fixPrompt {
	f.g.t.Helper()
	wire := f.FireWire("before_agent_start", rec{"systemPrompt": systemPrompt, "systemPromptOptions": options})
	out := fixPrompt{}
	if wire == nil {
		return out
	}
	out.Result, _ = wire["_pigPromptResult"].(map[string]any)
	out.Sections, _ = wire["_pigPromptSections"].(map[string]any)
	out.Options, _ = wire["_pigPromptOptions"].(map[string]any)
	return out
}

// FireAll delivers the event to every handler in registration order and returns their results.
func (f *fixture) FireAll(event string, data rec) []rec {
	f.g.t.Helper()
	host, leave := f.enter()
	defer leave()
	host.h.mu.Lock()
	ids := append([]int(nil), host.h.allHandlers[event]...)
	host.h.mu.Unlock()
	var out []rec
	for _, id := range ids {
		wire := fxDecodeEvent(f.dispatch(host, event, id, data))
		if wire != nil {
			wire = fxUnwrapEvent(event, wire)
		}
		out = append(out, wire)
	}
	return out
}

// TurnEnd is turnEnd(handlers, context, toolResults): an assistant turn end with those tool results.
func (f *fixture) TurnEnd(toolResults ...rec) rec {
	f.g.t.Helper()
	stop := "stop"
	if len(toolResults) > 0 {
		stop = "toolUse"
	}
	return f.TurnEndEvent(rec{
		"message":     rec{"role": "assistant", "stopReason": stop, "content": []any{}},
		"toolResults": fxRecs(toolResults),
		"continue":    true,
		"entries":     []any{},
		"context":     rec{"pendingMessages": []any{}},
	})
}

// TurnEndEvent fires a hand-built turn_end event.
func (f *fixture) TurnEndEvent(event rec) rec {
	f.g.t.Helper()
	return f.Fire("turn_end", event)
}

// ContextEvent fires the `context` event; the result is nil when the handler left the messages exactly as sent
// (`undefined`), else `{messages: [...]}`: replaced, or edited in place (the SDK's `_pigContextUnchanged` marker is removed).
func (f *fixture) ContextEvent(messages ...rec) rec {
	f.g.t.Helper()
	return f.contextLike("context", messages)
}

func (f *fixture) contextLike(event string, messages []rec) rec {
	f.g.t.Helper()
	sent := fxNormalize(fxRecs(messages))
	res := f.Fire(event, rec{"messages": fxRecs(messages)})
	if res == nil {
		return nil
	}
	got, has := res["messages"]
	if !has || (res["_pigContextUnchanged"] == true && fxEqual(got, sent)) {
		return nil
	}
	delete(res, "_pigContextUnchanged")
	return res
}

// GuidanceFor is guidanceFor(): the `posthorse` section a context_with_system handler writes into a system message.
func (f *fixture) GuidanceFor() string {
	f.g.t.Helper()
	res := f.contextLike("context_with_system", []rec{{"role": "system", "content": "", "sections": rec{}, "timestamp": float64(0)}})
	if res == nil {
		f.g.t.Fatalf("context_with_system left the system message unchanged")
	}
	msgs, _ := res["messages"].([]any)
	if len(msgs) == 0 {
		f.g.t.Fatalf("context_with_system returned no messages: %v", res)
	}
	m, _ := msgs[0].(map[string]any)
	sections, _ := m["sections"].(map[string]any)
	s, ok := sections["posthorse"].(string)
	if !ok {
		f.g.t.Fatalf("context_with_system wrote no posthorse section: %v", m)
	}
	return s
}

// PageContext is pageContext(): a view whose usage is reduced by the tokens of the delivered guidance, so an exact page
// boundary includes it. The guidance is measured now (like the original, once), the usage when each call starts.
func (f *fixture) PageContext() *fixture {
	f.g.t.Helper()
	g := f.GuidanceFor()
	return f.With(withTokensDelta(-int(math.Ceil(float64(len(utf16.Encode([]rune(g)))) / 4))))
}

// AutomaticResult is automaticResult(): session_before_compact (reason "threshold") with the leaf "boundary", a
// getBranch that throws "Use compaction event history" and a projection of projected (default: the message entries of
// branchEntries, each projecting its own message). nil when the handler returned undefined.
func (f *fixture) AutomaticResult(branchEntries []rec, projected ...rec) rec {
	f.g.t.Helper()
	proj := projected
	if projected == nil {
		for _, e := range branchEntries {
			if e["type"] == "message" {
				proj = append(proj, e)
			}
		}
	}
	st := f.resolve()
	settings := rec{"enabled": st.enabled, "reserveTokens": st.reserve, "keepRecentTokens": float64(0)}
	view := f.With(
		withHostAnswer("sessionRead.getLeafId", func(map[string]any) (any, string) { return "boundary", "" }),
		withHostError("sessionRead.getBranch", "Use compaction event history"),
		withHostError("watchSessionLog", "Use compaction event history"),
		withProjection(proj...),
	)
	return view.Fire("session_before_compact", rec{
		"reason":        "threshold",
		"branchEntries": fxRecs(branchEntries),
		"preparation":   rec{"settings": settings, "tokensBefore": float64(100)},
	})
}

// AutomaticHandoff is automaticHandoff(): the summary of the compaction the automatic boundary returns.
func (f *fixture) AutomaticHandoff(branchEntries []rec) string {
	f.g.t.Helper()
	res := f.AutomaticResult(branchEntries)
	if res == nil {
		f.g.t.Fatalf("session_before_compact returned undefined for an automatic threshold")
	}
	compaction, _ := res["compaction"].(map[string]any)
	summary, ok := compaction["summary"].(string)
	if !ok {
		f.g.t.Fatalf("session_before_compact result has no compaction.summary: %v", res)
	}
	return summary
}

// ---------------------------------------------------------------------------------------------------------------
// Direct policy context (budget / contract unit tests: the `{getCompactionSettings, getContextUsage, getSystemPrompt}`
// literal of rollover-cost.test.ts and archive.test.ts)

// fakePolicy implements policyContext over fixed values.
type fakePolicy struct {
	Settings compactionPolicy
	Usage    *contextUsage
	Prompt   string
	Err      error
}

func (p fakePolicy) getCompactionSettings() (compactionPolicy, error) { return p.Settings, p.Err }
func (p fakePolicy) getContextUsage() (*contextUsage, error)          { return p.Usage, p.Err }
func (p fakePolicy) getSystemPrompt() (string, error)                 { return p.Prompt, p.Err }

// ---------------------------------------------------------------------------------------------------------------
// JSON schema check (TypeBox `Check`, the subset the registered tools use)

// fxSchemaCheck validates v (decoded JSON) against a JSON schema (decoded JSON).
func fxSchemaCheck(schema any, v any) error { return fxSchemaAt(schema, v, "$") }

var fxPatterns sync.Map

func fxSchemaAt(schema any, v any, path string) error {
	s, ok := schema.(map[string]any)
	if !ok {
		return nil // true / unknown schema
	}
	if alts, ok := s["anyOf"].([]any); ok {
		for _, a := range alts {
			if fxSchemaAt(a, v, path) == nil {
				return nil
			}
		}
		return fmt.Errorf("%s: no anyOf alternative matches %v", path, v)
	}
	if alts, ok := s["oneOf"].([]any); ok {
		n := 0
		for _, a := range alts {
			if fxSchemaAt(a, v, path) == nil {
				n++
			}
		}
		if n != 1 {
			return fmt.Errorf("%s: %d oneOf alternatives match %v", path, n, v)
		}
	}
	if all, ok := s["allOf"].([]any); ok {
		for _, a := range all {
			if err := fxSchemaAt(a, v, path); err != nil {
				return err
			}
		}
	}
	if c, ok := s["const"]; ok && !fxEqual(c, v) {
		return fmt.Errorf("%s: %v is not the constant %v", path, v, c)
	}
	if e, ok := s["enum"].([]any); ok {
		found := false
		for _, x := range e {
			if fxEqual(x, v) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("%s: %v is not one of %v", path, v, e)
		}
	}
	if t, ok := s["type"]; ok {
		var types []string
		switch tt := t.(type) {
		case string:
			types = append(types, tt)
		case []any:
			for _, x := range tt {
				if str, ok := x.(string); ok {
					types = append(types, str)
				}
			}
		}
		matched := false
		for _, name := range types {
			if fxTypeIs(name, v) {
				matched = true
			}
		}
		if len(types) > 0 && !matched {
			return fmt.Errorf("%s: %v is not of type %v", path, v, types)
		}
	}
	switch x := v.(type) {
	case map[string]any:
		props, _ := s["properties"].(map[string]any)
		if req, ok := s["required"].([]any); ok {
			for _, r := range req {
				if name, _ := r.(string); name != "" {
					if _, present := x[name]; !present {
						return fmt.Errorf("%s: missing required property %q", path, name)
					}
				}
			}
		}
		for name, val := range x {
			if p, ok := props[name]; ok {
				if err := fxSchemaAt(p, val, path+"."+name); err != nil {
					return err
				}
			} else if ap, ok := s["additionalProperties"]; ok {
				if b, isBool := ap.(bool); isBool && !b {
					return fmt.Errorf("%s: unexpected property %q", path, name)
				}
				if m, isMap := ap.(map[string]any); isMap {
					if err := fxSchemaAt(m, val, path+"."+name); err != nil {
						return err
					}
				}
			}
		}
	case []any:
		if n, ok := s["minItems"].(float64); ok && float64(len(x)) < n {
			return fmt.Errorf("%s: fewer than %v items", path, n)
		}
		if n, ok := s["maxItems"].(float64); ok && float64(len(x)) > n {
			return fmt.Errorf("%s: more than %v items", path, n)
		}
		if items, ok := s["items"]; ok {
			for i, el := range x {
				if err := fxSchemaAt(items, el, path+"["+strconv.Itoa(i)+"]"); err != nil {
					return err
				}
			}
		}
	case string:
		n := len(utf16.Encode([]rune(x)))
		if m, ok := s["minLength"].(float64); ok && float64(n) < m {
			return fmt.Errorf("%s: shorter than %v", path, m)
		}
		if m, ok := s["maxLength"].(float64); ok && float64(n) > m {
			return fmt.Errorf("%s: longer than %v", path, m)
		}
		if p, ok := s["pattern"].(string); ok {
			re, _ := fxPatterns.Load(p)
			if re == nil {
				compiled, err := regexp.Compile(p)
				if err != nil {
					return nil
				}
				fxPatterns.Store(p, compiled)
				re = compiled
			}
			if !re.(*regexp.Regexp).MatchString(x) {
				return fmt.Errorf("%s: %q does not match %s", path, x, p)
			}
		}
	case float64:
		if m, ok := s["minimum"].(float64); ok && x < m {
			return fmt.Errorf("%s: %v < minimum %v", path, x, m)
		}
		if m, ok := s["maximum"].(float64); ok && x > m {
			return fmt.Errorf("%s: %v > maximum %v", path, x, m)
		}
		if m, ok := s["exclusiveMinimum"].(float64); ok && x <= m {
			return fmt.Errorf("%s: %v <= exclusiveMinimum %v", path, x, m)
		}
		if m, ok := s["exclusiveMaximum"].(float64); ok && x >= m {
			return fmt.Errorf("%s: %v >= exclusiveMaximum %v", path, x, m)
		}
	}
	return nil
}

func fxTypeIs(name string, v any) bool {
	switch name {
	case "string":
		_, ok := v.(string)
		return ok
	case "number":
		_, ok := v.(float64)
		return ok
	case "integer":
		f, ok := v.(float64)
		return ok && f == math.Trunc(f)
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "null":
		return v == nil
	}
	return true
}

func fxEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}
