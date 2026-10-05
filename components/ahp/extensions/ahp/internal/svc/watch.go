package svc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
	ahptypes "github.com/microsoft/agent-host-protocol/clients/go/ahptypes"
)

// Clock schedules the debounce and grace timers; tests drive it by hand.
type Clock interface {
	AfterFunc(d time.Duration, f func()) Stopper
}

// Stopper cancels a scheduled function.
type Stopper interface{ Stop() bool }

type realClock struct{}

func (realClock) AfterFunc(d time.Duration, f func()) Stopper { return time.AfterFunc(d, f) }

// Defaults of the upstream service; the poll interval replaces chokidar's native events.
const (
	DefaultGrace        = 30 * time.Second
	DefaultDebounce     = 50 * time.Millisecond
	DefaultPollInterval = time.Second
)

// WatchOptions configures a WatchService.
type WatchOptions struct {
	Paths        *PathPolicy
	Grace        time.Duration // 0 = DefaultGrace
	Debounce     time.Duration // 0 = DefaultDebounce
	PollInterval time.Duration // 0 = DefaultPollInterval
	Log          func(string)
	Clock        Clock // nil = wall clock

	// test hooks
	afterPath  func() // after the path policy resolved a create's URI
	afterReady func() // after the first scan of a create
	onClosed   func() // each time a poller is stopped
}

// WatchService is the resource watch channel factory (port of resource-watch.ts). Upstream leans
// on chokidar's native events; the standard library has no portable file notification, so this
// port keeps a snapshot of the watched tree and diffs it every PollInterval. Everything above the
// watcher (policy, batching, grace, disposal) is the upstream design. It implements
// host.ResourceWatchHandler.
type WatchService struct {
	host   *host.Host
	opts   WatchOptions
	paths  *PathPolicy
	clock  Clock
	unhook func()

	mu       sync.Mutex
	watches  map[string]*activeWatch
	pending  sync.WaitGroup
	disposed bool
	disposal chan struct{}
}

type activeWatch struct {
	channel      string
	resourceRoot string
	watchedRoot  string
	isDir        bool
	recursive    bool
	excludes     []string
	includes     []string

	stopPoll chan struct{}
	pollDone chan struct{}

	// guarded by WatchService.mu
	changes    map[string]ahptypes.ResourceChangeType
	flushTimer Stopper
	graceTimer Stopper
	released   bool
	snapshot   map[string]watchEntry
}

// NewWatchService creates the service and hooks it to the host's subscriber counts.
func NewWatchService(h *host.Host, opts WatchOptions) *WatchService {
	s := &WatchService{host: h, opts: opts, paths: opts.Paths, clock: opts.Clock, watches: map[string]*activeWatch{}, disposal: make(chan struct{})}
	if s.paths == nil {
		s.paths = &PathPolicy{}
	}
	if s.clock == nil {
		s.clock = realClock{}
	}
	s.unhook = h.OnSubscriberCountChanged(s.onSubscriberCount)
	return s
}

func (s *WatchService) grace() time.Duration {
	if s.opts.Grace > 0 {
		return s.opts.Grace
	}
	return DefaultGrace
}

func (s *WatchService) debounce() time.Duration {
	if s.opts.Debounce > 0 {
		return s.opts.Debounce
	}
	return DefaultDebounce
}

func (s *WatchService) pollInterval() time.Duration {
	if s.opts.PollInterval > 0 {
		return s.opts.PollInterval
	}
	return DefaultPollInterval
}

func (s *WatchService) log(format string, args ...any) {
	if s.opts.Log != nil {
		s.opts.Log(fmt.Sprintf(format, args...))
	}
}

// ActiveCount is how many watches are live.
func (s *WatchService) ActiveCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.watches)
}

var errDisposed = errors.New("ResourceWatchService is disposed")

// Dispose stops accepting creations, drains the ones in flight and releases every watch. Every
// call returns the same channel, closed when shutdown completed.
func (s *WatchService) Dispose() <-chan struct{} {
	s.mu.Lock()
	if s.disposed {
		s.mu.Unlock()
		return s.disposal
	}
	s.disposed = true
	channels := make([]string, 0, len(s.watches))
	for channel := range s.watches {
		channels = append(channels, channel)
	}
	s.mu.Unlock()
	s.unhook()
	go func() {
		var wg sync.WaitGroup
		for _, channel := range channels {
			wg.Add(1)
			go func() { defer wg.Done(); s.release(channel) }()
		}
		wg.Wait()
		s.pending.Wait()
		close(s.disposal)
	}()
	return s.disposal
}

func readPatterns(raw *json.RawMessage, name string) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal(*raw, &wrapper); err != nil || wrapper == nil {
		return nil, wire.InvalidParams(name + " must contain an items array")
	}
	items, ok := wrapper["items"]
	if !ok {
		return nil, wire.InvalidParams(name + " must contain an items array")
	}
	var list []json.RawMessage
	if err := json.Unmarshal(items, &list); err != nil || list == nil {
		return nil, wire.InvalidParams(name + ".items must be an array of strings")
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		var str string
		if err := json.Unmarshal(item, &str); err != nil {
			return nil, wire.InvalidParams(name + ".items must be an array of strings")
		}
		out = append(out, str)
	}
	return out, nil
}

func watchError(err error, uri string) error {
	var we *wire.Error
	switch {
	case errors.As(err, &we):
		return err
	case errors.Is(err, fs.ErrNotExist):
		return wire.NotFound(uri)
	case errors.Is(err, fs.ErrPermission):
		return wire.Coded(wire.CodePermissionDenied, "Permission denied: "+uri)
	case errors.Is(err, syscall.EINVAL), errors.Is(err, syscall.ENOTDIR):
		return wire.InvalidParams("Cannot watch " + uri)
	}
	return wire.Coded(wire.CodeInternalError, "Cannot watch "+uri+": "+err.Error())
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// Create implements host.ResourceWatchHandler.
func (s *WatchService) Create(_ context.Context, params ahptypes.CreateResourceWatchParams) (ahptypes.CreateResourceWatchResult, error) {
	s.mu.Lock()
	if s.disposed {
		s.mu.Unlock()
		return ahptypes.CreateResourceWatchResult{}, errDisposed
	}
	s.pending.Add(1)
	s.mu.Unlock()
	defer s.pending.Done()
	return s.create(params)
}

func (s *WatchService) isDisposed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.disposed
}

func (s *WatchService) create(params ahptypes.CreateResourceWatchParams) (ahptypes.CreateResourceWatchResult, error) {
	var zero ahptypes.CreateResourceWatchResult
	recursive := params.Recursive != nil && *params.Recursive
	excludes, err := readPatterns(params.Excludes, "excludes")
	if err != nil {
		return zero, err
	}
	includes, err := readPatterns(params.Includes, "includes")
	if err != nil {
		return zero, err
	}
	resourceRoot, err := s.paths.PathFor(params.Uri)
	if err != nil {
		return zero, err
	}
	if s.opts.afterPath != nil {
		s.opts.afterPath()
	}

	info, err := os.Stat(resourceRoot)
	if err != nil {
		return zero, watchError(err, params.Uri)
	}
	isDir := info.IsDir()
	if !isDir && recursive {
		return zero, wire.InvalidParams("Cannot watch a file recursively: " + params.Uri)
	}
	watchedRoot, err := filepath.EvalSymlinks(resourceRoot)
	if err != nil {
		return zero, watchError(err, params.Uri)
	}
	if s.isDisposed() {
		return zero, errDisposed
	}

	active := &activeWatch{
		channel: wire.ResourceWatchScheme + "/" + newUUID(), resourceRoot: resourceRoot, watchedRoot: watchedRoot,
		isDir: isDir, recursive: recursive, excludes: excludes, includes: includes,
		changes: map[string]ahptypes.ResourceChangeType{}, stopPoll: make(chan struct{}), pollDone: make(chan struct{}),
	}
	active.snapshot = scanTree(active, nil)
	if s.opts.afterReady != nil {
		s.opts.afterReady()
	}
	if s.isDisposed() {
		s.stopPoller(active)
		return zero, errDisposed
	}

	watchState := ahptypes.ResourceWatchState{Root: params.Uri, Recursive: recursive}
	if len(excludes) > 0 {
		raw, _ := json.Marshal(map[string][]string{"items": excludes})
		message := json.RawMessage(raw)
		watchState.Excludes = &message
	}
	if len(includes) > 0 {
		raw, _ := json.Marshal(map[string][]string{"items": includes})
		message := json.RawMessage(raw)
		watchState.Includes = &message
	}
	if err := s.host.Store().Create(active.channel, &watchState); err != nil {
		s.stopPoller(active)
		return zero, err
	}
	s.mu.Lock()
	if s.disposed {
		s.mu.Unlock()
		s.stopPoller(active)
		s.host.DeleteChannel(active.channel)
		return zero, errDisposed
	}
	s.watches[active.channel] = active
	s.startGraceLocked(active)
	s.mu.Unlock()
	go s.poll(active)
	return ahptypes.CreateResourceWatchResult{Channel: active.channel}, nil
}

func (s *WatchService) stopPoller(active *activeWatch) {
	select {
	case <-active.stopPoll:
	default:
		close(active.stopPoll)
	}
	if s.opts.onClosed != nil {
		s.opts.onClosed()
	}
}

// ── polling ─────────────────────────────────────────────────────────────

type watchEntry struct {
	dir  bool
	sig  string
	hash string // content digest kept for the next scan while the file is "racy", see racyWindow
	cmp  string // digest taken by this scan, for comparison with the previous scan's hash only
}

// racyWindow is git's "racy timestamp" problem: the kernel stamps a file with a coarse clock (a
// few milliseconds, whole seconds on some file systems), so a rewrite of equal size right after a
// scan leaves the stat signature unchanged. A file modified within this window of the scan is
// therefore also compared by content on the next scan. Larger files are compared by stat only.
const (
	racyWindow  = 2 * time.Second
	racyMaxSize = 1 << 20
)

func contentHash(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, racyMaxSize+1)); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// scanTree lists the watched scope: the root itself, and for a directory its children (all
// descendants when recursive). Excluded paths are pruned and symlinks are entries, never followed.
func scanTree(a *activeWatch, previous map[string]watchEntry) map[string]watchEntry {
	scanned := time.Now()
	out := map[string]watchEntry{}
	info, err := os.Lstat(a.watchedRoot)
	if err != nil {
		return out
	}
	out[""] = entryOf(info, a.watchedRoot, "", previous, scanned)
	if !info.IsDir() {
		return out
	}
	var walk func(dir, rel string, depth int)
	walk = func(dir, rel string, depth int) {
		children, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, child := range children {
			childRel := child.Name()
			if rel != "" {
				childRel = rel + "/" + child.Name()
			}
			if IsExcluded(childRel, a.excludes) {
				continue
			}
			childInfo, err := child.Info()
			if err != nil {
				continue
			}
			out[childRel] = entryOf(childInfo, filepath.Join(dir, child.Name()), childRel, previous, scanned)
			if childInfo.IsDir() && a.recursive {
				walk(filepath.Join(dir, child.Name()), childRel, depth+1)
			}
		}
	}
	walk(a.watchedRoot, "", 1)
	return out
}

func entryOf(info os.FileInfo, path, rel string, previous map[string]watchEntry, scanned time.Time) watchEntry {
	if info.IsDir() {
		return watchEntry{dir: true}
	}
	entry := watchEntry{sig: etagOf(info)}
	if !info.Mode().IsRegular() || info.Size() > racyMaxSize {
		return entry
	}
	old, seen := previous[rel]
	recent := scanned.Sub(info.ModTime()) < racyWindow
	// A previously hashed file with an unchanged stat is hashed once more: that scan is the one
	// that shows whether the equal-looking stat hid a rewrite.
	if recent || (seen && old.hash != "" && old.sig == entry.sig) {
		entry.cmp = contentHash(path)
		if recent {
			entry.hash = entry.cmp
		}
	}
	return entry
}

// changed reports whether a file entry differs from its earlier scan.
func (e watchEntry) changed(old watchEntry) bool {
	if e.sig != old.sig {
		return true
	}
	return e.cmp != "" && old.hash != "" && e.cmp != old.hash
}

func (s *WatchService) poll(a *activeWatch) {
	defer close(a.pollDone)
	ticker := time.NewTicker(s.pollInterval())
	defer ticker.Stop()
	for {
		select {
		case <-a.stopPoll:
			return
		case <-ticker.C:
		}
		previous := a.snapshot
		next := scanTree(a, previous)
		a.snapshot = next
		for rel, entry := range next {
			old, existed := previous[rel]
			switch {
			case !existed:
				s.record(a, rel, ahptypes.ResourceChangeTypeAdded)
			case old.dir != entry.dir:
				s.record(a, rel, ahptypes.ResourceChangeTypeDeleted)
				s.record(a, rel, ahptypes.ResourceChangeTypeAdded)
			case !entry.dir && entry.changed(old):
				s.record(a, rel, ahptypes.ResourceChangeTypeUpdated)
			}
		}
		for rel := range previous {
			if _, still := next[rel]; !still {
				s.record(a, rel, ahptypes.ResourceChangeTypeDeleted)
			}
		}
	}
}

func (s *WatchService) record(a *activeWatch, rel string, change ahptypes.ResourceChangeType) {
	if !MatchesPatterns(rel, a.includes, a.excludes) {
		return
	}
	path := a.resourceRoot
	if rel != "" {
		path = filepath.Join(a.resourceRoot, filepath.FromSlash(rel))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.released {
		return
	}
	merged := MergeChange(a.changes[path], change)
	if merged == "" {
		delete(a.changes, path)
	} else {
		a.changes[path] = merged
	}
	if len(a.changes) == 0 {
		if a.flushTimer != nil {
			a.flushTimer.Stop()
			a.flushTimer = nil
		}
		return
	}
	if a.flushTimer != nil {
		return
	}
	a.flushTimer = s.clock.AfterFunc(s.debounce(), func() {
		s.mu.Lock()
		a.flushTimer = nil
		s.mu.Unlock()
		s.flush(a)
	})
}

func (s *WatchService) flush(a *activeWatch) {
	s.mu.Lock()
	if a.released || s.watches[a.channel] != a {
		a.changes = map[string]ahptypes.ResourceChangeType{}
		s.mu.Unlock()
		return
	}
	paths := make([]string, 0, len(a.changes))
	for path := range a.changes {
		paths = append(paths, path)
	}
	sort.Slice(paths, func(i, j int) bool { return localeLess(paths[i], paths[j]) })
	items := make([]ahptypes.ResourceChange, 0, len(paths))
	for _, path := range paths {
		items = append(items, ahptypes.ResourceChange{Uri: wire.PathToFileURI(path), Type: a.changes[path]})
	}
	a.changes = map[string]ahptypes.ResourceChangeType{}
	s.mu.Unlock()
	if len(items) == 0 {
		return
	}
	raw, _ := json.Marshal(map[string]any{"items": items})
	s.host.DispatchServerAction(a.channel, ahptypes.StateAction{Value: &ahptypes.ResourceWatchChangedAction{
		Type: ahptypes.ActionTypeResourceWatchChanged, Changes: raw,
	}})
}

// ── lifetime ────────────────────────────────────────────────────────────

func (s *WatchService) onSubscriberCount(channel string, count int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.watches[channel]
	if a == nil {
		return
	}
	if count > 0 {
		if a.graceTimer != nil {
			a.graceTimer.Stop()
			a.graceTimer = nil
		}
		return
	}
	s.startGraceLocked(a)
}

func (s *WatchService) startGraceLocked(a *activeWatch) {
	if a.graceTimer != nil {
		return
	}
	var timer Stopper
	timer = s.clock.AfterFunc(s.grace(), func() {
		s.mu.Lock()
		if a.graceTimer == timer {
			a.graceTimer = nil
		}
		s.mu.Unlock()
		if s.host.SubscriberCount(a.channel) == 0 {
			s.log("releasing unwatched %s", a.channel)
			s.release(a.channel)
		}
	})
	a.graceTimer = timer
}

func (s *WatchService) release(channel string) {
	s.mu.Lock()
	a := s.watches[channel]
	if a == nil {
		s.mu.Unlock()
		return
	}
	delete(s.watches, channel)
	a.released = true
	if a.flushTimer != nil {
		a.flushTimer.Stop()
	}
	if a.graceTimer != nil {
		a.graceTimer.Stop()
	}
	a.changes = map[string]ahptypes.ResourceChangeType{}
	s.mu.Unlock()
	s.host.DeleteChannel(channel)
	s.stopPoller(a)
	<-a.pollDone
}
