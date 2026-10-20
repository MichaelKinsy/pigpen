package pi_permission_system

import (
	"runtime"
	"testing"
)

// Twins of test/handlers/gates/external-directory.test.ts.

func TestExternalDirectoryGateTwins(t *testing.T) {
	const f = "handlers/gates/external-directory"
	extTcc := func(mut ...func(*ToolCallContext)) ToolCallContext {
		return makeTcc(append([]func(*ToolCallContext){tccTool("read", obj("path", "/outside/project/file.ts")), func(c *ToolCallContext) { c.AgentName = nil }}, mut...)...)
	}
	askDefault := func() *gfResolver {
		return makeResolver(makeCheckResult(ckState("ask"), func(c *PermissionCheckResult) { c.ToolName = "external_directory" }))
	}
	// gateUnderTest builds the gate over a host-flavor normalizer for the tcc's cwd (the original's helper).
	gateUnderTest := func(t *testing.T, tcc ToolCallContext, infra []string, ex ToolAccessExtractorLookup, resolver ...ScopedPermissionResolver) GateResult {
		t.Helper()
		var r ScopedPermissionResolver = askDefault()
		if len(resolver) > 0 {
			r = resolver[0]
		}
		return DescribeExternalDirectoryGate(tcc, InfrastructureReadScope{Dirs: infra, ExcludedDirs: []string{}}, r, hostNormalizer(t, tcc.Cwd), ex)
	}
	asBypass := func(t *testing.T, g GateResult) *GateBypass {
		t.Helper()
		b, ok := g.(*GateBypass)
		if !ok {
			t.Fatalf("got %T, want a bypass", g)
		}
		return b
	}
	asDescriptor := func(t *testing.T, g GateResult) *GateDescriptor {
		t.Helper()
		d, ok := g.(*GateDescriptor)
		if !ok {
			t.Fatalf("got %T, want a descriptor", g)
		}
		return d
	}
	infraDirs := []string{"/test/agent", "/test/agent/git"}

	tw(t, f, "returns null when tool is not path-bearing", func(t *testing.T) {
		eq(t, gateUnderTest(t, extTcc(tccTool("bash", obj("command", "ls"))), []string{"/test/agent"}, nil) == nil, true, "null")
	})
	tw(t, f, "returns null when path is inside CWD", func(t *testing.T) {
		eq(t, gateUnderTest(t, extTcc(tccTool("read", obj("path", "/test/project/src/index.ts"))), []string{"/test/agent"}, nil) == nil, true, "null")
	})

	// Pi infrastructure read bypass.
	tw(t, f, "returns GateBypass for read targeting an infra dir", func(t *testing.T) {
		b := asBypass(t, gateUnderTest(t, extTcc(tccTool("read", obj("path", "/test/agent/git/some-package/SKILL.md"))), infraDirs, nil))
		eq(t, [2]any{b.Decision.Resolution, b.Decision.Result}, [2]any{PermissionDecisionResolution("infrastructure_auto_allowed"), "allow"}, "decision")
		eq(t, b.Log.Event, "permission_request.infrastructure_auto_allowed", "log event")
		// Containment allowed this, not a rule the operator wrote.
		eq(t, b.DecidedBy, DecisionSource{Kind: "infrastructure_read"}, "decidedBy")
	})
	tw(t, f, "records no extractor source, which only a built-in read tool can reach", func(t *testing.T) {
		// The bypass is gated on the read-only path-bearing tools, and every tool in that set classifies as `path`, whose branch
		// resolves by convention and never consults an extractor. So a bypass can never carry an inherited path today.
		inherited := funcLookup(func(string) *ResolvedToolAccessExtractor {
			return &ResolvedToolAccessExtractor{Origin: RegistrationInherited, Extractor: NewToolAccessExtractor(func(*jsObject) (string, bool) { return "/somewhere/else.md", true })}
		})
		b := asBypass(t, gateUnderTest(t, extTcc(tccTool("read", obj("path", "/test/agent/git/some-package/SKILL.md"))), infraDirs, inherited))
		// Full-shape, so the absence of extractorSource is asserted rather than merely unchecked.
		eq(t, b.Log.Event, "permission_request.infrastructure_auto_allowed", "event")
		eq(t, jsonOf(t, b.Log.Details), `{"source":"tool_call","toolCallId":"tc-1","toolName":"read","agentName":null,"path":"/test/agent/git/some-package/SKILL.md"}`, "details")
	})
	tw(t, f, "returns GateBypass respecting custom infraDirs", func(t *testing.T) {
		asBypass(t, gateUnderTest(t, extTcc(tccTool("read", obj("path", "/custom/infra/SKILL.md"))), []string{"/custom/infra"}, nil))
	})
	tw(t, f, "does NOT bypass for write tools targeting infra dirs", func(t *testing.T) {
		// Should be a GateDescriptor (needs permission check), not a bypass
		asDescriptor(t, gateUnderTest(t, extTcc(tccTool("write", obj("path", "/test/agent/git/some-file.ts", "content", "x"))), infraDirs, nil))
	})

	// GateDescriptor for external paths.
	tw(t, f, "returns GateDescriptor with surface 'external_directory_read'", func(t *testing.T) {
		eq(t, asDescriptor(t, gateUnderTest(t, extTcc(), []string{"/test/agent"}, nil)).Surface, "external_directory_read", "surface")
	})
	tw(t, f, "decision value is the external path", func(t *testing.T) {
		d := asDescriptor(t, gateUnderTest(t, extTcc(tccTool("read", obj("path", "/outside/project/file.ts"))), []string{"/test/agent"}, nil))
		eq(t, d.Decision, GateDecision{Surface: "external_directory_read", Value: "/outside/project/file.ts"}, "decision")
	})
	tw(t, f, "carries the child-fixed access facts on promptDetails (external_directory_read surface)", func(t *testing.T) {
		const path = "/outside/project/file.ts"
		d := asDescriptor(t, gateUnderTest(t, extTcc(tccTool("read", obj("path", path))), []string{"/test/agent"}, nil))
		ap := hostNormalizer(t, "/test/project").ForPath(path, nil)
		boundary := ap.BoundaryValue()
		eq(t, *d.PromptDetails.AccessIntent, ForwardedAccessFacts{Surface: "external_directory_read", MatchValues: ap.MatchValues(), BoundaryValue: &boundary}, "facts")
	})
	workingDir := PromptEvidence{Label: "working directory", Text: "/test/project"}
	tw(t, f, "emits an external_directory payload carrying the escaped boundary", func(t *testing.T) {
		d := asDescriptor(t, gateUnderTest(t, extTcc(tccTool("read", obj("path", "/outside/project/file.ts"))), []string{"/test/agent"}, nil))
		eq(t, d.Payload.Kind, "external_directory", "kind")
		eq(t, d.Payload.Request.Value, "/outside/project/file.ts", "value")
		eq(t, containsEvidence(d.Payload.Evidence, workingDir), true, "working directory evidence")
	})
	tw(t, f, "carries a precomputed preCheck and an empty input (matching is done by the gate)", func(t *testing.T) {
		d := asDescriptor(t, gateUnderTest(t, extTcc(tccTool("read", obj("path", "/outside/project/file.ts"))), []string{"/test/agent"}, nil))
		eq(t, jsonOf(t, d.Input), `{}`, "input")
		eq(t, d.PreCheck != nil && d.PreCheck.State == "ask", true, "preCheck")
	})
	tw(t, f, "resolves the typed and symlink-resolved aliases on the external_directory_read surface (#418)", func(t *testing.T) {
		resolver := askDefault()
		gateUnderTest(t, extTcc(tccTool("read", obj("path", "/outside/project/file.ts"))), []string{"/test/agent"}, nil, resolver)
		eq(t, len(resolver.calls), 1, "calls")
		it, ok := resolver.calls[0].(AccessPathAccessIntent)
		if !ok {
			t.Fatalf("intent %T", resolver.calls[0])
		}
		eq(t, [2]any{it.Surface, it.AgentName}, [2]any{"external_directory_read", (*string)(nil)}, "intent")
		eq(t, it.Path.MatchValues(), []string{"/outside/project/file.ts"}, "matchValues")
	})
	tw(t, f, "records a directory-scoped session approval on the external_directory_read surface", func(t *testing.T) {
		d := asDescriptor(t, gateUnderTest(t, extTcc(tccTool("read", obj("path", "/outside/project/file.ts"))), []string{"/test/agent"}, nil))
		eq(t, d.SessionApproval.Grants[0].Surface, "external_directory_read", "surface")
		eq(t, grantPatterns(d.SessionApproval), []string{"/outside/project/*"}, "patterns")
	})
	tw(t, f, "payload contains the external path and the boundary it escaped", func(t *testing.T) {
		d := asDescriptor(t, gateUnderTest(t, extTcc(tccTool("read", obj("path", "/outside/project/file.ts"))), []string{"/test/agent"}, nil))
		eq(t, d.Payload.Kind, "external_directory", "kind")
		eq(t, d.Payload.Request.ToolName, sp("read"), "toolName")
		eq(t, d.Payload.Request.Value, "/outside/project/file.ts", "value")
		eq(t, containsEvidence(d.Payload.Evidence, workingDir), true, "working directory evidence")
	})
	tw(t, f, "promptDetails includes path and tool_call source", func(t *testing.T) {
		d := asDescriptor(t, gateUnderTest(t, extTcc(tccTool("read", obj("path", "/outside/project/file.ts")), func(c *ToolCallContext) { c.AgentName, c.ToolCallID = sp("agent-1"), "tc-5" }), []string{"/test/agent"}, nil))
		eq(t, [5]any{d.PromptDetails.Source, d.PromptDetails.AgentName, d.PromptDetails.ToolCallID, d.PromptDetails.ToolName, d.PromptDetails.Path},
			[5]any{"tool_call", sp("agent-1"), sp("tc-5"), sp("read"), sp("/outside/project/file.ts")}, "details")
	})
	tw(t, f, "logContext includes the path, and no prompt wording", func(t *testing.T) {
		d := asDescriptor(t, gateUnderTest(t, extTcc(), []string{"/test/agent"}, nil))
		hasFields(t, d.LogContext, "source", "tool_call", "path", "/outside/project/file.ts")
		// The payload's request facts are stamped by the runner, not the gate.
		eq(t, d.LogContext.has("message"), false, "message")
	})

	// Extension and MCP tools are now external-directory gated (#352).
	tw(t, f, "gates an extension tool with an external input.path", func(t *testing.T) {
		d := asDescriptor(t, gateUnderTest(t, extTcc(tccTool("my-ext", obj("path", "/outside/project/file.ts"))), []string{"/test/agent"}, nil))
		eq(t, d.Surface, "external_directory", "surface")
	})
	tw(t, f, "gates an MCP tool with an external arguments.path", func(t *testing.T) {
		asDescriptor(t, gateUnderTest(t, extTcc(tccTool("mcp", obj("arguments", obj("path", "/outside/project/file.ts")))), []string{"/test/agent"}, nil))
	})
	tw(t, f, "uses a registered extractor's external path for a custom-shaped tool", func(t *testing.T) {
		asDescriptor(t, gateUnderTest(t, extTcc(tccTool("ffgrep", obj("target", "/outside/project/x"))), []string{"/test/agent"}, gfExtractors{"ffgrep", "target"}))
	})
	tw(t, f, "returns null for an extension tool whose path is inside cwd", func(t *testing.T) {
		eq(t, gateUnderTest(t, extTcc(tccTool("my-ext", obj("path", "/test/project/src/x.ts"))), []string{"/test/agent"}, nil) == nil, true, "null")
	})

	// The gate names the narrowest `external_directory`-family surface.
	surfacesFor := func(t *testing.T, toolName string) map[string]string {
		t.Helper()
		resolver := makeResolver(makeCheckResult(ckState("ask")))
		d := asDescriptor(t, gateUnderTest(t, extTcc(func(c *ToolCallContext) { c.ToolName = toolName }), []string{}, nil, resolver))
		return map[string]string{
			"intent": gfIntentSurface(resolver.calls[0]), "descriptor": d.Surface, "approval": d.SessionApproval.Grants[0].Surface,
			"facts": d.PromptDetails.AccessIntent.Surface, "decision": d.Decision.Surface, "payload": d.Payload.Request.Surface,
		}
	}
	allSurfaces := func(s string) map[string]string {
		return map[string]string{"intent": s, "descriptor": s, "approval": s, "facts": s, "decision": s, "payload": s}
	}
	for _, toolName := range []string{"read", "grep", "find", "ls"} {
		each(t, "names external_directory_read for "+toolName, func(t *testing.T) {
			eq(t, surfacesFor(t, toolName), allSurfaces("external_directory_read"), "surfaces")
		})
	}
	tw(t, f, "names external_directory_write for write", func(t *testing.T) {
		eq(t, surfacesFor(t, "write"), allSurfaces("external_directory_write"), "surfaces")
	})
	tw(t, f, "names the bare family for edit, and for a tool of unknown direction", func(t *testing.T) {
		for _, toolName := range []string{"edit", "my-ext"} {
			eq(t, surfacesFor(t, toolName), allSurfaces("external_directory"), toolName)
		}
	})
	tw(t, f, "keeps the payload kind 'external_directory' so renderer dispatch is untouched", func(t *testing.T) {
		eq(t, asDescriptor(t, gateUnderTest(t, extTcc(func(c *ToolCallContext) { c.ToolName = "read" }), []string{}, nil)).Payload.Kind, "external_directory", "kind")
	})
	tw(t, f, "derives the session approval through the injected flavor, not the host", func(t *testing.T) {
		// A native Windows path carries backslash separators the host POSIX path cannot see, so an ambient derivation collapses it to
		// `./*` and the recorded grant matches nothing (#655).
		mockRealpath(t, identityRealpath)
		tcc := extTcc(func(c *ToolCallContext) { c.Input, c.Cwd = obj("path", `C:\Other\data\x.txt`), `C:\Projects\App` })
		got := DescribeExternalDirectoryGate(tcc, InfrastructureReadScope{Dirs: []string{}, ExcludedDirs: []string{}}, askDefault(),
			NewPathNormalizer(Win32PathFlavor, `C:\Projects\App`), nil)
		eq(t, grantPatterns(asDescriptor(t, got).SessionApproval), []string{`c:\other\data\*`}, "patterns")
	})

	t.Run("infrastructure bypass under a rule", func(t *testing.T) {
		const skill = "/test/agent/git/x/SKILL.md"
		infra := []string{"/test/agent/git"}
		gateUnder := func(t *testing.T, mut ...func(*PermissionCheckResult)) GateResult {
			t.Helper()
			c := makeCheckResult(append([]func(*PermissionCheckResult){func(c *PermissionCheckResult) { c.ToolName = "external_directory_read" }}, mut...)...)
			return gateUnderTest(t, extTcc(tccTool("read", obj("path", skill))), infra, nil, makeResolver(c))
		}
		tw(t, f, "yields to a deny whose pattern names the path", func(t *testing.T) {
			d := asDescriptor(t, gateUnder(t, ckState("deny", skill)))
			eq(t, d.PreCheck.State, "deny", "state")
		})
		tw(t, f, `keeps the bypass under a catch-all "*" deny`, func(t *testing.T) {
			b := asBypass(t, gateUnder(t, ckState("deny", "*")))
			eq(t, b.DecidedBy, DecisionSource{Kind: "infrastructure_read"}, "decidedBy")
		})
		tw(t, f, `keeps the bypass under a catch-all written "**"`, func(t *testing.T) {
			// `**` compiles identically to `*`, so it is the same catch-all.
			asBypass(t, gateUnder(t, ckState("deny", "**")))
		})
		tw(t, f, "keeps the bypass under the universal fallback deny", func(t *testing.T) {
			asBypass(t, gateUnder(t, ckState("deny"), func(c *PermissionCheckResult) { c.MatchedPattern = nil }))
		})
		tw(t, f, "keeps the bypass under a targeted ask", func(t *testing.T) {
			asBypass(t, gateUnder(t, ckState("ask", skill)))
		})
	})
	_ = runtime.GOOS
}

func containsEvidence(list []PromptEvidence, want PromptEvidence) bool {
	for _, e := range list {
		if e == want {
			return true
		}
	}
	return false
}

// funcLookup is a ToolAccessExtractorLookup over a function.
type funcLookup func(string) *ResolvedToolAccessExtractor

func (f funcLookup) Resolve(name string) *ResolvedToolAccessExtractor { return f(name) }
