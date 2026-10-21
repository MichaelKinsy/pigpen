package sprites

import (
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestSharedOffersEightSixAndFourPixelHeads(t *testing.T) {
	src := Shared("")
	if src == nil {
		t.Fatal("Shared returned nil")
	}
	if got := src.Sizes(); !slices.Equal(got, []int{8, 6, 4}) {
		t.Fatalf("sizes %v", got)
	}
	for _, size := range src.Sizes() {
		rows := src.Head(size)
		if len(rows) != size {
			t.Fatalf("size %d has %d rows", size, len(rows))
		}
		for i, row := range rows {
			if len(row) != size {
				t.Fatalf("size %d row %d is %q", size, i, row)
			}
		}
	}
}

func TestTheEightPixelHeadIsTheAngryPigsPigVerbatim(t *testing.T) {
	want := []string{
		".OO..OO.",
		"OeeOOeeO",
		"OPPPPPPO",
		"OWKPPKWO",
		"ObPssPbO",
		"OPsKKsPO",
		".OPPPPO.",
		"..OOOO..",
	}
	if got := Shared("").Head(8); !slices.Equal(got, want) {
		t.Fatalf("8x8 head differs from PiG d86eb93 angrypigs/art.go pigBall:\n%q", got)
	}
}

func TestEverySymbolInEveryHeadHasAColor(t *testing.T) {
	src := Shared("pink")
	for member := range 12 {
		pal := src.Palette(member)
		for _, size := range src.Sizes() {
			for _, row := range src.Head(size) {
				for i := range len(row) {
					if _, ok := pal[row[i]]; !ok && row[i] != '.' {
						t.Fatalf("member %d size %d: symbol %q has no color", member, size, row[i])
					}
				}
			}
		}
		if pal['.'].A != 0 {
			t.Fatalf("member %d: '.' must be transparent", member)
		}
	}
}

func TestTheLeaderWearsTheChosenSpriteAndTheHerdIsAssorted(t *testing.T) {
	src := Shared("pink")
	if got := src.Palette(0)['P']; got != (color.RGBA{0xFF, 0xA8, 0xB7, 0xFF}) {
		t.Fatalf("pink leader body %v", got)
	}
	seen := map[color.RGBA]bool{src.Palette(0)['P']: true}
	for member := 1; member <= 8; member++ {
		body := src.Palette(member)['P']
		if body == src.Palette(0)['P'] {
			t.Fatalf("follower %d has the leader's color", member)
		}
		seen[body] = true
	}
	if len(seen) < 6 {
		t.Fatalf("herd looks uniform: %d distinct bodies in 9 pigs", len(seen))
	}
	if src.Palette(3)['P'] != src.Palette(3)['P'] || src.Palette(1)['P'] == src.Palette(2)['P'] {
		t.Fatal("neighbouring followers must differ, and a member's palette is stable")
	}
	// Followers keep cycling without running out.
	if src.Palette(500)['P'].A == 0 {
		t.Fatal("member 500 has no palette")
	}
}

func TestUnknownVariantFallsBackToTheDefaultGreenPig(t *testing.T) {
	def := Shared("").Palette(0)['P']
	if def != (color.RGBA{0x48, 0xA3, 0x81, 0xFF}) {
		t.Fatalf("default leader %v", def)
	}
	if Shared("no-such-pig").Palette(0)['P'] != def {
		t.Fatal("unknown id must fall back to the default")
	}
}

func TestLoadVariantIDReadsTheLoginStateFile(t *testing.T) {
	home := t.TempDir()
	if got := LoadVariantID(home); got != "pig-default" {
		t.Fatalf("no file: %q", got)
	}
	dir := filepath.Join(home, "state", "pig-standard")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "login.json")
	for content, want := range map[string]string{
		`{"variant":"lavender"}` + "\n": "lavender",
		`not json`:                      "pig-default",
		`{"variant":42}`:                "pig-default",
		`{}`:                            "pig-default",
		`{"variant":"no-such-pig"}`:     "pig-default",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := LoadVariantID(home); got != want {
			t.Errorf("%q: %q, want %q", content, got, want)
		}
	}
}

func TestPaletteMapsAreCopies(t *testing.T) {
	src := Shared("")
	src.Palette(0)['P'] = color.RGBA{1, 2, 3, 4}
	if src.Palette(0)['P'] == (color.RGBA{1, 2, 3, 4}) {
		t.Fatal("mutating a returned palette changed the source")
	}
}

func TestNoFollowerEverWearsItsLeadersOrItsNeighboursColor(t *testing.T) {
	for _, id := range []string{"", "pig-default", "pink", "green", "mint", "sandy", "grey", "blush", "lavender", "cloud", "sheriff"} {
		src := Shared(id)
		leader := src.Palette(0)['P']
		for member := 1; member <= 60; member++ {
			body := src.Palette(member)['P']
			if body == leader {
				t.Fatalf("leader %q: follower %d has the leader's color", id, member)
			}
			if member > 1 && body == src.Palette(member - 1)['P'] {
				t.Fatalf("leader %q: followers %d and %d share a color", id, member-1, member)
			}
		}
	}
}

// The colours the leader and the herd wear are the shared sprite library's. These are the values Pig Snake's own copy of the
// catalogue held before it adopted the library (PiG d86eb93, piglogin/variants.go): the move changed no pig.
func TestSharedPalettesAreTheColoursOfTheCatalogueTheGameHadBefore(t *testing.T) {
	rgb := func(r, g, b uint8) color.RGBA { return color.RGBA{R: r, G: g, B: b, A: 0xFF} }
	type colours struct{ body, highlight, snout, blush, ear color.RGBA }
	want := map[string]colours{
		"pig-default": {rgb(0x48, 0xA3, 0x81), rgb(0x64, 0xBE, 0x9E), rgb(0x32, 0x77, 0x5E), rgb(0x82, 0xDB, 0xBA), rgb(0x48, 0xA3, 0x81)},
		"pink":        {rgb(0xFF, 0xA8, 0xB7), rgb(0xFF, 0xC4, 0xCE), rgb(0xE8, 0x83, 0x96), rgb(0xFF, 0x86, 0x9A), rgb(0xFF, 0x90, 0xA4)},
		"green":       {rgb(0x16, 0xA3, 0x6A), rgb(0x45, 0xC8, 0x91), rgb(0x0F, 0x7A, 0x50), rgb(0x77, 0xD9, 0xAC), rgb(0x16, 0xA3, 0x6A)},
		"mint":        {rgb(0xC8, 0xF0, 0xDD), rgb(0xDF, 0xF6, 0xEB), rgb(0x96, 0xD0, 0xB4), rgb(0xF4, 0x9A, 0xA6), rgb(0xB8, 0xE4, 0xCF)},
		"sandy":       {rgb(0xE8, 0xC8, 0x9A), rgb(0xF4, 0xDB, 0xAF), rgb(0xC0, 0x99, 0x66), rgb(0xE8, 0x83, 0x96), rgb(0xD8, 0xAC, 0x78)},
		"grey":        {rgb(0xB6, 0xB2, 0xC0), rgb(0xD2, 0xCF, 0xDB), rgb(0x88, 0x84, 0x95), rgb(0xE8, 0x83, 0x96), rgb(0xA2, 0x9E, 0xB0)},
		"blush":       {rgb(0xFF, 0xB8, 0xC9), rgb(0xFF, 0xD4, 0xDD), rgb(0xE8, 0x6E, 0x88), rgb(0xFF, 0x52, 0x6E), rgb(0xFF, 0x8C, 0xA2)},
		"lavender":    {rgb(0xC9, 0xB4, 0xEA), rgb(0xDD, 0xCC, 0xF2), rgb(0x9F, 0x82, 0xC4), rgb(0xE8, 0x83, 0x96), rgb(0xB6, 0x9D, 0xDC)},
		"cloud":       {rgb(0xB0, 0xD8, 0xEC), rgb(0xCC, 0xE8, 0xF4), rgb(0x7C, 0xAE, 0xC8), rgb(0xF4, 0x9A, 0xA6), rgb(0x9C, 0xCA, 0xE0)},
		"sheriff":     {rgb(0xF2, 0xB8, 0xA8), rgb(0xFF, 0xD2, 0xC7), rgb(0xD9, 0x89, 0x78), rgb(0xD9, 0x5F, 0x4C), rgb(0xE7, 0x98, 0x88)},
	}
	for id, c := range want {
		pal := Shared(id).Palette(0)
		got := colours{pal['P'], pal['p'], pal['s'], pal['b'], pal['e']}
		if got != c {
			t.Errorf("%s: leader colours %+v, want %+v", id, got, c)
		}
		if pal['O'] != rgb(0x18, 0x14, 0x1E) && pal['O'] != rgb(0x18, 0x15, 0x1D) || pal['K'] != pal['O'] || pal['W'].A == 0 {
			t.Errorf("%s: outline, eye or white missing: O=%v K=%v W=%v", id, pal['O'], pal['K'], pal['W'])
		}
	}
	// The catalogue order is the order followers cycle in: after a pink leader the first follower is the default pig.
	if got := Shared("pink").Palette(1)['P']; got != want["pig-default"].body {
		t.Errorf("first follower of a pink leader: %v", got)
	}
}

// Only the Sheriff wears a hat, and it is drawn in the shared Sheriff variant's own colours for H, h and S.
func TestOnlyTheSheriffWearsAHatInThePaletteOfTheSharedSheriff(t *testing.T) {
	for _, id := range []string{"", "pig-default", "pink", "lavender"} {
		if h, ok := Shared(id).(Hatted); !ok || h.Hat(8) != nil || h.Hat(6) != nil || h.Hat(4) != nil {
			t.Errorf("%q must not wear a hat (Hatted=%v)", id, ok)
		}
	}
	src := Shared("sheriff")
	hatted, ok := src.(Hatted)
	if !ok {
		t.Fatal("the shared source must offer hats")
	}
	pal := src.Palette(0)
	for _, sym := range []byte{'H', 'h', 'S'} {
		if pal[sym].A == 0 {
			t.Errorf("the Sheriff palette has no colour for %q", sym)
		}
	}
	for _, size := range src.Sizes() {
		hat := hatted.Hat(size)
		if len(hat) == 0 || len(hat) > size/2+1 {
			t.Fatalf("size %d: hat has %d rows", size, len(hat))
		}
		for i, row := range hat {
			if len(row) != size {
				t.Fatalf("size %d: hat row %d is %q", size, i, row)
			}
			for j := range len(row) {
				if row[j] != '.' && pal[row[j]].A == 0 {
					t.Fatalf("size %d: symbol %q of the hat has no colour", size, row[j])
				}
			}
		}
	}
	if hatted.Hat(7) != nil {
		t.Error("an unknown size has no hat")
	}
	// A follower never wears the leader's hat: Hat describes the leader only, and the followers' palettes have no hat colours
	// that differ from the Sheriff's (the renderer draws the hat on member 0 alone, see scene).
}
