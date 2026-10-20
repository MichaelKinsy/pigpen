package bashparse

// The effect vocabulary a redirect proves (access-intent/effect.ts, the part redirect analysis
// reads). The root package aliases these types; they live here because bashparse cannot import it.

// AttributedEffect is a filesystem effect an access can have, including the fail-closed base case.
// `unproven` is not a third effect: it is the absence of a proof, and ADR 0013 section 10 makes it
// consult both directional surfaces, most-restrictive.
type AttributedEffect string

const (
	EffectRead     AttributedEffect = "read"
	EffectWrite    AttributedEffect = "write"
	EffectUnproven AttributedEffect = "unproven"
)

// EffectSource is what established an attribution: the blame fact ADR 0013 section 7 asks the
// review log to record. `retracted` is distinct from `unproven` on purpose.
type EffectSource string

const (
	SourceSyntax    EffectSource = "syntax"
	SourceCore      EffectSource = "core"
	SourceRetracted EffectSource = "retracted"
	SourceUnproven  EffectSource = "unproven"
)

// TokenEffect is a path token's attributed effect, paired with what established it.
type TokenEffect struct {
	Effect AttributedEffect
	Source EffectSource
}

// UnprovenEffect is the fail-closed base case: no proof, and nothing to blame for its absence.
var UnprovenEffect = TokenEffect{EffectUnproven, SourceUnproven}

var (
	syntaxReadEffect  = TokenEffect{EffectRead, SourceSyntax}
	syntaxWriteEffect = TokenEffect{EffectWrite, SourceSyntax}
)

// RedirectDestinationEffect is the effect a redirect operator proves for its destination token, or
// ok=false when the redirect names no file at all and no token should be collected
// (command-effects.ts redirectDestinationEffect).
//
// The operator is the whole proof: `> out.txt` writes out.txt whatever the command in front of it
// does, and `< in.txt` reads it. A syntax proof is therefore absolute. `>&` and `<&` are the two
// operators that may name either a file descriptor (`2>&1`, a duplication that touches no file) or
// a real file (`cmd >& out`); destinationIsDescriptor is the parse-tree fact that tells them apart,
// and the ok=false it produces is what keeps `2>&1`'s `1` out of the path surface. An operator
// outside the table proves nothing rather than dropping the token: dropping it would remove a path
// from the gates entirely, which is the one fail-open direction available here.
func RedirectDestinationEffect(operator string, destinationIsDescriptor bool) (TokenEffect, bool) {
	switch operator {
	case ">&", "<&":
		if destinationIsDescriptor {
			return TokenEffect{}, false
		}
		if operator == ">&" {
			return syntaxWriteEffect, true
		}
		return syntaxReadEffect, true
	case ">", ">>", ">|", "&>", "&>>":
		// Operators whose destination the shell truncates, appends to, or creates.
		return syntaxWriteEffect, true
	case "<", "<<<":
		// Operators whose destination the shell reads. `<<<` is a herestring, whose
		// herestring_redirect node carries the same shape; its destination is a literal rather than
		// a file in practice, and reading it proves no more than a read either way.
		return syntaxReadEffect, true
	}
	return UnprovenEffect, true
}
