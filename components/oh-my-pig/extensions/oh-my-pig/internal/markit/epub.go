package markit

import (
	"context"
	"fmt"
	"path"
	"strings"
)

var (
	epubExtensions         = []string{".epub"}
	epubMIMETypes          = []string{"application/epub", "application/epub+zip", "application/x-epub+zip"}
	maxMarkdownOutputBytes = MaxInMemorySize
)

// EpubConverter converts EPUB e-books (.epub) to Markdown.
// upstream: packages/coding-agent/src/markit/converters/epub.ts:1-137
type EpubConverter struct{}

func (e *EpubConverter) Name() string { return "epub" }

func (e *EpubConverter) Accepts(info StreamInfo) bool {
	return matchesExtensionOrMIME(info, epubExtensions, epubMIMETypes)
}

func (e *EpubConverter) Convert(ctx context.Context, input []byte, _ StreamInfo) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	pkg, err := openZip(input)
	if err != nil {
		return Result{}, err
	}

	// Find OPF path from META-INF/container.xml.
	containerXML, ok, err := pkg.readText("META-INF/container.xml")
	if err != nil {
		return Result{}, err
	}
	if !ok || containerXML == "" {
		return Result{}, fmt.Errorf("Invalid EPUB: missing container.xml")
	}
	containerRoot, err := parseXML(ctx, containerXML)
	if err != nil {
		return Result{}, fmt.Errorf("Invalid EPUB: %w", err)
	}
	rootfiles := containerRoot.firstChild("rootfiles").childElements("rootfile")
	opfPath := ""
	if len(rootfiles) > 0 {
		opfPath = rootfiles[0].attrOr("full-path", "")
	}
	if opfPath == "" {
		return Result{}, fmt.Errorf("Invalid EPUB: missing rootfile path")
	}

	// Parse content.opf.
	opfXML, ok, err := pkg.readText(opfPath)
	if err != nil {
		return Result{}, err
	}
	if !ok || opfXML == "" {
		return Result{}, fmt.Errorf("Invalid EPUB: missing content.opf")
	}
	opfRoot, err := parseXML(ctx, opfXML)
	if err != nil {
		return Result{}, fmt.Errorf("Invalid EPUB: %w", err)
	}

	// Extract Dublin Core metadata in deterministic upstream field order.
	metaNode := opfRoot.firstChild("metadata")
	title := epubMetaSingle(metaNode, "title")
	authors := strings.Join(epubMetaList(metaNode, "creator"), ", ")
	language := epubMetaSingle(metaNode, "language")
	publisher := epubMetaSingle(metaNode, "publisher")
	date := epubMetaSingle(metaNode, "date")
	description := epubMetaSingle(metaNode, "description")

	var sections []string
	type metaField struct {
		label string
		value string
	}
	metaFields := []metaField{
		{"Title", title},
		{"Authors", authors},
		{"Language", language},
		{"Publisher", publisher},
		{"Date", date},
		{"Description", description},
	}
	var metaLines []string
	for _, mf := range metaFields {
		if mf.value != "" {
			metaLines = append(metaLines, fmt.Sprintf("**%s:** %s", mf.label, mf.value))
		}
	}
	if len(metaLines) > 0 {
		sections = append(sections, strings.Join(metaLines, "\n"))
	}

	// Build manifest map (id -> href).
	manifest := make(map[string]string)
	for _, item := range opfRoot.firstChild("manifest").childElements("item") {
		id := item.attrOr("id", "")
		href := item.attrOr("href", "")
		if id != "" && href != "" {
			manifest[id] = href
		}
	}

	// Walk spine order and convert each XHTML/HTML chapter, capping cumulative
	// output at MaxInMemorySize so repeated spine references cannot amplify RAM.
	baseDir := ""
	if strings.Contains(opfPath, "/") {
		baseDir = path.Dir(opfPath)
	}
	totalBytes := 0
	for _, s := range sections {
		totalBytes += len(s)
	}
	for _, itemref := range opfRoot.firstChild("spine").childElements("itemref") {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		idref := itemref.attrOr("idref", "")
		href, ok := manifest[idref]
		if !ok || href == "" {
			continue
		}
		chapterPath := resolveRelativeZipPath(baseDir, href)
		chapterHTML, found, err := pkg.readText(chapterPath)
		if err != nil {
			return Result{}, err
		}
		if !found || chapterHTML == "" {
			continue
		}
		md, err := htmlToMarkdown(ctx, chapterHTML)
		if err != nil {
			return Result{}, err
		}
		md = strings.TrimSpace(md)
		if md != "" {
			addLen := len(md)
			if len(sections) > 0 {
				addLen += 2
			}
			if totalBytes+addLen > maxMarkdownOutputBytes {
				return Result{}, fmt.Errorf("EPUB converted markdown exceeds %d bytes limit", maxMarkdownOutputBytes)
			}
			totalBytes += addLen
			sections = append(sections, md)
		}
	}

	return Result{
		Markdown: strings.TrimSpace(strings.Join(sections, "\n\n")),
		Title:    title,
	}, nil
}

func epubMetaSingle(meta *xmlNode, local string) string {
	if meta == nil {
		return ""
	}
	if child := meta.firstChild(local); child != nil {
		return strings.TrimSpace(child.textContent())
	}
	return ""
}

func epubMetaList(meta *xmlNode, local string) []string {
	if meta == nil {
		return nil
	}
	var out []string
	for _, child := range meta.childElements(local) {
		if t := strings.TrimSpace(child.textContent()); t != "" {
			out = append(out, t)
		}
	}
	return out
}
