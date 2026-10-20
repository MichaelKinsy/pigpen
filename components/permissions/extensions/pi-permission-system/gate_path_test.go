package pi_permission_system

import (
	"testing"
)

// Twins of test/handlers/gates/path.test.ts.

func TestPathGateTwins(t *testing.T) {
	const f = "handlers/gates/path"
	readTcc := func(mut ...func(*ToolCallContext)) ToolCallContext {
		return makeTcc(append([]func(*ToolCallContext){tccTool("read", obj("path", ".env"))}, mut...)...)
	}
	describe := func(t *testing.T, tcc ToolCallContext, r ScopedPermissionResolver, ex ToolAccessExtractorLookup) GateResult {
		t.Helper()
		return DescribePathGate(tcc, r, hostNormalizer(t, "/test/project"), ex)
	}
	asDescriptor := func(t *testing.T, g GateResult) *GateDescriptor {
		t.Helper()
		d, ok := g.(*GateDescriptor)
		if !ok {
			t.Fatalf("got %T, want a descriptor", g)
		}
		return d
	}
	envPath := func(t *testing.T, p string) *AccessPath {
		return AccessPathForPath(p, AccessPathForPathOptions{Cwd: "/test/project", Flavor: PosixPathFlavor})
	}

	tw(t, f, "returns null for non-path-bearing tools", func(t *testing.T) {
		r := makeResolver()
		eq(t, describe(t, readTcc(tccTool("bash", obj("command", "ls"))), r, nil) == nil, true, "null")
		eq(t, len(r.calls), 0, "resolves")
	})
	tw(t, f, "returns null when tool has no extractable path", func(t *testing.T) {
		eq(t, describe(t, readTcc(tccTool("read", newObject())), makeResolver(), nil) == nil, true, "null")
	})
	tw(t, f, "returns null when path check result is allow", func(t *testing.T) {
		eq(t, describe(t, readTcc(), makeResolver(makeGateCheckResult(ckState("allow"))), nil) == nil, true, "null")
	})
	tw(t, f, "returns null when matchedPattern is undefined (universal default)", func(t *testing.T) {
		r := makeResolver(makeGateCheckResult(ckState("ask"), func(c *PermissionCheckResult) { c.MatchedPattern, c.Source, c.Origin = nil, "special", "builtin" }))
		eq(t, describe(t, readTcc(), r, nil) == nil, true, "null")
	})
	tw(t, f, "returns GateDescriptor when matchedPattern is defined (explicit path rule)", func(t *testing.T) {
		r := makeResolver(makeGateCheckResult(ckState("ask", "*.env"), func(c *PermissionCheckResult) { c.Source, c.Origin = "special", "global" }))
		asDescriptor(t, describe(t, readTcc(), r, nil))
	})
	tw(t, f, "returns GateDescriptor when path check result is deny", func(t *testing.T) {
		d := asDescriptor(t, describe(t, readTcc(), makeResolver(makeGateCheckResult(ckState("deny", "*.env"))), nil))
		eq(t, d.Surface, "path_read", "surface")
		eq(t, d.PreCheck.State, "deny", "state")
	})
	tw(t, f, "returns GateDescriptor when path check result is ask", func(t *testing.T) {
		d := asDescriptor(t, describe(t, readTcc(), makeResolver(makeGateCheckResult(ckState("ask", "*.env"))), nil))
		eq(t, d.Surface, "path_read", "surface")
		eq(t, d.PreCheck.State, "ask", "state")
	})
	tw(t, f, "descriptor has correct session approval surface and pattern", func(t *testing.T) {
		d := asDescriptor(t, describe(t, readTcc(func(c *ToolCallContext) { c.Input = obj("path", "/test/project/src/.env") }), makeResolver(makeGateCheckResult(ckState("ask", "*"))), nil))
		eq(t, d.SessionApproval != nil, true, "defined")
		eq(t, d.SessionApproval.Grants[0].Surface, "path_read", "surface")
		eq(t, len(d.SessionApproval.Grants), 1, "grants")
	})
	tw(t, f, "binds a current-directory file's session approval to the cwd subtree", func(t *testing.T) {
		d := asDescriptor(t, describe(t, readTcc(func(c *ToolCallContext) { c.Input, c.Cwd = obj("path", "index.html"), "/test/project" }), makeResolver(makeGateCheckResult(ckState("ask", "*"))), nil))
		eq(t, d.SessionApproval.Grants[0].Surface, "path_read", "surface")
		eq(t, grantPatterns(d.SessionApproval), []string{"/test/project/*"}, "patterns")
	})
	tw(t, f, "descriptor denialContext references the file path and tool name", func(t *testing.T) {
		d := asDescriptor(t, describe(t, readTcc(), makeResolver(makeGateCheckResult(ckState("deny", "*.env"))), nil))
		eq(t, d.Payload.Kind, "path", "kind")
		eq(t, d.Payload.Request.ToolName, sp("read"), "toolName")
		eq(t, d.Payload.Request.Value, ".env", "value")
		eq(t, d.Payload.Request.Requester.AgentName, (*string)(nil), "agent")
	})
	tw(t, f, "carries the child-fixed access facts on promptDetails (path surface)", func(t *testing.T) {
		d := asDescriptor(t, describe(t, readTcc(), makeResolver(makeGateCheckResult(ckState("ask", "*.env"))), nil))
		ap := envPath(t, ".env")
		boundary := ap.BoundaryValue()
		eq(t, *d.PromptDetails.AccessIntent, ForwardedAccessFacts{Surface: "path_read", MatchValues: ap.MatchValues(), BoundaryValue: &boundary}, "facts")
	})
	tw(t, f, "logContext carries the request facts and no prompt wording", func(t *testing.T) {
		d := asDescriptor(t, describe(t, readTcc(func(c *ToolCallContext) { c.AgentName, c.ToolCallID = sp("agent-1"), "tc-5" }), makeResolver(makeGateCheckResult(ckState("ask", "*.env"))), nil))
		// Full-shape rather than a subset: the request facts and the request id are stamped by the runner, so these five fields are the
		// whole of the gate's own contribution.
		eq(t, jsonOf(t, d.LogContext), `{"source":"tool_call","toolCallId":"tc-5","toolName":"read","agentName":"agent-1","path":".env"}`, "logContext")
	})
	tw(t, f, "emits a path payload naming the matched rule", func(t *testing.T) {
		d := asDescriptor(t, describe(t, readTcc(), makeResolver(makeGateCheckResult(ckState("ask", "*.env"))), nil))
		eq(t, d.Payload.Kind, "path", "kind")
		eq(t, d.Payload.Request.Value, ".env", "value")
		eq(t, d.Payload.Request.MatchedPattern, sp("*.env"), "matchedPattern")
	})
	tw(t, f, "descriptor decision uses surface 'path' and the file path as value", func(t *testing.T) {
		d := asDescriptor(t, describe(t, readTcc(), makeResolver(makeGateCheckResult(ckState("deny", "*.env"))), nil))
		eq(t, d.Decision, GateDecision{Surface: "path_read", Value: ".env"}, "decision")
	})
	tw(t, f, "resolves the path surface with an access-path intent and agent name", func(t *testing.T) {
		r := makeResolver(makeGateCheckResult(ckState("allow")))
		describe(t, readTcc(func(c *ToolCallContext) { c.AgentName = sp("my-agent") }), r, nil)
		eq(t, len(r.calls), 1, "calls")
		expectAccessPathCall(t, r.calls[0], "path_read", envPath(t, ".env"), sp("my-agent"))
	})
	tw(t, f, "emits an access-path whose matchValues include the symlink-resolved form (#486)", func(t *testing.T) {
		// /test/project/.env is a symlink to /vault/secret.env.
		n := hostNormalizer(t, "/test/project")
		mockRealpath(t, func(p string) (string, error) {
			if p == "/test/project/.env" {
				return "/vault/secret.env", nil
			}
			return p, nil
		})
		r := makeResolver(makeGateCheckResult(ckState("allow")))
		DescribePathGate(readTcc(), r, n, nil)
		it := r.calls[len(r.calls)-1].(AccessPathAccessIntent)
		eq(t, it.Path.MatchValues(), []string{"/test/project/.env", ".env", "/vault/secret.env"}, "matchValues")
	})

	tw(t, f, "passes raw ~/... path to resolver and builds descriptor on deny", func(t *testing.T) {
		r := makeResolver(makeGateCheckResult(ckState("deny", "~/.ssh/*")))
		d := asDescriptor(t, describe(t, readTcc(func(c *ToolCallContext) { c.Input = obj("path", "~/.ssh/config") }), r, nil))
		eq(t, d.PreCheck.State, "deny", "state")
		// Raw path preserved on the payload for display.
		eq(t, d.Payload.Kind, "path", "kind")
		eq(t, d.Payload.Request.ToolName, sp("read"), "toolName")
		eq(t, d.Payload.Request.Value, "~/.ssh/config", "value")
		expectAccessPathCall(t, r.calls[0], "path_read", envPath(t, "~/.ssh/config"), nil)
	})
	tw(t, f, "passes raw $HOME/... path to resolver and builds descriptor on deny", func(t *testing.T) {
		r := makeResolver(makeGateCheckResult(ckState("deny", "$HOME/.ssh/*")))
		d := asDescriptor(t, describe(t, readTcc(func(c *ToolCallContext) { c.Input = obj("path", "$HOME/.ssh/config") }), r, nil))
		eq(t, d.PreCheck.State, "deny", "state")
		eq(t, d.Payload.Kind, "path", "kind")
		eq(t, d.Payload.Request.Value, "$HOME/.ssh/config", "value")
	})
	tw(t, f, "returns null when home-relative path resolves to allow", func(t *testing.T) {
		eq(t, describe(t, readTcc(func(c *ToolCallContext) { c.Input = obj("path", "~/.ssh/config") }), makeResolver(makeGateCheckResult(ckState("allow"))), nil) == nil, true, "null")
	})

	tw(t, f, "gates an extension tool that exposes input.path", func(t *testing.T) {
		r := makeResolver(makeGateCheckResult(ckState("deny", "*.env")))
		asDescriptor(t, describe(t, readTcc(tccTool("my-ext", obj("path", ".env"))), r, nil))
		expectAccessPathCall(t, r.calls[0], "path", envPath(t, ".env"), nil)
	})
	tw(t, f, "gates an MCP tool via arguments.path", func(t *testing.T) {
		r := makeResolver(makeGateCheckResult(ckState("deny", "*.env")))
		asDescriptor(t, describe(t, readTcc(tccTool("mcp", obj("arguments", obj("path", ".env")))), r, nil))
		expectAccessPathCall(t, r.calls[0], "path", envPath(t, ".env"), nil)
	})
	tw(t, f, "uses a registered extractor's path for a custom-shaped tool", func(t *testing.T) {
		r := makeResolver(makeGateCheckResult(ckState("deny", "*")))
		describe(t, readTcc(tccTool("ffgrep", obj("target", "/etc/passwd"))), r, gfExtractors{"ffgrep", "target"})
		expectAccessPathCall(t, r.calls[0], "path", envPath(t, "/etc/passwd"), nil)
	})
	tw(t, f, "returns null for an extension tool without a path", func(t *testing.T) {
		r := makeResolver()
		eq(t, describe(t, readTcc(tccTool("my-ext", obj("other", true))), r, nil) == nil, true, "null")
		eq(t, len(r.calls), 0, "resolves")
	})

	// The gate names the narrowest `path`-family surface the tool proves.
	surfacesFor := func(t *testing.T, toolName string) map[string]string {
		t.Helper()
		r := makeResolver(makeGateCheckResult(ckState("ask", "*.env")))
		d := asDescriptor(t, describe(t, readTcc(tccTool(toolName, obj("path", ".env"))), r, nil))
		return map[string]string{
			"intent": gfIntentSurface(r.calls[0]), "descriptor": d.Surface, "approval": d.SessionApproval.Grants[0].Surface,
			"facts": d.PromptDetails.AccessIntent.Surface, "decision": d.Decision.Surface, "payload": d.Payload.Request.Surface,
		}
	}
	allSurfaces := func(s string) map[string]string {
		return map[string]string{"intent": s, "descriptor": s, "approval": s, "facts": s, "decision": s, "payload": s}
	}
	for _, toolName := range []string{"read", "grep", "find", "ls"} {
		each(t, "names path_read for "+toolName+", whose read is proven by its identity", func(t *testing.T) {
			eq(t, surfacesFor(t, toolName), allSurfaces("path_read"), "surfaces")
		})
	}
	tw(t, f, "names path_write for write", func(t *testing.T) {
		eq(t, surfacesFor(t, "write"), allSurfaces("path_write"), "surfaces")
	})
	tw(t, f, "names the bare family for edit, which reads and writes", func(t *testing.T) {
		eq(t, surfacesFor(t, "edit"), allSurfaces("path"), "surfaces")
	})
	tw(t, f, "keeps the payload kind 'path' so renderer dispatch is untouched", func(t *testing.T) {
		d := asDescriptor(t, describe(t, readTcc(tccTool("read", obj("path", ".env"))), makeResolver(makeGateCheckResult(ckState("ask", "*.env"))), nil))
		eq(t, d.Payload.Kind, "path", "kind")
	})
	tw(t, f, "derives the session approval through the injected flavor, not the host", func(t *testing.T) {
		// A native Windows path carries backslash separators the host POSIX path cannot see, so an ambient derivation collapses it to
		// `./*` and the recorded grant matches nothing (#655).
		mockRealpath(t, identityRealpath)
		n := NewPathNormalizer(Win32PathFlavor, `C:\Projects\App`)
		tcc := readTcc(func(c *ToolCallContext) { c.Input, c.Cwd = obj("path", `src\foo.ts`), `C:\Projects\App` })
		d := asDescriptor(t, DescribePathGate(tcc, makeResolver(makeGateCheckResult(ckState("ask", "*.ts"))), n, nil))
		eq(t, grantPatterns(d.SessionApproval), []string{`c:\projects\app\src\*`}, "patterns")
	})
}
