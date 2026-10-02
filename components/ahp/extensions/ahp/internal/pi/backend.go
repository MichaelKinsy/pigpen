// Package pi is the agent-facing half of the host: it drives a Pi agent through a [Backend], owns
// the live-session registry, and serves session history. It ports pi-ahp's src/pi. The
// protocol-shaped half (state and summaries) lives in package channels and knows nothing about
// the agent; the dependency runs one way.
package pi

import (
	"context"
	"encoding/json"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/mapper"
)

// Backend is the agent a chat drives. Subscribe delivers Pi's agent events (decoded JSON).
//
// Prompt and Steer take a context: cancelling it abandons the call (the agent itself is stopped
// with Abort). The optional capabilities are separate interfaces: [ModelSelector],
// [SelectionReporter], [Truncater], [Disposer].
type Backend interface {
	Subscribe(listener func(mapper.Event)) (unsubscribe func())
	Prompt(ctx context.Context, text string, images []mapper.Image) error
	Steer(ctx context.Context, text string, images []mapper.Image) error
	Abort(ctx context.Context) error
}

// Disposer releases a backend's resources. A failure is logged; it never blocks removal.
type Disposer interface{ Dispose() error }

// ModelSelector switches the model a turn runs on.
type ModelSelector interface {
	SelectModel(ctx context.Context, selection ahptypes.ModelSelection) error
}

// SelectionReporter reports the model and reasoning effort in effect.
type SelectionReporter interface {
	CurrentSelection() *ahptypes.ModelSelection
}

// Truncater moves the agent back to a session entry; false means it was refused.
type Truncater interface {
	Truncate(ctx context.Context, entryID string) (bool, error)
}

// messageInput converts a typed AHP message to Pi's prompt shape.
func messageInput(message ahptypes.Message) (mapper.MessageInput, error) {
	raw, err := json.Marshal(message)
	if err != nil {
		return mapper.MessageInput{}, err
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return mapper.MessageInput{}, err
	}
	return mapper.MessageInputForPi(generic)
}

func rejectionReason(message ahptypes.Message) string {
	raw, _ := json.Marshal(message)
	var generic any
	_ = json.Unmarshal(raw, &generic)
	return mapper.MessageRejectionReason(generic)
}
