package pi_permission_system

import (
	"strings"
	"testing"
)

// Twins of test/handlers/gates/{skill-input,skill-input-gate-pipeline,skill-read}.test.ts.

func makeSkillCheck(state string) PermissionCheckResult {
	return makeCheckResult(ckState(state, "*"), func(c *PermissionCheckResult) { c.ToolName, c.Source, c.Origin = "skill", "skill", "global" })
}

func TestSkillInputGateTwins(t *testing.T) {
	const f = "handlers/gates/skill-input"
	tw(t, f, "sets surface to 'skill'", func(t *testing.T) {
		eq(t, DescribeSkillInputGate("librarian", nil, makeSkillCheck("allow")).Surface, "skill", "surface")
	})
	tw(t, f, "sets input.name to the skill name", func(t *testing.T) {
		eq(t, jsonOf(t, DescribeSkillInputGate("librarian", nil, makeSkillCheck("allow")).Input), `{"name":"librarian"}`, "input")
	})
	tw(t, f, "passes preCheck through verbatim", func(t *testing.T) {
		check := makeSkillCheck("deny")
		eq(t, *DescribeSkillInputGate("librarian", nil, check).PreCheck, check, "preCheck")
	})
	tw(t, f, "makes the skill the payload's decision-relevant value", func(t *testing.T) {
		d := DescribeSkillInputGate("librarian", nil, makeSkillCheck("allow"))
		eq(t, d.Payload.Kind, "skill", "kind")
		eq(t, d.Payload.Request.Surface, "skill", "surface")
		eq(t, d.Payload.Request.Value, "librarian", "value")
		eq(t, d.Payload.Request.Requester.AgentName, (*string)(nil), "agent")
	})
	tw(t, f, "names the requesting agent on the payload when provided", func(t *testing.T) {
		d := DescribeSkillInputGate("librarian", sp("code-agent"), makeSkillCheck("allow"))
		eq(t, d.Payload.Request.Requester.AgentName, sp("code-agent"), "agent")
	})
	tw(t, f, "sets promptDetails source to 'skill_input' with skill name and agent", func(t *testing.T) {
		d := DescribeSkillInputGate("librarian", sp("code-agent"), makeSkillCheck("ask"))
		eq(t, [3]any{d.PromptDetails.Source, d.PromptDetails.AgentName, d.PromptDetails.SkillName}, [3]any{"skill_input", sp("code-agent"), sp("librarian")}, "details")
	})
	tw(t, f, "emits a skill payload naming the skill as the decision value", func(t *testing.T) {
		d := DescribeSkillInputGate("librarian", sp("code-agent"), makeSkillCheck("ask"))
		eq(t, d.Payload.Kind, "skill", "kind")
		eq(t, d.Payload.Request.Value, "librarian", "value")
	})
	tw(t, f, "names the skill in promptDetails so the prompt can identify it", func(t *testing.T) {
		eq(t, DescribeSkillInputGate("librarian", nil, makeSkillCheck("ask")).PromptDetails.SkillName, sp("librarian"), "skillName")
	})
	tw(t, f, "sets logContext source to 'skill_input' with skill name and agent", func(t *testing.T) {
		d := DescribeSkillInputGate("librarian", sp("code-agent"), makeSkillCheck("allow"))
		hasFields(t, d.LogContext, "source", "skill_input", "skillName", "librarian", "agentName", "code-agent")
	})
	tw(t, f, "sets decision surface to 'skill' and value to the skill name", func(t *testing.T) {
		eq(t, DescribeSkillInputGate("my-skill", nil, makeSkillCheck("allow")).Decision, GateDecision{Surface: "skill", Value: "my-skill"}, "decision")
	})
	tw(t, f, "carries the skill name as single-value access facts on promptDetails", func(t *testing.T) {
		d := DescribeSkillInputGate("my-skill", nil, makeSkillCheck("ask"))
		eq(t, *d.PromptDetails.AccessIntent, ForwardedAccessFacts{Surface: "skill", MatchValues: []string{"my-skill"}}, "accessIntent")
	})
	tw(t, f, "does not set preResolved or sessionApproval", func(t *testing.T) {
		d := DescribeSkillInputGate("librarian", nil, makeSkillCheck("allow"))
		eq(t, d.PreResolved == nil && d.SessionApproval == nil, true, "unset")
	})
}

// gfSkillInputs is makeSkillInputInputs: a recording checkPermission.
type gfSkillInputs struct {
	fn    func() PermissionCheckResult
	calls []struct {
		surface string
		input   any
		agent   *string
	}
}

func (s *gfSkillInputs) CheckPermission(surface string, input any, agentName *string) PermissionCheckResult {
	s.calls = append(s.calls, struct {
		surface string
		input   any
		agent   *string
	}{surface, input, agentName})
	return s.fn()
}

func makeSkillInputInputs(check ...PermissionCheckResult) *gfSkillInputs {
	c := makeCheckResult()
	if len(check) > 0 {
		c = check[0]
	}
	return &gfSkillInputs{fn: func() PermissionCheckResult { return c }}
}

type gfNotifier struct{ warnings []string }

func (n *gfNotifier) Warn(message string) { n.warnings = append(n.warnings, message) }

func TestSkillInputGatePipelineTwins(t *testing.T) {
	const f = "handlers/gates/skill-input-gate-pipeline"
	evaluate := func(t *testing.T, inputs *gfSkillInputs, agent *string, notifier *gfNotifier) GateOutcome {
		t.Helper()
		runner, _ := makeGateRunner()
		out, err := NewSkillInputGatePipeline(inputs).Evaluate("librarian", agent, notifier, runner)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	tw(t, f, "includes the skill name in the message (no agent)", func(t *testing.T) {
		if !strings.Contains(FormatSkillDenyNotice("librarian", nil), "librarian") {
			t.Error("notice lacks the skill")
		}
	})
	tw(t, f, "includes the skill name and agent name when agent is present", func(t *testing.T) {
		msg := FormatSkillDenyNotice("librarian", sp("code-agent"))
		if !strings.Contains(msg, "librarian") || !strings.Contains(msg, "code-agent") {
			t.Errorf("notice %q", msg)
		}
	})
	tw(t, f, "calls notifier.warn when the skill is denied", func(t *testing.T) {
		n := &gfNotifier{}
		evaluate(t, makeSkillInputInputs(makeCheckResult(ckState("deny"))), nil, n)
		if len(n.warnings) != 1 || !strings.Contains(n.warnings[0], "librarian") {
			t.Errorf("warnings %v", n.warnings)
		}
	})
	tw(t, f, "does not call notifier.warn when the skill is allowed", func(t *testing.T) {
		n := &gfNotifier{}
		evaluate(t, makeSkillInputInputs(makeCheckResult(ckState("allow"))), nil, n)
		eq(t, len(n.warnings), 0, "warnings")
	})
	tw(t, f, "does not call notifier.warn when the skill requires approval (ask)", func(t *testing.T) {
		n := &gfNotifier{}
		evaluate(t, makeSkillInputInputs(makeCheckResult(ckState("ask"))), nil, n)
		eq(t, len(n.warnings), 0, "warnings")
	})
	tw(t, f, "includes agent name in the deny notice when agent is present", func(t *testing.T) {
		n := &gfNotifier{}
		evaluate(t, makeSkillInputInputs(makeCheckResult(ckState("deny"))), sp("code-agent"), n)
		if len(n.warnings) != 1 || !strings.Contains(n.warnings[0], "code-agent") {
			t.Errorf("warnings %v", n.warnings)
		}
	})
	tw(t, f, "returns allow when the gate passes", func(t *testing.T) {
		eq(t, evaluate(t, makeSkillInputInputs(makeCheckResult(ckState("allow"))), nil, &gfNotifier{}), gfAllow, "outcome")
	})
	tw(t, f, "returns block when the gate denies", func(t *testing.T) {
		out := evaluate(t, makeSkillInputInputs(makeCheckResult(ckState("deny", "*"))), nil, &gfNotifier{})
		if out.Action != "block" || !strings.Contains(out.Reason, "librarian") {
			t.Errorf("outcome %+v", out)
		}
	})
	tw(t, f, "calls checkPermission with the skill surface, skill name, and agent name", func(t *testing.T) {
		inputs := makeSkillInputInputs()
		runner, _ := makeGateRunner()
		if _, err := NewSkillInputGatePipeline(inputs).Evaluate("explorer", sp("code-agent"), &gfNotifier{}, runner); err != nil {
			t.Fatal(err)
		}
		eq(t, len(inputs.calls), 1, "calls")
		eq(t, inputs.calls[0].surface, "skill", "surface")
		eq(t, jsonOf(t, inputs.calls[0].input), `{"name":"explorer"}`, "input")
		eq(t, inputs.calls[0].agent, sp("code-agent"), "agent")
	})
	tw(t, f, "calls checkPermission with undefined agentName when agentName is null", func(t *testing.T) {
		inputs := makeSkillInputInputs()
		runner, _ := makeGateRunner()
		if _, err := NewSkillInputGatePipeline(inputs).Evaluate("explorer", nil, &gfNotifier{}, runner); err != nil {
			t.Fatal(err)
		}
		eq(t, inputs.calls[0].agent, (*string)(nil), "agent")
	})
}

func TestSkillReadGateTwins(t *testing.T) {
	const f = "handlers/gates/skill-read"
	normalizer := NewPathNormalizer(PosixPathFlavor, "/test/project")
	entry := func(mut ...func(*SkillPromptEntry)) SkillPromptEntry {
		e := SkillPromptEntry{Name: "librarian", Description: "Research skills", Location: "/skills/librarian/SKILL.md", State: "ask",
			NormalizedLocation: "/skills/librarian/SKILL.md", NormalizedBaseDir: "/skills/librarian"}
		for _, m := range mut {
			m(&e)
		}
		return e
	}
	readTcc := func(mut ...func(*ToolCallContext)) ToolCallContext {
		return makeTcc(append([]func(*ToolCallContext){tccTool("read", obj("path", "/skills/librarian/SKILL.md"))}, mut...)...)
	}
	entries := func(e ...SkillPromptEntry) func() []SkillPromptEntry { return func() []SkillPromptEntry { return e } }

	tw(t, f, "returns null when tool is not read", func(t *testing.T) {
		eq(t, DescribeSkillReadGate(readTcc(func(c *ToolCallContext) { c.ToolName = "write" }), normalizer, entries(entry())) == nil, true, "null")
	})
	tw(t, f, "returns null when no active skill entries", func(t *testing.T) {
		eq(t, DescribeSkillReadGate(readTcc(), normalizer, entries()) == nil, true, "null")
	})
	tw(t, f, "returns null when read path does not match any skill", func(t *testing.T) {
		tcc := readTcc(func(c *ToolCallContext) { c.Input = obj("path", "/test/project/src/index.ts") })
		eq(t, DescribeSkillReadGate(tcc, normalizer, entries(entry())) == nil, true, "null")
	})
	tw(t, f, "returns null when input has no path", func(t *testing.T) {
		tcc := readTcc(func(c *ToolCallContext) { c.Input = newObject() })
		eq(t, DescribeSkillReadGate(tcc, normalizer, entries(entry())) == nil, true, "null")
	})
	preResolvedTwin := func(state string) func(t *testing.T) {
		return func(t *testing.T) {
			d := DescribeSkillReadGate(readTcc(), normalizer, entries(entry(func(e *SkillPromptEntry) { e.State = state })))
			if d == nil {
				t.Fatal("got nil")
			}
			eq(t, *d.PreResolved, GatePreResolved{State: state}, "preResolved")
		}
	}
	tw(t, f, "returns GateDescriptor with preResolved.state matching skill entry state (ask)", preResolvedTwin("ask"))
	tw(t, f, "returns GateDescriptor with preResolved.state matching skill entry state (allow)", preResolvedTwin("allow"))
	tw(t, f, "returns GateDescriptor with preResolved.state matching skill entry state (deny)", preResolvedTwin("deny"))
	tw(t, f, "decision surface is 'skill' and decision value is the skill name", func(t *testing.T) {
		d := DescribeSkillReadGate(readTcc(), normalizer, entries(entry(func(e *SkillPromptEntry) { e.Name = "my-skill" })))
		eq(t, d.Decision, GateDecision{Surface: "skill", Value: "my-skill"}, "decision")
	})
	tw(t, f, "carries the skill name as single-value access facts on promptDetails", func(t *testing.T) {
		d := DescribeSkillReadGate(readTcc(), normalizer, entries(entry(func(e *SkillPromptEntry) { e.Name = "my-skill" })))
		eq(t, *d.PromptDetails.AccessIntent, ForwardedAccessFacts{Surface: "skill", MatchValues: []string{"my-skill"}}, "accessIntent")
	})
	tw(t, f, "emits a skill_read payload keeping the skill as the decision value", func(t *testing.T) {
		d := DescribeSkillReadGate(readTcc(), normalizer, entries(entry(func(e *SkillPromptEntry) { e.Name = "my-skill" })))
		eq(t, d.Payload.Kind, "skill_read", "kind")
		eq(t, d.Payload.Request.Value, "my-skill", "value")
	})
	tw(t, f, "payload contains the skill name and the path it was reached through", func(t *testing.T) {
		d := DescribeSkillReadGate(readTcc(), normalizer, entries(entry()))
		eq(t, d.Payload.Kind, "skill_read", "kind")
		eq(t, d.Payload.Request.Value, "librarian", "value")
		eq(t, d.Payload.Request.Requester.AgentName, (*string)(nil), "agent")
		eq(t, d.Payload.Evidence, []PromptEvidence{{Label: "read path", Text: "/skills/librarian/SKILL.md"}}, "evidence")
	})
	tw(t, f, "promptDetails includes skill_read source and skillName", func(t *testing.T) {
		tcc := readTcc(func(c *ToolCallContext) { c.AgentName, c.ToolCallID = sp("test-agent"), "tc-42" })
		d := DescribeSkillReadGate(tcc, normalizer, entries(entry(func(e *SkillPromptEntry) { e.Name = "my-skill" })))
		eq(t, [5]any{d.PromptDetails.Source, d.PromptDetails.AgentName, d.PromptDetails.ToolCallID, d.PromptDetails.ToolName, d.PromptDetails.SkillName},
			[5]any{"skill_read", sp("test-agent"), sp("tc-42"), sp("read"), sp("my-skill")}, "details")
	})
	tw(t, f, "logContext includes skill_read source and skillName", func(t *testing.T) {
		tcc := readTcc(func(c *ToolCallContext) { c.AgentName = sp("agent-1") })
		d := DescribeSkillReadGate(tcc, normalizer, entries(entry()))
		hasFields(t, d.LogContext, "source", "skill_read", "skillName", "librarian", "agentName", "agent-1")
	})
	tw(t, f, "surface is 'skill' on the descriptor", func(t *testing.T) {
		eq(t, DescribeSkillReadGate(readTcc(), normalizer, entries(entry())).Surface, "skill", "surface")
	})
}
