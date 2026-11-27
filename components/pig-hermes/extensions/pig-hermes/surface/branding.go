package surface

import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

// PiG v0.8.0 native login / sprite grid dimensions.
const (
	LoginBrandWidth   = 41
	LoginBrandHeight  = 5
	LoginHeroWidth    = 32
	LoginHeroHeight   = 14
	LoginMascotWidth  = 16
	LoginMascotHeight = 14
)

// hermesArtPalette maps single-character ASCII pixel symbols to #RRGGBB colors
// for the Hermes Winged-Helmet PiG mascot, caduceus hero, and PIG-HERMES brand.
var hermesArtPalette = map[string]string{
	"O": "#18141E", // dark outline, eyes, nostrils, hooves
	"G": "#FFD700", // bright Hermes gold
	"A": "#FFBF00", // warm amber gold
	"B": "#CD7F32", // bronze rim & shading
	"W": "#FFF8DC", // cornsilk white wings & eye gleam
	"C": "#E6D5A7", // wing feather shadow
	"P": "#FFA8B7", // pig body pink
	"p": "#FFC4CE", // pig highlight pink
	"s": "#E88396", // pig snout pink
	"b": "#FF869A", // pig cheek blush
}

// hermesBrandGrid is the 41x5 gold-to-bronze block wordmark: "PIG-HERMES".
var hermesBrandGrid = []string{
	"GGG.GGG.GGG.....G.G.GGG.GGG.G...G.GGG.GGG",
	"G.G..G..G.......G.G.G...G.G.GG.GG.G...G..",
	"AAA..A..A.A.AAA.AAA.AAA.AA..A.A.A.AAA.AAA",
	"A....A..A.A.....A.A.A...A.A.A...A.A.....A",
	"B...BBB.BBB.....B.B.BBB.B.B.B...B.BBB.BBB",
}

// hermesMascotGrid is the 16x14 Hermes Winged-Helmet PiG pixel mascot.
var hermesMascotGrid = []string{
	"WW....GGGG....WW",
	"WWC..GAAAAG..CWW",
	".WWCGAAAAAAGCWW.",
	"..WOBBBBBBBBOW..",
	"...OPppppppPO...",
	"..OPpPPPPPPpPO..",
	"..OPOWPPPPWOPO..",
	"..OPOOPPPPOOPO..",
	"..OPbPssssPbPO..",
	"..OPPsOsOsPsPO..",
	"..OPPPssssPPPO..",
	"...OPPPPPPPPO...",
	"....OBGppGBO....",
	"....OO....OO....",
}

// hermesHeroGrid is the 32x14 hero scene: the Hermes Winged-Helmet PiG
// standing beside a golden winged caduceus staff.
var hermesHeroGrid = []string{
	"WW....GGGG....WW..WW....GG....WW",
	"WWC..GAAAAG..CWW..WWC..GAAG..CWW",
	".WWCGAAAAAAGCWW....WWC.GBBG.CWW.",
	"..WOBBBBBBBBOW......W..GGBG..W..",
	"...OPppppppPO.........A.GB.A....",
	"..OPpPPPPPPpPO.......A..GB..A...",
	"..OPOWPPPPWOPO........AAABAA....",
	"..OPOOPPPPOOPO.........AGGA.....",
	"..OPbPssssPbPO........A.GB.A....",
	"..OPPsOsOsPsPO.......A..GB..A...",
	"..OPPPssssPPPO........AAABAA....",
	"...OPPPPPPPPO..........AGGA.....",
	"....OBGppGBO............GB......",
	"....OO....OO...........BGGB.....",
}

// HermesSpriteDefinition returns the PiG v0.8.0 /sprite registration for the
// Hermes Winged-Helmet PiG mascot.
func HermesSpriteDefinition() sdk.SpriteDefinition {
	mascot := append([]string(nil), hermesMascotGrid...)
	return sdk.SpriteDefinition{
		ID:      "hermes-pig",
		Name:    "Hermes PiG",
		Tagline: "Winged messenger, forged in Go.",
		Mascot:  mascot,
		Palette: filterPaletteForGrids(hermesArtPalette, mascot),
	}
}

// HermesLoginDefinition returns the PiG v0.8.0 native login definition featuring
// the PIG-HERMES gold wordmark, winged-helmet pig + caduceus hero, and mascot.
func HermesLoginDefinition() sdk.LoginDefinition {
	brand := append([]string(nil), hermesBrandGrid...)
	hero := append([]string(nil), hermesHeroGrid...)
	mascot := append([]string(nil), hermesMascotGrid...)
	return sdk.LoginDefinition{
		Brand:       brand,
		Hero:        hero,
		Mascot:      mascot,
		Palette:     filterPaletteForGrids(hermesArtPalette, brand, hero, mascot),
		Name:        "pig-hermes",
		Description: "Hermes Agent on PiG",
		Tagline:     "Winged messenger, forged in Go.",
	}
}

func filterPaletteForGrids(palette map[string]string, grids ...[]string) map[string]string {
	used := make(map[byte]bool, len(palette))
	for _, grid := range grids {
		for _, row := range grid {
			for i := range len(row) {
				if row[i] != '.' {
					used[row[i]] = true
				}
			}
		}
	}
	out := make(map[string]string, len(used))
	for k, v := range palette {
		if len(k) == 1 && used[k[0]] {
			out[k] = v
		}
	}
	return out
}
