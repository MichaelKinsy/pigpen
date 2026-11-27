package filetools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

type magicSignature struct {
	prefix []byte
	name   string
	isISO  bool
}

var magicSignatures = []magicSignature{
	{[]byte("\x89PNG\r\n\x1a\n"), "PNG image data", false},
	{[]byte("\xff\xd8\xff"), "JPEG image data", false},
	{[]byte("GIF87a"), "GIF image data", false},
	{[]byte("GIF89a"), "GIF image data", false},
	{[]byte("RIFF"), "RIFF container (WAV/AVI/WebP family)", false},
	{[]byte("%PDF-"), "PDF document", false},
	{[]byte("PK\x03\x04"), "ZIP archive (also docx/xlsx/jar/apk)", false},
	{[]byte("PK\x05\x06"), "ZIP archive (empty)", false},
	{[]byte("\x1f\x8b"), "gzip compressed data", false},
	{[]byte("BZh"), "bzip2 compressed data", false},
	{[]byte("\xfd7zXZ\x00"), "xz compressed data", false},
	{[]byte("7z\xbc\xaf\x27\x1c"), "7-Zip archive", false},
	{[]byte("\x7fELF"), "ELF executable", false},
	{[]byte("MZ"), "Windows PE executable", false},
	{[]byte("\xcf\xfa\xed\xfe"), "Mach-O executable (64-bit)", false},
	{[]byte("\xca\xfe\xba\xbe"), "Mach-O universal binary / Java class", false},
	{[]byte("SQLite format 3\x00"), "SQLite database", false},
	{[]byte("OggS"), "Ogg container", false},
	{[]byte("fLaC"), "FLAC audio", false},
	{[]byte("ID3"), "MP3 audio (ID3 tag)", false},
	{[]byte("\x00\x00\x00"), "ISO media container (MP4/MOV family)", true},
	{[]byte("BM"), "BMP image data", false},
	{[]byte("II*\x00"), "TIFF image data (little-endian)", false},
	{[]byte("MM\x00*"), "TIFF image data (big-endian)", false},
}

func (s *state) readFileToolDef() sdk.ToolDefinition {
	return sdk.ToolDefinition{
		Name:        "read_file",
		Label:       "Read File",
		Description: "Read a file or list a directory with 1-indexed line numbers (<n>|). Supports pagination via offset/limit, negative offsets for tail reads, binary detection, and .ipynb notebook extraction.",
		Parameters: sdk.Schema{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the file or directory to read.",
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "1-indexed starting line number (default 1). Negative values read the last N lines.",
					"default":     1,
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of lines to return (default 500, max 2000).",
					"default":     defaultReadLimit,
				},
			},
			"required": []string{"path"},
		},
		Execute: s.executeReadFile,
	}
}

func (s *state) executeReadFile(ctx sdk.Context, params map[string]any) (any, error) {
	rawPath, ok := paramString(params, "path")
	if !ok || strings.TrimSpace(rawPath) == "" {
		return errResult("read_file: missing required field 'path'"), nil
	}
	absPath, err := resolvePath(safeCwd(ctx), rawPath)
	if err != nil {
		return errResult(fmt.Sprintf("read_file: %v", err)), nil
	}

	offset := paramInt(params, "offset", 1)
	limit := paramInt(params, "limit", defaultReadLimit)
	if limit <= 0 {
		limit = defaultReadLimit
	}
	if limit > maxReadLimit {
		limit = maxReadLimit
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return errResult(fmt.Sprintf("read_file: %v", err)), nil
	}
	if info.IsDir() {
		return s.readDirectory(absPath, offset, limit)
	}

	raw, err := os.ReadFile(absPath)
	if err != nil {
		return errResult(fmt.Sprintf("read_file: %v", err)), nil
	}

	// Handle Jupyter notebooks (.ipynb) via JSON cell extraction.
	if strings.EqualFold(filepath.Ext(absPath), ".ipynb") {
		if extracted, ok := extractNotebook(raw); ok {
			res := s.formatPagedText(extracted, offset, limit)
			s.recordRead(absPath, info, res.fullRead)
			return okResult(res.output, map[string]any{
				"path":        absPath,
				"total_lines": res.totalLines,
				"offset":      res.startLine,
				"limit":       limit,
				"truncated":   res.truncated,
				"next_offset": res.nextOffset,
				"format":      "ipynb",
			}), nil
		}
	}

	// Binary detection via NUL byte and UTF-8 validity.
	sample := raw
	if len(sample) > 8192 {
		sample = sample[:8192]
	}
	if isBinaryBytes(sample) {
		return errResult(describeBinaryFile(sample, info.Size())), nil
	}

	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	res := s.formatPagedText(text, offset, limit)
	s.recordRead(absPath, info, res.fullRead)

	return okResult(res.output, map[string]any{
		"path":        absPath,
		"total_lines": res.totalLines,
		"offset":      res.startLine,
		"limit":       limit,
		"truncated":   res.truncated,
		"next_offset": res.nextOffset,
	}), nil
}

func (s *state) readDirectory(absPath string, offset, limit int) (sdk.ToolResult, error) {
	entries, err := os.ReadDir(absPath)
	if err != nil {
		return errResult(fmt.Sprintf("read_file: %v", err)), nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return okResult("(empty directory)", map[string]any{"path": absPath, "entries": 0}), nil
	}
	startIdx := 0
	if offset > 1 {
		startIdx = offset - 1
	} else if offset < 0 {
		startIdx = len(names) + offset
		if startIdx < 0 {
			startIdx = 0
		}
	}
	if startIdx >= len(names) {
		return okResult(fmt.Sprintf("(offset %d beyond %d entries)", offset, len(names)), map[string]any{
			"path":    absPath,
			"entries": len(names),
		}), nil
	}
	endIdx := startIdx + limit
	if endIdx > len(names) {
		endIdx = len(names)
	}
	var sb strings.Builder
	for i := startIdx; i < endIdx; i++ {
		if i > startIdx {
			sb.WriteByte('\n')
		}
		sb.WriteString(names[i])
	}
	if endIdx < len(names) {
		fmt.Fprintf(&sb, "\n... [truncated: showing %d-%d of %d entries; use offset=%d to continue]", startIdx+1, endIdx, len(names), endIdx+1)
	}
	return okResult(sb.String(), map[string]any{
		"path":    absPath,
		"entries": len(names),
	}), nil
}

type pagedTextResult struct {
	output     string
	totalLines int
	startLine  int
	truncated  bool
	nextOffset int
	fullRead   bool
}

func (s *state) formatPagedText(content string, offset, limit int) pagedTextResult {
	if content == "" {
		return pagedTextResult{
			output:     "",
			totalLines: 0,
			startLine:  1,
			fullRead:   true,
		}
	}
	// Strip a single trailing newline so cat -n semantics apply without a phantom line.
	trimmed := strings.TrimSuffix(content, "\n")
	lines := strings.Split(trimmed, "\n")
	totalLines := len(lines)

	var startIdx int
	if offset < 0 {
		startIdx = totalLines + offset
		if startIdx < 0 {
			startIdx = 0
		}
	} else if offset <= 1 {
		startIdx = 0
	} else {
		startIdx = offset - 1
	}

	if startIdx >= totalLines {
		return pagedTextResult{
			output:     fmt.Sprintf("(offset %d is beyond end of file; total lines: %d)", offset, totalLines),
			totalLines: totalLines,
			startLine:  startIdx + 1,
			fullRead:   false,
		}
	}

	endIdx := startIdx + limit
	if endIdx > totalLines {
		endIdx = totalLines
	}

	maxLineLen := s.maxLineLength()
	maxChars := s.maxReadChars()

	var sb strings.Builder
	writtenLines := 0
	charTruncated := false
	lineTruncated := endIdx < totalLines

	for i := startIdx; i < endIdx; i++ {
		line := lines[i]
		if len(line) > maxLineLen {
			line = line[:maxLineLen] + "... [truncated]"
		}
		formatted := fmt.Sprintf("%d|%s", i+1, line)
		addLen := len(formatted)
		if writtenLines > 0 {
			addLen++ // newline
		}
		if writtenLines > 0 && sb.Len()+addLen > maxChars {
			charTruncated = true
			break
		}
		if writtenLines == 0 && len(formatted) > maxChars {
			sb.WriteString(formatted[:maxChars])
			writtenLines = 1
			charTruncated = true
			break
		}
		if writtenLines > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(formatted)
		writtenLines++
	}

	lastShown := startIdx + writtenLines
	truncated := charTruncated || lineTruncated || lastShown < totalLines
	nextOffset := 0
	if lastShown < totalLines {
		nextOffset = lastShown + 1
	}

	if truncated {
		reason := "line limit"
		if charTruncated {
			reason = fmt.Sprintf("%d-char budget", maxChars)
		}
		fmt.Fprintf(&sb, "\n... [truncated due to %s: showing lines %d-%d of %d; use offset=%d to read more]",
			reason, startIdx+1, lastShown, totalLines, nextOffset)
	}

	return pagedTextResult{
		output:     sb.String(),
		totalLines: totalLines,
		startLine:  startIdx + 1,
		truncated:  truncated,
		nextOffset: nextOffset,
		fullRead:   startIdx == 0 && lastShown == totalLines && !charTruncated,
	}
}

func isBinaryBytes(sample []byte) bool {
	if len(sample) == 0 {
		return false
	}
	if bytes.IndexByte(sample, 0) >= 0 {
		return true
	}
	if utf8.Valid(sample) {
		return false
	}
	// Allow up to 3 trailing bytes of an incomplete multi-byte UTF-8 rune at sample boundary.
	for trim := 1; trim <= 3 && trim < len(sample); trim++ {
		if utf8.Valid(sample[:len(sample)-trim]) {
			return false
		}
	}
	return true
}

func identifyBinaryBytes(sample []byte) string {
	for _, sig := range magicSignatures {
		if bytes.HasPrefix(sample, sig.prefix) {
			if sig.isISO {
				if len(sample) < 8 || !bytes.Equal(sample[4:8], []byte("ftyp")) {
					continue
				}
			}
			return sig.name
		}
	}
	return "unknown binary"
}

func describeBinaryFile(sample []byte, fileSize int64) string {
	kind := identifyBinaryBytes(sample)
	var sizeStr string
	switch {
	case fileSize >= 1024*1024:
		sizeStr = fmt.Sprintf("%.1f MB", float64(fileSize)/(1024*1024))
	case fileSize >= 1024:
		sizeStr = fmt.Sprintf("%.1f KB", float64(fileSize)/1024)
	default:
		sizeStr = fmt.Sprintf("%d bytes", fileSize)
	}
	return fmt.Sprintf("Binary file (%s, %s) — cannot display as text.", kind, sizeStr)
}

func isBinaryFileQuick(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return false
	}
	return bytes.IndexByte(buf[:n], 0) >= 0
}

type ipynbDocument struct {
	Cells []ipynbCell `json:"cells"`
}

type ipynbCell struct {
	CellType       string          `json:"cell_type"`
	ExecutionCount any             `json:"execution_count"`
	Source         json.RawMessage `json:"source"`
	Outputs        []ipynbOutput   `json:"outputs"`
}

type ipynbOutput struct {
	OutputType string                     `json:"output_type"`
	Name       string                     `json:"name"`
	Text       json.RawMessage            `json:"text"`
	Data       map[string]json.RawMessage `json:"data"`
	EName      string                     `json:"ename"`
	EValue     string                     `json:"evalue"`
}

func decodeIpynbStringOrSlice(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var lines []string
	if err := json.Unmarshal(raw, &lines); err == nil {
		return strings.Join(lines, "")
	}
	return ""
}

func extractNotebook(raw []byte) (string, bool) {
	var doc ipynbDocument
	if err := json.Unmarshal(raw, &doc); err != nil || doc.Cells == nil {
		return "", false
	}
	var sb strings.Builder
	for idx, cell := range doc.Cells {
		if idx > 0 {
			sb.WriteString("\n\n")
		}
		header := fmt.Sprintf("# Cell %d (%s)", idx+1, cell.CellType)
		if cell.ExecutionCount != nil {
			header = fmt.Sprintf("# Cell %d (%s, In[%v])", idx+1, cell.CellType, cell.ExecutionCount)
		}
		sb.WriteString(header)
		sb.WriteByte('\n')
		src := strings.TrimRight(decodeIpynbStringOrSlice(cell.Source), "\n")
		sb.WriteString(src)
		for _, out := range cell.Outputs {
			switch out.OutputType {
			case "stream":
				txt := strings.TrimRight(decodeIpynbStringOrSlice(out.Text), "\n")
				if txt != "" {
					sb.WriteString("\n# Output (")
					sb.WriteString(out.Name)
					sb.WriteString("):\n")
					sb.WriteString(txt)
				}
			case "execute_result", "display_data":
				if plain, ok := out.Data["text/plain"]; ok {
					txt := strings.TrimRight(decodeIpynbStringOrSlice(plain), "\n")
					if txt != "" {
						sb.WriteString("\n# Output:\n")
						sb.WriteString(txt)
					}
				}
			case "error":
				sb.WriteString("\n# Error: ")
				sb.WriteString(out.EName)
				if out.EValue != "" {
					sb.WriteString(": ")
					sb.WriteString(out.EValue)
				}
			}
		}
	}
	return sb.String(), true
}
