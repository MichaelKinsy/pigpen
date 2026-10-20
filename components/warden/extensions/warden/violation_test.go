package warden

import (
	"os"
	"reflect"
	"testing"
)

func violationOf(id string, severity Severity, family string, scope ViolationScope) Violation {
	return Violation{ID: id, Severity: severity, Source: SourcePattern, Description: id, PatternFamily: family, Scope: scope}
}

func firstPaths(vs []Violation) []string {
	out := []string{}
	for _, v := range vs {
		out = append(out, v.Scope.Paths[0])
	}
	return out
}

// twin: tests/violation.test.ts:22 "authorize: force-push requires explicit 'force push' or 'force-push' in the prompt"
func TestAuthorizeForcePushNeedsTheExplicitVerb(t *testing.T) {
	v := violationOf("git-force-push", SeverityDestructive, "git-force-push", ViolationScope{Command: "git push --force origin main", Tool: "bash"})
	if authorize("push my branch", v).Authorized || authorize("push my branch", v).ActionMatched {
		t.Fatal("push alone is not a force push")
	}
	if a := authorize("force push my branch", v); !a.Authorized || !a.ActionMatched {
		t.Fatalf("%+v", a)
	}
	if !authorize("force-push origin main", v).Authorized {
		t.Fatal("the hyphenated form")
	}
}

// twin: tests/violation.test.ts:38 "authorize: negation in the prompt prevents authorization"
func TestAuthorizeNegationPreventsAuthorization(t *testing.T) {
	v := violationOf("git-force-push", SeverityDestructive, "git-force-push", ViolationScope{Command: "git push --force origin main", Tool: "bash"})
	if authorize("don't force push", v).Authorized || !authorize("don't force push", v).Negated {
		t.Fatal("don't")
	}
	if !authorize("never force push", v).Negated || !authorize("do not force-push", v).Negated {
		t.Fatal("never / do not")
	}
	if authorize("don't push", v).Authorized || authorize("don't push", v).ActionMatched {
		t.Fatal("push alone does not match the force-push family")
	}
}

// twin: tests/violation.test.ts:54 "authorize: scope mismatch prevents authorization even when action matches"
func TestAuthorizeScopeMismatchPreventsAuthorization(t *testing.T) {
	v := violationOf("rm-rf", SeverityDestructive, "rm-rf", ViolationScope{Paths: []string{"eval/reports/"}, Tool: "bash"})
	if authorize("delete tmp.txt", v).Authorized || authorize("delete tmp.txt", v).ScopeMatched {
		t.Fatal("tmp.txt is not the scope")
	}
	if a := authorize("delete eval/reports", v); !a.Authorized || !a.ScopeMatched {
		t.Fatalf("%+v", a)
	}
}

// twin: tests/violation.test.ts:68 "authorize: hard-deny violations are not authorization-eligible"
func TestAuthorizeHardDenyIsNotEligible(t *testing.T) {
	v := Violation{ID: "never-talos-reset", Severity: SeverityDeny, Source: SourcePattern, Description: "blocked"}
	if a := authorize("reset talos", v); a.Authorized || a.ActionMatched {
		t.Fatalf("%+v", a)
	}
}

// twin: tests/violation.test.ts:76 "authorize: sensitive-path violations are not authorization-eligible"
func TestAuthorizeSensitivePathIsNotEligible(t *testing.T) {
	v := Violation{ID: "sensitive-path", Severity: SeveritySensitive, Source: SourcePattern, Description: "touches secrets"}
	if authorize("read the env", v).Authorized {
		t.Fatal("authorized")
	}
}

// twin: tests/violation.test.ts:86 "scopeMatches: exact path match"
func TestScopeMatchesExactPath(t *testing.T) {
	if !scopeMatches("delete eval/reports/", ViolationScope{Paths: []string{"eval/reports/"}}) || scopeMatches("delete something else", ViolationScope{Paths: []string{"eval/reports/"}}) {
		t.Fatal("exact path")
	}
}

// twin: tests/violation.test.ts:91 "scopeMatches: directory path with or without trailing slash"
func TestScopeMatchesDirectoryWithOrWithoutSlash(t *testing.T) {
	scope := ViolationScope{Paths: []string{"eval/reports/"}}
	if !scopeMatches("delete eval/reports", scope) || !scopeMatches("delete eval/reports/", scope) {
		t.Fatal("trailing slash")
	}
	if scopeMatches("delete reports", scope) || scopeMatches("clean up reports directory", scope) {
		t.Fatal("a generic word does not authorize a specific path")
	}
}

// twin: tests/violation.test.ts:98 "scopeMatches: no scope always matches"
func TestScopeMatchesNoScopeAlwaysMatches(t *testing.T) {
	if !scopeMatches("do anything", ViolationScope{}) || !scopeMatches("do anything", ViolationScope{Paths: nil}) {
		t.Fatal("no scope")
	}
}

// twin: tests/violation.test.ts:106 "isAuthEligible: risky and destructive are eligible; deny and sensitive are not"
func TestIsAuthEligible(t *testing.T) {
	if !isAuthEligible(SeverityRisky) || !isAuthEligible(SeverityDestructive) || isAuthEligible(SeverityDeny) || isAuthEligible(SeveritySensitive) {
		t.Fatal("eligibility")
	}
}

// twin: tests/violation.test.ts:116 "patternHitsToViolations: converts rm hits to per-target violations and skips non-rm bash"
func TestPatternHitsToViolationsConvertsRmHits(t *testing.T) {
	hits := []PatternHit{{ID: "rm-rf", Severity: SeverityRisky, Label: "rm -rf"}, {ID: "git-force-push", Severity: SeverityDestructive, Label: "git force push"}}
	vs := patternHitsToViolations(hits, "bash", map[string]any{"command": "rm -rf /tmp/x"})
	if len(vs) != 1 || vs[0].ID != "rm-rf" || len(vs[0].Scope.Paths) == 0 {
		t.Fatalf("%+v", vs)
	}
}

func rmViolations(command string) []Violation {
	return patternHitsToViolations([]PatternHit{{ID: "rm-rf", Severity: SeverityRisky, Label: "rm -rf"}}, "bash", map[string]any{"command": command})
}

// twin: tests/violation.test.ts:127 "patternHitsToViolations: rm targets stop at shell operators"
func TestRmTargetsStopAtShellOperators(t *testing.T) {
	vs := rmViolations("rm -rf /tmp/build && npm run build && npm test && echo done")
	if len(vs) != 1 || !reflect.DeepEqual(vs[0].Scope.Paths, []string{"/tmp/build"}) {
		t.Fatalf("%+v", vs)
	}
}

// twin: tests/violation.test.ts:136 "patternHitsToViolations: a multi-target rm keeps one violation per real target"
func TestMultiTargetRmKeepsOneViolationPerTarget(t *testing.T) {
	mustEqual(t, firstPaths(rmViolations("rm -rf a b c")), []string{"a", "b", "c"}, "targets")
}

// twin: tests/violation.test.ts:142 "patternHitsToViolations: every rm segment contributes its own targets"
func TestEveryRmSegmentContributesItsTargets(t *testing.T) {
	mustEqual(t, firstPaths(rmViolations("rm -rf a; rm -rf b c")), []string{"a", "b", "c"}, "targets")
}

// twin: tests/violation.test.ts:148 "evaluateAction: authorizing one rm target keeps an unrelated pattern hit"
func TestAuthorizingOneRmTargetKeepsAnUnrelatedPatternHit(t *testing.T) {
	// Hits are [rm-rf, rule-one, rule-two]; violations are one per target [a, b, c]. Authorizing c must not drop rule-two.
	config := cfg()
	config.CommandRules = []CommandRule{{ID: "rule-one", Pattern: `\brm\b`, Severity: "warn"}, {ID: "rule-two", Pattern: `\brm\b`, Severity: "confirm"}}
	v := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "rm -rf a b c"}, Cwd: os.TempDir(), Task: "delete c"}, EvaluateOptions{Config: config})
	if v.Level != LevelConfirm {
		t.Fatalf("rule-two still sets the level: %s", v.Level)
	}
	mustMatch(t, reasonsJoined(v), `rule-two`, "rule-two stays in the reasons")
	mustMatch(t, reasonsJoined(v), `rm -rf`, "rm-rf stays while a and b are not authorized")
}

// twin: tests/violation.test.ts:160 "patternHitsToViolations: an rm hit with no target is authorized only by its command segment"
func TestRmWithNoTargetIsAuthorizedOnlyByItsCommandSegment(t *testing.T) {
	vs := rmViolations("find . -name '*.log' | xargs rm -rf")
	if len(vs) != 1 || vs[0].ID != "rm-rf" || vs[0].Scope.Paths != nil {
		t.Fatalf("%+v", vs)
	}
	if authorize("clean up the log files", vs[0]).Authorized {
		t.Fatal("the verb alone does not authorize")
	}
	if !authorize("clean up the log files: find . -name '*.log'  |  xargs rm -rf", vs[0]).Authorized {
		t.Fatal("the quoted segment authorizes")
	}
}

// twin: tests/violation.test.ts:170 "evaluateAction: find -delete is not authorized by the verb alone and keeps its level"
func TestFindDeleteIsNotAuthorizedByTheVerbAlone(t *testing.T) {
	hits := []PatternHit{{ID: "find-delete", Severity: SeverityRisky, Label: "find -delete / -exec rm"}}
	vs := patternHitsToViolations(hits, "bash", map[string]any{"command": "find / -name x -delete"})
	if len(vs) != 1 || authorize("delete the old files", vs[0]).Authorized {
		t.Fatalf("%+v", vs)
	}
	v := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "find / -name x -delete"}, Cwd: os.TempDir(), Task: "delete the old files"}, EvaluateOptions{Config: cfg()})
	if v.Level == LevelAllow {
		t.Fatal("allowed")
	}
	mustMatch(t, reasonsJoined(v), `find -delete`, "find-delete stays in the level computation")
}

// twin: tests/violation.test.ts:181 "patternHitsToViolations: a quoted rm target with a space stays one target"
func TestAQuotedRmTargetWithASpaceStaysOneTarget(t *testing.T) {
	mustEqual(t, firstPaths(rmViolations("rm -rf 'a b' && npm test")), []string{"a b"}, "targets")
}

// twin: tests/violation.test.ts:187 "patternHitsToViolations: a redirection is not an rm target"
func TestARedirectionIsNotAnRmTarget(t *testing.T) {
	mustEqual(t, firstPaths(rmViolations("rm -rf build 2>/dev/null && npm test")), []string{"build"}, "targets")
}

// twin: tests/violation.test.ts:193 "patternHitsToViolations: rm targets stay with the segment that produced the hit"
func TestRmTargetsStayWithTheSegmentThatProducedTheHit(t *testing.T) {
	mustEqual(t, firstPaths(rmViolations("rm a; rm -rf b")), []string{"b"}, "targets")
}

var (
	unauthorized = Authorization{}
	authorizedA  = Authorization{Authorized: true, ActionMatched: true, ScopeMatched: true}
)

// twin: tests/violation.test.ts:202 "escalateBlastRadius: authorized violation keeps original severity"
func TestEscalateBlastRadiusAuthorizedKeepsSeverity(t *testing.T) {
	v := violationOf("rm-rf", SeverityRisky, "", ViolationScope{})
	if escalateBlastRadius(v, authorizedA, ViolationJudgmentAnswer{true, 0.95}, 0.85) != SeverityRisky {
		t.Fatal("escalated")
	}
}

// twin: tests/violation.test.ts:210 "escalateBlastRadius: unconfirmed violation keeps original severity"
func TestEscalateBlastRadiusUnconfirmedKeepsSeverity(t *testing.T) {
	v := violationOf("rm-rf", SeverityRisky, "", ViolationScope{})
	if escalateBlastRadius(v, unauthorized, ViolationJudgmentAnswer{false, 0.5}, 0.85) != SeverityRisky {
		t.Fatal("escalated")
	}
}

// twin: tests/violation.test.ts:218 "escalateBlastRadius: risky escalates to destructive when Jev confirms"
func TestEscalateBlastRadiusRiskyBecomesDestructive(t *testing.T) {
	v := violationOf("rm-rf", SeverityRisky, "", ViolationScope{})
	if escalateBlastRadius(v, unauthorized, ViolationJudgmentAnswer{true, 0.9}, 0.85) != SeverityDestructive {
		t.Fatal("not escalated")
	}
	// The threshold is exclusive: a confidence at the threshold does not escalate.
	if escalateBlastRadius(v, unauthorized, ViolationJudgmentAnswer{true, 0.85}, 0.85) != SeverityRisky {
		t.Fatal("escalated at the threshold")
	}
}

// twin: tests/violation.test.ts:226 "escalateBlastRadius: destructive escalates to deny when Jev confirms"
func TestEscalateBlastRadiusDestructiveBecomesDeny(t *testing.T) {
	v := violationOf("git-force-push", SeverityDestructive, "", ViolationScope{})
	if escalateBlastRadius(v, unauthorized, ViolationJudgmentAnswer{true, 0.95}, 0.85) != SeverityDeny {
		t.Fatal("not escalated")
	}
	// Sensitive and deny never move.
	for _, sev := range []Severity{SeveritySensitive, SeverityDeny} {
		if escalateBlastRadius(violationOf("x", sev, "", ViolationScope{}), unauthorized, ViolationJudgmentAnswer{true, 0.99}, 0.85) != sev {
			t.Fatalf("%s moved", sev)
		}
	}
}

// twin: tests/violation.test.ts:237 "escalateRulesViolation: Jev confirms against matched rule escalates to destructive"
func TestEscalateRulesViolationConfirmedBecomesDestructive(t *testing.T) {
	v := Violation{ID: "no-console", Severity: SeverityRisky, Source: SourceRulesGuard, Description: "no console", MatchedRule: "No console.log"}
	if escalateRulesViolation(v, ViolationJudgmentAnswer{true, 0.9}, 0.85) != SeverityDestructive {
		t.Fatal("not escalated")
	}
}

// twin: tests/violation.test.ts:245 "escalateRulesViolation: Jev does not confirm keeps original severity"
func TestEscalateRulesViolationUnconfirmedKeepsSeverity(t *testing.T) {
	v := Violation{ID: "no-console", Severity: SeverityRisky, Source: SourceRulesGuard, Description: "no console", MatchedRule: "No console.log"}
	if escalateRulesViolation(v, ViolationJudgmentAnswer{false, 0.3}, 0.85) != SeverityRisky {
		t.Fatal("escalated")
	}
}

// twin: tests/violation.test.ts:253 "escalateRulesViolation: no matchedRule keeps original severity"
func TestEscalateRulesViolationNeedsAMatchedRule(t *testing.T) {
	v := violationOf("rm-rf", SeverityRisky, "", ViolationScope{})
	if escalateRulesViolation(v, ViolationJudgmentAnswer{true, 0.95}, 0.85) != SeverityRisky {
		t.Fatal("escalated")
	}
}

func escalatedAt(id string, sev Severity) EscalatedViolation {
	return EscalatedViolation{Violation: Violation{ID: id, Severity: sev, Source: SourcePattern, Description: id}, EscalatedSeverity: sev}
}

// twin: tests/violation.test.ts:263 "aggregateLevel: no violations returns allow"
func TestAggregateLevelEmptyIsAllow(t *testing.T) {
	if aggregateLevel(nil) != LevelAllow {
		t.Fatal("not allow")
	}
}

// twin: tests/violation.test.ts:267 "aggregateLevel: highest severity wins"
func TestAggregateLevelHighestSeverityWins(t *testing.T) {
	risky, destructive, deny := escalatedAt("rm-rf", SeverityRisky), escalatedAt("git-force-push", SeverityDestructive), escalatedAt("never-rule", SeverityDeny)
	if aggregateLevel([]EscalatedViolation{risky}) != LevelWarn || aggregateLevel([]EscalatedViolation{risky, destructive}) != LevelConfirm || aggregateLevel([]EscalatedViolation{risky, destructive, deny}) != LevelDeny {
		t.Fatal("ladder")
	}
}

// twin: tests/violation.test.ts:284 "aggregateLevel: mix of authorized (removed) and remaining violations"
func TestAggregateLevelOfTheRemainingViolations(t *testing.T) {
	if aggregateLevel([]EscalatedViolation{escalatedAt("secret-literal", SeverityRisky)}) != LevelWarn {
		t.Fatal("not warn")
	}
}

// twin: tests/violation.test.ts:294 "removeAuthorized: removes only authorized violations"
func TestRemoveAuthorizedRemovesOnlyAuthorized(t *testing.T) {
	v1, v2 := violationOf("git-commit", SeverityRisky, "", ViolationScope{}), violationOf("secret-literal", SeverityRisky, "", ViolationScope{})
	remaining := removeAuthorized([]Violation{v1, v2}, []Authorization{authorizedA, unauthorized})
	if len(remaining) != 1 || remaining[0].ID != "secret-literal" {
		t.Fatalf("%+v", remaining)
	}
}

// twin: tests/violation.test.ts:304 "removeAuthorized: empty authorization array keeps all violations"
func TestRemoveAuthorizedWithNoAuthorizationsKeepsAll(t *testing.T) {
	if len(removeAuthorized([]Violation{violationOf("rm-rf", SeverityRisky, "", ViolationScope{})}, nil)) != 1 {
		t.Fatal("dropped")
	}
}

// twin: tests/violation.test.ts:312 "pipeline: authorized destructive action results in allow when no other violations"
func TestPipelineAuthorizedDestructiveIsAllowed(t *testing.T) {
	v := violationOf("rm-rf", SeverityDestructive, "rm-rf", ViolationScope{Paths: []string{"eval/reports/"}, Tool: "bash"})
	auth := authorize("delete eval/reports", v)
	if !auth.Authorized {
		t.Fatal("not authorized")
	}
	if len(removeAuthorized([]Violation{v}, []Authorization{auth})) != 0 || aggregateLevel(nil) != LevelAllow {
		t.Fatal("not removed")
	}
}

func escalateUnchanged(vs []Violation) []EscalatedViolation {
	var out []EscalatedViolation
	for _, v := range vs {
		out = append(out, EscalatedViolation{Violation: v, EscalatedSeverity: v.Severity})
	}
	return out
}

// twin: tests/violation.test.ts:326 "pipeline: one authorized + one unauthorized violation"
func TestPipelineOneAuthorizedOneNot(t *testing.T) {
	commit := violationOf("git-commit", SeverityRisky, "git-commit", ViolationScope{Command: "git commit", Tool: "bash"})
	secret := violationOf("secret-literal", SeverityRisky, "", ViolationScope{})
	authCommit, authSecret := authorize("commit the changes", commit), authorize("commit the changes", secret)
	if !authCommit.Authorized || authSecret.Authorized {
		t.Fatalf("%+v %+v", authCommit, authSecret)
	}
	remaining := removeAuthorized([]Violation{commit, secret}, []Authorization{authCommit, authSecret})
	if len(remaining) != 1 || remaining[0].ID != "secret-literal" || aggregateLevel(escalateUnchanged(remaining)) != LevelWarn {
		t.Fatalf("%+v", remaining)
	}
}

// twin: tests/violation.test.ts:346 "pipeline: hard deny is never removable by authorization"
func TestPipelineHardDenyIsNeverRemovable(t *testing.T) {
	v := Violation{ID: "never-deploy", Severity: SeverityDeny, Source: SourcePattern, Description: "blocked"}
	auth := authorize("deploy to production", v)
	remaining := removeAuthorized([]Violation{v}, []Authorization{auth})
	if auth.Authorized || len(remaining) != 1 || aggregateLevel(escalateUnchanged(remaining)) != LevelDeny {
		t.Fatalf("%+v", auth)
	}
}

// twin: tests/violation.test.ts:358 "pipeline: normal push does NOT authorize force-push (regression)"
func TestPipelineNormalPushDoesNotAuthorizeForcePush(t *testing.T) {
	v := violationOf("git-force-push", SeverityDestructive, "git-force-push", ViolationScope{Command: "git push --force origin main", Tool: "bash"})
	auth := authorize("push my branch", v)
	remaining := removeAuthorized([]Violation{v}, []Authorization{auth})
	if auth.Authorized || len(remaining) != 1 || aggregateLevel(escalateUnchanged(remaining)) != LevelConfirm {
		t.Fatalf("%+v", auth)
	}
}

// twin: tests/violation.test.ts:375 "parseViolationJudgments: parses noul probability answers (primary path)"
func TestParseViolationJudgmentsNoul(t *testing.T) {
	vs := []Violation{violationOf("rm-rf", SeverityRisky, "", ViolationScope{}), violationOf("git-push", SeverityDestructive, "", ViolationScope{})}
	got := parseViolationJudgments(vs, map[string]any{"violation_0": 0.92, "violation_1": 0.15})
	mustEqual(t, got, []ViolationJudgmentAnswer{{true, 0.92}, {false, 0.15}}, "answers")
}

// twin: tests/violation.test.ts:389 "parseViolationJudgments: parses legacy choice string answers"
func TestParseViolationJudgmentsLegacyChoice(t *testing.T) {
	vs := []Violation{violationOf("rm-rf", SeverityRisky, "", ViolationScope{}), violationOf("git-push", SeverityDestructive, "", ViolationScope{})}
	got := parseViolationJudgments(vs, map[string]any{"violation_0": "violation", "violation_1": "compliant"})
	mustEqual(t, got, []ViolationJudgmentAnswer{{true, 0.9}, {false, 0.1}}, "answers")
}

// twin: tests/violation.test.ts:403 "parseViolationJudgments: defaults to violated=true when extra is undefined"
func TestParseViolationJudgmentsDefaults(t *testing.T) {
	got := parseViolationJudgments([]Violation{violationOf("rm-rf", SeverityRisky, "", ViolationScope{})}, nil)
	mustEqual(t, got, []ViolationJudgmentAnswer{{true, 0.5}}, "default")
	// A malformed answer falls back the same way.
	got = parseViolationJudgments([]Violation{violationOf("rm-rf", SeverityRisky, "", ViolationScope{})}, map[string]any{"violation_0": "maybe"})
	mustEqual(t, got, []ViolationJudgmentAnswer{{true, 0.5}}, "malformed")
}

// twin: tests/violation.test.ts:413 "parseViolationJudgments: noul answer at threshold boundary"
func TestParseViolationJudgmentsBoundary(t *testing.T) {
	vs := []Violation{violationOf("rm-rf", SeverityRisky, "", ViolationScope{})}
	mustEqual(t, parseViolationJudgments(vs, map[string]any{"violation_0": 0.5}), []ViolationJudgmentAnswer{{true, 0.5}}, "0.5")
	mustEqual(t, parseViolationJudgments(vs, map[string]any{"violation_0": 0.49}), []ViolationJudgmentAnswer{{false, 0.49}}, "0.49")
}
