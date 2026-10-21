package tintinweb_subagents

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

// A subagent runs as a child `pig --mode rpc` process: the task goes in as a prompt, steering as a `steer`
// command, a stop as `abort`, and the answer and the usage are read back with get_last_assistant_text and
// get_session_stats. (The original runs the child in-process through Pi's SDK, which a Go extension cannot; PORT.md.)

// childSpec is what a child is started with.
type childSpec struct {
	Prompt       string
	SystemPrompt string
	PromptMode   string // "replace" or "append"
	Model        string
	Thinking     string
	Cwd          string
	MaxTurns     int
	GraceTurns   int // turns allowed after the wrap-up steer before the run is aborted (0: the default, 5)
	// InheritContext, when set, gives the text of the parent conversation that goes in front of the prompt. It is
	// called when the run starts. upstream: agent-runner.ts runAgent (options.inheritContext).
	InheritContext func() (string, error)

	// Tool, extension and skill scope, from planScope.
	NoExtensions   bool     // --no-extensions: no extension (and so no extension or MCP tool) is loaded
	ExtensionPaths []string // --extension: extensions loaded when the defaults are not
	AllowTools     []string // --tools, a strict allowlist; nil: unset, an empty list: --no-tools
	ExcludeTools   []string // --exclude-tools
	NoSkills       bool     // --no-skills: the skills the agent needs are in its system prompt, or it gets none
	Trust          string   // "approve" or "no-approve": the child trusts the project when the parent does
	Warnings       []string // misconfigurations of the scope, each counted like a tool use of the run

	// Session. upstream: agent-runner.ts (SessionManager.create, open, inMemory).
	Persist       bool   // the conversation is a pi session file (the original's persist_session / rememberAgents)
	SessionDir    string // --session-dir: where that file goes; "": pi's normal session directory
	ResumeFile    string // --session: reopen this session file instead of starting a conversation
	ParentSession string // the parent's session file, recorded in the new session's header so /resume nests it
	SessionName   string // --name

	// Schema (JSON) makes the child report through a StructuredOutput tool built from it. The tool is an extension
	// tool of the child, so a schema cannot be combined with NoExtensions.
	Schema string
	// Transcript and SessionID: the agent writes a `.output` transcript under the session's task directory.
	Transcript bool
	SessionID  string
	// Hooks receives what the child's event stream carries (usage, compaction, every event). Set by the manager.
	Hooks *childHooks
}

// childResult is what a finished child reports.
type childResult struct {
	Text     string
	ToolUses int
	Turns    int
	// ContextPercent is the child's context-window utilization when it settled, nil when unavailable (no model
	// window, or just after a compaction). upstream: usage.ts getSessionContextPercent.
	ContextPercent *float64
	// Conversation is the child's conversation as it ended (after any compaction) as getAgentConversation formats it,
	// bounded by maxConversationBytes. upstream: getAgentConversation(session).
	Conversation string
	// StructuredJSON is the validated StructuredOutput payload, when a schema was asked for and one validated.
	StructuredJSON    string
	StructuredRetried bool
	WrappedUp         bool   // steered to wrap up at the turn limit. upstream: agent-runner.ts `steered`
	Aborted           bool   // aborted after the grace turns past the limit. upstream: agent-runner.ts `aborted`
	Failure           string // the final turn failed (a provider error, or the output limit with no text). upstream: finalTurnError
}

// defaultGraceTurns is how many turns past the limit an agent that was told to wrap up may take before it is
// aborted. upstream: agent-runner.ts:353 (graceTurns = 5).
const defaultGraceTurns = 5

// turnLimitSteer is the message an agent gets when it reaches its turn limit. upstream: agent-runner.ts:1061.
const turnLimitSteer = "You have reached your turn limit. Wrap up immediately — provide your final answer now."

// child is a running agent that can be steered, stopped and resumed.
type child interface {
	// wait blocks until the current run has settled (or failed), and reports its outcome.
	wait() (*childResult, error)
	steer(message string) error
	abort()
	// sessionFile is the pi session file of the conversation, "" when it lives in this child only.
	sessionFile() string
	// resume sends a new prompt into the same conversation and starts a new run; wait reports that run.
	// upstream: agent-runner.ts resumeAgent.
	resume(prompt string) error
	// close ends the child process.
	close()
	// alive reports a child that can still take a prompt: a process that holds its conversation in memory.
	alive() bool
}

// childStarter starts a child (the real one starts a process; a test replaces it).
type childStarter func(ctx context.Context, spec childSpec) (child, error)

// pigBinary is the pig to start. An extension is a process of its own, so os.Executable() is not pig unless the
// extension is built into it (a Piglet Binary). The candidates, in order: TINTINWEB_SUBAGENTS_PIG_BINARY (used as
// it is), PIG_HARNESS_BINARY (the pig that started this extension process, which PiG puts in the environment of
// every extension it starts), the parent process (Linux and Android), this executable (a Piglet Binary, so its
// agents run with the same built-in members), and pig on PATH. A candidate counts when `--version` prints a version
// number; the first that does is kept.
func pigBinary() (string, error) {
	if b := os.Getenv("TINTINWEB_SUBAGENTS_PIG_BINARY"); b != "" {
		return b, nil
	}
	pigOnce.Do(func() {
		var candidates []string
		if b := os.Getenv("PIG_HARNESS_BINARY"); b != "" {
			candidates = append(candidates, b)
		}
		if exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", os.Getppid())); err == nil {
			candidates = append(candidates, exe)
		}
		if exe, err := os.Executable(); err == nil {
			candidates = append(candidates, exe)
		}
		if p, err := exec.LookPath("pig"); err == nil {
			candidates = append(candidates, p)
		}
		for _, c := range candidates {
			if isPig(c) {
				pigPath = c
				return
			}
		}
		pigErr = errors.New("cannot find the pig executable to run an agent with; set TINTINWEB_SUBAGENTS_PIG_BINARY")
	})
	return pigPath, pigErr
}

var (
	pigOnce sync.Once
	pigPath string
	pigErr  error
	version = regexp.MustCompile(`^\d+\.\d+\.\d+`)
)

func isPig(path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.Stdin = nil
	// Without this extension's connection details: a probed extension executable must not reach the host.
	cmd.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "PIG_EXT_") })
	out, err := cmd.Output()
	return err == nil && version.Match(bytes.TrimSpace(out))
}

// childArgs is the command line of a child. upstream: agent-runner.ts (the loader, the session and the tool options
// runAgent builds: no context files, prompt templates or themes; the scope of planScope; the session manager).
func childArgs(spec childSpec) []string {
	args := []string{"--mode", "rpc"}
	switch {
	case spec.ResumeFile != "":
		args = append(args, "--session", spec.ResumeFile)
	case !spec.Persist:
		args = append(args, "--no-session")
	}
	if spec.SessionDir != "" {
		args = append(args, "--session-dir", spec.SessionDir)
	}
	if spec.SessionName != "" {
		args = append(args, "--name", spec.SessionName)
	}
	// An explicit empty append keeps pig from discovering the global or project APPEND_SYSTEM.md, which the original's
	// loader suppresses too (appendSystemPromptOverride) so that prompt_mode: replace and isolated are not defeated.
	args = append(args, "--no-context-files", "--no-prompt-templates", "--no-themes", "--append-system-prompt", "")
	switch spec.Trust {
	case "approve":
		args = append(args, "--approve")
	case "no-approve":
		args = append(args, "--no-approve")
	}
	if spec.NoExtensions {
		args = append(args, "--no-extensions")
	}
	for _, p := range spec.ExtensionPaths {
		args = append(args, "--extension", p)
	}
	if spec.NoSkills {
		args = append(args, "--no-skills")
	}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	if spec.Thinking != "" {
		args = append(args, "--thinking", spec.Thinking)
	}
	if spec.AllowTools != nil {
		tools := spec.AllowTools
		if spec.Schema != "" { // re-admitted: the call asked for a schema, so denying the tool would make it unsatisfiable
			tools = append(slices.Clone(tools), structuredOutputToolName)
		}
		if len(tools) == 0 {
			args = append(args, "--no-tools")
		} else {
			args = append(args, "--tools", strings.Join(tools, ","))
		}
	}
	if len(spec.ExcludeTools) > 0 {
		args = append(args, "--exclude-tools", strings.Join(spec.ExcludeTools, ","))
	}
	if strings.TrimSpace(spec.SystemPrompt) != "" {
		if spec.PromptMode == "append" {
			args = append(args, "--append-system-prompt", spec.SystemPrompt)
		} else {
			args = append(args, "--system-prompt", spec.SystemPrompt)
		}
	}
	return args
}

// depthEnv marks a child: it registers no agent tools, so there is no nesting.
const depthEnv = "TINTINWEB_SUBAGENT_DEPTH"

// rpcChild is a child pig process. A run is one prompt and what follows until the agent settles; the child lives
// on after a run when its conversation exists nowhere else, so that resume can continue it.
type rpcChild struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	mu      sync.Mutex
	nextID  int
	pending map[string]chan map[string]any
	exited  chan struct{} // closed when the child's output ends
	exitErr chan error    // why the child ended, when the run had not settled
	keep    bool          // the conversation is in memory only: the process outlives a run
	session string        // the pi session file, "" for an in-memory conversation
	killed  bool
	// limits carries the turn-limit commands (the wrap-up steer, then the abort) to one sender, in order.
	limits chan map[string]any
	grace  int
	// abortWait is abortGrace as it was when the child started.
	abortWait time.Duration

	// The current run. upstream: agent-runner.ts runAgent / resumeAgent.
	settled        chan struct{}
	runErr         error
	toolUse, turns int
	maxTurn        int
	wrapped        bool
	aborted        bool // the run was aborted at maxTurn+grace turns
	first          bool // the run is the child's first (the RPC text fallback is for it only)
	// The last assistant message of the run: its stop reason, error and text, and the last text of any of its messages.
	lastStop, lastError, lastText, lastNonEmpty string
	// conv is the conversation the stream carried (bounded), and percent the context percent read just before a
	// stop: what a stopped or failed agent still reports. Both under mu.
	conv    conversationLog
	percent *float64
	hooks   *childHooks
	// capture is the StructuredOutput box, nil when no schema was asked for. retrying: the extra structured prompt
	// is in flight, so the next agent_settled ends the retry (settledRetry), not the first run (settled).
	capture      *structuredCapture
	retrying     bool
	settledRetry chan struct{}
}

func startRPCChild(ctx context.Context, spec childSpec) (child, error) {
	bin, err := pigBinary()
	if err != nil {
		return nil, err
	}
	if spec.Schema != "" && spec.NoExtensions {
		return nil, errors.New("a structured-output agent cannot be isolated: its StructuredOutput tool is an extension tool of the child")
	}
	// The child lives as long as ctx: the manager ends the context when it closes the child, not when a run settles, so
	// that a conversation that exists only in this process outlives the run. A cancel takes the whole process group.
	cmd := exec.CommandContext(ctx, bin, childArgs(spec)...)
	cmd.Cancel = func() error { terminate(cmd); return nil }
	cmd.Dir = spec.Cwd
	cmd.Env = append(os.Environ(), depthEnv+"=1")
	// The schema goes through a private file: an environment string is capped (Windows 32,767 characters, Linux
	// 128 KiB) and a schema may be larger.
	schemaFile := ""
	if spec.Schema != "" {
		if schemaFile, err = writeSchemaFile(spec.Schema); err != nil {
			return nil, err
		}
		cmd.Env = append(cmd.Env, structuredSchemaFileEnv+"="+schemaFile)
	}
	removeSchema := func() {
		if schemaFile != "" {
			_ = os.Remove(schemaFile)
		}
	}
	setProcessGroup(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		removeSchema()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		removeSchema()
		return nil, err
	}
	var stderr strings.Builder
	cmd.Stderr = &tailWriter{w: &stderr}
	if err := cmd.Start(); err != nil {
		removeSchema()
		return nil, err
	}
	c := &rpcChild{cmd: cmd, stdin: stdin, pending: map[string]chan map[string]any{}, exited: make(chan struct{}), exitErr: make(chan error, 1),
		grace: spec.GraceTurns, abortWait: abortGrace, keep: !spec.Persist && spec.ResumeFile == "", first: spec.ResumeFile == "", hooks: spec.Hooks}
	if spec.Schema != "" {
		c.capture = &structuredCapture{}
	}
	if c.grace <= 0 {
		c.grace = defaultGraceTurns
	}
	c.begin(spec.MaxTurns)
	c.limits = make(chan map[string]any, 2) // at most one steer and one abort are ever sent per run
	goGuarded("child turn limit", func() {
		for {
			select {
			case cmd := <-c.limits:
				guard("child turn limit command", func() { _, _ = c.call(cmd) })
			case <-c.exited:
				return
			}
		}
	})
	goGuarded("child reader", func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 1<<20), 64<<20)
		if guard("child output", func() {
			for sc.Scan() {
				var m map[string]any
				if json.Unmarshal(sc.Bytes(), &m) != nil {
					continue
				}
				c.handle(m)
			}
		}) {
			terminate(cmd) // an output the reader could not handle: end the child, which settles the run with an error
		}
		err := cmd.Wait()
		removeSchema()
		close(c.exited)
		c.mu.Lock()
		killed := c.killed
		settled := c.settled
		retry := c.settledRetry
		c.mu.Unlock()
		defer func() { // a retry that waits for the end of the run is released by the child's end
			select {
			case <-retry:
			default:
				close(retry)
			}
		}()
		select {
		case <-settled:
		default:
			c.mu.Lock()
			switch {
			case killed:
				c.runErr = errors.New("aborted")
			case err != nil:
				c.runErr = fmt.Errorf("the child exited: %v: %s", err, strings.TrimSpace(stderr.String()))
			default:
				c.runErr = errors.New("the child exited before the agent settled")
			}
			c.mu.Unlock()
			close(settled)
		}
	})
	// A new persisted conversation is linked to the parent's, which nests it under the parent in pi's /resume.
	if spec.Persist && spec.ResumeFile == "" && spec.ParentSession != "" {
		if _, err := c.call(map[string]any{"type": "new_session", "parentSession": spec.ParentSession}); err != nil {
			c.kill()
			return nil, err
		}
	}
	// The name goes on the session the agent runs in: --name names the process's first session only, and the one
	// new_session started replaces it. A name that cannot be set does not stop the agent. upstream: agent-runner.ts:1010-1013.
	if spec.SessionName != "" {
		_, _ = c.call(map[string]any{"type": "set_session_name", "name": spec.SessionName})
	}
	if spec.Persist || spec.ResumeFile != "" {
		m, err := c.call(map[string]any{"type": "get_state"})
		if err != nil {
			c.kill()
			return nil, err
		}
		if d, ok := m["data"].(map[string]any); ok {
			c.session, _ = d["sessionFile"].(string)
		}
	}
	if _, err := c.call(map[string]any{"type": "prompt", "message": spec.Prompt}); err != nil {
		c.kill()
		return nil, err
	}
	return c, nil
}

// begin resets the state of a run. maxTurns 0 is no limit.
func (c *rpcChild) begin(maxTurns int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.settled = make(chan struct{})
	c.settledRetry, c.retrying = make(chan struct{}), false
	c.runErr = nil
	c.toolUse, c.turns, c.maxTurn = 0, 0, maxTurns
	c.wrapped, c.aborted = false, false
	c.lastStop, c.lastError, c.lastText, c.lastNonEmpty = "", "", "", ""
}

func (c *rpcChild) sessionFile() string { return c.session }

// resume sends a new prompt into the conversation. A resumed turn has no turn limit, as in the original
// (resumeAgent counts no turns). upstream: agent-runner.ts resumeAgent.
func (c *rpcChild) resume(prompt string) error {
	if !c.alive() {
		return errors.New("the agent's process has ended")
	}
	c.begin(0)
	c.mu.Lock()
	c.first = false
	c.mu.Unlock()
	_, err := c.call(map[string]any{"type": "prompt", "message": prompt})
	return err
}

func (c *rpcChild) alive() bool {
	select {
	case <-c.exited:
		return false
	default:
		return c.keep
	}
}

func (c *rpcChild) close() {
	_ = c.stdin.Close()
	terminate(c.cmd)
}

func (c *rpcChild) handle(m map[string]any) {
	if c.hooks != nil && c.hooks.Event != nil && m["type"] != "response" {
		c.hooks.Event(m)
	}
	switch m["type"] {
	case "response":
		id, _ := m["id"].(string)
		c.mu.Lock()
		ch := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if ch != nil {
			ch <- m
		}
	case "tool_execution_start":
		c.mu.Lock()
		c.toolUse++
		c.mu.Unlock()
	case "tool_execution_end":
		c.captureStructured(m)
	case "compaction_end":
		// Only a successful compaction counts; an aborted or failed one leaves the session untouched.
		// upstream: agent-runner.ts:1091.
		if aborted, _ := m["aborted"].(bool); !aborted && c.hooks != nil && c.hooks.Compaction != nil {
			if result, _ := m["result"].(map[string]any); result != nil {
				reason, _ := m["reason"].(string)
				before, _ := result["tokensBefore"].(float64)
				c.hooks.Compaction(reason, int(before))
			}
		}
	case "turn_end":
		// Graceful shutdown at the turn limit: steer the agent to wrap up, and abort it if it is still going
		// graceTurns turns later. upstream: agent-runner.ts:1055-1066.
		c.mu.Lock()
		c.turns++
		steer, stop := false, false
		if c.maxTurn > 0 {
			if !c.wrapped && c.turns >= c.maxTurn {
				c.wrapped, steer = true, true
			} else if c.wrapped && !c.aborted && c.turns >= c.maxTurn+c.grace {
				c.aborted, stop = true, true
			}
		}
		c.mu.Unlock()
		if steer {
			c.limits <- map[string]any{"type": "steer", "message": turnLimitSteer}
		}
		if stop {
			c.limits <- map[string]any{"type": "abort"}
		}
	case "message_end":
		msg, ok := m["message"].(map[string]any)
		if ok {
			c.mu.Lock()
			c.conv.add(msg)
			c.mu.Unlock()
		}
		if ok && msg["role"] == "assistant" {
			stop, _ := msg["stopReason"].(string)
			errText, _ := msg["errorMessage"].(string)
			text := assistantText(msg["content"])
			c.mu.Lock()
			c.lastStop, c.lastError, c.lastText = stop, errText, text
			if strings.TrimSpace(text) != "" {
				c.lastNonEmpty = text
			}
			c.mu.Unlock()
			// The message's spend, one delta per assistant message. upstream: agent-runner.ts:1081.
			if u, ok := msg["usage"].(map[string]any); ok && c.hooks != nil && c.hooks.Usage != nil {
				c.hooks.Usage(usageOf(u))
			}
		}
	case "agent_settled":
		c.mu.Lock()
		settled, retrying, retry := c.settled, c.retrying, c.settledRetry
		c.mu.Unlock()
		if retrying {
			select {
			case <-retry:
			default:
				close(retry)
			}
			return
		}
		select {
		case <-settled:
		default:
			close(settled)
		}
	}
}

// call sends a command and waits for its response.
func (c *rpcChild) call(cmd map[string]any) (map[string]any, error) {
	return c.callWithin(cmd, 60*time.Second)
}

// callWithin is call with its own deadline.
func (c *rpcChild) callWithin(cmd map[string]any, limit time.Duration) (map[string]any, error) {
	c.mu.Lock()
	c.nextID++
	id := fmt.Sprint(c.nextID)
	ch := make(chan map[string]any, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	cmd["id"] = id
	data, _ := json.Marshal(cmd)
	if _, err := c.stdin.Write(append(data, '\n')); err != nil {
		return nil, err
	}
	select {
	case m := <-ch:
		if ok, _ := m["success"].(bool); !ok {
			return nil, fmt.Errorf("%v", m["error"])
		}
		return m, nil
	case <-c.exited:
		return nil, errors.New("the child exited before it answered")
	case <-time.After(limit):
		return nil, errors.New("the child did not answer")
	}
}

func (c *rpcChild) steer(message string) error {
	_, err := c.call(map[string]any{"type": "steer", "message": message})
	return err
}

// abortGrace is how long a child that keeps its conversation in memory has to settle after an abort before its
// process is ended.
var abortGrace = 10 * time.Second

// abort stops the current run. A child that keeps its conversation in memory is told to stop and lives on, so that
// the agent can be resumed (the original's `session.abort()` leaves the session as it is); one whose conversation is in
// a file, which a resume reopens, is ended. upstream: agent-manager.ts abort.
func (c *rpcChild) abort() {
	if !c.keep {
		c.mu.Lock()
		c.killed = true // before the abort is sent: the process may exit on it, and the run is then aborted, not lost
		c.mu.Unlock()
		// The kill ends the child: read what a stopped agent still reports (its context percent) while it can answer.
		if m, err := c.callWithin(map[string]any{"type": "get_session_stats"}, 2*time.Second); err == nil {
			d, _ := m["data"].(map[string]any)
			p := contextPercentOf(d)
			c.mu.Lock()
			c.percent = p
			c.mu.Unlock()
		}
		_, _ = c.call(map[string]any{"type": "abort"})
		terminate(c.cmd)
		return
	}
	c.mu.Lock()
	settled := c.settled
	c.mu.Unlock()
	_, _ = c.call(map[string]any{"type": "abort"})
	// A run that never settles must not hold its agent forever: past the grace the process goes.
	goGuarded("child abort", func() {
		select {
		case <-settled:
		case <-c.exited:
		case <-time.After(c.abortWait):
			c.kill()
		}
	})
}

// kill ends the child's process, and with it its conversation.
func (c *rpcChild) kill() {
	c.mu.Lock()
	c.killed = true
	c.mu.Unlock()
	terminate(c.cmd)
}

func (c *rpcChild) wait() (*childResult, error) {
	c.mu.Lock()
	settled := c.settled
	c.mu.Unlock()
	<-settled
	c.mu.Lock()
	err := c.runErr
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	res := &childResult{}
	// One more prompt when a schema was asked for and nothing usable came back (the model answered in prose, or only
	// ever called the tool invalidly), unless the run was aborted. upstream: agent-runner.ts:1120.
	if c.capture != nil {
		c.mu.Lock()
		again := structuredRetryWanted(c.capture.JSONUnset(), c.aborted, c.killed)
		c.retrying = again
		retry := c.settledRetry
		c.mu.Unlock()
		if again {
			res.StructuredRetried = true
			if _, err := c.call(map[string]any{"type": "prompt", "message": structuredRetryPrompt(c.capture)}); err == nil {
				select {
				case <-retry:
				case <-c.exited:
				}
			}
		}
	}
	c.mu.Lock()
	res.Text = strings.TrimSpace(c.lastText)
	if res.Text == "" {
		res.Text = strings.TrimSpace(c.lastNonEmpty)
	}
	first := c.first
	c.mu.Unlock()
	if res.Text == "" && first { // a resumed run never takes the previous turn's answer (#144)
		if m, err := c.call(map[string]any{"type": "get_last_assistant_text"}); err == nil {
			if d, ok := m["data"].(map[string]any); ok {
				t, _ := d["text"].(string)
				res.Text = strings.TrimSpace(t)
			}
		}
	}
	if m, err := c.call(map[string]any{"type": "get_session_stats"}); err == nil {
		if d, ok := m["data"].(map[string]any); ok {
			if f, ok := d["toolCalls"].(float64); ok {
				res.ToolUses = int(f)
			}
			res.ContextPercent = contextPercentOf(d)
		}
	}
	// The conversation as the child's session ends (after any compaction); the stream's own copy when it cannot say.
	messages := c.messages()
	if messages != nil {
		res.Conversation = conversationOf(messages)
	} else {
		res.Conversation, _ = c.snapshot()
	}
	if strings.TrimSpace(res.Text) == "" && first { // a final turn that errored with no content must not hide the turns before it
		res.Text = lastAssistantText(messages)
	}
	c.mu.Lock()
	res.Turns, res.WrappedUp, res.Aborted = c.turns, c.wrapped, c.aborted
	switch {
	case c.lastStop == "error":
		res.Failure = strings.TrimSpace(c.lastError)
		if res.Failure == "" {
			res.Failure = "provider error with no output"
		}
	case c.lastStop == "length" && strings.TrimSpace(c.lastText) == "":
		res.Failure = "run hit the output token limit before producing any text"
	}
	if res.ToolUses == 0 {
		res.ToolUses = c.toolUse
	}
	keep := c.keep
	c.mu.Unlock()
	if c.capture != nil {
		res.StructuredJSON = c.capture.JSON
		// A child asked for structured output that never gave any has failed, however articulate its prose was.
		if res.Failure == "" {
			res.Failure = structuredFailure(c.capture)
		}
	}
	if !keep { // a persisted conversation is reopened from its file by resume
		c.close()
	}
	return res, nil
}

// structuredRetryWanted is whether a settled run that was asked for structured output gets the one extra prompt: only
// when nothing validated and the run was not cut short (a turn-limit abort or a stop). upstream: agent-runner.ts:1120.
func structuredRetryWanted(nothingValidated, aborted, killed bool) bool {
	return nothingValidated && !aborted && !killed
}

// lastAssistantText is the last non-empty assistant text of a conversation, trimmed. upstream: getLastAssistantText.
func lastAssistantText(messages []map[string]any) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i]["role"] != "assistant" {
			continue
		}
		if text := strings.TrimSpace(contentText(messages[i]["content"])); text != "" {
			return text
		}
	}
	return ""
}

// stats reads the child's live context-window utilization (nil when unavailable). upstream: getSessionContextPercent.
func (c *rpcChild) stats() *float64 {
	m, err := c.call(map[string]any{"type": "get_session_stats"})
	if err != nil {
		return nil
	}
	d, _ := m["data"].(map[string]any)
	return contextPercentOf(d)
}

// messages reads the child's live conversation. upstream: session.messages.
func (c *rpcChild) messages() []map[string]any {
	m, err := c.call(map[string]any{"type": "get_messages"})
	if err != nil {
		return nil
	}
	d, _ := m["data"].(map[string]any)
	var out []map[string]any
	for _, x := range asList(d["messages"]) {
		if msg, ok := x.(map[string]any); ok {
			out = append(out, msg)
		}
	}
	return out
}

// snapshot is the conversation the stream carried and the context percent read before the stop (nil when none was).
func (c *rpcChild) snapshot() (string, *float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conv.String(), c.percent
}

// captureStructured reads a StructuredOutput call's outcome off the child's tool_execution_end event: the child's
// tool put the canonical payload (or the rejection) in the result's details.
func (c *rpcChild) captureStructured(m map[string]any) {
	if c.capture == nil || m["toolName"] != structuredOutputToolName {
		return
	}
	result, _ := m["result"].(map[string]any)
	details, _ := result["details"].(map[string]any)
	c.capture.mu.Lock()
	defer c.capture.mu.Unlock()
	c.capture.Called = true
	if isErr, _ := m["isError"].(bool); isErr {
		c.capture.HasError = true
		c.capture.LastError = rejectionReason(result)
		return
	}
	if j, _ := details["json"].(string); j != "" {
		c.capture.JSON, c.capture.LastError, c.capture.HasError = j, "", false
	}
}

// rejectionReason is the schema verdict inside the tool's rejection text.
func rejectionReason(result map[string]any) string {
	text := ""
	for _, x := range asList(result["content"]) {
		if part, ok := x.(map[string]any); ok {
			t, _ := part["text"].(string)
			text += t
		}
	}
	text = strings.TrimPrefix(text, "StructuredOutput did not match the required schema:\n")
	return strings.TrimSuffix(text, "\nCall it again with a corrected value.")
}

// JSONUnset reports whether no valid payload was captured yet.
func (c *structuredCapture) JSONUnset() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.JSON == ""
}

// usageOf reads one message's usage the way agent-runner.ts:1082 does: absent fields are 0, cost is cost.total.
func usageOf(u map[string]any) lifetimeUsage {
	n := func(k string) int { f, _ := u[k].(float64); return int(f) }
	cost := 0.0
	if c, ok := u["cost"].(map[string]any); ok {
		cost, _ = c["total"].(float64)
	}
	return lifetimeUsage{Input: n("input"), Output: n("output"), CacheWrite: n("cacheWrite"), CacheRead: n("cacheRead"), Cost: cost}
}

// contextPercentOf is contextUsage.percent of a get_session_stats answer, nil when null or absent.
func contextPercentOf(stats map[string]any) *float64 {
	cu, _ := stats["contextUsage"].(map[string]any)
	if p, ok := cu["percent"].(float64); ok {
		return &p
	}
	return nil
}

// assistantText joins the text parts of an assistant message's content.
func assistantText(content any) string {
	if s, ok := content.(string); ok {
		return s
	}
	var b strings.Builder
	for _, part := range asList(content) {
		if m, ok := part.(map[string]any); ok && m["type"] == "text" {
			t, _ := m["text"].(string)
			b.WriteString(t)
		}
	}
	return b.String()
}

func asList(v any) []any { l, _ := v.([]any); return l }

// tailWriter keeps the last 8 KB written to it.
type tailWriter struct {
	mu sync.Mutex
	w  *strings.Builder
}

func (t *tailWriter) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.w.Write(p)
	if s := t.w.String(); len(s) > 8192 {
		t.w.Reset()
		t.w.WriteString(s[len(s)-8192:])
	}
	return len(p), nil
}
