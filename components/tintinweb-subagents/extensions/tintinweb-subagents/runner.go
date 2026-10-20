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
	Tools        []string // nil: the child's default tools
	Cwd          string
	MaxTurns     int
	GraceTurns   int  // turns allowed after the wrap-up steer before the run is aborted (0: the default, 5)
	Isolated     bool // no extensions (and so no extension tools) in the child, unless it is given nested tools
	// Nested delegation. upstream: agent-runner.ts nestedRuntime.
	AgentID   string // the agent this child is: the owner of the children it spawns
	Depth     int    // this child's depth (a top-level agent's child is 1)
	MaxDepth  int    // the cap of the branch: a child at or past it gets no nested tools
	Allowed   any    // the agent file's allowed_subagents: nil, "all" or []string
	ConfigCwd string // the config root the child's own children are resolved from
}

// nestedActive says whether the child is given the nested tools: its file opted in and it is below the depth cap. A
// child at the cap can never spawn, so it gets nothing rather than three tools whose every call is an error.
// upstream: agent-runner.ts:847-865.
func (s childSpec) nestedActive() bool { return s.Allowed != nil && max(s.Depth, 1) < s.MaxDepth }

// childResult is what a finished child reports.
type childResult struct {
	Text      string
	ToolUses  int
	Tokens    int
	Turns     int
	WrappedUp bool   // steered to wrap up at the turn limit. upstream: agent-runner.ts `steered`
	Aborted   bool   // aborted after the grace turns past the limit. upstream: agent-runner.ts `aborted`
	Failure   string // the final turn failed (a provider error, or the output limit with no text). upstream: finalTurnError
}

// defaultGraceTurns is how many turns past the limit an agent that was told to wrap up may take before it is
// aborted. upstream: agent-runner.ts:353 (graceTurns = 5).
const defaultGraceTurns = 5

// turnLimitSteer is the message an agent gets when it reaches its turn limit. upstream: agent-runner.ts:1061.
const turnLimitSteer = "You have reached your turn limit. Wrap up immediately — provide your final answer now."

// child is a running agent that can be steered and stopped.
type child interface {
	// wait blocks until the child has settled (or failed), and reports its outcome.
	wait() (*childResult, error)
	steer(message string) error
	abort()
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

// childArgs is the command line of a child. upstream: agent-runner.ts (the session options the child is given).
func childArgs(spec childSpec) []string {
	args := []string{"--mode", "rpc", "--no-session"}
	tools := spec.Tools
	if spec.nestedActive() {
		// The nested tools live in this extension, so the child must load it: `extensions: false` alone is held by the
		// tool list (nothing but the built-ins and the nested tools is admitted), not by --no-extensions.
		switch {
		case tools != nil:
			tools = append(slices.Clone(tools), nestedToolNames...)
		case spec.Isolated:
			tools = append(slices.Clone(builtinToolNames), nestedToolNames...)
		}
	} else if spec.Isolated {
		args = append(args, "--no-extensions")
	}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	if spec.Thinking != "" {
		args = append(args, "--thinking", spec.Thinking)
	}
	if tools != nil {
		if len(tools) == 0 {
			args = append(args, "--no-tools")
		} else {
			args = append(args, "--tools", strings.Join(tools, ","))
		}
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

// The environment a child is started with. depthEnv marks a child: it registers no top-level tools. The rest is set
// only for a child that gets the nested tools, and says who it is and how far it may delegate (nested.go).
const (
	depthEnv     = "TINTINWEB_SUBAGENT_DEPTH"      // this child's depth, 1 for a top-level agent's child
	maxDepthEnv  = "TINTINWEB_SUBAGENT_MAX_DEPTH"  // the branch's cap
	allowedEnv   = "TINTINWEB_SUBAGENT_ALLOWED"    // JSON: "all" or the list of agent types
	agentIDEnv   = "TINTINWEB_SUBAGENT_ID"         // this child's agent id: the owner of its children
	configCwdEnv = "TINTINWEB_SUBAGENT_CONFIG_CWD" // the config root its children's agent files are read from
	usageEnv     = "TINTINWEB_SUBAGENT_USAGE_FILE" // where the child writes the tokens its own children spent
)

var subagentEnv = []string{depthEnv, maxDepthEnv, allowedEnv, agentIDEnv, configCwdEnv, usageEnv}

// childEnv is the environment of a child: the parent's, with this child's place in the tree. A variable the parent
// itself was started with (it may be a nested child) never reaches a grandchild.
func childEnv(spec childSpec, usageFile string) []string {
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		for _, name := range subagentEnv {
			if strings.HasPrefix(kv, name+"=") {
				return true
			}
		}
		return false
	})
	env = append(env, fmt.Sprintf("%s=%d", depthEnv, max(spec.Depth, 1)))
	if spec.nestedActive() {
		allowed, _ := json.Marshal(spec.Allowed)
		env = append(env, fmt.Sprintf("%s=%d", maxDepthEnv, spec.MaxDepth), allowedEnv+"="+string(allowed), agentIDEnv+"="+spec.AgentID,
			configCwdEnv+"="+spec.ConfigCwd, usageEnv+"="+usageFile)
	}
	return env
}

type rpcChild struct {
	cmd     *exec.Cmd
	usage   string // the file the child's own children's spend is written to ("" without nested tools)
	stdin   io.WriteCloser
	mu      sync.Mutex
	nextID  int
	pending map[string]chan map[string]any
	settled chan struct{}
	errc    chan error
	events  []map[string]any
	toolUse int
	turns   int
	maxTurn int
	grace   int
	wrapped bool
	aborted bool // the run was aborted at maxTurn+grace turns
	killed  bool
	// The last assistant message of the run: its stop reason, error and text. upstream: finalTurnError.
	lastStop, lastError, lastText string
	exited                        chan struct{} // closed when the child's output ends
	// limits carries the turn-limit commands (the wrap-up steer, then the abort) to one sender, in order.
	limits chan map[string]any
}

func startRPCChild(ctx context.Context, spec childSpec) (child, error) {
	bin, err := pigBinary()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bin, childArgs(spec)...)
	cmd.Dir = spec.Cwd
	usageFile := ""
	if spec.nestedActive() {
		f, err := os.CreateTemp("", "tintinweb-subagent-usage-*")
		if err != nil {
			return nil, err
		}
		usageFile = f.Name()
		_ = f.Close()
	}
	cmd.Env = childEnv(spec, usageFile)
	setProcessGroup(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		removeUsage(usageFile)
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		removeUsage(usageFile)
		return nil, err
	}
	var stderr strings.Builder
	cmd.Stderr = &tailWriter{w: &stderr}
	if err := cmd.Start(); err != nil {
		removeUsage(usageFile)
		return nil, err
	}
	c := &rpcChild{cmd: cmd, usage: usageFile, stdin: stdin, pending: map[string]chan map[string]any{}, settled: make(chan struct{}), errc: make(chan error, 1), maxTurn: spec.MaxTurns, grace: spec.GraceTurns, exited: make(chan struct{})}
	if c.grace <= 0 {
		c.grace = defaultGraceTurns
	}
	c.limits = make(chan map[string]any, 2) // at most one steer and one abort are ever sent
	go func() {
		for {
			select {
			case cmd := <-c.limits:
				_, _ = c.call(cmd)
			case <-c.exited:
				return
			}
		}
	}()
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 1<<20), 64<<20)
		for sc.Scan() {
			var m map[string]any
			if json.Unmarshal(sc.Bytes(), &m) != nil {
				continue
			}
			c.handle(m)
		}
		err := cmd.Wait()
		close(c.exited)
		removeUsage(c.usage)
		c.mu.Lock()
		killed := c.killed
		c.mu.Unlock()
		select {
		case <-c.settled:
		default:
			switch {
			case killed:
				c.errc <- errors.New("aborted")
			case err != nil:
				c.errc <- fmt.Errorf("the child exited: %v: %s", err, strings.TrimSpace(stderr.String()))
			default:
				c.errc <- errors.New("the child exited before the agent settled")
			}
			close(c.settled)
		}
	}()
	if _, err := c.call(map[string]any{"type": "prompt", "message": spec.Prompt}); err != nil {
		c.abort()
		return nil, err
	}
	return c, nil
}

func (c *rpcChild) handle(m map[string]any) {
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
		if msg, ok := m["message"].(map[string]any); ok && msg["role"] == "assistant" {
			stop, _ := msg["stopReason"].(string)
			errText, _ := msg["errorMessage"].(string)
			c.mu.Lock()
			c.lastStop, c.lastError, c.lastText = stop, errText, assistantText(msg["content"])
			c.mu.Unlock()
		}
	case "agent_settled":
		select {
		case <-c.settled:
		default:
			close(c.settled)
		}
	}
}

// call sends a command and waits for its response.
func (c *rpcChild) call(cmd map[string]any) (map[string]any, error) {
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
	case <-time.After(60 * time.Second):
		return nil, errors.New("the child did not answer")
	}
}

func (c *rpcChild) steer(message string) error {
	_, err := c.call(map[string]any{"type": "steer", "message": message})
	return err
}

func (c *rpcChild) abort() {
	c.mu.Lock()
	c.killed = true
	c.mu.Unlock()
	_, _ = c.call(map[string]any{"type": "abort"})
	terminate(c.cmd)
}

func (c *rpcChild) wait() (*childResult, error) {
	<-c.settled
	select {
	case err := <-c.errc:
		return nil, err
	default:
	}
	res := &childResult{}
	if m, err := c.call(map[string]any{"type": "get_last_assistant_text"}); err == nil {
		if d, ok := m["data"].(map[string]any); ok {
			res.Text, _ = d["text"].(string)
		}
	}
	if m, err := c.call(map[string]any{"type": "get_session_stats"}); err == nil {
		if d, ok := m["data"].(map[string]any); ok {
			if t, ok := d["tokens"].(map[string]any); ok {
				f, _ := t["total"].(float64)
				res.Tokens = int(f)
			}
			if f, ok := d["toolCalls"].(float64); ok {
				res.ToolUses = int(f)
			}
		}
	}
	nestedTokens := readUsage(c.usage) // what the agents this one owns spent; the child's own stats do not carry it
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
	res.Tokens += nestedTokens
	if res.ToolUses == 0 {
		res.ToolUses = c.toolUse
	}
	c.mu.Unlock()
	c.stdin.Close()
	terminate(c.cmd)
	return res, nil
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
