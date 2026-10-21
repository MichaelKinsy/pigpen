package pi_permission_system

import "testing"

// Twins of test/handlers/gates/tool.test.ts.

func TestToolGateTwins(t *testing.T) {
	const f = "handlers/gates/tool"
	formatter := func() *ToolPreviewFormatter {
		return NewToolPreviewFormatter(ToolPreviewFormatterOptions{ToolInputPreviewMaxLength: ToolInputPreviewMaxLength, ToolTextSummaryMaxLength: ToolTextSummaryMaxLength}, nil)
	}
	// The gate tests' own tcc: read-tool defaults with an empty input.
	tcc := func(mut ...func(*ToolCallContext)) ToolCallContext {
		return ToolCallContext{ToolName: "read", Input: newObject(), ToolCallID: "tc-1", Cwd: "/test/project", AgentName: nil}.with(mut...)
	}
	// The gate tests' own check: the state, tool "read", pattern "*".
	check := func(state string, mut ...func(*PermissionCheckResult)) PermissionCheckResult {
		return makeCheckResult(append([]func(*PermissionCheckResult){ckState(state, "*")}, mut...)...)
	}
	set := func(toolName string, extra ...func(*PermissionCheckResult)) []func(*PermissionCheckResult) {
		return append([]func(*PermissionCheckResult){func(c *PermissionCheckResult) { c.ToolName = toolName }}, extra...)
	}
	posix := NewPathNormalizer(PosixPathFlavor, "/test/project")
	pathAccessFor := func(pathValue string, n *PathNormalizer) *ToolPathAccess {
		p := n.ForPath(pathValue, nil)
		return &ToolPathAccess{Path: p, ApprovalPatterns: n.ApprovalPatternsFor(p)}
	}
	tool := func(name string, input *jsObject) func(*ToolCallContext) {
		return func(c *ToolCallContext) { c.ToolName, c.Input = name, input }
	}

	tw(t, f, "returns descriptor with tool name as surface for standard tools", func(t *testing.T) {
		d := DescribeToolGate(tcc(tool("read", newObject())), check("ask"), formatter(), nil, nil)
		eq(t, d.Surface, "read", "surface")
		eq(t, d.Decision.Surface, "read", "decision surface")
	})
	tw(t, f, "returns descriptor with tool name as decision value for standard tools", func(t *testing.T) {
		eq(t, DescribeToolGate(tcc(tool("write", newObject())), check("ask"), formatter(), nil, nil).Decision.Value, "write", "value")
	})
	tw(t, f, "returns bash surface with command in decision.value for bash tools", func(t *testing.T) {
		c := check("ask", func(c *PermissionCheckResult) { c.ToolName, c.Command = "bash", sp("git status") })
		d := DescribeToolGate(tcc(tool("bash", obj("command", "git status"))), c, formatter(), nil, nil)
		eq(t, d.Surface, "bash", "surface")
		eq(t, d.Decision.Surface, "bash", "decision surface")
		eq(t, d.Decision.Value, "git status", "value")
	})
	tw(t, f, "gates an aliased shell tool on the bash surface while keeping its tool name in logs (#574)", func(t *testing.T) {
		shell := &ShellInvocation{Command: "npm install"}
		c := check("ask", func(c *PermissionCheckResult) { c.ToolName, c.Source, c.Command = "bash", "bash", sp("npm install") })
		d := DescribeToolGate(tcc(tool("exec_command", obj("cmd", "npm install"))), c, formatter(), nil, shell)
		// Gated as bash: decision, surface, and session rule are bash-shaped.
		eq(t, d.Surface, "bash", "surface")
		eq(t, d.Decision.Surface, "bash", "decision surface")
		eq(t, d.Decision.Value, "npm install", "value")
		eq(t, d.SessionApproval.Grants[0].Surface, "bash", "approval surface")
		eq(t, grantPatterns(d.SessionApproval), []string{"npm install*"}, "patterns")
		// The invoked tool name is preserved for the review log and prompt.
		eq(t, d.LogContext.vals["toolName"], any("exec_command"), "log toolName")
		eq(t, d.PromptDetails.ToolName, sp("exec_command"), "prompt toolName")
		// "Gated as bash, invoked as exec_command" is two facts, and the payload records both rather than collapsing them.
		eq(t, d.Payload.Kind, "bash", "kind")
		eq(t, d.Payload.Request.ToolName, sp("bash"), "toolName")
		eq(t, d.Payload.Request.InvokedToolName, sp("exec_command"), "invokedToolName")
	})
	tw(t, f, "returns mcp surface with target in decision.value for MCP tools", func(t *testing.T) {
		c := check("ask", func(c *PermissionCheckResult) { c.ToolName, c.Target = "mcp", sp("server:tool") })
		d := DescribeToolGate(tcc(tool("mcp", obj("tool", "server:tool"))), c, formatter(), nil, nil)
		eq(t, d.Surface, "mcp", "surface")
		eq(t, d.Decision.Surface, "mcp", "decision surface")
		eq(t, d.Decision.Value, "server:tool", "value")
	})

	t.Run("a Pi MCP tool (mcp__<server>__<tool>)", func(t *testing.T) {
		const toolName = "mcp__danger_srv__wipe"
		c := check("ask", func(c *PermissionCheckResult) {
			c.ToolName, c.Source, c.Target, c.MatchedPattern = toolName, "mcp", sp("danger-srv"), sp("danger-srv")
		})
		describeMcpTool := func() *GateDescriptor {
			return DescribeToolGate(tcc(tool(toolName, obj("target", "prod"))), c, formatter(), nil, nil)
		}
		tw(t, f, "gates on the mcp surface with the target as the decision value", func(t *testing.T) {
			d := describeMcpTool()
			eq(t, d.Surface, "mcp", "surface")
			eq(t, d.Decision, GateDecision{Surface: "mcp", Value: "danger-srv"}, "decision")
			eq(t, *d.PromptDetails.AccessIntent, ForwardedAccessFacts{Surface: "mcp", MatchValues: []string{"danger-srv"}}, "accessIntent")
		})
		tw(t, f, "approves exactly this tool for the session, on the mcp surface", func(t *testing.T) {
			eq(t, describeMcpTool().SessionApproval.Grants, []ApprovalGrant{{Surface: "mcp", Pattern: toolName}}, "grants")
		})
		tw(t, f, "keeps the invoked tool name and shows its arguments", func(t *testing.T) {
			d := describeMcpTool()
			eq(t, d.LogContext.vals["toolName"], any(toolName), "log toolName")
			eq(t, d.LogContext.vals["toolInputPreview"], any(`input {"target":"prod"}`), "preview")
			eq(t, d.Payload.Kind, "mcp", "kind")
			eq(t, d.Payload.Request.Value, "danger-srv", "value")
			eq(t, d.Payload.Evidence, []PromptEvidence{{Label: "input", Text: `with input {"target":"prod"}`}}, "evidence")
		})
	})

	tw(t, f, "carries the checked tool and its matched rule on the payload", func(t *testing.T) {
		c := check("deny", func(c *PermissionCheckResult) { c.ToolName, c.MatchedPattern = "read", sp("re*") })
		d := DescribeToolGate(tcc(), c, formatter(), nil, nil)
		eq(t, d.Payload.Kind, "tool", "kind")
		eq(t, d.Payload.Request.ToolName, sp("read"), "toolName")
		eq(t, d.Payload.Request.MatchedPattern, sp("re*"), "matchedPattern")
		eq(t, d.Payload.Request.Requester.AgentName, (*string)(nil), "agent")
	})
	tw(t, f, "names the requesting agent on the payload when provided", func(t *testing.T) {
		d := DescribeToolGate(tcc(func(c *ToolCallContext) { c.AgentName = sp("my-agent") }), check("ask"), formatter(), nil, nil)
		eq(t, d.Payload.Request.Requester.AgentName, sp("my-agent"), "agent")
	})
	tw(t, f, "carries the command as the decision-relevant value for a bash ask", func(t *testing.T) {
		c := check("ask", func(c *PermissionCheckResult) { c.ToolName, c.Command = "bash", sp("ls") })
		d := DescribeToolGate(tcc(tool("bash", obj("command", "ls"))), c, formatter(), nil, nil)
		eq(t, d.Payload.Kind, "bash", "kind")
		eq(t, d.Payload.Request.Value, "ls", "value")
	})
	tw(t, f, "populates sessionApproval via suggestSessionPattern", func(t *testing.T) {
		c := check("ask", func(c *PermissionCheckResult) { c.ToolName, c.Command = "bash", sp("git status") })
		d := DescribeToolGate(tcc(tool("bash", obj("command", "git status"))), c, formatter(), nil, nil)
		eq(t, d.SessionApproval != nil, true, "defined")
		eq(t, d.SessionApproval.Grants[0].Surface, "bash", "surface")
		eq(t, len(d.SessionApproval.Grants), 1, "grants")
	})
	tw(t, f, "binds a current-directory file's session approval to the cwd subtree", func(t *testing.T) {
		d := DescribeToolGate(tcc(tool("edit", obj("path", "index.html"))), check("ask", set("edit")...), formatter(), pathAccessFor("index.html", posix), nil)
		eq(t, d.SessionApproval.Grants[0].Surface, "edit", "surface")
		eq(t, grantPatterns(d.SessionApproval), []string{"/test/project/*"}, "patterns")
	})
	tw(t, f, "resolves a sub-directory file's session approval to an absolute pattern", func(t *testing.T) {
		// The approval value derives from the AccessPath's lexical absolute form, so sub-directory approvals are absolute too: the
		// deliberate tradeoff that keeps the pattern aligned with the policy values it is matched against.
		d := DescribeToolGate(tcc(tool("edit", obj("path", "src/foo.ts"))), check("ask", set("edit")...), formatter(), pathAccessFor("src/foo.ts", posix), nil)
		eq(t, grantPatterns(d.SessionApproval), []string{"/test/project/src/*"}, "patterns")
	})
	tw(t, f, "falls back to a wildcard session approval when no AccessPath is given", func(t *testing.T) {
		// A path-bearing tool with no `input.path` keeps the `tool` intent and gets no AccessPath, so the suggestion collapses to the
		// catch-all.
		d := DescribeToolGate(tcc(tool("read", newObject())), check("ask"), formatter(), nil, nil)
		eq(t, d.SessionApproval.Grants[0].Surface, "read", "surface")
		eq(t, grantPatterns(d.SessionApproval), []string{"*"}, "patterns")
	})
	tw(t, f, "populates promptDetails with correct fields", func(t *testing.T) {
		d := DescribeToolGate(tcc(tool("read", newObject()), func(c *ToolCallContext) { c.AgentName, c.ToolCallID = sp("my-agent"), "tc-42" }), check("ask"), formatter(), nil, nil)
		eq(t, [4]any{d.PromptDetails.Source, d.PromptDetails.AgentName, d.PromptDetails.ToolCallID, d.PromptDetails.ToolName},
			[4]any{"tool_call", sp("my-agent"), sp("tc-42"), sp("read")}, "details")
		eq(t, d.PromptDetails.SessionLabel != nil, true, "sessionLabel")
	})
	tw(t, f, "carries the AccessPath's facts on promptDetails for a path-bearing tool", func(t *testing.T) {
		pa := pathAccessFor("src/foo.ts", posix)
		d := DescribeToolGate(tcc(tool("edit", obj("path", "src/foo.ts"))), check("ask", set("edit")...), formatter(), pa, nil)
		boundary := pa.Path.BoundaryValue()
		eq(t, *d.PromptDetails.AccessIntent, ForwardedAccessFacts{Surface: "edit", MatchValues: pa.Path.MatchValues(), BoundaryValue: &boundary}, "accessIntent")
	})
	tw(t, f, "records the approval pattern the pipeline derived", func(t *testing.T) {
		d := DescribeToolGate(tcc(tool("edit", obj("path", "src/foo.ts"))), check("ask", set("edit")...), formatter(), pathAccessFor("src/foo.ts", posix), nil)
		eq(t, grantPatterns(d.SessionApproval), []string{"/test/project/src/*"}, "patterns")
		eq(t, d.PromptDetails.SessionLabel, sp(`Yes, allow edit "/test/project/src/*" for this session`), "label")
	})
	tw(t, f, "derives the approval through the injected flavor, not the host", func(t *testing.T) {
		// A native Windows path carries backslash separators the host POSIX path cannot see, so an ambient derivation collapses it to
		// `./*` and the recorded grant matches nothing (#655).
		mockRealpath(t, identityRealpath)
		win := NewPathNormalizer(Win32PathFlavor, `C:\Projects\App`)
		d := DescribeToolGate(tcc(tool("edit", obj("path", `src\foo.ts`)), func(c *ToolCallContext) { c.Cwd = `C:\Projects\App` }),
			check("ask", set("edit")...), formatter(), pathAccessFor(`src\foo.ts`, win), nil)
		eq(t, grantPatterns(d.SessionApproval), []string{`c:\projects\app\src\*`}, "patterns")
	})
	tw(t, f, "carries the single decision value on promptDetails for a non-path tool (bash)", func(t *testing.T) {
		c := check("ask", func(c *PermissionCheckResult) { c.ToolName, c.Command = "bash", sp("git status") })
		d := DescribeToolGate(tcc(tool("bash", obj("command", "git status"))), c, formatter(), nil, nil)
		eq(t, *d.PromptDetails.AccessIntent, ForwardedAccessFacts{Surface: "bash", MatchValues: []string{"git status"}}, "accessIntent")
	})
	tw(t, f, "populates logContext with tool input preview fields", func(t *testing.T) {
		c := check("ask", func(c *PermissionCheckResult) { c.ToolName, c.Command = "bash", sp("ls") })
		d := DescribeToolGate(tcc(tool("bash", obj("command", "ls"))), c, formatter(), nil, nil)
		hasFields(t, d.LogContext, "source", "tool_call", "toolName", "bash")
		eq(t, d.LogContext.vals["command"], any("ls"), "command")
	})

	t.Run("wrapper floor exemption in the review log (#803)", func(t *testing.T) {
		bashGate := func(c PermissionCheckResult) *GateDescriptor {
			return DescribeToolGate(tcc(tool("bash", obj("command", *c.Command))), c, formatter(), nil, nil)
		}
		tw(t, f, "records why an exempt wrapper was let through", func(t *testing.T) {
			d := bashGate(check("allow", func(c *PermissionCheckResult) {
				c.ToolName, c.Source, c.Command, c.MatchedPattern = "bash", "bash", sp("xargs grep -l foo"), sp("grep *")
				c.ExecutedUnit, c.FloorExemption = sp("grep -l foo"), FloorExemptionCoreReader
			}))
			eq(t, d.LogContext.vals["floorExemption"], any("core-reader"), "floorExemption")
		})
		tw(t, f, "omits the fact for a decision no exemption produced", func(t *testing.T) {
			d := bashGate(check("ask", func(c *PermissionCheckResult) {
				c.ToolName, c.Source, c.Command, c.MatchedPattern = "bash", "bash", sp("xargs rm -rf"), sp("<indirection-bash-wrapper>")
			}))
			_, present := d.LogContext.vals["floorExemption"]
			eq(t, present, false, "present")
		})
	})

	tw(t, f, "uses toolName as input for checkPermission surface", func(t *testing.T) {
		d := DescribeToolGate(tcc(tool("edit", obj("path", "/a.ts"))), check("ask", set("edit")...), formatter(), pathAccessFor("/a.ts", posix), nil)
		eq(t, d.Surface, "edit", "surface")
		eq(t, jsonOf(t, d.Input), `{"path":"/a.ts"}`, "input")
	})
}
