package pi_permission_system

import "testing"

func TestToolSurfaceBaseline(t *testing.T) {
	const f = "exposure/tool-surface-baseline"
	registeredTools := []string{"read", "bash", "edit", "write", "ls", "grep", "task"}
	denying := func(denied ...string) func(string) bool {
		return func(n string) bool {
			for _, d := range denied {
				if d == n {
					return false
				}
			}
			return true
		}
	}
	reg := func(names []string) map[string]bool {
		m := map[string]bool{}
		for _, n := range names {
			m[n] = true
		}
		return m
	}
	// seen is the observation double: pi keeps a tool registered when it deactivates it.
	resolve := func(b *surfaceBaseline, active []string, isExposed func(string) bool, registered ...[]string) surfaceResolution {
		r := registeredTools
		if len(registered) > 0 {
			r = registered[0]
		}
		return b.resolveExposed(active, reg(r), isExposed)
	}
	S := func(s ...string) []string { return s }

	tw(t, f, "exposes every observed tool when the policy withholds nothing", func(t *testing.T) {
		got := resolve(&surfaceBaseline{}, S("read", "bash", "ls"), denying())
		eq(t, got, surfaceResolution{exposed: S("read", "bash", "ls")}, "surface")
	})
	tw(t, f, "never exposes a tool it has not observed active", func(t *testing.T) {
		b := &surfaceBaseline{}
		resolve(b, S("read", "bash"), denying())
		eq(t, resolve(b, S("read", "bash"), denying()).exposed, S("read", "bash"), "exposed")
	})
	tw(t, f, "adopts a tool that becomes active mid-session", func(t *testing.T) {
		b := &surfaceBaseline{}
		resolve(b, S("read"), denying())
		eq(t, resolve(b, S("read", "task"), denying()).exposed, S("read", "task"), "exposed")
	})
	tw(t, f, "reports the withheld tool and leaves it out of the exposed set", func(t *testing.T) {
		got := resolve(&surfaceBaseline{}, S("read", "ls", "bash"), denying("ls"))
		eq(t, got.exposed, S("read", "bash"), "exposed")
		eq(t, got.withheld, S("ls"), "withheld")
		eq(t, got.changed, true, "changed")
	})
	tw(t, f, "reports no change while the same tool stays withheld", func(t *testing.T) {
		b := &surfaceBaseline{}
		resolve(b, S("read", "ls"), denying("ls"))
		got := resolve(b, S("read"), denying("ls"))
		eq(t, got.withheld, S("ls"), "withheld")
		eq(t, got.changed, false, "changed")
	})
	tw(t, f, "restores a withheld tool once the policy exposes it again", func(t *testing.T) {
		b := &surfaceBaseline{}
		resolve(b, S("read", "ls"), denying("ls"))
		got := resolve(b, S("read"), denying())
		eq(t, got.exposed, S("read", "ls"), "exposed")
		eq(t, got.restored, S("ls"), "restored")
		eq(t, len(got.withheld), 0, "withheld")
		eq(t, got.changed, true, "changed")
	})
	tw(t, f, "restores the tool to its original position, not the end", func(t *testing.T) {
		b := &surfaceBaseline{}
		resolve(b, S("read", "ls", "bash", "write"), denying("ls"))
		eq(t, resolve(b, S("read", "bash", "write"), denying()).exposed, S("read", "ls", "bash", "write"), "exposed")
	})
	tw(t, f, "restores only the tools the relaxed policy now exposes", func(t *testing.T) {
		b := &surfaceBaseline{}
		resolve(b, S("read", "ls", "grep"), denying("ls", "grep"))
		got := resolve(b, S("read"), denying("grep"))
		eq(t, got.exposed, S("read", "ls"), "exposed")
		eq(t, got.restored, S("ls"), "restored")
		eq(t, got.withheld, S("grep"), "withheld")
	})
	tw(t, f, "drops a tool that stopped being active without being withheld", func(t *testing.T) {
		b := &surfaceBaseline{}
		resolve(b, S("read", "task"), denying())
		got := resolve(b, S("read"), denying())
		eq(t, got.exposed, S("read"), "exposed")
		eq(t, len(got.withheld), 0, "withheld")
	})
	tw(t, f, "does not resurrect a dropped tool when the policy later relaxes", func(t *testing.T) {
		b := &surfaceBaseline{}
		resolve(b, S("read", "task"), denying("task"))
		resolve(b, S(), denying("task"))
		eq(t, resolve(b, S(), denying()).exposed, S("task"), "exposed")
	})
	tw(t, f, "forgets a withheld tool once pi no longer has it registered", func(t *testing.T) {
		b := &surfaceBaseline{}
		resolve(b, S("read", "ls"), denying("ls"))
		resolve(b, S("read"), denying("ls"), S("read"))
		eq(t, resolve(b, S("read"), denying(), S("read")).exposed, S("read"), "exposed")
	})
	tw(t, f, "does not reactivate a withheld tool that pi re-registers inactive", func(t *testing.T) {
		b := &surfaceBaseline{}
		resolve(b, S("read", "ls"), denying("ls"))
		resolve(b, S("read"), denying("ls"), S("read"))
		eq(t, resolve(b, S("read"), denying(), S("read", "ls")).exposed, S("read"), "exposed")
	})
	tw(t, f, "keeps every active tool even when the registry reports nothing", func(t *testing.T) {
		got := (&surfaceBaseline{}).resolveExposed(S("read", "bash"), map[string]bool{}, denying())
		eq(t, got.exposed, S("read", "bash"), "exposed")
	})
	tw(t, f, "forgets the baseline so the next turn reseeds from Pi", func(t *testing.T) {
		b := &surfaceBaseline{}
		resolve(b, S("read", "ls"), denying("ls"))
		b.reset()
		got := resolve(b, S("read"), denying())
		eq(t, got.exposed, S("read"), "exposed")
		eq(t, len(got.restored), 0, "restored")
	})
}
