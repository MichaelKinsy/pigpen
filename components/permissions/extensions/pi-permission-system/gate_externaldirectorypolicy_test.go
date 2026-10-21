package pi_permission_system

import "testing"

// Twins of test/handlers/gates/external-directory-policy.test.ts.

func TestExternalDirectoryPolicyTwins(t *testing.T) {
	const f = "handlers/gates/external-directory-policy"
	const cwd = "/test/project"
	coreRead := TokenEffect{Effect: EffectRead, Source: SourceCore}
	syntaxWrite := TokenEffect{Effect: EffectWrite, Source: SourceSyntax}
	forPath := func(v string) *AccessPath {
		return AccessPathForPath(v, AccessPathForPathOptions{Cwd: cwd, Flavor: PosixPathFlavor})
	}
	access := func(value string, effect ...TokenEffect) BashExternalPath {
		e := UnprovenEffect
		if len(effect) > 0 {
			e = effect[0]
		}
		return BashExternalPath{Path: forPath(value), Effect: e}
	}
	check := func(state string, mut ...func(*PermissionCheckResult)) PermissionCheckResult {
		c := PermissionCheckResult{State: state, ToolName: "external_directory", Source: "special", Origin: "builtin"}
		for _, m := range mut {
			m(&c)
		}
		return c
	}
	hasValue := func(intent AccessIntent, v string) bool {
		it, ok := intent.(AccessPathAccessIntent)
		if !ok {
			return false
		}
		for _, m := range it.Path.MatchValues() {
			if m == v {
				return true
			}
		}
		return false
	}
	values := func(paths []UncoveredExternalPath) []string {
		out := []string{}
		for _, u := range paths {
			out = append(out, u.Path.Value())
		}
		return out
	}

	tw(t, f, "resolves the path's match aliases on the external_directory surface (#418)", func(t *testing.T) {
		path := forPath("/outside/a.ts")
		resolver := makeResolver(check("ask"))
		result := ResolveExternalDirectoryPolicy(path, resolver, "external_directory", nil)
		eq(t, len(resolver.calls), 1, "calls")
		expectAccessPathCall(t, resolver.calls[0], "external_directory", path, nil)
		eq(t, result, check("ask"), "result")
	})
	tw(t, f, "threads the agent name through to the resolver", func(t *testing.T) {
		path := forPath("/outside/a.ts")
		resolver := makeResolver(check("allow"))
		ResolveExternalDirectoryPolicy(path, resolver, "external_directory", sp("reviewer"))
		expectAccessPathCall(t, resolver.calls[0], "external_directory", path, sp("reviewer"))
	})

	tw(t, f, "returns no uncovered paths when every path resolves to allow", func(t *testing.T) {
		got := SelectUncoveredExternalPaths([]BashExternalPath{access("/outside/a.ts"), access("/outside/b.ts")}, makeResolver(check("allow")), nil)
		eq(t, len(got.Uncovered), 0, "uncovered")
		eq(t, got.WorstCheck == nil, true, "worstCheck")
	})
	tw(t, f, "collects only paths whose resolved state is not allow", func(t *testing.T) {
		allowed, asked := access("/outside/ok.ts"), access("/outside/ask.ts")
		resolver := makeResolver()
		resolver.fn = func(intent AccessIntent) PermissionCheckResult {
			if hasValue(intent, "/outside/ok.ts") {
				return check("allow")
			}
			return check("ask")
		}
		got := SelectUncoveredExternalPaths([]BashExternalPath{allowed, asked}, resolver, nil)
		eq(t, values(got.Uncovered), []string{asked.Path.Value()}, "uncovered")
	})
	tw(t, f, "routes each path through the surface its own effect names", func(t *testing.T) {
		resolver := makeResolver(check("ask"))
		got := SelectUncoveredExternalPaths([]BashExternalPath{access("/outside/read.ts", coreRead), access("/outside/write.ts", syntaxWrite), access("/outside/unknown.ts")}, resolver, nil)
		want := []string{"external_directory_read", "external_directory_write", "external_directory"}
		surfaces := []string{}
		for _, u := range got.Uncovered {
			surfaces = append(surfaces, u.Surface)
		}
		eq(t, surfaces, want, "uncovered surfaces")
		called := []string{}
		for _, c := range resolver.calls {
			called = append(called, gfIntentSurface(c))
		}
		eq(t, called, want, "resolved surfaces")
	})
	tw(t, f, "returns each uncovered entry's effect for the blame line", func(t *testing.T) {
		got := SelectUncoveredExternalPaths([]BashExternalPath{access("/outside/read.ts", coreRead)}, makeResolver(check("ask")), nil)
		eq(t, len(got.Uncovered), 1, "uncovered")
		eq(t, got.Uncovered[0].Effect, coreRead, "effect")
	})
	tw(t, f, "returns the most restrictive uncovered check as worstCheck (deny > ask)", func(t *testing.T) {
		resolver := makeResolver()
		resolver.fn = func(intent AccessIntent) PermissionCheckResult {
			if hasValue(intent, "/outside/deny.ts") {
				return check("deny")
			}
			return check("ask")
		}
		got := SelectUncoveredExternalPaths([]BashExternalPath{access("/outside/ask.ts"), access("/outside/deny.ts")}, resolver, nil)
		eq(t, got.WorstCheck.State, "deny", "state")
	})

	t.Run("session coverage", func(t *testing.T) {
		session, config, fallback, asked := access("/outside/session.ts"), access("/outside/config.ts"), access("/outside/default.ts"), access("/outside/ask.ts")
		// One check per path, keyed on the path's typed value.
		resolverByPath := func() *gfResolver {
			byPath := map[string]PermissionCheckResult{
				"/outside/session.ts": check("allow", func(c *PermissionCheckResult) {
					c.Source, c.MatchedPattern, c.Origin = "session", sp("/outside/*"), "session"
				}),
				"/outside/config.ts":  check("allow", func(c *PermissionCheckResult) { c.MatchedPattern = sp("/outside/*") }),
				"/outside/default.ts": check("allow"),
				"/outside/ask.ts":     check("ask"),
			}
			r := makeResolver()
			r.fn = func(intent AccessIntent) PermissionCheckResult {
				if it, ok := intent.(AccessPathAccessIntent); ok {
					if c, found := byPath[it.Path.Value()]; found {
						return c
					}
				}
				return check("ask")
			}
			return r
		}
		covered := func(p []*AccessPath) []string {
			out := []string{}
			for _, x := range p {
				out = append(out, x.Value())
			}
			return out
		}
		tw(t, f, "collects a session-granted allow as session-covered, not uncovered", func(t *testing.T) {
			got := SelectUncoveredExternalPaths([]BashExternalPath{session}, resolverByPath(), nil)
			eq(t, len(got.Uncovered), 0, "uncovered")
			eq(t, covered(got.SessionCovered), []string{"/outside/session.ts"}, "sessionCovered")
		})
		tw(t, f, "does not count a config-rule allow as session-covered", func(t *testing.T) {
			got := SelectUncoveredExternalPaths([]BashExternalPath{config}, resolverByPath(), nil)
			eq(t, len(got.Uncovered), 0, "uncovered")
			eq(t, len(got.SessionCovered), 0, "sessionCovered")
		})
		tw(t, f, "does not count a universal-fallback allow as session-covered", func(t *testing.T) {
			got := SelectUncoveredExternalPaths([]BashExternalPath{fallback}, resolverByPath(), nil)
			eq(t, len(got.Uncovered), 0, "uncovered")
			eq(t, len(got.SessionCovered), 0, "sessionCovered")
		})
		tw(t, f, "splits a mixed set three ways", func(t *testing.T) {
			got := SelectUncoveredExternalPaths([]BashExternalPath{session, config, fallback, asked}, resolverByPath(), nil)
			eq(t, values(got.Uncovered), []string{"/outside/ask.ts"}, "uncovered")
			eq(t, covered(got.SessionCovered), []string{"/outside/session.ts"}, "sessionCovered")
		})
	})
}
