package a2aext

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Turn is one task's run of PiG.
type Turn struct {
	Principal Principal
	ContextID string
	TaskID    string
	Prompt    string
}

// Update is incremental output from a running turn.
type Update struct {
	Text string // assistant text delta
	Tool string // name of a tool that started
}

// Result is a finished turn. Text is exactly the concatenation of the streamed text updates.
type Result struct {
	Text string
	// Failure is a short, peer-safe reason the turn failed. Provider error text is never copied into it.
	Failure string
}

// Worker runs turns.
type Worker interface {
	Run(ctx context.Context, turn Turn, onUpdate func(Update)) (Result, error)
}

// ProcessWorker runs each turn in a `pig --mode rpc` child process. A context is one PiG
// session file, so a later task in the same context continues the conversation.
type ProcessWorker struct {
	cfg      WorkerConfig
	stateDir string
	getenv   func(string) string
}

const maxFrameBytes = 16 << 20

// baseEnv is what a worker inherits. Provider keys and everything else must be named in passEnv.
var baseEnv = []string{
	"PATH", "HOME", "USER", "LANG", "LC_ALL", "TMPDIR", "TERM",
	"PIG_HOME", "PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR",
	"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy",
	"SSL_CERT_FILE", "SSL_CERT_DIR", "SystemRoot", "USERPROFILE", "APPDATA", "LOCALAPPDATA",
}

// NewProcessWorker builds a ProcessWorker whose per-principal sessions live under stateDir.
func NewProcessWorker(cfg WorkerConfig, stateDir string, getenv func(string) string) (*ProcessWorker, error) {
	if stateDir == "" {
		return nil, errors.New("a2a: worker needs a state directory")
	}
	if getenv == nil {
		getenv = os.Getenv
	}
	if cfg.Command == "" {
		cfg.Command = getenv("PIG_A2A_PIG")
	}
	if cfg.Command == "" {
		cfg.Command = "pig"
	}
	for _, name := range cfg.PassEnv {
		if !envRE.MatchString(name) {
			return nil, fmt.Errorf("a2a: worker passEnv %q is not an environment variable name", name)
		}
	}
	if cfg.GraceSeconds <= 0 {
		cfg.GraceSeconds = defaultGraceSeconds
	}
	return &ProcessWorker{cfg: cfg, stateDir: stateDir, getenv: getenv}, nil
}

func keyDigest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// SessionID derives the PiG session id for a principal's context. It is hex, so a hostile
// context id cannot reach a file name, and it depends on the principal's tenant boundary,
// so two tenants that pick the same context id never share a session.
func SessionID(p Principal, contextID string) string {
	return keyDigest("a2a-session\x00" + p.Key() + "\x00" + contextID)[:32]
}

func (w *ProcessWorker) sessionDir(p Principal) string {
	return filepath.Join(w.stateDir, "sessions", keyDigest("a2a-dir\x00" + p.Key())[:16])
}

func (w *ProcessWorker) cwd(p Principal) string {
	if w.cfg.Cwd != "" {
		return w.cfg.Cwd
	}
	// Without an explicit workspace the worker sees an empty private directory, not the operator's files.
	return filepath.Join(w.stateDir, "workspaces", keyDigest("a2a-dir\x00" + p.Key())[:16])
}

func (w *ProcessWorker) args(t Turn) []string {
	args := []string{"--mode", "rpc", "--offline", "--no-extensions", "--no-skills", "--no-context-files", "--no-prompt-templates", "--no-themes"}
	if len(w.cfg.Tools) > 0 {
		args = append(args, "--tools", strings.Join(w.cfg.Tools, ","))
	} else {
		// PiG's file tools are not confined to the working directory, so a worker gets no tools unless named.
		args = append(args, "--no-tools")
	}
	if w.cfg.Provider != "" {
		args = append(args, "--provider", w.cfg.Provider)
	}
	if w.cfg.Model != "" {
		args = append(args, "--model", w.cfg.Model)
	}
	args = append(args, "--session-dir", w.sessionDir(t.Principal), "--session-id", SessionID(t.Principal, t.ContextID))
	return append(args, w.cfg.Args...)
}

func (w *ProcessWorker) env() []string {
	var env []string
	seen := map[string]bool{}
	for _, name := range append(append([]string{}, baseEnv...), w.cfg.PassEnv...) {
		if v := w.getenv(name); v != "" && !seen[name] {
			seen[name] = true
			env = append(env, name+"="+v)
		}
	}
	return append(env, "PIG_A2A_WORKER=1", "PI_SKIP_VERSION_CHECK=1")
}

// wireEvent is the part of PiG's RPC JSONL the worker consumes.
type wireEvent struct {
	Type                  string `json:"type"`
	ID                    string `json:"id"`
	Command               string `json:"command"`
	Success               bool   `json:"success"`
	ToolName              string `json:"toolName"`
	AssistantMessageEvent *struct {
		Type  string `json:"type"`
		Delta string `json:"delta"`
	} `json:"assistantMessageEvent"`
	Message *struct {
		Role       string          `json:"role"`
		StopReason string          `json:"stopReason"`
		Content    json.RawMessage `json:"content"`
	} `json:"message"`
}

type frame struct {
	event wireEvent
	err   error
}

func readFrames(r io.Reader, out chan<- frame) {
	defer close(out)
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		var line []byte
		for {
			chunk, err := br.ReadSlice('\n')
			line = append(line, chunk...)
			if len(line) > maxFrameBytes {
				out <- frame{err: fmt.Errorf("PiG frame exceeds %d bytes", maxFrameBytes)}
				return
			}
			if err == bufio.ErrBufferFull {
				continue
			}
			if err != nil {
				if len(bytes.TrimSpace(line)) > 0 {
					out <- frame{err: fmt.Errorf("PiG stream closed inside a record: %w", err)}
				}
				return
			}
			break
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var ev wireEvent
		if err := json.Unmarshal(line, &ev); err != nil || ev.Type == "" {
			out <- frame{err: errors.New("PiG sent a record that is not an event")}
			return
		}
		out <- frame{event: ev}
	}
}

type tailBuffer struct{ b []byte }

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > 4096 {
		t.b = t.b[len(t.b)-4096:]
	}
	return len(p), nil
}

// Run implements Worker.
func (w *ProcessWorker) Run(ctx context.Context, t Turn, onUpdate func(Update)) (Result, error) {
	if strings.TrimSpace(t.Prompt) == "" {
		return Result{}, errors.New("a2a: empty prompt")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	for _, dir := range []string{w.sessionDir(t.Principal), w.cwd(t.Principal)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return Result{}, fmt.Errorf("a2a: prepare worker directory: %w", err)
		}
	}
	cmd := exec.Command(w.cfg.Command, w.args(t)...)
	cmd.Dir, cmd.Env = w.cwd(t.Principal), w.env()
	configureProcess(cmd)
	stderr := &tailBuffer{}
	cmd.Stderr = stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return Result{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, err
	}
	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("a2a: start worker %q: %w", w.cfg.Command, err)
	}
	frames := make(chan frame, 64)
	go readFrames(stdout, frames)
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	grace := time.Duration(w.cfg.GraceSeconds) * time.Second

	write := func(v map[string]any) error {
		b, _ := json.Marshal(v)
		_, err := stdin.Write(append(b, '\n'))
		return err
	}
	var result Result
	var runErr error
	defer func() {
		_ = stdin.Close()
		select {
		case <-exited:
		case <-time.After(grace):
			_ = terminateTree(cmd.Process)
			select {
			case <-exited:
			case <-time.After(grace):
				_ = killTree(cmd.Process)
				<-exited
			}
		}
		// A tool the worker started can outlive it.
		_ = killTree(cmd.Process)
		go func() {
			for range frames { // let the reader finish
			}
		}()
	}()

	if err := write(map[string]any{"id": "prompt", "type": "prompt", "message": t.Prompt}); err != nil {
		return Result{}, fmt.Errorf("a2a: write prompt: %w", err)
	}

	var (
		text       strings.Builder
		msgText    string // streamed text of the current assistant message
		msgHasText bool
		lastStop   string
		accepted   bool
		aborting   bool
		abortTimer <-chan time.Time
		ctxDone    = ctx.Done()
	)
	emit := func(s string) {
		if s == "" {
			return
		}
		if !msgHasText && text.Len() > 0 {
			text.WriteString("\n\n")
			onUpdate(Update{Text: "\n\n"})
		}
		msgHasText = true
		msgText += s
		text.WriteString(s)
		onUpdate(Update{Text: s})
	}
	for {
		select {
		case <-ctxDone:
			ctxDone = nil
			aborting = true
			_ = write(map[string]any{"id": "abort", "type": "abort"})
			abortTimer = time.After(grace)
		case <-abortTimer:
			return Result{}, ctx.Err()
		case f, ok := <-frames:
			if !ok {
				if aborting {
					return Result{}, ctx.Err()
				}
				return Result{}, fmt.Errorf("a2a: worker exited before the turn settled (%s)", strings.TrimSpace(string(stderr.b)))
			}
			if f.err != nil {
				return Result{}, fmt.Errorf("a2a: worker protocol: %w", f.err)
			}
			ev := f.event
			switch ev.Type {
			case "response":
				if ev.ID == "prompt" {
					if !ev.Success {
						return Result{Failure: "PiG rejected the prompt"}, nil
					}
					accepted = true
				}
			case "message_start":
				if ev.Message != nil && ev.Message.Role == "assistant" {
					msgText, msgHasText, lastStop = "", false, ""
				}
			case "message_update":
				if !aborting && ev.AssistantMessageEvent != nil && ev.AssistantMessageEvent.Type == "text_delta" {
					emit(ev.AssistantMessageEvent.Delta)
				}
			case "message_end":
				if ev.Message == nil || ev.Message.Role != "assistant" {
					break
				}
				lastStop = ev.Message.StopReason
				if lastStop == "error" || lastStop == "aborted" || aborting {
					break // a failed attempt may retry; never copy provider text
				}
				var blocks []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				}
				if json.Unmarshal(ev.Message.Content, &blocks) == nil {
					var final strings.Builder
					for _, b := range blocks {
						if b.Type == "text" {
							final.WriteString(b.Text)
						}
					}
					if f := final.String(); len(f) > len(msgText) && strings.HasPrefix(f, msgText) {
						emit(f[len(msgText):])
					}
				}
			case "tool_execution_start":
				if !aborting && ev.ToolName != "" {
					onUpdate(Update{Tool: ev.ToolName})
				}
			case "error":
				result.Failure = "PiG reported an error"
				return result, runErr
			case "extension_ui_request":
				result.Failure = "PiG asked for interactive input, which an A2A task cannot provide"
				return result, runErr
			case "agent_settled":
				if aborting {
					return Result{}, ctx.Err()
				}
				if !accepted {
					return Result{}, errors.New("a2a: worker settled before accepting the prompt")
				}
				switch lastStop {
				case "error":
					return Result{Text: text.String(), Failure: "the model call failed"}, nil
				case "aborted":
					return Result{Text: text.String(), Failure: "the turn was aborted"}, nil
				}
				return Result{Text: text.String()}, nil
			}
		}
	}
}
