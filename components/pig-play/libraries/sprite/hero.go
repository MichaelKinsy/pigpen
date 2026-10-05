package sprite

import (
	"fmt"
	"image/color"
	"strings"
)

// Hero grid size and palette symbols. Each letter row has its own symbol so a
// logo ramp can shade the wordmark from top to bottom.
const (
	HeroWidth     = 32
	HeroHeight    = 14
	heroRamp      = "123456789ABC"
	heroShadow    = 'D'
	HeroPeriodKey = 'Q'
)

// Hero draws the "PiG." wordmark with a one-pixel drop shadow. It returns the
// pixel grid and the palette (symbol to "#RRGGBB") that colors it.
func Hero(logo Logo) ([]string, map[string]string) {
	pixels := make([][]byte, HeroHeight)
	for y := range pixels {
		pixels[y] = []byte(strings.Repeat(".", HeroWidth))
	}
	for _, glyph := range heroGlyphs {
		for y, row := range glyph.rows {
			for x := range len(row) {
				if row[x] == '1' {
					pixels[glyph.y+y+1][glyph.x+x+1] = heroShadow
				}
			}
		}
	}
	palette := map[string]string{string(heroShadow): RGBAHex(logo.Shadow)}
	for _, glyph := range heroGlyphs {
		for y, row := range glyph.rows {
			symbol := heroRamp[glyph.y+y]
			if glyph.period {
				symbol = HeroPeriodKey
			}
			for x := range len(row) {
				if row[x] == '1' {
					pixels[glyph.y+y][glyph.x+x] = symbol
					if glyph.period {
						palette[string(symbol)] = RGBAHex(logo.Period)
					} else {
						palette[string(symbol)] = RGBAHex(logo.Ramp[glyph.y+y])
					}
				}
			}
		}
	}
	hero := make([]string, len(pixels))
	for y, row := range pixels {
		hero[y] = string(row)
	}
	return hero, palette
}

// RGBAHex formats a color as "#RRGGBB" (alpha is dropped).
func RGBAHex(value color.RGBA) string {
	return fmt.Sprintf("#%02X%02X%02X", value.R, value.G, value.B)
}
