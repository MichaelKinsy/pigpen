package warden

import (
	"math/rand/v2"
	"reflect"
	"strconv"
	"testing"
)

// scoreRuleAnswers against the entry-building version it replaces, over generated rules (own cutoffs, severities,
// source references, repeated ids) and answers (missing, empty, unknown outcomes, probabilities of every shape).
func TestScoreRuleAnswersAgreesWithTheReference(t *testing.T) {
	r := rand.New(rand.NewPCG(41, 41))
	severities := []string{"", "high", "normal", "low"}
	choices := []string{"", "compliant", "violation", "not_applicable", "insufficient_context", "bogus", "VIOLATION"}
	findings, soft := 0, 0
	for i := range 40000 {
		n := r.IntN(45)
		rules := make([]Rule, n)
		answers := map[string]Answer{}
		for j := range rules {
			rule := Rule{ID: "r" + strconv.Itoa(r.IntN(n+2)), Name: "Name " + strconv.Itoa(j), Body: "body " + strconv.Itoa(j), Severity: severities[r.IntN(4)]}
			if r.IntN(3) == 0 {
				v := r.Float64()
				rule.Threshold = &v
			}
			if r.IntN(4) == 0 {
				rule.SourceRef = &SourceRef{File: "AGENTS.md", Line: j}
			}
			rules[j] = rule
			if r.IntN(6) == 0 {
				continue
			}
			answer := Answer{Type: "choice", Choice: choices[r.IntN(len(choices))]}
			switch r.IntN(3) {
			case 0:
				answer.Probabilities = map[string]float64{"violation": r.Float64(), "compliant": r.Float64()}
			case 1:
				answer.Probabilities = map[string]float64{"compliant": r.Float64()}
			}
			answers["rule_"+rule.ID] = answer
		}
		config := RulesConfig{Threshold: r.Float64(), SoftThreshold: float64(r.IntN(2)) * r.Float64()}
		var aggregate *string
		if r.IntN(10) == 0 {
			name := "the project's AGENTS.md"
			aggregate = &name
			answers[aggregateQuestion] = Answer{Type: "choice", Choice: choices[r.IntN(len(choices))], Probabilities: map[string]float64{"violation": r.Float64()}}
		}
		got, want := scoreRuleAnswers(rules, answers, config, aggregate), scoreRuleAnswersRef(rules, answers, config, aggregate)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("case %d:\n got  %+v\n want %+v", i, got, want)
		}
		findings += len(want.Findings)
		soft += len(want.SoftFindings)
	}
	if findings < 20000 || soft < 5000 {
		t.Fatalf("generator too narrow: %d findings, %d soft", findings, soft)
	}
}
