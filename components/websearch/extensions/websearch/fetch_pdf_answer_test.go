package websearch

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Twins of fetch-pdf-answer.test.mjs. The answer-mode cases need the model call of fetch_content
// mode answer, which is another slice (docs/PORT.md); what they pin about the PDF side is covered
// below at the extraction level (TestPDFAnswerModeExtraction*).

const answerModelReason = "fetch_content mode answer is refused until the answer-mode slice ports its model call (tool_fetch.go), so the case's answer-model assertions have no Go counterpart yet; the PDF side of it is asserted by "

func pdfFetchEnv(t *testing.T, body []byte) {
	t.Helper()
	t.Setenv("TMPDIR", t.TempDir())
	useNet(t, func(netCall) netReply {
		return netReply{Status: 200, Header: hdr("content-type", "application/pdf"), Body: string(body)}
	})
}

func TestUpstream_fetch_pdf_answer(t *testing.T) {
	const F = "fetch-pdf-answer"
	tskip(t, F, "PDF answer mode supplies extracted Markdown, saves the file, and retains original content for retrieval", answerModelReason+"TestPDFAnswerModeExtractionSavesAndReturnsMarkdown")
	tw(t, F, "readable PDF mode preserves the file-path presentation without calling an answer model", func(t *testing.T) {
		r, _ := newRuntime(t, `{"pdf":{"provider":"unpdf"},"fetchRouting":{"providers":["http"]}}`)
		pdfFetchEnv(t, makePDF("Readable PDF body."))
		out, err := execTool(t, r, "fetch_content", map[string]any{"url": "https://93.184.216.34/readable.pdf"})
		noErr(t, err)
		if out.Details["successful"] != 1 {
			t.Fatalf("details %v", out.Details)
		}
		notice := out.Text()
		matchRE(t, `^PDF extracted and saved to: .+\n\nPages: 1\nCharacters: \d+$`, notice)
		if strings.Contains(notice, "Readable PDF body") {
			t.Fatal("the notice must not carry the body")
		}
		path := strings.SplitN(notice, "\n", 2)[0][len("PDF extracted and saved to: "):]
		saved, err := os.ReadFile(path)
		noErr(t, err)
		if !strings.Contains(string(saved), "Readable PDF body.") {
			t.Fatal(string(saved))
		}
		chars, _ := strconv.Atoi(regexp.MustCompile(`Characters: (\d+)`).FindStringSubmatch(notice)[1])
		if chars != jsLen(string(saved)) {
			t.Fatalf("characters %d, saved %d", chars, jsLen(string(saved)))
		}
		id, _ := out.Details["responseId"].(string)
		if stored := GetResult(id); stored == nil || stored.URLs[0].Content != notice {
			t.Fatalf("stored %v", stored)
		}
	})
	tskip(t, F, "batch PDF answers receive each document's own body even when output filenames collide", answerModelReason+"TestPDFAnswerModeExtractionBatchKeepsOwnBody")
	tskip(t, F, "PDF answers keep the model input bound while retaining the full extracted text", answerModelReason+"TestPDFAnswerModeExtractionSavesAndReturnsMarkdown (the full text is retained); the input bound belongs to the model call")
	tskip(t, F, "HTML answer mode still supplies the extracted page body", answerModelReason+"TestPDFAnswerModeExtractionHTMLBody")
	tskip(t, F, "disabled PDF extraction and raw binary fetches do not call an answer model", answerModelReason+"TestPDFAnswerModeExtractionDisabledAndRaw")
	tskip(t, F, "a cancelled PDF answer does not fetch or invoke an answer model", answerModelReason+"TestPDFAnswerModeExtractionCancelled")
}

func answerOpts() ExtractOptions {
	o := pageOpts()
	o.Mode = "answer"
	return o
}

func TestPDFAnswerModeExtractionSavesAndReturnsMarkdown(t *testing.T) {
	extractEnv(t, `{"pdf":{"provider":"unpdf"},"fetchRouting":{"providers":["http"]}}`)
	pdfFetchEnv(t, makePDF("The annual fee is 42 dollars."))
	res := ExtractContent(bg(), "https://93.184.216.34/fees.pdf", answerOpts())
	if res.Error != nil || !strings.Contains(res.Content, "The annual fee is 42 dollars.") || strings.Contains(res.Content, "PDF extracted and saved to:") {
		t.Fatalf("%+v", res)
	}
	files, err := os.ReadDir(os.Getenv("TMPDIR") + "/pi-web-pdf")
	noErr(t, err)
	if len(files) != 1 {
		t.Fatalf("%d files", len(files))
	}
	saved, _ := os.ReadFile(os.Getenv("TMPDIR") + "/pi-web-pdf/" + files[0].Name())
	if string(saved) != res.Content {
		t.Fatal("the saved file must equal the returned content")
	}
}

func TestPDFAnswerModeExtractionBatchKeepsOwnBody(t *testing.T) {
	extractEnv(t, `{"pdf":{"provider":"unpdf"},"fetchRouting":{"providers":["http"]}}`)
	t.Setenv("TMPDIR", t.TempDir())
	useNet(t, func(c netCall) netReply {
		text := "Second document body."
		if strings.Contains(c.URL, "/first/") {
			text = "First document body."
		}
		return netReply{Status: 200, Header: hdr("content-type", "application/pdf"), Body: string(makePDF(text))}
	})
	res := FetchAllContent(bg(), []string{"https://93.184.216.34/first/report.pdf", "https://93.184.216.34/second/report.pdf"}, answerOpts())
	if !strings.Contains(res[0].Content, "First document body.") || !strings.Contains(res[1].Content, "Second document body.") || res[0].Error != nil || res[1].Error != nil {
		t.Fatalf("%+v", res)
	}
}

func TestPDFAnswerModeExtractionHTMLBody(t *testing.T) {
	extractEnv(t, `{"fetchRouting":{"providers":["http"]}}`)
	page := `<!doctype html><html><head><title>HTML page</title></head><body><article><h1>HTML page</h1><p>` + strings.Repeat("HTML original body. ", 80) + `</p></article></body></html>`
	useNet(t, func(netCall) netReply { return netReply{Status: 200, Header: hdr("content-type", "text/html"), Body: page} })
	res := ExtractContent(bg(), "https://93.184.216.34/page", answerOpts())
	if res.Error != nil || !strings.Contains(res.Content, "HTML original body") {
		t.Fatalf("%+v", res)
	}
}

func TestPDFAnswerModeExtractionDisabledAndRaw(t *testing.T) {
	extractEnv(t, `{"pdf":{"enabled":false,"provider":"unpdf"},"fetchRouting":{"providers":["http"]}}`)
	pdfFetchEnv(t, makePDF("Do not answer this."))
	disabled := ExtractContent(bg(), "https://93.184.216.34/disabled.pdf", answerOpts())
	matchRE(t, `PDF extraction is disabled`, errOf(disabled))
	extractEnv(t, `{"pdf":{"provider":"unpdf"},"fetchRouting":{"providers":["http"]}}`)
	raw := pageOpts()
	raw.Mode = "raw"
	matchRE(t, `Unsupported content type in raw mode`, errOf(ExtractContent(bg(), "https://93.184.216.34/raw.pdf", raw)))
}

func TestPDFAnswerModeExtractionCancelled(t *testing.T) {
	extractEnv(t, `{"pdf":{"provider":"unpdf"},"fetchRouting":{"providers":["http"]}}`)
	t.Setenv("TMPDIR", t.TempDir())
	n := useNet(t, func(netCall) netReply { return reply(200, "x") })
	ctx, cancel := contextCanceled()
	defer cancel()
	res := ExtractContent(ctx, "https://93.184.216.34/aborted.pdf", answerOpts())
	matchRE(t, `(?i)abort`, errOf(res))
	if n.count() != 0 {
		t.Fatal("a cancelled fetch must not send requests")
	}
}
