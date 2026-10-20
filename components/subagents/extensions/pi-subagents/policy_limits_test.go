package pi_subagents

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func envOf(m map[string]string) envLookup {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestRecursionGuard(t *testing.T) {
	const f = "recursion-guard"
	noEnv := envOf(nil)
	i := func(n int, ok bool) [2]any { return [2]any{n, ok} }
	norm := func(v any) [2]any { n, ok := normalizeMaxSubagentDepth(v); return i(n, ok) }
	spawns := func(v any) [2]any { n, ok := normalizeMaxSubagentSpawnsPerSession(v); return i(n, ok) }
	tw(t, f, "is 2", func(t *testing.T) { equal(t, defaultSubagentMaxDepth, 2, "default depth") })
	tw(t, f, "accepts integers >= 0", func(t *testing.T) {
		equal(t, norm(0.0), i(0, true), "0")
		equal(t, norm(3.0), i(3, true), "3")
		equal(t, norm("4"), i(4, true), `"4"`)
	})
	tw(t, f, "rejects negatives and non-integers", func(t *testing.T) {
		equal(t, norm(-1.0), i(0, false), "-1")
		equal(t, norm(1.5), i(0, false), "1.5")
		equal(t, norm("garbage"), i(0, false), "garbage")
	})
	tw(t, f, "accepts integers >= 0", func(t *testing.T) {
		equal(t, spawns(0.0), i(0, true), "0")
		equal(t, spawns(12.0), i(12, true), "12")
		equal(t, spawns("9"), i(9, true), `"9"`)
	})
	tw(t, f, "rejects negatives and non-integers", func(t *testing.T) {
		equal(t, spawns(-1.0), i(0, false), "-1")
		equal(t, spawns(1.5), i(0, false), "1.5")
		equal(t, spawns("garbage"), i(0, false), "garbage")
	})
	perSession := func(env map[string]string, config any) [2]any {
		n, ok := resolveMaxSubagentSpawnsPerSession(config, envOf(env))
		return i(n, ok)
	}
	tw(t, f, "uses positive env values as opt-in caps", func(t *testing.T) {
		equal(t, perSession(map[string]string{"PI_SUBAGENT_MAX_SPAWNS_PER_SESSION": "5"}, 1.0), i(5, true), "env 5")
	})
	tw(t, f, "falls back to a positive config cap when env is absent", func(t *testing.T) {
		equal(t, perSession(nil, 7.0), i(7, true), "config 7")
	})
	tw(t, f, "ignores invalid env values and falls back to config", func(t *testing.T) {
		for _, v := range []string{"garbage", "-1", "1.5"} {
			env := map[string]string{"PI_SUBAGENT_MAX_SPAWNS_PER_SESSION": v}
			equal(t, perSession(env, 7.0), i(7, true), v)
			_, ok := resolveMaxSubagentSpawnsPerSession(nil, envOf(env))
			equal(t, ok, false, v+" without config")
		}
	})
	tw(t, f, "is unlimited by default", func(t *testing.T) {
		_, ok := resolveMaxSubagentSpawnsPerSession(nil, noEnv)
		equal(t, ok, false, "unset")
		_, ok = resolveMaxSubagentSpawnsPerSession(-1.0, noEnv)
		equal(t, ok, false, "-1")
	})
	tw(t, f, "treats zero as an explicit unlimited override", func(t *testing.T) {
		_, ok := resolveMaxSubagentSpawnsPerSession(7.0, envOf(map[string]string{"PI_SUBAGENT_MAX_SPAWNS_PER_SESSION": "0"}))
		equal(t, ok, false, "env 0")
		_, ok = resolveMaxSubagentSpawnsPerSession(0.0, noEnv)
		equal(t, ok, false, "config 0")
	})
	maxDepth := func(config any, rt *subagentDepthContext) int {
		return resolveCurrentMaxSubagentDepth(config, rt, noEnv)
	}
	tw(t, f, "uses the executor's own child runtime max when present", func(t *testing.T) {
		equal(t, maxDepth(1.0, &subagentDepthContext{Depth: 1, MaxDepth: 5.0, HasMaxDepth: true}), 5, "runtime max")
	})
	tw(t, f, "falls back to config for a top-level parent", func(t *testing.T) {
		equal(t, maxDepth(1.0, nil), 1, "config")
		equal(t, maxDepth(1.0, &subagentDepthContext{Depth: 1}), 1, "runtime without max")
	})
	tw(t, f, "falls back to default when neither runtime nor config is valid", func(t *testing.T) {
		equal(t, maxDepth(nil, nil), 2, "unset")
		equal(t, maxDepth(-1.0, nil), 2, "-1")
	})
	tw(t, f, "resolves maxTasks from config or falls back to the default", func(t *testing.T) {
		equal(t, resolveTopLevelParallelMaxTasks(12.0), 12, "12")
		equal(t, resolveTopLevelParallelMaxTasks(nil), 8, "unset")
		equal(t, resolveTopLevelParallelMaxTasks(0.0), 8, "0")
		equal(t, resolveTopLevelParallelMaxTasks("oops"), 8, "oops")
	})
	tw(t, f, "resolves concurrency from per-call override, config, or default", func(t *testing.T) {
		equal(t, resolveTopLevelParallelConcurrency(2.0, 6.0), 2, "override")
		equal(t, resolveTopLevelParallelConcurrency(nil, 6.0), 6, "config")
		equal(t, resolveTopLevelParallelConcurrency(0.0, 6.0), 6, "0 override")
		equal(t, resolveTopLevelParallelConcurrency(nil, 0.0), 4, "0 config")
	})
	tw(t, f, "keeps the inherited max when agent override is absent", func(t *testing.T) { equal(t, resolveChildMaxSubagentDepth(3, nil), 3, "inherited") })
	tw(t, f, "tightens to the lower per-agent max", func(t *testing.T) {
		equal(t, resolveChildMaxSubagentDepth(3, 1.0), 1, "3,1")
		equal(t, resolveChildMaxSubagentDepth(2, 0.0), 0, "2,0")
	})
	tw(t, f, "does not relax an already stricter inherited max", func(t *testing.T) { equal(t, resolveChildMaxSubagentDepth(1, 3.0), 1, "1,3") })
	check := func(config any, rt *subagentDepthContext) depthCheck { return checkSubagentDepth(config, rt, noEnv) }
	tw(t, f, "not blocked at depth=0, max=2", func(t *testing.T) { equal(t, check(2.0, nil), depthCheck{false, 0, 2}, "check") })
	tw(t, f, "uses config max depth when the runtime carries none", func(t *testing.T) {
		r := check(1.0, &subagentDepthContext{Depth: 1})
		equal(t, r.Blocked, true, "blocked")
		equal(t, r.MaxDepth, 1, "max")
	})
	tw(t, f, "not blocked at depth=1, max=2", func(t *testing.T) {
		equal(t, check(nil, &subagentDepthContext{Depth: 1, MaxDepth: 2.0, HasMaxDepth: true}).Blocked, false, "blocked")
	})
	tw(t, f, "blocks at depth=1, max=1 after one nested level", func(t *testing.T) {
		equal(t, check(nil, &subagentDepthContext{Depth: 1, MaxDepth: 1.0, HasMaxDepth: true}).Blocked, true, "blocked")
	})
	tw(t, f, "blocked at depth=2, max=2", func(t *testing.T) {
		equal(t, check(nil, &subagentDepthContext{Depth: 2, MaxDepth: 2.0, HasMaxDepth: true}), depthCheck{true, 2, 2}, "check")
	})
	tw(t, f, "blocked at depth=3, max=2", func(t *testing.T) {
		equal(t, check(nil, &subagentDepthContext{Depth: 3, MaxDepth: 2.0, HasMaxDepth: true}).Blocked, true, "blocked")
	})
	tw(t, f, "blocked at depth=0, max=0 (disables subagent entirely)", func(t *testing.T) { equal(t, check(0.0, nil).Blocked, true, "blocked") })
	tw(t, f, "defaults to depth=0, max=2 for a top-level parent", func(t *testing.T) { equal(t, check(nil, nil), depthCheck{false, 0, 2}, "check") })
	tw(t, f, "not blocked when the runtime depth is invalid (NaN)", func(t *testing.T) {
		equal(t, check(nil, &subagentDepthContext{Depth: math.NaN(), MaxDepth: 2.0, HasMaxDepth: true}).Blocked, false, "blocked")
	})
	child := func(max any, rt *subagentDepthContext) [2]any {
		d, m := resolveChildDepth(max, rt, noEnv)
		return [2]any{d, m}
	}
	tw(t, f, "increments from a top-level parent", func(t *testing.T) { equal(t, child(nil, nil), [2]any{1.0, 2}, "child") })
	tw(t, f, "increments from depth=1", func(t *testing.T) { equal(t, child(nil, &subagentDepthContext{Depth: 1}), [2]any{2.0, 2}, "child") })
	tw(t, f, "uses provided max depth override", func(t *testing.T) { equal(t, child(1.0, nil), [2]any{1.0, 1}, "child") })
	tw(t, f, "respects the inherited runtime max when override is absent", func(t *testing.T) {
		equal(t, child(nil, &subagentDepthContext{Depth: 0, MaxDepth: 5.0, HasMaxDepth: true}), [2]any{1.0, 5}, "child")
	})
	tw(t, f, "uses the explicit child override even when a looser inherited max exists", func(t *testing.T) {
		equal(t, child(1.0, &subagentDepthContext{Depth: 0, MaxDepth: 5.0, HasMaxDepth: true}), [2]any{1.0, 1}, "child")
	})
	tw(t, f, "falls back to depth=1 when the runtime depth is invalid (NaN)", func(t *testing.T) {
		d, _ := resolveChildDepth(nil, &subagentDepthContext{Depth: math.NaN()}, noEnv)
		equal(t, d, 1.0, "depth")
	})
}

func TestFanoutSpawnsPerRun(t *testing.T) {
	tw(t, "run-fanout-budget", "resolves environment over config and falls back to 64", func(t *testing.T) {
		none := envOf(nil)
		equal(t, resolveMaxSubagentSpawnsPerRun(nil, none), 64, "unset")
		equal(t, resolveMaxSubagentSpawnsPerRun(12.0, none), 12, "config")
		for _, bad := range []any{0.0, -1.0, 1.5, "bad"} {
			equal(t, resolveMaxSubagentSpawnsPerRun(bad, none), 64, "invalid config")
		}
		seven := envOf(map[string]string{"PI_SUBAGENT_MAX_SPAWNS_PER_RUN": "7"})
		equal(t, resolveMaxSubagentSpawnsPerRun(12.0, seven), 7, "env wins")
		for _, bad := range []string{"0", "-1", "1.5", "bad"} {
			equal(t, resolveMaxSubagentSpawnsPerRun(nil, envOf(map[string]string{"PI_SUBAGENT_MAX_SPAWNS_PER_RUN": bad})), 64, "invalid env "+bad)
		}
	})
}

func TestTempPaths(t *testing.T) {
	const f = "temp-paths"
	tw(t, f, "prefers uid when available", func(t *testing.T) {
		got := resolveTempScopeID(&tempScopeOptions{Getuid: func() int { return 501 }, Env: map[string]string{"USER": "alice"}, UserInfo: func() (string, error) { return "alice", nil }})
		equal(t, got, "uid-501", "scope")
	})
	tw(t, f, "falls back to environment usernames when uid is unavailable", func(t *testing.T) {
		got := resolveTempScopeID(&tempScopeOptions{Env: map[string]string{"USERNAME": "Alice Example"}, UserInfo: func() (string, error) { return "ignored", nil }})
		equal(t, got, "user-Alice-Example", "scope")
	})
	tw(t, f, "falls back to os.userInfo when environment is missing", func(t *testing.T) {
		got := resolveTempScopeID(&tempScopeOptions{Env: map[string]string{}, UserInfo: func() (string, error) { return "svc_account", nil }})
		equal(t, got, "user-svc_account", "scope")
	})
	tw(t, f, "falls back to home path when os.userInfo throws", func(t *testing.T) {
		got := resolveTempScopeID(&tempScopeOptions{
			Env:      map[string]string{},
			UserInfo: func() (string, error) { return "", os.ErrNotExist },
			Homedir:  func() (string, error) { return "/home/12345/app user", nil },
		})
		equal(t, got, "home-home-12345-app-user", "scope")
	})
	tw(t, f, "uses the explicit temp root before shared paths resolve", func(t *testing.T) {
		base := t.TempDir()
		override := filepath.Join(base, "async state")
		t.Setenv("PI_SUBAGENTS_TEMP_ROOT", override)
		t.Setenv("HOME", filepath.Join(base, "home"))
		equal(t, asyncDir(), filepath.Join(override, "async-subagent-runs"), "ASYNC_DIR")
		equal(t, resultsDir(), filepath.Join(override, "async-subagent-results"), "RESULTS_DIR")
		equal(t, tempRootDir(), override, "TEMP_ROOT_DIR")
	})
	tskip(t, f, "isolates agent-dir profile writes from an inherited PI_CODING_AGENT_DIR", "runs the original's profiles module and its Node test loader (test/support/isolated-temp-root.mjs) in a child Node process; the Go port has neither (profiles slice)")
	tskip(t, f, "records a nested test process as the runner parent", "tests the original's Node test loader (test/support/isolated-temp-root.mjs), which the Go port does not have")
	tw(t, f, "anchors shared temp directories under one scoped root", func(t *testing.T) {
		t.Setenv("PI_SUBAGENTS_TEMP_ROOT", "")
		root := tempRootDir()
		for _, p := range []struct{ got, name string }{{resultsDir(), "async-subagent-results"}, {asyncDir(), "async-subagent-runs"}, {chainRunsDir(), "chain-runs"}, {tempArtifactsDir(), "artifacts"}} {
			equal(t, filepath.Dir(p.got), root, p.name+" parent")
			equal(t, filepath.Base(p.got), p.name, "name")
		}
		polMatches(t, filepath.Base(root), `^pi-subagents-`)
	})
	tskip(t, f, "stops a test runner before consuming config when its test parent is gone", "tests the original's Node background-runner bootstrap (subagent-runner-bootstrap.ts) and its PI_SUBAGENTS_TEST_PARENT_PID loader; the Go port has no Node runner")
	tw(t, f, "writes async config files under the same scoped temp root", func(t *testing.T) {
		equal(t, filepath.Dir(asyncConfigPath("abc123")), tempRootDir(), "dir")
		equal(t, filepath.Base(asyncConfigPath("abc123")), "async-cfg-abc123.json", "name")
	})
}
