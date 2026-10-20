package warden

import (
	"context"
	"testing"
)

// The hot path of every guarded call: the offline pattern pass and the evaluation around it, with and without the
// guard core's rules (path rules and arming rules in the config, SQL targets in the command).

func benchAction(command string) ActionInput {
	return ActionInput{Tool: "bash", Input: map[string]any{"command": command}, Cwd: "/work/project", Task: "run the checks"}
}

func BenchmarkEvaluateActionOrdinaryCommand(b *testing.B) {
	config := DefaultConfig().Action
	action := benchAction("go test ./... && git status")
	b.ReportAllocs()
	for b.Loop() {
		EvaluateAction(context.Background(), action, EvaluateOptions{Config: config})
	}
}

func BenchmarkEvaluateActionWithPathRules(b *testing.B) {
	config := DefaultConfig().Action
	config.PathRules = []PathRule{
		{ID: "env", Paths: []string{"**/.env"}, Access: "none", Tools: []string{"*"}, Action: "warn"},
		{ID: "keys", Paths: []string{"~/.ssh/id_*"}, Access: "read", Tools: []string{"*"}, Action: "block"},
		{ID: "audit", Paths: []string{"/var/audit/*.log"}, Access: "write", Tools: []string{"*"}, Action: "confirm"},
	}
	action := benchAction("go test ./... && cat notes.txt | tee out.log")
	b.ReportAllocs()
	for b.Loop() {
		EvaluateAction(context.Background(), action, EvaluateOptions{Config: config})
	}
}

func BenchmarkEvaluateActionSQLCommand(b *testing.B) {
	config := DefaultConfig().Action
	action := benchAction(`psql -h 127.0.0.1 -U postgres -d app -c "DELETE FROM users WHERE email LIKE 'crew-%';"`)
	b.ReportAllocs()
	for b.Loop() {
		EvaluateAction(context.Background(), action, EvaluateOptions{Config: config})
	}
}

func BenchmarkArmingCheckWhenArmed(b *testing.B) {
	rule := ArmingRule{ID: "r", When: ArmingWhen{Edited: []string{"**/*.yaml"}}, Arms: ArmingArms{Command: `\bapply\b`, For: "10m"}, Action: "confirm"}
	tr := NewArmingTracker([]ArmingRule{rule}, nil)
	tr.Arm("write", "/p/a.yaml", "/p", nil)
	b.ReportAllocs()
	for b.Loop() {
		tr.CheckArmed("kubectl apply -f a.yaml")
	}
}
