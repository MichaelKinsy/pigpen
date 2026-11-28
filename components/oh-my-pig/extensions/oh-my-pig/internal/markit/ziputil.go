package markit

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
)

// MaxXMLNestingDepth bounds XML and HTML element nesting to prevent stack
// overflow / quadratic DoS on adversarial inputs.
const MaxXMLNestingDepth = 512

// zipPackage wraps a ZIP archive in memory with bounded extraction and path normalization.
// Each distinct member is counted once toward MaxTotalInflatedBytes, matching ZipPackage.
// upstream: packages/utils/src/ar/zip-package.ts:1-57, packages/utils/src/ar/limits.ts:1-91
type zipPackage struct {
	files         map[string]*zip.File
	order         []string
	counted       map[string]bool
	totalInflated int64
}

func openZip(data []byte) (*zipPackage, error) {
	if len(data) > MaxInMemorySize {
		return nil, fmt.Errorf("archive is too large to read in memory (%d bytes > %d bytes limit)", len(data), MaxInMemorySize)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("invalid ZIP archive: %w", err)
	}
	if len(zr.File) > MaxEntries {
		return nil, fmt.Errorf("archive has too many entries (> %d limit)", MaxEntries)
	}
	pkg := &zipPackage{
		files:   make(map[string]*zip.File, len(zr.File)),
		order:   make([]string, 0, len(zr.File)),
		counted: make(map[string]bool),
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/") {
			continue
		}
		norm := normalizeZipMember(f.Name)
		if norm == "" {
			continue
		}
		if _, exists := pkg.files[norm]; !exists {
			pkg.order = append(pkg.order, norm)
		}
		pkg.files[norm] = f
	}
	return pkg, nil
}

func normalizeZipMember(name string) string {
	cleaned := strings.ReplaceAll(name, "\\", "/")
	cleaned = strings.TrimPrefix(path.Clean("/"+cleaned), "/")
	if cleaned == "." {
		return ""
	}
	return cleaned
}

func (z *zipPackage) has(name string) bool {
	_, ok := z.files[normalizeZipMember(name)]
	return ok
}

func (z *zipPackage) members() []string {
	return z.order
}

func (z *zipPackage) readBytes(name string) ([]byte, bool, error) {
	norm := normalizeZipMember(name)
	f, ok := z.files[norm]
	if !ok {
		return nil, false, nil
	}
	if f.UncompressedSize64 > MaxMemberSize {
		return nil, true, fmt.Errorf("archive member '%s' exceeds %d bytes limit", norm, MaxMemberSize)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, true, err
	}
	defer rc.Close()

	alreadyCounted := z.counted[norm]
	capBytes := int64(MaxMemberSize)
	if !alreadyCounted {
		remaining := int64(MaxTotalInflatedBytes) - z.totalInflated
		if remaining < 0 {
			remaining = 0
		}
		if remaining < capBytes {
			capBytes = remaining
		}
	}

	data, err := io.ReadAll(io.LimitReader(rc, capBytes+1))
	if err != nil {
		return nil, true, err
	}
	if int64(len(data)) > capBytes {
		return nil, true, fmt.Errorf("archive member '%s' exceeds memory extraction limit", norm)
	}
	if !alreadyCounted {
		z.counted[norm] = true
		z.totalInflated += int64(len(data))
	}
	return data, true, nil
}

func (z *zipPackage) readText(name string) (string, bool, error) {
	data, ok, err := z.readBytes(name)
	if !ok || err != nil {
		return "", ok, err
	}
	// Strip UTF-8 BOM if present.
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	return string(data), true, nil
}

// xmlNode is a minimal namespace-aware DOM tree built with encoding/xml.Decoder.
// Go's encoding/xml does not expand custom DTD entities, avoiding XXE/billion-laughs.
// upstream: packages/utils/src/docx/xml.ts:1-166
type xmlNode struct {
	Name     xml.Name
	Attrs    []xml.Attr
	Text     string
	IsText   bool
	Children []*xmlNode
}

// parseXML decodes XML into an xmlNode tree while enforcing MaxXMLNestingDepth
// and periodic ctx cancellation checks.
func parseXML(ctx context.Context, src string) (*xmlNode, error) {
	dec := xml.NewDecoder(strings.NewReader(src))
	dec.Strict = true
	var stack []*xmlNode
	var root *xmlNode
	tokenCount := 0

	for {
		tokenCount++
		if tokenCount&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if len(stack) >= MaxXMLNestingDepth {
				return nil, fmt.Errorf("XML nesting depth exceeds limit (%d)", MaxXMLNestingDepth)
			}
			node := &xmlNode{
				Name:  t.Name,
				Attrs: append([]xml.Attr(nil), t.Attr...),
			}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, node)
			} else if root == nil {
				root = node
			}
			stack = append(stack, node)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if len(stack) > 0 && len(t) > 0 {
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, &xmlNode{
					IsText: true,
					Text:   string(t),
				})
			}
		}
	}
	if root == nil {
		return nil, fmt.Errorf("empty XML document")
	}
	return root, nil
}

// Well-known OOXML namespace URIs / prefixes.
const (
	nsOfficeRelationships = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	nsWordprocessingML    = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
)

// attr looks up an attribute by qualified name ("r:id", "w:val") or local name ("Id").
// When a prefix is given (e.g. "r:id" or "w:val"), exact namespace match is preferred
// before falling back to local-name match — preventing unprefixed attributes like
// `<p:sldId id="256" r:id="rId3"/>` from shadowing `r:id`.
// upstream: packages/utils/src/docx/xml.ts:148-156
func (n *xmlNode) attr(name string) (string, bool) {
	if n == nil {
		return "", false
	}
	prefix, local, hasPrefix := strings.Cut(name, ":")
	if hasPrefix {
		// 1. Prefer matching namespace URI (or literal prefix if undeclared).
		wantSpace := ""
		switch prefix {
		case "r":
			wantSpace = nsOfficeRelationships
		case "w":
			wantSpace = nsWordprocessingML
		}
		for _, a := range n.Attrs {
			if a.Name.Local != local {
				continue
			}
			if a.Name.Space == prefix || (wantSpace != "" && a.Name.Space == wantSpace) {
				return a.Value, true
			}
		}
		// 2. Any namespaced attribute with matching local name beats an unnamespaced one.
		for _, a := range n.Attrs {
			if a.Name.Local == local && a.Name.Space != "" {
				return a.Value, true
			}
		}
		// 3. Fallback to unnamespaced local name (matching docx/xml.ts:152-154).
		for _, a := range n.Attrs {
			if a.Name.Local == local {
				return a.Value, true
			}
		}
		return "", false
	}
	// Unprefixed lookup: prefer exact unnamespaced match first, then any local match.
	for _, a := range n.Attrs {
		if a.Name.Space == "" && a.Name.Local == name {
			return a.Value, true
		}
	}
	for _, a := range n.Attrs {
		if a.Name.Local == name {
			return a.Value, true
		}
	}
	return "", false
}

func (n *xmlNode) attrOr(name, fallback string) string {
	if v, ok := n.attr(name); ok {
		return v
	}
	return fallback
}

func (n *xmlNode) childElements(local string) []*xmlNode {
	if n == nil {
		return nil
	}
	var out []*xmlNode
	for _, c := range n.Children {
		if c.IsText {
			continue
		}
		if local == "" || c.Name.Local == local {
			out = append(out, c)
		}
	}
	return out
}

func (n *xmlNode) firstChild(local string) *xmlNode {
	if n == nil {
		return nil
	}
	for _, c := range n.Children {
		if !c.IsText && c.Name.Local == local {
			return c
		}
	}
	return nil
}

// descendants performs an iterative pre-order traversal collecting elements with
// the given local name.
func (n *xmlNode) descendants(local string) []*xmlNode {
	if n == nil {
		return nil
	}
	var out []*xmlNode
	stack := make([]*xmlNode, 0, len(n.Children))
	for i := len(n.Children) - 1; i >= 0; i-- {
		if !n.Children[i].IsText {
			stack = append(stack, n.Children[i])
		}
	}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur.Name.Local == local {
			out = append(out, cur)
		}
		for i := len(cur.Children) - 1; i >= 0; i-- {
			if !cur.Children[i].IsText {
				stack = append(stack, cur.Children[i])
			}
		}
	}
	return out
}

// textContent iteratively collects all descendant text without recursion.
func (n *xmlNode) textContent() string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	stack := make([]*xmlNode, 0, len(n.Children))
	for i := len(n.Children) - 1; i >= 0; i-- {
		stack = append(stack, n.Children[i])
	}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur.IsText {
			b.WriteString(cur.Text)
			continue
		}
		for i := len(cur.Children) - 1; i >= 0; i-- {
			stack = append(stack, cur.Children[i])
		}
	}
	return b.String()
}

// resolveRelativeZipPath resolves target relative to baseDir (e.g. "ppt/slides" + "../media/image1.png" -> "ppt/media/image1.png").
func resolveRelativeZipPath(baseDir, target string) string {
	target = strings.ReplaceAll(target, "\\", "/")
	if strings.HasPrefix(target, "/") {
		return normalizeZipMember(target)
	}
	if baseDir == "" {
		return normalizeZipMember(target)
	}
	return normalizeZipMember(path.Join(baseDir, target))
}

var reSafeImageExt = regexp.MustCompile(`^[a-z0-9]{1,10}$`)

// sanitizeImageExt returns a safe lowercase alphanumeric extension for extracted
// image filenames, preventing path traversal or bogus MIME parameters.
func sanitizeImageExt(ext string) string {
	ext = strings.ToLower(strings.TrimSpace(ext))
	ext = strings.TrimPrefix(ext, ".")
	if idx := strings.IndexAny(ext, ";+/\\ "); idx >= 0 {
		ext = ext[:idx]
	}
	if ext == "jpeg" {
		ext = "jpg"
	}
	if !reSafeImageExt.MatchString(ext) {
		return "png"
	}
	return ext
}

func formatMarkdownTable(rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}
	maxCols := 0
	for _, row := range rows {
		if len(row) > maxCols {
			maxCols = len(row)
		}
	}
	if maxCols == 0 {
		return ""
	}
	var lines []string
	padRow := func(r []string) []string {
		out := make([]string, maxCols)
		for i := range maxCols {
			if i < len(r) {
				out[i] = sanitizeTableCell(r[i])
			}
		}
		return out
	}
	header := padRow(rows[0])
	lines = append(lines, "| "+strings.Join(header, " | ")+" |")
	seps := make([]string, maxCols)
	for i := range seps {
		seps[i] = "---"
	}
	lines = append(lines, "| "+strings.Join(seps, " | ")+" |")
	for _, row := range rows[1:] {
		lines = append(lines, "| "+strings.Join(padRow(row), " | ")+" |")
	}
	return strings.Join(lines, "\n")
}

func sanitizeTableCell(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "|", `\|`)
	return strings.TrimSpace(s)
}
