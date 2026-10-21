package tintinweb_subagents

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"sync"
	"time"
)

// The streaming JSON-lines transcript of an agent (`<agent id>.output`), matching Claude Code's task output file.
// upstream: src/output-file.ts.

var (
	separators  = regexp.MustCompile(`[/\\]`)
	driveDash   = regexp.MustCompile(`^[A-Za-z]:-`)
	leadingDash = regexp.MustCompile(`^-+`)
)

// encodeCwd encodes a cwd as a filesystem-safe directory name (POSIX, Windows drive and UNC paths).
// upstream: output-file.ts encodeCwd.
func encodeCwd(cwd string) string {
	return leadingDash.ReplaceAllString(driveDash.ReplaceAllString(separators.ReplaceAllString(cwd, "-"), ""), "")
}

// sessionTaskDir is the per-session scratch directory, created if missing:
// <tmp>/pi-subagents-<uid>/<encoded cwd>/<session id>/tasks. upstream: output-file.ts sessionTaskDir.
func sessionTaskDir(cwd, sessionID string) (string, error) {
	uid := os.Getuid() // -1 on Windows, where the original's process.getuid is absent and reads 0
	if uid < 0 {
		uid = 0
	}
	root := filepath.Join(os.TempDir(), "pi-subagents-"+strconv.Itoa(uid))
	// A root that is already an owner-only directory (every spawn after the first) needs neither the mkdir nor the
	// chmod: two syscalls less on the start path of every agent.
	if st, err := os.Stat(root); err != nil || !st.IsDir() || st.Mode().Perm() != 0o700 {
		if err := os.MkdirAll(root, 0o700); err != nil {
			return "", err
		}
		// chmod enforces 0700 past umask; it fails on some Windows filesystems, where it is ignored.
		if err := os.Chmod(root, 0o700); err != nil && runtime.GOOS != "windows" {
			return "", err
		}
	}
	dir := filepath.Join(root, encodeCwd(cwd), sessionID, "tasks")
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return "", err
	}
	return dir, nil
}

// createOutputFilePath is the transcript path of an agent, its directory ensured. upstream: createOutputFilePath.
func createOutputFilePath(cwd, agentID, sessionID string) (string, error) {
	dir, err := sessionTaskDir(cwd, sessionID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, agentID+".output"), nil
}

// jsTimestamp is `new Date().toISOString()`: UTC, millisecond precision.
func jsTimestamp() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000Z") }

// jsonLine is JSON.stringify(v) + "\n": no HTML escaping, as JavaScript writes it.
func jsonLine(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return b.Bytes()
}

// transcriptEntry marshals one JSON line the way JSON.stringify writes the original's entry (field order kept).
type transcriptEntry struct {
	IsSidechain bool   `json:"isSidechain"`
	AgentID     string `json:"agentId"`
	Type        string `json:"type"`
	Message     any    `json:"message"`
	Timestamp   string `json:"timestamp"`
	Cwd         string `json:"cwd"`
}

// writeInitialEntry writes (and truncates to) the initial user prompt entry. upstream: writeInitialEntry.
func writeInitialEntry(path, agentID, prompt, cwd string) {
	_ = os.WriteFile(path, jsonLine(transcriptEntry{true, agentID, "user", map[string]any{"role": "user", "content": prompt}, jsTimestamp(), cwd}), 0o666)
}

// transcriptStream is the Go form of streamToOutputFile. The original reads `session.messages`; a child process
// has no such array, so the stream follows the messages its message_end events carry, in order, and flushes the
// not-yet-written tail at turn_end, as the original does. Only that tail is kept: a flushed message is never read
// again, and a successful compaction (which replaces Pi's array) drops whatever was seen since the last flush, as
// the original's re-anchor to the rebuilt array does. Every message counts, a system message included: Pi's
// session.messages holds the system prompt at index 0 (pi 1402973a, packages/agent/src/agent.ts and the recorded
// goldens), so the original's start index 1 skips it and writes the user prompt a second time.
type transcriptStream struct {
	mu      sync.Mutex
	path    string
	agentID string
	cwd     string
	skip    int // messages still to be seen but not written: the ones before the start index
	tail    []map[string]any
	closed  bool
}

// newTranscriptStream streams to path. startIndex is the index of the first message it is responsible for: a spawn
// writes messages[0] as the initial entry, so it starts at 1; a resume passes the session's length.
// upstream: output-file.ts streamToOutputFile (startIndex).
func newTranscriptStream(path, agentID, cwd string, startIndex int) *transcriptStream {
	return &transcriptStream{path: path, agentID: agentID, cwd: cwd, skip: startIndex}
}

// flushLocked appends the unwritten tail to the file and forgets it. The caller holds t.mu.
func (t *transcriptStream) flushLocked() {
	if len(t.tail) == 0 {
		return
	}
	f, err := os.OpenFile(t.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o666)
	if err == nil {
		for _, msg := range t.tail {
			kind := "toolResult"
			switch msg["role"] {
			case "assistant":
				kind = "assistant"
			case "user":
				kind = "user"
			}
			_, _ = f.Write(jsonLine(transcriptEntry{true, t.agentID, kind, msg, jsTimestamp(), t.cwd}))
		}
		f.Close()
	}
	t.tail = nil
}

// onEvent feeds one event of the child's RPC stream.
func (t *transcriptStream) onEvent(ev map[string]any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	switch ev["type"] {
	case "message_end":
		msg, ok := ev["message"].(map[string]any)
		switch {
		case !ok:
		case t.skip > 0:
			t.skip--
		default:
			t.tail = append(t.tail, msg)
		}
	case "turn_end":
		t.flushLocked()
	case "compaction_start":
		// Flush before the history is rewritten so a tail that never saw a turn_end still reaches the file.
		t.flushLocked()
	case "compaction_end":
		// The array was rebuilt: what was seen since the flush belongs to the old one. Aborted or failed compactions
		// leave it alone.
		aborted, _ := ev["aborted"].(bool)
		if result, _ := ev["result"].(map[string]any); !aborted && result != nil {
			t.tail, t.skip = nil, 0
		}
	}
}

// close does a final flush and detaches the stream.
func (t *transcriptStream) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.flushLocked()
	t.closed = true
}
