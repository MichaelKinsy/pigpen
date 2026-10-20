package websearch

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Port of gemini-url-context.ts: extract a page through Gemini's URL context tool, or through
// Gemini Web, as the last hosted fetch provider.

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
func geminiShouldRethrow(err error) bool {
	var credErr *CredentialResolutionError
	return errors.As(err, &credErr) || strings.HasPrefix(err.Error(), "Failed to parse ")
}

func geminiExtracted(url, content string) *ExtractedContent {
	title, ok := ExtractHeadingTitle(content)
	if !ok {
		title = lastPathSegment(url)
	}
	return &ExtractedContent{URL: url, Title: title, Content: content}
}

// ExtractWithURLContext is extractWithUrlContext: nil, nil when Gemini API access is not
// configured or the page could not be read.
func ExtractWithURLContext(ctx context.Context, url string) (*ExtractedContent, error) {
	tctx, cancel := withTimeout(ctx, geminiURLContextTimeout)
	defer cancel()
	adc, err := geminiADCAvailable()
	if err != nil {
		return nil, err
	}
	var apiKey string
	if !adc {
		if apiKey, err = GetAPIKey(tctx); err != nil {
			return nil, err
		}
	}
	gw, err := geminiGatewayConfigured()
	if err != nil {
		return nil, err
	}
	if apiKey == "" && !gw && !adc {
		return nil, nil
	}
	body := geminiRequestBody([]any{map[string]any{"url_context": map[string]any{}}}, geminiTextPart{geminiExtractionPrompt + url})
	res, err := geminiGenerate(tctx, DefaultGeminiModel, body, &apiKey)
	if err != nil {
		if geminiShouldRethrow(err) {
			return nil, err
		}
		return nil, nil
	}
	if !isHTTPOK(res) {
		_ = res.Body.Close()
		return nil, nil
	}
	data, err := geminiJSON(res)
	if err != nil {
		if geminiShouldRethrow(err) {
			return nil, err
		}
		return nil, nil
	}
	if meta, _ := jsonPath(data, "candidates", 0, "url_context_metadata", "url_metadata").([]any); len(meta) > 0 {
		switch strField(jsonPath(meta[0], "url_retrieval_status")) {
		case "URL_RETRIEVAL_STATUS_UNSAFE", "URL_RETRIEVAL_STATUS_ERROR":
			return nil, nil
		}
	}
	content := geminiCandidateText(data)
	if content == "" || jsLen(content) < geminiMinContentLength {
		return nil, nil
	}
	return geminiExtracted(url, content), nil
}

// ExtractWithGeminiWeb is extractWithGeminiWeb: nil, nil when Gemini Web is unavailable or empty.
func ExtractWithGeminiWeb(ctx context.Context, url string) (*ExtractedContent, error) {
	cookies, err := geminiWebClient.Available(ctx)
	if err != nil || cookies == nil {
		return nil, err
	}
	text, err := geminiWebClient.Query(ctx, geminiExtractionPrompt+url, cookies, geminiURLContextTimeout)
	if err != nil {
		if geminiShouldRethrow(err) {
			return nil, err
		}
		return nil, nil
	}
	if text == "" || jsLen(text) < geminiMinContentLength {
		return nil, nil
	}
	return geminiExtracted(url, text), nil
}
