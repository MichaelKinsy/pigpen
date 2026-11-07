package posthorse

import (
	"errors"
	"os"
	"path/filepath"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Port of src/note-store.ts.

// noteCall is NoteCall (note-store.ts:34).
type noteCall struct{ id, rootId string }

// noteRead is NoteRead (note-store.ts:36).
type noteRead struct {
	address  noteAddress
	path     string
	writable bool
}

// sessionNotes is SessionNotes (note-store.ts:41). One instance serves every handler of a session
// (parallel tool calls included), so its mutable state sits behind mu; nothing in it keeps a host
// context: every method that reads the session or the host takes the caller's ctx.
type sessionNotes struct {
	binding *noteBinding
	source  noteSource
	scopes  []noteScope

	mu        sync.Mutex
	owned     map[string]struct{}
	calls     map[string]string
	revisions map[string]int
}

// newSessionNotes is the constructor (note-store.ts:51); entries are manager.getEntries().
func newSessionNotes(binding *noteBinding, entries []rec, source noteSource) *sessionNotes {
	all := make([]rec, 0, len(entries)+len(source.entries))
	all = append(append(all, entries...), source.entries...)
	scopes := []noteScope{binding.scopeOf(), {root: binding.root}}
	if binding.kind == "fork" && binding.source != nil {
		scopes = append(scopes, *binding.source)
	}
	for _, b := range bindingsOf(all) {
		scopes = append(scopes, b.scopeOf())
	}
	return &sessionNotes{
		binding:   binding,
		source:    source,
		scopes:    scopes,
		owned:     ownedPaths(binding, entries),
		calls:     map[string]string{},
		revisions: map[string]int{},
	}
}

// directory: note-store.ts:65.
func (s *sessionNotes) directory() string { return scopeDirectory(s.binding) }

// branch: note-store.ts:68.
func (s *sessionNotes) branch(ctx sdk.Context) ([]rec, error) {
	return ctx.SessionManager().GetBranch(nil)
}

// projection: note-store.ts:71.
func (s *sessionNotes) projection(ctx sdk.Context) ([]projectedEntry, error) {
	projection, err := ctx.SessionManager().BuildSessionProjection()
	if err != nil {
		return nil, err
	}
	out := make([]projectedEntry, len(projection.Entries))
	for i, p := range projection.Entries {
		out[i] = projectedEntry{sourceEntry: p.SourceEntry, messages: p.Messages}
	}
	return out, nil
}

// revision: note-store.ts:74.
func (s *sessionNotes) revision(path string) (int, error) {
	canonical, err := canonicalPath(path)
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.revisions[canonical], nil
}

// recovery: note-store.ts:77.
func (s *sessionNotes) recovery(ctx sdk.Context) (notesRecovery, error) {
	entries, err := ctx.SessionManager().GetEntries()
	if err != nil {
		return notesRecovery{}, err
	}
	return notesRecovery{binding: s.binding, entries: entries}, nil
}

// startCall: note-store.ts:80.
func (s *sessionNotes) startCall(id string, parent *string) noteCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	rootID := id
	if parent != nil {
		rootID = *parent
		if known, ok := s.calls[*parent]; ok {
			rootID = known
		}
	}
	s.calls[id] = rootID
	return noteCall{id: id, rootId: rootID}
}

// finishCall: note-store.ts:85.
func (s *sessionNotes) finishCall(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.calls, id)
}

// call: note-store.ts:88.
func (s *sessionNotes) call(id string) noteCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	if root, ok := s.calls[id]; ok {
		return noteCall{id: id, rootId: root}
	}
	return noteCall{id: id, rootId: id}
}

// owns: note-store.ts:91.
func (s *sessionNotes) owns(path string) (bool, error) {
	if s.binding.kind == "project" {
		return true, nil
	}
	s.mu.Lock()
	_, owned := s.owned[path]
	s.mu.Unlock()
	if owned {
		return true, nil
	}
	target, err := notePath(s.binding, path)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(target)
	return err == nil, nil
}

// record: note-store.ts:98.
func (s *sessionNotes) record(ctx sdk.Context, files []noteTouchFile, call noteCall) error {
	if len(files) == 0 {
		return nil
	}
	for _, file := range files {
		if file.kind != "read" && sameScope(file, s.binding) {
			s.mu.Lock()
			s.owned[file.path] = struct{}{}
			s.mu.Unlock()
			target, err := notePath(file, file.path)
			if err != nil {
				return err
			}
			path, err := canonicalPath(target)
			if err != nil {
				return err
			}
			s.mu.Lock()
			s.revisions[path]++
			s.mu.Unlock()
		}
	}
	return ctx.AppendEntry(notesTouchType, noteTouchRecord{
		owner:          s.binding.owner,
		scope:          noteScope{root: s.binding.root, scope: s.binding.scope},
		rootToolCallId: call.rootId,
		toolCallId:     call.id,
		files:          files,
	})
}

// selectScope: note-store.ts:114.
func (s *sessionNotes) selectScope(ctx sdk.Context, selector string) (noteScope, error) {
	directory, err := canonicalPath(selector)
	if err != nil {
		return noteScope{}, err
	}
	for _, scope := range s.scopes {
		canonical, err := canonicalPath(scopeDirectory(scope))
		if err != nil {
			return noteScope{}, err
		}
		if canonical == directory {
			return scope, nil
		}
	}
	manifest, err := readManifest(directory)
	if err != nil {
		return noteScope{}, err
	}
	if manifest != nil && manifest.root == s.binding.root && inside(filepath.Join(s.binding.root, ".forks"), directory) {
		return manifest.scopeOf(), nil
	}
	return noteScope{}, errors.New("Unknown notes scope " + selector + ". Copy a scope selector from notes list/search.")
}

// entriesRead reads the session's entries (`manager.getEntries()`); nil reads them from the host. A batch of
// concurrent reads shares one read (sharedEntries): in the original the synchronous prefix of every call of a
// mapBatch batch runs before any of them awaits, so all of them see the same array, and in Go each read is a
// host round trip of the whole journal.
type entriesRead func() ([]rec, error)

// sharedEntries reads the entries once for every caller of get.
type sharedEntries struct {
	once    sync.Once
	read    func() ([]rec, error)
	entries []rec
	err     error
}

func (e *sharedEntries) get() ([]rec, error) {
	e.once.Do(func() { e.entries, e.err = e.read() })
	return e.entries, e.err
}

// reference: note-store.ts:132 (#reference). found is false for `undefined`.
func (s *sessionNotes) reference(ctx sdk.Context, read entriesRead, path string, withSignal bool) (noteAddress, bool, error) {
	if s.binding.kind != "fork" || s.binding.source == nil {
		return noteAddress{}, false, nil
	}
	if read == nil {
		read = ctx.SessionManager().GetEntries
	}
	entries, err := read()
	if err != nil {
		return noteAddress{}, false, err
	}
	signal := signalOf(ctx)
	if !withSignal {
		signal = nil
	}
	journal, err := sourceEntries(signal, s.source.file)
	if err != nil {
		return noteAddress{}, false, err
	}
	all := make([]rec, 0, len(entries)+len(s.source.entries)+len(journal))
	all = append(append(append(all, entries...), s.source.entries...), journal...)
	return resolveReference(*s.binding.source, path, all)
}

// read: note-store.ts:144. scope nil is undefined.
func (s *sessionNotes) read(ctx sdk.Context, path string, scope *string) (noteRead, error) {
	return s.readWith(ctx, nil, path, scope)
}

// readWith is read with the entries of a batch (see entriesRead); nil reads them when needed.
func (s *sessionNotes) readWith(ctx sdk.Context, entries entriesRead, path string, scope *string) (noteRead, error) {
	relative, err := noteRelative(path)
	if err != nil {
		return noteRead{}, err
	}
	if scope != nil {
		selected, err := s.selectScope(ctx, *scope)
		if err != nil {
			return noteRead{}, err
		}
		target, err := notePath(selected, relative)
		if err != nil {
			return noteRead{}, err
		}
		return noteRead{address: withPath(selected, relative), path: target, writable: sameScope(selected, s.binding)}, nil
	}
	owns, err := s.owns(relative)
	if err != nil {
		return noteRead{}, err
	}
	var reference noteAddress
	var found bool
	if !owns {
		if reference, found, err = s.reference(ctx, entries, relative, true); err != nil {
			return noteRead{}, err
		}
	}
	address := reference
	if !found {
		address = noteAddress{noteScope: noteScope{root: s.binding.root, scope: s.binding.scope}, path: relative}
	}
	target, err := notePath(address, relative)
	if err != nil {
		return noteRead{}, err
	}
	return noteRead{address: address, path: target, writable: !found}, nil
}

// adopt: note-store.ts:165. The result is "owned", "unavailable" or "adopted".
func (s *sessionNotes) adopt(ctx sdk.Context, path string, call noteCall) (string, error) {
	relative, err := noteRelative(path)
	if err != nil {
		return "", err
	}
	owns, err := s.owns(relative)
	if err != nil {
		return "", err
	}
	if owns {
		return "owned", nil
	}
	source, found, err := s.reference(ctx, nil, relative, false)
	if err != nil {
		return "", err
	}
	if !found {
		return "unavailable", nil
	}
	result, err := adoptNote(s.binding, snapshotNote{path: relative, source: source, lineage: source, anchors: []string{}})
	if err != nil {
		return "", err
	}
	file := noteTouchFile{
		noteAddress: noteAddress{noteScope: noteScope{root: s.binding.root, scope: s.binding.scope}, path: relative},
		kind:        "adopt",
	}
	if result.verified {
		seed := result.seed
		file.seed = &seed
	}
	if err := s.record(ctx, []noteTouchFile{file}, call); err != nil {
		return "", err
	}
	private, err := notePath(s.binding, relative)
	if err != nil {
		return "", err
	}
	if !result.verified {
		return "", noteErrorf(result.err, "Private copy created at "+private+"; source not edited, but copy verification failed. Read the private copy before retrying.")
	}
	if err := saveAdoption(s.binding, result.seed); err != nil {
		return "", noteErrorf(err, "Private copy created at "+private+"; source not edited, but its adoption manifest could not be saved. Read the private copy before retrying.")
	}
	return "adopted", nil
}

// prepareWrite: note-store.ts:206.
func (s *sessionNotes) prepareWrite() error { return ensureManifest(s.binding) }

// guardReference: note-store.ts:209.
func (s *sessionNotes) guardReference(ctx sdk.Context, path string, call noteCall) (string, error) {
	destination, err := notePath(s.binding, path)
	if err != nil {
		return "", err
	}
	copied, err := sdk.WithFileMutationQueue(destination, func() (string, error) { return s.adopt(ctx, path, call) })
	if err != nil {
		return "", err
	}
	if copied == "adopted" {
		return "Private copy created at " + destination + "; the source was not edited. Read the private copy, then retry the edit on that path.", nil
	}
	return "This session writes only " + s.directory() + ". Read " + destination + ", then retry there; the foreign path was not edited.", nil
}

// sessionKey: note-store.ts:216.
func sessionKey(ctx sdk.Context) (string, error) {
	header, err := ctx.SessionManager().GetHeader()
	if err != nil {
		return "", err
	}
	if header == nil {
		return "", errors.New("Pi session header is required for notes.")
	}
	id, _ := str(header, "id")
	timestamp, _ := str(header, "timestamp")
	return id + "\x00" + timestamp, nil
}

// sessionState is SessionState (note-store.ts:231): initializing until done is closed.
type sessionState struct {
	done  chan struct{}
	value *sessionNotes
	err   error
}

// notesStore is NotesStore (note-store.ts:225).
type notesStore struct {
	mu       sync.Mutex
	sessions map[string]*sessionState
	rootList []string
	rootSet  map[string]struct{}
}

// createNotesStore: note-store.ts:235.
func createNotesStore() *notesStore {
	return &notesStore{sessions: map[string]*sessionState{}, rootSet: map[string]struct{}{}}
}

func (n *notesStore) addRoot(root string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.rootSet[root]; !ok {
		n.rootSet[root] = struct{}{}
		n.rootList = append(n.rootList, root)
	}
}

// roots: note-store.ts:272.
func (n *notesStore) roots() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.rootList...)
}

// get: note-store.ts:238. Concurrent callers of a session that is still initializing share one
// initialization; a failed one is forgotten so the next call retries.
func (n *notesStore) get(ctx sdk.Context) (*sessionNotes, error) {
	key, err := sessionKey(ctx)
	if err != nil {
		return nil, err
	}
	n.mu.Lock()
	if existing, ok := n.sessions[key]; ok {
		n.mu.Unlock()
		<-existing.done
		return existing.value, existing.err
	}
	state := &sessionState{done: make(chan struct{})}
	n.sessions[key] = state
	n.mu.Unlock()
	state.value, state.err = n.initialize(ctx)
	if state.err != nil {
		n.mu.Lock()
		delete(n.sessions, key)
		n.mu.Unlock()
	}
	close(state.done)
	return state.value, state.err
}

func (n *notesStore) initialize(ctx sdk.Context) (*sessionNotes, error) {
	manager := ctx.SessionManager()
	entries, err := manager.GetEntries()
	if err != nil {
		return nil, err
	}
	header, err := manager.GetHeader()
	if err != nil {
		return nil, err
	}
	bindings := bindingsOf(entries)
	var root string
	found := false
	for i := len(bindings) - 1; i >= 0; i-- {
		if header != nil && sameOwner(bindings[i].owner, headerOwner(header)) {
			root, found = bindings[i].root, true
			break
		}
	}
	if !found {
		notes, err := notesDir(ctx.Cwd())
		if err != nil {
			return nil, err
		}
		if root, err = canonicalPath(notes); err != nil {
			return nil, err
		}
	}
	n.addRoot(root)
	for _, binding := range bindings {
		n.addRoot(binding.root)
	}
	binding, source, created, err := loadNotesBinding(ctx, root)
	if err != nil {
		return nil, err
	}
	if created {
		if err := ctx.AppendEntry(notesBindingType, binding); err != nil {
			return nil, err
		}
	}
	entries, err = manager.GetEntries()
	if err != nil {
		return nil, err
	}
	session := newSessionNotes(binding, entries, source)
	for _, scope := range session.scopes {
		n.addRoot(scope.root)
	}
	return session, nil
}

func headerOwner(header rec) noteOwner {
	id, _ := str(header, "id")
	timestamp, _ := str(header, "timestamp")
	return noteOwner{id: id, timestamp: timestamp}
}

// recovery: note-store.ts:274. nil is undefined: the session is not ready.
func (n *notesStore) recovery(ctx sdk.Context) (*notesRecovery, error) {
	key, err := sessionKey(ctx)
	if err != nil {
		return nil, err
	}
	n.mu.Lock()
	state := n.sessions[key]
	n.mu.Unlock()
	if state == nil {
		return nil, nil
	}
	select {
	case <-state.done:
	default:
		return nil, nil
	}
	if state.value == nil {
		return nil, nil
	}
	recovery, err := state.value.recovery(ctx)
	if err != nil {
		return nil, err
	}
	return &recovery, nil
}
