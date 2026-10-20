package pi_permission_system

// Port of src/access-intent/effect.ts.

// Effect is a filesystem effect an access can have: "read" or "write".
type Effect string

// AttributedEffect is an effect attribution including the fail-closed base case "unproven".
type AttributedEffect string

// EffectSource is what established an attribution: "syntax", "core", "retracted" or "unproven".
type EffectSource string

const (
	EffectRead  AttributedEffect = "read"
	EffectWrite AttributedEffect = "write"
	// EffectUnproven is the absence of a proof.
	EffectUnproven AttributedEffect = "unproven"

	SourceSyntax    EffectSource = "syntax"
	SourceCore      EffectSource = "core"
	SourceRetracted EffectSource = "retracted"
	SourceUnproven  EffectSource = "unproven"
)

// TokenEffect is a path token's attributed effect paired with what established it.
type TokenEffect struct {
	Effect AttributedEffect
	Source EffectSource
}

// UnprovenEffect is UNPROVEN_EFFECT: no proof, and nothing to blame for its absence.
var UnprovenEffect = TokenEffect{Effect: EffectUnproven, Source: SourceUnproven}

// MergeTokenEffects combines two attributions of the same resolved path. Agreement keeps the effect and prefers whichever source
// has something to blame, in either order; disagreement falls to UnprovenEffect.
func MergeTokenEffects(a, b TokenEffect) TokenEffect {
	if a.Effect != b.Effect {
		return UnprovenEffect
	}
	if a.Source == SourceUnproven {
		return b
	}
	return a
}
