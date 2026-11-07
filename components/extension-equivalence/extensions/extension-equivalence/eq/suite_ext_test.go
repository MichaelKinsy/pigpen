package eq

import (
	"strings"
	"testing"
)

func TestExpandRepeats(t *testing.T) {
	out, err := expandRepeats([]byte(`{"a":"x{{repeat:o:5}}y {{repeat: :2}}z"}`))
	if err != nil || string(out) != `{"a":"xoooooy   z"}` {
		t.Fatalf("got %q, %v", out, err)
	}
	if _, err := expandRepeats([]byte(`{{repeat:o:99999999}}`)); err == nil {
		t.Fatal("an absurd count must be refused")
	}
}

func TestCollapseRuns(t *testing.T) {
	long := strings.Repeat("é", 100)
	cases := map[string]string{
		"short":                                           "short",
		"a" + strings.Repeat("x", 64) + "b":               "a<run x x64>b",
		strings.Repeat("x", 63) + "y":                     strings.Repeat("x", 63) + "y",
		long + "!" + long:                                 "<run é x100>!<run é x100>",
		"H " + strings.Repeat("r", 1000):                  "H <run r x1000>",
		strings.Repeat("a", 70) + strings.Repeat("b", 10): "<run a x70>bbbbbbbbbb",
	}
	for in, want := range cases {
		if got := collapseRuns(in, 64); got != want {
			t.Errorf("collapseRuns(%.20q) = %q, want %q", in, got, want)
		}
	}
}

func TestCollapseRunsRejectsTinyThreshold(t *testing.T) {
	s := &Scenario{CollapseRuns: 10}
	if err := s.validateSuiteExt(); err == nil {
		t.Fatal("a threshold that collapses ordinary text must be refused")
	}
}

func TestExpandBuiltins(t *testing.T) {
	if got := expandBuiltins("{{cwd}}|{{agent}}|{{sessions}}|{{var:x}}", "/c", "/a", "/s"); got != "/c|/a|/s|{{var:x}}" {
		t.Fatal(got)
	}
}

// A trigger saves a value of its command's response and releases the held turn that quotes it: the turn is
// expanded after the release, so the value is there; the response stays out of the trace.
func TestTriggerSavesAValueForTheHeldTurn(t *testing.T) {
	tr := runFeatureLane(t, `{"name":"trigsave","settleMs":1,"tailMs":-1,
	  "llm":[{"text":"quotes {{var:first}}","hold":"go"}],
	  "steps":[{"rpc":{"type":"prompt","message":"x"},
	    "triggers":[{"when":{"llmRequest":1},"rpc":{"type":"get_entries"},"quiet":true,"save":{"first":"data.entries.0.id"},"release":["go"]}],
	    "captureFiles":["received.log"]}]}`)
	if e, failed := tr.Failed(); failed {
		t.Fatalf("lane failed: %s", e.Data)
	}
	if indexOf(tr, ChResponse, `"command":"get_entries"`) >= 0 {
		t.Error("the quiet trigger's response is in the trace")
	}
	if got := events(tr, ChFiles); len(got) != 1 || !strings.Contains(got[0], "get_entries") {
		t.Errorf("the trigger's command did not reach the host: %q", got)
	}
}

func TestTriggerSaveNeedsAnRPCAndAKnownPath(t *testing.T) {
	for name, body := range map[string]string{
		"no rpc":   `{"name":"a","llm":[{"text":"a","hold":"h"}],"steps":[{"rpc":{"type":"prompt","message":"x"},"triggers":[{"when":{"llmRequest":1},"release":["h"],"save":{"v":"data.x"}}]}]}`,
		"bad name": `{"name":"a","llm":[{"text":"a","hold":"h"}],"steps":[{"rpc":{"type":"prompt","message":"x"},"triggers":[{"when":{"llmRequest":1},"rpc":{"type":"get_state"},"save":{"1v":"data.x"}}]}]}`,
	} {
		if _, err := loadSC(t, body); err == nil {
			t.Errorf("%s: want a load error", name)
		}
	}
}

func TestSystemDiffInsertedKeepsOnlyTheAddedLines(t *testing.T) {
	v, ok := systemDeltaMode(SystemDiffInserted, "a\nb\nc", "a\nc\nNEW")
	if !ok {
		t.Fatal("an added line is a delta")
	}
	hs := v.(map[string]any)["hunks"].([]any)
	if len(hs) != 1 || hs[0].(map[string]any)["inserted"] != "NEW" || len(hs[0].(map[string]any)) != 1 {
		t.Fatalf("%v", hs)
	}
	if _, ok := systemDeltaMode(SystemDiffInserted, "a\nb\nc", "a\nc"); ok {
		t.Fatal("host wording the request lacks is not compared")
	}
	if _, ok := systemDeltaMode(SystemDiffInserted, "same", "same"); ok {
		t.Fatal("equal prompts have no delta")
	}
}
