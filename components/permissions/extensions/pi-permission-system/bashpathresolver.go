package pi_permission_system

// Port of src/access-intent/bash/bash-path-resolver.ts.

// BashPathRuleCandidate is a path-like token with the path's lexical and canonical forms for policy matching and the attributed
// effect that routes the token to a directional surface.
type BashPathRuleCandidate struct {
	Token  string
	Path   *AccessPath
	Effect TokenEffect
}

// BashExternalPath is a path resolving outside the working directory, with its attributed effect.
type BashExternalPath struct {
	Path   *AccessPath
	Effect TokenEffect
}
