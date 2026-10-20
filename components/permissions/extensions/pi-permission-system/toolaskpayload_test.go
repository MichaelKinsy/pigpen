package pi_permission_system

import "testing"

// makePermissionCheckResult is test/helpers/presentation-fixtures.ts: the package's least-privilege ask.
func makePermissionCheckResult(toolName string, mod func(*PermissionCheckResult)) PermissionCheckResult {
	r := PermissionCheckResult{ToolName: toolName, State: "ask", Source: "tool", Origin: "builtin"}
	if mod != nil {
		mod(&r)
	}
	return r
}

// makeToolPreviewFormatter builds a ToolPreviewFormatter at the built-in preview limits, with an optional limit override and
// custom formatter lookup.
func makeToolPreviewFormatter(mod func(*ToolPreviewFormatterOptions), lookup ToolInputFormatterLookup) *ToolPreviewFormatter {
	options := ToolPreviewFormatterOptions{ToolInputPreviewMaxLength: ToolInputPreviewMaxLength, ToolTextSummaryMaxLength: ToolTextSummaryMaxLength}
	if mod != nil {
		mod(&options)
	}
	return NewToolPreviewFormatter(options, lookup)
}

// lookupFunc is a ToolInputFormatterLookup over a function.
type lookupFunc func(name string) ToolInputFormatter

func (l lookupFunc) Get(name string) ToolInputFormatter { return l(name) }

// constFormatter is `() => result`: a formatter that ignores its input; ok false is `undefined`.
func constFormatter(result string, ok bool) ToolInputFormatter {
	return NewToolInputFormatter(func(*jsObject) (string, bool) { return result, ok })
}

func TestToolAskPayloadTwins(t *testing.T) {
	const f = "presentation/tool-ask-payload"
	toolResult := func(toolName string, mod func(*PermissionCheckResult)) PermissionCheckResult {
		return makePermissionCheckResult(toolName, mod)
	}
	mcpResult := func(target string, mod func(*PermissionCheckResult)) PermissionCheckResult {
		return makePermissionCheckResult("mcp", func(r *PermissionCheckResult) {
			r.Target = &target
			if mod != nil {
				mod(r)
			}
		})
	}
	// buildPayload builds a payload the way the per-tool gate does, defaulting the surface to the tool name.
	buildPayload := func(facts ToolAskFacts) PromptPayload {
		if facts.Surface == "" {
			facts.Surface = facts.Check.ToolName
		}
		return BuildToolAskPayload(facts)
	}
	formatter := func(lookup ToolInputFormatterLookup) *ToolPreviewFormatter {
		return makeToolPreviewFormatter(nil, lookup)
	}
	mcpLookup := func(preview string) ToolInputFormatterLookup {
		return lookupFunc(func(name string) ToolInputFormatter {
			if name == "mcp" {
				return constFormatter(preview, true)
			}
			return nil
		})
	}
	input := func(json string) any { return jo(t, json) }

	tw(t, f, "carries the gate surface, matched rule, and offending command", func(t *testing.T) {
		payload := buildPayload(ToolAskFacts{
			Check: toolResult("bash", func(r *PermissionCheckResult) {
				r.Command, r.MatchedPattern, r.CommandContext = sp("rm -rf foo"), sp("rm *"), BashContextCommandSubstitution
			}),
			Surface: "bash",
		})
		eq(t, payload.Kind, "bash", "kind")
		eq(t, payload.Request, PromptRequestFacts{
			Surface: "bash", ToolName: sp("bash"), Value: "rm -rf foo", MatchedPattern: sp("rm *"), CommandContext: sp("command_substitution"),
		}, "request")
	})
	tw(t, f, "carries the command unmasked, because the approver must see what runs", func(t *testing.T) {
		// The log masks a value bound to a sensitive name; the prompt never does. Masking here would blind the person being asked to
		// authorize the call.
		command := `KEY="sk-secret-value" curl https://x`
		payload := buildPayload(ToolAskFacts{Check: toolResult("bash", func(r *PermissionCheckResult) { r.Command = &command }), Surface: "bash"})
		eq(t, payload.Request.Value, command, "value")
	})
	tw(t, f, "carries the executed unit of a wrapper (#713)", func(t *testing.T) {
		payload := buildPayload(ToolAskFacts{
			Check: toolResult("bash", func(r *PermissionCheckResult) {
				r.Command, r.MatchedPattern, r.ExecutedUnit = sp("xargs grep foo"), sp("<indirection-bash-wrapper>"), sp("grep foo")
			}),
			Surface: "bash",
		})
		eq(t, payload.Request.ExecutedUnit, sp("grep foo"), "unit")
	})
	tw(t, f, "names the invoked tool when a shell alias re-exposes bash (#574)", func(t *testing.T) {
		payload := buildPayload(ToolAskFacts{
			Check: toolResult("bash", func(r *PermissionCheckResult) { r.Command = sp("ls") }), Surface: "bash", InvokedToolName: sp("exec_command"),
		})
		eq(t, payload.Request.ToolName, sp("bash"), "tool")
		eq(t, payload.Request.InvokedToolName, sp("exec_command"), "invoked")
	})
	tw(t, f, "omits the invoked tool when it repeats the gated one", func(t *testing.T) {
		payload := buildPayload(ToolAskFacts{Check: toolResult("read", nil), InvokedToolName: sp("read")})
		eq(t, payload.Request.InvokedToolName == nil, true, "invoked")
	})
	tw(t, f, "leaves the value empty for a bash check with no resolved command", func(t *testing.T) {
		eq(t, buildPayload(ToolAskFacts{Check: toolResult("bash", nil)}).Request.Value, "", "value")
	})
	tw(t, f, "lands the annotations slot empty", func(t *testing.T) {
		eq(t, buildPayload(ToolAskFacts{Check: toolResult("read", nil)}).Annotations, []PromptAnnotation{}, "annotations")
	})

	tw(t, f, "names the agent when one is known", func(t *testing.T) {
		got := buildPayload(ToolAskFacts{Check: toolResult("read", nil), AgentName: sp("my-agent"), Input: input(`{"path":"/src"}`), Formatter: formatter(nil)})
		eq(t, got.Request.Requester.AgentName, sp("my-agent"), "agent")
	})
	tw(t, f, "carries the enclosing command when the gated unit is only part of it", func(t *testing.T) {
		got := buildPayload(ToolAskFacts{
			Check: toolResult("bash", func(r *PermissionCheckResult) { r.Command = sp("rm -rf .") }), Input: input(`{"command":"echo \"hello\" && rm -rf ."}`), Formatter: formatter(nil),
		})
		eq(t, FindEvidence(got, "full command"), &PromptEvidence{Label: "full command", Text: `echo "hello" && rm -rf .`}, "evidence")
	})
	tw(t, f, "omits the enclosing command when it is the gated unit", func(t *testing.T) {
		got := buildPayload(ToolAskFacts{
			Check: toolResult("bash", func(r *PermissionCheckResult) { r.Command = sp("git push") }), Input: input(`{"command":"git push"}`), Formatter: formatter(nil),
		})
		eq(t, FindEvidence(got, "full command") == nil, true, "evidence")
	})
	// test.each: the parametrized title is not in the upstream ledger.
	for _, c := range []struct {
		name  string
		input any
	}{{"input is undefined", nil}, {"input has no command field", input(`{"unrelated":"value"}`)}, {"input command is empty", input(`{"command":""}`)}} {
		t.Run("omits the enclosing command when "+c.name, func(t *testing.T) {
			got := buildPayload(ToolAskFacts{
				Check: toolResult("bash", func(r *PermissionCheckResult) { r.Command = sp("git push") }), Input: c.input, Formatter: formatter(nil),
			})
			eq(t, FindEvidence(got, "full command") == nil, true, "evidence")
		})
	}
	tw(t, f, "adds no input preview, since the command is the value", func(t *testing.T) {
		got := buildPayload(ToolAskFacts{
			Check: toolResult("bash", func(r *PermissionCheckResult) { r.Command = sp("git status") }), Input: input(`{"command":"git status"}`), Formatter: formatter(nil),
		})
		eq(t, FindEvidence(got, "input") == nil, true, "evidence")
	})

	tw(t, f, "makes the target the decision-relevant value", func(t *testing.T) {
		payload := buildPayload(ToolAskFacts{Check: mcpResult("server:query", func(r *PermissionCheckResult) { r.MatchedPattern = sp("server:*") }), Formatter: formatter(nil)})
		eq(t, payload.Kind, "mcp", "kind")
		eq(t, payload.Request.Value, "server:query", "value")
		eq(t, payload.Request.MatchedPattern, sp("server:*"), "pattern")
	})
	tw(t, f, "carries the argument summary a registered formatter produced", func(t *testing.T) {
		got := buildPayload(ToolAskFacts{
			Check: mcpResult("exa:search", nil), Input: input(`{"tool":"exa:search","arguments":{"query":"typescript"}}`), Formatter: formatter(mcpLookup(`with query: "typescript"`)),
		})
		eq(t, FindEvidence(got, "input"), &PromptEvidence{Label: "input", Text: `with query: "typescript"`}, "evidence")
	})
	tw(t, f, "carries no evidence when the registered formatter declines", func(t *testing.T) {
		noArgs := lookupFunc(func(name string) ToolInputFormatter {
			if name == "mcp" {
				return constFormatter("", false)
			}
			return nil
		})
		got := buildPayload(ToolAskFacts{Check: mcpResult("exa:search", nil), Input: input(`{"tool":"exa:search"}`), Formatter: formatter(noArgs)})
		eq(t, got.Evidence, []PromptEvidence{}, "evidence")
	})
	tw(t, f, "carries no evidence when no formatter is provided", func(t *testing.T) {
		mcp := buildPayload(ToolAskFacts{Check: mcpResult("exa:search", nil), Input: input(`{"tool":"exa:search","arguments":{"query":"test"}}`)})
		eq(t, mcp.Evidence, []PromptEvidence{}, "mcp")
	})
	tw(t, f, "carries no evidence when no formatter is provided", func(t *testing.T) {
		generic := buildPayload(ToolAskFacts{Check: toolResult("task", nil), Input: input(`{"path":"/src"}`)})
		eq(t, generic.Evidence, []PromptEvidence{}, "generic")
	})

	// A Pi MCP tool (mcp__<server>__<tool>)
	for _, source := range []string{"mcp", "default"} {
		// test.each: the parametrized title is not in the upstream ledger.
		t.Run("is an mcp ask valued by its target, whose input is the MCP arguments (source "+source+")", func(t *testing.T) {
			payload := buildPayload(ToolAskFacts{
				Check: makePermissionCheckResult("mcp__danger_srv__wipe", func(r *PermissionCheckResult) { r.Source, r.Target = source, sp("danger-srv") }),
				Input: input(`{"target":"prod"}`), Formatter: formatter(nil),
			})
			eq(t, payload.Kind, "mcp", "kind")
			eq(t, payload.Request.Value, "danger-srv", "value")
			eq(t, payload.Evidence, []PromptEvidence{{Label: "input", Text: `with input {"target":"prod"}`}}, "evidence")
		})
	}

	tw(t, f, "carries the real input preview as evidence", func(t *testing.T) {
		got := buildPayload(ToolAskFacts{Check: toolResult("read", nil), Input: input(`{"path":"/src/foo.ts"}`), Formatter: formatter(nil)})
		evidence := FindEvidence(got, "input")
		if evidence == nil || evidence.Text != "for path '/src/foo.ts'" {
			t.Fatalf("evidence: %#v", evidence)
		}
	})
	tw(t, f, "carries no evidence when the formatter produces nothing", func(t *testing.T) {
		got := buildPayload(ToolAskFacts{Check: toolResult("task", nil), Input: input(`{}`), Formatter: formatter(nil)})
		eq(t, got.Evidence, []PromptEvidence{}, "evidence")
	})
}
