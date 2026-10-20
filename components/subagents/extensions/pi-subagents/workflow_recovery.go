package pi_subagents

import (
	"sync"

	"github.com/dlclark/regexp2/v2"
)

// isExplicitReadOnlyRecoveryReview is isExplicitReadOnlyRecoveryReview (scripted-workflow.ts:1322): after a child returned rejected
// acceptance metadata only a review child that is explicitly read-only may follow. The heuristic is the original's, pattern for
// pattern (workflow_recovery_patterns.go).

// lazyRe is a regular expression compiled on first use: compiling the 38 patterns of this heuristic with regexp2's ECMAScript and
// IgnoreCase options costs about 370 ms and 9 MB at package initialization, paid by every process that loads the extension, while
// the heuristic runs only after a child returned rejected acceptance metadata.
type lazyRe struct {
	once sync.Once
	src  string
	opts regexp2.RegexOptions
	re   *regexp2.Regexp
}

func lazyRegexp(src string, opts regexp2.RegexOptions) *lazyRe { return &lazyRe{src: src, opts: opts} }

func (l *lazyRe) get() *regexp2.Regexp {
	l.once.Do(func() { l.re = regexp2.MustCompile(l.src, l.opts) })
	return l.re
}

func recoveryReplaceAll(text string, patterns ...*lazyRe) string {
	for _, re := range patterns {
		if out, err := re.get().Replace(text, " ", -1, -1); err == nil {
			text = out
		}
	}
	return text
}

func recoveryTest(re *lazyRe, text string) bool {
	ok, err := re.get().MatchString(text)
	return err == nil && ok
}

// recoveryReReadOnlyAll is the read-only pattern with the global flag, which the original builds with new RegExp(source, "gi").
var recoveryReReadOnlyAll = lazyRegexp(recoveryReReadOnlyPattern.src, regexp2.ECMAScript|regexp2.IgnoreCase)

var recoveryAgentRe = lazyRegexp(`\b(?:advisor|oracle|review|reviewer)\b`, regexp2.ECMAScript|regexp2.IgnoreCase)

func isExplicitReadOnlyRecoveryReview(params *jsObject) bool {
	agent := jsTrim(objStr(params, "agent"))
	task := jsTrim(objStr(params, "task"))
	taskDestructiveCommandText := recoveryReplaceAll(task,
		recoveryReBlockedContextFragmentPattern,
		recoveryReRegressionEvidencePattern,
		recoveryReBlockedQuotedExamplePattern,
		recoveryReContextQuotedExamplePattern,
		recoveryReListedBlockedExamplePattern,
		recoveryReBlocksQuotedExamplePattern,
		recoveryReExamplesLikeBlockedPattern,
		recoveryReQuotedVisibleBlockedPattern,
		recoveryRePromptsLikeBlockedPattern,
		recoveryReGitMutationsBroaderContextPattern,
		recoveryReGitCoverageContextPattern,
		recoveryReRegressionFollowedByNamedRmPattern,
	)
	taskDashLiveActionText := recoveryReplaceAll(task,
		recoveryReDeliverablePattern,
		recoveryReContextObjectPattern,
		recoveryReMutationNounContextPattern,
		recoveryRePriorFixContextPattern,
		recoveryReDetectionContextPattern,
		recoveryRePatternChangeContextPattern,
		recoveryReGitPatternChangeContextPattern,
		recoveryReValidationEvidencePattern,
		recoveryReContractProhibitionPattern,
		recoveryReOnlyReviewContractPattern,
		recoveryReBlockedQuotedExamplePattern,
		recoveryReContextQuotedExamplePattern,
		recoveryReListedBlockedExamplePattern,
		recoveryReBlocksQuotedExamplePattern,
		recoveryReExamplesLikeBlockedPattern,
		recoveryReQuotedVisibleBlockedPattern,
		recoveryRePromptsLikeBlockedPattern,
		recoveryReGitMutationsBroaderContextPattern,
		recoveryReGitCoverageContextPattern,
		recoveryReRegressionQuotedExamplePattern,
		recoveryReRegressionAnaphoricExamplePattern,
		recoveryReRegressionFollowedByAnaphoricPattern,
		recoveryReBlockedLiveVariantContextPattern,
		recoveryReAnaphoricReferencesContextPattern,
	)
	taskMutationText := recoveryReplaceAll(task,
		recoveryReDeliverablePattern,
		recoveryReContextObjectPattern,
		recoveryReMutationNounContextPattern,
		recoveryRePriorFixContextPattern,
		recoveryReDetectionContextPattern,
		recoveryRePatternChangeContextPattern,
		recoveryReGitPatternChangeContextPattern,
		recoveryReValidationEvidencePattern,
		recoveryReContractProhibitionPattern,
		recoveryReOnlyReviewContractPattern,
		recoveryReBlockedContextFragmentPattern,
		recoveryReRegressionEvidencePattern,
		recoveryReBlockedQuotedExamplePattern,
		recoveryReContextQuotedExamplePattern,
		recoveryReListedBlockedExamplePattern,
		recoveryReBlocksQuotedExamplePattern,
		recoveryReExamplesLikeBlockedPattern,
		recoveryReQuotedVisibleBlockedPattern,
		recoveryRePromptsLikeBlockedPattern,
		recoveryReGitMutationsBroaderContextPattern,
		recoveryReGitCoverageContextPattern,
		recoveryReRegressionQuotedExamplePattern,
		recoveryReRegressionAnaphoricExamplePattern,
		recoveryReRegressionFollowedByAnaphoricPattern,
		recoveryReBlockedLiveVariantContextPattern,
		recoveryReAnaphoricReferencesContextPattern,
		recoveryReNoAnaphoricMutationClausePattern,
		recoveryReNoDelegationClausePattern,
		recoveryReNoMutationClausePattern,
		recoveryReReadOnlyAll,
	)
	if a, ok := params.vals["acceptance"].(bool); !ok || a {
		return false
	}
	return agent != "" &&
		recoveryTest(recoveryAgentRe, agent) &&
		recoveryTest(recoveryReReadOnlyPattern, task) &&
		!recoveryTest(recoveryReDestructiveCommandPattern, taskDestructiveCommandText) &&
		!recoveryTest(recoveryReMutationVerbPattern, taskMutationText) &&
		!recoveryTest(recoveryReDelegationPattern, taskMutationText) &&
		!recoveryTest(recoveryReAnaphoricMutationPattern, taskMutationText) &&
		!recoveryTest(recoveryReDestructiveCommandPattern, taskMutationText) &&
		!recoveryTest(recoveryReDashLiveActionPattern, taskDashLiveActionText)
}
