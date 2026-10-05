package pi_permission_system

import (
	"fmt"
	"testing"
)

func TestEvaluate(t *testing.T) {
	const f = "policy/rule"
	px := PosixPathFlavor
	allowBashGit := rule("bash", "git *", "allow", "", "global")
	denyBashGitPush := rule("bash", "git push *", "deny", "", "global")
	allowRead := rule("read", "*", "allow", "", "global")
	askMcp := rule("mcp", "*", "ask", "", "global")
	allowSkill := rule("skill", "librarian", "allow", "", "global")
	askSpecial := rule("special", "external_directory", "ask", "", "global")
	ev := func(s, p string, r Ruleset, fl PathFlavor, d ...string) Rule {
		def := ""
		if len(d) > 0 {
			def = d[0]
		}
		return Evaluate(s, p, r, fl, def)
	}

	tw(t, f, "returns matching rule when a rule matches", func(t *testing.T) {
		eq(t, ev("bash", "git status", Ruleset{allowBashGit}, px), allowBashGit, "rule")
	})
	tw(t, f, "returns synthetic rule with 'ask' when no rules match and no defaultAction", func(t *testing.T) {
		r := ev("bash", "npm install", Ruleset{allowBashGit}, px)
		eq(t, []string{r.Surface, r.Pattern, r.Action}, []string{"bash", "npm install", "ask"}, "synthetic")
	})
	tw(t, f, "returns synthetic rule with custom defaultAction when no rules match", func(t *testing.T) {
		r := ev("bash", "npm install", Ruleset{allowBashGit}, px, "deny")
		eq(t, []string{r.Surface, r.Pattern, r.Action}, []string{"bash", "npm install", "deny"}, "synthetic")
	})
	tw(t, f, "defaultAction does not affect matched rules", func(t *testing.T) {
		eq(t, ev("bash", "git status", Ruleset{allowBashGit}, px, "deny"), allowBashGit, "rule")
	})
	tw(t, f, "returns synthetic rule for empty ruleset", func(t *testing.T) {
		r := ev("mcp", "exa_search", nil, px)
		eq(t, []string{r.Surface, r.Pattern, r.Action}, []string{"mcp", "exa_search", "ask"}, "synthetic")
	})
	tw(t, f, "matches rules for all permission surfaces", func(t *testing.T) {
		eq(t, ev("read", "src/foo.ts", Ruleset{allowRead}, px).Action, "allow", "read")
		eq(t, ev("mcp", "exa_search", Ruleset{askMcp}, px).Action, "ask", "mcp")
		eq(t, ev("skill", "librarian", Ruleset{allowSkill}, px).Action, "allow", "skill")
		eq(t, ev("special", "external_directory", Ruleset{askSpecial}, px).Action, "ask", "special")
	})
	tw(t, f, "last-match-wins: later conflicting rule overrides earlier", func(t *testing.T) {
		eq(t, ev("bash", "git push origin main", Ruleset{allowBashGit, denyBashGitPush}, px), denyBashGitPush, "rule")
	})
	tw(t, f, "last-match-wins: broad deny followed by specific allow", func(t *testing.T) {
		denyAll := rule("bash", "*", "deny", "", "global")
		allowStatus := rule("bash", "git status", "allow", "", "global")
		eq(t, ev("bash", "git status", Ruleset{denyAll, allowStatus}, px), allowStatus, "rule")
	})
	tw(t, f, "wildcard surface in rule matches any surface value", func(t *testing.T) {
		u := Ruleset{rule("*", "*", "allow", "", "global")}
		eq(t, ev("bash", "anything", u, px).Action, "allow", "bash")
		eq(t, ev("mcp", "something", u, px).Action, "allow", "mcp")
		eq(t, ev("skill", "librarian", u, px).Action, "allow", "skill")
	})
	tw(t, f, "specific surface rule does not match a different surface", func(t *testing.T) {
		eq(t, ev("mcp", "git status", Ruleset{allowBashGit}, px).Action, "ask", "default")
	})
	tw(t, f, "merged rulesets: rules from later scope take priority", func(t *testing.T) {
		g := Ruleset{rule("bash", "git *", "ask", "", "global")}
		a := Ruleset{rule("bash", "git *", "allow", "", "agent")}
		eq(t, ev("bash", "git status", append(append(Ruleset{}, g...), a...), px).Action, "allow", "agent wins")
	})
	tw(t, f, "merged rulesets: earlier scope used when later scope has no match", func(t *testing.T) {
		g := Ruleset{rule("bash", "git *", "allow", "", "global")}
		a := Ruleset{rule("bash", "npm *", "deny", "", "agent")}
		eq(t, ev("bash", "git status", append(append(Ruleset{}, g...), a...), px).Action, "allow", "global")
	})
	tw(t, f, "empty ruleset returns synthetic default", func(t *testing.T) {
		r := ev("bash", "git status", nil, px)
		eq(t, []string{r.Surface, r.Pattern, r.Action}, []string{"bash", "git status", "ask"}, "synthetic")
	})
	tw(t, f, "rule.layer is ignored by evaluate() — matching is identical with or without it", func(t *testing.T) {
		withLayer := rule("bash", "git *", "allow", "config", "global")
		withoutLayer := rule("bash", "git *", "allow", "", "global")
		withDefault := rule("bash", "*", "ask", "default", "builtin")
		eq(t, ev("bash", "git status", Ruleset{withLayer}, px).Action, "allow", "with layer")
		eq(t, ev("bash", "git status", Ruleset{withoutLayer}, px).Action, "allow", "without layer")
		eq(t, ev("bash", "git status", Ruleset{withDefault, withLayer}, px), withLayer, "default first")
		eq(t, ev("bash", "git status", Ruleset{withLayer, withDefault}, px), withDefault, "default last")
	})
	tw(t, f, "evaluate() preserves origin on a matched rule", func(t *testing.T) {
		eq(t, ev("bash", "git status", Ruleset{rule("bash", "git *", "allow", "config", "project")}, px).Origin, "project", "origin")
	})
	tw(t, f, "evaluate() synthetic fallback rule has origin 'builtin'", func(t *testing.T) {
		eq(t, ev("bash", "npm install", nil, px).Origin, "builtin", "origin")
	})
	tw(t, f, "evaluate() propagates reason from the matched deny rule", func(t *testing.T) {
		r := rule("bash", "npm *", "deny", "config", "global")
		r.Reason = sp("Use pnpm instead")
		got := ev("bash", "npm install", Ruleset{r}, px)
		eq(t, got.Action, "deny", "action")
		eq(t, got.Reason, sp("Use pnpm instead"), "reason")
	})
	tw(t, f, "evaluate() carries reason through last-match-wins when deny wins", func(t *testing.T) {
		allowAll := rule("bash", "*", "allow", "config", "global")
		denyNpm := rule("bash", "npm *", "deny", "config", "global")
		denyNpm.Reason = sp("Use pnpm")
		got := ev("bash", "npm install", Ruleset{allowAll, denyNpm}, px)
		eq(t, got.Action, "deny", "action")
		eq(t, got.Reason, sp("Use pnpm"), "reason")
	})
	tw(t, f, "evaluate() drops reason when a later allow overrides the deny", func(t *testing.T) {
		denyNpm := rule("bash", "npm *", "deny", "config", "global")
		denyNpm.Reason = sp("Use pnpm")
		allowInstall := rule("bash", "npm install", "allow", "config", "global")
		got := ev("bash", "npm install", Ruleset{denyNpm, allowInstall}, px)
		eq(t, got.Action, "allow", "action")
		eq(t, got.Reason == nil, true, "reason")
	})
	tw(t, f, "evaluate() synthetic fallback rule has no reason", func(t *testing.T) {
		eq(t, ev("bash", "npm install", nil, px).Reason == nil, true, "reason")
	})
	tw(t, f, "RuleOrigin covers all seven provenance values", func(t *testing.T) {
		for _, o := range []string{"global", "project", "agent", "project-agent", "builtin", "baseline", "session"} {
			eq(t, ev("read", "*", Ruleset{rule("read", "*", "allow", "config", o)}, px).Origin, o, o)
		}
	})

	denyExt := rule("external_directory", "*", "deny", "config", "global")
	allowExtPi := rule("external_directory", `C:\Users\Foo\pi\*`, "allow", "config", "global")
	lowered := `c:\users\foo\pi\docs\readme.md`
	tw(t, f, "win32: external_directory allow override matches a lowercased path over a preceding deny", func(t *testing.T) {
		eq(t, ev("external_directory", lowered, Ruleset{denyExt, allowExtPi}, Win32PathFlavor).Action, "allow", "action")
	})
	tw(t, f, "posix: the same mixed-case override stays case-sensitive (falls through to deny)", func(t *testing.T) {
		eq(t, ev("external_directory", lowered, Ruleset{denyExt, allowExtPi}, px).Action, "deny", "action")
	})
	tw(t, f, "win32: a forward-slash external_directory pattern matches a backslash value", func(t *testing.T) {
		fwd := rule("external_directory", "C:/Users/Foo/pi/*", "allow", "config", "global")
		eq(t, ev("external_directory", lowered, Ruleset{denyExt, fwd}, Win32PathFlavor).Action, "allow", "action")
	})
	tw(t, f, "win32: a forward-slash path pattern matches a forward-slash value (#653)", func(t *testing.T) {
		askAll := rule("path", "*", "ask", "config", "global")
		allowDev := rule("path", "/dev/null", "allow", "config", "global")
		eq(t, ev("path", "/dev/null", Ruleset{askAll, allowDev}, Win32PathFlavor).Action, "allow", "action")
	})
	tw(t, f, "win32: bash surface keeps its separators unfolded (not a path surface)", func(t *testing.T) {
		r := rule("bash", "cat /tmp/x", "allow", "", "global")
		eq(t, ev("bash", `cat \tmp\x`, Ruleset{r}, Win32PathFlavor).Action, "ask", "action")
	})
	tw(t, f, "win32: bash surface stays case-sensitive (not a path surface)", func(t *testing.T) {
		r := rule("bash", "git *", "allow", "", "global")
		eq(t, ev("bash", "GIT push", Ruleset{r}, Win32PathFlavor).Action, "ask", "action")
	})

	catchAllAllow := rule("path", "*", "allow", "config", "global")
	catchAllAsk := rule("path", "*", "ask", "config", "global")
	relativeDeny := rule("path", "src/*", "deny", "config", "global")
	absoluteAllow := rule("path", "/proj/*", "allow", "config", "global")
	tw(t, f, "a later relative rule wins over a catch-all matched by another alias", func(t *testing.T) {
		got := EvaluateAnyValue("path", []string{"/proj/src/foo.ts", "src/foo.ts"}, Ruleset{catchAllAllow, relativeDeny}, px)
		eq(t, got.Rule, relativeDeny, "rule")
		eq(t, got.Value, "src/foo.ts", "value")
	})
	tw(t, f, "uses an absolute alias when no later relative rule matches", func(t *testing.T) {
		got := EvaluateAnyValue("path", []string{"/proj/src/foo.ts", "src/foo.ts"}, Ruleset{catchAllAsk, absoluteAllow}, px)
		eq(t, got.Rule, absoluteAllow, "rule")
		eq(t, got.Value, "/proj/src/foo.ts", "value")
	})
	tw(t, f, "falls back to the first value when only the synthesized default matches", func(t *testing.T) {
		u := rule("*", "*", "ask", "default", "builtin")
		got := EvaluateAnyValue("path", []string{"a", "b", "c"}, Ruleset{u}, px)
		eq(t, got.Rule.Layer, "default", "layer")
		eq(t, got.Value, "a", "value")
	})
	tw(t, f, "falls back to the first value's default when no rule matches", func(t *testing.T) {
		got := EvaluateAnyValue("path", []string{"/proj/src/foo.ts", "src/foo.ts"}, nil, px)
		eq(t, got.Rule.Action, "ask", "action")
		eq(t, got.Value, "/proj/src/foo.ts", "value")
	})
	tw(t, f, "uses '*' as fallback value when values array is empty", func(t *testing.T) {
		eq(t, EvaluateAnyValue("path", nil, nil, px).Value, "*", "value")
	})

	denyEnv := rule("path", "*.env", "deny", "config", "global")
	askSsh := rule("path", "/home/user/.ssh/*", "ask", "config", "global")
	allowAll := rule("path", "*", "allow", "config", "global")
	tw(t, f, "deny short-circuits: returns immediately without evaluating remaining values", func(t *testing.T) {
		got := EvaluateMostRestrictive("path", []string{".env", "README.md"}, Ruleset{allowAll, denyEnv}, px)
		if got == nil {
			t.Fatal("nil")
		}
		eq(t, got.Rule.Action, "deny", "action")
		eq(t, got.Value, ".env", "value")
	})
	tw(t, f, "ask accumulates: returns first ask when no deny found", func(t *testing.T) {
		got := EvaluateMostRestrictive("path", []string{"/home/user/.ssh/id_rsa", "README.md"}, Ruleset{allowAll, askSsh}, px)
		if got == nil {
			t.Fatal("nil")
		}
		eq(t, got.Rule.Action, "ask", "action")
		eq(t, got.Value, "/home/user/.ssh/id_rsa", "value")
	})
	tw(t, f, "all allow: returns null", func(t *testing.T) {
		eq(t, EvaluateMostRestrictive("path", []string{"README.md", "src/index.ts"}, Ruleset{allowAll}, px) == nil, true, "nil")
	})
	tw(t, f, "empty values: returns null", func(t *testing.T) {
		eq(t, EvaluateMostRestrictive("path", nil, Ruleset{allowAll, denyEnv}, px) == nil, true, "nil")
	})
	tw(t, f, "deny wins over ask", func(t *testing.T) {
		got := EvaluateMostRestrictive("path", []string{"/home/user/.ssh/id_rsa", ".env"}, Ruleset{allowAll, askSsh, denyEnv}, px)
		if got == nil {
			t.Fatal("nil")
		}
		eq(t, got.Rule.Action, "deny", "action")
		eq(t, got.Value, ".env", "value")
	})

	overlayAskBash := rule("bash", "*", "ask", "config", "global")
	overlayDenyEnv := rule("path", ".env", "deny", "config", "project")
	overlayAllowRead := rule("read", "*", "allow", "config", "agent")
	overlayAskDefault := rule("*", "*", "ask", "default", "builtin")
	actions := func(r Ruleset) (a, o []string) {
		for _, x := range r {
			a, o = append(a, x.Action), append(o, x.Origin)
		}
		return
	}
	tw(t, f, "rewrites an ask rule to allow tagged origin 'yolo'", func(t *testing.T) {
		eq(t, RewriteAsksToYolo(Ruleset{overlayAskBash}), Ruleset{rule("bash", "*", "allow", "config", "yolo")}, "result")
	})
	tw(t, f, "preserves surface, pattern, and layer while flipping ask", func(t *testing.T) {
		r := RewriteAsksToYolo(Ruleset{overlayAskBash})[0]
		eq(t, []string{r.Surface, r.Pattern, r.Layer, r.Action, r.Origin}, []string{"bash", "*", "config", "allow", "yolo"}, "fields")
	})
	tw(t, f, "rewrites the synthesized universal default ask rule", func(t *testing.T) {
		r := RewriteAsksToYolo(Ruleset{overlayAskDefault})[0]
		eq(t, []string{r.Action, r.Origin, r.Layer}, []string{"allow", "yolo", "default"}, "fields")
	})
	tw(t, f, "passes deny rules through untouched (preserves hard denies)", func(t *testing.T) {
		eq(t, RewriteAsksToYolo(Ruleset{overlayDenyEnv}), Ruleset{overlayDenyEnv}, "deny")
	})
	tw(t, f, "passes allow rules through untouched", func(t *testing.T) {
		eq(t, RewriteAsksToYolo(Ruleset{overlayAllowRead}), Ruleset{overlayAllowRead}, "allow")
	})
	tw(t, f, "rewrites only ask rules in a mixed ruleset, preserving order", func(t *testing.T) {
		a, o := actions(RewriteAsksToYolo(Ruleset{overlayAskDefault, overlayAllowRead, overlayAskBash, overlayDenyEnv}))
		eq(t, a, []string{"allow", "allow", "allow", "deny"}, "actions")
		eq(t, o, []string{"yolo", "agent", "yolo", "project"}, "origins")
	})
	tw(t, f, "does not mutate the input ruleset", func(t *testing.T) {
		in := Ruleset{overlayAskBash}
		RewriteAsksToYolo(in)
		eq(t, []string{in[0].Action, in[0].Origin}, []string{"ask", "global"}, "input")
	})
	tw(t, f, "'yolo' is a valid RuleOrigin", func(t *testing.T) { eq(t, rule("a", "b", "ask", "", "yolo").Origin, "yolo", "origin") })

	tw(t, f, "floors an allow rule to ask tagged origin 'fail-closed'", func(t *testing.T) {
		eq(t, FloorAllowsToAsk(Ruleset{overlayAllowRead}), Ruleset{rule("read", "*", "ask", "config", "fail-closed")}, "result")
	})
	tw(t, f, "preserves surface, pattern, and layer while flooring allow", func(t *testing.T) {
		r := FloorAllowsToAsk(Ruleset{overlayAllowRead})[0]
		eq(t, []string{r.Surface, r.Pattern, r.Layer, r.Action, r.Origin}, []string{"read", "*", "config", "ask", "fail-closed"}, "fields")
	})
	tw(t, f, "passes deny rules through untouched (preserves hard denies)", func(t *testing.T) {
		eq(t, FloorAllowsToAsk(Ruleset{overlayDenyEnv}), Ruleset{overlayDenyEnv}, "deny")
	})
	tw(t, f, "passes ask rules through untouched", func(t *testing.T) {
		eq(t, FloorAllowsToAsk(Ruleset{overlayAskBash}), Ruleset{overlayAskBash}, "ask")
	})
	tw(t, f, "floors only allow rules in a mixed ruleset, preserving order", func(t *testing.T) {
		a, o := actions(FloorAllowsToAsk(Ruleset{overlayAskDefault, overlayAllowRead, overlayAskBash, overlayDenyEnv}))
		eq(t, a, []string{"ask", "ask", "ask", "deny"}, "actions")
		eq(t, o, []string{"builtin", "fail-closed", "global", "project"}, "origins")
	})
	tw(t, f, "does not mutate the input ruleset", func(t *testing.T) {
		in := Ruleset{overlayAllowRead}
		FloorAllowsToAsk(in)
		eq(t, []string{in[0].Action, in[0].Origin}, []string{"allow", "agent"}, "input")
	})
	tw(t, f, "'fail-closed' is a valid RuleOrigin", func(t *testing.T) { eq(t, rule("a", "b", "ask", "", "fail-closed").Origin, "fail-closed", "origin") })

	denyBashAll := rule("bash", "*", "deny", "", "global")
	askBashGit := rule("bash", "git *", "ask", "", "global")
	universalDeny := rule("*", "*", "deny", "default", "global")
	fd := func(surface string, r Ruleset, fl PathFlavor) bool { return IsSurfaceFullyDenied(surface, r, fl) }
	tw(t, f, "a bare deny catch-all denies the whole surface", func(t *testing.T) { eq(t, fd("bash", Ruleset{denyBashAll}, px), true, "denied") })
	tw(t, f, "an exception written before the deny catch-all is shadowed", func(t *testing.T) {
		eq(t, fd("bash", Ruleset{askBashGit, denyBashAll}, px), true, "denied")
	})
	tw(t, f, "a universal deny rule denies every surface", func(t *testing.T) {
		eq(t, fd("bash", Ruleset{universalDeny}, px), true, "bash")
		eq(t, fd("read", Ruleset{universalDeny}, px), true, "read")
	})
	tw(t, f, "a permissive rule on another surface does not reach this one", func(t *testing.T) {
		eq(t, fd("bash", Ruleset{denyBashAll, rule("read", "*.md", "allow", "", "global")}, px), true, "denied")
	})
	tw(t, f, "an exception written after the deny catch-all keeps the surface reachable", func(t *testing.T) {
		eq(t, fd("bash", Ruleset{denyBashAll, askBashGit}, px), false, "reachable")
	})
	tw(t, f, "a surface with no rules at all falls to the synthesized ask", func(t *testing.T) { eq(t, fd("bash", nil, px), false, "reachable") })
	tw(t, f, "a surface exception escapes a universal deny", func(t *testing.T) {
		eq(t, fd("bash", Ruleset{universalDeny, askBashGit}, px), false, "reachable")
	})
	tw(t, f, "a narrower deny below the exception does not re-deny the whole surface", func(t *testing.T) {
		eq(t, fd("bash", Ruleset{denyBashAll, askBashGit, rule("bash", "git push *", "deny", "", "global")}, px), false, "reachable")
	})
	tw(t, f, "a per-tool path map with a permissive pattern keeps the tool reachable", func(t *testing.T) {
		eq(t, fd("read", Ruleset{rule("read", "*", "deny", "", "global"), rule("read", "*.md", "allow", "", "global")}, px), false, "reachable")
	})
	tw(t, f, "a win32 path surface folds separators and case when probing", func(t *testing.T) {
		eq(t, fd("read", Ruleset{rule("read", "*", "deny", "", "global"), rule("read", "C:/Notes/*", "allow", "", "global")}, Win32PathFlavor), false, "reachable")
	})

	// The parametrized upstream cases (test.each) have no ledger title; they run as plain tests.
	t.Run("home-directory patterns reach through a deny catch-all", func(t *testing.T) {
		useFakeHome(t)
		for _, p := range []string{"~/notes/*", "$HOME/notes/*", "${HOME}/notes/*", "~", "$HOME"} {
			if fd("read", Ruleset{rule("read", "*", "deny", "", "global"), rule("read", p, "allow", "", "global")}, px) {
				t.Errorf("%s: denied", p)
			}
		}
	})
	t.Run("pattern shapes reach through a deny catch-all", func(t *testing.T) {
		for _, p := range []string{"*", "**", "git *", "git", "npm i*", "a?c", "*.env", "exa:*", "cargo build --release"} {
			if fd("bash", Ruleset{denyBashAll, rule("bash", p, "ask", "", "global")}, px) {
				t.Errorf("%s: denied", fmt.Sprint(p))
			}
		}
	})
}
