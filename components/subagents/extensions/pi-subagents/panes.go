package pi_subagents

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

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
