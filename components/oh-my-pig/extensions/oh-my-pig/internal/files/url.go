package files

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/web"
)

const (
	// fetchDefaultMaxLines matches omp's FETCH_DEFAULT_MAX_LINES (300).
	// upstream: packages/coding-agent/src/tools/fetch.ts:48
	fetchDefaultMaxLines = 300
)

// urlFetcher is overridable in tests to avoid network access while exercising URL read logic.
var urlFetcher = web.Fetch

// parsedURLTarget is an external URL split from its trailing read selector.
// upstream: packages/coding-agent/src/tools/fetch.ts:146
type parsedURLTarget struct {
	URL string
	Sel ParsedSelector
}

// parseReadURLTarget recognizes http://, https:// (including collapsed http:/ / https:/),
// and bare host:port/ targets, peeling trailing omp line/raw selectors.
// upstream: packages/coding-agent/src/tools/fetch.ts:151
func parseReadURLTarget(rawPath string) (*parsedURLTarget, error) {
	repaired := repairCollapsedURLScheme(strings.TrimSpace(rawPath))
	base, selTokens := extractURLSelectors(repaired)
	if !isReadableURLPath(base) {
		return nil, nil
	}

	var raw bool
	var ranges []LineRange
	var tail int
	for _, tok := range selTokens {
		if strings.EqualFold(tok, "raw") {
			raw = true
			continue
		}
		if len(ranges) > 0 || tail > 0 {
			return nil, fmt.Errorf("URL selector has multiple range groups; combine them with commas (e.g. `:5-10,20-30`).")
		}
		p, ok, err := parseRangeOrTail(tok, false)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("Invalid URL line selector: %s", tok)
		}
		if p.Kind == SelectorLines {
			ranges = p.Ranges
		} else if p.Kind == SelectorTail {
			tail = p.Tail
		}
	}

	var sel ParsedSelector
	switch {
	case len(ranges) > 0:
		sel = ParsedSelector{Kind: SelectorLines, Ranges: ranges, Raw: raw}
	case tail > 0:
		sel = ParsedSelector{Kind: SelectorTail, Tail: tail, Raw: raw}
	case raw:
		sel = ParsedSelector{Kind: SelectorRaw, Raw: true}
	default:
		sel = ParsedSelector{Kind: SelectorNone}
	}
	return &parsedURLTarget{URL: base, Sel: sel}, nil
}

func repairCollapsedURLScheme(s string) string {
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "http:/") && !strings.HasPrefix(lower, "http://") {
		return s[:5] + "//" + s[6:]
	}
	if strings.HasPrefix(lower, "https:/") && !strings.HasPrefix(lower, "https://") {
		return s[:6] + "//" + s[7:]
	}
	return s
}

func isReadableURLPath(s string) bool {
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return true
	}
	// Bare host:port/ (must have a trailing slash after port, per read.md: "bare host:port needs trailing slash").
	slash := strings.IndexByte(s, '/')
	if slash <= 0 {
		return false
	}
	authority := s[:slash]
	colon := strings.LastIndexByte(authority, ':')
	if colon <= 0 || colon == len(authority)-1 {
		return false
	}
	host := authority[:colon]
	port := authority[colon+1:]
	if !allDigits(port) {
		return false
	}
	// Host must look like a hostname or IPv4 (no whitespace or path separators).
	if strings.ContainsAny(host, " \t\\") {
		return false
	}
	return true
}

// extractURLSelectors peels up to two trailing `:raw` or `:<range>` tokens from a URL,
// preserving `host:port` by never peeling a `:digits` directly off an authority with no `/`.
func extractURLSelectors(s string) (string, []string) {
	var tokens []string
	cur := s
	for range 2 {
		colon := strings.LastIndexByte(cur, ':')
		if colon < 0 {
			break
		}
		// Never peel the scheme colon (http:// or https://).
		if strings.HasPrefix(cur[colon:], "://") {
			break
		}
		prefix := cur[:colon]
		tok := cur[colon+1:]
		if !isURLSelectorToken(tok) {
			break
		}
		// If prefix has no '/' after the scheme (e.g. "http://127.0.0.1" or "localhost"),
		// a pure-digit token is a port, not a line selector.
		afterScheme := prefix
		if idx := strings.Index(prefix, "://"); idx >= 0 {
			afterScheme = prefix[idx+3:]
		}
		if !strings.Contains(afterScheme, "/") && allDigits(tok) {
			break
		}
		tokens = append(tokens, tok)
		cur = prefix
	}
	// Reverse tokens so they are in left-to-right order.
	for i, j := 0, len(tokens)-1; i < j; i, j = i+1, j-1 {
		tokens[i], tokens[j] = tokens[j], tokens[i]
	}
	return cur, tokens
}

func isURLSelectorToken(tok string) bool {
	if tok == "" || strings.Contains(tok, "/") {
		return false
	}
	if strings.EqualFold(tok, "raw") {
		return true
	}
	return looksLikeRangeOrTail(tok)
}

// parseURLFetchMeta extracts URL/Content-Type/Method header metadata and body from web.Fetch output.
func parseURLFetchMeta(fetched, fallbackURL string, raw bool) (body, finalURL, contentType, method string) {
	finalURL = fallbackURL
	contentType = "text/plain"
	method = "fetch"
	if raw {
		method = "raw"
		contentType = inferRawURLContentType(fetched, fallbackURL)
	}
	if !strings.HasPrefix(fetched, "URL: ") {
		return fetched, finalURL, contentType, method
	}
	sep := "\n\n---\n\n"
	idx := strings.Index(fetched, sep)
	if idx < 0 {
		return fetched, finalURL, contentType, method
	}
	header := fetched[:idx]
	body = fetched[idx+len(sep):]
	for line := range strings.SplitSeq(header, "\n") {
		switch {
		case strings.HasPrefix(line, "URL: "):
			if v := strings.TrimSpace(strings.TrimPrefix(line, "URL: ")); v != "" {
				finalURL = v
			}
		case strings.HasPrefix(line, "Content-Type: "):
			if v := strings.TrimSpace(strings.TrimPrefix(line, "Content-Type: ")); v != "" {
				contentType = v
			}
		case strings.HasPrefix(line, "Method: "):
			if v := strings.TrimSpace(strings.TrimPrefix(line, "Method: ")); v != "" {
				method = v
			}
		}
	}
	return body, finalURL, contentType, method
}

// inferRawURLContentType determines the MIME type for a raw URL response body when
// web.Fetch returns raw bytes without a metadata header (upstream: tools/fetch.ts:1477).
func inferRawURLContentType(body, rawURL string) string {
	trimmed := strings.TrimLeft(body, " \t\r\n")
	lowerPrefix := strings.ToLower(trimmed)
	if len(lowerPrefix) > 32 {
		lowerPrefix = lowerPrefix[:32]
	}
	if strings.HasPrefix(lowerPrefix, "<!doctype html") ||
		strings.HasPrefix(lowerPrefix, "<html") ||
		strings.HasPrefix(lowerPrefix, "<head") ||
		strings.HasPrefix(lowerPrefix, "<body") ||
		strings.HasPrefix(lowerPrefix, "<div") ||
		strings.HasPrefix(lowerPrefix, "<h1") ||
		strings.HasPrefix(lowerPrefix, "<p>") {
		return "text/html"
	}
	if strings.HasPrefix(trimmed, "[Binary content: ") {
		rest := strings.TrimPrefix(trimmed, "[Binary content: ")
		if comma := strings.IndexByte(rest, ','); comma > 0 {
			if ct := strings.TrimSpace(rest[:comma]); ct != "" {
				return ct
			}
		}
	}
	// Fall back to path extension hint before defaulting to text/plain.
	pathPart := rawURL
	if q := strings.IndexAny(pathPart, "?#"); q >= 0 {
		pathPart = pathPart[:q]
	}
	lowerPath := strings.ToLower(pathPart)
	switch {
	case strings.HasSuffix(lowerPath, ".html"), strings.HasSuffix(lowerPath, ".htm"):
		return "text/html"
	case strings.HasSuffix(lowerPath, ".xhtml"):
		return "application/xhtml+xml"
	case strings.HasSuffix(lowerPath, ".json"):
		return "application/json"
	case strings.HasSuffix(lowerPath, ".xml"):
		return "application/xml"
	case strings.HasSuffix(lowerPath, ".md"), strings.HasSuffix(lowerPath, ".markdown"):
		return "text/markdown"
	case strings.HasSuffix(lowerPath, ".css"):
		return "text/css"
	case strings.HasSuffix(lowerPath, ".js"), strings.HasSuffix(lowerPath, ".mjs"):
		return "text/javascript"
	}
	return "text/plain"
}
func makeURLDetails(origURL, finalURL, contentType, method string, raw, truncated bool, truncMeta map[string]any) map[string]any {
	meta := map[string]any{
		"source": map[string]any{"type": "url", "value": finalURL},
	}
	if truncMeta != nil {
		meta["truncation"] = truncMeta
	}
	d := map[string]any{
		"kind":        "url",
		"url":         origURL,
		"finalUrl":    finalURL,
		"contentType": contentType,
		"method":      method,
		"truncated":   truncated,
		"notes":       []string{},
		"meta":        meta,
	}
	if raw {
		d["raw"] = true
	}
	return d
}

// executeReadURL fetches a URL via urlFetcher (default web.Fetch) and applies omp's
// line selectors (against the body, without header) or head truncation (300 lines / 50 KB).
// upstream: packages/coding-agent/src/tools/fetch.ts:1758, read.ts:1715, read-format.ts:314
func executeReadURL(ctx context.Context, target *parsedURLTarget) (sdk.ToolResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	fetched, err := urlFetcher(ctx, target.URL, target.Sel.IsRaw())
	if err != nil {
		return sdk.ToolResult{}, err
	}

	body, finalURL, contentType, method := parseURLFetchMeta(fetched, target.URL, target.Sel.IsRaw())

	// Line-range or tail selector: slice the body (no URL/Content-Type/Method header),
	// with 1-before / 3-after context on single non-raw ranges, no line numbers,
	// and omp's `[N more lines in URL output. Use :M- to continue]` footer when lines remain.
	if target.Sel.Kind == SelectorLines || target.Sel.Kind == SelectorTail {
		norm := strings.ReplaceAll(body, "\r\n", "\n")
		allLines := strings.Split(norm, "\n")
		if strings.HasSuffix(norm, "\n") && len(allLines) > 0 {
			allLines = allLines[:len(allLines)-1]
		}
		totalLines := len(allLines)

		resolved := target.Sel.ResolveTail(totalLines)
		if resolved.IsMultiRange() {
			return sdk.ToolResult{
				Content: renderURLMultiRange(allLines, resolved),
				Details: makeURLDetails(target.URL, finalURL, contentType, method, resolved.IsRaw(), false, nil),
			}, nil
		}

		rawMode := resolved.IsRaw()
		r := resolved.Ranges[0]
		requestedStart := max(0, r.StartLine-1)
		if requestedStart >= totalLines {
			return sdk.ToolResult{
				Content: fmt.Sprintf("Line %d is beyond end of URL output (%d lines total). Use :1- to read from the start, or :%d to read the last line.", r.StartLine, totalLines, totalLines),
				Details: makeURLDetails(target.URL, finalURL, contentType, method, rawMode, false, nil),
			}, nil
		}

		requestedEnd := totalLines
		hasLimit := r.EndLine > 0
		if hasLimit {
			requestedEnd = min(r.EndLine, totalLines)
		}

		// Expand single non-raw range by 1 leading and 3 trailing context lines
		// (upstream: read-format.ts:339).
		startIdx := requestedStart
		endIdx := requestedEnd
		if !rawMode && r.StartLine > 1 {
			startIdx = max(0, requestedStart-RangeLeadingContextLines)
		}
		if !rawMode && hasLimit {
			endIdx = min(totalLines, requestedEnd+RangeTrailingContextLines)
		}

		// Apply byte/line cap on the expanded window.
		var outLines []string
		bytesUsed := 0
		truncated := false
		for i := startIdx; i < endIdx && len(outLines) < DefaultMaxLines; i++ {
			lb := len(allLines[i])
			if len(outLines) > 0 {
				lb++
			}
			if len(outLines) > 0 && bytesUsed+lb > DefaultMaxBytes {
				truncated = true
				break
			}
			outLines = append(outLines, allLines[i])
			bytesUsed += lb
		}
		out := strings.Join(outLines, "\n")
		shownEnd := startIdx + len(outLines)
		var truncMeta map[string]any
		if truncated {
			out += fmt.Sprintf("\n\n[Showing lines %d-%d of %d (%s limit). Use :%d- to continue]",
				startIdx+1, shownEnd, totalLines, formatBytes(DefaultMaxBytes), shownEnd+1)
			truncMeta = map[string]any{
				"direction":   "head",
				"truncatedBy": "bytes",
				"totalLines":  totalLines,
				"totalBytes":  len(body),
				"outputLines": len(outLines),
				"outputBytes": bytesUsed,
				"maxBytes":    DefaultMaxBytes,
				"shownRange":  map[string]any{"start": startIdx + 1, "end": shownEnd},
				"nextOffset":  shownEnd + 1,
			}
		} else if !rawMode && shownEnd < totalLines {
			remaining := totalLines - shownEnd
			out += fmt.Sprintf("\n\n[%d more lines in URL output. Use :%d- to continue]", remaining, shownEnd+1)
		}
		return sdk.ToolResult{
			Content: out,
			Details: makeURLDetails(target.URL, finalURL, contentType, method, rawMode, truncated, truncMeta),
		}, nil
	}

	// Whole URL read (no range/tail selector): keep header block, head-truncate at 300 lines / 50 KB.
	norm := strings.ReplaceAll(fetched, "\r\n", "\n")
	allLines := strings.Split(norm, "\n")
	if strings.HasSuffix(norm, "\n") && len(allLines) > 0 {
		allLines = allLines[:len(allLines)-1]
	}
	totalLines := len(allLines)

	// Whole URL read: head truncation at 300 lines / 50 KB (upstream: fetch.ts:1764).
	var outLines []string
	bytesUsed := 0
	truncatedByBytes := false
	truncatedByLines := false
	for i := range totalLines {
		if len(outLines) >= fetchDefaultMaxLines {
			truncatedByLines = true
			break
		}
		lb := len(allLines[i])
		if len(outLines) > 0 {
			lb++
		}
		if len(outLines) > 0 && bytesUsed+lb > DefaultMaxBytes {
			truncatedByBytes = true
			break
		}
		outLines = append(outLines, allLines[i])
		bytesUsed += lb
	}

	if !truncatedByBytes && !truncatedByLines {
		return sdk.ToolResult{
			Content: fetched,
			Details: makeURLDetails(target.URL, finalURL, contentType, method, target.Sel.IsRaw(), false, nil),
		}, nil
	}

	shownEnd := len(outLines)
	out := strings.Join(outLines, "\n")
	truncBy := "lines"
	if truncatedByBytes {
		truncBy = "bytes"
		out += fmt.Sprintf("\n\n[Showing lines 1-%d of %d (%s limit). Use :%d- to continue]",
			shownEnd, totalLines, formatBytes(DefaultMaxBytes), shownEnd+1)
	} else {
		out += fmt.Sprintf("\n\n[Showing lines 1-%d of %d. Use :%d- to continue]",
			shownEnd, totalLines, shownEnd+1)
	}
	truncMeta := map[string]any{
		"direction":   "head",
		"truncatedBy": truncBy,
		"totalLines":  totalLines,
		"totalBytes":  len(fetched),
		"outputLines": shownEnd,
		"outputBytes": bytesUsed,
		"shownRange":  map[string]any{"start": 1, "end": shownEnd},
		"nextOffset":  shownEnd + 1,
	}
	if truncatedByBytes {
		truncMeta["maxBytes"] = DefaultMaxBytes
	}
	return sdk.ToolResult{
		Content: out,
		Details: makeURLDetails(target.URL, finalURL, contentType, method, target.Sel.IsRaw(), true, truncMeta),
	}, nil
}

func renderURLMultiRange(allLines []string, sel ParsedSelector) string {
	totalLines := len(allLines)
	var blocks []string
	var notices []string
	for _, r := range sel.Ranges {
		startIdx := r.StartLine - 1
		if startIdx >= totalLines {
			bound := fmt.Sprintf("%d", r.StartLine)
			if r.EndLine > 0 {
				bound = fmt.Sprintf("%d-%d", r.StartLine, r.EndLine)
			}
			notices = append(notices, fmt.Sprintf("[Range %s is beyond end of URL output (%d lines total); skipped]", bound, totalLines))
			continue
		}
		endIdx := totalLines
		if r.EndLine > 0 {
			endIdx = min(r.EndLine, totalLines)
		}
		blocks = append(blocks, strings.Join(allLines[startIdx:endIdx], "\n"))
	}
	out := strings.Join(blocks, "\n…\n")
	if len(notices) > 0 {
		if out != "" {
			out += "\n\n"
		}
		out += strings.Join(notices, "\n")
	}
	return out
}
