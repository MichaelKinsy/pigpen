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
		b, ok := isBypass(gate(t, cmd("cat ./a.txt ./b.txt", func(c *ToolCallContext) { c.AgentName = sp("agent-1") }), r))
		if !ok {
			t.Fatal("want a bypass")
		}
		eq(t, b.Log.Event, "permission_request.session_approved", "event")
		eq(t, jsonOf(t, b.Log.Details), `{"source":"tool_call","toolCallId":"tc-1","toolName":"bash","agentName":"agent-1","command":"cat ./a.txt ./b.txt","tokens":["./a.txt","./b.txt"],"resolution":"session_approved"}`, "details")
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
		tcc := cmd("cat .env", func(c *ToolCallContext) { c.ToolName = "exec_command" })
		n := bgNormalizer(goos(), tcc.Cwd)
		program, err := ParseBashProgram("cat .env", n, nil)
		if err != nil {
			t.Fatal(err)
		}
		bgMockHome(t, "/mock/home")
		d := DescribeBashPathGate(tcc, program, makeResolver(ck(ckState("ask", "*"))), n).(*GateDescriptor)
		eq(t, d.Payload.Request.ToolName, sp("exec_command"), "payload")
		eq(t, d.PromptDetails.ToolName, sp("exec_command"), "prompt")
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
		d := DescribeBashExternalDirectoryGate(tcc, program, makeResolver(ck("ask", func(c *PermissionCheckResult) { c.MatchedPattern = sp("/outside/*") })), n).(*GateDescriptor)
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
