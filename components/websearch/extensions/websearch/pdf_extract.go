package websearch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Port of pdf-extract.ts: PDF to Markdown. The auto chain runs Datalab, then the Gemini API,
// then the local extractor (pdf_text.go, standing in for unpdf); pdf.provider pins a link.

// PDFExtractOptions are PDFExtractOptions upstream. A nil MaxPages reads pdf.maxPages; an empty
// OutputDir is <tmp>/pi-web-pdf.
type PDFExtractOptions struct {
	MaxPages      *int
	OutputDir     string
	Filename      string
	GeminiTimeout time.Duration
}

// PDFExtractResult is PDFExtractResult upstream: Content is the Markdown also written to
// OutputPath, Chars its length in UTF-16 units.
type PDFExtractResult struct {
	Title      string
	Content    string
	Pages      int
	Chars      int
	OutputPath string
}

// ExtractPDFToMarkdown extracts the text of a PDF and saves it as a Markdown file.
func ExtractPDFToMarkdown(ctx context.Context, buffer []byte, rawURL string, o PDFExtractOptions) (PDFExtractResult, error) {
	cfg, err := loadPDFConfig()
	if err != nil {
		return PDFExtractResult{}, err
	}
	maxPages := cfg.MaxPages
	if o.MaxPages != nil {
		maxPages = max(1, *o.MaxPages)
	}
	outputDir := o.OutputDir
	if outputDir == "" {
		outputDir = filepath.Join(os.TempDir(), "pi-web-pdf")
	}
	urlTitle := extractTitleFromURL(rawURL)
	write := func(body, title string, pages int, extra pdfMarkdownExtra) (PDFExtractResult, error) {
		return writeMarkdownResult(pdfMarkdown{body: body, title: title, pages: pages, outputDir: outputDir, filename: o.Filename, url: rawURL, pdfMarkdownExtra: extra})
	}

	if cfg.Provider == "auto" || cfg.Provider == "datalab" {
		if IsDatalabAPIAvailable() {
			r, err := ExtractPDFViaDatalab(ctx, buffer, DatalabPDFOptions{MaxPages: maxPages, Title: urlTitle, Mode: cfg.DatalabMode, Timeout: time.Duration(cfg.DatalabTimeoutMs * float64(time.Millisecond))})
			if err == nil {
				return write(r.Markdown, urlTitle, r.Pages, pdfMarkdownExtra{})
			}
			if shouldRethrowExtractionError(ctx, err) {
				return PDFExtractResult{}, err
			}
		}
	}

	if cfg.Provider == "auto" || cfg.Provider == "gemini" {
		if IsGeminiAPIAvailable() {
			body, err := ExtractPDFViaGemini(ctx, buffer, GeminiPDFOptions{MaxPages: maxPages, Title: urlTitle, Timeout: o.GeminiTimeout})
			if err == nil {
				return write(body, urlTitle, countPageMarkers(body), pdfMarkdownExtra{})
			}
			if shouldRethrowExtractionError(ctx, err) {
				return PDFExtractResult{}, err
			}
		}
	}

	doc, err := openPDF(buffer)
	if err != nil {
		return PDFExtractResult{}, err
	}
	title := urlTitle
	if t, ok := doc.infoString("Title"); ok && jsTrim(t) != "" {
		title = jsTrim(t)
	}
	author, _ := doc.infoString("Author")
	numPages := doc.numPages()
	pagesToExtract := min(numPages, maxPages)
	type page struct {
		number int
		text   string
	}
	var pages []page
	for i := 1; i <= pagesToExtract; i++ {
		if err := ctx.Err(); err != nil {
			return PDFExtractResult{}, err
		}
		text, err := doc.pageText(ctx, i)
		if err != nil {
			return PDFExtractResult{}, err
		}
		if text != "" {
			pages = append(pages, page{i, text})
		}
	}
	var body strings.Builder
	for i, p := range pages {
		if i > 0 {
			fmt.Fprintf(&body, "\n\n<!-- Page %d -->\n\n", p.number)
		}
		body.WriteString(p.text)
	}
	return write(body.String(), title, numPages, pdfMarkdownExtra{author: author, truncated: numPages > maxPages, pagesToExtract: pagesToExtract})
}

type pdfMarkdownExtra struct {
	author         string
	truncated      bool
	pagesToExtract int
}

type pdfMarkdown struct {
	body, title string
	pages       int
	outputDir   string
	filename    string
	url         string
	pdfMarkdownExtra
}

func writeMarkdownResult(o pdfMarkdown) (PDFExtractResult, error) {
	lines := []string{"# " + o.title, "", "> Source: " + o.url}
	pages := fmt.Sprintf("> Pages: %d", o.pages)
	if o.truncated {
		pages += fmt.Sprintf(" (extracted first %d)", o.pagesToExtract)
	}
	lines = append(lines, pages)
	if o.author != "" {
		lines = append(lines, "> Author: "+o.author)
	}
	lines = append(lines, "", "---", "")
	if o.body != "" {
		lines = append(lines, o.body)
	}
	if o.truncated {
		lines = append(lines, "", "---", "", fmt.Sprintf("*[Truncated: Only first %d of %d pages extracted]*", o.pagesToExtract, o.pages))
	}
	content := strings.Join(lines, "\n")
	name := o.filename
	if name == "" {
		name = sanitizeFilename(o.title) + ".md"
	}
	outputPath := filepath.Join(o.outputDir, name)
	// The saved text is the user's document: private to the user, unlike the original's default
	// permissions (docs/PORT.md, deliberate differences).
	if err := os.MkdirAll(o.outputDir, 0o700); err != nil {
		return PDFExtractResult{}, err
	}
	if err := os.WriteFile(outputPath, []byte(content), 0o600); err != nil {
		return PDFExtractResult{}, err
	}
	return PDFExtractResult{Title: o.title, Content: content, Pages: o.pages, Chars: jsLen(content), OutputPath: outputPath}, nil
}

// shouldRethrowExtractionError: a cancellation, a credential failure or an unreadable config
// ends the chain; any other provider failure falls through to the next one.
func shouldRethrowExtractionError(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return true
	}
	var credErr *CredentialResolutionError
	return errors.As(err, &credErr) || isConfigParseError(err.Error())
}

var (
	arxivPathRE     = regexp.MustCompile(`/(?:pdf|abs)/(\d+\.\d+)`)
	titleSeparators = regexp.MustCompile(`[_-]+`)
	filenameBad     = regexp.MustCompile(`[^a-z0-9` + jsSpaceSet + `-]`)
	filenameDashRun = regexp.MustCompile(`-+`)
	filenameSpace   = regexp.MustCompile(`[` + jsSpaceSet + `]+`)
)

// jsSpaceSet is the character class body of JavaScript's \s.
const jsSpaceSet = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

// extractTitleFromURL is a reasonable title from the URL (pdf-extract.ts:362).
func extractTitleFromURL(rawURL string) string {
	u, err := ParseURL(rawURL, nil)
	if err != nil {
		return "document"
	}
	pathname := u.EscapedPath()
	if pathname == "" {
		pathname = "/"
	}
	filename := nodeBasename(pathname, ".pdf")
	if strings.Contains(u.Hostname(), "arxiv.org") {
		if m := arxivPathRE.FindStringSubmatch(pathname); m != nil {
			filename = "arxiv-" + m[1]
		}
	}
	filename = jsTrim(collapseSpaces(titleSeparators.ReplaceAllString(filename, " ")))
	if filename == "" {
		return "document"
	}
	return filename
}

// nodeBasename is path.basename(p, ext): trailing slashes are ignored and the extension is only
// removed when something else remains.
func nodeBasename(p, ext string) string {
	p = strings.TrimRight(p, "/")
	base := path.Base(p)
	if p == "" || base == "/" {
		return ""
	}
	if ext != "" && base != ext && strings.HasSuffix(base, ext) {
		base = strings.TrimSuffix(base, ext)
	}
	return base
}

// sanitizeFilename is the file name stem of a title (pdf-extract.ts:387).
func sanitizeFilename(name string) string {
	s := strings.ToLower(name)
	s = filenameBad.ReplaceAllString(s, "")
	s = filenameSpace.ReplaceAllString(s, "-")
	s = filenameDashRun.ReplaceAllString(s, "-")
	s = jsSlice(s, 0, 100)
	s = strings.TrimPrefix(s, "-")
	s = strings.TrimSuffix(s, "-")
	if s == "" {
		return "document"
	}
	return s
}
