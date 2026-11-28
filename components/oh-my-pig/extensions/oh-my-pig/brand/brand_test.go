package brand

import (
	"reflect"
	"testing"
)

func TestComposeDeterministicAndExact(t *testing.T) {
	head := DefaultHead()
	if head.Width != HeadWidth || head.Height != HeadHeight {
		t.Fatalf("DefaultHead size = %dx%d, want %dx%d", head.Width, head.Height, HeadWidth, HeadHeight)
	}
	if len(head.Pixels) == 0 {
		t.Fatal("DefaultHead has no pixels")
	}

	a := Compose(head, PiDarkHex)
	b := Compose(head, PiDarkHex)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("Compose is not deterministic")
	}
	// Zero head falls back to DefaultHead.
	z := Compose(Head{}, PiDarkHex)
	if !reflect.DeepEqual(a, z) {
		t.Fatal("Compose(Head{}) does not equal Compose(DefaultHead())")
	}

	// Build lookup map and verify row-major ordering and bounds.
	at := make(map[[2]int]string, len(a))
	for i, p := range a {
		if p.X < 0 || p.X >= CanvasWidth || p.Y < 0 || p.Y >= CanvasHeight {
			t.Fatalf("pixel %+v out of %dx%d canvas", p, CanvasWidth, CanvasHeight)
		}
		if i > 0 {
			prev := a[i-1]
			if prev.Y > p.Y || (prev.Y == p.Y && prev.X >= p.X) {
				t.Fatalf("pixels not strictly row-major at %d: %+v then %+v", i, prev, p)
			}
		}
		at[[2]int{p.X, p.Y}] = p.Color
	}

	// Pig head pixels must appear verbatim at (HeadOriginX+x, HeadOriginY+y).
	for _, p := range head.Pixels {
		gx, gy := HeadOriginX+p.X, HeadOriginY+p.Y
		if gx < 0 || gx >= CanvasWidth || gy < 0 || gy >= CanvasHeight {
			continue
		}
		if got := at[[2]int{gx, gy}]; got != p.Color {
			t.Errorf("pig pixel (%d,%d) -> canvas (%d,%d) = %q, want %q", p.X, p.Y, gx, gy, got, p.Color)
		}
	}

	// Verify π bar flanks and bottom row under head, solid legs, checkerboard fade, and transparency outside.
	for _, pt := range [][2]int{
		{0, 9}, {4, 9}, {19, 9}, {23, 9}, // bar left/right of head
		{0, 12}, {11, 12}, {23, 12}, // bar row below head bottom outline (y=11)
		{6, 13}, {9, 17}, // left leg solid
		{6, 18}, {8, 18}, {7, 19}, {9, 19}, {6, 20}, {8, 20}, // left leg checkerboard (x+y even)
		{14, 13}, {17, 21}, // right leg solid
	} {
		if got := at[pt]; got != PiDarkHex {
			t.Errorf("pi pixel %v = %q, want %q", pt, got, PiDarkHex)
		}
	}

	for _, pt := range [][2]int{
		{0, 0}, {6, 0}, {17, 0}, {23, 0}, // corners above bar
		{0, 8}, {23, 8}, // row above bar
		{7, 18}, {9, 18}, {6, 19}, {8, 19}, {7, 20}, {9, 20}, // checkerboard gaps
		{6, 21}, {9, 21}, // below left leg fade
		{10, 13}, {13, 21}, // gap between legs
		{0, 13}, {5, 21}, {18, 13}, {23, 21}, // outside legs
	} {
		if got, ok := at[pt]; ok {
			t.Errorf("expected transparent at %v, got %q", pt, got)
		}
	}
}

func TestComposeFollowsCustomHead(t *testing.T) {
	custom := Head{
		Width:  HeadWidth,
		Height: HeadHeight,
		Pixels: []Pixel{
			{X: 0, Y: 0, Color: "#ff0000"},   // falls off canvas (gy=-1)
			{X: 3, Y: 1, Color: "#ffa8b7"},   // lands at (7, 0)
			{X: 8, Y: 10, Color: "#00ffaa"},  // lands at (12, 9), overwrites bar
			{X: 15, Y: 12, Color: "#112233"}, // lands at (19, 11), overwrites bar
		},
	}
	got := Compose(custom, PiColorToken)
	at := make(map[[2]int]string, len(got))
	for _, p := range got {
		at[[2]int{p.X, p.Y}] = p.Color
	}
	if at[[2]int{7, 0}] != "#ffa8b7" {
		t.Errorf("(7,0) = %q, want #ffa8b7", at[[2]int{7, 0}])
	}
	if at[[2]int{12, 9}] != "#00ffaa" {
		t.Errorf("(12,9) = %q, want #00ffaa (head must draw over bar)", at[[2]int{12, 9}])
	}
	if at[[2]int{19, 11}] != "#112233" {
		t.Errorf("(19,11) = %q, want #112233", at[[2]int{19, 11}])
	}
	if at[[2]int{12, 12}] != PiColorToken {
		t.Errorf("(12,12) = %q, want %q", at[[2]int{12, 12}], PiColorToken)
	}
}
