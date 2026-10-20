package pi_permission_system

import (
	"runtime"
	"testing"
)

// Twins of test/policy/{synthesize,permission-gate,permission-resolver}.test.ts and test/session/permission-session.test.ts.

func TestSynthesizeTwins(t *testing.T) {
	const f = "policy/synthesize"
	posix := PosixPathFlavor
	ev := func(surface, value string, rules Ruleset) Rule { return Evaluate(surface, value, rules, posix, "") }
	mcpAllow := Rule{Surface: "mcp", Pattern: "exa:*", Action: "allow", Layer: "config", Origin: "global"}

	tw(t, f, "emits a single universal catch-all rule with layer 'default' and origin 'builtin'", func(t *testing.T) {
		rules := SynthesizeDefaults("ask", "")
		eq(t, len(rules), 1, "len")
		eq(t, rules[0], rule("*", "*", "ask", "default", "builtin"), "rule")
	})
	tw(t, f, "reflects the supplied PermissionState as the action", func(t *testing.T) {
		for _, a := range []string{"allow", "deny", "ask"} {
			eq(t, SynthesizeDefaults(a, "")[0].Action, a, a)
		}
	})
	tw(t, f, "universal rule catches any surface via wildcardMatch", func(t *testing.T) {
		rules := SynthesizeDefaults("ask", "")
		eq(t, ev("read", "*", rules).Action, "ask", "read")
		eq(t, ev("bash", "git status", rules).Action, "ask", "bash")
		eq(t, ev("external_directory", "*", rules).Action, "ask", "ext")
		eq(t, ev("future_surface", "*", rules).Action, "ask", "future")
	})
	tw(t, f, "universal rule has layer 'default'", func(t *testing.T) { eq(t, ev("read", "*", SynthesizeDefaults("allow", "")).Layer, "default", "layer") })
	tw(t, f, "defaults to origin 'builtin' when no origin supplied", func(t *testing.T) { eq(t, SynthesizeDefaults("ask", "")[0].Origin, "builtin", "origin") })
	tw(t, f, "universal rule carries config scope origin when supplied", func(t *testing.T) { eq(t, SynthesizeDefaults("ask", "global")[0].Origin, "global", "origin") })
	tw(t, f, "origin is preserved through evaluate()", func(t *testing.T) {
		eq(t, ev("read", "*", SynthesizeDefaults("allow", "project")).Origin, "project", "origin")
	})
	tw(t, f, "all RuleOrigin values are accepted", func(t *testing.T) {
		for _, o := range []string{"global", "project", "agent", "project-agent", "builtin", "baseline", "session"} {
			eq(t, SynthesizeDefaults("ask", o)[0].Origin, o, o)
		}
	})

	tw(t, f, "returns empty ruleset when config has no mcp allow rules", func(t *testing.T) {
		eq(t, SynthesizeBaseline(Ruleset{{Surface: "mcp", Pattern: "*", Action: "deny", Layer: "config", Origin: "global"}}), Ruleset{}, "rules")
	})
	tw(t, f, "returns empty ruleset for empty config rules", func(t *testing.T) { eq(t, SynthesizeBaseline(Ruleset{}), Ruleset{}, "rules") })
	tw(t, f, "synthesizes 5 baseline rules when at least one mcp allow config rule exists", func(t *testing.T) {
		eq(t, len(SynthesizeBaseline(Ruleset{mcpAllow})), 5, "len")
	})
	tw(t, f, "baseline rules all have layer 'baseline', action 'allow', and origin 'baseline'", func(t *testing.T) {
		for _, r := range SynthesizeBaseline(Ruleset{mcpAllow}) {
			eq(t, []string{r.Layer, r.Action, r.Surface, r.Origin}, []string{"baseline", "allow", "mcp", "baseline"}, "rule")
		}
	})
	tw(t, f, "baseline rules cover the 5 MCP metadata targets", func(t *testing.T) {
		var patterns []string
		for _, r := range SynthesizeBaseline(Ruleset{mcpAllow}) {
			patterns = append(patterns, r.Pattern)
		}
		eq(t, patterns, []string{"mcp_status", "mcp_list", "mcp_search", "mcp_describe", "mcp_connect"}, "patterns")
	})
	tw(t, f, "baseline is NOT synthesized when allow rule is on a non-mcp surface", func(t *testing.T) {
		eq(t, SynthesizeBaseline(Ruleset{{Surface: "bash", Pattern: "git *", Action: "allow", Layer: "config", Origin: "global"}}), Ruleset{}, "rules")
	})
	tw(t, f, "baseline auto-allows mcp_status when an mcp allow rule exists", func(t *testing.T) {
		r := ev("mcp", "mcp_status", SynthesizeBaseline(Ruleset{mcpAllow}))
		eq(t, []string{r.Action, r.Layer, r.Origin}, []string{"allow", "baseline", "baseline"}, "rule")
	})

	tw(t, f, "returns concatenation of all layers in order", func(t *testing.T) {
		defaults := SynthesizeDefaults("ask", "")
		baseline := SynthesizeBaseline(Ruleset{mcpAllow})
		config := Ruleset{{Surface: "bash", Pattern: "rm -rf *", Action: "deny", Origin: "global"}}
		composed := ComposeRuleset(defaults, baseline, config)
		eq(t, len(composed), len(defaults)+len(baseline)+len(config), "len")
		eq(t, composed[0], defaults[0], "first")
		eq(t, composed[len(composed)-1], config[0], "last")
	})
	tw(t, f, "defaults come first (lowest priority), config comes last (highest priority)", func(t *testing.T) {
		config := Ruleset{{Surface: "bash", Pattern: "*", Action: "deny", Layer: "config", Origin: "global"}}
		r := ev("bash", "echo hello", ComposeRuleset(SynthesizeDefaults("ask", ""), nil, config))
		eq(t, []string{r.Action, r.Layer}, []string{"deny", "config"}, "rule")
	})
	tw(t, f, "config beats default for matching patterns", func(t *testing.T) {
		config := Ruleset{{Surface: "read", Pattern: "*", Action: "allow", Layer: "config", Origin: "global"}}
		r := ev("read", "*", ComposeRuleset(SynthesizeDefaults("ask", ""), nil, config))
		eq(t, []string{r.Action, r.Layer}, []string{"allow", "config"}, "rule")
	})
	tw(t, f, "baseline beats default but config beats baseline", func(t *testing.T) {
		baseline := Ruleset{{Surface: "mcp", Pattern: "mcp_status", Action: "allow", Layer: "baseline", Origin: "baseline"}}
		config := Ruleset{{Surface: "mcp", Pattern: "mcp_status", Action: "deny", Layer: "config", Origin: "global"}}
		r := ev("mcp", "mcp_status", ComposeRuleset(SynthesizeDefaults("ask", ""), baseline, config))
		eq(t, []string{r.Action, r.Layer}, []string{"deny", "config"}, "rule")
	})
	tw(t, f, "config beats baseline for specific patterns", func(t *testing.T) {
		baseline := Ruleset{{Surface: "mcp", Pattern: "mcp_status", Action: "allow", Layer: "baseline", Origin: "baseline"}}
		config := Ruleset{{Surface: "mcp", Pattern: "exa_web_search", Action: "allow", Layer: "config", Origin: "global"}}
		r := ev("mcp", "exa_web_search", ComposeRuleset(SynthesizeDefaults("ask", ""), baseline, config))
		eq(t, []string{r.Action, r.Layer}, []string{"allow", "config"}, "rule")
	})
	tw(t, f, "handles empty layers gracefully", func(t *testing.T) {
		defaults := SynthesizeDefaults("ask", "")
		eq(t, ComposeRuleset(defaults, nil, nil), defaults, "composed")
	})
}

// ---- permission-gate ----

var (
	policyRuleSource = DecisionSource{Kind: "rule", Surface: "bash", Pattern: sp("*"), Origin: sp("global")}
	decidedByHuman   = DecisionSource{Kind: "user", Via: "dialog"}
	decidedByAbsent  = DecisionSource{Kind: "unavailable", Reason: sp("No live authority was reachable for this session")}
)

type gateRig struct {
	params  PermissionGateParams
	logs    []string
	prompts int
}

func makeGateParams(t *testing.T, state string, prompt func() (PermissionPromptDecision, error)) *gateRig {
	r := &gateRig{}
	r.params = PermissionGateParams{
		State:         state,
		WriteLog:      func(event string, extra *jsObject) { r.logs = append(r.logs, event+" "+marshalJSON(extra, "")) },
		LogContext:    jo(t, `{"source":"test"}`),
		DecidedByRule: policyRuleSource,
		Messages: PermissionGateMessages{
			DenyReason: "Denied by policy.",
			// Names the decider it was handed: the gate's remaining job on this arm is to route the whole decision to the renderer.
			RefusedReason: func(d PermissionPromptDecision) string {
				if d.DenialReason != nil && *d.DenialReason != "" {
					return "Refused by " + d.DecidedBy.Kind + ". Reason: " + *d.DenialReason
				}
				return "Refused by " + d.DecidedBy.Kind + "."
			},
		},
	}
	r.params.PromptForApproval = func() (PermissionPromptDecision, error) {
		r.prompts++
		if prompt == nil {
			panic("unexpected prompt")
		}
		return prompt()
	}
	return r
}

func decision(approved bool, state PermissionDecisionState, by DecisionSource) PermissionPromptDecision {
	return PermissionPromptDecision{Approved: approved, State: state, DecidedBy: by}
}

func applyGate(t *testing.T, r *gateRig) PermissionGateResult {
	t.Helper()
	res, err := ApplyPermissionGate(r.params)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestPermissionGateTwins(t *testing.T) {
	const f = "policy/permission-gate"
	ret := func(d PermissionPromptDecision) func() (PermissionPromptDecision, error) {
		return func() (PermissionPromptDecision, error) { return d, nil }
	}

	tw(t, f, "returns block with deny reason when state is deny", func(t *testing.T) {
		eq(t, applyGate(t, makeGateParams(t, "deny", nil)), PermissionGateResult{Action: "block", DecidedBy: policyRuleSource, Reason: "Denied by policy."}, "result")
	})
	tw(t, f, "calls writeLog with policy_denied resolution", func(t *testing.T) {
		r := makeGateParams(t, "deny", nil)
		r.params.LogContext = jo(t, `{"source":"tool_call","toolName":"bash"}`)
		applyGate(t, r)
		eq(t, r.logs, []string{`permission_request.blocked {"source":"tool_call","toolName":"bash","resolution":"policy_denied","decidedBy":{"kind":"rule","surface":"bash","pattern":"*","origin":"global"}}`}, "logs")
	})
	tw(t, f, "does not call promptForApproval when state is deny", func(t *testing.T) {
		r := makeGateParams(t, "deny", nil)
		applyGate(t, r)
		eq(t, r.prompts, 0, "prompts")
	})

	unavailable := PermissionPromptDecision{Approved: false, State: DecisionDenied, DecidedBy: decidedByAbsent, ConfirmationUnavailable: true}
	tw(t, f, "blocks with the refusal render of the absent-authority decision", func(t *testing.T) {
		r := makeGateParams(t, "ask", ret(unavailable))
		eq(t, applyGate(t, r), PermissionGateResult{Action: "block", DecidedBy: decidedByAbsent, Reason: "Refused by unavailable."}, "result")
	})
	tw(t, f, "does not call writeLog when confirmation is unavailable (logged by the prompter)", func(t *testing.T) {
		r := makeGateParams(t, "ask", ret(unavailable))
		r.params.LogContext = jo(t, `{"source":"skill_read","skillName":"foo"}`)
		applyGate(t, r)
		eq(t, len(r.logs), 0, "logs")
	})
	tw(t, f, "passes the decision's denial reason to the refusal render", func(t *testing.T) {
		d := unavailable
		d.DenialReason = sp("Session 'parent-1' did not answer within 600s.")
		r := makeGateParams(t, "ask", ret(d))
		eq(t, applyGate(t, r), PermissionGateResult{Action: "block", DecidedBy: decidedByAbsent, Reason: "Refused by unavailable. Reason: Session 'parent-1' did not answer within 600s."}, "result")
	})

	tw(t, f, "blocks with the refusal render of the human's decision", func(t *testing.T) {
		r := makeGateParams(t, "ask", ret(decision(false, DecisionDenied, decidedByHuman)))
		eq(t, applyGate(t, r), PermissionGateResult{Action: "block", DecidedBy: decidedByHuman, Reason: "Refused by user."}, "result")
	})
	tw(t, f, "passes the human's denial reason to the refusal render", func(t *testing.T) {
		d := decision(false, DecisionDeniedWithReason, decidedByHuman)
		d.DenialReason = sp("not now")
		r := makeGateParams(t, "ask", ret(d))
		eq(t, applyGate(t, r), PermissionGateResult{Action: "block", DecidedBy: decidedByHuman, Reason: "Refused by user. Reason: not now"}, "result")
	})
	tw(t, f, "does not call writeLog when user rejects (logged by promptPermission)", func(t *testing.T) {
		r := makeGateParams(t, "ask", ret(decision(false, DecisionDenied, decidedByHuman)))
		applyGate(t, r)
		eq(t, len(r.logs), 0, "logs")
	})

	tw(t, f, "returns allow when user approves", func(t *testing.T) {
		r := makeGateParams(t, "ask", ret(decision(true, DecisionApproved, decidedByHuman)))
		eq(t, applyGate(t, r), PermissionGateResult{Action: "allow", DecidedBy: decidedByHuman}, "result")
	})
	tw(t, f, "does not call writeLog when user approves", func(t *testing.T) {
		r := makeGateParams(t, "ask", ret(decision(true, DecisionApproved, decidedByHuman)))
		applyGate(t, r)
		eq(t, len(r.logs), 0, "logs")
	})

	tw(t, f, "reports a session grant when the decision is approved_for_session and the ask carried a suggestion", func(t *testing.T) {
		r := makeGateParams(t, "ask", ret(decision(true, DecisionApprovedForSession, decidedByHuman)))
		r.params.CanGrantForSession = true
		eq(t, applyGate(t, r), PermissionGateResult{Action: "allow", DecidedBy: decidedByHuman, SessionGrant: &SessionGrant{Width: "proven"}}, "result")
	})
	tw(t, f, "records the width the decision names", func(t *testing.T) {
		d := decision(true, DecisionApprovedForSession, decidedByHuman)
		d.SessionGrantWidth = "family"
		r := makeGateParams(t, "ask", ret(d))
		r.params.CanGrantForSession = true
		eq(t, applyGate(t, r), PermissionGateResult{Action: "allow", DecidedBy: decidedByHuman, SessionGrant: &SessionGrant{Width: "family"}}, "result")
	})
	tw(t, f, "reports no session grant when the decision is approved (once)", func(t *testing.T) {
		r := makeGateParams(t, "ask", ret(decision(true, DecisionApproved, decidedByHuman)))
		r.params.CanGrantForSession = true
		eq(t, applyGate(t, r), PermissionGateResult{Action: "allow", DecidedBy: decidedByHuman}, "result")
	})
	tw(t, f, "reports no session grant when the ask carried no suggestion", func(t *testing.T) {
		r := makeGateParams(t, "ask", ret(decision(true, DecisionApprovedForSession, decidedByHuman)))
		r.params.CanGrantForSession = false
		eq(t, applyGate(t, r), PermissionGateResult{Action: "allow", DecidedBy: decidedByHuman}, "result")
	})
	tw(t, f, "reports no session grant when the user denies", func(t *testing.T) {
		r := makeGateParams(t, "ask", ret(decision(false, DecisionDenied, decidedByHuman)))
		r.params.CanGrantForSession = true
		eq(t, applyGate(t, r), PermissionGateResult{Action: "block", DecidedBy: decidedByHuman, Reason: "Refused by user."}, "result")
	})

	tw(t, f, "returns allow immediately when state is allow", func(t *testing.T) {
		eq(t, applyGate(t, makeGateParams(t, "allow", nil)), PermissionGateResult{Action: "allow", DecidedBy: policyRuleSource}, "result")
	})
	tw(t, f, "does not call writeLog when state is allow", func(t *testing.T) {
		r := makeGateParams(t, "allow", nil)
		applyGate(t, r)
		eq(t, len(r.logs), 0, "logs")
	})
	tw(t, f, "does not call promptForApproval when state is allow", func(t *testing.T) {
		r := makeGateParams(t, "allow", nil)
		applyGate(t, r)
		eq(t, r.prompts, 0, "prompts")
	})
}

// A state outside the original's three is never an allow in the port, and a rejected prompt propagates.
func TestPermissionGateFailsClosed(t *testing.T) {
	for _, state := range []string{"", "ALLOW", "yolo", "deny "} {
		res := applyGate(t, makeGateParams(t, state, nil))
		eq(t, res.Action, "block", "state "+state)
	}
	r := makeGateParams(t, "ask", func() (PermissionPromptDecision, error) { return PermissionPromptDecision{}, errTest })
	if _, err := ApplyPermissionGate(r.params); err == nil {
		t.Fatal("a rejected prompt must propagate")
	}
}

type testErr string

func (e testErr) Error() string { return string(e) }

const errTest = testErr("prompt rejected")

// ---- permission-resolver ----

type fakeCheck struct {
	intent ResolvedAccessIntent
	rules  Ruleset
}

type fakeManager struct {
	checks            []fakeCheck
	checkFn           func(ResolvedAccessIntent) PermissionCheckResult
	configured        []string
	toolPermission    string
	toolPermissionArg [2]any
	fullyDenied       bool
	fullyDeniedArg    [2]any
	issues            []string
	issuesArg         any
}

func newFakeManager() *fakeManager {
	return &fakeManager{toolPermission: "allow", issues: []string{}}
}

func (m *fakeManager) ConfigureForCwd(cwd string) { m.configured = append(m.configured, cwd) }
func (m *fakeManager) Check(intent ResolvedAccessIntent, rules Ruleset) PermissionCheckResult {
	m.checks = append(m.checks, fakeCheck{intent, rules})
	if m.checkFn != nil {
		return m.checkFn(intent)
	}
	return PermissionCheckResult{State: "allow", ToolName: "read", Source: "tool", Origin: "builtin"}
}
func (m *fakeManager) GetToolPermission(toolName string, agentName *string) string {
	m.toolPermissionArg = [2]any{toolName, agentName}
	return m.toolPermission
}
func (m *fakeManager) IsToolFullyDenied(toolName string, agentName *string) bool {
	m.fullyDeniedArg = [2]any{toolName, agentName}
	return m.fullyDenied
}
func (m *fakeManager) GetConfigIssues(agentName *string) []string {
	m.issuesArg = agentName
	return m.issues
}

func intentSurfaces(m *fakeManager) []string {
	var out []string
	for _, c := range m.checks {
		out = append(out, intentSurface(c.intent))
	}
	return out
}

func TestPermissionResolverTwins(t *testing.T) {
	const f = "policy/permission-resolver"
	toolIntent := func(surface, cmd string, agent *string) ToolAccessIntent {
		return ToolAccessIntent{Surface: surface, Input: jo(t, `{"command":"`+cmd+`"}`), AgentName: agent}
	}
	ap := func(path, cwd string) *AccessPath {
		return AccessPathForPath(path, AccessPathForPathOptions{Cwd: cwd, Flavor: PosixPathFlavor})
	}
	sess := func(surface, pattern string) Rule { return rule(surface, pattern, "allow", "session", "session") }

	tw(t, f, "forwards a tool intent with the empty session ruleset", func(t *testing.T) {
		pm := newFakeManager()
		in := toolIntent("bash", "ls", sp("agent-x"))
		NewPermissionResolver(pm, &SessionRules{}).Resolve(in)
		eq(t, pm.checks, []fakeCheck{{in, Ruleset{}}}, "checks")
	})
	tw(t, f, "applies a recorded session approval on the next resolve", func(t *testing.T) {
		pm := newFakeManager()
		sr := &SessionRules{}
		r := NewPermissionResolver(pm, sr)
		sr.RecordSessionApproval(SessionApprovalSingle("bash", "git *"))
		r.Resolve(toolIntent("bash", "git status", nil))
		passed := pm.checks[0].rules
		eq(t, len(passed), 1, "len")
		eq(t, []string{passed[0].Surface, passed[0].Pattern, passed[0].Action}, []string{"bash", "git *", "allow"}, "rule")
	})
	tw(t, f, "returns the manager's check result", func(t *testing.T) {
		pm := newFakeManager()
		want := PermissionCheckResult{State: "deny", ToolName: "bash", Source: "bash", Origin: "global", MatchedPattern: sp("rm *")}
		pm.checkFn = func(ResolvedAccessIntent) PermissionCheckResult { return want }
		eq(t, NewPermissionResolver(pm, &SessionRules{}).Resolve(toolIntent("bash", "rm -rf /", nil)), want, "result")
	})

	tw(t, f, "applies a recorded session approval on the next call", func(t *testing.T) {
		pm := newFakeManager()
		sr := &SessionRules{}
		r := NewPermissionResolver(pm, sr)
		sr.RecordSessionApproval(SessionApprovalSingle("path", "src/*"))
		r.Resolve(AccessPathAccessIntent{Surface: "path", Path: ap("src/a.ts", "/proj")})
		// The approval expanded into both directional members at record time.
		eq(t, pm.checks[0].rules, Ruleset{sess("path_read", "src/*"), sess("path_write", "src/*")}, "rules")
	})

	tw(t, f, "unwraps the AccessPath via matchValues() into a path-values intent", func(t *testing.T) {
		pm := newFakeManager()
		accessPath := ap("/tmp/x", "/workspace")
		NewPermissionResolver(pm, &SessionRules{}).Resolve(AccessPathAccessIntent{Surface: "external_directory", Path: accessPath, AgentName: sp("agent-x")})
		var intents []ResolvedAccessIntent
		for _, c := range pm.checks {
			intents = append(intents, c.intent)
		}
		// The family folds, so each member sees the same unwrapped values.
		eq(t, intents, []ResolvedAccessIntent{
			PathValuesAccessIntent{Surface: "external_directory_read", Values: accessPath.MatchValues(), AgentName: sp("agent-x")},
			PathValuesAccessIntent{Surface: "external_directory_write", Values: accessPath.MatchValues(), AgentName: sp("agent-x")},
		}, "intents")
	})
	tw(t, f, "returns the manager's check result for an access-path intent", func(t *testing.T) {
		pm := newFakeManager()
		pm.checkFn = func(ResolvedAccessIntent) PermissionCheckResult {
			return PermissionCheckResult{State: "deny", ToolName: "external_directory", Source: "special", Origin: "global", MatchedPattern: sp("/tmp/*")}
		}
		res := NewPermissionResolver(pm, &SessionRules{}).Resolve(AccessPathAccessIntent{Surface: "external_directory", Path: ap("/tmp/x", "/workspace")})
		eq(t, res.State, "deny", "state")
		eq(t, res.MatchedPattern, sp("/tmp/*"), "pattern")
	})

	tw(t, f, "passes a path-values intent to the manager unchanged, with the composed session ruleset", func(t *testing.T) {
		pm := newFakeManager()
		sr := &SessionRules{}
		r := NewPermissionResolver(pm, sr)
		sr.RecordSessionApproval(SessionApprovalSingle("external_directory", "/tmp/*"))
		r.ResolvePrefixed(PathValuesAccessIntent{Surface: "external_directory", Values: []string{"/tmp/x", "/real/tmp/x"}, AgentName: sp("Explore")})
		// Values and agentName pass through untouched; only the surface varies, one call per directional member of the family.
		eq(t, pm.checks[0].intent, PathValuesAccessIntent{Surface: "external_directory_read", Values: []string{"/tmp/x", "/real/tmp/x"}, AgentName: sp("Explore")}, "intent")
		var got [][3]string
		for _, x := range pm.checks[0].rules {
			got = append(got, [3]string{x.Surface, x.Pattern, x.Action})
		}
		eq(t, got, [][3]string{{"external_directory_read", "/tmp/*", "allow"}, {"external_directory_write", "/tmp/*", "allow"}}, "rules")
	})
	tw(t, f, "returns the manager's check result for a path-values intent", func(t *testing.T) {
		pm := newFakeManager()
		pm.checkFn = func(ResolvedAccessIntent) PermissionCheckResult {
			return PermissionCheckResult{State: "allow", ToolName: "path", Source: "special", Origin: "global", MatchedPattern: sp("src/**")}
		}
		res := NewPermissionResolver(pm, &SessionRules{}).ResolvePrefixed(PathValuesAccessIntent{Surface: "path", Values: []string{"/worktree/issue-42/src/foo.ts", "src/foo.ts"}, AgentName: sp("Explore")})
		eq(t, res.State, "allow", "state")
		eq(t, res.MatchedPattern, sp("src/**"), "pattern")
	})

	tw(t, f, "passes the command and its spellings to the manager unchanged, with the session ruleset", func(t *testing.T) {
		pm := newFakeManager()
		sr := &SessionRules{}
		r := NewPermissionResolver(pm, sr)
		sr.RecordSessionApproval(SessionApprovalSingle("bash", "~/bin/x"))
		in := BashCommandAccessIntent{Surface: "bash", Command: "~/bin/x", Spellings: []string{"/h/bin/x"}, AgentName: sp("agent-x")}
		r.Resolve(in)
		eq(t, pm.checks[0].intent, ResolvedAccessIntent(in), "intent")
		eq(t, len(pm.checks[0].rules), 1, "len")
		eq(t, pm.checks[0].rules[0].Pattern, "~/bin/x", "pattern")
	})

	tw(t, f, "delegates to manager.check as a tool intent without session rules", func(t *testing.T) {
		pm := newFakeManager()
		input := jo(t, `{"command":"ls"}`)
		NewPermissionResolver(pm, &SessionRules{}).CheckPermission("bash", input, sp("agent-1"), nil)
		eq(t, pm.checks, []fakeCheck{{ToolAccessIntent{Surface: "bash", Input: input, AgentName: sp("agent-1")}, nil}}, "checks")
	})
	tw(t, f, "passes optional sessionRules as the second arg to check", func(t *testing.T) {
		pm := newFakeManager()
		input := jo(t, `{"command":"ls"}`)
		extra := Ruleset{{Surface: "bash", Pattern: "*", Action: "allow", Origin: "session"}}
		NewPermissionResolver(pm, &SessionRules{}).CheckPermission("bash", input, nil, extra)
		eq(t, pm.checks, []fakeCheck{{ToolAccessIntent{Surface: "bash", Input: input}, extra}}, "checks")
	})

	tw(t, f, "delegates to permissionManager.getToolPermission", func(t *testing.T) {
		pm := newFakeManager()
		pm.toolPermission = "deny"
		res := NewPermissionResolver(pm, &SessionRules{}).GetToolPermission("write", sp("my-agent"))
		eq(t, pm.toolPermissionArg, [2]any{"write", sp("my-agent")}, "args")
		eq(t, res, "deny", "result")
	})
	tw(t, f, "delegates to permissionManager.isToolFullyDenied", func(t *testing.T) {
		pm := newFakeManager()
		pm.fullyDenied = true
		res := NewPermissionResolver(pm, &SessionRules{}).IsToolFullyDenied("write", sp("my-agent"))
		eq(t, pm.fullyDeniedArg, [2]any{"write", sp("my-agent")}, "args")
		eq(t, res, true, "result")
	})

	surfaceKeyed := func(bySurface map[string]string) *fakeManager {
		pm := newFakeManager()
		pm.checkFn = func(intent ResolvedAccessIntent) PermissionCheckResult {
			s := intentSurface(intent)
			state, ok := bySurface[s]
			if !ok {
				state = "ask"
			}
			return PermissionCheckResult{State: state, ToolName: s, Source: "special", Origin: "global", MatchedPattern: sp(s + "-pattern")}
		}
		return pm
	}
	tw(t, f, "consults both directional members for a bare family surface", func(t *testing.T) {
		pm := surfaceKeyed(map[string]string{"path_read": "allow"})
		NewPermissionResolver(pm, &SessionRules{}).Resolve(AccessPathAccessIntent{Surface: "path", Path: ap("/tmp/x", "/workspace"), AgentName: sp("agent-x")})
		eq(t, intentSurfaces(pm), []string{"path_read", "path_write"}, "surfaces")
	})
	tw(t, f, "returns the losing member's own result, so blame names the surface that decided", func(t *testing.T) {
		pm := surfaceKeyed(map[string]string{"path_read": "allow", "path_write": "deny"})
		res := NewPermissionResolver(pm, &SessionRules{}).Resolve(AccessPathAccessIntent{Surface: "path", Path: ap("/tmp/x", "/workspace")})
		eq(t, res, PermissionCheckResult{State: "deny", ToolName: "path_write", Source: "special", Origin: "global", MatchedPattern: sp("path_write-pattern")}, "result")
	})
	tw(t, f, "never masks a deny into an approvable ask (#712)", func(t *testing.T) {
		pm := surfaceKeyed(map[string]string{"external_directory_read": "deny", "external_directory_write": "allow"})
		res := NewPermissionResolver(pm, &SessionRules{}).ResolvePrefixed(PathValuesAccessIntent{Surface: "external_directory", Values: []string{"/tmp/x"}, AgentName: sp("agent-x")})
		eq(t, res.State, "deny", "state")
		eq(t, res.ToolName, "external_directory_read", "tool")
	})
	tw(t, f, "keeps the read member on a tie, per the members' normative order", func(t *testing.T) {
		pm := surfaceKeyed(map[string]string{"path_read": "ask", "path_write": "ask"})
		res := NewPermissionResolver(pm, &SessionRules{}).ResolvePrefixed(PathValuesAccessIntent{Surface: "path", Values: []string{"/tmp/x"}})
		eq(t, res.ToolName, "path_read", "tool")
	})
	tw(t, f, "does not synthesize a matchedPattern when neither member matched (#58)", func(t *testing.T) {
		pm := newFakeManager()
		pm.checkFn = func(intent ResolvedAccessIntent) PermissionCheckResult {
			return PermissionCheckResult{State: "ask", ToolName: intentSurface(intent), Source: "special", Origin: "builtin"}
		}
		res := NewPermissionResolver(pm, &SessionRules{}).ResolvePrefixed(PathValuesAccessIntent{Surface: "path", Values: []string{"/tmp/x"}})
		eq(t, res.MatchedPattern, (*string)(nil), "pattern")
	})
	tw(t, f, "passes the session ruleset to every member query", func(t *testing.T) {
		pm := surfaceKeyed(map[string]string{})
		sr := &SessionRules{}
		r := NewPermissionResolver(pm, sr)
		sr.Approve("path", "/tmp/*")
		r.ResolvePrefixed(PathValuesAccessIntent{Surface: "path", Values: []string{"/tmp/x"}})
		eq(t, len(pm.checks), 2, "calls")
		eq(t, pm.checks[0].rules, pm.checks[1].rules, "same rules")
		eq(t, len(pm.checks[0].rules), 2, "len")
	})
	tw(t, f, "does not fold a directional surface — it names one member already", func(t *testing.T) {
		pm := surfaceKeyed(map[string]string{"path_read": "allow"})
		res := NewPermissionResolver(pm, &SessionRules{}).ResolvePrefixed(PathValuesAccessIntent{Surface: "path_read", Values: []string{"/tmp/x"}})
		eq(t, len(pm.checks), 1, "calls")
		eq(t, res.State, "allow", "state")
	})
	tw(t, f, "does not fold a non-path surface", func(t *testing.T) {
		pm := surfaceKeyed(map[string]string{"bash": "allow"})
		NewPermissionResolver(pm, &SessionRules{}).Resolve(toolIntent("bash", "ls", nil))
		eq(t, len(pm.checks), 1, "calls")
	})
	tw(t, f, "delegates to permissionManager.getConfigIssues", func(t *testing.T) {
		pm := newFakeManager()
		pm.issues = []string{"issue-1"}
		res := NewPermissionResolver(pm, &SessionRules{}).GetConfigIssues(sp("agent-1"))
		eq(t, pm.issuesArg, sp("agent-1"), "arg")
		eq(t, res, []string{"issue-1"}, "result")
	})
}

// ---- permission-session ----

type fakeForwarding struct {
	started []ForwarderContext
	stopped int
}

func (f *fakeForwarding) Start(ctx ForwarderContext) { f.started = append(f.started, ctx) }
func (f *fakeForwarding) Stop()                      { f.stopped++ }

type fakeAuthSelection struct {
	activated   []SessionContext
	deactivated int
}

func (a *fakeAuthSelection) Activate(ctx SessionContext) { a.activated = append(a.activated, ctx) }
func (a *fakeAuthSelection) Deactivate()                 { a.deactivated++ }

type fakeConfigStore struct {
	current   PermissionSystemExtensionConfig
	refreshed []struct {
		cwd     string
		trusted bool
	}
	logged []string
}

func (s *fakeConfigStore) Current() PermissionSystemExtensionConfig { return s.current }
func (s *fakeConfigStore) Refresh(cwd string, trusted bool) {
	s.refreshed = append(s.refreshed, struct {
		cwd     string
		trusted bool
	}{cwd, trusted})
}
func (s *fakeConfigStore) LogResolvedPaths(cwd string) { s.logged = append(s.logged, cwd) }

type fakeNotifier struct {
	notified [][2]string
	statuses [][2]string
}

func (n *fakeNotifier) Notify(message, level string) {
	n.notified = append(n.notified, [2]string{message, level})
}
func (n *fakeNotifier) SetStatus(key, text string) {
	n.statuses = append(n.statuses, [2]string{key, text})
}

func makeSessionCtx(cwd string, hasUI bool) (SessionContext, *fakeNotifier) {
	n := &fakeNotifier{}
	return SessionContext{ForwarderContext: ForwarderContext{Cwd: cwd, HasUI: hasUI}, Notifier: n}, n
}

type sessionRig struct {
	session *PermissionSession
	pm      *fakeManager
	rules   *SessionRules
	fwd     *fakeForwarding
	auth    *fakeAuthSelection
	store   *fakeConfigStore
	paths   ExtensionPaths
}

func makeRealSession(flavor *PathFlavor) *sessionRig {
	r := &sessionRig{pm: newFakeManager(), rules: &SessionRules{}, fwd: &fakeForwarding{}, auth: &fakeAuthSelection{}, store: &fakeConfigStore{current: defaultExtensionConfig()}}
	r.paths = ExtensionPaths{
		AgentDir: "/test/agent", SessionsDir: "/test/agent/sessions", SubagentSessionsDir: "/test/agent/subagent-sessions",
		ForwardingDir: "/test/agent/sessions/permission-forwarding", GlobalLogsDir: "/test/agent/logs",
		PiInfrastructureDirs: []string{"/test/agent", "/test/agent/git"}, PiInfrastructureExcludedDirs: []string{"/test/agent/logs"},
	}
	fl := PathFlavorForPlatform(runtime.GOOS)
	if flavor != nil {
		fl = *flavor
	}
	r.session = NewPermissionSession(r.paths, r.fwd, r.pm, r.rules, r.store, r.auth, fl)
	return r
}

func makeSkillEntry(name string) SkillPromptEntry {
	return SkillPromptEntry{Name: name, Description: name + " skill", Location: "/" + name + "/SKILL.md", State: "allow",
		NormalizedLocation: "/" + name + "/SKILL.md", NormalizedBaseDir: "/" + name}
}

var sessionRegisteredTools = map[string]bool{"read": true, "ls": true}

// withhold drives one turn that withholds deniedTool from the observed active set.
func withhold(s *PermissionSession, deniedTool string, active ...string) ToolSurfaceResolution {
	return s.ResolveExposedTools(ToolSurfaceObservation{Active: active, Registered: sessionRegisteredTools}, func(n string) bool { return n != deniedTool })
}

// exposeAll drives one turn whose policy withholds nothing.
func exposeAll(s *PermissionSession, active ...string) ToolSurfaceResolution {
	return s.ResolveExposedTools(ToolSurfaceObservation{Active: active, Registered: sessionRegisteredTools}, func(string) bool { return true })
}

// stubAgentLookups stands in for the original's mock of the active-agent module.
type stubAgentLookups struct {
	fromSession *string
	fromPrompt  *string
}

func (r *sessionRig) stubAgents() *stubAgentLookups {
	s := &stubAgentLookups{}
	r.session.agentNameLookup = func(ActiveAgentContext) (*string, error) { return s.fromSession, nil }
	r.session.promptAgentNameLookup = func(string) *string { return s.fromPrompt }
	return s
}

func TestPermissionSessionTwins(t *testing.T) {
	const f = "session/permission-session"
	cfg := func(cwd string) SessionContext { c, _ := makeSessionCtx(cwd, true); return c }

	tw(t, f, "stores the context on activate", func(t *testing.T) {
		r := makeRealSession(nil)
		ctx := cfg("/test/project")
		r.session.Activate(ctx)
		eq(t, r.fwd.started, []ForwarderContext{ctx.ForwarderContext}, "started")
	})
	tw(t, f, "clears context on deactivate", func(t *testing.T) {
		r := makeRealSession(nil)
		r.session.Activate(cfg("/p"))
		r.session.Deactivate()
		eq(t, r.fwd.stopped, 1, "stopped")
	})
	tw(t, f, "forwards activate to the authorizer selection", func(t *testing.T) {
		r := makeRealSession(nil)
		ctx := cfg("/p")
		r.session.Activate(ctx)
		eq(t, len(r.auth.activated), 1, "activated")
		eq(t, r.auth.activated[0].Cwd, ctx.Cwd, "ctx")
	})
	tw(t, f, "forwards deactivate to the authorizer selection", func(t *testing.T) {
		r := makeRealSession(nil)
		r.session.Activate(cfg("/p"))
		r.session.Deactivate()
		eq(t, r.auth.deactivated, 1, "deactivated")
	})

	tw(t, f, "configures the injected PermissionManager for the context cwd when trusted", func(t *testing.T) {
		r := makeRealSession(nil)
		r.session.ResetForNewSession(cfg("/new/project"), true)
		eq(t, r.pm.configured, []string{"/new/project"}, "configured")
	})
	tw(t, f, "withholds the project cwd (global-only) when the project is untrusted", func(t *testing.T) {
		r := makeRealSession(nil)
		r.session.ResetForNewSession(cfg("/new/project"), false)
		eq(t, r.pm.configured, []string{""}, "configured (undefined)")
	})
	tw(t, f, "clears skill entries", func(t *testing.T) {
		r := makeRealSession(nil)
		r.session.SetActiveSkillEntries([]SkillPromptEntry{makeSkillEntry("test")})
		eq(t, len(r.session.GetActiveSkillEntries()), 1, "len")
		r.session.ResetForNewSession(cfg("/p"), true)
		eq(t, len(r.session.GetActiveSkillEntries()), 0, "after reset")
	})
	tw(t, f, "forgets the tool-surface baseline, so the new session reseeds from pi", func(t *testing.T) {
		r := makeRealSession(nil)
		withhold(r.session, "ls", "read", "ls")
		r.session.ResetForNewSession(cfg("/p"), true)
		eq(t, exposeAll(r.session, "read").exposed, []string{"read"}, "exposed")
	})
	tw(t, f, "starts forwarding with the new context", func(t *testing.T) {
		r := makeRealSession(nil)
		ctx := cfg("/p")
		r.session.ResetForNewSession(ctx, true)
		eq(t, r.fwd.started, []ForwarderContext{ctx.ForwarderContext}, "started")
	})
	tw(t, f, "activates the new context", func(t *testing.T) {
		r := makeRealSession(nil)
		ctx := cfg("/p")
		r.session.ResetForNewSession(ctx, true)
		// Verify the context is stored by resolving the agent name
		agents := r.stubAgents()
		agents.fromSession = sp("test-agent")
		name, err := r.session.ResolveAgentName(ctx, "")
		eq(t, []any{name, err}, []any{sp("test-agent"), error(nil)}, "name")
		eq(t, r.session.GetRuntimeContext() != nil, true, "context")
	})

	tw(t, f, "returns a normalizer bound to the reset session cwd", func(t *testing.T) {
		r := makeRealSession(&PosixPathFlavor)
		r.session.ResetForNewSession(cfg("/projects/app"), true)
		eq(t, r.session.GetPathNormalizer().ForPath("src/foo.ts", nil).Value(), "/projects/app/src/foo.ts", "value")
	})
	tw(t, f, "rebinds the normalizer cwd on a subsequent reset", func(t *testing.T) {
		r := makeRealSession(&PosixPathFlavor)
		r.session.ResetForNewSession(cfg("/projects/app"), true)
		r.session.ResetForNewSession(cfg("/projects/other"), true)
		eq(t, r.session.GetPathNormalizer().ForPath("a.ts", nil).Value(), "/projects/other/a.ts", "value")
	})
	tw(t, f, "binds the normalizer on activate, before any reset (no fail-open)", func(t *testing.T) {
		r := makeRealSession(&PosixPathFlavor)
		r.session.Activate(cfg("/projects/activated"))
		eq(t, r.session.GetPathNormalizer().ForPath("a.ts", nil).Value(), "/projects/activated/a.ts", "value")
	})
	tw(t, f, "builds a win32 normalizer when constructed with the win32 flavor", func(t *testing.T) {
		r := makeRealSession(&Win32PathFlavor)
		r.session.ResetForNewSession(cfg(`C:\Projects\App`), true)
		eq(t, r.session.GetPathNormalizer().ForPath(`src\foo.ts`, nil).Value(), `c:\projects\app\src\foo.ts`, "value")
	})

	tw(t, f, "clears session rules", func(t *testing.T) {
		r := makeRealSession(nil)
		r.rules.RecordSessionApproval(SessionApprovalSingle("bash", "*"))
		eq(t, len(r.rules.GetRuleset()), 1, "before")
		r.session.Shutdown()
		eq(t, r.rules.GetRuleset(), Ruleset{}, "after")
	})
	tw(t, f, "clears skill entries", func(t *testing.T) {
		r := makeRealSession(nil)
		r.session.SetActiveSkillEntries([]SkillPromptEntry{makeSkillEntry("s")})
		r.session.Shutdown()
		eq(t, len(r.session.GetActiveSkillEntries()), 0, "after")
	})
	tw(t, f, "forgets the tool-surface baseline", func(t *testing.T) {
		r := makeRealSession(nil)
		withhold(r.session, "ls", "read", "ls")
		r.session.Shutdown()
		eq(t, exposeAll(r.session, "read").exposed, []string{"read"}, "exposed")
	})
	tw(t, f, "stops forwarding and deactivates context", func(t *testing.T) {
		r := makeRealSession(nil)
		r.session.Activate(cfg("/p"))
		r.session.Shutdown()
		eq(t, r.fwd.stopped, 1, "stopped")
	})

	tw(t, f, "get/set skill entries", func(t *testing.T) {
		r := makeRealSession(nil)
		entries := []SkillPromptEntry{makeSkillEntry("a"), makeSkillEntry("b")}
		r.session.SetActiveSkillEntries(entries)
		eq(t, r.session.GetActiveSkillEntries(), entries, "entries")
	})

	tw(t, f, "returns name from session context", func(t *testing.T) {
		r := makeRealSession(nil)
		r.stubAgents().fromSession = sp("ctx-agent")
		name, _ := r.session.ResolveAgentName(cfg("/p"), "")
		eq(t, name, sp("ctx-agent"), "name")
	})
	tw(t, f, "falls back to system prompt", func(t *testing.T) {
		r := makeRealSession(nil)
		r.stubAgents().fromPrompt = sp("prompt-agent")
		name, _ := r.session.ResolveAgentName(cfg("/p"), "system prompt")
		eq(t, name, sp("prompt-agent"), "name")
	})
	tw(t, f, "falls back to last known name", func(t *testing.T) {
		r := makeRealSession(nil)
		agents := r.stubAgents()
		agents.fromSession = sp("first-agent")
		r.session.ResolveAgentName(cfg("/p"), "")
		agents.fromSession, agents.fromPrompt = nil, nil
		name, _ := r.session.ResolveAgentName(cfg("/p"), "")
		eq(t, name, sp("first-agent"), "name")
	})
	tw(t, f, "exposes lastKnownActiveAgentName", func(t *testing.T) {
		r := makeRealSession(nil)
		eq(t, r.session.LastKnownActiveAgentName(), (*string)(nil), "initial")
		r.stubAgents().fromSession = sp("named")
		r.session.ResolveAgentName(cfg("/p"), "")
		eq(t, r.session.LastKnownActiveAgentName(), sp("named"), "after")
	})

	tw(t, f, "getInfrastructureReadScope combines piInfrastructureDirs and piInfrastructureReadPaths", func(t *testing.T) {
		r := makeRealSession(nil)
		r.store.current = PermissionSystemExtensionConfig{PiInfrastructureReadPaths: []string{"/extra/path"}}
		eq(t, r.session.GetInfrastructureReadScope(), InfrastructureReadScope{
			Dirs: []string{"/test/agent", "/test/agent/git", "/extra/path"}, ExcludedDirs: []string{"/test/agent/logs"}}, "scope")
	})
	tw(t, f, "getInfrastructureReadScope holds only piInfrastructureDirs when config omits the field", func(t *testing.T) {
		eq(t, makeRealSession(nil).session.GetInfrastructureReadScope(), InfrastructureReadScope{
			Dirs: []string{"/test/agent", "/test/agent/git"}, ExcludedDirs: []string{"/test/agent/logs"}}, "scope")
	})

	tw(t, f, "refreshConfig delegates to configStore.refresh with the trust flag", func(t *testing.T) {
		r := makeRealSession(nil)
		ctx := cfg("/test/project")
		r.session.RefreshConfig(&ctx, true)
		eq(t, len(r.store.refreshed), 1, "calls")
		eq(t, []any{r.store.refreshed[0].cwd, r.store.refreshed[0].trusted}, []any{"/test/project", true}, "args")
	})
	tw(t, f, "refreshConfig forwards projectTrusted=false when untrusted", func(t *testing.T) {
		r := makeRealSession(nil)
		ctx := cfg("/test/project")
		r.session.RefreshConfig(&ctx, false)
		eq(t, []any{r.store.refreshed[0].cwd, r.store.refreshed[0].trusted}, []any{"/test/project", false}, "args")
	})

	yoloStore := func() *fakeConfigStore {
		c := defaultExtensionConfig()
		c.YoloMode = true
		return &fakeConfigStore{current: c}
	}
	tw(t, f, "syncs the status from the refreshed config when the ctx has a UI", func(t *testing.T) {
		r := makeRealSession(nil)
		r.store.current = yoloStore().current
		ctx, ui := makeSessionCtx("/p", true)
		r.session.RefreshConfig(&ctx, true)
		eq(t, ui.statuses, [][2]string{{permissionSystemStatusKey, permissionSystemYoloStatusValue}}, "statuses")
	})
	tw(t, f, "clears the status when the refreshed config leaves yolo off", func(t *testing.T) {
		r := makeRealSession(nil)
		ctx, ui := makeSessionCtx("/p", true)
		r.session.RefreshConfig(&ctx, true)
		eq(t, ui.statuses, [][2]string{{permissionSystemStatusKey, ""}}, "statuses (undefined)")
	})
	tw(t, f, "does not sync the status when the ctx has no UI", func(t *testing.T) {
		r := makeRealSession(nil)
		r.store.current = yoloStore().current
		ctx, ui := makeSessionCtx("/p", false)
		r.session.RefreshConfig(&ctx, true)
		eq(t, len(ui.statuses), 0, "statuses")
	})
	tw(t, f, "refreshes without a ctx and syncs nothing", func(t *testing.T) {
		r := makeRealSession(nil)
		r.session.RefreshConfig(nil, false)
		eq(t, []any{r.store.refreshed[0].cwd, r.store.refreshed[0].trusted}, []any{"", false}, "args (undefined cwd)")
	})

	tw(t, f, "logResolvedConfigPaths delegates to configStore.logResolvedPaths", func(t *testing.T) {
		r := makeRealSession(nil)
		r.session.LogResolvedConfigPaths()
		eq(t, len(r.store.logged), 1, "calls")
	})
	tw(t, f, "config getter delegates to configStore.current()", func(t *testing.T) {
		r := makeRealSession(nil)
		r.store.current = PermissionSystemExtensionConfig{DebugLog: true}
		eq(t, r.session.Config(), PermissionSystemExtensionConfig{DebugLog: true}, "config")
	})
	tw(t, f, "getToolPreviewLimits returns the built-in limits regardless of config", func(t *testing.T) {
		r := makeRealSession(nil)
		r.store.current = PermissionSystemExtensionConfig{}
		eq(t, r.session.GetToolPreviewLimits(), ResolveToolPreviewLimits(), "limits")
	})
	tw(t, f, "getToolPreviewLimits returns positive built-in defaults", func(t *testing.T) {
		l := makeRealSession(nil).session.GetToolPreviewLimits()
		eq(t, l.ToolInputPreviewMaxLength > 0 && l.ToolTextSummaryMaxLength > 0, true, "positive")
		eq(t, l, ToolPreviewFormatterOptions{ToolInputPreviewMaxLength: 200, ToolTextSummaryMaxLength: 80}, "values")
	})

	tw(t, f, "configures PermissionManager for current context cwd when trusted", func(t *testing.T) {
		r := makeRealSession(nil)
		r.session.Activate(cfg("/project"))
		r.session.Reload(true)
		eq(t, r.pm.configured, []string{"/project"}, "configured")
	})
	tw(t, f, "withholds the project cwd (global-only) when the project is untrusted", func(t *testing.T) {
		r := makeRealSession(nil)
		r.session.Activate(cfg("/project"))
		r.session.Reload(false)
		eq(t, r.pm.configured, []string{""}, "configured (undefined)")
	})
	tw(t, f, "clears skill entries", func(t *testing.T) {
		r := makeRealSession(nil)
		r.session.SetActiveSkillEntries([]SkillPromptEntry{makeSkillEntry("s")})
		r.session.Reload(true)
		eq(t, len(r.session.GetActiveSkillEntries()), 0, "after")
	})
	tw(t, f, "keeps the tool-surface baseline, so a relaxed rule restores its tool", func(t *testing.T) {
		// A reload is exactly when a relaxed policy arrives; reseeding the baseline from the already-filtered active set would strand
		// the tool.
		r := makeRealSession(nil)
		withhold(r.session, "ls", "read", "ls")
		r.session.Reload(true)
		eq(t, exposeAll(r.session, "read").exposed, []string{"read", "ls"}, "exposed")
	})

	tw(t, f, "returns null before activation", func(t *testing.T) { eq(t, makeRealSession(nil).session.GetRuntimeContext() == nil, true, "nil") })
	tw(t, f, "returns context after activation", func(t *testing.T) {
		r := makeRealSession(nil)
		ctx := cfg("/p")
		r.session.Activate(ctx)
		eq(t, r.session.GetRuntimeContext().Cwd, "/p", "cwd")
	})
	tw(t, f, "returns null after deactivation", func(t *testing.T) {
		r := makeRealSession(nil)
		r.session.Activate(cfg("/p"))
		r.session.Deactivate()
		eq(t, r.session.GetRuntimeContext() == nil, true, "nil")
	})

	tw(t, f, "forwards the message to ctx.ui.notify with 'warning' severity after activation", func(t *testing.T) {
		r := makeRealSession(nil)
		ctx, ui := makeSessionCtx("/p", true)
		r.session.Activate(ctx)
		r.session.Notify("something went wrong")
		eq(t, ui.notified, [][2]string{{"something went wrong", "warning"}}, "notified")
	})
	tw(t, f, "is a no-op and does not throw before activation", func(t *testing.T) { makeRealSession(nil).session.Notify("msg") })
	tw(t, f, "is a no-op and does not throw after deactivation", func(t *testing.T) {
		r := makeRealSession(nil)
		ctx, ui := makeSessionCtx("/p", true)
		r.session.Activate(ctx)
		r.session.Deactivate()
		r.session.Notify("msg")
		eq(t, len(ui.notified), 0, "notified")
	})
}

// ResolveAgentName must not read a session manager error as "no agent": a lower, less restrictive scope would apply.
func TestPermissionSessionResolveAgentNameReturnsLookupError(t *testing.T) {
	r := makeRealSession(nil)
	ctx, _ := makeSessionCtx("/p", true)
	ctx.SessionManager = failingEntriesManager{}
	r.session.knownAgentName = sp("previous")
	name, err := r.session.ResolveAgentName(ctx, `<active_agent name="from-prompt">`)
	if err == nil || name != nil {
		t.Fatalf("name %v, err %v", name, err)
	}
	eq(t, r.session.LastKnownActiveAgentName(), sp("previous"), "known name untouched")
}

type failingEntriesManager struct{}

func (failingEntriesManager) GetSessionID() (string, error)           { return "", errTest }
func (failingEntriesManager) GetSessionDir() (string, error)          { return "", errTest }
func (failingEntriesManager) GetEntries() ([]SessionEntryView, error) { return nil, errTest }
