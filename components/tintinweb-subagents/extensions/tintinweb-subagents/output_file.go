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
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	// chmod enforces 0700 past umask; it fails on some Windows filesystems, where it is ignored.
	if err := os.Chmod(root, 0o700); err != nil && runtime.GOOS != "windows" {
		return "", err
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

// ensureOutputFile makes sure a transcript file exists without touching what is in it (a resume reuses it).
// upstream: output-file.ts ensureOutputFile.
func ensureOutputFile(path string) {
	if f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o666); err == nil {
		f.Close()
	}
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
// has no such array, so the stream keeps the one its message_end events build (every message of the run, in order)
// and flushes the not-yet-written tail at turn_end, as the original does. Compaction replaces the array in Pi, so
// a successful compaction re-anchors at the end of what has been seen.
type transcriptStream struct {
	mu       sync.Mutex
	path     string
	agentID  string
	cwd      string
	messages []map[string]any
	written  int
	closed   bool
}

// newTranscriptStream streams to path. startIndex is the index of the first message it is responsible for: a spawn
// writes messages[0] (the prompt) as the initial entry, so it starts at 1; a resume passes the session's length.
// upstream: output-file.ts streamToOutputFile (startIndex).
func newTranscriptStream(path, agentID, cwd string, startIndex int) *transcriptStream {
	return &transcriptStream{path: path, agentID: agentID, cwd: cwd, written: startIndex}
}

// flushLocked writes every message from the anchor on. The caller holds t.mu.
func (t *transcriptStream) flushLocked() {
	for t.written < len(t.messages) {
		msg := t.messages[t.written]
		kind := "toolResult"
		switch msg["role"] {
		case "assistant":
			kind = "assistant"
		case "user":
			kind = "user"
		}
		line := jsonLine(transcriptEntry{true, t.agentID, kind, msg, jsTimestamp(), t.cwd})
		if f, err := os.OpenFile(t.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o666); err == nil {
			_, _ = f.Write(line)
			f.Close()
		}
		t.written++
	}
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
		// PiG's rpc stream also carries the system prompt as a message (role "system"); Pi's session.messages does
		// not hold one, and neither does the transcript.
		if msg, ok := ev["message"].(map[string]any); ok && msg["role"] != "system" {
			t.messages = append(t.messages, msg)
		}
	case "turn_end":
		t.flushLocked()
	case "compaction_start":
		// Flush before the history is rewritten so a tail that never saw a turn_end still reaches the file.
		t.flushLocked()
	case "compaction_end":
		aborted, _ := ev["aborted"].(bool)
		if result, _ := ev["result"].(map[string]any); !aborted && result != nil {
			t.written = len(t.messages)
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
