package pi_subagents

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Twins of async-execution.test.ts (the guidance text and the process-terminal event), async-recovery-descriptor.test.ts
// and async-result-timeout-evidence.test.ts (with the test-support helper it exercises).

func TestAsyncExecutionTwins(t *testing.T) {
	const f = "async-execution"
	tw(t, f, "formats interactive yield and headless auto-drain guidance separately", func(t *testing.T) {
		match := func(pattern, s string) {
			t.Helper()
			if !regexp.MustCompile(pattern).MatchString(s) {
				t.Errorf("%q does not match %s", s, pattern)
			}
		}
		noMatch := func(pattern, s string) {
			t.Helper()
			if regexp.MustCompile(pattern).MatchString(s) {
				t.Errorf("%q must not match %s", s, pattern)
			}
		}
		interactive := formatAsyncStartedMessage("Async: worker [interactive]", true)
		match(`(?is)interactive session.*return control`, interactive)
		match(`(?i)native completion notification`, interactive)
		match(`(?i)does not need a wait call`, interactive)
		match(`(?i)provider, detached, or other background work that lacks a native completion notification`, interactive)
		noMatch(`(?i)bg_wait\(\{ id:`, interactive)
		noMatch(`(?i)auto-drains current-session background work`, interactive)

		headless := formatAsyncStartedMessage("Async: worker [headless]", false)
		match(`(?i)non-interactive run.*auto-drains current-session subagent work at agent_end`, headless)
		match(`(?i)Use bg_wait only.*provider, detached, or other background-work results.*no native completion notification`, headless)
		noMatch(`nonBlocking: true`, headless)
		noMatch(`(?i)By default, return control to the user`, headless)
	})
	tskip(t, f, "places detached runner stdio logs in the async run directory", "the Go port has no detached runner process to log: the child's stdout is output-N.log (its JSON events, read by the in-process supervisor) and only its stderr goes to runner.stderr.log in the async directory (asserted by TestOmittedAsyncRunsInTheBackgroundAndWakesTheSession)")
	tskip(t, f, "omits runner log paths when asyncDir is unavailable", "resolveAsyncRunnerLogPaths has no Go counterpart: the supervisor always has its run directory (RunConfig.AsyncDir), so there is no launch without one")

	proof := &ProcessTerminal{Version: 1, State: "unknown", RunID: "run-terminal", RunnerProcessInstanceID: "runner-1", Reason: "writer-close-unverified"}
	staleMessage := "This extension ctx is stale after session replacement or reload."
	tw(t, f, "emits the process terminal proof on a live event bus", func(t *testing.T) {
		type emitted struct {
			channel string
			payload any
		}
		var got []emitted
		emitProcessTerminalEvent(func(c string, p any) error { got = append(got, emitted{c, p}); return nil }, proof)
		eq(t, got, []emitted{{subagentProcessTerminalEvt, proof}}, "emitted")
		eq(t, subagentProcessTerminalEvt, "subagent:process-terminal", "channel")
	})
	tw(t, f, "drops stale extension ctx failures without throwing", func(t *testing.T) {
		old := logAsyncError
		t.Cleanup(func() { logAsyncError = old })
		logged := false
		logAsyncError = func(...any) { logged = true }
		emitProcessTerminalEvent(func(string, any) error { return errors.New(staleMessage) }, proof)
		if logged {
			t.Error("a stale ctx failure was logged")
		}
	})
	tw(t, f, "logs non-stale event bus failures without throwing", func(t *testing.T) {
		old := logAsyncError
		t.Cleanup(func() { logAsyncError = old })
		var logged []any
		logAsyncError = func(args ...any) { logged = args }
		emitProcessTerminalEvent(func(string, any) error { return errors.New("event bus unavailable") }, proof)
		found := false
		for _, a := range logged {
			if e, ok := a.(error); ok && e.Error() == "event bus unavailable" {
				found = true
			}
		}
		if !found {
			t.Errorf("logged %v", logged)
		}
	})
}

// createRunFanoutBudget is run-fanout-budget.ts createRunFanoutBudget (the port only validates a persisted descriptor).
func createRunFanoutBudget(t *testing.T, rootRunID string, limit int) map[string]any {
	t.Helper()
	root := runFanoutRoot()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	safe := regexp.MustCompile(`[^A-Za-z0-9._-]`).ReplaceAllString(rootRunID, "_")
	dir := filepath.Join(root, safe+"-"+randomUUID())
	if err := os.MkdirAll(filepath.Join(dir, "claims"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{"version": 1, "rootRunId": rootRunID, "limit": limit, "createdAt": time.Now().UnixMilli()}
	b, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	return map[string]any{"version": 1, "rootRunId": rootRunID, "directory": dir, "limit": limit}
}

func TestAsyncRecoveryDescriptorTwins(t *testing.T) {
	const f = "async-recovery-descriptor"
	// baseDescriptor is the descriptor every case of the test file starts from.
	baseDescriptor := func(t *testing.T, root, runID string) map[string]any {
		return map[string]any{"version": 1, "runFanoutBudget": createRunFanoutBudget(t, runID, 64), "sourceRunId": runID, "agent": "worker", "cwd": root,
			"systemPromptMode": "replace", "inheritGlobalContext": false, "inheritProjectContext": false, "inheritSkills": false, "outputMode": "inline", "maxSubagentDepth": 2, "share": false}
	}
	write := func(t *testing.T, root string, d map[string]any) {
		t.Helper()
		b, _ := json.Marshal(d)
		writeFile(t, filepath.Join(root, "recovery-descriptor.json"), string(b))
	}
	env := func(t *testing.T) string {
		t.Setenv("PI_SUBAGENTS_TEMP_ROOT", tmp(t))
		return tmp(t)
	}
	tskip(t, f, "snapshots an explicit empty descendant allowlist before detached spawn", "the Go agent definition does not read allowedAgents (the descendant allowlist is part of the capability-ceiling slice, which is not ported), so a launch has no allowlist to snapshot")
	tw(t, f, "accepts launchContractDigest written by async execution", func(t *testing.T) {
		root := env(t)
		d := baseDescriptor(t, root, "run-digest")
		d["launchContractDigest"], d["context"] = "launch-contract-digest", "fork"
		write(t, root, d)
		got, err := readAsyncRecoveryDescriptor(root)
		eq(t, err, nil, "error")
		eq(t, got.LaunchContractDigest, "launch-contract-digest", "digest")
		eq(t, got.Context, "fork", "context")
	})
	tw(t, f, "accepts a safe baseRef in persisted recovery descriptors", func(t *testing.T) {
		root := env(t)
		d := baseDescriptor(t, root, "run-base-ref")
		d["baseRef"] = "@/foo"
		write(t, root, d)
		got, err := readAsyncRecoveryDescriptor(root)
		eq(t, err, nil, "error")
		eq(t, got.BaseRef, "@/foo", "baseRef")
	})
	tw(t, f, "rejects unsafe baseRef values in persisted recovery descriptors", func(t *testing.T) {
		root := env(t)
		for _, ref := range []string{"refs/heads/unsafe..ref", strings.Repeat("a", 40), strings.Repeat("a", 64)} {
			d := baseDescriptor(t, root, "run-bad-base-ref")
			d["baseRef"] = ref
			write(t, root, d)
			_, err := readAsyncRecoveryDescriptor(root)
			hasMatch(t, err, `baseRef must be a valid Git ref`)
		}
	})
	tw(t, f, "defaults inheritGlobalContext from inheritProjectContext for descriptors from older versions", func(t *testing.T) {
		root := env(t)
		d := baseDescriptor(t, root, "run-legacy-global-context")
		delete(d, "inheritGlobalContext")
		d["inheritProjectContext"] = true
		write(t, root, d)
		got, err := readAsyncRecoveryDescriptor(root)
		eq(t, err, nil, "error")
		eq(t, got.InheritGlobalContext, true, "inheritGlobalContext")
	})
	tw(t, f, "defaults legacy non-parent models to configured origin", func(t *testing.T) {
		root := env(t)
		d := baseDescriptor(t, root, "run-legacy-model-origin")
		d["model"] = "test/missing-primary"
		write(t, root, d)
		got, err := readAsyncRecoveryDescriptor(root)
		eq(t, err, nil, "error")
		eq(t, got.ModelOrigin, "configured", "modelOrigin")
	})
	tw(t, f, "defaults legacy parent models to inherited origin", func(t *testing.T) {
		root := env(t)
		d := baseDescriptor(t, root, "run-legacy-parent-origin")
		d["model"], d["modelOverrideFromParent"] = "gateway/parent-model", true
		write(t, root, d)
		got, err := readAsyncRecoveryDescriptor(root)
		eq(t, err, nil, "error")
		eq(t, got.ModelOrigin, "inherited", "modelOrigin")
	})
	tw(t, f, "rejects unresolved profile context values", func(t *testing.T) {
		root := env(t)
		d := baseDescriptor(t, root, "run-bad-context")
		d["context"] = "profile"
		write(t, root, d)
		_, err := readAsyncRecoveryDescriptor(root)
		hasMatch(t, err, `context is invalid`)
	})
	tw(t, f, "accepts persisted boolean fast settings and leaves omitted fast unset", func(t *testing.T) {
		root := env(t)
		d := baseDescriptor(t, root, "run-fast")
		d["fast"] = true
		write(t, root, d)
		got, err := readAsyncRecoveryDescriptor(root)
		eq(t, err, nil, "error")
		if got.Fast == nil || !*got.Fast {
			t.Errorf("fast %v", got.Fast)
		}
		d["fast"] = false
		write(t, root, d)
		got, _ = readAsyncRecoveryDescriptor(root)
		if got.Fast == nil || *got.Fast {
			t.Errorf("fast %v", got.Fast)
		}
		delete(d, "fast")
		write(t, root, d)
		got, _ = readAsyncRecoveryDescriptor(root)
		if got.Fast != nil {
			t.Errorf("fast %v", *got.Fast)
		}
	})
	tw(t, f, "rejects non-boolean fast values in persisted recovery descriptors", func(t *testing.T) {
		root := env(t)
		d := baseDescriptor(t, root, "run-bad-fast")
		d["fast"] = "true"
		write(t, root, d)
		_, err := readAsyncRecoveryDescriptor(root)
		hasMatch(t, err, `fast must be a boolean`)
	})
	tw(t, f, "rejects malformed launchContractDigest values", func(t *testing.T) {
		root := env(t)
		d := baseDescriptor(t, root, "run-bad-digest")
		d["launchContractDigest"], d["sourceRunId"] = map[string]any{}, "run-digest"
		write(t, root, d)
		_, err := readAsyncRecoveryDescriptor(root)
		hasMatch(t, err, `launchContractDigest must be a non-empty string`)
	})
}

// ── test/support/async-result-timeout-evidence.ts ──

type jsNull struct{}
type jsField struct {
	k string
	v any
}
type jsObj []jsField

func jsEncode(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case jsNull:
		return "null"
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case string:
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(x)
		return strings.TrimSuffix(b.String(), "\n")
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			if e == nil {
				parts[i] = "null"
			} else {
				parts[i] = jsEncode(e)
			}
		}
		return "[" + strings.Join(parts, ",") + "]"
	case jsObj:
		var parts []string
		for _, f := range x {
			if f.v == nil { // undefined: JSON.stringify omits the key
				continue
			}
			parts = append(parts, jsEncode(f.k)+":"+jsEncode(f.v))
		}
		return "{" + strings.Join(parts, ",") + "}"
	}
	panic(fmt.Sprintf("jsEncode %T", v))
}

func asyncResultTimeoutEvidence(asyncDir, id string) []string {
	record := func(v any) map[string]any {
		if m, ok := v.(map[string]any); ok {
			return m
		}
		return map[string]any{}
	}
	number := func(v any) any {
		if f, ok := v.(float64); ok && !math.IsInf(f, 0) && !math.IsNaN(f) {
			return f
		}
		return nil
	}
	known := func(v any, choices []string) string {
		if s, ok := v.(string); ok {
			for _, c := range choices {
				if c == s {
					return s
				}
			}
		}
		return "other/absent"
	}
	states := []string{"pending", "queued", "running", "complete", "completed", "failed", "cancelled", "paused", "stopped", "partial", "rejected", "observed", "unknown", "not-started"}
	var runnerID *string
	runnerMatches := func(v any) any {
		if runnerID == nil {
			return "unavailable"
		}
		return v == *runnerID
	}
	identity := func(raw map[string]any) jsObj {
		return jsObj{{"runIdMatches", raw["runId"] == id}, {"runnerIdMatches", runnerMatches(raw["runnerProcessInstanceId"])}}
	}
	proof := func(raw map[string]any) jsObj {
		out := identity(raw)
		out = append(out, jsField{"state", known(raw["state"], states)}, jsField{"observedAt", number(raw["observedAt"])},
			jsField{"reason", known(raw["reason"], []string{"observer-unavailable", "runner-candidate-missing", "runner-instance-mismatch", "writer-close-unverified", "process-tree-unverified", "canonical-session-unavailable", "canonical-session-lease-active", "canonical-session-release-unverified", "proof-write-failed", "stale-repair"})})
		if inst, ok := raw["instances"].([]any); ok {
			runner := []any{}
			for i, e := range inst {
				if i >= 16 {
					break
				}
				entry := record(e)
				if entry["kind"] != "runner" {
					continue
				}
				var exit, signal any
				if entry["exitCode"] == nil {
					exit = jsNull{}
				} else {
					exit = number(entry["exitCode"])
				}
				if entry["signal"] == nil {
					signal = jsNull{}
				} else {
					signal = known(entry["signal"], []string{"SIGTERM", "SIGKILL", "SIGINT", "SIGABRT", "SIGSEGV"})
				}
				runner = append(runner, jsObj{{"instanceMatches", runnerMatches(entry["processInstanceId"])}, {"closeObservedAt", number(entry["closeObservedAt"])}, {"exitCode", exit}, {"signal", signal}})
			}
			out = append(out, jsField{"runner", runner})
		}
		return out
	}
	merge := func(parts ...jsObj) jsObj {
		var out jsObj
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	eventTypes := []string{"subagent.run.started", "subagent.step.completed", "subagent.step.failed", "subagent.parallel.completed", "subagent.run.completed", "subagent.run.process_terminal"}
	names := []string{"status.json", "runner-startup-proceed.json", "process-terminal.json", "process-terminal-candidate.json", "events.jsonl", "runner.stdout.log", "runner.stderr.log"}
	var lines []string
	for _, name := range names {
		lines = append(lines, func() string {
			fail := func(err error) string {
				var syn *json.SyntaxError
				if errors.As(err, &syn) {
					return name + ": invalid JSON"
				}
				code := "other/absent"
				switch {
				case errors.Is(err, os.ErrNotExist):
					code = "ENOENT"
				case errors.Is(err, os.ErrPermission):
					code = "EACCES"
				}
				return name + ": " + code
			}
			fd, err := os.Open(filepath.Join(asyncDir, name))
			if err != nil {
				return fail(err)
			}
			defer fd.Close()
			info, err := fd.Stat()
			if err != nil {
				return fail(err)
			}
			size := info.Size()
			base := jsObj{{"bytes", size}, {"readAt", time.Now().UnixMilli()}}
			if size == 0 || strings.HasSuffix(name, ".log") || name == "runner-startup-proceed.json" {
				return name + ": " + jsEncode(base) + " (contents withheld)"
			}
			const limit = 16384
			var offset int64
			if name == "events.jsonl" && size > limit {
				offset = size - limit
			}
			if size > limit && name != "events.jsonl" {
				return name + ": " + jsEncode(base) + " (oversize, contents withheld)"
			}
			buf := make([]byte, min(size, limit))
			n, _ := fd.ReadAt(buf, offset)
			text := string(buf[:n])
			if name == "events.jsonl" {
				invalid, mismatched := 0, 0
				var events []any
				all := strings.Split(text, "\n")
				if offset > 0 {
					all = all[1:]
				}
				for _, line := range all {
					if line == "" {
						continue
					}
					var parsed any
					if json.Unmarshal([]byte(line), &parsed) != nil {
						invalid++
						continue
					}
					raw := record(parsed)
					typ, _ := raw["type"].(string)
					listed := false
					for _, e := range eventTypes {
						listed = listed || e == typ
					}
					if !listed {
						continue
					}
					if raw["runId"] != id {
						mismatched++
						continue
					}
					ev := jsObj{{"type", typ}, {"ts", number(raw["ts"])}, {"stepIndex", number(raw["stepIndex"])}, {"pid", number(raw["pid"])}, {"status", known(raw["status"], states)}, {"exitCode", number(raw["exitCode"])}}
					if pt, ok := raw["processTerminal"]; ok && pt != nil && pt != false && pt != "" {
						ev = append(ev, jsField{"proof", proof(record(pt))})
					}
					events = append(events, ev)
				}
				if len(events) > 16 {
					events = events[len(events)-16:]
				}
				return name + ": " + jsEncode(merge(base, jsObj{{"tailOnly", offset > 0}, {"invalidLines", invalid}, {"mismatchedRunEvents", mismatched}, {"events", events}}))
			}
			var parsed any
			if err := json.Unmarshal([]byte(text), &parsed); err != nil {
				return fail(err)
			}
			raw := record(parsed)
			switch name {
			case "status.json":
				terminal := record(raw["processTerminal"])
				if rid, ok := terminal["runnerProcessInstanceId"].(string); ok && raw["runId"] == id && terminal["runId"] == id {
					runnerID = &rid
				}
				var steps any
				if arr, ok := raw["steps"].([]any); ok {
					list := []any{}
					for i, s := range arr {
						if i >= 16 {
							break
						}
						list = append(list, known(record(s)["status"], states))
					}
					steps = list
				}
				return name + ": " + jsEncode(merge(base, jsObj{{"runIdMatches", raw["runId"] == id}, {"pid", number(raw["pid"])}, {"state", known(raw["state"], states)}, {"lastUpdate", number(raw["lastUpdate"])}, {"endedAt", number(raw["endedAt"])}, {"steps", steps}, {"proof", proof(terminal)}}))
			case "process-terminal-candidate.json":
				var writers []any
				for _, index := range []string{"0", "1"} {
					var count any
					if arr, ok := record(raw["writers"])[index].([]any); ok {
						count = len(arr)
					}
					writers = append(writers, jsObj{{"index", index}, {"count", count}, {"expected", number(record(raw["expectedWriters"])[index])}})
				}
				return name + ": " + jsEncode(merge(base, identity(raw), jsObj{{"writers", writers}}))
			}
			return name + ": " + jsEncode(merge(base, proof(raw)))
		}())
	}
	return lines
}

func TestAsyncResultTimeoutEvidenceTwins(t *testing.T) {
	const f = "async-result-timeout-evidence"
	matches := func(t *testing.T, pattern, s string) {
		t.Helper()
		if !regexp.MustCompile(pattern).MatchString(s) {
			t.Errorf("evidence does not match %s:\n%s", pattern, s)
		}
	}
	jsonOf := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	tw(t, f, "correlates timeout evidence without promoting pending sidecars or exposing contents", func(t *testing.T) {
		dir := tmp(t)
		proof := map[string]any{"runId": "run", "runnerProcessInstanceId": "private-runner", "state": "pending"}
		writeFile(t, filepath.Join(dir, "status.json"), jsonOf(map[string]any{"runId": "run", "state": "running", "pid": 123, "processTerminal": proof, "steps": []any{map[string]any{"status": "complete"}, map[string]any{"status": "complete"}}, "task": "SECRET"}))
		writeFile(t, filepath.Join(dir, "process-terminal.json"), jsonOf(proof))
		candidate := map[string]any{"writers": map[string]any{}, "expectedWriters": map[string]any{"0": 0, "1": 0}}
		for k, v := range proof {
			candidate[k] = v
		}
		writeFile(t, filepath.Join(dir, "process-terminal-candidate.json"), jsonOf(candidate))
		var events []string
		for _, e := range []map[string]any{
			{"type": "subagent.step.completed", "runId": "run", "stepIndex": 1, "ts": 42, "output": "SECRET"},
			{"type": "subagent.parallel.completed", "runId": "run", "stepIndex": 0, "ts": 43},
			{"type": "subagent.run.completed", "runId": "other", "ts": 43},
		} {
			events = append(events, jsonOf(e))
		}
		writeFile(t, filepath.Join(dir, "events.jsonl"), strings.Join(events, "\n"))
		writeFile(t, filepath.Join(dir, "runner.stderr.log"), "SECRET")
		evidence := strings.Join(asyncResultTimeoutEvidence(dir, "run"), "\n")
		matches(t, `"state":"pending"`, evidence)
		matches(t, `"runnerIdMatches":true`, evidence)
		matches(t, `"mismatchedRunEvents":1`, evidence)
		matches(t, `"stepIndex":1`, evidence)
		matches(t, `subagent.parallel.completed`, evidence)
		matches(t, `runner-startup-proceed.json: ENOENT`, evidence)
		if regexp.MustCompile(`SECRET|private-runner|subagent.run.completed`).MatchString(evidence) {
			t.Errorf("evidence leaks: %s", evidence)
		}
		observed := map[string]any{"runId": "run", "state": "observed", "runnerProcessInstanceId": "other", "instances": []any{map[string]any{"kind": "runner", "processInstanceId": "other", "closeObservedAt": 99, "exitCode": 1, "signal": nil}}}
		writeFile(t, filepath.Join(dir, "process-terminal.json"), jsonOf(observed))
		mismatch := strings.Join(asyncResultTimeoutEvidence(dir, "run"), "\n")
		matches(t, `"runnerIdMatches":false`, mismatch)
		matches(t, `"closeObservedAt":99,"exitCode":1,"signal":null`, mismatch)
	})
	tw(t, f, "bounds timeout reads and output, and tolerates missing, malformed and oversized evidence", func(t *testing.T) {
		dir := tmp(t)
		writeFile(t, filepath.Join(dir, "status.json"), strings.Repeat("x", 20_000))
		writeFile(t, filepath.Join(dir, "process-terminal.json"), "{")
		writeFile(t, filepath.Join(dir, "process-terminal-candidate.json"), jsonOf(map[string]any{"runId": "run", "runnerProcessInstanceId": "private-runner"}))
		writeFile(t, filepath.Join(dir, "runner.stdout.log"), strings.Repeat("SECRET", 100_000))
		completed := jsonOf(map[string]any{"type": "subagent.run.completed", "runId": "run", "ts": 42})
		lines := make([]string, 100)
		for i := range lines {
			lines[i] = completed
		}
		writeFile(t, filepath.Join(dir, "events.jsonl"), strings.Repeat("SECRET", 3_000)+"\n"+strings.Join(lines, "\n"))
		evidence := strings.Join(asyncResultTimeoutEvidence(dir, "run"), "\n")
		matches(t, `oversize`, evidence)
		matches(t, `invalid JSON`, evidence)
		matches(t, `"tailOnly":true`, evidence)
		matches(t, `"runnerIdMatches":"unavailable"`, evidence)
		eq(t, strings.Count(evidence, "subagent.run.completed"), 16, "completed events")
		if len(evidence) >= 8192 {
			t.Errorf("evidence is %d bytes", len(evidence))
		}
		if strings.Contains(evidence, "SECRET") {
			t.Error("evidence leaks")
		}
	})
}

func TestAsyncExecutionSkips(t *testing.T) {
	const f = "async-execution"
	tskip(t, f, "propagates static parallel machine placement before agent pins and rejects group worktrees", "parallel groups, Herdr saved-machine placement and managed worktrees belong to the parallel and workflow slices; the port refuses `chain`, `tasks` and `worktree` launches (refuse.go) so no async runner step is ever built for them")
	tskip(t, f, "uses supplied discovery context for missing async agents", "buildAsyncRunnerSteps has no Go counterpart: a launch resolves its single agent before the run starts, and the agent-definition directory report this text lists belongs to the discovery slice (agent-refinements, not ported)")
	tskip(t, f, "uses the resolved cwd override for no-context missing-agent fallback discovery", "the no-context fallback discovery is a branch of buildAsyncRunnerSteps for chain steps; the port resolves the one agent from the launch cwd before starting a run")
	tskip(t, f, "loads launch rules from the resolved async step cwd", "subagents.watchdog.rules (denied models per role) belong to the watchdog and acceptance slice, not ported")
	tskip(t, f, "resolves async step tool budgets with step over run over agent over config precedence", "tool budgets (toolBudget, config toolBudget, waitToolEnabled per step) belong to the tool-budget slice, not ported; the port refuses a toolBudget launch")
	tskip(t, f, "carries the resolved model context window into async runner steps", "the original's runner step carries contextLimit for its in-process budget accounting; the pig child manages its own context and the port has no usage budget")
	tskip(t, f, "carries explicit nested fanout authorization into async runner steps", "allowNestedSubagents authorization is part of the capability-ceiling slice, not ported (the agent definition does not read it)")
	tskip(t, f, "assigns default and agent-level deadlines to async serial and parallel children", "parallel children are refused, and the agent-level defaultTimeoutMs frontmatter is not read by the Go agent definition; the default deadline of a single run is asserted by TestTimeoutAndCapacity")
	tskip(t, f, "uses agent tool budget before config default when no run override exists", "tool budgets (agent toolBudget over config toolBudget) belong to the tool-budget slice, not ported")
	tskip(t, f, "attaches external runner config and rejects unsupported Pi-only overrides", "external CLI runners (runner.type external-cli) are a separate slice (external-cli-runner, not ported); a launch of such an agent is refused")
	tskip(t, f, "uses config default when no step, run, or agent budget exists", "tool budgets (config toolBudget default) belong to the tool-budget slice, not ported")
}

func TestAsyncPermissionSessionSkips(t *testing.T) {
	const f = "async-permission-session"
	tskip(t, f, "uses the parent session id for permission forwarding instead of the async status identity", "permission forwarding (PI_SUBAGENT_PARENT_SESSION and the permissionRules in a runner step) is carried by Pi's permission system; PiG's permission extension does not read these variables, and the pig child records the parent session id in status.json (asserted by TestOnlyCurrentSessionRunsAreControlled)")
	tskip(t, f, "consumes bounded dynamic fanout indexes before later static forked steps", "dynamic fan-out (expand/collect) and forked per-step session files belong to the workflow slice, not ported")
	tskip(t, f, "applies thinking overrides to the resolved async model", "thinkingOverridesByFlatIndex belongs to forked multi-step launches; the single-run `model:level` suffix override is covered by the run twins")
}
