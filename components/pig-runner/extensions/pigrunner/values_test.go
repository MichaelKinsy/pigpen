package pigrunner

// Tests added by the relocation (not in upstream): the mutation check showed that upstream's
// tests did not pin the jump velocity, the mid-air drop, or the "one jump at a time" rule.

import (
	"math"
	"testing"

	"github.com/MichaelKinsy/pigpen/components/pig-play/libraries/sprite"
)

func newTestGame() *Game { return NewGameWithVariant(80, 0, sprite.FindVariant("")) }

func TestJumpVelocityFollowsSpeed(t *testing.T) {
	g := newTestGame()
	g.Speed = 8
	g.Jump()
	want := -(10.0 + 8.0/10) * dinoScale
	if math.Abs(g.Pig.VelY-want) > 1e-12 {
		t.Fatalf("jump velocity at speed 8 = %v, want %v", g.Pig.VelY, want)
	}
}

func TestOnlyOneJumpAtATime(t *testing.T) {
	g := newTestGame()
	g.Speed = 6
	g.Jump()
	first := g.Pig.VelY
	g.Speed = 12 // the pig is still on the ground line at the start of the jump
	g.Jump()
	if g.Pig.VelY != first {
		t.Fatalf("a second Jump restarted the jump: %v then %v", first, g.Pig.VelY)
	}
}

func TestDuckInTheAirDropsOnce(t *testing.T) {
	g := newTestGame()
	g.Jump()
	for range 5 {
		g.Update()
	}
	if g.Pig.Y >= groundY {
		t.Fatal("the pig did not leave the ground")
	}
	g.Duck()
	if !g.Pig.speedDrop || g.Pig.VelY != dinoScale {
		t.Fatalf("mid-air duck: speedDrop %v, VelY %v, want true and %v", g.Pig.speedDrop, g.Pig.VelY, dinoScale)
	}
	g.Pig.VelY = 0.5
	g.Duck()
	if g.Pig.VelY != 0.5 {
		t.Fatal("a second mid-air duck reset the drop speed")
	}
}

func TestGameplayValuesAreThoseOfTheOriginal(t *testing.T) {
	for _, tc := range []struct {
		name      string
		got, want float64
	}{
		{"dinoSpeed", dinoSpeed, 6}, {"dinoAcceleration", dinoAcceleration, 0.001}, {"dinoMaxSpeed", dinoMaxSpeed, 13},
		{"dinoGravity", dinoGravity, 0.6}, {"dinoJumpVelocity", dinoJumpVelocity, 10}, {"dinoDropVelocity", dinoDropVelocity, 5},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
}
