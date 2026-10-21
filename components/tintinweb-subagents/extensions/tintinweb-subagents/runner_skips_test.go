package tintinweb_subagents

import "testing"

// Cases of the original's runner tests that have no Go form: they drive Pi's in-process AgentSession (its active-tool
// hooks, its agent loop, its private model registry), which a child pig process does not expose. What each checks is
// covered another way, named in the reason.
func TestRunnerCasesWithoutAGoForm(t *testing.T) {
	const f = "agent-runner"
	tskip(t, f, "a tool registered during session_start reaches the active set", "a tool an extension registers during the child's session_start does not exist in the parent's registry when the child is started, so the scope is a denylist of names (planScope ExcludeTools); an unknown late tool is admitted by pig's own gate, which is the original's dynamic-mode behavior for a tool that is not excluded")
	tskip(t, f, "a tool registered after bind is picked up on the next turn_end", "the original re-derives the active set on each turn_end through AgentSession.setActiveToolsByName; the child pig activates every registered tool the denylist does not exclude, with no per-turn narrowing to test")
	tskip(t, f, "ext: admits a late tool from the selected extension but not from others", "a late tool of an unselected extension cannot be denied by name before it exists; the original narrows the active set per turn, this port denies the names the parent's registry knows (TestToolAndExtensionScopeReachTheChildAsFlags)")
	tskip(t, f, "ext:foo/bar narrowing still applies to late-registered siblings", "narrowing is applied to the names the parent's registry lists for the extension (TestExtToolSelectorTwins: ext:foo/bar narrows foo to a single tool); a sibling registered after the child started is not known to the denylist")
	tskip(t, f, "beforeToolCall blocks an out-of-scope tool and delegates otherwise", "the original wraps Pi's agent.beforeToolCall hook in the child session; the child here is a pig process whose tool gate is --exclude-tools, and a call to an excluded tool never reaches an extension")
	tskip(t, f, "beforeToolCall preserves a hook pi installed before us", "there is no hook chain to preserve: this port installs no beforeToolCall in the child, the gate is pig's --exclude-tools")
	tskip(t, f, "scope outlives runAgent so resumed turns stay narrowed", "a resumed turn runs in the same child process (or a reopened one started with the same flags, TestResumeReopensAPersistedConversationFromItsFile), so the scope is the process's own and cannot lapse")

	const e = "agent-runner-e2e"
	tskip(t, e, "real pi-mono admits an extension-registered tool when it's in the allowlist (#47)", "runs Pi's real createAgentSession in process; the same property, an extension tool reaching a real child, is shown against Pi and PiG by the probe scenarios p-ext-list and p-tools-all (port/probes, the model request's tool list)")
	tskip(t, e, "an extension tool is absent when extensions are disabled (not loaded)", "in-process Pi session; extensions: false is TestToolAndExtensionScopeReachTheChildAsFlags (a strict allowlist, --no-extensions) and the probe scenario p-ext-false")
	tskip(t, e, "disallowedTools removes a real extension tool from the live session", "in-process Pi session; the denylist reaching the real child is the probe scenario p-disallowed")
	tskip(t, e, "the ext: allowlist flip mutes a loaded-but-unselected extension in real pi-mono", "in-process Pi session; the flip reaching the real child is the probe scenario p-ext-selector-flip")
	tskip(t, e, "an ext: selector surfaces the loaded extension's tool through the flip", "in-process Pi session; the selector reaching the real child is the probe scenario p-ext-selector")

	tskip(t, "e2e/isolated-provider.e2e", "ctx.modelRegistry.runtime is reachable and IS the runtime it wraps", "guards a private field of Pi's in-process ModelRegistry that the original reads to forward providers to its in-process child; a child pig process loads its providers from its own configuration, so there is nothing to forward")
	tskip(t, "e2e/tool-veto-reachability.e2e", "pi installs a chainable beforeToolCall, and runAgent's veto blocks out-of-scope tools", "guards Pi's AgentSession.beforeToolCall hook that the original's in-process veto relies on; this port has no such veto, the gate is pig's --exclude-tools")
}
