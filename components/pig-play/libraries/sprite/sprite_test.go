package sprite

// Twins of PiG's piglets/standard/extensions/piglogin tests that exercise the
// sprite catalogue, its persisted selection and the website art it reproduces
// (extension_test.go and login_art_test.go at MichaelKinsy/PiG d86eb93). The
// login-definition and golden-render twins left Pigpen with the pig-login Package:
// PiG has the sprite login built in since 0.4.0.

import (
	"image/color"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestVariantStateDefaultsAndPersists(t *testing.T) {
	root := t.TempDir()
	if got := ActiveVariant(root); got.ID != DefaultID {
		t.Fatalf("default variant = %q, want %q", got.ID, DefaultID)
	}
	if err := SaveVariant(root, "green"); err != nil {
		t.Fatal(err)
	}
	if got := ActiveVariant(root); got.ID != "green" {
		t.Fatalf("persisted variant = %q, want green", got.ID)
	}
	info, err := os.Stat(StatePath(root))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state mode = %o, want 600", info.Mode().Perm())
	}
	if filepath.Dir(StatePath(root)) != filepath.Join(root, "state", "pig-standard") {
		t.Fatalf("state path = %s", StatePath(root))
	}
}

func TestUnknownSavedVariantFallsBackToDefault(t *testing.T) {
	root := t.TempDir()
	path := StatePath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"variant":"unknown"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ActiveVariant(root); got.ID != DefaultID {
		t.Fatalf("unknown saved variant = %q, want %q", got.ID, DefaultID)
	}
}

func TestDefaultVariantIsTheWebsiteGreenPig(t *testing.T) {
	if Variants[0].ID != "pig-default" || DefaultID != "pig-default" {
		t.Fatalf("default variant = %q (Variants[0] = %q), want pig-default", DefaultID, Variants[0].ID)
	}
	if got := ActiveVariant(t.TempDir()); got.ID != "pig-default" {
		t.Fatalf("fresh config loads %q, want pig-default", got.ID)
	}
	if got := ActiveVariant(t.TempDir()); got.ID != "pig-default" {
		t.Fatalf("ActiveVariant on a fresh config = %q, want pig-default", got.ID)
	}
}

func TestPigDefaultResolvesByIDAndFromSavedState(t *testing.T) {
	if got := FindVariant("pig-default"); got.ID != "pig-default" || got.Logo == nil {
		t.Fatalf("FindVariant(pig-default) = %q", got.ID)
	}
	if _, ok := ByID("pig-default"); !ok {
		t.Fatal("/sprite set pig-default would be rejected")
	}
	root := t.TempDir()
	if err := SaveVariant(root, "pink"); err != nil {
		t.Fatal(err)
	}
	if got := ActiveVariant(root); got.ID != "pink" {
		t.Fatalf("saved pink loads %q; other variants must stay selectable", got.ID)
	}
	if err := SaveVariant(root, "pig-default"); err != nil {
		t.Fatal(err)
	}
	if got := ActiveVariant(root); got.ID != "pig-default" {
		t.Fatalf("saved pig-default loads %q", got.ID)
	}
}

// Every catalogue entry keeps the shared 16-by-14 grid, so a game can crop and
// scale any sprite from the same source.
func TestEveryVariantSpriteIsTheSharedGrid(t *testing.T) {
	for _, variant := range Variants {
		rows := MascotSpriteFor(variant)
		if len(rows) != 14 {
			t.Fatalf("%s sprite height = %d, want 14", variant.ID, len(rows))
		}
		for y, row := range rows {
			if len(row) != 16 {
				t.Fatalf("%s sprite row %d width = %d, want 16", variant.ID, y, len(row))
			}
		}
	}
}

// An unknown or empty ID resolves to the default sprite, never to a zero value.
func TestFindVariantFallsBackToDefault(t *testing.T) {
	for _, id := range []string{"", "no-such-pig"} {
		if got := FindVariant(id); got.ID != DefaultID {
			t.Fatalf("FindVariant(%q) = %q, want %q", id, got.ID, DefaultID)
		}
	}
	if _, ok := ByID("no-such-pig"); ok {
		t.Fatal("ByID accepted an unknown sprite")
	}
}

// The games read the sprite chosen with PiG's built-in /sprite (PiG 0.4.0, coding/piglogin/state.go): PiG writes
// json.Marshal({"variant": id}) and a newline, mode 0600, to <PIG_HOME>/state/pig-standard/login.json. Every one of
// PiG's base sprites is drawn as itself; an id PiG has and this catalogue lacks (a character sprite, or one an
// extension registered) is drawn as the default pig.
func TestGamesReadTheSelectionPiGsBuiltInLoginSaves(t *testing.T) {
	save := func(root, id string) {
		t.Helper()
		path := filepath.Join(root, "state", "pig-standard", "login.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`{"variant":"`+id+`"}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"pig-default", "pink", "green", "mint", "sandy", "grey", "blush", "lavender", "cloud", "sheriff"} {
		root := t.TempDir()
		save(root, id)
		if got := ActiveVariant(root); got.ID != id {
			t.Fatalf("PiG saved %q; the games draw %q", id, got.ID)
		}
	}
	for _, id := range []string{"pigrogu", "darth-vader", "an-extension-pig"} {
		root := t.TempDir()
		save(root, id)
		if got := ActiveVariant(root); got.ID != DefaultID {
			t.Fatalf("PiG saved %q, unknown here; the games draw %q, want %q", id, got.ID, DefaultID)
		}
	}
}

// Every symbol a sprite draws resolves to a color, and '.' is transparent.
func TestEveryVariantPaletteCoversItsSprite(t *testing.T) {
	for _, variant := range Variants {
		palette := MascotPalette(variant)
		if palette['.'].A != 0 {
			t.Fatalf("%s: '.' is not transparent", variant.ID)
		}
		for y, row := range MascotSpriteFor(variant) {
			for x := range len(row) {
				if _, ok := palette[row[x]]; !ok {
					t.Fatalf("%s: symbol %q at (%d,%d) has no palette color", variant.ID, row[x], x, y)
				}
			}
		}
	}
}

// websiteArt is assets/pig/website-art.txt: colors sampled from the PiG
// website art, with the SHA-256 of each source file in its header.
type websiteArt struct {
	logo map[string]color.RGBA
	pig  [][]color.RGBA
}

func loadWebsiteArt(t *testing.T) websiteArt {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "assets", "pig", "website-art.txt"))
	if err != nil {
		t.Fatal(err)
	}
	art := websiteArt{logo: map[string]color.RGBA{}}
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		colors := make([]color.RGBA, 0, len(fields)-1)
		for _, hex := range fields[1:] {
			value, err := strconv.ParseUint(hex, 16, 32)
			if err != nil || len(hex) != 6 {
				t.Fatalf("website-art.txt: bad color %q", hex)
			}
			colors = append(colors, color.RGBA{uint8(value >> 16), uint8(value >> 8), uint8(value), 0xFF})
		}
		if fields[0] == "pig" {
			art.pig = append(art.pig, colors)
		} else if len(colors) == 1 {
			art.logo[fields[0]] = colors[0]
		}
	}
	if len(art.pig) != 12 || len(art.pig[0]) != 14 || len(art.logo) != 4 {
		t.Fatalf("website-art.txt: %d pig rows, %d logo colors", len(art.pig), len(art.logo))
	}
	return art
}

// The website pig is a 14-by-12 pixel grid. The mascot grid is the same pig
// with a one-pixel transparent margin, and every cell matches the sampled
// website color.
func TestPigDefaultMascotMatchesWebsitePig(t *testing.T) {
	art := loadWebsiteArt(t)
	variant := FindVariant("pig-default")
	palette := MascotPalette(variant)
	sprite := MascotSpriteFor(variant)
	for gy := range 12 {
		for gx := range 14 {
			symbol := sprite[gy+1][gx+1]
			want, opaque := palette[symbol]
			if symbol == '.' || !opaque {
				continue
			}
			if got := art.pig[gy][gx]; distance(got, want) > 6 {
				t.Fatalf("cell (%d,%d) symbol %q = %s, website pig = %s", gx, gy, symbol, RGBAHex(want), RGBAHex(got))
			}
		}
	}
}

func distance(a, b color.RGBA) int {
	abs := func(v int) int { return max(v, -v) }
	return abs(int(a.R)-int(b.R)) + abs(int(a.G)-int(b.G)) + abs(int(a.B)-int(b.B))
}

func TestPigDefaultLogoMatchesWebsiteWordmark(t *testing.T) {
	art := loadWebsiteArt(t)
	logo := LogoFor(FindVariant("pig-default"))
	for _, tc := range []struct {
		name      string
		got, want color.RGBA
	}{
		{"period", logo.Period, art.logo["logo-period"]},
		{"shadow", logo.Shadow, art.logo["logo-ink"]},
	} {
		if tc.got != tc.want {
			t.Fatalf("%s = %s, website = %s", tc.name, RGBAHex(tc.got), RGBAHex(tc.want))
		}
	}
	for row, value := range logo.Ramp {
		if value != art.logo["logo-dark-text"] {
			t.Fatalf("letter row %d = %s, want the website dark-theme text %s", row, RGBAHex(value), RGBAHex(art.logo["logo-dark-text"]))
		}
	}
}

func TestHeroDrawsPiGWithPeriodInsideTheGrid(t *testing.T) {
	hero, _ := Hero(LogoFor(Default()))
	if len(hero) != HeroHeight {
		t.Fatalf("hero height = %d, want %d", len(hero), HeroHeight)
	}
	var periodCells int
	for y, row := range hero {
		if len(row) != HeroWidth {
			t.Fatalf("hero row %d width = %d, want %d", y, len(row), HeroWidth)
		}
		periodCells += strings.Count(row, string(HeroPeriodKey))
		if y >= LogoRows+1 && strings.Trim(row, ".") != "" {
			t.Fatalf("hero row %d below the shadow draws pixels: %q", y, row)
		}
	}
	if periodCells != 9 {
		t.Fatalf("period has %d pixels, want a 3x3 dot", periodCells)
	}
	if !strings.Contains(hero[LogoRows-1], "QQQ") {
		t.Fatalf("period is not on the baseline row: %q", hero[LogoRows-1])
	}
}
