package markit

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	pptxExtensions = []string{".pptx"}
	pptxMIMETypes  = []string{"application/vnd.openxmlformats-officedocument.presentationml.presentation"}
	rePptxSlideNum = regexp.MustCompile(`^ppt/slides/slide(\d+)\.xml$`)
)

// PptxConverter converts OOXML PowerPoint presentations (.pptx) to Markdown.
// upstream: packages/coding-agent/src/markit/converters/pptx.ts:1-326
type PptxConverter struct{}

func (p *PptxConverter) Name() string { return "pptx" }

func (p *PptxConverter) Accepts(info StreamInfo) bool {
	return matchesExtensionOrMIME(info, pptxExtensions, pptxMIMETypes)
}

func (p *PptxConverter) Convert(ctx context.Context, input []byte, info StreamInfo) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	pkg, err := openZip(input)
	if err != nil {
		return Result{}, err
	}
	presXML, ok, err := pkg.readText("ppt/presentation.xml")
	if err != nil {
		return Result{}, err
	}
	if !ok || presXML == "" {
		return Result{}, fmt.Errorf("Invalid PPTX: missing presentation.xml")
	}
	presRoot, err := parseXML(ctx, presXML)
	if err != nil {
		return Result{}, fmt.Errorf("Invalid PPTX: %w", err)
	}

	// Build relationship map from ppt/_rels/presentation.xml.rels.
	relMap := parseSimpleRelationships(ctx, pkg, "ppt/_rels/presentation.xml.rels")

	var slidePaths []string
	if sldIdLst := presRoot.firstChild("sldIdLst"); sldIdLst != nil {
		for _, sld := range sldIdLst.childElements("sldId") {
			rID := sld.attrOr("r:id", "")
			if target, ok := relMap[rID]; ok && target != "" {
				slidePaths = append(slidePaths, resolveRelativeZipPath("ppt", target))
			}
		}
	}
	// Fallback: enumerate ppt/slides/slideN.xml sorted numerically.
	if len(slidePaths) == 0 {
		type numberedSlide struct {
			path string
			num  int
		}
		var found []numberedSlide
		for _, m := range pkg.members() {
			if match := rePptxSlideNum.FindStringSubmatch(m); len(match) == 2 {
				n, _ := strconv.Atoi(match[1])
				found = append(found, numberedSlide{path: m, num: n})
			}
		}
		sort.Slice(found, func(i, j int) bool { return found[i].num < found[j].num })
		for _, f := range found {
			slidePaths = append(slidePaths, f.path)
		}
	}

	var sections []string
	imageCount := 0
	for i, slidePath := range slidePaths {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		slideXML, ok, err := pkg.readText(slidePath)
		if err != nil {
			return Result{}, err
		}
		if !ok || slideXML == "" {
			continue
		}
		slideRoot, err := parseXML(ctx, slideXML)
		if err != nil {
			return Result{}, fmt.Errorf("Invalid PPTX slide %s: %w", slidePath, err)
		}
		cSld := slideRoot.firstChild("cSld")
		spTree := cSld.firstChild("spTree")
		if spTree == nil {
			continue
		}

		// Parse slide-level rels for image references.
		slideRelsPath := strings.Replace(slidePath, "slides/slide", "slides/_rels/slide", 1) + ".rels"
		slideRelMap := parseSimpleRelationships(ctx, pkg, slideRelsPath)

		slideLines := []string{fmt.Sprintf("<!-- Slide %d -->", i+1)}
		isTitle := true
		for _, shape := range spTree.childElements("sp") {
			text := extractPptxShapeText(shape)
			if text == "" {
				continue
			}
			if isTitle {
				slideLines = append(slideLines, "# "+text)
				isTitle = false
			} else {
				slideLines = append(slideLines, text)
			}
		}

		// Embedded images.
		slideDir := path.Dir(slidePath)
		for _, pic := range spTree.childElements("pic") {
			blipFill := pic.firstChild("blipFill")
			blip := blipFill.firstChild("blip")
			rEmbed := blip.attrOr("r:embed", "")
			if rEmbed == "" {
				continue
			}
			target, ok := slideRelMap[rEmbed]
			if !ok || target == "" {
				continue
			}
			normImg := resolveRelativeZipPath(slideDir, target)
			if !pkg.has(normImg) {
				continue
			}
			imageCount++
			name := pic.firstChild("nvSpPr").firstChild("cNvPr").attrOr("name", "")
			if name == "" {
				name = pic.firstChild("nvPicPr").firstChild("cNvPr").attrOr("name", "")
			}
			if name == "" {
				name = fmt.Sprintf("image_%d", imageCount)
			}
			if info.ImageDir != "" {
				safeExt := sanitizeImageExt(path.Ext(normImg))
				data, found, readErr := pkg.readBytes(normImg)
				if readErr == nil && found {
					if mkErr := os.MkdirAll(info.ImageDir, 0o755); mkErr == nil {
						outPath := filepath.Join(info.ImageDir, fmt.Sprintf("slide%d_%d.%s", i+1, imageCount, safeExt))
						if writeErr := os.WriteFile(outPath, data, 0o644); writeErr == nil {
							slideLines = append(slideLines, fmt.Sprintf("![%s](%s)", name, outPath))
							continue
						}
					}
				}
			}
			slideLines = append(slideLines, fmt.Sprintf("<!-- image: %s (slide %d) -->", name, i+1))
		}

		// Tables inside graphicFrame elements.
		for _, gf := range spTree.childElements("graphicFrame") {
			if tbl := extractPptxTable(gf); tbl != "" {
				slideLines = append(slideLines, tbl)
			}
		}

		// Slide notes.
		noteFile := strings.Replace(slidePath, "slides/slide", "notesSlides/notesSlide", 1)
		if noteXML, hasNote, noteErr := pkg.readText(noteFile); noteErr == nil && hasNote && noteXML != "" {
			if noteRoot, parseErr := parseXML(ctx, noteXML); parseErr == nil {
				noteSpTree := noteRoot.firstChild("cSld").firstChild("spTree")
				if noteSpTree != nil {
					var noteTexts []string
					for _, ns := range noteSpTree.childElements("sp") {
						phType := ns.firstChild("nvSpPr").firstChild("nvPr").firstChild("ph").attrOr("type", "")
						if phType == "sldImg" {
							continue
						}
						if t := extractPptxShapeText(ns); t != "" {
							noteTexts = append(noteTexts, t)
						}
					}
					if len(noteTexts) > 0 {
						slideLines = append(slideLines, "\n### Notes:")
						slideLines = append(slideLines, strings.Join(noteTexts, "\n"))
					}
				}
			}
		}

		sections = append(sections, strings.Join(slideLines, "\n"))
	}

	return Result{Markdown: strings.TrimSpace(strings.Join(sections, "\n\n"))}, nil
}

func parseSimpleRelationships(ctx context.Context, pkg *zipPackage, relsPath string) map[string]string {
	out := make(map[string]string)
	xmlText, ok, err := pkg.readText(relsPath)
	if err != nil || !ok || xmlText == "" {
		return out
	}
	root, err := parseXML(ctx, xmlText)
	if err != nil {
		return out
	}
	for _, r := range root.childElements("Relationship") {
		id := r.attrOr("Id", "")
		target := r.attrOr("Target", "")
		if id != "" && target != "" {
			out[id] = target
		}
	}
	return out
}

// extractPptxShapeText extracts paragraph runs from a PPTX shape's txBody.
// upstream: packages/coding-agent/src/markit/converters/pptx.ts:259-276
func extractPptxShapeText(shape *xmlNode) string {
	txBody := shape.firstChild("txBody")
	if txBody == nil {
		return ""
	}
	var lines []string
	for _, p := range txBody.childElements("p") {
		var parts []string
		for _, r := range p.childElements("r") {
			if t := r.firstChild("t"); t != nil {
				parts = append(parts, t.textContent())
			}
		}
		if len(parts) > 0 {
			lines = append(lines, strings.Join(parts, ""))
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// extractPptxTable converts an a:tbl inside a p:graphicFrame into a Markdown pipe table.
// upstream: packages/coding-agent/src/markit/converters/pptx.ts:278-323
func extractPptxTable(gf *xmlNode) string {
	tbl := gf.firstChild("graphic").firstChild("graphicData").firstChild("tbl")
	if tbl == nil {
		return ""
	}
	trList := tbl.childElements("tr")
	if len(trList) == 0 {
		return ""
	}
	var mdRows [][]string
	for _, tr := range trList {
		var cellTexts []string
		for _, tc := range tr.childElements("tc") {
			txBody := tc.firstChild("txBody")
			if txBody == nil {
				cellTexts = append(cellTexts, "")
				continue
			}
			var parts []string
			for _, p := range txBody.childElements("p") {
				for _, r := range p.childElements("r") {
					if t := r.firstChild("t"); t != nil {
						parts = append(parts, t.textContent())
					}
				}
			}
			cellTexts = append(cellTexts, strings.Join(parts, " "))
		}
		mdRows = append(mdRows, cellTexts)
	}
	return formatMarkdownTable(mdRows)
}
