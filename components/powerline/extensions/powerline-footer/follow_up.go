package powerline_footer

import (
	"context"
	"sync"
)

// The auto follow-up input handler. upstream: index.ts 1995-2062 (the input handler), 2159-2180 (getRecentAgentContext).

// followUpHost is what the handler needs from the host's context for one input event.
type followUpHost interface {
	HasUI() bool
	Notify(message, level string)
	EditorText() (string, error)
	SetEditorText(text string)
	Branch() []map[string]any
	Signal() context.Context
	SendUserMessage(content any, deliverAs string) error
}

// inputEvent is the part of Pi's input event the handler reads.
type inputEvent struct {
	Text              string
	Source            string
	StreamingBehavior string
	Images            []any
}

type autoFollowUp struct {
	mu     sync.Mutex
	jev    jevClient
	getenv func(string) string
}

func newAutoFollowUp(jev jevClient, getenv func(string) string) *autoFollowUp {
	return &autoFollowUp{jev: jev, getenv: getenv}
}

func (f *autoFollowUp) sessionStart()                  {}
func (f *autoFollowUp) sessionShutdown()               {}
func (f *autoFollowUp) beforeAgentStart(prompt string) {}
func (f *autoFollowUp) routingCount() int              { return 0 }

// handleInput returns nil to let Pi carry on, or {"action":"handled"}.
func (f *autoFollowUp) handleInput(h followUpHost, enabled bool, ev inputEvent) any { return nil }

// recentAgentContext is getRecentAgentContext: the first 200 UTF-16 units of the latest assistant text block, "" when none.
func recentAgentContext(branch []map[string]any) string { return "" }
