package eval

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// upstream: packages/tui/src/tools/eval.ts, packages/tui/src/tools/output-meta.ts,
//           packages/tui/src/tools/streaming-output.ts,
//           packages/coding-agent/src/eval/types.ts,
//           packages/coding-agent/src/tools/tool-timeouts.ts

const (
	// DefaultTimeoutSec is omp's TOOL_TIMEOUTS.eval.default (30s).
	DefaultTimeoutSec = 30.0
	// MinTimeoutSec is omp's TOOL_TIMEOUTS.eval.min (1s).
	MinTimeoutSec = 1.0
	// MaxTimeoutSec is omp's TOOL_TIMEOUTS.eval.max (3600s).
	MaxTimeoutSec = 3600.0

	// MaxViewsPerCell matches omp's MAX_VIEWS_PER_CELL (eval.ts:179).
	MaxViewsPerCell = 16
	// MaxViewJSONChars matches omp's MAX_VIEW_JSON_CHARS (eval.ts:177).
	MaxViewJSONChars = 32 * 1024
	// MaxStatusEvents matches omp's MAX_STATUS_EVENTS (tui/tools/eval.ts:252).
	MaxStatusEvents = 200
)

// EvalLanguage is omp's EvalLanguage ("python" | "js").
// upstream: packages/tui/src/tools/eval.ts:58
type EvalLanguage string

const (
	LangPython EvalLanguage = "python"
	LangJS     EvalLanguage = "js"
)

// EvalStatusEvent is a status event emitted by eval prelude helpers for TUI rendering.
// upstream: packages/tui/src/tools/eval.ts:61
type EvalStatusEvent map[string]any

// EvalViewEntry pairs a pretty view with the exact printed text it stands for.
// upstream: packages/tui/src/tools/eval.ts:93
type EvalViewEntry struct {
	Text string `json:"text"`
	View any    `json:"view"`
}

// EvalCellResult is the per-cell execution result for transcript rendering.
// upstream: packages/tui/src/tools/eval.ts:99
type EvalCellResult struct {
	Index              int               `json:"index"`
	Title              string            `json:"title,omitempty"`
	Code               string            `json:"code"`
	Language           EvalLanguage      `json:"language,omitempty"`
	Output             string            `json:"output"`
	Views              []EvalViewEntry   `json:"views,omitempty"`
	Status             string            `json:"status"` // "pending" | "running" | "complete" | "error"
	DurationMs         *int64            `json:"durationMs,omitempty"`
	ExitCode           *int              `json:"exitCode,omitempty"`
	StatusEvents       []EvalStatusEvent `json:"statusEvents,omitempty"`
	StatusEventsElided int               `json:"statusEventsElided,omitempty"`
}

// EvalImageDetail matches omp's ImageContent wire shape stored in details.images.
type EvalImageDetail struct {
	Type     string `json:"type"`
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
	Detail   string `json:"detail,omitempty"`
	URL      string `json:"url,omitempty"`
}

// EvalAsyncDetail carries async job status when a cell auto-backgrounds.
type EvalAsyncDetail struct {
	State string `json:"state"`
	JobID string `json:"jobId"`
	Type  string `json:"type"`
}

// LineRange is a 1-indexed inclusive line range (upstream: TruncationMeta.shownRange).
type LineRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// TruncationMeta mirrors omp's TruncationMeta in packages/tui/src/tools/output-meta.ts:17.
type TruncationMeta struct {
	Direction           string     `json:"direction"`
	TruncatedBy         string     `json:"truncatedBy"`
	TotalLines          int        `json:"totalLines"`
	TotalBytes          int        `json:"totalBytes"`
	OutputLines         int        `json:"outputLines"`
	OutputBytes         int        `json:"outputBytes"`
	MaxBytes            int        `json:"maxBytes,omitempty"`
	ShownRange          *LineRange `json:"shownRange,omitempty"`
	ArtifactID          string     `json:"artifactId,omitempty"`
	ArtifactElidedBytes int        `json:"artifactElidedBytes,omitempty"`
	PartialLine         bool       `json:"partialLine,omitempty"`
}

// OutputMeta mirrors omp's OutputMeta in packages/tui/src/tools/output-meta.ts:107.
type OutputMeta struct {
	Truncation *TruncationMeta `json:"truncation,omitempty"`
}

// EvalToolDetails is the Details payload returned by the eval tool.
// Field names match packages/tui/src/tools/eval.ts EvalToolDetails verbatim.
type EvalToolDetails struct {
	Cells              []EvalCellResult  `json:"cells,omitempty"`
	Images             []EvalImageDetail `json:"images,omitempty"`
	StatusEvents       []EvalStatusEvent `json:"statusEvents,omitempty"`
	StatusEventsElided int               `json:"statusEventsElided,omitempty"`
	Replies            []string          `json:"replies,omitempty"`
	IsError            bool              `json:"isError,omitempty"`
	Meta               *OutputMeta       `json:"meta,omitempty"`
	Language           EvalLanguage      `json:"language,omitempty"`
	Languages          []EvalLanguage    `json:"languages,omitempty"`
	Notice             string            `json:"notice,omitempty"`
	Async              *EvalAsyncDetail  `json:"async,omitempty"`
}

// DisplayOutput is one non-text output emitted by a cell: image, view, or status.
// upstream: packages/coding-agent/src/eval/types.ts:43
type DisplayOutput struct {
	Type   string // "image" | "view" | "status"
	Image  *EvalImageDetail
	Text   string
	View   any
	Status EvalStatusEvent
}

// clampEvalTimeout mirrors omp's clampTimeout("eval", raw, maxTimeout) keeping fractional seconds.
// upstream: packages/coding-agent/src/tools/tool-timeouts.ts:34
func clampEvalTimeout(raw *float64, maxTimeout float64) float64 {
	val := DefaultTimeoutSec
	if raw != nil {
		if *raw == 0 {
			return 0
		}
		val = *raw
	}
	if maxTimeout > 0 && val > maxTimeout {
		val = maxTimeout
	}
	if math.IsNaN(val) || val < MinTimeoutSec {
		return MinTimeoutSec
	}
	if val > MaxTimeoutSec {
		return MaxTimeoutSec
	}
	return val
}

// formatTimeoutNumber formats a finite float64 without scientific notation (matching JS template strings).
func formatTimeoutNumber(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// statusEventKey mirrors omp's statusEventKey in packages/tui/src/tools/eval.ts:237.
func statusEventKey(event EvalStatusEvent) string {
	op, _ := event["op"].(string)
	if op != "agent" && op != "judge_batch" && op != "mcp_progress" {
		return ""
	}
	id, ok := event["id"].(string)
	if !ok {
		return ""
	}
	return op + "\x00" + id
}

// recordStatusEvent appends or coalesces a status event into cell, enforcing MaxStatusEvents.
// upstream: packages/tui/src/tools/eval.ts:282
func recordStatusEvent(cell *EvalCellResult, event EvalStatusEvent) {
	if event == nil {
		return
	}
	key := statusEventKey(event)
	if key != "" {
		for i, existing := range cell.StatusEvents {
			if statusEventKey(existing) == key {
				cell.StatusEvents[i] = event
				return
			}
		}
	}
	cell.StatusEvents = append(cell.StatusEvents, event)
	if len(cell.StatusEvents) <= MaxStatusEvents {
		return
	}
	keep := -1
	for i := len(cell.StatusEvents) - 1; i >= 0; i-- {
		if cell.StatusEvents[i]["op"] == "todo" && cell.StatusEvents[i]["committed"] == true {
			keep = i
			break
		}
	}
	oldest := -1
	for i, e := range cell.StatusEvents {
		if i == keep {
			continue
		}
		if e["op"] == "mcp_progress" || statusEventKey(e) == "" {
			oldest = i
			break
		}
	}
	if oldest < 0 {
		return
	}
	cell.StatusEvents = append(cell.StatusEvents[:oldest], cell.StatusEvents[oldest+1:]...)
	cell.StatusEventsElided++
}

// sanitizeText strips ANSI CSI escapes and normalizes CR/CRLF to LF.
func sanitizeText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\r' {
			if i+1 < len(s) && s[i+1] == '\n' {
				continue
			}
			b.WriteByte('\n')
			continue
		}
		if c == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && !((s[j] >= 'A' && s[j] <= 'Z') || (s[j] >= 'a' && s[j] <= 'z')) {
				j++
			}
			if j < len(s) {
				i = j
				continue
			}
		}
		b.WriteByte(c)
	}
	return b.String()
}

// boundedOutputBuffer keeps a bounded rolling tail of sanitized output in memory
// (at most 2*maxBytes internally, finalized to <= maxBytes on a line/UTF-8 boundary)
// while tracking exact total bytes and total lines seen.
// upstream: packages/tui/src/tools/streaming-output.ts (TailBuffer, OutputSink)
type boundedOutputBuffer struct {
	mu           sync.Mutex
	maxBytes     int
	keepLimit    int
	buf          []byte
	captureSpill bool
	spillCap     int
	fullBuf      []byte
	totalBytes   int
	totalLines   int
	endsWithNL   bool
	sawData      bool
	truncated    bool
}

// defaultSpillCaptureCap bounds the in-memory full capture when WriteSpill is
// wired (matching omp's 16 MiB artifact cap so pathological output never OOMs).
const defaultSpillCaptureCap = 16 * 1024 * 1024

func newBoundedOutputBuffer(maxBytes int) *boundedOutputBuffer {
	if maxBytes <= 0 {
		maxBytes = DefaultOutputMaxBytes
	}
	return &boundedOutputBuffer{
		maxBytes:  maxBytes,
		keepLimit: maxBytes * 2,
	}
}

func newSpillableOutputBuffer(maxBytes int, captureSpill bool) *boundedOutputBuffer {
	b := newBoundedOutputBuffer(maxBytes)
	if captureSpill {
		b.captureSpill = true
		b.spillCap = defaultSpillCaptureCap
	}
	return b
}

func (b *boundedOutputBuffer) Append(raw string) {
	if raw == "" {
		return
	}
	s := sanitizeText(raw)
	if s == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sawData = true
	b.totalBytes += len(s)
	b.totalLines += strings.Count(s, "\n")
	b.endsWithNL = s[len(s)-1] == '\n'
	if b.captureSpill && len(b.fullBuf) < b.spillCap {
		rem := b.spillCap - len(b.fullBuf)
		if len(s) <= rem {
			b.fullBuf = append(b.fullBuf, s...)
		} else {
			b.fullBuf = append(b.fullBuf, s[:rem]...)
		}
	}
	b.buf = append(b.buf, s...)
	if len(b.buf) > b.keepLimit {
		b.truncated = true
		drop := len(b.buf) - b.keepLimit
		// Advance past any UTF-8 continuation bytes so the kept window stays valid UTF-8.
		for drop < len(b.buf) && !utf8.RuneStart(b.buf[drop]) {
			drop++
		}
		copy(b.buf, b.buf[drop:])
		b.buf = b.buf[:len(b.buf)-drop]
	}
}

func (b *boundedOutputBuffer) PrependLine(line string) {
	if line == "" {
		return
	}
	s := sanitizeText(line)
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sawData = true
	b.totalBytes += len(s)
	b.totalLines += strings.Count(s, "\n")
	if b.captureSpill {
		combined := make([]byte, 0, min(len(s)+len(b.fullBuf), b.spillCap))
		combined = append(combined, s...)
		if rem := b.spillCap - len(combined); rem > 0 {
			if len(b.fullBuf) <= rem {
				combined = append(combined, b.fullBuf...)
			} else {
				combined = append(combined, b.fullBuf[:rem]...)
			}
		}
		b.fullBuf = combined
	}
	b.buf = append([]byte(s), b.buf...)
	if len(b.buf) > b.keepLimit {
		b.truncated = true
		drop := len(b.buf) - b.keepLimit
		for drop < len(b.buf) && !utf8.RuneStart(b.buf[drop]) {
			drop++
		}
		b.buf = b.buf[drop:]
	}
}

// FullText returns the captured full output when captureSpill was enabled.
func (b *boundedOutputBuffer) FullText() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.fullBuf) > 0 {
		return string(b.fullBuf)
	}
	return string(b.buf)
}

// AppendLine appends raw on a new line only if the buffer is non-empty and does not
// already end with a newline, avoiding a blank line before appended errors/annotations.
func (b *boundedOutputBuffer) AppendLine(raw string) {
	if raw == "" {
		return
	}
	b.mu.Lock()
	needNL := b.sawData && !b.endsWithNL
	b.mu.Unlock()
	if needNL {
		b.Append("\n" + raw)
		return
	}
	b.Append(raw)
}

// TailText returns the current bounded tail text for live updates without altering state.
func (b *boundedOutputBuffer) TailText() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.buf) <= b.maxBytes {
		return string(b.buf)
	}
	start := len(b.buf) - b.maxBytes
	for start < len(b.buf) && !utf8.RuneStart(b.buf[start]) {
		start++
	}
	return string(b.buf[start:])
}

// Finalize trims the kept tail to <= maxBytes on a line boundary (or valid UTF-8
// rune boundary for a single oversized line) and returns the output text plus
// omp-compatible TruncationMeta when truncated.
func (b *boundedOutputBuffer) Finalize() (text string, meta *TruncationMeta) {
	b.mu.Lock()
	defer b.mu.Unlock()

	totalLines := b.totalLines
	if b.sawData && !b.endsWithNL {
		totalLines++
	}
	if !b.truncated && len(b.buf) <= b.maxBytes {
		return strings.TrimSpace(string(b.buf)), nil
	}

	raw := b.buf
	partialLine := false
	if len(raw) > b.maxBytes {
		start := len(raw) - b.maxBytes
		// Prefer starting cleanly on the next line boundary when one fits in the window.
		nlOffset := -1
		for i := start; i < len(raw); i++ {
			if raw[i] == '\n' {
				nlOffset = i + 1
				break
			}
		}
		if nlOffset >= 0 && nlOffset < len(raw) {
			raw = raw[nlOffset:]
		} else {
			// Single line larger than maxBytes: cut cleanly on a UTF-8 rune boundary.
			for start < len(raw) && !utf8.RuneStart(raw[start]) {
				start++
			}
			raw = raw[start:]
			partialLine = true
		}
	}

	out := strings.TrimSpace(string(raw))
	outLines := 0
	if out != "" {
		outLines = strings.Count(out, "\n") + 1
	}
	if totalLines < outLines {
		totalLines = outLines
	}
	var shown *LineRange
	if outLines > 0 && totalLines >= outLines {
		startLine := totalLines - outLines + 1
		shown = &LineRange{Start: startLine, End: totalLines}
	}
	return out, &TruncationMeta{
		Direction:   "tail",
		TruncatedBy: "bytes",
		TotalLines:  totalLines,
		TotalBytes:  b.totalBytes,
		OutputLines: outLines,
		OutputBytes: len(out),
		MaxBytes:    b.maxBytes,
		ShownRange:  shown,
		PartialLine: partialLine,
	}
}

// formatBytesHuman matches omp's formatBytes (packages/utils/src/format.ts).
func formatBytesHuman(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%dB", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(n)/(1024*1024))
	}
}

// formatSpillReference formats an artifact reference matching omp's
// formatFullOutputReference (packages/tui/src/tools/output-meta.ts:174-179).
// Accepts either a full URL (`artifact://...`, `local://...`) or a bare id.
func formatSpillReference(artifactRef string) string {
	ref := strings.TrimSpace(artifactRef)
	if ref == "" {
		return ""
	}
	if !strings.Contains(ref, "://") {
		ref = "artifact://" + ref
	}
	return "Read " + ref + " for full output"
}

// normalizeArtifactID extracts the stored ArtifactID for TruncationMeta.
func normalizeArtifactID(artifactRef string) string {
	ref := strings.TrimSpace(artifactRef)
	if ref == "" {
		return ""
	}
	if rest, ok := strings.CutPrefix(ref, "local://"); ok {
		if id, hasLog := strings.CutSuffix(rest, ".log"); hasLog && id != "" {
			return id
		}
		return rest
	}
	if rest, ok := strings.CutPrefix(ref, "artifact://"); ok {
		return rest
	}
	return ref
}

// formatTruncationNotice mirrors omp's formatOutputNotice / formatTruncationMetaNotice
// for tail truncation with an optional artifact spill link
// (packages/tui/src/tools/output-meta.ts:272-335).
func formatTruncationNotice(t *TruncationMeta, spillRef ...string) string {
	if t == nil {
		return ""
	}
	ref := ""
	rawRef := ""
	if len(spillRef) > 0 && strings.TrimSpace(spillRef[0]) != "" {
		rawRef = strings.TrimSpace(spillRef[0])
	} else if t.ArtifactID != "" {
		rawRef = t.ArtifactID
	}
	if rawRef != "" {
		if t.ArtifactElidedBytes > 0 {
			dispRef := rawRef
			if !strings.Contains(dispRef, "://") {
				dispRef = "artifact://" + dispRef
			}
			ref = fmt.Sprintf("Read %s for a %s head sample (%s was not saved)", dispRef, formatBytesHuman(t.TotalBytes-t.ArtifactElidedBytes), formatBytesHuman(t.ArtifactElidedBytes))
		} else {
			ref = formatSpillReference(rawRef)
		}
	}
	var body string
	if t.PartialLine {
		line := 1
		if t.ShownRange != nil {
			line = t.ShownRange.Start
		}
		body = fmt.Sprintf("Showing line %d (partial, %s of %s) of %d", line, formatBytesHuman(t.OutputBytes), formatBytesHuman(t.TotalBytes), t.TotalLines)
	} else {
		if t.ShownRange != nil && t.ShownRange.End >= t.ShownRange.Start {
			body = fmt.Sprintf("Showing lines %d-%d of %d", t.ShownRange.Start, t.ShownRange.End, t.TotalLines)
		} else {
			body = fmt.Sprintf("Showing %d of %d lines", t.OutputLines, t.TotalLines)
		}
		if t.TruncatedBy == "bytes" {
			mb := t.MaxBytes
			if mb == 0 {
				mb = t.OutputBytes
			}
			body += fmt.Sprintf(" (%s limit)", formatBytesHuman(mb))
		}
	}
	if ref != "" {
		body += ". " + ref
	}
	return "\n\n[" + body + "]"
}
