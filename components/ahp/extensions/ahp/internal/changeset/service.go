package changeset

import (
	"context"
	"encoding/base64"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	ahptypes "github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/svc"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// Defaults of the upstream service.
const (
	DefaultDebounce   = 250 * time.Millisecond
	DefaultStateGrace = 30 * time.Second
)

// Session is what the service needs to know of a session.
type Session struct {
	URI              string
	SessionID        string
	WorkingDirectory string
}

// Options configure a [Service]. Debounce and StateGrace are used as given (zero means no delay);
// callers that want upstream's defaults pass [DefaultDebounce] and [DefaultStateGrace].
type Options struct {
	GetSession       func(sessionID string) (Session, bool)
	GetSessionByChat func(chat string) (Session, bool)
	// HydrateSession loads a session that exists on disk but is not live.
	HydrateSession func(ctx context.Context, sessionID string) (Session, bool, error)
	// OnSessionAvailable and OnSessionDeletionCommitted hook the session registry; both may be nil.
	OnSessionAvailable         func(listener func(Session)) (remove func())
	OnSessionDeletionCommitted func(listener func(sessionID string) error) (remove func())
	// AuthorizeResource refuses a file: URI the remote filesystem policy does not allow; nil allows all.
	AuthorizeResource func(uri string) error
	Git               Git       // nil = the git command line
	Watch             WatchFunc // nil = polling
	PollInterval      time.Duration
	Debounce          time.Duration
	StateGrace        time.Duration
	Log               func(format string, args ...any)
}

// op is one asynchronous operation others may wait for.
type op struct{ done chan struct{} }

func newOp() *op { return &op{done: make(chan struct{})} }

func (o *op) wait() {
	if o != nil {
		<-o.done
	}
}

// entry is the service's state for one session. Every field except id is guarded by Service.mu.
type entry struct {
	id      string
	session Session

	workspace           *Workspace
	probe               *op
	probeCancel         context.CancelFunc
	refresh             *op
	refreshRequested    bool
	suspended           bool
	abort               context.Context
	abortCancel         context.CancelFunc
	watcher             Watcher
	watchStarting       *op
	watchCancel         context.CancelFunc
	watcherNeedsRestart bool
	refreshTimer        *time.Timer
}

// Service publishes the two Git-backed static changesets of each session and owns their live
// resources (port of changeset-service.ts). It implements host.ChannelHydrator.
type Service struct {
	host  *host.Host
	opts  Options
	git   Git
	watch WatchFunc

	hydrateMu sync.Mutex // makes "channel absent, then create it" one step

	mu        sync.Mutex
	entries   map[string]*entry
	evictions map[string]*time.Timer
	disposed  bool
	unhooks   []func()
	disposal  chan struct{}
	disposing sync.Once
}

// New creates the service and hooks it to the host and the session registry.
func New(h *host.Host, opts Options) *Service {
	s := &Service{
		host: h, opts: opts, git: opts.Git, watch: opts.Watch,
		entries: map[string]*entry{}, evictions: map[string]*time.Timer{}, disposal: make(chan struct{}),
	}
	if s.git == nil {
		s.git = GitChanges{}
	}
	if s.watch == nil {
		s.watch = PollWatch(opts.PollInterval)
	}
	s.unhooks = append(s.unhooks,
		h.OnSubscriberCountChanged(s.subscriberChanged),
		h.OnActionCommitted(s.actionCommitted),
		h.AddClientActionValidator(func(channel string, _ host.ClientAction, _ string) string {
			if _, ok := ParseURI(channel); ok {
				return "This host exposes read-only changesets"
			}
			return ""
		}),
	)
	if opts.OnSessionAvailable != nil {
		s.unhooks = append(s.unhooks, opts.OnSessionAvailable(func(session Session) {
			go func() {
				if err := s.Attach(context.Background(), session); err != nil {
					s.logf("cannot attach changesets for %s: %v", session.URI, err)
				}
			}()
		}))
	}
	if opts.OnSessionDeletionCommitted != nil {
		s.unhooks = append(s.unhooks, opts.OnSessionDeletionCommitted(func(sessionID string) error {
			s.RemoveSession(sessionID)
			return nil
		}))
	}
	return s
}

func (s *Service) logf(format string, args ...any) {
	if s.opts.Log != nil {
		s.opts.Log(format, args...)
	}
}

// ActiveWatcherCount is how many sessions have a running watcher.
func (s *Service) ActiveWatcherCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, e := range s.entries {
		if e.watcher != nil {
			n++
		}
	}
	return n
}

func (s *Service) isDisposed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.disposed
}

// Attach registers a session and works out whether its working directory is a git work tree.
func (s *Service) Attach(_ context.Context, session Session) error {
	if s.isDisposed() {
		return nil
	}
	e := s.entryFor(session)
	s.mu.Lock()
	known := e.workspace != nil
	s.mu.Unlock()
	if !known {
		s.probe(e).wait()
	}
	return nil
}

func (s *Service) entryFor(session Session) *entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing := s.entries[session.SessionID]; existing != nil {
		existing.session = session
		return existing
	}
	e := &entry{id: session.SessionID, session: session}
	s.entries[session.SessionID] = e
	return e
}

func (s *Service) entry(sessionID string) *entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.entries[sessionID]
}

// Hydrate implements host.ChannelHydrator: a changeset channel exists once its session advertises it.
func (s *Service) Hydrate(ctx context.Context, channel string) (bool, error) {
	parsed, ok := ParseURI(channel)
	if !ok || s.isDisposed() {
		return false, nil
	}
	session, ok := s.opts.GetSession(parsed.SessionID)
	if !ok {
		var err error
		session, ok, err = s.opts.HydrateSession(ctx, parsed.SessionID)
		if err != nil {
			return false, err
		}
	}
	if !ok {
		return false, nil
	}
	e := s.entryFor(session)
	s.probe(e).wait()
	state := s.host.Store().Session(session.URI)
	advertised := false
	if state != nil {
		for _, candidate := range state.Changesets {
			if candidate.UriTemplate == channel {
				advertised = true
			}
		}
	}
	if !advertised {
		return false, nil
	}
	s.cancelStateEviction(channel)
	s.hydrateMu.Lock()
	defer s.hydrateMu.Unlock()
	if !s.host.Store().Has(channel) {
		_ = s.host.Store().Create(channel, &ahptypes.ChangesetState{Status: ahptypes.ChangesetStatusComputing, Files: []ahptypes.ChangesetFile{}}, wire.KindChangeset)
	}
	return true, nil
}

// SuspendSession stops a session's work and waits until nothing runs for it.
func (s *Service) SuspendSession(sessionID string) {
	s.mu.Lock()
	e := s.entries[sessionID]
	if e == nil {
		s.mu.Unlock()
		return
	}
	e.suspended = true
	e.refreshRequested = false
	if e.probeCancel != nil {
		e.probeCancel()
	}
	if e.abortCancel != nil {
		e.abortCancel()
	}
	if e.watchCancel != nil {
		e.watchCancel()
	}
	if e.refreshTimer != nil {
		e.refreshTimer.Stop()
		e.refreshTimer = nil
	}
	s.mu.Unlock()
	s.closeWatcher(e)
	s.mu.Lock()
	probe, refresh := e.probe, e.refresh
	s.mu.Unlock()
	probe.wait()
	refresh.wait()
}

// ResumeSession undoes SuspendSession, after a disposal that did not happen.
func (s *Service) ResumeSession(sessionID string) {
	s.mu.Lock()
	e := s.entries[sessionID]
	if e == nil || s.disposed {
		s.mu.Unlock()
		return
	}
	e.suspended = false
	needsProbe := e.workspace == nil
	s.mu.Unlock()
	if needsProbe {
		s.probe(e)
	}
	if s.hasInterest(sessionID) {
		s.requestRefresh(e)
	}
}

// RemoveSession drops a session's changesets and everything running for them.
func (s *Service) RemoveSession(sessionID string) {
	if s.entry(sessionID) != nil {
		s.SuspendSession(sessionID)
	}
	for _, kind := range Kinds {
		s.disposeChannel(URI(sessionID, kind))
	}
	s.mu.Lock()
	delete(s.entries, sessionID)
	s.mu.Unlock()
}

// Dispose stops the service; it returns once every session is removed.
func (s *Service) Dispose() {
	s.disposing.Do(func() {
		s.mu.Lock()
		s.disposed = true
		unhooks := s.unhooks
		s.unhooks = nil
		for _, timer := range s.evictions {
			timer.Stop()
		}
		s.evictions = map[string]*time.Timer{}
		ids := make([]string, 0, len(s.entries))
		for id := range s.entries {
			ids = append(ids, id)
		}
		s.mu.Unlock()
		for _, unhook := range unhooks {
			unhook()
		}
		var wg sync.WaitGroup
		for _, id := range ids {
			wg.Add(1)
			go func() { defer wg.Done(); s.RemoveSession(id) }()
		}
		wg.Wait()
		close(s.disposal)
	})
	<-s.disposal
}

// ReadResource serves git-blob: references; nil, nil means the URI is not one of ours.
func (s *Service) ReadResource(ctx context.Context, params ahptypes.ResourceReadParams) (*ahptypes.ResourceReadResult, error) {
	if !strings.HasPrefix(params.Uri, gitBlobScheme) {
		return nil, nil
	}
	blob, ok := ParseBlobURI(params.Uri)
	if !ok {
		return nil, wire.InvalidParams("Malformed git blob URI: " + params.Uri)
	}
	session, ok := s.opts.GetSession(blob.SessionID)
	if !ok {
		var err error
		session, ok, err = s.opts.HydrateSession(ctx, blob.SessionID)
		if err != nil {
			return nil, err
		}
	}
	if !ok {
		return nil, wire.NotFound(params.Uri)
	}
	if err := s.Attach(ctx, session); err != nil {
		return nil, err
	}
	var workspace *Workspace
	if e := s.entry(blob.SessionID); e != nil {
		s.mu.Lock()
		workspace = e.workspace
		s.mu.Unlock()
	}
	if workspace == nil {
		return nil, wire.NotFound(params.Uri)
	}
	blobPath, ok := s.git.PathForBlob(*workspace, blob)
	if !ok {
		return nil, wire.NotFound(params.Uri)
	}
	if s.opts.AuthorizeResource != nil {
		if err := s.opts.AuthorizeResource(fileURL(blobPath)); err != nil {
			return nil, err
		}
	}
	buffer, err := s.git.ReadBlob(ctx, *workspace, blob)
	if err != nil {
		return nil, wire.NotFound(params.Uri)
	}
	text := utf8.Valid(buffer) && !containsNUL(buffer)
	encoding := ahptypes.ContentEncodingUtf8
	if (params.Encoding != nil && *params.Encoding == ahptypes.ContentEncodingBase64) || !text {
		encoding = ahptypes.ContentEncodingBase64
	}
	result := &ahptypes.ResourceReadResult{Encoding: encoding}
	if encoding == ahptypes.ContentEncodingUtf8 {
		result.Data = string(buffer)
	} else {
		result.Data = base64.StdEncoding.EncodeToString(buffer)
	}
	contentType, known := svc.MimeType(blob.Path)
	if !known && text {
		contentType, known = "text/plain", true
	}
	if known {
		result.ContentType = &contentType
	}
	return result, nil
}

func containsNUL(b []byte) bool {
	for _, c := range b {
		if c == 0 {
			return true
		}
	}
	return false
}

// ── host observation ─────────────────────────────────────────────────────

func (s *Service) subscriberChanged(channel string, count int) {
	parsed, ok := ParseURI(channel)
	if !ok {
		if sessionID, isSession := wire.SessionIDFromURI(channel); isSession && count > 0 {
			if e := s.entry(sessionID); e != nil {
				s.probe(e)
			}
		}
		return
	}
	e := s.entry(parsed.SessionID)
	if e == nil {
		return
	}
	if count > 0 {
		s.cancelStateEviction(channel)
		s.requestRefresh(e)
		return
	}
	s.startStateEviction(channel)
	if !s.hasInterest(e.id) {
		s.mu.Lock()
		e.refreshRequested = false
		if e.abortCancel != nil {
			e.abortCancel()
		}
		s.mu.Unlock()
		go s.closeWatcher(e)
	}
}

func (s *Service) actionCommitted(channel string, action ahptypes.StateAction) {
	switch action.Value.(type) {
	case *ahptypes.ChatTurnCompleteAction, *ahptypes.ChatTurnCancelledAction, *ahptypes.ChatErrorAction:
	default:
		return
	}
	session, ok := s.opts.GetSessionByChat(channel)
	if !ok {
		return
	}
	e := s.entry(session.SessionID)
	if e == nil {
		return
	}
	if s.hasInterest(e.id) {
		s.requestRefresh(e)
	} else {
		s.probe(e)
	}
}

func (s *Service) startStateEviction(channel string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.evictions[channel]; ok || s.disposed {
		return
	}
	s.evictions[channel] = time.AfterFunc(s.opts.StateGrace, func() {
		s.mu.Lock()
		delete(s.evictions, channel)
		s.mu.Unlock()
		if s.host.SubscriberCount(channel) == 0 {
			s.host.DeleteChannel(channel)
		}
	})
}

func (s *Service) cancelStateEviction(channel string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if timer, ok := s.evictions[channel]; ok {
		timer.Stop()
		delete(s.evictions, channel)
	}
}

func (s *Service) hasInterest(sessionID string) bool {
	return s.host.SubscriberCount(URI(sessionID, LatestCommit)) > 0 || s.host.SubscriberCount(URI(sessionID, Uncommitted)) > 0
}

// ── probe ────────────────────────────────────────────────────────────────

func (s *Service) probe(e *entry) *op {
	s.mu.Lock()
	if e.probe != nil {
		running := e.probe
		s.mu.Unlock()
		return running
	}
	ctx, cancel := context.WithCancel(context.Background())
	operation := newOp()
	e.probe, e.probeCancel = operation, cancel
	s.mu.Unlock()
	go func() {
		s.doProbe(ctx, e)
		s.mu.Lock()
		if e.probe == operation {
			e.probe, e.probeCancel = nil, nil
		}
		s.mu.Unlock()
		cancel()
		close(operation.done)
	}()
	return operation
}

// live reports whether e is still the registered entry and may publish.
func (s *Service) live(e *entry) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.disposed && !e.suspended && s.entries[e.id] == e
}

func (s *Service) doProbe(ctx context.Context, e *entry) {
	workspace, err := s.inspectWorkspace(ctx, e)
	if err != nil {
		if ctx.Err() != nil || !s.live(e) {
			return
		}
		s.mu.Lock()
		e.workspace = nil
		s.mu.Unlock()
		s.applyCatalogue(e)
		s.logf("cannot inspect Git changes for %s: %v", s.sessionURI(e), err)
		return
	}
	if !s.live(e) {
		return
	}
	s.mu.Lock()
	e.workspace = workspace
	s.mu.Unlock()
	s.applyCatalogue(e)
}

func (s *Service) sessionURI(e *entry) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return e.session.URI
}

func (s *Service) inspectWorkspace(ctx context.Context, e *entry) (*Workspace, error) {
	s.mu.Lock()
	cwd := e.session.WorkingDirectory
	s.mu.Unlock()
	if s.opts.AuthorizeResource != nil {
		if err := s.opts.AuthorizeResource(fileURL(cwd)); err != nil {
			return nil, err
		}
	}
	return s.git.Inspect(ctx, cwd)
}

func (s *Service) applyCatalogue(e *entry) {
	s.mu.Lock()
	workspace := e.workspace
	sessionURI := e.session.URI
	s.mu.Unlock()
	var next []ahptypes.Changeset
	if workspace != nil {
		next = Catalogue(e.id, workspace.Head != "")
	}
	available := map[string]bool{}
	for _, changeset := range next {
		available[changeset.UriTemplate] = true
	}
	for _, kind := range Kinds {
		if channel := URI(e.id, kind); !available[channel] {
			s.disposeChannel(channel)
		}
	}
	state := s.host.Store().Session(sessionURI)
	if state != nil && !sameCatalogue(state.Changesets, next) {
		s.host.DispatchServerAction(sessionURI, ahptypes.StateAction{Value: &ahptypes.SessionChangesetsChangedAction{
			Type: ahptypes.ActionTypeSessionChangesetsChanged, Changesets: next,
		}})
	}
}

func sameCatalogue(left, right []ahptypes.Changeset) bool {
	if len(left) == 0 && len(right) == 0 {
		return true
	}
	return reflect.DeepEqual(left, right)
}

// ── refresh ──────────────────────────────────────────────────────────────

func (s *Service) requestRefresh(e *entry) {
	interest := s.hasInterest(e.id)
	s.mu.Lock()
	if s.disposed || e.suspended || !interest {
		s.mu.Unlock()
		return
	}
	e.refreshRequested = true
	if e.refreshTimer != nil {
		e.refreshTimer.Stop()
		e.refreshTimer = nil
	}
	if e.refresh != nil {
		s.mu.Unlock()
		return
	}
	operation := newOp()
	e.refresh = operation
	s.mu.Unlock()
	go func() {
		s.refreshLoop(e)
		s.mu.Lock()
		if e.refresh == operation {
			e.refresh = nil
		}
		again := e.refreshRequested
		s.mu.Unlock()
		close(operation.done)
		// An event can arrive after the loop's final check but before this cleanup. Consume that
		// wake-up instead of leaving it stranded.
		if again {
			s.requestRefresh(e)
		}
	}()
}

func (s *Service) refreshLoop(e *entry) {
	for {
		s.mu.Lock()
		if !e.refreshRequested || e.suspended || s.disposed {
			s.mu.Unlock()
			return
		}
		e.refreshRequested = false
		s.mu.Unlock()
		s.refreshOnce(e)
	}
}

func (s *Service) scheduleRefresh(e *entry) {
	interest := s.hasInterest(e.id)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.disposed || e.suspended || !interest {
		return
	}
	e.refreshRequested = true
	if e.refresh != nil || e.refreshTimer != nil {
		return
	}
	e.refreshTimer = time.AfterFunc(s.opts.Debounce, func() {
		s.mu.Lock()
		e.refreshTimer = nil
		s.mu.Unlock()
		s.requestRefresh(e)
	})
}

func (s *Service) canPublish(e *entry, ctx context.Context) bool {
	if ctx.Err() != nil || !s.live(e) {
		return false
	}
	_, ok := s.opts.GetSession(e.id)
	return ok
}

func (s *Service) refreshOnce(e *entry) {
	s.mu.Lock()
	probe := e.probe
	s.mu.Unlock()
	probe.wait()
	s.mu.Lock()
	if e.suspended || s.disposed {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.abort, e.abortCancel = ctx, cancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if e.abort == ctx {
			e.abort, e.abortCancel = nil, nil
		}
		s.mu.Unlock()
		cancel()
	}()

	workspace, err := s.inspectWorkspace(ctx, e)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		s.publishError(e, err)
		return
	}
	if !s.canPublish(e, ctx) {
		return
	}
	s.mu.Lock()
	e.workspace = workspace
	restart := e.watcherNeedsRestart
	s.mu.Unlock()
	s.applyCatalogue(e)
	if workspace == nil {
		s.closeWatcher(e)
		return
	}
	if restart {
		s.closeWatcher(e)
	}
	// Establish observation before scanning. Any event during the scan sets refreshRequested, so
	// the loop immediately computes a second snapshot.
	s.ensureWatcher(e)
	if !s.canPublish(e, ctx) {
		return
	}

	var kinds []Kind
	for _, kind := range Kinds {
		channel := URI(e.id, kind)
		if s.host.Store().Has(channel) && s.host.SubscriberCount(channel) > 0 {
			kinds = append(kinds, kind)
		}
	}
	for _, kind := range kinds {
		channel := URI(e.id, kind)
		state := s.host.Store().Changeset(channel)
		if state == nil || state.Status != ahptypes.ChangesetStatusComputing {
			s.host.DispatchServerAction(channel, ahptypes.StateAction{Value: &ahptypes.ChangesetStatusChangedAction{
				Type: ahptypes.ActionTypeChangesetStatusChanged, Status: ahptypes.ChangesetStatusComputing,
			}})
		}
	}
	var wg sync.WaitGroup
	for _, kind := range kinds {
		wg.Add(1)
		go func() { defer wg.Done(); s.compute(ctx, e, *workspace, kind) }()
	}
	wg.Wait()
}

func (s *Service) compute(ctx context.Context, e *entry, workspace Workspace, kind Kind) {
	channel := URI(e.id, kind)
	files, err := s.git.Compute(ctx, workspace, e.id, kind)
	if err != nil {
		if ctx.Err() != nil || !s.canPublish(e, ctx) || !s.host.Store().Has(channel) {
			return
		}
		s.publishErrorTo(channel, err)
		return
	}
	if !s.canPublish(e, ctx) || !s.host.Store().Has(channel) {
		return
	}
	if files == nil {
		files = []ahptypes.ChangesetFile{}
	}
	s.host.DispatchServerAction(channel, ahptypes.StateAction{Value: &ahptypes.ChangesetContentChangedAction{
		Type: ahptypes.ActionTypeChangesetContentChanged, Files: files,
	}})
	s.host.DispatchServerAction(channel, ahptypes.StateAction{Value: &ahptypes.ChangesetStatusChangedAction{
		Type: ahptypes.ActionTypeChangesetStatusChanged, Status: ahptypes.ChangesetStatusReady,
	}})
}

func (s *Service) publishErrorTo(channel string, err error) {
	s.host.DispatchServerAction(channel, ahptypes.StateAction{Value: &ahptypes.ChangesetStatusChangedAction{
		Type: ahptypes.ActionTypeChangesetStatusChanged, Status: ahptypes.ChangesetStatusError,
		Error: &ahptypes.ErrorInfo{ErrorType: "gitChangesFailed", Message: err.Error()},
	}})
}

func (s *Service) publishError(e *entry, err error) {
	for _, kind := range Kinds {
		channel := URI(e.id, kind)
		if s.host.Store().Has(channel) && s.host.SubscriberCount(channel) > 0 {
			s.publishErrorTo(channel, err)
		}
	}
}

// ── watching ─────────────────────────────────────────────────────────────

func sameWatchTarget(left *Workspace, right Workspace) bool {
	return left != nil && left.Cwd == right.Cwd && left.GitDirectory == right.GitDirectory && left.CommonGitDirectory == right.CommonGitDirectory
}

func changesIgnoreRules(workspace Workspace, path string) bool {
	return filepath.Base(path) == ".gitignore" || path == filepath.Join(workspace.CommonGitDirectory, "info", "exclude")
}

func (s *Service) ensureWatcher(e *entry) {
	interest := s.hasInterest(e.id)
	s.mu.Lock()
	if starting := e.watchStarting; starting != nil {
		s.mu.Unlock()
		starting.wait()
		return
	}
	if s.disposed || e.suspended || e.watcher != nil || e.workspace == nil || !interest {
		s.mu.Unlock()
		return
	}
	workspace := *e.workspace
	ctx, cancel := context.WithCancel(context.Background())
	operation := newOp()
	e.watchStarting, e.watchCancel = operation, cancel
	s.mu.Unlock()

	s.startWatcher(ctx, e, workspace)

	s.mu.Lock()
	if e.watchStarting == operation {
		e.watchStarting = nil
	}
	e.watchCancel = nil
	s.mu.Unlock()
	cancel()
	close(operation.done)
}

// stillWanted is the check made before and after the watcher is built.
func (s *Service) stillWanted(ctx context.Context, e *entry, workspace Workspace) bool {
	interest := s.hasInterest(e.id)
	s.mu.Lock()
	defer s.mu.Unlock()
	return ctx.Err() == nil && !s.disposed && !e.suspended && sameWatchTarget(e.workspace, workspace) && interest
}

func (s *Service) startWatcher(ctx context.Context, e *entry, workspace Workspace) {
	// If Git cannot describe ignored trees, do not fall back to recursively watching them: large
	// dependency trees can exhaust native descriptors.
	ignored, err := s.git.IgnoredDirectories(ctx, workspace)
	if err != nil {
		if ctx.Err() == nil {
			s.logf("cannot watch changes for %s: %v", s.sessionURI(e), err)
		}
		return
	}
	if !s.stillWanted(ctx, e, workspace) {
		return
	}
	metadataTargets := []string{
		filepath.Join(workspace.GitDirectory, "HEAD"),
		filepath.Join(workspace.GitDirectory, "index"),
		filepath.Join(workspace.CommonGitDirectory, "packed-refs"),
		filepath.Join(workspace.CommonGitDirectory, "refs"),
		filepath.Join(workspace.CommonGitDirectory, "info", "exclude"),
	}
	metadataRoots := []string{workspace.GitDirectory}
	if workspace.CommonGitDirectory != workspace.GitDirectory {
		metadataRoots = append(metadataRoots, workspace.CommonGitDirectory)
	}
	isIgnored := func(path string) bool {
		for _, root := range metadataRoots {
			if isInside(root, path) {
				if path == root {
					return false
				}
				for _, target := range metadataTargets {
					if isInside(path, target) || isInside(target, path) {
						return false
					}
				}
				return true
			}
		}
		for _, root := range ignored {
			if isInside(root, path) {
				return true
			}
		}
		return false
	}
	changed := func(path string) {
		if changesIgnoreRules(workspace, path) {
			s.mu.Lock()
			e.watcherNeedsRestart = true
			s.mu.Unlock()
		}
		s.scheduleRefresh(e)
	}
	var watcher Watcher
	onError := func(err error) {
		s.mu.Lock()
		current := e.watcher == watcher && watcher != nil
		if current {
			e.watcher = nil
		}
		s.mu.Unlock()
		s.logf("changeset watch failed for %s: %v", s.sessionURI(e), err)
		if watcher != nil {
			// The failing watcher calls this from its own goroutine, which Close waits for.
			go func(w Watcher) { _ = w.Close() }(watcher)
		}
	}
	paths := append([]string{workspace.Cwd}, metadataTargets...)
	built, err := s.watch(ctx, paths, isIgnored, changed, onError)
	if err != nil {
		s.logf("cannot watch changes for %s: %v", s.sessionURI(e), err)
		return
	}
	watcher = built
	if !s.stillWanted(ctx, e, workspace) {
		_ = watcher.Close()
		return
	}
	s.mu.Lock()
	e.watcher = watcher
	e.watcherNeedsRestart = false
	s.mu.Unlock()
}

func (s *Service) closeWatcher(e *entry) {
	s.mu.Lock()
	if e.watchCancel != nil {
		e.watchCancel()
	}
	starting := e.watchStarting
	s.mu.Unlock()
	starting.wait()
	s.mu.Lock()
	watcher := e.watcher
	e.watcher = nil
	s.mu.Unlock()
	if watcher == nil {
		return
	}
	if err := watcher.Close(); err != nil {
		s.logf("closing changeset watch failed for %s: %v", s.sessionURI(e), err)
	}
}

// disposeChannel clears and removes a changeset channel.
func (s *Service) disposeChannel(channel string) {
	s.cancelStateEviction(channel)
	if !s.host.Store().Has(channel) {
		return
	}
	s.host.DispatchServerAction(channel, ahptypes.StateAction{Value: &ahptypes.ChangesetClearedAction{Type: ahptypes.ActionTypeChangesetCleared}})
	s.host.DeleteChannel(channel)
}
