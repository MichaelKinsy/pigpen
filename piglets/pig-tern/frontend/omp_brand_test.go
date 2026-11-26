package frontend

import (
	"reflect"
	"strings"
	"testing"
	"time"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

func TestOmpBrandComposeDeterministicAndExact(t *testing.T) {
	head := defaultOmpHead()
	if head.Width != ompMarkHeadWidth || head.Height != ompMarkHeadHeight {
		t.Fatalf("defaultOmpHead = %dx%d, want %dx%d", head.Width, head.Height, ompMarkHeadWidth, ompMarkHeadHeight)
	}
	a := composeOmpMark(head, ompPiToken)
	b := composeOmpMark(head, ompPiToken)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("composeOmpMark is not deterministic")
	}
	if !reflect.DeepEqual(a, composeOmpMark(fe.Mark{}, ompPiToken)) {
		t.Fatal("composeOmpMark(zero) does not equal composeOmpMark(defaultOmpHead())")
	}

	at := make(map[[2]int]string, len(a))
	for _, p := range a {
		if p.X < 0 || p.X >= ompMarkCanvasWidth || p.Y < 0 || p.Y >= ompMarkCanvasHeight {
			t.Fatalf("pixel %+v out of bounds", p)
		}
		at[[2]int{p.X, p.Y}] = p.Color
	}
	for _, p := range head.Pixels {
		gx, gy := ompMarkHeadOriginX+p.X, ompMarkHeadOriginY+p.Y
		if gx < 0 || gx >= ompMarkCanvasWidth || gy < 0 || gy >= ompMarkCanvasHeight {
			continue
		}
		if got := at[[2]int{gx, gy}]; got != p.Color {
			t.Errorf("pig pixel (%d,%d)->(%d,%d) = %q, want %q", p.X, p.Y, gx, gy, got, p.Color)
		}
	}
	// Transparent outside π and pig.
	for _, pt := range [][2]int{{0, 0}, {23, 8}, {7, 18}, {10, 14}, {0, 21}, {23, 21}} {
		if got, ok := at[pt]; ok {
			t.Errorf("expected transparent at %v, got %q", pt, got)
		}
	}
}

func TestOmpBrandWelcomeLogoFollowsSpriteAndTheme(t *testing.T) {
	h := readyOmpRoles(t)
	h.session.env.AppName = "oh-my-pig"
	h.session.env.Version = "0.1.0"
	h.session.welcomeTipIdx = 0

	pigHeader := fe.Lines{
		Lines: []string{
			"   ▄▄▄▄  ▄▄▄▄  v0.8.0",
			"   escape interrupt · ctrl+c/ctrl+d clear/exit · / commands · ! bash ·",
			"   ctrl+o more",
			"   Press ctrl+o to show full startup help and loaded resources.",
		},
		Click: &fe.Area{Row: 0, Column: 1, Rows: 4, Columns: 16},
	}

	ops := h.applyOps(fe.Op{Kind: fe.Insert, Region: fe.RegionMain, ID: "n2", Index: 0, Node: pigHeader})
	logo := node(ops, "n.n2.lockup/logo")
	if logo == nil || logo["k"] != "image" {
		t.Fatalf("welcome logo = %#v", logo)
	}
	p := props(logo)
	if p["role"] != "omp.welcome.logo" {
		t.Fatalf("logo role = %#v, want omp.welcome.logo", p["role"])
	}
	if _, hasBuiltin := p["builtin"]; hasBuiltin {
		t.Fatalf("logo must not use builtin omp: %#v", p)
	}
	blob1, _ := p["blob"].(string)
	if len(blob1) != 64 {
		t.Fatalf("logo blob = %q, want 64-char sha256", blob1)
	}
	if p["w"] != float64(ompLogoWidthPx) || p["h"] != float64(ompLogoHeightPx) {
		t.Fatalf("logo w/h = %v/%v, want %d/%d", p["w"], p["h"], ompLogoWidthPx, ompLogoHeightPx)
	}

	// Verify omp-brand stylesheet was sent with welcome logo sizing, light-dark π color,
	// animated pig spinner keyframes, and reduced-motion rule.
	if !h.session.ompBrandSheetSent {
		t.Fatal("omp-brand stylesheet was not sent")
	}
	sheet := h.session.ompBrandSheetText
	for _, want := range []string{
		"[data-role='omp.welcome.logo'] .sf-img-svg{width:48px !important;height:44px !important}",
		"[data-role='omp.welcome.logo'] .omp-pi{fill:light-dark(#1b1c20,#eedaad)}",
		"[data-role='omp.working.pig']{display:inline-block;",
		"@keyframes omp-pig-spin{",
		"@media (prefers-reduced-motion: reduce){[data-role='omp.working.pig']::before{animation:none}}",
	} {
		if !strings.Contains(sheet, want) {
			t.Errorf("omp-brand sheet missing %q", want)
		}
	}

	// Changing frame.Mark (/sprite) updates BOTH the welcome logo blob and the omp-brand stylesheet!
	customMark := fe.Mark{
		Width:  16,
		Height: 14,
		Pixels: []fe.MarkPixel{
			{X: 3, Y: 1, Color: "#ffa8b7"},
			{X: 4, Y: 5, Color: "#ffffff"},
			{X: 5, Y: 6, Color: "#18141e"},
			{X: 8, Y: 8, Color: "#ffa8b7"},
		},
	}
	if err := h.session.Apply(fe.Frame{Mark: &customMark}); err != nil {
		t.Fatal(err)
	}
	msgs := h.messages()
	var sawBrandSheet, sawBlob, sawSet bool
	var blob2 string
	for _, m := range msgs {
		if m.verb == "s" && m.body["name"] == ompBrandSheetName {
			css, _ := m.body["css"].(string)
			if strings.Contains(css, "#ffa8b7") {
				sawBrandSheet = true
			}
		}
		if m.verb == "b" {
			sawBlob = true
		}
		if m.verb == "f" {
			for _, rawOp := range m.body["ops"].([]any) {
				op := rawOp.([]any)
				if op[0] == "set" && op[1] == "n.n2.lockup/logo" {
					patch := op[2].(map[string]any)
					if b, ok := patch["blob"].(string); ok {
						sawSet = true
						blob2 = b
					}
				}
			}
		}
	}
	if !sawBrandSheet {
		t.Error("sprite change did not re-send omp-brand stylesheet with new colors")
	}
	if !sawBlob || !sawSet || blob2 == "" || blob2 == blob1 {
		t.Errorf("sprite change did not update welcome logo blob: sawBlob=%v sawSet=%v blob1=%q blob2=%q", sawBlob, sawSet, blob1, blob2)
	}
}

func TestOmpBrandWorkingPigSpinnerAndSpinnerless(t *testing.T) {
	h := readyOmpRoles(t)
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	h.session.now = func() time.Time { return now }

	// Active working row carries el[omp.working.pig], NOT spinner{style:"starburst"}.
	ops := h.applyOps(workingOp(fe.Insert, fe.Working{Kind: fe.WorkingAgent, Message: "Thinking"}))
	row := node(ops, "dock.activity.status.working")
	if row == nil || row["k"] != "row" || props(row)["role"] != "omp.working" {
		t.Fatalf("working row = %#v", row)
	}
	kids := row["c"].([]any)
	if len(kids) != 5 {
		t.Fatalf("working children = %d, want 5: %#v", len(kids), kids)
	}
	spin := kids[0].(map[string]any)
	if spin["k"] != "el" || props(spin)["tag"] != "span" || props(spin)["role"] != "omp.working.pig" || props(spin)["aria"] != "Working" {
		t.Fatalf("pig spinner = %#v", spin)
	}

	// Spinnerless working kind (non-nil empty Frames) omits the pig spinner.
	ops = h.applyOps(workingOp(fe.Update, fe.Working{Kind: fe.WorkingAgent, Message: "Working…", Frames: []string{}}))
	delSpinner := false
	for _, rawOp := range ops {
		op := rawOp.([]any)
		if op[0] == "del" && op[1] == "dock.activity.status.working.spinner" {
			delSpinner = true
		}
	}
	if !delSpinner {
		t.Fatalf("spinnerless update did not delete pig spinner: %#v", ops)
	}

	// Inserting a fresh spinnerless working indicator has 4 children starting with elapsed.
	h2 := readyOmpRoles(t)
	h2.session.now = func() time.Time { return now }
	ops2 := h2.applyOps(workingOp(fe.Insert, fe.Working{Kind: fe.WorkingAgent, Message: "Quiet…", Frames: []string{}}))
	row2 := node(ops2, "dock.activity.status.working")
	if row2 == nil {
		t.Fatalf("spinnerless working row missing: %#v", ops2)
	}
	kids2 := row2["c"].([]any)
	if len(kids2) != 4 {
		t.Fatalf("spinnerless working children = %d, want 4: %#v", len(kids2), kids2)
	}
	if kids2[0].(map[string]any)["k"] != "elapsed" {
		t.Fatalf("spinnerless first child = %#v, want elapsed", kids2[0])
	}
}

func TestNonOmpRolesNeverSendOmpBrandSheet(t *testing.T) {
	h := newHarness(t)
	h.reply(`,"credits":64,"cols":120,"cell":{"w":8,"h":16},"dark":true`)
	h.session.InputReady()
	h.messages()

	if err := h.session.Apply(fe.Frame{
		Ops: []fe.Op{
			{Kind: fe.Insert, Region: fe.RegionMain, ID: "a", Node: fe.Lines{Lines: []string{"hello"}}},
			{Kind: fe.Insert, Region: fe.RegionDock, ID: "w", Node: fe.Working{Kind: fe.WorkingAgent, Message: "Working"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	for _, m := range h.messages() {
		if m.verb == "s" && m.body["name"] == ompBrandSheetName {
			t.Fatalf("non-Omp pig-tern must never send omp-brand stylesheet: %#v", m)
		}
	}
}
