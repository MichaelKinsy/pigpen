package pi_subagents

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A child session is the pig-process counterpart of the original's in-process child session (runs/shared/child-session.ts):
// the original creates a Pi AgentSession inside the process that owns it; the port starts `pig --mode rpc` (Pi's RPC protocol,
// one JSON object per line) and drives it through its stdin. The session is what steering, the watchdog and the result
// observers talk to; the process boundary is hidden behind this interface.

// childEvent is one Pi event of the child (`agent_start`, `message_end`, `queue_update`, `agent_settled`, ...).
type childEvent = map[string]any

type childSession interface {
	// Subscribe delivers every event of the child to fn, in order, on the goroutine that reads the child's output: keep fn
	// short. The returned function removes the subscription.
	Subscribe(fn func(childEvent)) (off func())
	// Prompt starts the child's run with text and returns when the run settled (`agent_settled`) or the child ended.
	Prompt(text string) error
	// Steer queues text after the current tool batch; it fails when the child is not running a turn or rejects the command.
	Steer(message string) error
	// FollowUp queues text for after the active turn settles; it fails when the child rejects the command.
	FollowUp(message string) error
	Abort() error
	// HasQueuedMessages reports the steering and follow-up messages a `queue_update` event shows as still queued.
	HasQueuedMessages() bool
	Messages() []map[string]any
	SessionFile() string
	SessionID() string
	// Dispose ends the child: standard input closes (Pi's shutdown), then SIGTERM, then a kill after the grace.
	Dispose() error
}

// childWire is the process side of an rpcChild: the child's pipes.
type rpcResponse struct {
	Success bool
	Error   string
	Data    map[string]any
}

// rpcChild is a child session over a `pig --mode rpc` process.
type rpcChild struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr *tailWriter
	grace  time.Duration

	wmu sync.Mutex // serializes writes to stdin

	mu        sync.Mutex
	nextID    int
	pending   map[string]chan rpcResponse
	subs      map[int]func(childEvent)
	nextSub   int
	queued    int
	streaming bool
	messages  []map[string]any
	settleW   []chan struct{}
	ended     bool
	exitCode  int
	exitErr   error
	state     map[string]any

	exited chan struct{}
}

var errChildEnded = errors.New("the child ended")

// startRpcChild starts the process. Its standard input is the command channel, so the caller sends the prompt itself.
func startRpcChild(req childRequest, grace time.Duration) (*rpcChild, error) {
	cmd := exec.Command(req.Bin, req.Args...)
	cmd.Dir = req.Cwd
	cmd.Env = req.Env
	if cmd.Env == nil {
		cmd.Env = []string{}
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	c := &rpcChild{cmd: cmd, stdin: stdin, stderr: &tailWriter{max: stderrTail}, grace: grace,
		pending: map[string]chan rpcResponse{}, subs: map[int]func(childEvent){}, exited: make(chan struct{})}
	cmd.Stderr = c.stderr
	configureChild(cmd)
	unlock := lockThread()
	err = cmd.Start()
	unlock()
	if err != nil {
		return nil, err
	}
	go c.read(stdout)
	return c, nil
}

// read parses the child's standard output (one JSON object per line) until it closes, then waits for the process.
func (c *rpcChild) read(stdout io.Reader) {
	r := bufio.NewReaderSize(stdout, 64*1024)
	for {
		line, err := r.ReadBytes('\n')
		if s := strings.TrimSpace(string(line)); s != "" {
			var ev map[string]any
			if json.Unmarshal([]byte(s), &ev) == nil {
				c.dispatch(ev)
			}
		}
		if err != nil {
			break
		}
	}
	werr := c.cmd.Wait()
	c.mu.Lock()
	c.ended = true
	if c.cmd.ProcessState != nil {
		c.exitCode = c.cmd.ProcessState.ExitCode()
	}
	var ee *exec.ExitError
	if werr != nil && !errors.As(werr, &ee) {
		c.exitErr = werr
	}
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	waiters := c.settleW
	c.settleW = nil
	c.mu.Unlock()
	for _, w := range waiters {
		close(w)
	}
	close(c.exited)
}

func (c *rpcChild) dispatch(ev map[string]any) {
	typ, _ := ev["type"].(string)
	if typ == "response" {
		id, _ := ev["id"].(string)
		c.mu.Lock()
		ch := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if ch != nil {
			resp := rpcResponse{}
			resp.Success, _ = ev["success"].(bool)
			resp.Error, _ = ev["error"].(string)
			resp.Data, _ = ev["data"].(map[string]any)
			ch <- resp
		}
		return
	}
	var waiters []chan struct{}
	c.mu.Lock()
	switch typ {
	case "agent_start":
		c.streaming = true
	case "queue_update":
		n := 0
		for _, k := range []string{"steering", "followUp"} {
			if l, ok := ev[k].([]any); ok {
				n += len(l)
			}
		}
		c.queued = n
	case "agent_end":
		if ms, ok := ev["messages"].([]any); ok {
			c.messages = c.messages[:0]
			for _, m := range ms {
				if mm, ok := m.(map[string]any); ok {
					c.messages = append(c.messages, mm)
				}
			}
		}
	case "agent_settled":
		c.streaming = false
		waiters = c.settleW
		c.settleW = nil
	}
	subs := make([]func(childEvent), 0, len(c.subs))
	for i := range c.nextSub {
		if fn, ok := c.subs[i]; ok {
			subs = append(subs, fn)
		}
	}
	c.mu.Unlock()
	for _, fn := range subs {
		fn(ev)
	}
	for _, w := range waiters {
		close(w)
	}
}

func (c *rpcChild) Subscribe(fn func(childEvent)) func() {
	c.mu.Lock()
	id := c.nextSub
	c.nextSub++
	c.subs[id] = fn
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		delete(c.subs, id)
		c.mu.Unlock()
	}
}

// command sends one command and waits for its response, or for the child to end.
func (c *rpcChild) command(cmd map[string]any, timeout time.Duration) (rpcResponse, error) {
	c.mu.Lock()
	if c.ended {
		c.mu.Unlock()
		return rpcResponse{}, errChildEnded
	}
	c.nextID++
	id := "pi-subagents-" + strconv.Itoa(c.nextID)
	ch := make(chan rpcResponse, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	cmd["id"] = id
	data, err := json.Marshal(cmd)
	if err == nil {
		c.wmu.Lock()
		_, err = c.stdin.Write(append(data, '\n'))
		c.wmu.Unlock()
	}
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return rpcResponse{}, fmt.Errorf("could not send %v to the child: %w", cmd["type"], err)
	}
	var tc <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		tc = t.C
	}
	select {
	case resp, ok := <-ch:
		if !ok {
			return rpcResponse{}, errChildEnded
		}
		return resp, nil
	case <-tc:
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return rpcResponse{}, fmt.Errorf("the child did not answer %v within %s", cmd["type"], timeout)
	}
}

const rpcCommandTimeout = 30 * time.Second

func (c *rpcChild) simple(typ, message string) error {
	cmd := map[string]any{"type": typ}
	if message != "" {
		cmd["message"] = message
	}
	resp, err := c.command(cmd, rpcCommandTimeout)
	if err != nil {
		return err
	}
	if !resp.Success {
		if resp.Error == "" {
			return fmt.Errorf("the child rejected %s", typ)
		}
		return errors.New(resp.Error)
	}
	return nil
}

func (c *rpcChild) Prompt(text string) error {
	settled := make(chan struct{})
	c.mu.Lock()
	c.settleW = append(c.settleW, settled)
	c.mu.Unlock()
	resp, err := c.command(map[string]any{"type": "prompt", "message": text}, rpcCommandTimeout)
	if err != nil {
		return err
	}
	if !resp.Success {
		if resp.Error == "" {
			return errors.New("the child rejected the prompt")
		}
		return errors.New(resp.Error)
	}
	if d, _ := resp.Data["disposition"].(string); d == "handled" {
		return nil
	}
	<-settled
	return nil
}

func (c *rpcChild) Steer(message string) error {
	c.mu.Lock()
	streaming := c.streaming
	c.mu.Unlock()
	if !streaming {
		return errors.New("the child is not running a turn")
	}
	return c.simple("steer", message)
}

func (c *rpcChild) FollowUp(message string) error { return c.simple("follow_up", message) }

func (c *rpcChild) Abort() error { return c.simple("abort", "") }

func (c *rpcChild) HasQueuedMessages() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.queued > 0
}

func (c *rpcChild) Messages() []map[string]any {
	resp, err := c.command(map[string]any{"type": "get_messages"}, 5*time.Second)
	if err == nil && resp.Success {
		if ms, ok := resp.Data["messages"].([]any); ok {
			out := make([]map[string]any, 0, len(ms))
			for _, m := range ms {
				if mm, ok := m.(map[string]any); ok {
					out = append(out, mm)
				}
			}
			return out
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]map[string]any(nil), c.messages...)
}

func (c *rpcChild) sessionState() map[string]any {
	c.mu.Lock()
	if c.state != nil {
		defer c.mu.Unlock()
		return c.state
	}
	c.mu.Unlock()
	resp, err := c.command(map[string]any{"type": "get_state"}, 5*time.Second)
	if err != nil || !resp.Success {
		return nil
	}
	c.mu.Lock()
	c.state = resp.Data
	c.mu.Unlock()
	return resp.Data
}

func (c *rpcChild) SessionFile() string { s, _ := c.sessionState()["sessionFile"].(string); return s }
func (c *rpcChild) SessionID() string   { s, _ := c.sessionState()["sessionId"].(string); return s }

// Stderr is the end of the child's standard error, for a failure message.
func (c *rpcChild) Stderr() string { return strings.TrimSpace(c.stderr.String()) }

// Exit is the child's exit code and start error once it ended.
func (c *rpcChild) Exit() (int, error) {
	<-c.exited
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.exitCode, c.exitErr
}

func (c *rpcChild) Dispose() error {
	c.mu.Lock()
	ended := c.ended
	c.mu.Unlock()
	if ended {
		return nil
	}
	c.wmu.Lock()
	_ = c.stdin.Close() // Pi's shutdown: end of input
	c.wmu.Unlock()
	select {
	case <-c.exited:
		return nil
	case <-time.After(c.grace):
	}
	terminate(c.cmd.Process)
	select {
	case <-c.exited:
		return nil
	case <-time.After(c.grace):
	}
	_ = c.cmd.Process.Kill()
	<-c.exited
	return nil
}

// childFinalText is what print mode prints: the text of the last assistant message of the run.
func childFinalText(messages []map[string]any) string {
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if role, _ := m["role"].(string); role != "assistant" {
			continue
		}
		return rpcAssistantText(m)
	}
	return ""
}

func rpcAssistantText(m map[string]any) string {
	var sb strings.Builder
	switch c := m["content"].(type) {
	case string:
		return c
	case []any:
		for _, p := range c {
			pm, _ := p.(map[string]any)
			if t, _ := pm["type"].(string); t == "text" {
				s, _ := pm["text"].(string)
				sb.WriteString(s)
			}
		}
	}
	return sb.String()
}

// childFailure is the error a run ended with ("error" or "aborted" stop reason of its last assistant message), or "".
func childFailure(messages []map[string]any) (stop, detail string) {
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if role, _ := m["role"].(string); role != "assistant" {
			continue
		}
		stop, _ = m["stopReason"].(string)
		if stop == "error" || stop == "aborted" {
			detail, _ = m["errorMessage"].(string)
			return stop, detail
		}
		return "", ""
	}
	return "", ""
}
