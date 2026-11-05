package main

import (
	"fmt"
	"image"
)

// ascii draws a 16x16 icon from rows of palette characters ('.' = transparent).
func ascii(rows []string, pal map[byte]string) *image.NRGBA {
	h := len(rows)
	w := len(rows[0])
	m := newImg(w, h)
	for y, r := range rows {
		if len(r) != w {
			panic(fmt.Sprintf("icon row %d has width %d, want %d", y, len(r), w))
		}
		for x := 0; x < w; x++ {
			if r[x] == '.' {
				continue
			}
			c, ok := pal[r[x]]
			if !ok {
				panic(fmt.Sprintf("icon: no colour for %q", r[x]))
			}
			px(m, x, y, c)
		}
	}
	return m
}

func hotbarPen() *image.NRGBA {
	pal := map[byte]string{
		'W': "#c58a4a", 'D': "#7a4e24", 'L': "#e0b06e", 'g': "#2f6b45",
		'P': "#f29ab0", 'p': "#c46a85", 'K': "#111111", 'N': "#8a3d57",
	}
	// a fenced square seen from the front: back rail, side rails, two front rails
	return ascii([]string{
		"................",
		"................",
		"..LD........LD..",
		"..DDWWWWWWWWDD..",
		"..D..gggggg..D..",
		"..W.gggggggg.W..",
		"..D.ggpggpgg.D..",
		"..W.ggPPPPgg.W..",
		"..D.ggPKKPgg.D..",
		"..W.ggPNNPgg.W..",
		"..D..gggggg..D..",
		"..LD...LD...LD..",
		"..DDWWWDDWWWDD..",
		"..DD...DD...DD..",
		"..DDWWWDDWWWDD..",
		"..DD...DD...DD..",
	}, pal)
}

var pigPal = map[byte]string{
	'D': "#c46a85", 'E': "#d98fa4", 'F': "#f29ab0", 'S': "#f8c4d2",
	'N': "#8a3d57", 'K': "#111111", 'W': "#ffffff",
}

func hotbarPig() *image.NRGBA {
	return ascii([]string{
		"................",
		"..DDD......DDD..",
		".DEEDDDDDDDDEED.",
		".DEEFFFFFFFFEED.",
		".DFFFFFFFFFFFFD.",
		".DFFWKFFFFWKFFD.",
		".DFFKKFFFFKKFFD.",
		".DFFFSSSSSSFFFD.",
		".DFFSSSSSSSSFFD.",
		".DFFSSNSSNSSFFD.",
		".DFFSSSSSSSSFFD.",
		"..DFFSSSSSSFFD..",
		"...DDDDDDDDDD...",
		"................",
		"................",
		"................",
	}, pigPal)
}

func hotbarRunt() *image.NRGBA {
	pal := map[byte]string{
		'O': hardC, 'L': hardL, 'B': hardD,
		'E': earC, 'F': faceC, 'S': snoutC, 'N': nostrilC, 'K': "#111111", 'W': "#ffffff",
		'D': "#c46a85",
	}
	return ascii([]string{
		"................",
		".....OOOOOO.....",
		"....OLLLOOOO....",
		"...OLLLOOOOOO...",
		"..BBBBBBBBBBBB..",
		"..DFFFFFFFFFFD..",
		"..EFWKFFFFWKFE..",
		"..EFKKFFFFKKFE..",
		"...FFFSSSSFFF...",
		"...FFSSSSSSFF...",
		"...FFSSNNSSFF...",
		"....FFSSSSFF....",
		".....DDDDDD.....",
		"................",
		"................",
		"................",
	}, pal)
}

func hotbarBelt() *image.NRGBA {
	pal := map[byte]string{
		'K': "#1c2024", 'G': "#4a5058", 'L': "#6b737c", 'A': "#f3d36b",
	}
	return ascii([]string{
		"................",
		"................",
		"................",
		"................",
		".KKKKKKKKKKKKKK.",
		".KLLLLLLLLLLLLK.",
		".KGAGGGGAGGGGGK.",
		".KGGAGGGGAGGGGK.",
		".KGGGAGGGGAGGGK.",
		".KGGAGGGGAGGGGK.",
		".KGAGGGGAGGGGGK.",
		".KLLLLLLLLLLLLK.",
		".KKKKKKKKKKKKKK.",
		"................",
		"................",
		"................",
	}, pal)
}

func hotbarOven() *image.NRGBA {
	pal := map[byte]string{
		'D': "#2e1510", 'B': "#5a2a1a", 'L': "#74382a", 'H': "#1a0b08",
		'R': "#e0561f", 'O': "#ffb347", 'Y': "#ffd27a",
	}
	return ascii([]string{
		"................",
		"..........DDDD..",
		"..........DBBD..",
		"..........DLBD..",
		"..DDDDDDDDDBBDD.",
		"..DLLLLLLLLLLLD.",
		"..DBBBBBBBBBBBD.",
		"..DDDDHHHHDDDDD.",
		"..DBBHHHHHHBBBD.",
		"..DBBHRRRRHBBBD.",
		"..DDDHROOORHDDD.",
		"..DBBHROYYORHBD.",
		"..DBBHROYYORHBD.",
		"..DDDDDDDDDDDDD.",
		"................",
		"................",
	}, pal)
}

func hotbarReview() *image.NRGBA {
	pal := map[byte]string{
		'O': "#10202f", 'B': "#1f3d5a", 'L': "#2d5780", 'P': "#eef3f7",
		'I': "#9ec9ee", 'G': "#4ed1a1", 'R': "#e05a4f",
	}
	return ascii([]string{
		"................",
		"....OOOOOOOOO...",
		"....OPPPPPPPO...",
		"....OPIIIIIPO...",
		"....OPPPPPPPO...",
		"....OPIIIIPPO...",
		"....OPPPPPPPO...",
		"....OPIIIIIPO...",
		"....OPGGGPRRO...",
		"....OPPPPRRRO...",
		"....OOOOOOROO...",
		"......OBBBO.....",
		"...OOOOBBBOOOO..",
		"...OBBBLLLBBBO..",
		"...OOOOOOOOOOO..",
		"................",
	}, pal)
}

func hotbarBarn() *image.NRGBA {
	pal := map[byte]string{
		'O': "#4f1a17", 'W': "#8f2f2a", 'V': "#a63a34", 'R': "#5e3f22",
		'T': "#774f2b", 'C': "#f3e3c3", 'D': "#6f2420", 'H': "#2a0f0d",
	}
	return ascii([]string{
		"................",
		"......RRRR......",
		".....RTTTTR.....",
		"....RTTHHTTR....",
		"...RTTTHHTTTR...",
		"..RTTTTTTTTTTR..",
		".ROOOOOOOOOOOOR.",
		"..OVVVVVVVVVVO..",
		"..OWWCCCCCCWWO..",
		"..OWCDCDDCDCWO..",
		"..OWCCDCCDCCWO..",
		"..OWCDCDDCDCWO..",
		"..OWCCCCCCCCWO..",
		"..OOOOOOOOOOOO..",
		"................",
		"................",
	}, pal)
}

func hotbarJobboard() *image.NRGBA {
	pal := map[byte]string{
		'P': "#5e3f22", 'W': "#8b5a2b", 'L': "#a9743a",
		'C': "#f3e3c3", 'I': "#a89570", 'R': "#e05a4f", 'G': "#4ed1a1", 'Y': "#e3a63b",
	}
	return ascii([]string{
		"................",
		"................",
		".PPPPPPPPPPPPPP.",
		".PLLLLLLLLLLLLP.",
		".PWRCWWGCWWYCWP.",
		".PWCCWWCCWWCCWP.",
		".PWCIWWCIWWCIWP.",
		".PWCCWWCCWWCCWP.",
		".PWCIWWWWWWCIWP.",
		".PWWWWWWWWWWWWP.",
		".PPPPPPPPPPPPPP.",
		"...PP......PP...",
		"...PW......PW...",
		"...PP......PP...",
		"................",
		"................",
	}, pal)
}

func hotbarRemove() *image.NRGBA {
	pal := map[byte]string{
		'S': "#c9d3da", 'Z': "#8794a0", 'R': "#e05a4f", 'r': "#a8352c", 'K': "#5e1c17",
	}
	return ascii([]string{
		"................",
		"......SSSSS.....",
		"....SSSZZZSS....",
		"...SSZ....ZSS...",
		"..SSZ......ZSS..",
		"..SZ.....Rr.ZS..",
		"..Z.....Rr...Z..",
		".......Rr.......",
		"......Rr........",
		".....Rr.........",
		"....Rr..........",
		"...Rr...........",
		"..Rr............",
		".KRr............",
		".KK.............",
		"................",
	}, pal)
}

func hotbarHand() *image.NRGBA {
	pal := map[byte]string{
		'K': "#7a5240", 'H': "#f1d6bd", 'S': "#d4a98a",
	}
	return ascii([]string{
		"................",
		"......KK.KK.....",
		".....KHHKHHK....",
		"..KK.KHHKHHKKK..",
		".KHHKKHHKHHKHHK.",
		".KHHKKHHKHHKHHK.",
		".KHHHKHHHHHKHHK.",
		"..KHHHHHHHHHHHK.",
		"..KKHHHHHHHHHSK.",
		"...KHHHHHHHHHSK.",
		"...KHHHHHHHHSK..",
		"....KSHHHHHSSK..",
		".....KSHHHSSK...",
		".....KKKKKKK....",
		"................",
		"................",
	}, pal)
}

// slot: 20x20 rounded frame in the storyboard chrome colours.
func hotbarSlot(on bool) *image.NRGBA {
	m := newImg(20, 20)
	bg, bd := "#1a2620", "#2b3d33"
	if on {
		bg, bd = "#2d4439", "#4ed1a1"
	}
	rect(m, 0, 0, 20, 20, bd)
	rect(m, 1, 1, 18, 18, bg)
	if on {
		rect(m, 1, 1, 18, 1, "#4a6a5a") // soft inner highlight
		rect(m, 1, 1, 1, 18, "#3a5a4a")
	}
	for _, c := range [][2]int{{0, 0}, {19, 0}, {0, 19}, {19, 19}} {
		px(m, c[0], c[1], "#00000000")
	}
	return m
}

// cursor: tile corner brackets, 16x16, transparent middle.
func cursor(c string) *image.NRGBA {
	m := newImg(16, 16)
	for _, x0 := range []int{0, 12} {
		for _, y0 := range []int{0, 12} {
			// 4x4 corner bracket, 2 px thick
			xo, yo := x0, y0
			rect(m, xo, yo, 4, 4, c)
			rect(m, xo+boolInt(x0 == 0)*2, yo+boolInt(y0 == 0)*2, 2, 2, "#00000000")
		}
	}
	return m
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// beltArrow: white 8x8 chevron; dir is where it points.
func beltArrow(dir string) *image.NRGBA {
	m := newImg(8, 8)
	for y, x := range []int{2, 3, 4, 5, 5, 4, 3, 2} {
		rect(m, x, y, 2, 1, "#ffffff")
	}
	turns := map[string]int{"right": 0, "down": 1, "left": 2, "up": 3}[dir]
	for i := 0; i < turns; i++ {
		m = rotCW(m)
	}
	return m
}

// rotCW rotates a square image 90 degrees clockwise.
func rotCW(m *image.NRGBA) *image.NRGBA {
	n := m.Rect.Dx()
	o := newImg(n, n)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			o.SetNRGBA(n-1-y, x, m.NRGBAAt(x, y))
		}
	}
	return o
}
