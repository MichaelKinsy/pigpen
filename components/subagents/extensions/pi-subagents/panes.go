package pi_subagents

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const paneTaskMax = 100 << 10

var panePoll = 500 * time.Millisecond

// paneHost runs children as interactive terminals in the parent's Herdr Desktop tab.
type paneHost struct {
	socket, parent, sessionDir string
	notify                     func(msg, level string)
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func uuidV4() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// run runs req in a pane. Before terminal.run, any failure falls back to the hidden runner for this one call; after it,
// a failure is the child's error and the child never runs twice.
func (p *paneHost) run(done <-chan struct{}, req childRequest, agent string) (childResult, error) {
	select {
	case <-done:
		return childResult{}, errCancelled
	default:
	}
	fallback := func(why string) (childResult, error) {
		p.notify(why, "warning")
		return runChild(done, req)
	}
	if len(req.Stdin) > paneTaskMax {
		return fallback(agent + "'s task is over 100 KiB; it runs without a pane.")
	}
	unreachable := func(err error) (childResult, error) {
		return fallback(fmt.Sprintf("Herdr Desktop is not reachable (%v); %s runs without a pane.", err, agent))
	}
	h, err := dialHC(p.socket)
	if err != nil {
		return unreachable(err)
	}
	defer h.Close()
	var parent hcTerminal
	if err := h.call("hello", map[string]any{"generation": 1}, nil); err != nil {
		return unreachable(err)
	}
	if err := h.call("terminal.get", map[string]any{"terminal": p.parent}, &parent); err != nil {
		return unreachable(err)
	}

	dir := p.sessionDir
	if dir == "" {
		if dir, err = os.MkdirTemp("", "pig-subagent-"); err != nil {
			return childResult{}, err
		}
		defer os.RemoveAll(dir)
	}
	labels := map[string]string{}
	if s, b := parent.Labels["herdr.space"], parent.Labels["herdr.tab"]; s != "" && b != "" {
		labels["herdr.space"], labels["herdr.tab"] = s, b
	}
	id := uuidV4()
	var started hcTerminal
	err = h.call("terminal.run", map[string]any{
		"argv": append([]string{req.Bin}, paneArgs(req.Args, dir, id, req.Stdin)...), "cwd": req.Cwd, "env": paneEnv(req.Env),
		"labels": labels, "name": paneName(agent, randHex(2)), "parent": p.parent, "no_notify": true,
	}, &started)
	if err != nil {
		return childResult{}, fmt.Errorf("herdr-core: %w", err) // the child never started
	}
	addr := started.Address
	live := true // unknown after a lost connection: cleanup then tries the kill anyway
	defer func() {
		c, err := dialHC(p.socket) // never reuse a connection after an error: a late reply would desync it
		if err != nil {
			return
		}
		defer c.Close()
		if live {
			_ = c.call("terminal.kill", map[string]any{"terminal": addr}, nil)
		}
		_ = c.call("terminal.rm", map[string]any{"terminal": addr}, nil)
	}()

	var t hcTerminal
	blocked := false
	for {
		select {
		case <-done:
			return childResult{}, errCancelled
		default:
		}
		if err := h.call("terminal.get", map[string]any{"terminal": addr}, &t); err != nil {
			return childResult{Exit: 1, Stderr: fmt.Sprintf("herdr-core stopped answering while %s ran in %s: %v", agent, addr, err)}, nil
		}
		st := ""
		if t.Status != nil {
			st = t.Status.State
		}
		live = t.State == "live"
		if t.State == "finished" || t.Turn >= 1 && (st == "done" || st == "idle" || st == "error") {
			break
		}
		if st == "blocked" && !blocked {
			p.notify(fmt.Sprintf("%s needs you in its Herdr pane (%s).", agent, addr), "warning")
		}
		blocked = st == "blocked"
		select {
		case <-done:
			return childResult{}, errCancelled
		case <-time.After(panePoll):
		}
	}

	res, ok := sessionAnswer(dir, id)
	if !ok {
		if t.State == "finished" {
			res = childResult{Exit: -1}
			if t.ExitCode != nil {
				res.Exit = *t.ExitCode
			}
			var screen struct{ Text string }
			if err := h.call("terminal.read", map[string]any{"terminal": addr, "lines": 40}, &screen); err == nil {
				res.Stderr = screen.Text
			}
		} else {
			res = childResult{Exit: 1, Stderr: fmt.Sprintf("no answer in %s's session file in %s", agent, dir)}
		}
	}
	out, errw := &headWriter{max: outputMaxBytes}, &tailWriter{max: stderrTail}
	out.Write([]byte(res.Stdout))
	errw.Write([]byte(res.Stderr))
	res.Stdout = out.text() // same trimming and cap as the hidden runner
	res.Stderr = strings.TrimSpace(errw.String())
	return res, nil
}

// panesEnabled reports whether subagents run in Herdr Desktop panes: the setting is on, the Herdr terminal environment
// is present, and the platform is not Windows.
func panesEnabled(s subagentSettings, getenv func(string) string, goos string) bool {
	return s.herdrPanes != nil && *s.herdrPanes && goos != "windows" &&
		getenv("HC_TERMINAL_SOCKET") != "" && getenv("HC_TERMINAL_ID") != ""
}

// paneArgs turns a print-mode child's argv into an interactive one: the leading --print goes, the session is pinned so
// its transcript can be found, and the prompt follows "--".
func paneArgs(args []string, sessionDir, sessionID, prompt string) []string {
	if len(args) > 0 && args[0] == "--print" {
		args = args[1:]
	}
	out := append([]string{}, args...)
	return append(out, "--session-dir", sessionDir, "--session-id", sessionID, "--", prompt)
}

// paneEnv maps the child's environment, dropping the parent terminal's HC_ variables.
func paneEnv(env []string) map[string]string {
	m := map[string]string{}
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if ok && !strings.HasPrefix(k, "HC_") {
			m[k] = v
		}
	}
	return m
}

// paneName is a terminal name of at most 32 characters ending in "-"+suffix.
func paneName(agent, suffix string) string {
	name := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' {
			return r
		}
		return '-'
	}, strings.ToLower(agent))
	if name == "" {
		name = "agent"
	}
	if max := 32 - 1 - len(suffix); len(name) > max {
		name = name[:max]
	}
	return name + "-" + suffix
}

// sessionAnswer reads the child's final answer from its transcript, mirroring PiG print mode: the last assistant
// message decides. It reports false when there is no transcript or no assistant message.
func sessionAnswer(dir, id string) (childResult, bool) {
	files, _ := filepath.Glob(filepath.Join(dir, "*_"+id+".jsonl"))
	if len(files) == 0 {
		return childResult{}, false
	}
	f, err := os.Open(files[len(files)-1])
	if err != nil {
		return childResult{}, false
	}
	defer f.Close()
	type message struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason   string `json:"stopReason"`
		ErrorMessage string `json:"errorMessage"`
	}
	var last *message
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		var l struct {
			Type    string  `json:"type"`
			Message message `json:"message"`
		}
		if json.Unmarshal(line, &l) == nil && l.Type == "message" && l.Message.Role == "assistant" {
			m := l.Message
			last = &m
		}
		if err != nil {
			break
		}
	}
	if last == nil {
		return childResult{}, false
	}
	if last.StopReason == "error" || last.StopReason == "aborted" {
		msg := last.ErrorMessage
		if msg == "" {
			msg = "Request " + last.StopReason
		}
		return childResult{Exit: 1, Stderr: msg}, true
	}
	var res childResult
	for _, c := range last.Content {
		if c.Type == "text" {
			res.Stdout += c.Text + "\n"
		}
	}
	if last.StopReason == "length" {
		res.Exit = 1
		res.Stderr = "Response was truncated: the model reached its output token limit."
	}
	return res, true
}
