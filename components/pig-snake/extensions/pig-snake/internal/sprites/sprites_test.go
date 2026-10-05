package sprites

import (
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestBuiltinOffersEightSixAndFourPixelHeads(t *testing.T) {
	src := Builtin("")
	if src == nil {
		t.Fatal("Builtin returned nil")
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
	if got := Builtin("").Head(8); !slices.Equal(got, want) {
		t.Fatalf("8x8 head differs from PiG d86eb93 angrypigs/art.go pigBall:\n%q", got)
	}
}

func TestEverySymbolInEveryHeadHasAColor(t *testing.T) {
	src := Builtin("pink")
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
	src := Builtin("pink")
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
	def := Builtin("").Palette(0)['P']
	if def != (color.RGBA{0x48, 0xA3, 0x81, 0xFF}) {
		t.Fatalf("default leader %v", def)
	}
	if Builtin("no-such-pig").Palette(0)['P'] != def {
		t.Fatal("unknown id must fall back to the default")
	}
}

func TestLoadVariantIDReadsTheLoginStateFile(t *testing.T) {
	home := t.TempDir()
	if got := LoadVariantID(home); got != "" {
		t.Fatalf("no file: %q", got)
	}
	dir := filepath.Join(home, "state", "pig-standard")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "login.json")
	for content, want := range map[string]string{
		`{"variant":"lavender"}` + "\n": "lavender",
		`not json`:                      "",
		`{"variant":42}`:                "",
		`{}`:                            "",
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
	src := Builtin("")
	src.Palette(0)['P'] = color.RGBA{1, 2, 3, 4}
	if src.Palette(0)['P'] == (color.RGBA{1, 2, 3, 4}) {
		t.Fatal("mutating a returned palette changed the source")
	}
}

func TestNoFollowerEverWearsItsLeadersOrItsNeighboursColor(t *testing.T) {
	for _, id := range []string{"", "pig-default", "pink", "green", "mint", "sandy", "grey", "blush", "lavender", "cloud", "sheriff"} {
		src := Builtin(id)
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
