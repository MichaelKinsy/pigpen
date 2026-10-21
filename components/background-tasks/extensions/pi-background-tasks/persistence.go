package pi_background_tasks

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Snapshot persistence: the session entry and the sidecar. upstream: extensions/persistence.ts.

// bgTasksBoundedManifest replaces an oversized snapshot entry: sessions are append-only, so a task list with multi-KB
// commands would make session history too large to resume. The sidecar stays canonical.
type bgTasksBoundedManifest struct {
	Version      int            `json:"version"`
	FullSnapshot bool           `json:"fullSnapshot"`
	Reason       string         `json:"reason"`
	ByteSize     int            `json:"byteSize"`
	Fingerprint  string         `json:"fingerprint"`
	Counts       manifestCounts `json:"counts"`
	UpdatedAt    int64          `json:"updatedAt"`
}

type manifestCounts struct {
	Tasks int `json:"tasks"`
}

// isBgTasksBoundedManifest recognizes a bounded manifest: version 2 and fullSnapshot false.
func isBgTasksBoundedManifest(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		if mf, isManifest := v.(*bgTasksBoundedManifest); isManifest {
			return mf != nil && mf.Version == 2 && !mf.FullSnapshot
		}
		return false
	}
	version, _ := m["version"].(float64)
	full, hasFull := m["fullSnapshot"].(bool)
	return version == 2 && hasFull && !full
}

// decodeSnapshots reads snapshot values from JSON-decoded data (or typed ones).
func decodeSnapshots(v any) []snapshot {
	switch list := v.(type) {
	case []snapshot:
		return list
	case []*snapshot:
		out := make([]snapshot, len(list))
		for i, s := range list {
			out[i] = *s
		}
		return out
	case []any:
		out := make([]snapshot, 0, len(list))
		for _, item := range list {
			b, err := json.Marshal(item)
			if err != nil {
				continue
			}
			var s snapshot
			if json.Unmarshal(b, &s) == nil {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// applyCustomEntryWithBarrier handles one session-branch custom entry of the state type. A bounded manifest (fullSnapshot
// false, any version) re-applies the sidecar tasks when a sidecar was loaded: the manifest says the sidecar is authoritative
// as of this point, and without this an older full snapshot earlier in the branch could regress canonical state.
func applyCustomEntryWithBarrier(data any, sidecarLoaded bool, sidecarTasks []snapshot, clear func(), apply func(snapshot)) {
	var tasks any
	if m, ok := data.(map[string]any); ok {
		if full, has := m["fullSnapshot"].(bool); has && !full {
			if sidecarLoaded && sidecarTasks != nil {
				clear()
				for _, s := range sidecarTasks {
					apply(s)
				}
			}
			return
		}
		tasks = m["tasks"]
	} else if payload, ok := data.(persistencePayload); ok {
		tasks = payload.Tasks
	}
	clear()
	for _, s := range decodeSnapshots(tasks) {
		apply(s)
	}
}

// stableSnapshotFingerprint is a fixed-size hex digest of a value's canonical JSON (keys sorted), so an oversized task list
// yields a small manifest.
func stableSnapshotFingerprint(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return ""
	}
	canonical, _ := json.Marshal(generic) // map keys are written in sorted order
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// persistencePayload is what the session entry and the sidecar hold.
type persistencePayload struct {
	Version   int        `json:"version"`
	Tasks     []snapshot `json:"tasks"`
	UpdatedAt int64      `json:"updatedAt"`
}

// persistResult says which channels took the write and why the session entry resolved as it did.
type persistResult struct {
	appendEntry  bool
	sidecar      bool
	appendReason string
}

// sessionRef is what a persist needs of the active context.
type sessionRef struct {
	id          string // session id
	file        string // session file, may be empty
	sidecarPath string
}

type persistenceDeps struct {
	appendEntry func(customType string, payload any) error
	customType  string
	// activeSession returns the active session, ok false without one.
	activeSession func() (sessionRef, bool)
	listSnapshots func() []snapshot
	notify        func(where string)
	maxEntryBytes int
	now           clockFunc
}

var unsafeFileName = regexp.MustCompile(`[^\w.-]+`)

func safeFileName(value string) string { return unsafeFileName.ReplaceAllString(value, "_") }

// sessionIDFor is the session's id: the manager's id when it has one, else the session file's name without its extension,
// else a per-process ephemeral id.
func sessionIDFor(id, file string) string {
	if strings.TrimSpace(id) != "" && jsTrim(id) != "" {
		return id
	}
	if file != "" {
		name := file
		if i := strings.LastIndexAny(name, `/\`); i >= 0 {
			name = name[i+1:]
		}
		return strings.TrimSuffix(name, ".jsonl")
	}
	return fmt.Sprintf("ephemeral-%d", os.Getpid())
}

// sidecarStatePath is `<agent dir>/kendex/sessions/<session>/pi-background-tasks/state.json`.
func sidecarStatePath(sessionID string) string {
	return filepath.Join(agentDirectory(), "kendex", "sessions", safeFileName(sessionID), "pi-background-tasks", "state.json")
}

// writeSidecarAtomic writes a temp file in the same directory and renames it into place, so a crash mid-write cannot shred the
// replay record. The temp file is mode 0600 and the directory 0700, so another user cannot read session state.
func writeSidecarAtomic(file, payload string, now int64) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp.%d.%d", file, os.Getpid(), now)
	if err := os.WriteFile(tmp, []byte(payload), 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, file); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func reportPersistFailure(where string, err error, notify func(where string)) {
	logBackgroundDiagnostic("persistence failed", map[string]any{"where": where, "error": err.Error()})
	if notify != nil {
		notify(where)
	}
}

// persistence is createPersistence's closure.
type persistence struct {
	deps    persistenceDeps
	mu      sync.Mutex
	lastFpr map[string]string // last fingerprint per session id
}

func newPersistence(deps persistenceDeps) *persistence {
	if deps.maxEntryBytes == 0 {
		deps.maxEntryBytes = bgTasksSnapshotMaxBytes
	}
	if deps.now == nil {
		deps.now = realNow
	}
	return &persistence{deps: deps, lastFpr: map[string]string{}}
}

func (p *persistence) payloadFor(tasks []snapshot) persistencePayload {
	if tasks == nil {
		tasks = []snapshot{}
	}
	return persistencePayload{Version: 1, Tasks: tasks, UpdatedAt: p.deps.now()}
}

// persistSnapshots writes the latest snapshot set on both channels independently: the session entry (the in-session JSONL
// custom entry the agent sees on restore through the branch) and the sidecar (read first on session_start). One channel's
// failure does not suppress the other.
func (p *persistence) persistSnapshots() persistResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	payload := p.payloadFor(p.deps.listSnapshots())
	serialized, _ := json.Marshal(payload)
	ref, hasCtx := p.deps.activeSession()
	res := persistResult{sidecar: !hasCtx}
	if !hasCtx {
		res.appendReason = "no-active-context"
	}
	maxBytes := p.deps.maxEntryBytes
	if hasCtx {
		fingerprint := stableSnapshotFingerprint(map[string]any{"tasks": payload.Tasks})
		if p.lastFpr[ref.id] == fingerprint {
			res.appendEntry = true
			res.appendReason = "unchanged"
		} else {
			byteSize := len(serialized)
			if byteSize <= maxBytes {
				if err := p.deps.appendEntry(p.deps.customType, payload); err != nil {
					res.appendReason = "error"
					reportPersistFailure("appendEntry", err, p.deps.notify)
				} else {
					res.appendReason = "appended"
					p.lastFpr[ref.id] = fingerprint
					res.appendEntry = true
				}
			} else {
				manifest := bgTasksBoundedManifest{Version: 2, FullSnapshot: false, Reason: "payload-too-large", ByteSize: byteSize, Fingerprint: fingerprint,
					Counts: manifestCounts{Tasks: len(payload.Tasks)}, UpdatedAt: payload.UpdatedAt}
				mb, _ := json.Marshal(manifest)
				if len(mb) > maxBytes {
					res.appendReason = "manifest-oversize-skipped"
					reportPersistFailure("appendEntry", fmt.Errorf("bounded manifest %dB exceeds cap %dB; sidecar remains canonical", len(mb), maxBytes), p.deps.notify)
				} else if err := p.deps.appendEntry(p.deps.customType, manifest); err != nil {
					res.appendReason = "error"
					reportPersistFailure("appendEntry", err, p.deps.notify)
				} else {
					res.appendReason = "manifest"
					p.lastFpr[ref.id] = fingerprint
					res.appendEntry = true
				}
			}
		}
		if err := writeSidecarAtomic(ref.sidecarPath, string(serialized)+"\n", p.deps.now()); err != nil {
			reportPersistFailure("sidecar", err, p.deps.notify)
		} else {
			res.sidecar = true
		}
		return res
	}
	// No active context: the unconditional append, so a one-shot call still records something.
	if err := p.deps.appendEntry(p.deps.customType, payload); err != nil {
		res.appendReason = "error"
		reportPersistFailure("appendEntry", err, p.deps.notify)
	} else {
		res.appendEntry = true
	}
	return res
}
