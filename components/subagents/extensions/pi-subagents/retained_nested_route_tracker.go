package pi_subagents

import (
	"io"
	"sync"
	"time"
)

// retained-nested-route-tracker.ts: drops a retained foreground nested route once none of its descendants is live. A refresh runs
// 25 ms after a notification (debounced) and every poll interval; the original's native fs.watch is an injectable seam here
// (no file-watching dependency is approved for this extension), so by default the poll alone covers it.

const (
	defaultRetainedPollInterval = 5 * time.Second
	retainedRefreshDebounce     = 25 * time.Millisecond
)

type retainedNestedRouteTrackerOptions struct {
	PollInterval time.Duration // 0: 5 s
	// Watch reports changes under dir through notify; nil disables native watching. The returned closer stops it. Its failure
	// is ignored (the poll covers it); err() events are delivered through onError.
	Watch func(dir string, notify func(), onError func()) (io.Closer, error)
}

type retainedNestedRouteTracker struct {
	state *SubagentState
	opts  retainedNestedRouteTrackerOptions

	mu       sync.Mutex
	watchers map[string]io.Closer
	timers   map[string]*time.Timer
	poller   *time.Ticker
	stopPoll chan struct{}
}

func newRetainedNestedRouteTracker(state *SubagentState, opts retainedNestedRouteTrackerOptions) *retainedNestedRouteTracker {
	if opts.PollInterval == 0 {
		opts.PollInterval = defaultRetainedPollInterval
	}
	return &retainedNestedRouteTracker{state: state, opts: opts, watchers: map[string]io.Closer{}, timers: map[string]*time.Timer{}}
}

func (t *retainedNestedRouteTracker) retained(rootRunID string) *nestedRoute {
	t.state.Mu.Lock()
	defer t.state.Mu.Unlock()
	return t.state.RetainedForegroundNestedRoutes[rootRunID]
}

func (t *retainedNestedRouteTracker) closeRoot(rootRunID string) {
	t.mu.Lock()
	w := t.watchers[rootRunID]
	delete(t.watchers, rootRunID)
	if tm := t.timers[rootRunID]; tm != nil {
		tm.Stop()
	}
	delete(t.timers, rootRunID)
	t.mu.Unlock()
	if w != nil {
		_ = w.Close()
	}
}

func (t *retainedNestedRouteTracker) refresh(rootRunID string) {
	r := t.retained(rootRunID)
	if r == nil {
		t.closeRoot(rootRunID)
		return
	}
	reg, err := projectNestedEvents(*r)
	if err != nil {
		nestedRouteLog("Failed to refresh retained nested descendants for foreground run '"+rootRunID+"':", err)
		return
	}
	if hasLiveNestedDescendants(reg.Children) {
		return
	}
	t.state.Mu.Lock()
	delete(t.state.RetainedForegroundNestedRoutes, rootRunID)
	t.state.Mu.Unlock()
	t.closeRoot(rootRunID)
}

func (t *retainedNestedRouteTracker) scheduleRefresh(rootRunID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timers[rootRunID] != nil {
		return
	}
	t.timers[rootRunID] = time.AfterFunc(retainedRefreshDebounce, func() {
		t.mu.Lock()
		delete(t.timers, rootRunID)
		t.mu.Unlock()
		t.refresh(rootRunID)
	})
}

func (t *retainedNestedRouteTracker) retainedIDs() []string {
	t.state.Mu.Lock()
	defer t.state.Mu.Unlock()
	ids := make([]string, 0, len(t.state.RetainedForegroundNestedRoutes))
	for id := range t.state.RetainedForegroundNestedRoutes {
		ids = append(ids, id)
	}
	return ids
}

func (t *retainedNestedRouteTracker) ensurePoller() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.poller != nil || len(t.retainedIDs()) == 0 {
		return
	}
	t.poller = time.NewTicker(t.opts.PollInterval)
	stop := make(chan struct{})
	t.stopPoll = stop
	go func(tick *time.Ticker) {
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				for _, id := range t.retainedIDs() {
					t.refresh(id)
				}
				if len(t.retainedIDs()) > 0 {
					continue
				}
				t.mu.Lock()
				if t.stopPoll == stop {
					t.poller.Stop()
					t.poller, t.stopPoll = nil, nil
				}
				t.mu.Unlock()
				return
			}
		}
	}(t.poller)
}

// Track starts watching a retained route: a refresh soon (the handoff race: the route stays until it ran), then on changes and by poll.
func (t *retainedNestedRouteTracker) Track(rootRunID string) {
	r := t.retained(rootRunID)
	if r == nil {
		return
	}
	t.mu.Lock()
	_, has := t.watchers[rootRunID]
	t.mu.Unlock()
	if t.opts.Watch != nil && !has {
		w, err := t.opts.Watch(r.EventSink, func() { t.scheduleRefresh(rootRunID) }, func() { t.closeRoot(rootRunID) })
		if err == nil && w != nil {
			t.mu.Lock()
			t.watchers[rootRunID] = w
			t.mu.Unlock()
		}
	}
	t.scheduleRefresh(rootRunID)
	t.ensurePoller()
}

// Clear stops everything and forgets every retained route (session teardown).
func (t *retainedNestedRouteTracker) Clear() {
	t.mu.Lock()
	if t.stopPoll != nil {
		close(t.stopPoll)
		t.poller.Stop()
		t.poller, t.stopPoll = nil, nil
	}
	ids := make([]string, 0, len(t.watchers))
	for id := range t.watchers {
		ids = append(ids, id)
	}
	for _, tm := range t.timers {
		tm.Stop()
	}
	t.timers = map[string]*time.Timer{}
	t.mu.Unlock()
	for _, id := range ids {
		t.closeRoot(id)
	}
	t.state.Mu.Lock()
	for id := range t.state.RetainedForegroundNestedRoutes {
		delete(t.state.RetainedForegroundNestedRoutes, id)
	}
	t.state.Mu.Unlock()
}
