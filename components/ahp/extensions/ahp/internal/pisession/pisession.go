// Package pisession reads and writes Pi's session files (JSONL, version 3): a header line, then
// entries forming a tree by id/parentId. It ports the parts of Pi's SessionManager
// (dist/core/session-manager.js, 0.87.1) that pi-ahp uses; the format is Pi's, so files it writes
// open in Pi and files Pi writes load here.
//
// Entries are kept as decoded JSON: Pi's session format is open-ended (extensions add custom
// entry types) and this package must round-trip what it does not understand.
package pisession

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// CurrentVersion is the session format version this package writes.
const CurrentVersion = 3

// Entry is one session entry (or the header) as decoded JSON.
type Entry map[string]any

// Type is the entry's `type`.
func (e Entry) Type() string { s, _ := e["type"].(string); return s }

// ID is the entry id.
func (e Entry) ID() string { s, _ := e["id"].(string); return s }

// ParentID is the parent entry id ("" for a root).
func (e Entry) ParentID() string { s, _ := e["parentId"].(string); return s }

// Timestamp is the entry's ISO timestamp.
func (e Entry) Timestamp() string { s, _ := e["timestamp"].(string); return s }

// Message is the message of a "message" entry.
func (e Entry) Message() map[string]any {
	if e.Type() != "message" {
		return nil
	}
	m, _ := e["message"].(map[string]any)
	return m
}

// Role is the role of a "message" entry's message.
func (e Entry) Role() string {
	s, _ := e.Message()["role"].(string)
	return s
}

var validID = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)

// ValidateID applies Pi's session id rules.
func ValidateID(id string) error {
	if !validID.MatchString(id) {
		return errors.New("Session id must be non-empty, contain only alphanumeric characters, '-', '_', and '.', and start and end with an alphanumeric character")
	}
	return nil
}

// Options for a new session.
type Options struct {
	// ID of the new session; generated when empty.
	ID            string
	ParentSession string
}

// Manager is a session: its entry tree, a leaf pointer and (optionally) its file.
//
// Like Pi's, a persistent session withholds its file until the first assistant message exists,
// then writes every entry so far in one go and appends from there on.
type Manager struct {
	mu        sync.Mutex
	cwd       string
	dir       string
	file      string
	persist   bool
	flushed   bool
	sessionID string
	entries   []Entry // header first
	byID      map[string]Entry
	leafID    string
	now       func() time.Time
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func uuidLike() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x70
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// InMemory creates a session that never touches disk.
func InMemory(cwd string, opts Options) (*Manager, error) {
	return create(cwd, "", false, opts)
}

// Create makes a persistent session whose file will live under dir.
func Create(cwd, dir string, opts Options) (*Manager, error) {
	return create(cwd, dir, true, opts)
}

func create(cwd, dir string, persist bool, opts Options) (*Manager, error) {
	if opts.ID != "" {
		if err := ValidateID(opts.ID); err != nil {
			return nil, err
		}
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		abs = cwd
	}
	m := &Manager{cwd: abs, dir: dir, persist: persist, now: time.Now, byID: map[string]Entry{}}
	if persist && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	m.newSession(opts)
	return m, nil
}

func (m *Manager) timestamp() string { return m.now().UTC().Format("2006-01-02T15:04:05.000Z") }

func (m *Manager) newSession(opts Options) {
	m.sessionID = opts.ID
	if m.sessionID == "" {
		m.sessionID = uuidLike()
	}
	ts := m.timestamp()
	header := Entry{"type": "session", "version": float64(CurrentVersion), "id": m.sessionID, "timestamp": ts, "cwd": m.cwd}
	if opts.ParentSession != "" {
		header["parentSession"] = opts.ParentSession
	}
	m.entries = []Entry{header}
	m.byID = map[string]Entry{}
	m.leafID = ""
	m.flushed = false
	if m.persist {
		stamp := strings.NewReplacer(":", "-", ".", "-").Replace(ts)
		m.file = filepath.Join(m.dir, stamp+"_"+m.sessionID+".jsonl")
	}
}

// Open loads an existing session file, appending to it from here on.
func Open(path string) (*Manager, error) {
	entries, err := LoadEntries(path)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("Session file is not a valid pi session: %s", path)
	}
	abs, _ := filepath.Abs(path)
	m := &Manager{file: abs, dir: filepath.Dir(abs), persist: true, flushed: true, now: time.Now}
	m.entries = entries
	m.sessionID, _ = entries[0]["id"].(string)
	m.cwd, _ = entries[0]["cwd"].(string)
	m.buildIndex()
	return m, nil
}

// LoadEntries reads a session file: header first. Malformed lines are skipped; a file whose first
// line is not a session header yields nothing.
func LoadEntries(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var entries []Entry
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadString('\n')
		if e, ok := ParseLine(line); ok {
			entries = append(entries, e)
		}
		if err != nil {
			break
		}
	}
	if len(entries) == 0 {
		return nil, nil
	}
	if entries[0].Type() != "session" {
		return nil, nil
	}
	if _, ok := entries[0]["id"].(string); !ok {
		return nil, nil
	}
	migrate(entries)
	return entries, nil
}

// migrate brings entries of an older format up to the current version (in place).
func migrate(entries []Entry) {
	version := 1.0
	if v, ok := entries[0]["version"].(float64); ok {
		version = v
	}
	if version >= CurrentVersion {
		return
	}
	if version < 2 {
		prev := ""
		ids := map[string]bool{}
		for i, e := range entries {
			if e.Type() == "session" {
				e["version"] = float64(2)
				continue
			}
			id := newID()[:8]
			for ids[id] {
				id = newID()[:8]
			}
			ids[id] = true
			e["id"] = id
			if prev == "" {
				e["parentId"] = nil
			} else {
				e["parentId"] = prev
			}
			prev = id
			if e.Type() == "compaction" {
				if idx, ok := e["firstKeptEntryIndex"].(float64); ok {
					if int(idx) >= 0 && int(idx) < len(entries) && entries[int(idx)].Type() != "session" {
						e["firstKeptEntryId"] = entries[int(idx)]["id"]
					}
					delete(e, "firstKeptEntryIndex")
				}
			}
			_ = i
		}
	}
	for _, e := range entries {
		if e.Type() == "session" {
			e["version"] = float64(CurrentVersion)
			continue
		}
		if msg := e.Message(); msg != nil && msg["role"] == "hookMessage" {
			msg["role"] = "custom"
		}
	}
}

func (m *Manager) buildIndex() {
	m.byID = map[string]Entry{}
	m.leafID = ""
	for _, e := range m.entries {
		if e.Type() == "session" {
			continue
		}
		m.byID[e.ID()] = e
		m.leafID = e.ID()
	}
}

// SessionID is the session's id.
func (m *Manager) SessionID() string { return m.sessionID }

// Cwd is the working directory the session was made for.
func (m *Manager) Cwd() string { return m.cwd }

// File is the session file path; "" for an in-memory session. It may not exist yet.
func (m *Manager) File() string { return m.file }

// Header is the session header.
func (m *Manager) Header() Entry {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.entries[0]
}

func (m *Manager) hasAssistant() bool {
	for _, e := range m.entries {
		if e.Role() == "assistant" {
			return true
		}
	}
	return false
}

func (m *Manager) writeAll() error {
	f, err := os.OpenFile(m.file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	for _, e := range m.entries {
		if err := writeLine(w, e); err != nil {
			return err
		}
	}
	return w.Flush()
}

func (m *Manager) appendLine(e Entry) error {
	f, err := os.OpenFile(m.file, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	if err := writeLine(w, e); err != nil {
		return err
	}
	return w.Flush()
}

func (m *Manager) persistEntry(e Entry) error {
	if !m.persist || m.file == "" {
		return nil
	}
	if !m.hasAssistant() {
		if m.flushed {
			return m.appendLine(e)
		}
		return nil
	}
	if !m.flushed {
		if err := m.writeAll(); err != nil {
			return err
		}
		m.flushed = true
		return nil
	}
	return m.appendLine(e)
}

func (m *Manager) generateID() string {
	for i := 0; i < 100; i++ {
		id := newID()[:8]
		if _, taken := m.byID[id]; !taken {
			return id
		}
	}
	return uuidLike()
}

func (m *Manager) appendEntry(e Entry) (string, error) {
	m.entries = append(m.entries, e)
	m.byID[e.ID()] = e
	m.leafID = e.ID()
	return e.ID(), m.persistEntry(e)
}

func (m *Manager) child(typ string) Entry {
	e := Entry{"type": typ, "id": m.generateID(), "timestamp": m.timestamp()}
	if m.leafID == "" {
		e["parentId"] = nil
	} else {
		e["parentId"] = m.leafID
	}
	return e
}

// AppendMessage appends a message as a child of the leaf and returns its entry id.
func (m *Manager) AppendMessage(message map[string]any) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.child("message")
	e["message"] = message
	return m.appendEntry(e)
}

// AppendSessionInfo records the session's display name.
func (m *Manager) AppendSessionInfo(name string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.child("session_info")
	e["name"] = strings.TrimSpace(strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(name))
	return m.appendEntry(e)
}

// AppendCompaction records a compaction.
func (m *Manager) AppendCompaction(summary, firstKeptEntryID string, tokensBefore int) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.child("compaction")
	e["summary"], e["firstKeptEntryId"], e["tokensBefore"] = summary, firstKeptEntryID, float64(tokensBefore)
	return m.appendEntry(e)
}

// Append appends an arbitrary entry of the given type as a child of the leaf; fields are merged in.
func (m *Manager) Append(typ string, fields map[string]any) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.child(typ)
	for k, v := range fields {
		e[k] = v
	}
	return m.appendEntry(e)
}

// SessionName is the latest session_info name, "" if none (an empty name clears it).
func (m *Manager) SessionName() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].Type() == "session_info" {
			s, _ := m.entries[i]["name"].(string)
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// LeafID is the current leaf ("" before any entry).
func (m *Manager) LeafID() string { m.mu.Lock(); defer m.mu.Unlock(); return m.leafID }

// LeafEntry is the current leaf entry, or nil.
func (m *Manager) LeafEntry() Entry {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.byID[m.leafID]
}

// Entry looks an entry up by id.
func (m *Manager) Entry(id string) (Entry, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.byID[id]
	return e, ok
}

// Entries are all entries except the header, in file order.
func (m *Manager) Entries() []Entry {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Entry(nil), m.entries[1:]...)
}

// Branch is the path from the root to fromID (the leaf when empty).
func (m *Manager) Branch(fromID string) []Entry {
	m.mu.Lock()
	defer m.mu.Unlock()
	if fromID == "" {
		fromID = m.leafID
	}
	return pathTo(m.byID, fromID)
}

func pathTo(byID map[string]Entry, id string) []Entry {
	var path []Entry
	seen := map[string]bool{}
	for cur, ok := byID[id]; ok && !seen[cur.ID()]; cur, ok = byID[cur.ParentID()] {
		seen[cur.ID()] = true // a corrupt file must not loop forever
		path = append(path, cur)
		if cur.ParentID() == "" {
			break
		}
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

// SetLeaf moves the leaf to an existing entry ("" moves it before the first entry).
func (m *Manager) SetLeaf(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id != "" {
		if _, ok := m.byID[id]; !ok {
			return fmt.Errorf("Entry %s not found", id)
		}
	}
	m.leafID = id
	return nil
}

// ContextEntries is the compaction-aware entry list along the current branch.
func (m *Manager) ContextEntries() []Entry {
	m.mu.Lock()
	defer m.mu.Unlock()
	return BuildContextEntries(m.entries[1:], m.leafID, m.byID)
}

// BuildContextEntries follows the branch to leafID (the last entry when leafID is empty and
// byID has no such entry). If the path holds compaction entries, the latest is represented by the
// compaction entry itself, followed by the kept entries starting at firstKeptEntryId and every
// entry after it; older summarised entries are omitted.
func BuildContextEntries(entries []Entry, leafID string, byID map[string]Entry) []Entry {
	if byID == nil {
		byID = map[string]Entry{}
		for _, e := range entries {
			byID[e.ID()] = e
		}
	}
	leaf, ok := byID[leafID]
	if !ok || leafID == "" {
		if leafID == "" && len(entries) > 0 {
			return nil // an explicit "before any entry" leaf
		}
		if len(entries) == 0 {
			return nil
		}
		leaf = entries[len(entries)-1]
	}
	path := pathTo(byID, leaf.ID())
	compactionIdx := -1
	for i, e := range path {
		if e.Type() == "compaction" {
			compactionIdx = i
		}
	}
	if compactionIdx < 0 {
		return path
	}
	compaction := path[compactionIdx]
	out := []Entry{compaction}
	found := false
	kept, _ := compaction["firstKeptEntryId"].(string)
	for _, e := range path[:compactionIdx] {
		if e.ID() == kept {
			found = true
		}
		if found && !(e.Type() == "message" && e.Role() == "system") {
			out = append(out, e)
		}
	}
	return append(out, path[compactionIdx+1:]...)
}

// ContextSettings is the thinking level and model in effect at the leaf: the last
// thinking_level_change, and the last model_change or assistant message, along the branch.
func (m *Manager) ContextSettings() (thinkingLevel, provider, modelID string) {
	thinkingLevel = "off"
	for _, e := range m.Branch("") {
		switch {
		case e.Type() == "thinking_level_change":
			thinkingLevel, _ = e["thinkingLevel"].(string)
		case e.Type() == "model_change":
			provider, _ = e["provider"].(string)
			modelID, _ = e["modelId"].(string)
		case e.Role() == "assistant":
			provider, _ = e.Message()["provider"].(string)
			modelID, _ = e.Message()["model"].(string)
		}
	}
	return thinkingLevel, provider, modelID
}

// FromEntries builds a read-only manager over entries obtained elsewhere (the host's session
// mirror), which carry no header: one is synthesised from id and cwd. The leaf is the last entry
// unless leafID names another. file is only reported by File.
func FromEntries(cwd, id, file string, entries []Entry, leafID string) (*Manager, error) {
	timestamp := ""
	if len(entries) > 0 {
		timestamp = entries[0].Timestamp()
	}
	header := Entry{"type": "session", "version": CurrentVersion, "id": id, "timestamp": timestamp, "cwd": cwd}
	m := &Manager{file: file, sessionID: id, cwd: cwd, now: time.Now, flushed: true}
	m.entries = append([]Entry{header}, entries...)
	m.buildIndex()
	if leafID != "" {
		if err := m.SetLeaf(leafID); err != nil {
			return nil, err
		}
	}
	return m, nil
}
