// Package settings implements omp's layered settings store and the `cfg://` protocol.
//
// Lock ordering invariant (NEVER hold two locks simultaneously except where noted):
//  1. `sessionsMu` protects only the `sessions` / `endedSet` / `endedRing` /
//     `overrideHook` maps. NEVER acquire any `Store.mu` or `sharedGlobalLayer.mu`
//     while holding `sessionsMu`, and NEVER acquire `sessionsMu` while holding
//     any `Store.mu`. Collect `*Store` pointers under `sessionsMu`, release it,
//     then lock each `Store.mu` individually.
//  2. Ancestor overrides are resolved BEFORE acquiring a child's `Store.mu`
//     (`collectParentOverrides`), locking each ancestor's `Store.mu` one at a time
//     with no other lock held.
//  3. `withConfigFileLock` (per-path mutex + OS advisory lock) may acquire
//     `sharedGlobalLayer.mu` strictly to publish the new snapshot atomically
//     before releasing the file lock (so concurrent readers never observe the
//     new file before the in-process snapshot updates). `sharedGlobalLayer.mu`
//     never acquires `withConfigFileLock`, `sessionsMu`, or `Store.mu`.
//  4. `OnChange` callbacks (`changeListenersMu`) are ALWAYS invoked after ALL
//     locks have been released.
package settings

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Provenance identifies the settings layer supplying an effective value.
// upstream: packages/coding-agent/src/config/settings.ts SettingProvenance
type Provenance string

const (
	ProvenanceEnv     Provenance = "env"
	ProvenanceRuntime Provenance = "runtime"
	ProvenanceOverlay Provenance = "overlay"
	ProvenanceProject Provenance = "project"
	ProvenanceGlobal  Provenance = "global"
	ProvenanceDefault Provenance = "default"
)

// EnvConfigFiles is this Piglet's own overlay path-list environment variable
// (analogous to omp's PI_CONFIG_FILES, isolated under the PIG_ prefix so real
// omp overlays do not leak into oh-my-pig).
const EnvConfigFiles = "PIG_CONFIG_FILES"

const maxSymlinkHops = 40

// ParentSession resolves the parent session ID of a subagent session so child
// sessions read through to their parent's runtime overrides (N5; upstream
// config/settings.ts:981). Assigned by the integrator from internal/agents;
// nil-safe when no subagent hierarchy is active.
var ParentSession func(ctx sdk.Context) (parentSessionID string, ok bool)

// ── Change listeners (OnChange) ──────────────────────────────────────────────

type changeListenerEntry struct {
	id uint64
	fn func(sessionID, key string)
}

var (
	changeListenersMu sync.RWMutex
	changeListeners   []changeListenerEntry
	nextListenerID    atomic.Uint64
)

// OnChange registers a callback invoked whenever a setting's effective value
// changes for any live session:
//   - a session override or ClearOverride (for the session and any live
//     descendant subagent sessions whose effective value changed),
//   - a persisted /save (for every live session sharing that global config
//     whose effective value changed), or
//   - an external edit to the global config file picked up on refresh (for
//     every live session sharing that global config whose effective value
//     changed).
//
// Callbacks are NEVER invoked for an ended session (after releaseSession /
// lifecycle.End) and NEVER invoked while holding any store or registry lock
// (safe for re-entrant reads or writes). Returns an idempotent unsubscribe
// function.
func OnChange(fn func(sessionID, key string)) (remove func()) {
	if fn == nil {
		return func() {}
	}
	id := nextListenerID.Add(1)
	changeListenersMu.Lock()
	changeListeners = append(changeListeners, changeListenerEntry{id: id, fn: fn})
	changeListenersMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			changeListenersMu.Lock()
			for i, entry := range changeListeners {
				if entry.id == id {
					changeListeners = append(changeListeners[:i], changeListeners[i+1:]...)
					break
				}
			}
			changeListenersMu.Unlock()
		})
	}
}

func hasChangeListeners() bool {
	changeListenersMu.RLock()
	n := len(changeListeners)
	changeListenersMu.RUnlock()
	return n > 0
}

func emitOnChange(sessionID, key string) {
	if sessionID != "" && isSessionEnded(sessionID) {
		return
	}
	changeListenersMu.RLock()
	if len(changeListeners) == 0 {
		changeListenersMu.RUnlock()
		return
	}
	snapshot := make([]func(string, string), len(changeListeners))
	for i, e := range changeListeners {
		snapshot[i] = e.fn
	}
	changeListenersMu.RUnlock()
	for _, fn := range snapshot {
		func() {
			defer func() { _ = recover() }()
			fn(sessionID, key)
		}()
	}
}

type sessionValueSnap struct {
	sessionID string
	store     *Store
	prev      any
}

func snapshotAffectedSessionsForKey(targetStore *Store, key string, includeGlobalPeers bool) []sessionValueSnap {
	if !hasChangeListeners() {
		return nil
	}
	targetStore.mu.RLock()
	targetID := targetStore.sessionID
	targetSG := targetStore.sharedGlobal
	targetStore.mu.RUnlock()

	type candidate struct {
		id         string
		store      *Store
		descendant bool
	}
	var raw []candidate

	// Collect candidate pointers under sessionsMu ONLY; NEVER lock Store.mu while holding sessionsMu (N1).
	sessionsMu.Lock()
	for sid, st := range sessions {
		if st == nil || st.store == nil || isSessionEndedLocked(sid) {
			continue
		}
		isDesc := targetID != "" && isDescendantSessionLocked(sid, targetID)
		if includeGlobalPeers || isDesc {
			raw = append(raw, candidate{id: sid, store: st.store, descendant: isDesc})
		}
	}
	sessionsMu.Unlock()

	var candidates []*Store
	seen := map[*Store]bool{}
	if targetID == "" || !isSessionEnded(targetID) {
		candidates = append(candidates, targetStore)
		seen[targetStore] = true
	}
	for _, c := range raw {
		if seen[c.store] {
			continue
		}
		if c.descendant {
			candidates = append(candidates, c.store)
			seen[c.store] = true
			continue
		}
		if includeGlobalPeers && targetSG != nil {
			c.store.mu.RLock()
			sameSG := c.store.sharedGlobal == targetSG
			c.store.mu.RUnlock()
			if sameSG {
				candidates = append(candidates, c.store)
				seen[c.store] = true
			}
		}
	}

	out := make([]sessionValueSnap, 0, len(candidates))
	for _, st := range candidates {
		st.mu.RLock()
		sid := st.sessionID
		st.mu.RUnlock()
		if sid != "" && isSessionEnded(sid) {
			continue
		}
		val, _ := st.getOrderedNoSync(key)
		out = append(out, sessionValueSnap{sessionID: sid, store: st, prev: val})
	}
	return out
}

func emitDiffsForKey(snaps []sessionValueSnap, key string) {
	for _, sn := range snaps {
		if sn.sessionID != "" && isSessionEnded(sn.sessionID) {
			continue
		}
		next, _ := sn.store.getOrdered(key)
		if !orderedValuesEqual(sn.prev, next) {
			emitOnChange(sn.sessionID, key)
		}
	}
}

// notifyGlobalExternalRefresh compares oldGlobal vs newGlobal for every live
// session sharing sg and emits OnChange for each (sessionID, key) whose
// effective value changed (T4, N1, N4).
func notifyGlobalExternalRefresh(sg *sharedGlobalLayer, oldGlobal, newGlobal *OrderedMap) {
	if sg == nil || !hasChangeListeners() {
		return
	}
	changedKeys := diffGlobalSchemaKeys(oldGlobal, newGlobal)
	if len(changedKeys) == 0 {
		return
	}
	notifyGlobalKeysForPeers(sg, nil, changedKeys, oldGlobal, newGlobal)
}

func notifyGlobalKeysForPeers(sg *sharedGlobalLayer, extraStore *Store, keys []string, oldGlobal, newGlobal *OrderedMap) {
	if sg == nil || !hasChangeListeners() || len(keys) == 0 {
		return
	}
	type liveEntry struct {
		id    string
		store *Store
	}
	// Collect all live session stores under sessionsMu ONLY; filter by sg after releasing sessionsMu (N1).
	sessionsMu.Lock()
	allLive := make([]liveEntry, 0, len(sessions))
	for sid, st := range sessions {
		if st == nil || st.store == nil || isSessionEndedLocked(sid) {
			continue
		}
		allLive = append(allLive, liveEntry{id: sid, store: st.store})
	}
	sessionsMu.Unlock()

	var peers []liveEntry
	seen := map[*Store]bool{}
	for _, item := range allLive {
		item.store.mu.RLock()
		sameSG := item.store.sharedGlobal == sg
		item.store.mu.RUnlock()
		if sameSG {
			peers = append(peers, item)
			seen[item.store] = true
		}
	}
	if extraStore != nil && !seen[extraStore] {
		extraStore.mu.RLock()
		sid := extraStore.sessionID
		extraStore.mu.RUnlock()
		if sid == "" || !isSessionEnded(sid) {
			peers = append(peers, liveEntry{id: sid, store: extraStore})
		}
	}

	for _, p := range peers {
		// Resolve parent overrides BEFORE taking p.store.mu (N1).
		parentOv := p.store.collectParentOverrides()
		for _, k := range keys {
			if p.id != "" && isSessionEnded(p.id) {
				break
			}
			entry, ok := Lookup(k)
			if !ok {
				continue
			}
			p.store.mu.RLock()
			prevVal := p.store.effectiveValueWithGlobalLocked(entry, oldGlobal, parentOv)
			nextVal := p.store.effectiveValueWithGlobalLocked(entry, newGlobal, parentOv)
			p.store.mu.RUnlock()
			if !orderedValuesEqual(prevVal, nextVal) {
				emitOnChange(p.id, k)
			}
		}
	}
}
func diffGlobalSchemaKeys(oldGlobal, newGlobal *OrderedMap) []string {

	loadSchema()
	var changed []string
	for i := range schemaAll {
		e := &schemaAll[i]
		ov, ok1 := getByPathOrdered(oldGlobal, e.Segments)
		nv, ok2 := getByPathOrdered(newGlobal, e.Segments)
		if ok1 != ok2 || !orderedValuesEqual(ov, nv) {
			changed = append(changed, e.ID)
		}
	}
	return changed
}

// ── Shared global config layer (one per resolved file path: N2, N3, N6) ──────

type fileStatSig struct {
	exists  bool
	mtimeNs int64
	ctimeNs int64
	dev     uint64
	ino     uint64
	size    int64
}

func statFileSig(p string) fileStatSig {
	info, err := os.Stat(p)
	if err != nil {
		return fileStatSig{}
	}
	dev, ino, ctimeNs := platformStatExtras(info)
	return fileStatSig{
		exists:  true,
		mtimeNs: info.ModTime().UnixNano(),
		ctimeNs: ctimeNs,
		dev:     dev,
		ino:     ino,
		size:    info.Size(),
	}
}

// sharedGlobalLayer holds one global config.yml's immutable parsed snapshot and
// stat signature (dev, inode, ctime, mtime, size), shared by every Store /
// session pointing at the same resolved path so saves are immediately visible
// across sessions (N6) and malformed files are cached until the file changes (N3).
// `data` is treated as immutable after publication, so steady-state reads return
// the version + pointer without cloning (T7).
type sharedGlobalLayer struct {
	mu           sync.RWMutex
	path         string // logical path (e.g. <agentDir>/config.yml)
	loaded       bool
	sig          fileStatSig
	data         *OrderedMap // immutable snapshot; never mutated in place
	loadErr      error
	version      uint64
	saveSeq      uint64       // bumped inside persistKey on publication; reloadIfUninterrupted aborts if saveSeq moved (N4)
	saveInFlight atomic.Int32 // >0 while persistKey is executing on this path (N4)
}

var (
	sharedGlobalsMu sync.Mutex
	sharedGlobals   = map[string]*sharedGlobalLayer{}
)

func getSharedGlobalLayer(configPath string) *sharedGlobalLayer {
	key := canonicalGlobalKey(configPath)
	sharedGlobalsMu.Lock()
	defer sharedGlobalsMu.Unlock()
	if g, ok := sharedGlobals[key]; ok {
		return g
	}
	g := &sharedGlobalLayer{
		path: configPath,
		data: NewOrderedMap(),
	}
	sharedGlobals[key] = g
	return g
}

func canonicalGlobalKey(configPath string) string {
	if resolved, err := resolveYAMLWritePath(configPath); err == nil {
		return filepath.Clean(resolved)
	}
	if abs, err := filepath.Abs(configPath); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(configPath)
}

// ensureFresh stats the file and re-parses ONLY if not yet loaded or its
// (dev, inode, ctime, mtime, size) signature changed (N3, N6, T6).
// When curVer already matches the current generation and the stat signature is
// unchanged, returns (nil, loadErr, ver, false) with zero allocations/clones (T7).
func (g *sharedGlobalLayer) ensureFresh(cwd string, curVer uint64) (data *OrderedMap, loadErr error, ver uint64, updated bool) {
	// If an in-process save is currently writing & publishing this file, serve the
	// last published snapshot so concurrent readers do not trigger a duplicate
	// external-edit OnChange before persistKey publishes inside its lock (N4).
	if g.saveInFlight.Load() > 0 {
		g.mu.RLock()
		if g.loaded {
			ver = g.version
			loadErr = g.loadErr
			if ver != curVer {
				data = g.data
				updated = true
			}
			g.mu.RUnlock()
			return data, loadErr, ver, updated
		}
		g.mu.RUnlock()
	}
	g.mu.RLock()
	startSaveSeq := g.saveSeq
	g.mu.RUnlock()

	sig := statFileSig(g.path)
	g.mu.RLock()
	if g.loaded && g.sig == sig {
		ver = g.version
		loadErr = g.loadErr
		if ver != curVer {
			data = g.data
			updated = true
		}
		g.mu.RUnlock()
		return data, loadErr, ver, updated
	}
	g.mu.RUnlock()

	oldData, newData, newErr, newVer, changed := g.reloadIfUninterrupted(cwd, startSaveSeq)
	// Emit OnChange on external edits INCLUDING transitions to a load error
	// (where previously configured keys fall back to defaults, N4).
	if changed {
		notifyGlobalExternalRefresh(g, oldData, newData)
	}
	if newVer == curVer {
		return nil, newErr, newVer, false
	}
	return newData, newErr, newVer, true
}

func (g *sharedGlobalLayer) reload(cwd string) (*OrderedMap, error, uint64) {
	_, data, err, ver, _ := g.reloadWithOld(cwd)
	return data, err, ver
}

func (g *sharedGlobalLayer) reloadWithOld(cwd string) (oldData, newData *OrderedMap, loadErr error, ver uint64, wasLoaded bool) {
	sig := statFileSig(g.path)
	parsed, loadErr := parseGlobalLayerFile(g.path, cwd)
	g.mu.Lock()
	wasLoaded = g.loaded
	oldData = g.data
	g.loaded = true
	g.sig = sig
	g.data = parsed
	g.loadErr = loadErr
	g.version++
	ver = g.version
	newData = g.data
	g.mu.Unlock()
	return oldData, newData, loadErr, ver, wasLoaded
}

// reloadIfUninterrupted re-reads g.path from disk, but discards its read if an
// in-process save ran or started since startSaveSeq (preventing a stale reader
// from overwriting a newer save's snapshot or emitting duplicate OnChange, N4).
func (g *sharedGlobalLayer) reloadIfUninterrupted(cwd string, startSaveSeq uint64) (oldData, newData *OrderedMap, loadErr error, ver uint64, emittedChange bool) {
	sig := statFileSig(g.path)
	parsed, perr := parseGlobalLayerFile(g.path, cwd)

	g.mu.Lock()
	if g.saveInFlight.Load() > 0 || g.saveSeq != startSaveSeq || (g.loaded && g.sig == sig) {
		newData = g.data
		loadErr = g.loadErr
		ver = g.version
		g.mu.Unlock()
		return nil, newData, loadErr, ver, false
	}
	wasLoaded := g.loaded
	oldData = g.data
	g.loaded = true
	g.sig = sig
	g.data = parsed
	g.loadErr = perr
	g.version++
	ver = g.version
	newData = g.data
	loadErr = perr
	g.mu.Unlock()
	return oldData, newData, loadErr, ver, wasLoaded
}

func parseGlobalLayerFile(p, cwd string) (*OrderedMap, error) {
	data, ok, err := readConfigFileOrdered(p)
	if err != nil {
		return NewOrderedMap(), fmt.Errorf("Settings config is invalid (%s): %w", p, err)
	}
	if !ok {
		return NewOrderedMap(), nil
	}
	migrated := migrateRawSettings(data)
	if verr := validateConfiguredLayer(migrated, cwd); verr != nil {
		return NewOrderedMap(), fmt.Errorf("Settings config is invalid (%s): %w", p, verr)
	}
	return migrated, nil
}

// willDropComments checks (without modifying disk) whether saving value at
// entry will fall back to full re-serialization over a file that has comments (T7).
func (g *sharedGlobalLayer) willDropComments(entry *Entry, value any) bool {
	writePath, err := resolveYAMLWritePath(g.path)
	if err != nil {
		return false
	}
	rawText, current, exists, err := readConfigFileRawAndOrdered(writePath)
	if err != nil || !exists || current == nil {
		return false
	}
	if existingVal, has := getByPathOrdered(current, entry.Segments); has && orderedValuesEqual(existingVal, value) {
		return false
	}
	return saveWillDropComments(rawText, entry.Segments, value, value == nil)
}

// persistResult describes the outcome of a disk save (N1).
type persistResult struct {
	changed           bool
	commentsPreserved bool
}

// persistKey performs a locked read-modify-write on g.path:
//   - Acquires per-path in-process mutex + cross-process O_EXCL lock file (N2).
//   - Re-reads raw text + parses current file on disk (refusing to overwrite a
//     malformed file, returning the parse error naming the line: B3/N3).
//   - Skips rewriting when the on-disk value already equals the requested value (N1).
//   - Patches the key's line in place (or appends cleanly) to preserve comments,
//     falling back to full re-serialization only for multi-line/flow values (N1).
//   - Re-parses the written candidate text and verifies it equals the intended
//     document before atomically replacing the file (N1).
//   - Updates the shared global layer so every session sees the new value at its
//     next read (N6).
func (g *sharedGlobalLayer) persistKey(entry *Entry, norm any, unset bool) (oldGlobal, newGlobal *OrderedMap, res persistResult, err error) {
	writePath, err := resolveYAMLWritePath(g.path)
	if err != nil {
		return nil, nil, persistResult{}, fmt.Errorf("resolve config path %s: %w", g.path, err)
	}

	g.saveInFlight.Add(1)
	defer g.saveInFlight.Add(-1)

	lockErr := withConfigFileLock(writePath, func() error {
		// Remove any stale `.<basename>.*.tmp` siblings left behind by a previously
		// killed writer for THIS target only while we hold its exclusive lock (N3, F1).
		cleanupStaleConfigTemps(writePath)
		mode := os.FileMode(0o600)
		if info, statErr := os.Stat(writePath); statErr == nil {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("cannot save %s: %s is not a regular file", entry.ID, writePath)
			}
			mode = info.Mode().Perm()
		}

		rawText, current, exists, rerr := readConfigFileRawAndOrdered(writePath)
		if rerr != nil {
			return fmt.Errorf("cannot save %s: existing config %s could not be parsed (%w); fix the file before saving", entry.ID, writePath, rerr)
		}
		if !exists || current == nil {
			current = NewOrderedMap()
			rawText = ""
		}

		// No-op skip: do not rewrite if on-disk value already matches (N1; upstream settings.ts:2393).
		existingVal, hasExisting := getByPathOrdered(current, entry.Segments)
		if unset {
			if !hasExisting {
				finalData := migrateRawSettings(current)
				finalSig := statFileSig(g.path)
				g.mu.Lock()
				oldGlobal = g.data
				g.loaded = true
				g.sig = finalSig
				g.data = finalData
				g.loadErr = nil
				g.saveSeq++
				g.version++
				newGlobal = g.data
				g.mu.Unlock()
				res = persistResult{changed: false, commentsPreserved: true}
				return nil
			}
		} else {
			if hasExisting && orderedValuesEqual(existingVal, norm) {
				finalData := migrateRawSettings(current)
				finalSig := statFileSig(g.path)
				g.mu.Lock()
				oldGlobal = g.data
				g.loaded = true
				g.sig = finalSig
				g.data = finalData
				g.loadErr = nil
				g.saveSeq++
				g.version++
				newGlobal = g.data
				g.mu.Unlock()
				res = persistResult{changed: false, commentsPreserved: true}
				return nil
			}
		}

		intended := current.Clone()
		if unset {
			deleteByPathOrdered(intended, entry.Segments)
		} else {
			setByPathOrdered(intended, entry.Segments, deepCloneOrdered(norm))
		}

		var (
			outText           string
			commentsPreserved bool
		)

		// Try comment-preserving surgical patch first (N1).
		if patched, ok := patchYAMLText(rawText, entry.Segments, norm, unset); ok {
			if verified, perr := ParseYAMLOrdered(patched); perr == nil && orderedValuesEqual(verified, intended) {
				outText = patched
				commentsPreserved = true
			}
		}
		if outText == "" {
			var merr error
			outText, merr = MarshalYAMLOrdered(intended)
			if merr != nil {
				return merr
			}
			commentsPreserved = !hasYAMLComments(rawText)
		}

		// Post-serialize verification: re-parse outText and require equality with
		// intended before touching the file on disk (N1).
		reparsed, perr := ParseYAMLOrdered(outText)
		if perr != nil || !orderedValuesEqual(reparsed, intended) {
			return fmt.Errorf("cannot save %s: internal verification failed after formatting config", entry.ID)
		}

		if err := os.MkdirAll(filepath.Dir(writePath), 0o700); err != nil {
			return fmt.Errorf("create config directory: %w", err)
		}
		if err := writeFileAtomically(writePath, []byte(outText), mode); err != nil {
			return err
		}

		// Publish the new snapshot INSIDE the file lock so concurrent readers
		// immediately see the updated signature and never fire a duplicate
		// external-edit OnChange (N4).
		finalData := migrateRawSettings(intended)
		finalSig := statFileSig(g.path)
		g.mu.Lock()
		oldGlobal = g.data
		g.loaded = true
		g.sig = finalSig
		g.data = finalData
		g.loadErr = nil
		g.saveSeq++
		g.version++
		newGlobal = g.data
		g.mu.Unlock()
		res = persistResult{changed: true, commentsPreserved: commentsPreserved}
		return nil
	})
	if lockErr != nil {
		return nil, nil, persistResult{}, lockErr
	}
	return oldGlobal, newGlobal, res, nil
}

// configTempPattern returns the CreateTemp pattern (`<prefix>*.tmp`) and prefix
// scoped to writePath's own basename (`.<basename>.`), so two config targets
// sharing a directory (e.g. symlinked into one dotfiles dir) never delete each
// other's in-flight temp files (F1).
func configTempPrefix(writePath string) string {
	return "." + filepath.Base(writePath) + "."
}

// cleanupStaleConfigTemps removes orphaned `.<basename>.*.tmp` regular files in
// writePath's directory left behind by a killed writer for writePath.
// Caller must hold the exclusive config file lock for writePath (N3, F1).
func cleanupStaleConfigTemps(writePath string) {
	dir := filepath.Dir(writePath)
	prefix := configTempPrefix(writePath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".tmp") || len(name) <= len(prefix)+len(".tmp") {
			continue
		}
		// Only remove regular files (never symlinks or directories); verify via Lstat.
		full := filepath.Join(dir, name)
		info, err := os.Lstat(full)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		_ = os.Remove(full)
	}
}

func hasYAMLComments(src string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" {
			continue
		}
		if stripYAMLComment(trimmed) != trimmed {
			return true
		}
	}
	return false
}

// ── Store ────────────────────────────────────────────────────────────────────

// StoreOptions configures a Store instance.
type StoreOptions struct {
	// AgentDir is the global agent directory holding config.yml / config.yaml
	// (e.g. <ConfigHome>/agent). Empty disables reading/writing a global file.
	AgentDir string
	// Cwd is the working directory for project settings (<cwd>/.pig/config.yml
	// or <cwd>/.pig/config.yaml).
	Cwd string
	// ProjectTrusted gates loading the project layer (integrator decision / B5).
	// When false, project settings are ignored.
	ProjectTrusted bool
	// HomeDir overrides os.UserHomeDir() for the cwd == home project exclusion.
	HomeDir string
	// ConfigFiles are explicit YAML overlays (loaded after PIG_CONFIG_FILES).
	ConfigFiles []string
	// InMemory disables disk reads/writes (used in tests).
	InMemory bool
	// Overrides are initial runtime overrides keyed by dotted setting id.
	Overrides map[string]any
	// SessionID optionally tags this Store with its owning session for OnChange
	// notifications and parent override fallthrough.
	SessionID string
}

// Store holds layered omp settings: shared global (disk-backed), project,
// overlay, and per-session runtime overrides, resolved together with
// environment variables and schema defaults.
// upstream: packages/coding-agent/src/config/settings.ts Settings
type Store struct {
	mu             sync.RWMutex
	sessionID      string
	agentDir       string
	cwd            string
	projectTrusted bool
	homeDir        string
	configFiles    []string
	inMemory       bool
	globalPath     string // resolved config.yml or existing config.yaml
	sharedGlobal   *sharedGlobalLayer
	globalVer      uint64
	globalErr      error // non-nil when global config failed to load; blocks /save (B3/N3)
	warnings       []string
	// parentSessionID names the parent session for lazy read-through of parent
	// runtime overrides (T3 / N5); resolved dynamically from `sessions` on read.
	parentSessionID string
	global          *OrderedMap
	project         *OrderedMap
	overlay         *OrderedMap
	overrides       *OrderedMap
	merged          *OrderedMap
	operator        *OrderedMap // global + overlay + overrides without project (grantAuthority)
}

// NewStore creates and loads a Store from disk (unless opts.InMemory is true).
// An unreadable or unparseable global or overlay file returns an error; a
// malformed project file is skipped and recorded in Warnings().
func NewStore(opts StoreOptions) (*Store, error) {
	loadSchema()
	s := newBlankStore(opts)
	if !opts.InMemory {
		if err := s.reloadLocked(true); err != nil {
			return nil, err
		}
	}
	if len(opts.Overrides) > 0 {
		migrated := migrateRawSettings(toOrderedMap(opts.Overrides))
		for _, k := range migrated.Keys {
			if err := s.setOverrideFromMapLocked(k, migrated.Values[k]); err != nil {
				return nil, err
			}
		}
	}
	s.rebuildMergedLocked()
	return s, nil
}

func newBlankStore(opts StoreOptions) *Store {
	return &Store{
		sessionID:      opts.SessionID,
		agentDir:       opts.AgentDir,
		cwd:            opts.Cwd,
		projectTrusted: opts.ProjectTrusted,
		homeDir:        opts.HomeDir,
		configFiles:    append([]string(nil), opts.ConfigFiles...),
		inMemory:       opts.InMemory,
		global:         NewOrderedMap(),
		project:        NewOrderedMap(),
		overlay:        NewOrderedMap(),
		overrides:      NewOrderedMap(),
		merged:         NewOrderedMap(),
		operator:       NewOrderedMap(),
	}
}

func (s *Store) setOverrideFromMapLocked(prefix string, val any) error {
	if _, ok := Lookup(prefix); ok {
		return s.setLocked(prefix, val, false)
	}
	if om, isMap := val.(*OrderedMap); isMap {
		for _, k := range om.Keys {
			if err := s.setOverrideFromMapLocked(prefix+"."+k, om.Values[k]); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("Unknown setting %q", prefix)
}

// NewIsolated creates an in-memory Store with optional initial runtime overrides.
// upstream: Settings.isolated
func NewIsolated(overrides map[string]any) (*Store, error) {
	return NewStore(StoreOptions{InMemory: true, Overrides: overrides})
}

// AgentDir returns the resolved global agent directory.
func (s *Store) AgentDir() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.agentDir
}

// Warnings returns any non-fatal load warnings (e.g. skipped malformed project config).
func (s *Store) Warnings() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.warnings...)
}

// LoadError returns the error that prevented the global config from loading, if any.
// Refreshes the shared global layer if the file's mtime or size changed (N3).
func (s *Store) LoadError() error {
	s.syncSharedGlobalIfNeeded()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.globalErr
}

// GlobalConfigPath returns the path to the global config file (config.yml or
// existing config.yaml). Empty for in-memory stores without an agentDir.
func (s *Store) GlobalConfigPath() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.globalPath != "" {
		return s.globalPath
	}
	if s.agentDir == "" {
		return ""
	}
	return filepath.Join(s.agentDir, "config.yml")
}

// GlobalSettings returns a plain-map deep copy of the global layer.
func (s *Store) GlobalSettings() map[string]any {
	s.syncSharedGlobalIfNeeded()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.global.ToPlainMap()
}

func (s *Store) globalOrderedClone() *OrderedMap {
	s.syncSharedGlobalIfNeeded()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.global.Clone()
}

// syncSharedGlobalIfNeeded checks if the shared global layer's (dev, ino,
// ctime, mtime, size) signature or version changed and refreshes this Store's
// merged view with zero allocations on steady-state reads (N3, N6, T6, T7).
func (s *Store) syncSharedGlobalIfNeeded() {
	s.mu.RLock()
	sg := s.sharedGlobal
	inMem := s.inMemory
	cwd := s.cwd
	curVer := s.globalVer
	s.mu.RUnlock()
	if inMem || sg == nil {
		return
	}
	data, loadErr, ver, updated := sg.ensureFresh(cwd, curVer)
	if !updated {
		return
	}
	s.mu.Lock()
	// Monotonic check: never overwrite a newer version with an older one fetched
	// concurrently by another reader goroutine (N4).
	if ver > s.globalVer {
		s.global = data
		s.globalErr = loadErr
		s.globalVer = ver
		s.rebuildMergedLocked()
	}
	s.mu.Unlock()
}

// SaveWillDropComments reports whether saving value at id to disk will fall
// back to full re-serialization over a file containing comments (T7).
func (s *Store) SaveWillDropComments(id string, value any) bool {
	entry, ok := Lookup(id)
	if !ok {
		return false
	}
	s.mu.RLock()
	inMem := s.inMemory
	sg := s.sharedGlobal
	gPath := s.globalPath
	aDir := s.agentDir
	s.mu.RUnlock()
	if inMem {
		return false
	}
	if sg == nil {
		if gPath == "" && aDir != "" {
			gPath = resolveMainConfigPath(aDir)
		}
		if gPath == "" {
			return false
		}
		sg = getSharedGlobalLayer(gPath)
	}
	return sg.willDropComments(entry, value)
}

// Reload re-reads global, project, and overlay files from disk, preserving
// runtime overrides.
func (s *Store) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inMemory {
		return nil
	}
	if err := s.reloadLocked(true); err != nil {
		return err
	}
	s.rebuildMergedLocked()
	return nil
}

// ReloadForCwd updates cwd (and project trust) and reloads disk layers while
// preserving this session's runtime overrides (N4).
func (s *Store) ReloadForCwd(cwd string, projectTrusted bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cwd = cwd
	s.projectTrusted = projectTrusted
	if !s.inMemory {
		if err := s.reloadLocked(false); err != nil {
			return err
		}
	}
	s.rebuildMergedLocked()
	return nil
}

func (s *Store) reloadLocked(forceGlobalReload bool) error {
	s.warnings = nil
	s.globalErr = nil

	// 1. Shared global layer: <agentDir>/config.yml (or existing config.yaml).
	s.global = NewOrderedMap()
	s.globalPath = ""
	if s.agentDir != "" {
		target := resolveMainConfigPath(s.agentDir)
		s.globalPath = target
		sg := getSharedGlobalLayer(target)
		s.sharedGlobal = sg
		var (
			data    *OrderedMap
			loadErr error
			ver     uint64
		)
		if forceGlobalReload {
			data, loadErr, ver = sg.reload(s.cwd)
		} else {
			data, loadErr, ver, _ = sg.ensureFresh(s.cwd, 0)
			if data == nil {
				sg.mu.RLock()
				data = sg.data
				sg.mu.RUnlock()
			}
		}
		s.global = data
		s.globalErr = loadErr
		s.globalVer = ver
		if loadErr != nil {
			return loadErr
		}
	}

	// 2. Project layer: ONLY <cwd>/.pig/config.yml (or existing .yaml), and ONLY
	// when the project is trusted AND cwd is not the user's home directory
	// (integrator decision + B4 + B5 + upstream discovery/builtin.ts:64-67).
	s.project = NewOrderedMap()
	if s.projectTrusted && s.cwd != "" && !isSameDirAsHome(s.cwd, s.homeDir) {
		projectDir := filepath.Join(s.cwd, ".pig")
		candidates := []string{
			filepath.Join(projectDir, "config.yml"),
			filepath.Join(projectDir, "config.yaml"),
		}
		for _, p := range candidates {
			data, ok, err := readConfigFileOrdered(p)
			if err != nil {
				s.warnings = append(s.warnings, fmt.Sprintf("Skipped invalid project config %s: %v", p, err))
				continue
			}
			if ok {
				cleaned := dropSettingsGroupShadows(migrateRawSettings(data))
				if err := validateConfiguredLayer(cleaned, s.cwd); err != nil {
					s.warnings = append(s.warnings, fmt.Sprintf("Skipped invalid project config %s: %v", p, err))
					continue
				}
				s.project = deepMergeOrdered(s.project, cleaned, "combine", "")
				break
			}
		}
	}

	// 3. Config overlays: PIG_CONFIG_FILES followed by explicit ConfigFiles.
	s.overlay = NewOrderedMap()
	var overlayPaths []string
	if envList := os.Getenv(EnvConfigFiles); envList != "" {
		for _, item := range filepath.SplitList(envList) {
			if strings.TrimSpace(item) != "" {
				overlayPaths = append(overlayPaths, item)
			}
		}
	}
	overlayPaths = append(overlayPaths, s.configFiles...)
	for _, rawPath := range overlayPaths {
		resolved := resolveOverlayPath(rawPath, s.cwd)
		data, ok, err := readConfigFileOrdered(resolved)
		if err != nil {
			return fmt.Errorf("load overlay config %s: %w", resolved, err)
		}
		if !ok {
			return fmt.Errorf("overlay config file not found: %s", resolved)
		}
		migrated := migrateRawSettings(data)
		if err := validateConfiguredLayer(migrated, s.cwd); err != nil {
			return fmt.Errorf("invalid overlay config %s: %w", resolved, err)
		}
		s.overlay = deepMergeOrdered(s.overlay, migrated, "combine", "")
	}
	return nil
}

func resolveMainConfigPath(agentDir string) string {
	ymlPath := filepath.Join(agentDir, "config.yml")
	yamlPath := filepath.Join(agentDir, "config.yaml")
	if !pathExistsOrSymlink(ymlPath) && pathExistsOrSymlink(yamlPath) {
		return yamlPath
	}
	return ymlPath
}

func pathExistsOrSymlink(p string) bool {
	info, err := os.Lstat(p)
	if err != nil {
		return false
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return true
	}
	return !info.IsDir()
}

func isSameDirAsHome(cwd, homeOverride string) bool {
	home := homeOverride
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil || home == "" {
			return false
		}
	}
	return normalizePathForMatch(cwd) == normalizePathForMatch(home)
}

func resolveOverlayPath(p, cwd string) string {
	p = expandTildePath(p)
	if filepath.IsAbs(p) || cwd == "" {
		return p
	}
	return filepath.Join(cwd, p)
}

func expandTildePath(p string) string {
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return p
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// readConfigFileRawAndOrdered safely reads a regular config file with a 1 MiB
// cap and returns its raw text plus parsed OrderedMap.
func readConfigFileRawAndOrdered(p string) (string, *OrderedMap, bool, error) {
	info, err := os.Stat(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil, false, nil
		}
		return "", nil, false, err
	}
	if !info.Mode().IsRegular() {
		return "", nil, false, fmt.Errorf("config path %s is not a regular file", p)
	}
	if info.Size() > maxYAMLBytes {
		return "", nil, false, fmt.Errorf("config file %s exceeds %d byte limit", p, maxYAMLBytes)
	}
	f, err := os.Open(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil, false, nil
		}
		return "", nil, false, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxYAMLBytes+1))
	if err != nil {
		return "", nil, false, err
	}
	if len(raw) > maxYAMLBytes {
		return "", nil, false, fmt.Errorf("config file %s exceeds %d byte limit", p, maxYAMLBytes)
	}
	text := string(raw)
	ext := strings.ToLower(filepath.Ext(p))
	if ext == ".json" || ext == ".jsonc" {
		parsed, err := decodeOrderedJSON(strings.TrimPrefix(text, "\uFEFF"))
		if err != nil {
			return text, nil, false, err
		}
		if parsed == nil {
			return text, NewOrderedMap(), true, nil
		}
		om, ok := parsed.(*OrderedMap)
		if !ok {
			return text, nil, false, fmt.Errorf("JSON config root must be an object")
		}
		return text, om, true, nil
	}
	om, err := ParseYAMLOrdered(text)
	if err != nil {
		return text, nil, false, err
	}
	return text, om, true, nil
}

func readConfigFileOrdered(p string) (*OrderedMap, bool, error) {
	_, om, ok, err := readConfigFileRawAndOrdered(p)
	return om, ok, err
}

// dropSettingsGroupShadows drops non-object leaves that would shadow a settings
// group (e.g. `"tui": "fullscreen"` over `tui.*`), preserving leaves, unknown
// keys, and nested objects.
// upstream: packages/coding-agent/src/config/settings.ts:413
func dropSettingsGroupShadows(data *OrderedMap) *OrderedMap {
	return dropSettingsGroupShadowsPrefix(data, "")
}

func dropSettingsGroupShadowsPrefix(data *OrderedMap, basePrefix string) *OrderedMap {
	loadSchema()
	if data == nil {
		return NewOrderedMap()
	}
	res := NewOrderedMap()
	for _, key := range data.Keys {
		val := data.Values[key]
		p := key
		if basePrefix != "" {
			p = basePrefix + "." + key
		}
		if !groupOnlyPfx[p] {
			res.Set(key, deepCloneOrdered(val))
			continue
		}
		sub, isObj := val.(*OrderedMap)
		if !isObj || sub == nil {
			continue
		}
		res.Set(key, dropSettingsGroupShadowsPrefix(sub, p))
	}
	return res
}

// DropSettingsGroupShadows is the exported form for testing and callers filtering
// foreign/project maps.
// upstream: packages/coding-agent/src/config/settings.ts:413
func DropSettingsGroupShadows(data map[string]any) map[string]any {
	return dropSettingsGroupShadows(toOrderedMap(data)).ToPlainMap()
}

// migrateRawSettings applies omp's core field-level migrations:
//   - queueMode -> steeringMode
//   - flat string theme: "light"/"dark" deleted so defaults apply; other names placed in one slot
//
// upstream: packages/coding-agent/src/config/settings.ts:3061-3120
func migrateRawSettings(m *OrderedMap) *OrderedMap {
	out := m.Clone()
	if qm, ok := out.Get("queueMode"); ok {
		if _, has := out.Get("steeringMode"); !has {
			out.Set("steeringMode", qm)
		}
		out.Delete("queueMode")
	}
	if themeRaw, ok := out.Get("theme"); ok {
		if themeStr, isStr := themeRaw.(string); isStr {
			if themeStr == "light" || themeStr == "dark" {
				out.Delete("theme")
			} else if themeStr != "" {
				slot := "dark"
				if isLikelyLightThemeName(themeStr) {
					slot = "light"
				}
				tm := NewOrderedMap()
				tm.Set(slot, themeStr)
				out.Set("theme", tm)
			}
		}
	}
	return out
}

func isLikelyLightThemeName(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "light") || strings.Contains(lower, "latte") || strings.Contains(lower, "dawn") || strings.Contains(lower, "day")
}

// validateConfiguredLayer runs ValidateConfigured on every configured setting in layer.
// upstream: packages/coding-agent/src/config/settings.ts:4424
func validateConfiguredLayer(layer *OrderedMap, cwd string) error {
	loadSchema()
	for i := range schemaAll {
		entry := &schemaAll[i]
		val, ok := configuredValueOrdered(layer, entry, cwd)
		if !ok || val == nil {
			continue
		}
		if err := entry.ValidateConfigured(val); err != nil {
			return err
		}
	}
	return nil
}

// projectLayerForMerge drops null/blank modelRoles entries in the project layer
// so they fall back to global roles.
// upstream: packages/coding-agent/src/config/settings.ts:299
func projectLayerForMerge(project *OrderedMap) *OrderedMap {
	rolesRaw, ok := project.Get("modelRoles")
	if !ok {
		return project
	}
	roles, ok := rolesRaw.(*OrderedMap)
	if !ok || roles == nil {
		return project
	}
	var filtered *OrderedMap
	for _, k := range roles.Keys {
		v := roles.Values[k]
		if _, valid := modelRoleValueFromUnknown(v); valid {
			continue
		}
		if filtered == nil {
			filtered = roles.Clone()
		}
		filtered.Delete(k)
	}
	if filtered == nil {
		return project
	}
	cp := project.Clone()
	cp.Set("modelRoles", filtered)
	return cp
}

func modelRoleValueFromUnknown(v any) (string, bool) {
	if s, ok := v.(string); ok {
		return s, true
	}
	if arr, ok := v.([]any); ok {
		parts := make([]string, 0, len(arr))
		for _, item := range arr {
			s, ok := item.(string)
			if !ok {
				return "", false
			}
			parts = append(parts, s)
		}
		return strings.Join(parts, ","), true
	}
	return "", false
}

func (s *Store) rebuildMergedLocked() {
	// upstream: packages/coding-agent/src/config/settings.ts #mergeOwnLayers
	merged := deepMergeOrdered(NewOrderedMap(), s.global, "replace", "")
	merged = deepMergeOrdered(merged, projectLayerForMerge(s.project), "layer", "")
	merged = deepMergeOrdered(merged, s.overlay, "layer", "")
	merged = deepMergeOrdered(merged, s.overrides, "replace", "")
	s.merged = merged

	// Operator-only (project-free) view for grantAuthority() (B5 / upstream settings.ts:954).
	op := deepMergeOrdered(NewOrderedMap(), s.global, "replace", "")
	op = deepMergeOrdered(op, s.overlay, "layer", "")
	op = deepMergeOrdered(op, s.overrides, "replace", "")
	s.operator = op
}

// deepMergeOrdered merges overrides over base preserving omp's key ordering
// (higher layer's keys first, then base-only keys; upstream settings.ts:4437)
// and array-merge rules (`prepend` / `append` / `replace`, plus scopedListDefault).
func deepMergeOrdered(base, overrides *OrderedMap, arrays, prefix string) *OrderedMap {
	result := NewOrderedMap()
	if overrides != nil {
		for _, k := range overrides.Keys {
			override := overrides.Values[k]
			baseVal, hasBase := base.Get(k)
			settingPath := k
			if prefix != "" {
				settingPath = prefix + "." + k
			}
			overrideMap, isOverrideMap := override.(*OrderedMap)
			baseMap, isBaseMap := baseVal.(*OrderedMap)

			if isOverrideMap && (isBaseMap || (arrays == "layer" && !hasBase && groupOnlyPfx[settingPath])) {
				b := baseMap
				if !isBaseMap || b == nil {
					b = NewOrderedMap()
				}
				result.Set(k, deepMergeOrdered(b, overrideMap, arrays, settingPath))
				continue
			}
			if overrideArr, isArr := override.([]any); arrays != "replace" && isArr {
				entry, _ := Lookup(settingPath)
				strategy := ""
				if entry != nil && entry.Type == TypeArray {
					strategy = entry.ArrayMerge
				}
				var effectiveBase []any
				if baseArr, ok := baseVal.([]any); ok {
					effectiveBase = make([]any, 0, len(baseArr))
					for _, item := range baseArr {
						effectiveBase = append(effectiveBase, deepCloneOrdered(item))
					}
					if entry != nil {
						for _, defItem := range scopedListDefault(entry, baseArr) {
							effectiveBase = append(effectiveBase, deepCloneOrdered(defItem))
						}
					}
				} else if arrays == "layer" && baseVal == nil && len(overrideArr) > 0 && entry != nil && entry.Type == TypeArray {
					if defArr, ok := entry.DefaultClone().([]any); ok {
						effectiveBase = defArr
					}
				}
				if len(overrideArr) > 0 && strategy == "prepend" && effectiveBase != nil {
					combined := make([]any, 0, len(overrideArr)+len(effectiveBase))
					for _, item := range overrideArr {
						combined = append(combined, deepCloneOrdered(item))
					}
					combined = append(combined, effectiveBase...)
					result.Set(k, combined)
					continue
				}
				if len(overrideArr) > 0 && strategy == "append" && effectiveBase != nil {
					combined := make([]any, 0, len(effectiveBase)+len(overrideArr))
					for _, item := range effectiveBase {
						combined = append(combined, deepCloneOrdered(item))
					}
					for _, item := range overrideArr {
						combined = append(combined, deepCloneOrdered(item))
					}
					result.Set(k, combined)
					continue
				}
				result.Set(k, deepCloneOrdered(overrideArr))
				continue
			}
			result.Set(k, deepCloneOrdered(override))
		}
	}
	if base != nil {
		for _, k := range base.Keys {
			if _, exists := result.Get(k); !exists {
				result.Set(k, deepCloneOrdered(base.Values[k]))
			}
		}
	}
	return result
}

// collectParentOverrides resolves and merges the live ancestor chain's runtime
// overrides WITHOUT holding s.mu, acquiring sessionsMu and each ancestor's
// Store.mu strictly one at a time so no lock-order cycle can occur (N1, T3).
func (s *Store) collectParentOverrides() *OrderedMap {
	s.mu.RLock()
	parentID := s.parentSessionID
	selfID := s.sessionID
	s.mu.RUnlock()
	if parentID == "" {
		return nil
	}

	seen := map[string]bool{}
	if selfID != "" {
		seen[selfID] = true
	}
	var ancStores []*Store
	curID := parentID

	sessionsMu.Lock()
	for curID != "" && !seen[curID] && len(ancStores) < 16 {
		seen[curID] = true
		if isSessionEndedLocked(curID) {
			break
		}
		st, ok := sessions[curID]
		if !ok || st == nil {
			break
		}
		if st.store != nil && st.store != s {
			ancStores = append(ancStores, st.store)
		}
		curID = st.parentID
	}
	sessionsMu.Unlock()

	if len(ancStores) == 0 {
		return nil
	}
	combined := NewOrderedMap()
	for i := len(ancStores) - 1; i >= 0; i-- {
		anc := ancStores[i]
		anc.mu.RLock()
		ancOv := anc.overrides.Clone()
		anc.mu.RUnlock()
		combined = deepMergeOrdered(combined, ancOv, "replace", "")
	}
	return combined
}

func (s *Store) effectiveOverridesWithParentLocked(parentOv *OrderedMap) *OrderedMap {
	if parentOv == nil || parentOv.Len() == 0 {
		return s.overrides
	}
	return deepMergeOrdered(parentOv, s.overrides, "replace", "")
}

func (s *Store) effectiveViewsWithParentLocked(parentOv *OrderedMap) (merged, operator, ov *OrderedMap) {
	if parentOv == nil || parentOv.Len() == 0 {
		return s.merged, s.operator, s.overrides
	}
	ov = deepMergeOrdered(parentOv, s.overrides, "replace", "")
	m := deepMergeOrdered(NewOrderedMap(), s.global, "replace", "")
	m = deepMergeOrdered(m, projectLayerForMerge(s.project), "layer", "")
	m = deepMergeOrdered(m, s.overlay, "layer", "")
	m = deepMergeOrdered(m, ov, "replace", "")

	op := deepMergeOrdered(NewOrderedMap(), s.global, "replace", "")
	op = deepMergeOrdered(op, s.overlay, "layer", "")
	op = deepMergeOrdered(op, ov, "replace", "")
	return m, op, ov
}

// effectiveValueWithGlobalLocked computes entry's effective value for this store
// substituting globalLayer for s.global (used to diff external file refreshes
// per live session for OnChange, T4). Caller must pass pre-collected parentOv
// and hold s.mu.RLock (never sessionsMu).
func (s *Store) effectiveValueWithGlobalLocked(entry *Entry, globalLayer, parentOv *OrderedMap) any {
	ov := s.effectiveOverridesWithParentLocked(parentOv)
	m := deepMergeOrdered(NewOrderedMap(), globalLayer, "replace", "")
	m = deepMergeOrdered(m, projectLayerForMerge(s.project), "layer", "")
	m = deepMergeOrdered(m, s.overlay, "layer", "")
	m = deepMergeOrdered(m, ov, "replace", "")
	return s.getFromViewLocked(entry, m, ov)
}

// Get returns the effective value of setting id (plain Go types: map[string]any,
// []any, scalars). For unknown keys, ok is false.
// upstream: packages/coding-agent/src/config/registry.ts Setting.get
func (s *Store) Get(id string) (any, bool) {
	v, ok := s.getOrdered(id)
	if !ok {
		return nil, false
	}
	return toPlainValue(v), true
}

// GetOperator returns the effective value of id from operator layers only
// (global + overlay + runtime + env + default, ignoring the project layer),
// matching omp's Settings.grantAuthority() (B5 / upstream settings.ts:954).
func (s *Store) GetOperator(id string) (any, bool) {
	entry, ok := Lookup(id)
	if !ok {
		return nil, false
	}
	s.syncSharedGlobalIfNeeded()
	parentOv := s.collectParentOverrides()
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, op, ov := s.effectiveViewsWithParentLocked(parentOv)
	v := s.getFromViewLocked(entry, op, ov)
	return toPlainValue(v), true
}

func (s *Store) getOrdered(id string) (any, bool) {
	if _, ok := Lookup(id); !ok {
		return nil, false
	}
	s.syncSharedGlobalIfNeeded()
	return s.getOrderedNoSync(id)
}

func (s *Store) getOrderedNoSync(id string) (any, bool) {
	entry, ok := Lookup(id)
	if !ok {
		return nil, false
	}
	parentOv := s.collectParentOverrides()
	s.mu.RLock()
	defer s.mu.RUnlock()
	merged, _, ov := s.effectiveViewsWithParentLocked(parentOv)
	return s.getFromViewLocked(entry, merged, ov), true
}

func (s *Store) getFromViewLocked(entry *Entry, view, ov *OrderedMap) any {
	if envVal, ok := s.effectiveEnvLocked(entry, view, ov); ok {
		return deepCloneOrdered(envVal)
	}
	return s.layeredLocked(entry, view)
}

func (s *Store) layeredLocked(entry *Entry, view *OrderedMap) any {
	raw, ok := configuredValueOrdered(view, entry, s.cwd)
	if !ok || raw == nil {
		return entry.DefaultClone()
	}
	if entry.Accepts(raw) {
		return deepCloneOrdered(raw)
	}
	return entry.DefaultClone()
}

func (s *Store) isConfiguredLocked(entry *Entry, view *OrderedMap) bool {
	val, ok := getByPathOrdered(view, entry.Segments)
	return ok && val != nil
}

func hasRuntimeOverrideIn(ov *OrderedMap, entry *Entry) bool {
	val, ok := getByPathOrdered(ov, entry.Segments)
	return ok && val != nil
}

// effectiveEnvLocked computes the active env override per omp's precedence:
//   - normal env var: beats config layers, but yields to runtime overrides.
//   - fallback="true": yields to any configured layer.
//   - fallback="blank": yields to any configured non-blank value.
//
// upstream: packages/coding-agent/src/config/registry.ts Setting.#effectiveEnv
func (s *Store) effectiveEnvLocked(entry *Entry, view, ov *OrderedMap) (any, bool) {
	val, ok := entry.EnvValue()
	if !ok {
		return nil, false
	}
	switch entry.EnvFallback {
	case "":
		if hasRuntimeOverrideIn(ov, entry) {
			return nil, false
		}
		return val, true
	case "true":
		if s.isConfiguredLocked(entry, view) {
			return nil, false
		}
		return val, true
	case "blank":
		raw, has := configuredValueOrdered(view, entry, s.cwd)
		if !has || raw == nil {
			return val, true
		}
		if str, isStr := raw.(string); isStr && strings.TrimSpace(str) == "" {
			return val, true
		}
		return nil, false
	default:
		return val, true
	}
}

// Provenance returns the layer supplying the effective value of id.
// upstream: packages/coding-agent/src/config/registry.ts Setting.provenance
// and settings.ts Settings.getProvenance
func (s *Store) Provenance(id string) Provenance {
	entry, ok := Lookup(id)
	if !ok {
		return ProvenanceDefault
	}
	s.syncSharedGlobalIfNeeded()
	parentOv := s.collectParentOverrides()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.provenanceLocked(entry, parentOv)
}

func (s *Store) provenanceLocked(entry *Entry, parentOv *OrderedMap) Provenance {
	merged, _, ov := s.effectiveViewsWithParentLocked(parentOv)
	if _, ok := s.effectiveEnvLocked(entry, merged, ov); ok {
		return ProvenanceEnv
	}
	if !s.isConfiguredLocked(entry, merged) {
		return ProvenanceDefault
	}
	if hasRuntimeOverrideIn(ov, entry) {
		return ProvenanceRuntime
	}
	if _, ok := getByPathOrdered(s.overlay, entry.Segments); ok {
		return ProvenanceOverlay
	}
	if _, ok := getByPathOrdered(projectLayerForMerge(s.project), entry.Segments); ok {
		return ProvenanceProject
	}
	if _, ok := getByPathOrdered(s.global, entry.Segments); ok {
		return ProvenanceGlobal
	}
	return ProvenanceDefault
}

// Set validates value against the schema and updates either the session runtime
// override layer (persist == false) or the global layer + config.yml on disk
// (persist == true). Passing value == nil clears the override (or removes the
// key from the global layer when persist == true).
func (s *Store) Set(id string, value any, persist bool) error {
	_, err := s.setWithResult(id, value, persist)
	return err
}

func (s *Store) setWithResult(id string, value any, persist bool) (persistResult, error) {
	entry, ok := Lookup(id)
	if !ok {
		return persistResult{}, fmt.Errorf("Unknown setting %q", id)
	}
	var norm any
	if value != nil {
		var err error
		norm, err = entry.Canonicalize(value)
		if err != nil {
			return persistResult{}, err
		}
		if err := entry.ValidateWrite(norm); err != nil {
			return persistResult{}, err
		}
	}
	var snaps []sessionValueSnap
	var res persistResult

	if !persist {
		snaps = snapshotAffectedSessionsForKey(s, entry.ID, false)
		s.mu.Lock()
		if value == nil {
			deleteByPathOrdered(s.overrides, entry.Segments)
		} else {
			setByPathOrdered(s.overrides, entry.Segments, deepCloneOrdered(norm))
		}
		s.rebuildMergedLocked()
		s.mu.Unlock()
		res = persistResult{changed: true, commentsPreserved: true}
		emitDiffsForKey(snaps, entry.ID)
		return res, nil
	}

	s.mu.Lock()
	inMem := s.inMemory
	if inMem {
		s.mu.Unlock()
		snaps = snapshotAffectedSessionsForKey(s, entry.ID, true)
		s.mu.Lock()
		if value == nil {
			deleteByPathOrdered(s.global, entry.Segments)
		} else {
			setByPathOrdered(s.global, entry.Segments, deepCloneOrdered(norm))
		}
		s.rebuildMergedLocked()
		s.mu.Unlock()
		res = persistResult{changed: true, commentsPreserved: true}
		emitDiffsForKey(snaps, entry.ID)
		return res, nil
	}
	if s.globalPath == "" {
		if s.agentDir == "" {
			s.mu.Unlock()
			return persistResult{}, errors.New("cannot save setting: no global config directory configured")
		}
		s.globalPath = resolveMainConfigPath(s.agentDir)
	}
	if s.sharedGlobal == nil {
		s.sharedGlobal = getSharedGlobalLayer(s.globalPath)
	}
	sg := s.sharedGlobal
	s.mu.Unlock()

	// Persist under the per-path mutex + OS advisory lock. persistKey returns the
	// exact before/after global snapshots atomically captured inside the lock, so
	// OnChange emits exactly once per change even under concurrent readers (N4).
	oldGlobal, newGlobal, res, perr := sg.persistKey(entry, norm, value == nil)
	if perr != nil {
		return persistResult{}, perr
	}
	s.syncSharedGlobalIfNeeded()
	if oldGlobal != nil && newGlobal != nil {
		notifyGlobalKeysForPeers(sg, s, diffGlobalSchemaKeys(oldGlobal, newGlobal), oldGlobal, newGlobal)
	}
	return res, nil
}
func (s *Store) setLocked(id string, value any, persist bool) error {
	entry, ok := Lookup(id)
	if !ok {
		return fmt.Errorf("Unknown setting %q", id)
	}
	if value == nil {
		if persist {
			deleteByPathOrdered(s.global, entry.Segments)
		} else {
			deleteByPathOrdered(s.overrides, entry.Segments)
		}
		return nil
	}
	norm, err := entry.Canonicalize(value)
	if err != nil {
		return err
	}
	if err := entry.ValidateWrite(norm); err != nil {
		return err
	}
	if persist {
		setByPathOrdered(s.global, entry.Segments, deepCloneOrdered(norm))
	} else {
		setByPathOrdered(s.overrides, entry.Segments, deepCloneOrdered(norm))
	}
	return nil
}

// resolveYAMLWritePath resolves symlinks (including dangling symlink chains)
// so atomic renames update the final target without replacing user symlinks (B2).
// upstream: packages/coding-agent/src/config/settings.ts:2458
func resolveYAMLWritePath(filePath string) (string, error) {
	if resolved, err := filepath.EvalSymlinks(filePath); err == nil {
		return resolved, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	cur := filePath
	for hops := 0; hops < maxSymlinkHops; hops++ {
		info, err := os.Lstat(cur)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				dir := filepath.Dir(cur)
				if realDir, derr := filepath.EvalSymlinks(dir); derr == nil {
					return filepath.Join(realDir, filepath.Base(cur)), nil
				}
				return cur, nil
			}
			return "", err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return cur, nil
		}
		linkTarget, err := os.Readlink(cur)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(linkTarget) {
			parent := filepath.Dir(cur)
			if realParent, perr := filepath.EvalSymlinks(parent); perr == nil {
				parent = realParent
			}
			linkTarget = filepath.Join(parent, linkTarget)
		}
		cur = filepath.Clean(linkTarget)
	}
	return "", fmt.Errorf("too many symlink hops resolving %s", filePath)
}

// ClearOverride removes a runtime override for id without touching disk.
func (s *Store) ClearOverride(id string) {
	entry, ok := Lookup(id)
	if !ok {
		return
	}
	snaps := snapshotAffectedSessionsForKey(s, entry.ID, false)
	s.mu.Lock()
	deleteByPathOrdered(s.overrides, entry.Segments)
	s.rebuildMergedLocked()
	s.mu.Unlock()
	emitDiffsForKey(snaps, entry.ID)
}

// SetProjectLayer replaces the in-memory project layer (primarily for tests).
func (s *Store) SetProjectLayer(layer map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.project = dropSettingsGroupShadows(migrateRawSettings(toOrderedMap(layer)))
	s.rebuildMergedLocked()
}

// SetOverlayLayer replaces the in-memory overlay layer (primarily for tests).
func (s *Store) SetOverlayLayer(layer map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.overlay = migrateRawSettings(toOrderedMap(layer))
	s.rebuildMergedLocked()
}

func writeFileAtomically(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, configTempPrefix(path)+"*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	_ = tmp.Chmod(perm)
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	ok = true
	return nil
}

// Typed getters on *Store.

func (s *Store) Bool(key string) bool {
	v, ok := s.Get(key)
	if !ok || v == nil {
		return false
	}
	b, _ := v.(bool)
	return b
}

func (s *Store) Int(key string) int {
	v, ok := s.Get(key)
	if !ok || v == nil {
		return 0
	}
	f, ok := asFloat64(v)
	if !ok {
		return 0
	}
	return int(math.Trunc(f))
}

func (s *Store) Float(key string) float64 {
	v, ok := s.Get(key)
	if !ok || v == nil {
		return 0
	}
	f, _ := asFloat64(v)
	return f
}

func (s *Store) String(key string) string {
	v, ok := s.Get(key)
	if !ok || v == nil {
		return ""
	}
	str, _ := v.(string)
	return str
}

func (s *Store) Strings(key string) []string {
	v, ok := s.Get(key)
	if !ok || v == nil {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if str, ok := item.(string); ok {
			out = append(out, str)
		}
	}
	return out
}

func (s *Store) Any(key string) any {
	v, _ := s.Get(key)
	return v
}

// ── Path-scoped array resolution (exact port of omp settings.ts:588-685, fixes A1) ──

func stringArrayFromUnknown(v any) []string {
	if s, ok := v.(string); ok {
		return []string{s}
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func pathScopePrefixes(entry *OrderedMap) []string {
	var out []string
	for _, k := range []string{"path", "paths", "pathPrefix", "pathPrefixes"} {
		if v, ok := entry.Get(k); ok {
			out = append(out, stringArrayFromUnknown(v)...)
		}
	}
	return out
}

func scopedListDefault(entry *Entry, list []any) []any {
	if entry.Type != TypeArray || entry.PathScoped == nil || entry.PathScoped.ItemType != "object" || len(list) == 0 {
		return nil
	}
	for _, item := range list {
		if om, ok := item.(*OrderedMap); ok && len(pathScopePrefixes(om)) == 0 {
			return nil
		}
	}
	if defArr, ok := entry.DefaultClone().([]any); ok {
		return defArr
	}
	return nil
}

func resolvePathScopedObjects(entries []any, valuesKey, cwd string, resolved *[]any) {
	for _, item := range entries {
		om, ok := item.(*OrderedMap)
		if !ok || om == nil {
			continue
		}
		prefixes := pathScopePrefixes(om)
		if len(prefixes) == 0 {
			*resolved = append(*resolved, om.Clone())
			continue
		}
		if !matchesAnyPathPrefix(cwd, prefixes) {
			continue
		}
		inlineRule := NewOrderedMap()
		for _, k := range om.Keys {
			if k == "path" || k == "paths" || k == "pathPrefix" || k == "pathPrefixes" || k == "values" || k == "items" || k == valuesKey {
				continue
			}
			inlineRule.Set(k, deepCloneOrdered(om.Values[k]))
		}
		if inlineRule.Len() > 0 {
			*resolved = append(*resolved, inlineRule)
		}
		for _, listKey := range []string{"values", "items", valuesKey} {
			if listKey == "" {
				continue
			}
			if v, has := om.Get(listKey); has {
				if sub, isArr := v.([]any); isArr {
					resolvePathScopedObjects(sub, valuesKey, cwd, resolved)
				}
			}
		}
	}
}

func configuredValueOrdered(merged *OrderedMap, entry *Entry, cwd string) (any, bool) {
	val, ok := getByPathOrdered(merged, entry.Segments)
	if !ok || val == nil {
		return nil, false
	}
	if resolved, ok := resolvePathScopedArrayOrdered(entry, val, cwd); ok {
		return resolved, true
	}
	return val, true
}

func resolvePathScopedArrayOrdered(entry *Entry, value any, cwd string) ([]any, bool) {
	scope := entry.PathScoped
	if scope == nil {
		return nil, false
	}
	arr, ok := value.([]any)
	if !ok {
		return nil, false
	}
	resolved := []any{}
	if scope.ItemType == "object" {
		resolvePathScopedObjects(arr, scope.ValuesKey, cwd, &resolved)
		for _, defItem := range scopedListDefault(entry, arr) {
			resolved = append(resolved, deepCloneOrdered(defItem))
		}
		return resolved, true
	}

	for _, item := range arr {
		if s, isStr := item.(string); isStr {
			resolved = append(resolved, s)
			continue
		}
		om, isMap := item.(*OrderedMap)
		if !isMap || om == nil {
			continue
		}
		prefixes := pathScopePrefixes(om)
		if len(prefixes) == 0 || !matchesAnyPathPrefix(cwd, prefixes) {
			continue
		}
		for _, listKey := range []string{"values", "items", scope.ValuesKey} {
			if listKey == "" {
				continue
			}
			if v, has := om.Get(listKey); has {
				for _, s := range stringArrayFromUnknown(v) {
					resolved = append(resolved, s)
				}
			}
		}
	}
	return resolved, true
}

func normalizePathForMatch(p string) string {
	if p == "" {
		return ""
	}
	p = expandTildePath(p)
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	return filepath.Clean(p)
}

func matchesAnyPathPrefix(cwd string, prefixes []string) bool {
	if cwd == "" {
		return false
	}
	normCwd := normalizePathForMatch(cwd)
	for _, raw := range prefixes {
		pfx := normalizePathForMatch(raw)
		if pfx == "" {
			continue
		}
		rel, err := filepath.Rel(pfx, normCwd)
		if err != nil {
			continue
		}
		if rel == "." || (rel != "" && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)) {
			return true
		}
	}
	return false
}

// ── Ordered path helpers ─────────────────────────────────────────────────────

func toOrderedMap(m map[string]any) *OrderedMap {
	if m == nil {
		return NewOrderedMap()
	}
	om, _ := toOrderedValue(m).(*OrderedMap)
	if om == nil {
		return NewOrderedMap()
	}
	return om
}

func getByPathOrdered(obj *OrderedMap, segments []string) (any, bool) {
	var cur any = obj
	for _, seg := range segments {
		om, ok := cur.(*OrderedMap)
		if !ok || om == nil {
			return nil, false
		}
		v, exists := om.Get(seg)
		if !exists {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

func setByPathOrdered(obj *OrderedMap, segments []string, value any) {
	cur := obj
	for i := 0; i < len(segments)-1; i++ {
		seg := segments[i]
		nextRaw, _ := cur.Get(seg)
		next, ok := nextRaw.(*OrderedMap)
		if !ok || next == nil {
			next = NewOrderedMap()
			cur.Set(seg, next)
		}
		cur = next
	}
	cur.Set(segments[len(segments)-1], value)
}

func deleteByPathOrdered(obj *OrderedMap, segments []string) {
	if len(segments) == 0 || obj == nil {
		return
	}
	containers := make([]*OrderedMap, 0, len(segments))
	containers = append(containers, obj)
	for i := 0; i < len(segments)-1; i++ {
		nextRaw, _ := containers[i].Get(segments[i])
		next, ok := nextRaw.(*OrderedMap)
		if !ok || next == nil {
			return
		}
		containers = append(containers, next)
	}
	last := containers[len(containers)-1]
	if _, exists := last.Get(segments[len(segments)-1]); !exists {
		return
	}
	last.Delete(segments[len(segments)-1])
	for i := len(containers) - 1; i > 0; i-- {
		if containers[i].Len() == 0 {
			containers[i-1].Delete(segments[i-1])
		} else {
			break
		}
	}
}

// ── Single per-session table (N3, N4, N5, N6, lifecycle) ─────────────────────

type cfgSessionGrant struct {
	save bool
}

// sessionState holds ALL per-session state in one table keyed by sessionID (N4):
// the session's Store (holding runtime overrides, project layer, and link to
// shared global layer), warn-once messages, cfg:// approval grant, and resolved
// trust/cwd.
type sessionState struct {
	store    *Store
	warned   map[string]bool
	grant    *cfgSessionGrant
	agentDir string
	cwd      string
	trusted  bool
	parentID string
}

const maxEndedTombstones = 4096

var (
	sessionsMu   sync.Mutex
	sessions     = map[string]*sessionState{}
	endedSet     = map[string]struct{}{}
	endedRing    []string // FIFO eviction order bounding endedSet to maxEndedTombstones (T4)
	overrideHook func(ctx sdk.Context) *Store
)

func isSessionEnded(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	sessionsMu.Lock()
	_, ended := endedSet[sessionID]
	sessionsMu.Unlock()
	return ended
}

func isSessionEndedLocked(sessionID string) bool {
	_, ended := endedSet[sessionID]
	return ended
}

// isDescendantSessionLocked reports whether sid has ancestorID in its parent chain.
// Caller must hold sessionsMu.
func isDescendantSessionLocked(sid, ancestorID string) bool {
	cur := sid
	seen := map[string]bool{}
	for depth := 0; cur != "" && !seen[cur] && depth < 16; depth++ {
		seen[cur] = true
		st, ok := sessions[cur]
		if !ok || st == nil {
			return false
		}
		if st.parentID == ancestorID {
			return true
		}
		cur = st.parentID
	}
	return false
}

// releaseSession removes all per-session state for sessionID only and records a
// bounded tombstone so late calls on an ended session neither fire OnChange nor
// re-create leaked entries (T4). Safe to call multiple times or for unknown/empty ids.
func releaseSession(sessionID string) {
	if sessionID == "" {
		return
	}
	sessionsMu.Lock()
	delete(sessions, sessionID)
	if _, already := endedSet[sessionID]; !already {
		if len(endedRing) >= maxEndedTombstones {
			evict := endedRing[0]
			endedRing = endedRing[1:]
			delete(endedSet, evict)
		}
		endedSet[sessionID] = struct{}{}
		endedRing = append(endedRing, sessionID)
	}
	sessionsMu.Unlock()
}

// ResetForTests clears all sessions, tombstones, shared global layers, change
// listeners, and cfg:// test hooks.
func ResetForTests() {
	sessionsMu.Lock()
	sessions = map[string]*sessionState{}
	endedSet = map[string]struct{}{}
	endedRing = nil
	overrideHook = nil
	sessionsMu.Unlock()

	sharedGlobalsMu.Lock()
	sharedGlobals = map[string]*sharedGlobalLayer{}
	sharedGlobalsMu.Unlock()

	changeListenersMu.Lock()
	changeListeners = nil
	changeListenersMu.Unlock()

	ParentSession = nil
	resetCfgStateForTests()
}

// SetStoreForTests installs a test store resolver; pass nil to restore default behavior.
func SetStoreForTests(fn func(ctx sdk.Context) *Store) {
	sessionsMu.Lock()
	overrideHook = fn
	sessionsMu.Unlock()
}

// AgentDirForContext resolves this product's agent directory from sdk.Context:
//   - PIG_CODING_AGENT_DIR (when set, matching PiG's AgentDir override)
//   - otherwise <ctx.ConfigHome()>/agent (e.g. ~/.pig/agent, $PIG_HOME/agent, $XDG_CONFIG_HOME/pig/agent)
func AgentDirForContext(ctx sdk.Context) string {
	if v := os.Getenv("PIG_CODING_AGENT_DIR"); v != "" {
		return expandTildePath(v)
	}
	if cfgHome, err := ctx.ConfigHome(); err == nil && cfgHome != "" {
		return filepath.Join(cfgHome, "agent")
	}
	return ""
}

// StoreFor returns the session-scoped Store for ctx.
//   - Resolves `IsProjectTrusted()` ONCE when the session entry is created (N4),
//     and refreshes it on `session_start` / `before_agent_start` (T2).
//   - Shares one package-level global layer per resolved config path (N6),
//     refreshed on save or when (dev, inode, ctime, mtime, size) changes (T6).
//   - Caches a store with `globalErr` when the global file is malformed so
//     session overrides persist and reads do not re-parse on every call (N3).
//   - Resolves parent session overrides lazily by parent session ID so child
//     sessions created before their parent still read through live (T3).
//   - For an ended session (tombstoned via releaseSession), returns an ephemeral
//     read-only Store without re-creating any table entry (T4).
func StoreFor(ctx sdk.Context) *Store {
	sessionsMu.Lock()
	hook := overrideHook
	sessionsMu.Unlock()
	if hook != nil {
		if st := hook(ctx); st != nil {
			return st
		}
	}

	sessionID := safeSessionID(ctx)
	if sessionID != "" {
		sessionsMu.Lock()
		ended := isSessionEndedLocked(sessionID)
		st, ok := sessions[sessionID]
		sessionsMu.Unlock()
		if ended {
			// Late call on an ended session must NEVER re-create an entry in `sessions` (T4).
			return buildSessionStore(StoreOptions{
				SessionID: sessionID,
				AgentDir:  AgentDirForContext(ctx),
				Cwd:       safeCwd(ctx),
			}, "")
		}
		if ok && st.store != nil {
			st.store.syncSharedGlobalIfNeeded()
			if err := st.store.LoadError(); err != nil {
				notifySessionOnce(ctx, st, err.Error())
			}
			return st.store
		}
	}

	agentDir := AgentDirForContext(ctx)
	cwd := safeCwd(ctx)
	trusted := safeIsProjectTrusted(ctx)

	var parentID string
	if hook := ParentSession; hook != nil {
		if pid, ok := hook(ctx); ok && pid != "" && pid != sessionID {
			parentID = pid
		}
	}

	st := buildSessionStore(StoreOptions{
		SessionID:      sessionID,
		AgentDir:       agentDir,
		Cwd:            cwd,
		ProjectTrusted: trusted,
	}, parentID)

	entry := &sessionState{
		store:    st,
		warned:   map[string]bool{},
		agentDir: agentDir,
		cwd:      cwd,
		trusted:  trusted,
		parentID: parentID,
	}

	if sessionID != "" {
		sessionsMu.Lock()
		if isSessionEndedLocked(sessionID) {
			sessionsMu.Unlock()
			return st
		}
		if existing, ok := sessions[sessionID]; ok {
			if existing.store != nil {
				sessionsMu.Unlock()
				existing.store.syncSharedGlobalIfNeeded()
				return existing.store
			}
			existing.store = st
			existing.agentDir = agentDir
			existing.cwd = cwd
			existing.trusted = trusted
			existing.parentID = parentID
			entry = existing
		} else {
			sessions[sessionID] = entry
		}
		sessionsMu.Unlock()
	}

	if err := st.LoadError(); err != nil {
		notifySessionOnce(ctx, entry, err.Error())
	}
	for _, w := range st.Warnings() {
		notifySessionOnce(ctx, entry, w)
	}
	return st
}

// buildSessionStore constructs a Store that keeps its sharedGlobal and globalErr
// even if the global config file failed to parse, so session overrides persist
// and the file is only retried when its stat signature changes (N3, T6).
func buildSessionStore(opts StoreOptions, parentID string) *Store {
	loadSchema()
	s := newBlankStore(opts)
	s.parentSessionID = parentID
	if !opts.InMemory {
		_ = s.reloadLocked(false)
	}
	s.rebuildMergedLocked()
	return s
}

// startSession handles `session_start` (including after `/reload` or `/resume`
// when PiG reuses the same session ID after firing `session_shutdown`):
//  1. Clears any tombstone for this session ID first (N2).
//  2. If no entry exists yet, creates it via StoreFor (resolving trust & project layer).
//  3. If an entry already exists, refreshes trust/cwd while keeping overrides.
func startSession(ctx sdk.Context) {
	sessionID := safeSessionID(ctx)
	if sessionID == "" {
		return
	}
	clearSessionTombstone(sessionID)
	sessionsMu.Lock()
	_, exists := sessions[sessionID]
	sessionsMu.Unlock()
	if !exists {
		_ = StoreFor(ctx)
		return
	}
	refreshSessionTrust(ctx)
}

func clearSessionTombstone(sessionID string) {
	if sessionID == "" {
		return
	}
	sessionsMu.Lock()
	if _, ended := endedSet[sessionID]; ended {
		delete(endedSet, sessionID)
		for i, id := range endedRing {
			if id == sessionID {
				endedRing = append(endedRing[:i], endedRing[i+1:]...)
				break
			}
		}
	}
	sessionsMu.Unlock()
}

// refreshSessionTrust re-resolves project trust and cwd at `before_agent_start`
// (one host call per turn). Only reloads the project layer when trust or cwd
// actually changed, preserving all session overrides (T2).
func refreshSessionTrust(ctx sdk.Context) {
	sessionID := safeSessionID(ctx)
	if sessionID == "" || isSessionEnded(sessionID) {
		return
	}
	trusted, err := safeIsProjectTrustedWithErr(ctx)
	if err != nil {
		// Never drop an already-resolved project layer on a transient/pending host error.
		return
	}
	cwd := safeCwd(ctx)

	sessionsMu.Lock()
	entry, ok := sessions[sessionID]
	if !ok || entry == nil || entry.store == nil {
		sessionsMu.Unlock()
		return
	}
	if cwd == "" {
		cwd = entry.cwd
	}
	changed := entry.trusted != trusted || entry.cwd != cwd
	if changed {
		entry.trusted = trusted
		entry.cwd = cwd
	}
	st := entry.store
	sessionsMu.Unlock()

	if !changed {
		return
	}
	_ = st.ReloadForCwd(cwd, trusted)
	for _, w := range st.Warnings() {
		notifySessionOnce(ctx, entry, w)
	}
}
func safeIsProjectTrustedWithErr(ctx sdk.Context) (bool, error) {
	var (
		trusted bool
		err     error
	)
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("context inactive: %v", r)
			}
		}()
		trusted, err = ctx.IsProjectTrusted()
	}()
	return trusted, err
}
func notifySessionOnce(ctx sdk.Context, entry *sessionState, msg string) {
	if msg == "" {
		return
	}
	sessionsMu.Lock()
	if entry.warned == nil {
		entry.warned = map[string]bool{}
	}
	if entry.warned[msg] {
		sessionsMu.Unlock()
		return
	}
	entry.warned[msg] = true
	sessionsMu.Unlock()
	safeNotify(ctx, msg, "warning")
}

// Session grant helpers backed by the single `sessions` table (N4).

func getSessionGrant(sessionID string) (cfgSessionGrant, bool) {
	if sessionID == "" {
		return cfgSessionGrant{}, false
	}
	sessionsMu.Lock()
	defer sessionsMu.Unlock()
	st, ok := sessions[sessionID]
	if !ok || st.grant == nil {
		return cfgSessionGrant{}, false
	}
	return *st.grant, true
}

func setSessionGrant(sessionID string, save bool) {
	if sessionID == "" {
		return
	}
	sessionsMu.Lock()
	defer sessionsMu.Unlock()
	st, ok := sessions[sessionID]
	if !ok {
		st = &sessionState{warned: map[string]bool{}}
		sessions[sessionID] = st
	}
	prevSave := false
	if st.grant != nil {
		prevSave = st.grant.save
	}
	st.grant = &cfgSessionGrant{save: save || prevSave}
}

func clearAllSessionGrants() {
	sessionsMu.Lock()
	defer sessionsMu.Unlock()
	for _, st := range sessions {
		st.grant = nil
	}
}

func safeCwd(ctx sdk.Context) string {
	var cwd string
	func() {
		defer func() { _ = recover() }()
		cwd = ctx.Cwd()
	}()
	return cwd
}

func safeHasUI(ctx sdk.Context) bool {
	var ok bool
	func() {
		defer func() { _ = recover() }()
		ok = ctx.HasUI()
	}()
	return ok
}

func safeMode(ctx sdk.Context) string {
	mode := "print"
	func() {
		defer func() { _ = recover() }()
		mode = ctx.Mode()
	}()
	return mode
}

func safeSessionID(ctx sdk.Context) string {
	var id string
	func() {
		defer func() { _ = recover() }()
		if v, err := ctx.GetSessionID(); err == nil {
			id = v
		}
	}()
	return id
}

func safeIsProjectTrusted(ctx sdk.Context) bool {
	var trusted bool
	func() {
		defer func() { _ = recover() }()
		if v, err := ctx.IsProjectTrusted(); err == nil {
			trusted = v
		}
	}()
	return trusted
}

func safeNotify(ctx sdk.Context, message, level string) {
	func() {
		defer func() { _ = recover() }()
		ctx.Notify(message, level)
	}()
}

// Package-level typed getters using ctx's Store, falling back to omp's defaults.

func Bool(ctx sdk.Context, key string) bool        { return StoreFor(ctx).Bool(key) }
func Int(ctx sdk.Context, key string) int          { return StoreFor(ctx).Int(key) }
func Float(ctx sdk.Context, key string) float64    { return StoreFor(ctx).Float(key) }
func String(ctx sdk.Context, key string) string    { return StoreFor(ctx).String(key) }
func Strings(ctx sdk.Context, key string) []string { return StoreFor(ctx).Strings(key) }
func Any(ctx sdk.Context, key string) any          { return StoreFor(ctx).Any(key) }

// OperatorAny returns key's effective value from operator layers only (no project layer).
// Use for security-sensitive settings like bash.patterns, task.perCallModel, task.perCallModelAllow.
func OperatorAny(ctx sdk.Context, key string) any {
	v, _ := StoreFor(ctx).GetOperator(key)
	return v
}

// Set updates key on ctx's Store after validating against the schema.
// When persist is true, writes to global config.yml; otherwise sets a session override.
func Set(ctx sdk.Context, key string, value any, persist bool) error {
	return StoreFor(ctx).Set(key, value, persist)
}

// Enabled reports whether a boolean setting is enabled, falling back to def if
// the key is unknown or unset. Other areas take this as a function variable so
// they need not depend on internal/settings directly during standalone tests.
func Enabled(ctx sdk.Context, key string, def bool) bool {
	entry, ok := Lookup(key)
	if !ok {
		return def
	}
	v, ok := StoreFor(ctx).Get(key)
	if !ok || v == nil {
		return def
	}
	if b, ok := v.(bool); ok {
		return b
	}
	if entry.Type == TypeEnum {
		if s, ok := v.(string); ok {
			return s != "off" && s != "false" && s != ""
		}
	}
	return def
}

// AskTimeoutMs returns ask.timeout in milliseconds, or 0 when disabled (0 seconds).
// upstream: packages/coding-agent/src/modes/settings.ts askTimeoutMs
func AskTimeoutMs(ctx sdk.Context) float64 {
	sec := Float(ctx, "ask.timeout")
	if sec <= 0 {
		return 0
	}
	return sec * 1000
}
