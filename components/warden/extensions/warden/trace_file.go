package warden

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Port of src/trace-file.ts. For hosts that run without the terminal UI, the trace is also appended to
// `<dir>/<session>.jsonl`, one JSON object per line, when PIGPEN_WARDEN_TRACE_DIR names a directory (the original's
// PI_WARDEN_TRACE_DIR under the port's PIGPEN_WARDEN_* naming, D4):
//
//   - {"v":1,"kind":"session","sessionId","cwd","wardenVersion","mode","judgments","at"} when the file is opened;
//   - {"v":1,"kind":"judgments","judgments","at"} when a guard finds the judgment state changed from the last line;
//   - {"v":1,"kind":"entry","id","at","guard","line","details","tokens"?} for every trace entry, as the trace stores it;
//   - {"v":1,"kind":"amend","id","line","at"} for an outcome line added to an entry still in the trace.
//
// The file keeps every entry; the in-memory limit does not apply. A write error is reported once and ends the file for
// the session; it never reaches a guard or a tool call. Writes happen on one goroutine at a time, off the caller's path.

// TraceDirEnv names the directory for trace files.
const TraceDirEnv = "PIGPEN_WARDEN_TRACE_DIR"

// PackageVersion is the version in package.json (the trace file's `wardenVersion`); a test keeps the two equal.
const PackageVersion = "0.1.13"

// JudgmentsOffReason is why no judge is available (backend.ts JudgmentsOffReason).
type JudgmentsOffReason string

// The reasons judgments can be off.
const (
	JudgmentsNoConsent   JudgmentsOffReason = "no_consent"
	JudgmentsBadBackend  JudgmentsOffReason = "bad_backend"
	JudgmentsNoKey       JudgmentsOffReason = "no_key"
	JudgmentsKeyRejected JudgmentsOffReason = "key_rejected"
	JudgmentsBudget      JudgmentsOffReason = "budget"
)

// JudgmentsState is `"on"` or `"off:<reason>"`.
type JudgmentsState string

// JudgmentsStateOf is the trace file's judgment state for a reason ("" is on).
func JudgmentsStateOf(reason JudgmentsOffReason) JudgmentsState {
	if reason == "" {
		return "on"
	}
	return JudgmentsState("off:" + string(reason))
}

// TraceDir is the trace directory, or "" when the feature is off. A relative path is not resolved against a cwd that
// moves, so it is off too.
func TraceDir(getenv func(string) string) string {
	if getenv == nil {
		getenv = os.Getenv
	}
	dir := getenv(TraceDirEnv)
	if dir != "" && filepath.IsAbs(dir) {
		return dir
	}
	return ""
}

var reNotFileID = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// traceFileID is holds.ts sessionFileId: the session id without separators or dots, at most 40 characters.
func traceFileID(sessionID string) string {
	id := reNotFileID.ReplaceAllString(sessionID, "")
	if len(id) > 40 {
		id = id[:40]
	}
	if id == "" {
		id = strconv.Itoa(os.Getpid())
	}
	return id
}

// TraceFilePath is the file for a session under dir.
func TraceFilePath(dir, sessionID string) string {
	return filepath.Join(dir, traceFileID(sessionID)+".jsonl")
}

// TraceFileSession is what the session line records.
type TraceFileSession struct {
	SessionID string
	Cwd       string
	Mode      string
	// Judgments is whether judgments can run as the session opens.
	Judgments JudgmentsState
}

// shownPath is the home directory as `~`, the way action summaries show a path outside the project.
func shownPath(path string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && (path == home || strings.HasPrefix(path, home+string(filepath.Separator))) {
		return Redact("~" + path[len(home):])
	}
	return Redact(path)
}

var reEntryID = regexp.MustCompile(`(?m)^\{"v":1,"kind":"entry","id":(\d+)`)

// lastEntryID is the highest entry id already in the file, so a reload or a resumed session keeps ids unique.
func lastEntryID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	last := 0
	for _, m := range reEntryID.FindAllSubmatch(data, -1) {
		if n, _ := strconv.Atoi(string(m[1])); n > last {
			last = n
		}
	}
	return last, nil
}

type traceSessionLine struct {
	V             int    `json:"v"`
	Kind          string `json:"kind"`
	SessionID     string `json:"sessionId"`
	Cwd           string `json:"cwd"`
	WardenVersion string `json:"wardenVersion"`
	Mode          string `json:"mode"`
	Judgments     string `json:"judgments"`
	At            string `json:"at"`
}

type traceJudgmentsLine struct {
	V         int    `json:"v"`
	Kind      string `json:"kind"`
	Judgments string `json:"judgments"`
	At        string `json:"at"`
}

type traceEntryLine struct {
	V       int      `json:"v"`
	Kind    string   `json:"kind"`
	ID      int      `json:"id"`
	At      string   `json:"at"`
	Guard   string   `json:"guard"`
	Line    string   `json:"line"`
	Details []string `json:"details"`
	Tokens  any      `json:"tokens,omitempty"`
}

type traceAmendLine struct {
	V    int    `json:"v"`
	Kind string `json:"kind"`
	ID   int    `json:"id"`
	Line string `json:"line"`
	At   string `json:"at"`
}

func traceISOTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// TraceFile appends the session's trace events in trace order, one write at a time.
type TraceFile struct {
	Path string

	onFailure func(string)
	now       func() time.Time

	mu        sync.Mutex
	cond      *sync.Cond
	queue     []func()
	running   bool
	stopped   bool
	base      int
	sequence  int
	ids       map[*TraceEntry]int
	judgments JudgmentsState
}

// NewTraceFile opens the session's file: the directory owner-only, the file owner-only, a session line first. The
// writing happens off the caller's goroutine; Flush waits for it. onFailure receives the one failure message.
func NewTraceFile(path, dir string, session TraceFileSession, onFailure func(string)) *TraceFile {
	f := &TraceFile{Path: path, onFailure: onFailure, now: time.Now, ids: map[*TraceEntry]int{}, judgments: session.Judgments}
	f.cond = sync.NewCond(&f.mu)
	header := traceSessionLine{V: 1, Kind: "session", SessionID: session.SessionID, Cwd: shownPath(session.Cwd), WardenVersion: PackageVersion,
		Mode: session.Mode, Judgments: string(session.Judgments), At: traceISOTime(f.now())}
	f.enqueue(func() error {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		base, err := lastEntryID(path)
		if err != nil {
			return err
		}
		f.base = base
		return appendLine(path, header)
	})
	return f
}

// Listener is the trace listener: subscribe it to the session's Trace. The record is built now, so a later amend does
// not show up in the entry line.
func (f *TraceFile) Listener(event TraceEvent) {
	f.mu.Lock()
	stopped := f.stopped
	f.mu.Unlock()
	if stopped || event.Kind == "clear" || event.Entry == nil {
		return
	}
	at := traceISOTime(f.now())
	switch event.Kind {
	case "push":
		f.mu.Lock()
		f.sequence++
		local := f.sequence
		f.ids[event.Entry] = local
		f.mu.Unlock()
		e := event.Entry
		line := traceEntryLine{V: 1, Kind: "entry", At: traceISOTime(e.At), Guard: string(e.Guard), Line: e.Line, Details: append([]string{}, e.Details...)}
		if e.Tokens != nil {
			tokens := make(map[string]string, len(e.Tokens))
			for k, v := range e.Tokens {
				tokens[k] = v
			}
			line.Tokens = tokens
		}
		f.enqueue(func() error { line.ID = f.base + local; return appendLine(f.Path, line) })
	case "amend":
		f.mu.Lock()
		local, ok := f.ids[event.Entry]
		f.mu.Unlock()
		if !ok {
			return
		}
		amend := traceAmendLine{V: 1, Kind: "amend", Line: event.Line, At: at}
		f.enqueue(func() error { amend.ID = f.base + local; return appendLine(f.Path, amend) })
	}
}

// Judgments adds a `judgments` line when the state differs from the last one written.
func (f *TraceFile) Judgments(state JudgmentsState) {
	f.mu.Lock()
	if f.stopped || state == f.judgments {
		f.mu.Unlock()
		return
	}
	f.judgments = state
	f.mu.Unlock()
	line := traceJudgmentsLine{V: 1, Kind: "judgments", Judgments: string(state), At: traceISOTime(f.now())}
	f.enqueue(func() error { return appendLine(f.Path, line) })
}

// Flush returns when every event so far is on disk or the file has stopped.
func (f *TraceFile) Flush() {
	f.mu.Lock()
	for f.running || len(f.queue) > 0 {
		f.cond.Wait()
	}
	f.mu.Unlock()
}

// enqueue runs write after every earlier write, on one goroutine that exists only while writes are waiting.
func (f *TraceFile) enqueue(write func() error) {
	f.mu.Lock()
	f.queue = append(f.queue, func() { f.run(write) })
	if !f.running {
		f.running = true
		go f.drain()
	}
	f.mu.Unlock()
}

func (f *TraceFile) drain() {
	for {
		f.mu.Lock()
		if len(f.queue) == 0 {
			f.running = false
			f.cond.Broadcast()
			f.mu.Unlock()
			return
		}
		task := f.queue[0]
		f.queue = f.queue[1:]
		f.mu.Unlock()
		task()
	}
}

func (f *TraceFile) run(write func() error) {
	f.mu.Lock()
	stopped := f.stopped
	f.mu.Unlock()
	if stopped {
		return
	}
	if err := write(); err != nil {
		f.mu.Lock()
		f.stopped = true
		f.mu.Unlock()
		if f.onFailure != nil {
			func() {
				defer func() { _ = recover() }() // a failing reporter must not take the writer down
				f.onFailure("warden: trace file " + f.Path + " could not be written (" + err.Error() + "); no more trace lines go to it this session.")
			}()
		}
	}
}

// appendLine appends one JSON line (no HTML escaping, as JSON.stringify) to a file created owner-only.
func appendLine(path string, record any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(record); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(buf.Bytes()); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
