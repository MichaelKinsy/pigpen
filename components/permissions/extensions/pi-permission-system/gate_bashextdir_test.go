package pi_permission_system

import (
	"path/filepath"
	"testing"
)

// Twins of test/handlers/gates/bash-external-directory.test.ts.

func TestBashExternalDirectoryGateTwins(t *testing.T) {
	const f = "handlers/gates/bash-external-directory"
	edTcc := func(mut ...func(*ToolCallContext)) ToolCallContext {
		in := obj("command", "cat /outside/project/file.ts")
		return ToolCallContext{ToolName: "bash", Input: in, ToolCallID: "tc-1", Cwd: "/test/project"}.with(mut...)
	}
	cmdTcc := func(command string, mut ...func(*ToolCallContext)) ToolCallContext {
		return edTcc(append([]func(*ToolCallContext){func(c *ToolCallContext) { c.Input = obj("command", command) }}, mut...)...)
	}
	ck := func(state string, mut ...func(*PermissionCheckResult)) PermissionCheckResult {
		c := PermissionCheckResult{State: state, ToolName: "external_directory", Source: "special", Origin: "builtin"}
		for _, m := range mut {
			m(&c)
		}
		return c
	}
	withSource := func(s string) func(*PermissionCheckResult) { return func(c *PermissionCheckResult) { c.Source = s } }
	withPattern := func(p string) func(*PermissionCheckResult) {
		return func(c *PermissionCheckResult) { c.MatchedPattern = sp(p) }
	}
	gate := func(t *testing.T, tcc ToolCallContext, r ScopedPermissionResolver) GateResult {
		t.Helper()
		return describeBashExtOn(t, goos(), tcc, r)
	}
	desc := func(t *testing.T, g GateResult) *GateDescriptor {
		t.Helper()
		d, ok := isDescriptor(g)
		if !ok {
			t.Fatalf("got %T, want a descriptor", g)
		}
		return d
	}
	bypass := func(t *testing.T, g GateResult) *GateBypass {
		t.Helper()
		b, ok := isBypass(g)
		if !ok {
			t.Fatalf("got %T, want a bypass", g)
		}
		return b
	}
	externalPaths := func(p PromptPayload) []string {
		out := []string{}
		for _, e := range AllEvidence(p, "external path") {
			out = append(out, e.Text)
		}
		return out
	}
	intentValues := func(intent AccessIntent) []string {
		if it, ok := intent.(AccessPathAccessIntent); ok {
			return it.Path.MatchValues()
		}
		return nil
	}
	hasValue := func(intent AccessIntent, v string) bool {
		for _, x := range intentValues(intent) {
			if x == v {
				return true
			}
		}
		return false
	}
	byIntent := func(fn func(AccessIntent) PermissionCheckResult) *gfResolver {
		r := makeResolver()
		r.fn = fn
		return r
	}
	ask := func() *gfResolver { return makeResolver(ck("ask")) }

	tw(t, f, "returns null when tool is not bash", func(t *testing.T) {
		eq(t, gate(t, edTcc(tccTool("read", obj("command", "cat /outside/project/file.ts"))), ask()) == nil, true, "null")
	})
	tw(t, f, "returns null when command has no external paths", func(t *testing.T) {
		eq(t, gate(t, cmdTcc("ls -la"), ask()) == nil, true, "null")
	})
	tw(t, f, "prompts for a $HOME write target that does not exist yet", func(t *testing.T) {
		d := desc(t, gate(t, cmdTcc(`touch "$HOME/pi-permission-system-repro-new"`), ask()))
		eq(t, externalPaths(d.Payload), []string{filepath.Join(homeDir(), "pi-permission-system-repro-new")}, "paths")
	})
	tw(t, f, "prompts for a braced ${HOME} reference", func(t *testing.T) {
		d := desc(t, gate(t, cmdTcc(`ls "${HOME}/somewhere"`), ask()))
		eq(t, externalPaths(d.Payload), []string{filepath.Join(homeDir(), "somewhere")}, "paths")
	})
	tw(t, f, "does not prompt for a variable it cannot resolve", func(t *testing.T) {
		eq(t, gate(t, cmdTcc(`CURRENT="$HOME"; ls "$CURRENT"`), ask()) == nil, true, "null")
	})
	tw(t, f, "resolves each external path on the external_directory surface via an access-path intent (#418)", func(t *testing.T) {
		r := ask()
		gate(t, cmdTcc("cat /outside/a.ts"), r)
		it := r.calls[0].(AccessPathAccessIntent)
		// `cat` is a pure-reader core word, so the path routes directionally.
		eq(t, it.Surface, "external_directory_read", "surface")
		eq(t, it.AgentName == nil, true, "agent")
		eq(t, it.Path.MatchValues(), []string{"/outside/a.ts"}, "values")
	})
	tw(t, f, "carries the deciding path's access facts on promptDetails (bash external_directory surface)", func(t *testing.T) {
		r := ask()
		d := desc(t, gate(t, cmdTcc("cat /outside/a.ts"), r))
		path := r.calls[0].(AccessPathAccessIntent).Path
		boundary := path.BoundaryValue()
		eq(t, *d.PromptDetails.AccessIntent, ForwardedAccessFacts{Surface: "external_directory_read", MatchValues: path.MatchValues(), BoundaryValue: &boundary}, "facts")
	})
	tw(t, f, "emits a bash_external_directory payload listing every escaping path", func(t *testing.T) {
		d := desc(t, gate(t, cmdTcc("cat /outside/a.ts"), ask()))
		eq(t, d.Payload.Kind, "bash_external_directory", "kind")
		eq(t, d.Payload.Request.Value, "cat /outside/a.ts", "value")
		eq(t, d.Payload.Evidence[1], PromptEvidence{Label: "external path", Text: "/outside/a.ts"}, "evidence")
	})
	tw(t, f, "returns GateBypass when all external paths are session-covered", func(t *testing.T) {
		b := bypass(t, gate(t, edTcc(), makeResolver(ck("allow", withSource("session")))))
		eq(t, b.Log.Event, "permission_request.session_approved", "event")
		eq(t, jsonOf(t, b.Log.Details), `{"source":"tool_call","toolCallId":"tc-1","toolName":"bash","agentName":null,"command":"cat /outside/project/file.ts","externalPaths":["/outside/project/file.ts"],"resolution":"session_approved"}`, "details")
		eq(t, b.DecidedBy, DecisionSource{Kind: "session_approval", Surface: "external_directory"}, "decidedBy")
	})
	tw(t, f, "returns GateDescriptor with multi-pattern sessionApproval for uncovered paths", func(t *testing.T) {
		d := desc(t, gate(t, cmdTcc("diff /outside/a.ts /outside/b.ts"), ask()))
		eq(t, d.SessionApproval != nil && len(d.SessionApproval.Grants) > 0, true, "grants")
	})
	tw(t, f, "writes no entry when every external path is config-rule allowed", func(t *testing.T) {
		r := byIntent(func(i AccessIntent) PermissionCheckResult {
			if len(intentValues(i)) > 0 {
				return ck("allow", withSource("special"), withPattern("/outside/*"))
			}
			return ck("ask")
		})
		eq(t, gate(t, edTcc(), r) == nil, true, "null")
	})
	tw(t, f, "writes no entry when every external path is allowed by the universal fallback", func(t *testing.T) {
		eq(t, gate(t, edTcc(), makeResolver(ck("allow"))) == nil, true, "null")
	})
	tw(t, f, "lists only the session-covered paths when the rest are config-allowed", func(t *testing.T) {
		r := byIntent(func(i AccessIntent) PermissionCheckResult {
			if hasValue(i, "/outside/a.ts") {
				return ck("allow", withSource("session"))
			}
			return ck("allow", withPattern("/outside/*"))
		})
		b := bypass(t, gate(t, cmdTcc("diff /outside/a.ts /outside/b.ts"), r))
		eq(t, b.DecidedBy, DecisionSource{Kind: "session_approval", Surface: "external_directory"}, "decidedBy")
		got, _ := b.Log.Details.get("externalPaths")
		eq(t, jsonOf(t, got), `["/outside/a.ts"]`, "externalPaths")
	})
	tw(t, f, "uses worst-check state from uncovered paths for preCheck (config deny > catch-all ask)", func(t *testing.T) {
		r := byIntent(func(i AccessIntent) PermissionCheckResult {
			if len(intentValues(i)) > 0 {
				return ck("deny", withSource("special"))
			}
			return ck("ask")
		})
		eq(t, desc(t, gate(t, edTcc(), r)).PreCheck.State, "deny", "state")
	})
	tw(t, f, "descriptor surface names what the deciding path proved", func(t *testing.T) {
		eq(t, desc(t, gate(t, edTcc(), ask())).Surface, "external_directory_read", "surface")
	})
	tw(t, f, "descriptor decision surface names what the deciding path proved", func(t *testing.T) {
		eq(t, desc(t, gate(t, edTcc(), ask())).Decision.Surface, "external_directory_read", "surface")
	})
	tw(t, f, "asks on the write surface for a bare target after a non-literal cd", func(t *testing.T) {
		eq(t, desc(t, gate(t, cmdTcc(`cd "$D" && echo hi > out.txt`), ask())).Surface, "external_directory_write", "surface")
	})
	tw(t, f, "routes a proven read to the read surface, end to end", func(t *testing.T) {
		d := desc(t, gate(t, cmdTcc("cat /outside/a.ts"), ask()))
		eq(t, d.Surface, "external_directory_read", "surface")
		eq(t, d.Payload.Request.Surface, "external_directory_read", "payload")
		eq(t, d.Decision.Surface, "external_directory_read", "decision")
		eq(t, d.SessionApproval.Grants[0].Surface, "external_directory_read", "grant")
	})
	tw(t, f, "routes a proven write to the write surface", func(t *testing.T) {
		d := desc(t, gate(t, cmdTcc("echo hi > /outside/out.txt"), ask()))
		eq(t, d.Surface, "external_directory_write", "surface")
		eq(t, d.SessionApproval.Grants[0].Surface, "external_directory_write", "grant")
	})
	tw(t, f, "routes an unproven path to the bare family, which folds both", func(t *testing.T) {
		d := desc(t, gate(t, cmdTcc("rm -rf /outside/gone"), ask()))
		eq(t, d.Surface, "external_directory", "surface")
		eq(t, d.SessionApproval.Grants[0].Surface, "external_directory", "grant")
	})
	tw(t, f, "grants each path only the direction its own token proved", func(t *testing.T) {
		d := desc(t, gate(t, cmdTcc("cat /outside/a.ts > /elsewhere/b.ts"), ask()))
		eq(t, d.SessionApproval.Grants, []ApprovalGrant{{"external_directory_read", "/outside/*"}, {"external_directory_write", "/elsewhere/*"}}, "grants")
	})
	tw(t, f, "grants both directions when two directions share a directory", func(t *testing.T) {
		d := desc(t, gate(t, cmdTcc("cat /outside/a.ts > /outside/b.ts"), ask()))
		eq(t, d.SessionApproval.Grants, []ApprovalGrant{{"external_directory_read", "/outside/*"}, {"external_directory_write", "/outside/*"}}, "grants")
	})
	tw(t, f, "records the deciding path's effect and blame source in the log", func(t *testing.T) {
		hasFields(t, desc(t, gate(t, cmdTcc("cat /outside/a.ts"), ask())).LogContext, "effect", "read", "effectSource", "core")
	})
	tw(t, f, "records a retraction as the blame source for a guarded word", func(t *testing.T) {
		d := desc(t, gate(t, cmdTcc("find /outside -delete"), ask()))
		eq(t, d.Surface, "external_directory", "surface")
		hasFields(t, d.LogContext, "effect", "unproven", "effectSource", "retracted")
	})
	tw(t, f, "records a retraction when a variable may spell the withdrawing option", func(t *testing.T) {
		d := desc(t, gate(t, cmdTcc("A=-delete; find /outside $A"), ask()))
		eq(t, d.Surface, "external_directory", "surface")
		hasFields(t, d.LogContext, "effect", "unproven", "effectSource", "retracted")
	})
	tw(t, f, "payload carries the command and the boundary it escaped", func(t *testing.T) {
		d := desc(t, gate(t, cmdTcc("cat /outside/file.ts"), ask()))
		eq(t, d.Payload.Kind, "bash_external_directory", "kind")
		eq(t, d.Payload.Request.Value, "cat /outside/file.ts", "value")
		eq(t, FindEvidence(d.Payload, "working directory").Text, "/test/project", "cwd")
	})
	tw(t, f, "promptDetails includes command and tool_call source", func(t *testing.T) {
		d := desc(t, gate(t, edTcc(func(c *ToolCallContext) { c.AgentName, c.ToolCallID = sp("agent-1"), "tc-5" }), ask()))
		pd := d.PromptDetails
		eq(t, pd.Source, PermissionReviewSource("tool_call"), "source")
		eq(t, pd.AgentName, sp("agent-1"), "agent")
		eq(t, pd.ToolCallID, sp("tc-5"), "id")
		eq(t, pd.ToolName, sp("bash"), "tool")
		eq(t, pd.Command, sp("cat /outside/project/file.ts"), "command")
	})
	tw(t, f, "config-allowed path is excluded; remaining ask path produces a descriptor", func(t *testing.T) {
		r := byIntent(func(i AccessIntent) PermissionCheckResult {
			if hasValue(i, "/outside/a.ts") {
				return ck("allow", withSource("special"))
			}
			return ck("ask")
		})
		d := desc(t, gate(t, cmdTcc("diff /outside/a.ts /outside/b.ts"), r))
		eq(t, len(d.SessionApproval.Grants), 1, "grants")
		eq(t, d.PreCheck.State, "ask", "state")
	})
	tw(t, f, "config-denied path makes worstCheck deny even when another path is ask", func(t *testing.T) {
		r := byIntent(func(i AccessIntent) PermissionCheckResult {
			if hasValue(i, "/outside/a.ts") {
				return ck("deny", withSource("special"))
			}
			return ck("ask")
		})
		d := desc(t, gate(t, cmdTcc("diff /outside/a.ts /outside/b.ts"), r))
		eq(t, d.PreCheck.State, "deny", "state")
		eq(t, len(d.SessionApproval.Grants), 2, "grants")
	})
	tw(t, f, "only includes uncovered paths when some are session-covered", func(t *testing.T) {
		r := byIntent(func(i AccessIntent) PermissionCheckResult {
			if hasValue(i, "/outside/a.ts") {
				return ck("allow", withSource("session"))
			}
			return ck("ask")
		})
		d := desc(t, gate(t, cmdTcc("diff /outside/a.ts /outside/b.ts"), r))
		eq(t, len(d.SessionApproval.Grants), 1, "grants")
	})

	// Review-log provenance through the real resolver.
	describeWithConfig := func(t *testing.T, permission string, session *SessionRules) GateResult {
		t.Helper()
		manager := newInMemoryManager(polScopes{global: permScope(t, permission)})
		manager.ConfigureForCwd("/test/project")
		return gate(t, cmdTcc("head -1 /etc/hostname"), NewPermissionResolver(manager, session))
	}
	tw(t, f, "writes no entry when the universal fallback allows the path", func(t *testing.T) {
		eq(t, describeWithConfig(t, `{"*":"allow"}`, &SessionRules{}) == nil, true, "null")
	})
	tw(t, f, "records a session approval when a session grant covers the path", func(t *testing.T) {
		session := &SessionRules{}
		session.Approve("external_directory_read", "/etc/*")
		b := bypass(t, describeWithConfig(t, `{"*":"allow"}`, session))
		eq(t, b.Log.Event, "permission_request.session_approved", "event")
		got, _ := b.Log.Details.get("externalPaths")
		eq(t, jsonOf(t, got), `["/etc/hostname"]`, "externalPaths")
	})

	// Git Bash semantics (win32).
	winTcc := func(command string) ToolCallContext {
		return cmdTcc(command, func(c *ToolCallContext) { c.Cwd = "C:/projects/app" })
	}
	tw(t, f, "does not prompt for a /dev/null redirect target", func(t *testing.T) {
		eq(t, describeBashExtOn(t, "win32", winTcc("echo hi > /dev/null"), ask()) == nil, true, "null")
	})
	tw(t, f, `prompts for a /tmp path displayed as typed, not as C:\tmp`, func(t *testing.T) {
		d := desc(t, describeBashExtOn(t, "win32", winTcc("ls /tmp"), ask()))
		eq(t, externalPaths(d.Payload), []string{"/tmp"}, "paths")
	})
	tw(t, f, "derives the session approval through the injected flavor, not the host", func(t *testing.T) {
		d := desc(t, describeBashExtOn(t, "win32", cmdTcc(`cat C:\Other\data\x.txt`, func(c *ToolCallContext) { c.Cwd = `C:\Projects\App` }), ask()))
		eq(t, grantPatterns(d.SessionApproval), []string{`c:\other\data\*`}, "patterns")
	})
	tw(t, f, "keeps a POSIX-shaped Git Bash directory token scoped to itself", func(t *testing.T) {
		d := desc(t, describeBashExtOn(t, "win32", winTcc("ls /tmp/logs/"), ask()))
		eq(t, grantPatterns(d.SessionApproval), []string{"/tmp/logs/*"}, "patterns")
	})
}
