package powerline_footer

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The SDK wiring of /cd, /reply and the last-prompt line. upstream: index.ts 2477-2485 (the two commands), 1228-1229,
// 1845, 1861, 1995 (last-prompt state) and 3179-3187 (the widget).
//
// /cd has one deliberate difference. The original calls process.chdir(nextCtx.cwd) inside withSession, so Pi's own process
// follows the new session's directory. A Go extension cannot do the same: os.Chdir is a fuse hazard, and the extension's
// process is not the host's. PiG's host never changes its own cwd on switchSession (it keeps the cwd per session and
// SwitchSession already replaces it), so the visible results are the same: the replacement session's tools, the footer and
// Context.Cwd() use the new directory. What a Go extension cannot observe or reproduce is a change of ITS OWN process cwd
// (relative paths in the extension's own file access). SDK capability named for the lead: host process cwd change
// (ctx.chdir). cd_command_test.go pins the difference.

// cdQuote holds the state of the slice: the last prompt shown below the editor and the cwd for /cd completions.
type cdQuote struct {
	a *app

	mu        sync.Mutex
	lp        lastPromptState
	pushed    []string // lines of the widget the host shows now, nil when none
	cwd       atomic.Pointer[string]
	widthOnce sync.Once
}

func newCdQuote(a *app) *cdQuote { return &cdQuote{a: a, lp: newLastPromptState()} }

// registerCdQuote wires /cd, /reply and the last-prompt widget (the slice's one line in Extension()).
func registerCdQuote(e *sdk.Extension, a *app) {
	q := newCdQuote(a)
	a.cdq = q
	e.RegisterCommand("cd", sdk.CommandOptions{
		Description: "Switch the Pi session working directory",
		GetArgumentCompletions: func(argumentPrefix string) ([]sdk.AutocompleteItem, error) {
			items := getCdArgumentCompletions(argumentPrefix, q.currentCwd(), getHomeDir())
			out := make([]sdk.AutocompleteItem, len(items))
			for i, item := range items {
				out[i] = sdk.AutocompleteItem{Value: item.Value, Label: item.Label, Description: item.Description}
			}
			return out, nil
		},
		Handler: func(ctx sdk.Context, args string) error { return runCdCommand(args, sdkCdHost{ctx}) },
	})
	e.RegisterCommand("reply", sdk.CommandOptions{
		Description: "Quote a previous user or assistant message into the editor",
		Handler:     runReplyCommand,
	})
	e.OnSessionShutdown(func(sdk.Context, map[string]any) (any, error) {
		q.cwd.Store(nil)
		return nil, nil
	})
	e.OnEvent(sdk.EventBeforeAgentStart, q.onBeforeAgentStart)
}

// runReplyCommand is the /reply handler and the entry point of the keys slice's `reply` shortcut action
// (index.ts 2379-2382 calls reply("", ctx)).
func runReplyCommand(ctx sdk.Context, args string) error { return reply(args, sdkQuoteContext{ctx}) }

func (q *cdQuote) currentCwd() string {
	if cwd := q.cwd.Load(); cwd != nil {
		return *cwd
	}
	wd, _ := os.Getwd()
	return wd
}

func (q *cdQuote) enabled(ctx sdk.Context) bool {
	q.a.mu.Lock()
	defer q.a.mu.Unlock()
	return q.a.core(ctx).enabled
}

// sessionStart runs at the end of the core's session_start handler (a second handler would reach the host after the core's
// and a fake host fires only the last one), with app.mu held and the core's `enabled`.
func (q *cdQuote) sessionStart(ctx sdk.Context, enabled bool) {
	cwd := ctx.Cwd()
	q.cwd.Store(&cwd)
	q.mu.Lock()
	q.lp.prompt = ""
	q.lp.show = showLastPromptSetting(readSettings(cwd))
	q.mu.Unlock()
	q.widthOnce.Do(func() {
		_, _ = ctx.OnWidthChange(func(c sdk.Context, _ int) { q.sync(c, q.enabled(c), false) })
	})
	q.sync(ctx, enabled, true)
}

func (q *cdQuote) onBeforeAgentStart(ctx sdk.Context, data map[string]any) (any, error) {
	prompt, _ := data["prompt"].(string)
	enabled := q.enabled(ctx)
	q.mu.Lock()
	q.lp.prompt = prompt
	q.mu.Unlock()
	q.sync(ctx, enabled, false)
	return nil, nil
}

// sync shows, replaces or clears the last-prompt widget. The widget lines are laid out at the terminal width when they are
// sent (the Go SDK has no widget renderer, G1), so a width change sends them again. force sends them even when unchanged:
// the original's setup clears the widgets and installs them again, which the core does on session start, /powerline and a
// placement or preset change. enabled is the core's `enabled`, passed in because /powerline calls this with app.mu held.
func (q *cdQuote) sync(ctx sdk.Context, enabled, force bool) {
	// The original installs the widget in the TUI only (`terminalWidgets = ctx.mode === "tui"`) and while the bar is enabled.
	want := enabled && ctx.HasUI() && ctx.Mode() == "tui" && lastPromptWidgetEnabled(osEnv)
	q.mu.Lock()
	defer q.mu.Unlock()
	var lines []string
	width := ctx.Width()
	if want {
		lines = q.lp.render(width)
	}
	if len(lines) == 0 {
		if q.pushed != nil {
			_ = ctx.SetWidget(widgetLastPrompt, nil)
			q.pushed = nil
		}
		return
	}
	if !force && q.pushed != nil && equalLines(q.pushed, lines) {
		return
	}
	_ = ctx.SetWidget(widgetLastPrompt, lines, sdk.WidgetOptions{"placement": "belowEditor"})
	q.pushed = lines
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// sdkCdHost is the command context of /cd over an SDK Context.
type sdkCdHost struct{ ctx sdk.Context }

func (h sdkCdHost) Cwd() string                  { return h.ctx.Cwd() }
func (h sdkCdHost) Notify(message, level string) { h.ctx.Notify(message, level) }
func (h sdkCdHost) WaitForIdle() error           { return h.ctx.WaitForIdle() }
func (h sdkCdHost) Home() string                 { return getHomeDir() }
func (h sdkCdHost) Now() time.Time               { return time.Now() }

func (h sdkCdHost) SessionFile() (string, error) {
	file, err := h.ctx.GetSessionFile()
	if err != nil || file == nil {
		return "", err
	}
	return *file, nil
}

// SessionsRoot is where Pi's getDefaultSessionDir puts the per-cwd directories. The session manager says whether the current
// session lives in such a directory; its parent is then the host's sessions root (Pi's `<agent dir>/sessions`, PiG's own
// agent directory under PiG). With a custom session directory the host's agent directory is not readable from an extension:
// PIG_CODING_AGENT_DIR (PiG) or the Pi agent directory the rest of this port reads is used.
func (h sdkCdHost) SessionsRoot() string {
	sm := h.ctx.SessionManager()
	if usesDefault, err := sm.UsesDefaultSessionDir(); err == nil && usesDefault {
		if dir, err := sm.GetSessionDir(); err == nil && dir != "" {
			return filepath.Dir(dir)
		}
	}
	if dir := os.Getenv("PIG_CODING_AGENT_DIR"); dir != "" && os.Getenv("PIG_USE_PI_DIRS") != "1" {
		return filepath.Join(normalizeAgentDirPath(dir), "sessions")
	}
	return getAgentPath("sessions")
}

type sdkNextContext struct{ ctx sdk.ReplacedSessionContext }

func (n sdkNextContext) Cwd() string                  { return n.ctx.Cwd() }
func (n sdkNextContext) Notify(message, level string) { n.ctx.Notify(message, level) }
func (n sdkNextContext) SetTitle(title string)        { n.ctx.SetTitle(title) }

func (h sdkCdHost) SwitchSession(path string, withSession func(next cdNextContext) error) (bool, error) {
	result, err := h.ctx.SwitchSession(path, map[string]any{
		"withSession": sdk.WithSessionFunc(func(next sdk.ReplacedSessionContext) error { return withSession(sdkNextContext{next}) }),
	})
	return result.Cancelled, err
}

// sdkQuoteContext is the quote-reply context over an SDK Context.
type sdkQuoteContext struct{ ctx sdk.Context }

func (c sdkQuoteContext) Mode() string { return c.ctx.Mode() }
func (c sdkQuoteContext) Branch() ([]map[string]any, error) {
	return c.ctx.SessionManager().GetBranch(nil)
}
func (c sdkQuoteContext) GetEditorText() (string, error) { return c.ctx.GetEditorText() }
func (c sdkQuoteContext) SetEditorText(text string)      { c.ctx.SetEditorText(text) }
func (c sdkQuoteContext) Notify(message, level string)   { c.ctx.Notify(message, level) }
func (c sdkQuoteContext) Select(title string, options []string) (string, bool, error) {
	return c.ctx.Select(title, options)
}
func (c sdkQuoteContext) Now() time.Time { return time.Now() }

// CanCustom: `ctx.ui.custom` exists on every Pi context; the Go SDK's Custom returns nil without a UI, which pickCandidate
// treats as a dismissed picker, exactly as the original's custom() resolves undefined there.
func (c sdkQuoteContext) CanCustom() bool { return true }

func (c sdkQuoteContext) Pick(candidates []quoteCandidate) (string, error) {
	picker := newQuotePicker(candidates, c.ctx.UITheme(), time.Now, nil, nil)
	value, err := c.ctx.Custom(&quotePickerComponent{picker: picker}, quotePickerOverlayOptions())
	if err != nil {
		return "", err
	}
	id, _ := value.(string)
	return id, nil
}
