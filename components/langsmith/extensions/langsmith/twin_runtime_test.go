package langsmith_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Twins of test/late-settle.test.ts and test/legacy-runtime.test.ts: the host's Pi VERSION gates agent_settled.
// The original mocks `VERSION` of @earendil-works/pi-coding-agent; the port reads it from the host (sdk.Version()).

func TestLateSettleTwin(t *testing.T) {
	trees := loadExpectedTrees(t)
	tw(t, "late-settle", "ignores a late agent_settled from the previous prompt", func(t *testing.T) {
		events := readRecording(t, "faux-overflow-compaction.jsonl")
		// Simulate another extension's `agent_settled` handler starting the next prompt: our handler only sees the
		// first prompt's settle once the second prompt has reached `turn_start`.
		settledIdx := -1
		for i, ev := range events {
			if ev.Event == "agent_settled" {
				settledIdx = i
				break
			}
		}
		settled := events[settledIdx]
		events = append(events[:settledIdx:settledIdx], events[settledIdx+1:]...)
		turnStartIdx := -1
		for i := settledIdx; i < len(events); i++ {
			if events[i].Event == "turn_start" {
				turnStartIdx = i
				break
			}
		}
		moved := append([]recordedEvent(nil), events[:turnStartIdx+1]...)
		moved = append(moved, settled)
		moved = append(moved, events[turnStartIdx+1:]...)

		r := newRig(t, rigOptions{PiVersion: "0.86.1", Cwd: recordingCwd(moved)})
		r.replay(moved)
		r.flush()
		got := assumedTree(t, r.ls.Calls())
		requireTree(t, got, trees["ignores a late agent_settled from the previous prompt"])

		// The first prompt keeps its own result rather than being marked replaced.
		first := got.Data["Pi agent run:0"]
		if _, has := first["error"]; has {
			t.Fatal("the first prompt must not be marked replaced")
		}
		if _, has := first["outputs"].(map[string]any)["messages"]; !has {
			t.Fatal("the first prompt keeps its loop outputs")
		}
		// The second prompt is ended by its own settle, not the stale one.
		second := got.Data["Pi agent run:3"]
		if _, has := second["error"]; has {
			t.Fatal("the second prompt has no error")
		}
		outputs := second["outputs"].(map[string]any)
		if _, has := outputs["messages"]; !has {
			t.Fatal("the second prompt keeps its loop outputs")
		}
		if _, has := outputs["incomplete"]; has {
			t.Fatal("the second prompt is not incomplete")
		}
	})
}

func TestLegacyRuntimeTwin(t *testing.T) {
	trees := loadExpectedTrees(t)
	tw(t, "legacy-runtime", "still ends the trace at agent_end on Pi without agent_settled", func(t *testing.T) {
		events := readRecording(t, "faux-overflow-compaction.jsonl")
		r := newRig(t, rigOptions{PiVersion: "0.80.3", Cwd: recordingCwd(events)})
		r.replay(events)
		r.flush()
		requireTree(t, assumedTree(t, r.ls.Calls()), trees["still ends the trace at agent_end on Pi without agent_settled"])
	})
}

// Twins of test/contract.test.ts. `it.each(["root", "subagent", "middleware", "compaction"])` has a parametrised title
// ("supports the %s agent type") that pigeq's ledger does not list; TestAgentTypes covers its four cases.

func gitRepo(t *testing.T, remote string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "a@b.c"}, {"config", "user.name", "n"}, {"config", "commit.gpgsign", "false"}, {"remote", "add", "origin", remote}, {"commit", "-q", "--allow-empty", "-m", "m"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v %s", args, err, out)
		}
	}
	return dir
}

// driveOneTurn is contract.test.ts driveOneTurn: root -> turn -> llm + tool.
func driveOneTurn(r *rig) {
	h := r.h
	h.Fire("before_agent_start", withPromptOptions("before_agent_start", jsonOf(r.t, `{"prompt":"hello","images":[]}`)))
	h.Fire("turn_start", jsonOf(r.t, `{"turnIndex":0,"timestamp":1}`))
	h.Fire("before_provider_request", jsonOf(r.t, `{"payload":{"messages":[{"role":"user","content":"hi"}]}}`))
	h.Fire("message_end", jsonOf(r.t, `{"message":{"role":"assistant","stopReason":"stop","content":[{"type":"text","text":"ok"}],"usage":{"input":10,"output":5,"totalTokens":15}}}`))
	h.Fire("tool_execution_start", jsonOf(r.t, `{"toolCallId":"call_1","toolName":"bash","args":{"command":"ls"}}`))
	h.Fire("tool_execution_end", jsonOf(r.t, `{"toolCallId":"call_1","toolName":"bash","result":{"content":"files"},"isError":false}`))
	h.Fire("turn_end", jsonOf(r.t, `{"turnIndex":0,"message":{"role":"assistant","stopReason":"stop","content":[]},"toolResults":[]}`))
	h.Fire("agent_end", jsonOf(r.t, `{"messages":[{"role":"assistant","stopReason":"stop","content":[]}]}`))
}

func metaOf(t *testing.T, got tree, prefix string) map[string]any {
	t.Helper()
	for _, name := range got.Nodes {
		if strings.HasPrefix(name, prefix) {
			extra, _ := got.Data[name]["extra"].(map[string]any)
			md, _ := extra["metadata"].(map[string]any)
			return md
		}
	}
	t.Fatalf("no run found for %q (have: %v)", prefix, got.Nodes)
	return nil
}

func TestContractTwins(t *testing.T) {
	const threadID = "sess_contract_test_123"
	tw(t, "contract", "emits the coding-agent-v1 contract on every run type", func(t *testing.T) {
		cwd := gitRepo(t, "git@github.com:langchain-ai/langsmith-pi-extension.git")
		r := newRig(t, rigOptions{Cwd: cwd, SessionID: threadID})
		driveOneTurn(r)
		r.flush()
		got := assumedTree(t, r.ls.Calls())

		root, turn, llm, tool := metaOf(t, got, "Pi agent run:"), metaOf(t, got, "Pi turn 0:"), metaOf(t, got, "anthropic:"), metaOf(t, got, "bash:")
		for i, md := range []map[string]any{root, turn, llm, tool} {
			eq := func(k string, want any) {
				t.Helper()
				if md[k] != want {
					t.Errorf("run %d: %s = %v, want %v", i, k, md[k], want)
				}
			}
			eq("ls_agent_purpose", "coding")
			eq("ls_agent_type", "root")
			eq("ls_integration", "pi")
			eq("ls_agent_runtime", "Pi")
			eq("ls_trace_schema_version", "coding-agent-v1")
			eq("thread_id", threadID)
			if _, has := md["ls_agent_kind"]; has {
				t.Errorf("run %d: ls_agent_kind must be undefined", i)
			}
			if _, ok := md["ls_integration_version"].(string); !ok {
				t.Errorf("run %d: ls_integration_version", i)
			}
			if _, ok := md["ls_agent_runtime_version"].(string); !ok {
				t.Errorf("run %d: ls_agent_runtime_version", i)
			}
			if _, ok := md["cwd"].(string); !ok {
				t.Errorf("run %d: cwd", i)
			}
			for _, k := range []string{"approval_policy", "ls_subagent_id", "ls_subagent_type"} {
				if _, has := md[k]; has {
					t.Errorf("run %d: scope-restricted key %s leaked", i, k)
				}
			}
		}

		// Git/repo: the test repository has a remote, a branch and a commit.
		if _, ok := root["git_branch"].(string); !ok {
			t.Error("git_branch")
		}
		if _, ok := root["git_commit_sha"].(string); !ok {
			t.Error("git_commit_sha")
		}
		if root["repository_provider"] != "github" || root["repository_url"] != "https://github.com/langchain-ai/langsmith-pi-extension" || root["repository_name"] != "langchain-ai/langsmith-pi-extension" {
			t.Errorf("repository facts: %v", root)
		}
		// Git/repo propagates to descendants.
		if llm["git_commit_sha"] != root["git_commit_sha"] || tool["repository_name"] != root["repository_name"] {
			t.Error("git facts must propagate")
		}

		// turn_number: the 1-based USER turn, stamped on the root and inherited.
		for i, md := range []map[string]any{root, turn, llm, tool} {
			if md["turn_number"] != float64(1) {
				t.Errorf("run %d: turn_number = %v", i, md["turn_number"])
			}
		}
		// The inner-loop index is preserved separately, never as turn_number.
		if turn["pi_loop_index"] != float64(0) {
			t.Errorf("pi_loop_index = %v", turn["pi_loop_index"])
		}

		// Preserved model/tool conventions.
		if llm["ls_provider"] != "anthropic" {
			t.Errorf("ls_provider = %v", llm["ls_provider"])
		}
		if _, ok := llm["ls_model_name"].(string); !ok {
			t.Error("ls_model_name")
		}
		if llm["ls_model_name"] != "claude opus 4.8" {
			t.Errorf("the model name is lower-cased: %v", llm["ls_model_name"])
		}
		if llm["usage_metadata"] == nil {
			t.Error("usage_metadata")
		}
		// ls_tool_name omitted: pi's run name already IS the native tool name.
		if _, has := tool["ls_tool_name"]; has {
			t.Error("ls_tool_name")
		}
	})

	tw(t, "contract", "increments turn_number per user prompt within a thread", func(t *testing.T) {
		r := newRig(t, rigOptions{SessionID: threadID, Cwd: os.TempDir()})
		// Two user prompts, each a minimal root -> agent_end cycle.
		for _, prompt := range []string{"first", "second"} {
			r.h.Fire("before_agent_start", withPromptOptions("before_agent_start", map[string]any{"prompt": prompt, "images": []any{}}))
			r.h.Fire("agent_end", jsonOf(t, `{"messages":[{"role":"assistant","stopReason":"stop","content":[]}]}`))
		}
		r.flush()
		got := assumedTree(t, r.ls.Calls())
		var numbers []any
		for _, name := range got.Nodes {
			if strings.HasPrefix(name, "Pi agent run:") {
				numbers = append(numbers, metaOf(t, got, name)["turn_number"])
			}
		}
		if len(numbers) != 2 || numbers[0] != float64(1) || numbers[1] != float64(2) {
			t.Fatalf("turn numbers %v, want [1 2]", numbers)
		}
	})
}

// Twins of test/host-options.test.ts. All three drive the TypeScript embedding options of LangSmithExtensionOptions
// (`client`, `config`, `getCurrentRunTree`) and the langsmith JS SDK's AsyncLocalStorage (`withRunTree`, `traceable`):
// a Go Piglet has no embedding entry point (lane-ext.md, named exclusion E1).

func TestHostOptionsSkips(t *testing.T) {
	tskip(t, "host-options", "enables tracing from an options-provided config",
		"passes `{ client, config: { enabled: true } }` to the default export; the Go Piglet has no options argument (exclusion E1). Its two effects are twinned through the real sources: TestConfigDefaultProjectApplies (the `pi-coding-agent` default project reaches session_name) and TestEnabledFromConfigFile (tracing enabled without any environment variable)")
	tskip(t, "host-options", "nests agent runs under a host-provided parent run tree",
		"needs `options.getCurrentRunTree` returning a RunTree built by the embedding host (exclusion E1); a Go Piglet has no host code that can hand it a run tree. The no-parent half is TestRootRunsAreNewTraces")
	tskip(t, "host-options", "nests agent runs under the current traceable run",
		"needs the langsmith JS SDK's AsyncLocalStorage (`traceable` + getCurrentRunTree(true)) around the extension call; Go has no such ambient context and the Go port never has a current run (exclusion E1, TestRootRunsAreNewTraces)")
}
