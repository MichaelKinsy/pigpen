package websearch

import (
	"context"

	"errors"

	"regexp"

	"time"
)

const (
	geminiPDFMimeType       = "application/pdf"
	geminiPDFDefaultTimeout = 120 * time.Second
)

var (
	geminiPageMarker  = regexp.MustCompile(`(?m)^<!-- Page (\d+) -->$`)
	geminiFenceRE     = regexp.MustCompile("(?is)^```(?:markdown|md)?[ \\t]*\\n([\\s\\S]*?)\\n```$")
	geminiTitleRE     = regexp.MustCompile(`^(<!-- Page 1 -->\s*\n+)#\s+(.+?)\s*\n+`)
	geminiTitleStrip  = regexp.MustCompile("[`*_~]")
	geminiTitleIgnore = regexp.MustCompile(`[\p{P}\p{S}\s]+`)
)

// GeminiPDFOptions are GeminiPDFExtractOptions upstream. Pages nil means "up to MaxPages".
type GeminiPDFOptions struct {
	Pages    *int
	MaxPages int
	Title    string
	Timeout  time.Duration
}

// ExtractPDFViaGemini returns the PDF as Markdown with one `<!-- Page N -->` marker per page.
func ExtractPDFViaGemini(ctx context.Context, pdf []byte, o GeminiPDFOptions) (string, error) {
	return *new(string), errors.New("websearch-gemini: not implemented")
}

func buildGeminiPDFPrompt(pagesToExtract int, exactPageCount bool) string { return *new(string) }

func stripEnclosingMarkdownFence(value string) string { return *new(string) }

func validateGeminiPageMarkers(markdown string, maxPages int, exact bool) error {
	return errors.New("websearch-gemini: not implemented")
}

func removeDuplicateInitialTitle(markdown, title string) string { return *new(string) }

func normalizeGeminiTitle(value string) string { return *new(string) }
