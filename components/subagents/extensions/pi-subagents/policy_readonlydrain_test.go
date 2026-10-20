package pi_subagents

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
)

// Twins of test/unit/readonly-drain-observation.test.ts. The original's first case drives the in-process child's hook factory
// (createCapturedChildHooks, observeReadonlyChildHookDrain: pi.on handlers of a child session built in the parent's process);
// the port's child is a pig process, so that case has no counterpart.

const rdFile = "/exact/child.jsonl"

func rdObservation() *ReadonlyDrainObservation {
	return newReadonlyDrainObservation(rdFile, func() bool {
		ps, _ := listBackgroundWorkProviders()
		return len(ps) == 0
	})
}

func rdState(session string) *SubagentState { return asyncState(session) }

// rdDrain runs the drain and returns its error, a panic of the wait (a thrown JavaScript error) included.
func rdDrain(deps AutoDrainDeps) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	return drainOutstandingWork(deps)
}

func TestReadonlyDrainObservation(t *testing.T) {
	const f = "readonly-drain-observation"
	tskip(t, f, "only the installed runtime drain can complete evidence, not acknowledgements, UI, wrong file or replacement installation",
		"drives the in-process child session's hook factory (createCapturedChildHooks and pi.on handlers of a session built inside the parent process); the port's child is a pig process with no such hooks")

	tw(t, f, "requires first false AND actual completion, with sticky first-true and second-episode denial", func(t *testing.T) {
		for _, active := range []bool{false, true} {
			proof := rdObservation()
			equal(t, proof.settled(), false, "fresh")
			proof.begin(rdFile, true)
			proof.predicate(active)
			equal(t, proof.check(), !active, "first true denies synchronously, before waiting or completion")
			equal(t, proof.settled(), false, "settled before completion")
			proof.predicate(false)
			proof.complete()
			equal(t, proof.settled(), !active, "settled after completion")
			proof.begin(rdFile, true)
			proof.predicate(false)
			proof.complete()
			equal(t, proof.settled(), false, "second episode")
		}
	})

	tw(t, f, "observes true before wait, preserves waits/errors/deadlines and never certifies injected dependencies", func(t *testing.T) {
		t.Setenv("PI_SUBAGENTS_TEMP_ROOT", t.TempDir())
		for _, mode := range []string{"success", "error", "throw", "timeout", "predicate throw", "false"} {
			proof := rdObservation()
			checks, waits := 0, 0
			var clock atomic.Int64
			err := rdDrain(AutoDrainDeps{State: rdState(rdFile), TimeoutMs: i64(10), Now: clock.Load, Observation: proof,
				HasWork: func(string, int64) (bool, error) {
					if mode == "predicate throw" {
						return false, errors.New("query failure")
					}
					first := false
					if mode != "false" {
						first = checks == 0
						checks++
					}
					return first || mode == "timeout", nil
				},
				Wait: func(_ WaitParams, _ <-chan struct{}, deps WaitDeps) WaitResult {
					waits++
					equal(t, proof.settled(), false, mode+": settled during the wait")
					equal(t, deps.FailOnFailedRuns, true, mode+": failOnFailedRuns")
					equal(t, deps.StopOnAttention != nil && !*deps.StopOnAttention, true, mode+": stopOnAttention")
					if mode == "throw" {
						panic("wait throw")
					}
					clock.Store(11)
					return WaitResult{Text: "done", IsError: mode == "error"}
				}})
			switch mode {
			case "error", "throw", "timeout", "predicate throw":
				if err == nil {
					t.Errorf("%s: the drain should fail", mode)
				} else {
					polMatches(t, err.Error(), `Auto-drain failed|wait throw|timed out|query failure`)
				}
			default:
				equal(t, err, nil, mode+": error")
			}
			want := 1
			if mode == "false" || mode == "predicate throw" {
				want = 0
			}
			equal(t, waits, want, mode+": waits")
			equal(t, proof.settled(), false, mode+": settled")
		}
		injections := map[string]AutoDrainDeps{
			"hasWork":                     {HasWork: func(string, int64) (bool, error) { return false, nil }},
			"wait":                        {Wait: func(WaitParams, <-chan struct{}, WaitDeps) WaitResult { return WaitResult{Text: "done"} }},
			"now":                         {Now: nowMs},
			"hasPendingSupervisorRequest": {HasPendingSupervisorRequest: func() bool { return false }},
		}
		for name, deps := range injections {
			proof := rdObservation()
			deps.State, deps.Observation = rdState(rdFile), proof
			equal(t, rdDrain(deps), nil, name+": error")
			equal(t, proof.settled(), false, name+": settled")
		}
	})

	tw(t, f, "requires exact identity, normal completion and a nonthrowing clean guard", func(t *testing.T) {
		t.Setenv("PI_SUBAGENTS_TEMP_ROOT", t.TempDir())
		for _, identity := range []string{"", "SDK-UUID", "/different.jsonl"} {
			proof := rdObservation()
			err := rdDrain(AutoDrainDeps{State: rdState(identity), Observation: proof})
			if identity == "" {
				if err == nil {
					t.Error("a missing identity must reject")
				} else {
					polMatches(t, err.Error(), `identity`)
				}
			} else {
				equal(t, err, nil, identity+": error")
			}
			equal(t, proof.settled(), false, identity+": settled")
		}
		broken := newReadonlyDrainObservation(rdFile, func() bool { panic("inspection") })
		equal(t, rdDrain(AutoDrainDeps{State: rdState(rdFile), Observation: broken}), nil, "broken guard")
		equal(t, broken.settled(), false, "broken guard settled")
	})

	kinds := []string{"empty", "dead-owned", "result-queued-owned", "result-running-owned", "dismissed-owned", "unrelated-running", "unrelated-dead", "terminal-owned", "unknown-owner", "unknown-state", "corrupt", "missing-status", "missing-directory", "rejected-entry", "rejected-entry-ENOENT", "rejected-entry-ENOTDIR", "unrelated-scale"}
	tw(t, f, "ordinary scan/read counts unchanged and conservative raw evidence: ${kind}", func(t *testing.T) {
		for _, kind := range kinds {
			t.Run(kind, func(t *testing.T) {
				observed, baseline := rdOrdinaryRun(t, kind, true), rdOrdinaryRun(t, kind, false)
				equal(t, observed, baseline, "observation preserves outcome and adds no index enumeration/status reads or metadata checks")
			})
		}
	})

	for _, throws := range []bool{false, true} {
		tw(t, f, "unknown provider veto precedes ordinary callbacks and survives removal (throws=${throws})", func(t *testing.T) {
			t.Setenv("PI_SUBAGENTS_TEMP_ROOT", t.TempDir())
			proof := rdObservation()
			calls := 0
			unregister, err := registerBackgroundWorkProvider(&BackgroundWorkProvider{Name: "drain-proof-provider", ListActiveWork: func(BackgroundWorkContext) ([]BackgroundWorkItem, error) {
				calls++
				equal(t, proof.check(), false, "the veto precedes the provider's callback")
				if throws {
					return nil, errors.New("provider failure")
				}
				return nil, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer unregister()
				equal(t, calls, 0, "no calls before the drain")
				err := rdDrain(AutoDrainDeps{State: rdState(rdFile), Observation: proof})
				if throws {
					if err == nil {
						t.Error("the provider failure must reject")
					} else {
						polMatches(t, err.Error(), `provider failure`)
					}
				} else {
					equal(t, err, nil, "error")
				}
				equal(t, calls > 0, true, "the provider was asked")
			}()
			equal(t, proof.settled(), false, "settled after the provider is removed")
		})
	}
}

type rdOutcome struct {
	Index, Read, Stat int
	Outcome           string
}

// rdOrdinaryRun builds one fixture of kind, drains it (with the observation when opted) and returns what the ordinary drain read
// and how it ended, after checking what the observation concluded.
func rdOrdinaryRun(t *testing.T, kind string, opted bool) rdOutcome {
	t.Helper()
	root := t.TempDir()
	asyncRoot, results := filepath.Join(root, "runs"), filepath.Join(root, "results")
	t.Setenv("PI_SUBAGENTS_TEMP_ROOT", filepath.Join(root, "temp"))
	rejectedEntry := strings.HasPrefix(kind, "rejected-entry")
	count := 1
	switch kind {
	case "unrelated-scale":
		count = 40
	case "empty":
		count = 0
	}
	var dirs []string
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("drain-proof-%s-%v-%d", kind, opted, i)
		dir := filepath.Join(asyncRoot, id)
		dirs = append(dirs, dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		state := "running"
		switch {
		case kind == "terminal-owned":
			state = "complete"
		case kind == "result-queued-owned":
			state = "queued"
		case kind == "unknown-state":
			state = "nonsense"
		}
		status := map[string]any{"runId": id, "mode": "single", "state": state, "startedAt": nowMs(), "steps": []any{map[string]any{"agent": "reader", "status": "running"}}}
		switch {
		case strings.HasPrefix(kind, "unrelated"):
			status["sessionId"] = "/other/file"
		case kind != "unknown-owner":
			status["sessionId"] = rdFile
		}
		if strings.Contains(kind, "dead") {
			status["pid"] = 2147483647
		}
		if kind == "dismissed-owned" {
			status["displayDismissedAt"] = 1
		}
		if kind != "missing-status" {
			body := "{"
			if kind != "corrupt" {
				b, _ := json.Marshal(status)
				body = string(b)
			}
			writeFile(t, filepath.Join(dir, "status.json"), body)
		}
		if err := updateActiveRunIndex(dir, "running", "", indexOptions{}); err != nil {
			t.Fatal(err)
		}
		if kind == "missing-directory" {
			if err := os.RemoveAll(dir); err != nil {
				t.Fatal(err)
			}
		}
		if rejectedEntry {
			if err := os.RemoveAll(dir); err != nil {
				t.Fatal(err)
			}
			writeFile(t, dir, "not a directory")
		}
		if strings.HasPrefix(kind, "result-") {
			writeFile(t, filepath.Join(results, id+".json"), `{"success":true,"results":[]}`)
		}
	}

	// The status stat of a regular file entry fails with ENOTDIR on POSIX systems and ENOENT on Windows: the native boundary is
	// exercised, and both contracts are injected deterministically.
	injected := map[string]error{
		"rejected-entry-ENOENT":  &fs.PathError{Op: "stat", Path: "status.json", Err: syscall.ENOENT},
		"rejected-entry-ENOTDIR": &fs.PathError{Op: "stat", Path: "status.json", Err: syscall.ENOTDIR},
	}[kind]
	boundary := fsErrorCode(injected)
	if rejectedEntry && injected == nil {
		_, err := os.Stat(filepath.Join(dirs[0], "status.json"))
		boundary = fsErrorCode(err)
		if boundary != "ENOENT" && boundary != "ENOTDIR" {
			t.Fatalf("the native boundary of a regular file entry is %q", boundary)
		}
	}

	var counts rdOutcome
	isStatus := func(p string) bool {
		for _, d := range dirs {
			if p == filepath.Join(d, "status.json") {
				return true
			}
		}
		return false
	}
	realStat, realRead, realDir := asyncStat, asyncReadFile, asyncReadDir
	seam(t, &asyncReadFile, func(p string) ([]byte, error) {
		if isStatus(p) {
			counts.Read++
		}
		return realRead(p)
	})
	seam(t, &asyncStat, func(p string) (os.FileInfo, error) {
		if isStatus(p) {
			counts.Stat++
			if injected != nil {
				return nil, injected
			}
		}
		return realStat(p)
	})
	seam(t, &asyncReadDir, func(p string) ([]os.DirEntry, error) {
		if p == filepath.Join(asyncRoot, ".active-runs") {
			counts.Index++
		}
		return realDir(p)
	})
	var proof *ReadonlyDrainObservation
	if opted {
		proof = rdObservation()
	}
	err := drainOutstandingWork(AutoDrainDeps{State: rdState(rdFile), AsyncDirRoot: asyncRoot, ResultsDir: results, Observation: proof})
	counts.Outcome = "completed"
	if err != nil {
		if !rejectedEntry {
			t.Fatalf("unexpected drain rejection: %v", err)
		}
		polMatches(t, err.Error(), `Failed to list async runs`)
		polMatches(t, err.Error(), `Failed to inspect async status file`)
		counts.Outcome = fsErrorCode(err)
	}
	want := "completed"
	if boundary == "ENOTDIR" {
		want = "ENOTDIR"
	}
	equal(t, counts.Outcome, want, "outcome")
	if rejectedEntry {
		equal(t, rdOutcome{Index: counts.Index, Read: counts.Read, Stat: counts.Stat}, rdOutcome{Index: 1, Read: 0, Stat: 1}, "a rejected entry reaches the cleanup status stat exactly once")
	}
	equal(t, counts.Index, 1, "the first predicate is false, with no wait or final requery")
	if proof != nil {
		settled := false
		for _, k := range []string{"empty", "unrelated-running", "unrelated-dead", "terminal-owned", "unrelated-scale"} {
			settled = settled || k == kind
		}
		equal(t, proof.settled(), settled, "settled")
	}
	read := func(dir string) string {
		b, err := os.ReadFile(filepath.Join(dir, "status.json"))
		if err != nil {
			t.Fatal(err)
		}
		var st map[string]any
		_ = json.Unmarshal(b, &st)
		s, _ := st["state"].(string)
		return s
	}
	switch {
	case kind == "dead-owned":
		equal(t, read(dirs[0]), "failed", "owned dead-PID repair remains")
	case kind == "unrelated-dead":
		equal(t, read(dirs[0]), "running", "session-scoped drain leaves foreign runs untouched")
	case strings.HasPrefix(kind, "result-"):
		equal(t, read(dirs[0]), "complete", "ordinary result repair remains")
	}
	return counts
}
