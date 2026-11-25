package files

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

const executableNotice = "[Notice: Made executable via chmod +x]"

var (
	uriLikeWritePathRE = regexp.MustCompile(`(?i)^([a-z][a-z0-9+.-]*):/{1,2}(.*)$`)

	// Hashline prefix stripping & read metadata detection.
	// upstream: crates/edit/src/modes/hashline/prefixes.rs, packages/coding-agent/src/tools/write-content.ts
	hlPrefixRE         = regexp.MustCompile(`^\s*(?:(?:>>>|>>)\s*)?(?:[+*-]\s*)?(\d+)[:|]`)
	hlHeaderStrictRE   = regexp.MustCompile(`^\s*\[[^#\r\n]+#[0-9a-fA-F]{4}\]\s*$`)
	hlHeaderLooseRE    = regexp.MustCompile(`^\s*\[[^#\r\n]+#[^ \t\r\n]*\]\s*$`)
	numberedRowRE      = regexp.MustCompile(`^\s*(?:(?:>>>|>>)\s*)?(?:[+*-]\s*)?(\d+)[:|](.*)$`)
	readRangeElisionRE = regexp.MustCompile(`^\s*[1-9]\d*\s*-\s*[1-9]\d*:.*(?:…|\.\.\.).*$`)
	readSingleElideRE  = regexp.MustCompile(`^\s*(?:…|\.\.\.)\s*$`)

	// Column truncation notice detection.
	// upstream: packages/tui/src/tools/output-meta.ts:229-253
	columnTruncNoticeRE   = regexp.MustCompile(`^\[Some lines truncated to -?\d+(?:\.\d+)?(?:e[+-]\d+)? (?:chars|bytes)(?:\. .*)?\]$`)
	columnTruncInNoticeRE = regexp.MustCompile(`^\[.*\. Some lines truncated to -?\d+(?:\.\d+)?(?:e[+-]\d+)? (?:chars|bytes)(?:\. .*)?\]$`)

	// Auto-generated file guard.
	// upstream: crates/edit/src/path_policy.rs:558-593
	genFilenamePatterns = []*regexp.Regexp{
		regexp.MustCompile(`^zz_generated\.`),
		regexp.MustCompile(`\.pb\.(go|cc|h|c)$`),
		regexp.MustCompile(`_pb2\.py$`),
		regexp.MustCompile(`_pb2_grpc\.py$`),
		regexp.MustCompile(`\.gen\.(go|ts|js|py)$`),
		regexp.MustCompile(`\.swagger\.json$`),
	}
	genMarkerPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)@generated\b`),
		regexp.MustCompile(`(?i)\bcode\s+generated\s+by\s+[a-z0-9_.-]+`),
		regexp.MustCompile(`(?i)\bcode\s+generated\b.*\bdo\s+not\s+edit\b`),
		regexp.MustCompile(`(?i)\bthis\s+file\s+was\s+automatically\s+generated\b`),
		regexp.MustCompile(`(?i)\bgenerated\s+by\s+(?:protoc(?:-gen-[\w-]+)?|sqlc|buf|swagger(?:-codegen)?|openapi(?:-generator)?|grpc-gateway|mockery|stringer|easyjson|deepcopy-gen|defaulter-gen|conversion-gen|client-gen|lister-gen|informer-gen|kysely-codegen|napi-rs)\b`),
	}
)

// normalizeCRLFToLF normalizes only CRLF to LF, preserving lone CR bytes.
// upstream: crates/edit/src/text.rs:41-46, packages/coding-agent/src/edit/normalize.ts:12
func normalizeCRLFToLF(s string) string {
	if !strings.Contains(s, "\r\n") {
		return s
	}
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// assertWriteTargetAddressable rejects URI-shaped paths that are not a Windows absolute path
// or a registered internal URL.
// upstream: packages/coding-agent/src/tools/write.ts:91
func assertWriteTargetAddressable(target string) error {
	trimmed := strings.TrimSpace(target)
	if isWinAbsolutePath(trimmed) || urls.CanHandle(trimmed) {
		return nil
	}
	// Allow file:// URLs (handled by expandPath).
	if len(trimmed) >= 7 && strings.EqualFold(trimmed[:7], "file://") {
		return nil
	}
	m := uriLikeWritePathRE.FindStringSubmatch(trimmed)
	if m == nil {
		return nil
	}
	scheme := strings.ToLower(m[1])
	suggestion := ""
	if _, ok := urls.Lookup(scheme); ok {
		suggestion = fmt.Sprintf(" Did you mean '%s://%s'?", scheme, m[2])
	}
	return fmt.Errorf("Unknown URI-like write target '%s'.%s Prefix the path with './' to write it as a filesystem path.", trimmed, suggestion)
}

// assertNotReadSelectorMisfire refuses a missing selector-suffixed empty write or a
// semicolon-joined list of read selectors.
// upstream: packages/coding-agent/src/tools/write.ts:121-174
func assertNotReadSelectorMisfire(target, content, cwd string) error {
	if count := readSelectorListMisfire(target); count > 0 {
		if probeLiteralMissing(target, cwd) {
			return fmt.Errorf(
				"write target '%s' is a semicolon-joined list of %d read-tool selectors, not a filesystem path — refusing to create it. "+
					"write creates a single file; issue one read() per path to read these ranges (e.g. read({ path: \"<one path>:<range>\" })).",
				target, count,
			)
		}
	}
	if len(content) == 0 {
		if _, sel := SplitPathAndSel(target); sel != "" {
			if probeLiteralMissing(target, cwd) {
				return fmt.Errorf(
					"write target '%s' ends with a read-tool selector ':%s' and no such file exists — refusing to create a literal file by that name. "+
						"If you meant to read it, use read({ path: %q }). "+
						"If you truly intend to create this file, pass its contents in `content` (a non-empty write is never blocked).",
					target, sel, target,
				)
			}
		}
	}
	return nil
}

func readSelectorListMisfire(target string) int {
	if !strings.Contains(target, ";") {
		return 0
	}
	segments := strings.Split(target, ";")
	if len(segments) < 2 {
		return 0
	}
	for _, seg := range segments {
		trimmed := strings.TrimSpace(seg)
		if trimmed == "" {
			return 0
		}
		if _, sel := SplitPathAndSel(trimmed); sel == "" {
			return 0
		}
	}
	return len(segments)
}

func probeLiteralMissing(target, cwd string) bool {
	abs := resolveToCwd(target, cwd)
	_, err := os.Lstat(abs)
	return os.IsNotExist(err)
}

// isReadTruncationNotice reports whether line is a truncation notice emitted by `read`.
// upstream: crates/edit/src/modes/hashline/prefixes.rs:21
func isReadTruncationNotice(line string) bool {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < 2 || trimmed[0] != '[' || trimmed[len(trimmed)-1] != ']' {
		return false
	}
	body := trimmed[1 : len(trimmed)-1]
	showingNotice := strings.HasPrefix(body, "Showing ") &&
		(strings.Contains(body, " line") || strings.Contains(body, "lines ") || strings.Contains(body, "bytes ")) &&
		(strings.Contains(body, " of ") || strings.Contains(body, " elided"))
	moreCountOK := false
	if countStr, _, ok := strings.Cut(body, " more line"); ok {
		if _, err := strconv.Atoi(countStr); err == nil {
			moreCountOK = true
		}
	}
	moreNotice := (strings.HasPrefix(body, "More lines in ") || moreCountOK) &&
		strings.Contains(body, " in ") &&
		strings.Contains(body, ". Use ") &&
		strings.HasSuffix(body, " to continue")
	elidedNotice := (strings.HasPrefix(body, "…") || strings.HasPrefix(body, "...")) &&
		strings.Contains(body, "ln elided;") &&
		strings.Contains(body, "re-read needed ranges")
	oversizedNotice := strings.HasPrefix(body, "Line ") &&
		strings.Contains(body, " exceeds ") &&
		strings.Contains(body, " limit.")
	return showingNotice || moreNotice || elidedNotice || oversizedNotice
}

func isColumnTruncationNotice(line string) bool {
	return columnTruncNoticeRE.MatchString(strings.TrimSpace(line))
}

func hasColumnTruncationClause(line string) bool {
	t := strings.TrimSpace(line)
	return columnTruncNoticeRE.MatchString(t) || columnTruncInNoticeRE.MatchString(t)
}

func isReadProjectionNotice(line string) bool {
	return isReadTruncationNotice(line) || isColumnTruncationNotice(line)
}

func isReadMetadataLine(line string) bool {
	return isReadTruncationNotice(line) ||
		isColumnTruncationNotice(line) ||
		readRangeElisionRE.MatchString(line) ||
		readSingleElideRE.MatchString(line)
}

func lastNonBlankLine(content string) (string, bool) {
	trimmed := strings.TrimRight(content, " \t\r\n")
	if trimmed == "" {
		return "", false
	}
	start := len(trimmed)
	for start > 0 && trimmed[start-1] != '\n' && trimmed[start-1] != '\r' {
		start--
	}
	return trimmed[start:], true
}

func endsWithReadTruncationNotice(content string) bool {
	line, ok := lastNonBlankLine(content)
	return ok && isReadProjectionNotice(line)
}

func readProjectionPayloadLength(content string) (int, bool) {
	text := normalizeCRLFToLF(content)
	end := len(text)
	noticeStart := -1
	for end > 0 {
		start := strings.LastIndexByte(text[:end], '\n') + 1
		line := text[start:end]
		if strings.TrimSpace(line) != "" {
			if isReadProjectionNotice(line) {
				noticeStart = start
			}
			break
		}
		end = start - 1
	}
	if noticeStart == -1 {
		return 0, false
	}
	payloadEnd := noticeStart
	for payloadEnd > 0 {
		prevStart := 0
		if payloadEnd > 1 {
			prevStart = strings.LastIndexByte(text[:payloadEnd-1], '\n') + 1
		}
		if strings.TrimSpace(text[prevStart:payloadEnd-1]) != "" {
			break
		}
		payloadEnd = prevStart
	}
	if payloadEnd == 0 {
		return 0, true
	}
	return payloadEnd - 1, true
}

// assertNotShorterReadProjection refuses overwriting a file with a truncated read projection.
// upstream: packages/coding-agent/src/tools/write.ts:295
func assertNotShorterReadProjection(displayPath, rawContent string, currentContent *string, writeContent string) error {
	if currentContent == nil {
		return nil
	}
	return assertNotShorterReadProjectionWithSize(displayPath, rawContent, int64(len(normalizeCRLFToLF(*currentContent))), writeContent)
}

func assertNotShorterReadProjectionWithSize(displayPath, rawContent string, currentLen int64, writeContent string) error {
	rawPayloadLen, ok := readProjectionPayloadLength(rawContent)
	if !ok {
		return nil
	}
	lastLine, _ := lastNonBlankLine(rawContent)
	columnCut := hasColumnTruncationClause(lastLine)
	if !columnCut {
		payloadLen := int64(rawPayloadLen)
		if writeContent != rawContent {
			payloadLen = int64(len(normalizeCRLFToLF(writeContent)))
		}
		if payloadLen >= currentLen {
			return nil
		}
	}
	if columnCut {
		return fmt.Errorf(
			"Refusing to overwrite '%s' with an incomplete read projection: the content ends with an omp read column-cap notice, so its cut lines would replace the full ones. Re-read the cut lines in raw mode and write the complete file, or use edit for a partial change.",
			displayPath,
		)
	}
	return fmt.Errorf(
		"Refusing to overwrite '%s' with an incomplete read projection: the content ends with an omp read truncation notice and covers less than the current source, so it would discard unseen content. Re-read the omitted ranges and write the complete file, or use edit for a partial change.",
		displayPath,
	)
}

// stripHashlinePrefixes strips numbered display prefixes when every content line carries one
// and line numbers count up by one.
// upstream: crates/edit/src/modes/hashline/prefixes.rs:163
func stripHashlinePrefixes(lines []string) ([]string, bool) {
	nonEmpty := 0
	headerCount := 0
	prefixCount := 0
	for _, line := range lines {
		if line == "" || isReadMetadataLine(line) {
			continue
		}
		nonEmpty++
		if hlHeaderStrictRE.MatchString(line) {
			headerCount++
			continue
		}
		if hlPrefixRE.MatchString(line) {
			prefixCount++
		}
	}
	contentLines := nonEmpty - headerCount
	if nonEmpty == 0 || contentLines == 0 || prefixCount != contentLines {
		return lines, false
	}
	// Check monotonic +1 numbering between skips/blanks/headers.
	hasPrev := false
	var prev uint64
	for _, line := range lines {
		if line == "" || isReadMetadataLine(line) || hlHeaderStrictRE.MatchString(line) {
			hasPrev = false
			continue
		}
		m := hlPrefixRE.FindStringSubmatch(line)
		if m == nil {
			return lines, false
		}
		num, err := strconv.ParseUint(m[1], 10, 64)
		if err != nil {
			return lines, false
		}
		if hasPrev && prev+1 != num {
			return lines, false
		}
		prev = num
		hasPrev = true
	}

	out := make([]string, 0, len(lines))
	changed := false
	for _, line := range lines {
		if isReadMetadataLine(line) || hlHeaderStrictRE.MatchString(line) {
			changed = true
			continue
		}
		cleaned := line
		for {
			loc := hlPrefixRE.FindStringIndex(cleaned)
			if loc == nil {
				break
			}
			cleaned = cleaned[loc[1]:]
			changed = true
		}
		out = append(out, cleaned)
	}
	if !changed {
		return lines, false
	}
	return out, true
}

func prefixesEvidenced(store *hashline.Store, lines []string, absPath string) bool {
	for _, line := range lines {
		if hlHeaderLooseRE.MatchString(line) {
			return true
		}
	}
	if absPath == "" || store == nil {
		return false
	}
	snap, ok := store.Head(absPath)
	if !ok {
		return false
	}
	source := strings.Split(snap.Text, "\n")
	numbered := 0
	matched := 0
	for _, line := range lines {
		m := numberedRowRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		numbered++
		idx, err := strconv.Atoi(m[1])
		if err == nil && idx >= 1 && idx <= len(source) && source[idx-1] == m[2] {
			matched++
		}
	}
	return matched > 0 && matched >= min(2, numbered)
}

// stripWriteContent strips copied hashline display headers/prefixes from write content
// when evidenced as copied output.
// upstream: packages/coding-agent/src/tools/write-content.ts:71-124
func stripWriteContent(store *hashline.Store, content, absPath string) (string, bool) {
	if content == "" {
		return content, false
	}
	lines := strings.Split(content, "\n")
	if cleaned, ok := stripHashlinePrefixes(lines); ok {
		cleanedText := strings.Join(cleaned, "\n")
		if cleanedText != content && prefixesEvidenced(store, lines, absPath) {
			return cleanedText, true
		}
		return content, false
	}
	headerIdx := -1
	for i, line := range lines {
		if strings.TrimSpace(line) != "" {
			headerIdx = i
			break
		}
	}
	if headerIdx == -1 || !hlHeaderLooseRE.MatchString(lines[headerIdx]) {
		return content, false
	}
	withoutHeader := make([]string, 0, len(lines)-1)
	withoutHeader = append(withoutHeader, lines[:headerIdx]...)
	withoutHeader = append(withoutHeader, lines[headerIdx+1:]...)
	if cleaned, ok := stripHashlinePrefixes(withoutHeader); ok {
		cleanedText := strings.Join(cleaned, "\n")
		if cleanedText != content {
			return cleanedText, true
		}
	}
	return content, false
}

// autoGeneratedMessage returns omp's refusal message if displayPath/head indicates an
// auto-generated file, or "" otherwise.
// upstream: crates/edit/src/path_policy.rs:234-248,558-675
func autoGeneratedMessage(displayPath string, head []byte) string {
	marker := generatedFilenameMarker(displayPath)
	if marker == "" {
		limit := min(len(head), 1024)
		marker = detectGeneratedMarker(string(head[:limit]), displayPath)
	}
	if marker == "" {
		return ""
	}
	return fmt.Sprintf(
		"Cannot modify auto-generated file: %s\n\n"+
			"This file appears to be automatically generated (detected marker: %q).\n"+
			"Auto-generated files should not be edited directly. Instead:\n"+
			"1. Find the source file or generator configuration\n"+
			"2. Make changes to the source\n"+
			"3. Regenerate the file",
		displayPath, marker,
	)
}

func generatedFilenameMarker(displayPath string) string {
	norm := strings.ReplaceAll(displayPath, "\\", "/")
	name := path.Base(norm)
	for _, re := range genFilenamePatterns {
		if re.MatchString(name) {
			return name
		}
	}
	return ""
}

func detectGeneratedMarker(content, displayPath string) string {
	styles := commentStylesForPath(displayPath)
	if len(styles) == 0 {
		return ""
	}
	header := leadingCommentText(strings.TrimPrefix(content, "\uFEFF"), styles)
	for _, re := range genMarkerPatterns {
		if found := re.FindString(header); found != "" {
			return found
		}
	}
	return ""
}

func commentStylesForPath(displayPath string) []string {
	norm := strings.ReplaceAll(displayPath, "\\", "/")
	name := strings.ToLower(path.Base(norm))
	switch name {
	case "dockerfile", "makefile", "justfile":
		return []string{"hash"}
	}
	ext := strings.TrimPrefix(path.Ext(name), ".")
	switch ext {
	case "c", "cc", "cpp", "cs", "dart", "go", "h", "hpp", "java", "js", "jsx", "kt", "kts", "mjs", "cjs", "php", "rs", "scala", "swift", "ts", "tsx":
		return []string{"slash"}
	case "py", "rb", "sh", "bash", "zsh", "yml", "yaml", "toml", "ini", "cfg", "conf", "env", "pl", "r":
		return []string{"hash"}
	case "sql":
		return []string{"sql"}
	case "html", "htm", "xml", "svg", "xhtml":
		return []string{"html"}
	default:
		return nil
	}
}

func hasStyle(styles []string, want string) bool {
	for _, s := range styles {
		if s == want {
			return true
		}
	}
	return false
}

func leadingCommentText(content string, styles []string) string {
	var result []string
	started := false
	slashBlock := false
	htmlBlock := false
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) > 40 {
		lines = lines[:40]
	}
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if i == 0 && strings.HasPrefix(line, "#!") {
			continue
		}
		if slashBlock {
			result = append(result, line)
			slashBlock = !strings.Contains(line, "*/")
			continue
		}
		if htmlBlock {
			result = append(result, line)
			htmlBlock = !strings.Contains(line, "-->")
			continue
		}
		if line == "" {
			if started {
				result = append(result, "")
			}
			continue
		}
		accepted := (hasStyle(styles, "slash") && strings.HasPrefix(line, "//")) ||
			(hasStyle(styles, "hash") && strings.HasPrefix(line, "#")) ||
			(hasStyle(styles, "sql") && strings.HasPrefix(line, "--")) ||
			(hasStyle(styles, "html") && strings.HasPrefix(line, "<!--"))
		if !accepted && !(hasStyle(styles, "slash") && strings.HasPrefix(line, "/*")) {
			break
		}
		started = true
		result = append(result, line)
		if hasStyle(styles, "slash") && strings.HasPrefix(line, "/*") {
			slashBlock = !strings.Contains(line, "*/")
		}
		if hasStyle(styles, "html") && strings.HasPrefix(line, "<!--") {
			htmlBlock = !strings.Contains(line, "-->")
		}
	}
	return strings.Join(result, "\n")
}

// textFileConvention holds the UTF-8 BOM and CRLF convention of an existing text file.
// upstream: packages/coding-agent/src/tools/write.ts:387-457
type textFileConvention struct {
	bom  bool
	crlf bool
}

func readTextFileConvention(absPath string) (*textFileConvention, error) {
	f, err := os.Open(absPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	buf := make([]byte, 8192)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return nil, err
	}
	chunk := buf[:n]
	if isProbablyBinary(chunk) {
		return nil, nil
	}
	bom := len(chunk) >= 3 && chunk[0] == 0xef && chunk[1] == 0xbb && chunk[2] == 0xbf

	var prev byte
	for {
		if lf := bytes.IndexByte(chunk, '\n'); lf >= 0 {
			crlf := (lf > 0 && chunk[lf-1] == '\r') || (lf == 0 && prev == '\r')
			return &textFileConvention{bom: bom, crlf: crlf}, nil
		}
		if len(chunk) == 0 {
			return &textFileConvention{bom: bom, crlf: false}, nil
		}
		prev = chunk[len(chunk)-1]
		scanBuf := make([]byte, 64*1024)
		rn, rerr := f.Read(scanBuf)
		if rn == 0 {
			if rerr != nil && rerr != io.EOF {
				return nil, rerr
			}
			return &textFileConvention{bom: bom, crlf: false}, nil
		}
		chunk = scanBuf[:rn]
	}
}

func applyTextFileConvention(content string, conv *textFileConvention) (string, textFileConvention) {
	var applied textFileConvention
	if conv == nil {
		return content, applied
	}
	text := content
	if conv.crlf && strings.Contains(text, "\n") && !strings.Contains(text, "\r") {
		text = strings.ReplaceAll(text, "\n", "\r\n")
		applied.crlf = true
	}
	if conv.bom && len(text) > 0 && !strings.HasPrefix(text, "\uFEFF") {
		text = "\uFEFF" + text
		applied.bom = true
	}
	return text, applied
}

func textFileConventionNote(applied textFileConvention, finalText string) string {
	var kept []string
	if applied.crlf && strings.Contains(finalText, "\r\n") {
		kept = append(kept, "CRLF line endings")
	}
	if applied.bom && strings.HasPrefix(finalText, "\uFEFF") {
		kept = append(kept, "UTF-8 BOM")
	}
	if len(kept) == 0 {
		return ""
	}
	return fmt.Sprintf("Note: kept the replaced file's %s.", strings.Join(kept, " and "))
}

// maybeMarkExecutableForShebang adds execute bits (chmod a+x) when content starts with
// `#!` (excluding Rust inner attributes `#![`). Returns true only if bits changed and stuck.
// upstream: packages/coding-agent/src/tools/write.ts:372
func maybeMarkExecutableForShebang(absPath, content string) bool {
	if !strings.HasPrefix(content, "#!") || strings.HasPrefix(content, "#![") {
		return false
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return false
	}
	cur := info.Mode().Perm()
	next := cur | 0o111
	if next == cur {
		return false
	}
	if err := os.Chmod(absPath, next); err != nil {
		return false
	}
	after, err := os.Stat(absPath)
	if err != nil {
		return false
	}
	return (after.Mode().Perm() & 0o111) == 0o111
}

// specialFileKind returns omp's description for non-regular, non-directory files.
// upstream: packages/coding-agent/src/tools/path-utils.ts:197
func specialFileKind(info os.FileInfo) string {
	mode := info.Mode()
	if mode.IsRegular() || mode.IsDir() {
		return ""
	}
	if mode&os.ModeCharDevice != 0 {
		return "character device"
	}
	if mode&os.ModeDevice != 0 {
		return "block device"
	}
	if mode&os.ModeNamedPipe != 0 {
		return "FIFO"
	}
	if mode&os.ModeSocket != 0 {
		return "socket"
	}
	return "special file"
}
