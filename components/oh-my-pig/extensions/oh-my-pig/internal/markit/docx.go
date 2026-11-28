package markit

import (
	"context"
	"fmt"
	"html"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	docxExtensions = []string{".docx"}
	docxMIMETypes  = []string{"application/vnd.openxmlformats-officedocument.wordprocessingml.document"}
	reDocxHeading  = regexp.MustCompile(`(?i)^(?:heading\s*|heading)([1-6])$`)
	reDataURIImg   = regexp.MustCompile(`!\[([^\]]*)\]\(data:[^)]*\)`)
)

// DocxConverter converts OOXML Word documents (.docx) to Markdown.
// upstream: packages/coding-agent/src/markit/converters/docx.ts:1-51
// upstream: packages/utils/src/docx/converter.ts:1-695
type DocxConverter struct{}

func (d *DocxConverter) Name() string { return "docx" }

func (d *DocxConverter) Accepts(info StreamInfo) bool {
	return matchesExtensionOrMIME(info, docxExtensions, docxMIMETypes)
}

func (d *DocxConverter) Convert(ctx context.Context, input []byte, info StreamInfo) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	pkg, err := openZip(input)
	if err != nil {
		return Result{}, err
	}
	docXML, ok, err := pkg.readText("word/document.xml")
	if err != nil {
		return Result{}, err
	}
	if !ok || docXML == "" {
		return Result{}, fmt.Errorf("Invalid DOCX: missing word/document.xml")
	}
	docRoot, err := parseXML(ctx, docXML)
	if err != nil {
		return Result{}, fmt.Errorf("Invalid DOCX: %w", err)
	}
	body := docRoot.firstChild("body")
	if body == nil {
		return Result{}, fmt.Errorf("Invalid DOCX: missing document body")
	}

	relsXML, _, err := pkg.readText("word/_rels/document.xml.rels")
	if err != nil {
		return Result{}, err
	}
	contentTypesXML, _, err := pkg.readText("[Content_Types].xml")
	if err != nil {
		return Result{}, err
	}
	stylesXML, _, err := pkg.readText("word/styles.xml")
	if err != nil {
		return Result{}, err
	}
	numberingXML, _, err := pkg.readText("word/numbering.xml")
	if err != nil {
		return Result{}, err
	}
	footnotesXML, _, err := pkg.readText("word/footnotes.xml")
	if err != nil {
		return Result{}, err
	}

	dctx := &docxContext{
		ctx:              ctx,
		pkg:              pkg,
		relationships:    parseDocxRelationships(ctx, relsXML),
		contentTypes:     parseDocxContentTypes(ctx, contentTypesXML),
		styles:           parseDocxStyles(ctx, stylesXML),
		numbering:        parseDocxNumbering(ctx, numberingXML),
		footnotes:        parseDocxFootnotes(ctx, footnotesXML),
		footnoteOrdinals: make(map[string]int),
		imageDir:         info.ImageDir,
	}

	blocks, err := dctx.parseBlocks(body.childElements(""))
	if err != nil {
		return Result{}, err
	}
	htmlBody, err := dctx.renderBlocks(blocks)
	if err != nil {
		return Result{}, err
	}
	fnHTML, err := dctx.renderFootnotes()
	if err != nil {
		return Result{}, err
	}
	fullHTML := htmlBody + fnHTML

	md, err := htmlToMarkdown(ctx, fullHTML)
	if err != nil {
		return Result{}, err
	}
	if info.ImageDir == "" {
		md = reDataURIImg.ReplaceAllString(md, "<!-- image: $1 -->")
	}
	return Result{Markdown: strings.TrimSpace(md)}, nil
}

type docxRelationship struct {
	target   string
	relType  string
	external bool
}

type docxFormatting struct {
	bold     *bool
	italic   *bool
	strike   *bool
	vertical string // "subscript" | "superscript" | ""
}

type docxNumRef struct {
	id    string
	level int
}

type docxStyle struct {
	id         string
	styleType  string
	name       string
	basedOn    string
	formatting docxFormatting
	numbering  *docxNumRef
}

type docxNumLevel struct {
	ordered bool
}

type docxParagraphBlock struct {
	html    string
	tag     string
	hasList bool
	level   int
	ordered bool
}

type docxBlock struct {
	isHTML bool
	html   string
	para   docxParagraphBlock
}

type docxUsedFootnote struct {
	id      string
	ordinal int
}

type docxContext struct {
	ctx              context.Context
	pkg              *zipPackage
	relationships    map[string]docxRelationship
	contentTypes     map[string]string
	styles           map[string]docxStyle
	numbering        map[string]map[int]docxNumLevel
	footnotes        map[string]*xmlNode
	usedFootnotes    []docxUsedFootnote
	footnoteOrdinals map[string]int
	imageDir         string
	imageCount       int
	stepCount        int
}

func (c *docxContext) checkCtx() error {
	c.stepCount++
	if c.stepCount&1023 == 0 {
		return c.ctx.Err()
	}
	return nil
}

func docxBoolProp(el *xmlNode) *bool {
	if el == nil {
		return nil
	}
	val := strings.ToLower(el.attrOr("w:val", ""))
	on := val != "0" && val != "false" && val != "off" && val != "none"
	return &on
}

func docxFormattingFromProps(props *xmlNode) docxFormatting {
	if props == nil {
		return docxFormatting{}
	}
	strike := docxBoolProp(props.firstChild("strike"))
	if strike == nil {
		strike = docxBoolProp(props.firstChild("dstrike"))
	}
	vert := props.firstChild("vertAlign").attrOr("w:val", "")
	if vert != "subscript" && vert != "superscript" {
		vert = ""
	}
	return docxFormatting{
		bold:     docxBoolProp(props.firstChild("b")),
		italic:   docxBoolProp(props.firstChild("i")),
		strike:   strike,
		vertical: vert,
	}
}

func mergeDocxFormatting(base, override docxFormatting) docxFormatting {
	out := base
	if override.bold != nil {
		out.bold = override.bold
	}
	if override.italic != nil {
		out.italic = override.italic
	}
	if override.strike != nil {
		out.strike = override.strike
	}
	if override.vertical != "" {
		out.vertical = override.vertical
	}
	return out
}

func parseDocxNumRef(props *xmlNode) *docxNumRef {
	if props == nil {
		return nil
	}
	numPr := props.firstChild("numPr")
	if numPr == nil {
		return nil
	}
	id := numPr.firstChild("numId").attrOr("w:val", "")
	if id == "" || id == "0" {
		return nil
	}
	lvlStr := numPr.firstChild("ilvl").attrOr("w:val", "0")
	lvl, _ := strconv.Atoi(lvlStr)
	return &docxNumRef{id: id, level: lvl}
}

func parseDocxRelationships(ctx context.Context, xmlText string) map[string]docxRelationship {
	rels := make(map[string]docxRelationship)
	if xmlText == "" {
		return rels
	}
	root, err := parseXML(ctx, xmlText)
	if err != nil {
		return rels
	}
	for _, el := range root.childElements("Relationship") {
		id := el.attrOr("Id", "")
		target := el.attrOr("Target", "")
		if id == "" || target == "" {
			continue
		}
		rels[id] = docxRelationship{
			target:   target,
			relType:  el.attrOr("Type", ""),
			external: el.attrOr("TargetMode", "") == "External",
		}
	}
	return rels
}

func parseDocxContentTypes(ctx context.Context, xmlText string) map[string]string {
	types := make(map[string]string)
	if xmlText == "" {
		return types
	}
	root, err := parseXML(ctx, xmlText)
	if err != nil {
		return types
	}
	for _, el := range root.childElements("") {
		switch el.Name.Local {
		case "Default":
			ext := strings.ToLower(el.attrOr("Extension", ""))
			ct := el.attrOr("ContentType", "")
			if ext != "" && ct != "" {
				types["."+ext] = ct
			}
		case "Override":
			part := strings.TrimPrefix(el.attrOr("PartName", ""), "/")
			ct := el.attrOr("ContentType", "")
			if part != "" && ct != "" {
				types[part] = ct
			}
		}
	}
	return types
}

func parseDocxStyles(ctx context.Context, xmlText string) map[string]docxStyle {
	styles := make(map[string]docxStyle)
	if xmlText == "" {
		return styles
	}
	root, err := parseXML(ctx, xmlText)
	if err != nil {
		return styles
	}
	for _, el := range root.childElements("style") {
		id := el.attrOr("w:styleId", "")
		stType := el.attrOr("w:type", "")
		if id == "" || (stType != "paragraph" && stType != "character") {
			continue
		}
		name := el.firstChild("name").attrOr("w:val", id)
		styles[id] = docxStyle{
			id:         id,
			styleType:  stType,
			name:       name,
			basedOn:    el.firstChild("basedOn").attrOr("w:val", ""),
			formatting: docxFormattingFromProps(el.firstChild("rPr")),
			numbering:  parseDocxNumRef(el.firstChild("pPr")),
		}
	}
	return styles
}

func parseDocxNumbering(ctx context.Context, xmlText string) map[string]map[int]docxNumLevel {
	out := make(map[string]map[int]docxNumLevel)
	if xmlText == "" {
		return out
	}
	root, err := parseXML(ctx, xmlText)
	if err != nil {
		return out
	}
	abstracts := make(map[string]map[int]docxNumLevel)
	for _, abs := range root.childElements("abstractNum") {
		id := abs.attrOr("w:abstractNumId", "")
		if id == "" {
			continue
		}
		lvls := make(map[int]docxNumLevel)
		for _, lvl := range abs.childElements("lvl") {
			idx, _ := strconv.Atoi(lvl.attrOr("w:ilvl", "0"))
			fmtVal := lvl.firstChild("numFmt").attrOr("w:val", "")
			lvls[idx] = docxNumLevel{ordered: fmtVal != "bullet"}
		}
		abstracts[id] = lvls
	}
	for _, num := range root.childElements("num") {
		id := num.attrOr("w:numId", "")
		absID := num.firstChild("abstractNumId").attrOr("w:val", "")
		if id == "" || absID == "" {
			continue
		}
		if lvls, ok := abstracts[absID]; ok {
			out[id] = lvls
		} else {
			out[id] = make(map[int]docxNumLevel)
		}
	}
	return out
}

func parseDocxFootnotes(ctx context.Context, xmlText string) map[string]*xmlNode {
	notes := make(map[string]*xmlNode)
	if xmlText == "" {
		return notes
	}
	root, err := parseXML(ctx, xmlText)
	if err != nil {
		return notes
	}
	for _, el := range root.childElements("footnote") {
		id := el.attrOr("w:id", "")
		if id != "" && !strings.HasPrefix(id, "-") {
			notes[id] = el
		}
	}
	return notes
}

func (c *docxContext) resolveStyleFormatting(styleID string, visited map[string]bool) docxFormatting {
	if styleID == "" || visited[styleID] {
		return docxFormatting{}
	}
	st, ok := c.styles[styleID]
	if !ok {
		return docxFormatting{}
	}
	visited[styleID] = true
	base := docxFormatting{}
	if st.basedOn != "" {
		base = c.resolveStyleFormatting(st.basedOn, visited)
	}
	return mergeDocxFormatting(base, st.formatting)
}

func (c *docxContext) resolveStyleNumbering(styleID string, visited map[string]bool) *docxNumRef {
	if styleID == "" || visited[styleID] {
		return nil
	}
	st, ok := c.styles[styleID]
	if !ok {
		return nil
	}
	visited[styleID] = true
	if st.numbering != nil {
		return st.numbering
	}
	if st.basedOn != "" {
		return c.resolveStyleNumbering(st.basedOn, visited)
	}
	return nil
}

func (c *docxContext) paragraphTag(styleID string) string {
	if styleID == "" {
		return "p"
	}
	name := styleID
	if st, ok := c.styles[styleID]; ok && st.name != "" {
		name = st.name
	}
	if m := reDocxHeading.FindStringSubmatch(name); len(m) == 2 {
		return "h" + m[1]
	}
	return "p"
}

func (c *docxContext) parseBlocks(elements []*xmlNode) ([]docxBlock, error) {
	var blocks []docxBlock
	for _, el := range elements {
		if err := c.checkCtx(); err != nil {
			return nil, err
		}
		switch el.Name.Local {
		case "p":
			p, ok, err := c.parseParagraph(el)
			if err != nil {
				return nil, err
			}
			if ok {
				blocks = append(blocks, docxBlock{para: p})
			}
		case "tbl":
			tblHTML, err := c.renderTable(el)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, docxBlock{isHTML: true, html: tblHTML})
		case "sdt", "customXml", "sdtContent":
			inner, err := c.parseBlocks(el.childElements(""))
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, inner...)
		}
	}
	return blocks, nil
}

func (c *docxContext) parseParagraph(el *xmlNode) (docxParagraphBlock, bool, error) {
	pPr := el.firstChild("pPr")
	styleID := pPr.firstChild("pStyle").attrOr("w:val", "")
	tag := c.paragraphTag(styleID)

	numRef := parseDocxNumRef(pPr)
	if numRef == nil && styleID != "" {
		numRef = c.resolveStyleNumbering(styleID, make(map[string]bool))
	}

	inlineHTML, err := c.renderInlineChildren(el)
	if err != nil {
		return docxParagraphBlock{}, false, err
	}
	if inlineHTML == "" && numRef == nil {
		return docxParagraphBlock{}, false, nil
	}

	pb := docxParagraphBlock{
		html: inlineHTML,
		tag:  tag,
	}
	if numRef != nil {
		pb.hasList = true
		pb.level = numRef.level
		pb.ordered = true
		if lvls, ok := c.numbering[numRef.id]; ok {
			if lvl, ok := lvls[numRef.level]; ok {
				pb.ordered = lvl.ordered
			}
		}
	}
	return pb, true, nil
}

func (c *docxContext) renderInlineChildren(el *xmlNode) (string, error) {
	var b strings.Builder
	for _, child := range el.Children {
		if child.IsText {
			continue
		}
		if err := c.checkCtx(); err != nil {
			return "", err
		}
		switch child.Name.Local {
		case "r":
			s, err := c.renderRun(child)
			if err != nil {
				return "", err
			}
			b.WriteString(s)
		case "hyperlink":
			contents, err := c.renderInlineChildren(child)
			if err != nil {
				return "", err
			}
			relID := child.attrOr("r:id", "")
			anchor := child.attrOr("w:anchor", "")
			target := ""
			if relID != "" {
				if rel, ok := c.relationships[relID]; ok {
					target = rel.target
				}
			} else if anchor != "" {
				target = "#" + anchor
			}
			if target != "" {
				fmt.Fprintf(&b, `<a href="%s">%s</a>`, html.EscapeString(target), contents)
			} else {
				b.WriteString(contents)
			}
		case "del", "pPr", "rPr":
			// Skip deleted revisions and property blocks.
		default:
			s, err := c.renderInlineChildren(child)
			if err != nil {
				return "", err
			}
			b.WriteString(s)
		}
	}
	return b.String(), nil
}

func (c *docxContext) renderRun(run *xmlNode) (string, error) {
	rPr := run.firstChild("rPr")
	styleID := rPr.firstChild("rStyle").attrOr("w:val", "")
	formatting := mergeDocxFormatting(
		c.resolveStyleFormatting(styleID, make(map[string]bool)),
		docxFormattingFromProps(rPr),
	)
	if styleID != "" {
		stName := styleID
		if st, ok := c.styles[styleID]; ok && st.name != "" {
			stName = st.name
		}
		if strings.EqualFold(stName, "strong") {
			t := true
			formatting.bold = &t
		}
	}

	var b strings.Builder
	for _, child := range run.childElements("") {
		switch child.Name.Local {
		case "t", "instrText":
			for _, tn := range child.Children {
				if tn.IsText {
					b.WriteString(html.EscapeString(tn.Text))
				}
			}
		case "tab":
			b.WriteByte('\t')
		case "br":
			if child.attrOr("w:type", "") != "page" {
				b.WriteString("<br />")
			}
		case "noBreakHyphen":
			b.WriteString("\u2011")
		case "softHyphen":
			b.WriteString("\u00ad")
		case "drawing", "pict":
			imgHTML, err := c.renderImage(child)
			if err != nil {
				return "", err
			}
			b.WriteString(imgHTML)
		case "footnoteReference":
			b.WriteString(c.renderFootnoteRef(child))
		}
	}
	return applyDocxFormatting(b.String(), formatting), nil
}

func applyDocxFormatting(s string, f docxFormatting) string {
	if s == "" {
		return ""
	}
	out := s
	if f.strike != nil && *f.strike {
		out = "<s>" + out + "</s>"
	}
	if f.vertical == "subscript" {
		out = "<sub>" + out + "</sub>"
	} else if f.vertical == "superscript" {
		out = "<sup>" + out + "</sup>"
	}
	if f.italic != nil && *f.italic {
		out = "<em>" + out + "</em>"
	}
	if f.bold != nil && *f.bold {
		out = "<strong>" + out + "</strong>"
	}
	return out
}

func (c *docxContext) renderImage(el *xmlNode) (string, error) {
	blips := el.descendants("blip")
	if len(blips) == 0 {
		return "", nil
	}
	relID := blips[0].attrOr("r:embed", "")
	rel, ok := c.relationships[relID]
	if !ok || rel.external {
		return "", nil
	}
	memberPath := resolveRelativeZipPath("word", rel.target)
	if !c.pkg.has(memberPath) {
		return "", nil
	}
	c.imageCount++
	alt := fmt.Sprintf("image_%d", c.imageCount)
	ext := strings.TrimPrefix(strings.ToLower(path.Ext(memberPath)), ".")
	ct := c.contentTypes[memberPath]
	if ct == "" && ext != "" {
		ct = c.contentTypes["."+ext]
	}
	if ct == "" {
		switch ext {
		case "jpg", "jpeg":
			ct = "image/jpeg"
		case "gif":
			ct = "image/gif"
		case "webp":
			ct = "image/webp"
		case "svg":
			ct = "image/svg+xml"
		default:
			ct = "image/png"
		}
	}
	if c.imageDir != "" {
		rawExt := ext
		if rawExt == "" {
			if parts := strings.Split(ct, "/"); len(parts) == 2 {
				rawExt = parts[1]
			}
		}
		safeExt := sanitizeImageExt(rawExt)
		data, found, err := c.pkg.readBytes(memberPath)
		if err != nil {
			return "", err
		}
		if !found {
			return "", nil
		}
		if err := os.MkdirAll(c.imageDir, 0o755); err != nil {
			return "", err
		}
		outPath := filepath.Join(c.imageDir, fmt.Sprintf("image_%d.%s", c.imageCount, safeExt))
		if err := os.WriteFile(outPath, data, 0o644); err != nil {
			return "", err
		}
		return fmt.Sprintf(`<img alt="%s" src="%s" />`, html.EscapeString(alt), html.EscapeString(outPath)), nil
	}
	return fmt.Sprintf(`<img alt="%s" src="data:%s;base64," />`, html.EscapeString(alt), html.EscapeString(ct)), nil
}

func (c *docxContext) renderFootnoteRef(el *xmlNode) string {
	id := el.attrOr("w:id", "")
	if id == "" {
		return ""
	}
	if _, ok := c.footnotes[id]; !ok {
		return ""
	}
	ord, exists := c.footnoteOrdinals[id]
	if !exists {
		ord = len(c.usedFootnotes) + 1
		c.footnoteOrdinals[id] = ord
		c.usedFootnotes = append(c.usedFootnotes, docxUsedFootnote{id: id, ordinal: ord})
	}
	escID := html.EscapeString(id)
	return fmt.Sprintf(`<sup><a href="#footnote-%s" id="footnote-ref-%s">[%d]</a></sup>`, escID, escID, ord)
}

func (c *docxContext) renderFootnotes() (string, error) {
	if len(c.usedFootnotes) == 0 {
		return "", nil
	}
	var b strings.Builder
	b.WriteString("<ol>")
	for _, fn := range c.usedFootnotes {
		el := c.footnotes[fn.id]
		if el == nil {
			continue
		}
		blocks, err := c.parseBlocks(el.childElements(""))
		if err != nil {
			return "", err
		}
		contents, err := c.renderBlocks(blocks)
		if err != nil {
			return "", err
		}
		escID := html.EscapeString(fn.id)
		backlink := fmt.Sprintf(`<a href="#footnote-ref-%s">↑</a>`, escID)
		if idx := strings.LastIndex(contents, "</p>"); idx >= 0 {
			contents = contents[:idx] + " " + backlink + contents[idx:]
		} else {
			contents += backlink
		}
		fmt.Fprintf(&b, `<li id="footnote-%s">%s</li>`, escID, contents)
	}
	b.WriteString("</ol>")
	return b.String(), nil
}

func (c *docxContext) renderTable(tbl *xmlNode) (string, error) {
	var b strings.Builder
	b.WriteString("<table>")
	for _, tr := range tbl.childElements("tr") {
		b.WriteString("<tr>")
		for _, tc := range tr.childElements("tc") {
			tcPr := tc.firstChild("tcPr")
			if vMerge := tcPr.firstChild("vMerge"); vMerge != nil && vMerge.attrOr("w:val", "") != "restart" {
				continue
			}
			blocks, err := c.parseBlocks(tc.childElements(""))
			if err != nil {
				return "", err
			}
			cellHTML, err := c.renderBlocks(blocks)
			if err != nil {
				return "", err
			}
			b.WriteString("<td>")
			b.WriteString(cellHTML)
			b.WriteString("</td>")
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</table>")
	return b.String(), nil
}

func (c *docxContext) renderBlocks(blocks []docxBlock) (string, error) {
	var b strings.Builder
	for i := 0; i < len(blocks); {
		if err := c.checkCtx(); err != nil {
			return "", err
		}
		blk := blocks[i]
		if blk.isHTML {
			b.WriteString(blk.html)
			i++
			continue
		}
		if !blk.para.hasList {
			fmt.Fprintf(&b, "<%s>%s</%s>", blk.para.tag, blk.para.html, blk.para.tag)
			i++
			continue
		}
		var listItems []docxParagraphBlock
		for i < len(blocks) && !blocks[i].isHTML && blocks[i].para.hasList {
			listItems = append(listItems, blocks[i].para)
			i++
		}
		for idx := 0; idx < len(listItems); {
			next, err := c.renderList(&b, listItems, idx, listItems[idx].level, 1)
			if err != nil {
				return "", err
			}
			idx = next
		}
	}
	return b.String(), nil
}

func (c *docxContext) renderList(b *strings.Builder, items []docxParagraphBlock, start, level, depth int) (int, error) {
	if depth > MaxXMLNestingDepth {
		return 0, fmt.Errorf("DOCX list nesting depth exceeds limit (%d)", MaxXMLNestingDepth)
	}
	ordered := items[start].ordered
	tag := "ul"
	if ordered {
		tag = "ol"
	}
	fmt.Fprintf(b, "<%s>", tag)
	i := start
	for i < len(items) {
		if err := c.checkCtx(); err != nil {
			return 0, err
		}
		item := items[i]
		if item.level < level || (item.level == level && item.ordered != ordered) {
			break
		}
		if item.level > level {
			break
		}
		b.WriteString("<li>")
		b.WriteString(item.html)
		i++
		for i < len(items) && items[i].level > level {
			next, err := c.renderList(b, items, i, items[i].level, depth+1)
			if err != nil {
				return 0, err
			}
			i = next
		}
		b.WriteString("</li>")
	}
	fmt.Fprintf(b, "</%s>", tag)
	return i, nil
}
