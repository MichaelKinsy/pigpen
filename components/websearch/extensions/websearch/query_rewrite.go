package websearch

import "context"

// Port of query-rewrite.ts. Stub: nothing is rewritten yet.

type modelCandidate struct{ provider, id string }

var rewriteModelCandidates []modelCandidate

func RewriteSearchQuery(_ context.Context, _ string, _ SideCallContext) (string, error) {
	return "", errAnswerNotPorted
}
