package shell

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// upstream: packages/tui/src/tools/streaming-output.ts, packages/tui/src/tools/output-meta.ts, packages/utils/src/spill-url.ts, packages/utils/src/format.ts

const (
	// DefaultSpillThreshold is omp's DEFAULT_MAX_BYTES (50 KiB inline budget).
	DefaultSpillThreshold = 50 * 1024
	// DefaultHeadBytes is omp's tools.artifactHeadBytes default (20 KiB).
	DefaultHeadBytes = 20 * 1024
	// DefaultTailBytes is omp's tools.artifactTailBytes default (20 KiB).
	DefaultTailBytes = 20 * 1024
	// InlineCapSlackBytes is omp's INLINE_CAP_SLACK_BYTES (2 KiB notice slack
	// added to tools.artifactSpillThreshold in resolveInlineByteCapBudget,
	// packages/coding-agent/src/tools/output-meta.ts:537,553).
	InlineCapSlackBytes = 2 * 1024
	// DefaultMaxColumns is omp's tools.outputMaxColumns default (768 bytes per line).
	DefaultMaxColumns = 768
	// DefaultArtifactMaxBytes is omp's ARTIFACT_DEFAULT_MAX_BYTES (16 MiB).
	DefaultArtifactMaxBytes = 16 * 1024 * 1024
	// DefaultArtifactHeadBytes is omp's ARTIFACT_DEFAULT_HEAD_BYTES (3 MiB).
	DefaultArtifactHeadBytes = 3 * 1024 * 1024
	// DefaultServiceLogMaxBytes caps a service's on-disk log file (4 MiB).
	DefaultServiceLogMaxBytes = 4 * 1024 * 1024
	// WallTimeNoticeMinMS is omp's WALL_TIME_NOTICE_MIN_MS (1000 ms).
	WallTimeNoticeMinMS = 1000
)

// SpillWriter writes large command outputs into the session's `local://` root
// (supplied by the URL lane's `local://` handler). When nil, OutputSink writes
// spilled output to a temp file under ArtifactStore and formats `local://<id>.log`.
//
// Deprecated for direct `os.WriteFile` wiring: prefer wiring `LocalSpillRoot`
// (or `WriteLocalSpill(root, data)` via `SpillAllocator`) so bash spills and
// `jobs.SpillFunc` share one per-session 0-based allocator that scans existing
// `<N>.log` files on first use and creates files exclusively (`O_CREATE|O_EXCL`).
var SpillWriter func(ctx sdk.Context, name string, data []byte) (url string, err error)

// LocalSpillRoot, when set by the integrator (e.g. `urls.LocalRoot`), returns
// the session's `local://` root directory. When assigned, both `shell` bash
// spills and `shell.WriteSessionSpill` (assigned to `jobs.SpillFunc`) share one
// per-session allocator rooted at that directory: ids start at 0, seed past
// every existing `<N>.log` on first use so resumed sessions never reuse an id,
// and create files exclusively (`O_WRONLY|O_CREATE|O_EXCL`) so existing files
// are never overwritten (upstream: session/artifacts.ts:87,128-156).
var LocalSpillRoot func(ctx sdk.Context) (string, error)

// SpillAllocator creates an exclusive `<N>.log` spill file under a session's
// `local://` root directory (starting at 0, seeded past any existing `<N>.log`
// on first use, bumping past concurrent EEXIST). One allocator per root dir is
// maintained process-wide so bash spills and `jobs.SpillFunc` share one id space.
// upstream: packages/coding-agent/src/session/artifacts.ts:86-163
type spillDirAllocator struct {
	mu      sync.Mutex
	dir     string
	scanned bool
	nextID  uint64
}

var (
	spillAllocMu    sync.Mutex
	spillAllocByDir = map[string]*spillDirAllocator{}
	spillFileNameRE = regexp.MustCompile(`^(\d+)\.log$`)
)

func spillIdOfFileName(name string) (uint64, bool) {
	m := spillFileNameRE.FindStringSubmatch(name)
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseUint(m[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func getSpillDirAllocator(dir string) *spillDirAllocator {
	clean := filepath.Clean(dir)
	spillAllocMu.Lock()
	defer spillAllocMu.Unlock()
	a := spillAllocByDir[clean]
	if a == nil {
		a = &spillDirAllocator{dir: clean}
		spillAllocByDir[clean] = a
	}
	return a
}

func (a *spillDirAllocator) ensureScannedLocked() error {
	if err := os.MkdirAll(a.dir, 0o755); err != nil {
		return err
	}
	if a.scanned {
		return nil
	}
	entries, err := os.ReadDir(a.dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var hasAny bool
	var maxID uint64
	for _, ent := range entries {
		if id, ok := spillIdOfFileName(ent.Name()); ok {
			if !hasAny || id > maxID {
				maxID = id
				hasAny = true
			}
		}
	}
	if hasAny {
		a.nextID = maxID + 1
	}
	a.scanned = true
	return nil
}

// writeExclusive claims the next free `<N>.log` in `a.dir` using O_CREATE|O_EXCL,
// writes data to it, and returns (id, "local://<id>.log", fullPath, nil).
func (a *spillDirAllocator) writeExclusive(data []byte) (id, url, fullPath string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureScannedLocked(); err != nil {
		return "", "", "", err
	}
	for {
		cur := a.nextID
		a.nextID++
		id = strconv.FormatUint(cur, 10)
		fullPath = filepath.Join(a.dir, spillFileName(id))
		f, openErr := os.OpenFile(fullPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if openErr != nil {
			if errors.Is(openErr, os.ErrExist) {
				continue
			}
			return "", "", "", openErr
		}
		_, wErr := f.Write(data)
		cErr := f.Close()
		if wErr != nil {
			_ = os.Remove(fullPath)
			return "", "", "", wErr
		}
		if cErr != nil {
			_ = os.Remove(fullPath)
			return "", "", "", cErr
		}
		return id, spillURL(id), fullPath, nil
	}
}

// WriteLocalSpillInRoot allocates the next free 0-based `<N>.log` under `root`
// (seeding past every existing `<N>.log` on first use and opening exclusively
// with O_CREATE|O_EXCL so resumed sessions or pre-existing files are never
// overwritten) and returns its `local://<N>.log` URL.
// Integrator wiring: pass the session's `local://` root (from `urls.LocalRoot(ctx)`),
// or wire `shell.LocalSpillRoot = urls.LocalRoot` and `jobs.SpillFunc = shell.WriteSessionSpill`.
// upstream: packages/coding-agent/src/session/artifacts.ts:86-163
func WriteLocalSpillInRoot(root string, data []byte) (url string, err error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("empty local spill root")
	}
	_, u, _, err := getSpillDirAllocator(root).writeExclusive(data)
	return u, err
}

// WriteSessionSpill writes `data` to the next free 0-based `<N>.log` in the
// session's `local://` root (resolved via `LocalSpillRoot`) using the same
// per-session exclusive allocator as bash spills, and returns `local://<N>.log`.
// Integrator wiring: assign `shell.LocalSpillRoot = urls.LocalRoot` and
// `jobs.SpillFunc = shell.WriteSessionSpill` (replacing `writeLocalSpill` and
// `async-<N>.log` in `wiring.go`).
// upstream: packages/coding-agent/src/session/agent-session.ts:4136-4140
func WriteSessionSpill(ctx sdk.Context, data []byte) (url string, err error) {
	if LocalSpillRoot != nil {
		root, err := LocalSpillRoot(ctx)
		if err != nil {
			return "", err
		}
		return WriteLocalSpillInRoot(root, data)
	}
	// Safe fallback when LocalSpillRoot is unassigned: write via ArtifactStore.
	if SpillWriter != nil {
		id := defaultRuntime.Artifacts.NextID()
		return SpillWriter(ctx, spillFileName(id), data)
	}
	sessionID := sessionKeyFromContext(ctx)
	id, path, err := defaultRuntime.Artifacts.Allocate(sessionID)
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	_, wErr := f.Write(data)
	cErr := f.Close()
	if wErr != nil {
		_ = os.Remove(path)
		return "", wErr
	}
	if cErr != nil {
		_ = os.Remove(path)
		return "", cErr
	}
	return spillURL(id), nil
}

// WriteLocalSpillExclusive creates `<name>` under `root` exclusively
// (`O_WRONLY|O_CREATE|O_EXCL`) and returns `local://<name>`. Returns `os.ErrExist`
// if the file already exists. Provided for callers that still wire `SpillWriter`.
func WriteLocalSpillExclusive(root, name string, data []byte) (string, error) {
	cleaned := strings.TrimLeft(filepath.ToSlash(strings.TrimSpace(name)), "/")
	if cleaned == "" {
		return "", fmt.Errorf("empty spill artifact name")
	}
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("empty local spill root")
	}
	target := filepath.Join(root, filepath.FromSlash(cleaned))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	_, wErr := f.Write(data)
	cErr := f.Close()
	if wErr != nil {
		_ = os.Remove(target)
		return "", wErr
	}
	if cErr != nil {
		_ = os.Remove(target)
		return "", cErr
	}
	return "local://" + cleaned, nil
}

// spillURL returns omp's canonical spill URL `local://<id>.log`.
// upstream: packages/utils/src/spill-url.ts:17
func spillURL(id string) string {
	if strings.Contains(id, "://") {
		return id
	}
	return fmt.Sprintf("local://%s.log", id)
}

// spillFileName returns omp's spill file name `<id>.log`.
// upstream: packages/utils/src/spill-url.ts:39
func spillFileName(id string) string {
	return fmt.Sprintf("%s.log", id)
}

// ArtifactStore allocates and retains spilled output artifacts (`<N>.log`,
// 0-based matching omp's ArtifactManager) scoped by session so session cleanup
// removes its temp files cleanly.
// upstream: packages/coding-agent/src/session/artifacts.ts:86-156
type ArtifactStore struct {
	mu        sync.RWMutex
	dir       string
	dirOwned  bool
	scanned   bool
	nextID    atomic.Uint64
	entries   map[string]string              // artifactID -> file path
	bySession map[string]map[string]struct{} // sessionID -> set of artifactIDs
}

// NewArtifactStore creates an ArtifactStore rooted at dir. If dir is empty, a
// temporary directory is created lazily on first allocation.
func NewArtifactStore(dir string) *ArtifactStore {
	return &ArtifactStore{
		dir:       dir,
		entries:   make(map[string]string),
		bySession: make(map[string]map[string]struct{}),
	}
}

// NextID reserves the next 0-based numeric spill ID without creating a file.
// upstream: packages/coding-agent/src/session/artifacts.ts:87,146
func (s *ArtifactStore) NextID() string {
	return fmt.Sprintf("%d", s.nextID.Add(1)-1)
}

func (s *ArtifactStore) scanDirLocked() {
	if s.scanned || s.dir == "" {
		return
	}
	entries, err := os.ReadDir(s.dir)
	if err == nil {
		var hasAny bool
		var maxID uint64
		for _, ent := range entries {
			if id, ok := spillIdOfFileName(ent.Name()); ok {
				if !hasAny || id > maxID {
					maxID = id
					hasAny = true
				}
			}
		}
		if hasAny {
			for {
				cur := s.nextID.Load()
				if cur > maxID {
					break
				}
				if s.nextID.CompareAndSwap(cur, maxID+1) {
					break
				}
			}
		}
	}
	s.scanned = true
}

// Allocate creates a new 0-based artifact ID and backing file path (`<id>.log`)
// for sessionID, scanning existing `<N>.log` files in s.dir on first use and
// reserving the file exclusively (`O_CREATE|O_EXCL`) so existing spills are
// never clobbered.
// upstream: packages/coding-agent/src/session/artifacts.ts:128-156
func (s *ArtifactStore) Allocate(sessionID string) (id string, path string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir == "" {
		tmp, mkErr := os.MkdirTemp("", "omp-artifacts-*")
		if mkErr != nil {
			return "", "", mkErr
		}
		s.dir = tmp
		s.dirOwned = true
	} else if mkErr := os.MkdirAll(s.dir, 0o755); mkErr != nil {
		return "", "", mkErr
	}
	s.scanDirLocked()
	for {
		id = fmt.Sprintf("%d", s.nextID.Add(1)-1)
		path = filepath.Join(s.dir, spillFileName(id))
		f, openErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if openErr != nil {
			if errors.Is(openErr, os.ErrExist) {
				continue
			}
			return "", "", openErr
		}
		_ = f.Close()
		break
	}
	s.entries[id] = path
	if sessionID != "" {
		set := s.bySession[sessionID]
		if set == nil {
			set = make(map[string]struct{})
			s.bySession[sessionID] = set
		}
		set[id] = struct{}{}
	}
	return id, path, nil
}

// Path returns the on-disk path for an allocated artifact ID, if known.
func (s *ArtifactStore) Path(id string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.entries[id]
	return p, ok
}

// RemoveSession deletes all fallback temp artifact files allocated for sessionID.
func (s *ArtifactStore) RemoveSession(sessionID string) {
	if sessionID == "" {
		return
	}
	s.mu.Lock()
	set := s.bySession[sessionID]
	delete(s.bySession, sessionID)
	paths := make([]string, 0, len(set))
	for id := range set {
		if p, ok := s.entries[id]; ok {
			paths = append(paths, p)
			delete(s.entries, id)
		}
	}
	s.mu.Unlock()
	for _, p := range paths {
		_ = os.Remove(p)
	}
}

// DisposeAll deletes every artifact file (and temp dir if owned). Used by test cleanup.
func (s *ArtifactStore) DisposeAll() {
	s.mu.Lock()
	paths := make([]string, 0, len(s.entries))
	for _, p := range s.entries {
		paths = append(paths, p)
	}
	dir := ""
	if s.dirOwned {
		dir = s.dir
		s.dir = ""
		s.dirOwned = false
	}
	s.entries = make(map[string]string)
	s.bySession = make(map[string]map[string]struct{})
	s.mu.Unlock()
	for _, p := range paths {
		_ = os.Remove(p)
	}
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
}

// OutputSummary mirrors omp's OutputSummary (streaming-output.ts).
type OutputSummary struct {
	Output               string
	Truncated            bool
	TotalLines           int
	TotalBytes           int
	OutputLines          int
	OutputBytes          int
	HeadLines            int
	TailLines            int
	ElidedBytes          int
	ElidedLines          int
	ColumnDroppedBytes   int
	ColumnTruncatedLines int
	ColumnFirstCutLine   int
	ColumnLastCutLine    int
	ColumnMax            int
	ArtifactID           string
	ArtifactURL          string
	ArtifactPath         string
	ArtifactElidedBytes  int
	ArtifactError        string
}

// OutputSinkOptions configures an OutputSink.
type OutputSinkOptions struct {
	SpillThreshold    int
	HeadBytes         int
	TailBytes         int // 0 => spill - head (default OutputSink split); >0 caps rolling tail when smaller than spill-head
	MaxColumns        int
	ArtifactMaxBytes  int // 0 => DefaultArtifactMaxBytes; <0 => unbounded
	ArtifactHeadBytes int // 0 => DefaultArtifactHeadBytes
	ArtifactStore     *ArtifactStore
	SessionID         string
	SDKContext        sdk.Context
	OnPreview         func(text string)
	OnChunk           func(chunk string)
}

// OutputSink captures bounded inline command output (head + rolling tail with
// middle elision when HeadBytes > 0, or tail-only when HeadBytes == 0),
// sanitizes ANSI/control bytes before storing/spilling, and caps the spilled
// artifact to ArtifactMaxBytes (default 16 MiB: 3 MiB head + rolling tail with
// `[ARTIFACT TRUNCATED: …]` marker).
// upstream: packages/tui/src/tools/streaming-output.ts:OutputSink
type OutputSink struct {
	mu sync.Mutex

	spillThreshold    int
	headLimit         int
	tailLimit         int
	maxColumns        int
	artifactMaxBytes  int
	artifactHeadLimit int
	artifactTailLimit int
	store             *ArtifactStore
	sessionID         string
	sdkCtx            sdk.Context
	onPreview         func(text string)
	onChunk           func(chunk string)

	headBuf       []byte
	headLines     int
	headClosed    bool
	tailBuf       []byte
	totalBytes    int
	totalLines    int // newline count
	sawData       bool
	truncated     bool
	pendingCR     bool
	sanitizer     streamSanitizer
	colLineBytes  int
	colEllipsis   bool
	colDropped    int
	colTruncLines int
	colFirstLine  int
	colLastLine   int
	curLine       int // 1-indexed line number for column cap tracking

	// Bounded artifact capture (head written to file or memory + rolling tail).
	artHeadBuf      []byte // buffered before file open, or full head for SpillWriter
	artHeadWritten  int
	artHeadClosed   bool
	artTailBuf      []byte
	artTailIncoming int
	artElidedBytes  int
	artifactFile    *os.File
	artifactID      string
	artifactURL     string
	artifactPath    string
	artifactErr     string
	useWriter       bool
	finalized       bool
}

// NewOutputSink constructs an OutputSink with omp's defaults for unset limits.
// upstream: packages/tui/src/tools/streaming-output.ts:1010-1035
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
	if opts.TailBytes > 0 && opts.TailBytes < tail {
		tail = opts.TailBytes
	}

	artMax := opts.ArtifactMaxBytes
	if artMax == 0 {
		artMax = DefaultArtifactMaxBytes
	} else if artMax < 0 {
		artMax = 0 // unbounded
	}
	artHead := opts.ArtifactHeadBytes
	if artMax > 0 {
		if artHead <= 0 {
			artHead = DefaultArtifactHeadBytes
		}
		if artHead > artMax/2 {
			artHead = artMax / 2
		}
	} else {
		artHead = 0
	}
	artTail := 0
	if artMax > 0 {
		artTail = artMax - artHead
	}

	return &OutputSink{
		spillThreshold:    spill,
		headLimit:         head,
		tailLimit:         tail,
		maxColumns:        opts.MaxColumns,
		artifactMaxBytes:  artMax,
		artifactHeadLimit: artHead,
		artifactTailLimit: artTail,
		store:             opts.ArtifactStore,
		sessionID:         opts.SessionID,
		sdkCtx:            opts.SDKContext,
		onPreview:         opts.OnPreview,
		onChunk:           opts.OnChunk,
		curLine:           1,
		useWriter:         LocalSpillRoot != nil || SpillWriter != nil,
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

// Push appends raw output text, normalizing CRLF/CR, sanitizing ANSI escapes
// and control characters (`streaming-output.ts:1081`), and updating inline and
// bounded artifact buffers.
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
	clean := s.sanitizer.Feed(normalized)
	if clean == "" {
		s.mu.Unlock()
		return
	}
	s.ingestCleanLocked(clean)

	var preview string
	cbPreview := s.onPreview
	cbChunk := s.onChunk
	if cbPreview != nil {
		preview, _, _, _, _, _, _ = s.composeBodyLocked()
	}
	s.mu.Unlock()

	if cbChunk != nil {
		cbChunk(clean)
	}
	if cbPreview != nil {
		cbPreview(preview)
	}
}

func (s *OutputSink) ingestCleanLocked(clean string) {
	if clean == "" {
		return
	}
	s.sawData = true
	s.totalBytes += len(clean)
	s.totalLines += strings.Count(clean, "\n")

	// Feed bounded artifact capture.
	if s.store != nil || s.useWriter {
		s.pushArtifactBytesLocked(clean)
	}

	// Apply optional per-line column cap to inline stream.
	inlineChunk := clean
	if s.maxColumns > 0 {
		inlineChunk = s.applyColumnCapLocked(clean)
	}

	s.appendInlineLocked(inlineChunk)

	needsSpill := s.truncated || s.colTruncLines > 0
	if needsSpill && !s.useWriter && s.artifactFile == nil && s.store != nil && s.artifactErr == "" {
		s.openArtifactLocked()
	}
}

func (s *OutputSink) pushArtifactBytesLocked(chunk string) {
	if s.artifactErr != "" {
		return
	}
	if s.artifactMaxBytes == 0 {
		// Unbounded artifact mode.
		if s.artifactFile != nil {
			if _, err := s.artifactFile.WriteString(chunk); err != nil {
				s.artifactErr = "write"
			}
			s.artHeadWritten += len(chunk)
		} else {
			s.artHeadBuf = append(s.artHeadBuf, chunk...)
		}
		return
	}

	// Bounded artifact mode: fill head up to artifactHeadLimit, then feed
	// rolling tail ring up to artifactTailLimit.
	if !s.artHeadClosed {
		curHead := s.artHeadWritten + len(s.artHeadBuf)
		rem := s.artifactHeadLimit - curHead
		if len(chunk) <= rem {
			s.writeArtifactHeadLocked(chunk)
			if s.artHeadWritten+len(s.artHeadBuf) >= s.artifactHeadLimit {
				s.artHeadClosed = true
			}
			return
		}
		cut := utf8BoundaryBackward(chunk, rem)
		if cut > 0 {
			s.writeArtifactHeadLocked(chunk[:cut])
		}
		s.artHeadClosed = true
		chunk = chunk[cut:]
	}

	if chunk == "" {
		return
	}
	s.artTailIncoming += len(chunk)
	if s.artifactTailLimit <= 0 {
		return
	}
	s.artTailBuf = append(s.artTailBuf, chunk...)
	if len(s.artTailBuf) > s.artifactTailLimit {
		trimStart := len(s.artTailBuf) - s.artifactTailLimit
		trimStart = utf8BoundaryForward(s.artTailBuf, trimStart)
		s.artTailBuf = append(s.artTailBuf[:0], s.artTailBuf[trimStart:]...)
	}
}

func (s *OutputSink) writeArtifactHeadLocked(chunk string) {
	if s.artifactFile != nil {
		if _, err := s.artifactFile.WriteString(chunk); err != nil {
			s.artifactErr = "write"
			return
		}
		s.artHeadWritten += len(chunk)
	} else {
		s.artHeadBuf = append(s.artHeadBuf, chunk...)
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

// applyColumnCapLocked applies omp's per-line UTF-8 byte cap
// (`packages/tui/src/tools/streaming-output.ts:1169-1225`).
// On the first overflow of a line, reserves 3 bytes for `"…"` (`headRoom = max(0, remaining - 3)`)
// and drops all remaining bytes until the next `\n`.
func (s *OutputSink) applyColumnCapLocked(chunk string) string {
	const ellipsis = "…"
	const ellipsisBytes = len(ellipsis) // 3 bytes in UTF-8
	var out strings.Builder
	out.Grow(len(chunk))
	cursor := 0
	for cursor < len(chunk) {
		nlRel := strings.IndexByte(chunk[cursor:], '\n')
		segEnd := len(chunk)
		if nlRel >= 0 {
			segEnd = cursor + nlRel
		}
		if segEnd > cursor {
			segment := chunk[cursor:segEnd]
			if s.colEllipsis {
				s.colDropped += len(segment)
			} else {
				remaining := s.maxColumns - s.colLineBytes
				if len(segment) <= remaining {
					out.WriteString(segment)
					s.colLineBytes += len(segment)
				} else {
					headRoom := max(0, remaining-ellipsisBytes)
					keptBytes := 0
					if headRoom > 0 {
						cut := utf8BoundaryBackward(segment, headRoom)
						if cut > 0 {
							out.WriteString(segment[:cut])
							keptBytes = cut
						}
					}
					out.WriteString(ellipsis)
					s.colDropped += len(segment) - keptBytes
					s.colTruncLines++
					if s.colFirstLine == 0 {
						s.colFirstLine = s.curLine
					}
					s.colLastLine = s.curLine
					s.colLineBytes += keptBytes + ellipsisBytes
					s.colEllipsis = true
				}
			}
		}
		if nlRel < 0 {
			break
		}
		out.WriteByte('\n')
		s.colLineBytes = 0
		s.colEllipsis = false
		s.curLine++
		cursor = segEnd + 1
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
	id, path, err := s.store.Allocate(s.sessionID)
	if err != nil {
		s.artifactErr = "open"
		s.artHeadBuf = nil
		s.artTailBuf = nil
		return
	}
	// Allocate already claimed `path` exclusively with O_CREATE|O_EXCL.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		s.artifactErr = "open"
		s.artHeadBuf = nil
		s.artTailBuf = nil
		return
	}
	s.artifactURL = spillURL(id)
	s.artifactPath = path
	s.artifactFile = f
	if len(s.artHeadBuf) > 0 {
		if _, wErr := f.Write(s.artHeadBuf); wErr != nil {
			s.artifactErr = "write"
		} else {
			s.artHeadWritten += len(s.artHeadBuf)
		}
		s.artHeadBuf = nil
	}
}

// buildArtifactTailNotice formats omp's `[ARTIFACT TRUNCATED: …]` marker
// injected between head and tail when the artifact size cap dropped bytes.
// upstream: streaming-output.ts:1561-1571
func (s *OutputSink) buildArtifactTailLocked() (notice string, tail []byte) {
	if s.artifactMaxBytes == 0 {
		return "", nil
	}
	tailBytes := len(s.artTailBuf)
	droppedBytes := max(0, s.artTailIncoming-tailBytes)
	if tailBytes == 0 && droppedBytes == 0 {
		return "", nil
	}
	if droppedBytes > 0 {
		s.artElidedBytes = droppedBytes
		headWritten := s.artHeadWritten + len(s.artHeadBuf)
		totalCapped := headWritten + s.artTailIncoming
		headSep := ""
		if headWritten > 0 {
			headSep = "\n"
		}
		tailSep := ""
		if tailBytes > 0 && s.artTailBuf[0] != '\n' {
			tailSep = "\n"
		}
		notice = fmt.Sprintf(
			"%s[ARTIFACT TRUNCATED: kept first %s + last %s of %s; %s elided from the middle]%s",
			headSep, formatBytes(headWritten), formatBytes(tailBytes), formatBytes(totalCapped), formatBytes(droppedBytes), tailSep,
		)
	}
	return notice, s.artTailBuf
}

func (s *OutputSink) composeBodyLocked() (body string, outputBytes, outputLines, headLines, tailLines, elidedBytes, elidedLines int) {
	totalLines := 0
	if s.sawData {
		totalLines = s.totalLines + 1
	}
	headBytes := len(s.headBuf)
	tailBytes := len(s.tailBuf)
	headStr := string(s.headBuf)
	tailStr := string(s.tailBuf)

	headLines = s.headLines
	if headBytes > 0 && !strings.HasSuffix(headStr, "\n") {
		headLines++
	}
	if tailBytes > 0 {
		tailLines = strings.Count(tailStr, "\n") + 1
	}

	effectiveTotalBytes := max(0, s.totalBytes-s.colDropped)
	if headBytes > 0 && s.truncated && effectiveTotalBytes > headBytes+tailBytes {
		// Cut head and tail windows back to whole lines (a window that is one
		// line fragment stays as is), matching omp's streaming-output.ts:1613-1636.
		headCut := -1
		if !strings.HasSuffix(headStr, "\n") {
			headCut = strings.LastIndexByte(headStr, '\n')
		}
		head := headStr
		if headCut >= 0 {
			head = headStr[:headCut+1]
		}
		tailCut := strings.IndexByte(tailStr, '\n')
		tail := tailStr
		if tailCut >= 0 && tailCut < len(tailStr)-1 {
			tail = tailStr[tailCut+1:]
		}
		keptHeadBytes := headBytes
		keptHeadLines := headLines
		if head != headStr {
			keptHeadBytes = len(head)
			keptHeadLines = strings.Count(head, "\n")
		}
		keptTailBytes := tailBytes
		keptTailLines := tailLines
		if tail != tailStr {
			keptTailBytes = len(tail)
			keptTailLines = strings.Count(tail, "\n") + 1
		}
		elidedBytes = max(0, effectiveTotalBytes-keptHeadBytes-keptTailBytes)
		elidedLines = max(0, totalLines-keptHeadLines-keptTailLines)
		marker := formatMiddleElisionMarker(elidedLines, elidedBytes)
		headSep := "\n"
		if strings.HasSuffix(head, "\n") {
			headSep = ""
		}
		tailSep := "\n"
		if strings.HasPrefix(tail, "\n") {
			tailSep = ""
		}
		body = head + headSep + marker + tailSep + tail
		outputBytes = keptHeadBytes + len(marker) + keptTailBytes + len(headSep) + len(tailSep)
		outputLines = keptHeadLines + 1 + keptTailLines
		return body, outputBytes, outputLines, keptHeadLines, keptTailLines, elidedBytes, elidedLines
	}
	if headBytes > 0 {
		body = headStr + tailStr
		outputBytes = headBytes + tailBytes
		if len(body) > 0 {
			outputLines = strings.Count(body, "\n") + 1
		}
		return body, outputBytes, outputLines, headLines, tailLines, 0, 0
	}
	body = tailStr
	outputBytes = tailBytes
	outputLines = tailLines
	return body, outputBytes, outputLines, 0, tailLines, 0, 0
}

// Preview returns the current inline view without finalizing the sink.
func (s *OutputSink) Preview() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, _, _, _, _, _, _ := s.composeBodyLocked()
	return body
}

// Dump finalizes the sink and returns the OutputSummary. If notice is non-empty,
// `[<notice>]\n` is prepended to the output body, matching omp's OutputSink.dump.
func (s *OutputSink) Dump(notice string) OutputSummary {
	s.mu.Lock()
	if s.pendingCR {
		s.pendingCR = false
		if clean := s.sanitizer.Feed("\n"); clean != "" {
			s.ingestCleanLocked(clean)
		}
	}
	if tail := s.sanitizer.Flush(); tail != "" {
		s.ingestCleanLocked(tail)
	}
	needsSpill := s.truncated || s.colTruncLines > 0
	var writerData []byte
	var writerID string
	localRootFn := LocalSpillRoot
	writer := SpillWriter
	if needsSpill && s.useWriter && (localRootFn != nil || writer != nil) && s.artifactErr == "" {
		truncNotice, tailBytes := s.buildArtifactTailLocked()
		totalLen := len(s.artHeadBuf) + len(truncNotice) + len(tailBytes)
		writerData = make([]byte, 0, totalLen)
		writerData = append(writerData, s.artHeadBuf...)
		writerData = append(writerData, truncNotice...)
		writerData = append(writerData, tailBytes...)
		if localRootFn == nil && writer != nil {
			if s.store != nil {
				writerID = s.store.NextID()
			} else {
				writerID = fmt.Sprintf("%d", fallbackSpillID.Add(1)-1)
			}
		}
	}

	s.closeArtifactLocked()
	body, outputBytes, outputLines, headLines, tailLines, elidedBytes, elidedLines := s.composeBodyLocked()

	totalLines := 0
	if s.sawData {
		totalLines = s.totalLines + 1
	}

	if notice != "" {
		// Upstream (streaming-output.ts:1655-1675): noticeLine is prefixed onto
		// `output` without altering `outputLines`/`outputBytes` so range math in
		// `output-meta.ts:232-237` never counts notice lines as head lines.
		noticeLine := "[" + notice + "]\n"
		body = noticeLine + body
	}

	sdkCtx := s.sdkCtx
	artifactID := s.artifactID
	artifactURL := s.artifactURL
	artifactPath := s.artifactPath
	artifactElided := s.artElidedBytes
	artifactErr := s.artifactErr
	truncated := s.truncated
	totalBytes := s.totalBytes
	colDropped := s.colDropped
	colTruncLines := s.colTruncLines
	colFirst := s.colFirstLine
	colLast := s.colLastLine
	maxCols := s.maxColumns
	s.mu.Unlock()

	if needsSpill && s.useWriter && artifactErr == "" {
		if localRootFn != nil {
			root, rootErr := localRootFn(sdkCtx)
			if rootErr != nil {
				artifactErr = "open"
				artifactID = ""
				artifactURL = ""
			} else {
				id, u, fullPath, wErr := getSpillDirAllocator(root).writeExclusive(writerData)
				if wErr != nil {
					artifactErr = "write"
					artifactID = ""
					artifactURL = ""
				} else {
					artifactID = id
					artifactURL = u
					artifactPath = fullPath
				}
			}
		} else if writer != nil && writerID != "" {
			u, err := writer(sdkCtx, spillFileName(writerID), writerData)
			if err != nil {
				artifactErr = "write"
				artifactID = ""
				artifactURL = ""
			} else {
				artifactID = writerID
				if u != "" {
					artifactURL = u
				} else {
					artifactURL = spillURL(writerID)
				}
			}
		}
	}
	if !(truncated || colTruncLines > 0) || artifactErr != "" {
		artifactID = ""
		artifactURL = ""
		artifactPath = ""
		artifactElided = 0
	}

	return OutputSummary{
		Output:               body,
		Truncated:            truncated,
		TotalLines:           totalLines,
		TotalBytes:           totalBytes,
		OutputLines:          outputLines,
		OutputBytes:          outputBytes,
		HeadLines:            headLines,
		TailLines:            tailLines,
		ElidedBytes:          elidedBytes,
		ElidedLines:          elidedLines,
		ColumnDroppedBytes:   colDropped,
		ColumnTruncatedLines: colTruncLines,
		ColumnFirstCutLine:   colFirst,
		ColumnLastCutLine:    colLast,
		ColumnMax:            maxCols,
		ArtifactID:           artifactID,
		ArtifactURL:          artifactURL,
		ArtifactPath:         artifactPath,
		ArtifactElidedBytes:  artifactElided,
		ArtifactError:        artifactErr,
	}
}

var fallbackSpillID atomic.Uint64

// Dispose closes any open spill file on error/abort paths.
func (s *OutputSink) Dispose() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeArtifactLocked()
}

func (s *OutputSink) closeArtifactLocked() {
	s.finalized = true
	if s.artifactFile != nil {
		if s.artifactErr == "" {
			truncNotice, tailBytes := s.buildArtifactTailLocked()
			if truncNotice != "" {
				if _, err := s.artifactFile.WriteString(truncNotice); err != nil {
					s.artifactErr = "flush"
				}
			}
			if len(tailBytes) > 0 && s.artifactErr == "" {
				if _, err := s.artifactFile.Write(tailBytes); err != nil {
					s.artifactErr = "flush"
				}
			}
		}
		if err := s.artifactFile.Close(); err != nil && s.artifactErr == "" {
			s.artifactErr = "end"
		}
		s.artifactFile = nil
	}
	s.artHeadBuf = nil
	s.artTailBuf = nil
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

// formatMiddleElisionMarker mirrors packages/tui/src/tools/streaming-output.ts.
func formatMiddleElisionMarker(elidedLines, elidedBytes int) string {
	if elidedLines <= 1 {
		return fmt.Sprintf("[…%dB elided…]", elidedBytes)
	}
	return fmt.Sprintf("[…%dln elided…]", elidedLines)
}

// formatFullOutputReference mirrors packages/tui/src/tools/output-meta.ts:174.
func formatFullOutputReference(artifactURL string, artifactElidedBytes int) string {
	ref := spillURL(artifactURL)
	if artifactElidedBytes > 0 {
		return fmt.Sprintf("Read %s for a head/tail sample of the output; %s from its middle was not saved", ref, formatBytes(artifactElidedBytes))
	}
	return fmt.Sprintf("Read %s for full output", ref)
}

// formatArtifactErrorNotice mirrors packages/tui/src/tools/output-meta.ts:346.
func formatArtifactErrorNotice(errKind string) string {
	return fmt.Sprintf("Full output was not saved completely (artifact %s failed)", errKind)
}

var middleElisionMarkerRE = regexp.MustCompile(`^\[…\d+(?:ln|B) elided…\]$`)

// computeMiddleRanges mirrors omp's `output-meta.ts:224-252` marker-based head/tail
// range computation, dropping the empty trailing element after a trailing newline.
func computeMiddleRanges(summary OutputSummary) (lastLine, headLines, tailLines, elidedLines int) {
	elidedLines = summary.ElidedLines
	if elidedLines <= 0 {
		elidedLines = max(0, summary.TotalLines-summary.OutputLines)
	}
	shown := strings.Split(summary.Output, "\n")
	markerAt := -1
	for i, line := range shown {
		if middleElisionMarkerRE.MatchString(line) {
			markerAt = i
			break
		}
	}
	trailing := 0
	if markerAt >= 0 && len(shown) > markerAt+1 && shown[len(shown)-1] == "" {
		trailing = 1
	}
	lastLine = summary.TotalLines - trailing
	keptLines := max(0, summary.OutputLines-1) // -1 for marker line
	if markerAt >= 0 {
		tailLines = len(shown) - markerAt - 1 - trailing
		headLines = max(0, summary.OutputLines-(len(shown)-markerAt))
	} else {
		headLines = (keptLines + 1) / 2
		tailLines = keptLines - headLines
	}
	return lastLine, headLines, tailLines, elidedLines
}

// formatWithCommas formats non-negative integers with ASCII thousands separators
// matching JS `Number.prototype.toLocaleString("en-US")`.
func formatWithCommas(n int) string {
	s := strconv.Itoa(n)
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	}
	if len(s) <= 3 {
		if neg {
			return "-" + s
		}
		return s
	}
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	rem := len(s) % 3
	if rem == 0 {
		rem = 3
	}
	b.WriteString(s[:rem])
	for i := rem; i < len(s); i += 3 {
		b.WriteByte(',')
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// buildOutputMeta builds omp's `details.meta` object matching
// `packages/coding-agent/src/tools/output-meta.ts:truncationFromSummary`.
func buildOutputMeta(summary OutputSummary) map[string]any {
	meta := map[string]any{}
	if summary.ArtifactError != "" {
		meta["artifactError"] = summary.ArtifactError
	}
	if summary.ColumnMax > 0 && summary.ColumnTruncatedLines > 0 {
		col := map[string]any{
			"maxColumn": summary.ColumnMax,
			"unit":      "bytes",
		}
		if summary.ArtifactID != "" && summary.ArtifactError == "" {
			col["artifactId"] = summary.ArtifactID
			if summary.ArtifactElidedBytes > 0 {
				col["artifactElidedBytes"] = summary.ArtifactElidedBytes
			}
		}
		if summary.ColumnFirstCutLine > 0 {
			col["lines"] = map[string]any{
				"first": summary.ColumnFirstCutLine,
				"last":  summary.ColumnLastCutLine,
			}
		}
		meta["limits"] = map[string]any{
			"columnTruncated": col,
		}
	}
	if summary.Truncated {
		var trunc map[string]any
		if summary.ElidedBytes > 0 {
			lastLine, headLines, tailLines, elidedLines := computeMiddleRanges(summary)
			trunc = map[string]any{
				"direction":   "middle",
				"truncatedBy": "middle",
				"totalLines":  lastLine,
				"totalBytes":  summary.TotalBytes,
				"outputLines": summary.OutputLines,
				"outputBytes": summary.OutputBytes,
				"elidedBytes": summary.ElidedBytes,
				"elidedLines": elidedLines,
			}
			if headLines > 0 {
				trunc["headRange"] = map[string]any{"start": 1, "end": headLines}
			}
			if tailLines > 0 && lastLine >= tailLines {
				trunc["tailRange"] = map[string]any{"start": lastLine - tailLines + 1, "end": lastLine}
			}
		} else {
			truncatedBy := "bytes"
			if summary.OutputBytes >= summary.TotalBytes && summary.OutputLines < summary.TotalLines {
				truncatedBy = "lines"
			}
			shownStart := max(1, summary.TotalLines-summary.OutputLines+1)
			trunc = map[string]any{
				"direction":   "tail",
				"truncatedBy": truncatedBy,
				"totalLines":  summary.TotalLines,
				"totalBytes":  summary.TotalBytes,
				"outputLines": summary.OutputLines,
				"outputBytes": summary.OutputBytes,
				"shownRange":  map[string]any{"start": shownStart, "end": summary.TotalLines},
			}
		}
		if summary.ArtifactID != "" && summary.ArtifactError == "" {
			trunc["artifactId"] = summary.ArtifactID
			if summary.ArtifactElidedBytes > 0 {
				trunc["artifactElidedBytes"] = summary.ArtifactElidedBytes
			}
		}
		meta["truncation"] = trunc
	}
	if len(meta) == 0 {
		return nil
	}
	return meta
}

// formatSummaryNotice renders the omp `[...]` output-meta footer for an OutputSummary.
// upstream: packages/tui/src/tools/output-meta.ts:formatOutputNotice
func formatSummaryNotice(summary OutputSummary) string {
	var parts []string

	refTarget := summary.ArtifactURL
	if refTarget == "" && summary.ArtifactID != "" {
		refTarget = spillURL(summary.ArtifactID)
	}

	if summary.Truncated {
		var notice string
		artifactRef := ""
		if refTarget != "" && summary.ArtifactError == "" {
			artifactRef = formatFullOutputReference(refTarget, summary.ArtifactElidedBytes)
		}
		if summary.ElidedBytes > 0 {
			lastLine, headLines, tailLines, elidedLines := computeMiddleRanges(summary)
			if headLines > 0 && tailLines > 0 && lastLine >= tailLines {
				tailStart := lastLine - tailLines + 1
				plural := "s"
				if elidedLines == 1 {
					plural = ""
				}
				notice = fmt.Sprintf("Showing lines 1-%d and %d-%d of %d; %s middle line%s (%s) elided",
					headLines, tailStart, lastLine, lastLine, formatWithCommas(elidedLines), plural, formatBytes(summary.ElidedBytes))
			} else {
				plural := "s"
				if lastLine == 1 {
					plural = ""
				}
				notice = fmt.Sprintf("Showing head and tail bytes of %s line%s; %s elided",
					formatWithCommas(lastLine), plural, formatBytes(summary.ElidedBytes))
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
		if summary.ColumnFirstCutLine > 0 && refTarget != "" && summary.ArtifactError == "" && summary.ArtifactElidedBytes == 0 {
			colNotice += fmt.Sprintf(". Use %s:raw:%d-%d to read line %d whole", refTarget, summary.ColumnFirstCutLine, summary.ColumnFirstCutLine, summary.ColumnFirstCutLine)
			if summary.ColumnLastCutLine > summary.ColumnFirstCutLine {
				colNotice += fmt.Sprintf("; cut lines run to line %d, each read the same way", summary.ColumnLastCutLine)
			}
		} else if refTarget != "" && summary.ArtifactError == "" {
			colNotice += ". " + formatFullOutputReference(refTarget, summary.ArtifactElidedBytes)
		}
		parts = append(parts, colNotice)
	}

	if len(parts) == 0 {
		return ""
	}
	return "\n\n[" + strings.Join(parts, ". ") + "]"
}

// formatWallTimeNotice mirrors packages/tui/src/tools/bash.ts:247.
func formatWallTimeNotice(wallTimeMS float64) string {
	return fmt.Sprintf("Wall time: %.2f seconds", wallTimeMS/1000.0)
}

// formatExitCodeNotice mirrors packages/tui/src/tools/bash.ts:252.
func formatExitCodeNotice(exitCode int) string {
	return fmt.Sprintf("Command exited with code %d", exitCode)
}

// formatBackgroundNotice mirrors packages/tui/src/tools/bash.ts:69.
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

// kbToBytes converts omp's KB-denominated output limit settings
// (`tools.artifactSpillThreshold`, `tools.artifactHeadBytes`,
// `tools.artifactTailBytes`) into exact UTF-8 byte counts matching omp's
// `getSpillConfig` (`packages/coding-agent/src/tools/output-meta.ts:511-515`:
// `value * 1024`, where fractional KB such as `2.5` yields `2560`).
func kbToBytes(kb float64) int {
	if kb <= 0 || kb != kb {
		return 0
	}
	return int(kb * 1024)
}

// resolveInlineByteCapBudget mirrors omp's `resolveInlineByteCapBudget`
// (`packages/coding-agent/src/tools/output-meta.ts:552-554`):
// `tools.artifactSpillThreshold * 1024 + 2 KiB` notice slack.
func resolveInlineByteCapBudget(spillThresholdBytes int) int {
	if spillThresholdBytes <= 0 {
		spillThresholdBytes = DefaultSpillThreshold
	}
	return spillThresholdBytes + InlineCapSlackBytes
}

// enforceInlineByteCap ports omp's final-defense `enforceInlineByteCap`
// (`packages/tui/src/tools/streaming-output.ts:762-780`): no-op when `text`
// fits within `maxBytes` (spill threshold + 2 KiB slack); otherwise keeps
// ~60% head and ~25% tail on line boundaries with `[…<N>B elided…]` and an
// optional `[raw output: local://<id>.log]` footer.
func enforceInlineByteCap(text string, maxBytes int, artifactRef string) string {
	if maxBytes <= 0 || len(text) <= maxBytes {
		return text
	}
	headBudget := int(float64(maxBytes) * 0.6)
	tailBudget := int(float64(maxBytes) * 0.25)
	headCut := utf8BoundaryBackward(text, headBudget)
	head := text[:headCut]
	if idx := strings.LastIndexByte(head, '\n'); idx > 0 {
		head = head[:idx]
	}
	tailStart := len(text) - tailBudget
	if tailStart < 0 {
		tailStart = 0
	}
	tailStart = utf8BoundaryForward([]byte(text), tailStart)
	tail := text[tailStart:]
	if idx := strings.IndexByte(tail, '\n'); idx >= 0 && idx < len(tail)-1 {
		tail = tail[idx+1:]
	}
	elidedBytes := max(0, len(text)-len(head)-len(tail))
	composed := fmt.Sprintf("%s\n[…%dB elided…]\n%s", head, elidedBytes, tail)
	if artifactRef != "" {
		sep := "\n"
		if strings.HasSuffix(composed, "\n") {
			sep = ""
		}
		composed += fmt.Sprintf("%s[raw output: %s]", sep, spillURL(artifactRef))
	}
	return composed
}
