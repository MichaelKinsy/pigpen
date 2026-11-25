package files

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/dirs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/hashline"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

const (
	// DefaultReadLimit matches omp's read.defaultLimit (300 lines for bare reads).
	// upstream: packages/coding-agent/src/tools/settings.ts:163
	DefaultReadLimit = 300
	// DefaultMaxLines matches omp's DEFAULT_MAX_LINES (3000).
	// upstream: packages/tui/src/tools/streaming-output.ts:12
	DefaultMaxLines = 3000
	// DefaultMaxBytes matches omp's DEFAULT_MAX_BYTES (50 KB).
	// upstream: packages/tui/src/tools/streaming-output.ts:14
	DefaultMaxBytes = 50 * 1024
	// DefaultSpillThreshold matches omp's tools.artifactSpillThreshold default (50 KB).
	// upstream: packages/coding-agent/src/tools/settings.ts:15
	DefaultSpillThreshold = 50 * 1024
	// DefaultOutputMaxColumns matches omp's tools.outputMaxColumns default (768 chars).
	// upstream: packages/coding-agent/src/tools/settings.ts:87
	DefaultOutputMaxColumns = 768
	// SnapshotMaxBytes matches omp's SNAPSHOT_MAX_BYTES (4 MiB): files up to this
	// size are buffered whole; larger files stream.
	// upstream: packages/coding-agent/src/tools/read.ts:218
	SnapshotMaxBytes = 4 * 1024 * 1024
	// HardSnapshotCapBytes matches omp's 64 MiB cap above which no hashline tag is computed.
	// upstream: crates/edit/src/store.rs:23-26
	HardSnapshotCapBytes = 64 * 1024 * 1024
	// MaxImageBytes matches omp's MAX_IMAGE_SIZE (20 MB).
	MaxImageBytes = 20 * 1024 * 1024
	// RangeLeadingContextLines matches omp's RANGE_LEADING_CONTEXT_LINES.
	// upstream: packages/coding-agent/src/tools/read-format.ts:258
	RangeLeadingContextLines = 1
	// RangeTrailingContextLines matches omp's RANGE_TRAILING_CONTEXT_LINES.
	// upstream: packages/coding-agent/src/tools/read-format.ts:259
	RangeTrailingContextLines = 3
)

// readMaxColumns controls the per-line column cap for read (default 768; overridable in tests).
var readMaxColumns = DefaultOutputMaxColumns

// readDefaultLimit controls the bare-read default line limit (default 300; overridable in tests).
var readDefaultLimit = DefaultReadLimit

type columnCutInfo struct {
	maxColumn  int
	first      int
	last       int
	firstBytes int
}

func addColumnCut(cut *columnCutInfo, maxCol, lineNum, sourceBytes int) *columnCutInfo {
	if cut == nil {
		return &columnCutInfo{maxColumn: maxCol, first: lineNum, last: lineNum, firstBytes: sourceBytes}
	}
	if lineNum > cut.last {
		cut.last = lineNum
	}
	if lineNum < cut.first {
		cut.first = lineNum
		cut.firstBytes = sourceBytes
	}
	return cut
}

// truncateLineChars truncates line to maxChars runes, appending '…' if cut.
// upstream: packages/tui/src/tools/streaming-output.ts:315
func truncateLineChars(line string, maxChars int) (string, bool) {
	if maxChars <= 0 || len(line) <= maxChars {
		return line, false
	}
	count := 0
	for i := range line {
		if count == maxChars {
			return line[:i] + "…", true
		}
		count++
	}
	return line, false
}

func formatColumnCutNotice(cut *columnCutInfo, selectorBase string) string {
	if cut == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n\n[Some lines truncated to %d chars", cut.maxColumn)
	if cut.firstBytes > DefaultMaxBytes {
		fmt.Fprintf(&b, ". Use %s:raw:%d-%d to read line %d uncut, byte-capped at %s",
			selectorBase, cut.first, cut.first, cut.first, formatBytes(DefaultMaxBytes))
	} else {
		fmt.Fprintf(&b, ". Use %s:raw:%d-%d to read line %d whole",
			selectorBase, cut.first, cut.first, cut.first)
	}
	if cut.last > cut.first {
		fmt.Fprintf(&b, "; cut lines run to line %d, each read the same way", cut.last)
	}
	b.WriteByte(']')
	return b.String()
}

// detectImageMIME sniffs raster image magic bytes (PNG, JPEG, GIF, WebP).
// upstream: packages/coding-agent/src/tools/read.ts:2056
func detectImageMIME(data []byte) string {
	if len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}) {
		return "image/png"
	}
	if len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff {
		return "image/jpeg"
	}
	if len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a") {
		return "image/gif"
	}
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "image/webp"
	}
	return ""
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
	// Trim trailing incomplete UTF-8 sequence at the 8KB boundary.
	for len(sample) > 0 && !utf8.Valid(sample) {
		if len(sample) > 1 && !utf8.RuneStart(sample[len(sample)-1]) {
			sample = sample[:len(sample)-1]
			continue
		}
		if len(data) > 8192 && len(sample) >= 8189 {
			sample = sample[:len(sample)-1]
			continue
		}
		return true
	}
	return false
}

// locatedURLOpts carries internal-URL metadata for file-backed routed reads.
type locatedURLOpts struct {
	url            string
	immutable      bool
	unbounded      bool
	unspilledPages bool
}

// executeRead implements omp's `read` tool without an sdk.Context (used in standalone tests).
func executeRead(store *hashline.Store, cwd, rawPath string) (sdk.ToolResult, error) {
	return executeReadWithCtx(sdk.Context{}, store, cwd, rawPath)
}

// executeReadWithCtx implements omp's `read` tool for internal URLs, external URLs, local files,
// directories, archives, notebooks, and raster images.
// upstream: packages/coding-agent/src/tools/read.ts
func executeReadWithCtx(ctx sdk.Context, store *hashline.Store, cwd, rawPath string) (sdk.ToolResult, error) {
	// 1. Internal URL routing (skill://, local://, agent://, proc://, etc.).
	// upstream: packages/coding-agent/src/tools/read.ts:2217
	if res, handled, err := tryReadInternalURL(ctx, store, cwd, rawPath); handled {
		if err == nil {
			// upstream: packages/coding-agent/src/tools/read.ts:2082-2083
			// Attach displayTarget from router.locateSync (unstandardized path from handler).
			uc := urls.Context{SDK: ctx, Cwd: cwd}
			if target, ok := urls.LocateSync(uc, rawPath); ok && target != "" {
				if d, ok := res.Details.(map[string]any); ok && d != nil {
					d["displayTarget"] = target
				}
			}
		}
		return res, err
	}

	// 2. External http(s) URLs.
	if urlTarget, err := parseReadURLTarget(rawPath); err != nil {
		return sdk.ToolResult{}, err
	} else if urlTarget != nil {
		sig := safeRunSignal(ctx)
		return executeReadURL(sig, urlTarget)
	}

	return readFilesystemPath(ctx, store, cwd, rawPath, nil)
}

func safeRunSignal(ctx sdk.Context) context.Context {
	var sig context.Context
	func() {
		defer func() { _ = recover() }()
		sig = ctx.Signal()
	}()
	if sig != nil {
		return sig
	}
	return context.Background()
}

// splitInternalURLSelector peels a trailing read selector from an internal URL when
// the scheme's spec accepts line selectors.
func splitInternalURLSelector(rawPath string) (urlPart, selStr string) {
	u, err := urls.Parse(rawPath)
	if err != nil {
		return rawPath, ""
	}
	h, ok := urls.Lookup(u.Scheme)
	if !ok {
		return rawPath, ""
	}
	if h.Spec().Selectors != urls.SelectorsLines {
		return rawPath, ""
	}
	schemePrefix := rawPath[:strings.Index(rawPath, "://")+3]
	rest := rawPath[len(schemePrefix):]
	baseRest, sel := SplitPathAndSel(rest)
	if sel == "" {
		return rawPath, ""
	}
	return schemePrefix + baseRest, sel
}

func tryReadInternalURL(ctx sdk.Context, store *hashline.Store, cwd, rawPath string) (sdk.ToolResult, bool, error) {
	trimmed := normalizeAtPrefix(strings.TrimSpace(rawPath))
	if !urls.CanHandle(trimmed) {
		return sdk.ToolResult{}, false, nil
	}
	urlPart, selStr := splitInternalURLSelector(trimmed)
	parsed, err := ParseSel(selStr)
	if err != nil {
		return sdk.ToolResult{}, true, err
	}
	if selStr != "" && parsed.Kind == SelectorNone {
		return sdk.ToolResult{}, true, invalidSelectorErr(selStr)
	}
	uc := urls.Context{SDK: ctx, Cwd: cwd}
	target, err := urls.ResolveTarget(uc, urlPart)
	if err != nil {
		return sdk.ToolResult{}, true, err
	}
	if target == nil {
		return sdk.ToolResult{}, false, nil
	}
	canonicalURL := target.URL.RawHref
	if canonicalURL == "" {
		canonicalURL = target.URL.Href
	}
	effectiveSel := selStr
	if target.Selector != "" {
		effectiveSel = target.Selector
	}

	if target.Kind == "file" {
		// Keep target.Path as located by the URL handler: skill:// paths inside the project dir
		// are already standardized if derived from cwd, while local:// and agent:// paths come
		// from os.tmpdir() + realpath and carry /private/... in omp.
		// upstream: packages/coding-agent/src/tools/read.ts:2266,2292,2298
		targetPath := target.Path
		if target.URL.Scheme == "skill" {
			targetPath = dirs.StandardizeMacOSPath(target.Path)
		}
		readArg := targetPath
		if effectiveSel != "" {
			readArg = targetPath + ":" + effectiveSel
		}
		loc := &locatedURLOpts{
			url:            canonicalURL,
			immutable:      target.Spec.Immutable,
			unbounded:      target.Spec.Unbounded,
			unspilledPages: target.Spec.UnspilledPages,
		}
		res, err := readFilesystemPath(ctx, store, cwd, readArg, loc)
		if err != nil {
			return sdk.ToolResult{}, true, err
		}
		details := ensureDetailsMap(&res)
		if _, ok := details["resolvedPath"]; !ok {
			details["resolvedPath"] = targetPath
		}
		// upstream: packages/coding-agent/src/tools/read.ts:2294
		if _, hasCT := details["contentType"]; !hasCT && isMarkdownPath(targetPath) {
			details["contentType"] = "text/markdown"
		}
		setMetaSource(details, "internal", canonicalURL)
		// upstream: packages/coding-agent/src/tools/read.ts:2296-2303
		if target.URL.Scheme == "skill" && !parsed.IsRaw() {
			provenance := fmt.Sprintf("[Skill file: %s]", targetPath)
			if res.Content != "" {
				res.Content = provenance + "\n" + res.Content
			} else {
				res.Content = provenance
			}
		}
		return res, true, nil
	}

	// Kind == "resource": resolve through the handler.
	res, err := urls.Resolve(uc, canonicalURL)
	if err != nil {
		return sdk.ToolResult{}, true, err
	}
	if res.Bytes != nil {
		if !parsed.IsRaw() {
			if mime := detectImageMIME(res.Bytes); mime != "" {
				details := copyDetails(res.Details)
				setMetaSource(details, "internal", canonicalURL)
				return sdk.ToolResult{
					Content: fmt.Sprintf("Read image file [%s]", mime),
					Images:  []sdk.ImageContent{{Data: base64.StdEncoding.EncodeToString(res.Bytes), MimeType: mime}},
					Details: details,
				}, true, nil
			}
			if isProbablyBinary(res.Bytes) {
				details := copyDetails(res.Details)
				setMetaSource(details, "internal", canonicalURL)
				return sdk.ToolResult{
					Content: fmt.Sprintf("[Cannot read binary resource '%s' (%s); not valid UTF-8 text. Use ':raw' to read bytes verbatim.]", canonicalURL, formatBytes(int64(len(res.Bytes)))),
					Details: details,
				}, true, nil
			}
		}
		res.Content = string(res.Bytes)
	}

	if res.Value {
		if parsed.Kind != SelectorNone && parsed.Kind != SelectorRaw {
			return sdk.ToolResult{}, true, fmt.Errorf("Cannot combine query extraction with line selectors")
		}
		details := copyDetails(res.Details)
		if parsed.Kind == SelectorRaw {
			details["raw"] = true
		}
		setMetaSource(details, "internal", canonicalURL)
		// upstream: read.ts:3588-3594 returns resource.content as-is without appending notes.
		return sdk.ToolResult{Content: res.Content, Details: details}, true, nil
	}

	immutable := target.Spec.Immutable
	if res.Immutable != nil {
		immutable = *res.Immutable
	}
	unbounded := target.Spec.Unbounded
	if res.Unbounded != nil {
		unbounded = *res.Unbounded
	}

	// upstream: read.ts:3601-3604, read-format.ts:455 — header path for resources is
	// formatPathRelativeToCwd(resource.sourcePath, cwd), and resource.notes are not appended.
	// Project-derived schemes (`conflict://`, `rule://`, `skill://`) inherit omp's standardized
	// session.cwd (`dirs.ts:148-158`), while `local://`/`agent://` keep their realpath under os.tmpdir().
	srcPath := res.SourcePath
	if srcPath != "" && (target.URL.Scheme == "conflict" || target.URL.Scheme == "rule" || target.URL.Scheme == "skill") {
		srcPath = dirs.StandardizeMacOSPath(srcPath)
	}
	resDisplayPath := canonicalURL
	if srcPath != "" {
		resDisplayPath = formatPathRelativeToCwd(srcPath, cwd)
	}
	out, err := renderInMemoryText(res.Content, resDisplayPath, "resource", parsed, store, srcPath, res.SourceStartLine, unbounded, immutable)
	if err != nil {
		return sdk.ToolResult{}, true, err
	}
	details := ensureDetailsMap(&out)
	for k, v := range res.Details {
		details[k] = v
	}
	if srcPath != "" {
		details["resolvedPath"] = srcPath
	}
	if res.ContentType != "" {
		details["contentType"] = res.ContentType
	}
	if res.IsDirectory {
		details["isDirectory"] = true
	}
	setMetaSource(details, "internal", canonicalURL)
	return out, true, nil
}

func setMetaSource(details map[string]any, srcType, value string) {
	meta, ok := details["meta"].(map[string]any)
	if !ok || meta == nil {
		meta = map[string]any{}
		details["meta"] = meta
	}
	if srcType == "path" {
		value = dirs.StandardizeMacOSPath(value)
	}
	meta["source"] = map[string]any{"type": srcType, "value": value}
}

func setMetaTruncation(details map[string]any, trunc map[string]any) {
	meta, ok := details["meta"].(map[string]any)
	if !ok || meta == nil {
		meta = map[string]any{}
		details["meta"] = meta
	}
	meta["truncation"] = trunc
}

// setMetaColumnTruncated records meta.limits.columnTruncated matching omp's OutputMetaBuilder.columnTruncated.
// upstream: packages/coding-agent/src/tools/read.ts:3485-3496, output-meta.ts:397-420
func setMetaColumnTruncated(details map[string]any, cut *columnCutInfo, selectorBase string, rawLineBudget int) {
	if cut == nil || cut.maxColumn <= 0 {
		return
	}
	meta, ok := details["meta"].(map[string]any)
	if !ok || meta == nil {
		meta = map[string]any{}
		details["meta"] = meta
	}
	limits, ok := meta["limits"].(map[string]any)
	if !ok || limits == nil {
		limits = map[string]any{}
		meta["limits"] = limits
	}
	ct := map[string]any{
		"maxColumn":    cut.maxColumn,
		"unit":         "chars",
		"lines":        map[string]any{"first": cut.first, "last": cut.last},
		"selectorBase": selectorBase,
	}
	if rawLineBudget > 0 && cut.firstBytes > rawLineBudget {
		ct["rawByteCap"] = rawLineBudget
	}
	limits["columnTruncated"] = ct
}

// isMarkdownPath mirrors packages/tui/src/lang-from-path.ts:109-114,230-232.
func isMarkdownPath(p string) bool {
	lower := strings.ToLower(p)
	for _, ext := range []string{".md", ".markdown", ".mdx", ".mdc", ".mkd", ".mdown"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// checkUnsupportedJSONQuery returns an explicit unsupported-feature error when
// rawPath looks like `<existing-file>?q=<jq>` (and does not exist literally on disk).
func checkUnsupportedJSONQuery(basePath, cwd string) error {
	qIdx := strings.IndexByte(basePath, '?')
	if qIdx <= 0 {
		return nil
	}
	filePart := basePath[:qIdx]
	queryStr := basePath[qIdx+1:]
	hasJSONParam := false
	for part := range strings.SplitSeq(queryStr, "&") {
		k, _, _ := strings.Cut(part, "=")
		switch k {
		case "q", "query", "raw", "compact", "offset", "limit":
			hasJSONParam = true
		}
	}
	if !hasJSONParam {
		return nil
	}
	absFile := resolveToCwd(filePart, cwd)
	info, err := os.Stat(absFile)
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	return fmt.Errorf("JSON/JSONL `?q=` jq filter queries are not supported in oh-my-pig (read '%s' with line selectors or use jq via bash instead).", filePart)
}

func copyDetails(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func contiguousLineNums(start, count int) []int {
	out := make([]int, count)
	for i := range count {
		out[i] = start + i
	}
	return out
}

func contiguousLineNumAny(start, count int) []any {
	out := make([]any, count)
	for i := range count {
		out[i] = start + i
	}
	return out
}

// countTextLines matches omp's read-format.ts:169-171: 0 for empty text, otherwise
// N newlines => N+1 lines (a trailing newline yields one more numbered row in displayContent).
func countTextLines(text string) int {
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}

// checkLineColDiagnosis detects `file:line:col` typos when `file` exists and returns omp's message.
// upstream: packages/coding-agent/src/tools/read.ts:2524-2541
func checkLineColDiagnosis(localReadPath, selStr, cwd string) error {
	if selStr == "" || !allDigits(selStr) {
		return nil
	}
	prefixPath, prefixSel := SplitPathAndSel(localReadPath)
	if prefixSel == "" || !allDigits(prefixSel) {
		return nil
	}
	absPrefix := resolveToCwd(prefixPath, cwd)
	if _, err := os.Lstat(absPrefix); err != nil {
		return nil
	}
	pNum, _ := strconv.Atoi(prefixSel)
	sNum, _ := strconv.Atoi(selStr)
	rangeHint := ""
	if sNum > pNum {
		rangeHint = fmt.Sprintf(" or :%s-%s for a range", prefixSel, selStr)
	}
	return fmt.Errorf("Invalid line selector ':%s:%s'. Use :%s for a single line%s.", prefixSel, selStr, prefixSel, rangeHint)
}

func readFilesystemPath(ctx sdk.Context, store *hashline.Store, cwd, rawPath string, loc *locatedURLOpts) (sdk.ToolResult, error) {
	// Check archive targets (`archive.zip`, `archive.zip:member/path:1-20`, `archive.tar.gz:member`).
	if archiveAbs, archiveDisplay, memberPath, selStr, ok := splitArchivePath(rawPath, cwd); ok {
		parsed, err := ParseSel(selStr)
		if err != nil {
			return sdk.ToolResult{}, err
		}
		archiveReadPath := rawPath
		if loc != nil {
			archiveReadPath = loc.url
		}
		return readArchive(archiveAbs, archiveDisplay, archiveReadPath, memberPath, parsed)
	}

	var basePath, selStr string
	if loc != nil {
		// For located URLs, selector was explicitly appended after target.Path by tryReadInternalURL.
		basePath, selStr = SplitPathAndSelPreferringLiteral(rawPath, cwd)
	} else {
		basePath, selStr = SplitPathAndSelPreferringLiteral(rawPath, cwd)
	}
	parsed, err := ParseSel(selStr)
	if err != nil {
		return sdk.ToolResult{}, err
	}

	absPath := resolveToCwd(basePath, cwd)
	displayPath := formatPathRelativeToCwd(absPath, cwd)
	selectorBase := ""
	immutable := false
	unbounded := false
	unspilledPages := false
	if loc != nil {
		// upstream: read.ts:3195-3198 — hashline header uses the backing file path
		// (formatPathRelativeToCwd(renderAbsolutePath, cwd)); only continuation hints use loc.url.
		selectorBase = loc.url
		immutable = loc.immutable
		unbounded = loc.unbounded
		unspilledPages = loc.unspilledPages
	}

	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			if qErr := checkUnsupportedJSONQuery(basePath, cwd); qErr != nil {
				return sdk.ToolResult{}, qErr
			}
			if diagErr := checkLineColDiagnosis(basePath, selStr, cwd); diagErr != nil {
				return sdk.ToolResult{}, diagErr
			}
			// upstream: packages/coding-agent/src/tools/read.ts:2543
			return sdk.ToolResult{}, fmt.Errorf("Path '%s' not found", basePath)
		}
		return sdk.ToolResult{}, err
	}
	if info.IsDir() {
		text, err := readDirectory(absPath, parsed)
		if err != nil {
			return sdk.ToolResult{}, err
		}
		// upstream: packages/coding-agent/src/tools/read.ts:3648-3651
		details := map[string]any{
			"isDirectory":  true,
			"resolvedPath": dirs.StandardizeMacOSPath(absPath),
		}
		setMetaSource(details, "path", absPath)
		return sdk.ToolResult{Content: text, Details: details}, nil
	}

	if kind := specialFileKind(info); kind != "" {
		// upstream: packages/coding-agent/src/tools/read.ts:2550
		return sdk.ToolResult{}, fmt.Errorf("Cannot read '%s': it is a %s, not a regular file or directory.", basePath, kind)
	}

	if parsed.Kind == SelectorConflicts {
		return readFileConflicts(ctx, cwd, absPath)
	}
	fileSize := info.Size()

	// Sniff header bytes first so large files aren't read into memory unnecessarily.
	headerBuf := make([]byte, 8192)
	f, err := os.Open(absPath)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	nHeader, readErr := f.Read(headerBuf)
	_ = f.Close()
	if readErr != nil && readErr != io.EOF {
		return sdk.ToolResult{}, readErr
	}
	headerBytes := headerBuf[:nHeader]

	// Raster image detection (unless :raw was explicitly requested).
	if !parsed.IsRaw() {
		if mime := detectImageMIME(headerBytes); mime != "" {
			if fileSize > MaxImageBytes {
				// upstream: packages/coding-agent/src/tools/read.ts:1760
				return sdk.ToolResult{}, fmt.Errorf("Image file too large: %s exceeds %s limit.", formatBytes(fileSize), formatBytes(MaxImageBytes))
			}
			data, err := os.ReadFile(absPath)
			if err != nil {
				return sdk.ToolResult{}, err
			}
			encoded := base64.StdEncoding.EncodeToString(data)
			details := map[string]any{"fileSize": fileSize}
			setMetaSource(details, "path", absPath)
			return sdk.ToolResult{
				Content: fmt.Sprintf("Read image file [%s]", mime),
				Images:  []sdk.ImageContent{{Data: encoded, MimeType: mime}},
				Details: details,
			}, nil
		}
	}

	// Binary guard (skipped in :raw mode).
	// upstream: packages/coding-agent/src/tools/read.ts:2803-2811
	if !parsed.IsRaw() && isProbablyBinary(headerBytes) {
		details := map[string]any{
			"resolvedPath": dirs.StandardizeMacOSPath(absPath),
		}
		setMetaSource(details, "path", absPath)
		return sdk.ToolResult{
			Content: fmt.Sprintf("[Cannot read binary file '%s' (%s); not valid UTF-8 text. Use ':raw' to read bytes verbatim.]", displayPath, formatBytes(fileSize)),
			Details: details,
		}, nil
	}

	// Jupyter notebook (.ipynb) rendered as editable cells unless :raw.
	// upstream: packages/coding-agent/src/tools/read.ts:2746
	if strings.HasSuffix(strings.ToLower(absPath), ".ipynb") && !parsed.IsRaw() {
		data, err := os.ReadFile(absPath)
		if err != nil {
			return sdk.ToolResult{}, err
		}
		return readNotebook(data, store, absPath, displayPath, parsed, immutable, unbounded)
	}

	// Large file streaming path (> SNAPSHOT_MAX_BYTES = 4 MiB) when not unbounded.
	// upstream: packages/coding-agent/src/tools/read.ts:218,3003
	if fileSize > SnapshotMaxBytes && !unbounded {
		return readLargeFileStreaming(ctx, store, cwd, absPath, displayPath, selectorBase, fileSize, parsed, immutable, unspilledPages)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return sdk.ToolResult{}, err
	}

	rawText := string(data)
	_, stripped := hashline.StripBOM(rawText)
	normalized := normalizeCRLFToLF(stripped)
	hasTrailingNL := strings.HasSuffix(normalized, "\n")

	// Unbounded schemes read whole with no page limits.
	// upstream: packages/coding-agent/src/tools/read.ts:2819-2828
	if unbounded {
		res, err := renderInMemoryText(rawText, displayPath, "file", parsed, store, absPath, 0, true, immutable)
		if err != nil {
			return sdk.ToolResult{}, err
		}
		details := ensureDetailsMap(&res)
		if loc != nil {
			details["resolvedPath"] = absPath
		} else {
			details["resolvedPath"] = dirs.StandardizeMacOSPath(absPath)
		}
		if loc != nil {
			setMetaSource(details, "internal", loc.url)
		} else {
			setMetaSource(details, "path", absPath)
		}
		return res, nil
	}

	var allLines []string
	if normalized != "" {
		allLines = hashline.SplitAddressableFileLines(normalized)
	}
	totalLines := len(allLines)
	rawMode := parsed.IsRaw()
	shouldAddHashLines := !rawMode && !immutable

	var header string
	var tag string
	if shouldAddHashLines {
		tag = hashline.FileHash(normalized)
		header = hashline.FormatHeader(displayPath, tag)
	}

	resolvedSel := parsed.ResolveTail(totalLines)
	if resolvedSel.IsMultiRange() {
		maxCol := 0
		if !rawMode {
			maxCol = readMaxColumns
		}
		text, displayedLines, dispContent, colCut := renderMultiRange(allLines, resolvedSel, displayPath, header, hasTrailingNL, shouldAddHashLines, maxCol)
		if shouldAddHashLines {
			store.RecordReadLines(absPath, normalized, displayedLines)
		} else if rawMode && !immutable && len(displayedLines) > 0 {
			store.RecordReadLines(absPath, normalized, displayedLines)
		}
		if colCut != nil {
			text += formatColumnCutNotice(colCut, selectorBase)
		}
		// upstream: packages/coding-agent/src/tools/read.ts:2026-2030,3462
		// Multi-range reads omit top-level totalLines (omp only sets it on single-window reads).
		details := map[string]any{
			"fileSize":    fileSize,
			"rowPrefixes": shouldAddHashLines,
		}
		if rawMode {
			details["raw"] = true
		}
		if dispContent != nil {
			details["displayContent"] = dispContent
		}
		if colCut != nil {
			setMetaColumnTruncated(details, colCut, selectorBase, DefaultMaxBytes)
		}
		if loc != nil {
			details["resolvedPath"] = absPath
			setMetaSource(details, "internal", loc.url)
		} else {
			setMetaSource(details, "path", absPath)
		}
		return sdk.ToolResult{Content: text, Details: details}, nil
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

	requestedStart := 0
	if hasOffset && offset > 1 {
		requestedStart = offset - 1
	}

	// Out-of-bounds check (graceful message, matching read.ts:3061-3075).
	if hasOffset && requestedStart >= totalLines {
		suggestion := "The file is empty."
		if totalLines > 0 {
			suggestion = fmt.Sprintf("Use %s:1- to read from the start, or %s:%d to read the last line.", selectorBase, selectorBase, totalLines)
		}
		resolvedOut := absPath
		if loc == nil {
			resolvedOut = dirs.StandardizeMacOSPath(absPath)
		}
		details := map[string]any{
			"resolvedPath": resolvedOut,
		}
		if rawMode {
			details["raw"] = true
		}
		if loc != nil {
			setMetaSource(details, "internal", loc.url)
		}
		return sdk.ToolResult{
			Content: fmt.Sprintf("Line %d is beyond end of file (%d lines total). %s", requestedStart+1, totalLines, suggestion),
			Details: details,
		}, nil
	}

	// Context expansion for single non-raw range reads (upstream: read.ts:2945).
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
	effectiveLimit := readDefaultLimit
	if hasLimit {
		effectiveLimit = limit
	}
	maxLinesToCollect := min(effectiveLimit+leadingCtx+trailingCtx, DefaultMaxLines)
	maxBytesForRead := computeReadByteBudget(startIdx, maxLinesToCollect, shouldAddHashLines, unspilledPages)

	maxCol := 0
	if !rawMode {
		maxCol = readMaxColumns
	}

	// Collect lines within line and byte budgets (charging capped width when maxCol > 0).
	// Also compute selectedBytes over the requested window (matching read.ts:431-434, 3164).
	selectedLineLimit := effectiveLimit + leadingCtx + trailingCtx
	selectedBytes := 0
	selectedLinesSeen := 0
	for i := startIdx; i < totalLines && selectedLinesSeen < selectedLineLimit; i++ {
		if selectedLinesSeen > 0 {
			selectedBytes++
		}
		selectedBytes += len(allLines[i])
		selectedLinesSeen++
	}

	var collected []string
	collectedBytes := 0
	stoppedByByteLimit := false
	for i := startIdx; i < totalLines && len(collected) < maxLinesToCollect; i++ {
		line := allLines[i]
		lineBytes := len(line)
		if maxCol > 0 && lineBytes > maxCol {
			if trunc, wasCut := truncateLineChars(line, maxCol); wasCut {
				lineBytes = len(trunc)
			}
		}
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
		details := map[string]any{"fileSize": fileSize, "totalLines": totalLines}
		if loc != nil {
			details["resolvedPath"] = absPath
			setMetaSource(details, "internal", loc.url)
		} else {
			setMetaSource(details, "path", absPath)
		}
		if shouldAddHashLines {
			return sdk.ToolResult{
				Content: fmt.Sprintf("[Line %d is %s, exceeds %s limit. Hashline output requires full lines; cannot emit an editable numbered preview for a truncated line.]",
					startLineDisplay, formatBytes(int64(firstBytes)), formatBytes(int64(maxBytesForRead))),
				Details: details,
			}, nil
		}
		snippet := truncateUTF8Bytes(allLines[startIdx], maxBytesForRead)
		if rawMode {
			details["raw"] = true
		}
		truncMeta := map[string]any{
			"direction":   "head",
			"truncatedBy": "bytes",
			"partialLine": true,
			"totalLines":  totalLines,
			"totalBytes":  firstBytes,
			"outputLines": 1,
			"outputBytes": len(snippet),
			"shownRange":  map[string]any{"start": startLineDisplay, "end": startLineDisplay},
		}
		setMetaTruncation(details, truncMeta)
		notice := fmt.Sprintf("Showing line %d (partial, %s of %s) of %d",
			startLineDisplay, formatBytes(int64(len(snippet))), formatBytes(int64(firstBytes)), totalLines)
		return sdk.ToolResult{Content: snippet + "\n\n[" + notice + "]", Details: details}, nil
	}
	// Empty file: in omp (read.ts:402, format.rs:120), an empty file has one empty
	// addressable line `1:` in non-raw hashline mode so `PUT 1.=1:` can populate it.
	if totalLines == 0 {
		details := map[string]any{"fileSize": fileSize, "totalLines": 0, "rowPrefixes": shouldAddHashLines}
		if loc != nil {
			details["resolvedPath"] = absPath
			setMetaSource(details, "internal", loc.url)
		} else {
			setMetaSource(details, "path", absPath)
		}
		if rawMode {
			details["raw"] = true
			return sdk.ToolResult{Content: "", Details: details}, nil
		}
		if shouldAddHashLines {
			store.RecordReadLines(absPath, normalized, []int{1})
			details["totalLines"] = 1
			details["displayContent"] = map[string]any{
				"text":        "",
				"startLine":   1,
				"lineNumbers": []any{1},
			}
			return sdk.ToolResult{Content: header + "\n1:", Details: details}, nil
		}
		return sdk.ToolResult{Content: "", Details: details}, nil
	}

	endIdx := startIdx + len(collected)
	wasTruncated := endIdx < totalLines

	// Apply display-only column truncation (never mutating snapshot text).
	// upstream: packages/coding-agent/src/tools/read.ts:3085-3104
	displayLines := collected
	var colCut *columnCutInfo
	cutSet := map[int]bool{}
	if !rawMode && maxCol > 0 {
		var cloned []string
		for i, line := range collected {
			if trunc, wasCut := truncateLineChars(line, maxCol); wasCut {
				if cloned == nil {
					cloned = make([]string, len(collected))
					copy(cloned, collected)
				}
				cloned[i] = trunc
				ln := startLineDisplay + i
				colCut = addColumnCut(colCut, maxCol, ln, len(line))
				cutSet[ln] = true
			}
		}
		if cloned != nil {
			displayLines = cloned
		}
	}

	var out string
	var dispContent map[string]any
	var seenLines []int

	if rawMode {
		out = strings.Join(collected, "\n")
		// upstream: packages/coding-agent/src/tools/read.ts:3116-3122
		// Raw output keeps the last line's terminator so consecutive ranges concatenate to the file:
		// every line has one except an unterminated final line.
		if len(collected) > 0 && (endIdx < totalLines || hasTrailingNL) {
			out += "\n"
		}
		// upstream: packages/coding-agent/src/tools/read.ts:3204-3214, 3373-3375
		// Emit displayContent on raw reads too using countTextLines(out) so a trailing
		// newline yields one more numbered row, matching omp's formatText.
		if len(collected) > 0 {
			dispContent = map[string]any{
				"text":        out,
				"startLine":   startLineDisplay,
				"lineNumbers": contiguousLineNumAny(startLineDisplay, countTextLines(out)),
			}
		}
		// upstream: packages/coding-agent/src/tools/read.ts:3362-3371
		if !immutable && len(collected) > 0 {
			store.RecordReadLines(absPath, normalized, contiguousLineNums(startLineDisplay, len(collected)))
		}
	} else {
		// Single-range / partial window gets block open/close context lines just like comma lists.
		// upstream: packages/coding-agent/src/tools/read.ts:3218-3253
		isWholeFile := startIdx == 0 && endIdx == totalLines
		if !isWholeFile {
			visible := make(map[int]bool, len(displayLines))
			displayByLine := make(map[int]string, len(displayLines))
			for i, dl := range displayLines {
				ln := startLineDisplay + i
				visible[ln] = true
				displayByLine[ln] = dl
			}
			ctxLines := hashline.FindBlockContextLines(allLines, visible)
			allNums := make([]int, 0, len(visible)+len(ctxLines))
			for ln := range visible {
				allNums = append(allNums, ln)
			}
			for ln := range ctxLines {
				if ln >= 1 && ln <= totalLines && !visible[ln] {
					allNums = append(allNums, ln)
				}
			}
			slices.Sort(allNums)

			var formattedRows []string
			var plainRows []string
			var lineNumsAny []any
			prev := 0
			for _, ln := range allNums {
				if prev > 0 && ln > prev+1 {
					formattedRows = append(formattedRows, "…")
					plainRows = append(plainRows, "…")
					lineNumsAny = append(lineNumsAny, nil)
				}
				rowText, ok := displayByLine[ln]
				if !ok {
					src := allLines[ln-1]
					if maxCol > 0 {
						if trunc, wasCut := truncateLineChars(src, maxCol); wasCut {
							rowText = trunc
							colCut = addColumnCut(colCut, maxCol, ln, len(src))
							cutSet[ln] = true
						} else {
							rowText = src
						}
					} else {
						rowText = src
					}
				}
				plainRows = append(plainRows, rowText)
				lineNumsAny = append(lineNumsAny, ln)
				if shouldAddHashLines {
					formattedRows = append(formattedRows, hashline.FormatNumberedLine(ln, rowText))
				} else {
					formattedRows = append(formattedRows, rowText)
				}
				if !cutSet[ln] {
					seenLines = append(seenLines, ln)
				}
				prev = ln
			}
			body := strings.Join(formattedRows, "\n")
			if shouldAddHashLines {
				out = header + "\n" + body
			} else {
				out = body
			}
			dispContent = map[string]any{
				"text":        strings.Join(plainRows, "\n"),
				"startLine":   allNums[0],
				"lineNumbers": lineNumsAny,
			}
		} else {
			plainJoined := strings.Join(displayLines, "\n")
			dispContent = map[string]any{
				"text":        plainJoined,
				"startLine":   startLineDisplay,
				"lineNumbers": contiguousLineNumAny(startLineDisplay, len(displayLines)),
			}
			if shouldAddHashLines {
				body := hashline.FormatNumberedLines(plainJoined, startLineDisplay)
				out = header + "\n" + body
			} else {
				out = plainJoined
			}
			for i := range displayLines {
				ln := startLineDisplay + i
				if !cutSet[ln] {
					seenLines = append(seenLines, ln)
				}
			}
		}

		if shouldAddHashLines {
			if len(cutSet) == 0 && isWholeFile {
				store.RecordReadWholeFile(absPath, normalized)
			} else {
				store.RecordReadLines(absPath, normalized, seenLines)
			}
		}
	}

	details := map[string]any{
		"fileSize":    fileSize,
		"totalLines":  totalLines,
		"rowPrefixes": shouldAddHashLines,
	}
	if rawMode {
		details["raw"] = true
	}
	if dispContent != nil {
		details["displayContent"] = dispContent
	}
	if loc != nil {
		details["resolvedPath"] = absPath
		setMetaSource(details, "internal", loc.url)
	} else {
		setMetaSource(details, "path", absPath)
	}
	// Immutable sources have no edit path, so their markers are content, not conflicts.
	// upstream: packages/coding-agent/src/tools/read.ts:3378-3419
	if !immutable && len(collected) > 0 {
		if warning, n := registerAndFormatReadConflicts(ctx, cwd, absPath, collected, startLineDisplay, cutSet); n > 0 {
			if rawMode {
				appendDetailsNote(details, warning)
			} else {
				out += warning
			}
			details["conflictCount"] = n
		}
	}
	// Raw mode reports truncation whenever an open-ended read or a finite range was cut short
	// by line or byte limits; a satisfied closed raw range (`:raw:2-4`) emits no paging footer.
	// upstream: packages/coding-agent/src/tools/read.ts:3126-3130, test/tools/read-raw-range.test.ts:207-219
	selectedWindowLines := totalLines - startIdx
	if rawMode && hasLimit {
		selectedWindowLines = min(limit, selectedWindowLines)
	}
	rawTruncated := rawMode && len(collected) < selectedWindowLines
	if (!rawMode && wasTruncated) || rawTruncated {
		nextOffset := endIdx + 1
		truncBy := "lines"
		if stoppedByByteLimit {
			truncBy = "bytes"
			out += fmt.Sprintf("\n\n[Showing lines %d-%d of %d (%s limit). Use %s:%d- to continue]",
				startLineDisplay, endIdx, totalLines, formatBytes(int64(maxBytesForRead)), selectorBase, nextOffset)
		} else {
			out += fmt.Sprintf("\n\n[Showing lines %d-%d of %d. Use %s:%d- to continue]",
				startLineDisplay, endIdx, totalLines, selectorBase, nextOffset)
		}
		// upstream: packages/coding-agent/src/tools/read.ts:3322-3331, output-meta.ts:181-197
		// omp always passes maxBytes: maxBytesForRead into resultBuilder.truncation on truncated
		// non-partial reads (both truncatedBy:"lines" and truncatedBy:"bytes").
		truncMeta := map[string]any{
			"direction":   "head",
			"truncatedBy": truncBy,
			"totalLines":  totalLines,
			"totalBytes":  selectedBytes,
			"outputLines": len(collected),
			"outputBytes": collectedBytes,
			"maxBytes":    maxBytesForRead,
			"shownRange":  map[string]any{"start": startLineDisplay, "end": endIdx},
			"nextOffset":  nextOffset,
		}
		setMetaTruncation(details, truncMeta)
	}
	if colCut != nil {
		out += formatColumnCutNotice(colCut, selectorBase)
		setMetaColumnTruncated(details, colCut, selectorBase, DefaultMaxBytes)
	}
	return sdk.ToolResult{Content: out, Details: details}, nil
}

func computeReadByteBudget(startIdx, maxLines int, hasPrefixes, unspilledPages bool) int {
	scaled := max(DefaultMaxBytes, maxLines*512)
	if unspilledPages {
		return scaled
	}
	threshold := DefaultSpillThreshold
	prefixWidth := 0
	if hasPrefixes {
		prefixWidth = len(strconv.Itoa(startIdx+maxLines)) + 1
	}
	prefixReserve := min(maxLines*prefixWidth, threshold/2)
	return min(scaled, threshold-prefixReserve)
}

// readLargeFileStreaming streams a line window from a file larger than SnapshotMaxBytes
// without reading the whole file into memory.
// Supports `:-N` tail selectors via resolveLargeFileTail, computes a streaming `[path#TAG]`
// header for files up to HardSnapshotCapBytes (64 MiB), and caps per-line buffering so a
// single multi-megabyte line is never loaded whole.
// upstream: packages/coding-agent/src/tools/read.ts:552-735,777-830,3003
func readLargeFileStreaming(
	ctx sdk.Context,
	store *hashline.Store,
	cwd, absPath, displayPath, selectorBase string,
	fileSize int64,
	parsed ParsedSelector,
	immutable, unspilledPages bool,
) (sdk.ToolResult, error) {
	if parsed.IsMultiRange() {
		return sdk.ToolResult{}, fmt.Errorf("Multi-range selectors are not supported on files larger than %s.", formatBytes(SnapshotMaxBytes))
	}

	rawMode := parsed.IsRaw()
	shouldAddHashLines := !rawMode && !immutable && fileSize <= HardSnapshotCapBytes

	var tailPlan *largeFileTailPlan
	if parsed.Kind == SelectorTail {
		plan, err := resolveLargeFileTail(absPath, parsed.Tail, !rawMode)
		if err != nil {
			return sdk.ToolResult{}, err
		}
		tailPlan = &plan
		parsed = parsed.ResolveTail(plan.totalLines)
	}

	offset := 1
	limit := readDefaultLimit
	hasLimit := false
	if parsed.Kind == SelectorLines && len(parsed.Ranges) == 1 {
		r := parsed.Ranges[0]
		offset = r.StartLine
		if r.EndLine > 0 {
			limit = r.EndLine - r.StartLine + 1
			hasLimit = true
		}
	}
	requestedStart := max(0, offset-1)
	leadingCtx := 0
	if !rawMode && offset > 1 {
		leadingCtx = min(requestedStart, RangeLeadingContextLines)
	}
	trailingCtx := 0
	if !rawMode && hasLimit {
		trailingCtx = RangeTrailingContextLines
	}
	startIdx := requestedStart - leadingCtx
	maxLines := min(limit+leadingCtx+trailingCtx, DefaultMaxLines)
	maxBytes := computeReadByteBudget(startIdx, maxLines, shouldAddHashLines, unspilledPages)
	maxCol := 0
	if !rawMode {
		maxCol = readMaxColumns
	}

	f, err := os.Open(absPath)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	defer f.Close()

	lineIdx := 0
	if tailPlan != nil && tailPlan.hasSeek && tailPlan.seekLine == startIdx {
		if _, err := f.Seek(tailPlan.seekByte, io.SeekStart); err == nil {
			lineIdx = tailPlan.seekLine
		}
	}

	// Stream line-by-line using ReadSlice so a single multi-megabyte line is bounded
	// to captureLimit bytes rather than allocated whole.
	// upstream: packages/coding-agent/src/tools/read.ts:552-602
	reader := bufio.NewReaderSize(f, 64*1024)
	var collected []string
	var collectedFullLen []int // true byte length of each collected line (for column-cut notice)
	collectedBytes := 0
	reachedEOF := false
	stoppedByByteLimit := false
	var firstLinePreview []byte
	firstLineByteLen := 0
	sawTrailingNL := false

	for {
		// Decide per-line capture cap.
		inWindow := lineIdx >= startIdx
		captureLimit := 0
		if inWindow {
			sep := 0
			if len(collected) > 0 {
				sep = 1
			}
			rem := maxBytes - collectedBytes - sep
			if rem > 0 {
				if maxCol > 0 {
					// 4 bytes per rune + slack guarantees truncation to maxCol runes matches the full line.
					captureLimit = 4*maxCol + 8
				} else {
					captureLimit = rem
				}
			}
		}

		var lineBuf []byte
		lineTotalLen := 0
		lineEndedWithNL := false
		hitEOF := false
		for {
			frag, rerr := reader.ReadSlice('\n')
			if len(frag) > 0 {
				hasNL := frag[len(frag)-1] == '\n'
				seg := frag
				if hasNL {
					seg = frag[:len(frag)-1]
					lineEndedWithNL = true
				}
				if inWindow && len(collected) == 0 && len(firstLinePreview) < maxBytes {
					need := maxBytes - len(firstLinePreview)
					take := min(need, len(seg))
					firstLinePreview = append(firstLinePreview, seg[:take]...)
				}
				if inWindow && captureLimit > 0 && len(lineBuf) < captureLimit {
					need := captureLimit - len(lineBuf)
					take := min(need, len(seg))
					lineBuf = append(lineBuf, seg[:take]...)
				}
				lineTotalLen += len(seg)
			}
			if rerr == bufio.ErrBufferFull {
				continue
			}
			if rerr == io.EOF {
				hitEOF = true
				break
			}
			if rerr != nil {
				return sdk.ToolResult{}, rerr
			}
			break
		}

		if lineTotalLen == 0 && !lineEndedWithNL && hitEOF {
			reachedEOF = true
			break
		}
		sawTrailingNL = lineEndedWithNL

		if inWindow {
			// Strip trailing '\r' from CRLF (only if full line fit in buffer or if lineBuf ends with '\r').
			if lineEndedWithNL && lineTotalLen == len(lineBuf) && len(lineBuf) > 0 && lineBuf[len(lineBuf)-1] == '\r' {
				lineBuf = lineBuf[:len(lineBuf)-1]
				lineTotalLen--
			}
			if lineIdx == 0 && bytes.HasPrefix(lineBuf, []byte{0xef, 0xbb, 0xbf}) {
				lineBuf = lineBuf[3:]
				lineTotalLen -= 3
			}
			text := string(lineBuf)
			if lineTotalLen > len(lineBuf) && maxCol == 0 {
				// Raw/uncapped line exceeded captureLimit (= remaining byte budget).
				if len(collected) == 0 {
					firstLineByteLen = lineTotalLen
				}
				stoppedByByteLimit = true
				break
			}
			lb := lineTotalLen
			if maxCol > 0 && lb > maxCol {
				if t, cut := truncateLineChars(text, maxCol); cut {
					lb = len(t)
				}
			}
			sep := 0
			if len(collected) > 0 {
				sep = 1
			}
			if len(collected) > 0 && collectedBytes+sep+lb > maxBytes {
				stoppedByByteLimit = true
				break
			}
			if len(collected) == 0 && lb > maxBytes {
				firstLineByteLen = lineTotalLen
				stoppedByByteLimit = true
				break
			}
			collected = append(collected, text)
			collectedFullLen = append(collectedFullLen, lineTotalLen)
			collectedBytes += sep + lb
			if len(collected) >= maxLines {
				if hitEOF {
					reachedEOF = true
				}
				break
			}
		}
		lineIdx++
		if hitEOF {
			reachedEOF = true
			break
		}
	}

	if requestedStart >= lineIdx+len(collected) && len(collected) == 0 && reachedEOF {
		details := map[string]any{
			"resolvedPath": dirs.StandardizeMacOSPath(absPath),
		}
		if rawMode {
			details["raw"] = true
		}
		return sdk.ToolResult{
			Content: fmt.Sprintf("Line %d is beyond end of file (%d lines total). Use %s:1- to read from the start, or %s:%d to read the last line.",
				requestedStart+1, lineIdx, selectorBase, selectorBase, max(1, lineIdx)),
			Details: details,
		}, nil
	}

	startLineDisplay := startIdx + 1
	if len(collected) == 0 && stoppedByByteLimit {
		details := map[string]any{"fileSize": fileSize}
		setMetaSource(details, "path", absPath)
		if shouldAddHashLines {
			return sdk.ToolResult{
				Content: fmt.Sprintf("[Line %d is %s, exceeds %s limit. Hashline output requires full lines; cannot emit an editable numbered preview for a truncated line.]\n\n[File not scanned to EOF]",
					startLineDisplay, formatBytes(int64(firstLineByteLen)), formatBytes(int64(maxBytes))),
				Details: details,
			}, nil
		}
		snippet := truncateUTF8Bytes(string(firstLinePreview), maxBytes)
		if rawMode {
			details["raw"] = true
		}
		truncMeta := map[string]any{
			"direction":   "head",
			"truncatedBy": "bytes",
			"partialLine": true,
			"totalBytes":  firstLineByteLen,
			"outputLines": 1,
			"outputBytes": len(snippet),
			"shownRange":  map[string]any{"start": startLineDisplay, "end": startLineDisplay},
		}
		setMetaTruncation(details, truncMeta)
		notice := fmt.Sprintf("Showing line %d (partial, %s of %s); file not scanned to EOF",
			startLineDisplay, formatBytes(int64(len(snippet))), formatBytes(int64(firstLineByteLen)))
		return sdk.ToolResult{Content: snippet + "\n\n[" + notice + "]", Details: details}, nil
	}

	// Compute streaming `[path#TAG]` header for files up to HardSnapshotCapBytes (64 MiB).
	// upstream: crates/edit/src/store.rs:377-390
	var header string
	if shouldAddHashLines {
		if tag, herr := streamFileHash(absPath, fileSize); herr == nil && tag != "" {
			header = hashline.FormatHeader(displayPath, tag)
		}
	}

	displayLines := collected
	var colCut *columnCutInfo
	var cutSet map[int]bool
	if !rawMode && maxCol > 0 {
		cloned := make([]string, len(collected))
		for i, l := range collected {
			if t, cut := truncateLineChars(l, maxCol); cut {
				cloned[i] = t
				colCut = addColumnCut(colCut, maxCol, startLineDisplay+i, collectedFullLen[i])
				if cutSet == nil {
					cutSet = map[int]bool{}
				}
				cutSet[startLineDisplay+i] = true
			} else {
				cloned[i] = l
			}
		}
		displayLines = cloned
	}

	plainJoined := strings.Join(displayLines, "\n")
	var out string
	if rawMode {
		out = strings.Join(collected, "\n")
		if len(collected) > 0 && (!reachedEOF || sawTrailingNL) {
			out += "\n"
		}
	} else {
		body := hashline.FormatNumberedLines(plainJoined, startLineDisplay)
		if header != "" {
			out = header + "\n" + body
		} else {
			out = body
		}
	}

	rawNeedsFooter := rawMode && !reachedEOF && (!hasLimit || len(collected) < limit)
	if (!rawMode && !reachedEOF) || rawNeedsFooter {
		nextOffset := startIdx + len(collected) + 1
		out += fmt.Sprintf("\n\n[More lines in file (%s total; not scanned to EOF). Use %s:%d- to continue]",
			formatBytes(fileSize), selectorBase, nextOffset)
	}
	if colCut != nil {
		out += formatColumnCutNotice(colCut, selectorBase)
	}

	dispText := plainJoined
	dispCount := len(displayLines)
	if rawMode {
		dispText = out
		dispCount = countTextLines(out)
	}
	details := map[string]any{
		"fileSize":    fileSize,
		"rowPrefixes": !rawMode,
		"displayContent": map[string]any{
			"text":        dispText,
			"startLine":   startLineDisplay,
			"lineNumbers": contiguousLineNumAny(startLineDisplay, dispCount),
		},
	}
	if tailPlan != nil {
		details["totalLines"] = tailPlan.totalLines
	}
	if rawMode {
		details["raw"] = true
	}
	setMetaSource(details, "path", absPath)
	if colCut != nil {
		setMetaColumnTruncated(details, colCut, selectorBase, DefaultMaxBytes)
	}
	if !immutable && len(collected) > 0 {
		if warning, n := registerAndFormatReadConflicts(ctx, cwd, absPath, collected, startLineDisplay, cutSet); n > 0 {
			if rawMode {
				appendDetailsNote(details, warning)
			} else {
				out += warning
			}
			details["conflictCount"] = n
		}
	}
	if (!rawMode && !reachedEOF) || rawNeedsFooter {
		truncBy := "lines"
		if stoppedByByteLimit {
			truncBy = "bytes"
		}
		truncMeta := map[string]any{
			"direction":   "head",
			"truncatedBy": truncBy,
			"totalBytes":  fileSize,
			"outputLines": len(collected),
			"outputBytes": collectedBytes,
			"shownRange":  map[string]any{"start": startLineDisplay, "end": startIdx + len(collected)},
			"nextOffset":  startIdx + len(collected) + 1,
		}
		if tailPlan != nil {
			truncMeta["totalLines"] = tailPlan.totalLines
		}
		setMetaTruncation(details, truncMeta)
	}
	return sdk.ToolResult{Content: out, Details: details}, nil
}
func renderMultiRange(
	allLines []string,
	sel ParsedSelector,
	displayPath, header string,
	hasTrailingNL bool,
	addLineNumbers bool,
	maxCol int,
) (string, []int, map[string]any, *columnCutInfo) {
	return renderMultiRangeWithOffset(allLines, sel, displayPath, header, hasTrailingNL, addLineNumbers, maxCol, 0)
}

func renderMultiRangeWithOffset(
	allLines []string,
	sel ParsedSelector,
	displayPath, header string,
	hasTrailingNL bool,
	addLineNumbers bool,
	maxCol int,
	sourceOffset int,
) (string, []int, map[string]any, *columnCutInfo) {
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
		}
		for ln := rangeStart + 1; ln <= rangeEnd; ln++ {
			visible[ln] = true
		}
	}

	var out string
	var displayed []int
	var dispContent map[string]any
	var colCut *columnCutInfo

	if rawMode {
		out = strings.Join(blocks, "\n")
		for ln := range visible {
			displayed = append(displayed, ln+sourceOffset)
		}
		slices.Sort(displayed)
	} else if len(visible) > 0 {
		ctxLines := hashline.FindBlockContextLines(allLines, visible)
		allNums := make([]int, 0, len(visible)+len(ctxLines))
		for ln := range visible {
			allNums = append(allNums, ln)
		}
		for ln := range ctxLines {
			if ln >= 1 && ln <= totalLines && !visible[ln] {
				allNums = append(allNums, ln)
			}
		}
		slices.Sort(allNums)

		var formattedRows []string
		var plainRows []string
		var lineNumsAny []any
		prev := 0
		for _, ln := range allNums {
			if prev > 0 && ln > prev+1 {
				formattedRows = append(formattedRows, "…")
				plainRows = append(plainRows, "…")
				lineNumsAny = append(lineNumsAny, nil)
			}
			src := allLines[ln-1]
			rowText := src
			wasCut := false
			if maxCol > 0 {
				if trunc, cut := truncateLineChars(src, maxCol); cut {
					rowText = trunc
					wasCut = true
					colCut = addColumnCut(colCut, maxCol, ln, len(src))
				}
			}
			plainRows = append(plainRows, rowText)
			displayLn := ln + sourceOffset
			lineNumsAny = append(lineNumsAny, displayLn)
			if addLineNumbers {
				formattedRows = append(formattedRows, hashline.FormatNumberedLine(displayLn, rowText))
			} else {
				formattedRows = append(formattedRows, rowText)
			}
			if !wasCut {
				displayed = append(displayed, displayLn)
			}
			prev = ln
		}
		body := strings.Join(formattedRows, "\n")
		if header != "" {
			out = header + "\n" + body
		} else {
			out = body
		}
		dispContent = map[string]any{
			"text":        strings.Join(plainRows, "\n"),
			"startLine":   allNums[0] + sourceOffset,
			"lineNumbers": lineNumsAny,
		}
	} else {
		out = header
	}
	if len(notices) > 0 {
		if out != "" {
			out += "\n\n"
		}
		out += strings.Join(notices, "\n")
	}
	return out, displayed, dispContent, colCut
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

func appendDetailsNote(details map[string]any, note string) {
	if note == "" {
		return
	}
	switch prev := details["notes"].(type) {
	case []string:
		details["notes"] = append(prev, note)
	default:
		details["notes"] = []string{note}
	}
}
