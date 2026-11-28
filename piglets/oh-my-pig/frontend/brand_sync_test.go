package frontend

import (
	"reflect"
	"testing"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/brand"
	ternfe "github.com/MichaelKinsy/pigpen/piglets/pig-tern/frontend"
)

// TestBrandCompositionMatchesExtension verifies that the frontend member's
// Candidate-3 π-plus-pig mashup composition and default green pig head match
// the extension's `brand` package pixel for pixel.
func TestBrandCompositionMatchesExtension(t *testing.T) {
	extHead := brand.DefaultHead()
	feHead := ternfe.DefaultOmpHeadForTest()
	if extHead.Width != feHead.Width || extHead.Height != feHead.Height {
		t.Fatalf("head dims: extension %dx%d vs frontend %dx%d", extHead.Width, extHead.Height, feHead.Width, feHead.Height)
	}
	wantHead := make([]fe.MarkPixel, len(extHead.Pixels))
	for i, p := range extHead.Pixels {
		wantHead[i] = fe.MarkPixel{X: p.X, Y: p.Y, Color: p.Color}
	}
	if !reflect.DeepEqual(wantHead, feHead.Pixels) {
		t.Fatalf("default pig head differs between extension brand package (%d px) and frontend member (%d px)", len(wantHead), len(feHead.Pixels))
	}

	extComposed := brand.Compose(extHead, brand.PiColorToken)
	feComposed := ternfe.ComposeOmpMarkForTest(feHead, brand.PiColorToken)
	wantComposed := make([]fe.MarkPixel, len(extComposed))
	for i, p := range extComposed {
		wantComposed[i] = fe.MarkPixel{X: p.X, Y: p.Y, Color: p.Color}
	}
	if !reflect.DeepEqual(wantComposed, feComposed) {
		t.Fatalf("composed 24x22 mark differs between extension brand package (%d px) and frontend member (%d px)", len(wantComposed), len(feComposed))
	}
}
