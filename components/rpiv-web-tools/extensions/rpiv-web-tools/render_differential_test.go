package rpiv_web_tools

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

// ansiTheme colors as the generator's theme does (port/gen-render-differential.mjs): every call wraps its text in SGR sequences.
type ansiTheme struct{}

var themeColors = map[string]int{"toolTitle": 33, "accent": 36, "dim": 90, "warning": 33, "success": 32, "muted": 37}

func (ansiTheme) Fg(name, text string) string {
	n, ok := themeColors[name]
	if !ok {
		n = 39
	}
	return fmt.Sprintf("\x1b[38;5;%dm%s\x1b[39m", n, text)
}
func (ansiTheme) Bold(text string) string { return "\x1b[1m" + text + "\x1b[22m" }

// TestRenderersMatchTheOriginalComponents compares the lines of the renderers with what the original's renderCall and
// renderResult return once Pi renders the Text component at the same width: the original's component is
// `new Text(text, 0, 0)`, so its lines are wrapped, tabs turned into three spaces and every line padded to the width.
func TestRenderersMatchTheOriginalComponents(t *testing.T) {
	data, err := os.ReadFile("testdata/render-differential.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Tool    string         `json:"tool"`
		Phase   string         `json:"phase"`
		Args    map[string]any `json:"args"`
		Options struct {
			Expanded  bool `json:"expanded"`
			IsPartial bool `json:"isPartial"`
		} `json:"options"`
		Result *struct {
			Content []map[string]any `json:"content"`
			Details map[string]any   `json:"details"`
		} `json:"result"`
		Rendered []struct {
			Width int      `json:"width"`
			Lines []string `json:"lines"`
		} `json:"rendered"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	th := ansiTheme{}
	for i, c := range cases {
		var text string
		switch {
		case c.Tool == "web_search" && c.Phase == "call":
			text = renderSearchCallText(c.Args, th)
		case c.Tool == "web_fetch" && c.Phase == "call":
			text = renderFetchCallText(c.Args, th)
		case c.Tool == "web_search":
			text = renderSearchResultText(c.Result.Details, c.Options.Expanded, c.Options.IsPartial, th)
		default:
			content, has := "", false
			if len(c.Result.Content) > 0 && c.Result.Content[0]["type"] == "text" {
				content, has = fmt.Sprint(c.Result.Content[0]["text"]), true
			}
			text = renderFetchResultText(c.Result.Details, content, has, c.Options.Expanded, c.Options.IsPartial, th)
		}
		for _, r := range c.Rendered {
			want := r.Lines
			if want == nil {
				want = []string{}
			}
			got := textLines(text, r.Width)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("case %d (%s %s) at width %d: text %q\n got  %q\n want %q", i, c.Tool, c.Phase, r.Width, strings.ReplaceAll(text, "\x1b", `\e`), got, want)
			}
		}
	}
}
