package pi_permission_system

import "testing"

// Cases beyond the original's tests: the parts of the bash path gate's result the originals never read (the whole session bypass, the
// worst-token selection, the identity of an aliased shell tool, the early stop at a deny).

func TestBashPathGateExtras(t *testing.T) {
	ck := func(mut ...func(*PermissionCheckResult)) PermissionCheckResult { return makeGateCheckResult(mut...) }
	gate := func(t *testing.T, tcc ToolCallContext, r ScopedPermissionResolver) GateResult {
		t.Helper()
		bgMockHome(t, "/mock/home")
		return describeBashPathOn(t, goos(), tcc, r)
	}
	cmd := func(command string, mut ...func(*ToolCallContext)) ToolCallContext {
		return makeTcc(append([]func(*ToolCallContext){func(c *ToolCallContext) { c.Input = obj("command", command) }}, mut...)...)
	}

	t.Run("the session bypass logs the whole command and every token", func(t *testing.T) {
		r := makeResolver(ck(func(c *PermissionCheckResult) { c.State, c.Source = "allow", "session" }))
		tcc := cmd("cat ./a.txt ./b.txt", func(c *ToolCallContext) { c.AgentName, c.ToolName = sp("agent-1"), "exec_command" })
		n := bgNormalizer(goos(), tcc.Cwd)
		program, err := ParseBashProgram("cat ./a.txt ./b.txt", n, nil)
		if err != nil {
			t.Fatal(err)
		}
		bgMockHome(t, "/mock/home")
		b, ok := isBypass(DescribeBashPathGate(tcc, program, r, n))
		if !ok {
			t.Fatal("want a bypass")
		}
		eq(t, b.Log.Event, "permission_request.session_approved", "event")
		eq(t, jsonOf(t, b.Log.Details), `{"source":"tool_call","toolCallId":"tc-1","toolName":"exec_command","agentName":"agent-1","command":"cat ./a.txt ./b.txt","tokens":["./a.txt","./b.txt"],"resolution":"session_approved"}`, "details")
		eq(t, b.Decision == nil, true, "no decision event")
	})
	t.Run("the first of equally restrictive tokens decides", func(t *testing.T) {
		r := makePathDispatchResolver(map[string]PermissionCheckResult{
			"/test/project/a.txt": ck(ckState("ask", "*")), "/test/project/b.txt": ck(ckState("ask", "*")),
		}, ck(ckState("allow")))
		d := gate(t, cmd("cat ./a.txt ./b.txt"), r).(*GateDescriptor)
		eq(t, d.Decision.Value, "./a.txt", "value")
	})
	t.Run("a deny after asks decides, and later tokens are not resolved", func(t *testing.T) {
		r := makePathDispatchResolver(map[string]PermissionCheckResult{
			"/test/project/a.txt": ck(ckState("ask", "*")), "/test/project/b.txt": ck(ckState("deny", "*.txt")), "/test/project/c.txt": ck(ckState("deny", "*.txt")),
		}, ck(ckState("allow")))
		d := gate(t, cmd("cat ./a.txt ./b.txt ./c.txt"), r).(*GateDescriptor)
		eq(t, d.Decision.Value, "./b.txt", "value")
		eq(t, d.PreCheck.State, "deny", "state")
		eq(t, len(r.calls), 2, "resolver calls stop at the first deny")
	})
	t.Run("an aliased shell tool's name reaches the payload, the prompt and the log", func(t *testing.T) {
		tcc := cmd("cat .env", func(c *ToolCallContext) { c.ToolName, c.AgentName = "exec_command", sp("agent-1") })
		n := bgNormalizer(goos(), tcc.Cwd)
		program, err := ParseBashProgram("cat .env", n, nil)
		if err != nil {
			t.Fatal(err)
		}
		bgMockHome(t, "/mock/home")
		resolver := makeResolver(ck(ckState("ask", "*")))
		d := DescribeBashPathGate(tcc, program, resolver, n).(*GateDescriptor)
		eq(t, resolver.calls[0].(AccessPathAccessIntent).AgentName, sp("agent-1"), "resolved with the agent")
		eq(t, d.Payload.Request.ToolName, sp("exec_command"), "payload")
		eq(t, d.Payload.Request.Requester.AgentName, sp("agent-1"), "payload agent")
		eq(t, d.PromptDetails.ToolName, sp("exec_command"), "prompt")
		eq(t, d.PromptDetails.AgentName, sp("agent-1"), "prompt agent")
		eq(t, d.PromptDetails.ToolCallID, sp("tc-1"), "prompt id")
		eq(t, d.PromptDetails.Source, PermissionReviewSource("tool_call"), "source")
		hasFields(t, d.LogContext, "toolName", "exec_command")
	})
}

func TestBashExternalDirectoryGateExtras(t *testing.T) {
	ck := func(state string, mut ...func(*PermissionCheckResult)) PermissionCheckResult {
		c := PermissionCheckResult{State: state, ToolName: "external_directory", Source: "special", Origin: "builtin"}
		for _, m := range mut {
			m(&c)
		}
		return c
	}
	t.Run("an aliased shell tool's name and the disclosure reach the payload, the prompt and the log", func(t *testing.T) {
		tcc := ToolCallContext{ToolName: "exec_command", Input: obj("command", "cat /outside/a.ts"), ToolCallID: "tc-9", Cwd: "/test/project", AgentName: sp("agent-1")}
		n := bgNormalizer(goos(), tcc.Cwd)
		program, err := ParseBashProgram("cat /outside/a.ts", n, nil)
		if err != nil {
			t.Fatal(err)
		}
		resolver := makeResolver(ck("ask", func(c *PermissionCheckResult) { c.MatchedPattern = sp("/outside/*") }))
		d := DescribeBashExternalDirectoryGate(tcc, program, resolver, n).(*GateDescriptor)
		eq(t, d.Payload.Request.ToolName, sp("exec_command"), "payload tool")
		eq(t, d.Payload.Request.Requester.AgentName, sp("agent-1"), "payload agent")
		eq(t, d.Payload.Request.MatchedPattern, sp("/outside/*"), "payload pattern")
		eq(t, d.PromptDetails.ToolName, sp("exec_command"), "prompt tool")
		eq(t, d.PromptDetails.ToolCallID, sp("tc-9"), "prompt id")
		eq(t, d.PromptDetails.AgentName, sp("agent-1"), "prompt agent")
		eq(t, jsonOf(t, d.LogContext), `{"source":"tool_call","toolCallId":"tc-9","toolName":"exec_command","agentName":"agent-1","command":"cat /outside/a.ts","externalPaths":["/outside/a.ts"],"effect":"read","effectSource":"core"}`, "log")
		eq(t, d.Decision, GateDecision{Surface: "external_directory_read", Value: "cat /outside/a.ts"}, "decision")
		eq(t, d.PreCheck.State, "ask", "precheck")
		eq(t, jsonOf(t, d.Input), `{}`, "input")
		eq(t, resolver.calls[0].(AccessPathAccessIntent).AgentName, sp("agent-1"), "resolved with the agent")
	})
	t.Run("the most restrictive path decides the surface, the facts and the precheck", func(t *testing.T) {
		tcc := ToolCallContext{ToolName: "bash", Input: obj("command", "cat /outside/a.ts > /elsewhere/b.ts"), ToolCallID: "tc-1", Cwd: "/test/project"}
		n := bgNormalizer(goos(), tcc.Cwd)
		program, _ := ParseBashProgram("cat /outside/a.ts > /elsewhere/b.ts", n, nil)
		r := makeResolver()
		r.fn = func(i AccessIntent) PermissionCheckResult {
			if it, ok := i.(AccessPathAccessIntent); ok && it.Path.Value() == "/elsewhere/b.ts" {
				return ck("deny")
			}
			return ck("ask")
		}
		d := DescribeBashExternalDirectoryGate(tcc, program, r, n).(*GateDescriptor)
		eq(t, d.PreCheck.State, "deny", "precheck")
		eq(t, d.Surface, "external_directory_write", "surface")
		eq(t, d.Decision.Surface, "external_directory_write", "decision")
		eq(t, d.PromptDetails.AccessIntent.Surface, "external_directory_write", "facts surface")
		eq(t, d.PromptDetails.AccessIntent.MatchValues, []string{"/elsewhere/b.ts"}, "facts values")
		hasFields(t, d.LogContext, "effect", "write")
	})
	t.Run("a path whose real location differs is disclosed with it", func(t *testing.T) {
		mockRealpath(t, func(p string) (string, error) {
			if p == "/outside/link" || len(p) > 13 && p[:14] == "/outside/link/" {
				return "/real" + p[len("/outside/link"):], nil
			}
			return p, nil
		})
		tcc := ToolCallContext{ToolName: "bash", Input: obj("command", "cat /outside/link/x.ts"), ToolCallID: "tc-1", Cwd: "/test/project"}
		n := NewPathNormalizer(PathFlavorForPlatform(goos()), tcc.Cwd)
		program, _ := ParseBashProgram("cat /outside/link/x.ts", n, nil)
		d := DescribeBashExternalDirectoryGate(tcc, program, makeResolver(ck("ask")), n).(*GateDescriptor)
		got := AllEvidence(d.Payload, "external path")
		eq(t, len(got), 1, "evidence")
		eq(t, got[0].Detail, sp("/real/x.ts"), "resolved target")
	})
	t.Run("a bypass logs the covered paths with their lexical values", func(t *testing.T) {
		tcc := ToolCallContext{ToolName: "bash", Input: obj("command", "cat /outside/a.ts"), ToolCallID: "tc-1", Cwd: "/test/project"}
		n := bgNormalizer(goos(), tcc.Cwd)
		program, _ := ParseBashProgram("cat /outside/a.ts", n, nil)
		b := DescribeBashExternalDirectoryGate(tcc, program, makeResolver(ck("allow", func(c *PermissionCheckResult) { c.Source = "session" })), n).(*GateBypass)
		eq(t, b.Decision == nil, true, "no decision event")
		eq(t, b.DecidedBy.Pattern == nil, true, "pattern")
	})
}

func TestBashCommandGateExtras(t *testing.T) {
	check := ResolveBashCommandCheck
	t.Run("the agent name reaches the whole-command check of a trivially empty command", func(t *testing.T) {
		r := makeResolver(bashResult("allow", "# c", "*"))
		check("# c", nil, sp("agent-x"), r)
		eq(t, r.calls, []AccessIntent{bashIntent("# c", nil, sp("agent-x"))}, "calls")
	})
	t.Run("a comment line followed by a command is not trivially empty", func(t *testing.T) {
		r := resolverFn(func(c string) PermissionCheckResult {
			if c == "rm x" {
				return bashResult("deny", c, "rm *")
			}
			return bashResult("allow", c, "*")
		})
		result := check("# note\nrm x", units("rm x"), nil, r)
		eq(t, result.State, "deny", "state")
	})
	t.Run("a command that begins with a blank line then a comment then a command is gated", func(t *testing.T) {
		r := resolverFn(func(c string) PermissionCheckResult {
			if c == "rm x" {
				return bashResult("ask", c, "rm *")
			}
			return bashResult("allow", c, "*")
		})
		eq(t, check("\n# note\nrm x", units("rm x"), nil, r).State, "ask", "state")
	})
	t.Run("a zero-unit command resolves its whole text with the agent and asks with the full synthetic result", func(t *testing.T) {
		r := makeResolver(bashResult("ask", "( rm x )", "*"))
		got := check("( rm x )", nil, sp("agent-x"), r)
		eq(t, r.calls, []AccessIntent{bashIntent("( rm x )", nil, sp("agent-x"))}, "calls")
		eq(t, got, PermissionCheckResult{State: "ask", ToolName: "bash", Source: "bash", Origin: "builtin", Command: sp("( rm x )"), MatchedPattern: sp("<unparseable-bash-command>")}, "result")
	})
	t.Run("an ask on the whole command does not replace the synthetic ask of a zero-unit command", func(t *testing.T) {
		got := check("( rm x )", nil, nil, makeResolver(bashResult("ask", "( rm x )", "rm *")))
		eq(t, got.MatchedPattern, sp("<unparseable-bash-command>"), "pattern")
	})
	t.Run("an exempt wrapper's inner command resolves with no spellings even when the unit has some", func(t *testing.T) {
		r := makeResolver(bashResult("allow", "grep -l foo", "*"))
		u := BashCommand{Text: "xargs grep -l foo", WrapperKind: "indirection", ExecutedUnit: "grep -l foo", FloorExemption: "core-reader", Spellings: []string{"alt"}}
		check("xargs grep -l foo", []BashCommand{u}, nil, r)
		eq(t, r.calls[len(r.calls)-1], AccessIntent(bashIntent("grep -l foo", nil, nil)), "inner call")
	})
}
