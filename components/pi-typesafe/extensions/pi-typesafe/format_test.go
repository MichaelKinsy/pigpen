package pi_typesafe

import (
	"strings"
	"testing"
)

func TestToFixed3IsJavaScriptsToFixed(t *testing.T) {
	// 0.0625 is exactly representable: JavaScript rounds the tie up, Go's formatting rounds it to even.
	cases := map[float64]string{0.9: "0.900", 0.0625: "0.063", 1: "1.000", 0.0005: "0.001", 2.5: "2.500", -0.0625: "-0.063", 0: "0.000", 12.3456: "12.346"}
	for in, want := range cases {
		if got := toFixed3(in); got != want {
			t.Errorf("toFixed3(%v) = %s, want %s", in, got, want)
		}
	}
}

func TestFormatFollowsTheOriginalLayoutAndQuestionOrder(t *testing.T) {
	details := map[string]any{
		"model": "jev-test", "elapsedMs": 12.0, "usage": map[string]any{"input_tokens": 42.0, "output_tokens": 0.0},
		"order": []any{"z", "a", "s"},
		"answers": map[string]any{
			"a": map[string]any{"type": "choice", "choice": "billing", "confidence": 1.0, "probabilities": map[string]any{"billing": 1.0, "other": 0.0}},
			"z": map[string]any{"type": "noul", "noul": 0.9},
			"s": map[string]any{"type": "score", "score": 1.5, "confidence": 0.5, "probabilities": map[string]any{"0": 0.25, "1": 0.5, "10": 0.25}},
		},
	}
	want := strings.Join([]string{
		`TypeSafe · "jev-test" · 12 ms`,
		`"z": P(yes) = 0.900`,
		`"a": "billing" · confidence 1.000`,
		`  {"billing":1,"other":0}`,
		`"s": 1.500 · confidence 0.500`,
		`  {"0":0.25,"1":0.5,"10":0.25}`,
		`42 input / 0 output tokens`,
		`Confidence is distribution concentration, not proof of correctness.`,
	}, "\n")
	if got := format(details, true); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if got := format(details, false); strings.Contains(got, `{"billing"`) {
		t.Error("collapsed output must not list probabilities")
	}
	delete(details, "order")
	if got := format(details, false); !strings.HasPrefix(strings.Split(got, "\n")[1], `"a"`) {
		t.Errorf("without order the ids are sorted: %s", got)
	}
}

func TestWrapTextKeepsEveryLineWithinWidth(t *testing.T) {
	text := "TypeSafe · a fairly long line with unbreakable_" + strings.Repeat("x", 60) + " and 🙂 wide cells\n\nnext"
	for _, width := range []int{10, 40, 80} {
		for _, line := range wrapText(text, width) {
			if visibleWidth(line) > width {
				t.Errorf("width %d: %q is %d cells", width, line, visibleWidth(line))
			}
		}
	}
	if got := wrapText("a b", 80); len(got) != 1 || got[0] != "a b" {
		t.Errorf("short text = %v", got)
	}
}

func TestDisclosureNamesTheDestination(t *testing.T) {
	if !strings.Contains(Disclosure("typesafe"), "api.typesafe.ai") {
		t.Error("the TypeSafe disclosure must name api.typesafe.ai")
	}
	own := Disclosure("ownmodel")
	if !strings.Contains(own, "Nothing is sent to api.typesafe.ai") || !strings.Contains(own, "model PiG is configured with") {
		t.Errorf("own-model disclosure = %s", own)
	}
	if !strings.Contains(ToolDescription("ownmodel"), own) || !strings.Contains(ToolDescription("typesafe"), Disclosure("typesafe")) {
		t.Error("the tool description carries the disclosure of its backend")
	}
}
