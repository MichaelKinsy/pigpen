package warden

import (
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

var (
	armClock    = time.Unix(1, 0)
	armNow      = func() time.Time { return armClock }
	atMs        = func(ms int64) { armClock = time.UnixMilli(ms) }
	fluxRule    = ArmingRule{ID: "gitops-edit-arms-reconcile", When: ArmingWhen{Edited: []string{"**/kustomization.yaml", "**/helmrelease.yaml"}, Tools: []string{"write", "edit"}}, Arms: ArmingArms{Command: `\bflux\b|\bkubectl\s+(apply|delete|prune)\b`, For: "10m"}, Action: "confirm", Message: "Cluster state definitions were edited this session; this reconciliation applies them."}
	blockRule   = ArmingRule{ID: "never-reset", When: ArmingWhen{Edited: []string{"**/dangerous.conf"}}, Arms: ArmingArms{Command: `\btalosctl\s+reset\b`, For: "5m"}, Action: "block"}
	holdRule    = ArmingRule{ID: "edit-arms-hold", When: ArmingWhen{Edited: []string{"**/config.yml"}}, Arms: ArmingArms{Command: `\bdeploy\b`, For: "1s"}, Action: "hold"}
	kustomize   = "flux-cluster/apps/kustomization.yaml"
	armedTester = func(rules ...ArmingRule) *ArmingTracker { atMs(1000); return NewArmingTracker(rules, armNow) }
)

// twin: tests/arming.test.ts:43 "arm → checkArmed fires; expire removes the arm"
func TestArmThenCheckFiresAndExpires(t *testing.T) {
	dir := t.TempDir()
	tr := armedTester(fluxRule)
	armed := tr.Arm("write", filepath.Join(dir, kustomize), dir, nil)
	if len(armed) != 1 || armed[0] != "gitops-edit-arms-reconcile" {
		t.Fatalf("%v", armed)
	}
	hits := tr.CheckArmed("flux reconcile --path ./clusters/prod")
	if len(hits) != 1 || hits[0].ID != "gitops-edit-arms-reconcile" || hits[0].Action != "confirm" {
		t.Fatalf("%+v", hits)
	}
	atMs(1000 + 11*60*1000)
	if len(tr.CheckArmed("flux reconcile --path ./clusters/prod")) != 0 {
		t.Fatal("the arm did not expire")
	}
}

// twin: tests/arming.test.ts:61 "re-arm refreshes the expiry"
func TestReArmRefreshesTheExpiry(t *testing.T) {
	dir := t.TempDir()
	tr := armedTester(fluxRule)
	tr.Arm("edit", filepath.Join(dir, kustomize), dir, nil)
	atMs(1000 + 9*60*1000)
	tr.Arm("edit", filepath.Join(dir, kustomize), dir, nil)
	atMs(1000 + 9*60*1000 + 6*60*1000)
	if len(tr.CheckArmed("flux reconcile")) != 1 {
		t.Fatal("not refreshed")
	}
}

// twin: tests/arming.test.ts:72 "when.tools filters: read tool does not arm"
func TestWhenToolsFiltersTheReadTool(t *testing.T) {
	dir := t.TempDir()
	tr := armedTester(fluxRule)
	if len(tr.Arm("read", filepath.Join(dir, kustomize), dir, nil)) != 0 || len(tr.CheckArmed("flux reconcile")) != 0 {
		t.Fatal("armed by a read")
	}
}

// twin: tests/arming.test.ts:82 "non-matching path does not arm"
func TestNonMatchingPathDoesNotArm(t *testing.T) {
	dir := t.TempDir()
	tr := armedTester(fluxRule)
	if len(tr.Arm("write", filepath.Join(dir, "README.md"), dir, nil)) != 0 || len(tr.CheckArmed("flux reconcile")) != 0 {
		t.Fatal("armed")
	}
}

// twin: tests/arming.test.ts:91 "non-matching command does not fire; armed state stays intact"
func TestNonMatchingCommandKeepsTheArm(t *testing.T) {
	dir := t.TempDir()
	tr := armedTester(fluxRule)
	tr.Arm("write", filepath.Join(dir, kustomize), dir, nil)
	if len(tr.CheckArmed("kubectl get pods")) != 0 {
		t.Fatal("fired")
	}
	if len(tr.CheckArmed("flux reconcile")) != 1 {
		t.Fatal("arm lost")
	}
}

// twin: tests/arming.test.ts:102 "block action fires on armed match"
func TestBlockActionFiresOnArmedMatch(t *testing.T) {
	dir := t.TempDir()
	tr := armedTester(blockRule)
	tr.Arm("write", filepath.Join(dir, "dangerous.conf"), dir, nil)
	if hits := tr.CheckArmed("talosctl reset --graceful=false"); len(hits) != 1 || hits[0].Action != "block" {
		t.Fatalf("%+v", hits)
	}
}

// twin: tests/arming.test.ts:111 "hold action fires on armed match"
func TestHoldActionFiresOnArmedMatch(t *testing.T) {
	dir := t.TempDir()
	tr := armedTester(holdRule)
	tr.Arm("write", filepath.Join(dir, "config.yml"), dir, nil)
	if hits := tr.CheckArmed("deploy --prod"); len(hits) != 1 || hits[0].Action != "hold" {
		t.Fatalf("%+v", hits)
	}
}

// twin: tests/arming.test.ts:120 "multiple rules arm from one edit; one rule armed by multiple edits (dedup paths)"
func TestMultipleRulesArmFromOneEditAndPathsDedup(t *testing.T) {
	dir := t.TempDir()
	a := ArmingRule{ID: "rule-a", When: ArmingWhen{Edited: []string{"**/*.yaml"}}, Arms: ArmingArms{Command: `\bapply\b`, For: "10m"}, Action: "confirm"}
	b := ArmingRule{ID: "rule-b", When: ArmingWhen{Edited: []string{"**/*.yaml"}}, Arms: ArmingArms{Command: `\bdelete\b`, For: "10m"}, Action: "confirm"}
	tr := armedTester(a, b)
	if armed := tr.Arm("write", filepath.Join(dir, kustomize), dir, nil); len(armed) != 2 {
		t.Fatalf("%v", armed)
	}
	if len(tr.CheckArmed("kubectl apply")) != 1 || len(tr.CheckArmed("kubectl delete")) != 1 {
		t.Fatal("respective commands")
	}
	tr.Arm("write", filepath.Join(dir, "flux-cluster/apps/another.yaml"), dir, nil)
	hits := tr.CheckArmed("kubectl apply")
	if len(hits) != 1 || len(hits[0].ArmedByPaths) != 2 {
		t.Fatalf("%+v", hits)
	}
}

// twin: tests/arming.test.ts:148 "bash redirect target matching a when.edited glob arms"
func TestBashRedirectTargetArms(t *testing.T) {
	dir := t.TempDir()
	tr := armedTester(fluxRule)
	if armed := tr.Arm("bash", "", dir, []string{filepath.Join(dir, kustomize)}); len(armed) != 1 {
		t.Fatalf("%v", armed)
	}
	if len(tr.CheckArmed("flux reconcile")) != 1 {
		t.Fatal("not armed")
	}
}

// twin: tests/arming.test.ts:158 "status line renders armed state"
func TestStatusLineRendersArmedState(t *testing.T) {
	dir := t.TempDir()
	tr := armedTester(fluxRule)
	if tr.StatusLine() != "" {
		t.Fatal("not empty")
	}
	tr.Arm("write", filepath.Join(dir, kustomize), dir, nil)
	if line := tr.StatusLine(); !regexp.MustCompile(`1 armed: gitops-edit-arms-reconcile \(\d+m left\)`).MatchString(line) {
		t.Fatal(line)
	}
}

// twin: tests/arming.test.ts:167 "reset clears all armed state"
func TestResetClearsAllArmedState(t *testing.T) {
	dir := t.TempDir()
	tr := armedTester(fluxRule)
	tr.Arm("write", filepath.Join(dir, kustomize), dir, nil)
	if tr.StatusLine() == "" {
		t.Fatal("not armed")
	}
	tr.Reset()
	if tr.StatusLine() != "" || len(tr.CheckArmed("flux reconcile")) != 0 {
		t.Fatal("not cleared")
	}
}

// twin: tests/arming.test.ts:177 "armedRuleIds lists currently armed rules"
func TestArmedRuleIDsListsTheArmedRules(t *testing.T) {
	dir := t.TempDir()
	tr := armedTester(fluxRule, blockRule)
	tr.Arm("write", filepath.Join(dir, kustomize), dir, nil)
	tr.Arm("write", filepath.Join(dir, "dangerous.conf"), dir, nil)
	ids := tr.ArmedRuleIDs()
	mustEqual(t, ids, []string{"gitops-edit-arms-reconcile", "never-reset"}, "ids")
}

// twin: tests/arming.test.ts:188 "caseSensitive flag on arms.command is honored"
func TestCaseSensitiveArmsCommandIsHonored(t *testing.T) {
	dir := t.TempDir()
	rule := ArmingRule{ID: "case-sensitive-rule", When: ArmingWhen{Edited: []string{"**/config.yml"}}, Arms: ArmingArms{Command: `\bFLUX\b`, For: "10m", CaseSensitive: true}, Action: "confirm"}
	tr := armedTester(rule)
	tr.Arm("write", filepath.Join(dir, "config.yml"), dir, nil)
	if len(tr.CheckArmed("flux reconcile")) != 0 || len(tr.CheckArmed("FLUX reconcile")) != 1 {
		t.Fatal("case")
	}
}

// twin: tests/arming.test.ts:204 "regex when.edited matches by regex, not glob"
func TestRegexWhenEditedMatchesByRegex(t *testing.T) {
	dir := t.TempDir()
	rule := ArmingRule{ID: "regex-when", When: ArmingWhen{Edited: []string{`.*\.ya?ml$`}, Regex: true}, Arms: ArmingArms{Command: `\bapply\b`, For: "10m"}, Action: "confirm"}
	tr := armedTester(rule)
	tr.Arm("write", filepath.Join(dir, kustomize), dir, nil)
	if len(tr.CheckArmed("kubectl apply")) != 1 {
		t.Fatal("not armed")
	}
}

// twin: tests/arming.test.ts:217 "malformed command regex does not crash; rule is inert"
func TestMalformedCommandRegexIsInert(t *testing.T) {
	dir := t.TempDir()
	rule := ArmingRule{ID: "bad-regex", When: ArmingWhen{Edited: []string{"**/*.yml"}}, Arms: ArmingArms{Command: "[invalid", For: "10m"}, Action: "confirm"}
	tr := armedTester(rule)
	tr.Arm("write", filepath.Join(dir, "config.yml"), dir, nil)
	if len(tr.CheckArmed("apply")) != 0 {
		t.Fatal("fired")
	}
}

// twin: tests/arming.test.ts:231 "hold action: armed hold rule fires block in the tracker (not warn)"
func TestArmedHoldRuleFiresAsAHold(t *testing.T) {
	dir := t.TempDir()
	tr := armedTester(holdRule)
	tr.Arm("write", filepath.Join(dir, "config.yml"), dir, nil)
	if hits := tr.CheckArmed("deploy"); len(hits) != 1 || hits[0].Action != "hold" {
		t.Fatalf("%+v", hits)
	}
}

// twin: tests/arming.test.ts:241 "session-scope: arming survives when only updateRules is called (no reset between runs)"
func TestArmingSurvivesUpdateRules(t *testing.T) {
	dir := t.TempDir()
	tr := armedTester(fluxRule)
	tr.Arm("write", filepath.Join(dir, kustomize), dir, nil)
	tr.UpdateRules([]ArmingRule{fluxRule})
	atMs(2000)
	if len(tr.CheckArmed("flux reconcile")) != 1 {
		t.Fatal("armed state lost")
	}
}

// twin: tests/arming.test.ts:252 "session-scope: reset clears armed state (session_start)"
func TestSessionStartResetClearsArming(t *testing.T) {
	dir := t.TempDir()
	tr := armedTester(fluxRule)
	tr.Arm("write", filepath.Join(dir, kustomize), dir, nil)
	tr.Reset()
	if len(tr.CheckArmed("flux reconcile")) != 0 {
		t.Fatal("not cleared")
	}
}

// twin: tests/arming.test.ts:261 "checkArmed: no stripDataText cost when nothing is armed"
func TestCheckArmedWithNothingArmedReturnsNothing(t *testing.T) {
	tr := armedTester(fluxRule)
	if len(tr.CheckArmed("flux reconcile")) != 0 {
		t.Fatal("fired")
	}
}

// twin: tests/arming.test.ts:269 "updateRules: recompiles only when the reference changes"
func TestUpdateRulesRecompilesWhenTheSliceChanges(t *testing.T) {
	dir := t.TempDir()
	rules := []ArmingRule{fluxRule}
	tr := NewArmingTracker(rules, armNow)
	tr.UpdateRules(rules)
	first := tr.compiled
	tr.UpdateRules(rules)
	if len(tr.compiled) != len(first) || (len(first) > 0 && tr.compiled[0] != first[0]) {
		t.Fatal("recompiled for the same slice")
	}
	tr.UpdateRules([]ArmingRule{fluxRule})
	if len(tr.compiled) > 0 && tr.compiled[0] == first[0] {
		t.Fatal("a different slice must recompile")
	}
	atMs(1000)
	tr.Arm("write", filepath.Join(dir, kustomize), dir, nil)
	if len(tr.CheckArmed("flux reconcile")) != 1 {
		t.Fatal("tracker broken")
	}
}

// twin: tests/arming.test.ts:284 "arms.for '0m' falls back to default duration, not 0ms"
func TestZeroMinutesFallsBackToTheDefaultDuration(t *testing.T) {
	dir := t.TempDir()
	rule := ArmingRule{ID: "zero-duration", When: ArmingWhen{Edited: []string{"**/*.yml"}}, Arms: ArmingArms{Command: "kubectl", For: "0m"}, Action: "confirm"}
	tr := armedTester(rule)
	tr.Arm("write", filepath.Join(dir, "config.yml"), dir, nil)
	atMs(1001)
	if len(tr.CheckArmed("kubectl")) != 1 {
		t.Fatal("0m is not the default")
	}
}

// twin: tests/arming.test.ts:299 "arms.for 0 (number) falls back to default duration"
func TestZeroNumberFallsBackToTheDefaultDuration(t *testing.T) {
	dir := t.TempDir()
	rule := ArmingRule{ID: "zero-num", When: ArmingWhen{Edited: []string{"**/*.yml"}}, Arms: ArmingArms{Command: "kubectl", For: float64(0)}, Action: "confirm"}
	tr := armedTester(rule)
	tr.Arm("write", filepath.Join(dir, "config.yml"), dir, nil)
	atMs(1001)
	if len(tr.CheckArmed("kubectl")) != 1 {
		t.Fatal("0 is not the default")
	}
}

// twin: tests/arming.test.ts:314 "unparseableArmingRules: surfaces rules with invalid regex"
func TestUnparseableArmingRulesSurfacesInvalidRegexes(t *testing.T) {
	rules := []ArmingRule{
		{ID: "good", When: ArmingWhen{Edited: []string{"**/*.yml"}}, Arms: ArmingArms{Command: "kubectl"}, Action: "confirm"},
		{ID: "bad", When: ArmingWhen{Edited: []string{"**/*.yml"}}, Arms: ArmingArms{Command: "[invalid"}, Action: "confirm"},
	}
	mustEqual(t, unparseableArmingRules(rules), []string{"bad"}, "ids")
}

// twin: tests/arming.test.ts:326 "arm/check parity: data heredoc redirect does not produce a write-sink target"
func TestDataHeredocRedirectIsNotAWriteSink(t *testing.T) {
	dataCmd := "cat <<'EOF'\nremember to redirect logs > ~/flux-cluster/kustomization.yaml someday\nEOF\necho done"
	stripped := StripDataText(dataCmd).Text
	if len(writeSinkTargets(dataCmd)) == 0 {
		t.Fatal("the raw command has a phantom redirect in the data body")
	}
	if len(writeSinkTargets(stripped)) != 0 {
		t.Fatal("the stripped command has no redirect: the arm side must use stripped text")
	}
}

// twin: tests/arming.test.ts:338 "arm/check parity: a real redirect in a shell-sink heredoc does produce a write-sink target"
func TestARealRedirectInAShellSinkHeredocIsAWriteSink(t *testing.T) {
	execCmd := "bash <<'EOF'\necho x > /tmp/kustomization.yaml\nEOF"
	if len(writeSinkTargets(StripDataText(execCmd).Text)) == 0 {
		t.Fatal("a shell-sink heredoc redirect survives StripDataText")
	}
}
