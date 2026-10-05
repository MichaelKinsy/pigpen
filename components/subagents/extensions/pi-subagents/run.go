package pi_subagents

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Running a subagent is an addition of the Go port. The original runs child Pi sessions through its executor and its
// scripted workflows; the port starts a child `pig --print` process per agent, as PiG's own subagents do.

const (
	// maxChildren is the original's MAX_CONCURRENCY: no more children run at once, whatever the model asks for in one turn.
	maxChildren = 4
	// defaultMaxDepth is the original's DEFAULT_SUBAGENT_MAX_DEPTH; PI_SUBAGENT_MAX_DEPTH changes it, as in the original.
	defaultMaxDepth = 2
	// The original's DEFAULT_MAX_OUTPUT: the answer a child gives back is cut to this, with its marker.
	outputMaxBytes = 200 * 1024
	outputMaxLines = 5000
	// stderrTail is how much of a child's stderr is kept to explain a failure.
	stderrTail = 8 * 1024
	// killGrace is how long a cancelled child has to stop after SIGTERM before it is killed.
	killGrace = 5 * time.Second
)

// childRequest is one child process: the program, its arguments, what it reads on stdin, where it runs and its whole
// environment.
type childRequest struct {
	Bin   string
	Args  []string
	Stdin string
	Cwd   string
	Env   []string
}

// childResult is what a child gave back: its answer (stdout, bounded), the end of its stderr and its exit code.
type childResult struct {
	Stdout string
	Stderr string
	Exit   int
}

var errCancelled = errors.New("cancelled")

var runChild = func(done <-chan struct{}, req childRequest) (childResult, error) {
	return execChild(done, req, killGrace)
}

// execChild runs one child to its end, or until done closes: then the child gets SIGTERM and, after grace, is killed.
// The child dies with this process (Linux: the parent-death signal), so a killed host leaves no child behind.
func execChild(done <-chan struct{}, req childRequest, grace time.Duration) (childResult, error) {
	cmd := exec.Command(req.Bin, req.Args...)
	cmd.Dir = req.Cwd
	cmd.Env = req.Env
	if cmd.Env == nil {
		cmd.Env = []string{}
	}
	cmd.Stdin = strings.NewReader(req.Stdin)
	stdout := &headWriter{max: outputMaxBytes}
	stderr := &tailWriter{max: stderrTail}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	configureChild(cmd)
	unlock := lockThread()
	defer unlock()
	if err := cmd.Start(); err != nil {
		return childResult{}, err
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	var err error
	cancelled := false
	select {
	case err = <-waited:
	case <-done:
		cancelled = true
		terminate(cmd.Process)
		select {
		case err = <-waited:
		case <-time.After(grace):
			_ = cmd.Process.Kill()
			err = <-waited
		}
	}
	res := childResult{Stdout: stdout.text(), Stderr: strings.TrimSpace(stderr.String())}
	if cancelled {
		return res, errCancelled
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		res.Exit = ee.ExitCode()
		return res, nil
	}
	return res, err
}

// headWriter keeps the first max bytes and counts the whole stream.
type headWriter struct {
	mu    sync.Mutex
	max   int
	head  []byte
	total int
	lines int
}

func (w *headWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.total += len(p)
	w.lines += strings.Count(string(p), "\n")
	if room := w.max - len(w.head); room > 0 {
		if room > len(p) {
			room = len(p)
		}
		w.head = append(w.head, p[:room]...)
	}
	return len(p), nil
}

// text is the answer, cut as the original's truncateOutput cuts it (first lines, then bytes, then its marker).
func (w *headWriter) text() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	lines := w.lines + 1
	if w.total <= w.max && lines <= outputMaxLines {
		return strings.TrimSpace(string(w.head))
	}
	kept := string(w.head)
	if parts := strings.SplitN(kept, "\n", outputMaxLines+1); len(parts) > outputMaxLines {
		kept = strings.Join(parts[:outputMaxLines], "\n")
	}
	for len(kept) > 0 { // a character cut by the byte bound is dropped whole
		if r, size := utf8.DecodeLastRuneInString(kept); r != utf8.RuneError || size > 1 {
			break
		}
		kept = kept[:len(kept)-1]
	}
	marker := fmt.Sprintf("[TRUNCATED: showing first %d of %d lines, %s of %s]\n", strings.Count(kept, "\n")+1, lines, formatBytes(len(kept)), formatBytes(w.total))
	return marker + kept
}

// formatBytes is the original's.
func formatBytes(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%dB", n)
	case n < 1024*1024:
		return jsToFixed(float64(n)/1024, 1) + "KB"
	}
	return jsToFixed(float64(n)/(1024*1024), 1) + "MB"
}

// tailWriter keeps the last max bytes.
type tailWriter struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (w *tailWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	if len(w.buf) > w.max {
		w.buf = append([]byte(nil), w.buf[len(w.buf)-w.max:]...)
	}
	return len(p), nil
}

func (w *tailWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.ToValidUTF8(string(w.buf), "")
}

// pigBinary finds the pig to start: PIG_SUBAGENT_PIG_BINARY, the pig that runs this extension (PIG_HARNESS_BINARY, set by
// the host for an extension process), the pig next to this program, then PATH.
func pigBinary() string {
	for _, k := range []string{"PIG_SUBAGENT_PIG_BINARY", "PIG_HARNESS_BINARY"} {
		if b := strings.TrimSpace(os.Getenv(k)); b != "" {
			return b
		}
	}
	if self, err := os.Executable(); err == nil {
		if c := filepath.Join(filepath.Dir(self), "pig"); fileExists(c) {
			return c
		}
	}
	if p, err := exec.LookPath("pig"); err == nil {
		return p
	}
	return "pig"
}

func fileExists(p string) bool { st, err := os.Stat(p); return err == nil && !st.IsDir() }

// thinkingLevels are the suffixes a model may carry (`provider/id:high`).
var thinkingLevels = map[string]bool{"off": true, "minimal": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true}

func hasThinkingSuffix(model string) bool {
	i := strings.LastIndex(model, ":")
	return i >= 0 && thinkingLevels[model[i+1:]]
}

// buildChildArgs is the child's command line: no extension discovery, print mode, and what the agent definition pins
// (model, thinking, tools, system prompt). The task is not an argument: it travels on stdin (childPrompt).
func buildChildArgs(a *AgentConfig, modelOverride string) []string {
	args := []string{"--no-extensions", "--print"}
	model := modelOverride
	if model == "" && a.Model != "" && a.Model != "inherit" {
		model = a.Model
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	if !hasThinkingSuffix(modelOverride) { // the schema: a suffix of the model "overrides agent thinking default"
		switch t := a.Thinking.(type) {
		case string:
			args = append(args, "--thinking", t)
		case bool:
			if !t {
				args = append(args, "--thinking", "off")
			}
		}
	}
	if len(a.Tools) > 0 {
		args = append(args, "--tools", strings.Join(a.Tools, ","))
	}
	if jsTrim(a.SystemPrompt) != "" {
		flag := "--system-prompt"
		if a.SystemPromptMode == "append" {
			flag = "--append-system-prompt"
		}
		args = append(args, flag, a.SystemPrompt)
	}
	return args
}

// childPrompt is the message a child gets on stdin: the original prompts its children with "Task: <task>". On stdin
// nothing of the task is read as an option or a file to attach, and its length is not bounded by the argument limit.
func childPrompt(task string) string { return "Task: " + task }

// childEnv is this process's environment without the host's internals for this extension (its socket, packed cell and
// harness argument file are not the child's), and with who the child is: a subagent, how deep, which agent.
func childEnv(base []string, depth int, role string) []string {
	out := []string{}
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		switch {
		case strings.HasPrefix(k, "PIG_EXT_"), strings.HasPrefix(k, "PIG_HARNESS_"), k == "PIG_SUBAGENT", k == "PIG_SUBAGENT_DEPTH", k == "PIG_AGENT_ROLE":
			continue
		}
		out = append(out, kv)
	}
	return append(out, "PIG_SUBAGENT=1", "PIG_SUBAGENT_DEPTH="+strconv.Itoa(depth+1), "PIG_AGENT_ROLE="+role)
}

func nonNegativeInt(s string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	return n, err == nil && n >= 0
}

// subagentDepth is how deep this process is (PIG_SUBAGENT_DEPTH; a child of another subagent extension, PIG_SUBAGENT
// alone, counts as 1) and how deep children may go (PI_SUBAGENT_MAX_DEPTH, else the original's default).
func subagentDepth(getenv func(string) string) (depth, max int) {
	if n, ok := nonNegativeInt(getenv("PIG_SUBAGENT_DEPTH")); ok {
		depth = n
	} else if getenv("PIG_SUBAGENT") != "" {
		depth = 1
	}
	max = defaultMaxDepth
	if n, ok := nonNegativeInt(getenv("PI_SUBAGENT_MAX_DEPTH")); ok {
		max = n
	}
	return depth, max
}

// nestedBlocked is the original's refusal at the maximum depth, or "".
func nestedBlocked(getenv func(string) string) string {
	depth, max := subagentDepth(getenv)
	if depth < max {
		return ""
	}
	return fmt.Sprintf("Nested subagent call blocked (depth=%d, max=%d). You are running at the maximum subagent nesting depth. Complete your current task directly without delegating to further subagents.", depth, max)
}

// resolveRunCwd is the original's resolveRequestedCwd and preflightLaunchCwd: a relative cwd is the session's, and a
// missing one, or a file, is refused before any child starts.
func resolveRunCwd(base, requested string) (string, error) {
	if requested == "" {
		return base, nil
	}
	effective := filepath.Clean(requested)
	if !filepath.IsAbs(effective) {
		effective = filepath.Join(base, requested)
	}
	resolution := ""
	if requested != effective {
		resolution = "\n(resolved from " + marshalJSON(requested, "") + ")"
	}
	st, err := os.Stat(effective)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", fmt.Errorf("Subagent launch aborted: cwd does not exist: %s%s", effective, resolution)
	case err != nil:
		return "", fmt.Errorf("Subagent launch aborted: cwd could not be accessed: %s%s\n%s", effective, resolution, err.Error())
	case !st.IsDir():
		return "", fmt.Errorf("Subagent launch aborted: cwd is not a directory: %s%s", effective, resolution)
	}
	return effective, nil
}

// eitherDone closes when a or b closes (nil when both are nil); stop releases its watcher.
func eitherDone(a, b <-chan struct{}) (<-chan struct{}, func()) {
	switch {
	case a == nil:
		return b, func() {}
	case b == nil:
		return a, func() {}
	}
	out, stop := make(chan struct{}), make(chan struct{})
	go func() {
		select {
		case <-a:
		case <-b:
		case <-stop:
			return
		}
		close(out)
	}()
	var once sync.Once
	return out, func() { once.Do(func() { close(stop) }) }
}

// childSlots holds one token per running child.
var childSlots = make(chan struct{}, maxChildren)

// childExit is a child that ended with a non-zero code.
type childExit struct {
	agent  string
	code   int
	detail string
}

func (e *childExit) Error() string {
	return fmt.Sprintf("%s exited with code %d: %s", e.agent, e.code, e.detail)
}

func runAgent(done <-chan struct{}, cwd string, a *AgentConfig, task, model string) (string, error) {
	select {
	case childSlots <- struct{}{}:
	case <-done:
		return "", fmt.Errorf("%s was cancelled", a.Name)
	}
	defer func() { <-childSlots }()
	depth, _ := subagentDepth(os.Getenv)
	res, err := runChild(done, childRequest{Bin: pigBinary(), Args: buildChildArgs(a, model), Stdin: childPrompt(task), Cwd: cwd, Env: childEnv(os.Environ(), depth, a.Name)})
	switch {
	case errors.Is(err, errCancelled):
		return res.Stdout, fmt.Errorf("%s was cancelled", a.Name)
	case err != nil:
		return "", fmt.Errorf("could not start %s: %w", a.Name, err)
	case res.Exit != 0:
		detail := res.Stderr
		if detail == "" {
			detail = res.Stdout
		}
		return res.Stdout, &childExit{agent: a.Name, code: res.Exit, detail: detail}
	}
	return res.Stdout, nil
}

// chainTask is a step's task: its own template, else {task} for the first step and {previous} for the rest.
func chainTask(steps []ChainStep, i int, request, previous string) string {
	t := steps[i].Task
	if t == "" {
		if i == 0 {
			t = "{task}"
		} else {
			t = "{previous}"
		}
	}
	return strings.NewReplacer("{task}", request, "{previous}", previous).Replace(t)
}

// runChain runs the steps in order, each one's output flowing into the next; it stops at the first failing step. Every
// agent is resolved before any child starts.
func runChain(done <-chan struct{}, cwd string, c *ChainConfig, agents []AgentConfig, request string, progress func(string)) (string, error) {
	resolved := make([]*AgentConfig, len(c.Steps))
	for i, s := range c.Steps {
		a, msg := ResolveAgentName(s.Agent, agents)
		if a == nil {
			if msg == "" {
				msg = fmt.Sprintf("Unknown agent '%s' in step %d of chain '%s'", s.Agent, i+1, c.Name)
			}
			return "", fmt.Errorf("%s", msg)
		}
		resolved[i] = a
	}
	previous := ""
	for i, s := range c.Steps {
		progress(fmt.Sprintf("[%d/%d] %s", i+1, len(c.Steps), s.Agent))
		model := s.Model
		out, err := runAgent(done, cwd, resolved[i], chainTask(c.Steps, i, request, previous), model)
		if err != nil {
			var ce *childExit
			if errors.As(err, &ce) {
				return out, fmt.Errorf("step %d (%s) exited with code %d: %s", i+1, s.Agent, ce.code, ce.detail)
			}
			return out, fmt.Errorf("step %d (%s): %w", i+1, s.Agent, err)
		}
		previous = out
	}
	return previous, nil
}
