// Package sprites is the one seam between Pig Snake and the pig art.
//
// The game and its renderer draw pigs only through [Source]. The built-in
// source below carries the art adapted from PiG d86eb93 (the 8x8 pig of
// Angry Pigs, the login sprite palettes; MIT, Michael Kinsy, see
// CREDITS.md). Lane pigpen-games extracts a shared sprite package: when it
// lands, a small adapter that implements Source on top of it replaces
// [Builtin] in one place (extension.go) and nothing else changes.
package sprites

import (
	"encoding/json"
	"image/color"
	"io"
	"maps"
	"os"
	"path/filepath"
)

// Source supplies the pig art.
//
// Implementations must be comparable values (a pointer), because the renderer
// keeps per-source caches keyed by the source and returned maps are copied by
// the caller's choice, not required to be shared.
type Source interface {
	// Sizes lists the available head sizes in pixels, largest first. A head
	// is square: Head(size) has size rows of size symbols.
	Sizes() []int
	// Head returns the pig head art of one of Sizes. Each byte indexes the
	// palette; '.' is transparent.
	Head(size int) []string
	// Palette returns the colors for herd member 0 (the leader, wearing the
	// sprite the player chose with /sprite) and members 1, 2, ... (the
	// followers). Any member number is valid; followers cycle.
	Palette(member int) map[byte]color.RGBA
}

func rgb(r, g, b uint8) color.RGBA { return color.RGBA{R: r, G: g, B: b, A: 0xFF} }

// variant is one pig colouring, taken from PiG's sprite catalogue
// (piglogin/variants.go at d86eb93).
type variant struct {
	id                                 string
	body, highlight, snout, blush, ear color.RGBA
	overrides                          map[byte]color.RGBA
}

// The catalogue order is the order followers cycle in. The first entry is the
// default leader.
var variants = []variant{
	{"pig-default", rgb(0x48, 0xA3, 0x81), rgb(0x64, 0xBE, 0x9E), rgb(0x32, 0x77, 0x5E), rgb(0x82, 0xDB, 0xBA), rgb(0x48, 0xA3, 0x81),
		map[byte]color.RGBA{'O': rgb(0x18, 0x15, 0x1D), 'K': rgb(0x18, 0x15, 0x1D), 'W': rgb(0xFE, 0xFE, 0xFE)}},
	{"pink", rgb(0xFF, 0xA8, 0xB7), rgb(0xFF, 0xC4, 0xCE), rgb(0xE8, 0x83, 0x96), rgb(0xFF, 0x86, 0x9A), rgb(0xFF, 0x90, 0xA4), nil},
	{"green", rgb(0x16, 0xA3, 0x6A), rgb(0x45, 0xC8, 0x91), rgb(0x0F, 0x7A, 0x50), rgb(0x77, 0xD9, 0xAC), rgb(0x16, 0xA3, 0x6A), nil},
	{"mint", rgb(0xC8, 0xF0, 0xDD), rgb(0xDF, 0xF6, 0xEB), rgb(0x96, 0xD0, 0xB4), rgb(0xF4, 0x9A, 0xA6), rgb(0xB8, 0xE4, 0xCF), nil},
	{"sandy", rgb(0xE8, 0xC8, 0x9A), rgb(0xF4, 0xDB, 0xAF), rgb(0xC0, 0x99, 0x66), rgb(0xE8, 0x83, 0x96), rgb(0xD8, 0xAC, 0x78), nil},
	{"grey", rgb(0xB6, 0xB2, 0xC0), rgb(0xD2, 0xCF, 0xDB), rgb(0x88, 0x84, 0x95), rgb(0xE8, 0x83, 0x96), rgb(0xA2, 0x9E, 0xB0), nil},
	{"blush", rgb(0xFF, 0xB8, 0xC9), rgb(0xFF, 0xD4, 0xDD), rgb(0xE8, 0x6E, 0x88), rgb(0xFF, 0x52, 0x6E), rgb(0xFF, 0x8C, 0xA2), nil},
	{"lavender", rgb(0xC9, 0xB4, 0xEA), rgb(0xDD, 0xCC, 0xF2), rgb(0x9F, 0x82, 0xC4), rgb(0xE8, 0x83, 0x96), rgb(0xB6, 0x9D, 0xDC), nil},
	{"cloud", rgb(0xB0, 0xD8, 0xEC), rgb(0xCC, 0xE8, 0xF4), rgb(0x7C, 0xAE, 0xC8), rgb(0xF4, 0x9A, 0xA6), rgb(0x9C, 0xCA, 0xE0), nil},
	// The Sheriff's hat art is not used here: only its skin colors.
	{"sheriff", rgb(0xF2, 0xB8, 0xA8), rgb(0xFF, 0xD2, 0xC7), rgb(0xD9, 0x89, 0x78), rgb(0xD9, 0x5F, 0x4C), rgb(0xE7, 0x98, 0x88), nil},
}

// basePalette holds the symbols every pig shares (piglogin/art.go).
var basePalette = map[byte]color.RGBA{
	'.': {},
	'O': rgb(0x18, 0x14, 0x1E),
	'K': rgb(0x18, 0x14, 0x1E),
	'P': rgb(0xFF, 0xA8, 0xB7),
	'p': rgb(0xFF, 0xC4, 0xCE),
	's': rgb(0xE8, 0x83, 0x96),
	'b': rgb(0xFF, 0x86, 0x9A),
	'W': rgb(0xFF, 0xFF, 0xFF),
	'e': rgb(0xFF, 0x90, 0xA4),
}

func (v variant) palette() map[byte]color.RGBA {
	p := make(map[byte]color.RGBA, len(basePalette))
	maps.Copy(p, basePalette)
	p['P'], p['p'], p['s'], p['b'], p['e'] = v.body, v.highlight, v.snout, v.blush, v.ear
	maps.Copy(p, v.overrides)
	return p
}

func findVariant(id string) int {
	for i, v := range variants {
		if v.id == id {
			return i
		}
	}
	return 0
}

// The pig heads. head8 is the launched pig of Angry Pigs (angrypigs/art.go
// pigBall at d86eb93), unchanged. head6 and head4 are Pig Snake's own
// smaller drawings of the same pig for small terminals: ears, eyes, snout
// (no dark outline, which disappears against the field at that size).
var (
	head8 = []string{
		".OO..OO.",
		"OeeOOeeO",
		"OPPPPPPO",
		"OWKPPKWO",
		"ObPssPbO",
		"OPsKKsPO",
		".OPPPPO.",
		"..OOOO..",
	}
	head6 = []string{
		"Pe..eP",
		"PPPPPP",
		"PKPPKP",
		"PPssPP",
		"PsKKsP",
		".PPPP.",
	}
	head4 = []string{
		"e..e",
		"KPPK",
		"PssP",
		".PP.",
	}
)

type builtin struct {
	leader int
}

// Builtin returns the source backed by the art adapted from PiG d86eb93. The
// leader wears the sprite variantID names (an unknown or empty id gives the
// default green pig); followers are the other colourings in catalogue order.
func Builtin(variantID string) Source { return &builtin{leader: findVariant(variantID)} }

func (*builtin) Sizes() []int { return []int{8, 6, 4} }

func (*builtin) Head(size int) []string {
	switch size {
	case 8:
		return head8
	case 6:
		return head6
	case 4:
		return head4
	}
	return nil
}

func (b *builtin) Palette(member int) map[byte]color.RGBA {
	if member <= 0 {
		return variants[b.leader].palette()
	}
	// Followers cycle through every colouring except the leader's.
	i := (member - 1) % (len(variants) - 1)
	if i >= b.leader {
		i++
	}
	return variants[i].palette()
}

// LoadVariantID reads the sprite chosen with /sprite from PiG's login state
// (state/pig-standard/login.json under configHome). It returns "" when the
// file is missing or unreadable, which selects the default sprite.
func LoadVariantID(configHome string) string {
	file, err := os.Open(filepath.Join(configHome, "state", "pig-standard", "login.json"))
	if err != nil {
		return ""
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 4096))
	if err != nil {
		return ""
	}
	var saved struct {
		Variant string `json:"variant"`
	}
	if json.Unmarshal(data, &saved) != nil {
		return ""
	}
	return saved.Variant
}
