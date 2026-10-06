package pi_test

import (
	"context"
	"strings"
	"testing"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
)

// The chat driver runs a client's prompt and steering on goroutines of its own (ChatDriver.track),
// and that is where client images are decoded (live.Session → PrepareImagesForPi). A panic there
// was outside every recover in the host, so one hostile message ended the AHP host process. It
// now ends that turn (or drops that steering message) with an error, as a rejected prompt does.

func TestChatDriverSurvivesPanickingPrompt(t *testing.T) {
	backend := funcBackend{prompt: func(context.Context, string) error { panic("decoder exploded") }}
	f := startFixture(t, backend)
	f.turnStarted("t1", userMessage("hi"))
	testkit.Eventually(t, "the panicking prompt to close its turn", turnCount(f, 1))
	turn := f.chat().Turns[0]
	if turn.State != ahptypes.TurnStateError {
		t.Fatalf("state = %s", turn.State)
	}
	last, ok := turn.ResponseParts[len(turn.ResponseParts)-1].Value.(*ahptypes.ErrorResponsePart)
	if !ok || !strings.Contains(last.Error.Message, "decoder exploded") {
		t.Fatalf("error part = %+v", turn.ResponseParts)
	}
	// The host still serves the next turn.
	f.turnStarted("t2", userMessage("again"))
	testkit.Eventually(t, "the next turn to close too", turnCount(f, 2))
}

func TestChatDriverSurvivesPanickingSteer(t *testing.T) {
	backend := funcBackend{steer: func(context.Context, string) error { panic("decoder exploded") }}
	f := startFixture(t, backend)
	f.turnStarted("t1", userMessage("long running"))
	testkit.Eventually(t, "the turn to be active", func() bool { return f.chat().ActiveTurn != nil })
	f.pending("steering", "steer-1", userMessage("focus"))
	testkit.Eventually(t, "the steering message to enter protocol state", func() bool { return f.chat().SteeringMessage != nil })
	testkit.Eventually(t, "the failed steering message to leave protocol state", func() bool { return f.chat().SteeringMessage == nil })
}
