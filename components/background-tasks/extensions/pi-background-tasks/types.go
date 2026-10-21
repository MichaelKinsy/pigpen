package pi_background_tasks

import (
	"sort"
	"sync"
	"time"
)

// Data types of the task runtime. upstream: extensions/types.ts, constants.ts.

const (
	bgMessageType = "kendex-background-tasks:event"
	bgStateType   = "kendex-background-tasks:state"
	bgWidgetKey   = "kendex-background-tasks"

	defaultTimeoutMs            = 0
	defaultOutputSettleMs       = 2000
	defaultForceKillGraceMs     = 5000
	defaultOutputBufferMaxChars = 1_000_000
	maxFinishedTasks            = 50
	defaultOutputAlertMaxChars  = 2000
	defaultLogTailMaxChars      = 10_000
	defaultForcedBackgroundMs   = 5 * 60 * 1000
	defaultWidgetFinishedRetain = 15_000
	defaultWakeBudgetMaxWakes   = 10
	defaultWakeBudgetMaxBytes   = 20_000
	wakeManifestFieldMaxChars   = 192
	wakeContentCommandMaxChars  = 160
	taskDisplayNameMaxChars     = 96
	maxWakeEvents               = 50
	outputUIRefreshMs           = 200
	outputPersistMs             = 1000
	bgToolResultMaxDetailsBytes = 64 * 1024
	bgToolResultMaxTasks        = 50
	bgToolResultSampleLimit     = 20
	bgTasksSnapshotMaxBytes     = 64 * 1024
	probeConcurrency            = 4
	probeTimeout                = time.Second
	defaultOrphanPoll           = 30 * time.Second
	iconCheck                   = "\uf00c"
	iconTimes                   = "\uf00d"
	notifyAlways                = "always"
	notifyTransition            = "transition"
	notifyFirstMatchOnly        = "first-match-only"
	statusRunning               = "running"
	statusCompleted             = "completed"
	statusFailed                = "failed"
	statusStopped               = "stopped"
	statusTimedOut              = "timed_out"
	stopUser                    = "user"
	stopTimeout                 = "timeout"
	stopShutdown                = "shutdown"
	termSelfExit                = "self-exit"
	termExtensionStop           = "extension-stop"
	termSessionShutdown         = "session-shutdown"
	termTimeout                 = "timeout"
	termExternal                = "external"
	termReconcileOnRestart      = "reconcile-on-restart"
	termOrphanedPidGone         = "orphaned-pid-gone"
	termOrphanedPidReused       = "orphaned-pid-reused"
	eventOutput                 = "output"
	eventExit                   = "exit"
	originBgTask                = "bg_task"
	originAutoBackground        = "auto-background"
)

// processIdentity detects pid reuse on restore or poll: the process start token and the executable name.
type processIdentity struct {
	Pid        int    `json:"pid"`
	StartToken string `json:"startToken"`
	Comm       string `json:"comm"`
}

type resourceControlMeta struct {
	Mode          string `json:"mode"`
	RequestedMode string `json:"requestedMode"`
	UnitName      string `json:"unitName,omitempty"`
	Warning       string `json:"warning,omitempty"`
}

// outputWakeBudget is the cumulative per-task accounting for the output-wake budget guard.
type outputWakeBudget struct {
	Wakes       int    `json:"wakes"`
	Bytes       int    `json:"bytes"`
	Exhausted   bool   `json:"exhausted"`
	AnnouncedAt *int64 `json:"announcedAt"`
}

// wakeEvent is one wake record, delivered or dropped.
type wakeEvent struct {
	DeliveredAt      *int64 `json:"deliveredAt"`
	DroppedReason    string `json:"droppedReason,omitempty"`
	EventAt          int64  `json:"eventAt"`
	EventType        string `json:"eventType"`
	Sequence         int    `json:"sequence"`
	TaskStatusAtEmit string `json:"taskStatusAtEmit"`
}

type wakePending struct {
	EventAt   int64  `json:"eventAt"`
	EventType string `json:"eventType"`
	Sequence  int    `json:"sequence"`
}

// snapshot is BackgroundTaskSnapshot: the serializable state of a task. Field order is taskSnapshot()'s literal order,
// which is the key order of the original's JSON.
type snapshot struct {
	Command              string               `json:"command"`
	Cwd                  string               `json:"cwd"`
	ExitCode             *int                 `json:"exitCode"`
	ExitNotified         bool                 `json:"exitNotified"`
	ExpiresAt            *float64             `json:"expiresAt"`
	ID                   string               `json:"id"`
	LastOutputAt         *int64               `json:"lastOutputAt"`
	LogFile              string               `json:"logFile"`
	NotifyOnExit         bool                 `json:"notifyOnExit"`
	NotifyOnOutput       bool                 `json:"notifyOnOutput"`
	NotifyPattern        string               `json:"notifyPattern,omitempty"`
	NotifyMode           string               `json:"notifyMode,omitempty"`
	DedupeKey            string               `json:"dedupeKey,omitempty"`
	OutputBytes          int64                `json:"outputBytes"`
	WakeSequence         int                  `json:"wakeSequence"`
	WakeEvents           []wakeEvent          `json:"wakeEvents"`
	VoidedWakeSequences  []int                `json:"voidedWakeSequences"`
	PendingWakes         []wakePending        `json:"pendingWakes"`
	LastOutputDedupeHash string               `json:"lastOutputDedupeHash,omitempty"`
	LastOutputDedupeBy   map[string]string    `json:"lastOutputDedupeByKey"`
	OutputPatternMatched bool                 `json:"outputPatternMatched"`
	OutputWakeBudget     *outputWakeBudget    `json:"outputWakeBudget,omitempty"`
	Pid                  int                  `json:"pid"`
	ProcIdent            *processIdentity     `json:"procIdent,omitempty"`
	ResourceControl      *resourceControlMeta `json:"resourceControl,omitempty"`
	SessionID            string               `json:"sessionId,omitempty"`
	StartedAt            int64                `json:"startedAt"`
	Status               string               `json:"status"`
	TerminationReason    string               `json:"terminationReason,omitempty"`
	Title                string               `json:"title"`
	UpdatedAt            int64                `json:"updatedAt"`
}

// compactSnapshot is compactBackgroundTaskSnapshot's result: the transcript-safe view, in the original's key order.
type compactSnapshot struct {
	Command           string               `json:"command"`
	Cwd               string               `json:"cwd"`
	DedupeKey         string               `json:"dedupeKey,omitempty"`
	ExitCode          *int                 `json:"exitCode"`
	ExitNotified      bool                 `json:"exitNotified"`
	ExpiresAt         *float64             `json:"expiresAt"`
	ID                string               `json:"id"`
	LastOutputAt      *int64               `json:"lastOutputAt"`
	LogFile           string               `json:"logFile"`
	NotifyMode        string               `json:"notifyMode,omitempty"`
	NotifyOnExit      bool                 `json:"notifyOnExit"`
	NotifyOnOutput    bool                 `json:"notifyOnOutput"`
	NotifyPattern     string               `json:"notifyPattern,omitempty"`
	OutputBytes       int64                `json:"outputBytes"`
	Pid               int                  `json:"pid"`
	ResourceControl   *resourceControlMeta `json:"resourceControl,omitempty"`
	SessionID         string               `json:"sessionId,omitempty"`
	StartedAt         int64                `json:"startedAt"`
	Status            string               `json:"status"`
	TerminationReason string               `json:"terminationReason,omitempty"`
	Title             string               `json:"title"`
	UpdatedAt         int64                `json:"updatedAt"`
}

// task is ManagedTask: the snapshot fields plus the live process and timers. Every field is guarded by the runtime's lock.
type task struct {
	snapshot
	child             proc
	closed            bool
	forceKillTimer    timer
	lastAnnouncedLen  int
	matcher           matcherFunc
	output            string
	outputTimer       timer
	voidedWakes       map[int]bool
	stopReason        string // "", stopUser, stopTimeout, stopShutdown
	timeoutTimer      timer
	restored          bool
	outputReadVersion int
	// outputUnits is the length of output in UTF-16 units (the original's output.length), kept so a chunk does not re-count
	// the whole buffer. outBuf backs output: it only ever grows by append, so the strings that view it stay valid.
	outputUnits int
	outBuf      []byte
	// gate is the producer's hold of the log writer: the task's pipes are not read until it resolves.
	gate *future
}

// sortedVoided is the voided wake sequences in ascending order.
func sortedVoided(set map[int]bool) []int {
	out := make([]int, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Ints(out)
	return out
}

// proc is a spawned task process.
type proc interface {
	Pid() int
}

// clockFunc is Date.now in milliseconds.
type clockFunc func() int64

func realNow() int64 { return time.Now().UnixMilli() }

// clockNow is the clock of the resource planning and the lifecycle transitions; a test pins it.
var clockNow clockFunc = realNow

// timer is a pending setTimeout or setInterval.
type timer interface{ Stop() }

// timers is the injectable timer source; tests fire timers by hand.
type timers interface {
	// After runs f once, after d. The callback runs on its own goroutine in production.
	After(d time.Duration, f func()) timer
	// Every runs f each d until stopped.
	Every(d time.Duration, f func()) timer
}

type realTimers struct{}

type stdTimer struct{ t *time.Timer }

func (s stdTimer) Stop() { s.t.Stop() }

type stdTicker struct {
	t    *time.Ticker
	stop chan struct{}
	once sync.Once
}

func (s *stdTicker) Stop() {
	s.once.Do(func() {
		s.t.Stop()
		close(s.stop)
	})
}

func (realTimers) After(d time.Duration, f func()) timer { return stdTimer{time.AfterFunc(d, f)} }

func (realTimers) Every(d time.Duration, f func()) timer {
	tk := &stdTicker{t: time.NewTicker(d), stop: make(chan struct{})}
	go func() {
		for {
			select {
			case <-tk.t.C:
				f()
			case <-tk.stop:
				return
			}
		}
	}()
	return tk
}

// future is a Promise that never rejects: resolved once, with callbacks run in registration order. then runs the callback
// at once when the future is already resolved.
type future struct {
	mu   sync.Mutex
	done bool
	cbs  []func()
	ch   chan struct{}
}

func newFuture() *future { return &future{ch: make(chan struct{})} }

func resolvedFuture() *future {
	f := newFuture()
	f.resolve()
	return f
}

func (f *future) resolve() {
	f.mu.Lock()
	if f.done {
		f.mu.Unlock()
		return
	}
	f.done = true
	cbs := f.cbs
	f.cbs = nil
	close(f.ch)
	f.mu.Unlock()
	for _, cb := range cbs {
		cb()
	}
}

func (f *future) then(cb func()) {
	f.mu.Lock()
	if f.done {
		f.mu.Unlock()
		cb()
		return
	}
	f.cbs = append(f.cbs, cb)
	f.mu.Unlock()
}

// wait blocks until the future resolves.
func (f *future) wait() { <-f.ch }

func (f *future) isDone() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.done
}
