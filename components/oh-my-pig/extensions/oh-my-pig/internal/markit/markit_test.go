package markit

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func buildTestZip(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
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
	return buf.Bytes()
}

func TestRegistryDetectionAndErrors(t *testing.T) {
	for _, ext := range []string{".pdf", ".docx", ".pptx", ".xlsx", ".epub", "PDF", "  .DocX "} {
		if !IsConvertibleExtension(ext) {
			t.Errorf("IsConvertibleExtension(%q) = false, want true", ext)
		}
	}
	for _, ext := range []string{"", ".txt", ".doc", ".ppt", ".xls", ".rtf", ".zip"} {
		if IsConvertibleExtension(ext) {
			t.Errorf("IsConvertibleExtension(%q) = true, want false", ext)
		}
	}
	if !IsConvertiblePath("/tmp/report.DOCX") {
		t.Errorf("IsConvertiblePath uppercase failed")
	}
	if IsConvertiblePath("/tmp/notes.md") {
		t.Errorf("IsConvertiblePath(.md) = true, want false")
	}

	_, err := ConvertBuffer(context.Background(), []byte("hello"), ".rtf")
	if err == nil || !strings.Contains(err.Error(), "Unsupported format: .rtf") {
		t.Fatalf("ConvertBuffer(.rtf) err = %v, want Unsupported format", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ConvertBuffer(ctx, []byte("hello"), ".docx"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled ctx err = %v, want context.Canceled", err)
	}
}

// TestTurndownGoldenParity verifies exact whole-output equality against real omp v19.1.1
// `createTurndown()` + `normalizeTablesHtml()` outputs.
func TestTurndownGoldenParity(t *testing.T) {
	cases := []struct {
		name string
		html string
		want string
	}{
		{
			name: "emphasis_strong_strike_escape",
			html: `<p>Hello <b>bold</b>, <i>ital</i>, <b><i>both</i></b>, <s>gone</s>, snake_case, DOCX_NEEDLE_1, a*b [c]</p>`,
			want: `Hello **bold**, _ital_, **_both_**, ~~gone~~, snake_case, DOCX_NEEDLE_1, a\*b \[c\]`,
		},
		{
			name: "br_hr_heading",
			html: `<h1>1. Title Here</h1><p>Line 1<br>Line 2</p><hr><p>After</p>`,
			want: "# 1. Title Here\n\nLine 1  \nLine 2\n\n* * *\n\nAfter",
		},
		{
			name: "links_and_imgs",
			html: `<p><a href="https://ex.com/a(b)" title="hi &quot;there&quot;">link</a> and <a>nohref</a> and <img src="a(1).png" alt="pic_1" title="T"></p>`,
			want: `[link](https://ex.com/a\(b\) "hi \"there\"") and [nohref]() and ![pic_1](a\(1\).png "T")`,
		},
		{
			name: "code_and_pre",
			html: "<p>Inline <code>a`b</code></p><pre><code class=\"language-go\">func main() {}\n</code></pre><pre>bare pre\nline 2</pre>",
			want: "Inline ``a`b``\n\n```go\nfunc main() {}\n```\n\nbare pre\nline 2",
		},
		{
			name: "block_elements",
			html: `<dl><dt>Term</dt><dd>Def</dd></dl><details><summary>Sum</summary><p>Body</p></details>`,
			want: "Term\n\nDef\n\nSum\n\nBody",
		},
		{
			name: "lists_nested_and_start",
			html: `<ul><li>First<p>Para 2</p><ul><li>Sub 1</li><li>Sub 2</li></ul></li><li>Second</li></ul><ol start="3"><li>Third</li><li>Fourth</li></ol>`,
			want: "- First\n  \n  Para 2\n  \n  \n  - Sub 1\n  - Sub 2\n- Second\n\n3. Third\n4. Fourth",
		},
		{
			name: "gfm_table_normalized",
			html: `<table><tr><td>H1</td><td>H2</td></tr><tr><td><p>A</p><p>B</p></td><td>C</td></tr></table>`,
			want: "| H1 | H2 |\n| --- | --- |\n| A B | C |",
		},
		{
			name: "epub_html_quirks",
			html: `<?xml version="1.0" encoding="ISO-8859-1"?><html><head><title>Ch1</title><style>p{color:red}</style><script>x()</script></head><body><!-- a -- b --><h1>Cap</h1><p>1 < 2 &amp; <img src=cover.png alt=Cov></p></body></html>`,
			want: "<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?>\n\nCh1\n\n# Cap\n\n1 < 2 & ![Cov](cover.png)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := htmlToMarkdown(context.Background(), tc.html)
			if err != nil {
				t.Fatalf("htmlToMarkdown: %v", err)
			}
			if got != tc.want {
				t.Errorf("mismatch\ngot:  %q\nwant: %q", got, tc.want)
			}
		})
	}
}

func TestDocxConverterGoldenAndFeatures(t *testing.T) {
	// Golden whole-output check against real omp DocxConverter.
	goldenZip := buildTestZip(t, map[string][]byte{
		"word/document.xml":  []byte(`<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><w:body><w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>1. Title</w:t></w:r></w:p><w:p><w:r><w:t>Hello </w:t></w:r><w:r><w:rPr><w:b/><w:i/></w:rPr><w:t>both</w:t></w:r><w:r><w:footnoteReference w:id="1"/></w:r></w:p><w:tbl><w:tr><w:tc><w:p><w:r><w:t>A</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>B</w:t></w:r></w:p></w:tc></w:tr><w:tr><w:tc><w:p><w:r><w:t>1</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>2</w:t></w:r></w:p></w:tc></w:tr></w:tbl></w:body></w:document>`),
		"word/styles.xml":    []byte(`<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/></w:style></w:styles>`),
		"word/footnotes.xml": []byte(`<w:footnotes xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:footnote w:id="1"><w:p><w:r><w:t>Note text</w:t></w:r></w:p></w:footnote></w:footnotes>`),
	})
	goldenRes, err := ConvertBuffer(context.Background(), goldenZip, ".docx")
	if err != nil {
		t.Fatalf("golden docx: %v", err)
	}
	wantGolden := "# 1. Title\n\nHello **_both_**[\\[1\\]](#footnote-1)\n\n| A | B |\n| --- | --- |\n| 1 | 2 |\n\n1. Note text [↑](#footnote-ref-1)"
	if goldenRes.Markdown != wantGolden {
		t.Errorf("DOCX golden mismatch\ngot:  %q\nwant: %q", goldenRes.Markdown, wantGolden)
	}

	// Feature coverage: lists, links, strikethrough, images, imageDir auto-mkdir + ext sanitization.
	docXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
            xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"
            xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
  <w:body>
    <w:p>
      <w:pPr><w:pStyle w:val="Heading1"/></w:pPr>
      <w:r><w:t>Project Overview</w:t></w:r>
    </w:p>
    <w:p>
      <w:r><w:t>Hello </w:t></w:r>
      <w:r><w:rPr><w:b/></w:rPr><w:t>DOCX_NEEDLE_1</w:t></w:r>
      <w:r><w:t> and </w:t></w:r>
      <w:r><w:rPr><w:i/></w:rPr><w:t>italic</w:t></w:r>
      <w:r><w:t> </w:t></w:r>
      <w:r><w:rPr><w:strike/></w:rPr><w:t>old</w:t></w:r>
      <w:hyperlink r:id="rIdLink"><w:r><w:t>docs</w:t></w:r></w:hyperlink>
    </w:p>
    <w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr><w:r><w:t>First bullet</w:t></w:r></w:p>
    <w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="2"/></w:numPr></w:pPr><w:r><w:t>Step one</w:t></w:r></w:p>
    <w:p><w:r><w:drawing><a:blip r:embed="rIdImg"/></w:drawing></w:r></w:p>
  </w:body>
</w:document>`
	zipBytes := buildTestZip(t, map[string][]byte{
		"word/document.xml":            []byte(docXML),
		"word/styles.xml":              []byte(`<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/></w:style></w:styles>`),
		"word/_rels/document.xml.rels": []byte(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rIdLink" Target="https://example.com" TargetMode="External"/><Relationship Id="rIdImg" Target="media/logo.png"/></Relationships>`),
		"word/numbering.xml":           []byte(`<w:numbering xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:abstractNum w:abstractNumId="10"><w:lvl w:ilvl="0"><w:numFmt w:val="bullet"/></w:lvl></w:abstractNum><w:abstractNum w:abstractNumId="20"><w:lvl w:ilvl="0"><w:numFmt w:val="decimal"/></w:lvl></w:abstractNum><w:num w:numId="1"><w:abstractNumId w:val="10"/></w:num><w:num w:numId="2"><w:abstractNumId w:val="20"/></w:num></w:numbering>`),
		"word/media/logo.png":          []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a},
	})

	res, err := ConvertBuffer(context.Background(), zipBytes, ".docx")
	if err != nil {
		t.Fatalf("ConvertBuffer(.docx): %v", err)
	}
	for _, want := range []string{
		"# Project Overview",
		"**DOCX_NEEDLE_1**",
		"_italic_",
		"~~old~~",
		"[docs](https://example.com)",
		"- First bullet",
		"1. Step one",
		"<!-- image: image_1 -->",
	} {
		if !strings.Contains(res.Markdown, want) {
			t.Errorf("DOCX markdown missing %q\ngot:\n%s", want, res.Markdown)
		}
	}

	// Verify imageDir auto-creates nonexistent directory and writes image.
	tmpDir := t.TempDir()
	docPath := filepath.Join(tmpDir, "sample.docx")
	imgDir := filepath.Join(tmpDir, "nested", "imgs")
	if err := os.WriteFile(docPath, zipBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	resWithImg, err := ConvertFileWithImageDir(context.Background(), docPath, imgDir)
	if err != nil {
		t.Fatalf("ConvertFileWithImageDir: %v", err)
	}
	extracted := filepath.Join(imgDir, "image_1.png")
	if _, err := os.Stat(extracted); err != nil {
		t.Errorf("expected extracted image at %s: %v", extracted, err)
	}
	if !strings.Contains(resWithImg.Markdown, "![image_1]("+extracted+")") {
		t.Errorf("expected markdown image link to %s\ngot:\n%s", extracted, resWithImg.Markdown)
	}

	// Corrupt ZIP returns error.
	if _, err := ConvertBuffer(context.Background(), []byte("not a zip"), ".docx"); err == nil {
		t.Errorf("expected error for corrupt DOCX")
	}
}

func TestPptxSlideOrderAndOrphanSlides(t *testing.T) {
	// sldIdLst intentionally lists slide3 first, then slide1; slide2 is an orphan not in sldIdLst.
	// Also tests that local `id="256"` does NOT shadow `r:id="rId3"`.
	presXML := `<?xml version="1.0" encoding="UTF-8"?>
<p:presentation xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"
                xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <p:sldIdLst>
    <p:sldId id="256" r:id="rId3"/>
    <p:sldId id="257" r:id="rId1"/>
  </p:sldIdLst>
</p:presentation>`

	presRels := `<?xml version="1.0" encoding="UTF-8"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Target="slides/slide1.xml"/>
  <Relationship Id="rId3" Target="slides/slide3.xml"/>
</Relationships>`

	slide1 := `<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><p:cSld><p:spTree><p:sp><p:txBody><a:p><a:r><a:t>Second In Deck</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld></p:sld>`
	slide2 := `<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><p:cSld><p:spTree><p:sp><p:txBody><a:p><a:r><a:t>ORPHAN SLIDE</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld></p:sld>`
	slide3 := `<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><p:cSld><p:spTree>
      <p:sp><p:txBody><a:p><a:r><a:t>First In Deck</a:t></a:r></a:p></p:txBody></p:sp>
      <p:sp><p:txBody><a:p><a:r><a:t>Body text</a:t></a:r></a:p></p:txBody></p:sp>
      <p:pic><p:nvPicPr><p:cNvPr id="3" name="Chart 1"/></p:nvPicPr><p:blipFill><a:blip r:embed="rImg1"/></p:blipFill></p:pic>
      <p:graphicFrame><a:graphic><a:graphicData><a:tbl>
        <a:tr><a:tc><a:txBody><a:p><a:r><a:t>K</a:t></a:r></a:p></a:txBody></a:tc><a:tc><a:txBody><a:p><a:r><a:t>V</a:t></a:r></a:p></a:txBody></a:tc></a:tr>
        <a:tr><a:tc><a:txBody><a:p><a:r><a:t>a</a:t></a:r></a:p></a:txBody></a:tc><a:tc><a:txBody><a:p><a:r><a:t>1</a:t></a:r></a:p></a:txBody></a:tc></a:tr>
      </a:tbl></a:graphicData></a:graphic></p:graphicFrame>
    </p:spTree></p:cSld></p:sld>`
	slide3Rels := `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rImg1" Target="../media/chart1.png"/></Relationships>`
	notes3 := `<p:notes xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><p:cSld><p:spTree><p:sp><p:nvSpPr><p:nvPr><p:ph type="sldImg"/></p:nvPr></p:nvSpPr><p:txBody><a:p><a:r><a:t>Skip</a:t></a:r></a:p></p:txBody></p:sp><p:sp><p:nvSpPr><p:nvPr><p:ph type="body"/></p:nvPr></p:nvSpPr><p:txBody><a:p><a:r><a:t>Speaker note</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld></p:notes>`

	zipBytes := buildTestZip(t, map[string][]byte{
		"ppt/presentation.xml":             []byte(presXML),
		"ppt/_rels/presentation.xml.rels":  []byte(presRels),
		"ppt/slides/slide1.xml":            []byte(slide1),
		"ppt/slides/slide2.xml":            []byte(slide2),
		"ppt/slides/slide3.xml":            []byte(slide3),
		"ppt/slides/_rels/slide3.xml.rels": []byte(slide3Rels),
		"ppt/notesSlides/notesSlide3.xml":  []byte(notes3),
		"ppt/media/chart1.png":             []byte{0x89, 'P', 'N', 'G'},
	})

	res, err := ConvertBuffer(context.Background(), zipBytes, ".pptx")
	if err != nil {
		t.Fatalf("ConvertBuffer(.pptx): %v", err)
	}
	want := strings.Join([]string{
		"<!-- Slide 1 -->",
		"# First In Deck",
		"Body text",
		"<!-- image: Chart 1 (slide 1) -->",
		"| K | V |",
		"| --- | --- |",
		"| a | 1 |",
		"",
		"### Notes:",
		"Speaker note",
		"",
		"<!-- Slide 2 -->",
		"# Second In Deck",
	}, "\n")
	if res.Markdown != want {
		t.Errorf("PPTX mismatch\ngot:\n%s\n\nwant:\n%s", res.Markdown, want)
	}
	if strings.Contains(res.Markdown, "ORPHAN SLIDE") || strings.Contains(res.Markdown, "<!-- Slide 3 -->") {
		t.Errorf("orphan slide leaked into PPTX output:\n%s", res.Markdown)
	}
}

func TestXlsxConverter(t *testing.T) {
	wbXML := `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Summary" sheetId="1" r:id="rId1"/><sheet name="Codes" sheetId="2" r:id="rId2"/></sheets></workbook>`
	relsXML := `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Target="worksheets/sheet1.xml"/><Relationship Id="rId2" Target="worksheets/sheet2.xml"/></Relationships>`
	sharedStringsXML := `<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><si><t>Name</t></si><si><t>Active</t></si><si><r><t>Rich</t></r><r><t>Text</t></r></si></sst>`
	sheet1XML := `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c t="s"><v>0</v></c><c t="s"><v>1</v></c><c t="inlineStr"><is><t>Zip</t></is></c></row><row r="2"><c t="s"><v>2</v></c><c t="b"><v>1</v></c><c t="inlineStr"><is><t>00123</t></is></c></row><row r="3"><c t="inlineStr"><is><t>Beta</t></is></c><c t="b"><v>0</v></c></row></sheetData></worksheet>`
	sheet2XML := `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c><v>90071992547409931234</v></c></row></sheetData></worksheet>`

	zipBytes := buildTestZip(t, map[string][]byte{
		"xl/workbook.xml":            []byte(wbXML),
		"xl/_rels/workbook.xml.rels": []byte(relsXML),
		"xl/sharedStrings.xml":       []byte(sharedStringsXML),
		"xl/worksheets/sheet1.xml":   []byte(sheet1XML),
		"xl/worksheets/sheet2.xml":   []byte(sheet2XML),
	})

	res, err := ConvertBuffer(context.Background(), zipBytes, ".xlsx")
	if err != nil {
		t.Fatalf("ConvertBuffer(.xlsx): %v", err)
	}
	want := "## Summary\n\n| Name | Active | Zip |\n| --- | --- | --- |\n| RichText | TRUE | 00123 |\n| Beta | FALSE |  |\n\n## Codes\n\n| 90071992547409931234 |\n| --- |"
	if res.Markdown != want {
		t.Errorf("XLSX mismatch\ngot:  %q\nwant: %q", res.Markdown, want)
	}
}

func TestEpubConverterGoldenAndQuirks(t *testing.T) {
	// Golden parity test matching real omp EpubConverter.
	goldenZip := buildTestZip(t, map[string][]byte{
		"META-INF/container.xml": []byte(`<container version="1.0"><rootfiles><rootfile full-path="OEBPS/content.opf"/></rootfiles></container>`),
		"OEBPS/content.opf":      []byte(`<package><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Book</dc:title><dc:creator>Ada</dc:creator></metadata><manifest><item id="c1" href="c1.xhtml"/></manifest><spine><itemref idref="c1"/></spine></package>`),
		"OEBPS/c1.xhtml":         []byte(`<?xml version="1.0" encoding="UTF-8"?><html><head><title>Ch1</title><style>p{color:red}</style><script>x()</script></head><body><h1>Cap</h1><p>Hello <i>world</i></p></body></html>`),
	})
	res, err := ConvertBuffer(context.Background(), goldenZip, ".epub")
	if err != nil {
		t.Fatalf("golden epub: %v", err)
	}
	if res.Title != "Book" {
		t.Errorf("Title = %q, want Book", res.Title)
	}
	want := "**Title:** Book\n**Authors:** Ada\n\n<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n\nCh1\n\n# Cap\n\nHello _world_"
	if res.Markdown != want {
		t.Errorf("EPUB golden mismatch\ngot:  %q\nwant: %q", res.Markdown, want)
	}

	// Non-UTF8 XML declaration + unescaped `<` + `--` comment + unquoted `<img src=cover.png>`.
	quirkyZip := buildTestZip(t, map[string][]byte{
		"META-INF/container.xml": []byte(`<container version="1.0"><rootfiles><rootfile full-path="content.opf"/></rootfiles></container>`),
		"content.opf":            []byte(`<package><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Quirky</dc:title></metadata><manifest><item id="c1" href="c1.html"/></manifest><spine><itemref idref="c1"/></spine></package>`),
		"c1.html":                []byte(`<?xml version="1.0" encoding="ISO-8859-1"?><html><body><!-- a -- b --><h2>Sec</h2><p>x < y and <img src=cover.png alt=Cover></p></body></html>`),
	})
	qRes, err := ConvertBuffer(context.Background(), quirkyZip, ".epub")
	if err != nil {
		t.Fatalf("quirky epub: %v", err)
	}
	for _, wantSub := range []string{"## Sec", "x < y", "![Cover](cover.png)"} {
		if !strings.Contains(qRes.Markdown, wantSub) {
			t.Errorf("quirky epub missing %q\ngot: %q", wantSub, qRes.Markdown)
		}
	}
}

func TestNestingDepthCapAndDoSResistance(t *testing.T) {
	// 1. Deeply nested XML in DOCX returns a clean error instead of stack overflow.
	depth := MaxXMLNestingDepth + 50
	deepXML := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p>` +
		strings.Repeat(`<w:x>`, depth) + `<w:r><w:t>hi</w:t></w:r>` + strings.Repeat(`</w:x>`, depth) +
		`</w:p></w:body></w:document>`
	docxZip := buildTestZip(t, map[string][]byte{"word/document.xml": []byte(deepXML)})
	if _, err := ConvertBuffer(context.Background(), docxZip, ".docx"); err == nil || !strings.Contains(err.Error(), "nesting depth") {
		t.Fatalf("expected XML nesting depth error, got: %v", err)
	}

	// 2. Deeply nested HTML in EPUB returns a clean error instead of crashing.
	deepHTML := `<html><body>` + strings.Repeat(`<span><b>`, depth) + `hi` + strings.Repeat(`</b></span>`, depth) + `</body></html>`
	epubZip := buildTestZip(t, map[string][]byte{
		"META-INF/container.xml": []byte(`<container version="1.0"><rootfiles><rootfile full-path="content.opf"/></rootfiles></container>`),
		"content.opf":            []byte(`<package><manifest><item id="c1" href="c1.xhtml"/></manifest><spine><itemref idref="c1"/></spine></package>`),
		"c1.xhtml":               []byte(deepHTML),
	})
	if _, err := ConvertBuffer(context.Background(), epubZip, ".epub"); err == nil || !strings.Contains(err.Error(), "nesting depth") {
		t.Fatalf("expected HTML nesting depth error, got: %v", err)
	}

	// 3. Re-reading the same ZIP member does not double-count against totalInflated.
	pkg, err := openZip(buildTestZip(t, map[string][]byte{"a.txt": bytes.Repeat([]byte("x"), 1024)}))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := pkg.readBytes("a.txt"); err != nil {
		t.Fatal(err)
	}
	firstCount := pkg.totalInflated
	if _, _, err := pkg.readBytes("a.txt"); err != nil {
		t.Fatal(err)
	}
	if pkg.totalInflated != firstCount {
		t.Errorf("re-reading member double-counted totalInflated: %d -> %d", firstCount, pkg.totalInflated)
	}

	// 4. Deeply increasing DOCX list w:ilvl returns a clean nesting error quickly (no quadratic blowup).
	var deepListBody strings.Builder
	for i := range MaxXMLNestingDepth + 20 {
		fmt.Fprintf(&deepListBody, `<w:p><w:pPr><w:numPr><w:ilvl w:val="%d"/><w:numId w:val="1"/></w:numPr></w:pPr><w:r><w:t>item %d</w:t></w:r></w:p>`, i, i)
	}
	deepListDoc := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` + deepListBody.String() + `</w:body></w:document>`
	deepListZip := buildTestZip(t, map[string][]byte{"word/document.xml": []byte(deepListDoc)})
	start := time.Now()
	if _, err := ConvertBuffer(context.Background(), deepListZip, ".docx"); err == nil || !strings.Contains(err.Error(), "DOCX list nesting depth") {
		t.Fatalf("expected DOCX list nesting depth error, got: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("deep DOCX list rejection took too long: %s", elapsed)
	}

	// 5. DOCX renderList respects context cancellation.
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	dctx := &docxContext{ctx: canceledCtx, stepCount: 1023}
	items := []docxParagraphBlock{{html: "a", hasList: true, level: 0}}
	var lb strings.Builder
	if _, err := dctx.renderList(&lb, items, 0, 0, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("renderList canceled ctx = %v, want context.Canceled", err)
	}

	// 6. EPUB cumulative Markdown output cap rejects repeated spine amplification.
	origCap := maxMarkdownOutputBytes
	t.Cleanup(func() { maxMarkdownOutputBytes = origCap })
	maxMarkdownOutputBytes = 1024
	var spineRefs strings.Builder
	for range 10 {
		spineRefs.WriteString(`<itemref idref="c1"/>`)
	}
	bigChapter := `<html><body><p>` + strings.Repeat("word ", 60) + `</p></body></html>`
	ampEpub := buildTestZip(t, map[string][]byte{
		"META-INF/container.xml": []byte(`<container version="1.0"><rootfiles><rootfile full-path="content.opf"/></rootfiles></container>`),
		"content.opf":            []byte(`<package><manifest><item id="c1" href="c1.xhtml"/></manifest><spine>` + spineRefs.String() + `</spine></package>`),
		"c1.xhtml":               []byte(bigChapter),
	})
	if _, err := ConvertBuffer(context.Background(), ampEpub, ".epub"); err == nil || !strings.Contains(err.Error(), "exceeds 1024 bytes limit") {
		t.Fatalf("expected EPUB cumulative markdown limit error, got: %v", err)
	}
}

func TestPdfConverterRefusalSuccessTimeoutAndOverflow(t *testing.T) {
	origLook := pdfLookPath
	origTimeout := pdfTimeout
	origMaxOut := pdfMaxStdoutBytes
	t.Cleanup(func() {
		pdfLookPath = origLook
		pdfTimeout = origTimeout
		pdfMaxStdoutBytes = origMaxOut
	})

	// 1. Missing CLI refusal.
	pdfLookPath = func(file string) (string, error) { return "", errors.New("not found") }
	conv := &PdfConverter{}
	if !conv.Accepts(StreamInfo{Extension: ".pdf"}) || !conv.Accepts(StreamInfo{MIMEType: "application/pdf; charset=binary"}) {
		t.Errorf("PdfConverter should accept .pdf and application/pdf")
	}
	if conv.Accepts(StreamInfo{Extension: ".txt", MIMEType: "text/plain"}) {
		t.Errorf("PdfConverter should not accept .txt")
	}
	if _, err := ConvertBuffer(context.Background(), []byte("%PDF-1.4\n"), ".pdf"); err == nil || !strings.Contains(err.Error(), "pdftotext is not installed on PATH") {
		t.Fatalf("expected ErrPdftotextNotFound refusal, got: %v", err)
	}

	if runtime.GOOS == "windows" {
		return
	}

	// 2. Fake pdftotext script verifying LocalPath is passed directly (not stdin) and stdout is returned.
	tmpDir := t.TempDir()
	pdfFile := filepath.Join(tmpDir, "doc.pdf")
	if err := os.WriteFile(pdfFile, []byte("%PDF-1.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	okScript := filepath.Join(tmpDir, "pdftotext-ok.sh")
	if err := os.WriteFile(okScript, []byte("#!/bin/sh\nprintf 'Page1\\fPage2 (arg=%s)\\n' \"$3\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	pdfLookPath = func(string) (string, error) { return okScript, nil }
	res, err := ConvertFile(context.Background(), pdfFile)
	if err != nil {
		t.Fatalf("ConvertFile(pdf) via fake script: %v", err)
	}
	wantPDF := "Page1\n\nPage2 (arg=" + pdfFile + ")"
	if res.Markdown != wantPDF {
		t.Errorf("PDF output = %q, want %q", res.Markdown, wantPDF)
	}

	// 3. Stdout overflow cap.
	bigScript := filepath.Join(tmpDir, "pdftotext-big.sh")
	if err := os.WriteFile(bigScript, []byte("#!/bin/sh\nhead -c 4096 /dev/zero | tr '\\0' 'A'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	pdfLookPath = func(string) (string, error) { return bigScript, nil }
	pdfMaxStdoutBytes = 1024
	if _, err := ConvertFile(context.Background(), pdfFile); err == nil || !strings.Contains(err.Error(), "exceeds 1024 bytes limit") {
		t.Fatalf("expected stdout overflow error, got: %v", err)
	}
	pdfMaxStdoutBytes = origMaxOut

	// 4. Timeout enforcement.
	slowScript := filepath.Join(tmpDir, "pdftotext-slow.sh")
	if err := os.WriteFile(slowScript, []byte("#!/bin/sh\nexec sleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	pdfLookPath = func(string) (string, error) { return slowScript, nil }
	pdfTimeout = 100 * time.Millisecond
	if _, err := ConvertFile(context.Background(), pdfFile); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error, got: %v", err)
	}
}
