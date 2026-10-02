package host_test

import (
	"testing"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/testkit"
	"github.com/MichaelKinsy/pigpen/ahp/internal/twin"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// Twins of upstream test/schema.test.ts, describe "wire schema".
func TestWireSchema(t *testing.T) {
	h := testkit.NewHost(host.Options{})

	twin.Run(t, "schema", "emits a conforming InitializeResult", func(t *testing.T) {
		c := testkit.Connect(t, h)
		res := c.Initialize(nextClientID(), map[string]any{"initialSubscriptions": []string{wire.RootChannel}})
		testkit.AssertValid(t, "commands", "InitializeResult", res)
	})

	twin.Run(t, "schema", "emits a conforming RootState snapshot", func(t *testing.T) {
		c := initialized(t, h)
		res := c.Subscribe(wire.RootChannel)
		if res.Snapshot == nil {
			t.Fatal("no snapshot")
		}
		testkit.AssertValid(t, "commands", "Snapshot", res.Snapshot)
		testkit.AssertValid(t, "state", "RootState", res.Snapshot.State.Root)
	})

	twin.Run(t, "schema", "emits conforming ActionEnvelopes", func(t *testing.T) {
		c := initialized(t, h, wire.RootChannel)
		h.DispatchServerAction(wire.RootChannel, ahptypes.StateAction{Value: &ahptypes.RootActiveSessionsChangedAction{
			Type: ahptypes.ActionTypeRootActiveSessionsChanged, ActiveSessions: 3,
		}})
		env := c.NextEnvelope(wire.RootChannel, 0)
		testkit.AssertValid(t, "actions", "ActionEnvelope", env)
		testkit.AssertValid(t, "actions", "StateAction", env.Action)
	})

	twin.Run(t, "schema", "fails validation for a structurally wrong action", func(t *testing.T) {
		// A reducer would accept this silently (unknown shape becomes a no-op); only the schema catches it.
		bogus := map[string]any{
			"channel":   wire.RootChannel,
			"action":    map[string]any{"type": "root/activeSessionsChanged"},
			"serverSeq": 1,
		}
		if testkit.CheckSchema("actions", "ActionEnvelope", bogus) == nil {
			t.Fatal("the schema accepted an action without its required field")
		}
	})
}

// Twins of upstream test/upstream-workarounds.test.ts: they fail once a spec re-sync no longer
// needs the workarounds the schema validator applies.
func TestUpstreamWorkarounds(t *testing.T) {
	if err := testkit.LoadSchemas(); err != nil {
		t.Fatal(err)
	}
	twin.Run(t, "upstream-workarounds", "still needs the dangling-$ref strip", func(t *testing.T) {
		if testkit.StrippedDanglingRefs == 0 {
			t.Fatal("the schemas no longer contain a dangling `$ref` to an empty `$defs` name: delete the strip from internal/testkit/schema.go and this test")
		}
	})
	twin.Run(t, "upstream-workarounds", "still needs every KNOWN_BITSET_ENUMS entry", func(t *testing.T) {
		found := false
		for _, e := range testkit.RelaxedBitsetEnums {
			if len(e) >= len("SessionStatus") && e[len(e)-len("SessionStatus"):] == "SessionStatus" {
				found = true
			}
		}
		if !found {
			t.Fatal("SessionStatus is no longer emitted as a closed enum: remove the relaxation from internal/testkit/schema.go")
		}
	})
}
