// Package session reads PiG's session JSONL and appends tool results to it.
//
// PiG's session format is a compatibility surface (header version 3). The
// adapter touches a session file only while no pig process runs, and only to
// append toolResult entries after the assistant entry a parked turn stopped at.
// Anything unexpected fails closed.
package session

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/MichaelKinsy/pigpen/components/kagent/harness/adapter/internal/journal"
)

// SupportedVersion is the only session header version the adapter appends to.
const SupportedVersion = 3

// ValidID reports whether id is acceptable as a PiG session id and as a
// kagent continuation id: a UUID or a string of [A-Za-z0-9._-] that starts and
// ends alphanumeric (PiG's own rule).
func ValidID(id string) error {
	if id == "" || len(id) > 128 {
		return errors.New("invalid PiG session id length")
	}
	for i, r := range id {
		alnum := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
		if alnum {
			continue
		}
		if (r == '.' || r == '-' || r == '_') && i != 0 && i != len(id)-1 {
			continue
		}
		return errors.New("invalid PiG session id")
	}
	return nil
}

// Find returns the one session file for id in dir. It reports found=false when
// there is none and an error when more than one exists: PiG's lookup is
// cwd-scoped, so the same id from another cwd silently forks the history.
func Find(dir, id string) (path string, found bool, err error) {
	if err := ValidID(id); err != nil {
		return "", false, err
	}
	matches, err := filepath.Glob(filepath.Join(dir, "*_"+id+".jsonl"))
	if err != nil {
		return "", false, err
	}
	switch len(matches) {
	case 0:
		return "", false, nil
	case 1:
		return matches[0], true, nil
	default:
		return "", false, fmt.Errorf("%d PiG session files carry id %s; the session history has forked", len(matches), id)
	}
}

// Entry is the part of a session entry the adapter needs.
type Entry struct {
	ID         string
	ParentID   string
	Type       string
	Role       string
	StopReason string
	ToolCallID string
	Calls      []journal.Call
}

// File is a scanned session file.
type File struct {
	Path    string
	Version int
	IDs     map[string]struct{}
	// Last is the final parseable entry (nil for a header-only file).
	Last *Entry
	// Assistant is the final assistant message entry, After the entries that
	// follow it, in file order.
	Assistant *Entry
	After     []Entry
	// NeedsNewline is true when the file does not end with LF (a torn tail),
	// so an append must start with one.
	NeedsNewline bool
}

type wireEntry struct {
	Type     string  `json:"type"`
	ID       string  `json:"id"`
	ParentID *string `json:"parentId"`
	Version  int     `json:"version"`
	Message  *struct {
		Role       string          `json:"role"`
		Content    json.RawMessage `json:"content"`
		StopReason string          `json:"stopReason"`
		ToolCallID string          `json:"toolCallId"`
	} `json:"message"`
}

// Scan reads a session file. A torn last line is tolerated and ignored, as
// PiG does; an unparseable line anywhere else is skipped the same way.
func Scan(path string) (*File, error) {
	handle, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open PiG session: %w", err)
	}
	defer handle.Close()
	file := &File{Path: path, IDs: map[string]struct{}{}}
	reader := bufio.NewReaderSize(handle, 1<<20)
	first := true
	for {
		line, readErr := reader.ReadBytes('\n')
		complete := bytes.HasSuffix(line, []byte{'\n'})
		if len(line) > 0 && !complete {
			file.NeedsNewline = true
		}
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) > 0 {
			var wire wireEntry
			if err := json.Unmarshal(trimmed, &wire); err == nil && wire.Type != "" {
				if first {
					if wire.Type != "session" {
						return nil, errors.New("PiG session file does not start with a session header")
					}
					file.Version = wire.Version
				} else {
					file.add(&wire)
				}
			} else if first {
				return nil, errors.New("PiG session header is unreadable")
			}
			first = false
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("read PiG session: %w", readErr)
		}
	}
	if first {
		return nil, errors.New("PiG session file is empty")
	}
	return file, nil
}

func (f *File) add(wire *wireEntry) {
	entry := Entry{ID: wire.ID, Type: wire.Type}
	if wire.ParentID != nil {
		entry.ParentID = *wire.ParentID
	}
	if wire.ID != "" {
		f.IDs[wire.ID] = struct{}{}
	}
	if wire.Message != nil {
		entry.Role, entry.StopReason, entry.ToolCallID = wire.Message.Role, wire.Message.StopReason, wire.Message.ToolCallID
		if entry.Role == "assistant" {
			entry.Calls = parseCalls(wire.Message.Content)
		}
	}
	f.Last = &entry
	if entry.Type == "message" && entry.Role == "assistant" {
		f.Assistant, f.After = &entry, nil
		return
	}
	if f.Assistant != nil {
		f.After = append(f.After, entry)
	}
}

func parseCalls(content json.RawMessage) []journal.Call {
	var blocks []struct {
		Type      string          `json:"type"`
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return nil
	}
	var calls []journal.Call
	for _, block := range blocks {
		if block.Type == "toolCall" && block.ID != "" {
			args := block.Arguments
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			calls = append(calls, journal.Call{ID: block.ID, Name: block.Name, Arguments: args})
		}
	}
	return calls
}

// ParkedTail returns the assistant entry a parked session ends at. The file
// must end at an assistant message that has tool calls and no later entries.
func (f *File) ParkedTail() (*Entry, error) {
	if f.Version != SupportedVersion {
		return nil, fmt.Errorf("PiG session header version %d is not supported (want %d)", f.Version, SupportedVersion)
	}
	if f.Assistant == nil || len(f.Assistant.Calls) == 0 || len(f.After) != 0 || f.Last == nil || f.Last.ID != f.Assistant.ID {
		return nil, errors.New("the PiG session does not end at an assistant tool-call entry")
	}
	return f.Assistant, nil
}

// Result is the tool result to append for one call.
type Result struct {
	CallID string
	Name   string
	Out    journal.Outcome
}

type resultLine struct {
	Type      string        `json:"type"`
	ID        string        `json:"id"`
	ParentID  string        `json:"parentId"`
	Timestamp string        `json:"timestamp"`
	Message   resultMessage `json:"message"`
}

type resultMessage struct {
	Role       string          `json:"role"`
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	Content    []journal.Block `json:"content"`
	IsError    bool            `json:"isError"`
	Timestamp  int64           `json:"timestamp"`
}

// Inject appends one toolResult entry for every call of the assistant message
// at assistantID that has no result yet. results supplies the entries by call
// id; every unresolved call must have one. It is idempotent: entries already
// present (a crashed earlier attempt, torn tail included) are not repeated.
// It fails closed unless the header version is 3 and the file ends at the
// recorded assistant entry followed only by results for that message's calls.
// It returns the number of entries appended.
func Inject(path, assistantID string, results []Result, now time.Time) (int, error) {
	file, err := Scan(path)
	if err != nil {
		return 0, err
	}
	if file.Version != SupportedVersion {
		return 0, fmt.Errorf("PiG session header version %d is not supported (want %d)", file.Version, SupportedVersion)
	}
	if file.Assistant == nil || file.Assistant.ID != assistantID {
		return 0, errors.New("the PiG session no longer ends at the parked assistant entry")
	}
	calls := map[string]bool{}
	for _, call := range file.Assistant.Calls {
		calls[call.ID] = true
	}
	have := map[string]bool{}
	parent := assistantID
	for _, entry := range file.After {
		if entry.Type != "message" || entry.Role != "toolResult" || !calls[entry.ToolCallID] || entry.ParentID != parent {
			return 0, errors.New("the PiG session has entries after the parked assistant entry that are not its tool results")
		}
		have[entry.ToolCallID] = true
		parent = entry.ID
	}
	var lines []byte
	appended := 0
	for _, result := range results {
		if !calls[result.CallID] {
			return 0, fmt.Errorf("tool result for %q does not belong to the parked assistant message", result.CallID)
		}
	}
	byID := map[string]Result{}
	for _, result := range results {
		byID[result.CallID] = result
	}
	for _, call := range file.Assistant.Calls {
		if have[call.ID] {
			continue
		}
		result, ok := byID[call.ID]
		if !ok {
			return 0, fmt.Errorf("no tool result supplied for call %q", call.ID)
		}
		id, err := newEntryID(file.IDs)
		if err != nil {
			return 0, err
		}
		content := result.Out.Content
		if content == nil {
			content = []journal.Block{}
		}
		line, err := json.Marshal(resultLine{
			Type: "message", ID: id, ParentID: parent, Timestamp: now.UTC().Format("2006-01-02T15:04:05.000Z"),
			Message: resultMessage{Role: "toolResult", ToolCallID: call.ID, ToolName: call.Name, Content: content, IsError: result.Out.IsError, Timestamp: now.UnixMilli()},
		})
		if err != nil {
			return 0, fmt.Errorf("encode tool result: %w", err)
		}
		lines = append(append(lines, line...), '\n')
		parent = id
		appended++
	}
	if appended == 0 {
		return 0, nil
	}
	if file.NeedsNewline {
		lines = append([]byte{'\n'}, lines...)
	}
	handle, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return 0, fmt.Errorf("open PiG session for append: %w", err)
	}
	if _, err := handle.Write(lines); err != nil {
		return 0, errors.Join(fmt.Errorf("append to PiG session: %w", err), handle.Close())
	}
	if err := handle.Sync(); err != nil {
		return 0, errors.Join(fmt.Errorf("sync PiG session: %w", err), handle.Close())
	}
	if err := handle.Close(); err != nil {
		return 0, fmt.Errorf("close PiG session: %w", err)
	}
	return appended, nil
}

func newEntryID(taken map[string]struct{}) (string, error) {
	for range 16 {
		var raw [4]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return "", fmt.Errorf("generate session entry id: %w", err)
		}
		id := hex.EncodeToString(raw[:])
		if _, clash := taken[id]; !clash {
			taken[id] = struct{}{}
			return id, nil
		}
	}
	return "", errors.New("could not generate a unique session entry id")
}

// NotExecutedText is the text of a sibling call the park interrupted.
func NotExecutedText(kind journal.Kind) string {
	what := "approval"
	if kind == journal.KindAskUser {
		what = "a question to the user"
	}
	return "Not executed: the batch was interrupted for " + what + ". Call it again if still needed."
}

// TextOutcome wraps text as an outcome.
func TextOutcome(text string, isError bool) journal.Outcome {
	return journal.Outcome{Content: []journal.Block{{Type: "text", Text: text}}, IsError: isError}
}
