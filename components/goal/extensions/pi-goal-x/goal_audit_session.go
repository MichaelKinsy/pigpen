package pi_goal_x

// The real auditorSessionFactory. The original runs the auditor and the Oracle on an in-process nested AgentSession
// (createAgentSession of the SDK); an extension process has no such session, so the port runs the host's own pig as a child
// process in JSON mode (`pig --mode json`: one prompt, the session's events as JSON Lines) and maps its event stream to the
// auditorSessionEvent values the auditor and the Oracle consume. Like pi-subagents' runner (components/subagents) the child gets
// no extensions of this host, a scrubbed environment, its own process group, SIGTERM on abort and a kill after a grace period.
//
// What the SDK's session options map to:
//   model/thinkingLevel   --provider/--model, --thinking
//   tools                 --tools (built-in names only), --no-mcp (an MCP tool would escape the allowlist)
//   in-memory session     --no-session
//   isolated loader       --system-prompt, --no-extensions --no-skills --no-prompt-templates --no-themes --no-context-files
//                         --no-approve; resourceLoaderOptions (auditorProjectResources) maps each noX=false to no flag
//   customTools           not expressible: pig cannot register an in-process tool in a child. They are emulated by a
//                         protocol appended to the prompt: the model ends its reply with <tool name>{JSON}</tool name> and the
//                         block is run through the tool's execute (see pigCustomToolSuffix).
//   compactionEnabled     NOT mapped: pig has no flag or environment variable that turns compaction off without persisting
//                         it in the user's settings.
//   modelRegistry/Runtime not mapped: the child resolves models and credentials from the same agent directory.

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// pigAuditKillGrace is how long a stopped child has to end after SIGTERM before its process group is killed.
	pigAuditKillGrace = 5 * time.Second
	// pigAuditStderrTail is how much of the child's stderr is kept to explain a failure.
	pigAuditStderrTail = 8 * 1024
	// pigAuditMaxLine bounds one event line; a longer line is dropped whole.
	pigAuditMaxLine = 16 << 20
	// pigAuditWaitDelay bounds the wait for a pipe that a grandchild keeps open after pig ended.
	pigAuditWaitDelay = 2 * time.Second
)

// pigAuditorFactory starts one child pig per session.
type pigAuditorFactory struct {
	// bin is the pig to run; nil resolves it with pigAuditBinary.
	bin func() string
	// environ is the parent environment; nil is os.Environ.
	environ func() []string
	// grace overrides pigAuditKillGrace when positive.
	grace time.Duration
}

func newPigAuditorSessionFactory() *pigAuditorFactory { return &pigAuditorFactory{} }

func (f *pigAuditorFactory) graceOrDefault() time.Duration {
	if f.grace > 0 {
		return f.grace
	}
	return pigAuditKillGrace
}

// pigAuditBinary finds the pig to start: the pig that runs this extension (PIG_HARNESS_BINARY, set by the host for an
// extension process), the pig next to this program, then PATH.
func pigAuditBinary() string {
	if b := strings.TrimSpace(os.Getenv("PIG_HARNESS_BINARY")); b != "" {
		return b
	}
	if self, err := os.Executable(); err == nil {
		if c := filepath.Join(filepath.Dir(self), "pig"); pigAuditFileExists(c) {
			return c
		}
	}
	if p, err := exec.LookPath("pig"); err == nil {
		return p
	}
	return "pig"
}

func pigAuditFileExists(p string) bool { st, err := os.Stat(p); return err == nil && !st.IsDir() }

// buildPigAuditorArgs is the child's command line for the session options (the prompt travels on stdin).
func buildPigAuditorArgs(o auditorSessionOptions) []string {
	args := []string{"--mode", "json", "--no-session", "--no-mcp"}
	if o.model != nil {
		args = append(args, "--provider", o.model.provider, "--model", o.model.id)
	}
	if o.thinkingLevel != "" {
		args = append(args, "--thinking", o.thinkingLevel)
	}
	custom := map[string]bool{}
	for _, t := range o.customTools {
		custom[t.name] = true
	}
	var builtin []string
	for _, name := range o.tools {
		if !custom[name] {
			builtin = append(builtin, name)
		}
	}
	if len(builtin) > 0 {
		args = append(args, "--tools", strings.Join(builtin, ","))
	} else {
		args = append(args, "--no-tools")
	}
	if o.resourceLoader != nil {
		args = append(args, "--system-prompt", o.resourceLoader.getSystemPrompt(),
			"--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes", "--no-context-files", "--no-approve")
	} else if l := o.resourceLoaderOptions; l != nil {
		for _, f := range []struct {
			off  bool
			flag string
		}{{l.noExtensions, "--no-extensions"}, {l.noSkills, "--no-skills"}, {l.noPromptTemplates, "--no-prompt-templates"},
			{l.noThemes, "--no-themes"}, {l.noContextFiles, "--no-context-files"}} {
			if f.off {
				args = append(args, f.flag)
			}
		}
	}
	return args
}

// pigAuditorEnv is the parent environment without the host's internals for this extension (its socket, packed cell and
// harness arguments are not the child's) and with who the child is: a delegated worker, so that a goal extension loaded
// into it (auditorProjectResources) stays out of the way.
func pigAuditorEnv(base []string) []string {
	out := make([]string, 0, len(base)+4)
	depth := 0
	for _, kv := range base {
		k, v, _ := strings.Cut(kv, "=")
		switch {
		case strings.HasPrefix(k, "PIG_EXT_"), strings.HasPrefix(k, "PIG_HARNESS_"), k == "PIG_SUBAGENT", k == "PIG_AGENT_ROLE",
			k == "PI_SUBAGENT_CHILD":
			continue
		case k == "PIG_SUBAGENT_DEPTH":
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
				depth = n
			}
			continue
		}
		out = append(out, kv)
	}
	return append(out, "PI_SUBAGENT_CHILD=1", "PIG_SUBAGENT=1", "PIG_SUBAGENT_DEPTH="+strconv.Itoa(depth+1), "PI_SKIP_VERSION_CHECK=1")
}

func (f *pigAuditorFactory) createSession(opts auditorSessionOptions) (auditorSession, error) {
	bin := ""
	if f.bin != nil {
		bin = f.bin()
	} else {
		bin = pigAuditBinary()
	}
	environ := os.Environ
	if f.environ != nil {
		environ = f.environ
	}
	s := &pigAuditorSession{
		bin: bin, args: buildPigAuditorArgs(opts), env: pigAuditorEnv(environ()), cwd: opts.cwd,
		grace: f.graceOrDefault(), customTools: opts.customTools,
		exited: make(chan struct{}),
	}
	return s, nil
}

// pigCustomToolSuffix is the prompt protocol that stands in for in-process custom tools.
func pigCustomToolSuffix(tools []*goalOracleTool) string {
	var b strings.Builder
	for _, t := range tools {
		b.WriteString("\n\nThe function `" + t.name + "` is not callable in this environment (" + t.description + "). To use it, end your final reply with exactly one block and nothing after it:\n")
		b.WriteString("<" + t.name + ">\n{JSON arguments}\n</" + t.name + ">\n")
		if t.parameters != nil {
			b.WriteString("The JSON arguments must match this JSON Schema:\n" + marshalJSON(t.parameters, "") + "\n")
		}
	}
	return b.String()
}

// pigRunCustomToolBlocks runs the last <name>{json}</name> block of an assistant reply through each custom tool.
func pigRunCustomToolBlocks(tools []*goalOracleTool, text string) {
	for _, t := range tools {
		open, close := "<"+t.name+">", "</"+t.name+">"
		end := strings.LastIndex(text, close)
		if end < 0 {
			continue
		}
		start := strings.LastIndex(text[:end], open)
		if start < 0 {
			continue
		}
		v, err := parseJSON([]byte(strings.TrimSpace(text[start+len(open) : end])))
		if err != nil {
			continue
		}
		if params, ok := v.(*jsObject); ok {
			t.execute("", params)
		}
	}
}

// pigAuditorSession is one child pig behind the auditorSession interface.
type pigAuditorSession struct {
	bin         string
	args        []string
	env         []string
	cwd         string
	grace       time.Duration
	customTools []*goalOracleTool

	mu        sync.Mutex
	listeners []pigListener
	nextID    int
	cmd       *exec.Cmd
	started   bool
	aborted   bool
	disposed  bool
	exited    chan struct{} // closed once the child has been waited for
	stopOnce  sync.Once
}

func (s *pigAuditorSession) subscribe(listener func(auditorSessionEvent)) func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextID
	s.nextID++
	s.listeners = append(s.listeners, pigListener{id, listener})
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for i, l := range s.listeners {
			if l.id == id {
				s.listeners = append(s.listeners[:i:i], s.listeners[i+1:]...)
				return
			}
		}
	}
}

type pigListener struct {
	id int
	fn func(auditorSessionEvent)
}

func (s *pigAuditorSession) emit(ev auditorSessionEvent) {
	s.mu.Lock()
	ls := s.listeners
	s.mu.Unlock()
	for _, l := range ls {
		l.fn(ev)
	}
}

// prompt runs the child to its end. An aborted session returns nil, as session.abort() does not throw in the original.
func (s *pigAuditorSession) prompt(text string) error {
	unlock := lockAuditThread()
	defer unlock()
	s.mu.Lock()
	if s.aborted || s.disposed {
		s.mu.Unlock()
		return nil
	}
	if s.started {
		s.mu.Unlock()
		return errors.New("auditor session already prompted")
	}
	s.started = true
	cmd := exec.Command(s.bin, s.args...)
	cmd.Dir = s.cwd
	cmd.Env = s.env
	if len(s.customTools) > 0 {
		text += pigCustomToolSuffix(s.customTools)
	}
	cmd.Stdin = strings.NewReader(text)
	mapper := &pigEventMapper{}
	out := &pigLineWriter{onLine: func(line []byte) {
		ev, ok, assistantText := mapper.mapLine(line)
		if !ok {
			return
		}
		s.emit(ev)
		if assistantText != "" && len(s.customTools) > 0 {
			pigRunCustomToolBlocks(s.customTools, assistantText)
		}
	}}
	stderr := &pigTailWriter{max: pigAuditStderrTail}
	cmd.Stdout, cmd.Stderr = out, stderr
	cmd.WaitDelay = pigAuditWaitDelay
	configureAuditChild(cmd)
	if err := cmd.Start(); err != nil {
		close(s.exited)
		s.mu.Unlock()
		return fmt.Errorf("could not start pig: %w", err)
	}
	s.cmd = cmd
	s.mu.Unlock()

	err := cmd.Wait()
	out.flush()
	close(s.exited)

	s.mu.Lock()
	aborted := s.aborted || s.disposed
	s.mu.Unlock()
	if aborted {
		killAuditChildGroup(cmd.Process) // whatever the stopped pig left behind
		return nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return fmt.Errorf("pig exited with code %d: %s", ee.ExitCode(), detail)
		}
		return fmt.Errorf("pig exited with code %d", ee.ExitCode())
	}
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		return err
	}
	return nil
}

// stop asks the child to end and kills its process group if it has not after the grace period.
func (s *pigAuditorSession) stop(cmd *exec.Cmd) {
	s.stopOnce.Do(func() {
		terminateAuditChild(cmd.Process)
		go func() {
			select {
			case <-s.exited:
			case <-time.After(s.grace):
				killAuditChildGroup(cmd.Process)
			}
		}()
	})
}

func (s *pigAuditorSession) abort() {
	s.mu.Lock()
	s.aborted = true
	cmd := s.cmd
	s.mu.Unlock()
	if cmd != nil {
		s.stop(cmd)
	}
}

// dispose releases the session: a child still running is stopped and waited for.
func (s *pigAuditorSession) dispose() {
	s.mu.Lock()
	s.disposed = true
	cmd := s.cmd
	started := s.started
	s.mu.Unlock()
	if cmd == nil || !started {
		return
	}
	s.stop(cmd)
	<-s.exited
}

// pigLineWriter splits a byte stream into lines and hands each complete line to onLine on the writer's goroutine.
type pigLineWriter struct {
	buf     []byte
	skip    bool // inside a line longer than pigAuditMaxLine: drop up to its newline
	onLine  func([]byte)
	maxLine int
}

func (w *pigLineWriter) limit() int {
	if w.maxLine > 0 {
		return w.maxLine
	}
	return pigAuditMaxLine
}

func (w *pigLineWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			if !w.skip {
				w.buf = append(w.buf, p...)
				if len(w.buf) > w.limit() {
					w.buf, w.skip = nil, true
				}
			}
			break
		}
		if !w.skip {
			w.buf = append(w.buf, p[:i]...)
			if len(w.buf) <= w.limit() {
				w.line()
			}
		}
		w.buf, w.skip = w.buf[:0], false
		p = p[i+1:]
	}
	return n, nil
}

func (w *pigLineWriter) line() {
	if l := bytes.TrimSuffix(w.buf, []byte("\r")); len(l) > 0 {
		w.onLine(l)
	}
}

// flush handles a last line without its newline.
func (w *pigLineWriter) flush() {
	if !w.skip && len(w.buf) > 0 {
		w.line()
	}
	w.buf, w.skip = nil, false
}

// pigTailWriter keeps the last max bytes.
type pigTailWriter struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (w *pigTailWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	if len(w.buf) > w.max {
		w.buf = append([]byte(nil), w.buf[len(w.buf)-w.max:]...)
	}
	return len(p), nil
}

func (w *pigTailWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.ToValidUTF8(string(w.buf), "")
}

// pigEventMapper turns pig's JSON-mode records into the events the auditor reads. JSON mode's message_update carries only the
// new piece (no cumulative partial message), so the mapper rebuilds the partial assistant text per content index.
type pigEventMapper struct {
	parts []pigPart
}

type pigPart struct {
	isText bool
	text   strings.Builder
}

const pigMaxContentIndex = 1024

func (m *pigEventMapper) partial() *auditorMessage {
	msg := &auditorMessage{role: "assistant"}
	for i := range m.parts {
		if !m.parts[i].isText {
			msg.content = append(msg.content, auditorContentPart{typ: "other"})
			continue
		}
		t := m.parts[i].text.String()
		msg.content = append(msg.content, auditorContentPart{typ: "text", text: &t})
	}
	return msg
}

func (m *pigEventMapper) at(idx int) *pigPart {
	if idx < 0 || idx >= pigMaxContentIndex {
		return nil
	}
	for len(m.parts) <= idx {
		m.parts = append(m.parts, pigPart{})
	}
	return &m.parts[idx]
}

// mapLine maps one record. ok is false for a record the auditor does not read (and for a malformed line). assistantText is
// the joined text of a finished assistant message.
func (m *pigEventMapper) mapLine(line []byte) (ev auditorSessionEvent, ok bool, assistantText string) {
	v, err := parseJSON(line)
	obj, isObj := v.(*jsObject)
	if err != nil || !isObj {
		return ev, false, ""
	}
	typ, _ := obj.str("type")
	switch typ {
	case "agent_start":
		return auditorSessionEvent{typ: typ}, true, ""
	case "tool_execution_start":
		name, _ := obj.str("toolName")
		args, _ := obj.get("args")
		return auditorSessionEvent{typ: typ, toolName: name, args: args}, true, ""
	case "tool_execution_end":
		name, _ := obj.str("toolName")
		return auditorSessionEvent{typ: typ, toolName: name}, true, ""
	case "message_start":
		if msg := obj.obj("message"); msg != nil {
			if role, _ := msg.str("role"); role == "assistant" {
				m.parts = m.parts[:0]
			}
		}
		return ev, false, ""
	case "message_update":
		ame := obj.obj("assistantMessageEvent")
		if ame == nil {
			return ev, false, ""
		}
		st, _ := ame.str("type")
		idx := 0
		if f, has := ame.num("contentIndex"); has {
			idx = int(f)
		}
		if p := m.at(idx); p != nil {
			switch st {
			case "text_start":
				p.isText = true
				p.text.Reset()
			case "text_delta":
				p.isText = true
				d, _ := ame.str("delta")
				p.text.WriteString(d)
			case "text_end":
				p.isText = true
				if c, has := ame.str("content"); has {
					p.text.Reset()
					p.text.WriteString(c)
				}
			}
		}
		return auditorSessionEvent{typ: typ, streamType: st, message: m.partial()}, true, ""
	case "message_end":
		msg := obj.obj("message")
		if msg == nil {
			return ev, false, ""
		}
		role, _ := msg.str("role")
		out := &auditorMessage{role: role}
		var texts []string
		if s, isStr := msg.str("content"); isStr {
			out.content = append(out.content, auditorContentPart{typ: "text", text: &s})
		} else if arr, isArr := func() ([]any, bool) { x, _ := msg.get("content"); a, ok := x.([]any); return a, ok }(); isArr {
			for _, e := range arr {
				c, _ := e.(*jsObject)
				if c == nil {
					continue
				}
				ct, _ := c.str("type")
				part := auditorContentPart{typ: ct}
				if t, has := c.str("text"); has {
					part.text = &t
					if ct == "text" {
						texts = append(texts, t)
					}
				}
				out.content = append(out.content, part)
			}
		}
		if role == "assistant" {
			m.parts = m.parts[:0]
			assistantText = strings.Join(texts, "\n")
		}
		return auditorSessionEvent{typ: typ, message: out}, true, assistantText
	}
	return ev, false, ""
}
