package pi_permission_system

import "testing"

// Twins of test/handlers/gates/descriptor.test.ts.

func TestGateDescriptorTwins(t *testing.T) {
	const f = "handlers/gates/descriptor"
	preCheck := func(mut ...func(*PermissionCheckResult)) func(*GateDescriptor) {
		return func(d *GateDescriptor) { d.PreCheck = gfCheck(makeCheckResult(mut...)) }
	}

	tw(t, f, "returns the descriptor's own preCheck when it carries one", func(t *testing.T) {
		pc := gfCheck(makeCheckResult(ckState("deny", "rm *")))
		if got := PreResolvedCheckOf(makeDescriptor(func(d *GateDescriptor) { d.PreCheck = pc })); got != pc {
			t.Errorf("got %v, want the same preCheck", got)
		}
	})
	tw(t, f, "prefers preCheck over preResolved when both are present", func(t *testing.T) {
		pc := gfCheck(makeCheckResult(ckState("deny", "rm *")))
		d := makeDescriptor(func(d *GateDescriptor) { d.PreCheck = pc; d.PreResolved = &GatePreResolved{State: "allow"} })
		if got := PreResolvedCheckOf(d); got != pc {
			t.Errorf("got %v, want the preCheck", got)
		}
	})
	tw(t, f, "synthesizes a builtin tool check from preResolved", func(t *testing.T) {
		d := makeDescriptor(func(d *GateDescriptor) { d.Surface = "skill"; d.PreResolved = &GatePreResolved{State: "ask"} })
		eq(t, *PreResolvedCheckOf(d), PermissionCheckResult{State: "ask", ToolName: "skill", Source: "tool", Origin: "builtin"}, "check")
	})
	tw(t, f, "returns null when the descriptor resolves nothing itself", func(t *testing.T) {
		if got := PreResolvedCheckOf(makeDescriptor()); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})

	tw(t, f, "accepts a descriptor whose preCheck denies", func(t *testing.T) {
		eq(t, IsUnconditionalDeny(makeDescriptor(preCheck(ckState("deny", "rm *")))), true, "deny")
	})
	tw(t, f, "accepts a descriptor whose preResolved state denies", func(t *testing.T) {
		d := makeDescriptor(func(d *GateDescriptor) { d.Surface = "skill"; d.PreResolved = &GatePreResolved{State: "deny"} })
		eq(t, IsUnconditionalDeny(d), true, "deny")
	})
	tw(t, f, "rejects an ask, which a human could still approve", func(t *testing.T) {
		eq(t, IsUnconditionalDeny(makeDescriptor(preCheck(ckState("ask", "*")))), false, "ask")
	})
	tw(t, f, "rejects an allow", func(t *testing.T) {
		eq(t, IsUnconditionalDeny(makeDescriptor(preCheck(ckState("allow")))), false, "allow")
	})
	tw(t, f, "rejects a session-sourced deny, which the runner's fast path allows", func(t *testing.T) {
		d := makeDescriptor(preCheck(ckState("deny", "rm *"), func(c *PermissionCheckResult) { c.Source = "session" }))
		eq(t, IsUnconditionalDeny(d), false, "session deny")
	})
	tw(t, f, "rejects a descriptor that resolves nothing of its own", func(t *testing.T) {
		eq(t, IsUnconditionalDeny(makeDescriptor()), false, "nothing")
	})
	tw(t, f, "rejects a bypass and a gate that does not apply", func(t *testing.T) {
		eq(t, IsUnconditionalDeny(&GateBypass{DecidedBy: gfHuman}), false, "bypass")
		eq(t, IsUnconditionalDeny(nil), false, "null")
	})

	denying := func(surface string) GateResult {
		return makeDescriptor(func(d *GateDescriptor) { d.Surface = surface }, preCheck(ckState("deny", "*")))
	}
	asking := func(surface string) GateResult {
		return makeDescriptor(func(d *GateDescriptor) { d.Surface = surface }, preCheck(ckState("ask", "*")))
	}
	same := func(t *testing.T, got, want []GateResult) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("len %d, want %d", len(got), len(want))
		}
		for i := range want {
			// the original compares with toEqual: a deep comparison, so two askingGate("a") calls are interchangeable
			eq(t, got[i], want[i], "gate")
		}
	}
	tw(t, f, "leaves the order untouched when no gate denies", func(t *testing.T) {
		gates := []GateResult{asking("a"), nil, asking("b")}
		same(t, OrderDenyFirst(gates), gates)
	})
	tw(t, f, "moves a later denying gate ahead of the rest", func(t *testing.T) {
		a, d := asking("external_directory_read"), denying("bash")
		same(t, OrderDenyFirst([]GateResult{a, d}), []GateResult{d, a})
	})
	tw(t, f, "keeps two denying gates in their original relative order", func(t *testing.T) {
		first, second := denying("path"), denying("bash")
		same(t, OrderDenyFirst([]GateResult{first, asking("a"), second}), []GateResult{first, second, asking("a")})
	})
	tw(t, f, "keeps the non-denying remainder in its original relative order", func(t *testing.T) {
		d, first, second := denying("bash"), asking("a"), asking("b")
		same(t, OrderDenyFirst([]GateResult{first, second, d}), []GateResult{d, first, second})
	})
}
