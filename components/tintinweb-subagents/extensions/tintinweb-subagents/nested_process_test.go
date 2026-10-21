//go:build !windows

package tintinweb_subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Nested delegation across real processes: the top-level extension starts a fake pig that itself delegates (nested_fakepig_test.go),
// two and three levels deep. Twins of nested-delegation-e2e.test.ts and subagents-nested-print-mode-e2e.test.ts, which run the
// original on Pi with a faux model; what they pin (the tools a child is offered, the answer travelling up, the depth cap,
// ownership, a stop reaching down) is read here from the tree of processes.

const (
	e2eFile       = "nested-delegation-e2e"
	printModeFile = "subagents-nested-print-mode-e2e"
	workerMarker  = "WORKER-REACHED-THE-TOP"
)

// processRig is a top-level session whose agents are real processes (the fake pig), with the script they follow.
type processRig struct {
	*rig
	logDir string
	cwd    string
}

func startProcessRig(t *testing.T, agents map[string]string, script nestScript) *processRig {
	t.Helper()
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	useFakePig(t, "nest")
	logDir := t.TempDir()
	b, _ := json.Marshal(script)
	scriptPath := filepath.Join(t.TempDir(), "script.json")
	if err := os.WriteFile(scriptPath, b, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_PIG_SCRIPT", scriptPath)
	t.Setenv("FAKE_PIG_LOGDIR", logDir)
	cwd := t.TempDir()
	for name, extra := range agents {
		writeAgentFile(t, cwd, name, extra)
	}
	resetTypes()
	t.Cleanup(resetTypes)
	r := &rig{fleet: &fleet{}, extBus: map[string][]string{}, goBus: map[string][]func(any){}}
	e := sdk.New("tintinweb-subagents")
	r.app = newApp(e, startRPCChild)
	r.app.register(e)
	r.Host = StartHost(t, e, HostOptions{Cwd: cwd, OnCallValue: r.answer})
	r.Fire("session_start", obj{"reason": "startup"})
	t.Cleanup(func() { r.app.mgr.abortAll() })
	return &processRig{rig: r, logDir: logDir, cwd: cwd}
}

// logs are the processes that started, outermost first.
func (p *processRig) logs(t *testing.T) []nestLog {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(p.logDir, "*.json"))
	var out []nestLog
	for _, f := range files {
		b, _ := os.ReadFile(f)
		var l nestLog
		if json.Unmarshal(b, &l) == nil {
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Depth < out[j].Depth })
	return out
}

// waitLogs waits until n processes have started.
func (p *processRig) waitLogs(t *testing.T, n int) []nestLog {
	t.Helper()
	for i := 0; i < 1000; i++ {
		if l := p.logs(t); len(l) >= n {
			return l
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("fewer than %d processes started: %+v", n, p.logs(t))
	return nil
}

func logOf(t *testing.T, logs []nestLog, prompt string) nestLog {
	t.Helper()
	for _, l := range logs {
		if l.Prompt == prompt {
			return l
		}
	}
	t.Fatalf("no process was given the prompt %q: %+v", prompt, logs)
	return nestLog{}
}

func hasArg(l nestLog, arg string) bool { return slices.Contains(l.Args, arg) }

func toolsArg(l nestLog) string {
	if i := slices.Index(l.Args, "--tools"); i >= 0 && i+1 < len(l.Args) {
		return l.Args[i+1]
	}
	return ""
}

func processGone(t *testing.T, pid int) bool {
	t.Helper()
	for i := 0; i < 1000; i++ {
		if !alive(pid) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func killLeftovers(t *testing.T, logs []nestLog) {
	t.Helper()
	for _, l := range logs {
		if alive(l.Pid) {
			if p, err := os.FindProcess(l.Pid); err == nil {
				_ = p.Kill()
			}
		}
	}
}

func call(name string, args obj) nestStep { return nestStep{Tool: name, Args: args} }

const nestedNames = "Agent,get_subagent_result,steer_subagent"

func TestNestedDelegationAcrossProcessesTwins(t *testing.T) {
	tw(t, e2eFile, "a child with allowed_subagents spawns its own child, and the output travels back up", func(t *testing.T) {
		p := startProcessRig(t, map[string]string{
			"orchestrator": "extensions: false\nallowed_subagents: worker\n",
			"worker":       "extensions: false\n",
		}, nestScript{
			"Delegate this downward.": {Steps: []nestStep{call("Agent", obj{"subagent_type": "worker", "description": "leaf work", "prompt": "Do the leaf work."})}, Answer: "orchestrator saw: "},
			"Do the leaf work.":       {Answer: workerMarker},
		})
		text := p.must("Agent", obj{"subagent_type": "orchestrator", "description": "delegate", "prompt": "Delegate this downward.", "run_in_background": false})
		// Two hops home: worker -> orchestrator -> parent.
		eq(t, strings.Contains(text, "orchestrator saw: "+workerMarker), true)
		logs := p.waitLogs(t, 2)
		orch, worker := logOf(t, logs, "Delegate this downward."), logOf(t, logs, "Do the leaf work.")
		// The opted-in child is offered the nested tools, though its file says `extensions: false`: they are admitted by
		// the tool list, and the extension that carries them is loaded.
		eq(t, orch.ToolsEnabled, true)
		eq(t, strings.HasSuffix(toolsArg(orch), nestedNames), true)
		eq(t, hasArg(orch, "--no-extensions"), false)
		eq(t, orch.Depth, "1")
		eq(t, orch.MaxDepth, "2")
		eq(t, orch.Allowed, `["worker"]`)
		eq(t, orch.ID, p.app.mgr.list()[0].ID) // the owner of its children is the agent itself
		// The leaf never opted in, so it has not.
		eq(t, worker.ToolsEnabled, false)
		eq(t, worker.Allowed, "")
		eq(t, worker.Depth, "2")
		eq(t, slices.Contains(strings.Split(toolsArg(worker), ","), "Agent"), false)
		eq(t, hasArg(worker, "--no-extensions"), true) // `extensions: false` and nothing to carry
	})

	tw(t, e2eFile, "backgrounds a nested child, polls it by id, and streams its transcript", func(t *testing.T) {
		// The transcript half of the original case is the nested tools' (nested_extension_test.go: filed under the root
		// session); the background spawn, the id and the poll from inside a child are what runs here.
		p := startProcessRig(t, map[string]string{
			"orchestrator": "extensions: false\nallowed_subagents: worker\n",
			"worker":       "extensions: false\n",
		}, nestScript{
			"Delegate this downward.": {Steps: []nestStep{
				call("Agent", obj{"subagent_type": "worker", "description": "leaf work", "prompt": "Do the leaf work.", "run_in_background": true}),
				call("get_subagent_result", obj{"agent_id": "$id", "wait": true}),
			}, Answer: "orchestrator polled: "},
			"Do the leaf work.": {Answer: workerMarker},
		})
		text := p.must("Agent", obj{"subagent_type": "orchestrator", "description": "delegate", "prompt": "Delegate this downward.", "run_in_background": false})
		eq(t, strings.Contains(text, "orchestrator polled: "), true)
		eq(t, strings.Contains(text, "Nested agent started in background. Agent ID: "), true)
		eq(t, strings.Contains(text, workerMarker), true)
	})

	tw(t, printModeFile, "does not inject nested orchestration tools into a custom agent by default", func(t *testing.T) {
		p := startProcessRig(t, map[string]string{"plain": ""}, nestScript{"plain-child": {Answer: "PLAIN_CHILD_RESULT"}})
		text := p.must("Agent", obj{"subagent_type": "plain", "description": "plain child", "prompt": "plain-child", "run_in_background": false})
		eq(t, strings.Contains(text, "PLAIN_CHILD_RESULT"), true)
		l := logOf(t, p.waitLogs(t, 1), "plain-child")
		eq(t, l.ToolsEnabled, false)
		eq(t, l.Allowed, "")
		eq(t, l.ID, "")
		for _, n := range strings.Split(nestedNames, ",") {
			eq(t, slices.Contains(strings.Split(toolsArg(l), ","), n), false)
		}
	})

	tw(t, printModeFile, "strips nested tools at the depth cap instead of injecting always-failing ones", func(t *testing.T) {
		// Default maxSubagentDepth is 2: main(0) -> level_one(1) -> level_two(2). Nested tools go to an agent only
		// while its depth < max, so the agent at the cap never sees them even when it opts in via allowed_subagents.
		p := startProcessRig(t, map[string]string{
			"level_one":   "allowed_subagents: level_two\n",
			"level_two":   "allowed_subagents: level_three\n",
			"level_three": "",
		}, nestScript{
			"level_one-child":   {Steps: []nestStep{call("Agent", obj{"subagent_type": "level_two", "description": "allowed level", "prompt": "level_two-child"})}},
			"level_two-child":   {Answer: "AT_CAP"},
			"level_three-child": {Answer: "UNEXPECTED_LEVEL_THREE"},
		})
		text := p.must("Agent", obj{"subagent_type": "level_one", "description": "recursive chain", "prompt": "level_one-child", "run_in_background": false})
		eq(t, strings.Contains(text, "AT_CAP"), true)
		logs := p.waitLogs(t, 2)
		one, two := logOf(t, logs, "level_one-child"), logOf(t, logs, "level_two-child")
		eq(t, one.ToolsEnabled, true)
		eq(t, strings.Contains(toolsArg(one), nestedNames), true)
		// The cap agent was given no allowance and no tools, though its file asked for them.
		eq(t, two.ToolsEnabled, false)
		eq(t, two.Allowed, "")
		eq(t, hasArg(two, "--tools") && strings.Contains(toolsArg(two), "Agent"), false)
		eq(t, len(p.logs(t)), 2) // level_three never started
	})

	tw(t, printModeFile, "holds a background child while it performs real nested delegation", func(t *testing.T) {
		p := startProcessRig(t, map[string]string{
			"background_delegator":  "allowed_subagents: background_grandchild\n",
			"background_grandchild": "",
		}, nestScript{
			"background-delegator-child":  {Steps: []nestStep{call("Agent", obj{"subagent_type": "background_grandchild", "description": "nested foreground work", "prompt": "background-grandchild-child"})}},
			"background-grandchild-child": {Steps: []nestStep{{SleepMs: 50}}, Answer: "BACKGROUND_NESTED_RESULT"},
		})
		started := p.must("Agent", obj{"subagent_type": "background_delegator", "description": "background nested work", "prompt": "background-delegator-child", "run_in_background": true})
		id := idFrom(t, started)
		got := p.must("get_subagent_result", obj{"agent_id": id, "wait": true})
		eq(t, strings.Contains(got, "BACKGROUND_NESTED_RESULT"), true)
		logs := p.waitLogs(t, 2)
		eq(t, len(logs), 2) // one delegator, one grandchild, each started once
	})

	tw(t, printModeFile, "keeps nested get and steer ownership-scoped to the calling parent", func(t *testing.T) {
		// The owner stays running so its nested child is live while a peer probes it: the probe's ownership denial is what is
		// read. Each level has a manager of its own, so what another agent owns is not even there to be found.
		p := startProcessRig(t, map[string]string{
			"owner":       "allowed_subagents: owned_child\n",
			"probe":       "allowed_subagents: all\n",
			"owned_child": "",
		}, nestScript{
			"owner-child": {Steps: []nestStep{
				call("Agent", obj{"subagent_type": "owned_child", "description": "owned nested child", "prompt": "owned-child", "run_in_background": true}),
				{Write: "nested-id", Value: "$id"},
			}, Hold: true},
			"owned-child": {Hold: true},
			"probe-child": {Steps: []nestStep{
				{WaitFile: "nested-id"},
				call("get_subagent_result", obj{"agent_id": "$id"}),
				call("steer_subagent", obj{"agent_id": "$id", "message": "foreign guidance"}),
			}, Answer: "OWNERSHIP_PROBE_DONE\n"},
		})
		owner := p.must("Agent", obj{"subagent_type": "owner", "description": "nested record owner", "prompt": "owner-child", "run_in_background": true})
		probe := p.must("Agent", obj{"subagent_type": "probe", "description": "foreign ownership probe", "prompt": "probe-child", "run_in_background": true})
		got := p.must("get_subagent_result", obj{"agent_id": idFrom(t, probe), "wait": true})
		eq(t, strings.Contains(got, "OWNERSHIP_PROBE_DONE"), true)
		nestedID, _ := os.ReadFile(filepath.Join(p.logDir, "nested-id"))
		eq(t, len(nestedID) > 0, true)
		eq(t, strings.Contains(got, `Nested agent not found or not owned by this parent: "`+string(nestedID)+`".`), true)
		eq(t, strings.Contains(got, `Running nested agent not found or not owned by this parent: "`+string(nestedID)+`".`), true)
		eq(t, strings.Contains(got, "Steering message sent"), false)
		// The owner (and its nested child) are still live: the top-level session still has a running agent.
		ownerRec := p.app.mgr.get(idFrom(t, owner))
		eq(t, ownerRec.Status, statusRunning)
		logs := p.waitLogs(t, 3)
		defer killLeftovers(t, logs)
		// Stopping the owner takes the child it owns down with it.
		owned := logOf(t, logs, "owned-child")
		eq(t, alive(owned.Pid), true)
		eq(t, p.app.mgr.abort(ownerRec), true)
		eq(t, processGone(t, owned.Pid), true)
	})
}

func TestStoppingAnAgentStopsWhatItOwns(t *testing.T) {
	hold := nestScript{
		"orchestrate": {Steps: []nestStep{call("Agent", obj{"subagent_type": "worker", "description": "held", "prompt": "hold", "run_in_background": true})}, Hold: true},
		"hold":        {Hold: true},
	}
	agents := map[string]string{"orchestrator": "allowed_subagents: worker\n", "worker": ""}
	t.Run("a stop of the parent reaches the grandchild", func(t *testing.T) {
		p := startProcessRig(t, agents, hold)
		orch := p.app.mgr.get(idFrom(t, p.must("Agent", obj{"subagent_type": "orchestrator", "description": "d", "prompt": "orchestrate", "run_in_background": true})))
		logs := p.waitLogs(t, 2)
		defer killLeftovers(t, logs)
		worker := logOf(t, logs, "hold")
		eq(t, alive(worker.Pid), true)
		eq(t, p.app.mgr.abort(orch), true)
		<-orch.done
		eq(t, processGone(t, worker.Pid), true)
		eq(t, processGone(t, logOf(t, logs, "orchestrate").Pid), true)
	})
	t.Run("the end of the session reaches the grandchild", func(t *testing.T) {
		p := startProcessRig(t, agents, hold)
		p.must("Agent", obj{"subagent_type": "orchestrator", "description": "d", "prompt": "orchestrate", "run_in_background": true})
		logs := p.waitLogs(t, 2)
		defer killLeftovers(t, logs)
		worker := logOf(t, logs, "hold")
		p.app.mgr.abortAll() // session_shutdown
		eq(t, processGone(t, worker.Pid), true)
	})
	t.Run("a parent that settles stops the background child it still owns", func(t *testing.T) {
		// "a detached child is stopped when you finish". upstream: abortOwnedChildren.
		p := startProcessRig(t, agents, nestScript{
			"orchestrate": {Steps: []nestStep{call("Agent", obj{"subagent_type": "worker", "description": "held", "prompt": "hold", "run_in_background": true})}, Answer: "parent done"},
			"hold":        {Hold: true},
		})
		text := p.must("Agent", obj{"subagent_type": "orchestrator", "description": "d", "prompt": "orchestrate", "run_in_background": false})
		eq(t, strings.Contains(text, "parent done"), true)
		logs := p.waitLogs(t, 2)
		defer killLeftovers(t, logs)
		eq(t, processGone(t, logOf(t, logs, "hold").Pid), true)
	})
}

// testSpendReachesTheTopThroughTheChain runs a chain three processes deep with a branch cap of 3, every process spending
// 42 tokens: the top-level record counts all of them. upstream: nested-tools.ts onAssistantUsage walks every ancestor.
func testSpendReachesTheTopThroughTheChain(t *testing.T) {
	t.Helper()
	p := startProcessRig(t, map[string]string{
		"top": "allowed_subagents: middle\n", "middle": "allowed_subagents: leaf\n", "leaf": "",
	}, nestScript{
		"top-prompt":    {Steps: []nestStep{call("Agent", obj{"subagent_type": "middle", "description": "d", "prompt": "middle-prompt"})}},
		"middle-prompt": {Steps: []nestStep{call("Agent", obj{"subagent_type": "leaf", "description": "d", "prompt": "leaf-prompt"})}},
		"leaf-prompt":   {Answer: "leaf"},
	})
	setMaxSubagentDepth(3) // after the rig: it resets the cap
	p.must("Agent", obj{"subagent_type": "top", "description": "d", "prompt": "top-prompt", "run_in_background": false})
	rec := p.app.mgr.list()[0]
	p.app.mgr.mu.Lock()
	tokens := getLifetimeTotal(rec.Usage)
	p.app.mgr.mu.Unlock()
	eq(t, tokens, 3*42)
	eq(t, len(p.logs(t)), 3)
}
