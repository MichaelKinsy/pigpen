package pi_subagents

import (
	"github.com/dlclark/regexp2/v2"
)

// isExplicitReadOnlyRecoveryReview is isExplicitReadOnlyRecoveryReview (scripted-workflow.ts:1322): after a child returned rejected
// acceptance metadata only a review child that is explicitly read-only may follow. The heuristic is the original's, pattern for
// pattern (workflow_recovery_patterns.go).

func recoveryReplaceAll(text string, patterns ...*regexp2.Regexp) string { panic("not implemented") }

func recoveryTest(re *regexp2.Regexp, text string) bool { panic("not implemented") }

// recoveryReReadOnlyAll is the read-only pattern with the global flag, which the original builds with new RegExp(source, "gi").
var recoveryReReadOnlyAll = regexp2.MustCompile(recoveryReReadOnlyPattern.String(), regexp2.ECMAScript|regexp2.IgnoreCase)

var recoveryAgentRe = regexp2.MustCompile(`\b(?:advisor|oracle|review|reviewer)\b`, regexp2.ECMAScript|regexp2.IgnoreCase)

func isExplicitReadOnlyRecoveryReview(params *jsObject) bool { panic("not implemented") }
