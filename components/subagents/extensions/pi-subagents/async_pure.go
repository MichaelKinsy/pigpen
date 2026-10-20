package pi_subagents

// The pure background modules: completion batching and de-duplication, the child cache-retention environment, the
// background process options, the wait tool's configuration, the top-level async override and the run id prefix rule.
// Ports completion-batcher.ts, completion-dedupe.ts, child-cache-retention.ts, background-process-options.ts, wait-config.ts,
// top-level-async.ts, run-id-query.ts and pid-namespace.ts.

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ── completion-batcher.ts ──

// CompletionBatchConfig is the resolved batching configuration.
type CompletionBatchConfig struct {
	Enabled             bool
	DebounceMs          int
	MaxWaitMs           int
	StragglerDebounceMs int
	StragglerMaxWaitMs  int
	StragglerWindowMs   int
}

// defaultCompletionBatchConfig is DEFAULT_COMPLETION_BATCH_CONFIG.
var defaultCompletionBatchConfig = CompletionBatchConfig{Enabled: true, DebounceMs: 150, MaxWaitMs: 1000, StragglerDebounceMs: 75, StragglerMaxWaitMs: 400, StragglerWindowMs: 2000}

// parsePositiveInt accepts a finite integer of at least 1 (a JSON number); anything else is ignored.
func parsePositiveInt(v any) (int, bool) {
	f, ok := v.(float64)
	if !ok {
		if i, isInt := v.(int); isInt {
			f, ok = float64(i), true
		}
	}
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) || f < 1 {
		return 0, false
	}
	return int(f), true
}

// resolveCompletionBatchConfig is resolveCompletionBatchConfig: the override wins over the global config, an invalid value
// is ignored (it never masks a valid one below it).
func resolveCompletionBatchConfig(global, override map[string]any) CompletionBatchConfig {
	pick := func(key string, def int) int {
		if n, ok := parsePositiveInt(override[key]); ok {
			return n
		}
		if n, ok := parsePositiveInt(global[key]); ok {
			return n
		}
		return def
	}
	d := defaultCompletionBatchConfig
	enabled := d.Enabled
	if b, ok := override["enabled"].(bool); ok {
		enabled = b
	} else if b, ok := global["enabled"].(bool); ok {
		enabled = b
	}
	return CompletionBatchConfig{
		Enabled:             enabled,
		DebounceMs:          pick("debounceMs", d.DebounceMs),
		MaxWaitMs:           pick("maxWaitMs", d.MaxWaitMs),
		StragglerDebounceMs: pick("stragglerDebounceMs", d.StragglerDebounceMs),
		StragglerMaxWaitMs:  pick("stragglerMaxWaitMs", d.StragglerMaxWaitMs),
		StragglerWindowMs:   pick("stragglerWindowMs", d.StragglerWindowMs),
	}
}

// batchTimers is the timer seam of the batcher (the original's TimerApi).
type batchTimers interface {
	SetTimeout(handler func(), delayMs int) any
	ClearTimeout(handle any)
}

type realBatchTimers struct{}

func (realBatchTimers) SetTimeout(handler func(), delayMs int) any {
	return time.AfterFunc(time.Duration(delayMs)*time.Millisecond, handler)
}

func (realBatchTimers) ClearTimeout(handle any) {
	if t, ok := handle.(*time.Timer); ok {
		t.Stop()
	}
}

// CompletionBatcher holds successful completions briefly so siblings that finish together arrive as one group.
type CompletionBatcher[T any] struct {
	mu         sync.Mutex
	config     CompletionBatchConfig
	emit       func(items []T)
	timers     batchTimers
	now        func() int64
	pending    []T
	debounce   any
	maxWait    any
	straggler  bool
	lastEmitAt *int64
	emitting   bool
}

// newCompletionBatcher is createCompletionBatcher. timers and now may be nil (real ones).
func newCompletionBatcher[T any](config CompletionBatchConfig, emit func([]T), timers batchTimers, now func() int64) *CompletionBatcher[T] {
	if timers == nil {
		timers = realBatchTimers{}
	}
	if now == nil {
		now = nowMs
	}
	return &CompletionBatcher[T]{config: config, emit: emit, timers: timers, now: now}
}

func (b *CompletionBatcher[T]) clearTimersLocked() {
	if b.debounce != nil {
		b.timers.ClearTimeout(b.debounce)
		b.debounce = nil
	}
	if b.maxWait != nil {
		b.timers.ClearTimeout(b.maxWait)
		b.maxWait = nil
	}
}

func (b *CompletionBatcher[T]) emitGroup() {
	b.mu.Lock()
	b.clearTimersLocked()
	if len(b.pending) == 0 {
		b.mu.Unlock()
		return
	}
	items := b.pending
	b.pending = nil
	t := b.now()
	b.lastEmitAt = &t
	b.mu.Unlock()
	b.emit(items)
}

// Push adds a batchable item; it emits at once when batching is disabled.
func (b *CompletionBatcher[T]) Push(item T) {
	if !b.config.Enabled {
		b.emit([]T{item})
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.pending) == 0 {
		b.straggler = b.lastEmitAt != nil && b.now()-*b.lastEmitAt < int64(b.config.StragglerWindowMs)
	}
	b.pending = append(b.pending, item)
	if b.debounce != nil {
		b.timers.ClearTimeout(b.debounce)
	}
	delay := b.config.DebounceMs
	if b.straggler {
		delay = b.config.StragglerDebounceMs
	}
	b.debounce = b.timers.SetTimeout(b.emitGroup, delay)
	if b.maxWait == nil {
		wait := b.config.MaxWaitMs
		if b.straggler {
			wait = b.config.StragglerMaxWaitMs
		}
		b.maxWait = b.timers.SetTimeout(b.emitGroup, wait)
	}
}

// Flush emits any held items immediately as one group.
func (b *CompletionBatcher[T]) Flush() {
	if !b.config.Enabled {
		return
	}
	b.emitGroup()
}

// Dispose clears the timers and returns the items that were never emitted.
func (b *CompletionBatcher[T]) Dispose() []T {
	if !b.config.Enabled {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.clearTimersLocked()
	abandoned := b.pending
	b.pending = nil
	return abandoned
}

// ── completion-dedupe.ts ──

func nonEmptyTrimmed(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return jsTrim(s)
}

func finiteNumber(v any) (float64, bool) {
	f, ok := v.(float64)
	if !ok {
		if i, isInt := v.(int64); isInt {
			return float64(i), true
		}
		if i, isInt := v.(int); isInt {
			return float64(i), true
		}
		return 0, false
	}
	return f, !math.IsNaN(f) && !math.IsInf(f, 0)
}

// buildCompletionKey is buildCompletionKey: one key per session, run and state; without an id, a key from the metadata.
func buildCompletionKey(data map[string]any, fallback string) string {
	sessionID := nonEmptyTrimmed(data["sessionId"])
	if sessionID == "" {
		sessionID = "no-session"
	}
	id := nonEmptyTrimmed(data["id"])
	state := nonEmptyTrimmed(data["state"])
	if id != "" {
		if state != "" {
			return fmt.Sprintf("session:%s:id:%s:state:%s", sessionID, id, state)
		}
		return fmt.Sprintf("session:%s:id:%s", sessionID, id)
	}
	agent := nonEmptyTrimmed(data["agent"])
	if agent == "" {
		agent = "unknown"
	}
	ts, taskIdx, total := "no-ts", "-", "-"
	if f, ok := finiteNumber(data["timestamp"]); ok {
		ts = jsNumber(f)
	}
	if f, ok := finiteNumber(data["taskIndex"]); ok {
		taskIdx = jsNumber(f)
	}
	if f, ok := finiteNumber(data["totalTasks"]); ok {
		total = jsNumber(f)
	}
	success := "?"
	if b, ok := data["success"].(bool); ok {
		success = "0"
		if b {
			success = "1"
		}
	}
	return strings.Join([]string{"meta", sessionID, agent, ts, taskIdx, total, success, fallback}, ":")
}

// markSeenWithTTL records key at now and reports whether it was already seen within ttlMs; older entries are dropped.
func markSeenWithTTL(seen map[string]int64, key string, now, ttlMs int64) bool {
	for k, ts := range seen {
		if now-ts > ttlMs {
			delete(seen, k)
		}
	}
	if _, ok := seen[key]; ok {
		return true
	}
	seen[key] = now
	return false
}

// ── child-cache-retention.ts ──

// childCacheRetention is the tier PI_SUBAGENT_CACHE_RETENTION sets for children ("" when unset: children inherit the parent's).
func childCacheRetention(env map[string]string) string { return env["PI_SUBAGENT_CACHE_RETENTION"] }

// childCacheRetentionEnv is the launch-environment form: PI_CACHE_RETENTION for a child, nothing when unset.
func childCacheRetentionEnv(env map[string]string) map[string]string {
	if r := childCacheRetention(env); r != "" {
		return map[string]string{"PI_CACHE_RETENTION": r}
	}
	return map[string]string{}
}

func processEnvMap() map[string]string {
	m := map[string]string{}
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return m
}

// ── background-process-options.ts ──

// BackgroundProcessOptions says how a background runner is started: detached from the parent's session on Unix (it
// survives the parent), never in a new console window on Windows.
type BackgroundProcessOptions struct {
	Detached    bool
	WindowsHide bool
}

func backgroundProcessOptions(goos string) BackgroundProcessOptions {
	return BackgroundProcessOptions{Detached: goos != "windows", WindowsHide: true}
}

// ── wait-config.ts ──

const (
	waitToolEnabledEnv          = "PI_SUBAGENT_WAIT_TOOL_ENABLED"
	waitToolDefaultTimeoutMsEnv = "PI_SUBAGENT_WAIT_TOOL_DEFAULT_TIMEOUT_MS"
)

// ResolvedWaitToolConfig is the bg_wait tool's configuration.
type ResolvedWaitToolConfig struct {
	Enabled          bool
	DefaultTimeoutMs *int
}

func waitEnvBool(env map[string]string) (bool, bool, error) {
	v, ok := env[waitToolEnabledEnv]
	if !ok {
		return false, false, nil
	}
	switch strings.ToLower(jsTrim(v)) {
	case "1", "true", "yes", "on", "enabled":
		return true, true, nil
	case "0", "false", "no", "off", "disabled":
		return false, true, nil
	}
	return false, false, fmt.Errorf("%s must be one of true/false, 1/0, yes/no, on/off, or enabled/disabled.", waitToolEnabledEnv)
}

func waitEnvTimeout(env map[string]string) (*int, error) {
	v, ok := env[waitToolDefaultTimeoutMsEnv]
	if !ok {
		return nil, nil
	}
	f, err := strconv.ParseFloat(jsTrim(v), 64)
	if jsTrim(v) == "" {
		f, err = 0, nil // Number("") is 0, which is not a positive integer
	}
	if err != nil || f != math.Trunc(f) || f < 1 || math.IsInf(f, 0) {
		return nil, fmt.Errorf("%s must be a positive integer.", waitToolDefaultTimeoutMsEnv)
	}
	n := int(f)
	return &n, nil
}

// resolveWaitToolConfig is resolveWaitToolConfig: config is a bool, an object or absent (nil); the environment wins.
func resolveWaitToolConfig(config any, env map[string]string) (ResolvedWaitToolConfig, error) {
	var cfgEnabled *bool
	var cfgTimeout *int
	switch c := config.(type) {
	case nil:
	case bool:
		cfgEnabled = &c
	case map[string]any:
		if e, has := c["enabled"]; has && e != nil {
			b, ok := e.(bool)
			if !ok {
				return ResolvedWaitToolConfig{}, fmt.Errorf("config.waitTool.enabled must be a boolean.")
			}
			cfgEnabled = &b
		}
		if t, has := c["defaultTimeoutMs"]; has && t != nil {
			n, ok := parsePositiveInt(t)
			if !ok {
				return ResolvedWaitToolConfig{}, fmt.Errorf("config.waitTool.defaultTimeoutMs must be a positive integer.")
			}
			cfgTimeout = &n
		}
	default:
		return ResolvedWaitToolConfig{}, fmt.Errorf("config.waitTool must be a boolean or an object with optional enabled and defaultTimeoutMs values.")
	}
	envTimeout, err := waitEnvTimeout(env)
	if err != nil {
		return ResolvedWaitToolConfig{}, err
	}
	envEnabled, hasEnv, err := waitEnvBool(env)
	if err != nil {
		return ResolvedWaitToolConfig{}, err
	}
	out := ResolvedWaitToolConfig{Enabled: true}
	switch {
	case hasEnv:
		out.Enabled = envEnabled
	case cfgEnabled != nil:
		out.Enabled = *cfgEnabled
	}
	if envTimeout != nil {
		out.DefaultTimeoutMs = envTimeout
	} else if cfgTimeout != nil {
		out.DefaultTimeoutMs = cfgTimeout
	}
	return out, nil
}

// ── top-level-async.ts ──

// applyForceTopLevelAsyncOverride returns params with async forced on and clarify off at depth 0 when the config forces
// top-level async (unless the call is foreground-only).
func applyForceTopLevelAsyncOverride(params map[string]any, depth int, forceTopLevelAsync bool) map[string]any {
	if b, _ := params["foregroundOnly"].(bool); b || !(depth == 0 && forceTopLevelAsync) {
		return params
	}
	out := make(map[string]any, len(params)+2)
	for k, v := range params {
		out[k] = v
	}
	out["async"], out["clarify"] = true, false
	return out
}

// ── run-id-query.ts ──

const (
	minSafeAsyncRunPrefixLength = 8
	fullAsyncRunIDLength        = 36
)

// canScanAsyncRunPrefix: full generated ids and longer exact identities never fall back to a root scan.
func canScanAsyncRunPrefix(id string) bool {
	n := len(jsUTF16(id))
	return n >= minSafeAsyncRunPrefixLength && n < fullAsyncRunIDLength
}

// jsUTF16 is the UTF-16 code units of s (JavaScript string length).
func jsUTF16(s string) []uint16 {
	var out []uint16
	for _, r := range s {
		if r >= 0x10000 {
			r -= 0x10000
			out = append(out, uint16(0xd800+(r>>10)), uint16(0xdc00+(r&0x3ff)))
		} else {
			out = append(out, uint16(r))
		}
	}
	return out
}

// ── pid-namespace.ts ──

var (
	pidScopeOnce sync.Once
	pidScope     string
)

// currentPidNamespaceScope is the Linux PID namespace identity ("" elsewhere), cached: reconciliation polls it.
func currentPidNamespaceScope() string {
	pidScopeOnce.Do(func() { pidScope = readPidNamespaceScope() })
	return pidScope
}

// ── process probes (stale-run-reconciler.ts checkPidLiveness) ──

// errNoSuchProcess and errNotPermitted are what a kill function reports for ESRCH and EPERM.
var (
	errNoSuchProcess = errors.New("no such process")
	errNotPermitted  = errors.New("operation not permitted")
)

// killFunc is the seam of the process probes: signal 0 only checks that the process exists.
type killFunc func(pid, signal int) error

// PidLiveness is the verdict of a liveness probe.
type PidLiveness string

const (
	pidAlive   PidLiveness = "alive"
	pidDead    PidLiveness = "dead"
	pidUnknown PidLiveness = "unknown"
)

// checkPidLiveness probes pid with signal 0: ESRCH is dead, any other failure (EPERM included) cannot tell.
func checkPidLiveness(pid int, kill killFunc) PidLiveness {
	if kill == nil {
		kill = defaultKill
	}
	err := kill(pid, 0)
	switch {
	case err == nil:
		return pidAlive
	case errors.Is(err, errNoSuchProcess):
		return pidDead
	}
	return pidUnknown
}
