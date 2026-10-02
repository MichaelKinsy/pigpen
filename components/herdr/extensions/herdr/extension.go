// Package herdr reports PiG's agent state to herdr, the terminal workspace manager.
//
// It follows herdr's published "Add Herdr support to your agent" contract
// (https://herdr.dev/docs/add-herdr-support/): inside a herdr pane, report idle,
// working or blocked through `"$HERDR_BIN_PATH" pane report-agent`, keep the
// report order with an increasing --seq, report the command that resumes the
// session (herdr 0.9.2+ restores the pane with it after a server restart), and
// release the pane when the user quits. Outside herdr the factory registers
// nothing.
//
// It is written against PiG's public Go extension SDK only, so it builds as a
// source extension and fuses into a Piglet Binary.
package herdr

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const (
	// source is the stable integration identity. herdr reserves the `herdr:` prefix for its own sources.
	source = "custom:pig"
	agent  = "pig"
	// callTimeout bounds every herdr CLI call: herdr must never slow the agent down.
	callTimeout = 3 * time.Second
	// maxMessageLength bounds the sidebar message, in characters.
	maxMessageLength = 120
	// command is the first word of the resume command: a plain command name on PATH, as herdr requires.
	command = "pig"
	// herdr refuses a resume command of more than 64 arguments or 8 KiB in total.
	maxResumeArgs  = 64
	maxResumeBytes = 8 * 1024
)

type agentState string

const (
	stateIdle    agentState = "idle"
	stateWorking agentState = "working"
	stateBlocked agentState = "blocked"
)

type report struct {
	state   agentState
	message string
	seq     int64
}

var (
	seqMu   sync.Mutex
	lastSeq int64
)

// nextSeq is monotonic across every reporter in this process and, because it is
// seeded from the clock (microseconds), across restarts and session
// replacements. herdr drops reports whose seq is not higher than the last one it
// accepted.
func nextSeq() int64 {
	seqMu.Lock()
	defer seqMu.Unlock()
	lastSeq = max(lastSeq+1, time.Now().UnixMicro())
	return lastSeq
}

type herdrEnv struct {
	bin    string
	paneID string
}

// readEnv reports whether this process runs inside a herdr pane. The contract
// is HERDR_ENV=1 and HERDR_PANE_ID, HERDR_BIN_PATH and HERDR_SOCKET_PATH all set;
// otherwise the integration does nothing. The reports go through the CLI, which
// finds the socket itself from the same environment.
func readEnv() (herdrEnv, bool) {
	bin, pane, socket := os.Getenv("HERDR_BIN_PATH"), os.Getenv("HERDR_PANE_ID"), os.Getenv("HERDR_SOCKET_PATH")
	if os.Getenv("HERDR_ENV") != "1" || bin == "" || pane == "" || socket == "" {
		return herdrEnv{}, false
	}
	return herdrEnv{bin: bin, paneID: pane}, true
}

// Extension is the conventional Go factory. Outside a herdr pane it registers nothing.
func Extension() *sdk.Extension {
	e := sdk.New("herdr")
	env, ok := readEnv()
	if !ok {
		return e
	}
	r := &reporter{env: env}

	// PiG tells every extension when a blocking UI prompt (select, confirm,
	// input, editor, custom) opens and settles.
	e.OnEvent(sdk.EventUIPromptStart, func(_ sdk.Context, data map[string]any) (any, error) {
		title, _ := data["title"].(string)
		if title == "" {
			title, _ = data["kind"].(string)
		}
		r.block(title)
		return nil, nil
	})
	e.OnEvent(sdk.EventUIPromptEnd, func(sdk.Context, map[string]any) (any, error) {
		r.unblock()
		return nil, nil
	})

	e.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		// herdr can only show a pane's terminal, so report the interactive session
		// and stay silent in RPC, JSON and print runs.
		if ctx.Mode() != "tui" {
			return nil, nil
		}
		path, id := sessionIdentity(ctx)
		// A reload can start this reporter in the middle of a turn.
		idle, err := ctx.IsIdle()
		r.start(path, id, err == nil && !idle)
		return nil, nil
	})

	e.OnEvent(sdk.EventAgentStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		if !r.isRoot() {
			return nil, nil
		}
		path, id := sessionIdentity(ctx)
		r.agentStarted(path, id)
		return nil, nil
	})

	// agent_settled fires once the run is fully over: no retry, compaction or
	// queued continuation follows, so it is the right moment to report idle.
	e.OnEvent(sdk.EventAgentSettled, func(ctx sdk.Context, _ map[string]any) (any, error) {
		if !r.isRoot() {
			return nil, nil
		}
		if idle, err := ctx.IsIdle(); err == nil && !idle {
			return nil, nil
		}
		r.agentSettled()
		return nil, nil
	})

	e.OnSessionShutdown(func(_ sdk.Context, data map[string]any) (any, error) {
		if !r.isRoot() {
			return nil, nil
		}
		if reason, _ := data["reason"].(string); reason != "quit" {
			// A successor reporter in this pane re-reports on its own. Releasing here
			// would race that report, and a late release would clear the pane.
			r.silence()
			return nil, nil
		}
		r.release()
		return nil, nil
	})
	return e
}

// sessionIdentity reads the session file and id. A host failure or a value herdr
// cannot use is omitted, not guessed.
func sessionIdentity(ctx sdk.Context) (path, id string) {
	if file, err := ctx.GetSessionFile(); err == nil && file != nil && isAbsolutePath(*file) {
		path = *file
	}
	if v, err := ctx.GetSessionID(); err == nil {
		id = v
	}
	return path, id
}

// isAbsolutePath accepts POSIX and Windows absolute paths, so a session file
// from any host is reported.
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

// resumeArgv is the command that reopens this session in the pane's directory,
// mirroring herdr's built-in pi integration (`pi --session <file>`, no model
// flag) with pig substituted. It is the same on PiG 0.3.x and 0.4.0: a
// `--session` argument with a path separator or a .jsonl suffix opens that exact
// file whatever the directory or session dir. When the file cannot be passed
// (herdr refuses apostrophes, control characters and more than 8 KiB) the
// session id goes to `--session-id`, which resumes the id in the pane's project
// session dir and never asks to fork the way `--session <id>` does from another
// directory. With neither, there is no resume command. An in-memory session
// (`pig --no-session`) has an id but no file, and nothing to resume:
// `--session-id` would create a new, persisted session with that id.
func resumeArgv(path, id string) []string {
	if path == "" {
		return nil
	}
	for _, argv := range [][]string{
		{command, "--session", path},
		{command, "--session-id", id},
	} {
		if argv[2] != "" && resumeArgvValid(argv) && (argv[1] == "--session" || validSessionID(id)) {
			return argv
		}
	}
	return nil
}

// resumeArgvValid applies herdr's own rules for a resume command.
func resumeArgvValid(argv []string) bool {
	total := 0
	for _, arg := range argv {
		total += len(arg)
		if strings.ContainsRune(arg, '\'') || strings.ContainsFunc(arg, unicode.IsControl) {
			return false
		}
	}
	return len(argv) <= maxResumeArgs && total <= maxResumeBytes
}

// validSessionID is PiG's rule for a `--session-id` value: letters, digits, `-`,
// `_` and `.`, starting and ending with a letter or digit.
func validSessionID(id string) bool {
	if id == "" {
		return false
	}
	alnum := func(c byte) bool { return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !alnum(c) && (i == 0 || i == len(id)-1 || c != '-' && c != '_' && c != '.') {
			return false
		}
	}
	return true
}

// messageArgs passes the message in the form every herdr parses: `--message
// <text>`. Every herdr CLI takes the next argument as the value, even one that
// starts with "-"; herdr before 0.9.0 does not know the attached
// `--message=<text>` form and would drop the whole report. Only a message that
// is exactly `--` goes attached (herdr 0.9.0+), because herdr 0.9.2+ splits the
// resume command off at the first `--` argument.
func messageArgs(message string) []string {
	if message == "--" {
		return []string{"--message=" + message}
	}
	return []string{"--message", message}
}

// messageFrom makes one short line for herdr's sidebar; prompt titles can be long and multi-line.
func messageFrom(value string) string {
	line := strings.Join(strings.FieldsFunc(value, func(r rune) bool { return unicode.IsSpace(r) || r == '\uFEFF' }), " ")
	runes := []rune(line)
	if len(runes) > maxMessageLength {
		return string(runes[:maxMessageLength-1]) + "…"
	}
	return line
}

// reporter is one herdr reporting session. Handlers may run concurrently, so
// every field is guarded by mu; host and herdr calls run outside it.
type reporter struct {
	env herdrEnv

	mu             sync.Mutex
	rootSession    bool
	sessionPath    string
	sessionID      string
	agentActive    bool
	blockedMessage string
	// Prompts currently open: PiG reports only the outermost prompt of a kind,
	// and producers can overlap, so count.
	blockedCount int
	hasLast      bool
	// resumeOff is set once herdr refused a resume command (herdr before 0.9.2,
	// or invalid_resume_argv): later reports leave it out.
	resumeOff   bool
	lastState   agentState
	lastMessage string
	released    bool

	// Only the newest state matters: while a call is in flight, newer states
	// replace the queued one, and calls never overlap, so herdr sees them in order.
	queued  *report
	sending bool
	drained chan struct{} // closed when the drain goroutine exits; valid while sending
}

func (r *reporter) isRoot() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rootSession
}

func (r *reporter) start(path, id string, active bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// A session start opens a fresh reporting epoch.
	r.rootSession = true
	r.released = false
	r.blockedCount, r.blockedMessage = 0, ""
	r.sessionPath, r.sessionID = path, id
	r.agentActive = active
	r.publishLocked(true)
}

func (r *reporter) agentStarted(path, id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessionPath, r.sessionID = path, id
	r.agentActive = true
	r.publishLocked(false)
}

func (r *reporter) agentSettled() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agentActive = false
	r.publishLocked(false)
}

func (r *reporter) block(label string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.rootSession {
		return
	}
	r.blockedCount++
	r.blockedMessage = messageFrom(label)
	r.publishLocked(false)
}

func (r *reporter) unblock() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.rootSession {
		return
	}
	r.blockedCount = max(0, r.blockedCount-1)
	if r.blockedCount == 0 {
		r.blockedMessage = ""
	}
	r.publishLocked(false)
}

// silence stops reporting without releasing the pane.
func (r *reporter) silence() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.released = true
	r.queued = nil
}

func (r *reporter) desiredLocked() (agentState, string) {
	if r.blockedCount > 0 {
		return stateBlocked, r.blockedMessage
	}
	if r.agentActive {
		return stateWorking, ""
	}
	return stateIdle, ""
}

func (r *reporter) publishLocked(force bool) {
	state, message := r.desiredLocked()
	if !force && r.hasLast && state == r.lastState && message == r.lastMessage {
		return
	}
	r.hasLast, r.lastState, r.lastMessage = true, state, message
	r.queueLocked(state, message)
}

func (r *reporter) queueLocked(state agentState, message string) {
	if r.released {
		// A report after the release would reclaim a pane whose agent has exited.
		return
	}
	r.queued = &report{state: state, message: message, seq: nextSeq()}
	if !r.sending {
		r.sending = true
		r.drained = make(chan struct{})
		go r.drain(r.drained)
	}
}

// drain sends queued reports one at a time until none is left.
func (r *reporter) drain(done chan struct{}) {
	defer close(done)
	for {
		r.mu.Lock()
		next := r.queued
		r.queued = nil
		if next == nil {
			r.sending = false
			r.mu.Unlock()
			return
		}
		args := r.reportArgsLocked(*next, !r.resumeOff)
		r.mu.Unlock()
		if out, err := callHerdr(r.env, args); err != nil && resumeRefused(out, err) {
			// herdr did not apply the report. Send it once more without the command,
			// silently, and stop attaching one: state and release keep working.
			r.mu.Lock()
			r.resumeOff = true
			args = r.reportArgsLocked(*next, false)
			r.mu.Unlock()
			_, _ = callHerdr(r.env, args)
		}
	}
}

// resumeRefused reports whether a failed report failed because herdr does not
// take a resume command: herdr before 0.9.2 does not know the `--` separator
// ("unknown option: --", exit 2), and 0.9.2+ answers `invalid_resume_argv` (exit
// 1) without applying the report. A timeout or any other failure is not a verdict
// on resume support.
func resumeRefused(output []byte, err error) bool {
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return false
	}
	text := strings.ToLower(string(output))
	if strings.Contains(text, "invalid_resume_argv") {
		return true
	}
	// "unknown option: --" names the separator itself; "unknown option: --message=..." does not.
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "unknown option: --" {
			return true
		}
	}
	return false
}

func (r *reporter) reportArgsLocked(rep report, withResume bool) []string {
	args := []string{
		"pane", "report-agent", r.env.paneID,
		"--source", source, "--agent", agent,
		"--state", string(rep.state), "--seq", strconv.FormatInt(rep.seq, 10),
	}
	if rep.message != "" {
		args = append(args, messageArgs(rep.message)...)
	}
	if r.sessionPath != "" {
		args = append(args, "--agent-session-path", r.sessionPath)
	}
	if r.sessionID != "" {
		args = append(args, "--agent-session-id", r.sessionID)
	}
	// The command goes last, after `--`. The state report in the same call is what
	// makes this source hold the pane, which herdr requires before it takes one.
	if withResume {
		if argv := resumeArgv(r.sessionPath, r.sessionID); argv != nil {
			args = append(args, "--")
			args = append(args, argv...)
		}
	}
	return args
}

// release stops new reports, drops the queued one and lets the in-flight call
// finish so the release is the last thing herdr hears from this pane.
func (r *reporter) release() {
	r.mu.Lock()
	r.released = true
	r.queued = nil
	var inFlight chan struct{}
	if r.sending {
		inFlight = r.drained
	}
	seq := nextSeq()
	r.mu.Unlock()
	if inFlight != nil {
		<-inFlight
	}
	_, _ = callHerdr(r.env, []string{
		"pane", "release-agent", r.env.paneID,
		"--source", source, "--agent", agent, "--seq", strconv.FormatInt(seq, 10),
	})
}

// callHerdr runs one herdr command and returns its output. Callers ignore a
// failed or slow herdr call: the reporter is best effort, and it must never fail
// or stall the agent.
func callHerdr(env herdrEnv, args []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	cmd := newCommand(ctx, env.bin, args)
	// A herdr that hangs is killed at the deadline; a grandchild holding the pipe
	// must not keep Wait from returning.
	cmd.WaitDelay = time.Second
	return cmd.CombinedOutput()
}
