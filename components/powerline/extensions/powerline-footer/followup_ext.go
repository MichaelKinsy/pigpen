package powerline_footer

import (
	"context"
	"os"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The SDK wiring of auto follow-up. upstream: index.ts 1990-2062 (before_agent_start, the input handler), 1836 and 1902 (the
// session generation). It keeps its own task, generation and routing state, so it does not touch the core's.
//
// Register it before the core's session_start handler: a test host's Fire reaches the last handler registered for an event.

// sdkFollowUpHost adapts the context of one input event to followUpHost.
type sdkFollowUpHost struct{ ctx sdk.Context }

func (h sdkFollowUpHost) HasUI() bool                  { return h.ctx.HasUI() }
func (h sdkFollowUpHost) Notify(message, level string) { h.ctx.Notify(message, level) }
func (h sdkFollowUpHost) EditorText() (string, error)  { return h.ctx.GetEditorText() }
func (h sdkFollowUpHost) SetEditorText(text string)    { h.ctx.SetEditorText(text) }
func (h sdkFollowUpHost) Signal() context.Context      { return h.ctx.Signal() }
func (h sdkFollowUpHost) SendUserMessage(content any, deliverAs string) error {
	return h.ctx.SendUserMessage(content, deliverAs)
}

func (h sdkFollowUpHost) Branch() []map[string]any {
	branch, err := h.ctx.SessionManager().GetBranch(nil)
	if err != nil {
		return nil
	}
	return branch
}

func registerFollowUp(e *sdk.Extension, a *app) {
	fu := newAutoFollowUp(newJevClient(), os.Getenv)
	e.OnEvent(sdk.EventSessionStart, func(sdk.Context, map[string]any) (any, error) {
		fu.sessionStart()
		return nil, nil
	})
	e.OnEvent(sdk.EventSessionShutdown, func(sdk.Context, map[string]any) (any, error) {
		fu.sessionShutdown()
		return nil, nil
	})
	e.OnEvent(sdk.EventBeforeAgentStart, func(_ sdk.Context, data map[string]any) (any, error) {
		prompt, _ := data["prompt"].(string)
		fu.beforeAgentStart(prompt)
		return nil, nil
	})
	e.OnEvent(sdk.EventInput, func(ctx sdk.Context, data map[string]any) (any, error) {
		a.mu.Lock()
		enabled := a.core(ctx).config.AutoFollowUp
		a.mu.Unlock()
		ev := inputEvent{}
		ev.Text, _ = data["text"].(string)
		ev.Source, _ = data["source"].(string)
		ev.StreamingBehavior, _ = data["streamingBehavior"].(string)
		ev.Images, _ = data["images"].([]any)
		return fu.handleInput(sdkFollowUpHost{ctx}, enabled, ev), nil
	})
}
