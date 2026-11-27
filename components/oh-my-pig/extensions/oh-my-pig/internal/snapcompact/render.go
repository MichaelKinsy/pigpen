package snapcompact

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
)

// upstream: crates/natives/src/snapcompact.rs

const (
	maxFrameSize = 16384
	docGutter    = 3

	dimOnRune     rune = 0x000e
	dimOffRune    rune = 0x000f
	fullBlockRune rune = 0x2588

	// DimOn / DimOff are zero-width ink toggles embedded in normalized text.
	DimOn  = "\u000e"
	DimOff = "\u000f"
	// NewlineGlyph fills one cell with solid black ink in place of newline runs.
	NewlineGlyph = "\u2588"
)

const (
	inkColors = 6
	inkBlack  = 7
	bgRepeat  = 8
	inkDim    = 9
)

// Indexed palette matching crates/natives/src/snapcompact.rs PALETTE verbatim.
var snapPalette = [10][3]uint8{
	{255, 255, 255}, // 0: white background
	{109, 2, 2},     // 1: red
	{109, 53, 2},    // 2: amber
	{24, 109, 2},    // 3: green
	{2, 109, 109},   // 4: teal
	{2, 32, 109},    // 5: blue
	{75, 2, 109},    // 6: violet
	{0, 0, 0},       // 7: bw ink
	{255, 247, 194}, // 8: repeat highlight band
	{128, 128, 128}, // 9: dim ink (tool-output spans / stopwords)
}

type gridSpec struct {
	cols   int
	rows   int
	repeat int
	cellW  int
	cellH  int
}

type laidCell struct {
	ch    rune
	ink   uint8
	x     int
	row   int
	units int
}

// isWideCodePoint mirrors crates/natives/src/snapcompact.rs is_wide and
// packages/snapcompact/src/snapcompact.ts isWideCodePoint.
func isWideCodePoint(cp rune) bool {
	return (cp >= 0x1100 && cp <= 0x115F) ||
		(cp >= 0x2E80 && cp <= 0x2EFF) ||
		(cp >= 0x2F00 && cp <= 0x2FDF) ||
		(cp >= 0x3000 && cp <= 0x303E) ||
		(cp >= 0x3041 && cp <= 0x33FF) ||
		(cp >= 0x3400 && cp <= 0x4DBF) ||
		(cp >= 0x4E00 && cp <= 0x9FFF) ||
		(cp >= 0xA000 && cp <= 0xA4CF) ||
		(cp >= 0xAC00 && cp <= 0xD7A3) ||
		(cp >= 0xF900 && cp <= 0xFAFF) ||
		(cp >= 0xFE30 && cp <= 0xFE4F) ||
		(cp >= 0xFF00 && cp <= 0xFF60) ||
		(cp >= 0xFFE0 && cp <= 0xFFE6) ||
		(cp >= 0x20000 && cp <= 0x2FFFD) ||
		(cp >= 0x30000 && cp <= 0x3FFFD)
}

type layoutState struct {
	runes     []rune
	pos       int
	grid      gridSpec
	doc       bool
	colW      int
	wideCells bool
	blackInk  bool
	sentence  int
	dim       bool
	cursor    int
	line      int
}

func newLayout(text string, grid gridSpec, doc, wideCells, blackInk bool) *layoutState {
	colW := 0
	if grid.cols > docGutter {
		colW = (grid.cols - docGutter) / 2
	}
	return &layoutState{
		runes:     []rune(text),
		grid:      grid,
		doc:       doc,
		colW:      colW,
		wideCells: wideCells,
		blackInk:  blackInk,
	}
}

func (l *layoutState) isEmpty() bool {
	if l.doc {
		return l.colW == 0 || l.grid.rows == 0
	}
	return l.grid.cols*l.grid.rows == 0
}

func (l *layoutState) next() (laidCell, bool) {
	for {
		if !l.doc && l.cursor >= l.grid.cols*l.grid.rows {
			return laidCell{}, false
		}
		if l.pos >= len(l.runes) {
			return laidCell{}, false
		}
		ch := l.runes[l.pos]
		l.pos++
		switch ch {
		case dimOnRune:
			l.dim = true
			continue
		case dimOffRune:
			l.dim = false
			continue
		case '\n':
			if l.doc {
				l.line++
				l.cursor = 0
				if l.line >= l.grid.rows*2 {
					return laidCell{}, false
				}
				continue
			}
		}

		var ink uint8
		if l.dim {
			ink = inkDim
		} else if l.blackInk {
			ink = inkBlack
		} else {
			ink = uint8(1 + l.sentence%inkColors)
		}
		if (ch == '.' || ch == '!' || ch == '?') && l.pos < len(l.runes) {
			nxt := l.runes[l.pos]
			if nxt == ' ' || nxt == fullBlockRune || (l.doc && nxt == '\n') {
				l.sentence++
			}
		}

		units := 1
		if l.wideCells && isWideCodePoint(ch) {
			units = 2
		}
		cell := l.cursor
		if l.doc {
			if units == 2 && l.colW >= 2 && cell == l.colW-1 {
				cell++
			}
			l.cursor = cell + units
			if l.cursor > l.colW {
				continue
			}
			column := l.line / l.grid.rows
			row := l.line - column*l.grid.rows
			x := (column*(l.colW+docGutter) + cell) * l.grid.cellW
			return laidCell{ch: ch, ink: ink, x: x, row: row, units: units}, true
		}

		if units == 2 && l.grid.cols >= 2 && cell%l.grid.cols == l.grid.cols-1 {
			cell++
		}
		l.cursor = cell + units
		if cell >= l.grid.cols*l.grid.rows {
			return laidCell{}, false
		}
		row := cell / l.grid.cols
		x := (cell - row*l.grid.cols) * l.grid.cellW
		return laidCell{ch: ch, ink: ink, x: x, row: row, units: units}, true
	}
}

func (l *layoutState) usedRows() int {
	for {
		if _, ok := l.next(); !ok {
			break
		}
	}
	var rows int
	if l.doc {
		rows = l.line + 1
	} else if l.grid.cols > 0 {
		rows = (l.cursor + l.grid.cols - 1) / l.grid.cols
	}
	if rows < 1 {
		rows = 1
	}
	if rows > l.grid.rows {
		rows = l.grid.rows
	}
	return rows
}

func fillRepeatBands(pixels []uint8, width, height int, grid gridSpec) {
	if grid.repeat <= 1 {
		return
	}
	for row := 0; row < grid.rows; row++ {
		for cp := 1; cp < grid.repeat; cp++ {
			bandTop := (row*grid.repeat + cp) * grid.cellH
			bandEnd := bandTop + grid.cellH
			if bandEnd > height {
				bandEnd = height
			}
			for y := bandTop; y < bandEnd; y++ {
				rowSlice := pixels[y*width : (y+1)*width]
				for i := range rowSlice {
					rowSlice[i] = bgRepeat
				}
			}
		}
	}
}

func blitGlyph(pixels []uint8, width, height int, glyph Glyph, left, top int, ink uint8) {
	for r, bits := range glyph.Rows {
		if bits == 0 {
			continue
		}
		y := top + r
		if y < 0 || y >= height {
			continue
		}
		rowBase := y * width
		for b := uint8(0); b < glyph.W; b++ {
			if bits&(0x80>>b) != 0 {
				x := left + int(b)
				if x >= 0 && x < width {
					pixels[rowBase+x] = ink
				}
			}
		}
	}
}

func fillCell(pixels []uint8, width, height int, grid gridSpec, xOrigin, row int, ink uint8) {
	x0 := xOrigin
	if x0 > width {
		x0 = width
	}
	x1 := xOrigin + grid.cellW
	if x1 > width {
		x1 = width
	}
	if x0 >= x1 {
		return
	}
	for cp := 0; cp < grid.repeat; cp++ {
		top := (row*grid.repeat + cp) * grid.cellH
		end := top + grid.cellH
		if end > height {
			end = height
		}
		for y := top; y < end; y++ {
			rowSlice := pixels[y*width+x0 : y*width+x1]
			for i := range rowSlice {
				rowSlice[i] = ink
			}
		}
	}
}

func renderBitmap(text string, width, height int, font *Font, grid gridSpec, doc, blackInk bool) []uint8 {
	pixels := make([]uint8, width*height)
	layout := newLayout(text, grid, doc, true, blackInk)
	if layout.isEmpty() {
		return pixels
	}
	fillRepeatBands(pixels, width, height, grid)
	fallback := font.Glyphs['?']
	for {
		cell, ok := layout.next()
		if !ok {
			break
		}
		if cell.ch == fullBlockRune {
			fillCell(pixels, width, height, grid, cell.x, cell.row, inkBlack)
			continue
		}
		glyph, found := font.Glyphs[cell.ch]
		if !found {
			glyph = fallback
		}
		if len(glyph.Rows) == 0 {
			continue
		}
		left := cell.x + glyph.XOff
		for cp := 0; cp < grid.repeat; cp++ {
			cellTop := (cell.row*grid.repeat + cp) * grid.cellH
			top := cellTop + font.Ascent - glyph.H - glyph.YOff
			blitGlyph(pixels, width, height, glyph, left, top, cell.ink)
		}
	}
	return pixels
}

// Lanczos3 resampling for stretch shapes (e.g. 6x6u-bw / 6x6u-sent).
// upstream: crates/natives/src/snapcompact.rs lanczos3 / contributions / stretch_indexed

type contrib struct {
	begin   int
	weights []float32
}

func lanczos3(x float32) float32 {
	if x < 0 {
		x = -x
	}
	if x < 1e-6 {
		return 1
	}
	if x >= 3 {
		return 0
	}
	pix := math.Pi * float64(x)
	return float32((math.Sin(pix) / pix) * (math.Sin(pix/3) / (pix / 3)))
}

func contributions(srcLen, dstLen int) []contrib {
	scale := float32(srcLen) / float32(dstLen)
	filtScale := scale
	if filtScale < 1 {
		filtScale = 1
	}
	support := 3 * filtScale
	out := make([]contrib, dstLen)
	for i := 0; i < dstLen; i++ {
		center := (float32(i) + 0.5) * scale
		begin := int(center - support)
		if begin < 0 {
			begin = 0
		}
		end := int(math.Ceil(float64(center + support)))
		if end > srcLen {
			end = srcLen
		}
		weights := make([]float32, 0, end-begin)
		var total float32
		for x := begin; x < end; x++ {
			w := lanczos3((float32(x) + 0.5 - center) / filtScale)
			weights = append(weights, w)
			total += w
		}
		if total != 0 {
			for k := range weights {
				weights[k] /= total
			}
		}
		out[i] = contrib{begin: begin, weights: weights}
	}
	return out
}

func stretchIndexed(src []uint8, sw, sh, dw, dh int, frame *image.RGBA) {
	horiz := contributions(sw, dw)
	tmp := make([]float32, dw*sh*3)
	for y := 0; y < sh; y++ {
		srcRow := src[y*sw : (y+1)*sw]
		dstRow := tmp[y*dw*3 : (y+1)*dw*3]
		for x, c := range horiz {
			var r, g, b float32
			for k, w := range c.weights {
				rgb := snapPalette[srcRow[c.begin+k]]
				r += float32(rgb[0]) * w
				g += float32(rgb[1]) * w
				b += float32(rgb[2]) * w
			}
			dstRow[x*3] = r
			dstRow[x*3+1] = g
			dstRow[x*3+2] = b
		}
	}
	vert := contributions(sh, dh)
	accRow := make([]float32, dw*3)
	frameW := frame.Bounds().Dx()
	copyW := dw
	if copyW > frameW {
		copyW = frameW
	}
	for y, c := range vert {
		clear(accRow)
		for k, w := range c.weights {
			srcRow := tmp[(c.begin+k)*dw*3 : (c.begin+k+1)*dw*3]
			for i := range accRow {
				accRow[i] += srcRow[i] * w
			}
		}
		rowOff := y * frame.Stride
		for x := 0; x < copyW; x++ {
			frame.Pix[rowOff+x*4] = clampByte(accRow[x*3])
			frame.Pix[rowOff+x*4+1] = clampByte(accRow[x*3+1])
			frame.Pix[rowOff+x*4+2] = clampByte(accRow[x*3+2])
			frame.Pix[rowOff+x*4+3] = 255
		}
	}
}

func clampByte(v float32) uint8 {
	r := math.Round(float64(v))
	if r <= 0 {
		return 0
	}
	if r >= 255 {
		return 255
	}
	return uint8(r)
}

func encodeIndexedPNG(pixels []uint8, width, height int) ([]byte, error) {
	var used [len(snapPalette)]bool
	for _, px := range pixels {
		if int(px) < len(used) {
			used[px] = true
		}
	}
	var remap [len(snapPalette)]uint8
	pal := make(color.Palette, 0, len(snapPalette))
	for i, isUsed := range used {
		if isUsed {
			remap[i] = uint8(len(pal))
			rgb := snapPalette[i]
			pal = append(pal, color.RGBA{R: rgb[0], G: rgb[1], B: rgb[2], A: 255})
		}
	}
	if len(pal) == 0 {
		pal = append(pal, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	}
	img := &image.Paletted{
		Pix:     make([]uint8, width*height),
		Stride:  width,
		Rect:    image.Rect(0, 0, width, height),
		Palette: pal,
	}
	for i, px := range pixels {
		img.Pix[i] = remap[px]
	}
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encodeRGBAPNG(img *image.RGBA) ([]byte, error) {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// RenderPNG rasterizes pre-normalized text into a PNG frame for shape and returns
// the raw PNG bytes and rendered pixel height.
// upstream: crates/natives/src/snapcompact.rs render_snapcompact_png_sync
func RenderPNG(text string, shape Shape, size int) (pngBytes []byte, width, height int, err error) {
	if size <= 0 {
		size = shape.FrameSize
	}
	if size <= 0 || size > maxFrameSize {
		return nil, 0, 0, fmt.Errorf("snapcompact: invalid frame size %d: expected 1..=%d", size, maxFrameSize)
	}
	font := ResolveFont(shape.Font)
	if font == nil {
		return nil, 0, 0, fmt.Errorf("snapcompact: unknown font %q", shape.Font)
	}
	blackInk := true
	switch shape.Variant {
	case "sent":
		blackInk = false
	case "bw", "":
		blackInk = true
	default:
		return nil, 0, 0, fmt.Errorf("snapcompact: unknown variant %q", shape.Variant)
	}
	targetW := shape.CellWidth
	if targetW < 1 {
		targetW = font.CellW
	}
	targetH := shape.CellHeight
	if targetH < 1 {
		targetH = font.CellH
	}
	repeat := shape.LineRepeat
	if repeat < 1 {
		repeat = 1
	}
	doc := shape.Columns == 2
	grid := gridSpec{
		cols:   size / targetW,
		rows:   size / targetH / repeat,
		repeat: repeat,
		cellW:  targetW,
		cellH:  targetH,
	}
	if grid.cols == 0 || grid.rows == 0 {
		return nil, 0, 0, fmt.Errorf("snapcompact: frame size %d cannot fit %dx%d cell grid (repeat %d)", size, targetW, targetH, repeat)
	}
	used := newLayout(text, grid, doc, true, blackInk).usedRows()
	contentHeight := used * grid.repeat * grid.cellH
	height = contentHeight
	if height < 64 {
		height = 64
	}
	stretch := (shape.Stretch == nil || *shape.Stretch) && (targetW != font.CellW || targetH != font.CellH)
	if !stretch {
		pixels := renderBitmap(text, size, height, font, grid, doc, blackInk)
		if contentHeight < height {
			clear(pixels[contentHeight*size:])
		}
		raw, err := encodeIndexedPNG(pixels, size, height)
		return raw, size, height, err
	}

	native := grid
	native.cellW = font.CellW
	native.cellH = font.CellH
	srcW := grid.cols * font.CellW
	srcH := used * grid.repeat * font.CellH
	dstW := grid.cols * targetW
	dstH := used * grid.repeat * targetH
	indexed := renderBitmap(text, srcW, srcH, font, native, doc, blackInk)

	frame := image.NewRGBA(image.Rect(0, 0, size, height))
	for i := 0; i < len(frame.Pix); i += 4 {
		frame.Pix[i] = 255
		frame.Pix[i+1] = 255
		frame.Pix[i+2] = 255
		frame.Pix[i+3] = 255
	}
	stretchIndexed(indexed, srcW, srcH, dstW, dstH, frame)
	raw, err := encodeRGBAPNG(frame)
	return raw, size, height, err
}

// RenderFrame renders one already-normalized frame and returns its base64 PNG + geometry.
func RenderFrame(text string, shape Shape, size int) (RenderedFrame, error) {
	if size <= 0 {
		size = shape.FrameSize
	}
	geo := ComputeGeometry(shape, size)
	chars := renderedChars(text, shape, geo)
	raw, _, _, err := RenderPNG(text, shape, size)
	if err != nil {
		return RenderedFrame{}, err
	}
	return RenderedFrame{
		Data:  base64.StdEncoding.EncodeToString(raw),
		Cols:  geo.Cols,
		Rows:  geo.Rows,
		Chars: chars,
	}, nil
}
