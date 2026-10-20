package powerline_footer

import (
	"strings"
	"testing"
)

// renderLastPromptLines (index.ts 3091-3115).

func TestLastPromptRender(t *testing.T) {
	s := newLastPromptState()
	if got := s.render(80); got != nil {
		t.Errorf("no prompt: %q", got)
	}
	s.prompt = "  fix\t the \n bug  "
	got := s.render(80)
	if want := " \x1b[38;5;244m↳\x1b[0m \x1b[38;5;244mfix the bug\x1b[0m"; len(got) != 1 || got[0] != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if again := s.render(80); len(again) != 1 || &again[0] != &got[0] {
		t.Error("the same prompt at the same width must come from the cache")
	}

	s.show = false
	if got := s.render(80); got != nil {
		t.Errorf("showLastPrompt off: %q", got)
	}
	s.show = true

	s.prompt = "   \n\t "
	if got := s.render(80); got != nil {
		t.Errorf("a blank prompt shows nothing: %q", got)
	}
}

func TestLastPromptNeedsRoomForTheText(t *testing.T) {
	s := newLastPromptState()
	s.prompt = "hello world"
	prefixWidth := visibleWidth(" \x1b[38;5;244m↳\x1b[0m ") // 3
	if got := s.render(prefixWidth + 9); got != nil {
		t.Errorf("9 free cells: %q", got)
	}
	got := s.render(prefixWidth + 10)
	if len(got) != 1 || visibleWidth(got[0]) > prefixWidth+10 || !strings.Contains(got[0], "hello wor\x1b[0m…") {
		t.Errorf("10 free cells: %q", got)
	}
	if got := s.render(prefixWidth + 11); len(got) != 1 || !strings.Contains(got[0], "hello world") || strings.Contains(got[0], "…") {
		t.Errorf("11 free cells fit the prompt: %q", got)
	}
}

func TestLastPromptCacheFollowsTheInputs(t *testing.T) {
	s := newLastPromptState()
	s.prompt = "one"
	first := s.render(40)
	s.prompt = "two"
	if got := s.render(40); got[0] == first[0] {
		t.Error("a new prompt reused the old lines")
	}
	narrow := s.render(20)
	wide := s.render(60)
	if len(narrow) != 1 || len(wide) != 1 {
		t.Fatal("no lines")
	}
	s.prompt = strings.Repeat("x", 100)
	if got := s.render(20); visibleWidth(got[0]) > 20 {
		t.Errorf("not cut to the width: %q", got)
	}
}

func TestShowLastPromptSetting(t *testing.T) {
	for name, c := range map[string]struct {
		settings map[string]any
		want     bool
	}{
		"unset":      {map[string]any{}, true},
		"true":       {map[string]any{"showLastPrompt": true}, true},
		"false":      {map[string]any{"showLastPrompt": false}, false},
		"zero":       {map[string]any{"showLastPrompt": float64(0)}, true}, // `!== false`: only false turns it off
		"null":       {map[string]any{"showLastPrompt": nil}, true},
		"the string": {map[string]any{"showLastPrompt": "false"}, true},
	} {
		if got := showLastPromptSetting(c.settings); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}

func TestLastPromptWidgetEnabled(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	for name, c := range map[string]struct {
		env  map[string]string
		want bool
	}{
		"nothing set":                           {nil, true},
		"the switch alone without the profiler": {map[string]string{"POWERLINE_PERF_LAST_PROMPT": "0"}, true},
		"profiler on":                           {map[string]string{"POWERLINE_DEBUG_PERF": "1"}, true},
		"profiler, last prompt off":             {map[string]string{"POWERLINE_DEBUG_PERF": "1", "POWERLINE_PERF_LAST_PROMPT": "0"}, false},
		"profiler, widgets off":                 {map[string]string{"POWERLINE_DEBUG_PERF": "1", "POWERLINE_PERF_WIDGETS": "0"}, false},
		"profiler, other value":                 {map[string]string{"POWERLINE_DEBUG_PERF": "1", "POWERLINE_PERF_LAST_PROMPT": "1"}, true},
	} {
		if got := lastPromptWidgetEnabled(env(c.env)); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}

func TestCollapseSpaces(t *testing.T) {
	for in, want := range map[string]string{"": "", "  a  b\n\tc ": "a b c", "\u00a0x\u2003y\u3000": "x y", "ab": "ab"} {
		if got := collapseSpaces(in); got != want {
			t.Errorf("collapseSpaces(%q) = %q, want %q", in, got, want)
		}
	}
}
