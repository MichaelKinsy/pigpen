package files

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/dirs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
)

func writeZipFixture(t *testing.T, dest string, entries map[string][]byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip.Create(%q): %v", name, err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatalf("zip.Write(%q): %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}
	if err := os.WriteFile(dest, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", dest, err)
	}
}

func TestReadMarkitDocuments(t *testing.T) {
	cwd := t.TempDir()
	store := hashline.NewStore()
	reHashHeader := regexp.MustCompile(`^\[[^\n]+#[0-9A-F]{4}\]\n1:`)

	// 1. Multi-paragraph DOCX to test full read (with hashline header), `:N-M` range,
	// `:raw`, suffix resolution, and ZIP-container member reads (`report.docx:word/document.xml`).
	var docParas strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&docParas, `<w:p><w:r><w:t>docx line %d</w:t></w:r></w:p>`, i)
	}
	docxXML := `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>` + docParas.String() + `</w:body>
</w:document>`
	docxPath := filepath.Join(cwd, "nested", "docs", "report.docx")
	writeZipFixture(t, docxPath, map[string][]byte{
		"word/document.xml": []byte(docxXML),
	})

	// Full read emits [path#TAG] header and numbered lines matching omp read.ts:2760-2782.
	res, err := executeRead(store, cwd, docxPath)
	if err != nil {
		t.Fatalf("read docx: %v", err)
	}
	if !reHashHeader.MatchString(res.Content) {
		t.Errorf("full docx read missing [path#TAG] header + line 1 prefix:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "docx line 1") || !strings.Contains(res.Content, "docx line 30") {
		t.Errorf("full docx read missing lines:\n%s", res.Content)
	}
	details, _ := res.Details.(map[string]any)
	if got, _ := details["resolvedPath"].(string); got != dirs.StandardizeMacOSPath(docxPath) {
		t.Errorf("resolvedPath = %q, want %q", got, dirs.StandardizeMacOSPath(docxPath))
	}
	if rp, _ := details["rowPrefixes"].(bool); !rp {
		t.Errorf("expected details.rowPrefixes=true on non-raw docx read")
	}

	// Line range selector (`:20-24`).
	ranged, err := executeRead(store, cwd, docxPath+":20-24")
	if err != nil {
		t.Fatalf("read docx range: %v", err)
	}
	if !strings.Contains(ranged.Content, "docx line 11") {
		t.Errorf("ranged docx read missing expected window line:\n%s", ranged.Content)
	}
	if strings.Contains(ranged.Content, "docx line 1\n") {
		t.Errorf("ranged docx read unexpectedly included line 1:\n%s", ranged.Content)
	}

	// Raw selector (`:raw:1-3`).
	rawRes, err := executeRead(store, cwd, docxPath+":raw:1-3")
	if err != nil {
		t.Fatalf("read docx raw: %v", err)
	}
	rawDetails, _ := rawRes.Details.(map[string]any)
	if rawFlag, _ := rawDetails["raw"].(bool); !rawFlag {
		t.Errorf("expected details.raw=true on :raw read")
	}
	if !strings.Contains(rawRes.Content, "docx line 1") || !strings.Contains(rawRes.Content, "docx line 2") {
		t.Errorf("raw docx read missing lines 1-3:\n%s", rawRes.Content)
	}
	if strings.Contains(rawRes.Content, "docx line 5") {
		t.Errorf("raw docx :1-3 leaked line 5:\n%s", rawRes.Content)
	}

	// Unique suffix resolution (`report.docx` -> `nested/docs/report.docx`).
	suffixRes, err := executeRead(store, cwd, "report.docx")
	if err != nil {
		t.Fatalf("read docx via suffix: %v", err)
	}
	if !strings.Contains(suffixRes.Content, "resolved to 'nested/docs/report.docx' via suffix match") {
		t.Errorf("missing suffix resolution notice:\n%s", suffixRes.Content)
	}
	if !strings.Contains(suffixRes.Content, "docx line 1") {
		t.Errorf("suffix-resolved docx missing content:\n%s", suffixRes.Content)
	}
	sDetails, _ := suffixRes.Details.(map[string]any)
	if sDetails["suffixResolution"] == nil {
		t.Errorf("expected details.suffixResolution on suffix-resolved docx")
	}

	// ZIP-container member read (`nested/docs/report.docx:word/document.xml`).
	memberRes, err := executeRead(store, cwd, "nested/docs/report.docx:word/document.xml")
	if err != nil {
		t.Fatalf("read docx member: %v", err)
	}
	if !strings.Contains(memberRes.Content, "<w:document") || !strings.Contains(memberRes.Content, "docx line 1") {
		t.Errorf("docx member read missing raw XML:\n%s", memberRes.Content)
	}

	// 2. PPTX.
	pptxPath := filepath.Join(cwd, "deck.pptx")
	writeZipFixture(t, pptxPath, map[string][]byte{
		"ppt/presentation.xml":            []byte(`<p:presentation xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><p:sldIdLst><p:sldId id="256" r:id="rId1"/></p:sldIdLst></p:presentation>`),
		"ppt/_rels/presentation.xml.rels": []byte(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Target="slides/slide1.xml"/></Relationships>`),
		"ppt/slides/slide1.xml":           []byte(`<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><p:cSld><p:spTree><p:sp><p:txBody><a:p><a:r><a:t>Deck Title</a:t></a:r></a:p></p:txBody></p:sp><p:sp><p:txBody><a:p><a:r><a:t>Slide bullet</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld></p:sld>`),
	})
	pptxRes, err := executeRead(store, cwd, pptxPath)
	if err != nil {
		t.Fatalf("read pptx: %v", err)
	}
	for _, want := range []string{"<!-- Slide 1 -->", "# Deck Title", "Slide bullet"} {
		if !strings.Contains(pptxRes.Content, want) {
			t.Errorf("pptx read missing %q:\n%s", want, pptxRes.Content)
		}
	}

	// 3. XLSX.
	xlsxPath := filepath.Join(cwd, "sheet.xlsx")
	writeZipFixture(t, xlsxPath, map[string][]byte{
		"xl/workbook.xml":            []byte(`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Data" sheetId="1" r:id="rId1"/></sheets></workbook>`),
		"xl/_rels/workbook.xml.rels": []byte(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>`),
		"xl/worksheets/sheet1.xml":   []byte(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c t="inlineStr"><is><t>Key</t></is></c><c t="inlineStr"><is><t>Val</t></is></c></row><row r="2"><c t="inlineStr"><is><t>k1</t></is></c><c><v>42</v></c></row></sheetData></worksheet>`),
	})
	xlsxRes, err := executeRead(store, cwd, xlsxPath)
	if err != nil {
		t.Fatalf("read xlsx: %v", err)
	}
	for _, want := range []string{"## Data", "| Key | Val |", "| k1 | 42 |"} {
		if !strings.Contains(xlsxRes.Content, want) {
			t.Errorf("xlsx read missing %q:\n%s", want, xlsxRes.Content)
		}
	}

	// 4. EPUB.
	epubPath := filepath.Join(cwd, "book.epub")
	writeZipFixture(t, epubPath, map[string][]byte{
		"META-INF/container.xml": []byte(`<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="content.opf"/></rootfiles></container>`),
		"content.opf":            []byte(`<package xmlns="http://www.idpf.org/2007/opf"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Test Book</dc:title></metadata><manifest><item id="c1" href="c1.xhtml"/></manifest><spine><itemref idref="c1"/></spine></package>`),
		"c1.xhtml":               []byte(`<html xmlns="http://www.w3.org/1999/xhtml"><body><h1>Intro</h1><p>EPUB body text</p></body></html>`),
	})
	epubRes, err := executeRead(store, cwd, epubPath)
	if err != nil {
		t.Fatalf("read epub: %v", err)
	}
	for _, want := range []string{"**Title:** Test Book", "# Intro", "EPUB body text"} {
		if !strings.Contains(epubRes.Content, want) {
			t.Errorf("epub read missing %q:\n%s", want, epubRes.Content)
		}
	}

	// 5. Corrupt ZIP refusal returns graceful `[Cannot read .docx file: ...]` notice with fileSize details.
	corruptPath := filepath.Join(cwd, "broken.docx")
	corruptPayload := []byte{0x00, 0x01, 0x02, 0x03, 'b', 'a', 'd'}
	if err := os.WriteFile(corruptPath, corruptPayload, 0o644); err != nil {
		t.Fatal(err)
	}
	corruptRes, err := executeRead(store, cwd, corruptPath)
	if err != nil {
		t.Fatalf("corrupt docx should return graceful notice, got error: %v", err)
	}
	if !strings.HasPrefix(corruptRes.Content, "[Cannot read .docx file:") {
		t.Errorf("corrupt docx content = %q, want [Cannot read .docx file: ...]", corruptRes.Content)
	}
	cDetails, _ := corruptRes.Details.(map[string]any)
	if got, _ := cDetails["fileSize"].(int64); got != int64(len(corruptPayload)) {
		t.Errorf("corrupt docx fileSize = %v, want %d", cDetails["fileSize"], len(corruptPayload))
	}
}
