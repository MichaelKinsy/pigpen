// Package scene draws Pig Snake: the board, the herd, the apple, the HUD and
// the title, result and too-small screens.
//
// The look follows PiG Standard's arcade games (PiG d86eb93, MIT, Michael
// Kinsy; see CREDITS.md): a half-block pixel scene over a night palette,
// a two-line text HUD under it, and 3x5 pixel-font cards. The pigs come from a
// [sprites.Source]; nothing here knows where the art lives.
package scene

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/pigpen/pig-snake/internal/pixel"
	"github.com/MichaelKinsy/pigpen/pig-snake/internal/snake"
	"github.com/MichaelKinsy/pigpen/pig-snake/internal/sprites"
)

const (
	// HUDLines is the number of text lines under the playfield.
	HUDLines = 2
	// MinCols and MinRows are the smallest playable board.
	MinCols = 8
	MinRows = 6
	// comfortCols and comfortRows are what a head size must give before the
	// renderer prefers it over a smaller one.
	comfortCols = 10
	comfortRows = 6
	// MaxCols and MaxRows cap the board so a huge terminal stays playable.
	MaxCols = 40
	MaxRows = 22
)

// Palette, from the PiG Runner night look (arcade.go at d86eb93).
var (
	roadDark  = color.RGBA{0x10, 0x12, 0x1A, 0xFF}
	accent    = color.RGBA{0x01, 0xA9, 0x82, 0xFF}
	dimStar   = color.RGBA{0x9A, 0x96, 0xB8, 0xFF}
	cardText  = color.RGBA{0xE8, 0xF4, 0xF0, 0xFF}
	fieldA    = color.RGBA{0x1F, 0x3A, 0x2B, 0xFF}
	fieldB    = color.RGBA{0x1B, 0x33, 0x27, 0xFF}
	crashTint = color.RGBA{0xF0, 0x30, 0x30, 0xFF}
)

// GridFor picks the board for a new game and the head size that draws it. A
// larger head is preferred while it leaves a comfortable board (10x6 cells);
// the smallest head accepts the minimum board (8x6). ok is false when even
// that does not fit. sizes must be largest first.
func GridFor(sizes []int, viewW, pixelH int) (cols, rows, size int, ok bool) {
	for i, s := range sizes {
		if s <= 0 {
			continue
		}
		cols, rows = min(max(viewW, 0)/s, MaxCols), min(max(pixelH, 0)/s, MaxRows)
		minC, minR := comfortCols, comfortRows
		if i == len(sizes)-1 {
			minC, minR = MinCols, MinRows
		}
		if cols >= minC && rows >= minR {
			return cols, rows, s, true
		}
	}
	return 0, 0, 0, false
}

// Fit picks the largest head size at which a cols x rows board fits the view.
func Fit(sizes []int, cols, rows, viewW, pixelH int) (size int, ok bool) {
	for _, s := range sizes {
		if s > 0 && cols*s <= viewW && rows*s <= pixelH {
			return s, true
		}
	}
	return 0, false
}

// MinView is the smallest content area (cells) Pig Snake can be played in:
// the minimum board at the smallest head, plus the HUD.
func MinView(sizes []int) (w, h int) {
	return BoardView(sizes, MinCols, MinRows)
}

// BoardView is the smallest content area (cells) that shows a cols x rows
// board: the board at the smallest head, plus the HUD.
func BoardView(sizes []int, cols, rows int) (w, h int) {
	small := 0
	for _, s := range sizes {
		if s > 0 && (small == 0 || s < small) {
			small = s
		}
	}
	return cols * small, (rows*small+1)/2 + HUDLines
}

// TooSmall is the message shown when not even the minimum board fits. Sizes
// are given as terminal sizes (the content area plus the overlay's one-cell
// box).
func TooSmall(viewW, viewH int, sizes []int) []string {
	w, h := MinView(sizes)
	return tooSmall(viewW, viewH, w, h)
}

// tooSmall names the content area needW x needH as the terminal size needed.
func tooSmall(viewW, viewH, needW, needH int) []string {
	return []string{
		"PIG SNAKE",
		"terminal too small",
		fmt.Sprintf("needs %dx%d, have %dx%d", needW+2, needH+2, viewW+2, viewH+2),
		"enlarge the window",
		"q quits",
	}
}

// BoardOrigin is the top-left pixel of a board centered on the canvas.
func BoardOrigin(canvasW, canvasH, cols, rows, size int) (x, y int) {
	return (canvasW - cols*size) / 2, (canvasH - rows*size) / 2
}

// Buffers are the reusable buffers of one view.
type Buffers struct {
	canvas  pixel.Canvas
	encoder pixel.Encoder
	pals    palettes
	hud     []string
	hudKey  hudKey
}

type hudKey struct {
	score, high, herd, width int
	mode                     snake.Mode
	status                   snake.Status
	cause                    snake.Death
	trueColor                bool
}

// palettes caches the pixel palettes of one source.
type palettes struct {
	src  sprites.Source
	pals []*pixel.Palette
}

func (p *palettes) member(src sprites.Source, member int) *pixel.Palette {
	if p.src != src {
		p.src, p.pals = src, p.pals[:0]
	}
	for len(p.pals) <= member {
		p.pals = append(p.pals, nil)
	}
	if p.pals[member] == nil {
		p.pals[member] = pixel.NewPalette(src.Palette(member))
	}
	return p.pals[member]
}

var applePal = pixel.NewPalette(map[byte]color.RGBA{
	'R': {0xE0, 0x3A, 0x3E, 0xFF}, 'r': {0xFF, 0x8A, 0x80, 0xFF}, 'D': {0xA8, 0x22, 0x2E, 0xFF},
	'g': {0x5E, 0xD0, 0x6B, 0xFF}, 's': {0x6B, 0x42, 0x22, 0xFF},
})

// Apple art at the head sizes Pig Snake draws. Original to Pig Snake.
var (
	apple8 = []string{"...s.g..", "...ssgg.", ".RRRRRR.", "RRrRRRRR", "RrRRRRRD", "RRRRRRRD", ".RRRRRD.", "..DDDD.."}
	apple6 = []string{"..sg..", ".RRRR.", "RrRRRD", "RRRRRD", ".RRRD.", "..DD.."}
	apple4 = []string{".sg.", "RrRR", "RRRD", ".DD."}
)

func appleFor(size int) []string {
	switch {
	case size >= 8:
		return apple8
	case size >= 6:
		return apple6
	}
	return apple4
}

// Draw paints the playfield onto an already-sized canvas: the board centered on
// it, the apple, the herd and, for a finished or waiting game, its card.
func Draw(c *pixel.Canvas, g *snake.Game, src sprites.Source, size int) {
	var p palettes
	draw(c, g, src, size, &p)
}

func draw(c *pixel.Canvas, g *snake.Game, src sprites.Source, size int, p *palettes) {
	c.Rect(0, 0, c.W, c.H, roadDark)
	ox, oy := BoardOrigin(c.W, c.H, g.W, g.H, size)
	frame := accent
	if g.Mode == snake.Wrap {
		frame = dimStar // no solid wall in wrap mode
	}
	c.Rect(ox-1, oy-1, g.W*size+2, g.H*size+2, frame)
	for y := range g.H {
		for x := range g.W {
			tile := fieldA
			if (x+y)%2 == 1 {
				tile = fieldB
			}
			c.Rect(ox+x*size, oy+y*size, size, size, tile)
		}
	}
	if g.Food.X >= 0 {
		art := appleFor(size)
		off := (size - len(art)) / 2
		c.Blit(ox+g.Food.X*size+off, oy+g.Food.Y*size+off, art, applePal)
	}
	head := src.Head(size)
	for member := len(g.Body) - 1; member >= 0; member-- {
		pos := g.Body[member]
		pal := p.member(src, member)
		if member == 0 && g.Status == snake.Over {
			pal = crashed(pal)
		}
		c.Blit(ox+pos.X*size, oy+pos.Y*size, head, pal)
	}
	switch g.Status {
	case snake.Waiting:
		card(c, ox, oy, g, size, "PIG SNAKE", "SPACE START")
	case snake.Paused:
		card(c, ox, oy, g, size, "PAUSED", "P RESUME")
	case snake.Over:
		card(c, ox, oy, g, size, "GAME OVER", "R RETRY")
	case snake.Won:
		card(c, ox, oy, g, size, "HERD FULL", "R RETRY")
	}
}

// crashed returns a copy of pal tinted red: the leader that hit something.
func crashed(pal *pixel.Palette) *pixel.Palette {
	var out pixel.Palette
	for i, col := range pal {
		if col.A != 0 {
			out[i] = pixel.Mix(col, crashTint, 110)
		}
	}
	return &out
}

// card draws a title or result card centered on the board: a dimmed plate, the
// title at twice the font size (or once when narrow) and the subtitle. It
// draws nothing when the canvas is too small to hold it legibly.
func card(c *pixel.Canvas, ox, oy int, g *snake.Game, size int, title, subtitle string) {
	bw, bh := g.W*size, g.H*size
	scale := 2
	if pixel.TextWidth(title)*scale+6 > c.W {
		scale = 1
	}
	titleW := pixel.TextWidth(title) * scale
	height := pixel.GlyphHeight*scale + 4 + pixel.GlyphHeight
	if titleW+6 > c.W || height+4 > bh {
		return
	}
	cx, top := ox+bw/2, oy+(bh-height)/2
	x0, y0 := cx-titleW/2-3, top-2
	for y := y0; y < y0+height+4; y++ {
		for x := x0; x < x0+titleW+6; x++ {
			if x >= 0 && x < c.W && y >= 0 && y < c.H {
				c.Set(x, y, pixel.Mix(c.At(x, y), roadDark, 200))
			}
		}
	}
	c.DrawTextScaled(cx-titleW/2, top, title, scale, accent, roadDark)
	if w := pixel.TextWidth(subtitle); w+2 <= c.W {
		c.DrawText(cx-w/2, top+pixel.GlyphHeight*scale+4, subtitle, cardText, roadDark)
	}
}

// ANSI styles of the HUD, as in the arcade games.
const (
	reset  = "\x1b[0m"
	grey   = "\x1b[90m"
	red    = "\x1b[91;1m"
	yellow = "\x1b[93;1m"
)

func fg(c color.RGBA, trueColor bool) string {
	if trueColor {
		return "\x1b[38;2;" + strconv.Itoa(int(c.R)) + ";" + strconv.Itoa(int(c.G)) + ";" + strconv.Itoa(int(c.B)) + "m"
	}
	return "\x1b[38;5;" + strconv.Itoa(int(pixel.RGBTo256(c))) + "m"
}

func pad4(n int) string {
	s := strconv.Itoa(n)
	for len(s) < 4 {
		s = "0" + s
	}
	return s
}

// HUD returns the two text lines under the playfield, exactly width cells wide:
// title, score, herd size, high score, mode and hints; then a status line.
func HUD(g *snake.Game, width int, trueColor bool) []string {
	ink := fg(accent, trueColor)
	scoreInk := ink
	switch g.Status {
	case snake.Over:
		scoreInk = red
	case snake.Paused:
		scoreInk = yellow
	}
	first := " " + ink + "PIG SNAKE" + reset + "  " + scoreInk + "score " + pad4(g.Score) + reset +
		"  herd " + strconv.Itoa(g.Herd()) + "  high " + pad4(g.High) + "  " + g.Mode.String() +
		"  " + grey + "wasd/arrows steer  p pause  q quit" + reset
	var status string
	switch g.Status {
	case snake.Waiting:
		status = " " + grey + "space start  m mode (walls or wrap)  q quit" + reset
	case snake.Paused:
		status = " " + yellow + "PAUSED" + reset + "  " + grey + "p resume" + reset
	case snake.Won:
		status = " " + yellow + "HERD COMPLETE" + reset + "  " + grey + "r retry  q quit" + reset
	case snake.Over:
		what := "the wall"
		if g.Cause == snake.HitHerd {
			what = "the herd"
		}
		status = " " + red + "CRASHED into " + what + reset + "  " + grey + "r retry  q quit" + reset
	default:
		status = " " + grey + "every apple adds a pig to the herd" + reset
	}
	return []string{pixel.FitLine(first, width), pixel.FitLine(status, width)}
}

// Render fills a viewW x viewH content area with exactly viewH lines of
// exactly viewW cells: the playfield and HUD, or the too-small message when
// the game's board does not fit at any head size.
func Render(b *Buffers, g *snake.Game, src sprites.Source, viewW, viewH int, dst []string) []string {
	viewW, viewH = max(viewW, 1), max(viewH, 1)
	pixelH := (viewH - HUDLines) * 2
	size, ok := Fit(src.Sizes(), g.W, g.H, viewW, pixelH)
	if !ok {
		// A waiting game takes the board of the terminal it is shown in; a game
		// under way keeps its board, so the hint names what that board needs.
		needW, needH := MinView(src.Sizes())
		if g.Status != snake.Waiting {
			needW, needH = BoardView(src.Sizes(), g.W, g.H)
		}
		return message(tooSmall(viewW, viewH, needW, needH), viewW, viewH, dst)
	}
	c := &b.canvas
	c.Resize(viewW, pixelH)
	draw(c, g, src, size, &b.pals)
	b.encoder.TrueColor = pixel.SupportsTrueColor()
	dst = b.encoder.Encode(c, dst)
	key := hudKey{g.Score, g.High, g.Herd(), viewW, g.Mode, g.Status, g.Cause, b.encoder.TrueColor}
	if b.hud == nil || key != b.hudKey {
		b.hud, b.hudKey = HUD(g, viewW, key.trueColor), key
	}
	return append(dst, b.hud...)
}

// message centers text lines in a viewW x viewH area, every line exactly viewW
// cells wide.
func message(lines []string, viewW, viewH int, dst []string) []string {
	top := max((viewH-len(lines))/2, 0)
	for y := range viewH {
		text := ""
		if i := y - top; i >= 0 && i < len(lines) {
			text = lines[i]
			if n := utf8.RuneCountInString(text); n < viewW {
				text = strings.Repeat(" ", (viewW-n)/2) + text
			}
		}
		dst = append(dst, pixel.FitLine(text, viewW))
	}
	return dst
}
