package files

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
)

const (
	// DefaultMaxLines matches omp's DEFAULT_MAX_LINES.
	DefaultMaxLines = 2000
	// DefaultMaxBytes matches omp's DEFAULT_MAX_BYTES (50 KB).
	DefaultMaxBytes = 50 * 1024
	// MaxImageBytes matches omp's MAX_IMAGE_SIZE (20 MB).
	MaxImageBytes = 20 * 1024 * 1024
	// RangeLeadingContextLines matches omp's RANGE_LEADING_CONTEXT_LINES.
	// upstream: packages/coding-agent/src/tools/read-format.ts:258
	RangeLeadingContextLines = 1
	// RangeTrailingContextLines matches omp's RANGE_TRAILING_CONTEXT_LINES.
	// upstream: packages/coding-agent/src/tools/read-format.ts:259
	RangeTrailingContextLines = 3
)

// detectImageMIME sniffs raster image magic bytes (PNG, JPEG, GIF, WebP).
// upstream: packages/coding-agent/src/tools/read.ts:2056
func detectImageMIME(data []byte) string {
	switch {
	case len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}):
		return "image/png"
	case len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff:
		return "image/jpeg"
	case len(data) >= 6 && (bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a"))):
		return "image/gif"
	case len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return "image/webp"
	default:
		return ""
	}
}

// isProbablyBinary checks the first 8KB for NUL bytes or invalid UTF-8 sequences.
// upstream: packages/coding-agent/src/tools/read.ts:2178
func isProbablyBinary(data []byte) bool {
	sample := data
	if len(sample) > 8192 {
		sample = sample[:8192]
	}
	if bytes.IndexByte(sample, 0) >= 0 {
		return true
	}
	if !utf8.Valid(sample) {
		// Allow a truncated multi-byte rune at the exact end of the 8KB sample.
		for cut := 1; cut <= 3 && cut <= len(sample); cut++ {
			if utf8.Valid(sample[:len(sample)-cut]) {
				return false
			}
		}
		return true
	}
	return false
}

// executeRead implements omp's `read` tool for local files, directories, and raster images.
// upstream: packages/coding-agent/src/tools/read.ts
func executeRead(store *hashline.Store, cwd, rawPath string) (sdk.ToolResult, error) {
	if urlTarget, err := parseReadURLTarget(rawPath); err != nil {
		return sdk.ToolResult{}, err
	} else if urlTarget != nil {
		return executeReadURL(context.Background(), urlTarget)
	}

	basePath, selStr := SplitPathAndSelPreferringLiteral(rawPath, cwd)
	parsed, err := ParseSel(selStr)
	if err != nil {
		return sdk.ToolResult{}, err
	}

	absPath := resolveToCwd(basePath, cwd)
	displayPath := formatPathRelativeToCwd(absPath, cwd)

	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return sdk.ToolResult{}, fmt.Errorf("File not found: %s", displayPath)
		}
		return sdk.ToolResult{}, err
	}

	if info.IsDir() {
		text, err := readDirectory(absPath, parsed)
		if err != nil {
			return sdk.ToolResult{}, err
		}
		return sdk.ToolResult{Content: text}, nil
	}

	if !info.Mode().IsRegular() {
		return sdk.ToolResult{}, fmt.Errorf("Cannot read '%s': not a regular file or directory.", displayPath)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return sdk.ToolResult{}, err
	}

	// Raster image detection (unless :raw was explicitly requested).
	if !parsed.IsRaw() {
		if mime := detectImageMIME(data); mime != "" {
			if len(data) > MaxImageBytes {
				return sdk.ToolResult{}, fmt.Errorf("Image file '%s' (%s) exceeds %s limit.", displayPath, formatBytes(int64(len(data))), formatBytes(MaxImageBytes))
			}
			encoded := base64.StdEncoding.EncodeToString(data)
			return sdk.ToolResult{
				Content: fmt.Sprintf("Read image file [%s]", mime),
				Images:  []sdk.ImageContent{{Data: encoded, MimeType: mime}},
			}, nil
		}
	}

	// Binary guard (skipped in :raw mode).
	if !parsed.IsRaw() && isProbablyBinary(data) {
		return sdk.ToolResult{
			Content: fmt.Sprintf("[Cannot read binary file '%s' (%s); not valid UTF-8 text. Use ':raw' to read bytes verbatim.]", displayPath, formatBytes(int64(len(data)))),
		}, nil
	}

	rawText := string(data)
	_, stripped := hashline.StripBOM(rawText)
	normalized := hashline.NormalizeToLF(stripped)
	hasTrailingNL := strings.HasSuffix(normalized, "\n")

	var allLines []string
	if normalized != "" {
		allLines = hashline.SplitAddressableFileLines(normalized)
	}
	totalLines := len(allLines)
	tag := hashline.FileHash(normalized)
	header := hashline.FormatHeader(displayPath, tag)

	resolvedSel := parsed.ResolveTail(totalLines)
	if resolvedSel.IsMultiRange() {
		text, displayedLines := renderMultiRange(allLines, resolvedSel, displayPath, header, hasTrailingNL)
		if !resolvedSel.IsRaw() {
			store.RecordSnapshotWithSeenLines(absPath, normalized, displayedLines)
		}
		return sdk.ToolResult{Content: text}, nil
	}

	// Single range or whole file.
	var offset, limit int
	hasOffset := false
	hasLimit := false
	if resolvedSel.Kind == SelectorLines && len(resolvedSel.Ranges) == 1 {
		r := resolvedSel.Ranges[0]
		offset = r.StartLine
		hasOffset = true
		if r.EndLine > 0 {
			limit = r.EndLine - r.StartLine + 1
			hasLimit = true
		}
	}

	rawMode := resolvedSel.IsRaw()
	requestedStart := 0
	if hasOffset && offset > 1 {
		requestedStart = offset - 1
	}

	// Out-of-bounds check (graceful message, matching read.ts:2390).
	if hasOffset && requestedStart >= totalLines {
		suggestion := "The file is empty."
		if totalLines > 0 {
			suggestion = fmt.Sprintf("Use %s:1 to read from the start, or %s:%d to read the last line.", displayPath, displayPath, totalLines)
		}
		return sdk.ToolResult{
			Content: fmt.Sprintf("Line %d is beyond end of file (%d lines total). %s", requestedStart+1, totalLines, suggestion),
		}, nil
	}

	// Context expansion for single non-raw range reads (upstream: read.ts:2340).
	expandStart := !rawMode && hasOffset && offset > 1
	expandEnd := !rawMode && hasLimit
	leadingCtx := 0
	if expandStart {
		leadingCtx = min(requestedStart, RangeLeadingContextLines)
	}
	trailingCtx := 0
	if expandEnd {
		trailingCtx = RangeTrailingContextLines
	}

	startIdx := requestedStart - leadingCtx
	effectiveLimit := DefaultMaxLines
	if hasLimit {
		effectiveLimit = limit
	}
	maxLinesToCollect := min(effectiveLimit+leadingCtx+trailingCtx, DefaultMaxLines)
	maxBytesForRead := max(DefaultMaxBytes, maxLinesToCollect*512)

	// Collect lines within line and byte budgets.
	var collected []string
	collectedBytes := 0
	stoppedByByteLimit := false
	for i := startIdx; i < totalLines && len(collected) < maxLinesToCollect; i++ {
		line := allLines[i]
		lineBytes := len(line)
		if len(collected) > 0 {
			lineBytes++ // newline
		}
		if len(collected) > 0 && collectedBytes+lineBytes > maxBytesForRead {
			stoppedByByteLimit = true
			break
		}
		if len(collected) == 0 && lineBytes > maxBytesForRead {
			stoppedByByteLimit = true
			break
		}
		collected = append(collected, line)
		collectedBytes += lineBytes
	}

	startLineDisplay := startIdx + 1

	// Handle single line exceeding byte budget.
	if len(collected) == 0 && stoppedByByteLimit && startIdx < totalLines {
		firstBytes := len(allLines[startIdx])
		if !rawMode {
			return sdk.ToolResult{
				Content: fmt.Sprintf("[Line %d is %s, exceeds %s limit. Hashline output requires full lines; cannot emit an editable numbered preview for a truncated line.]",
					startLineDisplay, formatBytes(int64(firstBytes)), formatBytes(int64(maxBytesForRead))),
			}, nil
		}
		// Raw mode: return truncated byte prefix.
		snippet := truncateUTF8Bytes(allLines[startIdx], maxBytesForRead)
		return sdk.ToolResult{Content: snippet}, nil
	}

	// Empty file.
	if totalLines == 0 {
		if rawMode {
			return sdk.ToolResult{Content: ""}, nil
		}
		return sdk.ToolResult{Content: header + "\n"}, nil
	}

	var out string
	if rawMode {
		out = strings.Join(collected, "\n")
		if !hasLimit && hasTrailingNL && startIdx+len(collected) == totalLines {
			out += "\n"
		}
	} else {
		body := hashline.FormatNumberedLines(strings.Join(collected, "\n"), startLineDisplay)
		out = header + "\n" + body
		if startIdx == 0 && len(collected) == totalLines {
			store.RecordSnapshot(absPath, normalized)
		} else {
			seen := make([]int, len(collected))
			for i := range collected {
				seen[i] = startLineDisplay + i
			}
			store.RecordSnapshotWithSeenLines(absPath, normalized, seen)
		}
	}

	endIdx := startIdx + len(collected)
	if !rawMode && endIdx < totalLines {
		nextOffset := endIdx + 1
		if stoppedByByteLimit {
			out += fmt.Sprintf("\n\n[Showing lines %d-%d of %d (%s limit). Use :%d to continue]",
				startLineDisplay, endIdx, totalLines, formatBytes(int64(maxBytesForRead)), nextOffset)
		} else {
			out += fmt.Sprintf("\n\n[Showing lines %d-%d of %d. Use :%d to continue]",
				startLineDisplay, endIdx, totalLines, nextOffset)
		}
	}

	return sdk.ToolResult{Content: out}, nil
}

func renderMultiRange(allLines []string, sel ParsedSelector, displayPath, header string, hasTrailingNL bool) (string, []int) {
	rawMode := sel.IsRaw()
	totalLines := len(allLines)
	var blocks []string
	var notices []string
	visible := make(map[int]bool)

	for _, r := range sel.Ranges {
		rangeStart := r.StartLine - 1
		if rangeStart >= totalLines {
			bound := fmt.Sprintf("%d", r.StartLine)
			if r.EndLine > 0 {
				bound = fmt.Sprintf("%d-%d", r.StartLine, r.EndLine)
			}
			notices = append(notices, fmt.Sprintf("[Range %s is beyond end of file (%d lines total); skipped]", bound, totalLines))
			continue
		}
		rangeEnd := totalLines
		if r.EndLine > 0 {
			rangeEnd = min(r.EndLine, totalLines)
		} else {
			rangeEnd = min(rangeStart+DefaultMaxLines, totalLines)
		}
		sliceLines := allLines[rangeStart:rangeEnd]
		if len(sliceLines) == 0 {
			continue
		}
		if rawMode {
			blockText := strings.Join(sliceLines, "\n")
			if r.EndLine == 0 && hasTrailingNL && rangeEnd == totalLines {
				blockText += "\n"
			}
			blocks = append(blocks, blockText)
		} else {
			for ln := rangeStart + 1; ln <= rangeEnd; ln++ {
				visible[ln] = true
			}
		}
	}

	var out string
	var displayed []int
	if rawMode {
		out = strings.Join(blocks, "\n")
	} else if len(visible) > 0 {
		body := hashline.RenderLinesWithBlockContext(allLines, visible)
		out = header + "\n" + body
		displayed = hashline.SeenLinesFromBody(body)
	} else {
		out = header
	}
	if len(notices) > 0 {
		if out != "" {
			out += "\n\n"
		}
		out += strings.Join(notices, "\n")
	}
	return out, displayed
}

func truncateUTF8Bytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	n := maxBytes
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
