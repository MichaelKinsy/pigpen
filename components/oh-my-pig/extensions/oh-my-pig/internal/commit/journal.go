package commit

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// upstream: packages/coding-agent/src/commit/selective/journal.ts

const (
	journalFile      = "changes.jsonl"
	blobDir          = "blobs"
	maxSnapshotChars = 8 * 1024 * 1024
	inlineDeltaChars = 4096
	maxDeltaEdits    = 1024
	tipCacheChars    = 32 * 1024 * 1024
)

// JournalAgent identifies the agent a journal record belongs to.
// upstream: journal.ts:41
type JournalAgent struct {
	Agent  string `json:"agent"`
	Parent string `json:"parent,omitempty"`
}

// DeltaEntry is one `[start, removed, inserted]` tuple in a LineDelta.
// upstream: journal.ts:53
type DeltaEntry struct {
	Start    int
	Removed  int
	Inserted []string
}

// MarshalJSON encodes DeltaEntry as `[start, removed, inserted]`.
func (d DeltaEntry) MarshalJSON() ([]byte, error) {
	ins := d.Inserted
	if ins == nil {
		ins = []string{}
	}
	return json.Marshal([]any{d.Start, d.Removed, ins})
}

// UnmarshalJSON decodes `[start, removed, inserted]`.
func (d *DeltaEntry) UnmarshalJSON(data []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if len(raw) != 3 {
		return fmt.Errorf("delta entry must have 3 elements, got %d", len(raw))
	}
	if err := json.Unmarshal(raw[0], &d.Start); err != nil {
		return err
	}
	if err := json.Unmarshal(raw[1], &d.Removed); err != nil {
		return err
	}
	if err := json.Unmarshal(raw[2], &d.Inserted); err != nil {
		return err
	}
	if d.Inserted == nil {
		d.Inserted = []string{}
	}
	return nil
}

// LineDelta is a list of changed regions turning one content into another.
type LineDelta []DeltaEntry

// StoredDelta holds either an inline LineDelta or a blob key string.
// upstream: journal.ts:56
type StoredDelta struct {
	Present bool
	BlobKey string
	Inline  LineDelta
}

func (s StoredDelta) MarshalJSON() ([]byte, error) {
	if s.BlobKey != "" {
		return json.Marshal(s.BlobKey)
	}
	if s.Inline == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(s.Inline)
}

func (s *StoredDelta) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*s = StoredDelta{}
		return nil
	}
	if trimmed[0] == '"' {
		var key string
		if err := json.Unmarshal(trimmed, &key); err != nil {
			return err
		}
		*s = StoredDelta{Present: true, BlobKey: key}
		return nil
	}
	var delta LineDelta
	if err := json.Unmarshal(trimmed, &delta); err != nil {
		return err
	}
	*s = StoredDelta{Present: true, Inline: delta}
	return nil
}

// OptionalBase distinguishes absent `base`, `base: null` (file did not exist), and `base: "<key>"`.
type OptionalBase struct {
	Set  bool
	Null bool
	Key  string
}

func (b OptionalBase) MarshalJSON() ([]byte, error) {
	if b.Null {
		return []byte("null"), nil
	}
	return json.Marshal(b.Key)
}

func (b *OptionalBase) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if bytes.Equal(trimmed, []byte("null")) {
		*b = OptionalBase{Set: true, Null: true}
		return nil
	}
	var key string
	if err := json.Unmarshal(trimmed, &key); err != nil {
		return err
	}
	*b = OptionalBase{Set: true, Key: key}
	return nil
}

// JournalRecord represents either a ChangeRecord ("change") or CommitRecord ("commit").
// upstream: journal.ts:62-93
type JournalRecord struct {
	Type   string `json:"type"`
	At     int64  `json:"at"`
	Agent  string `json:"agent"`
	Parent string `json:"parent,omitempty"`

	// ChangeRecord fields
	Tool string       `json:"tool,omitempty"`
	Path string       `json:"path,omitempty"`
	Base OptionalBase `json:"-"`
	Gap  StoredDelta  `json:"-"`
	Edit StoredDelta  `json:"-"`
	Sum  string       `json:"sum,omitempty"`

	// CommitRecord fields
	SHA   string   `json:"sha,omitempty"`
	Paths []string `json:"paths,omitempty"`
}

func (r JournalRecord) MarshalJSON() ([]byte, error) {
	if r.Type == "commit" {
		type wireCommit struct {
			Type   string   `json:"type"`
			At     int64    `json:"at"`
			Agent  string   `json:"agent"`
			Parent string   `json:"parent,omitempty"`
			SHA    string   `json:"sha"`
			Paths  []string `json:"paths"`
		}
		paths := r.Paths
		if paths == nil {
			paths = []string{}
		}
		return json.Marshal(wireCommit{
			Type:   "commit",
			At:     r.At,
			Agent:  r.Agent,
			Parent: r.Parent,
			SHA:    r.SHA,
			Paths:  paths,
		})
	}
	m := make(map[string]any, 10)
	m["type"] = "change"
	m["at"] = r.At
	m["agent"] = r.Agent
	if r.Parent != "" {
		m["parent"] = r.Parent
	}
	m["tool"] = r.Tool
	m["path"] = r.Path
	if r.Base.Set {
		if r.Base.Null {
			m["base"] = nil
		} else {
			m["base"] = r.Base.Key
		}
	}
	if r.Gap.Present {
		m["gap"] = r.Gap
	}
	if r.Edit.Present {
		m["edit"] = r.Edit
	}
	if r.Sum != "" {
		m["sum"] = r.Sum
	}
	return json.Marshal(m)
}

func (r *JournalRecord) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var out JournalRecord
	if v, ok := raw["type"]; ok {
		if err := json.Unmarshal(v, &out.Type); err != nil {
			return err
		}
	}
	if v, ok := raw["at"]; ok {
		if err := json.Unmarshal(v, &out.At); err != nil {
			return err
		}
	}
	if v, ok := raw["agent"]; ok {
		if err := json.Unmarshal(v, &out.Agent); err != nil {
			return err
		}
	}
	if v, ok := raw["parent"]; ok && !bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
		if err := json.Unmarshal(v, &out.Parent); err != nil {
			return err
		}
	}
	if out.Type == "commit" {
		if v, ok := raw["sha"]; ok {
			if err := json.Unmarshal(v, &out.SHA); err != nil {
				return err
			}
		}
		if v, ok := raw["paths"]; ok {
			if err := json.Unmarshal(v, &out.Paths); err != nil {
				return err
			}
		}
		*r = out
		return nil
	}
	if v, ok := raw["tool"]; ok {
		if err := json.Unmarshal(v, &out.Tool); err != nil {
			return err
		}
	}
	if v, ok := raw["path"]; ok {
		if err := json.Unmarshal(v, &out.Path); err != nil {
			return err
		}
	}
	if v, ok := raw["base"]; ok {
		if err := json.Unmarshal(v, &out.Base); err != nil {
			return err
		}
	}
	if v, ok := raw["gap"]; ok {
		if err := json.Unmarshal(v, &out.Gap); err != nil {
			return err
		}
	}
	if v, ok := raw["edit"]; ok {
		if err := json.Unmarshal(v, &out.Edit); err != nil {
			return err
		}
	}
	if v, ok := raw["sum"]; ok {
		if err := json.Unmarshal(v, &out.Sum); err != nil {
			return err
		}
	}
	*r = out
	return nil
}

// ChangeInput is the content snapshot passed to ChangeJournal.RecordChange.
// nil Before/After means file absent.
// upstream: journal.ts:96
type ChangeInput struct {
	Agent  string
	Parent string
	Tool   string
	Path   string
	Before *string
	After  *string
}

// RecordedWrite is one recorded write with the content around it, replayed by ChangeJournal.Snapshots.
// upstream: journal.ts:104
type RecordedWrite struct {
	Agent  string
	Before *string
	After  *string
}

// FileSnapshot is a file state read before a write.
// upstream: journal.ts:113
type FileSnapshot struct {
	Text *string
}

// ReadSnapshot reads file before writing it; returns (nil, false) when it cannot be read.
// A missing file returns (&FileSnapshot{Text: nil}, true).
// upstream: journal.ts:119
func ReadSnapshot(file string) (*FileSnapshot, bool) {
	data, err := os.ReadFile(file)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &FileSnapshot{Text: nil}, true
		}
		return nil, false
	}
	s := string(data)
	return &FileSnapshot{Text: &s}, true
}

type contentState struct {
	lines []string
	text  *string
}

// contentSum computes a fast content checksum formatted in base-36 (like omp's Bun.hash(...).toString(36)).
// upstream: journal.ts:145
func contentSum(text *string) string {
	if text == nil {
		return "-"
	}
	return hashBytesBase36([]byte(*text))
}

func hashBytesBase36(data []byte) string {
	sum := sha256.Sum256(data)
	v := binary.LittleEndian.Uint64(sum[:8])
	return strconv.FormatUint(v, 36)
}

func lineDelta(from, to []string) LineDelta {
	shorter := min(len(from), len(to))
	head := 0
	for head < shorter && from[head] == to[head] {
		head++
	}
	tail := 0
	for tail < shorter-head && from[len(from)-1-tail] == to[len(to)-1-tail] {
		tail++
	}
	a := from[head : len(from)-tail]
	b := to[head : len(to)-tail]
	regions, ok := ChangedRegionsWithin(a, b, maxDeltaEdits)
	if !ok {
		regions = []Region{{I1: 0, I2: len(a), J1: 0, J2: len(b)}}
	}
	out := make(LineDelta, len(regions))
	for i, reg := range regions {
		ins := make([]string, reg.J2-reg.J1)
		copy(ins, b[reg.J1:reg.J2])
		out[i] = DeltaEntry{
			Start:    head + reg.I1,
			Removed:  reg.I2 - reg.I1,
			Inserted: ins,
		}
	}
	return out
}

func applyDelta(lines []string, delta LineDelta) ([]string, bool) {
	if len(delta) == 0 {
		return lines, true
	}
	var out []string
	at := 0
	for _, d := range delta {
		if d.Start < at || d.Start+d.Removed > len(lines) {
			return nil, false
		}
		out = append(out, lines[at:d.Start]...)
		out = append(out, d.Inserted...)
		at = d.Start + d.Removed
	}
	out = append(out, lines[at:]...)
	return out, true
}

var (
	journalsMu sync.Mutex
	journals   = map[string]*ChangeJournal{}
)

type tipEntry struct {
	path string
	text *string
}

// ChangeJournal is the append-only change journal for one artifacts directory,
// shared by every session writing to it.
// upstream: journal.ts:186
type ChangeJournal struct {
	Dir string

	mu          sync.Mutex
	dirReady    bool
	storedBlobs map[string]bool
	tips        []tipEntry // LRU order, oldest first
	tipChars    int
}

// NewChangeJournal creates a standalone ChangeJournal for dir (used by tests).
func NewChangeJournal(dir string) *ChangeJournal {
	return &ChangeJournal{
		Dir:         dir,
		storedBlobs: make(map[string]bool),
	}
}

// JournalAt returns the process-wide ChangeJournal for artifactsDir.
// upstream: ChangeJournal.at (journal.ts:203)
func JournalAt(artifactsDir string) *ChangeJournal {
	journalsMu.Lock()
	defer journalsMu.Unlock()
	if j, ok := journals[artifactsDir]; ok {
		return j
	}
	j := NewChangeJournal(artifactsDir)
	journals[artifactsDir] = j
	return j
}

func equalStringPtr(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func storableText(text *string) bool {
	if text == nil {
		return true
	}
	return len(*text) <= maxSnapshotChars && !strings.ContainsRune(*text, 0)
}

// RecordChange records a file write. Failures are ignored so a full disk cannot fail an edit.
// upstream: journal.ts:216
func (j *ChangeJournal) RecordChange(input ChangeInput) {
	if equalStringPtr(input.Before, input.After) {
		return
	}
	at := time.Now().UnixMilli()
	j.mu.Lock()
	defer j.mu.Unlock()

	rec := JournalRecord{
		Type:   "change",
		At:     at,
		Agent:  input.Agent,
		Parent: input.Parent,
		Tool:   input.Tool,
		Path:   input.Path,
	}

	tip, hasTip := j.takeTipLocked(input.Path)
	stored := storableText(input.Before) && storableText(input.After)
	if stored {
		beforeLines := ToLines(input.Before)
		if !hasTip {
			if input.Before == nil {
				rec.Base = OptionalBase{Set: true, Null: true}
			} else {
				key, err := j.storeBlobLocked(*input.Before)
				if err != nil {
					return
				}
				rec.Base = OptionalBase{Set: true, Key: key}
			}
		} else if !equalStringPtr(tip, input.Before) {
			gap, err := j.storeDeltaLocked(lineDelta(ToLines(tip), beforeLines))
			if err != nil {
				return
			}
			rec.Gap = gap
		}
		edit, err := j.storeDeltaLocked(lineDelta(beforeLines, ToLines(input.After)))
		if err != nil {
			return
		}
		rec.Edit = edit
		rec.Sum = contentSum(input.After)
	}
	if err := j.appendLocked(rec); err != nil {
		return
	}
	if stored {
		j.setTipLocked(input.Path, input.After)
	}
}

// RecordCommit appends a CommitRecord to disk.
// upstream: journal.ts:251
func (j *ChangeJournal) RecordCommit(agent JournalAgent, sha string, paths []string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.appendLocked(JournalRecord{
		Type:   "commit",
		At:     time.Now().UnixMilli(),
		Agent:  agent.Agent,
		Parent: agent.Parent,
		SHA:    sha,
		Paths:  paths,
	})
}

// Flush waits for queued appends (synchronous under j.mu in Go).
func (j *ChangeJournal) Flush() {
	j.mu.Lock()
	j.mu.Unlock()
}

// Read returns every valid record in changes.jsonl, oldest first.
// upstream: journal.ts:262
func (j *ChangeJournal) Read() ([]JournalRecord, error) {
	j.mu.Lock()
	data, err := os.ReadFile(filepath.Join(j.Dir, journalFile))
	j.mu.Unlock()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var records []JournalRecord
	for line := range strings.SplitSeq(string(data), "\n") {
		if line == "" {
			continue
		}
		var rec JournalRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			// A torn final line from a crash mid-append; earlier records stay valid.
			continue
		}
		records = append(records, rec)
	}
	return records, nil
}

// Snapshots replays one path's change records (oldest first) into the content around each write.
// Returns (nil, false, nil) when no write can be replayed.
// upstream: journal.ts:290
func (j *ChangeJournal) Snapshots(records []JournalRecord) ([]RecordedWrite, bool, error) {
	var writes []RecordedWrite
	var tip *contentState
	for _, rec := range records {
		before, err := j.contentBefore(rec, tip)
		if err != nil {
			return nil, false, err
		}
		var edit LineDelta
		hasEdit := false
		if rec.Edit.Present {
			var ok bool
			edit, ok, err = j.loadDelta(rec.Edit)
			if err != nil {
				return nil, false, err
			}
			hasEdit = ok
		}
		var nextTip *contentState
		if before != nil && hasEdit {
			if lines, ok := applyDelta(before.lines, edit); ok {
				nextTip = &contentState{lines: lines, text: FromLines(lines)}
			}
		}
		tip = nextTip
		if before == nil || tip == nil || contentSum(tip.text) != rec.Sum {
			writes = nil
			tip = nil
			continue
		}
		writes = append(writes, RecordedWrite{
			Agent:  rec.Agent,
			Before: before.text,
			After:  tip.text,
		})
	}
	if len(writes) == 0 {
		return nil, false, nil
	}
	return writes, true, nil
}

func (j *ChangeJournal) contentBefore(rec JournalRecord, tip *contentState) (*contentState, error) {
	if rec.Base.Set {
		if rec.Base.Null {
			return &contentState{lines: nil, text: nil}, nil
		}
		text, ok, err := j.loadBlob(rec.Base.Key)
		if err != nil || !ok {
			return nil, err
		}
		return &contentState{lines: ToLines(&text), text: &text}, nil
	}
	if tip == nil || !rec.Gap.Present {
		return tip, nil
	}
	gap, ok, err := j.loadDelta(rec.Gap)
	if err != nil || !ok {
		return nil, err
	}
	lines, applied := applyDelta(tip.lines, gap)
	if !applied {
		return nil, nil
	}
	return &contentState{lines: lines, text: FromLines(lines)}, nil
}

func (j *ChangeJournal) ensureDirLocked() error {
	if j.dirReady {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(j.Dir, blobDir), 0o755); err != nil {
		return err
	}
	j.dirReady = true
	return nil
}

func (j *ChangeJournal) appendLocked(rec JournalRecord) error {
	if err := j.ensureDirLocked(); err != nil {
		return err
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	f, err := os.OpenFile(filepath.Join(j.Dir, journalFile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.Write(raw)
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	return cerr
}

func (j *ChangeJournal) takeTipLocked(file string) (*string, bool) {
	for i, e := range j.tips {
		if e.path == file {
			j.tips = append(j.tips[:i], j.tips[i+1:]...)
			if e.text != nil {
				j.tipChars -= len(*e.text)
			}
			return e.text, true
		}
	}
	return nil, false
}

func (j *ChangeJournal) setTipLocked(file string, text *string) {
	var cp *string
	if text != nil {
		s := *text
		cp = &s
		j.tipChars += len(s)
	}
	j.tips = append(j.tips, tipEntry{path: file, text: cp})
	for len(j.tips) > 0 && j.tipChars > tipCacheChars {
		oldest := j.tips[0]
		j.tips = j.tips[1:]
		if oldest.text != nil {
			j.tipChars -= len(*oldest.text)
		}
	}
}

func (j *ChangeJournal) storeDeltaLocked(delta LineDelta) (StoredDelta, error) {
	raw, err := json.Marshal(delta)
	if err != nil {
		return StoredDelta{}, err
	}
	if len(raw) > inlineDeltaChars {
		key, err := j.storeBlobLocked(string(raw))
		if err != nil {
			return StoredDelta{}, err
		}
		return StoredDelta{Present: true, BlobKey: key}, nil
	}
	return StoredDelta{Present: true, Inline: delta}, nil
}

func (j *ChangeJournal) loadDelta(stored StoredDelta) (LineDelta, bool, error) {
	if !stored.Present {
		return nil, false, nil
	}
	if stored.BlobKey == "" {
		return stored.Inline, true, nil
	}
	raw, ok, err := j.loadBlob(stored.BlobKey)
	if err != nil || !ok {
		return nil, false, err
	}
	var delta LineDelta
	if err := json.Unmarshal([]byte(raw), &delta); err != nil {
		return nil, false, nil
	}
	return delta, true, nil
}

func (j *ChangeJournal) storeBlobLocked(content string) (string, error) {
	raw := []byte(content)
	key := strconv.FormatInt(int64(len(raw)), 36) + "-" + hashBytesBase36(raw)
	if j.storedBlobs[key] {
		return key, nil
	}
	if err := j.ensureDirLocked(); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(raw); err != nil {
		_ = gz.Close()
		return "", err
	}
	if err := gz.Close(); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(j.Dir, blobDir, key), buf.Bytes(), 0o644); err != nil {
		return "", err
	}
	j.storedBlobs[key] = true
	return key, nil
}

func (j *ChangeJournal) loadBlob(key string) (string, bool, error) {
	data, err := os.ReadFile(filepath.Join(j.Dir, blobDir, key))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return "", false, err
	}
	defer gz.Close()
	out, err := io.ReadAll(gz)
	if err != nil {
		return "", false, err
	}
	return string(out), true, nil
}
