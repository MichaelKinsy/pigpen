// Package herdr reports PiG's agent state to herdr, the terminal workspace manager.
//
// It is a port of herdr-pig's extensions/herdr-agent-state.ts (Hai-Fai Solutions, Apache-2.0; revision
// 3ec680471e8282e0a8a64545781069f822c0c212, vendored unmodified in port/oracle), itself based on herdr's built-in Pi
// integration. Inside a herdr pane (HERDR_ENV=1 with HERDR_SOCKET_PATH and HERDR_PANE_ID) it reports `idle`,
// `working` and `blocked` and the command that resumes the session, over herdr's socket as newline-delimited JSON
// (`pane.report_agent`, `pane.release_agent`), and releases the pane when the user quits. Outside herdr it
// registers nothing.
//
// It is written against PiG's public Go extension SDK only, so it builds as a source extension and fuses into a
// Piglet Binary. The comments cite the original's line numbers ("ts:NN").
package herdr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const (
	// source and agent are the original's identity (ts:17-18). herdr reserves the `herdr:` source prefix for its own
	// integrations, so this reports under a plain source and herdr treats PiG as a self-reporting agent.
	source = "pig"
	agent  = "pig"
	// maxPromptMessage bounds the sidebar message of a prompt-based block, in characters.
	maxPromptMessage = 120
	// promptsEnv opts into the PiG-only prompt-based blocked state.
	promptsEnv = "PIGPEN_HERDR_UI_PROMPTS"
)

// The attempt bounds of one request (ts:57-62): 0.5 s, then one retry of 1.5 s.
var (
	firstAttempt = 500 * time.Millisecond
	retryAttempt = 1500 * time.Millisecond
)

type agentState string

const (
	stateIdle    agentState = "idle"
	stateWorking agentState = "working"
	stateBlocked agentState = "blocked"
)

// jsValue is a JavaScript value of a herdr:blocked label: undefined (the zero value), or a decoded JSON value.
// ref identifies an object or an array, which JavaScript compares by reference.
type jsValue struct {
	defined bool
	value   any
	ref     uint64
}

var refCounter struct {
	mu sync.Mutex
	n  uint64
}

// newJSValue wraps a decoded JSON value; a map or a slice gets a fresh identity.
func newJSValue(v any) jsValue {
	out := jsValue{defined: true, value: v}
	switch v.(type) {
	case map[string]any, []any:
		refCounter.mu.Lock()
		refCounter.n++
		out.ref = refCounter.n
		refCounter.mu.Unlock()
	}
	return out
}

// strictEquals is `===` on two values (NaN cannot arrive from JSON).
func (a jsValue) strictEquals(b jsValue) bool {
	if !a.defined || !b.defined {
		return a.defined == b.defined
	}
	if a.ref != 0 || b.ref != 0 {
		return a.ref == b.ref
	}
	return a.value == b.value
}

// truthy is JavaScript's ToBoolean for a decoded JSON value.
func truthy(v any) bool {
	switch v := v.(type) {
	case nil:
		return false
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		return v != ""
	default:
		return true
	}
}

var (
	seqMu   sync.Mutex
	lastSeq int64
)

// nextSeq increases across every reporter in this process and, because it is seeded from the clock (microseconds, as
// ts:74), across restarts and session replacements. herdr drops reports whose seq is not higher than the last one it
// accepted.
func nextSeq() int64 {
	seqMu.Lock()
	defer seqMu.Unlock()
	lastSeq = max(lastSeq+1, time.Now().UnixMicro())
	return lastSeq
}

// requestID is `pig:<kind>:<ms>:<random base 36>` (ts:82-84).
func requestID(kind string) string {
	return source + ":" + kind + ":" + strconv.FormatInt(time.Now().UnixMilli(), 10) + ":" + strconv.FormatUint(rand.Uint64N(1<<53), 36)
}

type herdrEnv struct {
	endpoint string
	paneID   string
}

// readEnv reports whether this process runs inside a herdr pane (ts:20-27): HERDR_ENV=1, a socket path and a pane id.
func readEnv(getenv func(string) string) (herdrEnv, bool) {
	socket, pane := getenv("HERDR_SOCKET_PATH"), getenv("HERDR_PANE_ID")
	if getenv("HERDR_ENV") != "1" || socket == "" || pane == "" {
		return herdrEnv{}, false
	}
	return herdrEnv{endpoint: endpointFor(runtime.GOOS, socket), paneID: pane}, true
}

// endpointFor is the socket endpoint (ts:21-22): on Windows herdr's socket is a named pipe and HERDR_SOCKET_PATH holds its name.
func endpointFor(goos, socketPath string) string {
	if goos == "windows" && socketPath != "" {
		return `\\.\pipe\` + socketPath
	}
	return socketPath
}

// isSafeResumeArg: herdr rejects a resume command whose arguments contain an apostrophe or a control character, and
// rejecting it drops the whole state report (ts:86-91). Control is exactly U+0000..U+001F and U+007F.
func isSafeResumeArg(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r == '\'' || r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// isAbsolutePath is path.posix.isAbsolute or path.win32.isAbsolute, so a session file from any host is reported.
func isAbsolutePath(p string) bool {
	if p == "" {
		return false
	}
	if p[0] == '/' || p[0] == '\\' {
		return true
	}
	isLetter := p[0] >= 'a' && p[0] <= 'z' || p[0] >= 'A' && p[0] <= 'Z'
	return len(p) >= 3 && isLetter && p[1] == ':' && (p[2] == '/' || p[2] == '\\')
}

// resumeArgvFor is the command that reopens the session (ts:93-118): `pig --session <file>` for an absolute, safe
// session file, else `pig --session <id>` for a safe id, else none. A nil file or id is one the session manager did not
// have (or could not give).
func resumeArgvFor(file, id *string) []string {
	if file != nil && isAbsolutePath(*file) && isSafeResumeArg(*file) {
		return []string{"pig", "--session", *file}
	}
	if id != nil && isSafeResumeArg(*id) {
		return []string{"pig", "--session", *id}
	}
	return nil
}

// sessionIdentity reads the session file and id; a host failure is an absent value, as a throwing getter is (ts:93-105).
func sessionIdentity(ctx sdk.Context) (file, id *string) {
	if f, err := ctx.GetSessionFile(); err == nil {
		file = f
	}
	if v, err := ctx.GetSessionID(); err == nil {
		id = &v
	}
	return file, id
}

// statePayload is the params of a report, in the original's key order (ts:121-133). A message that is undefined is
// left out, as JSON.stringify does.
type statePayload struct {
	PaneID     string          `json:"pane_id"`
	Source     string          `json:"source"`
	Agent      string          `json:"agent"`
	State      agentState      `json:"state"`
	Message    json.RawMessage `json:"message,omitempty"`
	Seq        int64           `json:"seq"`
	ResumeArgv []string        `json:"resume_argv,omitempty"`
}

type releasePayload struct {
	PaneID string `json:"pane_id"`
	Source string `json:"source"`
	Agent  string `json:"agent"`
	Seq    int64  `json:"seq"`
}

type request struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params"`
}

// encodeLine is JSON.stringify(request) + "\n" (ts:49).
func encodeLine(req request) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(req); err != nil {
		return nil, err
	}
	return rawLineSeparators(buf.Bytes()), nil
}

// rawLineSeparators writes U+2028 and U+2029 as themselves: Go's encoder escapes them, JSON.stringify does not, and the
// request line is meant to be the same bytes. A backslash escapes the next character, so `\\u2028` is left alone.
func rawLineSeparators(b []byte) []byte {
	if !bytes.Contains(b, []byte(`\u202`)) {
		return b
	}
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		switch {
		case b[i] == '\\' && i+1 < len(b) && b[i+1] == '\\':
			out = append(out, '\\', '\\')
			i++
		case b[i] == '\\' && i+5 < len(b) && string(b[i+1:i+5]) == "u202" && (b[i+5] == '8' || b[i+5] == '9'):
			if b[i+5] == '8' {
				out = append(out, "\u2028"...)
			} else {
				out = append(out, "\u2029"...)
			}
			i += 5
		default:
			out = append(out, b[i])
		}
	}
	return out
}

// Extension is the conventional Go factory. Outside a herdr pane it registers nothing.
func Extension() *sdk.Extension { return extensionFor(os.Getenv) }

// extensionFor is Extension over an environment lookup, so a test can run several in one process.
func extensionFor(getenv func(string) string) *sdk.Extension {
	e := sdk.New("herdr")
	env, ok := readEnv(getenv)
	if !ok {
		return e
	}
	r := &reporter{env: env}

	// PiG-only: PiG tells every extension when a blocking UI prompt (select, confirm, input, editor, custom) opens and
	// settles. Pi has no such event and herdr-pig never reports a prompt, so this is opt-in: by default the port reports
	// exactly what the original reports in the same session.
	if getenv(promptsEnv) == "1" {
		e.OnEvent(sdk.EventUIPromptStart, func(_ sdk.Context, data map[string]any) (any, error) {
			title, _ := data["title"].(string)
			if title == "" {
				title, _ = data["kind"].(string)
			}
			r.block(holderPrompt, newJSValue(promptMessage(title)))
			return nil, nil
		})
		e.OnEvent(sdk.EventUIPromptEnd, func(sdk.Context, map[string]any) (any, error) {
			r.unblock(holderPrompt)
			return nil, nil
		})
	}

	// Another extension marks the agent as waiting on the user by emitting `herdr:blocked` with `{ active, label }`
	// (ts:217-235). A failed subscription leaves the rest working.
	_, _ = e.Events().On("herdr:blocked", func(_ sdk.Context, data any) error {
		payload, _ := data.(map[string]any) // `data?.active` of anything else is undefined
		if !truthy(payload["active"]) {
			r.unblock(holderBus)
			return nil
		}
		var label jsValue // `data.label`: undefined when missing
		if v, present := payload["label"]; present {
			label = newJSValue(v)
		}
		r.block(holderBus, label)
		return nil
	})

	e.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		// TUI only: print, JSON and RPC modes have no pane for herdr to show (ts:238-241).
		if ctx.Mode() != "tui" {
			return nil, nil
		}
		file, id := sessionIdentity(ctx)
		// A reload can replace this extension mid-run without another agent_start (ts:245).
		idle, err := ctx.IsIdle()
		return nil, r.start(resumeArgvFor(file, id), err == nil && !idle, err)
	})

	e.OnEvent(sdk.EventAgentStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		if !r.isRoot() {
			return nil, nil
		}
		file, id := sessionIdentity(ctx)
		r.agentStarted(resumeArgvFor(file, id))
		return nil, nil
	})

	// agent_settled fires once the run is fully over: no retry, compaction or queued continuation follows (ts:258-264).
	e.OnEvent(sdk.EventAgentSettled, func(ctx sdk.Context, _ map[string]any) (any, error) {
		if !r.isRoot() {
			return nil, nil
		}
		// `ctx?.isIdle?.() !== true`: only a successful true is idle; a failed call surfaces as the handler's error.
		idle, err := ctx.IsIdle()
		if err != nil {
			return nil, err
		}
		if idle {
			r.agentSettled()
		}
		return nil, nil
	})

	e.OnSessionShutdown(func(_ sdk.Context, data map[string]any) (any, error) {
		if !r.isRoot() {
			return nil, nil
		}
		// Switching or reloading a session keeps PiG in the pane, so only a real quit releases it (ts:267-269).
		if reason, _ := data["reason"].(string); reason != "quit" {
			return nil, nil
		}
		r.release()
		return nil, nil
	})
	return e
}

// promptMessage makes one short line for herdr's sidebar; prompt titles can be long and multi-line.
func promptMessage(value string) string {
	line := strings.Join(strings.FieldsFunc(value, func(r rune) bool { return unicode.IsSpace(r) || r == '\uFEFF' }), " ")
	runes := []rune(line)
	if len(runes) > maxPromptMessage {
		return string(runes[:maxPromptMessage-1]) + "…"
	}
	return line
}

// holder is a source of a blocked state.
type holder int

const (
	holderBus    holder = iota // a herdr:blocked emitter on pi.events
	holderPrompt               // a PiG UI prompt (opt-in)
	holderCount
)

type report struct {
	state   agentState
	message jsValue
	seq     int64
}

// reporter is one herdr reporting session. Handlers may run concurrently, so every field is guarded by mu; herdr calls
// run outside it.
type reporter struct {
	env herdrEnv

	mu          sync.Mutex
	rootSession bool
	resume      []string
	agentActive bool
	// Blocks currently held, per holder: a bus release cannot end a prompt's block, nor the reverse.
	blockedCount   [holderCount]int
	blockedMessage jsValue

	hasLast     bool
	lastState   agentState
	lastMessage jsValue
	lastResume  string
	hasResume   bool

	// lastDiagnostic is the last failure written to stderr, so a repeat is not.
	lastDiagnostic string

	// Only the newest state matters: while a request is in flight, newer states replace the queued one, and requests
	// never overlap, so herdr sees them in order (ts:145-174).
	queued  *report
	sending bool
}

func (r *reporter) isRoot() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rootSession
}

// start is the session_start handler's state change (ts:242-247). It keeps everything else as it is, the blocked counters
// included: the original does not reset them (a second session_start on one instance does not occur under Pi, which loads
// a fresh copy of the module for every session). An isIdle failure fails the handler after the session and its resume
// command are recorded, as a throwing isIdle() does in the original.
func (r *reporter) start(resume []string, active bool, idleErr error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rootSession = true
	r.resume = resume
	if idleErr != nil {
		return idleErr
	}
	r.agentActive = active
	r.publishLocked(true)
	return nil
}

func (r *reporter) agentStarted(resume []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resume = resume
	r.agentActive = true
	r.publishLocked(false)
}

func (r *reporter) agentSettled() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agentActive = false
	r.publishLocked(false)
}

func (r *reporter) block(h holder, label jsValue) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.rootSession {
		return
	}
	r.blockedCount[h]++
	r.blockedMessage = label
	r.publishLocked(false)
}

func (r *reporter) unblock(h holder) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.rootSession {
		return
	}
	r.blockedCount[h] = max(0, r.blockedCount[h]-1)
	if !r.blockedLocked() {
		r.blockedMessage = jsValue{}
	}
	r.publishLocked(false)
}

func (r *reporter) blockedLocked() bool {
	for _, n := range r.blockedCount {
		if n > 0 {
			return true
		}
	}
	return false
}

func (r *reporter) desiredLocked() (agentState, jsValue) {
	if r.blockedLocked() {
		return stateBlocked, r.blockedMessage
	}
	if r.agentActive {
		return stateWorking, jsValue{}
	}
	return stateIdle, jsValue{}
}

// publishLocked is publishState (ts:200-215).
func (r *reporter) publishLocked(force bool) {
	state, message := r.desiredLocked()
	resume := strings.Join(r.resume, "\x00")
	hasResume := r.resume != nil
	if !force && r.hasLast && state == r.lastState && message.strictEquals(r.lastMessage) && hasResume == r.hasResume && resume == r.lastResume {
		return
	}
	r.hasLast, r.lastState, r.lastMessage, r.lastResume, r.hasResume = true, state, message, resume, hasResume
	r.queueLocked(state, message)
}

// queueLocked is queueState (ts:149-154). When nothing is in flight the drain starts at once on this state, as the
// original's drainStateQueue takes the queued state synchronously, so a following event cannot replace it.
func (r *reporter) queueLocked(state agentState, message jsValue) {
	r.queued = &report{state: state, message: message, seq: nextSeq()}
	if !r.sending {
		r.sending = true
		first := r.takeLocked()
		go r.drain(first)
	}
}

// takeLocked removes the queued state and builds its request. The resume command goes out as it is when the request is
// built, not when the state was queued (ts:129-133).
func (r *reporter) takeLocked() *outgoing {
	next := r.queued
	r.queued = nil
	if next == nil {
		return nil
	}
	return &outgoing{message: next.message, payload: statePayload{
		PaneID: r.env.paneID, Source: source, Agent: agent, State: next.state, Seq: next.seq,
		ResumeArgv: append([]string(nil), r.resume...),
	}}
}

// outgoing is a state report ready to send.
type outgoing struct {
	message jsValue
	payload statePayload
}

// drain sends queued reports one at a time until none is left (ts:156-174).
func (r *reporter) drain(next *outgoing) {
	for ; next != nil; next = r.nextOutgoing() {
		if next.message.defined {
			raw, err := marshalValue(next.message.value)
			if err != nil {
				r.diagnose("pane.report_agent", err)
				continue
			}
			next.payload.Message = raw
		}
		r.send("state", "pane.report_agent", next.payload)
	}
}

// nextOutgoing takes the next queued state, or ends the drain.
func (r *reporter) nextOutgoing() *outgoing {
	r.mu.Lock()
	defer r.mu.Unlock()
	next := r.takeLocked()
	if next == nil {
		r.sending = false
	}
	return next
}

// marshalValue is JSON.stringify of a decoded value, without HTML escaping.
func marshalValue(v any) (json.RawMessage, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// release stops new reports, drops the queued one and sends pane.release_agent (ts:271-274). It does not wait for a
// report in flight: the original's drain loop runs on, and herdr drops the older seq whichever arrives first.
func (r *reporter) release() {
	r.mu.Lock()
	r.rootSession = false
	r.queued = nil
	seq := nextSeq()
	r.mu.Unlock()
	r.send("release", "pane.release_agent", releasePayload{PaneID: r.env.paneID, Source: source, Agent: agent, Seq: seq})
}

// send is sendRequest (ts:57-62): one attempt of 0.5 s and, when it does not deliver, one of 1.5 s with the same request.
func (r *reporter) send(kind, method string, params any) {
	line, err := encodeLine(request{ID: requestID(kind), Method: method, Params: params})
	if err != nil {
		r.diagnose(method, err)
		return
	}
	err = attempt(r.env.endpoint, line, firstAttempt)
	if err != nil {
		err = attempt(r.env.endpoint, line, retryAttempt)
	}
	if err != nil {
		r.diagnose(method, err)
		return
	}
	r.healthy()
}

// diagnose writes one line to stderr, which PiG collects as the extension's output, when herdr did not take a
// request: a herdr that never hears about a pane is otherwise indistinguishable from one that was told. The same
// failure is written once until a request is delivered again. The agent never waits on it and the transcript never
// shows it. (herdr-pig fails silently; stderr is not an effect herdr observes. The line came from the herdr-agent-state
// extension of pigpen pull request 2, Yu Li.)
func (r *reporter) diagnose(method string, err error) {
	msg := fmt.Sprintf("herdr: %s failed: %v", method, err)
	r.mu.Lock()
	repeat := msg == r.lastDiagnostic
	r.lastDiagnostic = msg
	r.mu.Unlock()
	if !repeat {
		fmt.Fprintln(os.Stderr, msg)
	}
}

// healthy forgets the last failure, so the next one is written again.
func (r *reporter) healthy() {
	r.mu.Lock()
	r.lastDiagnostic = ""
	r.mu.Unlock()
}
