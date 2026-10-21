package pi_permission_system

// Port of src/access-intent/effect.ts.

import "github.com/MichaelKinsy/pigpen/pi-permission-system/bashparse"

// Effect is a filesystem effect an access can have: "read" or "write".
type Effect string

// The attributed-effect vocabulary lives in bashparse (the redirect analysis reads it and cannot import this package); these are
// the same types.
type (
	// AttributedEffect is an effect attribution including the fail-closed base case "unproven".
	AttributedEffect = bashparse.AttributedEffect
	// EffectSource is what established an attribution: "syntax", "core", "retracted" or "unproven".
	EffectSource = bashparse.EffectSource
	// TokenEffect is a path token's attributed effect paired with what established it.
	TokenEffect = bashparse.TokenEffect
)

const (
	EffectRead  AttributedEffect = bashparse.EffectRead
	EffectWrite AttributedEffect = bashparse.EffectWrite
	// EffectUnproven is the absence of a proof.
	EffectUnproven AttributedEffect = bashparse.EffectUnproven

	SourceSyntax    EffectSource = bashparse.SourceSyntax
	SourceCore      EffectSource = bashparse.SourceCore
	SourceRetracted EffectSource = bashparse.SourceRetracted
	SourceUnproven  EffectSource = bashparse.SourceUnproven
)

// UnprovenEffect is UNPROVEN_EFFECT: no proof, and nothing to blame for its absence.
var UnprovenEffect = bashparse.UnprovenEffect

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
