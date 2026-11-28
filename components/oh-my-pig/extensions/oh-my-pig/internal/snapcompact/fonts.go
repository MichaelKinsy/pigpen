// Package snapcompact implements omp's bitmap-frame context compression
// (@oh-my-pi/snapcompact + crates/natives/src/snapcompact.rs) in pure Go stdlib.
//
// Instead of calling an LLM to summarize discarded conversation history,
// snapcompact serializes discarded messages into a compact transcript, normalizes
// them for the selected bitmap font, lays out verbatim text edges around an
// imaged middle (foveating HQ/LQ/HQ when long), rasterizes each frame with
// bundled public-domain X.org BDF and unscii-8 bitmap fonts, encodes indexed/RGB
// PNGs, persists the archive in CompactionEntry.Details, and re-attaches the
// image blocks on every EventContext turn for vision-capable models.
//
// Upstream:
//   - packages/snapcompact/src/snapcompact.ts (tag v19.1.1)
//   - crates/natives/src/snapcompact.rs (tag v19.1.1)
//   - packages/coding-agent/src/session/compact-modes.ts (tag v19.1.1)
//   - packages/coding-agent/src/session/compaction-methods.ts (tag v19.1.1)
package snapcompact

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"embed"
	"io"
	"strconv"
	"strings"
	"sync"
)

//go:embed fonts/5x8.bdf.gz fonts/6x12.bdf.gz fonts/8x13.bdf.gz fonts/unscii-8.hex.gz
var embeddedFonts embed.FS

// Glyph is one bitmap character from a BDF or unifont .hex font.
// upstream: crates/natives/src/snapcompact.rs Glyph
type Glyph struct {
	W    uint8
	H    int
	XOff int
	YOff int
	Rows []uint8
}

// Font is a parsed bitmap font indexed by Unicode code point.
// upstream: crates/natives/src/snapcompact.rs Font
type Font struct {
	Name   string
	Glyphs map[rune]Glyph
	Ascent int
	CellW  int
	CellH  int
}

// Supports reports whether the font can render code point r.
// Renderer control codes (DIM_ON, DIM_OFF, FULL_BLOCK, '\n') are always supported.
func (f *Font) Supports(r rune) bool {
	if r == dimOnRune || r == dimOffRune || r == fullBlockRune || r == '\n' {
		return true
	}
	if f == nil {
		return false
	}
	_, ok := f.Glyphs[r]
	return ok
}

var (
	font5x8Once  sync.Once
	font5x8Val   *Font
	font8x8Once  sync.Once
	font8x8Val   *Font
	font6x12Once sync.Once
	font6x12Val  *Font
	font8x13Once sync.Once
	font8x13Val  *Font
)

// ResolveFont returns the lazily parsed embedded bitmap font for name.
// Supported names: "5x8", "8x8", "6x12", "8x13" ("silver" falls back to "8x13"
// in pure-stdlib Go since Silver.ttf requires a TrueType rasterizer).
func ResolveFont(name string) *Font {
	switch name {
	case "5x8", "":
		font5x8Once.Do(func() {
			font5x8Val = parseBDF("5x8", mustReadGzip("fonts/5x8.bdf.gz"), 5, 8)
		})
		return font5x8Val
	case "8x8":
		font8x8Once.Do(func() {
			font8x8Val = parseHex("8x8", mustReadGzip("fonts/unscii-8.hex.gz"))
		})
		return font8x8Val
	case "6x12":
		font6x12Once.Do(func() {
			font6x12Val = parseBDF("6x12", mustReadGzip("fonts/6x12.bdf.gz"), 6, 12)
		})
		return font6x12Val
	case "8x13", "silver":
		font8x13Once.Do(func() {
			font8x13Val = parseBDF("8x13", mustReadGzip("fonts/8x13.bdf.gz"), 8, 13)
		})
		return font8x13Val
	default:
		return nil
	}
}

func mustReadGzip(path string) []byte {
	raw, err := embeddedFonts.ReadFile(path)
	if err != nil {
		panic("snapcompact: missing embedded font " + path + ": " + err.Error())
	}
	gr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		panic("snapcompact: invalid gzip font " + path + ": " + err.Error())
	}
	defer func() { _ = gr.Close() }()
	out, err := io.ReadAll(gr)
	if err != nil {
		panic("snapcompact: decompress font " + path + ": " + err.Error())
	}
	return out
}

// parseBDF parses an X.org BDF bitmap font.
// upstream: crates/natives/src/snapcompact.rs parse_bdf
func parseBDF(name string, data []byte, cellW, cellH int) *Font {
	glyphs := make(map[rune]Glyph, 256)
	ascent := 0
	enc := -1
	var bbx [4]int

	scanner := bufio.NewScanner(bytes.NewReader(data))
	inBitmap := false
	var rows []uint8
	for scanner.Scan() {
		line := scanner.Text()
		if inBitmap {
			if strings.HasPrefix(line, "ENDCHAR") {
				inBitmap = false
				if enc >= 0 {
					w := bbx[0]
					if w < 0 {
						w = 0
					} else if w > 8 {
						w = 8
					}
					cpRows := make([]uint8, len(rows))
					copy(cpRows, rows)
					glyphs[rune(enc)] = Glyph{
						W:    uint8(w),
						H:    bbx[1],
						XOff: bbx[2],
						YOff: bbx[3],
						Rows: cpRows,
					}
				}
				rows = rows[:0]
				continue
			}
			v, _ := strconv.ParseUint(strings.TrimSpace(line), 16, 8)
			rows = append(rows, uint8(v))
			continue
		}
		if rest, ok := strings.CutPrefix(line, "FONT_ASCENT"); ok {
			if v, err := strconv.Atoi(strings.TrimSpace(rest)); err == nil {
				ascent = v
			}
		} else if rest, ok := strings.CutPrefix(line, "ENCODING"); ok {
			if v, err := strconv.Atoi(strings.TrimSpace(rest)); err == nil {
				enc = v
			} else {
				enc = -1
			}
		} else if rest, ok := strings.CutPrefix(line, "BBX"); ok {
			fields := strings.Fields(rest)
			for i := 0; i < 4; i++ {
				if i < len(fields) {
					bbx[i], _ = strconv.Atoi(fields[i])
				} else {
					bbx[i] = 0
				}
			}
		} else if strings.HasPrefix(line, "BITMAP") {
			inBitmap = true
			rows = rows[:0]
		}
	}
	return &Font{
		Name:   name,
		Glyphs: glyphs,
		Ascent: ascent,
		CellW:  cellW,
		CellH:  cellH,
	}
}

// parseHex parses a unifont-style .hex font (CODEPOINT:16-hex-digit 8x8 bitmap).
// upstream: crates/natives/src/snapcompact.rs parse_hex
func parseHex(name string, data []byte) *Font {
	glyphs := make(map[rune]Glyph, 256)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		cpHex, bitsHex, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		enc, err := strconv.ParseUint(strings.TrimSpace(cpHex), 16, 32)
		if err != nil {
			continue
		}
		bitsHex = strings.TrimSpace(bitsHex)
		if len(bitsHex) != 16 {
			continue
		}
		rows := make([]uint8, 8)
		for i := 0; i < 8; i++ {
			v, _ := strconv.ParseUint(bitsHex[i*2:i*2+2], 16, 8)
			rows[i] = uint8(v)
		}
		glyphs[rune(enc)] = Glyph{
			W:    8,
			H:    8,
			XOff: 0,
			YOff: -1,
			Rows: rows,
		}
	}
	return &Font{
		Name:   name,
		Glyphs: glyphs,
		Ascent: 7,
		CellW:  8,
		CellH:  8,
	}
}
