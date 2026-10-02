// Package jev is a Go port of y0usaf/pi-jev 0.2.2 (commit 88e5fb3): a typed
// decision layer for the coding agent. A gate judges bash, write and edit calls
// before they run; an output judge reads what bash printed; jev_ask lets the model
// ask typed questions itself.
//
// It differs from the original where the roadmap's code review found defects, and
// where the owner's rules require it: it judges nothing until the user opts in,
// says what leaves the machine, only the user's own config chooses the destination
// of the API key, and the judge is either the TypeSafe API or the model PiG is
// configured with. Every difference is listed in port/PORT.md with a test.
package jev

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const errorNotifyInterval = time.Minute

// outputCacheSeconds: identical output is judged once per window.
const outputCacheSeconds = 120

type lastGate struct {
	tool    string
	verdict gateVerdict
}

type lastOut struct {
	tool    string
	verdict outputVerdict
}

type ext struct {
	mu   sync.Mutex
	cfg  config
	be   *backend
	beEr error

	on, gateOn, outputOn bool
	mode                 string

	secrets     map[string]bool
	last        *lastGate
	lastOutput  *lastOut
	lastFailure string
	lastErrorAt time.Time
	warned      map[string]bool
	askReg      bool

	gateMemo, outMemo *memo
	now               func() time.Time
}

// Extension returns the Jev extension.
func Extension() *sdk.Extension {
	e := sdk.New("jev")
	x := &ext{cfg: defaultConfig(), secrets: map[string]bool{}, warned: map[string]bool{}, gateMemo: newMemo(), outMemo: newMemo(), now: time.Now}
	e.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) { x.sessionStart(ctx); return nil, nil })
	e.OnEvent(sdk.EventModelSelect, func(ctx sdk.Context, _ map[string]any) (any, error) { x.modelSelect(ctx); return nil, nil })
	e.OnEvent(sdk.EventToolCall, x.toolCall)
	e.OnToolResult(x.toolResult)
	e.Command("jev", "Jev: typed judgments of tool calls and output (status, on/off, mode, last, output, check)", x.command)
	return e
}

type snap struct {
	cfg                  config
	be                   *backend
	on, gateOn, outputOn bool
	mode                 string
}

func (x *ext) snapshot() snap {
	x.mu.Lock()
	defer x.mu.Unlock()
	return snap{x.cfg, x.be, x.on, x.gateOn, x.outputOn, x.mode}
}

func (x *ext) redact(s string) string {
	x.mu.Lock()
	defer x.mu.Unlock()
	for secret := range x.secrets {
		s = strings.ReplaceAll(s, secret, "[redacted]")
	}
	return s
}

func (x *ext) remember(secret string) {
	if s := strings.TrimSpace(secret); len(s) >= 8 {
		x.mu.Lock()
		x.secrets[s] = true
		x.mu.Unlock()
	}
}

func (x *ext) setStatus(ctx sdk.Context, glyph, rest string) {
	ctx.SetStatus(statusKey, statusText(x.plain(), glyph, rest))
}

func (x *ext) baseStatus(ctx sdk.Context) {
	s := x.snapshot()
	mode := "off"
	if s.gateOn {
		mode = s.mode
	}
	rest := mode
	if !s.outputOn {
		rest += " (out off)"
	}
	x.setStatus(ctx, "", rest)
}

// sessionStart reloads the configuration. Nothing is judged unless the user's own
// config says "enabled": a key alone is not consent.
func (x *ext) sessionStart(ctx sdk.Context) {
	l := loadConfig(ctx.ConfigHome(), ctx.Cwd())
	be, err := newBackend(l.Config, ctx)
	x.mu.Lock()
	x.cfg, x.be, x.beEr = l.Config, be, err
	x.on = l.Config.Enabled
	x.gateOn = x.on && l.Config.Gate.Enabled
	x.outputOn = x.on && l.Config.Output.Enabled
	x.mode = l.Config.Gate.Mode
	x.gateMemo, x.outMemo = newMemo(), newMemo()
	x.mu.Unlock()
	x.remember(l.Config.Key)

	for _, w := range l.Warnings {
		ctx.Notify("pi-jev: "+x.redact(w), "warning")
	}
	if !l.Config.Enabled {
		return
	}
	if err != nil {
		x.warnOnce(ctx, err.Error())
		return
	}
	x.baseStatus(ctx)
	if !l.Config.Acknowledged {
		ctx.Notify(startupDisclosure(l.Config, be.Destination()), "warning")
	}
	x.registerAsk(ctx)
}

// modelSelect keeps the default judge on the model the session uses. The
// disclosure names that judge as the provider that already receives the
// conversation, so after a switch (say, to a local model) judging must not keep
// sending content to the previous provider. A model named in the user's own
// config stays. Cached verdicts belong to the previous judge and are dropped.
func (x *ext) modelSelect(ctx sdk.Context) {
	x.mu.Lock()
	cfg, old := x.cfg, x.be
	x.mu.Unlock()
	if cfg.Backend != backendModel || cfg.Model != "" {
		return
	}
	be, err := newBackend(cfg, ctx)
	if err == nil && old != nil && be.Destination() == old.Destination() {
		return
	}
	x.mu.Lock()
	x.be, x.beEr = be, err
	x.gateMemo, x.outMemo = newMemo(), newMemo()
	on := x.on
	x.mu.Unlock()
	if !on {
		return
	}
	if err != nil {
		x.warnOnce(ctx, err.Error())
		return
	}
	x.baseStatus(ctx)
	x.registerAsk(ctx)
	ctx.Notify("pi-jev: now judging with "+be.Destination(), "info")
}

// warnOnce reports a setup problem once per distinct message. The missing-key text
// is the original's.
func (x *ext) warnOnce(ctx sdk.Context, reason string) {
	x.mu.Lock()
	seen := x.warned[reason]
	x.warned[reason] = true
	x.mu.Unlock()
	if seen {
		return
	}
	if strings.HasPrefix(reason, "no key.") {
		reason = fmt.Sprintf("no key. Set %s or apiKeyFile in pi-jev.json; the gate is inactive until then.", apiKeyEnv)
	}
	ctx.Notify("pi-jev: "+x.redact(reason), "warning")
}

// goContext turns the request's cancellation into a context.Context.
func goContext(c sdk.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	if done := c.Done(); done != nil {
		go func() {
			select {
			case <-done:
				cancel()
			case <-ctx.Done():
			}
		}()
	}
	return ctx, cancel
}

// lastUserRequest is the latest user text in the branch, so scope questions can
// weigh intent. An unreadable session means no request, never a failed judgment.
func lastUserRequest(ctx sdk.Context) string {
	branch, err := ctx.GetBranch()
	if err != nil {
		return ""
	}
	for i := len(branch) - 1; i >= 0; i-- {
		if branch[i].Role != "user" {
			continue
		}
		if t := strings.TrimSpace(branch[i].Content); t != "" {
			return t
		}
	}
	return ""
}

// failOpen records an unavailable judge and tells the user at most once a minute.
// The tool call proceeds: an outage must never stop the agent (the original's
// policy, kept), and the status says so instead of keeping an earlier "clear".
func (x *ext) failOpen(ctx sdk.Context, err error) {
	msg := x.redact(err.Error())
	x.mu.Lock()
	x.lastFailure = msg
	quiet := x.now().Sub(x.lastErrorAt) < errorNotifyInterval
	if !quiet {
		x.lastErrorAt = x.now()
	}
	c := x.cfg
	x.mu.Unlock()
	x.setStatus(ctx, "✗", "unavailable (failing open)")
	if !quiet {
		ctx.Notify(x.failureNote(msg, c), "error")
	}
}

func (x *ext) toolCall(ctx sdk.Context, data map[string]any) (any, error) {
	s := x.snapshot()
	tool, _ := data["toolName"].(string)
	if !s.gateOn || s.be == nil || !slices.Contains(s.cfg.Gate.Tools, tool) {
		return nil, nil
	}
	input := data["input"]
	user := lastUserRequest(ctx)
	key := strings.Join([]string{tool, stableKey(input), ctx.Cwd(), truncateText(user, userRequestChars)}, "\x00")
	resp, err := x.gateMemo.do(key, time.Duration(s.cfg.Gate.CacheSeconds)*time.Second, x.now, func() (*response, error) {
		gctx, cancel := goContext(ctx)
		defer cancel()
		state := gateStateJSON(ctx.Cwd(), tool, input, user, s.cfg.Gate.ArgumentChars, s.cfg.MaxStateChars)
		r, err := s.be.Ask(gctx, ctx, state, false, gateQuestions)
		return r, err
	})
	if err != nil {
		x.failOpen(ctx, err)
		return nil, nil
	}
	v := evaluateGate(resp, s.cfg)
	x.mu.Lock()
	x.last = &lastGate{tool, v}
	x.mu.Unlock()
	if v.Flagged {
		x.setStatus(ctx, "⚠", v.summary())
	} else {
		x.setStatus(ctx, "✓", fmt.Sprintf("clear (%s)", s.mode))
	}
	if !v.Flagged {
		return nil, nil
	}
	reason := "pi-jev: " + v.summary()
	if s.mode == "shadow" {
		ctx.Notify(x.shadowNote(tool, v, s.cfg), "warning")
		return nil, nil
	}
	if !ctx.HasUI() {
		if s.cfg.Gate.BlockWithoutUI {
			return map[string]any{"block": true, "reason": reason}, nil
		}
		// No UI means no way to approve a flagged call. Degrade to a warning rather
		// than deadlocking a headless run on a classifier's opinion.
		ctx.Notify(x.headlessNote(tool, v, s.cfg), "warning")
		return nil, nil
	}
	allow, cerr := ctx.Confirm("Jev flagged this tool call", x.confirmMessage(tool, input, v, s.cfg, s.be.Destination()))
	if cerr != nil {
		// The judge answered and flagged the call; only the user's answer is missing.
		// Fail-open covers an unavailable judge, not a missing approval: the original's
		// handler throws here and Pi blocks the call (agent-session.ts
		// _installAgentToolHooks, "Extension failed, blocking execution").
		return map[string]any{"block": true, "reason": fmt.Sprintf("%s (not confirmed: %s)", reason, x.redact(cerr.Error()))}, nil
	}
	if allow {
		return nil, nil
	}
	return map[string]any{"block": true, "reason": reason + " (declined)"}, nil
}

// contentText is the text of a tool result: the blocks' text joined by newlines.
func contentText(content any) string {
	if s, ok := content.(string); ok {
		return s
	}
	blocks, _ := content.([]any)
	var parts []string
	for _, b := range blocks {
		if m, ok := b.(map[string]any); ok {
			if t, ok := m["text"].(string); ok {
				parts = append(parts, t)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func (x *ext) toolResult(ctx sdk.Context, data map[string]any) (any, error) {
	s := x.snapshot()
	tool, _ := data["toolName"].(string)
	if !s.outputOn || s.be == nil || !slices.Contains(s.cfg.Output.Tools, tool) {
		return nil, nil
	}
	out := contentText(data["content"])
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}
	input, isErr := data["input"], data["isError"] == true
	key := tool + "\x00" + out // as the original: identical output costs one judgment
	resp, err := x.outMemo.do(key, outputCacheSeconds*time.Second, x.now, func() (*response, error) {
		gctx, cancel := goContext(ctx)
		defer cancel()
		state := outputStateJSON(ctx.Cwd(), tool, input, out, isErr, s.cfg.Output.OutputChars, s.cfg.MaxStateChars)
		return s.be.Ask(gctx, ctx, state, false, outputQuestions)
	})
	if err != nil {
		x.failOpen(ctx, err)
		return nil, nil
	}
	v := evaluateOutput(resp, s.cfg)
	x.mu.Lock()
	x.lastOutput = &lastOut{tool, v}
	x.mu.Unlock()
	if v.Notice == "" {
		return nil, nil
	}
	x.setStatus(ctx, map[string]string{"leak": "⚠", "advice": "ℹ"}[v.Kind], fmt.Sprintf("%s (%s)", v.Kind, tool))
	if v.Kind == "leak" {
		ctx.Notify(x.leakNote(tool, v.LeaksSecret, s.cfg), "warning")
	}
	// The model reads the tool result, so the notice rides with it.
	content, _ := data["content"].([]any)
	if _, isString := data["content"].(string); isString {
		content = []any{map[string]any{"type": "text", "text": data["content"]}}
	}
	patched := append(append([]any{}, content...), map[string]any{"type": "text", "text": "[pi-jev] " + v.Notice})
	return map[string]any{"content": patched}, nil
}
