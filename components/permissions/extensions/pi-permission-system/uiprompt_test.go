package pi_permission_system

import "testing"

// Twins of test/service/permission-ui-prompt.test.ts and the permissions:ui_prompt half of test/service/permission-events.test.ts.

func payloadWith(mut func(*PromptRequestFacts)) PromptPayload {
	return authPayload(func(p *PromptPayload) { mut(&p.Request) })
}

func eventJSON(e PermissionUiPromptEvent) string { return marshalJSON(e.JS(), "") }

func TestBuildUiPromptTwins(t *testing.T) {
	f := "service/permission-ui-prompt"
	want := func(id, source string, surface, value any, agent any, payload PromptPayload, forwarding string) string {
		return `{"requestId":"` + id + `","source":"` + source + `","surface":` + marshalJSON(surface, "") + `,"value":` + marshalJSON(value, "") +
			`,"agentName":` + marshalJSON(agent, "") + `,"request":` + marshalJSON(payload.JS().obj("request"), "") + `,"forwarding":` + forwarding + `}`
	}

	tw(t, f, "normalizes a skill prompt to the skill surface and skill-name value", func(t *testing.T) {
		payload := payloadWith(func(r *PromptRequestFacts) { r.Surface, r.Value = "skill", "deploy-helper" })
		got := BuildUiPrompt(UiPromptInput{RequestID: "req-2", Source: "skill_input", Payload: payload, SkillName: sp("deploy-helper")})
		eq(t, eventJSON(got), want("req-2", "skill_input", "skill", "deploy-helper", nil, payload, "null"), "event")
	})
	tw(t, f, "derives value with command > path > target > skillName > toolName precedence", func(t *testing.T) {
		got := BuildUiPrompt(UiPromptInput{RequestID: "req-3", Source: "tool_call", Payload: authPayload(), ToolName: sp("read"), Path: sp("/etc/hosts"), Target: sp("ignored")})
		eq(t, *got.Value, "/etc/hosts", "value")
		// The rest of the precedence chain.
		all := UiPromptInput{Source: "tool_call", ToolName: sp("t"), SkillName: sp("s"), Target: sp("g"), Path: sp("p"), Command: sp("c")}
		for _, step := range []struct {
			clear func(*UiPromptInput)
			want  string
		}{{func(*UiPromptInput) {}, "c"}, {func(i *UiPromptInput) { i.Command = nil }, "p"}, {func(i *UiPromptInput) { i.Path = nil }, "g"},
			{func(i *UiPromptInput) { i.Target = nil }, "s"}, {func(i *UiPromptInput) { i.SkillName = nil }, "t"}} {
			step.clear(&all)
			eq(t, *BuildUiPrompt(all).Value, step.want, "precedence")
		}
		all.ToolName = nil
		if BuildUiPrompt(all).Value != nil {
			t.Error("value not null")
		}
	})
	tw(t, f, "derives surface and value from direct fields and defaults forwarding to null", func(t *testing.T) {
		payload := payloadWith(func(r *PromptRequestFacts) { r.Surface, r.ToolName, r.Value, r.MatchedPattern = "bash", sp("bash"), "git push", sp("git *") })
		got := BuildUiPrompt(UiPromptInput{RequestID: "req-u1", Source: "tool_call", AgentName: sp("Explore"), Payload: payload, ToolName: sp("bash"), Command: sp("git push")})
		eq(t, eventJSON(got), want("req-u1", "tool_call", "bash", "git push", "Explore", payload, "null"), "event")
	})
	tw(t, f, "carries the payload's invariant core verbatim, with no evidence or annotations", func(t *testing.T) {
		payload := authPayload(func(p *PromptPayload) {
			p.Kind = "bash"
			p.Request = PromptRequestFacts{Requester: PromptRequester{AgentName: sp("Explore")}, Surface: "bash", ToolName: sp("bash"), InvokedToolName: sp("exec_command"),
				Value: "git push", MatchedPattern: sp("git *"), CommandContext: sp("subshell"), ExecutedUnit: sp("git push --force")}
			p.Evidence = []PromptEvidence{{Label: "full command", Text: "secret-ish"}}
			p.Annotations = []PromptAnnotation{{Source: "judge", Text: "advisory"}}
		})
		event := BuildUiPrompt(UiPromptInput{RequestID: "req-core", Source: "tool_call", AgentName: sp("Explore"), Payload: payload, ToolName: sp("bash"), Command: sp("git push")})
		eq(t, event.Request, payload.Request, "request")
		eq(t, marshalJSON(event.JS().obj("request"), ""), `{"requester":{"agentName":"Explore","forwarded":false,"sessionId":null},"surface":"bash","toolName":"bash",`+
			`"invokedToolName":"exec_command","value":"git push","matchedPattern":"git *","commandContext":"subshell","executedUnit":"git push --force"}`, "request JSON")
		// The bus is the narrowest renderer (ADR 0011 section 6): no evidence reaches it.
		for _, k := range []string{"message", "evidence", "annotations"} {
			if event.JS().has(k) {
				t.Errorf("event has %q", k)
			}
		}
	})
	tw(t, f, "uses explicit surface and value overrides in place of the derived projection", func(t *testing.T) {
		payload := payloadWith(func(r *PromptRequestFacts) { r.Surface = "external_directory" })
		got := BuildUiPrompt(UiPromptInput{RequestID: "req-u2", Source: "tool_call", AgentName: sp("Explore"), Payload: payload, ToolName: sp("bash"), Command: sp("git push"),
			Surface: DisplayStr("external_directory"), Value: DisplayStr("/etc/hosts")})
		eq(t, eventJSON(got), want("req-u2", "tool_call", "external_directory", "/etc/hosts", "Explore", payload, "null"), "event")
	})
	tw(t, f, "treats an explicit null surface/value override as intentional, not a fallback trigger", func(t *testing.T) {
		payload := authPayload()
		got := BuildUiPrompt(UiPromptInput{RequestID: "req-u3", Source: "tool_call", Payload: payload, ToolName: sp("bash"), Command: sp("git push"), Surface: DisplayNull, Value: DisplayNull})
		eq(t, eventJSON(got), want("req-u3", "tool_call", nil, nil, nil, payload, "null"), "event")
	})
	tw(t, f, "passes forwarding context through alongside explicit display fields", func(t *testing.T) {
		payload := payloadWith(func(r *PromptRequestFacts) {
			r.Requester = PromptRequester{AgentName: sp("Explore"), Forwarded: true, SessionID: sp("child-session")}
			r.Surface, r.ToolName, r.Value = "bash", sp("bash"), "git push"
		})
		got := BuildUiPrompt(UiPromptInput{RequestID: "req-u4", Source: "tool_call", AgentName: sp("Explore"), Payload: payload, Surface: DisplayStr("bash"), Value: DisplayStr("git push"),
			Forwarding: &ForwardedPromptContext{RequesterAgentName: sp("Explore"), RequesterSessionID: sp("child-session")}})
		eq(t, eventJSON(got), want("req-u4", "tool_call", "bash", "git push", "Explore", payload, `{"requesterAgentName":"Explore","requesterSessionId":"child-session"}`), "event")
	})
	tw(t, f, "passes forwarding context with null requester identity through unchanged", func(t *testing.T) {
		payload := authPayload()
		got := BuildUiPrompt(UiPromptInput{RequestID: "req-fwd-null", Source: "tool_call", Payload: payload, Surface: DisplayNull, Value: DisplayNull, Forwarding: &ForwardedPromptContext{}})
		eq(t, eventJSON(got), want("req-fwd-null", "tool_call", nil, nil, nil, payload, `{"requesterAgentName":null,"requesterSessionId":null}`), "event")
	})
}

func TestEmitUiPromptEventTwins(t *testing.T) {
	f := "service/permission-events"
	event := func(mut ...func(*PermissionUiPromptEvent)) PermissionUiPromptEvent {
		e := PermissionUiPromptEvent{RequestID: "req-123", Source: "tool_call", Surface: sp("bash"), Value: sp("git status"), AgentName: sp("Explore"), Request: authPayload().Request}
		for _, m := range mut {
			m(&e)
		}
		return e
	}
	tw(t, f, "emits on the permissions:ui_prompt channel", func(t *testing.T) {
		bus := &busFake{}
		EmitUiPromptEvent(bus, event())
		emits := bus.snapshot()
		eq(t, len(emits), 1, "emits")
		eq(t, emits[0].channel, "permissions:ui_prompt", "channel")
		eq(t, PermissionsUIPromptChannel, "permissions:ui_prompt", "constant")
	})
	tw(t, f, "forwards the full payload unchanged", func(t *testing.T) {
		bus := &busFake{}
		e := event(func(e *PermissionUiPromptEvent) {
			e.Forwarding = &ForwardedPromptContext{RequesterAgentName: sp("Worker"), RequesterSessionID: sp("child")}
		})
		EmitUiPromptEvent(bus, e)
		eq(t, marshalJSON(bus.snapshot()[0].data, ""), eventJSON(e), "payload")
	})
	tw(t, f, "swallows event bus errors because UI prompt broadcasts are observational", func(t *testing.T) {
		for _, bus := range []*busFake{{failOn: "permissions:ui_prompt"}, {panics: true}} {
			EmitUiPromptEvent(bus, event())
		}
	})
}
