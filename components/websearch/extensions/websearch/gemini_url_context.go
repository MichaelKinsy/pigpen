package websearch

import (
	"context"
	"errors"

	"time"
)

const (
	geminiExtractionPrompt = "Extract the complete readable content from this URL as clean markdown.\n" +
		"Include the page title, all text content, code blocks, and tables.\n" +
		"Do not summarize — extract the full content.\n\n" +
		"URL: "
	geminiURLContextTimeout = 60 * time.Second
	geminiMinContentLength  = 50
)

// geminiShouldRethrow is shouldRethrow: a credential failure or an unreadable config is shown to
// the user instead of being treated as "this provider found nothing".
func geminiShouldRethrow(err error) bool { return *new(bool) }

func geminiExtracted(url, content string) *ExtractedContent { return *new(*ExtractedContent) }

// ExtractWithURLContext is extractWithUrlContext: nil, nil when Gemini API access is not
// configured or the page could not be read.
func ExtractWithURLContext(ctx context.Context, url string) (*ExtractedContent, error) {
	return *new(*ExtractedContent), errors.New("websearch-gemini: not implemented")
}

// ExtractWithGeminiWeb is extractWithGeminiWeb: nil, nil when Gemini Web is unavailable or empty.
func ExtractWithGeminiWeb(ctx context.Context, url string) (*ExtractedContent, error) {
	return *new(*ExtractedContent), errors.New("websearch-gemini: not implemented")
}
