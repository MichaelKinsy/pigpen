package posthorse

// Fake session manager and session-file helpers for the layer-1 twins (see the API summary at the top of
// fixture_test.go). Test code only. Every package-level identifier of the fixture files carries the `fx` prefix (or
// is a `fixture*` / `fake*` type / `with*` option) so it cannot clash with the production names of the other lanes.
//
// fakeSession is the Go stand-in for Pi 1.5.0's SessionManager (core/session-manager.js) and for the
// "indexedBranch" object of test/posthorse.test.ts:98-128. Fidelity is exactly what the ported tests need:
//
//   - an entry tree with ids / parentId, a leaf, getBranch(fromId), getEntry, getEntries (file order),
//     getLeafId, getHeader, getChildren, getLabel, getTree;
//   - the appendX family of SessionManager (appendMessage, appendCustomEntry, appendCustomMessageEntry,
//     appendCompaction, appendContextEdit, appendThinkingLevelChange, appendModelChange, appendLabelChange,
//     appendSessionInfo, branchWithSummary, branch, resetLeaf), with deterministic 8-hex ids;
//   - buildContextEntries / buildSessionProjection: Pi's projection of compaction, branch_summary, custom_message and
//     context_edit entries (session-manager.js:124-285, messages.js:40-66). bashExecution and the other message roles
//     are passed through unchanged, as Pi's sessionEntryToContextMessages does (convertToLlm, which renders them, is not
//     part of the projection). NOT modelled: the `systemMessage` Pi stores on a compaction entry (it needs a system
//     message in the projection, which no ported test builds); a `systemMessage` member on an entry you build yourself
//     is honoured by the projection as in Pi.
//   - persistence: fxPersistedSession / fxOpenSession / fxForkSession write Pi's JSONL layout (header line, then one
//     line per entry, the file created once the session holds a user or assistant message: _hasConversation/_persist of
//     session-manager.js:760-790). setSessionFile and createBranchedSession are not modelled.
//
// The "implicit" session (fxIndexedSession) reproduces indexedBranch: entries without an id get "fixture-<index>",
// entries without parentId (absent or null, JS `??`) point at the previous entry, the leaf is the last entry.
//
// Entry maps passed to a session are shallow-copied (like indexedBranch's "owned" normalized copies): a test's own
// literal is never mutated; use setEntry(id, f) to change a stored entry afterwards.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// ---------------------------------------------------------------------------------------------------------------
// JSON helpers

// fxStringify is JSON.stringify of v (Go map keys sorted): the SDK's JS-semantics encoder, no HTML escaping.
func fxStringify(v any) string {
	out, err := sdkjson.MarshalStringify(v)
	if err != nil {
		panic(fmt.Sprintf("fixture: stringify %T: %v", v, err))
	}
	return string(out)
}

// fxKeyOrder is the member order Pi gives the entries it writes; the remaining members follow, sorted.
var fxKeyOrder = []string{"type", "version", "id", "customType", "parentId", "timestamp", "cwd", "parentSession"}

// fxLine is one JSONL line (no trailing newline) in Pi's member order with JSON.stringify escaping.
func fxLine(e rec) string {
	keys := make([]string, 0, len(e))
	for k := range e {
		keys = append(keys, k)
	}
	rank := func(k string) int {
		if i := slices.Index(fxKeyOrder, k); i >= 0 {
			return i
		}
		return len(fxKeyOrder)
	}
	sort.Slice(keys, func(i, j int) bool {
		ri, rj := rank(keys[i]), rank(keys[j])
		if ri != rj {
			return ri < rj
		}
		return keys[i] < keys[j]
	})
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(fxStringify(k))
		b.WriteByte(':')
		b.WriteString(fxStringify(e[k]))
	}
	b.WriteByte('}')
	return b.String()
}

// fxJSONL is the lines of entries, each ending in "\n" (the layout of a session file body).
func fxJSONL(entries ...rec) string {
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(fxLine(e))
		b.WriteByte('\n')
	}
	return b.String()
}

// fxISONow is `new Date().toISOString()`.
func fxISONow() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000Z") }

// fxDateMs is `new Date(timestamp).getTime()`: milliseconds, or nil (JSON null: NaN) for a value Date cannot parse.
func fxDateMs(v any) any {
	s, ok := v.(string)
	if !ok {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.000Z", "2006-01-02T15:04:05Z", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return float64(t.UnixMilli())
		}
	}
	return nil
}

func fxClone(m rec) rec {
	out := make(rec, len(m)+2)
	for k, v := range m {
		out[k] = v
	}
	return out
}

func fxID(e rec) string {
	id, _ := e["id"].(string)
	return id
}

// ---------------------------------------------------------------------------------------------------------------
// Entry builders. They return the plain maps the host and the extension exchange (type rec = map[string]any).

// fxText is `{type:"text", text}`.
func fxText(text string) rec { return rec{"type": "text", "text": text} }

// fxImage is `{type:"image", data, mimeType}`.
func fxImage(data, mimeType string) rec {
	return rec{"type": "image", "data": data, "mimeType": mimeType}
}

// fxToolCall is an assistant `{type:"toolCall", id, name, arguments}` block.
func fxToolCall(id, name string, arguments rec) rec {
	if arguments == nil {
		arguments = rec{}
	}
	return rec{"type": "toolCall", "id": id, "name": name, "arguments": arguments}
}

// fxUser is a user message; content is a string or a block list.
func fxUser(content any) rec { return rec{"role": "user", "content": content, "timestamp": float64(1)} }

// fxAssistant is an assistant message with a block list (or string) content.
func fxAssistant(content any) rec {
	return rec{"role": "assistant", "content": content, "timestamp": float64(1)}
}

// fxToolResult is a toolResult message.
func fxToolResult(toolCallID, toolName string, content any, isError bool) rec {
	return rec{"role": "toolResult", "toolCallId": toolCallID, "toolName": toolName, "content": content, "isError": isError, "timestamp": float64(1)}
}

// fxMessageEntry is `{type:"message", id, message}`; id "" leaves it for the session to assign.
func fxMessageEntry(id string, message rec) rec {
	e := rec{"type": "message", "message": message}
	if id != "" {
		e["id"] = id
	}
	return e
}

// fxCustomMessage is a custom_message entry (it participates in the model context). details may be nil.
func fxCustomMessage(id, customType string, content any, display bool, details rec) rec {
	e := rec{"type": "custom_message", "customType": customType, "content": content, "display": display}
	if id != "" {
		e["id"] = id
	}
	if details != nil {
		e["details"] = details
	}
	return e
}

// fxCustom is a custom (state-only) entry.
func fxCustom(id, customType string, data any) rec {
	e := rec{"type": "custom", "customType": customType, "data": data}
	if id != "" {
		e["id"] = id
	}
	return e
}

// fxCompaction is a compaction entry; firstKept "" is JSON null (`firstKeptEntryId: null`), details may be nil.
func fxCompaction(id, summary, firstKept string, details rec) rec {
	e := rec{"type": "compaction", "summary": summary, "firstKeptEntryId": nil, "tokensBefore": float64(100)}
	if firstKept != "" {
		e["firstKeptEntryId"] = firstKept
	}
	if id != "" {
		e["id"] = id
	}
	if details != nil {
		e["details"] = details
	}
	return e
}

// fxBoundary is a compaction entry carrying `details.posthorse: 1` (a Posthorse context boundary); extra members are
// merged into details.
func fxBoundary(id, summary string, extra rec) rec {
	details := rec{"posthorse": float64(1)}
	for k, v := range extra {
		details[k] = v
	}
	return fxCompaction(id, summary, "", details)
}

// fxContextWindow is the legacy `context_window` entry (its `handoff` is the carried summary).
func fxContextWindow(id, handoff string) rec {
	e := rec{"type": "context_window", "handoff": handoff}
	if id != "" {
		e["id"] = id
	}
	return e
}

// fxWithParent returns a copy of entry with parentId set ("" = null).
func fxWithParent(entry rec, parentID string) rec {
	out := fxClone(entry)
	if parentID == "" {
		out["parentId"] = nil
	} else {
		out["parentId"] = parentID
	}
	return out
}

// fxWithTimestamp returns a copy of entry with its ISO timestamp set.
func fxWithTimestamp(entry rec, ts string) rec {
	out := fxClone(entry)
	out["timestamp"] = ts
	return out
}

// fxHeader is the header line of a session file (`parentSession` "" leaves it out).
func fxHeader(id, cwd, parentSession string) rec {
	h := rec{"type": "session", "version": float64(3), "id": id, "timestamp": "2026-10-24T00:00:00.000Z", "cwd": cwd}
	if parentSession != "" {
		h["parentSession"] = parentSession
	}
	return h
}

// fxWriteSession writes header + entries as a session JSONL file (creating its directory).
func fxWriteSession(path string, header rec, entries ...rec) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(fxJSONL(append([]rec{header}, entries...)...)), 0o644)
}

// fxWriteArchive is test/posthorse.test.ts:255 writeArchive: a session file whose header is
// `{type:"session", id:<file>, cwd:<process cwd>}` followed by content (already JSONL text).
func fxWriteArchive(file, content string) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	return os.WriteFile(file, []byte(fxStringify(rec{"type": "session", "id": file, "cwd": wd})+"\n"+content), 0o644)
}

// fxArchivedID is test/posthorse.test.ts:250 archivedId: the file-qualified id the history search prints,
// `<id>@<10 chars of the base64url sha256 of source>`; legacy prints the whole digest (the 0.6 form).
func fxArchivedID(id, source string, legacy bool) string {
	sum := sha256.Sum256([]byte(source))
	digest := base64.RawURLEncoding.EncodeToString(sum[:])
	if legacy {
		return id + "@" + digest
	}
	return id + "@" + digest[:10]
}

// fxReadSession parses a session JSONL file into its header (nil when none) and entries; malformed lines are skipped
// like Pi's parseSessionEntries.
func fxReadSession(path string) (header rec, entries []rec, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e rec
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		if e["type"] == "session" {
			if header == nil {
				header = e
			}
			continue
		}
		entries = append(entries, e)
	}
	return header, entries, nil
}

// ---------------------------------------------------------------------------------------------------------------
// fakeSession

type fakeSession struct {
	mu       sync.Mutex
	cwd      string
	dir      string // session directory; "" for an in-memory session (SessionManager.inMemory)
	file     string // session file path; "" when there is none
	persist  bool
	flushed  bool
	id       string
	header   rec
	entries  []rec // non-header entries, file order
	byID     map[string]rec
	leaf     string
	hasLeaf  bool
	implicit bool
	counter  int
	version  uint64 // bumped on every change; the fixture's mirror sync compares it
}

var fxSessionSeq struct {
	mu sync.Mutex
	n  int
}

func fxNextSessionID() string {
	fxSessionSeq.mu.Lock()
	defer fxSessionSeq.mu.Unlock()
	fxSessionSeq.n++
	return fmt.Sprintf("fixture-session-%d", fxSessionSeq.n)
}

func fxSessionCore(cwd string) *fakeSession {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	s := &fakeSession{cwd: cwd, byID: map[string]rec{}, id: fxNextSessionID()}
	s.header = fxHeader(s.id, cwd, "")
	s.header["timestamp"] = fxISONow()
	return s
}

var fxFileStamp = strings.NewReplacer(":", "-", ".", "-")

// fxMemorySession is SessionManager.inMemory(cwd): no file, session directory "".
func fxMemorySession(cwd string) *fakeSession { return fxSessionCore(cwd) }

// fxIndexedSession is indexedBranch(entries): an implicit-ancestry in-memory session; leaf = last entry.
func fxIndexedSession(entries ...rec) *fakeSession {
	s := fxSessionCore("")
	s.implicit = true
	for _, e := range entries {
		s.push(e)
	}
	return s
}

// fxPersistedSession is SessionManager.create(cwd, dir): a session whose file is `<dir>/<timestamp>_<id>.jsonl`,
// written when the first user or assistant message is appended.
func fxPersistedSession(cwd, dir string) (*fakeSession, error) {
	s := fxSessionCore(cwd)
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, err
	}
	s.persist, s.dir = true, abs
	s.file = filepath.Join(abs, fxFileStamp.Replace(s.header["timestamp"].(string))+"_"+s.id+".jsonl")
	return s, nil
}

// fxOpenSession is SessionManager.open(path[, dir]): loads an existing session file (cwd from its header).
func fxOpenSession(path, dir string) (*fakeSession, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	header, entries, err := fxReadSession(abs)
	if err != nil {
		return nil, err
	}
	cwd := ""
	if header != nil {
		cwd, _ = header["cwd"].(string)
	}
	s := fxSessionCore(cwd)
	if dir == "" {
		dir = filepath.Dir(abs)
	}
	s.dir, _ = filepath.Abs(dir)
	s.persist, s.file, s.flushed = true, abs, true
	if header != nil {
		s.header = header
		s.id, _ = header["id"].(string)
	}
	for _, e := range entries {
		id := fxID(e)
		if id == "" {
			continue
		}
		s.byID[id] = e
		s.entries = append(s.entries, e)
		s.leaf, s.hasLeaf = id, true
	}
	return s, nil
}

// fxForkSession is SessionManager.forkFrom(sourcePath, targetCwd, dir): a new session file holding the source's entries
// (verbatim lines) under a header whose parentSession is the source path (session-manager.js:1386-1433).
func fxForkSession(sourcePath, targetCwd, dir string) (*fakeSession, error) {
	src, err := filepath.Abs(sourcePath)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return nil, err
	}
	var lines []string
	count := 0
	hasHeader := false
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e rec
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		count++
		if e["type"] == "session" {
			hasHeader = true
			continue
		}
		lines = append(lines, line)
	}
	if count == 0 {
		return nil, fmt.Errorf("Cannot fork: source session file is empty or invalid: %s", src)
	}
	if !hasHeader {
		return nil, fmt.Errorf("Cannot fork: source session has no header: %s", src)
	}
	cwd, err := filepath.Abs(targetCwd)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, err
	}
	id := fxNextSessionID()
	ts := fxISONow()
	header := rec{"type": "session", "version": float64(3), "id": id, "timestamp": ts, "cwd": cwd, "parentSession": src}
	file := filepath.Join(abs, fxFileStamp.Replace(ts)+"_"+id+".jsonl")
	body := fxLine(header) + "\n"
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		return nil, err
	}
	return fxOpenSession(file, abs)
}

// ---- reads ----------------------------------------------------------------------------------------------------

func (s *fakeSession) sessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

func (s *fakeSession) getCwd() string { return s.cwd }

func (s *fakeSession) getSessionDir() string { return s.dir }

// getSessionFile is "" for a session with no file path (JS undefined).
func (s *fakeSession) getSessionFile() string { return s.file }

func (s *fakeSession) getHeader() rec {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.header
}

// getLeafID is nil for an empty/reset session (JS null).
func (s *fakeSession) getLeafID() *string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.leafLocked()
}

func (s *fakeSession) leafLocked() *string {
	if s.implicit {
		if len(s.entries) == 0 {
			return nil
		}
		id := fxID(s.entries[len(s.entries)-1])
		return &id
	}
	if !s.hasLeaf {
		return nil
	}
	id := s.leaf
	return &id
}

func (s *fakeSession) getEntry(id string) rec {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byID[id]
}

// getEntries is every entry in file order (the header excluded).
func (s *fakeSession) getEntries() []rec {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.entries)
}

// getBranch walks parentId from fromID (nil = the leaf) to the root and returns the path root-first.
func (s *fakeSession) getBranch(fromID *string) []rec {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.branchLocked(fromID)
}

func (s *fakeSession) branchLocked(fromID *string) []rec {
	start := fromID
	if start == nil {
		start = s.leafLocked()
	}
	var path []rec
	if start == nil {
		return path
	}
	cur := s.byID[*start]
	for cur != nil && len(path) <= len(s.entries) {
		path = append(path, cur)
		pid, _ := cur["parentId"].(string)
		if pid == "" {
			break
		}
		cur = s.byID[pid]
	}
	slices.Reverse(path)
	return path
}

// getChildren is the entries whose parentId is parentID (nil = null) in file order.
func (s *fakeSession) getChildren(parentID *string) []rec {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []rec{}
	for _, e := range s.entries {
		pid, hasPid := e["parentId"].(string)
		if parentID == nil && !hasPid || parentID != nil && hasPid && pid == *parentID {
			out = append(out, e)
		}
	}
	return out
}

// getLabel is the label of an entry from the last label entry that targets it (nil when none or cleared).
func (s *fakeSession) getLabel(id string) *string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var label *string
	for _, e := range s.entries {
		if e["type"] == "label" && e["targetId"] == id {
			if l, ok := e["label"].(string); ok && l != "" {
				label = &l
			} else {
				label = nil
			}
		}
	}
	return label
}

// ---- appends --------------------------------------------------------------------------------------------------

func (s *fakeSession) nextID() string {
	for {
		s.counter++
		id := fmt.Sprintf("%08x", s.counter)
		if _, taken := s.byID[id]; !taken {
			return id
		}
	}
}

// push adds an implicit-ancestry entry (indexedBranch): a missing id becomes fixture-<index>, a missing or null
// parentId the previous entry. It returns the stored copy.
func (s *fakeSession) push(entry rec) rec {
	s.mu.Lock()
	defer s.mu.Unlock()
	owned := fxClone(entry)
	index := len(s.entries)
	if id, ok := owned["id"].(string); !ok || id == "" {
		owned["id"] = fmt.Sprintf("fixture-%d", index)
	}
	if _, ok := owned["parentId"].(string); !ok {
		if index > 0 {
			owned["parentId"] = s.entries[index-1]["id"]
		} else {
			owned["parentId"] = nil
		}
	}
	s.entries = append(s.entries, owned)
	s.byID[fxID(owned)] = owned
	s.version++
	return owned
}

// pop removes and returns the last entry of an implicit session (Array.pop on the original's branch array).
func (s *fakeSession) pop() rec {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.entries) == 0 {
		return nil
	}
	last := s.entries[len(s.entries)-1]
	s.entries = s.entries[:len(s.entries)-1]
	delete(s.byID, fxID(last))
	s.version++
	return last
}

// setEntry changes one stored entry in place (the original mutates the branch array's objects).
func (s *fakeSession) setEntry(id string, change func(e rec)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.byID[id]
	if e == nil {
		panic("fixture: setEntry: no entry " + id)
	}
	change(e)
	s.version++
}

// appendEntry adds a prepared entry (type and payload given; id, parentId and timestamp assigned unless set) as a child
// of the leaf.
func (s *fakeSession) appendEntry(entry rec) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appendLocked(entry)
}

func (s *fakeSession) appendLocked(entry rec) string {
	if s.implicit {
		panic("fixture: an implicit (indexed) session takes push(), not appendX")
	}
	e := fxClone(entry)
	if id, ok := e["id"].(string); !ok || id == "" {
		e["id"] = s.nextID()
	}
	if s.hasLeaf {
		e["parentId"] = s.leaf
	} else {
		e["parentId"] = nil
	}
	if _, ok := e["timestamp"]; !ok {
		e["timestamp"] = fxISONow()
	}
	id := fxID(e)
	prevEntries, prevLeaf, prevHas := len(s.entries), s.leaf, s.hasLeaf
	s.entries = append(s.entries, e)
	s.byID[id] = e
	s.leaf, s.hasLeaf = id, true
	if err := s.persistLocked(e); err != nil {
		s.entries = s.entries[:prevEntries]
		delete(s.byID, id)
		s.leaf, s.hasLeaf = prevLeaf, prevHas
		panic("fixture: persisting a session entry: " + err.Error())
	}
	s.version++
	return id
}

func (s *fakeSession) hasConversationLocked() bool {
	for _, e := range s.entries {
		if e["type"] != "message" {
			continue
		}
		if m, ok := e["message"].(rec); ok && (m["role"] == "user" || m["role"] == "assistant") {
			return true
		}
	}
	return false
}

// persistLocked is session-manager.js _persist: nothing until a user/assistant message exists, then the whole file once,
// then one appended line per entry.
func (s *fakeSession) persistLocked(e rec) error {
	if !s.persist || s.file == "" {
		return nil
	}
	if !s.flushed {
		if !s.hasConversationLocked() {
			return nil
		}
		var b strings.Builder
		b.WriteString(fxLine(s.header) + "\n")
		for _, x := range s.entries {
			b.WriteString(fxLine(x) + "\n")
		}
		f, err := os.OpenFile(s.file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		_, werr := f.WriteString(b.String())
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return werr
		}
		s.flushed = true
		return nil
	}
	f, err := os.OpenFile(s.file, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(fxLine(e) + "\n")
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// appendMessage is SessionManager.appendMessage(message).
func (s *fakeSession) appendMessage(message rec) string {
	return s.appendEntry(rec{"type": "message", "message": message})
}

// appendCustomEntry is SessionManager.appendCustomEntry(customType, data).
func (s *fakeSession) appendCustomEntry(customType string, data any) string {
	return s.appendEntry(rec{"type": "custom", "customType": customType, "data": data})
}

// appendCustomMessageEntry is SessionManager.appendCustomMessageEntry(customType, content, display, details); nil
// details leaves the member undefined (omitted).
func (s *fakeSession) appendCustomMessageEntry(customType string, content any, display bool, details rec) string {
	e := rec{"type": "custom_message", "customType": customType, "content": content, "display": display}
	if details != nil {
		e["details"] = details
	}
	return s.appendEntry(e)
}

// appendCompaction is SessionManager.appendCompaction(summary, firstKeptEntryId, tokensBefore, details): firstKept ""
// is `null`, stored as the new entry's own id (`firstKeptEntryId ?? id`). nil details stays undefined.
func (s *fakeSession) appendCompaction(summary, firstKept string, tokensBefore int, details rec) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextID()
	if firstKept == "" {
		firstKept = id
	}
	e := rec{"type": "compaction", "id": id, "summary": summary, "firstKeptEntryId": firstKept, "tokensBefore": float64(tokensBefore)}
	if details != nil {
		e["details"] = details
	}
	return s.appendLocked(e)
}

// appendThinkingLevelChange is SessionManager.appendThinkingLevelChange.
func (s *fakeSession) appendThinkingLevelChange(level string) string {
	return s.appendEntry(rec{"type": "thinking_level_change", "thinkingLevel": level})
}

// appendModelChange is SessionManager.appendModelChange.
func (s *fakeSession) appendModelChange(provider, modelID string) string {
	return s.appendEntry(rec{"type": "model_change", "provider": provider, "modelId": modelID})
}

// appendLabelChange is SessionManager.appendLabelChange (label "" clears).
func (s *fakeSession) appendLabelChange(targetID, label string) string {
	s.mu.Lock()
	_, ok := s.byID[targetID]
	s.mu.Unlock()
	if !ok {
		panic("Entry " + targetID + " not found")
	}
	return s.appendEntry(rec{"type": "label", "targetId": targetID, "label": label})
}

// appendSessionInfo is SessionManager.appendSessionInfo.
func (s *fakeSession) appendSessionInfo(name string) string {
	name = strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ").Replace(name))
	return s.appendEntry(rec{"type": "session_info", "name": name})
}

// appendContextEdit is SessionManager.appendContextEdit(targetId, replacement): replacement nil is `null` (omit the
// message); otherwise a rec with string/array `content`. The same validations as Pi, as errors.
func (s *fakeSession) appendContextEdit(targetID string, replacement rec) (string, error) {
	if replacement != nil {
		switch replacement["content"].(type) {
		case string, []any, []rec:
		default:
			return "", errors.New("Context edit replacement must be null or contain string/array content")
		}
	}
	s.mu.Lock()
	target := s.byID[targetID]
	if target == nil {
		s.mu.Unlock()
		return "", fmt.Errorf("Entry %s not found", targetID)
	}
	onBranch := slices.ContainsFunc(s.branchLocked(nil), func(e rec) bool { return fxID(e) == targetID })
	s.mu.Unlock()
	if !onBranch {
		return "", fmt.Errorf("Entry %s is not on the active branch", targetID)
	}
	role := "custom"
	editable := target["type"] == "custom_message"
	if target["type"] == "message" {
		if m, ok := target["message"].(rec); ok {
			role, _ = m["role"].(string)
			editable = role == "user" || role == "assistant" || role == "toolResult"
		}
	}
	if !editable {
		return "", fmt.Errorf("Entry %s does not contribute editable model content", targetID)
	}
	var normalized any
	if replacement != nil {
		normalized = replacement
		if text, isString := replacement["content"].(string); isString && (role == "assistant" || role == "toolResult") {
			normalized = rec{"content": []any{fxText(text)}}
		}
	}
	return s.appendEntry(rec{"type": "context_edit", "targetId": targetID, "replacement": normalized}), nil
}

// branch is SessionManager.branch(id): moves the leaf; the next append becomes a sibling branch.
func (s *fakeSession) branch(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[id]; !ok {
		panic("Entry " + id + " not found")
	}
	s.leaf, s.hasLeaf = id, true
	s.version++
}

// resetLeaf is SessionManager.resetLeaf(): the next append creates a new root.
func (s *fakeSession) resetLeaf() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.leaf, s.hasLeaf = "", false
	s.version++
}

// branchWithSummary is SessionManager.branchWithSummary(id, summary) (id "" = null).
func (s *fakeSession) branchWithSummary(branchFromID, summary string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if branchFromID != "" {
		if _, ok := s.byID[branchFromID]; !ok {
			panic("Entry " + branchFromID + " not found")
		}
	}
	fromID := "root"
	if s.hasLeaf {
		fromID = s.leaf
	}
	if branchFromID == "" {
		s.leaf, s.hasLeaf = "", false
	} else {
		s.leaf, s.hasLeaf = branchFromID, true
	}
	return s.appendLocked(rec{"type": "branch_summary", "fromId": fromID, "summary": summary})
}

// ---- projection (session-manager.js:124-285) ------------------------------------------------------------------

func (s *fakeSession) pathLocked() []rec {
	// buildSessionPath: leafId === null -> []; a leaf that is not found falls back to the last entry.
	if s.implicit {
		return s.branchLocked(nil)
	}
	if !s.hasLeaf {
		return nil
	}
	if _, ok := s.byID[s.leaf]; ok {
		return s.branchLocked(&s.leaf)
	}
	if len(s.entries) == 0 {
		return nil
	}
	last := fxID(s.entries[len(s.entries)-1])
	return s.branchLocked(&last)
}

// buildContextEntries is buildContextEntries(): the leaf path cut at the latest compaction (its firstKeptEntryId on).
func (s *fakeSession) buildContextEntries() []rec {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.contextEntriesLocked()
}

func (s *fakeSession) contextEntriesLocked() []rec {
	path := s.pathLocked()
	compactionIdx := -1
	for i, e := range path {
		if e["type"] == "compaction" {
			compactionIdx = i
		}
	}
	if compactionIdx < 0 {
		return path
	}
	compaction := path[compactionIdx]
	out := []rec{compaction}
	foundFirstKept := false
	for _, e := range path[:compactionIdx] {
		if e["id"] == compaction["firstKeptEntryId"] {
			foundFirstKept = true
		}
		if foundFirstKept && !fxIsSystemEntry(e) {
			out = append(out, e)
		}
	}
	return append(out, path[compactionIdx+1:]...)
}

func fxIsSystemEntry(e rec) bool {
	if e["type"] != "message" {
		return false
	}
	m, _ := e["message"].(rec)
	return m["role"] == "system"
}

// fxEntryMessages is sessionEntryToContextMessages.
func fxEntryMessages(e rec) []rec {
	switch e["type"] {
	case "message":
		m, _ := e["message"].(rec)
		if m == nil {
			return []rec{nil}
		}
		if m["role"] == "system" && m["content"] == nil {
			c := fxClone(m)
			c["content"] = ""
			return []rec{c}
		}
		if (m["role"] == "user" || m["role"] == "assistant" || m["role"] == "toolResult") && m["content"] == nil {
			c := fxClone(m)
			c["content"] = []any{}
			return []rec{c}
		}
		return []rec{m}
	case "custom_message":
		content := e["content"]
		if content == nil {
			content = []any{}
		}
		msg := rec{"role": "custom", "customType": e["customType"], "content": content, "display": e["display"], "timestamp": fxDateMs(e["timestamp"])}
		if d, ok := e["details"]; ok {
			msg["details"] = d
		}
		return []rec{msg}
	case "branch_summary":
		if summary, ok := e["summary"].(string); ok && summary != "" {
			msg := rec{"role": "branchSummary", "summary": summary, "timestamp": fxDateMs(e["timestamp"])}
			if from, ok := e["fromId"]; ok {
				msg["fromId"] = from
			}
			return []rec{msg}
		}
	case "compaction":
		summary := rec{"role": "compactionSummary", "summary": e["summary"], "timestamp": fxDateMs(e["timestamp"])}
		if t, ok := e["tokensBefore"]; ok {
			summary["tokensBefore"] = t
		}
		if sys, ok := e["systemMessage"].(rec); ok && sys != nil {
			return []rec{sys, summary}
		}
		return []rec{summary}
	}
	return []rec{}
}

// fxProjectEntry is projectContextEntry: a context_edit replaces the content of the messages its target contributes.
func fxProjectEntry(e, edit rec) []rec {
	messages := fxEntryMessages(e)
	if edit == nil {
		return messages
	}
	replacement, _ := edit["replacement"].(rec)
	if replacement == nil {
		return []rec{}
	}
	out := make([]rec, len(messages))
	for i, m := range messages {
		role, _ := m["role"].(string)
		if role != "user" && role != "assistant" && role != "toolResult" && role != "custom" {
			out[i] = m
			continue
		}
		c := fxClone(m)
		content := replacement["content"]
		if text, ok := content.(string); ok && (role == "assistant" || role == "toolResult") {
			content = []any{fxText(text)}
		}
		if content == nil {
			delete(c, "content")
		} else {
			c["content"] = content
		}
		out[i] = c
	}
	return out
}

// buildSessionProjection is buildSessionProjection(): {entries:[{sourceEntry,messages}], messages, thinkingLevel, model}.
func (s *fakeSession) buildSessionProjection() rec {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.pathLocked()
	thinking := any("off")
	var model any
	for _, e := range path {
		switch e["type"] {
		case "thinking_level_change":
			thinking = e["thinkingLevel"]
		case "model_change":
			model = rec{"provider": e["provider"], "modelId": e["modelId"]}
		case "message":
			if m, _ := e["message"].(rec); m != nil && m["role"] == "assistant" {
				mm := rec{}
				if v, ok := m["provider"]; ok {
					mm["provider"] = v
				}
				if v, ok := m["model"]; ok {
					mm["modelId"] = v
				}
				model = mm
			}
		}
	}
	contextEntries := s.contextEntriesLocked()
	edits := map[string]rec{}
	for _, e := range contextEntries {
		if e["type"] == "context_edit" {
			if target, ok := e["targetId"].(string); ok {
				edits[target] = e
			}
		}
	}
	projected := make([]any, 0, len(contextEntries))
	messages := []any{}
	for i, e := range contextEntries {
		ms := []rec{}
		// Only the newest compaction at index zero contributes a checkpoint and summary (session-manager.js:266-270).
		if e["type"] != "compaction" || i == 0 {
			ms = fxProjectEntry(e, edits[fxID(e)])
		}
		list := make([]any, len(ms))
		for j, m := range ms {
			list[j] = m
			messages = append(messages, m)
		}
		projected = append(projected, rec{"sourceEntry": e, "messages": list})
	}
	return rec{"entries": projected, "messages": messages, "thinkingLevel": thinking, "model": model}
}

// getTree is SessionManager.getTree(): roots with children sorted by timestamp (stable), labels attached.
func (s *fakeSession) getTree() []any {
	s.mu.Lock()
	defer s.mu.Unlock()
	nodes := map[string]rec{}
	children := map[string][]string{}
	var roots []string
	for _, e := range s.entries {
		nodes[fxID(e)] = rec{"entry": e, "children": []any{}}
	}
	labels := map[string]rec{}
	for _, e := range s.entries {
		if e["type"] == "label" {
			target, _ := e["targetId"].(string)
			if l, ok := e["label"].(string); ok && l != "" {
				labels[target] = rec{"label": l, "labelTimestamp": e["timestamp"]}
			} else {
				delete(labels, target)
			}
		}
	}
	for _, e := range s.entries {
		id := fxID(e)
		pid, hasPid := e["parentId"].(string)
		if !hasPid || pid == id {
			roots = append(roots, id)
		} else if _, ok := nodes[pid]; ok {
			children[pid] = append(children[pid], id)
		} else {
			roots = append(roots, id)
		}
	}
	ts := func(id string) float64 {
		if v, ok := fxDateMs(nodes[id]["entry"].(rec)["timestamp"]).(float64); ok {
			return v
		}
		return math.NaN()
	}
	var build func(id string) rec
	build = func(id string) rec {
		node := nodes[id]
		kids := children[id]
		sort.SliceStable(kids, func(i, j int) bool {
			a, b := ts(kids[i]), ts(kids[j])
			return !math.IsNaN(a) && !math.IsNaN(b) && a < b
		})
		list := make([]any, len(kids))
		for i, k := range kids {
			list[i] = build(k)
		}
		node["children"] = list
		if l, ok := labels[id]; ok {
			node["label"], node["labelTimestamp"] = l["label"], l["labelTimestamp"]
		}
		return node
	}
	out := make([]any, len(roots))
	for i, r := range roots {
		out[i] = build(r)
	}
	return out
}
