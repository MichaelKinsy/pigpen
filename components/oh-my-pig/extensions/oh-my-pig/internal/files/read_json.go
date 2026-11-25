package files

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/dirs"
)

// upstream: packages/coding-agent/src/tools/read-json.ts

const (
	defaultJSONQueryLimit      = 100
	maxJSONQueryLimit          = 1000
	maxQueryCaptureChars       = 5 * 1024 * 1024
	jsonQueryTimeout           = 30 * time.Second
	jsonQueryErrorLineMaxBytes = 512
	jsonQueryErrorMaxBytes     = 8 * 1024
	jsonPrettyIndent           = "  "
)

var (
	jsonFilePattern = regexp.MustCompile(`(?i)\.(?:jsonl?|ndjson)(?:[.~][^/\\]*)?$`)
	jsonQueryParam  = regexp.MustCompile(`(?:^|&)(?:q|query)=([^&]*)`)
)

type jsonPage struct {
	offset int
	limit  int
}

type jsonSelector struct {
	query   string
	raw     bool
	compact bool
	page    *jsonPage
}

type jqStop string

const (
	jqStopNone       jqStop = ""
	jqStopCaptureCap jqStop = "capture-cap"
	jqStopPageFull   jqStop = "page-full"
)

// opensLikeJSON reports whether header opens a JSON object or array after an
// optional UTF-8 BOM and ASCII whitespace.
func opensLikeJSON(header []byte) bool {
	i := 0
	if len(header) >= 3 && header[0] == 0xef && header[1] == 0xbb && header[2] == 0xbf {
		i = 3
	}
	for i < len(header) && (header[i] == 0x20 || header[i] == 0x09 || header[i] == 0x0a || header[i] == 0x0d) {
		i++
	}
	return i < len(header) && (header[i] == '{' || header[i] == '[')
}

// splitJSONQueryTarget splits readPath at its first '?'.
func splitJSONQueryTarget(readPath string) (jsonPath, queryString string, ok bool) {
	idx := strings.IndexByte(readPath, '?')
	if idx == -1 {
		return "", "", false
	}
	return readPath[:idx], readPath[idx+1:], true
}

// parseJSONSelector parses q/query, raw, compact, offset, and limit from a query string.
// q is percent-decoded but never form-decoded, so jq's '+' survives.
func parseJSONSelector(queryString string) *jsonSelector {
	match := jsonQueryParam.FindStringSubmatch(queryString)

	params := make(map[string]string)
	hasParam := func(name string) bool {
		_, ok := params[name]
		return ok
	}
	for part := range strings.SplitSeq(queryString, "&") {
		if part == "" {
			continue
		}
		k, v, _ := strings.Cut(part, "=")
		if dk, err := url.QueryUnescape(k); err == nil {
			k = dk
		}
		if dv, err := url.QueryUnescape(v); err == nil {
			v = dv
		}
		if _, exists := params[k]; !exists {
			params[k] = v
		}
	}

	if match == nil && !hasParam("raw") && !hasParam("compact") && !hasParam("offset") && !hasParam("limit") {
		return nil
	}

	encodedQuery := "."
	if match != nil && match[1] != "" {
		encodedQuery = match[1]
	}
	query, err := url.PathUnescape(encodedQuery)
	if err != nil {
		// A filter with a bare '%' (jq modulo) is not valid percent-encoding; take it verbatim.
		query = encodedQuery
	}

	flag := func(name string) bool {
		v := strings.ToLower(params[name])
		return v == "true" || v == "1"
	}

	offset, hasOffset := parseJSONCount(params, "offset", 0)
	limit, hasLimit := parseJSONCount(params, "limit", 1)

	var page *jsonPage
	if hasOffset || hasLimit {
		off := 0
		if hasOffset {
			off = offset
		}
		lim := defaultJSONQueryLimit
		if hasLimit {
			lim = limit
		}
		if lim > maxJSONQueryLimit {
			lim = maxJSONQueryLimit
		}
		page = &jsonPage{offset: off, limit: lim}
	}

	return &jsonSelector{
		query:   query,
		raw:     flag("raw"),
		compact: flag("compact"),
		page:    page,
	}
}

func parseJSONCount(params map[string]string, name string, minVal int) (int, bool) {
	raw, ok := params[name]
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < minVal {
		return 0, false
	}
	return n, true
}

func jsonContinuationHint(sel *jsonSelector, page jsonPage, nextOffset int, remaining *int) string {
	var flags strings.Builder
	if sel.raw {
		flags.WriteString("&raw=true")
	}
	if sel.compact {
		flags.WriteString("&compact=true")
	}
	count := "more results"
	if remaining != nil {
		count = fmt.Sprintf("%d more items", *remaining)
	}
	query := strings.ReplaceAll(strings.ReplaceAll(sel.query, "%", "%25"), "&", "%26")
	return fmt.Sprintf("[%s; append ?q=%s%s&limit=%d&offset=%d to continue]", count, query, flags.String(), page.limit, nextOffset)
}

func jsonStringEnd(text string, open int) int {
	for i := open + 1; i < len(text); i++ {
		if text[i] == '\\' {
			i++
		} else if text[i] == '"' {
			return i
		}
	}
	return len(text) - 1
}

func prettyJSON(compact string) string {
	var out strings.Builder
	out.Grow(len(compact) * 2)
	depth := 0
	for i := 0; i < len(compact); i++ {
		ch := compact[i]
		switch ch {
		case '"':
			end := jsonStringEnd(compact, i)
			out.WriteString(compact[i : end+1])
			i = end
		case '{', '[':
			closer := byte('}')
			if ch == '[' {
				closer = ']'
			}
			if i+1 < len(compact) && compact[i+1] == closer {
				out.WriteByte(ch)
				out.WriteByte(closer)
				i++
			} else {
				depth++
				out.WriteByte(ch)
				out.WriteByte('\n')
				out.WriteString(strings.Repeat(jsonPrettyIndent, depth))
			}
		case '}', ']':
			if depth > 0 {
				depth--
			}
			out.WriteByte('\n')
			out.WriteString(strings.Repeat(jsonPrettyIndent, depth))
			out.WriteByte(ch)
		case ',':
			out.WriteString(",\n")
			out.WriteString(strings.Repeat(jsonPrettyIndent, depth))
		case ':':
			out.WriteString(": ")
		default:
			out.WriteByte(ch)
		}
	}
	return out.String()
}

func splitCompactJSONArray(compact string) []string {
	var items []string
	depth := 0
	start := 1
	for i := 0; i < len(compact); i++ {
		ch := compact[i]
		if ch == '"' {
			i = jsonStringEnd(compact, i)
		} else if ch == '[' || ch == '{' {
			depth++
		} else if ch == ']' || ch == '}' {
			depth--
			if depth == 0 && i > start {
				items = append(items, compact[start:i])
			}
		} else if ch == ',' && depth == 1 {
			items = append(items, compact[start:i])
			start = i + 1
		}
	}
	return items
}

func renderJSONValue(value string, sel *jsonSelector) string {
	if sel.raw && strings.HasPrefix(value, `"`) {
		var s string
		if err := json.Unmarshal([]byte(value), &s); err == nil {
			return s
		}
	}
	if sel.compact {
		return value
	}
	return prettyJSON(value)
}

func pageJSONArray(array string, sel *jsonSelector, page jsonPage) string {
	items := splitCompactJSONArray(array)
	start := min(page.offset, len(items))
	end := min(page.offset+page.limit, len(items))
	slice := items[start:end]
	compact := "[" + strings.Join(slice, ",") + "]"
	text := compact
	if !sel.compact {
		text = prettyJSON(compact)
	}
	remaining := len(items) - page.offset - len(slice)
	if remaining > 0 {
		return text + "\n" + jsonContinuationHint(sel, page, page.offset+len(slice), &remaining)
	}
	return text
}

func pageJSONStream(values []string, sel *jsonSelector, page jsonPage, more bool) string {
	start := min(page.offset, len(values))
	end := min(page.offset+page.limit, len(values))
	slice := values[start:end]
	lines := make([]string, 0, len(slice)+1)
	for _, v := range slice {
		lines = append(lines, renderJSONValue(v, sel))
	}
	if more {
		lines = append(lines, jsonContinuationHint(sel, page, page.offset+len(slice), nil))
	} else if len(slice) == 0 && page.offset > 0 {
		lines = append(lines, fmt.Sprintf("[offset %d is past the last result (%d total)]", page.offset, len(values)))
	}
	return strings.Join(lines, "\n")
}

func truncateMiddleBytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	const ellipsis = "..."
	if maxBytes <= len(ellipsis) {
		return ellipsis[:maxBytes]
	}
	budget := maxBytes - len(ellipsis)
	headBudget := budget / 2
	tailBudget := budget - headBudget

	head := s[:headBudget]
	for len(head) > 0 && !utf8.ValidString(head) {
		head = head[:len(head)-1]
	}
	tail := s[len(s)-tailBudget:]
	for len(tail) > 0 && !utf8.ValidString(tail) {
		tail = tail[1:]
	}
	return head + ellipsis + tail
}

func truncateTailBytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	tail := s[len(s)-maxBytes:]
	for len(tail) > 0 && !utf8.ValidString(tail) {
		tail = tail[1:]
	}
	return tail
}

func boundJqStderr(stderr string) string {
	trimmed := strings.TrimSpace(stderr)
	if trimmed == "" {
		return ""
	}
	lines := strings.Split(trimmed, "\n")
	for i, line := range lines {
		lines[i] = truncateMiddleBytes(line, jsonQueryErrorLineMaxBytes)
	}
	return truncateTailBytes(strings.Join(lines, "\n"), jsonQueryErrorMaxBytes)
}

func withJqStderr(text, stderr string) string {
	if stderr == "" {
		return text
	}
	notice := "[jq stderr: " + stderr + "]"
	if text != "" {
		return notice + "\n" + text
	}
	return notice
}

func runJq(ctx context.Context, filePath, query string, flags []string, stopAfterLines int) (string, jqStop, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", jqStopNone, "", err
	}

	jqPath, err := exec.LookPath("jq")
	if err != nil {
		return "", jqStopNone, "", fmt.Errorf("Failed to execute JSON query: jq is not installed or not found on PATH")
	}

	runCtx, cancel := context.WithTimeout(ctx, jsonQueryTimeout)
	defer cancel()

	args := make([]string, 0, len(flags)+3)
	args = append(args, flags...)
	args = append(args, "--", query, filePath)

	cmd := exec.CommandContext(runCtx, jqPath, args...)
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", jqStopNone, "", fmt.Errorf("Failed to execute JSON query: %w", err)
	}
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return "", jqStopNone, "", fmt.Errorf("Failed to execute JSON query: %w", err)
	}

	var outBuf bytes.Buffer
	lines := 0
	stopped := jqStopNone
	buf := make([]byte, 32*1024)
	for {
		n, rerr := stdoutPipe.Read(buf)
		if n > 0 && stopped == jqStopNone {
			chunk := buf[:n]
			room := maxQueryCaptureChars - outBuf.Len()
			if len(chunk) > room {
				chunk = chunk[:room]
			}
			outBuf.Write(chunk)
			if stopAfterLines > 0 {
				lines += bytes.Count(chunk, []byte{'\n'})
				if lines >= stopAfterLines {
					stopped = jqStopPageFull
					cancel()
					break
				}
			}
			if outBuf.Len() >= maxQueryCaptureChars {
				stopped = jqStopCaptureCap
				cancel()
				break
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			break
		}
	}
	// Drain any remaining pipe data if we didn't cancel, or close pipe before Wait.
	_ = stdoutPipe.Close()
	waitErr := cmd.Wait()

	if err := ctx.Err(); err != nil {
		return "", jqStopNone, "", err
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) && stopped == jqStopNone {
		return "", jqStopNone, "", fmt.Errorf("JSON query timed out after %d seconds", int(jsonQueryTimeout/time.Second))
	}

	stderr := boundJqStderr(stderrBuf.String())
	if stopped == jqStopNone && waitErr != nil {
		msg := stderr
		if msg == "" {
			var exitErr *exec.ExitError
			if errors.As(waitErr, &exitErr) {
				msg = fmt.Sprintf("jq exited with code %d", exitErr.ExitCode())
			} else {
				msg = waitErr.Error()
			}
		}
		return "", jqStopNone, "", fmt.Errorf("Failed to execute JSON query: %s", msg)
	}

	return outBuf.String(), stopped, stderr, nil
}

func executeJSONQuery(ctx context.Context, filePath string, sel *jsonSelector) (string, error) {
	if sel.page == nil {
		var flags []string
		if sel.raw {
			flags = append(flags, "-r")
		}
		if sel.compact {
			flags = append(flags, "-c")
		}
		output, _, stderr, err := runJq(ctx, filePath, sel.query, flags, 0)
		if err != nil {
			return "", err
		}
		output = strings.TrimSuffix(output, "\n")
		return withJqStderr(output, stderr), nil
	}

	output, stopped, stderr, err := runJq(ctx, filePath, sel.query, []string{"-c"}, sel.page.offset+sel.page.limit+1)
	if err != nil {
		return "", err
	}
	values := strings.Split(output, "\n")
	tail := ""
	if len(values) > 0 {
		tail = values[len(values)-1]
		values = values[:len(values)-1]
	}
	if tail != "" && stopped == jqStopNone {
		values = append(values, tail)
	}
	if stopped == jqStopCaptureCap && len(values) <= sel.page.offset {
		return "", fmt.Errorf("JSON query output before the requested page exceeds %d MB; narrow the filter or stream large arrays with .[]", maxQueryCaptureChars/(1024*1024))
	}

	var text string
	if stopped == jqStopNone && len(values) == 1 && strings.HasPrefix(values[0], "[") {
		text = pageJSONArray(values[0], sel, *sel.page)
	} else {
		more := stopped != jqStopNone || len(values) > sel.page.offset+sel.page.limit
		text = pageJSONStream(values, sel, *sel.page, more)
	}
	return withJqStderr(text, stderr), nil
}

// resolveJSONReadPath resolves `file?q=...` to an existing non-image regular file.
func resolveJSONReadPath(basePath, cwd string) (string, *jsonSelector, bool) {
	jsonPath, queryString, ok := splitJSONQueryTarget(basePath)
	if !ok || jsonPath == "" {
		return "", nil, false
	}
	sel := parseJSONSelector(queryString)
	if sel == nil {
		return "", nil, false
	}

	absPath := resolveToCwd(jsonPath, cwd)
	info, err := os.Stat(absPath)
	if err != nil || !info.Mode().IsRegular() {
		return "", nil, false
	}

	f, err := os.Open(absPath)
	if err != nil {
		return "", nil, false
	}
	header := make([]byte, 8192)
	n, _ := f.Read(header)
	_ = f.Close()
	header = header[:n]

	if detectImageMIME(header) != "" {
		return "", nil, false
	}
	if !jsonFilePattern.MatchString(absPath) && !opensLikeJSON(header) {
		return "", nil, false
	}
	return absPath, sel, true
}

func readJSONQuery(ctx context.Context, absPath string, sel *jsonSelector, lineSel ParsedSelector) (sdk.ToolResult, error) {
	queryOutput, err := executeJSONQuery(ctx, absPath, sel)
	if err != nil {
		return sdk.ToolResult{}, err
	}

	stdPath := dirs.StandardizeMacOSPath(absPath)
	if lineSel.Kind != SelectorNone {
		res, err := renderInMemoryText(queryOutput, "", "JSON query output", lineSel, nil, "", 0, false, true)
		if err != nil {
			return sdk.ToolResult{}, err
		}
		d, _ := res.Details.(map[string]any)
		if d == nil {
			d = map[string]any{}
		}
		d["resolvedPath"] = stdPath
		setMetaSource(d, "path", absPath)
		res.Details = d
		return res, nil
	}

	details := map[string]any{
		"resolvedPath": stdPath,
	}
	setMetaSource(details, "path", absPath)

	// Head truncation matching read-json.ts:493-502 (DEFAULT_MAX_LINES=2000, DEFAULT_MAX_BYTES=50KB).
	allLines := strings.Split(queryOutput, "\n")
	totalLines := len(allLines)
	totalBytes := len(queryOutput)
	var kept []string
	keptBytes := 0
	truncBy := ""
	for i, line := range allLines {
		if i >= DefaultMaxLines {
			truncBy = "lines"
			break
		}
		sep := 0
		if len(kept) > 0 {
			sep = 1
		}
		if len(kept) > 0 && keptBytes+sep+len(line) > DefaultMaxBytes {
			truncBy = "bytes"
			break
		}
		if len(kept) == 0 && len(line) > DefaultMaxBytes {
			truncBy = "bytes"
			break
		}
		kept = append(kept, line)
		keptBytes += sep + len(line)
	}

	if truncBy == "" {
		return sdk.ToolResult{Content: queryOutput, Details: details}, nil
	}

	out := strings.Join(kept, "\n")
	firstLineExceeds := len(kept) == 0 && truncBy == "bytes"
	details["truncation"] = map[string]any{
		"truncated":             true,
		"truncatedBy":           truncBy,
		"totalLines":            totalLines,
		"totalBytes":            totalBytes,
		"outputLines":           len(kept),
		"outputBytes":           keptBytes,
		"lastLinePartial":       false,
		"firstLineExceedsLimit": firstLineExceeds,
	}
	setMetaTruncation(details, map[string]any{
		"direction":   "head",
		"truncatedBy": truncBy,
		"totalLines":  totalLines,
		"totalBytes":  totalBytes,
		"outputLines": len(kept),
		"outputBytes": keptBytes,
		"maxBytes":    DefaultMaxBytes,
	})
	return sdk.ToolResult{Content: out, Details: details}, nil
}
