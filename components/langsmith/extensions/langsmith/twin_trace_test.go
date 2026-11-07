package langsmith_test

import (
	"strings"
	"testing"
)

// Twins of test/trace.test.ts: the four recordings replayed through the fake host, the trees posted to the loopback
// LangSmith endpoint compared with upstream's expected trees (testdata/ext_expected_trees.json holds the asTree blocks
// of the TypeScript source, evaluated by node).

func replayTree(t *testing.T, recording string, o rigOptions) (tree, *rig) {
	t.Helper()
	events := readRecording(t, recording)
	o.Cwd = recordingCwd(events)
	r := newRig(t, o)
	r.replay(events)
	r.flush()
	return assumedTree(t, r.ls.Calls()), r
}

func TestTraceTwins(t *testing.T) {
	trees := loadExpectedTrees(t)

	tw(t, "trace", "openai responses", func(t *testing.T) {
		got, _ := replayTree(t, "oai-responses-tool-calls.jsonl", rigOptions{})
		requireTree(t, got, trees["openai responses"])
	})

	tw(t, "trace", "anthropic", func(t *testing.T) {
		got, _ := replayTree(t, "anthropic-tool-calls.jsonl", rigOptions{})
		requireTree(t, got, trees["anthropic"])
		for name, run := range got.Data {
			if !strings.HasPrefix(name, "anthropic:") {
				continue
			}
			if inputs, ok := run["inputs"].(map[string]any); !ok {
				t.Fatalf("%s has no inputs", name)
			} else if _, has := inputs["system"]; has {
				t.Fatalf("%s: inputs must not carry the Anthropic system field", name)
			}
		}
	})

	tw(t, "trace", "gemini", func(t *testing.T) {
		got, _ := replayTree(t, "gemini-tool-calls.jsonl", rigOptions{})
		requireTree(t, got, trees["gemini"])
	})

	tw(t, "trace", "overflow compaction stays in the prompt's trace", func(t *testing.T) {
		got, _ := replayTree(t, "faux-overflow-compaction.jsonl", rigOptions{})
		requireTree(t, got, trees["overflow compaction stays in the prompt's trace"])
		if _, has := got.Data["Pi agent run:3"]["error"]; has {
			t.Fatal("the overflow retry stays in the prompt's trace without an error on the root")
		}
	})
}
