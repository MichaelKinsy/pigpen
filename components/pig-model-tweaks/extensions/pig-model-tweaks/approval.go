package pigmodeltweaks

import (
	"fmt"
	"sync"
	"sync/atomic"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// handledInput is Pi's `{ action: "handled" }`: the host reads it off the input
// handler's result and sends nothing to the model. A shared value is safe
// because the host only reads it.
var handledInput = map[string]any{"action": "handled"}

// approvals owns the guard's confirmations that outlive their input handler.
//
// A confirmation runs after the handler returned because the handler itself must
// never block: PiG dispatches input handlers on the goroutine that owns the
// terminal, and a dialog opened there deadlocks the UI. The SDK keeps a Context
// usable after its handler returns, which is what makes this possible, and the
// host cancels a still-open dialog when the session ends, so every worker here
// ends with the session whether it answered, was refused, or was abandoned.
type approvals struct {
	workers sync.WaitGroup
	stopped atomic.Bool
}

// confirmOffLoop asks the guard's question after the handler returned, and
// re-sends the prompt when the user agrees.
//
// The prompt is consumed by the caller, because a decision cannot be taken after
// the fact: the host has already been told this input was handled. An accepted
// prompt therefore comes back through SendUserMessage, whose source is the
// extension rather than the terminal, so the guard does not ask about it twice.
func (s *store) confirmOffLoop(ctx sdk.Context, model modelRef, state *settings, text string, images []any) {
	if s.approvals.stopped.Load() {
		return
	}
	// Nothing to re-send: a prompt without text cannot have been worth asking
	// about, and sending an empty message would only produce a confusing turn.
	if text == "" {
		s.notifyRefused(ctx, model, state)
		return
	}
	s.approvals.workers.Add(1)
	go func() {
		defer s.approvals.workers.Done()
		if s.approvals.stopped.Load() || ctx.Err() != nil {
			return
		}
		ok, err := ctx.Confirm("Model not preferred",
			fmt.Sprintf("Current model: %s\n\nNot in your preferred list:\n%s\n\nContinue with %s?",
				model, formatAllowList(state.ModelGuard.AllowedModels), model))
		if err != nil {
			logf("confirm: %v", err)
			s.notifyRefused(ctx, model, state)
			return
		}
		if !ok {
			s.notifyRefused(ctx, model, state)
			return
		}
		if err := ctx.SendUserMessage(userContent(text, images), ""); err != nil {
			logf("send prompt: %v", err)
			ctx.Notify("the prompt could not be re-sent after confirmation", "warning")
		}
	}()
}

// confirmSelectOffLoop asks about a model switch after the model_select handler
// returned, and switches back when the user declines.
//
// The host applies a model change before it emits model_select and discards the
// handler's result (coding/session.go emits after s.agent.SetModel), so the
// selection cannot be refused where it is announced: the event is already
// history. The question therefore runs off-loop like confirmOffLoop's, and a
// decline calls SetModel on the model the event replaced, which is the only
// cancel the host offers. The switch-back emits its own model_select; the
// store's reverting flag consumes it so the answer is never asked twice.
//
// An approval is recorded session-only: the model is usable from now on, but
// remember-model keeps it out of the defaults either way.
func (s *store) confirmSelectOffLoop(ctx sdk.Context, model, previous modelRef, state *settings) {
	if s.approvals.stopped.Load() {
		return
	}
	s.approvals.workers.Add(1)
	go func() {
		defer s.approvals.workers.Done()
		if s.approvals.stopped.Load() || ctx.Err() != nil {
			return
		}
		ok, err := ctx.Confirm("Model not preferred",
			fmt.Sprintf("Switch to %s?\n\nNot in your preferred list:\n%s\n\nUse it for this session? It will not be remembered as your default.",
				model, formatAllowList(state.ModelGuard.AllowedModels)))
		if err != nil {
			logf("confirm model select: %v", err)
			return
		}
		if !s.takeAsking(model) {
			// The answer arrived after a newer selection replaced this switch
			// (and possibly opened its own dialog): acting on it would revert
			// or approve a model the user has already moved off.
			return
		}
		if ok {
			s.approveForSession(model)
			ctx.Notify("using "+model.String()+" for this session (not remembered)", "info")
			return
		}
		s.declineSelect(ctx, model, previous, state)
	}()
}

// declineSelect switches back to the model the declined selection replaced.
// Without a named previous model the session's startup model is the fallback;
// with neither there is nothing to return to, and the switch the host already
// applied is kept with a notice rather than left unexplained.
func (s *store) declineSelect(ctx sdk.Context, model, previous modelRef, state *settings) {
	if s.approvals.stopped.Load() {
		return
	}
	if previous.Provider == "" || previous.Model == "" {
		previous = s.startupModel()
	}
	if previous.Provider == "" || previous.Model == "" || sameModel(state, previous, model) {
		ctx.Notify("kept "+model.String()+": no earlier model to switch back to", "warning")
		return
	}
	s.markReverting(previous)
	if _, err := ctx.SetModel(previous.Provider + "/" + previous.Model); err != nil {
		s.markReverting(modelRef{}) // no model_select will come to consume it
		logf("switch back to %s: %v", previous, err)
		ctx.Notify("switch to "+model.String()+" declined, but "+previous.String()+" could not be restored", "warning")
		return
	}
	ctx.Notify("switch to "+model.String()+" cancelled; back on "+previous.String(), "info")
}

// notifyRefused reports a prompt that was not sent. The wording names the model
// and the command that allows it, because the alternative to a refused prompt is
// silently working on a model the user did not choose.
func (s *store) notifyRefused(ctx sdk.Context, model modelRef, state *settings) {
	if s.approvals.stopped.Load() {
		return
	}
	ctx.Notify(refusalNotice(model, state), "warning")
}

// refusalNotice is the one sentence both refusal paths use, so a prompt refused
// by the handler and one refused by a declined question read the same.
func refusalNotice(model modelRef, state *settings) string {
	return fmt.Sprintf("prompt not sent: %s is not in your preferred list (%s); /%s add %s to allow it",
		model, formatAllowList(state.ModelGuard.AllowedModels), modelGuardName, model)
}

// stopApprovals ends the guard's workers when the session does. It does not wait
// for them: a worker blocked in a dialog is released by the host's own shutdown,
// and waiting here would hold up the shutdown that releases it.
func (s *store) stopApprovals() {
	s.approvals.stopped.Store(true)
}

// userContent rebuilds the prompt the host handed the input handler. A prompt
// with images is sent as blocks so the images survive the round trip; a text-only
// prompt stays a plain string, which is the shape the host expects.
func userContent(text string, images []any) any {
	if len(images) == 0 {
		return text
	}
	blocks := make([]any, 0, len(images)+1)
	blocks = append(blocks, map[string]any{"type": "text", "text": text})
	for _, raw := range images {
		// The host owns these shapes; they are passed through as they arrived.
		if image, isObject := raw.(map[string]any); isObject {
			blocks = append(blocks, image)
		}
	}
	return blocks
}

// imagesOf reads the images an input event carried, as the host's own values.
func imagesOf(data map[string]any) []any {
	images, isList := data["images"].([]any)
	if !isList {
		return nil
	}
	return images
}

// sameModel reports whether two model references name the same model, ignoring
// the `:provider` suffix an OpenRouter lock appends to the id. Without that,
// every locked model would read as different from the default PiG was started
// with, and the guard would refuse the session's own model.
func sameModel(state *settings, left, right modelRef) bool {
	if left.Provider != right.Provider || left.Model == "" || right.Model == "" {
		return false
	}
	return baseIDOf(state, left.Model) == baseIDOf(state, right.Model)
}
