package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// makePDF is makePdf of the upstream fixture (port/oracle/test/pdf-fixture.mjs): a one-page PDF
// with the text wrapped at 60 characters.
func makePDF(text string) []byte {
	var cmds []string
	for _, line := range strings.Split(text, "\n") {
		chunks := []string{""}
		if len(line) > 0 {
			chunks = nil
			for len(line) > 0 {
				n := min(60, len(line))
				chunks, line = append(chunks, line[:n]), line[n:]
			}
		}
		for _, c := range chunks {
			cmds = append(cmds, "("+regexp.MustCompile(`[\\()]`).ReplaceAllString(c, `\$0`)+") Tj T*")
		}
	}
	stream := "BT /F1 12 Tf 14 TL 72 720 Td " + strings.Join(cmds, " ") + " ET"
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
	}
	body := "%PDF-1.4\n"
	var offsets []int
	for i, o := range objects {
		offsets = append(offsets, len(body))
		body += fmt.Sprintf("%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := len(body)
	body += fmt.Sprintf("xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, o := range offsets {
		body += fmt.Sprintf("%010d 00000 n \n", o)
	}
	body += fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return []byte(body)
}

// chainRun is one extractPDFToMarkdown call of the provider chain twins.
type chainRun struct {
	content string
	err     error
	net     *fakeNet
}

// runChain is buildChildScript of pdf-extract.test.mjs: Gemini ("none", "success", "truncate") and
// Datalab ("none", "success", "fail") answer from a fake net, the provider is pinned when given,
// and the extraction runs with ctx. The returned content is checked against the saved file.
func runChain(t *testing.T, ctx context.Context, gemini, datalab, provider string) chainRun {
	t.Helper()
	_, agentDir := isolate(t)
	unsetenv(t, "DATALAB_API_KEY", "DATALAB_MODE", "DATALAB_PROCESSING_LOCATION", "DATALAB_API_BASE", "GOOGLE_GEMINI_BASE_URL", "CLOUDFLARE_API_KEY")
	if provider != "" {
		writeConfig(t, agentDir, `{"pdf":{"provider":"`+provider+`"}}`)
	}
	if gemini != "none" {
		t.Setenv("GEMINI_API_KEY", "synthetic-gemini-key")
	}
	if datalab != "none" {
		t.Setenv("DATALAB_API_KEY", "synthetic-datalab-key")
	}
	n := useNet(t, func(c netCall) netReply {
		if ctx.Err() != nil {
			return netReply{Err: ctx.Err()}
		}
		switch {
		case strings.Contains(c.URL, "generateContent"):
			if provider == "datalab" || provider == "unpdf" {
				t.Errorf("Gemini called but provider=%s", provider)
			}
			if gemini == "success" || gemini == "truncate" {
				reason, text := "STOP", "<!-- Page 1 -->\nGemini PDF"
				if gemini == "truncate" {
					reason, text = "MAX_TOKENS", "<!-- Page 1 -->\nPartial"
				}
				return jsonReply(map[string]any{"candidates": []any{map[string]any{"finishReason": reason, "content": map[string]any{"parts": []any{map[string]any{"text": text}}}}}})
			}
		case datalab != "none" && strings.HasSuffix(c.URL, "/files/upload"):
			return jsonReply(map[string]any{"file_id": 7, "upload_url": "https://storage.test/put/abc", "reference": "datalab://file-7"})
		case datalab != "none" && strings.Contains(c.URL, "/put/"):
			return reply(200, "")
		case datalab != "none" && strings.HasSuffix(c.URL, "/files/7/confirm"):
			return jsonReply(map[string]any{"file_id": 7, "reference": "datalab://file-7", "message": "confirmed"})
		case datalab != "none" && strings.HasSuffix(c.URL, "/files/7"):
			return reply(200, "")
		case strings.Contains(c.URL, "/convert") || strings.Contains(c.URL, "/check"):
			if datalab == "fail" {
				return netReply{Status: 500, Body: `{"detail":"boom"}`, Header: hdr("content-type", "application/json")}
			}
			if strings.Contains(c.URL, "/convert") {
				return jsonReply(map[string]any{"status": "processing", "request_check_url": "/check/1"})
			}
			return jsonReply(map[string]any{"status": "complete", "success": true, "markdown": "<!-- Page 1 -->\nDatalab PDF", "page_count": 1})
		}
		return netReply{Err: fmt.Errorf("unexpected fetch: %s", c.URL)}
	})
	out := t.TempDir()
	r, err := ExtractPDFToMarkdown(ctx, makePDF("Hello PDF\nSecond (line)"), "https://example.test/hello.pdf", PDFExtractOptions{OutputDir: out})
	if err != nil {
		return chainRun{err: err, net: n}
	}
	saved, rerr := os.ReadFile(r.OutputPath)
	noErr(t, rerr)
	if r.Content != string(saved) || r.Chars != jsLen(string(saved)) {
		t.Fatal("returned PDF Markdown must match the saved file and character count")
	}
	if datalab != "none" && provider != "gemini" && provider != "unpdf" {
		if _, ok := n.find(func(c netCall) bool { return c.Method == "PUT" }); ok {
			waitDeleted(t, n)
		}
	}
	return chainRun{content: r.Content, net: n}
}

func (r chainRun) must(t *testing.T) string {
	t.Helper()
	noErr(t, r.err)
	return r.content
}

func TestUpstream_pdf_extract(t *testing.T) {
	const F = "pdf-extract"
	tskip(t, F, "extractPDFToMarkdown works on Node 22 without native Promise.try", "the case guards the Promise.try shim that unpdf needs on Node 22; the Go extractor has no Node runtime. The text it checks (Hello PDF, Second (line)) is asserted by TestPDFExtractTextFixture")
	tw(t, F, "extractPDFToMarkdown uses Gemini before loading unpdf", func(t *testing.T) {
		// Garbage input proves the local extractor is not reached once Gemini answered.
		_, _ = isolate(t)
		t.Setenv("GEMINI_API_KEY", "synthetic-gemini-key")
		useNet(t, func(netCall) netReply {
			return jsonReply(map[string]any{"candidates": []any{map[string]any{"finishReason": "STOP", "content": map[string]any{"parts": []any{map[string]any{"text": "<!-- Page 1 -->\nGemini PDF"}}}}}})
		})
		r, err := ExtractPDFToMarkdown(bg(), []byte("not a pdf at all"), "https://example.test/hello.pdf", PDFExtractOptions{OutputDir: t.TempDir()})
		noErr(t, err)
		if !strings.Contains(r.Content, "Gemini PDF") {
			t.Fatal(r.Content)
		}
	})
	tw(t, F, "extractPDFToMarkdown falls back to unpdf when Gemini output is truncated", func(t *testing.T) {
		if c := runChain(t, bg(), "truncate", "none", "").must(t); !strings.Contains(c, "Hello PDF") {
			t.Fatal(c)
		}
	})
	tw(t, F, "extractPDFToMarkdown preserves caller cancellation without local fallback", func(t *testing.T) {
		ctx, cancel := context.WithCancel(bg())
		cancel()
		r := runChain(t, ctx, "abort", "none", "")
		if !errors.Is(r.err, context.Canceled) || strings.Contains(r.content, "Hello PDF") {
			t.Fatalf("content %q error %v", r.content, r.err)
		}
	})
	tskip(t, F, "extractPDFToMarkdown passes PDF.js errors-only verbosity", "the case checks the verbosity option handed to PDF.js; the Go extractor has no PDF.js and logs nothing")
	tw(t, F, "extractPDFToMarkdown uses Datalab before unpdf when configured", func(t *testing.T) {
		if c := runChain(t, bg(), "none", "success", "").must(t); !strings.Contains(c, "Datalab PDF") {
			t.Fatal(c)
		}
	})
	tw(t, F, "extractPDFToMarkdown falls back to unpdf when Datalab conversion fails", func(t *testing.T) {
		if c := runChain(t, bg(), "none", "fail", "").must(t); !strings.Contains(c, "Hello PDF") {
			t.Fatal(c)
		}
	})
	tw(t, F, "extractPDFToMarkdown auto order runs Datalab before Gemini", func(t *testing.T) {
		c := runChain(t, bg(), "success", "success", "").must(t)
		if !strings.Contains(c, "Datalab PDF") || strings.Contains(c, "Gemini PDF") {
			t.Fatal(c)
		}
	})
	tw(t, F, "extractPDFToMarkdown auto order falls back from Datalab to Gemini", func(t *testing.T) {
		c := runChain(t, bg(), "success", "fail", "").must(t)
		if !strings.Contains(c, "Gemini PDF") || strings.Contains(c, "Datalab PDF") {
			t.Fatal(c)
		}
	})
	tw(t, F, "extractPDFToMarkdown provider=datalab skips Gemini even with a Gemini key", func(t *testing.T) {
		c := runChain(t, bg(), "success", "success", "datalab").must(t)
		if !strings.Contains(c, "Datalab PDF") || strings.Contains(c, "Gemini PDF") {
			t.Fatal(c)
		}
	})
	tw(t, F, "extractPDFToMarkdown provider=datalab without a key skips Gemini", func(t *testing.T) {
		c := runChain(t, bg(), "success", "none", "datalab").must(t)
		if !strings.Contains(c, "Hello PDF") || strings.Contains(c, "Gemini PDF") {
			t.Fatal(c)
		}
	})
	tw(t, F, "extractPDFToMarkdown provider=gemini skips Datalab even with a Datalab key", func(t *testing.T) {
		c := runChain(t, bg(), "success", "success", "gemini").must(t)
		if !strings.Contains(c, "Gemini PDF") || strings.Contains(c, "Datalab PDF") {
			t.Fatal(c)
		}
	})
	tw(t, F, "extractPDFToMarkdown provider=unpdf skips Gemini even with a Gemini key", func(t *testing.T) {
		c := runChain(t, bg(), "success", "none", "unpdf").must(t)
		if !strings.Contains(c, "Hello PDF") || strings.Contains(c, "Gemini PDF") {
			t.Fatal(c)
		}
	})
}

// The text of the upstream fixture through the local extractor, with the saved Markdown layout.
func TestPDFExtractTextFixture(t *testing.T) {
	_, _ = isolate(t)
	out := t.TempDir()
	r, err := ExtractPDFToMarkdown(bg(), makePDF("Hello PDF\nSecond (line)"), "https://example.test/hello.pdf", PDFExtractOptions{OutputDir: out})
	noErr(t, err)
	want := "# hello\n\n> Source: https://example.test/hello.pdf\n> Pages: 1\n\n---\n\nHello PDF Second (line)"
	if r.Content != want || r.Title != "hello" || r.Pages != 1 || r.Chars != len(want) || r.OutputPath != filepath.Join(out, "hello.md") {
		t.Fatalf("%+v", r)
	}
	if st, err := os.Stat(r.OutputPath); err != nil || st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("the saved file must be private: %v %v", st, err)
	}
}

// unpdfRecord is one fixture of testdata/pdf-unpdf.json, the original's output
// (port/record-pdf-extract.mjs).
type unpdfRecord struct {
	Title   string `json:"title"`
	Pages   int    `json:"pages"`
	Chars   int    `json:"chars"`
	File    string `json:"file"`
	Content string `json:"content"`
	Error   string `json:"error"`
}

type unpdfRecording struct {
	PDFs map[string]unpdfRecord `json:"pdfs"`
	URLs map[string]struct {
		Title string `json:"title"`
		File  string `json:"file"`
	} `json:"urls"`
}

func loadUnpdfRecording(t *testing.T) unpdfRecording {
	t.Helper()
	data, err := os.ReadFile("testdata/pdf-unpdf.json")
	noErr(t, err)
	var rec unpdfRecording
	noErr(t, json.Unmarshal(data, &rec))
	return rec
}

// pdfDifference pins the Go outcome of a fixture on which it differs from the original's.
type pdfDifference struct {
	reason  string
	wantErr string
}

// knownPDFDifferences are the fixtures on which the Go extractor's outcome differs from the
// original's, each pinned and explained in port/PORT.md ("Slice: PDF text extraction").
var knownPDFDifferences = map[string]pdfDifference{
	"encrypted-aes256-empty.pdf": {
		reason:  "the library reads encryption up to AES-128 (V4); unpdf's PDF.js needs Math.sumPrecise for AES-256, which PiG's Node 24 lacks, so the original fails here too, with another message",
		wantErr: "Invalid PDF structure.",
	},
}

// Every fixture of testdata/pdf through the local extractor against what the original made of it.
func TestPDFCorpusAgainstUnpdf(t *testing.T) {
	rec := loadUnpdfRecording(t)
	var names []string
	for name := range rec.PDFs {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) < 15 {
		t.Fatalf("only %d recorded fixtures", len(names))
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			want := rec.PDFs[name]
			_, agentDir := isolate(t)
			writeConfig(t, agentDir, `{"pdf":{"provider":"unpdf"}}`)
			data, err := os.ReadFile(filepath.Join("testdata", "pdf", name))
			noErr(t, err)
			out := t.TempDir()
			got, err := ExtractPDFToMarkdown(bg(), data, "https://example.test/docs/"+name, PDFExtractOptions{OutputDir: out})
			if diff, known := knownPDFDifferences[name]; known {
				if err == nil || err.Error() != diff.wantErr {
					t.Fatalf("pinned difference: error = %v, want %q (%s)", err, diff.wantErr, diff.reason)
				}
				t.Log("recorded difference: " + diff.reason)
				return
			}
			if want.Error != "" {
				if err == nil || err.Error() != want.Error {
					t.Fatalf("error = %v, want %q", err, want.Error)
				}
				return
			}
			noErr(t, err)
			if got.Content != want.Content || got.Title != want.Title || got.Pages != want.Pages || got.Chars != want.Chars || filepath.Base(got.OutputPath) != want.File {
				t.Fatalf("got title %q pages %d chars %d file %s\n%s\nwant title %q pages %d chars %d file %s\n%s", got.Title, got.Pages, got.Chars, filepath.Base(got.OutputPath), clip(got.Content), want.Title, want.Pages, want.Chars, want.File, clip(want.Content))
			}
		})
	}
}

func clip(s string) string {
	if len(s) > 600 {
		return s[:600] + "..."
	}
	return s
}

// The title and file name derived from a URL (extractTitleFromURL, sanitizeFilename).
func TestPDFTitleAndFileFromURL(t *testing.T) {
	rec := loadUnpdfRecording(t)
	data, err := os.ReadFile("testdata/pdf/text.pdf")
	noErr(t, err)
	if len(rec.URLs) < 10 {
		t.Fatalf("only %d recorded URLs", len(rec.URLs))
	}
	for u, want := range rec.URLs {
		t.Run(u, func(t *testing.T) {
			_, agentDir := isolate(t)
			writeConfig(t, agentDir, `{"pdf":{"provider":"unpdf"}}`)
			got, err := ExtractPDFToMarkdown(bg(), data, u, PDFExtractOptions{OutputDir: t.TempDir()})
			noErr(t, err)
			if got.Title != want.Title || filepath.Base(got.OutputPath) != want.File {
				t.Fatalf("title %q file %q, want %q %q", got.Title, filepath.Base(got.OutputPath), want.Title, want.File)
			}
		})
	}
}

// maxPages caps the pages read and the page count line and notice say so (pdf-extract.ts:226-285).
func TestPDFMaxPagesTruncation(t *testing.T) {
	_, agentDir := isolate(t)
	writeConfig(t, agentDir, `{"pdf":{"provider":"unpdf","maxPages":2}}`)
	data, err := os.ReadFile("testdata/pdf/many-pages.pdf")
	noErr(t, err)
	r, err := ExtractPDFToMarkdown(bg(), data, "https://example.test/many.pdf", PDFExtractOptions{OutputDir: t.TempDir()})
	noErr(t, err)
	if !strings.Contains(r.Content, "> Pages: 120 (extracted first 2)") || !strings.HasSuffix(r.Content, "\n\n---\n\n*[Truncated: Only first 2 of 120 pages extracted]*") || strings.Contains(r.Content, "Page 3 body") {
		t.Fatal(r.Content)
	}
	one := 1
	r, err = ExtractPDFToMarkdown(bg(), data, "https://example.test/many.pdf", PDFExtractOptions{OutputDir: t.TempDir(), MaxPages: &one})
	noErr(t, err)
	if !strings.Contains(r.Content, "(extracted first 1)") {
		t.Fatal(r.Content)
	}
}

// A config that cannot be parsed ends the chain instead of falling through to the local extractor.
func TestPDFConfigParseErrorIsFatal(t *testing.T) {
	_, agentDir := isolate(t)
	writeConfig(t, agentDir, `{"pdf": `)
	_, err := ExtractPDFToMarkdown(bg(), makePDF("x"), "https://example.test/x.pdf", PDFExtractOptions{OutputDir: t.TempDir()})
	wantErr(t, err, `^Failed to parse `)
}

// A cancellation between pages ends the local extraction with the context's error.
func TestPDFCancelDuringLocalExtraction(t *testing.T) {
	_, agentDir := isolate(t)
	writeConfig(t, agentDir, `{"pdf":{"provider":"unpdf"}}`)
	data, err := os.ReadFile("testdata/pdf/many-pages.pdf")
	noErr(t, err)
	ctx, cancel := context.WithCancel(bg())
	cancel()
	_, err = ExtractPDFToMarkdown(ctx, data, "https://example.test/many.pdf", PDFExtractOptions{OutputDir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatal(err)
	}
}

// The chain stops on the errors the original rethrows: a bad Datalab processing location (a
// config parse error) and a failing credential command, instead of falling back to local text.
func TestPDFChainRethrowsFatalDatalabErrors(t *testing.T) {
	_, agentDir := isolate(t)
	t.Setenv("DATALAB_API_KEY", "synthetic-datalab-key")
	useNet(t, func(netCall) netReply { return netReply{Err: errors.New("must not be reached")} })
	t.Setenv("DATALAB_PROCESSING_LOCATION", "mars")
	_, err := ExtractPDFToMarkdown(bg(), makePDF("x"), "https://example.test/x.pdf", PDFExtractOptions{OutputDir: t.TempDir()})
	wantErr(t, err, `^Failed to parse DATALAB_PROCESSING_LOCATION`)
	unsetenv(t, "DATALAB_PROCESSING_LOCATION", "DATALAB_API_KEY")
	writeConfig(t, agentDir, `{"datalabApiKey":"!exit 3"}`)
	_, err = ExtractPDFToMarkdown(bg(), makePDF("x"), "https://example.test/x.pdf", PDFExtractOptions{OutputDir: t.TempDir()})
	var credErr *CredentialResolutionError
	if !errors.As(err, &credErr) {
		t.Fatalf("error %v", err)
	}
}

// maxPages is clamped to at least one page, and a document with exactly maxPages pages is whole.
func TestPDFMaxPagesBounds(t *testing.T) {
	_, agentDir := isolate(t)
	writeConfig(t, agentDir, `{"pdf":{"provider":"unpdf"}}`)
	data, err := os.ReadFile("testdata/pdf/multipage.pdf")
	noErr(t, err)
	zero, three := 0, 3
	r, err := ExtractPDFToMarkdown(bg(), data, "https://example.test/m.pdf", PDFExtractOptions{OutputDir: t.TempDir(), MaxPages: &zero})
	noErr(t, err)
	if !strings.Contains(r.Content, "> Pages: 3 (extracted first 1)") {
		t.Fatal(r.Content)
	}
	r, err = ExtractPDFToMarkdown(bg(), data, "https://example.test/m.pdf", PDFExtractOptions{OutputDir: t.TempDir(), MaxPages: &three})
	noErr(t, err)
	if strings.Contains(r.Content, "Truncated") || strings.Contains(r.Content, "extracted first") {
		t.Fatal(r.Content)
	}
}
