package pi_subagents

import (
	"math"
	"sort"
	"strings"
	"testing"
)

// fakeClock is the batcher test's clock (completion-batcher.test.ts createFakeClock).
type fakeClock struct {
	now    int64
	nextID int
	jobs   map[int]*fakeJob
}

type fakeJob struct {
	id     int
	fireAt int64
	fn     func()
}

func newFakeClock() *fakeClock { return &fakeClock{nextID: 1, jobs: map[int]*fakeJob{}} }

func (c *fakeClock) SetTimeout(fn func(), delayMs int) any {
	id := c.nextID
	c.nextID++
	c.jobs[id] = &fakeJob{id: id, fireAt: c.now + int64(delayMs), fn: fn}
	return id
}

func (c *fakeClock) ClearTimeout(h any) {
	if id, ok := h.(int); ok {
		delete(c.jobs, id)
	}
}

func (c *fakeClock) advance(ms int64) {
	c.now += ms
	var due []*fakeJob
	for _, j := range c.jobs {
		if j.fireAt <= c.now {
			due = append(due, j)
		}
	}
	sort.Slice(due, func(a, b int) bool { return due[a].fireAt < due[b].fireAt })
	for _, j := range due {
		if _, ok := c.jobs[j.id]; !ok {
			continue
		}
		delete(c.jobs, j.id)
		j.fn()
	}
}

func (c *fakeClock) pending() int { return len(c.jobs) }

func TestResolveCompletionBatchConfig(t *testing.T) {
	const f = "completion-batcher"
	tw(t, f, "applies defaults when no config is provided", func(t *testing.T) {
		eq(t, resolveCompletionBatchConfig(nil, nil), defaultCompletionBatchConfig, "defaults")
	})
	tw(t, f, "override wins over global config", func(t *testing.T) {
		r := resolveCompletionBatchConfig(map[string]any{"debounceMs": 50.0, "maxWaitMs": 200.0}, map[string]any{"debounceMs": 80.0})
		eq(t, r.DebounceMs, 80, "debounce")
		eq(t, r.MaxWaitMs, 200, "maxWait")
	})
	tw(t, f, "rejects invalid booleans and non-positive or fractional integers", func(t *testing.T) {
		r := resolveCompletionBatchConfig(map[string]any{"enabled": "false", "debounceMs": 0.0, "maxWaitMs": -5.0, "stragglerDebounceMs": 1.5, "stragglerWindowMs": math.NaN()}, nil)
		d := defaultCompletionBatchConfig
		eq(t, r.Enabled, d.Enabled, "enabled")
		eq(t, r.DebounceMs, d.DebounceMs, "debounce")
		eq(t, r.MaxWaitMs, d.MaxWaitMs, "maxWait")
		eq(t, r.StragglerDebounceMs, d.StragglerDebounceMs, "stragglerDebounce")
		eq(t, r.StragglerWindowMs, d.StragglerWindowMs, "stragglerWindow")
	})
	tw(t, f, "ignores invalid overrides instead of masking valid global config", func(t *testing.T) {
		r := resolveCompletionBatchConfig(map[string]any{"enabled": false, "debounceMs": 80.0}, map[string]any{"enabled": "false", "debounceMs": "bad"})
		eq(t, r.Enabled, false, "enabled")
		eq(t, r.DebounceMs, 80, "debounce")
	})
}

type labelled struct{ label string }

func labels(items []labelled) []string {
	out := []string{}
	for _, i := range items {
		out = append(out, i.label)
	}
	return out
}

func TestCompletionBatcher(t *testing.T) {
	const f = "completion-batcher"
	collect := func() (*[][]string, func([]labelled)) {
		var emitted [][]string
		return &emitted, func(items []labelled) { emitted = append(emitted, labels(items)) }
	}
	tw(t, f, "emits immediately when batching is disabled", func(t *testing.T) {
		emitted, emit := collect()
		cfg := defaultCompletionBatchConfig
		cfg.Enabled = false
		b := newCompletionBatcher(cfg, emit, nil, nil)
		b.Push(labelled{"a"})
		b.Push(labelled{"b"})
		eq(t, *emitted, [][]string{{"a"}, {"b"}}, "emitted")
		b.Dispose()
	})
	tw(t, f, "coalesces a burst into one grouped emit when the debounce fires", func(t *testing.T) {
		clock := newFakeClock()
		emitted, emit := collect()
		b := newCompletionBatcher(CompletionBatchConfig{true, 150, 1000, 75, 400, 2000}, emit, clock, func() int64 { return clock.now })
		b.Push(labelled{"a"})
		b.Push(labelled{"b"})
		clock.advance(40)
		b.Push(labelled{"c"})
		eq(t, len(*emitted), 0, "nothing yet")
		clock.advance(150)
		eq(t, *emitted, [][]string{{"a", "b", "c"}}, "emitted")
		eq(t, clock.pending(), 0, "timers")
		b.Dispose()
	})
	tw(t, f, "emits at the max-wait cap even when items keep arriving", func(t *testing.T) {
		clock := newFakeClock()
		emitted, emit := collect()
		b := newCompletionBatcher(CompletionBatchConfig{true, 100, 300, 50, 150, 2000}, emit, clock, func() int64 { return clock.now })
		b.Push(labelled{"a"})
		for _, l := range []string{"late-0", "late-1", "late-2", "late-3", "late-4"} {
			b.Push(labelled{l})
			clock.advance(50)
		}
		eq(t, len(*emitted), 0, "nothing yet")
		clock.advance(50)
		eq(t, *emitted, [][]string{{"a", "late-0", "late-1", "late-2", "late-3", "late-4"}}, "emitted")
		b.Dispose()
	})
	tw(t, f, "flush emits held items immediately and clears timers", func(t *testing.T) {
		clock := newFakeClock()
		emitted, emit := collect()
		cfg := defaultCompletionBatchConfig
		b := newCompletionBatcher(cfg, emit, clock, func() int64 { return clock.now })
		b.Push(labelled{"a"})
		b.Push(labelled{"b"})
		eq(t, clock.pending(), 2, "timers")
		b.Flush()
		eq(t, *emitted, [][]string{{"a", "b"}}, "emitted")
		eq(t, clock.pending(), 0, "timers after flush")
		clock.advance(1000)
		eq(t, *emitted, [][]string{{"a", "b"}}, "no re-emit")
		b.Dispose()
	})
	tw(t, f, "dispose clears timers without emitting", func(t *testing.T) {
		clock := newFakeClock()
		emitted, emit := collect()
		b := newCompletionBatcher(defaultCompletionBatchConfig, emit, clock, func() int64 { return clock.now })
		b.Push(labelled{"a"})
		abandoned := b.Dispose()
		eq(t, labels(abandoned), []string{"a"}, "abandoned")
		eq(t, len(b.Dispose()), 0, "second dispose")
		eq(t, len(*emitted), 0, "emitted")
		eq(t, clock.pending(), 0, "timers")
		clock.advance(1000)
		eq(t, len(*emitted), 0, "still nothing")
	})
	tw(t, f, "creates a shorter straggler group for late siblings", func(t *testing.T) {
		clock := newFakeClock()
		emitted, emit := collect()
		b := newCompletionBatcher(CompletionBatchConfig{true, 200, 1000, 50, 150, 2000}, emit, clock, func() int64 { return clock.now })
		b.Push(labelled{"a"})
		clock.advance(200)
		eq(t, *emitted, [][]string{{"a"}}, "first group")
		clock.advance(100)
		b.Push(labelled{"b"})
		eq(t, *emitted, [][]string{{"a"}}, "held")
		clock.advance(50)
		eq(t, *emitted, [][]string{{"a"}, {"b"}}, "straggler group")
		b.Dispose()
	})
	tw(t, f, "starts a fresh normal group after the straggler window passes", func(t *testing.T) {
		clock := newFakeClock()
		emitted, emit := collect()
		b := newCompletionBatcher(CompletionBatchConfig{true, 200, 1000, 50, 150, 500}, emit, clock, func() int64 { return clock.now })
		b.Push(labelled{"a"})
		clock.advance(200)
		eq(t, *emitted, [][]string{{"a"}}, "first group")
		clock.advance(600)
		b.Push(labelled{"b"})
		clock.advance(50)
		eq(t, *emitted, [][]string{{"a"}}, "still held")
		clock.advance(150)
		eq(t, *emitted, [][]string{{"a"}, {"b"}}, "normal group")
		b.Dispose()
	})
}

func TestBuildCompletionKey(t *testing.T) {
	const f = "completion-dedupe"
	tw(t, f, "scopes ids to their owning session", func(t *testing.T) {
		first := buildCompletionKey(map[string]any{"id": "run-123", "sessionId": "session-a"}, "fallback")
		second := buildCompletionKey(map[string]any{"id": "run-123", "sessionId": "session-b"}, "fallback")
		eq(t, first, "session:session-a:id:run-123", "key")
		if first == second {
			t.Errorf("keys equal: %q", first)
		}
	})
	tw(t, f, "treats paused and complete payloads as distinct completions", func(t *testing.T) {
		paused := buildCompletionKey(map[string]any{"id": "run-123", "sessionId": "session-a", "state": "paused"}, "fallback")
		complete := buildCompletionKey(map[string]any{"id": "run-123", "sessionId": "session-a", "state": "complete"}, "fallback")
		eq(t, paused, "session:session-a:id:run-123:state:paused", "paused")
		if paused == complete {
			t.Errorf("keys equal: %q", paused)
		}
	})
	tw(t, f, "builds deterministic fallback key when id is missing", func(t *testing.T) {
		d := map[string]any{"agent": "reviewer", "timestamp": 123.0, "taskIndex": 1.0, "totalTasks": 2.0, "success": true}
		eq(t, buildCompletionKey(d, "x"), buildCompletionKey(d, "x"), "deterministic")
		eq(t, buildCompletionKey(d, "x"), "meta:no-session:reviewer:123:1:2:1:x", "shape")
	})
	tw(t, f, "returns true only for duplicates within ttl", func(t *testing.T) {
		seen := map[string]int64{}
		eq(t, markSeenWithTTL(seen, "k", 100, 1000), false, "first")
		eq(t, markSeenWithTTL(seen, "k", 200, 1000), true, "duplicate")
		eq(t, markSeenWithTTL(seen, "k", 1201, 1000), false, "expired")
	})
}

func TestChildCacheRetention(t *testing.T) {
	const f = "child-cache-retention"
	tw(t, f, "is unset by default so children inherit the parent's retention", func(t *testing.T) {
		eq(t, childCacheRetention(map[string]string{}), "", "unset")
		eq(t, childCacheRetention(map[string]string{"PI_SUBAGENT_CACHE_RETENTION": ""}), "", "empty")
	})
	tw(t, f, "returns the configured tier", func(t *testing.T) {
		eq(t, childCacheRetention(map[string]string{"PI_SUBAGENT_CACHE_RETENTION": "short"}), "short", "short")
		eq(t, childCacheRetention(map[string]string{"PI_SUBAGENT_CACHE_RETENTION": "long"}), "long", "long")
	})
	tw(t, f, "ignores the parent's own retention", func(t *testing.T) {
		eq(t, childCacheRetention(map[string]string{"PI_CACHE_RETENTION": "long"}), "", "parent tier")
	})
	tw(t, f, "contributes nothing when unset so a spawned child inherits the parent value", func(t *testing.T) {
		eq(t, childCacheRetentionEnv(map[string]string{}), map[string]string{}, "empty")
		launch := map[string]string{"PI_CACHE_RETENTION": "long"}
		for k, v := range childCacheRetentionEnv(map[string]string{}) {
			launch[k] = v
		}
		eq(t, launch["PI_CACHE_RETENTION"], "long", "inherited")
	})
	tw(t, f, "overrides the inherited value in a spawned child's launch environment", func(t *testing.T) {
		launch := map[string]string{"PI_CACHE_RETENTION": "long"}
		for k, v := range childCacheRetentionEnv(map[string]string{"PI_SUBAGENT_CACHE_RETENTION": "short"}) {
			launch[k] = v
		}
		eq(t, launch["PI_CACHE_RETENTION"], "short", "overridden")
	})
	// The original wraps an in-process child session's stream function; a Go child is a separate pig process that gets the
	// tier through its environment (childCacheRetentionEnv, applied in childEnv), so these cases have no function to wrap.
	tskip(t, f, "leaves the stream function untouched when no tier is configured", "pinChildCacheRetention wraps an in-process Pi session's stream function; a pig child process has none, and its unset tier leaves PI_CACHE_RETENTION alone (covered by 'contributes nothing when unset')")
	tskip(t, f, "overrides an inherited parent tier on the request env", "the per-request env of an in-process stream function does not exist for a child process; the override reaches it as PI_CACHE_RETENTION (covered by 'overrides the inherited value in a spawned child's launch environment')")
	tskip(t, f, "preserves other request env values and options", "tests that the wrapper forwards the other stream options; a child process inherits its whole environment, which TestChildEnvKeepsInheritedValuesAndPinsRetention covers")
	tskip(t, f, "leaves a sibling session's stream function alone", "tests isolation between two in-process sessions sharing one process; the tier is per child process environment and this extension never writes process.env (TestChildEnvKeepsInheritedValuesAndPinsRetention checks the parent's environment is untouched)")
}

func TestBackgroundProcessOptions(t *testing.T) {
	const f = "background-process-options"
	tw(t, f, "Windows background runners stay hidden without requesting a new console", func(t *testing.T) {
		eq(t, backgroundProcessOptions("windows"), BackgroundProcessOptions{Detached: false, WindowsHide: true}, "windows")
	})
	tw(t, f, "background runners remain detached on Unix", func(t *testing.T) {
		eq(t, backgroundProcessOptions("linux"), BackgroundProcessOptions{Detached: true, WindowsHide: true}, "linux")
	})
}

func TestWaitToolConfig(t *testing.T) {
	r, err := resolveWaitToolConfig(nil, map[string]string{})
	eq(t, err, nil, "err")
	eq(t, r, ResolvedWaitToolConfig{Enabled: true}, "default")
	r, _ = resolveWaitToolConfig(map[string]any{"enabled": false, "defaultTimeoutMs": 5000.0}, map[string]string{})
	eq(t, r.Enabled, false, "config enabled")
	eq(t, *r.DefaultTimeoutMs, 5000, "config timeout")
	r, _ = resolveWaitToolConfig(false, map[string]string{waitToolEnabledEnv: "yes", waitToolDefaultTimeoutMsEnv: "7"})
	eq(t, r.Enabled, true, "env wins")
	eq(t, *r.DefaultTimeoutMs, 7, "env timeout")
	_, err = resolveWaitToolConfig(nil, map[string]string{waitToolEnabledEnv: "maybe"})
	hasMatch(t, err, "PI_SUBAGENT_WAIT_TOOL_ENABLED must be one of")
	_, err = resolveWaitToolConfig(nil, map[string]string{waitToolDefaultTimeoutMsEnv: "0"})
	hasMatch(t, err, "must be a positive integer")
	_, err = resolveWaitToolConfig("on", map[string]string{})
	hasMatch(t, err, "config.waitTool must be a boolean or an object")
}

func TestTopLevelAsyncOverrideAndPrefixRule(t *testing.T) {
	p := map[string]any{"agent": "a"}
	got := applyForceTopLevelAsyncOverride(p, 0, true)
	eq(t, got["async"], true, "async forced")
	eq(t, got["clarify"], false, "clarify off")
	eq(t, p["async"], nil, "input untouched")
	eq(t, applyForceTopLevelAsyncOverride(p, 1, true)["async"], nil, "depth 1")
	eq(t, applyForceTopLevelAsyncOverride(map[string]any{"foregroundOnly": true}, 0, true)["async"], nil, "foreground only")
	eq(t, canScanAsyncRunPrefix("abcdefg"), false, "short")
	eq(t, canScanAsyncRunPrefix("abcdefgh"), true, "min")
	eq(t, canScanAsyncRunPrefix("123e4567-e89b-12d3-a456-426614174000"), false, "full id")
}

func TestChildEnvKeepsInheritedValuesAndPinsRetention(t *testing.T) {
	base := []string{"PATH=/bin", "PI_CACHE_RETENTION=long", "PI_SUBAGENT_CACHE_RETENTION=short", "ANTHROPIC_API_KEY=sk-test"}
	env := childEnv(base, 0, "helper")
	last := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		last[k] = v
	}
	eq(t, last["PI_CACHE_RETENTION"], "short", "the pinned tier")
	eq(t, last["ANTHROPIC_API_KEY"], "sk-test", "other values kept")
	eq(t, base[1], "PI_CACHE_RETENTION=long", "the parent's list is untouched")
	env = childEnv([]string{"PI_CACHE_RETENTION=long"}, 0, "helper")
	eq(t, env[0], "PI_CACHE_RETENTION=long", "inherited when no tier is pinned")
}
