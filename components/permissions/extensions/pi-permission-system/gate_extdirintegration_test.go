package pi_permission_system

import (
	"strings"
	"testing"
)

// Twins of test/handlers/external-directory-integration.test.ts.

func TestExternalDirectoryIntegrationTwins(t *testing.T) {
	const f = "handlers/external-directory-integration"
	run := func(t *testing.T, opts gfHandlerOpts, event any, mut ...func(*SessionContext)) (*gfHandlerRig, GateOutcome) {
		t.Helper()
		opts.tools = allExtTools
		ctx, _ := makeCtx(mut...)
		rig := makeHandler(t, opts)
		return rig, rig.toolCall(t, event, ctx)
	}
	readExt := func() *jsObject { return makeToolCallEvent("read", "input", obj("path", externalPath)) }
	blocked := func(t *testing.T, o GateOutcome) {
		t.Helper()
		if o.Action != "block" {
			t.Fatalf("got %+v, want block", o)
		}
	}
	extDecision := func(t *testing.T, rig *gfHandlerRig) PermissionDecisionEvent {
		t.Helper()
		d := findExtDirDecision(rig)
		if d == nil {
			t.Fatal("no external_directory decision event")
		}
		return *d
	}
	triple := func(d PermissionDecisionEvent) [3]string { return [3]string{d.Surface, d.Result, string(d.Resolution)} }

	tw(t, f, "the external-directory ask names the path it gates", func(t *testing.T) {
		got := BuildExternalDirectoryAskPayload(ExternalDirectoryAskFacts{PathAskFacts: PathAskFacts{ToolName: "read", PathValue: "/outside/file", Surface: "external_directory_read"}, Cwd: "/project"})
		eq(t, got.Request.Value, "/outside/file", "value")
	})
	tw(t, f, "EXTENSION_TAG is the expected value", func(t *testing.T) { eq(t, ExtensionTag, "[pi-permission-system]", "tag") })

	tw(t, f, "skips external_directory check when path is inside CWD", func(t *testing.T) {
		_, out := run(t, gfHandlerOpts{checkPermission: makeExtDirCheck("deny")}, makeToolCallEvent("read", "input", obj("path", extDirCwd+"/src/index.ts")))
		// Should not be blocked: the external_directory gate is skipped, and the tool gate sees "allow"
		eq(t, out, gfAllow, "outcome")
	})
	tw(t, f, "fires external_directory check when path is outside CWD", func(t *testing.T) {
		_, out := run(t, gfHandlerOpts{checkPermission: makeExtDirCheck("deny")}, readExt())
		blocked(t, out)
	})
	tw(t, f, "skips external_directory check for non-path-bearing tool (bash)", func(t *testing.T) {
		// bash is not path-bearing, so the tool-path external_directory gate does not fire (the bash gate is separate)
		_, out := run(t, gfHandlerOpts{checkPermission: makeExtDirCheck("deny", "allow")}, makeToolCallEvent("bash", "input", obj("command", "cat "+externalPath)))
		_ = out
	})
	for _, toolName := range allPathBearingTools {
		each(t, "blocks "+toolName+" with an out-of-cwd path when external_directory is deny", func(t *testing.T) {
			_, out := run(t, gfHandlerOpts{checkPermission: makeExtDirCheck("deny")}, makeToolCallEvent(toolName, "input", obj("path", externalPath)))
			blocked(t, out)
		})
	}
	for _, toolName := range optionalPathTools {
		each(t, "skips external_directory check for "+toolName+" when path is omitted", func(t *testing.T) {
			// No path in input: external_directory gate should not fire
			_, out := run(t, gfHandlerOpts{checkPermission: makeExtDirCheck("deny")}, makeToolCallEvent(toolName))
			eq(t, out, gfAllow, "outcome")
		})
	}

	tw(t, f, "falls through to tool gate when external_directory is allow", func(t *testing.T) {
		_, out := run(t, gfHandlerOpts{checkPermission: makeExtDirCheck("allow")}, readExt())
		eq(t, out, gfAllow, "outcome")
	})
	tw(t, f, "emits decision event with policy_allow on external_directory surface", func(t *testing.T) {
		rig, _ := run(t, gfHandlerOpts{checkPermission: makeExtDirCheck("allow")}, readExt())
		eq(t, triple(extDecision(t, rig)), [3]string{"external_directory_read", "allow", "policy_allow"}, "decision")
	})
	tw(t, f, "does not write a block review-log entry when external_directory is allow", func(t *testing.T) {
		rig, _ := run(t, gfHandlerOpts{checkPermission: makeExtDirCheck("allow")}, readExt())
		eq(t, len(blockReviewEntries(rig)), 0, "entries")
	})

	// #144: allow external reads, gate external writes
	tw(t, f, "allows read of external path when external_directory and read are both allow", func(t *testing.T) {
		_, out := run(t, gfHandlerOpts{checkPermission: makeExtDirCheck("allow", "allow")}, readExt())
		eq(t, out, gfAllow, "outcome")
	})
	tw(t, f, "prompts for write to external path when external_directory allows but write is ask", func(t *testing.T) {
		rig, out := run(t, gfHandlerOpts{checkPermission: makeExtDirCheck("allow", "ask"), prompter: makeApprovingPrompter()}, makeToolCallEvent("write", "input", obj("path", externalPath)))
		// external_directory passes; write gate prompts and user approves
		eq(t, out, gfAllow, "outcome")
		eq(t, len(rig.prompter.calls), 1, "escalations")
	})
	tw(t, f, "blocks write to external path when external_directory allows but write is deny", func(t *testing.T) {
		_, out := run(t, gfHandlerOpts{checkPermission: makeExtDirCheck("allow", "deny")}, makeToolCallEvent("write", "input", obj("path", externalPath)))
		blocked(t, out)
	})
	tw(t, f, "emits only the denying surface's decision event", func(t *testing.T) {
		rig, _ := run(t, gfHandlerOpts{checkPermission: makeExtDirCheck("allow", "deny")}, makeToolCallEvent("write", "input", obj("path", externalPath)))
		var write *PermissionDecisionEvent
		for _, d := range rig.events.decisions() {
			if d.Surface == "write" {
				d := d
				write = &d
				break
			}
		}
		if write == nil {
			t.Fatal("no write decision")
		}
		eq(t, triple(*write), [3]string{"write", "deny", "policy_deny"}, "decision")
		// The `write` deny pre-empts every other gate, so the boundary gate that would have allowed this path never runs and states no
		// decision about a call that did not happen (#899).
		eq(t, findExtDirDecision(rig) == nil, true, "no external_directory decision")
	})

	tw(t, f, "blocks with reason containing the external path", func(t *testing.T) {
		_, out := run(t, gfHandlerOpts{checkPermission: makeExtDirCheck("deny")}, readExt())
		blocked(t, out)
		eq(t, strings.Contains(out.Reason, externalPath), true, "reason "+out.Reason)
	})
	tw(t, f, "block reason contains extension attribution", func(t *testing.T) {
		_, out := run(t, gfHandlerOpts{checkPermission: makeExtDirCheck("deny")}, readExt())
		eq(t, strings.Contains(out.Reason, "[pi-permission-system]") && !strings.Contains(out.Reason, "Hard stop"), true, "reason "+out.Reason)
	})
	tw(t, f, "writes review-log entry with resolution policy_denied", func(t *testing.T) {
		rig, _ := run(t, gfHandlerOpts{checkPermission: makeExtDirCheck("deny")}, readExt())
		entries := blockReviewEntries(rig)
		eq(t, len(entries) >= 1, true, "entries")
		hasFields(t, entries[0].details, "resolution", "policy_denied")
	})
	tw(t, f, "emits decision event with policy_deny on external_directory surface", func(t *testing.T) {
		rig, _ := run(t, gfHandlerOpts{checkPermission: makeExtDirCheck("deny")}, readExt())
		eq(t, triple(extDecision(t, rig)), [3]string{"external_directory_read", "deny", "policy_deny"}, "decision")
	})

	tw(t, f, "does not block when user approves", func(t *testing.T) {
		_, out := run(t, gfHandlerOpts{checkPermission: makeExtDirCheck("ask"), prompter: makeApprovingPrompter()}, readExt())
		eq(t, out, gfAllow, "outcome")
	})
	tw(t, f, "emits user_approved decision when user approves", func(t *testing.T) {
		rig, _ := run(t, gfHandlerOpts{checkPermission: makeExtDirCheck("ask"), prompter: makeApprovingPrompter()}, readExt())
		eq(t, triple(extDecision(t, rig)), [3]string{"external_directory_read", "allow", "user_approved"}, "decision")
	})
	tw(t, f, "blocks when user denies", func(t *testing.T) {
		_, out := run(t, gfHandlerOpts{checkPermission: makeExtDirCheck("ask"), prompter: makeDenyingPrompter()}, readExt())
		blocked(t, out)
	})
	tw(t, f, "emits user_denied decision when user denies", func(t *testing.T) {
		rig, _ := run(t, gfHandlerOpts{checkPermission: makeExtDirCheck("ask"), prompter: makeDenyingPrompter()}, readExt())
		eq(t, triple(extDecision(t, rig)), [3]string{"external_directory_read", "deny", "user_denied"}, "decision")
	})
	tw(t, f, "block reason includes denialReason when user provides one", func(t *testing.T) {
		_, out := run(t, gfHandlerOpts{checkPermission: makeExtDirCheck("ask"), prompter: makeDenyingPrompter("not needed")}, readExt())
		blocked(t, out)
		eq(t, strings.Contains(out.Reason, "not needed"), true, "reason "+out.Reason)
	})
	tw(t, f, "blocks with confirmation_unavailable when no UI is available", func(t *testing.T) {
		_, out := run(t, gfHandlerOpts{checkPermission: makeExtDirCheck("ask"), prompter: makeUnavailablePrompter()}, readExt(), func(c *SessionContext) { c.HasUI = false })
		blocked(t, out)
		// The gate surface names the boundary; an unavailable verdict states only that approval was unreachable, since no retry shape
		// changes that.
		eq(t, out.Reason, ExtensionTag+" This 'external_directory_read' call for tool 'read' for path '"+externalPath+"' requires approval, but no interactive UI is available.", "reason")
	})
	tw(t, f, "emits confirmation_unavailable decision when no UI", func(t *testing.T) {
		rig, _ := run(t, gfHandlerOpts{checkPermission: makeExtDirCheck("ask"), prompter: makeUnavailablePrompter()}, readExt(), func(c *SessionContext) { c.HasUI = false })
		eq(t, triple(extDecision(t, rig)), [3]string{"external_directory_read", "deny", "confirmation_unavailable"}, "decision")
	})

	tw(t, f, "honors per-agent override of external_directory policy", func(t *testing.T) {
		// checkPermission varies by agentName: allow for "special-agent", deny otherwise
		agentAware := func(surface string, _ any, agent *string, _ Ruleset) PermissionCheckResult {
			if SurfaceFamilyOf(surface) == "external_directory" {
				state, origin := "deny", "global"
				if agent != nil && *agent == "special-agent" {
					state, origin = "allow", "agent"
				}
				return PermissionCheckResult{State: state, ToolName: surface, Source: "tool", Origin: origin}
			}
			return PermissionCheckResult{State: "allow", ToolName: surface, Source: "tool", Origin: "builtin"}
		}
		// With agent override → allowed
		rig1, out1 := run(t, gfHandlerOpts{checkPermission: agentAware, resolveAgent: func(ActiveAgentContext, string) (*string, error) { return sp("special-agent"), nil }}, readExt())
		eq(t, out1, gfAllow, "outcome with the agent")
		d := extDecision(t, rig1)
		eq(t, [3]any{d.Result, d.Resolution, d.AgentName}, [3]any{"allow", PermissionDecisionResolution("policy_allow"), sp("special-agent")}, "decision")
		// Without agent override → denied
		_, out2 := run(t, gfHandlerOpts{checkPermission: agentAware, resolveAgent: func(ActiveAgentContext, string) (*string, error) { return nil, nil }}, readExt())
		blocked(t, out2)
	})

	tw(t, f, "decision event value is the external path", func(t *testing.T) {
		rig, _ := run(t, gfHandlerOpts{checkPermission: makeExtDirCheck("deny")}, readExt())
		eq(t, extDecision(t, rig).Value, externalPath, "value")
	})
	tw(t, f, "decision event includes agentName when present", func(t *testing.T) {
		rig, _ := run(t, gfHandlerOpts{checkPermission: makeExtDirCheck("allow"), resolveAgent: func(ActiveAgentContext, string) (*string, error) { return sp("my-agent"), nil }}, readExt())
		eq(t, extDecision(t, rig).AgentName, sp("my-agent"), "agent")
	})
	tw(t, f, "decision event agentName is null when no agent", func(t *testing.T) {
		rig, _ := run(t, gfHandlerOpts{checkPermission: makeExtDirCheck("allow")}, readExt())
		eq(t, extDecision(t, rig).AgentName, (*string)(nil), "agent")
	})
}
