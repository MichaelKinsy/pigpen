package websearch

import (
	"context"
	"time"
)

// Stubs of the PDF text extraction: the red state of the twins (the port still refuses).

// PDFExtractOptions are PDFExtractOptions upstream.
type PDFExtractOptions struct {
	MaxPages      *int
	OutputDir     string
	Filename      string
	GeminiTimeout time.Duration
}

// PDFExtractResult is PDFExtractResult upstream.
type PDFExtractResult struct {
	Title      string
	Content    string
	Pages      int
	Chars      int
	OutputPath string
}

// ExtractPDFToMarkdown extracts the text of a PDF and saves it as a Markdown file.
func ExtractPDFToMarkdown(context.Context, []byte, string, PDFExtractOptions) (PDFExtractResult, error) {
	return PDFExtractResult{}, unsupported("PDF text extraction")
}
