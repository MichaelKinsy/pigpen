// SPDX-License-Identifier: MIT

// Package rex provides PiG session-start context when running inside the Rex
// terminal multiplexer.
//
// When REX_BLOCK is set (non-empty) and the rex CLI is on PATH, on
// session_start it starts `rex whoami --autostart=false --json` once in the
// background with a short deadline and returns immediately. On
// before_agent_start it waits up to the remaining deadline for that probe and
// appends a single context line to the system prompt. Outside Rex (REX_BLOCK
// unset/empty or rex absent from PATH) it registers nothing and invokes no
// commands.
//
// Program status (OSC 7501) and program resume (OSC 7577) are handled by PiG
// itself, so this extension registers no tools and no status/resume hooks.
package rex

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const (
	rexBlockEnv             = "REX_BLOCK"
	defaultWhoamiWait       = 2 * time.Second
	cmdWaitDelay            = 100 * time.Millisecond
	unknownPaneFallbackLine = "You are running in a Rex terminal, but Rex cannot tell which pane you are in; do not rely on $REX_BLOCK."
	noAnswerFallbackLine    = "You are running in a Rex terminal (REX_BLOCK is set), but rex whoami did not answer; check with rex whoami --json before you rely on $REX_BLOCK."
)

// options holds injectable dependencies for deterministic testing.
type options struct {
	getenv          func(string) string
	lookPath        func(string) (string, error)
	timeout         time.Duration
	beforeAgentWait time.Duration
}

// Extension is the conventional PiG Go extension factory.
// It is inert unless REX_BLOCK is non-empty and `rex` is on PATH.
func Extension() *sdk.Extension {
	return newExtension(options{
		getenv:   os.Getenv,
		lookPath: exec.LookPath,
		timeout:  defaultWhoamiWait,
	})
}

func newExtension(opts options) *sdk.Extension {
	if opts.getenv == nil {
		opts.getenv = os.Getenv
	}
	if opts.lookPath == nil {
		opts.lookPath = exec.LookPath
	}
	if opts.timeout <= 0 {
		opts.timeout = defaultWhoamiWait
	}

	e := sdk.New("rex")
	if strings.TrimSpace(opts.getenv(rexBlockEnv)) == "" {
		return e
	}
	rexBin, err := opts.lookPath("rex")
	if err != nil || rexBin == "" {
		return e
	}

	s := &state{
		rexBin:          rexBin,
		timeout:         opts.timeout,
		beforeAgentWait: opts.beforeAgentWait,
	}

	e.OnSessionStart(func(_ sdk.Context, _ map[string]any) (any, error) {
		s.startProbe()
		return nil, nil
	})

	e.OnSessionShutdown(func(_ sdk.Context, _ map[string]any) (any, error) {
		s.stop()
		return nil, nil
	})

	e.OnEvent(sdk.EventBeforeAgentStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		return appendContextLine(s.contextLine(), ctx.GetSystemPrompt)
	})

	return e
}

type state struct {
	rexBin          string
	timeout         time.Duration
	beforeAgentWait time.Duration

	mu       sync.Mutex
	gen      uint64
	started  bool
	deadline time.Time
	done     chan struct{}
	cancel   context.CancelFunc
	line     string
}

func (s *state) startProbe() {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.gen++
	gen := s.gen
	// Allow cmdWaitDelay + small slack beyond the subprocess timeout so a
	// timed-out probe can finish teardown and publish noAnswerFallbackLine.
	s.deadline = time.Now().Add(s.timeout + cmdWaitDelay + 50*time.Millisecond)
	done := make(chan struct{})
	s.done = done
	stopCtx, stopCancel := context.WithCancel(context.Background())
	probeCtx, probeCancel := context.WithTimeout(stopCtx, s.timeout)
	s.cancel = stopCancel
	s.mu.Unlock()

	go func() {
		defer probeCancel()
		defer stopCancel()
		line := probeWhoami(probeCtx, s.rexBin)
		s.mu.Lock()
		if stopCtx.Err() == nil && s.gen == gen {
			s.line = line
			s.cancel = nil
		}
		s.mu.Unlock()
		close(done)
	}()
}

func (s *state) stop() {
	s.mu.Lock()
	cancel := s.cancel
	if cancel != nil {
		// Probe is still running; cancel it and clear started so a replacement
		// session_start probes again instead of sticking on noAnswerFallbackLine.
		s.gen++
		s.cancel = nil
		s.started = false
		s.done = nil
		s.line = ""
	}
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *state) contextLine() string {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return ""
	}
	done := s.done
	wait := time.Until(s.deadline)
	if s.beforeAgentWait > 0 && (wait <= 0 || s.beforeAgentWait < wait) {
		wait = s.beforeAgentWait
	}
	s.mu.Unlock()

	select {
	case <-done:
		s.mu.Lock()
		line := s.line
		s.mu.Unlock()
		return line
	default:
	}

	if wait <= 0 {
		return ""
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-done:
		s.mu.Lock()
		line := s.line
		s.mu.Unlock()
		return line
	case <-timer.C:
		return ""
	}
}

// whoamiResponse decodes the real location fields emitted by
// `rex whoami --autostart=false --json`.
type whoamiResponse struct {
	SessionID string `json:"session_id"`
	WindowID  string `json:"window_id"`
	BlockID   string `json:"block_id"`
}

func probeWhoami(ctx context.Context, rexBin string) string {
	cmd := exec.CommandContext(ctx, rexBin, "whoami", "--autostart=false", "--json")
	cmd.WaitDelay = cmdWaitDelay
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	// Never surface subprocess stderr or exit failures to the agent.
	cmd.Stderr = nil

	if err := cmd.Run(); err != nil {
		return noAnswerFallbackLine
	}
	return formatWhoami(stdout.Bytes())
}

func formatWhoami(raw []byte) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return noAnswerFallbackLine
	}
	var resp whoamiResponse
	if err := json.Unmarshal(trimmed, &resp); err != nil {
		return noAnswerFallbackLine
	}

	sessionID := strings.TrimSpace(resp.SessionID)
	windowID := strings.TrimSpace(resp.WindowID)
	blockID := strings.TrimSpace(resp.BlockID)

	if sessionID == "" || blockID == "" {
		return unknownPaneFallbackLine
	}

	var b strings.Builder
	b.WriteString("You are running in a Rex terminal: session ")
	b.WriteString(sessionID)
	if windowID != "" {
		b.WriteString(", tab ")
		b.WriteString(windowID)
	}
	b.WriteString(", pane ")
	b.WriteString(blockID)
	b.WriteString(".")
	return b.String()
}

func appendContextLine(line string, getSystemPrompt func() (string, error)) (any, error) {
	if line == "" {
		return nil, nil
	}
	base, err := getSystemPrompt()
	if err != nil {
		return nil, nil
	}
	if base == "" {
		return map[string]any{"systemPrompt": line}, nil
	}
	return map[string]any{"systemPrompt": base + "\n\n" + line}, nil
}
