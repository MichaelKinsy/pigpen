package pi_subagents

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goldenToolTexts are the results of the `subagent` tool calls the ORIGINAL answered under Pi (testdata/*.jsonl, copies of
// port/host-bound/golden). The one thing that cannot be the same is the Path line of a builtin agent: the original reports its
// install directory (recorded as <oracle>), the port its embedded file.
func goldenToolTexts(t *testing.T, name string) []string {
	t.Helper()
	f, err := os.Open("testdata/" + name)
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
			texts = append(texts, strings.Replace(e.Data.Result.Content[0].Text, "<oracle>/agents/", "builtin/", 1))
		}
	}
	return texts
}

func TestGetBuiltinMatchesOriginal(t *testing.T) {
	texts := goldenToolTexts(t, "get-builtin.jsonl")
	if len(texts) != 2 {
		t.Fatalf("recorded results: %d, want 2", len(texts))
	}
	withTempHome(t)
	for i, name := range []string{"worker", "claude-code"} {
		got := getAgent(tmp(t), map[string]any{"agent": name})
		if got.isError || got.text != texts[i] {
			t.Errorf("%s:\n got %q\nwant %q", name, got.text, texts[i])
		}
	}
}

// The pigeq scenario manage-reset (update, get, reset and get of a builtin agent): its two `get` answers print the install path.
func TestManageResetMatchesOriginal(t *testing.T) {
	texts := goldenToolTexts(t, "manage-reset.jsonl")
	if len(texts) != 4 {
		t.Fatalf("recorded results: %d, want 4 (update, get, reset, get)", len(texts))
	}
	withTempHome(t)
	cwd := tmp(t)
	writeFile(t, filepath.Join(cwd, ".pi", "keep"), "")
	ctx := testMgmtCtx(cwd)
	steps := []struct {
		action string
		params map[string]any
	}{
		{"update", map[string]any{"agent": "scout", "config": map[string]any{"model": "claude-x"}, "agentScope": "project"}},
		{"get", map[string]any{"agent": "scout"}},
		{"reset", map[string]any{"agent": "scout", "agentScope": "project"}},
		{"get", map[string]any{"agent": "scout"}},
	}
	for i, st := range steps {
		got := handleManagementAction(st.action, st.params, ctx)
		if got.text != texts[i] {
			t.Errorf("%s %d:\n got %q\nwant %q", st.action, i, got.text, texts[i])
		}
	}
}
