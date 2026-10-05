package angrypigs

// Tests added by the relocation (not in upstream): the mutation check showed that upstream's
// tests reference the tuning constants symbolically, so changing a value went unnoticed. These
// pin the gameplay values of MichaelKinsy/PiG d86eb93 with literals and observable effects.

import (
	"math"
	"testing"
)

func TestGameplayValuesAreThoseOfTheOriginal(t *testing.T) {
	for _, tc := range []struct {
		name      string
		got, want float64
	}{
		{"gravity", gravity, 120}, {"maxSpeed", maxSpeed, 165}, {"hardHitSpeed", hardHitSpeed, 95},
		{"pigRadius", pigRadius, 3.5}, {"stepSeconds", stepSeconds, 1.0 / 120},
		{"birdPoints", birdPoints, 500}, {"blockPoints", blockPoints, 50}, {"unusedPig", unusedPig, 1000},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
	if maxHits[cellWood] != 2 || maxHits[cellStone] != 3 || maxHits[cellIce] != 1 || maxHits[cellBird] != 1 {
		t.Errorf("maxHits = %v, want wood 2, stone 3, ice 1, bird 1", maxHits)
	}
}

// Full power at 45 degrees launches at the maximum speed; gravity pulls the pig down by exactly
// its constant each step.
func TestLaunchSpeedAndGravityStep(t *testing.T) {
	g := emptyField()
	g.aim(45-g.angle, 100-g.power)
	v := g.launchVelocity()
	if got := math.Hypot(v.x, v.y); math.Abs(got-165) > 1e-9 {
		t.Fatalf("launch speed at full power = %v, want 165", got)
	}
	g.flying = true
	g.pig = point{x: 100, y: 40}
	g.velocity = point{}
	g.step()
	if want := -120.0 / 120; math.Abs(g.velocity.y-want) > 1e-9 {
		t.Fatalf("vertical velocity after one step = %v, want %v", g.velocity.y, want)
	}
}

// The elevation stops at 5 degrees and cannot go below.
func TestAimLowerBoundIsFiveDegrees(t *testing.T) {
	g := emptyField()
	g.aim(-1000, 0)
	if g.angle != 5 {
		t.Fatalf("angle = %d, want 5", g.angle)
	}
}
