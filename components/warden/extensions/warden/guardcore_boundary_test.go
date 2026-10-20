package warden

import (
	"path/filepath"
	"testing"
)

// Boundaries and corners the mutation run (port/mutate.py) found unpinned.

func TestGuardCoreThresholdsAreInclusive(t *testing.T) {
	config := DefaultConfig()
	security := &recordingJudge{handler: func(r Request) (Evaluation, error) {
		e := actionAnswers(0.1, 0.1, "expected_step", nil)
		e.Answers["security_risk"] = noul(0.7)
		for _, s := range SlopSymptoms {
			if has(r.Questions, "slop_"+s) {
				e.Answers["slop_"+s] = noul(map[bool]float64{true: 0.7, false: 0.05}[s == "stub"])
			}
		}
		return e, nil
	}}
	v := eval(ActionInput{Tool: "write", Input: map[string]any{"path": filepath.Join(cwd, "a.ts"), "content": "x = 1"}, Task: "write it"}, EvaluateOptions{Config: config.Action, Judge: security, GuardExtras: GuardExtras{Slop: &config.Slop, Security: &config.Security}})
	if v.Level != LevelWarn {
		t.Fatalf("a security score at the threshold warns: %s %v", v.Level, v.Reasons)
	}
	if len(v.SlopSymptoms) != 1 || v.SlopSymptoms[0] != "stub" {
		t.Fatalf("a slop score at the threshold is flagged: %v", v.SlopSymptoms)
	}
	lo := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "npm test"}, Task: "run"}, EvaluateOptions{Config: config.Action, Judge: largeOutputJudge(0.85), GuardExtras: GuardExtras{LargeOutput: largeOutputOn}})
	if lo.LargeOutputFamily != "npm test" {
		t.Fatalf("a large-output score at the threshold names the family: %q", lo.LargeOutputFamily)
	}
}

func TestSQLTargetCorners(t *testing.T) {
	if sqlTarget("PGSERVICE=localhost psql -h localhost -c 'SELECT 1'") != sqlUnknown {
		t.Fatal("a service name cannot be read")
	}
	if got := sqlHits("psql -h 127.0.0.1 <<EOF\nDELETE FROM t WHERE id = $ID;\nEOF"); len(got) != 1 || got[0][0] != "sql-delete" {
		t.Fatalf("a heredoc the shell expands is not read: %v", got)
	}
}

func TestArmingPathsDedupAndRedirectArms(t *testing.T) {
	dir := t.TempDir()
	rule := ArmingRule{ID: "r", When: ArmingWhen{Edited: []string{"**/*.yaml"}}, Arms: ArmingArms{Command: `\bapply\b`, For: "10m"}, Action: "confirm"}
	tr := armedTester(rule)
	p := filepath.Join(dir, "a.yaml")
	tr.Arm("write", p, dir, nil)
	tr.Arm("write", p, dir, nil)
	if hits := tr.CheckArmed("kubectl apply"); len(hits) != 1 || len(hits[0].ArmedByPaths) != 1 {
		t.Fatalf("the same path twice is one path: %+v", hits)
	}
	h := startHarness(t, harnessOpts{cfg: armingCfg(rule)})
	h.start()
	target := filepath.Join(h.host.opts.Cwd, "b.yaml")
	h.toolCall("bash", "b1", map[string]any{"command": "echo x > " + target})
	h.confirm = false
	if _, held := blocked(h.toolCall("bash", "b2", map[string]any{"command": "kubectl apply -f b.yaml"})); !held {
		t.Fatal("a bash redirect to a matching file arms the rule")
	}
}

func TestAggregateNeverLowersAUserRuleLevel(t *testing.T) {
	config := levelCfg()
	config.CommandRules = []CommandRule{{ID: "no-rm", Pattern: `\brm\b`, Severity: "confirm"}}
	v := eval(ActionInput{Tool: "bash", Input: map[string]any{"command": "rm -rf build"}, Task: "clean"}, EvaluateOptions{Config: config, Judge: scoreJudge(map[string]float64{"violation_0": 0.01})})
	if v.Level != LevelConfirm {
		t.Fatalf("the user rule's hold stays: %s", v.Level)
	}
}
