package pi_permission_system

import "testing"

// Twins of test/handlers/gates/bash-path.test.ts.

func TestBashPathGateTwins(t *testing.T) {
	const f = "handlers/gates/bash-path"
	cmdTcc := func(command string, mut ...func(*ToolCallContext)) ToolCallContext {
		return makeTcc(append([]func(*ToolCallContext){func(c *ToolCallContext) { c.Input = obj("command", command) }}, mut...)...)
	}
	gate := func(t *testing.T, tcc ToolCallContext, r ScopedPermissionResolver) GateResult {
		t.Helper()
		bgMockHome(t, "/mock/home")
		return describeBashPathOn(t, goos(), tcc, r)
	}
	desc := func(t *testing.T, g GateResult) *GateDescriptor {
		t.Helper()
		d, ok := isDescriptor(g)
		if !ok {
			t.Fatalf("got %T, want a descriptor", g)
		}
		return d
	}
	ck := func(mut ...func(*PermissionCheckResult)) PermissionCheckResult { return makeGateCheckResult(mut...) }
	noPattern := func(c *PermissionCheckResult) {
		c.State, c.MatchedPattern, c.Source, c.Origin = "ask", nil, "special", "builtin"
	}
	askAll := func() *gfResolver { return makeResolver(ck(ckState("ask", "*"))) }

	tw(t, f, "returns null for non-bash tools", func(t *testing.T) {
		eq(t, gate(t, makeTcc(tccTool("read", obj("path", ".env"))), makeResolver()) == nil, true, "null")
	})
	tw(t, f, "returns null when no tokens are extracted", func(t *testing.T) {
		eq(t, gate(t, cmdTcc("echo hello"), makeResolver()) == nil, true, "null")
	})
	tw(t, f, "returns null when all tokens evaluate to allow", func(t *testing.T) {
		eq(t, gate(t, makeTcc(), makeResolver(ck(ckState("allow")))) == nil, true, "null")
	})
	tw(t, f, "returns GateDescriptor when a token evaluates to deny", func(t *testing.T) {
		d := desc(t, gate(t, makeTcc(), makeResolver(ck(ckState("deny", "*.env")))))
		// `cat` is a pure-reader core word, so the token routes directionally.
		eq(t, d.Surface, "path_read", "surface")
		eq(t, d.PreCheck.State, "deny", "state")
	})
	tw(t, f, "returns GateDescriptor when a token evaluates to ask", func(t *testing.T) {
		d := desc(t, gate(t, makeTcc(), askAll()))
		eq(t, d.PreCheck.State, "ask", "state")
	})
	tw(t, f, "descriptor includes triggering token in prompt message", func(t *testing.T) {
		d := desc(t, gate(t, makeTcc(), makeResolver(ck(ckState("deny", "*.env")))))
		eq(t, d.PromptDetails.Command, sp("cat .env"), "command")
		// The bash path gate asks about the offending token, not the command.
		eq(t, d.Payload.Kind, "path", "kind")
		eq(t, d.Payload.Request.Value, ".env", "value")
	})
	tw(t, f, "descriptor decision uses the surface the deciding token proved", func(t *testing.T) {
		d := desc(t, gate(t, makeTcc(), makeResolver(ck(ckState("deny", "*.env")))))
		eq(t, d.Decision.Surface, "path_read", "surface")
	})
	tw(t, f, "carries the deciding token's access facts on promptDetails (bash path surface)", func(t *testing.T) {
		r := makeResolver(ck(ckState("deny", "*.env")))
		d := desc(t, gate(t, makeTcc(), r))
		path := r.lastIntent().(AccessPathAccessIntent).Path
		boundary := path.BoundaryValue()
		eq(t, *d.PromptDetails.AccessIntent, ForwardedAccessFacts{Surface: "path_read", MatchValues: path.MatchValues(), BoundaryValue: &boundary}, "facts")
	})
	tw(t, f, "returns GateBypass when session rule covers the path", func(t *testing.T) {
		g := gate(t, makeTcc(), makeResolver(ck(func(c *PermissionCheckResult) { c.State, c.Source = "allow", "session" })))
		b, ok := isBypass(g)
		if !ok {
			t.Fatalf("got %T, want a bypass", g)
		}
		eq(t, b.DecidedBy, DecisionSource{Kind: "session_approval", Surface: "path"}, "decidedBy")
	})
	tw(t, f, "returns null when command is missing", func(t *testing.T) {
		eq(t, gate(t, makeTcc(func(c *ToolCallContext) { c.Input = newObject() }), makeResolver()) == nil, true, "null")
	})
	tw(t, f, "evaluates most restrictive across multiple tokens", func(t *testing.T) {
		r := makePathDispatchResolver(map[string]PermissionCheckResult{"src/foo.ts": ck(ckState("allow"))}, ck(ckState("deny", "*.env")))
		d := desc(t, gate(t, cmdTcc("cat src/foo.ts .env"), r))
		eq(t, d.PreCheck.State, "deny", "state")
	})
	tw(t, f, "deny wins in multi-token: cp .env README.md", func(t *testing.T) {
		r := makePathDispatchResolver(map[string]PermissionCheckResult{".env": ck(ckState("deny", "*.env"))}, ck(ckState("allow")))
		d := desc(t, gate(t, cmdTcc("cp .env README.md"), r))
		eq(t, d.PreCheck.State, "deny", "state")
		eq(t, d.Decision.Value, ".env", "value")
	})
	tw(t, f, "extracts redirect target: echo test > .env triggers deny", func(t *testing.T) {
		r := makePathDispatchResolver(map[string]PermissionCheckResult{".env": ck(ckState("deny", "*.env"))}, ck(ckState("allow")))
		d := desc(t, gate(t, cmdTcc("echo test > .env"), r))
		eq(t, d.PreCheck.State, "deny", "state")
	})
	tw(t, f, "returns null when all tokens match only the universal default", func(t *testing.T) {
		eq(t, gate(t, makeTcc(), makeResolver(ck(noPattern))) == nil, true, "null")
	})
	tw(t, f, "ignores tokens matching universal default but fires for explicit rule matches", func(t *testing.T) {
		r := makePathDispatchResolver(map[string]PermissionCheckResult{".env": ck(ckState("deny", "*.env"))}, ck(noPattern))
		d := desc(t, gate(t, cmdTcc("cat src/foo.ts .env"), r))
		eq(t, d.PreCheck.State, "deny", "state")
		eq(t, d.Decision.Value, ".env", "value")
	})
	tw(t, f, "resolves cd-aware policy values while keeping the raw prompt token", func(t *testing.T) {
		r := makeResolver(ck(ckState("deny", "*")))
		d := desc(t, gate(t, cmdTcc("cd nested && cat src/file.txt", func(c *ToolCallContext) { c.Cwd = "/test/project" }), r))
		want := AccessPathForPath("src/file.txt", AccessPathForPathOptions{Cwd: "/test/project", ResolveBase: sp("/test/project/nested"), Flavor: PosixPathFlavor})
		found := false
		for _, call := range r.calls {
			if it := call.(AccessPathAccessIntent); it.Surface == "path_read" && it.Path.Value() == want.Value() && it.AgentName == nil {
				sameAccessPath(t, it.Path, want, "path")
				found = true
			}
		}
		eq(t, found, true, "resolved the cd-aware path")
		// The raw token drives the prompt payload, the decision, and the approval.
		eq(t, d.Payload.Request.Value, "src/file.txt", "payload")
		eq(t, d.Decision.Value, "src/file.txt", "decision")
	})
	tw(t, f, "does not resolve relative policy values through an unknown cd", func(t *testing.T) {
		r := makeResolver(ck(ckState("deny", "*")))
		gate(t, cmdTcc(`cd "$DIR" && cat src/foo.ts`, func(c *ToolCallContext) { c.Cwd = "/test/project" }), r)
		want := AccessPathForLiteral("src/foo.ts")
		found := false
		for _, call := range r.calls {
			if it := call.(AccessPathAccessIntent); it.Surface == "path_read" && it.Path.Value() == "src/foo.ts" {
				sameAccessPath(t, it.Path, want, "path")
				found = true
			}
		}
		eq(t, found, true, "literal path")
	})
	tw(t, f, "binds a current-directory token's session approval to the cwd subtree", func(t *testing.T) {
		d := desc(t, gate(t, cmdTcc("cat .env", func(c *ToolCallContext) { c.Cwd = "/test/project" }), makeResolver(ck(ckState("ask", "*")))))
		eq(t, d.Decision.Value, ".env", "value")
		eq(t, d.SessionApproval.Grants[0].Surface, "path_read", "surface")
		eq(t, grantPatterns(d.SessionApproval), []string{"/test/project/*"}, "patterns")
	})

	proj := func(c *ToolCallContext) { c.Cwd = "/test/project" }
	tw(t, f, "routes a proven read to the read surface, end to end", func(t *testing.T) {
		d := desc(t, gate(t, cmdTcc("cat .env", proj), askAll()))
		eq(t, d.Surface, "path_read", "surface")
		eq(t, d.Payload.Request.Surface, "path_read", "payload")
		eq(t, d.Decision.Surface, "path_read", "decision")
		eq(t, d.PromptDetails.AccessIntent.Surface, "path_read", "facts")
		eq(t, d.SessionApproval.Grants[0].Surface, "path_read", "grant")
	})
	tw(t, f, "routes a proven write to the write surface", func(t *testing.T) {
		d := desc(t, gate(t, cmdTcc("echo hi > .env", proj), askAll()))
		eq(t, d.Surface, "path_write", "surface")
		eq(t, d.Decision.Surface, "path_write", "decision")
		eq(t, d.SessionApproval.Grants[0].Surface, "path_write", "grant")
	})
	tw(t, f, "routes an unproven token to the bare family, which folds both", func(t *testing.T) {
		d := desc(t, gate(t, cmdTcc("rm .env", proj), askAll()))
		eq(t, d.Surface, "path", "surface")
		eq(t, d.Decision.Surface, "path", "decision")
		eq(t, d.SessionApproval.Grants[0].Surface, "path", "grant")
	})
	tw(t, f, "records the deciding token's effect and blame source in the log", func(t *testing.T) {
		d := desc(t, gate(t, cmdTcc("cat .env", proj), askAll()))
		hasFields(t, d.LogContext, "effect", "read", "effectSource", "core")
	})
	tw(t, f, "records a retraction as the blame source for a guarded word", func(t *testing.T) {
		d := desc(t, gate(t, cmdTcc("sort -o ./out.txt ./data.txt", proj), askAll()))
		eq(t, d.Surface, "path", "surface")
		hasFields(t, d.LogContext, "effect", "unproven", "effectSource", "retracted")
	})
	tw(t, f, "records nothing to blame when no source claimed the token", func(t *testing.T) {
		d := desc(t, gate(t, cmdTcc("rm .env", proj), askAll()))
		hasFields(t, d.LogContext, "effect", "unproven", "effectSource", "unproven")
	})
	tw(t, f, "routes a redirect destination on its own proof, not the reader's", func(t *testing.T) {
		r := makePathDispatchResolver(map[string]PermissionCheckResult{"/test/project/out.txt": ck(ckState("ask", "*"))}, ck(ckState("allow", "*")))
		d := desc(t, gate(t, cmdTcc("cat ./in.txt > ./out.txt", proj), r))
		eq(t, d.Decision.Value, "./out.txt", "value")
		eq(t, d.Surface, "path_write", "surface")
	})
	tw(t, f, "asks on the write surface for a bare creating redirect", func(t *testing.T) {
		d := desc(t, gate(t, cmdTcc("cat x > newfile", proj), askAll()))
		eq(t, d.Surface, "path_write", "surface")
		eq(t, jsonOf(t, d.Input), `{"path":"newfile"}`, "input")
	})
	tw(t, f, "stays unrestricted when only the universal default matches", func(t *testing.T) {
		eq(t, gate(t, cmdTcc("cat x > newfile", proj), makeResolver(ck(noPattern))) == nil, true, "null")
	})

	tw(t, f, "extracts ~/... token and builds descriptor on deny", func(t *testing.T) {
		r := makePathDispatchResolver(map[string]PermissionCheckResult{"/mock/home/.ssh/config": ck(ckState("deny", "~/.ssh/*"))}, ck(ckState("allow")))
		d := desc(t, gate(t, cmdTcc("cat ~/.ssh/config"), r))
		eq(t, d.PreCheck.State, "deny", "state")
		eq(t, d.PromptDetails.Command, sp("cat ~/.ssh/config"), "command")
		eq(t, d.Payload.Request.Value, "~/.ssh/config", "value")
	})
	tw(t, f, "extracts $HOME/... token and builds descriptor on deny", func(t *testing.T) {
		r := makePathDispatchResolver(map[string]PermissionCheckResult{"/mock/home/.ssh/config": ck(ckState("deny", "$HOME/.ssh/*"))}, ck(ckState("allow")))
		d := desc(t, gate(t, cmdTcc("cat $HOME/.ssh/config"), r))
		eq(t, d.PreCheck.State, "deny", "state")
		// A plain `$HOME` reference is resolved at token collection (#694), so the displayed token is the path the shell will touch.
		eq(t, d.Payload.Request.Value, "/mock/home/.ssh/config", "value")
	})

	win := func(command string) ToolCallContext {
		return cmdTcc(command, func(c *ToolCallContext) { c.Cwd = `C:\Projects\App` })
	}
	tw(t, f, "denies a backslash-relative token matching a path rule on win32", func(t *testing.T) {
		bgMockHome(t, "/mock/home")
		r := makePathDispatchResolver(map[string]PermissionCheckResult{`dir\file`: ck(ckState("deny", "dir/file"))}, ck(ckState("allow")))
		d := desc(t, describeBashPathOn(t, "win32", win(`cat dir\file`), r))
		eq(t, d.PreCheck.State, "deny", "state")
		eq(t, d.Payload.Request.Value, `dir\file`, "value")
	})
	tw(t, f, "derives the session approval through the injected flavor, not the host", func(t *testing.T) {
		bgMockHome(t, "/mock/home")
		r := makePathDispatchResolver(map[string]PermissionCheckResult{`c:\projects\app\dir\file`: ck(ckState("ask", "dir/file"))}, ck(ckState("allow")))
		d := desc(t, describeBashPathOn(t, "win32", win(`cat dir\file`), r))
		eq(t, grantPatterns(d.SessionApproval), []string{`c:\projects\app\dir\*`}, "patterns")
	})
	tw(t, f, "does not gate a backslash-relative token on posix (stays bare)", func(t *testing.T) {
		bgMockHome(t, "/mock/home")
		r := makePathDispatchResolver(map[string]PermissionCheckResult{`dir\file`: ck(ckState("deny", "dir/file"))}, ck(ckState("allow")))
		g := describeBashPathOn(t, "linux", cmdTcc(`cat dir\file`, func(c *ToolCallContext) { c.Cwd = "/projects/app" }), r)
		eq(t, g == nil, true, "null")
	})
}
