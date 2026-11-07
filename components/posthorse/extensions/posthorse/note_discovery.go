package posthorse

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Port of src/note-discovery.ts.

// discoveredNote is DiscoveredNote (note-discovery.ts:20).
type discoveredNote struct {
	noteFile
	address noteAddress
	lineage noteAddress
	seed    *noteSeed
	copies  []noteAddress
}

// scopeInventory is ScopeInventory (note-discovery.ts:26).
type scopeInventory struct {
	scope    noteScope
	manifest *noteBinding
}

// discoveryOptions is DiscoveryOptions (note-discovery.ts:27): path and scope nil are undefined.
type discoveryOptions struct {
	path  *string
	scope *string
}

// discoveryResult is the result of NoteDiscovery.list.
type discoveryResult struct {
	files   []discoveredNote
	notices []string
	labeled bool
}

// digestCache is DigestCache (note-discovery.ts:36).
type digestCache struct {
	identity noteIdentity
	digest   string
	revision int
}

// projectScopes: note-discovery.ts:41.
func projectScopes(session *sessionNotes, notice func(string)) ([]scopeInventory, error) {
	scopes := []scopeInventory{{scope: noteScope{root: session.binding.root}}}
	directory := filepath.Join(session.binding.root, ".forks")
	entries, err := readDirNode(directory)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		entries = nil
	}
	candidates := make([]fs.DirEntry, 0, len(entries))
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".staging-") {
			candidates = append(candidates, entry)
		}
	}
	manifests, err := mapBatch(candidates, func(entry fs.DirEntry) (*noteBinding, error) {
		if !entry.IsDir() {
			return nil, nil
		}
		return readManifest(filepath.Join(directory, entry.Name(), "notes"))
	})
	if err != nil {
		return nil, err
	}
	for i, entry := range candidates {
		manifest := manifests[i]
		if manifest == nil || manifest.root != session.binding.root {
			notice("Not indexed: unmanaged or incomplete notes data at " + filepath.Join(directory, entry.Name()) + ".")
		} else {
			scopes = append(scopes, scopeInventory{scope: manifest.scopeOf(), manifest: manifest})
		}
	}
	present := false
	for _, s := range scopes {
		if sameScope(s.scope, session.binding) {
			present = true
			break
		}
	}
	if !present {
		inv := scopeInventory{scope: session.binding.scopeOf()}
		if session.binding.kind == "fork" {
			inv.manifest = session.binding
		}
		scopes = append(scopes, inv)
	}
	return scopes, nil
}

// scopedFiles: note-discovery.ts:78.
func scopedFiles(ctx sdk.Context, inventory scopeInventory, options discoveryOptions, notice func(string)) ([]discoveredNote, error) {
	files, err := notesUnder(signalOf(ctx), inventory.scope, options.path, notice)
	if err != nil {
		return nil, err
	}
	var seeds map[string]*noteSeed
	if inventory.manifest != nil {
		seeds = make(map[string]*noteSeed, len(inventory.manifest.seeds))
		for i := range inventory.manifest.seeds {
			seeds[inventory.manifest.seeds[i].path] = &inventory.manifest.seeds[i]
		}
	}
	directory := scopeDirectory(inventory.scope)
	out := make([]discoveredNote, len(files))
	for i, file := range files {
		path := nodeRelative(directory, file.path)
		address := noteAddress{noteScope: inventory.scope.plain(), path: path}
		seed := seeds[path]
		lineage := address
		if seed != nil {
			lineage = seed.lineage
		}
		out[i] = discoveredNote{noteFile: file, address: address, seed: seed, lineage: lineage}
	}
	return out, nil
}

// noteKey: note-discovery.ts:101.
func noteKey(address noteAddress) string {
	b := append(make([]byte, 0, 64), '[')
	b = appendStr(b, address.root)
	b = append(b, ',')
	b = appendStr(b, address.scope)
	b = append(b, ',')
	b = appendStr(b, address.path)
	return string(append(b, ']'))
}

func readTarget[S scopeLike](scope S, path string) string {
	b := append(make([]byte, 0, 64), `{"scope":`...)
	b = appendStr(b, scopeDirectory(scope))
	b = append(b, `,"path":`...)
	b = appendStr(b, path)
	return string(append(b, '}'))
}

// noteReadTarget: note-discovery.ts:104.
func noteReadTarget(note *discoveredNote) string {
	return readTarget(note.address, note.address.path)
}

// noteLabel: note-discovery.ts:107.
func noteLabel(note *discoveredNote, session *sessionNotes, labeled bool) string {
	if !labeled {
		return note.address.path
	}
	mode := "read-only"
	if sameScope(note.address, session.binding) {
		mode = "current"
	}
	copies := ""
	if len(note.copies) > 0 {
		parts := make([]string, len(note.copies))
		for i, c := range note.copies {
			parts[i] = readTarget(c, c.path)
		}
		copies = "; identical copies: " + strings.Join(parts, ", ")
	}
	return note.address.path + " [" + mode + "; read " + noteReadTarget(note) + copies + "]"
}

// readReference: note-discovery.ts:119.
func readReference(ctx sdk.Context, session *sessionNotes, entries entriesRead, path string) (*discoveredNote, error) {
	target, err := session.readWith(ctx, entries, path, nil)
	if err != nil {
		return nil, err
	}
	if target.writable {
		return nil, nil
	}
	info, err := os.Stat(target.path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		err = nodeFsError(err, "stat", target.path)
	}
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil
	}
	return &discoveredNote{
		noteFile: noteFile{noteIdentity: fileIdentity(target.path, info), path: target.path},
		address:  target.address, lineage: target.address,
	}, nil
}

// noteDiscovery is NoteDiscovery (note-discovery.ts:145).
type noteDiscovery struct {
	mu      sync.Mutex
	digests map[string]digestCache
}

func newNoteDiscovery() *noteDiscovery { return &noteDiscovery{digests: map[string]digestCache{}} }

// knownDigest: note-discovery.ts:147.
func (d *noteDiscovery) knownDigest(note discoveredNote, revision int) (string, bool) {
	d.mu.Lock()
	cached, ok := d.digests[note.path]
	d.mu.Unlock()
	if ok && cached.revision == revision && sameIdentity(cached.identity, note.noteIdentity) {
		return cached.digest, true
	}
	if revision == 0 && note.seed != nil && sameIdentity(note.seed.identity, note.noteIdentity) {
		return note.seed.digest, true
	}
	return "", false
}

// digest: note-discovery.ts:162. ok is false for `undefined`.
func (d *noteDiscovery) digest(ctx sdk.Context, note discoveredNote, session *sessionNotes, notice func(string)) (string, bool, error) {
	signal := signalOf(ctx)
	revision, err := session.revision(note.path)
	if err != nil {
		return "", false, err
	}
	if known, ok := d.knownDigest(note, revision); ok {
		return known, true, nil
	}
	digest, err := fileDigest(signal, note.path)
	if err == nil {
		var info fs.FileInfo
		if info, err = os.Stat(note.path); err != nil {
			err = nodeFsError(err, "stat", note.path)
		} else {
			if !sameIdentity(note.noteIdentity, fileIdentity(note.path, info)) {
				return "", false, nil
			}
			d.mu.Lock()
			d.digests[note.path] = digestCache{identity: note.noteIdentity, digest: digest, revision: revision}
			d.mu.Unlock()
			return digest, true, nil
		}
	}
	if abort := ctxErr(signal); abort != nil {
		return "", false, abort
	}
	notice("Duplicate comparison unavailable for " + noteReadTarget(&note) + ": " + err.Error() + ". Copies remain separate.")
	return "", false, nil
}

// collect: note-discovery.ts:186 (#collapse).
func (d *noteDiscovery) collapse(ctx sdk.Context, files []discoveredNote, session *sessionNotes, notice func(string)) ([]discoveredNote, error) {
	groups := map[string][]discoveredNote{}
	var order []string
	for _, file := range files {
		key := noteKey(file.lineage) + "\x00" + strconv.FormatFloat(file.size, 'f', -1, 64)
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], file)
	}
	var selected []discoveredNote
	for _, key := range order {
		group := groups[key]
		if len(group) == 1 {
			selected = append(selected, group...)
			continue
		}
		sort.SliceStable(group, func(i, j int) bool {
			return sameScope(group[i].address, session.binding) && !sameScope(group[j].address, session.binding)
		})
		type digestResult struct {
			file    discoveredNote
			digest  string
			has     bool
			notices []string
		}
		results, err := mapBatch(group, func(file discoveredNote) (digestResult, error) {
			var fileNotices []string
			digest, has, err := d.digest(ctx, file, session, func(text string) { fileNotices = append(fileNotices, text) })
			return digestResult{file: file, digest: digest, has: has, notices: fileNotices}, err
		})
		if err != nil {
			return nil, err
		}
		var matchKeys []string
		matches := map[string]discoveredNote{}
		for _, r := range results {
			for _, text := range r.notices {
				notice(text)
			}
			// Only same-lineage, same-size candidates need content comparison.
			mk := noteKey(r.file.address)
			if r.has {
				mk = r.digest
			}
			previous, seen := matches[mk]
			if !seen || !r.has {
				if !seen {
					matchKeys = append(matchKeys, mk)
				}
				matches[mk] = r.file
			} else {
				previous.copies = append(append(make([]noteAddress, 0, len(previous.copies)+1), previous.copies...), r.file.address)
				matches[mk] = previous
			}
		}
		for _, mk := range matchKeys {
			selected = append(selected, matches[mk])
		}
	}
	return selected, nil
}

// references: note-discovery.ts:233 (#references).
func (d *noteDiscovery) references(ctx sdk.Context, session *sessionNotes, options discoveryOptions) ([]discoveredNote, error) {
	if session.binding.kind != "fork" || options.scope != nil {
		return nil, nil
	}
	recovery, err := session.recovery(ctx)
	if err != nil {
		return nil, err
	}
	branch, err := session.branch(ctx)
	if err != nil {
		return nil, err
	}
	projected, err := session.projection(ctx)
	if err != nil {
		return nil, err
	}
	directories := make([]string, len(session.scopes))
	for i, s := range session.scopes {
		directories[i] = scopeDirectory(s)
	}
	evidence := sessionNoteTouches(noteEvidence{
		branch: branch, allEntries: recovery.entries, projected: projected, directories: directories,
	})
	seen := map[string]struct{}{}
	var candidates []string
	for _, touch := range evidence.touches {
		path := touch.notePath
		if _, dup := seen[path]; dup {
			continue
		}
		seen[path] = struct{}{}
		owns, err := session.owns(path)
		if err != nil {
			return nil, err
		}
		if !owns && (options.path == nil || path == *options.path || strings.HasPrefix(path, *options.path+"/")) {
			candidates = append(candidates, path)
		}
	}
	// One read of the entries per batch of 16, as in the original (note-store.ts:132).
	resolved, err := mapBatchPrepared(candidates,
		func() (*sharedEntries, error) {
			return &sharedEntries{read: ctx.SessionManager().GetEntries}, nil
		},
		func(shared *sharedEntries, path string) (*discoveredNote, error) {
			return readReference(ctx, session, shared.get, path)
		})
	if err != nil {
		return nil, err
	}
	var files []discoveredNote
	for _, file := range resolved {
		if file != nil {
			files = append(files, *file)
		}
	}
	return files, nil
}

// list: note-discovery.ts:268.
func (d *noteDiscovery) list(ctx sdk.Context, session *sessionNotes, options discoveryOptions) (discoveryResult, error) {
	var notices []string
	notice := func(text string) { notices = append(notices, text) }
	var scopes []scopeInventory
	var references []discoveredNote
	var scopesErr, referencesErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if options.scope == nil {
			scopes, scopesErr = projectScopes(session, notice)
			return
		}
		scope, err := session.selectScope(ctx, *options.scope)
		if err != nil {
			scopesErr = err
			return
		}
		manifest, err := readManifest(*options.scope)
		if err != nil {
			scopesErr = err
			return
		}
		scopes = []scopeInventory{{scope: scope, manifest: manifest}}
	}()
	go func() {
		defer wg.Done()
		references, referencesErr = d.references(ctx, session, options)
	}()
	wg.Wait()
	if scopesErr != nil {
		return discoveryResult{}, scopesErr
	}
	if referencesErr != nil {
		return discoveryResult{}, referencesErr
	}
	type scoped struct {
		files   []discoveredNote
		notices []string
	}
	results, err := mapBatch(scopes, func(scope scopeInventory) (scoped, error) {
		var scopeNotices []string
		files, err := scopedFiles(ctx, scope, options, func(text string) { scopeNotices = append(scopeNotices, text) })
		return scoped{files: files, notices: scopeNotices}, err
	})
	if err != nil {
		return discoveryResult{}, err
	}
	var files []discoveredNote
	for _, r := range results {
		// Preserve native listing order while walking each scope independently.
		notices = append(notices, r.notices...)
		files = append(files, r.files...)
	}
	for _, reference := range references {
		key := noteKey(reference.address)
		exists := false
		for _, file := range files {
			if noteKey(file.address) == key {
				exists = true
				break
			}
		}
		if !exists {
			files = append(files, reference)
		}
	}
	selected := files
	if options.scope == nil {
		if selected, err = d.collapse(ctx, files, session, notice); err != nil {
			return discoveryResult{}, err
		}
	}
	sort.SliceStable(selected, func(i, j int) bool { return selected[i].mtimeMs > selected[j].mtimeMs })
	labeled := len(scopes) > 1
	for _, s := range scopes {
		if !sameScope(s.scope, session.binding) {
			labeled = true
		}
	}
	return discoveryResult{files: selected, notices: notices, labeled: labeled}, nil
}
