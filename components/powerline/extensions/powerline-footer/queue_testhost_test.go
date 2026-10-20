package powerline_footer

import (
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"
)

// A scripted host and a hand-driven clock for the queue's tests (internal package, no SDK).

type sentMessage struct{ Text, DeliverAs string }

type queueTestHost struct {
	mu        sync.Mutex
	cwd       string
	sessionID string
	hasUI     bool
	mode      string
	idle      bool
	idleErr   error
	idleHook  func() (bool, error)
	sends     []sentMessage
	sendHook  func(text, deliverAs string) error
	notes     [][2]string
	editor    string
	repaints  int
	compactCB func(error)
	compacts  int
	th        theme
}

func (h *queueTestHost) Cwd() string                  { return h.cwd }
func (h *queueTestHost) HasUI() bool                  { return h.hasUI }
func (h *queueTestHost) Mode() string                 { return h.mode }
func (h *queueTestHost) Model() *modelInfo            { return nil }
func (h *queueTestHost) ThinkingLevel() string        { return "off" }
func (h *queueTestHost) SessionID() string            { return h.sessionID }
func (h *queueTestHost) SessionName() string          { return "" }
func (h *queueTestHost) UsingOAuth() bool             { return false }
func (h *queueTestHost) AutoCompactEnabled() bool     { return true }
func (h *queueTestHost) Session() branchProvider      { return &fakeBranch{} }
func (h *queueTestHost) ContextUsage() map[string]any { return nil }
func (h *queueTestHost) ExtensionStatuses() []statusEntry {
	return nil
}
func (h *queueTestHost) ProviderGitBranch() *string { return nil }
func (h *queueTestHost) Theme() theme               { return h.th }
func (h *queueTestHost) Notify(message, level string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.notes = append(h.notes, [2]string{level, message})
}
func (h *queueTestHost) SetStatus(string, *string)               {}
func (h *queueTestHost) ClearWidget(string)                      {}
func (h *queueTestHost) SetFooterInstalled(bool)                 {}
func (h *queueTestHost) SetWidgetInstalled(string, string, bool) {}
func (h *queueTestHost) Repaint()                                { h.repaints++ }
func (h *queueTestHost) SetEditorText(text string)               { h.editor = text }
func (h *queueTestHost) Compact(onError func(error))             { h.compacts++; h.compactCB = onError }
func (h *queueTestHost) IsIdle() (bool, error) {
	if h.idleHook != nil {
		return h.idleHook()
	}
	return h.idle, h.idleErr
}

func (h *queueTestHost) Sender() func(text, deliverAs string) error {
	return func(text, deliverAs string) error {
		h.sends = append(h.sends, sentMessage{text, deliverAs})
		if h.sendHook != nil {
			return h.sendHook(text, deliverAs)
		}
		return nil
	}
}

func (h *queueTestHost) texts() []string {
	var out []string
	for _, s := range h.sends {
		out = append(out, s.Text)
	}
	return out
}

func (h *queueTestHost) notices() []string {
	var out []string
	for _, n := range h.notes {
		out = append(out, n[0]+": "+n[1])
	}
	return out
}

// fakeQueueClock is setTimeout and setInterval under t.mock.timers: time only moves on tick.
type fakeQueueClock struct {
	now    time.Duration
	timers []*fakeQueueTimer
}

type fakeQueueTimer struct {
	due      time.Duration
	interval time.Duration
	f        func()
	stopped  bool
}

func (t *fakeQueueTimer) Stop() { t.stopped = true }

func (c *fakeQueueClock) After(d time.Duration, f func()) queueTimer {
	t := &fakeQueueTimer{due: c.now + d, f: f}
	c.timers = append(c.timers, t)
	return t
}

func (c *fakeQueueClock) Every(d time.Duration, f func()) queueTimer {
	t := &fakeQueueTimer{due: c.now + d, interval: d, f: f}
	c.timers = append(c.timers, t)
	return t
}

// active counts the timers that are neither stopped nor spent.
func (c *fakeQueueClock) active() int {
	n := 0
	for _, t := range c.timers {
		if !t.stopped {
			n++
		}
	}
	return n
}

// tick moves the clock forward and runs every timer that falls due, in order, including timers they arm.
func (c *fakeQueueClock) tick(d time.Duration) {
	target := c.now + d
	for {
		var next *fakeQueueTimer
		for _, t := range c.timers {
			if !t.stopped && t.due <= target && (next == nil || t.due < next.due) {
				next = t
			}
		}
		if next == nil {
			break
		}
		c.now = next.due
		if next.interval > 0 {
			next.due += next.interval
		} else {
			next.stopped = true
		}
		next.f()
	}
	c.now = target
	live := c.timers[:0]
	for _, t := range c.timers {
		if !t.stopped {
			live = append(live, t)
		}
	}
	c.timers = live
}

// queueRig is the extension core on the scripted host, with its own agent directory and a hand-driven clock.
type queueRig struct {
	t     *testing.T
	root  string
	host  *queueTestHost
	p     *powerline
	q     *queueState
	clock *fakeQueueClock
	store *queueStore
}

func newQueueRig(t *testing.T) *queueRig {
	t.Helper()
	root := t.TempDir()
	return newQueueRigAt(t, root, root, "readiness", `{"powerline":{"welcome":false}}`, plainTheme{})
}

// newQueueRigAt starts the core in agentDir (Pi's agent directory: the settings and the queue store) with a working directory.
func newQueueRigAt(t *testing.T, agentDir, cwd, sessionID, settings string, th theme) *queueRig {
	t.Helper()
	root := agentDir
	t.Setenv("PI_CODING_AGENT_DIR", root)
	t.Setenv("HOME", root)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	host := &queueTestHost{cwd: cwd, sessionID: sessionID, mode: "tui", hasUI: true, idle: true, th: th}
	p := newPowerline(host)
	clk := &fakeQueueClock{}
	p.queue.clock = clk
	p.queue.spawn = func(f func()) { f() } // the unwaited host call runs inline, after the lock was released
	return &queueRig{t: t, root: root, host: host, p: p, q: p.queue, clock: clk, store: p.queue.store}
}

// emit delivers an event to the queue the way the extension's handlers do.
func (r *queueRig) emit(name string, data map[string]any) {
	r.t.Helper()
	if err := r.q.emit(name, data); err != nil {
		r.t.Fatalf("%s: %v", name, err)
	}
}

func (r *queueRig) item(text string, intent queueIntent, target queueTarget) queueItem {
	r.t.Helper()
	item, err := r.store.add(createQueueItemInput{Text: text, Intent: intent, Target: target, Source: queueSource{Cwd: r.root, SessionID: r.host.sessionID}})
	if err != nil {
		r.t.Fatal(err)
	}
	return item
}

func (r *queueRig) status(id string) queueStatus {
	r.t.Helper()
	item, err := r.store.get(id)
	if err != nil || item == nil {
		r.t.Fatalf("item %s: %v %v", id, item, err)
	}
	return item.Status
}

func (r *queueRig) statuses() map[string]queueStatus {
	items, _ := r.store.list()
	out := map[string]queueStatus{}
	for _, it := range items {
		out[it.ID] = it.Status
	}
	return out
}

func sortedIDs(m map[string]queueStatus) []string {
	var ids []string
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
