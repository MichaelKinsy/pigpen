package posthorse

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Port of src/note-provenance.ts.

// noteSource is NoteSource (note-provenance.ts:24): the journal metadata of the parent session chain
// and the parent's file (nil = undefined).
type noteSource struct {
	entries []rec
	file    *string
}

// noteCandidate is Candidate (note-provenance.ts:28).
type noteCandidate struct {
	snapshotNote
	cleared bool
}

// includedJournalType: note-provenance.ts:29.
func includedJournalType(entryType string, hasType, legacy bool) bool {
	return legacy || !hasType || entryType == "session" || entryType == "custom"
}

// journalHeadType is `/^\s*\{\s*"type"\s*:\s*"([^"]+)"/u.exec(head)?.[1]` (note-provenance.ts:39).
func journalHeadType(head string) (string, bool) {
	rest := jsTrimStart(head)
	skip := func(prefix string) bool {
		if !strings.HasPrefix(rest, prefix) {
			return false
		}
		rest = jsTrimStart(rest[len(prefix):])
		return true
	}
	if !skip("{") || !skip(`"type"`) || !skip(":") || !strings.HasPrefix(rest, `"`) {
		return "", false
	}
	rest = rest[1:]
	end := strings.IndexByte(rest, '"')
	if end <= 0 {
		return "", false
	}
	return rest[:end], true
}

// journalMetadata: note-provenance.ts:32.
func journalMetadata(ctx context.Context, file string, legacy bool) ([]rec, error) {
	var entries []rec
	for line, err := range sessionLines(ctx, file, nil) {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return entries, nil
			}
			return nil, err
		}
		entryType, hasType := journalHeadType(line.head)
		if !includedJournalType(entryType, hasType, legacy) {
			continue
		}
		text, err := line.text()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return entries, nil
			}
			return nil, err
		}
		entry, ok := parseSessionLine(text)
		if !ok {
			continue
		}
		t, has := str(entry, "type")
		if includedJournalType(t, has, legacy) {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

// sourceEntries: note-provenance.ts:55. Read only; opening a SessionManager here could migrate or
// create an archived source.
func sourceEntries(ctx context.Context, file *string) ([]rec, error) {
	seen := map[string]struct{}{}
	var entries []rec
	for current := file; current != nil; {
		if _, dup := seen[*current]; dup {
			break
		}
		seen[*current] = struct{}{}
		// Each ancestor's current ownership prevents falling through a deleted owned file.
		metadata, err := journalMetadata(ctx, *current, false)
		if err != nil {
			return nil, err
		}
		entries = append(entries, metadata...)
		current = nil
		for _, entry := range metadata {
			if t, _ := str(entry, "type"); t == "session" {
				if parent, ok := str(entry, "parentSession"); ok {
					current = &parent
				}
				break
			}
		}
	}
	return entries, nil
}

// legacySource: note-provenance.ts:74.
func legacySource(entries []rec) (noteScope, bool, error) {
	evidence := sessionNoteTouches(noteEvidence{branch: entries, allEntries: entries})
	var roots []string
	if evidence.defaultDir != "" {
		roots = append(roots, evidence.defaultDir)
	}
	for _, touch := range evidence.touches {
		if touch.absPath != "" && strings.HasSuffix(touch.absPath, "/"+touch.notePath) {
			roots = append(roots, touch.absPath[:len(touch.absPath)-len(touch.notePath)-1])
		}
	}
	var canonical []string
	seen := map[string]struct{}{}
	for _, root := range roots {
		if !nodeIsAbsolute(root) {
			return noteScope{}, false, nil
		}
	}
	for _, root := range roots {
		c, err := canonicalPath(root)
		if err != nil {
			return noteScope{}, false, err
		}
		if _, dup := seen[c]; !dup {
			seen[c] = struct{}{}
			canonical = append(canonical, c)
		}
	}
	if len(canonical) == 1 {
		return noteScope{root: canonical[0]}, true, nil
	}
	return noteScope{}, false, nil
}

// sourceScope: note-provenance.ts:93.
func sourceScope(ctx sdk.Context, entries []rec, source noteSource) (noteScope, bool, error) {
	var headerID, headerTimestamp string
	var hasID, hasTimestamp bool
	for _, entry := range source.entries {
		if t, _ := str(entry, "type"); t == "session" {
			headerID, hasID = str(entry, "id")
			headerTimestamp, hasTimestamp = str(entry, "timestamp")
			break
		}
	}
	all := make([]rec, 0, len(entries)+len(source.entries))
	all = append(append(all, entries...), source.entries...)
	bindings := bindingsOf(all)
	var binding *noteBinding
	for i := len(bindings) - 1; i >= 0; i-- {
		candidate := bindings[i]
		if !hasID || !hasTimestamp || (candidate.owner.id == headerID && candidate.owner.timestamp == headerTimestamp) {
			binding = candidate
			break
		}
	}
	if binding != nil {
		return binding.scopeOf(), true, nil
	}
	legacy := entries
	if source.file != nil {
		fromFile, err := journalMetadata(nil, *source.file, true)
		if err != nil {
			return noteScope{}, false, err
		}
		if len(fromFile) > 0 {
			legacy = fromFile
		}
	}
	return legacySource(legacy)
}

// resolveReference: note-provenance.ts:116.
func resolveReference[S scopeLike](scope S, path string, entries []rec) (noteAddress, bool, error) {
	source := new(noteScope)
	*source = scope.scopeOf()
	bindings := bindingsOf(entries)
	visited := map[string]struct{}{}
	for source != nil {
		current := *source
		directory := scopeDirectory(current)
		if _, dup := visited[directory]; dup {
			return noteAddress{}, false, errors.New("Cyclic note provenance at " + directory + ".")
		}
		visited[directory] = struct{}{}
		candidate, err := notePath(current, path)
		if err != nil {
			return noteAddress{}, false, err
		}
		if _, err := os.Stat(candidate); err == nil {
			return withPath(current, path), true, nil
		}
		// A manifest includes later adoptions; the journal includes later writes and deletions.
		binding, err := readManifest(directory)
		if err != nil {
			return noteAddress{}, false, err
		}
		if binding == nil {
			for i := len(bindings) - 1; i >= 0; i-- {
				if sameScope(bindings[i], current) {
					binding = bindings[i]
					break
				}
			}
		}
		if binding == nil || binding.kind == "project" {
			return noteAddress{}, false, nil
		}
		if _, owned := ownedPaths(binding, entries)[path]; owned {
			return noteAddress{}, false, nil
		}
		source = binding.source
	}
	return noteAddress{}, false, nil
}

// orderedCandidates is a Map<string, Candidate>: setting an existing key keeps its position.
type orderedCandidates struct {
	keys   []string
	values map[string]noteCandidate
}

func (m *orderedCandidates) set(path string, c noteCandidate) {
	if m.values == nil {
		m.values = map[string]noteCandidate{}
	}
	if _, ok := m.values[path]; !ok {
		m.keys = append(m.keys, path)
	}
	m.values[path] = c
}

func (m *orderedCandidates) has(path string) bool { _, ok := m.values[path]; return ok }

// touchedCandidates: note-provenance.ts:147.
func touchedCandidates(evidence noteEvidenceResult, scopes []noteScope, allEntries []rec) (*orderedCandidates, error) {
	bindings := bindingsOf(allEntries)
	selected := &orderedCandidates{}
	index := newScopeIndex(scopes)
	seedsByOwner := map[*noteBinding][]noteSeed{}
	for _, touch := range evidence.touches {
		absolute := touch.absPath
		if absolute == "" && evidence.defaultDir != "" {
			absolute = filepath.Join(evidence.defaultDir, touch.notePath)
		}
		if absolute == "" {
			continue
		}
		address, ok, err := index.addressOf(absolute)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		var owner *noteBinding
		for i := len(bindings) - 1; i >= 0; i-- {
			if sameScope(bindings[i], address) {
				owner = bindings[i]
				break
			}
		}
		lineage := address
		if owner != nil {
			seeds, cached := seedsByOwner[owner]
			if !cached {
				seeds = seedsOf(owner, allEntries)
				seedsByOwner[owner] = seeds
			}
			for _, seed := range seeds {
				if seed.path == address.path {
					lineage = seed.lineage
					break
				}
			}
		}
		anchor := touch.receiptId
		if anchor == "" {
			anchor = touch.entryId
		}
		selected.set(address.path, noteCandidate{
			snapshotNote: snapshotNote{path: address.path, source: address, lineage: lineage, anchors: []string{anchor}},
			cleared:      touch.kind == "clear",
		})
	}
	return selected, nil
}

// seedCandidates: note-provenance.ts:176.
func seedCandidates(ctx sdk.Context, entries []rec, source noteSource, scope *noteScope) ([]noteCandidate, error) {
	manager := ctx.SessionManager()
	allEntries := make([]rec, 0, len(entries)+len(source.entries))
	allEntries = append(append(allEntries, entries...), source.entries...)
	bindings := bindingsOf(allEntries)
	scopes := make([]noteScope, 0, len(bindings)+1)
	for _, b := range bindings {
		scopes = append(scopes, b.scopeOf())
	}
	if scope != nil {
		scopes = append(scopes, *scope)
	}
	branch, err := manager.GetBranch(nil)
	if err != nil {
		return nil, err
	}
	projection, err := manager.BuildSessionProjection()
	if err != nil {
		return nil, err
	}
	projected := make([]projectedEntry, len(projection.Entries))
	for i, p := range projection.Entries {
		projected[i] = projectedEntry{sourceEntry: p.SourceEntry, messages: p.Messages}
	}
	directories := make([]string, len(scopes))
	for i, s := range scopes {
		directories[i] = scopeDirectory(s)
	}
	evidence := sessionNoteTouches(noteEvidence{
		branch: branch, allEntries: allEntries, projected: projected, directories: directories,
	})
	if scope == nil && len(evidence.touches) > 0 {
		return nil, errors.New("Incomplete notes snapshot: the source notes root is missing or ambiguous.")
	}
	selected, err := touchedCandidates(evidence, scopes, allEntries)
	if err != nil {
		return nil, err
	}
	for _, binding := range bindings {
		for _, seed := range seedsOf(binding, allEntries) {
			if selected.has(seed.path) {
				continue
			}
			for _, anchor := range seed.anchors {
				if _, visible := evidence.visibleIds[anchor]; visible {
					selected.set(seed.path, noteCandidate{snapshotNote: snapshotNote{
						path: seed.path, anchors: seed.anchors, lineage: seed.lineage,
						source: noteAddress{noteScope: noteScope{root: binding.root, scope: binding.scope}, path: seed.path},
					}})
					break
				}
			}
		}
	}
	out := make([]noteCandidate, len(selected.keys))
	for i, key := range selected.keys {
		out[i] = selected.values[key]
	}
	return out, nil
}

// forkBinding: note-provenance.ts:213.
func forkBinding(ctx sdk.Context, entries []rec, binding *noteBinding, source noteSource) (*noteBinding, error) {
	scope, hasScope, err := sourceScope(ctx, entries, source)
	if err != nil {
		return nil, err
	}
	var scopePtr *noteScope
	if hasScope {
		scopePtr = &scope
	}
	candidates, err := seedCandidates(ctx, entries, source, scopePtr)
	if err != nil {
		return nil, err
	}
	all := make([]rec, 0, len(entries)+len(source.entries))
	all = append(append(all, entries...), source.entries...)
	addresses, err := mapBatch(candidates, func(candidate noteCandidate) (*noteAddress, error) {
		base := candidate.source.noteScope
		if hasScope {
			base = scope
		}
		address, ok, err := resolveReference(base, candidate.path, all)
		if err != nil || !ok {
			return nil, err
		}
		return &address, nil
	})
	if err != nil {
		return nil, err
	}
	files := make([]snapshotNote, 0, len(candidates))
	absent := []string{}
	for i, candidate := range candidates {
		// Resolve against the immediate parent's files, not a historical ancestor's version.
		switch {
		case addresses[i] != nil:
			files = append(files, snapshotNote{path: candidate.path, source: *addresses[i], lineage: candidate.lineage, anchors: candidate.anchors})
		case candidate.cleared:
			absent = append(absent, candidate.path)
		default:
			return nil, errors.New("Incomplete notes snapshot: source note " + candidate.path + " is unavailable.")
		}
	}
	next := *binding
	next.absent = absent
	next.source = nil
	if hasScope {
		next.source = &noteScope{root: scope.root, scope: scope.scope}
	}
	return snapshot(&next, files)
}

// loadNotesBinding: note-provenance.ts:249.
func loadNotesBinding(ctx sdk.Context, root string) (binding *noteBinding, source noteSource, created bool, err error) {
	manager := ctx.SessionManager()
	header, err := manager.GetHeader()
	if err != nil {
		return nil, noteSource{}, false, err
	}
	if header == nil {
		return nil, noteSource{}, false, errors.New("Pi did not supply the session header for notes ownership.")
	}
	entries, err := manager.GetEntries()
	if err != nil {
		return nil, noteSource{}, false, err
	}
	id, _ := str(header, "id")
	timestamp, _ := str(header, "timestamp")
	owner := noteOwner{id: id, timestamp: timestamp}
	var own *noteBinding
	bindings := bindingsOf(entries)
	for i := len(bindings) - 1; i >= 0; i-- {
		if sameOwner(bindings[i].owner, owner) {
			own = bindings[i]
			break
		}
	}
	var parent *string
	if p, ok := str(header, "parentSession"); ok {
		parent = &p
	}
	journal, err := sourceEntries(nil, parent)
	if err != nil {
		return nil, noteSource{}, false, err
	}
	source = noteSource{entries: journal, file: parent}
	if own != nil {
		return own, source, false, nil
	}
	all := make([]rec, 0, len(entries)+len(journal))
	all = append(append(all, entries...), journal...)
	fork := parent != nil || len(bindingsOf(all)) > 0
	if !fork {
		return &noteBinding{kind: "project", owner: owner, root: root}, source, true, nil
	}
	scope, err := forkScope(owner)
	if err != nil {
		return nil, noteSource{}, false, err
	}
	binding, err = forkBinding(ctx, entries, &noteBinding{
		kind: "fork", owner: owner, root: root, scope: scope, absent: []string{},
	}, source)
	if err != nil {
		return nil, noteSource{}, false, err
	}
	return binding, source, true, nil
}
