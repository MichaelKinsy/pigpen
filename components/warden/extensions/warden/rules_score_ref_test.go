package warden

// scoreAnswers and scoreRuleAnswers as they were before scoring stopped building a key and an entry per rule
// (rules_score_fast_test.go holds the new ones to these).

func scoreAnswersRef(answers map[string]Answer, entries []RuleScoreEntry, config RulesConfig) RuleScoring {
	out := RuleScoring{Scores: make([]RuleScore, 0, len(entries))}
	for _, entry := range entries {
		answer, ok := answers[entry.Key]
		if !ok || answer.Choice == "" {
			continue
		}
		violation := 0.0
		if p, has := answer.Probabilities[outcomeViolation]; has {
			violation = p
		} else if answer.Choice == outcomeViolation {
			violation = 1
		}
		outcome := answer.Choice
		if _, known := rulesQuestions().outcomes[outcome]; !known {
			outcome = outcomeInsufficent
		}
		score := RuleScore{ID: entry.ID, Name: entry.Name, Outcome: outcome, Violation: violation}
		cutoff := config.Threshold
		if entry.Rule != nil {
			score.Threshold, score.Severity, score.SourceRef = entry.Rule.Threshold, entry.Rule.Severity, entry.Rule.SourceRef
			if entry.Rule.Threshold != nil {
				cutoff = *entry.Rule.Threshold
			}
		}
		out.Scores = append(out.Scores, score)
		if violation >= cutoff {
			out.Findings = append(out.Findings, RuleFinding{RuleScore: score, Body: entry.Body})
		} else if config.SoftThreshold > 0 && violation >= config.SoftThreshold {
			out.SoftFindings = append(out.SoftFindings, RuleFinding{RuleScore: score, Body: entry.Body})
		}
	}
	bySeverityThenScore(out.Findings)
	bySeverityThenScore(out.SoftFindings)
	return out
}

// scoreRuleAnswers scores the judge's answers rule by rule: one score each, a finding at the rule's own cutoff (or
// rules.threshold when the rule sets none), and a soft finding between rules.softThreshold and that cutoff. An
// aggregate document is one rule under its source's name. Shared by the live guard and history replays.
func scoreRuleAnswersRef(applicable []Rule, answers map[string]Answer, config RulesConfig, aggregateName *string) RuleScoring {
	var entries []RuleScoreEntry
	if aggregateName != nil {
		entries = []RuleScoreEntry{{Key: aggregateQuestion, ID: aggregateQuestion, Name: *aggregateName}}
	} else {
		entries = make([]RuleScoreEntry, 0, len(applicable))
		for i := range applicable {
			rule := &applicable[i]
			entries = append(entries, RuleScoreEntry{Key: "rule_" + rule.ID, ID: rule.ID, Name: rule.Name, Body: rule.Body, Rule: rule})
		}
	}
	return scoreAnswersRef(answers, entries, config)
}
