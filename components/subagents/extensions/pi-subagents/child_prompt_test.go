package pi_subagents

import (
	"strings"
	"testing"
)

const sprSkillsSection = "\n\nThe following skills provide specialized instructions for specific tasks.\nUse the read tool to load a skill's file when the task matches its description.\nWhen a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md / dirname of the path) and use that absolute path in tool commands.\n\n<available_skills>\n  <skill>\n    <name>safe-bash</name>\n    <description>desc</description>\n    <location>/tmp/SKILL.md</location>\n  </skill>\n  <skill>\n    <name>pi-subagents</name>\n    <description>delegate to subagents</description>\n    <location>/tmp/pi-subagents/SKILL.md</location>\n  </skill>\n</available_skills>"

const sprConfiguredSkills = "\n\nThe following configured skills are available to this subagent.\nUse the read tool to load a skill's file when the task matches its description.\nWhen a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md / dirname of the path) and use that absolute path in tool commands.\n\n<available_skills>\n  <skill>\n    <name>configured-skill</name>\n    <description>explicit agent skill</description>\n    <location>/tmp/configured-skill/SKILL.md</location>\n  </skill>\n</available_skills>"

var sprBasePrompt = "You are a subagent." +
	"\n\n# Project Context\n\nProject-specific instructions and guidelines:\n\n## /repo/AGENTS.md\n\nProject rules\n\n" +
	sprSkillsSection + "\nCurrent date: 2026-04-16" + "\nCurrent working directory: /repo"

func TestSubagentPromptRuntime(t *testing.T) {
	const f = "subagent-prompt-runtime"
	withTempHome(t)
	globalDir := agentDir()
	has := func(t *testing.T, s, sub string, want bool, what string) {
		t.Helper()
		if strings.Contains(s, sub) != want {
			t.Errorf("%s: contains(%q) = %v, want %v\n%s", what, sub, !want, want, s)
		}
	}
	all := func(project, global, skills bool) rewriteOptions {
		return rewriteOptions{InheritProjectContext: project, InheritGlobalContext: global, InheritSkills: skills}
	}
	globalXML := func(name string) string {
		return "You are a subagent." +
			"\n\n<project_context>\n\nProject-specific instructions and guidelines:\n\n" +
			"<project_instructions path=\"" + globalDir + "/" + name + "\">\nGlobal rules\n</project_instructions>\n\n" +
			"<project_instructions path=\"/repo/" + name + "\">\nRepo rules\n</project_instructions>\n\n" +
			"</project_context>\n\n" + "Current working directory: /repo"
	}

	tw(t, f, "collects runtime extension acknowledgements until terminal serialization", func(t *testing.T) {
		var acknowledged [][]string
		c := &runtimeAckCollector{sink: func(ids []string) { acknowledged = append(acknowledged, ids) }}
		c.acknowledge("ext.one")
		c.acknowledge("ext.one")
		c.acknowledge("bad/path")
		c.finalize()
		c.acknowledge("late")
		eq(t, acknowledged, [][]string{{"ext.one", "ext.one"}}, "acknowledged")
	})
	tw(t, f, "strips only the project context block", func(t *testing.T) {
		r := stripProjectContext(sprBasePrompt)
		has(t, r, "# Project Context", false, "project context")
		has(t, r, "The following skills provide specialized instructions for specific tasks.", true, "skills")
		has(t, r, "Current date: 2026-04-16", true, "date")
	})
	tw(t, f, "strips an XML <project_context> block as full project context removal", func(t *testing.T) {
		r := stripProjectContext(globalXML("AGENTS.md"))
		has(t, r, "<project_context>", false, "tag")
		has(t, r, "<project_instructions", false, "instructions")
		has(t, r, "Current working directory: /repo", true, "cwd")
	})
	tw(t, f, "strips only global context files while preserving repository context", func(t *testing.T) {
		r := stripGlobalContext(globalXML("AGENTS.md"))
		has(t, r, "Global rules", false, "global")
		has(t, r, "Repo rules", true, "repo")
		has(t, r, "</project_context>", true, "closing tag")
	})
	tw(t, f, "strips only global context files from legacy Markdown while preserving repository context", func(t *testing.T) {
		prompt := "You are a subagent." + "\n\n# Project Context\n\nProject-specific instructions and guidelines:\n\n" +
			"## " + globalDir + "/AGENTS.md\n\nGlobal rules\n\n" + "## /repo/AGENTS.md\n\nRepo rules\n\n" + sprSkillsSection + "\nCurrent date: 2026-04-16"
		r := rewriteSubagentPrompt(prompt, all(true, false, true))
		has(t, r, "Global rules", false, "global")
		has(t, r, "## /repo/AGENTS.md", true, "repo heading")
		has(t, r, "Repo rules", true, "repo")
	})
	tw(t, f, "recognizes Pi context filenames case-insensitively", func(t *testing.T) {
		r := stripGlobalContext(globalXML("AGENTS.MD"))
		has(t, r, "Global rules", false, "global")
		has(t, r, "Repo rules", true, "repo")
	})
	tw(t, f, "strips global instructions from ${label}", func(t *testing.T) {
		for label, prompt := range map[string]string{
			"CRLF context":                       "<project_context>\r\n<project_instructions path=\"" + globalDir + "/AGENTS.md\">\r\nGlobal rules\r\n</project_instructions>\r\n<project_instructions path=\"/repo/AGENTS.md\">\r\nRepo rules\r\n</project_instructions>\r\n</project_context>",
			"content without a trailing newline": "<project_context><project_instructions path=\"" + globalDir + "/AGENTS.md\">Global rules</project_instructions><project_instructions path=\"/repo/AGENTS.md\">Repo rules</project_instructions></project_context>",
			"same-line content":                  "<project_context><project_instructions path=\"" + globalDir + "/CLAUDE.md\">Global rules</project_instructions><project_instructions path=\"/repo/CLAUDE.md\">Repo rules</project_instructions></project_context>",
		} {
			r := stripGlobalContext(prompt)
			has(t, r, "Global rules", false, label)
			has(t, r, "Repo rules", true, label)
		}
	})
	tw(t, f, "does not strip matching examples outside project context", func(t *testing.T) {
		example := "<project_instructions path=\"" + globalDir + "/AGENTS.md\">Example only</project_instructions>"
		eq(t, stripGlobalContext(example), example, "example")
	})
	tw(t, f, "inherits global context when inheritGlobalContext is true", func(t *testing.T) {
		prompt := "You are a subagent." + "\n\n<project_context>\n\nProject-specific instructions and guidelines:\n\n" +
			"<project_instructions path=\"" + globalDir + "/AGENTS.md\">\nGlobal rules\n</project_instructions>\n\n</project_context>\n\nCurrent working directory: /repo"
		has(t, rewriteSubagentPrompt(prompt, all(true, true, true)), "Global rules", true, "global")
	})
	tw(t, f, "removes global context files while keeping repository context via rewriteSubagentPrompt", func(t *testing.T) {
		r := rewriteSubagentPrompt(globalXML("AGENTS.md"), all(true, false, true))
		has(t, r, "Global rules", false, "global")
		has(t, r, "Repo rules", true, "repo")
	})
	tw(t, f, "strips only the inherited skills block", func(t *testing.T) {
		r := stripInheritedSkills(sprBasePrompt)
		has(t, r, "# Project Context", true, "project context")
		has(t, r, "<available_skills>", false, "skills")
		has(t, r, "Current date: 2026-04-16", true, "date")
	})
	tw(t, f, "can strip both inherited sections together", func(t *testing.T) {
		r := rewriteSubagentPrompt(sprBasePrompt, all(false, false, false))
		has(t, r, "# Project Context", false, "project context")
		has(t, r, "<available_skills>", false, "skills")
		has(t, r, "Current working directory: /repo", true, "cwd")
	})
	tw(t, f, "injects a child-only boundary that forbids proposing or running subagents", func(t *testing.T) {
		r := rewriteSubagentPrompt(sprBasePrompt, all(true, true, true))
		eq(t, strings.HasPrefix(r, childSubagentBoundaryInstructions), true, "starts with the boundary")
		has(t, r, "Do not propose or run subagents.", true, "boundary text")
		has(t, r, "If you need to edit files, use the available editing tools.", true, "edit text")
		has(t, r, "call the actual edit/write tools", false, "old text")
		has(t, r, "Do not print tool-call syntax, patches, or pseudo-tool calls as text.", true, "pseudo")
		again := rewriteSubagentPrompt(r, all(true, true, true))
		eq(t, strings.Index(again, childSubagentBoundaryInstructions), 0, "idempotent start")
		eq(t, strings.LastIndex(again, childSubagentBoundaryInstructions), 0, "idempotent end")
	})
	tw(t, f, "replaces inherited child boundaries with the fanout boundary when authorized", func(t *testing.T) {
		o := all(true, true, true)
		o.FanoutChild = true
		r := rewriteSubagentPrompt(childSubagentBoundaryInstructions+"\n\n"+sprBasePrompt, o)
		eq(t, strings.HasPrefix(r, childFanoutBoundaryInstructions), true, "starts with fanout")
		has(t, r, "You may use the `subagent` tool only for the fanout work explicitly requested in this task.", true, "fanout text")
		has(t, r, "If you need to edit files, use the available editing tools.", true, "edit text")
		has(t, r, "call the actual edit/write tools", false, "old text")
		has(t, r, "Do not propose or run subagents.", false, "strict text")
		eq(t, strings.LastIndex(r, childFanoutBoundaryInstructions), 0, "single boundary")
	})
	tw(t, f, "replaces inherited fanout boundaries with the strict boundary when fanout is not authorized", func(t *testing.T) {
		r := rewriteSubagentPrompt(childFanoutBoundaryInstructions+"\n\n"+sprBasePrompt, all(true, true, true))
		eq(t, strings.HasPrefix(r, childSubagentBoundaryInstructions), true, "starts with strict")
		has(t, r, "explicit fanout responsibility", false, "fanout text")
		eq(t, strings.LastIndex(r, childSubagentBoundaryInstructions), 0, "single boundary")
	})
	tw(t, f, "keeps explicitly injected skill content when inherited skills are stripped", func(t *testing.T) {
		prompt := "You are a subagent.\n\n<skill name=\"explicit\">\nKeep this section\n</skill>" +
			"\n\n# Project Context\n\nProject-specific instructions and guidelines:\n\n## /repo/AGENTS.md\n\nProject rules\n\n" + sprSkillsSection + "\nCurrent date: 2026-04-16"
		r := rewriteSubagentPrompt(prompt, all(false, false, false))
		has(t, r, "<skill name=\"explicit\">", true, "explicit skill")
		has(t, r, "<available_skills>", false, "skills")
		has(t, r, "# Project Context", false, "project context")
	})
	tw(t, f, "keeps configured lazy skill references when inherited skills are stripped", func(t *testing.T) {
		prompt := "You are a subagent." + sprConfiguredSkills +
			"\n\n# Project Context\n\nProject-specific instructions and guidelines:\n\n## /repo/AGENTS.md\n\nProject rules\n\n" + sprSkillsSection + "\nCurrent date: 2026-04-16"
		r := rewriteSubagentPrompt(prompt, all(false, false, false))
		has(t, r, "<name>configured-skill</name>", true, "configured")
		has(t, r, "/tmp/configured-skill/SKILL.md", true, "location")
		has(t, r, "<name>safe-bash</name>", false, "inherited")
		has(t, r, "# Project Context", false, "project context")
	})
	tw(t, f, "strips the subagent orchestration skill even when inherited skills remain", func(t *testing.T) {
		r := rewriteSubagentPrompt(sprBasePrompt, all(true, true, true))
		has(t, r, "<name>safe-bash</name>", true, "safe-bash")
		has(t, r, "<name>pi-subagents</name>", false, "orchestration skill")
		has(t, r, "delegate to subagents", false, "orchestration text")
	})
	tw(t, f, "strips explicit pi-subagents skill injection from child prompts", func(t *testing.T) {
		prompt := "Before\n\n<skill name=\"pi-subagents\">\nDo not keep this.\n</skill>\n\n<skill name=\"safe-bash\">\nKeep this.\n</skill>\nAfter"
		r := stripSubagentOrchestrationSkill(prompt)
		has(t, r, "Do not keep this", false, "stripped")
		has(t, r, "<skill name=\"safe-bash\">", true, "kept")
	})
	msg := func(t *testing.T, s string) *jsObject { return js(t, s).(*jsObject) }
	list := func(msgs ...*jsObject) []any {
		out := make([]any, len(msgs))
		for i, m := range msgs {
			out[i] = m
		}
		return out
	}
	strip := func(msgs []any) []any { out, _ := stripParentOnlySubagentMessages(msgs, true, false); return out }
	tw(t, f, "strips parent-only subagent custom messages from forked child context", func(t *testing.T) {
		user := msg(t, `{"role":"user","content":"Task"}`)
		other := msg(t, `{"role":"custom","customType":"other","content":"keep"}`)
		msgs := list(user,
			msg(t, `{"role":"custom","customType":"subagent-orchestration-instructions","content":"Subagent orchestration is enabled."}`),
			msg(t, `{"role":"custom","customType":"subagent-slash-result","content":"## Orchestration"}`),
			msg(t, `{"role":"custom","customType":"subagent-slash-text-result","content":"Subagent profiles"}`),
			msg(t, `{"role":"custom","customType":"subagent-notify","content":"Background task completed"}`),
			msg(t, `{"role":"custom","customType":"subagent_control_notice","content":"needs attention"}`),
			msg(t, `{"role":"custom","customType":"subagent_watchdog_warning","content":"<subagent_watchdog>parent-only</subagent_watchdog>"}`),
			msg(t, `{"role":"custom","customType":"subagent_watchdog_warning","content":"<subagent_watchdog>child-visible</subagent_watchdog>","details":{"source":"child"}}`),
			other)
		eq(t, strip(msgs), list(user, other), "messages")
	})
	tw(t, f, "strips prior parent subagent tool calls and results from forked child context", func(t *testing.T) {
		user := msg(t, `{"role":"user","content":"Task"}`)
		read := msg(t, `{"role":"toolResult","toolName":"read","content":"file contents"}`)
		got := strip(list(user, msg(t, `{"role":"toolResult","toolName":"subagent","content":"subagent results"}`), read,
			msg(t, `{"role":"assistant","content":[{"type":"text","text":"I will inspect the repo."},{"type":"toolCall","name":"subagent","input":{"agent":"worker"}},{"type":"toolCall","name":"read","input":{"path":"README.md"}}]}`),
			msg(t, `{"role":"assistant","content":[{"type":"toolCall","name":"subagent","input":{"agent":"reviewer"}}]}`)))
		eq(t, jsonString(got), jsonString(list(user, read,
			msg(t, `{"role":"assistant","content":[{"type":"text","text":"I will inspect the repo."},{"type":"toolCall","name":"read","input":{"path":"README.md"}}]}`))), "messages")
	})
	tw(t, f, "sanitizes non-portable tool ids in forked child context", func(t *testing.T) {
		got := strip(list(
			msg(t, `{"role":"assistant","content":[{"type":"toolCall","id":"call_read|fc_123","name":"read","input":{"path":"README.md"}},{"type":"toolCall","id":"call_bash-ok","name":"bash","input":{"command":"pwd"}}]}`),
			msg(t, `{"role":"toolResult","toolName":"read","toolCallId":"call_read|fc_123","content":"file contents"}`),
			msg(t, `{"role":"toolResult","toolName":"bash","toolCallId":"call_bash-ok","content":"cwd"}`)))
		want := list(
			msg(t, `{"role":"assistant","content":[{"type":"toolCall","id":"tool_Y2FsbF9yZWFkfGZjXzEyMw","name":"read","input":{"path":"README.md"}},{"type":"toolCall","id":"call_bash-ok","name":"bash","input":{"command":"pwd"}}]}`),
			msg(t, `{"role":"toolResult","toolName":"read","toolCallId":"tool_Y2FsbF9yZWFkfGZjXzEyMw","content":"file contents"}`),
			msg(t, `{"role":"toolResult","toolName":"bash","toolCallId":"call_bash-ok","content":"cwd"}`))
		eq(t, jsonString(got), jsonString(want), "messages")
	})
	tw(t, f, "preserves live nested subagent calls and results in fanout child context", func(t *testing.T) {
		user := msg(t, `{"role":"user","content":"Task"}`)
		result := msg(t, `{"role":"toolResult","toolName":"subagent","content":"OK"}`)
		call := msg(t, `{"role":"assistant","content":[{"type":"toolCall","name":"subagent","input":{"agent":"delegate"}}]}`)
		instruction := msg(t, `{"role":"custom","customType":"subagent-orchestration-instructions","content":"Subagent orchestration is enabled."}`)
		got, _ := stripParentOnlySubagentMessages(list(user, call, result, instruction), true, true)
		eq(t, got, list(user, call, result), "messages")
	})
	tw(t, f, "rewrites the final child-visible prompt through before_agent_start", func(t *testing.T) {
		r := rewriteSubagentPrompt(sprBasePrompt, all(false, true, false))
		has(t, r, "# Project Context", false, "project context")
		has(t, r, "<available_skills>", false, "skills")
		has(t, r, "Current date: 2026-04-16", true, "date")
	})
	tw(t, f, "uses the fanout boundary through before_agent_start for a fanout child", func(t *testing.T) {
		o := all(true, true, true)
		o.FanoutChild = true
		eq(t, strings.HasPrefix(rewriteSubagentPrompt(sprBasePrompt, o), childFanoutBoundaryInstructions), true, "fanout boundary")
	})
	tw(t, f, "filters parent-only artifacts from polluted fork context while preserving ordinary history", func(t *testing.T) {
		prior := msg(t, `{"role":"user","content":"Earlier we said planner → worker → reviewers → worker."}`)
		task := msg(t, `{"role":"user","content":"Now implement only the assigned fix."}`)
		other := msg(t, `{"role":"custom","customType":"other","content":"keep"}`)
		got := rewriteChildContext(list(prior,
			msg(t, `{"role":"custom","customType":"subagent-orchestration-instructions","content":"Subagent orchestration is enabled."}`),
			msg(t, `{"role":"custom","customType":"subagent-slash-result","content":"## Orchestration"}`),
			msg(t, `{"role":"assistant","content":[{"type":"toolCall","name":"subagent","input":{"agent":"worker"}}]}`),
			msg(t, `{"role":"toolResult","toolName":"subagent","content":"subagent results"}`),
			msg(t, `{"role":"custom","customType":"subagent_watchdog_warning","content":"<subagent_watchdog>parent-only</subagent_watchdog>"}`),
			msg(t, `{"role":"custom","customType":"subagent_watchdog_warning","content":"<subagent_watchdog>child-visible</subagent_watchdog>","details":{"source":"child"}}`),
			other, task), "", false)
		eq(t, got, list(prior, other, task), "messages")
	})
	toolID := func(t *testing.T, id string, api string) ([]any, []any) {
		msgs := list(msg(t, `{"role":"user","content":"Task"}`),
			msg(t, `{"role":"assistant","content":[{"type":"toolCall","id":`+jsonString(id)+`,"name":"read","input":{"path":"README.md"}}]}`),
			msg(t, `{"role":"toolResult","toolName":"read","toolCallId":`+jsonString(id)+`,"content":"file"}`))
		return msgs, rewriteChildContext(msgs, api, false)
	}
	tw(t, f, "bounds composite tool ids for Codex child context", func(t *testing.T) {
		id := "call_N7iYNRPXLl9czpXh3bDyMpIL|fc_0e76718634eca88f016a76fdc89aec81919763fa7858f67a0d"
		_, got := toolID(t, id, "openai-codex-responses")
		if got == nil {
			t.Fatal("no rewrite")
		}
		call := got[1].(*jsObject).vals["content"].([]any)[0].(*jsObject).vals["id"].(string)
		result := got[2].(*jsObject).vals["toolCallId"].(string)
		polMatches(t, call, `^[a-zA-Z0-9_-]+$`)
		eq(t, len(call) <= 64, true, "length")
		eq(t, result, call, "result id")
	})
	tw(t, f, "preserves composite tool ids for non-Codex APIs that normalize them", func(t *testing.T) {
		_, got := toolID(t, "call_7XJjvAJfk07117JO8LgBCZjY|fc_0e92b09b28010bac016a756e9e79cc8197b01825a5dc3d9eaa", "openai-responses")
		eq(t, got == nil, true, "no rewrite")
	})
	tw(t, f, "preserves composite tool ids for cursor-native children", func(t *testing.T) {
		id := "call_7XJjvAJfk07117JO8LgBCZjY\nfc_0e92b09b28010bac016a756e9e79cc8197b01825a5dc3d9eaa"
		msgs, got := toolID(t, id, "cursor-native")
		eq(t, got == nil, true, "no rewrite")
		eq(t, msgs[1].(*jsObject).vals["content"].([]any)[0].(*jsObject).vals["id"], any(id), "call id")
		eq(t, msgs[2].(*jsObject).vals["toolCallId"], any(id), "result id")
	})
	tw(t, f, "does not rewrite child context when no parent-only artifacts are present", func(t *testing.T) {
		msgs := list(msg(t, `{"role":"user","content":"Task"}`), msg(t, `{"role":"toolResult","toolName":"read","content":"file"}`),
			msg(t, `{"role":"assistant","content":[{"type":"toolCall","name":"read","input":{"path":"README.md"}}]}`))
		eq(t, rewriteChildContext(msgs, "", false) == nil, true, "no rewrite")
	})
	tskip(t, f, "does not skip drain for in-process child sessions when hasUI is true", "the final drain of a child session holds the agent_end hook until the child's foreground and background runs settle (wait-tool drain, drainOutstandingWork over the child's runtimeState); the port's children are separate pig processes started with --no-extensions, with no drain hook, and the foreground run state this reads belongs to the executor slice")
	tskip(t, f, "reads a late-installed owner barrier for each final drain and balances the hold", "exercises the drain observation barrier installed late by an owning executor and the holdFinalDrain balance of the in-process child runtime; the port has no in-process child sessions and no drain hook to hold")
	tskip(t, f, "does not grant nested wait access for an invalid inherited route", "tests the nested-route check that decides whether a child's bg_wait tool may wait on a nested result route (createNestedRoute, nestedResultsPath); the child bg_wait tool and nested result routes belong to the background-run slice and are refused here")
	tskip(t, f, "registered child bg_wait discovers nested personas and agent_end drains the same scope", "needs the child bg_wait tool, the active run index and nested persona discovery of the background-run slice (updateActiveRunIndex, nested events); none of these exist in the port")
	tskip(t, f, "ignores an unconfigured path-based load", "loads the child runtime module by file path with no configuration and checks that registration does not throw; a Go port has no path-loaded module, the child runtime is not an extension of this binary")
	tskip(t, f, "registers a requested watchdog_diff at launch HEAD and reports unavailable baseline outside Git", "the child watchdog's watchdog_diff tool snapshots the launch HEAD of a git checkout for the supervising watchdog; the watchdog is part of the acceptance, tool-budget and supervision slice and is not ported")
	tskip(t, f, "registers no permission hook by default and routes ask only to the watchdog arbiter", "the child permission gate (registerPermissionGate) routes an ask decision to the watchdog arbiter inside the child session; the permission gate and the watchdog arbiter belong to the policy and watchdog slices")
	tskip(t, f, "fails closed when an ask permission decision stalls", "times a stalled ask decision of the child permission gate through the watchdog arbiter; the gate is part of the policy and watchdog slices, not this port")
	tskip(t, f, "nudges after the tool budget soft limit and blocks configured tools after hard", "the child tool budget hooks count tool calls inside the child session and nudge or block at the limits; tool budgets belong to the policy slice and the port's children run without a runtime extension")
	tskip(t, f, "registers child watchdog lifecycle handlers only when the config enables them", "checks which lifecycle hooks the child watchdog registers for a given ChildWatchdogConfig; there is no child watchdog in the port")
	tskip(t, f, "registered structured_output tool accepts valid schema output and captures it", "the child structured_output tool validates JSON against the step's output schema and captures it to a file for the parent; structured output belongs to the policy slice (outputSchema is refused by the port)")
	tskip(t, f, "requires and validates acceptanceReport when structured capture is required", "the acceptanceReport parameter of the child structured_output tool is part of the acceptance gate of the policy slice, which is not ported")
	tskip(t, f, "captures an omitted optional acceptance report as undefined", "same structured_output capture file as the acceptance report case above; the capture and the acceptance gate are not ported")
	tskip(t, f, "reports capture cleanup failures after clearing every stale file it can", "clearStructuredOutputCaptures deletes stale capture files of the structured_output tool and reports failures; the capture store does not exist in the port")
	tskip(t, f, "scopes local structured_output schema refs under the value parameter", "rewrites local $ref pointers of an output schema under the structured_output tool's value parameter; output schemas are refused by the port, so there is no tool whose schema is rewritten")
	tskip(t, f, "defers native supervisor registration until runtime events and respects installed pi-intercom tools", "the native supervisor client (contact_supervisor) registers inside the child at session start or the first prompt, next to an installed pi-intercom extension; the supervisor channel is refused by the port (subagent_supervisor is a refusing tool)")
	tskip(t, f, "does not satisfy strict allowlists with native generic intercom", "evaluates a child's required tool allowlist against the tools the child registered at agent_start (evaluateChildToolDiagnostic); a port child has no startup tool check hook")
	tskip(t, f, "records missing core write tools from the actual child registry", "reads the child's real tool registry at agent_start to report missing core write tools (the child tool diagnostic); the port does not run a hook inside the child")
	tskip(t, f, "keeps installed pi-intercom while filling only a missing child contact_supervisor tool", "fills in a missing contact_supervisor tool next to a pi-intercom extension in the child's registry; the supervisor channel is not ported")
	tskip(t, f, "registers only native supervisor tools at runtime when pi-intercom is absent", "registers the native supervisor tools inside the child at runtime when no pi-intercom is installed; the supervisor channel is not ported")
	tskip(t, f, "records requested tools missing from the child registry after startup hooks settle", "waits for the child's startup hooks and then records requested tools that never registered (child tool diagnostic); no hooks run inside a port child")
	tskip(t, f, "classifies missing resolved MCP direct tools without softening strict diagnostics", "classifies missing resolved MCP direct tools in formatChildToolDiagnostic output; MCP direct tool selection and the child tool diagnostic are part of the child tool plan slice")
	tskip(t, f, "names an intercom child ${intercomAsks ? \"readably when pi-intercom takes its route as the intercom ID\" : \"by its route when pi-intercom does not ask for an ID\"}", "claims the child's intercom session id and names the child session through pi.setSessionName at before_agent_start, depending on whether pi-intercom asks for an id over the intercom identity event; intercom is not ported and a port child is a pig process whose session name is not set by an extension")
}
