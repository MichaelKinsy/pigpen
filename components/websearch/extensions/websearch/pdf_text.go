package websearch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	pdflib "github.com/ledongthuc/pdf"
	"golang.org/x/text/unicode/bidi"
	"golang.org/x/text/unicode/norm"
)

// Local PDF text extraction: the Go stand-in for unpdf (PDF.js) in pdf-extract.ts:196-240. The
// file structure (xref, filters, encryption, fonts, ToUnicode maps) comes from the pure-Go
// library; the text assembly follows PDF.js's getTextContent (items split at font changes, line
// changes and word gaps), because pdf-extract.ts joins the items with a space and collapses
// whitespace, so only where PDF.js would have started a new item or inserted a space matters.
// Differences from PDF.js are listed in docs/PORT.md ("PDF text extraction").

// pdfDocument is one opened PDF.
type pdfDocument struct {
	reader    *pdflib.Reader
	encrypted bool
}

// pdfPDFJSMessages are the messages PDF.js raises for the failures this library reports, so the
// tool result reads the same.
const (
	pdfErrInvalid  = "Invalid PDF structure."
	pdfErrEmpty    = "The PDF file is empty, i.e. its size is zero bytes."
	pdfErrPassword = "No password given"
)

// openPDF opens data, recovering from a damaged cross-reference table the way PDF.js does by
// rebuilding it from the objects in the file.
func openPDF(data []byte) (doc *pdfDocument, err error) {
	if len(data) == 0 {
		return nil, errors.New(pdfErrEmpty)
	}
	if i := bytes.Index(data[:min(len(data), 1024)], []byte("%PDF-")); i > 0 {
		data = data[i:]
	}
	r, err := newPDFReader(data)
	if err != nil && !isPDFPasswordError(err) {
		if rebuilt := rebuildXref(data); rebuilt != nil {
			r, err = newPDFReader(rebuilt)
		}
	}
	if err != nil {
		if isPDFPasswordError(err) {
			return nil, errors.New(pdfErrPassword)
		}
		return nil, errors.New(pdfErrInvalid)
	}
	return &pdfDocument{reader: r, encrypted: !safeValue(func() pdflib.Value { return r.Trailer().Key("Encrypt") }).IsNull()}, nil
}

func isPDFPasswordError(err error) bool { return errors.Is(err, pdflib.ErrInvalidPassword) }

// newPDFReader turns the library's panics on malformed input into errors.
func newPDFReader(data []byte) (r *pdflib.Reader, err error) {
	defer func() {
		if p := recover(); p != nil {
			r, err = nil, fmt.Errorf("%v", p)
		}
	}()
	r, err = pdflib.NewReader(bytes.NewReader(data), int64(len(data)))
	if err == nil && r.Trailer().Key("Root").IsNull() {
		return nil, errors.New("no document catalog")
	}
	return r, err
}

func safeValue(f func() pdflib.Value) (v pdflib.Value) {
	defer func() { _ = recover() }()
	return f()
}

var (
	objHeaderRE = regexp.MustCompile(`(?:^|[\r\n\s])(\d+)\s+(\d+)\s+obj\b`)
	trailerRE   = regexp.MustCompile(`trailer\s*<<`)
	rootRefRE   = regexp.MustCompile(`/Root\s+\d+\s+\d+\s+R`)
	prevKeyRE   = regexp.MustCompile(`/(?:Prev|XRefStm)\s+\d+`)
)

// rebuildXref scans the file for "N G obj" headers and appends a fresh cross-reference table and
// trailer, or returns nil when the file has no trailer with a /Root (PDF.js fails then too: a
// truncated file that lost its trailer is not recovered).
func rebuildXref(data []byte) []byte {
	type obj struct{ num, gen, off int }
	byNum := map[int]obj{}
	maxNum := 0
	for _, m := range objHeaderRE.FindAllSubmatchIndex(data, -1) {
		num, _ := strconv.Atoi(string(data[m[2]:m[3]]))
		gen, _ := strconv.Atoi(string(data[m[4]:m[5]]))
		if num <= 0 || num > 10_000_000 {
			continue
		}
		byNum[num] = obj{num, gen, m[2]}
		maxNum = max(maxNum, num)
	}
	if len(byNum) == 0 {
		return nil
	}
	var trailerDict string
	for _, loc := range trailerRE.FindAllIndex(data, -1) {
		end := min(len(data), loc[1]+4096)
		if i := bytes.Index(data[loc[0]:end], []byte(">>\n")); i >= 0 {
			end = loc[0] + i + 2
		}
		if t := string(data[loc[0]+len("trailer") : end]); rootRefRE.MatchString(t) {
			trailerDict = t
		}
	}
	if trailerDict == "" {
		return nil
	}
	trailerDict = prevKeyRE.ReplaceAllString(trailerDict, "")
	trailerDict = regexp.MustCompile(`/Size\s+\d+`).ReplaceAllString(trailerDict, "")
	trailerDict = strings.Replace(trailerDict, "<<", fmt.Sprintf("<< /Size %d", maxNum+1), 1)
	var b bytes.Buffer
	b.Write(data)
	b.WriteString("\n")
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n", maxNum+1)
	b.WriteString("0000000000 65535 f \n")
	for n := 1; n <= maxNum; n++ {
		if o, ok := byNum[n]; ok {
			fmt.Fprintf(&b, "%010d %05d n \n", o.off, o.gen)
		} else {
			b.WriteString("0000000000 65535 f \n")
		}
	}
	fmt.Fprintf(&b, "trailer\n%s\nstartxref\n%d\n%%%%EOF\n", trailerDict, xref)
	return b.Bytes()
}

func (d *pdfDocument) numPages() int {
	n := 0
	func() {
		defer func() { _ = recover() }()
		n = d.reader.NumPage()
	}()
	return max(n, 0)
}

// infoString is metadata.info[key] when it is a string.
func (d *pdfDocument) infoString(key string) (s string, ok bool) {
	defer func() {
		if recover() != nil {
			s, ok = "", false
		}
	}()
	v := d.reader.Trailer().Key("Info").Key(key)
	if v.Kind() != pdflib.String {
		return "", false
	}
	raw := []byte(v.RawString())
	if d.encrypted {
		raw = stripPKCS7(raw)
	}
	return decodePDFTextString(raw), true
}

// stripPKCS7 removes the padding the library leaves on AES-decrypted strings.
func stripPKCS7(b []byte) []byte {
	if n := len(b); n > 0 {
		pad := int(b[n-1])
		if pad >= 1 && pad <= 16 && pad <= n && bytes.Equal(b[n-pad:], bytes.Repeat([]byte{byte(pad)}, pad)) {
			return b[:n-pad]
		}
	}
	return b
}

// decodePDFTextString decodes a text string: UTF-16BE or UTF-8 with a byte order mark, else
// PDFDocEncoding (Latin-1 outside the 0x80-0x9f block, which PDFDocEncoding assigns to
// punctuation).
func decodePDFTextString(b []byte) string {
	switch {
	case len(b) >= 2 && b[0] == 0xfe && b[1] == 0xff:
		u := make([]uint16, 0, len(b)/2)
		for i := 2; i+1 < len(b); i += 2 {
			u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
		}
		return string(utf16.Decode(u))
	case len(b) >= 2 && b[0] == 0xff && b[1] == 0xfe:
		u := make([]uint16, 0, len(b)/2)
		for i := 2; i+1 < len(b); i += 2 {
			u = append(u, uint16(b[i+1])<<8|uint16(b[i]))
		}
		return string(utf16.Decode(u))
	case len(b) >= 3 && b[0] == 0xef && b[1] == 0xbb && b[2] == 0xbf:
		return string(b[3:])
	}
	runes := make([]rune, len(b))
	for i, c := range b {
		if c >= 0x80 && c <= 0x9f {
			runes[i] = pdfDocHigh[c-0x80]
		} else {
			runes[i] = rune(c)
		}
	}
	return string(runes)
}

// pdfDocHigh is PDFDocEncoding 0x80-0x9f (PDF 32000-1 Table D.2).
var pdfDocHigh = [32]rune{
	0x2022, 0x2020, 0x2021, 0x2026, 0x2014, 0x2013, 0x0192, 0x2044,
	0x2039, 0x203a, 0x2212, 0x2030, 0x201e, 0x201c, 0x201d, 0x2018,
	0x2019, 0x201a, 0x2122, 0xfb01, 0xfb02, 0x0141, 0x0152, 0x0160,
	0x0178, 0x017d, 0x0131, 0x0142, 0x0153, 0x0161, 0x017e, 0xfffd,
}

// pageText is getTextContent().items joined with a space, whitespace collapsed and trimmed.
// A page whose content cannot be read yields what was read before the failure (PDF.js tolerates
// damaged streams the same way).
func (d *pdfDocument) pageText(ctx context.Context, number int) (text string, err error) {
	defer func() {
		if p := recover(); p != nil {
			if p == errPDFCanceled {
				err = ctx.Err()
			}
		}
	}()
	page := d.reader.Page(number)
	if page.V.IsNull() {
		return "", nil
	}
	in := newPDFInterp(ctx, page)
	func() {
		defer func() {
			if p := recover(); p != nil && p == errPDFCanceled {
				panic(p)
			}
		}()
		in.run(page.V.Key("Contents"), pdfIdentity, page.Resources(), 0)
	}()
	in.b.flush()
	return jsTrim(collapseSpaces(strings.Join(in.b.items, " "))), nil
}

var errPDFCanceled = errors.New("pdf extraction canceled")

type pdfMatrix [6]float64

var pdfIdentity = pdfMatrix{1, 0, 0, 1, 0, 0}

// mul is a followed by b.
func (a pdfMatrix) mul(b pdfMatrix) pdfMatrix {
	return pdfMatrix{
		a[0]*b[0] + a[1]*b[2], a[0]*b[1] + a[1]*b[3],
		a[2]*b[0] + a[3]*b[2], a[2]*b[1] + a[3]*b[3],
		a[4]*b[0] + a[5]*b[2] + b[4], a[4]*b[1] + a[5]*b[3] + b[5],
	}
}

type pdfFont struct {
	font      pdflib.Font
	enc       pdflib.TextEncoding
	composite bool
	twoByte   bool
	type3     bool
	fontMat   float64
	defWidth  float64
	cidWidth  map[int]float64
	hasWidths bool
	fallback  float64
	base      string
}

type pdfTextState struct {
	ctm      pdfMatrix
	font     *pdfFont
	fontName string
	size     float64
	tc, tw   float64
	th       float64
	tl       float64
	rise     float64
}

type pdfInterp struct {
	ctx      context.Context
	page     pdflib.Page
	b        pdfTextBuilder
	fonts    map[string]*pdfFont
	st       pdfTextState
	stack    []pdfTextState
	tm, tlm  pdfMatrix
	box      [4]float64
	ops      int
	lastSize float64
	lastName string
	lastBase string
}

func newPDFInterp(ctx context.Context, page pdflib.Page) *pdfInterp {
	in := &pdfInterp{ctx: ctx, page: page, fonts: map[string]*pdfFont{}, st: pdfTextState{ctm: pdfIdentity, th: 1}, tm: pdfIdentity, tlm: pdfIdentity}
	in.box = pdfViewBox(page)
	return in
}

// pdfViewBox is the page's CropBox clipped to its MediaBox (PDF.js drops text outside it).
func pdfViewBox(page pdflib.Page) [4]float64 {
	read := func(key string) ([4]float64, bool) {
		for v := page.V; !v.IsNull(); v = v.Key("Parent") {
			if a := v.Key(key); a.Kind() == pdflib.Array && a.Len() >= 4 {
				x0, y0, x1, y1 := a.Index(0).Float64(), a.Index(1).Float64(), a.Index(2).Float64(), a.Index(3).Float64()
				return [4]float64{math.Min(x0, x1), math.Min(y0, y1), math.Max(x0, x1), math.Max(y0, y1)}, true
			}
		}
		return [4]float64{}, false
	}
	media, ok := read("MediaBox")
	if !ok {
		media = [4]float64{0, 0, 612, 792}
	}
	if crop, ok := read("CropBox"); ok {
		media = [4]float64{math.Max(media[0], crop[0]), math.Max(media[1], crop[1]), math.Min(media[2], crop[2]), math.Min(media[3], crop[3])}
	}
	if media[2] <= media[0] || media[3] <= media[1] {
		return [4]float64{0, 0, 612, 792}
	}
	return media
}

func (in *pdfInterp) font(res pdflib.Value, name string) *pdfFont {
	if f, ok := in.fonts[name]; ok {
		return f
	}
	dict := res.Key("Font").Key(name)
	if dict.IsNull() {
		dict = in.page.Resources().Key("Font").Key(name)
	}
	f := &pdfFont{font: pdflib.Font{V: dict}}
	f.enc = f.font.Encoder()
	subtype := dict.Key("Subtype").Name()
	f.composite = subtype == "Type0"
	f.type3 = subtype == "Type3"
	f.base = f.font.BaseFont()
	if f.type3 {
		f.fontMat = 0.001
		if m := dict.Key("FontMatrix"); m.Kind() == pdflib.Array && m.Len() >= 1 && m.Index(0).Float64() != 0 {
			f.fontMat = m.Index(0).Float64()
		}
	}
	if f.composite {
		enc := dict.Key("Encoding")
		f.twoByte = enc.Kind() == pdflib.Name && (strings.HasPrefix(enc.Name(), "Identity") || strings.HasSuffix(enc.Name(), "-UCS2-H") || strings.HasSuffix(enc.Name(), "-UCS2-V"))
		desc := dict.Key("DescendantFonts").Index(0)
		f.defWidth = 1000
		if dw := desc.Key("DW"); dw.Kind() == pdflib.Integer || dw.Kind() == pdflib.Real {
			f.defWidth = dw.Float64()
		}
		f.cidWidth = parseCIDWidths(desc.Key("W"))
	} else {
		f.hasWidths = dict.Key("Widths").Len() > 0
		f.fallback = 500
		if strings.Contains(strings.ToLower(f.base), "courier") {
			f.fallback = 600
		}
	}
	in.fonts[name] = f
	return f
}

// parseCIDWidths reads a CIDFont /W array: "c [w1 w2 ...]" and "cfirst clast w".
func parseCIDWidths(w pdflib.Value) map[int]float64 {
	out := map[int]float64{}
	n := w.Len()
	for i := 0; i < n && len(out) < 1<<16; {
		first := int(w.Index(i).Float64())
		if i+1 >= n {
			break
		}
		next := w.Index(i + 1)
		if next.Kind() == pdflib.Array {
			for j := 0; j < next.Len(); j++ {
				out[first+j] = next.Index(j).Float64()
			}
			i += 2
			continue
		}
		if i+2 >= n {
			break
		}
		last, width := int(next.Float64()), w.Index(i+2).Float64()
		for c := first; c <= last && c-first < 1<<16; c++ {
			out[c] = width
		}
		i += 3
	}
	return out
}

// glyphs splits a shown string into (text, width in text space units per font-size unit).
type pdfGlyph struct {
	text  string
	width float64
	space bool
}

func (f *pdfFont) decode(raw string) []pdfGlyph {
	var out []pdfGlyph
	switch {
	case f.composite && f.twoByte:
		for i := 0; i+1 < len(raw); i += 2 {
			code := int(raw[i])<<8 | int(raw[i+1])
			w := f.defWidth
			if cw, ok := f.cidWidth[code]; ok {
				w = cw
			}
			out = append(out, pdfGlyph{text: f.enc.Decode(raw[i : i+2]), width: w / 1000})
		}
	case f.composite:
		text := f.enc.Decode(raw)
		for _, r := range text {
			out = append(out, pdfGlyph{text: string(r), width: f.defWidth / 1000})
		}
	default:
		for i := 0; i < len(raw); i++ {
			code := int(raw[i])
			var w float64
			switch {
			case f.type3:
				w = f.font.Width(code) * f.fontMat
			case f.hasWidths && code >= f.font.FirstChar() && code <= f.font.LastChar():
				w = f.font.Width(code) / 1000
			case code == 32:
				w = 0.278
			default:
				w = f.fallback / 1000
			}
			g := pdfGlyph{text: f.enc.Decode(raw[i : i+1]), width: w}
			g.space = code == 32
			out = append(out, g)
		}
	}
	return out
}

const maxFormDepth = 8

func (in *pdfInterp) run(strm pdflib.Value, ctm pdfMatrix, res pdflib.Value, depth int) {
	if strm.IsNull() {
		return
	}
	saved, savedStack, savedFonts := in.st, in.stack, in.fonts
	in.st.ctm = ctm.mul(in.st.ctm)
	in.stack, in.fonts = nil, map[string]*pdfFont{}
	defer func() { in.st, in.stack, in.fonts = saved, savedStack, savedFonts }()
	handle := func(stk *pdflib.Stack, op string) {
		in.ops++
		if in.ops&1023 == 0 && in.ctx.Err() != nil {
			panic(errPDFCanceled)
		}
		n := stk.Len()
		args := make([]pdflib.Value, n)
		for i := n - 1; i >= 0; i-- {
			args[i] = stk.Pop()
		}
		in.op(op, args, res, depth)
	}
	// A page's /Contents array is read one stream at a time: the library would join the streams
	// without a separator, so "Do" at the end of one and "BT" at the start of the next fuse into
	// one unknown operator.
	if strm.Kind() == pdflib.Array {
		for i := range strm.Len() {
			pdflib.Interpret(strm.Index(i), handle)
		}
		return
	}
	pdflib.Interpret(strm, handle)
}

func num(args []pdflib.Value, i int) float64 {
	if i < len(args) {
		return args[i].Float64()
	}
	return 0
}

func (in *pdfInterp) op(op string, args []pdflib.Value, res pdflib.Value, depth int) {
	st := &in.st
	switch op {
	case "q":
		in.stack = append(in.stack, *st)
	case "Q":
		if n := len(in.stack); n > 0 {
			*st = in.stack[n-1]
			in.stack = in.stack[:n-1]
		}
	case "cm":
		if len(args) == 6 {
			st.ctm = pdfMatrix{num(args, 0), num(args, 1), num(args, 2), num(args, 3), num(args, 4), num(args, 5)}.mul(st.ctm)
		}
	case "BT":
		in.tm, in.tlm = pdfIdentity, pdfIdentity
	case "Tf":
		if len(args) == 2 {
			st.fontName, st.size = args[0].Name(), args[1].Float64()
			st.font = in.font(res, st.fontName)
		}
	case "Tc":
		st.tc = num(args, 0)
	case "Tw":
		st.tw = num(args, 0)
	case "Tz":
		st.th = num(args, 0) / 100
	case "TL":
		st.tl = num(args, 0)
	case "Ts":
		st.rise = num(args, 0)
	case "Td", "TD":
		if op == "TD" {
			st.tl = -num(args, 1)
		}
		in.tlm = pdfMatrix{1, 0, 0, 1, num(args, 0), num(args, 1)}.mul(in.tlm)
		in.tm = in.tlm
	case "Tm":
		if len(args) == 6 {
			in.tm = pdfMatrix{num(args, 0), num(args, 1), num(args, 2), num(args, 3), num(args, 4), num(args, 5)}
			in.tlm = in.tm
		}
	case "T*":
		in.nextLine()
	case "'", "\"":
		if op == "\"" && len(args) == 3 {
			st.tw, st.tc = num(args, 0), num(args, 1)
		}
		in.nextLine()
		if len(args) > 0 {
			in.show(args[len(args)-1].RawString(), 0)
		}
	case "Tj":
		if len(args) > 0 {
			in.show(args[0].RawString(), 0)
		}
	case "TJ":
		if len(args) == 0 || args[0].Kind() != pdflib.Array {
			return
		}
		arr := args[0]
		var pending strings.Builder
		flush := func(extra float64) {
			in.show(pending.String(), extra)
			pending.Reset()
		}
		for i := 0; i < arr.Len(); i++ {
			e := arr.Index(i)
			switch e.Kind() {
			case pdflib.String:
				pending.WriteString(e.RawString())
			case pdflib.Integer, pdflib.Real:
				if v := e.Float64(); v != 0 {
					flush(-v * st.size / 1000)
				}
			}
		}
		if pending.Len() > 0 {
			flush(0)
		}
	case "Do":
		if len(args) == 1 && depth < maxFormDepth {
			xo := res.Key("XObject").Key(args[0].Name())
			if xo.IsNull() {
				xo = in.page.Resources().Key("XObject").Key(args[0].Name())
			}
			if xo.Kind() == pdflib.Stream && xo.Key("Subtype").Name() == "Form" {
				m := pdfIdentity
				if a := xo.Key("Matrix"); a.Kind() == pdflib.Array && a.Len() == 6 {
					m = pdfMatrix{a.Index(0).Float64(), a.Index(1).Float64(), a.Index(2).Float64(), a.Index(3).Float64(), a.Index(4).Float64(), a.Index(5).Float64()}
				}
				inner := xo.Key("Resources")
				if inner.IsNull() {
					inner = res
				}
				in.b.flush()
				savedTm, savedTlm := in.tm, in.tlm
				in.run(xo, m, inner, depth+1)
				in.tm, in.tlm = savedTm, savedTlm
				in.b.flush()
			}
		}
	}
}

func (in *pdfInterp) nextLine() {
	in.tlm = pdfMatrix{1, 0, 0, 1, 0, -in.st.tl}.mul(in.tlm)
	in.tm = in.tlm
}

// trm is the text rendering matrix: the glyph origin and scale in device space.
func (in *pdfInterp) trm() pdfMatrix {
	st := &in.st
	return pdfMatrix{st.size * st.th, 0, 0, st.size, 0, st.rise}.mul(in.tm).mul(st.ctm)
}

// show is buildTextContentItem (pdf.js evaluator): shows one string, then moves by extra.
func (in *pdfInterp) show(raw string, extra float64) {
	st := &in.st
	if st.font == nil {
		return
	}
	// PDF.js starts a new item when the size changes or the font changes (a different resource
	// name for a different font program).
	if in.lastName != "" && (in.lastSize != st.size || in.lastName != st.fontName && in.lastBase != st.font.base) {
		in.b.flush()
	}
	in.lastName, in.lastBase, in.lastSize = st.fontName, st.font.base, st.size
	glyphs := st.font.decode(raw)
	for i, g := range glyphs {
		after := st.tc
		if i == len(glyphs)-1 {
			after += extra
		}
		if g.space || isPDFWhitespace(g.text) {
			if g.space {
				after += st.tw
			}
			in.advance(g.width*st.size + after)
			continue
		}
		adv := g.width * st.size
		if g.text == "" || isInvisibleFormat(g.text) {
			in.advance(adv + after)
			continue
		}
		t := in.trm()
		x, y := t[4], t[5]
		scale := math.Hypot(in.st.ctm[0], in.st.ctm[1]) * math.Hypot(in.tlm[0], in.tlm[1])
		if scale == 0 {
			scale = 1
		}
		inBox := !(x+adv*scale*st.th < in.box[0] || x > in.box[2] || y < in.box[1] || y > in.box[3])
		if inBox {
			ex, ey := in.device(pdfMatrix{1, 0, 0, 1, adv * st.th, 0}.mul(in.tm))
			in.b.glyph(g.text, pdfGlyphPlace{x: x, y: y, endX: ex, endY: ey, dirX: t[0], dirY: t[1], size: st.size, scale: scale, height: math.Hypot(t[2], t[3])})
		}
		in.advance(adv + after)
	}
	if len(glyphs) == 0 && extra != 0 {
		in.advance(extra)
	}
}

// device is the origin of the glyph space for text matrix tm in device space (the translation of
// getCurrentTextTransform).
func (in *pdfInterp) device(tm pdfMatrix) (x, y float64) {
	m := pdfMatrix{1, 0, 0, 1, 0, in.st.rise}.mul(tm).mul(in.st.ctm)
	return m[4], m[5]
}

// advance moves the text matrix right by tx (in unscaled text space, times the horizontal scale).
func (in *pdfInterp) advance(tx float64) {
	in.tm = pdfMatrix{1, 0, 0, 1, tx * in.st.th, 0}.mul(in.tm)
}

func isPDFWhitespace(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return r != utf8.RuneError && s != "" && isJSSpace(r) && len(s) == utf8.RuneLen(r)
}

// isInvisibleFormat is PDF.js's isInvisibleFormatMark: zero-width and bidi control characters.
func isInvisibleFormat(s string) bool {
	for _, r := range s {
		if !(r >= 0x200b && r <= 0x200f || r >= 0x202a && r <= 0x202e || r >= 0x2060 && r <= 0x2064 || r == 0xfeff) {
			return false
		}
	}
	return s != ""
}

// pdfTextBuilder groups glyphs into PDF.js text items.
type pdfTextBuilder struct {
	items   []string
	cur     strings.Builder
	active  bool
	hasPrev bool
	prevX   float64
	prevY   float64
	height  float64
}

func (b *pdfTextBuilder) flush() {
	if b.active {
		b.items = append(b.items, bidiVisualToLogical(normalizePDFText(b.cur.String())))
		b.cur.Reset()
		b.active = false
	}
}

// pdfGlyphPlace is where a glyph starts and ends in device space, the direction of the text line
// (the first row of the glyph matrix), the font size, the advance scale and the glyph height.
type pdfGlyphPlace struct {
	x, y, endX, endY float64
	dirX, dirY       float64
	size, scale      float64
	height           float64
}

// glyph is compareWithLastPosition plus the append of buildTextContentItem (pdf.js evaluator): a
// glyph that starts a new line, or after a gap that is a word space or more, starts or extends an
// item accordingly.
func (b *pdfTextBuilder) glyph(text string, p pdfGlyphPlace) {
	const (
		trackingSpaceMin = 0.102
		negativeSpaceMax = -0.2
		spaceInFlowMax   = 0.6
	)
	if b.hasPrev && !(b.prevX == p.x && b.prevY == p.y) {
		dx, dy := p.x-b.prevX, p.y-b.prevY
		norm := math.Hypot(p.dirX, p.dirY)
		if norm == 0 {
			norm = 1
		}
		ux, uy := p.dirX/norm, p.dirY/norm
		gap := (dx*ux + dy*uy) / p.scale
		across := -dx*uy + dy*ux
		itemHeight := b.height
		if !b.active {
			itemHeight = p.height
		}
		switch {
		case gap < negativeSpaceMax*p.size:
			b.flush()
		case math.Abs(across) > itemHeight:
			b.flush()
		case gap <= trackingSpaceMin*p.size:
		case gap <= spaceInFlowMax*p.size:
			if b.active {
				b.cur.WriteByte(' ')
			}
		default:
			b.flush()
		}
	}
	if !b.active {
		b.active = true
		b.height = p.height
	}
	b.cur.WriteString(text)
	b.hasPrev = true
	b.prevX, b.prevY = p.endX, p.endY
}

var pdfNormalizeRE = regexp.MustCompile("[\u00a0\u00b5\u037e\u0eb3\u2000-\u200a\u202f\u2126\ufb00-\ufb04\ufb06\ufb20-\ufb36\ufb38-\ufb3c\ufb3e\ufb40\ufb41\ufb43\ufb44\ufb46-\ufba1\ufba4-\ufba9\ufbae-\ufbb1\ufbd3-\ufbdc\ufbde-\ufbe7\ufbea-\ufbf8\ufbfc\ufbfd\ufc00-\ufc5d\ufc64-\ufcf1\ufcf5-\ufd3d\ufd88\ufdf4\ufdfa\ufdfb\ufe71\ufe77\ufe79\ufe7b\ufe7d]+|\ufb05+")

// normalizePDFText is PDF.js normalizeUnicode: ligatures and compatibility forms fold to NFKC.
func normalizePDFText(s string) string {
	return pdfNormalizeRE.ReplaceAllStringFunc(s, func(m string) string {
		if strings.HasPrefix(m, "\ufb05") {
			return strings.Repeat("\u017ft", utf8.RuneCountInString(m))
		}
		return norm.NFKC.String(m)
	})
}

func isRTLRune(r rune) bool {
	return r >= 0x0590 && r <= 0x08ff || r >= 0xfb1d && r <= 0xfdff || r >= 0xfe70 && r <= 0xfeff
}

// bidiVisualToLogical is PDF.js's bidi(): PDF content lists glyphs left to right as drawn, so a
// right-to-left run is stored reversed; applying the display reordering once more yields the
// logical order. Text without right-to-left characters is returned as it is.
func bidiVisualToLogical(s string) string {
	rtl := false
	for _, r := range s {
		if isRTLRune(r) {
			rtl = true
			break
		}
	}
	if !rtl {
		return s
	}
	var p bidi.Paragraph
	if _, err := p.SetString(s); err != nil {
		return s
	}
	order, err := p.Order()
	if err != nil {
		return s
	}
	var out strings.Builder
	for i := range order.NumRuns() {
		run := order.Run(i)
		if run.Direction() != bidi.RightToLeft {
			out.WriteString(run.String())
			continue
		}
		runes := []rune(run.String())
		for j := len(runes) - 1; j >= 0; j-- {
			out.WriteRune(runes[j])
		}
	}
	return out.String()
}
