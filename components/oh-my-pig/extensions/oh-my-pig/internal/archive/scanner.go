package archive

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/dirs"
)

// upstream: packages/coding-agent/src/session/session-listing.ts,
//           packages/coding-agent/src/session/transcript-index.ts,
//           packages/coding-agent/src/archive/archive.ts

// SessionDirsOverride optionally overrides the root session directories scanned
// by archive operations (primarily for deterministic tests). When nil,
// defaultSessionDirs scans PiG's `<agentDir>/sessions` plus `~/.omp/agent/sessions`
// when distinct and existing.
var SessionDirsOverride func() []string

// Bounded scanning limits to keep large transcripts and huge session trees safe and fast.
const (
	// maxScanLineBytes caps any single JSONL line read during full-file scans (8 MiB).
	// Lines exceeding this cap are drained to the next newline and skipped so a single
	// massive tool-result line (e.g. a base64 image) never OOMs the extension.
	maxScanLineBytes = 8 * 1024 * 1024
	// maxSearchFiles bounds how many session files a single full-text search scans.
	maxSearchFiles = 2000
	// maxHistoryScanFiles bounds how many session files prompts/recaps scan.
	maxHistoryScanFiles = 2000
	// headerPrefixBytes mirrors upstream SESSION_LIST_PREFIX_BYTES (4096) for quick header probing,
	// expanded slightly to 16 KiB so long session headers + initial messages fit in one read.
	headerPrefixBytes = 16 * 1024
	// maxPromptSnippetInHeader caps the first prompt text kept on SessionInfo.
	maxPromptSnippetInHeader = 4096
)

// SessionStatus is the coarse lifecycle status of a session, derived from its last persisted message.
// upstream: packages/coding-agent/src/session/session-listing.ts:34
type SessionStatus string

const (
	StatusComplete    SessionStatus = "complete"
	StatusInterrupted SessionStatus = "interrupted"
	StatusAborted     SessionStatus = "aborted"
	StatusError       SessionStatus = "error"
	StatusPending     SessionStatus = "pending"
	StatusUnknown     SessionStatus = "unknown"
)

// sessionInfo holds scanned metadata for one session JSONL file.
// upstream: packages/coding-agent/src/session/session-listing.ts:36-62
type sessionInfo struct {
	Path              string
	ID                string
	Cwd               string
	Origin            string
	Title             string
	ParentSessionPath string
	Created           time.Time
	Modified          time.Time
	MessageCount      int
	AssistantTurns    int
	Size              int64
	FirstMessage      string
	Status            SessionStatus
	LatestRecap       string
}

type statedFile struct {
	Path      string
	RealPath  string
	ModTime   time.Time
	Size      int64
	BucketDir string
}

const (
	maxScanCacheEntries   = 4096
	maxBucketCacheEntries = 2048
)

var (
	scanCacheMu    sync.RWMutex
	scanCache      = map[string]cachedSessionScan{}
	scanCacheOrder []string

	bucketCwdMu    sync.RWMutex
	bucketCwdCache = map[string]string{}
	bucketCwdOrder []string
)

type cachedSessionScan struct {
	size  int64
	mtime int64
	info  *sessionInfo
}

// ClearCachesForTests clears internal stat-keyed file scan caches.
func ClearCachesForTests() {
	scanCacheMu.Lock()
	clear(scanCache)
	scanCacheOrder = nil
	scanCacheMu.Unlock()

	bucketCwdMu.Lock()
	clear(bucketCwdCache)
	bucketCwdOrder = nil
	bucketCwdMu.Unlock()
}

// resolveBucketCwd probes a single header-only prefix (16 KiB) for a session
// bucket directory and caches the resolved project cwd by bucket path.
// Returns ("", false) if the bucket has no readable session header with a cwd.
func resolveBucketCwd(bucketDir, sampleFile string) (string, bool) {
	if bucketDir == "" {
		return "", false
	}
	bucketCwdMu.RLock()
	if cwd, ok := bucketCwdCache[bucketDir]; ok {
		bucketCwdMu.RUnlock()
		return cwd, cwd != ""
	}
	bucketCwdMu.RUnlock()

	cwd := readHeaderCwdPrefix(sampleFile)
	if cwd == "" {
		return "", false
	}
	bucketCwdMu.Lock()
	if _, exists := bucketCwdCache[bucketDir]; !exists {
		if len(bucketCwdCache) >= maxBucketCacheEntries {
			evictN := len(bucketCwdOrder) / 2
			if evictN < 1 {
				evictN = 1
			}
			for _, k := range bucketCwdOrder[:evictN] {
				delete(bucketCwdCache, k)
			}
			bucketCwdOrder = append([]string(nil), bucketCwdOrder[evictN:]...)
		}
		bucketCwdOrder = append(bucketCwdOrder, bucketDir)
	}
	bucketCwdCache[bucketDir] = cwd
	bucketCwdMu.Unlock()
	return cwd, true
}

// readHeaderCwdPrefix reads at most headerPrefixBytes (16 KiB) from file to
// extract the session header's cwd without scanning the full file.
func readHeaderCwdPrefix(file string) string {
	f, err := os.Open(file)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, headerPrefixBytes)
	n, _ := f.Read(buf)
	if n <= 0 {
		return ""
	}
	buf = buf[:n]
	for len(buf) > 0 {
		idx := bytes.IndexByte(buf, '\n')
		var line []byte
		if idx >= 0 {
			line = bytes.TrimSpace(buf[:idx])
			buf = buf[idx+1:]
		} else {
			line = bytes.TrimSpace(buf)
			buf = nil
		}
		if len(line) == 0 {
			continue
		}
		var hdr struct {
			Type string `json:"type"`
			Cwd  string `json:"cwd"`
		}
		if err := json.Unmarshal(line, &hdr); err != nil {
			continue
		}
		if hdr.Type == "session" {
			return strings.TrimSpace(hdr.Cwd)
		}
	}
	return ""
}

// bucketMatchesProject reports whether f's bucket directory matches cwd using a
// cached header-only prefix probe, allowing callers to skip non-matching buckets
// before doing any full-file scan.
func bucketMatchesProject(f statedFile, cwd string) bool {
	if cwd == "" || f.BucketDir == "" {
		return true
	}
	bucketCwd, ok := resolveBucketCwd(f.BucketDir, f.Path)
	if !ok {
		return true // fall back to per-file metadata scan if header probe couldn't determine cwd
	}
	return sameProjectPath(bucketCwd, cwd)
}

// defaultSessionDirs returns the deduplicated existing session root directories:
// PiG's `<agentDir>/sessions` first, then `~/.omp/agent/sessions` when distinct.
func defaultSessionDirs() []string {
	if SessionDirsOverride != nil {
		raw := SessionDirsOverride()
		return dedupeDirs(raw)
	}
	var candidates []string
	if agentDir := resolvePigAgentDir(); agentDir != "" {
		candidates = append(candidates, filepath.Join(agentDir, "sessions"))
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates, filepath.Join(home, ".omp", "agent", "sessions"))
	}
	return dedupeDirs(candidates)
}

func resolvePigAgentDir() string {
	if env := strings.TrimSpace(os.Getenv("PIG_CODING_AGENT_DIR")); env != "" {
		return expandHome(env)
	}
	if dir, err := sdk.AgentDir(); err == nil && strings.TrimSpace(dir) != "" {
		return expandHome(strings.TrimSpace(dir))
	}
	if env := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); env != "" {
		return expandHome(env)
	}
	if pigHome := strings.TrimSpace(os.Getenv("PIG_HOME")); pigHome != "" {
		return filepath.Join(expandHome(pigHome), "agent")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".pig", "agent")
	}
	return ""
}

func dedupeDirs(candidates []string) []string {
	seen := make(map[string]struct{}, len(candidates))
	var out []string
	for _, dir := range candidates {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			abs = filepath.Clean(dir)
		}
		info, err := os.Stat(abs)
		if err != nil || !info.IsDir() {
			continue
		}
		canon := abs
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			canon = resolved
		}
		if _, dup := seen[canon]; dup {
			continue
		}
		seen[canon] = struct{}{}
		out = append(out, abs)
	}
	return out
}

// discoverAllSessionFiles finds top-level `<root>/<bucket>/*.jsonl` session files across
// all configured session roots (never descending into nested subagent directories),
// deduplicated by canonical realpath, sorted newest mtime first.
//
// upstream: packages/coding-agent/src/session/session-listing.ts:1453-1534
func discoverAllSessionFiles(ctx context.Context) []statedFile {
	roots := defaultSessionDirs()
	if len(roots) == 0 {
		return nil
	}
	seenReal := make(map[string]struct{}, 64)
	var files []statedFile

	for _, root := range roots {
		if ctx != nil && ctx.Err() != nil {
			break
		}
		rootCanon, err := filepath.EvalSymlinks(root)
		if err != nil {
			rootCanon = filepath.Clean(root)
		}
		buckets, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, bucket := range buckets {
			if ctx != nil && ctx.Err() != nil {
				break
			}
			// Skip non-directories and symlink buckets escaping the root.
			bucketPath := filepath.Join(root, bucket.Name())
			bInfo, err := os.Lstat(bucketPath)
			if err != nil {
				continue
			}
			if bInfo.Mode()&os.ModeSymlink != 0 {
				// Follow bucket symlink only if it resolves to a directory inside root.
				resolved, err := filepath.EvalSymlinks(bucketPath)
				if err != nil || !isSubpath(rootCanon, resolved) {
					continue
				}
				rInfo, err := os.Stat(resolved)
				if err != nil || !rInfo.IsDir() {
					continue
				}
			} else if !bInfo.IsDir() {
				continue
			}

			entries, err := os.ReadDir(bucketPath)
			if err != nil {
				continue
			}
			for _, ent := range entries {
				name := ent.Name()
				if !strings.HasSuffix(name, ".jsonl") {
					continue
				}
				full := filepath.Join(bucketPath, name)
				linfo, err := os.Lstat(full)
				if err != nil {
					continue
				}
				var realPath string
				var info fs.FileInfo
				if linfo.Mode()&os.ModeSymlink != 0 {
					resolved, err := filepath.EvalSymlinks(full)
					if err != nil || !isSubpath(rootCanon, resolved) {
						continue
					}
					st, err := os.Stat(resolved)
					if err != nil || !st.Mode().IsRegular() {
						continue
					}
					realPath = resolved
					info = st
				} else {
					if !linfo.Mode().IsRegular() {
						continue
					}
					realPath = filepath.Join(rootCanon, bucket.Name(), name)
					info = linfo
				}
				if _, dup := seenReal[realPath]; dup {
					continue
				}
				seenReal[realPath] = struct{}{}
				files = append(files, statedFile{
					Path:      full,
					RealPath:  realPath,
					ModTime:   info.ModTime().UTC(),
					Size:      info.Size(),
					BucketDir: bucketPath,
				})
			}
		}
	}

	sort.SliceStable(files, func(i, j int) bool {
		if !files[i].ModTime.Equal(files[j].ModTime) {
			return files[i].ModTime.After(files[j].ModTime)
		}
		return files[i].Path < files[j].Path
	})
	return files
}

func isSubpath(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

// sameProjectPath compares two project cwd paths, accounting for macOS /private
// aliasing and trailing path separators.
func sameProjectPath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	na := normalizeProjectPath(a)
	nb := normalizeProjectPath(b)
	return na == nb
}

func normalizeProjectPath(p string) string {
	if p == "" {
		return ""
	}
	p = filepath.Clean(p)
	p = dirs.StandardizeMacOSPath(p)
	return p
}

// sessionIdFromPath extracts the session ID embedded in `<timestamp>_<id>.jsonl`.
// upstream: packages/coding-agent/src/session/session-listing.ts:1407-1413
func sessionIDFromPath(file string) string {
	base := filepath.Base(file)
	if !strings.HasSuffix(base, ".jsonl") {
		return ""
	}
	stem := strings.TrimSuffix(base, ".jsonl")
	sep := strings.LastIndexByte(stem, '_')
	if sep <= 0 || sep+1 >= len(stem) {
		return ""
	}
	return stem[sep+1:]
}

// isValidIDPrefix restricts session id prefix queries to `[A-Za-z0-9_-]`.
// upstream: packages/coding-agent/src/session/session-listing.ts:1645
func isValidIDPrefix(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		c := s[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-'
		if !ok {
			return false
		}
	}
	return true
}

// findSessionFilesByPrefix locates all top-level session files whose session ID
// starts with idPrefix, deduplicated by session ID and canonical path.
//
// upstream: packages/coding-agent/src/session/session-listing.ts:1644-1649
func findSessionFilesByPrefix(ctx context.Context, idPrefix string) []string {
	if !isValidIDPrefix(idPrefix) {
		return nil
	}
	all := discoverAllSessionFiles(ctx)
	seenID := make(map[string]struct{})
	var matches []string
	for _, f := range all {
		if ctx != nil && ctx.Err() != nil {
			break
		}
		id := sessionIDFromPath(f.Path)
		if id == "" {
			// Fallback: peek header id for custom-named files like current.jsonl.
			if info := scanSessionMetadata(f.Path, f.Size, f.ModTime); info != nil {
				id = info.ID
			}
		}
		if id == "" || !strings.HasPrefix(id, idPrefix) {
			continue
		}
		if _, dup := seenID[id]; dup {
			continue
		}
		seenID[id] = struct{}{}
		matches = append(matches, f.Path)
	}
	return matches
}

// readSessionInfo reads a single session file's metadata and exact message count.
func readSessionInfo(file string) *sessionInfo {
	st, err := os.Stat(file)
	if err != nil || !st.Mode().IsRegular() {
		return nil
	}
	return scanSessionMetadata(file, st.Size(), st.ModTime().UTC())
}

// scanSessionMetadata scans header, title, message count, status, first prompt, and
// latest recap for a session file, memoized by (path, size, mtime).
//
// upstream: packages/coding-agent/src/session/session-listing.ts:435-558,
//
//	packages/coding-agent/src/archive/archive.ts:362-394
func scanSessionMetadata(file string, size int64, modTime time.Time) *sessionInfo {
	mtimeNano := modTime.UnixNano()
	scanCacheMu.RLock()
	if cached, ok := scanCache[file]; ok && cached.size == size && cached.mtime == mtimeNano {
		scanCacheMu.RUnlock()
		if cached.info == nil {
			return nil
		}
		cp := *cached.info
		return &cp
	}
	scanCacheMu.RUnlock()

	info := scanSessionFileUncached(file, size, modTime)

	scanCacheMu.Lock()
	// Bound cache size with half-eviction by insertion order so hot entries stay warm.
	_, existed := scanCache[file]
	if !existed {
		if len(scanCache) >= maxScanCacheEntries {
			evictN := len(scanCacheOrder) / 2
			if evictN < 1 {
				evictN = 1
			}
			for _, k := range scanCacheOrder[:evictN] {
				delete(scanCache, k)
			}
			scanCacheOrder = append([]string(nil), scanCacheOrder[evictN:]...)
		}
		scanCacheOrder = append(scanCacheOrder, file)
	}
	if info != nil {
		cp := *info
		scanCache[file] = cachedSessionScan{size: size, mtime: mtimeNano, info: &cp}
	} else {
		scanCache[file] = cachedSessionScan{size: size, mtime: mtimeNano, info: nil}
	}
	scanCacheMu.Unlock()

	return info
}

// sessionRecapEntry is one recap extracted from a session file.
type sessionRecapEntry struct {
	Text      string
	At        time.Time
	SessionID string
	Project   string
	Order     int
}

// sessionPromptEntry is one user prompt extracted from a session file or history log.
type sessionPromptEntry struct {
	Text      string
	At        time.Time
	Project   string
	SessionID string
	Order     int
}

// sessionTranscriptMsg is one user or assistant message extracted from a session file.
type sessionTranscriptMsg struct {
	Role  string // "user" | "assistant"
	Text  string
	At    time.Time
	Order int
}

// fullSessionScan holds all extracted metadata, recaps, prompts, and transcript messages
// from one streaming pass over a session JSONL file.
type fullSessionScan struct {
	Info     *sessionInfo
	Recaps   []sessionRecapEntry
	Prompts  []sessionPromptEntry
	Messages []sessionTranscriptMsg
}

func scanSessionFileUncached(file string, size int64, modTime time.Time) *sessionInfo {
	f, err := os.Open(file)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	full := scanSessionReader(context.Background(), f, file, size, modTime, false, false, false)
	if full == nil {
		return nil
	}
	return full.Info
}

// scanFullSession reads a session JSONL file once and extracts metadata plus optionally
// recaps, prompts, and user/assistant transcript messages.
func scanFullSession(ctx context.Context, file string, wantRecaps, wantPrompts, wantMessages bool) *fullSessionScan {
	if ctx != nil && ctx.Err() != nil {
		return nil
	}
	st, err := os.Stat(file)
	if err != nil || !st.Mode().IsRegular() {
		return nil
	}
	f, err := os.Open(file)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	return scanSessionReader(ctx, f, file, st.Size(), st.ModTime().UTC(), wantRecaps, wantPrompts, wantMessages)
}

// boundedLineReader reads lines from r, returning (line, skippedTooLong, err).
// Any line larger than maxBytes is drained to the next '\n' and skipped without OOM.
type boundedLineReader struct {
	r        *bufio.Reader
	maxBytes int
}

func newBoundedLineReader(r io.Reader, maxBytes int) *boundedLineReader {
	return &boundedLineReader{
		r:        bufio.NewReaderSize(r, 64*1024),
		maxBytes: maxBytes,
	}
}

func (lr *boundedLineReader) next() ([]byte, bool, error) {
	var buf []byte
	tooLong := false
	for {
		frag, isPrefix, err := lr.r.ReadLine()
		if len(frag) > 0 && !tooLong {
			if len(buf)+len(frag) > lr.maxBytes {
				tooLong = true
				buf = nil
			} else if !isPrefix && len(buf) == 0 {
				return frag, false, err
			} else {
				buf = append(buf, frag...)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) && (len(buf) > 0 || tooLong) {
				return buf, tooLong, nil
			}
			return buf, tooLong, err
		}
		if !isPrefix {
			return buf, tooLong, nil
		}
	}
}

type rawSessionLine struct {
	Type          string          `json:"type"`
	ID            string          `json:"id"`
	Timestamp     string          `json:"timestamp"`
	Cwd           string          `json:"cwd"`
	Origin        any             `json:"origin"`
	Title         string          `json:"title"`
	Name          string          `json:"name"`
	ParentSession string          `json:"parentSession"`
	CustomType    string          `json:"customType"`
	ShortSummary  string          `json:"shortSummary"`
	Summary       string          `json:"summary"`
	Recap         string          `json:"recap"`
	Text          string          `json:"text"`
	Content       json.RawMessage `json:"content"`
	Data          json.RawMessage `json:"data"`
	Message       *rawMessageObj  `json:"message"`
}

type rawMessageObj struct {
	Role       string          `json:"role"`
	CustomType string          `json:"customType"`
	StopReason string          `json:"stopReason"`
	Content    json.RawMessage `json:"content"`
	Timestamp  any             `json:"timestamp"`
}

func scanSessionReader(ctx context.Context, r io.Reader, file string, size int64, modTime time.Time, wantRecaps, wantPrompts, wantMessages bool) *fullSessionScan {
	lr := newBoundedLineReader(r, maxScanLineBytes)

	var (
		hasHeader      bool
		slotTitle      string
		slotTitleSet   bool
		headerID       string
		headerCwd      string
		headerOrigin   string
		headerTitle    string
		headerParent   string
		headerCreated  time.Time
		latestTitle    string
		shortSummary   string
		latestRecap    string
		firstMessage   string
		messageCount   int
		assistantTurns int
		lastStatus     = StatusUnknown
		lineNo         int
		recapOrder     int
		promptOrder    int
		msgOrder       int

		recaps   []sessionRecapEntry
		prompts  []sessionPromptEntry
		messages []sessionTranscriptMsg
	)

	for {
		if ctx != nil && lineNo&0x3f == 0 && ctx.Err() != nil {
			return nil
		}
		line, tooLong, err := lr.next()
		if err != nil && errors.Is(err, io.EOF) && len(line) == 0 {
			break
		}
		if !tooLong && len(line) > 0 {
			trimmed := bytes.TrimSpace(line)
			if len(trimmed) > 0 && trimmed[0] == '{' {
				lineNo++
				// Fast-path skip toolResult message lines when not needed? Wait:
				// we still need messageCount and lastStatus (toolResult -> interrupted).
				// Checking line type cheaply lets us avoid full JSON decode of huge toolResult content
				// by keeping Content as json.RawMessage!
				var rec rawSessionLine
				if json.Unmarshal(trimmed, &rec) == nil {
					switch rec.Type {
					case "title":
						if lineNo == 1 {
							slotTitleSet = true
							slotTitle = strings.TrimSpace(rec.Title)
						}
					case "session":
						if !hasHeader && rec.ID != "" {
							hasHeader = true
							headerID = rec.ID
							headerCwd = rec.Cwd
							headerOrigin = parseOrigin(rec.Origin)
							headerParent = rec.ParentSession
							if slotTitleSet {
								headerTitle = slotTitle
							} else {
								headerTitle = strings.TrimSpace(rec.Title)
							}
							if ts, ok := parseTimestamp(rec.Timestamp); ok {
								headerCreated = ts
							}
						}
					case "title_change":
						if t := strings.TrimSpace(rec.Title); t != "" {
							latestTitle = t
						}
					case "session_info":
						if t := strings.TrimSpace(rec.Name); t != "" {
							latestTitle = t
						}
					case "compaction":
						// upstream: session-listing.ts:481 — compaction.shortSummary can serve as title fallback
						// and is also surfaced as a persisted summary/recap in JSONL.
						if s := strings.TrimSpace(rec.ShortSummary); s != "" {
							shortSummary = s
						}
						recapText := strings.TrimSpace(rec.ShortSummary)
						if recapText == "" {
							recapText = strings.TrimSpace(rec.Recap)
						}
						if recapText != "" {
							latestRecap = recapText
							if wantRecaps {
								recapOrder++
								recaps = append(recaps, sessionRecapEntry{
									Text:      recapText,
									At:        entryTime(rec.Timestamp, modTime),
									SessionID: headerID,
									Project:   headerCwd,
									Order:     recapOrder,
								})
							}
						}
					case "recap", "idle_recap":
						recapText := firstNonEmpty(rec.Recap, rec.Text, extractTextContent(rec.Content, "\n"))
						if recapText != "" {
							latestRecap = recapText
							if wantRecaps {
								recapOrder++
								recaps = append(recaps, sessionRecapEntry{
									Text:      recapText,
									At:        entryTime(rec.Timestamp, modTime),
									SessionID: headerID,
									Project:   headerCwd,
									Order:     recapOrder,
								})
							}
						}
					case "custom", "custom_message":
						if isRecapCustomType(rec.CustomType) {
							recapText := extractRecapFromCustom(&rec)
							if recapText != "" {
								latestRecap = recapText
								if wantRecaps {
									recapOrder++
									recaps = append(recaps, sessionRecapEntry{
										Text:      recapText,
										At:        entryTime(rec.Timestamp, modTime),
										SessionID: headerID,
										Project:   headerCwd,
										Order:     recapOrder,
									})
								}
							}
						}
					case "message":
						if rec.Message != nil {
							messageCount++
							lastStatus = statusFromRawMessage(rec.Message)
							switch rec.Message.Role {
							case "assistant":
								assistantTurns++
								if wantMessages {
									txt := strings.TrimSpace(extractTextContent(rec.Message.Content, "\n"))
									if txt != "" {
										msgOrder++
										messages = append(messages, sessionTranscriptMsg{
											Role:  "assistant",
											Text:  txt,
											At:    messageTime(rec.Timestamp, rec.Message.Timestamp, modTime),
											Order: msgOrder,
										})
									}
								}
							case "user":
								if firstMessage == "" || wantPrompts || wantMessages {
									txt := strings.TrimSpace(extractTextContent(rec.Message.Content, "\n"))
									if txt != "" {
										if firstMessage == "" {
											oneLine := strings.TrimSpace(extractTextContent(rec.Message.Content, " "))
											if len(oneLine) > maxPromptSnippetInHeader {
												oneLine = oneLine[:maxPromptSnippetInHeader]
											}
											firstMessage = oneLine
										}
										at := messageTime(rec.Timestamp, rec.Message.Timestamp, modTime)
										if wantPrompts {
											promptOrder++
											prompts = append(prompts, sessionPromptEntry{
												Text:      normalizePrompt(txt),
												At:        at,
												Project:   headerCwd,
												SessionID: headerID,
												Order:     promptOrder,
											})
										}
										if wantMessages {
											msgOrder++
											messages = append(messages, sessionTranscriptMsg{
												Role:  "user",
												Text:  txt,
												At:    at,
												Order: msgOrder,
											})
										}
									}
								}
							case "custom":
								if isRecapCustomType(rec.Message.CustomType) {
									recapText := strings.TrimSpace(extractTextContent(rec.Message.Content, "\n"))
									if recapText != "" {
										latestRecap = recapText
										if wantRecaps {
											recapOrder++
											recaps = append(recaps, sessionRecapEntry{
												Text:      recapText,
												At:        messageTime(rec.Timestamp, rec.Message.Timestamp, modTime),
												SessionID: headerID,
												Project:   headerCwd,
												Order:     recapOrder,
											})
										}
									}
								}
							}
						}
					}
				}
			}
		}
		if err != nil {
			break
		}
	}

	if !hasHeader {
		return nil
	}

	title := headerTitle
	if latestTitle != "" {
		title = latestTitle
	}
	if title == "" && shortSummary != "" {
		// upstream: session-listing.ts:539 (header.title ?? shortSummary)
		title = shortSummary
	}
	created := headerCreated
	if created.IsZero() {
		created = modTime
	}

	info := &sessionInfo{
		Path:              file,
		ID:                headerID,
		Cwd:               headerCwd,
		Origin:            headerOrigin,
		Title:             title,
		ParentSessionPath: headerParent,
		Created:           created.UTC(),
		Modified:          modTime.UTC(),
		MessageCount:      messageCount,
		AssistantTurns:    assistantTurns,
		Size:              size,
		FirstMessage:      firstMessage,
		Status:            lastStatus,
		LatestRecap:       latestRecap,
	}

	// Fill session ID / project on any recaps/prompts parsed before header (defensive).
	for i := range recaps {
		if recaps[i].SessionID == "" {
			recaps[i].SessionID = headerID
		}
		if recaps[i].Project == "" {
			recaps[i].Project = headerCwd
		}
	}
	for i := range prompts {
		if prompts[i].SessionID == "" {
			prompts[i].SessionID = headerID
		}
		if prompts[i].Project == "" {
			prompts[i].Project = headerCwd
		}
	}

	return &fullSessionScan{
		Info:     info,
		Recaps:   recaps,
		Prompts:  prompts,
		Messages: messages,
	}
}

func isRecapCustomType(customType string) bool {
	switch strings.ToLower(strings.TrimSpace(customType)) {
	case "recap", "idle-recap", "idle_recap", "session-recap", "session_recap", "omp:recap":
		return true
	}
	return false
}

func extractRecapFromCustom(rec *rawSessionLine) string {
	if s := strings.TrimSpace(rec.Recap); s != "" {
		return s
	}
	if s := strings.TrimSpace(rec.Text); s != "" {
		return s
	}
	if s := strings.TrimSpace(extractTextContent(rec.Content, "\n")); s != "" {
		return s
	}
	if len(rec.Data) > 0 {
		var d struct {
			Recap string `json:"recap"`
			Text  string `json:"text"`
		}
		if json.Unmarshal(rec.Data, &d) == nil {
			if s := strings.TrimSpace(d.Recap); s != "" {
				return s
			}
			if s := strings.TrimSpace(d.Text); s != "" {
				return s
			}
		}
	}
	return ""
}

func parseOrigin(v any) string {
	switch o := v.(type) {
	case string:
		return strings.TrimSpace(o)
	case map[string]any:
		if s, ok := o["client"].(string); ok {
			return strings.TrimSpace(s)
		}
		if s, ok := o["type"].(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// statusFromRawMessage derives SessionStatus from a message record.
// upstream: packages/coding-agent/src/session/session-listing.ts:203-230
func statusFromRawMessage(m *rawMessageObj) SessionStatus {
	if m == nil {
		return StatusUnknown
	}
	switch m.Role {
	case "assistant":
		switch m.StopReason {
		case "error":
			return StatusError
		case "aborted":
			return StatusAborted
		case "length":
			return StatusInterrupted
		}
		if hasToolCallBlock(m.Content) {
			return StatusInterrupted
		}
		return StatusComplete
	case "toolResult":
		return StatusInterrupted
	case "user":
		return StatusPending
	default:
		return StatusUnknown
	}
}

func hasToolCallBlock(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '[' {
		return false
	}
	var blocks []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return false
	}
	for _, b := range blocks {
		if b.Type == "toolCall" {
			return true
		}
	}
	return false
}

func deriveStatusFromSuffix(suffix []byte) SessionStatus {
	lines := bytes.Split(suffix, []byte{'\n'})
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var rec rawSessionLine
		if json.Unmarshal(line, &rec) == nil && rec.Type == "message" && rec.Message != nil {
			return statusFromRawMessage(rec.Message)
		}
	}
	return StatusUnknown
}

// extractTextContent extracts human-written text from a message content field
// (either a JSON string or an array of `{type: "text", text: "..."}` blocks),
// intentionally excluding thinking blocks, toolCall blocks, and image blocks.
//
// upstream: packages/coding-agent/src/session/transcript-index.ts:381
func extractTextContent(raw json.RawMessage, sep string) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		return ""
	}
	if raw[0] == '[' {
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &blocks) != nil {
			return ""
		}
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, sep)
	}
	return ""
}

// isEmptySession matches upstream session-listing.ts:743-749:
// a 0-turn stub with no assistant activity, no sanitized title, and no first prompt.
func isEmptySession(info *sessionInfo) bool {
	if info == nil {
		return true
	}
	if info.Status != "" && info.Status != StatusPending && info.Status != StatusUnknown {
		return false
	}
	if info.AssistantTurns > 0 {
		return false
	}
	if sanitizeSessionName(info.Title) != "" {
		return false
	}
	if sanitizeSessionName(info.FirstMessage) != "" {
		return false
	}
	return true
}

// sanitizeSessionName takes the first line, strips ASCII control chars, and trims whitespace.
// upstream: packages/coding-agent/src/session/session-listing.ts:124-130
func sanitizeSessionName(value string) string {
	if value == "" {
		return ""
	}
	first := value
	if idx := strings.IndexAny(value, "\r\n"); idx >= 0 {
		first = value[:idx]
	}
	var b strings.Builder
	b.Grow(len(first))
	for i := range len(first) {
		c := first[i]
		if c < 0x20 || c == 0x7f {
			continue
		}
		b.WriteByte(c)
	}
	return strings.TrimSpace(b.String())
}

// sessionDisplayName returns explicit title, else first user prompt, else "Untitled · HH:MM".
// upstream: packages/coding-agent/src/session/session-listing.ts:152-163
func sessionDisplayName(info *sessionInfo) string {
	if t := sanitizeSessionName(info.Title); t != "" {
		return t
	}
	if f := sanitizeSessionName(info.FirstMessage); f != "" && f != "(no messages)" {
		return f
	}
	ts := info.Created
	if ts.IsZero() {
		ts = info.Modified
	}
	return "Untitled · " + ts.Local().Format("15:04")
}

// normalizePrompt matches upstream history-storage.ts:110-115.
func normalizePrompt(prompt string) string {
	prompt = strings.ReplaceAll(prompt, "\r\n", "\n")
	prompt = strings.ReplaceAll(prompt, "\r", "\n")
	lines := strings.Split(prompt, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t\f\v")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func parseTimestamp(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t.UTC(), true
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC(), true
	}
	return time.Time{}, false
}

func entryTime(raw string, fallback time.Time) time.Time {
	if t, ok := parseTimestamp(raw); ok {
		return t
	}
	return fallback.UTC()
}

func messageTime(entryTS string, msgTS any, fallback time.Time) time.Time {
	if t, ok := parseTimestamp(entryTS); ok {
		return t
	}
	switch v := msgTS.(type) {
	case string:
		if t, ok := parseTimestamp(v); ok {
			return t
		}
	case float64:
		if v > 1e14 {
			// microseconds
			return time.UnixMicro(int64(v)).UTC()
		}
		if v > 1e11 {
			// milliseconds
			return time.UnixMilli(int64(v)).UTC()
		}
		if v > 1e8 {
			// seconds
			return time.Unix(int64(v), 0).UTC()
		}
	}
	return fallback.UTC()
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// readOptionalHistoryFiles reads any optional `history.jsonl` / `recaps.jsonl` files
// located beside configured session roots (`<agentDir>/history.jsonl` or `<agentDir>/recaps.jsonl`).
func readOptionalHistoryPrompts(ctx context.Context) []sessionPromptEntry {
	var out []sessionPromptEntry
	order := 0
	for _, root := range defaultSessionDirs() {
		if ctx != nil && ctx.Err() != nil {
			break
		}
		histPath := filepath.Join(filepath.Dir(root), "history.jsonl")
		st, err := os.Stat(histPath)
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		f, err := os.Open(histPath)
		if err != nil {
			continue
		}
		lr := newBoundedLineReader(f, maxScanLineBytes)
		for {
			line, tooLong, err := lr.next()
			if err != nil && errors.Is(err, io.EOF) && len(line) == 0 {
				break
			}
			if !tooLong && len(line) > 0 {
				var rec struct {
					Prompt    string `json:"prompt"`
					Text      string `json:"text"`
					Cwd       string `json:"cwd"`
					Project   string `json:"project"`
					SessionID string `json:"sessionId"`
					Session   string `json:"session"`
					At        string `json:"at"`
					Timestamp string `json:"timestamp"`
					CreatedAt any    `json:"created_at"`
				}
				if json.Unmarshal(bytes.TrimSpace(line), &rec) == nil {
					txt := normalizePrompt(firstNonEmpty(rec.Prompt, rec.Text))
					if txt != "" {
						order++
						at := messageTime(firstNonEmpty(rec.At, rec.Timestamp), rec.CreatedAt, st.ModTime().UTC())
						out = append(out, sessionPromptEntry{
							Text:      txt,
							At:        at,
							Project:   firstNonEmpty(rec.Cwd, rec.Project),
							SessionID: firstNonEmpty(rec.SessionID, rec.Session),
							Order:     order,
						})
					}
				}
			}
			if err != nil {
				break
			}
		}
		_ = f.Close()
	}
	return out
}

func readOptionalHistoryRecaps(ctx context.Context) []sessionRecapEntry {
	var out []sessionRecapEntry
	order := 0
	for _, root := range defaultSessionDirs() {
		if ctx != nil && ctx.Err() != nil {
			break
		}
		recapPath := filepath.Join(filepath.Dir(root), "recaps.jsonl")
		st, err := os.Stat(recapPath)
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		f, err := os.Open(recapPath)
		if err != nil {
			continue
		}
		lr := newBoundedLineReader(f, maxScanLineBytes)
		for {
			line, tooLong, err := lr.next()
			if err != nil && errors.Is(err, io.EOF) && len(line) == 0 {
				break
			}
			if !tooLong && len(line) > 0 {
				var rec struct {
					Recap     string `json:"recap"`
					Text      string `json:"text"`
					Cwd       string `json:"cwd"`
					Project   string `json:"project"`
					SessionID string `json:"sessionId"`
					Session   string `json:"session"`
					At        string `json:"at"`
					Timestamp string `json:"timestamp"`
					CreatedAt any    `json:"createdAt"`
				}
				if json.Unmarshal(bytes.TrimSpace(line), &rec) == nil {
					txt := strings.TrimSpace(firstNonEmpty(rec.Recap, rec.Text))
					sessID := firstNonEmpty(rec.SessionID, rec.Session)
					if txt != "" && sessID != "" {
						order++
						at := messageTime(firstNonEmpty(rec.At, rec.Timestamp), rec.CreatedAt, st.ModTime().UTC())
						out = append(out, sessionRecapEntry{
							Text:      txt,
							At:        at,
							SessionID: sessID,
							Project:   firstNonEmpty(rec.Cwd, rec.Project),
							Order:     order,
						})
					}
				}
			}
			if err != nil {
				break
			}
		}
		_ = f.Close()
	}
	return out
}
