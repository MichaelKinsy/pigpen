package frontend

import (
	"slices"
	"strconv"
	"strings"

	fe "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/stencil-hq/tern-sdk/go/tern"
)

// oh-my-pig's brand mark and working spinner (Options.Omp only).
//
// Composition (Candidate 3, canonical 24×22 art-pixel grid, origin top-left;
// mirrored in extension package `brand` and verified equal by
// piglets/oh-my-pig/frontend/brand_sync_test.go):
//   - π bar: x 0..23, y 9..12 (4 rows), coloured with `light-dark(#1b1c20, #eedaad)`
//     so the π keeps omp's warm cream in dark themes and crisp dark ink in light themes.
//   - left leg: x 6..9, y 13..17 solid, y 18..20 checkerboard ((x+y)%2 == 0).
//   - right leg: x 14..17, y 13..21 solid.
//   - pig head (16×14 from frame.Mark, falling back to PiG's default green pig)
//     drawn LAST on top at grid origin (4, -1), so its empty top row falls off
//     the canvas, its ears start at y=0, and its bottom outline lands on y=11,
//     overlapping the middle of the bar while the bar shows left, right, and one
//     row below it.
//
// Welcome logo in Tern:
//   The `image[omp.welcome.logo]` node receives an inline SVG blob
//   (`image/svg+xml`) with viewBox `0 0 24 22`, where every π pixel carries
//   `class="omp-pi"` and every pig pixel carries its exact `#rrggbb` fill,
//   `shape-rendering="crispEdges"`. Because Tern mounts SVG blobs as real SVG
//   elements (`.sf-img-svg`), a dedicated program stylesheet (`omp-brand`) can:
//     - size `.sf-img-svg` to 48×44 CSS px (exact 2× integer scale of 24×22,
//       matching Tern's 52px lockup height without moving the wordmark/version;
//       Spine's 28px round node scales to 24×22 CSS px at 1×),
//     - colour `.omp-pi` with `fill: light-dark(#1b1c20, #eedaad)` so the same
//       SVG adapts live to dark and light themes without re-uploading a blob,
//     - preserve Tern's `omp-welcome-rise` arrival on `[data-role='omp.welcome.logo']`
//       while dropping the built-in π-only stroke-trace/rainbow-sweep animations,
//     - disable the zoom-in cursor on the logo (`pointer-events: none`) since
//       the built-in welcome mark is non-zoomable brand chrome.
//   When `frame.Mark` changes (e.g. `/sprite`), the frontend encodes the new
//   SVG blob and sends an `OpSet` patching the welcome logo's `blob` prop.
//
// Working spinner in Tern:
//   Inside `row[omp.working]`, the starburst spinner is replaced with a `row`
//   carrying `role="omp.working.pig"` (when not spinnerless). The `omp-brand`
//   program stylesheet draws the active pig head at 1 CSS px per art pixel
//   (16×14 inside a 16×16 box so the 26px working row's height never changes)
//   via `::before` `box-shadow` pixel art, animated with a crisp 2-step blink +
//   1px vertical hop (`@keyframes omp-pig-spin`, `steps(1)` — no rotation or
//   subpixel blur). Under `@media (prefers-reduced-motion: reduce)`, `.sf-still`,
//   `.sf-paused`, and `.sf-covered`, the animation stops on the still open-eye
//   head. Updating `frame.Mark` re-emits `omp-brand`, updating the spinner live.

const (
	ompBrandSheetName = "omp-brand"

	ompMarkCanvasWidth  = 24
	ompMarkCanvasHeight = 22
	ompMarkHeadWidth    = 16
	ompMarkHeadHeight   = 14
	ompMarkHeadOriginX  = 4
	ompMarkHeadOriginY  = -1

	// ompLogoWidthPx and ompLogoHeightPx are the CSS pixel size of the welcome
	// logo in Tern (exact 2× integer scale of the 24×22 art grid, fitting inside
	// Tern's 52px `.sf-row[data-role='omp.welcome.lockup']`).
	ompLogoWidthPx  = ompMarkCanvasWidth * 2  // 48 CSS px
	ompLogoHeightPx = ompMarkCanvasHeight * 2 // 44 CSS px

	ompPiToken = "pi"
)

// defaultOmpPigRows is PiG's default green mascot grid (pig-default).
var defaultOmpPigRows = [ompMarkHeadHeight]string{
	"................",
	"...OOOO..OOOO...",
	"...OeeO..OeeO...",
	"..OeePPPPPPeeO..",
	".OPPPpPPPPpPPPO.",
	".OPPWWPPPPWWPPO.",
	".OPPWKPPPPKWPPO.",
	".OPbbPssssPbbPO.",
	".OPPPsKssKsPPPO.",
	".OPPPPssssPPPPO.",
	".OPPPPPPPPPPPPO.",
	"..OPPPPPPPPPPO..",
	"...OOOOOOOOOO...",
	"................",
}

var defaultOmpPigPalette = map[byte]string{
	'O': "#18151d",
	'K': "#18151d",
	'P': "#48a381",
	'p': "#64be9e",
	's': "#32775e",
	'b': "#82dbba",
	'W': "#fefefe",
	'e': "#48a381",
}

// defaultOmpHead returns PiG's default green 16×14 pig head as a fe.Mark.
func defaultOmpHead() fe.Mark {
	pixels := make([]fe.MarkPixel, 0, 140)
	for y, row := range defaultOmpPigRows {
		for x := range len(row) {
			ch := row[x]
			if ch == '.' {
				continue
			}
			if hex, ok := defaultOmpPigPalette[ch]; ok {
				pixels = append(pixels, fe.MarkPixel{X: x, Y: y, Color: hex})
			}
		}
	}
	return fe.Mark{Width: ompMarkHeadWidth, Height: ompMarkHeadHeight, Pixels: pixels}
}

// effectiveOmpHead returns mark when it carries pixels, else PiG's default green head.
func effectiveOmpHead(mark fe.Mark) fe.Mark {
	if len(mark.Pixels) > 0 {
		return mark
	}
	return defaultOmpHead()
}

// composeOmpMark builds the 24×22 Candidate-3 π-plus-pig mashup from head.
// π pixels carry Color == ompPiToken (unless piColor != ""); head pixels keep
// their exact colour. Returned pixels are in row-major order; transparent cells
// are omitted.
func composeOmpMark(head fe.Mark, piColor string) []fe.MarkPixel {
	head = effectiveOmpHead(head)
	if piColor == "" {
		piColor = ompPiToken
	}

	var grid [ompMarkCanvasHeight][ompMarkCanvasWidth]string

	// π bar: x 0..23, y 9..12 (4 rows).
	for y := 9; y <= 12; y++ {
		for x := range ompMarkCanvasWidth {
			grid[y][x] = piColor
		}
	}
	// Left leg: x 6..9, y 13..17 solid, y 18..20 checkerboard ((x+y)%2 == 0).
	for y := 13; y <= 17; y++ {
		for x := 6; x <= 9; x++ {
			grid[y][x] = piColor
		}
	}
	for y := 18; y <= 20; y++ {
		for x := 6; x <= 9; x++ {
			if (x+y)%2 == 0 {
				grid[y][x] = piColor
			}
		}
	}
	// Right leg: x 14..17, y 13..21 solid.
	for y := 13; y <= 21; y++ {
		for x := 14; x <= 17; x++ {
			grid[y][x] = piColor
		}
	}
	// Pig head drawn last on top at (4, -1).
	for _, p := range head.Pixels {
		if p.Color == "" {
			continue
		}
		gx := ompMarkHeadOriginX + p.X
		gy := ompMarkHeadOriginY + p.Y
		if gx >= 0 && gx < ompMarkCanvasWidth && gy >= 0 && gy < ompMarkCanvasHeight {
			grid[gy][gx] = p.Color
		}
	}

	out := make([]fe.MarkPixel, 0, 280)
	for y := range ompMarkCanvasHeight {
		for x := range ompMarkCanvasWidth {
			if c := grid[y][x]; c != "" {
				out = append(out, fe.MarkPixel{X: x, Y: y, Color: c})
			}
		}
	}
	return out
}

// ompLogoSVG encodes the 24×22 mashup as a compact inline SVG whose π pixels
// carry class="omp-pi" (themed by omp-brand CSS via light-dark(#1b1c20, #eedaad))
// and whose horizontal same-colour runs are merged into single <rect> elements.
func ompLogoSVG(head fe.Mark) []byte {
	pixels := composeOmpMark(head, ompPiToken)
	var grid [ompMarkCanvasHeight][ompMarkCanvasWidth]string
	for _, p := range pixels {
		grid[p.Y][p.X] = p.Color
	}

	var b strings.Builder
	b.Grow(4096)
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="`)
	b.WriteString(strconv.Itoa(ompLogoWidthPx))
	b.WriteString(`" height="`)
	b.WriteString(strconv.Itoa(ompLogoHeightPx))
	b.WriteString(`" viewBox="0 0 `)
	b.WriteString(strconv.Itoa(ompMarkCanvasWidth))
	b.WriteByte(' ')
	b.WriteString(strconv.Itoa(ompMarkCanvasHeight))
	b.WriteString(`" shape-rendering="crispEdges">`)

	for y := range ompMarkCanvasHeight {
		for x := 0; x < ompMarkCanvasWidth; {
			c := grid[y][x]
			if c == "" {
				x++
				continue
			}
			w := 1
			for x+w < ompMarkCanvasWidth && grid[y][x+w] == c {
				w++
			}
			b.WriteString(`<rect x="`)
			b.WriteString(strconv.Itoa(x))
			b.WriteString(`" y="`)
			b.WriteString(strconv.Itoa(y))
			b.WriteString(`" width="`)
			b.WriteString(strconv.Itoa(w))
			b.WriteString(`" height="1" `)
			if c == ompPiToken {
				b.WriteString(`class="omp-pi" fill="#eedaad"`)
			} else {
				b.WriteString(`fill="`)
				b.WriteString(c)
				b.WriteByte('"')
			}
			b.WriteString(`/>`)
			x += w
		}
	}
	b.WriteString(`</svg>`)
	return []byte(b.String())
}

// blinkPigHead returns a blink frame of head by replacing the upper eye white
// pixels (rows 5-6 in standard 16×14 PiG heads) with the adjacent body colour
// (or outline for closed eyelids) so the pig visibly blinks on frame 2.
// If head has a non-standard eye layout (e.g. a tiny synthetic test mark),
// blinkPigHead still returns a valid head with the same pixel count so CSS
// box-shadow interpolation/step frames stay cleanly aligned.
func blinkPigHead(head fe.Mark) fe.Mark {
	head = effectiveOmpHead(head)
	// Build a lookup of (x, y) -> color and find the modal (body) color + outline color.
	at := make(map[[2]int]string, len(head.Pixels))
	counts := make(map[string]int, 8)
	for _, p := range head.Pixels {
		at[[2]int{p.X, p.Y}] = p.Color
		counts[p.Color]++
	}
	// Body color: most frequent pixel color (e.g. #48a381 on pig-default, #ffa8b7 on pink).
	bodyColor := ""
	bodyCount := 0
	for c, n := range counts {
		if n > bodyCount || (n == bodyCount && c < bodyColor) {
			bodyColor, bodyCount = c, n
		}
	}
	// Eye-lid dark color: take the pupil/outline at (5,6) or (10,6) if present, else #18151d.
	lidColor := at[[2]int{5, 6}]
	if lidColor == "" {
		lidColor = at[[2]int{1, 5}]
	}
	if lidColor == "" {
		lidColor = "#18151d"
	}

	// Standard PiG 16×14 head eyes occupy (4..5, 5..6) and (10..11, 5..6).
	// On the blink frame: row 5 becomes body colour (eyelid closed over top of eye)
	// and row 6 becomes a 2px dark closed-eye bar (lidColor).
	out := make([]fe.MarkPixel, len(head.Pixels))
	copy(out, head.Pixels)
	if head.Width == ompMarkHeadWidth && head.Height == ompMarkHeadHeight && bodyColor != "" {
		for i, p := range out {
			if (p.X == 4 || p.X == 5 || p.X == 10 || p.X == 11) && p.Y == 5 {
				out[i].Color = bodyColor
			} else if (p.X == 4 || p.X == 5 || p.X == 10 || p.X == 11) && p.Y == 6 {
				out[i].Color = lidColor
			}
		}
	}
	return fe.Mark{Width: head.Width, Height: head.Height, Pixels: out}
}

// writeBoxShadowPixels writes the comma-separated `box-shadow` list for pixels
// shifted by (dx, dy) CSS pixels (1 CSS px per art pixel).
func writeBoxShadowPixels(b *strings.Builder, pixels []fe.MarkPixel, dx, dy int) {
	for i, p := range pixels {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Itoa(p.X + dx))
		b.WriteString("px ")
		b.WriteString(strconv.Itoa(p.Y + dy))
		b.WriteString("px ")
		b.WriteString(p.Color)
	}
}

// ompBrandSheet builds the `omp-brand` program stylesheet used only when
// Options.Omp (ompRoles) is active:
//  1. Sizes `image[omp.welcome.logo] .sf-img-svg` to 48×44 CSS px (exact 2× of
//     the 24×22 art grid, fitting Tern's 52px lockup height without moving the
//     wordmark/version), styles `.omp-pi` via `light-dark(#1b1c20, #eedaad)` so
//     the π follows light and dark themes automatically, removes the zoom cursor
//     on the brand mark, and fits Spine's 28px circle at 1× (24×22).
//  2. Draws the active pig head as crisp 1× pixel art (16×14 inside a 16×16
//     inline box so the 26px working row's height stays unchanged) on
//     `[data-role='omp.working.pig']::before`, animated with a crisp stepped
//     3-phase blink + 1px hop (`omp-pig-spin`).
//  3. Freezes the spinner on the still open-eye head under
//     `@media (prefers-reduced-motion: reduce)`, `.sf-still`, `.sf-paused`, and
//     `.sf-covered`.
func ompBrandSheet(mark fe.Mark) string {
	open := effectiveOmpHead(mark)
	blink := blinkPigHead(open)

	var b strings.Builder
	b.Grow(4096 + len(open.Pixels)*64)

	// Welcome lockup & wordmark rules:
	// Tern's base sheet sets `[data-role='omp.welcome.lockup']{height:52px;overflow:hidden}`
	// and `[data-role='omp.welcome.wordmark']{font:600 40px/0.9 ...}` with `.sf-text.nowrap{overflow:hidden}`,
	// which clips descenders in `"oh-my-pig"` (`y`, `g`) and crowds `v0.1.0`.
	// Keep single-attribute specificity (0,1,0) on lockup (without min-height) so
	// `.sf-chat-spine [data-role='omp.welcome.lockup']{height:28px}` (0,2,0) wins
	// in Spine mode, while Reader expands to fit content without clipping.
	// On wordmark, `.sf-text.nowrap{overflow:hidden}` has specificity (0,2,0),
	// so `.sf-text[data-role='omp.welcome.wordmark']` matches (0,2,0) in the later
	// program sheet so Reader descenders are not clipped (Spine keeps its 28px lockup
	// centered via `.sf-chat-spine [data-role='omp.welcome.lockup']`).
	b.WriteString(`[data-role='omp.welcome.lockup']{height:auto;overflow:visible;align-items:center}`)
	b.WriteString(`.sf-text[data-role='omp.welcome.wordmark']{line-height:1.15;overflow:visible}`)
	// Welcome logo rules (SVG blob in image[omp.welcome.logo]).
	b.WriteString(`[data-role='omp.welcome.logo']{cursor:default;pointer-events:none;margin-top:2px}`)
	b.WriteString(`[data-role='omp.welcome.logo'] .sf-img-svg{width:`)
	b.WriteString(strconv.Itoa(ompLogoWidthPx))
	b.WriteString(`px !important;height:`)
	b.WriteString(strconv.Itoa(ompLogoHeightPx))
	b.WriteString(`px !important}`)
	b.WriteString(`[data-role='omp.welcome.logo'] .omp-pi{fill:light-dark(#1b1c20,#eedaad)}`)
	// Working spinner rules (16×14 pig head at 1 CSS px per art pixel inside a 16×16 box).
	// The 1×1 ::before element sits at (-1px, -1px) inside the 16×16 box so shadow
	// offsets (x+1, y+2) place the resting 16×14 head at y=1..14, and a 1px upward
	// hop (y+1) lands at y=0..13 — every pixel strictly inside the 16×16 box and
	// on whole CSS pixels with zero clipping or blur.
	b.WriteString(`[data-role='omp.working.pig']{display:inline-block;position:relative;flex:none;width:16px;height:16px;overflow:visible}`)
	b.WriteString(`[data-role='omp.working.pig']::before{content:"";display:block;position:absolute;left:-1px;top:-1px;width:1px;height:1px;box-shadow:`)
	writeBoxShadowPixels(&b, open.Pixels, 1, 2)
	b.WriteString(`;animation:omp-pig-spin 1.2s steps(1,end) infinite}`)

	// Keyframes: 0-50% resting open eyes, 50-75% 1px hop open eyes, 75-100% resting blink.
	b.WriteString(`@keyframes omp-pig-spin{0%,49%{box-shadow:`)
	writeBoxShadowPixels(&b, open.Pixels, 1, 2)
	b.WriteString(`}50%,74%{box-shadow:`)
	writeBoxShadowPixels(&b, open.Pixels, 1, 1)
	b.WriteString(`}75%,100%{box-shadow:`)
	writeBoxShadowPixels(&b, blink.Pixels, 1, 2)
	b.WriteString(`}}`)

	// Reduced motion: hold still on open-eye head.
	// (Tern's own base sheet handles `.sf-still`, `.sf-paused`, and `.sf-covered`
	// on region ancestors, which scoped program sheets cannot match.)
	b.WriteString(`@media (prefers-reduced-motion: reduce){[data-role='omp.working.pig']::before{animation:none}}`)

	return b.String()
}

// ompLogoBlob queues the current mashup SVG blob for Tern and returns its blob ID.
func (s *session) ompLogoBlob() string {
	svg := ompLogoSVG(s.mark)
	img := &fe.ViewImage{Data: svg, MimeType: "image/svg+xml"}
	return s.blob(img)
}

// flushOmpBrandSheet sends or updates the `omp-brand` stylesheet when ompRoles
// is active and the sheet has not yet been sent or `s.mark` changed.
func (s *session) flushOmpBrandSheet() error {
	if !s.ompRoles || (!s.ompBrandDirty && s.ompBrandSheetSent) {
		return nil
	}
	css := ompBrandSheet(s.mark)
	if s.ompBrandSheetSent && css == s.ompBrandSheetText {
		s.ompBrandDirty = false
		return nil
	}
	if err := s.sf.Stylesheet(ompBrandSheetName, css); err != nil {
		return err
	}
	s.ompBrandSheetSent = true
	s.ompBrandSheetText = css
	s.ompBrandDirty = false
	return nil
}

// refreshOmpWelcomeLogo updates the live welcome logo node's blob prop whenever
// `frame.Mark` changes (e.g. after `/sprite`).
func (s *session) refreshOmpWelcomeLogo() {
	if !s.ompRoles || s.welcomeLogoNodeID == "" {
		return
	}
	// Only patch if the welcome node is still live in the transcript.
	if e := s.sent[s.welcomeEntryID]; e == nil || e.gone {
		return
	}
	blobID := s.ompLogoBlob()
	if blobID == s.welcomeLogoBlob {
		return
	}
	s.welcomeLogoBlob = blobID
	s.queue(tern.OpSet(s.welcomeLogoNodeID, map[string]any{"blob": blobID}))
}

// DefaultOmpHeadForTest returns the frontend's default 16×14 green pig head so
// oh-my-pig/frontend tests can assert parity with extension/brand.
func DefaultOmpHeadForTest() fe.Mark { return defaultOmpHead() }

// ComposeOmpMarkForTest returns the frontend's composed 24×22 Candidate-3 mark
// so oh-my-pig/frontend tests can assert parity with extension/brand.
func ComposeOmpMarkForTest(head fe.Mark, piColor string) []fe.MarkPixel {
	return composeOmpMark(head, piColor)
}

// sameOmpPixels reports whether two pixel slices match.
func sameOmpPixels(a, b []fe.MarkPixel) bool {
	return slices.Equal(a, b)
}
