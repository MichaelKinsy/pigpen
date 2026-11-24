package shell

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"
)

// upstream: packages/tui/src/tools/streaming-output.ts, packages/tui/src/tools/output-meta.ts, packages/utils/src/format.ts

const (
	// DefaultSpillThreshold is omp's DEFAULT_MAX_BYTES (50 KB inline budget).
	DefaultSpillThreshold = 50 * 1024
	// DefaultHeadBytes is omp's tools.artifactHeadBytes default (20 KB).
	DefaultHeadBytes = 20 * 1024
	// DefaultMaxColumns is omp's tools.outputMaxColumns default (768 bytes per line).
	DefaultMaxColumns = 768
	// DefaultArtifactMaxBytes is omp's ARTIFACT_DEFAULT_MAX_BYTES (16 MiB).
	DefaultArtifactMaxBytes = 16 * 1024 * 1024
	// DefaultArtifactHeadBytes is omp's ARTIFACT_DEFAULT_HEAD_BYTES (3 MiB).
	DefaultArtifactHeadBytes = 3 * 1024 * 1024
	// WallTimeNoticeMinMS is omp's WALL_TIME_NOTICE_MIN_MS (1000 ms).
	WallTimeNoticeMinMS = 1000
)

// ArtifactStore allocates and retains spilled output artifacts so callers and a
// later artifact:// reader can retrieve full command output by ID.
type ArtifactStore struct {
	mu      sync.RWMutex
	dir     string
	nextID  atomic.Uint64
	entries map[string]string // artifactID -> file path
}

// NewArtifactStore creates an ArtifactStore rooted at dir. If dir is empty, a
// temporary directory is created lazily on first allocation.
func NewArtifactStore(dir string) *ArtifactStore {
	return &ArtifactStore{
		dir:     dir,
		entries: make(map[string]string),
	}
}

// Allocate creates a new artifact ID and backing file path.
func (s *ArtifactStore) Allocate(prefix string) (id string, path string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir == "" {
		tmp, mkErr := os.MkdirTemp("", "omp-artifacts-*")
		if mkErr != nil {
			return "", "", mkErr
		}
		s.dir = tmp
	} else if mkErr := os.MkdirAll(s.dir, 0o755); mkErr != nil {
		return "", "", mkErr
	}
	n := s.nextID.Add(1)
	id = fmt.Sprintf("%d", n)
	if prefix == "" {
		prefix = "bash"
	}
	path = filepath.Join(s.dir, fmt.Sprintf("%s-%s.log", prefix, id))
	s.entries[id] = path
	return id, path, nil
}

// Path returns the on-disk path for an allocated artifact ID, if known.
func (s *ArtifactStore) Path(id string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.entries[id]
	return p, ok
}

// OutputSummary mirrors omp's OutputSummary (streaming-output.ts).
type OutputSummary struct {
	Output               string
	Truncated            bool
	TotalLines           int
	TotalBytes           int
	OutputLines          int
	OutputBytes          int
	ElidedBytes          int
	ElidedLines          int
	ColumnDroppedBytes   int
	ColumnTruncatedLines int
	ColumnMax            int
	ArtifactID           string
	ArtifactPath         string
	ArtifactElidedBytes  int
	ArtifactError        string
}

// OutputSinkOptions configures an OutputSink.
type OutputSinkOptions struct {
	SpillThreshold int
	HeadBytes      int
	MaxColumns     int
	ArtifactStore  *ArtifactStore
	ArtifactPrefix string
	OnPreview      func(text string)
}

// OutputSink captures bounded inline command output (head + rolling tail with
// middle elision when HeadBytes > 0, or tail-only when HeadBytes == 0) and
// spills the full raw stream to an artifact file once output overflows.
// upstream: streaming-output.ts:OutputSink
type OutputSink struct {
	mu sync.Mutex

	spillThreshold int
	headLimit      int
	tailLimit      int
	maxColumns     int
	store          *ArtifactStore
	artifactPrefix string
	onPreview      func(text string)

	headBuf       []byte
	headLines     int
	headClosed    bool
	tailBuf       []byte
	totalBytes    int
	totalLines    int // newline count
	sawData       bool
	truncated     bool
	pendingCR     bool
	colLineBytes  int
	colEllipsis   bool
	colDropped    int
	colTruncLines int

	rawBuffer    []byte // buffered before spill so artifact receives complete stream
	artifactFile *os.File
	artifactID   string
	artifactPath string
	artifactErr  string
	finalized    bool
}

// NewOutputSink constructs an OutputSink with omp's defaults for unset limits.
func NewOutputSink(opts OutputSinkOptions) *OutputSink {
	spill := opts.SpillThreshold
	if spill <= 0 {
		spill = DefaultSpillThreshold
	}
	head := opts.HeadBytes
	if head < 0 {
		head = 0
	}
	if head > spill/2 {
		head = spill / 2
	}
	tail := spill - head
	prefix := opts.ArtifactPrefix
	if prefix == "" {
		prefix = "bash"
	}
	return &OutputSink{
		spillThreshold: spill,
		headLimit:      head,
		tailLimit:      tail,
		maxColumns:     opts.MaxColumns,
		store:          opts.ArtifactStore,
		artifactPrefix: prefix,
		onPreview:      opts.OnPreview,
	}
}

// Write implements io.Writer so OutputSink can attach directly to exec.Cmd pipes.
func (s *OutputSink) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	s.Push(string(p))
	return len(p), nil
}

// Push appends raw output text, normalizing CRLF/CR and updating inline and
// spilled buffers.
func (s *OutputSink) Push(chunk string) {
	if chunk == "" {
		return
	}
	s.mu.Lock()
	if s.finalized {
		s.mu.Unlock()
		return
	}
	normalized := s.normalizeChunkLocked(chunk)
	if normalized == "" {
		s.mu.Unlock()
		return
	}
	s.sawData = true
	s.totalBytes += len(normalized)
	s.totalLines += strings.Count(normalized, "\n")

	// Write raw normalized bytes to artifact if already spilling, or keep in
	// rawBuffer until spill threshold is crossed.
	if s.artifactFile != nil {
		if s.artifactErr == "" {
			if _, err := s.artifactFile.WriteString(normalized); err != nil {
				s.artifactErr = "write"
			}
		}
	} else if s.store != nil {
		s.rawBuffer = append(s.rawBuffer, normalized...)
	}

	// Apply optional per-line column cap to inline stream.
	inlineChunk := normalized
	if s.maxColumns > 0 {
		inlineChunk = s.applyColumnCapLocked(normalized)
	}

	s.appendInlineLocked(inlineChunk)

	if s.truncated && s.artifactFile == nil && s.store != nil && s.artifactErr == "" {
		s.openArtifactLocked()
	}

	var preview string
	cb := s.onPreview
	if cb != nil {
		preview, _, _, _, _ = s.composeBodyLocked()
	}
	s.mu.Unlock()

	if cb != nil {
		cb(preview)
	}
}

func (s *OutputSink) normalizeChunkLocked(chunk string) string {
	if s.pendingCR {
		s.pendingCR = false
		if chunk[0] == '\n' {
			chunk = "\n" + chunk[1:]
		} else {
			chunk = "\n" + chunk
		}
	}
	if strings.HasSuffix(chunk, "\r") {
		s.pendingCR = true
		chunk = chunk[:len(chunk)-1]
	}
	if !strings.ContainsRune(chunk, '\r') {
		return chunk
	}
	chunk = strings.ReplaceAll(chunk, "\r\n", "\n")
	return strings.ReplaceAll(chunk, "\r", "\n")
}

func (s *OutputSink) applyColumnCapLocked(chunk string) string {
	var out strings.Builder
	out.Grow(len(chunk))
	for i := 0; i < len(chunk); {
		if chunk[i] == '\n' {
			out.WriteByte('\n')
			s.colLineBytes = 0
			s.colEllipsis = false
			i++
			continue
		}
		_, size := utf8.DecodeRuneInString(chunk[i:])
		if size <= 0 {
			size = 1
		}
		if s.colLineBytes+size <= s.maxColumns {
			out.WriteString(chunk[i : i+size])
			s.colLineBytes += size
		} else {
			if !s.colEllipsis {
				out.WriteString("…")
				s.colEllipsis = true
				s.colTruncLines++
			}
			s.colDropped += size
		}
		i += size
	}
	return out.String()
}

func (s *OutputSink) appendInlineLocked(chunk string) {
	if chunk == "" {
		return
	}
	if s.headLimit > 0 && !s.headClosed {
		rem := s.headLimit - len(s.headBuf)
		if len(chunk) <= rem {
			s.headBuf = append(s.headBuf, chunk...)
			s.headLines += strings.Count(chunk, "\n")
			if len(s.headBuf) >= s.headLimit {
				s.headClosed = true
			}
			return
		}
		cut := utf8BoundaryBackward(chunk, rem)
		if cut > 0 {
			s.headBuf = append(s.headBuf, chunk[:cut]...)
			s.headLines += strings.Count(chunk[:cut], "\n")
		}
		s.headClosed = true
		chunk = chunk[cut:]
	}

	s.tailBuf = append(s.tailBuf, chunk...)
	if len(s.tailBuf) > s.tailLimit {
		s.truncated = true
		trimStart := len(s.tailBuf) - s.tailLimit
		trimStart = utf8BoundaryForward(s.tailBuf, trimStart)
		s.tailBuf = append(s.tailBuf[:0], s.tailBuf[trimStart:]...)
	}
}

func (s *OutputSink) openArtifactLocked() {
	id, path, err := s.store.Allocate(s.artifactPrefix)
	if err != nil {
		s.artifactErr = "open"
		s.rawBuffer = nil
		return
	}
	f, err := os.Create(path)
	if err != nil {
		s.artifactErr = "open"
		s.rawBuffer = nil
		return
	}
	s.artifactID = id
	s.artifactPath = path
	s.artifactFile = f
	if len(s.rawBuffer) > 0 {
		if _, wErr := f.Write(s.rawBuffer); wErr != nil {
			s.artifactErr = "write"
		}
		s.rawBuffer = nil
	}
}

func (s *OutputSink) composeBodyLocked() (body string, outputBytes, outputLines, elidedBytes, elidedLines int) {
	totalLines := 0
	if s.sawData {
		totalLines = s.totalLines + 1
	}
	headBytes := len(s.headBuf)
	tailBytes := len(s.tailBuf)
	headStr := string(s.headBuf)
	tailStr := string(s.tailBuf)

	headLines := s.headLines
	if headBytes > 0 && !strings.HasSuffix(headStr, "\n") {
		headLines++
	}
	tailLines := 0
	if tailBytes > 0 {
		tailLines = strings.Count(tailStr, "\n") + 1
	}

	effectiveTotalBytes := max(0, s.totalBytes-s.colDropped)
	if headBytes > 0 && s.truncated && effectiveTotalBytes > headBytes+tailBytes {
		elidedBytes = max(0, effectiveTotalBytes-headBytes-tailBytes)
		elidedLines = max(0, totalLines-headLines-tailLines)
		marker := formatMiddleElisionMarker(elidedLines, elidedBytes)
		headSep := "\n"
		if strings.HasSuffix(headStr, "\n") {
			headSep = ""
		}
		tailSep := "\n"
		if strings.HasPrefix(tailStr, "\n") {
			tailSep = ""
		}
		body = headStr + headSep + marker + tailSep + tailStr
		outputBytes = len(body)
		outputLines = headLines + 1 + tailLines
		return body, outputBytes, outputLines, elidedBytes, elidedLines
	}
	if headBytes > 0 {
		body = headStr + tailStr
		outputBytes = len(body)
		if len(body) > 0 {
			outputLines = strings.Count(body, "\n") + 1
		}
		return body, outputBytes, outputLines, 0, 0
	}
	body = tailStr
	outputBytes = tailBytes
	outputLines = tailLines
	return body, outputBytes, outputLines, 0, 0
}

// Preview returns the current inline view without finalizing the sink.
func (s *OutputSink) Preview() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, _, _, _, _ := s.composeBodyLocked()
	return body
}

// Dump finalizes the sink and returns the OutputSummary. If notice is non-empty,
// `[<notice>]\n` is prepended to the output body, matching omp's OutputSink.dump.
func (s *OutputSink) Dump(notice string) OutputSummary {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.pendingCR {
		s.pendingCR = false
		s.sawData = true
		s.totalBytes++
		s.totalLines++
		if s.artifactFile != nil && s.artifactErr == "" {
			if _, err := s.artifactFile.WriteString("\n"); err != nil {
				s.artifactErr = "write"
			}
		}
		s.appendInlineLocked("\n")
	}

	s.closeArtifactLocked()
	body, outputBytes, outputLines, elidedBytes, elidedLines := s.composeBodyLocked()

	totalLines := 0
	if s.sawData {
		totalLines = s.totalLines + 1
	}

	if notice != "" {
		noticeLine := "[" + notice + "]\n"
		body = noticeLine + body
		outputBytes = len(body)
		outputLines = strings.Count(body, "\n") + 1
	}

	artifactID := ""
	artifactPath := ""
	if s.truncated && s.artifactErr == "" {
		artifactID = s.artifactID
		artifactPath = s.artifactPath
	}

	return OutputSummary{
		Output:               body,
		Truncated:            s.truncated,
		TotalLines:           totalLines,
		TotalBytes:           s.totalBytes,
		OutputLines:          outputLines,
		OutputBytes:          outputBytes,
		ElidedBytes:          elidedBytes,
		ElidedLines:          elidedLines,
		ColumnDroppedBytes:   s.colDropped,
		ColumnTruncatedLines: s.colTruncLines,
		ColumnMax:            s.maxColumns,
		ArtifactID:           artifactID,
		ArtifactPath:         artifactPath,
		ArtifactError:        s.artifactErr,
	}
}

// Dispose closes any open spill file on error/abort paths.
func (s *OutputSink) Dispose() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeArtifactLocked()
}

func (s *OutputSink) closeArtifactLocked() {
	s.finalized = true
	s.rawBuffer = nil
	if s.artifactFile != nil {
		if err := s.artifactFile.Close(); err != nil && s.artifactErr == "" {
			s.artifactErr = "end"
		}
		s.artifactFile = nil
	}
}

func utf8BoundaryBackward(s string, maxBytes int) int {
	if maxBytes <= 0 {
		return 0
	}
	if maxBytes >= len(s) {
		return len(s)
	}
	i := maxBytes
	for i > 0 && (s[i]&0xc0) == 0x80 {
		i--
	}
	return i
}

func utf8BoundaryForward(b []byte, pos int) int {
	if pos < 0 {
		return 0
	}
	i := pos
	for i < len(b) && (b[i]&0xc0) == 0x80 {
		i++
	}
	return i
}

// formatBytes mirrors packages/utils/src/format.ts:formatBytes.
func formatBytes(bytes int) string {
	if bytes < 1024 {
		return fmt.Sprintf("%dB", bytes)
	}
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	}
	if bytes < 1024*1024*1024 {
		return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
	}
	return fmt.Sprintf("%.1fGB", float64(bytes)/(1024*1024*1024))
}

// formatMiddleElisionMarker mirrors packages/tui/src/tools/streaming-output.ts:551.
func formatMiddleElisionMarker(elidedLines, elidedBytes int) string {
	if elidedLines <= 1 {
		return fmt.Sprintf("[…%dB elided…]", elidedBytes)
	}
	return fmt.Sprintf("[…%dln elided…]", elidedLines)
}

// formatFullOutputReference mirrors packages/tui/src/tools/output-meta.ts:151.
func formatFullOutputReference(artifactID string, artifactElidedBytes int) string {
	if artifactElidedBytes > 0 {
		return fmt.Sprintf("Read artifact://%s for a head/tail sample of the output; %s from its middle was not saved", artifactID, formatBytes(artifactElidedBytes))
	}
	return fmt.Sprintf("Read artifact://%s for full output", artifactID)
}

// formatArtifactErrorNotice mirrors packages/tui/src/tools/output-meta.ts:296.
func formatArtifactErrorNotice(errKind string) string {
	return fmt.Sprintf("Full output was not saved completely (artifact %s failed)", errKind)
}

// formatSummaryNotice renders the omp `[...]` output-meta footer for an OutputSummary.
// upstream: packages/tui/src/tools/output-meta.ts:formatOutputNotice
func formatSummaryNotice(summary OutputSummary) string {
	var parts []string

	if summary.Truncated {
		var notice string
		artifactRef := ""
		if summary.ArtifactID != "" && summary.ArtifactError == "" {
			artifactRef = formatFullOutputReference(summary.ArtifactID, summary.ArtifactElidedBytes)
		}
		if summary.ElidedBytes > 0 {
			elidedLines := summary.ElidedLines
			if elidedLines <= 0 {
				elidedLines = max(0, summary.TotalLines-summary.OutputLines)
			}
			keptLines := max(0, summary.OutputLines-1)
			headLines := (keptLines + 1) / 2
			tailLines := keptLines - headLines
			if headLines > 0 && tailLines > 0 && summary.TotalLines > 1 {
				tailStart := summary.TotalLines - tailLines + 1
				plural := "s"
				if elidedLines == 1 {
					plural = ""
				}
				notice = fmt.Sprintf("Showing lines 1-%d and %d-%d of %d; %d middle line%s (%s) elided",
					headLines, tailStart, summary.TotalLines, summary.TotalLines, elidedLines, plural, formatBytes(summary.ElidedBytes))
			} else {
				plural := "s"
				if summary.TotalLines == 1 {
					plural = ""
				}
				notice = fmt.Sprintf("Showing head and tail bytes of %d line%s; %s elided",
					summary.TotalLines, plural, formatBytes(summary.ElidedBytes))
			}
			if artifactRef != "" {
				notice += ". " + artifactRef
			}
		} else {
			shownStart := max(1, summary.TotalLines-summary.OutputLines+1)
			shownEnd := summary.TotalLines
			if shownEnd >= shownStart && summary.TotalLines > 0 {
				notice = fmt.Sprintf("Showing lines %d-%d of %d", shownStart, shownEnd, summary.TotalLines)
			} else {
				notice = fmt.Sprintf("Showing %d of %d lines", summary.OutputLines, summary.TotalLines)
			}
			if summary.OutputBytes < summary.TotalBytes {
				notice += fmt.Sprintf(" (%s limit)", formatBytes(summary.OutputBytes))
			}
			if artifactRef != "" {
				notice += ". " + artifactRef
			}
		}
		parts = append(parts, notice)
	}

	if summary.ArtifactError != "" {
		parts = append(parts, formatArtifactErrorNotice(summary.ArtifactError))
	}

	if summary.ColumnMax > 0 && summary.ColumnTruncatedLines > 0 {
		colNotice := fmt.Sprintf("Some lines truncated to %d bytes", summary.ColumnMax)
		if summary.ArtifactID != "" && summary.ArtifactError == "" {
			colNotice += ". " + formatFullOutputReference(summary.ArtifactID, summary.ArtifactElidedBytes)
		}
		parts = append(parts, colNotice)
	}

	if len(parts) == 0 {
		return ""
	}
	return "\n\n[" + strings.Join(parts, ". ") + "]"
}

// formatWallTimeNotice mirrors packages/tui/src/tools/bash.ts:218.
func formatWallTimeNotice(wallTimeMS float64) string {
	return fmt.Sprintf("Wall time: %.2f seconds", wallTimeMS/1000.0)
}

// formatExitCodeNotice mirrors packages/tui/src/tools/bash.ts:223.
func formatExitCodeNotice(exitCode int) string {
	return fmt.Sprintf("Command exited with code %d", exitCode)
}

// formatBackgroundNotice mirrors packages/tui/src/tools/bash.ts:41.
func formatBackgroundNotice(jobID string, timeoutSec *int) string {
	deadline := " (no deadline)"
	if timeoutSec != nil {
		deadline = fmt.Sprintf(" (killed once it has run %ds in total; `timeout: 0` disables the deadline)", *timeoutSec)
	}
	return fmt.Sprintf(
		"Backgrounded as job %s%s; its output is injected into the conversation as a follow-up the moment it finishes. Do NOT poll for it (no `sleep`, `ps`, `pgrep`, `top`, `pidwait`, log tailing): every poll is a wasted turn. Do other work, or end your reply and wait to be woken.",
		jobID, deadline,
	)
}
