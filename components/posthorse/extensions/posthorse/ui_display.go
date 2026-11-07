package posthorse

// Port of src/ui/display.ts: the display-only facts every tool result carries
// in its `details`. The union is one struct here; Kind selects the members
// that are present. The numeric members are float64 because the cards read
// the facts back from decoded JSON, where every number is a double and
// `Number.isInteger` is observable (display.ts:195-207).

import (
	"math"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// pageRange is PageRange (display.ts:3-7).
type pageRange struct {
	offset float64
	end    float64
	total  float64
}

// historySpan is HistorySpan (display.ts:9-12).
type historySpan struct {
	headerLength float64
	length       float64
}

// posthorseDisplay is PosthorseDisplay (display.ts:15-49).
//
// kind is one of "context", "notes-list", "notes-search", "note-read",
// "note-write", "note-append", "new-context", "history-search",
// "history-read". Members that the kind does not carry are zero.
type posthorseDisplay struct {
	kind string

	// kind "context": usage is nil when absent; usageTokens/usagePercent nil
	// are JSON null. rollover "" is absent.
	usage        bool
	usageTokens  *float64
	usageWindow  float64
	usagePercent *float64
	rollover     string
	rolloverAt   *float64

	// kinds "notes-list" and "notes-search".
	count float64
	page  *pageRange
	more  *bool

	// kinds "note-read" and "history-read" carry their page range inline.
	pageRange

	// kinds "notes-list", "notes-search", "note-read", "history-read".
	headerLength *float64

	// kind "history-search".
	entries      []historySpan
	footerLength *float64
	skipped      *float64

	// kind "history-read".
	imageOffset *float64
	imageEnd    *float64
	imageTotal  *float64
}

func fl(v int) *float64 {
	f := float64(v)
	return &f
}

// displayKind builds the facts that carry only a kind: "context" (usage not
// known yet, context-tools.ts:23), "new-context" (context-tools.ts:84),
// "note-write" (notes.ts:167) and "note-append" (notes.ts:200).
func displayKind(kind string) *posthorseDisplay { return &posthorseDisplay{kind: kind} }

// displayContext is the context report display (context-tools.ts:29-34).
// usage nil is only for completeness: the original builds this value after the
// usage is known. rollover "" and rolloverAt nil are `undefined`.
func displayContext(usage *contextUsage, rollover string, rolloverAt *int) *posthorseDisplay {
	d := &posthorseDisplay{kind: "context", rollover: rollover}
	if usage != nil {
		d.usage = true
		if usage.tokens != nil {
			d.usageTokens = fl(*usage.tokens)
		}
		d.usageWindow = float64(usage.contextWindow)
		d.usagePercent = usage.percent
	}
	if rolloverAt != nil {
		d.rolloverAt = fl(*rolloverAt)
	}
	return d
}

// displayNotes is the `notes()` page display of a list or search
// (paging.ts:127-136). kind is "notes-list" or "notes-search"; more is added
// only when true, as `...(more === true ? { more } : {})` does.
func displayNotes(kind string, headerLength, count, offset, end, total int, more bool) *posthorseDisplay {
	d := &posthorseDisplay{
		kind:         kind,
		headerLength: fl(headerLength),
		count:        float64(count),
		page:         &pageRange{offset: float64(offset), end: float64(end), total: float64(total)},
	}
	if more {
		d.more = &more
	}
	return d
}

// displayNoteRead is the read display of a note (notes.ts:133-138).
func displayNoteRead(headerLength, offset, end, total int) *posthorseDisplay {
	return &posthorseDisplay{
		kind:         "note-read",
		headerLength: fl(headerLength),
		pageRange:    pageRange{offset: float64(offset), end: float64(end), total: float64(total)},
	}
}

// displayHistorySearch is the history search display (history.ts:108-114
// history-pages.ts): entries are the page's spans.
func displayHistorySearch(entries []historySpan, footerLength int, more bool, skipped int) *posthorseDisplay {
	return &posthorseDisplay{
		kind:         "history-search",
		entries:      entries,
		footerLength: fl(footerLength),
		more:         &more,
		skipped:      fl(skipped),
	}
}

// displayHistoryRead is the history read display (history-pages.ts:173-182).
func displayHistoryRead(headerLength, offset, end, total, imageOffset, imageEnd, imageTotal int) *posthorseDisplay {
	return &posthorseDisplay{
		kind:         "history-read",
		headerLength: fl(headerLength),
		pageRange:    pageRange{offset: float64(offset), end: float64(end), total: float64(total)},
		imageOffset:  fl(imageOffset),
		imageEnd:     fl(imageEnd),
		imageTotal:   fl(imageTotal),
	}
}

// numberJSON appends v as JSON.stringify prints a finite number.
func numberJSON(dst []byte, v float64) []byte {
	out, err := json.AppendStringify(dst, v)
	if err != nil {
		return append(dst, "null"...)
	}
	return out
}

func optNumberJSON(dst []byte, key string, v *float64) []byte {
	if v == nil {
		return dst
	}
	dst = append(dst, ',', '"')
	dst = append(dst, key...)
	dst = append(dst, '"', ':')
	return numberJSON(dst, *v)
}

func numberKey(dst []byte, key string, v float64) []byte {
	dst = append(dst, ',', '"')
	dst = append(dst, key...)
	dst = append(dst, '"', ':')
	return numberJSON(dst, v)
}

func pageJSON(dst []byte, p pageRange) []byte {
	dst = append(dst, `{"offset":`...)
	dst = numberJSON(dst, p.offset)
	dst = numberKey(dst, "end", p.end)
	dst = numberKey(dst, "total", p.total)
	return append(dst, '}')
}

// MarshalJSON writes the facts in the key order of the TS object literals
// (the order is visible in traces): see the constructors' upstream lines.
func (d *posthorseDisplay) MarshalJSON() ([]byte, error) {
	dst := make([]byte, 0, 160)
	dst = append(dst, `{"kind":"`...)
	dst = append(dst, d.kind...)
	dst = append(dst, '"')
	switch d.kind {
	case "context":
		if d.usage {
			dst = append(dst, `,"usage":{"tokens":`...)
			if d.usageTokens == nil {
				dst = append(dst, "null"...)
			} else {
				dst = numberJSON(dst, *d.usageTokens)
			}
			dst = numberKey(dst, "contextWindow", d.usageWindow)
			dst = append(dst, `,"percent":`...)
			if d.usagePercent == nil {
				dst = append(dst, "null"...)
			} else {
				dst = numberJSON(dst, *d.usagePercent)
			}
			dst = append(dst, '}')
		}
		if d.rollover != "" {
			dst = append(dst, `,"rollover":"`...)
			dst = append(dst, d.rollover...)
			dst = append(dst, '"')
		}
		dst = optNumberJSON(dst, "rolloverAt", d.rolloverAt)
	case "notes-list", "notes-search":
		dst = optNumberJSON(dst, "headerLength", d.headerLength)
		dst = numberKey(dst, "count", d.count)
		if d.page != nil {
			dst = append(dst, `,"page":`...)
			dst = pageJSON(dst, *d.page)
		}
		if d.more != nil {
			dst = append(dst, `,"more":`...)
			dst = strconv.AppendBool(dst, *d.more)
		}
	case "note-read":
		dst = optNumberJSON(dst, "headerLength", d.headerLength)
		dst = numberKey(dst, "offset", d.offset)
		dst = numberKey(dst, "end", d.end)
		dst = numberKey(dst, "total", d.total)
	case "history-search":
		dst = append(dst, `,"entries":[`...)
		for i, e := range d.entries {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = append(dst, `{"headerLength":`...)
			dst = numberJSON(dst, e.headerLength)
			dst = numberKey(dst, "length", e.length)
			dst = append(dst, '}')
		}
		dst = append(dst, ']')
		dst = optNumberJSON(dst, "footerLength", d.footerLength)
		if d.more != nil {
			dst = append(dst, `,"more":`...)
			dst = strconv.AppendBool(dst, *d.more)
		}
		dst = optNumberJSON(dst, "skipped", d.skipped)
	case "history-read":
		dst = optNumberJSON(dst, "headerLength", d.headerLength)
		dst = numberKey(dst, "offset", d.offset)
		dst = numberKey(dst, "end", d.end)
		dst = numberKey(dst, "total", d.total)
		dst = optNumberJSON(dst, "imageOffset", d.imageOffset)
		dst = optNumberJSON(dst, "imageEnd", d.imageEnd)
		dst = optNumberJSON(dst, "imageTotal", d.imageTotal)
	}
	return append(dst, '}'), nil
}

// ---- validation of decoded facts (display.ts:51-147) ----

func isNumber(v any) bool {
	_, ok := v.(float64)
	return ok
}

// optionalNumbers: display.ts:51-56. A present key must be a number (a JSON
// null fails, as `null !== undefined` and typeof null is "object").
func optionalNumbers(m map[string]any, keys ...string) bool {
	for _, key := range keys {
		if v, ok := m[key]; ok && !isNumber(v) {
			return false
		}
	}
	return true
}

func numberOf(m map[string]any, key string) (float64, bool) {
	f, ok := m[key].(float64)
	return f, ok
}

func optNumber(m map[string]any, key string) *float64 {
	if f, ok := m[key].(float64); ok {
		return &f
	}
	return nil
}

// isPage: display.ts:58-65.
func isPage(v any) (pageRange, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return pageRange{}, false
	}
	offset, ok1 := numberOf(m, "offset")
	end, ok2 := numberOf(m, "end")
	total, ok3 := numberOf(m, "total")
	return pageRange{offset, end, total}, ok1 && ok2 && ok3
}

// isSpan: display.ts:67-73.
func isSpan(v any) (historySpan, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return historySpan{}, false
	}
	headerLength, ok1 := numberOf(m, "headerLength")
	length, ok2 := numberOf(m, "length")
	return historySpan{headerLength, length}, ok1 && ok2
}

func isContextDisplay(m map[string]any) (*posthorseDisplay, bool) {
	d := &posthorseDisplay{kind: "context"}
	if usage, present := m["usage"]; present { // display.ts:75-95
		u, ok := usage.(map[string]any)
		if !ok {
			return nil, false
		}
		tokens, tokensPresent := u["tokens"]
		percent, percentPresent := u["percent"]
		window, windowOK := numberOf(u, "contextWindow")
		if !tokensPresent || !percentPresent || !windowOK {
			return nil, false
		}
		if tokens != nil {
			f, ok := tokens.(float64)
			if !ok {
				return nil, false
			}
			d.usageTokens = &f
		}
		if percent != nil {
			f, ok := percent.(float64)
			if !ok {
				return nil, false
			}
			d.usagePercent = &f
		}
		d.usage = true
		d.usageWindow = window
	}
	if !optionalNumbers(m, "rolloverAt") {
		return nil, false
	}
	d.rolloverAt = optNumber(m, "rolloverAt")
	if rollover, present := m["rollover"]; present {
		s, _ := rollover.(string)
		if s != "enabled" && s != "disabled" && s != "unsupported" {
			return nil, false
		}
		d.rollover = s
	}
	return d, true
}

func optBool(m map[string]any, key string) (*bool, bool) {
	v, present := m[key]
	if !present {
		return nil, true
	}
	b, ok := v.(bool)
	return &b, ok
}

// displayOf is displayOf (display.ts:144-147): saved sessions and streamed
// tool results can predate the display contract, so unknown or malformed
// facts are nil.
func displayOf(value any) *posthorseDisplay {
	m, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	kind, _ := m["kind"].(string)
	switch kind {
	case "context":
		d, ok := isContextDisplay(m)
		if !ok {
			return nil
		}
		return d
	case "notes-list", "notes-search": // isNotesDisplay: display.ts:106-113
		count, ok := numberOf(m, "count")
		if !ok || !optionalNumbers(m, "headerLength") {
			return nil
		}
		d := &posthorseDisplay{kind: kind, count: count, headerLength: optNumber(m, "headerLength")}
		if page, present := m["page"]; present {
			p, ok := isPage(page)
			if !ok {
				return nil
			}
			d.page = &p
		}
		more, ok := optBool(m, "more")
		if !ok {
			return nil
		}
		d.more = more
		return d
	case "note-read":
		p, ok := isPage(m)
		if !ok || !optionalNumbers(m, "headerLength") {
			return nil
		}
		return &posthorseDisplay{kind: kind, pageRange: p, headerLength: optNumber(m, "headerLength")}
	case "history-read":
		p, ok := isPage(m)
		headerLength, hok := numberOf(m, "headerLength")
		if !ok || !hok || !optionalNumbers(m, "imageOffset", "imageEnd", "imageTotal") {
			return nil
		}
		return &posthorseDisplay{
			kind: kind, pageRange: p, headerLength: &headerLength,
			imageOffset: optNumber(m, "imageOffset"), imageEnd: optNumber(m, "imageEnd"), imageTotal: optNumber(m, "imageTotal"),
		}
	case "history-search": // isSearchDisplay: display.ts:97-104
		raw, ok := m["entries"].([]any)
		if !ok || !optionalNumbers(m, "footerLength", "skipped") {
			return nil
		}
		d := &posthorseDisplay{kind: kind, entries: make([]historySpan, 0, len(raw))}
		for _, item := range raw {
			span, ok := isSpan(item)
			if !ok {
				return nil
			}
			d.entries = append(d.entries, span)
		}
		more, ok := optBool(m, "more")
		if !ok {
			return nil
		}
		d.more = more
		d.footerLength = optNumber(m, "footerLength")
		d.skipped = optNumber(m, "skipped")
		return d
	case "note-write", "note-append", "new-context":
		return &posthorseDisplay{kind: kind}
	}
	return nil
}

// historySection is HistorySection (display.ts:149-152).
type historySection struct {
	body   string
	header string
}

// spansOf: display.ts:154-165.
func spansOf(rawLength int, display *posthorseDisplay) []historySpan {
	if display == nil {
		return nil
	}
	switch display.kind {
	case "history-search":
		return display.entries
	case "history-read":
		return []historySpan{{headerLength: *display.headerLength, length: float64(rawLength)}}
	}
	return nil
}

func isInteger(v float64) bool { return !math.IsInf(v, 0) && v == math.Trunc(v) }

// historySections: display.ts:167-193. raw.length and every slice are UTF-16.
func historySections(raw string, display *posthorseDisplay) []historySection {
	rawLength := u16len(raw)
	spans := spansOf(rawLength, display)
	if len(spans) == 0 {
		return nil
	}
	footerLength := 0.0
	if display.kind == "history-search" && display.footerLength != nil {
		footerLength = *display.footerLength
	}
	if !validFooter(footerLength, rawLength) {
		return nil
	}
	bodyLength := rawLength - int(footerLength)
	start := 0
	sections := make([]historySection, 0, len(spans))
	// Spans are sequential, so one forward pass maps every UTF-16 offset to a byte offset; ASCII text needs none.
	cursor := u16Cursor{s: raw, ascii: rawLength == len(raw)}
	for _, span := range spans {
		if !validSpan(span, start, bodyLength) {
			return nil
		}
		hl, l := int(span.headerLength), int(span.length)
		headerStart, split1 := cursor.to(start)
		bodyStart, split2 := cursor.to(start + hl)
		end, split3 := cursor.to(start + l)
		if split1 || split2 || split3 {
			// A boundary inside a surrogate pair splits it (display.ts:183-189 slices by UTF-16 unit).
			sections = append(sections, historySection{header: uiTrimEnd(u16slice(raw, start, start+hl)), body: u16slice(raw, start+hl, start+l)})
		} else {
			sections = append(sections, historySection{header: uiTrimEnd(raw[headerStart:bodyStart]), body: raw[bodyStart:end]})
		}
		start += l + 1
	}
	if start != bodyLength+1 {
		return nil
	}
	return sections
}

// validFooter: display.ts:195-197.
func validFooter(length float64, rawLength int) bool {
	return isInteger(length) && length >= 0 && length <= float64(rawLength)
}

// validSpan: display.ts:199-207.
func validSpan(span historySpan, start, bodyLength int) bool {
	return isInteger(span.headerLength) && isInteger(span.length) &&
		span.headerLength >= 0 && span.headerLength <= span.length &&
		float64(start)+span.length <= float64(bodyLength)
}

// uiIntegerString prints an integer-valued number as `${x}` does (-0 prints 0; no grouping).
func uiIntegerString(x float64) string {
	if x == 0 {
		return "0"
	}
	return strconv.FormatFloat(x, 'f', -1, 64)
}

// n is `value.toLocaleString("en-US")` (display.ts:209, cards.ts:30): ICU formats the shortest decimal that
// round-trips the double (not its exact binary value), rounds half away from zero to at most three fraction digits,
// groups the integer part in threes, and keeps the sign of a negative value that rounds to zero ("-0").
func n(value float64) string {
	switch {
	case math.IsNaN(value):
		return "NaN"
	case math.IsInf(value, 1):
		return "∞"
	case math.IsInf(value, -1):
		return "-∞"
	}
	neg := math.Signbit(value)
	if neg {
		value = -value
	}
	digits := strconv.FormatFloat(value, 'f', -1, 64)
	frac := ""
	if dot := strings.IndexByte(digits, '.'); dot >= 0 {
		digits, frac = digits[:dot], digits[dot+1:]
		if len(frac) > 3 {
			roundUp := frac[3] >= '5'
			frac = frac[:3]
			if roundUp { // increment the decimal string, carrying into the integer part
				buf := []byte(digits + frac)
				i := len(buf) - 1
				for ; i >= 0 && buf[i] == '9'; i-- {
					buf[i] = '0'
				}
				if i < 0 {
					buf = append([]byte{'1'}, buf...)
				} else {
					buf[i]++
				}
				digits, frac = string(buf[:len(buf)-3]), string(buf[len(buf)-3:])
			}
		}
		frac = strings.TrimRight(frac, "0")
		if frac != "" {
			frac = "." + frac
		}
	}
	var b strings.Builder
	b.Grow(len(digits) + len(digits)/3 + len(frac) + 1)
	if neg {
		b.WriteByte('-')
	}
	lead := len(digits) % 3
	if lead == 0 {
		lead = 3
	}
	b.WriteString(digits[:lead])
	for i := lead; i < len(digits); i += 3 {
		b.WriteByte(',')
		b.WriteString(digits[i : i+3])
	}
	b.WriteString(frac)
	return b.String()
}

// pageSummary: display.ts:211-219.
func pageSummary(page pageRange, more bool) string {
	if page.total == 0 {
		return "Empty note"
	}
	if page.offset == 0 && page.end == page.total {
		return n(page.total) + " chars · complete"
	}
	var b strings.Builder
	b.WriteString("Chars ")
	b.WriteString(n(page.offset))
	b.WriteString("–")
	b.WriteString(n(page.end))
	b.WriteString(" of ")
	b.WriteString(n(page.total))
	if more {
		b.WriteByte('+')
	}
	if page.end < page.total {
		b.WriteString("\nNext offset ")
		b.WriteString(n(page.end))
	} else {
		b.WriteString(" · final page")
	}
	return b.String()
}

// contextSummary: display.ts:221-232.
func contextSummary(display *posthorseDisplay) string {
	if !display.usage || display.usageTokens == nil {
		return "Context usage unknown"
	}
	tokens := *display.usageTokens
	configured := "≈" + n(math.Max(0, display.usageWindow-tokens)) + " tokens to configured context limit"
	var rollover string
	if display.rollover == "enabled" && display.rolloverAt != nil {
		rollover = "≈" + n(math.Max(0, *display.rolloverAt-tokens)) + " tokens to rollover"
	} else {
		state := display.rollover
		if state == "" {
			state = "unknown"
		}
		rollover = "Automatic rollover " + state
	}
	out := rollover + "\n" + configured + "\n≈" + n(tokens) + " / " + n(display.usageWindow) + " used"
	if display.usagePercent != nil {
		out += " (≈" + uiIntegerString(jsRound(*display.usagePercent)) + "%)"
	}
	return out
}

// historyReadSummary: display.ts:234-248.
func historyReadSummary(display *posthorseDisplay) string {
	if display.imageTotal == nil || *display.imageTotal == 0 || display.imageEnd == nil {
		return pageSummary(display.pageRange, false)
	}
	first := pageSummary(display.pageRange, false)
	if i := strings.IndexByte(first, '\n'); i >= 0 { // split("\n", 1).join("")
		first = first[:i]
	}
	imageOffset := 0.0
	if display.imageOffset != nil {
		imageOffset = *display.imageOffset
	}
	summary := first + "\nImages " + n(imageOffset) + "–" + n(*display.imageEnd) + " of " + n(*display.imageTotal)
	if display.end < display.total || *display.imageEnd < *display.imageTotal {
		return "Next offset " + n(display.end) + "\nimageOffset " + n(*display.imageEnd) + "\n" + summary
	}
	return summary
}

// historySearchSummary: display.ts:250-261.
func historySearchSummary(display *posthorseDisplay, all, validSections bool) string {
	if len(display.entries) > 0 && !validSections {
		return ""
	}
	skipped := 0.0
	if display.skipped != nil {
		skipped = *display.skipped
	}
	var more bool
	if display.more != nil {
		more = *display.more
	} else {
		more = display.footerLength != nil && *display.footerLength > 0
	}
	var b strings.Builder
	if len(display.entries) > 0 {
		b.WriteString(n(float64(len(display.entries))))
		b.WriteString(" matches")
	} else {
		b.WriteString("No matches")
	}
	b.WriteString(" · ")
	if all {
		b.WriteString("all sessions")
	} else {
		b.WriteString("current branch")
	}
	if skipped > 0 {
		b.WriteString(" · ")
		b.WriteString(n(skipped))
		b.WriteString(" in context skipped")
	}
	if more {
		b.WriteString("\nMore results; continue with cursor")
	}
	return b.String()
}

// displaySummary: display.ts:263-292. args is the tool call's arguments.
func displaySummary(display *posthorseDisplay, args map[string]any, validSections bool) string {
	if display == nil {
		return ""
	}
	switch display.kind {
	case "context":
		return contextSummary(display)
	case "notes-list":
		if display.count > 0 {
			plural := "s"
			if display.count == 1 {
				plural = ""
			}
			return n(display.count) + " note" + plural + " returned"
		}
		return "No notes yet"
	case "notes-search":
		if display.count > 0 {
			return n(display.count) + " matches returned"
		}
		return "No matches"
	case "note-read":
		return pageSummary(display.pageRange, false)
	case "note-write":
		if content, ok := args["content"].(string); ok && content == "" {
			return "Cleared note"
		}
		return "Saved note"
	case "note-append":
		return "Appended to note"
	case "history-search":
		all, _ := args["all"].(bool)
		return historySearchSummary(display, all, validSections)
	case "history-read":
		return historyReadSummary(display)
	case "new-context":
		return "Requested after foreground tools succeed.\nBackground work continues."
	}
	return ""
}

// summaryOnly: display.ts:294-312.
func summaryOnly(display *posthorseDisplay) bool {
	if display == nil {
		return false
	}
	switch display.kind {
	case "context", "new-context", "note-write", "note-append":
		return true
	case "notes-list", "notes-search":
		return display.count == 0
	case "note-read":
		return display.total == 0
	case "history-search":
		return len(display.entries) == 0
	}
	return false
}

// u16Cursor maps non-decreasing UTF-16 offsets of s to byte offsets in one forward pass.
type u16Cursor struct {
	s     string
	ascii bool
	unit  int
	off   int
}

// to returns the byte offset of UTF-16 offset unit; split is true when unit falls inside a surrogate pair (the
// offset after the pair is then returned) or lies before the cursor: the caller slices by unit instead.
func (c *u16Cursor) to(unit int) (off int, split bool) {
	if c.ascii {
		return min(unit, len(c.s)), false
	}
	for c.unit < unit && c.off < len(c.s) {
		size, units := decodeUnit(c.s, c.off)
		c.off += size
		c.unit += units
	}
	return c.off, c.unit > unit
}
