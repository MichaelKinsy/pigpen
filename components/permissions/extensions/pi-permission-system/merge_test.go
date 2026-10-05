package pi_permission_system

import "testing"

func TestRestrictiveness(t *testing.T) {
	const f = "policy/restrictiveness"
	mk := func(state, pattern string) *CheckResult { return &CheckResult{State: state, MatchedPattern: pattern} }
	same := func(t *testing.T, got, want *CheckResult) {
		t.Helper()
		if got != want {
			t.Errorf("got %+v, want %+v (same result)", got, want)
		}
	}
	tw(t, f, "returns undefined for an empty list", func(t *testing.T) {
		if PickMostRestrictive(nil) != nil {
			t.Error("want nil")
		}
	})
	tw(t, f, "returns the single result for a one-element list", func(t *testing.T) {
		only := mk("allow", "")
		same(t, PickMostRestrictive([]*CheckResult{only}), only)
	})
	tw(t, f, "prefers deny over ask and allow regardless of position", func(t *testing.T) {
		a, k, d := mk("allow", "a"), mk("ask", "b"), mk("deny", "c")
		same(t, PickMostRestrictive([]*CheckResult{a, k, d}), d)
		same(t, PickMostRestrictive([]*CheckResult{d, k, a}), d)
	})
	tw(t, f, "prefers ask over allow when no deny is present", func(t *testing.T) {
		a, k := mk("allow", ""), mk("ask", "")
		same(t, PickMostRestrictive([]*CheckResult{a, k}), k)
	})
	tw(t, f, "keeps the first deny on ties", func(t *testing.T) {
		d1, d2 := mk("deny", "first"), mk("deny", "second")
		same(t, PickMostRestrictive([]*CheckResult{d1, d2}), d1)
	})
	tw(t, f, "keeps the first ask on ties when no deny is present", func(t *testing.T) {
		a, k1, k2 := mk("allow", ""), mk("ask", "first"), mk("ask", "second")
		same(t, PickMostRestrictive([]*CheckResult{a, k1, k2}), k1)
	})
	tw(t, f, "returns the only result for a single-element tuple", func(t *testing.T) {
		only := mk("allow", "")
		same(t, MostRestrictiveOf([]*CheckResult{only}), only)
	})
	tw(t, f, "returns the losing member's own result, not a synthesized one", func(t *testing.T) {
		a := &CheckResult{State: "allow", ToolName: "path_read", MatchedPattern: "~/dev/*"}
		d := &CheckResult{State: "deny", ToolName: "path_write", MatchedPattern: "*"}
		same(t, MostRestrictiveOf([]*CheckResult{a, d}), d)
	})
	tw(t, f, "prefers deny over ask and allow regardless of position", func(t *testing.T) {
		a, k, d := mk("allow", "a"), mk("ask", "b"), mk("deny", "c")
		same(t, MostRestrictiveOf([]*CheckResult{a, k, d}), d)
		same(t, MostRestrictiveOf([]*CheckResult{d, k, a}), d)
	})
	tw(t, f, "keeps the first member on ties", func(t *testing.T) {
		k1, k2 := mk("ask", "first"), mk("ask", "second")
		same(t, MostRestrictiveOf([]*CheckResult{k1, k2}), k1)
	})
}

func TestPermissionMerge(t *testing.T) {
	const f = "policy/permission-merge"
	m := func(t *testing.T, base, over string) string {
		return marshalJSON(MergeFlatPermissions(jo(t, base), jo(t, over)), "")
	}
	tw(t, f, "string replaces string", func(t *testing.T) { eq(t, m(t, `{"tools":"ask"}`, `{"tools":"allow"}`), `{"tools":"allow"}`, "merged") })
	tw(t, f, "both objects → shallow-merge pattern maps", func(t *testing.T) {
		eq(t, m(t, `{"bash":{"rm *":"deny","git *":"ask"}}`, `{"bash":{"rm *":"allow","npm *":"allow"}}`),
			`{"bash":{"rm *":"allow","git *":"ask","npm *":"allow"}}`, "merged")
	})
	tw(t, f, "object replaces string", func(t *testing.T) {
		eq(t, m(t, `{"tools":"ask"}`, `{"tools":{"Write":"deny"}}`), `{"tools":{"Write":"deny"}}`, "merged")
	})
	tw(t, f, "string replaces object", func(t *testing.T) {
		eq(t, m(t, `{"tools":{"Write":"deny"}}`, `{"tools":"allow"}`), `{"tools":"allow"}`, "merged")
	})
	tw(t, f, "empty override returns base unchanged", func(t *testing.T) {
		eq(t, m(t, `{"tools":"ask","bash":{"rm *":"deny"}}`, `{}`), `{"tools":"ask","bash":{"rm *":"deny"}}`, "merged")
	})
	tw(t, f, "empty base returns override", func(t *testing.T) { eq(t, m(t, `{}`, `{"tools":"allow"}`), `{"tools":"allow"}`, "merged") })
	tw(t, f, "preserves keys only in base", func(t *testing.T) {
		eq(t, m(t, `{"tools":"ask","bash":"deny"}`, `{"tools":"allow"}`), `{"tools":"allow","bash":"deny"}`, "merged")
	})
	tw(t, f, "adds keys only in override", func(t *testing.T) {
		eq(t, m(t, `{"tools":"ask"}`, `{"bash":"allow"}`), `{"tools":"ask","bash":"allow"}`, "merged")
	})
}

func TestScopeMerge(t *testing.T) {
	const f = "policy/scope-merge"
	sc := func(name, perm string, t *testing.T) Scope {
		if perm == "" {
			return Scope{Name: name}
		}
		return Scope{Name: name, Permission: jo(t, perm)}
	}
	tw(t, f, "returns empty result for empty scopes array", func(t *testing.T) {
		r := MergeScopesWithOrigins(nil)
		eq(t, marshalJSON(r.Permission, ""), `{}`, "merged")
		eq(t, len(r.Origins), 0, "origins")
	})
	tw(t, f, "attributes a string surface value to the contributing scope via the '*' pattern", func(t *testing.T) {
		r := MergeScopesWithOrigins([]Scope{sc("global", `{"bash":"allow"}`, t)})
		eq(t, marshalJSON(r.Permission, ""), `{"bash":"allow"}`, "merged")
		eq(t, r.Origins["bash"]["*"], "global", "origin")
	})
	tw(t, f, "attributes each pattern of an object surface value to the contributing scope", func(t *testing.T) {
		r := MergeScopesWithOrigins([]Scope{sc("project", `{"bash":{"git *":"allow","npm *":"deny"}}`, t)})
		eq(t, marshalJSON(r.Permission, ""), `{"bash":{"git *":"allow","npm *":"deny"}}`, "merged")
		eq(t, r.Origins["bash"]["git *"], "project", "git")
		eq(t, r.Origins["bash"]["npm *"], "project", "npm")
	})
	tw(t, f, "shallow-merge: patterns not redefined by the higher scope keep their lower-scope origin;", func(t *testing.T) {
		r := MergeScopesWithOrigins([]Scope{sc("global", `{"bash":{"ls *":"allow","git *":"allow"}}`, t), sc("project", `{"bash":{"git *":"deny"}}`, t)})
		eq(t, marshalJSON(r.Permission, ""), `{"bash":{"ls *":"allow","git *":"deny"}}`, "merged")
		eq(t, r.Origins["bash"]["ls *"], "global", "ls")
		eq(t, r.Origins["bash"]["git *"], "project", "git")
	})
	tw(t, f, "full replacement (string over object): higher scope re-attributes the entire surface to its own origin", func(t *testing.T) {
		r := MergeScopesWithOrigins([]Scope{sc("global", `{"bash":{"ls *":"allow"}}`, t), sc("project", `{"bash":"deny"}`, t)})
		eq(t, marshalJSON(r.Permission, ""), `{"bash":"deny"}`, "merged")
		eq(t, r.Origins["bash"]["*"], "project", "star")
		_, has := r.Origins["bash"]["ls *"]
		eq(t, has, false, "ls")
	})
	tw(t, f, "full replacement (object over string): higher scope re-attributes the entire surface to its own origin", func(t *testing.T) {
		r := MergeScopesWithOrigins([]Scope{sc("global", `{"bash":"ask"}`, t), sc("project", `{"bash":{"git *":"deny"}}`, t)})
		eq(t, marshalJSON(r.Permission, ""), `{"bash":{"git *":"deny"}}`, "merged")
		eq(t, r.Origins["bash"]["git *"], "project", "git")
		_, has := r.Origins["bash"]["*"]
		eq(t, has, false, "star")
	})
	tw(t, f, "applies four-scope precedence in lowest→highest order (global → project → agent → project-agent)", func(t *testing.T) {
		r := MergeScopesWithOrigins([]Scope{sc("global", `{"read":"ask"}`, t), sc("project", `{"write":"deny"}`, t), sc("agent", `{"bash":"deny"}`, t), sc("project-agent", `{"mcp":"allow"}`, t)})
		eq(t, marshalJSON(r.Permission, ""), `{"read":"ask","write":"deny","bash":"deny","mcp":"allow"}`, "merged")
		eq(t, []string{r.Origins["read"]["*"], r.Origins["write"]["*"], r.Origins["bash"]["*"], r.Origins["mcp"]["*"]},
			[]string{"global", "project", "agent", "project-agent"}, "origins")
	})
	tw(t, f, "skips scopes with no permission key, contributing nothing to either map", func(t *testing.T) {
		r := MergeScopesWithOrigins([]Scope{sc("global", "", t), sc("project", `{"bash":"allow"}`, t)})
		eq(t, marshalJSON(r.Permission, ""), `{"bash":"allow"}`, "merged")
		eq(t, r.Origins["bash"]["*"], "project", "origin")
	})
	tw(t, f, "attributes the universal '*' surface like any other (downstream reads origins.get('*')?.get('*') for universalFallbackOrigin)", func(t *testing.T) {
		r := MergeScopesWithOrigins([]Scope{sc("global", `{"*":"deny"}`, t), sc("project", `{"*":"allow"}`, t)})
		eq(t, marshalJSON(r.Permission, ""), `{"*":"allow"}`, "merged")
		eq(t, r.Origins["*"]["*"], "project", "origin")
	})
}
