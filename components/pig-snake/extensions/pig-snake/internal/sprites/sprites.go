// Package sprites is the one seam between Pig Snake and the pig art.
//
// The game and its renderer draw pigs only through [Source]. [Shared] is the
// source the game uses: the colourings, the catalogue order and the sprite the
// player chose with /sprite come from the shared sprite library of the pig-play
// Package (libraries/sprite, adapted from PiG d86eb93, MIT, Michael Kinsy; see
// CREDITS.md); the heads below are Pig Snake's own drawings of that pig at three
// sizes. Nothing outside this package knows where the art lives.
package sprites

import (
	"image/color"

	"github.com/MichaelKinsy/pigpen/components/pig-play/libraries/sprite"
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

// Hatted is implemented by a Source whose leader wears something over its head,
// as the Sheriff does. The renderer draws Hat(size) over the top rows of the
// leader's head, in the leader's palette; a nil result is no hat.
type Hatted interface {
	Hat(size int) []string
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

// The Sheriff's hat, drawn over the top rows of the leader's head with the
// colours the shared Sheriff variant gives H (crown), h (shade) and S (band).
// Pig Snake's own small drawings, one per head size.
var (
	hat8 = []string{
		"..HHHH..",
		".HhSShH.",
		"HHHHHHHH",
	}
	hat6 = []string{
		".HHHH.",
		"HHSSHH",
	}
	// At four pixels the second row is the eyes: the hat is one row.
	hat4 = []string{
		"HHHH",
	}
)

// sheriffID names the catalogue variant that wears the hat.
const sheriffID = "sheriff"

type shared struct {
	leader int // index into sprite.Variants
}

// Shared returns the source backed by the shared sprite library. The leader
// wears the sprite variantID names (an unknown or empty id gives the default
// green pig); followers are the other colourings in catalogue order.
func Shared(variantID string) Source {
	want := sprite.FindVariant(variantID)
	for i, v := range sprite.Variants {
		if v.ID == want.ID {
			return &shared{leader: i}
		}
	}
	return &shared{}
}

func (*shared) Sizes() []int { return []int{8, 6, 4} }

func (*shared) Head(size int) []string {
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

func (s *shared) Palette(member int) map[byte]color.RGBA {
	n := len(sprite.Variants)
	if member <= 0 {
		return sprite.MascotPalette(sprite.Variants[s.leader])
	}
	// Followers cycle through every colouring except the leader's.
	i := (member - 1) % (n - 1)
	if i >= s.leader {
		i++
	}
	return sprite.MascotPalette(sprite.Variants[i])
}

// Hat implements Hatted: only the Sheriff wears one.
func (s *shared) Hat(size int) []string {
	if sprite.Variants[s.leader].ID != sheriffID {
		return nil
	}
	switch size {
	case 8:
		return hat8
	case 6:
		return hat6
	case 4:
		return hat4
	}
	return nil
}

// LoadVariantID is the id of the sprite chosen with /sprite, read by the shared
// library from PiG's login state (state/pig-standard/login.json under
// configHome). A missing, unreadable or unknown choice is the default sprite.
func LoadVariantID(configHome string) string { return sprite.ActiveVariant(configHome).ID }
