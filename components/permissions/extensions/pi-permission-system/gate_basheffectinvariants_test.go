package pi_permission_system

import "testing"

// Twins of test/handlers/gates/bash-effect-invariants.test.ts: the bash gates over a real resolver and a real manager, because every
// claim is about the composition.

func TestBashEffectInvariantsTwins(t *testing.T) {
	const f = "handlers/gates/bash-effect-invariants"
	const cwd = "/projects/my-app"
	tccOf := func(command string) ToolCallContext {
		return ToolCallContext{ToolName: "bash", Input: obj("command", command), ToolCallID: "tc-1", Cwd: cwd}
	}
	realResolver := func(t *testing.T, permission string) *PermissionResolver {
		t.Helper()
		return NewPermissionResolver(newInMemoryManager(polScopes{global: permScope(t, permission)}), &SessionRules{})
	}
	setup := func(t *testing.T) *PathNormalizer {
		t.Helper()
		bgMockHome(t, "/mock/home")
		return bgNormalizer("linux", cwd)
	}
	pathGate := func(t *testing.T, command, permission string) GateResult {
		n := setup(t)
		return DescribeBashPathGate(tccOf(command), bgProgram(t, tccOf(command), n), realResolver(t, permission), n)
	}
	extGate := func(t *testing.T, command, permission string) GateResult {
		n := setup(t)
		return DescribeBashExternalDirectoryGate(tccOf(command), bgProgram(t, tccOf(command), n), realResolver(t, permission), n)
	}
	// resolvedState is the state a gate resolved, or "allow" when it raised nothing at all.
	resolvedState := func(g GateResult) string {
		d, ok := g.(*GateDescriptor)
		if !ok {
			return "allow"
		}
		if d.PreCheck == nil {
			return "unknown"
		}
		return d.PreCheck.State
	}
	surfaceOf := func(g GateResult) string {
		if d, ok := g.(*GateDescriptor); ok {
			return d.Surface
		}
		return ""
	}

	tw(t, f, "projects the external path each fixture command names", func(t *testing.T) {
		n := setup(t)
		program := bgProgram(t, tccOf("cat /outside/notes.md"), n)
		var got []string
		for _, a := range program.ExternalAccesses() {
			got = append(got, a.Path.Value())
		}
		eq(t, got, []string{"/outside/notes.md"}, "externals")
	})
	tw(t, f, "projects the tilde path the bare-config cases name", func(t *testing.T) {
		n := setup(t)
		program := bgProgram(t, tccOf("cat ~/.ssh/id_rsa"), n)
		var got []string
		for _, c := range program.PathRuleCandidates() {
			got = append(got, c.Token)
		}
		eq(t, got, []string{"~/.ssh/id_rsa"}, "tokens")
	})

	tw(t, f, "still denies a proven read that a bare path deny covers", func(t *testing.T) {
		g := pathGate(t, "cat ~/.ssh/id_rsa", `{"path":{"~/.ssh/*":"deny"}}`)
		_, ok := isDescriptor(g)
		eq(t, ok, true, "descriptor")
		eq(t, resolvedState(g), "deny", "state")
		// The deny survives because load-time sugar wrote the rule onto both members, not because the fold consulted them.
		eq(t, surfaceOf(g), "path_read", "surface")
	})
	tw(t, f, "still denies a proven write that a bare path deny covers", func(t *testing.T) {
		g := pathGate(t, "echo hi > ~/.ssh/authorized_keys", `{"path":{"~/.ssh/*":"deny"}}`)
		eq(t, resolvedState(g), "deny", "state")
		eq(t, surfaceOf(g), "path_write", "surface")
	})
	tw(t, f, "still asks for a proven read outside the tree under a bare external_directory ask", func(t *testing.T) {
		g := extGate(t, "cat /outside/notes.md", `{"external_directory":{"*":"ask"}}`)
		eq(t, resolvedState(g), "ask", "state")
		eq(t, surfaceOf(g), "external_directory_read", "surface")
	})
	tw(t, f, "still allows a proven read that a bare external_directory allow covers", func(t *testing.T) {
		eq(t, resolvedState(extGate(t, "cat /outside/notes.md", `{"external_directory":{"/outside/*":"allow"}}`)), "allow", "state")
	})

	const readAllowed = `{"external_directory_read":{"/outside/*":"allow"}}`
	tw(t, f, "silences a proven read outside the tree", func(t *testing.T) {
		eq(t, resolvedState(extGate(t, "cat /outside/notes.md", readAllowed)), "allow", "state")
	})
	notSilenced := func(t *testing.T, command, surface string) {
		t.Helper()
		g := extGate(t, command, readAllowed)
		if surface != "" {
			_, ok := isDescriptor(g)
			eq(t, ok, true, "descriptor")
			eq(t, surfaceOf(g), surface, "surface")
		}
		eq(t, resolvedState(g) != "allow", true, "not allowed")
	}
	tw(t, f, "does not silence rm, which is unknown rather than proven-write", func(t *testing.T) {
		notSilenced(t, "rm -rf /outside/notes.md", "external_directory")
	})
	tw(t, f, "does not silence a proven write", func(t *testing.T) {
		notSilenced(t, "echo hi > /outside/notes.md", "external_directory_write")
	})
	tw(t, f, "does not silence a guarded word whose option withdrew its claim", func(t *testing.T) {
		notSilenced(t, "find /outside -delete", "external_directory")
	})
	tw(t, f, "silences sed that only prints", func(t *testing.T) {
		eq(t, resolvedState(extGate(t, "sed -n '1,80p' /outside/notes.md", readAllowed)), "allow", "state")
	})
	tw(t, f, "does not silence an in-place sed", func(t *testing.T) {
		notSilenced(t, "sed -i 's/a/b/' /outside/notes.md", "external_directory")
	})
	tw(t, f, "does not silence sed whose script is computed", func(t *testing.T) {
		notSilenced(t, `sed -n "$range" /outside/notes.md`, "external_directory")
	})
	tw(t, f, "does not silence sed that an unquoted variable may turn in-place", func(t *testing.T) {
		notSilenced(t, "sed -n 1p $opt /outside/notes.md", "")
	})
	tw(t, f, "silences awk that only reads its input", func(t *testing.T) {
		eq(t, resolvedState(extGate(t, "awk '{print $1}' /outside/data", readAllowed)), "allow", "state")
	})
	tw(t, f, "does not silence awk that writes back to its input", func(t *testing.T) {
		notSilenced(t, "awk '{print > FILENAME}' /outside/data", "external_directory")
	})
	tw(t, f, "keeps a redirect's write proof over a print-only sed", func(t *testing.T) {
		notSilenced(t, "sed -n p /outside/a > /outside/b", "external_directory_write")
	})
	tw(t, f, "consults both directions for a command outside the core", func(t *testing.T) {
		g := extGate(t, "pnpm test /outside/spec.ts", `{"external_directory":{"*":"ask"}}`)
		_, ok := isDescriptor(g)
		eq(t, ok, true, "descriptor")
		eq(t, surfaceOf(g), "external_directory", "surface")
		eq(t, resolvedState(g), "ask", "state")
	})
	tw(t, f, "consults both directions for a path-qualified core word", func(t *testing.T) {
		eq(t, surfaceOf(extGate(t, "/tmp/evil/cat /outside/notes.md", `{"external_directory":{"*":"ask"}}`)), "external_directory", "surface")
	})
}
