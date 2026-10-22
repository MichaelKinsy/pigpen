// Package stretchtext is a Go port of pi-stretchtext (https://github.com/MonadicMike/pi-stretchtext
// by Michal Pikosz, MIT, itself a port of cc-stretch by pro-vi): the model answers in one to three
// short sentences, and the phrases that hold more open in place into longer text, which may hold
// further phrases to open. Replies carry `{{short|long}}` stretches. The host draws each closed one
// as its underlined short form; a click on it opens it in place and a click on « closes it. alt+o
// puts a letter over each closed stretch and typing it opens it; alt+c does the same over each « to
// close.
package stretchtext

import (
	"errors"
	"fmt"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const (
	extensionName = "stretchtext"
	openKey       = "alt+o"
	closeKey      = "alt+c"
)

// instance is one loaded extension: the core, and what the core needs from the host.
type instance struct {
	core *Core

	mu       sync.Mutex
	ctx      *sdk.Context
	redrawCh chan struct{}
	stopOnce func()
	wg       sync.WaitGroup
}

// Extension returns the extension.
func Extension() *sdk.Extension {
	e := sdk.New(extensionName)
	x := &instance{}
	x.core = NewCore(RealClock(), x.theme, x.redraw)

	e.MarkdownTransformer(func(markdown string, context sdk.MarkdownTransformContext) string {
		return x.core.Transform(markdown, context.MessageType)
	})

	e.OnEvent(sdk.EventBeforeAgentStart, func(_ sdk.Context, data map[string]any) (any, error) {
		options, _ := data["systemPromptOptions"].(map[string]any)
		sections, ok := options["sections"].(*sdk.SystemPromptSections)
		if !ok {
			return nil, errors.New("stretchtext: before_agent_start carried no prompt sections")
		}
		x.core.BeforeAgentStart(sections)
		return nil, nil
	})

	// An empty widget below the editor is how the original gets the TUI and its theme; here the host
	// hands both over with the session.
	e.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		if ctx.Mode() != "tui" {
			return nil, nil
		}
		return nil, x.attach(ctx)
	})

	e.OnSessionShutdown(func(sdk.Context, map[string]any) (any, error) {
		x.shutdown()
		return nil, nil
	})

	e.Shortcut(openKey, "stretchtext: letters over closed stretches, to open one", func(ctx sdk.Context) error {
		return x.askForLetter(ctx, ModeOpen)
	})
	e.Shortcut(closeKey, "stretchtext: letters over open stretches, to close one", func(ctx sdk.Context) error {
		return x.askForLetter(ctx, ModeClose)
	})
	return e
}

// attach keeps the session's context for the theme and the redraw, starts the redraw worker and
// takes the clicks on stretch links.
func (x *instance) attach(ctx sdk.Context) error {
	x.mu.Lock()
	x.ctx = &ctx
	x.redrawCh = make(chan struct{}, 1)
	redrawCh := x.redrawCh
	x.mu.Unlock()
	x.wg.Go(func() { x.redrawLoop(ctx, redrawCh) })

	unsubscribe, err := ctx.OnLinkClick(LinkScheme, func(_ sdk.Context, url string) { x.core.Click(url) })
	if err != nil {
		return fmt.Errorf("stretchtext: take clicks on stretches: %w", err)
	}
	x.mu.Lock()
	x.stopOnce = unsubscribe
	x.mu.Unlock()
	x.core.Attach()
	return nil
}

func (x *instance) shutdown() {
	x.core.Stop()
	x.mu.Lock()
	unsubscribe := x.stopOnce
	x.stopOnce = nil
	redrawCh := x.redrawCh
	x.redrawCh = nil
	x.mu.Unlock()
	if unsubscribe != nil {
		unsubscribe()
	}
	if redrawCh != nil {
		close(redrawCh)
	}
	x.wg.Wait()
}

// redraw asks the worker to invalidate the transcript. Requests made while one is pending run once,
// as the original's Screen coalesces the redraws asked for in one tick.
func (x *instance) redraw() {
	x.mu.Lock()
	redrawCh := x.redrawCh
	x.mu.Unlock()
	if redrawCh == nil {
		return
	}
	select {
	case redrawCh <- struct{}{}:
	default:
	}
}

// redrawLoop is tui.invalidate(); tui.requestRender() of the original's Screen.redraw.
func (x *instance) redrawLoop(ctx sdk.Context, requests <-chan struct{}) {
	for range requests {
		tui := ctx.TUI()
		if err := tui.Invalidate(); err != nil {
			x.reportFailure("invalidate", err)
			continue
		}
		if err := tui.RequestRender(false); err != nil {
			x.reportFailure("request a render", err)
		}
	}
}

func (x *instance) reportFailure(what string, err error) {
	x.mu.Lock()
	ctx := x.ctx
	x.mu.Unlock()
	if ctx != nil {
		ctx.Notify(fmt.Sprintf("stretchtext: could not %s the transcript: %v", what, err), "error")
	}
}

// theme is the host's theme, nil until the session started.
func (x *instance) theme() Theme {
	x.mu.Lock()
	ctx := x.ctx
	x.mu.Unlock()
	if ctx == nil {
		return nil
	}
	return uiTheme{ctx.UITheme()}
}

// askForLetter is askForLetter of the original: letters over the stretches mode acts on, and a
// prompt that takes the letter typed.
func (x *instance) askForLetter(ctx sdk.Context, mode Mode) error {
	if ctx.Mode() != "tui" {
		return nil
	}
	prompt, notice := x.core.Alt(mode)
	if prompt == nil {
		ctx.Notify(notice, "info")
		return nil
	}
	// `void ctx.ui.custom(...)`: the prompt stays open after the shortcut returns, so it runs under the
	// session's own context and is joined at shutdown.
	x.mu.Lock()
	session := x.ctx
	x.mu.Unlock()
	if session == nil {
		return nil
	}
	x.wg.Go(func() {
		if _, err := session.Custom(letterPrompt{prompt}, nil); err != nil {
			session.Notify(fmt.Sprintf("stretchtext: the letter prompt failed: %v", err), "error")
		}
	})
	return nil
}

// letterPrompt adapts the prompt to the SDK's focused component.
type letterPrompt struct{ prompt *LetterPrompt }

func (p letterPrompt) Render(width int) []string { return []string{p.prompt.Render(width)} }

func (p letterPrompt) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	p.prompt.HandleInput(data)
	return sdk.RemoteComponentResult{Done: true}, nil
}

// uiTheme adapts the host's theme to the palette.
type uiTheme struct{ theme sdk.UITheme }

func (t uiTheme) Fg(token, text string) string { return t.theme.Fg(token, text) }
func (t uiTheme) Dark() bool                   { return t.theme.Appearance() == sdk.ThemeAppearance("dark") }
func (t uiTheme) ColorMode() string            { return t.theme.GetColorMode() }

func (t uiTheme) ColorOf(token string) (Color, bool) {
	switch c := t.theme.Colors()[token].(type) {
	case sdk.IndexedColor:
		return Indexed(c.Index), true
	case sdk.RgbColorValue:
		return RGB{c.R, c.G, c.B}, true
	case sdk.OklchColorValue:
		return OKLCH{c.L, c.C, c.H}, true
	}
	return nil, false
}
