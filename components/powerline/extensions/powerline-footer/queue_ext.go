package powerline_footer

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The SDK wiring of the queue: the /queue command, the events that drive the post-compaction delivery and the queue picker.
// upstream: index.ts registerCommand("queue") and the pi.on handlers named in queue_state.go.

// registerQueue is the one line of Extension() that wires the queue. session_start is not registered here: the core calls the
// queue first thing in its own handler.
func registerQueue(e *sdk.Extension, a *app) {
	e.Command("queue", "Manage Powerline queued prompts and project aliases", a.queueCommand)
	for _, ev := range queueEvents {
		e.OnEvent(ev, a.queueEvent(ev))
	}
}

// queueEvents are the events the queue handles itself (session_start goes through the core).
var queueEvents = []string{sdk.EventSessionBeforeCompact, sdk.EventSessionCompact, sdk.EventSessionCompactFailed, sdk.EventAgentSettled,
	sdk.EventAgentEnd, sdk.EventBeforeAgentStart, sdk.EventMessageStart, sdk.EventSessionShutdown}

// queueFor binds the core to this handler's context.
func (a *app) queueFor(ctx sdk.Context) *queueState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.core(ctx).queue
}

func (a *app) queueEvent(name string) sdk.EventFunc {
	return func(ctx sdk.Context, data map[string]any) (any, error) {
		return nil, a.queueFor(ctx).emit(name, data)
	}
}

func selectOver(ctx sdk.Context) selectFunc {
	return func(title, hint string, items []selectItem, maxVisible int) (*selectItem, error) {
		return showSelectOverlay(ctx, title, hint, items, maxVisible)
	}
}

func (a *app) queueCommand(ctx sdk.Context, args string) error {
	return a.queueFor(ctx).command(args, selectOver(ctx))
}

// openQueuePicker is openQueuePicker of index.ts 1786: the picker the queueOpen shortcut (ctrl+alt+q) opens. It blocks until
// the overlay closes and must not be called with the core's lock held.
func (a *app) openQueuePicker(ctx sdk.Context) {
	if err := a.queueFor(ctx).openQueuePicker(selectOver(ctx)); err != nil {
		ctx.Notify(err.Error(), "error")
	}
}
