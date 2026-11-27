package snapcompact

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// upstream: packages/snapcompact/src/snapcompact.ts (serialization, normalization, stopwords, pagination)

const (
	// ToolResultMaxChars is the default per-tool-result character cap.
	ToolResultMaxChars = 2000
	// ToolArgMaxChars is the default per-argument-value cap.
	ToolArgMaxChars = 500
	// ToolCallMaxChars is the default whole-argument-list cap per call.
	ToolCallMaxChars = 2000
	// TruncateHeadRatio is the default head share kept when eliding the middle.
	TruncateHeadRatio = 0.6

	textEdgePages           = 1
	fileOpSummaryLimit      = 20
	damagedPayloadMinChars  = 40
	previousSummaryHead     = "[Summary of earlier history]"
	filesSectionHead        = "FILES\n===================\n"
	historySectionHead      = "HISTORY\n==================="
	thinkLabel              = "¶think:"
	intentField             = "i"
	unrenderableMaxFraction = 0.05
)

// SerializeOptions configures conversation serialization.
type SerializeOptions struct {
	ToolResultMaxChars int
	ToolArgMaxChars    int
	ToolCallMaxChars   int
	TruncateHeadRatio  float64
	DimToolResults     *bool
	IncludeThinking    *bool
}

func (o SerializeOptions) toolResultMax() int {
	if o.ToolResultMaxChars > 0 {
		return o.ToolResultMaxChars
	}
	return ToolResultMaxChars
}

func (o SerializeOptions) toolArgMax() int {
	if o.ToolArgMaxChars > 0 {
		return o.ToolArgMaxChars
	}
	return ToolArgMaxChars
}

func (o SerializeOptions) toolCallMax() int {
	if o.ToolCallMaxChars > 0 {
		return o.ToolCallMaxChars
	}
	return ToolCallMaxChars
}

func (o SerializeOptions) headRatio() float64 {
	if o.TruncateHeadRatio > 0 || o.TruncateHeadRatio < 0 {
		return o.TruncateHeadRatio
	}
	return TruncateHeadRatio
}

func (o SerializeOptions) dimResults() bool {
	if o.DimToolResults != nil {
		return *o.DimToolResults
	}
	return true
}

func (o SerializeOptions) includeThinking() bool {
	if o.IncludeThinking != nil {
		return *o.IncludeThinking
	}
	return true
}

// truncateForSummary keeps the head and tail of text, eliding the middle beyond maxChars.
// upstream: packages/snapcompact/src/snapcompact.ts truncateForSummary
func truncateForSummary(text string, maxChars int, headRatio float64) string {
	runes := []rune(text)
	if len(runes) <= maxChars || maxChars <= 0 {
		return text
	}
	ratio := math.Min(math.Max(headRatio, 0), 1)
	headChars := int(math.Round(float64(maxChars) * ratio))
	tailChars := maxChars - headChars
	elided := len(runes) - maxChars
	tail := ""
	if tailChars > 0 {
		tail = string(runes[len(runes)-tailChars:])
	}
	return fmt.Sprintf("%s […%dch elided…] %s", string(runes[:headChars]), elided, tail)
}

var (
	reElidedMarker    = regexp.MustCompile(`\s*\[(?:…|\.{3})(\d+)ch elided(?:…|\.{3})\]\s*`)
	reDataURLAtom     = regexp.MustCompile(`(?i)data:([A-Za-z][\w.+-]*/[\w.+-]+(?:;[\w!#$%&'*+.^|~-]+=[\w!#$%&'*+.^|~-]+)*);base64,([A-Za-z0-9+/=]*(?:\s*\[(?:…|\.{3})\d+ch elided(?:…|\.{3})\]\s*[A-Za-z0-9+/=]*)?)(\s*\))?`)
	reCanonicalBase64 = regexp.MustCompile(`^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{4}|[A-Za-z0-9+/]{3}=|[A-Za-z0-9+/]{2}==)$`)
	reANSI            = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)|[@-Z\\-_])`)
	reMultiSpace      = regexp.MustCompile(` +`)
	reAlphaRun        = regexp.MustCompile(`[a-zA-Z\x{00c0}-\x{00d6}\x{00d8}-\x{00f6}\x{00f8}-\x{00ff}]+`)
	reURLScheme       = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://`)
	reLegendReason    = regexp.MustCompile("`¶user:`, `¶think:`, (`¶ai:`, (?:and )?`¶call:`:? (?:open )?user, )assistant reasoning, ")
)

func adjacentMarkdownOpenerStart(text string, dataIndex, cursor int) (int, bool) {
	i := dataIndex
	for i > cursor && (text[i-1] == ' ' || text[i-1] == '\t' || text[i-1] == '\n' || text[i-1] == '\r') {
		i--
	}
	if i-2 < cursor || text[i-1] != '(' || text[i-2] != ']' {
		return 0, false
	}
	opener := -1
	for j := i - 3; j >= cursor; j-- {
		c := text[j]
		if c == ']' || c == '\n' {
			break
		}
		if c == '[' {
			opener = j
		}
	}
	if opener < 0 {
		return 0, false
	}
	if opener > cursor && text[opener-1] == '!' {
		return opener - 1, true
	}
	return opener, true
}

// ElideDataUrls replaces inline base64 data URLs with deterministic metadata placeholders.
// archiveContext=true treats any matching prefix as damaged and always elides it.
// upstream: packages/snapcompact/src/snapcompact.ts elideDataUrls
func ElideDataUrls(text string, archiveContext bool) string {
	if !strings.Contains(strings.ToLower(text), ";base64,") {
		return text
	}
	matches := reDataURLAtom.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text
	}
	var out strings.Builder
	out.Grow(len(text))
	cursor := 0
	for _, m := range matches {
		urlStart, urlEnd := m[0], m[1]
		if urlStart < cursor {
			continue
		}
		mime := text[m[2]:m[3]]
		payload := text[m[4]:m[5]]
		closer := ""
		hasCloser := m[6] >= 0 && m[7] >= 0
		if hasCloser {
			closer = text[m[6]:m[7]]
		}
		marker := reElidedMarker.FindStringSubmatchIndex(payload)
		isAtom := archiveContext ||
			marker != nil ||
			reCanonicalBase64.MatchString(payload) ||
			len(payload) >= damagedPayloadMinChars
		if !isAtom {
			out.WriteString(text[cursor:urlEnd])
			cursor = urlEnd
			continue
		}
		b64Chars := len(payload)
		if marker != nil {
			elidedN, _ := strconv.Atoi(payload[marker[2]:marker[3]])
			b64Chars = len(payload) - (marker[1] - marker[0]) + elidedN
		}
		placeholder := fmt.Sprintf("[data URL omitted: %s, %d base64 chars]", mime, b64Chars)
		openerStart, hasOpener := adjacentMarkdownOpenerStart(text, urlStart, cursor)
		if hasOpener && openerStart < cursor {
			hasOpener = false
		}
		emitStart := urlStart
		if hasOpener {
			emitStart = openerStart
		}
		out.WriteString(text[cursor:emitStart])
		if hasOpener && hasCloser {
			out.WriteString(placeholder)
		} else {
			if hasOpener {
				out.WriteString(text[openerStart:urlStart])
			}
			out.WriteString(placeholder)
			out.WriteString(closer)
		}
		cursor = urlEnd
	}
	out.WriteString(text[cursor:])
	return out.String()
}

func stripDimMarkers(s string) string {
	if !strings.ContainsAny(s, DimOn+DimOff) {
		return s
	}
	return strings.NewReplacer(DimOn, "", DimOff, "").Replace(s)
}

// ToPlainText converts normalized archive text to plain text (drops dim toggles,
// restores newline glyphs to real newlines).
func ToPlainText(s string) string {
	return strings.ReplaceAll(stripDimMarkers(s), NewlineGlyph, "\n")
}

// SerializeConversation converts PiG/Agent JSON messages (map[string]any) to omp's
// scoped archive transcript (`¶user:`, `¶think:`, `¶ai:`, `¶call:`, `<out>…</out>`).
// upstream: packages/snapcompact/src/snapcompact.ts serializeConversation
func SerializeConversation(messages []map[string]any, opts SerializeOptions) string {
	toolResultMax := opts.toolResultMax()
	toolArgMax := opts.toolArgMax()
	toolCallMax := opts.toolCallMax()
	headRatio := opts.headRatio()
	dimResults := opts.dimResults()
	includeThink := opts.includeThinking()

	var parts []string
	lastPrefix := ""
	pushPart := func(prefix, content string) {
		if len(parts) > 0 && lastPrefix == prefix {
			last := parts[len(parts)-1]
			sep := "\n"
			if strings.HasSuffix(last, "\n") || strings.HasPrefix(content, "\n") {
				sep = ""
			}
			parts[len(parts)-1] = last + sep + content
			return
		}
		parts = append(parts, prefix+content)
		lastPrefix = prefix
	}

	uselessCallIDs := map[string]bool{}
	resultTextByCallID := map[string]string{}
	for _, msg := range messages {
		if strField(msg, "role") != "toolResult" {
			continue
		}
		callID := strField(msg, "toolCallId")
		if boolField(msg, "useless") && !boolField(msg, "isError") {
			if callID != "" {
				uselessCallIDs[callID] = true
			}
			continue
		}
		if txt := extractMessageText(msg["content"]); txt != "" && callID != "" {
			resultTextByCallID[callID] = txt
		}
	}

	renderResultBlock := func(rawText string) string {
		body := truncateForSummary(ElideDataUrls(stripDimMarkers(rawText), false), toolResultMax, headRatio)
		if dimResults {
			return "<out>\n" + DimOn + body + DimOff + "\n</out>"
		}
		return "<out>\n" + body + "\n</out>"
	}

	mergedCallIDs := map[string]bool{}

	for _, msg := range messages {
		role := strField(msg, "role")
		switch role {
		case "user":
			if txt := extractMessageText(msg["content"]); txt != "" {
				pushPart("¶user:", stripDimMarkers(txt))
			}
		case "custom", "hookMessage":
			if boolField(msg, "excludeFromContext") {
				continue
			}
			if txt := extractMessageText(msg["content"]); txt != "" {
				pushPart("¶user:", stripDimMarkers(txt))
			}
		case "bashExecution":
			if boolField(msg, "excludeFromContext") {
				continue
			}
			cmd := strField(msg, "command")
			out := strField(msg, "output")
			body := "Ran `" + cmd + "`\n"
			if out != "" {
				body += "```\n" + out + "\n```"
			} else {
				body += "(no output)"
			}
			pushPart("¶user:", stripDimMarkers(body))
		case "branchSummary":
			if summary := strField(msg, "summary"); summary != "" {
				pushPart("¶user:", stripDimMarkers(summary))
			}
		case "assistant":
			var pendingThinking, pendingText []string
			flushAssistant := func() {
				if len(pendingThinking) > 0 {
					pushPart("¶think:", strings.Join(pendingThinking, "\n"))
				}
				if len(pendingText) > 0 {
					pushPart("¶ai:", strings.Join(pendingText, "\n"))
				}
				pendingThinking = pendingThinking[:0]
				pendingText = pendingText[:0]
			}
			blocks, _ := msg["content"].([]any)
			for _, rawBlock := range blocks {
				block, ok := rawBlock.(map[string]any)
				if !ok {
					continue
				}
				switch strField(block, "type") {
				case "text":
					txt := stripDimMarkers(strField(block, "text"))
					if strings.TrimSpace(txt) != "" {
						pendingText = append(pendingText, txt)
					}
				case "thinking":
					if !includeThink {
						continue
					}
					th := stripDimMarkers(strField(block, "thinking"))
					if strings.TrimSpace(th) != "" {
						pendingThinking = append(pendingThinking, th)
					}
				case "toolCall":
					callID := strField(block, "id")
					if uselessCallIDs[callID] {
						continue
					}
					flushAssistant()
					args, _ := block["arguments"].(map[string]any)
					rawIntent := strField(block, "intent")
					if rawIntent == "" && args != nil {
						rawIntent = strField(args, intentField)
					}
					intent := strings.Join(strings.Fields(stripDimMarkers(rawIntent)), " ")
					var argKeys []string
					for k := range args {
						if k != intentField {
							argKeys = append(argKeys, k)
						}
					}
					sort.Strings(argKeys)
					argParts := make([]string, 0, len(argKeys))
					for _, k := range argKeys {
						encoded := "undefined"
						if b, err := json.Marshal(args[k]); err == nil {
							encoded = string(b)
						}
						argParts = append(argParts, k+"="+truncateForSummary(ElideDataUrls(encoded, false), toolArgMax, headRatio))
					}
					argsStr := truncateForSummary(strings.Join(argParts, ", "), toolCallMax, headRatio)
					firstLine := fmt.Sprintf("%s(%s)", strField(block, "name"), argsStr)
					if intent != "" {
						firstLine += "//" + intent
					}
					lines := []string{firstLine}
					if resText, ok := resultTextByCallID[callID]; ok {
						mergedCallIDs[callID] = true
						lines = append(lines, renderResultBlock(resText))
					}
					pushPart("¶call:", strings.Join(lines, "\n"))
				}
			}
			flushAssistant()
		case "toolResult":
			callID := strField(msg, "toolCallId")
			if uselessCallIDs[callID] || mergedCallIDs[callID] {
				continue
			}
			if resText, ok := resultTextByCallID[callID]; ok {
				pushPart("¶call:", "\n"+renderResultBlock(resText))
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func strField(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func boolField(m map[string]any, key string) bool {
	v, _ := m[key].(bool)
	return v
}

func extractMessageText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var b strings.Builder
		for _, item := range v {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if strField(m, "type") == "text" {
				b.WriteString(strField(m, "text"))
			}
		}
		return b.String()
	default:
		return ""
	}
}

// Text normalization tables (packages/snapcompact/src/snapcompact.ts CHAR_FOLD / EMOJI_FOLD / STOPWORDS).

var charFold = map[rune]string{
	'\u2018': "'", '\u2019': "'", '\u201a': "'", '\u201b': "'",
	'\u201c': `"`, '\u201d': `"`, '\u201e': `"`,
	'\u2032': "'", '\u2033': `"`, '\u2035': "'", '\u2036': `"`,
	'\u2039': "<", '\u203a': ">",
	'\u2010': "-", '\u2011': "-", '\u2012': "-", '\u2013': "-", '\u2014': "-", '\u2015': "-", '\u2212': "-",
	'\u2044': "/",
	'\u2024': ".", '\u2025': "..", '\u2026': "...", '\u22ef': "...",
	'\u2022': "*", '\u2023': "*", '\u2043': "-", '\u2219': "*", '\u25cf': "*", '\u25a0': "*", '\u25aa': "*",
	'\u2190': "<-", '\u2191': "^", '\u2192': "->", '\u2193': "v", '\u2194': "<->",
	'\u21d0': "<=", '\u21d2': "=>", '\u21d4': "<=>",
	'\u2713': "v", '\u2714': "v", '\u2717': "x", '\u2718': "x",
}

var emojiFold = map[rune]string{
	'✅': "[OK]", '☑': "[OK]", '✔': "[OK]",
	'❌': "[FAIL]", '❎': "[FAIL]", '✖': "[FAIL]",
	'⚠': "[WARN]", '🚨': "[ALERT]", 'ℹ': "[INFO]",
	'🐛': "[BUG]", '💥': "[CRASH]", '🔥': "[HOT]",
	'🔒': "[LOCK]", '🔓': "[UNLOCK]",
	'📁': "[DIR]", '📂': "[DIR]", '📄': "[FILE]", '📝': "[NOTE]",
	'🧪': "[TEST]", '⏳': "[WAIT]", '⌛': "[WAIT]", '🚀': "[RUN]",
}

var stopwords = func() map[string]bool {
	words := strings.Fields(
		"the a an and or of to in on at as is are was were be been by for with that this it its from had has have not but " +
			"he she his her they their them which also who whom when where while will would could should there then than " +
			"into over under about after before between during each such these those some most more other only same so",
	)
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}()

func isASCIIOrLatin1(r rune) bool {
	return (r >= 0x20 && r < 0x7f) || (r >= 0xa0 && r <= 0xff)
}

func isLineBreak(r rune) bool {
	return r == '\n' || r == '\r' || r == '\u2028' || r == '\u2029'
}

func isCollapsible(r rune) bool {
	return unicode.IsSpace(r) || unicode.Is(unicode.Cf, r)
}

func isUnrenderableControl(r rune) bool {
	return unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r)
}

func isPictograph(r rune) bool {
	return (r >= 0x1F300 && r <= 0x1FAFF) || (r >= 0x2600 && r <= 0x27BF)
}

func stripANSI(s string) string {
	if !strings.ContainsRune(s, '\x1b') {
		return s
	}
	return reANSI.ReplaceAllString(s, "")
}

func collapseWhitespace(s string) []rune {
	stripped := stripANSI(s)
	var out []rune
	runes := []rune(stripped)
	for i := 0; i < len(runes); {
		r := runes[i]
		if !isCollapsible(r) {
			out = append(out, r)
			i++
			continue
		}
		hasBreak := false
		hasSpace := false
		for i < len(runes) && isCollapsible(runes[i]) {
			if isLineBreak(runes[i]) {
				hasBreak = true
			}
			if !unicode.Is(unicode.Cf, runes[i]) {
				hasSpace = true
			}
			i++
		}
		if hasBreak {
			out = append(out, fullBlockRune)
		} else if hasSpace {
			out = append(out, ' ')
		}
	}
	return trimEdgeRuns(out)
}

func trimEdgeRuns(in []rune) []rune {
	start := 0
	for start < len(in) && (in[start] == ' ' || in[start] == fullBlockRune) {
		start++
	}
	end := len(in)
	for end > start && (in[end-1] == ' ' || in[end-1] == fullBlockRune) {
		end--
	}
	return in[start:end]
}

// RenderabilityScan reports whether a font can render text within the 5% fallback ceiling.
type RenderabilityScan struct {
	IsSafe            bool
	UnrenderableRatio float64
}

type normalizedStats struct {
	text          string
	totalGraphics int
	fallbackCount int
}

func normalizeWithStats(text string, fontName string) normalizedStats {
	runes := collapseWhitespace(text)
	if fontName == "" {
		fontName = "8x13"
	}
	font := ResolveFont(fontName)
	var out strings.Builder
	out.Grow(len(text))
	totalGraphics := 0
	fallbackCount := 0

	for _, r := range runes {
		if isASCIIOrLatin1(r) {
			out.WriteRune(r)
			totalGraphics++
			continue
		}
		if r == dimOnRune || r == dimOffRune || r == fullBlockRune {
			out.WriteRune(r)
			continue
		}
		if folded, ok := emojiFold[r]; ok {
			out.WriteString(folded)
			totalGraphics++
			continue
		}
		if folded, ok := charFold[r]; ok {
			out.WriteString(folded)
			totalGraphics++
			continue
		}
		if r >= 0x2500 && r <= 0x257f {
			if r == 0x2502 || r == 0x2503 {
				out.WriteByte('|')
			} else if r == 0x2500 || r == 0x2501 {
				out.WriteByte('-')
			} else {
				out.WriteByte('+')
			}
			totalGraphics++
			continue
		}
		if !isPictograph(r) && font != nil && font.Supports(r) {
			out.WriteRune(r)
			totalGraphics++
			continue
		}
		if isPictograph(r) {
			continue
		}
		if !isUnrenderableControl(r) {
			out.WriteByte('?')
			totalGraphics++
			fallbackCount++
		}
	}

	cleaned := reMultiSpace.ReplaceAllString(out.String(), " ")
	cleaned = string(trimEdgeRuns([]rune(cleaned)))
	return normalizedStats{
		text:          cleaned,
		totalGraphics: totalGraphics,
		fallbackCount: fallbackCount,
	}
}

// Normalize prepares text for printing with the named font.
// upstream: packages/snapcompact/src/snapcompact.ts normalize
func Normalize(text string, fontName string) string {
	return normalizeWithStats(text, fontName).text
}

// ScanRenderability scans text against fontName (unsafe when >5% of graphic chars fall back to '?').
// upstream: packages/snapcompact/src/snapcompact.ts scanRenderability
func ScanRenderability(text string, fontName string) RenderabilityScan {
	st := normalizeWithStats(text, fontName)
	ratio := 0.0
	if st.totalGraphics > 0 {
		ratio = float64(st.fallbackCount) / float64(st.totalGraphics)
	}
	return RenderabilityScan{
		IsSafe:            ratio <= unrenderableMaxFraction,
		UnrenderableRatio: ratio,
	}
}

// DimStopwords wraps English function words outside already-dim spans in DimOn/DimOff.
// upstream: packages/snapcompact/src/snapcompact.ts dimStopwords
func DimStopwords(text string) string {
	var out strings.Builder
	out.Grow(len(text) + 32)
	dim := false
	segStart := 0
	flush := func(seg string) {
		if seg == "" {
			return
		}
		if dim {
			out.WriteString(seg)
			return
		}
		out.WriteString(reAlphaRun.ReplaceAllStringFunc(seg, func(word string) string {
			if stopwords[strings.ToLower(word)] {
				return DimOn + word + DimOff
			}
			return word
		}))
	}
	for i := 0; i < len(text); i++ {
		if text[i] == byte(dimOnRune) || text[i] == byte(dimOffRune) {
			flush(text[segStart:i])
			out.WriteByte(text[i])
			dim = text[i] == byte(dimOnRune)
			segStart = i + 1
		}
	}
	flush(text[segStart:])
	return out.String()
}

// Cell-aware pagination & doc-column wrapping.

func charCells(r rune, wideCells bool) int {
	if r == dimOnRune || r == dimOffRune {
		return 0
	}
	if wideCells && isWideCodePoint(r) {
		return 2
	}
	return 1
}

func usesWideCells(shape Shape) bool {
	return shape.Font != "silver"
}

func cellLength(s string, wideCells bool) int {
	cells := 0
	for _, r := range s {
		cells += charCells(r, wideCells)
	}
	return cells
}

func sliceCells(s string, width int, wideCells bool) string {
	cells := 0
	placed := false
	var out strings.Builder
	for _, r := range s {
		w := charCells(r, wideCells)
		if placed && cells+w > width {
			break
		}
		out.WriteRune(r)
		cells += w
		if w > 0 {
			placed = true
		}
	}
	return out.String()
}

// paginateCells splits text into contiguous pages of at most capacity cells.
// upstream: packages/snapcompact/src/snapcompact.ts paginateCells
func paginateCells(text string, capacity, cols int, wideCells bool) []string {
	runes := []rune(text)
	var pages []string
	start := 0
	cell := 0
	hasCell := false
	for i, r := range runes {
		w := charCells(r, wideCells)
		if w == 0 {
			continue
		}
		at := cell
		if w == 2 && cols >= 2 && at%cols == cols-1 {
			at++
		}
		if hasCell && at+w > capacity {
			pages = append(pages, string(runes[start:i]))
			start = i
			at = 0
		}
		cell = at + w
		hasCell = true
	}
	if hasCell {
		pages = append(pages, string(runes[start:]))
	}
	return pages
}

// Wrap performs greedy word-wrap for doc columns.
// upstream: packages/snapcompact/src/snapcompact.ts wrap
func Wrap(text string, width int, wideCells bool) []string {
	if width <= 0 {
		return nil
	}
	var lines []string
	cur := ""
	curCells := 0
	for _, token := range strings.Fields(text) {
		word := token
		wordCells := cellLength(word, wideCells)
		for wordCells > width {
			if cur != "" {
				lines = append(lines, cur)
				cur = ""
				curCells = 0
			}
			head := sliceCells(word, width, wideCells)
			lines = append(lines, head)
			word = word[len(head):]
			wordCells = cellLength(word, wideCells)
		}
		if cur == "" {
			cur = word
			curCells = wordCells
		} else if curCells+1+wordCells <= width {
			cur += " " + word
			curCells += 1 + wordCells
		} else {
			lines = append(lines, cur)
			cur = word
			curCells = wordCells
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

func docPages(normalized string, geo Geometry, wideCells bool) []string {
	lines := Wrap(normalized, geo.Cols, wideCells)
	perPage := 2 * geo.Rows
	if perPage <= 0 {
		return nil
	}
	var pages []string
	for off := 0; off < len(lines); off += perPage {
		end := off + perPage
		if end > len(lines) {
			end = len(lines)
		}
		pages = append(pages, strings.Join(lines[off:end], "\n"))
	}
	return pages
}

func renderedChars(text string, shape Shape, geo Geometry) int {
	if shape.Columns == 2 {
		visible := 0
		for _, r := range text {
			if r != dimOnRune && r != dimOffRune && r != '\n' {
				visible++
			}
		}
		if visible > geo.Capacity {
			return geo.Capacity
		}
		return visible
	}
	wide := usesWideCells(shape)
	cell := 0
	count := 0
	for _, r := range text {
		w := charCells(r, wide)
		if w == 0 {
			continue
		}
		at := cell
		if w == 2 && geo.Cols >= 2 && at%geo.Cols == geo.Cols-1 {
			at++
		}
		if at+w > geo.Capacity {
			break
		}
		cell = at + w
		count++
	}
	return count
}

// StripThinkingSections drops every `¶think:` section outside dim spans.
// upstream: packages/snapcompact/src/snapcompact.ts stripThinkingSections
func StripThinkingSections(text string) string {
	if !strings.Contains(text, thinkLabel) {
		return text
	}
	// Split on section starts at string start or after \n / NewlineGlyph.
	var sections []string
	dimOpen := false
	curStart := 0
	appendPiece := func(piece string) {
		if dimOpen && len(sections) > 0 {
			sections[len(sections)-1] += piece
		} else {
			sections = append(sections, piece)
		}
		on := strings.LastIndex(piece, DimOn)
		off := strings.LastIndex(piece, DimOff)
		if on != off {
			dimOpen = on > off
		}
	}
	for i := 0; i < len(text); {
		if i > curStart && isScopeBoundary(text, i) {
			appendPiece(text[curStart:i])
			curStart = i
		}
		_, size := nextRuneStep(text, i)
		i += size
	}
	appendPiece(text[curStart:])

	var kept []string
	for _, s := range sections {
		if !strings.HasPrefix(s, thinkLabel) {
			kept = append(kept, s)
		}
	}
	if len(kept) == len(sections) {
		return text
	}
	stripped := strings.Join(kept, "")
	if len(sections) > 0 && strings.HasPrefix(sections[len(sections)-1], thinkLabel) {
		stripped = strings.TrimRight(stripped, "\n"+NewlineGlyph)
	}
	return stripped
}

func isScopeBoundary(s string, idx int) bool {
	if !hasScopePrefix(s[idx:]) {
		return false
	}
	if idx == 0 {
		return true
	}
	if s[idx-1] == '\n' {
		return true
	}
	return strings.HasSuffix(s[:idx], NewlineGlyph)
}

func hasScopePrefix(s string) bool {
	return strings.HasPrefix(s, "¶user:") ||
		strings.HasPrefix(s, "¶think:") ||
		strings.HasPrefix(s, "¶ai:") ||
		strings.HasPrefix(s, "¶call:")
}

func nextRuneStep(s string, i int) (rune, int) {
	for _, r := range s[i:] {
		if r == unicode.ReplacementChar && i < len(s) {
			return r, 1
		}
		return r, len(string(r))
	}
	return 0, 1
}

// TextWithoutThinking removes `¶think:` sections and the reasoning scope from any legend.
func TextWithoutThinking(text string) string {
	return reLegendReason.ReplaceAllString(StripThinkingSections(text), "`¶user:`, $1")
}

// FormatFileList renders omp's grouped FILES list from read/written/edited paths.
func FormatFileList(readFiles, modifiedFiles []string, readSet map[string]bool) string {
	if len(readFiles) == 0 && len(modifiedFiles) == 0 {
		return ""
	}
	mode := make(map[string]string, len(readFiles)+len(modifiedFiles))
	for _, f := range readFiles {
		if !reURLScheme.MatchString(f) {
			mode[f] = "Read"
		}
	}
	for _, f := range modifiedFiles {
		if !reURLScheme.MatchString(f) {
			if readSet[f] {
				mode[f] = "RW"
			} else {
				mode[f] = "Write"
			}
		}
	}
	if len(mode) == 0 {
		return ""
	}
	all := make([]string, 0, len(mode))
	for p := range mode {
		all = append(all, p)
	}
	sort.Strings(all)
	shown := all
	elided := 0
	if len(all) > fileOpSummaryLimit {
		shown = all[:fileOpSummaryLimit]
		elided = len(all) - fileOpSummaryLimit
	}
	var lines []string
	for _, p := range shown {
		lines = append(lines, fmt.Sprintf("%s (%s)", p, mode[p]))
	}
	if elided > 0 {
		lines = append(lines, fmt.Sprintf("[…%d files elided…]", elided))
	}
	return strings.Join(lines, "\n")
}
