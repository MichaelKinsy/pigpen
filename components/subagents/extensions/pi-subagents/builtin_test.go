package pi_subagents

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The `get` of a builtin agent against what the ORIGINAL printed under Pi (testdata/get-builtin.jsonl, a copy of port/host-bound/golden). The one line that
// cannot be the same is Path: the original reports its install directory, the port its embedded file.
func TestGetBuiltinMatchesOriginal(t *testing.T) {
	f, err := os.Open("testdata/get-builtin.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var texts []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var e struct {
			Ch   string `json:"ch"`
			Data struct {
				Type     string `json:"type"`
				ToolName string `json:"toolName"`
				Result   struct {
					Content []struct{ Text string } `json:"content"`
				} `json:"result"`
			} `json:"data"`
		}
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Ch == "host" && e.Data.Type == "tool_execution_end" && e.Data.ToolName == "subagent" {
			texts = append(texts, e.Data.Result.Content[0].Text)
		}
	}
	if len(texts) != 2 {
		t.Fatalf("recorded results: %d, want 2", len(texts))
	}
	withHome := withTempHome
	withHome(t)
	for i, name := range []string{"worker", "claude-code"} {
		want := strings.Replace(texts[i], "<oracle>/agents/", "builtin/", 1)
		got := getAgent(tmp(t), map[string]any{"agent": name})
		if got.isError || got.text != want {
			t.Errorf("%s:\n got %q\nwant %q", name, got.text, want)
		}
	}
}
