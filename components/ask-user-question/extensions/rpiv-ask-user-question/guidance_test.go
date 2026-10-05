package ask_user_question_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	ask "github.com/MichaelKinsy/pigpen/ask-user-question"
)

const fGuidance = "ask-user-question.guidance"

func writeConfig(t *testing.T, contents string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	if contents == "" {
		return
	}
	path := filepath.Join(home, ".config", "rpiv-ask-user-question", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func registered(t *testing.T) ToolDef {
	t.Helper()
	return startRig(t, rpcOpts()).ToolDef("ask_user_question")
}

func TestGuidance(t *testing.T) {
	tw(t, fGuidance, "describes the Type something row as appended to every question without stale fallback terms", func(t *testing.T) {
		joined := strings.Join(ask.DefaultPromptGuidelines(), "\n")
		for _, want := range []string{`automatically appended "Type something." row on every question`, "Esc to abandon"} {
			if !strings.Contains(joined, want) {
				t.Fatalf("guidelines lack %q", want)
			}
		}
		for _, stale := range []string{`"Other" free-text fallback`, "Chat about this"} {
			if strings.Contains(joined, stale) {
				t.Fatalf("guidelines contain %q", stale)
			}
		}
	})
	tw(t, fGuidance, "describes the all-question custom-answer contract in the registered tool", func(t *testing.T) {
		writeConfig(t, "")
		d := registered(t).Description
		for _, want := range []string{`automatically appended "Type something." row on every question`, "reserved labels are rejected at runtime"} {
			if !strings.Contains(d, want) {
				t.Fatalf("description lacks %q", want)
			}
		}
	})
	tw(t, fGuidance, "uses built-in defaults when no config file exists", func(t *testing.T) {
		writeConfig(t, "")
		d := registered(t)
		eq(t, d.PromptSnippet, ask.DefaultPromptSnippet(), "snippet")
		eq(t, d.PromptGuidelines, ask.DefaultPromptGuidelines(), "guidelines")
	})
	tw(t, fGuidance, "uses built-in defaults when config has no guidance field", func(t *testing.T) {
		writeConfig(t, `{"otherField":true}`)
		eq(t, registered(t).PromptSnippet, ask.DefaultPromptSnippet(), "snippet")
	})
	tw(t, fGuidance, "overrides promptSnippet with valid value", func(t *testing.T) {
		writeConfig(t, `{"guidance":{"promptSnippet":"Custom ask snippet"}}`)
		d := registered(t)
		eq(t, d.PromptSnippet, "Custom ask snippet", "snippet")
		eq(t, d.PromptGuidelines, ask.DefaultPromptGuidelines(), "guidelines")
	})
	tw(t, fGuidance, "overrides promptGuidelines with valid value", func(t *testing.T) {
		writeConfig(t, `{"guidance":{"promptGuidelines":["one","two"]}}`)
		d := registered(t)
		eq(t, d.PromptGuidelines, []string{"one", "two"}, "guidelines")
		eq(t, d.PromptSnippet, ask.DefaultPromptSnippet(), "snippet")
	})
	tw(t, fGuidance, "overrides both promptSnippet and promptGuidelines", func(t *testing.T) {
		writeConfig(t, `{"guidance":{"promptSnippet":"s","promptGuidelines":["g"]}}`)
		d := registered(t)
		eq(t, d.PromptSnippet, "s", "snippet")
		eq(t, d.PromptGuidelines, []string{"g"}, "guidelines")
	})
	tw(t, fGuidance, "falls back to defaults on empty promptSnippet", func(t *testing.T) {
		writeConfig(t, `{"guidance":{"promptSnippet":""}}`)
		eq(t, registered(t).PromptSnippet, ask.DefaultPromptSnippet(), "snippet")
	})
	tw(t, fGuidance, "falls back to defaults on wrong types", func(t *testing.T) {
		writeConfig(t, `{"guidance":{"promptSnippet":42,"promptGuidelines":"nope","description":false}}`)
		d := registered(t)
		eq(t, d.PromptSnippet, ask.DefaultPromptSnippet(), "snippet")
		eq(t, d.PromptGuidelines, ask.DefaultPromptGuidelines(), "guidelines")
		eq(t, d.Description, ask.DefaultToolDescription(), "description")
	})
	tw(t, fGuidance, "falls back to defaults on promptGuidelines with empty string item", func(t *testing.T) {
		writeConfig(t, `{"guidance":{"promptGuidelines":["ok",""]}}`)
		eq(t, registered(t).PromptGuidelines, ask.DefaultPromptGuidelines(), "guidelines")
	})
	tw(t, fGuidance, "overrides tool description with valid value", func(t *testing.T) {
		writeConfig(t, `{"guidance":{"description":"Custom ask tool description"}}`)
		d := registered(t)
		eq(t, d.Description, "Custom ask tool description", "description")
		eq(t, d.PromptSnippet, ask.DefaultPromptSnippet(), "snippet")
	})
	tw(t, fGuidance, "uses the built-in tool description when no config file exists", func(t *testing.T) {
		writeConfig(t, "")
		eq(t, registered(t).Description, ask.DefaultToolDescription(), "description")
	})
	tw(t, fGuidance, "falls back to the built-in tool description on empty description", func(t *testing.T) {
		writeConfig(t, `{"guidance":{"description":""}}`)
		eq(t, registered(t).Description, ask.DefaultToolDescription(), "description")
	})
	tw(t, fGuidance, "falls back to the built-in tool description on non-string description", func(t *testing.T) {
		writeConfig(t, `{"guidance":{"description":["x"]}}`)
		eq(t, registered(t).Description, ask.DefaultToolDescription(), "description")
	})
}

func TestDefaultCopyStatesTheLimits(t *testing.T) {
	eq(t, ask.DefaultPromptSnippet(), "Ask the user up to 4 structured questions (2-4 options each) when requirements are ambiguous", "snippet")
	eq(t, len(ask.DefaultPromptGuidelines()), 4, "guidelines")
}
