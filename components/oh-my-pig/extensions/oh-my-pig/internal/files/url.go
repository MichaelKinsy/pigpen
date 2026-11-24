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

// stripURLFetchHeader removes the `URL:/Content-Type:/Method:\n\n---\n\n` header block
// that web.Fetch prepends, returning the fetched body text for ranged URL reads.
func stripURLFetchHeader(fetched string) string {
	if !strings.HasPrefix(fetched, "URL: ") {
		return fetched
	}
	if idx := strings.Index(fetched, "\n\n---\n\n"); idx >= 0 {
		return fetched[idx+len("\n\n---\n\n"):]
	}
	return fetched
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

	// Line-range or tail selector: slice the body (no URL/Content-Type/Method header),
	// with 1-before / 3-after context on single non-raw ranges, no line numbers,
	// and omp's `[N more lines in URL output. Use :M to continue]` footer when lines remain.
	if target.Sel.Kind == SelectorLines || target.Sel.Kind == SelectorTail {
		body := stripURLFetchHeader(fetched)
		norm := strings.ReplaceAll(body, "\r\n", "\n")
		allLines := strings.Split(norm, "\n")
		if strings.HasSuffix(norm, "\n") && len(allLines) > 0 {
			allLines = allLines[:len(allLines)-1]
		}
		totalLines := len(allLines)

		resolved := target.Sel.ResolveTail(totalLines)
		if resolved.IsMultiRange() {
			return sdk.ToolResult{Content: renderURLMultiRange(allLines, resolved)}, nil
		}

		rawMode := resolved.IsRaw()
		r := resolved.Ranges[0]
		requestedStart := max(0, r.StartLine-1)
		if requestedStart >= totalLines {
			return sdk.ToolResult{
				Content: fmt.Sprintf("Line %d is beyond end of URL output (%d lines total). Use :1 to read from the start, or :%d to read the last line.", r.StartLine, totalLines, totalLines),
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
		if truncated {
			out += fmt.Sprintf("\n\n[Showing lines %d-%d of %d (%s limit). Use :%d to continue]",
				startIdx+1, shownEnd, totalLines, formatBytes(DefaultMaxBytes), shownEnd+1)
		} else if !rawMode && shownEnd < totalLines {
			remaining := totalLines - shownEnd
			out += fmt.Sprintf("\n\n[%d more lines in URL output. Use :%d to continue]", remaining, shownEnd+1)
		}
		return sdk.ToolResult{Content: out}, nil
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
	for i := 0; i < totalLines; i++ {
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
		return sdk.ToolResult{Content: fetched}, nil
	}

	shownEnd := len(outLines)
	out := strings.Join(outLines, "\n")
	if truncatedByBytes {
		out += fmt.Sprintf("\n\n[Showing lines 1-%d of %d (%s limit). Use :%d to continue]",
			shownEnd, totalLines, formatBytes(DefaultMaxBytes), shownEnd+1)
	} else {
		out += fmt.Sprintf("\n\n[Showing lines 1-%d of %d. Use :%d to continue]",
			shownEnd, totalLines, shownEnd+1)
	}
	return sdk.ToolResult{Content: out}, nil
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
